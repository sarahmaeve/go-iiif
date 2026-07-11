package source

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

type frontierFetcher struct {
	bodies map[string]string
	errs   map[string]error
	calls  map[string]int
}

func (f *frontierFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	if f.calls == nil {
		f.calls = make(map[string]int)
	}
	f.calls[url]++
	if err := f.errs[url]; err != nil {
		return nil, err
	}
	body, ok := f.bodies[url]
	if !ok {
		return nil, ErrNotFound
	}
	return []byte(body), nil
}

func TestCollectionFrontier_RestartsWithoutFetchingRoot(t *testing.T) {
	const (
		root = "https://example.org/collection/root"
		sub  = "https://example.org/collection/sub"
		m1   = "https://example.org/manifest/1"
		m2   = "https://example.org/manifest/2"
	)
	fetcher := &frontierFetcher{bodies: map[string]string{
		root: `{"manifests":[{"@id":"` + m1 + `"}],"collections":[{"@id":"` + sub + `"}]}`,
		sub:  `{"items":[{"id":"` + m2 + `","type":"Manifest"}]}`,
	}}
	statePath := filepath.Join(t.TempDir(), "frontier.json")
	first, err := OpenCollectionFrontierSource(fetcher, root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	for got, streamErr := range first.Manifests(context.Background()) {
		if streamErr != nil {
			t.Fatal(streamErr)
		}
		if got != m1 {
			t.Fatalf("first manifest = %q, want %q", got, m1)
		}
		break // simulate termination after root was committed, before sub fetch
	}
	if fetcher.calls[root] != 1 || fetcher.calls[sub] != 0 {
		t.Fatalf("first-run fetches = %v, want root only", fetcher.calls)
	}

	journal := NewMemoryJournal()
	if err := journal.MarkDone(m1); err != nil {
		t.Fatal(err)
	}
	second, err := OpenCollectionFrontierSource(fetcher, root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, NewResumableSource(second, journal))
	if !slices.Equal(got, []string{m2}) {
		t.Fatalf("restart manifests = %v, want [%s]", got, m2)
	}
	if fetcher.calls[root] != 1 || fetcher.calls[sub] != 1 {
		t.Fatalf("restart fetches = %v; root must not be requested again", fetcher.calls)
	}

	if err := journal.MarkDone(m2); err != nil {
		t.Fatal(err)
	}
	third, err := OpenCollectionFrontierSource(fetcher, root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, NewResumableSource(third, journal)); len(got) != 0 {
		t.Fatalf("completed restart yielded %v", got)
	}
	if fetcher.calls[root] != 1 || fetcher.calls[sub] != 1 {
		t.Fatalf("completed frontier made remote requests: %v", fetcher.calls)
	}
}

func TestCollectionFrontier_FailedFetchRemainsPending(t *testing.T) {
	const (
		root = "https://example.org/collection/root"
		m1   = "https://example.org/manifest/1"
	)
	errTemporary := errors.New("temporary")
	fetcher := &frontierFetcher{
		bodies: map[string]string{root: `{"items":[{"id":"` + m1 + `","type":"Manifest"}]}`},
		errs:   map[string]error{root: errTemporary},
	}
	statePath := filepath.Join(t.TempDir(), "frontier.json")
	first, err := OpenCollectionFrontierSource(fetcher, root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	var gotErr error
	for _, streamErr := range first.Manifests(context.Background()) {
		gotErr = streamErr
	}
	if !errors.Is(gotErr, errTemporary) {
		t.Fatalf("stream error = %v, want temporary error", gotErr)
	}
	delete(fetcher.errs, root)
	second, err := OpenCollectionFrontierSource(fetcher, root, statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, second); !slices.Equal(got, []string{m1}) {
		t.Fatalf("retry manifests = %v, want [%s]", got, m1)
	}
	if fetcher.calls[root] != 2 {
		t.Fatalf("pending collection fetched %d times, want one failure plus one retry", fetcher.calls[root])
	}
}

func TestCollectionFrontier_DeduplicatesCyclesAndManifests(t *testing.T) {
	const (
		a  = "https://example.org/collection/a"
		b  = "https://example.org/collection/b"
		m1 = "https://example.org/manifest/1"
	)
	fetcher := &frontierFetcher{bodies: map[string]string{
		a: `{"members":[{"id":"` + m1 + `","type":"Manifest"},{"id":"` + b + `","type":"Collection"}]}`,
		b: `{"items":[{"id":"` + m1 + `","type":"Manifest"},{"id":"` + a + `","type":"Collection"}]}`,
	}}
	src, err := OpenCollectionFrontierSource(fetcher, a, filepath.Join(t.TempDir(), "frontier.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, src); !slices.Equal(got, []string{m1}) {
		t.Fatalf("manifests = %v, want one deduplicated manifest", got)
	}
	if fetcher.calls[a] != 1 || fetcher.calls[b] != 1 {
		t.Fatalf("cycle fetched collections repeatedly: %v", fetcher.calls)
	}
}

func TestCollectionFrontier_RejectsDifferentRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frontier.json")
	fetcher := &frontierFetcher{bodies: map[string]string{}}
	if _, err := OpenCollectionFrontierSource(fetcher, "https://example.org/a", path); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCollectionFrontierSource(fetcher, "https://example.org/b", path); err == nil {
		t.Fatal("frontier accepted a different collection root")
	}
}
