package metadata

import (
	"reflect"
	"testing"
)

func TestBuildWorkRecord(t *testing.T) {
	mapping := FieldMapping{
		"language": FieldLanguage,
		"langue":   FieldLanguage,
		"date":     FieldDate,
		"origin":   FieldOrigin,
	}

	tests := []struct {
		name string
		meta []MetadataEntry
		want WorkRecord
	}{
		{
			name: "language and date are parsed via the mapping",
			meta: []MetadataEntry{
				{Label: "Language", Value: "French"},
				{Label: "Date", Value: "1450"},
			},
			want: WorkRecord{
				Langs:     []string{"fr"},
				DateRange: DateRange{Start: 1450, End: 1450},
			},
		},
		{
			name: "unmapped labels are ignored",
			meta: []MetadataEntry{
				{Label: "Shelfmark", Value: "MS 1234"},
				{Label: "Langue", Value: "français"},
			},
			want: WorkRecord{Langs: []string{"fr"}},
		},
		{
			name: "origin passes through trimmed and raw",
			meta: []MetadataEntry{
				{Label: "Origin", Value: "  Paris  "},
			},
			want: WorkRecord{Origin: "Paris"},
		},
		{
			name: "multiple distinct languages are collected in order",
			meta: []MetadataEntry{
				{Label: "Language", Value: "French"},
				{Label: "Language", Value: "Latin"},
			},
			want: WorkRecord{Langs: []string{"fr", "la"}},
		},
		{
			name: "languages that normalize to the same code are deduped",
			meta: []MetadataEntry{
				{Label: "Language", Value: "French"},
				{Label: "Langue", Value: "français"},
			},
			want: WorkRecord{Langs: []string{"fr"}},
		},
		{
			name: "unparseable language is skipped, parseable kept",
			meta: []MetadataEntry{
				{Label: "Language", Value: "Klingon"},
				{Label: "Language", Value: "German"},
			},
			want: WorkRecord{Langs: []string{"de"}},
		},
		{
			name: "unparseable date leaves a zero DateRange",
			meta: []MetadataEntry{
				{Label: "Date", Value: "undated"},
				{Label: "Language", Value: "Latin"},
			},
			want: WorkRecord{Langs: []string{"la"}},
		},
		{
			name: "empty metadata yields a zero record",
			meta: nil,
			want: WorkRecord{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildWorkRecord(tt.meta, mapping)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("BuildWorkRecord() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
