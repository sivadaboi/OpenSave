import fs from 'fs';
import path from 'path';
import AdmZip from 'adm-zip';
import db from './db.js';

/**
 * Ensures a directory exists.
 */
function ensureDir(dirPath) {
  if (!fs.existsSync(dirPath)) {
    fs.mkdirSync(dirPath, { recursive: true });
  }
}

/**
 * Recursively deletes all files and subfolders in a folder, but keeps the folder itself.
 */
function clearFolder(folderPath) {
  if (!fs.existsSync(folderPath)) return;
  const files = fs.readdirSync(folderPath);
  for (const file of files) {
    const curPath = path.join(folderPath, file);
    if (fs.lstatSync(curPath).isDirectory()) {
      fs.rmSync(curPath, { recursive: true, force: true });
    } else {
      fs.unlinkSync(curPath);
    }
  }
}

/**
 * Creates a zip snapshot of a directory or a single file.
 */
function zipDirectory(sourceDir, outPath) {
  const zip = new AdmZip();
  if (!fs.existsSync(sourceDir)) {
    throw new Error(`Source path does not exist: ${sourceDir}`);
  }
  const stat = fs.statSync(sourceDir);
  if (stat.isFile()) {
    zip.addLocalFile(sourceDir);
  } else {
    zip.addLocalFolder(sourceDir);
  }
  zip.writeZip(outPath);
}

/**
 * Unzips a snapshot to a directory or restores it as a single file.
 */
function unzipDirectory(zipPath, targetDir) {
  if (!fs.existsSync(zipPath)) {
    throw new Error(`Zip archive not found: ${zipPath}`);
  }
  
  let isFile = false;
  if (fs.existsSync(targetDir)) {
    isFile = fs.statSync(targetDir).isFile();
  } else {
    // If targetDir doesn't exist, check the ZIP archive contents
    const zip = new AdmZip(zipPath);
    const entries = zip.getEntries();
    if (entries.length === 1 && !entries[0].isDirectory) {
      isFile = true;
    }
  }

  if (isFile) {
    const parentDir = path.dirname(targetDir);
    if (!fs.existsSync(parentDir)) {
      fs.mkdirSync(parentDir, { recursive: true });
    }
    if (fs.existsSync(targetDir)) {
      fs.unlinkSync(targetDir);
    }
    const zip = new AdmZip(zipPath);
    zip.extractAllTo(parentDir, true);
  } else {
    ensureDir(targetDir);
    clearFolder(targetDir);
    const zip = new AdmZip(zipPath);
    zip.extractAllTo(targetDir, true);
  }
}

export function createSnapshot(gameId, comment = '', isSystemAuto = false) {
  const game = db.getGame(gameId);
  if (!game) {
    throw new Error(`Game with ID "${gameId}" not found.`);
  }

  const savePath = game.savePath;
  let isFile = false;
  if (fs.existsSync(savePath)) {
    isFile = fs.statSync(savePath).isFile();
  }

  if (!fs.existsSync(savePath)) {
    // If it does not exist, assume it's a file if it has a typical file extension, otherwise a directory
    const ext = path.extname(savePath);
    if (ext && ext.length > 1) {
      isFile = true;
      ensureDir(path.dirname(savePath));
    } else {
      ensureDir(savePath);
    }
  } else if (!isFile) {
    if (fs.readdirSync(savePath).length === 0) {
      ensureDir(savePath);
    }
  }

  const settings = db.getSettings();
  const gameBackupDir = path.join(settings.backupsDir, gameId, game.activeBranch);
  ensureDir(gameBackupDir);

  const timestamp = Date.now();
  const snapshotId = `snap_${timestamp}`;
  const zipName = `${snapshotId}.zip`;
  const zipPath = path.join(gameBackupDir, zipName);

  // Perform the zip compression
  zipDirectory(savePath, zipPath);

  // Get size of zip file
  const stats = fs.statSync(zipPath);
  const sizeBytes = stats.size;

  const snapshotMetadata = {
    id: snapshotId,
    timestamp: new Date(timestamp).toISOString(),
    comment: comment || (isSystemAuto ? 'Auto backup' : 'Manual snapshot'),
    isSystemAuto,
    zipPath,
    sizeBytes,
    branch: game.activeBranch
  };

  // Add snapshot to database
  const branches = game.branches || {};
  if (!branches[game.activeBranch]) {
    branches[game.activeBranch] = { name: game.activeBranch, snapshots: [] };
  }
  branches[game.activeBranch].snapshots.push(snapshotMetadata);
  
  // Enforce custom snapshot retention limit per game (defaults to 5)
  const maxSnapshots = (game.maxSnapshots !== undefined) ? game.maxSnapshots : 5;
  if (maxSnapshots > 0 && branches[game.activeBranch].snapshots.length > maxSnapshots) {
    const numToDelete = branches[game.activeBranch].snapshots.length - maxSnapshots;
    const deletedSnaps = branches[game.activeBranch].snapshots.splice(0, numToDelete);
    for (const snap of deletedSnaps) {
      try {
        if (fs.existsSync(snap.zipPath)) {
          fs.unlinkSync(snap.zipPath);
        }
      } catch (err) {
        console.error(`[Snapshot] Failed to delete pruned snapshot file ${snap.zipPath}:`, err.message);
      }
    }
  }
  
  db.updateGame(gameId, { branches });

  console.log(`[Snapshot] Created "${snapshotId}" for game "${game.name}" on branch "${game.activeBranch}" (${(sizeBytes / 1024).toFixed(1)} KB)`);
  return snapshotMetadata;
}

export function restoreSnapshot(gameId, snapshotId) {
  const game = db.getGame(gameId);
  if (!game) throw new Error(`Game "${gameId}" not found.`);

  // Find snapshot in any branch
  let targetSnapshot = null;
  for (const branchName in game.branches) {
    const snap = game.branches[branchName].snapshots.find(s => s.id === snapshotId);
    if (snap) {
      targetSnapshot = snap;
      break;
    }
  }

  if (!targetSnapshot) {
    throw new Error(`Snapshot "${snapshotId}" not found for game "${game.name}".`);
  }

  // Create safety restore point first if there are actual files in the save folder
  let hasFiles = false;
  if (fs.existsSync(game.savePath)) {
    const isFile = fs.statSync(game.savePath).isFile();
    if (isFile) {
      hasFiles = true;
    } else if (fs.readdirSync(game.savePath).length > 0) {
      hasFiles = true;
    }
  }

  if (hasFiles) {
    try {
      createSnapshot(gameId, `Pre-rollback safety restore point (before restoring ${snapshotId})`, true);
    } catch (e) {
      console.warn('[Snapshot] Failed to create safety rollback snapshot, continuing restore anyway:', e.message);
    }
  }

  // Unzip target snapshot to game save folder
  unzipDirectory(targetSnapshot.zipPath, game.savePath);
  
  console.log(`[Snapshot] Restored "${snapshotId}" to "${game.savePath}"`);
  return targetSnapshot;
}

export function createBranch(gameId, branchName) {
  const game = db.getGame(gameId);
  if (!game) throw new Error(`Game "${gameId}" not found.`);

  const cleanBranchName = branchName.toLowerCase().replace(/[^a-z0-9_-]/g, '');
  if (!cleanBranchName) throw new Error('Invalid branch name.');
  
  const branches = game.branches || {};
  if (branches[cleanBranchName]) {
    throw new Error(`Branch "${cleanBranchName}" already exists.`);
  }

  // Create branch starting with no snapshots (or copy current branch snapshots)
  branches[cleanBranchName] = {
    name: cleanBranchName,
    snapshots: []
  };

  db.updateGame(gameId, { branches });
  console.log(`[Branch] Created branch "${cleanBranchName}" for game "${game.name}"`);
  return branches[cleanBranchName];
}

export function switchBranch(gameId, targetBranchName) {
  const game = db.getGame(gameId);
  if (!game) throw new Error(`Game "${gameId}" not found.`);

  const currentBranchName = game.activeBranch;
  if (currentBranchName === targetBranchName) {
    return; // Already on this branch
  }

  if (!game.branches[targetBranchName]) {
    throw new Error(`Branch "${targetBranchName}" does not exist.`);
  }

  // 1. Take a snapshot of the current active save files and store them under the current branch
  let currentHadFiles = false;
  let isFile = false;
  if (fs.existsSync(game.savePath)) {
    isFile = fs.statSync(game.savePath).isFile();
    if (isFile) {
      currentHadFiles = true;
    } else if (fs.readdirSync(game.savePath).length > 0) {
      currentHadFiles = true;
    }
  }

  if (currentHadFiles) {
    try {
      createSnapshot(gameId, `Auto backup before switching to branch "${targetBranchName}"`, true);
    } catch (e) {
      console.warn('[Snapshot] Safety snapshot failed before branch switch:', e.message);
    }
  }

  // 2. Clear current save folder or delete file
  if (isFile) {
    if (fs.existsSync(game.savePath)) {
      fs.unlinkSync(game.savePath);
    }
  } else {
    clearFolder(game.savePath);
  }

  // 3. Update database active branch
  db.updateGame(gameId, { activeBranch: targetBranchName });

  // 4. Restore latest snapshot from the target branch if it has any snapshots
  const targetBranch = game.branches[targetBranchName];
  if (targetBranch.snapshots && targetBranch.snapshots.length > 0) {
    const latestSnapshot = targetBranch.snapshots[targetBranch.snapshots.length - 1];
    try {
      unzipDirectory(latestSnapshot.zipPath, game.savePath);
      console.log(`[Branch] Switched to branch "${targetBranchName}" and restored latest snapshot "${latestSnapshot.id}"`);
    } catch (err) {
      console.error(`[Branch] Failed to restore branch snapshot: ${err.message}`);
    }
  } else {
    console.log(`[Branch] Switched to empty branch "${targetBranchName}". Save folder cleared.`);
  }
}

export function getLatestSnapshot(gameId, branchName = null) {
  const game = db.getGame(gameId);
  if (!game) return null;
  const branch = branchName || game.activeBranch;
  const snapshots = game.branches[branch]?.snapshots || [];
  if (snapshots.length === 0) return null;
  return snapshots[snapshots.length - 1];
}
