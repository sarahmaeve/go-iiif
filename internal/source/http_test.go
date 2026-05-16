package source

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPFetcher_RejectsNonHTTPS(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))
	defer srv.Close() // srv.URL is plain http://

	f := NewHTTPFetcher(WithHTTPClient(srv.Client()))

	for _, url := range []string{srv.URL, "ftp://example.org/x", "example.org/no-scheme"} {
		if _, err := f.Fetch(context.Background(), url); err == nil {
			t.Fatalf("Fetch(%q) = nil error, want non-HTTPS rejected", url)
		}
	}
	if reached {
		t.Fatal("a non-HTTPS request was actually sent; it must be rejected before dialing")
	}
}

func TestHTTPFetcher_StatusHandling(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantErrIs  error
		wantErrSub string
	}{
		{name: "404 maps to ErrNotFound", status: http.StatusNotFound, wantErrIs: ErrNotFound},
		{name: "500 is a non-nil error mentioning status", status: http.StatusInternalServerError, wantErrSub: "500"},
		{name: "403 is a non-nil error mentioning status", status: http.StatusForbidden, wantErrSub: "403"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			f := NewHTTPFetcher(WithHTTPClient(srv.Client()))
			_, err := f.Fetch(context.Background(), srv.URL)
			if err == nil {
				t.Fatalf("Fetch on %d = nil error, want error", tt.status)
			}
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Fatalf("error = %v, want errors.Is %v", err, tt.wantErrIs)
			}
			if tt.wantErrSub != "" && !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.wantErrSub)
			}
		})
	}
}

func TestHTTPFetcher_PerHostUserAgent(t *testing.T) {
	f := NewHTTPFetcher()

	// Default / Anubis-protected hosts (e.g. Bodleian): honest, no
	// "Mozilla" — so Anubis scores it weight 0 and ALLOWs it instead of
	// issuing a JS proof-of-work challenge we cannot solve.
	bod := f.uaFor("digital.bodleian.ox.ac.uk")
	if strings.Contains(bod, "Mozilla") {
		t.Fatalf("Bodleian UA must not spoof a browser: %q", bod)
	}
	if !strings.Contains(bod, "iiif-preserve") {
		t.Fatalf("default UA should identify the tool: %q", bod)
	}

	// Gallica/BnF 403s non-browser UAs, so it (and only it) gets a
	// browser-like override.
	if g := f.uaFor("gallica.bnf.fr"); !strings.Contains(g, "Mozilla") {
		t.Fatalf("Gallica UA must be browser-like: %q", g)
	}
}

func TestHTTPFetcher_RejectsHTMLInterstitial(t *testing.T) {
	cases := []struct {
		name, ct, body string
		wantErr        bool
	}{
		{"anubis challenge 200/html", "text/html; charset=utf-8",
			"<!DOCTYPE html><title>Making sure you're not a bot</title>", true},
		{"html under a generic content-type", "application/octet-stream",
			"<!doctype html><html><body>nope</body></html>", true},
		{"real manifest json passes", "application/json",
			`{"@type":"sc:Manifest"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", c.ct)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()

			f := NewHTTPFetcher(WithHTTPClient(srv.Client()))
			body, err := f.Fetch(context.Background(), srv.URL)
			if c.wantErr {
				if !errors.Is(err, ErrNonResource) {
					t.Fatalf("err = %v, want ErrNonResource", err)
				}
				if body != nil {
					t.Fatalf("body = %q, want nil when rejected", body)
				}
				return
			}
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
		})
	}
}

func TestHTTPFetcher_FetchOverTLSSendsUserAgent(t *testing.T) {
	tests := []struct {
		name    string
		opts    []Option
		wantUA  string
		wantSub string // substring expected in the default UA
	}{
		{
			name:    "default user-agent is honest (identifies the tool, not a spoofed browser)",
			opts:    nil,
			wantSub: "iiif-preserve",
		},
		{
			name:   "user-agent is overridable",
			opts:   []Option{WithUserAgent("go-iiif-preservation/0.1 (+research)")},
			wantUA: "go-iiif-preservation/0.1 (+research)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotUA string
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotUA = r.Header.Get("User-Agent")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()

			opts := append([]Option{WithHTTPClient(srv.Client())}, tt.opts...)
			f := NewHTTPFetcher(opts...)

			body, err := f.Fetch(context.Background(), srv.URL)
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if string(body) != `{"ok":true}` {
				t.Fatalf("body = %q, want %q", body, `{"ok":true}`)
			}
			if strings.HasPrefix(gotUA, "Go-http-client") || gotUA == "" {
				t.Fatalf("User-Agent = %q, want a non-default agent", gotUA)
			}
			if tt.wantUA != "" && gotUA != tt.wantUA {
				t.Fatalf("User-Agent = %q, want %q", gotUA, tt.wantUA)
			}
			if tt.wantSub != "" && !strings.Contains(gotUA, tt.wantSub) {
				t.Fatalf("User-Agent = %q, want it to contain %q", gotUA, tt.wantSub)
			}
		})
	}
}
