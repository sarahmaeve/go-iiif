package source

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"
)

// RateLimiter gates outbound requests. Satisfied by *rate.Limiter; faked in
// tests to keep them deterministic.
type RateLimiter interface {
	Wait(ctx context.Context) error
}

// Politeness defaults live in DefaultRatePolicy: a gentle per-host baseline
// plus institution-specific overrides (Gallica is strongly rate-sensitive).
// This is a preservation crawler hitting cultural-heritage institutions, not
// a scraper.

// PoliteFetcher decorates a Fetcher with the politeness controls from DESIGN
// §4.3. It implements Fetcher, so it composes transparently with
// CollectionSource.
type PoliteFetcher struct {
	inner      Fetcher
	newLimiter func(host string) RateLimiter

	mu       sync.Mutex
	limiters map[string]RateLimiter

	sem chan struct{} // global concurrency cap; nil = unbounded

	maxAttempts int
	baseDelay   time.Duration
	sleep       func(ctx context.Context, d time.Duration) error
}

// retryableStatuses are the transient HTTP statuses worth retrying with
// backoff (DESIGN §4.3).
var retryableStatuses = map[int]bool{429: true, 503: true}

// PoliteOption configures a PoliteFetcher.
type PoliteOption func(*PoliteFetcher)

// WithRateLimiterFunc overrides how a per-host RateLimiter is constructed
// (used in tests to inject a deterministic fake).
func WithRateLimiterFunc(fn func(host string) RateLimiter) PoliteOption {
	return func(p *PoliteFetcher) { p.newLimiter = fn }
}

// WithRatePolicy sets per-host politeness policies, replacing the built-in
// DefaultRatePolicy.
func WithRatePolicy(rp RatePolicy) PoliteOption {
	return func(p *PoliteFetcher) {
		p.newLimiter = func(host string) RateLimiter {
			return newRateLimiter(rp.For(host))
		}
	}
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

// WithRetry sets the maximum number of attempts (>=1) and the base backoff
// delay. Backoff is exponential: base, 2*base, 4*base, ...
func WithRetry(maxAttempts int, baseDelay time.Duration) PoliteOption {
	return func(p *PoliteFetcher) {
		if maxAttempts >= 1 {
			p.maxAttempts = maxAttempts
		}
		if baseDelay > 0 {
			p.baseDelay = baseDelay
		}
	}
}

// WithSleeper overrides the backoff sleep (tests inject a non-blocking,
// recording sleeper).
func WithSleeper(fn func(ctx context.Context, d time.Duration) error) PoliteOption {
	return func(p *PoliteFetcher) { p.sleep = fn }
}

// NewPoliteFetcher wraps inner with per-host rate limiting.
func NewPoliteFetcher(inner Fetcher, opts ...PoliteOption) *PoliteFetcher {
	p := &PoliteFetcher{
		inner:       inner,
		limiters:    make(map[string]RateLimiter),
		maxAttempts: 1,
		baseDelay:   time.Second,
		sleep:       sleepCtx,
		newLimiter: func(host string) RateLimiter {
			return newRateLimiter(DefaultRatePolicy().For(host))
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

// Fetch applies the per-host rate limit and global concurrency cap, then
// delegates to the inner Fetcher, retrying transient 429/503 responses with
// exponential backoff (honoring Retry-After when the server provides it).
func (p *PoliteFetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("source: parsing %q: %w", rawURL, err)
	}

	// One concurrency slot for the whole operation, including backoff waits.
	if p.sem != nil {
		select {
		case p.sem <- struct{}{}:
			defer func() { <-p.sem }()
		case <-ctx.Done():
			return nil, fmt.Errorf("source: waiting for concurrency slot: %w", ctx.Err())
		}
	}

	limiter := p.limiterFor(u.Host)
	var lastErr error
	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		if err := limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("source: rate limiter wait for %s: %w", u.Host, err)
		}

		body, err := p.inner.Fetch(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err

		retryAfter, ok := retryableDelay(err)
		if !ok || attempt == p.maxAttempts {
			return nil, err
		}

		delay := retryAfter
		if delay <= 0 {
			delay = p.baseDelay << (attempt - 1) // base, 2*base, 4*base, ...
		}
		if err := p.sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

// retryableDelay reports whether err is a transient HTTP status worth
// retrying, and any server-provided Retry-After delay.
func retryableDelay(err error) (time.Duration, bool) {
	var se *HTTPStatusError
	if errors.As(err, &se) && retryableStatuses[se.Code] {
		return se.RetryAfter, true
	}
	return 0, false
}

// sleepCtx is the default backoff sleeper: it waits d or returns early if ctx
// is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
