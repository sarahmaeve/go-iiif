package serve

import "errors"

// ErrLibraryBusy means another serving/import process currently owns the
// researcher-metadata write lock.
var ErrLibraryBusy = errors.New("library research metadata is in use; stop the running server and retry")
