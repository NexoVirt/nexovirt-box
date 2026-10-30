<script>
  // A Box window over the desktop. It grows out of the tile (or button) that opened it and goes back
  // into it on close (critically damped spring, same path both ways). Every window floats like a
  // macOS window: drag by the title bar, resize from any edge or corner, maximise (button or
  // double-click on the title), minimise to the dock row; place and size are remembered per window.
  // A click anywhere brings it to the front. Escape closes the window in front, unless a dialog,
  // a menu or the terminal has the key. Phones (under 700 px) always get the full screen.
  import { onMount } from 'svelte';
  import { origin } from './desktop.svelte.js';
  import { remember, clamp, fit } from './windows.svelte.js';
  import { resizeRect } from './winmath.js';
  import X from '@lucide/svelte/icons/x';
  import Minus from '@lucide/svelte/icons/minus';
  import Maximize2 from '@lucide/svelte/icons/maximize-2';
  import Minimize2 from '@lucide/svelte/icons/minimize-2';

  // w: the window entry in the window manager ({ key, rect, max, min }), mutated here.
  let { w, title = '', rank = 0, focused = false, phone = false, fill = false, flush = false, onClose, onMinimize, onFocus, children } = $props();

  const from = origin.rect;
  origin.rect = null; // used once: a later window opened from inside a page zooms from the centre
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
  let el = $state();

  const isFloat = $derived(!phone && !w.max);

  // Critically damped spring (damping 1.0), normalized to end exactly at 1.
  const K = 8, END = 1 - (1 + K) * Math.exp(-K);
  const spring = (t) => Math.min(1, (1 - (1 + K * t) * Math.exp(-K * t)) / END);

  function zoom() {
    if (reduce) return { duration: 160, css: (t) => `opacity:${t}` };
    if (!from || !from.width) return { duration: 300, easing: spring, css: (t, u) => `opacity:${t};transform:scale(${1 - u * 0.04})` };
    const top = phone ? 0 : 48;
    const r = isFloat ? { left: w.rect.x, top: w.rect.y, width: w.rect.w, height: w.rect.h } : { left: 0, top, width: innerWidth, height: innerHeight - top };
    const s = Math.max(from.width / r.width, from.height / r.height);
    const dx = from.left + from.width / 2 - (r.left + r.width / 2), dy = from.top + from.height / 2 - (r.top + r.height / 2);
    return {
      duration: 480, easing: spring,
      css: (t, u) => `opacity:${Math.min(1, t * 3)};transform:translate(${dx * u}px,${dy * u}px) scale(${s + (1 - s) * t})`,
    };
  }

  // Drag by the title bar (1:1, from where it was grabbed).
  let grab = null;
  function barDown(e) {
    if (!isFloat || e.button !== 0 || e.target.closest('button')) return;
    e.preventDefault(); // no text selection while dragging
    grab = { dx: e.clientX - w.rect.x, dy: e.clientY - w.rect.y };
    e.currentTarget.setPointerCapture(e.pointerId);
  }
  function barMove(e) {
    if (!grab) return;
    const r = clamp({ ...w.rect, x: e.clientX - grab.dx, y: e.clientY - grab.dy });
    w.rect.x = r.x; w.rect.y = r.y;
  }
  function barUp() { if (grab) { grab = null; remember(w); } }

  // Resize from any edge or corner (resizeRect: the grabbed edge follows the pointer 1:1, the
  // opposite edge stays put, never smaller than the minimum or past the browser window).
  const EDGES = ['n', 'ne', 'e', 'se', 's', 'sw', 'w', 'nw'];
  let size = null;
  function sizeDown(e, dir) {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    size = { dir, x: e.clientX, y: e.clientY, r: { ...w.rect } };
    e.currentTarget.setPointerCapture(e.pointerId);
  }
  function sizeMove(e) {
    if (size) Object.assign(w.rect, resizeRect(size.r, size.dir, e.clientX - size.x, e.clientY - size.y, innerWidth, innerHeight));
  }
  function sizeUp() { if (size) { size = null; remember(w); } }
  // A smaller browser window: the window shrinks with it (the saved size stays until moved).
  function viewport() { if (!phone) fit(w.rect); }
  function toggleMax() { w.max = !w.max; remember(w); }

  function key(e) {
    if (!focused || w.min || e.key !== 'Escape' || e.defaultPrevented) return;
    const t = e.target;
    if (t?.closest?.('input, textarea, select, [contenteditable], .xterm')) return;
    if (document.querySelector('[aria-modal="true"], .ctx, .pop.open')) return;
    e.preventDefault(); // one Escape closes one window, not the next one that comes to the front
    onClose();
  }

  onMount(() => el?.focus({ preventScroll: true }));
  // Brought back from the dock or to the front by the keyboard: take the focus.
  $effect(() => { if (focused && !w.min && el && !el.contains(document.activeElement)) el.focus({ preventScroll: true }); });
</script>

<svelte:window onkeydown={key} onresize={viewport} />

<div class="window" class:floating={isFloat} class:maxed={!phone && w.max} class:phone class:focused class:hidden-win={w.min} bind:this={el}
  role="dialog" aria-modal="false" aria-label={title} tabindex="-1" style:z-index={30 + rank}
  style:left={isFloat ? `${w.rect.x}px` : null} style:top={isFloat ? `${w.rect.y}px` : null}
  style:width={isFloat ? `${w.rect.w}px` : null} style:height={isFloat ? `${w.rect.h}px` : null}
  onpointerdowncapture={() => !focused && onFocus()} onfocusin={() => !focused && onFocus()}
  transition:zoom>
  <!-- svelte-ignore a11y_no_static_element_interactions (dragging the title bar is a pointer extra; the buttons do the same by keyboard) -->
  <header class="titlebar" class:draggable={isFloat} onpointerdown={barDown} onpointermove={barMove} onpointerup={barUp} onpointercancel={barUp}
    ondblclick={(e) => !phone && !e.target.closest('button') && toggleMax()}>
    <div class="controls">
      <button class="ctl" onclick={onClose} aria-label="Close {title}" title="Close (Esc)"><X size={16} strokeWidth={2.4} /></button>
      {#if !phone}
        <button class="ctl" onclick={onMinimize} aria-label="Minimise {title}" title="Minimise"><Minus size={16} strokeWidth={2.4} /></button>
        <button class="ctl" onclick={toggleMax} aria-label={w.max ? `Restore ${title}` : `Maximise ${title}`} title={w.max ? 'Restore' : 'Maximise'}>
          {#if w.max}<Minimize2 size={15} strokeWidth={2.3} />{:else}<Maximize2 size={15} strokeWidth={2.3} />{/if}
        </button>
      {/if}
    </div>
    <h1 class="t-head title">{title}</h1>
    <span class="spacer"></span>
  </header>
  <div class="body" class:fill class:flush>
    {@render children()}
  </div>
  {#if isFloat}
    {#each EDGES as d (d)}
      <span class="edge {d}" onpointerdown={(e) => sizeDown(e, d)} onpointermove={sizeMove} onpointerup={sizeUp} onpointercancel={sizeUp} aria-hidden="true"></span>
    {/each}
  {/if}
</div>

<style>
  /* One surface edge to edge: the window body is --win-bg, pages put no background of their own on it. */
  .window {
    --win-bg: var(--surface);
    position: fixed; display: flex; flex-direction: column; background: var(--win-bg); overflow: hidden; outline: none;
  }
  .window.floating {
    border-radius: 14px;
    box-shadow: 0 0 0 1px var(--hairline), 0 22px 60px rgba(0, 0, 0, 0.2), 0 4px 14px rgba(0, 0, 0, 0.08);
  }
  .window.floating:not(.focused) { box-shadow: 0 0 0 1px var(--hairline), 0 10px 30px rgba(0, 0, 0, 0.12); }
  :global(.dark) .window.floating { box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.1), 0 24px 64px rgba(0, 0, 0, 0.6); }
  /* Maximised: everything under the top bar, which stays usable. */
  .window.maxed { left: 0; right: 0; bottom: 0; top: calc(3rem + env(safe-area-inset-top, 0px)); box-shadow: 0 -1px 0 var(--hairline); }
  .window.phone { inset: 0; z-index: 45 !important; }
  .hidden-win { display: none; }

  .titlebar {
    position: relative; z-index: 2; flex-shrink: 0; display: grid; grid-template-columns: 7.5rem 1fr 7.5rem; align-items: center;
    min-height: 2.75rem; padding: 0 0.75rem 0 0.375rem;
    background: var(--win-bg); box-shadow: 0 1px 0 var(--hairline);
    user-select: none; -webkit-user-select: none;
  }
  .phone .titlebar {
    grid-template-columns: 3rem 1fr 3rem;
    min-height: calc(3rem + env(safe-area-inset-top, 0px));
    padding: env(safe-area-inset-top, 0px) max(0.75rem, env(safe-area-inset-right, 0px)) 0 max(0.5rem, env(safe-area-inset-left, 0px));
  }
  .titlebar.draggable { touch-action: none; cursor: default; }
  .controls { display: flex; align-items: center; gap: 0.125rem; }
  .title { margin: 0; text-align: center; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .window:not(.focused) .title { color: var(--text-2); }
  .ctl {
    display: grid; place-items: center; width: 2.25rem; height: 2.25rem; border: 0; border-radius: 50%;
    background: none; color: var(--text-2); cursor: pointer; transition: background-color 150ms ease, color 150ms ease, transform 100ms ease-out;
  }
  .ctl:hover { background: var(--fill); color: var(--text); }
  .ctl:active { transform: scale(0.92); }

  /* isolation: nothing a page stacks (sticky toolbars, z-indexed bars) can rise above the resize edges. */
  .body {
    flex: 1; min-height: 0; overflow-y: auto; overscroll-behavior: contain; isolation: isolate;
    padding: 1.25rem 1.5rem 1.75rem;
  }
  .body.fill { display: flex; flex-direction: column; overflow: hidden; padding-bottom: 1rem; }
  /* :where keeps this at zero specificity, so fixed overlays (menus, panels) keep their own width. */
  :where(.body.fill) > :global(*) { width: 100%; }
  .body.flush { padding: 0; }
  .phone .body:not(.flush) { padding: 1rem max(1rem, env(safe-area-inset-right, 0px)) calc(1.5rem + env(safe-area-inset-bottom, 0px)) max(1rem, env(safe-area-inset-left, 0px)); }

  /* Resize handles along every edge and corner, above the title bar and the page. */
  .edge { position: absolute; z-index: 10; touch-action: none; }
  .edge.n, .edge.s { left: 0.875rem; right: 0.875rem; height: 6px; cursor: ns-resize; }
  .edge.e, .edge.w { top: 0.875rem; bottom: 0.875rem; width: 6px; cursor: ew-resize; }
  .edge.n { top: 0; } .edge.s { bottom: 0; } .edge.e { right: 0; } .edge.w { left: 0; }
  .edge.ne, .edge.nw, .edge.se, .edge.sw { width: 0.875rem; height: 0.875rem; }
  .edge.ne { top: 0; right: 0; cursor: nesw-resize; } .edge.sw { bottom: 0; left: 0; cursor: nesw-resize; }
  .edge.nw { top: 0; left: 0; cursor: nwse-resize; } .edge.se { bottom: 0; right: 0; cursor: nwse-resize; }

  /* The window title replaces the large page title; actions and subtitles stay. */
  .body :global(.page-head h1) { display: none; }
  .body :global(.page-head p) { margin-top: 0; }
  .body :global(.page-head:not(:has(p, button, a, select, input))) { display: none; }
  /* Sections inside a window: flat on the window surface, a hairline instead of a drop shadow. */
  .body :global(:is(.tile, .group)) { background: var(--surface-2); box-shadow: 0 0 0 1px var(--hairline); }
</style>
