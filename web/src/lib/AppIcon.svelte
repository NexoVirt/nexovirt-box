<script>
  import { apiPath } from './remote.js';
  // App icon from the Box media proxy (catalog apps and uploaded icons of own apps), placed as is
  // (no plate, frame or shadow). Own apps may pick a Lucide icon; a plain Lucide glyph stands in
  // when the app has no icon.
  import Package from '@lucide/svelte/icons/package';
  import { OWN_ICONS } from './ownIcons.js';

  let { id, name = '', size = 48, icon = '', v = 0 } = $props();
  let failed = $state(false);
  $effect(() => { id; v; failed = false; });
  let Lucide = $derived(icon?.startsWith('lucide:') ? OWN_ICONS[icon.slice(7)] : null);
</script>

<span class="icon" style:width="{size}px" style:height="{size}px" title={name || undefined}>
  {#if Lucide}
    <Lucide size={Math.round(size * 0.8)} strokeWidth={1.5} />
  {:else if !failed}
    <img src={apiPath(`/api/catalog/${id}/icon${v ? `?v=${v}` : ''}`)} alt="" loading="lazy" decoding="async" onerror={() => (failed = true)} />
  {:else}
    <Package size={Math.round(size * 0.8)} strokeWidth={1.5} />
  {/if}
</span>

<style>
  .icon { display: grid; place-items: center; flex-shrink: 0; color: var(--text-2); }
  img { width: 100%; height: 100%; object-fit: contain; }
</style>
