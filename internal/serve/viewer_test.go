package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeNestedBundle lays down a <root>/<host>/<slug>/ institution-nested
// bundle (the layout preserve.dirFor now produces).
func writeNestedBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []struct{ host, slug string }{
		{"iiif.bodleian.ox.ac.uk", "iiif_manifest_a.json"},
		{"gallica.bnf.fr", "iiif_ark_b_manifest.json"},
	} {
		dir := filepath.Join(root, p.host, p.slug)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		manifest := `{"@type":"sc:Manifest","sequences":[{"canvases":[{"images":[{"resource":{"@id":"https://` +
			p.host + `/img/x/full/full/0/default.jpg","service":{"@id":"https://` + p.host + `/img/x"}}}]}]}]}`
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		prov := `{"manifest_url":"https://` + p.host + `/m","images":[{"file":"0001.jpg",` +
			`"service_id":"https://` + p.host + `/img/x","source_url":"https://` + p.host + `/img/x/full/full/0/default.jpg"}]}`
		if err := os.WriteFile(filepath.Join(dir, "provenance.json"), []byte(prov), 0o600); err != nil {
			t.Fatalf("write provenance: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "0001.jpg"), []byte("\xff\xd8JPEG"), 0o600); err != nil {
			t.Fatalf("write jpg: %v", err)
		}
	}
	return root
}

// IIIF requires an Image API info.json `id` to equal the URL it is served
// from. The stored file has a placeholder, so serve must rewrite it.
func TestServer_InfoJSONIDRewrittenToRequestURL(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "inst", "slug", "0001")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stored := `{"@context":"http://iiif.io/api/image/3/context.json",` +
		`"id":"PLACEHOLDER","type":"ImageService3","profile":"level0","width":8,"height":8}`
	if err := os.WriteFile(filepath.Join(dir, "info.json"), []byte(stored), 0o600); err != nil {
		t.Fatalf("write info.json: %v", err)
	}

	ts := httptest.NewServer(New(root).Handler())
	defer ts.Close()

	code, body, ct := viewerGet(t, ts, "/inst/slug/0001/info.json")
	if code != 200 {
		t.Fatalf("GET info.json = %d", code)
	}
	if !strings.Contains(ct, "json") {
		t.Fatalf("info.json content-type = %q", ct)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("info.json invalid: %v\n%s", err, body)
	}
	want := ts.URL + "/inst/slug/0001"
	if got["id"] != want {
		t.Fatalf("info.json id = %v, want %s (the request URL base)", got["id"], want)
	}
	if got["profile"] != "level0" || got["type"] != "ImageService3" {
		t.Fatalf("other info.json fields not preserved: %v", got)
	}
}

// The editorial design serves its (offline, vendored) webfonts from the
// binary and the index references them — no CDN, works offline.
func TestServer_ServesEmbeddedFont(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()

	code, body, ctype := viewerGet(t, ts, "/__viewer__/fonts/newsreader-700.woff2")
	if code != http.StatusOK {
		t.Fatalf("GET font = %d, want 200", code)
	}
	if !strings.Contains(ctype, "font/woff2") {
		t.Fatalf("font content-type = %q, want font/woff2", ctype)
	}
	if len(body) < 4 || body[:4] != "wOF2" {
		t.Fatalf("font body is not woff2 (magic %q)", body[:min(len(body), 4)])
	}

	_, idx, _ := viewerGet(t, ts, "/")
	if !strings.Contains(idx, "@font-face") || !strings.Contains(idx, "Newsreader") {
		t.Fatalf("index does not adopt the editorial typography; body=%s", idx)
	}
	if !strings.Contains(idx, "/__viewer__/fonts/") {
		t.Fatalf("index does not reference the local font route; body=%s", idx)
	}
}

// The index shows per-manifest info, not just the slug: a title linking to
// the viewer, the institution linking to the original IIIF record, page
// count, and size status. Size indexing is deliberately asynchronous so a
// legacy library's tile tree never blocks this first response.
func TestServer_IndexShowsRichInfo(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()

	code, body, _ := viewerGet(t, ts, "/")
	if code != http.StatusOK {
		t.Fatalf("GET / = %d", code)
	}
	// Title from the manifest label (cookbook-v3 = "The Gulf Stream"),
	// still linking to the viewer.
	if !strings.Contains(body, "The Gulf Stream") {
		t.Fatalf("index missing manifest title; body=%s", body)
	}
	if !strings.Contains(body, `href="/cookbook-v3/"`) {
		t.Fatalf("title must still link to the viewer; body=%s", body)
	}
	// Institution links to the original IIIF record (provenance manifest_url).
	if !strings.Contains(body, `href="https://iiif.io/api/cookbook/recipe/0032-collection/manifest-01.json"`) {
		t.Fatalf("index missing IIIF-record link to manifest_url; body=%s", body)
	}
	// Page count (cookbook-v3 provenance has 1 image) and non-blocking size
	// status. A real Serve run fills and persists the exact MB value in the
	// background; a bare test Handler has not started that migration.
	if !strings.Contains(body, "1") || !strings.Contains(body, "size calculating…") {
		t.Fatalf("index missing page count / asynchronous size status; body=%s", body)
	}
	for _, want := range []string{`id="catalog-search"`, `id="catalog-sort"`, `data-pages=`, "Search library", "Page count"} {
		if !strings.Contains(body, want) {
			t.Fatalf("index missing catalogue search/sort control %q; body=%s", want, body)
		}
	}
}

// TestServer_NestedInstitutionLayout: with <host>/<slug>/ nesting the index
// must list each manifest by its full path, the viewer must serve at that
// nested path, and the manifest must still be localized (rewrite regression).
func TestServer_NestedInstitutionLayout(t *testing.T) {
	ts := httptest.NewServer(New(writeNestedBundle(t)).Handler())
	defer ts.Close()

	const a = "iiif.bodleian.ox.ac.uk/iiif_manifest_a.json"

	code, body, _ := viewerGet(t, ts, "/")
	if code != 200 {
		t.Fatalf("GET / = %d", code)
	}
	for _, want := range []string{
		`href="/iiif.bodleian.ox.ac.uk/iiif_manifest_a.json/"`,
		`href="/gallica.bnf.fr/iiif_ark_b_manifest.json/"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("index missing nested viewer link %q; body=%s", want, body)
		}
	}

	code, body, ct := viewerGet(t, ts, "/"+a+"/")
	if code != 200 || !strings.Contains(ct, "text/html") {
		t.Fatalf("viewer page at nested path = %d ct=%q", code, ct)
	}
	if !strings.Contains(body, "/"+a+"/manifest.json") || !strings.Contains(body, "Mirador.viewer") {
		t.Fatalf("nested viewer page not wired to its manifest; body=%s", body)
	}

	// Rewrite must still localize the nested manifest's image.
	code, body, _ = viewerGet(t, ts, "/"+a+"/manifest.json")
	if code != 200 {
		t.Fatalf("GET nested manifest = %d", code)
	}
	if !strings.Contains(body, "/"+a+"/0001.jpg") {
		t.Fatalf("nested manifest not localized; body=%s", body)
	}
	if strings.Contains(body, `"service"`) {
		t.Fatalf("nested manifest still has Image API service; body=%s", body)
	}
}

// viewerGet is a tiny GET helper returning status, body, and content-type.
func viewerGet(t *testing.T, ts *httptest.Server, path string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", path, err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("Content-Type")
}

// TestServer_IndexListsPreservedManifests: GET / is an HTML index linking
// every preserved dir that holds a manifest.json, so a researcher with no
// external viewer lands somewhere usable.
func TestServer_IndexListsPreservedManifests(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()

	code, body, ctype := viewerGet(t, ts, "/")
	if code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", code)
	}
	if !strings.Contains(ctype, "text/html") {
		t.Fatalf("GET / content-type = %q, want text/html", ctype)
	}
	// Must be our generated index, not the stdlib directory autoindex
	// (which would list provenance.json and emit a bare <pre> listing).
	if strings.Contains(body, "provenance.json") {
		t.Fatalf("index is the stdlib autoindex, not our viewer index; body=%s", body)
	}
	for _, dir := range []string{"cookbook-v3", "bodleian-c481"} {
		// Each preserved manifest links into its Mirador viewer page.
		if !strings.Contains(body, `href="/`+dir+`/"`) {
			t.Fatalf("index missing viewer link for %q; body=%s", dir, body)
		}
	}
}

// TestServer_ServesEmbeddedMiradorBundle: the Mirador 4 UMD bundle is served
// from the binary at a reserved prefix that cannot collide with a preserved
// dir slug.
func TestServer_ServesEmbeddedMiradorBundle(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()

	code, body, ctype := viewerGet(t, ts, "/__viewer__/mirador.min.js")
	if code != http.StatusOK {
		t.Fatalf("GET mirador bundle = %d, want 200", code)
	}
	if len(body) == 0 {
		t.Fatal("mirador bundle is empty")
	}
	if !strings.Contains(ctype, "javascript") {
		t.Fatalf("mirador bundle content-type = %q, want javascript", ctype)
	}
}

// The vendored bundle is our custom Mirador 4 + MAE build (rebuilt from
// source with the annotation editor folded in), not the stock viewer:
// region-drag annotation authoring depends on MAE being present. Guards
// against a regression to a plain Mirador bundle. ("add_a_rectangle" is a
// MAE-only i18n key; absent from stock Mirador. Verified red against the
// prior stock bundle.) The UMD global wrapper must still be intact.
func TestServer_BundleIsMAEAnnotationBuild(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()

	code, body, _ := viewerGet(t, ts, "/__viewer__/mirador.min.js")
	if code != http.StatusOK {
		t.Fatalf("GET mirador bundle = %d, want 200", code)
	}
	if !strings.Contains(body, "add_a_rectangle") {
		t.Fatal("bundle is not the MAE annotation build (missing MAE region tool); " +
			"region-drag annotation authoring will be unavailable")
	}
	if !strings.Contains(body, "ye.Mirador") && !strings.Contains(body, "Mirador=") {
		t.Fatal("custom build did not preserve the Mirador UMD global")
	}
}

// TestServer_ViewerPageEmbedsMiradorPointedAtLocalManifest: GET /<dir>/
// returns a Mirador page that loads the embedded bundle and instantiates the
// viewer against this dir's local (serve-time-rewritten) manifest.
// The viewer carries a left-side library rail listing every preserved
// manuscript so the user can switch documents without leaving the viewer.
func TestServer_ViewerHasLibraryRail(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()

	_, body, _ := viewerGet(t, ts, "/cookbook-v3/")

	if !strings.Contains(body, `class="library"`) {
		t.Fatalf("viewer missing the library rail; body=%s", body)
	}
	// Lists the OTHER preserved manuscript as a switchable link…
	if !strings.Contains(body, `href="/bodleian-c481/"`) {
		t.Fatalf("library rail does not list other manuscripts; body=%s", body)
	}
	// …and marks the one currently open.
	if !strings.Contains(body, `href="/cookbook-v3/"`) || !strings.Contains(body, "aria-current") {
		t.Fatalf("library rail does not mark the current manuscript; body=%s", body)
	}
	// Mirador still wired to THIS manuscript.
	if !strings.Contains(body, `data-manifest="/cookbook-v3/manifest.json"`) ||
		!strings.Contains(body, "Mirador.viewer") {
		t.Fatalf("library rail broke the Mirador wiring; body=%s", body)
	}
}

// Authoring is now done in-canvas via MAE (drag a region, edit in MAE's
// companion window). The legacy pure-Go "Annotate this manuscript"
// <details> form is removed: it duplicated MAE's affordance and confused
// the masthead. The viewer must NOT carry that form, and MAE/Mirador
// must still be wired.
func TestServer_ViewerHasNoLegacyAuthoringForm(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()

	_, body, _ := viewerGet(t, ts, "/cookbook-v3/")

	for _, gone := range []string{
		`id="annotate-form"`,
		`id="annotate-text"`,
		`id="annotate-canvas"`,
		`id="annotate-kind"`,
		"Annotate this manuscript",
	} {
		if strings.Contains(body, gone) {
			t.Fatalf("legacy pure-Go authoring form still present (%q); MAE is the authoring path now; body=%s", gone, body)
		}
	}
	for _, want := range []string{"/__viewer__/mirador.min.js", "Mirador.viewer"} {
		if !strings.Contains(body, want) {
			t.Fatalf("viewer no longer wires Mirador/MAE (missing %q); body=%s", want, body)
		}
	}
}

func TestServer_ViewerPageEmbedsMiradorPointedAtLocalManifest(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()

	code, body, ctype := viewerGet(t, ts, "/cookbook-v3/")
	if code != http.StatusOK {
		t.Fatalf("GET /cookbook-v3/ = %d, want 200", code)
	}
	if !strings.Contains(ctype, "text/html") {
		t.Fatalf("viewer page content-type = %q, want text/html", ctype)
	}
	if !strings.Contains(body, "/__viewer__/mirador.min.js") {
		t.Fatalf("viewer page does not load the embedded Mirador bundle; body=%s", body)
	}
	if !strings.Contains(body, "Mirador.viewer") {
		t.Fatalf("viewer page does not call Mirador.viewer; body=%s", body)
	}
	if !strings.Contains(body, "/cookbook-v3/manifest.json") {
		t.Fatalf("viewer page not pointed at the local manifest; body=%s", body)
	}
}

// Mirador 4 loads referenced annotations but, by default, leaves the
// sidebar closed and on-canvas highlights off — so a correctly stored
// and served note has nowhere to appear. The viewer must explicitly
// activate the annotations companion. Keys verified against the
// vendored bundle: sideBarOpenByDefault drives the initial open state,
// defaultSideBarPanel selects the panel, highlightAllAnnotations shows
// overlays without hover/selection.
func TestServer_ViewerActivatesAnnotationsPanel(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()

	code, body, _ := viewerGet(t, ts, "/cookbook-v3/")
	if code != http.StatusOK {
		t.Fatalf("GET /cookbook-v3/ = %d, want 200", code)
	}
	for _, want := range []string{
		"sideBarOpenByDefault: true",
		"defaultSideBarPanel: 'annotations'",
		"highlightAllAnnotations: true",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("viewer does not activate annotations panel: missing %q; body=%s", want, body)
		}
	}
}

// Mirador's default osdConfig.preserveViewport is true: on canvas switch it
// keeps the previous canvas's OSD viewport instead of re-homing. For a
// manuscript whose pages differ in size/orientation (e.g. Bodleian's mixed
// portrait/landscape leaves), every canvas after the first then renders
// into the first canvas's world bounds — a partial image. The viewer must
// disable viewport preservation so OSD re-homes per canvas.
func TestServer_ViewerDisablesViewportPreservation(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()

	code, body, _ := viewerGet(t, ts, "/cookbook-v3/")
	if code != http.StatusOK {
		t.Fatalf("GET /cookbook-v3/ = %d, want 200", code)
	}
	if !strings.Contains(body, "osdConfig") || !strings.Contains(body, "preserveViewport: false") {
		t.Fatalf("viewer does not disable OSD viewport preservation "+
			"(expected osdConfig.preserveViewport: false); body=%s", body)
	}
}
