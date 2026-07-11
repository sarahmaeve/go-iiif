// Command iiifpreserve discovers or directly fetches IIIF manifests, filters
// collection results, preserves images plus local deep-zoom pyramids, serves
// the offline library, diagnoses it, and exchanges researcher metadata.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"iter"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/institution"
	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/pipeline"
	"github.com/sarahmaeve/go-iiif/internal/preserve"
	"github.com/sarahmaeve/go-iiif/internal/serve"
	"github.com/sarahmaeve/go-iiif/internal/source"
)

type options struct {
	collection     string
	manifest       string // -manifest: preserve one manifest URL, no crawl/filter
	langs          []string
	from, to       int
	hasDate        bool
	places         []string
	max            int
	workers        int
	journal        string
	store          string // -store: persistent library root (resolved in run())
	preserve       string // deprecated alias for -store (back-compat)
	dryRun         bool   // classify only, do not download images
	doctor         bool   // validate the local library and exit
	exportMetadata string // write researcher-authored catalogue/annotations archive
	importMetadata string // non-destructively merge a researcher metadata archive
	servePort      int    // -serve PORT; non-zero = serve the store on localhost, don't crawl
	tlsCert        string
	tlsKey         string
	noTLS          bool
}

const (
	// defaultServePort is the localhost port used when -serve is given
	// without a value.
	defaultServePort = 8443
	// minServePort is the lowest non-privileged TCP port. Ports below
	// 1024 are root-only on Unix; the tool refuses to bind them so a
	// researcher can never be asked to (or accidentally) run privileged.
	minServePort = 1024
	maxServePort = 65535
)

// browserUnsafePorts are ports in the 1024..65535 range that Chrome and
// Firefox refuse to connect to (ERR_UNSAFE_PORT). It is the union of
// Chromium's net::kRestrictedPorts and Firefox's gBadPortList — both
// conform to the WHATWG Fetch "bad ports" list. Entries below 1024 are
// already rejected by the privileged-port check, so only the in-range
// ones are listed. Serving exists to be opened in a browser, so binding
// one of these is a guaranteed dead end; reject it early with a clear
// reason instead of letting the researcher hit an opaque browser error.
var browserUnsafePorts = map[int]struct{}{
	1719: {}, 1720: {}, 1723: {}, 2049: {}, 3659: {}, 4045: {}, 4190: {},
	5060: {}, 5061: {}, 6000: {}, 6566: {}, 6665: {}, 6666: {}, 6667: {},
	6668: {}, 6669: {}, 6679: {}, 6697: {}, 10080: {},
}

// servePortFlag backs -serve as an optional-value flag: a bare `-serve`
// selects defaultServePort, while `-serve=PORT` selects PORT. Implementing
// IsBoolFlag is what lets the bare form parse without an argument. A zero
// value means "not serving" (the default — crawl instead).
type servePortFlag int

func (p *servePortFlag) String() string {
	if p == nil || *p == 0 {
		return ""
	}
	return strconv.Itoa(int(*p))
}

// Set accepts "true" (the flag package's value for a bare bool-like flag,
// i.e. `-serve`) as "use the default port", or an explicit port string.
// Privileged and out-of-range ports are rejected here so the error
// surfaces at parse time with the same message regardless of source.
func (p *servePortFlag) Set(s string) error {
	if s == "true" { // bare -serve
		*p = defaultServePort
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("-serve: invalid port %q", s)
	}
	if n < minServePort || n > maxServePort {
		return fmt.Errorf("-serve: port %d out of range; use %d..%d (ports below %d are root-only)",
			n, minServePort, maxServePort, minServePort)
	}
	if _, unsafe := browserUnsafePorts[n]; unsafe {
		return fmt.Errorf("-serve: port %d is blocked by browsers (ERR_UNSAFE_PORT); pick another", n)
	}
	*p = servePortFlag(n)
	return nil
}

func (p *servePortFlag) IsBoolFlag() bool { return true }

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
		collection     = fs.String("collection", "", "IIIF Collection root URL to crawl")
		manifest       = fs.String("manifest", "", "preserve a single manifest URL (no crawl; skips the filter)")
		lang           = fs.String("lang", "", "comma-separated ISO 639-1 language codes")
		from           = fs.Int("from", 0, "earliest year (inclusive)")
		to             = fs.Int("to", 0, "latest year (inclusive)")
		place          = fs.String("place", "", "comma-separated place substrings")
		max            = fs.Int("max", 0, "stop after N manifests (0 = unlimited)")
		workers        = fs.Int("workers", 1, "concurrent manifest workers (1 = sequential; per-host politeness still enforced)")
		journal        = fs.String("journal", "", "path to a resumable crawl journal (optional)")
		store          = fs.String("store", "", "persistent image-library root (default: config `store=` or ~/iiif-images)")
		preserve       = fs.String("preserve", "", "deprecated alias for -store")
		dryRun         = fs.Bool("dry-run", false, "classify only; do not download images")
		doctor         = fs.Bool("doctor", false, "validate manifests, provenance, images, tiles, annotations, and catalogue")
		exportMetadata = fs.String("export-metadata", "", "export researcher-authored catalogue fields and annotations to FILE")
		importMetadata = fs.String("import-metadata", "", "non-destructively import researcher metadata from FILE")
		serve          servePortFlag
		tlsCert        = fs.String("tls-cert", defaultTLSCert, "TLS certificate PEM (default: mkcert convention path)")
		tlsKey         = fs.String("tls-key", defaultTLSKey, "TLS private key PEM (default: mkcert convention path)")
		noTLS          = fs.Bool("no-tls", false, "serve plain HTTP instead of HTTPS (debugging only)")
	)
	fs.Var(&serve, "serve", fmt.Sprintf(
		"serve the store over HTTPS on localhost instead of crawling; "+
			"bare -serve uses :%d, or -serve=PORT (%d..%d, excluding browser-blocked ports)",
		defaultServePort, minServePort, maxServePort))
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	// IsBoolFlag means a stray `-serve PORT` leaves PORT as a non-flag
	// argument (and halts flag parsing). The tool takes no positional
	// args, so any leftover is a mistake worth a pointed message.
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q; pass options as flags (use -serve=PORT, not -serve PORT)", fs.Arg(0))
	}
	if *collection != "" && *manifest != "" {
		return nil, errors.New("-collection and -manifest are mutually exclusive")
	}
	if *doctor && (serve != 0 || *collection != "" || *manifest != "") {
		return nil, errors.New("-doctor is mutually exclusive with -collection, -manifest, and -serve")
	}
	if (*exportMetadata != "" || *importMetadata != "") &&
		(serve != 0 || *collection != "" || *manifest != "" || *doctor || (*exportMetadata != "" && *importMetadata != "")) {
		return nil, errors.New("-export-metadata and -import-metadata are standalone, mutually exclusive modes")
	}
	if *exportMetadata != "" && *dryRun {
		return nil, errors.New("-dry-run is supported with -import-metadata, not -export-metadata")
	}
	if serve == 0 && *collection == "" && *manifest == "" && !*doctor && *exportMetadata == "" && *importMetadata == "" {
		return nil, errors.New("one of -collection, -manifest, -serve, -doctor, -export-metadata, or -import-metadata is required")
	}
	o := &options{
		collection:     *collection,
		manifest:       *manifest,
		langs:          splitCSV(*lang),
		from:           *from,
		to:             *to,
		hasDate:        *from != 0 || *to != 0,
		places:         splitCSV(*place),
		max:            *max,
		workers:        *workers,
		journal:        *journal,
		store:          *store,
		preserve:       *preserve,
		dryRun:         *dryRun,
		doctor:         *doctor,
		exportMetadata: *exportMetadata,
		importMetadata: *importMetadata,
		servePort:      int(serve),
		tlsCert:        *tlsCert,
		tlsKey:         *tlsKey,
		noTLS:          *noTLS,
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

	if o.doctor {
		return runDoctor(o, out, errOut)
	}
	if o.exportMetadata != "" {
		return runMetadataExport(o, out, errOut)
	}
	if o.importMetadata != "" {
		return runMetadataImport(o, out, errOut)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if o.servePort != 0 {
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
		Source:       src,
		Fetcher:      fetcher,
		Institutions: institution.Builtin(),
		Filter:       o.filter(),
		Workers:      o.workers,
	})

	var store preserve.BlobStore
	if !o.dryRun {
		store = preserve.NewLocalBlobStore(o.store)
	}

	n, matched, images := crawl(ctx, p.Run(ctx), fetcher, store, o.dryRun, o.max, out, errOut)
	errOut.line(runSummary(o.dryRun, n, matched, images))

	if out.err != nil || errOut.err != nil {
		return 1
	}
	return 0
}

func runMetadataExport(o *options, out, errOut *cliWriter) int {
	report, err := serve.ExportResearchMetadataFile(o.store, o.exportMetadata)
	if err != nil {
		errOut.line("iiifpreserve:", err)
		return 1
	}
	out.printf("iiifpreserve: exported %d bundle(s), %d annotation(s) to %s\n",
		report.Bundles, report.Annotations, o.exportMetadata)
	if out.err != nil {
		return 1
	}
	return 0
}

func runMetadataImport(o *options, out, errOut *cliWriter) int {
	report, err := serve.ImportResearchMetadataFileWithOptions(o.store, o.importMetadata, serve.MetadataImportOptions{DryRun: o.dryRun})
	if err != nil {
		errOut.line("iiifpreserve:", err)
		return 1
	}
	for _, warning := range report.Warnings {
		out.line("WARN", warning)
	}
	if o.dryRun {
		out.printf("iiifpreserve: import preview for %d bundle(s): %d catalogue change(s), %d annotation(s) would be added, %d duplicate(s) ignored (no files changed)\n",
			report.Bundles, report.CatalogChanges, report.AnnotationsAdded, report.Duplicates)
	} else {
		out.printf("iiifpreserve: imported %d bundle(s): %d catalogue change(s), %d annotation(s) added, %d duplicate(s) ignored\n",
			report.Bundles, report.CatalogChanges, report.AnnotationsAdded, report.Duplicates)
	}
	if out.err != nil {
		return 1
	}
	return 0
}

func runDoctor(o *options, out, errOut *cliWriter) int {
	report := serve.DiagnoseLibrary(o.store)
	for _, p := range report.Problems {
		line := fmt.Sprintf("%s %s: %s", p.Severity, p.Path, p.Message)
		if p.Severity == "ERROR" {
			errOut.line(line)
		} else {
			out.line(line)
		}
	}
	out.printf("iiifpreserve: doctor checked %d bundle(s), %d image(s), %d tile pyramid(s), %d file(s)\n",
		report.Bundles, report.Images, report.TilePyramids, report.FilesChecked)
	if !report.Healthy() || out.err != nil || errOut.err != nil {
		return 1
	}
	out.line("iiifpreserve: library is healthy")
	if out.err != nil {
		return 1
	}
	return 0
}

// crawl consumes the pipeline's classified results and, per Match, either
// previews the per-work image count (-dry-run, no fetch, no store) or
// preserves the images. It returns the manifest, match, and image tallies.
// A dry run short-circuits before any download even when store is non-nil,
// so the no-network guarantee does not depend on the caller nilling store.
func crawl(ctx context.Context, results iter.Seq[pipeline.Result], fetcher source.Fetcher, store preserve.BlobStore, dryRun bool, max int, out, errOut *cliWriter) (n, matched, images int) {
	for r := range results {
		out.line(formatResult(r))
		if out.err != nil {
			// stdout sink is gone (e.g. broken pipe): stop crawling.
			break
		}
		n++
		if r.Err == nil && r.Class == metadata.Match {
			matched++
			switch {
			case dryRun:
				cnt, line, err := dryRunLine(r.Manifest)
				if err != nil {
					errOut.line("iiifpreserve: enumerating", r.ManifestURL, "::", err)
				} else {
					images += cnt
					out.printf("%s", line)
				}
			case store != nil:
				sum, err := preserve.Preserve(ctx, fetcher, store, r.ManifestURL, r.Manifest)
				if err != nil {
					errOut.line("iiifpreserve: preserve", r.ManifestURL, "::", err)
				} else {
					images += sum.Stored
					out.printf("  preserved %d image(s) to %s (skipped %d, %d failed)\n",
						sum.Stored, sum.Dir, sum.Skipped, len(sum.Failures))
				}
			}
		}
		if max > 0 && n >= max {
			break
		}
	}
	return n, matched, images
}

// dryRunLine reports how many images a matched manifest holds, without
// downloading any of them — the per-work inventory line for a -dry-run crawl.
func dryRunLine(manifest []byte) (int, string, error) {
	imgs, err := preserve.EnumerateImages(manifest)
	if err != nil {
		return 0, "", err
	}
	return len(imgs), fmt.Sprintf("  %d image(s) (dry-run, not stored)\n", len(imgs)), nil
}

// runSummary is the final one-line tally. A dry run reports the images it
// would have preserved; a real run reports the images actually stored.
func runSummary(dryRun bool, n, matched, images int) string {
	if dryRun {
		return fmt.Sprintf("iiifpreserve: %d manifests, %d match, %d images (dry-run, not stored)",
			n, matched, images)
	}
	return fmt.Sprintf("iiifpreserve: %d manifests, %d match, %d images preserved",
		n, matched, images)
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

// serveAddr is the loopback bind address for a -serve port. Serving is
// localhost-only by design (single-researcher, no auth — same trust model
// as the rest of the tool), so the host is never operator-supplied.
func serveAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// runServe serves the preserved bundle dir over HTTPS until interrupted.
func runServe(ctx context.Context, o *options, out, errOut *cliWriter) int {
	addr := serveAddr(o.servePort)
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
	out.printf("%s", serveBanner(scheme, addr, o.store))

	if err := serve.New(o.store).ListenAndServe(ctx, addr, certFile, keyFile); err != nil {
		errOut.line("iiifpreserve: serve:", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
