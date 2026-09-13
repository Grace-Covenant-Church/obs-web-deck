#!/usr/bin/env node
/* Generates web/js/icons.js from the lucide-static package.
 *
 * The icons are vendored rather than fetched at runtime because the whole app
 * ships as one binary with no network access from the page. Re-run after
 * changing WANTED:
 *
 *   npm pack lucide-static && tar xzf lucide-static-*.tgz
 *   node scripts/gen-icons.mjs ./package
 */

import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const pkg = process.argv[2];

if (!pkg || !existsSync(join(pkg, 'icons'))) {
  console.error('usage: node scripts/gen-icons.mjs <path-to-lucide-static-package>');
  process.exit(1);
}

/* Local alias -> lucide icon name. Aliases keep deck.json readable and stable
   even if we later swap which lucide glyph backs one of them. */
const WANTED = {
  camera:      'camera',
  camcorder:   'video',
  image:       'image',
  slides:      'presentation',
  monitor:     'monitor',
  layers:      'layers',
  film:        'clapperboard',
  cast:        'cast',

  joystick:    'move',
  crosshair:   'crosshair',
  zoomIn:      'zoom-in',
  zoomOut:     'zoom-out',
  focus:       'focus',
  aperture:    'aperture',
  rotate:      'rotate-cw',

  tag:         'tag',
  star:        'star',
  text:        'type',
  eye:         'eye',
  eyeOff:      'eye-off',
  pip:         'picture-in-picture-2',
  panel:       'panel-top',

  mic:         'mic',
  micOff:      'mic-off',
  volume:      'volume-2',
  volumeOff:   'volume-x',
  music:       'music',
  headphones:  'headphones',
  sliders:     'sliders-horizontal',

  broadcast:   'radio',
  record:      'circle-dot',
  pause:       'pause',
  play:        'play',
  stop:        'square',
  cut:         'scissors',
  save:        'save',
  refresh:     'refresh-cw',
  timer:       'timer',
  power:       'power',
  bell:        'bell',
  lightbulb:   'lightbulb',
  users:       'users',
  chat:        'message-square',

  plus:        'plus',
  minus:       'minus',
  edit:        'pencil',
  expand:      'maximize',
  shrink:      'minimize',
  grid:        'grid-3x3',
  arrowLeft:   'arrow-left',
  keyboard:    'keyboard',
  settings:    'settings',
};

/** Pulls the drawable children out of a lucide SVG, dropping its wrapper.
 *  Every lucide icon shares the same 24x24 / currentColor / stroke-2 wrapper,
 *  which we re-apply once in icon() rather than repeating per icon. */
function innerSvg(svg) {
  const open = svg.indexOf('>', svg.indexOf('<svg'));
  const close = svg.lastIndexOf('</svg>');
  return svg
    .slice(open + 1, close)
    .replace(/\s+/g, ' ')
    .trim();
}

const entries = [];
const missing = [];

for (const [alias, name] of Object.entries(WANTED)) {
  const file = join(pkg, 'icons', `${name}.svg`);
  if (!existsSync(file)) { missing.push(`${alias} -> ${name}`); continue; }
  entries.push([alias, name, innerSvg(readFileSync(file, 'utf8'))]);
}

if (missing.length) {
  console.error(`missing ${missing.length} icon(s):\n  ${missing.join('\n  ')}`);
  process.exitCode = 1;
}

const version = JSON.parse(readFileSync(join(pkg, 'package.json'), 'utf8')).version;

const body = entries
  .map(([alias, name, inner]) => `  // lucide: ${name}\n  ${alias}: '${inner.replace(/'/g, "\\'")}',`)
  .join('\n');

const out = `/* GENERATED FILE - do not edit by hand.
 *
 * Icons from Lucide v${version} (ISC licence), vendored by
 * scripts/gen-icons.mjs. Re-run that script to change the set.
 * https://lucide.dev
 *
 * Each entry holds only the drawable children; the shared 24x24 /
 * currentColor / stroke wrapper is applied once in iconMarkup().
 */

export const ICONS = {
${body}
};

export const ICON_NAMES = Object.keys(ICONS);

/**
 * Renders an icon as SVG markup. An unknown name falls through to escaped
 * text, so an emoji or a bare letter remains a valid "icon" in deck.json.
 */
export function iconMarkup(name) {
  if (!name) return '';
  const body = ICONS[name];
  if (!body) return escapeText(name);
  return '<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor"' +
    ' stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round"' +
    ' aria-hidden="true">' + body + '</svg>';
}

function escapeText(s) {
  return String(s).replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}
`;

writeFileSync(join(root, 'web/js/icons.js'), out);
console.log(`wrote web/js/icons.js - ${entries.length} icons from lucide ${version}`);
