// Post-install patches for vendored viewer dependencies. Run by
// `make viewer` between `npm ci` and the bundle build; idempotent, and
// fails loudly when an expected pattern disappears (an upstream bump
// changed the dist — re-evaluate the patch before rebuilding).
//
// mirador-textoverlay 1.0.4: two sagas read annotationJson.resources
// unguarded, assuming the IIIF v2 AnnotationList shape. Any W3C/v3
// AnnotationPage (annotations under `items`, no `resources`) — which
// MAE's adapter emits for every canvas — throws a TypeError there;
// redux-saga treats that as fatal and cancels the plugin's whole
// watcher tree, cancelling in-flight OCR text fetches, so the overlay
// spinner never resolves. Guarding the two reads makes the sagas
// ignore v3 pages (they carry nothing these sagas handle anyway).
// TestViewerBundleGuardsTextOverlayAgainstV3AnnotationPages pins the
// patched forms in the committed bundle. Upstream issue worth filing;
// drop this patch when a fixed release is pinned.
import { readFileSync, writeFileSync } from 'node:fs';

const patches = [
  {
    file: new URL('../node_modules/mirador-textoverlay/dist/index.js', import.meta.url),
    edits: [
      {
        from: 'if (!n.resources.some(dr)) return;',
        to: 'if (!(n.resources||[]).some(dr)) return;',
      },
      {
        from: 'let r = n.resources.filter(',
        to: 'let r = (n.resources||[]).filter(',
      },
    ],
  },
];

for (const { file, edits } of patches) {
  let text = readFileSync(file, 'utf8');
  let changed = false;
  for (const { from, to } of edits) {
    if (text.includes(to)) continue; // already applied
    if (!text.includes(from)) {
      console.error(`patches/apply.mjs: pattern not found in ${file.pathname}:\n  ${from}\nThe dependency's dist has changed; re-evaluate this patch.`);
      process.exit(1);
    }
    text = text.replace(from, to);
    changed = true;
  }
  if (changed) {
    writeFileSync(file, text);
    console.log(`patched ${file.pathname}`);
  } else {
    console.log(`already patched ${file.pathname}`);
  }
}
