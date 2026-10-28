<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { runJob, appURL } from '$lib/jobs.js';
  import { toast } from '$lib/toast.svelte.js';
  import AppIcon from '$lib/AppIcon.svelte';
  import Dialog from '$lib/Dialog.svelte';
  import Popover from '$lib/Popover.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import DockerSetup from './DockerSetup.svelte';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import Ellipsis from '@lucide/svelte/icons/ellipsis';
  import Play from '@lucide/svelte/icons/play';
  import Square from '@lucide/svelte/icons/square';
  import RotateCw from '@lucide/svelte/icons/rotate-cw';
  import Download from '@lucide/svelte/icons/download';
  import ScrollText from '@lucide/svelte/icons/scroll-text';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import LayoutGrid from '@lucide/svelte/icons/layout-grid';

  let { onSessionLost, onNavigate } = $props();

  const STATES = {
    running: ['Running', 'ok'], partial: ['Partly running', 'warn'], restarting: ['Restarting', 'warn'],
    stopped: ['Stopped', ''], unknown: ['Unknown', ''],
  };
  const DONE = { start: 'started', stop: 'stopped', restart: 'restarted', update: 'updated' };

  let apps = $state(null);
  let error = $state('');
  let docker = $state(null);
  let busy = $state({}); // id -> action label
  let menu = $state(null); // id of the open menu
  // Logs dialog
  let logsFor = $state(null);
  let logsOpen = $state(false);
  let logs = $state('');
  let logsLoading = $state(false);
  let logBox = $state();
  let follow = true;
  // Uninstall dialog
  let removeFor = $state(null);
  let removeOpen = $state(false);
  let deleteData = $state(false);
  let removeLines = $state([]);
  let removing = $state(false);
  let origin = $state(null);

  async function load() {
    const [a, d] = await Promise.all([api('/api/apps'), api('/api/docker')]);
    if (a.status === 401) return onSessionLost();
    if (!a.ok) error = a.error;
    else { error = ''; apps = a.data; }
    if (d.ok) docker = d.data;
  }

  async function act(app, op) {
    menu = null;
    busy = { ...busy, [app.id]: op };
    const job = await runJob(`/api/apps/${app.id}/action`, { op });
    const { [app.id]: _, ...rest } = busy;
    busy = rest;
    if (job.http === 401) return onSessionLost();
    if (job.status === 'done') toast(`${app.name} ${DONE[op]}.`, 'success');
    else toast(job.error, 'error', 8000);
    load();
  }

  async function fetchLogs() {
    if (!logsFor) return;
    const res = await api(`/api/apps/${logsFor.id}/logs?tail=400`);
    logsLoading = false;
    if (res.ok) {
      logs = res.data.logs || 'No output yet.';
      if (follow) requestAnimationFrame(() => logBox && (logBox.scrollTop = logBox.scrollHeight));
    } else logs = res.error;
  }

  function showLogs(e, app) {
    menu = null;
    origin = { x: e.clientX, y: e.clientY };
    logsFor = app;
    logs = '';
    logsLoading = true;
    follow = true;
    logsOpen = true;
    fetchLogs();
  }

  // Live tail while the logs dialog is open.
  $effect(() => {
    if (!logsOpen) return;
    const t = setInterval(fetchLogs, 3000);
    return () => clearInterval(t);
  });

  function onLogScroll() {
    follow = logBox.scrollHeight - logBox.scrollTop - logBox.clientHeight < 24;
  }

  function askRemove(e, app) {
    menu = null;
    origin = { x: e.clientX, y: e.clientY };
    removeFor = app;
    deleteData = false;
    removeLines = [];
    removeOpen = true;
  }

  async function remove() {
    removing = true;
    const app = removeFor;
    const job = await runJob(`/api/apps/${app.id}/uninstall`, { delete_data: deleteData }, (j) => (removeLines = j.lines));
    removing = false;
    if (job.status === 'done') {
      removeOpen = false;
      toast(deleteData ? `${app.name} and its data were removed.` : `${app.name} was removed. Its data was kept.`, 'success');
    } else toast(job.error, 'error', 8000);
    load();
  }

  onMount(() => {
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  });
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">My Apps</h1>
      <p class="t-sub">Apps installed on this server.</p>
    </div>
    <button class="btn btn-tinted btn-sm" onclick={() => onNavigate('/apps')}><LayoutGrid size={15} />App Store</button>
  </header>

  {#if docker && (!docker.installed || !docker.running)}
    <DockerSetup {docker} onDone={load} />
  {/if}

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else if !apps}
    <div class="group">{#each Array(3) as _}<div class="row"><span class="skeleton sq"></span><span class="skeleton line"></span></div>{/each}</div>
  {:else if !apps.length}
    <div class="tile empty">
      <span class="empty-ico"><LayoutGrid size={26} /></span>
      <h2 class="t-title">No apps yet</h2>
      <p class="t-sub">Pick something from the App Store. It is ready in a minute.</p>
      <button class="btn btn-primary" onclick={() => onNavigate('/apps')}>Open the App Store</button>
    </div>
  {:else}
    <div class="group">
      {#each apps as app (app.id)}
        {@const [label, tone] = STATES[app.state] || STATES.unknown}
        <div class="row app">
          <AppIcon id={app.id} name={app.name} size={42} />
          <div class="main">
            <span class="t-head truncate">{app.name}</span>
            <span class="t-caption truncate">{[app.version && `Version ${app.version}`, app.main_port && `Port ${app.main_port}`].filter(Boolean).join('  ·  ')}</span>
          </div>
          <span class="state t-caption">
            {#if busy[app.id]}<Loader size={14} class="spinner" />{busy[app.id] === 'update' ? 'Updating' : 'Working'}
            {:else}<span class="dot {tone}"></span>{label}{/if}
          </span>
          {#if app.state === 'running' && appURL(app)}
            <a class="btn btn-tinted btn-sm open" href={appURL(app)} target="_blank" rel="noopener noreferrer"><ExternalLink size={15} />Open</a>
          {/if}
          <div class="more">
            <button class="icon-btn" onclick={() => (menu = menu === app.id ? null : app.id)} aria-haspopup="menu" aria-expanded={menu === app.id} aria-label="Actions for {app.name}" disabled={!!busy[app.id]}>
              <Ellipsis size={19} />
            </button>
            <Popover open={menu === app.id} label="Actions for {app.name}">
              {#if app.state !== 'running'}<button class="pop-item" role="menuitem" onclick={() => act(app, 'start')}><Play size={16} />Start</button>{/if}
              {#if app.state !== 'stopped'}<button class="pop-item" role="menuitem" onclick={() => act(app, 'stop')}><Square size={16} />Stop</button>{/if}
              <button class="pop-item" role="menuitem" onclick={() => act(app, 'restart')}><RotateCw size={16} />Restart</button>
              <button class="pop-item" role="menuitem" onclick={() => act(app, 'update')}><Download size={16} />Update</button>
              <button class="pop-item" role="menuitem" onclick={(e) => showLogs(e, app)}><ScrollText size={16} />Logs</button>
              <button class="pop-item danger" role="menuitem" onclick={(e) => askRemove(e, app)}><Trash2 size={16} />Uninstall</button>
            </Popover>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<svelte:window onpointerdown={(e) => { if (menu && !e.target.closest('.more')) menu = null; }} />

<Dialog bind:open={logsOpen} label="Logs" {origin} wide>
  <div class="dlg">
    <div class="dlg-head">
      <h2 class="t-title">Logs of {logsFor?.name}</h2>
      <span class="pill"><span class="dot ok pulse"></span>Live</span>
    </div>
    {#if logsLoading}
      <p class="t-sub busy"><Loader size={16} class="spinner" />Loading</p>
    {:else}
      <pre class="logs mono" bind:this={logBox} onscroll={onLogScroll}>{logs}</pre>
    {/if}
    <div class="buttons"><button class="btn btn-gray" data-close onclick={() => (logsOpen = false)}>Close</button></div>
  </div>
</Dialog>

<Dialog bind:open={removeOpen} label="Uninstall" {origin} locked={removing}>
  <div class="dlg">
    <h2 class="t-title">Uninstall {removeFor?.name}?</h2>
    <p class="t-sub">The app's containers are stopped and removed.</p>
    <label class="row-field">
      <span class="field-text"><span class="field-label">Also delete its data</span><span class="t-caption">Settings, databases and files of this app. This cannot be undone.</span></span>
      <input class="switch" type="checkbox" bind:checked={deleteData} disabled={removing} />
    </label>
    {#if removing}<JobLog lines={removeLines} />{/if}
    <div class="buttons">
      <button class="btn btn-gray" data-close onclick={() => (removeOpen = false)} disabled={removing}>Cancel</button>
      <button class="btn btn-danger" onclick={remove} disabled={removing}>
        {#if removing}<Loader size={16} class="spinner" />Removing{:else}Uninstall{/if}
      </button>
    </div>
  </div>
</Dialog>

<style>
  .page { display: grid; gap: 1.5rem; }
  .page-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; flex-wrap: wrap; padding-top: 0.5rem; }
  h1, h2, p { margin: 0; }
  .page-head p { margin-top: 0.375rem; }
  .skeleton.sq { width: 42px; height: 42px; border-radius: 10px; }
  .skeleton.line { display: block; width: 40%; height: 0.8rem; }
  .group { overflow: visible; }
  .app { gap: 0.875rem; }
  .main { display: grid; min-width: 0; flex: 1; }
  .state { display: inline-flex; align-items: center; gap: 0.4rem; white-space: nowrap; min-width: 6.5rem; }
  .more { position: relative; }
  .pop-item {
    display: flex; align-items: center; gap: 0.625rem; width: 100%; min-height: 2.5rem; padding: 0 0.625rem;
    border: 0; background: none; cursor: pointer; text-align: left; font-weight: 500; border-radius: 0.625rem;
    transition: background-color 150ms ease;
  }
  .pop-item:hover { background: var(--fill); }
  .pop-item.danger { color: var(--error); }
  .empty { display: grid; justify-items: center; gap: 0.5rem; text-align: center; padding: 2.5rem 1.25rem; }
  .empty .btn { margin-top: 0.75rem; }
  .empty-ico { display: grid; place-items: center; width: 3.5rem; height: 3.5rem; border-radius: 1.1rem; background: var(--accent-fill); color: var(--accent); margin-bottom: 0.25rem; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .dlg-head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
  .logs {
    margin: 0; height: min(28rem, 55svh); overflow: auto; padding: 0.875rem 1rem; border-radius: var(--r-control);
    background: var(--term-bg); box-shadow: inset 0 0 0 1px var(--hairline); font-size: 0.75rem; line-height: 1.5;
    white-space: pre-wrap; word-break: break-word;
  }
  .busy { display: flex; align-items: center; gap: 0.5rem; }
  .row-field { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 0.75rem 0.875rem; border-radius: var(--r-control); background: var(--fill); cursor: pointer; }
  .field-text { display: grid; gap: 0.125rem; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; }

  @media (max-width: 639px) {
    .app { flex-wrap: wrap; }
    .state { min-width: 0; order: 3; margin-left: calc(42px + 0.875rem); }
    .open { order: 4; }
  }
</style>
