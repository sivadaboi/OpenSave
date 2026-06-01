import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';
import { spawn } from 'child_process';
import http from 'http';
import WebSocket from 'ws';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const workspaceDir = path.resolve(__dirname, '..');

const testRootDir = path.join(workspaceDir, 'test_run_progress');
const homeADir = path.join(testRootDir, 'home-a');
const homeBDir = path.join(testRootDir, 'home-b');
const gameASaveDir = path.join(testRootDir, 'game-saves-a');
const gameBSaveDir = path.join(testRootDir, 'game-saves-b');

const portA = 8395;
const portB = 8396;

function cleanup() {
  if (fs.existsSync(testRootDir)) {
    fs.rmSync(testRootDir, { recursive: true, force: true });
  }
}

function setupFolders() {
  fs.mkdirSync(testRootDir, { recursive: true });
  fs.mkdirSync(homeADir, { recursive: true });
  fs.mkdirSync(homeBDir, { recursive: true });
  fs.mkdirSync(gameASaveDir, { recursive: true });
  fs.mkdirSync(gameBSaveDir, { recursive: true });
}

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

async function runTest() {
  console.log('====================================================');
  console.log('Starting Bidirectional Sync Progress & Error Tests...');
  console.log('====================================================');
  
  cleanup();
  setupFolders();

  const indexScript = path.join(workspaceDir, 'src/daemon/index.js');

  const envA = { ...process.env, USERPROFILE: homeADir, HOME: homeADir };
  const envB = { ...process.env, USERPROFILE: homeBDir, HOME: homeBDir };

  console.log(`[Test] Launching Daemon A on port ${portA}...`);
  const daemonA = spawn('node', [indexScript, '--port', portA.toString()], { env: envA });
  
  console.log(`[Test] Launching Daemon B on port ${portB}...`);
  const daemonB = spawn('node', [indexScript, '--port', portB.toString()], { env: envB });

  const logA = fs.createWriteStream(path.join(testRootDir, 'daemon-a.log'));
  const logB = fs.createWriteStream(path.join(testRootDir, 'daemon-b.log'));
  daemonA.stdout.pipe(logA);
  daemonA.stderr.pipe(logA);
  daemonB.stdout.pipe(logB);
  daemonB.stderr.pipe(logB);

  await sleep(3000);

  let success = false;
  let wsA = null;
  let wsB = null;

  try {
    // 1. Verify Status
    console.log('[Test] Verifying daemons health...');
    const statusA = await apiCall(portA, '/api/status');
    const statusB = await apiCall(portB, '/api/status');
    if (statusA.statusCode !== 200 || statusB.statusCode !== 200) {
      throw new Error('Daemons failed to initialize.');
    }

    // Configure settings
    await apiCall(portA, '/api/settings', 'POST', { deviceName: 'Device-A', autoSyncOnTrack: false });
    await apiCall(portB, '/api/settings', 'POST', { deviceName: 'Device-B', autoSyncOnTrack: false });

    // 2. Track game on both sides (with autoSync disabled to prevent automatic watcher triggers)
    console.log('[Test] Tracking game Celeste...');
    const addA = await apiCall(portA, '/api/games', 'POST', { name: 'Celeste', savePath: gameASaveDir, autoSync: false });
    const addB = await apiCall(portB, '/api/games', 'POST', { name: 'Celeste', savePath: gameBSaveDir, autoSync: false });
    if (addA.statusCode !== 201 || addB.statusCode !== 201) {
      throw new Error('Failed to track game Celeste.');
    }
    const gameId = addA.data.id;

    // 3. Pair Daemon A and Daemon B
    console.log('[Test] Pairing Daemons...');
    const reqPair = await apiCall(portA, '/api/peers/pair', 'POST', { address: '127.0.0.1', port: portB });
    if (reqPair.statusCode !== 200) {
      throw new Error(`Pairing request failed: ${JSON.stringify(reqPair)}`);
    }
    
    await sleep(1500);

    // Approve the pairing on Daemon B
    console.log('[Test] Approving pairing request on Daemon B...');
    const listRequests = await apiCall(portB, '/api/peers');
    const pendingRequest = listRequests.data.requests[0];
    if (!pendingRequest) {
      throw new Error('No pending pairing request found on Daemon B.');
    }

    const approveReq = await apiCall(portB, '/api/peers/approve', 'POST', { peerId: pendingRequest.peerId });
    if (approveReq.statusCode !== 200) {
      throw new Error('Failed to approve peer pairing.');
    }
    console.log('Pairing established.');

    // 4. Connect WebSockets to track real-time dashboard events
    console.log('[Test] Connecting WebSocket clients to both daemons...');
    const eventsA = [];
    const eventsB = [];

    wsA = new WebSocket(`ws://localhost:${portA}`);
    wsB = new WebSocket(`ws://localhost:${portB}`);

    wsA.on('message', (data) => {
      try {
        const msg = JSON.parse(data.toString());
        if (msg.event && msg.event.startsWith('sync-')) {
          eventsA.push(msg);
        }
      } catch (err) {}
    });

    wsB.on('message', (data) => {
      try {
        const msg = JSON.parse(data.toString());
        if (msg.event && msg.event.startsWith('sync-')) {
          eventsB.push(msg);
        }
      } catch (err) {}
    });

    const waitOpen = (ws) => new Promise((resolve, reject) => {
      if (ws.readyState === WebSocket.OPEN) {
        resolve();
      } else {
        ws.on('open', resolve);
        ws.on('error', reject);
      }
    });

    await waitOpen(wsA);
    await waitOpen(wsB);
    console.log('WebSockets connected successfully.');

    // Write a test file to A and manually snapshot it
    fs.writeFileSync(path.join(gameASaveDir, 'celeste_save.bin'), 'Celeste Game Save Content Extra Blocks'.repeat(100));
    console.log('[Test] Creating manual snapshot on Daemon A...');
    const snapA = await apiCall(portA, `/api/games/${gameId}/snapshot`, 'POST', { comment: 'Initial save' });
    if (snapA.statusCode !== 200) {
      throw new Error(`Failed to create manual snapshot on Daemon A. statusCode=${snapA.statusCode}, data=${JSON.stringify(snapA)}`);
    }

    // 5. Trigger Pull Sync from Daemon B (B will download from A)
    console.log('[Test] Triggering manual sync on B (downloading from A)...');
    const syncRes = await apiCall(portB, `/api/games/${gameId}/sync`, 'POST');
    if (syncRes.statusCode !== 200) {
      throw new Error(`Sync trigger failed with status ${syncRes.statusCode}`);
    }

    // Let the sockets capture events
    await sleep(1500);

    console.log('[Test] Verifying normal sync events on both peers...');
    // Assert events on B (downloader)
    console.log('Daemon B (Downloader) WebSocket Events:', JSON.stringify(eventsB, null, 2));
    const startEventB = eventsB.find(e => e.event === 'sync-start');
    const progressEventB = eventsB.find(e => e.event === 'sync-progress');
    const completeEventB = eventsB.find(e => e.event === 'sync-complete');

    if (!startEventB || !progressEventB || !completeEventB) {
      throw new Error('Daemon B (downloader) missed sync events.');
    }

    // Assert events on A (uploader)
    console.log('Daemon A (Uploader) WebSocket Events:', JSON.stringify(eventsA, null, 2));
    const startEventA = eventsA.find(e => e.event === 'sync-start');
    const progressEventA = eventsA.find(e => e.event === 'sync-progress');
    const completeEventA = eventsA.find(e => e.event === 'sync-complete');

    if (!startEventA || !progressEventA || !completeEventA) {
      throw new Error('Daemon A (uploader) missed sync events.');
    }

    // Check uploader's message
    if (!startEventA.data.message.includes('Uploading saves to')) {
      throw new Error(`Uploader message was wrong: "${startEventA.data.message}"`);
    }
    console.log(`\n✔ PASS: Uploader message correctly shows: "${startEventA.data.message}"`);

    // Check complete status
    if (completeEventA.data.result.status !== 'pushed') {
      throw new Error(`Uploader complete status was wrong: "${completeEventA.data.result.status}"`);
    }
    console.log('✔ PASS: Uploader completes with "pushed" status.');

    // 6. Test error propagation: Track a game on B with savePath pointing to a file (causing ENOTDIR)
    console.log('\n[Test] Testing error propagation...');
    // Clear captured events
    eventsA.length = 0;
    eventsB.length = 0;

    // Register a new game "Hades" where B's savePath is on a non-existent drive (forcing a filesystem error)
    const hadesDirB = 'Z:\\NonExistentDrive\\' + Math.random().toString(36).substring(7);
    const hadesDirA = path.join(testRootDir, 'hades-saves-a');
    fs.mkdirSync(hadesDirA, { recursive: true });

    const hadesA = await apiCall(portA, '/api/games', 'POST', { name: 'Hades', savePath: hadesDirA, autoSync: false });
    const hadesB = await apiCall(portB, '/api/games', 'POST', { name: 'Hades', savePath: hadesDirB, autoSync: false });
    const hadesId = hadesA.data.id;

    // Write file to A and manually snapshot it
    fs.writeFileSync(path.join(hadesDirA, 'hades_save.sav'), 'Hades Content');
    const snapHadesA = await apiCall(portA, `/api/games/${hadesId}/snapshot`, 'POST', { comment: 'Hades base' });
    if (snapHadesA.statusCode !== 200) {
      throw new Error(`Failed to create manual snapshot on Daemon A for Hades. statusCode=${snapHadesA.statusCode}, data=${JSON.stringify(snapHadesA)}`);
    }

    console.log('[Test] Triggering sync on B for Hades (should fail)...');
    // Trigger sync on B. It will pull and fail to write or create directory under hadesFileB because it is a file
    await apiCall(portB, `/api/games/${hadesId}/sync`, 'POST');
    await sleep(1500);

    console.log('Daemon B (Downloader) Hades Events:', JSON.stringify(eventsB, null, 2));
    console.log('Daemon A (Uploader) Hades Events:', JSON.stringify(eventsA, null, 2));

    const errorEventB = eventsB.find(e => e.event === 'sync-error');
    const errorEventA = eventsA.find(e => e.event === 'sync-error');

    if (!errorEventB) {
      throw new Error('Daemon B (downloader) did not capture a local sync-error.');
    }
    if (!errorEventA) {
      throw new Error('Daemon A (uploader) did not receive the sync-error from the puller.');
    }

    if (!errorEventA.data.error.includes('Upload to') && !errorEventA.data.error.includes('failed')) {
      throw new Error(`Uploader error message was wrong: "${errorEventA.data.error}"`);
    }

    console.log(`✔ PASS: Uploader received propagated error message: "${errorEventA.data.error}"`);
    console.log('✔ PASS: Downloader local error message: ', errorEventB.data.error);

    success = true;
  } catch (err) {
    console.error('❌ Test failed with error:', err);
  } finally {
    if (wsA) wsA.close();
    if (wsB) wsB.close();
    console.log('[Test] Terminating daemons...');
    daemonA.kill();
    daemonB.kill();
    cleanup();
  }

  if (success) {
    console.log('\n====================================================');
    console.log('✅ ALL PROGRESS & ERROR EVENT PROPAGATION TESTS PASSED!');
    console.log('====================================================');
    process.exit(0);
  } else {
    console.log('\n====================================================');
    console.log('❌ SOME TESTS FAILED');
    console.log('====================================================');
    process.exit(1);
  }
}

runTest();
