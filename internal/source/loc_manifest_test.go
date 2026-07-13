package source

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

type locScriptFetcher struct {
	bodies map[string][]byte
	errs   map[string]error
	calls  []string
}

func (f *locScriptFetcher) Fetch(_ context.Context, rawURL string) ([]byte, error) {
	f.calls = append(f.calls, rawURL)
	if err := f.errs[rawURL]; err != nil {
		return nil, err
	}
	return f.bodies[rawURL], nil
}

func TestLOCManifestFetcherFallsBackToOfficialItemJSON(t *testing.T) {
	const (
		manifestURL = "https://www.loc.gov/item/0027938281A-ms/manifest.json"
		itemURL     = "https://www.loc.gov/item/0027938281A-ms/?fo=json"
	)
	item := []byte(`{
		"item":{"title":"Greek Manuscripts 1447. Triodion.","date":"1500","language":["greek, ancient (to 1453)"]},
		"resources":[{"files":[[
			{"url":"https://tile.loc.gov/image-services/iiif/service:amed:ms:0001/full/pct:100/0/default.jpg","mimetype":"image/jpeg","width":4731,"height":3437},
			{"url":"https://tile.loc.gov/storage-services/service/amed/ms/0001.jp2","mimetype":"image/jp2","width":4731,"height":3437,"info":"https://tile.loc.gov/image-services/iiif/service:amed:ms:0001/info.json"}
		]]}]
	}`)
	inner := &locScriptFetcher{
		bodies: map[string][]byte{itemURL: item},
		errs: map[string]error{manifestURL: &HTTPStatusError{
			Code: http.StatusForbidden, URL: manifestURL,
		}},
	}

	fetcher := NewLOCManifestFetcher(inner)
	body, err := fetcher.Fetch(context.Background(), manifestURL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if want := []string{manifestURL, itemURL}; !reflect.DeepEqual(inner.calls, want) {
		t.Fatalf("calls = %v, want friendly two-step %v", inner.calls, want)
	}

	var got struct {
		ID    string              `json:"id"`
		Label map[string][]string `json:"label"`
		Items []struct {
			Width int `json:"width"`
			Items []struct {
				Items []struct {
					Body struct {
						ID      string `json:"id"`
						Service []struct {
							ID string `json:"id"`
						} `json:"service"`
					} `json:"body"`
				} `json:"items"`
			} `json:"items"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("generated manifest is not JSON: %v", err)
	}
	if got.ID != manifestURL || got.Label["en"][0] != "Greek Manuscripts 1447. Triodion." || len(got.Items) != 1 {
		t.Fatalf("generated manifest identity = %+v", got)
	}
	page := got.Items[0]
	image := page.Items[0].Items[0].Body
	if page.Width != 4731 || image.ID != "https://tile.loc.gov/image-services/iiif/service:amed:ms:0001/full/pct:100/0/default.jpg" {
		t.Fatalf("generated page/image = %+v / %+v", page, image)
	}
	if len(image.Service) != 1 || image.Service[0].ID != "https://tile.loc.gov/image-services/iiif/service:amed:ms:0001" {
		t.Fatalf("generated service = %+v", image.Service)
	}
	derivation, ok := fetcher.ManifestDerivation(manifestURL, body)
	wantDigest := sha256.Sum256(item)
	if !ok || derivation.Method != "loc-item-api-fallback" || derivation.SourceURL != itemURL ||
		derivation.SourceSHA256 != fmt.Sprintf("%x", wantDigest[:]) {
		t.Fatalf("derivation = %+v, %v", derivation, ok)
	}
	if _, stale := fetcher.ManifestDerivation(manifestURL, append(body, '\n')); stale {
		t.Fatal("changed manifest bytes inherited stale derivation metadata")
	}
}

func TestLOCManifestFetcherDoesNotMaskOtherFailuresOrHosts(t *testing.T) {
	const loc = "https://www.loc.gov/item/x/manifest.json"
	for _, tc := range []struct {
		name string
		url  string
		err  error
	}{
		{"non LOC passthrough", "https://example.org/manifest.json", errors.New("boom")},
		{"LOC non-403 passthrough", loc, &HTTPStatusError{Code: http.StatusNotFound, URL: loc}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := &locScriptFetcher{errs: map[string]error{tc.url: tc.err}}
			_, err := NewLOCManifestFetcher(inner).Fetch(context.Background(), tc.url)
			if !errors.Is(err, tc.err) {
				t.Fatalf("error = %v, want original %v", err, tc.err)
			}
			if len(inner.calls) != 1 {
				t.Fatalf("calls = %v, want no fallback", inner.calls)
			}
		})
	}
}
