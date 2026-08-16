/* WebSocket transport to the deck server.
 *
 * One socket carries control messages up and state broadcasts down. The
 * server owns the OBS connection, so this is the only channel the phone needs
 * and a drop here means no control at all.
 */

import { app, applyConfig, emit } from './state.js';

let ws = null;
let msgId = 0;

export function connect() {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(`${proto}//${location.host}/ws`);

  ws.onopen = () => emit('link', true);

  ws.onmessage = (ev) => {
    let msg;
    try {
      msg = JSON.parse(ev.data);
    } catch {
      return;
    }

    switch (msg.type) {
      case 'hello':
        applyConfig(msg.config);
        app.obs = msg.state ?? {};
        emit('config', msg.config);
        emit('obsstate', app.obs);
        break;
      case 'config':
        applyConfig(msg.config);
        emit('config', msg.config);
        break;
      case 'state':
        app.obs = msg.state ?? {};
        emit('obsstate', app.obs);
        break;
      case 'error':
        emit('error', msg.message);
        break;
    }
  };

  ws.onclose = () => {
    emit('link', false);
    // Reconnect promptly rather than backing off; without this socket the
    // deck is inert, so a slow retry is worse than a slightly noisy one.
    setTimeout(connect, 1200);
  };
}

export function send(obj) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(obj));
}

export function sendAction(action) {
  send({ type: 'action', id: ++msgId, action });
}

/** Fetches the live OBS inventory the editor offers as choices. */
export async function fetchCatalog() {
  const res = await fetch('/api/catalog');
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

/** Posts the whole layout; the server persists it and pushes the new config
 *  back to every connected phone, so a second device updates live. */
export async function saveDeck(deck) {
  const res = await fetch('/api/deck', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(deck),
  });
  if (!res.ok) throw new Error(await res.text());
}
