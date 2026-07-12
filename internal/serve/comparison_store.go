package serve

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	comparisonFileName  = "comparisons.json"
	comparisonVersion   = 1
	maxSavedComparisons = 100
)

var ErrComparisonNameExists = errors.New("comparison name already exists")

type savedComparison struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Docs      []string  `json:"docs"`
	Canvases  []string  `json:"canvases,omitempty"`
	SyncPage  bool      `json:"sync_page,omitempty"`
	SyncView  bool      `json:"sync_view,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type comparisonFile struct {
	Version int               `json:"version"`
	Sets    []savedComparison `json:"sets"`
}

type comparisonStore struct {
	path    string
	mu      sync.RWMutex
	sets    []savedComparison
	loadErr error
}

func newComparisonStore(root string) *comparisonStore {
	s := &comparisonStore{path: filepath.Join(root, catalogDirName, comparisonFileName)}
	b, err := os.ReadFile(s.path) //nolint:gosec // fixed state file below configured library root
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	if err != nil {
		s.loadErr = fmt.Errorf("comparisons: reading: %w", err)
		return s
	}
	var file comparisonFile
	if err := json.Unmarshal(b, &file); err != nil {
		s.loadErr = fmt.Errorf("comparisons: decoding: %w", err)
		return s
	}
	if file.Version != comparisonVersion {
		s.loadErr = fmt.Errorf("comparisons: unsupported version %d", file.Version)
		return s
	}
	s.sets = file.Sets
	return s
}

func (s *comparisonStore) list() []savedComparison {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]savedComparison, len(s.sets))
	copy(out, s.sets)
	for i := range out {
		out[i].Docs = append([]string(nil), out[i].Docs...)
		out[i].Canvases = append([]string(nil), out[i].Canvases...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *comparisonStore) add(set savedComparison) (savedComparison, error) {
	set.Name = strings.TrimSpace(set.Name)
	if set.Name == "" || utf8.RuneCountInString(set.Name) > 200 {
		return savedComparison{}, errors.New("comparison name must contain 1–200 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return savedComparison{}, s.loadErr
	}
	if len(s.sets) >= maxSavedComparisons {
		return savedComparison{}, fmt.Errorf("at most %d comparisons may be saved", maxSavedComparisons)
	}
	for _, existing := range s.sets {
		if strings.EqualFold(existing.Name, set.Name) {
			return savedComparison{}, ErrComparisonNameExists
		}
	}
	if set.ID == "" {
		set.ID = newComparisonID()
	}
	if set.CreatedAt.IsZero() {
		set.CreatedAt = time.Now().UTC()
	}
	set.Docs = append([]string(nil), set.Docs...)
	set.Canvases = append([]string(nil), set.Canvases...)
	s.sets = append(s.sets, set)
	if err := s.saveLocked(); err != nil {
		s.sets = s.sets[:len(s.sets)-1]
		return savedComparison{}, err
	}
	return set, nil
}

func (s *comparisonStore) delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return s.loadErr
	}
	for i := range s.sets {
		if s.sets[i].ID != id {
			continue
		}
		before := append([]savedComparison(nil), s.sets...)
		s.sets = append(s.sets[:i], s.sets[i+1:]...)
		if err := s.saveLocked(); err != nil {
			s.sets = before
			return err
		}
		return nil
	}
	return os.ErrNotExist
}

func (s *comparisonStore) saveLocked() error {
	file := comparisonFile{Version: comparisonVersion, Sets: s.sets}
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("comparisons: encoding: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("comparisons: creating state directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".comparisons-*.json")
	if err != nil {
		return fmt.Errorf("comparisons: creating temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("comparisons: writing: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("comparisons: closing: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("comparisons: finalizing: %w", err)
	}
	return nil
}

func newComparisonID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("comparison-%d", time.Now().UnixNano())
}
