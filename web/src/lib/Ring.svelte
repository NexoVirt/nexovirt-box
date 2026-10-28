<script>
  // Activity-ring style gauge. Accent while healthy, warn/error colours only when the value is high.
  let { value = 0, size = 112, stroke = 10, label = '', caption = '' } = $props();
  const r = $derived((size - stroke) / 2);
  const c = $derived(2 * Math.PI * r);
  const v = $derived(Math.max(0, Math.min(100, value)));
  const color = $derived(v >= 90 ? 'var(--error)' : v >= 75 ? 'var(--warn)' : 'var(--accent)');
</script>

<figure class="ring" style:--size="{size}px">
  <svg width={size} height={size} viewBox="0 0 {size} {size}" aria-hidden="true">
    <circle cx={size / 2} cy={size / 2} {r} fill="none" stroke="var(--fill)" stroke-width={stroke} />
    <circle
      class="arc" opacity={v < 0.5 ? 0 : 1} cx={size / 2} cy={size / 2} {r} fill="none" stroke={color} stroke-width={stroke}
      stroke-linecap="round" stroke-dasharray={c} stroke-dashoffset={c * (1 - v / 100)}
      transform="rotate(-90 {size / 2} {size / 2})"
    />
  </svg>
  <div class="center">
    <span class="value num">{Math.round(v)}<small>%</small></span>
  </div>
  <figcaption>
    <span class="t-head">{label}</span>
    {#if caption}<span class="t-caption num">{caption}</span>{/if}
  </figcaption>
</figure>

<style>
  .ring { margin: 0; display: grid; justify-items: center; gap: 0.75rem; position: relative; }
  .center { position: absolute; top: 0; width: var(--size); height: var(--size); display: grid; place-items: center; }
  .value { font-size: 1.625rem; font-weight: 650; letter-spacing: -0.03em; line-height: 1; }
  .value small { font-size: 0.8125rem; font-weight: 600; color: var(--text-2); margin-left: 1px; letter-spacing: 0; }
  .arc { transition: stroke-dashoffset var(--spring-t) var(--spring), stroke 300ms ease; }
  figcaption { display: grid; justify-items: center; text-align: center; gap: 0.125rem; }
</style>
