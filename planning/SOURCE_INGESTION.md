# Resumable source ingestion

Status: Slices 1–5 implemented

## Goal

A preservation run can be interrupted at any point—including an uncatchable
process kill—and restarted with the same command. It must reuse every valid
local artifact, repair incomplete derived artifacts from local data, and only
contact the source for content that is genuinely absent or must be refreshed.

This document focuses first on the user's concrete requirement: do not
re-request manuscript pages that have already been downloaded.

## Implementation status

Slice 1 is implemented:

- valid existing JPEGs are read and reused with no image request;
- missing or mismatched pyramid `info.json` commit markers cause a local tile
  rebuild from the JPEG;
- corrupt existing JPEGs are selectively refetched;
- successful HTTP responses are accepted only when they contain a readable
  JPEG, with invalid variants falling through to the next candidate;
- stale provenance is invalidated before bundle work and written back only
  when every required page succeeds;
- cancellation leaves committed page files reusable and no completion marker;
- collection mode counts failed/incomplete manifests and exits non-zero so a
  supervisor knows to restart it; and
- restart, tile-repair, corruption, cancellation, and completion-marker
  behavior have deterministic tests.

Pyramid resume deliberately checks only `info.json`, which is written
atomically after all derived images. This keeps the restart check O(1) per
page. The intentionally expensive verification of every advertised tile
remains the responsibility of `-doctor`.

Slice 2 is implemented:

- every real collection run automatically opens state under
  `.iiifpreserve/ingest/`;
- a stable SHA-256 fingerprint isolates collection URL, normalized
  language/date/place selection, filter semantics, and canonical institution
  field mappings, plus preservation semantics;
- successfully committed preserves and no-match decisions are marked done;
- fetch, classification, preservation, cancellation, and journal failures
  remain pending;
- `-dry-run` neither reads nor changes completion state;
- the old `-journal` flag is deprecated and explicitly migrates its entries
  into the automatic state for the current query; and
- an unterminated final journal record left by a kill is discarded on reopen,
  preventing false completion or a poisoned subsequent append.

Slice 3 is implemented:

- real collection runs persist pending and visited collection URLs plus
  discovered manifest URLs in an atomic query-scoped frontier;
- each fetched collection document is committed before newly discovered
  manifests are yielded;
- interruption after that commit resumes from the first pending collection,
  while fetch/decode/save failure leaves the current collection pending;
- cycles and duplicate manifest URLs are suppressed durably;
- a completed query rerun yields local discovery state through the completion
  ledger and performs no collection HTTP requests; and
- explicit `-fresh` clears the query's frontier and completion journal while
  retaining preserved bundles, annotations, and catalogue research metadata.

Slice 4 is implemented:

- validator-backed collection and manifest JSON responses are cached in a
  versioned, SHA-256-keyed file store under `.iiifpreserve/http-cache/`;
- ETag and Last-Modified validators survive process restarts and a 304 is
  satisfied from the atomically committed cached body;
- content type is retained with each bounded response body; and
- page-image responses are never placed in this cache because committed JPEGs
  remain their durable checkpoint.

Slice 5 is implemented:

- both SIGINT and SIGTERM cancel active CLI work cleanly;
- collection runs announce `resuming <run>` with reused, pending, and failed
  counts;
- query-scoped failure records make failed counts durable and are cleared only
  after a durable final disposition;
- read-only `-ingest-status` reports crawl frontiers, completions, failures,
  and bundles that have a manifest but no provenance completion marker;
- missing or failed pages are retried on every resume, while `-page-retries N`
  explicitly controls additional same-run page attempts; and
- collection mode exits non-zero while discovered manifests, collection
  frontier work, or recorded failures remain.

## Baseline before Slice 1

Page-image restart already works at a basic level:

- each `NNNN.jpg` is written through a temporary file and atomic rename;
- `Preserve` checks `BlobStore.Exists` before fetching an image; and
- an existing JPEG is reported as `skipped` without an image HTTP request.

This behavior applies to both `-manifest` and collection preservation because
they call the same `Preserve` function. An interrupted write does not leave a
partial JPEG that later appears complete.

There is also a durable `FileJournal` and a `ResumableSource`, but the current
CLI only uses the journal to skip entries already present in it. The CLI never
calls `MarkDone`, so `-journal` does not presently create useful completion
records during a real crawl.

Conditional-GET support likewise exists as an interface and an in-memory test
implementation, but the CLI constructs its HTTP fetcher without a conditional
store. Validator caching therefore does not survive—or currently participate
in—normal command-line runs.

## Gaps found in the baseline audit

1. **Fixed in Slice 1 — missing tile repair.** If the process dies after `NNNN.jpg` is committed
   but before its tile pyramid and `info.json` are complete, the next run skips
   the JPEG and never rebuilds the missing tiles from it.
2. **Fixed in Slice 1 — premature bundle completion.** `provenance.json` was written even when one
   or more images failed. The catalogue treats provenance as the completion
   marker, so a partial manuscript can appear complete.
3. **Fixed in Slice 2 — no automatic crawl completion ledger.** The optional journal was not
   marked by the CLI and requires a manually selected path.
4. **Fixed in Slice 3 — collection discovery restarts at the root.** A restart walked collection
   documents again before it reaches unfinished manifests.
5. **Manifest requests repeat.** A single-manifest restart re-fetches the
   manifest before it can discover that all page images exist. This is small
   compared with image traffic, but it is still network work and matters
   behind slow or fragile sources.
6. **Fixed in Slice 2 — a simple URL journal is filter-unsafe.** A manuscript classified as
   no-match for one language/date/place query must not be silently skipped in
   a later run with different filters.
7. **Termination handling is incomplete.** The CLI uses interrupt-driven
   cancellation. Atomic files make `SIGKILL` survivable, but ordinary
   termination should also cancel cleanly and flush state.

## Proposed behavior

Resume is automatic under the selected `-store`; researchers do not need to
invent a journal path. A normal rerun resumes. Explicit `-fresh` discards the
query's discovery and completion checkpoints but never deletes preserved
manuscripts or researcher metadata.

The console should distinguish the stages:

```text
[17/240] 0017.jpg reused; tiles complete
[18/240] 0018.jpg reused; rebuilt missing tiles
[19/240] 0019.jpg downloaded
```

On success, a bundle becomes visible to the catalogue only after every
required page has a valid JPEG and its best-effort tile outcome has been
recorded in the final atomic provenance file.

## Implementation plan

### Slice 1: make per-manuscript restart complete

This slice directly satisfies the no-re-download requirement and closes the
tile/provenance holes.

1. Add a read operation to the storage abstraction (or a deliberately local
   equivalent) so `Preserve` can decode an existing JPEG.
2. For every existing `NNNN.jpg`:
   - verify it is a readable, non-empty JPEG;
   - reuse it without an HTTP request;
   - if the pyramid is absent or incomplete, rebuild it from that JPEG; and
   - retain any prior source URL from existing provenance when available.
3. If an existing JPEG is corrupt, quarantine or explicitly replace only that
   page; do not treat mere existence as validity.
4. Do not write final `provenance.json` when required page images failed.
   Instead, keep the atomic page files as the restart checkpoint and report
   the bundle as incomplete.
5. Write final provenance last, atomically, only when the bundle is complete.
6. Add cancellation checks around image and tile loops so graceful termination
   stops promptly without converting cancellation into a completed bundle.

No new database is needed. The artifact files are the page-level checkpoint;
provenance is the bundle commit record.

### Slice 2: wire an automatic, query-aware crawl ledger

Store crawl state below `.iiifpreserve/ingest/`. Key each run by a stable hash
of the collection URL and selection inputs: language, date bounds, place,
institution mapping/schema version, and any semantics-changing options.

Record a manifest as complete only after its final disposition is durable:

- preserved bundle committed;
- confidently classified no-match for this run fingerprint; or
- an explicitly recorded permanent skip.

Failures and cancellation remain pending. Replace or deprecate the current
operator-supplied `-journal` after migrating its useful entries.

### Slice 3: checkpoint collection discovery

Convert recursive collection walking into an explicit durable frontier:

- pending collection URLs;
- visited collection URLs;
- discovered manifest URLs; and
- the run fingerprint.

Commit frontier changes atomically after each collection document. A restart
then continues from the pending frontier instead of walking from the root.
Deduplication remains URL-based within the run.

### Slice 4: durable HTTP validation cache (implemented)

Persist ETag, Last-Modified, content type, and the small response body needed
to satisfy a 304 for collection documents and manifests. Wire it into the
normal CLI fetcher. Do not cache page-image bodies here: preserved JPEGs are
already the durable cache and should bypass HTTP entirely.

Use bounded files under `.iiifpreserve/`, with atomic writes and a versioned
format. SQLite is optional here, not required; a small keyed file store is
adequate until measured scale or concurrent querying justifies a database.

### Slice 5: recovery-oriented CLI (implemented)

- Handle `SIGTERM` as well as interrupt where supported.
- Print `resuming <run>` and counts of reused, pending, and failed items.
- Add a read-only `-ingest-status` report for incomplete bundles and crawl
  runs.
- Add an explicit retry policy for previously failed pages.
- Make exit status non-zero when any requested bundle remains incomplete.

## Acceptance criteria for restart safety

1. Killing the process after any committed page and rerunning causes zero HTTP
   requests for those valid JPEGs.
2. Killing after a JPEG but before `info.json` causes tiles to be rebuilt from
   the local JPEG, with zero image HTTP requests.
3. Killing during an atomic JPEG write leaves no final corrupt JPEG.
4. A bundle with a failed page has no final completion marker and does not
   appear in the catalogue.
5. Rerunning eventually produces complete provenance containing one entry per
   required page.
6. A collection restart does not refetch or reprocess completed manifests for
   the same run fingerprint.
7. Changing selection filters creates or selects a distinct crawl state; old
   no-match decisions cannot hide newly relevant manuscripts.
8. `SIGINT`, `SIGTERM`, and uncatchable termination all leave a state that the
   next run can safely interpret.

## Test strategy

- A counting fetcher verifies zero requests for existing valid page images.
- Deterministic failpoints stop preservation after JPEG commit, during tiling,
  and before provenance commit; a second run verifies repair.
- Corrupt and zero-byte pre-existing JPEG fixtures verify selective refetch.
- CLI integration tests reopen crawl state in a new process-equivalent run.
- Run-fingerprint tests prove changed filters do not reuse no-match decisions.
- Collection-frontier tests stop after a subcollection and verify that restart
  begins at the persisted pending node.
