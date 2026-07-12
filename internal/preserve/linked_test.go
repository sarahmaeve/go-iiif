package preserve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/source"
)

func TestDiscoverLinkedResourcesV2AndV3(t *testing.T) {
	document := []byte(`{
	  "otherContent":[{"@id":"https://example.org/list/1","@type":"sc:AnnotationList"}],
	  "annotations":[
	    {"id":"https://example.org/page/1","type":"AnnotationPage"},
	    {"id":"https://example.org/page/embedded","type":"AnnotationPage","items":[
	      {"type":"Annotation","motivation":"supplementing","body":{"id":"https://example.org/ocr.txt","type":"Text","format":"text/plain"}},
	      {"type":"Annotation","motivation":"supplementing","body":{"type":"Choice","items":[
	        {"type":"SpecificResource","source":{"id":"https://example.org/translation.txt","type":"Text","format":"text/plain"}},
	        {"type":"TextualBody","value":"inline alternative"}
	      ]}}
	    ]}
	  ],
	  "painting":{"type":"Annotation","motivation":"painting","body":{"id":"https://example.org/image.jpg","type":"Image","format":"image/jpeg"}},
	  "structures":[{"id":"https://example.org/range/1","type":"Range","supplementary":{"id":"https://example.org/annotations/transcription","type":"AnnotationCollection"}}],
	  "seeAlso":[{"id":"https://example.org/ocr.xml","type":"Dataset","format":"application/alto+xml"}]
	}`)
	got, err := DiscoverLinkedResources(document)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("resources = %+v, want six external references", got)
	}
	joined := fmt.Sprint(got)
	for _, want := range []string{"/list/1", "/page/1", "/ocr.txt", "/translation.txt", "/annotations/transcription", "/ocr.xml"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("resources = %+v, missing %s", got, want)
		}
	}
	if strings.Contains(joined, "embedded") {
		t.Fatalf("embedded AnnotationPage should not be fetched: %+v", got)
	}
	if strings.Contains(joined, "image.jpg") {
		t.Fatalf("painting image leaked into linked-resource inventory: %+v", got)
	}
}

func TestPreserveFollowsAnnotationCollectionPages(t *testing.T) {
	const manifestURL = "https://example.org/manifest"
	const collectionURL = "https://example.org/annotations/all"
	const page1URL = "https://example.org/annotations/page1"
	const page2URL = "https://example.org/annotations/page2"
	manifest := []byte(`{"id":"https://example.org/manifest","type":"Manifest","items":[],"structures":[{"type":"Range","supplementary":{"id":"https://example.org/annotations/all","type":"AnnotationCollection"}}]}`)
	collection := []byte(`{"id":"https://example.org/annotations/all","type":"AnnotationCollection","first":{"id":"https://example.org/annotations/page1","type":"AnnotationPage"}}`)
	page1 := []byte(`{"id":"https://example.org/annotations/page1","type":"AnnotationPage","items":[],"next":{"id":"https://example.org/annotations/page2","type":"AnnotationPage"}}`)
	page2 := []byte(`{"id":"https://example.org/annotations/page2","type":"AnnotationPage","items":[]}`)
	fetcher := &linkedFetcher{bodies: map[string][]byte{collectionURL: collection, page1URL: page1, page2URL: page2}}
	store := NewLocalBlobStore(t.TempDir())
	sum, err := Preserve(t.Context(), fetcher, store, manifestURL, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if sum.LinkedStored != 3 || len(sum.LinkedFailures) != 0 {
		t.Fatalf("summary = %+v, want collection and two pages", sum)
	}
}

type linkedFetcher struct {
	bodies map[string][]byte
	calls  []string
}

func (f *linkedFetcher) Fetch(_ context.Context, rawURL string) ([]byte, error) {
	f.calls = append(f.calls, rawURL)
	if body, ok := f.bodies[rawURL]; ok {
		return body, nil
	}
	return nil, source.ErrNotFound
}

func TestPreserveLinkedAnnotationsOCRAndSeeAlso(t *testing.T) {
	const manifestURL = "https://example.org/manifest"
	const pageURL = "https://example.org/annotations/page1"
	const textURL = "https://example.org/ocr/page1.txt"
	const altoURL = "https://example.org/ocr/book.xml"
	manifest := []byte(`{
	  "id":"https://example.org/manifest","type":"Manifest",
	  "items":[],
	  "annotations":[{"id":"https://example.org/annotations/page1","type":"AnnotationPage"}],
	  "seeAlso":[{"id":"https://example.org/ocr/book.xml","type":"Dataset","format":"application/alto+xml"}]
	}`)
	page := []byte(`{
	  "id":"https://example.org/annotations/page1","type":"AnnotationPage",
	  "items":[{"type":"Annotation","motivation":"supplementing","body":{"id":"https://example.org/ocr/page1.txt","type":"Text","format":"text/plain"}}]
	}`)
	fetcher := &linkedFetcher{bodies: map[string][]byte{
		pageURL: page, textURL: []byte("recognized text"), altoURL: []byte("<alto/>")},
	}
	store := NewLocalBlobStore(t.TempDir())
	sum, err := Preserve(t.Context(), fetcher, store, manifestURL, manifest)
	if err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	if sum.Linked != 3 || sum.LinkedStored != 3 || len(sum.LinkedFailures) != 0 {
		t.Fatalf("summary = %+v, want three linked resources stored", sum)
	}

	provBytes, err := store.Get(t.Context(), dirFor(manifestURL)+"/provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var prov provenance
	if err := json.Unmarshal(provBytes, &prov); err != nil || len(prov.LinkedResources) != 3 {
		t.Fatalf("provenance = %+v, %v", prov, err)
	}
	for _, resource := range prov.LinkedResources {
		if resource.SHA256 == "" {
			t.Fatalf("resource lacks checksum: %+v", resource)
		}
		if ok, _ := store.Exists(t.Context(), dirFor(manifestURL)+"/"+resource.File); !ok {
			t.Fatalf("resource file missing: %+v", resource)
		}
	}

	fetcher.calls = nil
	second, err := Preserve(t.Context(), fetcher, store, manifestURL, manifest)
	if err != nil {
		t.Fatalf("second Preserve: %v", err)
	}
	if len(fetcher.calls) != 0 || second.LinkedSkipped != 3 {
		t.Fatalf("second calls=%v summary=%+v; linked resources should be reused", fetcher.calls, second)
	}
}

func TestLinkedFailureDoesNotSuppressImageBundle(t *testing.T) {
	const manifestURL = "https://example.org/manifest"
	manifest := []byte(`{"id":"https://example.org/manifest","type":"Manifest","items":[],"annotations":[{"id":"https://example.org/missing","type":"AnnotationPage"}]}`)
	store := NewLocalBlobStore(t.TempDir())
	sum, err := Preserve(t.Context(), &linkedFetcher{}, store, manifestURL, manifest)
	if err != nil {
		t.Fatalf("linked-resource failure must remain non-fatal: %v", err)
	}
	if len(sum.LinkedFailures) != 1 {
		t.Fatalf("summary = %+v, want one linked warning", sum)
	}
	provBytes, err := store.Get(t.Context(), dirFor(manifestURL)+"/provenance.json")
	if err != nil {
		t.Fatalf("completed bundle lacks provenance: %v", err)
	}
	if !strings.Contains(string(provBytes), "linked_failures") {
		t.Fatalf("unresolved link not recorded: %s", provBytes)
	}
}

func TestMalformedAnnotationJSONIsNotActivated(t *testing.T) {
	const manifestURL = "https://example.org/manifest"
	const pageURL = "https://example.org/bad-page"
	manifest := []byte(`{"id":"https://example.org/manifest","type":"Manifest","items":[],"annotations":[{"id":"https://example.org/bad-page","type":"AnnotationPage"}]}`)
	store := NewLocalBlobStore(t.TempDir())
	fetcher := &linkedFetcher{bodies: map[string][]byte{pageURL: []byte("not json")}}
	sum, err := Preserve(t.Context(), fetcher, store, manifestURL, manifest)
	if err != nil {
		t.Fatalf("malformed optional annotation should warn, not suppress the bundle: %v", err)
	}
	if len(sum.LinkedFailures) != 1 || !strings.Contains(sum.LinkedFailures[0], "not valid JSON") {
		t.Fatalf("summary = %+v, want malformed annotation warning", sum)
	}
	entry := linkedProvenanceEntry(LinkedResource{URL: pageURL, Kind: linkedAnnotation})
	if ok, _ := store.Exists(t.Context(), dirFor(manifestURL)+"/"+entry.File); ok {
		t.Fatalf("malformed annotation was stored at %s", entry.File)
	}
	provBytes, _ := store.Get(t.Context(), dirFor(manifestURL)+"/provenance.json")
	var prov provenance
	_ = json.Unmarshal(provBytes, &prov)
	if len(prov.LinkedResources) != 0 || len(prov.LinkedFailures) != 1 {
		t.Fatalf("malformed annotation provenance = %+v", prov)
	}
}

type cancelLinkedFetcher struct {
	cancel context.CancelFunc
}

func (f cancelLinkedFetcher) Fetch(_ context.Context, _ string) ([]byte, error) {
	f.cancel()
	return nil, context.Canceled
}

func TestLinkedCancellationKeepsCommittedSnapshot(t *testing.T) {
	const manifestURL = "https://example.org/manifest"
	store := NewLocalBlobStore(t.TempDir())
	original := []byte(`{"id":"https://example.org/manifest","type":"Manifest","items":[]}`)
	if _, err := Preserve(t.Context(), &linkedFetcher{}, store, manifestURL, original); err != nil {
		t.Fatal(err)
	}
	dir := dirFor(manifestURL)
	prior, _ := store.Get(t.Context(), dir+"/provenance.json")
	refresh := []byte(`{"id":"https://example.org/manifest","type":"Manifest","items":[],"annotations":[{"id":"https://example.org/page","type":"AnnotationPage"}]}`)
	ctx, cancel := context.WithCancel(t.Context())
	_, err := Preserve(ctx, cancelLinkedFetcher{cancel: cancel}, store, manifestURL, refresh)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Preserve error = %v, want cancellation", err)
	}
	got, readErr := store.Get(t.Context(), dir+"/provenance.json")
	if readErr != nil || string(got) != string(prior) {
		t.Fatalf("cancellation changed committed provenance: %v", readErr)
	}
}

type stageThenCancelFetcher struct {
	cancel context.CancelFunc
	first  string
	body   []byte
	calls  []string
}

func (f *stageThenCancelFetcher) Fetch(_ context.Context, rawURL string) ([]byte, error) {
	f.calls = append(f.calls, rawURL)
	if rawURL == f.first {
		f.cancel()
		return f.body, nil
	}
	return nil, source.ErrNotFound
}

func TestInterruptedBackfillReusesUncommittedStagedResource(t *testing.T) {
	const manifestURL = "https://example.org/manifest"
	const page1 = "https://example.org/page/1"
	const page2 = "https://example.org/page/2"
	store := NewLocalBlobStore(t.TempDir())
	original := []byte(`{"id":"https://example.org/manifest","type":"Manifest","items":[]}`)
	if _, err := Preserve(t.Context(), &linkedFetcher{}, store, manifestURL, original); err != nil {
		t.Fatal(err)
	}
	refresh := []byte(`{"id":"https://example.org/manifest","type":"Manifest","items":[],"annotations":[
	  {"id":"https://example.org/page/1","type":"AnnotationPage"},
	  {"id":"https://example.org/page/2","type":"AnnotationPage"}
	]}`)
	pageBody := []byte(`{"id":"page","type":"AnnotationPage","items":[]}`)
	ctx, cancel := context.WithCancel(t.Context())
	first := &stageThenCancelFetcher{cancel: cancel, first: page1, body: pageBody}
	if _, err := Preserve(ctx, first, store, manifestURL, refresh); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted Preserve error = %v, want cancellation", err)
	}
	if len(first.calls) != 1 || first.calls[0] != page1 {
		t.Fatalf("interrupted calls = %v", first.calls)
	}

	second := &linkedFetcher{bodies: map[string][]byte{page2: pageBody}}
	sum, err := Preserve(t.Context(), second, store, manifestURL, refresh)
	if err != nil {
		t.Fatalf("resumed Preserve: %v", err)
	}
	if len(second.calls) != 1 || second.calls[0] != page2 || sum.LinkedSkipped != 1 || sum.LinkedStored != 1 {
		t.Fatalf("resumed calls=%v summary=%+v; staged page1 should be reused", second.calls, sum)
	}
}
