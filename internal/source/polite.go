package source

import (
	"context"
	"fmt"
	"net/url"
	"sync"

	"golang.org/x/time/rate"
)

// RateLimiter gates outbound requests. Satisfied by *rate.Limiter; faked in
// tests to keep them deterministic.
type RateLimiter interface {
	Wait(ctx context.Context) error
}

// defaultPerHostRPS / defaultPerHostBurst are intentionally gentle: this is a
// preservation crawler hitting cultural-heritage institutions, not a scraper.
const (
	defaultPerHostRPS   = 1.0
	defaultPerHostBurst = 1
)

// PoliteFetcher decorates a Fetcher with the politeness controls from DESIGN
// §4.3. It implements Fetcher, so it composes transparently with
// CollectionSource.
type PoliteFetcher struct {
	inner      Fetcher
	newLimiter func(host string) RateLimiter

	mu       sync.Mutex
	limiters map[string]RateLimiter

	sem chan struct{} // global concurrency cap; nil = unbounded
}

// PoliteOption configures a PoliteFetcher.
type PoliteOption func(*PoliteFetcher)

// WithRateLimiterFunc overrides how a per-host RateLimiter is constructed
// (used in tests to inject a deterministic fake).
func WithRateLimiterFunc(fn func(host string) RateLimiter) PoliteOption {
	return func(p *PoliteFetcher) { p.newLimiter = fn }
}

// WithMaxConcurrent caps the total number of in-flight fetches across all
// hosts. n <= 0 means unbounded.
func WithMaxConcurrent(n int) PoliteOption {
	return func(p *PoliteFetcher) {
		if n > 0 {
			p.sem = make(chan struct{}, n)
		}
	}
}

// NewPoliteFetcher wraps inner with per-host rate limiting.
func NewPoliteFetcher(inner Fetcher, opts ...PoliteOption) *PoliteFetcher {
	p := &PoliteFetcher{
		inner:    inner,
		limiters: make(map[string]RateLimiter),
		newLimiter: func(string) RateLimiter {
			return rate.NewLimiter(rate.Limit(defaultPerHostRPS), defaultPerHostBurst)
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// limiterFor returns the shared RateLimiter for host, creating it once.
func (p *PoliteFetcher) limiterFor(host string) RateLimiter {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, ok := p.limiters[host]
	if !ok {
		l = p.newLimiter(host)
		p.limiters[host] = l
	}
	return l
}

// Fetch waits for the per-host rate limiter, then delegates to the inner
// Fetcher.
func (p *PoliteFetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("source: parsing %q: %w", rawURL, err)
	}
	if err := p.limiterFor(u.Host).Wait(ctx); err != nil {
		return nil, fmt.Errorf("source: rate limiter wait for %s: %w", u.Host, err)
	}

	if p.sem != nil {
		select {
		case p.sem <- struct{}{}:
			defer func() { <-p.sem }()
		case <-ctx.Done():
			return nil, fmt.Errorf("source: waiting for concurrency slot: %w", ctx.Err())
		}
	}

	return p.inner.Fetch(ctx, rawURL)
}
