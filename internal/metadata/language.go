package metadata

import (
	"errors"
	"strings"
)

// ErrUnknownLanguage reports that a language string could not be normalized
// to an ISO 639-1 code.
var ErrUnknownLanguage = errors.New("metadata: unknown language")

// languageAliases maps a normalized (lowercased, space-trimmed) language
// string — English name, endonym, or ISO 639-2 B/T code — to its ISO 639-1
// code. ISO 639-1 codes map to themselves.
var languageAliases = map[string]string{
	"french":   "fr",
	"français": "fr",
	"francais": "fr",
	"fre":      "fr",
	"fra":      "fr",
	"fr":       "fr",

	"latin": "la",
	"lat":   "la",
	"la":    "la",

	"english": "en",
	"eng":     "en",
	"en":      "en",

	"german":  "de",
	"deutsch": "de",
	"ger":     "de",
	"deu":     "de",
	"de":      "de",

	"italian":  "it",
	"italiano": "it",
	"ita":      "it",
	"it":       "it",

	"spanish":    "es",
	"español":    "es",
	"espanol":    "es",
	"castellano": "es",
	"spa":        "es",
	"es":         "es",

	"greek": "el",
	"gre":   "el",
	"ell":   "el",
	"el":    "el",

	"dutch":      "nl",
	"nederlands": "nl",
	"dut":        "nl",
	"nld":        "nl",
	"nl":         "nl",
}

// ParseLanguage normalizes a free-text language value to its ISO 639-1 code.
// It accepts the English name, the endonym, and ISO 639-2 B/T codes, and is
// case- and surrounding-whitespace-insensitive. It returns ErrUnknownLanguage
// when the input is empty or unrecognized.
func ParseLanguage(s string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(s))
	if key == "" {
		return "", ErrUnknownLanguage
	}
	if code, ok := languageAliases[key]; ok {
		return code, nil
	}
	return "", ErrUnknownLanguage
}
