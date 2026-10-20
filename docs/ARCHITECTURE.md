# FairPlay architecture

This document explains how FairPlay works and why it is built this way. For usage, see the [README](../README.md).

## 1. What it is

FairPlay is a single binary you run on your own computer. It serves a small web app, transcodes your video into adaptive chunks, streams them, and keeps every viewer on one global clock. Each friend gets a personal invite link and watches in any browser. Chat and emoji reactions float over the video. Nothing is deployed: when you stop FairPlay, the room ends.

## 2. Stack

| Part | Choice | Why |
|---|---|---|
| Server | Go, one static binary with the frontend embedded | About 8 MB of memory when idle. No runtime dependencies besides ffmpeg. |
| Frontend | Preact, TypeScript, Vite and hls.js | The app shell is 26 KB gzipped. hls.js adds adaptive bitrate and loads only on the player page. Safari and iOS can use native HLS. |
| Encoding | ffmpeg and ffprobe | Reads anything ffmpeg reads: MKV, MP4, AVI, MOV, WebM, HEVC, AV1, DTS, AC3, multiple audio tracks and subtitles. |
| Realtime | One WebSocket per viewer | Carries the clock, room state, chat, reactions and ping. No WebRTC and no TURN servers. |
| Internet | cloudflared quick tunnel by default, ngrok optional | Started and supervised by FairPlay, restarted with backoff if it drops. |
| Storage | In-memory room, invites in a small JSON file, transcode cache on disk | No database. |

## 3. Features

**Watching**
- A global timer: everyone plays the same frame at the same moment.
- Equal start: play is scheduled slightly in the future so everyone starts on the same tick.
- Personal pause, rewind and ±10 s skips. You drop into **Solo** and see how far behind the room you are.
- **Back to live** returns you to the room position in one tap.
- **Intermission**: a global pause with a countdown and a "+5 min" button.
- Adaptive quality per viewer, with up to three rungs between 1080p and 360p chosen from the source. Bandwidth picks the rung automatically, with a manual override.
- Audio track and subtitle selection. Subtitles come from the file or from a sidecar .srt, .vtt or .ass.
- Volume up to 200%, mute, fullscreen, picture-in-picture and keyboard shortcuts. Above 100% the element plays at full volume into a Web Audio gain node; the node is created on the first boost, which is a user gesture, because a media element wired to a suspended AudioContext is silent. iPhones and iPads stay at 100%, where Web Audio and the native HLS player don't mix.
- A screen lock, the button at the bottom right of the controls. While locked, play, pause, seeking and skipping are frozen (buttons, the seek bar, taps on the picture and their keys); the controls stay on screen, and volume, chat, reactions, fullscreen and the lock button keep working. The host's click on the picture never pauses the room in any case; it shows the controls, as a tap does on a phone.
- A playlist: queue files, reorder them, and move to the next. When a file ends, the next one loads and plays.

**Social**
- Floating chat in the bottom right. The panel is transparent and only the WhatsApp-style bubbles have a background. Four messages are visible, the oldest is faded, and the stack fades away when chat is quiet.
- A composer with a send button, and an emoji reaction bar beneath it.
- Reactions float up over the video for everyone, and are rate limited.
- Late joiners see the last 50 messages. A new message brings up only its own bubble (older ones stay hidden until the controls or the pointer bring the stack up), for its reading time (4 s plus 60 ms per character, 5–12 s) and whenever the controls are up; the text box, send button and reaction bar appear only under the pointer (or with the controls on a touchscreen); a reaction floats over the picture and never brings the chat up.
- A viewer list showing each name, status (live, solo, buffering, syncing) and ping. You can rename yourself there.

**Host**
- A host console designed in Figma (page "04 Host console concepts"): a banner with the film, its status, the tracks for everyone and the transport that moves the global timer; three cards under it, Library (folder, search, list, queue), People (invites, viewers and the lobby in one list, with a row menu for link, QR code, chat, control and removal) and Room (name and options); tabs for Show, People and Settings (sharing, room options, storage); Stop FairPlay and End room in the top bar's ⋯ menu; a footer with encoder, Wi-Fi address, internet state and version.
- Grant or revoke co-host control per viewer. Co-hosts can play, pause, seek and start intermissions.
- Hide or show chat for a specific viewer. A hidden viewer receives no chat messages and cannot post, but can still react. This is tied to their invite, so it sticks across reconnects.
- Kick, revoke an invite, lock the room, or end the room and revoke every link.

## 4. Architecture

```
┌──────────────── host computer ───────────────┐       ┌── internet ──┐
│ fairplay (Go)                                 │       │              │
│  /             embedded app                   │◄─────►│ cloudflared  │◄──► guests
│  /hls/{id}/…   HLS playlists and segments     │ :8321 │ or ngrok     │     (any browser)
│  /ws           room hub: clock, chat, ping    │       │ (TLS)        │
│  /api/*        join, lobby, invites, host     │       └──────────────┘
│  transcoder    ffmpeg jobs → cache/{key}/     │◄── Wi-Fi guests connect directly
└───────────────────────────────────────────────┘
```

| Package | Responsibility |
|---|---|
| `internal/server` | Routes, security headers, CSRF and origin checks, cookies and the bot shield |
| `internal/room` | The room clock as a pure state machine. Unit tested with exact timings |
| `internal/hub` | WebSocket fan-out, commands, co-hosts, chat visibility, the lobby feed and wait-for-everyone |
| `internal/auth` | Host key, invites, device binding, the lobby and sessions |
| `internal/media` | Library scan, ffprobe, the HLS ladder transcode, subtitles, cache and an allowlisted file server |
| `internal/tunnel` | The cloudflared and ngrok supervisor |
| `internal/eventlog` | The always-on JSON-lines event log of a showing: room state, viewer status, commands, encoder, HLS requests and player-side events (`fairplay logs`) |
| `web/` | The Preact app: player, sync engine, chat, host console and join page |

### Encoding pipeline

1. **Probe** with ffprobe for streams, codecs, duration, HDR and interlacing.
2. **Plan** up to three rungs by the picture's long side: 1920, 1280 and 854 px for 1080p, 720p and 480p. A 1920×800 film is therefore 1080p, and portrait videos are classed by their height. Rungs never upscale, and bitrate scales with the picture area. 4K sources top out at 1080p to keep the encoder light.
3. **Transcode** to fMP4 HLS in 4-second segments. Every rung is encoded with keyframes forced at the same 4-second marks, so quality switches are seamless. Interlaced sources are deinterlaced, and HDR sources are tonemapped to SDR when the ffmpeg build has zscale. Audio becomes stereo AAC, up to six tracks. FairPlay detects VAAPI (Linux, Intel/AMD: frames are uploaded once and every version is scaled on the GPU with `scale_vaapi`), NVENC, Quick Sync or VideoToolbox with a test encode and falls back to libx264. ffmpeg starts at the lowest CPU priority (`nice -n 19`, inherited by all its threads) with at most half the CPU threads, so the host's own playback stays smooth.
4. **Play while encoding.** ffmpeg writes the master playlist within seconds and grows the variant playlists as it goes. The room can start after about 12 seconds are encoded. If playback catches up to the encoder, the room pauses briefly with the reason shown, then resumes automatically.
5. **Cache** by file path, size, modification time and ladder version, so a second showing starts instantly and a new ladder prepares files again. The cache is capped at 40 GB by default (`-cache-max`), and the least recently used prepared videos are removed after each finished encode and at start. The video in use and the one still encoding are never removed.

**Track names** come from the language tag: English, Hindi, Indonesian, from an ISO 639 table of about 90 languages. Titles are kept only when they add information, such as SDH, Forced or Commentary. A title repeated on every track, or one that looks like a website or release group, is ignored. Duplicates are numbered.

**Track choice.** The host sets the room's audio, subtitles and quality. They reset when the video changes, and every player follows them. A viewer can override any of the three for their own screen. Auto quality is limited by the player's on-screen size times the device's pixel density, then by bandwidth, so phones settle at 720p and laptops at 1080p. The host's player is the exception: it streams from its own computer, so "auto" there means the best rung its screen can show, fixed, with no switching. hls.js's warm-up (a dip to 480p, then 720p, then 1080p in the first twelve seconds, whatever the bandwidth) made the browser's decoder reconfigure three times, and one of those switches broke the host's decoder.

### Event log

`internal/eventlog` writes one JSON object per line to `<data>/logs/fairplay-<date>-<time>.jsonl` for the whole run, buffered and flushed once a second. Nothing in the hot path waits for disk, and a failed write is ignored. Events: `start` and `stop`; `ws.join`, `ws.leave` and `ws.slow`; `room.state` on every broadcast; `cmd` and `host` for every command and console action; `chat`, `react` and `kick`; `media` for the encoder's progress and `encode.plan` with the exact ffmpeg command; `hls` for every file served, with size and time taken; `status` once a second per viewer (position against the room clock, drift, ping, jitter, buffering, seconds buffered ahead, buffered ranges, dropped frames, quality, bandwidth estimate, tab visibility); and `player.*` for what the browser saw, sent over the socket as `log` messages and rate limited per connection: `video.*` element events, `hls.*` (level loads, fragment appends, slow pieces, warnings, errors), `sync.*` (holds, nudges, hard seeks, learned seek cost) and `user.*` (every click). `fairplay logs` lists the files with some `jq` filters, and the 30 newest are kept.

Only the exact file names ffmpeg produces can be served, enforced by an allowlist regex. Guests can only reach the file the room is currently showing.

### Animated reactions

Reactions are [Noto Emoji Animation](https://googlefonts.github.io/noto-emoji-animation/), under CC BY 4.0. `tools/emoji-index` builds `internal/emoji/index.json`, which lists 611 animated emoji with skin-tone variants left out. It also converts the 12 most used into small animated WebP files that are bundled into the binary, 648 KB in total.

When someone sends any other emoji, the host's FairPlay downloads its GIF from Google's emoji CDN once. It converts it with ffmpeg (96 px, 15 fps) and caches it in `<cache>/emoji`. Only one download runs per emoji, and at most two at once. Guests only ever fetch from the host. The server accepts only codes from the index, so `/emoji/<code>.webp` can't be used to fetch anything else.

In the player, the plain emoji shows at once and the animation replaces it as soon as it loads, so an offline host still shows reactions. Each reaction pops in with a springy bounce and a burst of mini emoji, then drifts up over the chat. Only transform and opacity animate, and at most 10 run at once.

## 5. Ping and sync

The server is the clock. Clients never trust their own wall time.

**Measuring ping.** Each viewer sends a ping every 2 s. The server answers with its own timestamp. The client keeps the last 12 samples.
- **Ping** is the median round trip. It is shown next to every name and in the viewer's own badge.
- **Clock offset** comes from the sample with the lowest round trip, because network queues only ever make a sample slower. This removes ping from the sync maths: a viewer at 300 ms ping computes the same room position as one at 5 ms.
- **Jitter** is the spread between the quartiles. High jitter widens the drift tolerance so a shaky link is not corrected constantly.

**The room state** is one anchor: at server time `anchor`, the video is at `pos` seconds and advances at `rate` while playing. Every viewer derives the same position from that anchor, so the server never streams time ticks.

**Viewers never get ahead of the room.** Seeking and skipping are capped at the room position, where encoded chunks are known to exist. A solo viewer who catches up rejoins the room automatically, and the seek bar shades the part they can't reach yet.

**Following the room.** Every 250 ms the player compares its playhead to the room position.
- Below the tolerance (80 ms, more on jittery links): nothing happens.
- Up to 1.5 s off: playback rate is nudged between 0.95× and 1.05× until it converges. Nobody hears or sees this.
- Over 1.5 s off: hard seek. The player learns how long its seeks take and aims ahead by that much.

**Equal start.** Play and seek-while-playing are scheduled ahead by `clamp(2 × worst ping + 400 ms, 700 ms, 3 s)`, where the worst ping comes from viewers following the room and is capped at 1.2 s. Every viewer gets the message, buffers, and starts on the same tick.

**Protecting the experience.**
- If one viewer stalls, only that viewer catches up when their buffer refills, and the room keeps playing. The host can turn on "wait for everyone", which pauses the room after 0.7 s of anyone's stall and resumes 0.5 s after everyone is ready.
- Adaptive bitrate drops a slow viewer to a lower rung before they stall.
- A pause the browser makes on its own is never taken for the viewer's. A failing decoder pauses the element, and so does hls.js while it rebuilds the media source afterwards; the player recognises both (an error on the element, or a recovery within the last five seconds) and leaves the room alone. Before this, a decoder failure on the host's computer paused the whole room.
- Ping turns amber at 120 ms and red at 300 ms. The viewer stack shows "slow link" when anyone is above 300 ms.
- Reconnects resume automatically. After a FairPlay restart, a guest tab silently rejoins with its stored invite, because the device was already admitted.

**Measured:** two viewers stayed 14–25 ms apart. After Back to live, a viewer converged from 170 ms to 14 ms within a few seconds.

## 6. Authentication and forwarded links

No link can prove *who* opened it. FairPlay uses three layers that honest guests barely notice.

**Layer 1: personal, single-device invites.** Each invite is for one named person. The 128-bit token sits in the URL fragment, which browsers never send to servers, and the address bar drops it as soon as the page loads. FairPlay stores only a SHA-256 hash of it. The first browser that joins receives a device key in an HttpOnly cookie, which page scripts cannot read. After that the invite skips the lobby only on that device. Expiry defaults to 24 hours, and the host can choose 6 hours to 7 days.

**Layer 2: the lobby.** Every new device waits in the lobby. The host sees the invite label, the name typed, the device ("Android · Chrome") and the IP, then admits or denies. A forwarded or reused link is flagged: "Priya's link opened on a second device". Admitting moves the seat to the new device. Denying a first-time opener unbinds the link so the real guest is not locked out. The host can turn on auto-admit for first-time opens, but second devices always need approval.

**Layer 3: room controls.** Kick, revoke, regenerate a link, lock the room and end the room take effect instantly. They cut the session's socket, its media access and its cookie.

**Other protections.**
- **Host key.** It is 192-bit random, regenerated on every start, shown only in the terminal, and exchanged for an HttpOnly host cookie.
- **Sessions.** Cookies are HttpOnly and SameSite=Strict, and marked Secure behind the tunnel. Every `/hls`, `/ws` and `/api` call checks the session.
- **CSRF.** Every write must carry a matching `Origin` and a JSON content type. Bodies are capped at 8 KB.
- **WebSocket.** The origin is checked, messages are capped at 32 KB, and messages are decoded into typed fields. Unknown types are ignored. Chat is limited to a burst of 5 then 1/s, and reactions to a burst of 6 then 3/s.
- **Input.** Names are cleaned to 1–24 printable characters and messages to 200. Everything renders as text, never as HTML.
- **Headers.** A strict CSP with no inline scripts and `frame-ancestors 'none'`, plus nosniff, `Referrer-Policy: no-referrer`, a permissions policy and HSTS behind TLS.
- **Filesystem.** Media is served only from the chosen folder. Symlinks are resolved and must stay inside it, and there is no directory listing.

## 7. Bot and spam shield

The public tunnel URL is reachable by anyone, so every expensive or secret-checking endpoint is shielded. There is no CAPTCHA and no third-party service.

- **Proof of work.** Join, peek and host login need `sha256(salt:nonce)` to start with 16 zero bits. That is about 65,000 hashes: around 16 ms on a laptop and a fraction of a second on a phone. The puzzle is solved in a Web Worker while the guest types. Each recent strike from an IP adds 2 bits, up to 22, so a bot's cost grows fast. Challenges are single use and expire after 2 minutes.
- **Strikes.** Wrong tokens, wrong host keys, forged proofs and unadmitted socket attempts count as strikes. Five within 10 minutes ban the IP from the API for 15 minutes.
- **Rate limits.** Per IP the limit is a burst of 10, then 1 every 3 s. Globally it is a burst of 30, then 1 per second.
- **Lobby cap.** At most 10 join requests wait at once.
- **Quiet surface.** Unknown paths return 404, `robots.txt` disallows everything, and `X-Robots-Tag: noindex` is set. Header-read and idle timeouts stop slow-request attacks.
- **Real IPs.** `CF-Connecting-IP` and `X-Forwarded-For` are trusted only on loopback connections, which is where the tunnel connects.

## 8. Install and updates

**Installers.** `install.sh` (Linux, macOS) and `install.ps1` (Windows) find the latest release from the `github.com/<repo>/releases/latest` redirect, which has no API rate limit. They download the binary for the OS and CPU, check it against the release's `SHA256SUMS`, and install it for the current user without admin rights. If ffmpeg or cloudflared is missing, they offer to install it with the system package manager, Homebrew, winget, or Cloudflare's own release download. Both have an uninstall mode, with an optional purge of data.

**Releases.** Pushing a `v*` tag runs `.github/workflows/release.yml`. It tests, builds six binaries (Linux, macOS and Windows, each on amd64 and arm64), writes `SHA256SUMS`, signs it with the Ed25519 key in the `FAIRPLAY_SIGNING_KEY` secret, checks the signature against the public key compiled into the app, and publishes the GitHub release.

**Auto-update** lives in `internal/update`. On start, a release build:

1. Checks for a newer tag, with a 4-second timeout. Offline starts are not delayed.
2. Downloads `SHA256SUMS` and its signature, and verifies the signature with the built-in public key.
3. Downloads the binary, checks its SHA-256, and swaps it in atomically. A running `.exe` on Windows is renamed aside and cleaned up next start.
4. Restarts into the new version, with `exec` on Unix or a child process on Windows, before the room opens.

It never installs while a room is running. A long-running host is told about new releases in the console, checked every 6 hours. `-update check` only reports, and `-update off` disables checking.

**First run.** The video folder comes from `-media`, then `$FAIRPLAY_MEDIA`, then the folder last picked in the console (`settings.json`), then the first of `~/Videos`, `~/Movies` and `~/Downloads` that exists. If none does, `~/Videos` is created. The host console opens in the default browser. External tools are found on `PATH` and in common install folders: the binary's own folder, `~/.local/bin`, Homebrew, and winget's links. Missing tools produce the exact install command for the OS.

## 9. Performance

| Measure | Value |
|---|---|
| Server memory, idle | 8 MB |
| Server memory after 600 requests | 11 MB |
| Binary size | 7 MB, frontend included |
| Frontend gzipped | 26 KB app plus 5 KB CSS. hls.js (123 KB) loads only on the player page |
| Per viewer on the server | One goroutine pair and a 256-message send buffer. Segments stream from disk |
| Transcode, three rungs plus two audio tracks, libx264 | 1.5–2.8× realtime on a 16-thread CPU |

Over the internet every viewer pulls their own stream, so the host's upload speed is the ceiling. The console shows the estimate, and adaptive bitrate keeps weaker viewers watching at lower quality instead of stalling.

## 10. Design

The design was made in Figma before any code, and the colours come from the logo (a clapperboard with a coral bolt, traced into inline SVG in `web/src/logo.ts`): a near-black canvas, white text, the bolt's coral (`#f85d5b`) for the brand and primary actions, green for live, amber kept for "behind", "paused" and warnings, and crimson for destructive actions. Confirmations use FairPlay's own themed dialog (`web/src/components/Dialog.tsx`) instead of the browser's. Design tokens live in `web/src/styles.css`. Screens:

| Live | Solo, behind the room |
|---|---|
| ![Live](screens/player-live.png) | ![Behind](screens/player-behind.png) |

| Host console | Phone |
|---|---|
| ![Host console](screens/host-console.png) | ![Mobile](screens/mobile.png) |

## 11. Status

Every phase of the original plan is complete.

| Phase | Status |
|---|---|
| Scaffold: Go server, embedded app, host key | Done |
| Room clock, WebSocket hub, ping and sync engine | Done. Unit tests and a two-browser measurement |
| Player, controls, chat and reactions matching the design | Done. Checked in the browser on desktop and phone |
| Invites, device binding, lobby, sessions, hardening | Done. End-to-end tests and a forwarded link over the real tunnel |
| Media: probe, HLS ladder, subtitles, audio tracks, cache | Done. Real-encode test |
| Tunnel supervisor, host console, playlist | Done. cloudflared checked live |
| Tests, multi-browser sync, security pass | Done. Race detector clean, staticcheck clean |
| Installers, signed auto-update, first-run defaults | Done. Update tested end to end against a local release server, including tampered and forged releases |
| GPU encoding (VAAPI), low-priority ffmpeg, host playback on the encoding computer | Done. Measured on an Intel Iris Xe laptop: about two CPU cores for a 1080p film, no stall on the host or a guest |
| Event log, screen lock, host decoder recovery, fixed host quality | Done. Reproduced a host stall across three browsers with the log, and verified the lock in the browser |

Known limits:
- Picture-based subtitles (PGS, VobSub) cannot be shown.
- NVENC, Quick Sync and VideoToolbox are detected but were only exercised on machines without them, where VAAPI or libx264 was used.
- The ngrok path is implemented but was not run live.
