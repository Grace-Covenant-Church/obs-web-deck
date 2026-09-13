/* Page tabs, the button grid, and live feedback on tiles. */

import { app, on, currentPage, setPage, itemKey } from './state.js';
import { iconMarkup } from './icons.js';
import { sendAction, saveDeck } from './net.js';
import { openEditor, loadCatalog } from './editor.js';
import { toast } from './ui.js';
import { renderPtzPage, releasePtz } from './joystick.js';
import { init as initDrag } from './drag.js';

const $ = (id) => document.getElementById(id);

/* What the operator reads instead of the name in deck.json. The names in the
   file are also the wire format - page ids, saved buttons, other clients - so
   the wording is swapped on the way to the screen and nowhere else. */
const DISPLAY_NAMES = { PTZ: 'Camera', Scenes: 'Controls' };
const displayName = (name) => DISPLAY_NAMES[name] ?? name;

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
  btnAuto: $('btn-auto'),
  cols: $('cols'),
};

export function init() {
  el.btnEdit.innerHTML = iconMarkup('edit');
  el.btnEdit.onclick = toggleEditing;

  el.btnAuto.innerHTML = iconMarkup('camcorder');
  el.btnAuto.onclick = toggleAutoScenes;

  // Delegated, so it survives the grid being rebuilt on every render.
  initDrag(el.grid);

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
  // The bar's edit-mode controls are per-page - the column picker does not
  // apply to the PTZ layout, and the scene-tiles switch belongs to whichever
  // page is showing - so a page change has to repaint them too.
  on('page', () => {
    renderPages();
    renderBody();
    renderFeedback(app.obs);
    renderColsPicker();
    renderAutoToggle();
  });
}

function toggleEditing() {
  app.editing = !app.editing;
  el.btnEdit.classList.toggle('on', app.editing);
  document.body.classList.toggle('editing', app.editing);
  renderBody();
  renderFeedback(app.obs);
  renderColsPicker();
  renderAutoToggle();
  if (app.editing) loadCatalog();
}

export function renderAll() {
  renderPages();
  renderBody();
  renderFeedback(app.obs);
  renderColsPicker();
  renderAutoToggle();
}

/* Scene tiles on or off for this page.
 *
 * Offered only while editing, like the column picker: on a page that is mostly
 * generated tiles this switch empties the deck, which is not something to leave
 * a thumb's width from the page tabs during a service.
 */
function renderAutoToggle() {
  const page = currentPage();
  const onGridPage = page && page.layout !== 'ptz';
  el.btnAuto.hidden = !(app.editing && onGridPage);
  if (el.btnAuto.hidden) return;

  const showing = !!page.autoScenes;
  el.btnAuto.classList.toggle('on', showing);
  el.btnAuto.classList.toggle('off', !showing);
  el.btnAuto.setAttribute('aria-pressed', String(showing));
  el.btnAuto.title = showing
    ? 'Hide the tiles OBS fills in'
    : 'Show a tile for every OBS scene';
}

async function toggleAutoScenes() {
  const deck = structuredClone(app.cfg.deck);
  const page = deck.pages.find((p) => p.id === app.pageId);
  if (!page) return;
  page.autoScenes = !page.autoScenes;

  try {
    await saveDeck(deck);
    // Worth saying, because a row of tiles vanishing looks like data loss:
    // generated tiles come from OBS's live scene list and are never written to
    // deck.json, so there is nothing here to lose and the switch flips back.
    if (!page.autoScenes) toast('Scene tiles hidden — the switch brings them back');
  } catch (err) {
    toast(`Could not change scene tiles: ${err.message}`);
  }
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
    tab.textContent = displayName(page.name || page.id);
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
  // available box, so the deck fits whatever shape the screen is. --rows is
  // set below, once the rows actually being drawn are known.
  el.grid.style.setProperty('--cols', cols);
  el.grid.innerHTML = '';

  const byCell = new Map();
  for (const b of page?.buttons ?? []) byCell.set(`${b.row},${b.col}`, b);

  if (page?.autoScenes) addSceneTiles(byCell, cols, rows);

  rendered = [...byCell.values()];

  // A row nobody has put a button in is dead space during a show, so it is
  // dropped and the rest of the deck grows into it. Editing keeps every row:
  // an empty slot is the only place a new button can be aimed at.
  const shown = [];
  for (let r = 0; r < rows; r++) {
    const used = [...Array(cols).keys()].some((c) => byCell.has(`${r},${c}`));
    if (app.editing || used) shown.push(r);
  }

  // The cell size divides by this, so it has to count the rows that survived,
  // not the ones the grid is configured for. Never zero: an empty deck still
  // has to divide by something.
  el.grid.style.setProperty('--rows', Math.max(shown.length, 1));

  for (const r of shown) {
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
  label.textContent = displayName(btn.label ?? '');
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
  // Checked here rather than trusted from the tile's dimming: the scene can
  // change between a render and a thumb landing.
  if (!enabledIn(btn, app.obs)) {
    toast(`${btn.label || 'This button'} is off in ${sceneName(app.obs)}`);
    return;
  }
  if (action.type === 'page') {
    releasePtz();
    setPage(action.page);
    return;
  }
  navigator.vibrate?.(8);
  sendAction(action, { page: app.pageId, row: btn.row, col: btn.col });

  // A page switch is ours, not OBS's, so the server runs the rest of the chain
  // and we take this step here. It is applied at once rather than in sequence:
  // moving the deck changes nothing on air, so where in the order it happens
  // cannot be seen.
  const jump = action.type === 'chain'
    ? action.steps?.find((step) => step.type === 'page')
    : null;
  if (jump) {
    releasePtz();
    setPage(jump.page);
  }
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
    node.dataset.disabled = String(!enabledIn(btn, s));
  }
}

/* Whether the scene on air leaves this button usable. Mirrors Button.EnabledIn
 * on the server, which re-checks it when the press arrives.
 *
 * The rule lists the scenes the button works in, or - inverted - the ones it
 * does not. An empty list is no rule at all, which is what every button without
 * one has.
 *
 * The one thing the server has no equivalent of is the link being down. With no
 * OBS connection the program scene is unknown, and a gated button reads as
 * disabled rather than as available: not knowing whether a control is safe to
 * press is no reason to offer it. */
function enabledIn(btn, s = {}) {
  const scenes = btn.enable?.scenes;
  if (!scenes?.length) return true;
  if (!s.connected) return false;
  const listed = scenes.includes(s.programScene);
  return btn.enable.invert ? !listed : listed;
}

/** For the toast: what to call the scene that switched a button off. */
function sceneName(s = {}) {
  return s.connected ? (s.programScene || 'this scene') : 'this scene';
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
