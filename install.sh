#!/bin/sh
# tauko installer: downloads the latest release for this machine, verifies it,
# installs it to ~/.local/bin (no sudo) and runs `tauko setup`.
#
#   curl -fsSL https://raw.githubusercontent.com/ralihachev/tauko/main/install.sh | sh
#
# Options (environment variables):
#   TAUKO_VERSION=v0.1.0      install this release instead of the latest
#   TAUKO_INSTALL_DIR=/path   install the binary here instead of ~/.local/bin
#   TAUKO_NO_SETUP=1          only install the binary; skip `tauko setup`
#   TAUKO_BASE_URL=https://…  download from this mirror instead of GitHub releases
#   TAUKO_ARCHIVE=/path.tgz   install from a local archive (for testing)
set -eu

REPO="ralihachev/tauko"
DIR="${TAUKO_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '\033[36m→\033[0m %s\n' "$1"; }
fail() { printf '\033[31merror:\033[0m %s\n' "$1" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "tauko supports macOS and Linux (got $(uname -s)). On Windows, use WSL." ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported CPU: $(uname -m)" ;;
esac

name="tauko_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if [ -n "${TAUKO_ARCHIVE:-}" ]; then
  say "Using local archive $TAUKO_ARCHIVE"
  cp "$TAUKO_ARCHIVE" "$tmp/$name"
else
  command -v curl >/dev/null 2>&1 || fail "curl is required"
  if [ -n "${TAUKO_BASE_URL:-}" ]; then
    base="$TAUKO_BASE_URL"
  elif [ -n "${TAUKO_VERSION:-}" ]; then
    base="https://github.com/$REPO/releases/download/$TAUKO_VERSION"
  else
    base="https://github.com/$REPO/releases/latest/download"
  fi
  say "Downloading $name"
  curl -fsSL "$base/$name" -o "$tmp/$name" || fail "download failed: $base/$name"
  curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" || fail "checksum download failed"

  want="$(grep " $name\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
  [ -n "$want" ] || fail "no checksum for $name"
  if command -v sha256sum >/dev/null 2>&1; then
    got="$(sha256sum "$tmp/$name" | cut -d' ' -f1)"
  else
    got="$(shasum -a 256 "$tmp/$name" | cut -d' ' -f1)"
  fi
  [ "$got" = "$want" ] || fail "checksum mismatch for $name"
fi

tar -xzf "$tmp/$name" -C "$tmp" tauko
mkdir -p "$DIR"
mv "$tmp/tauko" "$DIR/tauko"
chmod +x "$DIR/tauko"
say "Installed $("$DIR/tauko" version) to $DIR/tauko"

case ":$PATH:" in
  *":$DIR:"*) ;;
  *)
    printf '\n\033[33m!\033[0m %s is not on your PATH. Add it to your shell profile:\n' "$DIR"
    printf '    echo '\''export PATH="%s:$PATH"'\'' >> ~/.%src\n\n' "$DIR" "$(basename "${SHELL:-sh}")"
    ;;
esac

if [ -z "${TAUKO_NO_SETUP:-}" ]; then
  "$DIR/tauko" setup
fi
