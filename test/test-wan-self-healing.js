import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';
import { spawn } from 'child_process';
import http from 'http';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const workspaceDir = path.resolve(__dirname, '..');

// Test folder pathways
const testRootDir = path.join(workspaceDir, 'test_run_wan_self_healing');
const homeADir = path.join(testRootDir, 'home-a');
const homeBDir = path.join(testRootDir, 'home-b');

const portA = 8395;
const portB = 8396;
const relayPort = 8386;

function cleanup() {
  if (fs.existsSync(testRootDir)) {
    fs.rmSync(testRootDir, { recursive: true, force: true });
  }
}

function setupFolders() {
  fs.mkdirSync(testRootDir, { recursive: true });
  fs.mkdirSync(homeADir, { recursive: true });
  fs.mkdirSync(homeBDir, { recursive: true });
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

async function runSelfHealingTest() {
  console.log('====================================================');
  console.log('Starting OpenSave WAN Self-Healing Integration Test...');
  console.log('====================================================');

  cleanup();
  setupFolders();

  const relayScript = path.join(workspaceDir, 'src/relay-server.js');
  const indexScript = path.join(workspaceDir, 'src/daemon/index.js');

  // 1. Launch Relay Server
  console.log(`[Test] Launching WAN Relay Server on port ${relayPort}...`);
  const relayServer = spawn('node', [relayScript]);
  const logRelay = fs.createWriteStream(path.join(testRootDir, 'relay.log'));
  relayServer.stdout.pipe(logRelay);
  relayServer.stderr.pipe(logRelay);

  await sleep(1500);

  // 2. Launch Daemon A
  const envA = { ...process.env, USERPROFILE: homeADir, HOME: homeADir };
  console.log(`[Test] Launching Daemon A on port ${portA}...`);
  const daemonA = spawn('node', [indexScript, '--port', portA.toString()], { env: envA });
  const logA = fs.createWriteStream(path.join(testRootDir, 'daemon-a.log'));
  daemonA.stdout.pipe(logA);
  daemonA.stderr.pipe(logA);

  // 3. Launch Daemon B
  let envB = { ...process.env, USERPROFILE: homeBDir, HOME: homeBDir };
  console.log(`[Test] Launching Daemon B on port ${portB}...`);
  let daemonB = spawn('node', [indexScript, '--port', portB.toString()], { env: envB });
  let logB = fs.createWriteStream(path.join(testRootDir, 'daemon-b.log'));
  daemonB.stdout.pipe(logB);
  daemonB.stderr.pipe(logB);

  await sleep(3000);

  let success = false;

  try {
    // Check daemon health
    const statusA = await apiCall(portA, '/api/status');
    const statusB = await apiCall(portB, '/api/status');
    if (statusA.statusCode !== 200 || statusB.statusCode !== 200) {
      throw new Error('Daemons did not start properly.');
    }

    const peerIdA = statusA.data.settings.nodeId;
    const peerIdB = statusB.data.settings.nodeId;
    console.log(`[Test] Peer A: ${peerIdA}, Peer B: ${peerIdB}`);

    // Join room
    console.log('[Test] Configuring WAN sync room codes...');
    await apiCall(portA, '/api/settings', 'POST', {
      deviceName: 'Device-A-SelfHeal',
      relayUrl: 'ws://localhost:8386',
      syncCode: 'self-heal-room-99'
    });

    await apiCall(portB, '/api/settings', 'POST', {
      deviceName: 'Device-B-SelfHeal',
      relayUrl: 'ws://localhost:8386',
      syncCode: 'self-heal-room-99'
    });

    await sleep(4000);

    // Verify discovery
    const listPeersA = await apiCall(portA, '/api/peers');
    const discoveredB = listPeersA.data.discovered.find(p => p.id === peerIdB);
    if (!discoveredB) {
      throw new Error('Daemon A did not discover Daemon B.');
    }

    // Pair A with B
    console.log('[Test] Pairing A and B...');
    await apiCall(portA, '/api/peers/pair', 'POST', {
      address: 'relay',
      port: 0,
      isWan: true,
      peerId: peerIdB
    });

    await sleep(1500);

    // B approves
    await apiCall(portB, '/api/peers/approve', 'POST', { peerId: peerIdA });
    await sleep(2000);

    // Verify paired status on both
    const peersA = (await apiCall(portA, '/api/peers')).data.paired;
    const peersB = (await apiCall(portB, '/api/peers')).data.paired;

    if (!peersA[peerIdB] || !peersB[peerIdA]) {
      throw new Error('Symmetric pairing was not established.');
    }
    console.log('✔ Symmetric pairing established successfully.');

    // 4. Simulate B going offline by killing Daemon B
    console.log('[Test] Stopping Daemon B (simulating offline/disconnected)...');
    daemonB.kill('SIGTERM');
    await sleep(1500);

    // 5. Unpair B on Daemon A (this cannot notify B since B is dead)
    console.log('[Test] Unpairing Daemon B on Daemon A while B is offline...');
    const unpairRes = await apiCall(portA, `/api/peers/${peerIdB}`, 'DELETE');
    if (unpairRes.statusCode !== 200) {
      throw new Error('Unpair request failed on Daemon A.');
    }

    // Verify A no longer has B paired
    const peersAAfterUnpair = (await apiCall(portA, '/api/peers')).data.paired;
    if (peersAAfterUnpair[peerIdB]) {
      throw new Error('Daemon A still has Daemon B paired after unpair command.');
    }
    console.log('✔ Daemon B successfully unpaired on Daemon A.');

    // 6. Restart Daemon B
    console.log('[Test] Restarting Daemon B (comes online with stale paired record for A)...');
    daemonB = spawn('node', [indexScript, '--port', portB.toString()], { env: envB });
    logB = fs.createWriteStream(path.join(testRootDir, 'daemon-b-restarted.log'));
    daemonB.stdout.pipe(logB);
    daemonB.stderr.pipe(logB);

    await sleep(4000); // Wait for Daemon B to reconnect to WAN room

    // Verify that B is now online again
    const statusBAfterRestart = await apiCall(portB, '/api/status');
    if (statusBAfterRestart.statusCode !== 200) {
      throw new Error('Daemon B did not start correctly after restart.');
    }

    console.log('[Test] Waiting for WAN heartbeat cycles to trigger self-healing...');
    // We wait up to 10 seconds for the ping/hello exchange and self-healing to trigger.
    await sleep(8000);

    // 7. Verify both daemons have each other unpaired now!
    const finalPeersA = (await apiCall(portA, '/api/peers')).data.paired;
    const finalPeersB = (await apiCall(portB, '/api/peers')).data.paired;

    const A_has_B = !!finalPeersA[peerIdB];
    const B_has_A = !!finalPeersB[peerIdA];

    if (A_has_B || B_has_A) {
      throw new Error(`Self-healing failed. A has B: ${A_has_B}, B has A: ${B_has_A}`);
    }

    success = true;
    console.log('\n====================================================');
    console.log('✅ WAN SELF-HEALING INTEGRATION TEST PASSED!');
    console.log('====================================================');

  } catch (err) {
    console.error('\n❌ WAN SELF-HEALING TEST FAILED:', err.message);
  } finally {
    console.log('[Test] Cleaning up processes...');
    daemonA.kill('SIGTERM');
    if (daemonB) daemonB.kill('SIGTERM');
    relayServer.kill('SIGTERM');

    await sleep(2000);
    if (success) {
      cleanup();
    }
    process.exit(success ? 0 : 1);
  }
}

runSelfHealingTest();
