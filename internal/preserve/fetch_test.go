package preserve

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/source"
)

func TestImageURLCandidates(t *testing.T) {
	const sid = "https://example.org/iiif/x"
	def := []string{
		"https://example.org/iiif/x/full/max/0/default.jpg",
		"https://example.org/iiif/x/full/full/0/default.jpg",
		"https://example.org/iiif/x",
	}

	t.Run("no preference keeps the default order", func(t *testing.T) {
		got := imageURLCandidates(sid, -1)
		if strings.Join(got, "\n") != strings.Join(def, "\n") {
			t.Fatalf("got %v, want %v", got, def)
		}
	})

	t.Run("preferred variant is tried first, others still follow", func(t *testing.T) {
		got := imageURLCandidates(sid, 1) // /full/full known-good
		want := []string{
			"https://example.org/iiif/x/full/full/0/default.jpg",
			"https://example.org/iiif/x/full/max/0/default.jpg",
			"https://example.org/iiif/x",
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("trailing slash does not double", func(t *testing.T) {
		if got := imageURLCandidates("https://example.org/iiif/x/", -1); got[2] != "https://example.org/iiif/x" {
			t.Fatalf("bare = %q", got[2])
		}
	})

	t.Run("static image URL is tried directly before Image API variants", func(t *testing.T) {
		const static = "https://media.example/painting.jpg"
		got := imageURLCandidates(static, -1)
		if got[0] != static {
			t.Fatalf("first candidate = %q, want static resource %q", got[0], static)
		}
	})
}

// seqFetcher returns a programmed error/body per URL.
type seqFetcher map[string]struct {
	body []byte
	err  error
}

func (s seqFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	r, ok := s[url]
	if !ok {
		return nil, source.ErrNotFound
	}
	return r.body, r.err
}

func TestFetchImage_FallsBackAndReportsVariant(t *testing.T) {
	const sid = "https://example.org/iiif/x"
	image := synthJPEG(t, 32, 24)
	f := seqFetcher{sid: {body: image}} // only bare works

	data, used, variant, err := FetchImage(context.Background(), f, sid, -1)
	if err != nil {
		t.Fatalf("FetchImage: %v", err)
	}
	if !bytes.Equal(data, image) || used != sid {
		t.Fatalf("data differs=%v used=%q", !bytes.Equal(data, image), used)
	}
	if variant != 2 {
		t.Fatalf("variant = %d, want 2 (bare)", variant)
	}
}

func TestFetchImage_SkipsInvalidSuccessfulResponse(t *testing.T) {
	const sid = "https://example.org/iiif/x"
	good := synthJPEG(t, 32, 24)
	f := seqFetcher{
		sid + "/full/max/0/default.jpg":  {body: []byte("not an image")},
		sid + "/full/full/0/default.jpg": {body: good},
	}
	data, used, variant, err := FetchImage(context.Background(), f, sid, -1)
	if err != nil {
		t.Fatalf("FetchImage: %v", err)
	}
	if !bytes.Equal(data, good) || used != sid+"/full/full/0/default.jpg" || variant != 1 {
		t.Fatalf("got used=%q variant=%d; want valid full/full fallback", used, variant)
	}
}

func TestFetchImage_AllCandidatesFail(t *testing.T) {
	_, _, _, err := FetchImage(context.Background(), seqFetcher{}, "https://example.org/iiif/x", -1)
	if err == nil || !errors.Is(err, source.ErrNotFound) {
		t.Fatalf("err = %v, want wrapping source.ErrNotFound", err)
	}
}

// countFetcher records every requested URL and serves the manifest plus a
// jpeg for any /full/full request; /full/max always 404s (Gallica-like).
type countFetcher struct {
	mu          sync.Mutex
	calls       []string
	manifestURL string
	manifest    string
	image       []byte
}

func (c *countFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	c.mu.Lock()
	c.calls = append(c.calls, url)
	c.mu.Unlock()
	if url == c.manifestURL {
		return []byte(c.manifest), nil
	}
	if strings.Contains(url, "/full/max/") {
		return nil, source.ErrNotFound
	}
	if strings.Contains(url, "/full/full/") {
		return c.image, nil
	}
	return nil, source.ErrNotFound
}

func (c *countFetcher) count(substr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, u := range c.calls {
		if strings.Contains(u, substr) {
			n++
		}
	}
	return n
}

// TestPreserve_MemoizesWorkingVariant: once /full/max is known dead for a
// manifest, later pages must not re-probe it (the Gallica throttle bottleneck).
func TestPreserve_MemoizesWorkingVariant(t *testing.T) {
	const mURL = "https://g.example/iiif/ms/manifest.json"
	manifest := `{"@type":"sc:Manifest","sequences":[{"canvases":[` +
		`{"images":[{"resource":{"service":{"@id":"https://g.example/iiif/img1"}}}]},` +
		`{"images":[{"resource":{"service":{"@id":"https://g.example/iiif/img2"}}}]},` +
		`{"images":[{"resource":{"service":{"@id":"https://g.example/iiif/img3"}}}]}` +
		`]}]}`
	f := &countFetcher{manifestURL: mURL, manifest: manifest, image: synthJPEG(t, 32, 24)}

	sum, err := Preserve(context.Background(), f, NewLocalBlobStore(t.TempDir()), mURL, []byte(manifest))
	if err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	if sum.Stored != 3 {
		t.Fatalf("Stored = %d, want 3", sum.Stored)
	}
	if got := f.count("/full/max/"); got != 1 {
		t.Fatalf("/full/max probed %d times, want 1 (memoized after first image)", got)
	}
}
