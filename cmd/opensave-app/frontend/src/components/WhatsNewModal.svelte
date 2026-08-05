<script>
  import { backdropClose } from '../lib/backdrop.js';
  import ReleaseNotes from './ReleaseNotes.svelte';
  import DiscordBanner from './DiscordBanner.svelte';
  import { navigate } from '../lib/stores.js';

  export let releases = [];
  export let version = '';
  export let from = '';
  export let onClose = () => {};

  function onKeydown(e) {
    if (e.key === 'Escape') onClose();
  }

  function openFullChangelog() {
    onClose();
    navigate('changelog');
  }
</script>

<svelte:window on:keydown={onKeydown} />

<div class="backdrop" use:backdropClose={onClose} role="presentation">
  <div class="modal" role="dialog" aria-modal="true" aria-label="What's new in OpenSave">
    <button class="x" on:click={onClose} title="Close" aria-label="Close">✕</button>

    <div class="hero">
      <div class="badge">Updated</div>
      <h2>OpenSave {version}</h2>
      {#if from}<p class="from">You were on {from}</p>{/if}
    </div>

    <div class="body">
      <div class="banner-slot"><DiscordBanner compact /></div>
      <ReleaseNotes {releases} compact />
    </div>

    <footer>
      <button class="ghost" on:click={openFullChangelog}>Full changelog</button>
      <button class="primary" on:click={onClose}>Got it</button>
    </footer>
  </div>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.62);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 95;
    padding: 32px;
  }
  .modal {
    position: relative;
    width: min(560px, 100%);
    max-height: min(78vh, 720px);
    display: flex;
    flex-direction: column;
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-lg);
    box-shadow: 0 24px 70px rgba(0, 0, 0, 0.55);
    overflow: hidden;
  }
  .x {
    position: absolute;
    top: 12px;
    right: 12px;
    border: none;
    background: transparent;
    color: var(--text-faint);
    font-size: 0.9rem;
    cursor: pointer;
    padding: 4px 8px;
    border-radius: 6px;
    z-index: 1;
  }
  .x:hover {
    background: var(--bg-hover);
    color: var(--text);
  }
  .hero {
    padding: 26px 28px 20px;
    border-bottom: 1px solid var(--border);
    background: linear-gradient(180deg, rgba(138, 99, 244, 0.12), transparent);
  }
  .badge {
    display: inline-block;
    font-size: 0.66rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--accent);
    background: rgba(138, 99, 244, 0.14);
    border: 1px solid rgba(138, 99, 244, 0.3);
    border-radius: 999px;
    padding: 3px 10px;
    margin-bottom: 10px;
  }
  h2 {
    font-size: 1.4rem;
    font-weight: 700;
    letter-spacing: -0.02em;
  }
  .from {
    color: var(--text-faint);
    font-size: 0.82rem;
    margin-top: 3px;
  }
  .body {
    padding: 22px 28px;
    overflow-y: auto;
    flex: 1;
  }
  .banner-slot {
    margin-bottom: 20px;
  }
  footer {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
    padding: 14px 20px;
    border-top: 1px solid var(--border);
    background: var(--bg);
  }
  button.primary,
  button.ghost {
    border-radius: var(--radius);
    padding: 8px 16px;
    font-size: 0.86rem;
    font-weight: 600;
    cursor: pointer;
  }
  button.primary {
    border: none;
    background: var(--accent);
    color: #fff;
  }
  button.primary:hover {
    filter: brightness(1.08);
  }
  button.ghost {
    background: transparent;
    border: 1px solid var(--border-strong);
    color: var(--text-dim);
  }
  button.ghost:hover {
    background: var(--bg-hover);
    color: var(--text);
  }
</style>
