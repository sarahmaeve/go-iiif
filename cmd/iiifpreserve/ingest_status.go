package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/source"
)

type ingestStatusSummary struct {
	Runs, Reused, Pending, Failed, IncompleteBundles int
}

func runIngestStatus(o *options, out, errOut *cliWriter) int {
	summary, err := reportIngestStatus(o.store, out)
	if err != nil {
		errOut.line("iiifpreserve: ingest status:", err)
		return 1
	}
	out.printf("iiifpreserve: ingest status: %d run(s), %d reused, %d pending, %d failed, %d incomplete bundle(s)\n",
		summary.Runs, summary.Reused, summary.Pending, summary.Failed, summary.IncompleteBundles)
	if out.err != nil || errOut.err != nil {
		return 1
	}
	return 0
}

func reportIngestStatus(root string, out *cliWriter) (ingestStatusSummary, error) {
	var summary ingestStatusSummary
	ingestDir := filepath.Join(root, ".iiifpreserve", "ingest")
	descriptors, err := filepath.Glob(filepath.Join(ingestDir, "*.json"))
	if err != nil {
		return summary, err
	}
	for _, descriptorPath := range descriptors {
		base := filepath.Base(descriptorPath)
		if strings.HasSuffix(base, ".frontier.json") || strings.HasSuffix(base, ".failures.json") {
			continue
		}
		b, err := os.ReadFile(descriptorPath) //nolint:gosec // path is from the configured ingest directory glob
		if err != nil {
			return summary, err
		}
		var descriptor ingestRunDescriptor
		if err := json.Unmarshal(b, &descriptor); err != nil {
			return summary, fmt.Errorf("decoding %s: %w", descriptorPath, err)
		}
		prefix := strings.TrimSuffix(descriptorPath, ".json")
		frontier, err := source.ReadCollectionFrontierStats(prefix + ".frontier.json")
		if err != nil {
			return summary, err
		}
		done, err := countCompletedURLs(prefix + ".done")
		if err != nil {
			return summary, err
		}
		failureState, err := openIngestFailures(prefix + ".failures.json")
		if err != nil {
			return summary, err
		}
		pending := max(0, frontier.Manifests-done)
		summary.Runs++
		summary.Reused += done
		summary.Pending += pending
		summary.Failed += failureState.Len()
		out.printf("run %s: %d reused, %d pending manifest(s), %d failed, %d pending collection(s), complete=%t\n",
			descriptor.Fingerprint[:min(12, len(descriptor.Fingerprint))], done, pending, failureState.Len(), frontier.PendingCollections, frontier.Complete)
		failedURLs := make([]string, 0, failureState.Len())
		for rawURL := range failureState.file.Failures {
			failedURLs = append(failedURLs, rawURL)
		}
		slices.Sort(failedURLs)
		for _, rawURL := range failedURLs {
			record := failureState.file.Failures[rawURL]
			out.printf("  failed manifest: %s :: %s (attempts %d)\n", rawURL, record.Message, record.Attempts)
		}
	}

	incomplete, err := findIncompleteBundles(root)
	if err != nil {
		return summary, err
	}
	for _, path := range incomplete {
		out.line("incomplete bundle:", path)
	}
	summary.IncompleteBundles = len(incomplete)
	return summary, nil
}

func countCompletedURLs(path string) (int, error) {
	f, err := os.Open(path) //nolint:gosec // query-scoped path under configured ingest directory
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if value := strings.TrimSpace(scanner.Text()); value != "" {
			seen[value] = struct{}{}
		}
	}
	return len(seen), scanner.Err()
}

func findIncompleteBundles(root string) ([]string, error) {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving library root: %w", err)
	}
	// Walk an fs.FS rooted at the configured library instead of passing an
	// operator-controlled path through every callback. WalkDir paths are now
	// slash-separated, relative names that cannot escape cleanRoot, and symlink
	// directories are not followed.
	rootFS := os.DirFS(cleanRoot)
	var incomplete []string
	err = fs.WalkDir(rootFS, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && name == ".iiifpreserve" {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != "manifest.json" {
			return nil
		}
		bundleDir := path.Dir(name)
		if _, err := fs.Stat(rootFS, path.Join(bundleDir, "provenance.json")); errors.Is(err, os.ErrNotExist) {
			incomplete = append(incomplete, bundleDir)
		} else if err != nil {
			return err
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return incomplete, err
}
