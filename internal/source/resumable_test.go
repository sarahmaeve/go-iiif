package source

import (
	"context"
	"iter"
	"os"
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

func TestFileJournal_DiscardsInterruptedTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crawl.journal")
	const complete = "https://h.example.org/complete.json"
	if err := os.WriteFile(path, []byte(complete+"\nhttps://h.example.org/part"), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := OpenFileJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if !j.Done(complete) || j.Done("https://h.example.org/part") {
		t.Fatalf("journal recovery complete=%v partial=%v", j.Done(complete), j.Done("https://h.example.org/part"))
	}
	const next = "https://h.example.org/next.json"
	if err := j.MarkDone(next); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := complete + "\n" + next + "\n"
	if string(b) != want {
		t.Fatalf("repaired journal = %q, want %q", b, want)
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
	if got := j1.Entries(); len(got) != 1 || got[0] != url {
		t.Fatalf("Entries = %v, want [%s]", got, url)
	}
	if err := j1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
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
	if err := j2.Close(); err != nil {
		t.Fatalf("Close reopened journal: %v", err)
	}
}
