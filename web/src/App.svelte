<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { theme, setThemeMode } from '$lib/theme.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import Auth from './Auth.svelte';
  import Dashboard from './Dashboard.svelte';
  import Console from './Console.svelte';
  import Logo from '$lib/Logo.svelte';
  import Sheet from '$lib/Sheet.svelte';
  import Popover from '$lib/Popover.svelte';
  import Toaster from '$lib/Toaster.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import Gauge from '@lucide/svelte/icons/gauge';
  import SquareTerminal from '@lucide/svelte/icons/square-terminal';
  import Menu from '@lucide/svelte/icons/menu';
  import LogOut from '@lucide/svelte/icons/log-out';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Sun from '@lucide/svelte/icons/sun';
  import Moon from '@lucide/svelte/icons/moon';

  const pages = [
    { path: '/', title: 'Dashboard', icon: Gauge },
    { path: '/console', title: 'Console', icon: SquareTerminal },
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
  let scrolled = $state(false);
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
    if (state.logged_in) loadSystem();
  }

  async function loadSystem() {
    const res = await api('/api/system');
    if (res.status === 401) return sessionLost();
    if (!res.ok) toast(res.error, 'error');
    else system = res.data;
  }

  function go(e, to) {
    e.preventDefault();
    menuOpen = false;
    if (to !== path) {
      history.pushState({}, '', to);
      path = to;
      scrollTo({ top: 0 });
    }
  }

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
    loadState();
    const onPop = () => (path = location.pathname);
    const onScroll = () => (scrolled = scrollY > 4);
    addEventListener('popstate', onPop);
    addEventListener('scroll', onScroll, { passive: true });
    return () => {
      removeEventListener('popstate', onPop);
      removeEventListener('scroll', onScroll);
    };
  });

  let page = $derived(pages.find((p) => p.path === path) || pages[0]);
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
  <div class="shell">
    <aside class="sidebar">
      <a href="/" class="brand" onclick={(e) => go(e, '/')}>
        <Logo size={30} />
        <span>
          <span class="t-head">NexoVirt Box</span>
          <span class="t-caption truncate">{system?.hostname ?? ' '}</span>
        </span>
      </a>
      {@render nav()}
      <div class="side-foot t-caption">Version {state.version}</div>
    </aside>

    <div class="content">
      <header class="toolbar" class:scrolled>
        <button class="icon-btn menu-btn" onclick={() => (menuOpen = true)} aria-label="Open navigation"><Menu size={20} /></button>
        <span class="toolbar-title t-head">{page.title}</span>
        <div class="toolbar-end">
          <button class="icon-btn" onclick={toggleTheme} aria-label={theme.current === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'} title={theme.current === 'dark' ? 'Light mode' : 'Dark mode'}>
            {#if theme.current === 'dark'}<Sun size={19} />{:else}<Moon size={19} />{/if}
          </button>
          <div class="user">
            <button class="avatar-btn" onclick={() => (userOpen = !userOpen)} aria-haspopup="menu" aria-expanded={userOpen} aria-label="Account and appearance">
              <span class="avatar">{initials}</span>
            </button>
            <Popover bind:open={userOpen} label="Account">
              <div class="pop-head">
                <span class="avatar lg">{initials}</span>
                <span>
                  <span class="t-head">{state.username}</span>
                  <span class="t-caption">Administrator</span>
                </span>
              </div>
              <div class="pop-section">
                <span class="t-section">Appearance</span>
                <Segmented options={themeModes} bind:value={mode} label="Appearance" />
              </div>
              <button class="pop-item" role="menuitem" onclick={signOut}><LogOut size={16} />Sign out</button>
            </Popover>
          </div>
        </div>
      </header>

      <main>
        {#if page.path === '/console'}
          <Console onSessionLost={sessionLost} />
        {:else}
          <Dashboard {system} onSessionLost={sessionLost} />
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
  </Sheet>
{/if}

<style>
  .center-screen { min-height: 100svh; display: grid; place-items: center; padding: 1rem; }
  .loading { display: flex; align-items: center; gap: 0.625rem; }
  .load-error { display: grid; gap: 1rem; width: min(24rem, 100%); }

  .shell { display: flex; min-height: 100svh; }

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
  .side-foot { margin-top: auto; padding: 0 0.625rem; color: var(--text-3); }

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

  /* Toolbar floats over the content; a hairline appears only once content scrolls under it. */
  .toolbar {
    position: sticky; top: 0; z-index: 20; display: flex; align-items: center; gap: 0.5rem;
    height: 3.75rem; padding: 0 1rem 0 1.5rem; padding-top: env(safe-area-inset-top, 0px);
    background: color-mix(in srgb, var(--bg) 72%, transparent);
    -webkit-backdrop-filter: blur(24px) saturate(180%); backdrop-filter: blur(24px) saturate(180%);
    box-shadow: 0 1px 0 transparent; transition: box-shadow 200ms ease;
  }
  .toolbar.scrolled { box-shadow: 0 1px 0 var(--hairline); }
  .toolbar-title { opacity: 0; transform: translateY(4px); transition: opacity 200ms ease, transform 300ms var(--spring); }
  .toolbar.scrolled .toolbar-title { opacity: 1; transform: none; }
  .toolbar-end { margin-left: auto; display: flex; align-items: center; }
  .menu-btn { display: none; margin-left: -0.5rem; }

  .user { position: relative; }
  .avatar-btn {
    display: grid; place-items: center; width: 2.75rem; height: 2.75rem; border: 0; padding: 0;
    background: none; border-radius: 999px; cursor: pointer; transition: transform 100ms ease-out;
  }
  .avatar-btn:active { transform: scale(0.94); }
  .avatar {
    display: grid; place-items: center; width: 2rem; height: 2rem; border-radius: 999px;
    background: var(--accent-fill); color: var(--accent); font-size: 0.75rem; font-weight: 700; letter-spacing: 0.02em;
  }
  .avatar.lg { width: 2.5rem; height: 2.5rem; font-size: 0.875rem; }
  .pop-head { display: flex; align-items: center; gap: 0.75rem; padding: 0.625rem 0.625rem 0.75rem; }
  .pop-head > span:last-child { display: grid; }
  .pop-section { display: grid; gap: 0.5rem; padding: 0.625rem; border-top: 1px solid var(--hairline); }
  .pop-item {
    display: flex; align-items: center; gap: 0.625rem; width: 100%; min-height: 2.75rem; padding: 0 0.625rem;
    border: 0; border-top: 1px solid var(--hairline); background: none; cursor: pointer; text-align: left; font-weight: 500;
    border-radius: 0 0 0.75rem 0.75rem; transition: background-color 150ms ease;
  }
  .pop-item:hover { background: var(--fill); }

  main { flex: 1; padding: 0.25rem 1.5rem 2.5rem; }

  .sheet-brand { display: flex; align-items: center; gap: 0.75rem; padding: 0.25rem 0.5rem 1rem; }

  @media (max-width: 899px) {
    .sidebar { display: none; }
    .menu-btn { display: inline-grid; }
    .toolbar { padding: 0 0.75rem 0 1rem; }
    main { padding: 0.25rem 1rem 2rem; }
  }
</style>
