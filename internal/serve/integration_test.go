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
	"strings"
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
	if resp.StatusCode != 200 {
		t.Fatalf("serve GET status %d", resp.StatusCode)
	}
	// The served manifest must point at THIS server's local image, with no
	// trace of the live institution — the whole point of preservation.
	localImg := "http://" + ln.Addr().String() + "/" + sum.Dir + "/0001.jpg"
	if !strings.Contains(string(got), localImg) {
		t.Fatalf("served manifest does not reference local image %q", localImg)
	}
	// The preserved page image's service must be gone (delocalized). The
	// manifest logo points at a different, un-preserved image service and
	// legitimately stays remote — we only localize preserved content.
	const preservedService = "iiif.bodleian.ox.ac.uk/iiif/image/c85d87de-abd9-43b1-abf4-c65a814dc0a8"
	if strings.Contains(string(got), preservedService) {
		t.Fatalf("served manifest still references the preserved image's live service")
	}
	t.Logf("OK: preserved, served, and rewritten %s to point at local images", sum.Dir)
}
