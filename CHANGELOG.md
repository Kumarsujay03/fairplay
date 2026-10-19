# Changelog

## Unreleased

- An event log runs for the whole showing, always on, at `~/.config/fairplay/logs/fairplay-<date>-<time>.jsonl` (one JSON object per line; `fairplay logs` lists the files and some `jq` filters). It records every room state change (play, pause, seek, who did it, the reason), every viewer's status once a second (position against the room clock, drift, ping, buffering, seconds buffered ahead, dropped frames, quality, bandwidth estimate, tab visibility), every command and host-console action, chat and reactions, joins and leaves, the exact ffmpeg command and the encoder's progress, every HLS request with its size and how long it took, and what each player saw in its browser: stalls, seeks, quality switches, hls.js errors, the sync engine's nudges and hard seeks, and the viewer's own clicks. When someone buffers or falls behind, the log says what every part of FairPlay was doing at that second. The 30 newest files are kept
- Fix: a decoder failure on the host's computer paused the whole room. When the browser's video decoder fails (a garbled green and magenta picture, then a decode error) it pauses the video element itself, and so does hls.js while it rebuilds the stream afterwards. The player took that pause for the host pressing Pause and stopped everyone. Those pauses are now recognised and ignored; the host's player recovers on its own and the room plays on. The same mistake could drop a guest into solo mode; it no longer does
- Fix: the Wi-Fi link could point at a VPN or virtual adapter (a 10.x.x.x address nobody on the Wi-Fi can reach) instead of the Wi-Fi card. Tunnels, point-to-point links and adapters named like Docker, VirtualBox, WireGuard, Tailscale or Windscribe are skipped, and a home 192.168.x.x address is listed first
- Screen lock: a lock button in the player controls. While locked, taps, clicks, double taps and keyboard shortcuts do nothing, the controls and chat stay hidden, and a tap shows a centred "Screen locked · tap to unlock" pill for a moment. For the host and every viewer
- The host's click on the picture no longer pauses the room: it shows or hides the controls, as a tap does on a phone. The host pauses with the button or the space bar
- Chat bubbles fade 10 seconds after the last message. On phones they stayed on screen forever, because the first tap counted as the mouse "entering" the chat and the composer kept its focus after a send
- The host's player plays one quality, the best rung its screen can show, with no automatic switching at all. hls.js's warm-up on every start was a dip to 480p, then 720p, then 1080p in the first twelve seconds, whatever the bandwidth. The host streams from its own computer, so there is nothing to measure, and each switch made the browser's decoder reconfigure; one of those switches is what broke it above. Picking a quality by hand still works as before

## 0.3.1 (2026-10-19)

The 0.1.4 encoding and streaming code from 0.3.0, with three changes to how ffmpeg runs, so the host can watch on the computer that prepares the film:

- GPU encoding on Linux through VAAPI (Intel and AMD graphics): the picture is uploaded to the GPU once and every version is scaled and encoded there. Preparing a 1080p film now takes about two CPU cores instead of eight to twelve. Without a usable GPU, libx264 on the CPU is used as before
- ffmpeg starts at the lowest CPU priority (through `nice -n 19`, so all its threads inherit it) and may use at most half of the CPU threads, so the browser always has cores to decode the picture
- With the GPU encoder, its own keyframe interval matches the 4-second pieces, so every version still cuts into identical 4-second pieces

Measured on a 1080p film while it was being prepared, with the host watching on the same laptop and a guest on another browser: no buffering on either, 1 dropped frame in 3,000 on the host, both back in sync within a second after each pause and play, and 10 ms apart on average.

## 0.3.0 (2026-10-19)

The encoding and streaming code is back to exactly what it was in 0.1.4, the last version before the host's own screen started buffering while guests played fine. Everything added to that path from 0.1.5 on is removed:

- Copying the original picture, quality-version and preparing-speed settings (`-versions`, `-prepare`), the bitrate patching of the playlist, the 45-second start threshold and 15-second seek margin
- GPU (VAAPI) encoding, the nice-19 start and CPU thread caps, the run-ahead-then-rest encoder and resuming an interrupted encode
- In the player: the stall restarts, the background-tab catch-up, the reworked pause and media-key handling, the playback-error retry window and the phone 720p cap. The sync engine is the 0.1.4 one again

Kept, because they are not part of encoding or streaming: `fairplay stop`, preloaded animated reactions and their longer hold, the Watching list layout, and the phone fixes (chat on the right in landscape, the settings sheet, the tap that no longer flickers, the status pill that shortens)

As in 0.1.4, preparing uses the CPU (libx264) at full speed and the encoder speed shows in the console. Videos prepared by 0.1.5–0.2.3 are prepared again the first time they are played.

## 0.2.3 (2026-10-19)

- The encoder runs ahead, then rests. It prepares at full speed until it is 10 minutes ahead of the room, then the process is frozen (no CPU, no GPU) and thaws when the lead shrinks to 5 minutes. The host usually watches on the computer that encodes, and an encoder working flat out through the whole showing was what made the host's own player stutter while every guest was fine. Guests notice nothing: the encoder is always minutes ahead of them. The console shows "10 min ahead, resting" while it waits. A video the room isn't showing is prepared without pauses
- An interrupted preparation (Ctrl+C, restart, another file's turn) now continues where it stopped instead of starting the film over

## 0.2.3 (2026-10-19)

- An interrupted preparation continues where it stopped. Until now, stopping FairPlay (or the encode) part way through a film threw the finished part away, and the next start encoded the whole film again from the beginning while you watched the first minute. The whole 4-second pieces are now kept with a small record of what made them, and the next Prepare of the same file picks up at that point; playback is allowed at once when enough was already prepared. The pieces are only reused by the very same plan (same quality versions, audio tracks, encoder and ladder); anything else, or a real encoder error, still starts over
- The host's cache list names an interrupted encode after its film instead of "Unfinished encode", and the cache limit never removes the room's own film while it waits to be continued

## 0.2.2 (2026-10-19)

- Automatic quality switching during playback is gone for good. It arrived in 0.1.7–0.1.8 as a cure for a busy computer, and every version of it turned out to be a cause of the very thing it was meant to fix: a mid-stream quality switch restarts the decoder, which shows up as a black picture at the start and a few seconds' delay behind everyone else, with the message "This computer is busy, using a lighter quality". The last way it could still trigger was the stall check, which started counting from the room's clock, so pressing Play in the console and then opening the room fired it before this player had even begun. Now a player keeps the quality it has (Auto by bandwidth and screen size, or the one chosen by hand), the stall check only runs once the player is really playing and simply restarts loading at the room position, and the "busy" message no longer exists
- Measured: host opens the room 8 s after pressing Play in the console: in sync from the first second at 1080p, no restart; host and guest within a few hundredths of a second of each other

## 0.2.1 (2026-10-19)

- Fix: the picture could stay black for 10–15 seconds at the start with a "This computer is busy" message. The check that watches for a picture falling behind the sound was too eager: it also fired during the first seconds after a start or seek and when frames were decoded but not shown (a tab behind another window), and each time it switched quality and restarted the decoder. It now waits 10 seconds after any start or seek, needs the picture a full second behind for 4 seconds, ignores hidden or covered players, and only acts when the decoder itself has stopped
- Fix: after a viewer paused, played again and was brought back to the room, they could end up 2–3 seconds ahead and stay there. A catch-up seek aimed ahead by the learned "seek cost" even when the viewer was already ahead, landing ahead again. It now aims ahead only when behind, and the learned cost is capped at one second
- A media key or another tab starting to play can no longer pause the whole room when pressed on the host's computer; the host uses the on-screen button. Guests still go solo as before
- The browser console names why a quality was lowered and why a pause was or wasn't taken as the viewer's

## 0.2.0 (2026-10-19)

The way videos are prepared and streamed is rebuilt. Since 0.1.5 the original picture was copied instead of encoded, which kept the file's own keyframes: anywhere from one to ten seconds apart. That made the stream's pieces long and uneven, so each player waited for a different piece at the encoder's edge, seeks landed late, and two screens could drift up to twenty seconds apart while both clocks read "in sync". The pacing added in 0.1.8 made the edge even closer.

- Every version is encoded again, on the GPU where there is one, with a keyframe every 4 seconds in all of them: identical 4-second pieces, instant quality switches and seeks, a steadily moving edge. The top version is encoded at 1.5× the file's own bitrate (at least the ladder's figure, at most 10 Mbps), so nothing is visibly lost
- Playback can start once 45 seconds are prepared, and the host can skip ahead only to 15 seconds behind the edge, so nobody ever waits at it
- No pacing: the encoder runs at full speed on the GPU (a 2-hour film in about 15 minutes, under one CPU core). The Cool/Fast setting now only sets how many CPU threads a CPU-only computer may use
- Audio that must be re-encoded gets 160 kbps stereo AAC
- Videos prepared by earlier versions are prepared again the first time they are played

## 0.1.9 (2026-10-19)

- GPU encoding on Linux through VAAPI (Intel and AMD graphics): the smaller versions are scaled and encoded on the GPU, so preparing a film costs about half of one CPU core. FairPlay tries it first; if the driver is missing, the start-up banner says what to install
- Automatic quality stops at 720p on phones and tablets, where it looks the same and saves battery and data; laptops and TVs get 1080p. Picking a quality by hand still allows anything
- The banner names the encoder in plain words (GPU (VAAPI), NVIDIA GPU (NVENC), CPU (libx264)…)

## 0.1.8 (2026-10-19)

- New **Preparing speed** setting (`-prepare`). **Cool**, the default, paces the encoder at twice playback speed with a quarter of the CPU threads at the lowest priority: about one or two cores, so the computer stays quiet and cool while you watch on it, and viewers can never catch up with it. **Fast** uses half the CPU flat out and has a 2-hour film ready in minutes
- Fix: the host's picture could run a few seconds behind the sound and behind everyone else while the clock said "in sync", when the computer was too busy to decode the original quality in time. The player now watches the frame actually on screen; when it falls behind, it switches to a lighter quality and restarts the decoder, and a player that can't catch up by playing slightly faster jumps instead
- Smaller versions are encoded with the `veryfast` preset again: `fast` cost twice the CPU for a difference nobody sees at 720p

## 0.1.7 (2026-10-19)

- Fix: the host's own player froze on "Buffering" or "Playback error" while guests played on, because preparing the video took almost all of the computer's CPU. ffmpeg now runs at the lowest priority from the start and uses at most half the CPU threads, so the browser on the host's computer always comes first
- When a computer can't decode the picture fast enough, the player steps down to a lighter quality instead of stalling, and it drops quality when frames are being dropped
- A long film no longer runs out of playback-error recoveries and reloads; the host's player stays in step with the room
- Reactions stay fully visible for about four seconds so their animation plays through, then drift up and fade
- Every animated reaction is prepared in the background after FairPlay starts (a one-time download, then cached), so even rare emoji animate the moment they are sent. Turn it off with `-emoji-preload=false`
- The "Watching" list keeps each viewer's status, ping and quality on one line, with the host's buttons on their own line when the panel is narrow

## 0.1.6 (2026-10-19)

- Fix: a player could get stuck on "Buffering" behind the "tap to start" screen while the room played on, and stay behind after a refresh. Autoplay retries now only stop when the browser really refuses, and a watchdog restarts loading at the room position when a player stops moving
- Fix: while a video was still preparing, the original-quality version was advertised at about 140 kbps, so automatic quality chose on wrong numbers. Videos already prepared by 0.1.5 are corrected when they load
- Fix: on phones a single tap in fullscreen made the controls flicker and close; a tap now shows them and the next tap hides them
- Phone landscape and fullscreen: the see-through chat is on the right, and the centre messages (waiting, paused, intermission) stay clear of it
- Phone portrait: the quality, audio and subtitles menu and the "who is watching" list open as a sheet at the bottom of the screen instead of going off its top; tap outside to close
- A browser that pauses a hidden tab no longer pauses the room for everyone when that tab is the host's, and a tab you come back to catches up at once
- Hard seeks wait for a slow seek to finish instead of restarting it every second
- `fairplay stop` ends a room started in another terminal

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
