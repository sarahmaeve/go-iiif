package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDoctorBundle(t *testing.T, tiled bool) (root, bundle string) {
	t.Helper()
	root = t.TempDir()
	bundle = filepath.Join(root, "example.org", "manuscript")
	if err := os.MkdirAll(bundle, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), []byte(`{"type":"Manifest"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tileField := ""
	if tiled {
		tileField = `,"tile_dir":"0001"`
	}
	prov := `{"manifest_url":"https://example.org/manifest","images":[{"file":"0001.jpg","service_id":"https://example.org/image"` + tileField + `}]}`
	if err := os.WriteFile(filepath.Join(bundle, "provenance.json"), []byte(prov), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "0001.jpg"), []byte("jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tiled {
		info := `{"width":4,"height":4,"sizes":[{"width":4,"height":4}],"tiles":[{"width":4,"height":4,"scaleFactors":[1]}]}`
		files := map[string][]byte{
			"0001/info.json":                 []byte(info),
			"0001/full/4,4/0/default.jpg":    []byte("full"),
			"0001/0,0,4,4/4,4/0/default.jpg": []byte("tile"),
		}
		for name, data := range files {
			path := filepath.Join(bundle, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root, bundle
}

func TestDiagnoseLibraryHealthyBundle(t *testing.T) {
	root, _ := writeDoctorBundle(t, true)
	report := DiagnoseLibrary(root)
	if !report.Healthy() {
		t.Fatalf("healthy library diagnosed as broken: %#v", report.Problems)
	}
	if report.Bundles != 1 || report.Images != 1 || report.TilePyramids != 1 || report.FilesChecked != 6 {
		t.Fatalf("report counts = %+v", report)
	}
	if len(report.Problems) != 1 || report.Problems[0].Severity != "WARN" || !strings.Contains(report.Problems[0].Message, "index is absent") {
		t.Fatalf("expected only absent-catalogue warning, got %#v", report.Problems)
	}
}

func TestDiagnoseLibraryFindsMissingAndCorruptFiles(t *testing.T) {
	root, bundle := writeDoctorBundle(t, true)
	if err := os.Remove(filepath.Join(bundle, "0001", "0,0,4,4", "4,4", "0", "default.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "annotations.json"), []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}

	report := DiagnoseLibrary(root)
	if report.Healthy() {
		t.Fatal("broken library diagnosed as healthy")
	}
	joined := ""
	for _, p := range report.Problems {
		joined += p.Path + ": " + p.Message + "\n"
	}
	for _, want := range []string{"default.jpg: missing or unreadable", "annotations.json", "decoding"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diagnosis missing %q; got:\n%s", want, joined)
		}
	}
}

func TestDiagnoseLibraryRejectsUnsafeProvenancePath(t *testing.T) {
	root, bundle := writeDoctorBundle(t, false)
	prov := `{"images":[{"file":"../outside.jpg"}]}`
	if err := os.WriteFile(filepath.Join(bundle, "provenance.json"), []byte(prov), 0o600); err != nil {
		t.Fatal(err)
	}
	report := DiagnoseLibrary(root)
	if report.Healthy() || len(report.Problems) == 0 {
		t.Fatalf("unsafe provenance path was accepted: %+v", report)
	}
	found := false
	for _, p := range report.Problems {
		found = found || strings.Contains(p.Message, "unsafe file path")
	}
	if !found {
		t.Fatalf("unsafe-path diagnosis missing: %#v", report.Problems)
	}
}
