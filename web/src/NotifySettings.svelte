<script>
  // Settings > Notifications: channels, which events go where, and the history.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { withProof } from '$lib/proof.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import { ago } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import Mail from '@lucide/svelte/icons/mail';
  import MessageSquare from '@lucide/svelte/icons/message-square';
  import Bell from '@lucide/svelte/icons/bell';
  import Webhook from '@lucide/svelte/icons/webhook';
  import Plus from '@lucide/svelte/icons/plus';
  import Send from '@lucide/svelte/icons/send';
  import Pencil from '@lucide/svelte/icons/pencil';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import HardDrive from '@lucide/svelte/icons/hard-drive';

  let { onSessionLost } = $props();

  const KINDS = [{ value: 'email', label: 'Email' }, { value: 'discord', label: 'Discord' }, { value: 'ntfy', label: 'ntfy' }, { value: 'webhook', label: 'Webhook' }];
  const ICON = { email: Mail, discord: MessageSquare, ntfy: Bell, webhook: Webhook };
  const KIND_LABEL = { email: 'Email', discord: 'Discord', ntfy: 'ntfy', webhook: 'Webhook' };

  let data = $state(null), rules = $state({}), threshold = $state(90), dirty = $state(false), savingRules = $state(false);
  let open = $state(false), ch = $state(null), busy = $state(false), error = $state(''), origin = $state(null), testing = $state({});

  async function load() {
    const res = await api('/api/notify');
    if (res.status === 401) return onSessionLost();
    if (!res.ok) return toast(res.error, 'error');
    data = res.data;
    rules = structuredClone(res.data.rules);
    threshold = res.data.disk_threshold;
    dirty = false;
  }

  function edit(e, c) {
    origin = { x: e.clientX, y: e.clientY };
    error = '';
    ch = c ? structuredClone($state.snapshot(c)) : { kind: 'ntfy', name: '', enabled: true, config: { server: 'https://ntfy.sh', port: '587', security: 'starttls' } };
    open = true;
  }
  const has = (k) => ch?.has?.includes(k);

  async function save(e) {
    e.preventDefault();
    busy = true;
    error = '';
    const res = await withProof(() => api('/api/notify/channels', { method: 'POST', body: ch }));
    busy = false;
    if (res.cancelled) return;
    if (!res.ok) { error = res.error; return; }
    open = false;
    toast('Channel saved.', 'success');
    load();
  }

  async function test(c) {
    testing = { ...testing, [c.id]: true };
    const res = await withProof(() => api(`/api/notify/channels/${c.id}/test`, { method: 'POST' }));
    testing = { ...testing, [c.id]: false };
    if (res.cancelled) return;
    if (res.ok) toast(`Test message sent to ${c.name}.`, 'success');
    else toast(res.error, 'error', 8000);
    load();
  }

  async function remove(c) {
    const res = await api(`/api/notify/channels/${c.id}/delete`, { method: 'POST' });
    if (!res.ok) return toast(res.error, 'error');
    load();
  }

  function toggleChannel(ev, id) {
    const r = rules[ev];
    if (r.all) { r.all = false; r.channels = data.channels.map((c) => c.id).filter((x) => x !== id); }
    else if (r.channels.includes(id)) r.channels = r.channels.filter((x) => x !== id);
    else r.channels = [...r.channels, id];
    if (data.channels.every((c) => r.channels.includes(c.id))) { r.all = true; r.channels = []; }
    dirty = true;
  }
  const picked = (ev, id) => rules[ev]?.all || rules[ev]?.channels.includes(id);

  async function saveRules() {
    savingRules = true;
    const res = await api('/api/notify/rules', { method: 'POST', body: { rules, disk_threshold: +threshold } });
    savingRules = false;
    if (!res.ok) return toast(res.error, 'error');
    toast('Notification settings saved.', 'success');
    dirty = false;
  }

  const evLabel = (k) => data?.events.find((e) => e.key === k)?.label || (k === 'test' ? 'Test message' : k);

  onMount(load);
</script>

{#if !data}
  <section class="group">{#each Array(3) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</section>
{:else}
  <div class="sub-head">
    <p class="t-section sub">Channels</p>
    <button class="btn btn-tinted btn-sm" onclick={(e) => edit(e, null)}><Plus size={15} />Add channel</button>
  </div>
  <section class="group">
    {#each data.channels as c (c.id)}
      {@const Icon = ICON[c.kind]}
      <div class="row">
        <span class="ico"><Icon size={16} /></span>
        <div class="grow"><span>{c.name}</span><span class="t-caption">{KIND_LABEL[c.kind]}{c.kind === 'ntfy' ? `: ${c.config.server}/${c.config.topic}` : c.kind === 'email' ? `: ${c.config.to}` : ''}{c.enabled ? '' : ', off'}</span></div>
        <button class="btn btn-gray btn-sm" onclick={() => test(c)} disabled={testing[c.id]}>{#if testing[c.id]}<Loader size={15} class="spinner" />{:else}<Send size={15} />{/if}Test</button>
        <button class="icon-btn" onclick={(e) => edit(e, c)} aria-label="Edit {c.name}"><Pencil size={17} /></button>
        <button class="icon-btn" onclick={() => remove(c)} aria-label="Delete {c.name}"><Trash2 size={17} /></button>
      </div>
    {:else}
      <div class="row t-sub">No channel yet. ntfy is the quickest: pick a topic name and subscribe to it in the ntfy app.</div>
    {/each}
  </section>

  <div class="sub-head">
    <p class="t-section sub">Events</p>
    {#if dirty}<button class="btn btn-primary btn-sm" onclick={saveRules} disabled={savingRules}>{#if savingRules}<Loader size={15} class="spinner" />{/if}Save</button>{/if}
  </div>
  <section class="group">
    {#each data.events as ev (ev.key)}
      <div class="row ev">
        <div class="grow"><span>{ev.label}</span><span class="t-caption">{ev.hint}</span></div>
        {#if rules[ev.key].enabled && data.channels.length > 1}
          <div class="chips">
            {#each data.channels as c (c.id)}
              <button class="chip" class:on={picked(ev.key, c.id)} aria-pressed={picked(ev.key, c.id)} onclick={() => toggleChannel(ev.key, c.id)}>{c.name}</button>
            {/each}
          </div>
        {/if}
        <input class="switch" type="checkbox" bind:checked={rules[ev.key].enabled} onchange={() => (dirty = true)} aria-label={ev.label} />
      </div>
    {/each}
    <div class="row">
      <span class="ico"><HardDrive size={16} /></span>
      <div class="grow"><span>Disk threshold</span><span class="t-caption">Warn when a disk is fuller than this.</span></div>
      <span class="thr"><input class="input" type="number" min="50" max="99" bind:value={threshold} oninput={() => (dirty = true)} aria-label="Disk threshold in percent" /> %</span>
    </div>
  </section>
  <p class="t-caption note">The same alert is sent at most once every 30 minutes, and at most 20 messages go out in 10 minutes.</p>

  <p class="t-section sub">Recent notifications</p>
  <section class="group">
    {#each data.history as h, i (i)}
      <div class="row">
        <div class="grow"><span>{h.title}</span><span class="t-caption">{evLabel(h.event)}. {h.result}</span></div>
        <span class="t-caption">{ago(h.at)}</span>
      </div>
    {:else}
      <div class="row t-sub">Nothing was sent yet.</div>
    {/each}
  </section>
{/if}

<Dialog bind:open label="Notification channel" {origin} locked={busy}>
  {#if ch}
    <form class="dlg" onsubmit={save}>
      <h2 class="t-title">{ch.id ? 'Edit channel' : 'New channel'}</h2>
      {#if !ch.id}<Segmented options={KINDS} bind:value={ch.kind} label="Channel type" />{/if}
      {#if error}<div class="notice" role="alert">{error}</div>{/if}
      <label class="field"><span class="field-label">Name</span><input class="input" bind:value={ch.name} maxlength="60" placeholder={KIND_LABEL[ch.kind]} required /></label>
      {#if ch.kind === 'email'}
        <div class="cols">
          <label class="field wide"><span class="field-label">Mail server</span><input class="input mono" bind:value={ch.config.host} placeholder="smtp.example.com" spellcheck="false" required /></label>
          <label class="field"><span class="field-label">Port</span><input class="input" bind:value={ch.config.port} inputmode="numeric" required /></label>
        </div>
        <label class="field"><span class="field-label">Security</span>
          <select class="input" bind:value={ch.config.security}><option value="starttls">STARTTLS (port 587)</option><option value="tls">TLS (port 465)</option><option value="none">None (local server only)</option></select>
        </label>
        <div class="cols">
          <label class="field"><span class="field-label">Username</span><input class="input" bind:value={ch.config.username} autocomplete="off" /></label>
          <label class="field"><span class="field-label">Password</span><input class="input" type="password" bind:value={ch.config.password} autocomplete="new-password" placeholder={has('password') ? 'Saved. Leave empty to keep it.' : ''} /></label>
        </div>
        <label class="field"><span class="field-label">From</span><input class="input" bind:value={ch.config.from} placeholder="box@example.com" required /></label>
        <label class="field"><span class="field-label">To</span><input class="input" bind:value={ch.config.to} placeholder="you@example.com, team@example.com" required /></label>
        <p class="t-caption">The password is stored encrypted. Changing the server or user needs the password again.</p>
      {:else if ch.kind === 'discord'}
        <label class="field"><span class="field-label">Webhook URL</span><input class="input mono" type="password" bind:value={ch.config.url} placeholder={has('url') ? 'Saved. Leave empty to keep it.' : 'https://discord.com/api/webhooks/...'} spellcheck="false" required={!has('url')} /></label>
        <p class="t-caption">In Discord: channel settings, Integrations, Webhooks, New webhook, Copy URL.</p>
      {:else if ch.kind === 'ntfy'}
        <label class="field"><span class="field-label">Server</span><input class="input mono" bind:value={ch.config.server} placeholder="https://ntfy.sh" spellcheck="false" /></label>
        <label class="field"><span class="field-label">Topic</span><input class="input mono" bind:value={ch.config.topic} placeholder="box-alerts-7fq2" spellcheck="false" required /></label>
        <label class="field"><span class="field-label">Access token</span><input class="input mono" type="password" bind:value={ch.config.token} placeholder={has('token') ? 'Saved. Leave empty to keep it.' : 'optional'} autocomplete="off" /></label>
        <p class="t-caption">On the public ntfy.sh server anyone who knows the topic can read it: use a long random topic name or your own server.</p>
      {:else}
        <label class="field"><span class="field-label">URL</span><input class="input mono" type="password" bind:value={ch.config.url} placeholder={has('url') ? 'Saved. Leave empty to keep it.' : 'https://example.com/hook'} spellcheck="false" required={!has('url')} /></label>
        <label class="field"><span class="field-label">Bearer token</span><input class="input mono" type="password" bind:value={ch.config.token} placeholder={has('token') ? 'Saved. Leave empty to keep it.' : 'optional'} autocomplete="off" /></label>
        <p class="t-caption">Box sends a POST with JSON: event, title, message, host and at.</p>
      {/if}
      <label class="row-field"><span class="field-label">Send messages through this channel</span><input class="switch" type="checkbox" bind:checked={ch.enabled} /></label>
      <div class="buttons">
        <button type="button" class="btn btn-gray" data-close onclick={() => (open = false)} disabled={busy}>Cancel</button>
        <button class="btn btn-primary" disabled={busy}>{#if busy}<Loader size={16} class="spinner" />{/if}Save</button>
      </div>
    </form>
  {/if}
</Dialog>

<style>
  h2, p { margin: 0; }
  .sub-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; }
  .sub { margin: 0.75rem 1.125rem 0; }
  .sub-head .sub { margin-bottom: 0; }
  .skeleton.line { display: block; width: 50%; height: 0.8rem; }
  .ev { flex-wrap: wrap; }
  .chips { display: flex; gap: 0.25rem; flex-wrap: wrap; }
  .chip { min-height: 1.875rem; padding: 0 0.625rem; border: 0; border-radius: 999px; background: var(--fill); color: var(--text-2); font: inherit; font-size: 0.75rem; font-weight: 500; cursor: pointer; transition: transform 100ms ease-out; }
  .chip:active { transform: scale(0.95); }
  .chip.on { background: var(--accent-fill); color: var(--accent); }
  .thr { display: inline-flex; align-items: center; gap: 0.375rem; }
  .thr .input { width: 4.75rem; }
  .note { margin: 0 1.125rem; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .field { display: grid; gap: 0.375rem; }
  .cols { display: flex; gap: 0.75rem; flex-wrap: wrap; }
  .cols .field { flex: 1; min-width: 8rem; }
  .cols .wide { flex: 3; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; }
</style>
