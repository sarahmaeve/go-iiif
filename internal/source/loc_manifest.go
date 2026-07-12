package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// LOCManifestFetcher preserves the ordinary Fetcher contract but works around
// www.loc.gov's route-specific Cloudflare challenge for Presentation
// manifests. It first requests the named manifest normally. Only when that
// exact LOC route returns 403 does it make a second, documented item-API pull
// (?fo=json) and derive a small Presentation 3 manifest from the ordered page
// files and IIIF Image API links in that response.
type LOCManifestFetcher struct {
	inner Fetcher
}

func NewLOCManifestFetcher(inner Fetcher) *LOCManifestFetcher {
	return &LOCManifestFetcher{inner: inner}
}

func (f *LOCManifestFetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	body, err := f.inner.Fetch(ctx, rawURL)
	if err == nil {
		return body, nil
	}
	itemURL, ok := locItemAPIURL(rawURL)
	var statusErr *HTTPStatusError
	if !ok || !errors.As(err, &statusErr) || statusErr.Code != http.StatusForbidden {
		return nil, err
	}

	itemBody, itemErr := f.inner.Fetch(ctx, itemURL)
	if itemErr != nil {
		return nil, fmt.Errorf("source: LOC manifest blocked and item API fallback failed: %w", itemErr)
	}
	manifest, itemErr := locItemToManifest(rawURL, itemURL, itemBody)
	if itemErr != nil {
		return nil, fmt.Errorf("source: LOC item API fallback: %w", itemErr)
	}
	return manifest, nil
}

func locItemAPIURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Hostname(), "www.loc.gov") {
		return "", false
	}
	clean := path.Clean(u.Path)
	const suffix = "/manifest.json"
	if !strings.HasPrefix(clean, "/item/") || !strings.HasSuffix(clean, suffix) {
		return "", false
	}
	itemPath := strings.TrimSuffix(clean, suffix)
	if strings.Count(strings.TrimPrefix(itemPath, "/item/"), "/") != 0 || itemPath == "/item/" {
		return "", false
	}
	u.Path = itemPath + "/"
	u.RawPath = ""
	u.RawQuery = "fo=json"
	u.Fragment = ""
	return u.String(), true
}

type locItemResponse struct {
	Item struct {
		Title    string          `json:"title"`
		Date     json.RawMessage `json:"date"`
		Language json.RawMessage `json:"language"`
		Rights   json.RawMessage `json:"rights"`
	} `json:"item"`
	Resources []struct {
		Files [][]locFile `json:"files"`
	} `json:"resources"`
}

type locFile struct {
	URL      string `json:"url"`
	Info     string `json:"info"`
	MIMEType string `json:"mimetype"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type locManifest struct {
	Context  string              `json:"@context"`
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Label    map[string][]string `json:"label"`
	Metadata []locMetadata       `json:"metadata,omitempty"`
	SeeAlso  []locSeeAlso        `json:"seeAlso"`
	Items    []locCanvas         `json:"items"`
}

type locMetadata struct {
	Label map[string][]string `json:"label"`
	Value map[string][]string `json:"value"`
}

type locSeeAlso struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Format  string `json:"format"`
	Profile string `json:"profile,omitempty"`
}

type locCanvas struct {
	ID     string              `json:"id"`
	Type   string              `json:"type"`
	Label  map[string][]string `json:"label"`
	Width  int                 `json:"width"`
	Height int                 `json:"height"`
	Items  []locAnnotationPage `json:"items"`
}

type locAnnotationPage struct {
	ID    string          `json:"id"`
	Type  string          `json:"type"`
	Items []locAnnotation `json:"items"`
}

type locAnnotation struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Motivation string   `json:"motivation"`
	Body       locImage `json:"body"`
	Target     string   `json:"target"`
}

type locImage struct {
	ID      string       `json:"id"`
	Type    string       `json:"type"`
	Format  string       `json:"format"`
	Width   int          `json:"width"`
	Height  int          `json:"height"`
	Service []locService `json:"service,omitempty"`
}

type locService struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Profile string `json:"profile"`
}

func locItemToManifest(manifestURL, itemURL string, body []byte) ([]byte, error) {
	var item locItemResponse
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("decoding item JSON: %w", err)
	}
	title := strings.TrimSpace(item.Item.Title)
	if title == "" {
		title = "Library of Congress item"
	}
	m := locManifest{
		Context: "http://iiif.io/api/presentation/3/context.json",
		ID:      manifestURL,
		Type:    "Manifest",
		Label:   map[string][]string{"en": {title}},
		SeeAlso: []locSeeAlso{{
			ID: itemURL, Type: "Dataset", Format: "application/json",
			Profile: "https://www.loc.gov/apis/json-and-yaml/",
		}},
	}
	for _, field := range []struct {
		label string
		raw   json.RawMessage
	}{
		{"Date", item.Item.Date},
		{"Language", item.Item.Language},
		{"Rights", item.Item.Rights},
	} {
		if values := locTextValues(field.raw); len(values) > 0 {
			m.Metadata = append(m.Metadata, locMetadata{
				Label: map[string][]string{"en": {field.label}},
				Value: map[string][]string{"en": values},
			})
		}
	}

	pageNumber := 0
	for _, resource := range item.Resources {
		for _, files := range resource.Files {
			image, ok := locPageImage(files)
			if !ok {
				continue
			}
			pageNumber++
			canvasID := fmt.Sprintf("%s#canvas-%04d", manifestURL, pageNumber)
			pageID := fmt.Sprintf("%s#page-%04d", manifestURL, pageNumber)
			annotationID := fmt.Sprintf("%s#annotation-%04d", manifestURL, pageNumber)
			m.Items = append(m.Items, locCanvas{
				ID: canvasID, Type: "Canvas",
				Label: map[string][]string{"none": {fmt.Sprintf("Page %d", pageNumber)}},
				Width: image.Width, Height: image.Height,
				Items: []locAnnotationPage{{
					ID: pageID, Type: "AnnotationPage",
					Items: []locAnnotation{{
						ID: annotationID, Type: "Annotation", Motivation: "painting",
						Body: image, Target: canvasID,
					}},
				}},
			})
		}
	}
	if len(m.Items) == 0 {
		return nil, errors.New("item JSON contains no preservable image pages")
	}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding derived manifest: %w", err)
	}
	return append(manifest, '\n'), nil
}

func locPageImage(files []locFile) (locImage, bool) {
	var largestJPEG, infoFile locFile
	for _, file := range files {
		if file.Info != "" && locPixelArea(file) > locPixelArea(infoFile) {
			infoFile = file
		}
		if strings.EqualFold(file.MIMEType, "image/jpeg") && file.URL != "" &&
			locPixelArea(file) > locPixelArea(largestJPEG) {
			largestJPEG = file
		}
	}
	if largestJPEG.URL == "" {
		return locImage{}, false
	}
	width, height := largestJPEG.Width, largestJPEG.Height
	if infoFile.Width > 0 && infoFile.Height > 0 {
		width, height = infoFile.Width, infoFile.Height
	}
	image := locImage{
		ID: largestJPEG.URL, Type: "Image", Format: "image/jpeg",
		Width: width, Height: height,
	}
	if serviceID, ok := strings.CutSuffix(infoFile.Info, "/info.json"); ok {
		image.Service = []locService{{
			ID: serviceID, Type: "ImageService2", Profile: "level2",
		}}
	}
	return image, true
}

func locPixelArea(file locFile) int64 {
	if file.Width <= 0 || file.Height <= 0 {
		return 0
	}
	return int64(file.Width) * int64(file.Height)
}

func locTextValues(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil && strings.TrimSpace(one) != "" {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return nil
	}
	out := many[:0]
	for _, value := range many {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
