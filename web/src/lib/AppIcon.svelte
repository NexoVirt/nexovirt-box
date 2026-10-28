<script>
  // App icon from the Box media proxy (catalog apps and uploaded icons of own apps), a Lucide
  // icon for own apps that picked one, else a letter tile.
  import { OWN_ICONS } from './ownIcons.js';
  let { id, name = '', size = 48, icon = '', v = 0 } = $props();
  let failed = $state(false);
  $effect(() => { id; v; failed = false; });
  let Lucide = $derived(icon?.startsWith('lucide:') ? OWN_ICONS[icon.slice(7)] : null);
</script>

<span class="icon" style:width="{size}px" style:height="{size}px" style:border-radius="{Math.round(size * 0.24)}px" style:font-size="{Math.round(size * 0.42)}px">
  {#if Lucide}
    <Lucide size={Math.round(size * 0.5)} strokeWidth={2} />
  {:else if !failed}
    <img src="/api/catalog/{id}/icon{v ? `?v=${v}` : ''}" alt="" loading="lazy" decoding="async" onerror={() => (failed = true)} />
  {:else}
    {(name || id).slice(0, 1).toUpperCase()}
  {/if}
</span>

<style>
  .icon {
    display: grid; place-items: center; flex-shrink: 0; overflow: hidden;
    background: var(--accent-fill); color: var(--accent); font-weight: 700;
    box-shadow: 0 0 0 1px var(--hairline);
  }
  img { width: 100%; height: 100%; object-fit: cover; background: #fff; }
</style>
