<script>
  // App details, the install form (typed questions) and the live install progress, in one dialog.
  import { api } from '$lib/api.js';
  import { runJob, plainText, appURL, versionLabel } from '$lib/jobs.js';
  import AppIcon from '$lib/AppIcon.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import X from '@lucide/svelte/icons/x';

  let { summary, arch, installed = false, docker, onClose, onInstalled, onSessionLost } = $props();

  let app = $state(null);
  let loadError = $state('');
  let view = $state('info'); // info | form | progress | done | failed
  let answers = $state({});
  let lines = $state([]);
  let error = $state('');
  let result = $state(null);
  let busy = $state(false);

  let archOK = $derived(!summary.architectures?.length || summary.architectures.includes(arch));
  let visible = $derived((app?.questions || []).filter((q) => !q.generate));
  let generated = $derived((app?.questions || []).filter((q) => q.generate));
  let blocker = $derived(
    installed ? 'Already installed.'
    : !archOK ? `Not available for ${arch} servers.`
    : !docker?.installed ? 'Install Docker first.'
    : !docker?.running ? 'Docker is not running.'
    : '',
  );

  $effect(() => {
    const id = summary.id;
    app = null;
    loadError = '';
    api(`/api/catalog/${id}`).then((res) => {
      if (res.status === 401) return onSessionLost();
      if (!res.ok) loadError = res.error;
      else {
        app = res.data;
        const a = {};
        for (const q of app.questions) a[q.key] = q.type === 'boolean' ? (q.default || 'false') : (q.generate ? '' : q.default || '');
        answers = a;
      }
    });
  });

  async function install(e) {
    e.preventDefault();
    busy = true;
    view = 'progress';
    lines = [];
    const job = await runJob(`/api/apps/${app.id}/install`, { answers }, (j) => (lines = j.lines));
    busy = false;
    if (job.http === 401) return onSessionLost();
    if (job.status === 'done') {
      result = job.result;
      view = 'done';
      onInstalled();
    } else {
      error = job.error;
      view = 'failed';
    }
  }

  function inputType(q) {
    return { password: 'password', number: 'number', port: 'number', email: 'email' }[q.type] || 'text';
  }
</script>

{#snippet field(q)}
  {#if q.type === 'boolean'}
    <label class="row-field">
      <span class="field-text"><span class="field-label">{q.label}</span>{#if q.hint}<span class="t-caption">{q.hint}</span>{/if}</span>
      <input class="switch" type="checkbox" checked={answers[q.key] === 'true'} onchange={(e) => (answers[q.key] = e.currentTarget.checked ? 'true' : 'false')} />
    </label>
  {:else}
    <label class="field">
      <span class="field-label">{q.label}{#if q.required}<span class="req" aria-hidden="true"> *</span>{/if}</span>
      {#if q.type === 'select'}
        <select class="input" bind:value={answers[q.key]} required={q.required}>
          {#if !q.required}<option value="">Not set</option>{/if}
          {#each q.options as o (o)}<option value={o}>{o}</option>{/each}
        </select>
      {:else}
        <input class="input" type={inputType(q)} bind:value={answers[q.key]} required={q.required && !q.generate}
          placeholder={q.generate ? 'Generated automatically' : q.placeholder || ''}
          minlength={q.type === 'text' || q.type === 'password' ? q.min || undefined : undefined}
          maxlength={q.max && (q.type === 'text' || q.type === 'password') ? q.max : undefined}
          min={q.type === 'port' ? 1 : q.type === 'number' ? q.min || undefined : undefined}
          max={q.type === 'port' ? 65535 : q.type === 'number' ? q.max || undefined : undefined}
          autocomplete={q.type === 'password' ? 'new-password' : 'off'} spellcheck="false" />
      {/if}
      {#if q.hint}<span class="t-caption">{q.hint}</span>{/if}
    </label>
  {/if}
{/snippet}

<div class="detail">
  <header class="head">
    {#if view === 'form' || view === 'failed'}
      <button class="icon-btn back" onclick={() => (view = view === 'failed' ? 'form' : 'info')} aria-label="Back"><ChevronLeft size={20} /></button>
    {/if}
    <AppIcon id={summary.id} name={summary.name} size={56} />
    <div class="title">
      <h2 class="t-title truncate">{summary.name}</h2>
      <p class="t-caption truncate">{[summary.version && `Version ${versionLabel(summary.version)}`, summary.category].filter(Boolean).join('  ·  ')}</p>
    </div>
    <button class="icon-btn" data-close onclick={onClose} disabled={busy} aria-label="Close"><X size={20} /></button>
  </header>

  {#if loadError}
    <div class="notice"><CircleAlert size={18} /><span>{loadError}</span></div>
  {:else if !app}
    <div class="loading t-sub"><Loader size={16} class="spinner" />Loading</div>
  {:else if view === 'info'}
    <div class="actions">
      <button class="btn btn-primary" disabled={!!blocker} onclick={() => (view = 'form')}>Install</button>
      {#if blocker}<span class="t-caption">{blocker}</span>{/if}
    </div>
    {#if app.docker_socket}
      <div class="notice neutral"><TriangleAlert size={18} /><span>This app gets access to Docker on this server. That means full control over the server. Only install it if you trust it.</span></div>
    {/if}
    {#if app.tagline}<p class="tagline">{app.tagline}</p>{/if}
    {#if app.screenshots?.length}
      <div class="shots">
        {#each app.screenshots as _, i (i)}
          <img src="/api/catalog/{app.id}/screenshot/{i}" alt="Screenshot {i + 1} of {app.name}" loading="lazy" onerror={(e) => e.currentTarget.remove()} />
        {/each}
      </div>
    {/if}
    {#if app.description && plainText(app.description) !== app.tagline}<p class="desc">{plainText(app.description)}</p>{/if}
    <div class="group meta">
      {#if app.main_port}<div class="row"><span class="row-label">Default web port</span><span class="row-value num">{app.main_port}</span></div>{/if}
      {#if app.website}<div class="row"><span class="row-label">Website</span><a class="row-value link truncate" href={app.website} target="_blank" rel="noopener noreferrer">{app.website.replace(/^https?:\/\//, '')}</a></div>{/if}
      {#if app.source}<div class="row"><span class="row-label">Source code</span><a class="row-value link truncate" href={app.source} target="_blank" rel="noopener noreferrer">{app.source.replace(/^https?:\/\//, '')}</a></div>{/if}
      <div class="row"><span class="row-label">App definition</span><a class="row-value link truncate" href={app.attribution.url} target="_blank" rel="noopener noreferrer">{app.attribution.store}, {app.attribution.license}</a></div>
    </div>
  {:else if view === 'form'}
    <form class="form" onsubmit={install}>
      {#if !visible.length}<p class="t-sub">{app.name} needs no settings. Box stores its data in its own folder.</p>{/if}
      {#each visible as q (q.key)}{@render field(q)}{/each}
      {#if generated.length}
        <details class="advanced">
          <summary>Generated passwords ({generated.length})</summary>
          <p class="t-caption">Box creates strong random values for these. Enter your own only if you need to know them.</p>
          {#each generated as q (q.key)}{@render field(q)}{/each}
        </details>
      {/if}
      <button class="btn btn-primary btn-block" disabled={busy}>Install {app.name}</button>
    </form>
  {:else if view === 'progress'}
    <p class="t-sub busy"><Loader size={16} class="spinner" />Installing {app.name}. The first download can take a few minutes.</p>
    <JobLog {lines} />
  {:else if view === 'done'}
    <div class="done">
      <span class="ok-ico"><CircleCheck size={28} /></span>
      <h3 class="t-title">{app.name} is installed</h3>
      <p class="t-sub">It runs now and starts again after a reboot.</p>
      <div class="buttons">
        {#if appURL(result)}
          <a class="btn btn-primary" href={appURL(result)} target="_blank" rel="noopener noreferrer"><ExternalLink size={16} />Open {app.name}</a>
        {/if}
        <button class="btn btn-gray" onclick={onClose}>Done</button>
      </div>
    </div>
  {:else if view === 'failed'}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
    {#if lines.length}<JobLog {lines} />{/if}
  {/if}
</div>

<style>
  .detail { display: grid; gap: 1rem; padding: 1.25rem 1.5rem 1.5rem; }
  .head { display: flex; align-items: center; gap: 0.875rem; }
  .back { margin-left: -0.75rem; }
  .title { flex: 1; min-width: 0; }
  h2, h3, p { margin: 0; }
  .loading, .busy { display: flex; align-items: center; gap: 0.5rem; }
  .actions { display: flex; align-items: center; gap: 0.875rem; flex-wrap: wrap; }
  .tagline { font-size: 0.9375rem; font-weight: 500; }
  .desc { color: var(--text-2); white-space: pre-line; max-height: 14rem; overflow: auto; }
  .shots { display: flex; gap: 0.75rem; overflow-x: auto; scroll-snap-type: x mandatory; padding-bottom: 0.25rem; }
  .shots img { height: 11rem; border-radius: 0.875rem; scroll-snap-align: start; box-shadow: 0 0 0 1px var(--hairline); }
  .meta { box-shadow: none; background: var(--surface-2); }
  :global(.dark) .meta { background: var(--fill); }
  .link { color: var(--accent); }
  .form { display: grid; gap: 1rem; }
  .req { color: var(--text-3); }
  .row-field { display: flex; align-items: center; justify-content: space-between; gap: 1rem; cursor: pointer; }
  .field-text { display: grid; gap: 0.125rem; }
  .advanced { display: grid; gap: 0.75rem; padding: 0.75rem 0.875rem; border-radius: var(--r-control); background: var(--fill); }
  .advanced[open] { gap: 0.875rem; }
  .advanced summary { cursor: pointer; font-weight: 600; font-size: 0.8125rem; }
  .advanced .input { background: var(--surface); }
  .done { display: grid; justify-items: center; gap: 0.5rem; text-align: center; padding: 0.5rem 0 0.25rem; }
  .ok-ico { display: grid; place-items: center; width: 3.5rem; height: 3.5rem; border-radius: 50%; background: var(--accent-fill); color: var(--accent); margin-bottom: 0.25rem; }
  .buttons { display: flex; gap: 0.5rem; flex-wrap: wrap; justify-content: center; margin-top: 0.75rem; }
</style>
