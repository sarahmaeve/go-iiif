package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ErrInsecureURL is returned when a URL is not https. The crawler refuses
// plaintext rather than silently upgrading, so misconfiguration is explicit.
var ErrInsecureURL = errors.New("source: non-https URL refused")

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

// defaultUserAgent is browser-like on purpose: some institutions (e.g.
// Gallica/BnF) reject the default Go HTTP client User-Agent with 403.
const defaultUserAgent = "Mozilla/5.0 (compatible; go-iiif-preservation/0.1; +https://github.com/sarahmaeve/go-iiif)"

// HTTPFetcher retrieves IIIF resources over HTTPS. It implements Fetcher.
type HTTPFetcher struct {
	client    *http.Client
	userAgent string
}

// Option configures an HTTPFetcher.
type Option func(*HTTPFetcher)

// WithHTTPClient overrides the HTTP client (used in tests to trust an
// httptest TLS server's certificate).
func WithHTTPClient(c *http.Client) Option {
	return func(f *HTTPFetcher) { f.client = c }
}

// WithUserAgent overrides the User-Agent header sent on every request.
func WithUserAgent(ua string) Option {
	return func(f *HTTPFetcher) { f.userAgent = ua }
}

// NewHTTPFetcher returns an HTTPFetcher with a browser-like User-Agent and the
// default HTTP client unless overridden.
func NewHTTPFetcher(opts ...Option) *HTTPFetcher {
	f := &HTTPFetcher{client: http.DefaultClient, userAgent: defaultUserAgent}
	for _, opt := range opts {
		opt(f)
	}
	return f
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
	req.Header.Set("User-Agent", f.userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("source: fetching %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
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
	return body, nil
}
