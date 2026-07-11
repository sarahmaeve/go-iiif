package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/annotation"
)

const exchangeManifestURL = "https://example.org/iiif/manifest/shared"
const exchangeCanvas = "https://example.org/iiif/canvas/1"

func writeExchangeBundle(t *testing.T, root, rel string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := `{"type":"Manifest","label":{"en":["Shared manuscript"]}}`
	prov := `{"manifest_url":"` + exchangeManifestURL + `","images":[]}`
	for name, body := range map[string]string{"manifest.json": manifest, "provenance.json": prov} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func exchangeAnnotation(id, text string) annotation.Annotation {
	body, _ := json.Marshal(map[string]string{"type": "TextualBody", "value": text})
	target, _ := json.Marshal(exchangeCanvas)
	return annotation.Annotation{ID: id, Type: "Annotation", Motivation: "commenting", Body: body, Target: target}
}

func TestResearchMetadataExportImportNonDestructive(t *testing.T) {
	sourceRoot := t.TempDir()
	sourceRel := "source.example/shared"
	sourceDir := writeExchangeBundle(t, sourceRoot, sourceRel)
	sourceCatalog := newCatalog(sourceRoot)
	if err := sourceCatalog.update(sourceRel, "Incoming English title", "Incoming research note", "Old French, John Dee"); err != nil {
		t.Fatal(err)
	}
	incoming := []annotation.Annotation{
		exchangeAnnotation("urn:annotation:same", "same note"),
		exchangeAnnotation("urn:annotation:conflict", "incoming conflict"),
		exchangeAnnotation("urn:annotation:new", "new shared note"),
	}
	if err := annotation.Save(sourceDir, annotation.Page{Type: "AnnotationPage", Items: incoming}); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	exported, err := ExportResearchMetadata(sourceRoot, &archive)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Bundles != 1 || exported.Annotations != 3 || strings.Contains(archive.String(), "0001.jpg") {
		t.Fatalf("unexpected export report/archive: %+v\n%s", exported, archive.String())
	}

	targetRoot := t.TempDir()
	targetRel := "different-layout/manuscript"
	targetDir := writeExchangeBundle(t, targetRoot, targetRel)
	targetCatalog := newCatalog(targetRoot)
	if err := targetCatalog.update(targetRel, "Local title", "", "Local tag, John Dee"); err != nil {
		t.Fatal(err)
	}
	local := []annotation.Annotation{
		exchangeAnnotation("urn:annotation:same", "same note"),
		exchangeAnnotation("urn:annotation:conflict", "local conflict"),
	}
	if err := annotation.Save(targetDir, annotation.Page{Type: "AnnotationPage", Items: local}); err != nil {
		t.Fatal(err)
	}

	result, err := ImportResearchMetadata(targetRoot, bytes.NewReader(archive.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if result.Bundles != 1 || result.CatalogChanges != 1 || result.AnnotationsAdded != 1 || result.Duplicates != 1 {
		t.Fatalf("import result = %+v", result)
	}
	if len(result.Warnings) < 2 {
		t.Fatalf("expected title/annotation conflicts, got %+v", result.Warnings)
	}

	mergedCatalog := newCatalog(targetRoot)
	entry, ok := mergedCatalog.get(targetRel)
	if !ok {
		t.Fatal("target catalogue entry disappeared")
	}
	if entry.CustomTitle != "Local title" || entry.Notes != "Incoming research note" || entry.Tags != "Local tag, John Dee, Old French" {
		t.Fatalf("non-destructive catalogue merge = %#v", entry)
	}
	page, err := annotation.Load(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("merged annotations = %d, want 3", len(page.Items))
	}
	for _, a := range page.Items {
		if a.ID == "urn:annotation:conflict" && !strings.Contains(string(a.Body), "local conflict") {
			t.Fatalf("conflicting local annotation was overwritten: %s", a.Body)
		}
	}

	second, err := ImportResearchMetadata(targetRoot, bytes.NewReader(archive.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if second.AnnotationsAdded != 0 || second.Duplicates != 2 {
		t.Fatalf("repeat import was not idempotent: %+v", second)
	}
}

func TestResearchMetadataImportRejectsUnknownFormat(t *testing.T) {
	root := t.TempDir()
	_, err := ImportResearchMetadata(root, strings.NewReader(`{"format":"other","version":1}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("unknown archive format error = %v", err)
	}
}
