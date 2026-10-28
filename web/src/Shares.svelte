<script>
  // Network shares: Samba (Windows, macOS, Linux) with Box's own share users, NFS exports for
  // listed client networks, and the public download links created in Files.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { ago } from '$lib/format.js';
  import { runJob } from '$lib/jobs.js';
  import Dialog from '$lib/Dialog.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import DataTable from '$lib/DataTable.svelte';
  import FolderOpen from '@lucide/svelte/icons/folder-open';
  import Users from '@lucide/svelte/icons/users';
  import User from '@lucide/svelte/icons/user';
  import Plus from '@lucide/svelte/icons/plus';
  import Pencil from '@lucide/svelte/icons/pencil';
  import Trash from '@lucide/svelte/icons/trash-2';
  import KeyRound from '@lucide/svelte/icons/key-round';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import Download from '@lucide/svelte/icons/download';
  import Link from '@lucide/svelte/icons/link';
  import Lock from '@lucide/svelte/icons/lock';
  import Network from '@lucide/svelte/icons/network';
  import MonitorSmartphone from '@lucide/svelte/icons/monitor-smartphone';

  let { onSessionLost } = $props();

  const SHARE_COLS = [
    { key: 'name', label: 'Name', primary: true, sortable: true },
    { key: 'path', label: 'Folder', sortable: true },
    { key: 'access', label: 'Access', value: (x) => (x.read_only ? 'Read only' : 'Read and write') },
    { key: 'users', label: 'Who', hide: 'md', value: (x) => x.users.join(', ') },
    { key: 'address', label: 'Address', hide: 'md' },
    { key: 'conn', label: 'Connected', hide: 'md' },
    { key: 'enabled', label: 'On', align: 'right' },
  ];
  const USER_COLS = [{ key: 'name', label: 'User', primary: true, sortable: true }];
  const EXPORT_COLS = [
    { key: 'path', label: 'Folder', primary: true, sortable: true },
    { key: 'access', label: 'Access' },
    { key: 'clients', label: 'Clients', value: (x) => x.clients.join(', ') },
    { key: 'comment', label: 'Note', hide: 'md' },
    { key: 'mount', label: 'Mount', hide: 'md' },
    { key: 'enabled', label: 'On', align: 'right' },
  ];
  const LINK_COLS = [
    { key: 'name', label: 'Name', primary: true, sortable: true },
    { key: 'path', label: 'Path', hide: 'md' },
    { key: 'created', label: 'Created', sortable: true, firstDir: 'desc' },
    { key: 'downloads', label: 'Downloads', align: 'right', sortable: true },
    { key: 'expires', label: 'Until', sortable: true },
  ];
  const TABS = [
    { value: 'samba', label: 'Samba' },
    { value: 'nfs', label: 'NFS' },
    { value: 'links', label: 'Links' },
  ];
  let tab = $state(['samba', 'nfs', 'links'].includes(location.hash.slice(1)) ? location.hash.slice(1) : 'samba');
  $effect(() => { history.replaceState(history.state, '', '#' + tab); });

  let st = $state(null), error = $state('');
  let links = $state(null);
  let roots = $state([]);
  let installing = $state(''), installLine = $state('');
  let busy = $state('');
  const host = location.hostname;

  let shareOpen = $state(false), share = $state(null), shareErr = $state('');
  let userOpen = $state(false), user = $state(null), userErr = $state('');
  let nfsOpen = $state(false), exp = $state(null), nfsErr = $state('');
  let confirmOpen = $state(false), confirmMsg = $state(''), confirmAct = null;

  const lost = (res) => { if (res.status === 401) { onSessionLost(); return true; } return false; };

  async function load() {
    const [s, l] = await Promise.all([api('/api/shares'), api('/api/share-links')]);
    if (lost(s)) return;
    if (!s.ok) { error = s.error; return; }
    error = '';
    st = s.data;
    if (l.ok) links = l.data;
  }

  async function loadRoots() {
    const res = await api('/api/files');
    if (res.ok) roots = res.data.roots || [];
  }

  async function install(what) {
    installing = what;
    const done = await runJob('/api/system/install', { op: what }, (j) => (installLine = j.lines.at(-1) || ''));
    installing = '';
    installLine = '';
    if (done.http === 401) return onSessionLost();
    if (done.status !== 'done') return toast(done.error || 'It could not be installed.', 'error', 9000);
    toast(what === 'samba' ? 'Samba is installed.' : 'The NFS server is installed.', 'success');
    load();
  }

  async function send(path, body, key) {
    busy = key;
    const res = await api(path, { method: 'POST', body });
    busy = '';
    if (lost(res)) return null;
    return res;
  }

  function ask(msg, act) { confirmMsg = msg; confirmAct = act; confirmOpen = true; }

  // ---- samba ----
  function editShare(s) {
    loadRoots();
    share = s ? { ...s, users: [...s.users] } : { id: '', name: '', path: roots[0] || '', read_only: false, guest: false, users: [], enabled: true, comment: '' };
    shareErr = '';
    shareOpen = true;
  }
  function toggleUser(u) { share.users = share.users.includes(u) ? share.users.filter((x) => x !== u) : [...share.users, u]; }
  async function saveShare(e) {
    e?.preventDefault();
    if (share.guest) share.read_only = true;
    const res = await send('/api/shares/samba', { share }, 'share');
    if (!res) return;
    if (!res.ok && res.error.startsWith('needs_handover:')) return offerHandover(res.error.slice(15).trim());
    if (!res.ok) { shareErr = res.error; return; }
    shareOpen = false;
    toast(`The share ${share.name} is saved.`, 'success');
    load();
  }
  // A read-write share needs a folder owned by one of its users: offer to hand it over.
  let hand = $state(null), handOpen = $state(false);
  async function offerHandover(msg) {
    const res = await send('/api/shares/samba/handover', { path: share.path, op: 'check' }, 'share');
    if (!res) return;
    if (!res.ok) { shareErr = `${msg} ${res.error}`; return; }
    hand = { ...res.data, msg, user: share.users[0] || '' };
    handOpen = true;
  }
  async function applyHandover() {
    const res = await send('/api/shares/samba/handover', { path: hand.path, name: hand.user, op: 'apply' }, 'hand');
    if (!res) return;
    if (!res.ok) return toast(res.error, 'error', 9000);
    handOpen = false;
    toast(`${hand.count} ${hand.count === 1 ? 'item belongs' : 'items belong'} to ${hand.user} now.`, 'success');
    saveShare();
  }
  function readOnlyInstead() { handOpen = false; share.read_only = true; saveShare(); }

  async function flipShare(s) {
    const res = await send('/api/shares/samba', { share: { ...s, enabled: !s.enabled } }, 'flip' + s.id);
    if (res && !res.ok) toast(res.error, 'error', 9000);
    load();
  }
  function deleteShare(s) {
    ask(`Remove the share ${s.name}? The files in ${s.path} stay where they are.`, async () => {
      const res = await send(`/api/shares/samba/${s.id}/delete`, {}, 'del');
      if (res && !res.ok) return toast(res.error, 'error', 9000);
      toast('The share is removed.', 'success');
      load();
    });
  }

  function editUser(name) { user = { name: name || '', password: '', existing: !!name }; userErr = ''; userOpen = true; }
  async function saveUser(e) {
    e.preventDefault();
    const res = await send('/api/shares/samba-users', { name: user.name, password: user.password }, 'user');
    if (!res) return;
    if (!res.ok) { userErr = res.error; return; }
    userOpen = false;
    toast(user.existing ? 'The password is changed.' : `${user.name} can now use shares.`, 'success');
    load();
  }
  function deleteUser(name) {
    ask(`Remove the share user ${name}? Box also removes the Linux account it created for it.`, async () => {
      const res = await send(`/api/shares/samba-users/${encodeURIComponent(name)}/delete`, {}, 'del');
      if (res && !res.ok) return toast(res.error, 'error', 9000);
      load();
    });
  }

  // ---- nfs ----
  function editExport(x) {
    loadRoots();
    exp = x ? { ...x, clientsText: x.clients.join('\n') } : { id: '', path: '', clientsText: '', read_only: true, enabled: true, comment: '' };
    nfsErr = '';
    nfsOpen = true;
  }
  async function saveExport(e) {
    e.preventDefault();
    const { clientsText, ...rest } = exp;
    const body = { ...rest, clients: clientsText.split(/[\s,]+/).filter(Boolean) };
    const res = await send('/api/shares/nfs', { export: body }, 'nfs');
    if (!res) return;
    if (!res.ok) { nfsErr = res.error; return; }
    nfsOpen = false;
    toast('The export is saved.', 'success');
    load();
  }
  async function flipExport(x) {
    const res = await send('/api/shares/nfs', { export: { ...x, enabled: !x.enabled } }, 'flip' + x.id);
    if (res && !res.ok) toast(res.error, 'error', 9000);
    load();
  }
  function deleteExport(x) {
    ask(`Stop exporting ${x.path}? Clients that mounted it lose access.`, async () => {
      const res = await send(`/api/shares/nfs/${x.id}/delete`, {}, 'del');
      if (res && !res.ok) return toast(res.error, 'error', 9000);
      load();
    });
  }

  // ---- links ----
  function revoke(l) {
    ask(`Revoke the link to ${l.name}? It stops working right away.`, async () => {
      const res = await send(`/api/share-links/${l.id}/revoke`, {}, 'del');
      if (res && !res.ok) return toast(res.error, 'error', 9000);
      toast('The link is revoked.', 'success');
      load();
    });
  }
  const until = (ts) => new Date(ts * 1000).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });

  onMount(() => { load(); loadRoots(); });
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Shares</h1>
      <p class="t-sub">Share folders on your network and send download links. Box only changes its own share settings.</p>
    </div>
    <Segmented options={TABS} bind:value={tab} label="Share type" />
  </header>

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else if !st}
    <div class="group">{#each Array(4) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</div>
  {:else if tab === 'samba'}
    {#if !st.samba.installed}
      <div class="tile banner">
        <span class="ico md accent"><MonitorSmartphone /></span>
        <span class="grow"><span class="t-head">Share folders with Windows, macOS and Linux</span>
          <span class="t-caption">{installing === 'samba' ? installLine || 'Installing Samba' : 'This needs Samba. Box installs it and adds one include line to smb.conf; your other Samba settings stay.'}</span></span>
        <button class="btn btn-primary btn-sm" onclick={() => install('samba')} disabled={!!installing}>{#if installing === 'samba'}<Loader size={15} class="spinner" />Installing{:else}<Download size={15} />Install Samba{/if}</button>
      </div>
    {:else}
      <DataTable label="Shared folders" title="Shared folders" columns={SHARE_COLS} rows={st.samba.shares} key="id" maxHeight="28rem"
        empty={{ icon: FolderOpen, title: 'No shared folders yet', text: st.samba.users.length ? 'Choose New share to share a folder.' : 'Add a share user first, then share a folder.' }}
        actions={(x) => [
          { label: 'Edit', icon: Pencil, run: () => editShare(x) },
          { label: 'Remove', icon: Trash, danger: true, run: () => deleteShare(x) },
        ]}>
        {#snippet toolbar()}
          <span class="t-caption state"><span class="dot" class:ok={st.samba.active}></span>Samba is {st.samba.active ? 'running' : 'not running'}</span>
          <button class="btn btn-tinted btn-sm" onclick={() => editShare(null)}><Plus size={15} />New share</button>
        {/snippet}
        {#snippet cell(x, c)}
          {#if c.key === 'name'}<span class="who"><span class="ico accent"><FolderOpen /></span><span class="truncate">{x.name}</span></span>
          {:else if c.key === 'path'}<span class="mono">{x.path}</span>
          {:else if c.key === 'access'}{x.read_only ? 'Read only' : 'Read and write'}{#if x.guest}<span class="muted">, guests</span>{/if}
          {:else if c.key === 'users'}<span class="muted">{x.guest ? 'Anyone on the network' : x.users.join(', ')}</span>
          {:else if c.key === 'address'}<span class="mono muted">\\{host}\{x.name}</span>
          {:else if c.key === 'conn'}{@const conn = st.samba.clients[x.name] || []}<span class="muted" title={conn.join(', ')}>{conn.length ? `${conn.length} connected` : ''}</span>
          {:else}<input type="checkbox" class="switch" checked={x.enabled} onchange={() => flipShare(x)} disabled={busy === 'flip' + x.id} aria-label="Share {x.name} on or off" />{/if}
        {/snippet}
      </DataTable>

      <DataTable label="Share users" title="Share users" description="Accounts only for shares: no shell, no home folder, no sign-in to the server." columns={USER_COLS}
        rows={st.samba.users.map((u) => ({ name: u }))} key="name" maxHeight="20rem"
        empty={{ icon: Users, title: 'No share users yet' }}
        actions={(u) => [
          { label: 'New password', icon: KeyRound, run: () => editUser(u.name) },
          { label: 'Remove', icon: Trash, danger: true, run: () => deleteUser(u.name) },
        ]}>
        {#snippet toolbar()}<button class="btn btn-gray btn-sm" onclick={() => editUser('')}><Plus size={15} />Add user</button>{/snippet}
        {#snippet cell(u)}<span class="who"><span class="ico"><User /></span>{u.name}</span>{/snippet}
      </DataTable>
    {/if}
  {:else if tab === 'nfs'}
    {#if !st.nfs.installed}
      <div class="tile banner">
        <span class="ico md accent"><Network /></span>
        <span class="grow"><span class="t-head">Export folders over NFS</span>
          <span class="t-caption">{installing === 'nfs' ? installLine || 'Installing the NFS server' : 'For Linux machines and hypervisors on your network. Box installs nfs-kernel-server and writes only its own exports file.'}</span></span>
        <button class="btn btn-primary btn-sm" onclick={() => install('nfs')} disabled={!!installing}>{#if installing === 'nfs'}<Loader size={15} class="spinner" />Installing{:else}<Download size={15} />Install NFS server{/if}</button>
      </div>
    {:else}
      <DataTable label="NFS exports" title="Exports" columns={EXPORT_COLS} rows={st.nfs.exports} key="id"
        empty={{ icon: Network, title: 'No exports yet', text: 'Root on a client is always mapped to nobody (root_squash).' }}
        actions={(x) => [
          { label: 'Edit', icon: Pencil, run: () => editExport(x) },
          { label: 'Remove', icon: Trash, danger: true, run: () => deleteExport(x) },
        ]}>
        {#snippet toolbar()}
          <span class="t-caption state"><span class="dot" class:ok={st.nfs.active}></span>NFS server {st.nfs.active ? 'running' : 'not running'}{#if st.nfs.clients.length}, connected: {st.nfs.clients.join(', ')}{/if}</span>
          <button class="btn btn-tinted btn-sm" onclick={() => editExport(null)}><Plus size={15} />New export</button>
        {/snippet}
        {#snippet cell(x, c)}
          {#if c.key === 'path'}<span class="who"><span class="ico accent"><Network /></span><span class="mono truncate">{x.path}</span></span>
          {:else if c.key === 'access'}{x.read_only ? 'Read only' : 'Read and write'}
          {:else if c.key === 'clients'}<span class="mono">{x.clients.join(', ')}</span>
          {:else if c.key === 'comment'}<span class="muted">{x.comment || ''}</span>
          {:else if c.key === 'mount'}<span class="mono muted">mount {host}:{x.path} /mnt</span>
          {:else}<input type="checkbox" class="switch" checked={x.enabled} onchange={() => flipExport(x)} disabled={busy === 'flip' + x.id} aria-label="Export on or off" />{/if}
        {/snippet}
      </DataTable>
    {/if}
  {:else}
    <DataTable label="Download links" title="Download links" description="Create a link in Files: right-click a file or folder and choose Share link." columns={LINK_COLS}
      rows={links} key="id" empty={{ icon: Link, title: 'No active links', text: 'Expired and revoked links disappear from this list.' }}
      actions={(l) => [{ label: 'Revoke', icon: Trash, danger: true, run: () => revoke(l) }]}>
      {#snippet cell(l, c)}
        {#if c.key === 'name'}<span class="who"><span class="ico accent"><Link /></span><span class="truncate">{l.name}{l.dir ? '.zip' : ''}</span>{#if l.password}<Lock size={13} aria-label="Password protected" />{/if}</span>
        {:else if c.key === 'path'}<span class="mono muted">{l.path}</span>
        {:else if c.key === 'created'}<span class="muted">{ago(l.created)}</span>
        {:else if c.key === 'downloads'}<span class="num">{l.downloads}{l.max_downloads ? ` of ${l.max_downloads}` : ''}</span>
        {:else}{until(l.expires)}{/if}
      {/snippet}
    </DataTable>
  {/if}
</div>

<datalist id="share-roots">{#each roots as r}<option value={r}></option>{/each}</datalist>

<Dialog bind:open={shareOpen} label="Share">
  {#if share}
    <form class="dlg" onsubmit={saveShare}>
      <h2 class="t-title">{share.id ? `Edit ${share.name}` : 'New share'}</h2>
      {#if shareErr}<div class="notice" role="alert">{shareErr}</div>{/if}
      <label class="field"><span class="field-label">Share name</span><input class="input" bind:value={share.name} placeholder="media" required maxlength="32" spellcheck="false" /><span class="t-caption">What people see on the network, like \\{host}\media.</span></label>
      <label class="field"><span class="field-label">Folder</span><input class="input mono" bind:value={share.path} list="share-roots" placeholder="/srv/media" required spellcheck="false" /><span class="t-caption">A folder inside the folders Files may show. Files are read and written as the folder's owner.</span></label>
      <label class="field"><span class="field-label">Description</span><input class="input" bind:value={share.comment} maxlength="60" placeholder="Optional" /></label>
      <label class="row-field"><span class="field-text"><span class="t-head">Read only</span><span class="t-caption">People can open files but not change them.</span></span><input type="checkbox" class="switch" bind:checked={share.read_only} disabled={share.guest} /></label>
      <label class="row-field"><span class="field-text"><span class="t-head">Guest access</span><span class="t-caption">Anyone on the network can read it without a password. Only for read-only shares.</span></span><input type="checkbox" class="switch" bind:checked={share.guest} /></label>
      {#if !share.guest}
        <div class="field">
          <span class="field-label">Who may use it</span>
          {#if st.samba.users.length}
            <div class="users">{#each st.samba.users as u}<label class="check"><input type="checkbox" checked={share.users.includes(u)} onchange={() => toggleUser(u)} />{u}</label>{/each}</div>
          {:else}
            <p class="t-caption">There are no share users yet. Close this and choose Add user first.</p>
          {/if}
        </div>
      {/if}
      <label class="row-field"><span class="field-text"><span class="t-head">Shared</span><span class="t-caption">Turn it off to hide it without losing the settings.</span></span><input type="checkbox" class="switch" bind:checked={share.enabled} /></label>
      <div class="buttons">
        <button type="button" class="btn btn-gray" data-close onclick={() => (shareOpen = false)}>Cancel</button>
        <button class="btn btn-primary" disabled={busy === 'share'}>{#if busy === 'share'}<Loader size={16} class="spinner" />Saving{:else}Save{/if}</button>
      </div>
    </form>
  {/if}
</Dialog>

<Dialog bind:open={handOpen} label="Hand the folder over">
  {#if hand}
    <div class="dlg">
      <h2 class="t-title">Hand this folder over?</h2>
      <p class="t-sub">{hand.path} belongs to {hand.owner}. Over Samba, files are written as the folder's owner, so a read-write share needs a folder that belongs to one of its users.</p>
      <label class="field"><span class="field-label">New owner</span>
        <select class="input" bind:value={hand.user}>{#each share.users as u}<option value={u}>{u}</option>{/each}</select>
      </label>
      <p class="t-caption">{hand.count.toLocaleString()} {hand.count === 1 ? 'item changes' : 'files and folders change'} owner to {hand.user} (and its group). Links are changed themselves, never what they point to.</p>
      <div class="buttons">
        <button class="btn btn-gray" data-close onclick={() => (handOpen = false)}>Cancel</button>
        <button class="btn btn-gray" onclick={readOnlyInstead}>Share read-only instead</button>
        <button class="btn btn-primary" onclick={applyHandover} disabled={busy === 'hand' || !hand.user}>{#if busy === 'hand'}<Loader size={16} class="spinner" />Handing over{:else}Hand over to {hand.user}{/if}</button>
      </div>
    </div>
  {/if}
</Dialog>

<Dialog bind:open={userOpen} label="Share user">
  {#if user}
    <form class="dlg" onsubmit={saveUser}>
      <h2 class="t-title">{user.existing ? `New password for ${user.name}` : 'Add share user'}</h2>
      {#if userErr}<div class="notice" role="alert">{userErr}</div>{/if}
      {#if !user.existing}<label class="field"><span class="field-label">User name</span><input class="input" bind:value={user.name} placeholder="anna" required maxlength="32" autocomplete="off" spellcheck="false" /></label>{/if}
      <label class="field"><span class="field-label">Password</span><input class="input" type="password" bind:value={user.password} required minlength="8" autocomplete="new-password" /><span class="t-caption">At least 8 characters. Used only to connect to shares.</span></label>
      <div class="buttons">
        <button type="button" class="btn btn-gray" data-close onclick={() => (userOpen = false)}>Cancel</button>
        <button class="btn btn-primary" disabled={busy === 'user'}>{#if busy === 'user'}<Loader size={16} class="spinner" />Saving{:else}Save{/if}</button>
      </div>
    </form>
  {/if}
</Dialog>

<Dialog bind:open={nfsOpen} label="NFS export">
  {#if exp}
    <form class="dlg" onsubmit={saveExport}>
      <h2 class="t-title">{exp.id ? 'Edit export' : 'New export'}</h2>
      {#if nfsErr}<div class="notice" role="alert">{nfsErr}</div>{/if}
      <label class="field"><span class="field-label">Folder</span><input class="input mono" bind:value={exp.path} list="share-roots" placeholder="/srv/data" required spellcheck="false" /></label>
      <label class="field"><span class="field-label">Allowed clients</span><textarea class="input area mono" bind:value={exp.clientsText} placeholder={'192.168.1.0/24\n10.0.0.5'} required spellcheck="false"></textarea><span class="t-caption">IP addresses or networks, one per line. Only these machines can mount it.</span></label>
      <label class="field"><span class="field-label">Description</span><input class="input" bind:value={exp.comment} maxlength="60" placeholder="Optional" /></label>
      <label class="row-field"><span class="field-text"><span class="t-head">Read only</span><span class="t-caption">Clients can read but not change files.</span></span><input type="checkbox" class="switch" bind:checked={exp.read_only} /></label>
      <label class="row-field"><span class="field-text"><span class="t-head">Exported</span><span class="t-caption">Turn it off to stop it without losing the settings.</span></span><input type="checkbox" class="switch" bind:checked={exp.enabled} /></label>
      <p class="t-caption">Root on a client always acts as nobody here (root_squash). Box never offers no_root_squash.</p>
      <div class="buttons">
        <button type="button" class="btn btn-gray" data-close onclick={() => (nfsOpen = false)}>Cancel</button>
        <button class="btn btn-primary" disabled={busy === 'nfs'}>{#if busy === 'nfs'}<Loader size={16} class="spinner" />Saving{:else}Save{/if}</button>
      </div>
    </form>
  {/if}
</Dialog>

<Dialog bind:open={confirmOpen} label="Confirm">
  <div class="dlg">
    <h2 class="t-title">Are you sure?</h2>
    <p class="t-sub">{confirmMsg}</p>
    <div class="buttons">
      <button class="btn btn-gray" data-close onclick={() => (confirmOpen = false)}>Cancel</button>
      <button class="btn btn-danger" disabled={busy === 'del'} onclick={async () => { await confirmAct(); confirmOpen = false; }}>{#if busy === 'del'}<Loader size={16} class="spinner" />{/if}Yes, go ahead</button>
    </div>
  </div>
</Dialog>

<style>
  .banner { display: flex; align-items: center; gap: 0.875rem; padding: 1rem 1.125rem; }
  .banner .grow { flex: 1; min-width: 0; display: grid; gap: 0.125rem; }
  .skeleton.line { display: block; width: 55%; height: 1rem; }
  .state { display: inline-flex; align-items: center; gap: 0.375rem; margin-right: 0.25rem; }
  .who { display: inline-flex; align-items: center; gap: 0.625rem; min-width: 0; max-width: 100%; }
  .muted { color: var(--text-2); }
  .users { display: flex; flex-wrap: wrap; gap: 0.5rem; }
  .check { display: inline-flex; align-items: center; gap: 0.5rem; min-height: 2.5rem; padding: 0 0.875rem; border-radius: var(--r-pill); background: var(--fill); cursor: pointer; font-weight: 500; }
  .check input { accent-color: var(--accent); width: 1rem; height: 1rem; }
  .area { height: 6rem; padding: 0.625rem 0.875rem; resize: vertical; line-height: 1.5; }
</style>
