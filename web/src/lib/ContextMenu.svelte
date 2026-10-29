<script>
  // Right-click menu at the pointer. Grows from the click point, stays inside the window,
  // closes on Escape, outside click, scroll or after a choice. Arrow keys move between items.
  import { tick } from 'svelte';

  let { menu = $bindable(null), onPick } = $props(); // menu = { x, y, items: [{ key, label, icon, danger, disabled } | 'sep'] }
  let el = $state();
  let pos = $state({ x: 0, y: 0 });

  $effect(() => {
    if (!menu) return;
    pos = { x: menu.x, y: menu.y };
    tick().then(() => {
      if (!el) return;
      // offset sizes: the menu is still scaled down by its grow animation here
      pos = { x: Math.max(8, Math.min(menu.x, innerWidth - el.offsetWidth - 8)), y: Math.max(8, Math.min(menu.y, innerHeight - el.offsetHeight - 8)) };
      el.querySelector('button:not(:disabled)')?.focus();
    });
  });

  function close() { menu = null; }
  function outside(e) { if (menu && el && !el.contains(e.target)) close(); }
  function key(e) {
    if (!menu) return;
    if (e.key === 'Escape') { e.preventDefault(); close(); return; }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const btns = [...el.querySelectorAll('button:not(:disabled)')];
      const i = btns.indexOf(document.activeElement);
      btns[(i + (e.key === 'ArrowDown' ? 1 : -1) + btns.length) % btns.length]?.focus();
    }
  }
  function pick(item) { close(); onPick(item.key); }
</script>

<svelte:window onpointerdown={outside} onkeydown={key} onblur={close} onresize={close} />

{#if menu}
  <div bind:this={el} class="ctx" role="menu" style:left="{pos.x}px" style:top="{pos.y}px" oncontextmenu={(e) => e.preventDefault()}>
    {#each menu.items as item, i (i)}
      {#if item === 'sep'}
        <div class="sep" role="separator"></div>
      {:else}
        {@const Icon = item.icon}
        <button role="menuitem" class:danger={item.danger} disabled={item.disabled} onclick={() => pick(item)}>
          {#if Icon}<Icon size={15} />{:else}<span class="pad"></span>{/if}
          <span class="label">{item.label}</span>
          {#if item.hint}<span class="hint">{item.hint}</span>{/if}
        </button>
      {/if}
    {/each}
  </div>
{/if}

<style>
  .ctx {
    position: fixed; z-index: 60; min-width: 13.5rem; padding: 0.3125rem; border-radius: 0.875rem;
    background: var(--material); box-shadow: var(--shadow-float);
    transform-origin: top left; animation: grow 220ms var(--spring);
  }
  @keyframes grow { from { transform: scale(0.94); opacity: 0; filter: blur(3px); } }
  button {
    display: flex; align-items: center; gap: 0.625rem; width: 100%; min-height: 2rem; padding: 0 0.625rem;
    border: 0; border-radius: 0.5rem; background: none; color: var(--text); font: inherit; font-size: 0.8125rem;
    text-align: left; cursor: pointer;
  }
  button:hover:not(:disabled), button:focus-visible { background: var(--accent); color: var(--on-accent); outline: none; }
  button:disabled { color: var(--text-3); cursor: default; }
  button.danger:not(:hover):not(:focus-visible) { color: var(--error); }
  .label { flex: 1; }
  .hint { color: var(--text-3); font-size: 0.75rem; }
  button:hover .hint, button:focus-visible .hint { color: inherit; opacity: 0.8; }
  .pad { width: 15px; }
  .sep { height: 1px; margin: 0.25rem 0.5rem; background: var(--hairline); }
  @media (prefers-reduced-motion: reduce) { .ctx { animation: none; } }
</style>
