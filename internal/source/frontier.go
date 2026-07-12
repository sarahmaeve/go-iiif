package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"slices"
)

const collectionFrontierVersion = 1

// CollectionFrontierSource walks a IIIF Collection through an atomic durable
// frontier. A restart yields already-discovered manifests from local state and
// continues with the first pending collection URL instead of fetching the root
// again. A completion Journal remains responsible for suppressing manifests
// whose final disposition is already durable.
type CollectionFrontierSource struct {
	fetcher Fetcher
	path    string
	state   collectionFrontier
}

type collectionFrontier struct {
	Version   int      `json:"version"`
	Root      string   `json:"root"`
	Pending   []string `json:"pending_collections,omitempty"`
	Visited   []string `json:"visited_collections,omitempty"`
	Manifests []string `json:"discovered_manifests,omitempty"`
	Complete  bool     `json:"complete"`
}

// CollectionFrontierStats is a read-only summary used by recovery-oriented
// CLI reporting.
type CollectionFrontierStats struct {
	PendingCollections int
	VisitedCollections int
	Manifests          int
	Complete           bool
}

// Stats reports the currently committed frontier state.
func (s *CollectionFrontierSource) Stats() CollectionFrontierStats {
	return CollectionFrontierStats{
		PendingCollections: len(s.state.Pending), VisitedCollections: len(s.state.Visited),
		Manifests: len(s.state.Manifests), Complete: s.state.Complete,
	}
}

// ReadCollectionFrontierStats reads a frontier without modifying it.
func ReadCollectionFrontierStats(statePath string) (CollectionFrontierStats, error) {
	b, err := os.ReadFile(statePath) //nolint:gosec // caller supplies a state path below its configured store
	if errors.Is(err, os.ErrNotExist) {
		return CollectionFrontierStats{}, nil
	}
	if err != nil {
		return CollectionFrontierStats{}, fmt.Errorf("source: reading collection frontier: %w", err)
	}
	var state collectionFrontier
	if err := json.Unmarshal(b, &state); err != nil {
		return CollectionFrontierStats{}, fmt.Errorf("source: decoding collection frontier: %w", err)
	}
	if state.Version != collectionFrontierVersion {
		return CollectionFrontierStats{}, fmt.Errorf("source: unsupported collection frontier version %d", state.Version)
	}
	return CollectionFrontierStats{
		PendingCollections: len(state.Pending), VisitedCollections: len(state.Visited),
		Manifests: len(state.Manifests), Complete: state.Complete,
	}, nil
}

// OpenCollectionFrontierSource opens or creates the collection discovery
// frontier at statePath. The path should be scoped to the ingest query
// fingerprint so unrelated collections never share discovery state.
func OpenCollectionFrontierSource(fetcher Fetcher, rootURL, statePath string) (*CollectionFrontierSource, error) {
	state, err := loadCollectionFrontier(rootURL, statePath)
	if err != nil {
		return nil, err
	}
	return &CollectionFrontierSource{fetcher: fetcher, path: statePath, state: state}, nil
}

func loadCollectionFrontier(rootURL, statePath string) (collectionFrontier, error) {
	b, err := os.ReadFile(statePath) //nolint:gosec // state path is derived below the configured library root
	if errors.Is(err, os.ErrNotExist) {
		state := collectionFrontier{
			Version: collectionFrontierVersion,
			Root:    rootURL,
			Pending: []string{rootURL},
		}
		if err := saveCollectionFrontier(statePath, state); err != nil {
			return collectionFrontier{}, err
		}
		return state, nil
	}
	if err != nil {
		return collectionFrontier{}, fmt.Errorf("source: reading collection frontier: %w", err)
	}
	var state collectionFrontier
	if err := json.Unmarshal(b, &state); err != nil {
		return collectionFrontier{}, fmt.Errorf("source: decoding collection frontier: %w", err)
	}
	if state.Version != collectionFrontierVersion {
		return collectionFrontier{}, fmt.Errorf("source: unsupported collection frontier version %d", state.Version)
	}
	if state.Root != rootURL {
		return collectionFrontier{}, fmt.Errorf("source: collection frontier belongs to %q, not %q", state.Root, rootURL)
	}
	if state.Complete && len(state.Pending) != 0 {
		return collectionFrontier{}, errors.New("source: collection frontier is marked complete with pending collections")
	}
	return state, nil
}

func saveCollectionFrontier(statePath string, state collectionFrontier) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("source: encoding collection frontier: %w", err)
	}
	b = append(b, '\n')
	dir := filepath.Dir(statePath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("source: creating collection frontier directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".frontier-*.tmp")
	if err != nil {
		return fmt.Errorf("source: creating collection frontier: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("source: writing collection frontier: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("source: closing collection frontier: %w", err)
	}
	if err := os.Rename(tmpName, statePath); err != nil {
		return fmt.Errorf("source: finalizing collection frontier: %w", err)
	}
	return nil
}

// Manifests first yields every locally discovered manifest, then advances the
// pending collection frontier one remotely fetched document at a time. Each
// document's newly discovered URLs are persisted before any are yielded.
func (s *CollectionFrontierSource) Manifests(ctx context.Context) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		emitted := make(map[string]bool, len(s.state.Manifests))
		manifestSeen := make(map[string]bool, len(s.state.Manifests))
		for _, manifestURL := range s.state.Manifests {
			manifestSeen[manifestURL] = true
			emitted[manifestURL] = true
			if !yield(manifestURL, nil) {
				return
			}
		}
		collectionSeen := make(map[string]bool, len(s.state.Visited)+len(s.state.Pending))
		for _, collectionURL := range s.state.Visited {
			collectionSeen[collectionURL] = true
		}
		for _, collectionURL := range s.state.Pending {
			collectionSeen[collectionURL] = true
		}

		for len(s.state.Pending) > 0 {
			if err := ctx.Err(); err != nil {
				yield("", err)
				return
			}
			current := s.state.Pending[0]
			body, err := s.fetcher.Fetch(ctx, current)
			if err != nil {
				yield("", fmt.Errorf("source: fetching collection %s: %w", current, err))
				return
			}
			manifests, collections, err := collectionReferences(body)
			if err != nil {
				yield("", fmt.Errorf("source: decoding collection %s: %w", current, err))
				return
			}

			next := collectionFrontier{
				Version:   s.state.Version,
				Root:      s.state.Root,
				Pending:   slices.Clone(s.state.Pending[1:]),
				Visited:   append(slices.Clone(s.state.Visited), current),
				Manifests: slices.Clone(s.state.Manifests),
			}
			var newlyDiscovered []string
			for _, manifestURL := range manifests {
				if manifestURL == "" || manifestSeen[manifestURL] {
					continue
				}
				manifestSeen[manifestURL] = true
				next.Manifests = append(next.Manifests, manifestURL)
				newlyDiscovered = append(newlyDiscovered, manifestURL)
			}
			for _, collectionURL := range collections {
				if collectionURL == "" || collectionSeen[collectionURL] {
					continue
				}
				collectionSeen[collectionURL] = true
				next.Pending = append(next.Pending, collectionURL)
			}
			next.Complete = len(next.Pending) == 0
			if err := saveCollectionFrontier(s.path, next); err != nil {
				yield("", err)
				return
			}
			s.state = next
			for _, manifestURL := range newlyDiscovered {
				if emitted[manifestURL] {
					continue
				}
				emitted[manifestURL] = true
				if !yield(manifestURL, nil) {
					return
				}
			}
		}
	}
}

func collectionReferences(body []byte) (manifests, collections []string, err error) {
	var col iiifCollection
	if err := json.Unmarshal(body, &col); err != nil {
		return nil, nil, err
	}
	for _, manifest := range col.Manifests {
		manifests = append(manifests, manifest.ID)
	}
	for _, collection := range col.Collections {
		collections = append(collections, collection.ID)
	}
	for _, refs := range [][]typedRef{col.Members, col.Items} {
		for _, ref := range refs {
			switch ref.kind() {
			case "Manifest":
				manifests = append(manifests, ref.id())
			case "Collection":
				collections = append(collections, ref.id())
			}
		}
	}
	return manifests, collections, nil
}
