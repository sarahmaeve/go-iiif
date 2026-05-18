package main

import (
	"context"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/pipeline"
)

func TestTLSSetupHint(t *testing.T) {
	cert := "/Users/x/.config/iiifpreserve/certs/127.0.0.1+1.pem"
	key := "/Users/x/.config/iiifpreserve/certs/127.0.0.1+1-key.pem"
	h := tlsSetupHint(cert, key)

	for _, want := range []string{
		cert, key,
		"mkcert -install",
		"mkcert -cert-file " + cert + " -key-file " + key + " 127.0.0.1 localhost",
		"-no-tls",
	} {
		if !contains(h, want) {
			t.Errorf("tlsSetupHint missing %q; got:\n%s", want, h)
		}
	}
}

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
			"-serve=8443",
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
		if o.servePort != 8443 || o.tlsCert != "/c.pem" || o.tlsKey != "/k.pem" {
			t.Fatalf("serve flags = %d %q %q", o.servePort, o.tlsCert, o.tlsKey)
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

	t.Run("bare -serve uses the default port on localhost", func(t *testing.T) {
		o, err := parseArgs([]string{"-serve"})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if o.servePort != defaultServePort {
			t.Fatalf("servePort = %d, want default %d", o.servePort, defaultServePort)
		}
		want := "127.0.0.1:" + strconv.Itoa(defaultServePort)
		if got := serveAddr(o.servePort); got != want {
			t.Fatalf("serveAddr = %q, want %q", got, want)
		}
	})

	t.Run("-serve=PORT picks an explicit port", func(t *testing.T) {
		o, err := parseArgs([]string{"-serve=9000"})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if o.servePort != 9000 {
			t.Fatalf("servePort = %d, want 9000", o.servePort)
		}
	})

	t.Run("privileged / root-only ports are refused", func(t *testing.T) {
		for _, a := range []string{"-serve=80", "-serve=443", "-serve=1023"} {
			if _, err := parseArgs([]string{a}); err == nil {
				t.Fatalf("%s: expected error (port below %d is root-only)", a, minServePort)
			}
		}
		// The first unprivileged port must be allowed.
		o, err := parseArgs([]string{"-serve=1024"})
		if err != nil {
			t.Fatalf("-serve=1024 should be allowed: %v", err)
		}
		if o.servePort != minServePort {
			t.Fatalf("servePort = %d, want %d", o.servePort, minServePort)
		}
	})

	t.Run("out-of-range ports are refused", func(t *testing.T) {
		for _, a := range []string{"-serve=0", "-serve=-1", "-serve=70000", "-serve=65536"} {
			if _, err := parseArgs([]string{a}); err == nil {
				t.Fatalf("%s: expected a range error", a)
			}
		}
	})

	t.Run("space-separated -serve PORT is rejected with guidance", func(t *testing.T) {
		_, err := parseArgs([]string{"-serve", "8443"})
		if err == nil {
			t.Fatal("expected error: -serve PORT (space) must be -serve=PORT")
		}
	})

	t.Run("tls flags default to the mkcert-convention path", func(t *testing.T) {
		o, err := parseArgs([]string{"-serve=8443"})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if o.tlsCert != defaultTLSCert || o.tlsKey != defaultTLSKey {
			t.Fatalf("tls defaults = %q / %q, want %q / %q",
				o.tlsCert, o.tlsKey, defaultTLSCert, defaultTLSKey)
		}
	})

	t.Run("manifest flag is parsed", func(t *testing.T) {
		o, err := parseArgs([]string{"-manifest", "https://iiif.io/m-01.json"})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if o.manifest != "https://iiif.io/m-01.json" {
			t.Fatalf("manifest = %q", o.manifest)
		}
	})

	t.Run("collection and manifest are mutually exclusive", func(t *testing.T) {
		_, err := parseArgs([]string{
			"-collection", "https://example.org/c/top",
			"-manifest", "https://example.org/m.json",
		})
		if err == nil {
			t.Fatal("expected error when both -collection and -manifest are given")
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
		o, err := parseArgs([]string{"-serve=8443"})
		if err != nil {
			t.Fatalf("serve without -preserve should be allowed now: %v", err)
		}
		if o.servePort != 8443 {
			t.Fatalf("servePort = %d, want 8443", o.servePort)
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

func TestDryRunLine(t *testing.T) {
	// Minimal Presentation 2.x manifest: two canvases, one image each.
	const v2 = `{"sequences":[{"canvases":[
		{"label":"f1","images":[{"resource":{"@id":"https://ex.org/img/1/full/full/0/default.jpg"}}]},
		{"label":"f2","images":[{"resource":{"@id":"https://ex.org/img/2/full/full/0/default.jpg"}}]}
	]}]}`
	n, line, err := dryRunLine([]byte(v2))
	if err != nil {
		t.Fatalf("dryRunLine: %v", err)
	}
	if n != 2 {
		t.Fatalf("image count = %d, want 2", n)
	}
	if !contains(line, "2 image(s)") || !contains(line, "dry-run") {
		t.Fatalf("line = %q, want it to mention 2 image(s) and dry-run", line)
	}
}

func TestDryRunLine_BadManifest(t *testing.T) {
	if _, _, err := dryRunLine([]byte("not json")); err == nil {
		t.Fatal("expected an error for an undecodable manifest")
	}
}

func TestRunSummary(t *testing.T) {
	// The load-bearing invariant: a dry run must never tell the researcher
	// images were "preserved", because nothing was written to disk.
	dry := runSummary(true, 10, 3, 42)
	if contains(dry, "preserved") {
		t.Fatalf("dry-run summary must not claim preservation: %q", dry)
	}
	if !contains(dry, "dry-run") || !contains(dry, "42") {
		t.Fatalf("dry-run summary must mark itself and report the total: %q", dry)
	}
	real := runSummary(false, 10, 3, 42)
	if !contains(real, "42 images preserved") {
		t.Fatalf("real summary must report images preserved: %q", real)
	}
}

// recordingFetcher counts Fetch calls so a dry run can be proven not to
// download anything.
type recordingFetcher struct{ calls int }

func (f *recordingFetcher) Fetch(context.Context, string) ([]byte, error) {
	f.calls++
	return nil, nil
}

// recordingStore counts Put calls so a dry run can be proven not to write.
type recordingStore struct{ puts int }

func (s *recordingStore) Put(context.Context, string, []byte) error { s.puts++; return nil }
func (s *recordingStore) Exists(context.Context, string) (bool, error) {
	return false, nil
}

// TestCrawl_DryRunEnumeratesWithoutDownloading is the real wiring test: it
// drives the crawl loop with a Match carrying a 2-image manifest and a
// NoMatch, with a store present, and asserts the dry-run path enumerates the
// per-work count but never touches the fetcher or the store.
func TestCrawl_DryRunEnumeratesWithoutDownloading(t *testing.T) {
	const twoImg = `{"sequences":[{"canvases":[
		{"images":[{"resource":{"@id":"https://ex.org/a/full/full/0/default.jpg"}}]},
		{"images":[{"resource":{"@id":"https://ex.org/b/full/full/0/default.jpg"}}]}
	]}]}`
	results := func(yield func(pipeline.Result) bool) {
		if !yield(pipeline.Result{ManifestURL: "https://ex.org/m1", Class: metadata.Match, Manifest: []byte(twoImg)}) {
			return
		}
		yield(pipeline.Result{ManifestURL: "https://ex.org/m2", Class: metadata.NoMatch})
	}

	fetcher := &recordingFetcher{}
	store := &recordingStore{}
	var sb strings.Builder
	out := &cliWriter{w: &sb}
	errOut := &cliWriter{w: io.Discard}

	n, matched, images := crawl(context.Background(), results, fetcher, store, true /*dryRun*/, 0, out, errOut)

	if n != 2 || matched != 1 || images != 2 {
		t.Fatalf("n,matched,images = %d,%d,%d; want 2,1,2", n, matched, images)
	}
	if fetcher.calls != 0 {
		t.Fatalf("dry run fetched %d time(s); it must not download anything", fetcher.calls)
	}
	if store.puts != 0 {
		t.Fatalf("dry run wrote %d blob(s); it must not store anything", store.puts)
	}
	if !contains(sb.String(), "2 image(s)") || !contains(sb.String(), "dry-run") {
		t.Fatalf("output = %q, want the per-work count and a dry-run marker", sb.String())
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
