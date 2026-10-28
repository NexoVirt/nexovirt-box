<script>
  // Firewall: overview, Box's own rules (edited as a draft, applied with a confirm window that
  // rolls back on its own), quick presets and the server's other rules read only.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import Dialog from '$lib/Dialog.svelte';
  import Popover from '$lib/Popover.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import AppIcon from '$lib/AppIcon.svelte';
  import Shield from '@lucide/svelte/icons/shield';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import ShieldAlert from '@lucide/svelte/icons/shield-alert';
  import ArrowDownToLine from '@lucide/svelte/icons/arrow-down-to-line';
  import ArrowUpFromLine from '@lucide/svelte/icons/arrow-up-from-line';
  import ListChecks from '@lucide/svelte/icons/list-checks';
  import Plus from '@lucide/svelte/icons/plus';
  import Zap from '@lucide/svelte/icons/zap';
  import Pencil from '@lucide/svelte/icons/pencil';
  import Trash from '@lucide/svelte/icons/trash-2';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import Undo from '@lucide/svelte/icons/undo-2';
  import Terminal from '@lucide/svelte/icons/terminal';
  import Globe from '@lucide/svelte/icons/globe';

  let { onSessionLost } = $props();

  const BACKENDS = { nftables: 'nftables', ufw: 'ufw', firewalld: 'firewalld' };
  const POLICY = { allow: 'Allowed', deny: 'Blocked', unknown: 'Unknown' };
  const boxPort = Number(location.port || (location.protocol === 'https:' ? 443 : 80));
  const PRESETS = [
    { label: 'SSH', hint: 'Port 22', icon: Terminal, rules: [{ port: '22', comment: 'SSH' }] },
    { label: 'Web (HTTP and HTTPS)', hint: 'Ports 80 and 443', icon: Globe, rules: [{ port: '80', comment: 'HTTP' }, { port: '443', comment: 'HTTPS' }] },
    { label: 'NexoVirt Box', hint: `Port ${boxPort}`, icon: Shield, rules: [{ port: String(boxPort), comment: 'NexoVirt Box' }] },
  ];

  let status = $state(null);
  let error = $state('');
  let draft = $state([]);
  let apps = $state([]);
  let saving = $state(false);
  let pending = $state(null); // { id, left, total }
  let presetsOpen = $state(false);
  let foreignOpen = $state(false);
  let blockOpen = $state(false), blockText = $state('');
  let editOpen = $state(false), editIndex = $state(-1), edit = $state(null), editError = $state('');
  let timer;

  async function load() {
    const [f, a] = await Promise.all([api('/api/firewall'), api('/api/apps')]);
    if (f.status === 401) return onSessionLost();
    if (!f.ok) { error = f.error; return; }
    error = '';
    status = f.data;
    draft = f.data.rules.map((r) => ({ ...r }));
    if (a.ok) apps = a.data;
    if (f.data.pending) startCountdown(f.data.pending.id, f.data.pending.seconds_left);
  }

  const dirty = $derived(status && JSON.stringify(draft) !== JSON.stringify(status.rules));
  const changes = $derived.by(() => {
    if (!status) return 0;
    const a = new Set(status.rules.map((r) => JSON.stringify(r))), b = new Set(draft.map((r) => JSON.stringify(r)));
    return [...a].filter((x) => !b.has(x)).length + [...b].filter((x) => !a.has(x)).length;
  });

  // The installed app a rule opens (same TCP port), for the app column.
  function appFor(r) {
    if (r.proto !== 'tcp') return null;
    return apps.find((x) => String(x.main_port) === r.port) || null;
  }

  function hasRule(port, proto = 'tcp') { return draft.some((r) => r.action === 'allow' && r.port === port && r.proto === proto && !r.source); }

  function addPreset(rules) {
    presetsOpen = false;
    let added = 0;
    for (const r of rules) {
      if (hasRule(r.port, r.proto || 'tcp')) continue;
      draft.push({ id: '', action: 'allow', proto: 'tcp', source: '', ...r });
      added++;
    }
    if (!added) toast('These ports are already allowed.');
  }

  function openEditor(i = -1) {
    editIndex = i;
    edit = i >= 0 ? { ...draft[i] } : { id: '', action: 'allow', proto: 'tcp', port: '', source: '', comment: '' };
    editError = '';
    editOpen = true;
  }

  const PORT_RE = /^\d{1,5}(-\d{1,5})?$/;
  const SOURCE_RE = /^[0-9a-fA-F:.]+(\/\d{1,3})?$/;
  function saveRule(e) {
    e.preventDefault();
    const r = { ...edit, port: edit.port.trim(), source: edit.source.trim(), comment: edit.comment.trim() };
    const [lo, hi = lo] = r.port.split('-').map(Number);
    if (!PORT_RE.test(r.port) || lo < 1 || hi > 65535 || lo > hi) { editError = 'Enter a port from 1 to 65535, or a range like 8000-8100.'; return; }
    if (r.source && !SOURCE_RE.test(r.source)) { editError = 'Enter an IP address or a network like 192.168.1.0/24, or leave it empty for anywhere.'; return; }
    if (!/^[A-Za-z0-9 ._,:()/+-]{0,60}$/.test(r.comment)) { editError = 'The note may contain letters, digits, spaces and . _ , : ( ) / + -'; return; }
    if (editIndex >= 0) draft[editIndex] = r;
    else draft.push(r);
    editOpen = false;
  }

  function blocksBox() {
    return draft.some((r) => {
      if (r.action !== 'deny' || r.proto !== 'tcp') return false;
      const [lo, hi = lo] = String(r.port).split('-').map(Number);
      return lo <= boxPort && boxPort <= hi;
    });
  }

  function apply() {
    if (blocksBox()) { blockText = ''; blockOpen = true; return; }
    send('');
  }

  async function send(confirm) {
    saving = true;
    blockOpen = false;
    const res = await api('/api/firewall', { method: 'POST', body: { rules: draft, confirm } });
    saving = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) return toast(res.error, 'error', 9000);
    startCountdown(res.data.change, res.data.seconds);
    load();
  }

  function startCountdown(id, left) {
    pending = { id, left, total: Math.max(left, 60) };
    clearInterval(timer);
    timer = setInterval(() => {
      if (!pending) return clearInterval(timer);
      pending.left -= 1;
      if (pending.left <= 0) {
        clearInterval(timer);
        pending = null;
        toast('The change was not confirmed, so the previous rules are back.', 'error', 9000);
        setTimeout(load, 1500);
      }
    }, 1000);
  }

  // Confirming is a new request over the new rules: if it gets through, Box is still reachable.
  async function keep() {
    const res = await api('/api/firewall/confirm', { method: 'POST', body: { change: pending.id } });
    if (!res.ok) return toast(res.error, 'error', 9000);
    clearInterval(timer);
    pending = null;
    toast('The firewall rules are saved.', 'success');
    load();
  }

  onMount(() => {
    load();
    return () => clearInterval(timer);
  });
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Firewall</h1>
      <p class="t-sub">Open or block ports. Box only changes its own rules and leaves everything else as it is.</p>
    </div>
  </header>

  {#if pending}
    <div class="tile pending" role="status">
      <span class="ico warn"><ShieldAlert size={18} /></span>
      <div class="grow">
        <span class="t-head">Keep these rules?</span>
        <span class="t-caption">They are active now. Without a confirmation, Box puts the previous rules back in <span class="num">{pending.left}</span> s, so you cannot lock yourself out.</span>
        <span class="bar countdown"><span style:transform="scaleX({pending.left / pending.total})" style:background="var(--warn)"></span></span>
      </div>
      <button class="btn btn-primary btn-sm" onclick={keep}>Keep rules</button>
    </div>
  {/if}

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else if !status}
    <div class="stats">{#each Array(4) as _}<div class="tile stat"><span class="skeleton" style:height="2.5rem"></span></div>{/each}</div>
    <div class="group">{#each Array(3) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</div>
  {:else if !status.backend}
    <div class="notice"><CircleAlert size={18} /><span>No firewall tool was found. Install nftables on the server (apt install nftables), then reload this page.</span></div>
  {:else}
    <div class="stats">
      <div class="tile stat">
        <span class="stat-ico"><ShieldCheck size={18} /></span>
        <span class="stat-text"><span class="t-caption">Firewall</span><span class="t-head">{BACKENDS[status.backend]}</span></span>
      </div>
      <div class="tile stat">
        <span class="stat-ico"><ArrowDownToLine size={18} /></span>
        <span class="stat-text"><span class="t-caption">Incoming by default</span><span class="t-head"><span class="dot" class:ok={status.policies?.in === 'allow'} class:warn={status.policies?.in === 'deny'}></span>{POLICY[status.policies?.in || 'unknown']}</span></span>
      </div>
      <div class="tile stat">
        <span class="stat-ico"><ArrowUpFromLine size={18} /></span>
        <span class="stat-text"><span class="t-caption">Outgoing by default</span><span class="t-head"><span class="dot" class:ok={status.policies?.out === 'allow'} class:warn={status.policies?.out === 'deny'}></span>{POLICY[status.policies?.out || 'unknown']}</span></span>
      </div>
      <div class="tile stat">
        <span class="stat-ico"><ListChecks size={18} /></span>
        <span class="stat-text"><span class="t-caption">Box rules</span><span class="t-head num">{status.rules.length}{#if dirty}<span class="t-caption"> ({changes} unsaved)</span>{/if}</span></span>
      </div>
    </div>

    <section class="group rules">
      <div class="group-head">
        <h2 class="t-head">Rules</h2>
        <div class="head-btns">
          <div class="pop-wrap">
            <button class="btn btn-gray btn-sm" onclick={() => (presetsOpen = !presetsOpen)} aria-haspopup="menu" aria-expanded={presetsOpen}><Zap size={15} />Quick add</button>
            <Popover bind:open={presetsOpen} label="Quick add">
              <p class="t-section pop-title">Common</p>
              {#each PRESETS as p (p.label)}
                {@const Icon = p.icon}
                <button class="pop-item" role="menuitem" onclick={() => addPreset(p.rules)}>
                  <span class="pop-ico"><Icon size={15} /></span><span class="grow"><span>{p.label}</span><span class="t-caption">{p.hint}</span></span>
                </button>
              {/each}
              {#if apps.some((a) => a.main_port)}
                <p class="t-section pop-title">Apps</p>
                {#each apps.filter((a) => a.main_port) as a (a.id)}
                  <button class="pop-item" role="menuitem" onclick={() => addPreset([{ port: String(a.main_port), comment: a.name.slice(0, 60) }])}>
                    <AppIcon id={a.id} name={a.name} size={22} icon={a.icon} /><span class="grow"><span class="truncate">{a.name}</span><span class="t-caption">Port {a.main_port}</span></span>
                  </button>
                {/each}
              {/if}
            </Popover>
          </div>
          <button class="btn btn-tinted btn-sm" onclick={() => openEditor()}><Plus size={15} />Add rule</button>
        </div>
      </div>

      {#if draft.length}
        <div class="table-scroll">
          <table>
            <thead>
              <tr><th>Direction</th><th>Action</th><th>Protocol</th><th>Port</th><th>From</th><th>Note</th><th>App</th><th class="sr">Actions</th></tr>
            </thead>
            <tbody>
              {#each draft as r, i (i)}
                {@const app = appFor(r)}
                {@const isNew = !status.rules.some((s) => JSON.stringify(s) === JSON.stringify(r))}
                <tr class:new={isNew} ondblclick={() => openEditor(i)}>
                  <td><span class="dir"><ArrowDownToLine size={14} />In</span>{#if isNew}<span class="unsaved">Unsaved</span>{/if}</td>
                  <td><span class="pill" class:allow={r.action === 'allow'} class:deny={r.action === 'deny'}><span class="dot" class:ok={r.action === 'allow'} class:error={r.action === 'deny'}></span>{r.action === 'allow' ? 'Allow' : 'Block'}</span></td>
                  <td class="mono">{r.proto.toUpperCase()}</td>
                  <td class="mono num">{r.port}</td>
                  <td class:mono={!!r.source} class:muted={!r.source}>{r.source || 'Anywhere'}</td>
                  <td class="note truncate">{r.comment || ''}</td>
                  <td>{#if app}<span class="app"><AppIcon id={app.id} name={app.name} size={20} icon={app.icon} /><span class="truncate">{app.name}</span></span>{/if}</td>
                  <td class="acts">
                    <button class="icon-btn sm" onclick={() => openEditor(i)} aria-label="Edit rule"><Pencil size={15} /></button>
                    <button class="icon-btn sm" onclick={() => draft.splice(i, 1)} aria-label="Remove rule"><Trash size={15} /></button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <div class="empty">
          <Shield size={30} />
          <p class="t-head">No rules from Box yet</p>
          <p class="t-caption">Everything the server allowed before still applies. Use Quick add for common ports.</p>
        </div>
      {/if}
    </section>

    {#if dirty || status.backend === 'ufw'}
      <div class="save-bar" class:dirty>
        {#if status.backend === 'ufw'}<p class="t-caption hint">Ports of Docker apps are opened by Docker itself and are not affected by ufw rules.</p>{/if}
        {#if dirty}
          <span class="t-sub">{changes === 1 ? '1 change' : `${changes} changes`} not applied yet</span>
          <button class="btn btn-gray btn-sm" onclick={() => (draft = status.rules.map((r) => ({ ...r })))}><Undo size={15} />Discard</button>
          <button class="btn btn-primary btn-sm" onclick={apply} disabled={saving || !!pending}>
            {#if saving}<Loader size={15} class="spinner" />Applying{:else}Apply rules{/if}
          </button>
        {/if}
      </div>
    {/if}

    <section class="group">
      <button class="row foreign-head" onclick={() => (foreignOpen = !foreignOpen)} aria-expanded={foreignOpen}>
        <span class="grow"><span class="t-head">Other rules on this server</span><span class="t-caption">{status.foreign.length} lines from {BACKENDS[status.backend]}, read only. Box never changes them.</span></span>
        <span class="chev" class:open={foreignOpen}><ChevronRight size={16} /></span>
      </button>
      {#if foreignOpen}<pre class="mono foreign">{status.foreign.join('\n') || 'None.'}</pre>{/if}
    </section>
  {/if}
</div>

<Dialog bind:open={editOpen} label={editIndex >= 0 ? 'Edit rule' : 'New rule'}>
  {#if edit}
    <form class="dlg" onsubmit={saveRule}>
      <h2 class="t-title">{editIndex >= 0 ? 'Edit rule' : 'New rule'}</h2>
      {#if editError}<div class="notice" role="alert">{editError}</div>{/if}
      <div class="field"><span class="field-label">Action</span><Segmented options={[{ value: 'allow', label: 'Allow' }, { value: 'deny', label: 'Block' }]} bind:value={edit.action} label="Allow or block" /></div>
      <div class="two">
        <div class="field"><span class="field-label">Protocol</span><Segmented options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} bind:value={edit.proto} label="Protocol" /></div>
        <label class="field"><span class="field-label">Port or range</span><input class="input mono" bind:value={edit.port} placeholder="8080 or 8000-8100" inputmode="numeric" required /></label>
      </div>
      <label class="field">
        <span class="field-label">From</span>
        <input class="input mono" bind:value={edit.source} placeholder="Anywhere" spellcheck="false" />
        <span class="t-caption">An IP address or network (192.168.1.0/24). Empty means from anywhere.</span>
      </label>
      <label class="field"><span class="field-label">Note</span><input class="input" bind:value={edit.comment} placeholder="What this rule is for" maxlength="60" /></label>
      <p class="t-caption">The rule is applied together with your other changes when you choose Apply rules.</p>
      <div class="buttons">
        <button type="button" class="btn btn-gray" data-close onclick={() => (editOpen = false)}>Cancel</button>
        <button class="btn btn-primary">{editIndex >= 0 ? 'Save' : 'Add'}</button>
      </div>
    </form>
  {/if}
</Dialog>

<Dialog bind:open={blockOpen} label="Block the Box port">
  <form class="dlg" onsubmit={(e) => { e.preventDefault(); send(blockText); }}>
    <h2 class="t-title">Block port {boxPort}?</h2>
    <p class="t-sub">NexoVirt Box itself answers on port {boxPort}. Blocking it can lock you out of this page. Type BLOCK to apply it anyway.</p>
    <input class="input mono" bind:value={blockText} placeholder="BLOCK" aria-label="Type BLOCK to confirm" autocomplete="off" />
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => (blockOpen = false)}>Cancel</button>
      <button class="btn btn-danger" disabled={blockText !== 'BLOCK'}>Block it</button>
    </div>
  </form>
</Dialog>

<style>
  .pending { display: flex; align-items: center; gap: 0.875rem; }
  .pending .grow { flex: 1; min-width: 0; display: grid; gap: 0.25rem; }
  .countdown { margin-top: 0.25rem; }
  .countdown > span { transition: transform 1s linear; }
  .ico.warn { color: var(--warn); }

  .stats { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 0.75rem; }
  .stat { display: flex; align-items: center; gap: 0.75rem; padding: 1rem 1.125rem; }
  .stat-ico { display: grid; place-items: center; width: 2.25rem; height: 2.25rem; color: var(--accent); flex-shrink: 0; }
  .stat-text { display: grid; gap: 0.125rem; min-width: 0; }
  .stat-text .t-head { display: flex; align-items: center; gap: 0.4rem; }

  .head-btns { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .pop-wrap { position: relative; }
  .pop-wrap :global(.pop) { width: 17rem; }
  .pop-title { margin: 0.375rem 0.625rem 0.25rem; }
  .pop-item { display: flex; align-items: center; gap: 0.625rem; width: 100%; min-height: 2.75rem; padding: 0.25rem 0.625rem; border: 0; border-radius: 0.625rem; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; }
  .pop-item:hover { background: var(--fill); }
  .pop-item .grow { flex: 1; min-width: 0; display: grid; }
  .pop-ico { display: grid; place-items: center; width: 22px; height: 22px; color: var(--text-2); }

  .table-scroll { overflow-x: auto; padding: 0.25rem 0.5rem 0.5rem; }
  table { width: 100%; border-collapse: separate; border-spacing: 0; font-size: 0.8125rem; }
  th { text-align: left; font-size: 0.75rem; font-weight: 600; color: var(--text-2); padding: 0.5rem 0.625rem; border-bottom: 1px solid var(--hairline); white-space: nowrap; }
  td { padding: 0.375rem 0.625rem; height: 2.875rem; border-bottom: 1px solid var(--hairline); white-space: nowrap; }
  tbody tr:last-child td { border-bottom: 0; }
  tbody tr:hover td { background: var(--fill); }
  tbody tr td:first-child { border-radius: 0.5rem 0 0 0.5rem; }
  tbody tr td:last-child { border-radius: 0 0.5rem 0.5rem 0; }
  .unsaved { margin-left: 0.375rem; color: var(--accent); font-size: 0.6875rem; font-weight: 600; }
  .sr { font-size: 0; }
  .dir { display: inline-flex; align-items: center; gap: 0.375rem; color: var(--text-2); }
  .pill.allow { background: color-mix(in srgb, var(--ok) 12%, transparent); color: var(--text); }
  .pill.deny { background: var(--error-fill); color: var(--text); }
  .note { max-width: 14rem; color: var(--text-2); }
  .muted { color: var(--text-2); }
  .app { display: inline-flex; align-items: center; gap: 0.5rem; max-width: 12rem; }
  .acts { text-align: right; width: 1%; }
  .icon-btn.sm { width: 2.25rem; height: 2.25rem; }

  .empty { display: grid; justify-items: center; gap: 0.375rem; padding: 2rem 1rem 2.25rem; text-align: center; color: var(--text-3); }
  .empty p { margin: 0; }
  .empty .t-head { color: var(--text-2); }

  .save-bar { position: sticky; bottom: 1rem; z-index: 10; display: flex; align-items: center; justify-content: flex-end; gap: 0.625rem; flex-wrap: wrap; }
  .save-bar.dirty { padding: 0.625rem 0.75rem 0.625rem 1.125rem; border-radius: 1.125rem; background: var(--material); box-shadow: var(--shadow-float); -webkit-backdrop-filter: blur(30px) saturate(180%); backdrop-filter: blur(30px) saturate(180%); }
  .save-bar .t-sub { margin-right: auto; }
  .hint { margin: 0 auto 0 0; }

  .foreign-head { width: 100%; border: 0; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; }
  .foreign-head .grow { display: grid; gap: 0.125rem; }
  .foreign-head:hover { background: var(--fill); }
  .chev { color: var(--text-3); transition: transform 250ms var(--spring); display: grid; }
  .chev.open { transform: rotate(90deg); }
  .foreign { margin: 0; padding: 0.875rem 1.125rem 1.125rem; max-height: 24rem; overflow: auto; font-size: 0.75rem; line-height: 1.5; color: var(--text-2); white-space: pre-wrap; border-top: 1px solid var(--hairline); }

  .two { display: grid; grid-template-columns: auto 1fr; gap: 1rem; align-items: start; }

  @media (max-width: 1023px) { .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
  @media (max-width: 639px) { .two { grid-template-columns: 1fr; } }
</style>
