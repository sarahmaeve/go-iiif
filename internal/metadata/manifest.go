package metadata

import (
	"encoding/json"
	"fmt"
)

// defaultPrefLangs is the language preference used when a label or value is
// multilingual. English first, then whatever the normalizer falls back to
// (v3 "none", single key, first sorted key) so a non-English-only manifest
// still yields usable text.
var defaultPrefLangs = []string{"en"}

// ExtractMetadata pulls the top-level `metadata` array out of a IIIF
// Presentation manifest as label/value pairs, for both Presentation API 2.x
// and 3.0. Label and value are decoded opaquely and coerced by
// normalizeIIIFText, so every IIIF text shape — plain string, v2 localized
// {"@value","@language"} (or arrays), v3 language map {"en":[…]} — is read
// rather than hard-failing the manifest. Entries with no usable label are
// skipped.
func ExtractMetadata(manifest []byte) ([]MetadataEntry, error) {
	var m struct {
		Metadata []struct {
			Label json.RawMessage `json:"label"`
			Value json.RawMessage `json:"value"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return nil, fmt.Errorf("metadata: decoding manifest: %w", err)
	}

	entries := make([]MetadataEntry, 0, len(m.Metadata))
	for _, e := range m.Metadata {
		label := normalizeIIIFText(e.Label, defaultPrefLangs)
		if label == "" {
			continue
		}
		entries = append(entries, MetadataEntry{
			Label: label,
			Value: normalizeIIIFText(e.Value, defaultPrefLangs),
		})
	}
	return entries, nil
}

// Title returns the manifest's top-level label as one display string,
// coercing any v2/v3 label shape (plain string, localized array, v3
// language map) via the same normalizer used for metadata values. Empty
// if absent or the manifest is not JSON.
func Title(manifest []byte) string {
	var m struct {
		Label json.RawMessage `json:"label"`
	}
	if json.Unmarshal(manifest, &m) != nil {
		return ""
	}
	return normalizeIIIFText(m.Label, defaultPrefLangs)
}

// ExtractV2Metadata is the former name of ExtractMetadata, retained so
// existing callers/tests keep working. Extraction is now version-agnostic.
func ExtractV2Metadata(manifest []byte) ([]MetadataEntry, error) {
	return ExtractMetadata(manifest)
}
