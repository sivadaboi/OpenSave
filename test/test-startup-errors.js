import assert from 'assert';
import fs from 'fs';
import path from 'path';
import os from 'os';
import { logErrorToFile } from '../src/errorHandler.js';

console.log('====================================================');
console.log('Running Startup Error Diagnostics Unit Tests...');
console.log('====================================================');

const HOME_DIR = path.join(os.homedir(), '.opensave');
const ERROR_LOG_FILE = path.join(HOME_DIR, 'startup-error.log');

try {
  // Setup: Backup existing error log if any
  let originalContent = null;
  if (fs.existsSync(ERROR_LOG_FILE)) {
    originalContent = fs.readFileSync(ERROR_LOG_FILE, 'utf8');
    fs.unlinkSync(ERROR_LOG_FILE);
  }

  // Test Case 1: Logging Error object
  const testError = new Error('Test Error: Database write failed');
  logErrorToFile(testError);

  assert.strictEqual(fs.existsSync(ERROR_LOG_FILE), true, 'Error log file should be created');
  let logContent = fs.readFileSync(ERROR_LOG_FILE, 'utf8');
  assert.ok(logContent.includes('Test Error: Database write failed'), 'Log content should contain error message');
  assert.ok(logContent.includes('test-startup-errors.js'), 'Log content should contain stack trace');
  console.log('✔ PASS: Successfully logged Error object with stack trace to startup-error.log');

  // Clear log for next test
  fs.unlinkSync(ERROR_LOG_FILE);

  // Test Case 2: Logging string message
  logErrorToFile('Test String Error Message');
  logContent = fs.readFileSync(ERROR_LOG_FILE, 'utf8');
  assert.ok(logContent.includes('Test String Error Message'), 'Log content should contain custom string error message');
  console.log('✔ PASS: Successfully logged string error message to startup-error.log');

  // Clear log for next test
  fs.unlinkSync(ERROR_LOG_FILE);

  // Test Case 3: Logging object error
  logErrorToFile({ customCode: 'EADDRINUSE', port: 8383 });
  logContent = fs.readFileSync(ERROR_LOG_FILE, 'utf8');
  assert.ok(logContent.includes('EADDRINUSE'), 'Log content should serialize object errors');
  assert.ok(logContent.includes('8383'), 'Log content should serialize object error properties');
  console.log('✔ PASS: Successfully logged serialized object error to startup-error.log');

  // Clean up and restore original content
  if (fs.existsSync(ERROR_LOG_FILE)) {
    fs.unlinkSync(ERROR_LOG_FILE);
  }
  if (originalContent !== null) {
    fs.writeFileSync(ERROR_LOG_FILE, originalContent, 'utf8');
  }

  console.log('\n✅ ALL STARTUP ERROR DIAGNOSTIC TESTS PASSED!');
  process.exit(0);
} catch (err) {
  console.error('\n❌ STARTUP ERROR DIAGNOSTIC TESTS FAILED:', err.stack || err.message);
  process.exit(1);
}
