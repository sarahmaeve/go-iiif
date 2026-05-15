// Package pipeline wires discovery, fetching, metadata normalization and the
// conservative filter into one pass: config(institutions) → Source → manifest
// fetch → normalize → filter (DESIGN §3). It classifies and routes; the
// preservation half (image fetch/tile/store) consumes the Match results.
package pipeline

import (
	"context"
	"fmt"
	"iter"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/source"
)

// Config is the wiring for one pipeline run.
type Config struct {
	Source  source.Source
	Fetcher source.Fetcher
	// Mapping is the per-institution label→field mapping for the manifests
	// this Source yields.
	Mapping metadata.FieldMapping
	// Filter is the researcher's selection predicate.
	Filter metadata.Filter
}

// Result is the outcome for one manifest. On a fetch/parse failure Err is set
// and Class is the zero value (Uncertain) — never silently dropped.
type Result struct {
	ManifestURL string
	Class       metadata.Classification
	Record      metadata.WorkRecord
	Err         error
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
// normalized into a WorkRecord, and classified by the Filter.
func (p *Pipeline) Run(ctx context.Context) iter.Seq[Result] {
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

func (p *Pipeline) process(ctx context.Context, url string) Result {
	body, err := p.cfg.Fetcher.Fetch(ctx, url)
	if err != nil {
		return Result{ManifestURL: url, Err: fmt.Errorf("pipeline: fetching %s: %w", url, err)}
	}
	entries, err := metadata.ExtractV2Metadata(body)
	if err != nil {
		return Result{ManifestURL: url, Err: fmt.Errorf("pipeline: %s: %w", url, err)}
	}
	rec := metadata.BuildWorkRecord(entries, p.cfg.Mapping)
	return Result{
		ManifestURL: url,
		Class:       p.cfg.Filter.Classify(rec),
		Record:      rec,
	}
}
