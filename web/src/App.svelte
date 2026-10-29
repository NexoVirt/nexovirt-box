<script>
  // Shell: a slim translucent top bar over the desktop. Box apps (App Store, Files, Monitor,
  // Terminal, My Apps, Logs, Backups) and Settings open as full-screen windows over it.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { theme, setThemeMode } from '$lib/theme.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import { router } from '$lib/nav.js';
  import { desktop, loadDesktop, setOrigin } from '$lib/desktop.svelte.js';
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
  // Windows opened from tiles; each page is its own chunk, loaded when first opened. Fill windows
  // scroll inside their own card; floating windows float over the desktop (Files).
  const WINDOWS = {
    '/apps': { title: 'App Store', load: () => import('./AppStore.svelte') },
    '/apps/installed': { title: 'My Apps', load: () => import('./MyApps.svelte'), nav: true },
    '/files': { title: 'Files', fill: true, floating: true, load: () => import('./Files.svelte') },
    '/monitor': { title: 'Monitor', load: () => import('./Monitor.svelte'), system: true },
    '/console': { title: 'Terminal', load: () => import('./Console.svelte') },
    '/backups': { title: 'Backups', fill: true, load: () => import('./Backups.svelte') },
  };
  const LOGS = { title: 'Logs', fill: true, load: () => import('./AppLogs.svelte'), nav: true };
  const loadSettings = () => import('./SettingsView.svelte');
  // Pages that were in the sidebar before the desktop: their old addresses still work.
  const MOVED = { '/firewall': 'firewall', '/shares': 'shares', '/disks': 'disks', '/updates': 'system-updates', '/activity': 'activity' };
  const OLD_TABS = { updates: 'box-updates' };

  let state = $state(null); // { setup_needed, logged_in, username, version }
  let loadError = $state('');
  let system = $state(null);
  let path = $state(location.pathname);
  let pageKey = $state(0);
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

  // to may carry a query or hash (/files?path=/srv, /backups#targets) for the page to read.
  function navigate(to) {
    userOpen = bellOpen = false;
    to = canonical(to);
    const p = to.split(/[?#]/)[0] || '/';
    if (to !== location.pathname + location.search + location.hash) {
      // deskDepth: how many steps back the desktop is, so closing a window can go back to it.
      const depth = p === '/' ? 0 : (history.state?.deskDepth ?? (path === '/' ? 0 : -1)) + 1;
      history.pushState({ deskDepth: depth > 0 ? depth : null }, '', to);
      if (p === path) pageKey++; // same page, new target: mount it again
      path = p;
    }
  }
  function closeWindow() {
    parked = null;
    // Back to the desktop entry when the window was opened from it, else straight there (deep link).
    const d = history.state?.deskDepth;
    if (d > 0) history.go(-d);
    else navigate('/');
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
    const fixed = canonical(location.pathname + location.search + location.hash);
    if (fixed !== location.pathname + location.search + location.hash) {
      history.replaceState({}, '', fixed);
      path = fixed.split(/[?#]/)[0];
    }
    loadState();
    const onPop = () => { path = location.pathname; userOpen = bellOpen = false; };
    addEventListener('popstate', onPop);
    return () => removeEventListener('popstate', onPop);
  });

  let logsApp = $derived(path.match(/^\/apps\/([a-z0-9][a-z0-9-]{0,62})\/logs$/)?.[1] || '');
  let settingsSection = $derived(path.startsWith('/settings') ? path.slice('/settings'.length).replace(/^\//, '') : null);
  // A minimised floating window stays alive (hidden) while the desktop is shown.
  let parked = $state(null); // { path, url, title }
  $effect(() => { if (parked && path !== '/' && path !== parked.path) parked = null; });
  let winPath = $derived(logsApp || WINDOWS[path] ? path : path === '/' && parked ? parked.path : null);
  let win = $derived(winPath ? (winPath === path && logsApp ? LOGS : WINDOWS[winPath]) : null);
  let minimized = $derived(!!win && winPath !== path);
  let windowOpen = $derived((!!win && !minimized && !win.floating) || settingsSection !== null);
  function minimize() {
    parked = { path, url: location.pathname + location.search + location.hash, title: win.title };
    navigate('/');
  }
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
  <header class="topbar glass blur" inert={windowOpen}>
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

  <Desktop {system} username={state.username} onSessionLost={sessionLost} onNavigate={navigate} inert={windowOpen} />

  {#if settingsSection !== null}
    <AppWindow title="Settings" onClose={closeWindow} flush>
      <Lazy load={loadSettings} section={settingsSection} {pageKey} onNavigate={navigate} onSessionLost={sessionLost} />
    </AppWindow>
  {:else if win}
    {#key winPath + ':' + (logsApp || '')}
      <AppWindow title={win.title} name={winPath.slice(1)} onClose={closeWindow} fill={win.fill} floating={win.floating}
        onMinimize={win.floating ? minimize : null} {minimized}>
        {#key pageKey}
          <Lazy load={win.load} id={logsApp || undefined} {system} onSessionLost={sessionLost} onNavigate={navigate} />
        {/key}
      </AppWindow>
    {/key}
  {/if}
  {#if parked && path === '/'}
    <button class="parked" onclick={() => navigate(parked.url)} aria-label="Show {parked.title} again">
      <span class="dot ok"></span>{parked.title}
    </button>
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
  .parked {
    position: fixed; left: 50%; bottom: calc(1rem + env(safe-area-inset-bottom, 0px)); z-index: 25; transform: translateX(-50%);
    display: inline-flex; align-items: center; gap: 0.5rem; min-height: 2.75rem; padding: 0 1.125rem; border: 0; border-radius: 999px;
    background: var(--material); color: var(--text); font: inherit; font-weight: 600; cursor: pointer; box-shadow: var(--shadow-float);
    animation: rise 320ms var(--spring);
  }
  .parked:active { transform: translateX(-50%) scale(0.96); }
  @keyframes rise { from { opacity: 0; transform: translateX(-50%) translateY(12px); } }
</style>
