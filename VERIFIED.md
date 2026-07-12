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

Acquired manifest bytes are preserved unmodified on disk and localization
happens at serve time, so a bundle works from any storage root and over plain
HTTP or mkcert HTTPS identically. The LOC fallback described below is explicit
about acquiring a derived manifest; use `-manifest-file` for a byte-faithful
manually downloaded LOC original.

## Acquisition-only live verification

Library of Congress item `0027938281A-ms` (*Greek Manuscripts 1447.
Triodion*) is verified through remote acquisition and enumeration, but is not
listed in the end-to-end table above because its full 123-page bundle has not
yet been downloaded and served. On 2026-07-12 the requested Presentation
manifest returned a Cloudflare 403; the automatic second pull to the official
`?fo=json` item endpoint returned the ordered page groups and the CLI reported
123 images. A representative `tile.loc.gov` Image API page returned its
full-resolution JPEG successfully. The fallback stores a derived Presentation
3 manifest; `-manifest-file` stores a manually downloaded original byte for
byte.

National Library of Scotland manifest `234298262` was also exercised with the
CLI-native `-taster`: 106 images enumerated and the first 2296×3000 JPEG
(747,648 bytes) fetched from `dg-view.nls.uk` via `/full/max`. No source
override or browser-like User-Agent was needed.

## Biblissima collection taster survey

On 2026-07-12, every Biblissima collection card not already represented in the
end-to-end table was checked with the `-taster` semantics: fetch and parse the
manifest, enumerate all canvas images, then fetch and decode only the first
full JPEG without creating a partial bundle. These are **acquisition checks**,
not claims that every page was preserved, tiled, served, and viewed.

| Biblissima source | Tasted manifest | Images | First JPEG | Notes |
|---|---|---:|---:|---|
| Arca | [manifest](https://api.irht.cnrs.fr/ark:/63955/f1x3v4se5m9l/manifest.json) | 16 | 2213×3000 | Passed |
| Bayerische Staatsbibliothek | [manifest](https://api.digitale-sammlungen.de/iiif/presentation/v2/bsb00120194/manifest) | 600 | 1830×2327 | Passed |
| Teca digitale — Biblioteca Medicea Laurenziana | [manifest](https://cdm21059.contentdm.oclc.org/iiif/cataloghi:3209/manifest.json) | 451 | 1204×5988 | Passed |
| Biblioteca Nacional de Portugal | [manifest](https://permalinkbnd.bnportugal.gov.pt/iiif/13436/manifest) | 112 | 1692×2050 | Passed; the first Biblissima result had no enumerable images, so the next result was used |
| Heidelberg University Library, Bibliotheca Palatina | [manifest](https://digi.ub.uni-heidelberg.de/diglit/iiif/cpg765/manifest.json) | 124 | 1688×2588 | Passed |
| NuBIS (Sorbonne) | [manifest](https://nubis.bis-sorbonne.fr/iiif/3/1k46/manifest) | 277 | 2296×2024 | Passed |
| Badische Landesbibliothek Karlsruhe | [manifest](https://digital.blb-karlsruhe.de/i3f/v20/12076/manifest) | 72 | 4582×6614 | Passed |
| Europeana Regia | [manifest](https://gallica.bnf.fr/iiif/ark:/12148/btv1b8446790v/manifest.json) | 724 | 4826×6712 | Passed through Gallica |
| Digital Collections, Leiden University Libraries | [manifest](https://digitalcollections.universiteitleiden.nl/iiif_manifest/item:4243998/manifest) | 133 | 3038×4026 | Passed |
| Mmmonk | [manifest](https://sharedcanvas.be/IIIF/manifests/B_OB_MS618) | 206 | 3010×4000 | Passed |
| FulDig (Fulda) | [manifest](https://fuldig.hs-fulda.de/viewer/api/v1/records/PPN439972744/manifest/) | 13 | 4185×5907 | Passed |
| Ludwig-Maximilians-Universität München | [manifest](https://discover.ub.uni-muenchen.de/digitalisate/BV035774216/BV035774216.json) | 18 | 2203×3091 | Passed |
| Parker Library On the Web | [manifest](https://dms-data.stanford.edu/data/manifests/Parker/bc854fy5899/manifest.json) | 306 | 5890×8010 | Passed |
| Mazarinum (Bibliothèque Mazarine) | [manifest](https://bibnum.institutdefrance.fr/iiif/1386/manifest) | 605 | 8661×4898 | Passed |
| Patrimonio Digital Complutense | [manifest](https://patrimoniodigital.ucm.es/iiif/2/139/manifest) | 398 | 1682×2384 | Passed |
| ENSBA — Bibliothèque numérique des Beaux-Arts de Paris | [manifest](https://alexandrine-bibnum.beauxartsparis.fr/iiif/2/216687/manifest) | 281 | 1667×1250 | Passed |
| British Library, Polonsky Pre-1200 Project | [manifest](https://bl.digirati.io/iiif/ark:/81055/vdc_100054149545.0x000001) | 235 | 3345×5000 | Passed |
| Bibliothèque de l'Agglomération du Pays de Saint-Omer | [manifest](https://bibliotheque-numerique.bibliotheque-agglo-stomer.fr/iiif/1364/manifest) | 173 | 3750×5000 | Passed |
| Ghent University Library | [manifest](https://adore.ugent.be/IIIF/manifests/archive.ugent.be%3A010C9ED6-94DB-11E3-AFBA-2845D43445F2) | 254 | 3183×4234 | Passed |
| Huntington Digital Library | [manifest](https://cdm16003.contentdm.oclc.org/iiif/info/p15150coll7/7800/manifest.json) | 223 | 2676×3000 | Passed |
| Harvard University Library | [Houghton manifest](https://workingwomenoftheeast.omeka.fas.harvard.edu/oa/items/55/manifest.json) | 1 | 1604×2033 | Passed through Harvard's Omeka wrapper and `ids.lib.harvard.edu` image service. Ten Biblissima `drs:` manifests and Harvard's documented `ids:11927378` raw URL returned HTML to both honest and browser-like clients |
| Rosalis (Toulouse) | [manifest](https://gallica.bnf.fr/iiif/ark:/12148/btv1b105600662/manifest.json) | 692 | 3534×4932 | Passed through Gallica |
| Manchester Digital Collections | [manifest](https://www.digitalcollections.manchester.ac.uk/iiif/MS-LATIN-00006) | 398 | 1334×2000 | Passed |
| Durham University and Cathedral Library | [manifest](https://iiif.durham.ac.uk/manifests/trifle/32150/t1/mr/20/t1mr207tp393/manifest) | 2 | 1016×1024 | Passed |
| BVH — Bibliothèques Virtuelles Humanistes | [manifest](https://iiif.bvh.univ-tours.fr/data/manifests/B721816101_RIB_026_manifest.json) | 225 | 776×5624 | Passed |
| Mémonum (Montpellier 3M) | [manifest](https://gallica.bnf.fr/iiif/ark:/12148/bpt6k1095306q/manifest.json) | 266 | 1620×2202 | Passed through Gallica |
| Cambridge Digital Library | [manifest](https://cudl.lib.cam.ac.uk/iiif/MS-ADD-03020) | 420 | 1339×2000 | Passed |
| Staats- und Universitätsbibliothek Bremen | [manifest](https://brema.suub.uni-bremen.de/i3f/v20/1617009/manifest) | 294 | 2808×3826 | Passed; the first Biblissima result returned HTTP 500, so the next result was used |
| Cecilia (Albi) | [manifest](https://cecilia.mediatheques.grand-albigeois.fr/iiif/109/manifest) | 349 | 2368×3312 | Passed |
| Thomas Fisher Rare Book Library, University of Toronto | [manifest](https://iiif.library.utoronto.ca/presentation/v2/fisher2:F6521/manifest) | 300 | 3352×4696 | Passed |
| CBMA — Corpus Burgundiae Medii Aevi | [manifest](https://manuscrits.cbma-project.eu/Auxerre_frad021-cart6_b10435.json) | 143 | 3780×4490 | Passed after making v2 canvas-label decoding tolerant of localized arrays |
| Rotomagus — Bibliothèque numérique patrimoniale de Rouen | [manifest](https://gallica.bnf.fr/iiif/ark:/12148/btv1b10052442z/manifest.json) | 93 | 2506×3936 | Passed through Gallica |
| PaGella — Patrimoine Grenoblois en ligne | [manifest](https://gallica.bnf.fr/iiif/ark:/12148/btv1b10663404h/manifest.json) | 422 | 1641×5498 | Passed through Gallica |
