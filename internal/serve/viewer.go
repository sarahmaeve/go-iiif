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

// miradorBundle is the vendored Mirador 4 UMD bundle, embedded so the
// binary needs no external viewer and no Node runtime (DESIGN §2). It is a
// custom build (viewer-src/, `make viewer`): Mirador 4 built from a local
// source checkout — not the npm 4.0.0 tag, which lacks the companion-window
// render path MAE's creation tools need (added to Mirador 4 after 4.0.0;
// MAE's own README requires the latest Mirador 4) — with the MAE annotation
// editor and an HTTP storage adapter folded in, so a researcher can draw
// region annotations on the canvas and have them persisted to the local
// bundle. Still a single asset (MAE CSS is inlined); the build is the only
// thing that uses Node, never the binary.
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

// viewerTmpl renders a quiet editorial masthead (kicker, title, source,
// back-to-catalogue) above a full-bleed Mirador. The data is a
// manifestSummary; html/template escapes each field for its context. The
// manifest path is passed via a data-attribute (HTML), never interpolated
// into the JS string, to avoid html/template's "\/" JS-string escaping.
var viewerTmpl = template.Must(template.New("viewer").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Cur.Title}} — preserved</title>
<style>
@font-face{font-family:"Newsreader";font-weight:700;font-display:swap;src:url("` + fontsRoutePrefix + `newsreader-700.woff2") format("woff2")}
@font-face{font-family:"IBM Plex Mono";font-weight:600;font-display:swap;src:url("` + fontsRoutePrefix + `ibm-plex-mono-600.woff2") format("woff2")}
:root{--bg:#f4efe8;--surface:#fbf8f3;--text:#1c1917;--muted:#6a625b;--border:#c9bfb2;--accent:#8b2332;--primary:#1f2933}
html,body{height:100%;margin:0}
body{display:flex;flex-direction:column;overflow:hidden;background:var(--bg);
 font-family:"Source Serif 4",Georgia,serif;color:var(--text)}
header.masthead{padding:12px 24px;border-bottom:2px solid var(--primary);
 box-shadow:0 1px 0 rgba(28,25,23,.06)}
.top{display:flex;justify-content:space-between;align-items:baseline;gap:16px}
.kicker,.back,.from{font-family:"IBM Plex Mono",ui-monospace,Menlo,monospace;
 font-size:.7rem;font-weight:600;text-transform:uppercase;letter-spacing:.14em;color:var(--muted)}
.kicker{margin:0}
.back{text-decoration:none;white-space:nowrap}
.back:hover{color:var(--accent)}
h1{font-family:"Newsreader",Georgia,serif;font-weight:700;font-size:1.35rem;line-height:1.15;
 color:var(--primary);margin:5px 0 0}
.from{letter-spacing:.1em;margin-left:.6rem}
.from a{color:var(--muted);text-decoration:none;border-bottom:1px solid var(--border)}
.from a:hover{color:var(--accent)}
/* Mirador's root is position:static and its <main> is position:absolute,
   so without a positioned, sized container its main escapes this flex
   child and covers the masthead. position:relative contains it; flex:1
   + min-height:0 gives it the height below the masthead. (per Mirador
   wiki, "Embedding in Another Environment") */
#mirador{flex:1;min-height:0;position:relative;overflow:hidden}
.stage{display:flex;flex:1;min-height:0}
aside.library{width:15rem;flex:none;overflow-y:auto;background:var(--bg);
 border-right:2px solid var(--primary);padding:14px 0}
aside.library .lib-h{margin:0 0 8px;padding:0 16px;font-family:"IBM Plex Mono",ui-monospace,monospace;
 font-size:.62rem;font-weight:600;text-transform:uppercase;letter-spacing:.14em;color:var(--muted)}
aside.library a{display:block;padding:7px 16px;color:var(--primary);text-decoration:none;
 font-family:"Newsreader",Georgia,serif;font-size:.95rem;line-height:1.25;border-bottom:1px solid var(--border)}
aside.library a:hover{color:var(--accent);background:var(--surface)}
aside.library a[aria-current]{color:var(--accent);background:var(--surface);
 box-shadow:inset 3px 0 0 var(--accent);font-weight:600}
</style>
<body>
<header class="masthead">
<div class="top"><p class="kicker">Preserved manuscript · offline</p><a class="back" href="/">&larr; Catalogue</a></div>
<h1>{{.Cur.Title}}<span class="from">from <a href="{{.Cur.RecordURL}}" rel="noopener noreferrer">{{.Cur.Institution}}</a></span></h1>
</header>
<div class="stage">
<aside class="library">
<p class="lib-h">Library · {{len .Docs}}</p>
{{range .Docs}}<a href="/{{.Dir}}/"{{if eq .Dir $.Cur.Dir}} aria-current="page"{{end}}>{{.Title}}</a>
{{end}}</aside>
<div id="mirador" data-manifest="/{{.Cur.Dir}}/manifest.json"></div>
</div>
<script src="` + miradorRoute + `"></script>
<script>
  Mirador.viewer({
    id: 'mirador',
    /* Mirador's default osdConfig.preserveViewport is true, so SET_CANVAS
       keeps the previous canvas's OSD viewport and OSD only goHome()s on
       first open. A manuscript with mixed page sizes/orientations (e.g.
       Bodleian's portrait+landscape leaves) then renders every canvas
       after the first into canvas 1's world bounds — a partial image.
       Disable it so OSD re-homes per canvas. Verified against the
       vendored bundle (top-level config key, sibling of window). */
    osdConfig: { preserveViewport: false },
    /* Mirador 4 loads referenced annotations but, by default, keeps the
       sidebar closed and on-canvas highlights off — a stored note then
       has nowhere to surface. Activate the annotations companion
       explicitly. Keys verified against the vendored bundle. */
    window: {
      allowFullscreen: true,
      sideBarOpenByDefault: true,
      defaultSideBarPanel: 'annotations',
      highlightAllAnnotations: true,
    },
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
		out = append(out, summaryFor(dir, filepath.ToSlash(rel)))
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out
}

// summaryFor derives one manifestSummary from a preserved bundle dir
// (absDir) and its root-relative slash slug. Shared by the index and the
// per-manuscript viewer header. Unreadable pieces degrade to placeholders.
func summaryFor(absDir, slug string) manifestSummary {
	ms := manifestSummary{Dir: slug, Languages: "—", Institution: "—"}

	mb, _ := os.ReadFile(filepath.Join(absDir, "manifest.json")) //nolint:gosec // G304: manifest under the served root
	ms.Title = metadata.Title(mb)
	if ms.Title == "" {
		ms.Title = ms.Dir
	}

	var prov struct {
		ManifestURL string     `json:"manifest_url"`
		Images      []struct{} `json:"images"`
	}
	if pb, perr := os.ReadFile(filepath.Join(absDir, "provenance.json")); perr == nil { //nolint:gosec // G304: sibling of a served manifest
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
	ms.Size = dirSize(absDir)
	return ms
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

// viewerPage is the viewer template's data: the manuscript being viewed
// plus the whole library, for the left-side switcher rail.
type viewerPage struct {
	Cur  manifestSummary
	Docs []manifestSummary
}

// serveViewer writes the Mirador page for dir (a verified preserved slug),
// with a left rail listing every preserved manuscript so the reader can
// switch documents without leaving the viewer.
func (s *Server) serveViewer(w http.ResponseWriter, dir string) {
	abs := filepath.Join(s.root, filepath.FromSlash(dir))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = viewerTmpl.Execute(w, viewerPage{ //nolint:errcheck // best-effort response write; client disconnect is not actionable
		Cur:  summaryFor(abs, dir),
		Docs: s.manifestSummaries(),
	})
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
