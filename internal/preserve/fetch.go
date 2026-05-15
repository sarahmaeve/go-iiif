package preserve

import (
	"context"
	"fmt"
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/source"
)

// imageSuffixes are the URL forms tried to get the largest available image,
// in default order: the IIIF max size, the (older) full size, then the bare
// URL for institutions serving only static images (no Image API).
var imageSuffixes = []string{
	"/full/max/0/default.jpg",
	"/full/full/0/default.jpg",
	"",
}

// imageURLCandidates returns the URLs to try for a service base. If preferred
// is a valid suffix index it is tried first (the others still follow, so an
// odd page that differs from the rest of a manifest still resolves);
// preferred < 0 means no preference (default order).
func imageURLCandidates(serviceID string, preferred int) []string {
	base := strings.TrimRight(serviceID, "/")
	order := make([]int, 0, len(imageSuffixes))
	if preferred >= 0 && preferred < len(imageSuffixes) {
		order = append(order, preferred)
	}
	for i := range imageSuffixes {
		if i != preferred {
			order = append(order, i)
		}
	}
	urls := make([]string, len(order))
	for j, i := range order {
		urls[j] = base + imageSuffixes[i]
	}
	return urls
}

// FetchImage downloads the largest available image for serviceID, trying the
// candidates (preferred suffix first when given) and returning the first
// non-empty response, the URL that worked, and the suffix index that worked
// so the caller can memoize it for the rest of a manifest — under a strict
// per-host throttle, re-probing a known-dead variant on every page is the
// dominant cost.
func FetchImage(ctx context.Context, fetcher source.Fetcher, serviceID string, preferred int) (data []byte, usedURL string, variant int, err error) {
	base := strings.TrimRight(serviceID, "/")

	order := make([]int, 0, len(imageSuffixes))
	if preferred >= 0 && preferred < len(imageSuffixes) {
		order = append(order, preferred)
	}
	for i := range imageSuffixes {
		if i != preferred {
			order = append(order, i)
		}
	}

	var lastErr error
	for _, i := range order {
		url := base + imageSuffixes[i]
		body, ferr := fetcher.Fetch(ctx, url)
		if ferr != nil {
			lastErr = ferr
			continue
		}
		if len(body) == 0 {
			lastErr = fmt.Errorf("preserve: empty body from %s", url)
			continue
		}
		return body, url, i, nil
	}
	return nil, "", -1, fmt.Errorf("preserve: no image for %s: %w", serviceID, lastErr)
}
