import assert from 'assert';
import fs from 'fs';
import path from 'path';
import os from 'os';
import express from 'express';
import db from '../src/daemon/db.js';
import { uploadToCloud } from '../src/daemon/cloud.js';

console.log('====================================================');
console.log('Running Cloud Sync Driver Integration Tests...');
console.log('====================================================');

const MOCK_PORT = 8399;
const app = express();

// A simple body parser that gets raw buffers for PUT requests
app.use((req, res, next) => {
  if (req.method === 'PUT') {
    const chunks = [];
    req.on('data', chunk => chunks.push(chunk));
    req.on('end', () => {
      req.rawBody = Buffer.concat(chunks);
      next();
    });
  } else {
    next();
  }
});

// For webhook POST requests, we use simple text/multipart checks
app.use(express.text({ type: '*/*' }));

let lastRequest = null;

app.put('/test-webdav/:fileName', (req, res) => {
  lastRequest = {
    method: 'PUT',
    url: req.url,
    headers: req.headers,
    body: req.rawBody
  };
  res.sendStatus(201);
});

app.post('/test-webhook', (req, res) => {
  lastRequest = {
    method: 'POST',
    url: req.url,
    headers: req.headers,
    body: req.body
  };
  res.sendStatus(200);
});

// Intercept global fetch for cloud OAuth-based providers
const originalFetch = global.fetch;
global.fetch = async (url, options) => {
  if (url.startsWith('https://www.googleapis.com/')) {
    lastRequest = {
      provider: 'google_drive',
      url,
      headers: options.headers,
      body: options.body
    };
    return {
      ok: true,
      status: 200,
      json: async () => ({ id: 'gdrive-mock-file-id' })
    };
  }

  if (url.startsWith('https://content.dropboxapi.com/')) {
    lastRequest = {
      provider: 'dropbox',
      url,
      headers: options.headers,
      body: options.body
    };
    return {
      ok: true,
      status: 200,
      json: async () => ({ id: 'dropbox-mock-file-id' })
    };
  }

  if (url.startsWith('https://graph.microsoft.com/')) {
    lastRequest = {
      provider: 'onedrive',
      url,
      headers: options.headers,
      body: options.body
    };
    return {
      ok: true,
      status: 200,
      json: async () => ({ id: 'onedrive-mock-file-id' })
    };
  }

  // Fallback to original fetch for WebDAV and Webhook
  return originalFetch(url, options);
};

const server = app.listen(MOCK_PORT, async () => {
  const tempDir = path.join(os.tmpdir(), `opensave-test-cloud-${Date.now()}`);
  fs.mkdirSync(tempDir, { recursive: true });
  db.setDbFileForTesting(path.join(tempDir, 'opensave-db.json'), tempDir);

  const testFile = path.join(tempDir, 'test-cloud-save.zip');
  fs.writeFileSync(testFile, 'ZIP_DATA_FOOTPRINT', 'utf8');

  try {
    // ----------------------------------------------------
    // Test Case 1: WebDAV PUT Upload
    // ----------------------------------------------------
    db.updateSettings({
      cloudSync: {
        enabled: true,
        provider: 'webdav',
        url: `http://localhost:${MOCK_PORT}/test-webdav/`,
        username: 'admin',
        password: 'secretpassword',
        headers: '{"X-Webdav-Header": "dav-test"}'
      }
    });

    lastRequest = null;
    await uploadToCloud(testFile, 'test-cloud-save.zip');

    assert.ok(lastRequest, 'Mock server should receive a request');
    assert.strictEqual(lastRequest.method, 'PUT');
    assert.strictEqual(lastRequest.url, '/test-webdav/test-cloud-save.zip');
    assert.strictEqual(lastRequest.headers['x-webdav-header'], 'dav-test');
    
    const expectedAuth = 'Basic ' + Buffer.from('admin:secretpassword').toString('base64');
    assert.strictEqual(lastRequest.headers['authorization'], expectedAuth);
    assert.strictEqual(lastRequest.body.toString('utf8'), 'ZIP_DATA_FOOTPRINT');
    console.log('✔ PASS: Successfully uploaded snapshot to WebDAV endpoint with basic authentication.');

    // ----------------------------------------------------
    // Test Case 2: Webhook POST Upload
    // ----------------------------------------------------
    db.updateSettings({
      cloudSync: {
        enabled: true,
        provider: 'webhook',
        url: `http://localhost:${MOCK_PORT}/test-webhook`,
        username: '',
        password: '',
        headers: '{"X-Webhook-Signature": "signed-token"}'
      }
    });

    lastRequest = null;
    await uploadToCloud(testFile, 'test-cloud-save.zip');

    assert.ok(lastRequest, 'Mock server should receive a webhook request');
    assert.strictEqual(lastRequest.method, 'POST');
    assert.strictEqual(lastRequest.url, '/test-webhook');
    assert.strictEqual(lastRequest.headers['x-webhook-signature'], 'signed-token');
    
    const bodyStr = lastRequest.body;
    assert.ok(bodyStr.includes('form-data; name="file"; filename="test-cloud-save.zip"'), 'Body should be a multipart/form-data with file parameters');
    assert.ok(bodyStr.includes('ZIP_DATA_FOOTPRINT'), 'Multipart body should contain file contents');
    console.log('✔ PASS: Successfully dispatched snapshot to Webhook endpoint with custom headers & multi-part body.');

    // ----------------------------------------------------
    // Test Case 3: Google Drive Upload
    // ----------------------------------------------------
    db.updateSettings({
      cloudSync: {
        enabled: true,
        provider: 'google_drive',
        folderId: 'gdrive-folder-123',
        tokens: {
          accessToken: 'gdrive-token-abc',
          refreshToken: 'gdrive-refresh-xyz',
          expiryTime: Date.now() + 3600000,
          userEmail: 'gdrive-user@gmail.com'
        }
      }
    });

    lastRequest = null;
    await uploadToCloud(testFile, 'test-cloud-save.zip');

    assert.ok(lastRequest, 'Should receive a Google Drive mock request');
    assert.strictEqual(lastRequest.provider, 'google_drive');
    assert.strictEqual(lastRequest.url, 'https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart');
    assert.strictEqual(lastRequest.headers['Authorization'], 'Bearer gdrive-token-abc');
    
    const gdriveFormData = lastRequest.body;
    assert.ok(gdriveFormData instanceof FormData, 'Body should be FormData instance');
    
    const metadataPart = gdriveFormData.get('metadata');
    assert.ok(metadataPart, 'Metadata part should exist');
    const metadataText = await metadataPart.text();
    const metadataObj = JSON.parse(metadataText);
    assert.strictEqual(metadataObj.name, 'test-cloud-save.zip');
    assert.strictEqual(metadataObj.mimeType, 'application/zip');
    assert.deepStrictEqual(metadataObj.parents, ['gdrive-folder-123']);

    const filePart = gdriveFormData.get('file');
    assert.ok(filePart, 'File part should exist');
    const fileText = await filePart.text();
    assert.strictEqual(fileText, 'ZIP_DATA_FOOTPRINT');
    console.log('✔ PASS: Successfully uploaded snapshot to Google Drive mock endpoint with metadata.');

    // ----------------------------------------------------
    // Test Case 4: Dropbox Upload
    // ----------------------------------------------------
    db.updateSettings({
      cloudSync: {
        enabled: true,
        provider: 'dropbox',
        tokens: {
          accessToken: 'dropbox-token-abc',
          refreshToken: 'dropbox-refresh-xyz',
          expiryTime: Date.now() + 3600000,
          userEmail: 'dropbox-user@gmail.com'
        }
      }
    });

    lastRequest = null;
    await uploadToCloud(testFile, 'test-cloud-save.zip');

    assert.ok(lastRequest, 'Should receive a Dropbox mock request');
    assert.strictEqual(lastRequest.provider, 'dropbox');
    assert.strictEqual(lastRequest.url, 'https://content.dropboxapi.com/2/files/upload');
    assert.strictEqual(lastRequest.headers['Authorization'], 'Bearer dropbox-token-abc');
    assert.strictEqual(lastRequest.headers['Content-Type'], 'application/octet-stream');
    
    const dropboxArgs = JSON.parse(lastRequest.headers['Dropbox-API-Arg']);
    assert.strictEqual(dropboxArgs.path, '/OpenSave/test-cloud-save.zip');
    assert.strictEqual(dropboxArgs.mode, 'overwrite');
    assert.strictEqual(dropboxArgs.mute, true);
    assert.strictEqual(lastRequest.body.toString('utf8'), 'ZIP_DATA_FOOTPRINT');
    console.log('✔ PASS: Successfully uploaded snapshot to Dropbox mock endpoint.');

    // ----------------------------------------------------
    // Test Case 5: OneDrive Upload
    // ----------------------------------------------------
    db.updateSettings({
      cloudSync: {
        enabled: true,
        provider: 'onedrive',
        tokens: {
          accessToken: 'onedrive-token-abc',
          refreshToken: 'onedrive-refresh-xyz',
          expiryTime: Date.now() + 3600000,
          userEmail: 'onedrive-user@gmail.com'
        }
      }
    });

    lastRequest = null;
    await uploadToCloud(testFile, 'test-cloud-save.zip');

    assert.ok(lastRequest, 'Should receive a OneDrive mock request');
    assert.strictEqual(lastRequest.provider, 'onedrive');
    assert.strictEqual(lastRequest.url, 'https://graph.microsoft.com/v1.0/me/drive/special/approot:/test-cloud-save.zip:/content');
    assert.strictEqual(lastRequest.headers['Authorization'], 'Bearer onedrive-token-abc');
    assert.strictEqual(lastRequest.headers['Content-Type'], 'application/zip');
    assert.strictEqual(lastRequest.body.toString('utf8'), 'ZIP_DATA_FOOTPRINT');
    console.log('✔ PASS: Successfully uploaded snapshot to OneDrive mock endpoint.');

    // ----------------------------------------------------
    // Test Case 6: Local Folder Sync
    // ----------------------------------------------------
    const localDestDir = path.join(tempDir, 'local-cloud-dest');
    db.updateSettings({
      cloudSync: {
        enabled: true,
        provider: 'local',
        url: localDestDir
      }
    });

    await uploadToCloud(testFile, 'test-cloud-save.zip');
    
    const copiedFilePath = path.join(localDestDir, 'test-cloud-save.zip');
    assert.ok(fs.existsSync(copiedFilePath), 'Local sync target should copy files to destination');
    assert.strictEqual(fs.readFileSync(copiedFilePath, 'utf8'), 'ZIP_DATA_FOOTPRINT');
    console.log('✔ PASS: Successfully completed local folder/NAS directory mirroring.');

    // Clean up
    fs.rmSync(tempDir, { recursive: true, force: true });
    global.fetch = originalFetch;
    console.log('\n✅ ALL CLOUD SYNC TESTS PASSED!');
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
    console.error('\n❌ CLOUD SYNC TESTS FAILED:', err.stack || err.message);
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
