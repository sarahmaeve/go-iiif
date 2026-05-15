package serve

import (
	"encoding/json"
	"fmt"
	"strings"
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
