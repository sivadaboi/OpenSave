#!/bin/sh
# OpenSave installer — headless CLI + daemon for Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/Liquid-co/OpenSave/main/scripts/install.sh | sh
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

REPO="Liquid-co/OpenSave"
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
    aarch64|arm64) arch="arm64" ;;
    *) die "unsupported architecture: $arch" ;;
esac

ASSET="opensave-linux-${arch}.tar.gz"

# The arm64 build is the headless pair only — the desktop app needs native
# WebKit, which doesn't cross-compile.
if [ "$arch" = "arm64" ]; then
    say "Note: arm64 ships the CLI and relay only (no desktop app)."
fi

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

# The archive's top-level directory differs by architecture
# (opensave-linux, opensave-linux-arm64), so find it rather than assume.
src=""
for candidate in "$tmp"/opensave-linux*; do
    if [ -d "$candidate" ] && [ -f "$candidate/opensave-cli" ]; then
        src="$candidate"
        break
    fi
done
[ -n "$src" ] || die "opensave-cli not found in $ASSET — unexpected archive layout"

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

# `opensave` is the name the docs, the man page and the tool's own help use,
# and what the Windows installer has always produced. `opensave-cli` stays as
# the real file because the systemd units, the .deb/.rpm layout and the Steam
# Deck plugin all look it up by that name.
link_alias() {
    alias_name="$1"
    target="$2"
    [ -f "$INSTALL_DIR/$target" ] || return 0

    # Never displace an unrelated command that happens to share the name —
    # "os" is short enough to collide with something the user installed.
    existing="$(command -v "$alias_name" 2>/dev/null || true)"
    if [ -n "$existing" ] && [ "$existing" != "$INSTALL_DIR/$alias_name" ]; then
        say "  (skipped '$alias_name' — already taken by $existing)"
        return 0
    fi

    # Symlink where possible; some filesystems (a FAT USB stick, a few
    # container overlays) refuse them, so fall back to a copy.
    if ! ln -sf "$target" "$INSTALL_DIR/$alias_name" 2>/dev/null; then
        cp -f "$INSTALL_DIR/$target" "$INSTALL_DIR/$alias_name"
    fi
    say "  $INSTALL_DIR/$alias_name"
}

link_alias opensave opensave-cli
link_alias os opensave-cli

installed_version="$("$INSTALL_DIR/opensave-cli" version 2>/dev/null || echo "unknown")"
say ""
say "Installed: $installed_version"

# ── PATH ─────────────────────────────────────────────────────────────────

# Put the install dir on PATH rather than printing instructions and hoping.
# Which file to write depends on the login shell: bash reads ~/.bashrc for
# interactive shells, zsh reads ~/.zshrc, and ~/.profile covers the rest.
add_to_path() {
    case ":$PATH:" in
        *":$INSTALL_DIR:"*) return 0 ;;
    esac

    line="export PATH=\"\$PATH:$INSTALL_DIR\""
    case "$(basename "${SHELL:-sh}")" in
        zsh)  rc="$HOME/.zshrc" ;;
        bash) rc="$HOME/.bashrc" ;;
        *)    rc="$HOME/.profile" ;;
    esac

    # Idempotent: re-running the installer must not stack duplicate lines.
    if [ -f "$rc" ] && grep -Fq "$INSTALL_DIR" "$rc" 2>/dev/null; then
        say "PATH already configured in $rc."
    elif {
        echo ""
        echo "# Added by the OpenSave installer"
        echo "$line"
    } >> "$rc" 2>/dev/null; then
        say "Added $INSTALL_DIR to PATH in $rc."
    else
        say "warning: could not write $rc — add this line yourself:"
        say "  $line"
    fi
    # This shell too, so the status panel below runs without reopening one.
    PATH="$PATH:$INSTALL_DIR"
    export PATH
    NEW_SHELL_NEEDED=1
}

NEW_SHELL_NEEDED=0
add_to_path

# ── Show what it found ───────────────────────────────────────────────────

say ""
"$INSTALL_DIR/opensave-cli" || true

if [ "$NEW_SHELL_NEEDED" = "1" ]; then
    say "Open a new terminal (or run: . $rc) for 'opensave' to work everywhere."
    say ""
fi

say "Next:"
say "  opensave scan                 find your game saves"
say "  opensave daemon start         run the sync service"
say "  opensave service install      run it automatically on login"
say "  opensave pair <other-device>  pair another machine"
say ""
say "'os' works as a short alias for 'opensave'."
say "On a Steam Deck, also run: sudo loginctl enable-linger \$USER"
