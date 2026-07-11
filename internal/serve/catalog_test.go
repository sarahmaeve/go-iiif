package serve

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverBundlesStopsAtManifestRoot(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "institution", "manuscript")
	tile := filepath.Join(bundle, "0001", "tile-shaped-subtree")
	if err := os.MkdirAll(tile, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{filepath.Join(bundle, "manifest.json"), filepath.Join(tile, "manifest.json")} {
		if err := os.WriteFile(name, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got := discoverBundles(root)
	if len(got) != 1 || got[0].slug != "institution/manuscript" {
		t.Fatalf("discoverBundles = %#v, want only the outer preserved bundle", got)
	}
}

func TestServerCatalogRequestUsesMemoryIndex(t *testing.T) {
	root := writeNestedBundle(t)
	srv := New(root)
	away := root + ".away"
	if err := os.Rename(root, away); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(away, root) }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "iiif_manifest_a.json") {
		t.Fatalf("cached catalogue response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestServerCatalogEditPersistsTitleAndNotes(t *testing.T) {
	root := writeNestedBundle(t)
	const dir = "iiif.bodleian.ox.ac.uk/iiif_manifest_a.json"
	form := url.Values{
		"dir":   {dir},
		"title": {"The English Catalogue Title"},
		"notes": {"From John Dee's library.\nFormer shelfmark <Dee>."},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, catalogEditRoute, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	New(root).Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST catalogue edit = %d %q", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, catalogDirName, catalogFileName)); err != nil {
		t.Fatalf("catalogue sidecar not persisted: %v", err)
	}

	// A fresh Server proves the fields survive process restart.
	srv := New(root)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{"The English Catalogue Title", "From John Dee&#39;s library.", "Former shelfmark &lt;Dee&gt;."} {
		if !strings.Contains(body, want) {
			t.Fatalf("catalogue missing persisted/escaped field %q; body=%s", want, body)
		}
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/"+dir+"/", nil))
	if !strings.Contains(rec.Body.String(), "The English Catalogue Title — preserved") {
		t.Fatalf("viewer did not use catalogue title override; body=%s", rec.Body.String())
	}
}

func TestServerCatalogStateIsNotServedAsStaticFile(t *testing.T) {
	root := writeNestedBundle(t)
	if err := New(root).catalog.update("iiif.bodleian.ox.ac.uk/iiif_manifest_a.json", "title", "note"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	New(root).Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/"+catalogDirName+"/"+catalogFileName, nil))
	if rec.Code != http.StatusNotFound {
		b, _ := io.ReadAll(rec.Result().Body)
		t.Fatalf("GET persistent catalogue = %d %q, want 404", rec.Code, b)
	}
}

func TestCatalogSizeRefreshPersistsForNextStartup(t *testing.T) {
	root := writeBundle(t)
	c := newCatalog(root)
	entry, ok := c.get("example.org_iiif_m")
	if !ok || entry.sizeKnown {
		t.Fatalf("new legacy entry = (%#v, %v), want unknown size", entry, ok)
	}
	c.refreshSizes(context.Background())

	next := newCatalog(root)
	entry, ok = next.get("example.org_iiif_m")
	if !ok || !entry.sizeKnown || entry.sizeBytes == 0 || !strings.HasSuffix(entry.Size, " MB") {
		t.Fatalf("persisted sized entry = (%#v, %v)", entry, ok)
	}
}

func TestCatalogDoesNotOverwriteCorruptResearchData(t *testing.T) {
	root := writeBundle(t)
	dir := filepath.Join(root, catalogDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, catalogFileName)
	const corrupt = `{this is not recoverable JSON`
	if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}

	c := newCatalog(root)
	if err := c.update("example.org_iiif_m", "replacement", "replacement"); err == nil {
		t.Fatal("editing through a corrupt catalogue unexpectedly succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != corrupt {
		t.Fatalf("corrupt research data was overwritten: %q", got)
	}
}
