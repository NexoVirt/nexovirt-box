<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { runJob, appURL, versionLabel, isDigest, jobFollower, BUSY_LABEL } from '$lib/jobs.js';
  import Credentials from '$lib/Credentials.svelte';
  import KeyRound from '@lucide/svelte/icons/key-round';
  import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
  import { toast } from '$lib/toast.svelte.js';
  import AppIcon from '$lib/AppIcon.svelte';
  import { installs } from '$lib/installs.svelte.js';
  import Dialog from '$lib/Dialog.svelte';
  import Popover from '$lib/Popover.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import DockerSetup from './DockerSetup.svelte';
  import OwnAppDialog from './OwnAppDialog.svelte';
  import AppBackups from './AppBackups.svelte';
  import CatalogUpdateDialog from './CatalogUpdateDialog.svelte';
  import { withProof } from '$lib/proof.svelte.js';
  import FileDiff from '@lucide/svelte/icons/file-diff';
  import Archive from '@lucide/svelte/icons/archive';
  import FilePen from '@lucide/svelte/icons/file-pen';
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
  import Globe from '@lucide/svelte/icons/globe';
  import Undo2 from '@lucide/svelte/icons/undo-2';
  import CalendarClock from '@lucide/svelte/icons/calendar-clock';

  let { onSessionLost, onNavigate } = $props();

  const STATES = {
    running: ['Running', 'ok'], partial: ['Partly running', 'warn'], restarting: ['Keeps restarting', 'warn'],
    stopped: ['Stopped', ''], exited: ['Stopped with an error', 'error'], not_created: ['Not started yet', ''],
    interrupted: ['Install interrupted', 'warn'], unknown: ['Unknown', ''],
  };
  const DONE = { start: 'started', stop: 'stopped', restart: 'restarted', update: 'updated', install: 'installed', retry: 'installed', uninstall: 'removed', backup: 'backed up', restore: 'restored', verify: 'backup checked', edit: 'redeployed', rollback: 'rolled back' };
  // Down although nobody stopped it.
  const DOWN = ['exited', 'partial', 'restarting', 'stopped', 'not_created'];

  let apps = $state(null);
  let error = $state('');
  let docker = $state(null);
  let busy = $state({}); // id -> action started on this page
  let bg = $state({}); // id -> action running in the background (after a reload, another tab)
  let credsFor = $state(null), credsOpen = $state(false);
  let menu = $state(null); // id of the open menu
  // Uninstall dialog
  let removeFor = $state(null);
  let removeOpen = $state(false);
  let deleteData = $state(false);
  let removeLines = $state([]);
  let removing = $state(false);
  let origin = $state(null);
  // Updates and domains
  let updates = $state({ apps: {} });
  let checking = $state(false);
  let proxy = $state(null);
  let ownOpen = $state(false), ownId = $state('');
  let backupsFor = $state(null), backupsOpen = $state(false);
  let catFor = $state(null), catOpen = $state(false);
  let socketFor = $state(null), socketOpen = $state(false), socketBusy = $state(false); // { app, op }
  let rollFor = $state(null), rollOpen = $state(false);
  let noCopyFor = $state(null), noCopyOpen = $state(false), noCopyMsg = $state('');
  let auto = $state({}), autoFor = $state(null), autoOpen = $state(false), autoForm = $state({ on: false, from: '03:00', to: '05:00' }), autoBusy = $state(false);
  let domainFor = $state(null), domainOpen = $state(false), domain = $state(''), domainBusy = $state(false), domainError = $state('');

  const followJobs = jobFollower((job, id) => {
    const name = apps?.find((x) => x.id === id)?.name || 'The app';
    if ((job.kind === 'install' || job.kind === 'retry') && installs[id]) return load(); // its own toast
    if (job.status === 'done') toast(`${name} ${DONE[job.kind] || 'is ready'}.`, 'success');
    else if (job.http !== 401) toast(job.error, 'error', 8000);
    load();
  }, () => busy);

  async function load() {
    const [a, d, j] = await Promise.all([api('/api/apps'), api('/api/docker'), followJobs()]);
    if (a.status === 401) return onSessionLost();
    if (!a.ok) error = a.error;
    else { error = ''; apps = a.data; openWanted(); }
    if (d.ok) docker = d.data;
    bg = j;
  }

  const busyOf = (app) => busy[app.id] || bg[app.id] || app.busy || '';
  const unexpected = $derived((apps || []).filter((a) => !busyOf(a) && !a.stopped && DOWN.includes(a.state)));

  function stateText(app) {
    if (app.state === 'exited' && app.exit_code) return `Stopped with an error (code ${app.exit_code})`;
    return (STATES[app.state] || STATES.unknown)[0];
  }

  // /apps/installed?app=<id>[&do=login|backups|uninstall]: from the desktop tile menu. Shows that app
  // (scrolled to and marked) and opens the asked dialog once.
  let wanted = new URLSearchParams(location.search);
  let marked = $state('');
  function openWanted() {
    const id = wanted.get('app');
    if (!id) return;
    const app = apps.find((x) => x.id === id);
    const what = wanted.get('do');
    wanted = new URLSearchParams();
    if (!app) return;
    marked = id;
    setTimeout(() => document.getElementById('app-' + id)?.scrollIntoView({ block: 'center', behavior: 'smooth' }), 50);
    const at = { clientX: innerWidth / 2, clientY: innerHeight / 2 };
    if (what === 'login' && !app.custom) showCreds(at, app);
    else if (what === 'backups') openBackups(at, app);
    else if (what === 'uninstall') askRemove(at, app);
  }

  export function openOwn(e, app) {
    menu = null;
    origin = e ? { x: e.clientX, y: e.clientY } : null;
    ownId = app?.id || '';
    ownOpen = true;
  }

  function openBackups(e, app) {
    menu = null;
    origin = { x: e.clientX, y: e.clientY };
    backupsFor = app;
    backupsOpen = true;
  }

  async function retry(app) {
    if (app.custom) return act(app, 'start'); // own apps resume with their own files
    menu = null;
    busy = { ...busy, [app.id]: 'retry' };
    const job = await runJob(`/api/apps/${app.id}/install`, { retry: true });
    const { [app.id]: _, ...rest } = busy;
    busy = rest;
    if (job.http === 401) return onSessionLost();
    if (job.status === 'done') toast(`${app.name} is installed and running.`, 'success');
    else toast(job.error, 'error', 8000);
    load();
  }

  function showCreds(e, app) {
    menu = null;
    origin = { x: e.clientX, y: e.clientY };
    credsFor = app;
    credsOpen = true;
  }

  async function loadExtras() {
    const [u, p, au] = await Promise.all([api('/api/updates'), api('/api/proxy'), api('/api/apps/auto-updates')]);
    if (u.ok) updates = u.data;
    if (p.ok) proxy = p.data;
    if (au.ok) auto = au.data || {};
  }

  function askRollback(e, app) {
    menu = null;
    origin = { x: e.clientX, y: e.clientY };
    rollFor = app;
    rollOpen = true;
  }

  function openAuto(e, app) {
    menu = null;
    origin = { x: e.clientX, y: e.clientY };
    autoFor = app;
    autoForm = { on: false, from: '03:00', to: '05:00', ...(auto[app.id] || {}) };
    autoOpen = true;
  }

  async function saveAuto(e) {
    e.preventDefault();
    autoBusy = true;
    const res = await api(`/api/apps/${autoFor.id}/auto-update`, { method: 'POST', body: autoForm });
    autoBusy = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) return toast(res.error, 'error');
    auto = { ...auto, [autoFor.id]: res.data };
    autoOpen = false;
    toast(res.data.on ? `${autoFor.name} updates itself between ${res.data.from} and ${res.data.to}.` : `Automatic updates for ${autoFor.name} are off.`, 'success');
  }

  export async function checkUpdates() {
    checking = true;
    const res = await api('/api/updates/check', { method: 'POST' });
    checking = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) return toast(res.error, 'error');
    updates = res.data;
    const n = Object.values(res.data.apps).filter((u) => u.available).length;
    toast(n ? `${n} app${n > 1 ? 's have' : ' has'} an update.` : 'All apps are up to date.', n ? 'info' : 'success');
  }

  // The proxied address when the app has a domain, else the direct port.
  function openURL(app) {
    if (app.domain && proxy?.enabled && proxy.running) {
      const port = proxy.config.https_port;
      return `https://${app.domain}${port === 443 ? '' : ':' + port}`;
    }
    return appURL(app);
  }

  function editDomain(e, app) {
    menu = null;
    origin = { x: e.clientX, y: e.clientY };
    domainFor = app;
    domain = app.domain || '';
    domainError = '';
    domainOpen = true;
  }

  async function saveDomain(e) {
    e.preventDefault();
    domainBusy = true;
    domainError = '';
    const res = await api(`/api/apps/${domainFor.id}/domain`, { method: 'POST', body: { domain } });
    domainBusy = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) {
      domainError = res.error;
      return;
    }
    domainOpen = false;
    toast(res.data.domain ? res.data.note : 'The domain was removed.', res.data.tls === 'internal' ? 'info' : 'success', 9000);
    load();
  }

  async function act(app, op, allow = false, extra = {}) {
    menu = null;
    busy = { ...busy, [app.id]: op };
    const job = await (allow ? withProof : (f) => f())(() => runJob(`/api/apps/${app.id}/action`, { op, allow_docker_socket: allow, ...extra }));
    const { [app.id]: _, ...rest } = busy;
    busy = rest;
    if (job.cancelled) return;
    if (job.http === 401) return onSessionLost();
    if (job.status === 'done') toast(`${app.name} ${DONE[op]}.`, 'success');
    else if (job.error?.startsWith('needs_docker_socket:')) { socketFor = { app, op }; socketOpen = true; }
    else if (op === 'update' && job.error?.startsWith('Not enough free space for the rollback copy')) { noCopyFor = app; noCopyMsg = job.error; noCopyOpen = true; }
    else toast(job.error, 'error', 8000);
    if (op === 'update' && job.status === 'done') updates.apps[app.id] = { available: false };
    load();
  }

  async function allowSocket() {
    socketBusy = true;
    await act(socketFor.app, socketFor.op, true);
    socketBusy = false;
    socketOpen = false;
  }

  function openCatalog(e, app) {
    menu = null;
    origin = e ? { x: e.clientX, y: e.clientY } : null;
    catFor = app;
    catOpen = true;
  }

  function showLogs(e, app) {
    menu = null;
    onNavigate(`/apps/${app.id}/logs`);
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
    loadExtras();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  });
</script>

<div class="page">

  {#if docker && (!docker.installed || !docker.running)}
    <DockerSetup {docker} onDone={load} />
  {/if}

  {#each unexpected as app (app.id)}
    <div class="notice down" role="status">
      <CircleAlert size={18} />
      <div class="down-text">
        <span><strong>{app.name}</strong> {app.state === 'not_created' ? 'is not running yet.' : app.state === 'restarting' ? 'keeps restarting.' : app.exit_code ? `stopped unexpectedly (exit code ${app.exit_code}).` : 'is not running, but nobody stopped it.'}</span>
        {#if app.last_lines?.length}<pre class="tail mono">{app.last_lines.join('\n')}</pre>{/if}
        <span class="down-btns">
          <button class="btn btn-primary btn-sm" onclick={() => act(app, 'start')}><Play size={15} />Start</button>
          {#if app.state !== 'not_created'}<button class="btn btn-gray btn-sm" onclick={(e) => showLogs(e, app)}><ScrollText size={15} />View logs</button>{/if}
        </span>
      </div>
    </div>
  {/each}

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else if !apps}
    <div class="list">{#each Array(3) as _}<div class="row"><span class="skeleton sq"></span><span class="skeleton line"></span></div>{/each}</div>
  {:else if !apps.length}
    <div class="empty-state">
      <span class="ico xl accent"><LayoutGrid /></span>
      <h2 class="t-title">No apps yet</h2>
      <p class="t-sub">Pick something from the App Store. It is ready in a minute.</p>
      <button class="btn btn-primary" onclick={() => onNavigate('/apps')}>Open the App Store</button>
    </div>
  {:else}
    <div class="list">
      {#each apps as app (app.id)}
        {@const [, tone] = STATES[app.state] || STATES.unknown}
        {@const working = busyOf(app)}
        {@const where = app.domain || (app.main_port && `Port ${app.main_port}`) || ''}
        {@const version = !app.custom && app.version ? versionLabel(app.version) : ''}
        <div class="row app" class:marked={marked === app.id} id="app-{app.id}">
          <AppIcon id={app.id} name={app.name} size={42} icon={app.icon} />
          <div class="main">
            <span class="t-head truncate">{app.name}</span>
            <span class="t-caption facts">
              {#if app.custom}<span>Own app</span>{:else if version}<span class="ver" title={isDigest(app.version) ? `Image digest ${app.version}` : `Version ${app.version}`}>Version {version}</span>{/if}
              {#if where && (app.custom || version)}<span aria-hidden="true">·</span>{/if}
              {#if where}<span class="truncate">{where}</span>{/if}
            </span>
          </div>
          <span class="state t-caption">
            {#if working}<Loader size={14} class="spinner" />{BUSY_LABEL[working] || 'Working'}
            {:else}<span class="dot {tone}"></span><span class="truncate">{stateText(app)}</span>{/if}
          </span>
          <div class="acts">
            {#if working === 'install' || working === 'retry'}
              <button class="btn btn-tinted btn-sm" onclick={() => onNavigate(`/apps?app=${app.id}`)}><ScrollText size={15} /><span class="open-text">Show progress</span></button>
            {/if}
            {#if app.state === 'interrupted' && !working}
              <button class="btn btn-primary btn-sm" onclick={() => retry(app)}><RotateCcw size={15} /><span class="open-text">Retry install</span></button>
            {/if}
            {#if updates.apps[app.id]?.available && !working}
              <button class="btn btn-primary btn-sm" onclick={() => act(app, 'update')} title="A newer image is available: {updates.apps[app.id].images.join(', ')}"><Download size={15} /><span class="open-text">Update</span></button>
            {/if}
            {#if app.catalog_update && !working}
              <button class="btn btn-tinted btn-sm" onclick={(e) => openCatalog(e, app)} title="The app store has a newer definition for this app"><FileDiff size={15} /><span class="open-text">Catalog update available</span></button>
            {/if}
            {#if app.state === 'running' && openURL(app)}
              <a class="btn btn-tinted btn-sm open" href={openURL(app)} target="_blank" rel="noopener noreferrer" aria-label="Open {app.name}"><ExternalLink size={15} /><span class="open-text">Open</span></a>
            {/if}
            <div class="more">
              <button class="icon-btn" onclick={() => (menu = menu === app.id ? null : app.id)} aria-haspopup="menu" aria-expanded={menu === app.id} aria-label="Actions for {app.name}" disabled={!!working}>
                <Ellipsis size={19} />
              </button>
              <Popover open={menu === app.id} label="Actions for {app.name}">
                {#if app.state === 'interrupted'}<button class="pop-item" role="menuitem" onclick={() => retry(app)}><RotateCcw size={16} />Retry install</button>
                {:else if app.state !== 'running'}<button class="pop-item" role="menuitem" onclick={() => act(app, 'start')}><Play size={16} />Start</button>{/if}
                {#if !['stopped', 'exited', 'not_created', 'interrupted'].includes(app.state)}<button class="pop-item" role="menuitem" onclick={() => act(app, 'stop')}><Square size={16} />Stop</button>{/if}
                {#if app.state !== 'interrupted' && app.state !== 'not_created'}
                  <button class="pop-item" role="menuitem" onclick={() => act(app, 'restart')}><RotateCw size={16} />Restart</button>
                  <button class="pop-item" role="menuitem" onclick={() => act(app, 'update')}><Download size={16} />Update</button>
                  <button class="pop-item" role="menuitem" onclick={(e) => openAuto(e, app)}><CalendarClock size={16} />Automatic updates</button>
                {/if}
                {#if app.last_update?.rollback}<button class="pop-item" role="menuitem" onclick={(e) => askRollback(e, app)}><Undo2 size={16} />Roll back last update</button>{/if}
                {#if app.custom}<button class="pop-item" role="menuitem" onclick={(e) => openOwn(e, app)}><FilePen size={16} />Edit compose file</button>
                {:else}<button class="pop-item" role="menuitem" onclick={(e) => showCreds(e, app)}><KeyRound size={16} />Login details</button>{/if}
                <button class="pop-item" role="menuitem" onclick={(e) => openBackups(e, app)}><Archive size={16} />Backups</button>
                <button class="pop-item" role="menuitem" onclick={(e) => showLogs(e, app)}><ScrollText size={16} />Logs</button>
                <button class="pop-item" role="menuitem" onclick={(e) => editDomain(e, app)}><Globe size={16} />Domain and HTTPS</button>
                <button class="pop-item danger" role="menuitem" onclick={(e) => askRemove(e, app)}><Trash2 size={16} />Uninstall</button>
              </Popover>
            </div>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<svelte:window onpointerdown={(e) => { if (menu && !e.target.closest('.more')) menu = null; }} />

<OwnAppDialog bind:open={ownOpen} editId={ownId} {origin} {onSessionLost} onDone={load} />
<CatalogUpdateDialog bind:open={catOpen} app={catFor} {origin} {onSessionLost} onDone={load} />

<Dialog bind:open={socketOpen} label="Docker access" locked={socketBusy}>
  <div class="dlg">
    <h2 class="t-title">Allow Docker access for {socketFor?.app.name}?</h2>
    <p class="t-sub">This app uses the Docker socket, which means full control of the server. Box asks once and remembers your answer. You confirm it with your password.</p>
    <div class="buttons">
      <button class="btn btn-gray" data-close onclick={() => (socketOpen = false)} disabled={socketBusy}>Cancel</button>
      <button class="btn btn-primary" onclick={allowSocket} disabled={socketBusy}>{#if socketBusy}<Loader size={16} class="spinner" />Starting{:else}Allow and start{/if}</button>
    </div>
  </div>
</Dialog>

<Dialog bind:open={rollOpen} label="Roll back" {origin}>
  <div class="dlg">
    <h2 class="t-title">Roll back {rollFor?.name}?</h2>
    <p class="t-sub">Box puts back the images and the data from just before the last update{rollFor?.last_update ? ` (${new Date(rollFor.last_update.at * 1000).toLocaleString()})` : ''}. Changes the app made since then are lost.</p>
    <div class="buttons">
      <button class="btn btn-gray" data-close onclick={() => (rollOpen = false)}>Cancel</button>
      <button class="btn btn-danger" onclick={() => { rollOpen = false; act(rollFor, 'rollback'); }}>Roll back</button>
    </div>
  </div>
</Dialog>

<Dialog bind:open={noCopyOpen} label="Update without rollback copy">
  <div class="dlg">
    <h2 class="t-title">Update {noCopyFor?.name} without a rollback copy?</h2>
    <p class="t-sub">{noCopyMsg} Without the copy, Box cannot go back automatically if the new version does not start.</p>
    <div class="buttons">
      <button class="btn btn-gray" data-close onclick={() => (noCopyOpen = false)}>Cancel</button>
      <button class="btn btn-primary" onclick={() => { noCopyOpen = false; act(noCopyFor, 'update', false, { no_copy: true }); }}>Update anyway</button>
    </div>
  </div>
</Dialog>

<Dialog bind:open={autoOpen} label="Automatic updates" {origin} locked={autoBusy}>
  <form class="dlg" onsubmit={saveAuto}>
    <h2 class="t-title">Automatic updates for {autoFor?.name}</h2>
    <p class="t-sub">When a newer image is found, Box updates the app inside this daily window (server time). It saves a rollback copy first and goes back on its own when the app does not answer afterwards.</p>
    <label class="row-field"><span class="field-label">Update automatically</span><input class="switch" type="checkbox" bind:checked={autoForm.on} disabled={autoBusy} /></label>
    <div class="times">
      <label class="field"><span class="field-label">From</span><input class="input" type="time" bind:value={autoForm.from} required disabled={autoBusy || !autoForm.on} /></label>
      <label class="field"><span class="field-label">Until</span><input class="input" type="time" bind:value={autoForm.to} required disabled={autoBusy || !autoForm.on} /></label>
    </div>
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => (autoOpen = false)} disabled={autoBusy}>Cancel</button>
      <button class="btn btn-primary" disabled={autoBusy}>{#if autoBusy}<Loader size={16} class="spinner" />Saving{:else}Save{/if}</button>
    </div>
  </form>
</Dialog>

<AppBackups bind:open={backupsOpen} app={backupsFor} apps={apps || []} {origin} {onSessionLost} {onNavigate} onDone={load} />

<Dialog bind:open={credsOpen} label="Login details" {origin}>
  <div class="dlg">
    <h2 class="t-title">Login details for {credsFor?.name}</h2>
    {#if credsOpen && credsFor}<Credentials id={credsFor.id} {onSessionLost} />{/if}
    <div class="buttons"><button class="btn btn-gray" data-close onclick={() => (credsOpen = false)}>Close</button></div>
  </div>
</Dialog>

<Dialog bind:open={domainOpen} label="Domain" {origin} locked={domainBusy}>
  <form class="dlg" onsubmit={saveDomain}>
    <h2 class="t-title">Domain for {domainFor?.name}</h2>
    {#if !proxy?.enabled}
      <p class="t-sub">Turn on the reverse proxy in Settings first. It serves your apps with HTTPS under their own domain.</p>
      <div class="buttons">
        <button type="button" class="btn btn-gray" data-close onclick={() => (domainOpen = false)}>Close</button>
        <button type="button" class="btn btn-primary" onclick={() => { domainOpen = false; onNavigate('/settings/network'); }}>Open Settings</button>
      </div>
    {:else}
      <p class="t-sub">Point a DNS record of the domain to this server, then save. Box gets a free Let's Encrypt certificate when it can, otherwise a local one.</p>
      {#if domainError}<div class="notice" role="alert">{domainError}</div>{/if}
      <input class="input" bind:value={domain} placeholder="app.example.com" aria-label="Domain" spellcheck="false" autocapitalize="none" />
      {#if domainFor?.domain}<p class="t-caption">Leave empty and save to remove the domain.</p>{/if}
      <div class="buttons">
        <button type="button" class="btn btn-gray" data-close onclick={() => (domainOpen = false)} disabled={domainBusy}>Cancel</button>
        <button class="btn btn-primary" disabled={domainBusy}>{#if domainBusy}<Loader size={16} class="spinner" />Checking DNS{:else}Save{/if}</button>
      </div>
    {/if}
  </form>
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
  h2, p { margin: 0; }
  .times { display: grid; grid-template-columns: 1fr 1fr; gap: 0.75rem; }
  .skeleton.sq { width: 42px; height: 42px; border-radius: 10px; }
  .skeleton.line { display: block; width: 40%; height: 0.8rem; }
  /* A plain list on the window surface: rows split by hairlines, nothing behind them. */
  /* One grid for all rows (subgrid), so states and actions line up even when a row has no Open. */
  .list { display: grid; grid-template-columns: 42px minmax(0, 1fr) 9.5rem max-content; column-gap: 0.875rem; }
  .list .row { grid-column: 1 / -1; padding: 0.75rem 0.25rem; min-height: 4.25rem; }
  .list .row.app { display: grid; grid-template-columns: subgrid; align-items: center; }
  .list .row + .row::before { left: calc(42px + 1.125rem); }
  .facts { display: flex; align-items: center; gap: 0.375rem; min-width: 0; white-space: nowrap; }
  .facts > span { flex-shrink: 0; }
  .facts > .truncate { flex-shrink: 1; min-width: 0; }
  .ver { flex-shrink: 1 !important; min-width: 0; max-width: 18rem; overflow: hidden; text-overflow: ellipsis; }
  .app { gap: 0.875rem; border-radius: var(--r-control); }
  .app.marked { background: var(--accent-fill); transition: background-color 600ms ease; }
  .main { display: grid; gap: 0.125rem; min-width: 0; flex: 1; }
  .state { display: inline-flex; align-items: center; gap: 0.4rem; white-space: nowrap; min-width: 0; }
  .acts { display: flex; align-items: center; justify-content: flex-end; gap: 0.5rem; flex-shrink: 0; }
  .more { position: relative; }
  .pop-item {
    display: flex; align-items: center; gap: 0.625rem; width: 100%; min-height: 2.5rem; padding: 0 0.625rem;
    border: 0; background: none; cursor: pointer; text-align: left; font-weight: 500; border-radius: 0.625rem;
    transition: background-color 150ms ease;
  }
  .pop-item:hover { background: var(--fill); }
  .pop-item.danger { color: var(--error); }
  .empty-state .btn { margin-top: 0.75rem; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .dlg-head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
  .busy { display: flex; align-items: center; gap: 0.5rem; }
  .down { align-items: flex-start; margin-bottom: 0.75rem; }
  .down-text { display: grid; gap: 0.5rem; min-width: 0; flex: 1; }
  .down-btns { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .tail { margin: 0; padding: 0.5rem 0.625rem; border-radius: 0.5rem; background: var(--fill); color: var(--text-2); font-size: 0.75rem; line-height: 1.45; white-space: pre-wrap; word-break: break-word; max-height: 7.5rem; overflow: auto; }
  .row-field { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 0.75rem 0.875rem; border-radius: var(--r-control); background: var(--fill); cursor: pointer; }
  .field-text { display: grid; gap: 0.125rem; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; }

  @media (max-width: 639px) {
    .list { grid-template-columns: 42px minmax(0, 1fr) max-content; row-gap: 0.375rem; }
    .main { grid-column: 2 / -1; }
    .state { grid-column: 2; }
    .open { width: 2.25rem; padding: 0; }
    .open-text { display: none; }
  }
</style>
