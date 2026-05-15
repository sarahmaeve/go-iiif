package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

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
