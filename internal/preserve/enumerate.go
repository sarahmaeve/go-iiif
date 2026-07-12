// Package preserve turns a matched IIIF manifest into a preservation copy:
// enumerate its canvas images, fetch the largest JPEG via the Image API, and
// store them with the manifest and local level-0 deep-zoom pyramids.
package preserve

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ImageResource is one canvas image to preserve.
type ImageResource struct {
	// ServiceID is the IIIF Image API base URL, or a static image URL when
	// the institution does not implement the Image API.
	ServiceID string
	CanvasID  string
	Width     int
	Height    int
	Label     string
}

// idHolder decodes both Presentation 2.x (@id) and 3.0 (id).
type idHolder struct {
	IDV2 string `json:"@id"`
	IDV3 string `json:"id"`
}

func (h idHolder) id() string {
	if h.IDV3 != "" {
		return h.IDV3
	}
	return h.IDV2
}

type v2Manifest struct {
	Sequences []struct {
		Canvases []struct {
			idHolder
			Label  json.RawMessage `json:"label"`
			Images []struct {
				Resource imageResource `json:"resource"`
			} `json:"images"`
		} `json:"canvases"`
	} `json:"sequences"`
}

type v3Manifest struct {
	Items []struct { // canvases
		idHolder
		Items []struct { // annotation pages
			Items []struct { // annotations
				Body imageResource `json:"body"`
			} `json:"items"`
		} `json:"items"`
	} `json:"items"`
}

type imageResource struct {
	idHolder
	Width   int             `json:"width"`
	Height  int             `json:"height"`
	Service json.RawMessage `json:"service"`
}

// serviceID resolves the Image API base for a resource: its service @id/id
// (service may be an object or an array), else the resource id with a
// trailing IIIF image request stripped, else the raw id (static image).
func (r imageResource) serviceID() string {
	if len(r.Service) > 0 {
		var one idHolder
		if err := json.Unmarshal(r.Service, &one); err == nil && one.id() != "" {
			return one.id()
		}
		var many []idHolder
		if err := json.Unmarshal(r.Service, &many); err == nil && len(many) > 0 {
			return many[0].id()
		}
	}
	id := r.id()
	for _, suffix := range []string{"/full/full/0/default.jpg", "/full/max/0/default.jpg"} {
		if base, ok := strings.CutSuffix(id, suffix); ok {
			return base
		}
	}
	return id
}

// EnumerateImages extracts the per-canvas images to preserve from a IIIF
// Presentation manifest.
func EnumerateImages(manifest []byte) ([]ImageResource, error) {
	var out []ImageResource

	// Presentation 2.x: sequences → canvases → images → resource.
	var v2 v2Manifest
	if err := json.Unmarshal(manifest, &v2); err != nil {
		return nil, fmt.Errorf("preserve: decoding manifest: %w", err)
	}
	for _, seq := range v2.Sequences {
		for _, canvas := range seq.Canvases {
			for _, img := range canvas.Images {
				out = append(out, ImageResource{
					ServiceID: img.Resource.serviceID(),
					CanvasID:  canvas.id(),
					Width:     img.Resource.Width,
					Height:    img.Resource.Height,
					Label:     presentationLabel(canvas.Label),
				})
			}
		}
	}
	if len(out) > 0 {
		return out, nil
	}

	// Presentation 3.0: items (canvases) → items (pages) → items
	// (annotations) → body.
	var v3 v3Manifest
	if err := json.Unmarshal(manifest, &v3); err != nil {
		return nil, fmt.Errorf("preserve: decoding v3 manifest: %w", err)
	}
	for _, canvas := range v3.Items {
		for _, page := range canvas.Items {
			for _, anno := range page.Items {
				out = append(out, ImageResource{
					ServiceID: anno.Body.serviceID(),
					CanvasID:  canvas.id(),
					Width:     anno.Body.Width,
					Height:    anno.Body.Height,
				})
			}
		}
	}
	return out, nil
}

// presentationLabel tolerates the label shapes found across Presentation 2
// deployments: a plain string, an array of localized @value objects, or a
// Presentation 3-style language map. Labels are display-only here; a novel
// shape must never prevent preservation of an otherwise valid canvas image.
func presentationLabel(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return plain
	}
	var localized []struct {
		Value string `json:"@value"`
	}
	if json.Unmarshal(raw, &localized) == nil {
		for _, value := range localized {
			if value.Value != "" {
				return value.Value
			}
		}
	}
	var languageMap map[string][]string
	if json.Unmarshal(raw, &languageMap) == nil {
		for _, language := range []string{"en", "none"} {
			if values := languageMap[language]; len(values) > 0 {
				return values[0]
			}
		}
		for _, values := range languageMap {
			if len(values) > 0 {
				return values[0]
			}
		}
	}
	return ""
}
