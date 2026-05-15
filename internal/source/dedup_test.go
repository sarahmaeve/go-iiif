package source

import (
	"fmt"
	"sync"
	"testing"
)

func TestDeduper_SeenReportsDuplicates(t *testing.T) {
	d := NewDeduper()

	a := []byte("manifest-A")
	b := []byte("manifest-B")

	if d.Seen(a) {
		t.Fatal("first sighting of A reported as duplicate")
	}
	if !d.Seen(a) {
		t.Fatal("second sighting of A not reported as duplicate")
	}
	if d.Seen(b) {
		t.Fatal("first sighting of B reported as duplicate")
	}
	// Identical content under a different retrieval is still a duplicate.
	if !d.Seen([]byte("manifest-A")) {
		t.Fatal("equal content not detected as duplicate")
	}
}

func TestDeduper_ConcurrentSafe(t *testing.T) {
	d := NewDeduper()
	const goroutines = 50

	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Go(func() {
			d.Seen(fmt.Appendf(nil, "content-%d", i%5))
		})
	}
	wg.Wait()
	// 5 distinct contents: each must now report as seen.
	for i := range 5 {
		if !d.Seen(fmt.Appendf(nil, "content-%d", i)) {
			t.Fatalf("content-%d not recorded under concurrent access", i)
		}
	}
}
