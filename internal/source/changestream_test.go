package source

import (
	"os"
	"path/filepath"
	"testing"
)

// TestChangeStreamSource_DedupAndDeleteSemantics covers Change Discovery
// consumer semantics: a stream re-lists an object on every change, so repeated
// object ids collapse to one emission (first occurrence, stream order
// preserved); Delete activities are not emitted. Constructed spec-accurate
// fixture (real streams are huge; this isolates the logic).
//
// Known limitation (documented in changestream.go): this is the streaming
// model — a later Delete does not retroactively suppress an earlier emitted
// Create of the same object (that would require buffering the whole stream).
func TestChangeStreamSource_DedupAndDeleteSemantics(t *testing.T) {
	const root = "https://ex.org/activity/all-changes"
	const page = "https://ex.org/activity/page-0"
	fetcher := mapFetcher{
		root: `{"type":"OrderedCollection","first":{"id":"` + page + `"}}`,
		page: `{"type":"OrderedCollectionPage","orderedItems":[
			{"type":"Create","object":{"id":"https://ex.org/m/A.json","type":"Manifest"}},
			{"type":"Update","object":{"id":"https://ex.org/m/A.json","type":"Manifest"}},
			{"type":"Create","object":{"id":"https://ex.org/m/B.json","type":"Manifest"}},
			{"type":"Delete","object":{"id":"https://ex.org/m/X.json","type":"Manifest"}},
			{"type":"Update","object":{"id":"https://ex.org/m/C.json","type":"Manifest"}}]}`,
	}

	got := collect(t, NewChangeStreamSource(fetcher, root))
	want := []string{
		"https://ex.org/m/A.json",
		"https://ex.org/m/B.json",
		"https://ex.org/m/C.json",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v (A deduped, X delete skipped)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("manifest[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestChangeStreamSource_WalksRealBodleianActivity drives the adapter with
// the real Digital Bodleian Change Discovery OrderedCollection + first page
// (saved fixtures, no network), with a stub terminator page so the offline
// walk ends deterministically.
func TestChangeStreamSource_WalksRealBodleianActivity(t *testing.T) {
	oc, err := os.ReadFile(filepath.Join("testdata", "bodleian_activity_ordered_collection.json"))
	if err != nil {
		t.Fatalf("reading OrderedCollection fixture: %v", err)
	}
	page0, err := os.ReadFile(filepath.Join("testdata", "bodleian_activity_page0.json"))
	if err != nil {
		t.Fatalf("reading page-0 fixture: %v", err)
	}

	const (
		root  = "https://iiif.bodleian.ox.ac.uk/iiif/activity/all-changes"
		page1 = "https://iiif.bodleian.ox.ac.uk/iiif/activity/page-1"
	)
	fetcher := mapFetcher{
		root: string(oc),
		"https://iiif.bodleian.ox.ac.uk/iiif/activity/page-0": string(page0),
		// Real page-0's "next" points here; terminate the offline walk.
		page1: `{"@context":"http://iiif.io/api/discovery/1/context.json",
			"id":"` + page1 + `","type":"OrderedCollectionPage","orderedItems":[]}`,
	}

	got := collect(t, NewChangeStreamSource(fetcher, root))

	if len(got) != 100 {
		t.Fatalf("got %d manifest URLs, want 100 (page-0 has 100 Create activities)", len(got))
	}
	const wantFirst = "https://iiif.bodleian.ox.ac.uk/iiif/manifest/0df0371f-b0fb-4cb7-9c4d-8d108c06a694.json"
	if got[0] != wantFirst {
		t.Fatalf("first manifest = %q, want %q", got[0], wantFirst)
	}
}
