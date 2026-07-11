//go:build windows

package serve

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type libraryWriteLock struct {
	file *os.File
	path string
}

func acquireLibraryWriteLock(root string) (*libraryWriteLock, error) {
	dir := filepath.Join(root, catalogDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("library lock: creating state directory: %w", err)
	}
	path := filepath.Join(dir, "research.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600) //nolint:gosec // fixed state file under configured root
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, ErrLibraryBusy
		}
		return nil, fmt.Errorf("library lock: opening: %w", err)
	}
	return &libraryWriteLock{file: f, path: path}, nil
}

func (l *libraryWriteLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	removeErr := os.Remove(l.path)
	if err != nil {
		return err
	}
	return removeErr
}
