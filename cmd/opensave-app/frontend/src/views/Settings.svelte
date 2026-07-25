<script>
  import { settings, toast, askConfirm, gameList } from '../lib/stores.js';
  import { api, native } from '../lib/api.js';

  // Donation page, opened in the system browser (never in-app). Deliberately
  // a single constant so the destination is easy to audit and change.
  // TODO: placeholder — replace with the real Gumroad product URL before release.
  const DONATE_URL = 'https://opensaveapp.gumroad.com/l/support';

  let tab = 'general';
  let draft = null;
  let busy = false;
  let pruning = false;

  async function cleanUpSnapshots() {
    pruning = true;
    try {
      // Save the limit first so the cleanup uses it, then prune everything.
      await api.post('/api/settings', draft);
      const res = await api.post('/api/snapshots/prune', { applyDefaultToAll: true });
      const mb = (res.freedBytes / 1048576).toFixed(1);
      toast(
        res.removed > 0
          ? `Removed ${res.removed} old snapshot${res.removed === 1 ? '' : 's'}, freed ${mb} MB`
          : 'Nothing to clean up — all games are within their limit',
        'success'
      );
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      pruning = false;
    }
  }

  $: if ($settings && !draft) {
    draft = structuredClone($settings);
    // older daemons may omit cloudSync from the settings payload
    draft.cloudSync ??= {
      enabled: true, provider: 'local', url: '', username: '', password: '', headers: '{}', folderId: ''
    };
  }

  async function save() {
    busy = true;
    try {
      const updated = await api.post('/api/settings', draft);
      settings.set(updated);
      draft = structuredClone(updated);
      toast('Settings saved', 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      busy = false;
    }
  }

  // Path translations editor
  function addRule() {
    draft.pathTranslations = [...(draft.pathTranslations ?? []), { fromPattern: '', toPattern: '' }];
  }
  function removeRule(i) {
    draft.pathTranslations = draft.pathTranslations.filter((_, idx) => idx !== i);
  }

  // Custom scan paths
  async function addScanPath() {
    const dir = await native.selectDirectory('Add a folder to auto-scan');
    if (dir) draft.customScanPaths = [...(draft.customScanPaths ?? []), dir];
  }
  function removeScanPath(i) {
    draft.customScanPaths = draft.customScanPaths.filter((_, idx) => idx !== i);
  }

  // Excluded folders — locations the auto-scan should skip entirely.
  async function addExcludePath() {
    const dir = await native.selectDirectory('Choose a folder to exclude from auto-scan');
    if (dir) draft.excludePaths = [...(draft.excludePaths ?? []), dir];
  }
  function removeExcludePath(i) {
    draft.excludePaths = draft.excludePaths.filter((_, idx) => idx !== i);
  }

  // Reset tracking — untrack every game so the user can re-add them from the
  // right locations (e.g. after moving games between launchers or drives).
  // Non-destructive: this only clears the tracked list; snapshot backups on
  // disk are kept.
  let resetting = false;
  async function resetTracking() {
    const n = $gameList.length;
    if (n === 0) return;
    const ok = await askConfirm(
      `Untrack all ${n} game${n === 1 ? '' : 's'}? They'll be removed from your library so you can re-add them from the correct locations. Your save snapshots on disk are kept — nothing is deleted.`,
      { title: 'Reset tracking?', confirmText: `Untrack ${n}`, danger: true }
    );
    if (!ok) return;
    resetting = true;
    try {
      const res = await api.post('/api/games/untrack-bulk', { all: true });
      toast(`Untracked ${res.untracked} game${res.untracked === 1 ? '' : 's'} — snapshots kept`, 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      resetting = false;
    }
  }

  async function pickBackupsDir() {
    const dir = await native.selectDirectory('Select snapshots storage folder');
    if (dir) draft.backupsDir = dir;
  }

  async function pickSyncBackupsDir() {
    const dir = await native.selectDirectory('Select pre-sync safety backups folder');
    if (dir) draft.syncBackupsDir = dir;
  }

  // Relay hosting: fetch LAN IPs / public IP to share with friends.
  let relayInfo = null;
  async function loadRelayInfo() {
    try {
      relayInfo = await api.get('/api/relay/ips');
    } catch (e) {
      toast(e.message, 'error');
    }
  }
</script>

<div class="head">
  <h2 class="page-title">Settings</h2>
</div>

{#if !draft}
  <p class="quiet">Loading…</p>
{:else}
  <div class="pill-tabs" style="margin-bottom: 18px;">
    <button class:active={tab === 'general'} on:click={() => (tab = 'general')}>General</button>
    <button class:active={tab === 'sync'} on:click={() => (tab = 'sync')}>Sync</button>
    <button class:active={tab === 'storage'} on:click={() => (tab = 'storage')}>Storage</button>
    <button class:active={tab === 'advanced'} on:click={() => (tab = 'advanced')}>Advanced</button>
    <button class="support-tab" class:active={tab === 'support'} on:click={() => (tab = 'support')}>💛 Support</button>
  </div>

  {#if tab === 'general'}
    <div class="card">
      <h3 class="section-title">🖥️ Device identity</h3>
      <div class="field">
        <label for="s-name">Device name — how other devices see you</label>
        <input id="s-name" bind:value={draft.deviceName} />
      </div>
      <div class="field">
        <label for="s-type">Device type</label>
        <select id="s-type" bind:value={draft.deviceType}>
          <option value="desktop">Desktop (Windows / macOS / Linux PC)</option>
          <option value="deck">Steam Deck (SteamOS handheld)</option>
          <option value="handheld">Handheld (ROG Ally / Legion Go / emulator)</option>
          <option value="mobile">Companion (mobile device)</option>
        </select>
        <span class="hint">Shown to other devices when they discover you.</span>
      </div>
      <div class="field">
        <label for="s-node">Device ID</label>
        <input id="s-node" value={draft.nodeId ?? ''} readonly class="mono" />
        <span class="hint">This device's unique network identifier (read-only).</span>
      </div>
    </div>

    <div class="card" style="margin-top: 14px;">
      <h3 class="section-title">🚀 Startup</h3>
      <label class="check">
        <input type="checkbox" bind:checked={draft.startOnBoot} />
        Start OpenSave when the computer starts
      </label>
      <p class="hint" style="margin-top: 6px;">
        Launches minimized to the system tray so syncing runs in the background.
      </p>
    </div>

  {:else if tab === 'sync'}
    <div class="card">
      <h3 class="section-title">🔄 Sync behavior</h3>
      <label class="check">
        <input type="checkbox" bind:checked={draft.autoSyncOnTrack} />
        Sync a game immediately when it's first tracked
      </label>
      <label class="check" style="margin-top: 18px;">
        <input type="checkbox" bind:checked={draft.matchByAppId} />
        Match saves across PCs by Steam App ID
      </label>
      <span class="hint" style="margin-top: 6px;">Links the same game across devices even when it was tracked under different names or drives (e.g. a Steam copy on one PC, a standalone copy on another). Leave this off if you deliberately keep two separate copies of the same game that shouldn't merge. You can always link games by hand from a game's page.</span>
      <div class="field" style="margin-top: 14px;">
        <label for="s-limit">Internet bandwidth limit</label>
        <select id="s-limit" bind:value={draft.speedLimit}>
          <option value={0}>Unlimited (max speed)</option>
          <option value={100}>100 KB/s (very low)</option>
          <option value={500}>500 KB/s (medium)</option>
          <option value={1024}>1 MB/s (high)</option>
          <option value={5120}>5 MB/s (very high)</option>
          <option value={10240}>10 MB/s (ultra)</option>
        </select>
        <span class="hint">Only applies to relay (internet) syncs — LAN is never throttled.</span>
      </div>
    </div>

    <div class="card" style="margin-top: 14px;">
      <h3 class="section-title">🌐 Internet relay</h3>
      <div class="field">
        <label for="s-relay-url">WebSocket relay URL</label>
        <input id="s-relay-url" bind:value={draft.relayUrl} placeholder="wss://opensave-relay.onrender.com" />
        <span class="hint">The relay that carries syncs across the internet. Join a room from <strong>Internet Sync</strong>.</span>
      </div>
      <label class="check">
        <input type="checkbox" bind:checked={draft.hostRelay} on:change={() => draft.hostRelay && loadRelayInfo()} />
        Host a WAN relay server on this device
      </label>
      <p class="hint" style="margin-top: 6px;">
        Lets friends connect directly to you instead of the public relay.
      </p>
      {#if draft.hostRelay}
        <div class="field" style="margin-top: 12px;">
          <label for="s-relay-port">Relay hosting port</label>
          <input id="s-relay-port" type="number" bind:value={draft.relayPort} />
          <span class="hint">Forward this TCP port on your router so friends on the internet can reach you.</span>
        </div>
        <button class="btn small" on:click={loadRelayInfo}>Show my addresses to share</button>
        {#if relayInfo}
          <div class="share-banner">
            <div class="share-title">📡 Share these with your friend</div>
            <div class="share-row"><span>LAN IPs:</span> {relayInfo.lanIps?.join(', ') || '—'}</div>
            <div class="share-row"><span>Public IP:</span> {relayInfo.publicIp || 'unavailable'}</div>
            <div class="share-row"><span>Relay port:</span> {relayInfo.relayPort}</div>
          </div>
        {/if}
      {/if}
    </div>

    <div class="card" style="margin-top: 14px;">
      <h3 class="section-title">☁️ Cloud backup</h3>
      <label class="check">
        <input type="checkbox" bind:checked={draft.cloudSync.enabled} />
        Mirror every new snapshot to the cloud automatically
      </label>
      <p class="hint" style="margin-top: 6px;">
        On by default — uploads only happen once a provider is connected on the
        <strong>Cloud Backup</strong> page. Snapshots are stored in an <strong>OpenSave</strong> folder.
      </p>
      <div class="field" style="margin-top: 14px;">
        <label for="s-driveid">Google Drive folder ID (optional)</label>
        <input id="s-driveid" bind:value={draft.cloudSync.folderId} placeholder="Leave blank to use the auto-created OpenSave folder" />
        <span class="hint">
          Only set this to store snapshots in a specific existing Drive folder (the ID is the long code in
          the folder's URL) instead of the auto-managed one.
        </span>
      </div>
      <div class="field" style="margin-bottom: 0;">
        <label for="s-oauth-gd">Custom OAuth client IDs (optional, advanced)</label>
        <span class="hint" style="margin-bottom: 8px; display: block;">
          Google Drive and Dropbox ship with built-in credentials — leave these blank unless you want
          sign-ins to go through your own registered app. OneDrive has no built-in credentials, so it
          requires one. Takes effect on the next sign-in.
        </span>
        <div class="oauth-ids">
          <input
            id="s-oauth-gd"
            placeholder="Google Drive client ID"
            value={draft.cloudSync.customClientIds?.google_drive ?? ''}
            on:input={(e) => (draft.cloudSync.customClientIds = { ...(draft.cloudSync.customClientIds ?? {}), google_drive: e.currentTarget.value })}
          />
          <input
            placeholder="Dropbox client ID"
            value={draft.cloudSync.customClientIds?.dropbox ?? ''}
            on:input={(e) => (draft.cloudSync.customClientIds = { ...(draft.cloudSync.customClientIds ?? {}), dropbox: e.currentTarget.value })}
          />
          <input
            placeholder="OneDrive client ID"
            value={draft.cloudSync.customClientIds?.onedrive ?? ''}
            on:input={(e) => (draft.cloudSync.customClientIds = { ...(draft.cloudSync.customClientIds ?? {}), onedrive: e.currentTarget.value })}
          />
        </div>
      </div>
    </div>
  {:else if tab === 'storage'}
    <div class="card">
      <h3 class="section-title">🗄️ Snapshot storage</h3>
      <div class="field">
        <label for="s-backups">Snapshots folder</label>
        <div class="path-row">
          <input id="s-backups" bind:value={draft.backupsDir} />
          <button class="btn" on:click={pickBackupsDir}>Browse</button>
        </div>
        <span class="hint">Where version-history snapshots (ZIP archives) are stored.</span>
      </div>
      <div class="field">
        <label for="s-sync-backups">Pre-sync safety backups folder</label>
        <div class="path-row">
          <input id="s-sync-backups" bind:value={draft.syncBackupsDir} placeholder="Default: ~/.opensave/backups" />
          <button class="btn" on:click={pickSyncBackupsDir}>Browse</button>
        </div>
        <span class="hint">A safety copy of your save is taken here before every incoming sync, so a bad sync is always reversible.</span>
      </div>
    </div>

    <div class="card" style="margin-top: 14px;">
      <h3 class="section-title">🧹 Retention</h3>
      <label class="check">
        <input type="checkbox" bind:checked={draft.autoDeleteBackups} />
        Auto-delete old pre-sync backups
      </label>
      {#if draft.autoDeleteBackups}
        <div class="field" style="margin-top: 10px;">
          <label for="s-days">Retention period</label>
          <select id="s-days" bind:value={draft.autoDeleteDays}>
            <option value={7}>7 days</option>
            <option value={14}>14 days</option>
            <option value={30}>30 days</option>
            <option value={60}>60 days</option>
            <option value={90}>90 days</option>
            <option value={180}>180 days</option>
          </select>
        </div>
      {/if}
    </div>

    <div class="card" style="margin-top: 14px;">
      <h3 class="section-title">📸 Snapshot history</h3>
      <div class="field">
        <label for="s-max-snaps">Snapshots to keep per game</label>
        <input id="s-max-snaps" type="number" min="0" style="max-width: 120px;" bind:value={draft.defaultMaxSnapshots} />
        <span class="hint">
          Default limit for newly tracked games (0 = keep everything). The oldest are pruned first,
          per branch. Change a single game's limit in its Configuration tab.
        </span>
      </div>
      <div class="field" style="margin-bottom: 0;">
        <div>
          <button class="btn small" disabled={pruning} on:click={cleanUpSnapshots}>
            {pruning ? 'Cleaning up…' : '🧹 Clean up now'}
          </button>
        </div>
        <span class="hint">
          Applies the limit to every existing game and deletes snapshots beyond it across all
          branches — frees disk space immediately.
        </span>
      </div>
    </div>

    <div class="card" style="margin-top: 14px;">
      <h3 class="section-title">🔎 Game scanner</h3>
      <div class="field" style="margin-bottom: 0;">
        <label for="s-scan-paths">Extra folders to auto-scan</label>
        <span class="hint">Auto-scan already checks Steam and common emulators — add custom libraries here.</span>
        {#each draft.customScanPaths ?? [] as p, i}
          <div class="rule-row">
            <span class="rule-path" title={p}>{p}</span>
            <button class="btn small danger" on:click={() => removeScanPath(i)}>✕</button>
          </div>
        {/each}
        <button id="s-scan-paths" class="btn small" on:click={addScanPath}>+ Add folder</button>
      </div>

      <div class="field" style="margin: 18px 0 0;">
        <label for="s-exclude-paths">Folders to exclude</label>
        <span class="hint">Auto-scan skips these folders and everything inside them — handy for stale save locations (like an old GSE saves directory) you don't want offered again.</span>
        {#each draft.excludePaths ?? [] as p, i}
          <div class="rule-row">
            <span class="rule-path" title={p}>{p}</span>
            <button class="btn small danger" on:click={() => removeExcludePath(i)}>✕</button>
          </div>
        {/each}
        <button id="s-exclude-paths" class="btn small" on:click={addExcludePath}>+ Exclude folder</button>
      </div>
    </div>

    <div class="card" style="margin-top: 14px;">
      <h3 class="section-title">🧹 Reset tracking</h3>
      <div class="field" style="margin-bottom: 0;">
        <span class="hint">Untrack every game at once, then re-run Auto-scan to add them back from the correct locations — useful after moving games between launchers or drives. This only clears the tracking list; your save snapshots on disk are kept.</span>
        <button
          class="btn small danger"
          style="margin-top: 12px; width: fit-content; align-self: flex-start;"
          on:click={resetTracking}
          disabled={resetting || $gameList.length === 0}
        >
          {#if resetting}Untracking…{:else if $gameList.length === 0}No games tracked{:else}Untrack all {$gameList.length} game{$gameList.length === 1 ? '' : 's'}{/if}
        </button>
      </div>
    </div>
  {:else if tab === 'advanced'}
    <div class="card">
      <h3 class="section-title">⚙️ Network</h3>
      <div class="field" style="margin-bottom: 0;">
        <label for="s-port">Daemon port</label>
        <input id="s-port" type="number" bind:value={draft.port} />
        <span class="hint">The local API + LAN peer port. Changing it requires a restart.</span>
      </div>
    </div>

    <div class="card" style="margin-top: 14px;">
      <h3 class="section-title">🔀 Cross-platform path translation</h3>
      <div class="field" style="margin-bottom: 0;">
        <span class="hint">
          Rewrites a peer's save paths to local conventions, e.g. "C:\Users\me\Saves" → "/home/deck/saves".
        </span>
        {#each draft.pathTranslations ?? [] as rule, i}
          <div class="rule-row">
            <input placeholder="From pattern" bind:value={rule.fromPattern} />
            <span class="arrow">→</span>
            <input placeholder="To pattern" bind:value={rule.toPattern} />
            <button class="btn small danger" on:click={() => removeRule(i)}>✕</button>
          </div>
        {/each}
        <button id="s-rules" class="btn small" on:click={addRule}>+ Add rule</button>
      </div>
    </div>
  {:else if tab === 'support'}
    <div class="card">
      <h3 class="section-title">💛 Support OpenSave</h3>
      <p class="support-lede">
        OpenSave is free and open source, and it stays that way — no accounts, no ads, no
        telemetry, and no feature locked behind a payment.
      </p>
      <p class="support-body">
        It's built and maintained in spare time. If it's saved you the hassle of copying
        save folders around, you're welcome to chip in toward its development. Entirely
        optional, and nothing in the app changes either way.
      </p>
      <div class="support-actions">
        <button class="btn" on:click={() => native.openExternal(DONATE_URL)}>
          Open donation page ↗
        </button>
      </div>
      <p class="support-foot">
        Opens in your browser. Prefer not to donate? Reporting a bug or suggesting a feature
        helps just as much.
      </p>
    </div>
  {/if}

  {#if tab !== 'support'}
    <div class="save-bar">
      <button class="btn primary" disabled={busy} on:click={save}>Save changes</button>
    </div>
  {/if}
{/if}

<style>
  .head {
    margin-bottom: 18px;
  }
  .oauth-ids {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .quiet {
    color: var(--text-faint);
  }
  .check {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 0.92rem;
    color: var(--text);
    cursor: pointer;
    padding: 4px 0;
  }
  .check input {
    accent-color: var(--accent);
    width: 16px;
    height: 16px;
  }
  .path-row {
    display: flex;
    gap: 8px;
  }
  .path-row input {
    flex: 1;
  }
  .rule-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
  }
  .rule-row input {
    flex: 1;
    padding: 7px 10px;
    background: var(--bg);
    border: 1px solid var(--border-strong);
    border-radius: 8px;
    color: var(--text);
    font-size: 0.85rem;
    outline: none;
  }
  .rule-path {
    flex: 1;
    font-size: 0.83rem;
    color: var(--text-dim);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .arrow {
    color: var(--text-faint);
  }
  /* Support tab: set apart from the settings tabs (it configures nothing) but
     never shouty — no accent fill, just a softer separated pill. */
  .support-tab {
    margin-left: auto;
  }
  .support-lede {
    font-size: 0.92rem;
    color: var(--text);
    margin: 0 0 10px;
    line-height: 1.5;
  }
  .support-body {
    font-size: 0.88rem;
    color: var(--text-dim);
    margin: 0 0 16px;
    line-height: 1.5;
    max-width: 62ch;
  }
  .support-actions {
    display: flex;
    gap: 10px;
    align-items: center;
  }
  .support-foot {
    font-size: 0.8rem;
    color: var(--text-faint);
    margin: 14px 0 0;
  }
  .section-title {
    font-size: 0.95rem;
    font-weight: 600;
    margin-bottom: 14px;
    padding-bottom: 10px;
    border-bottom: 1px solid var(--border);
    color: var(--text);
  }
  .mono {
    font-family: ui-monospace, 'Cascadia Code', 'Consolas', monospace;
    font-size: 0.82rem;
    color: var(--text-dim);
  }
  input[readonly] {
    opacity: 0.75;
    cursor: default;
  }
  .share-banner {
    margin-top: 12px;
    background: rgba(138, 99, 244, 0.06);
    border: 1px solid rgba(138, 99, 244, 0.28);
    border-radius: var(--radius);
    padding: 12px 14px;
    font-size: 0.82rem;
  }
  .share-title {
    font-weight: 600;
    margin-bottom: 6px;
  }
  .share-row {
    color: var(--text-dim);
    margin-top: 3px;
  }
  .share-row span {
    color: var(--text-faint);
    display: inline-block;
    width: 78px;
  }
  .save-bar {
    display: flex;
    justify-content: flex-end;
    margin-top: 16px;
  }
</style>
