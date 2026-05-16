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

// The embedded viewer is Mirador + MAE; MAE's storage adapter loads
// annotations from /<dir>/annotations and dispatches them for display
// itself. The served manifest must therefore carry NO annotation
// reference — injecting one would make Mirador core fetch the same page
// independently and every stored annotation would render twice (the
// reported "shown double" bug). The REST endpoint stays the single
// source of truth; the manifest is left to the image rewrite only.
func TestServer_ManifestNotAnnotationInjected(t *testing.T) {
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
	// No second load path: no injected endpoint reference, no v2
	// otherContent list, and the painting AnnotationPage left intact.
	for _, banned := range []string{"/annotations?canvas=", "otherContent", "sc:AnnotationList"} {
		if strings.Contains(body, banned) {
			t.Fatalf("served manifest still injects an annotation path (%q) — Mirador core would double-load:\n%s", banned, body)
		}
	}
	var m struct {
		Items []struct {
			Items       []struct{ Type string } `json:"items"`
			Annotations []json.RawMessage       `json:"annotations"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("served manifest invalid: %v", err)
	}
	cv := m.Items[0]
	if len(cv.Items) != 1 || cv.Items[0].Type != "AnnotationPage" {
		t.Fatalf("existing painting AnnotationPage altered: %+v", cv.Items)
	}
	if len(cv.Annotations) != 0 {
		t.Fatalf("canvas.annotations must be absent (MAE adapter is the load path): %+v", cv.Annotations)
	}

	// The single source of truth — the REST endpoint MAE's adapter calls —
	// still returns the stored note.
	_, ann, _ := viewerGet(t, ts, "/inst/ms/annotations?canvas="+canvasID)
	if !strings.Contains(ann, `"AnnotationPage"`) || !strings.Contains(ann, "a scholarly marginal note") {
		t.Fatalf("annotation endpoint did not return the W3C note:\n%s", ann)
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
	// ...and reachable via the REST endpoint MAE's storage adapter loads
	// from (the single source of truth; the manifest is not injected).
	_, ann, _ := viewerGet(t, ts, "/inst/ms/annotations?canvas="+cid)
	if !strings.Contains(ann, "a created note") {
		t.Fatalf("created annotation not reachable via the annotation endpoint:\n%s", ann)
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

// TestServer_MAEAdapterCanvasContract pins the exact server contract the
// vendored MAE storage adapter (viewer-src/src/adapter.js
// HttpAnnotationAdapter) depends on: a region annotation in MAE's
// SpecificResource/FragmentSelector shape POSTs successfully, and
// `all()` — GET /<dir>/annotations?canvas=<id> — returns a well-formed
// AnnotationPage whose `items` is filtered to that canvas with the region
// selector preserved. If this breaks, MAE annotations silently never
// surface in the viewer; a Go test catches it without a browser.
func TestServer_MAEAdapterCanvasContract(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "inst", "ms")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const cidA = "https://inst.example/iiif/ms/canvas/p1"
	const cidB = "https://inst.example/iiif/ms/canvas/p2"
	for n, c := range map[string]string{
		"manifest.json":   `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://inst.example/iiif/ms/manifest.json","type":"Manifest","items":[{"id":"` + cidA + `","type":"Canvas","width":10,"height":10,"items":[]},{"id":"` + cidB + `","type":"Canvas","width":10,"height":10,"items":[]}]}`,
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

	// MAE's region output: a SpecificResource target with a
	// FragmentSelector (what point-and-drag produces), one per canvas.
	regionA := `{"type":"Annotation","motivation":"highlighting","target":{"source":"` + cidA + `","selector":{"type":"FragmentSelector","conformsTo":"http://www.w3.org/TR/media-frags/","value":"xywh=2,3,4,5"}}}`
	if code, body := do(http.MethodPost, "/inst/ms/annotations", regionA); code != http.StatusCreated {
		t.Fatalf("POST region A = %d %s", code, body)
	}
	if code, _ := do(http.MethodPost, "/inst/ms/annotations",
		`{"type":"Annotation","motivation":"commenting","body":{"type":"TextualBody","value":"on p2"},"target":"`+cidB+`"}`); code != http.StatusCreated {
		t.Fatalf("POST B not created")
	}

	// adapter.all() for canvas A: AnnotationPage, items filtered to A,
	// region selector intact.
	code, page := do(http.MethodGet, "/inst/ms/annotations?canvas="+cidA, "")
	if code != http.StatusOK {
		t.Fatalf("GET ?canvas=A = %d", code)
	}
	var got struct {
		Type  string `json:"type"`
		Items []struct {
			Target json.RawMessage `json:"target"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(page), &got); err != nil {
		t.Fatalf("adapter all() body not a JSON AnnotationPage: %v\n%s", err, page)
	}
	if got.Type != "AnnotationPage" {
		t.Fatalf("adapter all() type = %q, want AnnotationPage", got.Type)
	}
	if len(got.Items) != 1 {
		t.Fatalf("?canvas=A returned %d items, want 1 (B must be excluded): %s", len(got.Items), page)
	}
	if !strings.Contains(string(got.Items[0].Target), "xywh=2,3,4,5") ||
		!strings.Contains(string(got.Items[0].Target), "FragmentSelector") {
		t.Fatalf("region selector not preserved through round-trip: %s", got.Items[0].Target)
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
