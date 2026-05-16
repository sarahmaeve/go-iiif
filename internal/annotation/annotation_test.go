package annotation

import (
	"encoding/json"
	"errors"
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

func TestStore_AddUpdateDelete(t *testing.T) {
	dir := t.TempDir()

	a1 := Annotation{ID: "urn:a:1", Type: "Annotation", Target: []byte(`"c/p1"`),
		Body: []byte(`{"type":"TextualBody","value":"first"}`)}
	a2 := Annotation{ID: "urn:a:2", Type: "Annotation", Target: []byte(`"c/p2"`),
		Body: []byte(`{"type":"TextualBody","value":"second"}`)}

	if err := Add(dir, a1); err != nil {
		t.Fatalf("Add a1: %v", err)
	}
	if err := Add(dir, a2); err != nil {
		t.Fatalf("Add a2: %v", err)
	}
	if p, _ := Load(dir); len(p.Items) != 2 {
		t.Fatalf("after 2 Add, items = %d, want 2", len(p.Items))
	}

	// Update replaces the item with the same id, in place.
	a1u := a1
	a1u.Body = []byte(`{"type":"TextualBody","value":"first edited"}`)
	if err := Update(dir, a1u); err != nil {
		t.Fatalf("Update: %v", err)
	}
	p, _ := Load(dir)
	if len(p.Items) != 2 || p.Items[0].ID != "urn:a:1" {
		t.Fatalf("Update changed structure: %+v", p.Items)
	}
	var bd struct{ Value string }
	if err := json.Unmarshal(p.Items[0].Body, &bd); err != nil || bd.Value != "first edited" {
		t.Fatalf("Update did not replace a1's body in place: value=%q err=%v", bd.Value, err)
	}

	// Update of an unknown id is ErrNotFound (not a silent append).
	if err := Update(dir, Annotation{ID: "urn:nope", Target: []byte(`"c/p9"`)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update unknown = %v, want ErrNotFound", err)
	}
	if p, _ := Load(dir); len(p.Items) != 2 {
		t.Fatalf("failed Update must not change the store (items=%d)", len(p.Items))
	}

	// Delete removes by id; deleting an absent id is ErrNotFound.
	if err := Delete(dir, "urn:a:1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	p, _ = Load(dir)
	if len(p.Items) != 1 || p.Items[0].ID != "urn:a:2" {
		t.Fatalf("Delete did not remove a1: %+v", p.Items)
	}
	if err := Delete(dir, "urn:a:1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete absent = %v, want ErrNotFound", err)
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
