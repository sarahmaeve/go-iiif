package source

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
)

// CollectionSource walks a IIIF Presentation Collection tree, emitting the
// URL of every manifest it reaches.
type CollectionSource struct {
	fetcher Fetcher
	root    string
}

// NewCollectionSource returns a Source that walks the collection at rootURL
// using fetcher to retrieve each collection/sub-collection document.
func NewCollectionSource(fetcher Fetcher, rootURL string) *CollectionSource {
	return &CollectionSource{fetcher: fetcher, root: rootURL}
}

// iiifCollection is the subset of a IIIF Collection we need to walk it,
// covering both Presentation 2.x (manifests/collections/members, @id/@type)
// and 3.0 (items, id/type).
type iiifCollection struct {
	Manifests   []v2Ref    `json:"manifests"`
	Collections []v2Ref    `json:"collections"`
	Members     []typedRef `json:"members"` // v2 mixed manifest/collection
	Items       []typedRef `json:"items"`   // v3 mixed manifest/collection
}

type v2Ref struct {
	ID string `json:"@id"`
}

// typedRef is one entry in a v2 "members" or v3 "items" array; it may be a
// Manifest or a sub-Collection, in either Presentation version's key style.
type typedRef struct {
	IDV2   string `json:"@id"`
	IDV3   string `json:"id"`
	TypeV2 string `json:"@type"` // e.g. "sc:Manifest", "sc:Collection"
	TypeV3 string `json:"type"`  // e.g. "Manifest", "Collection"
}

func (r typedRef) id() string {
	if r.IDV3 != "" {
		return r.IDV3
	}
	return r.IDV2
}

// kind returns "Manifest", "Collection", or "" — normalizing the v2 "sc:"
// prefix away.
func (r typedRef) kind() string {
	if r.TypeV3 != "" {
		return r.TypeV3
	}
	return strings.TrimPrefix(r.TypeV2, "sc:")
}

// Manifests yields every manifest URL reachable from the root collection,
// depth-first: a collection's own manifests are emitted before descending
// into its sub-collections.
func (c *CollectionSource) Manifests(ctx context.Context) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		c.walk(ctx, c.root, make(map[string]bool), yield)
	}
}

// walk recurses one collection URL, returning false as soon as yield asks to
// stop so the whole traversal unwinds. visited guards against cyclic or
// diamond collection references re-walking the same document.
func (c *CollectionSource) walk(ctx context.Context, url string, visited map[string]bool, yield func(string, error) bool) bool {
	if visited[url] {
		return true
	}
	visited[url] = true

	body, err := c.fetcher.Fetch(ctx, url)
	if err != nil {
		return yield("", fmt.Errorf("source: fetching collection %s: %w", url, err))
	}
	var col iiifCollection
	if err := json.Unmarshal(body, &col); err != nil {
		return yield("", fmt.Errorf("source: decoding collection %s: %w", url, err))
	}
	// v2 typed arrays: manifests first, then descend sub-collections.
	for _, m := range col.Manifests {
		if !yield(m.ID, nil) {
			return false
		}
	}
	for _, sub := range col.Collections {
		if !c.walk(ctx, sub.ID, visited, yield) {
			return false
		}
	}

	// v2 "members" and v3 "items": a single ordered list mixing manifests
	// and sub-collections — process in document order.
	for _, refs := range [][]typedRef{col.Members, col.Items} {
		for _, ref := range refs {
			switch ref.kind() {
			case "Manifest":
				if !yield(ref.id(), nil) {
					return false
				}
			case "Collection":
				if !c.walk(ctx, ref.id(), visited, yield) {
					return false
				}
			}
		}
	}
	return true
}
