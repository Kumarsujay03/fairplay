# Changelog

## 0.1.5 (2026-10-19)

- Much smaller and better encodes. When the browser can play the original H.264 picture, FairPlay streams it untouched instead of re-encoding it, so the top quality is exactly the source and preparing starts faster. Smaller versions are encoded with quality-based settings capped at a bitrate, keyframes match the original so switching is seamless, and AAC stereo audio is copied. A 1080p film now takes about 1.3–1.5× its file size instead of about 2.3×
- New host setting and `-versions` flag for the quality versions of new videos: `original` (original only), `balanced` (original + 720p, the default) or `full` (original + 720p + 480p)
- The console shows "Original quality" and the disk space each video takes, with an estimate while it is preparing
- Reactions rise more slowly so the animation can be seen
- Phones: the emoji picker opens below the reactions and fits the screen; the room status pill shortens instead of running into the viewer list; the ping stays on one line; the console hides the thumbnail and uses shorter dropdown labels

## 0.1.4 (2026-10-18)

- QR code for every invite in the host console: scan it with a phone camera to open the exact link, port included (typing the address without `:8321` sends the browser to the wrong place)
- Fullscreen on phones turns to landscape and uses the cinema layout: video edge to edge, see-through chat on the left with reactions, the emoji picker and the chat toggle
- On touchscreens a tap shows or hides the controls instead of pausing

## 0.1.3 (2026-10-18)

- Telegram-style animated reactions: every animated Noto emoji (611 of them) in a searchable picker with categories and recents; reactions pop in big with a burst, play their animation and drift up over the chat. The 12 most used are bundled; others are fetched once by the host and cached
- Viewers can't go ahead of the room: seeking and skipping stop at the room position (no missing chunks, no spoilers); catching up rejoins the room automatically
- When the browser blocks sound, playback starts muted and in sync with a "tap to unmute" pill, instead of waiting for a tap
- Picture-in-picture on phones and Safari as well as desktop
- Chat messages are limited to 200 characters, with a counter
- Default cache limit raised to 40 GB
- Fix: after a restart, playing an already prepared video stayed on "Preparing video…"
- Fix: tapping the seek bar sometimes did nothing

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
