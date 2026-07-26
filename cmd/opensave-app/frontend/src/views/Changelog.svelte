<script>
  import { onMount } from 'svelte';
  import { native } from '../lib/api.js';
  import ReleaseNotes from '../components/ReleaseNotes.svelte';

  let releases = [];
  let current = '';
  let loading = true;

  onMount(async () => {
    try {
      const [rel, info] = await Promise.all([native.changelogReleases(), native.appInfo()]);
      releases = rel ?? [];
      current = info?.version ?? '';
    } catch {
      releases = [];
    }
    loading = false;
  });
</script>

<div class="page">
  <header class="head">
    <div>
      <h1>Changelog</h1>
      <p class="sub">Everything that's changed, newest first.</p>
    </div>
    {#if current}
      <div class="running" title="The version you're running">
        <span>Running</span>
        {current}
      </div>
    {/if}
  </header>

  {#if loading}
    <p class="muted">Loading…</p>
  {:else}
    <ReleaseNotes {releases} />
  {/if}
</div>

<style>
  .page {
    padding: 26px 30px 40px;
    max-width: 780px;
  }
  .head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 26px;
  }
  h1 {
    font-size: 1.5rem;
    font-weight: 700;
    letter-spacing: -0.02em;
  }
  .sub {
    color: var(--text-dim);
    font-size: 0.88rem;
    margin-top: 4px;
  }
  .running {
    flex-shrink: 0;
    text-align: right;
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--accent);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 8px 12px;
    background: var(--bg-raised);
  }
  .running span {
    display: block;
    font-size: 0.66rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    color: var(--text-faint);
    margin-bottom: 1px;
  }
  .muted {
    color: var(--text-faint);
    font-size: 0.88rem;
  }
</style>
