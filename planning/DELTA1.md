# DELTA1: OCR text display and digital storytelling

Status: Part 1 Option A (textoverlay vendoring) is **implemented and
browser-verified** as of 2026-07-12 — it required a local patch for an
upstream crash; see the implementation-status block for what was found
and what verification remains. Part 2 (storytelling) is an unbuilt plan.

## Purpose

[DELTAS.md](DELTAS.md) rated two of altomator's demonstrated capabilities
🔴 for this tool:

- **Display OCR as a text overlay** (their `mirador-textoverlay` example);
- **Digital storytelling** (Storiiies / Exhibit / CanvasPanel — guided,
  narrated tours through a document).

This document specifies what code changes each would take, grounded in the
actual source as of 2026-07-12. Note the tension: DELTAS.md recommended the
textoverlay direction (its item 4) but listed storytelling under "explicitly
decline". This plan specifies both so the decline can be an informed
decision rather than a reflex.

---

## Part 1 — Display OCR as a text overlay

### What already works (verified in source)

The preservation and serving halves of this feature are **already built**;
the entire gap is viewer-side display.

- **Preserve.** `DiscoverLinkedResources` (`internal/preserve/linked.go`)
  collects canvas `seeAlso` entries (any format, ALTO included) and
  `otherContent`/`annotations` references; `preserveLinkedResources`
  fetches them into `resources/<sha256(url)>.xml` with SHA-256 recorded in
  provenance. Gallica's per-page ALTO linked via `seeAlso` is exactly this
  shape.
- **Serve.** `rewriteLinkedReferences` (`internal/serve/rewrite.go`)
  rewrites the served manifest's `seeAlso`/`otherContent` URLs to the local
  `resources/` copies. Same-origin, so no CORS problem; `.xml` gets a
  correct content type from the file server.
- **Viewer.** The custom Mirador 4 UMD (`viewer-src/src/index.js`) injects
  plugins at exactly one point:
  `MiradorAll.viewer(merged, [...maePlugins, ...plugins])`. There is a
  clean seam for another plugin.

So today a preserved Gallica bundle already *contains and serves* its OCR
offline — it is preserved-but-invisible.

### Ecosystem status (researched 2026-07-12 — supersedes the first draft)

The first draft of this document claimed `mirador-textoverlay` was
Mirador-3-only and proposed a port. **That is no longer true.** Upstream
`dbmdz/mirador-textoverlay` released **1.0.0 on 2026-05-18, updated to
Mirador 4 / MUI v7 / React 19**, with fixes through **1.0.4 (2026-06-25)**.
Its published peer dependencies — `mirador 4.x`, `react ^18 || ^19`, MUI
`^7.3` in its own deps — match `viewer-src/package.json` (Mirador 4 source
checkout, React ^19, MUI ^7) exactly. The wider ecosystem moved too:
Daniel Berthereau published 2026 Mirador 4 ports of the companion
`mirador-ocr-helper` (side-panel OCR text with bidirectional
image↔panel highlighting), surfaced through the Omeka S Mirador module.

OCR discovery in textoverlay 1.x matches our preserved data: it reads
canvas `seeAlso` entries with format `application/xml+alto` /
`text/vnd.hocr+html` **or a profile starting with
`http://www.loc.gov/standards/alto/`** — Gallica's per-canvas
`"profile": "http://www.loc.gov/standards/alto/ns-v4#"` matches by profile
prefix, and our serve-time rewrite swaps only the URL to the local
`resources/` copy, leaving profile/format untouched. Same-origin fetch, so
no CORS issue. It also renders v3 line-level `supplementing` annotations
and v2 `contentAsText`, which keeps Option B below fully compatible.

Options, in recommended order:

### Option A (recommended): adopt upstream mirador-textoverlay 1.x

No Go changes to preserve or serve — those halves already work. The whole
change is in the one-time viewer vendoring build:

1. `viewer-src/package.json`: add `"mirador-textoverlay": "1.0.4"`
   (pinned, following the MAE-pinning precedent and its rationale of
   reading the published dist before trusting a bump).
2. `viewer-src/src/index.js`: `import textOverlayPlugin from
   'mirador-textoverlay';` and spread it into the existing injection
   point: `MiradorAll.viewer(merged, [...maePlugins,
   ...textOverlayPlugin, ...plugins])`. If the plugin emits a sidecar
   CSS file, inline it exactly as MAE's is (`?inline` import + injected
   `<style>`), preserving the single-asset embed model.
3. `internal/serve/viewer.go` (`viewerTmpl`): add `textOverlay` config to
   the `Mirador.viewer` window settings (e.g. `{ enabled: true,
   selectable: true }`) so the toolbar appears on bundles that have OCR.
4. `make viewer` → new `mirador.min.js` → `go:embed` as today.

Verification checklist before calling it done:

- Our Mirador checkout is a post-4.0.0 master build (required by MAE);
  confirm textoverlay 1.0.4 works against that commit, not just the
  4.0.0 npm tag it was built for.
- MAE coexistence: both plugins add OSD overlays and companion-window
  state; smoke-test drawing an annotation with the text layer enabled.
- A real preserved Gallica bundle with ALTO `seeAlso`: text renders,
  is selectable, and works fully offline (network disabled).
- Bundle size delta of the embedded UMD.
- Extend `browser_smoke_test.go` with a text-layer assertion.

#### Implementation status (2026-07-12): implemented but untested

What landed:

- `mirador-textoverlay` pinned at `1.0.4` in `viewer-src/package.json`;
  published dist read before adoption (single JS module, no sidecar CSS).
- Plugin spread into the existing injection point in
  `viewer-src/src/index.js`, with a comment documenting the upstream
  packaging quirk: the dist imports `@mui/icons-material` without
  declaring it. To cover that hole, `@mui/icons-material` **was added as
  a direct dependency** in `viewer-src/package.json`. (An earlier draft
  of this note claimed no new declaration was added and that the import
  resolved transitively through the Mirador checkout — that did not
  match the code.) An upstream issue is worth filing.
- `textOverlay: { enabled: true, selectable: true, visible: false }`
  added to both `Mirador.viewer` configs (`internal/serve/viewer.go`,
  `internal/serve/comparison.go`), test-first
  (`TestServer_ViewerPagesEnableTextOverlay`).
- `make viewer` rebuilt cleanly against the pinned Mirador base: the
  post-4.0.0 master commit `23a93a6f` the sibling `../mirador` checkout
  has carried since 2026-05-15 — the same base every prior bundle (and
  MAE 1.2.4) was built and verified on. The commit is now recorded in
  `viewer-src/MIRADOR_COMMIT` and enforced by `make viewer`. (During the
  original session the checkout was accidentally pulled forward to
  4.1.0 master and the bundle built on that unvetted base; it was rolled
  back to `23a93a6f` and rebuilt on 2026-07-12.) The embedded bundle
  grew 4.09 → 4.18 MB and contains the plugin's code. Full offline
  `go test ./...` green.

#### Verification (2026-07-12, follow-up session): rendered in-browser

Dogfooded against altomator's OCR-linked Vogue manifest
(`raw.githubusercontent.com/altomator/IIIF/main/collection/vogue-avec-ocr/bpt6k9604118j.json`,
74 canvases, ALTO `seeAlso` on canvases 19–21, ~16–20 min preserve
under the 13 s Gallica throttle). The serve-time rewrite, local ALTO
serving, and plugin discovery all worked first try; rendering did not:

- **Upstream bug found (mirador-textoverlay 1.0.4, present in vanilla
  1.x):** two of its sagas read `annotationJson.resources` unguarded,
  assuming the v2 `sc:AnnotationList` shape. MAE's adapter emits W3C
  AnnotationPages (`items`, no `resources`) for every canvas, so the
  read threw a `TypeError`; redux-saga treats that as fatal to the
  plugin's whole watcher tree and **cancelled the in-flight ALTO
  fetch** — cancellation dispatches neither success nor failure, so the
  overlay spinner span forever. Fixed by guarding both reads in the
  vendoring build (`viewer-src/patches/apply.mjs`, run by `make viewer`
  between `npm ci` and the bundle build;
  `TestViewerBundleGuardsTextOverlayAgainstV3AnnotationPages` pins the
  patched forms in the committed bundle). Upstream issue worth filing;
  drop the patch when a fixed release is pinned.
- With the patch, the text overlay renders on all three ALTO canvases
  of the preserved bundle, and the visibility/opacity controls work.
- Benign leftover: a Reselect dev-mode memoization warning ("result
  function returned its own inputs") logs once per page from the
  plugin's selectors; advisory only.

Still open:

- A formal network-down check (everything fetches from localhost, so
  this should be a formality) and a smoke of MAE annotation drawing
  with the text layer enabled on the same canvas.
- **Finding from the first dogfood attempt:** Gallica's *own* manifests
  do not carry per-canvas ALTO `seeAlso` (verified live: one
  manifest-level OAI `seeAlso` only — which also fails to preserve, by
  policy, being plain-HTTP). altomator hand-linked ALTO in his manifest
  copies. So a natively preserved Gallica bundle will **not** trigger
  the overlay; institutions that publish per-canvas ALTO `seeAlso`
  natively (e.g. MDZ/BSB — unverified) remain worth checking.

### Option B (complementary, optional): server-side ALTO → annotations, Go only

Convert preserved ALTO into W3C `supplementing` annotations at serve time
and let annotation-aware viewers render them. This is the IIIF cookbook
newspaper recipe (0068) — the same approach altomator points at under
"OCR as Annotations" — implemented in pure Go with no new JS dependency.
With Option A adopted this is no longer the display path, but it retains
independent value: textoverlay 1.x itself renders line-level
`supplementing` annotations, the converted text becomes visible to any
*external* IIIF consumer of our served manifests, and the parser is the
foundation for a future local content-search over OCR. Build on demand,
not speculatively.

Code changes:

1. **New package `internal/ocr`** — ALTO parser.
   - `Parse(data []byte) (Page, error)` using `encoding/xml`:
     handle ALTO v2–v4 namespaces; extract `Page` dimensions and
     `TextLine` elements with `HPOS/VPOS/WIDTH/HEIGHT` and concatenated
     `String CONTENT` (line granularity, not word — thousands of word
     annotations per page would swamp Mirador).
   - Coordinate scaling: ALTO page units do not always equal canvas
     pixels (Gallica's do; others measure in tenths of millimetres per
     `MeasurementUnit`). Scale `line × (canvasW / altoPageW)` and record
     the assumption. TDD with a real downloaded Gallica ALTO fixture
     (text XML — allowed in-repo).
2. **Conversion endpoint** in `internal/serve`:
   `GET /<dir>/ocr/<canvas-index>` returns an AnnotationPage
   (v3) or `sc:AnnotationList` (`?fmt=oa`, mirroring the existing
   annotations endpoint convention in `internal/serve/serve.go`) of
   line-level `supplementing` annotations with
   `xywh=` fragment targets on the canvas. Read-only GET — no
   loopback-mutation guards needed beyond what serving already does.
   Cache converted pages keyed by resource file stamp (copy the
   `manifest_cache.go` pattern).
3. **Manifest injection** in `rewriteManifest` (`internal/serve/rewrite.go`):
   for each canvas whose `seeAlso` resolved to a preserved ALTO resource,
   add an `annotations` (v3) / `otherContent` (v2) reference to the
   conversion endpoint. Important distinction from user annotations:
   DESIGN §4.4 deliberately does *not* inject the MAE store into the
   manifest because MAE's adapter loads it (injection would double every
   annotation). OCR annotations are the opposite case — read-only, never
   owned by MAE's adapter — so manifest injection is the correct and
   non-colliding path. Verify MAE's adapter ignores them (different
   endpoint; its `all` reads only `/<dir>/annotations`).
4. **Doctor** (`internal/serve/doctor.go`): warn when a canvas `seeAlso`
   ALTO exists but fails to parse (non-fatal — OCR is additive).
5. **Verification**: extend `browser_smoke_test.go` — open a bundle with
   preserved ALTO, toggle Mirador's annotation display, assert line
   highlights appear.

### Discarded options (kept for the record)

The first draft proposed a **port/fork of the Mirador-3 textoverlay** and a
**hand-rolled OSD overlay**. Both are obsolete: upstream shipped native
Mirador 4 support (1.0.0, 2026-05-18) on exactly our React 19 / MUI 7
stack, so there is nothing to port and nothing to hand-roll. If upstream
ever abandons Mirador 4, the pin plus the vendored `node_modules` in the
one-time build keeps our embedded asset reproducible in the meantime.

### Recommendation

Ship Option A: pin `mirador-textoverlay@1.0.4`, wire it into the existing
plugin injection point, verify offline against a preserved Gallica ALTO
bundle. Consider the Daniel Berthereau Mirador 4 `mirador-ocr-helper`
port (side-panel text with image↔panel highlighting) as a second,
independent vendoring decision after textoverlay lands. Hold Option B
until an external-consumer or search use case actually appears.

---

## Part 2 — Digital storytelling (guided tours)

### Scope definition

A Storiiies-style story is: an ordered list of steps, each step =
(manuscript, canvas, region, caption); a player that advances step by
step, moving the viewport to each region beside its caption; and an
authoring flow to create those steps. Local-first, no accounts, portable.

### Existing building blocks (verified in source)

Three of this feature's structural parts already exist in other clothes:

- **Region + text authoring is built.** MAE annotation authoring
  (`viewer-src/src/adapter.js`, `internal/annotation`) already lets a
  researcher draw a region on a canvas and attach text, persisted per
  bundle with stable IDs. A story step *is* an annotation plus ordering.
- **Named portable workspace storage is built.**
  `internal/serve/comparison_store.go` (`comparisons.json`, atomic writes,
  ID minting) + `comparison_mutations.go` (save/delete endpoints with
  loopback Host and Origin checks) + `research_metadata.go` (export/import
  with non-destructive merge, `mergeComparisons`) form the exact pattern
  to copy.
- **Bookmarkable multi-document viewer routing is built.**
  `internal/serve/comparison.go` (slug/canvas query routing,
  `safeComparisonSlug`, `manifestCanvasIDs`) and the `viewerTmpl` /
  `comparisonTmpl` chrome show how a new page type plugs in.

### The one real risk: driving the viewport

`planning/LESSONS.md` documents that *continuous two-way* viewport
synchronization between Mirador windows failed and was removed. A story
player needs something much weaker: a **one-shot, one-directional**
command per step — set the canvas, then fit the viewport to a region.
No store subscriptions, no feedback loops, no cross-window mirroring.
Design rule for this feature: the player dispatches
(`setCanvas`, then `updateViewport`/OSD `fitBounds` computed from the
step's `xywh`) exactly once per user navigation, and never listens to
viewport state. If even one-shot `updateViewport` proves unreliable in
Mirador 4, fall back to acquiring the OSD instance and calling
`viewport.fitBounds` directly — LESSONS.md notes dispatch-level control
was the unreliable layer.

### Implementation slices (TDD each)

1. **Story store** — `internal/serve/story_store.go`:

   ```go
   type storyStep struct {
       Dir      string // bundle slug, validated like comparison slugs
       CanvasID string
       Region   string // "x,y,w,h" or empty = whole canvas
       Caption  string // authored text
   }
   type savedStory struct {
       ID, Title string
       Steps     []storyStep
   }
   ```

   Stored in `.iiifpreserve/stories.json` beside `comparisons.json`;
   atomic write + ID minting copied from `comparisonStore`. Red/Green:
   list/add/delete/round-trip, unsafe-slug rejection.
2. **REST surface** — `story_mutations.go`: `GET /stories` (list),
   `POST /stories/save`, `POST /stories/delete`, mirroring
   `handleComparisonSave`/`handleComparisonDelete` including the loopback
   Host + Origin guards. Validate every step's `Dir` against the catalogue
   and `CanvasID` against the bundle's manifest (reuse
   `manifestCanvasIDs`).
3. **Authoring, minimal slice** — no new drawing UI. In the viewer page
   (`viewerTmpl`), each existing MAE annotation gets an "Add to story"
   control: pick/create a story, append a step from the annotation's
   canvas + region + text (editable caption). Ordering UI copies the
   comparison tray's move-up/move-down list (`indexTmpl` compare-tray
   pattern). This makes MAE the region editor for stories for free.
4. **Player page** — `GET /story/<id>`: editorial chrome (masthead +
   caption panel + prev/next + step counter) beside a single Mirador
   window. Step navigation performs the one-shot viewport command above.
   Steps may cross bundles (each step names its own `Dir`); switching
   manifests between steps is `setCanvas`/window-update on the same
   window, not multiple synced windows — deliberately inside the
   LESSONS.md safety envelope. Bookmarkable: `/story/<id>?step=3`.
5. **Export/import** — add `stories` to `researchMetadataArchive`
   (`research_metadata.go`), version-bumped, with a non-destructive
   `mergeStories` modeled on `mergeComparisons` (match bundles by original
   manifest URL, skip exact duplicates, warn on conflicts). This makes a
   story a *shareable research artifact* — the local-first answer to
   Storiiies' hosted links.
6. **Optional later** — export a story as a standalone IIIF v3 manifest
   whose canvases carry `annotations` with the captions (interop with
   external tools), or as a single static HTML file. Out of scope for the
   first cut.

### What we deliberately do not build

- No hosted/publishing component (Storiiies' and Exhibit's core) — sharing
  happens through the metadata export, consistent with the local-first
  thesis.
- No slide transitions/theming beyond the existing editorial style.
- No CanvasPanel-style embeddable framework.

### Risk ordering

Slice 4 carries the viewport risk — prototype the one-shot `fitBounds`
against Mirador 4 *first*, before building any chrome around it. The other
slices follow patterns that already exist in this codebase.

---

## Suggested sequencing

1. Part 1 Option A (vendor mirador-textoverlay 1.0.4) — preserved OCR
   becomes a selectable text layer, offline, with no Go changes.
2. Prototype the Mirador 4 one-shot viewport command (a spike that
   de-risks all of Part 2 and is also reusable for "jump to annotation"
   in the plain viewer).
3. Part 2 slices 1–5.
4. Part 1 Option B (ALTO → annotations in Go) only when an external-
   consumer or local-search use case appears.
