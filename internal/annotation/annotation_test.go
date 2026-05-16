package annotation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanvasID(t *testing.T) {
	cases := []struct {
		name, target, want string
	}{
		{"string target", `"https://ex/iiif/canvas/p1"`, "https://ex/iiif/canvas/p1"},
		{"string target with fragment", `"https://ex/iiif/canvas/p1#xywh=10,20,30,40"`, "https://ex/iiif/canvas/p1"},
		{"object source+selector", `{"source":"https://ex/iiif/canvas/p2","selector":{"type":"FragmentSelector","value":"xywh=0,0,5,5"}}`, "https://ex/iiif/canvas/p2"},
		{"object id", `{"id":"https://ex/iiif/canvas/p3#xywh=1,1,1,1","type":"Canvas"}`, "https://ex/iiif/canvas/p3"},
		{"empty", `""`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := Annotation{Target: []byte(c.target)}
			if got := a.CanvasID(); got != c.want {
				t.Fatalf("CanvasID(%s) = %q, want %q", c.target, got, c.want)
			}
		})
	}
}

func TestLocalStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Absent file → empty, well-formed page, no error.
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(absent): %v", err)
	}
	if p.Type != "AnnotationPage" || len(p.Items) != 0 {
		t.Fatalf("absent load = %+v, want empty AnnotationPage", p)
	}

	p.Items = append(p.Items,
		Annotation{
			ID: "urn:a:1", Type: "Annotation", Motivation: "commenting",
			Body:   []byte(`{"type":"TextualBody","value":"a marginal note","format":"text/plain"}`),
			Target: []byte(`"https://ex/iiif/canvas/p1#xywh=10,10,50,20"`),
		},
		Annotation{
			ID: "urn:a:2", Type: "Annotation", Motivation: "translating",
			Body:   []byte(`{"type":"TextualBody","value":"the lover","language":"en"}`),
			Target: []byte(`{"source":"https://ex/iiif/canvas/p2"}`),
		},
	)
	if err := Save(dir, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "annotations.json")); err != nil {
		t.Fatalf("annotations.json not written: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Type != "AnnotationPage" || got.Context == "" {
		t.Fatalf("loaded page malformed: %+v", got)
	}
	if len(got.Items) != 2 || got.Items[0].ID != "urn:a:1" || got.Items[1].CanvasID() != "https://ex/iiif/canvas/p2" {
		t.Fatalf("round-trip items wrong: %+v", got.Items)
	}
}

func TestByCanvas(t *testing.T) {
	p := Page{Items: []Annotation{
		{ID: "1", Target: []byte(`"c/p1#xywh=0,0,1,1"`)},
		{ID: "2", Target: []byte(`"c/p2"`)},
		{ID: "3", Target: []byte(`{"source":"c/p1"}`)},
	}}
	by := p.ByCanvas()
	if len(by["c/p1"]) != 2 || len(by["c/p2"]) != 1 {
		t.Fatalf("ByCanvas grouping = %v, want p1:2 p2:1", by)
	}
}
