// Package annotation is the offline store for user-generated W3C Web
// Annotations (notes, highlights, shapes, translations, bookmarks…) kept
// beside each preserved bundle as a IIIF Presentation 3 AnnotationPage.
// Pure stdlib; bodies/targets are passed through opaquely so any valid
// annotation Mirador can display round-trips faithfully.
package annotation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned by Update/Delete when no stored annotation has
// the given id (so a failed edit/delete is explicit, not a silent append
// or no-op).
var ErrNotFound = errors.New("annotation: not found")

// FileName is the per-bundle annotation store, a sibling of manifest.json.
const FileName = "annotations.json"

const defaultContext = "http://www.w3.org/ns/anno.jsonld"

// Annotation is one W3C Web Annotation. The store neither constrains nor
// reinterprets what a client wrote: ID/Type/Motivation/Body/Target are the
// fields the store itself indexes on, and Extra carries every *other*
// top-level field verbatim. A client like MAE adds creator, creationDate,
// maeData (its editable drawing state) and a per-annotation @context;
// dropping any of them would stop MAE matching/editing a reloaded
// annotation, so the round-trip is byte-faithful (Body/Target/Extra are
// raw JSON, emitted unescaped).
type Annotation struct {
	ID         string
	Type       string
	Motivation any
	Body       json.RawMessage
	Target     json.RawMessage
	Extra      map[string]json.RawMessage // any non-core top-level fields, passed through unchanged
}

// coreField reports whether key is one the struct models explicitly (so it
// must not also live in Extra, or it would be written twice).
func coreField(key string) bool {
	switch key {
	case "id", "type", "motivation", "body", "target":
		return true
	}
	return false
}

// UnmarshalJSON keeps every non-core top-level field in Extra so nothing a
// client wrote is lost on the storage round-trip.
func (a *Annotation) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*a = Annotation{}
	if v, ok := m["id"]; ok {
		_ = json.Unmarshal(v, &a.ID)
	}
	if v, ok := m["type"]; ok {
		_ = json.Unmarshal(v, &a.Type)
	}
	if v, ok := m["motivation"]; ok {
		_ = json.Unmarshal(v, &a.Motivation)
	}
	a.Body = m["body"]
	a.Target = m["target"]
	for k, v := range m {
		if !coreField(k) {
			if a.Extra == nil {
				a.Extra = make(map[string]json.RawMessage)
			}
			a.Extra[k] = v
		}
	}
	return nil
}

// MarshalJSON re-emits the core fields plus the verbatim Extra fields.
// Body/Target/Extra are json.RawMessage so they pass through byte-for-byte
// (no re-escaping of e.g. SvgSelector markup).
func (a Annotation) MarshalJSON() ([]byte, error) {
	m := make(map[string]json.RawMessage, len(a.Extra)+5)
	for k, v := range a.Extra {
		if !coreField(k) {
			m[k] = v
		}
	}
	idb, _ := json.Marshal(a.ID)
	m["id"] = idb
	if a.Type != "" {
		tb, _ := json.Marshal(a.Type)
		m["type"] = tb
	}
	if a.Motivation != nil {
		mb, err := json.Marshal(a.Motivation)
		if err != nil {
			return nil, fmt.Errorf("annotation: encoding motivation: %w", err)
		}
		m["motivation"] = mb
	}
	if len(a.Body) > 0 {
		m["body"] = a.Body
	}
	if len(a.Target) > 0 {
		m["target"] = a.Target
	}
	return json.Marshal(m)
}

// Page is a IIIF Presentation 3 / W3C AnnotationPage.
type Page struct {
	Context string       `json:"@context"`
	ID      string       `json:"id,omitempty"`
	Type    string       `json:"type"`
	Items   []Annotation `json:"items"`
}

// CanvasID is the target Canvas id with any media-fragment stripped, so
// annotations group by the canvas they sit on regardless of selector.
func (a Annotation) CanvasID() string {
	var s string
	if json.Unmarshal(a.Target, &s) == nil {
		return strings.SplitN(s, "#", 2)[0]
	}
	var o struct {
		Source json.RawMessage `json:"source"`
		ID     json.RawMessage `json:"id"`
	}
	if json.Unmarshal(a.Target, &o) == nil {
		v := resolveRef(o.Source)
		if v == "" {
			v = resolveRef(o.ID)
		}
		return strings.SplitN(v, "#", 2)[0]
	}
	return ""
}

// resolveRef pulls a resource id out of a JSON value that may be a plain
// string ("https://…/canvas") or an object ({"id"|"@id":"https://…"}).
// MAE's SpecificResource sometimes nests the canvas as an object, which a
// string-only decode silently dropped (→ POST 400, save lost).
func resolveRef(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		ID   string `json:"id"`
		AtID string `json:"@id"`
	}
	if json.Unmarshal(raw, &o) == nil {
		if o.ID != "" {
			return o.ID
		}
		return o.AtID
	}
	return ""
}

// ByCanvas groups the page's annotations by target Canvas id.
func (p Page) ByCanvas() map[string][]Annotation {
	by := make(map[string][]Annotation)
	for _, a := range p.Items {
		if c := a.CanvasID(); c != "" {
			by[c] = append(by[c], a)
		}
	}
	return by
}

// Load reads the AnnotationPage in dir. An absent file is not an error —
// it yields a well-formed empty page (a manuscript simply has no notes yet).
func Load(dir string) (Page, error) {
	empty := Page{Context: defaultContext, Type: "AnnotationPage", Items: []Annotation{}}
	b, err := os.ReadFile(filepath.Join(dir, FileName)) //nolint:gosec // G304: fixed filename under a server-controlled bundle dir
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return empty, nil
		}
		return empty, fmt.Errorf("annotation: reading %s: %w", FileName, err)
	}
	var p Page
	if err := json.Unmarshal(b, &p); err != nil {
		return empty, fmt.Errorf("annotation: decoding %s: %w", FileName, err)
	}
	if p.Type == "" {
		p.Type = "AnnotationPage"
	}
	if p.Context == "" {
		p.Context = defaultContext
	}
	if p.Items == nil {
		p.Items = []Annotation{}
	}
	return p, nil
}

// Save writes the page to dir atomically (temp file + rename) so a crash
// mid-write never leaves a truncated, unparseable store.
func Save(dir string, p Page) error {
	if p.Context == "" {
		p.Context = defaultContext
	}
	if p.Type == "" {
		p.Type = "AnnotationPage"
	}
	if p.Items == nil {
		p.Items = []Annotation{}
	}
	out, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("annotation: encoding: %w", err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("annotation: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".annotations-*.json")
	if err != nil {
		return fmt.Errorf("annotation: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("annotation: writing: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("annotation: closing: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, FileName)); err != nil {
		return fmt.Errorf("annotation: finalizing: %w", err)
	}
	return nil
}

// Add appends a (already-id'd) annotation to dir's store and persists it.
func Add(dir string, a Annotation) error {
	p, err := Load(dir)
	if err != nil {
		return err
	}
	p.Items = append(p.Items, a)
	return Save(dir, p)
}

// Update replaces the stored annotation whose id equals a.ID, in place.
// ErrNotFound (and no write) if none matches — a failed edit is explicit.
func Update(dir string, a Annotation) error {
	p, err := Load(dir)
	if err != nil {
		return err
	}
	for i := range p.Items {
		if p.Items[i].ID == a.ID {
			p.Items[i] = a
			return Save(dir, p)
		}
	}
	return ErrNotFound
}

// Delete removes the stored annotation with the given id. ErrNotFound (and
// no write) if none matches.
func Delete(dir, id string) error {
	p, err := Load(dir)
	if err != nil {
		return err
	}
	kept := p.Items[:0:0]
	for _, a := range p.Items {
		if a.ID != id {
			kept = append(kept, a)
		}
	}
	if len(kept) == len(p.Items) {
		return ErrNotFound
	}
	p.Items = kept
	return Save(dir, p)
}
