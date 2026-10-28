<script>
  // Activity log as a data table in a card that fills the page. Pages load from the server as you
  // scroll (cursor paging), with filters for type and user, a search and a sortable time column.
  import { onMount } from 'svelte';
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
  import Search from '@lucide/svelte/icons/search';
  import ArrowUp from '@lucide/svelte/icons/arrow-up';
  import ArrowDown from '@lucide/svelte/icons/arrow-down';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import X from '@lucide/svelte/icons/x';

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

  let items = $state([]);
  let total = $state(0);
  let next = $state(0);
  let users = $state([]);
  let loading = $state(false);
  let first = $state(true);
  let error = $state('');
  let type = $state(''), user = $state(''), query = $state('');
  let asc = $state(false);
  let scroller = $state(), sentinel = $state();
  let seq = 0, searchTimer;

  function label(a) {
    if (LABELS[a]) return LABELS[a];
    if (a.startsWith('app.')) return [a.slice(4).replace(/[._]/g, ' ').replace(/^\w/, (c) => c.toUpperCase()), Package];
    return [a, Activity];
  }

  function params(cursor) {
    const p = new URLSearchParams({ limit: String(PAGE), order: asc ? 'asc' : 'desc' });
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
    first = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) { error = res.error; return; }
    items = res.data.items;
    total = res.data.total;
    next = res.data.next;
    users = res.data.users;
    scroller?.scrollTo({ top: 0 });
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

  function search() {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(reset, 250);
  }

  const timeFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' });

  onMount(() => {
    reset();
    // Load the next page once the end of the list scrolls into view.
    const io = new IntersectionObserver((e) => e[0].isIntersecting && more(), { root: scroller, rootMargin: '300px' });
    io.observe(sentinel);
    return () => io.disconnect();
  });
</script>

<div class="page fill">
  <header class="page-head compact"><h1 class="t-large">Activity</h1></header>
  <div class="card tile">
    <div class="head">
      <div class="title">
        <p class="t-sub">Sign-ins, console sessions, file and app changes on this Box.</p>
      </div>
      <div class="filters">
        <label class="search">
          <Search size={14} />
          <input bind:value={query} oninput={search} placeholder="Search" aria-label="Search the activity" spellcheck="false" />
          {#if query}<button class="clear" onclick={() => { query = ''; reset(); }} aria-label="Clear search"><X size={13} /></button>{/if}
        </label>
        <select class="input sm" bind:value={type} onchange={reset} aria-label="Type">
          {#each TYPES as t (t.value)}<option value={t.value}>{t.label}</option>{/each}
        </select>
        <select class="input sm" bind:value={user} onchange={reset} aria-label="User">
          <option value="">All users</option>
          {#each users as u (u)}<option value={u}>{u}</option>{/each}
        </select>
        <button class="icon-btn sm" onclick={reset} aria-label="Refresh" title="Refresh"><RefreshCw size={16} class={loading ? 'spinner' : ''} /></button>
      </div>
    </div>

    <div class="scroller" bind:this={scroller}>
      <table>
        <thead>
          <tr>
            <th class="c-what">Event</th>
            <th class="c-detail">Details</th>
            <th class="c-user">User</th>
            <th class="c-ip">Address</th>
            <th class="c-time">
              <button class="sort" onclick={() => { asc = !asc; reset(); }} aria-sort={asc ? 'ascending' : 'descending'}>
                Time{#if asc}<ArrowUp size={12} />{:else}<ArrowDown size={12} />{/if}
              </button>
            </th>
          </tr>
        </thead>
        <tbody>
          {#each items as it (it.id)}
            {@const [text, Icon, tone] = label(it.action)}
            <tr>
              <td class="c-what"><span class="what"><span class="ico" class:error={tone === 'error'}><Icon size={14} /></span><span class="truncate">{text}</span></span></td>
              <td class="c-detail"><span class="truncate mono-ish" title={it.detail}>{it.detail}</span></td>
              <td class="c-user">{it.username || "Unknown"}</td>
              <td class="c-ip mono">{it.ip}</td>
              <td class="c-time num" title={timeFmt.format(it.at * 1000)}><span class="ago">{ago(it.at)}</span><span class="abs">{timeFmt.format(it.at * 1000)}</span></td>
            </tr>
          {/each}
        </tbody>
      </table>
      {#if first}
        <div class="pad">{#each Array(10) as _}<div class="skeleton line"></div>{/each}</div>
      {:else if error}
        <div class="pad"><div class="notice"><CircleAlert size={18} /><span>{error}</span></div></div>
      {:else if !items.length}
        <div class="empty"><Activity size={30} /><p class="t-head">{query || type || user ? 'Nothing matches these filters' : 'Nothing happened yet'}</p></div>
      {/if}
      <div bind:this={sentinel} class="sentinel">
        {#if loading && !first && items.length}<Loader size={16} class="spinner" />{/if}
      </div>
    </div>

    <div class="foot t-caption num">
      {#if !first && !error}Showing {items.length} of {total} {total === 1 ? 'entry' : 'entries'}{/if}
    </div>
  </div>
</div>

<style>
  .card { flex: 1; min-height: 0; display: flex; flex-direction: column; padding: 0; overflow: hidden; }
  .head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap; padding: 1rem 1rem 0.875rem 1.25rem; border-bottom: 1px solid var(--hairline); }
  .title h1, .title p { margin: 0; }
  .filters { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
  .search { display: flex; align-items: center; gap: 0.375rem; height: 2.25rem; padding: 0 0.625rem; border-radius: 0.625rem; background: var(--fill); color: var(--text-3); width: 13rem; }
  .search input { flex: 1; min-width: 0; border: 0; background: none; font-size: 0.8125rem; color: var(--text); }
  .search input::placeholder { color: var(--text-3); }
  .search input:focus-visible { outline: none; }
  .search:focus-within { box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 45%, transparent); }
  .clear { display: grid; place-items: center; width: 1.25rem; height: 1.25rem; padding: 0; border: 0; border-radius: 50%; background: var(--fill-2); color: var(--text-2); cursor: pointer; }
  .input.sm { height: 2.25rem; width: auto; min-width: 9rem; font-size: 0.8125rem; border-radius: 0.625rem; }
  .icon-btn.sm { width: 2.25rem; height: 2.25rem; }

  .scroller { flex: 1; min-height: 0; overflow: auto; }
  table { width: 100%; border-collapse: separate; border-spacing: 0; font-size: 0.8125rem; table-layout: fixed; }
  thead th {
    position: sticky; top: 0; z-index: 1; height: 2.25rem; padding: 0 0.75rem; text-align: left; font-size: 0.75rem; font-weight: 600; color: var(--text-2);
    background: color-mix(in srgb, var(--surface) 90%, transparent); -webkit-backdrop-filter: blur(20px); backdrop-filter: blur(20px);
    border-bottom: 1px solid var(--hairline);
  }
  thead th:first-child { padding-left: 1.25rem; }
  td { height: 2.75rem; padding: 0 0.75rem; border-bottom: 1px solid var(--hairline); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  td:first-child { padding-left: 1.25rem; }
  tbody tr:hover td { background: var(--fill); }
  .c-what { width: 30%; }
  .c-detail { width: 30%; color: var(--text-2); }
  .c-user { width: 10%; }
  .c-ip { width: 12%; color: var(--text-2); }
  .c-time { width: 18%; text-align: right; padding-right: 1.25rem !important; }
  thead .c-time { text-align: right; }
  .sort { display: inline-flex; align-items: center; gap: 0.25rem; padding: 0; border: 0; background: none; color: inherit; font: inherit; cursor: pointer; }
  .sort:hover { color: var(--text); }
  .what { display: flex; align-items: center; gap: 0.625rem; min-width: 0; font-weight: 500; }
  .ico { display: grid; place-items: center; width: 1.625rem; height: 1.625rem; border-radius: 0.5rem; flex-shrink: 0; background: var(--accent-fill); color: var(--accent); }
  .ico.error { background: var(--error-fill); color: var(--error); }
  .truncate { display: block; }
  .what .truncate { display: inline; }
  .ago { display: none; }
  .c-time .abs { color: var(--text-2); }

  .pad { padding: 1rem 1.25rem; display: grid; gap: 0.75rem; }
  .skeleton.line { height: 1.25rem; }
  .empty { display: grid; justify-items: center; gap: 0.375rem; padding: 3rem 1rem; color: var(--text-3); text-align: center; }
  .empty p { margin: 0; color: var(--text-2); }
  .sentinel { display: grid; place-items: center; min-height: 2.5rem; color: var(--text-3); }
  .foot { display: flex; align-items: center; min-height: 2.25rem; padding: 0 1.25rem; border-top: 1px solid var(--hairline); }

  @media (max-width: 899px) {
    .c-detail, .c-ip { display: none; }
    .c-what { width: 52%; }
    .c-user { width: 18%; }
    .c-time { width: 30%; }
    .ago { display: inline; }
    .c-time .abs { display: none; }
    .search { width: 100%; flex: 1 1 100%; }
    .filters { width: 100%; }
    .input.sm { flex: 1; min-width: 0; }
  }
</style>
