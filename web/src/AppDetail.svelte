<script>
  // App details, the install form (typed questions) and the live install progress, in one dialog.
  import { api } from '$lib/api.js';
  import { untrack } from 'svelte';
  import { installs, startInstall } from '$lib/installs.svelte.js';
  import { plainText, appURL, versionLabel, generatePassword, webLink } from '$lib/jobs.js';
  import AppIcon from '$lib/AppIcon.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import Secret from '$lib/Secret.svelte';
  import Credentials from '$lib/Credentials.svelte';
  import HScroll from '$lib/HScroll.svelte';
  import Lightbox from '$lib/Lightbox.svelte';
  import RichText from '$lib/RichText.svelte';
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
  let dataDir = $state('');
  let error = $state('');
  let job = $derived(installs[summary.id]); // the install job, kept when this sheet closes
  let lines = $derived(job?.lines || []);
  let result = $derived(job?.result || null);
  let busy = $state(false);
  let own = $state({}); // password key -> the admin sets it instead of a generated one
  let confirm = $state({});
  let fieldError = $state({});
  let socketOK = $state(false);
  let badShots = $state({}); // screenshots that failed to load are left out
  let viewing = $state(-1); // lightbox index into shots
  let shotEls = [];
  let shots = $derived((app?.screenshots || []).map((_, i) => i).filter((i) => !badShots[i])
    .map((i) => ({ i, src: `/api/catalog/${app.id}/screenshot/${i}`, alt: `Screenshot ${i + 1} of ${app.name}` })));

  let archOK = $derived(!summary.architectures?.length || summary.architectures.includes(arch));
  let visible = $derived((app?.questions || []).filter((q) => q.type !== 'password'));
  let passwords = $derived((app?.questions || []).filter((q) => q.type === 'password'));
  const MIN_OWN = 8;
  const minOwn = (q) => Math.max(q.min || 0, MIN_OWN);
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
        for (const q of app.questions) {
          if (q.type === 'password') a[q.key] = q.locked ? '' : generatePassword(q); // locked ones are made by the server
          else a[q.key] = q.type === 'boolean' ? (q.default || 'false') : (q.generate ? '' : q.default || '');
        }
        answers = a;
        own = {};
        confirm = {};
        fieldError = {};
        socketOK = false;
        badShots = {};
        const j = untrack(() => installs[id]);
        view = !j ? 'info' : j.status === 'running' ? 'progress' : j.status === 'done' ? 'done' : 'failed';
        if (view === 'failed') error = j.error || 'The install did not finish.';
      }
    });
  });

  // The install ends while the sheet shows its progress.
  $effect(() => {
    const st = job?.status;
    untrack(() => {
      if (view !== 'progress' || !st || st === 'running') return;
      if (job.http === 401) return onSessionLost();
      if (st === 'done') { view = 'done'; onInstalled(); }
      else { error = job.error || 'The install did not finish.'; view = 'failed'; }
    });
  });

  function setOwn(q, on) {
    own = { ...own, [q.key]: on };
    answers[q.key] = on ? '' : generatePassword(q);
    confirm[q.key] = '';
    fieldError = { ...fieldError, [q.key]: '' };
  }

  function checkPasswords() {
    const errs = {};
    for (const q of passwords) {
      if (!own[q.key]) continue;
      const v = answers[q.key] || '';
      if (v.length < minOwn(q)) errs[q.key] = `Use at least ${minOwn(q)} characters.`;
      else if (q.max && v.length > q.max) errs[q.key] = `Use at most ${q.max} characters.`;
      else if (/['\r\n]/.test(v)) errs[q.key] = 'Single quotes and line breaks are not possible here.';
      else if (v !== (confirm[q.key] || '')) errs[q.key] = 'The two passwords do not match.';
    }
    fieldError = errs;
    return !Object.keys(errs).length;
  }

  async function install(e) {
    e.preventDefault();
    if (!checkPasswords()) return;
    busy = true;
    const res = await startInstall(app.id, app.name, { answers, data_dir: dataDir.trim(), allow_docker_socket: socketOK });
    busy = false;
    if (res.cancelled) return;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) {
      error = res.error;
      view = 'failed';
      return;
    }
    view = 'progress';
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

{#snippet pwField(q)}
  <div class="pw">
    <div class="pw-head">
      <span class="field-text"><span class="field-label">{q.label}</span>{#if q.hint}<span class="t-caption">{q.hint}</span>{/if}</span>
      {#if !q.locked}
        <button type="button" class="link-btn" onclick={() => setOwn(q, !own[q.key])}>{own[q.key] ? 'Use a generated one' : 'Set my own'}</button>
      {/if}
    </div>
    {#if q.locked}
      <span class="pw-value"><span class="pill">Generated</span><span class="t-caption">Shown in the login details after the install.</span></span>
    {:else if !own[q.key]}
      <span class="pw-value"><span class="pill">Generated</span><Secret value={answers[q.key]} label={q.label} /></span>
    {:else}
      <input class="input" type="password" bind:value={answers[q.key]} placeholder="New password" aria-label={q.label}
        autocomplete="new-password" minlength={minOwn(q)} maxlength={q.max || undefined} required />
      <input class="input" type="password" bind:value={confirm[q.key]} placeholder="Repeat the password" aria-label="Repeat {q.label}"
        autocomplete="new-password" required />
      {#if fieldError[q.key]}<span class="t-caption err" role="alert">{fieldError[q.key]}</span>
      {:else}<span class="t-caption">At least {minOwn(q)} characters.</span>{/if}
    {/if}
  </div>
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
    {#if app && view === 'info'}
      <div class="head-actions wide-only">
        {#if blocker}<span class="t-caption blocker">{blocker}</span>{/if}
        <button class="btn btn-primary btn-sm" disabled={!!blocker} onclick={() => (view = 'form')}>Install</button>
      </div>
    {:else if app && view === 'form'}
      <div class="head-actions wide-only">
        <button class="btn btn-primary btn-sm" form="install-form" disabled={busy}>Install</button>
      </div>
    {/if}
    <button class="icon-btn" data-close onclick={onClose} aria-label="Close"><X size={20} /></button>
  </header>

  {#if loadError}
    <div class="notice"><CircleAlert size={18} /><span>{loadError}</span></div>
  {:else if !app}
    <div class="loading t-sub"><Loader size={16} class="spinner" />Loading</div>
  {:else if view === 'info'}
    <div class="actions narrow-only">
      <button class="btn btn-primary" disabled={!!blocker} onclick={() => (view = 'form')}>Install</button>
      {#if blocker}<span class="t-caption">{blocker}</span>{/if}
    </div>
    {#if app.docker_socket}
      <div class="notice neutral"><TriangleAlert size={18} /><span>This app gets access to Docker on this server. That means full control over the server. Only install it if you trust it.</span></div>
    {/if}
    {#if app.tagline}<p class="tagline">{app.tagline}</p>{/if}
    {#if shots.length}
      <HScroll label="screenshots" gap="0.75rem" aria-label="Screenshots">
        {#each shots as s, k (s.i)}
          <button type="button" class="shot" bind:this={shotEls[k]} onclick={() => (viewing = k)} aria-label="Open {s.alt}">
            <img src={s.src} alt={s.alt} loading="lazy" onerror={() => (badShots = { ...badShots, [s.i]: true })} />
          </button>
        {/each}
      </HScroll>
      <Lightbox images={shots} bind:index={viewing} thumb={(k) => shotEls[k]?.querySelector('img')} label="Screenshots of {app.name}" />
    {/if}
    {#if app.notes}
      <section class="getting-started"><h3 class="t-section">Getting started</h3><RichText text={app.notes} /></section>
    {/if}
    {#if app.description && plainText(app.description) !== app.tagline}<p class="desc">{plainText(app.description)}</p>{/if}
    <div class="group meta">
      {#if app.main_port}<div class="row"><span class="row-label">Default web port</span><span class="row-value num">{app.main_port}</span></div>{/if}
      {#if webLink(app.website)}<div class="row"><span class="row-label">Website</span><a class="row-value link truncate" href={webLink(app.website)} target="_blank" rel="noopener noreferrer">{app.website.replace(/^https?:\/\//, '')}</a></div>{/if}
      {#if webLink(app.source)}<div class="row"><span class="row-label">Source code</span><a class="row-value link truncate" href={webLink(app.source)} target="_blank" rel="noopener noreferrer">{app.source.replace(/^https?:\/\//, '')}</a></div>{/if}
      <div class="row"><span class="row-label">App definition</span><a class="row-value link truncate" href={webLink(app.attribution.url)} target="_blank" rel="noopener noreferrer">{app.attribution.store}, {app.attribution.license}</a></div>
    </div>
  {:else if view === 'form'}
    <form class="form" id="install-form" onsubmit={install}>
      {#if !visible.length && !passwords.length}<p class="t-sub">{app.name} needs no settings. Box stores its data in its own folder.</p>{/if}
      {#each visible as q (q.key)}{@render field(q)}{/each}
      {#if passwords.length}
        <section class="pw-group" aria-label="Passwords">
          <h3 class="t-section">Passwords</h3>
          <p class="t-caption">Box creates strong passwords for you. You find them later in Apps, Installed, under Login details.</p>
          {#each passwords as q (q.key)}{@render pwField(q)}{/each}
        </section>
      {/if}
      {#if app.docker_socket}
        <label class="row-field socket">
          <span class="field-text"><span class="field-label">Give {app.name} access to Docker</span><span class="t-caption">It can then start any container and control the whole server. Only agree if you trust this app.</span></span>
          <input class="switch" type="checkbox" bind:checked={socketOK} required />
        </label>
      {/if}
      <details class="advanced">
        <summary>Data folder</summary>
        <p class="t-caption">Where {app.name} keeps its files. Leave empty for Box's own folder (/var/lib/nexovirt-box/apps/{app.id}/data). A folder you choose must be new or empty.</p>
        <input class="input" bind:value={dataDir} placeholder="/srv/{app.id}" aria-label="Data folder" spellcheck="false" autocapitalize="none" />
      </details>
      <button class="btn btn-primary btn-block narrow-only" disabled={busy}>Install {app.name}</button>
    </form>
  {:else if view === 'progress'}
    <p class="t-sub busy"><Loader size={16} class="spinner" />Installing {app.name}. The first download can take a few minutes.</p>
    <p class="t-caption">You can close this. The install goes on in the background and Apps shows its progress.</p>
    <JobLog {lines} />
  {:else if view === 'done'}
    <div class="done">
      <span class="ico xl accent ok-ico"><CircleCheck /></span>
      <h3 class="t-title">{app.name} is installed</h3>
      <p class="t-sub">It runs now and starts again after a reboot.</p>
      <div class="done-creds"><Credentials id={app.id} {onSessionLost} /></div>
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
  .detail { display: grid; grid-template-columns: minmax(0, 1fr); gap: 1rem; padding: 1.25rem 1.5rem 1.5rem; }
  .head { display: flex; align-items: center; gap: 0.875rem; }
  .back { margin-left: -0.75rem; }
  .title { flex: 1; min-width: 0; }
  h2, h3, p { margin: 0; }
  .loading, .busy { display: flex; align-items: center; gap: 0.5rem; }
  .head-actions { display: flex; align-items: center; gap: 0.75rem; flex-shrink: 0; }
  .blocker { max-width: 12rem; text-align: right; }
  .narrow-only { display: none !important; }
  @media (max-width: 639px) {
    .wide-only { display: none !important; }
    .narrow-only { display: flex !important; }
  }
  .actions { display: flex; align-items: center; gap: 0.875rem; flex-wrap: wrap; }
  .tagline { font-size: 0.9375rem; font-weight: 500; }
  .desc { color: var(--text-2); white-space: pre-line; max-height: 14rem; overflow-y: auto; overflow-x: hidden; overflow-wrap: anywhere; }
  .shot { flex-shrink: 0; padding: 0; border: 0; background: none; border-radius: 0.875rem; cursor: zoom-in; transition: transform 100ms ease-out; }
  .shot:active { transform: scale(0.97); }
  .shot:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .shot img { display: block; height: 11rem; max-width: none; border-radius: 0.875rem; }
  .getting-started { display: grid; gap: 0.375rem; padding: 0.75rem 0.875rem; border-radius: var(--r-control); background: var(--fill); }
  .meta { box-shadow: none; background: var(--surface-2); }
  :global(.dark) .meta { background: var(--fill); }
  .link { color: var(--accent); }
  .form { display: grid; grid-template-columns: minmax(0, 1fr); gap: 1rem; }
  .req { color: var(--text-3); }
  .row-field { display: flex; align-items: center; justify-content: space-between; gap: 1rem; cursor: pointer; }
  .field-text { display: grid; gap: 0.125rem; }
  .advanced { display: grid; gap: 0.75rem; padding: 0.75rem 0.875rem; border-radius: var(--r-control); background: var(--fill); }
  .advanced[open] { gap: 0.875rem; }
  .advanced summary { cursor: pointer; font-weight: 600; font-size: 0.8125rem; }
  .advanced .input { background-color: var(--surface); }
  .pw-group { display: grid; gap: 0.75rem; }
  .pw-group h3 { margin: 0; }
  .pw { display: grid; gap: 0.5rem; padding: 0.75rem 0.875rem; border-radius: var(--r-control); background: var(--fill); }
  .pw .input { background-color: var(--surface); }
  .pw-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; }
  .pw-value { display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
  .link-btn { border: 0; background: none; padding: 0.25rem 0; min-height: 2rem; color: var(--accent); font-weight: 600; font-size: 0.8125rem; cursor: pointer; white-space: nowrap; }
  .err { color: var(--error); }
  .socket { padding: 0.75rem 0.875rem; border-radius: var(--r-control); background: var(--fill); }
  .done-creds { width: 100%; text-align: left; margin-top: 0.5rem; }
  .done { display: grid; justify-items: center; gap: 0.5rem; text-align: center; padding: 0.5rem 0 0.25rem; }
  .ok-ico { margin-bottom: 0.25rem; }
  .buttons { display: flex; gap: 0.5rem; flex-wrap: wrap; justify-content: center; margin-top: 0.75rem; }
</style>
