package institution

import (
	"testing"
	"time"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
)

func TestBuiltin_Default(t *testing.T) {
	d := Builtin().For("anything.example.org")

	if d.MinInterval != 750*time.Millisecond || d.Burst != 1 || d.Jitter != 600*time.Millisecond {
		t.Fatalf("default politeness = %+v, want 750ms/1/600ms", d)
	}
	if d.UserAgent == "" || containsFold(d.UserAgent, "mozilla") {
		t.Fatalf("default UA must be honest (non-Mozilla, identifying): %q", d.UserAgent)
	}
	// Generic + pilot-institution labels resolve.
	for label, want := range map[string]metadata.FieldKind{
		"language":        metadata.FieldLanguage,
		"langue":          metadata.FieldLanguage, // Gallica
		"date statement":  metadata.FieldDate,     // Bodleian
		"place of origin": metadata.FieldOrigin,   // Bodleian
	} {
		if d.FieldMapping[label] != want {
			t.Errorf("default mapping[%q] = %v, want %v", label, d.FieldMapping[label], want)
		}
	}
}

func TestBuiltin_Gallica(t *testing.T) {
	g := Builtin().For("gallica.bnf.fr")

	if g.MinInterval != 13*time.Second || g.Jitter != 0 {
		t.Fatalf("Gallica politeness = %+v, want fixed 13s, no jitter", g)
	}
	if !containsFold(g.UserAgent, "mozilla") {
		t.Fatalf("Gallica UA must be browser-like (it 403s honest UAs): %q", g.UserAgent)
	}
}

func TestBuiltin_ECodicesFieldMapping(t *testing.T) {
	e := Builtin().For("www.e-codices.unifr.ch")

	// e-codices uses the honest default UA and standard rate (verified live)…
	if e.UserAgent == "" || containsFold(e.UserAgent, "mozilla") {
		t.Fatalf("e-codices should use the honest default UA: %q", e.UserAgent)
	}
	if e.MinInterval != 750*time.Millisecond {
		t.Fatalf("e-codices rate = %v, want the default 750ms", e.MinInterval)
	}
	// …but its label vocabulary must now classify.
	for label, want := range map[string]metadata.FieldKind{
		"text language":             metadata.FieldLanguage,
		"date of origin (english)":  metadata.FieldDate,
		"place of origin (english)": metadata.FieldOrigin,
		"century":                   metadata.FieldDate,
	} {
		if e.FieldMapping[label] != want {
			t.Errorf("e-codices mapping[%q] = %v, want %v", label, e.FieldMapping[label], want)
		}
	}
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && (indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	ls, lsub := toLower(s), toLower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
