package preserve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
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
	Repaired    int    // existing images whose missing tile pyramids were rebuilt
	Tiled       int    // images for which a level0 tile pyramid was built
	Failures    []string
}

// ErrIncomplete means one or more required page images could not be
// preserved. The successfully committed page files remain as restart state,
// but no final provenance completion marker is written.
var ErrIncomplete = errors.New("preserve: bundle incomplete")

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

// dotsOnly matches a segment that is nothing but dots ("." / ".." / ...).
// The unsafeKeyChars allow-set keeps "." (legitimate in slugs like "v1.2"),
// which would otherwise let a "../" host or path survive as a real
// traversal segment once joined under the store root.
var dotsOnly = regexp.MustCompile(`^\.+$`)

// safeSegment neutralizes a single path segment that would traverse out of
// the store root, while leaving ordinary dotted slugs untouched.
func safeSegment(s string) string {
	if dotsOnly.MatchString(s) {
		return "_"
	}
	return s
}

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
	host = safeSegment(strings.Trim(unsafeKeyChars.ReplaceAllString(host, "_"), "_"))
	slug := safeSegment(strings.Trim(unsafeKeyChars.ReplaceAllString(rest, "_"), "_"))
	switch {
	case host == "":
		return slug
	case slug == "":
		return host
	default:
		return host + "/" + slug
	}
}

// ProgressEvent reports the disposition of one image during Preserve, so a
// long throttled run (e.g. a multi-page Gallica manuscript) is observable.
type ProgressEvent struct {
	Index, Total int    // 1-based image index, total images in the manifest
	File         string // e.g. "0042.jpg"
	Action       string // "stored" | "skipped" | "repaired" | "failed"
}

type preserveConfig struct {
	progress    func(ProgressEvent)
	pageRetries int
}

// Option configures Preserve.
type Option func(*preserveConfig)

// WithProgress invokes fn once per image with its disposition.
func WithProgress(fn func(ProgressEvent)) Option {
	return func(c *preserveConfig) { c.progress = fn }
}

// WithPageRetries retries a page this many additional times after all of its
// image-service variants fail. Missing or corrupt pages are always attempted
// again on the next preservation run regardless of this same-run policy.
func WithPageRetries(n int) Option {
	return func(c *preserveConfig) {
		if n > 0 {
			c.pageRetries = n
		}
	}
}

// Preserve fetches every canvas image of a matched manifest at the largest
// available size and stores the images, the manifest, and a provenance record
// under one BlobStore prefix. Already-stored valid images are reused without
// an HTTP request, and missing tile pyramids are rebuilt from those local
// images. Provenance is written last and only for a complete bundle.
func Preserve(ctx context.Context, fetcher source.Fetcher, store BlobStore, manifestURL string, manifestBytes []byte, opts ...Option) (Summary, error) {
	var cfg preserveConfig
	for _, o := range opts {
		o(&cfg)
	}
	report := func(i, total int, file, action string) {
		if cfg.progress != nil {
			cfg.progress(ProgressEvent{Index: i, Total: total, File: file, Action: action})
		}
	}

	images, err := EnumerateImages(manifestBytes)
	if err != nil {
		return Summary{ManifestURL: manifestURL}, err
	}

	dir := dirFor(manifestURL)
	sum := Summary{ManifestURL: manifestURL, Dir: dir, Images: len(images)}
	if err := ctx.Err(); err != nil {
		return sum, err
	}
	provenanceKey := dir + "/provenance.json"
	previous, err := previousProvenance(ctx, store, provenanceKey)
	if err != nil {
		return sum, err
	}
	// provenance.json is the catalogue's completion marker. Invalidate it
	// before changing the bundle so a crash or failed refresh cannot expose a
	// partially updated manuscript as complete.
	if err := store.Delete(ctx, provenanceKey); err != nil {
		return sum, fmt.Errorf("preserve: invalidating completion marker: %w", err)
	}

	if err := store.Put(ctx, dir+"/manifest.json", manifestBytes); err != nil {
		return sum, fmt.Errorf("preserve: storing manifest: %w", err)
	}

	prov := provenance{
		ManifestURL: manifestURL,
		License:     extractLicense(manifestBytes),
	}
	// preferred remembers the size-variant that worked for this manifest so
	// later pages skip re-probing a known-dead one (dominant cost under a
	// strict per-host throttle, e.g. Gallica's 13s). -1 = unknown.
	preferred := -1
	for i, img := range images {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		file := fmt.Sprintf("%04d.jpg", i+1)
		key := dir + "/" + file

		exists, err := store.Exists(ctx, key)
		if err != nil {
			sum.Failures = append(sum.Failures, fmt.Sprintf("%s: %v", file, err))
			report(i+1, sum.Images, file, "failed")
			continue
		}
		if exists {
			data, readErr := store.Get(ctx, key)
			if readErr != nil {
				sum.Failures = append(sum.Failures, fmt.Sprintf("%s: %v", file, readErr))
				report(i+1, sum.Images, file, "failed")
				continue
			}
			if _, decodeErr := jpeg.DecodeConfig(bytes.NewReader(data)); decodeErr == nil {
				sum.Skipped++
				entry := provenanceImg{File: file, ServiceID: img.ServiceID}
				if old, ok := previous[file]; ok && old.ServiceID == img.ServiceID {
					entry.SourceURL = old.SourceURL
				}
				prefix := fmt.Sprintf("%04d", i+1)
				complete, completeErr := tilePyramidComplete(ctx, store, dir+"/"+prefix, data, defaultTileSize)
				if completeErr == nil && complete {
					sum.Tiled++
					entry.TileDir = prefix
					prov.Images = append(prov.Images, entry)
					report(i+1, sum.Images, file, "skipped")
					continue
				}
				if _, tileErr := renderTilePyramid(ctx, store, dir+"/"+prefix, data, defaultTileSize); tileErr == nil {
					sum.Tiled++
					sum.Repaired++
					entry.TileDir = prefix
					prov.Images = append(prov.Images, entry)
					report(i+1, sum.Images, file, "repaired")
					continue
				} else if err := ctx.Err(); err != nil {
					return sum, err
				}
				// Tiling remains best-effort. The valid flat JPEG is a complete
				// preservation artifact even when a pyramid cannot be produced.
				prov.Images = append(prov.Images, entry)
				report(i+1, sum.Images, file, "skipped")
				continue
			}
			// A corrupt local JPEG is not a checkpoint. Fetch just this page
			// again and atomically replace it below.
		}

		var data []byte
		var used string
		var variant int
		for attempt := 0; attempt <= cfg.pageRetries; attempt++ {
			data, used, variant, err = FetchImage(ctx, fetcher, img.ServiceID, preferred)
			if err == nil || ctx.Err() != nil {
				break
			}
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return sum, ctxErr
			}
			sum.Failures = append(sum.Failures, fmt.Sprintf("%s: %v", file, err))
			report(i+1, sum.Images, file, "failed")
			continue
		}
		if _, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil {
			sum.Failures = append(sum.Failures, fmt.Sprintf("%s: downloaded image is not a readable JPEG: %v", file, err))
			report(i+1, sum.Images, file, "failed")
			continue
		}
		preferred = variant // memoize for the rest of this manifest
		if err := store.Put(ctx, key, data); err != nil {
			sum.Failures = append(sum.Failures, fmt.Sprintf("%s: %v", file, err))
			report(i+1, sum.Images, file, "failed")
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
		} else if err := ctx.Err(); err != nil {
			return sum, err
		}
		prov.Images = append(prov.Images, entry)
		report(i+1, sum.Images, file, "stored")
	}

	if len(sum.Failures) > 0 || len(prov.Images) != len(images) {
		return sum, fmt.Errorf("%w: %d of %d required image(s) failed", ErrIncomplete, len(sum.Failures), len(images))
	}

	provJSON, err := json.MarshalIndent(prov, "", "  ")
	if err != nil {
		return sum, fmt.Errorf("preserve: encoding provenance: %w", err)
	}
	if err := store.Put(ctx, provenanceKey, provJSON); err != nil {
		return sum, fmt.Errorf("preserve: storing provenance: %w", err)
	}
	return sum, nil
}

func previousProvenance(ctx context.Context, store BlobStore, key string) (map[string]provenanceImg, error) {
	out := make(map[string]provenanceImg)
	exists, err := store.Exists(ctx, key)
	if err != nil || !exists {
		return out, err
	}
	b, err := store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("preserve: reading existing provenance: %w", err)
	}
	return decodePreviousProvenance(b), nil
}

func decodePreviousProvenance(b []byte) map[string]provenanceImg {
	out := make(map[string]provenanceImg)
	var old provenance
	if json.Unmarshal(b, &old) != nil {
		return out // corrupt completion marker is replaced if recovery succeeds
	}
	for _, img := range old.Images {
		out[img.File] = img
	}
	return out
}

func tilePyramidComplete(ctx context.Context, store BlobStore, prefix string, jpegBytes []byte, tileSize int) (bool, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(jpegBytes))
	if err != nil {
		return false, fmt.Errorf("preserve: inspecting local JPEG for tile repair: %w", err)
	}
	plan := tilePlan(cfg.Width, cfg.Height, tileSize)
	wantInfo, err := infoJSON(plan, prefix)
	if err != nil {
		return false, err
	}
	infoExists, err := store.Exists(ctx, prefix+"/info.json")
	if err != nil || !infoExists {
		return false, err
	}
	gotInfo, err := store.Get(ctx, prefix+"/info.json")
	if err != nil || !bytes.Equal(bytes.TrimSpace(gotInfo), bytes.TrimSpace(wantInfo)) {
		return false, err
	}
	// renderTilePyramid writes info.json last, after every derived image.
	// Its matching atomic presence is therefore the O(1) pyramid commit
	// marker. Doctor performs the deliberately expensive all-tile audit.
	return true, nil
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
