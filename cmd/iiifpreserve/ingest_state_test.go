package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarahmaeve/go-iiif/internal/institution"
	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/pipeline"
	"github.com/sarahmaeve/go-iiif/internal/source"
)

func TestIngestRunFingerprintUsesSelectionSemantics(t *testing.T) {
	registry := institution.Builtin()
	base := &options{
		collection: "https://example.org/collection",
		langs:      []string{"la", "fr", "fr"},
		hasDate:    true,
		from:       1400,
		to:         1500,
		places:     []string{"Paris", " venice "},
	}
	a, err := newIngestRunDescriptor(base, registry)
	if err != nil {
		t.Fatal(err)
	}
	equivalent := *base
	equivalent.langs = []string{"fr", "la"}
	equivalent.places = []string{"VENICE", "paris"}
	b, err := newIngestRunDescriptor(&equivalent, registry)
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint != b.Fingerprint {
		t.Fatalf("equivalent query fingerprints differ: %s != %s", a.Fingerprint, b.Fingerprint)
	}

	changed := equivalent
	changed.to = 1600
	c, err := newIngestRunDescriptor(&changed, registry)
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint == c.Fingerprint {
		t.Fatal("changed date filter reused the previous ingest fingerprint")
	}

	changedRegistry := institution.Builtin()
	changedRegistry.Default.FieldMapping["script"] = metadata.FieldLanguage
	d, err := newIngestRunDescriptor(base, changedRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint == d.Fingerprint {
		t.Fatal("changed institution mapping reused the previous ingest fingerprint")
	}
}

func TestOpenIngestStatePersistsCompletions(t *testing.T) {
	root := t.TempDir()
	o := &options{collection: "https://example.org/collection", langs: []string{"fr"}}
	first, err := openIngestState(root, o, institution.Builtin())
	if err != nil {
		t.Fatalf("open first state: %v", err)
	}
	const manifestURL = "https://example.org/manifest/1"
	if err := first.journal.MarkDone(manifestURL); err != nil {
		t.Fatal(err)
	}
	fingerprint := first.descriptor.Fingerprint
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := openIngestState(root, o, institution.Builtin())
	if err != nil {
		t.Fatalf("reopen state: %v", err)
	}
	if second.descriptor.Fingerprint != fingerprint || !second.journal.Done(manifestURL) {
		t.Fatalf("reopened state lost identity or completion: %+v", second.descriptor)
	}
	for _, suffix := range []string{".json", ".done"} {
		path := filepath.Join(root, ".iiifpreserve", "ingest", fingerprint+suffix)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing ingest state file %s: %v", path, err)
		}
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	changed, err := openIngestState(root, &options{
		collection: o.collection,
		langs:      []string{"la"},
	}, institution.Builtin())
	if err != nil {
		t.Fatalf("open changed-query state: %v", err)
	}
	defer func() { _ = changed.Close() }()
	if changed.descriptor.Fingerprint == fingerprint || changed.journal.Done(manifestURL) {
		t.Fatal("changed filter reused completions from a different ingest query")
	}
}

func TestIngestStateMigratesLegacyJournal(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(t.TempDir(), "crawl.journal")
	legacy, err := source.OpenFileJournal(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://example.org/m/1", "https://example.org/m/2"} {
		if err := legacy.MarkDone(u); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := openIngestState(root, &options{collection: "https://example.org/collection"}, institution.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	count, err := state.migrateLegacy(legacyPath)
	if err != nil || count != 2 {
		t.Fatalf("migration = %d, %v; want 2, nil", count, err)
	}
	if count, err = state.migrateLegacy(legacyPath); err != nil || count != 0 {
		t.Fatalf("repeat migration = %d, %v; want 0, nil", count, err)
	}
}

func TestOpenIngestStateFreshClearsCheckpointsOnly(t *testing.T) {
	root := t.TempDir()
	o := &options{collection: "https://example.org/collection"}
	state, err := openIngestState(root, o, institution.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.journal.MarkDone("https://example.org/manifest/1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.frontierPath, []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	journalPath, frontierPath := state.journalPath, state.frontierPath
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	freshOptions := *o
	freshOptions.fresh = true
	fresh, err := openIngestState(root, &freshOptions, institution.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	if got := fresh.journal.Entries(); len(got) != 0 {
		t.Fatalf("fresh journal retained %v", got)
	}
	if _, err := os.Stat(frontierPath); !os.IsNotExist(err) {
		t.Fatalf("fresh scan retained frontier %s: %v", frontierPath, err)
	}
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatalf("fresh scan did not recreate empty journal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".iiifpreserve", "ingest", fresh.descriptor.Fingerprint+".json")); err != nil {
		t.Fatalf("fresh scan removed query descriptor: %v", err)
	}
}

func TestCrawlMarksOnlyDurableRealRunOutcomes(t *testing.T) {
	const noMatchURL = "https://example.org/no-match"
	const matchURL = "https://example.org/match"
	results := func(yield func(pipeline.Result) bool) {
		if !yield(pipeline.Result{ManifestURL: noMatchURL, Class: metadata.NoMatch}) {
			return
		}
		yield(pipeline.Result{ManifestURL: matchURL, Class: metadata.Match, Manifest: []byte(`{"sequences":[{"canvases":[]}]}`)})
	}
	journal := source.NewMemoryJournal()
	store := &recordingStore{}
	crawl(context.Background(), results, journal, nil, &recordingFetcher{}, store, false, 0, 0,
		&cliWriter{w: io.Discard}, &cliWriter{w: io.Discard})
	if !journal.Done(noMatchURL) || !journal.Done(matchURL) {
		t.Fatalf("durable outcomes not journaled: no-match=%v match=%v", journal.Done(noMatchURL), journal.Done(matchURL))
	}

	dryJournal := source.NewMemoryJournal()
	dryResults := func(yield func(pipeline.Result) bool) {
		yield(pipeline.Result{ManifestURL: noMatchURL, Class: metadata.NoMatch})
	}
	crawl(context.Background(), dryResults, dryJournal, nil, &recordingFetcher{}, store, true, 0, 0,
		&cliWriter{w: io.Discard}, &cliWriter{w: io.Discard})
	if dryJournal.Done(noMatchURL) {
		t.Fatal("dry-run changed ingest completion state")
	}

	failedJournal := source.NewMemoryJournal()
	failedResults := func(yield func(pipeline.Result) bool) {
		yield(pipeline.Result{ManifestURL: "https://example.org/failed", Err: errBoom})
	}
	crawl(context.Background(), failedResults, failedJournal, nil, &recordingFetcher{}, store, false, 0, 0,
		&cliWriter{w: io.Discard}, &cliWriter{w: io.Discard})
	if failedJournal.Done("https://example.org/failed") {
		t.Fatal("failed manifest was marked complete")
	}
}

func TestCrawlPersistsAndClearsFailureState(t *testing.T) {
	failures, err := openIngestFailures(filepath.Join(t.TempDir(), "failures.json"))
	if err != nil {
		t.Fatal(err)
	}
	const manifestURL = "https://example.org/manifest"
	failed := func(yield func(pipeline.Result) bool) {
		yield(pipeline.Result{ManifestURL: manifestURL, Err: errBoom})
	}
	crawl(context.Background(), failed, source.NewMemoryJournal(), failures, &recordingFetcher{}, &recordingStore{}, false, 0, 0,
		&cliWriter{w: io.Discard}, &cliWriter{w: io.Discard})
	if failures.Len() != 1 {
		t.Fatalf("failure count = %d, want 1", failures.Len())
	}
	reopened, err := openIngestFailures(failures.path)
	if err != nil || reopened.Len() != 1 {
		t.Fatalf("reopened failures = %+v, %v", reopened, err)
	}

	succeeded := func(yield func(pipeline.Result) bool) {
		yield(pipeline.Result{ManifestURL: manifestURL, Class: metadata.NoMatch})
	}
	journal := source.NewMemoryJournal()
	crawl(context.Background(), succeeded, journal, reopened, &recordingFetcher{}, &recordingStore{}, false, 0, 0,
		&cliWriter{w: io.Discard}, &cliWriter{w: io.Discard})
	if reopened.Len() != 0 || !journal.Done(manifestURL) {
		t.Fatalf("successful retry left failures=%d done=%v", reopened.Len(), journal.Done(manifestURL))
	}
}
