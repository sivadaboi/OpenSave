<script>
  import { conflicts, conflictResolution, games, toast } from '../lib/stores.js';
  import { api } from '../lib/api.js';
  import { demandAttention } from '../lib/notify.js';

  let busy = false;
  let showDiff = false;
  let seen = new Set(); // gameIds already announced
  let applying = new Set(); // gameIds whose resolution runs in the background

  // Conflicts being applied stay hidden: the resolution (possibly a long
  // relay pull) runs in the background and only clears the conflict once
  // it finishes — but the user already made their choice, so don't keep
  // the modal (or its disabled buttons) on screen.
  $: entries = Object.entries($conflicts).filter(([gid]) => !applying.has(gid));
  $: current = entries[0]; // one at a time

  // When a background resolution reports back: on failure the conflict is
  // still active, so un-hide it (the modal reappears with the error toast
  // explaining why). On success the conflict entry is already gone.
  $: onResolutionDone($conflictResolution);
  function onResolutionDone(ev) {
    if (ev && applying.has(ev.gameId)) {
      applying.delete(ev.gameId);
      applying = new Set(applying);
    }
  }
  $: gameName = current ? ($games[current[0]]?.name ?? current[0]) : '';
  $: conflict = current ? current[1] : null;
  $: peerName = conflict ? (conflict.peer.Name ?? conflict.peer.name ?? 'the other device') : '';

  // Announce new conflicts: chime + surface the window + toast, same
  // treatment as incoming pairing requests.
  $: onConflicts(entries);
  function onConflicts(list) {
    const fresh = list.filter(([gid]) => !seen.has(gid));
    if (fresh.length > 0) {
      demandAttention();
      const name = $games[fresh[0][0]]?.name ?? 'a game';
      toast(`Save conflict for “${name}” — choose which version to keep`, 'error');
    }
    seen = new Set(list.map(([gid]) => gid));
    // Housekeeping: forget "applying" markers for conflicts that no longer
    // exist (the background resolution finished and cleared them).
    let pruned = false;
    for (const gid of applying) {
      if (!(gid in $conflicts)) {
        applying.delete(gid);
        pruned = true;
      }
    }
    if (pruned) applying = new Set(applying);
  }

  // Which side is further along? Compare last-modified times.
  $: localMs = conflict?.localStats?.latestMtimeMs ?? 0;
  $: remoteMs = conflict?.remoteStats?.latestMtimeMs ?? 0;
  $: newerSide = localMs && remoteMs ? (localMs > remoteMs ? 'local' : remoteMs > localMs ? 'remote' : '') : '';

  async function resolve(resolution) {
    if (!current || busy) return;
    busy = true;
    const [gameId, c] = current;
    const name = gameName;
    try {
      // Returns immediately; the actual apply (possibly a long transfer)
      // runs in the background and reports via the conflict-resolved WS
      // event, which stores.js turns into the outcome toast.
      await api.post(`/api/games/${gameId}/resolve-conflict`, {
        peerId: c.peer.ID ?? c.peer.id,
        resolution
      });
      applying = new Set(applying).add(gameId);
      if (resolution !== 'keep-local') {
        toast(`Applying your choice for “${name}” — transferring ${peerName}'s files…`, 'info');
      }
      showDiff = false;
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      busy = false;
    }
  }

  const fmtTime = (t) => (t ? new Date(t).toLocaleString() : '—');
  const fmtMs = (ms) => (ms ? new Date(ms).toLocaleString() : 'unknown');
  const fmtSize = (n) =>
    n < 0 ? '—' : n >= 1048576 ? (n / 1048576).toFixed(1) + ' MB' : n >= 1024 ? (n / 1024).toFixed(1) + ' KB' : n + ' B';
  const diffIcon = (s) => (s === 'changed' ? '✱' : s === 'only-remote' ? '+' : '−');
  const diffLabel = (s) =>
    s === 'changed' ? 'differs' : s === 'only-remote' ? `only on ${peerName}` : 'only on this device';
</script>

{#if current && conflict}
  <div class="overlay">
    <div class="modal card">
      <h3>⚔️ Save conflict — {gameName}</h3>
      <p class="desc">
        Both this device and <strong>{peerName}</strong> changed this save since the last sync.
        Pick which version to play from.
      </p>

      <div class="versions">
        <div class="version" class:newer={newerSide === 'local'}>
          <div class="v-head">
            <span class="v-title">💻 This device</span>
            {#if newerSide === 'local'}<span class="v-badge">played more recently</span>{/if}
          </div>
          <div class="v-stats">
            <span>{conflict.localStats?.files ?? '—'} files</span>
            <span>{fmtSize(conflict.localStats?.totalBytes ?? -1)}</span>
          </div>
          <div class="v-time">last change {fmtMs(localMs)}</div>
        </div>
        <div class="version" class:newer={newerSide === 'remote'}>
          <div class="v-head">
            <span class="v-title">🖥️ {peerName}</span>
            {#if newerSide === 'remote'}<span class="v-badge">played more recently</span>{/if}
          </div>
          <div class="v-stats">
            <span>{conflict.remoteStats?.files ?? '—'} files</span>
            <span>{fmtSize(conflict.remoteStats?.totalBytes ?? -1)}</span>
          </div>
          <div class="v-time">last change {fmtMs(remoteMs)}</div>
        </div>
      </div>

      {#if conflict.diffTotal > 0}
        <button class="diff-toggle" on:click={() => (showDiff = !showDiff)}>
          {showDiff ? '▾' : '▸'} What's different ({conflict.diffTotal} file{conflict.diffTotal === 1 ? '' : 's'})
        </button>
        {#if showDiff}
          <div class="diff-list">
            {#each conflict.diffFiles as d (d.path)}
              <div class="diff-row">
                <span class="diff-icon" data-status={d.status}>{diffIcon(d.status)}</span>
                <span class="diff-path" title={d.path}>{d.path}</span>
                <span class="diff-meta">{diffLabel(d.status)}</span>
                <span class="diff-sizes">{fmtSize(d.localSize)} → {fmtSize(d.remoteSize)}</span>
              </div>
            {/each}
            {#if conflict.diffTotal > conflict.diffFiles.length}
              <div class="diff-more">…and {conflict.diffTotal - conflict.diffFiles.length} more</div>
            {/if}
          </div>
        {/if}
      {/if}

      <div class="actions">
        <button class="btn" disabled={busy} on:click={() => resolve('keep-local')}>Keep mine</button>
        <button class="btn" disabled={busy} on:click={() => resolve('keep-remote')}>Keep theirs</button>
        <button class="btn primary" disabled={busy} on:click={() => resolve('merge-branch')}>
          Keep both (recommended)
        </button>
      </div>
      <p class="hint-line">
        🛡️ Each device decides for itself — <strong>“Keep mine” never overwrites {peerName}'s save</strong>
        (they choose for their device). Nothing is lost either way: “Keep both” parks {peerName}'s version on a
        branch, and “Keep theirs” snapshots yours first. Restore anything from the game's Snapshots / Branches tabs.
      </p>
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 90;
  }
  .modal {
    width: 580px;
    max-width: calc(100vw - 48px);
    max-height: calc(100vh - 80px);
    overflow-y: auto;
  }
  h3 {
    margin-bottom: 8px;
  }
  .desc {
    color: var(--text-dim);
    font-size: 0.9rem;
    margin-bottom: 16px;
  }
  .versions {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 10px;
    margin-bottom: 14px;
  }
  .version {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 12px;
  }
  .version.newer {
    border-color: rgba(74, 222, 128, 0.45);
  }
  .v-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 6px;
    margin-bottom: 8px;
  }
  .v-title {
    font-weight: 600;
    font-size: 0.9rem;
  }
  .v-badge {
    font-size: 0.66rem;
    font-weight: 700;
    color: var(--success);
    background: rgba(74, 222, 128, 0.12);
    padding: 2px 7px;
    border-radius: 999px;
    white-space: nowrap;
  }
  .v-stats {
    display: flex;
    gap: 12px;
    font-size: 0.82rem;
    color: var(--text-dim);
    margin-bottom: 4px;
  }
  .v-time {
    font-size: 0.74rem;
    color: var(--text-faint);
  }
  .diff-toggle {
    border: none;
    background: transparent;
    color: var(--text-dim);
    font-size: 0.84rem;
    font-weight: 600;
    cursor: pointer;
    padding: 4px 0;
    margin-bottom: 6px;
  }
  .diff-toggle:hover {
    color: var(--text);
  }
  .diff-list {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 8px 12px;
    margin-bottom: 14px;
    max-height: 180px;
    overflow-y: auto;
  }
  .diff-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 0;
    font-size: 0.8rem;
  }
  .diff-icon {
    width: 16px;
    text-align: center;
    font-weight: 700;
    flex-shrink: 0;
  }
  .diff-icon[data-status='changed'] {
    color: var(--warn);
  }
  .diff-icon[data-status='only-remote'] {
    color: var(--success);
  }
  .diff-icon[data-status='only-local'] {
    color: var(--danger);
  }
  .diff-path {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    font-family: ui-monospace, 'Cascadia Code', Consolas, monospace;
    font-size: 0.76rem;
  }
  .diff-meta {
    color: var(--text-faint);
    font-size: 0.72rem;
    white-space: nowrap;
  }
  .diff-sizes {
    color: var(--text-faint);
    font-size: 0.72rem;
    white-space: nowrap;
  }
  .diff-more {
    color: var(--text-faint);
    font-size: 0.76rem;
    padding: 6px 0 2px;
    text-align: center;
  }
  .actions {
    display: flex;
    gap: 8px;
    justify-content: flex-end;
    flex-wrap: wrap;
  }
  .hint-line {
    margin-top: 12px;
    font-size: 0.78rem;
    color: var(--text-faint);
    line-height: 1.5;
  }
</style>
