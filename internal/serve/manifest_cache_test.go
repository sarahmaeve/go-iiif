package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestServerManifestRewriteCacheAndInvalidation(t *testing.T) {
	root := writeNestedBundle(t)
	srv := New(root)
	const route = "/iiif.bodleian.ox.ac.uk/iiif_manifest_a.json/manifest.json"

	get := func(host string) string {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+host+route, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET manifest = %d %q", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	first := get("127.0.0.1:8443")
	second := get("127.0.0.1:8443")
	if first != second {
		t.Fatal("cached manifest response changed without source change")
	}
	srv.manifestCache.mu.RLock()
	entry := srv.manifestCache.entries["https://127.0.0.1:8443/iiif.bodleian.ox.ac.uk/iiif_manifest_a.json"]
	srv.manifestCache.mu.RUnlock()
	if len(entry.body) == 0 || entry.stamp == "" {
		t.Fatalf("manifest was not cached: %#v", entry)
	}

	provPath := filepath.Join(root, "iiif.bodleian.ox.ac.uk", "iiif_manifest_a.json", "provenance.json")
	prov, err := os.ReadFile(provPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provPath, append(prov, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = get("127.0.0.1:8443")
	srv.manifestCache.mu.RLock()
	updated := srv.manifestCache.entries["https://127.0.0.1:8443/iiif.bodleian.ox.ac.uk/iiif_manifest_a.json"]
	srv.manifestCache.mu.RUnlock()
	if updated.stamp == entry.stamp {
		t.Fatal("provenance change did not invalidate manifest cache")
	}

	other := get("localhost:8443")
	if other == first {
		t.Fatal("request base URL was not reflected in separately cached manifest")
	}
}
