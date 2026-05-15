package preserve

import (
	"os"
	"path/filepath"
	"testing"
)

// Shared real manifest fixtures live in internal/metadata/testdata; reference
// them rather than duplicating a 257KB Gallica manifest.
func readManifest(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "metadata", "testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func TestEnumerateImages_RealV3Manifest(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "iiif_v3_manifest_cookbook0032_01.json"))
	if err != nil {
		t.Fatalf("reading v3 fixture: %v", err)
	}
	imgs, err := EnumerateImages(b)
	if err != nil {
		t.Fatalf("EnumerateImages: %v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("got %d images, want 1", len(imgs))
	}
	got := imgs[0]
	const wantService = "https://iiif.io/api/image/3.0/example/reference/329817fc8a251a01c393f517d8a17d87-Winslow_Homer_-_The_Gulf_Stream_-_Metropolitan_Museum_of_Art"
	if got.ServiceID != wantService {
		t.Fatalf("ServiceID = %q, want %q", got.ServiceID, wantService)
	}
	if got.Width != 5886 || got.Height != 3540 {
		t.Fatalf("dims = %dx%d, want 5886x3540", got.Width, got.Height)
	}
}

func TestEnumerateImages_RealV2Manifests(t *testing.T) {
	t.Run("Gallica Français 2814 (multi-canvas v2)", func(t *testing.T) {
		imgs, err := EnumerateImages(readManifest(t, "gallica_btv1b9059632h.json"))
		if err != nil {
			t.Fatalf("EnumerateImages: %v", err)
		}
		if len(imgs) < 2 {
			t.Fatalf("got %d images, want a multi-canvas manuscript", len(imgs))
		}
		first := imgs[0]
		if first.ServiceID != "https://gallica.bnf.fr/iiif/ark:/12148/btv1b9059632h/f1" {
			t.Fatalf("first ServiceID = %q", first.ServiceID)
		}
		if first.Width != 8070 || first.Height != 6002 {
			t.Fatalf("first dims = %dx%d, want 8070x6002", first.Width, first.Height)
		}
	})

	t.Run("Bodleian C 4.8(1) Linc. (single-canvas v2)", func(t *testing.T) {
		imgs, err := EnumerateImages(readManifest(t, "bodleian_f317ad0c.json"))
		if err != nil {
			t.Fatalf("EnumerateImages: %v", err)
		}
		if len(imgs) != 1 {
			t.Fatalf("got %d images, want 1", len(imgs))
		}
		got := imgs[0]
		if got.ServiceID != "https://iiif.bodleian.ox.ac.uk/iiif/image/c85d87de-abd9-43b1-abf4-c65a814dc0a8" {
			t.Fatalf("ServiceID = %q", got.ServiceID)
		}
		if got.Width != 2735 || got.Height != 4103 {
			t.Fatalf("dims = %dx%d, want 2735x4103", got.Width, got.Height)
		}
	})
}
