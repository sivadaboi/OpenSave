const fs = require('fs');
const path = require('path');

module.exports = async function(context) {
  const { electronPlatformName, appOutDir } = context;
  if (electronPlatformName === 'linux') {
    console.log('Running Linux post-pack hook...');
    const binPath = path.join(appOutDir, 'savesync');
    const targetBinPath = path.join(appOutDir, 'savesync.bin');
    
    if (fs.existsSync(binPath)) {
      console.log(`Renaming native binary ${binPath} to ${targetBinPath}...`);
      fs.renameSync(binPath, targetBinPath);
      
      console.log('Writing Linux launcher script...');
      const scriptContent = `#!/bin/bash
# SaveSync Linux Launcher Wrapper
# Bypasses chrome-sandbox SUID permission requirements for portable extraction
DIR="\$(cd "\$(dirname "\$0")" && pwd)"
exec "\$DIR/savesync.bin" --no-sandbox "\$@"
`;
      fs.writeFileSync(binPath, scriptContent, { encoding: 'utf8', mode: 0o755 });
      
      // Try to set permissions (helps if host OS supports it, though Windows NTFS might ignore it)
      try {
        fs.chmodSync(binPath, '755');
        fs.chmodSync(targetBinPath, '755');
        console.log('Permissions set for savesync wrapper and savesync.bin.');
      } catch (err) {
        console.warn(`Failed to set execution permissions: ${err.message}`);
      }
    } else {
      console.error(`Binary not found at ${binPath}`);
    }
  }
};
