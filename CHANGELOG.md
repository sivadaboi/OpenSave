# Changelog

All notable changes to OpenSave are documented here. This project adheres to
[Semantic Versioning](https://semver.org/).

## [2.2.1] — 2026-08-02

Mostly conflicts and backups. Three separate faults could raise a conflict on
a save the other device had never touched, a fourth could skip a conflict that
should have been raised, and a backup made from the command line could not be
restored onto a new machine at all.

### Added

- **Snapshots you take yourself are no longer thrown away by the ones the app
  takes for you.** Retention kept the newest few snapshots per branch
  regardless of where they came from, so in a game that saves often — Elden
  Ring, Dragonsword: Awakening — a play session's worth of automatic backups
  filled the whole allowance within minutes and pushed out the snapshot you
  took on purpose before a boss. Manual and automatic snapshots now have
  separate allowances, and **manual ones are kept forever by default**, so a
  burst of automatic backups can only ever replace other automatic backups.
  Set a limit for them per game in its Configuration tab, or for new games
  under Settings → Snapshot history; from the command line,
  `opensave game <id> set max-manual-snapshots <n>` and
  `opensave config set manual-snapshot-limit <n>`, where 0 means keep
  everything. Cloud backups follow the same rule, so your off-site copy no
  longer ends up thinner than the machine it is backing up. Existing games
  pick this up on upgrade without being reconfigured.
- **Sign in to Google Drive or Dropbox from the command line.** Cloud backup
  previously needed the desktop app to authorise it, which left a Steam Deck
  in Game Mode or a headless install unable to set it up at all. `opensave
  cloud connect`, `cloud setup`, `cloud status` and `cloud disconnect` now
  cover the whole flow, alongside pushing, pulling, listing and deleting
  cloud saves.
- **See a paired device's games, and what a conflict actually is, from the
  command line.** `opensave peers games <peerId>` lists what another device
  tracks, and `opensave conflicts` shows which files differ and on which
  side rather than only that a conflict exists.
- **Set a game's Steam App ID by hand.** Cover art and cross-device matching
  both key off the App ID, and a game the scanner could not identify had no
  way to be told. Name matching also now recognises titles whose folder name
  has lost its spaces.
- **Emulators installed on another drive are found.** The scan looked for
  each emulator's per-user data folder, which is where an *installed* one
  keeps its saves — and is on C: whatever drive the emulator itself is on.
  RetroArch, the Citra forks (Azahar, Lime3DS) and the yuzu forks (Eden,
  Suyu) are all commonly unzipped somewhere instead, and then keep their
  saves inside that folder, so anyone with their emulators on D: got nothing
  from a scan at all. Those installs are now recognised where they actually
  live: at the top of any internal drive, or inside a folder named for the
  collection ("Emulators", "Emulation", "Games"). Adding the folder as a
  custom scan path works too, and now offers the emulator's *save* folders
  rather than the whole install with its cores, BIOS and ROMs. Reported by
  Erakodo on Windows 11.
- **Dismiss a save location from the scan results.** A stale or wrong
  location can be excluded permanently instead of being offered every time
  you scan.
- **Linking a game to its copy on another device offers that device's
  games.** Linking previously meant knowing and typing the other machine's
  id for the game, which is not something anyone has to hand.
- **The Steam Deck plugin ships with every release.** It had to be built from
  source before, which is not much use on a Deck.
- **Retention limits appear in `opensave status --json`.** A headless install
  had no way to confirm what it had just set.

### Fixed

- **Conflicts on saves the other device had never touched, after sending it a
  change.** Each device remembers the last state both were known to share, and
  judges a conflict by whether both have since moved away from it. After
  sending a change, that shared point is only updated when the other device
  confirms it caught up — and that confirmation travels the network, where it
  can be lost. When it was, the shared point stayed frozen at a state neither
  device held any more, and once it is behind both of them, *every* later
  edit reads as both sides having changed. The next ordinary save prompted a
  conflict on a file the other device had not opened. The state handed over
  is now recorded, so the next sync can prove the change landed by seeing the
  other device holding exactly it, with no confirmation needed.
- **The same thing after the other device deleted a file.** A deletion is
  applied directly — the file is removed and that is the end of it — so no
  sync runs on the receiving side and nothing updated its record of the
  shared state, which went on describing a save that still contained the
  deleted file. It corrected itself whenever some later sync happened along,
  which is why this only showed up as an occasional conflict long after the
  deletion. The record is now brought up to date when the deletion is
  applied.
- **A conflict that should have been raised could be skipped, overwriting the
  other device's copy without asking.** The record of what was last sent was
  kept after it was no longer relevant, so if the other device later returned
  to that exact state — rolling back to one of its own snapshots does
  precisely that — it looked unchanged, and this device pushed over the
  rollback instead of stopping to ask. The record is now cleared as soon as
  the two devices are known to agree on something newer.
- **A backup made from the command line could not be restored onto a new
  machine.** `opensave backup export` with no games named fell back to an
  older archive format that stores snapshot files and nothing else — no
  names, no save locations — so restoring it onto a fresh install matched
  nothing, restored nothing, and said it had succeeded. It now writes the
  same archive the desktop app does. Restoring one also tracks the games it
  contains, instead of putting the files back and leaving the library empty.
- **A save folder containing a single file was restored one directory too
  high.** Restoring decides whether a save is a folder or a single file by
  looking at where it is going, and on a machine that has never held that
  game there is nothing there to look at. A folder holding one save — which
  is most of them — is indistinguishable from a save that *is* one file, and
  the file was written into the parent directory: the save came back, one
  level above where the game reads it, reported as restored. Backups now
  record which of the two it was.
- **`opensave add` never took the first snapshot it promised.** Tracking a
  game snapshots whatever is already there, in the background so the desktop
  app stays responsive. From the command line the process exited before that
  finished and took the snapshot with it, so every game tracked from the
  command line started with no history at all — the one snapshot most worth
  having, of the save before you started playing.
- **Changes made from the command line did not reach a running app.** Adding
  a game, removing one, or turning auto-sync off while the desktop app was
  open left the app unaware: the game was tracked but watched by nobody, so
  it got no automatic snapshots and no automatic sync until the app was
  restarted, with nothing on screen to say so.
- **`opensave backup export` never said what it had captured.** It read a
  count the server does not send, so every backup — full or empty — reported
  the same bare success. Importing one was equally silent about how much came
  back, or that nothing had.
- **A game could end up tracked twice after being linked.** Auto-tracking a
  peer's game checks whether that game is already linked to one here before
  creating anything, but the check and the creation were separate steps and a
  link written in between was seen by neither: the link found no entry to
  absorb because it did not exist yet, and the auto-track had already decided
  no link existed. The game then appeared a second time under the peer's id,
  beside the entry it had just been linked to. Both now happen as one step.
- **A snapshot could be left half-written when the app closed.** Snapshots are
  started from several places — the watcher as a game saves, a sync following
  the peer onto another branch, a safety copy before a restore — and shutdown
  did not wait for one in progress. It carried on writing into a folder that
  was going away and recorded itself against a database that had already
  closed, so the snapshot was never really taken and nothing reported it.
- **Two snapshots taken in the same millisecond lost one of them.** Snapshot
  identifiers are built from the clock, so a sync snapshotting several games
  at once, or an automatic backup landing beside a manual one, could collide.
  The second failed and the backup silently did not happen.
- **Cloud backups could be left empty or truncated.** A backup interrupted
  part-way left a partial file in the cloud that would be treated as a good
  copy; the app now finishes an upload before shutting down, and repairs a
  remote copy whose size does not match instead of skipping it.
- **Switch emulator saves were offered as one enormous entry.** The scanner
  presented the whole NAND profile tree rather than each game's own save, so
  tracking one game meant tracking all of them.
- **Snapshot file sizes showed as 0 B, and restoring a single file did
  nothing.** Both were the command line reading fields by the wrong name —
  which produces an empty value rather than an error, so both looked like
  they had worked.
- **`opensave status --json` printed the ordinary status panel.** The flag
  was accepted and ignored, so anything scripting against it got a table of
  text where it expected JSON.
- **An update that could not be written reported success and then failed.**
  The check asked whether a temporary file could be created next to the app,
  which antivirus and Controlled Folder Access will happily allow while
  refusing the executable itself. It now attempts the exact file the update
  writes, and falls back to the installer when that is refused.

## [2.2.0] — 2026-07-28

### Added

- **Open a game's save folder from the app.** Every tracked game has a
  button that reveals its save location in Explorer, Finder or your Linux
  file manager — for checking what the scanner actually picked, or getting
  at a file by hand.
- **Match the same game across devices, even under different names.** A
  title tracked as "The First Berserker: Khazan" on one machine and a
  repack's folder name on another can now be linked, either automatically by
  Steam App ID or by linking the two entries yourself. App-ID matching is
  off by default, so two separate copies of the same game are never merged
  without you asking.
- **A completely rebuilt command line.** `opensave` is now a full headless
  client rather than a helper: auto-scan, tracking, pairing and approval,
  sync, conflict resolution, snapshots and branches, cloud backup, snapshot
  browsing and single-file restore, per-game settings, game linking, and
  running as a background service. It has styled output, a status panel on
  the bare command, shell completions for bash/zsh/fish, a man page, an
  installer on Windows and Linux, `--json` on every command for scripting,
  and `opensave update` to update itself. A Steam Deck in Game Mode or a
  headless server never needs the desktop app.
- **`opensave install` puts the CLI on your PATH from the binary itself.**
  The install scripts already did this, but the release also publishes the
  bare executable, and downloading that left you with a loose file to place
  and a PATH to edit by hand. Running `opensave install` now copies it
  somewhere permanent, sets up the `os` and `opensave-cli` aliases, and adds
  the directory to your PATH, so `opensave` works from any new terminal.
  `--dir` picks a different location.
- **Select several games at once.** Multi-select in the library for batch
  untracking, plus a "reset tracking" option that clears the library without
  touching your saves or snapshots.
- **Exclude folders from auto-scan.** Stale or wrong save locations can be
  dismissed permanently instead of being offered on every scan.
- **A Changelog page, and readable release notes.** The changelog lives
  under Settings in the sidebar, and the greeting after an update shows what
  changed since the version you were actually on.
- **Steam Deck: a rebuilt Decky plugin.** Sync, snapshot and resolve
  conflicts from Game Mode, with live sync progress and cover art in the
  panel, a daemon that starts itself, and a systemd service so syncing
  continues without opening anything.
- **Wider save detection.** Nine more emulators (PS1, PS4, Vita, Xbox,
  Dreamcast, 3DS and Switch forks), saves inside non-Steam Wine prefixes
  from Heroic, Bottles and Lutris, and save folders found by name inside a
  game's install directory.
- **Native Linux packages.** `.deb` and `.rpm` alongside the tarball and
  Flatpak.
- **A Support tab**, if you want to help fund the relay and the project.

### Changed

- **Large saves sync far faster and no longer load into memory first.**
  Blocks are written to disk as they arrive instead of being collected in
  full, so a 1 GB save no longer needs 1 GB of RAM before anything is
  written. Requests are pipelined rather than processed in fixed groups, and
  block data is compressed over the internet relay. A 40 MB save transfers
  in about a second on a LAN.
- **Auto-scan is quicker and quieter.** Cover-art fetches no longer hold up
  a scan, an unreachable Steam API costs one timeout instead of one per
  game, already-tracked saves are shown grouped below a divider rather than
  hidden, and shader caches are no longer offered as saves.
- **Cover art works on networks that block Steam.** When Steam's CDN can't
  be reached the app falls back to an image proxy automatically, and only
  warns about genuine connectivity problems rather than every game without
  published art.
- A sync interrupted by quitting the app is now cancelled cleanly instead of
  being left writing into the save folder during shutdown.

### Fixed

- **The Windows installer could rewrite unrelated parts of your PATH.** It
  set PATH through an API that hands back the *expanded* value and always
  writes a plain string back, so a PATH built from `%USERPROFILE%` or
  `%JAVA_HOME%` had those references replaced by whatever they pointed at
  during the install, and stopped following the variable afterwards. It also
  matched its own directory as a substring, so an unrelated folder with a
  similar name could convince it there was nothing to add. It now edits only
  the entry it owns and leaves the rest of the value as it found it.
- **Saves could be reported as conflicting when nothing about them
  disagreed.** A save's fingerprint covers its folders as well as its files,
  so a folder that existed on only one device was enough to make two
  otherwise identical saves look diverged — and a conflict would be raised
  asking which version to keep. There was nothing to choose between: the
  dialog had no differing file to name, so it listed none, and both sides
  showed the same file count, the same size and the same last-change time.
  A folder on one side only is now recognised for what it is and simply
  created on the other, as it always should have been. This is also why
  conflicts were appearing when the other device hadn't been touched.
- **The conflict dialog couldn't account for folders.** Where a real
  conflict involves a folder that exists on one side only, it is now listed
  alongside the differing files instead of being left out of the summary.
- **A device could stay online in the room while receiving nothing at all.**
  The relay gives every connected client a writer that drains its outbound
  queue. That writer stopped on any write error — including the send timeout
  a briefly stalled peer trips — while the reader kept accepting messages for
  it. Nothing drained the queue after that, so it filled, and every message
  bound for that device was dropped from then on. The device showed a healthy
  connection, kept sending its own heartbeats, and saw no peers and no syncs
  until something else closed the socket. The connection now closes when its
  writer stops, so the client reconnects instead of going quietly deaf.
- **A reinstalled device showed as offline forever.** Clearing a device's data
  gives it a new identity, and the pairing on the other machine still points
  at the old one — so its messages never match, and it sits at "offline"
  while plainly online and in the same room. Only unpairing and pairing again
  fixed it, with nothing on screen to suggest why. OpenSave now recognises
  this and says so, naming the device and the fix. It deliberately does not
  re-point the pairing on its own: pairing is what stops an unknown device
  reaching your saves, and adopting whatever turns up under a familiar name
  would give that away.
- **A paired device on both Wi-Fi and the internet relay could drop to
  offline while still connected.** Local discovery overwrote how the device
  was reached, quietly moving it out of the relay's care; when the local
  sighting then aged out, the device was marked offline even though the relay
  connection was live. Losing sight of a device on the local network now
  falls back to the relay instead of declaring it gone.
- **Auto-scan tracked the folder around your saves rather than the saves.**
  Steam and every Steam emulator keep the real save files in a `remote/`
  subfolder, with sync bookkeeping, achievements and playtime counters
  beside it; the scanner offered the parent. Those extra files change on
  every session independently on each device, so two machines diverged after
  every play with no save having changed — reported as both "it didn't find
  the exact save location" and "it's a bit glitchy". Containers wrapping a
  game's own save tree are unwrapped too.
- **Games matched across devices could agree what to sync, then fail to sync
  it.** App-ID matching and manual links were applied when a peer asked for a
  manifest and nowhere else, so the actual file transfer came back "Game not
  found" and no save data moved.
- **Pairing could complete on only one device.** The device starting the
  pairing tells the other which port to call back on, and a daemon that had
  fallen back to a different port advertised one nothing was listening on —
  so the approval succeeded on one machine and the other showed no paired
  devices at all. Internet sync kept working in that state, which made it
  look like a LAN-only fault. A callback that can't get through is now
  reported instead of passing silently.
- **The internet relay dropped every device every 15-30 minutes.** The host
  sleeps an idle instance and WebSocket traffic doesn't count as activity, so
  the relay went to sleep underneath live connections. Connected devices keep
  it awake now, reconnect with backoff instead of retrying in lockstep, and a
  routine reconnect no longer fills the activity log with warnings.
- **The relay could be taken down by a single stalled device.** Its outbound
  queue was bounded by message count while a message can be 16 MB, so one
  peer that stopped reading could make it buffer gigabytes.
- **A newer database no longer stops an older build from starting.**
- **Sync no longer guesses when two tracked games share an App ID.** Picking
  one could drop a peer's saves into the wrong folder, so it says so and lets
  you link the right pair yourself.
- Several save locations for the same game can be tracked separately.
- The auto-scan window no longer closes when you click into its search box —
  and the same fix applies to every other dialog.
- `opensave pair requests` shows which device is asking, instead of a bare
  id you had to approve blind.

## [2.1.1] — 2026-07-20

### Fixed

- **Conflict resolutions now stick.** Resolving a save conflict (Keep
  both / Keep mine / Keep theirs) records the agreed state so the same
  conflict can't re-appear on the next sync — fixing an endless
  re-prompting loop. "Keep mine" now also propagates your version to the
  peer instead of leaving the two devices permanently diverged.
- **Adding a game now shows up on your other devices immediately.**
  Tracking a game syncs it to paired peers right away (they auto-track
  it) instead of waiting for the periodic reconcile.
- **Untrack is now two-way and recoverable.** Untracking a game removes
  it on paired devices too and doesn't bounce back; re-tracking it on any
  device restores it on all of them.
- A peer that isn't tracking a game no longer produces an endless "Game
  not found" retry loop in the log.
- **Empty snapshots are caught, not silently backed up.** A snapshot
  with no files (almost always a mis-tracked save path) is flagged in
  Activity and never mirrored to the cloud. WebDAV uploads are verified
  after the fact, so a truncated/empty upload fails loudly.
- The in-app logo (title bar, About, boot screen) now shows the new icon.
- **Auto-scan no longer offers a game's whole install folder as its
  "save".** Games that keep their save file directly in the install
  directory (over 1,100 manifest entries — e.g. Sonic & Sega All-Stars
  Racing's `ssr_save.bin`) previously widened to the entire multi-GB
  install dir, which then got snapshotted and mirrored to cloud. The
  scanner now tracks the save file itself; single-file saves are fully
  supported by watch, snapshot, and sync.
- Save files sitting directly in broad folders like Documents are now
  offered as single-file saves instead of being skipped entirely.
- **Relay: large save transfers no longer kill the connection.** The
  relay's WebSocket message limit (32 KB by default) was far below a
  sync block (~2.7 MB), so every real transfer dropped the link and
  looped on reconnect-retry. The public relay is already fixed; this
  release carries the fix into the bundled `opensave-relay` and the
  in-app "host a relay" feature.
- **Handheld launch crashes fixed** (ROG Ally-class devices): WebKit's
  DMA-BUF renderer is disabled by default on Linux (it crashes on a
  range of GPU drivers; an explicitly set WEBKIT_DISABLE_DMABUF_RENDERER
  is respected), and the Flatpak moved to the GNOME 49 runtime, whose
  Mesa supports current handheld APUs. The tray icon now also works
  inside the Flatpak sandbox.
- Auto-scan no longer floods results with identical tiles from a busy
  Proton prefix (the "38× Persona 3 Reload" report): the precise
  manifest pass runs first, the coarse prefix listing defers to it,
  vendor/middleware folders are excluded, and entries keep their
  "(subfolder)" qualifier when renamed.
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

- **Snapshot & branch management.** Delete individual snapshots or whole
  branches from a game's tabs, and a one-click "Clean up now" in Settings
  that prunes every game to its limit across all branches (and sweeps
  abandoned `conflict-*` branches left by resolved conflicts).
- **Snapshot retention controls.** A global default limit — now **20
  snapshots per game** — set in Settings, plus a per-game override in
  each game's Configuration tab. Retention now applies to every branch,
  not just the active one.
- **Yuzu-family Switch emulators.** Auto-scan now detects Suyu, Sudachi,
  Citron, and Eden alongside Yuzu and Ryujinx.
- **Choose-what-to-export save backups.** "Export saves…" now opens a
  picker listing every save on the machine — tracked games plus
  auto-detected ones — with select-all / tracked-only shortcuts. The
  .sscb file carries each game's current save AND where it belongs
  (paths stored in a machine-portable form, so a different PC or user
  account restores to the right place).
- **Two import modes.** "Add to snapshots" (the default) imports the
  saves into snapshot history without touching a single live file;
  "Overwrite current saves" restores everything onto disk — tracked
  games get a safety snapshot first, untracked targets get a safety zip
  in the backups folder before anything is replaced. The Activity tab
  records every game: what was restored, to which path, tracked or not.
- **Steam Deck: official Flatpak.** Every release now ships an
  `OpenSave.flatpak` that runs on stock SteamOS — no system packages, no
  lost install after SteamOS updates (the GNOME runtime provides the
  WebKit the app needs). SD-card saves (`/run/media`) are visible to the
  sandbox, and the in-app updater is Flatpak-aware (points at the new
  bundle instead of trying to self-swap the read-only install).
- **EmuDeck detection.** Auto-scan finds the `Emulation/saves` tree
  EmuDeck routes every emulator into — internal storage and SD card —
  offering each emulator as its own entry ("EmuDeck (retroarch)").
- **New app icon** — the pixel-art OS logo, across the app, installer,
  tray, and website.
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

[2.1.0]: https://github.com/Liquid-co/OpenSave/releases/tag/v2.1.0
[2.0.1]: https://github.com/Liquid-co/OpenSave/releases/tag/v2.0.1
[2.0.0]: https://github.com/Liquid-co/OpenSave/releases/tag/v2.0.0
