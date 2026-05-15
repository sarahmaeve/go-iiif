package preserve

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// synthJPEG builds a w×h test image in memory (a deterministic gradient, so
// scaled tiles are visually checkable) and JPEG-encodes it. No raster is
// committed to the repo.
func synthJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode synthetic jpeg: %v", err)
	}
	return buf.Bytes()
}

func decodedSize(t *testing.T, path string) (int, int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return cfg.Width, cfg.Height
}

func TestRenderTilePyramid_WritesIIIFLevel0Files(t *testing.T) {
	root := t.TempDir()
	store := NewLocalBlobStore(root)

	plan, err := renderTilePyramid(context.Background(), store, "img1", synthJPEG(t, 600, 400), 256)
	if err != nil {
		t.Fatalf("renderTilePyramid: %v", err)
	}
	if !reflect.DeepEqual(plan.ScaleFactors, []int{1, 2, 4}) {
		t.Fatalf("ScaleFactors = %v, want [1 2 4]", plan.ScaleFactors)
	}

	// info.json present and valid.
	if _, err := os.Stat(filepath.Join(root, "img1", "info.json")); err != nil {
		t.Fatalf("info.json not written: %v", err)
	}

	// IIIF Image API path layout: <prefix>/<region>/<size>/0/default.jpg,
	// and full-image sizes under <prefix>/full/<w>,<h>/0/default.jpg.
	cases := []struct {
		path         string
		wantW, wantH int
	}{
		{"img1/full/300,200/0/default.jpg", 300, 200},
		{"img1/full/150,100/0/default.jpg", 150, 100},
		{"img1/0,0,256,256/256,256/0/default.jpg", 256, 256},
		{"img1/512,256,88,144/88,144/0/default.jpg", 88, 144},
	}
	for _, c := range cases {
		full := filepath.Join(root, filepath.FromSlash(c.path))
		if _, err := os.Stat(full); err != nil {
			t.Fatalf("expected tile %s not written: %v", c.path, err)
		}
		if gw, gh := decodedSize(t, full); gw != c.wantW || gh != c.wantH {
			t.Fatalf("%s decoded %dx%d, want %dx%d", c.path, gw, gh, c.wantW, c.wantH)
		}
	}
}

func TestTilePlan_PyramidMath(t *testing.T) {
	t.Run("image larger than tile builds a full pyramid", func(t *testing.T) {
		p := tilePlan(600, 400, 256)

		if !reflect.DeepEqual(p.ScaleFactors, []int{1, 2, 4}) {
			t.Fatalf("ScaleFactors = %v, want [1 2 4]", p.ScaleFactors)
		}
		wantSizes := [][2]int{{600, 400}, {300, 200}, {150, 100}}
		if !reflect.DeepEqual(p.Sizes, wantSizes) {
			t.Fatalf("Sizes = %v, want %v", p.Sizes, wantSizes)
		}
		// s=1: 3x2=6 tiles, s=2: 2x1=2 tiles, s=4: whole image in 1 tile.
		if len(p.Tiles) != 9 {
			t.Fatalf("len(Tiles) = %d, want 9", len(p.Tiles))
		}

		// Clipped bottom-right tile at scale 1: region clipped to the image,
		// requested size equals the (unscaled) region.
		want := TileRequest{X: 512, Y: 256, W: 88, H: 144, SW: 88, SH: 144}
		if !slices.Contains(p.Tiles, want) {
			t.Fatalf("missing clipped s=1 tile %+v in %+v", want, p.Tiles)
		}
		// Scale 2: 512px full-res tile, requested at half size.
		want = TileRequest{X: 0, Y: 0, W: 512, H: 400, SW: 256, SH: 200}
		if !slices.Contains(p.Tiles, want) {
			t.Fatalf("missing s=2 tile %+v in %+v", want, p.Tiles)
		}
		// Top of pyramid: whole image as one tile at scale 4.
		want = TileRequest{X: 0, Y: 0, W: 600, H: 400, SW: 150, SH: 100}
		if !slices.Contains(p.Tiles, want) {
			t.Fatalf("missing top-level tile %+v in %+v", want, p.Tiles)
		}
	})

	t.Run("image smaller than tile is a single level", func(t *testing.T) {
		p := tilePlan(100, 80, 256)
		if !reflect.DeepEqual(p.ScaleFactors, []int{1}) {
			t.Fatalf("ScaleFactors = %v, want [1]", p.ScaleFactors)
		}
		if !reflect.DeepEqual(p.Sizes, [][2]int{{100, 80}}) {
			t.Fatalf("Sizes = %v, want [[100 80]]", p.Sizes)
		}
		if len(p.Tiles) != 1 || p.Tiles[0] != (TileRequest{X: 0, Y: 0, W: 100, H: 80, SW: 100, SH: 80}) {
			t.Fatalf("Tiles = %+v, want one full tile", p.Tiles)
		}
	})
}

func TestInfoJSON_ImageService3Level0(t *testing.T) {
	b, err := infoJSON(tilePlan(600, 400, 256), "https://host/lib/img")
	if err != nil {
		t.Fatalf("infoJSON: %v", err)
	}
	var got struct {
		Context  string                        `json:"@context"`
		ID       string                        `json:"id"`
		Type     string                        `json:"type"`
		Protocol string                        `json:"protocol"`
		Profile  string                        `json:"profile"`
		Width    int                           `json:"width"`
		Height   int                           `json:"height"`
		Sizes    []struct{ Width, Height int } `json:"sizes"`
		Tiles    []struct {
			Width, Height int
			ScaleFactors  []int `json:"scaleFactors"`
		} `json:"tiles"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("info.json is not valid JSON: %v\n%s", err, b)
	}
	if got.Context != "http://iiif.io/api/image/3/context.json" || got.Type != "ImageService3" ||
		got.Profile != "level0" || got.Protocol != "http://iiif.io/api/image" {
		t.Fatalf("static IIIF Image API 3 level0 fields wrong: %+v", got)
	}
	if got.ID != "https://host/lib/img" || got.Width != 600 || got.Height != 400 {
		t.Fatalf("id/width/height = %q %d %d", got.ID, got.Width, got.Height)
	}
	wantSizes := [][2]int{{600, 400}, {300, 200}, {150, 100}}
	for i, s := range got.Sizes {
		if s.Width != wantSizes[i][0] || s.Height != wantSizes[i][1] {
			t.Fatalf("sizes[%d] = %dx%d, want %v", i, s.Width, s.Height, wantSizes[i])
		}
	}
	if len(got.Sizes) != 3 {
		t.Fatalf("len(sizes) = %d, want 3", len(got.Sizes))
	}
	if len(got.Tiles) != 1 || got.Tiles[0].Width != 256 || got.Tiles[0].Height != 256 ||
		!reflect.DeepEqual(got.Tiles[0].ScaleFactors, []int{1, 2, 4}) {
		t.Fatalf("tiles = %+v, want one 256 tile with scaleFactors [1 2 4]", got.Tiles)
	}
}
