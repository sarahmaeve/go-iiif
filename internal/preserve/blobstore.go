package preserve

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// BlobStore persists preservation artifacts under string keys (slash-separated
// paths). Local first; the interface keeps other backends possible later.
type BlobStore interface {
	Put(ctx context.Context, key string, data []byte) error
	Exists(ctx context.Context, key string) (bool, error)
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

func (s *LocalBlobStore) path(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}

func (s *LocalBlobStore) Put(_ context.Context, key string, data []byte) error {
	dst := s.path(key)
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

func (s *LocalBlobStore) Exists(_ context.Context, key string) (bool, error) {
	_, err := os.Stat(s.path(key))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("preserve: stat %s: %w", key, err)
}
