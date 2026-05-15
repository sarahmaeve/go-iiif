// Package metadata normalizes free-text, multilingual IIIF Presentation
// metadata into typed records that filters can run clean predicates over.
package metadata

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// ErrNoDate reports that no date could be parsed from the input string.
var ErrNoDate = errors.New("metadata: no parseable date")

// DateRange is an inclusive span of years. A single year is represented as
// Start == End.
type DateRange struct {
	Start int
	End   int
}

var (
	reYear    = regexp.MustCompile(`^\d{1,4}$`)
	reRange   = regexp.MustCompile(`^(\d{1,4})\s*[-–—]\s*(\d{1,4})$`)
	reCentury = regexp.MustCompile(`^(\d{1,2})(?:st|nd|rd|th)\s+century$`)
	reCirca   = regexp.MustCompile(`^(?:circa|ca\.?|c\.?)\s*(\d{1,4})$`)
	reSpaces  = regexp.MustCompile(`\s+`)

	// French: "xve siècle", "xv siecle" (norm is lowercased, spaces collapsed).
	reCenturyFR = regexp.MustCompile(`^([ivxlcdm]+)(?:e|eme|ème|ère|er)?\s+si[eè]cle$`)
	// Spanish: "s. xv", "s xv", "siglo xv".
	reCenturyES = regexp.MustCompile(`^(?:s\.?|siglo)\s+([ivxlcdm]+)$`)
)

var romanValues = map[byte]int{'i': 1, 'v': 5, 'x': 10, 'l': 50, 'c': 100, 'd': 500, 'm': 1000}

// romanToInt parses a lowercase Roman numeral using the standard subtractive
// rule. It returns 0 for input that is not a well-formed Roman numeral.
func romanToInt(s string) int {
	total, prev := 0, 0
	for i := len(s) - 1; i >= 0; i-- {
		v, ok := romanValues[s[i]]
		if !ok {
			return 0
		}
		if v < prev {
			total -= v
		} else {
			total += v
			prev = v
		}
	}
	return total
}

// circaSlack is the number of years a "circa"/"c."/"ca." date is widened by
// on each side, turning an approximate point date into a fuzzy range.
const circaSlack = 20

// centuryRange returns the inclusive year span for the nth century CE,
// e.g. n=15 → 1401..1500.
func centuryRange(n int) DateRange {
	return DateRange{Start: (n-1)*100 + 1, End: n * 100}
}

// ParseDateRange parses a free-text date string into an inclusive DateRange.
// It returns ErrNoDate when the input contains no recognizable date.
func ParseDateRange(s string) (DateRange, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DateRange{}, ErrNoDate
	}

	norm := reSpaces.ReplaceAllString(strings.ToLower(s), " ")
	if m := reCentury.FindStringSubmatch(norm); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n == 0 {
			return DateRange{}, ErrNoDate
		}
		return centuryRange(n), nil
	}

	if m := reCenturyFR.FindStringSubmatch(norm); m != nil {
		if n := romanToInt(m[1]); n > 0 {
			return centuryRange(n), nil
		}
	}

	if m := reCenturyES.FindStringSubmatch(norm); m != nil {
		if n := romanToInt(m[1]); n > 0 {
			return centuryRange(n), nil
		}
	}

	if m := reCirca.FindStringSubmatch(norm); m != nil {
		y, err := strconv.Atoi(m[1])
		if err != nil {
			return DateRange{}, ErrNoDate
		}
		return DateRange{Start: y - circaSlack, End: y + circaSlack}, nil
	}

	if reYear.MatchString(s) {
		y, err := strconv.Atoi(s)
		if err != nil {
			return DateRange{}, ErrNoDate
		}
		return DateRange{Start: y, End: y}, nil
	}

	if m := reRange.FindStringSubmatch(s); m != nil {
		lo, err1 := strconv.Atoi(m[1])
		hi, err2 := strconv.Atoi(m[2])
		if err1 != nil || err2 != nil {
			return DateRange{}, ErrNoDate
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		return DateRange{Start: lo, End: hi}, nil
	}

	return DateRange{}, ErrNoDate
}
