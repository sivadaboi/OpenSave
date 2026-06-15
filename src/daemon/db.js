import fs from 'fs';
import path from 'path';
import os from 'os';
import crypto from 'crypto';

const DEFAULT_PORT = 8383;
const OLD_HOME_DIR = path.join(os.homedir(), '.savesync');
const HOME_DIR = path.join(os.homedir(), '.opensave');
const DB_FILE = path.join(HOME_DIR, 'opensave-db.json');

// Migrate old folder if it exists
if (fs.existsSync(OLD_HOME_DIR) && !fs.existsSync(HOME_DIR)) {
  try {
    fs.renameSync(OLD_HOME_DIR, HOME_DIR);
    console.log(`[Migration] Automatically migrated user database folder from ${OLD_HOME_DIR} to ${HOME_DIR}`);
    
    // Also rename savesync-db.json to opensave-db.json inside it if present
    const oldDbFile = path.join(HOME_DIR, 'savesync-db.json');
    const newDbFile = path.join(HOME_DIR, 'opensave-db.json');
    if (fs.existsSync(oldDbFile)) {
      fs.renameSync(oldDbFile, newDbFile);
      console.log(`[Migration] Automatically renamed database file to ${newDbFile}`);
    }
  } catch (err) {
    console.error(`[Migration] Failed to migrate database folder:`, err.message);
  }
}

// Ensure home directories exist
if (!fs.existsSync(HOME_DIR)) {
  fs.mkdirSync(HOME_DIR, { recursive: true });
}
const BACKUPS_DIR = path.join(HOME_DIR, 'backups');
if (!fs.existsSync(BACKUPS_DIR)) {
  fs.mkdirSync(BACKUPS_DIR, { recursive: true });
}

// Public OpenSave cloud relay — deployed on Render.com free tier.
// Users who self-host can override this in Settings > Internet Sync.
const CLOUD_RELAY_URL = 'wss://opensave-relay.onrender.com';

const defaultState = {
  settings: {
    deviceName: os.hostname() || 'Unknown Device',
    nodeId: `node_${crypto.randomUUID().replace(/-/g, '')}`,
    deviceType: 'desktop',
    port: DEFAULT_PORT,
    syncInterval: 5000,
    syncOnWatch: true,
    dataDir: HOME_DIR,
    backupsDir: BACKUPS_DIR,
    syncBackupsDir: BACKUPS_DIR,
    autoDeleteBackups: false,
    autoDeleteDays: 30,
    autoSyncOnTrack: true,
    customScanPaths: [],
    pathTranslations: [],
    relayUrl: CLOUD_RELAY_URL,
    syncCode: '',
    hostRelay: false,
    relayPort: 8386,
    startOnBoot: false,
    speedLimit: 0,
    uiMode: 'modern',
    cloudSync: {
      enabled: false,
      provider: 'local', // 'local' | 'webdav' | 'webhook' | 'google_drive' | 'onedrive' | 'dropbox'
      url: '',
      username: '',
      password: '',
      headers: '{}',
      folderId: '',
      // Per-provider custom OAuth Client IDs supplied by the user.
      // Leave empty to use OpenSave's built-in registered app credentials.
      customClientIds: {
        google_drive: '',
        onedrive: '',
        dropbox: ''
      },
      tokens: {
        accessToken: '',
        refreshToken: '',
        expiryTime: 0,
        userEmail: ''
      }
    }
  },
  games: {},
  peers: {}
};

class Database {
  constructor() {
    this.data = { ...defaultState };
    this.load();
  }

  load() {
    try {
      const dbFile = this.getDbFilePath();
      if (fs.existsSync(dbFile)) {
        const fileContent = fs.readFileSync(dbFile, 'utf8');
        this.data = JSON.parse(fileContent);

        // Auto-migrate old paths in settings
        const settings = this.data.settings || {};
        if (settings.dataDir && settings.dataDir.includes('.savesync')) {
          settings.dataDir = settings.dataDir.replace('.savesync', '.opensave');
        }
        if (settings.backupsDir && settings.backupsDir.includes('.savesync')) {
          settings.backupsDir = settings.backupsDir.replace('.savesync', '.opensave');
        }
        if (settings.syncBackupsDir && settings.syncBackupsDir.includes('.savesync')) {
          settings.syncBackupsDir = settings.syncBackupsDir.replace('.savesync', '.opensave');
        }

        // Auto-migrate zipPaths for all snapshots of all games and branches
        const games = this.data.games || {};
        for (const gameId in games) {
          const game = games[gameId];
          for (const branchName in game.branches) {
            const branch = game.branches[branchName];
            if (branch.snapshots) {
              branch.snapshots.forEach(snap => {
                if (snap.zipPath && snap.zipPath.includes('.savesync')) {
                  snap.zipPath = snap.zipPath.replace('.savesync', '.opensave');
                }
              });
            }
          }
        }

        // Fill in missing default structures if database is older
        this.data.settings = { ...defaultState.settings, ...this.data.settings };
        if (!this.data.settings.nodeId) {
          this.data.settings.nodeId = `node_${crypto.randomUUID().replace(/-/g, '')}`;
        }
        this.data.games = games;
        this.data.peers = this.data.peers || {};
        this.save();
      } else {
        this.data = JSON.parse(JSON.stringify(defaultState));
        this.save();
      }
    } catch (error) {
      console.error('Error loading database, resetting to default:', error);
      this.data = JSON.parse(JSON.stringify(defaultState));
      this.save();
    }
  }

  save() {
    try {
      const dbFile = this.getDbFilePath();
      fs.writeFileSync(dbFile, JSON.stringify(this.data, null, 2), 'utf8');
    } catch (error) {
      console.error('Error saving database:', error);
    }
  }

  getSettings() {
    return this.data.settings;
  }

  updateSettings(newSettings) {
    this.data.settings = { ...this.data.settings, ...newSettings };
    this.save();
    return this.data.settings;
  }

  getGames() {
    return this.data.games;
  }

  getGame(id) {
    return this.data.games[id];
  }

  addGame(name, savePath) {
    // Generate clean ID from name
    const id = name.toLowerCase().replace(/[^a-z0-9]/g, '-').replace(/-+/g, '-').replace(/^-|-$/g, '');
    
    if (this.data.games[id]) {
      throw new Error(`Game with name/id "${name}" already exists.`);
    }

    this.data.games[id] = {
      id,
      name,
      savePath: path.resolve(savePath),
      activeBranch: 'main',
      autoSync: true,
      maxSnapshots: 5,
      branches: {
        main: {
          name: 'main',
          snapshots: [] // list of snapshot metadata
        }
      },
      createdAt: new Date().toISOString()
    };
    
    this.save();
    return this.data.games[id];
  }

  removeGame(id) {
    if (!this.data.games[id]) {
      throw new Error(`Game ID "${id}" not found.`);
    }
    delete this.data.games[id];
    this.save();
  }

  updateGame(id, fields) {
    if (!this.data.games[id]) {
      throw new Error(`Game ID "${id}" not found.`);
    }
    this.data.games[id] = { ...this.data.games[id], ...fields };
    this.save();
    return this.data.games[id];
  }

  getPeers() {
    return this.data.peers;
  }

  addPeer(peerId, name, address, port, deviceType = 'desktop') {
    this.data.peers[peerId] = {
      id: peerId,
      name,
      deviceType,
      address,
      port: parseInt(port, 10),
      pairedAt: new Date().toISOString(),
      lastSynced: null,
      status: 'offline'
    };
    this.save();
    return this.data.peers[peerId];
  }

  removePeer(peerId) {
    if (this.data.peers[peerId]) {
      delete this.data.peers[peerId];
      this.save();
    }
  }

  updatePeer(peerId, fields) {
    if (this.data.peers[peerId]) {
      this.data.peers[peerId] = { ...this.data.peers[peerId], ...fields };
      this.save();
      return this.data.peers[peerId];
    }
    return null;
  }

  // Set custom database file (useful for running tests with temporary DB paths)
  setDbFileForTesting(testDbPath, testHomeDir) {
    // Make sure test directories exist
    if (!fs.existsSync(testHomeDir)) {
      fs.mkdirSync(testHomeDir, { recursive: true });
    }
    const testBackupsDir = path.join(testHomeDir, 'backups');
    if (!fs.existsSync(testBackupsDir)) {
      fs.mkdirSync(testBackupsDir, { recursive: true });
    }

    this.data.settings.dataDir = testHomeDir;
    this.data.settings.backupsDir = testBackupsDir;

    // Point DB file to test location
    // We override DB_FILE by rewriting the load/save target
    this._customDbFile = testDbPath;
    this.load();
  }

  // Helper to resolve actual DB path in use
  getDbFilePath() {
    return this._customDbFile || DB_FILE;
  }
}

// Singleton database instance
const db = new Database();
export default db;
