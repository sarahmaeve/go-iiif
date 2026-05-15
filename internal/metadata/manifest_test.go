package metadata

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// These tests run the parsers against real IIIF Presentation manifests
// downloaded from the pilot institutions (DESIGN §5.1 — validate the
// normalizer against live Gallica/Bodleian data).

func TestExtractV2Metadata_RealManifests(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		mapping FieldMapping
		want    WorkRecord
	}{
		{
			name: "Gallica BnF Français 2814",
			file: "gallica_btv1b9059632h.json",
			// Gallica labels: "Date", "Language"; origin not exposed.
			mapping: FieldMapping{
				"date":     FieldDate,
				"language": FieldLanguage,
			},
			want: WorkRecord{
				Langs:     []string{"fr"},
				DateRange: DateRange{Start: 1301, End: 1400},
			},
		},
		{
			name: "Digital Bodleian C 4.8(1) Linc.",
			file: "bodleian_f317ad0c.json",
			// Bodleian labels differ: "Date Statement", "Place of Origin".
			mapping: FieldMapping{
				"date statement":  FieldDate,
				"language":        FieldLanguage,
				"place of origin": FieldOrigin,
			},
			want: WorkRecord{
				Langs:     []string{"la"},
				DateRange: DateRange{Start: 1506, End: 1506},
				Origin:    "Italy, Venice",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}
			entries, err := ExtractV2Metadata(data)
			if err != nil {
				t.Fatalf("ExtractV2Metadata: %v", err)
			}
			got := BuildWorkRecord(entries, tt.mapping)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("WorkRecord = %+v, want %+v", got, tt.want)
			}
		})
	}
}
