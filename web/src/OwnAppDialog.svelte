<script>
  // Add your own app from a compose file (pasted or uploaded), or edit one with a diff preview.
  // Box stores the file exactly as given; the helper checks it before anything runs.
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { withProof } from '$lib/proof.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import { lineDiff } from '$lib/diff.js';
  import { OWN_ICONS } from '$lib/ownIcons.js';
  import Dialog from '$lib/Dialog.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import AppIcon from '$lib/AppIcon.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Upload from '@lucide/svelte/icons/upload';
  import FileCode from '@lucide/svelte/icons/file-code';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';

  let { open = $bindable(false), editId = '', origin = null, onDone, onSessionLost } = $props();

  const SAMPLE = `services:\n  web:\n    image: nginx:alpine\n    ports:\n      - "8080:80"\n    volumes:\n      - ./html:/usr/share/nginx/html\n    restart: unless-stopped\n`;
  let name = $state(''), composeText = $state(''), env = $state(''), icon = $state('lucide:server'), upload = $state(null), uploadPreview = $state('');
  let socket = $state(false), original = $state(null), loading = $state(false), step = $state('edit'), busy = $state(false), lines = $state([]), error = $state('');

  $effect(() => {
    if (!open) return;
    error = ''; lines = []; step = 'edit'; upload = null; uploadPreview = ''; socket = false;
    if (editId) load();
    else { original = null; name = ''; composeText = ''; env = ''; icon = 'lucide:server'; }
  });

  async function load() {
    loading = true;
    const res = await api(`/api/apps/${editId}/custom`);
    loading = false;
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { error = res.error; return; }
    original = res.data;
    name = res.data.name; composeText = res.data.compose; env = res.data.env; icon = res.data.icon || 'lucide:server'; socket = !!res.data.docker_socket_ok;
  }

  let mentionsSocket = $derived(/docker\.sock/.test(composeText));
  let diff = $derived(original && step === 'review' ? lineDiff(original.compose, composeText) : []);
  let envChanged = $derived(original && original.env !== env);
  let changed = $derived(diff.filter((d) => d.op !== ' ').length);

  function readFile(e, into) {
    const f = e.currentTarget.files?.[0];
    if (!f) return;
    if (f.size > 256 * 1024) { error = 'The file can have at most 256 KB.'; return; }
    const r = new FileReader();
    r.onload = () => { if (into === 'compose') composeText = r.result; else env = r.result; };
    r.readAsText(f);
  }

  function pickIcon(e) {
    const f = e.currentTarget.files?.[0];
    if (!f) return;
    if (f.size > 200 * 1024) { error = 'The icon can have at most 200 KB.'; return; }
    const r = new FileReader();
    r.onload = () => { upload = r.result; uploadPreview = r.result; icon = 'upload'; };
    r.readAsDataURL(f);
  }

  function next(e) {
    e.preventDefault();
    error = '';
    if (!name.trim()) { error = 'Give the app a name.'; return; }
    if (!composeText.trim()) { error = 'Paste or upload a compose file.'; return; }
    if (mentionsSocket && !socket) { error = 'This compose file mounts the Docker socket. Allow it below if you trust the app.'; return; }
    if (original) step = 'review';
    else save();
  }

  async function save() {
    busy = true;
    error = '';
    lines = [];
    const body = { op: original ? 'edit' : 'add', id: original?.id || '', name, compose: composeText, env, icon, allow_docker_socket: socket };
    const job = await withProof(() => runJob('/api/apps/custom', body, (j) => (lines = j.lines)));
    if (job.cancelled) { busy = false; return; }
    if (job.http === 401) { busy = false; return onSessionLost?.(); }
    if (job.status !== 'done') { busy = false; error = job.error; return; }
    const id = job.result?.id || original?.id;
    if (upload && id) {
      const r = await api(`/api/apps/${id}/icon`, { method: 'POST', body: { data: upload } });
      if (!r.ok) toast(r.error, 'error');
    }
    busy = false;
    open = false;
    toast(original ? `${name} was updated.` : `${name} is installed and running.`, 'success');
    onDone?.();
  }
</script>

<Dialog bind:open label={editId ? 'Edit app' : 'Add your own app'} {origin} locked={busy} wide>
  {#if step === 'edit'}
    <form class="dlg" onsubmit={next}>
      <h2 class="t-title">{editId ? `Edit ${original?.name || ''}` : 'Add your own app'}</h2>
      {#if loading}
        <p class="t-sub busy-line"><Loader size={16} class="spinner" />Loading</p>
      {:else}
        {#if !editId}<p class="t-sub">Paste a Docker Compose file. Box keeps it exactly as you wrote it and manages the app like one from the store: start, stop, update, logs, backups and a domain.</p>{/if}
        {#if error}<div class="notice" role="alert">{error}</div>{/if}
        <div class="top">
          <label class="field grow"><span class="field-label">Name</span><input class="input" bind:value={name} maxlength="60" placeholder="My app" disabled={busy} /></label>
          <div class="field">
            <span class="field-label">Icon</span>
            <div class="icons" role="radiogroup" aria-label="Icon">
              {#each Object.keys(OWN_ICONS) as k}
                {@const I = OWN_ICONS[k]}
                <button type="button" class="ic" class:on={icon === 'lucide:' + k} role="radio" aria-checked={icon === 'lucide:' + k} aria-label={k} onclick={() => { icon = 'lucide:' + k; upload = null; }}><I size={17} /></button>
              {/each}
              <label class="ic up" class:on={icon === 'upload'} title="Upload an image">
                {#if uploadPreview}<img src={uploadPreview} alt="" />{:else if icon === 'upload' && editId}<AppIcon id={editId} size={28} />{:else}<Upload size={17} />{/if}
                <input type="file" accept="image/png,image/jpeg,image/webp,image/svg+xml" onchange={pickIcon} hidden />
              </label>
            </div>
          </div>
        </div>
        <div class="field">
          <div class="label-row">
            <span class="field-label">compose.yaml</span>
            <span class="label-btns">
              {#if !composeText}<button type="button" class="link" onclick={() => (composeText = SAMPLE)}>Use an example</button>{/if}
              <label class="link"><FileCode size={14} />Upload file<input type="file" accept=".yml,.yaml,text/yaml" onchange={(e) => readFile(e, 'compose')} hidden /></label>
            </span>
          </div>
          <textarea class="input mono code" bind:value={composeText} rows="12" spellcheck="false" placeholder="services:&#10;  web:&#10;    image: ..." disabled={busy}></textarea>
        </div>
        <div class="field">
          <div class="label-row">
            <span class="field-label">.env (optional)</span>
            <label class="link"><FileCode size={14} />Upload file<input type="file" onchange={(e) => readFile(e, 'env')} hidden /></label>
          </div>
          <textarea class="input mono code env" bind:value={env} rows="3" spellcheck="false" placeholder="KEY=value" disabled={busy}></textarea>
        </div>
        <p class="t-caption">Relative folders like ./data stay inside the app's own folder. Images must be ready to pull (no build). Box refuses access to its own files, system folders, host devices and privileged mode.</p>
        {#if mentionsSocket}
          <label class="row-field warn"><span class="field-text"><span class="field-label"><TriangleAlert size={15} />Allow access to Docker</span><span class="t-caption">The file mounts the Docker socket. That gives the app full control of the server.</span></span><input class="switch" type="checkbox" bind:checked={socket} disabled={busy} /></label>
        {/if}
        {#if busy}<JobLog {lines} />{/if}
        <div class="buttons">
          <button type="button" class="btn btn-gray" data-close onclick={() => (open = false)} disabled={busy}>Cancel</button>
          <button class="btn btn-primary" disabled={busy}>{#if busy}<Loader size={16} class="spinner" />Installing{:else}{editId ? 'Review changes' : 'Check and install'}{/if}</button>
        </div>
      {/if}
    </form>
  {:else}
    <div class="dlg">
      <h2 class="t-title">Review the changes</h2>
      <p class="t-sub">{changed ? `${changed} changed lines in compose.yaml.` : 'compose.yaml is unchanged.'}{envChanged ? ' The .env file changed too.' : ''} The app is checked again and restarted with the new definition. If it does not start, the old one comes back.</p>
      {#if error}<div class="notice" role="alert">{error}</div>{/if}
      <pre class="diff mono">{#each diff as d}<span class="ln {d.op === '+' ? 'add' : d.op === '-' ? 'del' : ''}">{d.op} {d.text}
</span>{/each}</pre>
      {#if busy}<JobLog {lines} />{/if}
      <div class="buttons">
        <button class="btn btn-gray" onclick={() => (step = 'edit')} disabled={busy}>Back</button>
        <button class="btn btn-primary" onclick={save} disabled={busy || (!changed && !envChanged && name === original?.name && icon === original?.icon && !upload)}>{#if busy}<Loader size={16} class="spinner" />Saving{:else}Save and redeploy{/if}</button>
      </div>
    </div>
  {/if}
</Dialog>

<style>
  h2, p { margin: 0; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .field { display: grid; gap: 0.375rem; }
  .top { display: flex; gap: 1rem; flex-wrap: wrap; align-items: flex-start; }
  .top .grow { flex: 1; min-width: 12rem; }
  .icons { display: flex; flex-wrap: wrap; gap: 0.25rem; max-width: 20rem; }
  .ic { display: grid; place-items: center; width: 2.25rem; height: 2.25rem; border: 0; border-radius: 0.625rem; background: var(--fill); color: var(--text-2); cursor: pointer; overflow: hidden; transition: transform 100ms ease-out, background-color 150ms ease; }
  .ic:active { transform: scale(0.94); }
  .ic.on { background: var(--accent); color: var(--on-accent); }
  .ic img { width: 100%; height: 100%; object-fit: cover; }
  .label-row { display: flex; justify-content: space-between; align-items: center; gap: 1rem; }
  .label-btns { display: flex; gap: 0.875rem; }
  .link { display: inline-flex; align-items: center; gap: 0.3rem; border: 0; background: none; padding: 0; color: var(--accent); font: inherit; font-size: 0.8125rem; font-weight: 500; cursor: pointer; }
  .code { height: auto; min-height: 14rem; padding-block: 0.75rem; resize: vertical; font-size: 0.8125rem; line-height: 1.5; white-space: pre; tab-size: 2; }
  .code.env { min-height: 4.5rem; }
  .field-text { display: grid; gap: 0.125rem; }
  .warn .field-label { display: inline-flex; align-items: center; gap: 0.375rem; color: var(--warn); }
  .busy-line { display: flex; align-items: center; gap: 0.5rem; }
  .diff { margin: 0; max-height: 24rem; overflow: auto; padding: 0.75rem 0; border-radius: var(--r-control); background: var(--fill); font-size: 0.78rem; line-height: 1.5; }
  .ln { display: block; padding: 0 0.875rem; white-space: pre; }
  .ln.add { background: color-mix(in srgb, var(--ok) 16%, transparent); }
  .ln.del { background: color-mix(in srgb, var(--error) 14%, transparent); color: var(--text-2); }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; }
</style>
