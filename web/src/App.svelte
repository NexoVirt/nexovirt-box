<script>
  // Shell: a slim translucent top bar over the desktop. Box apps (Apps, Files, Monitor, Terminal,
  // Logs, Backups) and Settings open as floating windows over it, several at once. The address bar
  // shows the window in front, so a link opens (or brings forward) that window.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { theme, setThemeMode } from '$lib/theme.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import { router } from '$lib/nav.js';
  import { desktop, loadDesktop, setOrigin } from '$lib/desktop.svelte.js';
  import { wm, place, topWindow } from '$lib/windows.svelte.js';
  import { applyTheme } from '$lib/themes.js';
  import { activityLabel } from '$lib/activityLabels.js';
  import { ago } from '$lib/format.js';
  import { resumeInstalls } from '$lib/installs.svelte.js';
  import Auth from './Auth.svelte';
  import Desktop from './Desktop.svelte';
  import AppWindow from '$lib/AppWindow.svelte';
  import Lazy from '$lib/Lazy.svelte';
  import PasswordGate from '$lib/PasswordGate.svelte';
  import Popover from '$lib/Popover.svelte';
  import Toaster from '$lib/Toaster.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import LogOut from '@lucide/svelte/icons/log-out';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Sun from '@lucide/svelte/icons/sun';
  import Moon from '@lucide/svelte/icons/moon';
  import SettingsIcon from '@lucide/svelte/icons/settings';
  import SquareTerminal from '@lucide/svelte/icons/square-terminal';
  import Bell from '@lucide/svelte/icons/bell';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import UserRound from '@lucide/svelte/icons/user-round';

  const themeModes = [
    { value: 'light', label: 'Light' },
    { value: 'dark', label: 'Dark' },
    { value: 'system', label: 'Auto' },
  ];
  // Window kinds; each page is its own chunk, loaded when first opened. Fill windows scroll inside
  // their own content; size is the default (centred, remembered once moved or resized).
  const KINDS = {
    apps: { title: 'Apps', fill: true, flush: true, size: [1100, 740], load: () => import('./AppManager.svelte') },
    files: { title: 'Files', fill: true, flush: true, size: [1120, 720], load: () => import('./Files.svelte') },
    monitor: { title: 'Monitor', size: [1040, 720], load: () => import('./Monitor.svelte') },
    console: { title: 'Terminal', fill: true, flush: true, size: [940, 600], load: () => import('./Console.svelte') },
    backups: { title: 'Backups', fill: true, size: [1080, 720], load: () => import('./Backups.svelte') },
    settings: { title: 'Settings', fill: true, flush: true, size: [1080, 720], load: () => import('./SettingsView.svelte') },
    logs: { title: 'Logs', fill: true, size: [1000, 660], load: () => import('./AppLogs.svelte') },
  };
  const SIMPLE = { '/files': 'files', '/monitor': 'monitor', '/console': 'console', '/backups': 'backups' };
  // Which window a path belongs to, and the props its page gets from the path.
  function resolve(p) {
    if (p === '/apps' || p === '/apps/installed') return { key: 'apps', kind: KINDS.apps, props: { tab: p === '/apps' ? 'store' : 'installed' } };
    const logs = p.match(/^\/apps\/([a-z0-9][a-z0-9-]{0,62})\/logs$/)?.[1];
    if (logs) return { key: 'logs-' + logs, kind: KINDS.logs, title: 'Logs: ' + logs, props: { id: logs } };
    if (p === '/settings' || p.startsWith('/settings/')) return { key: 'settings', kind: KINDS.settings, props: { section: p.slice(10) } };
    return SIMPLE[p] ? { key: SIMPLE[p], kind: KINDS[SIMPLE[p]], props: {} } : null;
  }
  const pathOf = (u) => u.split(/[?#]/)[0] || '/';
  // Pages that were in the sidebar before the desktop: their old addresses still work.
  const MOVED = { '/firewall': 'firewall', '/shares': 'shares', '/disks': 'disks', '/updates': 'system-updates', '/activity': 'activity' };
  const OLD_TABS = { updates: 'box-updates' };

  let state = $state(null); // { setup_needed, logged_in, username, version }
  let loadError = $state('');
  let system = $state(null);
  let phone = $state(matchMedia('(max-width: 699px)').matches);
  let userOpen = $state(false), bellOpen = $state(false);
  let mode = $state(theme.mode);
  let bell = $state(null), diskWarn = $state([]);

  $effect(() => { if (mode !== theme.mode) setThemeMode(mode); });
  // The user's theme (or the one being edited) as CSS tokens, again on every light/dark switch.
  $effect(() => { const t = desktop.preview || desktop.prefs?.theme; if (desktop.prefs) applyTheme(t, theme.current === 'dark'); });
  function toggleTheme() { mode = theme.current === 'dark' ? 'light' : 'dark'; }

  // Rewrites an old address (/firewall, /settings#security) to its place in the Settings window.
  function canonical(to) {
    const u = new URL(to, location.origin);
    if (MOVED[u.pathname]) return '/settings/' + MOVED[u.pathname] + u.search + u.hash;
    if (u.pathname === '/settings' && u.hash.length > 1) {
      const k = u.hash.slice(1);
      return '/settings/' + (OLD_TABS[k] || k);
    }
    return u.pathname + u.search + u.hash;
  }

  async function loadState() {
    loadError = '';
    const res = await api('/api/auth/state');
    if (!res.ok) { loadError = res.error; return; }
    state = res.data;
    if (state.logged_in) { loadSystem(); resumeInstalls(); loadDesktop(); loadDiskWarn(); }
  }

  async function loadSystem() {
    const res = await api('/api/system');
    if (res.status === 401) return sessionLost();
    if (!res.ok) toast(res.error, 'error');
    else system = res.data;
  }
  async function loadDiskWarn() {
    const res = await api('/api/disks/summary');
    if (res.ok) diskWarn = res.data.warnings;
  }

  // ---- windows ----
  const here = () => location.pathname + location.search + location.hash;
  function setURL(u) { if (u !== here()) history.replaceState(null, '', u); }
  // The page in front may have put its tab into the address (#targets): keep it for later.
  function keepFrontURL() {
    const t = topWindow();
    if (t && resolve(location.pathname)?.key === t.key) t.url = here();
  }
  // Opens the window a path belongs to, or brings it forward. A query or hash (/files?path=/srv,
  // /apps/installed?app=x&do=backups, /backups#targets) is read by the page, so it mounts again.
  function navigate(to) {
    userOpen = bellOpen = false;
    to = canonical(to);
    const r = resolve(pathOf(to));
    if (!r) return setURL('/');
    keepFrontURL();
    let w = wm.list.find((x) => x.key === r.key);
    if (w) {
      if (to.length > pathOf(to).length) w.gen++;
      w.url = to;
    } else {
      wm.list.push({ key: r.key, url: to, gen: 0, z: 0, min: false, ...place(r.key, r.kind.size, wm.list.filter((x) => !x.min).length) });
      w = wm.list[wm.list.length - 1];
    }
    focusWindow(w);
  }
  function focusWindow(w) {
    if (topWindow() !== w) keepFrontURL();
    w.min = false;
    w.z = ++wm.seq;
    setURL(w.url);
  }
  function closeWindow(w) {
    keepFrontURL();
    wm.list = wm.list.filter((x) => x !== w);
    setURL(topWindow()?.url || '/');
  }
  function minimizeWindow(w) {
    keepFrontURL();
    w.min = true;
    setURL(topWindow()?.url || '/');
  }
  function openFromBar(e, to) { setOrigin(e.currentTarget); navigate(to); }

  async function signOut() {
    userOpen = false;
    const res = await api('/api/logout', { method: 'POST' });
    if (!res.ok) toast(res.error, 'error');
    system = null;
    await loadState();
  }

  function sessionLost() {
    state = { ...state, logged_in: false };
    system = null;
    toast('Your session ended. Please sign in again.');
  }

  async function openBell() {
    bellOpen = !bellOpen;
    if (!bellOpen) return;
    const res = await api('/api/activity?limit=6');
    if (res.status === 401) return sessionLost();
    bell = res.ok ? res.data.items : [];
  }

  onMount(() => {
    router.go = navigate;
    if (location.pathname !== '/') navigate(here()); // a deep link opens its window
    loadState();
    const onPop = () => { userOpen = bellOpen = false; if (location.pathname !== '/') navigate(here()); };
    addEventListener('popstate', onPop);
    const mq = matchMedia('(max-width: 699px)');
    const onMq = () => (phone = mq.matches);
    mq.addEventListener('change', onMq);
    return () => { removeEventListener('popstate', onPop); mq.removeEventListener('change', onMq); };
  });

  const front = $derived(topWindow());
  // z-order as small numbers (front = highest), so dialogs and menus always stay above windows.
  const ranks = $derived(new Map([...wm.list].sort((a, b) => a.z - b.z).map((w, i) => [w.key, i])));
  const docked = $derived(wm.list.filter((w) => w.min));
  // The desktop is hidden (and its polling paused) behind a maximised window or on a phone.
  const covered = $derived(wm.list.some((w) => !w.min && (w.max || phone)));
  const titleOf = (w) => { const r = resolve(pathOf(w.url)); return r?.title || r?.kind.title || ''; };
  let initials = $derived(state?.username ? state.username.slice(0, 2).toUpperCase() : '');
</script>

<Toaster />
<PasswordGate />

{#if !state}
  <div class="center-screen">
    {#if loadError}
      <div class="load-error">
        <div class="notice"><CircleAlert size={18} /><span>{loadError}</span></div>
        <button class="btn btn-gray" onclick={loadState}><RefreshCw size={16} />Try again</button>
      </div>
    {:else}
      <div class="loading t-sub"><Loader size={18} class="spinner" />Loading NexoVirt Box</div>
    {/if}
  </div>
{:else if state.setup_needed}
  <Auth mode="setup" onDone={loadState} />
{:else if !state.logged_in}
  <Auth mode="login" onDone={loadState} />
{:else}
  <header class="topbar glass blur" inert={covered && phone}>
    <div class="side">
      <div class="anchor">
        <button class="bar-btn account" onclick={() => (userOpen = !userOpen)} aria-haspopup="menu" aria-expanded={userOpen} aria-label="Account: {state.username}">
          <span class="avatar">{initials || '?'}</span>
        </button>
        <Popover bind:open={userOpen} label="Account" left>
          <div class="who">
            <span class="ico md accent"><UserRound /></span>
            <span class="who-text"><span class="t-head truncate">{state.username}</span><span class="t-caption">{system?.hostname ? `${system.hostname}  ·  ` : ''}Box {state.version}</span></span>
          </div>
          <div class="pop-section">
            <span class="pop-label">Appearance</span>
            <Segmented options={themeModes} bind:value={mode} label="Appearance" />
          </div>
          <button class="pop-item" role="menuitem" onclick={(e) => openFromBar(e, '/settings/security')}><SettingsIcon size={16} />Account and security</button>
          <button class="pop-item" role="menuitem" onclick={signOut}><LogOut size={16} />Sign out</button>
        </Popover>
      </div>
      <button class="bar-btn" onclick={(e) => openFromBar(e, '/settings')} aria-label="Settings" title="Settings"><SettingsIcon size={18} /></button>
      <button class="bar-btn" onclick={(e) => openFromBar(e, '/console')} aria-label="Terminal" title="Terminal"><SquareTerminal size={18} /></button>
    </div>
    <div class="side">
      <div class="anchor">
        <button class="bar-btn" onclick={openBell} aria-haspopup="menu" aria-expanded={bellOpen} aria-label={diskWarn.length ? 'Activity, a disk needs attention' : 'Activity'} title="Activity">
          <Bell size={18} />{#if diskWarn.length}<span class="dot warn badge"></span>{/if}
        </button>
        <Popover bind:open={bellOpen} label="Activity">
          <div class="bell">
            {#if diskWarn.length}
              <button class="bell-row warn" onclick={(e) => openFromBar(e, '/settings/disks')}>
                <span class="ico warn"><TriangleAlert /></span>
                <span class="bell-text"><span class="truncate">{diskWarn.length === 1 ? 'A disk needs attention' : `${diskWarn.length} disks need attention`}</span><span class="t-caption truncate">{diskWarn[0].model || diskWarn[0].disk}</span></span>
              </button>
            {/if}
            {#if !bell}
              {#each Array(3) as _}<div class="bell-row"><span class="skeleton" style:height="2rem" style:flex="1"></span></div>{/each}
            {:else}
              {#each bell as it (it.id)}
                {@const [text, Icon, tone] = activityLabel(it.action)}
                <div class="bell-row">
                  <span class="ico" class:error={tone === 'error'}><Icon /></span>
                  <span class="bell-text"><span class="truncate">{text}</span><span class="t-caption truncate">{it.detail || it.username}  ·  {ago(it.at)}</span></span>
                </div>
              {:else}
                <p class="t-caption bell-empty">Nothing happened yet.</p>
              {/each}
            {/if}
            <button class="pop-item all" role="menuitem" onclick={(e) => openFromBar(e, '/settings/activity')}>All activity<ChevronRight size={15} /></button>
          </div>
        </Popover>
      </div>
      <button class="bar-btn" onclick={toggleTheme} aria-label={theme.current === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'} title={theme.current === 'dark' ? 'Light mode' : 'Dark mode'}>
        {#if theme.current === 'dark'}<Sun size={18} />{:else}<Moon size={18} />{/if}
      </button>
    </div>
  </header>

  <Desktop {system} username={state.username} onSessionLost={sessionLost} onNavigate={navigate} inert={covered} />

  {#each wm.list as w (w.key)}
    {@const r = resolve(pathOf(w.url))}
    {#if r}
      <AppWindow {w} title={titleOf(w)} rank={ranks.get(w.key) ?? 0} focused={front === w} {phone} fill={r.kind.fill} flush={r.kind.flush}
        onClose={() => closeWindow(w)} onMinimize={() => minimizeWindow(w)} onFocus={() => focusWindow(w)}>
        {#key w.gen}
          <Lazy load={r.kind.load} {...r.props} pageKey={w.gen} {system} onSessionLost={sessionLost} onNavigate={navigate} />
        {/key}
      </AppWindow>
    {/if}
  {/each}
  {#if docked.length}
    <nav class="dockrow" aria-label="Minimised windows">
      {#each docked as w (w.key)}
        <button class="parked" onclick={() => focusWindow(w)} aria-label="Show {titleOf(w)} again"><span class="dot ok"></span>{titleOf(w)}</button>
      {/each}
    </nav>
  {/if}
{/if}

<style>
  .center-screen { min-height: 100svh; display: grid; place-items: center; padding: 1rem; }
  .loading { display: flex; align-items: center; gap: 0.625rem; }
  .load-error { display: grid; gap: 1rem; width: min(24rem, 100%); }

  .topbar {
    position: fixed; top: 0; left: 0; right: 0; z-index: 20; display: flex; align-items: center; justify-content: space-between;
    min-height: calc(3rem + env(safe-area-inset-top, 0px));
    padding: env(safe-area-inset-top, 0px) max(0.75rem, env(safe-area-inset-right, 0px)) 0 max(0.75rem, env(safe-area-inset-left, 0px));
    border-radius: 0; box-shadow: 0 1px 0 var(--hairline);
  }
  .side { display: flex; align-items: center; gap: 0.125rem; }
  .anchor { position: relative; }
  .bar-btn {
    position: relative; display: grid; place-items: center; width: 2.75rem; height: 2.75rem; border: 0; border-radius: 50%;
    background: none; color: var(--text); cursor: pointer; transition: background-color 150ms ease, transform 100ms ease-out;
  }
  .bar-btn:hover, .bar-btn[aria-expanded='true'] { background: var(--fill); }
  .bar-btn:active { transform: scale(0.92); }
  .avatar {
    display: grid; place-items: center; width: 1.875rem; height: 1.875rem; border-radius: 50%;
    background: var(--accent); color: var(--on-accent); font-size: 0.6875rem; font-weight: 700; letter-spacing: 0.03em;
  }
  .badge { position: absolute; top: 0.6rem; right: 0.6rem; box-shadow: 0 0 0 2px var(--surface); }

  .who { display: flex; align-items: center; gap: 0.75rem; padding: 0.5rem 0.5rem 0.75rem; }
  .who-text { display: grid; min-width: 0; }
  .topbar :global(.pop) { width: 17.5rem; }
  .pop-section { display: grid; gap: 0.5rem; padding: 0.625rem 0.5rem; border-top: 1px solid var(--hairline); }
  .pop-label { font-size: 0.8125rem; font-weight: 500; color: var(--text-2); padding: 0 0.125rem; }
  .pop-item {
    display: flex; align-items: center; gap: 0.625rem; width: 100%; min-height: 2.75rem; padding: 0 0.625rem;
    border: 0; border-top: 1px solid var(--hairline); background: none; color: var(--text); cursor: pointer; text-align: left; font-weight: 500;
    transition: background-color 150ms ease;
  }
  .pop-item:last-child { border-radius: 0 0 0.75rem 0.75rem; }
  .pop-item:hover { background: var(--fill); }

  .topbar :global(.pop:not(.left)) { width: 20rem; }
  .bell { display: grid; }
  .bell-row { display: flex; align-items: center; gap: 0.75rem; min-height: 3rem; padding: 0.375rem 0.5rem; border: 0; background: none; color: var(--text); font: inherit; text-align: left; border-radius: 0.625rem; }
  button.bell-row { cursor: pointer; }
  button.bell-row:hover { background: var(--fill); }
  .bell-text { flex: 1; min-width: 0; display: grid; }
  .bell-empty { margin: 0; padding: 0.75rem 0.5rem; }
  .all { justify-content: space-between; margin-top: 0.25rem; }
  .dockrow {
    position: fixed; left: 50%; bottom: env(safe-area-inset-bottom, 0px); z-index: 49; transform: translateX(-50%);
    display: flex; gap: 0.5rem; max-width: 100vw; overflow-x: auto; padding: 1rem 1rem 1.25rem; scrollbar-width: none; pointer-events: none;
  }
  .parked {
    flex-shrink: 0; display: inline-flex; align-items: center; gap: 0.5rem; min-height: 2.75rem; padding: 0 1.125rem; border: 0; border-radius: 999px; pointer-events: auto;
    background: var(--material); color: var(--text); font: inherit; font-weight: 600; cursor: pointer; box-shadow: var(--shadow-float);
    animation: rise 320ms var(--spring);
  }
  .parked:active { transform: scale(0.96); }
  @keyframes rise { from { opacity: 0; transform: translateY(12px); } }
</style>
