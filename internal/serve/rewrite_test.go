package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	out, err := rewriteManifest(manifest, prov, base)
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

	out, err := rewriteManifest(manifest, prov, base)
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
