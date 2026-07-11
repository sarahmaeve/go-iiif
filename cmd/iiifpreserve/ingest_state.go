package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/institution"
	"github.com/sarahmaeve/go-iiif/internal/metadata"
	"github.com/sarahmaeve/go-iiif/internal/source"
)

const ingestStateVersion = 1

type ingestRunDescriptor struct {
	Version         int      `json:"version"`
	Fingerprint     string   `json:"fingerprint"`
	Collection      string   `json:"collection"`
	Languages       []string `json:"languages,omitempty"`
	HasDate         bool     `json:"has_date,omitempty"`
	From            int      `json:"from,omitempty"`
	To              int      `json:"to,omitempty"`
	Places          []string `json:"places,omitempty"`
	FilterVersion   int      `json:"filter_version"`
	PreserveVersion int      `json:"preserve_version"`
	MappingDigest   string   `json:"mapping_digest"`
}

type ingestFingerprintInput struct {
	Version         int      `json:"version"`
	Collection      string   `json:"collection"`
	Languages       []string `json:"languages,omitempty"`
	HasDate         bool     `json:"has_date,omitempty"`
	From            int      `json:"from,omitempty"`
	To              int      `json:"to,omitempty"`
	Places          []string `json:"places,omitempty"`
	FilterVersion   int      `json:"filter_version"`
	PreserveVersion int      `json:"preserve_version"`
	MappingDigest   string   `json:"mapping_digest"`
}

type ingestState struct {
	descriptor   ingestRunDescriptor
	journalPath  string
	frontierPath string
	journal      *source.FileJournal
}

func newIngestRunDescriptor(o *options, registry institution.Registry) (ingestRunDescriptor, error) {
	input := ingestFingerprintInput{
		Version:         ingestStateVersion,
		Collection:      strings.TrimSpace(o.collection),
		Languages:       normalizedSet(o.langs, false),
		HasDate:         o.hasDate,
		From:            o.from,
		To:              o.to,
		Places:          normalizedSet(o.places, true),
		FilterVersion:   1,
		PreserveVersion: 1,
		MappingDigest:   mappingDigest(registry),
	}
	b, err := json.Marshal(input)
	if err != nil {
		return ingestRunDescriptor{}, fmt.Errorf("ingest state: encoding fingerprint: %w", err)
	}
	sum := sha256.Sum256(b)
	return ingestRunDescriptor{
		Version:         input.Version,
		Fingerprint:     hex.EncodeToString(sum[:]),
		Collection:      input.Collection,
		Languages:       input.Languages,
		HasDate:         input.HasDate,
		From:            input.From,
		To:              input.To,
		Places:          input.Places,
		FilterVersion:   input.FilterVersion,
		PreserveVersion: input.PreserveVersion,
		MappingDigest:   input.MappingDigest,
	}, nil
}

func normalizedSet(values []string, foldCase bool) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if foldCase {
			value = strings.ToLower(value)
		}
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

type mappingFingerprintEntry struct {
	Host   string                    `json:"host"`
	Fields []mappingFingerprintField `json:"fields"`
}

type mappingFingerprintField struct {
	Label string             `json:"label"`
	Kind  metadata.FieldKind `json:"kind"`
}

func mappingDigest(registry institution.Registry) string {
	hosts := make([]string, 0, len(registry.ByHost))
	for host := range registry.ByHost {
		hosts = append(hosts, host)
	}
	slices.Sort(hosts)
	entries := []mappingFingerprintEntry{{Host: "*", Fields: canonicalMapping(registry.Default.FieldMapping)}}
	for _, host := range hosts {
		entries = append(entries, mappingFingerprintEntry{Host: host, Fields: canonicalMapping(registry.ByHost[host].FieldMapping)})
	}
	b, _ := json.Marshal(entries)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func canonicalMapping(mapping metadata.FieldMapping) []mappingFingerprintField {
	labels := make([]string, 0, len(mapping))
	for label := range mapping {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	out := make([]mappingFingerprintField, 0, len(labels))
	for _, label := range labels {
		out = append(out, mappingFingerprintField{Label: label, Kind: mapping[label]})
	}
	return out
}

func openIngestState(root string, o *options, registry institution.Registry) (*ingestState, error) {
	descriptor, err := newIngestRunDescriptor(o, registry)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, ".iiifpreserve", "ingest")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("ingest state: creating directory: %w", err)
	}
	descriptorPath := filepath.Join(dir, descriptor.Fingerprint+".json")
	want, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("ingest state: encoding descriptor: %w", err)
	}
	want = append(want, '\n')
	if got, readErr := os.ReadFile(descriptorPath); readErr == nil { //nolint:gosec // derived path under configured store
		if string(got) != string(want) {
			return nil, fmt.Errorf("ingest state: descriptor %s does not match its query fingerprint", descriptorPath)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, fmt.Errorf("ingest state: reading descriptor: %w", readErr)
	} else if err := writeIngestFile(descriptorPath, want); err != nil {
		return nil, err
	}

	journalPath := filepath.Join(dir, descriptor.Fingerprint+".done")
	frontierPath := filepath.Join(dir, descriptor.Fingerprint+".frontier.json")
	if o.fresh {
		// Delete discovery first: interruption between these removals may
		// repeat fewer old manifests than requested, but can never retain a
		// completed frontier that hides newly added upstream manuscripts.
		for _, path := range []string{frontierPath, journalPath} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("ingest state: resetting %s: %w", path, err)
			}
		}
	}
	journal, err := source.OpenFileJournal(journalPath)
	if err != nil {
		return nil, err
	}
	return &ingestState{descriptor: descriptor, journalPath: journalPath, frontierPath: frontierPath, journal: journal}, nil
}

func writeIngestFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ingest-*.tmp")
	if err != nil {
		return fmt.Errorf("ingest state: creating descriptor: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("ingest state: writing descriptor: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("ingest state: closing descriptor: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("ingest state: finalizing descriptor: %w", err)
	}
	return nil
}

func (s *ingestState) Close() error { return s.journal.Close() }

func (s *ingestState) migrateLegacy(path string) (int, error) {
	legacyAbs, err := filepath.Abs(path)
	if err != nil {
		return 0, fmt.Errorf("ingest state: resolving legacy journal: %w", err)
	}
	targetAbs, err := filepath.Abs(s.journalPath)
	if err != nil {
		return 0, fmt.Errorf("ingest state: resolving automatic journal: %w", err)
	}
	if legacyAbs == targetAbs {
		return 0, nil
	}
	legacy, err := source.OpenFileJournal(path)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, manifestURL := range legacy.Entries() {
		if s.journal.Done(manifestURL) {
			continue
		}
		if err := s.journal.MarkDone(manifestURL); err != nil {
			_ = legacy.Close()
			return count, err
		}
		count++
	}
	return count, legacy.Close()
}
