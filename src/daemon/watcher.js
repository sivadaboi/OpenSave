import chokidar from 'chokidar';
import db from './db.js';
import { createSnapshot } from './snapshot.js';
import { log } from './logger.js';
import path from 'path';

class WatcherEngine {
  constructor() {
    this.watchers = {}; // gameId -> chokidar Watcher instance
    this.debounceTimers = {}; // gameId -> Timeout ID
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
