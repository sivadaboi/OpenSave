<script>
  import { toasts } from '../lib/stores.js';
</script>

<div class="toasts">
  {#each $toasts as t (t.id)}
    <div class="toast {t.kind}">{t.message}</div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed;
    bottom: 40px;
    right: 20px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    z-index: 100;
  }
  .toast {
    padding: 11px 16px;
    background: var(--bg-raised);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    font-size: 0.88rem;
    max-width: 380px;
    box-shadow: 0 6px 24px rgba(0, 0, 0, 0.45);
    animation: slide-in 0.18s ease-out;
  }
  .toast.success {
    border-color: rgba(74, 222, 128, 0.4);
  }
  .toast.error {
    border-color: rgba(217, 87, 87, 0.5);
  }
  @keyframes slide-in {
    from {
      transform: translateX(30px);
      opacity: 0;
    }
    to {
      transform: none;
      opacity: 1;
    }
  }
</style>
