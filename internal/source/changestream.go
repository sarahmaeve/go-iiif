package source

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
)

// ChangeStreamSource consumes a IIIF Change Discovery API 1.0 stream
// (an Activity Streams OrderedCollection of paged Create/Update/Delete
// activities) and emits the manifest URL of each Create/Update. It is the
// preferred adapter where published — cheaper to refresh than re-walking the
// whole Collection tree (DESIGN §4.1).
type ChangeStreamSource struct {
	fetcher Fetcher
	root    string
}

// Resumable refresh: wrap a ChangeStreamSource in ResumableSource with a
// FileJournal — already-preserved manifests are skipped on re-run, and page
// re-fetches are cheap via the polite layer's conditional GET. An
// activity-level checkpoint (start near the stream end instead of re-walking
// every page) is a deferred optimization, not required for correctness.
//
// NewChangeStreamSource returns a Source over the Change Discovery
// OrderedCollection at rootURL.
func NewChangeStreamSource(fetcher Fetcher, rootURL string) *ChangeStreamSource {
	return &ChangeStreamSource{fetcher: fetcher, root: rootURL}
}

type asRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type asOrderedCollection struct {
	First asRef `json:"first"`
}

type asActivity struct {
	Type   string `json:"type"`
	Object asRef  `json:"object"`
}

type asPage struct {
	OrderedItems []asActivity `json:"orderedItems"`
	Next         asRef        `json:"next"`
}

// Manifests yields the manifest URL of every Create/Update activity, in
// stream order, following page `next` links. Repeated object ids collapse to
// their first occurrence (a stream re-lists an object on every change).
// Delete and other activity types are skipped. A page seen twice ends the
// walk (cycle guard).
//
// This is the streaming consumer model: a later Delete does NOT retroactively
// suppress an already-emitted Create of the same object — that would require
// buffering the whole (potentially huge) stream. A subsequent fetch of a
// since-deleted manifest degrades gracefully to ErrNotFound at the pipeline.
func (c *ChangeStreamSource) Manifests(ctx context.Context) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		body, err := c.fetcher.Fetch(ctx, c.root)
		if err != nil {
			yield("", fmt.Errorf("source: fetching activity collection %s: %w", c.root, err))
			return
		}
		var oc asOrderedCollection
		if err := json.Unmarshal(body, &oc); err != nil {
			yield("", fmt.Errorf("source: decoding activity collection %s: %w", c.root, err))
			return
		}

		visited := make(map[string]bool)
		emitted := make(map[string]bool)
		for page := oc.First.ID; page != "" && !visited[page]; {
			visited[page] = true

			body, err := c.fetcher.Fetch(ctx, page)
			if err != nil {
				yield("", fmt.Errorf("source: fetching activity page %s: %w", page, err))
				return
			}
			var p asPage
			if err := json.Unmarshal(body, &p); err != nil {
				yield("", fmt.Errorf("source: decoding activity page %s: %w", page, err))
				return
			}
			for _, act := range p.OrderedItems {
				switch act.Type {
				case "Create", "Update":
					if emitted[act.Object.ID] {
						continue
					}
					emitted[act.Object.ID] = true
					if !yield(act.Object.ID, nil) {
						return
					}
				}
			}
			page = p.Next.ID
		}
	}
}
