/* Page tabs, the button grid, and live feedback on tiles. */

import { app, on, currentPage, setPage, itemKey } from './state.js';
import { iconMarkup } from './icons.js';
import { sendAction, saveDeck } from './net.js';
import { openEditor, loadCatalog } from './editor.js';
import { toast } from './ui.js';
import { renderPtzPage, releasePtz } from './joystick.js';

const $ = (id) => document.getElementById(id);

/* Tiles currently rendered, explicit and generated alike. Feedback iterates
   this rather than page.buttons, which holds only the explicit ones. */
let rendered = [];
let lastSceneSig = null;

const el = {
  pages: $('pages'),
  grid: $('grid'),
  ptz: $('ptz'),
  gridWrap: $('grid-wrap'),
  btnEdit: $('btn-edit'),
  cols: $('cols'),
};

export function init() {
  el.btnEdit.innerHTML = iconMarkup('edit');
  el.btnEdit.onclick = toggleEditing;

  on('config', renderAll);
  on('obsstate', (s) => {
    // A changed scene list means different tiles, not just different colours.
    const sig = (s.sceneList ?? []).join('\u0000');
    if (currentPage()?.autoScenes && sig !== lastSceneSig) {
      lastSceneSig = sig;
      renderBody();
    }
    renderFeedback(s);
  });
  on('page', () => { renderPages(); renderBody(); renderFeedback(app.obs); });
}

function toggleEditing() {
  app.editing = !app.editing;
  el.btnEdit.classList.toggle('on', app.editing);
  document.body.classList.toggle('editing', app.editing);
  renderBody();
  renderFeedback(app.obs);
  renderColsPicker();
  if (app.editing) loadCatalog();
}

export function renderAll() {
  renderPages();
  renderBody();
  renderFeedback(app.obs);
  renderColsPicker();
}

/* Column width picker, offered only while editing. Rows are left alone: the
 * grid is bottom-anchored and scrolls with content, so width is the dimension
 * that actually changes how reachable the tiles are on a phone. */
function renderColsPicker() {
  const cfg = app.cfg;
  const onGridPage = currentPage()?.layout !== 'ptz';
  el.cols.hidden = !(app.editing && onGridPage && cfg);
  if (el.cols.hidden) return;

  const min = cfg.minCols ?? 2;
  const max = cfg.maxCols ?? 5;
  const current = cfg.deck.grid.cols;

  el.cols.innerHTML = '';
  for (let n = min; n <= max; n++) {
    const b = document.createElement('button');
    b.className = 'col-opt';
    b.textContent = n;
    b.title = `${n} columns`;
    b.setAttribute('aria-pressed', String(n === current));
    b.onclick = () => setCols(n);
    el.cols.appendChild(b);
  }
}

async function setCols(n) {
  if (n === app.cfg.deck.grid.cols) return;

  const deck = structuredClone(app.cfg.deck);
  deck.grid.cols = n;

  // Narrowing hides buttons rather than deleting them; they stay in the file
  // and come back when the grid is widened again. Say so, because a tile
  // vanishing with no explanation reads as data loss.
  const hidden = deck.pages
    .filter((p) => p.layout !== 'ptz')
    .flatMap((p) => p.buttons ?? [])
    .filter((b) => b.col >= n || b.row >= deck.grid.rows).length;

  try {
    await saveDeck(deck);
    if (hidden) {
      toast(`${hidden} button${hidden === 1 ? '' : 's'} now off-grid — widen to restore`);
    }
  } catch (err) {
    toast(`Could not change columns: ${err.message}`);
  }
}

function renderPages() {
  el.pages.innerHTML = '';
  for (const page of app.cfg?.deck.pages ?? []) {
    const tab = document.createElement('button');
    tab.className = 'page-tab';
    tab.textContent = page.name || page.id;
    tab.setAttribute('aria-selected', String(page.id === app.pageId));
    tab.onclick = () => {
      // Leaving the PTZ page must never strand a camera mid-slew.
      releasePtz();
      setPage(page.id);
    };
    el.pages.appendChild(tab);
  }
}

function renderBody() {
  const page = currentPage();
  const isPtz = page?.layout === 'ptz';

  el.ptz.hidden = !isPtz;
  el.gridWrap.hidden = isPtz;

  if (isPtz) renderPtzPage(page);
  else renderGrid(page);
}

function renderGrid(page) {
  const { cols, rows } = app.cfg.deck.grid;

  // The stylesheet derives a square cell size from these against the
  // available box, so the deck fits whatever shape the screen is.
  el.grid.style.setProperty('--cols', cols);
  el.grid.style.setProperty('--rows', rows);
  el.grid.innerHTML = '';

  const byCell = new Map();
  for (const b of page?.buttons ?? []) byCell.set(`${b.row},${b.col}`, b);

  if (page?.autoScenes) addSceneTiles(byCell, cols, rows);

  rendered = [...byCell.values()];

  for (let r = 0; r < rows; r++) {
    const row = document.createElement('div');
    row.className = 'deck-row';
    for (let c = 0; c < cols; c++) {
      const btn = byCell.get(`${r},${c}`);
      row.appendChild(btn ? tile(btn) : emptyCell(r, c));
    }
    el.grid.appendChild(row);
  }
}

/* Generates a tile per OBS scene into the page's free cells.
 *
 * These are derived from live state and never written to deck.json, so the
 * deck follows whatever collection OBS has loaded instead of naming scenes
 * that may not exist. Explicit buttons keep their cell; scenes flow into
 * what's left, and any that don't fit are dropped rather than overflowing. */
function addSceneTiles(byCell, cols, rows) {
  const scenes = app.obs?.sceneList ?? [];
  let i = 0;

  for (let r = 0; r < rows && i < scenes.length; r++) {
    for (let c = 0; c < cols && i < scenes.length; c++) {
      const key = `${r},${c}`;
      if (byCell.has(key)) continue;
      const scene = scenes[i++];
      byCell.set(key, {
        row: r, col: c,
        label: scene,
        icon: 'camcorder',
        color: '#1d4ed8',
        generated: true,
        action: { type: 'scene', scene },
        feedback: { type: 'activeScene', scene },
      });
    }
  }
}

function tile(btn) {
  const node = document.createElement('button');
  node.className = 'tile';
  node.style.setProperty('--accent', btn.color || '#334155');
  node.dataset.key = `${btn.row},${btn.col}`;

  if (btn.icon) {
    const icon = document.createElement('span');
    icon.className = 'tile-icon';
    icon.innerHTML = iconMarkup(btn.icon);
    node.appendChild(icon);
  }

  const label = document.createElement('span');
  label.className = 'tile-label';
  label.textContent = btn.label ?? '';
  node.appendChild(label);

  if (btn.generated) node.dataset.generated = 'true';

  node.onclick = () => {
    if (!app.editing) { press(btn); return; }
    // Generated scene tiles have nothing to edit: they mirror OBS, and any
    // change would be overwritten on the next scene list update.
    if (btn.generated) {
      toast('Scene buttons follow OBS. Turn off autoScenes to place them by hand.');
      return;
    }
    openEditor(btn.row, btn.col);
  };
  return node;
}

function emptyCell(row, col) {
  const node = document.createElement('button');
  node.className = 'tile tile-empty';
  node.dataset.key = `${row},${col}`;
  if (app.editing) {
    node.innerHTML = iconMarkup('plus');
    node.onclick = () => openEditor(row, col);
  } else {
    node.disabled = true;
  }
  return node;
}

function press(btn) {
  const action = btn.action;
  if (!action?.type) return;
  if (action.type === 'page') {
    releasePtz();
    setPage(action.page);
    return;
  }
  navigator.vibrate?.(8);
  sendAction(action);
}

/** Applies live OBS state to tiles without rebuilding the DOM. */
function renderFeedback(s = {}) {
  const page = currentPage();
  if (!page || page.layout === 'ptz') return;

  for (const btn of rendered) {
    const node = el.grid.querySelector(`[data-key="${btn.row},${btn.col}"]`);
    if (!node) continue;

    const active = evalFeedback(btn.feedback, s);
    if (active === null) delete node.dataset.active;
    else node.dataset.active = String(active);

    node.dataset.unavailable = String(isUnavailable(btn, s));
  }
}

function evalFeedback(fb, s) {
  if (!fb) return null;
  // With no OBS connection every value is unknown, and an inverted condition
  // (a live mic) would otherwise read as lit on a dead link.
  if (!s.connected) return null;

  let v;
  switch (fb.type) {
    case 'activeScene':   v = s.programScene === fb.scene; break;
    case 'previewScene':  v = s.previewScene === fb.scene; break;
    case 'streaming':     v = !!s.streaming; break;
    case 'recording':     v = !!s.recording; break;
    case 'recordPaused':  v = !!s.recordPaused; break;
    case 'sourceVisible': v = !!s.sources?.[itemKey(fb.scene, fb.source)]; break;
    case 'inputMuted':    v = !!s.inputs?.[fb.input]; break;
    default: return null;
  }
  return fb.invert ? !v : v;
}

/** A tile naming something this OBS profile lacks is dimmed, rather than
 *  silently doing nothing when pressed. */
function isUnavailable(btn, s) {
  if (!s.connected) return true;
  const fb = btn.feedback;
  if (fb?.type === 'sourceVisible') {
    return !(itemKey(fb.scene, fb.source) in (s.sources ?? {}));
  }
  if (fb?.type === 'inputMuted') return !(fb.input in (s.inputs ?? {}));
  return false;
}
