package source

import (
	"context"
	"sync"
	"testing"
)

// recordingFetcher records the URLs it was asked to fetch.
type recordingFetcher struct {
	mu   sync.Mutex
	urls []string
	body string
}

func (r *recordingFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.urls = append(r.urls, url)
	return []byte(r.body), nil
}

// fakeLimiter records how many times Wait was called.
type fakeLimiter struct{ waits int }

func (f *fakeLimiter) Wait(context.Context) error {
	f.waits++
	return nil
}

func TestPoliteFetcher_PerHostRateLimiting(t *testing.T) {
	inner := &recordingFetcher{body: `{"ok":true}`}

	limiters := map[string]*fakeLimiter{}
	pf := NewPoliteFetcher(inner, WithRateLimiterFunc(func(host string) RateLimiter {
		l := &fakeLimiter{}
		limiters[host] = l
		return l
	}))

	ctx := context.Background()
	mustFetch := func(url string) {
		t.Helper()
		body, err := pf.Fetch(ctx, url)
		if err != nil {
			t.Fatalf("Fetch(%s): %v", url, err)
		}
		if string(body) != `{"ok":true}` {
			t.Fatalf("body = %q", body)
		}
	}

	mustFetch("https://a.example.org/1")
	mustFetch("https://a.example.org/2")
	mustFetch("https://b.example.org/1")

	if len(limiters) != 2 {
		t.Fatalf("expected one limiter per distinct host, got %d: %v", len(limiters), limiters)
	}
	if got := limiters["a.example.org"].waits; got != 2 {
		t.Fatalf("host a: Wait called %d times, want 2 (one per request, shared limiter)", got)
	}
	if got := limiters["b.example.org"].waits; got != 1 {
		t.Fatalf("host b: Wait called %d times, want 1", got)
	}
	if len(inner.urls) != 3 {
		t.Fatalf("inner fetched %d urls, want 3: %v", len(inner.urls), inner.urls)
	}
}
