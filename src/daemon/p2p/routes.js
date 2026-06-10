import fs from 'fs';
import path from 'path';
import db from '../db.js';
import { getFolderManifest, readBlocks, translatePathToLocal, isSafePath, resolveLocalSaveFilePath } from '../delta.js';
import { getLatestSnapshot } from '../snapshot.js';
import watcherEngine from '../watcher.js';

// Connection authorization middleware for local P2P endpoints
function requirePairedPeer(req, res, next) {
  let clientIp = (req.ip || '').replace('::ffff:', '');
  if (clientIp === '::1' || clientIp === '127.0.0.1') clientIp = 'localhost';
  
  // Always allow local dashboard/CLI interface requests
  const isLocal = clientIp === 'localhost' || clientIp === '127.0.0.1' || clientIp === '::1';
  if (isLocal) {
    return next();
  }

  // Check if the client IP matches one of our active paired peers
  const peers = db.getPeers();
  const matchedPeer = Object.values(peers).find(p => p.address === clientIp || (clientIp === 'localhost' && p.address === '127.0.0.1'));
  
  if (!matchedPeer) {
    console.warn(`[P2P Guard] Blocked unauthorized request from unpaired LAN IP: ${clientIp}`);
    return res.status(401).json({ error: 'Unauthorized: Requesting peer is not paired.' });
  }

  // Mark peer as online since we received a valid request from them (with throttle to prevent redundant writes)
  const lastSeenLimit = 10000; // 10 seconds
  const lastSeenTime = typeof matchedPeer.lastSeen === 'string' ? new Date(matchedPeer.lastSeen).getTime() : (matchedPeer.lastSeen || 0);
  const shouldUpdate = matchedPeer.status !== 'online' || matchedPeer.address !== clientIp || (Date.now() - lastSeenTime > lastSeenLimit);
  if (shouldUpdate) {
    db.updatePeer(matchedPeer.id, { 
      status: 'online', 
      address: clientIp, 
      lastSeen: Date.now() 
    });
  }
  
  next();
}

export function registerExpressRoutes(app, p2pEngine) {
  app.get('/api/p2p/ping', (req, res) => {
    const { from } = req.query;
    const paired = from ? !!db.getPeers()[from] : true;
    res.status(200).json({
      status: 'ok',
      paired,
      deviceName: db.getSettings().deviceName,
      deviceType: db.getSettings().deviceType || 'desktop',
      games: p2pEngine.getLocalGamesState()
    });
  });

  app.post('/api/p2p/approve-confirm', (req, res) => {
    const { peerId, deviceName, deviceType, port } = req.body;
    let clientIp = (req.ip || '').replace('::ffff:', '');
    if (clientIp === '::1' || clientIp === '127.0.0.1') clientIp = 'localhost';

    // ── Validate this confirm matches a handshake we initiated ────────────
    // Check 1: sentPairingRequests — set when we called /api/peers/pair
    const sentRequests = p2pEngine.sentPairingRequests || {};
    const GRACE_MS = 120000; // 2-minute window
    const now = Date.now();

    const keyByIp     = `${clientIp}:${port}`;
    const sentByPeer  = sentRequests[peerId]  && (now - sentRequests[peerId])  < GRACE_MS;
    const sentByIp    = sentRequests[clientIp] && (now - sentRequests[clientIp]) < GRACE_MS;
    const sentByKey   = sentRequests[keyByIp]  && (now - sentRequests[keyByIp])  < GRACE_MS;

    // localhost alias cross-checks (same machine testing / loopback)
    const sentByLocalAlias =
      (clientIp === 'localhost' && (
        (sentRequests['127.0.0.1'] && (now - sentRequests['127.0.0.1']) < GRACE_MS) ||
        (sentRequests[`127.0.0.1:${port}`] && (now - sentRequests[`127.0.0.1:${port}`]) < GRACE_MS)
      )) ||
      (clientIp === '127.0.0.1' && (
        (sentRequests['localhost'] && (now - sentRequests['localhost']) < GRACE_MS) ||
        (sentRequests[`localhost:${port}`] && (now - sentRequests[`localhost:${port}`]) < GRACE_MS)
      ));

    // Check 2: pairingRequests — if we have an active handshake from this peer
    // (covers the case where they sent us a handshake AND we approved, then they
    //  send approve-confirm back — both sides initiated simultaneously or they
    //  replied to our handshake by approving and confirming back)
    const hasPairingRecord = !!(p2pEngine.pairingRequests && p2pEngine.pairingRequests[peerId]);

    // Check 3: already paired (idempotent re-confirm after reconnect)
    const alreadyPaired = !!db.getPeers()[peerId];

    const isValid = sentByPeer || sentByIp || sentByKey || sentByLocalAlias || hasPairingRecord || alreadyPaired;

    if (!isValid) {
      console.warn(`[P2P Guard] Blocked unsolicited /approve-confirm from IP: ${clientIp}, peerId: ${peerId}`);
      return res.status(400).json({ error: 'Pairing confirmation rejected: no matching handshake initiated.' });
    }

    // Clean up consumed sentPairingRequests entries to prevent stale accumulation
    delete sentRequests[peerId];
    delete sentRequests[clientIp];
    delete sentRequests[keyByIp];

    // Use the real client IP as peer address (not 'localhost' — that only applies on same machine)
    const peerAddress = (clientIp === 'localhost' || clientIp === '127.0.0.1')
      ? clientIp
      : clientIp;

    db.addPeer(peerId, deviceName, peerAddress, port, deviceType || 'desktop');
    db.updatePeer(peerId, { status: 'online', lastSeen: Date.now() });

    // Clean up the pairing request record if it exists
    if (p2pEngine.pairingRequests && p2pEngine.pairingRequests[peerId]) {
      delete p2pEngine.pairingRequests[peerId];
    }

    console.log(`[P2P] Pairing confirmed with ${deviceName} (${peerId}) from ${clientIp}:${port}`);

    if (typeof p2pEngine.onPeerUpdate === 'function') {
      p2pEngine.onPeerUpdate();
    }

    res.status(200).json({ success: true, message: 'Pairing confirmed.' });
  });

  app.post('/api/p2p/handshake', (req, res) => {
    const { peerId, deviceName, deviceType, port } = req.body;
    let clientIp = (req.ip || '').replace('::ffff:', '');
    if (clientIp === '::1' || clientIp === '127.0.0.1') clientIp = 'localhost';

    p2pEngine.pairingRequests[peerId] = {
      peerId,
      deviceName,
      deviceType: deviceType || 'desktop',
      address: clientIp,
      port,
      isWan: false
    };

    // Register a sentPairingRequests entry so that when we approve and the
    // remote peer sends approve-confirm back, the guard accepts it.
    // This covers the case where the remote initiated the handshake directly
    // (without going through /api/peers/pair on our side).
    if (!p2pEngine.sentPairingRequests) p2pEngine.sentPairingRequests = {};
    p2pEngine.sentPairingRequests[peerId]  = Date.now();
    p2pEngine.sentPairingRequests[clientIp] = Date.now();
    p2pEngine.sentPairingRequests[`${clientIp}:${port}`] = Date.now();

    if (typeof p2pEngine.onPeerUpdate === 'function') {
      p2pEngine.onPeerUpdate();
    }

    res.status(200).json({ status: 'pending', message: 'Pairing request received. Waiting for host approval.' });
  });

  app.post('/api/p2p/unpair', requirePairedPeer, (req, res) => {
    const { peerId } = req.body;
    db.removePeer(peerId);
    if (typeof p2pEngine.onPeerUpdate === 'function') {
      p2pEngine.onPeerUpdate();
    }
    res.status(200).json({ success: true, message: 'Unpaired successfully.' });
  });

  app.get('/api/p2p/manifest/:gameId', requirePairedPeer, (req, res) => {
    const { gameId } = req.params;
    let game = db.getGame(gameId);
    if (!game) {
      const { name, savePath } = req.query;
      if (name && savePath) {
        try {
          const localSavePath = translatePathToLocal(savePath);
          console.log(`[P2P] Auto-tracking game "${name}" at "${localSavePath}" (original: "${savePath}") requested by peer.`);
          if (!fs.existsSync(localSavePath)) {
            fs.mkdirSync(localSavePath, { recursive: true });
          }
          game = db.addGame(name, localSavePath);
          watcherEngine.watchGame(game);
        } catch (err) {
          return res.status(400).json({ error: `Auto-track failed: ${err.message}` });
        }
      } else {
        return res.status(404).json({ error: 'Game not found.' });
      }
    }
    try {
      const activeBranchObj = game.branches[game.activeBranch];
      res.status(200).json({
        gameId,
        activeBranch: game.activeBranch,
        latestSnapshot: getLatestSnapshot(gameId),
        manifest: getFolderManifest(game.savePath),
        history: activeBranchObj ? activeBranchObj.snapshots.map(s => s.id) : []
      });
    } catch (err) {
      res.status(500).json({ error: err.message });
    }
  });

  app.post('/api/p2p/blocks/:gameId', requirePairedPeer, (req, res) => {
    const { gameId } = req.params;
    const { relPath, blockIndices, blockSize } = req.body;
    const game = db.getGame(gameId);
    if (!game) return res.status(404).json({ error: 'Game not found.' });

    // Security: Validate that the requested path resides inside the game's save folder
    if (!isSafePath(game.savePath, relPath)) {
      console.warn(`[P2P Guard] Path traversal attempt blocked on game ${gameId}: ${relPath}`);
      return res.status(403).json({ error: 'Access denied: path traversal attempt detected.' });
    }

    try {
      const fullPath = resolveLocalSaveFilePath(game.savePath, relPath);
      res.status(200).json({ relPath, blocks: readBlocks(fullPath, blockIndices, blockSize) });
    } catch (err) {
      res.status(500).json({ error: err.message });
    }
  });

  app.get('/api/p2p/snapshot/:gameId/:snapshotId', requirePairedPeer, (req, res) => {
    const { gameId, snapshotId } = req.params;
    const game = db.getGame(gameId);
    if (!game) return res.status(404).json({ error: 'Game not found.' });

    let snapshot = null;
    for (const b in game.branches) {
      const snap = game.branches[b].snapshots.find(s => s.id === snapshotId);
      if (snap) {
        snapshot = snap;
        break;
      }
    }

    if (!snapshot || !fs.existsSync(snapshot.zipPath)) {
      return res.status(404).json({ error: 'Snapshot ZIP file not found.' });
    }
    res.download(snapshot.zipPath);
  });

  // Peer requests deletion of a specific file (for deletion sync)
  app.post('/api/p2p/delete-file/:gameId', requirePairedPeer, (req, res) => {
    const { gameId } = req.params;
    const { relPath } = req.body;
    if (!relPath) return res.status(400).json({ error: 'relPath is required.' });
    
    const game = db.getGame(gameId);
    if (!game) return res.status(404).json({ error: 'Game not found.' });

    try {
      // Security: ensure relPath doesn't escape the save directory
      if (!isSafePath(game.savePath, relPath)) {
        return res.status(403).json({ error: 'Path traversal denied.' });
      }

      const resolvedPath = path.resolve(resolveLocalSaveFilePath(game.savePath, relPath));
      if (fs.existsSync(resolvedPath)) {
        const stat = fs.statSync(resolvedPath);
        if (stat.isDirectory()) {
          fs.rmSync(resolvedPath, { recursive: true, force: true });
        } else {
          fs.unlinkSync(resolvedPath);
        }
        console.log(`[P2P] Deleted file at peer request: ${relPath} (game: ${gameId})`);
      }
      res.status(200).json({ success: true });
    } catch (err) {
      res.status(500).json({ error: err.message });
    }
  });

  app.post('/api/p2p/sync-event/:gameId', requirePairedPeer, (req, res) => {
    const { gameId } = req.params;
    const { eventType, data } = req.body;

    if (eventType === 'sync-start') {
      if (typeof p2pEngine.onSyncStart === 'function') {
        p2pEngine.onSyncStart(gameId, data);
      }
    } else if (eventType === 'sync-progress') {
      if (typeof p2pEngine.onSyncProgress === 'function') {
        p2pEngine.onSyncProgress(gameId, data);
      }
    } else if (eventType === 'sync-complete') {
      if (typeof p2pEngine.onSyncComplete === 'function') {
        p2pEngine.onSyncComplete(gameId, data);
      }
    } else if (eventType === 'sync-error') {
      if (typeof p2pEngine.onSyncError === 'function') {
        p2pEngine.onSyncError(gameId, data);
      }
    }

    res.status(200).json({ success: true });
  });
}

