package metadata

import "testing"

func TestParseDateRange(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    DateRange
		wantErr bool
	}{
		{name: "plain four-digit year", in: "1450", want: DateRange{Start: 1450, End: 1450}},
		{name: "plain year with surrounding space", in: "  1450 ", want: DateRange{Start: 1450, End: 1450}},
		{name: "hyphen year range", in: "1450-1480", want: DateRange{Start: 1450, End: 1480}},
		{name: "en-dash year range", in: "1450–1480", want: DateRange{Start: 1450, End: 1480}},
		{name: "spaced year range", in: " 1450 - 1480 ", want: DateRange{Start: 1450, End: 1480}},
		{name: "reversed range is normalized", in: "1480-1450", want: DateRange{Start: 1450, End: 1480}},
		{name: "15th century", in: "15th century", want: DateRange{Start: 1401, End: 1500}},
		{name: "1st century", in: "1st century", want: DateRange{Start: 1, End: 100}},
		{name: "century case and spacing insensitive", in: "  21ST  Century ", want: DateRange{Start: 2001, End: 2100}},
		{name: "circa widens by 20 years each side", in: "circa 1480", want: DateRange{Start: 1460, End: 1500}},
		{name: "c. abbreviation", in: "c. 1480", want: DateRange{Start: 1460, End: 1500}},
		{name: "ca. abbreviation", in: "ca. 1480", want: DateRange{Start: 1460, End: 1500}},
		{name: "circa case and spacing insensitive", in: "  CIRCA  1480 ", want: DateRange{Start: 1460, End: 1500}},
		{name: "French Roman century XVe siècle", in: "XVe siècle", want: DateRange{Start: 1401, End: 1500}},
		{name: "French Roman century no accent", in: "XVe siecle", want: DateRange{Start: 1401, End: 1500}},
		{name: "French Roman century no ordinal suffix", in: "XV siècle", want: DateRange{Start: 1401, End: 1500}},
		{name: "French Roman century XXIe", in: "XXIe siècle", want: DateRange{Start: 2001, End: 2100}},
		{name: "Spanish s. XV", in: "s. XV", want: DateRange{Start: 1401, End: 1500}},
		{name: "Spanish siglo XV", in: "siglo XV", want: DateRange{Start: 1401, End: 1500}},
		{name: "Roman century case and spacing insensitive", in: "  xiv  SIÈCLE ", want: DateRange{Start: 1301, End: 1400}},
		{name: "empty string is an error", in: "", wantErr: true},
		{name: "non-date free text is an error", in: "a fine manuscript", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDateRange(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDateRange(%q) = %+v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDateRange(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseDateRange(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}
