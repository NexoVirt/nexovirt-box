<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes, ago } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import Folder from '@lucide/svelte/icons/folder';
  import FileIcon from '@lucide/svelte/icons/file';
  import FileText from '@lucide/svelte/icons/file-text';
  import FileImage from '@lucide/svelte/icons/file-image';
  import HardDrive from '@lucide/svelte/icons/hard-drive';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import ArrowUp from '@lucide/svelte/icons/arrow-up';
  import FolderPlus from '@lucide/svelte/icons/folder-plus';
  import Upload from '@lucide/svelte/icons/upload';
  import Download from '@lucide/svelte/icons/download';
  import Pencil from '@lucide/svelte/icons/pencil';
  import Trash from '@lucide/svelte/icons/trash-2';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';

  let { onSessionLost } = $props();

  const IMAGES = /\.(png|jpe?g|gif|webp)$/i;
  const TEXT = /\.(txt|log|md|json|ya?ml|conf|env|sh|ini|toml|csv)$/i;
  const CHUNK = 8 * 1024 * 1024;

  let path = $state(''); // '' = the list of top folders
  let roots = $state([]);
  let entries = $state(null);
  let error = $state('');
  let origin = $state(null);
  let dragging = $state(false);
  let uploads = $state([]); // { name, done, total }

  let nameOpen = $state(false), nameMode = $state('mkdir'), nameValue = $state(''), nameFor = $state(null), nameBusy = $state(false), nameError = $state('');
  let delOpen = $state(false), delFor = $state(null), delBusy = $state(false);
  let viewOpen = $state(false), viewFor = $state(null), viewText = $state('');
  let replaceOpen = $state(false), replaceList = $state([]);
  let fileInput = $state();

  const join = (dir, name) => (dir.endsWith('/') ? dir : dir + '/') + name;
  const parent = (p) => p.slice(0, p.lastIndexOf('/')) || '/';
  const fileURL = (p, inline) => `/api/files/download?path=${encodeURIComponent(p)}${inline ? '&inline=1' : ''}`;

  async function open(p) {
    error = '';
    entries = null;
    const res = await api(`/api/files?path=${encodeURIComponent(p)}`);
    if (res.status === 401) return onSessionLost();
    if (!res.ok) {
      error = res.error;
      entries = [];
      return;
    }
    path = p;
    if (p === '') roots = res.data.roots;
    else entries = res.data.entries;
  }

  // The root a path belongs to, and the breadcrumb below it.
  const root = $derived(roots.filter((r) => path === r || path.startsWith(r.endsWith('/') ? r : r + '/')).sort((a, b) => b.length - a.length)[0] || '');
  const crumbs = $derived.by(() => {
    if (!root) return [];
    const rest = path.slice(root.length).split('/').filter(Boolean);
    const out = [{ label: root, path: root }];
    let cur = root;
    for (const part of rest) {
      cur = join(cur, part);
      out.push({ label: part, path: cur });
    }
    return out;
  });

  function up() {
    open(path === root ? '' : parent(path));
  }

  function iconFor(e) {
    if (e.dir) return Folder;
    if (IMAGES.test(e.name)) return FileImage;
    if (TEXT.test(e.name)) return FileText;
    return FileIcon;
  }

  async function activate(e) {
    const p = join(path, e.name);
    if (e.dir) return open(p);
    if (IMAGES.test(e.name) || TEXT.test(e.name)) {
      viewFor = { ...e, path: p };
      viewText = '';
      viewOpen = true;
      if (TEXT.test(e.name)) {
        if (e.size > 2 * 1024 * 1024) viewText = 'This file is too large to show here. Download it instead.';
        else {
          const r = await fetch(fileURL(p, true), { credentials: 'same-origin' }).catch(() => null);
          viewText = r && r.ok ? await r.text() : 'The file could not be read.';
        }
      }
      return;
    }
    location.href = fileURL(p, false);
  }

  function askName(ev, mode, entry) {
    origin = { x: ev.clientX, y: ev.clientY };
    nameMode = mode;
    nameFor = entry;
    nameValue = mode === 'mkdir' ? '' : join(path, entry.name);
    nameError = '';
    nameOpen = true;
  }

  async function submitName(e) {
    e.preventDefault();
    nameBusy = true;
    nameError = '';
    const res = nameMode === 'mkdir'
      ? await api('/api/files/mkdir', { method: 'POST', body: { path: join(path, nameValue.trim()) } })
      : await api('/api/files/rename', { method: 'POST', body: { path: join(path, nameFor.name), to: nameValue.trim() } });
    nameBusy = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) {
      nameError = res.error;
      return;
    }
    nameOpen = false;
    toast(nameMode === 'mkdir' ? 'Folder created.' : 'Moved.', 'success');
    open(path);
  }

  function askDelete(ev, entry) {
    origin = { x: ev.clientX, y: ev.clientY };
    delFor = entry;
    delOpen = true;
  }

  async function doDelete() {
    delBusy = true;
    const res = await api('/api/files/delete', { method: 'POST', body: { path: join(path, delFor.name) } });
    delBusy = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) return toast(res.error, 'error', 8000);
    delOpen = false;
    toast(`${delFor.name} was deleted.`, 'success');
    open(path);
  }

  function pickFiles(list) {
    const files = [...list];
    if (!files.length || !root) return;
    const taken = files.filter((f) => entries?.some((e) => e.name === f.name));
    if (taken.length) {
      replaceList = files;
      replaceOpen = true;
      return;
    }
    uploadAll(files, false);
  }

  async function uploadAll(files, overwrite) {
    replaceOpen = false;
    const dir = path;
    for (const f of files) {
      const u = { name: f.name, done: 0, total: f.size };
      uploads.push(u);
      const item = uploads[uploads.length - 1];
      const id = [...crypto.getRandomValues(new Uint8Array(8))].map((b) => b.toString(16).padStart(2, '0')).join('');
      const target = join(dir, f.name);
      let ok = true;
      for (let off = 0; off < f.size || off === 0; off += CHUNK) {
        const last = off + CHUNK >= f.size;
        const q = `path=${encodeURIComponent(target)}&id=${id}&offset=${off}${last ? '&final=1' : ''}${overwrite ? '&overwrite=1' : ''}`;
        const res = await fetch('/api/files/upload?' + q, { method: 'POST', credentials: 'same-origin', body: f.slice(off, off + CHUNK) }).catch(() => null);
        if (!res || !res.ok) {
          const body = res ? await res.json().catch(() => ({})) : {};
          if (res?.status === 401) return onSessionLost();
          toast(`${f.name}: ${body.error || 'The upload failed.'}`, 'error', 8000);
          fetch(`/api/files/upload?path=${encodeURIComponent(target)}&id=${id}&offset=0&abort=1`, { method: 'POST', credentials: 'same-origin' });
          ok = false;
          break;
        }
        item.done = Math.min(f.size, off + CHUNK);
        if (last) break;
      }
      uploads.splice(uploads.indexOf(item), 1);
      if (ok) toast(`${f.name} uploaded.`, 'success');
    }
    if (path === dir) open(dir);
  }

  function drop(e) {
    e.preventDefault();
    dragging = false;
    if (path) pickFiles(e.dataTransfer.files);
  }

  onMount(() => open(''));
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Files</h1>
      <p class="t-sub">Browse, upload and manage files in the folders Box may show.</p>
    </div>
    {#if path}
      <div class="head-btns">
        <button class="btn btn-gray btn-sm" onclick={(e) => askName(e, 'mkdir')}><FolderPlus size={15} />New folder</button>
        <button class="btn btn-tinted btn-sm" onclick={() => fileInput.click()}><Upload size={15} />Upload</button>
        <input type="file" multiple hidden bind:this={fileInput} onchange={(e) => { pickFiles(e.currentTarget.files); e.currentTarget.value = ''; }} />
      </div>
    {/if}
  </header>

  {#if path}
    <nav class="crumbs" aria-label="Folder">
      <button class="icon-btn" onclick={up} aria-label="Up one folder"><ArrowUp size={17} /></button>
      <button class="crumb" onclick={() => open('')}>All folders</button>
      {#each crumbs as c (c.path)}
        <ChevronRight size={14} class="sep" />
        <button class="crumb" class:current={c.path === path} onclick={() => open(c.path)}>{c.label}</button>
      {/each}
      <button class="icon-btn refresh" onclick={() => open(path)} aria-label="Reload"><RefreshCw size={16} /></button>
    </nav>
  {/if}

  {#each uploads as u (u)}
    <div class="tile upload">
      <Loader size={16} class="spinner" />
      <span class="truncate">{u.name}</span>
      <div class="bar grow-bar"><span style:width="{u.total ? (u.done / u.total) * 100 : 100}%"></span></div>
      <span class="t-caption num">{bytes(u.done)} of {bytes(u.total)}</span>
    </div>
  {/each}

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {/if}

  {#if !path}
    <div class="group">
      {#each roots as r (r)}
        <button class="row link-row" onclick={() => open(r)}>
          <span class="ico"><HardDrive size={16} /></span>
          <span class="grow truncate">{r}</span>
          <ChevronRight size={16} />
        </button>
      {:else}
        <div class="row t-sub">Loading folders</div>
      {/each}
    </div>
    <p class="t-caption">Change these folders in Settings. System folders are never shown.</p>
  {:else if !entries}
    <div class="group">{#each Array(6) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</div>
  {:else}
    <div class="group list" class:dragging role="region" aria-label="Folder contents, drop files here to upload"
      ondragover={(e) => { e.preventDefault(); dragging = true; }} ondragleave={() => (dragging = false)} ondrop={drop}>
      {#each entries as e (e.name)}
        {@const Icon = iconFor(e)}
        <div class="row entry">
          <button class="name" onclick={() => activate(e)}>
            <span class="ico" class:folder={e.dir}><Icon size={16} /></span>
            <span class="grow"><span class="truncate">{e.name}</span><span class="t-caption">{e.dir ? 'Folder' : bytes(e.size)}{e.link ? ', link' : ''}  ·  {ago(e.mtime)}</span></span>
          </button>
          <div class="entry-btns">
            {#if !e.dir}<a class="icon-btn" href={fileURL(join(path, e.name), false)} aria-label="Download {e.name}"><Download size={16} /></a>{/if}
            <button class="icon-btn" onclick={(ev) => askName(ev, 'move', e)} aria-label="Rename or move {e.name}"><Pencil size={16} /></button>
            <button class="icon-btn" onclick={(ev) => askDelete(ev, e)} aria-label="Delete {e.name}"><Trash size={16} /></button>
          </div>
        </div>
      {:else}
        <div class="row t-sub">This folder is empty. Drop files here to upload them.</div>
      {/each}
    </div>
  {/if}
</div>

<Dialog bind:open={nameOpen} label={nameMode === 'mkdir' ? 'New folder' : 'Rename or move'} {origin} locked={nameBusy}>
  <form class="dlg" onsubmit={submitName}>
    <h2 class="t-title">{nameMode === 'mkdir' ? 'New folder' : `Rename or move ${nameFor?.name}`}</h2>
    {#if nameMode !== 'mkdir'}<p class="t-sub">Change the name, or the whole path to move it.</p>{/if}
    {#if nameError}<div class="notice" role="alert">{nameError}</div>{/if}
    <input class="input" bind:value={nameValue} placeholder={nameMode === 'mkdir' ? 'Folder name' : ''} aria-label="Name" spellcheck="false" required />
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => (nameOpen = false)} disabled={nameBusy}>Cancel</button>
      <button class="btn btn-primary" disabled={nameBusy || !nameValue.trim()}>{#if nameBusy}<Loader size={16} class="spinner" />{/if}{nameMode === 'mkdir' ? 'Create' : 'Save'}</button>
    </div>
  </form>
</Dialog>

<Dialog bind:open={delOpen} label="Delete" {origin} locked={delBusy}>
  <div class="dlg">
    <h2 class="t-title">Delete {delFor?.name}?</h2>
    <p class="t-sub">{delFor?.dir ? 'The folder and everything in it are deleted.' : 'The file is deleted.'} This cannot be undone.</p>
    <div class="buttons">
      <button class="btn btn-gray" data-close onclick={() => (delOpen = false)} disabled={delBusy}>Cancel</button>
      <button class="btn btn-danger" onclick={doDelete} disabled={delBusy}>{#if delBusy}<Loader size={16} class="spinner" />{/if}Delete</button>
    </div>
  </div>
</Dialog>

<Dialog bind:open={replaceOpen} label="Replace files">
  <div class="dlg">
    <h2 class="t-title">Replace existing files?</h2>
    <p class="t-sub">Some files with the same name are already in this folder.</p>
    <div class="buttons">
      <button class="btn btn-gray" data-close onclick={() => (replaceOpen = false)}>Cancel</button>
      <button class="btn btn-primary" onclick={() => uploadAll(replaceList, true)}>Replace</button>
    </div>
  </div>
</Dialog>

<Dialog bind:open={viewOpen} label="Preview" wide>
  <div class="dlg">
    <div class="dlg-head">
      <h2 class="t-title truncate">{viewFor?.name}</h2>
      <a class="btn btn-gray btn-sm" href={viewFor ? fileURL(viewFor.path, false) : '#'}><Download size={15} />Download</a>
    </div>
    {#if viewFor && IMAGES.test(viewFor.name)}
      <img class="preview" src={fileURL(viewFor.path, true)} alt={viewFor.name} />
    {:else}
      <pre class="mono text">{viewText || 'Loading'}</pre>
    {/if}
    <div class="buttons"><button class="btn btn-gray" data-close onclick={() => (viewOpen = false)}>Close</button></div>
  </div>
</Dialog>

<style>
  .head-btns { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .crumbs { display: flex; align-items: center; gap: 0.25rem; flex-wrap: wrap; margin-top: -0.5rem; }
  .crumbs :global(.sep) { color: var(--text-3); }
  .crumb { border: 0; background: none; color: var(--accent); cursor: pointer; min-height: 2.25rem; padding: 0 0.375rem; border-radius: 0.5rem; font: inherit; }
  .crumb:hover { background: var(--fill); }
  .crumb.current { color: var(--text); font-weight: 600; }
  .refresh { margin-left: auto; }
  .upload { display: flex; align-items: center; gap: 0.75rem; padding: 0.75rem 1rem; }
  .grow-bar { flex: 1; }
  .link-row, .name { border: 0; background: none; width: 100%; text-align: left; font: inherit; color: inherit; cursor: pointer; }
  .link-row:hover { background: var(--fill); }
  .entry { padding-block: 0.375rem; }
  .name { display: flex; align-items: center; gap: 0.875rem; flex: 1; min-width: 0; min-height: 2.75rem; border-radius: 0.625rem; }
  .ico.folder { background: var(--accent-fill); color: var(--accent); }
  .entry-btns { display: flex; gap: 0.125rem; opacity: 0.8; }
  .entry:hover .entry-btns { opacity: 1; }
  .list.dragging { box-shadow: 0 0 0 2px var(--accent), var(--shadow-tile); }
  .preview { max-width: 100%; max-height: 65svh; justify-self: center; border-radius: var(--r-control); }
  .text { margin: 0; max-height: 60svh; overflow: auto; padding: 0.875rem 1rem; border-radius: var(--r-control); background: var(--term-bg); font-size: 0.75rem; line-height: 1.5; white-space: pre-wrap; word-break: break-word; }
</style>
