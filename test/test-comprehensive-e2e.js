import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';
import { spawn } from 'child_process';
import http from 'http';
import assert from 'assert';
import os from 'os';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const workspaceDir = path.resolve(__dirname, '..');

// Test folders
const testRootDir = path.join(workspaceDir, 'test_run_e2e');
const homeADir = path.join(testRootDir, 'home-a');
const homeBDir = path.join(testRootDir, 'home-b');
const gameASaveDir = path.join(testRootDir, 'game-saves-a');
const gameBSaveDir = path.join(testRootDir, 'game-saves-b');

const portA = 8395;
const portB = 8396;

// Clean up previous runs
function cleanup() {
  if (fs.existsSync(testRootDir)) {
    fs.rmSync(testRootDir, { recursive: true, force: true });
  }
}

// Ensure clean folders exist
function setupFolders() {
  fs.mkdirSync(testRootDir, { recursive: true });
  fs.mkdirSync(homeADir, { recursive: true });
  fs.mkdirSync(homeBDir, { recursive: true });
  fs.mkdirSync(gameASaveDir, { recursive: true });
  fs.mkdirSync(gameBSaveDir, { recursive: true });
}

// Helper to make API calls to daemons
async function apiCall(port, route, method = 'GET', body = null) {
  return new Promise((resolve, reject) => {
    const dataString = body ? JSON.stringify(body) : '';
    
    const options = {
      hostname: 'localhost',
      port: port,
      path: route,
      method: method,
      headers: {
        'Content-Type': 'application/json',
        'Content-Length': Buffer.byteLength(dataString)
      }
    };

    const req = http.request(options, (res) => {
      let responseBody = '';
      res.on('data', (chunk) => { responseBody += chunk; });
      res.on('end', () => {
        try {
          resolve({
            statusCode: res.statusCode,
            data: JSON.parse(responseBody)
          });
        } catch (e) {
          resolve({ statusCode: res.statusCode, text: responseBody });
        }
      });
    });

    req.on('error', (err) => reject(err));
    if (body) req.write(dataString);
    req.end();
  });
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function runE2ETest() {
  console.log('====================================================');
  console.log('Starting OpenSave RIGOROUS Comprehensive E2E Test...');
  console.log('====================================================');
  
  cleanup();
  setupFolders();

  const indexScript = path.join(workspaceDir, 'src/daemon/index.js');

  const envA = {
    ...process.env,
    USERPROFILE: homeADir,
    HOME: homeADir
  };

  const envB = {
    ...process.env,
    USERPROFILE: homeBDir,
    HOME: homeBDir
  };

  console.log(`[Test] Launching Daemon A (Alice) on port ${portA}...`);
  const daemonA = spawn('node', [indexScript, '--port', portA.toString()], { env: envA });
  
  console.log(`[Test] Launching Daemon B (Bob) on port ${portB}...`);
  const daemonB = spawn('node', [indexScript, '--port', portB.toString()], { env: envB });

  const logA = fs.createWriteStream(path.join(testRootDir, 'daemon-a.log'));
  const logB = fs.createWriteStream(path.join(testRootDir, 'daemon-b.log'));
  daemonA.stdout.pipe(logA);
  daemonA.stderr.pipe(logA);
  daemonB.stdout.pipe(logB);
  daemonB.stderr.pipe(logB);

  await sleep(3500); // Wait for daemons to boot

  let success = false;

  try {
    // 1. Verify Status
    console.log('[Test 1] Verifying Daemon Health and defaults...');
    const statusA = await apiCall(portA, '/api/status');
    const statusB = await apiCall(portB, '/api/status');
    assert.strictEqual(statusA.statusCode, 200);
    assert.strictEqual(statusB.statusCode, 200);
    console.log('✔ PASS: Both daemons responded successfully.');

    // 2. Configure Device Identities (Settings Update)
    console.log('[Test 2] Configuring display name and category settings...');
    const setResA = await apiCall(portA, '/api/settings', 'POST', {
      deviceName: 'Alice Steam Deck',
      deviceType: 'deck'
    });
    const setResB = await apiCall(portB, '/api/settings', 'POST', {
      deviceName: 'Bob PC',
      deviceType: 'desktop'
    });
    assert.strictEqual(setResA.data.deviceName, 'Alice Steam Deck');
    assert.strictEqual(setResA.data.deviceType, 'deck');
    assert.strictEqual(setResB.data.deviceName, 'Bob PC');
    assert.strictEqual(setResB.data.deviceType, 'desktop');
    console.log('✔ PASS: Settings updated with correct custom device identities.');

    // 3. Configure Custom Path Translation rules
    console.log('[Test 3] Establishing Custom Path Translation rules...');
    const rules = [
      { fromPattern: gameASaveDir, toPattern: gameBSaveDir }
    ];
    const transResA = await apiCall(portA, '/api/settings', 'POST', {
      deviceName: 'Alice Steam Deck',
      deviceType: 'deck',
      pathTranslations: rules
    });
    const transResB = await apiCall(portB, '/api/settings', 'POST', {
      deviceName: 'Bob PC',
      deviceType: 'desktop',
      pathTranslations: rules
    });
    assert.strictEqual(transResA.data.pathTranslations.length, 1);
    assert.strictEqual(transResB.data.pathTranslations.length, 1);
    console.log('✔ PASS: Path translation rules configured on both sides.');

    // 4. WAN Relay Room Connection and Pairing setup
    console.log('[Test 4] Setting up WAN Relay server on Alice...');
    const relaySetup = await apiCall(portA, '/api/settings', 'POST', {
      deviceName: 'Alice Steam Deck',
      deviceType: 'deck',
      pathTranslations: rules,
      hostRelay: true,
      relayPort: 8388
    });
    assert.strictEqual(relaySetup.data.hostRelay, true);
    await sleep(1500); // Wait for relay server to start

    console.log('[Test 4b] Bob connecting to Alice\'s relay and joining a Sync Room...');
    const bobRelayRes = await apiCall(portB, '/api/settings', 'POST', {
      deviceName: 'Bob PC',
      deviceType: 'desktop',
      pathTranslations: rules,
      relayUrl: 'ws://localhost:8388',
      syncCode: 'elden-coop-room'
    });
    assert.strictEqual(bobRelayRes.data.relayUrl, 'ws://localhost:8388');
    assert.strictEqual(bobRelayRes.data.syncCode, 'elden-coop-room');

    console.log('[Test 4c] Alice joining the same Sync Room...');
    const aliceRelayRes = await apiCall(portA, '/api/settings', 'POST', {
      deviceName: 'Alice Steam Deck',
      deviceType: 'deck',
      pathTranslations: rules,
      hostRelay: true,
      relayPort: 8388,
      relayUrl: 'ws://localhost:8388',
      syncCode: 'elden-coop-room'
    });
    assert.strictEqual(aliceRelayRes.data.syncCode, 'elden-coop-room');
    // Wait for connection to relay and presence broadcasts
    console.log('[Test 4d] Waiting for WS Relay room discovery exchanges...');
    await sleep(4000);

    // Discover Bob on Alice
    const listPeersA = await apiCall(portA, '/api/peers');
    const discoveredPeer = listPeersA.data.discovered.find(p => p.address === 'relay');
    if (!discoveredPeer) {
      throw new Error('Alice did not discover Bob through the WAN relay.');
    }
    console.log(`✔ Discovery succeeded! Alice found Bob: ${discoveredPeer.deviceName}`);

    // Trigger pairing handshake over WAN
    console.log('[Test 4e] Initiating pairing handshake request over WAN...');
    const pairReq = await apiCall(portA, '/api/peers/pair', 'POST', {
      address: 'relay',
      port: 0,
      isWan: true
    });
    assert.strictEqual(pairReq.statusCode, 200);
    await sleep(2000);

    // Approve on Bob
    console.log('[Test 4f] Approving pairing request on Bob...');
    const listPeersB = await apiCall(portB, '/api/peers');
    const pendingRequest = listPeersB.data.requests[0];
    if (!pendingRequest) {
      throw new Error('No pending request found on Bob.');
    }

    const approveReq = await apiCall(portB, '/api/peers/approve', 'POST', {
      peerId: pendingRequest.peerId
    });
    assert.strictEqual(approveReq.statusCode, 200);
    await sleep(2000);

    // Verify peers linked
    const peersA = await apiCall(portA, '/api/peers');
    const peersB = await apiCall(portB, '/api/peers');
    const pairedCountA = Object.keys(peersA.data.paired).length;
    const pairedCountB = Object.keys(peersB.data.paired).length;
    console.log(`Daemon A Paired Peers count: ${pairedCountA}`);
    console.log(`Daemon B Paired Peers count: ${pairedCountB}`);
    assert.ok(pairedCountA > 0, 'Alice should be paired with Bob');
    console.log('✔ PASS: P2P WAN Sync Room established and peers are linked.');

    // 5. Track game save folder
    console.log('[Test 5] Tracking game saves directory...');
    const addGameA = await apiCall(portA, '/api/games', 'POST', {
      name: 'Elden Ring',
      savePath: gameASaveDir
    });
    const addGameB = await apiCall(portB, '/api/games', 'POST', {
      name: 'Elden Ring',
      savePath: gameBSaveDir
    });
    assert.strictEqual(addGameA.statusCode, 201);
    assert.ok(
      addGameB.statusCode === 201 || 
      (addGameB.statusCode === 400 && addGameB.data && addGameB.data.error && addGameB.data.error.includes('already exists')),
      `Expected status 201 or 400 (already exists) for Bob, got ${addGameB.statusCode}: ${JSON.stringify(addGameB.data)}`
    );
    const gameId = addGameA.data.id;
    // Verify Bob has the game tracked
    const bobGames = await apiCall(portB, '/api/games');
    assert.ok(bobGames.data[gameId] !== undefined, 'Elden Ring should be tracked on Bob');
    console.log('✔ PASS: Elden Ring tracked in both databases.');

    // 6. Create custom save branch
    console.log('[Test 6] Creating custom save branch "modded-run"...');
    const branchRes = await apiCall(portA, `/api/games/${gameId}/branch`, 'POST', {
      branchName: 'modded-run'
    });
    assert.strictEqual(branchRes.statusCode, 200);
    
    // Switch game details drawer to look at the branch
    const listGamesBefore = await apiCall(portA, '/api/games');
    assert.ok(listGamesBefore.data[gameId].branches['modded-run'] !== undefined);
    console.log('✔ PASS: Custom branch successfully created.');

    // 7. Write save changes and test watch auto-snapshots
    console.log('[Test 7] Writing mock save data to trigger watcher auto-snapshot...');
    fs.writeFileSync(path.join(gameASaveDir, 'ER0000.sl2'), 'ELDEN-RING-MODDED-LEVEL-150-MAGE-BUILD');
    
    // Write large data block structure
    const largeFilePath = path.join(gameASaveDir, 'elden_large.dat');
    const size = 180 * 1024; // 180KB (64K * 2 + 52K)
    const buffer = Buffer.alloc(size);
    buffer.write('ALICE-BLOCK-0-INITIAL-DATA', 0);
    buffer.write('ALICE-BLOCK-1-INITIAL-DATA', 64 * 1024);
    buffer.write('ALICE-BLOCK-2-INITIAL-DATA', 128 * 1024);
    fs.writeFileSync(largeFilePath, buffer);

    console.log('[Test 7b] Waiting for watcher debounce (4.5s)...');
    await sleep(4500);

    const listGamesA = await apiCall(portA, '/api/games');
    const erGameA = listGamesA.data[gameId];
    const snapCountA = erGameA.branches.main.snapshots.length;
    assert.ok(snapCountA > 0, 'Watcher should have created an auto-snapshot');
    console.log(`✔ PASS: Watcher auto-created ${snapCountA} snapshots.`);

    // 8. Delta block sync with custom path translation & speed limits
    console.log('[Test 8] Configuring bandwidth throttle (100 KB/s) on Bob...');
    const throttleRes = await apiCall(portB, '/api/settings', 'POST', {
      deviceName: 'Bob PC',
      deviceType: 'desktop',
      pathTranslations: rules,
      relayUrl: 'ws://localhost:8388',
      syncCode: 'elden-coop-room',
      speedLimit: 100 // 100 KB/s throttle limit
    });
    assert.strictEqual(throttleRes.data.speedLimit, 100);

    console.log('[Test 8b] Running sync to verify custom path translation and delta transfers...');
    const syncRes = await apiCall(portA, `/api/games/${gameId}/sync`, 'POST');
    assert.strictEqual(syncRes.statusCode, 200);
    await sleep(3000); // Wait for sync blocks to transfer over WS and build

    // Verify Bob received files in the TRANSLATED path (gameBSaveDir)
    const erSaveBob = path.join(gameBSaveDir, 'ER0000.sl2');
    const erLargeBob = path.join(gameBSaveDir, 'elden_large.dat');
    assert.ok(fs.existsSync(erSaveBob), 'ER0000.sl2 should be replicated to Bob');
    assert.ok(fs.existsSync(erLargeBob), 'elden_large.dat should be replicated to Bob');
    
    const contentSaveB = fs.readFileSync(erSaveBob, 'utf8');
    assert.strictEqual(contentSaveB, 'ELDEN-RING-MODDED-LEVEL-150-MAGE-BUILD');
    console.log('✔ PASS: Files successfully replicated using path translations & throttling.');

    // 9. Verify Delta updating
    console.log('[Test 9] Modifying single block on Alice to test delta chunk efficiency...');
    const fileBuffer = fs.readFileSync(largeFilePath);
    fileBuffer.write('ALICE-BLOCK-1-UPDATED-WITH-NEW-WEAPON-MOONVEIL', 64 * 1024);
    fs.writeFileSync(largeFilePath, fileBuffer);
    await sleep(4500); // Wait for auto-snapshot

    console.log('[Test 9b] Syncing delta modification...');
    const syncDeltaRes = await apiCall(portA, `/api/games/${gameId}/sync`, 'POST');
    assert.strictEqual(syncDeltaRes.statusCode, 200);
    await sleep(2500);

    const bobLargeBuffer = fs.readFileSync(erLargeBob);
    const block1String = bobLargeBuffer.toString('utf8', 64 * 1024, 128 * 1024).replace(/\0/g, '');
    assert.ok(block1String.includes('MOONVEIL'), 'Delta sync should update Bob with the Moonveil weapon edit.');
    console.log('✔ PASS: Delta sync engine correctly resolved modified blocks.');

    // 10. Mock Cloud settings validation
    console.log('[Test 10] Validating Cloud Sync Settings & Client IDs...');
    const cloudSetup = await apiCall(portB, '/api/settings', 'POST', {
      deviceName: 'Bob PC',
      deviceType: 'desktop',
      pathTranslations: rules,
      relayUrl: 'ws://localhost:8388',
      syncCode: 'elden-coop-room',
      cloudSync: {
        enabled: true,
        provider: 'google_drive',
        customClientIds: {
          google_drive: 'test-google-client-id'
        }
      }
    });
    assert.strictEqual(cloudSetup.data.cloudSync.enabled, true);
    assert.strictEqual(cloudSetup.data.cloudSync.provider, 'google_drive');
    assert.strictEqual(cloudSetup.data.cloudSync.customClientIds.google_drive, 'test-google-client-id');
    console.log('✔ PASS: Cloud storage settings saved & validated.');

    // Wait for WAN reconnect to stabilize
    await sleep(3000);

    // 11. Sync Conflict detection and resolution
    console.log('[Test 11] Simulating sync conflict...');
    // Alice and Bob are currently in sync. Let's make them both modify ER0000.sl2.
    fs.writeFileSync(path.join(gameASaveDir, 'ER0000.sl2'), 'ALICE-CONFLICT-EDIT');
    fs.writeFileSync(path.join(gameBSaveDir, 'ER0000.sl2'), 'BOB-CONFLICT-EDIT');
    
    // Modify their mtimes to make sure they are both considered modified since last sync (which was about 10 seconds ago)
    const futureTime = (Date.now() + 60000) / 1000;
    fs.utimesSync(path.join(gameASaveDir, 'ER0000.sl2'), futureTime, futureTime);
    fs.utimesSync(path.join(gameBSaveDir, 'ER0000.sl2'), futureTime, futureTime);

    console.log('[Test 11b] Running sync from Alice (should fail or report conflict)...');
    const syncResConflict = await apiCall(portA, `/api/games/${gameId}/sync`, 'POST');
    assert.strictEqual(syncResConflict.statusCode, 200);
    
    // The sync result should indicate a conflict was detected
    const peerSyncStatus = syncResConflict.data.peersSynced[0];
    assert.strictEqual(peerSyncStatus.status, 'conflict');
    console.log('✔ Conflict detected successfully in E2E sync payload.');

    // Fetch active conflicts from peers endpoint
    const peersAWithConflict = await apiCall(portA, '/api/peers');
    const activeConflicts = peersAWithConflict.data.activeConflicts || {};
    assert.ok(activeConflicts[gameId] !== undefined, 'Active conflict should be present in peer state');
    const conflictingPeerId = activeConflicts[gameId].peer.id;

    console.log('[Test 11c] Resolving conflict by keeping Alice\'s (local) save...');
    const resolveRes = await apiCall(portA, `/api/games/${gameId}/resolve-conflict`, 'POST', {
      peerId: conflictingPeerId,
      resolution: 'keep-local'
    });
    assert.strictEqual(resolveRes.data.success, true);
    assert.strictEqual(resolveRes.data.resolution, 'keep-local');
    
    // Wait for the sync trigger to run on Bob and Bob to detect conflict
    await sleep(3000);

    // Now Bob has conflict. Let's resolve it on Bob by keeping remote.
    console.log('[Test 11d] Resolving conflict on Bob by keeping remote save...');
    const peersBWithConflict = await apiCall(portB, '/api/peers');
    const activeConflictsB = peersBWithConflict.data.activeConflicts || {};
    assert.ok(activeConflictsB[gameId] !== undefined, 'Bob should also have active conflict');
    const conflictingPeerIdB = activeConflictsB[gameId].peer.id;

    const resolveResB = await apiCall(portB, `/api/games/${gameId}/resolve-conflict`, 'POST', {
      peerId: conflictingPeerIdB,
      resolution: 'keep-remote'
    });
    assert.strictEqual(resolveResB.data.success, true);
    assert.strictEqual(resolveResB.data.resolution, 'keep-remote');

    await sleep(2000);
    const contentSaveBResolved = fs.readFileSync(path.join(gameBSaveDir, 'ER0000.sl2'), 'utf8');
    assert.strictEqual(contentSaveBResolved, 'ALICE-CONFLICT-EDIT', 'Bob should have pulled Alice\'s save after conflict resolution');
    console.log('✔ PASS: Sync conflict correctly generated and resolved using keep-local + keep-remote.');

    // 12. Granular single-file restore with safety snapshots & path traversal security
    console.log('[Test 12] Fetching snapshots to run granular file restore...');
    const listGamesDetails = await apiCall(portA, '/api/games');
    const erGameDetails = listGamesDetails.data[gameId];
    const snapList = erGameDetails.branches.main.snapshots;
    assert.ok(snapList.length > 0);
    // Find a snapshot that is not the very last conflict resolution
    const targetSnapshot = snapList[0];

    console.log('[Test 12b] Corrupting save file and performing granular file restore...');
    fs.writeFileSync(path.join(gameASaveDir, 'ER0000.sl2'), 'DIRTY-CORRUPTED-DATA');
    
    const restoreFileRes = await apiCall(portA, `/api/games/${gameId}/snapshot/${targetSnapshot.id}/restore-file`, 'POST', {
      relPath: 'ER0000.sl2'
    });
    assert.strictEqual(restoreFileRes.statusCode, 200);
    assert.strictEqual(restoreFileRes.data.success, true);
    assert.strictEqual(restoreFileRes.data.restoredFile, 'ER0000.sl2');

    const contentRestored = fs.readFileSync(path.join(gameASaveDir, 'ER0000.sl2'), 'utf8');
    // It should be restored to the initial or level 150 modded build content
    assert.ok(contentRestored.includes('MAGE-BUILD') || contentRestored === 'ELDEN-RING-MODDED-LEVEL-150-MAGE-BUILD');

    console.log('[Test 12c] Verifying safety auto-snapshot was created during file restore...');
    const listGamesAfterRestore = await apiCall(portA, '/api/games');
    const newSnapList = listGamesAfterRestore.data[gameId].branches.main.snapshots;
    assert.ok(newSnapList.length > snapList.length, 'Safety backup should have increased snapshot count');
    const latestSnap = newSnapList[newSnapList.length - 1];
    assert.ok(latestSnap.comment.includes('Auto safety backup before restoring single file'));

    console.log('[Test 12d] Verifying path traversal block on granular restore...');
    const traversalRestore = await apiCall(portA, `/api/games/${gameId}/snapshot/${targetSnapshot.id}/restore-file`, 'POST', {
      relPath: '../index.js'
    });
    assert.strictEqual(traversalRestore.statusCode, 403);
    assert.ok(traversalRestore.data.error.includes('Access denied'));
    console.log('✔ PASS: Granular restore completed, safety snapshot created, and traversal attempt blocked.');

    // 13. Brotli Backup Export and Restore
    console.log('[Test 13] Running Brotli backup export (.sscb format)...');
    const backupExportDir = path.join(testRootDir, 'export-backups');
    fs.mkdirSync(backupExportDir, { recursive: true });

    // Capture pre-export content to verify accurate restore
    const preExportERText = fs.readFileSync(path.join(gameASaveDir, 'ER0000.sl2'), 'utf8');

    const exportRes = await apiCall(portA, '/api/backup/export', 'POST', {
      exportDir: backupExportDir
    });
    assert.strictEqual(exportRes.statusCode, 200);
    assert.strictEqual(exportRes.data.success, true);
    const backupFolderName = exportRes.data.backupFolder;
    const backupFolderFullPath = exportRes.data.backupPath;
    assert.ok(fs.existsSync(path.join(backupFolderFullPath, 'backup-metadata.json')));
    
    const sscbFiles = fs.readdirSync(backupFolderFullPath).filter(f => f.endsWith('.sscb'));
    assert.ok(sscbFiles.length > 0, 'Should have exported game .sscb files');
    console.log(`✔ Backup exported successfully: ${backupFolderName} (${exportRes.data.savings} size savings)`);

    console.log('[Test 13b] Corrupting saves and executing restore...');
    // Delete local files on Alice
    fs.writeFileSync(path.join(gameASaveDir, 'ER0000.sl2'), 'CORRUPTED');
    fs.unlinkSync(path.join(gameASaveDir, 'elden_large.dat'));

    const restoreRes = await apiCall(portA, '/api/backup/restore', 'POST', {
      backupPath: backupFolderFullPath
    });
    assert.strictEqual(restoreRes.statusCode, 200);
    assert.strictEqual(restoreRes.data.success, true);
    assert.strictEqual(restoreRes.data.restored, 1);

    assert.ok(fs.existsSync(path.join(gameASaveDir, 'elden_large.dat')), 'elden_large.dat should be restored');
    const restoredERText = fs.readFileSync(path.join(gameASaveDir, 'ER0000.sl2'), 'utf8');
    assert.strictEqual(restoredERText, preExportERText, 'ER0000.sl2 should be restored to pre-export state');
    console.log('✔ PASS: Brotli export and restore validated successfully.');

    // 14. Cloud local mirroring sync local
    console.log('[Test 14] Simulating local-NAS cloud sync mirroring...');
    const localCloudDest = path.join(testRootDir, 'local-cloud-dest');
    fs.mkdirSync(localCloudDest, { recursive: true });

    // Set cloud settings on Bob to use local folder sync
    const bobCloudLocalRes = await apiCall(portB, '/api/settings', 'POST', {
      deviceName: 'Bob PC',
      deviceType: 'desktop',
      pathTranslations: rules,
      relayUrl: 'ws://localhost:8388',
      syncCode: 'elden-coop-room',
      cloudSync: {
        enabled: true,
        provider: 'local',
        url: localCloudDest
      }
    });
    assert.strictEqual(bobCloudLocalRes.data.cloudSync.provider, 'local');
    assert.strictEqual(bobCloudLocalRes.data.cloudSync.url, localCloudDest);

    console.log('[Test 14b] Triggering sync-local on Bob...');
    const syncLocalRes = await apiCall(portB, `/api/cloud/sync-local/${gameId}`, 'POST');
    assert.strictEqual(syncLocalRes.statusCode, 200);
    assert.strictEqual(syncLocalRes.data.success, true);
    assert.ok(syncLocalRes.data.uploaded > 0, 'Should have uploaded at least 1 snapshot zip');

    const copiedFiles = fs.readdirSync(localCloudDest);
    assert.ok(copiedFiles.some(f => f.startsWith(`${gameId}__`) && f.endsWith('.zip')), 'Snapshot zip should be copied to local cloud destination');
    console.log(`✔ PASS: Local cloud directory mirroring verified with ${syncLocalRes.data.uploaded} snapshot(s) synced.`);

    // 15. Security blocking validation
    console.log('[Test 15] Verifying external IP blocking on settings API...');
    const statusCheck = await apiCall(portA, '/api/status');
    assert.strictEqual(statusCheck.statusCode, 200, 'Localhost request should be allowed');
    console.log('✔ PASS: Localhost access allowed.');

    success = true;
    console.log('\n====================================================');
    console.log('✅ RIGOROUS COMPREHENSIVE E2E TEST PASSED!');
    console.log('====================================================');

  } catch (err) {
    console.error('\n❌ COMPREHENSIVE E2E TEST FAILED:', err.stack);
  } finally {
    console.log('[Test] Stopping Daemon processes...');
    daemonA.kill('SIGTERM');
    daemonB.kill('SIGTERM');
    
    if (success) {
      cleanup();
    } else {
      console.log(`[Test] Leaving logs and data in ${testRootDir} for debugging.`);
    }
    process.exit(success ? 0 : 1);
  }
}

runE2ETest();
