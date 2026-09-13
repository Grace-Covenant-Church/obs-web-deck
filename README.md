# obs-deck

A phone-friendly, Stream Deck style control surface for OBS, with a
low-latency WebRTC preview of the program output and a virtual joystick for
PTZ cameras.

Everything ships in **one static binary** — UI assets, WebRTC media server and
obs-websocket client included. No Node, no Python, no MediaMTX, no runtime.

```
  OBS ──WHIP──▶ obs-deck ──WHEP──▶ phone browser
                   │
                   └──obs-websocket──▶ OBS  (scenes, sources, audio, hotkeys, PTZ)
```

---

## Installing on Ubuntu

### Dependencies

**obs-web-deck itself has none.** It is a statically linked binary with no
libc dependency, no runtime, and its UI assets compiled in. Copy it and run
it.

What you do need is OBS and, for camera control, the PTZ plugin:

```sh
# OBS Studio 28 or newer - obs-websocket is built in from 28 onward
sudo add-apt-repository ppa:obsproject/obs-studio
sudo apt update
sudo apt install obs-studio

# Optional: PTZ camera control
# Grab the .deb for your OBS version from the releases page:
#   https://github.com/glikely/obs-ptz/releases
sudo apt install ./obs-ptz-*-linux-x86_64.deb

# Only needed for the on-screen QR badge: Chromium draws it. xdg-utils is
# the fallback that opens the code in whatever browser you do have.
sudo apt install chromium-browser xdg-utils
```

The MPEG-TS preview path needs `libx264` and `libopus` available to OBS's
FFmpeg. Both are present in the PPA and Ubuntu archive builds. If you run OBS
as a **Flatpak**, it is sandboxed: obs-websocket and the UDP preview still work
over loopback, but plugins must be installed into the Flatpak, not with `apt`.

### Install

```sh
tar xzf obs-web-deck-v0.1.0-linux-amd64.tar.gz
cd obs-web-deck-v0.1.0-linux-amd64

install -Dm755 obs-web-deck ~/.local/bin/obs-web-deck
mkdir -p ~/.config/obs-web-deck
cp config/*.json ~/.config/obs-web-deck/

obs-web-deck -config ~/.config/obs-web-deck
```

It prints a QR code and the LAN address, and pins the same code to the corner
of the screen.

### Let the phone through the firewall

Ubuntu ships ufw disabled, but if you have enabled it:

```sh
sudo ufw allow 8080/tcp comment 'obs-web-deck'
```

The MPEG-TS port needs no rule: it is loopback only.

### Autostart at login

A systemd **user** service starts it with your desktop session and restarts it
if it dies. Write `~/.config/systemd/user/obs-web-deck.service`:

```ini
[Unit]
Description=OBS Web Deck
After=graphical-session.target

[Service]
Type=simple
ExecStart=%h/.local/bin/obs-web-deck -config %h/.config/obs-web-deck
Restart=on-failure
RestartSec=3

[Install]
WantedBy=graphical-session.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now obs-web-deck
systemctl --user status obs-web-deck
journalctl --user -u obs-web-deck -f
```

`WantedBy=graphical-session.target` rather than `default.target` is what puts
the badge on screen. A service reads its environment once, at the moment it
starts, and `default.target` gets there first at boot - before the desktop has
published `DISPLAY` - so obs-web-deck finds no session to open a window in and
quietly skips the badge for the rest of the day. Hung off the graphical session
instead, it starts with that environment already set.

Add `-no-window` to the `ExecStart` line to keep the badge off; the QR page is
always at `http://<this-machine>:8080/qr` as well, and `?overlay=1` on it is the
transparent version for an OBS Browser Source.

There is no `After=obs` dependency on purpose: obs-web-deck starts happily
without OBS and reconnects whenever OBS appears, so ordering does not matter.

To keep it running when you are logged out (a dedicated booth machine that
boots to a console):

```sh
sudo loginctl enable-linger $USER
```

## Build

```sh
make build          # -> ./obs-web-deck
make check          # gofmt, vet, tests
```

`CGO_ENABLED=0` is not optional and the Makefile sets it. With cgo enabled Go
links its libc resolver, producing a dynamically linked binary that will fail
on a machine with a different glibc:

```
$ file obs-web-deck
obs-web-deck: ELF 64-bit LSB executable, x86-64, statically linked
```

Cross-compiling needs no toolchain on the target:

```sh
make cross          # linux amd64/arm64, macOS arm64, Windows amd64
```

## Run

```sh
./obs-web-deck                                    # ./config, listens on :8080
./obs-web-deck -config ~/.config/obs-web-deck     # config elsewhere
./obs-web-deck -addr 0.0.0.0:9000                 # different port
./obs-web-deck -no-window                         # no QR badge on screen
./obs-web-deck -no-qr                             # no QR in the terminal
```

On startup it prints a scannable QR code and the LAN address, and puts the same
code on screen as a badge. The address comes from the routing table rather than
the first interface found, so Docker bridges and VPNs do not get picked by
mistake.

### The QR badge

A 250x250 window in the bottom-right corner of the operator's screen, always on
top, no title bar and nothing on it but the code - so a volunteer can scan it
off the booth screen without anyone leaving OBS. Clicks pass straight through
it: it cannot be focused, moved or typed into, and a stray click lands on
whatever is behind it.

Chromium draws the badge in app mode, and obs-deck then takes the window over
by talking X11 directly - which is also how the window ends up click-through,
since nothing on the command line can set an input region. That needs no
X libraries at build time and no cgo, so the single static binary survives.

It lands on the primary monitor (the other heads on a booth machine are usually
program and preview feeds), above a taskbar rather than behind it, and lifted
clear of the bottom edge where a dock or OBS's own status bar tends to sit. Under a
Wayland session it goes through XWayland, which is the only way a window gets a
say in where it sits.

Where any of that is unavailable - no Chromium-family browser, or no X server
to talk to - the code still opens in whatever browser the desktop uses, just as
an ordinary window that you place yourself.

The badge runs on a browser profile of its own - under `~/.cache/obs-web-deck`,
or inside the snap's home if the browser is a snap, which is confined and would
otherwise ignore the setting - so an already-running browser cannot swallow it.
It closes with obs-deck either way, through the window manager rather than by
killing a process, since the browser that ends up drawing it is not necessarily
the one obs-deck started. Use `-no-window` to skip it; the page stays at
`http://<this-machine>:8080/qr`.

On first run it writes default `settings.json` and `deck.json` into the config
directory. It never overwrites files you have already edited.

The OBS password can be supplied as `OBS_PASSWORD` instead of putting it in
the config file.

---

## Setup, in order

### 1. Enable obs-websocket

In OBS: **Tools → WebSocket Server Settings** → enable the server, set a
password, note the port (default 4455). Put the password in `settings.json`
under `obs.password`, or export `OBS_PASSWORD`.

### 2. Point OBS at the preview

If OBS is **not** streaming anywhere else, use the native WHIP output —
lowest latency and fewest moving parts. If you also need YouTube, skip to
*Streaming to YouTube and previewing at the same time* below.

**Settings → Stream → Service: WHIP**, and set the server to the URL obs-deck
prints at startup:

```
http://127.0.0.1:8080/preview/whip
```

Leave the bearer token blank. If OBS is already streaming somewhere real,
Stream is occupied — see *Two outputs at once* below.

### 3. Set the encoder for low latency

This is where latency is actually won or lost. OBS defaults will cost you
seconds:

| Setting | Value | Why |
|---|---|---|
| Output resolution | **640x360** | See *Preview resolution* below |
| Keyframe interval | **1s** | The default 2s is how long a joining viewer waits, and how long a catch-up freezes for |
| B-frames | **0** | B-frames buffer future frames by definition |
| Tune / preset | `zerolatency` (x264) or `P1` low-latency (NVENC) | |
| Rate control | CBR | Avoids VBV bursts the jitter buffer must absorb |
| Bitrate | 1–1.5 Mbps at 640x360 | A phone gains nothing above this, and less buffering means less delay |

Rescale the *output*, not the canvas: **Settings → Output → Advanced →
Streaming → Rescale Output → 640x360**. The canvas stays whatever your real
program output needs.

### 4. Open it on the phone

Browse to the address obs-deck prints, e.g. `http://192.168.1.113:8080`.
Tap the expand button in the preview's top-right corner to take over the whole
screen (see [Fullscreen](#fullscreen)), or add to home screen for the same
thing on every launch.

### 5. PTZ (only on a machine with obs-ptz and real hardware)

Install the [obs-ptz](https://github.com/glikely/obs-ptz) plugin, configure the
camera in its dock, then set `ptz.enabled: true` and `ptz.deviceId` in
`settings.json`.

---

## Expected latency

Roughly **250–450 ms** end to end on a decent LAN:

| Stage | Typical |
|---|---|
| Camera capture into OBS | 50–150 ms |
| OBS encode | 30–80 ms |
| obs-deck RTP forward | < 5 ms |
| WiFi | 10–50 ms |
| Phone jitter buffer | 50–150 ms |

The HUD shows the jitter buffer figure, which is the part that actually moves
when the network degrades. Browsers cannot measure true glass-to-glass
latency, so treat that number as the tunable component, not the whole budget.

**Put the OBS machine on wired ethernet.** WiFi jitter on the publish side is
the single easiest way to blow the budget.

---

## Preview resolution

The preview runs at **640x360**, set on the OBS output that feeds it — obs-deck
never transcodes, so the frame size is whatever OBS encodes.

It is a small frame on purpose. The preview exists to answer "is the right
thing on air, and is it framed roughly right", which 640x360 answers on a phone
held at arm's length. Everything above that costs latency at both ends: more
pixels to encode, more bits to push across the WiFi, more to decode on a phone
that is also running the deck. Judging focus or reading lower-thirds is a job
for the OBS machine's own monitor.

**Match the canvas aspect ratio.** OBS's output rescale stretches, it does not
letterbox, so 640x480 against a 1920x1080 canvas gives a squashed preview
rather than a 4:3 one with bars. 640x360 is the 16:9 figure; scale from your
own canvas if it is not 16:9.

The UI does not assume the shape either — the stage box takes its aspect ratio
from the frames that arrive, so rescaling the output to something else re-fits
the preview with no black bars and no config change here.

---

## Staying realtime

MPEG-TS arrives over UDP, which has no back pressure: nothing upstream slows
down when this machine has a bad moment. A stall leaves a backlog of datagrams
queued, and playing that backlog out in order would leave the preview
permanently as late as the worst stall — a control surface showing a
second-old program feed while you press buttons.

So in `mpegts` mode obs-deck measures how far the media timeline has slipped
behind the wall clock and, past `video.maxLagMs` (400 ms by default), drops
frames until it has caught up:

```
[media] preview is 1.42s behind, dropping to the next keyframe
[media] preview caught up, 38 frames dropped
```

H264 can only be re-entered at a keyframe, so video is discarded up to the next
one — with a 1s keyframe interval, a catch-up costs at most a second of
freeze and then resumes live. A backlog deeper than one GOP is dropped a GOP at
a time rather than declared solved at the first keyframe. Opus access units
decode independently, so audio is dropped one unit at a time.

The count is in `/api/health` as `droppedFrames`. A number that climbs steadily
rather than in occasional bursts means the machine is not keeping up with the
encoder at all — lower the preview bitrate or resolution, or move to a faster
preset.

Set `maxLagMs` negative to turn the guard off and forward every frame however
late it is. The guard does not apply in `builtin` (WHIP) mode: there, RTP is
forwarded packet by packet as it arrives, so there is no buffer of ours to
fall behind in.

---

## Icons

Icons come from [Lucide](https://lucide.dev) (ISC), vendored into
`web/js/icons.js` by `scripts/gen-icons.mjs` so the page never reaches the
network. They are monochrome and inherit the tile's colour.

To change the set, edit `WANTED` in that script and re-run it:

```sh
npm pack lucide-static && tar xzf lucide-static-*.tgz
node scripts/gen-icons.mjs ./package
```

An icon name that is not in the set falls through to plain text, so an emoji
or a bare letter still works in `deck.json`.

## Editing buttons

Tap the pencil in the page bar. Tap any tile — or any empty slot — to set its
label, icon, colour and action. Changes save to `deck.json` and push live to
every connected phone.

### Rearranging tiles

In edit mode, drag a tile to move it. A press that stays put is still a tap and
opens the editor; a press that travels picks the tile up, and the slot it would
land in is outlined as you go. Dropping on an empty slot moves the button;
dropping on a filled one makes the two trade places, so a drag is never a way
to lose a button. Release outside the grid, or back where you started, to
cancel.

Auto-generated scene tiles cannot be dragged — they follow whatever collection
OBS has loaded, so a hand-placed position would not survive the next scene list
update. Turn off `autoScenes` for the page to place those by hand.

### Grid width

In edit mode a **2 / 3 / 4 / 5** picker appears next to the pencil. Fewer,
larger tiles are easier to hit mid-show; more tiles fit more scenes. The bound
is enforced on the server too, so a hand-edited `deck.json` cannot produce a
grid the UI can't render.

Narrowing the deck **hides** buttons that fall outside it rather than deleting
them — they stay in `deck.json` and return when you widen it again. The UI
says how many went off-grid so a tile vanishing never reads as data loss.

Tile size is the smaller of what the width allows and what the height allows,
so the deck fits the screen at any column count and in either orientation.

### Fullscreen

The expand button in the preview's top-right corner puts the **whole page** —
preview and deck together — into fullscreen, not just the video. That is the
point: on a phone acting as a control surface the browser chrome is a hazard,
because a stray swipe at the address bar mid-show costs the operator the deck.
The strip it reclaims goes to the tiles.

The icon follows the browser rather than the button, so exiting with Esc, the
back gesture or the system bars leaves it showing the right state.

iOS Safari only ever fullscreens a `<video>`, so the button says so and points
at **Share → Add to Home Screen**, which opens the deck standalone and gets the
same result. The meta tags for that are already in `index.html`.

### Portrait and landscape

Portrait stacks the preview above the deck; landscape puts them side by side,
with the preview taking full height and capped at 60% of the width. The deck
needs no special casing in either — it sizes itself from whatever box is left.

### Scenes are pulled from OBS

A page with `"autoScenes": true` fills its free cells from OBS's live scene
list, in the order OBS displays them. Nothing hardcodes scene names, so the
deck follows whatever collection is loaded and updates when scenes are added,
removed, or renamed. Explicit buttons always keep their cell; scenes flow into
what is left.

Generated tiles are not editable, because they mirror OBS and any edit would
be overwritten on the next update.

**To hide them**, tap the camcorder switch that appears next to the pencil in
edit mode. It is struck through while the tiles are hidden, and it applies to
the page you are on — each page carries its own `autoScenes`. The same switch
turns them on for a page that has never had them.

Hiding is not deleting: generated tiles come from OBS's live scene list and are
never written to `deck.json`, so nothing is lost and the switch flips straight
back — including any scene added in OBS while they were hidden. Buttons you
placed by hand are untouched either way. The switch is offered only in edit
mode, because on a page that is mostly generated tiles it empties the deck, and
that is not something to leave a thumb's width from the page tabs mid-service.

### Action types

| Type | Fields | Notes |
|---|---|---|
| `scene` | `scene` | Switch program scene. The staged preview scene is left where it is, including when the scene pressed is already on air — in studio mode OBS reaches a program change through preview, so the deck puts preview back afterwards |
| `preview` | `scene` | Set preview scene (studio mode) |
| `transition` | — | Studio mode cut; no-op otherwise |
| `crossfade` | `percent` | Hold the studio-mode T-bar `percent` of the way from program to preview, mixing the two on air. The mix stays up until something else ends it: another `crossfade`, a `transition`, or a `scene`/`preview` button, each of which drops back to program first because OBS refuses those requests while it is holding a mix. 0 returns to program only and 100 completes the swap. If the current transition cannot be held part way (Cut, Stinger) OBS substitutes Fade for the duration. No-op outside studio mode |
| `source` | `scene`, `source` | Toggle source visibility |
| `mute` | `input` | Toggle an audio input |
| `stream` / `record` | — | Toggle streaming / recording |
| `recordPause` | — | Pause / resume recording |
| `replay` | — | Save replay buffer |
| `hotkey` | `hotkey` | **Any** OBS keybind, including plugin hotkeys |
| `ptzPreset` | `preset` | Recall a PTZ preset |
| `page` | `page` | Jump to another deck page |
| `chain` | `steps` | Run several actions in order — see [Chaining actions](#chaining-actions) |

`hotkey` is the escape hatch that makes every bindable OBS action reachable.
Prefer a native action where one exists — obs-websocket documents hotkey
support as best-effort and does not guarantee it.

### Chaining actions

One tile can run several actions in order. In the editor, the **Steps** strip
above the action picker starts at `1 +`; tap `+` and the fields below edit the
new step instead. A button with one step is an ordinary button, one with several
is a chain — you never pick "chain" from a list, an existing button just grows
into one, and deleting back down to a single step makes it ordinary again.

The case that wants this most is taking a camera through a partial mix:

```json
{ "row": 0, "col": 0, "label": "Take Cam 2",
  "action": { "type": "chain", "steps": [
    { "type": "preview",   "scene": "Cam 2" },
    { "type": "crossfade", "percent": 50 }
  ] } }
```

Steps run on the server, one after another, and each waits for OBS to answer
before the next is sent — so a `preview` step is in place by the time the
`crossfade` after it mixes toward it. A chain **stops at the first failure**
and reports which step it was: later steps usually depend on earlier ones, and
half a chain is easier to recover from than a chain that carried on past a
broken step.

Two things a chain cannot do:

- **A step cannot be a chain.** Nesting buys nothing a flat list cannot say, and
  a hand-edited `deck.json` pointing a chain into itself would otherwise recurse
  until the stack gives out. Twelve steps is the ceiling.
- **The press queues behind itself.** A chain runs on the read loop of the phone
  that pressed it, so that phone's *next* message waits for the chain to
  finish. That is the right order for a sequence of presses, but it is why the
  delays below are capped.

#### Waiting between steps

Steps after the first offer a **Wait before this step** in milliseconds. Most
chains need none: every request already waits for its own reply. The exception
is a transition — OBS reports one as done the moment it *starts*, not when it
finishes — so a step that must land after a 300 ms transition needs to be told
to wait for it.

Waits are clamped: 2 s per step, and 5 s across a whole chain, however they are
spread. A `page` step is taken by the phone rather than the server, and applied
at once rather than in sequence — moving the deck changes nothing on air, so
where in the order it happens cannot be seen.

A chained button lights up for its first step that has a lit state, so a
"preview then crossfade" tile still reads as the camera it takes. PTZ presets
take no chain: the preset strip has nowhere to show the steps.

### Scene-gated buttons

A button can be switched off in scenes it does not belong to. In the editor,
**Available in** offers *Only the scenes below* or *Every scene except those
below*, then a chip per scene.

Outside its scenes the tile dims, drains of colour and does nothing when
pressed — it keeps its slot, because a deck whose tiles move around as scenes
change cannot be learned by muscle memory. Editing still reaches it: the pencil
lifts the dimming so a gated button can be changed from any scene.

```json
{ "row": 0, "col": 1, "label": "Lyrics",
  "action": { "type": "source", "scene": "Worship", "source": "Lyrics" },
  "enable": { "scenes": ["Worship", "Sermon"] } }
```

`"invert": true` reads the list the other way — every scene *except* these.
That is usually the shorter list, and it keeps working when a scene is added in
OBS. An empty list is no gate at all rather than a gate that closes everywhere.

The rule is checked twice. The phone dims the tile and refuses the tap, which is
where the operator sees it; the server re-checks the button's own rule against
the scene it knows is on air, so a press sent moments after a scene change does
not land on the wrong scene. Presses whose button cannot be found — generated
`autoScenes` tiles, or a button deleted underneath a phone that has not
refreshed — are allowed through rather than blocked.

Gating matches the **program** scene, not preview. While OBS is disconnected
every gated button is off: not knowing which scene is on air is no reason to
offer a control that might be wrong for it.

PTZ presets take no gate. The preset strip is drawn once per page rather than on
every OBS update, so a rule there would not follow the scene, and the editor
does not offer one.

### Feedback types

A button lights up when its feedback condition is true: `activeScene`,
`previewScene`, `streaming`, `recording`, `recordPaused`, `sourceVisible`,
`inputMuted`. Add `"invert": true` to flip it — that is how a *live* mic reads
as lit rather than a muted one.

While OBS is disconnected no feedback is applied, so an inverted condition
cannot read as lit on a dead link.

### PTZ page

A page with `"layout": "ptz"` renders the camera controls instead of a grid:

- **Pan/tilt stick** — proportional. A small deflection creeps, a full one
  slews. Both axes clamp to the unit circle, so a diagonal drag is not faster
  than a straight one.
- **Zoom rocker** — a spring-return slider across the bottom. Hold it right to
  zoom in, left to zoom out; it keeps zooming at a rate set by how far you
  pushed it, and snaps back to centre the moment you let go.
- **Preset strip** — buttons on this page. **Tap recalls, long-press stores**
  the camera's current position into that slot. A store asks first: the phone
  buzzes when the hold registers, then a confirm names the slot, because the
  overwrite cannot be undone — obs-ptz can neither read back what a slot held
  nor restore it.

Tune the ceilings with `panSpeed` / `tiltSpeed` / `zoomSpeed`. All three axes
travel in one `ptz_move_continuous` call, so a simultaneous pan and zoom is a
single request rather than two competing ones.

#### Preset slots and names

obs-ptz exposes `ptz_preset_recall` and `ptz_preset_save` and nothing else —
there is **no way to enumerate presets or read their names from the plugin**.
So slots and labels live in `deck.json` and are yours to assign. Tap the pencil
on the PTZ page to add, rename, recolour, or re-slot them; the numbers just
have to match how the camera itself is configured.

---

## Streaming to YouTube and previewing at the same time

OBS allows only one Stream output, so when Stream is committed to YouTube the
preview has to come out of the Recording slot. Set `video.mode` to `"mpegts"`
and obs-deck listens for an MPEG-TS push instead of a WHIP one:

```json
"video": { "mode": "mpegts", "tsListen": "127.0.0.1:9000", "streamPath": "preview" }
```

**Settings → Stream** — YouTube as normal.

**Settings → Output → Recording → Custom Output (FFmpeg)**

| Field | Value |
|---|---|
| FFmpeg Output Type | Output to URL |
| File path or URL | `udp://127.0.0.1:9000?pkt_size=1316` |
| Container Format | `mpegts` |
| Video Encoder | `libx264` (or `h264_nvenc`) |
| Rescale Output | checked, `640x360` |
| Video Bitrate | 1200 Kbps |
| Keyframe interval (frames) | 30 |
| Video Encoder Settings | `preset=veryfast tune=zerolatency bf=0` |
| Audio Encoder | `libopus` |
| Audio Bitrate | 64 |

Press **Start Streaming** for YouTube and **Start Recording** for the preview.
The two encoders are independent, so YouTube can have 1080p60 while the phone
gets 640x360.

#### OBS on a different machine

`tsListen` defaults to `127.0.0.1:9000`, which only accepts a push from the
machine obs-deck is running on. To take one from elsewhere on the LAN, bind
every interface:

```json
"video": { "mode": "mpegts", "tsListen": "0.0.0.0:9000" }
```

and point OBS's `File path or URL` at this machine instead of loopback. On a
box with several interfaces, which address the OBS machine can reach is not
always obvious, so obs-deck prints the usable ones at startup:

```
obs-deck listening on http://0.0.0.0:8080
  phone:  http://192.168.2.7:8080
  OBS MPEG-TS URL:  udp://192.168.2.7:9000?pkt_size=1316
  OBS MPEG-TS URL:  udp://172.16.100.16:9000?pkt_size=1316
```

Use `[::]:9000` instead if the push has to arrive over IPv6; `0.0.0.0` is
IPv4-only. The ingest has no authentication and no notion of who is allowed to
publish — see *Caveats*.

MPEG-TS carries H264 and Opus, which are exactly what WebRTC wants, so frames
are unwrapped and forwarded with **no transcoding**. `libopus` is not optional
— browsers cannot decode AAC over WebRTC, and obs-deck logs a clear warning if
it sees AAC.

This costs you disk recording, since Recording is now the preview. If you need
a local file as well, that is a third output: use `obs-multi-rtmp`, or
`video.mode: "external"` with MediaMTX.

### Why not ffmpeg's WHIP muxer?

It looks like the obvious answer — ffmpeg 7.1+ has a `whip` muxer, and on
Linux OBS links the system libavformat, so the muxer is present. It works from
the ffmpeg CLI. It does **not** work from OBS's Custom Output, which fails
with `Not yet implemented in FFmpeg, patches welcome`. The WHIP muxer is
`AVFMT_NOFILE` and manages its own connection, which OBS's FFmpeg wrapper does
not appear to accommodate. MPEG-TS avoids the whole problem.

---

## Troubleshooting a black preview

Check the server side first:

```sh
curl -s localhost:8080/api/health
```

`recording` tells you whether OBS's Recording output is actually running, and
`publishing` whether any media has reached obs-deck. If `recording` is true but
`publishing` is false, OBS is sending somewhere else — check the URL.
`droppedFrames` counts what the realtime guard has discarded; see *Staying
realtime* above.

obs-deck logs the elementary streams it sees, and names any codec WebRTC
cannot carry:

```
[media] preview: PMT pid=256 stream_type=0x02
[media] preview: video is MPEG-2, which WebRTC cannot carry.
        Set the recording video encoder to libx264 or h264_nvenc.
```

If nothing is logged at all, no packets are arriving. OBS's files on disk are
not authoritative while it is running — it flushes them only on certain
events — so read the live configuration instead:

```sh
OBSDECK_OBS=localhost:4455 go test ./internal/obsws/ -run TestDoctor -v
```

That dumps the active profile and every output's real settings map, including
the URL and the encoders actually in effect.

Two things that bite:

- **`preset`/`tune`/`bf` belong in Video Encoder Settings, not Muxer
  Settings.** In the muxer box they are silently ignored by the mpegts muxer,
  so you get none of the low-latency tuning.
- **Use `127.0.0.1`, not `localhost`.** obs-deck binds IPv4 loopback; if
  `localhost` resolves to `::1` first, the packets go somewhere nothing is
  listening and UDP reports no error.

## Caveats

- **Saving from the UI strips `//` comment keys from `deck.json`.** The file is
  rewritten from the parsed structure, so the inline documentation in the
  shipped default is lost the first time you edit a button on the phone. The
  comments in `settings.json` are safe — obs-deck never writes that file.
- **No authentication.** Anyone who can reach the port can drive OBS. This is
  a trusted-LAN tool; do not expose it to the internet. The same goes for the
  MPEG-TS ingest once `tsListen` is off loopback: whatever arrives on that
  port becomes the preview, and a second sender does not politely queue — it
  interleaves and desyncs the demuxer.
- **`ptz.profile` is best-effort across plugin versions.** The request names
  come from the installed plugin's own signatures:

  ```c
  void ptz_move_continuous(int device_id, float pan, float tilt, float zoom, float focus)
  void ptz_preset_recall(int device_id, int preset_id)
  void ptz_preset_save(int device_id, int preset_id)
  ```

  If a future obs-ptz renames one, fix it in `settings.json` — no rebuild.

## Safety behaviours worth knowing

A button gated to certain scenes is refused twice — dimmed and untappable on the
phone, and re-checked against live state on the server — so a stale page cannot
fire a control that is wrong for the scene on air. See
[Scene-gated buttons](#scene-gated-buttons).

A phone that loses WiFi mid-pan would otherwise leave the camera slewing until
it hits a hard limit. Three guards prevent that: a 700 ms deadman timer on the
server, a stop when the last phone disconnects, and a stop when the page is
backgrounded or navigated away from.

## Layout

```
main.go                  flags, embedded assets, wiring, shutdown
internal/config/         settings + deck structs, load, atomic save
internal/obsws/          client   connection lifecycle, reconnect
                         state    snapshot, event pump, resync
                         actions  scene/source/audio/output verbs
                         catalog  live inventory for the editor
internal/ptz/            obs-ptz vendor calls, speed scaling, deadman
internal/media/          media    WebRTC setup, routing, SDP helpers
                         whip     WHIP publisher ingest (OBS Stream)
                         whep     viewer egress (phones)
                         mpegts   MPEG-TS/UDP ingest (OBS Recording)
internal/server/         server   routing and wiring
                         api      REST endpoints
                         ws       socket hub and broadcast
                         actions  button action dispatch
web/                     index.html
  css/                   base, stage, deck, ptz, editor
  js/                    state    shared state and pub/sub
                         net      websocket transport
                         player   WHEP playback and stats
                         deck     tabs, grid, feedback
                         joystick stick, rocker, presets
                         editor   the button editor sheet
                         icons    generated from Lucide
scripts/gen-icons.mjs    regenerates the icon set
```

Modules publish and subscribe through `state.js` rather than importing each
other, which keeps the graph acyclic: `net` publishes, the rest react.

## Tests

```sh
go test ./...
```

`internal/media` covers the full WHIP-in/WHEP-out path with a real Pion
publisher and subscriber, so media forwarding is verified without OBS running.
The realtime guard is covered separately: lag measurement, keyframe detection,
and which frames a catch-up drops are all unit-tested against synthetic
timestamps, since a real backlog is not reproducible on demand.

There is also a live check that needs a running obs-deck and a real OBS. It
briefly switches the program scene and puts it back, so it is skipped unless
you opt in:

```sh
OBSDECK_LIVE=127.0.0.1:8080 go test ./internal/server/ -run TestLive -v
```
