<script>
  import { onMount } from 'svelte';
  import { native } from '../lib/api.js';
  import { navigate } from '../lib/stores.js';
  import { backdropClose } from '../lib/backdrop.js';
  import { DISCORD_URL, GITHUB_URL } from '../lib/links.js';
  import logoUrl from '../assets/logo.png';

  export let onClose = () => {};

  let info = null;
  onMount(async () => {
    try {
      info = await native.appInfo();
    } catch {
      info = { name: 'OpenSave', version: '2.0.0' };
    }
  });

  // The changelog has its own view now; showing a second, raw copy here was
  // where users met literal markdown.
  function openChangelog() {
    onClose();
    navigate('changelog');
  }

  $: buildLabel =
    info?.buildTime && info.buildTime !== '0' ? new Date(Number(info.buildTime)).toLocaleString() : '';

  function onKeydown(e) {
    if (e.key === 'Escape') onClose();
  }
</script>

<svelte:window on:keydown={onKeydown} />

<div class="backdrop" use:backdropClose={onClose} role="presentation">
  <div class="modal" role="dialog" aria-modal="true" aria-label="About OpenSave">
    <button class="x" on:click={onClose} title="Close" aria-label="Close">✕</button>
    <img class="logo" src={logoUrl} alt="" />
    <h2>{info?.name ?? 'OpenSave'}</h2>
    <div class="ver">
      Version {info?.version ?? '—'}{#if buildLabel}<span class="build"> · built {buildLabel}</span>{/if}
    </div>
    <p class="tagline">{info?.tagline ?? ''}</p>

    <div class="meta">
      <div><span>License</span> {info?.license ?? 'MIT'}</div>
      <div><span>Built with</span> {info?.tech ?? 'Go + Wails'}</div>
    </div>

    <div class="links">
      <button class="link-btn discord" on:click={() => native.openExternal(DISCORD_URL)}>
        <svg viewBox="0 0 24 18" width="16" height="12" fill="currentColor" aria-hidden="true">
          <path d="M20.32 1.53A19.8 19.8 0 0 0 15.43 0c-.21.38-.46.9-.63 1.31a18.3 18.3 0 0 0-5.6 0C9.03.9 8.77.38 8.56 0A19.74 19.74 0 0 0 3.67 1.53C.57 6.19-.27 10.73.15 15.21A19.9 19.9 0 0 0 6.18 18c.49-.66.92-1.37 1.29-2.11-.71-.27-1.39-.6-2.03-.98.17-.13.34-.26.5-.4a14.2 14.2 0 0 0 12.12 0c.16.14.33.27.5.4-.64.38-1.32.71-2.03.98.37.74.8 1.45 1.29 2.11a19.87 19.87 0 0 0 6.03-2.79c.5-5.19-.84-9.69-3.53-13.68ZM8.02 12.46c-1.18 0-2.15-1.08-2.15-2.4s.95-2.4 2.15-2.4c1.2 0 2.17 1.09 2.15 2.4 0 1.32-.95 2.4-2.15 2.4Zm7.96 0c-1.18 0-2.15-1.08-2.15-2.4s.95-2.4 2.15-2.4c1.2 0 2.17 1.09 2.15 2.4 0 1.32-.95 2.4-2.15 2.4Z" />
        </svg>
        Join the Discord
      </button>
      <button class="link-btn" on:click={() => native.openExternal(GITHUB_URL)}>GitHub</button>
      <button class="link-btn" on:click={openChangelog}>Changelog</button>
    </div>

    <p class="copy">{info?.copyright ?? ''}</p>
    <p class="note">Wire-compatible with the original Node.js/Electron OpenSave — Go and JS devices sync together.</p>
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
    z-index: 90;
    padding: 32px;
  }
  .modal {
    position: relative;
    width: min(420px, 100%);
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-lg);
    padding: 32px 28px 26px;
    text-align: center;
    box-shadow: 0 20px 60px rgba(0, 0, 0, 0.5);
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
  }
  .x:hover {
    background: var(--bg-hover);
    color: var(--text);
  }
  .logo {
    width: 72px;
    height: 72px;
    border-radius: 18px;
    margin-bottom: 12px;
  }
  h2 {
    font-size: 1.4rem;
    font-weight: 700;
  }
  .ver {
    color: var(--accent);
    font-weight: 600;
    font-size: 0.9rem;
    margin-top: 2px;
  }
  .tagline {
    color: var(--text-dim);
    font-size: 0.9rem;
    margin-top: 8px;
  }
  .meta {
    display: flex;
    justify-content: center;
    gap: 22px;
    margin: 18px 0 14px;
    font-size: 0.85rem;
    color: var(--text);
  }
  .meta span {
    display: block;
    color: var(--text-faint);
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    margin-bottom: 2px;
  }
  .build {
    color: var(--text-faint);
    font-weight: 400;
    font-size: 0.78rem;
  }
  .links {
    display: flex;
    justify-content: center;
    flex-wrap: wrap;
    gap: 8px;
    margin-bottom: 14px;
  }
  .link-btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    background: transparent;
    color: var(--text-dim);
    cursor: pointer;
    font-size: 0.82rem;
    padding: 7px 13px;
    transition: all 0.12s;
  }
  .link-btn:hover {
    background: var(--bg-hover);
    color: var(--text);
  }
  /* Discord's blurple, so the one link people are being pointed to is the
     one their eye lands on first. */
  .link-btn.discord {
    color: #fff;
    background: #5865f2;
    border-color: #5865f2;
    font-weight: 600;
  }
  .link-btn.discord:hover {
    background: #4752c4;
    border-color: #4752c4;
    color: #fff;
  }
  .copy {
    font-size: 0.78rem;
    color: var(--text-faint);
  }
  .note {
    font-size: 0.76rem;
    color: var(--text-faint);
    margin-top: 12px;
    line-height: 1.5;
    border-top: 1px solid var(--border);
    padding-top: 12px;
  }
</style>
