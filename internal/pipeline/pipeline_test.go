package pipeline

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/source"
)

// fakeSource yields a fixed list of manifest URLs.
type fakeSource []string

func (s fakeSource) Manifests(context.Context) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for _, u := range s {
			if !yield(u, nil) {
				return
			}
		}
	}
}

// fakeFetcher serves manifest JSON from a map.
type fakeFetcher map[string]string

func (f fakeFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	return []byte(f[url]), nil
}

func manifestJSON(lang, date string) string {
	return `{"@type":"sc:Manifest","metadata":[` +
		`{"label":"Language","value":"` + lang + `"},` +
		`{"label":"Date","value":"` + date + `"}]}`
}

// errFetcher errors for one specific URL, serving JSON for the rest.
type errFetcher struct {
	ok      fakeFetcher
	failURL string
}

func (e errFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	if url == e.failURL {
		return nil, source.ErrNotFound
	}
	return e.ok.Fetch(ctx, url)
}

func TestPipeline_FetchErrorDoesNotAbortRun(t *testing.T) {
	const (
		mGood = "https://h.example.org/good/manifest.json"
		mBad  = "https://h.example.org/bad/manifest.json"
	)
	p := New(Config{
		Source:  fakeSource{mBad, mGood},
		Fetcher: errFetcher{ok: fakeFetcher{mGood: manifestJSON("français", "1450")}, failURL: mBad},
		Mapping: metadata.FieldMapping{"language": metadata.FieldLanguage, "date": metadata.FieldDate},
		Filter:  metadata.Filter{Languages: []string{"fr"}},
	})

	results := map[string]Result{}
	for r := range p.Run(context.Background()) {
		results[r.ManifestURL] = r
	}

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (bad must not abort the run)", len(results))
	}
	bad := results[mBad]
	if bad.Err == nil || !errors.Is(bad.Err, source.ErrNotFound) {
		t.Fatalf("bad result Err = %v, want wrapping source.ErrNotFound", bad.Err)
	}
	if bad.Class != metadata.Uncertain {
		t.Fatalf("failed fetch Class = %v, want zero/Uncertain", bad.Class)
	}
	if good := results[mGood]; good.Err != nil || good.Class != metadata.Match {
		t.Fatalf("good result = %+v, want Match with no error", good)
	}
}

func TestPipeline_ResultCarriesManifestBytes(t *testing.T) {
	const m = "https://h.example.org/m/manifest.json"
	body := manifestJSON("français", "1450")
	p := New(Config{
		Source:  fakeSource{m},
		Fetcher: fakeFetcher{m: body},
		Mapping: metadata.FieldMapping{"language": metadata.FieldLanguage, "date": metadata.FieldDate},
		Filter:  metadata.Filter{Languages: []string{"fr"}},
	})

	var got Result
	for r := range p.Run(context.Background()) {
		got = r
	}
	if string(got.Manifest) != body {
		t.Fatalf("Result.Manifest = %q, want the fetched manifest bytes %q", got.Manifest, body)
	}
}

func TestPipeline_ClassifiesManifests(t *testing.T) {
	const (
		mMatch     = "https://h.example.org/match/manifest.json"
		mNoMatch   = "https://h.example.org/nomatch/manifest.json"
		mUncertain = "https://h.example.org/uncertain/manifest.json"
	)
	src := fakeSource{mMatch, mNoMatch, mUncertain}
	fetcher := fakeFetcher{
		mMatch:     manifestJSON("français", "1450"),
		mNoMatch:   manifestJSON("Latin", "1450"),
		mUncertain: `{"@type":"sc:Manifest","metadata":[{"label":"Date","value":"1450"}]}`,
	}

	p := New(Config{
		Source:  src,
		Fetcher: fetcher,
		Mapping: metadata.FieldMapping{"language": metadata.FieldLanguage, "date": metadata.FieldDate},
		Filter:  metadata.Filter{Languages: []string{"fr"}, Date: &metadata.DateRange{Start: 1400, End: 1500}},
	})

	got := map[string]metadata.Classification{}
	for r := range p.Run(context.Background()) {
		if r.Err != nil {
			t.Fatalf("unexpected result error for %s: %v", r.ManifestURL, r.Err)
		}
		got[r.ManifestURL] = r.Class
	}

	want := map[string]metadata.Classification{
		mMatch:     metadata.Match,
		mNoMatch:   metadata.NoMatch,
		mUncertain: metadata.Uncertain,
	}
	for url, wc := range want {
		if got[url] != wc {
			t.Fatalf("manifest %s classified %v, want %v", url, got[url], wc)
		}
	}
}
