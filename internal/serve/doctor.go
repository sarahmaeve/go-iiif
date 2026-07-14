package serve

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/annotation"
	"github.com/sarahmaeve/go-iiif/internal/preserve"
)

// DoctorProblem is one actionable integrity finding in a local library.
type DoctorProblem struct {
	Severity string
	Path     string
	Message  string
}

// DoctorReport summarizes a read-only, exhaustive library integrity check.
type DoctorReport struct {
	Bundles         int
	Images          int
	TilePyramids    int
	LinkedResources int
	FilesChecked    int
	Problems        []DoctorProblem
	// ManifestReruns contains unique source manifest URLs whose linked
	// resources are missing or have recorded preservation failures.
	ManifestReruns []string
}

func (r DoctorReport) Healthy() bool {
	for _, p := range r.Problems {
		if p.Severity == "ERROR" {
			return false
		}
	}
	return true
}

func (r *DoctorReport) problem(severity, path, format string, args ...any) {
	r.Problems = append(r.Problems, DoctorProblem{
		Severity: severity,
		Path:     filepath.ToSlash(path),
		Message:  fmt.Sprintf(format, args...),
	})
}

func (r *DoctorReport) recommendManifestRerun(rawURL string) {
	if rawURL == "" {
		return
	}
	for _, existing := range r.ManifestReruns {
		if existing == rawURL {
			return
		}
	}
	r.ManifestReruns = append(r.ManifestReruns, rawURL)
}

// DiagnoseLibrary validates preserved source records and every file that a
// local Image API info.json promises to the viewer. It never writes or
// repairs: research data should not be changed by a diagnostic command.
func DiagnoseLibrary(root string) DoctorReport {
	var report DoctorReport
	fi, err := os.Stat(root)
	if err != nil {
		report.problem("ERROR", root, "cannot open library: %v", err)
		return report
	}
	if !fi.IsDir() {
		report.problem("ERROR", root, "library root is not a directory")
		return report
	}

	bundles, err := discoverBundlesChecked(root)
	if err != nil {
		report.problem("ERROR", root, "cannot scan library: %v", err)
		return report
	}
	report.Bundles = len(bundles)
	known := make(map[string]bool, len(bundles))
	for _, ref := range bundles {
		known[ref.slug] = true
		diagnoseBundle(&report, ref)
	}
	diagnoseCatalog(&report, root, known)
	sort.Slice(report.Problems, func(i, j int) bool {
		if report.Problems[i].Path == report.Problems[j].Path {
			return report.Problems[i].Message < report.Problems[j].Message
		}
		return report.Problems[i].Path < report.Problems[j].Path
	})
	sort.Strings(report.ManifestReruns)
	return report
}

func diagnoseBundle(report *DoctorReport, ref bundleRef) {
	manifestPath := filepath.Join(ref.absDir, "manifest.json")
	manifest, err := os.ReadFile(manifestPath) //nolint:gosec // discovered under configured library root
	presentationManifest := manifest
	if err != nil {
		report.problem("ERROR", ref.slug+"/manifest.json", "cannot read manifest: %v", err)
	} else {
		report.FilesChecked++
		if !json.Valid(manifest) {
			report.problem("ERROR", ref.slug+"/manifest.json", "invalid JSON")
		}
	}

	provPath := filepath.Join(ref.absDir, "provenance.json")
	provBytes, err := os.ReadFile(provPath) //nolint:gosec // fixed sibling under discovered bundle
	if err != nil {
		report.problem("ERROR", ref.slug+"/provenance.json", "cannot read provenance: %v", err)
		return
	}
	report.FilesChecked++
	var prov provenanceDoc
	if err := json.Unmarshal(provBytes, &prov); err != nil {
		report.problem("ERROR", ref.slug+"/provenance.json", "invalid provenance JSON: %v", err)
		return
	}
	if prov.ManifestFile != "" {
		if !safeManifestFile(prov.ManifestFile) {
			report.problem("ERROR", ref.slug+"/provenance.json", "unsafe manifest_file %q", prov.ManifestFile)
		} else {
			activePath := filepath.Join(ref.absDir, filepath.FromSlash(prov.ManifestFile))
			active, activeErr := os.ReadFile(activePath) //nolint:gosec // validated relative path under bundle
			if activeErr != nil {
				report.problem("ERROR", ref.slug+"/"+prov.ManifestFile, "cannot read active manifest: %v", activeErr)
			} else {
				presentationManifest = active
				report.FilesChecked++
				if !json.Valid(active) {
					report.problem("ERROR", ref.slug+"/"+prov.ManifestFile, "invalid JSON")
				}
			}
		}
	}
	preservedLinked := make(map[string]bool, len(prov.LinkedResources))
	for _, resource := range prov.LinkedResources {
		preservedLinked[resource.URL] = true
	}
	linkedRerunRecommended := len(prov.LinkedFailures) > 0
	if expected, discoverErr := preserve.DiscoverLinkedResources(presentationManifest); discoverErr == nil {
		type missingGroup struct {
			count int
			first string
		}
		missing := make(map[string]missingGroup)
		for _, resource := range expected {
			if !preservedLinked[resource.URL] {
				group := missing[resource.Kind]
				group.count++
				if group.first == "" {
					group.first = resource.URL
				}
				missing[resource.Kind] = group
			}
		}
		kinds := make([]string, 0, len(missing))
		for kind := range missing {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			group := missing[kind]
			report.problem("WARN", ref.slug+"/manifest.json", "%d linked %s resource(s) are not preserved (first: %s)", group.count, kind, group.first)
			linkedRerunRecommended = true
		}
	}

	seen := make(map[string]bool)
	for i, img := range prov.Images {
		report.Images++
		label := fmt.Sprintf("provenance image %d", i+1)
		if !safeBundleRelative(img.File) {
			report.problem("ERROR", ref.slug+"/provenance.json", "%s has unsafe file path %q", label, img.File)
			continue
		}
		if seen[img.File] {
			report.problem("ERROR", ref.slug+"/provenance.json", "duplicate image file %q", img.File)
		}
		seen[img.File] = true
		checkRegularFile(report, ref.absDir, ref.slug, img.File)

		if img.TileDir == "" {
			continue
		}
		if !safeBundleRelative(img.TileDir) {
			report.problem("ERROR", ref.slug+"/provenance.json", "%s has unsafe tile_dir %q", label, img.TileDir)
			continue
		}
		report.TilePyramids++
		diagnosePyramid(report, ref, img.TileDir)
	}
	for i, resource := range prov.LinkedResources {
		report.LinkedResources++
		label := fmt.Sprintf("linked resource %d", i+1)
		if !safeBundleRelative(resource.File) {
			report.problem("ERROR", ref.slug+"/provenance.json", "%s has unsafe file path %q", label, resource.File)
			continue
		}
		checkRegularFile(report, ref.absDir, ref.slug, resource.File)
		data, readErr := os.ReadFile(filepath.Join(ref.absDir, filepath.FromSlash(resource.File))) //nolint:gosec // validated bundle-relative path
		if readErr == nil && resource.SHA256 != "" {
			digest := sha256.Sum256(data)
			if fmt.Sprintf("%x", digest[:]) != resource.SHA256 {
				report.problem("ERROR", ref.slug+"/"+resource.File, "SHA-256 does not match provenance")
			}
		}
	}
	for _, failure := range prov.LinkedFailures {
		path := ref.slug + "/provenance.json"
		if failure.URL != "" {
			path = failure.URL
		}
		report.problem("WARN", path, "unpreserved linked %s resource: %s", failure.Kind, failure.Error)
	}
	if linkedRerunRecommended {
		report.recommendManifestRerun(prov.ManifestURL)
	}

	if _, err := annotation.Load(ref.absDir); err != nil {
		report.problem("ERROR", ref.slug+"/"+annotation.FileName, "%v", err)
	} else if _, err := os.Stat(filepath.Join(ref.absDir, annotation.FileName)); err == nil {
		report.FilesChecked++
	}
}

func safeBundleRelative(name string) bool {
	if name == "" || filepath.IsAbs(name) {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func checkRegularFile(report *DoctorReport, absBundle, slug, rel string) {
	fi, err := os.Stat(filepath.Join(absBundle, filepath.FromSlash(rel)))
	path := slug + "/" + filepath.ToSlash(rel)
	if err != nil {
		report.problem("ERROR", path, "missing or unreadable: %v", err)
		return
	}
	report.FilesChecked++
	if !fi.Mode().IsRegular() {
		report.problem("ERROR", path, "expected a regular file")
	} else if fi.Size() == 0 {
		report.problem("ERROR", path, "file is empty")
	}
}

type doctorInfo struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	Sizes  []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"sizes"`
	Tiles []struct {
		Width        int   `json:"width"`
		Height       int   `json:"height"`
		ScaleFactors []int `json:"scaleFactors"`
	} `json:"tiles"`
}

func diagnosePyramid(report *DoctorReport, ref bundleRef, tileDir string) {
	infoRel := filepath.ToSlash(filepath.Join(tileDir, "info.json"))
	infoPath := filepath.Join(ref.absDir, filepath.FromSlash(infoRel))
	b, err := os.ReadFile(infoPath) //nolint:gosec // validated relative provenance path under bundle
	if err != nil {
		report.problem("ERROR", ref.slug+"/"+infoRel, "cannot read tile metadata: %v", err)
		return
	}
	report.FilesChecked++
	var info doctorInfo
	if err := json.Unmarshal(b, &info); err != nil {
		report.problem("ERROR", ref.slug+"/"+infoRel, "invalid info.json: %v", err)
		return
	}
	if info.Width <= 0 || info.Height <= 0 || len(info.Tiles) == 0 {
		report.problem("ERROR", ref.slug+"/"+infoRel, "invalid dimensions or missing tile profile")
		return
	}
	for _, size := range info.Sizes {
		if size.Width <= 0 || size.Height <= 0 {
			report.problem("ERROR", ref.slug+"/"+infoRel, "invalid advertised full size %dx%d", size.Width, size.Height)
			continue
		}
		rel := fmt.Sprintf("%s/full/%d,%d/0/default.jpg", tileDir, size.Width, size.Height)
		checkRegularFile(report, ref.absDir, ref.slug, rel)
	}
	for _, profile := range info.Tiles {
		if profile.Width <= 0 {
			report.problem("ERROR", ref.slug+"/"+infoRel, "invalid tile width %d", profile.Width)
			continue
		}
		tileHeight := profile.Height
		if tileHeight <= 0 {
			tileHeight = profile.Width
		}
		for _, scale := range profile.ScaleFactors {
			if scale <= 0 {
				report.problem("ERROR", ref.slug+"/"+infoRel, "invalid scale factor %d", scale)
				continue
			}
			maxInt := int(^uint(0) >> 1)
			if scale > maxInt/profile.Width || scale > maxInt/tileHeight {
				report.problem("ERROR", ref.slug+"/"+infoRel, "scale factor %d overflows tile dimensions", scale)
				continue
			}
			fullW, fullH := profile.Width*scale, tileHeight*scale
			cols, rows := doctorCeilDiv(info.Width, fullW), doctorCeilDiv(info.Height, fullH)
			if cols > 10_000_000/max(rows, 1) {
				report.problem("ERROR", ref.slug+"/"+infoRel, "tile profile advertises an unreasonable number of files")
				continue
			}
			for y := 0; y < info.Height; y += fullH {
				for x := 0; x < info.Width; x += fullW {
					rw, rh := min(fullW, info.Width-x), min(fullH, info.Height-y)
					sw, sh := doctorCeilDiv(rw, scale), doctorCeilDiv(rh, scale)
					rel := fmt.Sprintf("%s/%d,%d,%d,%d/%d,%d/0/default.jpg", tileDir, x, y, rw, rh, sw, sh)
					checkRegularFile(report, ref.absDir, ref.slug, rel)
				}
			}
		}
	}
}

func doctorCeilDiv(a, b int) int { return (a + b - 1) / b }

func diagnoseCatalog(report *DoctorReport, root string, known map[string]bool) {
	path := filepath.Join(root, catalogDirName, catalogFileName)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			report.problem("WARN", filepath.Join(catalogDirName, catalogFileName), "catalogue index is absent; serving will rebuild it")
		} else {
			report.problem("ERROR", filepath.Join(catalogDirName, catalogFileName), "cannot inspect catalogue: %v", err)
		}
		return
	}
	report.FilesChecked++
	c := &catalog{path: path}
	entries, err := c.load()
	if err != nil {
		report.problem("ERROR", filepath.Join(catalogDirName, catalogFileName), "%v", err)
		return
	}
	for dir := range entries {
		if !known[dir] {
			report.problem("WARN", filepath.Join(catalogDirName, catalogFileName), "stale entry for missing bundle %q", dir)
		}
	}
	for dir := range known {
		if _, ok := entries[dir]; !ok {
			report.problem("WARN", filepath.Join(catalogDirName, catalogFileName), "missing entry for bundle %q; serving will add it", dir)
		}
	}
}
