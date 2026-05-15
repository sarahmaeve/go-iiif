package preserve

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jpegEverything serves the same fake JPEG for any image request, and the
// manifest for the manifest URL.
type jpegEverything struct{ manifestURL, manifest string }

func (j jpegEverything) Fetch(_ context.Context, url string) ([]byte, error) {
	if url == j.manifestURL {
		return []byte(j.manifest), nil
	}
	return []byte("\xff\xd8\xff\xe0FAKEJPEG"), nil
}

func TestDirFor_NestsByInstitution(t *testing.T) {
	cases := []struct {
		url, want string
	}{
		{
			"https://iiif.bodleian.ox.ac.uk/iiif/manifest/f317ad0c.json",
			"iiif.bodleian.ox.ac.uk/iiif_manifest_f317ad0c.json",
		},
		{
			"https://gallica.bnf.fr/iiif/ark:/12148/btv1b8451636g/manifest.json",
			"gallica.bnf.fr/iiif_ark_12148_btv1b8451636g_manifest.json",
		},
	}
	for _, c := range cases {
		got := dirFor(c.url)
		if got != c.want {
			t.Errorf("dirFor(%q) = %q, want %q", c.url, got, c.want)
		}
		// Host must be its own leading path segment (institution nesting).
		host, _, ok := strings.Cut(got, "/")
		if !ok || strings.Contains(host, "_iiif") {
			t.Errorf("dirFor(%q) = %q: host is not an isolated first segment", c.url, got)
		}
	}
}

func TestPreserve_StoresImagesManifestAndProvenance(t *testing.T) {
	manifestBytes := readManifest(t, "bodleian_f317ad0c.json") // single-canvas v2
	const manifestURL = "https://iiif.bodleian.ox.ac.uk/iiif/manifest/f317ad0c.json"

	root := t.TempDir()
	store := NewLocalBlobStore(root)
	fetcher := jpegEverything{manifestURL: manifestURL, manifest: string(manifestBytes)}

	sum, err := Preserve(context.Background(), fetcher, store, manifestURL, manifestBytes)
	if err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	if sum.Images != 1 || sum.Stored != 1 || len(sum.Failures) != 0 {
		t.Fatalf("summary = %+v, want 1 image stored, no failures", sum)
	}

	dir := filepath.Join(root, sum.Dir)
	for _, f := range []string{"manifest.json", "0001.jpg", "provenance.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("expected %s in %s: %v", f, sum.Dir, err)
		}
	}

	prov, err := os.ReadFile(filepath.Join(dir, "provenance.json"))
	if err != nil {
		t.Fatalf("reading provenance: %v", err)
	}
	var p struct {
		ManifestURL string `json:"manifest_url"`
		Images      []struct {
			File      string `json:"file"`
			SourceURL string `json:"source_url"`
		} `json:"images"`
	}
	if err := json.Unmarshal(prov, &p); err != nil {
		t.Fatalf("provenance not valid JSON: %v", err)
	}
	if p.ManifestURL != manifestURL || len(p.Images) != 1 || p.Images[0].File != "0001.jpg" {
		t.Fatalf("provenance = %+v, want manifest URL + 1 image record", p)
	}
	if !strings.HasPrefix(p.Images[0].SourceURL, "https://iiif.bodleian.ox.ac.uk/iiif/image/") {
		t.Fatalf("provenance source URL = %q, want the resolved image URL", p.Images[0].SourceURL)
	}
}

func TestPreserve_IdempotentSkipsExisting(t *testing.T) {
	manifestBytes := readManifest(t, "bodleian_f317ad0c.json")
	const manifestURL = "https://iiif.bodleian.ox.ac.uk/iiif/manifest/f317ad0c.json"
	store := NewLocalBlobStore(t.TempDir())
	fetcher := jpegEverything{manifestURL: manifestURL, manifest: string(manifestBytes)}

	if _, err := Preserve(context.Background(), fetcher, store, manifestURL, manifestBytes); err != nil {
		t.Fatalf("first Preserve: %v", err)
	}
	sum, err := Preserve(context.Background(), fetcher, store, manifestURL, manifestBytes)
	if err != nil {
		t.Fatalf("second Preserve: %v", err)
	}
	if sum.Stored != 0 || sum.Skipped != 1 {
		t.Fatalf("re-run summary = %+v, want 0 stored, 1 skipped (idempotent)", sum)
	}
}
