# Local IIIF Preservation Tool — Design

> Status: **acquire → select → preserve → serve → view → deep-zoom is built
> and live-dogfooded.** One binary crawls a real institution politely (or
> takes a single `-manifest <url>`), filters, writes a complete on-disk copy
> (images + **local IIIF level0 tile pyramids** + manifest + provenance) into
> a persistent institution-nested library, serves it over HTTPS with the
> manifest **rewritten on the fly** to point at local images *and* a local
> Image API service, and embeds a **Mirador 4** viewer so a researcher needs
> no external tools. Deep zoom works from local static tiles.
>
> **Status detail:** project status lives in this document (§8), not in
> assistant memory. One caveat: rewrite is serve-time, so a dumb static host
> (S3) would serve the un-rewritten manifest — acceptable given local-first
> scope. Known limitation: bundles preserved *before* tiling existed are not
> re-tiled on an idempotent re-run. Working name TBD (binary provisionally
> `iiifpreserve`). Single Go binary (one non-stdlib dep:
> `golang.org/x/image`). See §8 for component status.

## 1. Goal

Let a researcher on a local machine build and serve an offline, viewer-ready
copy of *subsets* of IIIF collections from chosen institutions — resilient to
network outages and institutional incidents (e.g. the British Library 2023
cyber-attack) — with polite trawling and faceted selection such as
"French manuscripts, 15th century".

This replaces the loosely-coupled four-tool Lego stack from the source article
(`iiif-download` Python CLI → `iiif-tiler-rust` → `http-server` Node →
Triiiceratops) with one compiled binary. The fragile seam in that stack — URI
base and directory layout matching by convention across three runtimes — is
eliminated by folding download, tiling, and serving into a single process.

Reference article: https://digitalorientalist.com/2026/05/12/running-iiif-locally-a-simple-setup-guide/

## 2. Confirmed decisions

| Area | Decision | Rationale |
|---|---|---|
| Language | **Go**, single static binary | One toolchain, easy embed of viewer asset, no Python/Node |
| Viewer | **Built.** Vendored prebuilt **Mirador 4** UMD bundle (`mirador@4.0.0`), `go:embed`-ed | "No Node" = no Node runtime or npm build; a browser-run static asset is allowed. Mirador chosen for the multi-up/annotation feature set |
| Discovery | **Per-institution crawl**, no aggregator | No dependency on Europeana/Biblissima; full control |
| Source adapters | `collection` (universal IIIF Collection tree) + `changestream` (IIIF Change Discovery API, resumable, preferred when available) behind one `Source` interface | Collection tree is the guaranteed path; change streams enable cheap refresh |
| Subsetting | Local **metadata normalization** → typed `WorkRecord` → predicate filter, applied **before** image download | No global IIIF search exists; metadata is free-text/multilingual/per-institution |
| Filter policy | **Two outcomes** `match` / `no-match`. A specified criterion only excludes when the metadata confidently fails it; when the field is absent the item is **kept** (lenient) | Preservation tool: losing a possibly-wanted manuscript is worse than an extra download. No reject/approve workflow |
| Storage | **`BlobStore` interface**, `local` first; persistent root via `-store` > config file (`store=`) > `~/iiif-images` default; **nested by institution** `<root>/<host>/<slug>/` | Researcher's local drive as a long-lived library; interface keeps other backends possible later (no `aws-sdk-go`). Config is a tiny stdlib `key=value` parser (no YAML/TOML dep) |
| Acquire modes | `-collection <url>` (crawl) **or** `-manifest <url>` (single resource, skips the filter, uses the polite Go fetcher) | A single named manifest is an intentional choice; `-manifest` also replaces curl for fixtures/dogfooding. `-dry-run` = classify only |
| Per-item preservation | Per-canvas JPEG via the IIIF Image API at the **largest available size** (`/full/max` → `/full/full` → bare URL), plus the manifest and a provenance log | Grounded in the reference tool (`iiif-download`); a research preservation copy, not a commercial mirror |
| Serving | **Built.** Static HTTPS file server over the BlobStore tree; `*/manifest.json` rewritten on the fly (serve-time, provenance-driven) so images resolve locally. Stored manifest stays pristine | Simplest correct; no config, no second file; loopback-only |
| Embedded viewer | **Built.** Mirador 4 served at `/` (index of preserved manifests) and `/<dir>/` (viewer), bundle at `/__viewer__/mirador.min.js` | A researcher needs no external viewer; manifest passed via a data-attribute to dodge html/template JS-string `\/` escaping |
| Tiling + deep zoom | **Built.** Preserve generates a local IIIF Image API **level0** static tile pyramid (`golang.org/x/image/draw`, 512px tiles) per image; serve rewrites `info.json` `id` and re-points the manifest service | Deep zoom from local static files; matches institutional viewer functionality. Best-effort: an undecodable image keeps the flat JPEG and the serve-time strip fallback |
| Pilot institutions | **Gallica (BnF)** and **Digital Bodleian** | Large French collection + well-structured metadata to stress-test the normalizer |

## 3. Pipeline

```
config(institutions)
  → Source adapter (collection | changestream)
  → manifest fetch (conditional GET via ETag/If-Modified-Since, checkpointed)
  → metadata normalize  → filter (match | no-match; lenient on missing data)
  → [match] enumerate canvas image services (v2 + v3)
  → polite image fetch: largest JPEG via the IIIF Image API
    (/full/max → /full/full → bare URL; per-host rate, backoff, dedup)
  → per image: render a local IIIF level0 static tile pyramid + info.json
  → BlobStore.Put (local, institution-nested): per-manifest dir of JPEGs +
    per-image tile pyramids + manifest.json + provenance (source URLs,
    recorded license, per-image tile_dir)
  [preservation ends here — content is fully saved and deep-zoomable]

serve (built): static HTTPS server over BlobStore; */manifest.json
  rewritten at serve time so images resolve locally and re-point at the
  local Image API service; */info.json `id` rewritten to the request URL;
  embedded Mirador 4 viewer at / and /<dir>/ (stored files stay pristine).
```

## 4. Key components

### 4.1 Source interface
- `collection` adapter: walk nested IIIF Collection → sub-Collections → manifests.
  Universal; build first.
- `changestream` adapter: consume IIIF Change Discovery API (ordered, paged
  Activity Streams: create/update/delete). Resumable; preferred where published.
  Availability per institution unconfirmed — must verify for Gallica/Bodleian.
- Per-institution config: base URL, adapter type, polite rate, scope path.
- Emits a uniform stream of manifest URLs regardless of adapter.

### 4.2 Metadata normalization + filter — highest risk
Presentation `metadata` is free-text, multilingual, per-institution inconsistent:
- Language keys: `Language` / `Langue` / `Sprache`; values `French` / `français`
  / `fre` / `fra`.
- Date forms: `1450`, `15th century`, `XVe siècle`, `circa 1480`, `s. XV`,
  ranges, or only embedded in free-text description.
- Place-of-origin vs. language vs. holding institution routinely conflated.

Approach: per-institution field-mapping rules + value parsers
(century/date-range parser, language→ISO-639 normalizer) → typed
`WorkRecord{langs, dateRange, origin, ...}`. Filters are clean predicates over
typed records. Classify `match` / `no-match`; the filter runs before any
image download.

> **Policy note — lenient on missing data.** A specified criterion
> (`-lang`, `-from/-to`, `-place`) excludes an item only when the parsed
> metadata *confidently* fails it. When the relevant field is absent (e.g.
> a Digital Bodleian item with a clean date but no language under the
> default field mapping), the item is **kept**, not excluded — losing a
> possibly-wanted manuscript is worse than an extra download in a
> preservation tool. Field mapping is still genuinely per-institution and
> the default mapping is only a starting point; improving recall is a
> mapping/parser problem, not a reason for a reject/approve workflow.

### 4.3 Polite trawler
Per-host token-bucket rate limit, global concurrency cap, exponential backoff on
429/503, conditional GET so re-runs are cheap, content-hash dedup, resumable
checkpoint/journal so an interrupted large crawl restarts where it stopped.
Image size requests the **largest available**: `/full/max/0/default.jpg`, then
`/full/full/0/default.jpg`, then the bare resource URL (institutions serving
static images, no Image API). License, if present, is **recorded for
provenance**, not used to gate downloads — this is a research tool, not a
commercial mirror.

### 4.4 Storage
`BlobStore` interface, `local` implementation first (the interface keeps other
backends possible; `aws-sdk-go` is explicitly out). Per matched manifest: a
directory of per-canvas JPEGs + a local IIIF level0 tile pyramid per image
(`<NNNN>/info.json` + `<NNNN>/<region>/<size>/0/default.jpg`) + the saved
`manifest.json` + a provenance log (manifest URL, recorded license,
per-image source URLs and `tile_dir`). The root is institution-nested
(`<root>/<host>/<slug>/`). Writes are atomic (temp+rename); re-runs skip
already-stored images. Tiling is best-effort: an undecodable image keeps
just the flat JPEG.

The saved `manifest.json` is stored **unmodified** (fidelity/provenance);
rewriting happens at serve time (§4.5), not on disk.

### 4.5 Serving (built)
A static HTTPS file server over the BlobStore tree (stdlib only;
operator-supplied cert via mkcert, mirroring signatory; `-no-tls` debug
escape; loopback-only; graceful shutdown). A request for `*/manifest.json`
is **rewritten on the fly**: `rewriteManifest` reads the sibling
`provenance.json`, and for every preserved image points its resource id at
`<server>/<dir>/NNNN.jpg` and sets `format: image/jpeg`. If a tile pyramid
was built (`tile_dir`), it **re-points** the IIIF Image API `service` at the
local `<server>/<dir>/NNNN` (`ImageService3`, `level0`) so the viewer deep-
zooms from local static tiles; otherwise it strips the `service` (a service-
less image is a valid static IIIF image). A request for `*/info.json` has
its `id` rewritten to the request URL (IIIF requires `id` == the served
URL). Provenance-driven and structure-agnostic, so v2 and v3 work with no
traversal-order concerns; stored files stay pristine; a missing/failed
rewrite falls back to the untouched file (serving must not break).

> **Real-data note.** Manifests reference an image server in several roles.
> Only *preserved content* is localized: the content image and per-canvas
> thumbnails (same preserved service) are rewritten; the manifest `logo`
> points at a different, un-preserved service and correctly stays remote
> (it is institutional chrome, not the work). A dead logo link offline is
> acceptable; rewriting it would be wrong.

## 5. Known risks / validate early

1. **Metadata normalization is make-or-break.** Gallica vs. Bodleian
   label/date conventions differ; century/date-range parser and language
   normalizer need real-data testing first. Prime TDD target — date-string
   parsing is naturally test-first.
2. **Change Discovery availability** — RESOLVED for Bodleian: it publishes
   an Activity Streams `OrderedCollection` (~21,731 items), verified live.
   Gallica: still unverified. `collection` adapter remains the guaranteed
   path; both adapters are built.
3. **Image API quirks.** Not every institution implements the Image API;
   some serve only static images. Need the bare-URL fallback and
   content-type checking (per the reference tool).

## 6. Suggested first build slice

`collection` Source adapter + metadata normalizer + filter, validated against
live Gallica and Bodleian data, **test-first on the date/language parsers** —
*before* any download/tile/serve code, since the filter gates everything and
carries the most risk.

## 7. Open / deferred

- **Done since last revision** (were roadmap): embedded Mirador 4 viewer;
  local IIIF level0 tile pyramids + deep zoom; tolerant *version-agnostic*
  metadata extraction (object-valued v2/v3 labels no longer drop manifests);
  `-store`/config/`-manifest`/`-dry-run`; institution-nested library.
- **Re-tile existing bundles:** a bundle preserved *before* tiling is not
  re-tiled on idempotent re-run (skip branch only checks the flat jpg).
- **In-browser deep zoom: confirmed working** (Mirador zoom-in verified
  visually against a served, tiled bundle). Everything up to the served
  tiles/info.json is also automated-test covered.
- Free-text-embedded dates: deferred parser gap (needs false-positive-rate
  decision) that still costs some filter recall.
- Preservation dogfooded end-to-end against **Bodleian**, the **IIIF
  Cookbook** (v3), and **Gallica/BnF** (single-image estampe
  `btv1b9055204k`: ~39s honouring the 13s/host throttle → tiled, served,
  manifest re-pointed, info.json localized; deep zoom confirmed in-browser).
  **Whole multi-page Gallica manuscript validated end-to-end**: a 25-page
  manuscript (`btv1b53140000q`) preserved under the 13s throttle (25/25,
  0 failed, per-page progress) → 25 local tile pyramids → served with all
  25 images re-pointed to local level0, all thumbnails dropped, per-page
  info.json localized, deep tiles served; re-run resumed in 0.56s (25
  skipped, no HTTP).
- **Thumbnails:** a manifest/canvas `thumbnail` pointing at a *different*
  service than the preserved image (e.g. Gallica's `…ark.thumbnail`)
  cannot be matched by provenance. `rewriteManifest` now **drops any
  non-local `thumbnail`** (structure-agnostic) so nothing broken is
  requested offline; the viewer derives a thumbnail from the now-local
  level0 image service. A thumbnail already under the local base is kept.
- **Whole-manuscript downloads** are practical via `-manifest`:
  (a) the working size-variant is memoized per manifest, so a dead
  `/full/max` is probed once, not on every page (~2× faster on Gallica);
  (b) per-image progress goes to stderr (`[42/300] 0042.jpg stored`);
  (c) runs are resumable — `Preserve` skips already-stored images with no
  HTTP, so an interrupted multi-hour run continues politely on re-run
  (re-run of the estampe: 1.1s vs 38.7s, skipped, zero network). It is
  still inherently long (BnF documents no rate limit; the conservative
  13s is deliberate). No per-manifest image cap yet — a `-max-images N`
  is a separate *triage/sampling* feature (grab first N pages to
  evaluate), not the whole-manuscript path.
- Working name for the project/binary; module path
  (`github.com/sarahmaeve/go-iiif` vs. a short bare path) — raised, undecided.
- Confirm Change Discovery availability for Gallica (Bodleian: confirmed).

## 8. Implementation status

Built test-first (Red/Green). Default `go test ./...` is fully offline; live
checks are `-tags=integration` opt-in or the manual binary.

| Component | Status | Where |
|---|---|---|
| Date parser (year, ranges, Arabic/Roman centuries, `circa` fuzzy ±20y) | ✅ done | `internal/metadata` |
| Language → ISO-639 normalizer (8 langs: name/endonym/639-2) | ✅ done | `internal/metadata` |
| `WorkRecord` builder + per-institution `FieldMapping` | ✅ done | `internal/metadata` |
| Two-outcome `match`/`no-match` filter (lang/date/origin; lenient on missing data) | ✅ done | `internal/metadata` |
| Tolerant **version-agnostic** metadata extraction (`ExtractMetadata` + `normalizeIIIFText`: plain/v2-localized/v3 language-map; English-preferring) | ✅ done | `internal/metadata` |
| `collection` Source adapter (recursive, cycle-safe) | ✅ done | `internal/source` |
| HTTPS `Fetcher` (HTTPS-only, browser UA, status mapping) | ✅ done | `internal/source` |
| Polite trawler: per-host rate limit, concurrency cap, 429/503 backoff, conditional GET, content-hash dedup, resumable journal | ✅ done | `internal/source` |
| End-to-end classification pipeline | ✅ done | `internal/pipeline` |
| Concurrent pipeline fan-out (opt-in `Workers`; per-host politeness preserved; live multi-host verified) | ✅ done | `internal/pipeline` |
| CLI entrypoint | ✅ done (provisional name) | `cmd/iiifpreserve` |
| Live validation vs. Gallica + Bodleian (manifests, recursive walk, full run, concurrent multi-host) | ✅ done | `*_test.go` `//go:build integration` |
| `changestream` Source adapter (IIIF Change Discovery) | ✅ done | `internal/source` |
| IIIF **v3** collections (`items`) + v2 mixed `members` | ✅ done | `internal/source` |
| Per-host `RatePolicy` (built-in Gallica 13s throttle) | ✅ done | `internal/source` |
| Canvas image enumeration (v2 + v3) | ✅ done | `internal/preserve` |
| Largest-image fetch (`/full/max`→`/full/full`→bare); **working variant memoized per manifest** (skip dead probe after page 1, ~2× on Gallica) | ✅ done | `internal/preserve` |
| `Preserve`: store JPEGs + manifest + provenance; idempotent (resumable); per-image fault-tolerant; `WithProgress` per-image events | ✅ done | `internal/preserve` |
| CLI `-manifest` per-image progress to stderr (`[N/total] file action`) | ✅ done | `cmd/iiifpreserve` |
| `BlobStore` (`local`, atomic writes) | ✅ done | `internal/preserve` |
| `pipeline.Result` carries manifest bytes; storage root `-store` > config (`store=`) > `~/iiif-images`; `-dry-run` classify-only | ✅ done | `cmd/iiifpreserve` |
| Single-resource downloader `-manifest <url>` (polite Go fetcher, skips filter; replaces curl) | ✅ done | `cmd/iiifpreserve` |
| Institution-nested library layout `<root>/<host>/<slug>/` (`dirFor`) | ✅ done | `internal/preserve` |
| Non-string v2 / object-valued v3 metadata values | ✅ done (`normalizeIIIFText`) | `internal/metadata` |
| Free-text-embedded dates | ⬜ deferred (needs false-positive-rate decision) | DESIGN §4.2 |
| Static HTTPS server over `BlobStore` (mkcert cert, `-no-tls`, graceful) | ✅ done | `internal/serve` |
| Serve-time manifest rewrite (provenance-driven, v2+v3; re-points to local Image API service when tiled, else strips; drops non-local thumbnails) | ✅ done | `internal/serve` |
| Embedded **Mirador 4** viewer (`go:embed` UMD; index + `/<dir>/` + bundle route) | ✅ done | `internal/serve` |
| **Tiling + deep zoom**: local IIIF level0 static pyramids (`tile.go`: `tilePlan`/`infoJSON`/`renderTilePyramid`, `x/image/draw`) | ✅ done | `internal/preserve` |
| Serve-time `info.json` `id` rewrite to the request URL | ✅ done | `internal/serve` |
| Live dogfood: `-manifest` Cookbook v3 + real Bodleian + Gallica/BnF estampe + **25-page Gallica manuscript** → tiled preserve → serve → localized + re-pointed + deep tile; resume verified (re-run 0.56s, all skipped) | ✅ done | `internal/{preserve,serve}` `//go:build integration` + manual binary |
| Re-tile bundles preserved before tiling existed | ⬜ deferred (idempotent skip only checks flat jpg) | DESIGN §7 |
The binary runs the full `acquire → select → preserve → serve → view →
deep-zoom` path live (one binary, real institution or single `-manifest`:
filtered, polite, institution-nested on-disk copy with provenance and local
IIIF tile pyramids, served over HTTPS with the manifest re-pointed at local
images + a local Image API service, viewed in an embedded Mirador 4). The
remaining high-value gap is recall: the free-text-date parser (§4.2) and
per-institution field mapping, plus re-tiling pre-tiling bundles. Project
status is tracked here, not in assistant memory.
