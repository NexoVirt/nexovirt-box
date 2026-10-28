<script>
  // Catalog update for an installed app: shows what changes in compose.yaml and which settings
  // are added, then rewrites, checks and redeploys (the helper puts the old files back on failure).
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { withProof } from '$lib/proof.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import { lineDiff } from '$lib/diff.js';
  import Dialog from '$lib/Dialog.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { open = $bindable(false), app = null, origin = null, onSessionLost, onDone } = $props();

  let info = $state(null), error = $state(''), busy = $state(false), lines = $state([]), socket = $state(false);
  let diff = $derived(info ? lineDiff(info.old, info.new) : []);

  $effect(() => {
    if (open && app) load(app.id);
  });

  async function load(id) {
    info = null;
    error = '';
    lines = [];
    socket = false;
    const res = await api(`/api/apps/${id}/catalog-update`);
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { error = res.error; return; }
    info = res.data;
  }

  async function apply() {
    busy = true;
    lines = [];
    const job = await withProof(() => runJob(`/api/apps/${app.id}/catalog-update`, { allow_docker_socket: socket }, (j) => (lines = j.lines)));
    busy = false;
    if (job.cancelled) return;
    if (job.http === 401) return onSessionLost?.();
    if (job.status === 'done') { open = false; toast(`${app.name} runs with the new catalog definition.`, 'success'); }
    else toast(job.error.replace(/^needs_docker_socket:\s*/, ''), 'error', 10000);
    onDone?.();
  }
</script>

<Dialog bind:open label="Catalog update" {origin} locked={busy} wide>
  <div class="dlg">
    <h2 class="t-title">Catalog update for {app?.name}</h2>
    {#if error}
      <div class="notice" role="alert">{error}</div>
    {:else if !info}
      <p class="t-sub busy-line"><Loader size={16} class="spinner" />Comparing with the catalog</p>
    {:else}
      <p class="t-sub">The app store has a newer definition{info.version_new && info.version_new !== info.version_old ? ` (version ${info.version_new})` : ''}. Box rewrites compose.yaml, keeps your answers and passwords, checks it and starts the app again. If it does not start, the previous definition comes back.</p>
      <pre class="diff mono">{#each diff as d}<span class="ln {d.op === '+' ? 'add' : d.op === '-' ? 'del' : ''}">{d.op} {d.text}
</span>{/each}</pre>
      {#if info.new_keys.length}<p class="t-caption">New settings Box adds: <span class="mono">{info.new_keys.join(', ')}</span></p>{/if}
      {#if info.docker_socket}
        <label class="row-field">
          <span class="field-text"><span class="field-label">Allow the Docker socket</span><span class="t-caption">This app uses the Docker socket, which means full control of the server.</span></span>
          <input class="switch" type="checkbox" bind:checked={socket} disabled={busy} />
        </label>
      {/if}
      {#if busy || lines.length}<JobLog {lines} />{/if}
    {/if}
    <div class="buttons">
      <button class="btn btn-gray" data-close onclick={() => (open = false)} disabled={busy}>Cancel</button>
      <button class="btn btn-primary" onclick={apply} disabled={busy || !info || (info.docker_socket && !socket)}>
        {#if busy}<Loader size={16} class="spinner" />Updating{:else}Apply update{/if}
      </button>
    </div>
  </div>
</Dialog>

<style>
  .busy-line { display: flex; align-items: center; gap: 0.5rem; }
  .diff { margin: 0; max-height: 24rem; overflow: auto; padding: 0.75rem 0; border-radius: var(--r-control); background: var(--fill); font-size: 0.78rem; line-height: 1.5; }
  .ln { display: block; padding: 0 0.875rem; white-space: pre; }
  .ln.add { background: color-mix(in srgb, var(--ok) 16%, transparent); }
  .ln.del { background: color-mix(in srgb, var(--error) 14%, transparent); color: var(--text-2); }
</style>
