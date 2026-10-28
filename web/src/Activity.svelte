<script>
  // Activity log as a data table in a card that fills the page. Pages load from the server as you
  // scroll (cursor paging), with filters for type and user, a search and a sortable time column.
  import { onMount } from 'svelte';
  import DataTable from '$lib/DataTable.svelte';
  import { api } from '$lib/api.js';
  import { ago } from '$lib/format.js';
  import LogIn from '@lucide/svelte/icons/log-in';
  import LogOut from '@lucide/svelte/icons/log-out';
  import ShieldAlert from '@lucide/svelte/icons/shield-alert';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import SquareTerminal from '@lucide/svelte/icons/square-terminal';
  import Package from '@lucide/svelte/icons/package';
  import Container from '@lucide/svelte/icons/container';
  import FolderOpen from '@lucide/svelte/icons/folder-open';
  import Activity from '@lucide/svelte/icons/activity';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';

  let { onSessionLost } = $props();

  const PAGE = 50;
  // Readable text per audit action; unknown actions show as they are.
  const LABELS = {
    'login.ok': ['Signed in', LogIn],
    'login.failed': ['Sign-in failed', ShieldAlert, 'error'],
    'logout': ['Signed out', LogOut],
    'reauth.ok': ['Confirmed password', ShieldCheck],
    'reauth.failed': ['Wrong password at confirmation', ShieldAlert, 'error'],
    'account.password': ['Changed the password', ShieldCheck],
    'account.password_failed': ['Wrong current password', ShieldAlert, 'error'],
    'account.sessions_end': ['Signed out other browsers', LogOut],
    'console.open': ['Opened the console', SquareTerminal],
    'console.close': ['Closed the console', SquareTerminal],
    'docker.install': ['Installed Docker', Container],
    'catalog.sync': ['Synced the app catalog', Package],
    'settings.change': ['Changed a setting', Activity],
    '2fa.totp_on': ['Turned on the authenticator app', ShieldCheck],
    '2fa.totp_off': ['Turned off the authenticator app', ShieldAlert, 'error'],
    '2fa.passkey_add': ['Added a passkey', ShieldCheck],
    '2fa.passkey_remove': ['Removed a passkey', ShieldAlert],
    '2fa.recovery_new': ['Made new recovery codes', ShieldCheck],
    '2fa.password_failed': ['Wrong password in security settings', ShieldAlert, 'error'],
    'firewall.apply': ['Changed firewall rules', ShieldCheck],
    'firewall.apply_failed': ['A firewall change failed', ShieldAlert, 'error'],
    'firewall.confirm': ['Kept the firewall rules', ShieldCheck],
    'files.upload': ['Uploaded a file', FolderOpen],
    'files.download': ['Downloaded', FolderOpen],
    'files.delete': ['Deleted a file or folder', FolderOpen, 'error'],
    'files.move': ['Moved or renamed', FolderOpen],
    'files.copy': ['Copied', FolderOpen],
    'files.mkdir': ['Created a folder', FolderOpen],
    'files.roots': ['Changed the folders in Files', FolderOpen],
    'proxy.setup': ['Set up the reverse proxy', Container],
    'proxy.remove': ['Turned off the reverse proxy', Container],
    'images.prune': ['Cleaned up unused images', Container],
    'box.update': ['Updated NexoVirt Box', Package],
    'backup.run': ['Backed up', Package],
    'backup.failed': ['A backup failed', ShieldAlert, 'error'],
    'backup.restore': ['Restored a backup', Package],
    'backup.restore.failed': ['A restore failed', ShieldAlert, 'error'],
    'backup.verify': ['Checked a backup', ShieldCheck],
    'backup.verify.failed': ['A backup check failed', ShieldAlert, 'error'],
    'backup.delete': ['Deleted a backup', Package, 'error'],
    'backup.prune': ['Removed old backups', Package],
    'backup.key_create': ['Created the backup key', ShieldCheck],
    'backup.key_import': ['Imported a backup key', ShieldAlert],
    'backup.target_save': ['Saved a backup target', Package],
    'backup.target_delete': ['Removed a backup target', Package],
    'backup.schedule_save': ['Saved a backup schedule', Package],
    'backup.schedule_delete': ['Deleted a backup schedule', Package],
    'app.custom_add': ['Added an own app', Package],
    'app.custom_edit': ['Changed an own app', Package],
    'app.custom_view': ['Opened an own app definition', Package],
    'notify.channel_save': ['Saved a notification channel', Activity],
    'notify.channel_delete': ['Deleted a notification channel', Activity],
    'notify.test': ['Sent a test notification', Activity],
    'notify.rules': ['Changed notification events', Activity],
  };
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

  function label(a) {
    if (LABELS[a]) return LABELS[a];
    if (a.startsWith('app.')) return [a.slice(4).replace(/[._]/g, ' ').replace(/^\w/, (c) => c.toUpperCase()), Package];
    return [a, Activity];
  }

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
