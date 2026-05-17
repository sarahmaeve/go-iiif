package metadata

import (
	"os"
	"path/filepath"
	"testing"
)

// seedManifests adds the real testdata manifests to a fuzz corpus so the
// fuzzer mutates from genuine IIIF shapes, not just synthetic bytes.
func seedManifests(f *testing.F) {
	f.Helper()
	matches, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		f.Fatalf("globbing testdata: %v", err)
	}
	for _, p := range matches {
		b, err := os.ReadFile(p) //nolint:gosec // G304: test-controlled testdata path
		if err != nil {
			f.Fatalf("reading %s: %v", p, err)
		}
		f.Add(b)
	}
}

// FuzzParseDateRange asserts the parser's contract over arbitrary input:
// it never panics, is deterministic, and every successfully parsed range
// is well-formed (Start <= End).
func FuzzParseDateRange(f *testing.F) {
	for _, s := range []string{
		"", "1450", "1450-1480", "1480-1450", "15th century", "1st century",
		"circa 1480", "ca. 1480", "XVe siècle", "s. XV", "siglo xv",
		"a fine manuscript", "  ", "0", "9999-0", "mmmmmmmmmm siecle",
		"-1450", "0th century", "0e siecle",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, in string) {
		got, err := ParseDateRange(in)

		// Determinism: identical input must yield identical output.
		got2, err2 := ParseDateRange(in)
		if (err == nil) != (err2 == nil) || got != got2 {
			t.Fatalf("ParseDateRange(%q) not deterministic: (%+v,%v) vs (%+v,%v)",
				in, got, err, got2, err2)
		}

		if err != nil {
			// On error the zero DateRange is returned; no further contract.
			return
		}
		if got.Start > got.End {
			t.Fatalf("ParseDateRange(%q) = %+v: Start > End", in, got)
		}
	})
}

// FuzzParseLanguage asserts no panic and the round-trip invariant: any code
// ParseLanguage returns must itself parse back to the same code (ISO 639-1
// codes are documented to map to themselves).
func FuzzParseLanguage(f *testing.F) {
	for _, s := range []string{
		"", "  ", "french", "Français", "FR", "eng", "en", "Latin",
		"deutsch", "español", "unknown", "e n", "fr ", "\x00", "ﬀ",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, in string) {
		code, err := ParseLanguage(in)
		if err != nil {
			if code != "" {
				t.Fatalf("ParseLanguage(%q) returned code %q with error %v", in, code, err)
			}
			return
		}
		if code == "" {
			t.Fatalf("ParseLanguage(%q) returned empty code with nil error", in)
		}
		// Round-trip: a returned code is a valid input mapping to itself.
		again, err2 := ParseLanguage(code)
		if err2 != nil || again != code {
			t.Fatalf("ParseLanguage round-trip broke: %q -> %q -> (%q,%v)",
				in, code, again, err2)
		}
	})
}

// FuzzExtractMetadata asserts the extractor never panics on arbitrary bytes,
// is deterministic, and honors its documented contract that entries with an
// empty label are skipped (so every returned entry has a non-empty Label).
func FuzzExtractMetadata(f *testing.F) {
	seedManifests(f)
	for _, s := range []string{
		``, `{}`, `null`, `[]`, `{"metadata":null}`, `{"metadata":[]}`,
		`{"metadata":[{"label":"x","value":"y"}]}`,
		`{"metadata":[{"label":"","value":"y"}]}`,
		`{"metadata":[{"label":{"en":["t"]},"value":[{"@value":"v"}]}]}`,
		`{"metadata":[{"label":{"@value":1.5}}]}`,
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, manifest []byte) {
		entries, err := ExtractMetadata(manifest)
		if err != nil {
			if entries != nil {
				t.Fatalf("ExtractMetadata error path returned non-nil entries: %v", entries)
			}
			return
		}
		for i, e := range entries {
			if e.Label == "" {
				t.Fatalf("ExtractMetadata returned entry %d with empty label: %+v", i, e)
			}
		}
		// Determinism on the success path.
		again, err2 := ExtractMetadata(manifest)
		if err2 != nil || len(again) != len(entries) {
			t.Fatalf("ExtractMetadata not deterministic: %d vs %d (err %v)",
				len(entries), len(again), err2)
		}
	})
}

// FuzzTitle asserts Title never panics on arbitrary bytes and is deterministic.
func FuzzTitle(f *testing.F) {
	seedManifests(f)
	for _, s := range []string{
		``, `{}`, `null`, `{"label":"A Manuscript"}`,
		`{"label":{"en":["Title"],"fr":["Titre"]}}`,
		`{"label":[{"@value":"v2","@language":"en"}]}`,
		`{"label":{"@value":3}}`,
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, manifest []byte) {
		got := Title(manifest)
		if got2 := Title(manifest); got != got2 {
			t.Fatalf("Title not deterministic: %q vs %q", got, got2)
		}
	})
}

// FuzzNormalizeIIIFText drives coerceText/coerceArray/coerceMap through their
// public entry point with arbitrary JSON, asserting no panic (including on
// deeply nested or recursive shapes) and determinism.
func FuzzNormalizeIIIFText(f *testing.F) {
	for _, s := range []string{
		``, `null`, `"plain"`, `1.5`, `true`, `[]`, `{}`,
		`["a","b"]`, `[{"@value":"v","@language":"en"}]`,
		`{"en":["x"],"fr":["y"],"none":["z"]}`,
		`{"@value":"v"}`, `{"value":"v","language":"en"}`,
		`[[[["deep"]]]]`, `{"a":{"b":{"c":"d"}}}`,
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		got := normalizeIIIFText(raw, defaultPrefLangs)
		if got2 := normalizeIIIFText(raw, defaultPrefLangs); got != got2 {
			t.Fatalf("normalizeIIIFText not deterministic: %q vs %q", got, got2)
		}
		// Empty prefLangs must also be safe (no out-of-range, no panic).
		_ = normalizeIIIFText(raw, nil)
	})
}

// FuzzBuildWorkRecord exercises the composite normalizer with two arbitrary
// label/value pairs both mapped to language, asserting no panic and that
// the documented language de-duplication holds.
func FuzzBuildWorkRecord(f *testing.F) {
	f.Add("language", "French", "Language", "fr")
	f.Add("date", "1450-1480", "lang", "english")
	f.Add("", "", "", "")

	f.Fuzz(func(t *testing.T, l1, v1, l2, v2 string) {
		meta := []MetadataEntry{{Label: l1, Value: v1}, {Label: l2, Value: v2}}
		mapping := FieldMapping{"language": FieldLanguage, "date": FieldDate, "origin": FieldOrigin}

		rec := BuildWorkRecord(meta, mapping)

		seen := make(map[string]bool, len(rec.Langs))
		for _, code := range rec.Langs {
			if seen[code] {
				t.Fatalf("BuildWorkRecord produced duplicate lang %q in %v", code, rec.Langs)
			}
			seen[code] = true
		}
	})
}
