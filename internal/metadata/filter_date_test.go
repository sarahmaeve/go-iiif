package metadata

import "testing"

func TestFilterClassify_Date(t *testing.T) {
	want1450 := &DateRange{Start: 1400, End: 1500}

	tests := []struct {
		name   string
		filter Filter
		rec    WorkRecord
		want   Classification
	}{
		{
			name:   "record date overlaps the wanted range",
			filter: Filter{Date: want1450},
			rec:    WorkRecord{DateRange: DateRange{Start: 1450, End: 1450}},
			want:   Match,
		},
		{
			name:   "record date is confidently outside the range",
			filter: Filter{Date: want1450},
			rec:    WorkRecord{DateRange: DateRange{Start: 1600, End: 1620}},
			want:   NoMatch,
		},
		{
			name:   "record has no parsed date is kept (lenient)",
			filter: Filter{Date: want1450},
			rec:    WorkRecord{},
			want:   Match,
		},
		{
			name:   "no date constraint does not exclude",
			filter: Filter{},
			rec:    WorkRecord{DateRange: DateRange{Start: 1600, End: 1620}},
			want:   Match,
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

func TestFilterClassify_Combination(t *testing.T) {
	f := Filter{Languages: []string{"fr"}, Date: &DateRange{Start: 1400, End: 1500}}

	tests := []struct {
		name string
		rec  WorkRecord
		want Classification
	}{
		{
			name: "all criteria satisfied",
			rec:  WorkRecord{Langs: []string{"fr"}, DateRange: DateRange{Start: 1450, End: 1450}},
			want: Match,
		},
		{
			name: "one criterion confidently fails dominates to NoMatch",
			rec:  WorkRecord{Langs: []string{"fr"}, DateRange: DateRange{Start: 1700, End: 1700}},
			want: NoMatch,
		},
		{
			name: "satisfied criterion plus missing data is kept (lenient)",
			rec:  WorkRecord{Langs: []string{"fr"}},
			want: Match,
		},
		{
			name: "missing language but failing date still NoMatch",
			rec:  WorkRecord{DateRange: DateRange{Start: 1700, End: 1700}},
			want: NoMatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := f.Classify(tt.rec); got != tt.want {
				t.Fatalf("Classify() = %v, want %v", got, tt.want)
			}
		})
	}
}
