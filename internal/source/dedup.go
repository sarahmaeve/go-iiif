package source

import (
	"crypto/sha256"
	"sync"
)

// Deduper detects content already seen during a crawl, so the same bytes
// fetched under different URLs (mirrors, duplicates) are processed once
// (DESIGN §4.3). It keys on the SHA-256 of the content, not the URL.
type Deduper struct {
	mu   sync.Mutex
	seen map[[sha256.Size]byte]struct{}
}

// NewDeduper returns an empty Deduper.
func NewDeduper() *Deduper {
	return &Deduper{seen: make(map[[sha256.Size]byte]struct{})}
}

// Seen records content and reports whether it had been seen before. The first
// call for given content returns false; subsequent calls for equal content
// return true.
func (d *Deduper) Seen(content []byte) bool {
	sum := sha256.Sum256(content)
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[sum]; ok {
		return true
	}
	d.seen[sum] = struct{}{}
	return false
}
