# OpenSave — Decky plugin

Game Mode panel for OpenSave: sync saves, resolve conflicts, and start the
sync service without dropping to Desktop Mode.

> **Untested on hardware.** This has been built and typechecked, but has never
> been loaded on a real Steam Deck. Expect to iterate. See
> [IMPROVEMENTS.md](IMPROVEMENTS.md) for what's implemented.

## What it does

- Live sync status, per-game **Sync now**, and **Sync all**
- **Resolves conflicts** — keep both / keep this device's / keep the peer's
- **Auto-syncs around gameplay** — when a game starts and when it exits
- **Snapshot** a save before a risky run
- **Starts the sync service** if it isn't running (Game Mode never launches the
  desktop app, so this is what makes the plugin usable at all)

## Requirements

1. **Decky Loader** installed on the Deck — https://decky.xyz
2. **OpenSave installed**, so there's a daemon to talk to. The Flatpak is the
   supported route on a stock Deck; install it in Desktop Mode first, and track
   at least one game there.

## Build

Run on your dev machine (not the Deck):

```bash
cd opensave-decky-plugin
npm install
npm run build      # produces dist/index.js
```

## Install on the Deck

A Decky plugin is just a folder under `~/homebrew/plugins/`. Only four things
need to ship — `node_modules/`, `src/` and the source map are not needed:

```
OpenSave/
├── plugin.json
├── package.json
├── main.py
└── dist/index.js
```

### Over the network (easiest, repeatable)

Enable SSH on the Deck once (Desktop Mode: `sudo systemctl enable --now sshd`,
and set a password with `passwd` if you never have). Then from your dev
machine, in `opensave-decky-plugin/`:

```bash
ssh deck@<deck-ip> "mkdir -p ~/homebrew/plugins/OpenSave/dist"
scp plugin.json package.json main.py deck@<deck-ip>:~/homebrew/plugins/OpenSave/
scp dist/index.js deck@<deck-ip>:~/homebrew/plugins/OpenSave/dist/
```

### By USB stick

Copy those same four files (keeping `dist/index.js` inside a `dist` folder)
to `/home/deck/homebrew/plugins/OpenSave/` in Desktop Mode.

### Then reload Decky

```bash
sudo systemctl restart plugin_loader
```

or just reboot. Back in Game Mode: **QAM (the ⋯ button) → the plug icon →
OpenSave**.

## If it doesn't appear

- **Check ownership.** Files copied as root won't load:
  `sudo chown -R deck:deck ~/homebrew/plugins/OpenSave`
- **Read the loader log** — this is where a Python import error or a bad
  `plugin.json` will show up:
  `journalctl -u plugin_loader -n 100 --no-pager`
- **Plugin loads but shows nothing useful?** The panel's own logging goes to
  Decky's plugin log directory; `decky.logger` output appears in the same
  `plugin_loader` journal.

## If the panel says the sync service isn't running

That's expected on a fresh boot — press **Start sync service**. To have it
running always, install the systemd unit at
[`packaging/systemd/opensave-daemon.service`](../packaging/systemd/opensave-daemon.service);
it carries its own instructions, including the `loginctl enable-linger` step
that keeps it alive across Game Mode / Desktop Mode switches.
