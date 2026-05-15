package metadata

import (
	"errors"
	"testing"
)

func TestParseLanguage(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "English name", in: "French", want: "fr"},
		{name: "endonym with cedilla", in: "français", want: "fr"},
		{name: "endonym without cedilla", in: "francais", want: "fr"},
		{name: "ISO 639-2/B code", in: "fre", want: "fr"},
		{name: "ISO 639-2/T code", in: "fra", want: "fr"},
		{name: "already ISO 639-1", in: "fr", want: "fr"},
		{name: "case and space insensitive", in: "  FRENCH  ", want: "fr"},
		{name: "empty string is an error", in: "", wantErr: true},
		{name: "unknown language is an error", in: "Klingon", wantErr: true},
	}

	tests = append(tests, []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "Latin name", in: "Latin", want: "la"},
		{name: "Latin 639-2", in: "lat", want: "la"},
		{name: "English endonym", in: "English", want: "en"},
		{name: "English 639-2", in: "eng", want: "en"},
		{name: "German English name", in: "German", want: "de"},
		{name: "German endonym", in: "Deutsch", want: "de"},
		{name: "German 639-2/B", in: "ger", want: "de"},
		{name: "German 639-2/T", in: "deu", want: "de"},
		{name: "Italian endonym", in: "italiano", want: "it"},
		{name: "Spanish endonym with tilde", in: "español", want: "es"},
		{name: "Spanish endonym without tilde", in: "espanol", want: "es"},
		{name: "Greek 639-2/B", in: "gre", want: "el"},
		{name: "Greek 639-2/T", in: "ell", want: "el"},
		{name: "Dutch endonym", in: "Nederlands", want: "nl"},
		{name: "Dutch 639-2/B", in: "dut", want: "nl"},
	}...)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLanguage(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseLanguage(%q) = %q, want error", tt.in, got)
				}
				if !errors.Is(err, ErrUnknownLanguage) {
					t.Fatalf("ParseLanguage(%q) error = %v, want ErrUnknownLanguage", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLanguage(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseLanguage(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
