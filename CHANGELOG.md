# Changelog

All notable changes to OpenSave are documented here. This project adheres to
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed

- **Content-based conflict detection.** Sync now records the manifest
  hash both devices verifiably held at each convergence (a merge-base,
  like git) and flags a conflict only when BOTH sides changed relative to
  it — replacing wall-clock mtime comparisons that had a blind window
  right after each sync under clock skew.
- Two devices that start with identical saves no longer hit a false
  "both sides modified" conflict on the first change: an in-sync check
  now records the shared state on both peers, not just the initiator.
- Unpairing a device now proactively notifies it (LAN and relay), so the
  other side stops treating you as paired immediately — no more ghost
  sync attempts or phantom "1 sync in progress" after an unpair.
- Sync progress can no longer stick at "0%" forever: a per-peer watchdog
  caps each sync pass, and the dashboard clears stalled sync indicators
  on its own (the backend retry loop still re-syncs automatically).
- Linux: the app window/taskbar icon now shows correctly, and the Linux
  tarball ships a launcher entry + icon with a one-line
  `install-desktop.sh` for app-menu integration.

### Added

- **System tray on Linux** (StatusNotifier/D-Bus): close-to-tray with
  Open / Sync all / Quit, matching Windows. On desktops without a tray
  host (stock GNOME without an extension), closing the window quits
  normally instead of stranding a hidden app.

## [2.1.0] — 2026-07-16

### Added

- **Linux & Steam Deck save detection.** Auto-scan is now platform-aware:
  - Emulator saves are found at their real Linux locations (native and
    Flatpak) — RetroArch, Dolphin, PCSX2, RPCS3, Ryujinx, yuzu, Citra,
    Cemu, PPSSPP.
  - **Proton prefixes are scanned**: games run through Proton store their
    saves in `steamapps/compatdata/<appid>/pfx`, and OpenSave now finds
    them (with the game's Steam cover art) — the bulk of Steam Deck saves.
  - The Ludusavi manifest resolves native Linux paths (`<xdgData>`,
    `<xdgConfig>`, `<home>`) and expands Windows-path entries inside each
    Proton prefix.
- The in-app updater is OS-aware: it installs the Linux tarball build on
  Linux (extracting the app binary) and the portable exe on Windows, and
  only ever applies a binary matching the running platform.


- Auto-scan now uses the community-maintained
  [Ludusavi manifest](https://github.com/mtkennerly/ludusavi-manifest)
  (sourced from PCGamingWiki): save locations for tens of thousands of
  games, detected purely by path — Steam, GOG, Epic, itch, and
  repack/cracked installs alike. A compressed snapshot (20k+ games,
  <1 MB) ships inside the binary, so scanning works instantly and fully
  offline; fresher manifest data downloads in the background at most
  once a week and takes precedence when present.
- More Steam-emulator/repack save locations detected: GSE (Goldberg
  fork), EMPRESS, Online-Fix, CPY, SmartSteamEmu, SKIDROW, and 3DM
  wrappers, alongside the existing Goldberg/CODEX/RUNE/Tenoke/FLT set.
- Large files are first-class: uploads and downloads stream from disk
  (memory use no longer scales with file size), Google Drive uses
  resumable uploads, and Dropbox/OneDrive switch to chunked upload
  sessions past their single-request limits — a 600 MB save moves
  through snapshot + cloud upload with ~1 MB of extra memory.
- Untracking a game now offers to delete its cloud snapshots too, so
  orphaned files no longer pile up in the provider.

### Fixed

- A save change made while a sync was already running is no longer lost
  until the periodic reconcile: the request queues a follow-up pass that
  runs when the active sync finishes (previously, deleting or changing a
  file mid-sync could silently skip propagation for minutes).
- Tracking a folder no longer blocks the app: path validation refuses
  nonexistent paths, drive roots, whole-profile/system folders, and
  OpenSave's own data directory with a clear message; the same folder
  can't be tracked twice; and the initial snapshot runs in the
  background (tracking a huge folder previously froze the API for
  minutes and could wedge the file watcher until restart).
- The file-watcher engine no longer holds its global lock during
  recursive directory walks — one slow watch can't freeze every other
  game's tracking operations.
- A snapshot no longer fails outright when a single file is unreadable
  (locked by the game or antivirus): unreadable files are skipped with
  a warning, and only a fully unreadable save is an error.
- Watcher auto-snapshots now push a live update to the dashboard.
- The "What's new" greeting no longer announces an update when only the
  build timestamp changed.

## [2.0.1] — 2026-07-15

First update delivered through the in-app updater. If you installed 2.0.0,
the update banner will offer this release — one click installs it.

### Fixed

- In-app update now works for installed (Program Files) copies: when the
  app can't replace its own files, it downloads the installer and launches
  it instead (UAC prompt) rather than failing with "Access is denied".
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

[2.1.0]: https://github.com/sivadaboi/OpenSave/releases/tag/v2.1.0
[2.0.1]: https://github.com/sivadaboi/OpenSave/releases/tag/v2.0.1
[2.0.0]: https://github.com/sivadaboi/OpenSave/releases/tag/v2.0.0
