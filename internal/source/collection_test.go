package source

import (
	"context"
	"testing"
	"time"
)

// mapFetcher is an in-memory Fetcher for tests: URL -> manifest/collection
// JSON bytes. A missing URL returns ErrNotFound.
type mapFetcher map[string]string

func (m mapFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	body, ok := m[url]
	if !ok {
		return nil, ErrNotFound
	}
	return []byte(body), nil
}

// collect drains the manifest stream into a slice, failing on first error.
func collect(t *testing.T, s Source) []string {
	t.Helper()
	var got []string
	for url, err := range s.Manifests(context.Background()) {
		if err != nil {
			t.Fatalf("Manifests stream error: %v", err)
		}
		got = append(got, url)
	}
	return got
}

func TestCollectionSource_NestedV2Collections(t *testing.T) {
	const (
		top = "https://example.org/collection/top"
		sub = "https://example.org/collection/sub"
	)
	fetcher := mapFetcher{
		top: `{
			"@type": "sc:Collection", "@id": "` + top + `",
			"collections": [{"@id": "` + sub + `", "@type": "sc:Collection"}],
			"manifests": [{"@id": "https://example.org/iiif/a/manifest.json", "@type": "sc:Manifest"}]
		}`,
		sub: `{
			"@type": "sc:Collection", "@id": "` + sub + `",
			"manifests": [
				{"@id": "https://example.org/iiif/b/manifest.json", "@type": "sc:Manifest"},
				{"@id": "https://example.org/iiif/c/manifest.json", "@type": "sc:Manifest"}
			]
		}`,
	}

	got := collect(t, NewCollectionSource(fetcher, top))
	want := []string{
		"https://example.org/iiif/a/manifest.json",
		"https://example.org/iiif/b/manifest.json",
		"https://example.org/iiif/c/manifest.json",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("manifest[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCollectionSource_CyclicReferenceTerminates(t *testing.T) {
	const (
		a = "https://example.org/collection/a"
		b = "https://example.org/collection/b"
	)
	// a -> b -> a (cycle). Each collection also has one manifest.
	fetcher := mapFetcher{
		a: `{"@type":"sc:Collection","@id":"` + a + `",
			"collections":[{"@id":"` + b + `"}],
			"manifests":[{"@id":"https://example.org/iiif/a/manifest.json"}]}`,
		b: `{"@type":"sc:Collection","@id":"` + b + `",
			"collections":[{"@id":"` + a + `"}],
			"manifests":[{"@id":"https://example.org/iiif/b/manifest.json"}]}`,
	}

	done := make(chan []string, 1)
	go func() { done <- collect(t, NewCollectionSource(fetcher, a)) }()

	select {
	case got := <-done:
		want := []string{
			"https://example.org/iiif/a/manifest.json",
			"https://example.org/iiif/b/manifest.json",
		}
		if len(got) != len(want) {
			t.Fatalf("got %v, want each manifest exactly once %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("manifest[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("walk did not terminate on a cyclic collection reference")
	}
}

func TestCollectionSource_FlatV2Manifests(t *testing.T) {
	const root = "https://example.org/collection/top"
	fetcher := mapFetcher{
		root: `{
			"@type": "sc:Collection",
			"@id": "` + root + `",
			"manifests": [
				{"@id": "https://example.org/iiif/a/manifest.json", "@type": "sc:Manifest"},
				{"@id": "https://example.org/iiif/b/manifest.json", "@type": "sc:Manifest"}
			]
		}`,
	}

	src := NewCollectionSource(fetcher, root)
	got := collect(t, src)

	want := []string{
		"https://example.org/iiif/a/manifest.json",
		"https://example.org/iiif/b/manifest.json",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d manifests %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("manifest[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
