<script>
  // System updates (apt): check, choose, see the plan (dependencies picked up, removals refused),
  // install in the background with a live log, restart when the system asks for it.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { ago } from '$lib/format.js';
  import { runJob } from '$lib/jobs.js';
  import Dialog from '$lib/Dialog.svelte';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import Package from '@lucide/svelte/icons/package';
  import Power from '@lucide/svelte/icons/power';
  import Download from '@lucide/svelte/icons/download';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';

  let { onSessionLost } = $props();

  let status = $state(null), error = $state('');
  let refreshing = $state(false), refreshInfo = $state(null); // { errors, reason_text }
  let selected = $state(new Set());
  let plan = $state(null), planOpen = $state(false), planBusy = $state(''), starting = $state(false);
  let run = $state(null); // { state, log, offset, exit_code }
  let logOpen = $state(true), logEl = $state();
  let rebootOpen = $state(false), rebootText = $state(''), rebooting = $state(false);
  let autoBusy = $state(false);
  let poll;

  const lost = (res) => { if (res.status === 401) { onSessionLost(); return true; } return false; };

  async function load() {
    const res = await api('/api/system-updates');
    if (lost(res)) return;
    if (!res.ok) { error = res.error; return; }
    error = '';
    status = res.data;
    const names = new Set(status.packages.map((p) => p.name));
    selected = new Set([...selected].filter((n) => names.has(n)));
    if (status.running && !poll) follow(true);
  }

  async function refresh() {
    refreshing = true;
    const res = await api('/api/system-updates/refresh', { method: 'POST' });
    refreshing = false;
    if (lost(res)) return;
    if (!res.ok) return toast(res.error, 'error', 9000);
    status = res.data;
    refreshInfo = res.data.errors?.length ? res.data : null;
    toast(res.data.packages.length ? `${res.data.packages.length} ${res.data.packages.length === 1 ? "update" : "updates"} available.` : 'Everything is up to date.', res.data.errors?.length ? 'info' : 'success');
  }

  const security = $derived(status ? status.packages.filter((p) => p.security).length : 0);
  const allOn = $derived(status && status.packages.length > 0 && selected.size === status.packages.length);

  function toggle(name) {
    const s = new Set(selected);
    s.has(name) ? s.delete(name) : s.add(name);
    selected = s;
  }
  function toggleAll() { selected = allOn ? new Set() : new Set(status.packages.map((p) => p.name)); }
  function selectSecurity() { selected = new Set(status.packages.filter((p) => p.security).map((p) => p.name)); }

  // Simulate first: the plan dialog shows what apt will really do.
  async function preview(op) {
    planBusy = op;
    const body = op === 'all' ? { op } : { op, packages: [...selected] };
    const res = await api('/api/system-updates/simulate', { method: 'POST', body });
    planBusy = '';
    if (lost(res)) return;
    if (!res.ok) return toast(res.error, 'error', 9000);
    plan = { op, ...res.data };
    if (op === 'selected' && res.data.added.length) selected = new Set([...selected, ...res.data.added]);
    planOpen = true;
  }

  async function install() {
    starting = true;
    const body = plan.op === 'all' ? { op: 'all' } : { op: 'selected', packages: [...selected] };
    const res = await api('/api/system-updates/start', { method: 'POST', body });
    starting = false;
    if (lost(res)) return;
    if (!res.ok) return toast(res.error, 'error', 9000);
    planOpen = false;
    run = { state: 'running', log: '', offset: 0, exit_code: -1 };
    logOpen = true;
    follow(false);
  }

  function follow(resume) {
    clearTimeout(poll);
    if (!run) run = { state: 'running', log: '', offset: 0, exit_code: -1 };
    const tick = async () => {
      const res = await api(`/api/system-updates/progress?offset=${run.offset}`);
      if (lost(res)) return;
      if (res.ok) {
        const d = res.data;
        run = { ...d, log: (d.offset < run.offset ? '' : run.log) + d.log };
        queueMicrotask(() => logEl && (logEl.scrollTop = logEl.scrollHeight));
        if (d.state !== 'running') {
          poll = null;
          if (!resume || d.state !== 'idle') toast(d.state === 'done' ? 'The updates are installed.' : 'The update failed. The log shows why.', d.state === 'done' ? 'success' : 'error', 9000);
          selected = new Set();
          load();
          return;
        }
      }
      poll = setTimeout(tick, 1500);
    };
    tick();
  }

  async function reboot(e) {
    e.preventDefault();
    rebooting = true;
    const res = await api('/api/system/reboot', { method: 'POST', body: { confirm: rebootText } });
    rebooting = false;
    if (lost(res)) return;
    if (!res.ok) return toast(res.error, 'error', 9000);
    rebootOpen = false;
    toast('The server restarts now. This page comes back when it is up again.', 'info', 12000);
  }

  async function setAuto(on) {
    autoBusy = true;
    const done = await runJob('/api/system-updates/auto', { op: on ? 'on' : 'off' });
    autoBusy = false;
    if (done.http === 401) return onSessionLost();
    if (done.status !== 'done') toast(done.error || 'It could not be changed.', 'error', 9000);
    else toast(on ? 'Security updates now install on their own every day.' : 'Automatic security updates are off.', 'success');
    load();
  }

  onMount(() => {
    load();
    return () => clearTimeout(poll);
  });
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Updates</h1>
      <p class="t-sub">Package updates for this server from apt. Box installs them in the background and never removes packages.</p>
    </div>
    <button class="btn btn-gray" onclick={refresh} disabled={refreshing || !status || run?.state === 'running'}>
      {#if refreshing}<Loader size={16} class="spinner" />Checking{:else}<RefreshCw size={16} />Check for updates{/if}
    </button>
  </header>

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else if !status}
    <div class="group">{#each Array(5) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</div>
  {:else}
    {#if status.reboot_required}
      <div class="tile banner" role="status">
        <span class="ico warn"><Power size={17} /></span>
        <span class="grow"><span class="t-head">Restart needed</span><span class="t-caption">An update (for example a new kernel) only takes effect after the server restarts.</span></span>
        <button class="btn btn-gray btn-sm" onclick={() => { rebootText = ''; rebootOpen = true; }} disabled={run?.state === 'running'}>Restart server</button>
      </div>
    {/if}

    {#if refreshing}
      <div class="tile banner" role="status">
        <span class="ico"><Loader size={17} class="spinner" /></span>
        <span class="grow"><span class="t-head">Checking the package sources</span><span class="t-caption">apt reads every repository. This can take up to two and a half minutes.</span></span>
      </div>
    {/if}

    {#if refreshInfo}
      <div class="tile repo" role="alert">
        <div class="repo-head"><CircleAlert size={18} /><span class="t-head">{refreshInfo.reason_text}</span></div>
        <pre class="mono out">{refreshInfo.errors.join('\n')}</pre>
        <p class="t-caption">The list below may be incomplete. Fix the sources in /etc/apt (for example in the console) and check again.</p>
      </div>
    {/if}

    {#if run && run.state !== 'idle'}
      <section class="group">
        <button class="row run-head" onclick={() => (logOpen = !logOpen)} aria-expanded={logOpen}>
          <span class="ico" class:accent={run.state === 'running'} class:okc={run.state === 'done'} class:errc={run.state === 'failed'}>
            {#if run.state === 'running'}<Loader size={16} class="spinner" />{:else if run.state === 'done'}<CircleCheck size={16} />{:else}<CircleAlert size={16} />{/if}
          </span>
          <span class="grow">
            <span class="t-head">{run.state === 'running' ? 'Installing updates' : run.state === 'done' ? 'Updates installed' : 'The update failed'}</span>
            <span class="t-caption">{run.state === 'running' ? 'It keeps running if you leave this page.' : `apt finished with exit code ${run.exit_code}.`}</span>
          </span>
          <span class="chev" class:open={logOpen}><ChevronRight size={16} /></span>
        </button>
        {#if logOpen}<pre class="mono log" bind:this={logEl}>{run.log || 'Starting...'}</pre>{/if}
      </section>
    {/if}

    <section class="group">
      <div class="group-head">
        <div>
          <h2 class="t-head">Available updates</h2>
          <p class="t-caption">
            {status.packages.length} {status.packages.length === 1 ? 'package' : 'packages'}{#if security}, {security} security{/if}
            {#if status.lists_at} · lists from {ago(status.lists_at)}{/if}
          </p>
        </div>
        {#if security && security !== status.packages.length}
          <button class="btn btn-gray btn-sm" onclick={selectSecurity}><ShieldCheck size={15} />Select security updates</button>
        {/if}
      </div>
      {#if status.packages.length}
        <div class="table-scroll">
          <table>
            <thead>
              <tr>
                <th class="cb"><input type="checkbox" checked={allOn} indeterminate={selected.size > 0 && !allOn} onchange={toggleAll} aria-label="Select all" /></th>
                <th>Package</th><th>Installed</th><th>New version</th><th>Source</th>
              </tr>
            </thead>
            <tbody>
              {#each status.packages as p (p.name)}
                <tr class:on={selected.has(p.name)} onclick={() => toggle(p.name)}>
                  <td class="cb"><input type="checkbox" checked={selected.has(p.name)} onclick={(e) => e.stopPropagation()} onchange={() => toggle(p.name)} aria-label="Select {p.name}" /></td>
                  <td class="name"><span class="truncate">{p.name}</span></td>
                  <td class="mono muted">{p.current}</td>
                  <td class="mono">{p.candidate}</td>
                  <td>{#if p.security}<span class="pill sec"><ShieldCheck size={13} />Security</span>{:else}<span class="muted">{p.suite}</span>{/if}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <div class="empty">
          <CircleCheck size={30} />
          <p class="t-head">Everything is up to date</p>
          <p class="t-caption">Check for updates to read the package sources again.</p>
        </div>
      {/if}
    </section>

    {#if status.packages.length}
      <div class="save-bar">
        <span class="t-sub">{selected.size ? `${selected.size} selected` : 'Nothing selected'}</span>
        <button class="btn btn-gray btn-sm" onclick={() => preview('selected')} disabled={!selected.size || !!planBusy || run?.state === 'running'}>
          {#if planBusy === 'selected'}<Loader size={15} class="spinner" />Planning{:else}Install selected{/if}
        </button>
        <button class="btn btn-primary btn-sm" onclick={() => preview('all')} disabled={!!planBusy || run?.state === 'running'}>
          {#if planBusy === 'all'}<Loader size={15} class="spinner" />Planning{:else}<Download size={15} />Install all{/if}
        </button>
      </div>
    {/if}

    <section class="group">
      <label class="row auto">
        <span class="ico"><ShieldCheck size={16} /></span>
        <span class="grow">
          <span class="t-head">Install security updates automatically</span>
          <span class="t-caption">
            {#if status.auto.enabled && !status.auto.box}On through the server's own unattended-upgrades setup. Box leaves it as it is.
            {:else}Uses unattended-upgrades once a day (installed when you turn it on). Only security updates, no restarts.{/if}
          </span>
        </span>
        {#if autoBusy}<Loader size={18} class="spinner" />{/if}
        <input type="checkbox" class="switch" checked={status.auto.enabled} disabled={autoBusy || (status.auto.enabled && !status.auto.box)}
          onchange={(e) => setAuto(e.target.checked)} aria-label="Automatic security updates" />
      </label>
    </section>
  {/if}
</div>

<Dialog bind:open={planOpen} label="Install updates" wide>
  {#if plan}
    <div class="dlg">
      <h2 class="t-title">{plan.op === 'all' ? 'Install all updates?' : 'Install the selected updates?'}</h2>
      {#if plan.simulation.remove.length}
        <div class="notice" role="alert"><CircleAlert size={18} /><span>apt would remove {plan.simulation.remove.join(', ')}. Box does not remove packages, so this cannot be installed here. Choose fewer packages or run it in the console.</span></div>
      {:else}
        <p class="t-sub">apt upgrades {plan.simulation.upgrade.length} {plan.simulation.upgrade.length === 1 ? 'package' : 'packages'}{#if plan.simulation.install.length} and adds {plan.simulation.install.length} new {plan.simulation.install.length === 1 ? 'one' : 'ones'} it needs{/if}.</p>
      {/if}
      {#if plan.added?.length}
        <div class="notice neutral"><Package size={18} /><span>Also selected because the others need them: {plan.added.join(', ')}.</span></div>
      {/if}
      {#if plan.simulation.install.length}<p class="t-caption">New: {plan.simulation.install.join(', ')}</p>{/if}
      {#if plan.simulation.kernel}<p class="t-caption">This includes a new kernel. Restart the server afterwards.</p>{/if}
      <div class="plan mono">{plan.simulation.upgrade.join('  ')}</div>
      <div class="buttons">
        <button class="btn btn-gray" data-close onclick={() => (planOpen = false)}>Cancel</button>
        <button class="btn btn-primary" onclick={install} disabled={starting || plan.simulation.remove.length > 0 || !(plan.simulation.upgrade.length + plan.simulation.install.length)}>
          {#if starting}<Loader size={16} class="spinner" />Starting{:else}Install{/if}
        </button>
      </div>
    </div>
  {/if}
</Dialog>

<Dialog bind:open={rebootOpen} label="Restart the server">
  <form class="dlg" onsubmit={reboot}>
    <h2 class="t-title">Restart the server?</h2>
    <p class="t-sub">Every app and open console stops until the server is back, usually after a minute or two. Type REBOOT to restart now.</p>
    <input class="input mono" bind:value={rebootText} placeholder="REBOOT" aria-label="Type REBOOT to confirm" autocomplete="off" />
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => (rebootOpen = false)}>Cancel</button>
      <button class="btn btn-danger" disabled={rebootText !== 'REBOOT' || rebooting}>{#if rebooting}<Loader size={16} class="spinner" />{/if}Restart</button>
    </div>
  </form>
</Dialog>

<style>
  .banner { display: flex; align-items: center; gap: 0.875rem; padding: 1rem 1.125rem; }
  .banner .grow, .run-head .grow { flex: 1; min-width: 0; display: grid; gap: 0.125rem; }
  .ico.warn { background: color-mix(in srgb, var(--warn) 16%, transparent); color: var(--warn); }
  .ico.accent { background: var(--accent-fill); color: var(--accent); }
  .ico.okc { background: color-mix(in srgb, var(--ok) 14%, transparent); color: var(--ok); }
  .ico.errc { background: var(--error-fill); color: var(--error); }

  .repo { display: grid; gap: 0.625rem; }
  .repo-head { display: flex; align-items: center; gap: 0.5rem; color: var(--error); }
  .repo p { margin: 0; }
  .out { margin: 0; padding: 0.75rem 0.875rem; border-radius: var(--r-control); background: var(--fill); white-space: pre-wrap; word-break: break-word; font-size: 0.75rem; max-height: 14rem; overflow: auto; }

  .run-head { width: 100%; border: 0; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; }
  .run-head:hover { background: var(--fill); }
  .chev { color: var(--text-3); transition: transform 250ms var(--spring); display: grid; }
  .chev.open { transform: rotate(90deg); }
  .log {
    margin: 0; padding: 0.875rem 1.125rem 1.125rem; height: 18rem; overflow: auto; font-size: 0.75rem; line-height: 1.5;
    white-space: pre-wrap; word-break: break-word; border-top: 1px solid var(--hairline); background: var(--surface-2);
  }

  .group-head p { margin: 0.125rem 0 0; }
  .table-scroll { overflow-x: auto; padding: 0.25rem 0.5rem 0.5rem; max-height: 34rem; overflow-y: auto; }
  table { width: 100%; border-collapse: separate; border-spacing: 0; font-size: 0.8125rem; }
  th { position: sticky; top: 0; background: var(--surface); text-align: left; font-size: 0.75rem; font-weight: 600; color: var(--text-2); padding: 0.5rem 0.625rem; border-bottom: 1px solid var(--hairline); white-space: nowrap; z-index: 1; }
  td { padding: 0.375rem 0.625rem; height: 2.75rem; border-bottom: 1px solid var(--hairline); white-space: nowrap; }
  tbody tr { cursor: pointer; }
  tbody tr:last-child td { border-bottom: 0; }
  tbody tr:hover td { background: var(--fill); }
  tbody tr.on td { background: var(--accent-fill); }
  tbody tr td:first-child { border-radius: 0.5rem 0 0 0.5rem; }
  tbody tr td:last-child { border-radius: 0 0.5rem 0.5rem 0; }
  .cb { width: 2.25rem; }
  .cb input { width: 1.05rem; height: 1.05rem; accent-color: var(--accent); cursor: pointer; }
  .name { font-weight: 600; max-width: 18rem; }
  .muted { color: var(--text-2); }
  .pill.sec { height: 1.5rem; background: var(--accent-fill); color: var(--accent); }

  .empty { display: grid; justify-items: center; gap: 0.375rem; padding: 2rem 1rem 2.25rem; text-align: center; color: var(--text-3); }
  .empty p { margin: 0; }
  .empty .t-head { color: var(--text-2); }

  .save-bar {
    position: sticky; bottom: 1rem; z-index: 10; display: flex; align-items: center; justify-content: flex-end; gap: 0.625rem; flex-wrap: wrap;
    padding: 0.625rem 0.75rem 0.625rem 1.125rem; border-radius: 1.125rem; background: var(--material); box-shadow: var(--shadow-float);
    -webkit-backdrop-filter: blur(30px) saturate(180%); backdrop-filter: blur(30px) saturate(180%);
  }
  .save-bar .t-sub { margin-right: auto; }

  .auto { cursor: pointer; }
  .plan { max-height: 10rem; overflow: auto; padding: 0.75rem 0.875rem; border-radius: var(--r-control); background: var(--fill); font-size: 0.75rem; line-height: 1.6; word-break: break-word; }
</style>
