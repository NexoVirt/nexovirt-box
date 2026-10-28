<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { withProof } from '$lib/proof.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes, ago } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import DataTable from '$lib/DataTable.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import AppIcon from '$lib/AppIcon.svelte';
  import RestoreDialog from './RestoreDialog.svelte';
  import TargetDialog from './TargetDialog.svelte';
  import KeyRound from '@lucide/svelte/icons/key-round';
  import HardDrive from '@lucide/svelte/icons/hard-drive';
  import Cloud from '@lucide/svelte/icons/cloud';
  import Server from '@lucide/svelte/icons/server';
  import CalendarClock from '@lucide/svelte/icons/calendar-clock';
  import Plus from '@lucide/svelte/icons/plus';
  import Play from '@lucide/svelte/icons/play';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Copy from '@lucide/svelte/icons/copy';
  import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import Pencil from '@lucide/svelte/icons/pencil';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import Archive from '@lucide/svelte/icons/archive';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import History from '@lucide/svelte/icons/history';
  import Settings2 from '@lucide/svelte/icons/settings-2';

  let { onSessionLost } = $props();

  const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
  const KIND_ICON = { local: HardDrive, s3: Cloud, pbs: Server };

  let data = $state(null), error = $state(''), apps = $state([]);
  let origin = $state(null);
  // key
  let keyOpen = $state(false), keyMode = $state('create'), keyText = $state(''), keyBusy = $state(false), keyError = $state(''), secret = $state(''), saved = $state(false);
  // targets
  let targetOpen = $state(false), targetEdit = $state(null);
  // schedules
  let schedOpen = $state(false), sched = $state(null), schedBusy = $state(false), schedError = $state('');
  // run now
  let runOpen = $state(false), runApp = $state('box'), runTarget = $state(''), running = $state(false), runLines = $state([]);
  // restore and verify
  let restoreOpen = $state(false), restorePreset = $state(null);
  let verifying = $state({});
  // tabs (#overview, #archives, ... in the URL)
  const TABS = [
    { value: 'overview', label: 'Overview' },
    { value: 'archives', label: 'Backups' },
    { value: 'schedules', label: 'Schedules' },
    { value: 'targets', label: 'Targets' },
    { value: 'activity', label: 'Activity' },
  ];
  let tab = $state(TABS.some((t) => t.value === location.hash.slice(1)) ? location.hash.slice(1) : 'overview');
  $effect(() => { if (location.hash.slice(1) !== tab) history.replaceState(null, '', '#' + tab); });
  // archives on one target
  let archTarget = $state(''), archives = $state(null), archError = $state(''), archApp = $state(''), archSeq = 0;
  let histSort = $state({ key: 'at', dir: 'desc' }), histStatus = $state('');

  let targets = $derived(data?.targets?.targets || []);
  let appName = (id) => (id === 'box' ? 'Box settings' : id === '*' ? 'All apps and Box settings' : apps.find((a) => a.id === id)?.name || id);
  let targetName = (id) => targets.find((t) => t.id === id)?.name || 'Removed target';

  async function load() {
    const [b, a] = await Promise.all([api('/api/backups'), api('/api/apps')]);
    if (b.status === 401) return onSessionLost();
    if (!b.ok) { error = b.error; return; }
    error = '';
    data = b.data;
    if (a.ok) apps = a.data;
    if (!runTarget && targets.length) runTarget = targets[0].id;
    if (!targets.some((t) => t.id === archTarget)) archTarget = targets[0]?.id || '';
  }

  function at(e) { origin = e ? { x: e.clientX, y: e.clientY } : null; }

  // ---- key ----
  function openKey(e, mode) { at(e); keyMode = mode; keyText = ''; keyError = ''; secret = ''; saved = false; keyOpen = true; }
  async function saveKey(e) {
    e?.preventDefault();
    keyBusy = true;
    keyError = '';
    const replace = data.key.configured;
    const res = await withProof(() => api('/api/backups/key', { method: 'POST', body: { op: keyMode, key: keyText, confirm: replace ? 'REPLACE' : '' } }));
    keyBusy = false;
    if (res.cancelled) return;
    if (!res.ok) { keyError = res.error; return; }
    if (res.data.secret) secret = res.data.secret;
    else { keyOpen = false; toast('The backup key was imported.', 'success'); }
    load();
  }
  function copySecret() { navigator.clipboard?.writeText(secret).then(() => toast('Copied.', 'success')); }

  // ---- targets ----
  function addTarget(e) { at(e); targetEdit = null; targetOpen = true; }
  function editTarget(e, t) { at(e); targetEdit = t; targetOpen = true; }
  async function deleteTarget(t) {
    const res = await withProof(() => api(`/api/backups/targets/${t.id}/delete`, { method: 'POST' }));
    if (res.cancelled) return;
    if (!res.ok) return toast(res.error, 'error', 7000);
    toast(`${t.name} was removed. Backups already stored there stay.`, 'success');
    load();
  }

  // ---- schedules ----
  function openSched(e, s) {
    at(e);
    schedError = '';
    sched = s ? { ...s, keep: { ...s.keep } } : { app: '*', target: targets[0]?.id || '', freq: 'daily', weekday: 0, time: '03:30', keep: { last: 7, daily: 7, weekly: 4 }, enabled: true };
    schedOpen = true;
  }
  async function saveSched(e) {
    e.preventDefault();
    schedBusy = true;
    const s = { ...sched, weekday: +sched.weekday, keep: { last: +sched.keep.last || 0, daily: +sched.keep.daily || 0, weekly: +sched.keep.weekly || 0 } };
    const res = await api('/api/backups/schedules', { method: 'POST', body: s });
    schedBusy = false;
    if (!res.ok) { schedError = res.error; return; }
    schedOpen = false;
    toast('Schedule saved.', 'success');
    load();
  }
  async function toggleSched(s) {
    await api('/api/backups/schedules', { method: 'POST', body: { ...s, enabled: !s.enabled } });
    load();
  }
  async function deleteSched(s) {
    await api(`/api/backups/schedules/${s.id}/delete`, { method: 'POST' });
    toast('Schedule deleted.', 'success');
    load();
  }
  function schedText(s) {
    const when = s.freq === 'daily' ? `Every day at ${s.time}` : `Every ${DAYS[s.weekday]} at ${s.time}`;
    return when;
  }
  function keepText(k) {
    const p = [];
    if (k.last) p.push(`last ${k.last}`);
    if (k.daily) p.push(`${k.daily} daily`);
    if (k.weekly) p.push(`${k.weekly} weekly`);
    return p.length ? 'Keeps ' + p.join(', ') : 'Keeps every backup';
  }

  // ---- run now ----
  function openRun(e, app = 'box') { at(e); runApp = app; runLines = []; runOpen = true; }
  async function runNow() {
    running = true;
    runLines = [];
    const job = await runJob('/api/backups/run', { app: runApp, target: runTarget }, (j) => (runLines = j.lines));
    running = false;
    if (job.http === 401) return onSessionLost();
    if (job.status === 'done') { runOpen = false; toast(`Backup of ${appName(runApp)} saved (${bytes(job.result?.size || 0)}).`, 'success'); }
    else toast(job.error, 'error', 9000);
    load();
  }

  // ---- history actions ----
  async function verify(r) {
    verifying = { ...verifying, [r.id]: true };
    toast(`Checking ${r.name}. This reads the whole backup.`, 'info');
    const job = await runJob('/api/backups/verify', { target: r.target, name: r.name });
    verifying = { ...verifying, [r.id]: false };
    if (job.status === 'done') toast(`${r.name} is complete and can be restored (${job.result.files} files).`, 'success', 7000);
    else toast(job.error, 'error', 9000);
    load();
  }
  // ---- archives ----
  async function loadArchives() {
    if (!archTarget) { archives = []; return; }
    const my = ++archSeq;
    archives = null;
    archError = '';
    const res = await api(`/api/backups/targets/${archTarget}/archives`);
    if (my !== archSeq) return;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) { archError = res.error; archives = []; return; }
    archives = res.data;
  }
  $effect(() => { if (tab === 'archives' && archTarget) loadArchives(); });
  async function deleteArchive(a) {
    const res = await withProof(() => api('/api/backups/delete', { method: 'POST', body: { target: archTarget, name: a.name } }));
    if (res.cancelled) return;
    if (!res.ok) return toast(res.error, 'error', 7000);
    toast('The backup was deleted.', 'success');
    loadArchives();
  }
  function browse(t) { archTarget = t.id; tab = 'archives'; }

  function restore(e, r) { at(e); restorePreset = r ? { target: r.target, name: r.name, app: r.app } : null; restoreOpen = true; }

  const STATUS = { ok: ['Done', 'ok'], failed: ['Failed', 'error'] };
  const TYPE = { local: 'Folder on this server', s3: 'S3 bucket', pbs: 'Proxmox Backup Server' };
  const when = new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
  const full = (t) => new Date(t * 1000).toLocaleString();
  function where(t) { return t.type === 'local' ? t.path : t.type === 's3' ? `${t.bucket}${t.prefix ? '/' + t.prefix : ''} on ${t.endpoint}` : t.repository; }
  function untilText(t) {
    const s = t - Date.now() / 1000;
    if (s < 3600) return `in ${Math.max(1, Math.round(s / 60))} min`;
    if (s < 86400) return `in ${Math.round(s / 3600)} h`;
    return `in ${Math.round(s / 86400)} days`;
  }

  // Overview numbers.
  let backups = $derived((data?.history || []).filter((r) => r.kind === 'backup'));
  let lastBackup = $derived(backups[0]);
  let lastOk = $derived(backups.find((r) => r.status === 'ok'));
  let nextSched = $derived((data?.schedules || []).filter((s) => s.enabled && s.next_run).sort((a, b) => a.next_run - b.next_run)[0]);
  let weekAgo = $derived(Date.now() / 1000 - 7 * 86400);
  let recentFailed = $derived((data?.history || []).filter((r) => r.status === 'failed' && r.at > weekAgo));
  let history = $derived(histStatus ? (data?.history || []).filter((r) => r.status === histStatus) : data?.history || []);

  const ARCH_COLS = [
    { key: 'app', label: 'App', primary: true, sortable: true, value: (a) => appName(a.app) },
    { key: 'at', label: 'Created', sortable: true, firstDir: 'desc' },
    { key: 'size', label: 'Size', align: 'right', sortable: true, firstDir: 'desc' },
    { key: 'name', label: 'File', mono: true, hide: 'md' },
  ];
  const SCHED_COLS = [
    { key: 'app', label: 'Backs up', primary: true, value: (s) => appName(s.app) },
    { key: 'when', label: 'When', value: (s) => schedText(s) },
    { key: 'target', label: 'To', value: (s) => targetName(s.target), hide: 'md' },
    { key: 'keep', label: 'Keeps', value: (s) => keepText(s.keep), hide: 'md' },
    { key: 'next_run', label: 'Next run', sortable: true },
    { key: 'enabled', label: 'On', align: 'right' },
  ];
  const TARGET_COLS = [
    { key: 'name', label: 'Name', primary: true, sortable: true },
    { key: 'type', label: 'Type', value: (t) => TYPE[t.type] || t.type, sortable: true },
    { key: 'where', label: 'Location', value: where, mono: true, hide: 'md' },
  ];
  const HIST_COLS = [
    { key: 'at', label: 'When', sortable: true, firstDir: 'desc' },
    { key: 'app', label: 'What', primary: true, value: (r) => appName(r.app), sortable: true },
    { key: 'kind', label: 'Action', value: (r) => KIND[r.kind] || r.kind },
    { key: 'target', label: 'Target', value: (r) => r.target_name || targetName(r.target), hide: 'md' },
    { key: 'size', label: 'Size', align: 'right', hide: 'md' },
    { key: 'seconds', label: 'Took', align: 'right', hide: 'md' },
    { key: 'status', label: 'Result', wrap: true },
  ];
  const histActions = (r) => r.kind === 'backup' && r.status === 'ok' && r.name ? [
    { label: 'Restore', icon: RotateCcw, run: () => restore(null, r) },
    { label: 'Check that it restores', icon: ShieldCheck, disabled: verifying[r.id], run: () => verify(r) },
  ] : [];

  const KIND = { backup: 'Backup', restore: 'Restore', verify: 'Check' };

  onMount(() => {
    load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  });
</script>

<div class="page fill">
  <header class="page-head compact">
    <div>
      <h1 class="t-large">Backups</h1>
      <p class="t-sub">Encrypted copies of your apps and of Box's settings.</p>
    </div>
    {#if data?.key.configured && targets.length}
      <div class="head-btns">
        <button class="btn btn-gray btn-sm" onclick={(e) => restore(e, null)}><RotateCcw size={15} />Restore</button>
        <button class="btn btn-primary btn-sm" onclick={(e) => openRun(e)}><Play size={15} />Back up now</button>
      </div>
    {/if}
  </header>

  <div class="tabs"><Segmented options={TABS} bind:value={tab} label="Backups sections" /></div>

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else if !data}
    <div class="stats">{#each Array(3) as _}<div class="tile stat"><span class="skeleton" style:height="3.5rem"></span></div>{/each}</div>
  {:else if tab === 'overview'}
    <div class="scroll">
      {#if !data.key.configured}
        <section class="tile intro">
          <span class="ico xl accent"><KeyRound /></span>
          <div class="intro-text">
            <h2 class="t-title">Set up the backup key</h2>
            <p class="t-sub">Every backup is encrypted with this key before it leaves the app. Box keeps a copy for one-click restores. Save it somewhere safe too: you need it to restore on another server.</p>
            <div class="btns">
              <button class="btn btn-primary" onclick={(e) => openKey(e, 'create')}>Create a key</button>
              <button class="btn btn-gray" onclick={(e) => openKey(e, 'import')}>I have a key</button>
            </div>
          </div>
        </section>
      {:else if !targets.length}
        <section class="tile intro">
          <span class="ico xl accent"><HardDrive /></span>
          <div class="intro-text">
            <h2 class="t-title">Choose where backups go</h2>
            <p class="t-sub">A folder on this server, an S3 bucket or a Proxmox Backup Server. Another machine is safer: a copy on the same disk does not survive a broken disk.</p>
            <div class="btns"><button class="btn btn-primary" onclick={(e) => { tab = 'targets'; addTarget(e); }}><Plus size={16} />Add a target</button></div>
          </div>
        </section>
      {/if}

      <div class="stats">
        <div class="tile stat">
          <span class="ico md" class:ok={lastBackup?.status === 'ok'} class:error={lastBackup?.status === 'failed'} class:accent={!lastBackup}>
            {#if lastBackup?.status === 'failed'}<CircleAlert />{:else if lastBackup}<CircleCheck />{:else}<Archive />{/if}
          </span>
          <span class="stat-text">
            <span class="t-caption">Last backup</span>
            {#if lastBackup}
              <span class="t-head" title={full(lastBackup.at)}>{lastBackup.status === 'ok' ? 'Done' : 'Failed'} {ago(lastBackup.at)}</span>
              <span class="t-caption truncate">{appName(lastBackup.app)}{lastBackup.size ? `, ${bytes(lastBackup.size)}` : ''}</span>
            {:else}<span class="t-head">None yet</span>{/if}
          </span>
        </div>
        <div class="tile stat">
          <span class="ico md accent"><CalendarClock /></span>
          <span class="stat-text">
            <span class="t-caption">Next scheduled</span>
            {#if nextSched}
              <span class="t-head" title={full(nextSched.next_run)}>{when.format(nextSched.next_run * 1000)}</span>
              <span class="t-caption truncate">{appName(nextSched.app)}, {untilText(nextSched.next_run)}</span>
            {:else}<span class="t-head">No schedule</span>{#if targets.length}<button class="link" onclick={(e) => { tab = 'schedules'; openSched(e); }}>Add one</button>{/if}{/if}
          </span>
        </div>
        <div class="tile stat">
          <span class="ico md accent"><History /></span>
          <span class="stat-text">
            <span class="t-caption">Last 7 days</span>
            <span class="t-head num">{backups.filter((r) => r.at > weekAgo && r.status === 'ok').length} backups</span>
            <span class="t-caption">{recentFailed.length ? `${recentFailed.length} failed` : 'Nothing failed'}</span>
          </span>
        </div>
      </div>

      {#if recentFailed.length}
        <div class="notice" role="alert">
          <CircleAlert size={18} />
          <span><strong>{appName(recentFailed[0].app)}</strong>: {recentFailed[0].error || 'The last run failed.'} <button class="link" onclick={() => { histStatus = 'failed'; tab = 'activity'; }}>Show what failed</button></span>
        </div>
      {/if}

      {#if data.key.configured}
        <section class="group">
          <div class="row">
            <span class="ico accent"><KeyRound /></span>
            <div class="grow"><span>Backup key</span><span class="t-caption mono truncate">Public part {data.key.recipient}</span></div>
            <button class="btn btn-gray btn-sm" onclick={(e) => openKey(e, 'import')}>Replace</button>
          </div>
          <div class="row">
            <span class="ico warn"><ShieldCheck /></span>
            <div class="grow"><span>Keep a copy of the secret key</span><span class="t-caption">Box showed it once when it was created. Without it, backups cannot be restored on another server. Keep it in a password manager, not on this server.</span></div>
          </div>
        </section>
      {/if}

      <section class="group">
        <button class="row link-row" onclick={() => (tab = 'archives')}>
          <span class="ico"><Archive /></span><span class="grow"><span>All backups</span><span class="t-caption">Browse, check, restore or delete what is stored on a target.</span></span>
        </button>
        <button class="row link-row" onclick={() => (tab = 'schedules')}>
          <span class="ico"><CalendarClock /></span><span class="grow"><span>Schedules</span><span class="t-caption">{data.schedules.length ? `${data.schedules.filter((s) => s.enabled).length} of ${data.schedules.length} on` : 'None yet'}</span></span>
        </button>
        <button class="row link-row" onclick={() => (tab = 'targets')}>
          <span class="ico"><Server /></span><span class="grow"><span>Targets</span><span class="t-caption">{targets.length ? targets.map((t) => t.name).join(', ') : 'None yet'}</span></span>
        </button>
      </section>
    </div>
  {:else if tab === 'archives'}
    <DataTable fill label="Backups" columns={ARCH_COLS} rows={targets.length ? archives : []} key="name" error={archError} onretry={loadArchives}
      searchable placeholder="Search backups" bind:search={archApp} sort={{ key: 'at', dir: 'desc' }}
      empty={{ icon: Archive, title: targets.length ? 'No backups on this target' : 'No target yet', text: targets.length ? 'Back up now or add a schedule.' : 'Add a target first.' }}
      actions={(a) => [
        { label: 'Restore', icon: RotateCcw, run: () => restore(null, { target: archTarget, name: a.name, app: a.app }) },
        { label: 'Check that it restores', icon: ShieldCheck, run: () => verify({ id: a.name, target: archTarget, name: a.name }) },
        { label: 'Delete', icon: Trash2, danger: true, run: () => deleteArchive(a) },
      ]}
      footText={archives ? `${archives.length} ${archives.length === 1 ? 'backup' : 'backups'}, ${bytes(archives.reduce((n, a) => n + (a.size || 0), 0))}` : ''}>
      {#snippet toolbar()}
        {#if targets.length > 1}
          <select class="input" bind:value={archTarget} aria-label="Target">{#each targets as t (t.id)}<option value={t.id}>{t.name}</option>{/each}</select>
        {/if}
        <button class="icon-btn" onclick={loadArchives} aria-label="Reload" title="Reload"><RefreshCw size={16} class={archives === null ? 'spinner' : ''} /></button>
      {/snippet}
      {#snippet cell(a, c)}
        {#if c.key === 'app'}<span class="who">{@render appMark(a.app)}<span class="truncate">{appName(a.app)}</span></span>
        {:else if c.key === 'at'}<span title={full(a.at)}>{ago(a.at)}</span>
        {:else if c.key === 'size'}<span class="num">{bytes(a.size || 0)}</span>
        {:else}<span class="muted">{a.name}</span>{/if}
      {/snippet}
    </DataTable>
  {:else if tab === 'schedules'}
    <DataTable fill label="Schedules" columns={SCHED_COLS} rows={data.schedules} key="id"
      empty={{ icon: CalendarClock, title: 'No schedule yet', text: targets.length ? 'Daily backups at night are a good start.' : 'Add a target first.' }}
      actions={(s) => [
        { label: 'Edit', icon: Pencil, run: () => openSched(null, s) },
        { label: 'Back up now', icon: Play, run: () => { runTarget = s.target; openRun(null, s.app === '*' ? 'box' : s.app); } },
        { label: 'Delete', icon: Trash2, danger: true, run: () => deleteSched(s) },
      ]}>
      {#snippet toolbar()}<button class="btn btn-tinted btn-sm" onclick={(e) => openSched(e)} disabled={!targets.length}><Plus size={15} />Add schedule</button>{/snippet}
      {#snippet emptyAction()}{#if targets.length}<button class="btn btn-primary btn-sm" onclick={(e) => openSched(e)}><Plus size={15} />Add schedule</button>{/if}{/snippet}
      {#snippet cell(s, c)}
        {#if c.key === 'app'}<span class="who">{@render appMark(s.app)}<span class="truncate">{appName(s.app)}</span></span>
        {:else if c.key === 'when'}{schedText(s)}
        {:else if c.key === 'target'}{targetName(s.target)}
        {:else if c.key === 'keep'}<span class="muted">{keepText(s.keep)}</span>
        {:else if c.key === 'next_run'}{#if s.enabled && s.next_run}<span title={full(s.next_run)}>{when.format(s.next_run * 1000)}</span>{:else}<span class="muted">Off</span>{/if}
        {:else}<input class="switch" type="checkbox" checked={s.enabled} onchange={() => toggleSched(s)} aria-label="Schedule on" />{/if}
      {/snippet}
    </DataTable>
  {:else if tab === 'targets'}
    <DataTable fill label="Targets" columns={TARGET_COLS} rows={targets} key="id"
      empty={{ icon: HardDrive, title: 'No target yet', text: 'Add a folder on this server, an S3 bucket or a Proxmox Backup Server.' }}
      actions={(t) => [
        { label: 'Edit', icon: Pencil, run: () => editTarget(null, t) },
        { label: 'Browse backups', icon: Archive, run: () => browse(t) },
        { label: 'Remove', icon: Trash2, danger: true, run: () => deleteTarget(t) },
      ]}>
      {#snippet toolbar()}<button class="btn btn-tinted btn-sm" onclick={addTarget}><Plus size={15} />Add target</button>{/snippet}
      {#snippet emptyAction()}<button class="btn btn-primary btn-sm" onclick={addTarget}><Plus size={15} />Add target</button>{/snippet}
      {#snippet cell(t, c)}
        {#if c.key === 'name'}
          {@const Icon = KIND_ICON[t.type] || HardDrive}
          <span class="who"><span class="ico accent"><Icon /></span><span class="truncate">{t.name}</span>{#if t.type === 'pbs' && !data.targets.pbs_client}<span class="pill warn-pill">Client missing</span>{/if}</span>
        {:else if c.key === 'type'}{TYPE[t.type] || t.type}
        {:else}<span class="muted">{where(t)}</span>{/if}
      {/snippet}
    </DataTable>
  {:else}
    <DataTable fill label="Backup activity" columns={HIST_COLS} rows={history} key="id" bind:sort={histSort}
      searchable placeholder="Search activity" actions={histActions}
      empty={{ icon: History, title: histStatus ? 'Nothing failed' : 'Nothing happened yet', text: histStatus ? '' : 'Backups, restores and checks show up here.' }}>
      {#snippet toolbar()}
        <select class="input" bind:value={histStatus} aria-label="Result">
          <option value="">All results</option><option value="ok">Done</option><option value="failed">Failed</option>
        </select>
      {/snippet}
      {#snippet cell(r, c)}
        {#if c.key === 'at'}<span title={full(r.at)}>{ago(r.at)}</span>
        {:else if c.key === 'app'}<span class="who">{@render appMark(r.app)}<span class="truncate">{appName(r.app)}</span></span>
        {:else if c.key === 'kind'}{KIND[r.kind] || r.kind}{#if r.source === 'schedule'}<span class="muted"> (scheduled)</span>{/if}
        {:else if c.key === 'target'}{r.target_name || targetName(r.target)}
        {:else if c.key === 'size'}<span class="num">{r.size ? bytes(r.size) : ''}</span>
        {:else if c.key === 'seconds'}<span class="num muted">{r.seconds}s</span>
        {:else}
          {@const [text, tone] = STATUS[r.status] || ['Unknown', '']}
          <span class="res">{#if verifying[r.id]}<Loader size={14} class="spinner" />Checking{:else}<span class="dot {tone}"></span>{text}{/if}</span>
          {#if r.error}<span class="t-caption err">{r.error}</span>{/if}
        {/if}
      {/snippet}
    </DataTable>
  {/if}
</div>

{#snippet appMark(id)}
  {#if id === 'box' || id === '*'}<span class="ico accent"><Settings2 /></span>
  {:else}<AppIcon {id} name={appName(id)} size={22} icon={apps.find((a) => a.id === id)?.icon} />{/if}
{/snippet}

<Dialog bind:open={keyOpen} label="Backup key" {origin} locked={keyBusy || (!!secret && !saved)}>
  <form class="dlg" onsubmit={saveKey}>
    {#if secret}
      <h2 class="t-title">Save your backup key</h2>
      <p class="t-sub">This is the only time Box shows it. Store it in a password manager. Without it, backups cannot be restored on another server.</p>
      <div class="secret mono">{secret}</div>
      <button type="button" class="btn btn-gray btn-sm copy" onclick={copySecret}><Copy size={15} />Copy</button>
      <label class="row-field"><span class="field-label">I saved the key in a safe place</span><input class="switch" type="checkbox" bind:checked={saved} /></label>
      <div class="buttons"><button type="button" class="btn btn-primary" disabled={!saved} onclick={() => (keyOpen = false)}>Done</button></div>
    {:else}
      <h2 class="t-title">{keyMode === 'create' ? 'Create the backup key' : data?.key.configured ? 'Replace the backup key' : 'Use an existing key'}</h2>
      {#if keyMode === 'import'}
        <p class="t-sub">{data?.key.configured ? 'Backups made with the current key can then only be restored with that old key. ' : ''}Paste a key that starts with AGE-SECRET-KEY-1, for example from another Box.</p>
        <input class="input mono" bind:value={keyText} placeholder="AGE-SECRET-KEY-1..." spellcheck="false" autocomplete="off" aria-label="Backup key" />
      {:else}
        <p class="t-sub">Box creates a new random key and shows it to you once.</p>
      {/if}
      {#if keyError}<div class="notice" role="alert">{keyError}</div>{/if}
      <div class="buttons">
        <button type="button" class="btn btn-gray" data-close onclick={() => (keyOpen = false)} disabled={keyBusy}>Cancel</button>
        {#if keyMode === 'import' && !data?.key.configured}<button type="button" class="btn btn-gray" onclick={() => (keyMode = 'create')}>Create one instead</button>{/if}
        <button class="btn btn-primary" disabled={keyBusy || (keyMode === 'import' && !keyText)}>{#if keyBusy}<Loader size={16} class="spinner" />{/if}{keyMode === 'create' ? 'Create key' : 'Use this key'}</button>
      </div>
    {/if}
  </form>
</Dialog>

<TargetDialog bind:open={targetOpen} target={targetEdit} {origin} pbsClient={data?.targets?.pbs_client} defaultPath={data?.targets?.default_path} onSaved={load} />

<Dialog bind:open={schedOpen} label="Schedule" {origin} locked={schedBusy}>
  {#if sched}
    <form class="dlg" onsubmit={saveSched}>
      <h2 class="t-title">{sched.id ? 'Edit schedule' : 'New schedule'}</h2>
      {#if schedError}<div class="notice" role="alert">{schedError}</div>{/if}
      <label class="field"><span class="field-label">Back up</span>
        <select class="input" bind:value={sched.app}>
          <option value="*">All apps and Box settings</option>
          <option value="box">Box settings</option>
          {#each apps as a (a.id)}<option value={a.id}>{a.name}</option>{/each}
        </select>
      </label>
      <label class="field"><span class="field-label">To</span>
        <select class="input" bind:value={sched.target}>{#each targets as t (t.id)}<option value={t.id}>{t.name}</option>{/each}</select>
      </label>
      <div class="cols">
        <label class="field"><span class="field-label">How often</span>
          <select class="input" bind:value={sched.freq}><option value="daily">Every day</option><option value="weekly">Every week</option></select>
        </label>
        {#if sched.freq === 'weekly'}
          <label class="field"><span class="field-label">On</span>
            <select class="input" bind:value={sched.weekday}>{#each DAYS as d, i}<option value={i}>{d}</option>{/each}</select>
          </label>
        {/if}
        <label class="field"><span class="field-label">At</span><input class="input" type="time" bind:value={sched.time} required /></label>
      </div>
      <span class="field-label">Keep</span>
      <div class="cols">
        <label class="field"><span class="t-caption">Newest</span><input class="input" type="number" min="0" max="1000" bind:value={sched.keep.last} /></label>
        <label class="field"><span class="t-caption">One per day, days</span><input class="input" type="number" min="0" max="1000" bind:value={sched.keep.daily} /></label>
        <label class="field"><span class="t-caption">One per week, weeks</span><input class="input" type="number" min="0" max="1000" bind:value={sched.keep.weekly} /></label>
      </div>
      <p class="t-caption">Older backups of the same app on this target are deleted after each scheduled backup. All zero keeps everything. Apps stop for the moment their files are copied.</p>
      <div class="buttons">
        <button type="button" class="btn btn-gray" data-close onclick={() => (schedOpen = false)} disabled={schedBusy}>Cancel</button>
        <button class="btn btn-primary" disabled={schedBusy}>{#if schedBusy}<Loader size={16} class="spinner" />{/if}Save</button>
      </div>
    </form>
  {/if}
</Dialog>

<Dialog bind:open={runOpen} label="Back up now" {origin} locked={running}>
  <div class="dlg">
    <h2 class="t-title">Back up now</h2>
    <label class="field"><span class="field-label">What</span>
      <select class="input" bind:value={runApp} disabled={running}>
        <option value="box">Box settings</option>
        {#each apps as a (a.id)}<option value={a.id}>{a.name}</option>{/each}
      </select>
    </label>
    <label class="field"><span class="field-label">To</span>
      <select class="input" bind:value={runTarget} disabled={running}>{#each targets as t (t.id)}<option value={t.id}>{t.name}</option>{/each}</select>
    </label>
    {#if runApp !== 'box'}<p class="t-caption">The app stops while its files are copied, then starts again.</p>{/if}
    {#if running}<JobLog lines={runLines} />{/if}
    <div class="buttons">
      <button class="btn btn-gray" data-close onclick={() => (runOpen = false)} disabled={running}>Cancel</button>
      <button class="btn btn-primary" onclick={runNow} disabled={running || !runTarget}>{#if running}<Loader size={16} class="spinner" />Backing up{:else}Back up{/if}</button>
    </div>
  </div>
</Dialog>

<RestoreDialog bind:open={restoreOpen} preset={restorePreset} {targets} {apps} {origin} {onSessionLost} onDone={load} />

<style>
  h2, p { margin: 0; }
  .head-btns { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .tabs { display: flex; }
  .tabs :global(.seg) { min-width: min(100%, 34rem); }
  .scroll { flex: 1; min-height: 0; overflow: auto; display: grid; align-content: start; gap: 1rem; padding-bottom: 1rem; }
  .intro { display: flex; gap: 1.25rem; align-items: flex-start; padding: 1.5rem; }
  .intro-text { display: grid; gap: 0.5rem; max-width: 40rem; }
  .btns { display: flex; gap: 0.5rem; flex-wrap: wrap; margin-top: 0.5rem; }
  .stats { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.75rem; }
  .stat { display: flex; align-items: flex-start; gap: 0.875rem; padding: 1.125rem 1.25rem; }
  .stat-text { display: grid; gap: 0.125rem; min-width: 0; }
  .stat .t-head { font-size: 1.0625rem; }
  .link { padding: 0; border: 0; background: none; color: var(--accent); font: inherit; font-weight: 600; cursor: pointer; justify-self: start; }
  .link-row { width: 100%; border: 0; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; }
  .link-row:hover { background: var(--fill); }
  .who { display: inline-flex; align-items: center; gap: 0.625rem; min-width: 0; max-width: 100%; }
  .muted { color: var(--text-2); }
  .warn-pill { color: var(--warn); }
  .res { display: inline-flex; align-items: center; gap: 0.4rem; }
  .err { display: block; white-space: normal; max-width: 22rem; color: var(--error); }
  .cols { display: flex; gap: 0.75rem; flex-wrap: wrap; }
  .cols .field { flex: 1; min-width: 8rem; }
  .secret { padding: 0.875rem; border-radius: var(--r-control); background: var(--fill); word-break: break-all; font-size: 0.8125rem; user-select: all; }
  .copy { justify-self: start; }
  @media (max-width: 899px) { .stats { grid-template-columns: 1fr; } }
  @media (max-width: 639px) {
    .intro { flex-direction: column; }
    .tabs { overflow-x: auto; }
    .tabs :global(.seg) { min-width: 30rem; }
  }
</style>
