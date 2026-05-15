package source

import (
	"context"
	"errors"
	"testing"
	"time"
)

// scriptedFetcher returns the next programmed result on each call.
type scriptedFetcher struct {
	results []scriptResult
	calls   int
}

type scriptResult struct {
	body []byte
	err  error
}

func (s *scriptedFetcher) Fetch(context.Context, string) ([]byte, error) {
	r := s.results[min(s.calls, len(s.results)-1)]
	s.calls++
	return r.body, r.err
}

func TestPoliteFetcher_RetriesTransientStatuses(t *testing.T) {
	var slept []time.Duration
	fakeSleep := func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}

	inner := &scriptedFetcher{results: []scriptResult{
		{err: &HTTPStatusError{Code: 503}},
		{err: &HTTPStatusError{Code: 429}},
		{body: []byte("finally ok")},
	}}

	pf := NewPoliteFetcher(inner,
		WithRateLimiterFunc(func(string) RateLimiter { return noWaitLimiter{} }),
		WithRetry(5, 100*time.Millisecond),
		WithSleeper(fakeSleep),
	)

	body, err := pf.Fetch(context.Background(), "https://h.example.org/x")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != "finally ok" {
		t.Fatalf("body = %q, want %q", body, "finally ok")
	}
	if inner.calls != 3 {
		t.Fatalf("inner called %d times, want 3 (2 transient + 1 success)", inner.calls)
	}
	if len(slept) != 2 {
		t.Fatalf("slept %d times, want 2", len(slept))
	}
	if !(slept[0] == 100*time.Millisecond && slept[1] == 200*time.Millisecond) {
		t.Fatalf("backoff = %v, want exponential [100ms 200ms]", slept)
	}
}

func TestPoliteFetcher_HonorsRetryAfterOverExponential(t *testing.T) {
	var slept []time.Duration
	inner := &scriptedFetcher{results: []scriptResult{
		{err: &HTTPStatusError{Code: 503, RetryAfter: 5 * time.Second}},
		{body: []byte("ok")},
	}}
	pf := NewPoliteFetcher(inner,
		WithRateLimiterFunc(func(string) RateLimiter { return noWaitLimiter{} }),
		WithRetry(3, 100*time.Millisecond),
		WithSleeper(func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		}),
	)

	if _, err := pf.Fetch(context.Background(), "https://h.example.org/x"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(slept) != 1 || slept[0] != 5*time.Second {
		t.Fatalf("backoff = %v, want server Retry-After [5s], not exponential base", slept)
	}
}

func TestPoliteFetcher_NonRetryableReturnsImmediately(t *testing.T) {
	inner := &scriptedFetcher{results: []scriptResult{{err: &HTTPStatusError{Code: 500}}}}
	pf := NewPoliteFetcher(inner,
		WithRateLimiterFunc(func(string) RateLimiter { return noWaitLimiter{} }),
		WithRetry(5, time.Millisecond),
		WithSleeper(func(context.Context, time.Duration) error { return nil }),
	)

	_, err := pf.Fetch(context.Background(), "https://h.example.org/x")
	var se *HTTPStatusError
	if !errors.As(err, &se) || se.Code != 500 {
		t.Fatalf("err = %v, want HTTPStatusError 500", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner called %d times, want 1 (500 is not retryable)", inner.calls)
	}
}

func TestPoliteFetcher_RetryExhaustionReturnsLastError(t *testing.T) {
	inner := &scriptedFetcher{results: []scriptResult{{err: &HTTPStatusError{Code: 503}}}}
	pf := NewPoliteFetcher(inner,
		WithRateLimiterFunc(func(string) RateLimiter { return noWaitLimiter{} }),
		WithRetry(3, time.Millisecond),
		WithSleeper(func(context.Context, time.Duration) error { return nil }),
	)

	_, err := pf.Fetch(context.Background(), "https://h.example.org/x")
	var se *HTTPStatusError
	if !errors.As(err, &se) || se.Code != 503 {
		t.Fatalf("err = %v, want final HTTPStatusError 503", err)
	}
	if inner.calls != 3 {
		t.Fatalf("inner called %d times, want 3 (max attempts)", inner.calls)
	}
}
