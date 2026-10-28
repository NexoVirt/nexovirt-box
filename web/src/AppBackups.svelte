<script>
  // Backups of one app (My Apps): recent backups, back up now, restore.
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes, ago } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import RestoreDialog from './RestoreDialog.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Play from '@lucide/svelte/icons/play';
  import RotateCcw from '@lucide/svelte/icons/rotate-ccw';

  let { open = $bindable(false), app = null, apps = [], origin = null, onSessionLost, onNavigate, onDone } = $props();

  let data = $state(null), target = $state(''), busy = $state(false), lines = $state([]), restoreOpen = $state(false);
  let targets = $derived(data?.targets?.targets || []);

  $effect(() => { if (open && app) load(); });

  async function load() {
    data = null;
    lines = [];
    const res = await api(`/api/backups?app=${app.id}&limit=8`);
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { toast(res.error, 'error'); return; }
    data = res.data;
    if (!targets.some((t) => t.id === target)) target = targets[0]?.id || '';
  }

  async function backup() {
    busy = true;
    const job = await runJob('/api/backups/run', { app: app.id, target }, (j) => (lines = j.lines));
    busy = false;
    if (job.status === 'done') toast(`Backup of ${app.name} saved (${bytes(job.result?.size || 0)}).`, 'success');
    else toast(job.error, 'error', 9000);
    load();
    onDone?.();
  }

  const setUp = () => { open = false; onNavigate?.('/backups'); };
</script>

<Dialog bind:open label="Backups" {origin} locked={busy} wide>
  <div class="dlg">
    <h2 class="t-title">Backups of {app?.name}</h2>
    {#if !data}
      <p class="t-sub busy-line"><Loader size={16} class="spinner" />Loading</p>
    {:else if !data.key.configured || !targets.length}
      <p class="t-sub">Set up the backup key and a target on the Backups page first. Then this app can be backed up and restored here.</p>
      <div class="buttons"><button class="btn btn-gray" data-close onclick={() => (open = false)}>Close</button><button class="btn btn-primary" onclick={setUp}>Open Backups</button></div>
    {:else}
      <div class="run">
        <select class="input" bind:value={target} disabled={busy} aria-label="Target">{#each targets as t (t.id)}<option value={t.id}>{t.name}</option>{/each}</select>
        <button class="btn btn-primary" onclick={backup} disabled={busy}>{#if busy}<Loader size={16} class="spinner" />Backing up{:else}<Play size={15} />Back up now{/if}</button>
        <button class="btn btn-gray" onclick={() => (restoreOpen = true)} disabled={busy}><RotateCcw size={15} />Restore</button>
      </div>
      <p class="t-caption">{app?.name} stops while its files are copied and starts again right after.</p>
      {#if busy}<JobLog {lines} />{/if}
      <div class="group inset">
        {#each data.history as r (r.id)}
          <div class="row">
            <span class="dot {r.status === 'ok' ? 'ok' : 'error'}"></span>
            <div class="grow"><span>{r.kind === 'backup' ? 'Backup' : r.kind === 'restore' ? 'Restore' : 'Check'} to {r.target_name}</span><span class="t-caption">{r.error || r.name}</span></div>
            <span class="t-caption">{r.size ? bytes(r.size) + ', ' : ''}{ago(r.at)}</span>
          </div>
        {:else}
          <div class="row t-sub">No backup of this app yet.</div>
        {/each}
      </div>
      <div class="buttons"><button class="btn btn-gray" data-close onclick={() => (open = false)} disabled={busy}>Close</button></div>
    {/if}
  </div>
</Dialog>

<RestoreDialog bind:open={restoreOpen} app={app?.id} preset={{ target, app: app?.id }} {targets} {apps} {onSessionLost} onDone={() => { load(); onDone?.(); }} />

<style>
  h2, p { margin: 0; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .busy-line { display: flex; align-items: center; gap: 0.5rem; }
  .run { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .run select { flex: 1; min-width: 10rem; }
  .inset { box-shadow: 0 0 0 1px var(--hairline); }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; }
</style>
