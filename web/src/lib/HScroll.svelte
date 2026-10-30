<script>
  // A sideways strip (category chips, screenshots) that works with every input: a normal mouse
  // wheel scrolls it while the pointer is over it, previous/next buttons show while it
  // overflows, the arrow keys move between its items, touch and trackpads scroll natively.
  // Extra attributes (role, aria-label) go on the strip itself.
  import { hscroll } from './hscroll.js';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';

  let { label = 'items', gap = '0.375rem', children, ...rest } = $props();
  let track = $state();
  let sides = $state({ any: false, start: false, end: false });

  function page(dir) {
    const smooth = !matchMedia('(prefers-reduced-motion: reduce)').matches;
    track.scrollBy({ left: dir * Math.max(120, track.clientWidth * 0.8), behavior: smooth ? 'smooth' : 'auto' });
  }
</script>

<div class="hs">
  {#if sides.any}
    <button type="button" class="icon-btn hs-btn" tabindex="-1" aria-hidden="true" title="Scroll {label} back"
      disabled={!sides.start} onclick={() => page(-1)}><ChevronLeft size={16} strokeWidth={2.4} /></button>
  {/if}
  <div class="hs-track" style:gap bind:this={track} use:hscroll={{ onchange: (s) => (sides = s) }} {...rest}>
    {@render children()}
  </div>
  {#if sides.any}
    <button type="button" class="icon-btn hs-btn" tabindex="-1" aria-hidden="true" title="Scroll {label} on"
      disabled={!sides.end} onclick={() => page(1)}><ChevronRight size={16} strokeWidth={2.4} /></button>
  {/if}
</div>

<style>
  .hs { display: flex; align-items: center; gap: 0.25rem; min-width: 0; }
  /* Room above and below so focus rings are not cut off by the scroll box. */
  .hs-track {
    flex: 1; min-width: 0; display: flex; overflow-x: auto; overscroll-behavior-x: contain;
    scrollbar-width: none; padding: 3px 0; margin: -3px 0;
  }
  .hs-track::-webkit-scrollbar { display: none; }
  .hs-btn { flex-shrink: 0; width: 2.25rem; height: 2.25rem; }
  .hs-btn:disabled { opacity: 0.3; cursor: default; background: none; }
</style>
