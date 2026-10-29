<script>
  // App window over the desktop. It grows out of the tile (or button) that opened it and goes back
  // into it on close (critically damped spring, same path both ways).
  // Full windows cover the screen. Floating windows (Files) float over the desktop like a macOS
  // window: drag by the title bar, resize from the corner, maximise (button or double-click),
  // minimise to the desktop; size, place and maximise are remembered in this browser.
  // Escape closes, unless a dialog, a menu or the terminal has the key. Phones always get the full window.
  import { onMount } from 'svelte';
  import { origin } from './desktop.svelte.js';
  import X from '@lucide/svelte/icons/x';
  import Minus from '@lucide/svelte/icons/minus';
  import Maximize2 from '@lucide/svelte/icons/maximize-2';
  import Minimize2 from '@lucide/svelte/icons/minimize-2';

  let { title = '', onClose, onMinimize = null, fill = false, flush = false, floating = false, name = '', minimized = false, children } = $props();

  const from = origin.rect;
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const phone = matchMedia('(max-width: 639px)').matches;
  // svelte-ignore state_referenced_locally
  const KEY = 'nxbox-win-' + name; // one window instance per page (keyed by the caller)
  let el = $state();

  // Place and size: remembered per window, kept on screen.
  function initial() {
    let saved = null;
    try { saved = JSON.parse(localStorage.getItem(KEY) || 'null'); } catch { /* storage blocked */ }
    const W = innerWidth, H = innerHeight;
    const w = Math.min(saved?.w || 1120, W - 32), h = Math.min(saved?.h || 720, H - 72);
    const x = saved ? Math.min(Math.max(saved.x, 120 - w), W - 120) : (W - w) / 2;
    const y = saved ? Math.min(Math.max(saved.y, 52), H - 60) : Math.max(56, (H - h) / 2 + 16);
    return { x, y, w, h, max: !!saved?.max };
  }
  // svelte-ignore state_referenced_locally
  let rect = $state(floating ? initial() : { max: false });
  const isFloat = $derived(floating && !phone && !rect.max);
  function remember() { if (floating) try { localStorage.setItem(KEY, JSON.stringify(rect)); } catch { /* storage blocked */ } }

  // Critically damped spring (damping 1.0), normalized to end exactly at 1.
  const K = 8, END = 1 - (1 + K) * Math.exp(-K);
  const spring = (t) => Math.min(1, (1 - (1 + K * t) * Math.exp(-K * t)) / END);

  function zoom() {
    if (reduce) return { duration: 160, css: (t) => `opacity:${t}` };
    if (!from || !from.width) return { duration: 320, easing: spring, css: (t, u) => `opacity:${t};transform:scale(${1 - u * 0.04})` };
    const box = isFloat ? { left: rect.x, top: rect.y, width: rect.w, height: rect.h } : { left: 0, top: 0, width: innerWidth, height: innerHeight };
    const s = Math.max(from.width / box.width, from.height / box.height);
    const dx = from.left + from.width / 2 - (box.left + box.width / 2), dy = from.top + from.height / 2 - (box.top + box.height / 2);
    const r0 = isFloat ? 14 : 0;
    return {
      duration: 520, easing: spring,
      css: (t, u) => `opacity:${Math.min(1, t * 3)};border-radius:${Math.round(r0 + u * 30)}px;transform:translate(${dx * u}px,${dy * u}px) scale(${s + (1 - s) * t})`,
    };
  }

  // Drag by the title bar (1:1, from where it was grabbed).
  let grab = null;
  function barDown(e) {
    if (!isFloat || e.button !== 0 || e.target.closest('button')) return;
    grab = { dx: e.clientX - rect.x, dy: e.clientY - rect.y };
    e.currentTarget.setPointerCapture(e.pointerId);
  }
  function barMove(e) {
    if (!grab) return;
    rect.x = Math.min(Math.max(e.clientX - grab.dx, 120 - rect.w), innerWidth - 120);
    rect.y = Math.min(Math.max(e.clientY - grab.dy, 48), innerHeight - 48);
  }
  function barUp() { if (grab) { grab = null; remember(); } }
  // Resize from the corner.
  let size = null;
  function sizeDown(e) {
    size = { x: e.clientX, y: e.clientY, w: rect.w, h: rect.h };
    e.currentTarget.setPointerCapture(e.pointerId);
  }
  function sizeMove(e) {
    if (!size) return;
    rect.w = Math.max(520, Math.min(innerWidth - rect.x - 8, size.w + e.clientX - size.x));
    rect.h = Math.max(360, Math.min(innerHeight - rect.y - 8, size.h + e.clientY - size.y));
  }
  function sizeUp() { if (size) { size = null; remember(); } }
  function toggleMax() { rect.max = !rect.max; remember(); }

  function key(e) {
    if (minimized || e.key !== 'Escape' || e.defaultPrevented) return;
    const t = e.target;
    if (t?.closest?.('input, textarea, select, [contenteditable], .xterm')) return;
    if (document.querySelector('[aria-modal="true"], .ctx, .pop.open')) return;
    onClose();
  }

  onMount(() => el?.focus({ preventScroll: true }));
</script>

<svelte:window onkeydown={key} />

<div class="window" class:floating={isFloat} class:hidden-win={minimized} bind:this={el} role="dialog" aria-modal="false" aria-label={title} tabindex="-1"
  style:left={isFloat ? `${rect.x}px` : null} style:top={isFloat ? `${rect.y}px` : null}
  style:width={isFloat ? `${rect.w}px` : null} style:height={isFloat ? `${rect.h}px` : null}
  transition:zoom>
  <!-- svelte-ignore a11y_no_static_element_interactions (dragging the title bar is a pointer extra; the buttons do the same by keyboard) -->
  <header class="titlebar" class:draggable={isFloat} onpointerdown={barDown} onpointermove={barMove} onpointerup={barUp} onpointercancel={barUp}
    ondblclick={(e) => floating && !phone && !e.target.closest('button') && toggleMax()}>
    <div class="controls">
      <button class="ctl" onclick={onClose} aria-label="Close {title}" title="Close (Esc)"><X size={16} strokeWidth={2.4} /></button>
      {#if floating && !phone}
        {#if onMinimize}<button class="ctl" onclick={onMinimize} aria-label="Minimise {title}" title="Minimise"><Minus size={16} strokeWidth={2.4} /></button>{/if}
        <button class="ctl" onclick={toggleMax} aria-label={rect.max ? `Restore ${title}` : `Maximise ${title}`} title={rect.max ? 'Restore' : 'Maximise'}>
          {#if rect.max}<Minimize2 size={15} strokeWidth={2.3} />{:else}<Maximize2 size={15} strokeWidth={2.3} />{/if}
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
    <span class="grip" onpointerdown={sizeDown} onpointermove={sizeMove} onpointerup={sizeUp} onpointercancel={sizeUp} aria-hidden="true"></span>
  {/if}
</div>

<style>
  .window {
    position: fixed; inset: 0; z-index: 40; display: flex; flex-direction: column;
    background: var(--bg); overflow: hidden; outline: none;
  }
  .window.floating { inset: auto; border-radius: 14px; box-shadow: 0 0 0 1px var(--hairline), 0 30px 80px rgba(0, 0, 0, 0.28), 0 6px 18px rgba(0, 0, 0, 0.12); z-index: 30; }
  .hidden-win { display: none; }
  .titlebar {
    position: relative; z-index: 2; display: grid; grid-template-columns: 7.5rem 1fr 7.5rem; align-items: center;
    min-height: calc(3rem + env(safe-area-inset-top, 0px)); padding: env(safe-area-inset-top, 0px) max(0.75rem, env(safe-area-inset-right, 0px)) 0 max(0.5rem, env(safe-area-inset-left, 0px));
    background: var(--material-thick); box-shadow: 0 1px 0 var(--hairline);
    -webkit-backdrop-filter: blur(16px) saturate(160%); backdrop-filter: blur(16px) saturate(160%);
    user-select: none; -webkit-user-select: none;
  }
  .floating .titlebar { min-height: 2.75rem; padding-top: 0; }
  .titlebar.draggable { touch-action: none; }
  .controls { display: flex; align-items: center; gap: 0.125rem; }
  .title { margin: 0; text-align: center; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ctl {
    display: grid; place-items: center; width: 2.5rem; height: 2.5rem; border: 0; border-radius: 50%;
    background: none; color: var(--text-2); cursor: pointer; transition: background-color 150ms ease, color 150ms ease, transform 100ms ease-out;
  }
  .ctl:hover { background: var(--fill); color: var(--text); }
  .ctl:active { transform: scale(0.92); }
  .body {
    flex: 1; min-height: 0; overflow-y: auto; overscroll-behavior: contain;
    padding: 1.5rem max(2rem, env(safe-area-inset-right, 0px)) calc(2.5rem + env(safe-area-inset-bottom, 0px)) max(2rem, env(safe-area-inset-left, 0px));
  }
  .body > :global(*) { max-width: 96rem; margin-inline: auto; }
  .body.fill { display: flex; flex-direction: column; overflow: hidden; padding-bottom: calc(0.75rem + env(safe-area-inset-bottom, 0px)); }
  .body.fill > :global(*) { width: 100%; }
  .floating .body { padding: 0.625rem; }
  .body.flush { padding: 0; }
  .body.flush > :global(*) { max-width: none; }
  .grip { position: absolute; right: 0; bottom: 0; width: 1.25rem; height: 1.25rem; cursor: nwse-resize; touch-action: none; z-index: 3; }
  /* The window title replaces the large page title; actions and subtitles stay. */
  .body :global(.page-head h1) { display: none; }
  .body :global(.page-head p) { margin-top: 0; }
  .body :global(.page-head:not(:has(p, button, a, select, input))) { display: none; }
  @media (max-width: 639px) {
    .titlebar { grid-template-columns: 3rem 1fr 3rem; }
    .body { padding: 1rem max(1rem, env(safe-area-inset-right, 0px)) calc(2rem + env(safe-area-inset-bottom, 0px)) max(1rem, env(safe-area-inset-left, 0px)); }
  }
</style>
