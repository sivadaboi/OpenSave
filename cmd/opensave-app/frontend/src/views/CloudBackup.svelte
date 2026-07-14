<script>
  import { gameList, toast, cloudAuthEvent, askConfirm } from '../lib/stores.js';
  import { api, native } from '../lib/api.js';
  import { onMount, onDestroy } from 'svelte';

  let config = null;
  let busy = false;
  let authCode = '';
  let authInProgress = false;
  let authAuto = false; // backend caught the redirect automatically
  let showManualCode = false;

  // The daemon broadcasts cloud-auth when the browser redirect lands on
  // its temporary localhost listener — sign-in completes with no pasting.
  const unsubAuth = cloudAuthEvent.subscribe((ev) => {
    if (!ev || !authInProgress) return;
    cloudAuthEvent.set(null);
    authInProgress = false;
    showManualCode = false;
    if (ev.success) {
      toast(`Connected as ${ev.userEmail}`, 'success');
      load();
    } else {
      toast(ev.error ?? 'Sign-in failed', 'error');
    }
  });
  onDestroy(unsubAuth);
  let cloudGames = null; // grouped explorer data (null = not loaded yet)
  let openGame = null; // gameId of the expanded group
  let browsing = false;
  let uploadGame = ''; // game selected for pushing local snapshots up

  const providers = [
    { id: 'google_drive', label: 'Google Drive', oauth: true, img: 'cloud/googledrive.png' },
    { id: 'onedrive', label: 'OneDrive', oauth: true, img: 'cloud/onedrive.png' },
    { id: 'dropbox', label: 'Dropbox', oauth: true, img: 'cloud/dropbox.png' },
    { id: 'local', label: 'Local Folder', oauth: false, icon: 'folder' },
    { id: 'webdav', label: 'WebDAV Server', oauth: false, icon: 'cloud' },
    { id: 'webhook', label: 'HTTP Webhook', oauth: false, icon: 'webhook' }
  ];

  const iconPaths = {
    folder: 'M20 6h-8l-2-2H4c-1.1 0-1.99.9-1.99 2L2 18c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2z',
    cloud: 'M19.35 10.04C18.67 6.59 15.64 4 12 4 9.11 4 6.6 5.64 5.35 8.04 2.34 8.36 0 10.91 0 14c0 3.31 2.69 6 6 6h13c2.76 0 5-2.24 5-5 0-2.64-2.05-4.78-4.65-4.96z',
    webhook: 'M20 4H4c-1.1 0-1.99.9-1.99 2L2 18c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 14H4V8h16v10zM12 10H8v2h4v-2zm4 4h-8v2h8v-2z'
  };

  // The provider the stored OAuth tokens belong to (server truth at load
  // time) — independent of which card is currently selected, so the
  // connected card keeps saying "connected" while you browse the others.
  let connectedProvider = null;

  // Some providers can't reveal the account email (privacy settings / missing
  // scope) — the daemon stores a placeholder then. Only display it if it
  // actually looks like an address.
  const isEmail = (s) => /\S+@\S+\.\S+/.test(s ?? '');

  // Pure function of its arguments so the template call re-renders whenever
  // connectedProvider/config change (a closure over `config` would go stale).
  function providerStatus(id, connected, cfg) {
    if (!cfg) return '';
    if (id === connected) {
      const email = cfg.tokens?.userEmail;
      return isEmail(email) ? email : 'Connected';
    }
    if (['google_drive', 'onedrive', 'dropbox'].includes(id)) return 'Click to sign in';
    if (id === cfg.provider && cfg.url) return 'Configured';
    return 'Not configured';
  }

  onMount(load);

  async function load() {
    try {
      const settings = await api.get('/api/settings');
      config = settings.cloudSync ?? {
        enabled: false, provider: 'local', url: '', username: '', password: '', headers: '{}', folderId: ''
      };
      connectedProvider = config.tokens?.userEmail ? config.provider : null;
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  async function save() {
    busy = true;
    try {
      await api.post('/api/settings', { cloudSync: config });
      toast('Cloud settings saved', 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function startAuth() {
    busy = true;
    try {
      const res = await api.post('/api/auth/start', { provider: config.provider });
      native.openExternal(res.authUrl);
      authInProgress = true;
      authAuto = !!res.autoCallback;
      showManualCode = !authAuto;
      if (authAuto) {
        toast('Finish signing in — OpenSave connects automatically.');
      } else {
        toast('Sign in using the browser window, then paste the code from the redirect URL here.');
      }
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      busy = false;
    }
  }

  function cancelAuth() {
    authInProgress = false;
    showManualCode = false;
    authCode = '';
  }

  async function finishAuth() {
    busy = true;
    try {
      const res = await api.post('/api/auth/callback', { code: authCode.trim() });
      toast(`Connected as ${res.userEmail}`, 'success');
      authInProgress = false;
      showManualCode = false;
      authCode = '';
      await load();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function disconnect() {
    busy = true;
    try {
      await api.post('/api/auth/disconnect');
      toast('Disconnected');
      await load();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function pickLocalFolder() {
    const dir = await native.selectDirectory('Select backup destination folder');
    if (dir) config.url = dir;
  }

  // A cloud call that fails on expired credentials means the daemon has
  // just wiped the dead tokens — reload so the "connected" badge flips to
  // "sign in" instead of lying about a working connection.
  function handleCloudError(e) {
    toast(e.message, 'error');
    if (/expired|reconnect|not authenticated|re-auth/i.test(e.message)) load();
  }

  async function browseCloud() {
    browsing = true;
    cloudGames = null;
    try {
      cloudGames = await api.get('/api/cloud/browse');
    } catch (e) {
      handleCloudError(e);
    } finally {
      browsing = false;
    }
  }

  const toggleGame = (id) => (openGame = openGame === id ? null : id);

  const restoreCloud = async (gameId, file) => {
    if (!(await askConfirm(`Restore ${file.snapshotId} from the cloud over your current save?`, { title: 'Restore from cloud?', confirmText: 'Restore' }))) return;
    busy = true;
    try {
      await api.post(`/api/cloud/restore/${gameId}`, { fileName: file.name });
      toast('Restored from cloud', 'success');
    } catch (e) {
      handleCloudError(e);
    } finally {
      busy = false;
    }
  };

  const uploadLocal = async (gameId) => {
    busy = true;
    try {
      const res = await api.post(`/api/cloud/sync-local/${gameId}`);
      toast(`Uploaded ${res.uploaded}, skipped ${res.skipped}`, 'success');
      await browseCloud();
      openGame = gameId;
    } catch (e) {
      handleCloudError(e);
    } finally {
      busy = false;
    }
  };

  // ── .sscb export/import ──────────────────────────────────────────
  const exportBackup = async () => {
    const target = await native.selectSaveFile('Export all snapshots', 'opensave-backup.sscb');
    if (!target) return;
    busy = true;
    try {
      const res = await api.post('/api/backup/export', { targetPath: target });
      toast(`Exported ${res.snapshotCount} snapshot(s)`, 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      busy = false;
    }
  };

  const importBackup = async () => {
    const src = await native.selectFile('Select an .sscb backup to import');
    if (!src) return;
    busy = true;
    try {
      const res = await api.post('/api/backup/restore', { sourcePath: src });
      toast(`Imported ${res.imported}, skipped ${res.skipped}`, 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      busy = false;
    }
  };

  $: currentProvider = providers.find((p) => p.id === config?.provider);
  const fmtSize = (n) => (n >= 1048576 ? (n / 1048576).toFixed(1) + ' MB' : (n / 1024).toFixed(1) + ' KB');
</script>

<div class="head">
  <h2 class="page-title">Cloud Backup</h2>
</div>

{#if !config}
  <p class="quiet">Loading…</p>
{:else}
  <div class="card">
    <div class="enable-row">
      <div>
        <h3>Mirror snapshots to the cloud</h3>
        <p class="quiet">Every new snapshot uploads automatically in the background.</p>
      </div>
      <label class="switch">
        <input type="checkbox" bind:checked={config.enabled} on:change={save} />
        <span></span>
      </label>
    </div>

    <div class="provider-label">Select cloud storage provider</div>
    <div class="provider-grid">
      {#each providers as p}
        <button
          class="provider-card"
          class:active={config.provider === p.id}
          on:click={() => { config.provider = p.id; cloudGames = null; openGame = null; }}
        >
          {#if p.id === connectedProvider}
            <span class="prov-check" title="Connected">✓</span>
          {/if}
          <div class="provider-icon">
            {#if p.img}
              <img src={p.img} alt={p.label} />
            {:else}
              <svg viewBox="0 0 24 24" width="34" height="34" fill="currentColor"><path d={iconPaths[p.icon]} /></svg>
            {/if}
          </div>
          <div class="provider-name">{p.label}</div>
          <div class="provider-status" class:is-connected={p.id === connectedProvider}>
            {providerStatus(p.id, connectedProvider, config)}
          </div>
        </button>
      {/each}
    </div>

    {#if config.provider === 'local'}
      <div class="field">
        <label for="cb-folder">Destination folder (e.g. a NAS mount)</label>
        <div class="path-row">
          <input id="cb-folder" bind:value={config.url} placeholder="D:\Backups\OpenSave" />
          <button class="btn" on:click={pickLocalFolder}>Browse</button>
        </div>
      </div>
    {:else if config.provider === 'webdav'}
      <div class="field">
        <label for="cb-url">WebDAV URL</label>
        <input id="cb-url" bind:value={config.url} placeholder="https://nas.local/dav/opensave/" />
      </div>
      <div class="two">
        <div class="field">
          <label for="cb-user">Username</label>
          <input id="cb-user" bind:value={config.username} />
        </div>
        <div class="field">
          <label for="cb-pass">Password</label>
          <input id="cb-pass" type="password" bind:value={config.password} />
        </div>
      </div>
    {:else if config.provider === 'webhook'}
      <div class="field">
        <label for="cb-hook">Webhook URL (receives multipart POST)</label>
        <input id="cb-hook" bind:value={config.url} />
      </div>
      <div class="field">
        <label for="cb-headers">Custom headers (JSON)</label>
        <input id="cb-headers" bind:value={config.headers} placeholder={'{"Authorization": "Bearer …"}'} />
      </div>
    {:else}
      <!-- OAuth providers -->
      {#if connectedProvider === config.provider && config.tokens?.userEmail}
        <div class="acct">
          <div class="acct-icon">
            {#if currentProvider?.img}
              <img src={currentProvider.img} alt="" />
            {:else}
              <svg viewBox="0 0 24 24" width="24" height="24" fill="currentColor"><path d={iconPaths.cloud} /></svg>
            {/if}
          </div>
          <div class="acct-info">
            <div class="acct-title">Connected to {currentProvider?.label}</div>
            <div class="acct-sub">
              <span class="acct-dot"></span>
              {isEmail(config.tokens.userEmail)
                ? config.tokens.userEmail
                : 'Signed in — new snapshots upload automatically'}
            </div>
          </div>
          <button class="btn small danger" disabled={busy} on:click={disconnect}>Disconnect</button>
        </div>
      {:else}
        {#if connectedProvider}
          <p class="quiet" style="margin-bottom: 10px;">
            You're currently connected to {providers.find((x) => x.id === connectedProvider)?.label} — signing in
            here will replace that connection.
          </p>
        {/if}
        {#if !authInProgress}
          <button class="btn primary" disabled={busy} on:click={startAuth}>
            Sign in with {currentProvider?.label}
          </button>
          {#if config.provider === 'google_drive'}
            <p class="quiet" style="margin-top: 10px;">
              ⚠️ On Google's consent screen, <strong>tick the checkbox</strong> allowing OpenSave to access
              its own Drive files — without it, uploads fail with "insufficient permissions".
            </p>
          {/if}
        {:else}
          <div class="auth-waiting">
            <span class="cspin"></span>
            <div class="auth-waiting-text">
              <strong>Waiting for you to finish signing in…</strong>
              <span class="quiet">Approve access in your browser — OpenSave connects by itself.</span>
            </div>
            <button class="btn small" on:click={cancelAuth}>Cancel</button>
          </div>
          {#if !showManualCode && authAuto}
            <button class="linkish" on:click={() => (showManualCode = true)}>
              Having trouble? Paste the code manually
            </button>
          {/if}
          {#if showManualCode}
            <div class="auth-code">
              <p class="quiet">
                After approving access, the browser lands on a localhost page. Copy the <code>code</code> value
                from its address bar and paste it here:
              </p>
              <div class="path-row">
                <input placeholder="4/0AY0e-g7…" bind:value={authCode} />
                <button class="btn primary" disabled={!authCode || busy} on:click={finishAuth}>Connect</button>
              </div>
            </div>
          {/if}
        {/if}
      {/if}
      {#if config.provider === 'google_drive'}
        <div class="field" style="margin-top: 14px;">
          <label for="cb-folderid">Drive folder ID (optional)</label>
          <input id="cb-folderid" bind:value={config.folderId} />
        </div>
      {/if}

      <div class="oauth-config">
        <div class="oauth-config-head">🛠️ Custom OAuth Client ID <span class="optional">(optional)</span></div>
        <p class="oauth-config-note">
          Google Drive and Dropbox include built-in credentials — sign in with zero setup. For OneDrive, or to use
          your own registered app, enter a Client ID. Takes effect on next sign-in.
        </p>
        <input
          placeholder="Leave blank to use built-in credentials"
          value={config.customClientIds?.[config.provider] ?? ''}
          on:input={(e) => {
            config.customClientIds = { ...(config.customClientIds ?? {}), [config.provider]: e.currentTarget.value };
          }}
        />
      </div>
    {/if}

    <div class="actions">
      <button class="btn primary" disabled={busy} on:click={save}>Save settings</button>
    </div>
  </div>

  <div class="section-head">
    <h3 class="section">Browse cloud snapshots</h3>
    <button class="btn small" disabled={browsing} on:click={browseCloud}>
      {browsing ? 'Loading…' : cloudGames ? 'Refresh' : 'Browse cloud'}
    </button>
  </div>
  <div class="card">
    <!-- Push a game's local snapshots up (games with nothing in the cloud
         yet won't appear in the explorer below until they're uploaded). -->
    <div class="browse-row">
      <select bind:value={uploadGame}>
        <option value="">Upload a game's local snapshots…</option>
        {#each $gameList as g}
          <option value={g.id}>{g.name}</option>
        {/each}
      </select>
      <button class="btn" disabled={!uploadGame || busy} on:click={() => uploadLocal(uploadGame)}>
        Upload to cloud
      </button>
    </div>

    {#if browsing && !cloudGames}
      <p class="quiet" style="margin-top: 14px;">Reading cloud storage…</p>
    {:else if cloudGames && cloudGames.length === 0}
      <p class="quiet" style="margin-top: 14px;">No snapshots in the cloud yet. Upload a game above to get started.</p>
    {:else if cloudGames}
      <div class="explorer">
        {#each cloudGames as g (g.gameId)}
          <div class="game-group" class:open={openGame === g.gameId}>
            <button class="group-head" on:click={() => toggleGame(g.gameId)}>
              <svg class="chev" viewBox="0 0 24 24" width="16" height="16" fill="currentColor">
                <path d="M8.59 16.59L13.17 12 8.59 7.41 10 6l6 6-6 6z" />
              </svg>
              <span class="group-name">{g.gameName}</span>
              <span class="group-meta">{g.count} snapshot{g.count === 1 ? '' : 's'} · {fmtSize(g.totalSize)}</span>
            </button>
            {#if openGame === g.gameId}
              <div class="cloud-list">
                {#each g.snapshots as f (f.name)}
                  <div class="cloud-row">
                    <div class="cloud-info">
                      <div class="cloud-name">{f.snapshotId} <span class="badge offline">{f.branch}</span></div>
                      <div class="cloud-meta">{fmtSize(f.sizeBytes)} · {new Date(f.createdTime).toLocaleString()}</div>
                    </div>
                    <button class="btn small primary" disabled={busy} on:click={() => restoreCloud(g.gameId, f)}>
                      Restore
                    </button>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {:else}
      <p class="quiet" style="margin-top: 14px;">Click <strong>Browse cloud</strong> to see every snapshot stored in the cloud, grouped by game.</p>
    {/if}
  </div>

  <h3 class="section">Full backup file</h3>
  <div class="card export-row">
    <div>
      <h3>Export / import everything</h3>
      <p class="quiet">A single compressed .sscb file with every snapshot — for moving to a new PC.</p>
    </div>
    <div class="export-actions">
      <button class="btn" disabled={busy} on:click={importBackup}>Import .sscb</button>
      <button class="btn primary" disabled={busy} on:click={exportBackup}>Export all</button>
    </div>
  </div>
{/if}

<style>
  .head {
    margin-bottom: 20px;
  }
  .enable-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 16px;
  }
  .quiet {
    color: var(--text-faint);
    font-size: 0.85rem;
  }
  .switch {
    position: relative;
    width: 44px;
    height: 24px;
    flex-shrink: 0;
  }
  .switch input {
    opacity: 0;
    width: 0;
    height: 0;
  }
  .switch span {
    position: absolute;
    inset: 0;
    background: var(--bg-active);
    border-radius: 999px;
    cursor: pointer;
    transition: background 0.15s;
  }
  .switch span::before {
    content: '';
    position: absolute;
    width: 18px;
    height: 18px;
    left: 3px;
    top: 3px;
    background: var(--text-dim);
    border-radius: 50%;
    transition: transform 0.15s, background 0.15s;
  }
  .switch input:checked + span {
    background: var(--accent);
  }
  .switch input:checked + span::before {
    transform: translateX(20px);
    background: #fff;
  }
  .provider-label {
    display: block;
    font-size: 0.85rem;
    color: var(--text-dim);
    font-weight: 600;
    margin: 18px 0 10px;
  }
  .provider-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    gap: 10px;
    margin-bottom: 18px;
  }
  .provider-card {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 7px;
    padding: 16px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    color: var(--text-dim);
    cursor: pointer;
    transition: border-color 0.12s, background 0.12s, transform 0.12s;
    text-align: center;
  }
  .provider-card:hover {
    border-color: var(--border-strong);
    transform: translateY(-1px);
  }
  .provider-card.active {
    border-color: var(--accent);
    background: var(--accent-soft);
  }
  .provider-icon {
    width: 40px;
    height: 40px;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .provider-icon img {
    width: 34px;
    height: 34px;
    object-fit: contain;
  }
  .provider-name {
    font-weight: 600;
    font-size: 0.9rem;
    color: var(--text);
  }
  .provider-status {
    font-size: 0.72rem;
    color: var(--text-faint);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 130px;
  }
  .provider-card.active .provider-status {
    color: var(--accent);
  }
  .provider-status.is-connected,
  .provider-card.active .provider-status.is-connected {
    color: var(--success);
    font-weight: 600;
  }
  .prov-check {
    position: absolute;
    top: 8px;
    right: 8px;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    background: var(--success);
    color: #0c0c0d;
    font-size: 0.72rem;
    font-weight: 800;
    display: flex;
    align-items: center;
    justify-content: center;
    box-shadow: 0 2px 6px rgba(0, 0, 0, 0.4);
  }
  .oauth-config {
    margin-top: 16px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 14px 16px;
    background: rgba(255, 255, 255, 0.01);
  }
  .oauth-config-head {
    font-weight: 600;
    font-size: 0.85rem;
    margin-bottom: 6px;
  }
  .oauth-config .optional {
    font-weight: 400;
    color: var(--text-faint);
    font-size: 0.75rem;
  }
  .oauth-config-note {
    font-size: 0.76rem;
    color: var(--text-faint);
    line-height: 1.5;
    margin-bottom: 10px;
  }
  .oauth-config input {
    width: 100%;
    padding: 8px 12px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    color: var(--text);
    outline: none;
  }
  .two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  .path-row {
    display: flex;
    gap: 8px;
  }
  .path-row input {
    flex: 1;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    margin-top: 8px;
  }
  .acct {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 16px 18px;
    background: rgba(74, 222, 128, 0.05);
    border: 1px solid rgba(74, 222, 128, 0.3);
    border-radius: var(--radius-lg);
  }
  .acct-icon {
    width: 44px;
    height: 44px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 11px;
    color: var(--text-dim);
    flex-shrink: 0;
  }
  .acct-icon img {
    width: 26px;
    height: 26px;
    object-fit: contain;
  }
  .acct-info {
    flex: 1;
    min-width: 0;
  }
  .acct-title {
    font-weight: 600;
    font-size: 0.98rem;
  }
  .acct-sub {
    display: flex;
    align-items: center;
    gap: 7px;
    color: var(--text-dim);
    font-size: 0.82rem;
    margin-top: 3px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .acct-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--success);
    box-shadow: 0 0 6px rgba(74, 222, 128, 0.6);
    flex-shrink: 0;
  }
  .auth-code {
    margin-top: 12px;
  }
  .auth-waiting {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 16px;
    background: var(--accent-soft);
    border: 1px solid var(--accent);
    border-radius: var(--radius);
  }
  .auth-waiting-text {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: 0.88rem;
  }
  .cspin {
    width: 16px;
    height: 16px;
    border: 2px solid var(--accent-soft);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: cspin 0.8s linear infinite;
    flex-shrink: 0;
  }
  @keyframes cspin {
    to { transform: rotate(360deg); }
  }
  .linkish {
    margin-top: 10px;
    border: none;
    background: transparent;
    color: var(--text-faint);
    font-size: 0.8rem;
    cursor: pointer;
    text-decoration: underline;
    padding: 2px 0;
  }
  .linkish:hover {
    color: var(--text-dim);
  }
  .auth-code code {
    background: var(--bg);
    padding: 1px 5px;
    border-radius: 4px;
  }
  .section {
    margin: 22px 0 10px;
  }
  .section-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin: 22px 0 10px;
  }
  .section-head .section {
    margin: 0;
  }
  .explorer {
    margin-top: 14px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .game-group {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--bg);
    overflow: hidden;
  }
  .game-group.open {
    border-color: var(--border-strong);
  }
  .group-head {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 12px 14px;
    background: transparent;
    border: none;
    color: var(--text);
    cursor: pointer;
    text-align: left;
    transition: background 0.12s;
  }
  .group-head:hover {
    background: var(--bg-active);
  }
  .chev {
    color: var(--text-faint);
    flex-shrink: 0;
    transition: transform 0.15s;
  }
  .game-group.open .chev {
    transform: rotate(90deg);
  }
  .group-name {
    font-weight: 600;
    font-size: 0.92rem;
    flex: 1;
  }
  .group-meta {
    font-size: 0.76rem;
    color: var(--text-faint);
    white-space: nowrap;
  }
  .browse-row {
    display: flex;
    gap: 10px;
  }
  .browse-row select {
    flex: 1;
    padding: 9px 12px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    color: var(--text);
    outline: none;
  }
  .cloud-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 0 12px 12px;
  }
  .cloud-row {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .cloud-info {
    flex: 1;
  }
  .cloud-name {
    font-weight: 600;
    font-size: 0.9rem;
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .cloud-meta {
    font-size: 0.76rem;
    color: var(--text-faint);
  }
  .export-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 16px;
  }
  .export-actions {
    display: flex;
    gap: 8px;
  }
</style>
