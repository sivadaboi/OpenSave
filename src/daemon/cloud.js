import fs from 'fs';
import path from 'path';
import db from './db.js';
import { log } from './logger.js';
import { getOrRefreshAccessToken } from './cloud-auth.js';

/**
 * Uploads a file (e.g. a snapshot ZIP) to the configured cloud provider in the background.
 * @param {string} filePath - Absolute path to the file on disk.
 * @param {string} fileName - Filename to use on the remote target.
 */
export async function uploadToCloud(filePath, fileName) {
  const settings = db.getSettings();
  const { cloudSync } = settings;

  if (!cloudSync || !cloudSync.enabled) {
    return;
  }

  const { provider } = cloudSync;
  log('info', `Cloud Sync: Uploading ${fileName} via ${provider.toUpperCase()}...`);

  try {
    if (!fs.existsSync(filePath)) {
      throw new Error(`File not found: ${filePath}`);
    }

    const fileBuffer = fs.readFileSync(filePath);
    let success = false;

    if (provider === 'webdav' || provider === 'webhook') {
      const { url, username, password, headers } = cloudSync;
      if (!url) {
        throw new Error('No destination URL configured.');
      }

      let fetchUrl = url;
      let options = {
        headers: {}
      };

      // Parse custom headers if configured
      if (headers) {
        try {
          const parsed = JSON.parse(headers);
          options.headers = { ...options.headers, ...parsed };
        } catch (e) {
          log('warn', `Cloud Sync: Failed to parse custom headers JSON: ${e.message}`);
        }
      }

      if (provider === 'webdav') {
        const baseUrl = url.endsWith('/') ? url : `${url}/`;
        fetchUrl = `${baseUrl}${encodeURIComponent(fileName)}`;
        options.method = 'PUT';
        options.body = fileBuffer;
        options.headers['Content-Type'] = 'application/zip';

        if (username || password) {
          const credentials = Buffer.from(`${username || ''}:${password || ''}`).toString('base64');
          options.headers['Authorization'] = `Basic ${credentials}`;
        }
      } else if (provider === 'webhook') {
        options.method = 'POST';
        const formData = new FormData();
        const blob = new Blob([fileBuffer]);
        formData.append('file', blob, fileName);
        options.body = formData;
      }

      const response = await fetch(fetchUrl, options);
      if (!response.ok) {
        throw new Error(`Server returned HTTP ${response.status} ${response.statusText}`);
      }
      success = true;

    } else if (provider === 'google_drive') {
      const token = await getOrRefreshAccessToken('google_drive');
      const metadata = {
        name: fileName,
        mimeType: 'application/zip'
      };
      if (cloudSync.folderId) {
        metadata.parents = [cloudSync.folderId];
      }

      const form = new FormData();
      form.append('metadata', new Blob([JSON.stringify(metadata)], { type: 'application/json' }));
      form.append('file', new Blob([fileBuffer], { type: 'application/zip' }));

      const res = await fetch('https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart', {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${token}` },
        body: form
      });

      if (!res.ok) {
        const errText = await res.text();
        throw new Error(`Google Drive API returned HTTP ${res.status} - ${errText}`);
      }
      success = true;

    } else if (provider === 'dropbox') {
      const token = await getOrRefreshAccessToken('dropbox');
      const args = {
        path: `/OpenSave/${fileName}`,
        mode: 'overwrite',
        mute: true
      };

      const res = await fetch('https://content.dropboxapi.com/2/files/upload', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${token}`,
          'Dropbox-API-Arg': JSON.stringify(args),
          'Content-Type': 'application/octet-stream'
        },
        body: fileBuffer
      });

      if (!res.ok) {
        const errText = await res.text();
        throw new Error(`Dropbox API returned HTTP ${res.status} - ${errText}`);
      }
      success = true;

    } else if (provider === 'onedrive') {
      const token = await getOrRefreshAccessToken('onedrive');
      const uploadUrl = `https://graph.microsoft.com/v1.0/me/drive/special/approot:/${encodeURIComponent(fileName)}:/content`;

      const res = await fetch(uploadUrl, {
        method: 'PUT',
        headers: {
          'Authorization': `Bearer ${token}`,
          'Content-Type': 'application/zip'
        },
        body: fileBuffer
      });

      if (!res.ok) {
        const errText = await res.text();
        throw new Error(`OneDrive API returned HTTP ${res.status} - ${errText}`);
      }
      success = true;

    } else if (provider === 'local') {
      const { url } = cloudSync;
      if (!url) {
        throw new Error('No local folder destination configured.');
      }
      if (!fs.existsSync(url)) {
        fs.mkdirSync(url, { recursive: true });
      }
      const destPath = path.join(url, fileName);
      fs.copyFileSync(filePath, destPath);
      success = true;
    } else {
      throw new Error(`Unsupported cloud sync provider: ${provider}`);
    }

    if (success) {
      log('success', `Cloud Sync: Successfully uploaded "${fileName}" to remote ${provider}!`);
    }
  } catch (err) {
    log('error', `Cloud Sync Failed for "${fileName}" to ${provider}:`, err.message);
  }
}

/**
 * Lists all zip snapshot files from the configured cloud provider.
 * Returns an array of file items: { name, sizeBytes, createdTime, id }
 */
export async function listCloudFiles() {
  const settings = db.getSettings();
  const { cloudSync } = settings;

  if (!cloudSync || !cloudSync.enabled) {
    throw new Error('Cloud sync is not enabled.');
  }

  const { provider } = cloudSync;
  log('info', `Cloud Sync: Listing files from remote ${provider.toUpperCase()}...`);

  try {
    if (provider === 'webdav') {
      const { url, username, password } = cloudSync;
      if (!url) throw new Error('No destination URL configured.');
      
      const headers = {
        'Depth': '1',
        'Content-Type': 'text/xml'
      };
      if (username || password) {
        const credentials = Buffer.from(`${username || ''}:${password || ''}`).toString('base64');
        headers['Authorization'] = `Basic ${credentials}`;
      }

      const response = await fetch(url, {
        method: 'PROPFIND',
        headers
      });

      if (!response.ok) {
        throw new Error(`WebDAV list returned HTTP ${response.status}`);
      }

      const xmlText = await response.text();
      const files = [];
      const responseRegex = /<[^>]*response[^>]*>([\s\S]*?)<\/[^>]*response[^>]*>/gi;
      let match;
      while ((match = responseRegex.exec(xmlText)) !== null) {
        const block = match[1];
        
        const hrefMatch = block.match(/<[^>]*href[^>]*>([\s\S]*?)<\/[^>]*href[^>]*>/i);
        if (!hrefMatch) continue;
        
        const href = decodeURIComponent(hrefMatch[1].trim());
        const name = path.basename(href);
        if (!name || name === path.basename(url) || name === '') continue;

        const sizeMatch = block.match(/<[^>]*getcontentlength[^>]*>(\d+)<\/[^>]*getcontentlength[^>]*>/i);
        const sizeBytes = sizeMatch ? parseInt(sizeMatch[1], 10) : 0;

        const dateMatch = block.match(/<[^>]*getlastmodified[^>]*>([\s\S]*?)<\/[^>]*getlastmodified[^>]*>/i);
        const createdTime = dateMatch ? new Date(dateMatch[1].trim()).toISOString() : new Date().toISOString();

        files.push({ name, sizeBytes, createdTime });
      }
      return files;

    } else if (provider === 'google_drive') {
      const token = await getOrRefreshAccessToken('google_drive');
      let query = "trashed = false and mimeType = 'application/zip'";
      if (cloudSync.folderId) {
        query += ` and '${cloudSync.folderId}' in parents`;
      }
      const url = `https://www.googleapis.com/drive/v3/files?q=${encodeURIComponent(query)}&fields=files(id,name,size,createdTime)`;
      const res = await fetch(url, {
        headers: { 'Authorization': `Bearer ${token}` }
      });
      if (!res.ok) {
        const errText = await res.text();
        throw new Error(`Google Drive API returned HTTP ${res.status} - ${errText}`);
      }
      const data = await res.json();
      return (data.files || []).map(f => ({
        id: f.id,
        name: f.name,
        sizeBytes: f.size ? parseInt(f.size, 10) : 0,
        createdTime: f.createdTime
      }));

    } else if (provider === 'dropbox') {
      const token = await getOrRefreshAccessToken('dropbox');
      const res = await fetch('https://api.dropboxapi.com/2/files/list_folder', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${token}`,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({ path: '/OpenSave' })
      });
      
      if (res.status === 409) {
        return []; // Folder doesn't exist yet
      }
      
      if (!res.ok) {
        const errText = await res.text();
        throw new Error(`Dropbox API returned HTTP ${res.status} - ${errText}`);
      }
      const data = await res.json();
      return (data.entries || [])
        .filter(e => e['.tag'] === 'file' && e.name.endsWith('.zip'))
        .map(e => ({
          name: e.name,
          sizeBytes: e.size,
          createdTime: e.client_modified
        }));

    } else if (provider === 'onedrive') {
      const token = await getOrRefreshAccessToken('onedrive');
      const res = await fetch('https://graph.microsoft.com/v1.0/me/drive/special/approot/children', {
        headers: { 'Authorization': `Bearer ${token}` }
      });
      if (!res.ok) {
        const errText = await res.text();
        throw new Error(`OneDrive API returned HTTP ${res.status} - ${errText}`);
      }
      const data = await res.json();
      return (data.value || [])
        .filter(f => f.file && f.name.endsWith('.zip'))
        .map(f => ({
          name: f.name,
          sizeBytes: f.size,
          createdTime: f.createdDateTime
        }));

    } else if (provider === 'local') {
      const { url } = cloudSync;
      if (!url || !fs.existsSync(url)) {
        return [];
      }
      const files = fs.readdirSync(url);
      return files
        .filter(f => f.endsWith('.zip'))
        .map(f => {
          const stats = fs.statSync(path.join(url, f));
          return {
            name: f,
            sizeBytes: stats.size,
            createdTime: stats.mtime.toISOString()
          };
        });
        
    } else {
      return [];
    }
  } catch (err) {
    log('error', `Cloud Sync list failed for ${provider}:`, err.message);
    throw err;
  }
}

/**
 * Downloads a file from the configured cloud provider to the local file system.
 */
export async function downloadFromCloud(fileName, localPath) {
  const settings = db.getSettings();
  const { cloudSync } = settings;

  if (!cloudSync || !cloudSync.enabled) {
    throw new Error('Cloud sync is not enabled.');
  }

  const { provider } = cloudSync;
  log('info', `Cloud Sync: Downloading ${fileName} via ${provider.toUpperCase()}...`);

  try {
    let fileBuffer;

    if (provider === 'webdav') {
      const { url, username, password } = cloudSync;
      if (!url) throw new Error('No destination URL configured.');

      const baseUrl = url.endsWith('/') ? url : `${url}/`;
      const fetchUrl = `${baseUrl}${encodeURIComponent(fileName)}`;
      const headers = {};
      if (username || password) {
        const credentials = Buffer.from(`${username || ''}:${password || ''}`).toString('base64');
        headers['Authorization'] = `Basic ${credentials}`;
      }

      const res = await fetch(fetchUrl, { headers });
      if (!res.ok) {
        throw new Error(`WebDAV GET returned HTTP ${res.status} ${res.statusText}`);
      }
      fileBuffer = Buffer.from(await res.arrayBuffer());

    } else if (provider === 'google_drive') {
      const token = await getOrRefreshAccessToken('google_drive');
      // 1. List files matching this name in the folder to get Google Drive's fileId
      let query = `name = '${fileName.replace(/'/g, "\\'")}' and trashed = false`;
      if (cloudSync.folderId) {
        query += ` and '${cloudSync.folderId}' in parents`;
      }
      const listUrl = `https://www.googleapis.com/drive/v3/files?q=${encodeURIComponent(query)}&fields=files(id)`;
      const listRes = await fetch(listUrl, {
        headers: { 'Authorization': `Bearer ${token}` }
      });
      if (!listRes.ok) {
        const errText = await listRes.text();
        throw new Error(`Google Drive list query returned HTTP ${listRes.status} - ${errText}`);
      }
      const listData = await listRes.json();
      const files = listData.files || [];
      if (files.length === 0) {
        throw new Error(`File "${fileName}" not found on Google Drive.`);
      }
      const fileId = files[0].id;

      // 2. Fetch content using alt=media
      const contentRes = await fetch(`https://www.googleapis.com/drive/v3/files/${fileId}?alt=media`, {
        headers: { 'Authorization': `Bearer ${token}` }
      });
      if (!contentRes.ok) {
        const errText = await contentRes.text();
        throw new Error(`Google Drive media fetch returned HTTP ${contentRes.status} - ${errText}`);
      }
      fileBuffer = Buffer.from(await contentRes.arrayBuffer());

    } else if (provider === 'dropbox') {
      const token = await getOrRefreshAccessToken('dropbox');
      const args = { path: `/OpenSave/${fileName}` };
      const res = await fetch('https://content.dropboxapi.com/2/files/download', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${token}`,
          'Dropbox-API-Arg': JSON.stringify(args)
        }
      });
      if (!res.ok) {
        const errText = await res.text();
        throw new Error(`Dropbox download API returned HTTP ${res.status} - ${errText}`);
      }
      fileBuffer = Buffer.from(await res.arrayBuffer());

    } else if (provider === 'onedrive') {
      const token = await getOrRefreshAccessToken('onedrive');
      const downloadUrl = `https://graph.microsoft.com/v1.0/me/drive/special/approot:/${encodeURIComponent(fileName)}:/content`;
      const res = await fetch(downloadUrl, {
        headers: { 'Authorization': `Bearer ${token}` }
      });
      if (!res.ok) {
        const errText = await res.text();
        throw new Error(`OneDrive download API returned HTTP ${res.status} - ${errText}`);
      }
      fileBuffer = Buffer.from(await res.arrayBuffer());

    } else if (provider === 'local') {
      const { url } = cloudSync;
      if (!url) throw new Error('No local folder destination configured.');
      const srcPath = path.join(url, fileName);
      if (!fs.existsSync(srcPath)) {
        throw new Error(`File "${fileName}" not found in local folder.`);
      }
      fs.copyFileSync(srcPath, localPath);
      return;

    } else {
      throw new Error(`Downloading is not supported for provider: ${provider}`);
    }

    const parentDir = path.dirname(localPath);
    if (!fs.existsSync(parentDir)) {
      fs.mkdirSync(parentDir, { recursive: true });
    }
    fs.writeFileSync(localPath, fileBuffer);
    log('success', `Cloud Sync: Successfully downloaded "${fileName}" from remote ${provider}.`);

  } catch (err) {
    log('error', `Cloud download failed for "${fileName}" from ${provider}:`, err.message);
    throw err;
  }
}
