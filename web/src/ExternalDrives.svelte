<script>
  // External (USB) drives: mount, use as backup target or in Files, safe eject, format only
  // after typing FORMAT <device>.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { withProof } from '$lib/proof.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import Usb from '@lucide/svelte/icons/usb';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { onSessionLost, onNavigate } = $props();
  let drives = $state(null), error = $state(''), busy = $state('');
  let fmtFor = $state(null), fmtOpen = $state(false), fmtFS = $state('exfat'), fmtName = $state('Backup'), fmtConfirm = $state(''), fmtBusy = $state(false), fmtLines = $state([]), fmtError = $state('');

  async function load() {
    const res = await api('/api/drives');
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { error = res.error; drives = []; return; }
    error = ''; drives = res.data.drives;
  }

  async function act(kind, path, done) {
    busy = path + kind;
    const res = await api('/api/drives/' + kind, { method: 'POST', body: { path } });
    busy = '';
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) toast(res.error, 'error', 8000); else toast(done, 'success');
    load();
  }

  async function backupTarget(p, d) {
    const name = (p.label || d.model || 'USB drive').slice(0, 40);
    const res = await withProof(() => api('/api/backups/targets', { method: 'POST', body: { type: 'local', name, path: p.mountpoint + '/nexovirt-box-backups' } }));
    if (res.cancelled) return;
    if (!res.ok) return toast(res.error, 'error', 8000);
    toast(`${name} is a backup target now. Pick it in Backups > Schedules.`, 'success', 7000);
  }

  function askFormat(d) { fmtFor = d; fmtConfirm = ''; fmtError = ''; fmtLines = []; fmtOpen = true; }

  async function format(e) {
    e.preventDefault();
    fmtBusy = true; fmtError = '';
    const job = await withProof(() => runJob('/api/drives/format', { path: fmtFor.path, fs: fmtFS, name: fmtName, confirm: fmtConfirm }, (j) => (fmtLines = j.lines)));
    fmtBusy = false;
    if (job.cancelled) return;
    if (job.status !== 'done') { fmtError = job.error; return; }
    fmtOpen = false;
    toast(`${fmtFor.path} is formatted.`, 'success');
    load();
  }

  onMount(load);
</script>

<section class="ext">
  <div class="head"><h2 class="t-head">External drives</h2><button class="icon-btn" onclick={load} aria-label="Look for drives again" title="Look for drives again"><RefreshCw size={16} /></button></div>
  {#if error}<div class="notice">{error}</div>{/if}
  <div class="group">
    {#if !drives}<div class="row"><Loader size={16} class="spinner" />Looking for drives</div>
    {:else}
      {#each drives as d (d.path)}
        <div class="row">
          <span class="ico accent"><Usb /></span>
          <span class="grow"><span class="truncate">{[d.vendor, d.model].filter(Boolean).join(' ') || 'USB drive'}</span><span class="t-caption mono">{d.path} · {bytes(d.size)}</span></span>
          {#if d.parts.some((p) => p.ours)}<button class="btn btn-gray btn-sm" onclick={() => act('eject', d.path, 'You can unplug the drive now.')} disabled={!!busy}>Eject</button>{/if}
          <button class="btn btn-gray btn-sm" onclick={() => askFormat(d)} disabled={!!busy || d.parts.some((p) => p.mountpoint)}>Format</button>
        </div>
        {#each d.parts as p (p.path)}
          <div class="row part">
            <span class="grow"><span class="truncate">{p.label || p.path}</span><span class="t-caption">{p.fstype || 'no file system'}{p.mountpoint ? ` · mounted at ${p.mountpoint}` : ''} · {bytes(p.size)}</span></span>
            {#if !p.mountpoint}
              <button class="btn btn-tinted btn-sm" onclick={() => act('mount', p.path, `${p.label || p.path} is mounted.`)} disabled={!!busy || !p.fstype}>{#if busy === p.path + 'mount'}<Loader size={15} class="spinner" />{/if}Mount</button>
            {:else if p.ours}
              <button class="btn btn-gray btn-sm" onclick={() => onNavigate?.('/files?path=' + encodeURIComponent(p.mountpoint))}>Open in Files</button>
              <button class="btn btn-gray btn-sm" onclick={() => backupTarget(p, d)}>Use for backups</button>
              <button class="btn btn-gray btn-sm" onclick={() => act('unmount', d.path, 'The drive is unmounted.')} disabled={!!busy}>Unmount</button>
            {/if}
          </div>
        {/each}
      {:else}
        <div class="row t-sub">No USB drive is plugged in.</div>
      {/each}
    {/if}
  </div>
  <p class="t-caption">Mounted drives show up in Files (as a place) and can be shared in Shares. Box mounts them without running programs from them; a restart of the server unmounts them.</p>
</section>

<Dialog bind:open={fmtOpen} label="Format drive" locked={fmtBusy}>
  <form class="dlg" onsubmit={format}>
    <h2 class="t-title">Format {fmtFor?.path}?</h2>
    <p class="t-sub">Everything on {[fmtFor?.vendor, fmtFor?.model].filter(Boolean).join(' ') || 'this drive'} ({bytes(fmtFor?.size || 0)}) is deleted. This cannot be undone.</p>
    {#if fmtError}<div class="notice" role="alert">{fmtError}</div>{/if}
    <label class="field"><span class="field-label">File system</span>
      <select class="input" bind:value={fmtFS} disabled={fmtBusy}><option value="exfat">exFAT (works on Windows and Mac too)</option><option value="ext4">ext4 (Linux only)</option></select></label>
    <label class="field"><span class="field-label">Name</span><input class="input" bind:value={fmtName} maxlength="16" disabled={fmtBusy} /></label>
    <label class="field"><span class="field-label">Type FORMAT {fmtFor?.path} to confirm</span><input class="input mono" bind:value={fmtConfirm} autocomplete="off" spellcheck="false" disabled={fmtBusy} /></label>
    {#if fmtBusy}<JobLog lines={fmtLines} />{/if}
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => (fmtOpen = false)} disabled={fmtBusy}>Cancel</button>
      <button class="btn btn-danger" disabled={fmtBusy || fmtConfirm !== 'FORMAT ' + fmtFor?.path}>{#if fmtBusy}<Loader size={16} class="spinner" />Formatting{:else}Format{/if}</button>
    </div>
  </form>
</Dialog>

<style>
  h2, p { margin: 0; }
  .ext { display: grid; gap: 0.625rem; }
  .head { display: flex; align-items: center; justify-content: space-between; }
  .grow { flex: 1; min-width: 0; display: grid; }
  .part { padding-left: 3rem; flex-wrap: wrap; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; }
</style>
