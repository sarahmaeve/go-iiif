package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

const ingestFailuresVersion = 1

type ingestFailure struct {
	Message  string    `json:"message"`
	Attempts int       `json:"attempts"`
	Updated  time.Time `json:"updated"`
}

type ingestFailureFile struct {
	Version  int                      `json:"version"`
	Failures map[string]ingestFailure `json:"failures,omitempty"`
}

type ingestFailures struct {
	path string
	file ingestFailureFile
}

func openIngestFailures(path string) (*ingestFailures, error) {
	f := ingestFailureFile{Version: ingestFailuresVersion, Failures: make(map[string]ingestFailure)}
	b, err := os.ReadFile(path) //nolint:gosec // query-scoped path under configured store
	if errors.Is(err, os.ErrNotExist) {
		return &ingestFailures{path: path, file: f}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ingest state: reading failures: %w", err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("ingest state: decoding failures: %w", err)
	}
	if f.Version != ingestFailuresVersion {
		return nil, fmt.Errorf("ingest state: unsupported failures version %d", f.Version)
	}
	if f.Failures == nil {
		f.Failures = make(map[string]ingestFailure)
	}
	return &ingestFailures{path: path, file: f}, nil
}

func (f *ingestFailures) MarkFailed(rawURL string, cause error) error {
	if rawURL == "" {
		return nil
	}
	record := f.file.Failures[rawURL]
	record.Message = cause.Error()
	record.Attempts++
	record.Updated = time.Now().UTC()
	f.file.Failures[rawURL] = record
	return f.save()
}

func (f *ingestFailures) ClearFailure(rawURL string) error {
	if _, ok := f.file.Failures[rawURL]; !ok {
		return nil
	}
	delete(f.file.Failures, rawURL)
	return f.save()
}

func (f *ingestFailures) Len() int { return len(f.file.Failures) }

func (f *ingestFailures) save() error {
	b, err := json.MarshalIndent(f.file, "", "  ")
	if err != nil {
		return fmt.Errorf("ingest state: encoding failures: %w", err)
	}
	return writeIngestFile(f.path, append(b, '\n'))
}
