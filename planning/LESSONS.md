# Lessons from comparison synchronization

## Goal

The comparison workspace originally offered two optional features:

- **Pair page position**, which moved the other manuscripts to the canvas at
  the same zero-based index.
- **Sync zoom and pan**, which was intended to copy zoom, pan, rotation, and
  flip from the active manuscript window to the other windows.

The expected behavior was that a completed zoom or pan in either window would
be reflected in the others. In practice, viewport synchronization did not
move the other windows reliably and eventually left zooming, panning, and page
navigation unresponsive.

## Original implementation

Page pairing subscribed to the Mirador Redux store, detected a changed
`canvasId`, found its index in the source manifest, and dispatched
`Mirador.setCanvas` for the corresponding canvases in the other windows.

Viewport synchronization used `Mirador.OSDReferences` to access each
OpenSeadragon viewer. An `animation-finish` handler normalized the source
view's visible bounds relative to its home bounds, mapped that rectangle onto
each target's home bounds, and called `fitBounds`. Rotation and flip were
copied separately.

To prevent a programmatic target animation from synchronizing back to its
source, the implementation used a 1.5-second suppression deadline per target
window.

## Debugging attempts that did not work

### Replacing the timing-based feedback guard

The first diagnosis was that target animations could finish after the
suppression deadline and start an endless feedback loop. This was plausible:
OpenSeadragon's `fitBounds(..., false)` starts a spring animation whose
`animation-finish` event is indistinguishable from the event produced by a
user interaction.

The attempted fix removed the deadline and applied synchronized bounds and
rotation immediately. Immediate OpenSeadragon updates do not emit a second
`animation-finish`, so this should have broken the reciprocal event cycle.
It did not make synchronization work for the user.

### Repairing re-entrant page bookkeeping

The page-pairing subscription dispatched Redux actions synchronously while it
still held an older snapshot of the windows. A nested subscription could
record the newly paired canvas, after which the outer subscription overwrote
that record with stale state.

The attempted fix recorded canvas state before nested dispatches, refreshed
the windows afterward, and protected the `syncingPage` flag with
`try`/`finally`. This addressed a real re-entrancy hazard but did not resolve
the reported viewport failure or the resulting unusable state.

### Updating both OpenSeadragon and Mirador Redux state

Source inspection showed that Mirador also stores viewport coordinates in
Redux and may reapply them to OpenSeadragon during a React render. Updating
only the OpenSeadragon target could therefore be undone by Mirador restoring
its older viewport.

The next attempted fix dispatched Mirador's exported `updateViewport` action
after changing the OpenSeadragon target, using the target's resulting zoom,
center, rotation, and flip. This also failed to correct the behavior observed
by the user.

## Why the debugging process was insufficient

The investigation relied on source inspection, API contracts, static template
assertions, and the Go test suite. Those checks established that the expected
code was rendered and that the server behavior remained valid, but they did
not prove that:

- synchronization handlers attached to the live viewer instances;
- the relevant events fired in the assumed order;
- React, Redux, and OpenSeadragon agreed on viewport ownership;
- a propagated viewport survived the complete render and animation cycle; or
- repeated interaction in both windows remained responsive.

The existing comparison test checked for JavaScript strings such as public
Mirador API names. It was not a behavioral test and passed while the feature
was broken. Each unsuccessful patch was therefore based on a plausible defect
without a deterministic reproduction that demonstrated the defect was the
primary cause. Passing Go tests gave false confidence about browser-side
behavior they did not exercise.

Avoiding headless-browser and screenshot-driven debugging was reasonable, but
the replacement should have been a focused JavaScript state/event harness or
instrumented runtime trace. Without one, changing feedback guards and state
ownership was still guesswork.

## Resolution

Page pairing and viewport synchronization were removed from the comparison
workspace. The controls, event handlers, Redux dispatches, URL modes, saved
comparison fields, research-metadata fields, tests, and documentation for the
features were deleted.

Comparison windows now navigate, zoom, pan, rotate, and flip independently.
The workspace continues to preserve ordered manuscript selection, current
canvas deep links, annotations, and saved comparisons.

## Guidance for a future attempt

Before reintroducing synchronization:

1. Put the synchronization algorithm in a small JavaScript module rather than
   embedding it in the page template.
2. Define one authoritative viewport state owner and one explicit mechanism
   for distinguishing user changes from propagated changes.
3. Build a deterministic test with mocked viewers, synchronous Redux
   subscriptions, delayed animation completion, and alternating interactions
   from both windows.
4. Verify canvas changes, viewer replacement, missing image dimensions, and
   different image and window aspect ratios.
5. Add temporary structured event tracing during manual verification so the
   event source, target, generation, and stored viewport can be compared.
6. Treat static template assertions as wiring checks only, never as evidence
   that synchronization works.
