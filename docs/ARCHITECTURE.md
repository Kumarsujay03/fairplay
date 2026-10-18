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
- Volume, mute, fullscreen, picture-in-picture and keyboard shortcuts.
- A playlist: queue files, reorder them, and move to the next. When a file ends, the next one loads and plays.

**Social**
- Floating chat in the bottom right. The panel is transparent and only the WhatsApp-style bubbles have a background. Four messages are visible, the oldest is faded, and the stack fades away when chat is quiet.
- A composer with a send button, and an emoji reaction bar beneath it.
- Reactions float up over the video for everyone, and are rate limited.
- Late joiners see the last 50 messages.
- A viewer list showing each name, status (live, solo, buffering, syncing) and ping. You can rename yourself there.

**Host**
- A host console with the file library, transcode progress, how far the encoder is ahead of the room, invites, the lobby, room settings, tunnel status, room-wide ping and an upload estimate.
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
| `web/` | The Preact app: player, sync engine, chat, host console and join page |

### Encoding pipeline

1. **Probe** with ffprobe for streams, codecs, duration, HDR and interlacing.
2. **Plan** up to three rungs at or below the source height. 4K sources top out at 1080p to keep the encoder light.
3. **Transcode** to fMP4 HLS in 4-second segments. Every rung is encoded with keyframes forced at the same 4-second marks, so quality switches are seamless. Interlaced sources are deinterlaced, and HDR sources are tonemapped to SDR when the ffmpeg build has zscale. Audio becomes stereo AAC, up to six tracks. FairPlay detects NVENC, Quick Sync or VideoToolbox with a test encode and falls back to libx264 `veryfast`. ffmpeg runs at lower priority so the host's own playback stays smooth.
4. **Play while encoding.** ffmpeg writes the master playlist within seconds and grows the variant playlists as it goes. The room can start after about 12 seconds are encoded. If playback catches up to the encoder, the room pauses briefly with the reason shown, then resumes automatically.
5. **Cache** by file path, size and modification time, so a second showing starts instantly.

Only the exact file names ffmpeg produces can be served, enforced by an allowlist regex. Guests can only reach the file the room is currently showing.

## 5. Ping and sync

The server is the clock. Clients never trust their own wall time.

**Measuring ping.** Each viewer sends a ping every 2 s. The server answers with its own timestamp. The client keeps the last 12 samples.
- **Ping** is the median round trip. It is shown next to every name and in the viewer's own badge.
- **Clock offset** comes from the sample with the lowest round trip, because network queues only ever make a sample slower. This removes ping from the sync maths: a viewer at 300 ms ping computes the same room position as one at 5 ms.
- **Jitter** is the spread between the quartiles. High jitter widens the drift tolerance so a shaky link is not corrected constantly.

**The room state** is one anchor: at server time `anchor`, the video is at `pos` seconds and advances at `rate` while playing. Every viewer derives the same position from that anchor, so the server never streams time ticks.

**Following the room.** Every 250 ms the player compares its playhead to the room position.
- Below the tolerance (80 ms, more on jittery links): nothing happens.
- Up to 1.5 s off: playback rate is nudged between 0.95× and 1.05× until it converges. Nobody hears or sees this.
- Over 1.5 s off: hard seek. The player learns how long its seeks take and aims ahead by that much.

**Equal start.** Play and seek-while-playing are scheduled ahead by `clamp(2 × worst ping + 400 ms, 700 ms, 3 s)`, where the worst ping comes from viewers following the room and is capped at 1.2 s. Every viewer gets the message, buffers, and starts on the same tick.

**Protecting the experience.**
- If one viewer stalls, only that viewer catches up when their buffer refills, and the room keeps playing. The host can turn on "wait for everyone", which pauses the room after 0.7 s of anyone's stall and resumes 0.5 s after everyone is ready.
- Adaptive bitrate drops a slow viewer to a lower rung before they stall.
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
- **Input.** Names are cleaned to 1–24 printable characters and messages to 500. Everything renders as text, never as HTML.
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

The design was made in Figma before any code. The theme is dark cinema: a near-black canvas, a warm amber accent like projector light, green for live, and glass overlays on the video. Design tokens live in `web/src/styles.css` and match the Figma variables. Screens:

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

Known limits:
- Picture-based subtitles (PGS, VobSub) cannot be shown.
- Hardware encoders are detected but were only exercised on machines without them, where libx264 was used.
- The ngrok path is implemented but was not run live.
