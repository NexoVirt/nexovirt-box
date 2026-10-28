<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { theme, toggleTheme } from '$lib/theme.svelte.js';
  import Auth from './Auth.svelte';
  import Dashboard from './Dashboard.svelte';
  import Console from './Console.svelte';
  import Logo from '$lib/Logo.svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import * as Sheet from '$lib/components/ui/sheet/index.js';
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
  import { Toaster } from '$lib/components/ui/sonner/index.js';
  import * as Alert from '$lib/components/ui/alert/index.js';
  import { toast } from 'svelte-sonner';
  import LayoutDashboard from '@lucide/svelte/icons/layout-dashboard';
  import SquareTerminal from '@lucide/svelte/icons/square-terminal';
  import Menu from '@lucide/svelte/icons/menu';
  import Sun from '@lucide/svelte/icons/sun';
  import Moon from '@lucide/svelte/icons/moon';
  import LogOut from '@lucide/svelte/icons/log-out';
  import ChevronDown from '@lucide/svelte/icons/chevron-down';
  import Server from '@lucide/svelte/icons/server';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import Loader from '@lucide/svelte/icons/loader-circle';

  const pages = [
    { path: '/', title: 'Dashboard', icon: LayoutDashboard },
    { path: '/console', title: 'Console', icon: SquareTerminal },
  ];

  let state = $state(null); // { setup_needed, logged_in, username, version }
  let loadError = $state('');
  let system = $state(null);
  let path = $state(location.pathname);
  let menuOpen = $state(false);

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
    if (!res.ok) toast.error(res.error);
    else system = res.data;
  }

  function go(e, to) {
    e.preventDefault();
    menuOpen = false;
    if (to !== path) {
      history.pushState({}, '', to);
      path = to;
    }
  }

  async function signOut() {
    const res = await api('/api/logout', { method: 'POST' });
    if (!res.ok) toast.error(res.error);
    system = null;
    await loadState();
  }

  // A page reports a lost session (401): show the sign-in again.
  function sessionLost() {
    state = { ...state, logged_in: false };
    system = null;
    toast.info('Your session ended. Please sign in again.');
  }

  onMount(() => {
    loadState();
    const onPop = () => (path = location.pathname);
    addEventListener('popstate', onPop);
    return () => removeEventListener('popstate', onPop);
  });

  let page = $derived(pages.find((p) => p.path === path) || pages[0]);
</script>

<Toaster richColors position="top-right" />

{#snippet nav()}
  <nav class="grid gap-1">
    {#each pages as p (p.path)}
      {@const Icon = p.icon}
      <a
        href={p.path}
        onclick={(e) => go(e, p.path)}
        aria-current={p.path === page.path ? 'page' : undefined}
        class="flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none aria-[current=page]:bg-accent aria-[current=page]:text-foreground"
      >
        <Icon class="size-4" />
        {p.title}
      </a>
    {/each}
  </nav>
{/snippet}

{#snippet brand()}
  <a href="/" onclick={(e) => go(e, '/')} class="flex items-center gap-2.5 rounded-md text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none">
    <Logo size={28} />
    <span class="font-semibold tracking-tight">NexoVirt Box</span>
  </a>
{/snippet}

{#if !state}
  <div class="flex min-h-svh items-center justify-center p-4">
    {#if loadError}
      <div class="grid w-full max-w-sm gap-4">
        <Alert.Root variant="destructive">
          <CircleAlert />
          <Alert.Description>{loadError}</Alert.Description>
        </Alert.Root>
        <Button variant="outline" onclick={loadState}><RefreshCw />Try again</Button>
      </div>
    {:else}
      <div class="flex items-center gap-3 text-sm text-muted-foreground">
        <Loader class="size-4 animate-spin" />Loading NexoVirt Box...
      </div>
    {/if}
  </div>
{:else if state.setup_needed}
  <Auth mode="setup" onDone={loadState} />
{:else if !state.logged_in}
  <Auth mode="login" onDone={loadState} />
{:else}
  <div class="flex min-h-svh">
    <aside class="sticky top-0 hidden h-svh w-60 shrink-0 flex-col border-r bg-sidebar px-4 py-5 lg:flex">
      <div class="mb-8 px-2">{@render brand()}</div>
      {@render nav()}
      <div class="mt-auto px-3 text-xs text-muted-foreground" title="Version {state.version}">v{state.version}</div>
    </aside>

    <div class="flex min-w-0 flex-1 flex-col">
      <header class="sticky top-0 z-10 flex h-14 items-center gap-3 border-b bg-background/80 px-4 backdrop-blur lg:px-8">
        <Sheet.Root bind:open={menuOpen}>
          <Sheet.Trigger>
            {#snippet child({ props })}
              <Button {...props} variant="ghost" size="icon" class="lg:hidden" aria-label="Open navigation"><Menu /></Button>
            {/snippet}
          </Sheet.Trigger>
          <Sheet.Content side="left" class="w-64 p-5">
            <Sheet.Header class="mb-6 p-0">
              <Sheet.Title>{@render brand()}</Sheet.Title>
            </Sheet.Header>
            {@render nav()}
          </Sheet.Content>
        </Sheet.Root>

        <div class="flex min-w-0 items-center gap-2 text-sm">
          <Server class="size-4 shrink-0 text-muted-foreground" />
          {#if system}
            <span class="truncate font-medium">{system.hostname}</span>
          {:else}
            <span class="h-4 w-28 animate-pulse rounded bg-muted"></span>
          {/if}
        </div>

        <div class="ml-auto flex items-center gap-1">
          <Button variant="ghost" size="icon" onclick={toggleTheme} aria-label={theme.current === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}>
            {#if theme.current === 'dark'}<Sun />{:else}<Moon />{/if}
          </Button>
          <DropdownMenu.Root>
            <DropdownMenu.Trigger>
              {#snippet child({ props })}
                <Button {...props} variant="ghost" class="gap-2 px-2">
                  <span class="flex size-7 items-center justify-center rounded-full bg-secondary text-xs font-semibold">
                    {state.username.slice(0, 2).toUpperCase()}
                  </span>
                  <span class="hidden text-sm sm:inline">{state.username}</span>
                  <ChevronDown class="size-4 text-muted-foreground" />
                </Button>
              {/snippet}
            </DropdownMenu.Trigger>
            <DropdownMenu.Content align="end" class="w-48">
              <DropdownMenu.Label class="font-normal">
                <div class="text-sm font-medium">{state.username}</div>
                <div class="text-xs text-muted-foreground">Administrator</div>
              </DropdownMenu.Label>
              <DropdownMenu.Separator />
              <DropdownMenu.Item onSelect={signOut}><LogOut />Sign out</DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Root>
        </div>
      </header>

      <main class="flex-1 px-4 py-6 lg:px-8 lg:py-8">
        {#if page.path === '/console'}
          <Console onSessionLost={sessionLost} />
        {:else}
          <Dashboard {system} onSessionLost={sessionLost} />
        {/if}
      </main>
    </div>
  </div>
{/if}
