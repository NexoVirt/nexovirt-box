<script>
  // Trash of the file explorer: deleted items of every place, restore or delete for good,
  // empty it, and how many days items are kept.
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes, ago } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import DataTable from '$lib/DataTable.svelte';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import Undo2 from '@lucide/svelte/icons/undo-2';
  import FileIcon from '@lucide/svelte/icons/file';
  import Folder from '@lucide/svelte/icons/folder';

  let { open = $bindable(false), onChanged, onSessionLost } = $props();
  let items = $state(null), days = $state(30), error = $state(''), busy = $state(false), confirmEmpty = $state(false);

  $effect(() => { if (open) { confirmEmpty = false; load(); } });

  async function load() {
    const res = await api('/api/files/trash');
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { error = res.error; items = []; return; }
    error = ''; items = res.data.items; days = res.data.days;
  }

  async function act(kind, it) {
    const res = await api(`/api/files/trash/${kind}`, { method: 'POST', body: { root: it.root, id: it.id } });
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) return toast(res.error, 'error', 8000);
    toast(kind === 'restore' ? `${it.name} is back in ${res.data.path.slice(0, res.data.path.lastIndexOf('/')) || '/'}.` : `${it.name} was deleted for good.`, 'success');
    onChanged?.();
    load();
  }

  async function empty() {
    busy = true;
    const res = await api('/api/files/trash/empty', { method: 'POST', body: {} });
    busy = false; confirmEmpty = false;
    if (!res.ok) return toast(res.error, 'error');
    toast('The trash is empty.', 'success');
    load();
  }

  async function saveDays() {
    const res = await api('/api/files/trash/settings', { method: 'POST', body: { days: Number(days) } });
    if (!res.ok) return toast(res.error, 'error');
    toast(`Deleted items are kept for ${res.data.days} days.`, 'success');
  }

  const COLS = [
    { key: 'name', label: 'Name', primary: true, sortable: true, width: '30%' },
    { key: 'path', label: 'Was in', hide: 'md', width: '34%' },
    { key: 'size', label: 'Size', align: 'right', sortable: true, width: '12%' },
    { key: 'deleted', label: 'Deleted', align: 'right', sortable: true, firstDir: 'desc', width: '16%' },
  ];
</script>

<Dialog bind:open label="Trash" wide locked={busy}>
  <div class="dlg">
    <h2 class="t-title">Trash</h2>
    <p class="t-sub">Deleted files and folders stay here and are deleted for good after the number of days below.</p>
    <DataTable label="Trash" columns={COLS} rows={items} key="id" {error} onretry={load} maxHeight="22rem"
      actions={(it) => [{ label: 'Restore', icon: Undo2, run: () => act('restore', it) }, { label: 'Delete for good', icon: Trash2, danger: true, run: () => act('delete', it) }]}
      empty={{ icon: Trash2, title: 'The trash is empty' }}>
      {#snippet cell(it, c)}
        {#if c.key === 'name'}<span class="nm"><span class="ico accent">{#if it.dir}<Folder />{:else}<FileIcon />{/if}</span><span class="truncate">{it.name}</span></span>
        {:else if c.key === 'path'}<span class="muted truncate" title={it.path}>{it.path.slice(0, it.path.lastIndexOf('/')) || '/'}</span>
        {:else if c.key === 'size'}<span class="num">{bytes(it.size)}</span>
        {:else}<span class="num muted">{ago(it.deleted)}</span>{/if}
      {/snippet}
    </DataTable>
    <div class="foot">
      <label class="keep"><span class="t-caption">Keep for</span><input class="input days" type="number" min="1" max="365" bind:value={days} onchange={saveDays} aria-label="Days to keep" /><span class="t-caption">days</span></label>
      {#if confirmEmpty}
        <span class="t-caption">Delete everything in the trash for good?</span>
        <button class="btn btn-gray btn-sm" onclick={() => (confirmEmpty = false)}>No</button>
        <button class="btn btn-danger btn-sm" onclick={empty} disabled={busy}>Empty trash</button>
      {:else}
        <button class="btn btn-gray btn-sm" onclick={() => (confirmEmpty = true)} disabled={!items?.length}>Empty trash</button>
      {/if}
      <button class="btn btn-primary btn-sm" data-close onclick={() => (open = false)}>Done</button>
    </div>
  </div>
</Dialog>

<style>
  h2, p { margin: 0; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .nm { display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
  .muted { color: var(--text-2); }
  .foot { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 0.5rem; }
  .keep { display: inline-flex; align-items: center; gap: 0.375rem; margin-right: auto; }
  .days { width: 5rem; }
</style>
