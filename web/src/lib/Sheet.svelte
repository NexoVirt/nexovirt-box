<script>
  // Bottom sheet. Enters and leaves along the same path (bottom), tracks the finger 1:1 when dragged,
  // projects the release velocity to decide close vs. stay, and springs back with a little bounce
  // only after a flick. Escape and the scrim close it.
  import { tick } from 'svelte';

  let { open = $bindable(false), label = 'Menu', children } = $props();

  let sheet = $state();
  let y = $state(0); // drag offset in px (0 = fully open)
  let dragging = $state(false);
  let settle = $state('var(--spring-t) var(--spring)');
  let shown = $state(false); // stays true during the exit animation
  let start = 0, grab = 0, history = [];

  $effect(() => {
    if (open) {
      shown = true;
      y = 0;
      tick().then(() => sheet?.querySelector('a, button')?.focus());
    }
  });

  function close() { open = false; }

  function project(v, d = 0.998) {
    return (v / 1000) * d / (1 - d);
  }

  function down(e) {
    if (e.button !== 0) return;
    dragging = true;
    start = e.clientY;
    grab = y;
    history = [{ y: e.clientY, t: e.timeStamp }];
    sheet.setPointerCapture(e.pointerId);
  }

  function move(e) {
    if (!dragging) return;
    const dy = grab + e.clientY - start;
    // Rubber-band when pulled up past the open position.
    y = dy >= 0 ? dy : (dy * 120 * 0.55) / (120 + 0.55 * Math.abs(dy));
    history.push({ y: e.clientY, t: e.timeStamp });
    if (history.length > 5) history.shift();
  }

  function up() {
    if (!dragging) return;
    dragging = false;
    const a = history[0], b = history[history.length - 1];
    const v = b.t > a.t ? ((b.y - a.y) / (b.t - a.t)) * 1000 : 0; // px/s, + = downwards
    const h = sheet.offsetHeight;
    const flick = Math.abs(v) > 300;
    settle = flick ? 'var(--spring-bounce-t) var(--spring-bounce)' : 'var(--spring-t) var(--spring)';
    if (y + project(v) > h * 0.5) close();
    else y = 0;
  }

  function ended(e) {
    if (e.target === sheet && !open) { shown = false; y = 0; settle = 'var(--spring-t) var(--spring)'; }
  }

  function key(e) { if (open && e.key === 'Escape') close(); }
</script>

<svelte:window onkeydown={key} />

{#if shown}
  <div class="scrim" class:open onclick={close} aria-hidden="true"></div>
  <div
    bind:this={sheet}
    class="sheet"
    class:open
    role="dialog"
    aria-modal="true"
    aria-label={label}
    style:transform={open ? `translateY(${Math.max(y, -24)}px)` : 'translateY(105%)'}
    style:transition={dragging ? 'none' : `transform ${settle}`}
    ontransitionend={ended}
  >
    <div class="handle" role="presentation" onpointerdown={down} onpointermove={move} onpointerup={up} onpointercancel={up}>
      <span></span>
    </div>
    <div class="body">{@render children()}</div>
  </div>
{/if}

<style>
  .scrim {
    position: fixed; inset: 0; z-index: 40; background: var(--scrim); opacity: 0;
    transition: opacity 300ms ease;
  }
  .scrim.open { opacity: 1; }
  .sheet {
    position: fixed; z-index: 41; left: 0.5rem; right: 0.5rem; bottom: 0.5rem;
    padding-bottom: env(safe-area-inset-bottom, 0px);
    border-radius: 1.75rem; background: var(--material); box-shadow: var(--shadow-float);
    -webkit-backdrop-filter: blur(30px) saturate(180%); backdrop-filter: blur(30px) saturate(180%);
    touch-action: none; will-change: transform;
  }
  .handle { display: grid; place-items: center; height: 1.75rem; cursor: grab; }
  .handle span { width: 2.25rem; height: 0.3125rem; border-radius: 999px; background: var(--fill-2); }
  .body { padding: 0 0.75rem 0.875rem; overflow-x: hidden; overflow-wrap: anywhere; min-width: 0; }
  .body :global(:where(pre)) { max-width: 100%; overflow-x: auto; overflow-wrap: normal; }
  @starting-style { .sheet.open { transform: translateY(105%) !important; } .scrim.open { opacity: 0; } }
  @media (prefers-reduced-motion: reduce) {
    .sheet { transition: opacity 200ms ease !important; }
  }
</style>
