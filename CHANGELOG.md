# Changelog

All notable changes to OpenSave are documented here. This project adheres to
[Semantic Versioning](https://semver.org/).

## [2.0.1] — 2026-07-15

First update delivered through the in-app updater. If you installed 2.0.0,
the update banner will offer this release — one click installs it.

### Fixed

- A provider card (e.g. Local Folder) no longer shows "Connected" off the
  OAuth tokens belonging to a different provider.
- Non-app binaries (CLI, relay) report the correct version.
- GitHub releases are titled "OpenSave vX.Y.Z" instead of the bare tag.

### Notes

- Early 2.0.0 downloads predate the final 2.0.0 build; this release brings
  every install to a known-good state via the in-app updater.

## [2.0.0] — 2026-07-14

Complete rewrite of OpenSave from Node.js/Electron to **Go + Wails**: one small
native binary with no runtime to install. Wire-compatible with the original —
Go and JS devices sync together (same REST routes, P2P protocol, UDP discovery,
and relay envelope).

### Added

- Native desktop app (Wails webview) with a Hydra-style dark UI; system-tray
  background running.
- Update OpenSave from inside the app: one-click install from GitHub
  releases, or pull a newer build directly from a paired device ("Update
  from this device" on the Devices page) — no more copying the exe around.
- Release notes shown in the update banner, and the full changelog in the
  About dialog ("What's new").
- Activity log also written to `~/.opensave/opensave.log` for
  after-the-fact diagnosis.
- In-app styled confirmation dialogs replace the bare browser popups.
- Auto-scan for Steam, emulator, repack, Epic, GOG, and Unreal saves, shown as
  a browsable grid of vertical Steam cover art.
- Block-level delta sync (SHA-256, adaptive 64 KB–2 MB blocks) — only changed
  blocks transfer.
- Snapshot history with per-branch timelines, whole-save and single-file
  restore, and an automatic safety snapshot before every restore.
- Lineage-based conflict resolution (keep local / remote / both-as-branch).
- P2P over LAN (zero-config UDP discovery) and internet (relay room codes),
  with an option to self-host the relay.
- Cloud backup to Google Drive, Dropbox, OneDrive, WebDAV, webhook, or a
  local/NAS folder, with a per-game cloud snapshot browser.
- Cloud snapshot browser: cover-art tile grid (like auto-scan) with per-game
  drill-in, restore, delete, upload, live upload progress, and In
  cloud / Not uploaded filters.
- Google Drive snapshots now live in an auto-created "OpenSave" folder
  instead of the Drive root (override with a folder ID in Settings).
- Cloud mirroring is on by default; the toggle, Drive folder ID, and custom
  OAuth client IDs moved to Settings → Sync.
- In-app About dialog and an optional "update available" banner.
- First-run welcome with guided next steps.

### Fixed

- Cross-origin (CORS) preflight is handled, so tracking games from the UI no
  longer fails with "Failed to fetch".
- Cloud sync self-heals a revoked/expired OAuth token instead of falsely
  showing "connected", and prompts you to reconnect.
- Cover-art image error handling no longer risks a UI freeze; the sidebar,
  cards, and detail header fall back cleanly.
- Per-game view state no longer leaks between games in the detail view.
- A failed download could delete the original file on the sending device
  (sync lineage now only counts files verifiably present on both sides).
- Leftover `.opensave.tmp` files from interrupted transfers no longer sync
  to other devices; stale ones are cleaned up automatically.
- Antivirus briefly locking freshly-written files (especially `.exe`) no
  longer fails the sync — the final rename retries for several seconds.
- Save paths pointing at profile/system folders are refused with a clear
  message instead of failing every sync on a Windows junction.
- Resolving a save conflict no longer freezes the app during long
  transfers; progress updates during large files instead of sitting at 0%.
- Clear full-screen error (with Retry) when the window can't reach the
  background service, instead of endless "Loading…" panels.

### Security / safety

- Local and single-file restores now confirm before overwriting the current
  save (the current state is snapshotted first).
- The local API and dashboard remain loopback-only; relay traffic is limited
  to paired peers.

[2.0.1]: https://github.com/sivadaboi/OpenSave/releases/tag/v2.0.1
[2.0.0]: https://github.com/sivadaboi/OpenSave/releases/tag/v2.0.0
