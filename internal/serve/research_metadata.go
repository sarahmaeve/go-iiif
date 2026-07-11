package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sarahmaeve/go-iiif/internal/annotation"
)

const (
	researchMetadataFormat  = "iiifpreserve-research-metadata"
	researchMetadataVersion = 1
	maxResearchMetadataSize = 64 << 20
)

type researchMetadataArchive struct {
	Format     string                   `json:"format"`
	Version    int                      `json:"version"`
	ExportedAt time.Time                `json:"exported_at"`
	Bundles    []researchMetadataBundle `json:"bundles"`
}

type researchMetadataBundle struct {
	ManifestURL string                  `json:"manifest_url"`
	BundleDir   string                  `json:"bundle_dir"`
	Catalog     researchCatalogFields   `json:"catalog,omitempty"`
	Annotations []annotation.Annotation `json:"annotations,omitempty"`
}

type researchCatalogFields struct {
	DisplayTitle string   `json:"display_title,omitempty"`
	Notes        string   `json:"notes,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// MetadataTransferReport describes an export or non-destructive import.
type MetadataTransferReport struct {
	Bundles          int
	CatalogChanges   int
	Annotations      int
	AnnotationsAdded int
	Duplicates       int
	Warnings         []string
}

// ExportResearchMetadata writes a portable archive containing only
// researcher-authored catalogue fields and annotations, never images/tiles.
func ExportResearchMetadata(root string, w io.Writer) (MetadataTransferReport, error) {
	var report MetadataTransferReport
	c := newCatalog(root)
	if c.loadErr != nil {
		return report, c.loadErr
	}
	archive := researchMetadataArchive{
		Format: researchMetadataFormat, Version: researchMetadataVersion, ExportedAt: time.Now().UTC(),
	}
	for _, entry := range c.list() {
		page, err := annotation.Load(filepath.Join(root, filepath.FromSlash(entry.Dir)))
		if err != nil {
			return report, fmt.Errorf("metadata export: %s: %w", entry.Dir, err)
		}
		if entry.CustomTitle == "" && entry.Notes == "" && entry.Tags == "" && len(page.Items) == 0 {
			continue
		}
		bundle := researchMetadataBundle{
			ManifestURL: entry.RecordURL,
			BundleDir:   entry.Dir,
			Catalog: researchCatalogFields{
				DisplayTitle: entry.CustomTitle,
				Notes:        entry.Notes,
				Tags:         splitCatalogTags(entry.Tags),
			},
			Annotations: page.Items,
		}
		archive.Bundles = append(archive.Bundles, bundle)
		report.Annotations += len(page.Items)
	}
	report.Bundles = len(archive.Bundles)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(archive); err != nil {
		return report, fmt.Errorf("metadata export: encoding: %w", err)
	}
	return report, nil
}

// ExportResearchMetadataFile atomically replaces filename with a complete
// archive, so an interrupted export never leaves a truncated exchange file.
func ExportResearchMetadataFile(root, filename string) (MetadataTransferReport, error) {
	var report MetadataTransferReport
	dir := filepath.Dir(filename)
	tmp, err := os.CreateTemp(dir, ".iiif-metadata-*.json")
	if err != nil {
		return report, fmt.Errorf("metadata export: creating temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	report, err = ExportResearchMetadata(root, tmp)
	if err != nil {
		_ = tmp.Close()
		return report, err
	}
	if err := tmp.Close(); err != nil {
		return report, fmt.Errorf("metadata export: closing temporary file: %w", err)
	}
	if err := os.Rename(tmpName, filename); err != nil {
		return report, fmt.Errorf("metadata export: finalizing: %w", err)
	}
	return report, nil
}

// ImportResearchMetadata merges an exchange archive without overwriting
// local research. Blank local title/notes are filled, tags are unioned, new
// annotation ids are appended, exact duplicates are ignored, and conflicts
// are reported as warnings.
func ImportResearchMetadata(root string, r io.Reader) (MetadataTransferReport, error) {
	var report MetadataTransferReport
	b, err := io.ReadAll(io.LimitReader(r, maxResearchMetadataSize+1))
	if err != nil {
		return report, fmt.Errorf("metadata import: reading: %w", err)
	}
	if len(b) > maxResearchMetadataSize {
		return report, fmt.Errorf("metadata import: archive exceeds %d bytes", maxResearchMetadataSize)
	}
	var archive researchMetadataArchive
	if err := json.Unmarshal(b, &archive); err != nil {
		return report, fmt.Errorf("metadata import: decoding: %w", err)
	}
	if archive.Format != researchMetadataFormat || archive.Version != researchMetadataVersion {
		return report, fmt.Errorf("metadata import: unsupported format %q version %d", archive.Format, archive.Version)
	}

	c := newCatalog(root)
	if c.loadErr != nil {
		return report, c.loadErr
	}
	byURL := make(map[string]string)
	for _, entry := range c.list() {
		if entry.RecordURL != "" {
			byURL[entry.RecordURL] = entry.Dir
		}
	}
	seenBundles := make(map[string]bool)
	for _, incoming := range archive.Bundles {
		dir, ok := byURL[incoming.ManifestURL]
		if !ok {
			dir = incoming.BundleDir
			_, ok = c.get(dir)
		}
		if !ok || seenBundles[dir] {
			reason := "no matching local bundle"
			if seenBundles[dir] {
				reason = "duplicate bundle in archive"
			}
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %s", incoming.BundleDir, reason))
			continue
		}
		seenBundles[dir] = true
		report.Bundles++
		if mergeCatalogResearch(c, dir, incoming.Catalog, &report) {
			report.CatalogChanges++
		}
		mergeAnnotations(root, dir, incoming.Annotations, &report)
	}
	return report, nil
}

func ImportResearchMetadataFile(root, filename string) (MetadataTransferReport, error) {
	f, err := os.Open(filename) //nolint:gosec // explicit operator-supplied import path
	if err != nil {
		return MetadataTransferReport{}, fmt.Errorf("metadata import: opening: %w", err)
	}
	defer func() { _ = f.Close() }()
	return ImportResearchMetadata(root, f)
}

func mergeCatalogResearch(c *catalog, dir string, incoming researchCatalogFields, report *MetadataTransferReport) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[dir]
	if !ok {
		return false
	}
	changed := false
	if incoming.DisplayTitle != "" {
		switch {
		case entry.CustomTitle == "":
			entry.CustomTitle = strings.TrimSpace(incoming.DisplayTitle)
			changed = true
		case entry.CustomTitle != strings.TrimSpace(incoming.DisplayTitle):
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: display title conflict; kept local value", dir))
		}
	}
	if incoming.Notes != "" {
		switch {
		case entry.Notes == "":
			entry.Notes = strings.TrimSpace(incoming.Notes)
			changed = true
		case entry.Notes != strings.TrimSpace(incoming.Notes):
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: notes conflict; kept local value", dir))
		}
	}
	mergedTags := normalizeCatalogTags(strings.Join(append(splitCatalogTags(entry.Tags), incoming.Tags...), ","))
	if mergedTags != entry.Tags {
		entry.Tags = mergedTags
		changed = true
	}
	if !changed {
		return false
	}
	before := c.entries[dir]
	entry.finishDisplayFields()
	c.entries[dir] = entry
	if err := c.saveLocked(); err != nil {
		c.entries[dir] = before
		report.Warnings = append(report.Warnings, fmt.Sprintf("%s: catalogue merge could not be saved: %v", dir, err))
		return false
	}
	return true
}

func mergeAnnotations(root, dir string, incoming []annotation.Annotation, report *MetadataTransferReport) {
	if len(incoming) == 0 {
		return
	}
	absDir := filepath.Join(root, filepath.FromSlash(dir))
	page, err := annotation.Load(absDir)
	if err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%s: could not load annotations: %v", dir, err))
		return
	}
	existing := make(map[string]annotation.Annotation, len(page.Items))
	for _, a := range page.Items {
		existing[a.ID] = a
	}
	added := 0
	for _, a := range incoming {
		if a.ID == "" || a.CanvasID() == "" {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: skipped incoming annotation without id or Canvas target", dir))
			continue
		}
		if old, ok := existing[a.ID]; ok {
			if annotationsEqual(old, a) {
				report.Duplicates++
			} else {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s: annotation %q conflicts; kept local value", dir, a.ID))
			}
			continue
		}
		page.Items = append(page.Items, a)
		existing[a.ID] = a
		added++
	}
	if added == 0 {
		return
	}
	if err := annotation.Save(absDir, page); err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%s: could not save annotations: %v", dir, err))
		return
	}
	report.AnnotationsAdded += added
}

func annotationsEqual(a, b annotation.Annotation) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

func splitCatalogTags(tags string) []string {
	if tags == "" {
		return nil
	}
	parts := strings.Split(tags, ",")
	out := make([]string, 0, len(parts))
	for _, tag := range parts {
		if tag = strings.TrimSpace(tag); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}
