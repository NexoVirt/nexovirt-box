<script>
  // Live history line. New samples scroll in from the right (one step per sample) instead of jumping.
  // SVG attributes + transform only, so it works under the strict CSP.
  let { values = [], seq = 0, capacity = 60, max = 100, height = 40, fill = true, interval = 2000, color = 'var(--accent)', top: fixedTop = 0 } = $props();
  const W = 300;
  let top = $derived(fixedTop || (max > 0 ? max : Math.max(1, ...values) * 1.15));
  let step = $derived(W / Math.max(1, capacity - 1));

  // Newest sample sits on the right edge.
  let pts = $derived(values.map((v, i) => [W - (values.length - 1 - i) * step, height - (Math.min(v, top) / top) * (height - 3) - 1.5]));

  function smooth(p) {
    if (p.length < 2) return '';
    let d = `M${p[0][0]},${p[0][1]}`;
    for (let i = 0; i < p.length - 1; i++) {
      const [x0, y0] = p[i - 1] || p[i], [x1, y1] = p[i], [x2, y2] = p[i + 1], [x3, y3] = p[i + 2] || p[i + 1];
      d += ` C${x1 + (x2 - x0) / 6},${y1 + (y2 - y0) / 6} ${x2 - (x3 - x1) / 6},${y2 - (y3 - y1) / 6} ${x2},${y2}`;
    }
    return d;
  }
  let line = $derived(smooth(pts));
  let area = $derived(pts.length > 1 ? `${line} L${pts[pts.length - 1][0]},${height} L${pts[0][0]},${height} Z` : '');

  let g = $state();
  const reduce = matchMedia('(prefers-reduced-motion: reduce)');
  let last = 0;

  // On each new sample: start shifted right by one step (old points where they were), then glide left.
  // seq = total samples seen (keeps counting once the history is full).
  $effect(() => {
    const n = seq;
    if (!g || n === last) return;
    const first = last === 0;
    last = n;
    if (first || values.length < 2 || reduce.matches) return;
    g.style.transition = 'none';
    g.style.transform = `translateX(${step}px)`;
    g.getBoundingClientRect(); // commit the shifted start before gliding
    g.style.transition = `transform ${interval}ms linear`;
    g.style.transform = 'translateX(0)';
  });
</script>

<svg viewBox="0 0 {W} {height}" preserveAspectRatio="none" width="100%" {height} aria-hidden="true">
  <g bind:this={g}>
    {#if fill && area}<path d={area} fill={color} fill-opacity="0.1" />{/if}
    {#if line}
      <path d={line} fill="none" stroke={color} stroke-width="1.75" vector-effect="non-scaling-stroke"
        stroke-linejoin="round" stroke-linecap="round" />
    {/if}
  </g>
</svg>

<style>
  svg { display: block; overflow: hidden; }
</style>
