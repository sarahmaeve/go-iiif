package metadata

import "slices"

// Classification is the conservative three-bucket result of filtering a
// WorkRecord (DESIGN §4.3). The zero value is Uncertain so that any unset
// result defaults to the review queue — never a silent drop or fetch.
type Classification int

const (
	// Uncertain: insufficient/ambiguous metadata to decide; route to the
	// review queue, do not fetch until a researcher approves.
	Uncertain Classification = iota
	// NoMatch: the record confidently fails a criterion; exclude it.
	NoMatch
	// Match: every specified criterion is confidently satisfied.
	Match
)

// Filter is a researcher's selection predicate over typed WorkRecords. A
// zero-valued constraint means "no constraint on that field".
type Filter struct {
	Languages []string   // ISO 639-1; any-of. Empty = no language constraint.
	Date      *DateRange // nil = no date constraint.
}

// Classify applies the filter conservatively: Match only when every specified
// criterion is satisfied, NoMatch as soon as one confidently fails, Uncertain
// when a criterion lacks the data to decide. NoMatch dominates Uncertain,
// which dominates Match.
func (f Filter) Classify(rec WorkRecord) Classification {
	result := Match
	for _, c := range []Classification{f.classifyLanguage(rec), f.classifyDate(rec)} {
		switch c {
		case NoMatch:
			return NoMatch
		case Uncertain:
			result = Uncertain
		}
	}
	return result
}

func (f Filter) classifyLanguage(rec WorkRecord) Classification {
	if len(f.Languages) == 0 {
		return Match
	}
	if len(rec.Langs) == 0 {
		return Uncertain
	}
	if slices.ContainsFunc(rec.Langs, func(l string) bool {
		return slices.Contains(f.Languages, l)
	}) {
		return Match
	}
	return NoMatch
}

func (f Filter) classifyDate(rec WorkRecord) Classification {
	if f.Date == nil {
		return Match
	}
	if rec.DateRange == (DateRange{}) {
		return Uncertain
	}
	// Inclusive overlap of two year spans.
	if rec.DateRange.Start <= f.Date.End && f.Date.Start <= rec.DateRange.End {
		return Match
	}
	return NoMatch
}
