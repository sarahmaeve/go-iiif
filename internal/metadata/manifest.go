package metadata

import (
	"encoding/json"
	"fmt"
)

// ExtractV2Metadata pulls the top-level metadata array out of a IIIF
// Presentation API 2.x manifest as label/value pairs.
//
// IIIF v2 permits a metadata value to be a plain string, a localized
// {"@value","@language"} object, or an array of either. Both pilot
// institutions (Gallica, Digital Bodleian) emit plain strings here, so only
// that form is handled today; non-string values are skipped rather than
// guessed at. Other value shapes are a future test-first cycle.
func ExtractV2Metadata(manifest []byte) ([]MetadataEntry, error) {
	var m struct {
		Metadata []struct {
			Label string          `json:"label"`
			Value json.RawMessage `json:"value"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return nil, fmt.Errorf("metadata: decoding IIIF v2 manifest: %w", err)
	}

	entries := make([]MetadataEntry, 0, len(m.Metadata))
	for _, e := range m.Metadata {
		var s string
		if err := json.Unmarshal(e.Value, &s); err != nil {
			// Non-string value (localized object/array): not yet supported.
			continue
		}
		entries = append(entries, MetadataEntry{Label: e.Label, Value: s})
	}
	return entries, nil
}
