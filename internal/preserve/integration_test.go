//go:build integration

// Live dogfood for the preservation slice. Excluded from the default build;
// run with:
//
//	go test -tags=integration ./internal/preserve/
//
// Fetches one real Digital Bodleian manifest (single canvas) and its largest
// JPEG through the polite fetcher — ~2 courteous requests — and verifies the
// stored bundle.
package preserve

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sarahmaeve/go-iiif/internal/source"
)

func TestIntegration_PreserveLiveBodleianManifest(t *testing.T) {
	const manifestURL = "https://iiif.bodleian.ox.ac.uk/iiif/manifest/f317ad0c-a35b-4e9f-8426-c71f215d382d.json"

	fetcher := source.NewPoliteFetcher(source.NewHTTPFetcher())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	manifestBytes, err := fetcher.Fetch(ctx, manifestURL)
	if err != nil {
		t.Fatalf("fetching live manifest: %v", err)
	}

	root := t.TempDir()
	sum, err := Preserve(ctx, fetcher, NewLocalBlobStore(root), manifestURL, manifestBytes)
	if err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	if sum.Images != 1 || sum.Stored != 1 || len(sum.Failures) != 0 {
		t.Fatalf("summary = %+v, want 1 image stored, no failures", sum)
	}

	dir := filepath.Join(root, sum.Dir)
	jpeg, err := os.ReadFile(filepath.Join(dir, "0001.jpg"))
	if err != nil {
		t.Fatalf("reading stored image: %v", err)
	}
	// Real JPEG starts with the SOI marker 0xFFD8.
	if len(jpeg) < 2 || !bytes.HasPrefix(jpeg, []byte{0xFF, 0xD8}) {
		t.Fatalf("stored 0001.jpg is not a JPEG (%d bytes, prefix %x)", len(jpeg), jpeg[:min(2, len(jpeg))])
	}
	for _, f := range []string{"manifest.json", "provenance.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("expected %s: %v", f, err)
		}
	}
	t.Logf("OK: preserved %d image(s) (%d bytes) + manifest + provenance to %s",
		sum.Stored, len(jpeg), sum.Dir)
}
