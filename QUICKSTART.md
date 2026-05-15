# Quickstart — zero to a local, deep-zoomable IIIF copy

`iiifpreserve` downloads IIIF manifests + images to a local library, builds
deep-zoom tiles, and serves them through an embedded Mirador viewer — fully
offline. This walks from nothing to a manuscript open in your browser.

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
iiifpreserve: preserved 1 image(s) to ~/iiif-images/iiif.io/…manifest-01.json (skipped 0, 0 failed)
```

It downloaded into `~/iiif-images` (the default library), nested by
institution, and built a local IIIF level-0 tile pyramid for deep zoom.
Re-running is idempotent — already-stored images are skipped, so an
interrupted download just continues.

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
iiifpreserve -serve 127.0.0.1:8443
```

```
iiifpreserve: serving ~/iiif-images at https://127.0.0.1:8443 (Ctrl-C to stop)
iiifpreserve: open https://127.0.0.1:8443/ in a browser for the embedded Mirador viewer
```

Open **https://127.0.0.1:8443/** — an index of every preserved manifest.
Click one to open it in Mirador and zoom in to pixel level. All local; no
network. Stop with Ctrl-C.

## What you have

Under `~/iiif-images/<host>/<slug>/`:

- `manifest.json` — the original, stored unmodified (rewritten to local
  URLs only at serve time)
- `NNNN.jpg` — each page at full size
- `NNNN/` — its IIIF level-0 tile pyramid + `info.json` (deep zoom)
- `provenance.json` — source URLs, recorded license, tile records

It's durable, offline, and re-servable from anywhere — the rewrite is
request-relative, so the library works no matter where it lives.

## Next steps

- **A whole institution, filtered** — crawl a IIIF Collection instead of
  one manifest:
  ```sh
  iiifpreserve -collection <collection-url> -lang fr -from 1400 -to 1500
  ```
  Filters are lenient: an item missing the filtered field is kept, never
  silently dropped. `-workers N` parallelises across hosts; `-journal
  <file>` makes a large crawl resumable.

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
