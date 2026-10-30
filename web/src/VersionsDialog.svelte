<script>
  // Earlier versions of a file (kept when an upload replaced it). Restore keeps the current file
  // as a version too, so it can be undone.
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes, ago } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import History from '@lucide/svelte/icons/history';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { open = $bindable(false), path = '', onChanged, onSessionLost } = $props();
  let list = $state(null), error = $state(''), busy = $state('');
  const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });

  $effect(() => { if (open && path) load(); });

  async function load() {
    list = null;
    const res = await api('/api/files/versions?path=' + encodeURIComponent(path));
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { error = res.error; list = []; return; }
    error = ''; list = res.data.versions;
  }

  async function restore(v) {
    busy = v.id;
    const res = await api('/api/files/versions/restore', { method: 'POST', body: { path, id: v.id } });
    busy = '';
    if (!res.ok) return toast(res.error, 'error', 8000);
    toast(`The version from ${fmt.format(v.saved * 1000)} is back. The file before it is kept as a version.`, 'success', 7000);
    onChanged?.();
    load();
  }
</script>

<Dialog bind:open label="Versions">
  <div class="dlg">
    <h2 class="t-title">Versions of {path.split('/').pop()}</h2>
    <p class="t-sub">Box keeps the last 10 versions for 30 days when you replace a file here. Changes made outside Box are not kept.</p>
    {#if error}<div class="notice">{error}</div>{/if}
    <div class="group">
      {#if !list}<div class="row"><Loader size={16} class="spinner" />Loading</div>
      {:else}
        {#each list as v (v.id)}
          <div class="row">
            <span class="ico accent"><History /></span>
            <span class="grow"><span>{fmt.format(v.saved * 1000)}</span><span class="t-caption">{bytes(v.size)}, {ago(v.saved)}</span></span>
            <button class="btn btn-tinted btn-sm" onclick={() => restore(v)} disabled={!!busy}>{#if busy === v.id}<Loader size={15} class="spinner" />{/if}Restore</button>
          </div>
        {:else}
          <div class="row t-sub">No earlier versions.</div>
        {/each}
      {/if}
    </div>
    <div class="buttons"><button class="btn btn-gray" data-close onclick={() => (open = false)}>Close</button></div>
  </div>
</Dialog>

<style>
  h2, p { margin: 0; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .grow { flex: 1; min-width: 0; display: grid; }
  .buttons { display: flex; justify-content: flex-end; }
</style>
