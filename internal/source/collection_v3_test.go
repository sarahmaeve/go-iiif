package source

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCollectionSource_WalksRealV3Collection drives the walker with the real
// IIIF Cookbook v3 collection fixture (flat items[] of Manifests, v3 id/type
// keys), no network.
func TestCollectionSource_WalksRealV3Collection(t *testing.T) {
	v3, err := os.ReadFile(filepath.Join("testdata", "iiif_v3_collection_cookbook0032.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	const root = "https://iiif.io/api/cookbook/recipe/0032-collection/collection.json"
	fetcher := mapFetcher{root: string(v3)}

	got := collect(t, NewCollectionSource(fetcher, root))
	want := []string{
		"https://iiif.io/api/cookbook/recipe/0032-collection/manifest-01.json",
		"https://iiif.io/api/cookbook/recipe/0032-collection/manifest-02.json",
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

// TestCollectionSource_WalksV2Members covers the legacy v2 "members" array,
// which mixes manifests and sub-collections in one list distinguished by
// @type. Constructed (not downloaded): real v2 members collections are rare.
func TestCollectionSource_WalksV2Members(t *testing.T) {
	const (
		top = "https://example.org/v2/top"
		sub = "https://example.org/v2/sub"
	)
	fetcher := mapFetcher{
		top: `{"@context":"http://iiif.io/api/presentation/2/context.json",
			"@id":"` + top + `","@type":"sc:Collection","members":[
			{"@id":"https://example.org/iiif/a/manifest.json","@type":"sc:Manifest"},
			{"@id":"` + sub + `","@type":"sc:Collection"}]}`,
		sub: `{"@id":"` + sub + `","@type":"sc:Collection","manifests":[
			{"@id":"https://example.org/iiif/b/manifest.json"}]}`,
	}

	got := collect(t, NewCollectionSource(fetcher, top))
	want := []string{
		"https://example.org/iiif/a/manifest.json",
		"https://example.org/iiif/b/manifest.json",
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

// TestCollectionSource_WalksNestedV3 covers a v3 collection whose items[]
// contains a sub-Collection that must be fetched and recursed.
func TestCollectionSource_WalksNestedV3(t *testing.T) {
	const (
		top = "https://example.org/v3/top"
		sub = "https://example.org/v3/sub"
	)
	fetcher := mapFetcher{
		top: `{"@context":"http://iiif.io/api/presentation/3/context.json",
			"id":"` + top + `","type":"Collection","items":[
			{"id":"https://example.org/iiif/a/manifest.json","type":"Manifest"},
			{"id":"` + sub + `","type":"Collection"}]}`,
		sub: `{"@context":"http://iiif.io/api/presentation/3/context.json",
			"id":"` + sub + `","type":"Collection","items":[
			{"id":"https://example.org/iiif/b/manifest.json","type":"Manifest"}]}`,
	}

	got := collect(t, NewCollectionSource(fetcher, top))
	want := []string{
		"https://example.org/iiif/a/manifest.json",
		"https://example.org/iiif/b/manifest.json",
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
