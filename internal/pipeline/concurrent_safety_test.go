package pipeline

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
)

// assertGoroutinesSettle fails if the live goroutine count does not return to
// (at most) base within a short window — a dependency-free leak check.
func assertGoroutinesSettle(t *testing.T, base int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.GC()
		if n := runtime.NumGoroutine(); n <= base {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("goroutines did not settle: have %d, base %d", n, base)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// blockingCtxFetcher models a real fetcher: it blocks until the context is
// cancelled, then returns. Mirrors HTTPFetcher/PoliteFetcher ctx-awareness.
type blockingCtxFetcher struct{}

func (blockingCtxFetcher) Fetch(ctx context.Context, _ string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func bigSource(n int) fakeSource {
	s := make(fakeSource, 0, n)
	for i := range n {
		s = append(s, "https://h.example.org/"+string(rune('a'+i%26))+string(rune('0'+i/26))+"/m.json")
	}
	return s
}

func TestPipeline_ConcurrentEarlyBreakNoLeak(t *testing.T) {
	base := runtime.NumGoroutine()

	p := New(Config{
		Source:  bigSource(50),
		Fetcher: fakeFetcher{}, // returns "" → ExtractV2Metadata errors → Result{Err}
		Mapping: metadata.FieldMapping{},
		Filter:  metadata.Filter{},
		Workers: 4,
	})

	for range p.Run(context.Background()) {
		break // consumer stops after the very first result
	}

	assertGoroutinesSettle(t, base)
}

func TestPipeline_ConcurrentContextCancelStops(t *testing.T) {
	base := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	p := New(Config{
		Source:  bigSource(50),
		Fetcher: blockingCtxFetcher{}, // every worker parks until ctx done
		Mapping: metadata.FieldMapping{},
		Filter:  metadata.Filter{},
		Workers: 4,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range p.Run(ctx) {
		}
	}()

	cancel() // mid-flight cancellation

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
	assertGoroutinesSettle(t, base)
}
