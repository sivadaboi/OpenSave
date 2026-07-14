# OpenSave — User Guide

OpenSave keeps your game saves in sync across your devices, peer-to-peer. No
accounts, no subscriptions. This guide walks through everyday use.

## 1. First run

When you open OpenSave the first time your library is empty. Two ways to add
games:

- **🔍 Auto-scan** — finds saves from Steam, emulators (RetroArch, Dolphin,
  Ryujinx, Yuzu, Citra, PCSX2, RPCS3, PPSSPP, Cemu, Xenia), Steam-emulator
  repacks (Goldberg, CODEX, RUNE, …), Epic, GOG, and Unreal games. Pick the
  ones you want and click **Track selected**.
- **+ Track folder** — point OpenSave at any save folder or single save file
  manually.

Tracked games appear in the sidebar and on the Home grid.

## 2. Snapshots & restore

Every time a save changes, OpenSave takes a **snapshot** automatically (only
changed blocks are stored, so history is cheap). Open a game to:

- **Snapshot now** — take a manual snapshot with a comment.
- **Restore** — roll the whole save back to any snapshot. Your current state
  is snapshotted first, so a restore is always reversible.
- **Browse files** — restore a single file out of a snapshot.
- **Branches** — keep parallel playthroughs (e.g. `main` and `ng-plus`).
  Switching branches snapshots the current save first, then restores the
  other branch's latest state.

## 3. Syncing between devices

### On the same network (LAN)

1. Install OpenSave on both devices.
2. Open **Devices** — they discover each other automatically over the LAN.
3. Click **Pair**; approve the request on the other device.
4. Paired devices sync tracked games automatically.

If discovery is blocked, use **Connect via IP** with the other device's LAN
IP and port (default `8383`).

### Across the internet (relay)

1. Open **Internet Sync** and either generate a **room code** or paste one
   shared by a friend.
2. Both devices join the same room code — saves sync through the relay.
3. No port forwarding needed. To self-host the relay, enable **Host relay on
   this device** under Settings → Sync and forward the relay port.

### Conflicts

If the same save changed on two devices independently, OpenSave detects it
from sync lineage (not clocks) and asks you to **keep yours, theirs, or both**
(both creates a new branch). Nothing is overwritten without your choice.

## 4. Cloud backup (optional)

Open **Cloud Backup** to mirror snapshots to Google Drive, Dropbox, OneDrive,
WebDAV, a webhook, or a local/NAS folder. Sign in, flip the toggle, and every
new snapshot uploads in the background. Use **Browse cloud** to explore and
restore snapshots per game.

> Note: the built-in Google Drive credentials use a shared OAuth app that may
> expire weekly. For always-on cloud sync, enter your own Client ID under the
> "Custom OAuth Client ID" box, or use Dropbox / WebDAV / a local folder.

## 5. Settings

- **General** — device name/type shown to peers, start-on-boot.
- **Sync** — auto-sync on track, bandwidth limit, relay URL, relay hosting.
- **Storage** — snapshot folder, pre-sync safety-backup folder, retention,
  extra scan folders.
- **Advanced** — daemon port, cross-platform path translation rules (e.g.
  rewrite `C:\Users\me\Saves` → `/home/deck/saves`).

## 6. Tray & background

Closing the window **hides OpenSave to the system tray** so syncing keeps
running. Right-click the tray icon to reopen, sync all games, or quit.

## 7. Troubleshooting

- **"404 Not Found" on launch** — another program is using port `8383`. Quit
  the other OpenSave (or app on that port), or change the port in Settings →
  Advanced.
- **Devices don't see each other** — make sure both are on a *Private* network
  profile and OpenSave is allowed through the firewall; otherwise pair by IP.
- **Cloud upload fails with "session expired"** — reconnect the provider under
  Cloud Backup (see the Google note above).
- **Steam Deck / Game Mode** — a Decky Loader plugin lives in
  `opensave-decky-plugin/`.
