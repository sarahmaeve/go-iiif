package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	catalogDirName  = ".iiifpreserve"
	catalogFileName = "catalog.json"
	catalogVersion  = 1
)

// persistedCatalog is the small, durable index beside the preserved bundles.
// Bundles remain authoritative: source metadata is re-read at startup, while
// expensive byte totals and researcher-authored catalogue fields survive
// restarts here.
type persistedCatalog struct {
	Version int                       `json:"version"`
	Entries map[string]persistedEntry `json:"entries"`
}

type persistedEntry struct {
	SourceStamp string `json:"source_stamp"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	SizeKnown   bool   `json:"size_known,omitempty"`
	CustomTitle string `json:"custom_title,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

// catalog is an in-memory request-time index with a persistent sidecar. It
// deliberately contains one row per manuscript, never one row per tile.
type catalog struct {
	root string
	path string

	mu      sync.RWMutex
	entries map[string]manifestSummary
	once    sync.Once
	wg      sync.WaitGroup
	loadErr error // protects an unreadable/corrupt sidecar from being overwritten
}

func newCatalog(root string) *catalog {
	c := &catalog{
		root:    root,
		path:    filepath.Join(root, catalogDirName, catalogFileName),
		entries: make(map[string]manifestSummary),
	}

	saved, err := c.load()
	c.loadErr = err
	for _, ref := range discoverBundles(root) {
		s := summaryFor(ref.absDir, ref.slug)
		if old, ok := saved[ref.slug]; ok {
			s.CustomTitle = old.CustomTitle
			s.Notes = old.Notes
			if old.SizeKnown && old.SourceStamp == s.sourceStamp {
				s.sizeBytes = old.SizeBytes
				s.sizeKnown = true
			}
		}
		s.finishDisplayFields()
		c.entries[ref.slug] = s
	}
	return c
}

func (c *catalog) load() (map[string]persistedEntry, error) {
	b, err := os.ReadFile(c.path) //nolint:gosec // fixed file under the configured library root
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("catalog: reading index: %w", err)
	}
	var doc persistedCatalog
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("catalog: decoding index: %w", err)
	}
	if doc.Version != catalogVersion {
		return nil, fmt.Errorf("catalog: unsupported index version %d", doc.Version)
	}
	return doc.Entries, nil
}

type bundleRef struct {
	absDir string
	slug   string
}

// discoverBundles finds manifest roots without entering them. A preserved
// bundle can contain tens of thousands of tile files; once manifest.json is
// present, descending further can discover nothing and is the source of the
// former 30–40 second request latency.
func discoverBundles(root string) []bundleRef {
	var out []bundleRef
	var walk func(string, string)
	walk = func(absDir, rel string) {
		entries, err := os.ReadDir(absDir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() && entry.Name() == "manifest.json" && rel != "" {
				out = append(out, bundleRef{absDir: absDir, slug: filepath.ToSlash(rel)})
				return
			}
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == catalogDirName {
				continue
			}
			childRel := entry.Name()
			if rel != "" {
				childRel = filepath.Join(rel, entry.Name())
			}
			walk(filepath.Join(absDir, entry.Name()), childRel)
		}
	}
	walk(root, "")
	sort.Slice(out, func(i, j int) bool { return out[i].slug < out[j].slug })
	return out
}

func (c *catalog) list() []manifestSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]manifestSummary, 0, len(c.entries))
	for _, entry := range c.entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out
}

func (c *catalog) get(dir string) (manifestSummary, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.entries[dir]
	return s, ok
}

func (c *catalog) update(dir, customTitle, notes string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[dir]
	if !ok {
		return fs.ErrNotExist
	}
	before := entry
	entry.CustomTitle = strings.TrimSpace(customTitle)
	entry.Notes = strings.TrimSpace(notes)
	entry.finishDisplayFields()
	c.entries[dir] = entry
	if err := c.saveLocked(); err != nil {
		c.entries[dir] = before
		return err
	}
	return nil
}

func (c *catalog) saveLocked() error {
	if c.loadErr != nil {
		return c.loadErr
	}
	doc := persistedCatalog{Version: catalogVersion, Entries: make(map[string]persistedEntry, len(c.entries))}
	for dir, entry := range c.entries {
		doc.Entries[dir] = persistedEntry{
			SourceStamp: entry.sourceStamp,
			SizeBytes:   entry.sizeBytes,
			SizeKnown:   entry.sizeKnown,
			CustomTitle: entry.CustomTitle,
			Notes:       entry.Notes,
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("catalog: encoding: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o750); err != nil {
		return fmt.Errorf("catalog: creating index directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".catalog-*.json")
	if err != nil {
		return fmt.Errorf("catalog: creating temporary index: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("catalog: writing index: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("catalog: closing index: %w", err)
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		return fmt.Errorf("catalog: finalizing index: %w", err)
	}
	return nil
}

// startSizeRefresh performs the one legacy-library migration in the
// background. Requests immediately use the small in-memory catalogue; sizes
// absent from the sidecar are filled without holding up the catalogue page.
func (c *catalog) startSizeRefresh(ctx context.Context) {
	c.once.Do(func() {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.refreshSizes(ctx)
		}()
	})
}

func (c *catalog) wait() { c.wg.Wait() }

func (c *catalog) refreshSizes(ctx context.Context) {
	for _, current := range c.list() {
		if current.sizeKnown {
			continue
		}
		n, err := dirSize(ctx, filepath.Join(c.root, filepath.FromSlash(current.Dir)))
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			continue
		}
		c.mu.Lock()
		entry, ok := c.entries[current.Dir]
		if ok && entry.sourceStamp == current.sourceStamp {
			entry.sizeBytes = n
			entry.sizeKnown = true
			entry.finishDisplayFields()
			c.entries[current.Dir] = entry
		}
		c.mu.Unlock()
	}

	c.mu.Lock()
	_ = c.saveLocked() // best effort: serving remains useful on a read-only library
	c.mu.Unlock()
}

func dirSize(ctx context.Context, dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable pieces produce a best-effort total
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !d.IsDir() {
			if fi, infoErr := d.Info(); infoErr == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total, err
}
