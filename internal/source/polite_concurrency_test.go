package source

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// gateFetcher blocks each Fetch on a shared channel so the test can hold
// requests in-flight and observe peak concurrency.
type gateFetcher struct {
	release chan struct{}
	active  atomic.Int32
	maxSeen atomic.Int32
	entered chan struct{}
}

func (g *gateFetcher) Fetch(context.Context, string) ([]byte, error) {
	n := g.active.Add(1)
	for {
		m := g.maxSeen.Load()
		if n <= m || g.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	g.entered <- struct{}{}
	<-g.release
	g.active.Add(-1)
	return []byte("ok"), nil
}

func TestPoliteFetcher_GlobalConcurrencyCap(t *testing.T) {
	defer goleak.VerifyNone(t)

	const cap = 2
	const callers = 5

	gate := &gateFetcher{
		release: make(chan struct{}),
		entered: make(chan struct{}, callers),
	}
	pf := NewPoliteFetcher(gate,
		WithMaxConcurrent(cap),
		WithRateLimiterFunc(func(string) RateLimiter { return noWaitLimiter{} }),
	)

	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			_, _ = pf.Fetch(context.Background(), "https://h.example.org/x")
		})
	}

	// Let the cap-sized first wave enter, then release everything.
	for range callers {
		select {
		case <-gate.entered:
		case <-time.After(2 * time.Second):
			// Expected once the cap is enforced: not all callers can enter.
		}
		gate.release <- struct{}{}
	}
	wg.Wait()

	if peak := gate.maxSeen.Load(); peak > cap {
		t.Fatalf("peak concurrency = %d, want <= %d", peak, cap)
	}
}

type noWaitLimiter struct{}

func (noWaitLimiter) Wait(context.Context) error { return nil }
