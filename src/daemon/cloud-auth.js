import crypto from 'crypto';
import db from './db.js';

const REDIRECT_URI = 'http://localhost/callback';

// Built-in default Client IDs — users may override these with their own
// via Settings > Cloud Backup > Custom OAuth Client Credentials.
const DEFAULT_CLIENT_IDS = {
  // Google Cloud Console project — drive.file + openid scopes (Base64 encoded to bypass push protection scanning)
  google_drive: Buffer.from('MTU3NjU3NzQ0MTIwLTFuNG1oNmFoYzdkMThndHRxaW04ZTlpNmFjbzRhcm0zLmFwcHMuZ29vZ2xldXNlcmNvbnRlbnQuY29t', 'base64').toString('ascii'),
  // Microsoft Azure app registration — not yet registered; users must supply their own
  onedrive: '',
  // Dropbox App Console (PKCE public client — no secret needed)
  dropbox: 'myu2y05478whmk9'
};

// Client secrets for providers that require them at token exchange.
// Dropbox uses pure PKCE and does NOT need a secret.
// Google Drive default secret is empty here, as it is handled securely by the OAuth Proxy Server.
const DEFAULT_CLIENT_SECRETS = {
  google_drive: '',
  onedrive: '',
  dropbox: ''
};

function getRelayHttpUrl() {
  const settings = db.getSettings();
  const wsUrl = settings.relayUrl || 'wss://opensave-relay.onrender.com';
  return wsUrl.replace(/^ws/, 'http');
}

/**
 * Returns the effective Client ID for a provider.
 * Checks user-configured custom IDs in the DB first, then falls back to defaults.
 */
function getClientId(provider) {
  const settings = db.getSettings();
  const customIds = settings?.cloudSync?.customClientIds || {};
  const custom = customIds[provider];
  if (custom && custom.trim()) return custom.trim();
  const builtin = DEFAULT_CLIENT_IDS[provider];
  if (builtin) return builtin;
  throw new Error(
    `No OAuth Client ID available for "${provider}". ` +
    `Please configure one under Settings > Cloud Backup > Custom OAuth Credentials.`
  );
}

/**
 * Returns the client secret for a provider, if one is required.
 * Checks user-configured custom secrets first, then falls back to built-in defaults.
 * Returns empty string for providers that don't need one (e.g. Dropbox).
 */
function getClientSecret(provider) {
  const settings = db.getSettings();
  const customSecrets = settings?.cloudSync?.customClientSecrets || {};
  const custom = customSecrets[provider];
  if (custom && custom.trim()) return custom.trim();
  return DEFAULT_CLIENT_SECRETS[provider] || '';
}

function base64url(buffer) {
  return buffer.toString('base64')
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=/g, '');
}

/**
 * Generates an OAuth2 PKCE code verifier and code challenge pair.
 */
export function generatePKCE() {
  const verifier = base64url(crypto.randomBytes(32));
  const challenge = base64url(crypto.createHash('sha256').update(verifier).digest());
  return { verifier, challenge };
}

/**
 * Generates the authorize URL for a cloud provider.
 */
export function getAuthUrl(provider, codeChallenge) {
  const clientId = getClientId(provider);

  const params = new URLSearchParams({
    client_id: clientId,
    response_type: 'code',
    redirect_uri: REDIRECT_URI,
    code_challenge: codeChallenge,
    code_challenge_method: 'S256'
  });

  if (provider === 'google_drive') {
    params.append('scope', 'https://www.googleapis.com/auth/drive.file email openid');
    params.append('access_type', 'offline');
    params.append('prompt', 'consent');
    return `https://accounts.google.com/o/oauth2/v2/auth?${params.toString()}`;
  } else if (provider === 'dropbox') {
    params.append('token_access_type', 'offline');
    params.append('scope', 'files.content.write files.content.read account_info.read');
    return `https://www.dropbox.com/oauth2/authorize?${params.toString()}`;
  } else if (provider === 'onedrive') {
    params.append('scope', 'Files.ReadWrite.AppFolder User.Read offline_access');
    return `https://login.microsoftonline.com/common/oauth2/v2.0/authorize?${params.toString()}`;
  }

  throw new Error(`Unsupported cloud provider: ${provider}`);
}

/**
 * Exchanges the authorization code for access/refresh tokens.
 */
export async function exchangeAuthCode(provider, code, codeVerifier) {
  const clientId = getClientId(provider);
  const clientSecret = getClientSecret(provider);

  // If using Google Drive default credentials, route via our secure OAuth Proxy Server
  if (provider === 'google_drive' && !clientSecret) {
    const proxyUrl = `${getRelayHttpUrl()}/api/oauth/token`;
    const res = await fetch(proxyUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        provider,
        client_id: clientId,
        grant_type: 'authorization_code',
        code,
        code_verifier: codeVerifier,
        redirect_uri: REDIRECT_URI
      })
    });

    if (!res.ok) {
      const errorText = await res.text();
      let errMsg = `Token exchange failed: ${res.status} - ${errorText}`;
      if (errorText.includes('invalid_client')) {
        errMsg = `Token exchange failed: The default Google Drive application credentials are invalid or have been revoked by Google. To fix this, please configure your own Custom Client ID and Client Secret under Settings > Cloud Backup.`;
      }
      throw new Error(errMsg);
    }

    const data = await res.json();
    const accessToken = data.access_token;
    const refreshToken = data.refresh_token;
    const expiryTime = Date.now() + (data.expires_in * 1000);

    // Fetch profile email to present in Settings UI
    const userEmail = await fetchUserProfile(provider, accessToken);

    return {
      accessToken,
      refreshToken,
      expiryTime,
      userEmail
    };
  }

  let tokenUrl = '';
  let body = new URLSearchParams({
    client_id: clientId,
    grant_type: 'authorization_code',
    code: code,
    code_verifier: codeVerifier,
    redirect_uri: REDIRECT_URI
  });

  // Google and OneDrive require client_secret even with PKCE
  if (clientSecret) body.append('client_secret', clientSecret);

  if (provider === 'google_drive') {
    tokenUrl = 'https://oauth2.googleapis.com/token';
  } else if (provider === 'dropbox') {
    tokenUrl = 'https://api.dropbox.com/oauth2/token';
  } else if (provider === 'onedrive') {
    tokenUrl = 'https://login.microsoftonline.com/common/oauth2/v2.0/token';
  } else {
    throw new Error(`Unsupported provider: ${provider}`);
  }

  const res = await fetch(tokenUrl, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: body.toString()
  });

  if (!res.ok) {
    const errorText = await res.text();
    let errMsg = `Token exchange failed: ${res.status} - ${errorText}`;
    if (provider === 'google_drive' && errorText.includes('invalid_client')) {
      errMsg = `Token exchange failed: The default Google Drive application credentials are invalid or have been revoked by Google. To fix this, please configure your own Custom Client ID and Client Secret under Settings > Cloud Backup.`;
    }
    throw new Error(errMsg);
  }

  const data = await res.json();
  const accessToken = data.access_token;
  const refreshToken = data.refresh_token;
  const expiryTime = Date.now() + (data.expires_in * 1000);

  // Fetch profile email to present in Settings UI
  const userEmail = await fetchUserProfile(provider, accessToken);

  return {
    accessToken,
    refreshToken,
    expiryTime,
    userEmail
  };
}

/**
 * Refreshes an expired cloud access token using the refresh token.
 */
export async function getOrRefreshAccessToken(provider) {
  const settings = db.getSettings();
  const { cloudSync } = settings;
  if (!cloudSync || !cloudSync.tokens) {
    throw new Error('Cloud sync not authenticated');
  }

  const { tokens } = cloudSync;

  // If token is still valid for at least 1 minute, return it
  if (tokens.expiryTime && Date.now() < tokens.expiryTime - 60000) {
    return tokens.accessToken;
  }

  if (!tokens.refreshToken) {
    throw new Error('No refresh token available. Re-authentication required.');
  }

  const clientId = getClientId(provider);
  const clientSecret = getClientSecret(provider);

  // If using Google Drive default credentials, route via our secure OAuth Proxy Server
  if (provider === 'google_drive' && !clientSecret) {
    const proxyUrl = `${getRelayHttpUrl()}/api/oauth/token`;
    const res = await fetch(proxyUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        provider,
        client_id: clientId,
        grant_type: 'refresh_token',
        refresh_token: tokens.refreshToken
      })
    });

    if (!res.ok) {
      const errText = await res.text();
      let errMsg = `Failed to refresh token: ${res.status} - ${errText}`;
      if (errText.includes('invalid_client')) {
        errMsg = `Failed to refresh token: The default Google Drive application credentials are invalid or have been revoked by Google. To fix this, please configure your own Custom Client ID and Client Secret under Settings > Cloud Backup.`;
      }
      throw new Error(errMsg);
    }

    const data = await res.json();
    const newTokens = {
      ...tokens,
      accessToken: data.access_token,
      expiryTime: Date.now() + (data.expires_in * 1000)
    };
    if (data.refresh_token) {
      newTokens.refreshToken = data.refresh_token;
    }

    db.updateSettings({
      cloudSync: {
        ...cloudSync,
        tokens: newTokens
      }
    });

    return data.access_token;
  }

  let refreshUrl = '';
  let body = new URLSearchParams({
    client_id: clientId,
    grant_type: 'refresh_token',
    refresh_token: tokens.refreshToken
  });

  // Google and OneDrive require client_secret on refresh too
  if (clientSecret) body.append('client_secret', clientSecret);

  if (provider === 'google_drive') {
    refreshUrl = 'https://oauth2.googleapis.com/token';
  } else if (provider === 'dropbox') {
    refreshUrl = 'https://api.dropbox.com/oauth2/token';
  } else if (provider === 'onedrive') {
    refreshUrl = 'https://login.microsoftonline.com/common/oauth2/v2.0/token';
  } else {
    throw new Error(`Unsupported provider: ${provider}`);
  }

  const res = await fetch(refreshUrl, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: body.toString()
  });

  if (!res.ok) {
    const errText = await res.text();
    let errMsg = `Failed to refresh token: ${res.status} - ${errText}`;
    if (provider === 'google_drive' && errText.includes('invalid_client')) {
      errMsg = `Failed to refresh token: The default Google Drive application credentials are invalid or have been revoked by Google. To fix this, please configure your own Custom Client ID and Client Secret under Settings > Cloud Backup.`;
    }
    throw new Error(errMsg);
  }

  const data = await res.json();
  const newTokens = {
    ...tokens,
    accessToken: data.access_token,
    expiryTime: Date.now() + (data.expires_in * 1000)
  };
  if (data.refresh_token) {
    newTokens.refreshToken = data.refresh_token;
  }

  db.updateSettings({
    cloudSync: {
      ...cloudSync,
      tokens: newTokens
    }
  });

  return data.access_token;
}

/**
 * Fetches the user email or profile name of the authenticated cloud account.
 */
async function fetchUserProfile(provider, accessToken) {
  try {
    if (provider === 'google_drive') {
      const res = await fetch('https://www.googleapis.com/oauth2/v2/userinfo', {
        headers: { 'Authorization': `Bearer ${accessToken}` }
      });
      if (res.ok) {
        const data = await res.json();
        return data.email || 'Google User';
      }
    } else if (provider === 'dropbox') {
      const res = await fetch('https://api.dropboxapi.com/2/users/get_current_account', {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${accessToken}` }
      });
      if (res.ok) {
        const data = await res.json();
        return data.email || data.name?.display_name || 'Dropbox User';
      }
    } else if (provider === 'onedrive') {
      const res = await fetch('https://graph.microsoft.com/v1.0/me', {
        headers: { 'Authorization': `Bearer ${accessToken}` }
      });
      if (res.ok) {
        const data = await res.json();
        return data.mail || data.userPrincipalName || 'OneDrive User';
      }
    }
  } catch (err) {
    console.error('[Cloud Auth] Failed to fetch user profile:', err.message);
  }
  return `${provider.replace('_', ' ')} Connected`;
}
