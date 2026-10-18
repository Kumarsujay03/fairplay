# Changelog

## 0.1.2 (2026-10-18)

- Reactions now float over the chat instead of under it, with a bouncy pop-in, sway and wobble as they rise
- **Stop FairPlay** button in the host console: ends the room, tells every viewer, and closes FairPlay on the host's computer
- The player's host-console button is now a settings gear

## 0.1.1 (2026-10-18)

- Wide films (for example 1920×800) are now prepared up to 1080p instead of 720p, never upscaled, with bitrate matched to the picture
- Audio and subtitle tracks are named by language (English, Hindi, Indonesian…); uploader tags such as "MoviesMod.ai.in" are ignored, repeats are numbered, SDH and Forced are shown
- The host chooses audio, subtitles and quality for everyone; each viewer can switch to their own; quality is labelled 1080p HD, 720p SD, 480p data saver, and Auto picks the best for each device and connection
- Intermission length is a host setting (default 5 minutes, also `-intermission`)
- Stop and clear the current video for everyone; remove single videos from the queue or clear it
- Chat keeps 200 messages and scrolls without a scrollbar, with a "new messages" pill
- Reactions rise from the middle of the chat
- Firewall help: FairPlay detects ufw, firewalld, Windows and macOS firewalls that block Wi-Fi guests, offers to fix it once, and adds `fairplay firewall`
- Storage: `fairplay cache` and `fairplay cache clear`, a Storage card in the host console, and a 20 GB limit that removes the oldest prepared videos (`-cache-max`)
- Fix: an empty cache crashed the Storage view

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
