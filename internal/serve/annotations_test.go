package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// injectedRef pulls the per-canvas annotation endpoint URL Mirador will
// fetch out of a served manifest (the path+query, for viewerGet).
func injectedRef(t *testing.T, manifestBody string) string {
	t.Helper()
	k := strings.Index(manifestBody, "/annotations?canvas=")
	if k < 0 {
		t.Fatalf("no annotation reference injected:\n%s", manifestBody)
	}
	s := strings.LastIndex(manifestBody[:k], `"`) + 1
	e := strings.Index(manifestBody[k:], `"`) + k
	full := manifestBody[s:e]
	// strip scheme://host (keep the full /<dir>/annotations?… path+query)
	if i := strings.Index(full, "://"); i >= 0 {
		if j := strings.Index(full[i+3:], "/"); j >= 0 {
			return full[i+3+j:]
		}
	}
	return full
}

// Mirador loads annotations by FETCHING the AnnotationPage/AnnotationList
// by id (it ignores inline items/resources). So the served manifest must
// carry a *reference* to our per-canvas endpoint, and that endpoint must
// return the note. v3 → canvas.annotations (AnnotationPage, fmt=w3c).
func TestServer_InjectsAnnotationReference_V3(t *testing.T) {
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
	       "body":{"id":"https://inst.example/img.jpg","type":"Image","format":"image/jpeg"},
	       "target":"` + canvasID + `"}]}]}]}`
	prov := `{"manifest_url":"https://inst.example/iiif/ms/manifest.json","images":[]}`
	anns := `{"type":"AnnotationPage","items":[{"id":"urn:note:1","type":"Annotation",
	  "motivation":"commenting","body":{"type":"TextualBody","value":"a scholarly marginal note"},
	  "target":"` + canvasID + `#xywh=10,10,40,20"}]}`
	for n, c := range map[string]string{"manifest.json": manifest, "provenance.json": prov, "annotations.json": anns} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	ts := httptest.NewServer(New(root).Handler())
	defer ts.Close()

	_, body, _ := viewerGet(t, ts, "/inst/ms/manifest.json")
	// A reference, NOT the inline note text.
	if strings.Contains(body, "a scholarly marginal note") {
		t.Fatalf("note must be referenced, not inlined, into the manifest:\n%s", body)
	}
	var m struct {
		Items []struct {
			Items       []struct{ Type string } `json:"items"`
			Annotations []struct {
				Type, ID string
			} `json:"annotations"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("served manifest invalid: %v", err)
	}
	cv := m.Items[0]
	if len(cv.Items) != 1 || cv.Items[0].Type != "AnnotationPage" {
		t.Fatalf("existing painting AnnotationPage clobbered: %+v", cv.Items)
	}
	if len(cv.Annotations) != 1 || cv.Annotations[0].Type != "AnnotationPage" ||
		!strings.Contains(cv.Annotations[0].ID, "/annotations?canvas=") ||
		!strings.Contains(cv.Annotations[0].ID, "fmt=w3c") {
		t.Fatalf("canvas.annotations is not a w3c reference: %+v", cv.Annotations)
	}

	// Follow the reference exactly as Mirador would.
	_, ann, _ := viewerGet(t, ts, injectedRef(t, body))
	if !strings.Contains(ann, `"AnnotationPage"`) || !strings.Contains(ann, "a scholarly marginal note") {
		t.Fatalf("referenced endpoint did not return the W3C note:\n%s", ann)
	}
}

// v2 (the real library: Gallica/Bodleian/e-codices) → canvas.otherContent
// sc:AnnotationList reference; the fetched URL returns Open Annotation.
func TestServer_InjectsAnnotationReference_V2(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gallica.bnf.fr", "ms")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const cid = "https://gallica.bnf.fr/iiif/ark:/12148/x/canvas/f1"
	for n, c := range map[string]string{
		"manifest.json": `{"@context":"http://iiif.io/api/presentation/2/context.json","@type":"sc:Manifest",
		  "sequences":[{"@type":"sc:Sequence","canvases":[{"@id":"` + cid + `","@type":"sc:Canvas","width":10,"height":10,"images":[]}]}]}`,
		"provenance.json":  `{"manifest_url":"https://gallica.bnf.fr/iiif/ark:/12148/x/manifest.json","images":[]}`,
		"annotations.json": `{"type":"AnnotationPage","items":[{"id":"urn:n:1","type":"Annotation","motivation":"commenting","body":{"type":"TextualBody","value":"scribal correction"},"target":"` + cid + `#xywh=1,2,3,4"}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	ts := httptest.NewServer(New(root).Handler())
	defer ts.Close()

	_, body, _ := viewerGet(t, ts, "/gallica.bnf.fr/ms/manifest.json")
	if strings.Contains(body, "scribal correction") {
		t.Fatalf("v2 note must be referenced, not inlined:\n%s", body)
	}
	if !strings.Contains(body, "sc:AnnotationList") || !strings.Contains(body, "otherContent") ||
		!strings.Contains(body, "fmt=oa") {
		t.Fatalf("v2 otherContent reference missing:\n%s", body)
	}
	_, ann, _ := viewerGet(t, ts, injectedRef(t, body))
	for _, want := range []string{"sc:AnnotationList", "oa:Annotation", `"on"`, "scribal correction"} {
		if !strings.Contains(ann, want) {
			t.Fatalf("v2 referenced endpoint missing %q:\n%s", want, ann)
		}
	}
}

// C1: POST an annotation → it is stored beside the bundle and is then
// injected into the served manifest (the full author→store→display loop,
// offline, no source contact).
func TestServer_CreateAnnotation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "inst", "ms")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const cid = "https://inst.example/iiif/ms/canvas/p1"
	for n, c := range map[string]string{
		"manifest.json":   `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://inst.example/iiif/ms/manifest.json","type":"Manifest","items":[{"id":"` + cid + `","type":"Canvas","width":10,"height":10,"items":[]}]}`,
		"provenance.json": `{"manifest_url":"https://inst.example/iiif/ms/manifest.json","images":[]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	ts := httptest.NewServer(New(root).Handler())
	defer ts.Close()

	post := func(body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost,
			ts.URL+"/inst/ms/annotations", strings.NewReader(body))
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Malformed JSON and missing target are rejected.
	if code, _ := post(`not json`); code != http.StatusBadRequest {
		t.Fatalf("bad json = %d, want 400", code)
	}
	if code, _ := post(`{"type":"Annotation","body":{"type":"TextualBody","value":"x"}}`); code != http.StatusBadRequest {
		t.Fatalf("missing target = %d, want 400", code)
	}

	// A valid annotation is accepted...
	code, respBody := post(`{"type":"Annotation","motivation":"commenting","body":{"type":"TextualBody","value":"a created note"},"target":"` + cid + `#xywh=1,2,3,4"}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d (%s), want 201", code, respBody)
	}
	if !strings.Contains(respBody, `"id"`) {
		t.Fatalf("response should echo the stored annotation with an assigned id: %s", respBody)
	}

	// ...persisted to annotations.json...
	if _, err := os.Stat(filepath.Join(dir, "annotations.json")); err != nil {
		t.Fatalf("annotations.json not written: %v", err)
	}
	// ...and reachable via the reference Mirador fetches from the manifest.
	_, mbody, _ := viewerGet(t, ts, "/inst/ms/manifest.json")
	_, ann, _ := viewerGet(t, ts, injectedRef(t, mbody))
	if !strings.Contains(ann, "a created note") {
		t.Fatalf("created annotation not reachable via the injected reference:\nmanifest=%s\nann=%s", mbody, ann)
	}

	// POST to a path that is not a preserved bundle is refused.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost,
		ts.URL+"/not/a/bundle/annotations", strings.NewReader(`{"type":"Annotation","target":"x"}`))
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST nonbundle: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST to non-bundle = %d, want 404", resp.StatusCode)
	}
}

// Option A backend: the full REST surface a storage adapter needs —
// list (GET), create (POST), update (PUT), delete (DELETE).
func TestServer_AnnotationsREST(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "inst", "ms")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const cid = "https://inst.example/iiif/ms/canvas/p1"
	for n, c := range map[string]string{
		"manifest.json":   `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://inst.example/iiif/ms/manifest.json","type":"Manifest","items":[{"id":"` + cid + `","type":"Canvas","width":10,"height":10,"items":[]}]}`,
		"provenance.json": `{"manifest_url":"https://inst.example/iiif/ms/manifest.json","images":[]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	ts := httptest.NewServer(New(root).Handler())
	defer ts.Close()

	do := func(method, url, body string) (int, string) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, _ := http.NewRequestWithContext(t.Context(), method, ts.URL+url, rd)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, url, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Empty list initially: a well-formed AnnotationPage.
	code, list := do(http.MethodGet, "/inst/ms/annotations", "")
	if code != http.StatusOK || !strings.Contains(list, `"AnnotationPage"`) {
		t.Fatalf("GET empty list = %d %s", code, list)
	}

	// Create two.
	code, a1 := do(http.MethodPost, "/inst/ms/annotations",
		`{"type":"Annotation","motivation":"commenting","body":{"type":"TextualBody","value":"one"},"target":"`+cid+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST = %d %s", code, a1)
	}
	var created struct{ ID string }
	if err := json.Unmarshal([]byte(a1), &created); err != nil || created.ID == "" {
		t.Fatalf("POST response has no id: %s", a1)
	}
	do(http.MethodPost, "/inst/ms/annotations",
		`{"type":"Annotation","motivation":"tagging","body":{"type":"TextualBody","value":"rubric"},"target":"`+cid+`"}`)

	code, list = do(http.MethodGet, "/inst/ms/annotations", "")
	if code != http.StatusOK || strings.Count(list, `"target"`) != 2 {
		t.Fatalf("GET list after 2 creates = %d, want 2 items: %s", code, list)
	}

	// Update the first.
	code, _ = do(http.MethodPut, "/inst/ms/annotations",
		`{"id":"`+created.ID+`","type":"Annotation","motivation":"commenting","body":{"type":"TextualBody","value":"one edited"},"target":"`+cid+`"}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	_, list = do(http.MethodGet, "/inst/ms/annotations", "")
	if !strings.Contains(list, "one edited") {
		t.Fatalf("PUT did not persist: %s", list)
	}
	// PUT an unknown id → 404.
	if code, _ := do(http.MethodPut, "/inst/ms/annotations", `{"id":"urn:nope","type":"Annotation","target":"`+cid+`"}`); code != http.StatusNotFound {
		t.Fatalf("PUT unknown = %d, want 404", code)
	}

	// Delete the first; absent → 404.
	code, _ = do(http.MethodDelete, "/inst/ms/annotations?id="+created.ID, "")
	if code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", code)
	}
	if code, _ := do(http.MethodDelete, "/inst/ms/annotations?id="+created.ID, ""); code != http.StatusNotFound {
		t.Fatalf("DELETE absent = %d, want 404", code)
	}
	_, list = do(http.MethodGet, "/inst/ms/annotations", "")
	if strings.Count(list, `"target"`) != 1 {
		t.Fatalf("after delete want 1 item: %s", list)
	}

	// Unsupported method → 405.
	if code, _ := do(http.MethodPatch, "/inst/ms/annotations", "{}"); code != http.StatusMethodNotAllowed {
		t.Fatalf("PATCH = %d, want 405", code)
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
