<script>
  import DataTable from '$lib/DataTable.svelte';
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { createPasskey, friendly, passkeysSupported } from '$lib/webauthn.js';
  import { ago } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import Logo from '$lib/Logo.svelte';
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
  import Segmented from '$lib/Segmented.svelte';
  import { theme, setThemeMode } from '$lib/theme.svelte.js';
  import Server from '@lucide/svelte/icons/server';
  import Palette from '@lucide/svelte/icons/palette';
  import Lock from '@lucide/svelte/icons/lock';
  import Monitor from '@lucide/svelte/icons/monitor';
  import LogOut from '@lucide/svelte/icons/log-out';
  import Waypoints from '@lucide/svelte/icons/waypoints';
  import Scale from '@lucide/svelte/icons/scale';
  import FolderTree from '@lucide/svelte/icons/folder-tree';
  import NotifySettings from './NotifySettings.svelte';
  import LocalNames from './LocalNames.svelte';
  import VpnSettings from './VpnSettings.svelte';
  import NetworkOverview from './NetworkOverview.svelte';
  import WallpaperSettings from './WallpaperSettings.svelte';
  import ThemeSettings from './ThemeSettings.svelte';

  const THEMES = [{ value: 'light', label: 'Light' }, { value: 'dark', label: 'Dark' }, { value: 'system', label: 'Auto' }];
  let mode = $state(theme.mode);
  $effect(() => { if (mode !== theme.mode) setThemeMode(mode); });

  let system = $state(null), sessions = $state(null), trusted = $state('');
  let cpOpen = $state(false), cpCurrent = $state(''), cpNew = $state(''), cpRepeat = $state(''), cpBusy = $state(false), cpError = $state('');

  async function loadAccount() {
    const [sy, se] = await Promise.all([api('/api/system'), api('/api/sessions')]);
    if (sy.ok) system = sy.data;
    if (se.ok) { sessions = se.data.sessions; trusted = se.data.trusted_proxies; }
  }

  async function changePassword(e) {
    e.preventDefault();
    if (cpNew !== cpRepeat) { cpError = 'The new passwords do not match.'; return; }
    cpBusy = true;
    cpError = '';
    const res = await api('/api/account/password', { method: 'POST', body: { current: cpCurrent, new: cpNew } });
    cpBusy = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) { cpError = res.error; return; }
    cpOpen = false;
    toast(res.data.ended ? `Password changed. ${res.data.ended} other ${res.data.ended === 1 ? 'browser was' : 'browsers were'} signed out.` : 'Password changed.', 'success');
    loadAccount();
  }

  const endOthers = (e) => askPassword(e, 'Sign out other browsers', 'Every browser except this one is signed out, and their consoles close.', false, async (password) => {
    const res = await api('/api/sessions/end-others', { method: 'POST', body: { password } });
    if (!res.ok) return res.error;
    toast(res.data.ended ? `${res.data.ended} ${res.data.ended === 1 ? 'browser was' : 'browsers were'} signed out.` : 'No other browser was signed in.', 'success');
    loadAccount();
  });

  function browserName(ua = '') {
    const b = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : ua ? 'Browser' : 'Unknown browser';
    const os = /Mac OS X|Macintosh/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Android/.test(ua) ? 'Android' : /iPhone|iPad/.test(ua) ? 'iOS' : /Linux/.test(ua) ? 'Linux' : '';
    return os ? `${b} on ${os}` : b;
  }

  // One section of the Settings window (SettingsView picks it): general, security, apps, network,
  // notifications, updates or about.
  let { section = 'general', onSessionLost } = $props();
  const tab = $derived(section);

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

  onMount(() => { load(); loadAccount(); });
  const SESSION_COLS = [
    { key: 'agent', label: 'Browser', primary: true },
    { key: 'ip', label: 'Address' },
    { key: 'created_at', label: 'Signed in', sortable: true, firstDir: 'desc' },
  ];
</script>


<div class="page">
  {#if error}
    <div class="notice">{error}</div>
  {:else if !tfa}
    <div class="group">{#each Array(4) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</div>
  {:else}
      <div class="pane">
        {#if tab === 'general'}
          <section class="group">
            <div class="row">
              <span class="ico"><Server size={16} /></span>
              <div class="grow"><span>Server name</span><span class="t-caption">Shown on the desktop. It is the hostname of this server.</span></div>
              <span class="row-value mono">{system?.hostname ?? '-'}</span>
            </div>
            <div class="row">
              <span class="ico"><Palette size={16} /></span>
              <div class="grow"><span>Appearance</span><span class="t-caption">Auto follows your device. Remembered in this browser.</span></div>
              <Segmented options={THEMES} bind:value={mode} label="Appearance" />
            </div>
          </section>
          <ThemeSettings />
          <WallpaperSettings {onSessionLost} />
          <p class="t-section sub">Files</p>
          <section class="group">
            <form class="row stack" onsubmit={saveRoots}>
              <span class="ico"><FolderTree size={16} /></span>
              <div class="grow">
                <label class="field-label" for="roots">Folders shown in Files</label>
                <textarea id="roots" class="input mono roots" bind:value={roots} rows="4" spellcheck="false"></textarea>
                <span class="t-caption">One folder per line. System folders such as /etc or /root are never shown. Empty means the defaults.</span>
              </div>
              <div class="btns"><button class="btn btn-gray btn-sm" disabled={rootsBusy}>{#if rootsBusy}<Loader size={15} class="spinner" />{/if}Save</button></div>
            </form>
          </section>

        {:else if tab === 'security'}
          <p class="t-section sub">Password</p>
          <section class="group">
            <div class="row">
              <span class="ico"><Lock size={16} /></span>
              <div class="grow"><span>Password</span><span class="t-caption">Changing it signs out every other browser.</span></div>
              <button class="btn btn-gray btn-sm" onclick={() => { cpCurrent = cpNew = cpRepeat = ''; cpError = ''; cpOpen = true; }}>Change</button>
            </div>
          </section>

          <p class="t-section sub">Two-step sign-in</p>
          <section class="group">
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

          <div class="sessions">
            <DataTable label="Signed-in browsers" title="Signed-in browsers" columns={SESSION_COLS} rows={sessions} key="id" maxHeight="22rem" empty={{ icon: Monitor, title: 'No sessions' }}>
              {#snippet toolbar()}{#if sessions && sessions.length > 1}<button class="btn btn-gray btn-sm" onclick={endOthers}><LogOut size={15} />Sign out the others</button>{/if}{/snippet}
              {#snippet cell(se, c)}
                {#if c.key === 'agent'}<span class="who"><span class="ico"><Monitor /></span><span class="truncate">{se.agent || se.ip ? browserName(se.agent) : 'Earlier sign-in'}</span>{#if se.current}<span class="pill here">This browser</span>{/if}</span>
                {:else if c.key === 'ip'}<span class="mono muted">{se.ip || ''}</span>
                {:else}<span class="muted">{se.created_at ? ago(se.created_at) : 'Before Box recorded it'}</span>{/if}
              {/snippet}
            </DataTable>
          </div>

          <p class="t-section sub">Reverse proxies in front of Box</p>
          <section class="group">
            <div class="row">
              <span class="ico"><Waypoints size={16} /></span>
              <div class="grow">
                <span>Trusted proxies</span>
                <span class="t-caption">Only these may tell Box the visitor's real address (X-Forwarded-For). Set with <span class="mono">-trusted-proxies</span> in the nexovirt-box service.</span>
              </div>
              <span class="row-value mono">{trusted || 'None'}</span>
            </div>
          </section>

        {:else if tab === 'apps'}
          <section class="group">
            <form class="row stack" onsubmit={saveURL}>
              <span class="ico"><Package size={16} /></span>
              <div class="grow">
                <label class="field-label" for="catalog-url">Online catalog address</label>
                <input id="catalog-url" class="input" bind:value={catalogURL} placeholder="https://apps.example.com/catalog" inputmode="url" spellcheck="false" />
                <span class="t-caption">Leave empty to use the catalog built into Box. Online catalogs must be signed by NexoVirt.</span>
              </div>
              <div class="btns">
                <button class="btn btn-gray btn-sm" disabled={savingURL}>{#if savingURL}<Loader size={15} class="spinner" />{/if}Save</button>
                <button type="button" class="btn btn-tinted btn-sm" onclick={sync} disabled={syncing}>
                  {#if syncing}<Loader size={15} class="spinner" />Syncing{:else}<RefreshCw size={15} />Sync now{/if}
                </button>
              </div>
            </form>
            <div class="row">
              <span class="ico"><FolderOpen size={16} /></span>
              <div class="grow"><span>App data</span><span class="t-caption">Each app keeps its files in its own folder unless you pick one at install.</span></div>
              <span class="row-value mono">/var/lib/nexovirt-box/apps/&lt;app&gt;/data</span>
            </div>
          </section>
          <p class="t-section sub">Storage</p>
          <section class="group">
            <div class="row">
              <span class="ico"><Brush size={16} /></span>
              <div class="grow">
                <span>Unused app images</span>
                <span class="t-caption">{unused ? (unused.count ? `${unused.count} old images use ${bytes(unused.bytes)}. They are left over from updates.` : 'Nothing to clean up.') : 'Checking'}</span>
              </div>
              <button class="btn btn-gray btn-sm" onclick={prune} disabled={pruning || !unused?.count}>{#if pruning}<Loader size={15} class="spinner" />{/if}Clean up</button>
            </div>
          </section>

        {:else if tab === 'notifications'}
          <NotifySettings {onSessionLost} />

        {:else if tab === 'network'}
          <div class="sub-head">
            <p class="t-section sub">Reverse proxy and HTTPS</p>
            {#if proxy?.enabled}<span class="pill"><span class="dot {proxy.running ? 'ok' : 'warn'}"></span>{proxy.running ? 'Running' : 'Stopped'}</span>{/if}
          </div>
          <section class="group">
            {#if !proxy}
              <div class="row"><span class="skeleton line"></span></div>
            {:else}
              <form class="row stack" onsubmit={setupProxy}>
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
                </div>
                <div class="btns">
                  {#if proxy.enabled}<button type="button" class="btn btn-gray btn-sm" onclick={removeProxy} disabled={proxyBusy}>Turn off</button>{/if}
                  <button class="btn btn-tinted btn-sm" disabled={proxyBusy}>{#if proxyBusy}<Loader size={15} class="spinner" />{/if}{proxy.enabled ? 'Apply' : 'Turn on'}</button>
                </div>
              </form>
            {/if}
          </section>
          {#if proxy}
            <p class="t-section sub">Domains</p>
            <section class="group">
              {#each proxy.routes || [] as rt (rt.domain)}
                <div class="row">
                  <span class="ico"><Globe size={16} /></span>
                  <div class="grow"><span class="mono">{rt.domain}</span><span class="t-caption">{rt.tls === 'letsencrypt' ? "Let's Encrypt" : 'Local certificate'}{rt.cert ? `, ${rt.cert.issuer}, valid until ${new Date(rt.cert.expires * 1000).toLocaleDateString()}` : ', no certificate yet'}{rt.protect ? ', Box sign-in in front' : ''}{rt.port ? `, port ${rt.port}` : ''}</span></div>
                  <a class="btn btn-gray btn-sm" href="https://{rt.domain}" target="_blank" rel="noopener noreferrer">Open</a>
                </div>
              {:else}
                <div class="row t-sub">No app has a domain yet. Give one in Apps, in the app's menu.</div>
              {/each}
            </section>
          {/if}
          <LocalNames {onSessionLost} />
          <VpnSettings {onSessionLost} />
          <NetworkOverview {onSessionLost} />

        {:else if tab === 'updates'}
          <section class="group">
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
              <div class="btns">
                <button class="btn btn-gray btn-sm" onclick={checkNow} disabled={checking}>{#if checking}<Loader size={15} class="spinner" />{/if}Check now</button>
                {#if updates?.box.available}<button class="btn btn-primary btn-sm" onclick={applyUpdate} disabled={updBusy}>{#if updBusy}<Loader size={15} class="spinner" />Installing{:else}Install {updates.box.latest}{/if}</button>{/if}
              </div>
            </div>
            <form class="row stack" onsubmit={saveReleaseURL}>
              <span class="ico"><Sparkles size={16} /></span>
              <div class="grow">
                <label class="field-label" for="release-url">Update address</label>
                <input id="release-url" class="input" bind:value={releaseURL} placeholder="https://downloads.example.com/box" inputmode="url" spellcheck="false" />
                <span class="t-caption">Box only installs releases signed by NexoVirt, and only when you click Install. If the new version does not start, the old one comes back.</span>
              </div>
              <div class="btns"><button class="btn btn-gray btn-sm">Save</button></div>
            </form>
          </section>

        {:else}
          <div class="about-head">
            <Logo size={64} />
            <span class="t-title">NexoVirt Box</span>
            <span class="t-caption">A web desktop for your own server</span>
          </div>
          <section class="group">
            <div class="row"><span class="row-label">NexoVirt Box</span><span class="row-value num">{updates?.box.current ?? '-'}</span></div>
            <div class="row"><span class="row-label">Server</span><span class="row-value">{system?.hostname ?? '-'}</span></div>
            <div class="row"><span class="row-label">Operating system</span><span class="row-value">{system?.os ?? '-'}</span></div>
            <div class="row"><span class="row-label">Kernel</span><span class="row-value mono">{system?.kernel ?? '-'}</span></div>
            <div class="row"><span class="row-label">Architecture</span><span class="row-value">{system?.arch ?? '-'}</span></div>
          </section>
          <section class="group">
            <div class="row">
              <span class="ico"><Scale size={16} /></span>
              <div class="grow"><span>Open source under the GNU AGPL 3.0</span><span class="t-caption">App definitions come from Runtipi (GPL-3.0), CasaOS (Apache-2.0) and BigBear (MIT). Each app shows its source and license in the App Store.</span></div>
            </div>
          </section>
        {/if}
      </div>
  {/if}
</div>

<Dialog bind:open={cpOpen} label="Change password" locked={cpBusy}>
  <form class="dlg" onsubmit={changePassword}>
    <h2 class="t-title">Change password</h2>
    <p class="t-sub">Every other browser is signed out afterwards.</p>
    {#if cpError}<div class="notice" role="alert">{cpError}</div>{/if}
    <input class="input" type="password" bind:value={cpCurrent} autocomplete="current-password" placeholder="Current password" aria-label="Current password" required />
    <input class="input" type="password" bind:value={cpNew} autocomplete="new-password" placeholder="New password (at least 10 characters)" aria-label="New password" minlength="10" maxlength="72" required />
    <input class="input" type="password" bind:value={cpRepeat} autocomplete="new-password" placeholder="New password again" aria-label="Repeat the new password" required />
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => (cpOpen = false)} disabled={cpBusy}>Cancel</button>
      <button class="btn btn-primary" disabled={cpBusy || !cpCurrent || !cpNew}>{#if cpBusy}<Loader size={16} class="spinner" />{/if}Change password</button>
    </div>
  </form>
</Dialog>

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
  .about-head { display: grid; justify-items: center; gap: 0.375rem; padding: 1rem 0 0.75rem; text-align: center; color: var(--text); }
  .about-head .t-title { margin-top: 0.375rem; }
  .sessions { margin-top: 1.25rem; }
  .who { display: flex; align-items: center; gap: 0.625rem; min-width: 0; max-width: 100%; }
  .muted { color: var(--text-2); }
  .pane { display: grid; gap: 0.75rem; min-width: 0; animation: fade 220ms ease; }
  @keyframes fade { from { opacity: 0; transform: translateY(4px); } }
  .sub { margin: 0.75rem 1.125rem 0; }
  .sub-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; }
  .sub-head .sub { margin-bottom: 0; }
  .here { margin-left: 0.5rem; height: 1.375rem; background: var(--accent-fill); color: var(--accent); vertical-align: middle; }
  .row-value.mono { color: var(--text-2); font-weight: 400; }
  .key-name { max-width: 11rem; min-height: 2.25rem; }
  .stack { align-items: flex-start; }
  .stack .grow { gap: 0.375rem; }
  .btns { display: flex; gap: 0.5rem; align-self: center; flex-shrink: 0; }
  .ports { display: flex; gap: 0.75rem; flex-wrap: wrap; margin-top: 0.5rem; }
  .ports .field { width: 7.5rem; }
  .ports .email { flex: 1; min-width: 12rem; }
  .roots { resize: vertical; min-height: 5.5rem; height: auto; padding-block: 0.625rem; }
  .qr { justify-self: center; border-radius: 0.75rem; background: #fff; padding: 0.5rem; }
  .secret { word-break: break-all; color: var(--text); }
  .codes { list-style: none; margin: 0; padding: 0.875rem; display: grid; grid-template-columns: 1fr 1fr; gap: 0.375rem 1rem; border-radius: var(--r-control); background: var(--fill); }
  @media (max-width: 639px) {
    .row { flex-wrap: wrap; }
    .key-name { max-width: none; flex: 1; }
    .btns { width: 100%; justify-content: flex-end; }
  }
</style>
