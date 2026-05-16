// Custom UMD entry for the iiifpreserve embedded viewer.
//
// This rebuilds Mirador 4's UMD surface (global `Mirador`, named exports
// incl. `viewer`/`settings`, default) from source, but folds in the MAE
// annotation editor and an HTTP storage adapter so a researcher can draw
// region annotations directly on the canvas and have them persisted to the
// local bundle — with no Node at runtime and still a single embedded asset
// (MAE's CSS is inlined, not emitted as a second file). DESIGN §C / line 290.
//
// `Mirador.viewer(config)` keeps its existing call signature: the wrapper
// transparently injects the MAE plugins and a per-bundle annotation adapter,
// so the Go-side template change stays minimal and the pure-Go xywh form
// remains a working fallback.
import * as MiradorAll from 'mirador';
import maePlugins from 'mirador-annotation-editor';
import maeCss from 'mirador-annotation-editor/dist/index.css?inline';
import HttpAnnotationAdapter from './adapter.js';

// Single-asset model: inject MAE's stylesheet at load instead of emitting
// a sibling .css the Go embed would have to know about.
if (typeof document !== 'undefined' && !document.querySelector('style[data-mae]')) {
  const style = document.createElement('style');
  style.setAttribute('data-mae', '');
  style.textContent = maeCss;
  document.head.appendChild(style);
}

// The annotation REST surface is served at /<dir>/annotations, a sibling of
// the viewer page — resolve it relative to the document, never templated in.
function annotationEndpoint() {
  return new URL('annotations', document.baseURI).href;
}

export function viewer(config, plugins = []) {
  const merged = {
    ...config,
    annotation: {
      adapter: (canvasId) => new HttpAnnotationAdapter(annotationEndpoint(), canvasId),
      ...(config && config.annotation),
    },
  };
  return MiradorAll.viewer(merged, [...maePlugins, ...plugins]);
}

// Re-export the rest of Mirador's public surface unchanged; the explicit
// local `viewer` above shadows the star-exported one.
export * from 'mirador';
export const { settings } = MiradorAll;
export { HttpAnnotationAdapter };
export default { ...MiradorAll.default, viewer };
