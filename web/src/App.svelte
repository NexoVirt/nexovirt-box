<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { theme, setThemeMode } from '$lib/theme.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import Auth from './Auth.svelte';
  import Dashboard from './Dashboard.svelte';
  import Monitor from './Monitor.svelte';
  import Console from './Console.svelte';
  import Activity from './Activity.svelte';
  import AppStore from './AppStore.svelte';
  import MyApps from './MyApps.svelte';
  import Settings from './Settings.svelte';
  import Files from './Files.svelte';
  import Firewall from './Firewall.svelte';
  import Logo from '$lib/Logo.svelte';
  import Sheet from '$lib/Sheet.svelte';
  import Popover from '$lib/Popover.svelte';
  import Toaster from '$lib/Toaster.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import Gauge from '@lucide/svelte/icons/gauge';
  import House from '@lucide/svelte/icons/house';
  import SquareTerminal from '@lucide/svelte/icons/square-terminal';
  import History from '@lucide/svelte/icons/history';
  import LayoutGrid from '@lucide/svelte/icons/layout-grid';
  import Boxes from '@lucide/svelte/icons/boxes';
  import Menu from '@lucide/svelte/icons/menu';
  import LogOut from '@lucide/svelte/icons/log-out';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Sun from '@lucide/svelte/icons/sun';
  import Moon from '@lucide/svelte/icons/moon';
  import SettingsIcon from '@lucide/svelte/icons/settings';
  import FolderOpen from '@lucide/svelte/icons/folder-open';
  import Shield from '@lucide/svelte/icons/shield';
  import { router } from '$lib/nav.js';
  import AppLogs from './AppLogs.svelte';
  import { resumeInstalls } from '$lib/installs.svelte.js';

  const pages = [
    { path: '/', title: 'Home', icon: House },
    { path: '/monitor', title: 'Monitor', icon: Gauge },
    { path: '/apps', title: 'App Store', icon: LayoutGrid },
    { path: '/apps/installed', title: 'My Apps', icon: Boxes },
    { path: '/files', title: 'Files', icon: FolderOpen },
    { path: '/firewall', title: 'Firewall', icon: Shield },
    { path: '/console', title: 'Console', icon: SquareTerminal },
    { path: '/activity', title: 'Activity', icon: History },
    { path: '/settings', title: 'Settings', icon: SettingsIcon },
  ];
  const themeModes = [
    { value: 'light', label: 'Light' },
    { value: 'dark', label: 'Dark' },
    { value: 'system', label: 'Auto' },
  ];

  let state = $state(null); // { setup_needed, logged_in, username, version }
  let loadError = $state('');
  let system = $state(null);
  let path = $state(location.pathname);
  let menuOpen = $state(false);
  let userOpen = $state(false);
  let mode = $state(theme.mode);

  $effect(() => { if (mode !== theme.mode) setThemeMode(mode); });

  function toggleTheme() { mode = theme.current === 'dark' ? 'light' : 'dark'; }

  async function loadState() {
    loadError = '';
    const res = await api('/api/auth/state');
    if (!res.ok) {
      loadError = res.error;
      return;
    }
    state = res.data;
    if (state.logged_in) { loadSystem(); resumeInstalls(); }
  }

  async function loadSystem() {
    const res = await api('/api/system');
    if (res.status === 401) return sessionLost();
    if (!res.ok) toast(res.error, 'error');
    else system = res.data;
  }

  function go(e, to) {
    e.preventDefault();
    navigate(to);
  }

  // to may carry a query or hash (/files?path=/srv, /settings#security) for the page to read.
  function navigate(to) {
    menuOpen = false;
    const p = to.split(/[?#]/)[0] || '/';
    if (to !== location.pathname + location.search + location.hash) {
      history.pushState({}, '', to);
      if (p === path) pageKey++; // same page, new target: mount it again
      path = p;
      scrollTo({ top: 0 });
    }
  }
  let pageKey = $state(0);

  async function signOut() {
    userOpen = false;
    const res = await api('/api/logout', { method: 'POST' });
    if (!res.ok) toast(res.error, 'error');
    system = null;
    await loadState();
  }

  // A page reports a lost session (401): show the sign-in again.
  function sessionLost() {
    state = { ...state, logged_in: false };
    system = null;
    toast('Your session ended. Please sign in again.');
  }

  onMount(() => {
    router.go = navigate;
    loadState();
    const onPop = () => (path = location.pathname);
    addEventListener('popstate', onPop);
    return () => removeEventListener('popstate', onPop);
  });

  // /apps/<id>/logs: the logs page of one app (shown under My Apps in the sidebar).
  let logsApp = $derived(path.match(/^\/apps\/([a-z0-9][a-z0-9-]{0,62})\/logs$/)?.[1] || '');
  let page = $derived(pages.find((p) => p.path === (logsApp ? '/apps/installed' : path)) || pages[0]);
  // These pages fill the window and scroll inside their own card.
  const FILL = new Set(['/files', '/activity']);
  let fill = $derived(FILL.has(page.path) || !!logsApp);
  let initials = $derived(state?.username ? state.username.slice(0, 2).toUpperCase() : '');
</script>

<Toaster />

{#snippet nav()}
  <nav class="nav" aria-label="Main">
    {#each pages as p (p.path)}
      {@const Icon = p.icon}
      <a href={p.path} onclick={(e) => go(e, p.path)} aria-current={p.path === page.path ? 'page' : undefined}>
        <span class="nav-icon"><Icon size={17} strokeWidth={2.1} /></span>
        {p.title}
      </a>
    {/each}
  </nav>
{/snippet}

{#snippet account(inSheet)}
  <div class="account">
    <div class="user">
      {#if inSheet}
        <div class="account-btn static">
          <span class="avatar">{initials}</span>
          <span class="account-text"><span class="truncate">{state.username}</span><span class="t-caption">Version {state.version}</span></span>
        </div>
      {:else}
        <button class="account-btn" onclick={() => (userOpen = !userOpen)} aria-haspopup="menu" aria-expanded={userOpen} aria-label="Account and appearance">
          <span class="avatar">{initials}</span>
          <span class="account-text"><span class="truncate">{state.username}</span><span class="t-caption">Version {state.version}</span></span>
        </button>
      {/if}
    </div>
    <button class="icon-btn theme-btn" onclick={toggleTheme} aria-label={theme.current === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'} title={theme.current === 'dark' ? 'Light mode' : 'Dark mode'}>
      {#if theme.current === 'dark'}<Sun size={18} />{:else}<Moon size={18} />{/if}
    </button>
    {#if !inSheet}
      <Popover bind:open={userOpen} label="Account" up>
        <div class="pop-section">
          <span class="pop-label">Appearance</span>
          <Segmented options={themeModes} bind:value={mode} label="Appearance" />
        </div>
        <button class="pop-item" role="menuitem" onclick={signOut}><LogOut size={16} />Sign out</button>
      </Popover>
    {/if}
  </div>
  {#if inSheet}
    <div class="sheet-account">
      <Segmented options={themeModes} bind:value={mode} label="Appearance" />
      <button class="btn btn-gray btn-sm" onclick={signOut}><LogOut size={15} />Sign out</button>
    </div>
  {/if}
{/snippet}

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
  <div class="shell" class:fill>
    <aside class="sidebar">
      <a href="/" class="brand" onclick={(e) => go(e, '/')}>
        <Logo size={30} />
        <span>
          <span class="t-head">NexoVirt Box</span>
          <span class="t-caption truncate">{system?.hostname ?? ' '}</span>
        </span>
      </a>
      {@render nav()}
      <div class="side-foot">{@render account(false)}</div>
    </aside>

    <div class="content">
      <header class="mobile-bar">
        <button class="icon-btn" onclick={() => (menuOpen = true)} aria-label="Open navigation"><Menu size={20} /></button>
      </header>

      <main>
        {#if logsApp}
          {#key logsApp}<AppLogs id={logsApp} onSessionLost={sessionLost} onNavigate={navigate} />{/key}
        {:else if page.path === '/console'}
          <Console onSessionLost={sessionLost} />
        {:else if page.path === '/files'}
          {#key pageKey}<Files onSessionLost={sessionLost} />{/key}
        {:else if page.path === '/firewall'}
          <Firewall onSessionLost={sessionLost} />
        {:else if page.path === '/settings'}
          {#key pageKey}<Settings onSessionLost={sessionLost} />{/key}
        {:else if page.path === '/activity'}
          <Activity onSessionLost={sessionLost} />
        {:else if page.path === '/apps'}
          {#key pageKey}<AppStore onSessionLost={sessionLost} />{/key}
        {:else if page.path === '/apps/installed'}
          <MyApps onSessionLost={sessionLost} onNavigate={navigate} />
        {:else if page.path === '/monitor'}
          <Monitor {system} onSessionLost={sessionLost} />
        {:else}
          <Dashboard {system} username={state.username} onSessionLost={sessionLost} onNavigate={navigate} />
        {/if}
      </main>
    </div>
  </div>

  <Sheet bind:open={menuOpen} label="Navigation">
    <div class="sheet-brand">
      <Logo size={28} />
      <span>
        <span class="t-head">NexoVirt Box</span>
        <span class="t-caption">{system?.hostname ?? ''}</span>
      </span>
    </div>
    {@render nav()}
    <div class="sheet-foot">{@render account(true)}</div>
  </Sheet>
{/if}

<style>
  .center-screen { min-height: 100svh; display: grid; place-items: center; padding: 1rem; }
  .loading { display: flex; align-items: center; gap: 0.625rem; }
  .load-error { display: grid; gap: 1rem; width: min(24rem, 100%); }

  .shell { display: flex; min-height: 100svh; }
  .shell.fill { height: 100svh; }
  .shell.fill .content { min-height: 0; }
  .shell.fill main { min-height: 0; display: flex; flex-direction: column; padding-bottom: 0.5rem; }

  /* Floating translucent sidebar: the heavier structural material. */
  .sidebar {
    position: sticky; top: 0.5rem; align-self: flex-start; margin: 0.5rem 0 0.5rem 0.5rem;
    height: calc(100svh - 1rem); width: 15.5rem; flex-shrink: 0; display: flex; flex-direction: column;
    padding: 1rem 0.75rem; border-radius: 1.375rem; background: var(--material-thick);
    box-shadow: 0 0 0 1px var(--hairline);
    -webkit-backdrop-filter: blur(40px) saturate(180%); backdrop-filter: blur(40px) saturate(180%);
  }
  .brand { display: flex; align-items: center; gap: 0.75rem; padding: 0.25rem 0.5rem 1.25rem; border-radius: 0.75rem; }
  .brand > span, .sheet-brand > span { display: grid; min-width: 0; }

  .nav { display: grid; gap: 0.125rem; }
  .nav a {
    display: flex; align-items: center; gap: 0.75rem; min-height: 2.75rem; padding: 0 0.5rem;
    border-radius: 0.75rem; font-weight: 500; color: var(--text);
    transition: background-color 150ms ease, transform 100ms ease-out;
  }
  .nav a:hover { background: var(--fill); }
  .nav a:active { transform: scale(0.98); }
  .nav a[aria-current='page'] { background: var(--accent); color: var(--on-accent); }
  .nav-icon { display: grid; place-items: center; width: 1.25rem; color: var(--text-2); }
  .nav a[aria-current='page'] .nav-icon { color: inherit; }

  .content { flex: 1; min-width: 0; display: flex; flex-direction: column; }

  .user { position: relative; }
  .account { position: relative; display: flex; align-items: center; gap: 0.25rem; }
  .account .user { flex: 1; min-width: 0; }
  .account-btn {
    display: flex; align-items: center; gap: 0.625rem; width: 100%; min-height: 3rem; padding: 0.375rem 0.5rem;
    border: 0; border-radius: 0.875rem; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer;
    transition: background-color 150ms ease, transform 100ms ease-out;
  }
  .account-btn:hover, .account-btn[aria-expanded='true'] { background: var(--fill); }
  .account-btn:active { transform: scale(0.98); }
  .account-btn.static { cursor: default; }
  .account-btn.static:hover { background: none; }
  .account-text { display: grid; min-width: 0; font-weight: 600; }
  .theme-btn { flex-shrink: 0; }
  .side-foot { margin-top: auto; padding-top: 0.75rem; border-top: 1px solid var(--hairline); }
  .sheet-foot { margin-top: 0.75rem; padding-top: 0.75rem; border-top: 1px solid var(--hairline); }
  .sheet-account { display: flex; align-items: center; justify-content: space-between; gap: 0.75rem; padding: 0.5rem 0.5rem 0; }
  .mobile-bar { display: none; }
  .avatar {
    display: grid; place-items: center; width: 2rem; height: 2rem; border-radius: 999px;
    background: var(--accent-fill); color: var(--accent); font-size: 0.75rem; font-weight: 700; letter-spacing: 0.02em;
  }
  .pop-section { display: grid; gap: 0.5rem; padding: 0.5rem 0.5rem 0.625rem; }
  .pop-label { font-size: 0.8125rem; font-weight: 500; color: var(--text-2); padding: 0 0.125rem; }
  .pop-item {
    display: flex; align-items: center; gap: 0.625rem; width: 100%; min-height: 2.75rem; padding: 0 0.625rem;
    border: 0; border-top: 1px solid var(--hairline); background: none; cursor: pointer; text-align: left; font-weight: 500;
    border-radius: 0 0 0.75rem 0.75rem; transition: background-color 150ms ease;
  }
  .pop-item:hover { background: var(--fill); }

  main { flex: 1; padding: 1.75rem 2rem 2.5rem; }

  .sheet-brand { display: flex; align-items: center; gap: 0.75rem; padding: 0.25rem 0.5rem 1rem; }

  @media (max-width: 899px) {
    .sidebar { display: none; }
    .mobile-bar {
      position: sticky; top: 0; z-index: 20; display: flex; align-items: center; gap: 0.25rem;
      min-height: 3.25rem; padding: env(safe-area-inset-top, 0px) 0.5rem 0 0.25rem;
      background: color-mix(in srgb, var(--bg) 80%, transparent);
      -webkit-backdrop-filter: blur(24px) saturate(180%); backdrop-filter: blur(24px) saturate(180%);
    }
    main { padding: 0.5rem 1rem 2rem; }
  }
</style>
