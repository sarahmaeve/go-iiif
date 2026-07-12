package source

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFileConditionalStorePersistsVersionedBoundedEntry(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileConditionalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	const rawURL = "https://example.org/manifest/1"
	want := CacheEntry{ETag: `"v1"`, LastModified: "yesterday", ContentType: "application/ld+json", Body: []byte(`{"id":"one"}`)}
	if err := store.Put(rawURL, want); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewFileConditionalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := reopened.Get(rawURL)
	if err != nil || !ok {
		t.Fatalf("Get = %+v, %v, %v", got, ok, err)
	}
	if got.ETag != want.ETag || got.LastModified != want.LastModified || got.ContentType != want.ContentType || string(got.Body) != string(want.Body) {
		t.Fatalf("entry = %+v, want %+v", got, want)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("cache files = %v, %v", files, err)
	}
	b, err := os.ReadFile(files[0])
	if err != nil || !bytes.Contains(b, []byte(`"version":1`)) {
		t.Fatalf("cache record lacks version: %q, %v", b, err)
	}

	if err := reopened.Put("https://example.org/too-large", CacheEntry{Body: make([]byte, maxConditionalBodyBytes+1)}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reopened.Get("https://example.org/too-large"); err != nil || ok {
		t.Fatalf("oversized body cached: ok=%v err=%v", ok, err)
	}
}

func TestHTTPFetcher_ConditionalGET_ETag(t *testing.T) {
	var sawINM string
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		sawINM = r.Header.Get("If-None-Match")
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "application/ld+json")
		_, _ = w.Write([]byte(`{"id":"manifest-body-v1"}`))
	}))
	defer srv.Close()

	store := NewMemoryConditionalStore()
	f := NewHTTPFetcher(WithHTTPClient(srv.Client()), WithConditionalStore(store))

	// First fetch: no validators, 200, body cached.
	body, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if string(body) != `{"id":"manifest-body-v1"}` {
		t.Fatalf("first body = %q", body)
	}

	// Second fetch: sends If-None-Match, server replies 304, cached body returned.
	body, err = f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if string(body) != `{"id":"manifest-body-v1"}` {
		t.Fatalf("second body = %q, want cached body on 304", body)
	}
	if sawINM != `"v1"` {
		t.Fatalf("If-None-Match sent = %q, want %q", sawINM, `"v1"`)
	}
	if hits != 2 {
		t.Fatalf("server hits = %d, want 2", hits)
	}
}

func TestHTTPFetcher_ConditionalGET_LastModified(t *testing.T) {
	const lm = "Wed, 21 Oct 2015 07:28:00 GMT"
	var sawIMS string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawIMS = r.Header.Get("If-Modified-Since")
		if r.Header.Get("If-Modified-Since") == lm {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Last-Modified", lm)
		_, _ = w.Write([]byte(`{"id":"lm-body"}`))
	}))
	defer srv.Close()

	store := NewMemoryConditionalStore()
	f := NewHTTPFetcher(WithHTTPClient(srv.Client()), WithConditionalStore(store))

	if _, err := f.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	body, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if string(body) != `{"id":"lm-body"}` {
		t.Fatalf("body = %q, want cached body on 304", body)
	}
	if sawIMS != lm {
		t.Fatalf("If-Modified-Since sent = %q, want %q", sawIMS, lm)
	}
}

func TestHTTPFetcher_ConditionalGETSurvivesFileStoreReopen(t *testing.T) {
	var validators []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		validators = append(validators, r.Header.Get("If-None-Match"))
		if r.Header.Get("If-None-Match") == `"durable"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"durable"`)
		_, _ = w.Write([]byte(`{"type":"Manifest"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	firstStore, err := NewFileConditionalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := NewHTTPFetcher(WithHTTPClient(srv.Client()), WithConditionalStore(firstStore))
	if _, err := first.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	secondStore, err := NewFileConditionalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	second := NewHTTPFetcher(WithHTTPClient(srv.Client()), WithConditionalStore(secondStore))
	body, err := second.Fetch(context.Background(), srv.URL)
	if err != nil || string(body) != `{"type":"Manifest"}` {
		t.Fatalf("reopened fetch = %q, %v", body, err)
	}
	if len(validators) != 2 || validators[0] != "" || validators[1] != `"durable"` {
		t.Fatalf("validators = %v", validators)
	}
}

func TestHTTPFetcher_DoesNotConditionallyCacheImageBodies(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("ETag", `"image"`)
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	}))
	defer srv.Close()
	store := NewMemoryConditionalStore()
	fetcher := NewHTTPFetcher(WithHTTPClient(srv.Client()), WithConditionalStore(store))
	if _, err := fetcher.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Get(srv.URL); err != nil || ok {
		t.Fatalf("image response entered conditional cache: ok=%v err=%v", ok, err)
	}
}
