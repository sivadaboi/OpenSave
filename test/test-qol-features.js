import assert from 'assert';
import path from 'path';
import os from 'os';
import fs from 'fs';
import db from '../src/daemon/db.js';
import { translatePathToLocal, diffManifests, getBlockSizeForFile, getFileBlocks } from '../src/daemon/delta.js';

console.log('====================================================');
console.log('Running Premium QoL Features Unit Tests...');
console.log('====================================================');

// Pin Codec implementation to test (matching app.js exactly)
function encodePin(ip, port) {
  if (!ip) return null;
  const parts = ip.split('.').map(Number);
  if (parts.length !== 4) return null;
  const [a, b, c, d] = parts;

  let classType = 3;
  let ipData = 0;

  if (a === 192 && b === 168) {
    classType = 0;
    ipData = (c << 8) | d;
  } else if (a === 10) {
    classType = 1;
    ipData = (b << 16) | (c << 8) | d;
  } else if (a === 172 && b >= 16 && b <= 31) {
    classType = 2;
    ipData = ((b - 16) << 16) | (c << 8) | d;
  } else {
    return null; // unsupported for PIN
  }

  let portOffset = port - 8380;
  if (portOffset < 0 || portOffset > 31) {
    portOffset = 3; // default offset for 8383
  }

  const val = (classType * 536870912) + (ipData * 32) + portOffset;
  const pinCode = val.toString(36).toUpperCase().padStart(6, '0');
  return `SS-LAN-${pinCode}`;
}

function decodePin(pin) {
  if (!pin) return null;
  const cleaned = pin.trim().toUpperCase();
  if (!cleaned.startsWith('SS-LAN-')) return null;
  const pinCode = cleaned.replace(/^SS-LAN-/, '');
  if (!/^[A-Z0-9]{6}$/.test(pinCode)) return null;

  const val = parseInt(pinCode.toLowerCase(), 36);
  if (isNaN(val)) return null;

  const classType = Math.floor(val / 536870912) & 0x03;
  const ipData = Math.floor((val % 536870912) / 32) & 0xFFFFFF;
  const portOffset = val % 32;

  let ip = null;
  if (classType === 0) {
    const c = (ipData >> 8) & 0xFF;
    const d = ipData & 0xFF;
    ip = `192.168.${c}.${d}`;
  } else if (classType === 1) {
    const b = (ipData >> 16) & 0xFF;
    const c = (ipData >> 8) & 0xFF;
    const d = ipData & 0xFF;
    ip = `10.${b}.${c}.${d}`;
  } else if (classType === 2) {
    const b = ((ipData >> 16) & 0xFF) + 16;
    const c = (ipData >> 8) & 0xFF;
    const d = ipData & 0xFF;
    ip = `172.${b}.${c}.${d}`;
  } else {
    return null;
  }

  const port = 8380 + portOffset;
  return { ip, port };
}

try {
  // Use temporary isolated database for settings tests
  const testDbFile = path.join(os.tmpdir(), `savesync-qol-test-db-${Date.now()}.json`);
  const testHomeDir = path.join(os.tmpdir(), `savesync-qol-test-home-${Date.now()}`);
  db.setDbFileForTesting(testDbFile, testHomeDir);

  // 1. Test Base36 PIN Codec
  console.log('Testing Base36 PIN Codec...');
  const testCases = [
    { ip: '192.168.1.100', port: 8383 },
    { ip: '192.168.0.1', port: 8380 },
    { ip: '10.0.0.5', port: 8385 },
    { ip: '172.16.42.99', port: 8400 },
    { ip: '172.31.255.254', port: 8411 }
  ];

  for (const tc of testCases) {
    const pin = encodePin(tc.ip, tc.port);
    assert.ok(pin.startsWith('SS-LAN-'), `PIN should start with SS-LAN-: ${pin}`);
    const decoded = decodePin(pin);
    assert.deepStrictEqual(decoded, tc, `Decoded PIN mismatch! Got ${JSON.stringify(decoded)}, expected ${JSON.stringify(tc)}`);
  }
  console.log('✔ PASS: Base36 PIN Codec encodes and decodes properly.');

  // 2. Test Custom Path Translations
  console.log('Testing Custom Path Translations...');
  
  // Set up mock path translation rules in DB
  const mockRules = [
    { fromPattern: 'D:\\Games\\Saves', toPattern: '/home/deck/Games/Saves' },
    { fromPattern: 'C:\\Users\\Siva\\AppData\\Local\\Game', toPattern: '/home/deck/.config/Game' }
  ];
  db.updateSettings({ pathTranslations: mockRules });

  // Test Case A: translate from Windows rule prefix to Linux prefix
  const winSavePath = 'D:\\Games\\Saves\\RPG_Game\\save1.sav';
  const expectedLinuxPath = path.join('/home/deck/Games/Saves', 'RPG_Game\\save1.sav'); 
  const resultA = translatePathToLocal(winSavePath);
  assert.strictEqual(resultA, expectedLinuxPath, `Path translation failed! Got: ${resultA}, expected: ${expectedLinuxPath}`);

  // Test Case B: translate from Linux rule prefix to Windows prefix (bidirectional)
  const linuxSavePath = '/home/deck/Games/Saves/RPG_Game/save1.sav';
  const expectedWinPath = path.join('D:\\Games\\Saves', 'RPG_Game/save1.sav');
  const resultB = translatePathToLocal(linuxSavePath);
  assert.strictEqual(resultB, expectedWinPath, `Bidirectional path translation failed! Got: ${resultB}, expected: ${expectedWinPath}`);

  // Test Case C: translate to Local for other directories still falls back to default Users/Home replacement
  const defaultWinPath = 'C:\\Users\\GuestUser\\Documents\\Game\\save.dat';
  const expectedHomeSub = path.join(os.homedir(), 'Documents\\Game\\save.dat');
  const resultC = translatePathToLocal(defaultWinPath);
  assert.strictEqual(resultC, expectedHomeSub, `Default fallback path translation failed! Got: ${resultC}, expected: ${expectedHomeSub}`);

  console.log('✔ PASS: Bidirectional Custom Path Translation resolved rules correctly.');

  // 3. Test Conflict Diff Generator
  console.log('Testing Conflict Diff Generator...');
  const localManifest = {
    files: {
      'save1.dat': { size: 100, mtime: 1700000000, hash: 'h1' },
      'save2.dat': { size: 200, mtime: 1700000000, hash: 'h2-local' },
      'local-only.dat': { size: 50, mtime: 1700000000, hash: 'hl' }
    }
  };
  const remoteManifest = {
    files: {
      'save1.dat': { size: 100, mtime: 1700000000, hash: 'h1' },
      'save2.dat': { size: 220, mtime: 1700000000, hash: 'h2-remote' },
      'remote-only.dat': { size: 60, mtime: 1700000000, hash: 'hr' }
    }
  };

  const diff = diffManifests(localManifest, remoteManifest);
  
  // Assert modified includes save2.dat
  assert.ok(diff.modified['save2.dat'] !== undefined, 'save2.dat should be marked as modified');
  // Assert added includes remote-only.dat
  assert.ok(diff.added.includes('remote-only.dat'), 'remote-only.dat should be marked as added');
  // Assert deleted includes local-only.dat
  assert.ok(diff.deleted.includes('local-only.dat'), 'local-only.dat should be marked as deleted');

  console.log('✔ PASS: Conflict Diff identified added, modified, and deleted files.');

  // 4. Test Dynamic Block Sizing
  console.log('Testing Dynamic Block Sizing...');
  assert.strictEqual(getBlockSizeForFile(10 * 1024 * 1024), 64 * 1024, '10MB should use 64KB blocks');
  assert.strictEqual(getBlockSizeForFile(25 * 1024 * 1024), 512 * 1024, '25MB should use 512KB blocks');
  assert.strictEqual(getBlockSizeForFile(120 * 1024 * 1024), 2 * 1024 * 1024, '120MB should use 2MB blocks');

  const tempTestFile = path.join(os.tmpdir(), `savesync-blocksize-test-${Date.now()}.dat`);
  const testData = Buffer.alloc(100 * 1024);
  fs.writeFileSync(tempTestFile, testData);

  const blocks64 = getFileBlocks(tempTestFile, testData.length, 64 * 1024);
  assert.strictEqual(blocks64.blocks.length, 2, 'Should produce 2 blocks for 64KB block size');
  assert.strictEqual(blocks64.blocks[0].length, 64 * 1024, 'First block should be 64KB');
  assert.strictEqual(blocks64.blocks[1].length, 36 * 1024, 'Second block should be 36KB');

  const blocks512 = getFileBlocks(tempTestFile, testData.length, 512 * 1024);
  assert.strictEqual(blocks512.blocks.length, 1, 'Should produce 1 block for 512KB block size');
  assert.strictEqual(blocks512.blocks[0].length, 100 * 1024, 'Single block should be 100KB');

  fs.unlinkSync(tempTestFile);
  console.log('✔ PASS: Dynamic block size resolved classifications and partitioned blocks correctly.');

  // Clean up temporary database files
  try {
    if (fs.existsSync(testDbFile)) fs.unlinkSync(testDbFile);
    if (fs.existsSync(testHomeDir)) fs.rmSync(testHomeDir, { recursive: true, force: true });
  } catch (e) {}

  console.log('\n✅ ALL PREMIUM QOL TESTS PASSED!');
  process.exit(0);
} catch (err) {
  console.error('\n❌ PREMIUM QOL TESTS FAILED:', err.stack);
  process.exit(1);
}
