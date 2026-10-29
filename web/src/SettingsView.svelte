<script>
  // Settings window (System Settings style): groups on the left, the section on the right.
  // Every former sidebar page lives here as a section; the URL is /settings/<section>.
  // Phones show the list first and drill down into a section.
  import Lazy from '$lib/Lazy.svelte';
  import SlidersHorizontal from '@lucide/svelte/icons/sliders-horizontal';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import Package from '@lucide/svelte/icons/package';
  import Network from '@lucide/svelte/icons/network';
  import Shield from '@lucide/svelte/icons/shield';
  import Share2 from '@lucide/svelte/icons/share-2';
  import HardDrive from '@lucide/svelte/icons/hard-drive';
  import PackageCheck from '@lucide/svelte/icons/package-check';
  import Sparkles from '@lucide/svelte/icons/sparkles';
  import Archive from '@lucide/svelte/icons/archive';
  import BellRing from '@lucide/svelte/icons/bell-ring';
  import History from '@lucide/svelte/icons/history';
  import Info from '@lucide/svelte/icons/info';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import Search from '@lucide/svelte/icons/search';

  let { section = '', pageKey = 0, onNavigate, onSessionLost } = $props();

  const GROUPS = [
    [
      { key: 'general', label: 'General', icon: SlidersHorizontal, hint: 'Wallpaper, appearance, name' },
      { key: 'security', label: 'Account and security', icon: ShieldCheck, hint: 'Password, two-step sign-in, sessions' },
    ],
    [
      { key: 'apps', label: 'Apps and catalog', icon: Package, hint: 'Catalog address, image cleanup' },
      { key: 'network', label: 'Network and domains', icon: Network, hint: 'Reverse proxy, HTTPS, domains' },
      { key: 'firewall', label: 'Firewall', icon: Shield, hint: 'Open or block ports' },
      { key: 'shares', label: 'Shares', icon: Share2, hint: 'Samba, NFS and links', fill: false },
      { key: 'disks', label: 'Disks', icon: HardDrive, hint: 'Health and SMART' },
    ],
    [
      { key: 'system-updates', label: 'System updates', icon: PackageCheck, hint: 'Operating system packages' },
      { key: 'box-updates', label: 'Box updates', icon: Sparkles, hint: 'New versions of NexoVirt Box' },
      { key: 'backups', label: 'Backups', icon: Archive, hint: 'Targets, schedules, restores', fill: true },
    ],
    [
      { key: 'notifications', label: 'Notifications', icon: BellRing, hint: 'Email, ntfy, webhooks' },
      { key: 'activity', label: 'Activity', icon: History, hint: 'Sign-ins and changes', fill: true },
      { key: 'about', label: 'About', icon: Info, hint: 'Version and licenses' },
    ],
  ];
  const ALL = GROUPS.flat();
  // Each section is its own chunk, loaded when it is first opened.
  const LOAD = {
    settings: () => import('./Settings.svelte'), firewall: () => import('./Firewall.svelte'), shares: () => import('./Shares.svelte'),
    disks: () => import('./Disks.svelte'), 'system-updates': () => import('./Updates.svelte'), backups: () => import('./Backups.svelte'),
    activity: () => import('./Activity.svelte'),
  };
  // Sections that come from the tabs of Settings.svelte.
  const TAB = { general: 'general', security: 'security', apps: 'apps', network: 'network', notifications: 'notifications', 'box-updates': 'updates', about: 'about' };

  let filter = $state('');
  let narrow = $state(matchMedia('(max-width: 759px)').matches);
  $effect(() => {
    const m = matchMedia('(max-width: 759px)');
    const on = () => (narrow = m.matches);
    m.addEventListener('change', on);
    return () => m.removeEventListener('change', on);
  });

  const current = $derived(ALL.find((s) => s.key === section) || (narrow ? null : ALL[0]));
  const q = $derived(filter.trim().toLowerCase());
  const groups = $derived(q ? [ALL.filter((s) => (s.label + ' ' + s.hint).toLowerCase().includes(q))] : GROUPS);

  function open(key) { onNavigate('/settings/' + key); }
</script>

<div class="settings" class:drill={narrow} class:in-section={narrow && current}>
  <nav class="list" aria-label="Settings sections">
    <label class="find">
      <Search size={15} />
      <input bind:value={filter} placeholder="Search settings" aria-label="Search settings" spellcheck="false" autocomplete="off" />
    </label>
    {#each groups as g, gi (gi)}
      <div class="lgroup">
        {#each g as s (s.key)}
          {@const Icon = s.icon}
          <a href="/settings/{s.key}" class="item" class:on={current?.key === s.key && !narrow}
            aria-current={current?.key === s.key ? 'page' : undefined}
            onclick={(e) => { e.preventDefault(); open(s.key); }}>
            <span class="ico item-ico"><Icon /></span>
            <span class="item-text"><span class="truncate">{s.label}</span>{#if narrow}<span class="t-caption truncate">{s.hint}</span>{/if}</span>
            {#if narrow}<ChevronRight size={16} class="chev" />{/if}
          </a>
        {/each}
      </div>
    {:else}
      <p class="t-caption none">No setting matches.</p>
    {/each}
  </nav>

  {#if current}
    <div class="pane" class:fill={current.fill}>
      <div class="pane-inner" class:fill={current.fill}>
        {#if narrow}
          <button class="back" onclick={() => onNavigate('/settings')}><ChevronLeft size={18} />Settings</button>
        {/if}
        <h2 class="t-large pane-title">{current.label}</h2>
        {#key current.key + ':' + pageKey}
          <Lazy load={LOAD[TAB[current.key] ? 'settings' : current.key]} section={TAB[current.key]} {onSessionLost} />
        {/key}
      </div>
    </div>
  {/if}
</div>

<style>
  .settings { display: grid; grid-template-columns: 17rem minmax(0, 1fr); height: 100%; min-height: 0; }
  .list {
    display: grid; align-content: start; gap: 0.875rem; padding: 1rem 0.75rem calc(1.5rem + env(safe-area-inset-bottom, 0px)) max(0.75rem, env(safe-area-inset-left, 0px));
    overflow-y: auto; background: var(--material-thick); box-shadow: 1px 0 0 var(--hairline);
  }
  .find { display: flex; align-items: center; gap: 0.5rem; height: 2.25rem; padding: 0 0.75rem; border-radius: 0.75rem; background: var(--fill); color: var(--text-2); }
  .find input { flex: 1; min-width: 0; border: 0; background: none; font-size: 0.875rem; }
  .find input:focus-visible { outline: none; }
  .find:focus-within { box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 40%, transparent); }
  .lgroup { display: grid; gap: 0.0625rem; }
  .item {
    display: flex; align-items: center; gap: 0.625rem; min-height: 2.375rem; padding: 0 0.625rem; border-radius: 0.625rem;
    font-weight: 500; color: var(--text); transition: background-color 150ms ease, transform 100ms ease-out;
  }
  .item:hover { background: var(--fill); }
  .item:active { transform: scale(0.98); }
  .item.on { background: var(--accent); color: var(--on-accent); }
  .item-ico { color: var(--accent); }
  .item.on .item-ico { color: inherit; }
  .item-text { flex: 1; min-width: 0; display: grid; }
  .none { padding: 0 0.625rem; margin: 0; }

  .pane { min-width: 0; min-height: 0; overflow-y: auto; overscroll-behavior: contain; }
  .pane.fill { overflow: hidden; display: flex; flex-direction: column; }
  .pane-inner {
    display: grid; gap: 1.25rem; align-content: start; max-width: 64rem; margin-inline: auto;
    padding: 1.75rem max(2rem, env(safe-area-inset-right, 0px)) calc(2.5rem + env(safe-area-inset-bottom, 0px)) 2rem;
  }
  .pane-inner.fill { display: flex; flex-direction: column; flex: 1; min-height: 0; width: 100%; max-width: 90rem; padding-bottom: 1rem; }
  .pane-inner.fill > :global(.page.fill) { flex: 1; min-height: 0; }
  .pane-title { margin: 0; }
  /* The section title above replaces the page's own large title. */
  .pane :global(.page-head h1) { display: none; }
  .pane :global(.page-head p) { margin-top: 0; }
  .pane :global(.page-head:not(:has(p, button, a, select, input, .pill))) { display: none; }

  .back { justify-self: start; display: inline-flex; align-items: center; gap: 0.125rem; min-height: 2.75rem; margin: -0.75rem 0 -0.5rem -0.5rem; padding: 0 0.5rem; border: 0; background: none; color: var(--accent); font: inherit; font-weight: 500; cursor: pointer; }

  /* Phones: one column, list first, then the section. */
  .drill { grid-template-columns: 1fr; }
  .drill .list { background: none; box-shadow: none; padding: 1rem max(1rem, env(safe-area-inset-right, 0px)) 2rem max(1rem, env(safe-area-inset-left, 0px)); }
  .drill .lgroup { background: var(--surface); border-radius: var(--r-tile); box-shadow: var(--shadow-tile); padding: 0.25rem; }
  .drill .item { min-height: 3.25rem; }
  .drill .item :global(.chev) { color: var(--text-3); }
  .drill.in-section .list { display: none; }
  .drill .pane-inner { padding: 1rem max(1rem, env(safe-area-inset-right, 0px)) calc(2rem + env(safe-area-inset-bottom, 0px)) max(1rem, env(safe-area-inset-left, 0px)); }
  .drill .find { height: 2.75rem; }
  .drill .pane-title { font-size: 1.625rem; }
</style>
