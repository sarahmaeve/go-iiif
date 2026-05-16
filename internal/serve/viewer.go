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

// viewerTmpl renders a quiet editorial masthead (kicker, title, source,
// back-to-catalogue) above a full-bleed Mirador. The data is a
// manifestSummary; html/template escapes each field for its context. The
// manifest path is passed via a data-attribute (HTML), never interpolated
// into the JS string, to avoid html/template's "\/" JS-string escaping.
var viewerTmpl = template.Must(template.New("viewer").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} — preserved</title>
<style>
@font-face{font-family:"Newsreader";font-weight:700;font-display:swap;src:url("` + fontsRoutePrefix + `newsreader-700.woff2") format("woff2")}
@font-face{font-family:"IBM Plex Mono";font-weight:600;font-display:swap;src:url("` + fontsRoutePrefix + `ibm-plex-mono-600.woff2") format("woff2")}
:root{--bg:#f4efe8;--surface:#fbf8f3;--text:#1c1917;--muted:#6a625b;--border:#c9bfb2;--accent:#8b2332;--primary:#1f2933;--success:#2f6b45}
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
details.annotate{margin-top:8px}
details.annotate>summary{font-family:"IBM Plex Mono",ui-monospace,Menlo,monospace;font-size:.7rem;
 font-weight:600;text-transform:uppercase;letter-spacing:.12em;color:var(--muted);cursor:pointer;list-style:none}
details.annotate>summary::-webkit-details-marker{display:none}
details.annotate>summary::before{content:"+ ";color:var(--accent)}
details.annotate[open]>summary::before{content:"\2013 "}
.annotate form{display:flex;flex-wrap:wrap;gap:8px 14px;align-items:flex-end;
 margin-top:10px;padding-top:10px;border-top:1px solid var(--border)}
.annotate label{display:flex;flex-direction:column;gap:3px;font-family:"IBM Plex Mono",ui-monospace,monospace;
 font-size:.6rem;font-weight:600;text-transform:uppercase;letter-spacing:.1em;color:var(--muted)}
.annotate select,.annotate input,.annotate textarea{font-family:"Source Serif 4",Georgia,serif;
 font-size:.9rem;padding:5px 7px;border:1px solid var(--border);border-radius:6px;
 background:var(--surface);color:var(--text)}
.annotate textarea{min-width:17rem;min-height:2.2rem;resize:vertical}
.annotate input.xywh{width:3.6rem}
.annotate button{font-family:"IBM Plex Mono",ui-monospace,monospace;font-size:.66rem;font-weight:600;
 text-transform:uppercase;letter-spacing:.1em;color:#fff;background:var(--primary);border:0;
 border-radius:6px;padding:8px 16px;cursor:pointer}
.annotate button:hover{background:var(--accent)}
.annotate .status{flex-basis:100%;font-family:"IBM Plex Mono",ui-monospace,monospace;
 font-size:.66rem;letter-spacing:.06em;color:var(--muted)}
.annotate .status.err{color:var(--accent)}
.annotate .status.ok{color:var(--success)}
</style>
<body>
<header class="masthead">
<div class="top"><p class="kicker">Preserved manuscript · offline</p><a class="back" href="/">&larr; Catalogue</a></div>
<h1>{{.Title}}<span class="from">from <a href="{{.RecordURL}}" rel="noopener noreferrer">{{.Institution}}</a></span></h1>
<details class="annotate">
<summary>Annotate this manuscript (saved offline, beside the preserved copy)</summary>
<form id="annotate-form">
<label>Page<select id="annotate-canvas"></select></label>
<label>Kind<select id="annotate-kind">
<option value="commenting">Note</option>
<option value="translating">Translation</option>
<option value="tagging">Tag</option>
<option value="highlighting">Highlight</option>
<option value="bookmarking">Bookmark</option>
</select></label>
<label>Text<textarea id="annotate-text" placeholder="note, translation, or tag"></textarea></label>
<label>Lang<input id="annotate-lang" size="4" placeholder="en"></label>
<label>x<input id="annotate-x" class="xywh" inputmode="numeric"></label>
<label>y<input id="annotate-y" class="xywh" inputmode="numeric"></label>
<label>w<input id="annotate-w" class="xywh" inputmode="numeric"></label>
<label>h<input id="annotate-h" class="xywh" inputmode="numeric"></label>
<button type="submit">Save</button>
<span class="status" id="annotate-status"></span>
</form>
</details>
</header>
<div id="mirador" data-manifest="/{{.Dir}}/manifest.json"></div>
<script src="` + miradorRoute + `"></script>
<script>
  Mirador.viewer({
    id: 'mirador',
    window: { allowFullscreen: true },
    windows: [{ manifestId: document.getElementById('mirador').dataset.manifest }]
  });
</script>
<script>
/* Annotation authoring: pure, dependency-free, decoupled from Mirador
   internals. Reads the served manifest for the canvas list and POSTs a
   W3C annotation to the C1 endpoint (relative URLs resolve under this
   /<dir>/ page). No template values in JS — avoids html/template's
   JS-string escaping entirely. */
(function () {
  var form = document.getElementById('annotate-form');
  if (!form) return;
  var sel = document.getElementById('annotate-canvas');
  var st = document.getElementById('annotate-status');
  function lab(l) {
    if (!l) return '';
    if (typeof l === 'string') return l;
    if (Array.isArray(l)) return lab(l[0]);
    if (typeof l === 'object') { for (var k in l) return lab(l[k]); }
    return '';
  }
  fetch('manifest.json').then(function (r) { return r.json(); }).then(function (m) {
    var cs = [];
    if (Array.isArray(m.items)) {
      m.items.forEach(function (i) { if (i.type === 'Canvas') cs.push({ id: i.id, label: lab(i.label) }); });
    } else if (m.sequences && m.sequences[0] && m.sequences[0].canvases) {
      m.sequences[0].canvases.forEach(function (c) { cs.push({ id: c['@id'], label: lab(c.label) }); });
    }
    cs.forEach(function (c, n) {
      var o = document.createElement('option');
      o.value = c.id;
      // Always show a sequence number; many manifests label every canvas
      // "NP" (no pagination) or leave it blank — append the label only
      // when it actually says something.
      var lb = (c.label || '').trim();
      o.textContent = 'p. ' + (n + 1) +
        (lb && lb.toUpperCase() !== 'NP' ? ' — ' + lb : '');
      sel.appendChild(o);
    });
    if (!cs.length) { st.textContent = 'no pages found'; st.className = 'status err'; }
  }).catch(function () { st.textContent = 'could not load pages'; st.className = 'status err'; });

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var cid = sel.value;
    if (!cid) return;
    var kind = document.getElementById('annotate-kind').value;
    var v = function (id) { return document.getElementById(id).value.trim(); };
    var text = v('annotate-text'), lang = v('annotate-lang');
    var x = v('annotate-x'), y = v('annotate-y'), w = v('annotate-w'), h = v('annotate-h');
    var target = (x && y && w && h) ? cid + '#xywh=' + [x, y, w, h].join(',') : cid;
    var ann = { type: 'Annotation', motivation: kind, target: target };
    if (kind !== 'bookmarking' && text) {
      var b = { type: 'TextualBody', value: text, format: 'text/plain' };
      if (kind === 'translating' && lang) b.language = lang;
      ann.body = b;
    }
    st.textContent = 'saving'; st.className = 'status';
    fetch('annotations', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(ann)
    }).then(function (r) {
      if (r.status !== 201) throw new Error('HTTP ' + r.status);
      return r.json();
    }).then(function () {
      st.textContent = 'saved — ';
      var a = document.createElement('a');
      a.href = ''; a.textContent = 'reload to view it';
      st.appendChild(a);
      st.className = 'status ok';
      form.reset();
    }).catch(function (err) {
      st.textContent = 'save failed: ' + err.message;
      st.className = 'status err';
    });
  });
})();
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

// serveViewer writes the Mirador page for dir (a verified preserved slug).
func (s *Server) serveViewer(w http.ResponseWriter, dir string) {
	abs := filepath.Join(s.root, filepath.FromSlash(dir))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = viewerTmpl.Execute(w, summaryFor(abs, dir)) //nolint:errcheck // best-effort response write; client disconnect is not actionable
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
