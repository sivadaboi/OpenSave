<div align="center">

# OpenSave

### Steam Cloud for every game you own.

**OpenSave** syncs your game saves between devices peer-to-peer — no Steam required, no accounts, no subscriptions. Point it at a folder, pair your devices, and your saves follow you everywhere.

*Complete Go rewrite of the original Node.js/Electron app: one small native binary, no runtime, wire-compatible with existing peers.*

</div>

---

## Features

- **Auto-detection** — scans for saves from Steam, emulators (RetroArch, Dolphin, Ryujinx, Yuzu, Citra, PCSX2, RPCS3, PPSSPP, Cemu, Xenia), Steam-emulator repacks (Goldberg, CODEX, RUNE, Tenoke, …), Epic, GOG, and Unreal Engine conventions
- **Track anything** — any folder or single save file, watched live with block-level change detection (SHA-256, 64KB–2MB adaptive blocks); only changed blocks ever transfer
- **P2P sync** — automatic over LAN (zero config discovery) or across the internet through a relay room code (no port forwarding); paired-device model with explicit approval
- **Snapshot history** — every change creates a versioned snapshot; roll back whole saves or single files; branches keep parallel playthroughs (and conflict resolutions) safe
- **Conflict handling** — diverged saves are detected via sync lineage (not wall clocks); keep yours, theirs, or both on a new branch
- **Cloud backup** — optional mirroring to Google Drive, Dropbox, OneDrive, WebDAV, a webhook, or a local/NAS folder
- **Privacy-first** — no accounts, no telemetry; the relay only routes encrypted WebSocket frames and never stores saves

## Install

**Windows** — download `OpenSave.Setup.exe` (installer) or the portable `OpenSave.exe` from Releases.

**Linux / Steam Deck** — download and extract `opensave-linux-amd64.tar.gz`, run `./opensave`. A Decky Loader plugin for Game Mode lives in `opensave-decky-plugin/`.

**Upgrading from the original (JS) OpenSave?** Your data migrates automatically on first launch — tracked games, snapshots, pairings, and cloud settings are imported from `~/.opensave/opensave-db.json` (kept as a backup, never deleted). Go and JS devices can pair and sync with each other during the transition.

## Build from source

```bash
# Desktop app (needs Go 1.24+, Node 18+, and the Wails CLI)
go install github.com/wailsapp/wails/v2/cmd/wails@latest
cd cmd/opensave-app && wails build

# Headless daemon + CLI
go build ./cmd/opensave-cli

# Relay server (self-host)
go build ./cmd/opensave-relay
```

## Self-hosting the relay

```bash
./opensave-relay                          # listens on :8386
PORT=10000 ./opensave-relay               # custom port
docker build -f relay/Dockerfile .       # or as a container
```

Point **Settings → Internet Sync → Relay server** at your instance. `opensave-cli upnp 8386` forwards the port on UPnP-capable routers.

## Architecture

```
cmd/opensave-app     Wails desktop app (daemon embedded + Svelte UI)
cmd/opensave-cli     Headless daemon & CLI
cmd/opensave-relay   Stateless WAN relay (room broker + OAuth proxy)
internal/
  store              SQLite persistence + legacy JSON import
  delta              Block hashing, manifest diff, patching
  snapshot           ZIP snapshots, branches, retention
  watcher            Save-change detection (safe-write aware, lock guard)
  p2p                Discovery, pairing, sync engine, LAN/WAN transports
  cloud              Six backup providers + PKCE OAuth
  api                Local REST + WebSocket dashboard API
opensave-decky-plugin  Steam Deck Game Mode plugin (Decky Loader)
```

The daemon exposes the same REST/WebSocket API and P2P wire protocol as the original JS app, so old and new versions interoperate.

## Documentation

- [User Guide](USER_GUIDE.md) — first run, syncing, snapshots, cloud backup, troubleshooting
- [Privacy](PRIVACY.md) — what OpenSave does and doesn't do with your data
- [Changelog](CHANGELOG.md) — release notes

## License

[MIT](LICENSE) — retains the original author's copyright and credits the Go rewrite.
