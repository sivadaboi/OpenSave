<script>
  import { native } from '../lib/api.js';
  import logoUrl from '../assets/logo.svg';
</script>

<div class="titlebar" role="banner" style="--wails-draggable: drag" on:dblclick={() => native.toggleMaximise()}>
  <div class="brand">
    <img class="logo" src={logoUrl} alt="" draggable="false" />
    <span class="title">OpenSave</span>
  </div>
  <div class="controls" style="--wails-draggable: no-drag">
    <button on:click={() => native.minimise()} title="Minimise" aria-label="Minimise">
      <svg width="10" height="10" viewBox="0 0 10 10"><line x1="1" y1="5.5" x2="9" y2="5.5" stroke="currentColor" stroke-width="1.2"/></svg>
    </button>
    <button on:click={() => native.toggleMaximise()} title="Maximise" aria-label="Maximise">
      <svg width="10" height="10" viewBox="0 0 10 10"><rect x="1.5" y="1.5" width="7" height="7" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>
    </button>
    <button class="close" on:click={() => native.close()} title="Close" aria-label="Close">
      <svg width="10" height="10" viewBox="0 0 10 10"><path d="M1.5 1.5 L8.5 8.5 M8.5 1.5 L1.5 8.5" stroke="currentColor" stroke-width="1.2"/></svg>
    </button>
  </div>
</div>

<style>
  .titlebar {
    height: var(--titlebar-h);
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--bg-sidebar);
    border-bottom: 1px solid var(--border);
    padding-left: 10px;
    flex-shrink: 0;
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .logo {
    width: 18px;
    height: 18px;
    border-radius: 5px;
  }
  .title {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--text-dim);
    letter-spacing: 0.02em;
  }
  .controls {
    display: flex;
    height: 100%;
  }
  .controls button {
    width: 44px;
    height: 100%;
    border: none;
    background: transparent;
    color: var(--text-dim);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .controls button:hover {
    background: var(--bg-hover);
    color: var(--text);
  }
  .controls button.close:hover {
    background: var(--danger);
    color: #fff;
  }
</style>
