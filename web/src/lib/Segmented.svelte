<script>
  // Segmented control with a sliding thumb (critically damped spring, interruptible: it animates
  // from wherever it is when you click again).
  let { options, value = $bindable(), label = '' } = $props();
  let index = $derived(Math.max(0, options.findIndex((o) => o.value === value)));
</script>

<div class="seg" role="radiogroup" aria-label={label} style:--n={options.length} style:--i={index}>
  <span class="thumb" aria-hidden="true"></span>
  {#each options as o (o.value)}
    <button type="button" role="radio" aria-checked={o.value === value} onclick={() => (value = o.value)}>{o.label}</button>
  {/each}
</div>

<style>
  .seg {
    position: relative; display: grid; grid-template-columns: repeat(var(--n), 1fr);
    padding: 2px; border-radius: 0.6rem; background: var(--fill); min-width: calc(var(--n) * 3.75rem);
  }
  .thumb {
    position: absolute; top: 2px; bottom: 2px; left: 2px; width: calc((100% - 4px) / var(--n));
    border-radius: 0.5rem; background: var(--surface);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08), 0 0 0 0.5px var(--hairline);
    transform: translateX(calc(var(--i) * 100%));
    transition: transform var(--spring-t) var(--spring);
  }
  :global(.dark) .thumb { background: #3f3f46; }
  button {
    position: relative; z-index: 1; border: 0; background: none; cursor: pointer;
    height: 1.875rem; padding: 0 0.75rem; font-size: 0.8125rem; font-weight: 600; color: var(--text-2);
    border-radius: 0.5rem; transition: color 150ms ease;
  }
  button[aria-checked='true'] { color: var(--text); }
  /* Larger hit area than the visual control. */
  button::after { content: ''; position: absolute; inset: -7px 0; }
</style>
