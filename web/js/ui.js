/* HUD, toasts, and the video chrome buttons. */

import { app, on } from './state.js';
import { iconMarkup } from './icons.js';

const $ = (id) => document.getElementById(id);

const el = {
  stage: $('stage'),
  video: $('preview'),
  pillObs: $('pill-obs'),
  pillStream: $('pill-stream'),
  pillRecord: $('pill-record'),
  sceneTag: $('scene-tag'),
  btnAudio: $('btn-audio'),
  btnFull: $('btn-full'),
  toast: $('toast'),
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
  el.btnFull.innerHTML = iconMarkup('expand');

  el.btnAudio.onclick = () => {
    el.video.muted = !el.video.muted;
    el.btnAudio.innerHTML = iconMarkup(el.video.muted ? 'volumeOff' : 'volume');
  };

  el.btnFull.onclick = () => {
    if (document.fullscreenElement) document.exitFullscreen();
    else el.stage.requestFullscreen?.();
  };

  on('obsstate', renderHud);
  on('error', toast);
  on('link', (up) => {
    if (!up) setPill(el.pillObs, 'off');
  });

  renderHud(app.obs);
}
