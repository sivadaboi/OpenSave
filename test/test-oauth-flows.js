import assert from 'assert';
import path from 'path';
import os from 'os';
import fs from 'fs';
import db from '../src/daemon/db.js';
import { generatePKCE, getAuthUrl, exchangeAuthCode, getOrRefreshAccessToken } from '../src/daemon/cloud-auth.js';

console.log('====================================================');
console.log('Running Cloud OAuth Flow Tests...');
console.log('====================================================');

const tempDir = path.join(os.tmpdir(), `opensave-test-oauth-${Date.now()}`);
fs.mkdirSync(tempDir, { recursive: true });

// Redirect database save/load logic to a temporary test database
db.setDbFileForTesting(path.join(tempDir, 'opensave-db.json'), tempDir);

let fetchCalls = [];
const originalFetch = global.fetch;

// Mock global fetch
global.fetch = async (url, options) => {
  fetchCalls.push({ url, options });
  
  let bodyParams = {};
  if (options && options.body) {
    if (typeof options.body === 'string') {
      const parsed = new URLSearchParams(options.body);
      for (const [key, val] of parsed.entries()) {
        bodyParams[key] = val;
      }
    }
  }

  if (url === 'https://oauth2.googleapis.com/token') {
    if (bodyParams.grant_type === 'authorization_code') {
      assert.strictEqual(bodyParams.code, 'google-auth-code');
      assert.strictEqual(bodyParams.code_verifier, 'google-verifier');
      return {
        ok: true,
        status: 200,
        json: async () => ({
          access_token: 'google-access-token-123',
          refresh_token: 'google-refresh-token-456',
          expires_in: 3600
        })
      };
    } else if (bodyParams.grant_type === 'refresh_token') {
      assert.strictEqual(bodyParams.refresh_token, 'google-refresh-token-456');
      return {
        ok: true,
        status: 200,
        json: async () => ({
          access_token: 'google-refreshed-access-token-789',
          expires_in: 3600
        })
      };
    }
  }
  
  if (url === 'https://www.googleapis.com/oauth2/v2/userinfo') {
    assert.strictEqual(options.headers['Authorization'], 'Bearer google-access-token-123');
    return {
      ok: true,
      status: 200,
      json: async () => ({
        email: 'test-user@gmail.com'
      })
    };
  }

  if (url === 'https://api.dropbox.com/oauth2/token') {
    if (bodyParams.grant_type === 'authorization_code') {
      assert.strictEqual(bodyParams.code, 'dropbox-auth-code');
      return {
        ok: true,
        status: 200,
        json: async () => ({
          access_token: 'dropbox-access-token-123',
          refresh_token: 'dropbox-refresh-token-456',
          expires_in: 3600
        })
      };
    } else if (bodyParams.grant_type === 'refresh_token') {
      assert.strictEqual(bodyParams.refresh_token, 'dropbox-refresh-token-456');
      return {
        ok: true,
        status: 200,
        json: async () => ({
          access_token: 'dropbox-refreshed-access-token-789',
          expires_in: 3600
        })
      };
    }
  }

  if (url === 'https://api.dropboxapi.com/2/users/get_current_account') {
    assert.strictEqual(options.headers['Authorization'], 'Bearer dropbox-access-token-123');
    return {
      ok: true,
      status: 200,
      json: async () => ({
        email: 'test-user@dropbox.com'
      })
    };
  }

  if (url === 'https://login.microsoftonline.com/common/oauth2/v2.0/token') {
    if (bodyParams.grant_type === 'authorization_code') {
      assert.strictEqual(bodyParams.code, 'onedrive-auth-code');
      return {
        ok: true,
        status: 200,
        json: async () => ({
          access_token: 'onedrive-access-token-123',
          refresh_token: 'onedrive-refresh-token-456',
          expires_in: 3600
        })
      };
    } else if (bodyParams.grant_type === 'refresh_token') {
      assert.strictEqual(bodyParams.refresh_token, 'onedrive-refresh-token-456');
      return {
        ok: true,
        status: 200,
        json: async () => ({
          access_token: 'onedrive-refreshed-access-token-789',
          expires_in: 3600
        })
      };
    }
  }

  if (url === 'https://graph.microsoft.com/v1.0/me') {
    assert.strictEqual(options.headers['Authorization'], 'Bearer onedrive-access-token-123');
    return {
      ok: true,
      status: 200,
      json: async () => ({
        mail: 'test-user@onedrive.com'
      })
    };
  }

  return {
    ok: false,
    status: 404,
    text: async () => 'Not Found'
  };
};

try {
  // ----------------------------------------------------
  // Test PKCE Challenge Generation
  // ----------------------------------------------------
  const pkce = generatePKCE();
  assert.ok(pkce.verifier, 'Verifier must be generated');
  assert.ok(pkce.challenge, 'Challenge must be generated');
  assert.strictEqual(typeof pkce.verifier, 'string');
  assert.strictEqual(typeof pkce.challenge, 'string');
  console.log('✔ PASS: PKCE code verifier and S256 challenge generated successfully.');

  // ----------------------------------------------------
  // Test Auth URL Assembly
  // ----------------------------------------------------
  const googleUrl = getAuthUrl('google_drive', 'mock-challenge');
  assert.ok(googleUrl.includes('client_id='), 'Auth URL must contain client_id');
  assert.ok(googleUrl.includes('code_challenge=mock-challenge'), 'Auth URL must contain code challenge');
  assert.ok(googleUrl.includes('code_challenge_method=S256'), 'Auth URL must specify S256 method');
  assert.ok(googleUrl.includes('scope='), 'Auth URL must contain scope parameter');
  console.log('✔ PASS: Google Drive authorize URL successfully assembled with PKCE args.');

  const dropboxUrl = getAuthUrl('dropbox', 'mock-challenge');
  assert.ok(dropboxUrl.includes('code_challenge=mock-challenge'));
  assert.ok(dropboxUrl.includes('token_access_type=offline'));
  console.log('✔ PASS: Dropbox authorize URL successfully assembled.');

  // OneDrive has no built-in default Client ID — requires user to supply their own.
  // Verify that the error is thrown as expected when no key is configured.
  try {
    getAuthUrl('onedrive', 'mock-challenge');
    assert.fail('Expected getAuthUrl("onedrive") to throw when no Client ID is configured');
  } catch (err) {
    assert.ok(err.message.includes('No OAuth Client ID'), `Expected missing-key error, got: ${err.message}`);
    console.log('✔ PASS: OneDrive correctly requires user-supplied Client ID (no built-in key).');
  }

  // Verify OneDrive URL works when a custom Client ID is set in DB
  db.updateSettings({
    cloudSync: {
      ...db.getSettings().cloudSync,
      customClientIds: { google_drive: '', onedrive: 'my-test-ms-client-id', dropbox: '' }
    }
  });
  const onedriveUrl = getAuthUrl('onedrive', 'mock-challenge');
  assert.ok(onedriveUrl.includes('code_challenge=mock-challenge'));
  assert.ok(onedriveUrl.includes('my-test-ms-client-id'));
  console.log('✔ PASS: OneDrive authorize URL assembled with user-supplied Client ID.');
  // Reset custom IDs
  db.updateSettings({ cloudSync: { ...db.getSettings().cloudSync, customClientIds: { google_drive: '', onedrive: '', dropbox: '' } } });


  // ----------------------------------------------------
  // Test Auth Code Exchange & Profile Retrievals
  // ----------------------------------------------------
  fetchCalls = [];
  const googleTokens = await exchangeAuthCode('google_drive', 'google-auth-code', 'google-verifier');
  assert.strictEqual(googleTokens.accessToken, 'google-access-token-123');
  assert.strictEqual(googleTokens.refreshToken, 'google-refresh-token-456');
  assert.strictEqual(googleTokens.userEmail, 'test-user@gmail.com');
  assert.ok(googleTokens.expiryTime > Date.now());
  console.log('✔ PASS: Google Drive auth code exchanged and profile email retrieved.');

  const dropboxTokens = await exchangeAuthCode('dropbox', 'dropbox-auth-code', 'dropbox-verifier');
  assert.strictEqual(dropboxTokens.accessToken, 'dropbox-access-token-123');
  assert.strictEqual(dropboxTokens.refreshToken, 'dropbox-refresh-token-456');
  assert.strictEqual(dropboxTokens.userEmail, 'test-user@dropbox.com');
  console.log('✔ PASS: Dropbox auth code exchanged and profile email retrieved.');

  // OneDrive requires a user-supplied custom Client ID — set one before testing exchange
  db.updateSettings({ cloudSync: { ...db.getSettings().cloudSync, customClientIds: { google_drive: '', onedrive: 'my-test-ms-client-id', dropbox: '' } } });
  const onedriveTokens = await exchangeAuthCode('onedrive', 'onedrive-auth-code', 'onedrive-verifier');
  assert.strictEqual(onedriveTokens.accessToken, 'onedrive-access-token-123');
  assert.strictEqual(onedriveTokens.refreshToken, 'onedrive-refresh-token-456');
  assert.strictEqual(onedriveTokens.userEmail, 'test-user@onedrive.com');
  console.log('✔ PASS: OneDrive auth code exchanged and profile email retrieved (with custom Client ID).');
  db.updateSettings({ cloudSync: { ...db.getSettings().cloudSync, customClientIds: { google_drive: '', onedrive: '', dropbox: '' } } });


  // ----------------------------------------------------
  // Test Access Token Cache & Expiration Refreshes
  // ----------------------------------------------------
  // Save credentials in db setting (valid token first)
  db.updateSettings({
    cloudSync: {
      enabled: true,
      provider: 'google_drive',
      tokens: {
        accessToken: 'cached-google-token',
        refreshToken: 'google-refresh-token-456',
        expiryTime: Date.now() + 600000, // 10 minutes in future
        userEmail: 'test-user@gmail.com'
      }
    }
  });

  fetchCalls = [];
  // Should return cached token without using fetch
  const cachedToken = await getOrRefreshAccessToken('google_drive');
  assert.strictEqual(cachedToken, 'cached-google-token');
  assert.strictEqual(fetchCalls.length, 0, 'Should not initiate network fetch when token is still valid');
  console.log('✔ PASS: Returned cached token directly since expiration is in the future.');

  // Now expire the token
  db.updateSettings({
    cloudSync: {
      enabled: true,
      provider: 'google_drive',
      tokens: {
        accessToken: 'expired-google-token',
        refreshToken: 'google-refresh-token-456',
        expiryTime: Date.now() - 1000, // expired 1s ago
        userEmail: 'test-user@gmail.com'
      }
    }
  });

  fetchCalls = [];
  // Should refresh the token
  const refreshedToken = await getOrRefreshAccessToken('google_drive');
  assert.strictEqual(refreshedToken, 'google-refreshed-access-token-789');
  assert.strictEqual(fetchCalls.length, 1);
  assert.strictEqual(fetchCalls[0].url, 'https://oauth2.googleapis.com/token');
  
  // DB should be updated with new token and new expiry time
  const updatedSettings = db.getSettings();
  assert.strictEqual(updatedSettings.cloudSync.tokens.accessToken, 'google-refreshed-access-token-789');
  assert.ok(updatedSettings.cloudSync.tokens.expiryTime > Date.now());
  console.log('✔ PASS: Successfully refreshed expired access token using refresh token.');

  // Clean up
  fs.rmSync(tempDir, { recursive: true, force: true });
  global.fetch = originalFetch;
  console.log('\n✅ ALL OAUTH FLOW TESTS PASSED!');
  process.exit(0);
} catch (err) {
  global.fetch = originalFetch;
  console.error('\n❌ OAUTH FLOW TESTS FAILED:', err.stack || err.message);
  process.exit(1);
}
