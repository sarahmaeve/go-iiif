package serve

import (
	_ "embed"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// miradorBundle is the vendored prebuilt Mirador 4 UMD bundle, embedded so
// the binary needs no external viewer and no Node runtime (DESIGN §2). Pinned
// to mirador@4.0.0 (dist/mirador.min.js from unpkg).
//
//go:embed viewer/mirador.min.js
var miradorBundle []byte

// miradorRoute is the reserved path the embedded bundle is served from. The
// double-underscore prefix cannot collide with a preserved-manifest dir slug
// (institution host + IIIF path, e.g. "bodleian-c481").
const miradorRoute = "/__viewer__/mirador.min.js"

// indexTmpl is the landing page: a researcher with no external viewer lands
// here and clicks into any preserved manifest.
var indexTmpl = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Preserved IIIF manifests</title>
<body>
<h1>Preserved IIIF manifests</h1>
<ul>
{{range .}}<li><a href="/{{.}}/">{{.}}</a></li>
{{end}}</ul>
</body>
</html>
`))

// viewerTmpl renders Mirador against this dir's local (serve-time-rewritten)
// manifest. {{.}} is the dir slug; html/template escapes it for both the
// HTML and the JS string contexts.
var viewerTmpl = template.Must(template.New("viewer").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.}} — Mirador</title>
<style>html,body,#mirador{margin:0;height:100%}</style>
<body>
<div id="mirador" data-manifest="/{{.}}/manifest.json"></div>
<script src="` + miradorRoute + `"></script>
<script>
  Mirador.viewer({
    id: 'mirador',
    window: { allowFullscreen: true },
    windows: [{ manifestId: document.getElementById('mirador').dataset.manifest }]
  });
</script>
</body>
</html>
`))

// preservedDirs returns the sorted root-relative slash paths of every dir
// holding a manifest.json, at any depth — the preserved manifests this
// server can view. With institution nesting these look like
// "<host>/<slug>".
func (s *Server) preservedDirs() []string {
	var dirs []string
	_ = filepath.WalkDir(s.root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "manifest.json" {
			return nil //nolint:nilerr // skip unreadable entries; a partial index still serves
		}
		rel, rerr := filepath.Rel(s.root, filepath.Dir(p))
		if rerr == nil && rel != "." {
			dirs = append(dirs, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs
}

// serveIndex writes the landing page listing every preserved manifest.
func (s *Server) serveIndex(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = indexTmpl.Execute(w, s.preservedDirs()) //nolint:errcheck // best-effort response write; client disconnect is not actionable
}

// serveViewer writes the Mirador page for dir (a verified preserved slug).
func (s *Server) serveViewer(w http.ResponseWriter, dir string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = viewerTmpl.Execute(w, dir) //nolint:errcheck // best-effort response write; client disconnect is not actionable
}

// serveBundle writes the embedded Mirador UMD bundle.
func (s *Server) serveBundle(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(miradorBundle) //nolint:errcheck // best-effort response write; client disconnect is not actionable
}

// hasManifest reports whether clean (already path.Clean'd, no trailing
// slash) is a request for a preserved manifest dir at any depth — e.g.
// "/<host>/<slug>" — so the viewer page is served instead of a file
// listing. path.Clean has already removed any "..".
func (s *Server) hasManifest(clean string) (dir string, ok bool) {
	dir = strings.TrimPrefix(clean, "/")
	if dir == "" || dir == "." {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(dir), "manifest.json")); err != nil {
		return "", false
	}
	return dir, true
}
