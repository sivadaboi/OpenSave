<script>
  import { gameList, toast, cloudAuthEvent, cloudUploadEvent, backupProgressEvent, askConfirm } from '../lib/stores.js';
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
  let browsing = false;
  let cloudOpen = false; // cloud snapshot browser modal
  let cloudFilter = '';
  let cloudTab = 'all'; // all | cloud | local
  let detailId = null; // gameId drilled into (null = tile grid)
  let uploading = false; // an upload-to-cloud is running (independent of busy
  // so Restore/Delete don't gray out while snapshots are being pushed up)
  let uploadProg = null; // live {done, total, current} from the daemon

  const unsubUpload = cloudUploadEvent.subscribe((ev) => {
    if (!ev) return;
    uploadProg = ev.complete ? null : ev;
  });
  onDestroy(unsubUpload);

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
      // OAuth tokens survive a provider switch (so switching back reconnects
      // instantly) — but only the OAuth provider they belong to is
      // "connected"; never badge local/webdav/webhook off someone's tokens.
      connectedProvider =
        config.tokens?.userEmail && ['google_drive', 'onedrive', 'dropbox'].includes(config.provider)
          ? config.provider
          : null;
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
    // keep the current listing visible while refreshing so the detail view
    // doesn't flash back to the loading spinner mid-upload
    try {
      cloudGames = await api.get('/api/cloud/browse');
    } catch (e) {
      handleCloudError(e);
    } finally {
      browsing = false;
    }
  }

  function openCloudBrowser() {
    cloudOpen = true;
    cloudFilter = '';
    cloudTab = 'all';
    detailId = null;
    browseCloud();
  }
  const closeCloudBrowser = () => {
    cloudOpen = false;
    detailId = null;
  };

  // One tile per game — tracked games and cloud-only games merged, so the
  // grid can both browse what's up there and upload what isn't yet.
  $: cloudMap = new Map((cloudGames ?? []).map((g) => [g.gameId, g]));
  $: tiles = [
    ...(cloudGames ?? []).map((g) => {
      const local = $gameList.find((x) => x.id === g.gameId);
      return { id: g.gameId, name: g.gameName, coverUrl: local?.coverUrl, tracked: !!local, cloud: g };
    }),
    ...$gameList
      .filter((lg) => !cloudMap.has(lg.id))
      .map((lg) => ({ id: lg.id, name: lg.name, coverUrl: lg.coverUrl, tracked: true, cloud: null }))
  ];
  $: tabCounts = {
    all: tiles.length,
    cloud: tiles.filter((t) => t.cloud).length,
    local: tiles.filter((t) => !t.cloud).length
  };
  $: filteredTiles = tiles.filter(
    (t) =>
      (cloudTab === 'all' || (cloudTab === 'cloud' ? !!t.cloud : !t.cloud)) &&
      t.name.toLowerCase().includes(cloudFilter.trim().toLowerCase())
  );
  $: detailTile = detailId ? tiles.find((t) => t.id === detailId) : null;

  // Delete only removes the remote copy — local snapshots stay put.
  const deleteCloud = async (g, f) => {
    if (
      !(await askConfirm(
        `Delete ${f.snapshotId} of "${g.gameName}" from the cloud? Snapshots stored on your devices are not affected.`,
        { title: 'Delete cloud snapshot?', confirmText: 'Delete', danger: true }
      ))
    )
      return;
    busy = true;
    try {
      await api.post(`/api/cloud/delete/${g.gameId}`, { fileName: f.name, id: f.id ?? '' });
      g.snapshots = g.snapshots.filter((x) => x.name !== f.name);
      g.count = g.snapshots.length;
      g.totalSize = g.snapshots.reduce((n, x) => n + x.sizeBytes, 0);
      cloudGames = cloudGames.filter((x) => x.count > 0);
      toast('Deleted from cloud', 'success');
    } catch (e) {
      handleCloudError(e);
    } finally {
      busy = false;
    }
  };

  $: cloudTotals = (cloudGames ?? []).reduce(
    (a, g) => ({ snaps: a.snaps + g.count, size: a.size + g.totalSize }),
    { snaps: 0, size: 0 }
  );

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
    uploading = true;
    try {
      const res = await api.post(`/api/cloud/sync-local/${gameId}`);
      toast(
        res.uploaded === 0 && res.skipped > 0
          ? `Everything already in the cloud (${res.skipped} skipped)`
          : `Uploaded ${res.uploaded}, skipped ${res.skipped}`,
        'success'
      );
      await browseCloud(); // detailTile re-derives from the fresh listing
    } catch (e) {
      handleCloudError(e);
    } finally {
      uploading = false;
      uploadProg = null;
    }
  };

  // ── .sscb export/import ──────────────────────────────────────────
  // Export: a picker modal lists tracked games plus auto-detected saves;
  // the selection's LIVE saves (with their locations) go into the file.
  let exportOpen = false;
  let exportItems = null; // [{id,name,savePath,appId,cover,tracked}], null while loading
  let exportSel = {};
  let exporting = false;

  // Small cover art next to each row — same Steam box-art the auto-scan
  // grid uses, with the tracked game's own coverUrl taking precedence.
  const portraitUrl = (appId) =>
    `https://cdn.cloudflare.steamstatic.com/steam/apps/${appId}/library_600x900.jpg`;

  const openExportPicker = async () => {
    exportOpen = true;
    exportItems = null;
    exportSel = {};
    try {
      const [games, scan] = await Promise.all([api.get('/api/games'), api.get('/api/presets/scan')]);
      const tracked = Object.values(games ?? {}).map((g) => ({
        id: g.id, name: g.name, savePath: g.savePath, appId: g.appId,
        cover: g.coverUrl || (g.appId ? portraitUrl(g.appId) : ''), tracked: true,
      }));
      const knownPaths = new Set(tracked.map((g) => g.savePath.toLowerCase()));
      const knownIds = new Set(tracked.map((g) => g.id));
      const detected = (scan ?? [])
        .filter((d) => !knownPaths.has(d.savePath.toLowerCase()) && !knownIds.has(d.id))
        .map((d) => ({
          id: d.id, name: d.name, savePath: d.savePath, appId: d.appId,
          cover: d.appId ? portraitUrl(d.appId) : '', tracked: false,
        }));
      for (const g of tracked) exportSel[g.id] = true; // tracked pre-selected
      exportItems = [...tracked, ...detected];
    } catch (e) {
      toast(e.message, 'error');
      exportOpen = false;
    }
  };

  const exportSetAll = (value, trackedOnly = false) => {
    for (const it of exportItems ?? []) {
      exportSel[it.id] = trackedOnly ? value && it.tracked : value;
    }
    exportSel = exportSel;
  };

  $: exportCount = exportItems ? exportItems.filter((it) => exportSel[it.id]).length : 0;
  $: allSelected = !!exportItems && exportItems.length > 0 && exportCount === exportItems.length;

  const runExport = async () => {
    const chosen = (exportItems ?? []).filter((it) => exportSel[it.id]);
    if (!chosen.length) return;
    const target = await native.selectSaveFile('Export selected saves', 'opensave-saves.sscb');
    if (!target) return;
    exporting = true;
    try {
      const res = await api.post('/api/backup/export', {
        targetPath: target,
        games: chosen.map(({ id, name, appId, savePath }) => ({ id, name, appId, savePath })),
      });
      const skipped = res.skipped?.length ?? 0;
      toast(
        `Exported ${res.exported} save${res.exported === 1 ? '' : 's'}${skipped ? `, ${skipped} skipped — see Activity` : ''}`,
        skipped ? 'info' : 'success'
      );
      exportOpen = false;
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      exporting = false;
    }
  };

  // Import: mode dialog first. "snapshots" (default) never touches live
  // files; "overwrite" restores every save in the file onto disk — the
  // backend takes safety copies of anything it replaces.
  let importOpen = false;
  let importSrc = '';
  let importMode = 'snapshots';
  let importing = false;

  const pickImportFile = async () => {
    const src = await native.selectBackupFile('Select an .sscb backup to import');
    if (!src) return;
    importSrc = src;
    importMode = 'snapshots';
    importOpen = true;
  };

  // Live per-game progress while the daemon walks the export/import.
  let backupProg = null;
  const unsubBackupProg = backupProgressEvent.subscribe((ev) => {
    backupProg = ev && !ev.complete ? ev : null;
  });
  onDestroy(unsubBackupProg);

  const runImport = async () => {
    importing = true;
    try {
      const res = await api.post('/api/backup/restore', { sourcePath: importSrc, mode: importMode });
      if (res.legacy) {
        toast(`Imported ${res.imported} snapshot(s), skipped ${res.skipped}`, 'success');
      } else {
        const bits = [];
        if (res.restored) bits.push(`${res.restored} restored`);
        if (res.snapshots) bits.push(`${res.snapshots} added to snapshots`);
        if (res.skipped) bits.push(`${res.skipped} skipped`);
        toast(`Import finished: ${bits.join(', ') || 'nothing imported'} — details in Activity`, res.skipped ? 'info' : 'success');
      }
      importOpen = false;
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      importing = false;
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
    <div class="provider-label" style="margin-top: 0;">Select cloud storage provider</div>
    <div class="provider-grid">
      {#each providers as p}
        <button
          class="provider-card"
          class:active={config.provider === p.id}
          on:click={() => { config.provider = p.id; cloudGames = null; detailId = null; }}
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
    {/if}

    <div class="actions">
      <span class="quiet" style="margin-right: auto;">
        Automatic mirroring, Drive folder ID, and custom OAuth client IDs live in
        <strong>Settings → Sync</strong>.
      </span>
      <button class="btn primary" disabled={busy} on:click={save}>Save settings</button>
    </div>
  </div>

  <h3 class="section">Cloud snapshots</h3>
  <div class="card export-row">
    <div>
      <h3>Browse your cloud library</h3>
      <p class="quiet">
        Every game with snapshots in the cloud, as cover-art tiles — upload, restore, or delete per game.
      </p>
    </div>
    <button class="btn primary" on:click={openCloudBrowser}>☁️ Browse cloud</button>
  </div>

  <h3 class="section">Backup file (.sscb)</h3>
  <div class="card export-row">
    <div>
      <h3>Export / import saves</h3>
      <p class="quiet">
        Pick any saves on this machine — tracked or just detected — and export them with their
        locations into one file. Import adds them to snapshots, or fully restores them onto disk.
      </p>
    </div>
    <div class="export-actions">
      <button class="btn" disabled={busy} on:click={pickImportFile}>Import .sscb</button>
      <button class="btn primary" disabled={busy} on:click={openExportPicker}>Export saves…</button>
    </div>
  </div>
{/if}

{#if exportOpen}
  <div
    class="cloud-overlay"
    on:click|self={() => (exportOpen = false)}
    on:keydown={(e) => e.key === 'Escape' && (exportOpen = false)}
    role="presentation"
  >
    <div class="cloud-modal">
      <div class="cloud-modal-head">
        <div>
          <h2>📦 Export saves</h2>
          <p class="cloud-modal-sub">
            {#if !exportItems}
              Looking for saves on this machine…
            {:else}
              {exportCount} of {exportItems.length} selected — each game's current save is exported
              along with where it belongs
            {/if}
          </p>
        </div>
        <div class="cloud-head-actions">
          <button class="btn icon" on:click={() => (exportOpen = false)} title="Close">✕</button>
        </div>
      </div>

      {#if !exportItems}
        <div class="cloud-loading"><span class="cspin"></span> Listing tracked games and scanning for saves…</div>
      {:else}
        <div class="export-toolbar">
          <button class="btn small" on:click={() => exportSetAll(!allSelected)}>
            {allSelected ? 'Unselect all' : 'Select all'}
          </button>
          <button class="btn small" on:click={() => { exportSetAll(false); exportSetAll(true, true); }}>Tracked only</button>
        </div>
        <div class="cloud-modal-list">
          {#if exportItems.length === 0}
            <div class="cloud-empty">
              <div class="cloud-empty-icon">📦</div>
              <p>No saves found to export.</p>
            </div>
          {:else}
            {#each exportItems as it (it.id)}
              <label class="export-item">
                <input type="checkbox" bind:checked={exportSel[it.id]} />
                <div class="export-cover">
                  {#if it.cover}
                    <img src={it.cover} alt="" loading="lazy" on:error={(e) => (e.currentTarget.style.display = 'none')} />
                  {/if}
                  <span class="export-cover-fallback">🎮</span>
                </div>
                <div class="cloud-info">
                  <div class="cloud-name">
                    {it.name}
                    {#if it.tracked}<span class="badge online">tracked</span>{:else}<span class="badge offline">detected</span>{/if}
                  </div>
                  <div class="cloud-meta"><code>{it.savePath}</code></div>
                </div>
              </label>
            {/each}
          {/if}
        </div>
        {#if exporting && backupProg}
          <div class="upload-progress">
            <div class="upload-progress-text">
              <span class="cspin"></span>
              Exporting {Math.min(backupProg.done + 1, backupProg.total)} of {backupProg.total}
              {#if backupProg.current}&nbsp;— <code>{backupProg.current}</code>{/if}
            </div>
            <div class="upload-bar">
              <div class="upload-bar-fill" style="width: {backupProg.total > 0 ? Math.round((backupProg.done / backupProg.total) * 100) : 8}%"></div>
            </div>
          </div>
        {/if}
        <div class="cloud-modal-foot">
          <span class="quiet">Detected saves export fine without being tracked.</span>
          <div class="export-foot-actions">
            <button class="btn" on:click={() => (exportOpen = false)}>Cancel</button>
            <button class="btn primary" disabled={exporting || exportCount === 0} on:click={runExport}>
              {exporting ? 'Exporting…' : `Export ${exportCount} save${exportCount === 1 ? '' : 's'}`}
            </button>
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}

{#if importOpen}
  <div
    class="cloud-overlay"
    on:click|self={() => !importing && (importOpen = false)}
    on:keydown={(e) => e.key === 'Escape' && !importing && (importOpen = false)}
    role="presentation"
  >
    <div class="cloud-modal import-modal">
      <div class="cloud-modal-head">
        <div>
          <h2>📥 Import backup</h2>
          <p class="cloud-modal-sub"><code>{importSrc}</code></p>
        </div>
        <div class="cloud-head-actions">
          <button class="btn icon" disabled={importing} on:click={() => (importOpen = false)} title="Close">✕</button>
        </div>
      </div>

      <div class="import-modes">
        <label class="mode-option" class:selected={importMode === 'snapshots'}>
          <input type="radio" bind:group={importMode} value="snapshots" />
          <div>
            <div class="mode-title">Add to snapshots <span class="badge online">recommended</span></div>
            <p class="quiet">
              Each save in the file is added to its game's snapshot history. Nothing on disk
              changes — restore individual games whenever you choose. Games not tracked on this
              machine are reported in Activity and skipped.
            </p>
          </div>
        </label>
        <label class="mode-option danger-option" class:selected={importMode === 'overwrite'}>
          <input type="radio" bind:group={importMode} value="overwrite" />
          <div>
            <div class="mode-title">Overwrite current saves</div>
            <p class="quiet">
              Every save in the file is written to its location on this machine — tracked games to
              their tracked folder, others to the path recorded in the backup. A safety copy of
              whatever is there now is taken first. Check Activity afterwards for exactly what was
              restored where.
            </p>
          </div>
        </label>
      </div>

      {#if importing && backupProg}
        <div class="upload-progress">
          <div class="upload-progress-text">
            <span class="cspin"></span>
            Importing {Math.min(backupProg.done + 1, backupProg.total)} of {backupProg.total}
            {#if backupProg.current}&nbsp;— <code>{backupProg.current}</code>{/if}
          </div>
          <div class="upload-bar">
            <div class="upload-bar-fill" style="width: {backupProg.total > 0 ? Math.round((backupProg.done / backupProg.total) * 100) : 8}%"></div>
          </div>
        </div>
      {/if}
      <div class="cloud-modal-foot">
        <span class="quiet">Nothing is ever overwritten without a safety copy.</span>
        <div class="export-foot-actions">
          <button class="btn" disabled={importing} on:click={() => (importOpen = false)}>Cancel</button>
          <button
            class="btn {importMode === 'overwrite' ? 'danger' : 'primary'}"
            disabled={importing}
            on:click={runImport}
          >
            {importing ? 'Importing…' : importMode === 'overwrite' ? 'Overwrite saves' : 'Add to snapshots'}
          </button>
        </div>
      </div>
    </div>
  </div>
{/if}

{#if cloudOpen}
  <div
    class="cloud-overlay"
    on:click|self={closeCloudBrowser}
    on:keydown={(e) => e.key === 'Escape' && closeCloudBrowser()}
    role="presentation"
  >
    <div class="cloud-modal">
      <div class="cloud-modal-head">
        <div>
          <h2>☁️ Cloud snapshots</h2>
          <p class="cloud-modal-sub">
            {#if browsing}
              Reading cloud storage…
            {:else if cloudGames}
              {cloudGames.length} game{cloudGames.length === 1 ? '' : 's'} · {cloudTotals.snaps}
              snapshot{cloudTotals.snaps === 1 ? '' : 's'} · {fmtSize(cloudTotals.size)} in the cloud
            {:else}
              Could not read cloud storage
            {/if}
          </p>
        </div>
        <div class="cloud-head-actions">
          <button class="btn small" disabled={browsing} on:click={browseCloud}>
            {browsing ? 'Loading…' : 'Refresh'}
          </button>
          <button class="btn icon" on:click={closeCloudBrowser} title="Close">✕</button>
        </div>
      </div>

      {#if browsing && !cloudGames}
        <div class="cloud-loading"><span class="cspin"></span> Listing snapshots from your provider…</div>
      {:else if cloudGames && detailTile}
        <!-- drill-in: one game's cloud snapshots -->
        <div class="detail-head">
          <button class="btn small" on:click={() => (detailId = null)}>← Back</button>
          <div class="detail-title">
            <strong>{detailTile.name}</strong>
            <span class="quiet">
              {#if detailTile.cloud}
                {detailTile.cloud.count} snapshot{detailTile.cloud.count === 1 ? '' : 's'} · {fmtSize(detailTile.cloud.totalSize)} in the cloud
              {:else}
                nothing in the cloud yet
              {/if}
            </span>
          </div>
          {#if detailTile.tracked}
            <button class="btn small" disabled={uploading} on:click={() => uploadLocal(detailTile.id)}>
              {uploading ? 'Uploading…' : '⬆ Upload local snapshots'}
            </button>
          {/if}
        </div>
        {#if uploading}
          <div class="upload-progress">
            <div class="upload-progress-text">
              <span class="cspin"></span>
              {#if uploadProg && uploadProg.total > 0}
                Uploading {Math.min(uploadProg.done + 1, uploadProg.total)} of {uploadProg.total}
                {#if uploadProg.current}&nbsp;— <code>{uploadProg.current}</code>{/if}
              {:else}
                Checking what needs uploading…
              {/if}
            </div>
            <div class="upload-bar">
              <div
                class="upload-bar-fill"
                style="width: {uploadProg && uploadProg.total > 0 ? Math.round((uploadProg.done / uploadProg.total) * 100) : 8}%"
              ></div>
            </div>
          </div>
        {/if}
        <div class="cloud-modal-list">
          {#if detailTile.cloud}
            {#each detailTile.cloud.snapshots as f (f.name)}
              <div class="cloud-row">
                <div class="cloud-info">
                  <div class="cloud-name">{f.snapshotId} <span class="badge offline">{f.branch}</span></div>
                  <div class="cloud-meta">{fmtSize(f.sizeBytes)} · {new Date(f.createdTime).toLocaleString()}</div>
                </div>
                <button
                  class="btn small primary"
                  disabled={busy || !detailTile.tracked}
                  title={detailTile.tracked ? 'Download and restore this snapshot' : 'Track this game first to restore'}
                  on:click={() => restoreCloud(detailTile.id, f)}
                >
                  Restore
                </button>
                <button
                  class="btn small danger"
                  disabled={busy}
                  title="Delete from the cloud (local snapshots are kept)"
                  on:click={() => deleteCloud(detailTile.cloud, f)}
                >
                  Delete
                </button>
              </div>
            {/each}
          {:else}
            <div class="cloud-empty">
              <div class="cloud-empty-icon">☁️</div>
              <p>No cloud snapshots for this game yet.</p>
              <p class="quiet">Use <strong>Upload local snapshots</strong> above to push them up.</p>
            </div>
          {/if}
        </div>
        <div class="cloud-modal-foot">
          <span class="quiet">Deleting only removes the cloud copy — snapshots on your devices stay.</span>
          <button class="btn" on:click={closeCloudBrowser}>Close</button>
        </div>
      {:else if cloudGames}
        <!-- tile grid -->
        <div class="cloud-toolbar">
          <input class="cloud-search" placeholder="Filter by name…" bind:value={cloudFilter} />
          <div class="cloud-tabs">
            {#each [['all', 'All'], ['cloud', 'In cloud'], ['local', 'Not uploaded']] as [id, label]}
              <button class:active={cloudTab === id} on:click={() => (cloudTab = id)}>
                {label} <span class="count">{tabCounts[id]}</span>
              </button>
            {/each}
          </div>
        </div>

        <div class="cloud-modal-list">
          <div class="cloud-grid">
            {#each filteredTiles as t (t.id)}
              <div
                class="cover-tile"
                on:click={() => (detailId = t.id)}
                on:keydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), (detailId = t.id))}
                role="button"
                tabindex="0"
                title={t.name}
              >
                <div class="cover-art">
                  {#if t.coverUrl}
                    <img src={t.coverUrl} alt={t.name} loading="lazy" on:error={(e) => (e.currentTarget.style.display = 'none')} />
                  {/if}
                  <div class="cover-fallback">
                    <span class="cover-emoji">{t.cloud ? '☁️' : '💾'}</span>
                    <span class="cover-fallback-name">{t.name}</span>
                  </div>
                  {#if t.cloud}
                    <span class="cover-type in-cloud">☁ {t.cloud.count}</span>
                  {:else}
                    <span class="cover-type">local only</span>
                  {/if}
                  <div class="cover-hover">
                    <button class="btn small primary" on:click|stopPropagation={() => (detailId = t.id)}>
                      {t.cloud ? 'Browse' : 'Upload'}
                    </button>
                  </div>
                </div>
                <div class="cover-name">{t.name}</div>
              </div>
            {:else}
              <div class="cloud-grid-empty">
                {tiles.length === 0
                  ? 'No tracked games and nothing in the cloud yet.'
                  : 'No matches for this filter.'}
              </div>
            {/each}
          </div>
        </div>

        <div class="cloud-modal-foot">
          <span class="quiet">Click a game to browse, restore, delete, or upload its snapshots.</span>
          <button class="btn" on:click={closeCloudBrowser}>Close</button>
        </div>
      {/if}
    </div>
  </div>
{/if}

<style>
  .head {
    margin-bottom: 20px;
  }
  .quiet {
    color: var(--text-faint);
    font-size: 0.85rem;
  }
  .upload-progress {
    padding: 12px 22px;
    border-bottom: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .upload-progress-text {
    display: flex;
    align-items: center;
    gap: 9px;
    font-size: 0.84rem;
    color: var(--text-dim);
  }
  .upload-progress-text code {
    background: var(--bg);
    padding: 1px 6px;
    border-radius: 4px;
    font-size: 0.78rem;
  }
  .upload-bar {
    height: 6px;
    border-radius: 999px;
    background: var(--bg-active);
    overflow: hidden;
  }
  .upload-bar-fill {
    height: 100%;
    border-radius: 999px;
    background: var(--accent);
    transition: width 0.3s ease;
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
    align-items: center;
    gap: 12px;
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
  /* Cloud snapshot browser overlay (same pattern as the auto-scan modal) */
  .cloud-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.62);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 80;
    padding: 32px;
  }
  .cloud-modal {
    width: min(760px, 100%);
    height: min(78vh, 720px);
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-lg);
    display: flex;
    flex-direction: column;
    box-shadow: 0 20px 60px rgba(0, 0, 0, 0.5);
  }
  .cloud-modal-head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    padding: 20px 22px 14px;
    border-bottom: 1px solid var(--border);
  }
  .cloud-modal-head h2 {
    font-size: 1.2rem;
  }
  .cloud-modal-sub {
    font-size: 0.84rem;
    color: var(--text-faint);
    margin-top: 3px;
  }
  .cloud-head-actions {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .cloud-loading {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 10px;
    color: var(--text-dim);
  }
  .cloud-empty {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 6px;
    color: var(--text-dim);
    text-align: center;
    padding: 20px;
  }
  .cloud-empty-icon {
    font-size: 2.2rem;
    opacity: 0.6;
  }
  .cloud-toolbar {
    display: flex;
    gap: 12px;
    padding: 14px 22px 10px;
    align-items: center;
    flex-wrap: wrap;
  }
  .cloud-search {
    flex: 1;
    min-width: 200px;
    padding: 9px 13px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    color: var(--text);
    outline: none;
  }
  .cloud-tabs {
    display: flex;
    gap: 6px;
  }
  .cloud-tabs button {
    padding: 7px 13px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: transparent;
    color: var(--text-dim);
    font-size: 0.85rem;
    cursor: pointer;
  }
  .cloud-tabs button:hover {
    background: var(--bg-hover);
  }
  .cloud-tabs button.active {
    background: var(--bg-active);
    color: var(--text);
    border-color: var(--border-strong);
  }
  .cloud-tabs .count {
    color: var(--text-faint);
    font-size: 0.75rem;
    margin-left: 2px;
  }
  /* Cover-art tile grid (same look as the auto-scan modal) */
  .cloud-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 16px;
  }
  .cloud-grid-empty {
    grid-column: 1 / -1;
    text-align: center;
    color: var(--text-faint);
    padding: 50px 20px;
  }
  .cover-tile {
    cursor: pointer;
    outline: none;
  }
  .cover-art {
    position: relative;
    aspect-ratio: 600 / 900;
    border-radius: 10px;
    overflow: hidden;
    background: var(--bg-active);
    border: 2px solid transparent;
    transition: transform 0.12s, border-color 0.12s, box-shadow 0.12s;
  }
  .cover-tile:hover .cover-art {
    transform: translateY(-2px);
    box-shadow: 0 8px 22px rgba(0, 0, 0, 0.45);
  }
  .cover-art img {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
    z-index: 2;
  }
  .cover-fallback {
    position: absolute;
    inset: 0;
    z-index: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    padding: 14px;
    text-align: center;
    background: linear-gradient(160deg, rgba(138, 99, 244, 0.22), rgba(138, 99, 244, 0.04));
  }
  .cover-emoji {
    font-size: 2.2rem;
  }
  .cover-fallback-name {
    font-weight: 700;
    font-size: 0.9rem;
    color: var(--text);
    line-height: 1.25;
    display: -webkit-box;
    -webkit-line-clamp: 4;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .cover-type {
    position: absolute;
    top: 8px;
    right: 8px;
    z-index: 3;
    padding: 2px 8px;
    border-radius: 999px;
    font-size: 0.68rem;
    font-weight: 600;
    background: rgba(0, 0, 0, 0.6);
    color: var(--text-dim);
    backdrop-filter: blur(2px);
  }
  .cover-type.in-cloud {
    color: var(--accent);
  }
  .cover-hover {
    position: absolute;
    inset: 0;
    z-index: 3;
    display: flex;
    align-items: flex-end;
    justify-content: center;
    padding: 12px;
    opacity: 0;
    background: linear-gradient(to top, rgba(0, 0, 0, 0.75), transparent 55%);
    transition: opacity 0.12s;
  }
  .cover-tile:hover .cover-hover {
    opacity: 1;
  }
  .cover-name {
    margin-top: 7px;
    font-size: 0.82rem;
    font-weight: 500;
    color: var(--text-dim);
    text-align: center;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* Drill-in detail view */
  .detail-head {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 22px;
    border-bottom: 1px solid var(--border);
  }
  .detail-title {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
  }
  .detail-title strong {
    font-size: 0.98rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .cloud-modal-list {
    flex: 1;
    overflow-y: auto;
    padding: 4px 22px 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .cloud-modal-foot {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 12px;
    padding: 14px 22px;
    border-top: 1px solid var(--border);
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

  /* Export picker + import mode dialog */
  .export-toolbar {
    display: flex;
    gap: 8px;
    padding: 10px 20px 0;
  }
  .export-item {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 12px;
    border-radius: 8px;
    cursor: pointer;
  }
  .export-item:hover {
    background: var(--bg-hover, rgba(128, 128, 128, 0.08));
  }
  .export-item input {
    flex-shrink: 0;
  }
  .export-item code {
    font-size: 0.72rem;
    word-break: break-all;
  }
  .export-cover {
    position: relative;
    width: 34px;
    height: 46px;
    border-radius: 5px;
    overflow: hidden;
    flex-shrink: 0;
    background: var(--bg-hover, rgba(128, 128, 128, 0.12));
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .export-cover img {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
  }
  .export-cover-fallback {
    font-size: 1rem;
  }
  .export-foot-actions {
    display: flex;
    gap: 8px;
  }
  .import-modal {
    max-width: 560px;
    /* Two radio cards + a footer don't need the full browser-modal
       height — size to content so the dialog doesn't look hollow. */
    height: auto;
    max-height: min(78vh, 720px);
  }
  .import-modes {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 16px 20px;
  }
  .mode-option {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 14px;
    border: 1px solid var(--border);
    border-radius: 10px;
    cursor: pointer;
  }
  .mode-option input {
    margin-top: 3px;
  }
  .mode-option.selected {
    border-color: var(--accent);
    background: var(--bg-hover, rgba(128, 128, 128, 0.06));
  }
  .mode-option.danger-option.selected {
    border-color: var(--danger, #e5484d);
  }
  .mode-title {
    font-weight: 600;
    margin-bottom: 4px;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .mode-option p {
    margin: 0;
    font-size: 0.82rem;
  }
</style>
