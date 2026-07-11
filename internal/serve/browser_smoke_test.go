//go:build browser

package serve

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBrowserSmokeCatalogueViewerTileAnnotation(t *testing.T) {
	if os.Getenv("IIIF_BROWSER_SMOKE") != "1" {
		t.Skip("experimental browser smoke is parked; set IIIF_BROWSER_SMOKE=1 to develop it")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the opt-in browser smoke test")
	}
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		for _, candidate := range []string{
			"google-chrome", "chromium", "chromium-browser",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		} {
			if path, lookErr := exec.LookPath(candidate); lookErr == nil {
				chrome = path
				break
			}
		}
	}
	if chrome == "" {
		t.Skip("Chrome/Chromium is required; set CHROME_BIN to enable")
	}

	root := writeBrowserSmokeBundle(t)
	srv := New(root)
	srv.enforceLocalMutations = true
	srv.logf = t.Logf
	ts := httptest.NewServer(srv.logRequests(srv.Handler()))
	defer ts.Close()

	_, here, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(here), "..", "..", "scripts", "browser-smoke.mjs")
	profile := filepath.Join(t.TempDir(), "chrome-profile")
	cmd := exec.CommandContext(t.Context(), node, script, ts.URL+"/", chrome, profile) //nolint:gosec // fixed test script and discovered browser binaries
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("browser smoke failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	var result struct {
		TileLoaded bool `json:"tileLoaded"`
		Annotation struct {
			Status int  `json:"status"`
			Found  bool `json:"found"`
		} `json:"annotation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("browser result is not JSON: %v: %s", err, stdout.String())
	}
	if !result.TileLoaded || result.Annotation.Status != 201 || !result.Annotation.Found {
		t.Fatalf("incomplete browser result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "example.test", "browser-smoke", "annotations.json")); err != nil {
		t.Fatalf("browser annotation was not persisted: %v", err)
	}
}

func writeBrowserSmokeBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "example.test", "browser-smoke")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.test/manifest","type":"Manifest","label":{"en":["Browser Smoke Manuscript"]},"items":[{"id":"https://example.test/canvas/1","type":"Canvas","width":64,"height":64,"items":[{"id":"https://example.test/page/1","type":"AnnotationPage","items":[{"id":"https://example.test/painting/1","type":"Annotation","motivation":"painting","target":"https://example.test/canvas/1","body":{"id":"https://example.test/image/full/max/0/default.jpg","type":"Image","format":"image/jpeg","width":64,"height":64,"service":[{"id":"https://example.test/image","type":"ImageService3","profile":"level2"}]}}]}]}]}`
	provenance := `{"manifest_url":"https://example.test/manifest","images":[{"file":"0001.jpg","service_id":"https://example.test/image","source_url":"https://example.test/image/full/max/0/default.jpg","tile_dir":"0001"}]}`
	info := `{"@context":"http://iiif.io/api/image/3/context.json","id":"placeholder","type":"ImageService3","protocol":"http://iiif.io/api/image","profile":"level0","width":64,"height":64,"sizes":[{"width":64,"height":64}],"tiles":[{"width":64,"height":64,"scaleFactors":[1]}]}`
	for name, body := range map[string]string{"manifest.json": manifest, "provenance.json": provenance, "0001/info.json": info} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 120, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001.jpg", "0001/full/64,64/0/default.jpg", "0001/0,0,64,64/64,64/0/default.jpg"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
