# Quickstart — zero to a local, deep-zoomable IIIF copy

`iiifpreserve` downloads IIIF manifests + images to a local library, builds
deep-zoom tiles, and serves them through an embedded Mirador viewer — fully
offline. This walks from nothing to a manuscript open in your browser.

## The happy path

You found something you want — a manuscript on a library's website, a link
in a footnote, a IIIF icon on a viewer. Getting it onto your machine is
three moves:

1. **Get its IIIF manifest URL.** Every IIIF item has one. Most viewers
   show a IIIF drag-and-drop icon or a "IIIF manifest" link — copy that
   URL. Some institutions also have a derivable pattern (Gallica below).
2. **Preserve it** — `iiifpreserve -manifest <that-url>` downloads the
   images into `~/iiif-images` and builds deep-zoom tiles.
3. **Serve and view** — `iiifpreserve -serve`, open the
   page, click the item: it's now local, offline, zoomable. (First time
   only: do the one-time HTTPS setup in §3 below, or add `-no-tls` and
   use `http://`.)

### Example A — Digital Bodleian

You're looking at an item on `digital.bodleian.ox.ac.uk`. Its page has a
**IIIF manifest** link (Bodleian manifests look like
`https://iiif.bodleian.ox.ac.uk/iiif/manifest/<id>.json`). Copy it, then:

```sh
iiifpreserve -manifest https://iiif.bodleian.ox.ac.uk/iiif/manifest/f317ad0c-a35b-4e9f-8426-c71f215d382d.json
iiifpreserve -serve
# open https://127.0.0.1:8443/ → click the bodleian entry
```

That manifest is one image; it preserves in a couple of seconds and is
immediately deep-zoomable in the viewer.

### Example B — Gallica / BnF

Gallica needs no link-hunting — the manifest is **derivable from the page
URL**. A Gallica item page is `https://gallica.bnf.fr/ark:/12148/<ARK>`
(e.g. you're viewing `…/ark:/12148/btv1b9055204k`). Insert `/iiif/` and
append `/manifest.json`:

```
https://gallica.bnf.fr/ark:/12148/btv1b9055204k
        → https://gallica.bnf.fr/iiif/ark:/12148/btv1b9055204k/manifest.json
```

```sh
iiifpreserve -manifest https://gallica.bnf.fr/iiif/ark:/12148/btv1b9055204k/manifest.json
iiifpreserve -serve
```

This one (a BnF photograph) is a single 5127×7000 image — ~40 s, because
BnF is politely rate-limited (13 s/host, by design). A multi-page Gallica
manuscript takes proportionally longer but shows per-page progress and is
resumable: re-running continues where it left off.

> Not sure how big something is? `iiifpreserve -manifest <url> -dry-run`
> prints the image count without downloading.

The rest of this document is the same path with full detail and setup.

### Local manifest file (including a manually downloaded LOC manifest)

If you already have the original manifest JSON, preserve it directly:

```sh
iiifpreserve -manifest-file ./manifest.json -dry-run  # count pages first
iiifpreserve -manifest-file ./manifest.json
```

The file must contain an absolute top-level `id` (Presentation 3) or `@id`
(Presentation 2). Its bytes are stored unchanged; that ID supplies the bundle
identity and provenance. Only the page images are fetched.

For Library of Congress single-item URLs, `-manifest` also has an automatic
fallback. It tries the named manifest first and, only if LOC returns its
current 403 challenge, makes a second polite request to LOC's documented item
JSON API and derives the ordered canvases from its `tile.loc.gov` IIIF links:

```sh
iiifpreserve -manifest https://www.loc.gov/item/0027938281A-ms/manifest.json -dry-run
# reports 123 images; remove -dry-run to preserve them
```

Use `-manifest-file` when exact fidelity to a manually downloaded original
manifest matters; the automatic fallback necessarily stores a derived
Presentation 3 manifest because the challenged original was unavailable.

## 0. Prerequisites

- **Go 1.26+** (`go version`)
- **mkcert** — only for the no-warning HTTPS viewer (`brew install mkcert`
  on macOS). You can skip it with `-no-tls` and use plain HTTP.

## 1. Install

From a checkout of this repository:

```sh
make install        # go install ./cmd/iiifpreserve
```

`make install` puts `iiifpreserve` in `$(go env GOPATH)/bin` — make sure
that's on your `PATH`. (Or `make build` → `./bin/iiifpreserve`, then call
`./bin/iiifpreserve` directly.)

> The project/binary name and module path are still provisional (see
> `DESIGN.md` §7); the binary is currently `iiifpreserve`.

## 2. Download a copy

Start with a small public example (the IIIF Cookbook — one image, no rate
limits, a few seconds):

```sh
iiifpreserve -manifest https://iiif.io/api/cookbook/recipe/0032-collection/manifest-01.json
```

You'll see per-image progress, ending with:

```
iiifpreserve: [1/1] 0001.jpg stored
iiifpreserve: preserved 1 image(s) to ~/iiif-images/iiif.io/…manifest-01.json (reused 0, repaired 0)
```

It downloaded into `~/iiif-images` (the default library), nested by
institution, and built a local IIIF level-0 tile pyramid for deep zoom.
Re-running is idempotent: valid stored images are reused without an image
request. If interruption happened after a JPEG was committed but before its
tile pyramid completed, the pyramid is rebuilt from the local JPEG. A bundle
only appears in the catalogue after every required page succeeds.

> Preview first without downloading: add `-dry-run` (prints the image
> count and exits).

## 3. One-time HTTPS setup (for the viewer)

The viewer is served over HTTPS. Make a locally-trusted cert once so the
browser shows no warnings:

```sh
mkcert -install
mkdir -p ~/.config/iiifpreserve/certs
mkcert -cert-file ~/.config/iiifpreserve/certs/127.0.0.1+1.pem \
       -key-file  ~/.config/iiifpreserve/certs/127.0.0.1+1-key.pem \
       127.0.0.1 localhost
```

`iiifpreserve -serve` finds these by default — no flags needed. (Skipping
mkcert entirely? Add `-no-tls` to step 4 and use `http://` instead.)

## 4. Serve and view

```sh
iiifpreserve -serve          # default :8443; -serve=PORT to choose another
```

```
iiifpreserve: serving ~/iiif-images at https://127.0.0.1:8443 (Ctrl-C to stop)
iiifpreserve: open https://127.0.0.1:8443/ in a browser for the embedded Mirador viewer
```

Open **https://127.0.0.1:8443/** — an index of every preserved manifest.
Click one to open it in Mirador and zoom in to pixel level. All local; no
network. Newly completed preservation bundles appear automatically while the
server is running; “Refresh library” forces an immediate shallow refresh.
Stop with Ctrl-C.

The catalogue can be searched across titles, institutions, languages, notes,
and tags, and sorted by archive path, title, institution, or page count. Open
“Edit title or notes” on any entry to supply an English display title, research
notes, or comma-separated tags; these fields persist across server restarts
and library refreshes.

To verify the complete local library—including every image and tile promised
by its IIIF metadata—run the read-only doctor:

```sh
iiifpreserve -doctor
# or: iiifpreserve -doctor -store /Volumes/archive/iiif
```

Warnings do not fail the check. Missing, empty, corrupt, or unsafe referenced
files are reported as errors and produce a non-zero exit status; doctor never
modifies the library.

## Exchange research metadata

Catalogue titles, notes, tags, and annotations are small and can be exchanged
without copying the image/tile library:

```sh
# Researcher A
iiifpreserve -export-metadata my-research.json

# Researcher B, after preserving the same manuscript(s)
iiifpreserve -import-metadata my-research.json -dry-run
iiifpreserve -import-metadata my-research.json
```

Import matches bundles by original manifest URL, so local directory layouts
may differ. It is deliberately non-destructive: blank local titles/notes are
filled, tags are unioned, new annotation IDs are added, exact duplicates are
ignored, and conflicting local values are kept with a warning. Bundles absent
from the recipient's library are skipped. `-dry-run` previews the same merge
counts and warnings without changing any file. Stop the local server before an
actual import; the server and importer use a library lock so two processes
cannot overwrite researcher metadata concurrently. Restart the server after
the import.

## What you have

Under `~/iiif-images/<host>/<slug>/`:

- `manifest.json` — the acquired manifest, stored unmodified (rewritten to
  local URLs only at serve time). It is the upstream original for normal URL
  and file ingestion; LOC's documented fallback is explicitly derived from
  the official item JSON API.
- `NNNN.jpg` — each page at full size
- `NNNN/` — its IIIF level-0 tile pyramid + `info.json` (deep zoom)
- `provenance.json` — source URLs, recorded license, tile records
- `.iiifpreserve/catalog.json` at the library root — catalogue overrides,
  notes, tags, and cached sizes

It's durable, offline, and re-servable from anywhere — the rewrite is
request-relative, so the library works no matter where it lives.

## Next steps

- **Compare manuscripts** — use **Add to comparison** on two to four catalogue
  entries, reorder them in the bottom tray, then open **Compare manuscripts**.
  The resulting local URL is bookmarkable. Each Mirador window remains
  independent, and annotations are read from and saved to the bundle that owns
  the active canvas. The workspace URL tracks current canvases; name and save
  useful workspaces to reopen them from the catalogue. Saved comparisons are
  included in researcher metadata export/import.

- **A whole institution, filtered** — crawl a IIIF Collection instead of
  one manifest:
  ```sh
  iiifpreserve -collection <collection-url> -lang fr -from 1400 -to 1500
  ```
  Filters are lenient: an item missing the filtered field is kept, never
  silently dropped. `-workers N` parallelises across hosts. Real collection
  runs are automatically resumable: completion state is stored under the
  library's `.iiifpreserve/ingest/` directory and isolated by collection URL,
  filters, and institution mapping. Rerun the same command to continue.
  `-dry-run` never reads or changes completion state. The former `-journal
  <file>` option is deprecated and now only migrates that file into the
  automatic state for the current query. Once a scan has completed, rerunning
  makes no collection-discovery requests; use `-fresh` to scan the source from
  its root again for newly added or changed manuscripts. This clears only the
  query's ingest checkpoints, never preserved bundles or research metadata.
  Collection and manifest validators are cached automatically under
  `.iiifpreserve/http-cache/`, so unchanged JSON can be satisfied by a 304;
  page images are deliberately excluded. Use `-ingest-status` for a read-only
  report of pending/failed crawl work and incomplete bundles. Missing pages
  are attempted again on every rerun; `-page-retries N` selects how many
  additional attempts happen in the same run (default 1).

- **Gallica/BnF** — works, but BnF is rate-limited (13 s/host by design),
  so a manuscript takes a while. It's resumable and shows per-page
  progress; pick a short one first, e.g.:
  ```sh
  iiifpreserve -manifest https://gallica.bnf.fr/iiif/ark:/12148/btv1b53199927r/manifest.json
  ```

- **Relocate / configure the library** — `-store <dir>`, or set a default
  in `~/.config/iiifpreserve/config`:
  ```
  store = /Volumes/archive/iiif
  ```

- **Just classify, don't download** — `-dry-run`.

See `DESIGN.md` for architecture and current status.
