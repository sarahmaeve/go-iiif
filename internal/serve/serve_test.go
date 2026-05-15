package serve

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeBundle lays down a minimal preserved bundle in a temp dir.
func writeBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "example.org_iiif_m")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"@type":"sc:Manifest"}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0001.jpg"), []byte("\xff\xd8JPEGBYTES"), 0o600); err != nil {
		t.Fatalf("write jpg: %v", err)
	}
	return root
}

func TestServer_ServesPreservedBundle(t *testing.T) {
	root := writeBundle(t)
	ts := httptest.NewServer(New(root).Handler())
	defer ts.Close()

	get := func(path string) (int, string) {
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
		return resp.StatusCode, string(b)
	}

	if code, body := get("/example.org_iiif_m/manifest.json"); code != 200 || body != `{"@type":"sc:Manifest"}` {
		t.Fatalf("manifest GET = %d %q", code, body)
	}
	if code, body := get("/example.org_iiif_m/0001.jpg"); code != 200 || body != "\xff\xd8JPEGBYTES" {
		t.Fatalf("image GET = %d %q", code, body)
	}
	if code, _ := get("/example.org_iiif_m/missing.json"); code != http.StatusNotFound {
		t.Fatalf("missing file = %d, want 404", code)
	}
}

func TestServer_ServeGracefulShutdownOnContextCancel(t *testing.T) {
	root := writeBundle(t)
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- New(root).Serve(ctx, ln, "", "") }()

	// Server is live: a request succeeds.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"http://"+ln.Addr().String()+"/example.org_iiif_m/0001.jpg", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET while serving: %v", err)
	}
	_ = resp.Body.Close()

	cancel() // trigger graceful shutdown
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil on graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}
}
