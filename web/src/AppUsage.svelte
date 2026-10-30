<script>
  // Live usage per app (Monitor): CPU, memory against its limit, network since start, disk.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { bytes, pct, level } from '$lib/format.js';
  import DataTable from '$lib/DataTable.svelte';
  import AppIcon from '$lib/AppIcon.svelte';
  import LayoutGrid from '@lucide/svelte/icons/layout-grid';

  let { onSessionLost } = $props();
  let rows = $state(null), error = $state('');
  let names = {};

  const COLUMNS = [
    { key: 'name', label: 'App', width: '28%', primary: true, sortable: true },
    { key: 'cpu', label: 'CPU', width: '14%', align: 'right', sortable: true, firstDir: 'desc' },
    { key: 'mem', label: 'Memory', width: '24%', sortable: true, firstDir: 'desc' },
    { key: 'net', label: 'Network in / out', width: '18%', align: 'right', hide: 'md', value: (r) => r.net_rx + r.net_tx },
    { key: 'disk', label: 'Disk', width: '16%', align: 'right', sortable: true, firstDir: 'desc' },
  ];

  async function loadNames() {
    const res = await api('/api/apps');
    if (res.ok) names = Object.fromEntries(res.data.map((a) => [a.id, { name: a.name, icon: a.icon }]));
  }

  async function load() {
    if (document.hidden) return;
    const res = await api('/api/apps/stats');
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { error = res.error; return; }
    error = '';
    rows = Object.entries(res.data).map(([id, u]) => ({ id, ...u, name: names[id]?.name || id, icon: names[id]?.icon }));
  }

  onMount(() => {
    loadNames().then(load);
    const t = setInterval(load, 10000), n = setInterval(loadNames, 60000);
    return () => { clearInterval(t); clearInterval(n); };
  });
</script>

<DataTable label="Apps" title="Apps" description="Live use per app. Memory shows the limit when you set one (app menu, Resource limits)." columns={COLUMNS} {rows} {error} onretry={load}
  sort={{ key: 'cpu', dir: 'desc' }} empty={{ icon: LayoutGrid, title: 'No apps installed' }}>
  {#snippet cell(r, c)}
    {#if c.key === 'name'}
      <span class="app"><AppIcon id={r.id} name={r.name} size={24} icon={r.icon} /><span class="truncate">{r.name}</span></span>
    {:else if c.key === 'cpu'}
      <span class="num">{r.cpu.toFixed(1)} %</span>
    {:else if c.key === 'mem'}
      {@const lim = r.limits?.memory_mb ? r.mem_limit : 0}
      <span class="mem">
        <span class="num">{bytes(r.mem)}{#if lim}<span class="t-caption"> of {bytes(lim)}</span>{/if}</span>
        {#if lim}{@const p = pct(r.mem, lim)}<span class="bar"><span style:transform="scaleX({Math.min(p, 100) / 100})" style:background={p >= 75 ? level(p) : 'var(--accent)'}></span></span>{/if}
      </span>
    {:else if c.key === 'net'}
      <span class="num muted">{bytes(r.net_rx)} / {bytes(r.net_tx)}</span>
    {:else}
      <span class="num">{r.disk < 0 ? 'Measuring' : bytes(r.disk)}</span>
    {/if}
  {/snippet}
</DataTable>

<style>
  .app { display: flex; align-items: center; gap: 0.625rem; min-width: 0; }
  .mem { display: grid; gap: 0.3rem; }
  .muted { color: var(--text-2); }
</style>
