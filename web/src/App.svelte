<script>
  import { onMount } from 'svelte';
  import { api } from './lib/api.js';
  import Auth from './Auth.svelte';
  import Dashboard from './Dashboard.svelte';
  import Console from './Console.svelte';
  import Logo from './lib/Logo.svelte';
  import Icon from './lib/Icon.svelte';

  const pages = [
    { path: '/', title: 'Dashboard', icon: 'layout-dashboard' },
    { path: '/console', title: 'Console', icon: 'terminal-2' },
  ];

  let state = $state(null); // { setup_needed, logged_in, username, version }
  let loadError = $state('');
  let path = $state(location.pathname);
  let menuOpen = $state(false);
  let theme = $state(document.documentElement.getAttribute('data-bs-theme') || 'light');
  let signingOut = $state(false);

  async function loadState() {
    loadError = '';
    const res = await api('/api/auth/state');
    if (!res.ok) {
      loadError = res.error;
      return;
    }
    state = res.data;
  }

  function go(e, to) {
    e.preventDefault();
    menuOpen = false;
    if (to !== path) {
      history.pushState({}, '', to);
      path = to;
    }
  }

  function toggleTheme() {
    theme = theme === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-bs-theme', theme);
    try { localStorage.setItem('nxbox-theme', theme); } catch {}
  }

  async function signOut() {
    signingOut = true;
    await api('/api/logout', { method: 'POST' });
    signingOut = false;
    await loadState();
  }

  // A page reports a lost session (401): show the sign-in again.
  function sessionLost() {
    state = { ...state, logged_in: false };
  }

  onMount(() => {
    loadState();
    const onPop = () => (path = location.pathname);
    addEventListener('popstate', onPop);
    return () => removeEventListener('popstate', onPop);
  });

  let page = $derived(pages.find((p) => p.path === path) || pages[0]);
</script>

{#if !state}
  <div class="page page-center">
    <div class="container container-tight py-4 text-center">
      {#if loadError}
        <div class="alert alert-danger text-start" role="alert">{loadError}</div>
        <button class="btn" onclick={loadState}><Icon name="refresh" />Try again</button>
      {:else}
        <div class="spinner-border text-secondary" role="status"></div>
        <div class="text-secondary mt-3">Loading NexoVirt Box...</div>
      {/if}
    </div>
  </div>
{:else if state.setup_needed}
  <Auth mode="setup" onDone={loadState} />
{:else if !state.logged_in}
  <Auth mode="login" onDone={loadState} />
{:else}
  <div class="page">
    <aside class="navbar navbar-vertical navbar-expand-lg">
      <div class="container-fluid">
        <button class="navbar-toggler" type="button" aria-label="Toggle navigation" aria-expanded={menuOpen} onclick={() => (menuOpen = !menuOpen)}>
          <span class="navbar-toggler-icon"></span>
        </button>
        <a href="/" class="navbar-brand d-flex align-items-center gap-2" onclick={(e) => go(e, '/')}>
          <Logo size={30} />
          <span class="fw-bold">NexoVirt Box</span>
        </a>
        <div class="collapse navbar-collapse" class:show={menuOpen}>
          <ul class="navbar-nav pt-lg-3">
            {#each pages as p}
              <li class="nav-item" class:active={p.path === page.path}>
                <a class="nav-link" href={p.path} onclick={(e) => go(e, p.path)}>
                  <span class="nav-link-icon d-md-none d-lg-inline-block"><Icon name={p.icon} /></span>
                  <span class="nav-link-title">{p.title}</span>
                </a>
              </li>
            {/each}
          </ul>
          <div class="mt-auto pb-3 px-lg-3">
            <div class="d-flex align-items-center gap-2">
              <span class="avatar avatar-sm bg-secondary-lt text-secondary">{state.username.slice(0, 2).toUpperCase()}</span>
              <div class="flex-fill text-truncate">
                <div class="fw-medium text-truncate">{state.username}</div>
                <div class="small text-secondary">Version {state.version}</div>
              </div>
              <button class="btn btn-icon btn-ghost-secondary" title={theme === 'dark' ? 'Light mode' : 'Dark mode'} aria-label="Toggle theme" onclick={toggleTheme}>
                <Icon name={theme === 'dark' ? 'sun' : 'moon'} />
              </button>
              <button class="btn btn-icon btn-ghost-secondary" class:btn-loading={signingOut} title="Sign out" aria-label="Sign out" onclick={signOut}>
                <Icon name="logout" />
              </button>
            </div>
          </div>
        </div>
      </div>
    </aside>
    <div class="page-wrapper">
      {#if page.path === '/console'}
        <Console onSessionLost={sessionLost} />
      {:else}
        <Dashboard onSessionLost={sessionLost} />
      {/if}
    </div>
  </div>
{/if}
