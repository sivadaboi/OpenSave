# 🎮 OpenSave — Complete User Guide & Getting Started

Welcome to **OpenSave**! This guide will walk you through setting up and using the app step-by-step. By the end of this guide, you will be syncing save files between your PC, Steam Deck, or emulator configurations like a pro.

> [!WARNING]
> **Important Note:** If you are experiencing any issues with syncing or cloud backups, please download and use **pre-release version 1.1.4** from the [GitHub Releases](https://github.com/sivadaboi/OpenSave/releases/tag/v1.1.4) page to resolve these issues.

---

## 🧭 Core Concepts: How OpenSave Works
OpenSave runs in the background as a lightweight system tray service (the "Daemon") and opens a dashboard in your browser. 
* **LAN Sync (Local)**: Automatically detects other devices running OpenSave on your local Wi-Fi/Ethernet network and syncs saves directly, peer-to-peer.
* **Internet Sync (WAN)**: Connects devices in different locations using a secure, temporary Room Code. Traffic is routed via a cloud relay (no port forwarding required!).
* **Snapshots**: Every time a change is made, OpenSave takes a tiny `.zip` backup of your save folder. If a save gets corrupted or you want to rollback, you can restore any snapshot with one click.

---

## 🚀 Step 1: Launching OpenSave
1. Download and run the **OpenSave Setup** installer on your devices.
2. Once installed, launch **OpenSave**. You will see the OpenSave icon in your system tray (bottom-right on Windows, top-bar on Linux/Steam Deck).
3. Right-click the tray icon and select **Open Dashboard** (or open your web browser and go to `http://localhost:8383`).

---

## 📂 Step 2: Tracking a Game Save Folder
To sync a game, OpenSave needs to know where its saves are located:
1. On the **Games** tab of the dashboard, click **Track New Folder**.
2. **Game Title**: Enter the name of the game (e.g. `Elden Ring`).
3. **Save Directory**: Click **Browse** and select the folder where the game stores its saves.
   * *Tip for Steam games*: You can find standard Steam save paths under `C:\Users\<YourUsername>\AppData\Local` or `C:\Users\<YourUsername>\Saved Games`.
4. Click **Track Game**.
5. OpenSave will scan the folder, index your files, and create your initial snapshot backup.

---

## 🔗 Step 3: Pairing Your Devices
To synchronize saves, you must link your devices together.

### Option A: Local Network Sync (LAN) — Recommended
If both devices are on the same Wi-Fi or Ethernet network:
1. Go to the **Devices** tab on both machines.
2. Look at the **Discovered on LAN** section. If automatic discovery is working, you will see the other device listed. Click **Pair**.
3. A popup will appear on the other device asking to approve the pairing. Click **Approve**.
4. **Done!** Your devices are now paired. Whenever you close a game, OpenSave will automatically sync it to the other machine.

> [!NOTE]
> If your router or Windows Firewall blocks automatic discovery, you can pair manually:
> * On Device A, look at the **Connect via IP or PIN Code** card to find your **Local Sync PIN** (e.g. `SS-LAN-03CBCC`).
> * On Device B, enter Device A's PIN into the input box and click **Pair**.

---

### Option B: Internet Sync (WAN)
If your devices are in different locations (e.g. one at home, one at a friend's house):
1. Go to the **Internet Sync** tab on both devices.
2. On **Device A**, click **🎲 Generate Random Code** (or type a custom code, e.g., `ELDEN-COOP-SYNC`) and click **Join Room**.
3. Share this code with your friend or type the exact same code on your second machine (**Device B**), then click **Join Room**.
4. Both devices will join the secure relay room. They are now linked over the internet!

---

## ⚡ Step 4: Synchronizing and Resolving Conflicts

### Automatic Syncing
Once paired, OpenSave handles the rest. When you edit or play a tracked game:
1. OpenSave detects that the save files were updated.
2. It waits for writing to stop (2-second debounce timer) to prevent copying incomplete saves.
3. It takes a backup snapshot.
4. It checks the paired peer. If they are online, it syncs only the modified 64KB blocks of the save files.

---

### ⚠️ Resolving Save Conflicts
If you played a game on your PC offline, and also played it on your Steam Deck offline, both saves have modified files that are newer than the last sync. This creates a **Conflict**.

OpenSave will open a **Version Conflict Modal** showing:
* Local Version details (Comment, Timestamp, Snapshot ID).
* Remote Version details.
* **Altered Files list**: showing precisely which files were added, deleted, or modified.

**Your Options:**
1. **Keep Local**: Overwrites the peer's saves with your current device's saves.
2. **Keep Remote**: Overwrites your active saves with the peer's saves.
3. **Keep Both (Branches)**: Keeps your local saves active, but pulls the peer's saves into a separate branch (e.g., `conflict-steamdeck-1234`). You can swap between these branches at any time in the History tab!

---

## 🕒 Step 5: Version History & Granular Rollbacks
If a save file gets corrupted or you want to undo a gaming session:
1. Go to the **Games** tab and click on the game.
2. Scroll down to the **Backup History** timeline.
3. You will see a chronological list of backups.
4. **Full Rollback**: Click **Rollback** on any snapshot to reset the entire save folder to that exact moment. OpenSave automatically takes a safety backup of your current saves before doing this so you can't lose anything.
5. **Granular Restore**: Click **Browse Files** on a snapshot to view all files contained inside that backup ZIP. Click **Restore File** next to a specific file to restore *only* that file, leaving the rest of your save folder untouched.

---

## 🛠️ Advanced: Cross-Platform Paths & Custom Mappings
If you sync between a Windows PC and a Linux Steam Deck, OpenSave automatically translates standard user folder prefixes (e.g. mapping `C:\Users\John\Documents` to `/home/deck/Documents`).

For custom game folders or emulators:
1. Go to **Settings** > **Custom Path Translations**.
2. Click **Add Rule**.
3. **Pattern A**: Enter your Windows folder path (e.g. `D:\Emulators\Saves`).
4. **Pattern B**: Enter the corresponding path on your other device (e.g. `/home/deck/Emulation/saves`).
5. Click **Add Rule**.
6. Now, any saves synced within these folders will translate correctly across both systems!
