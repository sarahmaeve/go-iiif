package source

import (
	"bufio"
	"context"
	"fmt"
	"iter"
	"os"
	"strings"
	"sync"
)

// Journal records which manifest URLs a crawl has fully completed, so an
// interrupted run can restart where it stopped (DESIGN §4.3).
type Journal interface {
	Done(manifestURL string) bool
	MarkDone(manifestURL string) error
}

// MemoryJournal is a non-durable in-memory Journal (tests, ephemeral runs).
type MemoryJournal struct {
	mu   sync.RWMutex
	done map[string]struct{}
}

// NewMemoryJournal returns an empty in-memory Journal.
func NewMemoryJournal() *MemoryJournal {
	return &MemoryJournal{done: make(map[string]struct{})}
}

func (j *MemoryJournal) Done(u string) bool {
	j.mu.RLock()
	defer j.mu.RUnlock()
	_, ok := j.done[u]
	return ok
}

func (j *MemoryJournal) MarkDone(u string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.done[u] = struct{}{}
	return nil
}

// FileJournal is a durable, append-only Journal: one completed URL per line.
// Reopening the same path restores prior completions.
type FileJournal struct {
	mu   sync.Mutex
	f    *os.File
	done map[string]struct{}
}

// OpenFileJournal opens (creating if needed) the journal at path and loads
// any previously recorded completions.
func OpenFileJournal(path string) (*FileJournal, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("source: opening journal %s: %w", path, err)
	}
	j := &FileJournal{f: f, done: make(map[string]struct{})}

	if _, err := f.Seek(0, 0); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("source: rewinding journal %s: %w", path, err)
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			j.done[line] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("source: reading journal %s: %w", path, err)
	}
	return j, nil
}

func (j *FileJournal) Done(u string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	_, ok := j.done[u]
	return ok
}

func (j *FileJournal) MarkDone(u string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.done[u]; ok {
		return nil
	}
	if _, err := fmt.Fprintln(j.f, u); err != nil {
		return fmt.Errorf("source: appending to journal: %w", err)
	}
	if err := j.f.Sync(); err != nil {
		return fmt.Errorf("source: syncing journal: %w", err)
	}
	j.done[u] = struct{}{}
	return nil
}

// Close releases the underlying journal file.
func (j *FileJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.f.Close()
}

// ResumableSource decorates a Source, skipping manifest URLs the Journal
// already records as completed.
type ResumableSource struct {
	inner   Source
	journal Journal
}

// NewResumableSource returns a Source that yields only not-yet-completed
// manifests from inner.
func NewResumableSource(inner Source, j Journal) *ResumableSource {
	return &ResumableSource{inner: inner, journal: j}
}

// Manifests yields each manifest URL from the inner Source that the Journal
// has not already marked done.
func (r *ResumableSource) Manifests(ctx context.Context) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for u, err := range r.inner.Manifests(ctx) {
			if err != nil {
				if !yield("", err) {
					return
				}
				continue
			}
			if r.journal.Done(u) {
				continue
			}
			if !yield(u, nil) {
				return
			}
		}
	}
}
