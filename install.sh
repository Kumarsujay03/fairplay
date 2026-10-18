#!/bin/sh
# FairPlay installer for Linux and macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/Kumarsujay03/fairplay/main/install.sh | sh
#
# Installs the latest release to ~/.local/bin/fairplay after checking its
# SHA-256 against the release's SHA256SUMS, then offers to install ffmpeg
# (needed) and cloudflared (for internet sharing) if they are missing.
# FairPlay keeps itself up to date after that; nothing starts at login.
#
#   ... | sh -s -- --uninstall        remove FairPlay, keep invites and settings
#   ... | sh -s -- --uninstall --purge   also delete invites, settings and the video cache
#   ... | sh -s -- --yes              install missing tools without asking
set -eu
repo=Kumarsujay03/fairplay
bindir="${FAIRPLAY_BIN_DIR:-$HOME/.local/bin}"

uninstall=0; purge=0; yes="${FAIRPLAY_YES:-0}"
for a in "$@"; do
  case "$a" in
    --uninstall) uninstall=1 ;;
    --purge) purge=1 ;;
    --yes|-y) yes=1 ;;
    *) echo "Unknown option: $a" >&2; exit 1 ;;
  esac
done

case "$(uname -s)" in
  Linux) os=linux; cfg="${XDG_CONFIG_HOME:-$HOME/.config}/fairplay"; cache="${XDG_CACHE_HOME:-$HOME/.cache}/fairplay" ;;
  Darwin) os=darwin; cfg="$HOME/Library/Application Support/fairplay"; cache="$HOME/Library/Caches/fairplay" ;;
  *) echo "Unsupported OS: $(uname -s). On Windows use install.ps1." >&2; exit 1 ;;
esac

if [ "$uninstall" = 1 ]; then
  rm -f "$bindir/fairplay"
  echo "Removed $bindir/fairplay"
  if [ "$purge" = 1 ]; then
    rm -rf "$cfg" "$cache"
    echo "Deleted invites and settings ($cfg) and prepared videos ($cache)"
  else
    echo "Kept your invites and settings in $cfg (add --purge to delete them)"
  fi
  exit 0
fi

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "Unsupported CPU: $(uname -m)" >&2; exit 1 ;;
esac

# ask "question" → returns 0 for yes. Reads the terminal even when piped from curl.
ask() {
  [ "$yes" = 1 ] && return 0
  [ -r /dev/tty ] || return 1
  printf "%s [Y/n] " "$1" > /dev/tty
  read -r reply < /dev/tty || return 1
  case "$reply" in ""|y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

tag=$(curl -fsSI "https://github.com/$repo/releases/latest" | tr -d '\r' | sed -n 's#^[Ll]ocation: .*/releases/tag/\(v[0-9.]*\)$#\1#p')
[ -n "$tag" ] || { echo "Could not find the latest FairPlay release" >&2; exit 1; }
asset="fairplay-$tag-$os-$arch"
base="https://github.com/$repo/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading FairPlay $tag ..."
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"
want=$(grep " $asset\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)
if command -v sha256sum >/dev/null; then got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
else got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1); fi
[ -n "$want" ] && [ "$want" = "$got" ] || { echo "Checksum mismatch, not installing" >&2; exit 1; }

mkdir -p "$bindir"
chmod +x "$tmp/$asset"
mv "$tmp/$asset" "$bindir/fairplay"
echo "FairPlay $tag installed to $bindir/fairplay"

# ---- tools FairPlay uses ----
have() { command -v "$1" >/dev/null 2>&1 || [ -x "$bindir/$1" ]; }

if ! have ffmpeg || ! have ffprobe; then
  cmd=""
  if [ "$os" = darwin ]; then
    command -v brew >/dev/null && cmd="brew install ffmpeg"
  elif command -v apt-get >/dev/null; then cmd="sudo apt-get install -y ffmpeg"
  elif command -v dnf >/dev/null; then cmd="sudo dnf install -y ffmpeg"
  elif command -v pacman >/dev/null; then cmd="sudo pacman -S --noconfirm ffmpeg"
  elif command -v zypper >/dev/null; then cmd="sudo zypper install -y ffmpeg"
  fi
  echo
  echo "FairPlay needs ffmpeg to prepare videos, and it is not installed."
  if [ -n "$cmd" ] && ask "Install it now with: $cmd ?"; then
    sh -c "$cmd" < /dev/tty
  else
    echo "Install ffmpeg before starting FairPlay: ${cmd:-see https://ffmpeg.org/download.html}"
  fi
fi

if ! have cloudflared; then
  echo
  echo "cloudflared lets friends join over the internet (without it, only your Wi-Fi can join)."
  if [ "$os" = darwin ]; then
    if command -v brew >/dev/null && ask "Install it now with: brew install cloudflared ?"; then brew install cloudflared; fi
  elif ask "Download it from Cloudflare's GitHub releases into $bindir ?"; then
    curl -fsSL -o "$bindir/cloudflared" "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-$arch"
    chmod +x "$bindir/cloudflared"
    echo "cloudflared installed to $bindir/cloudflared"
  fi
fi

echo
case ":$PATH:" in
  *":$bindir:"*) echo "Start FairPlay with:  fairplay" ;;
  *) echo "Start FairPlay with:  $bindir/fairplay"
     echo "(To type just 'fairplay', add $bindir to your PATH, e.g. echo 'export PATH=\"$bindir:\$PATH\"' >> ~/.profile)" ;;
esac
echo "It opens the host console in your browser and updates itself when a new version is out."
