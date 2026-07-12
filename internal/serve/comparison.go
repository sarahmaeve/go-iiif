package serve

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	minComparisonDocs = 2
	maxComparisonDocs = 4
)

type comparisonItem struct {
	Dir      string `json:"dir"`
	Title    string `json:"title"`
	Manifest string `json:"manifest"`
}

type comparisonPage struct {
	Items           []comparisonItem
	ItemsJSON       template.JS
	EndpointsJSON   template.JS
	ChangeSelection string
}

var comparisonTmpl = template.Must(template.New("comparison").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Manuscript comparison — preserved</title>
<style>
@font-face{font-family:"Newsreader";font-weight:700;font-display:swap;src:url("` + fontsRoutePrefix + `newsreader-700.woff2") format("woff2")}
@font-face{font-family:"IBM Plex Mono";font-weight:600;font-display:swap;src:url("` + fontsRoutePrefix + `ibm-plex-mono-600.woff2") format("woff2")}
:root{--bg:#f4efe8;--surface:#fbf8f3;--text:#1c1917;--muted:#6a625b;--border:#c9bfb2;--accent:#8b2332;--primary:#1f2933}
*{box-sizing:border-box}html,body{height:100%;margin:0}
body{display:flex;flex-direction:column;overflow:hidden;background:var(--bg);color:var(--text);font-family:"Source Serif 4",Georgia,serif}
.masthead{padding:10px 20px 12px;border-bottom:2px solid var(--primary);background:var(--bg)}
.top{display:flex;justify-content:space-between;align-items:center;gap:18px}.kicker{margin:0;font:600 .68rem "IBM Plex Mono",monospace;text-transform:uppercase;letter-spacing:.14em;color:var(--muted)}
.actions{display:flex;gap:14px;align-items:center}.actions a,.actions button{border:0;border-bottom:1px solid var(--border);padding:0;background:transparent;color:var(--muted);cursor:pointer;text-decoration:none;font:600 .66rem "IBM Plex Mono",monospace;text-transform:uppercase;letter-spacing:.1em}.actions a:hover,.actions button:hover{color:var(--accent);border-color:var(--accent)}
h1{margin:5px 0 0;font:700 1.35rem/1.15 "Newsreader",Georgia,serif;color:var(--primary)}
.titles{margin:5px 0 0;padding:0;display:flex;flex-wrap:wrap;gap:4px 16px;list-style:none;color:var(--muted);font-size:.88rem}.titles li:before{content:"◆";padding-right:6px;color:var(--accent);font-size:.55rem}
.notice{display:none;margin:0;padding:7px 20px;background:var(--surface);border-bottom:1px solid var(--border);color:var(--muted);font-size:.85rem}
#mirador{flex:1;min-height:0;position:relative;overflow:hidden}.copy-status{position:absolute;left:-9999px}
@media(max-width:48rem){.top{align-items:flex-start;flex-direction:column}.actions{flex-wrap:wrap}.notice{display:block}.masthead{padding-bottom:9px}
 #mirador .mosaic-root{overflow-y:auto!important;display:block!important}
 #mirador .mosaic-tile{position:relative!important;inset:auto!important;width:100%!important;height:70vh!important;min-height:28rem;margin:0 0 6px!important}
 #mirador .mosaic-split{display:none!important}}
</style>
<body>
<header class="masthead">
<div class="top"><p class="kicker">Preserved archive · offline comparison</p><nav class="actions" aria-label="Comparison actions"><a href="/">&larr; Catalogue</a><a href="{{.ChangeSelection}}">Change selection</a><button id="copy-comparison" type="button">Copy comparison link</button></nav></div>
<h1 id="comparison-heading" tabindex="-1">Compare manuscripts</h1>
<ul class="titles">{{range .Items}}<li>{{.Title}}</li>{{end}}</ul>
</header>
<p class="notice">On a narrow screen the manuscript windows stack; a wider display is more comfortable for visual comparison.</p>
<div id="mirador"></div>
<p id="copy-status" class="copy-status" aria-live="polite"></p>
<script id="comparison-items" type="application/json">{{.ItemsJSON}}</script>
<script id="annotation-endpoints" type="application/json">{{.EndpointsJSON}}</script>
<script src="` + miradorRoute + `"></script>
<script>
(() => {
  const items = JSON.parse(document.getElementById('comparison-items').textContent);
  const annotationEndpointByCanvas = JSON.parse(document.getElementById('annotation-endpoints').textContent);
  Mirador.viewer({
    id: 'mirador',
    osdConfig: { preserveViewport: false },
    annotation: { endpointByCanvas: annotationEndpointByCanvas, strictRouting: true },
    window: {
      allowClose: false,
      allowFullscreen: true,
      sideBarOpenByDefault: true,
      defaultSideBarPanel: 'annotations',
      highlightAllAnnotations: true
    },
    workspace: { type: 'mosaic' },
    windows: items.map((item) => ({ manifestId: item.manifest }))
  });
  const status = document.getElementById('copy-status');
  document.getElementById('copy-comparison').addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(window.location.href);
      status.textContent = 'Comparison link copied.';
    } catch (_) {
      window.prompt('Copy this comparison link:', window.location.href);
      status.textContent = 'Comparison link ready to copy.';
    }
  });
  document.getElementById('comparison-heading').focus();
})();
</script>
</body>
</html>
`))

func safeComparisonSlug(slug string) bool {
	if slug == "" || strings.HasPrefix(slug, "/") || strings.Contains(slug, `\`) {
		return false
	}
	clean := path.Clean("/" + slug)
	return clean == "/"+slug && clean != "/." && !strings.Contains("/"+slug+"/", "/../") && !strings.Contains("/"+slug+"/", "/./")
}

func (s *Server) comparisonSelection(raw []string) ([]comparisonItem, map[string]string, error) {
	if len(raw) < minComparisonDocs || len(raw) > maxComparisonDocs {
		return nil, nil, fmt.Errorf("select between %d and %d manuscripts", minComparisonDocs, maxComparisonDocs)
	}
	seen := make(map[string]bool, len(raw))
	canvasOwner := make(map[string]string)
	items := make([]comparisonItem, 0, len(raw))
	for _, slug := range raw {
		if !safeComparisonSlug(slug) {
			return nil, nil, fmt.Errorf("unsafe manuscript selection %q", slug)
		}
		if seen[slug] {
			return nil, nil, fmt.Errorf("duplicate manuscript selection %q", slug)
		}
		seen[slug] = true
		summary, ok := s.catalog.get(slug)
		if !ok {
			return nil, nil, fmt.Errorf("unknown manuscript %q", slug)
		}
		manifestPath := filepath.Join(s.root, filepath.FromSlash(slug), "manifest.json")
		manifest, err := os.ReadFile(manifestPath) //nolint:gosec // slug was resolved through the catalogue and confined above
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil, fmt.Errorf("manuscript %q is no longer available", slug)
			}
			// Keep an unreadable manifest in the workspace so Mirador can show
			// its failed window. With no canvas mapping it cannot mutate notes.
			manifest = nil
		}
		endpoint := "/" + slug + "/annotations"
		for _, canvasID := range manifestCanvasIDs(manifest) {
			if previous, exists := canvasOwner[canvasID]; exists && previous != endpoint {
				return nil, nil, fmt.Errorf("canvas %q belongs to more than one selected manuscript", canvasID)
			}
			canvasOwner[canvasID] = endpoint
		}
		items = append(items, comparisonItem{Dir: slug, Title: summary.Title, Manifest: "/" + slug + "/manifest.json"})
	}
	return items, canvasOwner, nil
}

func manifestCanvasIDs(manifest []byte) []string {
	if len(manifest) == 0 {
		return nil
	}
	var doc struct {
		Sequences []struct {
			Canvases []struct {
				ID   string `json:"id"`
				AtID string `json:"@id"`
			} `json:"canvases"`
		} `json:"sequences"`
		Items []struct {
			ID   string `json:"id"`
			AtID string `json:"@id"`
			Type string `json:"type"`
		} `json:"items"`
	}
	if json.Unmarshal(manifest, &doc) != nil {
		return nil
	}
	seen := make(map[string]bool)
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, sequence := range doc.Sequences {
		for _, canvas := range sequence.Canvases {
			if canvas.ID != "" {
				add(canvas.ID)
			} else {
				add(canvas.AtID)
			}
		}
	}
	for _, item := range doc.Items {
		if item.Type == "Canvas" || item.Type == "sc:Canvas" {
			if item.ID != "" {
				add(item.ID)
			} else {
				add(item.AtID)
			}
		}
	}
	return ids
}

func (s *Server) serveComparison(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "comparison is GET-only", http.StatusMethodNotAllowed)
		return
	}
	docs := r.URL.Query()["doc"]
	items, endpoints, err := s.comparisonSelection(docs)
	if err != nil {
		http.Error(w, "invalid comparison: "+err.Error(), http.StatusBadRequest)
		return
	}
	itemsJSON, err := json.Marshal(items)
	if err != nil {
		http.Error(w, "could not encode comparison", http.StatusInternalServerError)
		return
	}
	endpointsJSON, err := json.Marshal(endpoints)
	if err != nil {
		http.Error(w, "could not encode annotation routes", http.StatusInternalServerError)
		return
	}
	query := url.Values{"doc": docs}.Encode()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = comparisonTmpl.Execute(w, comparisonPage{ //nolint:errcheck // best-effort response write
		Items: items,
		// Both values were produced by encoding/json, which escapes script
		// delimiters. Marking them as JS prevents html/template from quoting
		// the complete JSON value inside application/json script elements.
		ItemsJSON:       template.JS(itemsJSON),     //nolint:gosec // trusted encoding/json output
		EndpointsJSON:   template.JS(endpointsJSON), //nolint:gosec // trusted encoding/json output
		ChangeSelection: "/?" + query,
	})
}
