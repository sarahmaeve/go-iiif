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
	"testing"
	"time"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/source"
)

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
				Source:  fakeSource{tc.url},
				Fetcher: fetcher,
				Mapping: tc.mapping,
				Filter:  tc.filter,
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
