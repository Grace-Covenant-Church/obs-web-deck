/* Shared client state and a minimal pub/sub.
 *
 * Modules publish and subscribe here rather than importing each other, which
 * keeps the dependency graph acyclic: net publishes, deck/player/ui react.
 */

export const app = {
  cfg: null,      // { deck, whepUrl, videoMode, publishing, ptzEnabled }
  obs: {},        // OBS state snapshot from the server
  catalog: null,  // scenes/sources/inputs/hotkeys, fetched lazily by the editor
  pageId: null,
  editing: false,
};

const listeners = new Map();

export function on(event, fn) {
  if (!listeners.has(event)) listeners.set(event, new Set());
  listeners.get(event).add(fn);
  return () => listeners.get(event).delete(fn);
}

export function emit(event, payload) {
  for (const fn of listeners.get(event) ?? []) {
    try {
      fn(payload);
    } catch (err) {
      // One bad subscriber must not stop the others from updating.
      console.error(`[${event}]`, err);
    }
  }
}

export function currentPage() {
  return app.cfg?.deck.pages.find((p) => p.id === app.pageId) ?? null;
}

/** Switching pages records it in the hash so a reload lands back here. */
export function setPage(id) {
  app.pageId = id;
  history.replaceState(null, '', `#${id}`);
  emit('page', id);
}

export function applyConfig(cfg) {
  app.cfg = cfg;
  if (!app.pageId || !cfg.deck.pages.some((p) => p.id === app.pageId)) {
    // A page named in the URL hash wins on first load, so an operator can
    // bookmark straight to the PTZ surface.
    const fromHash = decodeURIComponent(location.hash.slice(1));
    app.pageId = cfg.deck.pages.some((p) => p.id === fromHash)
      ? fromHash
      : cfg.deck.pages[0]?.id ?? null;
  }
}

/** Key format must match the server's scene-item map exactly. */
export const itemKey = (scene, source) => `${scene}\u0000${source}`;
