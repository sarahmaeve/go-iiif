package metadata

import "testing"

func TestTitle(t *testing.T) {
	cases := []struct {
		name, manifest, want string
	}{
		{"v3 language-map label", `{"label":{"en":["The Gulf Stream"]}}`, "The Gulf Stream"},
		{"v2 plain string label", `{"label":"Bodleian Library MS. Add. A. 22"}`, "Bodleian Library MS. Add. A. 22"},
		{"v2 localized array label", `{"label":[{"@value":"Le Roman de la Rose","@language":"fr"}]}`, "Le Roman de la Rose"},
		{"no label", `{"@type":"sc:Manifest"}`, ""},
		{"not json", `not json`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Title([]byte(c.manifest)); got != c.want {
				t.Fatalf("Title(%s) = %q, want %q", c.manifest, got, c.want)
			}
		})
	}
}
