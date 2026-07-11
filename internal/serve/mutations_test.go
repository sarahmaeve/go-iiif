package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerMutationGuard(t *testing.T) {
	root := writeNestedBundle(t)
	srv := New(root)
	srv.enforceLocalMutations = true

	do := func(host, origin, fetchSite string) int {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, catalogRefreshRoute, nil)
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			req.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}

	if got := do("evil.example", "", ""); got != http.StatusForbidden {
		t.Fatalf("foreign Host = %d, want 403", got)
	}
	if got := do("127.0.0.1:8443", "https://evil.example", "cross-site"); got != http.StatusForbidden {
		t.Fatalf("foreign browser origin = %d, want 403", got)
	}
	if got := do("127.0.0.1:8443", "http://127.0.0.1:8443", "same-origin"); got != http.StatusSeeOther {
		t.Fatalf("same-origin mutation = %d, want 303", got)
	}
	if got := do("localhost:8443", "", ""); got != http.StatusSeeOther {
		t.Fatalf("local command-line mutation = %d, want 303", got)
	}
}

func TestRequestHostname(t *testing.T) {
	for input, want := range map[string]string{
		"127.0.0.1:8443": "127.0.0.1",
		"localhost:8443": "localhost",
		"[::1]:8443":     "::1",
	} {
		if got := requestHostname(input); got != want {
			t.Errorf("requestHostname(%q) = %q, want %q", input, got, want)
		}
	}
}
