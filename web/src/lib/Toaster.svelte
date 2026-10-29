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
    <div class="toast {t.kind}" role="status" transition:fly={{ y: -24, duration: 380, easing: cubicOut }}>
      <button type="button" class="msg" onclick={() => dismiss(t.id)}><Icon size={18} /><span>{t.message}</span></button>
      {#if t.actions?.length}
        <span class="acts">
          {#each t.actions as a (a.label)}
            <button type="button" class="act" onclick={() => { dismiss(t.id); a.run(); }}>{a.label}</button>
          {/each}
        </span>
      {/if}
    </div>
  {/each}
</div>

<style>
  .stack {
    position: fixed; top: calc(0.75rem + env(safe-area-inset-top, 0px)); left: 50%; transform: translateX(-50%);
    z-index: 60; display: grid; gap: 0.5rem; width: min(26rem, calc(100vw - 2rem)); pointer-events: none;
  }
  .toast {
    pointer-events: auto; display: grid; gap: 0.5rem; text-align: left;
    padding: 0.8rem 1rem; border: 0; border-radius: 1.125rem; background: var(--material); box-shadow: var(--shadow-float);
    font-weight: 500; letter-spacing: 0;
  }
  .msg { display: flex; align-items: flex-start; gap: 0.625rem; padding: 0; border: 0; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; }
  .acts { display: flex; gap: 0.5rem; justify-content: flex-end; flex-wrap: wrap; }
  .act {
    min-height: 2rem; padding: 0 0.875rem; border: 0; border-radius: var(--r-pill); cursor: pointer;
    background: var(--fill); color: var(--accent); font-weight: 600; font-size: 0.8125rem;
    transition: transform 100ms ease-out;
  }
  .act:active { transform: scale(0.96); }
  .toast :global(svg) { margin-top: 1px; color: var(--accent); }
  .toast.error :global(svg) { color: var(--error); }
  .toast.success :global(svg) { color: var(--ok); }
</style>
