package rdfingest

import (
	"strings"
	"testing"
)

func TestParseNTriples(t *testing.T) {
	data := []byte(`<https://museum.example/work> <https://schema.org/name> "N-Triples Work"@en .
<https://museum.example/work> <https://schema.org/image> <https://media.example/work.jpg> .
<https://media.example/work.jpg> <https://schema.org/width> "600" .
<https://media.example/work.jpg> <https://schema.org/height> "900" .`)
	graph, err := Parse(data, ParseOptions{Format: FormatNTriples})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if graph.Len() != 4 {
		t.Fatalf("triples = %d, want 4", graph.Len())
	}
	name := graph.Objects("https://museum.example/work", schema+"name")
	if len(name) != 1 || name[0].Value != "N-Triples Work" || name[0].Language != "en" {
		t.Fatalf("name = %+v", name)
	}
}

func TestConvertTurtle(t *testing.T) {
	data := []byte(`@prefix schema: <https://schema.org/> .
<https://museum.example/turtle> a schema:VisualArtwork ;
  schema:name "Turtle Work"@en ;
  schema:image <https://media.example/turtle.jpg> .
<https://media.example/turtle.jpg>
  schema:width 700 ; schema:height 1000 .`)
	result, err := Convert(data, Options{
		Format: FormatTurtle, SourceURL: "https://museum.example/turtle.ttl",
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.RecordURL != "https://museum.example/turtle" || result.ImageWidth != 700 || result.ImageHeight != 1000 ||
		!strings.Contains(string(result.Manifest), "Turtle Work") {
		t.Fatalf("conversion = %+v manifest=%s", result, result.Manifest)
	}
}

func TestConvertJSONLD(t *testing.T) {
	data := []byte(`{
  "@context": {"schema":"https://schema.org/"},
  "@graph": [
    {"@id":"https://museum.example/jsonld", "@type":"schema:VisualArtwork",
     "schema:name":{"@value":"JSON-LD Work","@language":"en"},
     "schema:image":{"@id":"https://media.example/jsonld.jpg"}},
    {"@id":"https://media.example/jsonld.jpg", "@type":"schema:ImageObject",
     "schema:width":750, "schema:height":1100}
  ]
}`)
	result, err := Convert(data, Options{
		Format: FormatJSONLD, SourceURL: "https://museum.example/jsonld.jsonld",
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.RecordURL != "https://museum.example/jsonld" || result.ImageWidth != 750 || result.ImageHeight != 1100 ||
		!strings.Contains(string(result.Manifest), "JSON-LD Work") {
		t.Fatalf("conversion = %+v manifest=%s", result, result.Manifest)
	}
}

func TestDetectFormatFromContentAndName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   string
		source string
		want   Format
	}{
		{"RDF XML", `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"/>`, "work.rdf", FormatRDFXML},
		{"Turtle", `@prefix schema: <https://schema.org/> .`, "work.ttl", FormatTurtle},
		{"N-Triples", `<https://e/s> <https://e/p> "o" .`, "work.nt", FormatNTriples},
		{"JSON-LD", `{"@context":{},"@id":"https://e/s"}`, "work.jsonld", FormatJSONLD},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectFormat([]byte(tc.data), tc.source)
			if err != nil || got != tc.want {
				t.Fatalf("DetectFormat = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
