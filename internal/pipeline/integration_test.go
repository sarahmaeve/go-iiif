//go:build integration

// Live dogfood: exercises the real HTTPS fetch path end-to-end against the
// pilot institutions. Excluded from the default build; run explicitly with:
//
//	go test -tags=integration ./internal/pipeline/
//
// It fetches only two already-known manifest URLs (no recursive crawl) and
// goes through the polite fetcher, so it stays a courteous ~2 requests.
package pipeline

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/source"
)

// stampFetcher records the wall-clock interval of every Fetch so a test can
// prove two requests to different hosts actually overlapped in time.
type stampFetcher struct {
	inner source.Fetcher
	mu    sync.Mutex
	spans map[string][2]time.Time // url → [start, end]
}

func (s *stampFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	start := time.Now()
	body, err := s.inner.Fetch(ctx, url)
	end := time.Now()
	s.mu.Lock()
	s.spans[url] = [2]time.Time{start, end}
	s.mu.Unlock()
	return body, err
}

func TestIntegration_LivePilotManifests(t *testing.T) {
	const (
		gallica  = "https://gallica.bnf.fr/iiif/ark:/12148/btv1b9059632h/manifest.json"
		bodleian = "https://iiif.bodleian.ox.ac.uk/iiif/manifest/f317ad0c-a35b-4e9f-8426-c71f215d382d.json"
	)

	// Real HTTPS fetcher (browser-like UA — Gallica 403s the Go default),
	// wrapped in the polite layer.
	fetcher := source.NewPoliteFetcher(source.NewHTTPFetcher())

	cases := []struct {
		name     string
		url      string
		mapping  metadata.FieldMapping
		filter   metadata.Filter
		wantRec  metadata.WorkRecord
		wantClsf metadata.Classification
	}{
		{
			name:    "Gallica Français 2814",
			url:     gallica,
			mapping: metadata.FieldMapping{"date": metadata.FieldDate, "language": metadata.FieldLanguage},
			filter:  metadata.Filter{Languages: []string{"fr"}, Date: &metadata.DateRange{Start: 1300, End: 1400}},
			wantRec: metadata.WorkRecord{
				Langs:     []string{"fr"},
				DateRange: metadata.DateRange{Start: 1301, End: 1400},
			},
			wantClsf: metadata.Match,
		},
		{
			name: "Digital Bodleian C 4.8(1) Linc.",
			url:  bodleian,
			mapping: metadata.FieldMapping{
				"date statement":  metadata.FieldDate,
				"language":        metadata.FieldLanguage,
				"place of origin": metadata.FieldOrigin,
			},
			filter: metadata.Filter{Languages: []string{"la"}, Date: &metadata.DateRange{Start: 1500, End: 1550}, Places: []string{"Venice"}},
			wantRec: metadata.WorkRecord{
				Langs:     []string{"la"},
				DateRange: metadata.DateRange{Start: 1506, End: 1506},
				Origin:    "Italy, Venice",
			},
			wantClsf: metadata.Match,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			p := New(Config{
				Source:       fakeSource{tc.url},
				Fetcher:      fetcher,
				Institutions: regWith(tc.mapping),
				Filter:       tc.filter,
			})

			var got []Result
			for r := range p.Run(ctx) {
				got = append(got, r)
			}
			if len(got) != 1 {
				t.Fatalf("got %d results, want 1", len(got))
			}
			r := got[0]
			if r.Err != nil {
				t.Fatalf("live fetch/parse failed: %v", r.Err)
			}
			if r.Class != tc.wantClsf {
				t.Fatalf("classification = %v, want %v (record %+v)", r.Class, tc.wantClsf, r.Record)
			}
			if got, want := r.Record.DateRange, tc.wantRec.DateRange; got != want {
				t.Fatalf("DateRange = %+v, want %+v", got, want)
			}
			if len(r.Record.Langs) != len(tc.wantRec.Langs) || (len(tc.wantRec.Langs) == 1 && r.Record.Langs[0] != tc.wantRec.Langs[0]) {
				t.Fatalf("Langs = %v, want %v", r.Record.Langs, tc.wantRec.Langs)
			}
			if r.Record.Origin != tc.wantRec.Origin {
				t.Fatalf("Origin = %q, want %q", r.Record.Origin, tc.wantRec.Origin)
			}
			t.Logf("OK %s → %v, record=%+v", tc.name, r.Class, r.Record)
		})
	}
}

// TestIntegration_ConcurrentMultiHost runs the concurrent pipeline over
// manifests from two different institutions/hosts and proves the two live
// fetches overlapped in time — the cross-host parallelism concurrency exists
// for. Per-host politeness is untouched (one request per host here).
func TestIntegration_ConcurrentMultiHost(t *testing.T) {
	const (
		gallica  = "https://gallica.bnf.fr/iiif/ark:/12148/btv1b9059632h/manifest.json"
		bodleian = "https://iiif.bodleian.ox.ac.uk/iiif/manifest/f317ad0c-a35b-4e9f-8426-c71f215d382d.json"
	)

	sf := &stampFetcher{
		inner: source.NewPoliteFetcher(source.NewHTTPFetcher()),
		spans: make(map[string][2]time.Time),
	}

	// Union mapping: the two institutions use disjoint labels, so one
	// mapping correctly normalizes both.
	mapping := metadata.FieldMapping{
		"date":            metadata.FieldDate,
		"language":        metadata.FieldLanguage,
		"date statement":  metadata.FieldDate,
		"place of origin": metadata.FieldOrigin,
	}

	p := New(Config{
		Source:       fakeSource{gallica, bodleian},
		Fetcher:      sf,
		Institutions: regWith(mapping),
		Filter:       metadata.Filter{}, // no constraint: assert extraction, not selection
		Workers:      2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got := map[string]Result{}
	start := time.Now()
	for r := range p.Run(ctx) {
		got[r.ManifestURL] = r
	}
	elapsed := time.Since(start)

	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if g := got[gallica]; g.Err != nil || len(g.Record.Langs) != 1 || g.Record.Langs[0] != "fr" ||
		g.Record.DateRange != (metadata.DateRange{Start: 1301, End: 1400}) {
		t.Fatalf("Gallica result wrong: %+v err=%v", g.Record, g.Err)
	}
	if b := got[bodleian]; b.Err != nil || len(b.Record.Langs) != 1 || b.Record.Langs[0] != "la" ||
		b.Record.Origin != "Italy, Venice" {
		t.Fatalf("Bodleian result wrong: %+v err=%v", b.Record, b.Err)
	}

	// Proof of cross-host overlap: intervals intersect iff
	// aStart < bEnd && bStart < aEnd.
	a, b := sf.spans[gallica], sf.spans[bodleian]
	if !(a[0].Before(b[1]) && b[0].Before(a[1])) {
		t.Fatalf("fetches did not overlap (ran serially):\n  gallica  %v–%v\n  bodleian %v–%v",
			a[0], a[1], b[0], b[1])
	}
	t.Logf("OK: concurrent multi-host run, both correct, fetches overlapped, total %v", elapsed)
}
