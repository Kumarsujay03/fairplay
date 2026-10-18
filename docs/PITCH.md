# FairPlay — watch together, perfectly in sync

*Pitch v2, approved with changes on 2026-10-18. Built and verified the same day; see §10.*

Figma: https://www.figma.com/design/SOHOZXlUiNsUnSdzDreUB4 (pages: 01 Foundations, 02 Components, 03 Screens)

## Changes from v1 of this pitch

| Your note | What changed |
|---|---|
| Name | **FairPlay** |
| Go + Preact | Kept |
| cloudflared default, ngrok optional | Kept |
| "Complete, with all features and encoding" | No v1/v2 split. Full ffmpeg transcoding to an adaptive HLS ladder ships in the first build. Every feature in this doc is in scope. |
| Host-only control | Default. Host can grant "co-host" control to any viewer and revoke it any time. |
| Ping, so live stays smooth | New §5: per-viewer ping measured continuously, shown in the UI, and fed into the sync math. |
| Forwarded links | New §6: device-bound invites plus a host-approval lobby. A forwarded link cannot get a stranger in without you clicking Admit. |
| Bots and spam on the public URL | New §6a: proof-of-work check, strike-and-ban, rate limits and a lobby flood cap. |

---

## 1. What it is

A single binary you run on your laptop. It serves a tiny web app, transcodes your video into adaptive chunks, streams them, and keeps every viewer on one global clock. Each friend gets a personal invite link. They open it in any browser, confirm their name, and watch with you. Chat and emoji reactions float over the video. Nothing is deployed; when you close FairPlay the room and every link die.

## 2. Stack

| Part | Choice | Why |
|---|---|---|
| Server | Go, one static binary, frontend embedded | ~20 MB RAM, no runtime deps. |
| Frontend | Preact + TypeScript (Vite) and hls.js | Preact is 4 KB. hls.js gives YouTube-style adaptive bitrate. Safari and iOS use native HLS. |
| Encoding | ffmpeg (bundled path or system) | Any input ffmpeg reads: MKV, MP4, AVI, MOV, WebM, HEVC, AV1, DTS, AC3, multi-audio, subtitles. |
| Realtime | One WebSocket per viewer | Clock, state, chat, reactions, ping. No WebRTC, no TURN. |
| Internet | cloudflared quick tunnel (default), ngrok (optional) | Spawned and supervised by FairPlay. |
| Storage | In-memory room, invites in a small JSON file, transcode cache on disk | No database. |

## 3. Features (all in the first build)

**Watching**
- Global timer: everyone plays the same frame at the same moment.
- Equal start: play is scheduled slightly in the future so everyone starts on the same tick.
- Personal pause, rewind, skip ±10 s. You drop into **Solo** and see how far behind you are.
- **Back to live** returns you to the room position in one tap.
- **Intermission**: global pause with a countdown and a "+5 min" button.
- Adaptive quality per viewer (1080p, 720p, 480p, 360p), chosen automatically by bandwidth, with a manual override.
- Audio track and subtitle selection. Subtitles are extracted from the file or loaded from a sidecar .srt or .vtt.
- Volume, mute, fullscreen, picture-in-picture, keyboard shortcuts.
- Playlist: queue several files, host moves to next.

**Social**
- Floating chat bottom-right. Transparent panel, WhatsApp-style bubbles, 3–4 visible, older ones fade.
- Composer with send icon, emoji reaction bar beneath.
- Reactions float up over the video for everyone, rate limited.
- Late joiners see the last 50 messages.
- Viewer list with name, status (live, solo, buffering) and ping.

**Host**
- Host console: file picker, transcode progress, invites, lobby, room toggles, tunnel status, room-wide ping.
- Grant or revoke co-host control per viewer.
- Kick, revoke invite, lock room, end room.
- Bandwidth estimate before the show.

## 4. Architecture

```
┌──────────────── host laptop ─────────────────┐       ┌── internet ──┐
│ fairplay (Go)                                 │       │              │
│  /             embedded SPA                   │◄─────►│ cloudflared  │◄──► guests
│  /hls/{id}/…   HLS playlists + segments       │ :8321 │ or ngrok     │     (any browser)
│  /ws           room hub (clock, chat, ping)   │       │ (TLS)        │
│  /api/*        join, lobby, invites, host     │       └──────────────┘
│  transcoder    ffmpeg jobs → cache/{hash}/    │◄── LAN guests connect directly
└───────────────────────────────────────────────┘
```

Server packages: `server` (routes, headers, auth middleware), `room` (state machine), `hub` (WebSocket fan-out), `media` (probe, transcode, HLS serving, path guard), `auth` (host key, invites, sessions, lobby), `tunnel` (cloudflared and ngrok supervisor).

### Encoding pipeline

1. **Probe** with ffprobe: streams, codecs, duration, bitrate, subtitles.
2. **Plan** a ladder capped at the source resolution. If the source video is already H.264, the top rung is copied rather than re-encoded, which is near instant.
3. **Transcode** to fMP4 HLS, 4-second segments with aligned keyframes so quality switches are seamless. Hardware encoders are used when present (NVENC, VAAPI, QSV), libx264 `veryfast` otherwise.
4. **Play while encoding.** Playlists are written as they grow, so the room can start after the first segments of every rung exist. The host console shows how far ahead of the room the encoder is.
5. **Cache** by file hash, so a second showing of the same file starts instantly.

## 5. Ping and sync

The server is the clock. Clients never trust their own wall time.

**Measuring ping.** Every viewer sends a ping every 2 s. The server answers with its own timestamp. The client keeps the last 10 samples.
- **Ping** is the median round-trip time, shown next to each name and in the viewer's own badge.
- **Clock offset** comes from the sample with the lowest round trip, which is the one least distorted by network queues. This is what removes ping from the sync math: a viewer at 300 ms ping still computes the same room position as a viewer at 5 ms.
- **Jitter** is the spread of the samples. High jitter widens the drift tolerance so the player is not constantly corrected.

**Following the room.** Every 250 ms the client compares its playhead to the target position.
- Under 80 ms of drift: nothing happens.
- 80 ms to 1.5 s: playback rate is nudged between 0.95× and 1.05× until it converges. It is inaudible and has no visible jump.
- Over 1.5 s: hard seek to the target.

**Equal start.** When the host presses play, the start time is set to `now + max(1 s, 2 × worst ping + buffer margin)`. Every viewer gets the message, preloads, and starts on the same tick.

**Protecting the experience.**
- If one viewer stalls, by default only that viewer catches up when their buffer refills; the room keeps playing. A host toggle, "wait for everyone", turns a stall into a short global pause instead.
- Adaptive bitrate drops a slow viewer to a lower rung before they stall.
- A viewer whose ping rises above 400 ms or whose connection drops gets a warning badge, and the host sees it in the viewer list.
- Reconnects resume automatically and re-sync within one ping cycle.

**Target.** Viewers in Follow mode stay within about 80 ms of each other, which is below what anyone notices while chatting.

## 6. Authentication and forwarded links

No link can prove *who* opened it. Anyone holding a URL can open it. So FairPlay uses three layers. Each one is invisible to an honest guest, and together they stop a forwarded link.

**Layer 1: personal, single-device invites.** Each invite is for one named person. The secret token is 128-bit random, sits in the URL fragment so it never reaches any server or tunnel logs, and is stored on your machine only as a hash. The first browser that uses it receives a device key, kept in an HttpOnly cookie and in browser storage. After that, the token works **only** on that device. Expiry defaults to 24 hours.

**Layer 2: the lobby.** Every join waits in a lobby until you admit it. You see the invite label, the name they typed, the device ("Android · Chrome") and the network they came from. You click **Admit** or **Deny**. If Priya forwarded her link and a stranger opens it first, you see a join that does not look like Priya, and you deny it. If a second device tries Priya's already-used link, you see a flagged warning: "Priya's link opened on a second device". The host can switch to auto-admit for trusted links if the lobby feels like too much effort.

**Layer 3: room controls.** Lock room, kick, revoke invite, and end room all take effect instantly. Kicked or revoked sessions are cut from the WebSocket and from the media endpoints.

**Ease for viewers:** tap link, name is prefilled from the invite, tap Join, wait a second for the host to admit. On later visits the same device rejoins without the lobby.

**Everything else:**
- **Host key.** It is 128-bit, shown only in your terminal, and exchanged for an HttpOnly host session cookie.
- **Sessions.** Cookies are HttpOnly, SameSite=Strict, and Secure behind the tunnel. Every `/hls`, `/ws` and `/api` call is checked.
- **WebSocket.** The Origin is checked, messages are capped at 32 KB, rate limits are applied per user, and JSON is schema-validated.
- **Input.** Names are 1–24 characters and messages up to 500. Everything is rendered as text, never as HTML.
- **Headers.** The CSP is strict with no inline scripts. Frames, sniffing and referrers are all denied.
- **Filesystem.** Media comes only from the folder you pass on the command line. Paths are cleaned, symlinks are resolved and re-checked, and there is no directory listing.
- **Abuse limits.** Joins are rate limited per IP. The room has a size cap that defaults to 12.

## 6a. Bot and spam shield

The public tunnel URL is reachable by anyone, so every expensive or secret-checking endpoint sits behind a shield. None of it needs a CAPTCHA or a third-party service.

- **Proof of work.** Join, peek and host login need `sha256(salt:nonce)` to start with 16 zero bits. That takes about 0.2 s on a phone, solved in a Web Worker while the guest types a name. Each recent strike from the same IP adds 2 bits, up to 22, so a bot's cost grows fast.
- **Strikes.** A wrong token, a wrong host key, a forged proof or an unadmitted socket attempt each count as one. Five within 10 minutes ban the IP for 15 minutes.
- **Rate limits.** Per IP the limit is a burst of 10, then 1 every 3 s. Globally it is a burst of 30, then 1 per second.
- **Lobby cap.** At most 10 join requests wait at once.
- **Quiet surface.** Unknown paths return 404, `robots.txt` disallows everything, pages carry `X-Robots-Tag: noindex`, and server timeouts stop slow-header attacks.
- **Real IPs.** Visitor IPs come from `CF-Connecting-IP` and `X-Forwarded-For`, trusted only when the request arrives from loopback, where the tunnel connects.

## 7. Performance budget

| Thing | Budget |
|---|---|
| Server memory, idle | under 25 MB |
| Frontend JS gzipped | 26 KB app shell. hls.js (123 KB) loads only on the player page, so the invite page stays tiny |
| Per viewer server memory | under 1 MB; segments stream from disk |
| Chat DOM | last 50 messages |
| Reactions | pooled nodes, CSS transforms only |

Over the internet every viewer pulls their own stream, so your upload speed is the ceiling. Adaptive bitrate keeps weaker viewers watching at a lower quality instead of stalling.

## 8. Design

The Figma file holds the design. The theme is dark cinema: a near-black canvas with a warm amber accent, green for live, and glass overlays over the video. The typeface is Inter.

- **Foundations** has the colour, spacing and radius tokens and the text styles.
- **Components** has the icons, Button, IconButton, SyncBadge, ChatBubble, ReactionChip, ChatInput, Input, ProgressBar, Avatar and ViewerStack.
- **Screens** has the host console, guest join, the player in its live, behind and intermission states, and a mobile layout.

## 9. Build order

1. Toolchain and scaffold: Go server, embedded Preact app, host key.
2. Room state, WebSocket hub, ping and the sync engine.
3. Player, controls, chat and reactions to match Figma.
4. Invites, device binding, lobby, sessions and hardening.
5. Media: probe, HLS ladder transcode, subtitles, audio tracks, cache.
6. Tunnel supervisor, host console and playlist.
7. Tests, a multi-browser sync check, and a security pass.

## 10. Results (2026-10-18)

Verified in a real browser against a 60 s 1080p MKV with two audio tracks and an embedded subtitle.

| Check | Result |
|---|---|
| Two viewers in sync | 14–25 ms apart |
| Back to live after a solo pause | Hard seek, then converged from 170 ms to 14 ms in a few seconds |
| Transcode, three rungs plus two audio tracks, libx264 | 1.5–2.8× realtime, playable after the first chunks |
| Forwarded link over the public tunnel | Held in the lobby, flagged as a second device, denied |
| Returning device after a FairPlay restart | Rejoins without the lobby, and the room tab reconnects by itself |
| Go tests | room, auth, media with a real encode, end-to-end server and shield tests, race detector clean |

Bugs found and fixed during verification:
- A new host link opened in the same tab did not reload the page.
- The console grid overflowed on narrow screens.
- The phone control row clipped.
- Reactions launched on top of the chat.
