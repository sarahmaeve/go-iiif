package metadata

import (
	"slices"
	"strings"
)

// Classification is the result of filtering a WorkRecord against the
// researcher's predicate. Two outcomes only. When a filtered field is
// absent the policy is lenient (Match): this is a preservation tool, so
// losing a possibly-wanted manuscript is worse than an extra download. The
// zero value is Match, so an unset result is never silently dropped.
type Classification int

const (
	// Match: keep it — every specified criterion is satisfied, or a
	// criterion's data is absent (lenient).
	Match Classification = iota
	// NoMatch: the record confidently fails a specified criterion.
	NoMatch
)

// Filter is a researcher's selection predicate over typed WorkRecords. A
// zero-valued constraint means "no constraint on that field".
type Filter struct {
	Languages []string   // ISO 639-1; any-of. Empty = no language constraint.
	Date      *DateRange // nil = no date constraint.
	Places    []string   // any-of, case-insensitive substring of Origin. Empty = no constraint.
}

// Classify returns NoMatch as soon as a specified criterion confidently
// fails; otherwise Match. A criterion whose data is absent does not exclude
// (lenient — preserve when unsure).
func (f Filter) Classify(rec WorkRecord) Classification {
	per := []Classification{f.classifyLanguage(rec), f.classifyDate(rec), f.classifyOrigin(rec)}
	if slices.Contains(per, NoMatch) {
		return NoMatch
	}
	return Match
}

func (f Filter) classifyLanguage(rec WorkRecord) Classification {
	if len(f.Languages) == 0 || len(rec.Langs) == 0 {
		return Match // no constraint, or no data to exclude on
	}
	if slices.ContainsFunc(rec.Langs, func(l string) bool {
		return slices.Contains(f.Languages, l)
	}) {
		return Match
	}
	return NoMatch
}

func (f Filter) classifyDate(rec WorkRecord) Classification {
	if f.Date == nil || rec.DateRange == (DateRange{}) {
		return Match // no constraint, or no parsed date to exclude on
	}
	// Inclusive overlap of two year spans.
	if rec.DateRange.Start <= f.Date.End && f.Date.Start <= rec.DateRange.End {
		return Match
	}
	return NoMatch
}

func (f Filter) classifyOrigin(rec WorkRecord) Classification {
	if len(f.Places) == 0 {
		return Match
	}
	origin := strings.ToLower(strings.TrimSpace(rec.Origin))
	if origin == "" {
		return Match // no origin data to exclude on
	}
	for _, p := range f.Places {
		if strings.Contains(origin, strings.ToLower(strings.TrimSpace(p))) {
			return Match
		}
	}
	return NoMatch
}
