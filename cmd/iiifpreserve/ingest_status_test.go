package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/institution"
)

func TestIngestStatusReportsRunsFailuresAndIncompleteBundlesReadOnly(t *testing.T) {
	root := t.TempDir()
	state, err := openIngestState(root, &options{collection: "https://example.org/collection"}, institution.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.journal.MarkDone("https://example.org/m/1"); err != nil {
		t.Fatal(err)
	}
	if err := state.failures.MarkFailed("https://example.org/m/2", errors.New("temporary source failure")); err != nil {
		t.Fatal(err)
	}
	frontier := `{"version":1,"root":"https://example.org/collection","pending_collections":["https://example.org/sub"],"visited_collections":["https://example.org/collection"],"discovered_manifests":["https://example.org/m/1","https://example.org/m/2"],"complete":false}`
	if err := os.WriteFile(state.frontierPath, []byte(frontier), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "example.org", "m_2")
	if err := os.MkdirAll(bundle, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), []byte(`{"type":"Manifest"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	summary, err := reportIngestStatus(root, &cliWriter{w: &output})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Runs != 1 || summary.Reused != 1 || summary.Pending != 1 || summary.Failed != 1 || summary.IncompleteBundles != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	for _, want := range []string{"1 reused", "1 pending manifest", "1 failed", "1 pending collection", "incomplete bundle: example.org/m_2"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output %q lacks %q", output.String(), want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".iiifpreserve", "http-cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only status created HTTP cache: %v", err)
	}
}
