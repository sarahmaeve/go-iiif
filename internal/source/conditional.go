package source

import "sync"

// CacheEntry holds the validators and body needed for a conditional GET so an
// unchanged resource costs a 304 instead of a full re-download (DESIGN §4.3).
type CacheEntry struct {
	ETag         string
	LastModified string
	Body         []byte
}

// ConditionalStore persists conditional-GET validators per URL. A durable
// implementation makes re-runs of a large crawl cheap.
type ConditionalStore interface {
	Get(url string) (CacheEntry, bool)
	Put(url string, e CacheEntry)
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

func (s *MemoryConditionalStore) Get(url string) (CacheEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[url]
	return e, ok
}

func (s *MemoryConditionalStore) Put(url string, e CacheEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[url] = e
}
