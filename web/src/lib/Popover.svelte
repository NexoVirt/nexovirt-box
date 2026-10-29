<script>
  // Small menu that grows out of its trigger (transform-origin = trigger corner) and materializes
  // (scale + blur + opacity together). Click outside or Escape closes it.
  let { open = $bindable(false), label = 'Menu', up = false, left = false, children } = $props();
  let el = $state();

  function outside(e) {
    if (open && el && !el.parentElement.contains(e.target)) open = false;
  }
  function key(e) { if (open && e.key === 'Escape') open = false; }
</script>

<svelte:window onpointerdown={outside} onkeydown={key} />

<div bind:this={el} class="pop" class:open class:up class:left role="menu" aria-label={label} inert={!open}>
  {@render children()}
</div>

<style>
  .pop {
    position: absolute; right: 0; top: calc(100% + 0.5rem); z-index: 30; width: 15rem; padding: 0.375rem;
    border-radius: 1rem; background: var(--material); box-shadow: var(--shadow-float);
    -webkit-backdrop-filter: blur(30px) saturate(180%); backdrop-filter: blur(30px) saturate(180%);
    transform-origin: top right; transform: scale(0.92); opacity: 0; filter: blur(4px); pointer-events: none;
    transition: transform 280ms var(--spring), opacity 180ms ease, filter 220ms ease;
  }
  .pop.open { transform: scale(1); opacity: 1; filter: none; pointer-events: auto; }
  /* Opens upward from a trigger at the bottom (the sidebar account button). */
  .pop.up { top: auto; bottom: calc(100% + 1.125rem); left: 0; right: 0; width: auto; min-width: 13rem; transform-origin: bottom center; }
  /* Grows from a trigger on the left edge (account button in the top bar). */
  .pop.left { right: auto; left: 0; transform-origin: top left; }
  @media (prefers-reduced-motion: reduce) { .pop { transform: none; filter: none; } }
</style>
