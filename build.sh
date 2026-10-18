#!/bin/sh
# Build release binaries for Linux, macOS and Windows into dist/.
# Usage: ./build.sh [version]     (default: version from `git describe`)
set -e
cd "$(dirname "$0")"
VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=$VERSION"
if [ ! -f internal/webui/dist/index.html ]; then
  (cd web && npm ci --no-audit --no-fund && npm run build)
  touch internal/webui/dist/.keep
fi
rm -rf dist && mkdir dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${target%/*}; arch=${target#*/}
  ext=""; [ "$os" = windows ] && ext=".exe"
  out="dist/fairplay-$VERSION-$os-$arch$ext"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags="$LDFLAGS" -o "$out" ./cmd/fairplay
  echo "built $out"
done
(cd dist && sha256sum fairplay-* > SHA256SUMS)
