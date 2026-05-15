package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/pipeline"
)

func TestParseConfig(t *testing.T) {
	in := strings.NewReader(`
# the persistent image library
store = /data/iiif

  # blank lines and comments ignored above and below

empty=
`)
	cfg, err := parseConfig(in)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got := cfg["store"]; got != "/data/iiif" {
		t.Errorf("store = %q, want /data/iiif (value trimmed)", got)
	}
	if _, ok := cfg["# the persistent image library"]; ok {
		t.Error("comment line was parsed as a key")
	}
	if got, ok := cfg["empty"]; !ok || got != "" {
		t.Errorf("empty key = %q,%v; want \"\",true", got, ok)
	}
}

func TestResolveStore(t *testing.T) {
	home := t.TempDir()
	def := filepath.Join(home, "iiif-images")

	t.Run("flag wins over config and default", func(t *testing.T) {
		got := resolveStore("/flag/path", map[string]string{"store": "/cfg/path"}, home)
		if got != "/flag/path" {
			t.Errorf("got %q, want /flag/path", got)
		}
	})
	t.Run("config used when no flag", func(t *testing.T) {
		got := resolveStore("", map[string]string{"store": "/cfg/path"}, home)
		if got != "/cfg/path" {
			t.Errorf("got %q, want /cfg/path", got)
		}
	})
	t.Run("default when neither", func(t *testing.T) {
		got := resolveStore("", nil, home)
		if got != def {
			t.Errorf("got %q, want %q", got, def)
		}
	})
	t.Run("tilde in config expands to home", func(t *testing.T) {
		got := resolveStore("", map[string]string{"store": "~/lib"}, home)
		if got != filepath.Join(home, "lib") {
			t.Errorf("got %q, want %q", got, filepath.Join(home, "lib"))
		}
	})
}

func TestServeBanner(t *testing.T) {
	b := serveBanner("https", "127.0.0.1:8443", "/tmp/preserved")
	// Researcher must be told the exact URL to open for the embedded viewer.
	if !contains(b, "https://127.0.0.1:8443/") {
		t.Errorf("banner missing viewer URL; got:\n%s", b)
	}
	if !contains(b, "Mirador") {
		t.Errorf("banner does not mention the embedded Mirador viewer; got:\n%s", b)
	}
	if !contains(b, "/tmp/preserved") {
		t.Errorf("banner missing served dir; got:\n%s", b)
	}
}

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

	t.Run("store and dry-run flags", func(t *testing.T) {
		o, err := parseArgs([]string{
			"-collection", "https://example.org/c/top",
			"-store", "/data/iiif", "-dry-run",
		})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if o.store != "/data/iiif" {
			t.Fatalf("store = %q, want /data/iiif", o.store)
		}
		if !o.dryRun {
			t.Fatal("dryRun = false, want true")
		}
	})

	t.Run("serve no longer requires preserve (store has a default)", func(t *testing.T) {
		o, err := parseArgs([]string{"-serve", "127.0.0.1:8443"})
		if err != nil {
			t.Fatalf("serve without -preserve should be allowed now: %v", err)
		}
		if o.serve != "127.0.0.1:8443" {
			t.Fatalf("serve = %q", o.serve)
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
