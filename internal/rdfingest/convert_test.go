package rdfingest

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const (
	rdfType = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	schema  = "https://schema.org/"
)

func TestParseRDFXMLProducesVocabularyNeutralGraph(t *testing.T) {
	rdf := []byte(`<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
         xmlns:schema="https://schema.org/">
  <schema:VisualArtwork rdf:about="https://museum.example/object/42">
    <schema:name xml:lang="en">An Example Painting</schema:name>
    <schema:image rdf:resource="https://media.example/42.jpg"/>
  </schema:VisualArtwork>
</rdf:RDF>`)

	graph, err := Parse(rdf, ParseOptions{Format: FormatRDFXML})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if graph.Len() != 3 {
		t.Fatalf("graph has %d triples, want 3: %+v", graph.Len(), graph.Triples())
	}
	types := graph.Objects("https://museum.example/object/42", rdfType)
	if len(types) != 1 || types[0].Kind != IRI || types[0].Value != schema+"VisualArtwork" {
		t.Fatalf("rdf:type objects = %+v", types)
	}
	names := graph.Objects("https://museum.example/object/42", schema+"name")
	if len(names) != 1 || names[0].Kind != Literal || names[0].Value != "An Example Painting" || names[0].Language != "en" {
		t.Fatalf("schema:name objects = %+v", names)
	}
}

func TestConvertDirectImageRDFToPresentation3(t *testing.T) {
	rdf := []byte(`<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
         xmlns:schema="https://schema.org/">
  <schema:VisualArtwork rdf:about="https://museum.example/object/42">
    <schema:name xml:lang="en">An Example Painting</schema:name>
    <schema:name xml:lang="fr">Un tableau d'exemple</schema:name>
    <schema:description xml:lang="en">A small test painting.</schema:description>
    <schema:dateCreated>1617</schema:dateCreated>
    <schema:date>16170000000000</schema:date>
    <schema:creator>Example Artist</schema:creator>
    <schema:image rdf:resource="https://media.example/42.jpg"/>
  </schema:VisualArtwork>
  <schema:ImageObject rdf:about="https://media.example/42.jpg">
    <schema:width>800</schema:width>
    <schema:height>1200</schema:height>
  </schema:ImageObject>
</rdf:RDF>`)

	result, err := Convert(rdf, Options{
		Format:    FormatRDFXML,
		SourceURL: "https://museum.example/object/42.rdf",
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.RecordURL != "https://museum.example/object/42" || result.ImageURL != "https://media.example/42.jpg" {
		t.Fatalf("conversion identity = %+v", result)
	}
	wantDigest := sha256.Sum256(rdf)
	if result.SourceSHA256 != fmt.Sprintf("%x", wantDigest[:]) {
		t.Fatalf("source digest = %q", result.SourceSHA256)
	}

	var manifest struct {
		Context string              `json:"@context"`
		ID      string              `json:"id"`
		Type    string              `json:"type"`
		Label   map[string][]string `json:"label"`
		Summary map[string][]string `json:"summary"`
		SeeAlso []struct {
			ID     string `json:"id"`
			Format string `json:"format"`
		} `json:"seeAlso"`
		Metadata []struct {
			Label map[string][]string `json:"label"`
			Value map[string][]string `json:"value"`
		} `json:"metadata"`
		Items []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
			Items  []struct {
				Items []struct {
					Body struct {
						ID     string `json:"id"`
						Width  int    `json:"width"`
						Height int    `json:"height"`
					} `json:"body"`
				} `json:"items"`
			} `json:"items"`
		} `json:"items"`
	}
	if err := json.Unmarshal(result.Manifest, &manifest); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if manifest.Context != "http://iiif.io/api/presentation/3/context.json" || manifest.Type != "Manifest" || manifest.ID != result.RecordURL {
		t.Fatalf("manifest identity = %+v", manifest)
	}
	if manifest.Label["en"][0] != "An Example Painting" || manifest.Label["fr"][0] != "Un tableau d'exemple" ||
		manifest.Summary["en"][0] != "A small test painting." {
		t.Fatalf("manifest language maps = label:%v summary:%v", manifest.Label, manifest.Summary)
	}
	if len(manifest.SeeAlso) != 1 || manifest.SeeAlso[0].ID != "https://museum.example/object/42.rdf" || manifest.SeeAlso[0].Format != "application/rdf+xml" {
		t.Fatalf("manifest seeAlso = %+v", manifest.SeeAlso)
	}
	metadata := make(map[string]map[string][]string)
	for _, entry := range manifest.Metadata {
		if len(entry.Label["en"]) == 0 {
			t.Fatalf("metadata entry has no English label: %+v", entry)
		}
		metadata[entry.Label["en"][0]] = entry.Value
	}
	if len(metadata["Date"]["none"]) != 1 || metadata["Date"]["none"][0] != "1617" ||
		len(metadata["Creator"]["none"]) == 0 || metadata["Creator"]["none"][0] != "Example Artist" {
		t.Fatalf("manifest metadata = %+v", manifest.Metadata)
	}
	body := manifest.Items[0].Items[0].Items[0].Body
	if manifest.Items[0].Width != 800 || manifest.Items[0].Height != 1200 || body.ID != result.ImageURL || body.Width != 800 || body.Height != 1200 {
		t.Fatalf("canvas/body = %+v / %+v", manifest.Items[0], body)
	}
}

func TestConvertIndirectVisualItemUsesPreferredImageWithoutHostSpecificCode(t *testing.T) {
	rdf := []byte(`<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
         xmlns:sioc="http://rdfs.org/sioc/ns#"
         xmlns:gnoss="https://example.org/graph/"
         xmlns:cidoc="http://www.cidoc-crm.org/cidoc-crm#"
         xmlns:catalog="https://museum.example/vocabulary/">
  <sioc:Item rdf:about="https://museum.example/works/abc">
    <gnoss:has_docSem rdf:resource="https://data.example/object/abc"/>
  </sioc:Item>
  <cidoc:E22_Man-Made_Object rdf:about="https://data.example/object/abc">
    <catalog:title xml:lang="en">Graph Painting</catalog:title>
    <catalog:main_image>images/preferred.jpg</catalog:main_image>
    <cidoc:p65_shows_visual_item rdf:resource="https://data.example/image/one"/>
    <cidoc:p65_shows_visual_item rdf:resource="https://data.example/image/two"/>
  </cidoc:E22_Man-Made_Object>
  <cidoc:E36_Visual_Item rdf:about="https://data.example/image/one">
    <catalog:imageWidth>1000</catalog:imageWidth>
    <catalog:imageHeight>1500</catalog:imageHeight>
    <catalog:filePath>images/preferred.jpg</catalog:filePath>
  </cidoc:E36_Visual_Item>
  <cidoc:E36_Visual_Item rdf:about="https://data.example/image/two">
    <catalog:imageWidth>2000</catalog:imageWidth>
    <catalog:imageHeight>3000</catalog:imageHeight>
    <catalog:filePath>images/alternate.jpg</catalog:filePath>
  </cidoc:E36_Visual_Item>
</rdf:RDF>`)

	result, err := Convert(rdf, Options{
		Format:       FormatRDFXML,
		SourceURL:    "https://museum.example/works/abc.rdf",
		ImageBaseURL: "https://media.example/assets/",
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.RecordURL != "https://museum.example/works/abc" {
		t.Fatalf("record URL = %q", result.RecordURL)
	}
	if result.ImageURL != "https://media.example/assets/images/preferred.jpg" || result.ImageWidth != 1000 || result.ImageHeight != 1500 {
		t.Fatalf("preferred image = %+v", result)
	}
	if strings.Contains(string(result.Manifest), "alternate.jpg") {
		t.Fatalf("derived single-image manifest included non-primary image: %s", result.Manifest)
	}
}

func TestConvertRelativeImageRequiresResolutionInput(t *testing.T) {
	rdf := []byte(`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns:schema="https://schema.org/" xmlns:ex="https://example.org/vocabulary/"><schema:VisualArtwork rdf:about="https://museum.example/42"><schema:name>Work</schema:name><schema:image>images/42.jpg</schema:image><ex:hasImage rdf:resource="https://data.example/image/42"/></schema:VisualArtwork><schema:ImageObject rdf:about="https://data.example/image/42"><schema:contentUrl>images/42.jpg</schema:contentUrl><schema:width>800</schema:width><schema:height>1200</schema:height></schema:ImageObject></rdf:RDF>`)
	_, err := Convert(rdf, Options{Format: FormatRDFXML, SourceURL: "https://museum.example/42.rdf"})
	if err == nil || !strings.Contains(err.Error(), "image base") {
		t.Fatalf("Convert error = %v, want actionable image-base error", err)
	}
}

func TestConvertLocalArtifactsUseStableGraphIDsAndActualImageDimensions(t *testing.T) {
	rdf := []byte(`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns:schema="https://schema.org/" xmlns:ex="https://example.org/vocabulary/"><schema:VisualArtwork rdf:about="https://museum.example/works/local"><schema:name>Locally acquired work</schema:name><schema:image>images/original.jpg</schema:image><ex:hasImage rdf:resource="https://data.example/image/local"/></schema:VisualArtwork><schema:ImageObject rdf:about="https://data.example/image/local"><schema:contentUrl>images/original.jpg</schema:contentUrl><schema:width>1944</schema:width><schema:height>2952</schema:height></schema:ImageObject></rdf:RDF>`)

	result, err := Convert(rdf, Options{
		Format:           FormatRDFXML,
		LocalSource:      true,
		LocalImage:       true,
		LocalImageWidth:  1264,
		LocalImageHeight: 1920,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.SourceURL != "https://museum.example/works/local#iiifpreserve-rdf-source" {
		t.Fatalf("local source identity = %q", result.SourceURL)
	}
	if result.ImageURL != "https://museum.example/works/local/iiifpreserve-primary-image.jpg" {
		t.Fatalf("local image identity = %q", result.ImageURL)
	}
	if result.ImageWidth != 1264 || result.ImageHeight != 1920 {
		t.Fatalf("local image dimensions = %dx%d", result.ImageWidth, result.ImageHeight)
	}
	if !strings.Contains(string(result.Manifest), `"width": 1264`) || !strings.Contains(string(result.Manifest), result.SourceURL) {
		t.Fatalf("manifest does not describe local artifacts: %s", result.Manifest)
	}
}

func TestConvertLocalImageSuppliesMissingRDFPresentationLayer(t *testing.T) {
	rdf := []byte(`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns:dcterms="http://purl.org/dc/terms/"><rdf:Description rdf:about="https://museum.example/works/descriptive-only"><dcterms:title xml:lang="en">Description without a digital image</dcterms:title></rdf:Description></rdf:RDF>`)

	result, err := Convert(rdf, Options{
		Format:           FormatRDFXML,
		LocalSource:      true,
		LocalImage:       true,
		LocalImageWidth:  900,
		LocalImageHeight: 600,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.RecordURL != "https://museum.example/works/descriptive-only" {
		t.Fatalf("record URL = %q", result.RecordURL)
	}
	if result.ImageURL != "https://museum.example/works/descriptive-only/iiifpreserve-primary-image.jpg" || result.ImageWidth != 900 || result.ImageHeight != 600 {
		t.Fatalf("local presentation layer = %+v", result)
	}
	if !strings.Contains(string(result.Manifest), "Description without a digital image") {
		t.Fatalf("manifest lost RDF description: %s", result.Manifest)
	}
}
