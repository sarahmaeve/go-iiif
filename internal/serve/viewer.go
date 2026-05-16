package serve

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sarahmaeve/go-iiif/internal/institution"
	"github.com/sarahmaeve/go-iiif/internal/metadata"
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
<style>body{font-family:system-ui,sans-serif;margin:2rem}
table{border-collapse:collapse}
th,td{padding:.4rem .9rem;border-bottom:1px solid #ddd;text-align:left}
th{font-size:.85rem;text-transform:uppercase;color:#555}</style>
<body>
<h1>Preserved IIIF manifests</h1>
<table>
<tr><th>Title</th><th>Language</th><th>Institution</th><th>~Pages</th><th>~Size</th></tr>
{{range .}}<tr>
<td><a href="/{{.Dir}}/">{{.Title}}</a></td>
<td>{{.Languages}}</td>
<td><a href="{{.RecordURL}}" rel="noopener noreferrer">{{.Institution}}</a></td>
<td>{{.Pages}}</td>
<td>{{.Size}}</td>
</tr>
{{end}}</table>
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

// manifestSummary is one row of the index: enough to choose a manuscript
// without opening it. All fields are derived from the already-stored
// manifest.json + provenance.json + on-disk size — no re-fetch.
type manifestSummary struct {
	Dir         string // viewer link target, /<Dir>/
	Title       string
	Languages   string
	Institution string // host of the original IIIF record
	RecordURL   string // original manifest URL (the IIIF record)
	Pages       int
	Size        string
}

// manifestSummaries builds an index row for every preserved manifest dir at
// any depth. Unreadable pieces degrade to placeholders rather than dropping
// the row — a partial index still serves.
func (s *Server) manifestSummaries() []manifestSummary {
	var out []manifestSummary
	_ = filepath.WalkDir(s.root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "manifest.json" {
			return nil //nolint:nilerr // skip unreadable entries; a partial index still serves
		}
		dir := filepath.Dir(p)
		rel, rerr := filepath.Rel(s.root, dir)
		if rerr != nil || rel == "." {
			return nil //nolint:nilerr // un-relativizable entry: skip it, keep indexing the rest
		}
		ms := manifestSummary{Dir: filepath.ToSlash(rel), Languages: "—", Institution: "—"}

		mb, _ := os.ReadFile(p) //nolint:gosec // G304: p is a manifest.json under the served root
		ms.Title = metadata.Title(mb)
		if ms.Title == "" {
			ms.Title = ms.Dir
		}

		var prov struct {
			ManifestURL string     `json:"manifest_url"`
			Images      []struct{} `json:"images"`
		}
		if pb, perr := os.ReadFile(filepath.Join(dir, "provenance.json")); perr == nil { //nolint:gosec // G304: sibling of a served manifest
			_ = json.Unmarshal(pb, &prov)
		}
		ms.Pages = len(prov.Images)
		ms.RecordURL = prov.ManifestURL
		host := ""
		if u, uerr := url.Parse(prov.ManifestURL); uerr == nil && u.Host != "" {
			host = u.Host
			ms.Institution = host
		}
		if entries, eerr := metadata.ExtractMetadata(mb); eerr == nil {
			rec := metadata.BuildWorkRecord(entries, institution.Builtin().For(host).FieldMapping)
			if len(rec.Langs) > 0 {
				ms.Languages = strings.Join(rec.Langs, ", ")
			}
		}
		ms.Size = dirSize(dir)
		out = append(out, ms)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out
}

// dirSize is the estimated on-disk size of a preserved bundle (images +
// tile pyramids dominate), rendered for humans.
func dirSize(dir string) string {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, ierr := d.Info(); ierr == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return fmt.Sprintf("%.0f MB", float64(total)/(1024*1024))
}

// serveIndex writes the landing page: a row per preserved manifest with
// enough info to choose one without opening it.
func (s *Server) serveIndex(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = indexTmpl.Execute(w, s.manifestSummaries()) //nolint:errcheck // best-effort response write; client disconnect is not actionable
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
