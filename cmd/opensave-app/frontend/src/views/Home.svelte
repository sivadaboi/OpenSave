<script>
  import { gameList, peers, navigate, toast, syncActivity } from '../lib/stores.js';
  import { api, native } from '../lib/api.js';

  export let params = {};

  let showAdd = params.add ?? false;
  $: if (params.add) showAdd = true;

  // ── Track-a-game form ────────────────────────────────────────────
  let newName = '';
  let newPath = '';
  let adding = false;

  async function pickFolder() {
    const dir = await native.selectDirectory('Select the save folder to track');
    if (dir) newPath = dir;
  }

  async function addGame() {
    if (!newName || !newPath || adding) return;
    adding = true;
    try {
      const game = await api.post('/api/games', { name: newName, savePath: newPath });
      toast(`Now tracking "${newName}"`, 'success');
      newName = '';
      newPath = '';
      showAdd = false;
      navigate('game', { gameId: game.id });
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      adding = false;
    }
  }

  // ── Auto-scan ────────────────────────────────────────────────────
  let scanning = false;
  let scanResults = null;

  // ── Auto-scan overlay ────────────────────────────────────────────
  let scanOpen = false;
  let scanFilter = '';
  let scanType = 'all';
  let selected = new Set();
  let selectedCount = 0; // reactive mirror of selected.size

  async function scan() {
    scanning = true;
    scanOpen = true;
    scanResults = null;
    selected = new Set();
    selectedCount = 0;
    try {
      scanResults = await api.get('/api/presets/scan');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      scanning = false;
    }
  }

  function closeScan() {
    scanOpen = false;
    scanResults = null;
  }

  function onKeydown(e) {
    if (e.key === 'Escape' && scanOpen) closeScan();
  }

  $: trackedPaths = new Set($gameList.map((g) => (g.savePath ?? '').toLowerCase()));
  $: filteredResults = (scanResults ?? []).filter((r) => {
    if (trackedPaths.has((r.savePath ?? '').toLowerCase())) return false;
    if (scanType !== 'all' && r.type !== scanType) return false;
    if (scanFilter && !`${r.name} ${r.savePath}`.toLowerCase().includes(scanFilter.toLowerCase())) return false;
    return true;
  });
  $: scanCounts = {
    all: (scanResults ?? []).length,
    emulator: (scanResults ?? []).filter((r) => r.type === 'emulator').length,
    repack: (scanResults ?? []).filter((r) => r.type === 'repack').length,
    game: (scanResults ?? []).filter((r) => r.type === 'game').length
  };

  function toggleSelect(id) {
    if (selected.has(id)) selected.delete(id);
    else selected.add(id);
    selected = selected; // trigger reactivity
    selectedCount = selected.size;
  }
  function selectAllVisible() {
    for (const r of filteredResults) selected.add(r.id);
    selected = selected;
    selectedCount = selected.size;
  }
  function clearSelection() {
    selected = new Set();
    selectedCount = 0;
  }

  async function trackDetected(item, keepOpen = false) {
    try {
      await api.post('/api/games', { name: item.name, savePath: item.savePath, appId: item.appId ?? '' });
      if (scanResults) scanResults = scanResults.filter((r) => r.id !== item.id);
      selected.delete(item.id);
      selectedCount = selected.size;
      if (!keepOpen) toast(`Now tracking "${item.name}"`, 'success');
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  async function trackSelected() {
    const items = (scanResults ?? []).filter((r) => selected.has(r.id));
    if (items.length === 0) return;
    let ok = 0;
    for (const item of items) {
      try {
        await api.post('/api/games', { name: item.name, savePath: item.savePath, appId: item.appId ?? '' });
        ok++;
      } catch {}
    }
    scanResults = (scanResults ?? []).filter((r) => !selected.has(r.id));
    clearSelection();
    toast(`Tracked ${ok} game${ok === 1 ? '' : 's'}`, 'success');
    if (filteredResults.length === 0) closeScan();
  }

  async function syncAll() {
    for (const g of $gameList) {
      api.post(`/api/games/${g.id}/sync`).catch(() => {});
    }
    toast('Sync triggered for all games');
  }

  $: onlinePeers = Object.values($peers).filter((p) => p.status === 'online');
  const typeLabels = { emulator: 'Emulator', repack: 'Repack', game: 'Game' };
  // Vertical Steam library box-art (portrait) for the cover grid.
  const portraitUrl = (appId) =>
    `https://cdn.cloudflare.steamstatic.com/steam/apps/${appId}/library_600x900.jpg`;
  const typeIcon = (t) => (t === 'emulator' ? '🕹️' : t === 'repack' ? '📦' : '🎮');
</script>

<svelte:window on:keydown={onKeydown} />

<div class="head">
  <h2 class="page-title">Home</h2>
  <div class="head-actions">
    <button class="btn" on:click={scan} disabled={scanning}>
      {scanning ? 'Scanning…' : '🔍 Auto-scan'}
    </button>
    <button class="btn" on:click={syncAll} disabled={$gameList.length === 0}>⟳ Sync all</button>
    <button class="btn primary" on:click={() => (showAdd = !showAdd)}>+ Track folder</button>
  </div>
</div>

{#if showAdd}
  <div class="card add-card">
    <h3>Track a save folder</h3>
    <div class="row">
      <div class="field grow">
        <label for="g-name">Game name</label>
        <input id="g-name" placeholder="e.g. Elden Ring" bind:value={newName} on:keydown={(e) => e.key === 'Enter' && addGame()} />
      </div>
      <div class="field grow2">
        <label for="g-path">Save folder or file</label>
        <div class="path-row">
          <input id="g-path" placeholder="C:\Users\you\AppData\…" bind:value={newPath} on:keydown={(e) => e.key === 'Enter' && addGame()} />
          <button class="btn" on:click={pickFolder}>Browse</button>
        </div>
      </div>
    </div>
    <div class="add-actions">
      <button class="btn" on:click={() => (showAdd = false)}>Cancel</button>
      <button class="btn primary" disabled={!newName || !newPath || adding} on:click={addGame}>
        {adding ? 'Adding…' : 'Start tracking'}
      </button>
    </div>
  </div>
{/if}

{#if scanOpen}
  <div class="scan-overlay" on:click|self={closeScan} on:keydown={(e) => e.key === 'Escape' && closeScan()} role="presentation">
    <div class="scan-modal">
      <div class="scan-modal-head">
        <div>
          <h2>🔍 Auto-scan results</h2>
          <p class="scan-modal-sub">
            {#if scanning}Scanning your system…{:else}Found {scanCounts.all} save location{scanCounts.all === 1 ? '' : 's'} — {filteredResults.length} available to track{/if}
          </p>
        </div>
        <button class="btn icon" on:click={closeScan} title="Close">✕</button>
      </div>

      {#if scanning}
        <div class="scan-loading"><span class="cspin"></span> Scanning Steam, emulators, and configured folders…</div>
      {:else}
        <div class="scan-toolbar">
          <input class="scan-search" placeholder="Filter by name or path…" bind:value={scanFilter} />
          <div class="scan-type-tabs">
            {#each [['all', 'All'], ['game', 'Games'], ['emulator', 'Emulators'], ['repack', 'Repacks']] as [id, label]}
              <button class:active={scanType === id} on:click={() => (scanType = id)}>
                {label} <span class="count">{scanCounts[id]}</span>
              </button>
            {/each}
          </div>
        </div>

        <div class="scan-modal-list">
          <div class="scan-grid">
            {#each filteredResults as item (item.id)}
              <div
                class="cover-tile"
                class:sel={selected.has(item.id)}
                on:click={() => toggleSelect(item.id)}
                on:keydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), toggleSelect(item.id))}
                role="button"
                tabindex="0"
                title={item.savePath}
              >
                <div class="cover-art">
                  {#if item.appId}
                    <img
                      src={portraitUrl(item.appId)}
                      alt={item.name}
                      loading="lazy"
                      on:error={(e) => (e.currentTarget.style.display = 'none')}
                    />
                  {/if}
                  <div class="cover-fallback">
                    <span class="cover-emoji">{typeIcon(item.type)}</span>
                    <span class="cover-fallback-name">{item.name}</span>
                  </div>

                  {#if selected.has(item.id)}
                    <div class="cover-check">✓</div>
                  {/if}
                  <span class="cover-type">{typeLabels[item.type] ?? item.type}</span>

                  <div class="cover-hover">
                    <button class="btn small primary" on:click|stopPropagation={() => trackDetected(item)}>Track</button>
                  </div>
                </div>
                <div class="cover-name" title={item.name}>{item.name}</div>
              </div>
            {:else}
              <div class="scan-empty">
                {scanCounts.all === 0 ? 'Nothing detected. You can still track any folder manually.' : 'No matches for this filter.'}
              </div>
            {/each}
          </div>
        </div>

        <div class="scan-modal-foot">
          <div class="scan-select-actions">
            <button class="btn small" on:click={selectAllVisible} disabled={filteredResults.length === 0}>Select all ({filteredResults.length})</button>
            {#if selectedCount > 0}
              <button class="btn small" on:click={clearSelection}>Clear</button>
            {/if}
          </div>
          <button class="btn primary" disabled={selectedCount === 0} on:click={trackSelected}>
            Track selected ({selectedCount})
          </button>
        </div>
      {/if}
    </div>
  </div>
{/if}

<div class="stats">
  <div class="card stat">
    <div class="stat-num">{$gameList.length}</div>
    <div class="stat-label">games tracked</div>
  </div>
  <div class="card stat">
    <div class="stat-num">{onlinePeers.length}</div>
    <div class="stat-label">peers online</div>
  </div>
  <div class="card stat">
    <div class="stat-num">{Object.values($syncActivity).filter((s) => s.state === 'running').length}</div>
    <div class="stat-label">active syncs</div>
  </div>
</div>

{#if $gameList.length === 0}
  <div class="welcome">
    <div class="welcome-icon">🎮</div>
    <h3>Welcome to OpenSave</h3>
    <p>Keep your game saves in sync across every device — no accounts, no cloud lock-in. Start by finding your saves:</p>
    <div class="welcome-actions">
      <button class="btn primary" on:click={scan} disabled={scanning}>
        {scanning ? 'Scanning…' : '🔍 Auto-scan for saves'}
      </button>
      <button class="btn" on:click={() => (showAdd = true)}>+ Track a folder manually</button>
    </div>
    <p class="welcome-hint">Then open <strong>Devices</strong> to pair another PC or Steam Deck, or <strong>Cloud Backup</strong> to mirror snapshots online.</p>
  </div>
{:else}
  <h3 class="section">Library</h3>
  <div class="grid">
    {#each $gameList as game (game.id)}
      <button class="card game-card" on:click={() => navigate('game', { gameId: game.id })}>
        <div class="gc-cover">
          {#if game.coverUrl}
            <img
              src={game.coverUrl}
              alt=""
              loading="lazy"
              on:load={(e) => (e.currentTarget.style.display = '')}
              on:error={(e) => (e.currentTarget.style.display = 'none')}
            />
          {/if}
          <div class="gc-cover-fallback"><span>{game.name}</span></div>
        </div>
        <div class="gc-body">
          <div class="gc-name">{game.name}</div>
          <div class="gc-meta">
            branch <strong>{game.activeBranch}</strong>
            · {Object.values(game.branches ?? {}).reduce((n, b) => n + (b.snapshots?.length ?? 0), 0)} snapshots
          </div>
          <div class="gc-path" title={game.savePath}>{game.savePath}</div>
          {#if $syncActivity[game.id]?.state === 'running'}
            <div class="gc-sync">syncing… {$syncActivity[game.id].percentage ?? 0}%</div>
          {/if}
        </div>
      </button>
    {/each}
  </div>
{/if}

<style>
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 20px;
    gap: 12px;
    flex-wrap: wrap;
  }
  .head-actions {
    display: flex;
    gap: 8px;
  }
  .add-card {
    margin-bottom: 20px;
  }
  .add-card h3 {
    margin-bottom: 14px;
  }
  .row {
    display: flex;
    gap: 12px;
    flex-wrap: wrap;
  }
  .grow {
    flex: 1;
    min-width: 180px;
  }
  .grow2 {
    flex: 2;
    min-width: 260px;
  }
  .path-row {
    display: flex;
    gap: 8px;
  }
  .path-row input {
    flex: 1;
  }
  .add-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  /* Auto-scan overlay */
  .scan-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.62);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 80;
    padding: 32px;
  }
  .scan-modal {
    width: min(920px, 100%);
    height: min(82vh, 860px);
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-lg);
    display: flex;
    flex-direction: column;
    box-shadow: 0 20px 60px rgba(0, 0, 0, 0.5);
  }
  .scan-modal-head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    padding: 20px 22px 14px;
    border-bottom: 1px solid var(--border);
  }
  .scan-modal-head h2 {
    font-size: 1.2rem;
  }
  .scan-modal-sub {
    font-size: 0.84rem;
    color: var(--text-faint);
    margin-top: 3px;
  }
  .scan-loading {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 10px;
    color: var(--text-dim);
  }
  .cspin {
    width: 14px;
    height: 14px;
    border: 2px solid var(--accent-soft);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  .scan-toolbar {
    display: flex;
    gap: 12px;
    padding: 14px 22px;
    align-items: center;
    flex-wrap: wrap;
  }
  .scan-search {
    flex: 1;
    min-width: 200px;
    padding: 9px 13px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    color: var(--text);
    outline: none;
  }
  .scan-type-tabs {
    display: flex;
    gap: 6px;
  }
  .scan-type-tabs button {
    padding: 7px 13px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: transparent;
    color: var(--text-dim);
    font-size: 0.85rem;
    cursor: pointer;
  }
  .scan-type-tabs button:hover {
    background: var(--bg-hover);
  }
  .scan-type-tabs button.active {
    background: var(--bg-active);
    color: var(--text);
    border-color: var(--border-strong);
  }
  .scan-type-tabs .count {
    color: var(--text-faint);
    font-size: 0.75rem;
    margin-left: 2px;
  }
  .scan-modal-list {
    flex: 1;
    overflow-y: auto;
    padding: 4px 22px 8px;
  }
  .scan-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 16px;
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
  .cover-tile.sel .cover-art {
    border-color: var(--accent);
    box-shadow: 0 0 0 2px var(--accent-soft);
  }
  .cover-art img {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
    z-index: 2;
  }
  /* No App ID (no img) or a failed image (hidden on error) reveals the
     gradient fallback sitting underneath at a lower z-index. */
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
  .cover-check {
    position: absolute;
    top: 8px;
    left: 8px;
    z-index: 3;
    width: 24px;
    height: 24px;
    border-radius: 50%;
    background: var(--accent);
    color: #fff;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.8rem;
    font-weight: 700;
    box-shadow: 0 2px 6px rgba(0, 0, 0, 0.4);
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
  .cover-tile.sel .cover-name {
    color: var(--text);
  }
  .scan-empty {
    grid-column: 1 / -1;
    text-align: center;
    color: var(--text-faint);
    padding: 50px 20px;
  }
  .scan-modal-foot {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 14px 22px;
    border-top: 1px solid var(--border);
  }
  .scan-select-actions {
    display: flex;
    gap: 8px;
  }

  .stats {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 12px;
    margin-bottom: 26px;
  }
  .stat {
    text-align: center;
    padding: 18px;
  }
  .stat-num {
    font-size: 1.8rem;
    font-weight: 700;
  }
  .stat-label {
    color: var(--text-faint);
    font-size: 0.82rem;
  }

  .welcome {
    text-align: center;
    max-width: 520px;
    margin: 40px auto;
    padding: 40px 28px;
    background: var(--bg-raised);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
  }
  .welcome-icon {
    font-size: 3rem;
    margin-bottom: 10px;
  }
  .welcome h3 {
    font-size: 1.3rem;
    font-weight: 700;
    margin-bottom: 10px;
  }
  .welcome p {
    color: var(--text-dim);
    font-size: 0.92rem;
    line-height: 1.55;
  }
  .welcome-actions {
    display: flex;
    gap: 10px;
    justify-content: center;
    margin: 22px 0 6px;
    flex-wrap: wrap;
  }
  .welcome-hint {
    font-size: 0.8rem;
    color: var(--text-faint);
    margin-top: 16px;
  }

  .section {
    margin-bottom: 12px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    gap: 12px;
  }
  .game-card {
    text-align: left;
    cursor: pointer;
    color: var(--text);
    transition: border-color 0.12s, transform 0.12s;
    padding: 0;
    overflow: hidden;
  }
  .game-card:hover {
    border-color: var(--border-strong);
    transform: translateY(-1px);
  }
  .gc-cover {
    position: relative;
    aspect-ratio: 460 / 175;
    background: var(--bg);
    border-bottom: 1px solid var(--border);
  }
  .gc-cover img {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
    z-index: 1;
  }
  .gc-cover-fallback {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 12px;
    background: linear-gradient(135deg, rgba(138, 99, 244, 0.16), rgba(138, 99, 244, 0.04));
  }
  .gc-cover-fallback span {
    font-weight: 700;
    font-size: 1.05rem;
    color: var(--text-dim);
    text-align: center;
    overflow: hidden;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
  }
  .gc-body {
    padding: 14px 16px;
  }
  .gc-name {
    font-weight: 600;
    margin-bottom: 6px;
  }
  .gc-meta {
    font-size: 0.8rem;
    color: var(--text-dim);
    margin-bottom: 6px;
  }
  .gc-path {
    font-size: 0.73rem;
    color: var(--text-faint);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .gc-sync {
    margin-top: 8px;
    font-size: 0.78rem;
    color: var(--accent);
    font-weight: 600;
  }
</style>
