/* WHEP playback of the OBS program feed.
 *
 * Negotiation is a single SDP POST: offer up, answer back. No trickle
 * endpoint, because on a LAN gathering completes in milliseconds and the
 * extra round trips would cost more than they save.
 */

import { app, on } from './state.js';

const $ = (id) => document.getElementById(id);

const el = {
  stage: $('stage'),
  video: $('preview'),
  emptyText: $('stage-empty-text'),
  latency: $('stat-latency'),
};

let pc = null;
let statsTimer = null;

export function init() {
  on('config', () => { if (!pc) start(); });

  // iOS Safari tears down WebRTC on backgrounding; re-establish on return
  // rather than leaving a frozen last frame on screen.
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden && !pc) start();
  });
}

export async function start() {
  const url = app.cfg?.whepUrl;
  if (!url) {
    el.emptyText.textContent = 'Preview disabled in settings';
    return;
  }

  stop();

  const conn = new RTCPeerConnection();
  pc = conn;

  // Declared before the offer so the SDP advertises both kinds even when OBS
  // is publishing video only.
  conn.addTransceiver('video', { direction: 'recvonly' });
  conn.addTransceiver('audio', { direction: 'recvonly' });

  const stream = new MediaStream();
  conn.ontrack = (ev) => {
    stream.addTrack(ev.track);
    el.video.srcObject = stream;
    el.stage.classList.add('has-video');
  };

  // Fit the stage to the frames OBS is actually sending, rather than assuming
  // a shape. `resize` covers a mid-session change of output resolution, which
  // fires no other event.
  el.video.addEventListener('loadedmetadata', fitStage);
  el.video.addEventListener('resize', fitStage);

  conn.onconnectionstatechange = () => {
    if (!['failed', 'disconnected', 'closed'].includes(conn.connectionState)) return;
    el.stage.classList.remove('has-video');
    el.emptyText.textContent = 'Preview disconnected, retrying…';
    // Only retry the connection we still own; a newer attempt supersedes us.
    if (pc === conn) retry(3000);
  };

  try {
    await conn.setLocalDescription(await conn.createOffer());
    await iceComplete(conn);

    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
      body: conn.localDescription.sdp,
    });

    if (res.status === 404) {
      // Expected until OBS starts its WHIP output; poll rather than error.
      el.emptyText.textContent = 'Waiting for OBS to publish…';
      conn.close();
      if (pc === conn) retry(2000);
      return;
    }
    if (!res.ok) throw new Error(`WHEP ${res.status}`);

    await conn.setRemoteDescription({ type: 'answer', sdp: await res.text() });
    startStats(conn);
  } catch (err) {
    el.emptyText.textContent = `Preview error: ${err.message}`;
    conn.close();
    if (pc === conn) retry(3000);
  }
}

/* Letterboxing is not free on a phone: the stage is a fixed slice of a small
 * screen, so black bars come straight out of the preview's usable size. */
function fitStage() {
  const { videoWidth: w, videoHeight: h } = el.video;
  if (!w || !h) return;
  el.stage.style.setProperty('--stage-ratio', `${w} / ${h}`);
}

function retry(ms) {
  pc = null;
  setTimeout(start, ms);
}

export function stop() {
  clearInterval(statsTimer);
  if (pc) { pc.close(); pc = null; }
  el.stage.classList.remove('has-video');
}

function iceComplete(conn) {
  if (conn.iceGatheringState === 'complete') return Promise.resolve();
  return new Promise((resolve) => {
    const check = () => {
      if (conn.iceGatheringState !== 'complete') return;
      conn.removeEventListener('icegatheringstatechange', check);
      resolve();
    };
    conn.addEventListener('icegatheringstatechange', check);
    // Never let a stalled gather block playback indefinitely.
    setTimeout(resolve, 1500);
  });
}

/* Browsers cannot measure true glass-to-glass latency. The jitter buffer
 * delay is the part of the budget that actually moves when the network
 * degrades, so that is what the HUD reports. */
function startStats(conn) {
  clearInterval(statsTimer);
  let lastDelay = 0;
  let lastCount = 0;

  statsTimer = setInterval(async () => {
    if (conn.connectionState !== 'connected') return;
    const stats = await conn.getStats();
    stats.forEach((r) => {
      if (r.type !== 'inbound-rtp' || r.kind !== 'video') return;
      const delay = r.jitterBufferDelay ?? 0;
      const count = r.jitterBufferEmittedCount ?? 0;
      if (count > lastCount) {
        const ms = ((delay - lastDelay) / (count - lastCount)) * 1000;
        el.latency.textContent = `${Math.round(ms)} ms buf`;
        el.latency.dataset.warn = ms > 400 ? 'true' : 'false';
      }
      lastDelay = delay;
      lastCount = count;
    });
  }, 1000);
}
