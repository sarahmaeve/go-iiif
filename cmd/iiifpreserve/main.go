// Command iiifpreserve walks a IIIF Collection, normalizes each manifest's
// metadata, and classifies it against a researcher's filter (DESIGN §3,
// discovery+selection half). It prints the routing decision per manifest;
// the preservation half (download/tile/store) is not yet built.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/pipeline"
	"github.com/sarahmaeve/go-iiif/internal/preserve"
	"github.com/sarahmaeve/go-iiif/internal/serve"
	"github.com/sarahmaeve/go-iiif/internal/source"
)

type options struct {
	collection string
	manifest   string // -manifest: preserve one manifest URL, no crawl/filter
	langs      []string
	from, to   int
	hasDate    bool
	places     []string
	max        int
	workers    int
	journal    string
	store      string // -store: persistent library root (resolved in run())
	preserve   string // deprecated alias for -store (back-compat)
	dryRun     bool   // classify only, do not download images
	serve      string // addr; non-empty = serve the store, don't crawl
	tlsCert    string
	tlsKey     string
	noTLS      bool
}

// filter builds the researcher's selection predicate from the parsed flags.
func (o *options) filter() metadata.Filter {
	f := metadata.Filter{Languages: o.langs, Places: o.places}
	if o.hasDate {
		f.Date = &metadata.DateRange{Start: o.from, End: o.to}
	}
	return f
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func parseArgs(args []string) (*options, error) {
	fs := flag.NewFlagSet("iiifpreserve", flag.ContinueOnError)
	var (
		collection = fs.String("collection", "", "IIIF Collection root URL to crawl")
		manifest   = fs.String("manifest", "", "preserve a single manifest URL (no crawl; skips the filter)")
		lang       = fs.String("lang", "", "comma-separated ISO 639-1 language codes")
		from       = fs.Int("from", 0, "earliest year (inclusive)")
		to         = fs.Int("to", 0, "latest year (inclusive)")
		place      = fs.String("place", "", "comma-separated place substrings")
		max        = fs.Int("max", 0, "stop after N manifests (0 = unlimited)")
		workers    = fs.Int("workers", 1, "concurrent manifest workers (1 = sequential; per-host politeness still enforced)")
		journal    = fs.String("journal", "", "path to a resumable crawl journal (optional)")
		store      = fs.String("store", "", "persistent image-library root (default: config `store=` or ~/iiif-images)")
		preserve   = fs.String("preserve", "", "deprecated alias for -store")
		dryRun     = fs.Bool("dry-run", false, "classify only; do not download images")
		serve      = fs.String("serve", "", "serve the store over HTTPS at this addr (e.g. 127.0.0.1:8443) instead of crawling")
		tlsCert    = fs.String("tls-cert", defaultTLSCert, "TLS certificate PEM (default: mkcert convention path)")
		tlsKey     = fs.String("tls-key", defaultTLSKey, "TLS private key PEM (default: mkcert convention path)")
		noTLS      = fs.Bool("no-tls", false, "serve plain HTTP instead of HTTPS (debugging only)")
	)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *collection != "" && *manifest != "" {
		return nil, errors.New("-collection and -manifest are mutually exclusive")
	}
	if *serve == "" && *collection == "" && *manifest == "" {
		return nil, errors.New("one of -collection, -manifest, or -serve is required")
	}
	o := &options{
		collection: *collection,
		manifest:   *manifest,
		langs:      splitCSV(*lang),
		from:       *from,
		to:         *to,
		hasDate:    *from != 0 || *to != 0,
		places:     splitCSV(*place),
		max:        *max,
		workers:    *workers,
		journal:    *journal,
		store:      *store,
		preserve:   *preserve,
		dryRun:     *dryRun,
		serve:      *serve,
		tlsCert:    *tlsCert,
		tlsKey:     *tlsKey,
		noTLS:      *noTLS,
	}
	return o, nil
}

func formatResult(r pipeline.Result) string {
	if r.Err != nil {
		return "ERROR     " + r.ManifestURL + " :: " + r.Err.Error()
	}
	label := "MATCH    "
	if r.Class == metadata.NoMatch {
		label = "NO-MATCH "
	}
	return fmt.Sprintf("%s %s :: langs=%v date=%d-%d origin=%q",
		label, r.ManifestURL, r.Record.Langs,
		r.Record.DateRange.Start, r.Record.DateRange.End, r.Record.Origin)
}

// defaultMapping covers the labels seen across the pilot institutions
// (Gallica, Digital Bodleian). A per-institution mapping flag can come later.
func defaultMapping() metadata.FieldMapping {
	return metadata.FieldMapping{
		"language":        metadata.FieldLanguage,
		"langue":          metadata.FieldLanguage,
		"date":            metadata.FieldDate,
		"date statement":  metadata.FieldDate,
		"place of origin": metadata.FieldOrigin,
		"origin":          metadata.FieldOrigin,
	}
}

// cliWriter records the first write error so an output failure — most
// importantly a broken pipe on stdout (`iiifpreserve … | head`) — surfaces in
// the exit code and stops further work, rather than being silently dropped.
type cliWriter struct {
	w   io.Writer
	err error
}

func (c *cliWriter) line(a ...any) {
	if c.err == nil {
		_, c.err = fmt.Fprintln(c.w, a...)
	}
}

func (c *cliWriter) printf(format string, a ...any) {
	if c.err == nil {
		_, c.err = fmt.Fprintf(c.w, format, a...)
	}
}

func run(args []string, stdoutW, stderrW io.Writer) int {
	out := &cliWriter{w: stdoutW}
	errOut := &cliWriter{w: stderrW}

	o, err := parseArgs(args)
	if err != nil {
		errOut.line("iiifpreserve:", err)
		return 2
	}

	// Resolve the persistent library root: -store flag > config `store=` >
	// ~/iiif-images. -preserve is a deprecated alias for -store.
	home, err := os.UserHomeDir()
	if err != nil {
		errOut.line("iiifpreserve: cannot determine home dir:", err)
		return 1
	}
	cfg, err := loadConfig(home)
	if err != nil {
		errOut.line("iiifpreserve:", err)
		return 1
	}
	storeFlag := o.store
	if storeFlag == "" {
		storeFlag = o.preserve // deprecated alias
	}
	o.store = resolveStore(storeFlag, cfg, home)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if o.serve != "" {
		return runServe(ctx, o, out, errOut)
	}

	if o.manifest != "" {
		return runManifest(ctx, o, out, errOut)
	}

	// One polite fetcher shared across discovery, manifest, and image
	// fetches so a single per-host rate limiter governs all traffic.
	fetcher := source.NewPoliteFetcher(source.NewHTTPFetcher())

	var src source.Source = source.NewCollectionSource(fetcher, o.collection)
	if o.journal != "" {
		j, err := source.OpenFileJournal(o.journal)
		if err != nil {
			errOut.line("iiifpreserve:", err)
			return 1
		}
		defer func() {
			if cerr := j.Close(); cerr != nil {
				errOut.line("iiifpreserve: closing journal:", cerr)
			}
		}()
		src = source.NewResumableSource(src, j)
	}

	p := pipeline.New(pipeline.Config{
		Source:  src,
		Fetcher: fetcher,
		Mapping: defaultMapping(),
		Filter:  o.filter(),
		Workers: o.workers,
	})

	var store preserve.BlobStore
	if !o.dryRun {
		store = preserve.NewLocalBlobStore(o.store)
	}

	var n, matched, preserved int
	for r := range p.Run(ctx) {
		out.line(formatResult(r))
		if out.err != nil {
			// stdout sink is gone (e.g. broken pipe): stop crawling.
			break
		}
		n++
		if r.Err == nil && r.Class == metadata.Match {
			matched++
			if store != nil {
				sum, err := preserve.Preserve(ctx, fetcher, store, r.ManifestURL, r.Manifest)
				if err != nil {
					errOut.line("iiifpreserve: preserve", r.ManifestURL, "::", err)
				} else {
					preserved += sum.Stored
					out.printf("  preserved %d image(s) to %s (skipped %d, %d failed)\n",
						sum.Stored, sum.Dir, sum.Skipped, len(sum.Failures))
				}
			}
		}
		if o.max > 0 && n >= o.max {
			break
		}
	}
	errOut.printf("iiifpreserve: %d manifests, %d match, %d images preserved\n", n, matched, preserved)

	if out.err != nil || errOut.err != nil {
		return 1
	}
	return 0
}

// serveBanner is the startup message: where the bundle is served and the
// exact URL to open for the embedded Mirador viewer (no external viewer
// needed — DESIGN §2).
func serveBanner(scheme, addr, dir string) string {
	base := scheme + "://" + addr
	return fmt.Sprintf(
		"iiifpreserve: serving %s at %s (Ctrl-C to stop)\n"+
			"iiifpreserve: open %s/ in a browser for the embedded Mirador viewer\n",
		dir, base, base)
}

// runManifest preserves a single explicitly-named manifest. No crawl, no
// filter — naming a manifest is an intentional choice. Uses the same polite
// HTTPS fetcher as the crawler (no curl). -dry-run fetches and reports
// without storing.
func runManifest(ctx context.Context, o *options, out, errOut *cliWriter) int {
	fetcher := source.NewPoliteFetcher(source.NewHTTPFetcher())

	body, err := fetcher.Fetch(ctx, o.manifest)
	if err != nil {
		errOut.line("iiifpreserve: fetching manifest:", err)
		return 1
	}

	if o.dryRun {
		images, err := preserve.EnumerateImages(body)
		if err != nil {
			errOut.line("iiifpreserve: enumerating images:", err)
			return 1
		}
		out.printf("iiifpreserve: %s — %d image(s) (dry-run, not stored)\n", o.manifest, len(images))
		if out.err != nil {
			return 1
		}
		return 0
	}

	// Per-image progress to stderr — a whole Gallica manuscript under the
	// 13s/host throttle takes a long time; a blind run is unusable.
	progress := preserve.WithProgress(func(e preserve.ProgressEvent) {
		errOut.printf("iiifpreserve: [%d/%d] %s %s\n", e.Index, e.Total, e.File, e.Action)
	})
	sum, err := preserve.Preserve(ctx, fetcher, preserve.NewLocalBlobStore(o.store), o.manifest, body, progress)
	if err != nil {
		errOut.line("iiifpreserve: preserve:", err)
		return 1
	}
	out.printf("iiifpreserve: preserved %d image(s) to %s/%s (skipped %d, %d failed)\n",
		sum.Stored, o.store, sum.Dir, sum.Skipped, len(sum.Failures))
	if out.err != nil || errOut.err != nil {
		return 1
	}
	return 0
}

// tlsSetupHint is the remediation shown when the TLS cert/key are missing:
// the exact one-time mkcert recipe to produce a browser-trusted cert at
// the expected path, or the -no-tls escape. mkcert's -install adds its CA
// to the OS/browser trust store so the served viewer has no warnings.
func tlsSetupHint(cert, key string) string {
	return fmt.Sprintf(
		"iiifpreserve: TLS cert/key not found:\n"+
			"  cert: %s\n  key:  %s\n"+
			"set up a locally-trusted cert once (no browser warnings):\n"+
			"  mkcert -install\n"+
			"  mkdir -p %s\n"+
			"  mkcert -cert-file %s -key-file %s 127.0.0.1 localhost\n"+
			"or pass -no-tls to serve plain HTTP (debugging only)",
		cert, key, filepath.Dir(cert), cert, key)
}

// runServe serves the preserved bundle dir over HTTPS until interrupted.
func runServe(ctx context.Context, o *options, out, errOut *cliWriter) int {
	certFile, keyFile := o.tlsCert, o.tlsKey
	if o.noTLS {
		certFile, keyFile = "", ""
		errOut.line("iiifpreserve: WARNING -no-tls serves plain HTTP (debugging only)")
	} else {
		home, _ := os.UserHomeDir()
		certFile = expandHome(certFile, home)
		keyFile = expandHome(keyFile, home)
		for _, f := range []string{certFile, keyFile} {
			// G703: f is an operator-supplied -tls-cert/-tls-key path
			// (or its default), not attacker-controlled input.
			if _, err := os.Stat(f); err != nil { //nolint:gosec // G703: operator-supplied TLS path
				errOut.line(tlsSetupHint(certFile, keyFile))
				return 2
			}
		}
	}

	scheme := "https"
	if o.noTLS {
		scheme = "http"
	}
	out.printf("%s", serveBanner(scheme, o.serve, o.store))

	if err := serve.New(o.store).ListenAndServe(ctx, o.serve, certFile, keyFile); err != nil {
		errOut.line("iiifpreserve: serve:", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
