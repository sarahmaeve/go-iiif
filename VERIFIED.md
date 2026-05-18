# Verified institutions

IIIF sources `iiifpreserve` has been run against **end to end** — manifest
fetched, images preserved, local level-0 tile pyramids built, served with
the manifest re-pointed to local copies, deep zoom confirmed. Each row is
something actually exercised, not assumed.

Use any of these via the happy path (see `QUICKSTART.md`):

```sh
iiifpreserve -manifest <manifest-url>
iiifpreserve -serve            # default :8443; -serve=PORT for another
```

| Institution | IIIF host | Manifest URL pattern | Verified example | Notes |
|---|---|---|---|---|
| **Digital Bodleian** (Oxford) | `iiif.bodleian.ox.ac.uk` | `…/iiif/manifest/<uuid>.json` (the item page's "IIIF manifest" link) | MS. Add. A. 22 *Roman de la Rose* (10 ff.); `f317ad0c…` | Honest default UA; passes its Anubis bot-wall (no browser spoof needed). |
| **Gallica / BnF** (Paris) | `gallica.bnf.fr` | `gallica.bnf.fr/iiif/ark:/12148/<ARK>/manifest.json` (derivable from the item page URL) | estampe `btv1b9055204k` (1 img); `btv1b53140000q` (25 ff.) | Built-in: browser-UA override (it 403s honest UAs) + deliberate 13 s/host throttle. Large mss are slow but resumable. |
| **e-codices** (Swiss virtual mss library) | `www.e-codices.unifr.ch` | `…/metadata/iiif/<id>/manifest.json` | Basel UB, F III 15d (`ubb-F-III-0015d`, 44 ff.) | Honest default UA; standard rate. Direct `-manifest` works fully. *Filtered crawls* won't constrain on language/date yet — its labels (`Text Language`, `Date of Origin (English)`, `Place of Origin (English)`) aren't in the default field mapping (lenient filter keeps items rather than dropping them). |
| **IIIF Cookbook** (reference implementation) | `iiif.io` | `iiif.io/api/cookbook/recipe/<recipe>/manifest-*.json` | recipe 0032 *The Gulf Stream* | Reference/test source; public, no rate limits — the fastest first run. |

Manifest fidelity is preserved unmodified on disk; localization happens at
serve time, so a bundle works from any storage root and over plain HTTP or
mkcert HTTPS identically.
