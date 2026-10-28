<script>
  // Creates a public download link for one file or folder: expiry, optional password and
  // download limit. The link is shown once (Box only keeps a hash of it).
  import { api } from './api.js';
  import { toast } from './toast.svelte.js';
  import Dialog from './Dialog.svelte';
  import Segmented from './Segmented.svelte';
  import Link from '@lucide/svelte/icons/link';
  import Copy from '@lucide/svelte/icons/copy';
  import Check from '@lucide/svelte/icons/check';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { open = $bindable(false), path = '', name = '', dir = false, origin = null, onCreated = () => {} } = $props();

  const EXPIRY = [
    { value: '1', label: '1 hour' },
    { value: '24', label: '1 day' },
    { value: '168', label: '7 days' },
    { value: '720', label: '30 days' },
  ];
  let hours = $state('168');
  let usePassword = $state(false), password = $state('');
  let useLimit = $state(false), limit = $state(1);
  let busy = $state(false), error = $state('');
  let url = $state(''), copied = $state(false);

  $effect(() => {
    if (open) { hours = '168'; usePassword = false; password = ''; useLimit = false; limit = 1; error = ''; url = ''; copied = false; }
  });

  async function create(e) {
    e.preventDefault();
    error = '';
    if (usePassword && (password.length < 6 || password.length > 72)) { error = 'The password needs 6 to 72 characters.'; return; }
    if (useLimit && !(limit >= 1 && limit <= 10000)) { error = 'The download limit must be between 1 and 10000.'; return; }
    busy = true;
    const res = await api('/api/share-links', {
      method: 'POST',
      body: { path, expires_hours: Number(hours), password: usePassword ? password : '', max_downloads: useLimit ? Number(limit) : 0 },
    });
    busy = false;
    if (!res.ok) { error = res.error; return; }
    url = res.data.url;
    onCreated(res.data.link);
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(url);
      copied = true;
      setTimeout(() => (copied = false), 1800);
    } catch {
      toast('Copying is blocked by the browser. Select the link and copy it by hand.', 'error');
    }
  }
</script>

<Dialog bind:open label="Share link" {origin}>
  {#if url}
    <div class="dlg">
      <div class="head"><span class="ico md accent"><Link /></span><h2 class="t-title">Link ready</h2></div>
      <p class="t-sub">Anyone with this link can download {name}{dir ? ' as a zip file' : ''}. Copy it now: Box keeps only a fingerprint of it and cannot show it again.</p>
      <div class="url-row">
        <input class="input mono" value={url} readonly aria-label="Share link" onfocus={(e) => e.target.select()} />
        <button class="btn btn-tinted" onclick={copy} aria-label="Copy link">
          {#if copied}<Check size={16} />Copied{:else}<Copy size={16} />Copy{/if}
        </button>
      </div>
      <p class="t-caption">You can revoke it any time under Shares, Links.</p>
      <div class="buttons"><button class="btn btn-primary" data-close onclick={() => (open = false)}>Done</button></div>
    </div>
  {:else}
    <form class="dlg" onsubmit={create}>
      <div class="head"><span class="ico md accent"><Link /></span><h2 class="t-title">Share {dir ? 'folder' : 'file'}</h2></div>
      <p class="t-sub truncate" title={path}>{name}</p>
      {#if error}<div class="notice" role="alert">{error}</div>{/if}
      <div class="field">
        <span class="field-label">Link works for</span>
        <Segmented options={EXPIRY} bind:value={hours} label="Expiry" />
      </div>
      <label class="row-field">
        <span class="field-text"><span class="t-head">Password</span><span class="t-caption">Ask for a password before the download.</span></span>
        <input type="checkbox" class="switch" bind:checked={usePassword} />
      </label>
      {#if usePassword}
        <input class="input" type="password" bind:value={password} placeholder="6 characters or more" autocomplete="new-password" aria-label="Link password" />
      {/if}
      <label class="row-field">
        <span class="field-text"><span class="t-head">Download limit</span><span class="t-caption">The link stops working after this many downloads.</span></span>
        <input type="checkbox" class="switch" bind:checked={useLimit} />
      </label>
      {#if useLimit}
        <input class="input num" type="number" min="1" max="10000" bind:value={limit} aria-label="Number of downloads" />
      {/if}
      <div class="buttons">
        <button type="button" class="btn btn-gray" data-close onclick={() => (open = false)}>Cancel</button>
        <button class="btn btn-primary" disabled={busy}>{#if busy}<Loader size={16} class="spinner" />Creating{:else}Create link{/if}</button>
      </div>
    </form>
  {/if}
</Dialog>

<style>
  .head { display: flex; align-items: center; gap: 0.625rem; }
  .url-row { display: flex; gap: 0.5rem; }
  .url-row .input { flex: 1; min-width: 0; }
  @media (max-width: 479px) { .url-row { flex-direction: column; } }
</style>
