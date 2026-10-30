<script>
  // Several boxes: let another box connect to this one (pairing token, shown once, with this
  // box's certificate fingerprint), and add other boxes to switch to from the top bar.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { withProof } from '$lib/proof.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import { ago } from '$lib/format.js';
  import { activeRemote, setRemote } from '$lib/remote.js';
  import Dialog from '$lib/Dialog.svelte';
  import Server from '@lucide/svelte/icons/server';
  import KeyRound from '@lucide/svelte/icons/key-round';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import Copy from '@lucide/svelte/icons/copy';

  let { onSessionLost } = $props();
  let pairing = $state(null), remotes = $state(null), busy = $state(false);
  let pName = $state(''), pScope = $state('full'), made = $state(null), madeOpen = $state(false);
  let add = $state({ name: '', url: '', token: '', fingerprint: '' });
  const viewing = activeRemote();

  async function load() {
    const [p, r] = await Promise.all([api('/api/pairing'), api('/api/remotes')]);
    if (p.status === 401) return onSessionLost?.();
    if (p.ok) pairing = p.data;
    if (r.ok) remotes = r.data;
  }

  async function createPairing(e) {
    e.preventDefault();
    busy = true;
    const res = await withProof(() => api('/api/pairing', { method: 'POST', body: { name: pName, scope: pScope } }));
    busy = false;
    if (res.cancelled) return;
    if (!res.ok) return toast(res.error, 'error');
    made = { ...res.data, url: location.origin }; madeOpen = true; pName = '';
    load();
  }

  async function revoke(t) {
    const res = await api(`/api/pairing/${t.id}/revoke`, { method: 'POST', body: {} });
    if (!res.ok) return toast(res.error, 'error');
    toast(`${t.name} can no longer connect.`, 'success');
    load();
  }

  async function addRemote(e) {
    e.preventDefault();
    busy = true;
    const res = await withProof(() => api('/api/remotes', { method: 'POST', body: add }));
    busy = false;
    if (res.cancelled) return;
    if (!res.ok) return toast(res.error, 'error', 9000);
    toast(`${add.name} is added. Switch to it in the top bar.`, 'success');
    add = { name: '', url: '', token: '', fingerprint: '' };
    load();
  }

  async function removeRemote(r) {
    const res = await api(`/api/remotes/${r.id}/delete`, { method: 'POST', body: {} });
    if (!res.ok) return toast(res.error, 'error');
    if (viewing?.id === r.id) setRemote(null);
    load();
  }

  async function copy(text) {
    try { await navigator.clipboard.writeText(text); toast('Copied.', 'success'); } catch { toast('Select the text and copy it.', 'info'); }
  }

  const fp = (s) => (s || '').match(/.{1,2}/g)?.join(':') || 'not available';
  onMount(load);
</script>

<p class="t-section sub">Other boxes</p>
<section class="group">
  {#each remotes || [] as r (r.id)}
    <div class="row">
      <span class="ico"><Server size={16} /></span>
      <div class="grow"><span>{r.name}</span><span class="t-caption mono">{r.url}</span></div>
      <button class="btn btn-gray btn-sm" onclick={() => setRemote(r)} disabled={viewing?.id === r.id}>{viewing?.id === r.id ? 'Open now' : 'Switch to it'}</button>
      <button class="icon-btn" onclick={() => removeRemote(r)} aria-label="Remove {r.name}"><Trash2 size={16} /></button>
    </div>
  {:else}
    <div class="row t-sub">No other box added. Create a pairing token on the other box (below, on that box) and add it here.</div>
  {/each}
  <form class="row add" onsubmit={addRemote}>
    <input class="input" bind:value={add.name} placeholder="Name, for example NAS" maxlength="40" required />
    <input class="input mono" bind:value={add.url} placeholder="https://192.168.1.20:8443" required spellcheck="false" />
    <input class="input mono" bind:value={add.token} placeholder="Pairing token (nxbp_...)" required spellcheck="false" autocomplete="off" />
    <input class="input mono" bind:value={add.fingerprint} placeholder="Certificate fingerprint shown on the other box" required spellcheck="false" />
    <button class="btn btn-primary btn-sm" disabled={busy}>Add box</button>
  </form>
</section>

<p class="t-section sub">Let another box connect to this one</p>
<section class="group">
  <div class="row t-caption">This box's certificate fingerprint: <span class="mono fp">{fp(pairing?.fingerprint)}</span></div>
  {#each pairing?.tokens || [] as t (t.id)}
    <div class="row">
      <span class="ico"><KeyRound size={16} /></span>
      <div class="grow"><span>{t.name}</span><span class="t-caption">{t.scope === 'full' ? 'Full access' : 'Read only'} · {t.last_used ? `used ${ago(t.last_used)}` : 'not used yet'}</span></div>
      <button class="icon-btn" onclick={() => revoke(t)} aria-label="Revoke {t.name}"><Trash2 size={16} /></button>
    </div>
  {/each}
  <form class="row add" onsubmit={createPairing}>
    <input class="input" bind:value={pName} placeholder="Which box connects, for example Main box" maxlength="40" required />
    <select class="input" bind:value={pScope}><option value="full">Full access (apps, files, backups)</option><option value="read">Read only</option></select>
    <button class="btn btn-tinted btn-sm" disabled={busy}>Create pairing token</button>
  </form>
  <div class="row t-caption">A paired box never reaches sign-in, account, two-step sign-in or the terminal here, and actions that ask for a password stay on this box.</div>
</section>

<Dialog bind:open={madeOpen} label="Pairing token">
  {#if made}
    <div class="dlg">
      <h2 class="t-title">Pairing token</h2>
      <p class="t-sub">Enter these on the other box under Settings > Network > Other boxes. The token is shown only now.</p>
      <div class="kv"><span class="t-caption">Address</span><code class="mono">{made.url}</code></div>
      <div class="kv"><span class="t-caption">Token</span><code class="mono">{made.token}</code><button class="icon-btn" onclick={() => copy(made.token)} aria-label="Copy the token"><Copy size={15} /></button></div>
      <div class="kv"><span class="t-caption">Fingerprint</span><code class="mono">{made.fingerprint}</code><button class="icon-btn" onclick={() => copy(made.fingerprint)} aria-label="Copy the fingerprint"><Copy size={15} /></button></div>
      <div class="buttons"><button class="btn btn-primary" data-close onclick={() => { madeOpen = false; made = null; }}>Done</button></div>
    </div>
  {/if}
</Dialog>

<style>
  h2, p { margin: 0; }
  .grow { flex: 1; min-width: 0; display: grid; }
  .add { flex-wrap: wrap; gap: 0.5rem; }
  .add .input { flex: 1 1 12rem; min-width: 0; }
  .fp { word-break: break-all; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .kv { display: grid; grid-template-columns: 6rem 1fr auto; align-items: center; gap: 0.5rem; }
  .kv code { word-break: break-all; font-size: 0.75rem; }
  .buttons { display: flex; justify-content: flex-end; }
</style>
