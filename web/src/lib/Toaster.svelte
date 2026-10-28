<script>
  import { toasts, dismiss } from './toast.svelte.js';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import Info from '@lucide/svelte/icons/info';
  import { fly } from 'svelte/transition';
  import { cubicOut } from 'svelte/easing';
  const icons = { error: CircleAlert, success: CircleCheck, info: Info };
</script>

<div class="stack" aria-live="polite">
  {#each toasts as t (t.id)}
    {@const Icon = icons[t.kind] || Info}
    <button type="button" class="toast {t.kind}" onclick={() => dismiss(t.id)} transition:fly={{ y: -24, duration: 380, easing: cubicOut }}>
      <Icon size={18} />
      <span>{t.message}</span>
    </button>
  {/each}
</div>

<style>
  .stack {
    position: fixed; top: calc(0.75rem + env(safe-area-inset-top, 0px)); left: 50%; transform: translateX(-50%);
    z-index: 60; display: grid; gap: 0.5rem; width: min(26rem, calc(100vw - 2rem)); pointer-events: none;
  }
  .toast {
    pointer-events: auto; display: flex; align-items: flex-start; gap: 0.625rem; text-align: left; cursor: pointer;
    padding: 0.8rem 1rem; border: 0; border-radius: 1.125rem; background: var(--material); box-shadow: var(--shadow-float);
    -webkit-backdrop-filter: blur(30px) saturate(180%); backdrop-filter: blur(30px) saturate(180%);
    font-weight: 500; letter-spacing: 0;
  }
  .toast :global(svg) { margin-top: 1px; color: var(--accent); }
  .toast.error :global(svg) { color: var(--error); }
  .toast.success :global(svg) { color: var(--ok); }
</style>
