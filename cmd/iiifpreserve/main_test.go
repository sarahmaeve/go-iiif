package main

import (
	"bytes"
	"context"
	"io"
	"os"
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

	t.Run("fresh requires a real collection run", func(t *testing.T) {
		if _, err := parseArgs([]string{"-manifest", "https://example.org/m", "-fresh"}); err == nil {
			t.Fatal("-fresh with -manifest should fail")
		}
		if _, err := parseArgs([]string{"-collection", "https://example.org/c", "-fresh", "-dry-run"}); err == nil {
			t.Fatal("-fresh with -dry-run should fail")
		}
		if _, err := parseArgs([]string{"-collection", "https://example.org/c", "-fresh", "-serve"}); err == nil {
			t.Fatal("-fresh collection scan with -serve should fail")
		}
		o, err := parseArgs([]string{"-collection", "https://example.org/c", "-fresh"})
		if err != nil || !o.fresh {
			t.Fatalf("real fresh collection parse = %+v, %v", o, err)
		}
	})

	t.Run("ingest status and page retry policy", func(t *testing.T) {
		o, err := parseArgs([]string{"-ingest-status", "-store", "/data/iiif"})
		if err != nil || !o.ingestStatus {
			t.Fatalf("status parse = %+v, %v", o, err)
		}
		if _, err := parseArgs([]string{"-ingest-status", "-collection", "https://example.org/c"}); err == nil {
			t.Fatal("status and collection should be mutually exclusive")
		}
		o, err = parseArgs([]string{"-manifest", "https://example.org/m", "-page-retries", "3"})
		if err != nil || o.pageRetries != 3 {
			t.Fatalf("page retry parse = %+v, %v", o, err)
		}
		if _, err := parseArgs([]string{"-manifest", "https://example.org/m", "-page-retries", "-1"}); err == nil {
			t.Fatal("negative page retries should fail")
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

	t.Run("browser-unsafe ports are refused", func(t *testing.T) {
		// Representative entries from the WHATWG Fetch "bad ports" list
		// that fall in our allowed 1024..65535 range; browsers reject
		// these with ERR_UNSAFE_PORT, so serving on them is a dead end.
		for _, a := range []string{"-serve=6666", "-serve=6000", "-serve=2049", "-serve=10080", "-serve=4190"} {
			if _, err := parseArgs([]string{a}); err == nil {
				t.Fatalf("%s: expected a browser-unsafe-port error", a)
			}
		}
		// A normal port near them must still be accepted.
		if _, err := parseArgs([]string{"-serve=8443"}); err != nil {
			t.Fatalf("-serve=8443 (safe) should be allowed: %v", err)
		}
		if _, err := parseArgs([]string{"-serve=6670"}); err != nil {
			t.Fatalf("-serve=6670 (safe) should be allowed: %v", err)
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

	t.Run("doctor is a standalone library mode", func(t *testing.T) {
		o, err := parseArgs([]string{"-doctor", "-store", "/data/iiif"})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if !o.doctor || o.store != "/data/iiif" {
			t.Fatalf("doctor options = %+v", o)
		}
		if _, err := parseArgs([]string{"-doctor", "-serve"}); err == nil {
			t.Fatal("-doctor and -serve should be mutually exclusive")
		}
		if _, err := parseArgs([]string{"-doctor", "-manifest", "https://example.org/m"}); err == nil {
			t.Fatal("-doctor and -manifest should be mutually exclusive")
		}
	})

	t.Run("metadata exchange modes are standalone", func(t *testing.T) {
		export, err := parseArgs([]string{"-export-metadata", "/tmp/research.json", "-store", "/data/iiif"})
		if err != nil || export.exportMetadata != "/tmp/research.json" {
			t.Fatalf("export parse = %+v, %v", export, err)
		}
		incoming, err := parseArgs([]string{"-import-metadata", "/tmp/research.json"})
		if err != nil || incoming.importMetadata != "/tmp/research.json" {
			t.Fatalf("import parse = %+v, %v", incoming, err)
		}
		if _, err := parseArgs([]string{"-export-metadata", "a", "-import-metadata", "b"}); err == nil {
			t.Fatal("simultaneous export/import should be rejected")
		}
		if _, err := parseArgs([]string{"-import-metadata", "a", "-serve"}); err == nil {
			t.Fatal("import and serve should be rejected")
		}
		if preview, err := parseArgs([]string{"-import-metadata", "a", "-dry-run"}); err != nil || !preview.dryRun {
			t.Fatalf("import preview parse = %+v, %v", preview, err)
		}
		if _, err := parseArgs([]string{"-export-metadata", "a", "-dry-run"}); err == nil {
			t.Fatal("export -dry-run should be rejected")
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

func TestRunDoctor(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "example.org", "manuscript")
	if err := os.MkdirAll(bundle, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"manifest.json":   `{"type":"Manifest"}`,
		"provenance.json": `{"images":[{"file":"0001.jpg"}]}`,
		"0001.jpg":        "jpeg",
	} {
		if err := os.WriteFile(filepath.Join(bundle, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-doctor", "-store", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("healthy doctor exit = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !contains(stdout.String(), "library is healthy") || !contains(stdout.String(), "1 bundle(s)") {
		t.Fatalf("doctor output = %q", stdout.String())
	}

	if err := os.Remove(filepath.Join(bundle, "0001.jpg")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-doctor", "-store", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("broken doctor exit = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !contains(stderr.String(), "ERROR") || !contains(stderr.String(), "0001.jpg") {
		t.Fatalf("doctor error output = %q", stderr.String())
	}
}

func TestRunMetadataExportImport(t *testing.T) {
	writeBundle := func(root, rel string) string {
		t.Helper()
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{
			"manifest.json":   `{"type":"Manifest","label":{"en":["Exchange"]}}`,
			"provenance.json": `{"manifest_url":"https://example.org/shared","images":[]}`,
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	source := t.TempDir()
	sourceRel := "source/shared"
	sourceDir := writeBundle(source, sourceRel)
	stateDir := filepath.Join(source, ".iiifpreserve")
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	catalog := `{"version":1,"entries":{"` + sourceRel + `":{"custom_title":"Shared title","notes":"Shared note","tags":"exchange"}}}`
	if err := os.WriteFile(filepath.Join(stateDir, "catalog.json"), []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	annotations := `{"type":"AnnotationPage","items":[{"id":"urn:shared:1","type":"Annotation","target":"https://example.org/canvas/1"}]}`
	if err := os.WriteFile(filepath.Join(sourceDir, "annotations.json"), []byte(annotations), 0o600); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "research.json")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-export-metadata", archive, "-store", source}, &stdout, &stderr); code != 0 {
		t.Fatalf("export exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	target := t.TempDir()
	targetDir := writeBundle(target, "target/shared")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-import-metadata", archive, "-store", target, "-dry-run"}, &stdout, &stderr); code != 0 {
		t.Fatalf("preview exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !contains(stdout.String(), "no files changed") {
		t.Fatalf("preview output = %q", stdout.String())
	}
	for _, path := range []string{filepath.Join(target, ".iiifpreserve", "catalog.json"), filepath.Join(targetDir, "annotations.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("preview unexpectedly created %s: %v", path, err)
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-import-metadata", archive, "-store", target}, &stdout, &stderr); code != 0 {
		t.Fatalf("import exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, path := range []string{filepath.Join(target, ".iiifpreserve", "catalog.json"), filepath.Join(targetDir, "annotations.json")} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("import did not create %s: %v", path, err)
		}
		if !strings.Contains(string(b), "Shared") && !strings.Contains(string(b), "urn:shared:1") {
			t.Fatalf("imported file lacks research metadata: %s", b)
		}
	}
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

func (s *recordingStore) Put(context.Context, string, []byte) error   { s.puts++; return nil }
func (s *recordingStore) Get(context.Context, string) ([]byte, error) { return nil, os.ErrNotExist }
func (s *recordingStore) Exists(context.Context, string) (bool, error) {
	return false, nil
}
func (s *recordingStore) Delete(context.Context, string) error { return nil }

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

	n, matched, images, failures := crawl(context.Background(), results, nil, nil, fetcher, store, true /*dryRun*/, 0, 0, out, errOut)

	if n != 2 || matched != 1 || images != 2 || failures != 0 {
		t.Fatalf("n,matched,images,failures = %d,%d,%d,%d; want 2,1,2,0", n, matched, images, failures)
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

func TestCrawl_CountsFailedManifestForExitStatus(t *testing.T) {
	results := func(yield func(pipeline.Result) bool) {
		yield(pipeline.Result{ManifestURL: "https://ex.org/broken", Err: errBoom})
	}
	var stdout, stderr strings.Builder
	n, matched, images, failures := crawl(context.Background(), results, nil, nil, &recordingFetcher{}, nil, false, 0, 0,
		&cliWriter{w: &stdout}, &cliWriter{w: &stderr})
	if n != 1 || matched != 0 || images != 0 || failures != 1 {
		t.Fatalf("crawl = %d,%d,%d,%d; want 1,0,0,1", n, matched, images, failures)
	}
	if !contains(stdout.String(), "ERROR") || !contains(stdout.String(), "boom") {
		t.Fatalf("stdout = %q, want visible pipeline error", stdout.String())
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
