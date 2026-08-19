/* The in-app button editor.
 *
 * Mutates the deck structure the server already sent us and posts the whole
 * thing back; the server persists it and broadcasts the new layout, so other
 * connected phones update without a reload.
 */

import { app, currentPage } from './state.js';
import { ICON_NAMES, iconMarkup } from './icons.js';
import { fetchCatalog, saveDeck } from './net.js';
import { toast } from './ui.js';

const $ = (id) => document.getElementById(id);

const COLORS = ['#1d4ed8', '#0891b2', '#0f766e', '#65a30d', '#b45309',
                '#be123c', '#7c3aed', '#475569'];

/** Actions that have a meaningful lit state. */
const FEEDBACKABLE = new Set(['scene', 'preview', 'source', 'mute', 'stream',
                              'record', 'recordPause']);

/** Which extra fields each action type needs. */
const FIELDS = {
  scene:     ['w-scene'],
  preview:   ['w-scene'],
  source:    ['w-scene', 'w-source'],
  mute:      ['w-input'],
  hotkey:    ['w-hotkey'],
  ptzPreset: ['w-preset'],
  crossfade: ['w-percent'],
  page:      ['w-page'],
};

const ALL_FIELDS = ['w-scene', 'w-source', 'w-input', 'w-hotkey', 'w-preset', 'w-percent', 'w-page'];

const ed = {
  sheet: $('sheet'), scrim: $('scrim'), title: $('sheet-title'),
  label: $('f-label'), icons: $('f-icons'), colors: $('f-colors'),
  action: $('f-action'), wAction: $('w-action'),
  scene: $('f-scene'), source: $('f-source'), input: $('f-input'),
  hotkey: $('f-hotkey'), hotkeyFilter: $('f-hotkey-filter'),
  preset: $('f-preset'), percent: $('f-percent'), page: $('f-page'),
  feedback: $('f-feedback'),
};

let target = null;   // { row, col, existing } or { preset, existing }
let icon = '';
let color = COLORS[0];

export function init() {
  for (const c of COLORS) {
    const sw = document.createElement('button');
    sw.className = 'swatch';
    sw.style.background = c;
    sw.dataset.color = c;
    sw.onclick = () => { color = c; markColors(); };
    ed.colors.appendChild(sw);
  }

  // A "none" choice first, so an icon can be cleared as well as chosen.
  ed.icons.appendChild(iconChoice('', 'None'));
  for (const name of ICON_NAMES) ed.icons.appendChild(iconChoice(name, name));

  ed.action.onchange = () => showFields(ed.action.value);
  ed.scene.onchange = () => fillSources(ed.scene.value);
  ed.hotkeyFilter.oninput = () => fillHotkeys(ed.hotkeyFilter.value);

  $('sheet-close').onclick = close;
  $('btn-cancel').onclick = close;
  $('btn-clear').onclick = clearButton;
  $('btn-save').onclick = save;
  ed.scrim.onclick = close;
}

function iconChoice(name, title) {
  const b = document.createElement('button');
  b.className = 'icon-choice';
  b.title = title;
  b.dataset.icon = name;
  b.innerHTML = name ? iconMarkup(name) : '<span class="icon-none">–</span>';
  b.onclick = () => { icon = name; markIcons(); };
  return b;
}

function markColors() {
  for (const sw of ed.colors.children) {
    sw.dataset.on = String(sw.dataset.color === color);
  }
}

function markIcons() {
  for (const b of ed.icons.children) {
    b.dataset.on = String(b.dataset.icon === icon);
  }
  const active = ed.icons.querySelector('[data-on="true"]');
  active?.scrollIntoView({ block: 'nearest' });
}

/** The catalog only changes when OBS does, so it is fetched on demand. */
export async function loadCatalog() {
  try {
    app.catalog = await fetchCatalog();
  } catch (err) {
    toast(`Could not read OBS catalog: ${err.message}`);
  }
}

/* ---------------- opening ---------------- */

export function openEditor(row, col) {
  const existing = (currentPage().buttons ?? [])
    .find((b) => b.row === row && b.col === col);
  target = { row, col, existing };

  ed.title.textContent = existing ? 'Edit button' : 'New button';
  ed.wAction.hidden = false;
  ed.action.value = existing?.action?.type ?? '';

  fillCommon(existing);
  fillPickers(existing);
  showFields(ed.action.value);
  open();
}

/** PTZ presets are edited with the action fixed, so the sheet reduces to
 *  naming, colour, icon and which slot on the camera to drive. */
export function openPresetEditor(slot, existing) {
  target = { preset: slot, existing };

  ed.title.textContent = existing ? 'Edit preset' : 'New preset';
  ed.wAction.hidden = true;
  ed.action.value = 'ptzPreset';

  fillCommon(existing);
  ed.preset.value = existing?.action?.preset ?? slot;
  showFields('ptzPreset');
  open();
}

function fillCommon(existing) {
  ed.label.value = existing?.label ?? '';
  icon = existing?.icon ?? '';
  color = existing?.color ?? COLORS[0];
  ed.feedback.checked = !!existing?.feedback;
  markColors();
  markIcons();
}

function fillPickers(existing) {
  const a = existing?.action;
  fillSelect(ed.scene, app.catalog?.scenes ?? [], a?.scene);
  fillSources(a?.scene ?? ed.scene.value);
  if (a?.source) ed.source.value = a.source;
  fillSelect(ed.input, app.catalog?.inputs ?? [], a?.input);
  fillHotkeys('', a?.hotkey);
  fillSelect(ed.page, (app.cfg?.deck.pages ?? []).map((p) => p.id), a?.page);
  ed.preset.value = a?.preset ?? 1;
  ed.percent.value = a?.percent ?? 50;
}

function open() {
  ed.sheet.hidden = false;
  ed.scrim.hidden = false;
}

function close() {
  ed.sheet.hidden = true;
  ed.scrim.hidden = true;
  target = null;
}

/* ---------------- field plumbing ---------------- */

function fillSelect(sel, items, selected) {
  sel.innerHTML = '';
  for (const item of items) {
    const o = document.createElement('option');
    o.value = item;
    o.textContent = item;
    sel.appendChild(o);
  }
  if (selected != null) sel.value = selected;
}

function fillSources(scene) {
  fillSelect(ed.source, app.catalog?.sources?.[scene] ?? []);
}

function fillHotkeys(filter, selected) {
  const all = app.catalog?.hotkeys ?? [];
  const f = filter.trim().toLowerCase();
  const shown = f ? all.filter((h) => h.toLowerCase().includes(f)) : all;
  // The list runs to hundreds of entries; cap it and let the filter narrow.
  fillSelect(ed.hotkey, shown.slice(0, 300), selected);
}

function showFields(type) {
  for (const id of ALL_FIELDS) $(id).hidden = true;
  for (const id of FIELDS[type] ?? []) $(id).hidden = false;
  $('w-feedback').hidden = !FEEDBACKABLE.has(type);
}

/* ---------------- building & saving ---------------- */

function buildAction() {
  const t = ed.action.value;
  if (!t) return null;
  const a = { type: t };
  if (['scene', 'preview', 'source'].includes(t)) a.scene = ed.scene.value;
  if (t === 'source') a.source = ed.source.value;
  if (t === 'mute') a.input = ed.input.value;
  if (t === 'hotkey') a.hotkey = ed.hotkey.value;
  if (t === 'ptzPreset') a.preset = Number(ed.preset.value);
  // Clamped here as well as on the server, so the tile label never promises a
  // percent the T-bar cannot reach.
  if (t === 'crossfade') a.percent = Math.min(100, Math.max(0, Number(ed.percent.value) || 0));
  if (t === 'page') a.page = ed.page.value;
  return a;
}

function buildFeedback(action) {
  if (!ed.feedback.checked || !FEEDBACKABLE.has(action.type)) return undefined;
  switch (action.type) {
    case 'scene':       return { type: 'activeScene', scene: action.scene };
    case 'preview':     return { type: 'previewScene', scene: action.scene };
    case 'source':      return { type: 'sourceVisible', scene: action.scene, source: action.source };
    // A live mic should read as lit, so the mute flag is inverted.
    case 'mute':        return { type: 'inputMuted', input: action.input, invert: true };
    case 'stream':      return { type: 'streaming' };
    case 'record':      return { type: 'recording' };
    case 'recordPause': return { type: 'recordPaused' };
    default:            return undefined;
  }
}

async function save() {
  const action = buildAction();
  if (!action) { toast('Pick an action first'); return; }

  const btn = {
    label: ed.label.value.trim() || defaultLabel(action),
    icon,
    color,
    action,
  };

  const deck = structuredClone(app.cfg.deck);
  const page = deck.pages.find((p) => p.id === app.pageId);

  if (target.preset !== undefined) {
    // Preset strip entries carry no grid coordinates.
    const slot = target.existing?.action?.preset ?? target.preset;
    page.buttons = (page.buttons ?? [])
      .filter((b) => !(b.action?.type === 'ptzPreset' && b.action.preset === slot));
    page.buttons.push(btn);
  } else {
    const { row, col } = target;
    Object.assign(btn, { row, col });
    const fb = buildFeedback(action);
    if (fb) btn.feedback = fb;
    page.buttons = (page.buttons ?? [])
      .filter((b) => !(b.row === row && b.col === col));
    page.buttons.push(btn);
  }

  await persist(deck);
  close();
}

function defaultLabel(action) {
  if (action.type === 'ptzPreset') return `P${action.preset}`;
  if (action.type === 'crossfade') return `Fade ${action.percent}%`;
  return action.type;
}

async function clearButton() {
  const deck = structuredClone(app.cfg.deck);
  const page = deck.pages.find((p) => p.id === app.pageId);

  if (target.preset !== undefined) {
    const slot = target.existing?.action?.preset ?? target.preset;
    page.buttons = (page.buttons ?? [])
      .filter((b) => !(b.action?.type === 'ptzPreset' && b.action.preset === slot));
  } else {
    const { row, col } = target;
    page.buttons = (page.buttons ?? [])
      .filter((b) => !(b.row === row && b.col === col));
  }

  await persist(deck);
  close();
}

async function persist(deck) {
  try {
    await saveDeck(deck);
  } catch (err) {
    toast(`Save failed: ${err.message}`);
  }
}
