package serve

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path"
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

// fontsFS holds the vendored editorial webfonts (SIL OFL 1.1: Newsreader,
// Source Serif 4, IBM Plex Mono) so the "Literary Longform" index renders
// faithfully offline — no CDN, no runtime font dependency.
//
//go:embed fonts/*.woff2
var fontsFS embed.FS

// Reserved asset routes. The double-underscore prefix cannot collide with a
// preserved-manifest dir slug (institution host + IIIF path).
const (
	miradorRoute     = "/__viewer__/mirador.min.js"
	fontsRoutePrefix = "/__viewer__/fonts/"
)

// indexTmpl is the landing page: a researcher with no external viewer lands
// here and clicks into any preserved manifest.
// indexTmpl follows the "Literary Longform Interface" design language:
// editorial restraint — serif display, mono folio labels, hairline rules,
// paper-toned surface, generous margins; no card grid or heavy chrome.
// Fonts are vendored and served locally (offline-safe) with system serif/
// mono fallbacks if they fail.
var indexTmpl = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Preserved IIIF Manifests</title>
<style>
@font-face{font-family:"Newsreader";font-weight:700;font-display:swap;src:url("` + fontsRoutePrefix + `newsreader-700.woff2") format("woff2")}
@font-face{font-family:"Newsreader";font-weight:600;font-display:swap;src:url("` + fontsRoutePrefix + `newsreader-600.woff2") format("woff2")}
@font-face{font-family:"Source Serif 4";font-weight:400;font-display:swap;src:url("` + fontsRoutePrefix + `source-serif-4-400.woff2") format("woff2")}
@font-face{font-family:"IBM Plex Mono";font-weight:600;font-display:swap;src:url("` + fontsRoutePrefix + `ibm-plex-mono-600.woff2") format("woff2")}
:root{--bg:#f4efe8;--surface:#fbf8f3;--text:#1c1917;--muted:#6a625b;--border:#c9bfb2;--accent:#8b2332;--primary:#1f2933}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text);
 font-family:"Source Serif 4",Georgia,"Times New Roman",serif;font-size:18px;line-height:1.72;
 -webkit-font-smoothing:antialiased}
.page{max-width:46rem;margin:0 auto;padding:64px 24px 96px}
.kicker{font-family:"IBM Plex Mono",ui-monospace,Menlo,monospace;font-size:.75rem;font-weight:600;
 text-transform:uppercase;letter-spacing:.16em;color:var(--muted);margin:0 0 12px}
h1{font-family:"Newsreader",Georgia,serif;font-weight:700;font-size:1.944rem;line-height:1.1;
 letter-spacing:.01em;color:var(--primary);margin:0}
.rule{border:0;border-top:1px solid var(--border);margin:24px 0 0}
.rule.dbl{border-top:2px solid var(--primary);margin-top:16px}
.count{font-family:"IBM Plex Mono",ui-monospace,monospace;font-size:.75rem;font-weight:600;
 text-transform:uppercase;letter-spacing:.12em;color:var(--muted);margin:32px 0 0}
.entry{padding:24px 0;border-bottom:1px solid var(--border)}
.entry .title{font-family:"Newsreader",Georgia,serif;font-weight:600;font-size:1.62rem;line-height:1.15;
 color:var(--primary);text-decoration:none;display:inline-block}
.entry .title:hover{color:var(--accent);text-decoration:underline;text-underline-offset:3px}
.meta{font-family:"IBM Plex Mono",ui-monospace,Menlo,monospace;font-size:.75rem;font-weight:600;
 text-transform:uppercase;letter-spacing:.08em;color:var(--muted);margin:8px 0 0}
.meta a{color:var(--secondary,#6b5c53);text-decoration:none;border-bottom:1px solid var(--border)}
.meta a:hover{color:var(--accent)}
.meta .sep{padding:0 8px;color:var(--border)}
.empty{font-style:italic;color:var(--muted);margin-top:32px}
</style>
<body>
<main class="page">
<p class="kicker">Preserved Archive</p>
<h1>Preserved IIIF Manifests</h1>
<hr class="rule"><hr class="rule dbl">
<p class="count">{{len .}} manuscript(s) · offline · deep-zoomable</p>
{{range .}}<article class="entry">
<a class="title" href="/{{.Dir}}/">{{.Title}}</a>
<p class="meta"><a href="{{.RecordURL}}" rel="noopener noreferrer">{{.Institution}}</a><span class="sep">·</span>{{.Languages}}<span class="sep">·</span>{{.Pages}} pp<span class="sep">·</span>{{.Size}}</p>
</article>
{{else}}<p class="empty">No preserved manifests yet.</p>
{{end}}</main>
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

// serveFont writes a vendored editorial webfont from the embedded FS. Only
// the base name is used (no traversal; embed.FS is read-only regardless).
func (s *Server) serveFont(w http.ResponseWriter, r *http.Request, files http.Handler) {
	name := path.Base(path.Clean(r.URL.Path))
	b, err := fontsFS.ReadFile("fonts/" + name)
	if err != nil {
		files.ServeHTTP(w, r) // unknown font → let the file server 404
		return
	}
	w.Header().Set("Content-Type", "font/woff2")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	//nolint:errcheck,gosec // errcheck: best-effort write; G705: bytes are a read-only embedded .woff2 chosen by path.Base, served as font/woff2 — not user-controlled HTML
	_, _ = w.Write(b)
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
