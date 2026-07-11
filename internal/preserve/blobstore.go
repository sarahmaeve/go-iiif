package preserve

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrKeyEscapesRoot reports a blob key that resolves outside the store root.
// dirFor already sanitizes keys; this is defense in depth so a future caller
// (or a bug there) cannot make the store read or write arbitrary paths.
var ErrKeyEscapesRoot = errors.New("preserve: blob key escapes store root")

// BlobStore persists preservation artifacts under string keys (slash-separated
// paths). Local first; the interface keeps other backends possible later.
type BlobStore interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Exists(ctx context.Context, key string) (bool, error)
	Delete(ctx context.Context, key string) error
}

// LocalBlobStore writes blobs as files under a root directory. Writes are
// atomic (temp file + rename in the destination dir) so an interrupted Put
// never leaves a partial blob that later looks valid.
type LocalBlobStore struct {
	root string
}

// NewLocalBlobStore returns a BlobStore rooted at dir.
func NewLocalBlobStore(dir string) *LocalBlobStore {
	return &LocalBlobStore{root: dir}
}

func (s *LocalBlobStore) path(key string) (string, error) {
	p := filepath.Join(s.root, filepath.FromSlash(key))
	if p != s.root && !strings.HasPrefix(p, s.root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrKeyEscapesRoot, key)
	}
	return p, nil
}

func (s *LocalBlobStore) Put(_ context.Context, key string, data []byte) error {
	dst, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("preserve: creating dir for %s: %w", key, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return fmt.Errorf("preserve: temp file for %s: %w", key, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("preserve: writing %s: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("preserve: closing %s: %w", key, err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("preserve: finalizing %s: %w", key, err)
	}
	return nil
}

func (s *LocalBlobStore) Get(_ context.Context, key string) ([]byte, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p) //nolint:gosec // path is confined to the configured store root
	if err != nil {
		return nil, fmt.Errorf("preserve: reading %s: %w", key, err)
	}
	return b, nil
}

func (s *LocalBlobStore) Exists(_ context.Context, key string) (bool, error) {
	p, err := s.path(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("preserve: stat %s: %w", key, err)
}

func (s *LocalBlobStore) Delete(_ context.Context, key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("preserve: deleting %s: %w", key, err)
	}
	return nil
}
