// HttpAnnotationAdapter maps MAE's storage-adapter contract onto the
// iiifpreserve per-bundle annotation REST surface (internal/serve:
// GET/POST/PUT/DELETE /<dir>/annotations). MAE constructs one adapter per
// canvas via `adapter(canvasId)`; every mutating method returns the full
// W3C AnnotationPage, exactly like MAE's reference LocalStorageAdapter
// (verified against the vendored MAE dist). Loopback-only, single-user —
// same trust model as the rest of serving; the Go server additionally rejects
// foreign browser origins and non-loopback Host headers on mutations.
const JSON_HEADERS = { 'Content-Type': 'application/json' };

export default class HttpAnnotationAdapter {
  constructor(endpoint, canvasId, user = 'Anonymous') {
    this.endpoint = endpoint; // absolute /<dir>/annotations URL
    this.canvasId = canvasId;
    this.user = user;
    // MAE keys annotations per canvas; our GET filters with ?canvas=.
    this.annotationPageId = `${endpoint}?canvas=${encodeURIComponent(canvasId)}`;
  }

  // MAE's save flow reads the author from the adapter (sets
  // annotation.creator). The reference LocalStorageAdapter exposes this;
  // omitting it threw "getStorageAdapterUser is not a function" and aborted
  // the save before any request. Single-user offline tool → a fixed label.
  getStorageAdapterUser() {
    return this.user;
  }

  async all() {
    try {
      const r = await fetch(this.annotationPageId, { headers: { Accept: 'application/json' } });
      if (!r.ok) return this.#empty();
      const page = await r.json();
      if (!Array.isArray(page.items)) page.items = [];
      return page;
    } catch {
      return this.#empty();
    }
  }

  async create(annotation) {
    await fetch(this.endpoint, { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify(annotation) });
    return this.all();
  }

  async update(annotation) {
    await fetch(this.endpoint, { method: 'PUT', headers: JSON_HEADERS, body: JSON.stringify(annotation) });
    return this.all();
  }

  async delete(annotationId) {
    await fetch(`${this.endpoint}?id=${encodeURIComponent(annotationId)}`, { method: 'DELETE' });
    return this.all();
  }

  async get(annotationId) {
    const page = await this.all();
    return page.items.find((a) => a.id === annotationId) || null;
  }

  #empty() {
    return { id: this.annotationPageId, type: 'AnnotationPage', items: [] };
  }
}
