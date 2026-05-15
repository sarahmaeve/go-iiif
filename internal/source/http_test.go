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

func TestHTTPFetcher_FetchOverTLSSendsUserAgent(t *testing.T) {
	tests := []struct {
		name    string
		opts    []Option
		wantUA  string
		wantSub string // substring expected in the default UA
	}{
		{
			name:    "default user-agent is browser-like, not Go default",
			opts:    nil,
			wantSub: "Mozilla",
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
