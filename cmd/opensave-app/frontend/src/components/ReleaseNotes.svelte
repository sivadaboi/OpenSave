<script>
  // Renders parsed changelog releases. The changelog is authored as markdown
  // for GitHub, so showing the file verbatim meant users read literal "##"
  // and "**" markers. Parsing happens in Go; this only lays it out.
  export let releases = [];
  export let compact = false;

  // Inline code is the one marker left in the text by the parser, because it
  // wants styling rather than removing. Split on backtick pairs so it can be
  // rendered as elements — never as HTML, since changelog text should not be
  // able to inject markup.
  function segments(text) {
    const out = [];
    let rest = text ?? '';
    while (true) {
      const open = rest.indexOf('`');
      if (open < 0) break;
      const close = rest.indexOf('`', open + 1);
      if (close < 0) break;
      if (open > 0) out.push({ code: false, value: rest.slice(0, open) });
      out.push({ code: true, value: rest.slice(open + 1, close) });
      rest = rest.slice(close + 1);
    }
    if (rest) out.push({ code: false, value: rest });
    return out;
  }

  // "Fixed" / "Added" / "Changed" each get their own tint so a long release
  // can be skimmed by kind rather than read start to finish.
  function toneFor(title) {
    const t = (title ?? '').toLowerCase();
    if (t.startsWith('fix')) return 'fixed';
    if (t.startsWith('add') || t.startsWith('new')) return 'added';
    if (t.startsWith('chang') || t.startsWith('improv')) return 'changed';
    if (t.startsWith('remov') || t.startsWith('deprecat')) return 'removed';
    return 'other';
  }
</script>

<div class="notes" class:compact>
  {#each releases as rel (rel.version)}
    <section class="release">
      <header>
        <h3>{rel.version}</h3>
        {#if rel.date}<span class="date">{rel.date}</span>{/if}
      </header>

      {#each rel.sections as sec}
        <div class="group">
          {#if sec.title}
            <div class="kind {toneFor(sec.title)}">{sec.title}</div>
          {/if}
          <ul>
            {#each sec.entries as e}
              <li>
                {#if e.lead}<strong>{e.lead}</strong>{' '}{/if}<span class="body"
                  >{#each segments(e.text) as seg}{#if seg.code}<code>{seg.value}</code>{:else}{seg.value}{/if}{/each}</span
                >
              </li>
            {/each}
          </ul>
        </div>
      {/each}
    </section>
  {:else}
    <p class="empty">No release notes available.</p>
  {/each}
</div>

<style>
  .notes {
    display: flex;
    flex-direction: column;
    gap: 30px;
  }
  .release {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  header {
    display: flex;
    align-items: baseline;
    gap: 10px;
    padding-bottom: 8px;
    border-bottom: 1px solid var(--border);
  }
  h3 {
    font-size: 1.15rem;
    font-weight: 700;
    color: var(--text);
    letter-spacing: -0.01em;
  }
  .date {
    font-size: 0.78rem;
    color: var(--text-faint);
  }
  .group {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .kind {
    align-self: flex-start;
    font-size: 0.68rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    padding: 3px 9px;
    border-radius: 999px;
    border: 1px solid transparent;
  }
  .kind.fixed {
    color: #7ee0a8;
    background: rgba(126, 224, 168, 0.1);
    border-color: rgba(126, 224, 168, 0.25);
  }
  .kind.added {
    color: var(--accent);
    background: rgba(138, 99, 244, 0.12);
    border-color: rgba(138, 99, 244, 0.3);
  }
  .kind.changed {
    color: #e0c07e;
    background: rgba(224, 192, 126, 0.1);
    border-color: rgba(224, 192, 126, 0.25);
  }
  .kind.removed,
  .kind.other {
    color: var(--text-dim);
    background: var(--bg-hover);
    border-color: var(--border);
  }
  ul {
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 9px;
    margin: 0;
    padding: 0;
  }
  li {
    position: relative;
    padding-left: 16px;
    font-size: 0.86rem;
    line-height: 1.6;
    color: var(--text-dim);
  }
  li::before {
    content: '';
    position: absolute;
    left: 2px;
    top: 0.62em;
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: var(--border-strong);
  }
  li strong {
    color: var(--text);
    font-weight: 600;
  }
  code {
    font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
    font-size: 0.82em;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 1px 5px;
  }
  .empty {
    color: var(--text-faint);
    font-size: 0.88rem;
  }

  .compact li {
    font-size: 0.83rem;
  }
  .compact .notes,
  .compact.notes {
    gap: 22px;
  }
</style>
