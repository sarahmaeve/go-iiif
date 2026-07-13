# DELTAS2: Local-first digital storytelling

Status: exploratory implementation plan, written 2026-07-12. Nothing here is
prescriptive. It is a way to make the product choices, technical seams, and
failure modes concrete enough to evaluate before implementation.

## Why this document exists

The [altomator/IIIF README](https://github.com/altomator/IIIF) groups three
different things under digital mediation and storytelling:

- [Storiiies](https://github.com/CogappLabs/StoriiiesViewer): a guided tour
  through points or regions of a IIIF image, with ordered narrative text and
  optional audio;
- [Exhibit](https://exhibit.so): a hosted authoring and publishing environment
  for richer stories, quizzes, IIIF images, and 3D media;
- [CanvasPanel](https://canvas-panel.netlify.app/): a toolkit for building
  custom IIIF layouts and applications.

Those examples establish a possibility, not a feature specification for this
repository. They represent three product scales: a tour, an exhibit platform,
and an application framework. The useful question is not “how do we match all
three?” but “what narrative capability belongs naturally in a durable,
offline, researcher-controlled IIIF library?”

The proposed answer is deliberately narrower:

> A story is a portable, ordered tour of locally preserved canvases. Each step
> focuses a canvas region and presents a caption. It can be authored, played,
> exported, imported, and inspected without an account or network service.

That is close to the Storiiies interaction model, borrows some authoring
expectations from Exhibit, and does not try to become CanvasPanel.

## Evidence from the referenced tools

This section records observations, not requirements. Sources were checked on
2026-07-12.

### Storiiies: a IIIF manifest can be the delivery format

The current open-source Storiiies Viewer consumes a constrained IIIF
Presentation 3 manifest. Its example story is a single Canvas with an embedded
AnnotationPage whose ordered `commenting` annotations contain:

- a `TextualBody` (`text/plain` or `text/html`);
- a Canvas target with an `xywh` media fragment;
- optionally a point selector or an audio body.

It also uses manifest `label`, `summary`, and `requiredStatement` for the title
card and attribution. The viewer currently documents important constraints:
embedded annotation pages only, incomplete multilingual support, and no
multiple-image story support. This makes its manifest shape an excellent
interchange target, but a poor choice for this application's only internal
storage model.

### Exhibit: authoring and publishing are most of the product

Exhibit's public description emphasizes an editor, responsive layouts,
stories and quizzes, IIIF and 3D media, themes, hosted/public/password-protected
publishing, embedding, duplication, and remixing. Most of that is product and
service surface rather than IIIF plumbing. Reproducing it would introduce
accounts, hosted state, permissions, layout systems, and media pipelines that
do not fit this repository's local preservation thesis.

Useful lessons to borrow are smaller: immediate preview, clear sequencing,
reordering, accessible presentation, and export that produces something a
researcher can actually share.

### CanvasPanel: composition is a different layer

CanvasPanel is useful when an application needs custom layouts and rendering
around IIIF resources. It is not necessary for a focused tour player. The
existing embedded Mirador/OpenSeadragon stack already handles preserved
images, canvases, annotations, OCR, and deep zoom. Adding a second rendering
framework before the story model is proven would multiply integration cost.

## Product position

### What the first complete feature should do

A researcher can:

1. create a named story in the local catalogue;
2. add a title, short introduction, and optional byline/credit;
3. navigate to a preserved canvas, frame a region, and add a plain-text
   caption;
4. repeat, reorder, edit, preview, and delete steps;
5. play the story with previous/next controls and a bookmarkable current step;
6. export/import it with the rest of the research metadata;
7. export an interoperable IIIF Presentation 3 story manifest where the story
   falls within the target format's capabilities.

The player remains useful with JavaScript interaction disabled only to the
ordinary degree expected of the existing viewer: story metadata and captions
should still be structurally present in the HTML, but deep-zoom transitions
require the embedded viewer.

### What it should not become in the first release

- a hosted publishing service, account system, or collaboration server;
- a general slide/page layout editor;
- a quiz engine;
- a rich-text CMS;
- a video, 3D, or arbitrary embed platform;
- automatic narration, synthetic voice, or generative captioning;
- a replacement for W3C annotations;
- continuous viewport or page synchronization between Mirador windows.

These exclusions are scope boundaries, not permanent rejections.

## Design principles

### Local-first and source-safe

Story state belongs under `.iiifpreserve/`, not inside immutable preserved
manifests or provenance. It uses the same atomic-write and corrupt-file
preservation behavior as the catalogue and saved comparisons. Story authoring
must never rewrite source manifests, images, linked OCR, or annotations.

### A story is not an annotation list

An annotation describes something about a target. A story adds order,
editorial framing, title-card metadata, and presentation intent. Existing MAE
annotations are valuable source material, but making a story a live list of
annotation IDs creates surprising coupling: editing or deleting a research
note would rewrite or break a narrative.

The safer rule is:

- “Add annotation to story” copies its target and textual body into a new
  story step;
- the step may record `source_annotation_id` for traceability;
- subsequent annotation changes do not modify the story automatically;
- the editor may offer an explicit “refresh from annotation” action later.

The story editor should also support capturing the current viewport directly,
so authoring is not dependent on MAE internals.

### Stable local identity, portable external identity

Canvas IDs can change during an upstream manifest refresh even when the
preserved image itself is reused. A step therefore should not rely on
`canvas_id` alone. Internally it should retain:

- bundle directory;
- original manifest URL;
- preserved image filename (the stable local artifact identity);
- Canvas ID observed when authored;
- source image service/resource ID when available.

Playback resolution should prefer the exact active Canvas ID, then use the
preserved image record to locate its current Canvas ID. A failed resolution is
shown as a broken step; it must never silently jump to a different page.

Portable export identifies the bundle primarily by original manifest URL and
the target by Canvas ID, following the existing comparison exchange pattern.

### The author decides the order

The `steps` array is authoritative. Do not infer order from creation time,
annotation order, Canvas order, filenames, or IDs. Every step has a stable ID
so URLs and editor operations survive reordering.

### Plain text first

The first authoring surface should store and render plain-text captions. This
avoids making HTML sanitization and rich-text editing prerequisites for the
core interaction. A later safe-rich-text body can be represented explicitly
as `{format, value}` and sanitized on both import and render. Imported IIIF
`text/html` must never be passed directly into a template.

## Proposed internal model

Store `.iiifpreserve/stories.json` as a versioned file:

```json
{
  "version": 1,
  "stories": [
    {
      "id": "8bb0f13d6d3d7a08c5590d76",
      "title": "Reading the erased marginal hand",
      "summary": "A short tour across three details.",
      "byline": "Sarah Researcher",
      "language": "en",
      "created_at": "2026-07-12T20:00:00Z",
      "updated_at": "2026-07-12T20:15:00Z",
      "steps": [
        {
          "id": "e012f45e77642d02",
          "bundle_dir": "example.org/iiif_manifest_ms.json",
          "manifest_url": "https://example.org/iiif/manifest/ms.json",
          "image_file": "0007.jpg",
          "canvas_id": "https://example.org/iiif/canvas/7",
          "source_image_id": "https://example.org/iiif/image/7",
          "region": {"x": 410, "y": 275, "width": 920, "height": 610},
          "heading": "A second layer of writing",
          "caption": "The drypoint ruling continues beneath the later note.",
          "source_annotation_id": "urn:annotation:optional-origin"
        }
      ]
    }
  ]
}
```

The exact field names can change after a prototype. The important choices are:

- integer pixel regions in Canvas coordinates, not viewer zoom/center values;
- empty or omitted `region` means the whole Canvas;
- captions are snapshots;
- source and local identities coexist;
- story and step IDs are opaque and stable;
- no server-origin URLs are persisted.

Suggested first-release limits:

- 100 stories per library;
- 500 steps per story;
- 200 runes in title and step heading;
- 2,000 runes in summary;
- 10,000 runes in a caption;
- 4 MiB maximum `stories.json`;
- positive finite region values, clamped to Canvas dimensions.

Reject duplicate IDs, duplicate case-insensitive story titles, invalid UTF-8,
unknown bundles, unresolved images, non-member canvases, zero-area regions,
and regions wholly outside the Canvas. Permit a region that touches an edge;
normalize it to the Canvas boundary.

## Playback architecture

### A dedicated single-window player

Use a new route such as `/__stories__/<story-id>/`, with editorial chrome:

- title, summary, and byline;
- caption panel;
- previous, next, restart, and “return to focus” controls;
- step count and progress;
- one Mirador window;
- a link back to the catalogue and a separate edit action.

Do not reuse the comparison mosaic. A story has one focus at a time, even if
later steps move between bundles.

Use `?step=<stable-step-id>` rather than only an ordinal. Navigation should
`pushState`; browser back/forward should restore the selected step. Unknown or
deleted step IDs fall back to the first valid step with an explicit notice.

### One-shot viewport control, not synchronization

`planning/LESSONS.md` records the failure of continuous page and viewport
synchronization. Story playback needs a narrower operation:

1. select the desired manifest/Canvas;
2. wait until that Canvas and its OpenSeadragon viewer are ready;
3. fit the viewport once to the stored Canvas-space rectangle;
4. stop controlling the viewport until the next explicit story navigation.

The researcher remains free to pan and zoom between steps. “Return to focus”
replays step 3. There is no viewport subscription feeding story state and no
attempt to mirror user motion.

### Prefer a tiny Mirador plugin seam

The pinned Mirador source exposes an `OpenSeadragonViewer` plugin hook with
the live `viewer`, `canvasWorld`, and `windowId`. `CanvasWorld` exposes Canvas
to world-coordinate conversion. A small in-tree story-focus plugin can:

- receive a desired `{stepID, canvasID, region, commandToken}`;
- wait until its `canvasWorld` contains the target Canvas;
- scale the Canvas-space region into world coordinates;
- call `viewer.viewport.fitBoundsWithConstraints(...)` once;
- acknowledge the command so React re-renders do not repeat it.

This is preferable to guessing Mirador's persisted `{x, y, zoom}` values or
subscribing to Redux viewport changes. It is also a clean place to implement
the inverse “capture current view” conversion for the authoring screen.

Before committing to the feature, build this as a throwaway or minimal spike
against the pinned Mirador commit. It is the only genuinely novel integration
risk.

### Canvas and manifest changes

For the first vertical slice, constrain a story to one preserved bundle while
allowing multiple canvases within it. That covers the normal guided-tour use
case and avoids asynchronous manifest replacement as a prerequisite.

Cross-bundle stories are a sensible second capability. Implement them only
after testing Mirador's single-window manifest replacement lifecycle. If it is
fragile, destroying and recreating the one viewer window at a bundle boundary
is acceptable; a deterministic transition is more important than preserving
unrelated viewer state.

## Authoring architecture

### Editor shape

Use a dedicated `/__stories__/<id>/edit` page rather than forcing story state
into the normal viewer template. A practical layout is:

- viewer on the left/top;
- ordered step list on the right/bottom;
- title/summary/byline fields;
- caption fields for the selected step;
- capture, update focus, whole Canvas, duplicate, move up/down, and delete;
- preview button opening the player at the selected step.

Drag-and-drop ordering is optional polish. Accessible move-up/move-down
buttons are required and sufficient for the first release.

### Capturing a step

“Add current view” asks the story-focus plugin for the visible viewport bounds,
converts them into the active Canvas coordinate space, clamps them, and posts
a normalized step draft to the server. If the complete Canvas is effectively
visible, store no region rather than a noisy near-whole rectangle.

“Use selected annotation” is a second path. Extract a FragmentSelector when
available and copy its textual body. SVG-only and arbitrary shape annotations
should initially be rejected with a clear explanation or reduced to a bounding
box only when that conversion is exact and tested.

### Mutation API

The editor is structured state, so use bounded JSON requests rather than a
large collection of form fields. Suggested routes:

- `POST /__stories__/create`
- `PUT /__stories__/<id>` for an atomic complete-story replacement
- `DELETE /__stories__/<id>`
- `GET /__stories__/<id>/data` for editor/player data
- `GET /__stories__/<id>/` and `/edit` for HTML

All non-GET requests use the existing `allowMutation` Host/Origin protection,
`http.MaxBytesReader`, exact content-type/method checks, and store-level
locking. A complete replacement keeps validation and crash consistency much
simpler than many reorder/patch endpoints. Require an `updated_at` or revision
token to reject stale-tab overwrites with `409 Conflict`.

As with comparisons, a corrupt state file remains untouched and disables story
edits rather than being replaced by an empty file.

## IIIF interoperability

### Make export a first-class deliverable

For a one-Canvas story, generate a Presentation 3 manifest compatible with the
documented Storiiies shape:

- story title → manifest `label`;
- summary → manifest `summary`;
- source attribution → `requiredStatement`;
- locally or originally identified painting body;
- one embedded AnnotationPage;
- ordered `commenting` annotations;
- `TextualBody` with `text/plain` initially;
- Canvas target plus `#xywh=x,y,w,h`, or a SpecificResource selector when a
  point is supported later.

Do not inject these annotations into the preserved source manifest. Generate a
new derived story manifest at a story route and/or explicit export command.
The output must identify itself as derived and retain links to the source
manifest.

Provide two clear modes if both are needed:

- **local playback manifest:** request-relative image/service URLs, fully
  offline while `iiifpreserve -serve` is running;
- **portable web manifest:** original source image/service URLs plus embedded
  story text, usable by external services but network-dependent.

Avoid emitting a file that looks portable while containing `localhost` URLs.

### Multi-Canvas and cross-bundle limits

Storiiies Viewer currently documents no multiple-image support, and grouping
annotations by Canvas loses a single global step order. Do not claim compatible
export for a story outside the supported one-Canvas subset.

Possible later representations include a synthetic Canvas per step, a Range
that orders step resources, or a tool-specific extension. Each has semantic
costs and should be validated with real consumers before adoption. Internal
playback need not wait for this interoperability problem to be solved.

### Import

Importing arbitrary IIIF manifests as editable stories is not MVP. A later
importer can recognize the constrained Storiiies-style shape, preserve unknown
fields where practical, sanitize HTML bodies, and report unsupported media or
multiple-image structures rather than flattening them silently.

## Research metadata portability

Add stories to the researcher-authored metadata archive. Because the current
format rejects every version other than 1, adding a field under version 1
would let older importers silently discard stories. Prefer:

- export version 2 once stories ship;
- accept both versions 1 and 2 on import;
- preserve existing version-1 behavior;
- identify bundles by manifest URL first, bundle directory second;
- merge story names case-insensitively;
- treat identical story content as a duplicate;
- keep local state and warn on same-name content conflicts;
- support `-dry-run` with accurate story counts and warnings.

The standalone IIIF story-manifest export and research-metadata archive solve
different problems: interoperability with viewers versus lossless transfer
between two local libraries. Keep both.

## Accessibility and presentation requirements

Accessibility belongs in the first definition of done:

- all navigation and reordering works by keyboard;
- caption changes receive an appropriate live announcement without rereading
  the entire page;
- focus moves predictably after next/previous, deletion, and reorder;
- controls expose disabled state and meaningful labels;
- text remains selectable and responds to browser zoom;
- the caption panel can precede or follow the image in document order without
  relying on visual position;
- `prefers-reduced-motion` makes region changes immediate;
- image interaction is never the only way to advance;
- story title, current step, and total steps are exposed structurally;
- external caption links use safe schemes and `rel="noopener noreferrer"`;
- an empty or broken step has an intelligible textual error.

Autoplay is out of scope. If audio narration arrives later, it must never start
without an explicit user action and must have a text alternative.

## Integrity and diagnostics

Extend doctor to inspect stories without changing them:

- file/version/schema validity;
- duplicate story or step IDs;
- title and size limits;
- referenced bundle and preserved image existence;
- Canvas resolution against the active manifest/provenance;
- finite, positive, in-bounds regions;
- missing captions or broken optional annotation origins as warnings;
- irrecoverable target failures as errors.

The catalogue should display a broken-story indicator rather than omitting a
story whose source changed. The editor can offer explicit repair by selecting a
new target.

Deletion of a bundle is currently external filesystem action. It may leave a
story broken; it must not cascade-delete authored research.

## Implementation slices and gates

### Gate 0 — viewport spike

Build a minimal story-focus plugin and static fixture with two regions on one
Canvas and two canvases in one manifest.

Acceptance:

- each explicit navigation fits the correct region once;
- the user can pan away without being snapped back;
- “return to focus” works;
- changing Canvas then fitting is deterministic under cached and cold tiles;
- repeated React/Redux updates do not replay the command;
- keyboard navigation and reduced motion work;
- no regression to ordinary viewer or comparison behavior.

Stop or redesign here if this is unreliable. Do not build persistence first.

### Slice 1 — read-only player over a fixture

Add story HTML/data routes and render a checked-in fixture, still without an
editor or store. Establish responsive layout, URL/history semantics,
accessibility, error states, and browser tests.

### Slice 2 — story store and validation

Add `story_store.go` and `story_validation.go`, modeled on but not mechanically
copied from `comparison_store.go`. Cover deep-copy behavior, atomic saves,
revision conflicts, corrupt-file preservation, size limits, target resolution,
and concurrent reads/writes.

### Slice 3 — minimal authoring

Create/edit/delete a one-bundle story, capture the current view, edit captions,
and reorder steps. Use complete validated replacement with optimistic
concurrency. Add annotation-to-step only after direct viewport capture works.

### Slice 4 — metadata export/import and doctor

Introduce research metadata version 2 with backward-compatible v1 import,
non-destructive story merge, dry-run reporting, and exhaustive diagnostics.

### Slice 5 — IIIF story export

Generate and validate the one-Canvas Presentation 3 subset. Test the output
against the open-source Storiiies Viewer, an ordinary IIIF validator, and this
application's own preserved/local image routes. Clearly reject unsupported
multi-Canvas export.

### Slice 6 — optional cross-bundle stories

Prototype one-window manifest replacement. If reliable, relax validation and
add transitions across preserved bundles. If not, keep the simpler one-bundle
contract; it is still a complete storytelling feature.

## Test strategy

### Go unit tests

- schema and normalization table tests;
- region parsing, clamping, and scale conversions;
- stable target resolution after Canvas-ID-only refresh;
- state add/update/delete, deep copies, limits, and corrupt files;
- route method/content-type/body-limit/mutation-origin tests;
- research metadata v1/v2 compatibility and merge conflicts;
- deterministic IIIF export fixtures;
- doctor findings for every broken-reference class.

### Browser tests

- capture visible region → save → reload editor → same region;
- next/previous and browser history;
- Canvas transition followed by one-shot region fit;
- user pan is not undone;
- edit/reorder/delete with keyboard only;
- narrow-screen caption/viewer layout;
- ordinary annotations and OCR overlay still work in the player;
- network-disabled playback of a local story;
- no requests to source image hosts in local mode.

Avoid tests that merely search the vendored bundle for action names. The core
risk is behavioral and belongs in a browser.

### Interoperability checks

- generated JSON passes Presentation 3 structural validation;
- a one-Canvas story plays in the current Storiiies Viewer;
- plain Mirador still displays the derived manifest sensibly;
- portable export contains no localhost URL;
- local export contains no remote image/OCR request.

## Likely code map

New Go files, names provisional:

- `internal/serve/story_store.go`
- `internal/serve/story_validation.go`
- `internal/serve/story_mutations.go`
- `internal/serve/story_player.go`
- `internal/serve/story_export.go`
- corresponding `_test.go` files

Existing Go changes:

- `internal/serve/serve.go`: routes and `Server.storyStore`;
- `internal/serve/viewer.go`: catalogue story summaries/actions;
- `internal/serve/research_metadata.go`: v2 story exchange;
- `internal/serve/doctor.go`: story diagnostics;
- `cmd/iiifpreserve/main.go`: only if standalone story export earns a CLI
  flag; do not add one merely because a route exists.

Viewer changes:

- `viewer-src/src/story-focus.js`: tiny OpenSeadragonViewer plugin;
- `viewer-src/src/index.js`: plugin registration/export seam;
- committed rebuilt `internal/serve/viewer/mirador.min.js`;
- story page JavaScript should remain small and page-specific rather than
  turning the normal viewer into an editor.

## Risk register

| Risk | Consequence | Mitigation / decision gate |
|---|---|---|
| Mirador Canvas switch races OSD fit | Wrong region or intermittent playback | Gate 0; plugin acknowledgement after target Canvas is live |
| Redux/render updates replay focus | User is snapped back after panning | Command token consumed once; no viewport subscription |
| Canvas IDs change on refresh | Broken or misdirected steps | Anchor to preserved image file plus manifest/service identities; never silently retarget |
| Story state and annotations diverge | Confusing authored output | Snapshot on add; optional provenance link; explicit refresh only |
| HTML caption injection | Local script execution/data exposure | Plain text MVP; allowlisted sanitization before any HTML support |
| Old importer discards new stories | Silent research loss | Metadata archive v2; v1/v2 import; explicit counts |
| Interop export overpromises | External viewer fails or changes order | Declare and enforce one-Canvas supported subset |
| Feature grows into a CMS | Long implementation with weak preservation value | Hold quizzes, arbitrary layouts/media, accounts, and hosting outside MVP |
| Cross-bundle transitions are fragile | Complex state and blank viewer windows | One-bundle feature first; separate Slice 6 gate |

## Recommended decision

Proceed only through Gate 0 first. If one-shot focus is reliable, build the
one-bundle vertical slice through authoring, playback, metadata portability,
and one-Canvas IIIF export. That is small enough to remain recognizably part of
this tool and complete enough to be useful.

Do not start by adopting an external storytelling framework. The durable
asset here is the story model and its portable relationship to preserved IIIF
content. The rendering surface can remain a thin layer over the Mirador and
OpenSeadragon stack already carried by the repository.
