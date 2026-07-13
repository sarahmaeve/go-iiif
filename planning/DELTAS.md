# Deltas against altomator's IIIF experiments

Status: Analysis, audited against actual source (not DESIGN.md) on
2026-07-12; two corrections from that audit are marked *(verified)*
below. One recommendation has since been acted on: the OCR text-overlay
vendoring (item 4 / DELTA1 Part 1 Option A) is **implemented and
browser-verified** in the working tree (it needed a local patch for an
upstream plugin crash). Implementation deltas for the OCR-overlay and
storytelling items live in [DELTA1.md](DELTA1.md).

## Purpose

Measure this tool against the use cases demonstrated in
[altomator/IIIF](https://github.com/altomator/IIIF) ("IIIF experiments with
Gallica content") to see which examples our system can already perform, which
it can perform partially, and which it cannot — and why. The goal is to
locate the mission-relevant gaps worth closing, not to chase feature parity
with a demonstration repository.

## Framing: two different kinds of tool

Almost every gap below follows from one distinction.

- **altomator/IIIF is a showcase.** It is hand-authored manifests and
  collection JSON, a couple of Perl generation scripts, and Roboflow-trained
  computer-vision output, all *demonstrated through third-party hosted web
  viewers* (Mirador drag-and-drop, AllMaps, Storiiies, Compariscope /
  LayerStack, OpenSeadragon, mirador-textoverlay). It is an authoring and
  demonstration repository.
- **iiifpreserve is a preservation binary.** It crawls, filters, downloads
  images, builds local IIIF level0 tile pyramids, serves over HTTPS with a
  serve-time manifest rewrite, and embeds a Mirador 4 + MAE viewer for
  offline viewing. It is a consume-and-preserve-offline tool.

So each example has two honest questions: *can we preserve and reproduce it
offline?* and *can we author or generate it?* We are strong on the first and
largely absent on the second.

## Feature-by-feature verdict

Legend: ✅ yes · 🟡 partial · 🔴 no · ⚪ not applicable.

### Comparing documents and images

- 🟡 **Compare two IIIF documents side by side** (their Galileo BnF +
  Stanford example). We have a two-to-four manuscript comparison workspace
  (`internal/serve/comparison.go`, Mirador mosaic), but only over **locally
  preserved** bundles chosen from our catalogue — not arbitrary remote
  drag-and-drop. Preserve both, then compare.
- 🔴 **Compare a IIIF document with a loose local non-IIIF image.** No path
  to drop an arbitrary image beside a manifest; everything is per-canvas,
  preserved from a manifest.
- 🟡 **Mixed manifest** (non-IIIF colorized image + IIIF document as a canvas
  sequence). We can *consume* one via `-manifest-file`, and the largest-image
  fetch has a bare-URL fallback for non-Image-API canvases, but we do not
  **author** mixed manifests.
- 🔴 **Image layering / alignment** (Compariscope, LayerStack, Leaflet-iiif).
  Our comparison is side-by-side windows, not opacity-blended layer stacks,
  and we do not handle alignment data.
- 🔴 **Multispectral IR/UV** (Quinatzin Mapa) — both viewer and data side
  *(verified)*. No layering or band-switch UI, and alternate image bytes are
  **not preserved**: the linked-resource walker descends into
  `Choice`/`SpecificResource` bodies but explicitly skips anything typed or
  formatted as an image (`collectContentReferences`,
  `internal/preserve/linked.go` — painting images are deferred to the image
  pipeline), while `EnumerateImages` (`internal/preserve/enumerate.go`)
  decodes a v3 painting body as one flat image resource, so a `Choice` body
  yields no usable service id and its alternatives are silently dropped.
  Only *non-image* Choice bodies (e.g. a choice of text translations, per
  `linked_test.go`) are preserved. DESIGN §4.4's "including
  Choice/SpecificResource alternatives" is true only for non-image bodies.

### Deep zoom with large images

- ✅ **Deep zoom of large IIIF images.** Core feature: local IIIF level0 tile
  pyramids (`internal/preserve/tile.go`) plus Mirador deep zoom, verified
  in-browser. Caveat: altomator's example *stitches a 38k×21k mosaic of 20k
  faces*; that image-production step is out of scope — we deep-zoom images
  that already exist.

### IIIF collections

- ✅ **Consume / preserve nested collections** (periodicals by year, thematic
  file plans). Recursive v2 `members` + v3 `items` crawl, cycle-safe
  (`internal/source/collection.go`).
- 🟡 **Reproduce the browsable collection tree offline.** We flatten
  preserved manifests into a searchable, sortable **flat catalogue**, not a
  re-emitted nested Collection (e.g. Vogue-by-year).
- 🔴 **Author / generate collection JSON** (their Perl scripts). We consume
  collections; we do not write them.

### IIIF annotations

- ✅ **Display existing IIIF annotations offline** (`otherContent`,
  GallicaPix server export, Mandragore). We preserve `otherContent`
  AnnotationLists, `annotations` AnnotationPages, paginated `supplementary`
  collections, and external bodies, rewrite them to local, and serve offline
  (DESIGN §4.4). This is **stronger than altomator's flow**, which needs a
  live GallicaPix endpoint; ours works with the network down.
- ✅ **Manual in-canvas annotation authoring.** Embedded MAE editor plus an
  offline W3C annotation store (`internal/annotation`). altomator has no
  first-party authoring — they import JSON.
- 🔴 **AI / computer-vision annotation generation** (Roboflow object
  detection). We store and preserve annotations; we do not run models to
  produce them.

### Georeference and maps

- 🔴 **Georeferencing** (AllMaps editor, GCPs derived from catalogue field
  042). No georeference-extension handling, no GCP derivation, no maps
  viewer. If a georeference AnnotationPage is linked from a manifest we may
  preserve its bytes, but we cannot act on it.

### IIIF ranges

- ✅ **Table of contents / ranges** (their Victor Hugo example). Passive:
  manifest bytes are preserved verbatim, so ranges pass through and Mirador
  renders the ToC with no special handling.

### IIIF and OCR

- ✅ **Preserve ALTO / HTR resources** linked via `seeAlso` or `otherContent`.
  We fetch and localize ALTO/TEI/XML/text `seeAlso` resources (DESIGN §4.4).
- ✅ **Display OCR as a text overlay** (the `mirador-textoverlay` plugin) —
  **implemented and browser-verified** (2026-07-12). The viewer build
  vendors textoverlay 1.0.4 (native Mirador 4), enabled in both viewer
  templates, with a local patch for an upstream crash on W3C (v3)
  annotation pages that MAE emits — unpatched, the overlay spinner never
  resolves. Verified rendering on a preserved OCR-linked Gallica bundle.
  Note: Gallica's own manifests carry no per-canvas ALTO `seeAlso`
  (verified live), so the overlay triggers only on hand-linked manifests
  or institutions publishing ALTO links natively. Details in
  [DELTA1.md](DELTA1.md).
- 🔴 **ALTO → IIIF-annotation conversion** (XSLT). No conversion step.

### IIIF and A/V

- 🔴 **Video / audio manifests** *(verified, corrected)*. The pipeline is
  images-only, but the failure mode is louder than first written: a v3 A/V
  painting body **is** enumerated as an image candidate
  (`EnumerateImages` has no type/motivation check), the fetch then requires
  a decodable JPEG (`jpeg.DecodeConfig`, `internal/preserve/fetch.go`), so
  every A/V page fails and `Preserve` returns `ErrIncomplete` — an A/V
  manifest **fails to preserve outright** rather than saving a silent
  shell. No provenance is committed, so nothing surfaces in the catalogue.

### Curation and storytelling

- ⚪ **IIIF Curation** (CODH viewer). altomator's own entry is `tbc`; our
  comparison workspaces are curation-*like* but not the IIIF Curation API.
- 🔴 **Digital storytelling** (Storiiies, Exhibit, CanvasPanel). No narrative
  authoring.
- ⚪ **Almanac browser extension.** Out of scope entirely.

## Why the reds are red

These are root causes, not incidental omissions.

1. **Preservation-first, images-only canvas model.** The pipeline enumerates
   *image* services per canvas and stores per-canvas JPEGs plus tiles. A/V —
   and, to a degree, image layering and multispectral band-switching — fall
   outside that model by design.
2. **Consume, don't author.** Collections, mixed manifests, georeference
   annotations, and stories are all *authoring* outputs. We are a downloader
   and server, so anything whose deliverable is "produce new IIIF JSON" is
   out of scope.
3. **Single embedded viewer, no plugin ecosystem.** altomator leans on a zoo
   of hosted viewers (AllMaps, LayerStack, Storiiies, textoverlay-Mirador,
   standalone OpenSeadragon). We ship exactly one custom Mirador 4 + MAE
   build, offline. That deliberately buys offline resilience at the cost of
   specialty viewers.
4. **No ML / CV.** Roboflow-style annotation generation is a whole capability
   class we do not have and should not grow into lightly.

## Recommendations

Ranked by fit to the offline-preservation mission.

### Worth doing — directly on-mission

1. **Preserve A/V bodies.** Extend canvas enumeration to recognize
   `video`/`audio` painting bodies and download them alongside JPEGs.
   This is the single biggest content gap — a Gallica A/V manifest fails
   preservation outright today (`ErrIncomplete`; see the A/V entry above).
   Mirador can play A/V manifests (verify against our embedded Mirador 4
   build before claiming done).
2. **Reproduce the collection tree offline.** We already crawl nested
   collections; emit a local browsable Collection (or at least group the
   catalogue by the source hierarchy) so a preserved Vogue-by-year set
   browses like the original rather than as a flat list.
3. **Preserve Choice image alternatives.** Contrary to the first draft of
   this analysis, this is a *code change*, not a verification task
   *(verified)*: `EnumerateImages` must learn to descend into
   `Choice`/`oa:Choice` painting bodies and emit one `ImageResource` per
   alternative, with provenance and serve-time rewrite following. Once the
   alternates are preserved locally, verify whether Mirador's native Choice
   switcher renders them offline — if it does, the multispectral example
   closes with no layering code of our own.

### Worth considering — high user value

4. **Bundle the `mirador-textoverlay` plugin into our viewer build.**
   *Status: implemented and browser-verified (2026-07-12).* Upstream
   released native Mirador 4 / React 19 / MUI 7 versions (1.0.0–1.0.4,
   May–June 2026) matching our viewer stack exactly — no port was
   needed. The plugin is vendored (pinned 1.0.4), wired, enabled in both
   viewer templates test-first, locally patched for an upstream crash
   triggered by MAE's W3C annotation pages, and verified rendering
   preserved ALTO in-browser. The patch, the remaining checks
   (network-down formality, MAE-drawing smoke), and a live finding about
   Gallica manifests lacking per-canvas ALTO `seeAlso` are recorded in
   [DELTA1.md](DELTA1.md).
5. **Free-text-embedded date parsing** (already deferred in DESIGN §7). Not
   an altomator feature, but it is the recall gap that most limits how well
   we can *select* the kind of thematic Gallica corpora altomator curates.

### Explicitly decline — document as non-goals

6. Georeferencing / AllMaps, storytelling (Storiiies / Exhibit), image-layering
   viewers, ALTO→annotation XSLT conversion, and CV / Roboflow annotation
   generation. These are authoring and specialty-viewer capabilities
   orthogonal to offline preservation; adopting any pulls the project toward
   being "another IIIF showcase" and away from the one-binary-preservation
   thesis. Better to interoperate: preserve the georeference AnnotationPage,
   OCR, or story-manifest **bytes** so the *external* tool still works, rather
   than reimplement it.

## Net assessment

For the overlap that matters to a preservation researcher — deep zoom,
collection ingestion, annotation display *and* authoring, OCR-resource
preservation, and side-by-side comparison — we already meet or exceed
altomator's demonstrations, and we do it **offline**, which their
server-and-hosted-viewer approach cannot. The reds are overwhelmingly
authoring and specialty-viewer features that are deliberately outside our
scope; only **A/V preservation** is a genuine mission-relevant hole worth
closing soon.
