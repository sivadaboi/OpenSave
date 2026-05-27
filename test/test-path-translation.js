import assert from 'assert';
import path from 'path';
import os from 'os';
import { translatePathToLocal } from '../src/daemon/delta.js';

console.log('====================================================');
console.log('Running Bidirectional Path Translation Unit Tests...');
console.log('====================================================');

try {
  // Test case 1: Windows to Local
  const winPath = 'C:\\Users\\John\\Documents\\My Games\\Dark Souls III';
  const expectedLocalFromWin = path.join(os.homedir(), 'Documents/My Games/Dark Souls III');
  const result1 = translatePathToLocal(winPath);
  assert.strictEqual(result1, expectedLocalFromWin, `Windows to Local translation failed! Got: ${result1}, Expected: ${expectedLocalFromWin}`);
  console.log('✔ PASS: Windows path translated to local home subpath correctly.');

  // Test case 2: Linux to Local
  const linuxPath = '/home/deck/Documents/My Games/Dark Souls III';
  const expectedLocalFromLinux = path.join(os.homedir(), 'Documents/My Games/Dark Souls III');
  const result2 = translatePathToLocal(linuxPath);
  assert.strictEqual(result2, expectedLocalFromLinux, `Linux to Local translation failed! Got: ${result2}, Expected: ${expectedLocalFromLinux}`);
  console.log('✔ PASS: Linux path translated to local home subpath correctly.');

  // Test case 3: Unrelated paths are unchanged
  const systemPath = 'C:\\Windows\\System32';
  const result3 = translatePathToLocal(systemPath);
  assert.strictEqual(result3, path.normalize(systemPath), `System path should remain unchanged! Got: ${result3}`);
  console.log('✔ PASS: Unrelated path remained unchanged.');

  console.log('\n✅ ALL PATH TRANSLATION TESTS PASSED!');
  process.exit(0);
} catch (err) {
  console.error('\n❌ PATH TRANSLATION TESTS FAILED:', err.message);
  process.exit(1);
}
