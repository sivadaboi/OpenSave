/**
 * test-lan-pairing.js
 * ─────────────────────────────────────────────────────────────
 * Comprehensive LAN-only pairing & sync test suite.
 *
 * Simulates two daemon instances (Alice / Bob) running on localhost
 * at different ports, exercising the FULL LAN pairing flow:
 *
 *   handshake → approve → approve-confirm → ping → sync → unpair → re-pair
 *
 * Note on UDP discovery: both daemons run on the same machine, so the
 * daemon's self-IP filter drops each other's UDP broadcasts (by design —
 * on two real devices on a LAN this works correctly). We test that each
 * daemon IS broadcasting, and that manual pairing (direct IP) works
 * perfectly, which is the real-world fallback path.
 * ─────────────────────────────────────────────────────────────
 */

import fs from 'fs';
import path from 'path';
import dgram from 'dgram';
import http from 'http';
import { fileURLToPath } from 'url';
import { spawn } from 'child_process';
import assert from 'assert';

const __filename = fileURLToPath(import.meta.url);
const __dirname  = path.dirname(__filename);
const ROOT       = path.resolve(__dirname, '..');

// ── Test directories ─────────────────────────────────────────
const testRoot  = path.join(ROOT, 'test_run_lan');
const homeA     = path.join(testRoot, 'home-a');
const homeB     = path.join(testRoot, 'home-b');
const savesA    = path.join(testRoot, 'saves-a');
const savesB    = path.join(testRoot, 'saves-b');

// ── Ports ─────────────────────────────────────────────────────
const portA = 8490;   // Alice's API
const portB = 8491;   // Bob's API
const DISCOVERY_UDP_PORT = 8385;

const sleep = ms => new Promise(r => setTimeout(r, ms));

// ── Cleanup / Setup ───────────────────────────────────────────
function cleanup() {
  if (fs.existsSync(testRoot)) fs.rmSync(testRoot, { recursive: true, force: true });
}
function setup() {
  [testRoot, homeA, homeB, savesA, savesB].forEach(d => fs.mkdirSync(d, { recursive: true }));
}

// ── HTTP helper ───────────────────────────────────────────────
function apiCall(port, route, method = 'GET', body = null) {
  return new Promise((resolve, reject) => {
    const payload = body ? JSON.stringify(body) : '';
    const req = http.request({
      hostname : 'localhost',
      port,
      path     : route,
      method,
      headers  : {
        'Content-Type'  : 'application/json',
        'Content-Length': Buffer.byteLength(payload)
      }
    }, res => {
      let raw = '';
      res.on('data', c => raw += c);
      res.on('end', () => {
        try { resolve({ statusCode: res.statusCode, data: JSON.parse(raw) }); }
        catch { resolve({ statusCode: res.statusCode, text: raw }); }
      });
    });
    req.on('error', reject);
    if (payload) req.write(payload);
    req.end();
  });
}

// ── Test Result Tracker ───────────────────────────────────────
let passed = 0, failed = 0;
function pass(label) { console.log(`  ✔ PASS  ${label}`); passed++; }
function fail(label, err) { console.error(`  ✘ FAIL  ${label}\n         ${err.message}`); failed++; }
async function test(label, fn) {
  try { await fn(); pass(label); }
  catch (e) { fail(label, e); }
}

// ─────────────────────────────────────────────────────────────
//  MAIN
// ─────────────────────────────────────────────────────────────
async function run() {
  console.log('\n═══════════════════════════════════════════════════');
  console.log('   OpenSave — LAN Pairing Comprehensive Test Suite');
  console.log('═══════════════════════════════════════════════════\n');

  cleanup();
  setup();

  const daemonScript = path.join(ROOT, 'src/daemon/index.js');
  const envA = { ...process.env, USERPROFILE: homeA, HOME: homeA };
  const envB = { ...process.env, USERPROFILE: homeB, HOME: homeB };

  console.log(`[Boot] Starting Daemon A (Alice) on port ${portA}…`);
  const procA = spawn('node', [daemonScript, '--port', String(portA)], { env: envA });
  console.log(`[Boot] Starting Daemon B (Bob)   on port ${portB}…`);
  const procB = spawn('node', [daemonScript, '--port', String(portB)], { env: envB });

  const logA = fs.createWriteStream(path.join(testRoot, 'alice.log'));
  const logB = fs.createWriteStream(path.join(testRoot, 'bob.log'));
  procA.stdout.pipe(logA); procA.stderr.pipe(logA);
  procB.stdout.pipe(logB); procB.stderr.pipe(logB);

  await sleep(3500);

  let aliceId, bobId, gameId;

  // ── Section 1: Boot Health ──────────────────────────────────
  console.log('\n── Section 1: Daemon Health ─────────────────────────\n');

  await test('Alice daemon responds to /api/status', async () => {
    const r = await apiCall(portA, '/api/status');
    assert.strictEqual(r.statusCode, 200);
    assert.ok(r.data.settings, 'status should include settings');
  });

  await test('Bob daemon responds to /api/status', async () => {
    const r = await apiCall(portB, '/api/status');
    assert.strictEqual(r.statusCode, 200);
  });

  await test('Alice /api/peers returns empty paired list', async () => {
    const r = await apiCall(portA, '/api/peers');
    assert.strictEqual(r.statusCode, 200);
    assert.strictEqual(Object.keys(r.data.paired).length, 0);
  });

  await test('Bob /api/peers returns empty paired list', async () => {
    const r = await apiCall(portB, '/api/peers');
    assert.strictEqual(r.statusCode, 200);
    assert.strictEqual(Object.keys(r.data.paired).length, 0);
  });

  // ── Section 2: Settings & Device Identity ───────────────────
  console.log('\n── Section 2: Device Identity ───────────────────────\n');

  await test('Alice: POST /api/settings sets deviceName and deviceType', async () => {
    const r = await apiCall(portA, '/api/settings', 'POST', {
      deviceName: 'Alice-PC', deviceType: 'desktop'
    });
    assert.strictEqual(r.statusCode, 200);
    assert.strictEqual(r.data.deviceName, 'Alice-PC');
    assert.strictEqual(r.data.deviceType, 'desktop');
  });

  await test('Bob: POST /api/settings sets deviceName and deviceType', async () => {
    const r = await apiCall(portB, '/api/settings', 'POST', {
      deviceName: 'Bob-Deck', deviceType: 'deck'
    });
    assert.strictEqual(r.statusCode, 200);
    assert.strictEqual(r.data.deviceName, 'Bob-Deck');
    assert.strictEqual(r.data.deviceType, 'deck');
  });

  await test('Alice: GET /api/settings returns persisted settings', async () => {
    const r = await apiCall(portA, '/api/settings');
    assert.strictEqual(r.statusCode, 200);
    assert.strictEqual(r.data.deviceName, 'Alice-PC', `Expected Alice-PC, got: ${r.data.deviceName}`);
    aliceId = r.data.nodeId;
    assert.ok(aliceId && aliceId.length > 0, 'nodeId must be non-empty');
  });

  await test('Bob: GET /api/settings returns persisted settings', async () => {
    const r = await apiCall(portB, '/api/settings');
    assert.strictEqual(r.statusCode, 200);
    assert.strictEqual(r.data.deviceName, 'Bob-Deck', `Expected Bob-Deck, got: ${r.data.deviceName}`);
    bobId = r.data.nodeId;
    assert.ok(bobId && bobId.length > 0, 'nodeId must be non-empty');
  });

  await test('Alice and Bob have unique nodeIds', async () => {
    assert.ok(aliceId && bobId, 'both nodeIds must be set');
    assert.notStrictEqual(aliceId, bobId, `nodeIds must differ (got: ${aliceId} vs ${bobId})`);
  });

  // ── Section 3: UDP Discovery ────────────────────────────────
  console.log('\n── Section 3: UDP LAN Discovery ─────────────────────\n');

  await test('UDP port 8385 is available (daemon binds with reuseAddr)', async () => {
    await new Promise((resolve, reject) => {
      const probe = dgram.createSocket({ type: 'udp4', reuseAddr: true });
      probe.bind({ port: DISCOVERY_UDP_PORT, address: '0.0.0.0' }, () => {
        probe.close(); resolve();
      });
      probe.on('error', err => {
        if (err.code === 'EADDRINUSE') resolve();
        else reject(err);
      });
    });
  });

  await test('Alice is broadcasting opensave-ping on the network', async () => {
    // The daemon broadcasts on all active LAN interfaces. On the test machine
    // we can hear our own broadcast because we bind reuseAddr and don't filter by sender.
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('No opensave-ping from Alice within 5s')), 5000);
      const sock = dgram.createSocket({ type: 'udp4', reuseAddr: true });
      sock.bind({ port: DISCOVERY_UDP_PORT, address: '0.0.0.0' }, () => sock.setBroadcast(true));
      sock.on('message', msg => {
        try {
          const d = JSON.parse(msg.toString());
          if (d.type === 'opensave-ping' && d.nodeId === aliceId) {
            clearTimeout(timer); sock.close();
            assert.ok(d.port > 0, 'ping must include valid port');
            assert.strictEqual(d.deviceName, 'Alice-PC');
            resolve();
          }
        } catch {}
      });
      sock.on('error', err => { clearTimeout(timer); sock.close(); reject(err); });
    });
  });

  await test('Bob is broadcasting opensave-ping on the network', async () => {
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('No opensave-ping from Bob within 5s')), 5000);
      const sock = dgram.createSocket({ type: 'udp4', reuseAddr: true });
      sock.bind({ port: DISCOVERY_UDP_PORT, address: '0.0.0.0' }, () => sock.setBroadcast(true));
      sock.on('message', msg => {
        try {
          const d = JSON.parse(msg.toString());
          if (d.type === 'opensave-ping' && d.nodeId === bobId) {
            clearTimeout(timer); sock.close();
            assert.strictEqual(d.deviceName, 'Bob-Deck');
            resolve();
          }
        } catch {}
      });
      sock.on('error', err => { clearTimeout(timer); sock.close(); reject(err); });
    });
  });

  // Note: mutual UDP auto-discovery is skipped on same machine due to the
  // daemon's correct self-IP filter. On real separate LAN devices this works.
  // Manual pairing via direct IP is tested below (Section 4).
  console.log('  [note] Mutual auto-discovery skipped (same-machine self-filter is correct behaviour)');
  console.log('         Pairing via direct IP (the real-world fallback path) is tested in Section 4.\n');

  // ── Section 4: Direct LAN Pairing Flow ─────────────────────
  console.log('\n── Section 4: LAN Pairing Flow (Direct IP) ─────────\n');

  await test('/api/p2p/ping responds correctly on Alice', async () => {
    const r = await apiCall(portA, `/api/p2p/ping?from=${bobId}`);
    assert.strictEqual(r.statusCode, 200);
    assert.strictEqual(r.data.status, 'ok');
    assert.strictEqual(r.data.deviceName, 'Alice-PC');
  });

  await test('/api/p2p/ping responds correctly on Bob', async () => {
    const r = await apiCall(portB, `/api/p2p/ping?from=${aliceId}`);
    assert.strictEqual(r.statusCode, 200);
    assert.strictEqual(r.data.status, 'ok');
    assert.strictEqual(r.data.deviceName, 'Bob-Deck');
  });

  // Step 1: Alice sends a handshake to Bob (direct IP — simulates UI "Add by IP")
  await test('Alice sends handshake to Bob via /api/peers/pair (direct IP)', async () => {
    const r = await apiCall(portA, '/api/peers/pair', 'POST', {
      address: '127.0.0.1',
      port   : portB,
      isWan  : false
    });
    assert.strictEqual(r.statusCode, 200);
    // Returns pending (handshake sent, waiting for approval)
    assert.ok(r.data.status === 'pending' || r.data.success, `Unexpected: ${JSON.stringify(r.data)}`);
  });

  await sleep(500);

  // Step 2: Bob sees a pending request from Alice
  await test('Bob has a pending pairing request from Alice', async () => {
    const r = await apiCall(portB, '/api/peers');
    const requests = r.data.requests || [];
    const req = requests.find(p => p.peerId === aliceId);
    assert.ok(req, `No pending request from Alice. Requests: ${JSON.stringify(requests)}`);
    assert.strictEqual(req.deviceName, 'Alice-PC');
    assert.strictEqual(req.isWan, false, 'Must be a LAN (not WAN) request');
  });

  // Step 3: Bob approves — this adds Alice to Bob's paired list and sends approve-confirm back
  await test('Bob approves Alice pairing request', async () => {
    const r = await apiCall(portB, '/api/peers/approve', 'POST', { peerId: aliceId });
    assert.strictEqual(r.statusCode, 200);
    assert.ok(r.data.success, `Approve did not return success: ${JSON.stringify(r.data)}`);
  });

  // Wait for approve-confirm to travel Alice ← Bob and be processed
  console.log('  [wait] Allowing 3s for approve-confirm round-trip…');
  await sleep(3000);

  // ── Section 5: Post-Pair Online Status ──────────────────────
  console.log('\n── Section 5: Online Status After Pairing ───────────\n');

  // Wait for the 10s ping interval to fire at least once
  console.log('  [wait] Allowing 12s for ping interval to mark peers online…');
  await sleep(12000);

  // Check paired AFTER the ping interval (gives approve-confirm + ping time to settle)
  await test('Alice is now paired with Bob', async () => {
    const r = await apiCall(portA, '/api/peers');
    const paired = r.data.paired || {};
    assert.ok(paired[bobId],
      `Bob (${bobId}) not in Alice's paired list.\nPaired: ${JSON.stringify(Object.keys(paired))}`);
    assert.strictEqual(paired[bobId].name, 'Bob-Deck');
  });

  await test('Bob is now paired with Alice', async () => {
    const r = await apiCall(portB, '/api/peers');
    const paired = r.data.paired || {};
    assert.ok(paired[aliceId],
      `Alice (${aliceId}) not in Bob's paired list.\nPaired: ${JSON.stringify(Object.keys(paired))}`);
    assert.strictEqual(paired[aliceId].name, 'Alice-PC');
  });

  await test('Bob shows as online in Alice\'s paired list', async () => {
    const r = await apiCall(portA, '/api/peers');
    const bob = r.data.paired[bobId];
    assert.ok(bob, 'Bob must be in paired list');
    assert.strictEqual(bob.status, 'online', `Expected online, got: ${bob.status}`);
    assert.strictEqual(bob.name, 'Bob-Deck');
  });

  await test('Alice shows as online in Bob\'s paired list', async () => {
    const r = await apiCall(portB, '/api/peers');
    const alice = r.data.paired[aliceId];
    assert.ok(alice, 'Alice must be in paired list');
    assert.strictEqual(alice.status, 'online', `Expected online, got: ${alice.status}`);
    assert.strictEqual(alice.name, 'Alice-PC');
  });

  await test('Ping returns paired=true for known peer', async () => {
    const r = await apiCall(portB, `/api/p2p/ping?from=${aliceId}`);
    assert.strictEqual(r.statusCode, 200);
    assert.strictEqual(r.data.paired, true, 'paired should be true for a known nodeId');
  });

  await test('Ping includes game state object', async () => {
    const r = await apiCall(portA, `/api/p2p/ping?from=${bobId}`);
    assert.ok(typeof r.data.games === 'object', 'Ping must return games object');
  });

  // ── Section 6: LAN File Sync ────────────────────────────────
  console.log('\n── Section 6: LAN File Sync ─────────────────────────\n');

  await test('Alice tracks "Hollow Knight" save folder', async () => {
    const r = await apiCall(portA, '/api/games', 'POST', {
      name: 'Hollow Knight', savePath: savesA
    });
    assert.strictEqual(r.statusCode, 201);
    gameId = r.data.id;
    assert.ok(gameId, 'game must have an id');
  });

  await test('Bob tracks "Hollow Knight" save folder (different path)', async () => {
    const r = await apiCall(portB, '/api/games', 'POST', {
      name: 'Hollow Knight', savePath: savesB
    });
    assert.strictEqual(r.statusCode, 201);
  });

  // Write save data on Alice's side
  fs.writeFileSync(path.join(savesA, 'player.dat'), 'HK-SAVE-SLOT-1-MAWLEK-DEFEATED');
  fs.writeFileSync(path.join(savesA, 'settings.cfg'), '[options]\nfullscreen=1\nvolume=80');

  console.log('  [wait] Allowing 4.5s for watcher auto-snapshot…');
  await sleep(4500);

  await test('Alice auto-snapshot created by watcher', async () => {
    const r = await apiCall(portA, '/api/games');
    const game = r.data[gameId];
    assert.ok(game, 'game must exist');
    const snaps = game.branches.main.snapshots;
    assert.ok(snaps.length > 0, `Expected >0 snapshots, found: ${snaps.length}`);
  });

  await test('LAN sync from Alice to Bob succeeds (reports peer synced)', async () => {
    const r = await apiCall(portA, `/api/games/${gameId}/sync`, 'POST');
    assert.strictEqual(r.statusCode, 200);
    assert.ok(
      r.data.peersSynced && r.data.peersSynced.length > 0,
      `Must report at least one peer synced. Got: ${JSON.stringify(r.data)}`
    );
  });

  await sleep(2500);

  await test('Bob received player.dat from Alice via LAN', async () => {
    const dest = path.join(savesB, 'player.dat');
    assert.ok(fs.existsSync(dest), `player.dat missing at ${dest}`);
    assert.strictEqual(fs.readFileSync(dest, 'utf8'), 'HK-SAVE-SLOT-1-MAWLEK-DEFEATED');
  });

  await test('Bob received settings.cfg from Alice via LAN', async () => {
    const dest = path.join(savesB, 'settings.cfg');
    assert.ok(fs.existsSync(dest), `settings.cfg missing at ${dest}`);
    assert.ok(fs.readFileSync(dest, 'utf8').includes('fullscreen=1'));
  });

  await test('Delta re-sync: Alice modifies player.dat, Bob gets the update', async () => {
    fs.writeFileSync(path.join(savesA, 'player.dat'), 'HK-SAVE-SLOT-1-HORNET-DEFEATED');
    await sleep(4500); // let watcher snapshot
    const r = await apiCall(portA, `/api/games/${gameId}/sync`, 'POST');
    assert.strictEqual(r.statusCode, 200);
    await sleep(2000);
    assert.strictEqual(
      fs.readFileSync(path.join(savesB, 'player.dat'), 'utf8'),
      'HK-SAVE-SLOT-1-HORNET-DEFEATED'
    );
  });

  await test('New file added by Alice syncs to Bob', async () => {
    fs.writeFileSync(path.join(savesA, 'slot2.dat'), 'HK-SLOT-2-HORNET-BOSS');
    await sleep(4500);
    const r = await apiCall(portA, `/api/games/${gameId}/sync`, 'POST');
    assert.strictEqual(r.statusCode, 200);
    await sleep(2000);
    assert.ok(fs.existsSync(path.join(savesB, 'slot2.dat')), 'slot2.dat should be synced');
    assert.strictEqual(
      fs.readFileSync(path.join(savesB, 'slot2.dat'), 'utf8'),
      'HK-SLOT-2-HORNET-BOSS'
    );
  });

  // ── Section 7: Security Guardrails ──────────────────────────
  console.log('\n── Section 7: Security Guardrails ───────────────────\n');

  await test('Localhost can always access manifest endpoint (not blocked)', async () => {
    const r = await apiCall(portA, `/api/p2p/manifest/${gameId}`);
    assert.notStrictEqual(r.statusCode, 401, 'Localhost must never be blocked by requirePairedPeer');
  });

  await test('POST /api/peers/unpair removes Bob from Alice paired list', async () => {
    const r = await apiCall(portA, '/api/peers/unpair', 'POST', { peerId: bobId });
    assert.strictEqual(r.statusCode, 200);
    assert.strictEqual(r.data.success, true);
    await sleep(1000);
    const peersRes = await apiCall(portA, '/api/peers');
    assert.ok(!peersRes.data.paired[bobId], 'Bob must be gone from Alice\'s paired list');
  });

  await test('Alice removed from Bob\'s paired list after unpair-notify', async () => {
    await sleep(2000); // give unpair-notify time to arrive
    const r = await apiCall(portB, '/api/peers');
    assert.ok(!r.data.paired[aliceId], 'Alice must be removed from Bob after unpair-notify');
  });

  await test('DELETE /api/peers/:peerId also works for unpairing (re-pair first)', async () => {
    // Quick re-pair to test DELETE
    await apiCall(portA, '/api/peers/pair', 'POST', { address: '127.0.0.1', port: portB, isWan: false });
    await sleep(500);
    const peersB = await apiCall(portB, '/api/peers');
    const req = (peersB.data.requests || []).find(p => p.peerId === aliceId);
    if (req) await apiCall(portB, '/api/peers/approve', 'POST', { peerId: aliceId });
    await sleep(2000);
    // Now delete via DELETE
    const r = await apiCall(portA, `/api/peers/${bobId}`, 'DELETE');
    assert.strictEqual(r.statusCode, 200);
    await sleep(1000);
    const finalPeers = await apiCall(portA, '/api/peers');
    assert.ok(!finalPeers.data.paired[bobId], 'Bob must be gone after DELETE unpair');
  });

  // ── Section 8: Re-pairing ───────────────────────────────────
  console.log('\n── Section 8: Re-Pairing After Unpair ───────────────\n');

  await test('Full re-pair cycle completes successfully', async () => {
    // Alice initiates pair to Bob
    await apiCall(portA, '/api/peers/pair', 'POST', {
      address: '127.0.0.1', port: portB, isWan: false
    });
    await sleep(500);

    // Bob approves
    const peersB = await apiCall(portB, '/api/peers');
    const req = (peersB.data.requests || []).find(p => p.peerId === aliceId);
    assert.ok(req, 'Bob should have a pairing request from Alice');
    const approveRes = await apiCall(portB, '/api/peers/approve', 'POST', { peerId: aliceId });
    assert.strictEqual(approveRes.statusCode, 200);

    await sleep(2000);

    // Both should be paired
    const alicePeers = await apiCall(portA, '/api/peers');
    const bobPeers   = await apiCall(portB, '/api/peers');
    assert.ok(alicePeers.data.paired[bobId],   'Alice should be re-paired with Bob');
    assert.ok(bobPeers.data.paired[aliceId],   'Bob should be re-paired with Alice');

    // Run initial sync from both sides to establish lastSynced baseline while files are identical
    const syncResA = await apiCall(portA, `/api/games/${gameId}/sync`, 'POST');
    assert.strictEqual(syncResA.statusCode, 200);
    const syncResB = await apiCall(portB, `/api/games/${gameId}/sync`, 'POST');
    assert.strictEqual(syncResB.statusCode, 200);
  });

  await test('LAN sync works again after re-pair', async () => {
    fs.writeFileSync(path.join(savesA, 'player.dat'), 'HK-SAVE-HOLLOWNEST-COMPLETE');
    // Allow enough time for the gameplay guard + watcher settle timer to create a snapshot
    await sleep(6000);
    const r = await apiCall(portA, `/api/games/${gameId}/sync`, 'POST');
    assert.strictEqual(r.statusCode, 200);
    await sleep(3000);
    assert.strictEqual(
      fs.readFileSync(path.join(savesB, 'player.dat'), 'utf8'),
      'HK-SAVE-HOLLOWNEST-COMPLETE'
    );
  });

  // ── Summary ──────────────────────────────────────────────────
  console.log('\n═══════════════════════════════════════════════════');
  const total = passed + failed;
  console.log(`  Results: ${passed}/${total} passed, ${failed} failed`);

  if (failed > 0) {
    console.log(`\n  ❌ SOME TESTS FAILED.`);
    console.log(`     alice.log → ${path.join(testRoot, 'alice.log')}`);
    console.log(`     bob.log   → ${path.join(testRoot, 'bob.log')}`);
  } else {
    console.log('\n  ✅ ALL LAN PAIRING TESTS PASSED!');
  }
  console.log('═══════════════════════════════════════════════════\n');

  procA.kill('SIGTERM');
  procB.kill('SIGTERM');

  if (failed === 0) cleanup();

  process.exit(failed > 0 ? 1 : 0);
}

run().catch(err => {
  console.error('Fatal test runner error:', err);
  process.exit(1);
});
