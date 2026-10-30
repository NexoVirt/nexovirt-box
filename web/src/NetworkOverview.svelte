<script>
  // Devices in the local network and the ports this box listens on (with the app behind each).
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import DataTable from '$lib/DataTable.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Radar from '@lucide/svelte/icons/radar';
  import Network from '@lucide/svelte/icons/network';
  import PlugZap from '@lucide/svelte/icons/plug-zap';

  let { onSessionLost } = $props();
  let data = $state(null), error = $state(''), sweeping = $state(false);

  async function load(sweep = false) {
    if (sweep) sweeping = true;
    const res = await api('/api/network/overview', sweep ? { method: 'POST', body: {} } : undefined);
    sweeping = false;
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { error = res.error; data = { devices: [], listeners: [] }; return; }
    error = ''; data = res.data;
  }

  const DEV_COLS = [
    { key: 'name', label: 'Device', primary: true, sortable: true, value: (d) => d.name || d.ip },
    { key: 'ip', label: 'Address', mono: true, sortable: true },
    { key: 'mac', label: 'Hardware address', mono: true, hide: 'md' },
    { key: 'state', label: 'Seen', hide: 'sm' },
  ];
  const PORT_COLS = [
    { key: 'port', label: 'Port', sortable: true, align: 'right', width: '12%' },
    { key: 'proto', label: 'Protocol', width: '12%' },
    { key: 'addr', label: 'Listens on', mono: true, hide: 'md' },
    { key: 'who', label: 'Program', primary: true, sortable: true, value: (l) => l.app || l.process },
  ];
  onMount(() => load(false));
</script>

<p class="t-section sub">Devices and ports</p>
{#if error}<div class="notice">{error}</div>{/if}
<DataTable label="Devices in the network" title="Devices in the network" description={data ? `Seen by this box in ${data.lan || 'the local network'}. A scan pings the addresses of its own /24 once.` : ''}
  columns={DEV_COLS} rows={data?.devices ?? null} key="ip" maxHeight="20rem" empty={{ icon: Network, title: 'No devices seen yet', text: 'Scan the network to find them.' }}>
  {#snippet toolbar()}
    <button class="btn btn-gray btn-sm" onclick={() => load(true)} disabled={sweeping}>{#if sweeping}<Loader size={15} class="spinner" />Scanning{:else}<Radar size={15} />Scan the network{/if}</button>
  {/snippet}
  {#snippet cell(d, c)}
    {#if c.key === 'name'}{d.name || 'Unknown device'}
    {:else if c.key === 'state'}<span class="muted">{d.state}</span>
    {:else}{d[c.key]}{/if}
  {/snippet}
</DataTable>
<DataTable label="Open ports" title="Ports on this box" description="Programs and apps that listen for connections." columns={PORT_COLS} rows={data?.listeners ?? null}
  key={(l) => l.proto + l.port + l.process} maxHeight="20rem" empty={{ icon: PlugZap, title: 'Nothing listens' }}>
  {#snippet cell(l, c)}
    {#if c.key === 'who'}{#if l.app}<b>{l.app}</b> <span class="muted">(app)</span>{:else}{l.process || 'Unknown'}{/if}
    {:else if c.key === 'port'}<span class="num">{l.port}</span>
    {:else}{l[c.key]}{/if}
  {/snippet}
</DataTable>

<style>
  .muted { color: var(--text-2); }
</style>
