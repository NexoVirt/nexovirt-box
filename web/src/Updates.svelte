<script>
  // System updates (apt): check, choose, see the plan (dependencies picked up, removals refused),
  // install in the background with a live log, restart when the system asks for it.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { ago } from '$lib/format.js';
  import { runJob } from '$lib/jobs.js';
  import Dialog from '$lib/Dialog.svelte';
  import DataTable from '$lib/DataTable.svelte';
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
  const PKG_COLS = [
    { key: 'name', label: 'Package', primary: true, sortable: true },
    { key: 'current', label: 'Installed', mono: true, hide: 'md' },
    { key: 'candidate', label: 'New version', mono: true },
    { key: 'suite', label: 'Source', sortable: true, value: (p) => (p.security ? '0 security' : p.suite) },
  ];

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
        <span class="ico md warn"><Power /></span>
        <span class="grow"><span class="t-head">Restart needed</span><span class="t-caption">An update (for example a new kernel) only takes effect after the server restarts.</span></span>
        <button class="btn btn-gray btn-sm" onclick={() => { rebootText = ''; rebootOpen = true; }} disabled={run?.state === 'running'}>Restart server</button>
      </div>
    {/if}

    {#if refreshing}
      <div class="tile banner" role="status">
        <span class="ico md accent"><Loader class="spinner" /></span>
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
          <span class="ico md" class:accent={run.state === 'running'} class:ok={run.state === 'done'} class:error={run.state === 'failed'}>
            {#if run.state === 'running'}<Loader class="spinner" />{:else if run.state === 'done'}<CircleCheck />{:else}<CircleAlert />{/if}
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

    <DataTable label="Available updates" title="Available updates"
      description="{status.packages.length} {status.packages.length === 1 ? 'package' : 'packages'}{security ? `, ${security} security` : ''}{status.lists_at ? ` · lists from ${ago(status.lists_at)}` : ''}"
      columns={PKG_COLS} rows={status.packages} key="name" selectable bind:selected searchable placeholder="Search packages" maxHeight="34rem"
      empty={{ icon: CircleCheck, title: 'Everything is up to date', text: 'Check for updates to read the package sources again.' }}>
      {#snippet toolbar()}
        {#if security && security !== status.packages.length}
          <button class="btn btn-gray btn-sm" onclick={selectSecurity}><ShieldCheck size={15} />Select security updates</button>
        {/if}
      {/snippet}
      {#snippet cell(p, c)}
        {#if c.key === 'name'}{p.name}
        {:else if c.key === 'current'}<span class="muted">{p.current}</span>
        {:else if c.key === 'candidate'}{p.candidate}
        {:else}{#if p.security}<span class="pill sec"><ShieldCheck size={13} />Security</span>{:else}<span class="muted">{p.suite}</span>{/if}{/if}
      {/snippet}
    </DataTable>

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
        <span class="ico accent"><ShieldCheck /></span>
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

  .muted { color: var(--text-2); }
  .pill.sec { height: 1.5rem; background: var(--accent-fill); color: var(--accent); }

  .save-bar {
    position: sticky; bottom: 1rem; z-index: 10; display: flex; align-items: center; justify-content: flex-end; gap: 0.625rem; flex-wrap: wrap;
    padding: 0.625rem 0.75rem 0.625rem 1.125rem; border-radius: 1.125rem; background: var(--material); box-shadow: var(--shadow-float);
    -webkit-backdrop-filter: blur(30px) saturate(180%); backdrop-filter: blur(30px) saturate(180%);
  }
  .save-bar .t-sub { margin-right: auto; }

  .auto { cursor: pointer; }
  .plan { max-height: 10rem; overflow: auto; padding: 0.75rem 0.875rem; border-radius: var(--r-control); background: var(--fill); font-size: 0.75rem; line-height: 1.6; word-break: break-word; }
</style>
