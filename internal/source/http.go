package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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

// defaultUserAgent is honest on purpose. It identifies the tool, its
// purpose, and a contact URL — so a polite one-time preservation fetch is
// distinguishable from an abusive scraper, and so bot-walls that score
// browser-spoofing UAs as suspicious (e.g. Anubis adds weight to any
// "Mozilla"/"Opera" UA and then issues a JS proof-of-work an HTTP client
// cannot solve) score this as weight 0 and let it through.
const defaultUserAgent = "iiif-preserve/0.1 (+https://github.com/sarahmaeve/go-iiif; one-time preservation fetch of public-domain material)"

// browserUserAgent is a deliberate browser-spoof, used ONLY for hosts that
// reject honest non-browser UAs (Gallica/BnF answers them with 403). This
// is the exception, not the default — see builtinHostUserAgents.
const browserUserAgent = "Mozilla/5.0 (compatible; iiif-preserve/0.1; +https://github.com/sarahmaeve/go-iiif)"

// builtinHostUserAgents overrides the honest default per host. Same
// per-host shape as DefaultRatePolicy: the default is correct citizenship;
// a host only gets an override when it forces one.
var builtinHostUserAgents = map[string]string{
	"gallica.bnf.fr": browserUserAgent,
}

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

// NewHTTPFetcher returns an HTTPFetcher with a browser-like User-Agent and the
// default HTTP client unless overridden.
func NewHTTPFetcher(opts ...Option) *HTTPFetcher {
	hostUA := make(map[string]string, len(builtinHostUserAgents))
	maps.Copy(hostUA, builtinHostUserAgents)
	f := &HTTPFetcher{client: http.DefaultClient, userAgent: defaultUserAgent, hostUA: hostUA}
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

// parseRetryAfter parses the delta-seconds form of the Retry-After header.
// The HTTP-date form is ignored (returns 0) — callers fall back to their own
// backoff schedule.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("source: reading %s: %w", rawURL, err)
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
