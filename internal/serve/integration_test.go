//go:build integration

// Live dogfood for the full preserve→serve chain. Excluded from the default
// build; run with:
//
//	go test -tags=integration ./internal/serve/
//
// Fetches one real Digital Bodleian manifest, preserves it, serves the bundle
// over loopback HTTP, and fetches the manifest back — proving a researcher
// can read their preserved copy with the institution out of the picture.
package serve

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/sarahmaeve/go-iiif/internal/preserve"
	"github.com/sarahmaeve/go-iiif/internal/source"
)

func TestIntegration_PreserveThenServe(t *testing.T) {
	const manifestURL = "https://iiif.bodleian.ox.ac.uk/iiif/manifest/f317ad0c-a35b-4e9f-8426-c71f215d382d.json"

	fetcher := source.NewPoliteFetcher(source.NewHTTPFetcher())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	manifestBytes, err := fetcher.Fetch(ctx, manifestURL)
	if err != nil {
		t.Fatalf("fetch live manifest: %v", err)
	}
	root := t.TempDir()
	sum, err := preserve.Preserve(ctx, fetcher, preserve.NewLocalBlobStore(root), manifestURL, manifestBytes)
	if err != nil {
		t.Fatalf("preserve: %v", err)
	}
	if sum.Stored != 1 {
		t.Fatalf("expected 1 image stored, got %+v", sum)
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveCtx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { _ = New(root).Serve(serveCtx, ln, "", "") }()

	url := "http://" + ln.Addr().String() + "/" + sum.Dir + "/manifest.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("serve GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(got) != string(manifestBytes) {
		t.Fatalf("served manifest mismatch: status %d, %d bytes (preserved %d)",
			resp.StatusCode, len(got), len(manifestBytes))
	}
	t.Logf("OK: preserved + served %s back over loopback (%d bytes)", sum.Dir, len(got))
}
