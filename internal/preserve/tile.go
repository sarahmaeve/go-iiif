package preserve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"

	xdraw "golang.org/x/image/draw"
)

// IIIF Image API level-0 static tile-pyramid planning. This is pure
// arithmetic — no image decoding — so it is exhaustively unit-testable; the
// exact request set must match what OpenSeadragon/Mirador asks an
// ImageService3 level0 endpoint for, or deep zoom 404s.

// TileRequest is one precomputed IIIF image request: a region in
// full-resolution pixel coordinates (X,Y,W,H) rendered at size (SW,SH).
type TileRequest struct {
	X, Y, W, H int
	SW, SH     int
}

// TilePlan is the full level-0 pyramid for one image: the scale factors and
// per-factor full sizes that go into info.json, plus every tile request a
// viewer will issue.
type TilePlan struct {
	Width, Height, TileSize int
	ScaleFactors            []int
	Sizes                   [][2]int // (w,h) per scale factor, ScaleFactors order
	Tiles                   []TileRequest
}

// infoJSON renders the IIIF Image API 3 level0 info.json for a plan. id is
// the Image API base URL the document is served from; IIIF requires it to
// equal the request URL, so (like the manifest) it is rewritten at serve
// time and only a placeholder is stored.
func infoJSON(p TilePlan, id string) ([]byte, error) {
	type size struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	doc := struct {
		Context  string `json:"@context"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Protocol string `json:"protocol"`
		Profile  string `json:"profile"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		Sizes    []size `json:"sizes"`
		Tiles    []struct {
			Width        int   `json:"width"`
			Height       int   `json:"height"`
			ScaleFactors []int `json:"scaleFactors"`
		} `json:"tiles"`
	}{
		Context:  "http://iiif.io/api/image/3/context.json",
		ID:       id,
		Type:     "ImageService3",
		Protocol: "http://iiif.io/api/image",
		Profile:  "level0",
		Width:    p.Width,
		Height:   p.Height,
	}
	for _, s := range p.Sizes {
		doc.Sizes = append(doc.Sizes, size{Width: s[0], Height: s[1]})
	}
	doc.Tiles = append(doc.Tiles, struct {
		Width        int   `json:"width"`
		Height       int   `json:"height"`
		ScaleFactors []int `json:"scaleFactors"`
	}{Width: p.TileSize, Height: p.TileSize, ScaleFactors: p.ScaleFactors})

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("preserve: encoding info.json: %w", err)
	}
	return out, nil
}

// renderTilePyramid decodes a JPEG and writes a complete IIIF Image API
// level0 static set under prefix in store: every scaled full size at
// "<prefix>/full/<w>,<h>/0/default.jpg", every tile at
// "<prefix>/<x>,<y>,<w>,<h>/<sw>,<sh>/0/default.jpg", and
// "<prefix>/info.json". High-quality CatmullRom downscaling. The returned
// plan lets the caller record provenance / re-point the manifest.
func renderTilePyramid(ctx context.Context, store BlobStore, prefix string, jpegBytes []byte, tileSize int) (TilePlan, error) {
	src, err := jpeg.Decode(bytes.NewReader(jpegBytes))
	if err != nil {
		return TilePlan{}, fmt.Errorf("preserve: decoding image for tiling: %w", err)
	}
	b := src.Bounds()
	p := tilePlan(b.Dx(), b.Dy(), tileSize)

	put := func(key string, w, h int, sr image.Rectangle) error {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, sr, xdraw.Src, nil)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
			return fmt.Errorf("preserve: encoding %s: %w", key, err)
		}
		if err := store.Put(ctx, key, buf.Bytes()); err != nil {
			return fmt.Errorf("preserve: storing %s: %w", key, err)
		}
		return nil
	}

	for _, s := range p.Sizes {
		key := fmt.Sprintf("%s/full/%d,%d/0/default.jpg", prefix, s[0], s[1])
		if err := put(key, s[0], s[1], b); err != nil {
			return p, err
		}
	}
	for _, tr := range p.Tiles {
		key := fmt.Sprintf("%s/%d,%d,%d,%d/%d,%d/0/default.jpg",
			prefix, tr.X, tr.Y, tr.W, tr.H, tr.SW, tr.SH)
		sr := image.Rect(b.Min.X+tr.X, b.Min.Y+tr.Y, b.Min.X+tr.X+tr.W, b.Min.Y+tr.Y+tr.H)
		if err := put(key, tr.SW, tr.SH, sr); err != nil {
			return p, err
		}
	}

	info, err := infoJSON(p, prefix)
	if err != nil {
		return p, err
	}
	if err := store.Put(ctx, prefix+"/info.json", info); err != nil {
		return p, fmt.Errorf("preserve: storing info.json: %w", err)
	}
	return p, nil
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

// tilePlan computes the level-0 pyramid for a width×height image tiled at
// tileSize. Scale factors are 1,2,4,… up to the level where the whole image
// fits in a single tile.
func tilePlan(width, height, tileSize int) TilePlan {
	p := TilePlan{Width: width, Height: height, TileSize: tileSize}

	for s := 1; ; s *= 2 {
		p.ScaleFactors = append(p.ScaleFactors, s)
		p.Sizes = append(p.Sizes, [2]int{ceilDiv(width, s), ceilDiv(height, s)})
		if ceilDiv(width, s) <= tileSize && ceilDiv(height, s) <= tileSize {
			break
		}
	}

	for _, s := range p.ScaleFactors {
		full := tileSize * s // tile edge in full-resolution pixels
		for y := 0; y < height; y += full {
			for x := 0; x < width; x += full {
				rw := min(full, width-x)
				rh := min(full, height-y)
				p.Tiles = append(p.Tiles, TileRequest{
					X: x, Y: y, W: rw, H: rh,
					SW: ceilDiv(rw, s), SH: ceilDiv(rh, s),
				})
			}
		}
	}
	return p
}
