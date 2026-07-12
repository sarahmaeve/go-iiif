# Local IIIF Preservation Tool — Design

> Status: **acquire → select → preserve → serve → view → deep-zoom is built
> and live-dogfooded.** One binary crawls a real institution politely (or
> takes a single `-manifest <url>` or pristine local `-manifest-file <path>`), filters, writes a complete on-disk copy
> (images + **local IIIF level0 tile pyramids** + manifest + provenance) into
> a persistent institution-nested library, serves it over HTTPS with the
> manifest **rewritten on the fly** to point at local images *and* a local
> Image API service, and embeds a **Mirador 4** viewer so a researcher needs
> no external tools. Deep zoom works from local static tiles.
>
> **Status detail:** project status lives in this document (§8), not in
> assistant memory. One caveat: rewrite is serve-time, so a dumb static host
> (S3) would serve the un-rewritten manifest — acceptable given local-first
> scope. Working name TBD (binary provisionally
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
| Viewer | **Built.** Vendored **Mirador 4** UMD, `go:embed`-ed — a *custom* build from a local Mirador 4 **source checkout** (`viewer-src/` + `MIRADOR_SRC`) with the MAE annotation editor folded in. Not the `mirador@4.0.0` npm tag: MAE's annotation-creation companion window needs the render-time `CompanionWindowRegistry` lookup that landed in Mirador 4 *after* the 4.0.0 release (MAE's README requires the latest Mirador 4) | "No Node" = no Node runtime in the **binary**; a one-time `make viewer` vendoring build (Node) producing a single browser-run static asset is allowed. Mirador chosen for the multi-up/annotation feature set |
| Discovery | **Per-institution crawl**, no aggregator | No dependency on Europeana/Biblissima; full control |
| Source adapters | `collection` (universal IIIF Collection tree) + `changestream` (IIIF Change Discovery API, resumable, preferred when available) behind one `Source` interface | Collection tree is the guaranteed path; change streams enable cheap refresh |
| Subsetting | Local **metadata normalization** → typed `WorkRecord` → predicate filter, applied **before** image download | No global IIIF search exists; metadata is free-text/multilingual/per-institution |
| Filter policy | **Two outcomes** `match` / `no-match`. A specified criterion only excludes when the metadata confidently fails it; when the field is absent the item is **kept** (lenient) | Preservation tool: losing a possibly-wanted manuscript is worse than an extra download. No reject/approve workflow |
| Storage | **`BlobStore` interface**, `local` first; persistent root via `-store` > config file (`store=`) > `~/iiif-images` default; **nested by institution** `<root>/<host>/<slug>/` | Researcher's local drive as a long-lived library; interface keeps other backends possible later (no `aws-sdk-go`). Config is a tiny stdlib `key=value` parser (no YAML/TOML dep) |
| Acquire modes | `-collection <url>` (crawl), `-manifest <url>` (single remote resource), or `-manifest-file <path>` (single already-downloaded manifest); both single-resource modes skip the filter | A single named manifest is an intentional choice. The file mode preserves its input bytes exactly, takes the manifest's top-level `id`/`@id` as bundle identity, and avoids a manifest request when an institution permits browser download but challenges programmatic access. `-dry-run` = classify/count only |
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
Per-host token-bucket rate limit (default 750 ms base spacing + a random
pad, a uniform multiple of 30 ms in [30, 600] ms, so timing isn't perfectly
periodic; Gallica keeps a deliberate fixed 13 s, no jitter), global
concurrency cap, exponential backoff on
429/503, and an automatic query-scoped completion ledger under
`.iiifpreserve/ingest/`. Its stable fingerprint includes collection URL,
language/date/place filters, filter semantics version, and the canonical
institution field mappings, plus a preservation-semantics version. Durable preserves and no-match decisions are
recorded; failures remain pending, with their latest error and attempt count
stored for recovery reporting, and dry runs do not use crawl completion state. Reopening repairs an interrupted
journal tail. Collection discovery itself uses an atomic frontier containing
pending/visited collection URLs and discovered manifests; each fetched
collection is committed before its manifests are yielded, so restart repeats
at most the current collection request. A completed frontier makes no remote
discovery requests until explicit `-fresh`, which resets only ingest state.
The normal CLI fetcher persists ETag/Last-Modified validators, content type,
and bounded JSON response bodies under `.iiifpreserve/http-cache/`; page-image
bodies are excluded because committed JPEGs are their durable cache. A 304
after restart is satisfied from the atomic cached record.
Library of Congress Presentation manifest routes are a narrow exception:
`www.loc.gov/item/<id>/manifest.json` currently receives a Cloudflare 403,
while LOC's documented item JSON API and `tile.loc.gov` IIIF Image API remain
available to identified clients. The fetcher first tries the requested
manifest normally; on a 403 for only that exact route it politely pulls
`/item/<id>/?fo=json` and derives an ordered Presentation 3 manifest from the
item's page-file groups and `info.json` links. This is intentionally two-step,
so the pristine upstream manifest wins automatically if the challenge is
removed. The derived manifest links its source item JSON with `seeAlso`. A
researcher who manually downloads the original should instead use
`-manifest-file` to retain exact manifest bytes.
**Bot-wall stance:** present an honest identifying User-Agent (a one-time
public-domain preservation fetch is not abusive) rather than spoofing a
browser — bot-walls like Anubis (which Bodleian uses) add suspicion weight
to "Mozilla"/"Opera" UAs and then issue a JS proof-of-work an HTTP client
cannot solve, but score an honest UA as benign and allow it. Browser-spoof
is the per-host *exception* (Gallica 403s honest UAs). Defense-in-depth: a
2xx HTML response (Anubis answers challenges with 200) is rejected, never
archived in place of a manifest/image.
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
valid already-stored images without HTTP and repair a missing pyramid from the
local JPEG. Tiling is best-effort. `provenance.json` is written last only when
all required page images succeed, making it the atomic bundle completion
marker used by the catalogue.

The acquired `manifest.json` bytes are stored **unmodified**
(fidelity/provenance); rewriting happens at serve time (§4.5), not on disk.
For `-manifest-file` and ordinary remote manifests those are the original
upstream bytes. The documented LOC fallback is the explicit exception: its
acquired bytes are a derived Presentation 3 manifest linked to the official
source item JSON with `seeAlso`.

### 4.5 Serving (built)
A static HTTPS file server over the BlobStore tree (stdlib only;
operator-supplied cert via mkcert, mirroring signatory; `-tls-cert`/
`-tls-key` default to the mkcert-convention path
`~/.config/iiifpreserve/certs/127.0.0.1+1{,-key}.pem` so `-serve` needs
no TLS flags after a one-time `mkcert -install` + generate; a missing
cert prints the exact recipe; `-no-tls` debug escape; loopback-only;
graceful shutdown). A request for `*/manifest.json`
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
- **In-browser deep zoom: confirmed working** (Mirador zoom-in verified
  visually against a served, tiled bundle). Everything up to the served
  tiles/info.json is also automated-test covered.
- Free-text-embedded dates: deferred parser gap (needs false-positive-rate
  decision) that still costs some filter recall.
- **Per-institution config — RESOLVED/consolidated.** Rate, User-Agent,
  and field mapping were three parallel mechanisms (`source.RatePolicy`,
  `source.builtinHostUserAgents`, cmd's `defaultMapping`). They now live in
  one host-keyed `institution.Profile`/`Registry` (`internal/institution`,
  the single source of truth; `source` derives its limiter/UA from it, the
  pipeline resolves the mapping per *manifest* host). The e-codices label
  vocabulary (`Text Language`, `Date of Origin (English)`, `Place of Origin
  (English)`, `Century`) is in the shared default mapping, so filtered
  e-codices crawls now constrain correctly. Adding a source is one place.
- **Library of Congress — RESOLVED for single items without evasion.** Its
  Presentation manifest route is currently behind a Cloudflare managed
  challenge, but its documented item JSON API and `tile.loc.gov` Image API
  accept the honest client. `-manifest` now uses the narrow polite two-step
  fallback described in §4.3; the supplied 123-image Greek manuscript
  `0027938281A-ms` was enumerated live. `-manifest-file` is the byte-faithful
  route when the original manifest has been downloaded manually. A complete
  123-page preservation/serve/deep-zoom run has not yet been dogfooded.
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
| `WorkRecord` builder + `FieldMapping` (mapping data lives in `internal/institution`) | ✅ done | `internal/metadata` |
| **Consolidated per-institution `Profile`/`Registry`** (rate + UA + field mapping, host-keyed; single source of truth — `source` derives limiter/UA, pipeline resolves mapping per manifest host) | ✅ done | `internal/institution` |
| Two-outcome `match`/`no-match` filter (lang/date/origin; lenient on missing data) | ✅ done | `internal/metadata` |
| Tolerant **version-agnostic** metadata extraction (`ExtractMetadata` + `normalizeIIIFText`: plain/v2-localized/v3 language-map; English-preferring) | ✅ done | `internal/metadata` |
| `collection` Source adapter (recursive, cycle-safe) | ✅ done | `internal/source` |
| HTTPS `Fetcher` (HTTPS-only enforced, std TLS verify; **honest identifying UA** by default with per-host browser-spoof override only where forced e.g. Gallica; honest `Accept`; status mapping; **rejects HTML interstitials** so a bot-wall/error page is never archived; narrow LOC manifest-403 → official item-JSON derived-manifest fallback) | ✅ done | `internal/source` |
| Polite trawler: per-host rate limit, concurrency cap, 429/503 backoff; automatic query-aware completion ledger and failure report; atomic pending/visited/discovered collection frontier; completed reruns make zero discovery requests; `-fresh` safely starts a new scan; durable bounded JSON conditional-GET cache with ETag/Last-Modified (page images excluded); SIGINT/SIGTERM recovery, `-ingest-status`, and explicit `-page-retries` policy | ✅ done | `internal/source`, `cmd/iiifpreserve` |
| End-to-end classification pipeline | ✅ done | `internal/pipeline` |
| Concurrent pipeline fan-out (opt-in `Workers`; per-host politeness preserved; live multi-host verified) | ✅ done | `internal/pipeline` |
| CLI entrypoint | ✅ done (provisional name) | `cmd/iiifpreserve` |
| Live validation vs. Gallica + Bodleian (manifests, recursive walk, full run, concurrent multi-host) | ✅ done | `*_test.go` `//go:build integration` |
| `changestream` Source adapter (IIIF Change Discovery) | ✅ done | `internal/source` |
| IIIF **v3** collections (`items`) + v2 mixed `members` | ✅ done | `internal/source` |
| Per-host `RatePolicy` (default 750ms + 30–600ms jitter; built-in Gallica fixed 13s, no jitter) | ✅ done | `internal/source` |
| Canvas image enumeration (v2 + v3) | ✅ done | `internal/preserve` |
| Largest-image fetch (`/full/max`→`/full/full`→bare); **working variant memoized per manifest** (skip dead probe after page 1, ~2× on Gallica) | ✅ done | `internal/preserve` |
| `Preserve`: store validated JPEGs + manifest + completion-safe provenance; idempotent restart reuses local pages without HTTP, repairs uncommitted pyramids locally, preserves successful pages across failures, and returns `ErrIncomplete` without exposing a partial bundle; `WithProgress` per-image events | ✅ done | `internal/preserve` |
| CLI `-manifest` per-image progress to stderr (`[N/total] file action`) | ✅ done | `cmd/iiifpreserve` |
| `BlobStore` (`local`, atomic writes) | ✅ done | `internal/preserve` |
| `pipeline.Result` carries manifest bytes; storage root `-store` > config (`store=`) > `~/iiif-images`; `-dry-run` classify-only | ✅ done | `cmd/iiifpreserve` |
| Single-resource downloader `-manifest <url>` (polite Go fetcher, skips filter; replaces curl) | ✅ done | `cmd/iiifpreserve` |
| Institution-nested library layout `<root>/<host>/<slug>/` (`dirFor`) | ✅ done | `internal/preserve` |
| Non-string v2 / object-valued v3 metadata values | ✅ done (`normalizeIIIFText`) | `internal/metadata` |
| Free-text-embedded dates | ⬜ deferred (needs false-positive-rate decision) | DESIGN §4.2 |
| Static HTTPS server over `BlobStore` (mkcert cert; defaulted cert paths + setup-hint on miss; `-no-tls`; graceful) | ✅ done | `internal/serve`, `cmd/iiifpreserve` |
| Serve-time manifest rewrite (provenance-driven, v2+v3; re-points to local Image API service when tiled, else strips; drops non-local thumbnails) | ✅ done | `internal/serve` |
| Rewritten-manifest in-memory cache keyed by request base URL and manifest+provenance file stamps; completed re-preservation invalidates automatically | ✅ done | `internal/serve` |
| Embedded **Mirador 4** viewer (`go:embed` UMD; `/<dir>/` + bundle route); **persistent rich catalogue** — per-manifest title / language / institution (links to the IIIF record) / ~pages / ~size plus researcher-editable display title, free-form notes, and normalized comma-separated tags. Client-side search covers all visible metadata/notes/tags and sorting supports archive path, title, institution, and page count—no database required. Bundle metadata is indexed once at server construction; discovery stops at each manifest root, exact legacy size totals migrate to `.iiifpreserve/catalog.json` in the background, and HTTP requests never traverse image/tile trees. A five-second shallow reconciliation plus manual “Refresh library” action adds completed bundles and removes vanished ones without restarting; `provenance.json`, written last by Preserve, is the completion marker, so partial downloads never surface. Styled in the "Literary Longform" editorial design with **vendored OFL fonts served locally** (Newsreader/Source Serif 4/IBM Plex Mono, `go:embed`, offline — no CDN); per-manuscript viewer carries a quiet editorial masthead (kicker, title, source→IIIF-record, ← Catalogue) and a **left library rail** listing every preserved manuscript (current marked `aria-current`) for in-viewer document switching — our own chrome, deliberately decoupled from Mirador's internal collection UI — beside full-bleed Mirador | ✅ done | `internal/serve` |
| Two-to-four manuscript comparison: accessible ordered catalogue tray; bookmarkable local-slug/canvas URL; Mirador mosaic windows; strict Presentation 2/3 canvas ownership routing; atomic named workspaces portable through metadata exchange; explicit relative-page and normalized viewport/rotation/flip synchronization | ✅ slices 1–4 | `internal/serve`, `viewer-src` |
| Read-only library doctor (`-doctor`) validates bundle JSON/provenance, referenced images, every full-size and tiled JPEG advertised by local `info.json`, annotations, unsafe paths, and catalogue consistency; warnings are non-fatal, integrity errors return non-zero | ✅ done | `internal/serve`, `cmd/iiifpreserve` |
| Researcher metadata exchange (`-export-metadata FILE` / `-import-metadata FILE`) — portable versioned JSON contains catalogue overrides/notes/tags, W3C annotations, and named comparison workspaces. Import matches original manifest URL across differing layouts and is non-destructive: fills blank title/notes, unions tags, adds new annotation IDs/workspaces, ignores exact duplicates, warns and keeps local conflicts/missing bundles. `-dry-run` previews counts/warnings without writes; atomic state writes and a library-wide process lock prevent concurrent server/import updates (the server must be stopped for an actual import) | ✅ done | `internal/serve`, `cmd/iiifpreserve` |
| **Offline annotation store (D)** — user W3C Web Annotations kept beside the bundle as `annotations.json` (AnnotationPage; atomic writes). Stored **verbatim**: only id/type/motivation/body/target are indexed, every other top-level field (MAE's `creator`/`creationDate`/`maeData` editable drawing state/`@context`) round-trips byte-faithful, so MAE can re-match and edit a reloaded annotation. The annotation REST endpoint is the **single source of truth**: `GET /<dir>/annotations` (`?canvas=` filter; `?fmt=oa` → `sc:AnnotationList`/`oa:Annotation` for any v2 consumer; no params = full AnnotationPage = adapter `all`), `POST` create (mints id if absent; rejects non-bundle paths / bad JSON / no target — `CanvasID` tolerant of string and object `source`/`id`), `PUT` update, `DELETE`. POST/PUT/DELETE load-modify-save sequences are serialized per bundle to prevent lost concurrent updates. When running through `Serve`, every mutation surface (annotations and catalogue) requires a loopback Host and rejects foreign `Origin`/`Sec-Fetch-Site: cross-site` browser requests, mitigating localhost CSRF/DNS rebinding while preserving origin-less CLI clients. The served manifest is **not** annotation-injected: MAE's storage adapter loads from this endpoint and dispatches for display itself (its `receiveAnnotation` saga); injecting a manifest reference too would make Mirador core fetch the same page independently and double every annotation. | ✅ done | `internal/annotation`, `internal/serve` |
| **Option A backend (done):** full per-bundle annotation REST surface — `GET` (AnnotationPage list), `POST` (create), `PUT` (update by id), `DELETE` (`?id=`) — over `annotation.{Add,Update,Delete}` (atomic; `ErrNotFound`→404; 405 + Allow on others). Pure Go, no Node; this is the storage-adapter backend MAE will call. | ✅ done | `internal/annotation`, `internal/serve` |
| **Option A frontend (done):** custom Mirador 4 UMD built from a local Mirador 4 **source checkout** (`viewer-src/` + `MIRADOR_SRC`, `make viewer`) with the **MAE annotation editor** + a ~50-line `HttpAnnotationAdapter` mapping MAE's `create/update/delete/all` → the existing REST endpoints, for in-canvas point-and-drag region selection. Source build (not the `mirador@4.0.0` npm tag) is required: MAE's creation companion window only renders with the post-4.0.0 `CompanionWindowRegistry` path. **MAE is pinned to `1.2.4`** (not latest `1.3.0`): `1.2.5` introduced a regression that renders `<Typography component="HotKey">` — an invalid element string — spamming a React casing warning and doubling tooltips; verified by reading each published dist, `1.2.4` is the last release free of it while still carrying the `getStorageAdapterUser` (save) and `companionWindowKey` (panel) code we depend on. Still one embedded asset (MAE CSS inlined, no sidecar); Node is a one-time vendoring step, never the binary. The legacy pure-Go `<details>` xywh form has been removed — MAE's in-canvas drawing is the sole authoring path. | ✅ done | `viewer-src/`, `internal/serve` |
| **Tiling + deep zoom**: local IIIF level0 static pyramids (`tile.go`: `tilePlan`/`infoJSON`/`renderTilePyramid`, `x/image/draw`) | ✅ done | `internal/preserve` |
| Serve-time `info.json` `id` rewrite to the request URL | ✅ done | `internal/serve` |
| Live dogfood: `-manifest` Cookbook v3 + Bodleian + Gallica estampe + 25-page Gallica ms + **e-codices Basel F III 15d (44 ff.)** → tiled preserve → serve → localized + re-pointed + deep tile; resume verified. LOC's supplied Greek manuscript two-step acquisition → 123 canvases is live dry-run verified (full preservation still pending). `-manifest-file` is offline-tested with pristine v3 input. Verified sources tracked in `VERIFIED.md` | ✅ done | `internal/{source,preserve,serve}` `//go:build integration` + manual binary |
The binary runs the full `acquire → select → preserve → serve → view →
deep-zoom` path live (one binary, real institution or single `-manifest`:
filtered, polite, institution-nested on-disk copy with provenance and local
IIIF tile pyramids, served over HTTPS with the manifest re-pointed at local
images + a local Image API service, viewed in an embedded Mirador 4). The
remaining high-value gap is recall: the free-text-date parser (§4.2) and
per-institution field mapping. Project status is tracked here, not in
assistant memory.
