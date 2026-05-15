package metadata

import "strings"

// FieldKind classifies what a piece of IIIF Presentation metadata represents
// for filtering purposes. The zero value, FieldIgnore, means a label is not
// mapped and its value is dropped.
type FieldKind int

const (
	FieldIgnore FieldKind = iota
	FieldLanguage
	FieldDate
	FieldOrigin
)

// FieldMapping maps a normalized (lowercased, trimmed) metadata label to the
// FieldKind it should populate. It is supplied per institution, since
// Presentation metadata labels are free-text and inconsistent.
type FieldMapping map[string]FieldKind

// MetadataEntry is one label/value pair from a IIIF Presentation manifest's
// metadata array.
type MetadataEntry struct {
	Label string
	Value string
}

// WorkRecord is the typed, normalized view of a work's metadata that filters
// run clean predicates over. Classification (match/no-match) is the
// filter's responsibility, not this type's.
type WorkRecord struct {
	Langs     []string // ISO 639-1 codes
	DateRange DateRange
	Origin    string
}

// BuildWorkRecord normalizes raw Presentation metadata into a WorkRecord using
// the per-institution mapping. Unmapped labels are ignored; values that fail
// to parse are skipped rather than guessed at.
func BuildWorkRecord(meta []MetadataEntry, mapping FieldMapping) WorkRecord {
	var rec WorkRecord
	seenLang := make(map[string]bool)
	for _, e := range meta {
		key := strings.ToLower(strings.TrimSpace(e.Label))
		switch mapping[key] {
		case FieldLanguage:
			if code, err := ParseLanguage(e.Value); err == nil && !seenLang[code] {
				seenLang[code] = true
				rec.Langs = append(rec.Langs, code)
			}
		case FieldDate:
			if dr, err := ParseDateRange(e.Value); err == nil {
				rec.DateRange = dr
			}
		case FieldOrigin:
			rec.Origin = strings.TrimSpace(e.Value)
		case FieldIgnore:
			// Unmapped label: drop it.
		}
	}
	return rec
}
