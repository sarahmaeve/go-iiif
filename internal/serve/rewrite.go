package serve

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/annotation"
)

// provenanceDoc mirrors the subset of internal/preserve's provenance.json the
// rewrite needs: which local file each original image came from.
type provenanceDoc struct {
	Images []struct {
		File      string `json:"file"`
		ServiceID string `json:"service_id"`
		SourceURL string `json:"source_url"`
		TileDir   string `json:"tile_dir,omitempty"`
	} `json:"images"`
}

// localTarget is where a preserved image is re-pointed: the flat JPEG URL,
// and (when a level0 pyramid was built) the local Image API service base
// that gives the viewer deep zoom.
type localTarget struct {
	imageURL   string
	serviceURL string // "" when no local pyramid → service is stripped
}

// rewriteManifest returns the manifest with every preserved image's resource
// pointed at the local copy under base (e.g. "https://host/<dir>"), the IIIF
// Image API `service` removed (there is no local Image API — preserved images
// are flat JPEGs), and `format` set to image/jpeg. It is provenance-driven
// and structure-agnostic: it matches image nodes by the recorded original
// URLs, so it works for Presentation 2.x and 3.0 without knowing either
// shape. Non-image JSON is preserved (key order is not — viewers don't care).
func rewriteManifest(manifest, provenance []byte, base string) ([]byte, error) {
	var prov provenanceDoc
	if err := json.Unmarshal(provenance, &prov); err != nil {
		return nil, fmt.Errorf("serve: decoding provenance: %w", err)
	}
	// anchor (original image-server URL prefix) → local target.
	local := make(map[string]localTarget, len(prov.Images))
	for _, img := range prov.Images {
		b := strings.TrimRight(base, "/")
		t := localTarget{imageURL: b + "/" + img.File}
		if img.TileDir != "" {
			t.serviceURL = b + "/" + img.TileDir
		}
		if img.ServiceID != "" {
			local[img.ServiceID] = t
		}
		if img.SourceURL != "" {
			local[img.SourceURL] = t
		}
	}

	var doc any
	if err := json.Unmarshal(manifest, &doc); err != nil {
		return nil, fmt.Errorf("serve: decoding manifest: %w", err)
	}
	rewriteNode(doc, local)
	stripRemoteThumbnails(doc, strings.TrimRight(base, "/"))

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serve: encoding rewritten manifest: %w", err)
	}
	return out, nil
}

// isCanvas reports whether a decoded node is a IIIF Canvas (v3 "Canvas" or
// v2 "sc:Canvas").
func isCanvas(m map[string]any) bool {
	if t, _ := m["type"].(string); t == "Canvas" {
		return true
	}
	t, _ := m["@type"].(string)
	return t == "sc:Canvas"
}

// appendArr appends item to existing if it is a JSON array, else starts a
// new one — so an existing annotations/otherContent list is never clobbered.
func appendArr(existing, item any) []any {
	if ex, ok := existing.([]any); ok {
		return append(ex, item)
	}
	return []any{item}
}

// toGeneric round-trips a stored annotation through JSON into the decoded
// document's any-graph so it embeds cleanly.
func toGeneric(a annotation.Annotation) (any, bool) {
	b, err := json.Marshal(a)
	if err != nil {
		return nil, false
	}
	var m any
	return m, json.Unmarshal(b, &m) == nil
}

// toOpenAnnotation converts a stored W3C annotation to the IIIF
// Presentation 2 Open Annotation shape Mirador reads from a v2 canvas's
// otherContent (sc:AnnotationList). FragmentSelector/string targets carry
// over via `on`; the text body becomes a dctypes:Text resource.
func toOpenAnnotation(a annotation.Annotation) map[string]any {
	on := ""
	if json.Unmarshal(a.Target, &on) != nil || on == "" {
		on = a.CanvasID()
	}
	mot := "oa:commenting"
	if s, ok := a.Motivation.(string); ok && s != "" {
		if strings.Contains(s, ":") {
			mot = s
		} else {
			mot = "oa:" + s
		}
	}
	oa := map[string]any{"@type": "oa:Annotation", "motivation": mot, "on": on}
	if a.ID != "" {
		oa["@id"] = a.ID
	}
	var bd struct {
		Value    string `json:"value"`
		Format   string `json:"format"`
		Language string `json:"language"`
	}
	if json.Unmarshal(a.Body, &bd) == nil && bd.Value != "" {
		res := map[string]any{"@type": "dctypes:Text", "chars": bd.Value}
		res["format"] = bd.Format
		if bd.Format == "" {
			res["format"] = "text/plain"
		}
		if bd.Language != "" {
			res["language"] = bd.Language
		}
		oa["resource"] = []any{res}
	}
	return oa
}

// injectAnnotations attaches the user's stored annotations to the Canvases
// they target, in the shape that manifest's Presentation version makes
// Mirador read: v3 → an embedded W3C AnnotationPage in canvas.annotations;
// v2 → an sc:AnnotationList of oa:Annotation in canvas.otherContent (the v3
// `annotations` key is ignored by Mirador for v2). Inline — no extra fetch,
// fully offline. Existing lists are appended to, not clobbered. Returns nil
// (no change) on any problem — annotations must never break serving. Canvas
// ids match the original id, which the image rewrite leaves untouched.
func injectAnnotations(manifestJSON []byte, page annotation.Page, base string) []byte {
	by := page.ByCanvas()
	if len(by) == 0 {
		return nil
	}
	var doc any
	if json.Unmarshal(manifestJSON, &doc) != nil {
		return nil
	}

	n := 0
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if isCanvas(v) {
				if _, id := nodeID(v); id != "" {
					if anns := by[id]; len(anns) > 0 {
						n++
						pid := fmt.Sprintf("%s/annotations/%d", strings.TrimRight(base, "/"), n)
						if t, _ := v["type"].(string); t == "Canvas" {
							items := make([]any, 0, len(anns))
							for _, a := range anns {
								if m, ok := toGeneric(a); ok {
									items = append(items, m)
								}
							}
							v["annotations"] = appendArr(v["annotations"], map[string]any{
								"id": pid, "type": "AnnotationPage", "items": items,
							})
						} else {
							res := make([]any, 0, len(anns))
							for _, a := range anns {
								res = append(res, toOpenAnnotation(a))
							}
							v["otherContent"] = appendArr(v["otherContent"], map[string]any{
								"@id": pid, "@type": "sc:AnnotationList", "resources": res,
							})
						}
					}
				}
			}
			for _, c := range v {
				walk(c)
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(doc)

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil
	}
	return out
}

// matchLocal returns the local target for an id/service string that begins
// with a recorded original anchor, and whether one matched.
func matchLocal(s string, local map[string]localTarget) (localTarget, bool) {
	for anchor, t := range local {
		if s == anchor || strings.HasPrefix(s, anchor) {
			return t, true
		}
	}
	return localTarget{}, false
}

func nodeID(m map[string]any) (key, val string) {
	if v, ok := m["@id"].(string); ok {
		return "@id", v
	}
	if v, ok := m["id"].(string); ok {
		return "id", v
	}
	return "", ""
}

// serviceAnchor extracts an id string from a node's `service` (object or
// array of objects), used to recognize an image resource by its service.
func serviceAnchor(svc any) string {
	switch s := svc.(type) {
	case map[string]any:
		_, v := nodeID(s)
		return v
	case []any:
		for _, e := range s {
			if em, ok := e.(map[string]any); ok {
				if _, v := nodeID(em); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// rewriteNode walks the decoded manifest. An object is treated as a preserved
// image resource when its own id, or its service's id, begins with a recorded
// original URL; that object is then localized in place.
func rewriteNode(n any, local map[string]localTarget) {
	switch v := n.(type) {
	case map[string]any:
		idKey, idVal := nodeID(v)
		var t localTarget
		var ok bool
		if idVal != "" {
			t, ok = matchLocal(idVal, local)
		}
		if !ok {
			if a := serviceAnchor(v["service"]); a != "" {
				t, ok = matchLocal(a, local)
			}
		}
		if ok && idKey != "" {
			v[idKey] = t.imageURL
			v["format"] = "image/jpeg"
			if t.serviceURL != "" {
				// Re-point at the local level0 pyramid for deep zoom.
				v["service"] = []any{map[string]any{
					"id":      t.serviceURL,
					"type":    "ImageService3",
					"profile": "level0",
				}}
			} else {
				delete(v, "service") // no local pyramid: flat JPEG only
			}
			return // localized; don't descend further into it
		}
		for _, child := range v {
			rewriteNode(child, local)
		}
	case []any:
		for _, e := range v {
			rewriteNode(e, local)
		}
	}
}

// thumbnailURL extracts a representative URL from a IIIF thumbnail value,
// which may be a string, an {@id|id} object, or an array of either.
func thumbnailURL(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		_, id := nodeID(t)
		return id
	case []any:
		for _, e := range t {
			if u := thumbnailURL(e); u != "" {
				return u
			}
		}
	}
	return ""
}

// stripRemoteThumbnails deletes every `thumbnail` whose target is not under
// base. Gallica (and others) point thumbnails at a different service than
// the preserved image, so they cannot be matched by provenance; leaving
// them would 404 offline. Dropping them is structure-agnostic and lets the
// viewer derive a thumbnail from the now-local image service. Any thumbnail
// already localized (under base) is kept.
func stripRemoteThumbnails(n any, base string) {
	switch v := n.(type) {
	case map[string]any:
		if tn, ok := v["thumbnail"]; ok {
			if u := thumbnailURL(tn); u == "" || !strings.HasPrefix(u, base) {
				delete(v, "thumbnail")
			}
		}
		for _, child := range v {
			stripRemoteThumbnails(child, base)
		}
	case []any:
		for _, e := range v {
			stripRemoteThumbnails(e, base)
		}
	}
}
