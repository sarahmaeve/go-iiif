package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCollectionSource_WalksRealBodleianRoot drives the walker with the real
// Digital Bodleian top-level collection document (a saved fixture, no
// network). The root has six sub-collections and no direct manifests; we stub
// the sub-collections so the recursion is deterministic while the root shape
// is real.
func TestCollectionSource_WalksRealBodleianRoot(t *testing.T) {
	top, err := os.ReadFile(filepath.Join("testdata", "bodleian_collection_top.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	const rootURL = "https://iiif.bodleian.ox.ac.uk/iiif/collection/top"
	fetcher := mapFetcher{rootURL: string(top)}

	// Extract the real sub-collection @ids from the fixture and give each a
	// stub with a single manifest, so a successful walk must descend into the
	// real-shaped tree.
	subIDs := []string{
		"https://iiif.bodleian.ox.ac.uk/iiif/collection/institutions",
		"https://iiif.bodleian.ox.ac.uk/iiif/collection/named-collections",
		"https://iiif.bodleian.ox.ac.uk/iiif/collection/object-types",
		"https://iiif.bodleian.ox.ac.uk/iiif/collection/origins",
		"https://iiif.bodleian.ox.ac.uk/iiif/collection/projects",
		"https://iiif.bodleian.ox.ac.uk/iiif/collection/collections",
	}
	var want []string
	for i, id := range subIDs {
		if !strings.Contains(string(top), id) {
			t.Fatalf("fixture missing expected sub-collection %s", id)
		}
		m := id + "/m" + string(rune('1'+i)) + "/manifest.json"
		fetcher[id] = `{"@type":"sc:Collection","@id":"` + id + `",
			"manifests":[{"@id":"` + m + `"}]}`
		want = append(want, m)
	}

	got := collect(t, NewCollectionSource(fetcher, rootURL))

	if len(got) != len(want) {
		t.Fatalf("got %d manifests %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("manifest[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
