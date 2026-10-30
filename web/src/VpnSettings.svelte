<script>
  // One-click WireGuard VPN: reach this box and its local network from anywhere (no internet
  // through the box). A device's config and QR code are shown once.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { withProof } from '$lib/proof.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import { ago } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import ShieldEllipsis from '@lucide/svelte/icons/shield-ellipsis';
  import Smartphone from '@lucide/svelte/icons/smartphone';
  import Copy from '@lucide/svelte/icons/copy';
  import Trash2 from '@lucide/svelte/icons/trash-2';

  let { onSessionLost } = $props();
  let st = $state(null), busy = $state(false), line = $state(''), endpoint = $state(''), port = $state(51820), devName = $state('');
  let shown = $state(null), shownOpen = $state(false);

  async function load() {
    const res = await api('/api/vpn');
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) return toast(res.error, 'error');
    st = res.data;
    if (!endpoint) endpoint = st.endpoint || st.public_ip || '';
    if (st.port) port = st.port;
  }

  async function turn(on) {
    busy = true;
    if (on && !st.installed) {
      const job = await runJob('/api/system/install', { op: 'wireguard' }, (j) => (line = j.lines.at(-1) || ''));
      line = '';
      if (job.status !== 'done') { busy = false; return toast(job.error || 'WireGuard could not be installed.', 'error', 9000); }
    }
    const res = await withProof(() => api('/api/vpn', { method: 'POST', body: { on, port: Number(port), endpoint } }));
    busy = false;
    if (res.cancelled) return;
    if (!res.ok) return toast(res.error, 'error', 9000);
    toast(on ? 'The VPN is on. Add a device below.' : 'The VPN is off.', 'success');
    load();
  }

  async function addDevice(e) {
    e.preventDefault();
    busy = true;
    const res = await withProof(() => api('/api/vpn/clients', { method: 'POST', body: { name: devName } }));
    busy = false;
    if (res.cancelled) return;
    if (!res.ok) return toast(res.error, 'error', 8000);
    shown = res.data; shownOpen = true; devName = '';
    load();
  }

  async function revoke(c) {
    const res = await api('/api/vpn/clients/revoke', { method: 'POST', body: { name: c.name } });
    if (!res.ok) return toast(res.error, 'error');
    toast(`${c.name} can no longer connect.`, 'success');
    load();
  }

  async function copy() {
    try { await navigator.clipboard.writeText(shown.config); toast('Copied.', 'success'); } catch { toast('Select the text and copy it.', 'info'); }
  }

  onMount(load);
</script>

<p class="t-section sub">VPN (WireGuard)</p>
<section class="group">
  <div class="row">
    <span class="ico"><ShieldEllipsis size={16} /></span>
    <div class="grow">
      <span>Reach this box from anywhere</span>
      <span class="t-caption">{busy && line ? line : st?.enabled ? `On: devices reach the box at ${st.subnet?.replace('.0/24', '.1')} and your network ${st.lan}. Internet traffic does not go through the box.` : 'Devices connect with the WireGuard app and reach only this box and your local network.'}</span>
    </div>
    {#if busy}<Loader size={16} class="spinner" />{/if}
    {#if st}<input class="switch" type="checkbox" checked={st.enabled} disabled={busy || (!st.enabled && !endpoint)} onchange={(e) => turn(e.currentTarget.checked)} aria-label="VPN" />{/if}
  </div>
  {#if st}
    <div class="row cfg">
      <label class="field grow"><span class="field-label">Address devices connect to</span><input class="input mono" bind:value={endpoint} placeholder="box.example.com or your public IP" spellcheck="false" disabled={busy} /></label>
      <label class="field"><span class="field-label">UDP port</span><input class="input" type="number" min="1024" max="65535" bind:value={port} disabled={busy} /></label>
      {#if st.enabled}<button class="btn btn-tinted btn-sm" onclick={() => turn(true)} disabled={busy}>Save</button>{/if}
    </div>
    {#if st.behind_nat}
      <div class="row t-caption">This box is behind a router (public address {st.public_ip}). Forward UDP port {port} on the router to {st.lan_ip}, or devices outside your network cannot connect.</div>
    {/if}
  {/if}
</section>

{#if st?.enabled}
  <section class="group">
    {#each st.clients as c (c.name)}
      <div class="row">
        <span class="ico"><Smartphone size={16} /></span>
        <div class="grow"><span>{c.name}</span><span class="t-caption mono">{c.ip} · {c.handshake ? `last seen ${ago(c.handshake)}` : 'not connected yet'}</span></div>
        <button class="icon-btn" onclick={() => revoke(c)} aria-label="Remove {c.name}" title="Remove"><Trash2 size={16} /></button>
      </div>
    {:else}
      <div class="row t-sub">No device yet.</div>
    {/each}
    <form class="row" onsubmit={addDevice}>
      <input class="input grow" bind:value={devName} placeholder="Device name, for example My phone" maxlength="40" required disabled={busy} />
      <button class="btn btn-primary btn-sm" disabled={busy || !devName.trim()}>Add device</button>
    </form>
  </section>
{/if}

<Dialog bind:open={shownOpen} label="VPN device">
  {#if shown}
    <div class="dlg">
      <h2 class="t-title">{shown.name} is ready</h2>
      <p class="t-sub">Scan the code with the WireGuard app, or import the text as a file. It is shown only now: the private key is not kept on the box.</p>
      {#if shown.qr}<img class="qr" src={shown.qr} alt="WireGuard configuration as a QR code" />{/if}
      <pre class="conf mono">{shown.config}</pre>
      <div class="buttons">
        <button class="btn btn-gray" onclick={copy}><Copy size={15} />Copy</button>
        <button class="btn btn-primary" data-close onclick={() => { shownOpen = false; shown = null; }}>Done</button>
      </div>
    </div>
  {/if}
</Dialog>

<style>
  h2, p { margin: 0; }
  .grow { flex: 1; min-width: 0; display: grid; }
  .cfg { flex-wrap: wrap; align-items: flex-end; gap: 0.75rem; }
  .field { display: grid; gap: 0.25rem; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .qr { width: 15rem; height: 15rem; justify-self: center; background: #fff; image-rendering: pixelated; }
  .conf { margin: 0; padding: 0.75rem; border-radius: var(--r-control); background: var(--fill); font-size: 0.75rem; white-space: pre-wrap; word-break: break-all; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; }
</style>
