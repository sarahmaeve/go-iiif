# iiifpreserve

> Working name — subject to change.

A single Go binary that builds and serves an offline, viewer-ready copy of
subsets of IIIF collections.

It crawls a chosen institution politely (or takes a single `-manifest <url>`),
filters by metadata, writes a complete on-disk copy (images + local IIIF
level0 tile pyramids + manifest + provenance) into a persistent
institution-nested library, serves it over HTTPS with the manifest rewritten
on the fly to point at local images and a local Image API service, and embeds
a Mirador 4 viewer with in-canvas annotation authoring — so a researcher needs
no external tools and the copy survives network outages.

- One compiled binary; no Node or Python at runtime.
- Single-resource preservation via `-manifest <url>` — the path exercised
  end to end (download → tile → serve → view).
- Polite institution crawl with faceted `match`/`no-match` selection (e.g.
  French manuscripts, 15th c.) — built and live-tested at the classification
  layer; not yet dogfooded as a whole-library run.
- Deep zoom from local static tiles.
- Offline W3C Web Annotation authoring, stored beside the bundle.
- A persistent, searchable/sortable local catalogue with editable display
  titles, research notes, and tags;
  catalogue requests never rescan image or tile files, and newly completed
  bundles appear while the server is running.
- A read-only `-doctor` mode that validates manifests, provenance, local
  images, advertised deep-zoom tiles, annotations, and the catalogue index.
- Per-manuscript serialization for annotation edits, with loopback Host and
  browser-Origin checks on every local mutation endpoint.
- Portable researcher-metadata export and non-destructive import for sharing
  catalogue titles/notes/tags and annotations without copying image trees.
- In-memory caching of localized manifests, invalidated by manifest or
  provenance changes.

## Getting started

See [QUICKSTART.md](QUICKSTART.md).

## Design

Architecture, decisions, and status: [DESIGN.md](DESIGN.md).
