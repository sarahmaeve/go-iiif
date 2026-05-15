package source

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
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

// iiifCollection is the subset of a IIIF v2 Collection we need to walk it.
type iiifCollection struct {
	Manifests []struct {
		ID string `json:"@id"`
	} `json:"manifests"`
	Collections []struct {
		ID string `json:"@id"`
	} `json:"collections"`
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
	return true
}
