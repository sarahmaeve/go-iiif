package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sarahmaeve/go-iiif/internal/institution"
)

// ErrInsecureURL is returned when a URL is not https. The crawler refuses
// plaintext rather than silently upgrading, so misconfiguration is explicit.
var ErrInsecureURL = errors.New("source: non-https URL refused")

// ErrNonResource is returned when a 2xx response is an HTML page rather than
// a IIIF resource — typically a bot-wall interstitial (e.g. Anubis, which
// deliberately answers challenges with HTTP 200) or an error page. A
// preservation tool must never silently archive such a page in place of the
// manifest/image it asked for.
var ErrNonResource = errors.New("source: response is an HTML page, not a IIIF resource (bot challenge or error?)")

// ErrResponseTooLarge is returned when a response body exceeds
// maxResponseBytes. A malicious or broken server streaming an unbounded
// body would otherwise exhaust process memory; we refuse rather than buffer
// it. The cap is generous enough for full-resolution preservation images.
var ErrResponseTooLarge = errors.New("source: response body exceeds size limit")

// maxResponseBytes bounds a single fetched body. A var (not const) so tests
// can lower it without streaming the production-sized cap. 512 MiB clears
// even very large manuscript images while still bounding worst-case memory
// under the concurrency limiter.
var maxResponseBytes int64 = 512 << 20

// defaultHTTPTimeout bounds a single fetch (connect + headers + body). Without
// it, a server that accepts the connection then stalls holds a worker
// goroutine forever and can drain PoliteFetcher's concurrency semaphore.
const defaultHTTPTimeout = 90 * time.Second

// HTTPStatusError is returned for a non-2xx response (other than 404, which
// maps to ErrNotFound). It carries the status code so the polite layer can
// decide whether the failure is retryable, and any server-provided
// Retry-After delay.
type HTTPStatusError struct {
	Code       int
	URL        string
	RetryAfter time.Duration
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("source: fetching %s: unexpected status %d %s",
		e.URL, e.Code, http.StatusText(e.Code))
}

// Per-host User-Agent policy (honest default + Gallica's required browser
// spoof) lives in internal/institution — the single per-institution home.

// HTTPFetcher retrieves IIIF resources over HTTPS. It implements Fetcher.
type HTTPFetcher struct {
	client    *http.Client
	userAgent string
	hostUA    map[string]string
	store     ConditionalStore
}

// Option configures an HTTPFetcher.
type Option func(*HTTPFetcher)

// WithHTTPClient overrides the HTTP client (used in tests to trust an
// httptest TLS server's certificate).
func WithHTTPClient(c *http.Client) Option {
	return func(f *HTTPFetcher) { f.client = c }
}

// WithUserAgent overrides the default User-Agent (per-host overrides still
// apply unless also replaced via WithHostUserAgent).
func WithUserAgent(ua string) Option {
	return func(f *HTTPFetcher) { f.userAgent = ua }
}

// WithHostUserAgent sets the User-Agent for a specific host, overriding the
// built-in per-host table (e.g. to point a self-hosted mirror's UA, or in
// tests).
func WithHostUserAgent(host, ua string) Option {
	return func(f *HTTPFetcher) { f.hostUA[host] = ua }
}

// WithConditionalStore enables conditional GET: validators (ETag/
// Last-Modified) are stored per URL and replayed so unchanged resources
// return 304 and reuse the cached body.
func WithConditionalStore(s ConditionalStore) Option {
	return func(f *HTTPFetcher) { f.store = s }
}

// NewHTTPFetcher returns an HTTPFetcher whose default and per-host
// User-Agents come from the institution registry (honest default; Gallica
// browser-spoof override), and the default HTTP client unless overridden.
func NewHTTPFetcher(opts ...Option) *HTTPFetcher {
	reg := institution.Builtin()
	hostUA := make(map[string]string, len(reg.ByHost))
	for host, p := range reg.ByHost {
		hostUA[host] = p.UserAgent
	}
	f := &HTTPFetcher{client: &http.Client{Timeout: defaultHTTPTimeout}, userAgent: reg.Default.UserAgent, hostUA: hostUA}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// uaFor returns the User-Agent for host: a per-host override if one exists
// (e.g. Gallica's required browser spoof), else the honest default.
func (f *HTTPFetcher) uaFor(host string) string {
	if ua, ok := f.hostUA[host]; ok {
		return ua
	}
	return f.userAgent
}

// looksLikeHTML reports whether a 2xx body is an HTML document — used to
// catch bot-wall interstitials served with a non-HTML/absent content-type.
func looksLikeHTML(body []byte) bool {
	s := strings.ToLower(strings.TrimSpace(string(body[:min(len(body), 512)])))
	return strings.HasPrefix(s, "<!doctype html") || strings.HasPrefix(s, "<html")
}

// maxRetryAfterSecs caps an honored Retry-After. A larger value (including
// one that would overflow time.Duration's int64 nanoseconds and wrap
// negative) is treated as absent: nothing legitimately needs a multi-day
// wait, and the caller's own backoff is a safer bound than a server-supplied
// one.
const maxRetryAfterSecs = 24 * 60 * 60

// parseRetryAfter parses the delta-seconds form of the Retry-After header.
// The HTTP-date form, a negative value, or one exceeding maxRetryAfterSecs
// is ignored (returns 0) — callers fall back to their own backoff schedule.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 && secs <= maxRetryAfterSecs {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// Fetch retrieves url, sending the configured User-Agent.
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("source: parsing %q: %w", rawURL, err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("%w: %q", ErrInsecureURL, rawURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("source: building request for %s: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", f.uaFor(u.Host))
	// Honest content negotiation: we want IIIF JSON or images, never
	// HTML. This is correct for the API and also less browser-like, so
	// bot-walls are less likely to treat us as a browser to challenge.
	req.Header.Set("Accept", "application/ld+json, application/json;q=0.9, image/jpeg;q=0.8, image/*;q=0.7, */*;q=0.1")

	var cached CacheEntry
	haveCached := false
	if f.store != nil {
		if cached, haveCached = f.store.Get(rawURL); haveCached {
			if cached.ETag != "" {
				req.Header.Set("If-None-Match", cached.ETag)
			}
			if cached.LastModified != "" {
				req.Header.Set("If-Modified-Since", cached.LastModified)
			}
		}
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("source: fetching %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		if !haveCached {
			return nil, fmt.Errorf("source: %s returned 304 with no cached body", rawURL)
		}
		return cached.Body, nil
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, rawURL)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, &HTTPStatusError{
			Code:       resp.StatusCode,
			URL:        rawURL,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("source: reading %s: %w", rawURL, err)
	}
	if int64(len(body)) > maxResponseBytes {
		return nil, fmt.Errorf("%w: %s (limit %d bytes)", ErrResponseTooLarge, rawURL, maxResponseBytes)
	}

	// Never archive an HTML page in place of a IIIF resource. Bot-walls
	// (Anubis answers challenges with 200) and error pages are HTML; a
	// IIIF manifest/info.json is JSON and an image is binary. Checked
	// before the conditional store so a challenge can't poison the cache.
	if ct := resp.Header.Get("Content-Type"); strings.Contains(strings.ToLower(ct), "text/html") || looksLikeHTML(body) {
		return nil, fmt.Errorf("%w: %s (content-type %q)", ErrNonResource, rawURL, ct)
	}

	if f.store != nil {
		if etag, lm := resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"); etag != "" || lm != "" {
			f.store.Put(rawURL, CacheEntry{ETag: etag, LastModified: lm, Body: body})
		}
	}
	return body, nil
}
