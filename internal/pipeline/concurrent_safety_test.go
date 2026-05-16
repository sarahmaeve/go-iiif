package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"go.uber.org/goleak"
)

// blockingCtxFetcher models a real fetcher: it blocks until the context is
// cancelled, then returns. Mirrors HTTPFetcher/PoliteFetcher ctx-awareness so
// the cancellation path is genuinely exercised.
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
	defer goleak.VerifyNone(t)

	p := New(Config{
		Source:       bigSource(50),
		Fetcher:      fakeFetcher{}, // returns "" → ExtractV2Metadata errors → Result{Err}
		Institutions: regWith(metadata.FieldMapping{}),
		Filter:       metadata.Filter{},
		Workers:      4,
	})

	for range p.Run(context.Background()) {
		break // consumer stops after the very first result
	}
}

func TestPipeline_ConcurrentContextCancelStops(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx, cancel := context.WithCancel(context.Background())
	p := New(Config{
		Source:       bigSource(50),
		Fetcher:      blockingCtxFetcher{}, // every worker parks until ctx done
		Institutions: regWith(metadata.FieldMapping{}),
		Filter:       metadata.Filter{},
		Workers:      4,
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
}
