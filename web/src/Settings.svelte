<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { createPasskey, friendly, passkeysSupported } from '$lib/webauthn.js';
  import { ago } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import Smartphone from '@lucide/svelte/icons/smartphone';
  import KeyRound from '@lucide/svelte/icons/key-round';
  import LifeBuoy from '@lucide/svelte/icons/life-buoy';
  import Package from '@lucide/svelte/icons/package';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Trash from '@lucide/svelte/icons/trash-2';
  import Copy from '@lucide/svelte/icons/copy';
  import Plus from '@lucide/svelte/icons/plus';

  let { onSessionLost } = $props();

  let tfa = $state(null);
  let settings = $state(null);
  let error = $state('');

  // Password dialog: every 2FA change needs the current password first.
  let pwOpen = $state(false), pwTitle = $state(''), pwText = $state(''), pwNeedsCode = $state(false);
  let pw = $state(''), pwCode = $state(''), pwBusy = $state(false), pwError = $state('');
  let pwAction = null, origin = $state(null);

  // TOTP setup dialog.
  let totpOpen = $state(false), totp = $state(null), totpCode = $state(''), totpBusy = $state(false), totpError = $state('');
  // Recovery codes, shown once.
  let codesOpen = $state(false), codes = $state([]);
  let keyName = $state('');

  let catalogURL = $state(''), savingURL = $state(false), syncing = $state(false);

  async function load() {
    const [a, b] = await Promise.all([api('/api/2fa'), api('/api/settings')]);
    if (a.status === 401 || b.status === 401) return onSessionLost();
    if (!a.ok || !b.ok) {
      error = (a.ok ? b : a).error;
      return;
    }
    tfa = a.data;
    settings = b.data;
    catalogURL = settings.catalog_url || '';
  }

  function askPassword(e, title, text, needsCode, action) {
    origin = e ? { x: e.clientX, y: e.clientY } : null;
    pwTitle = title; pwText = text; pwNeedsCode = needsCode; pwAction = action;
    pw = ''; pwCode = ''; pwError = '';
    pwOpen = true;
  }

  async function submitPassword(e) {
    e.preventDefault();
    pwBusy = true;
    pwError = '';
    const err = await pwAction(pw, pwCode);
    pwBusy = false;
    if (err) {
      pwError = err;
      return;
    }
    pwOpen = false;
  }

  function showCodes(list) {
    if (list && list.length) {
      codes = list;
      codesOpen = true;
    }
  }

  const startTOTP = (e) => askPassword(e, 'Set up an authenticator app', 'Enter your password to continue.', false, async (password) => {
    const res = await api('/api/2fa/totp/begin', { method: 'POST', body: { password } });
    if (!res.ok) return res.error;
    totp = res.data;
    totpCode = ''; totpError = '';
    setTimeout(() => (totpOpen = true), 150);
  });

  async function enableTOTP(e) {
    e.preventDefault();
    totpBusy = true;
    totpError = '';
    const res = await api('/api/2fa/totp/enable', { method: 'POST', body: { code: totpCode } });
    totpBusy = false;
    if (!res.ok) {
      totpError = res.error;
      return;
    }
    totpOpen = false;
    toast('Two-step sign-in is on.', 'success');
    showCodes(res.data.recovery_codes);
    load();
  }

  const disableTOTP = (e) => askPassword(e, 'Turn off the authenticator app', 'Enter your password and a current code.', true, async (password, code) => {
    const res = await api('/api/2fa/totp/disable', { method: 'POST', body: { password, code } });
    if (!res.ok) return res.error;
    toast('The authenticator app is turned off.');
    load();
  });

  const newCodes = (e) => askPassword(e, 'New recovery codes', 'Your old recovery codes stop working.', false, async (password) => {
    const res = await api('/api/2fa/recovery', { method: 'POST', body: { password } });
    if (!res.ok) return res.error;
    setTimeout(() => showCodes(res.data.recovery_codes), 150);
    load();
  });

  const addPasskey = (e) => askPassword(e, 'Add a passkey', 'Enter your password, then follow your browser.', false, async (password) => {
    const res = await api('/api/2fa/passkey/begin', { method: 'POST', body: { password } });
    if (!res.ok) return res.error;
    let cred;
    try {
      cred = await createPasskey(res.data);
    } catch (err) {
      return friendly(err);
    }
    const name = keyName.trim() || 'Passkey';
    const fin = await fetch('/api/2fa/passkey/finish?name=' + encodeURIComponent(name), {
      method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(cred),
    }).catch(() => null);
    const body = fin ? await fin.json().catch(() => ({})) : {};
    if (!fin || !fin.ok) return body.error || 'The passkey could not be added.';
    keyName = '';
    toast('Passkey added.', 'success');
    setTimeout(() => showCodes(body.recovery_codes), 150);
    load();
  });

  const removePasskey = (e, k) => askPassword(e, `Remove "${k.name}"?`, 'You can no longer sign in with this passkey.', false, async (password) => {
    const res = await api(`/api/2fa/passkey/${encodeURIComponent(k.id)}/delete`, { method: 'POST', body: { password } });
    if (!res.ok) return res.error;
    toast('Passkey removed.');
    load();
  });

  async function copyCodes() {
    try {
      await navigator.clipboard.writeText(codes.join('\n'));
      toast('Copied.');
    } catch {
      toast('Copying is blocked here. Select the codes and copy them by hand.', 'error');
    }
  }

  async function saveURL(e) {
    e.preventDefault();
    savingURL = true;
    const res = await api('/api/settings', { method: 'POST', body: { catalog_url: catalogURL } });
    savingURL = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) return toast(res.error, 'error');
    toast('Saved.', 'success');
  }

  async function sync() {
    syncing = true;
    const res = await api('/api/catalog/sync', { method: 'POST' });
    syncing = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) return toast(res.error, 'error');
    toast(res.data.message || 'The app catalog is up to date.', 'success');
  }

  onMount(load);
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Settings</h1>
      <p class="t-sub">Sign-in security and where Box gets its apps.</p>
    </div>
  </header>

  {#if error}
    <div class="notice">{error}</div>
  {:else if !tfa}
    <div class="group">{#each Array(4) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</div>
  {:else}
    <section class="group">
      <div class="group-head"><h2 class="t-head">Two-step sign-in</h2></div>
      <div class="row">
        <span class="ico"><Smartphone size={16} /></span>
        <div class="grow">
          <span>Authenticator app</span>
          <span class="t-caption">{tfa.totp ? 'On. Sign-in and the console ask for a code.' : 'Codes from an app like 1Password, Google Authenticator or Aegis.'}</span>
        </div>
        {#if tfa.totp}
          <button class="btn btn-gray btn-sm" onclick={disableTOTP}>Turn off</button>
        {:else}
          <button class="btn btn-tinted btn-sm" onclick={startTOTP}>Set up</button>
        {/if}
      </div>
      {#each tfa.passkeys as k (k.id)}
        <div class="row">
          <span class="ico"><KeyRound size={16} /></span>
          <div class="grow">
            <span class="truncate">{k.name}</span>
            <span class="t-caption">Added {ago(k.created_at)}{k.last_used_at ? `, last used ${ago(k.last_used_at)}` : ''}</span>
          </div>
          <button class="icon-btn" aria-label="Remove passkey {k.name}" onclick={(e) => removePasskey(e, k)}><Trash size={17} /></button>
        </div>
      {/each}
      <div class="row">
        <span class="ico"><KeyRound size={16} /></span>
        <div class="grow">
          <span>Passkeys</span>
          <span class="t-caption">
            {#if !passkeysSupported()}This browser has no passkey support.
            {:else if !tfa.passkeys_available}Passkeys need a domain name with a trusted certificate (or localhost). Open Box by its name to add one.
            {:else}Sign in with Touch ID, Face ID, Windows Hello or a security key.{/if}
          </span>
        </div>
        {#if tfa.passkeys_available && passkeysSupported()}
          <input class="input key-name" bind:value={keyName} placeholder="Name, e.g. MacBook" aria-label="Passkey name" maxlength="60" />
          <button class="btn btn-tinted btn-sm" onclick={addPasskey}><Plus size={15} />Add</button>
        {/if}
      </div>
      {#if tfa.totp || tfa.passkeys.length}
        <div class="row">
          <span class="ico"><LifeBuoy size={16} /></span>
          <div class="grow">
            <span>Recovery codes</span>
            <span class="t-caption">{tfa.recovery_left} of 10 left. Each one signs you in once if you lose your phone or key.</span>
          </div>
          <button class="btn btn-gray btn-sm" onclick={newCodes}>New codes</button>
        </div>
      {/if}
    </section>

    <section class="group">
      <div class="group-head"><h2 class="t-head">App catalog</h2></div>
      <form class="row catalog" onsubmit={saveURL}>
        <span class="ico"><Package size={16} /></span>
        <div class="grow">
          <label class="field-label" for="catalog-url">Online catalog address</label>
          <input id="catalog-url" class="input" bind:value={catalogURL} placeholder="https://apps.example.com/catalog" inputmode="url" spellcheck="false" />
          <span class="t-caption">Leave empty to use the catalog built into Box. Online catalogs must be signed by NexoVirt.</span>
        </div>
        <div class="catalog-btns">
          <button class="btn btn-gray btn-sm" disabled={savingURL}>{#if savingURL}<Loader size={15} class="spinner" />{/if}Save</button>
          <button type="button" class="btn btn-tinted btn-sm" onclick={sync} disabled={syncing}>
            {#if syncing}<Loader size={15} class="spinner" />Syncing{:else}<RefreshCw size={15} />Sync now{/if}
          </button>
        </div>
      </form>
    </section>
  {/if}
</div>

<Dialog bind:open={pwOpen} label={pwTitle} {origin} locked={pwBusy}>
  <form class="dlg" onsubmit={submitPassword}>
    <h2 class="t-title">{pwTitle}</h2>
    <p class="t-sub">{pwText}</p>
    {#if pwError}<div class="notice" role="alert">{pwError}</div>{/if}
    <input class="input" type="password" bind:value={pw} autocomplete="current-password" placeholder="Password" aria-label="Password" required />
    {#if pwNeedsCode}
      <input class="input" bind:value={pwCode} inputmode="numeric" autocomplete="one-time-code" maxlength="7" placeholder="Code from your app" aria-label="Code" required />
    {/if}
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => (pwOpen = false)} disabled={pwBusy}>Cancel</button>
      <button class="btn btn-primary" disabled={pwBusy || !pw}>{#if pwBusy}<Loader size={16} class="spinner" />{/if}Continue</button>
    </div>
  </form>
</Dialog>

<Dialog bind:open={totpOpen} label="Authenticator app" locked={totpBusy}>
  <form class="dlg" onsubmit={enableTOTP}>
    <h2 class="t-title">Scan with your authenticator app</h2>
    {#if totp}
      <img class="qr" src={totp.qr} alt="QR code for your authenticator app" width="200" height="200" />
      <p class="t-caption">Cannot scan? Enter this key: <span class="mono secret">{totp.secret}</span></p>
    {/if}
    {#if totpError}<div class="notice" role="alert">{totpError}</div>{/if}
    <input class="input" bind:value={totpCode} inputmode="numeric" autocomplete="one-time-code" maxlength="7" placeholder="6-digit code from the app" aria-label="Code" required />
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => (totpOpen = false)} disabled={totpBusy}>Cancel</button>
      <button class="btn btn-primary" disabled={totpBusy || !totpCode}>{#if totpBusy}<Loader size={16} class="spinner" />{/if}Turn on</button>
    </div>
  </form>
</Dialog>

<Dialog bind:open={codesOpen} label="Recovery codes" locked>
  <div class="dlg">
    <h2 class="t-title">Save your recovery codes</h2>
    <p class="t-sub">They are shown only now. Keep them somewhere safe, for example in your password manager.</p>
    <ul class="codes mono">{#each codes as c}<li>{c}</li>{/each}</ul>
    <div class="buttons">
      <button class="btn btn-gray" onclick={copyCodes}><Copy size={16} />Copy</button>
      <button class="btn btn-primary" onclick={() => (codesOpen = false)}>I saved them</button>
    </div>
  </div>
</Dialog>

<style>
  .key-name { max-width: 11rem; min-height: 2.25rem; }
  .catalog { align-items: flex-start; }
  .catalog .grow { gap: 0.375rem; }
  .catalog-btns { display: flex; gap: 0.5rem; align-self: center; }
  .qr { justify-self: center; border-radius: 0.75rem; background: #fff; padding: 0.5rem; }
  .secret { word-break: break-all; color: var(--text); }
  .codes { list-style: none; margin: 0; padding: 0.875rem; display: grid; grid-template-columns: 1fr 1fr; gap: 0.375rem 1rem; border-radius: var(--r-control); background: var(--fill); }
  @media (max-width: 639px) {
    .row { flex-wrap: wrap; }
    .key-name { max-width: none; flex: 1; }
    .catalog-btns { width: 100%; justify-content: flex-end; }
  }
</style>
