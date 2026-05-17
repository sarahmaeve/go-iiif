package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// FuzzLocalInfoDims is a path-traversal containment test. A canary
// info.json (width 666) is planted just OUTSIDE the bundle, reachable only
// by escaping bundleDir; a legitimate one (width 100) sits inside. The
// security contract: no tileDir value may ever cause the canary to be read,
// and the function must never panic.
func FuzzLocalInfoDims(f *testing.F) {
	root := f.TempDir()
	bundleDir := filepath.Join(root, "bundle")
	if err := os.MkdirAll(filepath.Join(bundleDir, "tiles"), 0o755); err != nil {
		f.Fatalf("mkdir bundle/tiles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "tiles", "info.json"),
		[]byte(`{"width":100,"height":120}`), 0o644); err != nil {
		f.Fatalf("write legit info.json: %v", err)
	}
	// Canary outside the bundle, reachable only via "../evil".
	if err := os.MkdirAll(filepath.Join(root, "evil"), 0o755); err != nil {
		f.Fatalf("mkdir evil: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "evil", "info.json"),
		[]byte(`{"width":666,"height":666}`), 0o644); err != nil {
		f.Fatalf("write canary info.json: %v", err)
	}

	for _, s := range []string{
		"tiles", "", "..", "../evil", "../../evil", "tiles/../../evil",
		"/etc", filepath.Join(root, "evil"), "tiles/.", "./tiles",
		".", "evil", "tiles/sub", "\x00", "tiles\x00",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, tileDir string) {
		w, h := localInfoDims(bundleDir, tileDir)

		if w == 666 || h == 666 {
			t.Fatalf("path traversal: localInfoDims(%q, %q) read the canary -> (%d,%d)",
				bundleDir, tileDir, w, h)
		}
		// Determinism.
		if w2, h2 := localInfoDims(bundleDir, tileDir); w2 != w || h2 != h {
			t.Fatalf("localInfoDims(%q) not deterministic: (%d,%d) vs (%d,%d)",
				tileDir, w, h, w2, h2)
		}
		// Any non-zero result must be the one legitimate in-bundle file.
		if (w != 0 || h != 0) && (w != 100 || h != 120) {
			t.Fatalf("localInfoDims(%q) returned unexpected dims (%d,%d)", tileDir, w, h)
		}
	})
}

// FuzzRewriteHelpers drives the defensive type-asserting manifest helpers
// (nodeID, serviceAnchor, thumbnailURL, isCanvas) with arbitrary decoded
// JSON, asserting they never panic and are deterministic regardless of
// shape — these run over untrusted third-party manifests.
func FuzzRewriteHelpers(f *testing.F) {
	for _, s := range []string{
		`{}`, `{"id":"x"}`, `{"@id":"y"}`, `{"id":1}`,
		`{"type":"Canvas"}`, `{"@type":"sc:Canvas"}`,
		`{"service":{"id":"s"}}`, `{"service":[{"@id":"s2"}]}`,
		`{"service":["bad",null,3]}`, `"plain"`, `[1,2,3]`, `null`,
		`{"thumbnail":[{"id":"t"}]}`, `{"thumbnail":"u"}`,
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return // only exercise decodable JSON
		}
		if m, ok := v.(map[string]any); ok {
			k1, v1 := nodeID(m)
			k2, v2 := nodeID(m)
			if k1 != k2 || v1 != v2 {
				t.Fatalf("nodeID not deterministic for %v", m)
			}
			if k1 != "" && k1 != "@id" && k1 != "id" {
				t.Fatalf("nodeID returned unexpected key %q", k1)
			}
			_ = isCanvas(m)
			_ = serviceAnchor(m["service"])
		}
		a := thumbnailURL(v)
		if b := thumbnailURL(v); a != b {
			t.Fatalf("thumbnailURL not deterministic: %q vs %q", a, b)
		}
	})
}
