//go:build integration

// Live dogfood for the recursive, polite collection walk. Excluded from the
// default build; run with:
//
//	go test -tags=integration ./internal/source/
//
// It walks the real Digital Bodleian top-level collection but stops at the
// first manifest. The walk is depth-first, so the number of live requests
// before the first manifest is bounded by the tree depth along one path
// (asserted), not the whole collection — and every request goes through the
// polite per-host rate limiter.
package source

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type countingFetcher struct {
	inner Fetcher
	calls atomic.Int32
}

func (c *countingFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	c.calls.Add(1)
	return c.inner.Fetch(ctx, url)
}

func TestIntegration_LiveCollectionWalkBounded(t *testing.T) {
	const root = "https://iiif.bodleian.ox.ac.uk/iiif/collection/top"

	counter := &countingFetcher{inner: NewHTTPFetcher()}
	fetcher := NewPoliteFetcher(counter)
	src := NewCollectionSource(fetcher, root)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var first string
	var streamErr error
	for u, err := range src.Manifests(ctx) {
		if err != nil {
			streamErr = err
			break
		}
		first = u
		break // early-stop: depth-first ⇒ ~tree-depth fetches only
	}

	if streamErr != nil {
		t.Fatalf("live collection walk errored: %v", streamErr)
	}
	if first == "" {
		t.Fatal("walk yielded no manifest URL")
	}
	if !strings.HasPrefix(first, "https://iiif.bodleian.ox.ac.uk/") || !strings.Contains(first, "manifest") {
		t.Fatalf("first manifest URL looks wrong: %q", first)
	}
	if n := counter.calls.Load(); n > 25 {
		t.Fatalf("walk made %d live requests before first manifest; expected a bounded depth-first path", n)
	}
	t.Logf("OK: first manifest %s after %d live requests", first, counter.calls.Load())
}
