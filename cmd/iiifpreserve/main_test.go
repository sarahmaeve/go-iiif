package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
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

func TestBnFOAIHTTPWarningIsExplicit(t *testing.T) {
	const rawURL = "http://oai.bnf.fr/oai2/OAIHandler?verb=Identify"
	var stderr bytes.Buffer
	warnBnFOAIHTTP(&cliWriter{w: &stderr}, rawURL)
	got := stderr.String()
	for _, want := range []string{"WARNING", "HTTP, not HTTPS", "BnF OAI", rawURL} {
		if !strings.Contains(got, want) {
			t.Errorf("warning missing %q; got %q", want, got)
		}
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

	t.Run("local manifest file is parsed and exclusive", func(t *testing.T) {
		o, err := parseArgs([]string{"-manifest-file", "/tmp/manuscript.json"})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if o.manifestFile != "/tmp/manuscript.json" {
			t.Fatalf("manifestFile = %q", o.manifestFile)
		}
		for _, args := range [][]string{
			{"-manifest-file", "local.json", "-manifest", "https://example.org/m"},
			{"-manifest-file", "local.json", "-collection", "https://example.org/c"},
			{"-manifest-file", "local.json", "-doctor"},
		} {
			if _, err := parseArgs(args); err == nil {
				t.Fatalf("parseArgs(%v) succeeded; want mutually-exclusive mode error", args)
			}
		}
	})

	t.Run("RDF acquisition flags are part of the main application", func(t *testing.T) {
		o, err := parseArgs([]string{
			"-rdf-file", "/tmp/work.rdf",
			"-image-file", "/tmp/work.jpg",
		})
		if err != nil {
			t.Fatalf("parseArgs: %v", err)
		}
		if o.rdfFile != "/tmp/work.rdf" || o.imageFile != "/tmp/work.jpg" {
			t.Fatalf("RDF options = %+v", o)
		}
		for _, args := range [][]string{
			{"-rdf-file", "work.rdf", "-manifest", "https://example.org/manifest"},
			{"-rdf-file", "work.rdf", "-collection", "https://example.org/collection"},
			{"-image-file", "work.jpg", "-manifest-file", "manifest.json"},
			{"-image-base", "https://media.example/", "-serve"},
		} {
			if _, err := parseArgs(args); err == nil {
				t.Fatalf("parseArgs(%v) succeeded; want RDF mode validation error", args)
			}
		}
	})

	t.Run("the network -rdf URL flag has been removed", func(t *testing.T) {
		// RDF is now preserved only from a local document (-rdf-file); the
		// bot-wall-prone URL-fetch path was removed. The flag must no longer
		// parse, so a stale script fails loudly instead of silently changing
		// meaning.
		if _, err := parseArgs([]string{"-rdf", "https://example.org/work.rdf"}); err == nil {
			t.Fatal("parseArgs(-rdf ...) succeeded; want unknown-flag error after removal")
		}
	})

	t.Run("taster requires exactly one single-manifest mode", func(t *testing.T) {
		for _, args := range [][]string{
			{"-taster", "-manifest", "https://example.org/m"},
			{"-taster", "-manifest-file", "local.json"},
		} {
			o, err := parseArgs(args)
			if err != nil || !o.taster {
				t.Fatalf("parseArgs(%v) = %+v, %v", args, o, err)
			}
		}
		for _, args := range [][]string{
			{"-taster", "-collection", "https://example.org/c"},
			{"-taster", "-serve"},
			{"-taster", "-manifest", "https://example.org/m", "-dry-run"},
		} {
			if _, err := parseArgs(args); err == nil {
				t.Fatalf("parseArgs(%v) succeeded; want taster mode error", args)
			}
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

func TestRunDoctorCollatesManifestRerunCommands(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "gallica.bnf.fr", "manuscript")
	if err := os.MkdirAll(bundle, 0o750); err != nil {
		t.Fatal(err)
	}
	manifestURL := "https://gallica.bnf.fr/iiif/ark:/12148/test/manifest.json"
	files := map[string]string{
		"manifest.json": `{"type":"Manifest","seeAlso":[{"id":"http://oai.bnf.fr/oai2/OAIHandler?verb=GetRecord&identifier=one"},{"id":"http://oai.bnf.fr/oai2/OAIHandler?verb=GetRecord&identifier=two"}]}`,
		"provenance.json": `{"manifest_url":"` + manifestURL + `","images":[],"linked_failures":[` +
			`{"url":"http://oai.bnf.fr/oai2/OAIHandler?verb=GetRecord&identifier=one","kind":"seeAlso","error":"old failure"},` +
			`{"url":"http://oai.bnf.fr/oai2/OAIHandler?verb=GetRecord&identifier=two","kind":"seeAlso","error":"old failure"}]}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(bundle, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-doctor", "-store", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("doctor exit = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "recommended commands") {
		t.Fatalf("doctor omitted recommendation heading: %q", got)
	}
	wantCommand := "iiifpreserve -manifest " + shellQuoteArg(manifestURL) + " -store " + shellQuoteArg(root)
	if strings.Count(got, wantCommand) != 1 {
		t.Fatalf("doctor command count = %d, want one collated command; output=%q", strings.Count(got, wantCommand), got)
	}
}

func TestShellQuoteArg(t *testing.T) {
	if got, want := shellQuoteArg("/tmp/Sarah's library"), "'/tmp/Sarah'\"'\"'s library'"; got != want {
		t.Fatalf("shellQuoteArg = %q, want %q", got, want)
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

func TestLoadManifestFilePreservesBytesAndUsesManifestID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loc-manifest.json")
	want := []byte("{\n  \"@context\": \"http://iiif.io/api/presentation/2/context.json\",\n  \"@id\": \"https://www.loc.gov/item/0027938281A-ms/manifest.json\",\n  \"@type\": \"sc:Manifest\",\n  \"sequences\": []\n}\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	gotID, got, err := loadManifestFile(path)
	if err != nil {
		t.Fatalf("loadManifestFile: %v", err)
	}
	if gotID != "https://www.loc.gov/item/0027938281A-ms/manifest.json" {
		t.Fatalf("manifest ID = %q", gotID)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("loaded bytes changed:\n got %q\nwant %q", got, want)
	}
}

func TestLoadManifestFileRequiresJSONManifestID(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{"bad JSON", "not json"},
		{"missing id", `{"type":"Manifest"}`},
		{"non URL id", `{"id":"manuscript","type":"Manifest"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadManifestFile(path); err == nil {
				t.Fatal("loadManifestFile succeeded; want validation error")
			}
		})
	}
}

func TestRunLocalManifestFileDryRun(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	manifest := []byte(`{
		"id":"https://example.org/iiif/local/manifest.json",
		"type":"Manifest",
		"items":[{"items":[{"items":[{"body":{"id":"https://example.org/image.jpg","type":"Image"}}]}]}]
	}`)
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := run([]string{
		"-manifest-file", manifestPath,
		"-dry-run",
		"-store", t.TempDir(),
	}, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %q", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "https://example.org/iiif/local/manifest.json") ||
		!strings.Contains(stdout.String(), "1 image(s)") {
		t.Fatalf("stdout = %q, want manifest identity and image count", stdout.String())
	}
}

func TestRunLocalRDFAndImageBuildsOrdinaryOfflineBundle(t *testing.T) {
	tmp := t.TempDir()
	rdfPath := filepath.Join(tmp, "work.rdf")
	imagePath := filepath.Join(tmp, "work.jpg")
	store := filepath.Join(tmp, "library")
	rdf := []byte(`<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
 xmlns:sioc="http://rdfs.org/sioc/ns#" xmlns:graph="https://example.org/graph/"
 xmlns:cidoc="http://www.cidoc-crm.org/cidoc-crm#" xmlns:catalog="https://example.org/catalog/">
 <sioc:Item rdf:about="https://museum.example/works/painting"><graph:has_docSem rdf:resource="https://data.example/object/painting"/></sioc:Item>
 <cidoc:E22_Man-Made_Object rdf:about="https://data.example/object/painting">
  <catalog:title xml:lang="en">Offline RDF Painting</catalog:title>
  <catalog:main_image>images/original.jpg</catalog:main_image>
  <cidoc:p65_shows_visual_item rdf:resource="https://data.example/image/painting"/>
 </cidoc:E22_Man-Made_Object>
 <cidoc:E36_Visual_Item rdf:about="https://data.example/image/painting">
  <catalog:filePath>images/original.jpg</catalog:filePath><catalog:imageWidth>1944</catalog:imageWidth><catalog:imageHeight>2952</catalog:imageHeight>
 </cidoc:E36_Visual_Item>
</rdf:RDF>`)
	if err := os.WriteFile(rdfPath, rdf, 0o600); err != nil {
		t.Fatal(err)
	}
	imageData := image.NewRGBA(image.Rect(0, 0, 64, 96))
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, imageData, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := run([]string{
		"-rdf-file", rdfPath,
		"-image-file", imagePath,
		"-store", store,
	}, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exit, stdout.String(), stderr.String())
	}
	bundle := filepath.Join(store, "museum.example", "works_painting")
	for _, name := range []string{"manifest.json", "provenance.json", "0001.jpg", filepath.Join("0001", "info.json")} {
		if _, err := os.Stat(filepath.Join(bundle, name)); err != nil {
			t.Errorf("bundle lacks %s: %v", name, err)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(bundle, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "Offline RDF Painting") || !strings.Contains(string(manifest), `"width": 64`) {
		t.Fatalf("derived manifest does not use RDF metadata and actual image dimensions: %s", manifest)
	}
	provenance, err := os.ReadFile(filepath.Join(bundle, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var prov struct {
		ManifestDerivation struct {
			Method       string `json:"method"`
			SourceURL    string `json:"source_url"`
			SourceSHA256 string `json:"source_sha256"`
		} `json:"manifest_derivation"`
		LinkedResources []struct {
			URL  string `json:"url"`
			File string `json:"file"`
		} `json:"linked_resources"`
	}
	if err := json.Unmarshal(provenance, &prov); err != nil {
		t.Fatal(err)
	}
	if prov.ManifestDerivation.Method != "rdf-to-iiif-v1" || prov.ManifestDerivation.SourceSHA256 == "" || len(prov.LinkedResources) != 1 {
		t.Fatalf("RDF provenance = %+v", prov)
	}
	preservedRDF, err := os.ReadFile(filepath.Join(bundle, filepath.FromSlash(prov.LinkedResources[0].File)))
	if err != nil || !bytes.Equal(preservedRDF, rdf) {
		t.Fatalf("preserved RDF differs from source: %v", err)
	}
}

func TestRunLocalRDFDetectsTurtleSerialization(t *testing.T) {
	turtlePath := filepath.Join(t.TempDir(), "work.ttl")
	turtle := []byte(`@prefix schema: <https://schema.org/> .
<https://museum.example/works/turtle> a schema:VisualArtwork ;
 schema:name "Turtle Painting"@en ;
 schema:image <https://media.example/turtle.jpg> .
<https://media.example/turtle.jpg> schema:width 640 ; schema:height 960 .`)
	if err := os.WriteFile(turtlePath, turtle, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := run([]string{
		"-rdf-file", turtlePath,
		"-dry-run",
		"-store", t.TempDir(),
	}, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "https://museum.example/works/turtle") || !strings.Contains(stdout.String(), "640x960") {
		t.Fatalf("stdout = %q, want Turtle record and image dimensions", stdout.String())
	}
}

type tasterFetcher struct {
	wantURL string
	image   []byte
	calls   []string
}

func (f *tasterFetcher) Fetch(_ context.Context, rawURL string) ([]byte, error) {
	f.calls = append(f.calls, rawURL)
	if rawURL != f.wantURL {
		return nil, errBoom
	}
	return f.image, nil
}

func TestRunTasterFetchesOnlyFirstImageAndStoresNothing(t *testing.T) {
	const (
		manifestURL  = "https://view.example/manifest.json"
		firstService = "https://images.example/iiif/first"
	)
	manifest := []byte(`{"sequences":[{"canvases":[
		{"images":[{"resource":{"service":{"@id":"` + firstService + `"}}}]},
		{"images":[{"resource":{"service":{"@id":"https://images.example/iiif/second"}}}]}
	]}]}`)
	var encoded bytes.Buffer
	imageData := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := range 24 {
		for x := range 32 {
			imageData.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	if err := jpeg.Encode(&encoded, imageData, nil); err != nil {
		t.Fatal(err)
	}
	fetcher := &tasterFetcher{
		wantURL: firstService + "/full/max/0/default.jpg",
		image:   encoded.Bytes(),
	}
	var stdout, stderr strings.Builder
	exit := runTaster(context.Background(), fetcher, manifestURL, manifest,
		&cliWriter{w: &stdout}, &cliWriter{w: &stderr})
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %q", exit, stderr.String())
	}
	if len(fetcher.calls) != 1 || fetcher.calls[0] != fetcher.wantURL {
		t.Fatalf("fetch calls = %v, want only %q", fetcher.calls, fetcher.wantURL)
	}
	for _, want := range []string{"image 1/2", "32x24 JPEG", "not stored", fetcher.wantURL} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
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
