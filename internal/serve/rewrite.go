package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
	// w, h are the locally stored pixel dimensions read from the level0
	// info.json. They differ from the manifest's declared size when the
	// source server downscaled on the way in (e.g. Bodleian caps
	// /full/max/ at 4000px on the long edge). Zero when unknown.
	w, h int
}

// localInfoDims reads the width/height a level0 pyramid was actually built
// at from <bundleDir>/<tileDir>/info.json. Returns (0,0) on any problem —
// dimension correction is best-effort and must never break serving.
func localInfoDims(bundleDir, tileDir string) (w, h int) {
	if bundleDir == "" || tileDir == "" {
		return 0, 0
	}
	// tileDir comes from the bundle's own provenance.json; still, confine it
	// to a single in-bundle path segment so a crafted value cannot escape
	// bundleDir via ".." or an absolute path.
	if tileDir != filepath.Base(tileDir) || tileDir == ".." || filepath.IsAbs(tileDir) {
		return 0, 0
	}
	//nolint:gosec // G304: tileDir is constrained above to a single
	// non-traversing segment under the trusted bundle directory.
	raw, err := os.ReadFile(filepath.Join(bundleDir, tileDir, "info.json"))
	if err != nil {
		return 0, 0
	}
	var d struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return 0, 0
	}
	return d.Width, d.Height
}

// rewriteManifest returns the manifest with every preserved image's resource
// pointed at the local copy under base (e.g. "https://host/<dir>"), the IIIF
// Image API `service` removed (there is no local Image API — preserved images
// are flat JPEGs), and `format` set to image/jpeg. It is provenance-driven
// and structure-agnostic: it matches image nodes by the recorded original
// URLs, so it works for Presentation 2.x and 3.0 without knowing either
// shape. Non-image JSON is preserved (key order is not — viewers don't care).
func rewriteManifest(manifest, provenance []byte, base, bundleDir string) ([]byte, error) {
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
			t.w, t.h = localInfoDims(bundleDir, img.TileDir)
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
	rewriteNode(doc, nil, local)
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

// annotationRefURL is the per-canvas annotation endpoint Mirador will
// fetch. fmt selects the shape Mirador's loader expects for that manifest
// version: "w3c" (AnnotationPage) for v3, "oa" (sc:AnnotationList) for v2.
func annotationRefURL(base, canvasID, fmtq string) string {
	return fmt.Sprintf("%s/annotations?canvas=%s&fmt=%s",
		strings.TrimRight(base, "/"), url.QueryEscape(canvasID), fmtq)
}

// injectAnnotations adds, to each Canvas that has stored annotations, a
// *reference* to this server's per-canvas annotation endpoint — because
// Mirador loads annotations by fetching the AnnotationPage/AnnotationList
// by id/@id (it ignores inline items/resources). v3 → canvas.annotations
// (AnnotationPage), v2 → canvas.otherContent (sc:AnnotationList). The
// endpoint is on this same loopback server, so it stays fully offline.
// Existing lists are appended to, not clobbered. Returns nil (no change)
// on any problem — annotations must never break serving. Canvas ids match
// the original id, which the image rewrite leaves untouched.
func injectAnnotations(manifestJSON []byte, page annotation.Page, base string) []byte {
	by := page.ByCanvas()
	if len(by) == 0 {
		return nil
	}
	var doc any
	if json.Unmarshal(manifestJSON, &doc) != nil {
		return nil
	}

	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if isCanvas(v) {
				if _, id := nodeID(v); id != "" && len(by[id]) > 0 {
					if t, _ := v["type"].(string); t == "Canvas" {
						v["annotations"] = appendArr(v["annotations"], map[string]any{
							"id":   annotationRefURL(base, id, "w3c"),
							"type": "AnnotationPage",
						})
					} else {
						v["otherContent"] = appendArr(v["otherContent"], map[string]any{
							"@id":   annotationRefURL(base, id, "oa"),
							"@type": "sc:AnnotationList",
						})
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

	// Encode without HTML escaping so the injected annotation URL keeps a
	// literal "&" (Go would otherwise emit & — valid JSON Mirador
	// decodes fine, but the manifest stays human-readable this way).
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil
	}
	return buf.Bytes()
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

// setDims overwrites width/height on a node when dims are known (>0),
// keeping the manifest's declared size in step with the locally stored
// pixels. Without this, a level0 deep-zoom source is asked for tiles at
// coordinates that were never generated and only a sub-region renders.
func setDims(m map[string]any, w, h int) {
	if m == nil || w <= 0 || h <= 0 {
		return
	}
	m["width"] = w
	m["height"] = h
}

// rewriteNode walks the decoded manifest, tracking the nearest enclosing
// Canvas. An object is treated as a preserved image resource when its own
// id, or its service's id, begins with a recorded original URL; that object
// is then localized in place. When it is re-pointed at a local level0
// pyramid, the resource's and the enclosing Canvas's width/height are
// corrected to the locally stored pixel size.
func rewriteNode(n any, canvas map[string]any, local map[string]localTarget) {
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
				// Correct the manifest's declared size to the pixels we
				// actually stored, on both the image resource and its
				// Canvas, so the level0 tile grid lines up.
				setDims(v, t.w, t.h)
				setDims(canvas, t.w, t.h)
			} else {
				delete(v, "service") // no local pyramid: flat JPEG only
			}
			return // localized; don't descend further into it
		}
		if isCanvas(v) {
			canvas = v
		}
		for _, child := range v {
			rewriteNode(child, canvas, local)
		}
	case []any:
		for _, e := range v {
			rewriteNode(e, canvas, local)
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
