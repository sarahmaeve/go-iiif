package source

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	conditionalCacheVersion = 1
	maxConditionalBodyBytes = 16 << 20
)

// CacheEntry holds the validators and body needed for a conditional GET so an
// unchanged resource costs a 304 instead of a full re-download (DESIGN §4.3).
type CacheEntry struct {
	ETag         string
	LastModified string
	ContentType  string
	Body         []byte
}

// ConditionalStore persists conditional-GET validators per URL. A durable
// implementation makes re-runs of a large crawl cheap.
type ConditionalStore interface {
	Get(url string) (CacheEntry, bool, error)
	Put(url string, e CacheEntry) error
}

// MemoryConditionalStore is a goroutine-safe in-memory ConditionalStore.
type MemoryConditionalStore struct {
	mu sync.RWMutex
	m  map[string]CacheEntry
}

// NewMemoryConditionalStore returns an empty in-memory store.
func NewMemoryConditionalStore() *MemoryConditionalStore {
	return &MemoryConditionalStore{m: make(map[string]CacheEntry)}
}

func (s *MemoryConditionalStore) Get(url string) (CacheEntry, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[url]
	return e, ok, nil
}

func (s *MemoryConditionalStore) Put(url string, e CacheEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[url] = e
	return nil
}

// FileConditionalStore keeps one versioned, URL-keyed cache record per file.
// The response-size bound deliberately admits collection and manifest JSON but
// excludes preservation images, whose durable cache is the library itself.
type FileConditionalStore struct {
	dir string
	mu  sync.RWMutex
}

type conditionalCacheRecord struct {
	Version      int    `json:"version"`
	URL          string `json:"url"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	Body         []byte `json:"body"`
}

// NewFileConditionalStore opens a durable conditional-response cache below
// dir. Records are created lazily by Put.
func NewFileConditionalStore(dir string) (*FileConditionalStore, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("source: creating HTTP validation cache: %w", err)
	}
	return &FileConditionalStore{dir: dir}, nil
}

func (s *FileConditionalStore) path(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

func (s *FileConditionalStore) Get(rawURL string) (CacheEntry, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, err := os.ReadFile(s.path(rawURL)) //nolint:gosec // SHA-256-derived filename under configured cache root
	if errors.Is(err, os.ErrNotExist) {
		return CacheEntry{}, false, nil
	}
	if err != nil {
		return CacheEntry{}, false, fmt.Errorf("source: reading HTTP validation cache: %w", err)
	}
	var record conditionalCacheRecord
	if err := json.Unmarshal(b, &record); err != nil {
		return CacheEntry{}, false, fmt.Errorf("source: decoding HTTP validation cache: %w", err)
	}
	if record.Version != conditionalCacheVersion {
		return CacheEntry{}, false, fmt.Errorf("source: unsupported HTTP validation cache version %d", record.Version)
	}
	if record.URL != rawURL {
		return CacheEntry{}, false, errors.New("source: HTTP validation cache URL digest collision")
	}
	if len(record.Body) > maxConditionalBodyBytes {
		return CacheEntry{}, false, fmt.Errorf("source: HTTP validation cache body exceeds %d bytes", maxConditionalBodyBytes)
	}
	return CacheEntry{ETag: record.ETag, LastModified: record.LastModified, ContentType: record.ContentType, Body: record.Body}, true, nil
}

func (s *FileConditionalStore) Put(rawURL string, entry CacheEntry) error {
	if len(entry.Body) > maxConditionalBodyBytes {
		return nil
	}
	record := conditionalCacheRecord{
		Version: conditionalCacheVersion, URL: rawURL, ETag: entry.ETag,
		LastModified: entry.LastModified, ContentType: entry.ContentType, Body: entry.Body,
	}
	b, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("source: encoding HTTP validation cache: %w", err)
	}
	b = append(b, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp, err := os.CreateTemp(s.dir, ".http-cache-*.tmp")
	if err != nil {
		return fmt.Errorf("source: creating HTTP validation cache record: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("source: writing HTTP validation cache record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("source: closing HTTP validation cache record: %w", err)
	}
	if err := os.Rename(tmpName, s.path(rawURL)); err != nil {
		return fmt.Errorf("source: finalizing HTTP validation cache record: %w", err)
	}
	return nil
}
