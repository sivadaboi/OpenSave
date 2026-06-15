import assert from 'assert';
import fs from 'fs';
import path from 'path';
import os from 'os';
import express from 'express';
import db from '../src/daemon/db.js';
import { listCloudFiles, downloadFromCloud } from '../src/daemon/cloud.js';

console.log('====================================================');
console.log('Running Cloud Explorer Unit & Integration Tests...');
console.log('====================================================');

const MOCK_PORT = 8398;
const app = express();

let lastRequest = null;

// Mock WebDAV endpoint
app.get('/test-webdav/:fileName', (req, res) => {
  lastRequest = { method: 'GET', url: req.url, headers: req.headers };
  res.setHeader('Content-Type', 'application/zip');
  res.send(Buffer.from('MOCK_WEBDAV_ZIP_DATA'));
});

app.use('/test-webdav', (req, res) => {
  if (req.method === 'PROPFIND') {
    lastRequest = { method: 'PROPFIND', url: req.url, headers: req.headers };
    
    // Return mock WebDAV XML responses with file info
    const xmlResponse = `<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/test-webdav/</D:href>
    <D:propstat>
      <D:prop>
        <D:resourcetype><D:collection/></D:resourcetype>
      </D:prop>
    </D:propstat>
  </D:response>
  <D:response>
    <D:href>/test-webdav/dark-souls-iii__main__snap_1780939143631.zip</D:href>
    <D:propstat>
      <D:prop>
        <D:getcontentlength>12345</D:getcontentlength>
        <D:getlastmodified>Mon, 09 Jun 2026 12:00:00 GMT</D:getlastmodified>
        <D:resourcetype/>
      </D:prop>
      <D:status>HTTP/1.1 200 OK</D:status>
    </D:propstat>
  </D:response>
  <D:response>
    <D:href>/test-webdav/hollow-knight__main__snap_1780939143999.zip</D:href>
    <D:propstat>
      <D:prop>
        <D:getcontentlength>6789</D:getcontentlength>
        <D:getlastmodified>Mon, 09 Jun 2026 13:00:00 GMT</D:getlastmodified>
        <D:resourcetype/>
      </D:prop>
      <D:status>HTTP/1.1 200 OK</D:status>
    </D:propstat>
  </D:response>
</D:multistatus>`;
    res.setHeader('Content-Type', 'text/xml');
    res.status(207).send(xmlResponse);
  } else {
    res.sendStatus(405);
  }
});

// Intercept global fetch to mock OAuth providers
const originalFetch = global.fetch;
global.fetch = async (url, options = {}) => {
  const method = options.method || 'GET';
  
  if (url.startsWith('https://www.googleapis.com/')) {
    lastRequest = { provider: 'google_drive', method, url, headers: options.headers, body: options.body };
    
    // File list response
    if (url.includes('/files?q=')) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          files: [
            { id: 'gd-file-1', name: 'dark-souls-iii__main__snap_1780939143631.zip', size: '12345', createdTime: '2026-06-09T12:00:00.000Z' },
            { id: 'gd-file-2', name: 'hollow-knight__main__snap_1780939143999.zip', size: '6789', createdTime: '2026-06-09T13:00:00.000Z' }
          ]
        })
      };
    }
    
    // File media response
    if (url.includes('/files/gd-file-1?alt=media')) {
      return {
        ok: true,
        status: 200,
        arrayBuffer: async () => new TextEncoder().encode('MOCK_GDRIVE_ZIP_DATA').buffer
      };
    }
  }

  if (url.startsWith('https://api.dropboxapi.com/')) {
    lastRequest = { provider: 'dropbox', method, url, headers: options.headers, body: options.body };
    
    if (url.includes('/list_folder')) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          entries: [
            { '.tag': 'file', name: 'dark-souls-iii__main__snap_1780939143631.zip', size: 12345, client_modified: '2026-06-09T12:00:00.000Z' },
            { '.tag': 'file', name: 'hollow-knight__main__snap_1780939143999.zip', size: 6789, client_modified: '2026-06-09T13:00:00.000Z' }
          ]
        })
      };
    }
  }

  if (url.startsWith('https://content.dropboxapi.com/')) {
    lastRequest = { provider: 'dropbox', method, url, headers: options.headers, body: options.body };
    
    if (url.includes('/download')) {
      return {
        ok: true,
        status: 200,
        arrayBuffer: async () => new TextEncoder().encode('MOCK_DROPBOX_ZIP_DATA').buffer
      };
    }
  }

  if (url.startsWith('https://graph.microsoft.com/')) {
    lastRequest = { provider: 'onedrive', method, url, headers: options.headers, body: options.body };
    
    if (url.includes('/approot/children')) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          value: [
            { file: {}, name: 'dark-souls-iii__main__snap_1780939143631.zip', size: 12345, createdDateTime: '2026-06-09T12:00:00.000Z' },
            { file: {}, name: 'hollow-knight__main__snap_1780939143999.zip', size: 6789, createdDateTime: '2026-06-09T13:00:00.000Z' }
          ]
        })
      };
    }

    if (url.includes('approot:/dark-souls-iii__main__snap_1780939143631.zip:/content')) {
      return {
        ok: true,
        status: 200,
        arrayBuffer: async () => new TextEncoder().encode('MOCK_ONEDRIVE_ZIP_DATA').buffer
      };
    }
  }

  return originalFetch(url, options);
};

const server = app.listen(MOCK_PORT, async () => {
  const tempDir = path.join(os.tmpdir(), `opensave-test-explorer-${Date.now()}`);
  fs.mkdirSync(tempDir, { recursive: true });
  db.setDbFileForTesting(path.join(tempDir, 'opensave-db.json'), tempDir);

  try {
    // ----------------------------------------------------
    // Test Case 1: WebDAV list and download
    // ----------------------------------------------------
    db.updateSettings({
      cloudSync: {
        enabled: true,
        provider: 'webdav',
        url: `http://localhost:${MOCK_PORT}/test-webdav/`,
        username: 'webdav-user',
        password: 'webdav-password'
      }
    });

    lastRequest = null;
    const webdavFiles = await listCloudFiles();
    assert.ok(lastRequest, 'Should make request to WebDAV server');
    assert.strictEqual(lastRequest.method, 'PROPFIND');
    assert.strictEqual(webdavFiles.length, 2);
    assert.strictEqual(webdavFiles[0].name, 'dark-souls-iii__main__snap_1780939143631.zip');
    assert.strictEqual(webdavFiles[0].sizeBytes, 12345);
    console.log('✔ PASS: Successfully listed cloud snapshots from WebDAV server and parsed XML.');

    const downloadPath1 = path.join(tempDir, 'downloaded_webdav.zip');
    await downloadFromCloud('dark-souls-iii__main__snap_1780939143631.zip', downloadPath1);
    assert.ok(fs.existsSync(downloadPath1));
    assert.strictEqual(fs.readFileSync(downloadPath1, 'utf8'), 'MOCK_WEBDAV_ZIP_DATA');
    console.log('✔ PASS: Successfully downloaded snapshot from WebDAV server.');

    // ----------------------------------------------------
    // Test Case 2: Google Drive list and download
    // ----------------------------------------------------
    db.updateSettings({
      cloudSync: {
        enabled: true,
        provider: 'google_drive',
        folderId: 'folder-abc',
        tokens: { accessToken: 'token-abc', expiryTime: Date.now() + 360000, userEmail: 'test@gmail.com' }
      }
    });

    lastRequest = null;
    const gdFiles = await listCloudFiles();
    assert.ok(lastRequest);
    assert.strictEqual(lastRequest.provider, 'google_drive');
    assert.ok(lastRequest.url.includes('/files?q='));
    assert.strictEqual(gdFiles.length, 2);
    assert.strictEqual(gdFiles[0].name, 'dark-souls-iii__main__snap_1780939143631.zip');
    console.log('✔ PASS: Successfully listed cloud snapshots from Google Drive API.');

    const downloadPath2 = path.join(tempDir, 'downloaded_gd.zip');
    await downloadFromCloud('dark-souls-iii__main__snap_1780939143631.zip', downloadPath2);
    assert.strictEqual(fs.readFileSync(downloadPath2, 'utf8'), 'MOCK_GDRIVE_ZIP_DATA');
    console.log('✔ PASS: Successfully downloaded snapshot from Google Drive API.');

    // ----------------------------------------------------
    // Test Case 3: Dropbox list and download
    // ----------------------------------------------------
    db.updateSettings({
      cloudSync: {
        enabled: true,
        provider: 'dropbox',
        tokens: { accessToken: 'token-abc', expiryTime: Date.now() + 360000, userEmail: 'test@gmail.com' }
      }
    });

    lastRequest = null;
    const dbFiles = await listCloudFiles();
    assert.ok(lastRequest);
    assert.strictEqual(lastRequest.provider, 'dropbox');
    assert.strictEqual(dbFiles.length, 2);
    console.log('✔ PASS: Successfully listed cloud snapshots from Dropbox API.');

    const downloadPath3 = path.join(tempDir, 'downloaded_dropbox.zip');
    await downloadFromCloud('dark-souls-iii__main__snap_1780939143631.zip', downloadPath3);
    assert.strictEqual(fs.readFileSync(downloadPath3, 'utf8'), 'MOCK_DROPBOX_ZIP_DATA');
    console.log('✔ PASS: Successfully downloaded snapshot from Dropbox API.');

    // ----------------------------------------------------
    // Test Case 4: OneDrive list and download
    // ----------------------------------------------------
    db.updateSettings({
      cloudSync: {
        enabled: true,
        provider: 'onedrive',
        tokens: { accessToken: 'token-abc', expiryTime: Date.now() + 360000, userEmail: 'test@gmail.com' }
      }
    });

    lastRequest = null;
    const odFiles = await listCloudFiles();
    assert.ok(lastRequest);
    assert.strictEqual(lastRequest.provider, 'onedrive');
    assert.strictEqual(odFiles.length, 2);
    console.log('✔ PASS: Successfully listed cloud snapshots from OneDrive API.');

    const downloadPath4 = path.join(tempDir, 'downloaded_onedrive.zip');
    await downloadFromCloud('dark-souls-iii__main__snap_1780939143631.zip', downloadPath4);
    assert.strictEqual(fs.readFileSync(downloadPath4, 'utf8'), 'MOCK_ONEDRIVE_ZIP_DATA');
    console.log('✔ PASS: Successfully downloaded snapshot from OneDrive API.');

    // ----------------------------------------------------
    // Test Case 5: Local Folder list and copy
    // ----------------------------------------------------
    const localCloudDir = path.join(tempDir, 'local-cloud');
    fs.mkdirSync(localCloudDir, { recursive: true });
    
    // Populate fake local cloud snapshot files
    fs.writeFileSync(path.join(localCloudDir, 'dark-souls-iii__main__snap_1780939143631.zip'), 'MOCK_LOCAL_ZIP_DATA');
    fs.writeFileSync(path.join(localCloudDir, 'hollow-knight__main__snap_1780939143999.zip'), 'MOCK_LOCAL_ZIP_DATA_2');

    db.updateSettings({
      cloudSync: {
        enabled: true,
        provider: 'local',
        url: localCloudDir
      }
    });

    const localFiles = await listCloudFiles();
    assert.strictEqual(localFiles.length, 2);
    console.log('✔ PASS: Successfully listed cloud snapshots from Local Folder.');

    const downloadPath5 = path.join(tempDir, 'downloaded_local.zip');
    await downloadFromCloud('dark-souls-iii__main__snap_1780939143631.zip', downloadPath5);
    assert.strictEqual(fs.readFileSync(downloadPath5, 'utf8'), 'MOCK_LOCAL_ZIP_DATA');
    console.log('✔ PASS: Successfully copied snapshot from Local Folder.');

    // Clean up
    fs.rmSync(tempDir, { recursive: true, force: true });
    global.fetch = originalFetch;
    console.log('\n✅ ALL CLOUD EXPLORER TESTS PASSED SUCCESSFULLY!');
    if (server.closeAllConnections) {
      server.closeAllConnections();
    }
    server.close(() => {
      setTimeout(() => {
        process.exit(0);
      }, 50);
    });
  } catch (err) {
    global.fetch = originalFetch;
    console.error('\n❌ CLOUD EXPLORER TESTS FAILED:', err.stack || err.message);
    if (server.closeAllConnections) {
      server.closeAllConnections();
    }
    server.close(() => {
      setTimeout(() => {
        process.exit(1);
      }, 50);
    });
  }
});
