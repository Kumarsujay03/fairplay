# FairPlay

Watch videos together, perfectly in sync. FairPlay is one small program you run on your own computer. Friends join from any browser, on your Wi-Fi or over the internet. Nothing is uploaded or deployed anywhere.

- **One global clock.** Everyone sees the same frame at the same moment. Viewers measured about 15–25 ms apart in testing.
- **Watch solo when you want.** Pause or rewind just for yourself, then tap **Back to live** to rejoin the room.
- **Intermission.** The host pauses everyone, with a countdown.
- **Any video file.** MKV, MP4, AVI, HEVC and more are converted by ffmpeg into adaptive quality from 1080p down to 360p. Multiple audio tracks and subtitles are kept.
- **Floating chat and emoji reactions** sit over the video.
- **Private by default.** Each person gets their own invite link, the host admits every new device, and a bot shield protects the public link.

Design: [Figma file](https://www.figma.com/design/SOHOZXlUiNsUnSdzDreUB4). Architecture and decisions: [docs/PITCH.md](docs/PITCH.md).

## Quick start

You need **ffmpeg**, and **cloudflared** for internet sharing. Without cloudflared, FairPlay still works on your Wi-Fi.

```bash
make build
```

```bash
./bin/fairplay -media ~/Movies -name "Movie night"
```

The terminal prints your **host link**. It works like a password, so keep it private. Open it to reach the host console, then:

1. Pick a video. It starts preparing right away, and playback can begin after a few seconds.
2. Create an invite for each friend. The link is copied for you to send.
3. When they open it, admit them from the lobby.
4. Press play. Everyone starts on the same tick.

## How the invite links stay private

A forwarded link cannot get a stranger in on its own.

- **One link per person.** The secret sits in the part of the URL after `#`. Browsers never send that part to any server, so it never shows up in tunnel logs or link previews.
- **Locked to a device.** The first browser that joins owns the link. If the link is opened anywhere else, the host sees a flagged request: "Priya's link opened on a second device".
- **The host admits every new device.** Returning devices rejoin straight away, even after FairPlay restarts.
- **The host stays in control.** Kick, revoke, lock the room, or end the room with every link revoked.

### Bot and spam shield

- **Proof of work.** Join and login requests need a small puzzle solved by the browser. It takes about 0.2 s and happens in the background while the guest types a name. A bot pays that cost on every attempt.
- **Strikes and bans.** Five wrong tokens, keys or forged proofs from one network ban it for 15 minutes. Each strike also makes the next puzzle harder.
- **Rate limits.** There are limits per IP and globally, plus a cap of 10 pending join requests, so spam cannot bury real guests.
- **Nothing to discover.** Unknown paths return 404, and pages carry `noindex`. Slow-request attacks hit server timeouts.
- **Hardened browser surface.** A strict Content-Security-Policy, same-origin checks on every write and socket, and HttpOnly SameSite cookies.

## How sync works

The server is the clock. Each browser pings it every 2 seconds and estimates its clock offset from the fastest sample. That removes ping from the maths: a viewer at 300 ms ping computes the same position as one at 5 ms. Four times a second the player compares itself with the room. Small drift is corrected by nudging playback speed between 0.95× and 1.05×, which nobody notices. Large drift triggers a seek. Play is scheduled a moment in the future, based on the slowest viewer's ping, so nobody starts late. Ping, jitter and drift are visible to everyone in the viewer list and the settings menu.

## Options

| Flag | Default | Meaning |
|---|---|---|
| `-media` | `~/Videos` | Folder with your videos |
| `-name` | `Movie night` | Room name guests see |
| `-port` | `8321` | Port |
| `-tunnel` | `cloudflared` | `cloudflared`, `ngrok` or `off` |
| `-lan` | `true` | Allow direct Wi-Fi joins. `false` means localhost and tunnel only |
| `-max` | `12` | Maximum guests |
| `-auto-admit` | `false` | Admit first-time opens without the lobby. Second devices always need approval |
| `-ffmpeg`, `-ffprobe`, `-tunnel-bin` | from PATH | Paths to the tools |
| `-cache` | `~/.cache/fairplay` | Where prepared videos are kept. Delete it any time to free space |

## Keyboard

Space or K plays and pauses. Left or J goes back 10 s, Right or L goes forward 10 s. M mutes, F toggles fullscreen and C toggles chat. G goes back to live, Up and Down change the volume, and Enter starts typing in chat.

## Development

```bash
make test
```

```bash
cd web && npm run dev
```

The dev server proxies the API to a FairPlay running on port 8321. The Go server is in `cmd/` and `internal/`, and the Preact frontend is in `web/`, built into the binary.

## Notes and limits

- Over the internet every viewer streams their own copy, so your upload speed is the ceiling. The host console shows the upload needed. Viewers on slow links drop to a lower quality automatically.
- The first showing of a file transcodes it. This runs at about 2–3× realtime on a modern CPU with libx264, and faster with NVENC or Quick Sync. Later showings start instantly from the cache.
- Picture-based subtitles (PGS or VobSub from Blu-ray rips) cannot be shown. Text subtitles and sidecar `.srt` or `.vtt` files work.
- ngrok's free plan shows a warning page to visitors and needs `ngrok config add-authtoken` first. cloudflared needs no account.
