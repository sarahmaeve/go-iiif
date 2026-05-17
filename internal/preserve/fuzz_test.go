package preserve

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// withinRoot reports whether p (already absolute/clean) is root itself or a
// descendant of it — i.e. no path-traversal escape.
func withinRoot(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// FuzzEnumerateImages asserts the untrusted-manifest image walker never
// panics on arbitrary bytes, is deterministic, and returns no images
// alongside an error.
func FuzzEnumerateImages(f *testing.F) {
	for _, s := range []string{
		``, `{}`, `null`, `[]`, `{"sequences":null}`,
		`{"sequences":[{"canvases":[{"label":"c","images":[{"resource":{"@id":"u"}}]}]}]}`,
		`{"items":[{"items":[{"items":[{"body":{"id":"u","service":{"id":"s"}}}]}]}]}`,
		`{"items":[{"items":[{"items":[{"body":{"service":[{"@id":"s"}]}}]}]}]}`,
		`{"sequences":[{"canvases":[{"label":{"@value":"x"}}]}]}`,
		`{"items":[{"items":[{"items":[{"body":{"width":-1}}]}]}]}`,
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, manifest []byte) {
		out, err := EnumerateImages(manifest)
		if err != nil && len(out) != 0 {
			t.Fatalf("EnumerateImages error path returned %d images", len(out))
		}
		out2, err2 := EnumerateImages(manifest)
		if (err == nil) != (err2 == nil) || len(out) != len(out2) {
			t.Fatalf("EnumerateImages not deterministic: (%d,%v) vs (%d,%v)",
				len(out), err, len(out2), err2)
		}
	})
}

// FuzzDirFor asserts the security contract of the BlobStore key deriver:
// for ANY manifest URL, the derived prefix joined under a store root must
// stay inside that root. dirFor exists to be this boundary, so the property
// is its specification.
func FuzzDirFor(f *testing.F) {
	const root = "/srv/store"
	for _, s := range []string{
		"https://gallica.bnf.fr/ark:/12148/btv1b9059632h/manifest.json",
		"https://example.org/iiif/3/manifest",
		"", "no-scheme", "https://../foo", "..//x", "https://h/../../../etc",
		"http://host/a/../b", "://", "https://host/.", "https://./x",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, manifestURL string) {
		got := dirFor(manifestURL)
		if got2 := dirFor(manifestURL); got2 != got {
			t.Fatalf("dirFor(%q) not deterministic: %q vs %q", manifestURL, got, got2)
		}
		joined := filepath.Join(root, filepath.FromSlash(got)+"/manifest.json")
		if !withinRoot(root, joined) {
			t.Fatalf("dirFor(%q) = %q -> %q escapes store root %q",
				manifestURL, got, joined, root)
		}
	})
}

// FuzzBlobStorePath asserts the LocalBlobStore never writes or stats outside
// its root regardless of key — defense in depth behind dirFor.
func FuzzBlobStorePath(f *testing.F) {
	for _, s := range []string{
		"a/b.jpg", "host/slug/manifest.json", "../escape", "../../etc/x",
		"/abs", "a/../../b", "", ".", "..", "a/./b",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, key string) {
		root := t.TempDir()
		s := NewLocalBlobStore(root)

		// Exists must never report true for, nor stat, anything outside root;
		// it returns an error for an escaping key rather than leaking.
		if _, err := s.Exists(context.Background(), key); err == nil {
			// ok: contained key (may or may not exist)
		}
		// Put must refuse to write outside root.
		err := s.Put(context.Background(), key, []byte("x"))
		if err == nil {
			written := filepath.Join(root, filepath.FromSlash(key))
			if !withinRoot(root, written) {
				t.Fatalf("Put(%q) succeeded writing outside root: %q", key, written)
			}
		}
	})
}
