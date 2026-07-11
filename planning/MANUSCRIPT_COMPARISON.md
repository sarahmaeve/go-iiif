# Manuscript comparison workspace

Status: proposed

## Purpose

Let a researcher select two to four preserved manuscripts from the local
catalogue and inspect them side by side in one Mirador workspace. The first
version should make visual comparison immediate without introducing accounts,
a database, or a second viewer stack.

Typical uses include comparing scribal hands, page layouts, decoration,
bindings, related textual witnesses, or different institutional photographs
of the same object.

## User experience

### Selecting manuscripts

Each catalogue entry gains an **Add to comparison** control. Selecting an
entry adds it to a fixed comparison tray at the bottom of the catalogue.

The tray:

- shows the short title of each selected manuscript;
- permits removal and reordering;
- accepts two to four manuscripts;
- disables **Compare manuscripts** until two are selected; and
- explains the four-manuscript limit rather than silently dropping entries.

Selection initially lives in the page and the resulting URL. It does not need
to be written to the catalogue. Refreshing the catalogue before opening the
workspace may clear the tray; opening the comparison produces a reusable URL.

### Comparison workspace

The comparison page uses the existing editorial masthead and embedded Mirador
bundle. It opens one Mirador window per selected manuscript, arranged in a
two-column workspace where screen width permits. Mirador remains responsible
for canvas navigation, zoom, rotation, fullscreen, and annotation display and
editing.

The page provides:

- **Back to catalogue**;
- the title of each selected manuscript;
- **Copy comparison link**;
- **Change selection**, returning to the catalogue with the current selection;
- a clear small-screen message, followed by a usable stacked layout; and
- the normal offline indication.

The first release does not synchronize page turns, zoom, or pan. Manuscripts
rarely have page-for-page alignment, and implicit synchronization would be
surprising. Those behaviors can be explicit follow-up tools.

## URL and server contract

Use a GET-only route with repeated query parameters:

```text
/__compare__/?doc=iiif.bodleian.ox.ac.uk/a&doc=gallica.bnf.fr/b
```

`doc` values are catalogue bundle slugs, not filesystem paths or arbitrary
manifest URLs. The handler resolves every value through the in-memory
catalogue and rejects unknown, duplicate, unsafe, fewer-than-two, and
more-than-four selections with a useful 400 response. The catalogue controls
construct the query with `URLSearchParams`.

The route is intentionally encoded in the URL so a comparison can be
bookmarked or copied on the same researcher's machine. The URL is local: it is
not expected to work on another researcher's library unless the same bundle
slugs exist there.

## Technical design

### Go server

Add:

- `compareRoute = "/__compare__/"` in `internal/serve/viewer.go`;
- a `comparisonPage` template model containing the selected
  `manifestSummary` values and their local manifest URLs;
- `serveComparison`, registered before generic bundle routing; and
- catalogue template controls and the small comparison-tray script.

The comparison template must pass manifest configuration as JSON produced by
`encoding/json`, not by concatenating JavaScript strings. The existing rules
for loopback-only serving and local mutation endpoints remain unchanged.

Mirador configuration is the existing viewer configuration with multiple
windows:

```javascript
windows: selected.map((item) => ({ manifestId: item.manifest }))
```

Let Mirador choose window identifiers and initial layout. Avoid depending on
undocumented Redux actions for the first release.

### Annotation routing prerequisite

The current embedded wrapper derives one annotation endpoint from the single
`#mirador` element's `data-manifest`. That is not correct in a multi-manifest
workspace: an annotation created on manuscript B could otherwise be sent to
manuscript A.

Before enabling annotation editing on the comparison page, build a mapping
from every selected manifest's canvas IDs to its local
`/<bundle>/annotations` endpoint. The server can derive the canvas IDs while
reading the already-local manifests. Supply that map to the page as JSON and
configure MAE's adapter as:

```javascript
adapter: (canvasId) => new Mirador.HttpAnnotationAdapter(
  annotationEndpointByCanvas[canvasId],
  canvasId,
)
```

If a canvas is not in the map, the adapter must fail closed: annotation
editing is disabled for that canvas and must never fall back to another
manuscript's endpoint. The ordinary one-manuscript viewer may retain its
current convenience fallback.

### State and persistence

The MVP adds no persistent schema. Selection is represented by the comparison
URL. Catalogue metadata and annotations continue using their existing files.

A later saved-workspace feature could add named comparison sets to researcher
metadata export/import. That should be a separate schema decision rather than
silently expanding catalogue entries now.

## Accessibility and keyboard behavior

- Selection controls have visible labels and expose selected state with
  `aria-pressed` or native checkboxes.
- The tray announces additions/removals through a polite live region.
- Keyboard users can add, remove, reorder, and open a comparison.
- Focus moves to the comparison heading after navigation, not directly into a
  Mirador window.
- Color is not the only indication that a manuscript is selected.

## Failure behavior

- A selected bundle removed before navigation produces a 400 page naming the
  missing catalogue item.
- A manifest that becomes unreadable produces a visible failed window without
  preventing the other selected manuscripts from opening.
- A canvas lacking a known annotation endpoint remains viewable but cannot
  mutate annotations.
- No comparison request may cause remote manifest or image access; all
  `manifestId` values are local rewritten-manifest routes.

## Acceptance criteria

1. A researcher can select two, three, or four catalogue entries and open all
   of them in one Mirador workspace.
2. The workspace loads with the network disconnected.
3. Its URL can be bookmarked and recreates the same ordered selection.
4. Unknown, duplicate, traversal-shaped, and over-limit `doc` inputs are
   rejected without reading outside the library.
5. Existing one-manuscript viewing is unchanged.
6. Annotation reads and mutations are routed to the bundle owning the active
   canvas; a missing mapping cannot write to another bundle.
7. The catalogue selection flow works with mouse and keyboard and has a
   comprehensible narrow-screen fallback.

## Test plan

- Handler tests for selection count, ordering, unknown slugs, duplicates, and
  traversal inputs.
- Template test confirming that each local manifest appears once and remote
  source URLs are not used as `manifestId` values.
- Canvas-to-annotation-endpoint unit tests covering both Presentation 2 and 3
  manifests.
- Mutation integration test proving annotations made against two selected
  manuscripts land in their respective `annotations.json` files.
- Catalogue DOM behavior can remain covered by focused script/unit tests. The
  parked headless-browser harness is not required to ship this feature.

## Delivery slices

1. Multi-select catalogue tray, validated comparison route, and read-only
   multi-window workspace.
2. Correct per-canvas annotation routing and annotation editing.
3. Optional deep links to initial canvases and named saved comparison sets.
4. Only after research use demonstrates a need: explicit synchronized zoom,
   pan, or page pairing.

