package metadata

import (
	"encoding/json"
	"sort"
	"strconv"
)

// normalizeIIIFText coerces a IIIF Presentation label/value — which may be a
// plain string, a v2 localized {"@value","@language"} object, an array of
// either, or a v3 BCP-47 language map {"en":[…],"fr":[…]} — down to one
// representative string. Languages in prefLangs are preferred in order; the
// v3 "none" key and single-key maps are sensible fallbacks. Absent or
// non-text values yield "". This is the tolerant approach the reference
// downloader uses (get_meta/mono_val), so a manifest never hard-fails on a
// label/value shape.
func normalizeIIIFText(raw json.RawMessage, prefLangs []string) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return coerceText(v, prefLangs)
}

func coerceText(v any, prefLangs []string) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any:
		return coerceArray(t, prefLangs)
	case map[string]any:
		return coerceMap(t, prefLangs)
	default:
		return ""
	}
}

// coerceArray handles a v2 array of localized objects (or plain strings):
// a preferred-language entry wins, else the first non-empty coercion.
func coerceArray(arr []any, prefLangs []string) string {
	for _, lang := range prefLangs {
		for _, e := range arr {
			if m, ok := e.(map[string]any); ok && localizedLang(m) == lang {
				if s := coerceText(localizedValue(m), prefLangs); s != "" {
					return s
				}
			}
		}
	}
	for _, e := range arr {
		if s := coerceText(e, prefLangs); s != "" {
			return s
		}
	}
	return ""
}

// coerceMap handles a v2 localized object ({"@value","@language"}) or a v3
// language map. Preference: an @value object; then prefLangs in order; then
// the v3 "none" key; then a lone key; then the first key in sorted order
// (deterministic).
func coerceMap(m map[string]any, prefLangs []string) string {
	if val, ok := m["@value"]; ok {
		return coerceText(val, prefLangs)
	}
	for _, lang := range prefLangs {
		if val, ok := m[lang]; ok {
			if s := coerceText(val, prefLangs); s != "" {
				return s
			}
		}
	}
	if val, ok := m["none"]; ok {
		if s := coerceText(val, prefLangs); s != "" {
			return s
		}
	}
	if len(m) == 1 {
		for _, val := range m {
			return coerceText(val, prefLangs)
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s := coerceText(m[k], prefLangs); s != "" {
			return s
		}
	}
	return ""
}

// localizedLang / localizedValue read the v2 {"@value","@language"} pair
// (also tolerating the un-prefixed spelling).
func localizedLang(m map[string]any) string {
	if s, ok := m["@language"].(string); ok {
		return s
	}
	if s, ok := m["language"].(string); ok {
		return s
	}
	return ""
}

func localizedValue(m map[string]any) any {
	if v, ok := m["@value"]; ok {
		return v
	}
	return m["value"]
}
