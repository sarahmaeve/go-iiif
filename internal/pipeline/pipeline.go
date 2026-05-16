// Package pipeline wires discovery, fetching, metadata normalization and the
// conservative filter into one pass: config(institutions) → Source → manifest
// fetch → normalize → filter (DESIGN §3). It classifies and routes; the
// preservation half (image fetch/tile/store) consumes the Match results.
package pipeline

import (
	"context"
	"fmt"
	"iter"
	neturl "net/url"
	"sync"

	"github.com/sarahmaeve/go-iiif/internal/institution"
	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/source"
)

// Config is the wiring for one pipeline run.
type Config struct {
	Source  source.Source
	Fetcher source.Fetcher
	// Institutions resolves the per-institution profile (incl. the
	// label→field mapping) by each manifest's URL host.
	Institutions institution.Registry
	// Filter is the researcher's selection predicate.
	Filter metadata.Filter
	// Workers is the number of concurrent per-manifest workers. <=1 runs
	// sequentially and yields in Source order; >1 fans manifest processing
	// out across a bounded pool and yields in completion order. Per-host
	// politeness still holds because the shared Fetcher's per-host limiter
	// is enforced regardless of worker count.
	Workers int
}

// Result is the outcome for one manifest. On a fetch/parse failure Err is
// set; Class is then meaningless (callers check Err first).
type Result struct {
	ManifestURL string
	Class       metadata.Classification
	Record      metadata.WorkRecord
	// Manifest is the raw fetched manifest, carried so a Match can be
	// preserved without re-fetching it. Nil on a fetch failure.
	Manifest []byte
	Err      error
}

// Pipeline runs a Config.
type Pipeline struct {
	cfg Config
}

// New returns a Pipeline for cfg.
func New(cfg Config) *Pipeline {
	return &Pipeline{cfg: cfg}
}

// Run walks the Source and yields one Result per manifest: fetched,
// normalized into a WorkRecord, and classified by the Filter. With
// Config.Workers > 1 the per-manifest stage is fanned out and Results are
// yielded in completion order; otherwise it is sequential and ordered.
func (p *Pipeline) Run(ctx context.Context) iter.Seq[Result] {
	if p.cfg.Workers > 1 {
		return p.runConcurrent(ctx)
	}
	return p.runSequential(ctx)
}

func (p *Pipeline) runSequential(ctx context.Context) iter.Seq[Result] {
	return func(yield func(Result) bool) {
		for url, err := range p.cfg.Source.Manifests(ctx) {
			if err != nil {
				if !yield(Result{ManifestURL: url, Err: err}) {
					return
				}
				continue
			}
			if !yield(p.process(ctx, url)) {
				return
			}
		}
	}
}

// runConcurrent keeps the (cheap, rate-limited) Source walk as a single
// producer and fans the expensive per-manifest work across Workers
// goroutines. Channel ownership: the producer owns urls, the workers'
// WaitGroup owner owns results. Every send selects on ctx.Done() so a
// consumer that stops early (cancel) cannot leak goroutines.
func (p *Pipeline) runConcurrent(ctx context.Context) iter.Seq[Result] {
	return func(yield func(Result) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		urls := make(chan string)
		results := make(chan Result)

		// Producer: drain the Source. Owns and closes urls.
		go func() {
			defer close(urls)
			for url, err := range p.cfg.Source.Manifests(ctx) {
				if err != nil {
					select {
					case results <- Result{ManifestURL: url, Err: err}:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case urls <- url:
				case <-ctx.Done():
					return
				}
			}
		}()

		// Workers: process manifests until urls is drained or ctx is done.
		var wg sync.WaitGroup
		for range p.cfg.Workers {
			wg.Go(func() {
				for url := range urls {
					select {
					case results <- p.process(ctx, url):
					case <-ctx.Done():
						return
					}
				}
			})
		}

		// Closer: the WaitGroup owner closes results.
		go func() {
			wg.Wait()
			close(results)
		}()

		for r := range results {
			if !yield(r) {
				cancel()
				// Drain until the closer closes results so every
				// goroutine has exited before we return.
				for range results { //nolint:revive // intentional drain
				}
				return
			}
		}
	}
}

func (p *Pipeline) process(ctx context.Context, url string) Result {
	body, err := p.cfg.Fetcher.Fetch(ctx, url)
	if err != nil {
		return Result{ManifestURL: url, Err: fmt.Errorf("pipeline: fetching %s: %w", url, err)}
	}
	entries, err := metadata.ExtractMetadata(body)
	if err != nil {
		return Result{ManifestURL: url, Err: fmt.Errorf("pipeline: %s: %w", url, err)}
	}
	host := ""
	if u, perr := neturl.Parse(url); perr == nil {
		host = u.Host
	}
	mapping := p.cfg.Institutions.For(host).FieldMapping
	rec := metadata.BuildWorkRecord(entries, mapping)
	return Result{
		ManifestURL: url,
		Class:       p.cfg.Filter.Classify(rec),
		Record:      rec,
		Manifest:    body,
	}
}
