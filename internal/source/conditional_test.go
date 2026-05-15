package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
		_, _ = w.Write([]byte("manifest-body-v1"))
	}))
	defer srv.Close()

	store := NewMemoryConditionalStore()
	f := NewHTTPFetcher(WithHTTPClient(srv.Client()), WithConditionalStore(store))

	// First fetch: no validators, 200, body cached.
	body, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if string(body) != "manifest-body-v1" {
		t.Fatalf("first body = %q", body)
	}

	// Second fetch: sends If-None-Match, server replies 304, cached body returned.
	body, err = f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if string(body) != "manifest-body-v1" {
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
		_, _ = w.Write([]byte("lm-body"))
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
	if string(body) != "lm-body" {
		t.Fatalf("body = %q, want cached body on 304", body)
	}
	if sawIMS != lm {
		t.Fatalf("If-Modified-Since sent = %q, want %q", sawIMS, lm)
	}
}
