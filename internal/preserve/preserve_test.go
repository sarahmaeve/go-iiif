package preserve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jpegEverything serves the same fake JPEG for any image request, and the
// manifest for the manifest URL.
type jpegEverything struct {
	manifestURL, manifest string
	image                 []byte
}

func (j jpegEverything) Fetch(_ context.Context, url string) ([]byte, error) {
	if url == j.manifestURL {
		return []byte(j.manifest), nil
	}
	return j.image, nil
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
	fetcher := jpegEverything{manifestURL: manifestURL, manifest: string(manifestBytes), image: synthJPEG(t, 32, 24)}

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
	fetcher := jpegEverything{manifestURL: manifestURL, manifest: string(manifestBytes), image: synthJPEG(t, 32, 24)}

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

// realJPEG serves a decodable 600x400 image for any image URL (so tiling
// runs), and the manifest for the manifest URL.
type realJPEG struct {
	manifestURL, manifest string
	img                   []byte
}

func (f realJPEG) Fetch(_ context.Context, url string) ([]byte, error) {
	if url == f.manifestURL {
		return []byte(f.manifest), nil
	}
	return f.img, nil
}

func TestPreserve_ReportsProgress(t *testing.T) {
	const mURL = "https://g.example/iiif/ms/manifest.json"
	manifest := `{"@type":"sc:Manifest","sequences":[{"canvases":[` +
		`{"images":[{"resource":{"service":{"@id":"https://g.example/iiif/img1"}}}]},` +
		`{"images":[{"resource":{"service":{"@id":"https://g.example/iiif/img2"}}}]},` +
		`{"images":[{"resource":{"service":{"@id":"https://g.example/iiif/img3"}}}]}` +
		`]}]}`
	f := &countFetcher{manifestURL: mURL, manifest: manifest, image: synthJPEG(t, 32, 24)}

	var events []ProgressEvent
	_, err := Preserve(context.Background(), f, NewLocalBlobStore(t.TempDir()), mURL, []byte(manifest),
		WithProgress(func(e ProgressEvent) { events = append(events, e) }))
	if err != nil {
		t.Fatalf("Preserve: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("got %d progress events, want 3", len(events))
	}
	for i, e := range events {
		if e.Index != i+1 || e.Total != 3 {
			t.Fatalf("event %d = %+v, want Index %d Total 3", i, e, i+1)
		}
		if e.Action != "stored" {
			t.Fatalf("event %d Action = %q, want stored", i, e.Action)
		}
	}
}

func TestPreserve_RendersTilePyramid(t *testing.T) {
	manifestBytes := readManifest(t, "bodleian_f317ad0c.json")
	const manifestURL = "https://iiif.bodleian.ox.ac.uk/iiif/manifest/f317ad0c.json"
	root := t.TempDir()
	store := NewLocalBlobStore(root)
	fetcher := realJPEG{manifestURL: manifestURL, manifest: string(manifestBytes), img: synthJPEG(t, 600, 400)}

	sum, err := Preserve(context.Background(), fetcher, store, manifestURL, manifestBytes)
	if err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	if sum.Stored != 1 || sum.Tiled != 1 || len(sum.Failures) != 0 {
		t.Fatalf("summary = %+v, want 1 stored, 1 tiled, no failures", sum)
	}

	// The level0 pyramid sits beside the flat jpg, under the image's prefix.
	if _, err := os.Stat(filepath.Join(root, sum.Dir, "0001", "info.json")); err != nil {
		t.Fatalf("tile info.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, sum.Dir, "0001.jpg")); err != nil {
		t.Fatalf("flat jpg should still exist alongside tiles: %v", err)
	}

	prov, err := os.ReadFile(filepath.Join(root, sum.Dir, "provenance.json"))
	if err != nil {
		t.Fatalf("reading provenance: %v", err)
	}
	var p struct {
		Images []struct {
			File    string `json:"file"`
			TileDir string `json:"tile_dir"`
		} `json:"images"`
	}
	if err := json.Unmarshal(prov, &p); err != nil {
		t.Fatalf("provenance JSON: %v", err)
	}
	if len(p.Images) != 1 || p.Images[0].TileDir != "0001" {
		t.Fatalf("provenance images = %+v, want tile_dir 0001", p.Images)
	}
}

func TestPreserve_RepairsMissingTilesWithoutRefetch(t *testing.T) {
	const manifestURL = "https://g.example/iiif/ms/manifest.json"
	const manifest = `{"sequences":[{"canvases":[{"images":[{"resource":{"service":{"@id":"https://g.example/iiif/img1"}}}]}]}]}`
	root := t.TempDir()
	store := NewLocalBlobStore(root)
	dir := dirFor(manifestURL)
	image := synthJPEG(t, 600, 400)
	if err := store.Put(context.Background(), dir+"/0001.jpg", image); err != nil {
		t.Fatalf("preparing downloaded page: %v", err)
	}
	fetcher := &countFetcher{manifestURL: manifestURL, manifest: manifest, image: image}
	var actions []string
	sum, err := Preserve(context.Background(), fetcher, store, manifestURL, []byte(manifest),
		WithProgress(func(e ProgressEvent) { actions = append(actions, e.Action) }))
	if err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	if len(fetcher.calls) != 0 {
		t.Fatalf("repair fetched %v; existing page must be reused locally", fetcher.calls)
	}
	if sum.Stored != 0 || sum.Skipped != 1 || sum.Repaired != 1 || sum.Tiled != 1 {
		t.Fatalf("summary = %+v, want one reused page with repaired tiles", sum)
	}
	if len(actions) != 1 || actions[0] != "repaired" {
		t.Fatalf("actions = %v, want [repaired]", actions)
	}
	for _, name := range []string{"0001/info.json", "provenance.json"} {
		if _, err := os.Stat(filepath.Join(root, dir, filepath.FromSlash(name))); err != nil {
			t.Fatalf("expected repaired %s: %v", name, err)
		}
	}
}

func TestPreserve_RepairsPyramidMissingCommitMarkerWithoutRefetch(t *testing.T) {
	manifestBytes := readManifest(t, "bodleian_f317ad0c.json")
	const manifestURL = "https://iiif.bodleian.ox.ac.uk/iiif/manifest/f317ad0c.json"
	root := t.TempDir()
	store := NewLocalBlobStore(root)
	image := synthJPEG(t, 600, 400)
	fetcher := realJPEG{manifestURL: manifestURL, manifest: string(manifestBytes), img: image}
	first, err := Preserve(context.Background(), fetcher, store, manifestURL, manifestBytes)
	if err != nil {
		t.Fatalf("first Preserve: %v", err)
	}
	missing := filepath.Join(root, first.Dir, "0001", "info.json")
	if err := os.Remove(missing); err != nil {
		t.Fatalf("removing pyramid commit marker: %v", err)
	}
	counted := &countFetcher{manifestURL: manifestURL, manifest: string(manifestBytes), image: image}
	second, err := Preserve(context.Background(), counted, store, manifestURL, manifestBytes)
	if err != nil {
		t.Fatalf("second Preserve: %v", err)
	}
	if len(counted.calls) != 0 || second.Repaired != 1 {
		t.Fatalf("calls=%v summary=%+v; want local repair with no fetch", counted.calls, second)
	}
	if _, err := os.Stat(missing); err != nil {
		t.Fatalf("missing pyramid commit marker was not rebuilt: %v", err)
	}
}

func TestPreserve_RefetchesCorruptExistingPage(t *testing.T) {
	const manifestURL = "https://g.example/iiif/ms/manifest.json"
	const manifest = `{"sequences":[{"canvases":[{"images":[{"resource":{"service":{"@id":"https://g.example/iiif/img1"}}}]}]}]}`
	root := t.TempDir()
	store := NewLocalBlobStore(root)
	dir := dirFor(manifestURL)
	if err := store.Put(context.Background(), dir+"/0001.jpg", []byte("not a jpeg")); err != nil {
		t.Fatal(err)
	}
	image := synthJPEG(t, 32, 24)
	fetcher := &countFetcher{manifestURL: manifestURL, manifest: manifest, image: image}
	sum, err := Preserve(context.Background(), fetcher, store, manifestURL, []byte(manifest))
	if err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	if sum.Stored != 1 || sum.Skipped != 0 || fetcher.count("/full/full/") != 1 {
		t.Fatalf("summary=%+v calls=%v; want only corrupt page replaced", sum, fetcher.calls)
	}
	got, err := store.Get(context.Background(), dir+"/0001.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(got)); err != nil {
		t.Fatalf("replacement is not a JPEG: %v", err)
	}
}

func TestPreserve_IncompleteBundleHasNoProvenance(t *testing.T) {
	const manifestURL = "https://g.example/iiif/ms/manifest.json"
	const manifest = `{"sequences":[{"canvases":[{"images":[{"resource":{"service":{"@id":"https://g.example/iiif/missing"}}}]}]}]}`
	root := t.TempDir()
	store := NewLocalBlobStore(root)
	dir := dirFor(manifestURL)
	if err := store.Put(context.Background(), dir+"/provenance.json", []byte(`{"manifest_url":"stale","images":[]}`)); err != nil {
		t.Fatal(err)
	}
	_, err := Preserve(context.Background(), seqFetcher{}, store, manifestURL, []byte(manifest))
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Preserve error = %v, want ErrIncomplete", err)
	}
	if ok, statErr := store.Exists(context.Background(), dir+"/provenance.json"); statErr != nil || ok {
		t.Fatalf("completion marker after failed run = %v, %v; want absent", ok, statErr)
	}
	if ok, statErr := store.Exists(context.Background(), dir+"/manifest.json"); statErr != nil || !ok {
		t.Fatalf("restart manifest = %v, %v; want preserved", ok, statErr)
	}
}

func TestPreserve_CancelThenRestartReusesCommittedPage(t *testing.T) {
	const manifestURL = "https://g.example/iiif/ms/manifest.json"
	const manifest = `{"sequences":[{"canvases":[` +
		`{"images":[{"resource":{"service":{"@id":"https://g.example/iiif/img1"}}}]},` +
		`{"images":[{"resource":{"service":{"@id":"https://g.example/iiif/img2"}}}]}` +
		`]}]}`
	root := t.TempDir()
	store := NewLocalBlobStore(root)
	image := synthJPEG(t, 32, 24)
	ctx, cancel := context.WithCancel(context.Background())
	firstFetcher := &countFetcher{manifestURL: manifestURL, manifest: manifest, image: image}
	_, err := Preserve(ctx, firstFetcher, store, manifestURL, []byte(manifest), WithProgress(func(e ProgressEvent) {
		if e.Index == 1 {
			cancel()
		}
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted Preserve error = %v, want context.Canceled", err)
	}
	dir := dirFor(manifestURL)
	if ok, _ := store.Exists(context.Background(), dir+"/0001.jpg"); !ok {
		t.Fatal("first committed page was lost after cancellation")
	}
	if ok, _ := store.Exists(context.Background(), dir+"/provenance.json"); ok {
		t.Fatal("cancelled bundle has a completion marker")
	}

	secondFetcher := &countFetcher{manifestURL: manifestURL, manifest: manifest, image: image}
	sum, err := Preserve(context.Background(), secondFetcher, store, manifestURL, []byte(manifest))
	if err != nil {
		t.Fatalf("restart Preserve: %v", err)
	}
	if sum.Skipped != 1 || sum.Stored != 1 {
		t.Fatalf("restart summary = %+v, want one reused and one downloaded page", sum)
	}
	for _, call := range secondFetcher.calls {
		if strings.Contains(call, "img1") {
			t.Fatalf("restart re-requested committed page: %v", secondFetcher.calls)
		}
	}
	if ok, _ := store.Exists(context.Background(), dir+"/provenance.json"); !ok {
		t.Fatal("successful restart did not commit provenance")
	}
}
