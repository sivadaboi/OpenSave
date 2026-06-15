import { logErrorToFile } from './errorHandler.js';
import { app, BrowserWindow, dialog, Tray, Menu, nativeImage } from 'electron';
import path from 'path';
import { fileURLToPath } from 'url';
import fs from 'fs';

let mainWindow = null;
let tray = null;
let isQuitting = false;

// Valid 16x16 purple gamepad PNG (base64 encoded)
const TRAY_ICON_DATA = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAACXBIWXMAAAsTAAALEwEAmpwYAAABBklEQVQ4jZWTMUsDQRCFv707c0GCEbSxSqcWQrAQLBTtRND/IEFsBAsbwUoQLARBC0EUK1tbwULQQhDBQhCxsBFBEBG8u+PcLZzxknDJBR94u2/evJ3ZXSillFJKKaWUUkrpf3POuU3gbf1cEuBxEEJYdM5tAEdABjRArRBCWgghXMaYI2AB2AKeVoBzYB/YBdaBNWAXWAfmwE/2jRBCWM/zHFhrIYRQKKWUUkrpWSnlFUoFlFIeUEoplVJKKeWVUkoppZRSSimllFJKKaWUUkoZVUqZVUopZRaRlkMopVJKKaWUUkoppVBKKaWUUkqplFJKKaWUUkoppVBKKaWUUkqplFJKKaWUUkqplFJKKaWUUkqplFJKKeUPvgEqkRiEAAAAAElFTkSuQmCC';



// Expose native folder dialog browser to Express API bridge
global.selectDirectoryCallback = async () => {
  const result = await dialog.showOpenDialog({
    properties: ['openDirectory', 'createDirectory']
  });
  return result.filePaths[0];
};

// Expose file selection dialog to Express API bridge
global.selectFileCallback = async () => {
  const result = await dialog.showOpenDialog({
    properties: ['openFile'],
    filters: [
      { name: 'Executables', extensions: ['exe', 'bat', 'lnk', 'cmd'] },
      { name: 'All Files', extensions: ['*'] }
    ]
  });
  return result.filePaths[0];
};

// Expose external URL opening to daemon
global.openExternalUrl = async (url) => {
  const { shell } = await import('electron');
  return shell.openExternal(url);
};

// Expose startup settings manager
global.updateStartupSettings = (enabled) => {
  try {
    if (process.platform === 'linux') {
      const homeDir = app.getPath('home');
      const autostartDir = path.join(homeDir, '.config', 'autostart');
      const desktopPath = path.join(autostartDir, 'opensave.desktop');

      if (enabled) {
        if (!fs.existsSync(autostartDir)) {
          fs.mkdirSync(autostartDir, { recursive: true });
        }
        const execPath = process.execPath;
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
        console.log('[Startup Settings] Linux autostart desktop entry created.');
      } else {
        if (fs.existsSync(desktopPath)) {
          fs.unlinkSync(desktopPath);
          console.log('[Startup Settings] Linux autostart desktop entry removed.');
        }
      }
    } else {
      app.setLoginItemSettings({
        openAtLogin: enabled,
        path: process.execPath,
        args: ['--hidden']
      });
      console.log(`[Startup Settings] Login item settings updated: enabled=${enabled}`);
    }
  } catch (err) {
    console.error('[Startup Settings] Failed to set login item settings:', err.message);
  }
};

// Expose window state hooks to Express REST API
global.minimizeWindow = () => {
  if (mainWindow) mainWindow.minimize();
};

global.maximizeWindow = () => {
  if (mainWindow) {
    if (mainWindow.isMaximized()) {
      mainWindow.unmaximize();
    } else {
      mainWindow.maximize();
    }
  }
};

global.closeWindow = () => {
  if (mainWindow) {
    mainWindow.hide(); // Minimize to system tray
  }
};

// Expose OAuth BrowserWindow authorization popup to Express API daemon
global.openAuthWindow = (url, redirectUrl) => {
  return new Promise((resolve, reject) => {
    import('electron').then(({ BrowserWindow }) => {
      const authWindow = new BrowserWindow({
        width: 600,
        height: 750,
        show: true,
        title: 'Sign In — Cloud Provider',
        autoHideMenuBar: true,
        webPreferences: {
          nodeIntegration: false,
          contextIsolation: true
        }
      });

      authWindow.loadURL(url);

      const checkUrl = (currentUrl) => {
        if (currentUrl.startsWith(redirectUrl)) {
          try {
            const parsedUrl = new URL(currentUrl);
            const code = parsedUrl.searchParams.get('code');
            const error = parsedUrl.searchParams.get('error');
            if (code) {
              resolve(code);
            } else if (error) {
              reject(new Error(error));
            } else {
              reject(new Error('No authorization code found in redirect URL.'));
            }
          } catch (e) {
            reject(e);
          }
          authWindow.close();
        }
      };

      authWindow.webContents.on('will-navigate', (event, currentUrl) => {
        checkUrl(currentUrl);
      });

      authWindow.webContents.on('will-redirect', (event, currentUrl) => {
        checkUrl(currentUrl);
      });

      authWindow.on('closed', () => {
        reject(new Error('User closed the login window.'));
      });
    }).catch(err => {
      reject(new Error('Electron app is required for popup authentication window.'));
    });
  });
};

import db from './daemon/db.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

function createWindow() {
  const settings = db.getSettings();
  const port = settings.port || 8383;
  const shouldHide = process.argv.includes('--hidden') || process.argv.includes('--minimized');

  // Create the native window as frameless for custom OS header title bar
  mainWindow = new BrowserWindow({
    width: 1200,
    height: 800,
    minWidth: 900,
    minHeight: 650,
    frame: false,
    title: 'OpenSave — Universal Save Synchronizer',
    backgroundColor: '#05060f', // Match dashboard background
    show: !shouldHide,
    webPreferences: {
      nodeIntegration: false,
      contextIsolation: true
    }
  });

  // Remove default menu bar
  mainWindow.removeMenu();

  // Load the dashboard hosted locally by the daemon
  mainWindow.loadURL(`http://localhost:${port}`);

  // Prevent app close, hide window to run in system tray background
  mainWindow.on('close', (event) => {
    if (!isQuitting) {
      event.preventDefault();
      mainWindow.hide();
    }
  });

  // Handle open links externally in default browser
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    import('electron').then(({ shell }) => {
      shell.openExternal(url);
    });
    return { action: 'deny' };
  });

  // Create the system tray
  createTray();
}

function createTray() {
  if (tray) return;

  try {
    // Load icon directly from base64 data — no file I/O needed
    const icon = nativeImage.createFromDataURL(TRAY_ICON_DATA);
    tray = new Tray(icon);
  } catch (err) {
    console.error('[Tray] Failed to create tray icon:', err.message);
    // Fall back to an empty icon so the tray still works
    tray = new Tray(nativeImage.createEmpty());
  }

  tray.setToolTip('OpenSave — Running in background');

  const contextMenu = Menu.buildFromTemplate([
    {
      label: 'Open OpenSave',
      click: () => {
        if (mainWindow) {
          mainWindow.show();
          mainWindow.focus();
        }
      }
    },
    {
      label: 'Sync All Games Now',
      click: async () => {
        try {
          const settings = db.getSettings();
          const port = settings.port || 8383;
          const res = await fetch(`http://localhost:${port}/api/games/sync-all`, { method: 'POST' });
          if (res.ok) {
            console.log('[Tray] Sync-all triggered successfully.');
          }
        } catch (e) {
          console.error('[Tray] Sync-all trigger failed:', e.message);
        }
      }
    },
    { type: 'separator' },
    {
      label: 'Quit',
      click: () => {
        isQuitting = true;
        app.quit();
      }
    }
  ]);

  tray.setContextMenu(contextMenu);
  tray.on('double-click', () => {
    if (mainWindow) {
      mainWindow.show();
      mainWindow.focus();
    }
  });
}

const gotTheLock = app.requestSingleInstanceLock();

if (!gotTheLock) {
  console.log('[App] Another instance is already running. Quitting.');
  app.quit();
} else {
  app.on('second-instance', () => {
    // Focus the existing window if user tries to open it again
    if (mainWindow) {
      if (mainWindow.isMinimized()) mainWindow.restore();
      mainWindow.show();
      mainWindow.focus();
    }
  });

  // Start the background daemon dynamically to avoid running it in secondary instances
  import('./daemon/index.js')
    .then(() => {
      // Electron Startup
      app.whenReady().then(() => {
        // Sync startup settings with database state
        const settings = db.getSettings();
        if (global.updateStartupSettings) {
          global.updateStartupSettings(!!settings.startOnBoot);
        }

        createWindow();

        app.on('activate', () => {
          if (BrowserWindow.getAllWindows().length === 0) {
            createWindow();
          }
        });
      });
    })
    .catch((err) => {
      console.error('Failed to start OpenSave background daemon:', err);
      logErrorToFile(err);
      try {
        dialog.showErrorBox(
          'OpenSave Daemon Startup Error',
          `The background database/server daemon failed to initialize.\n\nError details:\n${err.stack || err.message || err}`
        );
      } catch (dialogErr) {}
      app.quit();
    });

  // Quit when all windows are closed
  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') {
      app.quit();
    }
  });
}
