# SyncSave 🎮🔄

**SyncSave** is a lightweight, decentralized, peer-to-peer (P2P) game save synchronization engine. It automatically tracks save games across multiple devices (PCs, laptops, handhelds) and replicates them in real-time without relying on external cloud storage.

---

## Key Features

* **⚡ Peer-to-Peer (P2P) Sync**: Connects devices directly over local Wi-Fi/LAN (via UDP multicast auto-discovery) or across the internet (using a lightweight self-hosted WAN WebSocket relay).
* **🔄 Live Sync Progress**: Displays real-time transfer speeds (MB/s), percentages, and remaining block sizes during replication.
* **📂 Automated Game Detection**: Features scanner presets for Epic Games, GOG, Steam, Unreal Engine save folders (`%LOCALAPPDATA%/<GameName>/Saved/SaveGames`), and custom save folder mappings.
* **🛡️ Version Control & Retention**: Implements a simple branching model (like Git) for save files. Set custom snapshot retention limits per-game (e.g., keep the last 5 saves) to prevent disk bloat.
* **⚡ Selective Syncing Toggle**: Toggle auto-sync on/off on a per-game basis directly from the UI.
* **📦 Native App Packaging**: Integrates with Electron to run as a borderless desktop dashboard and packages into a standard Windows installer setup wizard.

---

## Getting Started

### Prerequisites

* [Node.js](https://nodejs.org/) (v20 or higher)

### Run in Development

1. **Install dependencies**:
   ```bash
   npm install
   ```
2. **Run as a Native Desktop App (Electron)**:
   ```bash
   npm run start:app
   ```
3. **Run the Daemon only (Headless)**:
   ```bash
   npm run start
   ```

---

## Project Documentation

* **[Installation & Packaging Guide](INSTALL.md)**: Detailed instructions on compiling standard Windows setup wizards (`.exe`), single portable executables (`pkg`), and setting up UPnP port forwarding for WAN relays.
* **[Privacy & License Agreement](PRIVACY.md)**: Standard software usage policy.
* **[License (MIT)](LICENSE)**: Open-source legal permissions.

---

## How it Works Under the Hood

```
+--------------------+                     +--------------------+
|   SyncSave (PC A)  |                     |   SyncSave (PC B)  |
|  [File Watcher]    |                     |  [File Watcher]    |
|  [P2P Sync Engine] | <=================> |  [P2P Sync Engine] |
+---------+----------+      Local LAN      +---------+----------+
          |             (or WAN via Relay)           |
          v                                          v
    ~/.syncsave/                               ~/.syncsave/
    (Database & Snapshots)                     (Database & Snapshots)
```

1. **Watcher**: The daemon monitors selected save folders for changes using `chokidar`.
2. **Snapshot**: On change, it creates a zipped snapshot under `~/.syncsave/backups/`.
3. **Delta Check**: It scans local vs. peer block listings to calculate differences.
4. **Replication**: It syncs only the changed blocks over WebSockets directly to the target device.
