<script>
  // App icon from the Box media proxy, placed as is (no plate, frame or shadow). A plain Lucide
  // glyph stands in when the app has no icon.
  import Package from '@lucide/svelte/icons/package';

  let { id, name = '', size = 48 } = $props();
  let failed = $state(false);
  $effect(() => { id; failed = false; });
</script>

<span class="icon" style:width="{size}px" style:height="{size}px" title={name || undefined}>
  {#if !failed}
    <img src="/api/catalog/{id}/icon" alt="" loading="lazy" decoding="async" onerror={() => (failed = true)} />
  {:else}
    <Package size={Math.round(size * 0.8)} strokeWidth={1.5} />
  {/if}
</span>

<style>
  .icon { display: grid; place-items: center; flex-shrink: 0; color: var(--text-2); }
  img { width: 100%; height: 100%; object-fit: contain; }
</style>
