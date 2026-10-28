<script>
  // Tiny history line (0..100). SVG attributes only, so it works under the strict CSP.
  let { values = [], max = 100, class: cls = 'text-primary' } = $props();
  const W = 120, H = 32;
  let points = $derived(
    values.map((v, i) => `${(i / Math.max(1, values.length - 1)) * W},${H - (Math.min(v, max) / max) * (H - 2) - 1}`).join(' ')
  );
</script>

<svg viewBox="0 0 {W} {H}" preserveAspectRatio="none" width="100%" height={H} class={cls} aria-hidden="true">
  {#if values.length > 1}
    <polyline points={points} fill="none" stroke="currentColor" stroke-width="1.5" vector-effect="non-scaling-stroke" stroke-linejoin="round" />
  {/if}
</svg>
