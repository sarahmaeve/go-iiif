package metadata

import "testing"

func TestFilterClassify_Language(t *testing.T) {
	tests := []struct {
		name   string
		filter Filter
		rec    WorkRecord
		want   Classification
	}{
		{
			name:   "record language matches the wanted language",
			filter: Filter{Languages: []string{"fr"}},
			rec:    WorkRecord{Langs: []string{"fr"}},
			want:   Match,
		},
		{
			name:   "any-of: one of several record languages matches",
			filter: Filter{Languages: []string{"fr"}},
			rec:    WorkRecord{Langs: []string{"la", "fr"}},
			want:   Match,
		},
		{
			name:   "record has a confidently different language",
			filter: Filter{Languages: []string{"fr"}},
			rec:    WorkRecord{Langs: []string{"la"}},
			want:   NoMatch,
		},
		{
			name:   "missing language metadata is kept (lenient: preserve when unsure)",
			filter: Filter{Languages: []string{"fr"}},
			rec:    WorkRecord{},
			want:   Match,
		},
		{
			name:   "no language constraint does not exclude",
			filter: Filter{},
			rec:    WorkRecord{Langs: []string{"la"}},
			want:   Match,
		},
		{
			name:   "Match is the zero value of Classification (unset never silently dropped)",
			filter: Filter{Languages: []string{"fr"}},
			rec:    WorkRecord{},
			want:   Classification(0),
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
