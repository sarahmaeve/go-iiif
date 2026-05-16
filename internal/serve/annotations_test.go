package serve

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/annotation"
)

// annPageJSON builds an annotation.Page from one raw annotation object.
func annPageJSON(t *testing.T, item string) annotation.Page {
	t.Helper()
	var a annotation.Annotation
	if err := json.Unmarshal([]byte(item), &a); err != nil {
		t.Fatalf("bad annotation fixture: %v", err)
	}
	return annotation.Page{Type: "AnnotationPage", Items: []annotation.Annotation{a}}
}

// A user's offline annotations (annotations.json beside the bundle) must be
// injected into the served manifest's matching Canvas so Mirador displays
// them — no extra fetch, no source contact.
func TestServer_InjectsAnnotationsIntoCanvas(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "inst", "ms")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const canvasID = "https://inst.example/iiif/ms/canvas/p1"
	manifest := `{"@context":"http://iiif.io/api/presentation/3/context.json",
	  "id":"https://inst.example/iiif/ms/manifest.json","type":"Manifest",
	  "items":[{"id":"` + canvasID + `","type":"Canvas","height":100,"width":80,
	    "items":[{"id":"https://inst.example/p1/ap","type":"AnnotationPage","items":[
	      {"id":"https://inst.example/p1/anno","type":"Annotation","motivation":"painting",
	       "body":{"id":"https://inst.example/iiif/img/p1/full/max/0/default.jpg","type":"Image","format":"image/jpeg"},
	       "target":"` + canvasID + `"}]}]}]}`
	prov := `{"manifest_url":"https://inst.example/iiif/ms/manifest.json","images":[{"file":"0001.jpg","source_url":"https://inst.example/iiif/img/p1/full/max/0/default.jpg"}]}`
	anns := `{"@context":"http://www.w3.org/ns/anno.jsonld","type":"AnnotationPage","items":[
	  {"id":"urn:note:1","type":"Annotation","motivation":"commenting",
	   "body":{"type":"TextualBody","value":"a scholarly marginal note","format":"text/plain"},
	   "target":"` + canvasID + `#xywh=10,10,40,20"}]}`
	for name, content := range map[string]string{
		"manifest.json": manifest, "provenance.json": prov, "annotations.json": anns,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	ts := httptest.NewServer(New(root).Handler())
	defer ts.Close()
	_, body, _ := viewerGet(t, ts, "/inst/ms/manifest.json")

	if !strings.Contains(body, "a scholarly marginal note") {
		t.Fatalf("user annotation not injected into served manifest:\n%s", body)
	}

	// Structurally: the Canvas now carries an AnnotationPage with our note,
	// without clobbering the existing painting AnnotationPage in items[].
	var m struct {
		Items []struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Items []struct {
				Type string `json:"type"`
			} `json:"items"`
			Annotations []struct {
				Type  string `json:"type"`
				Items []struct {
					Motivation string `json:"motivation"`
				} `json:"items"`
			} `json:"annotations"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("served manifest invalid: %v", err)
	}
	cv := m.Items[0]
	if len(cv.Items) != 1 || cv.Items[0].Type != "AnnotationPage" {
		t.Fatalf("existing painting AnnotationPage in items[] was clobbered: %+v", cv.Items)
	}
	if len(cv.Annotations) != 1 || cv.Annotations[0].Type != "AnnotationPage" ||
		len(cv.Annotations[0].Items) != 1 || cv.Annotations[0].Items[0].Motivation != "commenting" {
		t.Fatalf("canvas.annotations not injected correctly: %+v", cv.Annotations)
	}
}

// Most of the preserved library is IIIF Presentation 2 (Gallica/Bodleian/
// e-codices). Injection must work on the v2 sequences→canvases shape with
// @id / @type:"sc:Canvas", not just v3.
func TestInjectAnnotations_V2Canvas(t *testing.T) {
	const cid = "https://gallica.bnf.fr/iiif/ark:/12148/x/canvas/f1"
	manifest := []byte(`{"@context":"http://iiif.io/api/presentation/2/context.json",
	  "@type":"sc:Manifest","sequences":[{"@type":"sc:Sequence","canvases":[
	    {"@id":"` + cid + `","@type":"sc:Canvas","width":5127,"height":7000,"images":[]}]}]}`)
	page := annPageJSON(t, `{"id":"urn:n:1","type":"Annotation","motivation":"commenting",
	  "body":{"type":"TextualBody","value":"hand B begins here"},"target":"`+cid+`#xywh=0,0,10,10"}`)

	out := injectAnnotations(manifest, page, "https://h/inst/ms")
	if out == nil {
		t.Fatal("injectAnnotations returned nil for a valid v2 manifest")
	}
	if !strings.Contains(string(out), "hand B begins here") {
		t.Fatalf("v2 canvas did not get the annotation:\n%s", out)
	}
}

// End-to-end through the Handler with a v2 manifest + provenance +
// annotations.json on disk — the real preserved-library scenario
// (Gallica/Bodleian/e-codices are v2).
func TestServer_InjectsAnnotations_V2EndToEnd(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gallica.bnf.fr", "ms")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const cid = "https://gallica.bnf.fr/iiif/ark:/12148/x/canvas/f1"
	files := map[string]string{
		"manifest.json": `{"@context":"http://iiif.io/api/presentation/2/context.json","@type":"sc:Manifest",
		  "sequences":[{"@type":"sc:Sequence","canvases":[{"@id":"` + cid + `","@type":"sc:Canvas",
		  "width":10,"height":10,"images":[]}]}]}`,
		"provenance.json":  `{"manifest_url":"https://gallica.bnf.fr/iiif/ark:/12148/x/manifest.json","images":[]}`,
		"annotations.json": `{"type":"AnnotationPage","items":[{"id":"urn:n:1","type":"Annotation","motivation":"commenting","body":{"type":"TextualBody","value":"scribal correction"},"target":"` + cid + `#xywh=1,2,3,4"}]}`,
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	ts := httptest.NewServer(New(root).Handler())
	defer ts.Close()
	_, body, _ := viewerGet(t, ts, "/gallica.bnf.fr/ms/manifest.json")
	if !strings.Contains(body, "scribal correction") || !strings.Contains(body, `"annotations"`) {
		t.Fatalf("v2 annotation not injected end-to-end:\n%s", body)
	}
}

// No annotations.json → manifest served byte-for-byte as the rewrite left
// it (the feature must be zero-impact when unused).
func TestServer_NoAnnotationsNoInjection(t *testing.T) {
	ts := httptest.NewServer(New(filepath.Join("testdata", "bundle")).Handler())
	defer ts.Close()
	_, body, _ := viewerGet(t, ts, "/cookbook-v3/manifest.json")
	if strings.Contains(body, `"annotations"`) {
		t.Fatalf("manifest gained an annotations key with no annotations.json present:\n%s", body)
	}
}
