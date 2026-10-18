# Changelog

## 0.1.0 (2026-10-18)

First release.

- Global room clock with ping-compensated sync, equal start, solo mode and Back to live
- Intermission with countdown, co-host control, playlist with auto-advance
- ffmpeg HLS ladder (up to 1080p/720p/480p), multiple audio tracks, text and sidecar subtitles, play while encoding, transcode cache, hardware encoder detection, HDR tonemapping and deinterlacing
- Personal device-bound invites, host lobby with second-device flagging, kick, revoke, lock and end room
- Bot shield: proof of work, strikes and bans, rate limits, lobby flood cap
- Floating chat with per-viewer hide/show, emoji reactions, viewer list with ping
- Host console, phone layout, keyboard shortcuts, automatic rejoin after restarts
- cloudflared and ngrok tunnel supervisor
- One-line installers for Linux, macOS and Windows that verify checksums and offer to install ffmpeg and cloudflared, with an uninstall mode
- Signed auto-update on start (`fairplay update` to update now, `-update check|off` to opt out)
- `fairplay` with no options picks or creates your video folder and opens the host console in the browser; change the folder from the console
- `fairplay check` to see what FairPlay found, friendlier errors for a missing folder, missing ffmpeg or a busy port
