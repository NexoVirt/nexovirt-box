<script>
  // Activity log as a data table in a card that fills the page. Pages load from the server as you
  // scroll (cursor paging), with filters for type and user, a search and a sortable time column.
  import { onMount } from 'svelte';
  import DataTable from '$lib/DataTable.svelte';
  import { api } from '$lib/api.js';
  import { ago } from '$lib/format.js';
  import { activityLabel as label } from '$lib/activityLabels.js';
  import Activity from '@lucide/svelte/icons/activity';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';

  let { onSessionLost } = $props();

  const PAGE = 50;
  const TYPES = [
    { value: '', label: 'All activity' },
    { value: 'signin', label: 'Sign-ins and security' },
    { value: 'console', label: 'Console' },
    { value: 'apps', label: 'Apps and Docker' },
    { value: 'files', label: 'Files' },
    { value: 'firewall', label: 'Firewall' },
    { value: 'system', label: 'Settings and updates' },
  ];

  let items = $state(null);
  let total = $state(0);
  let next = $state(0);
  let users = $state([]);
  let loading = $state(false);
  let error = $state('');
  let type = $state(''), user = $state(''), query = $state('');
  let sort = $state({ key: 'at', dir: 'desc' });
  let seq = 0;

  function params(cursor) {
    const p = new URLSearchParams({ limit: String(PAGE), order: sort.dir === 'asc' ? 'asc' : 'desc' });
    if (cursor) p.set('cursor', String(cursor));
    if (type) p.set('type', type);
    if (user) p.set('user', user);
    if (query.trim()) p.set('q', query.trim());
    return p;
  }

  // reset reloads from the top (filters, sort or refresh changed).
  async function reset() {
    const my = ++seq;
    loading = true;
    error = '';
    const res = await api('/api/activity?' + params(0));
    if (my !== seq) return;
    loading = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) { error = res.error; items = items || []; return; }
    items = res.data.items;
    total = res.data.total;
    next = res.data.next;
    users = res.data.users;
  }

  async function more() {
    if (loading || !next) return;
    const my = seq;
    loading = true;
    const res = await api('/api/activity?' + params(next));
    if (my !== seq) return;
    loading = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) { error = res.error; return; }
    items = [...items, ...res.data.items];
    total = res.data.total;
    next = res.data.next;
  }

  const timeFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' });
  const COLUMNS = [
    { key: 'action', label: 'Event', width: '30%', primary: true },
    { key: 'detail', label: 'Details', width: '30%', hide: 'md' },
    { key: 'username', label: 'User', width: '10%' },
    { key: 'ip', label: 'Address', width: '12%', mono: true, hide: 'md' },
    { key: 'at', label: 'Time', width: '18%', align: 'right', sortable: true, firstDir: 'desc' },
  ];
  let filtering = $derived(!!(query || type || user));

  onMount(reset);
</script>

<div class="page fill">
  <header class="page-head compact">
    <div>
      <h1 class="t-large">Activity</h1>
      <p class="t-sub">Sign-ins, console sessions, file and app changes on this Box.</p>
    </div>
  </header>
  <DataTable fill fixed server label="Activity" columns={COLUMNS} rows={items} key="id" searchable bind:search={query} placeholder="Search the activity"
    onsearch={reset} bind:sort onsort={reset} onmore={more} hasMore={!!next} busy={loading} {total} {error} onretry={reset}
    empty={{ icon: Activity, title: filtering ? 'Nothing matches these filters' : 'Nothing happened yet' }}>
    {#snippet toolbar()}
      <select class="input" bind:value={type} onchange={reset} aria-label="Type">
        {#each TYPES as t (t.value)}<option value={t.value}>{t.label}</option>{/each}
      </select>
      <select class="input" bind:value={user} onchange={reset} aria-label="User">
        <option value="">All users</option>
        {#each users as u (u)}<option value={u}>{u}</option>{/each}
      </select>
      <button class="icon-btn" onclick={reset} aria-label="Refresh" title="Refresh"><RefreshCw size={16} class={loading ? 'spinner' : ''} /></button>
    {/snippet}
    {#snippet cell(it, c)}
      {#if c.key === 'action'}
        {@const [text, Icon, tone] = label(it.action)}
        <span class="what"><span class="ico" class:accent={tone !== 'error'} class:error={tone === 'error'}><Icon /></span><span class="truncate">{text}</span></span>
      {:else if c.key === 'detail'}
        <span class="muted" title={it.detail}>{it.detail}</span>
      {:else if c.key === 'username'}
        {it.username || 'Unknown'}
      {:else if c.key === 'ip'}
        <span class="muted">{it.ip}</span>
      {:else}
        <span class="num muted" title={timeFmt.format(it.at * 1000)}><span class="ago">{ago(it.at)}</span><span class="abs">{timeFmt.format(it.at * 1000)}</span></span>
      {/if}
    {/snippet}
  </DataTable>
</div>

<style>
  .what { display: flex; align-items: center; gap: 0.625rem; min-width: 0; }
  .what .truncate { min-width: 0; }
  .muted { color: var(--text-2); }
  .ago { display: none; }
  @media (max-width: 899px) {
    .ago { display: inline; }
    .abs { display: none; }
  }
</style>
