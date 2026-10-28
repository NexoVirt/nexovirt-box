<script>
  // Modal dialog. On wide screens it grows out of the point that opened it (transform-origin =
  // the trigger) and materializes (scale + blur + opacity together); on phones it is a bottom
  // sheet that enters and leaves along the same path. Scrim and Escape close it unless `locked`.
  import { tick } from 'svelte';

  let { open = $bindable(false), label = 'Dialog', origin = null, locked = false, wide = false, children } = $props();

  let panel = $state();
  let shown = $state(false);
  let ox = $state('50%'), oy = $state('50%');

  $effect(() => {
    if (open) {
      shown = true;
      tick().then(() => {
        if (origin && panel) {
          const r = panel.getBoundingClientRect();
          ox = `${origin.x - r.left}px`;
          oy = `${origin.y - r.top}px`;
        }
        panel?.querySelector('input, select, textarea, button:not([data-close])')?.focus();
      });
    }
  });

  function close() { if (!locked) open = false; }
  function key(e) { if (open && e.key === 'Escape') close(); }
  function ended(e) { if (e.target === panel && !open) shown = false; }
</script>

<svelte:window onkeydown={key} />

{#if shown}
  <div class="scrim" class:open onclick={close} aria-hidden="true"></div>
  <div class="wrap" class:open>
    <div
      bind:this={panel}
      class="panel"
      class:wide
      role="dialog"
      aria-modal="true"
      aria-label={label}
      style:transform-origin="{ox} {oy}"
      ontransitionend={ended}
    >
      {@render children()}
    </div>
  </div>
{/if}

<style>
  .scrim { position: fixed; inset: 0; z-index: 50; background: var(--scrim); opacity: 0; transition: opacity 260ms ease; }
  .scrim.open { opacity: 1; }
  .wrap { position: fixed; inset: 0; z-index: 51; display: grid; place-items: center; padding: 1.5rem; pointer-events: none; }
  .panel {
    pointer-events: auto; width: min(34rem, 100%); max-height: min(46rem, calc(100svh - 3rem)); overflow: auto;
    border-radius: 1.5rem; background: var(--surface); box-shadow: var(--shadow-float);
    opacity: 0; transform: scale(0.9); filter: blur(6px);
    transition: transform var(--spring-t) var(--spring), opacity 200ms ease, filter 240ms ease;
    overscroll-behavior: contain;
  }
  .panel.wide { width: min(46rem, 100%); }
  .wrap.open .panel { opacity: 1; transform: none; filter: none; }
  @starting-style { .wrap.open .panel { opacity: 0; transform: scale(0.9); filter: blur(6px); } .scrim.open { opacity: 0; } }

  @media (max-width: 639px) {
    .wrap { place-items: end stretch; padding: 0.5rem; }
    .panel { width: 100%; max-height: calc(100svh - 2rem); border-radius: 1.75rem; transform: translateY(105%); filter: none; opacity: 1; }
    .panel.wide { width: 100%; }
    .wrap.open .panel { transform: none; }
    @starting-style { .wrap.open .panel { transform: translateY(105%); opacity: 1; filter: none; } }
  }
  @media (prefers-reduced-motion: reduce) {
    .panel, .wrap.open .panel { transform: none !important; filter: none !important; transition: opacity 200ms ease; }
  }
</style>
