package serve

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
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
	miradorRoute        = "/__viewer__/mirador.min.js"
	fontsRoutePrefix    = "/__viewer__/fonts/"
	catalogEditRoute    = "/__catalog__/edit"
	catalogRefreshRoute = "/__catalog__/refresh"
	compareRoute        = "/__compare__/"
	compareSaveRoute    = "/__compare__/save"
	compareDeleteRoute  = "/__compare__/delete"
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
.catalogue-bar{display:flex;align-items:baseline;justify-content:space-between;gap:16px;margin:32px 0 0}
.count{font-family:"IBM Plex Mono",ui-monospace,monospace;font-size:.75rem;font-weight:600;
 text-transform:uppercase;letter-spacing:.12em;color:var(--muted);margin:0}
.refresh button{border:0;border-bottom:1px solid var(--border);padding:0;background:transparent;color:var(--muted);cursor:pointer;
 font:600 .68rem "IBM Plex Mono",ui-monospace,monospace;text-transform:uppercase;letter-spacing:.1em}
.refresh button:hover{color:var(--accent);border-color:var(--accent)}
.catalogue-tools{display:grid;grid-template-columns:minmax(0,1fr) 12rem;gap:12px;margin:18px 0 4px}
.catalogue-tools label{display:grid;gap:4px;font-family:"IBM Plex Mono",ui-monospace,monospace;font-size:.62rem;
 font-weight:600;text-transform:uppercase;letter-spacing:.1em;color:var(--muted)}
.catalogue-tools input,.catalogue-tools select{width:100%;border:1px solid var(--border);background:var(--surface);color:var(--text);
 padding:8px 10px;font:400 .9rem "Source Serif 4",Georgia,serif;text-transform:none;letter-spacing:normal}
.saved-comparisons{margin:24px 0 4px;padding:14px 16px;background:var(--surface);border:1px solid var(--border)}
.saved-comparisons h2{margin:0 0 8px;font:600 1.1rem "Newsreader",Georgia,serif}.saved-comparisons ul{list-style:none;margin:0;padding:0;display:grid;gap:5px}
.saved-comparisons li{display:flex;justify-content:space-between;align-items:baseline;gap:12px}.saved-comparisons a{color:var(--primary);text-decoration:none;border-bottom:1px solid var(--border)}.saved-comparisons a:hover{color:var(--accent)}
.saved-comparisons form{display:inline}.saved-comparisons button{border:0;background:transparent;color:var(--muted);cursor:pointer;font:600 .62rem "IBM Plex Mono",monospace;text-transform:uppercase;letter-spacing:.08em}.saved-comparisons button:hover{color:var(--accent)}
.entry{padding:24px 0;border-bottom:1px solid var(--border)}
.entry .title{font-family:"Newsreader",Georgia,serif;font-weight:600;font-size:1.62rem;line-height:1.15;
 color:var(--primary);text-decoration:none;display:inline-block}
.entry .title:hover{color:var(--accent);text-decoration:underline;text-underline-offset:3px}
.meta{font-family:"IBM Plex Mono",ui-monospace,Menlo,monospace;font-size:.75rem;font-weight:600;
 text-transform:uppercase;letter-spacing:.08em;color:var(--muted);margin:8px 0 0}
.meta a{color:var(--secondary,#6b5c53);text-decoration:none;border-bottom:1px solid var(--border)}
.meta a:hover{color:var(--accent)}
.meta .sep{padding:0 8px;color:var(--border)}
.notes{margin:12px 0 0;padding-left:14px;border-left:2px solid var(--border);white-space:pre-wrap;color:#403a35}
.tags{font-family:"IBM Plex Mono",ui-monospace,monospace;font-size:.68rem;text-transform:uppercase;letter-spacing:.08em;
 color:var(--accent);margin:10px 0 0}
.edit{margin-top:12px;font-family:"IBM Plex Mono",ui-monospace,Menlo,monospace;font-size:.72rem;color:var(--muted)}
.edit summary{cursor:pointer;display:inline-block;border-bottom:1px solid var(--border)}
.edit form{display:grid;gap:10px;margin-top:12px;padding:14px;background:var(--surface);border:1px solid var(--border)}
.edit label{display:grid;gap:4px;text-transform:uppercase;letter-spacing:.08em;font-weight:600}
.edit input,.edit textarea{width:100%;border:1px solid var(--border);background:#fffdf9;color:var(--text);padding:8px 10px;
 font:400 .95rem/1.4 "Source Serif 4",Georgia,serif;text-transform:none;letter-spacing:normal}
.edit textarea{min-height:5rem;resize:vertical}
.edit button{justify-self:start;border:0;background:var(--primary);color:white;padding:8px 14px;cursor:pointer;
 font:600 .7rem "IBM Plex Mono",ui-monospace,monospace;text-transform:uppercase;letter-spacing:.1em}
.edit button:hover{background:var(--accent)}
.compare-add{margin-top:12px;border:1px solid var(--border);background:transparent;color:var(--primary);padding:7px 10px;cursor:pointer;
 font:600 .68rem "IBM Plex Mono",ui-monospace,monospace;text-transform:uppercase;letter-spacing:.08em}
.compare-add:hover,.compare-add[aria-pressed="true"]{border-color:var(--accent);color:var(--accent);background:var(--surface)}
.compare-tray{position:fixed;z-index:20;left:0;right:0;bottom:0;background:var(--primary);color:white;border-top:3px solid var(--accent);padding:14px 24px 16px;box-shadow:0 -4px 18px rgba(28,25,23,.18)}
.compare-tray[hidden]{display:none}.compare-inner{max-width:58rem;margin:0 auto}.compare-head{display:flex;align-items:baseline;justify-content:space-between;gap:16px}
.compare-head h2{margin:0;font:700 1.15rem "Newsreader",Georgia,serif}.compare-help{margin:0;color:#ddd4c9;font-size:.82rem}
.compare-list{list-style:none;margin:10px 0;padding:0;display:grid;gap:6px}.compare-list li{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:10px;align-items:center;border-top:1px solid #52606c;padding-top:6px}
.compare-title{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.compare-item-actions{display:flex;gap:5px}.compare-item-actions button{border:1px solid #8795a1;background:transparent;color:white;cursor:pointer;padding:3px 7px;font:600 .62rem "IBM Plex Mono",monospace;text-transform:uppercase;letter-spacing:.05em}.compare-item-actions button:disabled{opacity:.35;cursor:default}
.compare-footer{display:flex;justify-content:space-between;align-items:center;gap:16px;margin-top:10px}.compare-live{margin:0;color:#ddd4c9;font-size:.82rem}.compare-open{background:var(--surface);color:var(--primary);padding:8px 13px;text-decoration:none;font:600 .68rem "IBM Plex Mono",monospace;text-transform:uppercase;letter-spacing:.08em}.compare-open[aria-disabled="true"]{opacity:.45;pointer-events:none}.compare-open:not([aria-disabled="true"]):hover{color:var(--accent)}
body.compare-active .page{padding-bottom:22rem}
.empty{font-style:italic;color:var(--muted);margin-top:32px}
@media(max-width:36rem){.catalogue-tools{grid-template-columns:1fr}.catalogue-bar,.compare-head,.compare-footer{align-items:flex-start;flex-direction:column}.compare-tray{padding:12px 16px}.compare-list li{grid-template-columns:1fr}.compare-item-actions{flex-wrap:wrap}body.compare-active .page{padding-bottom:30rem}}
</style>
<body>
<main class="page">
<p class="kicker">Preserved Archive</p>
<h1>Preserved IIIF Manifests</h1>
<hr class="rule"><hr class="rule dbl">
{{if .Comparisons}}<section class="saved-comparisons"><h2>Saved comparisons</h2><ul>{{range .Comparisons}}<li><a href="{{.Href}}">{{.Name}}</a><form method="post" action="` + compareDeleteRoute + `"><input type="hidden" name="id" value="{{.ID}}"><button type="submit" aria-label="Delete saved comparison {{.Name}}">Delete</button></form></li>{{end}}</ul></section>{{end}}
<div class="catalogue-bar"><p class="count">{{len .Docs}} manuscript(s) · offline · deep-zoomable</p>
<form class="refresh" method="post" action="` + catalogRefreshRoute + `"><button type="submit">Refresh library</button></form></div>
<div class="catalogue-tools">
<label>Search library<input id="catalog-search" type="search" placeholder="Title, institution, language, notes, or tags"></label>
<label>Sort by<select id="catalog-sort"><option value="archive">Archive path</option><option value="title">Title</option><option value="institution">Institution</option><option value="pages">Page count</option></select></label>
</div>
<section id="catalog-entries">
{{range .Docs}}<article class="entry" data-dir="{{.Dir}}" data-title="{{.Title}}" data-institution="{{.Institution}}" data-pages="{{.Pages}}" data-search="{{.SourceTitle}} {{.Title}} {{.Institution}} {{.Languages}} {{.Notes}} {{.Tags}}">
<a class="title" href="/{{.Dir}}/">{{.Title}}</a>
<p class="meta"><a href="{{.RecordURL}}" rel="noopener noreferrer">{{.Institution}}</a><span class="sep">·</span>{{.Languages}}<span class="sep">·</span>{{.Pages}} pp<span class="sep">·</span>{{.Size}}</p>
{{if .Notes}}<p class="notes">{{.Notes}}</p>{{end}}
{{if .Tags}}<p class="tags">Tags · {{.Tags}}</p>{{end}}
<button class="compare-add" type="button" data-compare-dir="{{.Dir}}" data-compare-title="{{.Title}}" aria-pressed="false" aria-controls="comparison-tray">Add to comparison</button>
<details class="edit"><summary>Edit title or notes</summary>
<form method="post" action="` + catalogEditRoute + `">
<input type="hidden" name="dir" value="{{.Dir}}">
<label>Display title<input name="title" maxlength="500" value="{{.CustomTitle}}" placeholder="{{.SourceTitle}}"></label>
<label>Catalogue notes<textarea name="notes" maxlength="20000">{{.Notes}}</textarea></label>
<label>Tags<input name="tags" maxlength="1000" value="{{.Tags}}" placeholder="Old French, John Dee, reviewed"></label>
<button type="submit">Save catalogue entry</button>
</form></details>
</article>
{{else}}<p class="empty">No preserved manifests yet.</p>
{{end}}</section>
<p class="empty" id="catalog-no-results" hidden>No manuscripts match this search.</p>
</main>
<section class="compare-tray" id="comparison-tray" aria-labelledby="comparison-title" hidden>
<div class="compare-inner">
<div class="compare-head"><h2 id="comparison-title">Manuscript comparison</h2><p class="compare-help">Choose 2–4 manuscripts. Order is preserved in the comparison link.</p></div>
<ol class="compare-list" id="comparison-list"></ol>
<div class="compare-footer"><p class="compare-live" id="comparison-live" aria-live="polite">Select at least two manuscripts.</p><a class="compare-open" id="comparison-open" href="` + compareRoute + `" aria-disabled="true">Compare manuscripts</a></div>
</div></section>
<script>
(() => {
  const search = document.getElementById('catalog-search');
  const sort = document.getElementById('catalog-sort');
  const box = document.getElementById('catalog-entries');
  const empty = document.getElementById('catalog-no-results');
  const count = document.querySelector('.count');
  const entries = Array.from(box.querySelectorAll('article.entry'));
  const text = (entry, key) => (entry.dataset[key] || '').toLocaleLowerCase();
  function apply() {
    const query = search.value.trim().toLocaleLowerCase();
    let visible = 0;
    for (const entry of entries) {
      entry.hidden = query !== '' && !entry.dataset.search.toLocaleLowerCase().includes(query);
      if (!entry.hidden) visible++;
    }
    const mode = sort.value;
    entries.sort((a, b) => {
      if (mode === 'pages') return Number(b.dataset.pages) - Number(a.dataset.pages) || text(a, 'title').localeCompare(text(b, 'title'));
      if (mode === 'title' || mode === 'institution') return text(a, mode).localeCompare(text(b, mode));
      return text(a, 'dir').localeCompare(text(b, 'dir'));
    });
    for (const entry of entries) box.appendChild(entry);
    empty.hidden = visible !== 0 || entries.length === 0;
    count.textContent = visible + ' manuscript(s) · offline · deep-zoomable';
  }
  search.addEventListener('input', apply);
  sort.addEventListener('change', apply);

  const tray = document.getElementById('comparison-tray');
  const list = document.getElementById('comparison-list');
  const live = document.getElementById('comparison-live');
  const open = document.getElementById('comparison-open');
  const addButtons = Array.from(document.querySelectorAll('.compare-add'));
  const byDir = new Map(addButtons.map((button) => [button.dataset.compareDir, button]));
  let selected = new URLSearchParams(window.location.search).getAll('doc').filter((dir, i, all) => byDir.has(dir) && all.indexOf(dir) === i).slice(0, 4);

  function selectionURL(base) {
    const params = new URLSearchParams();
    for (const dir of selected) params.append('doc', dir);
    const query = params.toString();
    return base + (query ? '?' + query : '');
  }
  function announce(message) { live.textContent = message; }
  function renderSelection(message) {
    tray.hidden = selected.length === 0;
    document.body.classList.toggle('compare-active', selected.length !== 0);
    list.replaceChildren();
    selected.forEach((dir, index) => {
      const source = byDir.get(dir);
      const row = document.createElement('li');
      const title = document.createElement('span');
      title.className = 'compare-title';
      title.textContent = source.dataset.compareTitle;
      const actions = document.createElement('span');
      actions.className = 'compare-item-actions';
      const action = (label, disabled, fn) => {
        const button = document.createElement('button');
        button.type = 'button'; button.textContent = label; button.disabled = disabled;
        button.setAttribute('aria-label', label + ' ' + source.dataset.compareTitle);
        button.addEventListener('click', fn); actions.appendChild(button);
      };
      action('Earlier', index === 0, () => { [selected[index - 1], selected[index]] = [selected[index], selected[index - 1]]; renderSelection('Moved ' + source.dataset.compareTitle + ' earlier.'); });
      action('Later', index === selected.length - 1, () => { [selected[index], selected[index + 1]] = [selected[index + 1], selected[index]]; renderSelection('Moved ' + source.dataset.compareTitle + ' later.'); });
      action('Remove', false, () => { selected.splice(index, 1); renderSelection('Removed ' + source.dataset.compareTitle + '.'); });
      row.append(title, actions); list.appendChild(row);
    });
    for (const button of addButtons) {
      const active = selected.includes(button.dataset.compareDir);
      button.setAttribute('aria-pressed', String(active));
      button.textContent = active ? 'Remove from comparison' : 'Add to comparison';
    }
    const ready = selected.length >= 2;
    open.setAttribute('aria-disabled', String(!ready));
    open.tabIndex = ready ? 0 : -1;
    open.href = selectionURL('` + compareRoute + `');
    history.replaceState(null, '', selectionURL('/'));
    if (message) announce(message);
    else if (!ready) announce('Select ' + (2 - selected.length) + ' more manuscript' + (selected.length === 0 ? 's' : '') + '.');
    else announce(selected.length + ' manuscripts selected.');
  }
  for (const button of addButtons) button.addEventListener('click', () => {
    const dir = button.dataset.compareDir;
    const index = selected.indexOf(dir);
    if (index >= 0) {
      selected.splice(index, 1);
      renderSelection('Removed ' + button.dataset.compareTitle + '.');
      return;
    }
    if (selected.length === 4) {
      announce('Comparison is limited to four manuscripts. Remove one before adding another.');
      return;
    }
    selected.push(dir);
    renderSelection('Added ' + button.dataset.compareTitle + '.');
  });
  open.addEventListener('click', (event) => { if (selected.length < 2) event.preventDefault(); });
  renderSelection();
})();
</script>
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
	Title       string // effective display title (custom title, then source title)
	SourceTitle string
	CustomTitle string
	Notes       string
	Tags        string
	Languages   string
	Institution string // host of the original IIIF record
	RecordURL   string // original manifest URL (the IIIF record)
	Pages       int
	Size        string

	sourceStamp string
	sizeBytes   int64
	sizeKnown   bool
}

// manifestSummaries returns the request-time in-memory catalogue. Discovery,
// metadata parsing, and size migration happen outside HTTP request handling.
func (s *Server) manifestSummaries() []manifestSummary {
	return s.catalog.list()
}

// summaryFor derives one manifestSummary from a preserved bundle dir
// (absDir) and its root-relative slash slug. Shared by the index and the
// per-manuscript viewer header. Unreadable pieces degrade to placeholders.
func summaryFor(absDir, slug string) manifestSummary {
	ms := manifestSummary{Dir: slug, Languages: "—", Institution: "—"}

	manifestPath := filepath.Join(absDir, "manifest.json")
	provenancePath := filepath.Join(absDir, "provenance.json")
	mb, _ := os.ReadFile(manifestPath) //nolint:gosec // G304: manifest under the served root
	ms.SourceTitle = metadata.Title(mb)
	if ms.SourceTitle == "" {
		ms.SourceTitle = ms.Dir
	}

	var prov struct {
		ManifestURL string     `json:"manifest_url"`
		Images      []struct{} `json:"images"`
	}
	if pb, perr := os.ReadFile(provenancePath); perr == nil { //nolint:gosec // G304: sibling of a served manifest
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
	ms.sourceStamp = fileStamp(manifestPath) + ":" + fileStamp(provenancePath)
	ms.finishDisplayFields()
	return ms
}

func fileStamp(name string) string {
	fi, err := os.Stat(name)
	if err != nil {
		return "-"
	}
	return fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size())
}

func (ms *manifestSummary) finishDisplayFields() {
	ms.Title = ms.SourceTitle
	if ms.CustomTitle != "" {
		ms.Title = ms.CustomTitle
	}
	if ms.sizeKnown {
		ms.Size = fmt.Sprintf("%.0f MB", float64(ms.sizeBytes)/(1024*1024))
	} else {
		ms.Size = "size calculating…"
	}
}

// serveIndex writes the landing page: a row per preserved manifest with
// enough info to choose one without opening it.
type indexPage struct {
	Docs        []manifestSummary
	Comparisons []savedComparisonSummary
}

func (s *Server) serveIndex(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = indexTmpl.Execute(w, indexPage{ //nolint:errcheck // best-effort response write; client disconnect is not actionable
		Docs: s.manifestSummaries(), Comparisons: comparisonSummaries(s.comparisons.list()),
	})
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
	cur, ok := s.catalog.get(dir)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = viewerTmpl.Execute(w, viewerPage{ //nolint:errcheck // best-effort response write; client disconnect is not actionable
		Cur:  cur,
		Docs: s.manifestSummaries(),
	})
}

// handleCatalogEdit persists the researcher's display-title override and
// free-form catalogue notes. Like annotations, this is a loopback-only,
// single-user editing surface and needs no separate authentication layer.
func (s *Server) handleCatalogEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowMutation(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid catalogue edit", http.StatusBadRequest)
		return
	}
	dir := r.FormValue("dir")
	title := r.FormValue("title")
	notes := r.FormValue("notes")
	tags := r.FormValue("tags")
	if len(title) > 500 || len(notes) > 20000 || len(tags) > 1000 {
		http.Error(w, "catalogue edit is too large", http.StatusBadRequest)
		return
	}
	if err := s.catalog.update(dir, title, notes, tags); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "could not save catalogue edit", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleCatalogRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowMutation(w, r) {
		return
	}
	s.catalog.refreshSources()
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
