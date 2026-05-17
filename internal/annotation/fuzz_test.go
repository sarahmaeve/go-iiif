package annotation

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzCanvasID asserts CanvasID never panics on an arbitrary Target, is
// deterministic, and always returns the canvas id with the media fragment
// stripped (no '#' may survive — annotations must group by bare canvas).
func FuzzCanvasID(f *testing.F) {
	for _, s := range []string{
		`"https://example/canvas/1"`,
		`"https://example/canvas/1#xywh=0,0,10,10"`,
		`{"source":"https://example/c#frag","id":"x"}`,
		`{"source":{"id":"https://example/c"}}`,
		`{"id":{"@id":"https://example/c"}}`,
		`null`, `1`, `[]`, `"#leading"`, `{}`, `"a#b#c"`,
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, target []byte) {
		a := Annotation{Target: json.RawMessage(target)}
		got := a.CanvasID()
		if strings.Contains(got, "#") {
			t.Fatalf("CanvasID() = %q: media fragment not stripped", got)
		}
		if got2 := a.CanvasID(); got2 != got {
			t.Fatalf("CanvasID() not deterministic: %q vs %q", got, got2)
		}
	})
}

// FuzzResolveRef asserts the reference resolver never panics on arbitrary
// JSON, is deterministic, and returns a string (empty when no id present).
func FuzzResolveRef(f *testing.F) {
	for _, s := range []string{
		``, `null`, `"https://example/c"`, `{"id":"a"}`, `{"@id":"b"}`,
		`{"id":"","@id":"b"}`, `{"id":1}`, `[1,2]`, `{}`, `"123"`,
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		got := resolveRef(json.RawMessage(raw))
		if got2 := resolveRef(json.RawMessage(raw)); got2 != got {
			t.Fatalf("resolveRef not deterministic: %q vs %q", got, got2)
		}
	})
}
