#!/bin/sh
# Registers OpenSave with your desktop environment: launcher entry + icon.
# Run from the extracted opensave-linux folder:  sh install-desktop.sh
set -e

HERE="$(cd "$(dirname "$0")" && pwd)"
APPS="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
ICONS="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/512x512/apps"

mkdir -p "$APPS" "$ICONS"
cp "$HERE/opensave.png" "$ICONS/opensave.png"

# Point Exec at the binary wherever the user extracted it.
sed "s|^Exec=.*|Exec=$HERE/opensave|" "$HERE/opensave.desktop" > "$APPS/opensave.desktop"
chmod +x "$APPS/opensave.desktop" "$HERE/opensave" 2>/dev/null || true

command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$APPS" || true
echo "OpenSave registered — it should now appear in your app launcher."
