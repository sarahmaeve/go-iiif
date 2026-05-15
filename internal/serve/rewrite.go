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
	} `json:"images"`
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
	// anchor (original image-server URL prefix) → local URL.
	local := make(map[string]string, len(prov.Images))
	for _, img := range prov.Images {
		localURL := strings.TrimRight(base, "/") + "/" + img.File
		if img.ServiceID != "" {
			local[img.ServiceID] = localURL
		}
		if img.SourceURL != "" {
			local[img.SourceURL] = localURL
		}
	}

	var doc any
	if err := json.Unmarshal(manifest, &doc); err != nil {
		return nil, fmt.Errorf("serve: decoding manifest: %w", err)
	}
	rewriteNode(doc, local)

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serve: encoding rewritten manifest: %w", err)
	}
	return out, nil
}

// matchLocal returns the local URL for an id/service string that begins with
// a recorded original anchor, or "" if none.
func matchLocal(s string, local map[string]string) string {
	for anchor, localURL := range local {
		if s == anchor || strings.HasPrefix(s, anchor) {
			return localURL
		}
	}
	return ""
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
func rewriteNode(n any, local map[string]string) {
	switch v := n.(type) {
	case map[string]any:
		idKey, idVal := nodeID(v)
		hit := ""
		if idVal != "" {
			hit = matchLocal(idVal, local)
		}
		if hit == "" {
			if a := serviceAnchor(v["service"]); a != "" {
				hit = matchLocal(a, local)
			}
		}
		if hit != "" && idKey != "" {
			v[idKey] = hit
			v["format"] = "image/jpeg"
			delete(v, "service")
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
