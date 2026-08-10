# Getting Started with OpenSave

This guide assumes you have never used OpenSave and starts from nothing. It
explains what each thing means as it comes up. If you want the short version,
the [README](README.md) has a four-step quick start; if you want the reference,
the [User Guide](USER_GUIDE.md) covers every setting.

**Contents**

1. [What OpenSave actually does](#1-what-opensave-actually-does)
2. [Installing it](#2-installing-it)
3. [First run: finding your saves](#3-first-run-finding-your-saves)
4. [Reading the scan results](#4-reading-the-scan-results)
5. [Adding your second device](#5-adding-your-second-device)
6. [What happens while you play](#6-what-happens-while-you-play)
7. [Getting an old save back](#7-getting-an-old-save-back)
8. [Branches: two playthroughs at once](#8-branches-two-playthroughs-at-once)
9. [When two devices disagree](#9-when-two-devices-disagree)
10. [Games with awkward saves](#10-games-with-awkward-saves)
11. [Steam Deck](#11-steam-deck)
12. [Cloud backup (optional)](#12-cloud-backup-optional)
13. [Moving to a new PC](#13-moving-to-a-new-pc)
14. [Running your own relay](#14-running-your-own-relay)
15. [Every setting, and what it does](#15-every-setting-and-what-it-does)
16. [Odds and ends](#16-odds-and-ends)
17. [Doing all this from a terminal](#17-doing-all-this-from-a-terminal)
18. [When something looks wrong](#18-when-something-looks-wrong)
19. [Glossary](#19-glossary)

---

## 1. What OpenSave actually does

You play a game on your desktop. Later you want to carry on from your laptop,
or your Steam Deck. Unless the game supports Steam Cloud, your progress does
not follow you — the save is a folder of files sitting on one machine.

OpenSave watches those folders. When a save changes, it does two things:

- **Keeps a copy** of how the folder looked, so you can go back to it.
- **Sends the change to your other devices**, so they have the same save.

Two things it deliberately does *not* do:

- **There is no account and no server.** Your devices talk to each other
  directly. Nothing is uploaded anywhere unless you switch on cloud backup
  yourself.
- **It never overwrites without being sure.** If two devices changed the same
  save independently, it stops and asks you, rather than picking one.

It works for any game whose save is a folder or a file on disk — bought
anywhere, or installed from outside a store entirely. It also handles
emulators.

## 2. Installing it

Download the latest build from the
[releases page](https://github.com/Liquid-co/OpenSave/releases).

**Windows** — download `OpenSave.exe` and run it. There is no installer and
nothing goes in the registry.

> Windows SmartScreen may warn you because the build is not code-signed
> (signing certificates cost money this project does not have). Click **More
> info → Run anyway** if you are comfortable with that.

**Linux** — download the tarball, extract it, and run `./OpenSave`.

**Steam Deck** — see [section 11](#11-steam-deck).

Do this on **each device** you want to sync. They all need the app.

## 3. First run: finding your saves

Open OpenSave. Your library is empty. There are two ways to fill it.

### Auto-scan (start here)

Click **🔍 Auto-scan**. OpenSave looks in all the usual places — Steam
libraries, Proton and Wine prefixes, emulator folders, the Windows locations
games conventionally use — and checks them against a community-maintained list
of save paths covering tens of thousands of titles.

It takes a few seconds and then shows what it found, as a grid of cover art.

### Track a folder yourself

If a game is not found, click **+ Track folder** and point OpenSave at the
folder (or single file) the game saves into. This always works; auto-scan is
only a convenience.

Not sure where a game saves? [PCGamingWiki](https://www.pcgamingwiki.com) lists
save paths for most games — search the title and look for "Save game data
location".

## 4. Reading the scan results

This screen is where most of the confusion happens, so it is worth
understanding. Every tile is **one game**, and underneath it says what the
folder holds:

```
Balatro
7 files · 20.4 KB · 19d ago
```

That last part is the useful bit. **A game is often found in three or four
different places** — the Steam folder, wherever your launcher put it, and one
left behind by an install you have moved on from. They all look plausible. The
date tells you which one you are actually playing.

### "found in N folders"

When a game turns up in more than one place, its tile says **found in 3
folders**. Click that and you get the list, each folder labelled:

| Label | What it means | Ticked by default? |
| --- | --- | --- |
| **the save folder** | The one to track. OpenSave picked the folder with the most recent files | Yes |
| **part of the same save** | Sits right beside the save folder and holds another piece of it — settings, mods, profiles. These get tracked *together* as one game | Yes |
| **another copy — probably an old install** | The same game somewhere unrelated. Usually a leftover | No |
| **already inside the folder above** | Covered already. Cannot be ticked, because two entries covering the same files would fight over them | No — greyed out |

The **Track** button on the tile does whatever is ticked. If two folders are
one save, it says **Track all 2** and creates one game with both.

If OpenSave gets it wrong — you know the old-looking folder is the one you
play — just open the list and tick the one you want instead.

### Empty folders are hidden

Steam creates a save folder for every game you own whether or not saves go
there, so a real scan is full of folders with nothing in them. Those are
hidden. The toolbar says **Show 48 empty** if you want them — useful if you
want to track a game *before* playing it for the first time.

### Tracking several games at once

Click tiles to select them, then **Track selected**. If OpenSave listed two
folders separately that you know are one game, tick both and use **Track as one
game** instead.

## 5. Adding your second device

Install and open OpenSave on the other machine too, then pick whichever
situation matches.

### Both on the same network (home Wi-Fi, same router)

1. Open **Devices** on both.
2. They find each other automatically and appear in the list.
3. Click **Pair** on one. **Approve the request on the other.**

Pairing is mutual on purpose — nobody can connect to your machine without you
saying yes on that machine.

If they do not see each other, your network is probably set to *Public*, which
blocks local discovery. Set it to *Private* on Windows, allow OpenSave through
the firewall, or skip discovery entirely: under **Devices → Add by IP
address**, type the other machine's local address (something like
`192.168.1.42`) and port `8383`, then **Send pairing request**.

### Different networks (a friend's house, a laptop on mobile data)

1. Open **Internet Sync** on the first device and **generate a room code**.
2. Enter the same room code on the second device.
3. They find each other through a relay. No port forwarding, no router
   settings.

The relay only passes along encrypted data between your devices. It cannot read
your saves and does not store them.

Treat the room code like a password — anyone who has it can send your devices a
pairing request. They still cannot sync anything without you approving it on
the device itself.

Prefer to run your own relay instead of the hosted one? See
[docs/RELAY.md](docs/RELAY.md).

### Then

Once paired, tracked games sync automatically. You do not need to press
anything.

Both devices need to be **tracking the same game** for it to sync. If a game is
named differently on each machine, OpenSave can usually match them by Steam App
ID — or you can link them by hand under the game's settings.

## 6. What happens while you play

Nothing you have to think about. Concretely:

- The moment the game writes a save, OpenSave notices.
- It takes a **snapshot** — a copy of that moment you can return to.
- It sends the change to every paired device that is online and tracking that
  game.
- Only the parts of the file that changed are sent, not the whole save.

If the other device is off, the change waits and syncs when it comes back.

### Closing the window does not quit

Closing OpenSave hides it to the **system tray** (bottom-right on Windows) so
syncing keeps running. Right-click the tray icon to reopen it, sync everything,
or actually quit.

## 7. Getting an old save back

Open a game from the sidebar. The **Snapshots** tab lists every version, newest
first.

- **Restore** — put the whole save back to how it was at that point.
- **Browse files** — look inside a snapshot and restore one file out of it,
  leaving everything else alone.
- **Snapshot now** — take one deliberately, with a comment, before you try
  something risky.

**Restoring is safe.** OpenSave snapshots your current save *before* replacing
it, so if you restore the wrong thing you can restore your way back.

### How many are kept

Two separate allowances, so a game that autosaves constantly cannot push out a
snapshot you took on purpose:

- **Automatic snapshots** — the ones taken as the game saves. The newest 20 per
  branch are kept by default; older ones drop off.
- **Manual snapshots** — the ones you took yourself. **Kept forever by
  default.**

Both are per game, under its **Configuration** tab.

## 8. Branches: two playthroughs at once

A **branch** is a separate line of saves for the same game. Use one to start
New Game+ without losing your finished playthrough, or to try a decision and
back out.

When you create a branch you choose what it starts from:

- **Your current save** (the default) — switching to it changes nothing until
  you play. Safe.
- **Empty** — for a genuinely fresh run. Switching to it *clears the save
  folder*, and your old save is safe on the branch you left.

Switching branches always snapshots the current save onto the branch you are
leaving first, so a switch can be undone by switching back. If that snapshot
cannot be taken, nothing is changed at all.

## 9. When two devices disagree

If you play on your desktop and then on your laptop **before the first sync
finished**, both have changes the other does not. OpenSave notices and stops.

It does not use timestamps to decide — clocks disagree between machines, and
"newer" is not the same as "the one you want". It tracks what the two devices
last agreed on, and if both have moved since, that is a conflict.

You get a screen showing what differs and three choices:

- **Keep mine** — this device's version wins.
- **Keep theirs** — the other device's version wins.
- **Keep both** — the other version is put on a new branch so nothing is lost.
  Pick this if you are not sure.

Nothing is overwritten until you choose.

If a game has several save folders, they are judged separately: a disagreement
in your settings folder does not hold up your actual progress.

## 10. Games with awkward saves

Two situations that trip people up, and what to do about each.

### A save split across several folders

Some games keep progress in one place and settings or mods in another —
TrackMania keeps Scores, Tracks and Profiles as three sibling folders.

Auto-scan usually spots this and offers them together (see
[section 4](#4-reading-the-scan-results)). To do it by hand, open the game →
**Configuration → Save locations**, name the extra folder and pick it. All of
them then sync, snapshot and restore as one.

**Use the same name on your other devices.** The name is what the two machines
match on, because the folder itself lives somewhere different on each. A
location a device knows the name of but has no folder for is shown as needing
one, and is skipped until you choose it.

Removing a location never deletes anything — it only stops OpenSave covering
it.

### Files that shouldn't travel between machines

Some games store device-specific settings — resolution, controller bindings —
in the same folder as the save. Copying those to another machine can break the
game there, and you cannot just narrow the folder without losing the save.

Open the game → **Configuration → Files that shouldn't sync**. Either:

- **Click "Pick from your save folder"** and tick the file. This is the easy
  way: it lists what is actually there, marks each file **syncs** or **won't
  sync**, and writes the rule for you.
- **Or type patterns**, one per line, like a `.gitignore`:

```
Config.gs      a file of that name, anywhere in the folder
/Config.gs     only at the top level
*.log          anything ending .log
logs/          a whole folder
!keep.log      an exception to a line above
```

Upper and lower case do not matter. Set the same list on your other devices —
each machine applies its own.

**Excluding a file never stops it being backed up.** Snapshots still capture
it, and a restore brings it back. Excluding only stops it travelling between
machines.

One thing to know: a pattern applies to *every* folder of that game, so if the
same filename exists in two of its save locations it is excluded in both.

## 11. Steam Deck

OpenSave runs on the Deck like any other Linux app, but Game Mode has no
desktop, so there are two extra pieces.

**Sync in the background, always** — in Desktop Mode, open a terminal:

```bash
opensave service install
systemctl --user enable --now opensave-daemon
sudo loginctl enable-linger $USER
```

That last line is what keeps it running when you are not logged into the
desktop.

**Control it from Game Mode** — a [Decky Loader](https://decky.xyz) plugin
lives in `opensave-decky-plugin/`, giving you sync status and a sync button
without leaving Game Mode.

Your Deck pairs with your PC exactly like any other device. If they are on the
same Wi-Fi they will find each other; otherwise use a room code.

## 12. Cloud backup (optional)

Everything above works with no cloud at all. If you also want an off-site copy
— against a dead drive or a stolen laptop — open **Cloud Backup** and connect
Google Drive, Dropbox, OneDrive, WebDAV, a webhook, or just a folder on a NAS.

Every new snapshot then uploads in the background. **Browse cloud** lets you
explore and restore from there.

**Two provider quirks worth knowing before you pick one:**

- **Google Drive** works out of the box, but OpenSave's built-in credentials
  are a shared app still in testing, so Drive can ask you to sign in again
  about once a week. To stop that, open **Use your own OAuth app** under the
  provider and paste a Client ID you create in the
  [Google Cloud console](https://console.cloud.google.com/apis/credentials)
  (redirect URI `http://localhost/callback`).
- **OneDrive needs your own app registration** before it will work at all —
  Microsoft doesn't allow a shared one, so OpenSave can't ship credentials for
  it. Selecting OneDrive shows the fields and a link to the Azure portal.

**Dropbox, WebDAV, and a local or NAS folder have neither problem** and are the
easiest choice if you just want it set up once.

## 13. Moving to a new PC

Two ways, depending on whether the old machine still works.

**It still works.** Install OpenSave on the new one, pair the two
([section 5](#5-adding-your-second-device)), track the same games, and let them
sync. Nothing else to do.

**It does not, or you want one file to carry.** OpenSave can write your whole
library — games, settings, snapshot history — to a single portable archive:

- **Cloud Backup → Export** picks which games to include and writes an `.sscb`
  file. Put it on a USB stick or in cloud storage.
- On the new machine, **Cloud Backup → Import** reads it back.

On import you choose what happens to anything already there: **merge**, which
keeps both, or **overwrite**, which replaces. Overwrite is the destructive one
and is labelled as such.

The same thing from a terminal:

```bash
opensave backup export my-library.sscb
opensave backup import my-library.sscb
```

An `.sscb` is just a container — your saves are inside it as ordinary files.

## 14. Running your own relay

Only relevant for syncing across the internet, and only if you would rather not
use the free hosted relay. Two options, in increasing order of effort.

**Host it on a PC you already own.** Settings → **Internet relay** → tick
**Host a WAN relay server on this device**, and pick a port. The button below
shows the addresses to hand to whoever is connecting. You will need to forward
that port on your router, and this machine has to be switched on for the others
to find each other through it.

**Put it on a server.** A one-command installer handles the whole thing —
binary, service, firewall, and a certificate if you have a domain:

```bash
sudo bash install-relay.sh --domain relay.example.com
```

Then on each of your gaming devices, point at it and join a room. The full
walkthrough, including what to do when a reverse proxy is in the way, is in
[docs/RELAY.md](docs/RELAY.md).

One thing worth repeating, because it catches everyone: **the relay itself
never joins a room.** It has nothing to configure beyond a port. Rooms exist
because your devices ask for them.

## 15. Every setting, and what it does

Settings has five tabs. Most of it you will never need.

### General

| Setting | What it does |
| --- | --- |
| **Device name** | How this machine appears to your other devices |
| **Device type** | Desktop / laptop / Deck. Cosmetic — it picks the icon others see |
| **Device ID** | This machine's network identity. Read-only; other devices remember you by it |
| **Start OpenSave when the computer starts** | Launches minimised to the tray, so syncing runs without you opening anything |

### Sync

| Setting | What it does |
| --- | --- |
| **Internet bandwidth limit** | Caps relay transfers only. **LAN syncing is never throttled** |
| **WebSocket relay URL** | Which relay carries internet syncs. Only change it if you self-host |
| **Host a WAN relay server on this device** | Turns this machine into the relay — see [section 14](#14-running-your-own-relay) |
| **Google Drive folder ID** | Store snapshots in a specific existing Drive folder instead of the one OpenSave manages |

### Storage

| Setting | What it does |
| --- | --- |
| **Snapshots folder** | Where snapshot archives live. Move it to a bigger drive if history grows |
| **Pre-sync safety backups folder** | A copy of your save taken **before every incoming sync**, so a bad sync is always reversible. Separate from snapshots, and the reason an unwanted sync is recoverable |
| **Retention period** | How long those safety copies are kept (7–90 days) |
| **Automatic snapshots to keep per game** | Default cap for newly tracked games, per branch. 0 keeps everything. Per-game override in that game's Configuration tab |
| **Manual snapshots to keep per game** | The ones you took yourself. **0 = forever, and that is the default** |
| **Extra folders to auto-scan** | Additional places to look. Point it at a second games library |
| **Folders to exclude** | Places auto-scan should skip for good — a stale save directory you are tired of being offered |

### Advanced

| Setting | What it does |
| --- | --- |
| **Daemon port** | The local API and LAN peer port, 8383 by default. Change it if something else has that port. **Needs a restart**, and it is also the port other devices use to reach you |
| **Cross-platform path translation** | Rewrite rules for paths that differ between machines, so a Windows save folder can map to its Steam Deck equivalent. Only needed when the same game is tracked on both |

### Updates

**Offer me beta versions** — get pre-releases as well as stable ones. If you are
*already* running a beta you will be offered newer betas whether or not this is
ticked; otherwise there would be no way forward from a beta. You are still
offered the stable release the moment it is newer, so it is not a one-way door.

## 16. Odds and ends

Small things that are easy to miss.

**The same game named differently on two machines.** If a title is tracked as
"Elden Ring" here and "ELDEN RING" there, they will not sync — they are two
games as far as OpenSave is concerned. Either rename one, or link them with
`opensave link <gameId> <otherGameId>`. Games that share a Steam App ID can be
matched automatically with **Settings → Sync → match by App ID**, which is off
by default so two genuinely separate copies are never merged behind your back.

**Launching games.** Set a game's executable in its Configuration tab and
OpenSave gets a **Play** button, so it can be the thing you open first. Purely
optional.

**Activity.** The **Activity** tab is the log of everything OpenSave has done —
syncs, snapshots, errors. It is the first place to look when something did not
happen and you want to know why.

**Multi-select in your library.** The **☑ Select** button on Home lets you act
on several games at once, such as untracking a batch you added by mistake.

**Untracking is safe.** Removing a game from your library never deletes a save
file or a snapshot. Nothing in OpenSave deletes a save except restoring over
one, which snapshots first anyway.

**Where your data lives.** Everything is under a `.opensave` folder in your home
directory: `opensave.db` holds your library and settings, `backups/` holds the
snapshot archives, and `daemon.addr` records where the background service is
listening. Deleting that folder resets OpenSave completely and touches none of
your actual game saves.

## 17. Doing all this from a terminal

Everything the window does, the `opensave` command does too — which is what you
want on a headless server, or a Steam Deck that lives in Game Mode.

```bash
opensave scan                     # find saves
opensave add 3                    # track the third result
opensave status                   # what is tracked, and who is paired
opensave sync --all               # sync now
opensave snapshots elden-ring     # history for one game
```

Add `--json` to any command to script against it. The full guide, including
which commands need the background service running and worked sequences for
pairing and internet sync, is [docs/CLI.md](docs/CLI.md).

## 18. When something looks wrong

**"404 Not Found" when the app opens.** Something else is on port `8383` —
often a second copy of OpenSave still running. Check the system tray and Task
Manager, or change the port under **Settings → Advanced**.

**My devices cannot see each other.** Set the network profile to *Private*,
allow OpenSave through the firewall, and make sure both are on the same
network. Failing that, use **Connect via IP**, or a room code under **Internet
Sync**, which works even on the same network.

**A game is not detected.** Use **+ Track folder** and point at it. Auto-scan
covers a lot but cannot cover everything.

**A game is detected in the wrong place.** Open **found in N folders** on its
tile and pick the folder with a recent date. If you already tracked the wrong
one, untrack it and track the right one — untracking never deletes saves.

**My save did not sync.** Check both devices are tracking that game, that they
show as paired under **Devices**, and that the game is not waiting on a
conflict. **Activity** shows what OpenSave has been doing and usually says why.

**I restored the wrong snapshot.** Restore again — your pre-restore state was
snapshotted automatically, so it is in the list.

**A folder shows as empty but I know it has saves.** The scan hides folders
with no files. Tick **Show N empty** to see them. If a folder really does have
files and still shows as empty, OpenSave could not read it — check permissions.

**I want to start over.** Untracking a game removes it from your library and
leaves every save file and snapshot on disk. Nothing you do in OpenSave deletes
a save file except explicitly restoring over one.

## 19. Glossary

| Term | What it means |
| --- | --- |
| **Track** | Tell OpenSave to watch a folder. It does not move or change anything |
| **Snapshot** | A saved copy of how a save folder looked at one moment |
| **Restore / rollback** | Put a save folder back to how a snapshot has it |
| **Branch** | A separate line of saves for the same game — a second playthrough |
| **Peer / device** | Another machine running OpenSave that you have paired with |
| **Pairing** | Mutually approving two devices so they may sync. Both sides must agree |
| **Relay** | A server that passes encrypted data between devices on different networks. It cannot read your saves |
| **Room code** | The shared word that lets two devices find each other through a relay |
| **Conflict** | Both devices changed a save independently. OpenSave asks rather than guessing |
| **Save location** | An extra folder belonging to the same game, for saves split across places |
| **Exclusion** | A file that should stay on its own machine. Still backed up, just never sent |

---

## Still stuck?

**[Join the OpenSave Discord →](https://discord.gg/hvBv92DZvn)** — the quickest
way to get an answer, and where new builds get discussed before they ship.

Bugs and feature requests are welcome on
[GitHub issues](https://github.com/Liquid-co/OpenSave/issues).

Next: the [User Guide](USER_GUIDE.md) is the full reference — every setting and
what it changes. If you would rather drive OpenSave from a terminal, or you are
setting up a headless machine, the [CLI guide](docs/CLI.md) covers that end to
end.
