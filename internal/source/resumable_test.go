package source

import (
	"context"
	"iter"
	"path/filepath"
	"testing"
)

// staticSource yields a fixed list of manifest URLs.
type staticSource []string

func (s staticSource) Manifests(context.Context) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for _, u := range s {
			if !yield(u, nil) {
				return
			}
		}
	}
}

func TestResumableSource_SkipsCompletedManifests(t *testing.T) {
	all := staticSource{
		"https://h.example.org/a/manifest.json",
		"https://h.example.org/b/manifest.json",
		"https://h.example.org/c/manifest.json",
	}

	j := NewMemoryJournal()
	if err := j.MarkDone("https://h.example.org/b/manifest.json"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	src := NewResumableSource(all, j)
	got := collect(t, src)

	want := []string{
		"https://h.example.org/a/manifest.json",
		"https://h.example.org/c/manifest.json",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v (b already done)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("manifest[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFileJournal_PersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crawl.journal")

	j1, err := OpenFileJournal(path)
	if err != nil {
		t.Fatalf("OpenFileJournal: %v", err)
	}
	const url = "https://h.example.org/a/manifest.json"
	if j1.Done(url) {
		t.Fatal("fresh journal reports url done")
	}
	if err := j1.MarkDone(url); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	// Reopen from the same path: completion must survive a process restart.
	j2, err := OpenFileJournal(path)
	if err != nil {
		t.Fatalf("reopen OpenFileJournal: %v", err)
	}
	if !j2.Done(url) {
		t.Fatal("completed url not durable across reopen")
	}
	if j2.Done("https://h.example.org/other.json") {
		t.Fatal("unrelated url reported done")
	}
}
