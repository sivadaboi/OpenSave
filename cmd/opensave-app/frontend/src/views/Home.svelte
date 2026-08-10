<script>
  import { gameList, peers, navigate, toast, syncActivity, askConfirm } from '../lib/stores.js';
  import { api, native, coverURL, gameCover } from '../lib/api.js';
  import { backdropClose } from '../lib/backdrop.js';
  // The scan screen's decisions live in a plain module so they can be tested
  // without opening the app — see scan.test.js, where each case is a mistake
  // that actually reached a build.
  import {
    buildGroups,
    contentsLabel,
    isEmptyResult,
    normPath,
    plannedGames,
    rootNameFor
  } from '../lib/scan.js';

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

  // Dismissing a scan result. The exclude list already existed, but only as a
  // folder picker buried in Settings — which meant answering "stop offering
  // me this" required leaving the scan, then finding and re-typing a path you
  // were just looking at. The decision is made here, looking at the result,
  // so it should be actionable here.
  let excluding = null;
  async function excludeResult(item) {
    if (excluding) return;
    const ok = await askConfirm(
      `Stop offering "${item.name}" in future scans? Nothing on disk is touched — this only tells the scanner to skip ${item.savePath}. You can undo it under Settings → Excluded folders.`,
      { title: 'Exclude from scans?', confirmText: 'Exclude' }
    );
    if (!ok) return;
    excluding = item.id;
    try {
      // Read-modify-write against current settings rather than a stored copy:
      // the scan overlay can be open for a while, and clobbering a change
      // made elsewhere in the meantime would be a silent settings loss.
      const current = await api.get('/api/settings');
      const paths = current.excludePaths ?? [];
      if (!paths.includes(item.savePath)) {
        await api.post('/api/settings', { excludePaths: [...paths, item.savePath] });
      }
      scanResults = (scanResults ?? []).filter((r) => r.id !== item.id);
      selected.delete(item.id);
      selected = selected;
      toast(`"${item.name}" won't be offered again`, 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      excluding = null;
    }
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
  let showTracked = false; // include saves already being tracked
  // Folders with nothing in them are hidden by default — a fifth of a real
  // machine's results, mostly userdata shells Steam creates for every game
  // you own. Kept reachable, since tracking a folder before the game's first
  // save is a legitimate thing to want.
  let showEmpty = false;
  // Which game's folder list is open. One at a time: the panel spans the grid
  // and several open at once turns the tiles into a wall of paths.
  let expandedGroup = null;

  async function scan() {
    scanning = true;
    scanOpen = true;
    scanResults = null;
    selected = new Set();
    selectedCount = 0;
    try {
      const results = await api.get('/api/presets/scan');
      fillMissingAppIds(results);
      scanResults = results;
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      scanning = false;
    }
  }

  // The same game can turn up at several locations, but only some entries
  // carry a Steam App ID (and therefore cover art). Share each App ID across
  // every same-named entry so duplicates all show the same artwork instead
  // of one having a cover and the other a blank tile.
  function fillMissingAppIds(results) {
    if (!results) return;
    const byName = new Map();
    for (const r of results) {
      const key = (r.name ?? '').trim().toLowerCase();
      if (r.appId && key && !byName.has(key)) byName.set(key, r.appId);
    }
    for (const r of results) {
      if (r.appId) continue;
      const key = (r.name ?? '').trim().toLowerCase();
      if (byName.has(key)) r.appId = byName.get(key);
    }
  }

  function closeScan() {
    scanOpen = false;
    scanResults = null;
  }

  function onKeydown(e) {
    if (e.key === 'Escape' && scanOpen) closeScan();
  }

  $: trackedPaths = new Set($gameList.map((g) => normPath(g.savePath)));
  const isTracked = (r) => trackedPaths.has(normPath(r.savePath));
  // Note: reference trackedPaths directly (not via isTracked) so Svelte sees
  // it as a dependency and refreshes the list when tracked-state changes.

  // Everything the type tabs and the counts describe. Empty folders are out
  // of the pool entirely unless asked for, so the tab counts match what the
  // grid shows rather than counting rows nobody can see.
  $: scanPool = (scanResults ?? []).filter((r) => showEmpty || !isEmptyResult(r));
  $: emptyCount = (scanResults ?? []).filter(isEmptyResult).length;

  $: filteredResults = scanPool.filter((r) => {
    if (!showTracked && trackedPaths.has(normPath(r.savePath))) return false;
    if (scanType !== 'all' && r.type !== scanType) return false;
    if (scanFilter && !`${r.name} ${r.savePath}`.toLowerCase().includes(scanFilter.toLowerCase())) return false;
    return true;
  });
  // Keep the saves you can actually add at the top and push already-tracked
  // ones below a divider — with "Show tracked" on, interleaving them buries
  // the actionable entries in a wall of tiles.
  $: allGroups = buildGroups(filteredResults, trackedPaths);
  $: availableGroups = allGroups.filter((g) => !trackedPaths.has(normPath(g.primary.savePath)));
  $: trackedGroups = allGroups.filter((g) => trackedPaths.has(normPath(g.primary.savePath)));
  $: orderedGroups = [...availableGroups, ...trackedGroups];
  $: shownAvailable = availableGroups.length;
  $: scanCounts = {
    all: scanPool.length,
    emulator: scanPool.filter((r) => r.type === 'emulator').length,
    repack: scanPool.filter((r) => r.type === 'repack').length,
    game: scanPool.filter((r) => r.type === 'game').length
  };

  function toggleSelect(id) {
    if (selected.has(id)) selected.delete(id);
    else selected.add(id);
    selected = selected; // trigger reactivity
    selectedCount = selected.size;
  }
  // Clicking a game picks the folders the daemon suggests for it — the save
  // folder plus anything sitting beside it that belongs to the same save.
  // Folders it flagged as already covered, or as another install's leftovers,
  // are left for you to tick yourself.
  function toggleGroup(group) {
    const on = group.suggested.every((m) => selected.has(m.id));
    for (const m of group.suggested) {
      if (on) selected.delete(m.id);
      else selected.add(m.id);
    }
    selected = selected;
    selectedCount = selected.size;
  }
  const groupSelected = (group, sel) => group.members.some((m) => sel.has(m.id));
  function selectAllVisible() {
    for (const g of availableGroups) for (const m of g.suggested) selected.add(m.id);
    selected = selected;
    selectedCount = selected.size;
  }
  function clearSelection() {
    selected = new Set();
    selectedCount = 0;
  }

  // Track one game from a set of folders: the first is the save folder and the
  // rest become its extra locations. Adding a location can legitimately fail —
  // the daemon refuses one that sits inside the save folder, because two
  // locations over the same files fight over them — so those are collected and
  // reported rather than swallowed.
  async function trackAsOneGame(primary, extras) {
    const created = await api.post('/api/games', {
      name: primary.name,
      savePath: primary.savePath,
      appId: primary.appId ?? ''
    });
    const gameId = created?.id;
    const failed = [];
    if (gameId) {
      const taken = new Set();
      for (const e of extras) {
        try {
          await api.post(`/api/games/${gameId}/roots`, {
            name: rootNameFor(e.savePath, taken),
            path: e.savePath
          });
        } catch (err) {
          failed.push(`${e.savePath}: ${err.message}`);
        }
      }
    }
    return { gameId, failed };
  }

  // Drop the rows just tracked. With "Show tracked" on they stay and re-render
  // as tracked tiles — removing them would make what you just tracked vanish
  // from a list whose whole point is showing tracked saves.
  function consumeRows(ids) {
    if (scanResults && !showTracked) scanResults = scanResults.filter((r) => !ids.has(r.id));
    for (const id of ids) selected.delete(id);
    selected = selected;
    selectedCount = selected.size;
  }

  async function trackGroup(group) {
    const [primary, ...extras] = group.suggested;
    try {
      const { failed } = await trackAsOneGame(primary, extras);
      consumeRows(new Set(group.suggested.map((m) => m.id)));
      if (failed.length > 0) {
        toast(`Tracking "${primary.name}" — ${failed.length} folder(s) could not be added: ${failed[0]}`, 'error');
      } else if (extras.length > 0) {
        toast(`Now tracking "${primary.name}" across ${extras.length + 1} folders`, 'success');
      } else {
        toast(`Now tracking "${primary.name}"`, 'success');
      }
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  async function trackSelected() {
    const items = (scanResults ?? []).filter((r) => selected.has(r.id));
    if (items.length === 0) return;

    // Folders of one game go in as one game with locations, not as several
    // games that happen to share a name. That is the whole point of grouping:
    // tracking Scores, Tracks and Profiles separately gives you three library
    // entries that each sync a third of a save.
    let games = 0;
    let locations = 0;
    const problems = [];
    for (const { primary, extras } of plannedGames(items)) {
      try {
        const { failed } = await trackAsOneGame(primary, extras);
        games++;
        locations += extras.length - failed.length;
        problems.push(...failed);
      } catch (e) {
        problems.push(`${primary.name}: ${e.message}`);
      }
    }

    consumeRows(new Set(items.map((i) => i.id)));
    clearSelection();
    const extra = locations > 0 ? ` with ${locations} extra location${locations === 1 ? '' : 's'}` : '';
    toast(`Tracked ${games} game${games === 1 ? '' : 's'}${extra}`, problems.length > 0 ? 'error' : 'success');
    if (problems.length > 0) toast(problems[0], 'error');
    if (filteredResults.length === 0) closeScan();
  }

  // The escape hatch: fold whatever is ticked into one game, whatever the
  // daemon decided. It gets the grouping right most of the time and cannot get
  // it right always — two folders of one game with unrelated names and no
  // AppID have nothing to match on.
  async function mergeSelectedIntoOneGame() {
    const items = (scanResults ?? []).filter((r) => selected.has(r.id));
    if (items.length < 2) return;
    // The most recently written folder leads, matching how the daemon picks a
    // group's primary — the freshest is the save actually being played.
    const ordered = [...items].sort((a, b) => (b.latestMtime ?? 0) - (a.latestMtime ?? 0));
    const [primary, ...extras] = ordered;
    try {
      const { failed } = await trackAsOneGame(primary, extras);
      consumeRows(new Set(items.map((i) => i.id)));
      clearSelection();
      if (failed.length > 0) toast(`Tracked "${primary.name}", but ${failed.length} folder(s) failed: ${failed[0]}`, 'error');
      else toast(`Tracking "${primary.name}" across ${items.length} folders`, 'success');
      if (filteredResults.length === 0) closeScan();
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  async function syncAll() {
    for (const g of $gameList) {
      api.post(`/api/games/${g.id}/sync`).catch(() => {});
    }
    toast('Sync triggered for all games');
  }

  // ── Library multi-select ─────────────────────────────────────────
  // Lets the user act on several games at once (e.g. clear a batch of
  // wrongly-tracked entries) without navigating in and out of each one.
  let selectMode = false;
  let libSelected = new Set();
  let libSelectedCount = 0;

  function toggleSelectMode() {
    selectMode = !selectMode;
    if (!selectMode) clearLibSelection();
  }
  function toggleLibSelect(id) {
    if (libSelected.has(id)) libSelected.delete(id);
    else libSelected.add(id);
    libSelected = libSelected;
    libSelectedCount = libSelected.size;
  }
  function selectAllGames() {
    for (const g of $gameList) libSelected.add(g.id);
    libSelected = libSelected;
    libSelectedCount = libSelected.size;
  }
  function clearLibSelection() {
    libSelected = new Set();
    libSelectedCount = 0;
  }
  $: allSelected = $gameList.length > 0 && libSelectedCount === $gameList.length;
  function toggleSelectAll() {
    if (allSelected) clearLibSelection();
    else selectAllGames();
  }
  async function untrackSelectedGames() {
    const n = libSelectedCount;
    if (n === 0) return;
    const ok = await askConfirm(
      `Untrack ${n} selected game${n === 1 ? '' : 's'}? They'll be removed from your library. Your save snapshots on disk are kept — nothing is deleted.`,
      { title: 'Untrack selected?', confirmText: `Untrack ${n}`, danger: true }
    );
    if (!ok) return;
    try {
      const res = await api.post('/api/games/untrack-bulk', { ids: [...libSelected] });
      toast(`Untracked ${res.untracked} game${res.untracked === 1 ? '' : 's'} — snapshots kept`, 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      selectMode = false;
      clearLibSelection();
    }
  }

  $: onlinePeers = Object.values($peers).filter((p) => p.status === 'online');
  const typeLabels = { emulator: 'Emulator', repack: 'Repack', game: 'Game' };
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
  <div class="scan-overlay" use:backdropClose={closeScan} on:keydown={(e) => e.key === 'Escape' && closeScan()} role="presentation">
    <div class="scan-modal">
      <div class="scan-modal-head">
        <div>
          <h2>🔍 Auto-scan results</h2>
          <p class="scan-modal-sub">
            {#if scanning}Scanning your system…{:else}Found {scanCounts.all} save location{scanCounts.all === 1 ? '' : 's'} — {shownAvailable} available to track{#if emptyCount > 0 && !showEmpty}, {emptyCount} empty hidden{/if}{/if}
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
          <label class="scan-show-tracked" title="Also show saves you already track">
            <input type="checkbox" bind:checked={showTracked} />
            Show tracked
          </label>
          {#if emptyCount > 0}
            <label class="scan-show-tracked" title="Folders that exist but hold no files. Steam creates one for every game you own, whether or not saves go there.">
              <input type="checkbox" bind:checked={showEmpty} />
              Show {emptyCount} empty
            </label>
          {/if}
        </div>

        <div class="scan-modal-list">
          <div class="scan-grid">
            {#each orderedGroups as group, i (group.id)}
              {@const item = group.primary}
              {#if i === availableGroups.length && trackedGroups.length > 0}
                <div class="scan-divider">
                  Already tracked ({trackedGroups.length})
                </div>
              {/if}
              <div
                class="cover-tile"
                class:sel={groupSelected(group, selected)}
                class:tracked={isTracked(item)}
                class:empty-result={isEmptyResult(item)}
                on:click={() => !isTracked(item) && toggleGroup(group)}
                on:keydown={(e) => !isTracked(item) && (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), toggleGroup(group))}
                role="button"
                tabindex="0"
                title={item.savePath}
              >
                <div class="cover-art">
                  {#if item.appId}
                    <img
                      src={coverURL(item.appId, true)}
                      alt={item.name}
                      loading="lazy"
                      on:error={(e) => (e.currentTarget.style.display = 'none')}
                    />
                  {/if}
                  <div class="cover-fallback">
                    <span class="cover-emoji">{typeIcon(item.type)}</span>
                    <span class="cover-fallback-name">{item.name}</span>
                  </div>

                  {#if groupSelected(group, selected)}
                    <div class="cover-check">✓</div>
                  {/if}
                  <span class="cover-type">{typeLabels[item.type] ?? item.type}</span>

                  {#if isTracked(item)}
                    <span class="cover-tracked">✓ Tracked</span>
                  {:else}
                    <div class="cover-hover">
                      <button class="btn small primary" on:click|stopPropagation={() => trackGroup(group)}>
                        {group.suggested.length > 1 ? `Track all ${group.suggested.length}` : 'Track'}
                      </button>
                      <button
                        class="btn small"
                        disabled={excluding === item.id}
                        title="Stop offering this location in future scans"
                        on:click|stopPropagation={() => excludeResult(item)}
                      >
                        {excluding === item.id ? 'Excluding…' : 'Exclude'}
                      </button>
                    </div>
                  {/if}
                </div>
                <div class="cover-name" title={item.name}>{item.name}</div>
                <div class="cover-meta" title={item.savePath}>{contentsLabel(item)}</div>
                {#if group.extras.length > 0}
                  <button
                    class="cover-folders"
                    class:open={expandedGroup === group.id}
                    on:click|stopPropagation={() => (expandedGroup = expandedGroup === group.id ? null : group.id)}
                  >
                    {expandedGroup === group.id ? '▾' : '▸'} found in {group.members.length} folders
                    {#if group.suggested.length > 1}<span class="cover-folders-hint">· {group.suggested.length} are one save</span>{/if}
                  </button>
                {/if}
              </div>

              {#if expandedGroup === group.id}
                <div class="group-detail">
                  <div class="group-detail-head">
                    <strong>{item.name}</strong> was found in {group.members.length} places.
                    Tick the folders that belong to this save — they are tracked as one game.
                  </div>
                  {#each group.members as m (m.id)}
                    <label class="group-row" class:covered={m.role === 'inside'}>
                      <input
                        type="checkbox"
                        checked={selected.has(m.id)}
                        disabled={m.role === 'inside' || isTracked(m)}
                        on:change={() => toggleSelect(m.id)}
                      />
                      <span class="group-row-main">
                        <span class="group-row-path">{m.savePath}</span>
                        <span class="group-row-meta">
                          {contentsLabel(m)}
                          {#if m.role === 'primary'}<span class="tag tag-primary">the save folder</span>
                          {:else if m.role === 'location'}<span class="tag tag-loc">part of the same save</span>
                          {:else if m.role === 'inside'}<span class="tag">already inside the folder above</span>
                          {:else if m.role === 'alternative'}<span class="tag tag-alt">another copy — probably an old install</span>{/if}
                          {#if isTracked(m)}<span class="tag tag-primary">already tracked</span>{/if}
                        </span>
                      </span>
                    </label>
                  {/each}
                </div>
              {/if}
            {:else}
              <div class="scan-empty">
                {scanCounts.all === 0 ? 'Nothing detected. You can still track any folder manually.' : 'No matches for this filter.'}
              </div>
            {/each}
          </div>
        </div>

        <div class="scan-modal-foot">
          <div class="scan-select-actions">
            <button class="btn small" on:click={selectAllVisible} disabled={availableGroups.length === 0}>Select all ({availableGroups.length})</button>
            {#if selectedCount > 0}
              <button class="btn small" on:click={clearSelection}>Clear</button>
            {/if}
          </div>
          <!-- Both tracking actions sit together on the right: they are the
               two answers to the same question — one game or several — and
               splitting them across the bar made the merge read as a filter. -->
          <div class="scan-track-actions">
            {#if selectedCount > 1}
              <button
                class="btn primary"
                title="Track everything ticked as a single game, whatever it was grouped under. For a split save the scan did not spot."
                on:click={mergeSelectedIntoOneGame}
              >
                Track as one game
              </button>
            {/if}
            <button class="btn primary" disabled={selectedCount === 0} on:click={trackSelected}>
              Track selected ({selectedCount})
            </button>
          </div>
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
  <div class="section-row">
    <h3 class="section">Library</h3>
    {#if selectMode}
      <div class="select-bar">
        <span class="select-count">{libSelectedCount} selected</span>
        <button class="btn small" on:click={toggleSelectAll}>
          {allSelected ? 'Unselect all' : `Select all (${$gameList.length})`}
        </button>
        <button class="btn small danger" disabled={libSelectedCount === 0} on:click={untrackSelectedGames}>
          Untrack selected
        </button>
        <button class="btn small" on:click={toggleSelectMode}>Cancel</button>
      </div>
    {:else}
      <button class="btn small" on:click={toggleSelectMode}>☑ Select</button>
    {/if}
  </div>
  <div class="grid">
    {#each $gameList as game (game.id)}
      <button
        class="card game-card"
        class:selecting={selectMode}
        class:selected={selectMode && libSelected.has(game.id)}
        on:click={() => (selectMode ? toggleLibSelect(game.id) : navigate('game', { gameId: game.id }))}
      >
        {#if selectMode}
          <div class="gc-check" class:on={libSelected.has(game.id)}>{libSelected.has(game.id) ? '✓' : ''}</div>
        {/if}
        <div class="gc-cover">
          {#if gameCover(game)}
            <img
              src={gameCover(game)}
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
    /* Stacked, not side by side: a cover tile is portrait and narrow, and
       two buttons in a row wrap raggedly at the smaller grid sizes. */
    flex-direction: column;
    align-items: stretch;
    justify-content: flex-end;
    gap: 6px;
    padding: 12px;
    opacity: 0;
    background: linear-gradient(to top, rgba(0, 0, 0, 0.75), transparent 55%);
    transition: opacity 0.12s;
  }
  /* The overlay only appears on hover, so a keyboard user tabbing to these
     buttons would otherwise be operating something invisible. */
  .cover-hover:focus-within {
    opacity: 1;
  }
  .cover-tile:hover .cover-hover {
    opacity: 1;
  }
  /* Already-tracked entries (shown when "Show tracked" is on): dimmed,
     non-selectable, badged. */
  .cover-tile.tracked {
    cursor: default;
    opacity: 0.6;
  }
  .cover-tile.tracked:hover .cover-art {
    transform: none;
  }
  .cover-tracked {
    position: absolute;
    left: 8px;
    bottom: 8px;
    z-index: 3;
    padding: 3px 9px;
    border-radius: 999px;
    font-size: 0.68rem;
    font-weight: 700;
    background: var(--accent);
    color: #fff;
  }
  /* Full-width heading separating the already-tracked group from the saves
     you can still add. */
  .scan-divider {
    grid-column: 1 / -1;
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 10px 0 2px;
    color: var(--text-faint);
    font-size: 0.78rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .scan-divider::after {
    content: '';
    flex: 1;
    height: 1px;
    background: var(--border);
  }
  .scan-show-tracked {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 7px 13px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: transparent;
    font-size: 0.85rem;
    color: var(--text-dim);
    white-space: nowrap;
    cursor: pointer;
  }
  .scan-show-tracked:hover {
    background: var(--bg-hover);
  }
  /* A notch smaller than the app-wide 17px: this one sits in a compact pill
     next to 0.85rem text, and the full-size circle makes the pill taller
     than the buttons beside it. */
  .scan-show-tracked input {
    width: 15px;
    height: 15px;
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
  .cover-meta {
    margin-top: 2px;
    font-size: 0.72rem;
    color: var(--text-faint);
    text-align: center;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* An empty folder is shown greyed rather than removed: it is still
     trackable on purpose, for a game that has not saved yet. */
  .cover-tile.empty-result .cover-art {
    opacity: 0.45;
  }
  .cover-tile.empty-result .cover-meta {
    font-style: italic;
  }

  /* ── A game found in more than one place ───────────────────────── */
  .cover-folders {
    margin-top: 4px;
    width: 100%;
    background: none;
    border: 0;
    padding: 2px 0;
    font-size: 0.72rem;
    color: var(--accent);
    cursor: pointer;
    text-align: center;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .cover-folders:hover,
  .cover-folders.open {
    text-decoration: underline;
  }
  .cover-folders-hint {
    color: var(--text-faint);
  }
  /* Spans the grid so the folder list reads as a list, not as a tile. */
  .group-detail {
    grid-column: 1 / -1;
    background: var(--bg-elev, rgba(127, 127, 127, 0.08));
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 12px 14px;
    margin: 2px 0 10px;
  }
  .group-detail-head {
    font-size: 0.8rem;
    color: var(--text-dim);
    margin-bottom: 10px;
  }
  .group-row {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    padding: 6px 4px;
    border-top: 1px solid var(--border);
    cursor: pointer;
  }
  .group-row:first-of-type {
    border-top: 0;
  }
  .group-row.covered {
    opacity: 0.55;
    cursor: default;
  }
  .group-row-main {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .group-row-path {
    font-size: 0.78rem;
    color: var(--text);
    word-break: break-all;
  }
  .group-row-meta {
    font-size: 0.72rem;
    color: var(--text-faint);
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
  }
  .tag {
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0 7px;
    font-size: 0.68rem;
    line-height: 1.5;
    color: var(--text-dim);
  }
  .tag-primary {
    border-color: var(--accent);
    color: var(--accent);
  }
  .tag-loc {
    border-color: var(--ok, var(--accent));
    color: var(--ok, var(--accent));
  }
  .tag-alt {
    border-color: var(--warn, var(--border));
    color: var(--warn, var(--text-dim));
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
  .scan-track-actions {
    display: flex;
    gap: 8px;
    align-items: center;
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
    position: relative;
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
  .game-card.selected {
    border-color: var(--accent);
    box-shadow: 0 0 0 1px var(--accent);
  }
  .gc-check {
    position: absolute;
    top: 8px;
    left: 8px;
    z-index: 3;
    width: 24px;
    height: 24px;
    border-radius: 50%;
    background: rgba(0, 0, 0, 0.55);
    border: 2px solid #fff;
    color: #fff;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.8rem;
    font-weight: 700;
    box-shadow: 0 2px 6px rgba(0, 0, 0, 0.4);
  }
  .gc-check.on {
    background: var(--accent);
    border-color: var(--accent);
  }
  .section-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 12px;
    flex-wrap: wrap;
  }
  .section-row .section {
    margin-bottom: 0;
  }
  .select-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .select-count {
    color: var(--text-dim);
    font-size: 0.85rem;
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
