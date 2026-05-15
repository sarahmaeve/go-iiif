package preserve

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/source"
)

// Summary reports the outcome of preserving one manifest.
type Summary struct {
	ManifestURL string
	Dir         string // BlobStore key prefix the bundle was written under
	Images      int    // canvas images found
	Stored      int    // images newly fetched and stored
	Skipped     int    // images already present (idempotent re-run)
	Failures    []string
}

type provenance struct {
	ManifestURL string          `json:"manifest_url"`
	License     string          `json:"license,omitempty"`
	Images      []provenanceImg `json:"images"`
}

type provenanceImg struct {
	File      string `json:"file"`
	ServiceID string `json:"service_id"`
	SourceURL string `json:"source_url"`
}

var unsafeKeyChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// dirFor derives a stable, filesystem-safe BlobStore prefix from a manifest
// URL.
func dirFor(manifestURL string) string {
	s := manifestURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	s = unsafeKeyChars.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

// Preserve fetches every canvas image of a matched manifest at the largest
// available size and stores the images, the manifest, and a provenance record
// under one BlobStore prefix. A single image failure is recorded but does not
// abort the manifest; already-stored images are skipped (idempotent).
func Preserve(ctx context.Context, fetcher source.Fetcher, store BlobStore, manifestURL string, manifestBytes []byte) (Summary, error) {
	images, err := EnumerateImages(manifestBytes)
	if err != nil {
		return Summary{ManifestURL: manifestURL}, err
	}

	dir := dirFor(manifestURL)
	sum := Summary{ManifestURL: manifestURL, Dir: dir, Images: len(images)}

	if err := store.Put(ctx, dir+"/manifest.json", manifestBytes); err != nil {
		return sum, fmt.Errorf("preserve: storing manifest: %w", err)
	}

	prov := provenance{
		ManifestURL: manifestURL,
		License:     extractLicense(manifestBytes),
	}
	for i, img := range images {
		file := fmt.Sprintf("%04d.jpg", i+1)
		key := dir + "/" + file

		exists, err := store.Exists(ctx, key)
		if err != nil {
			sum.Failures = append(sum.Failures, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		if exists {
			sum.Skipped++
			prov.Images = append(prov.Images, provenanceImg{File: file, ServiceID: img.ServiceID})
			continue
		}

		data, used, err := FetchImage(ctx, fetcher, img.ServiceID)
		if err != nil {
			sum.Failures = append(sum.Failures, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		if err := store.Put(ctx, key, data); err != nil {
			sum.Failures = append(sum.Failures, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		sum.Stored++
		prov.Images = append(prov.Images, provenanceImg{File: file, ServiceID: img.ServiceID, SourceURL: used})
	}

	provJSON, err := json.MarshalIndent(prov, "", "  ")
	if err != nil {
		return sum, fmt.Errorf("preserve: encoding provenance: %w", err)
	}
	if err := store.Put(ctx, dir+"/provenance.json", provJSON); err != nil {
		return sum, fmt.Errorf("preserve: storing provenance: %w", err)
	}
	return sum, nil
}

// extractLicense records (does not enforce) any license/rights string for
// provenance. Best-effort across v2/v3 shapes; empty if none found.
func extractLicense(manifestBytes []byte) string {
	var m struct {
		License     json.RawMessage `json:"license"`
		Rights      json.RawMessage `json:"rights"`
		Attribution json.RawMessage `json:"attribution"`
	}
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return ""
	}
	for _, raw := range []json.RawMessage{m.License, m.Rights, m.Attribution} {
		if s := firstString(raw); s != "" {
			return s
		}
	}
	return ""
}

// firstString pulls a plain string out of a value that may be a string, an
// array of strings, or absent.
func firstString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		return arr[0]
	}
	return ""
}
