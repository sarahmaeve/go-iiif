package metadata

import (
	"encoding/json"
	"testing"
)

func TestNormalizeIIIFText(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		prefLangs []string
		want      string
	}{
		{"plain string (v2 simple)", `"français"`, []string{"en"}, "français"},
		{"v2 localized object", `{"@value":"French","@language":"en"}`, []string{"en"}, "French"},
		{
			"v2 localized array prefers en",
			`[{"@value":"français","@language":"fr"},{"@value":"French","@language":"en"}]`,
			[]string{"en"}, "French",
		},
		{
			"v2 localized array no en falls back to first",
			`[{"@value":"français","@language":"fr"}]`,
			[]string{"en"}, "français",
		},
		{
			"v3 language map prefers en",
			`{"en":["French"],"fr":["français"]}`,
			[]string{"en"}, "French",
		},
		{
			"v3 language map no en, single key used",
			`{"fr":["français"]}`,
			[]string{"en"}, "français",
		},
		{"v3 language map 'none' key", `{"none":["Untitled"]}`, []string{"en"}, "Untitled"},
		{"v3 plain string array", `["First","Second"]`, []string{"en"}, "First"},
		{
			"pref order honored (fr before de)",
			`{"de":["Deutsch"],"fr":["français"]}`,
			[]string{"en", "fr"}, "français",
		},
		{"number coerced", `1899`, []string{"en"}, "1899"},
		{"null is empty", `null`, []string{"en"}, ""},
		{"empty object is empty", `{}`, []string{"en"}, ""},
		{"empty array is empty", `[]`, []string{"en"}, ""},
		{"empty string is empty", `""`, []string{"en"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeIIIFText(json.RawMessage(c.raw), c.prefLangs)
			if got != c.want {
				t.Errorf("normalizeIIIFText(%s, %v) = %q, want %q", c.raw, c.prefLangs, got, c.want)
			}
		})
	}
}
