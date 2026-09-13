/* Dragging tiles between grid slots while editing.
 *
 * Only the row/col of the buttons already in deck.json change, so a move is
 * the same round trip as any other edit: mutate the deck, post the whole
 * thing, let the broadcast repaint every connected phone.
 *
 * In edit mode a tap already opens the sheet, so there is no long press to
 * wait out: a press that travels becomes a drag, a press that doesn't stays a
 * tap. The threshold is what separates them.
 */

import { app, currentPage, emit } from './state.js';
import { saveDeck } from './net.js';
import { toast } from './ui.js';

/** Travel, in px, before a press counts as a drag rather than a tap. */
const THRESHOLD = 7;

let grid = null;
let st = null;             // the gesture in flight, null when idle
let swallowClick = false;  // a finished drag must not also open the editor

export function init(gridEl) {
  grid = gridEl;
  grid.addEventListener('pointerdown', onDown);
  // Capture phase, so the tile's own click handler never sees the tap that
  // ended a drag.
  grid.addEventListener('click', (ev) => {
    if (!swallowClick) return;
    swallowClick = false;
    ev.stopPropagation();
    ev.preventDefault();
  }, true);
}

/* ---------------- gesture ---------------- */

function onDown(ev) {
  swallowClick = false;
  if (st || !app.editing || !ev.isPrimary) return;

  const node = ev.target.closest?.('.tile');
  const from = node && parseKey(node.dataset.key);
  // Empty slots have nothing to pick up, and generated scene tiles are not
  // ours to place — they mirror whatever collection OBS has loaded.
  if (!from || !buttonAt(from)) return;

  st = { id: ev.pointerId, node, from, x0: ev.clientX, y0: ev.clientY,
         active: false, ghost: null, over: null, dx: 0, dy: 0 };

  // On the window rather than the tile: the grid is rebuilt on every render,
  // and a drag that outlives its own node still has to finish somewhere.
  window.addEventListener('pointermove', onMove);
  window.addEventListener('pointerup', onUp);
  window.addEventListener('pointercancel', cancel);
}

function onMove(ev) {
  if (!st || ev.pointerId !== st.id) return;

  if (!st.active) {
    if (Math.hypot(ev.clientX - st.x0, ev.clientY - st.y0) < THRESHOLD) return;
    lift(ev);
  }

  st.ghost.style.transform =
    `translate(${ev.clientX + st.dx}px, ${ev.clientY + st.dy}px)`;
  hover(cellUnder(ev));
}

/** Detaches a copy of the tile to follow the finger, leaving the slot behind
 *  so the grid keeps its shape while the tile is in the air. */
function lift(ev) {
  const r = st.node.getBoundingClientRect();
  // Grab offset, so the tile does not jump to centre itself under the finger.
  st.dx = r.left - ev.clientX;
  st.dy = r.top - ev.clientY;

  const ghost = st.node.cloneNode(true);
  ghost.className = 'tile tile-ghost';
  ghost.style.width = `${r.width}px`;
  ghost.style.height = `${r.height}px`;
  document.body.appendChild(ghost);

  st.ghost = ghost;
  st.active = true;
  st.node.dataset.dragging = 'true';
  navigator.vibrate?.(12);
}

function onUp(ev) {
  if (!st || ev.pointerId !== st.id) return;
  const { active, from, node } = st;
  const to = active ? cellUnder(ev) : null;
  teardown();

  if (!active) return;
  swallowClick = true;
  // A re-render mid-drag (a changed scene list, another phone saving) leaves
  // the tile we picked up detached, and its cell no longer means anything.
  if (!node.isConnected || !to || same(from, to)) return;
  move(from, to);
}

function cancel(ev) {
  if (!st || ev.pointerId !== st.id) return;
  const wasActive = st.active;
  teardown();
  swallowClick = wasActive;
}

function teardown() {
  if (!st) return;
  window.removeEventListener('pointermove', onMove);
  window.removeEventListener('pointerup', onUp);
  window.removeEventListener('pointercancel', cancel);
  delete st.node.dataset.dragging;
  st.ghost?.remove();
  hover(null);
  st = null;
}

/* ---------------- targets ---------------- */

/** The ghost carries pointer-events: none, so this hit-tests the grid under
 *  it rather than the tile being dragged. */
function cellUnder(ev) {
  const node = document.elementFromPoint(ev.clientX, ev.clientY)?.closest('.tile');
  if (!node || !grid.contains(node)) return null;
  return parseKey(node.dataset.key);
}

function hover(cell) {
  if (same(cell, st?.over)) return;
  if (st) st.over = cell;
  for (const node of grid.querySelectorAll('[data-drop]')) delete node.dataset.drop;
  if (!cell) return;
  const node = grid.querySelector(`[data-key="${cell.row},${cell.col}"]`);
  if (node) node.dataset.drop = 'true';
}

function parseKey(key) {
  const [row, col] = (key ?? '').split(',').map(Number);
  return Number.isInteger(row) && Number.isInteger(col) ? { row, col } : null;
}

const same = (a, b) => a?.row === b?.row && a?.col === b?.col;

/** Explicit buttons only: generated scene tiles are absent from page.buttons,
 *  which is exactly what makes them undraggable. */
function buttonAt(cell) {
  return (currentPage()?.buttons ?? [])
    .find((b) => b.row === cell.row && b.col === cell.col);
}

/* ---------------- saving ---------------- */

async function move(from, to) {
  const deck = structuredClone(app.cfg.deck);
  const page = deck.pages.find((p) => p.id === app.pageId);
  const buttons = page?.buttons ?? [];

  const moving = buttons.find((b) => b.row === from.row && b.col === from.col);
  if (!moving) return;
  // Read the destination before moving anything, or the button in flight
  // matches its own new cell.
  const occupant = buttons.find((b) => b.row === to.row && b.col === to.col);

  moving.row = to.row;
  moving.col = to.col;
  // Slots trade places rather than overwrite: a drag should never be the way
  // a button gets deleted.
  if (occupant) {
    occupant.row = from.row;
    occupant.col = from.col;
  }

  // Repaint from the local copy at once; the server's broadcast confirms it a
  // moment later. Waiting on the round trip leaves the tile looking stuck.
  const previous = app.cfg.deck;
  app.cfg.deck = deck;
  emit('config', app.cfg);

  try {
    await saveDeck(deck);
  } catch (err) {
    app.cfg.deck = previous;
    emit('config', app.cfg);
    toast(`Move failed: ${err.message}`);
  }
}
