package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A thumbnail pointing at a non-preserved remote service (Gallica uses a
// different path than the image service) would 404 offline. The rewrite
// drops any non-local thumbnail so nothing broken is requested offline.
func TestRewriteManifest_DropsRemoteThumbnails(t *testing.T) {
	manifest := []byte(`{
	  "thumbnail":{"@id":"https://gallica.bnf.fr/ark:/12148/xyz.thumbnail"},
	  "sequences":[{"canvases":[{
	    "thumbnail":{"@id":"https://gallica.bnf.fr/ark:/12148/xyz/f1.thumbnail"},
	    "images":[{"resource":{
	      "@id":"https://gallica.bnf.fr/iiif/ark:/12148/xyz/f1/full/full/0/native.jpg",
	      "service":{"@id":"https://gallica.bnf.fr/iiif/ark:/12148/xyz/f1"}
	    }}]
	  }]}]
	}`)
	prov := []byte(`{"images":[{"file":"0001.jpg",` +
		`"service_id":"https://gallica.bnf.fr/iiif/ark:/12148/xyz/f1",` +
		`"source_url":"https://gallica.bnf.fr/iiif/ark:/12148/xyz/f1/full/full/0/native.jpg",` +
		`"tile_dir":"0001"}]}`)

	out, err := rewriteManifest(manifest, prov, "https://h/inst/slug", "")
	if err != nil {
		t.Fatalf("rewriteManifest: %v", err)
	}
	if strings.Contains(string(out), "thumbnail") {
		t.Fatalf("remote thumbnail not dropped:\n%s", out)
	}
	// The preserved image must still be localized + re-pointed.
	if !strings.Contains(string(out), "https://h/inst/slug/0001.jpg") ||
		strings.Contains(string(out), "gallica.bnf.fr/iiif") {
		t.Fatalf("image not localized alongside thumbnail drop:\n%s", out)
	}
}

// When provenance records a tile_dir, the rewrite must RE-POINT the image
// at the local level0 pyramid (deep zoom) instead of stripping the service.
func TestRewriteManifest_RepointsToLocalTileService(t *testing.T) {
	manifest := []byte(`{
	  "items":[{"items":[{"items":[{"body":{
	    "id":"https://remote.example/iiif/abc/full/max/0/default.jpg",
	    "type":"Image",
	    "service":[{"id":"https://remote.example/iiif/abc","type":"ImageService3"}]
	  }}]}]}]
	}`)
	prov := []byte(`{"images":[{"file":"0001.jpg",` +
		`"service_id":"https://remote.example/iiif/abc",` +
		`"source_url":"https://remote.example/iiif/abc/full/max/0/default.jpg",` +
		`"tile_dir":"0001"}]}`)
	base := "https://h/inst/slug"

	out, err := rewriteManifest(manifest, prov, base, "")
	if err != nil {
		t.Fatalf("rewriteManifest: %v", err)
	}
	if strings.Contains(string(out), "remote.example") {
		t.Fatalf("original remote URLs still present:\n%s", out)
	}

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("rewritten manifest invalid: %v", err)
	}
	body := doc["items"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)["body"].(map[string]any)

	if body["id"] != base+"/0001.jpg" {
		t.Fatalf("body id = %v, want %s/0001.jpg", body["id"], base)
	}
	svc, ok := body["service"].([]any)
	if !ok || len(svc) != 1 {
		t.Fatalf("body.service = %v, want a 1-element array (deep-zoom service kept)", body["service"])
	}
	s := svc[0].(map[string]any)
	if s["id"] != base+"/0001" || s["type"] != "ImageService3" || s["profile"] != "level0" {
		t.Fatalf("local service = %v, want id %s/0001 ImageService3 level0", s, base)
	}
}

// When an image is re-pointed at the local level0 pyramid, the manifest's
// Canvas and image-resource width/height must be corrected to the locally
// stored pixel size (the source server may have downscaled on the way in,
// e.g. Bodleian caps /full/max/ at 4000px). A stale, larger dimension makes
// a level0 deep-zoom source request tiles that were never generated, so
// only a sub-region of every page renders.
func TestRewriteManifest_CorrectsDimsToLocalInfoJSON(t *testing.T) {
	bundleDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bundleDir, "0001"), 0o755); err != nil {
		t.Fatalf("mkdir tile dir: %v", err)
	}
	info := []byte(`{"@context":"http://iiif.io/api/image/3/context.json",
	  "type":"ImageService3","profile":"level0","width":2644,"height":4000}`)
	if err := os.WriteFile(filepath.Join(bundleDir, "0001", "info.json"), info, 0o644); err != nil {
		t.Fatalf("write info.json: %v", err)
	}

	manifest := []byte(`{
	  "sequences":[{"canvases":[{
	    "@type":"sc:Canvas","width":2737,"height":4140,
	    "images":[{"resource":{
	      "@id":"https://remote.example/iiif/abc/full/max/0/default.jpg",
	      "width":2737,"height":4140,
	      "service":{"@id":"https://remote.example/iiif/abc"}
	    }}]
	  }]}]
	}`)
	prov := []byte(`{"images":[{"file":"0001.jpg",` +
		`"service_id":"https://remote.example/iiif/abc",` +
		`"source_url":"https://remote.example/iiif/abc/full/max/0/default.jpg",` +
		`"tile_dir":"0001"}]}`)
	base := "https://h/inst/slug"

	out, err := rewriteManifest(manifest, prov, base, bundleDir)
	if err != nil {
		t.Fatalf("rewriteManifest: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("rewritten manifest invalid: %v", err)
	}
	canvas := doc["sequences"].([]any)[0].(map[string]any)["canvases"].([]any)[0].(map[string]any)
	res := canvas["images"].([]any)[0].(map[string]any)["resource"].(map[string]any)

	if w, _ := res["width"].(float64); w != 2644 {
		t.Errorf("resource width = %v, want 2644 (local info.json)", res["width"])
	}
	if h, _ := res["height"].(float64); h != 4000 {
		t.Errorf("resource height = %v, want 4000 (local info.json)", res["height"])
	}
	if w, _ := canvas["width"].(float64); w != 2644 {
		t.Errorf("canvas width = %v, want 2644 (local info.json)", canvas["width"])
	}
	if h, _ := canvas["height"].(float64); h != 4000 {
		t.Errorf("canvas height = %v, want 4000 (local info.json)", canvas["height"])
	}
	// And the deep-zoom service must still be re-pointed locally.
	svc, ok := res["service"].([]any)
	if !ok || len(svc) != 1 || svc[0].(map[string]any)["id"] != base+"/0001" {
		t.Fatalf("local level0 service not set: %v", res["service"])
	}
}

func TestRewriteManifest_RealV3Cookbook(t *testing.T) {
	dir := filepath.Join("testdata", "bundle", "cookbook-v3")
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	prov, err := os.ReadFile(filepath.Join(dir, "provenance.json"))
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}

	const (
		base    = "https://archive.local/cookbook-v3"
		svcHost = "https://iiif.io/api/image/3.0/example/reference/"
		wantImg = "https://archive.local/cookbook-v3/0001.jpg"
	)

	out, err := rewriteManifest(manifest, prov, base, dir)
	if err != nil {
		t.Fatalf("rewriteManifest: %v", err)
	}
	if strings.Contains(string(out), svcHost) {
		t.Fatalf("original image-server host still present in rewritten v3 manifest")
	}

	var g map[string]any
	if err := json.Unmarshal(out, &g); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// v3: items[0].items[0].items[0].body
	canvas := g["items"].([]any)[0].(map[string]any)
	anno := canvas["items"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)
	body := anno["body"].(map[string]any)

	if id, _ := body["id"].(string); id != wantImg {
		t.Fatalf("v3 body id = %q, want %q", id, wantImg)
	}
	if _, hasService := body["service"]; hasService {
		t.Fatalf("v3 body still has a service block; must be removed")
	}
	if f, _ := body["format"].(string); f != "image/jpeg" {
		t.Fatalf("v3 body format = %q, want image/jpeg", f)
	}
	// Non-image content preserved (canvas dimensions).
	if h, _ := canvas["height"].(float64); h != 3540 {
		t.Fatalf("v3 canvas height not preserved: %v", canvas["height"])
	}
}

func TestRewriteManifest_RealBodleian(t *testing.T) {
	dir := filepath.Join("testdata", "bundle", "bodleian-c481")
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	prov, err := os.ReadFile(filepath.Join(dir, "provenance.json"))
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}

	const (
		base      = "https://archive.local/bodleian-c481"
		serviceID = "https://iiif.bodleian.ox.ac.uk/iiif/image/c85d87de-abd9-43b1-abf4-c65a814dc0a8"
		wantImg   = "https://archive.local/bodleian-c481/0001.jpg"
	)

	out, err := rewriteManifest(manifest, prov, base, dir)
	if err != nil {
		t.Fatalf("rewriteManifest: %v", err)
	}

	// Must still be valid JSON.
	var generic map[string]any
	if err := json.Unmarshal(out, &generic); err != nil {
		t.Fatalf("rewritten manifest is not valid JSON: %v", err)
	}
	// No trace of the original image-server host should remain.
	if strings.Contains(string(out), serviceID) {
		t.Fatalf("original service id still present in rewritten manifest")
	}
	// The local image URL must be present.
	if !strings.Contains(string(out), wantImg) {
		t.Fatalf("local image URL %q not found in rewritten manifest", wantImg)
	}
	// Non-image content preserved (Bodleian manifest label).
	if lbl, _ := generic["label"].(string); lbl != "Bodleian Library C 4.8(1) Linc." {
		t.Fatalf("top-level label not preserved: %q", lbl)
	}

	// The canvas image resource: id local, no service, jpeg format.
	seqs := generic["sequences"].([]any)
	canvas := seqs[0].(map[string]any)["canvases"].([]any)[0].(map[string]any)
	res := canvas["images"].([]any)[0].(map[string]any)["resource"].(map[string]any)

	if id, _ := res["@id"].(string); id != wantImg {
		t.Fatalf("resource @id = %q, want %q", id, wantImg)
	}
	if _, hasService := res["service"]; hasService {
		t.Fatalf("resource still has a service block; it must be removed")
	}
	if f, _ := res["format"].(string); f != "image/jpeg" {
		t.Fatalf("resource format = %q, want image/jpeg", f)
	}
}
