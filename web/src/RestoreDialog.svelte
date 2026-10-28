<script>
  // Restore: pick a target, a backup on it, and restore it over the same app or as a new app.
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { withProof } from '$lib/proof.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Trash2 from '@lucide/svelte/icons/trash-2';

  let { open = $bindable(false), preset = null, targets = [], apps = [], origin = null, app: onlyApp = '', onSessionLost, onDone } = $props();

  let target = $state(''), list = $state(null), listError = $state(''), name = $state('');
  let mode = $state('same'), newId = $state(''), socket = $state(false), busy = $state(false), lines = $state([]);

  $effect(() => {
    if (open) {
      target = preset?.target || targets[0]?.id || '';
      name = preset?.name || '';
      mode = 'same';
      newId = '';
      lines = [];
      loadList();
    }
  });

  async function loadList() {
    if (!target) return;
    list = null;
    listError = '';
    const app = onlyApp || preset?.app || '';
    const res = await api(`/api/backups/targets/${target}/archives${app ? '?app=' + app : ''}`);
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { listError = res.error; return; }
    list = res.data;
    if (!list.some((a) => a.name === name)) name = list[0]?.name || '';
  }

  let picked = $derived(list?.find((a) => a.name === name));
  let installed = $derived(picked && apps.some((a) => a.id === picked.app));
  const label = (id) => (id === 'box' ? 'Box settings' : apps.find((a) => a.id === id)?.name || id);

  async function restore() {
    busy = true;
    lines = [];
    const body = { target, name, new_id: mode === 'new' ? newId.trim() : '', allow_docker_socket: socket };
    const job = await withProof(() => runJob('/api/backups/restore', body, (j) => (lines = j.lines)));
    busy = false;
    if (job.cancelled) return;
    if (job.http === 401) return onSessionLost?.();
    if (job.status === 'done') {
      open = false;
      toast(picked.app === 'box' ? 'Box settings were restored. Box restarts now; sign in again in a moment.' : `${label(job.result?.app || picked.app)} was restored.`, 'success', 9000);
    } else toast(job.error, 'error', 10000);
    onDone?.();
  }

  async function remove() {
    const res = await withProof(() => api('/api/backups/delete', { method: 'POST', body: { target, name } }));
    if (res.cancelled) return;
    if (!res.ok) return toast(res.error, 'error', 7000);
    toast('The backup was deleted.', 'success');
    loadList();
  }
</script>

<Dialog bind:open label="Restore" {origin} locked={busy} wide>
  <div class="dlg">
    <h2 class="t-title">Restore a backup</h2>
    <label class="field"><span class="field-label">From</span>
      <select class="input" bind:value={target} onchange={loadList} disabled={busy}>{#each targets as t (t.id)}<option value={t.id}>{t.name}</option>{/each}</select>
    </label>
    {#if listError}
      <div class="notice" role="alert">{listError}</div>
    {:else if !list}
      <p class="t-sub busy-line"><Loader size={16} class="spinner" />Reading the backups on this target</p>
    {:else if !list.length}
      <div class="notice neutral">There are no backups on this target yet.</div>
    {:else}
      <div class="list" role="listbox" aria-label="Backups">
        {#each list as a (a.name)}
          <button class="item" class:on={a.name === name} role="option" aria-selected={a.name === name} onclick={() => (name = a.name)} disabled={busy}>
            <span class="grow"><span>{label(a.app)}</span><span class="t-caption mono">{a.name}</span></span>
            <span class="t-caption">{new Date(a.at * 1000).toLocaleString()}</span>
            <span class="t-caption size">{bytes(a.size)}</span>
          </button>
        {/each}
      </div>
      {#if picked && picked.app !== 'box'}
        <Segmented options={[{ value: 'same', label: installed ? `Over ${label(picked.app)}` : 'As it was' }, { value: 'new', label: 'As a new app' }]} bind:value={mode} label="Restore to" />
        {#if mode === 'new'}
          <label class="field"><span class="field-label">New app id</span><input class="input mono" bind:value={newId} placeholder="{picked.app}-copy" spellcheck="false" disabled={busy} /></label>
          <p class="t-caption">The copy gets its own folder. It uses the same ports, so stop the original first or change the ports afterwards.</p>
        {:else if installed}
          <p class="t-caption">{label(picked.app)} stops, its data and settings are replaced with the backup, then it starts again. If anything fails, the current files come back.</p>
        {:else}
          <p class="t-caption">The app is set up again from the backup, for example on a new server.</p>
        {/if}
        {#if !installed || mode === 'new'}
          <label class="row-field"><span class="field-text"><span class="field-label">The app may use Docker</span><span class="t-caption">Only for apps that manage containers. This gives the app full control of the server.</span></span><input class="switch" type="checkbox" bind:checked={socket} disabled={busy} /></label>
        {/if}
      {:else if picked}
        <p class="t-caption">Box's settings, accounts and schedules are replaced. Box restarts and everyone signs in again.</p>
      {/if}
    {/if}
    {#if busy}<JobLog {lines} />{/if}
    <div class="buttons">
      {#if picked}<button class="btn btn-danger del" onclick={remove} disabled={busy}><Trash2 size={15} />Delete</button>{/if}
      <button class="btn btn-gray" data-close onclick={() => (open = false)} disabled={busy}>Cancel</button>
      <button class="btn btn-primary" onclick={restore} disabled={busy || !picked || (mode === 'new' && !newId.trim())}>
        {#if busy}<Loader size={16} class="spinner" />Restoring{:else}Restore{/if}
      </button>
    </div>
  </div>
</Dialog>

<style>
  h2, p { margin: 0; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .field { display: grid; gap: 0.375rem; }
  .busy-line { display: flex; align-items: center; gap: 0.5rem; }
  .list { display: grid; gap: 0.25rem; max-height: 16rem; overflow: auto; padding: 0.25rem; border-radius: var(--r-control); background: var(--fill); }
  .item { display: flex; align-items: center; gap: 0.875rem; min-height: 2.75rem; padding: 0.5rem 0.75rem; border: 0; border-radius: 0.625rem; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; transition: background-color 150ms ease, transform 100ms ease-out; }
  .item:hover { background: var(--surface); }
  .item:active { transform: scale(0.99); }
  .item.on { background: var(--surface); box-shadow: 0 0 0 2px var(--accent); }
  .item .grow { flex: 1; display: grid; min-width: 0; }
  .size { min-width: 4.5rem; text-align: right; }
  .field-text { display: grid; gap: 0.125rem; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; flex-wrap: wrap; }
  .del { margin-right: auto; }
  @media (max-width: 639px) { .item { flex-wrap: wrap; } }
</style>
