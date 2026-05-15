package preserve

import (
	"context"
	"fmt"
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/source"
)

// imageURLCandidates returns the URLs to try, in order, to get the largest
// available image for an Image API service base: the IIIF max size, then the
// (older) full size, then the bare URL for institutions that serve only
// static images and do not implement the Image API.
func imageURLCandidates(serviceID string) []string {
	base := strings.TrimRight(serviceID, "/")
	return []string{
		base + "/full/max/0/default.jpg",
		base + "/full/full/0/default.jpg",
		base,
	}
}

// FetchImage downloads the largest available image for serviceID, trying the
// candidate URLs in order and returning the first non-empty response. It
// reports which URL succeeded.
func FetchImage(ctx context.Context, fetcher source.Fetcher, serviceID string) (data []byte, usedURL string, err error) {
	var lastErr error
	for _, url := range imageURLCandidates(serviceID) {
		body, err := fetcher.Fetch(ctx, url)
		if err != nil {
			lastErr = err
			continue
		}
		if len(body) == 0 {
			lastErr = fmt.Errorf("preserve: empty body from %s", url)
			continue
		}
		return body, url, nil
	}
	return nil, "", fmt.Errorf("preserve: no image for %s: %w", serviceID, lastErr)
}
