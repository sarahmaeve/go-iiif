// Package institution is the single home for everything that varies by
// IIIF source: politeness, the User-Agent to present, and the metadata
// label→field mapping. Previously these were three parallel mechanisms
// (source.RatePolicy, source.builtinHostUserAgents, cmd's defaultMapping);
// this consolidates them behind one host-keyed Profile so adding a source
// is one place, not three. It imports only internal/metadata + time, so
// internal/source can depend on it without a cycle.
package institution

import (
	"time"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
)

// HonestUserAgent identifies the tool, purpose, and a contact URL so a
// one-time polite preservation fetch is distinguishable from an abusive
// scraper — and so bot-walls (e.g. Anubis) that penalise browser-spoofing
// UAs score it as benign. The default for every host.
const HonestUserAgent = "iiif-preserve/0.1 (+https://github.com/sarahmaeve/go-iiif; one-time preservation fetch of public-domain material)"

// browserUserAgent is a deliberate browser spoof, used ONLY for hosts that
// reject honest non-browser UAs (Gallica/BnF answers them with 403).
const browserUserAgent = "Mozilla/5.0 (compatible; iiif-preserve/0.1; +https://github.com/sarahmaeve/go-iiif)"

// Profile is the consolidated per-institution configuration.
type Profile struct {
	// Politeness — consumed by internal/source's rate limiter.
	MinInterval time.Duration
	Burst       int
	Jitter      time.Duration // 0 = no jitter (deliberate, e.g. Gallica)
	// UserAgent to present to this institution (never empty).
	UserAgent string
	// FieldMapping: normalized (lowercased, trimmed) label → FieldKind.
	FieldMapping metadata.FieldMapping
}

// Registry resolves a Profile by URL host, falling back to Default.
type Registry struct {
	Default Profile
	ByHost  map[string]Profile
}

// For returns the Profile for host, falling back to Default.
func (r Registry) For(host string) Profile {
	if p, ok := r.ByHost[host]; ok {
		return p
	}
	return r.Default
}

// defaultFieldMapping is the union of label conventions seen across the
// verified institutions (see VERIFIED.md). The strings are distinct enough
// that one shared map is correct for all; a host needing a genuinely
// conflicting label can still get a ByHost override.
func defaultFieldMapping() metadata.FieldMapping {
	return metadata.FieldMapping{
		// generic
		"language": metadata.FieldLanguage,
		"date":     metadata.FieldDate,
		"origin":   metadata.FieldOrigin,
		// Gallica/BnF
		"langue": metadata.FieldLanguage,
		// Digital Bodleian
		"date statement":  metadata.FieldDate,
		"place of origin": metadata.FieldOrigin,
		// e-codices (Swiss virtual manuscript library)
		"text language":             metadata.FieldLanguage,
		"date of origin (english)":  metadata.FieldDate,
		"place of origin (english)": metadata.FieldOrigin,
		"century":                   metadata.FieldDate,
	}
}

// Builtin is the canonical registry: an honest, gently-throttled, jittered
// default for any host, with Gallica/BnF overridden to its required
// browser UA and deliberate fixed 13s spacing (no jitter). e-codices,
// Bodleian, and the IIIF Cookbook all use the Default profile — their
// labels are covered by the shared mapping and they accept the honest UA.
func Builtin() Registry {
	return Registry{
		Default: Profile{
			MinInterval:  750 * time.Millisecond,
			Burst:        1,
			Jitter:       600 * time.Millisecond,
			UserAgent:    HonestUserAgent,
			FieldMapping: defaultFieldMapping(),
		},
		ByHost: map[string]Profile{
			"gallica.bnf.fr": {
				MinInterval:  13 * time.Second,
				Burst:        1,
				Jitter:       0,
				UserAgent:    browserUserAgent,
				FieldMapping: defaultFieldMapping(),
			},
		},
	}
}
