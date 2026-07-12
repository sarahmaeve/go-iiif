package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyComparisonFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, slug := range []string{"cookbook-v3", "bodleian-c481"} {
		dst := filepath.Join(root, slug)
		if err := os.MkdirAll(dst, 0o750); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"manifest.json", "provenance.json"} {
			b, err := os.ReadFile(filepath.Join("testdata", "bundle", slug, name)) //nolint:gosec // fixed fixture
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, name), b, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func comparisonPath(docs ...string) string {
	q := url.Values{"doc": docs}
	return compareRoute + "?" + q.Encode()
}

func TestManifestCanvasIDsPresentation2And3(t *testing.T) {
	v2 := []byte(`{"sequences":[{"canvases":[{"@id":"https://example.org/canvas/1"},{"@id":"https://example.org/canvas/2"}]}]}`)
	v3 := []byte(`{"items":[{"id":"https://example.org/canvas/3","type":"Canvas"},{"id":"https://example.org/page/1","type":"AnnotationPage"}]}`)
	if got := manifestCanvasIDs(v2); strings.Join(got, ",") != "https://example.org/canvas/1,https://example.org/canvas/2" {
		t.Fatalf("v2 canvas IDs = %v", got)
	}
	if got := manifestCanvasIDs(v3); len(got) != 1 || got[0] != "https://example.org/canvas/3" {
		t.Fatalf("v3 canvas IDs = %v", got)
	}
	if got := manifestCanvasIDs([]byte(`not json`)); len(got) != 0 {
		t.Fatalf("invalid manifest canvas IDs = %v, want none", got)
	}
}

func TestServerComparisonRouteValidatesSelection(t *testing.T) {
	srv := New(filepath.Join("testdata", "bundle"))
	handler := srv.Handler()
	cases := []struct {
		name string
		docs []string
		want string
	}{
		{name: "too few", docs: []string{"cookbook-v3"}, want: "between 2 and 4"},
		{name: "too many", docs: []string{"a", "b", "c", "d", "e"}, want: "between 2 and 4"},
		{name: "duplicate", docs: []string{"cookbook-v3", "cookbook-v3"}, want: "duplicate"},
		{name: "unknown", docs: []string{"cookbook-v3", "missing"}, want: "unknown manuscript"},
		{name: "traversal", docs: []string{"cookbook-v3", "../bodleian-c481"}, want: "unsafe manuscript"},
		{name: "backslash", docs: []string{"cookbook-v3", `..\bodleian-c481`}, want: "unsafe manuscript"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, comparisonPath(tc.docs...), nil)
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("comparison = %d %q, want 400 containing %q", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, comparisonPath("cookbook-v3", "bodleian-c481"), nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST comparison = %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestComparisonRejectsRemovedBundleAndAmbiguousCanvasOwner(t *testing.T) {
	root := writeNestedBundle(t)
	srv := New(root)
	const first = "iiif.bodleian.ox.ac.uk/iiif_manifest_a.json"
	const second = "gallica.bnf.fr/iiif_ark_b_manifest.json"
	if err := os.Remove(filepath.Join(root, second, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.comparisonSelection([]string{first, second}); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("removed bundle error = %v", err)
	}

	// Restore both manifests with one globally ambiguous canvas ID. Choosing
	// either endpoint would risk a cross-bundle annotation write, so reject.
	const shared = `{"sequences":[{"canvases":[{"@id":"https://example.org/canvas/shared"}]}]}`
	for _, slug := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(root, slug, "manifest.json"), []byte(shared), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := srv.comparisonSelection([]string{first, second}); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("ambiguous canvas error = %v", err)
	}
}

func TestComparisonKeepsUnreadableManifestAsFailClosedWindow(t *testing.T) {
	root := writeNestedBundle(t)
	const first = "iiif.bodleian.ox.ac.uk/iiif_manifest_a.json"
	const second = "gallica.bnf.fr/iiif_ark_b_manifest.json"
	if err := os.WriteFile(filepath.Join(root, second, "manifest.json"), []byte(`not a manifest`), 0o600); err != nil {
		t.Fatal(err)
	}
	items, endpoints, err := New(root).comparisonSelection([]string{first, second})
	if err != nil || len(items) != 2 {
		t.Fatalf("selection = %v, %v", items, err)
	}
	if len(endpoints) != 0 { // writeNestedBundle canvases intentionally have no IDs; malformed contributes none.
		t.Fatalf("unreadable manifest contributed annotation routes: %v", endpoints)
	}
}

func TestServerComparisonUsesOrderedLocalManifestsAndCanvasRoutes(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()
	code, body, contentType := viewerGet(t, ts, comparisonPath("cookbook-v3", "bodleian-c481"))
	if code != http.StatusOK || !strings.Contains(contentType, "text/html") {
		t.Fatalf("comparison = %d ct=%q", code, contentType)
	}
	first := strings.Index(body, `"manifest":"/cookbook-v3/manifest.json"`)
	second := strings.Index(body, `"manifest":"/bodleian-c481/manifest.json"`)
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("local manifest order missing or wrong: first=%d second=%d body=%s", first, second, body)
	}
	if strings.Contains(body, `"manifest":"https://`) {
		t.Fatalf("remote source URL used as manifestId: %s", body)
	}
	for _, want := range []string{
		`"https://iiif.io/api/cookbook/recipe/0032-collection/manifest/1/canvas/p1":"/cookbook-v3/annotations"`,
		`"https://iiif.bodleian.ox.ac.uk/iiif/canvas/c85d87de-abd9-43b1-abf4-c65a814dc0a8.json":"/bodleian-c481/annotations"`,
		"strictRouting: true", "workspace: { type: 'mosaic' }", "sideBarOpenByDefault: true",
		"#mirador .mosaic-tile", "Copy comparison link", "Change selection",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("comparison lacks %q; body=%s", want, body)
		}
	}
}

func TestComparisonDeepLinksValidateCanvasOwnership(t *testing.T) {
	srv := New(filepath.Join("testdata", "bundle"))
	const cookbookCanvas = "https://iiif.io/api/cookbook/recipe/0032-collection/manifest/1/canvas/p1"
	const bodleianCanvas = "https://iiif.bodleian.ox.ac.uk/iiif/canvas/c85d87de-abd9-43b1-abf4-c65a814dc0a8.json"
	query := comparisonQuery([]string{"cookbook-v3", "bodleian-c481"}, []string{cookbookCanvas, bodleianCanvas})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, compareRoute+"?"+query.Encode(), nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deep comparison = %d %q", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"canvas":"` + cookbookCanvas + `"`, `"canvas":"` + bodleianCanvas + `"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("deep comparison lacks %q", want)
		}
	}

	bad := []string{
		comparisonPath("cookbook-v3", "bodleian-c481") + "&canvas=" + url.QueryEscape(bodleianCanvas),
		comparisonPath("cookbook-v3", "bodleian-c481") + "&canvas=&canvas=&canvas=extra",
	}
	for _, requestPath := range bad {
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, requestPath, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid deep link %s = %d %q", requestPath, rec.Code, rec.Body.String())
		}
	}
}

func TestSavedComparisonPersistsListsAndDeletes(t *testing.T) {
	root := copyComparisonFixture(t)
	srv := New(root)
	const canvas = "https://iiif.io/api/cookbook/recipe/0032-collection/manifest/1/canvas/p1"
	form := url.Values{
		"name": {"Hands and currents"}, "doc": {"cookbook-v3", "bodleian-c481"},
		"canvas": {canvas, ""},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, compareSaveRoute, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "saved=1") {
		t.Fatalf("save = %d Location=%q body=%q", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	sets := newComparisonStore(root).list()
	if len(sets) != 1 || sets[0].Name != "Hands and currents" || sets[0].Canvases[0] != canvas {
		t.Fatalf("saved sets = %+v", sets)
	}
	reopened := New(root)
	rec = httptest.NewRecorder()
	reopened.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	for _, want := range []string{"Saved comparisons", "Hands and currents", "canvas="} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("catalogue lacks saved comparison %q: %s", want, rec.Body.String())
		}
	}

	deleteForm := url.Values{"id": {sets[0].ID}}
	rec = httptest.NewRecorder()
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, compareDeleteRoute, strings.NewReader(deleteForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reopened.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || len(newComparisonStore(root).list()) != 0 {
		t.Fatalf("delete = %d sets=%v", rec.Code, newComparisonStore(root).list())
	}
}

func TestSavedComparisonDoesNotOverwriteCorruptState(t *testing.T) {
	root := copyComparisonFixture(t)
	stateDir := filepath.Join(root, catalogDirName)
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(stateDir, comparisonFileName)
	const corrupt = `{not recoverable`
	if err := os.WriteFile(statePath, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newComparisonStore(root)
	if _, err := store.add(savedComparison{Name: "Must not save", Docs: []string{"cookbook-v3", "bodleian-c481"}}); err == nil {
		t.Fatal("corrupt comparison state allowed a write")
	}
	b, err := os.ReadFile(statePath)
	if err != nil || string(b) != corrupt {
		t.Fatalf("corrupt comparison state changed: %q, %v", b, err)
	}
}

func TestSavedComparisonNamesAreUnique(t *testing.T) {
	store := newComparisonStore(t.TempDir())
	set := savedComparison{Name: "Related hands", Docs: []string{"a", "b"}}
	if _, err := store.add(set); err != nil {
		t.Fatal(err)
	}
	set.Name = " related HANDS "
	if _, err := store.add(set); !errors.Is(err, ErrComparisonNameExists) {
		t.Fatalf("duplicate-name error = %v", err)
	}
}

func TestComparisonTemplateOmitsSynchronizationFeatures(t *testing.T) {
	rec := httptest.NewRecorder()
	New(filepath.Join("testdata", "bundle")).Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, comparisonPath("cookbook-v3", "bodleian-c481"), nil))
	for _, want := range []string{
		"Mirador.getWindows", "history.replaceState", "saved-canvas",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("comparison workspace lacks %q", want)
		}
	}
	for _, unwanted := range []string{
		"sync-page", "sync-view", "Pair page position", "Sync zoom and pan",
		"Mirador.setCanvas", "Mirador.updateViewport", "Mirador.OSDReferences", "animation-finish", "getHomeBounds",
	} {
		if strings.Contains(rec.Body.String(), unwanted) {
			t.Fatalf("comparison workspace still contains synchronization feature %q", unwanted)
		}
	}
}

func TestComparisonCanvasRoutesKeepAnnotationsInOwningBundle(t *testing.T) {
	writable := copyComparisonFixture(t)
	srv := New(writable)
	items, endpoints, err := srv.comparisonSelection([]string{"cookbook-v3", "bodleian-c481"})
	if err != nil || len(items) != 2 || len(endpoints) != 2 {
		t.Fatalf("selection = %v endpoints=%v err=%v", items, endpoints, err)
	}
	for canvasID, endpoint := range endpoints {
		annotation := map[string]any{"type": "Annotation", "target": canvasID, "body": map[string]any{"type": "TextualBody", "value": endpoint}}
		body, _ := json.Marshal(annotation)
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(string(body)))
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST %s = %d %q", endpoint, rec.Code, rec.Body.String())
		}
	}
	for _, slug := range []string{"cookbook-v3", "bodleian-c481"} {
		b, err := os.ReadFile(filepath.Join(writable, slug, "annotations.json"))
		if err != nil {
			t.Fatal(err)
		}
		ownEndpoint := "/" + slug + "/annotations"
		otherSlug := "cookbook-v3"
		if slug == otherSlug {
			otherSlug = "bodleian-c481"
		}
		if !strings.Contains(string(b), ownEndpoint) || strings.Contains(string(b), "/"+otherSlug+"/annotations") {
			t.Fatalf("%s annotation store crossed bundle boundary: %s", slug, b)
		}
	}
}

func TestCatalogueHasAccessibleOrderedComparisonTray(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()
	_, body, _ := viewerGet(t, ts, "/")
	for _, want := range []string{
		`class="compare-add"`, `aria-pressed="false"`, `id="comparison-tray"`, `aria-live="polite"`,
		"Comparison is limited to four manuscripts", "Earlier", "Later", "Remove", "URLSearchParams", compareRoute,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("catalogue comparison flow lacks %q; body=%s", want, body)
		}
	}
}

func TestEmbeddedViewerHasStrictCanvasAnnotationRouting(t *testing.T) {
	bundle := string(miradorBundle)
	for _, want := range []string{"endpointByCanvas", "strictRouting", "ReadOnlyAnnotationAdapter", "Annotation canvas has no local owner"} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("embedded viewer lacks strict annotation routing marker %q", want)
		}
	}
}
