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
  import Globe from '@lucide/svelte/icons/globe';
  import Sparkles from '@lucide/svelte/icons/sparkles';
  import Brush from '@lucide/svelte/icons/brush';
  import FolderOpen from '@lucide/svelte/icons/folder-open';
  import { runJob } from '$lib/jobs.js';
  import { bytes } from '$lib/format.js';
  import JobLog from '$lib/JobLog.svelte';

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
  // Reverse proxy
  let proxy = $state(null), httpPort = $state(80), httpsPort = $state(443), acmeEmail = $state(''), proxyBusy = $state(false), proxyLines = $state([]);
  // Updates
  let updates = $state(null), releaseURL = $state(''), updBusy = $state(false), updLines = $state([]), checking = $state(false);
  // Cleanup
  let unused = $state(null), pruning = $state(false);
  // File explorer folders
  let roots = $state(''), rootsBusy = $state(false);

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
    releaseURL = settings.release_url || '';
    loadExtras();
  }

  async function loadExtras() {
    const [p, u, i, f] = await Promise.all([api('/api/proxy'), api('/api/updates'), api('/api/images/unused'), api('/api/files')]);
    if (p.ok) {
      proxy = p.data;
      if (proxy.config) {
        httpPort = proxy.config.http_port;
        httpsPort = proxy.config.https_port;
        acmeEmail = proxy.config.email || '';
      } else {
        httpPort = proxy.port80_free ? 80 : 8080;
        httpsPort = proxy.port443_free ? 443 : 8444;
      }
    }
    if (u.ok) updates = u.data;
    if (i.ok) unused = i.data;
    if (f.ok) roots = f.data.roots.join('\n');
  }

  async function setupProxy(e) {
    e.preventDefault();
    proxyBusy = true;
    proxyLines = [];
    const job = await runJob('/api/proxy', { http_port: Number(httpPort), https_port: Number(httpsPort), email: acmeEmail.trim() }, (j) => (proxyLines = j.lines));
    proxyBusy = false;
    if (job.http === 401) return onSessionLost();
    if (job.status === 'done') toast('The reverse proxy runs.', 'success');
    else toast(job.error, 'error', 9000);
    loadExtras();
  }

  async function removeProxy() {
    proxyBusy = true;
    const job = await runJob('/api/proxy/remove', {}, (j) => (proxyLines = j.lines));
    proxyBusy = false;
    if (job.status === 'done') toast('The reverse proxy is stopped. Apps stay reachable on their ports.');
    else toast(job.error, 'error', 9000);
    loadExtras();
  }

  async function saveReleaseURL(e) {
    e.preventDefault();
    const res = await api('/api/settings', { method: 'POST', body: { release_url: releaseURL } });
    if (!res.ok) return toast(res.error, 'error');
    toast('Saved.', 'success');
    checkNow();
  }

  async function checkNow() {
    checking = true;
    const res = await api('/api/updates/check', { method: 'POST' });
    checking = false;
    if (res.ok) updates = res.data;
    else toast(res.error, 'error');
  }

  async function applyUpdate() {
    updBusy = true;
    updLines = [];
    const target = updates.box.latest;
    const job = await runJob('/api/updates/box/apply', {}, (j) => (updLines = j.lines));
    // The web service restarts during the update, so the job usually ends with the old process.
    // Then wait until the new version answers.
    let installed = job.status === 'done';
    for (let i = 0; !installed && job.status !== 'done' && i < 45; i++) {
      const st = await api('/api/auth/state');
      if (st.ok && st.data.version === target) installed = true;
      else if (st.ok && job.error && !/no longer known|not reachable/i.test(job.error)) break;
      else await new Promise((r) => setTimeout(r, 2000));
    }
    updBusy = false;
    if (installed) {
      toast(`NexoVirt Box ${target} is installed. The page reloads.`, 'success');
      setTimeout(() => location.reload(), 3000);
    } else toast(job.error || 'The update did not finish. Check the activity log.', 'error', 10000);
  }

  async function prune() {
    pruning = true;
    const job = await runJob('/api/images/prune', {});
    pruning = false;
    if (job.status === 'done') toast(`Freed ${bytes(job.result.bytes)}.`, 'success');
    else toast(job.error, 'error', 8000);
    loadExtras();
  }

  async function saveRoots(e) {
    e.preventDefault();
    rootsBusy = true;
    const res = await api('/api/files/roots', { method: 'POST', body: { roots } });
    rootsBusy = false;
    if (!res.ok) return toast(res.error, 'error', 8000);
    roots = res.data.roots.join('\n');
    toast('Saved.', 'success');
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
      <p class="t-sub">Sign-in security, apps, HTTPS, updates and storage.</p>
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

    <section class="group">
      <div class="group-head"><h2 class="t-head">Reverse proxy and HTTPS</h2>
        {#if proxy?.enabled}<span class="pill"><span class="dot {proxy.running ? 'ok' : 'warn'}"></span>{proxy.running ? 'Running' : 'Stopped'}</span>{/if}
      </div>
      {#if !proxy}
        <div class="row"><span class="skeleton line"></span></div>
      {:else}
        <form class="row proxy" onsubmit={setupProxy}>
          <span class="ico"><Globe size={16} /></span>
          <div class="grow">
            <span>Serve apps under their own domain with HTTPS</span>
            <span class="t-caption">Box runs Caddy for this. It never takes over a port another program uses.
              {#if !proxy.port80_free || !proxy.port443_free}Ports {[!proxy.port80_free && 80, !proxy.port443_free && 443].filter(Boolean).join(' and ')} are used by another program here, so pick other ports. Let's Encrypt then needs port 80 or 443 forwarded to them.{/if}</span>
            <div class="ports">
              <label class="field"><span class="field-label">HTTP port</span><input class="input num" type="number" min="1" max="65535" bind:value={httpPort} required /></label>
              <label class="field"><span class="field-label">HTTPS port</span><input class="input num" type="number" min="1" max="65535" bind:value={httpsPort} required /></label>
              <label class="field email"><span class="field-label">Email for Let's Encrypt (optional)</span><input class="input" type="email" bind:value={acmeEmail} placeholder="you@example.com" /></label>
            </div>
            {#if proxyLines.length && proxyBusy}<JobLog lines={proxyLines} />{/if}
            {#if proxy.routes?.length}<span class="t-caption">Domains: {proxy.routes.map((r) => `${r.domain} (${r.tls === 'letsencrypt' ? "Let's Encrypt" : 'local certificate'})`).join(', ')}</span>{/if}
          </div>
          <div class="catalog-btns">
            {#if proxy.enabled}<button type="button" class="btn btn-gray btn-sm" onclick={removeProxy} disabled={proxyBusy}>Turn off</button>{/if}
            <button class="btn btn-tinted btn-sm" disabled={proxyBusy}>{#if proxyBusy}<Loader size={15} class="spinner" />{/if}{proxy.enabled ? 'Apply' : 'Turn on'}</button>
          </div>
        </form>
      {/if}
    </section>

    <section class="group">
      <div class="group-head"><h2 class="t-head">Updates</h2></div>
      <div class="row">
        <span class="ico"><Sparkles size={16} /></span>
        <div class="grow">
          <span>NexoVirt Box {updates?.box.current ?? ''}</span>
          <span class="t-caption">
            {#if !updates?.box.configured}Set an update address below to get new versions.
            {:else if updates.box.error}{updates.box.error}
            {:else if updates.box.available}{`Version ${updates.box.latest} is available. ${updates.box.notes || ''}`}
            {:else if updates.box.latest}This is the newest version.
            {:else}Not checked yet.{/if}
          </span>
          {#if updBusy}<JobLog lines={updLines} />{/if}
        </div>
        <div class="catalog-btns">
          <button class="btn btn-gray btn-sm" onclick={checkNow} disabled={checking}>{#if checking}<Loader size={15} class="spinner" />{/if}Check now</button>
          {#if updates?.box.available}<button class="btn btn-primary btn-sm" onclick={applyUpdate} disabled={updBusy}>{#if updBusy}<Loader size={15} class="spinner" />Installing{:else}Install {updates.box.latest}{/if}</button>{/if}
        </div>
      </div>
      <form class="row catalog" onsubmit={saveReleaseURL}>
        <span class="ico"><Sparkles size={16} /></span>
        <div class="grow">
          <label class="field-label" for="release-url">Update address</label>
          <input id="release-url" class="input" bind:value={releaseURL} placeholder="https://downloads.example.com/box" inputmode="url" spellcheck="false" />
          <span class="t-caption">Box only installs releases signed by NexoVirt, and only when you click Install. If the new version does not start, the old one comes back.</span>
        </div>
        <div class="catalog-btns"><button class="btn btn-gray btn-sm">Save</button></div>
      </form>
    </section>

    <section class="group">
      <div class="group-head"><h2 class="t-head">Storage</h2></div>
      <div class="row">
        <span class="ico"><Brush size={16} /></span>
        <div class="grow">
          <span>Unused app images</span>
          <span class="t-caption">{unused ? (unused.count ? `${unused.count} old images use ${bytes(unused.bytes)}. They are left over from updates.` : 'Nothing to clean up.') : 'Checking'}</span>
        </div>
        <button class="btn btn-gray btn-sm" onclick={prune} disabled={pruning || !unused?.count}>{#if pruning}<Loader size={15} class="spinner" />{/if}Clean up</button>
      </div>
      <form class="row catalog" onsubmit={saveRoots}>
        <span class="ico"><FolderOpen size={16} /></span>
        <div class="grow">
          <label class="field-label" for="roots">Folders in Files</label>
          <textarea id="roots" class="input mono roots" bind:value={roots} rows="4" spellcheck="false"></textarea>
          <span class="t-caption">One folder per line. System folders such as /etc or /root are never shown. Empty means the defaults.</span>
        </div>
        <div class="catalog-btns"><button class="btn btn-gray btn-sm" disabled={rootsBusy}>{#if rootsBusy}<Loader size={15} class="spinner" />{/if}Save</button></div>
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
  .ports { display: flex; gap: 0.75rem; flex-wrap: wrap; margin-top: 0.5rem; }
  .ports .field { width: 7.5rem; }
  .ports .email { flex: 1; min-width: 12rem; }
  .proxy { align-items: flex-start; }
  .roots { resize: vertical; min-height: 5.5rem; }
  .qr { justify-self: center; border-radius: 0.75rem; background: #fff; padding: 0.5rem; }
  .secret { word-break: break-all; color: var(--text); }
  .codes { list-style: none; margin: 0; padding: 0.875rem; display: grid; grid-template-columns: 1fr 1fr; gap: 0.375rem 1rem; border-radius: var(--r-control); background: var(--fill); }
  @media (max-width: 639px) {
    .row { flex-wrap: wrap; }
    .key-name { max-width: none; flex: 1; }
    .catalog-btns { width: 100%; justify-content: flex-end; }
  }
</style>
