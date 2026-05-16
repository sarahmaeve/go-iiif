package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
)

// barrierFetcher blocks every Fetch until release is closed, so the test can
// hold many calls in flight at once and observe peak concurrency.
type barrierFetcher struct {
	body    string
	active  atomic.Int32
	peak    atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (b *barrierFetcher) Fetch(context.Context, string) ([]byte, error) {
	n := b.active.Add(1)
	for {
		p := b.peak.Load()
		if n <= p || b.peak.CompareAndSwap(p, n) {
			break
		}
	}
	b.entered <- struct{}{}
	<-b.release
	b.active.Add(-1)
	return []byte(b.body), nil
}

func TestPipeline_ConcurrentFanOut(t *testing.T) {
	const (
		n       = 6
		workers = 4
	)
	urls := make(fakeSource, 0, n)
	for i := range n {
		urls = append(urls, "https://h.example.org/"+string(rune('a'+i))+"/manifest.json")
	}

	bf := &barrierFetcher{
		body:    manifestJSON("français", "1450"),
		entered: make(chan struct{}, n),
		release: make(chan struct{}),
	}

	p := New(Config{
		Source:       urls,
		Fetcher:      bf,
		Institutions: regWith(metadata.FieldMapping{"language": metadata.FieldLanguage, "date": metadata.FieldDate}),
		Filter:       metadata.Filter{Languages: []string{"fr"}},
		Workers:      workers,
	})

	var mu sync.Mutex
	got := map[string]metadata.Classification{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for r := range p.Run(context.Background()) {
			mu.Lock()
			got[r.ManifestURL] = r.Class
			mu.Unlock()
		}
	}()

	// Wait until a full worker-sized wave is simultaneously in flight.
	for range workers {
		<-bf.entered
	}
	if peak := bf.peak.Load(); peak < 2 {
		t.Fatalf("peak concurrency = %d, want >= 2 (fan-out not happening)", peak)
	}
	close(bf.release)
	<-done

	if bf.peak.Load() > workers {
		t.Fatalf("peak concurrency = %d, want <= %d", bf.peak.Load(), workers)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != n {
		t.Fatalf("got %d results, want %d", len(got), n)
	}
	for u, c := range got {
		if c != metadata.Match {
			t.Fatalf("%s classified %v, want Match", u, c)
		}
	}
}
