// Package serve exposes a preserved BlobStore tree over HTTP(S) as static
// files — DESIGN §3 serve half, step 1. It does no IIIF logic: manifests are
// served exactly as preserved (URL rewrite is the deferred step 3). Lifecycle
// shape follows the signatory pipeline server.
package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Server serves a preserved-bundle directory tree as static files, bound to
// localhost.
type Server struct {
	root   string
	server *http.Server
}

// New returns a Server rooted at the preserved-bundle directory.
func New(root string) *Server {
	return &Server{root: root}
}

// Handler is the static file handler (exposed for tests). http.Dir cleans
// paths, so ".." traversal outside root is rejected by the stdlib.
func (s *Server) Handler() http.Handler {
	return http.FileServer(http.Dir(s.root))
}

// ListenAndServe binds addr (forced to loopback) and serves until ctx is
// cancelled. TLS is used when both certFile and keyFile are non-empty.
func (s *Server) ListenAndServe(ctx context.Context, addr, certFile, keyFile string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("serve: listen %s: %w", addr, err)
	}
	return s.Serve(ctx, ln, certFile, keyFile)
}

// Serve serves on ln until ctx is cancelled, then shuts down gracefully.
// Returns nil on a clean shutdown.
func (s *Server) Serve(ctx context.Context, ln net.Listener, certFile, keyFile string) error {
	s.server = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	// Graceful shutdown. shutdownCtx is rooted at Background deliberately:
	// the parent ctx is already cancelled by the time we get here, so
	// deriving from it would abort the drain immediately.
	go func() { //nolint:gosec // G118: shutdownCtx deliberately rooted at Background — parent ctx is already cancelled here, deriving from it would abort the drain
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx) //nolint:contextcheck // see comment above: Background-rooted on purpose
	}()

	var err error
	if certFile != "" && keyFile != "" {
		err = s.server.ServeTLS(ln, certFile, keyFile)
	} else {
		err = s.server.Serve(ln)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
