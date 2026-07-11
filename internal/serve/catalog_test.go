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
	"time"
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
		"tags":  {"Old French, John Dee, old french"},
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
	for _, want := range []string{"The English Catalogue Title", "From John Dee&#39;s library.", "Former shelfmark &lt;Dee&gt;.", "Tags · Old French, John Dee"} {
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
	if err := New(root).catalog.update("iiif.bodleian.ox.ac.uk/iiif_manifest_a.json", "title", "note", "tag"); err != nil {
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
	writeTestProvenance(t, root)
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
	writeTestProvenance(t, root)
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
	if err := c.update("example.org_iiif_m", "replacement", "replacement", "replacement"); err == nil {
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

func writeTestProvenance(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "example.org_iiif_m")
	if err := os.WriteFile(filepath.Join(dir, "provenance.json"), []byte(`{"manifest_url":"https://example.org/m","images":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogRefreshWaitsForCompletedBundle(t *testing.T) {
	root := writeNestedBundle(t)
	c := newCatalog(root)
	dir := filepath.Join(root, "new.example", "new-manuscript")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{"label":"New manuscript"}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	c.refreshSources()
	if _, ok := c.get("new.example/new-manuscript"); ok {
		t.Fatal("in-progress bundle appeared before provenance completion marker")
	}

	prov := `{"manifest_url":"https://new.example/manifest","images":[]}`
	if err := os.WriteFile(filepath.Join(dir, "provenance.json"), []byte(prov), 0o600); err != nil {
		t.Fatal(err)
	}
	if changes := c.refreshSources(); changes == 0 {
		t.Fatal("completed bundle did not change catalogue")
	}
	if _, ok := c.get("new.example/new-manuscript"); !ok {
		t.Fatal("completed bundle was not added")
	}

	if err := os.Rename(dir, dir+".removed"); err != nil {
		t.Fatal(err)
	}
	c.refreshSources()
	if _, ok := c.get("new.example/new-manuscript"); ok {
		t.Fatal("removed bundle remained in catalogue")
	}
}

func TestCatalogBackgroundRefreshFindsNewBundle(t *testing.T) {
	root := writeNestedBundle(t)
	c := newCatalog(root)
	c.refreshInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	c.startBackground(ctx)
	defer func() {
		cancel()
		c.wait()
	}()

	dir := filepath.Join(root, "live.example", "new-manuscript")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"label":"Live"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "provenance.json"), []byte(`{"manifest_url":"https://live.example/m","images":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := c.get("live.example/new-manuscript"); ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background refresh did not discover completed bundle")
}

func TestServerManualCatalogRefresh(t *testing.T) {
	root := writeNestedBundle(t)
	srv := New(root)
	dir := filepath.Join(root, "manual.example", "new-manuscript")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"label":"Manually refreshed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "provenance.json"), []byte(`{"manifest_url":"https://manual.example/m","images":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, catalogRefreshRoute, nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST refresh = %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), "manual.example/new-manuscript") || !strings.Contains(rec.Body.String(), "Refresh library") {
		t.Fatalf("refreshed catalogue missing new entry/button; body=%s", rec.Body.String())
	}
}

func TestCatalogRefreshLoadsExternalMetadataImport(t *testing.T) {
	root := writeNestedBundle(t)
	serving := newCatalog(root)
	externalImport := newCatalog(root)
	const dir = "iiif.bodleian.ox.ac.uk/iiif_manifest_a.json"
	if err := externalImport.update(dir, "Externally imported title", "Imported note", "exchange"); err != nil {
		t.Fatal(err)
	}
	serving.refreshSources()
	entry, ok := serving.get(dir)
	if !ok || entry.CustomTitle != "Externally imported title" || entry.Notes != "Imported note" || entry.Tags != "exchange" {
		t.Fatalf("live catalogue did not reload external import: (%#v, %v)", entry, ok)
	}
}
