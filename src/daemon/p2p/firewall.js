import { exec } from 'child_process';
import os from 'os';
import fs from 'fs';
import path from 'path';
import { log } from '../logger.js';
import db from '../db.js';

export function setupWindowsFirewall() {
  if (os.platform() !== 'win32') return;

  const settings = db.getSettings();
  if (settings.skipFirewallSetup) {
    log('info', 'Windows Firewall setup skipped (skipFirewallSetup is enabled).');
    return;
  }

  // Check if our rules exist
  exec('netsh advfirewall firewall show rule name="SyncSave TCP"', (err, stdout) => {
    if (err || !stdout.includes('SyncSave TCP')) {
      log('warn', 'SyncSave Windows Firewall rules not found. Requesting permission to configure rules...');

      const tempDir = os.tmpdir();
      const psScriptPath = path.join(tempDir, `syncsave-firewall-${Date.now()}.ps1`);
      
      const psScriptContent = `# SyncSave Firewall Configuration Script
netsh advfirewall firewall delete rule name="SaveSync TCP"
netsh advfirewall firewall delete rule name="SaveSync UDP"
netsh advfirewall firewall delete rule name="SyncSave TCP"
netsh advfirewall firewall delete rule name="SyncSave UDP"
netsh advfirewall firewall add rule name="SyncSave TCP" dir=in action=allow protocol=TCP localport=8383-8395 profile=any enable=yes description="SyncSave P2P sync and relay TCP traffic"
netsh advfirewall firewall add rule name="SyncSave UDP" dir=in action=allow protocol=UDP localport=8383-8395 profile=any enable=yes description="SyncSave LAN peer discovery UDP broadcast"
`;

      try {
        fs.writeFileSync(psScriptPath, psScriptContent, 'utf8');

        // Execute via elevated PowerShell (UAC prompt will appear)
        const cmd = `powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process powershell -ArgumentList '-NoProfile -ExecutionPolicy Bypass -File \\"${psScriptPath}\\"' -Verb RunAs -Wait"`;
        
        exec(cmd, (spawnErr) => {
          // Clean up temp file immediately
          try {
            if (fs.existsSync(psScriptPath)) {
              fs.unlinkSync(psScriptPath);
            }
          } catch (e) {}

          if (spawnErr) {
            log('error', 'User rejected or Windows failed to apply firewall rules. We will skip prompting again.');
            db.updateSettings({ skipFirewallSetup: true });
          } else {
            log('success', 'Windows Firewall configured successfully! SyncSave is ready for out-of-the-box local connections.');
          }
        });
      } catch (writeErr) {
        log('error', `Failed to write temporary firewall script: ${writeErr.message}`);
      }
    } else {
      log('info', 'SyncSave Windows Firewall rules verified successfully.');
    }
  });
}
