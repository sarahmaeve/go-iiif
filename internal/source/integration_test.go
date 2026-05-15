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

// Proves the live v3 path: the IIIF Cookbook publishes a stable Presentation
// 3.0 collection. One GET (its two items are Manifests, not recursed).
func TestIntegration_LiveV3CookbookCollection(t *testing.T) {
	const root = "https://iiif.io/api/cookbook/recipe/0032-collection/collection.json"
	src := NewCollectionSource(NewPoliteFetcher(NewHTTPFetcher()), root)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var got []string
	for u, err := range src.Manifests(ctx) {
		if err != nil {
			t.Fatalf("live v3 walk errored: %v", err)
		}
		got = append(got, u)
	}
	want := []string{
		"https://iiif.io/api/cookbook/recipe/0032-collection/manifest-01.json",
		"https://iiif.io/api/cookbook/recipe/0032-collection/manifest-02.json",
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("live v3 walk = %v, want %v", got, want)
	}
	t.Logf("OK: live v3 collection yielded %v", got)
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

// Proves the live IIIF Change Discovery seam: walks the real Digital Bodleian
// ActivityStream, early-stopping after a few manifests. Bounded — only the
// OrderedCollection + first page are fetched (asserted), every request polite.
func TestIntegration_LiveChangeStreamBounded(t *testing.T) {
	const root = "https://iiif.bodleian.ox.ac.uk/iiif/activity/all-changes"

	counter := &countingFetcher{inner: NewHTTPFetcher()}
	src := NewChangeStreamSource(NewPoliteFetcher(counter), root)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var got []string
	for u, err := range src.Manifests(ctx) {
		if err != nil {
			t.Fatalf("live change stream errored: %v", err)
		}
		got = append(got, u)
		if len(got) == 5 {
			break // early-stop within the first page
		}
	}

	if len(got) != 5 {
		t.Fatalf("got %d manifest URLs, want 5", len(got))
	}
	for _, u := range got {
		if !strings.HasPrefix(u, "https://iiif.bodleian.ox.ac.uk/iiif/manifest/") {
			t.Fatalf("unexpected manifest URL: %q", u)
		}
	}
	if n := counter.calls.Load(); n > 3 {
		t.Fatalf("made %d live requests for the first 5 manifests; expected ~2 (collection + page-0)", n)
	}
	t.Logf("OK: live change stream, first 5 manifests after %d requests", counter.calls.Load())
}
