# OpenSave 2.0.0 — the Go rewrite

OpenSave has been rewritten from the ground up in **Go**. The Electron app is gone: 2.0 is one small native binary with an embedded UI — faster to start, lighter on RAM, nothing to install but the app itself.

**Your data comes with you.** On first launch, 2.0 imports everything from the original app — tracked games, snapshots, pairings, and cloud settings (`opensave-db.json` is kept as a backup, never deleted). Old (1.x) and new (2.0) devices speak the same wire protocol, so a mixed fleet keeps syncing while you upgrade one machine at a time.

## Highlights

- **One native binary** — Wails desktop app with a dark UI and system-tray background running; no Node, no Electron, no runtime.
- **Auto-scan that actually finds things** — Steam (real library folders), emulators (RetroArch, Dolphin, Ryujinx, Yuzu, Citra, PCSX2, RPCS3, PPSSPP, Cemu, Xenia), Goldberg/CODEX/RUNE-style repacks, Epic, GOG, Unity `LocalLow`, and Unreal saves — shown as a grid of cover-art tiles.
- **Block-level delta sync** — SHA-256, adaptive 64 KB–2 MB blocks; only changed blocks ever transfer.
- **Snapshot history with branches** — whole-save or single-file restore, an automatic safety snapshot before every restore, and branches for parallel playthroughs.
- **Lineage-based conflict resolution** — keep local, keep remote, or keep both as a branch. Detected by sync history, not wall clocks. Fully undoable.
- **P2P everywhere** — zero-config LAN discovery, or internet sync via relay room codes (self-hostable relay included).
- **Cloud backup (optional)** — Google Drive, Dropbox, OneDrive, WebDAV, webhook, or a local/NAS folder, with a browsable per-game cloud snapshot explorer.
- **In-app updates** — one-click install from GitHub releases, or pull a newer build directly from a paired device.
- **Headless CLI** — `opensave-cli` covers scan/add/status/snapshot/rollback/branch/checkout for servers and Steam Deck.

## Reliability & safety

- Conflict resolution never overwrites a peer without consent; restores confirm first and snapshot the current state.
- Interrupted syncs auto-retry; leftover temp files are cleaned up and never propagate.
- Sync lineage only counts files verifiably present on both sides — a failed download can no longer delete the original.
- Antivirus file-lock races are retried instead of failing the sync.
- Periodic reconcile backstop guarantees cross-device changes are always detected.
- Activity log at `~/.opensave/opensave.log`; clear full-screen errors (with Retry) instead of endless loading panels.

## Downloads

| Platform | File |
|---|---|
| Windows (installer) | `OpenSave.Setup.exe` |
| Windows (portable) | `OpenSave.exe` |
| Linux / Steam Deck | `opensave-linux-amd64.tar.gz` |
| CLI + relay | included in both platform bundles |

Full details in the [Changelog](https://github.com/sivadaboi/OpenSave/blob/main/CHANGELOG.md) and the [User Guide](https://github.com/sivadaboi/OpenSave/blob/main/USER_GUIDE.md).
