<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { withProof } from '$lib/proof.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes, ago } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import Popover from '$lib/Popover.svelte';
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
  import Ellipsis from '@lucide/svelte/icons/ellipsis';
  import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import Pencil from '@lucide/svelte/icons/pencil';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import Archive from '@lucide/svelte/icons/archive';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';

  let { onSessionLost } = $props();

  const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
  const KIND_ICON = { local: HardDrive, s3: Cloud, pbs: Server };

  let data = $state(null), error = $state(''), apps = $state([]);
  let origin = $state(null), menu = $state(null);
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
  function editTarget(e, t) { menu = null; at(e); targetEdit = t; targetOpen = true; }
  async function deleteTarget(t) {
    menu = null;
    const res = await withProof(() => api(`/api/backups/targets/${t.id}/delete`, { method: 'POST' }));
    if (res.cancelled) return;
    if (!res.ok) return toast(res.error, 'error', 7000);
    toast(`${t.name} was removed. Backups already stored there stay.`, 'success');
    load();
  }

  // ---- schedules ----
  function openSched(e, s) {
    menu = null;
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
    menu = null;
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
    menu = null;
    verifying = { ...verifying, [r.id]: true };
    const job = await runJob('/api/backups/verify', { target: r.target, name: r.name });
    verifying = { ...verifying, [r.id]: false };
    if (job.status === 'done') toast(`${r.name} is complete and can be restored (${job.result.files} files).`, 'success', 7000);
    else toast(job.error, 'error', 9000);
    load();
  }
  function restore(e, r) { menu = null; at(e); restorePreset = r ? { target: r.target, name: r.name, app: r.app } : null; restoreOpen = true; }

  const STATUS = { ok: ['Done', 'ok'], failed: ['Failed', 'error'] };
  const KIND = { backup: 'Backup', restore: 'Restore', verify: 'Check' };

  onMount(() => {
    load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  });
</script>

<div class="page">
  <header class="page-head">
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

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else if !data}
    <div class="group">{#each Array(4) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</div>
  {:else}
    {#if !data.key.configured}
      <div class="tile intro">
        <span class="intro-ico"><KeyRound size={26} /></span>
        <div class="intro-text">
          <h2 class="t-title">Set up the backup key</h2>
          <p class="t-sub">Every backup is encrypted with this key before it leaves the app. Box keeps a copy for one-click restores. Save it somewhere safe too: you need it to restore on another server.</p>
          <div class="intro-btns">
            <button class="btn btn-primary" onclick={(e) => openKey(e, 'create')}>Create a key</button>
            <button class="btn btn-gray" onclick={(e) => openKey(e, 'import')}>I have a key</button>
          </div>
        </div>
      </div>
    {/if}

    <div class="sub-head">
      <p class="t-section">Targets</p>
      <button class="btn btn-tinted btn-sm" onclick={addTarget}><Plus size={15} />Add target</button>
    </div>
    <section class="group">
      {#each targets as t (t.id)}
        {@const Icon = KIND_ICON[t.type] || HardDrive}
        <div class="row">
          <span class="ico"><Icon size={16} /></span>
          <div class="grow">
            <span>{t.name}</span>
            <span class="t-caption mono truncate">{t.type === 'local' ? t.path : t.type === 's3' ? `${t.bucket}${t.prefix ? '/' + t.prefix : ''} on ${t.endpoint}` : t.repository}</span>
          </div>
          {#if t.type === 'pbs' && !data.targets.pbs_client}<span class="pill warn-pill">Client missing</span>{/if}
          <div class="more">
            <button class="icon-btn" onclick={() => (menu = menu === 't' + t.id ? null : 't' + t.id)} aria-label="Actions for {t.name}" aria-expanded={menu === 't' + t.id}><Ellipsis size={19} /></button>
            <Popover open={menu === 't' + t.id} label="Actions for {t.name}">
              <button class="pop-item" onclick={(e) => editTarget(e, t)}><Pencil size={16} />Edit</button>
              <button class="pop-item" onclick={(e) => restore(e, { target: t.id })}><RotateCcw size={16} />Browse backups</button>
              <button class="pop-item danger" onclick={() => deleteTarget(t)}><Trash2 size={16} />Remove</button>
            </Popover>
          </div>
        </div>
      {:else}
        <div class="row t-sub">No target yet. Add a folder on this server, an S3 bucket or a Proxmox Backup Server.</div>
      {/each}
    </section>

    <div class="sub-head">
      <p class="t-section">Schedules</p>
      <button class="btn btn-tinted btn-sm" onclick={(e) => openSched(e)} disabled={!targets.length}><Plus size={15} />Add schedule</button>
    </div>
    <section class="group">
      {#each data.schedules as s (s.id)}
        <div class="row">
          <span class="ico"><CalendarClock size={16} /></span>
          <div class="grow">
            <span>{appName(s.app)}</span>
            <span class="t-caption">{schedText(s)} to {targetName(s.target)}. {keepText(s.keep)}.</span>
          </div>
          <input class="switch" type="checkbox" checked={s.enabled} onchange={() => toggleSched(s)} aria-label="Schedule on" />
          <div class="more">
            <button class="icon-btn" onclick={() => (menu = menu === 's' + s.id ? null : 's' + s.id)} aria-label="Schedule actions" aria-expanded={menu === 's' + s.id}><Ellipsis size={19} /></button>
            <Popover open={menu === 's' + s.id} label="Schedule actions">
              <button class="pop-item" onclick={(e) => openSched(e, s)}><Pencil size={16} />Edit</button>
              <button class="pop-item danger" onclick={() => deleteSched(s)}><Trash2 size={16} />Delete</button>
            </Popover>
          </div>
        </div>
      {:else}
        <div class="row t-sub">{targets.length ? 'No schedule yet. Daily backups at night are a good start.' : 'Add a target first.'}</div>
      {/each}
    </section>

    <p class="t-section sub">History</p>
    <div class="table-wrap group">
      <table class="hist">
        <thead><tr><th>When</th><th>What</th><th>Action</th><th>Target</th><th class="num">Size</th><th class="num">Took</th><th>Result</th><th></th></tr></thead>
        <tbody>
          {#each data.history as r (r.id)}
            {@const [label, tone] = STATUS[r.status] || ['Unknown', '']}
            <tr>
              <td title={new Date(r.at * 1000).toLocaleString()}>{ago(r.at)}</td>
              <td>{appName(r.app)}</td>
              <td>{KIND[r.kind] || r.kind}{r.source === 'schedule' ? ' (scheduled)' : ''}</td>
              <td class="truncate">{r.target_name || targetName(r.target)}</td>
              <td class="num">{r.size ? bytes(r.size) : ''}</td>
              <td class="num">{r.seconds}s</td>
              <td><span class="res" title={r.error}><span class="dot {tone}"></span>{label}</span>{#if r.error}<span class="t-caption err">{r.error}</span>{/if}</td>
              <td class="act">
                {#if r.kind === 'backup' && r.status === 'ok' && r.name}
                  <div class="more">
                    <button class="icon-btn" onclick={() => (menu = menu === 'h' + r.id ? null : 'h' + r.id)} aria-label="Actions for this backup" disabled={verifying[r.id]}>
                      {#if verifying[r.id]}<Loader size={17} class="spinner" />{:else}<Ellipsis size={19} />{/if}
                    </button>
                    <Popover open={menu === 'h' + r.id} label="Backup actions">
                      <button class="pop-item" onclick={(e) => restore(e, r)}><RotateCcw size={16} />Restore</button>
                      <button class="pop-item" onclick={() => verify(r)}><ShieldCheck size={16} />Check that it restores</button>
                    </Popover>
                  </div>
                {/if}
              </td>
            </tr>
          {:else}
            <tr><td colspan="8" class="t-sub empty-row"><Archive size={16} />No backups yet.</td></tr>
          {/each}
        </tbody>
      </table>
    </div>

    {#if data.key.configured}
      <p class="t-section sub">Encryption</p>
      <section class="group">
        <div class="row">
          <span class="ico"><KeyRound size={16} /></span>
          <div class="grow"><span>Backup key</span><span class="t-caption mono truncate">Public part {data.key.recipient}</span></div>
          <button class="btn btn-gray btn-sm" onclick={(e) => openKey(e, 'import')}>Replace</button>
        </div>
      </section>
    {/if}
  {/if}
</div>

<svelte:window onpointerdown={(e) => { if (menu && !e.target.closest('.more')) menu = null; }} />

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
  .intro { display: flex; gap: 1.125rem; align-items: flex-start; margin-bottom: 0.5rem; }
  .intro-ico { display: grid; place-items: center; flex-shrink: 0; width: 3.25rem; height: 3.25rem; border-radius: 1rem; background: var(--accent-fill); color: var(--accent); }
  .intro-text { display: grid; gap: 0.5rem; }
  .intro-btns { display: flex; gap: 0.5rem; flex-wrap: wrap; margin-top: 0.5rem; }
  .sub-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; margin: 1.25rem 1.125rem 0.5rem; }
  .sub { margin: 1.25rem 1.125rem 0.5rem; }
  .group { overflow: visible; }
  .more { position: relative; }
  .warn-pill { color: var(--warn); }
  .skeleton.line { display: block; width: 50%; height: 0.8rem; }
  .table-wrap { overflow-x: auto; }
  .hist { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
  .hist th { text-align: left; font-weight: 600; font-size: 0.75rem; color: var(--text-2); padding: 0.75rem 0.875rem 0.5rem; white-space: nowrap; }
  .hist td { padding: 0.625rem 0.875rem; border-top: 1px solid var(--hairline); vertical-align: middle; white-space: nowrap; }
  .hist td.truncate { max-width: 12rem; }
  .num { text-align: right; font-variant-numeric: tabular-nums; }
  .res { display: inline-flex; align-items: center; gap: 0.4rem; }
  .err { display: block; white-space: normal; max-width: 22rem; }
  .act { width: 2.75rem; }
  .empty-row { display: flex; align-items: center; gap: 0.5rem; padding: 1.25rem 0.875rem; }
  .pop-item { display: flex; align-items: center; gap: 0.625rem; width: 100%; min-height: 2.5rem; padding: 0 0.625rem; border: 0; background: none; cursor: pointer; text-align: left; font-weight: 500; border-radius: 0.625rem; white-space: nowrap; }
  .pop-item:hover { background: var(--fill); }
  .pop-item.danger { color: var(--error); }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .field { display: grid; gap: 0.375rem; }
  .cols { display: flex; gap: 0.75rem; flex-wrap: wrap; }
  .cols .field { flex: 1; min-width: 8rem; }
  .secret { padding: 0.875rem; border-radius: var(--r-control); background: var(--fill); word-break: break-all; font-size: 0.8125rem; user-select: all; }
  .copy { justify-self: start; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; flex-wrap: wrap; }
  @media (max-width: 639px) { .intro { flex-direction: column; } }
</style>
