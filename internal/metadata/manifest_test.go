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

// A real IIIF Presentation v3 manifest has object-valued labels/values
// ({"en":[…]}). The strict v2 struct used to hard-fail the whole manifest
// here; tolerant extraction must read it like any other.
func TestExtractMetadata_V3ObjectLabels(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "cookbook_0032_manifest01.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	entries, err := ExtractMetadata(data)
	if err != nil {
		t.Fatalf("ExtractMetadata on a real v3 manifest: %v", err)
	}
	got := make(map[string]string, len(entries))
	for _, e := range entries {
		got[e.Label] = e.Value
	}
	if got["Artist"] != "Winslow Homer (1836–1910)" {
		t.Errorf("Artist = %q, want %q", got["Artist"], "Winslow Homer (1836–1910)")
	}
	if got["Date"] != "1899" {
		t.Errorf("Date = %q, want %q", got["Date"], "1899")
	}
}

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
