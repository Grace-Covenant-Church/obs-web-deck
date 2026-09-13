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
  wEnable: $('w-enable'), enableMode: $('f-enable-mode'),
  enableScenes: $('f-enable-scenes'), enableHint: $('f-enable-hint'),
  wSteps: $('w-steps'), stepStrip: $('f-steps'),
  wDelay: $('w-delay'), delay: $('f-delay'),
};

/** Matches the server's own bound; it refuses a longer chain outright. */
const MAX_STEPS = 12;

/** Longest wait the server will honour before a step, in ms. */
const MAX_DELAY = 2000;

let target = null;   // { row, col, existing } or { preset, existing }
let icon = '';
let color = COLORS[0];
let gateScenes = new Set();  // scenes ticked in the "available in" gate
let steps = [];              // the chain being edited, one entry per step
let stepIndex = 0;           // which step the fields below are showing

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

  ed.action.onchange = () => {
    showFields(ed.action.value);
    // Keeps the strip's tooltips honest while a step is being retyped.
    commitStep();
    renderSteps();
  };
  ed.enableMode.onchange = showGate;
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
  ed.wEnable.hidden = false;
  ed.wSteps.hidden = false;

  fillCommon(existing);
  loadSteps(existing?.action);
  open();
}

/** PTZ presets are edited with the action fixed, so the sheet reduces to
 *  naming, colour, icon and which slot on the camera to drive. */
export function openPresetEditor(slot, existing) {
  target = { preset: slot, existing };

  ed.title.textContent = existing ? 'Edit preset' : 'New preset';
  ed.wAction.hidden = true;
  // The preset strip is drawn once per page rather than on every OBS update,
  // so a scene gate there would not follow the scene. Offering one that does
  // nothing is worse than not offering it.
  ed.wEnable.hidden = true;
  // A preset is one camera recall. Chaining belongs on the grid, where the
  // steps have somewhere to be read from.
  ed.wSteps.hidden = true;

  fillCommon(existing);
  loadSteps(existing?.action ?? { type: 'ptzPreset' });
  ed.preset.value = existing?.action?.preset ?? slot;
  open();
}

/* ---------------- chain steps ---------------- */

/* A button holds one action or a chain of them, and the sheet edits either
 * through the same fields: the strip picks which step they are showing.
 *
 * Which of the two a button becomes is decided on save rather than chosen up
 * front - one step is a plain action, more than one is a chain - so chaining is
 * something an existing button grows into, with no separate concept to find. */
function loadSteps(action) {
  steps = action?.type === 'chain'
    ? structuredClone(action.steps ?? [])
    : (action?.type ? [structuredClone(action)] : []);
  if (!steps.length) steps = [{}];
  stepIndex = 0;
  renderSteps();
  loadStep(0);
}

/** Points the shared fields at one step. */
function loadStep(i) {
  const step = steps[i] ?? {};
  ed.action.value = step.type ?? '';
  fillPickers(step);
  if (step.type === 'source') ed.source.value = step.source ?? '';
  showFields(ed.action.value);
}

/** Reads the fields back into the step they were showing. Called before the
 *  strip moves anywhere else, and again on save. */
function commitStep() {
  const a = buildAction();
  if (!a) {
    steps[stepIndex] = {};
    return;
  }
  const wait = Math.min(MAX_DELAY, Math.max(0, Number(ed.delay.value) || 0));
  if (stepIndex > 0 && wait) a.delayMs = wait;
  steps[stepIndex] = a;
}

function renderSteps() {
  ed.stepStrip.innerHTML = '';

  steps.forEach((step, i) => {
    const b = stepChip(String(i + 1), step.type || 'nothing yet');
    b.dataset.on = String(i === stepIndex);
    b.onclick = () => selectStep(i);
    ed.stepStrip.appendChild(b);
  });

  const add = stepChip('+', 'Add a step');
  add.onclick = addStep;
  ed.stepStrip.appendChild(add);

  if (steps.length > 1) {
    const del = stepChip('\u2212', 'Remove this step');
    del.classList.add('step-del');
    del.onclick = removeStep;
    ed.stepStrip.appendChild(del);
  }
}

function stepChip(text, title) {
  const b = document.createElement('button');
  b.className = 'step-chip';
  b.type = 'button';
  b.textContent = text;
  b.title = title;
  return b;
}

function selectStep(i) {
  commitStep();
  stepIndex = i;
  renderSteps();
  loadStep(i);
}

function addStep() {
  commitStep();
  if (steps.length >= MAX_STEPS) {
    toast(`A button runs at most ${MAX_STEPS} steps`);
    return;
  }
  steps.push({});
  selectStep(steps.length - 1);
}

/** No commit first: the step being read out of the fields is the one going. */
function removeStep() {
  steps.splice(stepIndex, 1);
  stepIndex = Math.min(stepIndex, steps.length - 1);
  renderSteps();
  loadStep(stepIndex);
}

/* Collapses the strip into what gets saved: one step is a plain action, several
 * are a chain. Returns null with a toast if there is nothing to save. */
function buildChain() {
  commitStep();
  const filled = steps.filter((step) => step.type);
  if (!filled.length) {
    toast('Pick an action first');
    return null;
  }
  if (filled.length !== steps.length) {
    toast('Every step needs an action');
    return null;
  }
  // A wait before the first step would only delay the press itself, and a step
  // promoted to first by a deletion may still be carrying one.
  delete filled[0].delayMs;
  return filled.length === 1 ? filled[0] : { type: 'chain', steps: filled };
}

function fillCommon(existing) {
  ed.label.value = existing?.label ?? '';
  icon = existing?.icon ?? '';
  color = existing?.color ?? COLORS[0];
  ed.feedback.checked = !!existing?.feedback;
  markColors();
  markIcons();
  fillGate(existing?.enable);
}

/* ---------------- the scene gate ---------------- */

/* Offers every scene OBS currently has, plus any the rule already names. A
 * scene that has been renamed or belongs to another collection would otherwise
 * vanish from the sheet while still gating the button, which reads as the deck
 * being broken. */
function fillGate(rule) {
  const scenes = rule?.scenes ?? [];
  gateScenes = new Set(scenes);
  ed.enableMode.value = scenes.length ? (rule.invert ? 'except' : 'only') : '';

  const known = app.catalog?.scenes ?? app.obs?.sceneList ?? [];
  const offered = [...new Set([...known, ...scenes])];

  ed.enableScenes.innerHTML = '';
  for (const name of offered) ed.enableScenes.appendChild(sceneChip(name));
  if (!offered.length) {
    const note = document.createElement('span');
    note.className = 'hint chips-empty';
    note.textContent = 'No scenes to choose from - is OBS connected?';
    ed.enableScenes.appendChild(note);
  }
  showGate();
}

function sceneChip(name) {
  const b = document.createElement('button');
  b.className = 'scene-chip';
  b.type = 'button';
  b.textContent = name;
  b.dataset.scene = name;
  b.dataset.on = String(gateScenes.has(name));
  b.onclick = () => {
    gateScenes.has(name) ? gateScenes.delete(name) : gateScenes.add(name);
    b.dataset.on = String(gateScenes.has(name));
  };
  return b;
}

/** The scene list is only meaningful once a mode has been picked. */
function showGate() {
  const gated = ed.enableMode.value !== '';
  ed.enableScenes.hidden = !gated;
  ed.enableHint.hidden = !gated;
}

/** Nothing ticked is no gate at all, so it saves as none rather than as a rule
 *  that would switch the button off in every scene. */
function buildEnable() {
  const mode = ed.enableMode.value;
  if (!mode || !gateScenes.size) return undefined;
  const rule = { scenes: [...gateScenes] };
  if (mode === 'except') rule.invert = true;
  return rule;
}

/** Loads one action into the shared detail fields. Called per step, so it must
 *  set every field rather than leaving the previous step's value behind. */
function fillPickers(a) {
  fillSelect(ed.scene, app.catalog?.scenes ?? [], a?.scene);
  fillSources(a?.scene ?? ed.scene.value);
  if (a?.source) ed.source.value = a.source;
  fillSelect(ed.input, app.catalog?.inputs ?? [], a?.input);
  fillHotkeys('', a?.hotkey);
  fillSelect(ed.page, (app.cfg?.deck.pages ?? []).map((p) => p.id), a?.page);
  ed.preset.value = a?.preset ?? 1;
  ed.percent.value = a?.percent ?? 50;
  ed.delay.value = a?.delayMs ?? 0;
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

  // Nothing to wait for before the first step; see stepDelay on the server.
  ed.wDelay.hidden = ed.wSteps.hidden || stepIndex === 0;

  // The lit state belongs to the button, not to one step, so it is offered
  // whenever any step has one - not just the step being edited.
  $('w-feedback').hidden = !litStep(type);
}

/** The step whose state the button lights up for: the first that has one, with
 *  the step being edited read from the dropdown rather than from `steps`. */
function litStep(currentType) {
  if (FEEDBACKABLE.has(currentType)) return currentType;
  return steps.find((step, i) => i !== stepIndex && FEEDBACKABLE.has(step.type))?.type ?? '';
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

/** A chain lights up for its first step that has a lit state, so a "preview
 *  then crossfade" tile still reads as the camera it takes. */
function chainFeedbackSource(action) {
  if (action.type !== 'chain') return action;
  return action.steps.find((step) => FEEDBACKABLE.has(step.type)) ?? action;
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
  const action = buildChain();
  if (!action) return;

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
    const fb = buildFeedback(chainFeedbackSource(action));
    if (fb) btn.feedback = fb;
    const gate = buildEnable();
    if (gate) btn.enable = gate;
    page.buttons = (page.buttons ?? [])
      .filter((b) => !(b.row === row && b.col === col));
    page.buttons.push(btn);
  }

  await persist(deck);
  close();
}

function defaultLabel(action) {
  // A chain is named after what it starts with; the rest rarely fits on a tile.
  if (action.type === 'chain') return defaultLabel(action.steps[0]);
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
