import chokidar from 'chokidar';
import db from './db.js';
import { createSnapshot } from './snapshot.js';
import { log } from './logger.js';
import { getAllFiles } from './delta.js';
import path from 'path';
import fs from 'fs';

function isFileLocked(filePath) {
  try {
    const fd = fs.openSync(filePath, 'r+');
    fs.closeSync(fd);
    return false;
  } catch (err) {
    if (err.code === 'EBUSY' || err.code === 'EPERM' || err.code === 'EACCES') {
      return true;
    }
    return false;
  }
}

function isPathLocked(savePath) {
  if (!fs.existsSync(savePath)) return false;
  const stat = fs.statSync(savePath);
  if (stat.isFile()) {
    return isFileLocked(savePath);
  }
  
  try {
    const files = getAllFiles(savePath);
    for (const file of files) {
      const fullPath = path.join(savePath, file);
      if (isFileLocked(fullPath)) {
        return true;
      }
    }
  } catch (e) {}
  
  return false;
}

class WatcherEngine {
  constructor() {
    this.watchers = {}; // gameId -> chokidar Watcher instance
    this.debounceTimers = {}; // gameId -> Timeout ID
    this.gameplayGuardTimers = {}; // gameId -> Timeout ID
    this.onChangeCallback = null; // callback to trigger peer sync
  }

  setSyncCallback(callback) {
    this.onChangeCallback = callback;
  }

  start() {
    log('info', 'Starting save file watcher engine...');
    const games = db.getGames();
    for (const gameId in games) {
      try {
        this.watchGame(games[gameId]);
      } catch (err) {
        log('error', `Failed to watch game ${gameId}`, err.message);
      }
    }
  }

  watchGame(game) {
    if (this.watchers[game.id]) {
      this.unwatchGame(game.id);
    }

    const savePath = game.savePath;
    log('info', `Registering watcher for "${game.name}"`, savePath);

    if (!chokidar) {
      log('error', 'Chokidar is not loaded');
      return;
    }

    const watcher = chokidar.watch(savePath, {
      ignored: /(^|[\/\\])\../, // ignore dotfiles
      persistent: true,
      ignoreInitial: true
    });

    watcher.on('all', (event, filePath) => {
      log('info', `File ${event} in "${game.name}"`, path.basename(filePath));
      this.handleChange(game.id);
    });

    watcher.on('error', (err) => {
      log('error', `Watcher error for "${game.name}"`, err.message);
    });

    this.watchers[game.id] = watcher;
  }

  unwatchGame(gameId) {
    if (this.watchers[gameId]) {
      this.watchers[gameId].close();
      delete this.watchers[gameId];
      if (this.debounceTimers[gameId]) {
        clearTimeout(this.debounceTimers[gameId]);
        delete this.debounceTimers[gameId];
      }
      if (this.gameplayGuardTimers[gameId]) {
        clearTimeout(this.gameplayGuardTimers[gameId]);
        delete this.gameplayGuardTimers[gameId];
      }
      log('info', `Stopped watching game ID: ${gameId}`);
    }
  }

  handleChange(gameId) {
    const game = db.getGame(gameId);
    if (!game) return;

    // Debounce the change event so we only snapshot after writing stops
    if (this.debounceTimers[gameId]) {
      clearTimeout(this.debounceTimers[gameId]);
    }

    this.debounceTimers[gameId] = setTimeout(async () => {
      delete this.debounceTimers[gameId];
      
      const savePath = game.savePath;
      
      const checkAndRun = async () => {
        if (isPathLocked(savePath)) {
          log('info', `Gameplay Guard Active`, `Save file write-locks are active for "${game.name}". Pausing auto-sync until game is idle/closed...`);
          
          if (this.gameplayGuardTimers[gameId]) {
            clearTimeout(this.gameplayGuardTimers[gameId]);
          }
          this.gameplayGuardTimers[gameId] = setTimeout(checkAndRun, 5000);
          return;
        }

        if (this.gameplayGuardTimers[gameId]) {
          clearTimeout(this.gameplayGuardTimers[gameId]);
          delete this.gameplayGuardTimers[gameId];
        }

        log('success', `Gameplay Guard Cleared`, `Save files unlocked for "${game.name}". Resuming auto-sync.`);
        log('event', 'Detected changes', `Settle timer expired for "${game.name}". Triggering auto-snapshot.`);
        
        let attempts = 0;
        const maxAttempts = 5;
        const delayMs = 1500;

        const performSnapshot = async () => {
          try {
            // Create new auto-snapshot
            const snap = createSnapshot(gameId, 'Auto-backup (save file changed)', true);
            
            // Notify the P2P engine to synchronize changes
            if (this.onChangeCallback) {
              this.onChangeCallback(gameId, snap);
            }
            log('success', `Auto-snapshot and sync triggered for "${game.name}"`);
          } catch (err) {
            attempts++;
            const isLockError = err.code === 'EBUSY' || err.code === 'EPERM' || err.code === 'EACCES' || err.message.includes('busy') || err.message.includes('locked');
            
            if (isLockError && attempts < maxAttempts) {
              log('warn', `Auto-snapshot failed due to locked/busy files (attempt ${attempts}/${maxAttempts}) for "${game.name}". Retrying in ${delayMs}ms...`);
              setTimeout(performSnapshot, delayMs);
            } else {
              log('error', `Auto-snapshot failed for "${game.name}" after ${attempts} attempt(s)`, err.message);
            }
          }
        };

        performSnapshot();
      };

      checkAndRun();
    }, 2000); // 2 seconds debounce
  }

  stop() {
    log('info', 'Stopping watcher engine...');
    for (const gameId in this.watchers) {
      this.unwatchGame(gameId);
    }
  }
}

const watcherEngine = new WatcherEngine();
export default watcherEngine;
