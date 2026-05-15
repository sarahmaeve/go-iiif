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
	Tiled       int    // images for which a level0 tile pyramid was built
	Failures    []string
}

// defaultTileSize is the level0 tile edge. 512 balances file count against
// zoom granularity and matches the common OpenSeadragon/Mirador default.
const defaultTileSize = 512

type provenance struct {
	ManifestURL string          `json:"manifest_url"`
	License     string          `json:"license,omitempty"`
	Images      []provenanceImg `json:"images"`
}

type provenanceImg struct {
	File      string `json:"file"`
	ServiceID string `json:"service_id"`
	SourceURL string `json:"source_url"`
	TileDir   string `json:"tile_dir,omitempty"` // level0 pyramid prefix, if built
}

var unsafeKeyChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// dirFor derives a stable, filesystem-safe BlobStore prefix from a manifest
// URL, nested by institution: "<host>/<slugified-path>". The host is its own
// path segment so a permanent library groups manifests under their source
// institution.
func dirFor(manifestURL string) string {
	s := manifestURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	host, rest, _ := strings.Cut(s, "/")
	host = strings.Trim(unsafeKeyChars.ReplaceAllString(host, "_"), "_")
	slug := strings.Trim(unsafeKeyChars.ReplaceAllString(rest, "_"), "_")
	switch {
	case host == "":
		return slug
	case slug == "":
		return host
	default:
		return host + "/" + slug
	}
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
			entry := provenanceImg{File: file, ServiceID: img.ServiceID}
			prefix := fmt.Sprintf("%04d", i+1)
			if ok, _ := store.Exists(ctx, dir+"/"+prefix+"/info.json"); ok {
				sum.Tiled++
				entry.TileDir = prefix
			}
			prov.Images = append(prov.Images, entry)
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
		entry := provenanceImg{File: file, ServiceID: img.ServiceID, SourceURL: used}
		// Best-effort level0 pyramid for deep zoom. An undecodable image
		// (or render error) leaves only the flat jpg — the serve-time
		// rewrite falls back gracefully.
		prefix := fmt.Sprintf("%04d", i+1)
		if _, terr := renderTilePyramid(ctx, store, dir+"/"+prefix, data, defaultTileSize); terr == nil {
			sum.Tiled++
			entry.TileDir = prefix
		}
		prov.Images = append(prov.Images, entry)
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
