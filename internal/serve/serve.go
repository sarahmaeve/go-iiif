// Package serve exposes a preserved BlobStore tree over HTTP(S) as static
// files — DESIGN §3 serve half, step 1. It does no IIIF logic: manifests are
// served exactly as preserved (URL rewrite is the deferred step 3). Lifecycle
// shape follows the signatory pipeline server.
package serve

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sarahmaeve/go-iiif/internal/annotation"
)

// Server serves a preserved-bundle directory tree as static files, bound to
// localhost.
type Server struct {
	root    string
	server  *http.Server
	catalog *catalog
	// logf, when set, receives one line per HTTP request as
	// "METHOD STATUS PATH" (printf-style). Used to diagnose what a viewer
	// actually requests against the rewritten manifest / tile pyramid.
	logf func(format string, args ...any)
}

// New returns a Server rooted at the preserved-bundle directory, logging
// one line per request to stderr.
func New(root string) *Server {
	lg := log.New(os.Stderr, "iiifserve ", log.LstdFlags)
	s := &Server{root: root, catalog: newCatalog(root), logf: lg.Printf}
	if s.catalog.loadErr != nil {
		lg.Printf("%v; preserving the file unchanged and disabling catalogue edits", s.catalog.loadErr)
	}
	return s
}

// statusRecorder captures the response status so it can be logged. It
// defaults to 200 — the status implied when a handler writes a body
// without an explicit WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logRequests wraps next, emitting "METHOD STATUS PATH" via s.logf after
// each request. A no-op wrapper when logf is nil.
func (s *Server) logRequests(next http.Handler) http.Handler {
	if s.logf == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logf("%s %d %s", r.Method, rec.status, r.URL.Path)
	})
}

// Handler serves the preserved tree as static files, except a
// `*/manifest.json` request is rewritten on the fly so its image URLs point
// at this server's local copies (provenance-driven; the stored file stays
// pristine). http.Dir cleans paths, so ".." traversal is rejected by stdlib.
func (s *Server) Handler() http.Handler {
	files := http.FileServer(http.Dir(s.root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(r.URL.Path)
		if clean == catalogEditRoute {
			s.handleCatalogEdit(w, r)
			return
		}
		// The persistent catalogue is application state, not a static asset.
		if clean == "/"+catalogDirName || strings.HasPrefix(clean, "/"+catalogDirName+"/") {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(clean, "/annotations") {
			s.handleAnnotations(w, r, clean)
			return
		}
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
			// Not a manifest bundle: refuse rather than let http.FileServer
			// render a directory listing that exposes the preserved tree.
			http.NotFound(w, r)
			return
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

// writeJSON encodes v as the JSON response with status, without HTML
// escaping so URLs (e.g. the annotation endpoint's "&"-bearing id) stay
// literal — valid JSON either way, but consistent with the manifest.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) //nolint:errcheck // best-effort response write; client disconnect is not actionable
}

// newAnnotationID mints an opaque, collision-free id for a client-created
// annotation that didn't supply one.
func newAnnotationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil { // crypto/rand failure is unrecoverable; fall back to time
		return fmt.Sprintf("urn:annotation:%d", time.Now().UnixNano())
	}
	return "urn:uuid:" + hex.EncodeToString(b[:])
}

// readAnnotation parses (size-capped) the request body into an annotation,
// requiring a Canvas target. It writes the 400 itself and returns ok=false
// on any problem.
func (s *Server) readAnnotation(w http.ResponseWriter, r *http.Request) (annotation.Annotation, bool) {
	var a annotation.Annotation
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "annotation too large or unreadable", http.StatusBadRequest)
		return a, false
	}
	if err := json.Unmarshal(body, &a); err != nil {
		http.Error(w, "invalid annotation JSON", http.StatusBadRequest)
		return a, false
	}
	if len(a.Target) == 0 || a.CanvasID() == "" {
		http.Error(w, "annotation needs a Canvas target", http.StatusBadRequest)
		return a, false
	}
	if a.Type == "" {
		a.Type = "Annotation"
	}
	return a, true
}

// handleAnnotations is the per-bundle annotation REST surface backing the
// offline store (and the Mirador storage adapter): GET list / POST create /
// PUT update / DELETE. Loopback-only, single-user, so no auth — the same
// trust model as the rest of serving. Mutations are displayed via the
// existing serve-time injection (the viewer re-fetches the manifest to see
// them).
func (s *Server) handleAnnotations(w http.ResponseWriter, r *http.Request, clean string) {
	dir, ok := s.hasManifest(strings.TrimSuffix(clean, "/annotations"))
	if !ok {
		http.NotFound(w, r) // only a real preserved bundle is annotatable
		return
	}
	annDir := filepath.Join(s.root, filepath.FromSlash(dir))

	switch r.Method {
	case http.MethodGet:
		page, err := annotation.Load(annDir)
		if err != nil {
			http.Error(w, "could not read annotation store", http.StatusInternalServerError)
			return
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		selfURL := scheme + "://" + r.Host + r.URL.RequestURI() // id == fetched URL

		q := r.URL.Query()
		canvas := q.Get("canvas")
		if canvas == "" {
			// Whole store as a W3C AnnotationPage — the MAE adapter "all".
			page.ID = selfURL
			writeJSON(w, http.StatusOK, page)
			return
		}

		// Per-canvas, in the shape Mirador's loader expects for this
		// manifest version (the reference injected into the manifest).
		sub := make([]annotation.Annotation, 0, len(page.Items))
		for _, a := range page.Items {
			if a.CanvasID() == canvas {
				sub = append(sub, a)
			}
		}
		if q.Get("fmt") == "oa" {
			res := make([]any, 0, len(sub))
			for _, a := range sub {
				res = append(res, toOpenAnnotation(a))
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"@context":  "http://iiif.io/api/presentation/2/context.json",
				"@id":       selfURL,
				"@type":     "sc:AnnotationList",
				"resources": res,
			})
			return
		}
		writeJSON(w, http.StatusOK, annotation.Page{
			Context: "http://iiif.io/api/presentation/3/context.json",
			ID:      selfURL, Type: "AnnotationPage", Items: sub,
		})

	case http.MethodPost:
		a, ok := s.readAnnotation(w, r)
		if !ok {
			return
		}
		if a.ID == "" {
			a.ID = newAnnotationID()
		}
		if err := annotation.Add(annDir, a); err != nil {
			http.Error(w, "could not save annotation", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, a)

	case http.MethodPut:
		a, ok := s.readAnnotation(w, r)
		if !ok {
			return
		}
		if a.ID == "" {
			http.Error(w, "update needs the annotation id", http.StatusBadRequest)
			return
		}
		switch err := annotation.Update(annDir, a); {
		case errors.Is(err, annotation.ErrNotFound):
			http.NotFound(w, r)
		case err != nil:
			http.Error(w, "could not update annotation", http.StatusInternalServerError)
		default:
			writeJSON(w, http.StatusOK, a)
		}

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "delete needs ?id=", http.StatusBadRequest)
			return
		}
		switch err := annotation.Delete(annDir, id); {
		case errors.Is(err, annotation.ErrNotFound):
			http.NotFound(w, r)
		case err != nil:
			http.Error(w, "could not delete annotation", http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNoContent)
		}

	default:
		w.Header().Set("Allow", "GET, POST, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	base := scheme + "://" + r.Host + strings.TrimSuffix(clean, "/manifest.json")

	out := manifest
	if pf, perr := dir.Open(path.Join(path.Dir(clean), "provenance.json")); perr == nil {
		prov, _ := io.ReadAll(pf)
		_ = pf.Close()
		bundleDir := filepath.Join(s.root, filepath.FromSlash(path.Dir(clean)))
		if rw, rerr := rewriteManifest(manifest, prov, base, bundleDir); rerr == nil {
			out = rw
		}
	}

	// Annotations are NOT injected into the served manifest. The embedded
	// viewer is Mirador + MAE, whose storage adapter loads annotations
	// from /<dir>/annotations and dispatches them for display itself
	// (MAE's receiveAnnotation saga). Injecting a manifest reference too
	// would make Mirador core fetch the same page independently — every
	// stored annotation would render twice. The REST endpoint is the
	// single source of truth for display, create, and edit.

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
	s.catalog.startSizeRefresh(ctx)
	defer s.catalog.wait()
	s.server = &http.Server{
		Handler:           s.logRequests(s.Handler()),
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
