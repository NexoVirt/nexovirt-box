<script>
  // Full-size image viewer. Opens by growing out of the thumbnail it was opened from and closes
  // back into it (same path), springs from the current on-screen value so it can be interrupted.
  // A native modal <dialog> keeps focus inside and the page inert; focus goes back on close.
  // Arrow keys, prev/next buttons and swipe page through; Escape and a click beside the image close.
  //
  //   <Lightbox {images} bind:index thumb={(i) => element} />
  //   images: [{ src, alt }], index: -1 = closed, thumb(i): element to grow from (optional)
  import { tick } from 'svelte';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import X from '@lucide/svelte/icons/x';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { images = [], index = $bindable(-1), thumb = () => null, label = 'Image viewer' } = $props();

  let dlg = $state();
  let img = $state();
  let cur = $state(0);
  let on = $state(false); // scrim + chrome visible
  let loaded = $state(false);
  let dx = $state(0); // swipe offset
  let dragging = $state(false);
  let restore = null, anim = null, hidden = null, closing = false;
  let startX = 0, startY = 0, moved = false, onImage = false, history = [];

  const reduced = () => matchMedia('(prefers-reduced-motion: reduce)').matches;
  const spring = () => getComputedStyle(document.documentElement).getPropertyValue('--spring').trim() || 'ease-out';
  const SPRING_MS = 520;

  $effect(() => {
    if (index >= 0 && dlg && !dlg.open) show(index);
  });

  // Neighbours load in the background so paging is instant.
  $effect(() => {
    if (index < 0) return;
    for (const i of [cur - 1, cur + 1]) if (images[i]) new Image().src = images[i].src;
  });

  function play(keyframes, opts) {
    anim?.cancel();
    try {
      anim = img.animate(keyframes, { duration: SPRING_MS, easing: spring(), fill: 'both', ...opts });
    } catch {
      anim = img.animate(keyframes, { duration: SPRING_MS, easing: 'ease-out', fill: 'both', ...opts });
    }
    return anim.finished.catch(() => {});
  }

  // Transform that puts the full image exactly over the thumbnail.
  function fromThumb(el) {
    if (!el || !img) return null;
    const t = el.getBoundingClientRect(), r = img.getBoundingClientRect();
    if (!t.width || !r.width || t.bottom < 0 || t.top > innerHeight || t.right < 0 || t.left > innerWidth) return null;
    const x = t.left + t.width / 2 - (r.left + r.width / 2), y = t.top + t.height / 2 - (r.top + r.height / 2);
    return `translate(${x}px, ${y}px) scale(${t.width / r.width}, ${t.height / r.height})`;
  }

  function setHidden(el) {
    if (hidden) hidden.style.visibility = '';
    hidden = el;
    if (el) el.style.visibility = 'hidden';
  }

  async function show(i) {
    restore = document.activeElement;
    closing = false;
    cur = i;
    loaded = false;
    dx = 0;
    dlg.showModal();
    await tick();
    try { await img.decode(); } catch {}
    loaded = true;
    on = true;
    const t = thumb(cur), from = reduced() ? null : fromThumb(t);
    if (from) {
      setHidden(t);
      await play([{ transform: from, borderRadius: '0.875rem' }, { transform: 'none', borderRadius: '0.5rem' }]);
      setHidden(null);
    } else {
      await play([{ opacity: 0, transform: reduced() ? 'none' : 'scale(0.94)' }, { opacity: 1, transform: 'none' }], { duration: 220, easing: 'ease-out' });
    }
  }

  async function close() {
    if (closing || !dlg?.open) return;
    closing = true;
    on = false;
    const t = thumb(cur), current = img ? getComputedStyle(img).transform : 'none';
    const to = reduced() || !loaded ? null : fromThumb(t);
    if (to) {
      setHidden(t);
      await play([{ transform: current }, { transform: to, borderRadius: '0.875rem' }]);
    } else {
      await play([{ opacity: 1 }, { opacity: 0 }], { duration: 180, easing: 'ease-in' });
    }
    if (!closing) return; // reopened meanwhile
    anim?.cancel();
    setHidden(null);
    dlg.close();
    index = -1;
    closing = false;
    (restore?.isConnected ? restore : t)?.focus?.();
  }

  async function go(step, velocity = 0) {
    const n = cur + step;
    if (!images[n]) {
      dx = 0;
      return;
    }
    const from = dx;
    cur = n;
    loaded = false;
    dx = 0;
    await tick();
    try { await img.decode(); } catch {}
    loaded = true;
    if (reduced()) return play([{ opacity: 0 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
    const off = from || step * 60;
    // A flick keeps its momentum into the next image (a little bounce only then).
    await play([{ transform: `translateX(${off + step * 40}px)`, opacity: 0.4 }, { transform: 'none', opacity: 1 }],
      velocity > 0.6 ? { easing: getComputedStyle(document.documentElement).getPropertyValue('--spring-bounce').trim() || 'ease-out' } : {});
  }

  function key(e) {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); }
    else if (e.key === 'ArrowLeft') { e.preventDefault(); e.stopPropagation(); go(-1); }
    else if (e.key === 'ArrowRight') { e.preventDefault(); e.stopPropagation(); go(1); }
  }

  function down(e) {
    if (e.button !== 0) return;
    dragging = true;
    onImage = !!e.target.closest('img');
    moved = false;
    startX = e.clientX;
    startY = e.clientY;
    history = [{ x: e.clientX, t: e.timeStamp }];
    e.currentTarget.setPointerCapture(e.pointerId);
  }

  function move(e) {
    if (!dragging) return;
    const d = e.clientX - startX;
    if (Math.abs(d) > 6 || Math.abs(e.clientY - startY) > 6) moved = true;
    const edge = (d > 0 && !images[cur - 1]) || (d < 0 && !images[cur + 1]);
    dx = edge ? (d * 120 * 0.55) / (120 + 0.55 * Math.abs(d)) : d; // rubber band at the ends
    history.push({ x: e.clientX, t: e.timeStamp });
    if (history.length > 5) history.shift();
  }

  function up(e) {
    if (!dragging) return;
    dragging = false;
    if (!moved) {
      dx = 0;
      if (!onImage) close(); // click beside the image
      return;
    }
    const a = history[0], b = history[history.length - 1];
    const v = b.t > a.t ? (b.x - a.x) / (b.t - a.t) : 0; // px/ms
    const projected = dx + (v * 0.998) / (1 - 0.998); // Apple's momentum projection, v in px/ms
    if (Math.abs(projected) > innerWidth / 4) go(projected < 0 ? 1 : -1, Math.abs(v));
    else dx = 0;
  }
</script>

<dialog bind:this={dlg} class="lightbox" class:on aria-label={label} onkeydown={key} oncancel={(e) => { e.preventDefault(); close(); }}>
  {#if index >= 0 && images[cur]}
    <div class="scrim" aria-hidden="true"></div>
    <div class="stage" role="presentation" onpointerdown={down} onpointermove={move} onpointerup={up} onpointercancel={() => { dragging = false; dx = 0; }}>
      {#key cur}
        <img bind:this={img} src={images[cur].src} alt={images[cur].alt || ''} draggable="false"
          style:translate="{dx}px 0" style:transition={dragging ? 'none' : 'translate var(--spring-t) var(--spring)'} />
      {/key}
      {#if !loaded}<span class="load"><Loader size={22} class="spinner" /></span>{/if}
    </div>
    <div class="chrome">
      {#if images.length > 1}<span class="count num" aria-live="polite">{cur + 1} / {images.length}</span>{/if}
      <button class="ctl close" onclick={close} aria-label="Close"><X size={20} /></button>
      {#if images.length > 1}
        <button class="ctl prev" onclick={() => go(-1)} disabled={cur === 0} aria-label="Previous image"><ChevronLeft size={24} /></button>
        <button class="ctl next" onclick={() => go(1)} disabled={cur === images.length - 1} aria-label="Next image"><ChevronRight size={24} /></button>
      {/if}
    </div>
  {/if}
</dialog>

<style>
  .lightbox {
    position: fixed; inset: 0; width: 100vw; height: 100dvh; max-width: none; max-height: none;
    margin: 0; padding: 0; border: 0; background: transparent; color: #fff; overflow: hidden;
  }
  .lightbox::backdrop { background: transparent; }
  .scrim {
    position: absolute; inset: 0; background: rgba(9, 9, 11, 0.86); opacity: 0;
    -webkit-backdrop-filter: blur(20px); backdrop-filter: blur(20px);
    transition: opacity 320ms ease;
  }
  .on .scrim { opacity: 1; }
  .stage {
    position: absolute; inset: 0; display: grid; place-items: center;
    padding: max(3.75rem, env(safe-area-inset-top)) 4.5rem max(3.75rem, env(safe-area-inset-bottom));
    touch-action: pan-y pinch-zoom; user-select: none; -webkit-user-select: none;
  }
  img {
    max-width: 100%; max-height: 100%; object-fit: contain; border-radius: 0.5rem;
    transform-origin: center; will-change: transform; -webkit-user-drag: none;
  }
  .load { position: absolute; display: grid; place-items: center; color: rgba(255, 255, 255, 0.7); }
  .chrome { position: absolute; inset: 0; pointer-events: none; opacity: 0; transition: opacity 240ms ease; }
  .on .chrome { opacity: 1; }
  .count {
    position: absolute; top: max(1rem, env(safe-area-inset-top)); left: 50%; translate: -50% 0;
    padding: 0.375rem 0.75rem; border-radius: var(--r-pill); background: rgba(255, 255, 255, 0.14);
    font-size: 0.8125rem; font-weight: 600; font-variant-numeric: tabular-nums; letter-spacing: 0.01em;
    -webkit-backdrop-filter: blur(20px); backdrop-filter: blur(20px);
  }
  .ctl {
    position: absolute; pointer-events: auto; display: grid; place-items: center;
    width: 2.75rem; height: 2.75rem; border: 0; border-radius: 50%; cursor: pointer;
    background: rgba(255, 255, 255, 0.14); color: #fff;
    -webkit-backdrop-filter: blur(20px); backdrop-filter: blur(20px);
    transition: transform 100ms ease-out, background-color 160ms ease, opacity 160ms ease;
  }
  .ctl:hover { background: rgba(255, 255, 255, 0.22); }
  .ctl:active { transform: scale(0.94); }
  .ctl:disabled { opacity: 0.3; cursor: default; }
  .ctl:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
  .close { top: max(0.75rem, env(safe-area-inset-top)); right: 0.75rem; }
  .prev, .next { top: 50%; translate: 0 -50%; }
  .prev { left: 0.75rem; }
  .next { right: 0.75rem; }
  @media (max-width: 639px) {
    .stage { padding-inline: 0.5rem; }
    /* Phones page by swiping; the buttons sit at the bottom, out of the image. */
    .prev, .next { top: auto; bottom: max(0.75rem, env(safe-area-inset-bottom)); translate: none; }
  }
  @media (prefers-reduced-motion: reduce) {
    img { transition: none !important; }
  }
  @media (prefers-reduced-transparency: reduce) {
    .scrim { background: rgba(9, 9, 11, 0.96); -webkit-backdrop-filter: none; backdrop-filter: none; }
  }
</style>
