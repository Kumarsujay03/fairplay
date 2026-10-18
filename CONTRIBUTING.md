# Contributing to FairPlay

Thanks for taking the time to contribute!

## Reporting bugs and requesting features

- Search existing issues first to avoid duplicates.
- Use the **Bug report** or **Feature request** template. For bugs, include your OS, browser, how guests connected (Wi-Fi or tunnel), the ffmpeg version (`ffmpeg -version | head -1`) and anything printed in the terminal.
- For security problems, follow the [security policy](SECURITY.md) instead of opening a public issue.

## Development setup

You need Go 1.23 or newer, Node.js 20 or newer, and ffmpeg with ffprobe. Go has one dependency, [`github.com/coder/websocket`](https://github.com/coder/websocket). The frontend uses Preact, `@preact/signals` and hls.js.

```bash
git clone https://github.com/Kumarsujay03/fairplay.git
cd fairplay
make build
```

```bash
make test
```

`make build` builds the frontend into `internal/webui/dist` and then `bin/fairplay`. `make test` runs the Go tests with the race detector, including a real ffmpeg encode and end-to-end server tests, plus the TypeScript type-check.

For frontend work, run FairPlay on port 8321 in one terminal and the Vite dev server in another. Vite proxies the API, sockets and video.

```bash
./bin/fairplay -media ~/Videos -tunnel off
```

```bash
cd web && npm run dev
```

To test as a guest on one machine, use different hostnames for host and guest, such as `127.0.0.1` and your LAN IP, so their cookies stay separate.

## Making changes

1. Fork the repository and create a branch, for example `git checkout -b fix/short-description`.
2. Keep changes focused. One fix or feature per pull request.
3. Add or update tests. The room clock in `internal/room` is pure logic and easy to test exactly. Server behaviour belongs in `internal/server/server_test.go`.
4. Run `gofmt -l .`, `go vet ./...` and `make test` before opening the pull request.
5. Keep it light. FairPlay idles at about 8 MB of memory, and the app shell is about 26 KB gzipped. Please don't add dependencies without discussing it first.

## Releasing (maintainer)

1. Add the new version to `CHANGELOG.md`.
2. Tag the release with `git tag -a vX.Y.Z -m vX.Y.Z`.
3. Push the tag with `git push origin vX.Y.Z`.

The release workflow tests, builds every platform, signs `SHA256SUMS` with the `FAIRPLAY_SIGNING_KEY` secret and publishes the release. Installed copies pick it up on their next start. The private key never leaves the maintainer's machine and the Actions secret. If it is lost, existing installs can no longer auto-update.

## Project layout

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for how the pieces fit together, how sync works, and the security model.
