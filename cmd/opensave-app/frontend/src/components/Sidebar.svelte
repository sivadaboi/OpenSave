<script>
  import { view, navigate, settings, gameList, conflictCount, pairingRequests, syncActivity } from '../lib/stores.js';

  let filter = '';

  const nav = [
    { id: 'home', label: 'Home', icon: 'M3 10.5 L10 4 L17 10.5 M5 9 V16 H8.5 V12 H11.5 V16 H15 V9' },
    { id: 'devices', label: 'Devices', icon: 'M3 6 h9 v7 H3 z M5 15.5 h5 M7.5 13 v2.5 M14 9 h3 v6.5 h-3 z' },
    { id: 'cloud', label: 'Cloud Backup', icon: 'M6 14 a3.5 3.5 0 0 1 0 -7 a4.5 4.5 0 0 1 8.6 1.2 A3 3 0 0 1 14 14 z' },
    { id: 'activity', label: 'Activity', icon: 'M3 10 h3 l2 -5 l3 10 l2 -5 h4' },
    { id: 'settings', label: 'Settings', icon: 'M10 7 a3 3 0 1 0 0 6 a3 3 0 1 0 0 -6 M10 2.5 v2 M10 15.5 v2 M2.5 10 h2 M15.5 10 h2 M4.6 4.6 l1.4 1.4 M14 14 l1.4 1.4 M15.4 4.6 L14 6 M6 14 l-1.4 1.4' }
  ];

  $: deviceName = $settings?.deviceName ?? '…';
  $: filteredGames = $gameList.filter((g) => g.name.toLowerCase().includes(filter.toLowerCase()));
  $: activeSyncs = Object.values($syncActivity).filter((s) => s.state === 'running').length;

  function badgeFor(id) {
    if (id === 'devices' && $pairingRequests.length > 0) return $pairingRequests.length;
    if (id === 'home' && $conflictCount > 0) return $conflictCount;
    return 0;
  }

  function initials(name) {
    return name.split(/\s+/).map((w) => w[0]).join('').slice(0, 2).toUpperCase();
  }
</script>

<aside>
  <div class="profile">
    <div class="avatar">{initials(deviceName)}</div>
    <div class="who">
      <div class="name">{deviceName}</div>
      <div class="sub">this device</div>
    </div>
  </div>

  <nav>
    {#each nav as item}
      <button class:active={$view.name === item.id} on:click={() => navigate(item.id)}>
        <svg width="17" height="17" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d={item.icon} />
        </svg>
        <span>{item.label}</span>
        {#if badgeFor(item.id)}
          <span class="nav-badge">{badgeFor(item.id)}</span>
        {/if}
      </button>
    {/each}
  </nav>

  <div class="library-head">
    <span>MY LIBRARY</span>
    <button class="add" title="Track a game" on:click={() => navigate('home', { add: true })}>+</button>
  </div>

  <input class="filter" placeholder="Filter library" bind:value={filter} />

  <div class="library">
    {#each filteredGames as game (game.id)}
      <button
        class="game"
        class:active={$view.name === 'game' && $view.params.gameId === game.id}
        on:click={() => navigate('game', { gameId: game.id })}
      >
        <span class="thumb">
          <span class="cover-fallback">{initials(game.name)}</span>
          {#if game.coverUrl}
            <img
              src={game.coverUrl}
              alt=""
              on:load={(e) => (e.currentTarget.style.display = '')}
              on:error={(e) => (e.currentTarget.style.display = 'none')}
            />
          {/if}
        </span>
        <span class="game-name">{game.name}</span>
        {#if $syncActivity[game.id]?.state === 'running'}
          <span class="spin" title="Syncing"></span>
        {/if}
      </button>
    {:else}
      <div class="library-empty">
        {$gameList.length === 0 ? 'No games tracked yet' : 'No matches'}
      </div>
    {/each}
  </div>

  {#if activeSyncs > 0}
    <div class="sync-note">
      <span class="spin"></span>
      {activeSyncs} sync{activeSyncs > 1 ? 's' : ''} in progress
    </div>
  {/if}
</aside>

<style>
  aside {
    width: var(--sidebar-w);
    background: var(--bg-sidebar);
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    flex-shrink: 0;
    min-height: 0;
  }
  .profile {
    display: flex;
    align-items: center;
    gap: 11px;
    padding: 16px 16px 12px;
  }
  .avatar {
    width: 38px;
    height: 38px;
    border-radius: 10px;
    background: var(--accent-soft);
    color: var(--accent);
    display: flex;
    align-items: center;
    justify-content: center;
    font-weight: 700;
    font-size: 0.9rem;
  }
  .who .name {
    font-weight: 600;
    font-size: 0.95rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 150px;
  }
  .who .sub {
    font-size: 0.72rem;
    color: var(--text-faint);
  }

  nav {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 4px 10px;
  }
  nav button {
    display: flex;
    align-items: center;
    gap: 11px;
    padding: 9px 12px;
    border: none;
    border-radius: var(--radius);
    background: transparent;
    color: var(--text-dim);
    font-size: 0.93rem;
    font-weight: 500;
    cursor: pointer;
    text-align: left;
  }
  nav button:hover {
    background: var(--bg-hover);
    color: var(--text);
  }
  nav button.active {
    background: var(--bg-active);
    color: var(--text);
  }
  .nav-badge {
    margin-left: auto;
    background: var(--accent);
    color: #fff;
    border-radius: 999px;
    font-size: 0.7rem;
    font-weight: 700;
    padding: 1px 7px;
  }

  .library-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px 18px 8px;
    font-size: 0.72rem;
    font-weight: 700;
    letter-spacing: 0.08em;
    color: var(--text-faint);
  }
  .library-head .add {
    border: none;
    background: transparent;
    color: var(--text-dim);
    font-size: 1.1rem;
    cursor: pointer;
    line-height: 1;
    padding: 2px 6px;
    border-radius: 6px;
  }
  .library-head .add:hover {
    background: var(--bg-hover);
    color: var(--text);
  }

  .filter {
    margin: 0 14px 8px;
    padding: 7px 11px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    font-size: 0.85rem;
    outline: none;
  }
  .filter:focus {
    border-color: var(--border-strong);
  }

  .library {
    flex: 1;
    overflow-y: auto;
    padding: 0 10px 10px;
    min-height: 0;
  }
  .game {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 6px 8px;
    border: none;
    border-radius: 8px;
    background: transparent;
    color: var(--text-dim);
    font-size: 0.88rem;
    cursor: pointer;
    text-align: left;
  }
  .game:hover {
    background: var(--bg-hover);
    color: var(--text);
  }
  .game.active {
    background: var(--bg-active);
    color: var(--text);
  }
  .thumb {
    position: relative;
    width: 24px;
    height: 24px;
    flex-shrink: 0;
  }
  .thumb img {
    position: absolute;
    inset: 0;
    width: 24px;
    height: 24px;
    border-radius: 6px;
    object-fit: cover;
  }
  .cover-fallback {
    position: absolute;
    inset: 0;
    border-radius: 6px;
    background: var(--bg-active);
    color: var(--text-faint);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.62rem;
    font-weight: 700;
  }
  .game-name {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .library-empty {
    padding: 18px 10px;
    color: var(--text-faint);
    font-size: 0.82rem;
    text-align: center;
  }

  .sync-note {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 18px;
    border-top: 1px solid var(--border);
    color: var(--text-dim);
    font-size: 0.8rem;
  }
  .spin {
    width: 11px;
    height: 11px;
    border: 2px solid var(--accent-soft);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
    flex-shrink: 0;
    margin-left: auto;
  }
  .sync-note .spin {
    margin-left: 0;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
