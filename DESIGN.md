# Local IIIF Preservation Tool — Design

> Status: planning. Working name TBD. Single Go binary.

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
| Viewer | **Vendored prebuilt JS bundle** (OpenSeadragon / Triiiceratops / Mirador), embedded in binary | "No Node" = no Node runtime or npm build; a browser-run static asset is allowed |
| Discovery | **Per-institution crawl**, no aggregator | No dependency on Europeana/Biblissima; full control |
| Source adapters | `collection` (universal IIIF Collection tree) + `changestream` (IIIF Change Discovery API, resumable, preferred when available) behind one `Source` interface | Collection tree is the guaranteed path; change streams enable cheap refresh |
| Subsetting | Local **metadata normalization** → typed `WorkRecord` → predicate filter, applied **before** image download | No global IIIF search exists; metadata is free-text/multilingual/per-institution |
| Filter policy | **Conservative**: three buckets `match` / `uncertain` / `no-match`; `uncertain` → review queue, not fetched until researcher approves | Avoids silently dropping real targets or wasting bandwidth on false positives |
| Storage | **`BlobStore` interface**: `local` + `s3-compatible` (AWS/MinIO/Backblaze/R2) | Researcher choice of local drive or personal object storage |
| Per-item preservation | Keep **max institution-permitted source image + level-0 tile pyramid** | Re-tilable offline; a true preservation copy, not just view tiles |
| Serving | Static IIIF Image API (level 0) + preserved Presentation manifests with rewritten image-service URLs, served straight from `BlobStore` | Works even without the binary running; S3-friendly |
| Tiling | Reimplemented in Go, in-process (not shelling out to `iiif-tiler-rust`) | Removes the article's loose-coupling seam |
| Pilot institutions | **Gallica (BnF)** and **Digital Bodleian** | Large French collection + well-structured metadata to stress-test the normalizer |

## 3. Pipeline

```
config(institutions)
  → Source adapter (collection | changestream)
  → manifest fetch (conditional GET via ETag/If-Modified-Since, checkpointed)
  → metadata normalize  → filter (match | uncertain | no-match)
  → [match] polite image fetch (per-host token bucket, backoff, content-hash dedup)
  → keep max institution-permitted source image
  → tile to level-0 pyramid
  → BlobStore.Put (local | s3)
  → rewrite Presentation manifest image-service URLs → store alongside tiles
serve: static IIIF Image API (level 0) + preserved Presentation manifests from BlobStore
view:  embedded prebuilt viewer bundle
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
typed records. Classify `match` / `uncertain` / `no-match`; never fetch
`uncertain` until reviewed. Filter runs before any image download.

### 4.3 Polite trawler
Per-host token-bucket rate limit, global concurrency cap, exponential backoff on
429/503, conditional GET so re-runs are cheap, content-hash dedup, resumable
checkpoint/journal so an interrupted large crawl restarts where it stopped.
Honor `info.json` profile / `maxWidth` / `sizes` and manifest `rights`/license
before selecting the "max permitted" derivative — politeness and legality.

### 4.4 Storage & serving
`BlobStore` interface, `local` + `s3-compatible` implementations. Per item:
max-permitted source image + level-0 pyramid + rewritten Presentation manifest.
Level-0 means zoom levels and tile size are baked at download time; keeping the
source image makes offline re-tiling possible without re-fetching.

## 5. Known risks / validate early

1. **Metadata normalization is make-or-break.** Gallica vs. Bodleian
   label/date conventions differ; century/date-range parser and language
   normalizer need real-data testing first. Prime TDD target — date-string
   parsing is naturally test-first.
2. **Change Discovery availability uncertain** per institution. Do not assert
   Gallica/Bodleian publish Activity Streams until verified. `collection`
   adapter is the guaranteed path; build it first.
3. **License / permitted-size compliance.** Read `info.json`
   profile/`maxWidth`/`sizes` and manifest `rights`/license before choosing the
   max derivative.
4. **Loose-coupling seam** from the source article eliminated by in-process
   tiling.

## 6. Suggested first build slice

`collection` Source adapter + metadata normalizer + filter, validated against
live Gallica and Bodleian data, **test-first on the date/language parsers** —
*before* any download/tile/serve code, since the filter gates everything and
carries the most risk.

## 7. Open / deferred

- Working name for the project/binary.
- Confirm Change Discovery API availability for Gallica and Digital Bodleian.
- Choice of vendored viewer (OpenSeadragon vs. Triiiceratops vs. Mirador).
- Static vs. selectable tile parameters (levels/tile size) per collection.
- Review-queue UX (CLI list + approve, or a small served page).
