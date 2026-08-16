/* The PTZ surface: a proportional pan/tilt stick, a spring-return zoom
 * rocker, and the preset strip.
 *
 * Both controls are spring-loaded - they report a deflection while held and
 * snap back to centre on release. obs-ptz carries pan, tilt and zoom in one
 * continuous-move call, so a single sender drives all three axes together.
 */

import { send } from './net.js';
import { app, on } from './state.js';
import { iconMarkup } from './icons.js';
import { toast } from './ui.js';
import { openPresetEditor } from './editor.js';

const $ = (id) => document.getElementById(id);

const el = {
  stick: $('stick'),
  knob: $('stick-knob'),
  rocker: $('rocker'),
  rockerKnob: $('rocker-knob'),
  presets: $('presets'),
};

/** Deflections below this are hand tremor, not intent. */
const DEADZONE = 0.08;

/** How often the current deflection is pushed while a control is held. The
 *  server dedupes unchanged values, so this doubles as the deadman keepalive
 *  without flooding the camera. */
const SEND_MS = 60;

const axes = { pan: 0, tilt: 0, zoom: 0 };
let timer = null;

function push() {
  send({ type: 'ptz', pan: axes.pan, tilt: axes.tilt, zoom: axes.zoom });
}

function beginSending() {
  if (!timer) timer = setInterval(push, SEND_MS);
  push();
}

function stopSendingIfIdle() {
  if (axes.pan || axes.tilt || axes.zoom) return;
  clearInterval(timer);
  timer = null;
  push(); // one explicit zero so the camera halts immediately
}

/** Centres every axis and halts the camera. Safe to call at any time. */
export function releasePtz() {
  const wasLive = axes.pan || axes.tilt || axes.zoom || timer;
  axes.pan = axes.tilt = axes.zoom = 0;
  moveKnob(0, 0);
  moveRocker(0);
  clearInterval(timer);
  timer = null;
  if (wasLive) send({ type: 'ptz', pan: 0, tilt: 0, zoom: 0 });
}

const shape = (v) => (Math.abs(v) < DEADZONE ? 0 : v);

/* ---------------- pan / tilt stick ---------------- */

function moveKnob(nx, ny) {
  const r = el.stick.clientWidth / 2 - el.knob.clientWidth / 2;
  el.knob.style.transform = `translate(${nx * r}px, ${ny * r}px)`;
}

function initStick() {
  let pointer = null;

  const update = (ev) => {
    const rect = el.stick.getBoundingClientRect();
    const radius = rect.width / 2;
    let dx = (ev.clientX - (rect.left + radius)) / radius;
    let dy = (ev.clientY - (rect.top + rect.height / 2)) / radius;

    // Clamp to the unit circle so a corner drag is not faster than a straight
    // one at full deflection.
    const mag = Math.hypot(dx, dy);
    if (mag > 1) { dx /= mag; dy /= mag; }

    axes.pan = shape(dx);
    axes.tilt = shape(-dy); // screen Y grows downward; camera tilt does not
    moveKnob(dx, dy);
  };

  el.stick.addEventListener('pointerdown', (ev) => {
    pointer = ev.pointerId;
    el.stick.setPointerCapture(pointer);
    el.stick.classList.add('active');
    update(ev);
    beginSending();
  });

  el.stick.addEventListener('pointermove', (ev) => {
    if (ev.pointerId === pointer) update(ev);
  });

  const release = (ev) => {
    if (ev.pointerId !== pointer) return;
    pointer = null;
    el.stick.classList.remove('active');
    axes.pan = axes.tilt = 0;
    moveKnob(0, 0);
    stopSendingIfIdle();
    if (timer) push();
  };
  el.stick.addEventListener('pointerup', release);
  el.stick.addEventListener('pointercancel', release);
}

/* ---------------- zoom rocker ---------------- */

function moveRocker(nx) {
  const track = el.rocker.clientWidth;
  const travel = track / 2 - el.rockerKnob.clientWidth / 2;
  el.rockerKnob.style.transform = `translateX(${nx * travel}px)`;
}

function initRocker() {
  let pointer = null;

  const update = (ev) => {
    const rect = el.rocker.getBoundingClientRect();
    const half = rect.width / 2;
    const nx = Math.max(-1, Math.min(1, (ev.clientX - (rect.left + half)) / half));
    axes.zoom = shape(nx);
    moveRocker(nx);
  };

  el.rocker.addEventListener('pointerdown', (ev) => {
    pointer = ev.pointerId;
    el.rocker.setPointerCapture(pointer);
    el.rocker.classList.add('active');
    update(ev);
    beginSending();
  });

  el.rocker.addEventListener('pointermove', (ev) => {
    if (ev.pointerId === pointer) update(ev);
  });

  const release = (ev) => {
    if (ev.pointerId !== pointer) return;
    pointer = null;
    el.rocker.classList.remove('active');
    axes.zoom = 0;
    moveRocker(0); // CSS transition animates the snap back to centre
    stopSendingIfIdle();
    if (timer) push();
  };
  el.rocker.addEventListener('pointerup', release);
  el.rocker.addEventListener('pointercancel', release);
}

/* ---------------- presets ---------------- */

/** Long-press threshold for storing rather than recalling a preset. */
const HOLD_MS = 800;

export function renderPtzPage(page) {
  el.presets.innerHTML = '';

  const presets = (page.buttons ?? [])
    .filter((b) => b.action?.type === 'ptzPreset')
    .sort((a, b) => (a.action.preset ?? 0) - (b.action.preset ?? 0));

  for (const p of presets) el.presets.appendChild(presetButton(p));

  if (app.editing) {
    const add = document.createElement('button');
    add.className = 'preset preset-add';
    add.innerHTML = iconMarkup('plus');
    add.onclick = () => openPresetEditor(nextFreeSlot(presets));
    el.presets.appendChild(add);
  }
}

function nextFreeSlot(presets) {
  const used = new Set(presets.map((p) => p.action.preset));
  for (let i = 1; i <= 64; i++) if (!used.has(i)) return i;
  return 1;
}

function presetButton(p) {
  const b = document.createElement('button');
  b.className = 'preset';
  b.style.setProperty('--accent', p.color || '#b45309');

  if (p.icon) {
    const icon = document.createElement('span');
    icon.className = 'preset-icon';
    icon.innerHTML = iconMarkup(p.icon);
    b.appendChild(icon);
  }
  const label = document.createElement('span');
  label.className = 'preset-label';
  label.textContent = p.label || `P${p.action.preset}`;
  b.appendChild(label);

  if (app.editing) {
    b.onclick = () => openPresetEditor(p.action.preset, p);
    return b;
  }

  // Tap recalls; a long press stores the current position, which is how PTZ
  // operators expect presets to behave.
  let held = false;
  let hold = null;

  b.addEventListener('pointerdown', () => {
    held = false;
    hold = setTimeout(() => {
      held = true;
      navigator.vibrate?.([12, 40, 12]);
      send({ type: 'ptzPreset', preset: p.action.preset, save: true });
      toast(`Stored ${p.label || `preset ${p.action.preset}`}`);
    }, HOLD_MS);
  });
  b.addEventListener('pointerup', () => {
    clearTimeout(hold);
    if (!held) send({ type: 'ptzPreset', preset: p.action.preset, save: false });
  });
  b.addEventListener('pointercancel', () => clearTimeout(hold));

  return b;
}

/* ---------------- wiring ---------------- */

export function init() {
  initStick();
  initRocker();

  // Backgrounding the phone or navigating away must not leave the camera
  // slewing; these are the last line of defence before the server's deadman.
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) releasePtz();
  });
  window.addEventListener('pagehide', () => send({ type: 'ptzStop' }));

  on('config', () => { if (!app.cfg?.ptzEnabled) releasePtz(); });
}
