import fs from 'fs';
import path from 'path';
import os from 'os';

const HOME_DIR = path.join(os.homedir(), '.opensave');
const ERROR_LOG_FILE = path.join(HOME_DIR, 'startup-error.log');

// Ensure home directory exists for log file
try {
  if (!fs.existsSync(HOME_DIR)) {
    fs.mkdirSync(HOME_DIR, { recursive: true });
  }
} catch (e) {
  console.error('[errorHandler] Failed to ensure home directory:', e.message);
}

export function logErrorToFile(error) {
  try {
    const timestamp = new Date().toISOString();
    const errorDetails = error instanceof Error 
      ? `${error.stack || error.message}` 
      : typeof error === 'object' 
        ? JSON.stringify(error) 
        : String(error);
    const errorMessage = `[${timestamp}] ${errorDetails}\n`;
    fs.appendFileSync(ERROR_LOG_FILE, errorMessage, 'utf8');
  } catch (e) {
    console.error('[errorHandler] Failed to write error log to disk:', e.message);
  }
}

// Trap uncaught exceptions in the Electron main process
process.on('uncaughtException', async (error) => {
  console.error('[Uncaught Exception]', error);
  logErrorToFile(error);
  try {
    const { app, dialog } = await import('electron');
    const detail = error && (error.stack || error.message || String(error));
    dialog.showErrorBox(
      'OpenSave Startup Crash',
      `An unexpected main process crash occurred.\n\nError Details:\n${detail}\n\nThis error has been logged to:\n${ERROR_LOG_FILE}`
    );
    app.quit();
  } catch (err) {
    console.error('Failed to show Electron error box, exiting process:', err.message);
    process.exit(1);
  }
});

// Trap unhandled promise rejections
process.on('unhandledRejection', async (reason) => {
  console.error('[Unhandled Rejection]', reason);
  logErrorToFile(reason);
  try {
    const { app, dialog } = await import('electron');
    const detail = reason && (reason.stack || reason.message || String(reason));
    dialog.showErrorBox(
      'OpenSave Unhandled Rejection',
      `An unhandled promise rejection occurred in the main process.\n\nError Details:\n${detail}\n\nThis error has been logged to:\n${ERROR_LOG_FILE}`
    );
    app.quit();
  } catch (err) {
    console.error('Failed to show Electron error box, exiting process:', err.message);
    process.exit(1);
  }
});
