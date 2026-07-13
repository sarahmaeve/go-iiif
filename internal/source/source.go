// Package source discovers IIIF manifest URLs from an institution, emitting a
// uniform stream regardless of the underlying discovery mechanism (DESIGN
// §4.1). The collection adapter (universal IIIF Collection tree walk) is the
// guaranteed path; a changestream adapter is planned where institutions
// publish the IIIF Change Discovery API.
package source

import (
	"context"
	"errors"
	"iter"
)

// ErrNotFound is returned by a Fetcher when a URL has no resource.
var ErrNotFound = errors.New("source: resource not found")

// Fetcher retrieves the raw bytes of a IIIF resource. Production uses the
// polite trawler (rate-limited, conditional GET); tests use an in-memory fake.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// ManifestDerivation describes a manifest representation synthesized from a
// different source response. Fetchers that perform such a transformation can
// expose it through ManifestDerivationProvider so preservation provenance does
// not mistake derived bytes for the representation returned by manifestURL.
type ManifestDerivation struct {
	Method       string
	SourceURL    string
	SourceSHA256 string
}

// ManifestDerivationProvider is an optional Fetcher capability. The manifest
// bytes participate in the lookup so a later ordinary response for the same
// URL cannot inherit stale derivation metadata.
type ManifestDerivationProvider interface {
	ManifestDerivation(manifestURL string, manifest []byte) (ManifestDerivation, bool)
}

// Source emits a stream of manifest URLs. The stream is pull-based so callers
// can stop early — required for polite, resumable crawls.
type Source interface {
	Manifests(ctx context.Context) iter.Seq2[string, error]
}
