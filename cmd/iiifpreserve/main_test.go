package main

import (
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/pipeline"
)

func TestParseArgs(t *testing.T) {
	t.Run("collection is required", func(t *testing.T) {
		if _, err := parseArgs([]string{"-lang", "fr"}); err == nil {
			t.Fatal("expected error when -collection is missing")
		}
	})

	t.Run("parses all flags", func(t *testing.T) {
		o, err := parseArgs([]string{
			"-collection", "https://example.org/c/top",
			"-lang", "fr,la",
			"-from", "1400", "-to", "1500",
			"-place", "Venice,Paris",
			"-max", "7",
			"-workers", "8",
			"-preserve", "/tmp/out",
			"-serve", "127.0.0.1:8443",
			"-tls-cert", "/c.pem", "-tls-key", "/k.pem",
		})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if o.collection != "https://example.org/c/top" {
			t.Fatalf("collection = %q", o.collection)
		}
		if o.max != 7 {
			t.Fatalf("max = %d, want 7", o.max)
		}
		if o.workers != 8 {
			t.Fatalf("workers = %d, want 8", o.workers)
		}
		if o.preserve != "/tmp/out" {
			t.Fatalf("preserve = %q, want /tmp/out", o.preserve)
		}
		if o.serve != "127.0.0.1:8443" || o.tlsCert != "/c.pem" || o.tlsKey != "/k.pem" {
			t.Fatalf("serve flags = %q %q %q", o.serve, o.tlsCert, o.tlsKey)
		}
		f := o.filter()
		wantLangs := []string{"fr", "la"}
		if len(f.Languages) != 2 || f.Languages[0] != wantLangs[0] || f.Languages[1] != wantLangs[1] {
			t.Fatalf("Languages = %v, want %v", f.Languages, wantLangs)
		}
		if f.Date == nil || *f.Date != (metadata.DateRange{Start: 1400, End: 1500}) {
			t.Fatalf("Date = %+v, want {1400 1500}", f.Date)
		}
		if len(f.Places) != 2 || f.Places[0] != "Venice" {
			t.Fatalf("Places = %v", f.Places)
		}
	})

	t.Run("no date flags means no date constraint", func(t *testing.T) {
		o, err := parseArgs([]string{"-collection", "https://example.org/c/top", "-lang", "fr"})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if o.filter().Date != nil {
			t.Fatal("Date should be nil when -from/-to not given")
		}
	})
}

func TestFormatResult(t *testing.T) {
	cases := []struct {
		class metadata.Classification
		want  string
	}{
		{metadata.Match, "MATCH"},
		{metadata.NoMatch, "NO-MATCH"},
		{metadata.Uncertain, "UNCERTAIN"},
	}
	for _, c := range cases {
		r := pipeline.Result{
			ManifestURL: "https://example.org/m.json",
			Class:       c.class,
			Record:      metadata.WorkRecord{Langs: []string{"fr"}, DateRange: metadata.DateRange{Start: 1450, End: 1450}},
		}
		got := formatResult(r)
		if !contains(got, c.want) || !contains(got, r.ManifestURL) {
			t.Fatalf("formatResult(%v) = %q, want it to contain %q and the URL", c.class, got, c.want)
		}
	}

	errLine := formatResult(pipeline.Result{ManifestURL: "https://example.org/bad.json", Err: errBoom})
	if !contains(errLine, "ERROR") || !contains(errLine, "boom") {
		t.Fatalf("error result = %q, want ERROR and cause", errLine)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var errBoom = boomError{}

type boomError struct{}

func (boomError) Error() string { return "boom" }
