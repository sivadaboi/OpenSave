const fs = require('fs');
const path = require('path');

module.exports = async function(context) {
  const { electronPlatformName, appOutDir } = context;
  if (electronPlatformName === 'linux') {
    console.log('Running Linux post-pack hook...');
    const binPath = path.join(appOutDir, 'syncsave');
    const targetBinPath = path.join(appOutDir, 'syncsave.bin');
    
    if (fs.existsSync(binPath)) {
      console.log(`Renaming native binary ${binPath} to ${targetBinPath}...`);
      fs.renameSync(binPath, targetBinPath);
      
      console.log('Writing Linux launcher script...');
      const scriptContent = `#!/bin/bash
# SyncSave Linux Launcher Wrapper
# Bypasses chrome-sandbox SUID permission requirements for portable extraction
DIR="\$(cd "\$(dirname "\$0")" && pwd)"
exec "\$DIR/syncsave.bin" --no-sandbox "\$@"
`;
      fs.writeFileSync(binPath, scriptContent, { encoding: 'utf8', mode: 0o755 });
      
      // Try to set permissions (helps if host OS supports it, though Windows NTFS might ignore it)
      try {
        fs.chmodSync(binPath, '755');
        fs.chmodSync(targetBinPath, '755');
        console.log('Permissions set for syncsave wrapper and syncsave.bin.');
      } catch (err) {
        console.warn(`Failed to set execution permissions: ${err.message}`);
      }
    } else {
      console.error(`Binary not found at ${binPath}`);
    }
  }
};
