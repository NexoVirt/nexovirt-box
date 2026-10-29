<script>
  // Full-screen app window over the desktop. It grows out of the tile (or button) that opened it
  // and goes back into it on close (critically damped spring, same path both ways). The title bar
  // has a close button back to the desktop; Escape closes too, unless a dialog, a menu or the
  // terminal has the key.
  import { onMount } from 'svelte';
  import { origin } from './desktop.svelte.js';
  import X from '@lucide/svelte/icons/x';

  let { title = '', onClose, fill = false, flush = false, children } = $props();

  const from = origin.rect;
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
  let el = $state();

  // Critically damped spring (damping 1.0), normalized to end exactly at 1.
  const K = 8, END = 1 - (1 + K) * Math.exp(-K);
  const spring = (t) => Math.min(1, (1 - (1 + K * t) * Math.exp(-K * t)) / END);

  function zoom() {
    if (reduce) return { duration: 160, css: (t) => `opacity:${t}` };
    if (!from || !from.width) return { duration: 320, easing: spring, css: (t, u) => `opacity:${t};transform:scale(${1 - u * 0.04})` };
    const W = innerWidth, H = innerHeight;
    const s = Math.max(from.width / W, from.height / H);
    const dx = from.left + from.width / 2 - W / 2, dy = from.top + from.height / 2 - H / 2;
    return {
      duration: 520, easing: spring,
      css: (t, u) => `opacity:${Math.min(1, t * 3)};border-radius:${Math.round(u * 40)}px;transform:translate(${dx * u}px,${dy * u}px) scale(${s + (1 - s) * t})`,
    };
  }

  function key(e) {
    if (e.key !== 'Escape' || e.defaultPrevented) return;
    const t = e.target;
    if (t?.closest?.('input, textarea, select, [contenteditable], .xterm')) return;
    if (document.querySelector('[aria-modal="true"], .ctx, .pop.open')) return;
    onClose();
  }

  onMount(() => el?.focus({ preventScroll: true }));
</script>

<svelte:window onkeydown={key} />

<div class="window" bind:this={el} role="dialog" aria-modal="false" aria-label={title} tabindex="-1" transition:zoom>
  <header class="titlebar">
    <button class="close" onclick={onClose} aria-label="Close {title} and go back to the desktop" title="Back to the desktop (Esc)">
      <X size={16} strokeWidth={2.4} />
    </button>
    <h1 class="t-head title">{title}</h1>
    <span class="spacer"></span>
  </header>
  <div class="body" class:fill class:flush>
    {@render children()}
  </div>
</div>

<style>
  .window {
    position: fixed; inset: 0; z-index: 40; display: flex; flex-direction: column;
    background: var(--bg); overflow: hidden; outline: none; will-change: transform;
  }
  .titlebar {
    position: relative; z-index: 2; display: grid; grid-template-columns: 3rem 1fr 3rem; align-items: center;
    min-height: calc(3rem + env(safe-area-inset-top, 0px)); padding: env(safe-area-inset-top, 0px) max(0.75rem, env(safe-area-inset-right, 0px)) 0 max(0.75rem, env(safe-area-inset-left, 0px));
    background: var(--material-thick); box-shadow: 0 1px 0 var(--hairline);
    -webkit-backdrop-filter: blur(30px) saturate(180%); backdrop-filter: blur(30px) saturate(180%);
  }
  .title { margin: 0; text-align: center; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .close {
    display: grid; place-items: center; width: 2.75rem; height: 2.75rem; margin-left: -0.25rem; border: 0; border-radius: 50%;
    background: none; color: var(--text-2); cursor: pointer; transition: background-color 150ms ease, color 150ms ease, transform 100ms ease-out;
  }
  .close:hover { background: var(--fill); color: var(--text); }
  .close:active { transform: scale(0.92); }
  .body {
    flex: 1; min-height: 0; overflow-y: auto; overscroll-behavior: contain;
    padding: 1.5rem max(2rem, env(safe-area-inset-right, 0px)) calc(2.5rem + env(safe-area-inset-bottom, 0px)) max(2rem, env(safe-area-inset-left, 0px));
  }
  .body > :global(*) { max-width: 96rem; margin-inline: auto; }
  .body.fill { display: flex; flex-direction: column; overflow: hidden; padding-bottom: calc(0.75rem + env(safe-area-inset-bottom, 0px)); }
  .body.fill > :global(*) { width: 100%; }
  .body.flush { padding: 0; }
  .body.flush > :global(*) { max-width: none; }
  /* The window title replaces the large page title; actions and subtitles stay. */
  .body :global(.page-head h1) { display: none; }
  .body :global(.page-head p) { margin-top: 0; }
  .body :global(.page-head:not(:has(p, button, a, select, input))) { display: none; }
  @media (max-width: 639px) {
    .body { padding: 1rem max(1rem, env(safe-area-inset-right, 0px)) calc(2rem + env(safe-area-inset-bottom, 0px)) max(1rem, env(safe-area-inset-left, 0px)); }
  }
</style>
