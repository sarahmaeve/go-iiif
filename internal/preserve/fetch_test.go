package preserve

import (
	"context"
	"errors"
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/source"
)

func TestImageURLCandidates(t *testing.T) {
	tests := []struct {
		name      string
		serviceID string
		want      []string
	}{
		{
			name:      "Image API service base",
			serviceID: "https://iiif.bodleian.ox.ac.uk/iiif/image/abc",
			want: []string{
				"https://iiif.bodleian.ox.ac.uk/iiif/image/abc/full/max/0/default.jpg",
				"https://iiif.bodleian.ox.ac.uk/iiif/image/abc/full/full/0/default.jpg",
				"https://iiif.bodleian.ox.ac.uk/iiif/image/abc",
			},
		},
		{
			name:      "trailing slash does not double",
			serviceID: "https://example.org/iiif/x/",
			want: []string{
				"https://example.org/iiif/x/full/max/0/default.jpg",
				"https://example.org/iiif/x/full/full/0/default.jpg",
				"https://example.org/iiif/x",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageURLCandidates(tt.serviceID)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("candidate[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
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

func TestFetchImage_FallsBackThroughCandidates(t *testing.T) {
	const sid = "https://example.org/iiif/x"

	// Image API max/full 404; bare static URL succeeds.
	f := seqFetcher{
		sid: {body: []byte("\xff\xd8jpegbytes")},
	}
	data, used, err := FetchImage(context.Background(), f, sid)
	if err != nil {
		t.Fatalf("FetchImage: %v", err)
	}
	if string(data) != "\xff\xd8jpegbytes" {
		t.Fatalf("data = %q", data)
	}
	if used != sid {
		t.Fatalf("used URL = %q, want bare %q", used, sid)
	}
}

func TestFetchImage_AllCandidatesFail(t *testing.T) {
	_, _, err := FetchImage(context.Background(), seqFetcher{}, "https://example.org/iiif/x")
	if err == nil {
		t.Fatal("want error when every candidate fails")
	}
	if !errors.Is(err, source.ErrNotFound) {
		t.Fatalf("err = %v, want it to wrap source.ErrNotFound", err)
	}
}
