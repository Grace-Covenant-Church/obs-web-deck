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

# Only needed if you want the QR code to open in a browser window
sudo apt install xdg-utils
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

It prints a QR code and the LAN address, and opens that page in a browser.

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
ExecStart=%h/.local/bin/obs-web-deck -config %h/.config/obs-web-deck -no-window
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now obs-web-deck
systemctl --user status obs-web-deck
journalctl --user -u obs-web-deck -f
```

`-no-window` keeps it from opening a browser on every login; the QR page is
always at `http://<this-machine>:8080/qr`. Drop the flag if you want the window.

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
./obs-web-deck -no-window                         # do not open a browser
./obs-web-deck -no-qr                             # no QR in the terminal
```

On startup it prints a scannable QR code and the LAN address, and opens the
same page in a browser window. The address comes from the routing table rather
than the first interface found, so Docker bridges and VPNs do not get picked
by mistake.

### Showing the QR inside OBS

Add a **Browser Source** pointing at:

```
http://127.0.0.1:8080/qr?overlay=1
```

640x360 is about right. That variant has a transparent background and no page
chrome, so it composites cleanly into a corner of a scene - handy for a
volunteer who needs to get their phone on the deck without being told the IP.

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
| Keyframe interval | **1s** | The default 2s is how long a joining viewer waits |
| B-frames | **0** | B-frames buffer future frames by definition |
| Tune / preset | `zerolatency` (x264) or `P1` low-latency (NVENC) | |
| Rate control | CBR | Avoids VBV bursts the jitter buffer must absorb |
| Bitrate | 2–4 Mbps at 720p | A phone gains nothing above this, and less buffering means less delay |

### 4. Open it on the phone

Browse to the address obs-deck prints, e.g. `http://192.168.1.113:8080`.
Add to home screen for a full-screen, chrome-free surface.

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
be overwritten on the next update. Set `autoScenes: false` to place every
scene by hand instead.

### Action types

| Type | Fields | Notes |
|---|---|---|
| `scene` | `scene` | Switch program scene |
| `preview` | `scene` | Set preview scene (studio mode) |
| `transition` | — | Studio mode cut; no-op otherwise |
| `source` | `scene`, `source` | Toggle source visibility |
| `mute` | `input` | Toggle an audio input |
| `stream` / `record` | — | Toggle streaming / recording |
| `recordPause` | — | Pause / resume recording |
| `replay` | — | Save replay buffer |
| `hotkey` | `hotkey` | **Any** OBS keybind, including plugin hotkeys |
| `ptzPreset` | `preset` | Recall a PTZ preset |
| `page` | `page` | Jump to another deck page |

`hotkey` is the escape hatch that makes every bindable OBS action reachable.
Prefer a native action where one exists — obs-websocket documents hotkey
support as best-effort and does not guarantee it.

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
  the camera's current position into that slot.

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
| Video Bitrate | 2500 Kbps |
| Keyframe interval (frames) | 30 |
| Video Encoder Settings | `preset=veryfast tune=zerolatency bf=0` |
| Audio Encoder | `libopus` |
| Audio Bitrate | 64 |

Press **Start Streaming** for YouTube and **Start Recording** for the preview.
The two encoders are independent, so YouTube can have 1080p60 while the phone
gets 720p.

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
  a trusted-LAN tool; do not expose it to the internet.
- **`ptz.profile` is best-effort across plugin versions.** The request names
  come from the installed plugin's own signatures:

  ```c
  void ptz_move_continuous(int device_id, float pan, float tilt, float zoom, float focus)
  void ptz_preset_recall(int device_id, int preset_id)
  void ptz_preset_save(int device_id, int preset_id)
  ```

  If a future obs-ptz renames one, fix it in `settings.json` — no rebuild.

## Safety behaviours worth knowing

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

There is also a live check that needs a running obs-deck and a real OBS. It
briefly switches the program scene and puts it back, so it is skipped unless
you opt in:

```sh
OBSDECK_LIVE=127.0.0.1:8080 go test ./internal/server/ -run TestLive -v
```
