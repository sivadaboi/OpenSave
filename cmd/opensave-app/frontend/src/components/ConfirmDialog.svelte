<script>
  import { confirmRequest, answerConfirm } from '../lib/stores.js';

  let confirmBtn;

  // Focus the primary action as soon as the dialog appears so Enter/Escape
  // work immediately, like a native dialog would.
  $: if ($confirmRequest && confirmBtn) confirmBtn.focus();

  function onKeydown(e) {
    if (!$confirmRequest) return;
    if (e.key === 'Escape') answerConfirm(false);
    if (e.key === 'Enter') answerConfirm(true);
  }
</script>

<svelte:window on:keydown={onKeydown} />

{#if $confirmRequest}
  <div class="backdrop" on:click|self={() => answerConfirm(false)} role="presentation">
    <div class="modal card" role="alertdialog" aria-modal="true" aria-label={$confirmRequest.title}>
      <h3>{$confirmRequest.title}</h3>
      <p class="message">{$confirmRequest.message}</p>
      <div class="actions">
        <button class="btn" on:click={() => answerConfirm(false)}>{$confirmRequest.cancelText}</button>
        <button
          class="btn primary"
          class:danger={$confirmRequest.danger}
          bind:this={confirmBtn}
          on:click={() => answerConfirm(true)}
        >
          {$confirmRequest.confirmText}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 96; /* above the conflict modal (90) — confirms are always the top question */
    padding: 32px;
  }
  .modal {
    width: min(440px, 100%);
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-lg);
    padding: 22px 24px 20px;
    box-shadow: 0 18px 50px rgba(0, 0, 0, 0.5);
  }
  h3 {
    margin: 0 0 10px;
    font-size: 1.05rem;
  }
  .message {
    color: var(--text-dim);
    font-size: 0.92rem;
    line-height: 1.5;
    margin: 0 0 18px;
    word-break: break-word;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
  }
  .btn.danger {
    background: #b91c1c;
    border-color: #b91c1c;
  }
  .btn.danger:hover {
    background: #dc2626;
  }
</style>
