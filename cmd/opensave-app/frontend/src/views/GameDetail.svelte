<script>
  import { games, navigate, toast, syncActivity, askConfirm } from '../lib/stores.js';
  import { api, native, coverURL, gameCover } from '../lib/api.js';

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
  // Cover preview for the config editor: a custom URL wins, else the proxied
  // Steam art for the App ID being edited.
  $: cfgCover =
    cfg?.coverUrl && !cfg.coverUrl.includes('steamstatic.com') ? cfg.coverUrl : coverURL(cfg?.appId);

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
  async function deleteBranch(name) {
    if (!(await askConfirm(`Delete branch "${name}" and all its snapshots? Your current save and other branches aren't affected.`, { title: 'Delete branch?', confirmText: 'Delete', danger: true }))) return;
    run(`Deleted branch "${name}"`, () => api.del(`/api/games/${game.id}/branch/${encodeURIComponent(name)}`));
  }
  async function deleteSnapshot(snap) {
    if (!(await askConfirm(`Delete snapshot ${snap.id}? This can't be undone; your current save isn't affected.`, { title: 'Delete snapshot?', confirmText: 'Delete', danger: true }))) return;
    run('Snapshot deleted', () => api.del(`/api/games/${game.id}/snapshot/${snap.id}`));
  }

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
    const gameId = game.id;
    const gameName = game.name;
    await run('Stopped tracking', () => api.del(`/api/games/${gameId}`));
    navigate('home');

    // Cloud copies would otherwise linger forever — offer to clean them up.
    try {
      const settings = await api.get('/api/settings');
      if (settings.cloudSync?.enabled) {
        if (
          await askConfirm(
            `Also delete "${gameName}"'s snapshots from the cloud? Local snapshot files stay on disk either way.`,
            { title: 'Clean up cloud copies?', confirmText: 'Delete from cloud', cancelText: 'Keep them', danger: true }
          )
        ) {
          const res = await api.post(`/api/cloud/delete-game/${gameId}`);
          toast(
            res.deleted > 0
              ? `Removed ${res.deleted} cloud snapshot(s)`
              : 'No cloud snapshots to remove',
            'success'
          );
        }
      }
    } catch (e) {
      toast(`Cloud cleanup failed: ${e.message}`, 'error');
    }
  }

  // Linked copies — cross-device "same game" links (the manual counterpart
  // to App-ID matching). Loaded lazily when the Manage tab is opened.
  let aliases = [];
  let aliasesFor = null;
  let linkTarget = '';
  $: if (game && tab === 'danger' && aliasesFor !== game.id) loadAliases();
  $: otherGames = Object.values($games).filter((g) => g.id !== params.gameId);

  // Games tracked on paired devices. Without these the picker can only offer
  // entries from this machine, which merges local duplicates but can never
  // connect a title tracked under one name here and another name there —
  // the case App-ID matching can't cover, because a save sitting in
  // AppData\<company>\<game> carries no App ID anywhere in its path.
  let peerGames = [];
  let peerGamesFor = null;
  let peerGamesLoading = false;
  $: if (game && tab === 'danger' && peerGamesFor !== game.id) loadPeerGames();

  async function loadPeerGames() {
    peerGamesFor = game.id;
    peerGamesLoading = true;
    peerGames = [];
    try {
      const peers = await api.get('/api/peers');
      // Ask each device separately and keep whatever answers: one being
      // offline must not cost you the ability to link against the others.
      const results = await Promise.allSettled(
        (peers ?? []).map(async (p) => ({
          peer: p,
          games: await api.get(`/api/peers/${p.id}/games`),
        }))
      );
      const local = new Set(Object.keys($games));
      peerGames = results
        .filter((r) => r.status === 'fulfilled')
        .flatMap((r) =>
          (r.value.games ?? [])
            // An id we already track is the same entry, not a link target.
            .filter((g) => !local.has(g.id))
            .map((g) => ({ ...g, peerName: r.value.peer.name }))
        );
    } catch {
      peerGames = [];
    } finally {
      peerGamesLoading = false;
    }
  }

  async function loadAliases() {
    aliasesFor = game.id;
    try {
      aliases = await api.get(`/api/games/${game.id}/aliases`);
    } catch {
      aliases = [];
    }
  }
  async function linkGame() {
    if (!linkTarget) return;
    const other = $games[linkTarget];
    const remote = peerGames.find((g) => g.id === linkTarget);

    // Two different operations behind one button, and the difference matters
    // to the user: merging a local entry removes it from this library, while
    // linking another device's entry changes nothing here at all. Saying
    // "removed from your library" for the second would be a lie about a
    // destructive step that isn't happening.
    const message = remote
      ? `Link "${remote.name}" on ${remote.peerName} to "${game.name}"? The two will be treated as the same game when these devices sync. Nothing on either device is removed.`
      : `Link "${other?.name ?? linkTarget}" into "${game.name}"? They'll be treated as the same game when syncing across devices. "${other?.name ?? linkTarget}" is removed from your library here — its save files and snapshots on disk are kept.`;

    const ok = await askConfirm(message, { title: 'Link games?', confirmText: 'Link' });
    if (!ok) return;
    const canonicalId = game.id;
    await run('Games linked', () => api.post(`/api/games/${canonicalId}/link`, { alias: linkTarget }));
    linkTarget = '';
    await loadAliases();
  }
  async function unlink(aliasId) {
    await run('Link removed', () => api.del(`/api/games/${game.id}/alias/${aliasId}`));
    await loadAliases();
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
  // Reveal the save location in Explorer / Finder / the Linux file manager.
  // The bridge returns a message when it can't (e.g. the folder was deleted).
  async function openSaveFolder() {
    const problem = await native.openFolder(game.savePath);
    if (problem) toast(problem, 'error');
  }

  async function savePath() {
    await run('Save path updated', () => api.patch(`/api/games/${game.id}`, { savePath: pathDraft }));
    editPath = false;
  }

  // Steam App ID. Detected automatically where the save path or the game's
  // name gives it away, but a save under AppData\<company>\<game> carries no
  // App ID anywhere and may not be in the manifest either — leaving no way to
  // turn on matching for exactly the games that need it most. Setting it also
  // gets the cover art, which the backend re-derives when this changes.
  let editAppID = false;
  let appIDDraft = '';
  let appIDError = '';

  function startEditAppID() {
    appIDDraft = game.appId ?? '';
    appIDError = '';
    editAppID = true;
  }

  async function saveAppID() {
    const value = appIDDraft.trim();
    // Steam App IDs are digits. Catching this here beats saving something
    // that silently matches nothing and looks like the feature is broken.
    if (value !== '' && !/^\d+$/.test(value)) {
      appIDError = 'A Steam App ID is digits only — the number in the store URL.';
      return;
    }
    appIDError = '';
    await run(value === '' ? 'App ID cleared' : 'App ID set', () =>
      api.patch(`/api/games/${game.id}`, { appId: value })
    );
    editAppID = false;
  }
</script>

{#if !game}
  <div class="empty"><h3>Game not found</h3></div>
{:else}
  <div class="head">
    <button class="btn icon back" on:click={() => navigate('home')} title="Back">←</button>
    {#if gameCover(game)}
      <img
        class="head-cover"
        src={gameCover(game)}
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
      <button class="btn small" on:click={openSaveFolder} title="Show this folder in your file manager">
        📂 Open folder
      </button>
      <button class="btn small" on:click={() => { pathDraft = game.savePath; editPath = true; }}>Edit</button>
    {/if}
  </div>

  <div class="path-line">
    {#if editAppID}
      <input
        class="path-input appid-input"
        bind:value={appIDDraft}
        placeholder="e.g. 1245620"
        on:keydown={(e) => e.key === 'Enter' && saveAppID()}
      />
      <button class="btn small primary" on:click={saveAppID}>Save</button>
      <button class="btn small" on:click={() => (editAppID = false)}>Cancel</button>
      {#if appIDError}<span class="appid-error">{appIDError}</span>{/if}
    {:else}
      <span class="path" title="Steam App ID — used to match this game across devices">
        Steam App ID: {game.appId ? game.appId : '—'}
      </span>
      <button class="btn small" on:click={startEditAppID}>
        {game.appId ? 'Edit' : 'Set'}
      </button>
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
              <button class="btn small danger" disabled={busy} on:click={() => deleteSnapshot(snap)}>Delete</button>
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
            <div class="branch-actions">
              <button class="btn small primary" disabled={busy} on:click={() => switchBranch(branch.name)}>
                Switch to
              </button>
              {#if branch.name !== 'main'}
                <button class="btn small danger" disabled={busy} on:click={() => deleteBranch(branch.name)}>
                  Delete
                </button>
              {/if}
            </div>
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
          {#if cfgCover}
            <img src={cfgCover} alt="" on:error={(e) => (e.currentTarget.style.display = 'none')} />
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
      <h3>Linked copies</h3>
      <p class="danger-desc">
        If this game is tracked under a different name or drive on another PC (e.g. a Steam copy vs. a
        portable copy), link the copies so their saves sync across devices. Linking merges another tracked
        game here into this one — its save files and snapshots on disk are kept.
      </p>
      {#if aliases.length > 0}
        <div class="alias-list">
          {#each aliases as a}
            <div class="alias-row">
              <span class="alias-id" title={a.savePath || a.id}>
                🔗 {a.name || a.id}{a.savePath ? ` — ${a.savePath}` : ''}
              </span>
              <button class="btn small" disabled={busy} on:click={() => unlink(a.id)}>Unlink</button>
            </div>
          {/each}
        </div>
      {/if}
      {#if otherGames.length > 0 || peerGames.length > 0}
        <div class="link-row">
          <select bind:value={linkTarget}>
            <option value="">Choose a game to link…</option>
            {#if otherGames.length > 0}
              <optgroup label="On this device (merges the entry)">
                {#each otherGames as g}
                  <!-- Same-named entries are normal now that one game can be
                       tracked at several save locations, so show the path too —
                       otherwise duplicates are indistinguishable in this list. -->
                  <option value={g.id}>{g.name} — {g.savePath}</option>
                {/each}
              </optgroup>
            {/if}
            {#if peerGames.length > 0}
              <optgroup label="On a paired device (nothing is removed)">
                {#each peerGames as g}
                  <option value={g.id}>{g.name} — {g.peerName}</option>
                {/each}
              </optgroup>
            {/if}
          </select>
          <button class="btn small primary" disabled={busy || !linkTarget} on:click={linkGame}>Link</button>
        </div>
        {#if peerGamesLoading}
          <p class="danger-desc">Checking paired devices…</p>
        {/if}
      {:else if peerGamesLoading}
        <p class="danger-desc">Checking paired devices…</p>
      {:else}
        <p class="danger-desc">
          Nothing to link to. Track the other copy on this device, or pair the device that
          has it and make sure it's online — its games appear here once it answers.
        </p>
      {/if}
    </div>

    <div class="card" style="margin-top: 16px;">
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
  /* The App ID is a second detail line about the same game, so it reads as
     part of the header block rather than a separate section. */
  .path-line + .path-line {
    margin-top: -12px;
  }
  /* An App ID is a short number; a full-width box invites pasting a path. */
  .appid-input {
    flex: 0 0 170px;
  }
  .appid-error {
    font-size: 0.78rem;
    color: var(--danger);
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
  .branch-actions {
    display: flex;
    gap: 6px;
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

  /* Linked copies */
  .alias-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-bottom: 12px;
  }
  .alias-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    padding: 7px 10px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .alias-id {
    font-family: monospace;
    font-size: 0.82rem;
    color: var(--text-dim);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .link-row {
    display: flex;
    gap: 10px;
    align-items: center;
    margin-top: 14px;
  }
  .link-row select {
    flex: 1;
    min-width: 0;
    padding: 8px 10px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    color: var(--text);
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
