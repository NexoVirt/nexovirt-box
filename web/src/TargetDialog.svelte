<script>
  // Add or edit a backup target. Secrets are never sent back by the server: an empty secret field
  // keeps the stored one.
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { withProof } from '$lib/proof.svelte.js';
  import Dialog from '$lib/Dialog.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { open = $bindable(false), target = null, origin = null, pbsClient = false, defaultPath = '', onSaved } = $props();

  const TYPES = [{ value: 'local', label: 'Folder' }, { value: 's3', label: 'S3' }, { value: 'pbs', label: 'Proxmox Backup' }];
  let t = $state({}), busy = $state(false), error = $state('');

  $effect(() => {
    if (open) {
      error = '';
      t = target ? { ...target, secret_key: '', password: '' } : { type: 'local', name: 'This server', path: defaultPath, tls: true };
    }
  });
  const has = (k) => target?.has?.includes(k);

  async function save(e) {
    e.preventDefault();
    busy = true;
    error = '';
    const res = await withProof(() => api('/api/backups/targets', { method: 'POST', body: t }));
    busy = false;
    if (res.cancelled) return;
    if (!res.ok) { error = res.error; return; }
    open = false;
    toast(`${res.data.name} saved.`, 'success');
    onSaved?.();
  }
</script>

<Dialog bind:open label="Backup target" {origin} locked={busy}>
  <form class="dlg" onsubmit={save}>
    <h2 class="t-title">{target ? 'Edit target' : 'New backup target'}</h2>
    {#if !target}<Segmented options={TYPES} bind:value={t.type} label="Target type" />{/if}
    {#if error}<div class="notice" role="alert">{error}</div>{/if}
    <label class="field"><span class="field-label">Name</span><input class="input" bind:value={t.name} required maxlength="60" /></label>
    {#if t.type === 'local'}
      <label class="field"><span class="field-label">Folder</span><input class="input mono" bind:value={t.path} placeholder="/srv/backups" spellcheck="false" required /></label>
      <p class="t-caption">A folder on this server, for example a mounted USB disk or NAS share. Backups on the same disk as the apps do not survive a broken disk.</p>
    {:else if t.type === 's3'}
      <label class="field"><span class="field-label">Address</span><input class="input mono" bind:value={t.endpoint} placeholder="s3.eu-central-1.amazonaws.com" spellcheck="false" required /></label>
      <div class="cols">
        <label class="field"><span class="field-label">Bucket</span><input class="input mono" bind:value={t.bucket} spellcheck="false" required /></label>
        <label class="field"><span class="field-label">Folder in the bucket</span><input class="input mono" bind:value={t.prefix} placeholder="optional" spellcheck="false" /></label>
      </div>
      <div class="cols">
        <label class="field"><span class="field-label">Access key</span><input class="input mono" bind:value={t.access_key} spellcheck="false" autocomplete="off" required /></label>
        <label class="field"><span class="field-label">Secret key</span><input class="input mono" type="password" bind:value={t.secret_key} autocomplete="new-password" placeholder={has('secret_key') ? 'Saved. Leave empty to keep it.' : ''} required={!has('secret_key')} /></label>
      </div>
      <div class="cols">
        <label class="field"><span class="field-label">Region</span><input class="input mono" bind:value={t.region} placeholder="optional, for example eu-central-1" spellcheck="false" /></label>
        <label class="row-field tls"><span class="field-label">HTTPS</span><input class="switch" type="checkbox" bind:checked={t.tls} /></label>
      </div>
      <p class="t-caption">Works with AWS S3, Backblaze B2, Wasabi, Cloudflare R2, MinIO and others. Plain HTTP only to this server or a private network.</p>
    {:else}
      {#if !pbsClient}<div class="notice neutral">This server has no proxmox-backup-client. Install the package proxmox-backup-client from the Proxmox repository before you use this target.</div>{/if}
      <label class="field"><span class="field-label">Repository</span><input class="input mono" bind:value={t.repository} placeholder="backup@pbs@10.0.0.5:datastore" spellcheck="false" required /></label>
      <label class="field"><span class="field-label">Password or API token secret</span><input class="input mono" type="password" bind:value={t.password} autocomplete="new-password" placeholder={has('password') ? 'Saved. Leave empty to keep it.' : ''} /></label>
      <label class="field"><span class="field-label">Fingerprint</span><input class="input mono" bind:value={t.fingerprint} placeholder="optional when the certificate is trusted" spellcheck="false" /></label>
    {/if}
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => (open = false)} disabled={busy}>Cancel</button>
      <button class="btn btn-primary" disabled={busy}>{#if busy}<Loader size={16} class="spinner" />{/if}Save</button>
    </div>
  </form>
</Dialog>

<style>
  h2, p { margin: 0; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .field { display: grid; gap: 0.375rem; }
  .cols { display: flex; gap: 0.75rem; flex-wrap: wrap; align-items: end; }
  .cols .field { flex: 1; min-width: 10rem; }
  .tls { flex: 0 0 auto; min-height: 2.75rem; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; }
</style>
