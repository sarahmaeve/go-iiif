// Package serve exposes a preserved BlobStore tree over HTTP(S) as static
// files — DESIGN §3 serve half, step 1. It does no IIIF logic: manifests are
// served exactly as preserved (URL rewrite is the deferred step 3). Lifecycle
// shape follows the signatory pipeline server.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
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

// Handler serves the preserved tree as static files, except a
// `*/manifest.json` request is rewritten on the fly so its image URLs point
// at this server's local copies (provenance-driven; the stored file stays
// pristine). http.Dir cleans paths, so ".." traversal is rejected by stdlib.
func (s *Server) Handler() http.Handler {
	files := http.FileServer(http.Dir(s.root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(r.URL.Path)
		switch {
		case clean == miradorRoute:
			s.serveBundle(w)
			return
		case strings.HasPrefix(clean, fontsRoutePrefix):
			s.serveFont(w, r, files)
			return
		case r.URL.Path == "/":
			s.serveIndex(w)
			return
		case strings.HasSuffix(clean, "/manifest.json"):
			s.serveManifest(w, r, files)
			return
		case strings.HasSuffix(clean, "/info.json"):
			s.serveInfoJSON(w, r, files)
			return
		}
		// A "/<dir>/" request for a preserved manifest renders the embedded
		// Mirador viewer instead of the stdlib directory listing.
		if strings.HasSuffix(r.URL.Path, "/") {
			if dir, ok := s.hasManifest(clean); ok {
				s.serveViewer(w, dir)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

// serveInfoJSON serves a stored level0 info.json with its `id` set to the
// request URL base. IIIF requires info.json `id` to equal the URL it is
// served from; the stored file holds a placeholder (the host is unknown at
// preserve time). Falls back to the raw file on any error.
func (s *Server) serveInfoJSON(w http.ResponseWriter, r *http.Request, files http.Handler) {
	dir := http.Dir(s.root)
	clean := path.Clean(r.URL.Path)

	f, err := dir.Open(clean)
	if err != nil {
		files.ServeHTTP(w, r)
		return
	}
	raw, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		files.ServeHTTP(w, r)
		return
	}

	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		files.ServeHTTP(w, r)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	doc["id"] = scheme + "://" + r.Host + strings.TrimSuffix(clean, "/info.json")
	out, err := json.Marshal(doc)
	if err != nil {
		files.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out) //nolint:errcheck // best-effort response write; client disconnect is not actionable
}

// serveManifest serves the preserved manifest with image URLs rewritten to
// this server. If provenance is absent or rewrite fails, it falls back to
// serving the manifest untouched — serving must not break on a rewrite issue.
func (s *Server) serveManifest(w http.ResponseWriter, r *http.Request, files http.Handler) {
	dir := http.Dir(s.root)
	clean := path.Clean(r.URL.Path)

	mf, err := dir.Open(clean)
	if err != nil {
		files.ServeHTTP(w, r) // let the file server produce the 404
		return
	}
	manifest, err := io.ReadAll(mf)
	_ = mf.Close()
	if err != nil {
		files.ServeHTTP(w, r)
		return
	}

	out := manifest
	if pf, perr := dir.Open(path.Join(path.Dir(clean), "provenance.json")); perr == nil {
		prov, _ := io.ReadAll(pf)
		_ = pf.Close()
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base := scheme + "://" + r.Host + strings.TrimSuffix(clean, "/manifest.json")
		if rw, rerr := rewriteManifest(manifest, prov, base); rerr == nil {
			out = rw
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out) //nolint:errcheck // best-effort response write; client disconnect is not actionable
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
