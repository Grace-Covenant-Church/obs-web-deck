/* Bootstrap.
 *
 * Each module wires its own DOM and subscribes to state events; this file
 * only fixes the order they come up in. Everything below is idempotent, so
 * nothing here depends on the server having answered yet.
 */

import * as ui from './js/ui.js';
import * as editor from './js/editor.js';
import * as joystick from './js/joystick.js';
import * as deck from './js/deck.js';
import * as player from './js/player.js';
import { connect } from './js/net.js';

ui.init();       // toasts first, so later failures have somewhere to surface
editor.init();
joystick.init();
deck.init();
player.init();

connect();
