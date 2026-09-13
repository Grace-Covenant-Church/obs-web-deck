/* HUD, toasts, the confirm dialog, and the video chrome buttons. */

import { app, on } from './state.js';
import { iconMarkup } from './icons.js';

const $ = (id) => document.getElementById(id);

const el = {
  video: $('preview'),
  pillObs: $('pill-obs'),
  pillStream: $('pill-stream'),
  pillRecord: $('pill-record'),
  sceneTag: $('scene-tag'),
  btnAudio: $('btn-audio'),
  btnFull: $('btn-full'),
  toast: $('toast'),
  confirm: $('confirm'),
  confirmScrim: $('confirm-scrim'),
  confirmTitle: $('confirm-title'),
  confirmText: $('confirm-text'),
  confirmYes: $('confirm-yes'),
  confirmNo: $('confirm-no'),
};

let toastTimer = null;

export function toast(msg) {
  el.toast.textContent = msg;
  el.toast.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.toast.classList.remove('show'), 2600);
}

function setPill(node, state) {
  node.dataset.state = state;
}

function renderHud(s) {
  setPill(el.pillObs, s.connected ? 'on' : 'off');
  setPill(el.pillStream, s.streaming ? 'on' : 'off');
  setPill(el.pillRecord, s.recording ? (s.recordPaused ? 'warn' : 'on') : 'off');
  el.sceneTag.textContent = s.programScene ?? '';
}

export function init() {
  el.btnAudio.innerHTML = iconMarkup('volumeOff');

  el.btnAudio.onclick = () => {
    el.video.muted = !el.video.muted;
    el.btnAudio.innerHTML = iconMarkup(el.video.muted ? 'volumeOff' : 'volume');
  };

  el.btnFull.onclick = toggleFullscreen;

  // Esc, the back gesture and the system bars all exit without going through
  // the button, so the icon follows the browser rather than our own clicks.
  document.addEventListener('fullscreenchange', renderFullscreen);
  document.addEventListener('webkitfullscreenchange', renderFullscreen);
  renderFullscreen();

  el.confirmYes.onclick = () => settle(true);
  el.confirmNo.onclick = () => settle(false);
  el.confirmScrim.onclick = () => settle(false);
  document.addEventListener('keydown', (ev) => {
    if (ev.key === 'Escape') settle(false);
  });

  on('obsstate', renderHud);
  on('error', toast);
  on('link', (up) => {
    if (!up) setPill(el.pillObs, 'off');
  });

  renderHud(app.obs);
}

/* ---------------- confirm ---------------- */

/* Asks before a press that overwrites something. A promise rather than a
 * callback, so the caller reads as one sequence: ask, then act.
 *
 * Only one dialog exists, so a second ask cancels the first rather than
 * stacking; nothing in the app opens two, but a resolve that never fires would
 * leave the caller hanging forever. */
let pending = null;

export function askConfirm({ title, text = '', confirm = 'OK' }) {
  settle(false);

  el.confirmTitle.textContent = title;
  el.confirmText.textContent = text;
  el.confirmText.hidden = !text;
  el.confirmYes.textContent = confirm;
  el.confirmScrim.hidden = false;
  el.confirm.hidden = false;
  el.confirmYes.focus();

  return new Promise((resolve) => { pending = resolve; });
}

function settle(answer) {
  if (!pending) return;
  const resolve = pending;
  pending = null;
  el.confirm.hidden = true;
  el.confirmScrim.hidden = true;
  resolve(answer);
}

/* ---------------- fullscreen ---------------- */

/* The whole page goes fullscreen, not just the preview. On a phone being used
 * as a control surface the browser chrome is a hazard: a stray swipe at the
 * address bar mid-show costs the operator the deck, and the reclaimed strip is
 * a row of tiles. */
const fullscreenEl = () =>
  document.fullscreenElement ?? document.webkitFullscreenElement ?? null;

function toggleFullscreen() {
  if (fullscreenEl()) {
    (document.exitFullscreen ?? document.webkitExitFullscreen)?.call(document);
    return;
  }

  const root = document.documentElement;
  const request = root.requestFullscreen ?? root.webkitRequestFullscreen;
  if (!request) {
    // iOS Safari only ever fullscreens a <video>, so there is nothing to call
    // here. Installing the page gets the same result, and index.html already
    // carries the meta tags that make it open standalone.
    toast('On iOS, use Share \u2192 Add to Home Screen for fullscreen');
    return;
  }

  // navigationUI is ignored where it is not understood; where it is, it keeps
  // Android's system bars from sitting on top of the bottom row of tiles.
  Promise.resolve(request.call(root, { navigationUI: 'hide' }))
    .catch(() => toast('The browser refused fullscreen'));
}

function renderFullscreen() {
  const active = !!fullscreenEl();
  el.btnFull.innerHTML = iconMarkup(active ? 'shrink' : 'expand');
  el.btnFull.title = active ? 'Exit fullscreen' : 'Fullscreen';
  el.btnFull.classList.toggle('on', active);
  document.body.classList.toggle('fullscreen', active);
}
