package serve

import "sync"

type cachedManifest struct {
	stamp string
	body  []byte
}

// manifestCache keeps serve-time localized manifests in memory. The cache
// key includes the request base URL; the stamp includes manifest and
// provenance metadata, so a completed re-preservation invalidates naturally.
type manifestCache struct {
	mu      sync.RWMutex
	entries map[string]cachedManifest
}

func (c *manifestCache) get(key, stamp string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || entry.stamp != stamp {
		return nil, false
	}
	return entry.body, true
}

func (c *manifestCache) put(key, stamp string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]cachedManifest)
	}
	c.entries[key] = cachedManifest{stamp: stamp, body: body}
}
