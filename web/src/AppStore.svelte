<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import AppIcon from '$lib/AppIcon.svelte';
  import Dialog from '$lib/Dialog.svelte';
  import AppDetail from './AppDetail.svelte';
  import DockerSetup from './DockerSetup.svelte';
  import { sheet, backgrounded, installs } from '$lib/installs.svelte.js';
  import Search from '@lucide/svelte/icons/search';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';

  let { onSessionLost } = $props();

  const PAGE = 48;
  let catalog = $state(null);
  let error = $state('');
  let docker = $state(null);
  let installed = $state(new Set());
  let query = $state('');
  let cat = $state('All');
  let shown = $state(PAGE);
  let syncing = $state(false);
  let selected = $state(null);
  let open = $state(false);
  let origin = $state(null);
  let sentinel = $state();

  // Closing the sheet of a running install only hides it; the install goes on.
  let lastOpen = '';
  $effect(() => {
    const id = open && selected ? selected.id : '';
    sheet.id = id;
    if (!id && lastOpen) backgrounded(lastOpen, selected?.name);
    lastOpen = id;
    return () => { if (sheet.id === id) sheet.id = ''; };
  });
  // An install that finished while its sheet was closed shows as installed.
  $effect(() => {
    for (const [id, it] of Object.entries(installs)) if (it.status === 'done' && !installed.has(id)) installed = new Set([...installed, id]);
  });

  let categories = $derived.by(() => {
    if (!catalog) return [];
    const counts = {};
    for (const a of catalog.apps) counts[a.category || 'Other'] = (counts[a.category || 'Other'] || 0) + 1;
    return ['All', ...Object.keys(counts).sort((a, b) => counts[b] - counts[a])];
  });

  let filtered = $derived.by(() => {
    if (!catalog) return [];
    const q = query.trim().toLowerCase();
    return catalog.apps.filter((a) =>
      (cat === 'All' || (a.category || 'Other') === cat) &&
      (!q || a.name.toLowerCase().includes(q) || (a.tagline || '').toLowerCase().includes(q)));
  });

  $effect(() => { query; cat; shown = PAGE; });

  async function load() {
    error = '';
    const [c, d, i] = await Promise.all([api('/api/catalog'), api('/api/docker'), api('/api/apps')]);
    if (c.status === 401) return onSessionLost();
    if (!c.ok) { error = c.error; return; }
    catalog = c.data;
    docker = d.ok ? d.data : { installed: false, error: d.error };
    if (!d.ok) toast(d.error, 'error');
    installed = new Set(i.ok ? i.data.map((a) => a.id) : []);
    // Deep link from the home search: /apps?app=<id> opens that app.
    const want = new URLSearchParams(location.search).get('app');
    const hit = want && catalog.apps.find((x) => x.id === want);
    if (hit) { selected = hit; open = true; }
  }

  async function sync() {
    syncing = true;
    const res = await api('/api/catalog/sync', { method: 'POST' });
    syncing = false;
    toast(res.ok ? res.data.message : res.error, res.ok ? 'info' : 'error');
    if (res.ok) load();
  }

  function pick(e, a) {
    origin = { x: e.clientX, y: e.clientY };
    selected = a;
    open = true;
  }

  async function refreshInstalled() {
    const i = await api('/api/apps');
    if (i.ok) installed = new Set(i.data.map((a) => a.id));
  }

  // Render more cards as the end of the list comes near.
  $effect(() => {
    if (!sentinel) return;
    const io = new IntersectionObserver((es) => {
      if (es.some((e) => e.isIntersecting)) shown += PAGE;
    }, { rootMargin: '600px' });
    io.observe(sentinel);
    return () => io.disconnect();
  });

  onMount(load);
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">App Store</h1>
      <p class="t-sub">
        {#if catalog}{catalog.apps.length} apps, installed with one click as Docker containers.{:else}<span class="skeleton inline"></span>{/if}
      </p>
    </div>
    <button class="btn btn-gray btn-sm" onclick={sync} disabled={syncing || !catalog}>
      <RefreshCw size={15} class={syncing ? 'spinner' : ''} />Check for new apps
    </button>
  </header>

  {#if docker && (!docker.installed || !docker.running) && !docker.error}
    <DockerSetup {docker} onDone={load} />
  {/if}

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else}
    <div class="controls">
      <label class="search">
        <Search size={17} />
        <input type="search" placeholder="Search apps" bind:value={query} aria-label="Search apps" />
      </label>
      <div class="chips" role="tablist" aria-label="Categories">
        {#each categories as c (c)}
          <button class="chip" role="tab" aria-selected={c === cat} onclick={() => (cat = c)}>{c}</button>
        {/each}
      </div>
    </div>

    {#if !catalog}
      <div class="grid">
        {#each Array(12) as _}<div class="card skel"><span class="skeleton sq"></span><span class="skeleton line"></span></div>{/each}
      </div>
    {:else if !filtered.length}
      <div class="tile empty t-sub">No app matches "{query}".</div>
    {:else}
      <div class="grid">
        {#each filtered.slice(0, shown) as a (a.id)}
          <button class="card" onclick={(e) => pick(e, a)}>
            <AppIcon id={a.id} name={a.name} size={52} />
            <span class="card-text">
              <span class="t-head truncate">{a.name}</span>
              <span class="t-caption tagline">{a.tagline || a.category}</span>
              <span class="card-foot">
                <span class="t-caption">{a.category}</span>
                {#if installed.has(a.id)}<span class="pill sm">Installed</span>{/if}
              </span>
            </span>
          </button>
        {/each}
      </div>
      {#if shown < filtered.length}<div bind:this={sentinel} class="sentinel" aria-hidden="true"></div>{/if}
    {/if}
  {/if}
</div>

<Dialog bind:open label={selected?.name || 'App'} {origin} wide>
  {#if selected}
    <AppDetail summary={selected} arch={catalog?.arch} installed={installed.has(selected.id)} {docker}
      onClose={() => (open = false)} onInstalled={refreshInstalled} {onSessionLost} />
  {/if}
</Dialog>

<style>
  h1, p { margin: 0; }
  .skeleton.inline { display: inline-block; width: 16rem; height: 0.9rem; vertical-align: middle; }

  .controls { display: grid; gap: 0.875rem; position: sticky; top: 3.75rem; z-index: 5; padding: 0.25rem 0 0.5rem; background: color-mix(in srgb, var(--bg) 88%, transparent); -webkit-backdrop-filter: blur(16px); backdrop-filter: blur(16px); }
  .search { display: flex; align-items: center; gap: 0.625rem; height: 2.75rem; padding: 0 0.875rem; border-radius: var(--r-control); background: var(--surface); box-shadow: var(--shadow-tile); color: var(--text-3); max-width: 32rem; }
  .search:focus-within { box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 45%, transparent); }
  .search input { flex: 1; min-width: 0; border: 0; background: none; height: 100%; font-size: 0.9375rem; color: var(--text); outline: none; }
  .chips { display: flex; gap: 0.375rem; overflow-x: auto; scrollbar-width: none; padding-bottom: 2px; }
  .chips::-webkit-scrollbar { display: none; }
  .chip {
    flex-shrink: 0; height: 2rem; padding: 0 0.875rem; border: 0; border-radius: var(--r-pill); cursor: pointer;
    background: var(--fill); color: var(--text-2); font-size: 0.8125rem; font-weight: 600;
    transition: background-color 150ms ease, color 150ms ease, transform 100ms ease-out;
  }
  .chip:active { transform: scale(0.96); }
  .chip[aria-selected='true'] { background: var(--accent); color: var(--on-accent); }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(17rem, 1fr)); gap: 1rem; }
  .card {
    display: flex; gap: 0.875rem; align-items: flex-start; text-align: left; padding: 1rem; border: 0; cursor: pointer;
    background: var(--surface); border-radius: var(--r-tile); box-shadow: var(--shadow-tile);
    transition: transform 100ms ease-out, box-shadow 200ms ease;
  }
  .card:hover { box-shadow: var(--shadow-float); }
  .card:active { transform: scale(0.98); }
  .card-text { display: grid; gap: 0.25rem; min-width: 0; flex: 1; }
  .tagline { display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; min-height: 2.05em; }
  .card-foot { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; margin-top: 0.125rem; }
  .card-foot .t-caption { color: var(--text-3); }
  .pill.sm { height: 1.375rem; padding: 0 0.5rem; font-size: 0.6875rem; background: var(--accent-fill); color: var(--accent); }
  .card.skel { cursor: default; display: grid; gap: 0.75rem; }
  .skeleton.sq { width: 52px; height: 52px; border-radius: 12px; }
  .skeleton.line { display: block; width: 70%; height: 0.8rem; }
  .empty { text-align: center; }
  .sentinel { height: 1px; }
</style>
