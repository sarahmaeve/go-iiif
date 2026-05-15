package serve

import (
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

// TestServer_ViewerPageEmbedsMiradorPointedAtLocalManifest: GET /<dir>/
// returns a Mirador page that loads the embedded bundle and instantiates the
// viewer against this dir's local (serve-time-rewritten) manifest.
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
