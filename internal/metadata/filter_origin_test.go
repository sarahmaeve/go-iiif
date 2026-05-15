package metadata

import "testing"

func TestFilterClassify_Origin(t *testing.T) {
	tests := []struct {
		name   string
		filter Filter
		rec    WorkRecord
		want   Classification
	}{
		{
			name:   "wanted place is a substring of the record origin",
			filter: Filter{Places: []string{"Venice"}},
			rec:    WorkRecord{Origin: "Italy, Venice"},
			want:   Match,
		},
		{
			name:   "any-of: one wanted place matches",
			filter: Filter{Places: []string{"France", "Venice"}},
			rec:    WorkRecord{Origin: "Italy, Venice"},
			want:   Match,
		},
		{
			name:   "case-insensitive match",
			filter: Filter{Places: []string{"venice"}},
			rec:    WorkRecord{Origin: "Italy, Venice"},
			want:   Match,
		},
		{
			name:   "origin present but no wanted place is NoMatch",
			filter: Filter{Places: []string{"Venice"}},
			rec:    WorkRecord{Origin: "Paris"},
			want:   NoMatch,
		},
		{
			name:   "missing origin is Uncertain",
			filter: Filter{Places: []string{"Venice"}},
			rec:    WorkRecord{Origin: "  "},
			want:   Uncertain,
		},
		{
			name:   "no place constraint does not exclude",
			filter: Filter{},
			rec:    WorkRecord{Origin: "Paris"},
			want:   Match,
		},
		{
			name:   "combined with language and date, all satisfied",
			filter: Filter{Languages: []string{"la"}, Date: &DateRange{Start: 1500, End: 1550}, Places: []string{"Venice"}},
			rec:    WorkRecord{Langs: []string{"la"}, DateRange: DateRange{Start: 1506, End: 1506}, Origin: "Italy, Venice"},
			want:   Match,
		},
		{
			name:   "combined: failing place dominates to NoMatch",
			filter: Filter{Languages: []string{"la"}, Places: []string{"Paris"}},
			rec:    WorkRecord{Langs: []string{"la"}, Origin: "Italy, Venice"},
			want:   NoMatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.Classify(tt.rec); got != tt.want {
				t.Fatalf("Classify() = %v, want %v", got, tt.want)
			}
		})
	}
}
