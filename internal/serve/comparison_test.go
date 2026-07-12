package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestComparisonCanvasRoutesKeepAnnotationsInOwningBundle(t *testing.T) {
	root := filepath.Join("testdata", "bundle")
	// Work on a copy because this test deliberately creates annotation files.
	writable := t.TempDir()
	for _, slug := range []string{"cookbook-v3", "bodleian-c481"} {
		src := filepath.Join(root, slug)
		dst := filepath.Join(writable, slug)
		if err := os.MkdirAll(dst, 0o750); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"manifest.json", "provenance.json"} {
			b, err := os.ReadFile(filepath.Join(src, name)) //nolint:gosec // fixed test fixture
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, name), b, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
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
