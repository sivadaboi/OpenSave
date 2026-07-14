<script>
  import { logEntries } from '../lib/stores.js';

  const colors = { info: 'var(--text-dim)', warn: 'var(--warn)', error: 'var(--danger)', success: 'var(--success)' };
  const fmtTime = (t) => new Date(t).toLocaleTimeString();

  // Stick to the bottom as new entries arrive. Implemented as an action
  // (node is a local, uninstrumented reference): assigning scrollTop on a
  // bind:this variable would $$invalidate it and loop the reactive flush
  // forever, freezing the app.
  function autoscroll(node, _count) {
    const toBottom = () => queueMicrotask(() => (node.scrollTop = node.scrollHeight));
    toBottom();
    return { update: toBottom };
  }
</script>

<div class="head">
  <h2 class="page-title">Activity</h2>
</div>

<div class="card log" use:autoscroll={$logEntries.length}>
  {#if $logEntries.length === 0}
    <div class="empty"><h3>Nothing yet</h3><p>Sync events, snapshots, and warnings show up here.</p></div>
  {:else}
    {#each $logEntries as entry}
      <div class="line">
        <span class="time">{fmtTime(entry.timestamp)}</span>
        <span class="level" style="color: {colors[entry.level] ?? 'var(--text-dim)'}">{entry.level}</span>
        <span class="msg">{entry.message}</span>
      </div>
    {/each}
  {/if}
</div>

<style>
  .head {
    margin-bottom: 18px;
  }
  .log {
    height: calc(100vh - var(--titlebar-h) - var(--statusbar-h) - 130px);
    overflow-y: auto;
    font-family: 'Cascadia Code', 'Consolas', monospace;
    font-size: 0.8rem;
    padding: 14px;
  }
  .line {
    display: flex;
    gap: 10px;
    padding: 2px 0;
    align-items: baseline;
  }
  .time {
    color: var(--text-faint);
    flex-shrink: 0;
  }
  .level {
    width: 56px;
    flex-shrink: 0;
    font-weight: 600;
  }
  .msg {
    color: var(--text);
    word-break: break-word;
    user-select: text;
  }
</style>
