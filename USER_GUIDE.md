# OpenSave — User Guide

OpenSave keeps your game saves in sync across your devices, peer-to-peer. No
accounts, no subscriptions. This guide walks through everyday use.

> **New to OpenSave?** Start with [Getting Started](GETTING_STARTED.md) — it
> covers the same ground from a fresh install and explains the terms as it
> goes. This page is the reference for when you know what you are looking for.

## 1. First run

When you open OpenSave the first time your library is empty. Two ways to add
games:

- **🔍 Auto-scan** — finds saves from Steam, emulators (RetroArch, Dolphin,
  Ryujinx, Yuzu, Citra, PCSX2, RPCS3, PPSSPP, Cemu, Xenia), Steam-emulator
  repacks (Goldberg, CODEX, RUNE, …), Epic, GOG, and Unreal games. Pick the
  ones you want and click **Track selected**.
- **+ Track folder** — point OpenSave at any save folder or single save file
  manually.

### Reading the scan results

Each result shows how many files the folder holds, how big it is, and when it
was last written. That last one is the useful one: the same game is often
detected in three or four places — the Steam folder, wherever the launcher put
it, and one left behind by an install you have moved on from — and the one
with a recent date is the live save.

**One tile per game.** When a game is found in several places you get a single
tile with **found in N folders** underneath it. Click that to see them all,
each labelled with what OpenSave thinks it is:

| Label | What it means |
|---|---|
| **the save folder** | the one to track — the freshest of the group |
| **part of the same save** | sits beside the save folder and holds another piece of it. Ticked by default, tracked as one game |
| **another copy — probably an old install** | the same game somewhere unrelated. Not ticked; tick it if it is the one you actually play |
| **already inside the folder above** | covered already, so it cannot be ticked |

Clicking a tile ticks the save folder plus anything marked *part of the same
save*, and **Track** adds them as one game with extra locations — not as
several games that share a name. Open the list to overrule any of it.

If OpenSave puts two folders in different tiles when they are really one game
— it has nothing to match on when the names differ and there is no Steam AppID
— tick both and use **Track as one game** at the bottom.

Folders holding no files are hidden, because Steam creates one for every game
you own whether or not saves go there; on a typical library that is a fifth of
the results. Tick **Show N empty** in the scan toolbar to see them, which is
worth doing if you want to track a game before it has saved for the first
time. From the command line, `opensave scan --all` does the same.

Tracked games appear in the sidebar and on the Home grid.

### A game whose save is split across folders

Some games keep their save data in one place and their settings or mods in
another. Open the game, go to **Configuration -> Save locations**, name the
extra folder and pick it. It is then synced, snapshotted and restored along
with the main one.

Give the location the **same name** on your other devices. The name is what
the two sides match on -- the folder itself lives somewhere different on each
machine, so the path cannot be. A location whose name a device knows but has
no folder for is shown as needing one, and is skipped until you choose it.

Removing a location never deletes its files; it only stops OpenSave covering
them. From the command line:

```
opensave locations <gameId>
opensave locations add <gameId> config "C:/Users/me/Documents/Game"
opensave locations remove <gameId> config
```

### Files that shouldn't sync

Some games keep device-specific settings in the same folder as the save, and
copying those to another machine can break the game there. Open the game, go
to **Configuration -> Files that shouldn't sync**, and list them one per line,
like a `.gitignore`:

```
Config.gs      # a file of that name, at any depth
/Config.gs     # only at the top of the save folder
*.log          # by extension
logs/          # a folder and everything in it
!keep.log      # an exception to an earlier line
```

Matching ignores case. Set the same list on your other devices -- each applies
its own, and a device without the list is unaffected.

You don't have to know the filenames in advance. Click **Pick from your save
folder** under the box to list what is actually in there, each file marked
**syncs** or **won't sync**. Tick one and the pattern is written for you,
anchored so it can only mean that file; untick one caught by a wildcard and an
`!` exception is added instead of the wildcard being thrown away. The verdicts
come from the same matcher the sync itself uses, and they update as you type,
so you can see a rule working before you rely on it.

A pattern applies to every one of a game's folders, so if the same filename
appears in two save locations it is excluded in both.

**Snapshots still capture these files**, so a restore brings them back.
Excluding something stops it travelling between devices; it never stops it
being backed up. From the command line:

```
opensave ignore <gameId>
opensave ignore <gameId> add Config.gs
opensave ignore <gameId> test Config.gs      # would this sync?
```

## 2. Snapshots & restore

Every time a save changes, OpenSave takes a **snapshot** automatically (only
changed blocks are stored, so history is cheap). Open a game to:

- **Snapshot now** — take a manual snapshot with a comment.
- **Restore** — roll the whole save back to any snapshot. Your current state
  is snapshotted first, so a restore is always reversible.
- **Browse files** — restore a single file out of a snapshot.
- **Branches** — keep parallel playthroughs (e.g. `main` and `ng-plus`).
  When you create one you choose what it starts from: **your current save**
  (the default — switching to it changes nothing until you play) or **empty**,
  for a genuinely fresh run, where switching to it clears the save folder.
  Switching always snapshots the current save onto the branch you're leaving
  first, so it can be undone by switching back — and if that snapshot can't be
  taken, nothing is changed at all.

## 3. Syncing between devices

### On the same network (LAN)

1. Install OpenSave on both devices.
2. Open **Devices** — they discover each other automatically over the LAN.
3. Click **Pair**; approve the request on the other device.
4. Paired devices sync tracked games automatically.

If discovery is blocked, use **Devices -> Add by IP address** with the other
device's LAN IP and port (default `8383`), then **Send pairing request**.

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
> expire weekly, so Drive can ask you to reconnect. Dropbox, WebDAV and a
> local/NAS folder do not have this problem. Your own Google Client ID avoids
> the expiry, but there is no field for it in the app yet -- it has to be set
> through the local API:
>
> ```
> curl -X POST http://127.0.0.1:8383/api/settings >   -H 'Content-Type: application/json' >   -d '{"cloudSync":{"customClientIds":{"google_drive":"YOUR-ID"}}}'
> ```

## 5. Settings

- **General** — device name/type shown to peers, start-on-boot.
- **Sync** — auto-sync on track, bandwidth limit, relay URL, relay hosting.
- **Storage** — snapshot folder, pre-sync safety-backup folder, retention,
  extra scan folders.
- **Updates** — whether to be offered beta builds.
- **Advanced** — daemon port, cross-platform path translation rules (e.g.
  rewrite `C:\Users\me\Saves` → `/home/deck/saves`).

### Beta builds

By default OpenSave only offers you published releases. Tick **Offer me beta
versions** under Settings → Updates to be offered pre-releases as well, if you
want to try what is coming and report on it.

If you are already running a beta, that happens automatically — you do not need
to find this setting first, and you will still be offered the stable release the
moment it is newer than your build. From the command line:

```
opensave config set update-channel beta      # or stable
opensave update --check
```

### Snapshot retention

Automatic and manual snapshots have separate allowances, so a game that saves
often cannot push out a snapshot you took on purpose.

- **Automatic snapshots to keep** — how many of the backups taken as the game
  saves are kept, per branch. 0 keeps them all.
- **Manual snapshots to keep** — the ones you took yourself. **0 keeps them
  forever, and that is the default.**

Both are set per game in its Configuration tab, or for newly tracked games
under Settings → Snapshot history. From the command line:

```
opensave game <gameId> set max-snapshots 20
opensave game <gameId> set max-manual-snapshots 0     # 0 = keep forever
opensave config set snapshot-limit 20                 # default for new games
opensave config set manual-snapshot-limit 0
```

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

## 8. Getting help

Still stuck, or want to ask for something? **[Join the OpenSave Discord →](https://discord.gg/hvBv92DZvn)**
It is the quickest way to get an answer, and where new builds are discussed
before they ship. Bugs are also welcome on
[GitHub issues](https://github.com/Liquid-co/OpenSave/issues).
