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
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/pipeline"
	"github.com/sarahmaeve/go-iiif/internal/source"
)

type options struct {
	collection string
	langs      []string
	from, to   int
	hasDate    bool
	places     []string
	max        int
	workers    int
	journal    string
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
		collection = fs.String("collection", "", "IIIF Collection root URL (required)")
		lang       = fs.String("lang", "", "comma-separated ISO 639-1 language codes")
		from       = fs.Int("from", 0, "earliest year (inclusive)")
		to         = fs.Int("to", 0, "latest year (inclusive)")
		place      = fs.String("place", "", "comma-separated place substrings")
		max        = fs.Int("max", 0, "stop after N manifests (0 = unlimited)")
		workers    = fs.Int("workers", 1, "concurrent manifest workers (1 = sequential; per-host politeness still enforced)")
		journal    = fs.String("journal", "", "path to a resumable crawl journal (optional)")
	)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *collection == "" {
		return nil, errors.New("-collection is required")
	}
	o := &options{
		collection: *collection,
		langs:      splitCSV(*lang),
		from:       *from,
		to:         *to,
		hasDate:    *from != 0 || *to != 0,
		places:     splitCSV(*place),
		max:        *max,
		workers:    *workers,
		journal:    *journal,
	}
	return o, nil
}

func formatResult(r pipeline.Result) string {
	if r.Err != nil {
		return "ERROR     " + r.ManifestURL + " :: " + r.Err.Error()
	}
	var label string
	switch r.Class {
	case metadata.Match:
		label = "MATCH    "
	case metadata.NoMatch:
		label = "NO-MATCH "
	default:
		label = "UNCERTAIN"
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

func run(args []string, stdout, stderr io.Writer) int {
	o, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "iiifpreserve:", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var src source.Source = source.NewCollectionSource(
		source.NewPoliteFetcher(source.NewHTTPFetcher()), o.collection)
	if o.journal != "" {
		j, err := source.OpenFileJournal(o.journal)
		if err != nil {
			fmt.Fprintln(stderr, "iiifpreserve:", err)
			return 1
		}
		defer func() { _ = j.Close() }()
		src = source.NewResumableSource(src, j)
	}

	p := pipeline.New(pipeline.Config{
		Source:  src,
		Fetcher: source.NewPoliteFetcher(source.NewHTTPFetcher()),
		Mapping: defaultMapping(),
		Filter:  o.filter(),
		Workers: o.workers,
	})

	var n, matched int
	for r := range p.Run(ctx) {
		fmt.Fprintln(stdout, formatResult(r))
		n++
		if r.Err == nil && r.Class == metadata.Match {
			matched++
		}
		if o.max > 0 && n >= o.max {
			break
		}
	}
	fmt.Fprintf(stderr, "iiifpreserve: %d manifests, %d match\n", n, matched)
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
