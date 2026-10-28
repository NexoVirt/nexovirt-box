<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import Dialog from '$lib/Dialog.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import Shield from '@lucide/svelte/icons/shield';
  import ShieldAlert from '@lucide/svelte/icons/shield-alert';
  import Plus from '@lucide/svelte/icons/plus';
  import Trash from '@lucide/svelte/icons/trash-2';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import Undo from '@lucide/svelte/icons/undo-2';

  let { onSessionLost } = $props();

  const BACKENDS = { nftables: 'nftables', ufw: 'ufw', firewalld: 'firewalld' };
  const boxPort = Number(location.port || (location.protocol === 'https:' ? 443 : 80));

  let status = $state(null);
  let error = $state('');
  let draft = $state([]);
  let apps = $state([]);
  let saving = $state(false);
  let pending = $state(null); // { id, left }
  let blockOpen = $state(false), blockText = $state('');
  let tick;

  async function load() {
    const [f, a] = await Promise.all([api('/api/firewall'), api('/api/apps')]);
    if (f.status === 401) return onSessionLost();
    if (!f.ok) {
      error = f.error;
      return;
    }
    error = '';
    status = f.data;
    draft = f.data.rules.map((r) => ({ ...r }));
    if (a.ok) apps = a.data;
    if (f.data.pending) startCountdown(f.data.pending.id, f.data.pending.seconds_left);
  }

  const dirty = $derived(status && JSON.stringify(draft) !== JSON.stringify(status.rules));

  function add(rule = {}) {
    draft.push({ id: '', action: 'allow', proto: 'tcp', port: '', source: '', comment: '', ...rule });
  }

  function openApp(e) {
    const app = apps.find((x) => x.id === e.currentTarget.value);
    e.currentTarget.value = '';
    if (!app) return;
    if (draft.some((r) => r.action === 'allow' && r.port === String(app.main_port))) return toast(`Port ${app.main_port} is already allowed.`);
    add({ port: String(app.main_port), comment: app.name.slice(0, 60) });
  }

  function blocksBox() {
    return draft.some((r) => {
      if (r.action !== 'deny' || r.proto !== 'tcp') return false;
      const [lo, hi = lo] = String(r.port).split('-').map(Number);
      return lo <= boxPort && boxPort <= hi;
    });
  }

  function apply() {
    if (blocksBox()) {
      blockText = '';
      blockOpen = true;
      return;
    }
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
  }

  function startCountdown(id, left) {
    pending = { id, left };
    clearInterval(tick);
    tick = setInterval(() => {
      if (!pending) return clearInterval(tick);
      pending.left -= 1;
      if (pending.left <= 0) {
        clearInterval(tick);
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
    clearInterval(tick);
    pending = null;
    toast('The firewall rules are saved.', 'success');
    load();
  }

  onMount(() => {
    load();
    return () => clearInterval(tick);
  });
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Firewall</h1>
      <p class="t-sub">Open or block ports. Box only changes its own rules and leaves everything else as it is.</p>
    </div>
    {#if status?.backend}<span class="pill"><Shield size={14} />{BACKENDS[status.backend]}</span>{/if}
  </header>

  {#if pending}
    <div class="tile pending" role="status">
      <span class="ico warn"><ShieldAlert size={18} /></span>
      <div class="grow">
        <span class="t-head">Keep these rules?</span>
        <span class="t-caption">They are active now. If you do not confirm, Box puts the previous rules back in <span class="num">{pending.left}</span> seconds, so you cannot lock yourself out.</span>
      </div>
      <button class="btn btn-primary btn-sm" onclick={keep}>Keep rules</button>
    </div>
  {/if}

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else if !status}
    <div class="group">{#each Array(3) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</div>
  {:else if !status.backend}
    <div class="notice"><CircleAlert size={18} /><span>No firewall tool was found. Install nftables on the server (apt install nftables), then reload this page.</span></div>
  {:else}
    <section class="group">
      <div class="group-head">
        <h2 class="t-head">Box rules</h2>
        <div class="head-btns">
          {#if apps.length}
            <select class="input select-sm" onchange={openApp} aria-label="Open the port of an app">
              <option value="">Open an app's port</option>
              {#each apps as a (a.id)}<option value={a.id}>{a.name} (port {a.main_port})</option>{/each}
            </select>
          {/if}
          <button class="btn btn-tinted btn-sm" onclick={() => add()}><Plus size={15} />Add rule</button>
        </div>
      </div>
      {#each draft as r, i (i)}
        <div class="row rule">
          <Segmented options={[{ value: 'allow', label: 'Allow' }, { value: 'deny', label: 'Block' }]} bind:value={r.action} label="Allow or block" />
          <select class="input proto" bind:value={r.proto} aria-label="Protocol"><option value="tcp">TCP</option><option value="udp">UDP</option></select>
          <input class="input port num" bind:value={r.port} placeholder="Port, e.g. 8080" aria-label="Port or range" inputmode="numeric" />
          <input class="input source" bind:value={r.source} placeholder="From anywhere" aria-label="Source address or network" spellcheck="false" />
          <input class="input note" bind:value={r.comment} placeholder="Note" aria-label="Note" maxlength="60" />
          <button class="icon-btn" aria-label="Remove rule" onclick={() => draft.splice(i, 1)}><Trash size={17} /></button>
        </div>
      {:else}
        <div class="row t-sub">No rules from Box yet. Everything the server allowed before still applies.</div>
      {/each}
    </section>

    <div class="actions">
      {#if status.backend === 'ufw'}<p class="t-caption hint">Ports of Docker apps are opened by Docker itself and are not affected by ufw rules.</p>{/if}
      {#if dirty}<button class="btn btn-gray" onclick={() => (draft = status.rules.map((r) => ({ ...r })))}><Undo size={16} />Discard</button>{/if}
      <button class="btn btn-primary" onclick={apply} disabled={!dirty || saving || !!pending}>
        {#if saving}<Loader size={16} class="spinner" />Applying{:else}Apply rules{/if}
      </button>
    </div>

    <details class="group foreign">
      <summary class="row"><span class="t-head">Other rules on this server</span><span class="t-caption">{status.foreign.length} lines, read only</span></summary>
      <pre class="mono">{status.foreign.join('\n') || 'None.'}</pre>
    </details>
  {/if}
</div>

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
  .ico.warn { background: color-mix(in srgb, var(--warn) 16%, transparent); color: var(--warn); }
  .head-btns { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .select-sm { min-height: 2.25rem; padding-block: 0; width: auto; font-size: 0.8125rem; }
  .rule { gap: 0.625rem; flex-wrap: wrap; }
  .rule .input { min-height: 2.25rem; }
  .proto { width: 5.5rem; }
  .port { width: 8.5rem; }
  .source { width: 11rem; }
  .note { flex: 1; min-width: 8rem; }
  .actions { display: flex; justify-content: flex-end; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
  .hint { margin: 0 auto 0 0; }
  .foreign summary { cursor: pointer; list-style: none; justify-content: space-between; }
  .foreign pre { margin: 0; padding: 0.875rem 1.125rem 1.125rem; max-height: 22rem; overflow: auto; font-size: 0.75rem; line-height: 1.5; color: var(--text-2); white-space: pre-wrap; }
  @media (max-width: 639px) {
    .proto, .port, .source { flex: 1; width: auto; }
  }
</style>
