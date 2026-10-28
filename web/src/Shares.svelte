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
    if (!res.ok) { shareErr = res.error; return; }
    shareOpen = false;
    toast(`The share ${share.name} is saved.`, 'success');
    load();
  }
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
        <span class="ico accent"><MonitorSmartphone size={17} /></span>
        <span class="grow"><span class="t-head">Share folders with Windows, macOS and Linux</span>
          <span class="t-caption">{installing === 'samba' ? installLine || 'Installing Samba' : 'This needs Samba. Box installs it and adds one include line to smb.conf; your other Samba settings stay.'}</span></span>
        <button class="btn btn-primary btn-sm" onclick={() => install('samba')} disabled={!!installing}>{#if installing === 'samba'}<Loader size={15} class="spinner" />Installing{:else}<Download size={15} />Install Samba{/if}</button>
      </div>
    {:else}
      <section class="group">
        <div class="group-head">
          <div><h2 class="t-head">Shared folders</h2><p class="t-caption"><span class="dot" class:ok={st.samba.active}></span> Samba is {st.samba.active ? 'running' : 'not running'}</p></div>
          <button class="btn btn-tinted btn-sm" onclick={() => editShare(null)}><Plus size={15} />New share</button>
        </div>
        {#if st.samba.shares.length}
          {#each st.samba.shares as s (s.id)}
            {@const conn = st.samba.clients[s.name] || []}
            <div class="row item">
              <span class="ico"><FolderOpen size={16} /></span>
              <span class="grow">
                <span class="t-head">{s.name} <span class="pill sm">{s.read_only ? 'Read only' : 'Read and write'}</span>{#if s.guest}<span class="pill sm">Guests</span>{/if}</span>
                <span class="t-caption truncate"><span class="mono">{s.path}</span> · {s.guest ? 'anyone on the network' : s.users.join(', ')}</span>
                <span class="t-caption mono truncate">\\{host}\{s.name}  ·  smb://{host}/{s.name}</span>
              </span>
              <span class="conn t-caption" title={conn.join(', ')}>{conn.length ? `${conn.length} connected` : ''}</span>
              <input type="checkbox" class="switch" checked={s.enabled} onchange={() => flipShare(s)} disabled={busy === 'flip' + s.id} aria-label="Share {s.name} on or off" />
              <button class="icon-btn sm" onclick={() => editShare(s)} aria-label="Edit {s.name}"><Pencil size={15} /></button>
              <button class="icon-btn sm" onclick={() => deleteShare(s)} aria-label="Remove {s.name}"><Trash size={15} /></button>
            </div>
          {/each}
        {:else}
          <div class="empty"><FolderOpen size={28} /><p class="t-head">No shared folders yet</p><p class="t-caption">{st.samba.users.length ? 'Choose New share to share a folder.' : 'Add a share user first, then share a folder.'}</p></div>
        {/if}
      </section>

      <section class="group">
        <div class="group-head">
          <div><h2 class="t-head">Share users</h2><p class="t-caption">Accounts only for shares: no shell, no home folder, no sign-in to the server.</p></div>
          <button class="btn btn-gray btn-sm" onclick={() => editUser('')}><Plus size={15} />Add user</button>
        </div>
        {#if st.samba.users.length}
          {#each st.samba.users as u (u)}
            <div class="row item">
              <span class="ico"><User size={16} /></span>
              <span class="grow"><span class="t-head">{u}</span></span>
              <button class="btn btn-gray btn-sm" onclick={() => editUser(u)}><KeyRound size={14} />New password</button>
              <button class="icon-btn sm" onclick={() => deleteUser(u)} aria-label="Remove {u}"><Trash size={15} /></button>
            </div>
          {/each}
        {:else}
          <div class="empty"><Users size={28} /><p class="t-caption">No share users yet.</p></div>
        {/if}
      </section>
    {/if}
  {:else if tab === 'nfs'}
    {#if !st.nfs.installed}
      <div class="tile banner">
        <span class="ico accent"><Network size={17} /></span>
        <span class="grow"><span class="t-head">Export folders over NFS</span>
          <span class="t-caption">{installing === 'nfs' ? installLine || 'Installing the NFS server' : 'For Linux machines and hypervisors on your network. Box installs nfs-kernel-server and writes only its own exports file.'}</span></span>
        <button class="btn btn-primary btn-sm" onclick={() => install('nfs')} disabled={!!installing}>{#if installing === 'nfs'}<Loader size={15} class="spinner" />Installing{:else}<Download size={15} />Install NFS server{/if}</button>
      </div>
    {:else}
      <section class="group">
        <div class="group-head">
          <div><h2 class="t-head">Exports</h2><p class="t-caption"><span class="dot" class:ok={st.nfs.active}></span> NFS server {st.nfs.active ? 'running' : 'not running'}{#if st.nfs.clients.length} · connected: {st.nfs.clients.join(', ')}{/if}</p></div>
          <button class="btn btn-tinted btn-sm" onclick={() => editExport(null)}><Plus size={15} />New export</button>
        </div>
        {#if st.nfs.exports.length}
          {#each st.nfs.exports as x (x.id)}
            <div class="row item">
              <span class="ico"><Network size={16} /></span>
              <span class="grow">
                <span class="t-head"><span class="mono">{x.path}</span> <span class="pill sm">{x.read_only ? 'Read only' : 'Read and write'}</span></span>
                <span class="t-caption truncate">For {x.clients.join(', ')}{#if x.comment} · {x.comment}{/if}</span>
                <span class="t-caption mono truncate">mount {host}:{x.path} /mnt</span>
              </span>
              <input type="checkbox" class="switch" checked={x.enabled} onchange={() => flipExport(x)} disabled={busy === 'flip' + x.id} aria-label="Export on or off" />
              <button class="icon-btn sm" onclick={() => editExport(x)} aria-label="Edit export"><Pencil size={15} /></button>
              <button class="icon-btn sm" onclick={() => deleteExport(x)} aria-label="Remove export"><Trash size={15} /></button>
            </div>
          {/each}
        {:else}
          <div class="empty"><Network size={28} /><p class="t-head">No exports yet</p><p class="t-caption">Root on a client is always mapped to nobody (root_squash).</p></div>
        {/if}
      </section>
    {/if}
  {:else}
    <section class="group">
      <div class="group-head">
        <div><h2 class="t-head">Download links</h2><p class="t-caption">Create a link in Files: right-click a file or folder and choose Share link.</p></div>
      </div>
      {#if links === null}
        <div class="row"><span class="skeleton line"></span></div>
      {:else if links.length}
        {#each links as l (l.id)}
          <div class="row item">
            <span class="ico"><Link size={16} /></span>
            <span class="grow">
              <span class="t-head truncate">{l.name}{l.dir ? '.zip' : ''} {#if l.password}<Lock size={13} aria-label="Password protected" />{/if}</span>
              <span class="t-caption truncate"><span class="mono">{l.path}</span> · created {ago(l.created)}</span>
            </span>
            <span class="t-caption meta"><span class="num">{l.downloads}{l.max_downloads ? ` of ${l.max_downloads}` : ''}</span> downloads<br />until {until(l.expires)}</span>
            <button class="btn btn-gray btn-sm" onclick={() => revoke(l)}>Revoke</button>
          </div>
        {/each}
      {:else}
        <div class="empty"><Link size={28} /><p class="t-head">No active links</p><p class="t-caption">Expired and revoked links disappear from this list.</p></div>
      {/if}
    </section>
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
  .ico.accent { background: var(--accent-fill); color: var(--accent); }
  .skeleton.line { display: block; width: 55%; height: 1rem; }
  .group-head p { margin: 0.125rem 0 0; display: flex; align-items: center; gap: 0.375rem; }
  .item { min-height: 4rem; }
  .item .t-head { display: flex; align-items: center; gap: 0.4rem; flex-wrap: wrap; }
  .pill.sm { height: 1.375rem; padding: 0 0.55rem; font-size: 0.6875rem; }
  .conn { white-space: nowrap; }
  .meta { text-align: right; white-space: nowrap; }
  .icon-btn.sm { width: 2.25rem; height: 2.25rem; }
  .empty { display: grid; justify-items: center; gap: 0.375rem; padding: 1.75rem 1rem 2rem; text-align: center; color: var(--text-3); }
  .empty p { margin: 0; }
  .empty .t-head { color: var(--text-2); }
  .users { display: flex; flex-wrap: wrap; gap: 0.5rem; }
  .check { display: inline-flex; align-items: center; gap: 0.5rem; min-height: 2.5rem; padding: 0 0.875rem; border-radius: var(--r-pill); background: var(--fill); cursor: pointer; font-weight: 500; }
  .check input { accent-color: var(--accent); width: 1rem; height: 1rem; }
  .area { height: 6rem; padding: 0.625rem 0.875rem; resize: vertical; line-height: 1.5; }
  @media (max-width: 639px) {
    .item { flex-wrap: wrap; }
    .meta { text-align: left; }
  }
</style>
