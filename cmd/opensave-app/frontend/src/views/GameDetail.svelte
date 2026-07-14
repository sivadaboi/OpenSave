<script>
  import { games, navigate, toast, syncActivity, askConfirm } from '../lib/stores.js';
  import { api, native } from '../lib/api.js';

  export let params = {};

  $: game = $games[params.gameId];
  $: activity = $syncActivity[params.gameId];

  let tab = 'snapshots';
  let newBranch = '';
  let snapshotComment = '';
  let busy = false;
  let browsing = null; // {snapshotId, files}

  // GameDetail is reused (not remounted) when navigating between games, so
  // reset per-game view state whenever the game id changes — otherwise the
  // previous game's cloud list / open file browser would leak across.
  let loadedFor = null;
  $: if (params.gameId !== loadedFor) {
    loadedFor = params.gameId;
    tab = 'snapshots';
    browsing = null;
    cloudSnaps = null;
    cloudLoading = false;
  }

  // Editable per-game configuration (loaded from the game, saved via PATCH).
  let cfg = null;
  $: if (game && (cfg === null || cfg._id !== game.id)) {
    cfg = {
      _id: game.id,
      appId: game.appId ?? '',
      exePath: game.exePath ?? '',
      coverUrl: game.coverUrl ?? '',
      autoSync: game.autoSync ?? true,
      maxSnapshots: game.maxSnapshots ?? 5
    };
  }

  // Cloud explorer state.
  let cloudSnaps = null;
  let cloudLoading = false;

  $: branches = game ? Object.values(game.branches ?? {}) : [];
  $: allSnapshots = branches
    .flatMap((b) => (b.snapshots ?? []).map((s) => ({ ...s, branch: b.name })))
    .sort((a, b) => (a.timestamp < b.timestamp ? 1 : -1));

  const fmtTime = (t) => (t ? new Date(t).toLocaleString() : '—');
  const fmtSize = (n) => (n >= 1048576 ? (n / 1048576).toFixed(1) + ' MB' : (n / 1024).toFixed(1) + ' KB');

  async function run(label, fn) {
    if (busy) return;
    busy = true;
    try {
      await fn();
      if (label) toast(label, 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      busy = false;
    }
  }

  const syncNow = () => run('Sync triggered', () => api.post(`/api/games/${game.id}/sync`));
  const takeSnapshot = () =>
    run('Snapshot created', async () => {
      await api.post(`/api/games/${game.id}/snapshot`, { comment: snapshotComment });
      snapshotComment = '';
    });
  const rollback = async (snap) => {
    if (!(await askConfirm(`Restore snapshot ${snap.id} over your current save? Your current state is snapshotted first, so this is reversible.`, { title: 'Restore snapshot?', confirmText: 'Restore' }))) return;
    return run(`Restored ${snap.id}`, () => api.post(`/api/games/${game.id}/rollback`, { snapshotId: snap.id }));
  };
  const createBranch = () =>
    run('Branch created', async () => {
      await api.post(`/api/games/${game.id}/branch`, { name: newBranch });
      newBranch = '';
    });
  const switchBranch = (name) =>
    run(`Switched to "${name}"`, () => api.post(`/api/games/${game.id}/branch/switch`, { name }));

  async function browseSnapshot(snap) {
    try {
      const files = await api.get(`/api/games/${game.id}/snapshot/${snap.id}/files`);
      browsing = { snapshotId: snap.id, files };
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  const restoreFile = async (relPath) => {
    if (!(await askConfirm(`Restore "${relPath}" from ${browsing.snapshotId} over the current file?`, { title: 'Restore file?', confirmText: 'Restore' }))) return;
    return run(`Restored ${relPath}`, () =>
      api.post(`/api/games/${game.id}/snapshot/${browsing.snapshotId}/restore-file`, { relPath })
    );
  };

  async function untrack() {
    if (!(await askConfirm(`Stop tracking "${game.name}"? Snapshot files stay on disk.`, { title: 'Stop tracking?', confirmText: 'Stop tracking', danger: true }))) return;
    await run('Stopped tracking', () => api.del(`/api/games/${game.id}`));
    navigate('home');
  }

  // ── Configuration ────────────────────────────────────────────────
  const saveConfig = () =>
    run('Configuration saved', () =>
      api.patch(`/api/games/${game.id}`, {
        appId: cfg.appId,
        exePath: cfg.exePath,
        coverUrl: cfg.coverUrl,
        autoSync: cfg.autoSync,
        maxSnapshots: Number(cfg.maxSnapshots)
      })
    );

  async function browseExe() {
    const file = await native.selectFile('Select the game executable');
    if (file) cfg.exePath = file;
  }

  // ── Cloud explorer ───────────────────────────────────────────────
  async function loadCloudSnaps() {
    cloudLoading = true;
    cloudSnaps = null;
    try {
      cloudSnaps = await api.get(`/api/cloud/snapshots/${game.id}`);
    } catch (e) {
      toast(e.message, 'error');
      cloudSnaps = [];
    } finally {
      cloudLoading = false;
    }
  }

  $: if (game && tab === 'cloud' && cloudSnaps === null && !cloudLoading) loadCloudSnaps();

  const restoreCloud = async (snap) => {
    if (!(await askConfirm(`Download and restore cloud snapshot ${snap.snapshotId} over your current save?`, { title: 'Restore from cloud?', confirmText: 'Download & restore' }))) return;
    return run('Restored from cloud', async () => {
      await api.post(`/api/cloud/restore/${game.id}`, { fileName: snap.name });
    });
  };

  const uploadToCloud = () =>
    run('Uploaded to cloud', async () => {
      const res = await api.post(`/api/cloud/sync-local/${game.id}`);
      toast(`Uploaded ${res.uploaded}, skipped ${res.skipped}`, 'success');
      await loadCloudSnaps();
    });

  async function launchGame() {
    await run('Launching…', () => api.post(`/api/games/${game.id}/launch`));
  }

  let editPath = false;
  let pathDraft = '';
  async function savePath() {
    await run('Save path updated', () => api.patch(`/api/games/${game.id}`, { savePath: pathDraft }));
    editPath = false;
  }
</script>

{#if !game}
  <div class="empty"><h3>Game not found</h3></div>
{:else}
  <div class="head">
    <button class="btn icon back" on:click={() => navigate('home')} title="Back">←</button>
    {#if game.coverUrl}
      <img
        class="head-cover"
        src={game.coverUrl}
        alt=""
        on:load={(e) => (e.currentTarget.style.display = '')}
        on:error={(e) => (e.currentTarget.style.display = 'none')}
      />
    {/if}
    <div class="title-block">
      <h2 class="page-title">{game.name}</h2>
      <div class="sub">
        branch <strong>{game.activeBranch}</strong>
        {#if activity?.state === 'running'}
          · <span class="syncing">syncing {activity.percentage ?? 0}%</span>
        {/if}
      </div>
    </div>
    <div class="head-actions">
      {#if game.appId || game.exePath}
        <button class="btn" disabled={busy} on:click={launchGame}>▶ Launch</button>
      {/if}
      <button class="btn primary" disabled={busy} on:click={syncNow}>⟳ Sync now</button>
    </div>
  </div>

  <div class="path-line">
    {#if editPath}
      <input class="path-input" bind:value={pathDraft} />
      <button class="btn small" on:click={async () => (pathDraft = (await native.selectDirectory('Select save folder')) || pathDraft)}>Browse</button>
      <button class="btn small primary" on:click={savePath}>Save</button>
      <button class="btn small" on:click={() => (editPath = false)}>Cancel</button>
    {:else}
      <span class="path" title={game.savePath}>{game.savePath}</span>
      <button class="btn small" on:click={() => { pathDraft = game.savePath; editPath = true; }}>Edit</button>
    {/if}
  </div>

  <div class="pill-tabs tabs">
    <button class:active={tab === 'snapshots'} on:click={() => (tab = 'snapshots')}>Snapshots</button>
    <button class:active={tab === 'branches'} on:click={() => (tab = 'branches')}>Branches</button>
    <button class:active={tab === 'cloud'} on:click={() => (tab = 'cloud')}>☁️ Cloud</button>
    <button class:active={tab === 'config'} on:click={() => (tab = 'config')}>Configuration</button>
    <button class:active={tab === 'danger'} on:click={() => (tab = 'danger')}>Manage</button>
  </div>

  {#if tab === 'snapshots'}
    <div class="card snap-new">
      <input placeholder="Snapshot comment (optional)" bind:value={snapshotComment} />
      <button class="btn primary" disabled={busy} on:click={takeSnapshot}>📸 Snapshot now</button>
    </div>

    {#if browsing}
      <div class="card browse">
        <div class="browse-head">
          <h3>Files in {browsing.snapshotId}</h3>
          <button class="btn small" on:click={() => (browsing = null)}>Close</button>
        </div>
        {#each browsing.files.filter((f) => !f.isDir) as f}
          <div class="file-row">
            <span class="file-path">{f.path}</span>
            <span class="file-size">{fmtSize(f.size)}</span>
            <button class="btn small" disabled={busy} on:click={() => restoreFile(f.path)}>Restore file</button>
          </div>
        {/each}
      </div>
    {/if}

    {#if allSnapshots.length === 0}
      <div class="empty"><h3>No snapshots yet</h3><p>Snapshots are created automatically when your save changes.</p></div>
    {:else}
      <div class="snap-list">
        {#each allSnapshots as snap (snap.id)}
          <div class="card snap">
            <div class="snap-info">
              <div class="snap-top">
                <span class="snap-id">{snap.id}</span>
                <span class="badge offline">{snap.branch}</span>
                {#if snap.isSystemAuto}<span class="badge offline">auto</span>{/if}
              </div>
              <div class="snap-comment">{snap.comment}</div>
              <div class="snap-meta">{fmtTime(snap.timestamp)} · {fmtSize(snap.sizeBytes)}</div>
            </div>
            <div class="snap-actions">
              <button class="btn small" on:click={() => browseSnapshot(snap)}>Browse files</button>
              <button class="btn small primary" disabled={busy} on:click={() => rollback(snap)}>Restore</button>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  {:else if tab === 'branches'}
    <div class="card snap-new">
      <input placeholder="New branch name (e.g. ng-plus)" bind:value={newBranch} />
      <button class="btn primary" disabled={!newBranch || busy} on:click={createBranch}>+ Create branch</button>
    </div>
    <div class="snap-list">
      {#each branches as branch (branch.name)}
        <div class="card snap">
          <div class="snap-info">
            <div class="snap-top">
              <span class="snap-id">{branch.name}</span>
              {#if branch.name === game.activeBranch}<span class="badge online">active</span>{/if}
            </div>
            <div class="snap-meta">{branch.snapshots?.length ?? 0} snapshot(s)</div>
          </div>
          {#if branch.name !== game.activeBranch}
            <button class="btn small primary" disabled={busy} on:click={() => switchBranch(branch.name)}>
              Switch to
            </button>
          {/if}
        </div>
      {/each}
    </div>
    <p class="branch-hint">
      Switching branches snapshots your current save first, then restores the other branch's latest state.
    </p>
  {:else if tab === 'cloud'}
    <div class="card">
      <div class="cloud-head">
        <div>
          <h3>☁️ Cloud snapshots for {game.name}</h3>
          <p class="cloud-sub">Snapshots backed up to your configured cloud provider.</p>
        </div>
        <div class="cloud-actions">
          <button class="btn small" disabled={busy} on:click={loadCloudSnaps}>↻ Refresh</button>
          <button class="btn small primary" disabled={busy} on:click={uploadToCloud}>↑ Upload local snapshots</button>
        </div>
      </div>

      {#if cloudLoading}
        <div class="cloud-loading"><span class="cspin"></span> Loading cloud snapshots…</div>
      {:else if !cloudSnaps || cloudSnaps.length === 0}
        <div class="cloud-empty">
          <p>No cloud snapshots for this game yet.</p>
          <p class="cloud-hint">
            Enable a provider in <button class="linklike" on:click={() => navigate('cloud')}>Cloud Backup</button>,
            then use “Upload local snapshots”.
          </p>
        </div>
      {:else}
        <table class="cloud-table">
          <thead>
            <tr><th>Branch</th><th>Date</th><th>Size</th><th></th></tr>
          </thead>
          <tbody>
            {#each cloudSnaps as snap (snap.name)}
              <tr>
                <td><span class="badge offline">{snap.branch}</span></td>
                <td class="mono">{new Date(snap.createdTime).toLocaleString()}</td>
                <td class="mono">{fmtSize(snap.sizeBytes)}</td>
                <td class="right">
                  <button class="btn small primary" disabled={busy} on:click={() => restoreCloud(snap)}>Restore</button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </div>
  {:else if tab === 'config'}
    {#if cfg}
      <div class="card config-card">
        <div class="config-cover">
          {#if cfg.coverUrl}
            <img src={cfg.coverUrl} alt="" on:error={(e) => (e.currentTarget.style.display = 'none')} />
          {:else}
            <div class="config-cover-fallback">🎮</div>
          {/if}
        </div>
        <div class="config-fields">
          <h3>Launch &amp; sync configuration</h3>
          <div class="field">
            <label for="c-appid">Steam App ID</label>
            <input id="c-appid" placeholder="e.g. 1091500" bind:value={cfg.appId} />
            <span class="hint">Used to launch via Steam and fetch cover art automatically.</span>
          </div>
          <div class="field">
            <label for="c-exe">Executable path (non-Steam)</label>
            <div class="path-row">
              <input id="c-exe" placeholder="Browse to the game .exe" bind:value={cfg.exePath} />
              <button class="btn" on:click={browseExe}>Browse</button>
            </div>
          </div>
          <div class="field">
            <label for="c-cover">Custom cover image URL</label>
            <input id="c-cover" placeholder="https://…  (great for emulator games)" bind:value={cfg.coverUrl} />
            <span class="hint">Leave blank to auto-use Steam art from the App ID. Paste any image URL for emulators.</span>
          </div>
          <label class="check">
            <input type="checkbox" bind:checked={cfg.autoSync} />
            Auto-sync saves when changes are detected
          </label>
          <div class="field" style="margin-top: 12px;">
            <label for="c-max">Snapshot retention limit</label>
            <input id="c-max" type="number" min="0" bind:value={cfg.maxSnapshots} />
            <span class="hint">Max snapshots kept per branch (0 = unlimited). Oldest are pruned first.</span>
          </div>
          <div class="config-save">
            <button class="btn primary" disabled={busy} on:click={saveConfig}>Save configuration</button>
          </div>
        </div>
      </div>
    {/if}
  {:else}
    <div class="card">
      <h3>Stop tracking</h3>
      <p class="danger-desc">
        Removes "{game.name}" from OpenSave. Your save files and existing snapshot archives on disk are
        kept.
      </p>
      <button class="btn danger" disabled={busy} on:click={untrack}>Stop tracking this game</button>
    </div>
  {/if}
{/if}

<style>
  .head {
    display: flex;
    align-items: center;
    gap: 14px;
    margin-bottom: 6px;
  }
  .back {
    font-size: 1rem;
  }
  .head-cover {
    height: 52px;
    aspect-ratio: 460 / 215;
    object-fit: cover;
    border-radius: 8px;
    border: 1px solid var(--border);
  }
  .title-block {
    flex: 1;
    min-width: 0;
  }
  .sub {
    color: var(--text-dim);
    font-size: 0.85rem;
    margin-top: 2px;
  }
  .syncing {
    color: var(--accent);
    font-weight: 600;
  }
  .head-actions {
    display: flex;
    gap: 8px;
  }
  .path-line {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 0 18px 50px;
  }
  .path {
    font-size: 0.8rem;
    color: var(--text-faint);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .path-input {
    flex: 1;
    padding: 6px 10px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    color: var(--text);
    font-size: 0.82rem;
    outline: none;
  }
  .tabs {
    margin-bottom: 18px;
  }
  .snap-new {
    display: flex;
    gap: 10px;
    padding: 14px;
    margin-bottom: 14px;
  }
  .snap-new input {
    flex: 1;
    padding: 8px 12px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    color: var(--text);
    outline: none;
  }
  .snap-list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .snap {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 14px 16px;
  }
  .snap-info {
    flex: 1;
    min-width: 0;
  }
  .snap-top {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 4px;
  }
  .snap-id {
    font-weight: 600;
    font-size: 0.9rem;
  }
  .snap-comment {
    font-size: 0.83rem;
    color: var(--text-dim);
    margin-bottom: 3px;
  }
  .snap-meta {
    font-size: 0.75rem;
    color: var(--text-faint);
  }
  .snap-actions {
    display: flex;
    gap: 6px;
  }
  .browse {
    margin-bottom: 14px;
  }
  .browse-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 10px;
  }
  .file-row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 7px 4px;
    border-bottom: 1px solid var(--border);
    font-size: 0.85rem;
  }
  .file-row:last-child {
    border-bottom: none;
  }
  .file-path {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .file-size {
    color: var(--text-faint);
    font-size: 0.78rem;
  }
  .branch-hint {
    margin-top: 12px;
    font-size: 0.8rem;
    color: var(--text-faint);
  }
  .danger-desc {
    color: var(--text-dim);
    font-size: 0.88rem;
    margin: 8px 0 14px;
  }

  /* Cloud explorer */
  .cloud-head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
    margin-bottom: 14px;
    flex-wrap: wrap;
  }
  .cloud-sub {
    font-size: 0.82rem;
    color: var(--text-faint);
    margin-top: 3px;
  }
  .cloud-actions {
    display: flex;
    gap: 8px;
  }
  .cloud-loading,
  .cloud-empty {
    padding: 30px 10px;
    text-align: center;
    color: var(--text-faint);
  }
  .cloud-hint {
    font-size: 0.82rem;
    margin-top: 6px;
  }
  .linklike {
    background: none;
    border: none;
    color: var(--accent);
    cursor: pointer;
    padding: 0;
    font: inherit;
  }
  .cspin {
    display: inline-block;
    width: 13px;
    height: 13px;
    border: 2px solid var(--accent-soft);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
    vertical-align: middle;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  .cloud-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
  }
  .cloud-table th {
    text-align: left;
    color: var(--text-faint);
    font-weight: 600;
    font-size: 0.75rem;
    padding: 6px 10px;
    border-bottom: 1px solid var(--border);
  }
  .cloud-table td {
    padding: 9px 10px;
    border-bottom: 1px solid var(--border);
  }
  .cloud-table tr:last-child td {
    border-bottom: none;
  }
  .cloud-table .mono {
    font-family: 'Cascadia Code', 'Consolas', monospace;
    font-size: 0.78rem;
    color: var(--text-dim);
  }
  .cloud-table .right {
    text-align: right;
  }

  /* Configuration panel */
  .config-card {
    display: flex;
    gap: 20px;
    align-items: flex-start;
  }
  .config-cover {
    flex-shrink: 0;
    width: 160px;
    aspect-ratio: 460 / 215;
    border-radius: var(--radius);
    overflow: hidden;
    border: 1px solid var(--border);
    background: var(--bg);
  }
  .config-cover img {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }
  .config-cover-fallback {
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 2rem;
  }
  .config-fields {
    flex: 1;
    min-width: 0;
  }
  .config-fields h3 {
    margin-bottom: 14px;
  }
  .config-fields .field {
    margin-bottom: 14px;
  }
  .config-fields .check {
    display: flex;
    align-items: center;
    gap: 9px;
    font-size: 0.9rem;
    cursor: pointer;
  }
  .config-fields .check input {
    accent-color: var(--accent);
    width: 16px;
    height: 16px;
  }
  .config-save {
    display: flex;
    justify-content: flex-end;
    margin-top: 8px;
  }
  @media (max-width: 720px) {
    .config-card {
      flex-direction: column;
    }
    .config-cover {
      width: 100%;
    }
  }
</style>
