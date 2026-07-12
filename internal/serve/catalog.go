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
	"time"
)

const (
	catalogDirName  = ".iiifpreserve"
	catalogFileName = "catalog.json"
	catalogVersion  = 1
)

const catalogRefreshInterval = 5 * time.Second

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
	Tags        string `json:"tags,omitempty"`
}

// catalog is an in-memory request-time index with a persistent sidecar. It
// deliberately contains one row per manuscript, never one row per tile.
type catalog struct {
	root string
	path string

	mu              sync.RWMutex
	entries         map[string]manifestSummary
	once            sync.Once
	wg              sync.WaitGroup
	loadErr         error // protects an unreadable/corrupt sidecar from being overwritten
	persistedStamp  string
	refreshInterval time.Duration
}

func newCatalog(root string) *catalog {
	c := &catalog{
		root:            root,
		path:            filepath.Join(root, catalogDirName, catalogFileName),
		entries:         make(map[string]manifestSummary),
		refreshInterval: catalogRefreshInterval,
	}

	saved, err := c.load()
	c.loadErr = err
	c.persistedStamp = fileStamp(c.path)
	for _, ref := range discoverBundles(root) {
		s := summaryFor(ref.absDir, ref.slug)
		if strings.HasSuffix(s.sourceStamp, ":-") {
			continue // provenance.json is Preserve's completion marker
		}
		if old, ok := saved[ref.slug]; ok {
			s.CustomTitle = old.CustomTitle
			s.Notes = old.Notes
			s.Tags = old.Tags
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
	out, _ := discoverBundlesChecked(root)
	return out
}

func discoverBundlesChecked(root string) ([]bundleRef, error) {
	var out []bundleRef
	var walk func(string, string) error
	walk = func(absDir, rel string) error {
		entries, err := os.ReadDir(absDir)
		if err != nil {
			return fmt.Errorf("catalog: reading %s: %w", absDir, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && entry.Name() == "manifest.json" && rel != "" {
				out = append(out, bundleRef{absDir: absDir, slug: filepath.ToSlash(rel)})
				return nil
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
			if err := walk(filepath.Join(absDir, entry.Name()), childRel); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].slug < out[j].slug })
	return out, nil
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

func (c *catalog) snapshot() map[string]manifestSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]manifestSummary, len(c.entries))
	for dir, entry := range c.entries {
		out[dir] = entry
	}
	return out
}

func (c *catalog) update(dir, customTitle, notes, tags string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[dir]
	if !ok {
		return fs.ErrNotExist
	}
	before := entry
	entry.CustomTitle = strings.TrimSpace(customTitle)
	entry.Notes = strings.TrimSpace(notes)
	entry.Tags = normalizeCatalogTags(tags)
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
			Tags:        entry.Tags,
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
	c.persistedStamp = fileStamp(c.path)
	return nil
}

// startBackground performs legacy size migration and live shallow refresh.
// Requests immediately use the small in-memory catalogue; neither task holds
// up the catalogue page.
func (c *catalog) startBackground(ctx context.Context) {
	c.once.Do(func() {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.runBackground(ctx)
		}()
	})
}

func (c *catalog) wait() { c.wg.Wait() }

// refreshSources performs a shallow reconciliation with the bundle tree.
// Existing entries retain researcher-authored fields and cached sizes. A new
// entry is admitted only after provenance.json exists: Preserve writes that
// file last, so it is also the bundle's completion marker.
func (c *catalog) refreshSources() (changes int) {
	c.reloadPersistedResearch()
	current := c.snapshot()
	fresh := make(map[string]manifestSummary)
	refs, err := discoverBundlesChecked(c.root)
	if err != nil {
		return 0 // a transient scan failure must never erase the live catalogue
	}
	for _, ref := range refs {
		stamp := activeManifestStamp(ref.absDir)
		old, existed := current[ref.slug]
		switch {
		case strings.HasSuffix(stamp, ":-") && existed:
			fresh[ref.slug] = old // do not regress a completed entry mid-write
		case strings.HasSuffix(stamp, ":-"):
			continue // new bundle is not complete yet
		case existed && old.sourceStamp == stamp:
			fresh[ref.slug] = old // unchanged: avoid reparsing large manifest JSON
		default:
			fresh[ref.slug] = summaryFor(ref.absDir, ref.slug)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	next := make(map[string]manifestSummary, len(fresh))
	for dir, entry := range fresh {
		old, existed := c.entries[dir]
		if existed {
			entry.CustomTitle = old.CustomTitle
			entry.Notes = old.Notes
			entry.Tags = old.Tags
			if old.sizeKnown && old.sourceStamp == entry.sourceStamp {
				entry.sizeBytes = old.sizeBytes
				entry.sizeKnown = true
			}
		}
		entry.finishDisplayFields()
		next[dir] = entry
		if !existed || entry != old {
			changes++
		}
	}
	for dir := range c.entries {
		if _, ok := next[dir]; !ok {
			changes++
		}
	}
	if changes > 0 {
		c.entries = next
		_ = c.saveLocked() // best effort; in-memory refresh remains useful
	}
	return changes
}

// reloadPersistedResearch notices catalogue edits written by a separate
// non-destructive import process while the server is running. The sidecar is
// atomically replaced, so every observed version is complete JSON.
func (c *catalog) reloadPersistedResearch() {
	stamp := fileStamp(c.path)
	c.mu.RLock()
	unchanged := stamp == c.persistedStamp
	c.mu.RUnlock()
	if unchanged || stamp == "-" {
		return
	}
	saved, err := c.load()
	c.mu.Lock()
	defer c.mu.Unlock()
	if fileStamp(c.path) != stamp {
		return // another edit won the race; the next refresh will load it
	}
	if err != nil {
		c.loadErr = err
		return
	}
	for dir, persisted := range saved {
		entry, ok := c.entries[dir]
		if !ok {
			continue
		}
		entry.CustomTitle = persisted.CustomTitle
		entry.Notes = persisted.Notes
		entry.Tags = persisted.Tags
		entry.finishDisplayFields()
		c.entries[dir] = entry
	}
	c.loadErr = nil
	c.persistedStamp = stamp
}

func normalizeCatalogTags(raw string) string {
	seen := make(map[string]bool)
	var tags []string
	for _, part := range strings.Split(raw, ",") {
		tag := strings.TrimSpace(part)
		key := strings.ToLower(tag)
		if tag == "" || seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, tag)
	}
	return strings.Join(tags, ", ")
}

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

// runBackground finishes any legacy size migration, then notices completed,
// changed, and removed bundles without requiring a server restart.
func (c *catalog) runBackground(ctx context.Context) {
	c.refreshSizes(ctx)
	interval := c.refreshInterval
	if interval <= 0 {
		interval = catalogRefreshInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if c.refreshSources() > 0 {
				c.refreshSizes(ctx)
			}
		}
	}
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
