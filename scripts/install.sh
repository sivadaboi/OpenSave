#!/bin/sh
# OpenSave installer — headless CLI + daemon for Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/sivadaboi/OpenSave/main/scripts/install.sh | sh
#
# Installs to ~/.local/bin by default, so no root is needed. Override with:
#   OPENSAVE_INSTALL_DIR=/usr/local/bin   where to put the binaries
#   OPENSAVE_VERSION=v2.2.0               pin a version instead of latest
#
# The download is checksum-verified against the SHA256SUMS published with the
# release. That matters more than usual here: you are piping a script to a
# shell, so the least this script can do is refuse to install bytes it can't
# vouch for.

set -eu

REPO="sivadaboi/OpenSave"
INSTALL_DIR="${OPENSAVE_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${OPENSAVE_VERSION:-latest}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || die "this installer needs '$1' but it isn't installed"
}

need uname
need tar
need mktemp

# curl or wget, whichever is present.
if command -v curl >/dev/null 2>&1; then
    fetch() { curl -fsSL "$1" -o "$2"; }
    fetch_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
    fetch() { wget -qO "$2" "$1"; }
    fetch_stdout() { wget -qO- "$1"; }
else
    die "this installer needs curl or wget"
fi

# ── Platform ─────────────────────────────────────────────────────────────

os="$(uname -s)"
arch="$(uname -m)"

case "$os" in
    Linux) ;;
    Darwin) die "macOS builds aren't published yet — build from source: go build ./cmd/opensave-cli" ;;
    *) die "unsupported OS: $os" ;;
esac

case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64)
        die "arm64 Linux builds aren't published yet. Build from source:
  git clone https://github.com/$REPO && cd OpenSave
  go build -o opensave-cli ./cmd/opensave-cli" ;;
    *) die "unsupported architecture: $arch" ;;
esac

ASSET="opensave-linux-${arch}.tar.gz"

if [ "$VERSION" = "latest" ]; then
    BASE="https://github.com/$REPO/releases/latest/download"
else
    BASE="https://github.com/$REPO/releases/download/$VERSION"
fi

# ── Download ─────────────────────────────────────────────────────────────

tmp="$(mktemp -d)"
# shellcheck disable=SC2064  # expand tmp now, not at trap time
trap "rm -rf '$tmp'" EXIT INT TERM

say "Downloading OpenSave ($VERSION, linux/$arch)…"
fetch "$BASE/$ASSET" "$tmp/$ASSET" || die "could not download $BASE/$ASSET"

# Verify against the published checksums when they exist. Releases before
# 2.2.0 predate SHA256SUMS, so a missing file is a warning, not a failure.
if fetch "$BASE/SHA256SUMS" "$tmp/SHA256SUMS" 2>/dev/null; then
    if command -v sha256sum >/dev/null 2>&1; then
        # Tolerate both sha256sum spellings: "<hash>  file" (text mode) and
        # "<hash> *file" (binary mode).
        expected="$(awk -v want="$ASSET" '
            { name = $NF; sub(/^\*/, "", name)
              if (name == want) { print $1; exit } }' "$tmp/SHA256SUMS")"
        actual="$(sha256sum "$tmp/$ASSET" | awk '{print $1}')"
        [ -n "$expected" ] || die "no checksum for $ASSET in SHA256SUMS"
        [ "$expected" = "$actual" ] || die "checksum mismatch — refusing to install
  expected: $expected
  actual:   $actual"
        say "Checksum verified."
    else
        say "warning: sha256sum not found — skipping checksum verification"
    fi
else
    say "warning: this release publishes no SHA256SUMS — skipping verification"
fi

# ── Install ──────────────────────────────────────────────────────────────

tar -xzf "$tmp/$ASSET" -C "$tmp" || die "could not extract $ASSET"

src="$tmp/opensave-linux"
[ -d "$src" ] || die "unexpected archive layout: $src missing"
[ -f "$src/opensave-cli" ] || die "opensave-cli missing from the archive"

mkdir -p "$INSTALL_DIR" || die "could not create $INSTALL_DIR"

install_one() {
    name="$1"
    [ -f "$src/$name" ] || return 0
    cp "$src/$name" "$INSTALL_DIR/$name.new"
    chmod +x "$INSTALL_DIR/$name.new"
    # Replace atomically: overwriting a running binary in place fails on Linux
    # ("text file busy"), and a rename does not.
    mv -f "$INSTALL_DIR/$name.new" "$INSTALL_DIR/$name"
    say "  $INSTALL_DIR/$name"
}

say "Installing…"
install_one opensave-cli
install_one opensave-relay

installed_version="$("$INSTALL_DIR/opensave-cli" version 2>/dev/null || echo "unknown")"
say ""
say "Installed: $installed_version"

# ── Next steps ───────────────────────────────────────────────────────────

case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
        say ""
        say "NOTE: $INSTALL_DIR isn't on your PATH. Add it with:"
        say "  echo 'export PATH=\"\$PATH:$INSTALL_DIR\"' >> ~/.profile"
        say "  export PATH=\"\$PATH:$INSTALL_DIR\""
        ;;
esac

say ""
say "Next:"
say "  opensave-cli scan                 find your game saves"
say "  opensave-cli daemon start         run the sync service"
say "  opensave-cli service install      run it automatically on login"
say "  opensave-cli pair <other-device>  pair another machine"
say ""
say "On a Steam Deck, also run: sudo loginctl enable-linger \$USER"
