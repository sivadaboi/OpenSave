<script>
  import { onMount } from 'svelte';
  import { initApi, connectWS, native } from './lib/api.js';
  import { applyMessage, wsConnected, view, appUpdate, toast, showAbout, aboutChangelogOpen } from './lib/stores.js';

  import logoUrl from './assets/logo.svg';
  import TitleBar from './components/TitleBar.svelte';
  import Sidebar from './components/Sidebar.svelte';
  import StatusBar from './components/StatusBar.svelte';
  import Toasts from './components/Toasts.svelte';
  import ConflictModal from './components/ConflictModal.svelte';
  import PairingBanner from './components/PairingBanner.svelte';
  import ConfirmDialog from './components/ConfirmDialog.svelte';

  import Home from './views/Home.svelte';
  import GameDetail from './views/GameDetail.svelte';
  import Devices from './views/Devices.svelte';
  import CloudBackup from './views/CloudBackup.svelte';
  import Settings from './views/Settings.svelte';
  import ActivityLog from './views/ActivityLog.svelte';

  let ready = false;
  let bootError = '';
  let retrying = false;
  let update = null; // {available, latest, url, assetUrl?, notes?} when a newer release exists
  let showNotes = false;
  let installStarted = false;

  async function installRelease() {
    if (!update?.assetUrl || installStarted) return;
    installStarted = true;
    const err = await native.installFromUrl(update.assetUrl);
    if (err) {
      toast(err, 'error');
      installStarted = false;
    }
  }

  async function boot() {
    bootError = '';
    retrying = true;
    try {
      await initApi();
      connectWS(applyMessage, (up) => wsConnected.set(up));
      ready = true;
    } catch (e) {
      bootError = e.message;
    }
    retrying = false;
  }

  let updatedTo = ''; // set when this launch is the first on a new build

  function openWhatsNew() {
    aboutChangelogOpen.set(true);
    showAbout.set(true);
    updatedTo = '';
  }

  onMount(async () => {
    await boot();
    // First launch after an update (including peer-to-peer, which carries
    // no release notes): announce it and offer the embedded changelog.
    try {
      const g = await native.updateGreeting();
      if (g?.updatedFrom) {
        const info = await native.appInfo();
        updatedTo = info?.version ?? '';
      }
    } catch {}
    // Non-blocking: never let an update check affect startup. Re-check
    // every 6h too — the app lives in the tray for weeks at a time.
    const check = async () => {
      try {
        const res = await native.checkUpdate();
        if (res?.available) update = res;
      } catch {}
    };
    await check();
    setInterval(check, 6 * 3600 * 1000);
  });

  const views = {
    home: Home,
    game: GameDetail,
    devices: Devices,
    // 'internet' is kept as an alias so any deep-link opens Devices on its
    // Over-the-internet tab (Internet Sync now lives there).
    internet: Devices,
    cloud: CloudBackup,
    settings: Settings,
    activity: ActivityLog
  };
</script>

<div class="shell">
  <TitleBar />
  {#if $appUpdate && $appUpdate.state !== 'error'}
    <div class="update-banner installing">
      <span>
        {#if $appUpdate.state === 'downloading'}
          ⬇️ Updating OpenSave — downloading {$appUpdate.percentage ?? 0}%…
        {:else if $appUpdate.state === 'installing'}
          🔧 Installing update…
        {:else}
          🔄 Restarting with the new version…
        {/if}
        <em>The app restarts itself when done — your games keep syncing.</em>
      </span>
    </div>
  {:else if update}
    <div class="update-banner">
      <span>🎉 OpenSave {update.latest} is available — you're on {update.current}.</span>
      <div class="update-actions">
        {#if update.notes}
          <button class="dismiss" on:click={() => (showNotes = !showNotes)}>
            {showNotes ? 'Hide notes' : "What's new"}
          </button>
        {/if}
        {#if update.assetUrl}
          <button class="link" disabled={installStarted} on:click={installRelease}>
            {installStarted ? 'Starting…' : 'Install & restart'}
          </button>
        {:else if update.flatpak}
          <button class="link" on:click={() => native.openExternal(update.url)}>Get .flatpak</button>
        {:else}
          <button class="link" on:click={() => native.openExternal(update.url)}>Download</button>
        {/if}
        <button class="dismiss" on:click={() => (update = null)} aria-label="Dismiss">✕</button>
      </div>
    </div>
    {#if showNotes && update.notes}
      <div class="update-notes">{update.notes}</div>
    {/if}
  {:else if updatedTo}
    <div class="update-banner">
      <span>🎉 OpenSave was updated to v{updatedTo}.</span>
      <div class="update-actions">
        <button class="link" on:click={openWhatsNew}>What's new</button>
        <button class="dismiss" on:click={() => (updatedTo = '')} aria-label="Dismiss">✕</button>
      </div>
    </div>
  {/if}
  <div class="body">
    {#if bootError}
      <div class="boot-error">
        <img class="boot-logo" src={logoUrl} alt="OpenSave" />
        <h2>OpenSave failed to start</h2>
        <p>{bootError}</p>
        <button class="btn primary" disabled={retrying} on:click={boot}>
          {retrying ? 'Retrying…' : 'Retry'}
        </button>
        <p class="boot-hint">
          Details are saved to <code>.opensave\opensave.log</code> in your user folder.
        </p>
      </div>
    {:else if ready}
      <Sidebar />
      <main>
        <svelte:component this={views[$view.name] ?? Home} params={$view.params} />
      </main>
    {:else}
      <div class="boot-loading">
        <img class="boot-logo pulse" src={logoUrl} alt="OpenSave" />
        <span>Starting OpenSave…</span>
      </div>
    {/if}
  </div>
  <StatusBar />
  <Toasts />
  <ConflictModal />
  <ConfirmDialog />
  {#if ready}<PairingBanner />{/if}
</div>

<style>
  .shell {
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  .body {
    flex: 1;
    display: flex;
    min-height: 0;
  }
  main {
    flex: 1;
    overflow-y: auto;
    padding: 24px 28px;
    min-width: 0;
  }
  .boot-loading,
  .boot-error {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    color: var(--text-dim);
  }
  .boot-error p {
    color: var(--danger);
    max-width: 480px;
    text-align: center;
  }
  .boot-error .btn {
    margin-top: 6px;
  }
  .boot-hint {
    color: var(--text-dim) !important;
    font-size: 0.8rem;
  }
  .boot-hint code {
    background: var(--bg-raised);
    padding: 1px 5px;
    border-radius: 5px;
  }
  .update-banner {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 8px 16px;
    background: var(--accent-soft);
    border-bottom: 1px solid var(--accent);
    color: var(--text);
    font-size: 0.86rem;
    flex-shrink: 0;
  }
  .update-actions {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .update-banner .link {
    border: none;
    background: var(--accent);
    color: #fff;
    font-weight: 600;
    font-size: 0.82rem;
    padding: 4px 12px;
    border-radius: 7px;
    cursor: pointer;
  }
  .update-banner .link:hover {
    background: var(--accent-hover);
  }
  .update-banner .dismiss {
    border: none;
    background: transparent;
    color: var(--text-dim);
    cursor: pointer;
    padding: 4px 6px;
    border-radius: 6px;
    font-size: 0.8rem;
  }
  .update-banner .dismiss:hover {
    background: var(--bg-hover);
    color: var(--text);
  }
  .update-banner.installing em {
    color: var(--text-dim);
    font-style: normal;
    margin-left: 8px;
    font-size: 0.8rem;
  }
  .update-notes {
    max-height: 180px;
    overflow-y: auto;
    white-space: pre-wrap;
    padding: 10px 16px;
    background: var(--bg-raised);
    border-bottom: 1px solid var(--border);
    color: var(--text-dim);
    font-size: 0.82rem;
    flex-shrink: 0;
  }
  .boot-logo {
    width: 72px;
    height: 72px;
    border-radius: 18px;
    margin-bottom: 6px;
  }
  .pulse {
    animation: pulse 1.6s ease-in-out infinite;
  }
  @keyframes pulse {
    0%, 100% { opacity: 0.45; transform: scale(0.97); }
    50% { opacity: 1; transform: scale(1); }
  }
</style>
