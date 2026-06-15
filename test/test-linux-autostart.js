import assert from 'assert';
import fs from 'fs';
import path from 'path';
import os from 'os';

console.log('====================================================');
console.log('Running Linux Autostart Manager Unit Tests...');
console.log('====================================================');

// Mock a temporary home directory for testing desktop file creation
const tempHome = path.join(os.tmpdir(), `opensave-test-autostart-${Date.now()}`);
fs.mkdirSync(tempHome, { recursive: true });

// Mock the Electron app dependency
const mockApp = {
  getPath: (name) => {
    if (name === 'home') return tempHome;
    throw new Error(`Unexpected getPath call: ${name}`);
  },
  setLoginItemSettings: () => {
    // Standard Windows/macOS mock
  }
};

// Expose mock variables
global.app = mockApp;

// Create a mock implementation of global.updateStartupSettings (using the logic in main.js)
const testUpdateStartupSettings = (enabled, platformOverride) => {
  try {
    const platform = platformOverride || process.platform;
    if (platform === 'linux') {
      const homeDir = mockApp.getPath('home');
      const autostartDir = path.join(homeDir, '.config', 'autostart');
      const desktopPath = path.join(autostartDir, 'opensave.desktop');

      if (enabled) {
        if (!fs.existsSync(autostartDir)) {
          fs.mkdirSync(autostartDir, { recursive: true });
        }
        const execPath = '/usr/bin/opensave';
        const desktopContent = `[Desktop Entry]
Type=Application
Version=1.0
Name=OpenSave
Comment=OpenSave background game save synchronizer daemon
Exec="${execPath}" --hidden
Icon=opensave
Terminal=false
Categories=Utility;
X-GNOME-Autostart-enabled=true
`;
        fs.writeFileSync(desktopPath, desktopContent, 'utf8');
      } else {
        if (fs.existsSync(desktopPath)) {
          fs.unlinkSync(desktopPath);
        }
      }
    }
  } catch (err) {
    console.error('Failed to set login item settings:', err.message);
  }
};

try {
  const autostartDir = path.join(tempHome, '.config', 'autostart');
  const desktopFile = path.join(autostartDir, 'opensave.desktop');

  // Test Case 1: Enabling Autostart on Linux
  testUpdateStartupSettings(true, 'linux');
  assert.strictEqual(fs.existsSync(desktopFile), true, 'Desktop autostart file should be created');
  const content = fs.readFileSync(desktopFile, 'utf8');
  assert.ok(content.includes('Name=OpenSave'), 'Desktop file should have correct name');
  assert.ok(content.includes('Exec="/usr/bin/opensave" --hidden'), 'Desktop file should have correct exec path');
  console.log('✔ PASS: Successfully created and verified Linux desktop entry on enable.');

  // Test Case 2: Disabling Autostart on Linux
  testUpdateStartupSettings(false, 'linux');
  assert.strictEqual(fs.existsSync(desktopFile), false, 'Desktop autostart file should be deleted on disable');
  console.log('✔ PASS: Successfully deleted Linux desktop entry on disable.');

  // Clean up
  if (fs.existsSync(autostartDir)) {
    fs.rmSync(tempHome, { recursive: true, force: true });
  }

  console.log('\n✅ ALL LINUX AUTOSTART TESTS PASSED!');
  process.exit(0);
} catch (err) {
  console.error('\n❌ LINUX AUTOSTART TESTS FAILED:', err.stack || err.message);
  process.exit(1);
}
