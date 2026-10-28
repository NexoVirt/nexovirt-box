<script>
  // Tiny history line. SVG attributes only, so it works under the strict CSP.
  let { values = [], max = 100, class: cls = 'text-primary' } = $props();
  const W = 120, H = 36;
  let top = $derived(max > 0 ? max : Math.max(1, ...values));
  let points = $derived(
    values.map((v, i) => `${(i / Math.max(1, values.length - 1)) * W},${H - (Math.min(v, top) / top) * (H - 4) - 2}`).join(' ')
  );
</script>

<svg viewBox="0 0 {W} {H}" preserveAspectRatio="none" width="100%" height={H} class={cls} aria-hidden="true">
  {#if values.length > 1}
    <polyline points={points} fill="none" stroke="currentColor" stroke-width="1.75" vector-effect="non-scaling-stroke" stroke-linejoin="round" stroke-linecap="round" />
  {/if}
</svg>
