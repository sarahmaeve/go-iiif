//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package serve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type libraryWriteLock struct{ file *os.File }

func acquireLibraryWriteLock(root string) (*libraryWriteLock, error) {
	dir := filepath.Join(root, catalogDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("library lock: creating state directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "research.lock"), os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // fixed state file under configured root
	if err != nil {
		return nil, fmt.Errorf("library lock: opening: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLibraryBusy
		}
		return nil, fmt.Errorf("library lock: acquiring: %w", err)
	}
	return &libraryWriteLock{file: f}, nil
}

func (l *libraryWriteLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
