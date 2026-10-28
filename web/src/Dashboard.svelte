<script>
  // Home: a desktop for the server. A launcher with the installed apps (click opens the app,
  // right-click or long-press for actions), widgets for system, network and storage, a clock and
  // a search that finds apps, settings, pages and folders.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes, rate, pct, duration } from '$lib/format.js';
  import { runJob, appURL } from '$lib/jobs.js';
  import AppIcon from '$lib/AppIcon.svelte';
  import Ring from '$lib/Ring.svelte';
  import Sparkline from '$lib/Sparkline.svelte';
  import ContextMenu from '$lib/ContextMenu.svelte';
  import Search from '@lucide/svelte/icons/search';
  import Plus from '@lucide/svelte/icons/plus';
  import ArrowDown from '@lucide/svelte/icons/arrow-down';
  import ArrowUp from '@lucide/svelte/icons/arrow-up';
  import HardDrive from '@lucide/svelte/icons/hard-drive';
  import Gauge from '@lucide/svelte/icons/gauge';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import Play from '@lucide/svelte/icons/play';
  import Square from '@lucide/svelte/icons/square';
  import RotateCw from '@lucide/svelte/icons/rotate-cw';
  import Boxes from '@lucide/svelte/icons/boxes';
  import LayoutGrid from '@lucide/svelte/icons/layout-grid';
  import FolderOpen from '@lucide/svelte/icons/folder-open';
  import Shield from '@lucide/svelte/icons/shield';
  import SquareTerminal from '@lucide/svelte/icons/square-terminal';
  import History from '@lucide/svelte/icons/history';
  import Settings from '@lucide/svelte/icons/settings';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';

  let { system, username = '', onSessionLost, onNavigate } = $props();

  const PLACES = [
    { label: 'Monitor', hint: 'Live charts for CPU, memory and network', to: '/monitor', icon: Gauge },
    { label: 'App Store', hint: 'Install apps', to: '/apps', icon: LayoutGrid },
    { label: 'My Apps', hint: 'Start, stop, update and logs', to: '/apps/installed', icon: Boxes },
    { label: 'Files', hint: 'Browse and upload files', to: '/files', icon: FolderOpen },
    { label: 'Firewall', hint: 'Open or block ports', to: '/firewall', icon: Shield },
    { label: 'Console', hint: 'Root shell in the browser', to: '/console', icon: SquareTerminal },
    { label: 'Activity', hint: 'Sign-ins and changes', to: '/activity', icon: History },
    { label: 'Settings: Security', hint: 'Password, two-step sign-in, browsers', to: '/settings#security', icon: Settings },
    { label: 'Settings: Apps and catalog', hint: 'Catalog address, image cleanup', to: '/settings#apps', icon: Settings },
    { label: 'Settings: Network', hint: 'Reverse proxy, HTTPS and domains', to: '/settings#network', icon: Settings },
    { label: 'Settings: Updates', hint: 'Update NexoVirt Box', to: '/settings#updates', icon: Settings },
    { label: 'Settings: General', hint: 'Appearance and folders in Files', to: '/settings#general', icon: Settings },
  ];
  const STATE = { running: 'ok', partial: 'warn', restarting: 'warn', stopped: '', exited: '' };

  let apps = $state(null);
  let appsError = $state('');
  let proxy = $state(null);
  let busy = $state({});
  let snap = $state(null);
  let cpuHist = $state([]), netHist = $state([]);
  let seq = $state(0);
  let now = $state(new Date());
  let query = $state(''), searchOpen = $state(false), active = $state(0);
  let catalog = null, catalogLoading = $state(false), catalogApps = $state([]);
  let menu = $state(null), menuApp = null;
  let searchEl = $state();
  let narrow = $state(typeof matchMedia !== 'undefined' && matchMedia('(max-width: 639px)').matches);

  const greeting = $derived(now.getHours() < 5 ? 'Good night' : now.getHours() < 12 ? 'Good morning' : now.getHours() < 18 ? 'Good afternoon' : 'Good evening');
  const timeText = $derived(now.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }));
  const dateText = $derived(now.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' }));
  const memPct = $derived(snap ? pct(snap.memory.used, snap.memory.total) : 0);
  const rootDisk = $derived(snap ? snap.disks.find((d) => d.mountpoint === '/') || snap.disks[0] : null);
  const diskPct = $derived(rootDisk ? pct(rootDisk.used, rootDisk.total) : 0);
  const net = $derived(snap ? snap.network.reduce((a, n) => ({ rx: a.rx + n.rx_rate, tx: a.tx + n.tx_rate }), { rx: 0, tx: 0 }) : { rx: 0, tx: 0 });
  const running = $derived(apps ? apps.filter((a) => a.state === 'running').length : 0);

  async function loadApps() {
    const [a, p] = await Promise.all([api('/api/apps'), api('/api/proxy')]);
    if (a.status === 401) return onSessionLost();
    if (!a.ok) { appsError = a.error; apps = []; return; }
    apps = a.data;
    if (p.ok) proxy = p.data;
  }

  function openURL(app) {
    if (app.domain && proxy?.enabled && proxy.running) {
      const port = proxy.config.https_port;
      return `https://${app.domain}${port === 443 ? '' : ':' + port}`;
    }
    return appURL(app);
  }

  function launch(app) {
    const url = openURL(app);
    if (!url) return onNavigate('/apps/installed');
    if (app.state !== 'running') toast(`${app.name} is not running. Start it first.`, 'info');
    window.open(url, '_blank', 'noopener,noreferrer');
  }

  // Right-click, or a long press on touch screens, opens the app menu.
  function appMenu(e, app) {
    e.preventDefault();
    menuApp = app;
    const up = app.state === 'running';
    menu = {
      x: e.clientX, y: e.clientY,
      items: [
        { key: 'open', label: `Open ${app.name}`, icon: ExternalLink, disabled: !openURL(app) },
        'sep',
        { key: 'start', label: 'Start', icon: Play, disabled: up || !!busy[app.id] },
        { key: 'stop', label: 'Stop', icon: Square, disabled: !up || !!busy[app.id] },
        { key: 'restart', label: 'Restart', icon: RotateCw, disabled: !up || !!busy[app.id] },
        'sep',
        { key: 'manage', label: 'Open in My Apps', icon: Boxes },
      ],
    };
  }
  let pressTimer;
  function pressStart(e, app) {
    if (e.pointerType !== 'touch') return;
    pressTimer = setTimeout(() => appMenu({ preventDefault() {}, clientX: e.clientX, clientY: e.clientY }, app), 500);
  }
  function pressEnd() { clearTimeout(pressTimer); }

  async function pick(key) {
    const app = menuApp;
    if (!app) return;
    if (key === 'open') return launch(app);
    if (key === 'manage') return onNavigate('/apps/installed');
    busy = { ...busy, [app.id]: key };
    const job = await runJob(`/api/apps/${app.id}/action`, { op: key });
    const { [app.id]: _, ...rest } = busy;
    busy = rest;
    if (job.http === 401) return onSessionLost();
    if (job.status === 'done') toast(`${app.name} ${key === 'start' ? 'started' : key === 'stop' ? 'stopped' : 'restarted'}.`, 'success');
    else toast(job.error, 'error', 8000);
    loadApps();
  }

  // Search: installed apps, pages and settings, App Store apps (loaded on first use), and folders for "/" paths.
  async function ensureCatalog() {
    if (catalog || catalogLoading) return;
    catalogLoading = true;
    const res = await api('/api/catalog');
    catalogLoading = false;
    if (res.ok) { catalog = res.data.apps; catalogApps = catalog; }
  }
  const results = $derived.by(() => {
    const q = query.trim().toLowerCase();
    if (!q) return [];
    const out = [];
    if (q.startsWith('/')) out.push({ kind: 'Folder', label: query.trim(), hint: 'Open in Files', icon: FolderOpen, to: '/files?path=' + encodeURIComponent(query.trim().replace(/\/+$/, '') || '/') });
    for (const a of apps || []) if (a.name.toLowerCase().includes(q)) out.push({ kind: 'Installed', label: a.name, hint: a.state === 'running' ? 'Running' : 'Not running', app: a });
    for (const p of PLACES) if ((p.label + ' ' + p.hint).toLowerCase().includes(q)) out.push({ kind: 'Go to', label: p.label, hint: p.hint, icon: p.icon, to: p.to });
    const have = new Set((apps || []).map((a) => a.id));
    let n = 0;
    for (const a of catalogApps) {
      if (n >= 6) break;
      if (!have.has(a.id) && (a.name.toLowerCase().includes(q) || (a.category || '').toLowerCase().includes(q))) {
        out.push({ kind: 'App Store', label: a.name, hint: a.tagline || a.category, store: a, to: '/apps?app=' + encodeURIComponent(a.id) });
        n++;
      }
    }
    return out.slice(0, 14);
  });
  $effect(() => { query; active = 0; });

  function choose(r) {
    searchOpen = false;
    query = '';
    if (r.app) return launch(r.app);
    onNavigate(r.to);
  }
  function searchKey(e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); active = Math.min(results.length - 1, active + 1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); active = Math.max(0, active - 1); }
    else if (e.key === 'Enter' && results[active]) { e.preventDefault(); choose(results[active]); }
    else if (e.key === 'Escape') { searchOpen = false; searchEl?.blur(); }
  }
  // "/" or Cmd/Ctrl+K focuses the search from anywhere on the page.
  function globalKey(e) {
    if (e.target.closest?.('input, textarea')) return;
    if (e.key === '/' || ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k')) { e.preventDefault(); searchEl?.focus(); }
  }

  onMount(() => {
    loadApps();
    const tick = setInterval(() => (now = new Date()), 15000);
    const es = new EventSource('/api/stats/stream');
    const take = (s) => {
      snap = s;
      cpuHist = [...cpuHist, s.cpu.total].slice(-60);
      netHist = [...netHist, s.network.reduce((a, n) => a + n.rx_rate + n.tx_rate, 0)].slice(-60);
      seq++;
    };
    es.addEventListener('history', (e) => {
      try {
        const list = JSON.parse(e.data);
        if (!Array.isArray(list) || !list.length) return;
        const last = list.slice(-60);
        cpuHist = last.map((s) => s.cpu.total);
        netHist = last.map((s) => s.network.reduce((a, n) => a + n.rx_rate + n.tx_rate, 0));
        snap = last[last.length - 1];
        seq++;
      } catch { /* ignore a broken frame */ }
    });
    es.onmessage = (e) => { try { const s = JSON.parse(e.data); if (!snap || s.time !== snap.time) take(s); } catch { /* ignore */ } };
    es.onerror = async () => {
      const res = await api('/api/auth/state');
      if (res.ok && !res.data.logged_in) { es.close(); onSessionLost(); }
    };
    return () => { clearInterval(tick); es.close(); };
  });
</script>

<svelte:window onkeydown={globalKey} />

<div class="home">
  <!-- Greeting, clock and search -->
  <section class="hero">
    <div class="greet">
      <p class="t-sub">{dateText}</p>
      <h1 class="t-large">{greeting}{username ? `, ${username}` : ''}</h1>
      <p class="t-sub">{system?.hostname ?? ''}{snap ? `  ·  up ${duration(snap.uptime)}` : ''}{apps ? `  ·  ${running} of ${apps.length} apps running` : ''}</p>
    </div>
    <div class="clock num" aria-label="Time">{timeText}</div>
  </section>

  <div class="search-wrap">
    <label class="search" class:open={searchOpen && query}>
      <Search size={18} />
      <input bind:this={searchEl} bind:value={query} onfocus={() => { searchOpen = true; ensureCatalog(); }} onblur={() => setTimeout(() => (searchOpen = false), 150)}
        onkeydown={searchKey} placeholder={narrow ? "Search apps and settings" : "Search apps, settings, or type a folder like /srv"} aria-label="Search" role="combobox"
        aria-expanded={searchOpen && !!query} aria-controls="home-results" spellcheck="false" autocomplete="off" />
      <kbd class="t-caption">/</kbd>
    </label>
    {#if searchOpen && query}
      <ul class="results" id="home-results" role="listbox">
        {#each results as r, i (r.kind + r.label)}
          {@const Icon = r.icon}
          <li role="option" aria-selected={i === active}>
            <button class:on={i === active} onmousedown={(e) => e.preventDefault()} onclick={() => choose(r)} onmouseenter={() => (active = i)}>
              {#if r.app}<AppIcon id={r.app.id} name={r.app.name} size={28} />
              {:else if r.store}<AppIcon id={r.store.id} name={r.store.name} size={28} />
              {:else}<span class="r-ico"><Icon size={16} /></span>{/if}
              <span class="r-text"><span class="truncate">{r.label}</span><span class="t-caption truncate">{r.hint}</span></span>
              <span class="r-kind t-caption">{r.kind}</span>
            </button>
          </li>
        {:else}
          <li class="r-empty t-sub">{catalogLoading ? 'Searching' : 'Nothing found'}</li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="desk">
    <div class="left">
    <!-- Launcher -->
    <section class="launcher tile" aria-label="Apps">
      <div class="l-head">
        <h2 class="t-head">Apps</h2>
        <button class="link" onclick={() => onNavigate('/apps/installed')}>Manage<ChevronRight size={14} /></button>
      </div>
      {#if appsError}
        <div class="notice"><CircleAlert size={18} /><span>{appsError}</span></div>
      {/if}
      <div class="grid">
        {#if !apps}
          {#each Array(6) as _}<div class="app"><span class="skeleton" style:width="64px" style:height="64px" style:border-radius="15px"></span><span class="skeleton" style:width="4rem" style:height="0.75rem"></span></div>{/each}
        {:else}
          {#each apps as a (a.id)}
            <button class="app" onclick={() => launch(a)} oncontextmenu={(e) => appMenu(e, a)}
              onpointerdown={(e) => pressStart(e, a)} onpointerup={pressEnd} onpointerleave={pressEnd} onpointercancel={pressEnd}
              title="{a.name}: click to open, right-click for more">
              <span class="icon-wrap">
                <AppIcon id={a.id} name={a.name} size={64} />
                {#if busy[a.id]}<span class="busy"><Loader size={18} class="spinner" /></span>{/if}
                <span class="dot state {STATE[a.state] ?? ''}" aria-label={a.state}></span>
              </span>
              <span class="app-name">{a.name}</span>
            </button>
          {/each}
          <button class="app add" onclick={() => onNavigate('/apps')}>
            <span class="icon-wrap"><span class="add-tile"><Plus size={26} /></span></span>
            <span class="app-name">Add app</span>
          </button>
        {/if}
      </div>
      {#if apps && !apps.length}
        <p class="t-caption empty-hint">No apps yet. Add one from the App Store: 600+ apps install with one click.</p>
      {/if}
    </section>

    <!-- Shortcuts to the Box tools -->
    <section class="tile shortcuts" aria-label="Shortcuts">
      {#each PLACES.slice(0, 7) as p (p.to)}
        {@const Icon = p.icon}
        <button class="shortcut" onclick={() => onNavigate(p.to)}>
          <span class="s-ico"><Icon size={20} /></span>
          <span class="s-text"><span class="t-head">{p.label}</span><span class="t-caption truncate">{p.hint}</span></span>
        </button>
      {/each}
    </section>
    </div>

    <!-- Widgets -->
    <aside class="widgets">
      <button class="tile widget sys" onclick={() => onNavigate('/monitor')} aria-label="System, open Monitor">
        <div class="w-head"><span class="t-head">System</span><span class="link">Monitor<ChevronRight size={14} /></span></div>
        {#if snap}
          <div class="rings">
            <Ring value={snap.cpu.total} size={74} stroke={7} label="CPU" />
            <Ring value={memPct} size={74} stroke={7} label="Memory" />
            <Ring value={diskPct} size={74} stroke={7} label="Disk" />
          </div>
          <div class="spark"><Sparkline values={cpuHist} {seq} capacity={60} height={34} /></div>
          <p class="t-caption num">Load {snap.load.map((l) => l.toFixed(2)).join('  ')}</p>
        {:else}
          <div class="rings">{#each Array(3) as _}<span class="skeleton" style:width="74px" style:height="74px" style:border-radius="50%"></span>{/each}</div>
        {/if}
      </button>

      <div class="tile widget">
        <div class="w-head"><span class="t-head">Network</span></div>
        {#if snap}
          <div class="net num">
            <span><ArrowDown size={15} />{rate(net.rx)}</span>
            <span><ArrowUp size={15} />{rate(net.tx)}</span>
          </div>
          <div class="spark"><Sparkline values={netHist} {seq} capacity={60} max={0} height={34} /></div>
        {:else}
          <span class="skeleton" style:height="3.5rem"></span>
        {/if}
      </div>

      <button class="tile widget" onclick={() => onNavigate('/files')} aria-label="Storage, open Files">
        <div class="w-head"><span class="t-head">Storage</span><span class="link">Files<ChevronRight size={14} /></span></div>
        {#if snap}
          {#each snap.disks.slice(0, 3) as d (d.mountpoint)}
            {@const p = pct(d.used, d.total)}
            <div class="drive">
              <HardDrive size={15} />
              <span class="drive-main">
                <span class="drive-top"><span class="truncate">{d.mountpoint}</span><span class="t-caption num">{bytes(d.total - d.used)} free</span></span>
                <span class="bar"><span style:transform="scaleX({p / 100})" style:background={p >= 90 ? 'var(--error)' : p >= 75 ? 'var(--warn)' : 'var(--accent)'}></span></span>
              </span>
            </div>
          {/each}
        {:else}
          <span class="skeleton" style:height="3.5rem"></span>
        {/if}
      </button>
    </aside>
  </div>
</div>

<ContextMenu bind:menu onPick={pick} />

<style>
  .home { display: grid; gap: 1.25rem; }
  .hero { display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; }
  .greet { display: grid; gap: 0.25rem; min-width: 0; }
  .greet p, .greet h1 { margin: 0; }
  .clock { font-size: 3rem; line-height: 1; font-weight: 250; letter-spacing: -0.04em; color: var(--text); }

  .search-wrap { position: relative; z-index: 15; }
  .search {
    display: flex; align-items: center; gap: 0.625rem; height: 3.25rem; padding: 0 1rem; border-radius: 1.125rem;
    background: var(--surface); box-shadow: var(--shadow-tile); color: var(--text-3); transition: box-shadow 200ms ease, border-radius 200ms ease;
  }
  .search:focus-within { box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 40%, transparent), var(--shadow-tile); }
  .search input { flex: 1; min-width: 0; border: 0; background: none; font-size: 1rem; color: var(--text); }
  .search input::placeholder { color: var(--text-3); }
  .search input:focus-visible { outline: none; }
  kbd { display: grid; place-items: center; min-width: 1.5rem; height: 1.5rem; padding: 0 0.375rem; border-radius: 0.375rem; background: var(--fill); font-family: var(--mono); }
  .results {
    position: absolute; left: 0; right: 0; top: calc(100% + 0.5rem); margin: 0; padding: 0.375rem; list-style: none;
    max-height: 26rem; overflow-y: auto; border-radius: 1.125rem; background: var(--material); box-shadow: var(--shadow-float);
    -webkit-backdrop-filter: blur(30px) saturate(180%); backdrop-filter: blur(30px) saturate(180%);
    transform-origin: top center; animation: drop 220ms var(--spring);
  }
  @keyframes drop { from { opacity: 0; transform: scale(0.98) translateY(-4px); } }
  .results button { display: flex; align-items: center; gap: 0.75rem; width: 100%; min-height: 3rem; padding: 0.375rem 0.625rem; border: 0; border-radius: 0.75rem; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; }
  .results button.on { background: var(--accent); color: var(--on-accent); }
  .results button.on .t-caption { color: inherit; opacity: 0.85; }
  .r-ico { display: grid; place-items: center; width: 28px; height: 28px; border-radius: 7px; background: var(--fill); color: var(--text-2); flex-shrink: 0; }
  .results button.on .r-ico { background: rgba(255, 255, 255, 0.2); color: inherit; }
  .r-text { flex: 1; min-width: 0; display: grid; }
  .r-kind { flex-shrink: 0; }
  .r-empty { padding: 0.75rem 0.875rem; }

  .desk { display: grid; grid-template-columns: minmax(0, 1fr) 20rem; gap: 1.25rem; align-items: start; }
  .launcher { padding: 1.25rem 1.25rem 1.5rem; display: grid; gap: 1rem; }
  .l-head, .w-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
  .l-head h2 { margin: 0; }
  .link { display: inline-flex; align-items: center; gap: 0.125rem; border: 0; background: none; padding: 0; color: var(--accent); font: inherit; font-size: 0.8125rem; font-weight: 500; cursor: pointer; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(6.5rem, 1fr)); gap: 0.75rem 0.5rem; }
  .app {
    display: grid; justify-items: center; gap: 0.5rem; padding: 0.75rem 0.25rem 0.5rem; border: 0; border-radius: 1rem;
    background: none; color: inherit; font: inherit; cursor: pointer; user-select: none; -webkit-touch-callout: none;
    transition: background-color 150ms ease, transform 120ms ease-out;
  }
  .app:hover { background: var(--fill); }
  .app:active { transform: scale(0.94); }
  .icon-wrap { position: relative; display: grid; place-items: center; }
  .icon-wrap :global(.icon) { box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08), 0 6px 16px rgba(0, 0, 0, 0.08), 0 0 0 1px var(--hairline); }
  .busy { position: absolute; inset: 0; display: grid; place-items: center; border-radius: 15px; background: color-mix(in srgb, var(--surface) 70%, transparent); }
  .dot.state { position: absolute; right: -3px; bottom: -3px; width: 0.875rem; height: 0.875rem; box-shadow: 0 0 0 2.5px var(--surface); }
  .app-name { font-size: 0.8125rem; font-weight: 500; line-height: 1.2; text-align: center; max-width: 100%; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
  .add-tile { display: grid; place-items: center; width: 64px; height: 64px; border-radius: 15px; background: var(--fill); color: var(--text-2); transition: background-color 150ms ease; }
  .add:hover .add-tile { background: var(--accent-fill); color: var(--accent); }
  .empty-hint { margin: 0; }

  .left { display: grid; gap: 1.25rem; min-width: 0; }
  .shortcuts { display: grid; grid-template-columns: repeat(auto-fill, minmax(13rem, 1fr)); gap: 0.25rem; padding: 0.5rem; }
  .shortcut { display: flex; align-items: center; gap: 0.75rem; min-height: 3.5rem; padding: 0.5rem 0.75rem; border: 0; border-radius: 0.875rem; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; transition: background-color 150ms ease, transform 120ms ease-out; }
  .shortcut:hover { background: var(--fill); }
  .shortcut:active { transform: scale(0.98); }
  .s-ico { display: grid; place-items: center; width: 2.5rem; height: 2.5rem; border-radius: 0.75rem; background: var(--accent-fill); color: var(--accent); flex-shrink: 0; }
  .s-text { display: grid; min-width: 0; }
  .widgets { display: grid; gap: 1rem; }
  .widget { display: grid; gap: 0.75rem; padding: 1rem 1.125rem; border: 0; color: inherit; font: inherit; text-align: left; }
  button.widget { cursor: pointer; transition: transform 120ms ease-out; }
  button.widget:active { transform: scale(0.98); }
  .rings { display: flex; justify-content: space-between; gap: 0.5rem; }
  .rings :global(.ring) { margin: 0; }
  .spark { margin: 0 -0.25rem; }
  .widget p { margin: 0; }
  .net { display: flex; gap: 1.25rem; font-weight: 600; }
  .net span { display: inline-flex; align-items: center; gap: 0.375rem; }
  .drive { display: flex; align-items: center; gap: 0.625rem; color: var(--text-2); }
  .drive-main { flex: 1; min-width: 0; display: grid; gap: 0.375rem; color: var(--text); font-size: 0.8125rem; }
  .drive-top { display: flex; justify-content: space-between; gap: 0.5rem; }

  @media (max-width: 1099px) {
    .desk { grid-template-columns: 1fr; }
    .widgets { grid-template-columns: repeat(auto-fit, minmax(15rem, 1fr)); }
  }
  @media (max-width: 639px) {
    .clock { font-size: 2.25rem; }
    .hero { align-items: flex-start; flex-direction: column-reverse; gap: 0.5rem; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(5.25rem, 1fr)); }
    kbd { display: none; }
    .shortcuts { display: none; }
  }
</style>
