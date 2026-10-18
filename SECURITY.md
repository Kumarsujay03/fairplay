# Security Policy

## Reporting a vulnerability

Please **do not** open a public issue for security problems.

Report it privately through GitHub instead:

1. Open the repository's **Security** tab.
2. Click **Report a vulnerability**.
3. Describe the issue, how to reproduce it, and what impact it has.

You'll get a response as soon as possible, usually within a few days. Once the issue is confirmed, a fix will be prepared and you'll be credited in the advisory unless you'd prefer not to be.

## Supported versions

Only the latest release is supported with security fixes.

## Threat model

FairPlay runs on the host's own computer and is reachable from the internet through a tunnel while a room is open. The public URL should be treated as known to attackers.

**In scope**

- **Strangers joining without an invite.** Every page except the app shell needs a session. Sessions come only from a valid invite token (128-bit, stored hashed, compared in constant time) or the host key (192-bit, regenerated on every start).
- **Forwarded or leaked invite links.** A link binds to the first device that joins. Every new device waits in a lobby for the host, and a second device on the same link is flagged. Tokens travel in the URL fragment, so they never reach servers, tunnel logs or link-preview bots.
- **Bots and brute force.** Join, peek and login need a solved proof-of-work challenge. Failed attempts add strikes that raise the puzzle cost, and five strikes ban the IP for 15 minutes. There are per-IP and global rate limits, and a cap of 10 pending lobby requests.
- **Cross-site attacks.** Cookies are HttpOnly and SameSite=Strict. Every write and WebSocket checks `Origin`. A strict Content-Security-Policy with no inline scripts, `frame-ancestors 'none'` and nosniff is applied everywhere.
- **Chat injection.** Names and messages are cleaned and always rendered as text.
- **Reading other files.** Media is served only from the chosen folder. Symlinks must resolve inside it, and HLS requests must match the exact file names ffmpeg produces. Guests can reach only the video the room is currently showing.
- **Abuse by an admitted guest.** Chat and reactions are rate limited, and the host can hide chat for that viewer, kick them or revoke their link. Only the host and co-hosts the host chose can control playback.

**Out of scope**

- **A guest re-sharing what they watch.** Anyone who can watch can record their screen.
- **Malware on the host machine.** Malware running as your user can read the host key printed in the terminal.
- **A host who admits the wrong person.** Check the name, device and IP in the lobby before admitting.
- **Tunnel provider attacks.** The tunnel provider (Cloudflare or ngrok) terminates TLS and can see traffic.
- **Large-scale DoS.** A large-scale denial of service against the tunnel URL is beyond what a home connection can absorb. Rate limits and bans only reduce the cost of small-scale abuse.
- **Wi-Fi traffic.** Guests joining over Wi-Fi use plain HTTP. Someone who controls the local network could read or alter that traffic. Use the tunnel link on networks you don't trust.
