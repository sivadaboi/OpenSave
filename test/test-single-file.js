import assert from 'assert';
import fs from 'fs';
import path from 'path';
import os from 'os';
import db from '../src/daemon/db.js';
import { getFolderManifest, patchFile } from '../src/daemon/delta.js';
import { createSnapshot, restoreSnapshot } from '../src/daemon/snapshot.js';

console.log('====================================================');
console.log('Running Single-File Save Configuration Unit Tests...');
console.log('====================================================');

const tempDir = path.join(os.tmpdir(), `opensave-test-single-${Date.now()}`);
fs.mkdirSync(tempDir, { recursive: true });

const testDbPath = path.join(tempDir, 'opensave-test-db.json');
const testHomeDir = path.join(tempDir, 'home');
fs.mkdirSync(testHomeDir, { recursive: true });

// Setup test sandbox database
db.setDbFileForTesting(testDbPath, testHomeDir);

const singleFilePath = path.join(tempDir, 'test-game-save.sav');
fs.writeFileSync(singleFilePath, 'original save file data content that is a bit longer than 0 bytes');

try {
  // Test 1: Get manifest of single file
  const manifest = getFolderManifest(singleFilePath);
  assert.ok(manifest.files['test-game-save.sav'], 'Manifest should contain the filename as a key');
  assert.strictEqual(manifest.files['test-game-save.sav'].size, 65, 'Manifest size should be correct');
  assert.ok(manifest.files['test-game-save.sav'].hash, 'Manifest should calculate integrity hash');
  assert.ok(manifest.files['test-game-save.sav'].blocks.length > 0, 'Manifest should split single file into blocks');
  console.log('✔ PASS: Manifest generation on a single file path is successful.');

  // Test 2: Add game and create snapshot
  db.addGame('Test Retro Emulator Game', singleFilePath);
  const game = db.getGame('test-retro-emulator-game');
  assert.strictEqual(game.savePath, singleFilePath, 'Save path should be the single file path');

  const snapshot = createSnapshot('test-retro-emulator-game', 'Initial single-file backup', false);
  assert.ok(fs.existsSync(snapshot.zipPath), 'Snapshot zip archive should be created');
  console.log('✔ PASS: createSnapshot on single-file configuration is successful.');

  // Test 3: Modify the file and restore from snapshot
  fs.writeFileSync(singleFilePath, 'completely new modified save file data content that is different');
  const restored = restoreSnapshot('test-retro-emulator-game', snapshot.id);
  assert.strictEqual(restored.id, snapshot.id, 'Restored snapshot ID should match');

  const contentAfterRestore = fs.readFileSync(singleFilePath, 'utf8');
  assert.strictEqual(contentAfterRestore, 'original save file data content that is a bit longer than 0 bytes', 'Restore should overwrite and reset single file content');
  console.log('✔ PASS: restoreSnapshot on single-file configuration is successful.');

  // Test 4: Patching a single file
  const localManifest = getFolderManifest(singleFilePath);
  const remoteFileMeta = {
    size: 26,
    hash: '69f5ce99f8bb37ddfaf799beaeb8e902c73ba216e7cd41d5c692207780088413',
    blocks: [
      {
        index: 0,
        length: 26,
        hash: '69f5ce99f8bb37ddfaf799beaeb8e902c73ba216e7cd41d5c692207780088413'
      }
    ]
  };
  const blockChunks = [
    {
      index: 0,
      length: 26,
      data: Buffer.from('patched save file content!').toString('base64')
    }
  ];

  patchFile(singleFilePath, blockChunks, remoteFileMeta);
  const patchedContent = fs.readFileSync(singleFilePath, 'utf8');
  assert.strictEqual(patchedContent, 'patched save file content!', 'Patching should correctly write file content');
  console.log('✔ PASS: patchFile on single-file configuration is successful.');

  console.log('\n✅ ALL SINGLE-FILE TESTS PASSED!');
  
  // Cleanup test files
  fs.rmSync(tempDir, { recursive: true, force: true });
  process.exit(0);
} catch (err) {
  console.error('\n❌ SINGLE-FILE TESTS FAILED:', err.message);
  try {
    fs.rmSync(tempDir, { recursive: true, force: true });
  } catch (e) {}
  process.exit(1);
}
