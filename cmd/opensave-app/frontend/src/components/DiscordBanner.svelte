<script>
  // The changelog renderer draws entry text as plain text — deliberately, so
  // release notes can never inject markup — which means a URL written into
  // CHANGELOG.md is something you have to retype by hand. This is the
  // clickable version, shown above the notes where the announcement is.
  import { native } from '../lib/api.js';
  import { DISCORD_URL } from '../lib/links.js';

  export let compact = false;
</script>

<button class="discord-banner" class:compact on:click={() => native.openExternal(DISCORD_URL)}>
  <span class="mark" aria-hidden="true">
    <svg viewBox="0 0 24 18" width="26" height="20" fill="currentColor">
      <path d="M20.32 1.53A19.8 19.8 0 0 0 15.43 0c-.21.38-.46.9-.63 1.31a18.3 18.3 0 0 0-5.6 0C9.03.9 8.77.38 8.56 0A19.74 19.74 0 0 0 3.67 1.53C.57 6.19-.27 10.73.15 15.21A19.9 19.9 0 0 0 6.18 18c.49-.66.92-1.37 1.29-2.11-.71-.27-1.39-.6-2.03-.98.17-.13.34-.26.5-.4a14.2 14.2 0 0 0 12.12 0c.16.14.33.27.5.4-.64.38-1.32.71-2.03.98.37.74.8 1.45 1.29 2.11a19.87 19.87 0 0 0 6.03-2.79c.5-5.19-.84-9.69-3.53-13.68ZM8.02 12.46c-1.18 0-2.15-1.08-2.15-2.4s.95-2.4 2.15-2.4c1.2 0 2.17 1.09 2.15 2.4 0 1.32-.95 2.4-2.15 2.4Zm7.96 0c-1.18 0-2.15-1.08-2.15-2.4s.95-2.4 2.15-2.4c1.2 0 2.17 1.09 2.15 2.4 0 1.32-.95 2.4-2.15 2.4Z" />
    </svg>
  </span>
  <span class="text">
    <span class="title">The OpenSave Discord is open</span>
    <span class="sub">Get help, report a bug, or see what's coming before it ships.</span>
  </span>
  <span class="cta">Join <span class="arrow" aria-hidden="true">→</span></span>
</button>

<style>
  .discord-banner {
    display: flex;
    align-items: center;
    gap: 14px;
    width: 100%;
    text-align: left;
    padding: 16px 18px;
    border-radius: var(--radius-lg);
    border: 1px solid rgba(88, 101, 242, 0.45);
    background:
      radial-gradient(130% 180% at 0% 0%, rgba(88, 101, 242, 0.28), transparent 60%),
      var(--bg);
    color: var(--text);
    cursor: pointer;
    transition: all 0.15s ease;
  }
  .discord-banner:hover {
    border-color: #5865f2;
    background:
      radial-gradient(130% 180% at 0% 0%, rgba(88, 101, 242, 0.42), transparent 62%),
      var(--bg);
  }
  .mark {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
    width: 44px;
    height: 44px;
    border-radius: 12px;
    background: #5865f2;
    color: #fff;
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 3px;
    flex: 1;
    min-width: 0;
  }
  .title {
    font-size: 1rem;
    font-weight: 700;
    letter-spacing: -0.01em;
  }
  .sub {
    font-size: 0.82rem;
    color: var(--text-dim);
    line-height: 1.45;
  }
  .cta {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 0.85rem;
    font-weight: 700;
    color: #fff;
    background: #5865f2;
    border-radius: 999px;
    padding: 8px 16px;
  }
  .discord-banner:hover .cta {
    background: #4752c4;
  }
  .discord-banner:hover .arrow {
    transform: translateX(2px);
  }
  .arrow {
    display: inline-block;
    transition: transform 0.15s ease;
  }

  /* In the What's New modal the banner shares a narrow column with the
     notes, so it drops the tagline rather than wrapping onto three lines. */
  .compact {
    padding: 12px 14px;
    gap: 11px;
  }
  .compact .mark {
    width: 36px;
    height: 36px;
    border-radius: 10px;
  }
  .compact .title {
    font-size: 0.9rem;
  }
  .compact .sub {
    display: none;
  }
  .compact .cta {
    font-size: 0.8rem;
    padding: 6px 13px;
  }

  @media (max-width: 520px) {
    .discord-banner {
      flex-wrap: wrap;
    }
  }
</style>
