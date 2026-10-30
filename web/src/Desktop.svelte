<script>
  // The desktop: wallpaper, frosted widgets on the left, search and the app grid on the right.
  // Tiles are the installed apps (once, in the Apps group) and the Box tools in the System dock
  // (Apps window with Installed and App Store, Files, Monitor, Terminal, Backups, Settings).
  // Click opens, right-click or long-press shows actions, dragging reorders (saved on the server).
  import { onMount, tick } from 'svelte';
  import { flip } from 'svelte/animate';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { theme } from '$lib/theme.svelte.js';
  import { runJob, appURL, jobFollower } from '$lib/jobs.js';
  import { desktop, saveDesktop, wallpaperOf, measureTone, setOrigin, WIDGETS } from '$lib/desktop.svelte.js';
  import AppIcon from '$lib/AppIcon.svelte';
  import ContextMenu from '$lib/ContextMenu.svelte';
  import Dialog from '$lib/Dialog.svelte';
  import Widgets from './desktop/Widgets.svelte';
  import Search from '@lucide/svelte/icons/search';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import Play from '@lucide/svelte/icons/play';
  import Square from '@lucide/svelte/icons/square';
  import RotateCw from '@lucide/svelte/icons/rotate-cw';
  import ScrollText from '@lucide/svelte/icons/scroll-text';
  import KeyRound from '@lucide/svelte/icons/key-round';
  import Archive from '@lucide/svelte/icons/archive';
  import Settings2 from '@lucide/svelte/icons/settings-2';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import EyeOff from '@lucide/svelte/icons/eye-off';
  import ArrowLeft from '@lucide/svelte/icons/arrow-left';
  import ArrowRight from '@lucide/svelte/icons/arrow-right';
  import ChevronUp from '@lucide/svelte/icons/chevron-up';
  import ChevronDown from '@lucide/svelte/icons/chevron-down';
  import LayoutGrid from '@lucide/svelte/icons/layout-grid';
  import FolderOpen from '@lucide/svelte/icons/folder-open';
  import Gauge from '@lucide/svelte/icons/gauge';
  import SquareTerminal from '@lucide/svelte/icons/square-terminal';
  import Boxes from '@lucide/svelte/icons/boxes';
  import Settings from '@lucide/svelte/icons/settings';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import Plus from '@lucide/svelte/icons/plus';

  let { system, username = '', onSessionLost, onNavigate, inert = false } = $props();

  const SYSTEM = [
    { id: 'sys-apps', name: 'Apps', icon: LayoutGrid, to: '/apps/installed', hint: 'Installed apps and the App Store' },
    { id: 'sys-files', name: 'Files', icon: FolderOpen, to: '/files', hint: 'Browse and upload files' },
    { id: 'sys-monitor', name: 'Monitor', icon: Gauge, to: '/monitor', hint: 'Live charts for CPU, memory and network' },
    { id: 'sys-terminal', name: 'Terminal', icon: SquareTerminal, to: '/console', hint: 'Root shell in the browser' },
    { id: 'sys-backups', name: 'Backups', icon: Archive, to: '/backups', hint: 'Back up and restore apps' },
    { id: 'sys-settings', name: 'Settings', icon: Settings, to: '/settings', hint: 'Everything about this Box' },
  ];
  const PLACES = [
    { label: 'Apps: Installed', hint: 'Start, stop, update and logs', to: '/apps/installed', icon: Boxes },
    { label: 'Apps: App Store', hint: 'Install new apps', to: '/apps', icon: LayoutGrid },
    { label: 'Settings: General', hint: 'Wallpaper, appearance and folders in Files', to: '/settings/general', icon: Settings },
    { label: 'Settings: Account and security', hint: 'Password, two-step sign-in, browsers', to: '/settings/security', icon: Settings },
    { label: 'Settings: Apps and catalog', hint: 'Catalog address, image cleanup', to: '/settings/apps', icon: Settings },
    { label: 'Settings: Network and domains', hint: 'Reverse proxy, HTTPS and domains', to: '/settings/network', icon: Settings },
    { label: 'Settings: Firewall', hint: 'Open or block ports', to: '/settings/firewall', icon: Settings },
    { label: 'Settings: Shares', hint: 'Samba, NFS and share links', to: '/settings/shares', icon: Settings },
    { label: 'Settings: Disks', hint: 'Disk health and SMART', to: '/settings/disks', icon: Settings },
    { label: 'Settings: System updates', hint: 'Debian and Ubuntu packages', to: '/settings/system-updates', icon: Settings },
    { label: 'Settings: Box updates', hint: 'Update NexoVirt Box', to: '/settings/box-updates', icon: Settings },
    { label: 'Settings: Notifications', hint: 'Email, ntfy, webhooks', to: '/settings/notifications', icon: Settings },
    { label: 'Settings: Activity', hint: 'Sign-ins and changes', to: '/settings/activity', icon: Settings },
    { label: 'Settings: About', hint: 'Version and licenses', to: '/settings/about', icon: Settings },
  ];
  const STATE = { running: 'ok', partial: 'warn', restarting: 'warn', stopped: '', exited: 'error', interrupted: 'warn' };
  const STATE_TEXT = { running: 'Running', partial: 'Partly running', restarting: 'Restarting', stopped: 'Stopped', exited: 'Stopped with an error', interrupted: 'Install interrupted' };

  let apps = $state(null), lastApps = '';
  let appsError = $state('');
  let proxy = $state(null);
  let busy = $state({});
  let bg = $state({});
  const follow = jobFollower(() => loadApps(), () => busy);
  let query = $state(''), searchOpen = $state(false), active = $state(0);
  let catalog = null, catalogLoading = $state(false), catalogApps = $state([]);
  let menu = $state(null), menuTile = null;
  let searchEl = $state();
  let editOpen = $state(false);
  let narrow = $state(matchMedia('(max-width: 639px)').matches);

  const prefs = $derived(desktop.prefs);
  const wall = $derived(wallpaperOf(prefs, theme.current === 'dark'));
  const onDark = $derived(wall.tone === 'dark' || (prefs?.dim ?? 0) >= 45);
  $effect(() => { if (wall.id === 'custom') measureTone(wall.image); });

  // Two groups in the saved order: Box's own tools (System) and the installed apps. New ones go last,
  // except the Apps window tile, which leads the dock until it is moved.
  const pos = $derived(new Map((prefs?.order || []).map((id, i) => [id, i])));
  const sorted = (list) => list.map((t, i) => ({ t, k: pos.has(t.id) ? pos.get(t.id) : t.id === 'sys-apps' ? -1 : 10000 + i })).sort((a, b) => a.k - b.k).map((x) => x.t);
  const sysTiles = $derived.by(() => {
    const hidden = new Set(prefs?.hidden || []);
    return sorted(SYSTEM.filter((s) => !hidden.has(s.id)).map((s) => ({ ...s, sys: true, group: 'sys' })));
  });
  const appTiles = $derived(sorted((apps || []).map((a) => ({ id: a.id, name: a.name, app: a, group: 'apps' }))));
  const tiles = $derived([...sysTiles, ...appTiles]);
  const groupOf = (g) => (g === 'sys' ? sysTiles : appTiles);
  let drag = $state(null); // { id, group, order, x, y, dx, dy, w, h }
  const shownOf = (g) => (drag?.group === g ? drag.order.map((id) => groupOf(g).find((t) => t.id === id)).filter(Boolean) : groupOf(g));
  const shownSys = $derived(shownOf('sys'));
  const shownApps = $derived(shownOf('apps'));
  function saveOrder(group, order) {
    saveDesktop({ order: group === 'sys' ? [...order, ...appTiles.map((t) => t.id)] : [...sysTiles.map((t) => t.id), ...order] });
  }

  async function loadApps() {
    const [a, p, j] = await Promise.all([api('/api/apps'), api('/api/proxy'), follow()]);
    if (a.status === 401) return onSessionLost();
    if (!a.ok) { appsError = a.error; apps = apps || []; return; }
    appsError = '';
    const next = JSON.stringify(a.data);
    if (next !== lastApps) { lastApps = next; apps = a.data; } // unchanged polls do not touch the grid
    bg = Object.fromEntries(a.data.filter((x) => x.busy).map((x) => [x.id, x.busy]).concat(Object.entries(j)));
    if (p.ok) proxy = p.data;
  }

  function openURL(app) {
    if (app.domain && proxy?.enabled && proxy.running) {
      const port = proxy.config.https_port;
      return `https://${app.domain}${port === 443 ? '' : ':' + port}`;
    }
    return appURL(app);
  }

  function go(to, el) { setOrigin(el); onNavigate(to); }

  function launch(t, el) {
    if (t.sys) return go(t.to, el);
    const app = t.app;
    const work = busy[app.id] || bg[app.id];
    if (work === 'install' || work === 'retry') return go(`/apps?app=${app.id}`, el);
    const url = openURL(app);
    if (!url) return go(`/apps/installed?app=${app.id}`, el);
    if (app.state !== 'running') toast(`${app.name} is not running. Start it first.`, 'info');
    window.open(url, '_blank', 'noopener,noreferrer');
  }

  // ---- context menu ----
  function tileMenu(x, y, t, el) {
    menuTile = { t, el };
    const list = groupOf(t.group);
    const i = list.findIndex((x) => x.id === t.id);
    const move = [
      { key: 'left', label: 'Move earlier', icon: ArrowLeft, disabled: i <= 0 },
      { key: 'right', label: 'Move later', icon: ArrowRight, disabled: i >= list.length - 1 },
    ];
    if (t.sys) {
      menu = { x, y, items: [{ key: 'open', label: `Open ${t.name}`, icon: ExternalLink }, 'sep', ...move, 'sep', { key: 'hide', label: 'Hide from desktop', icon: EyeOff }] };
      return;
    }
    const app = t.app, up = app.state === 'running', working = !!(busy[app.id] || bg[app.id]);
    menu = {
      x, y,
      items: [
        { key: 'open', label: `Open ${app.name}`, icon: ExternalLink, disabled: !openURL(app) },
        'sep',
        { key: 'start', label: 'Start', icon: Play, disabled: up || working },
        { key: 'stop', label: 'Stop', icon: Square, disabled: !up || working },
        { key: 'restart', label: 'Restart', icon: RotateCw, disabled: !up || working },
        'sep',
        ...(app.custom ? [] : [{ key: 'login', label: 'Login details', icon: KeyRound }]),
        { key: 'logs', label: 'Logs', icon: ScrollText },
        { key: 'backups', label: 'Backups', icon: Archive },
        { key: 'settings', label: 'Settings', icon: Settings2 },
        'sep', ...move, 'sep',
        { key: 'uninstall', label: 'Uninstall', icon: Trash2, danger: true, disabled: working },
      ],
    };
  }

  async function pick(key) {
    const { t, el } = menuTile || {};
    if (!t) return;
    if (key === 'open') return t.sys ? go(t.to, el) : window.open(openURL(t.app), '_blank', 'noopener,noreferrer');
    if (key === 'left' || key === 'right') return moveTile(t, key === 'left' ? -1 : 1);
    if (key === 'hide') return saveDesktop({ hidden: [...(prefs.hidden || []), t.id] });
    const app = t.app;
    if (key === 'logs') return go(`/apps/${app.id}/logs`, el);
    if (key === 'login') return go(`/apps/installed?app=${app.id}&do=login`, el);
    if (key === 'backups') return go(`/apps/installed?app=${app.id}&do=backups`, el);
    if (key === 'settings') return go(`/apps/installed?app=${app.id}`, el);
    if (key === 'uninstall') return go(`/apps/installed?app=${app.id}&do=uninstall`, el);
    busy = { ...busy, [app.id]: key };
    const job = await runJob(`/api/apps/${app.id}/action`, { op: key });
    const { [app.id]: _, ...rest } = busy;
    busy = rest;
    if (job.http === 401) return onSessionLost();
    if (job.status === 'done') toast(`${app.name} ${key === 'start' ? 'started' : key === 'stop' ? 'stopped' : 'restarted'}.`, 'success');
    else if (job.error?.startsWith('needs_docker_socket:')) { toast('Allow Docker access for this app in Apps first.', 'info', 8000); go('/apps/installed'); }
    else toast(job.error, 'error', 8000);
    loadApps();
  }

  function moveTile(t, by) {
    const order = groupOf(t.group).map((x) => x.id);
    const i = order.indexOf(t.id), j = i + by;
    if (i < 0 || j < 0 || j >= order.length) return;
    [order[i], order[j]] = [order[j], order[i]];
    saveOrder(t.group, order);
  }

  // ---- drag to reorder ----
  // Mouse: drag after a few pixels. Touch: a long press opens the menu; moving the finger then
  // lifts the tile out of the menu and drags it (scrolling is held while it is lifted).
  let press = null, suppressClick = false;
  function down(e, t) {
    if (e.pointerType === 'mouse' && e.button !== 0) return;
    const el = e.currentTarget;
    press = { t, el, x0: e.clientX, y0: e.clientY, type: e.pointerType, armed: e.pointerType !== 'touch', timer: 0 };
    if (e.pointerType === 'touch') {
      press.timer = setTimeout(() => {
        if (!press) return;
        press.armed = true;
        navigator.vibrate?.(8);
        tileMenu(press.x0, press.y0, t, el);
      }, 450);
    }
  }
  function move(e) {
    if (!press) return;
    const dist = Math.hypot(e.clientX - press.x0, e.clientY - press.y0);
    if (drag) {
      drag.x = e.clientX; drag.y = e.clientY;
      reorderAt(e.clientX, e.clientY);
      return;
    }
    if (!press.armed) { if (dist > 10) { clearTimeout(press.timer); press = null; } return; }
    if (dist < 7) return;
    menu = null;
    const r = press.el.getBoundingClientRect();
    drag = { id: press.t.id, group: press.t.group, order: groupOf(press.t.group).map((t) => t.id), x: e.clientX, y: e.clientY, dx: press.x0 - r.left, dy: press.y0 - r.top, w: r.width, h: r.height };
  }
  function reorderAt(x, y) {
    const hit = document.elementsFromPoint(x, y).find((n) => n.dataset?.tile && n.dataset.tile !== drag.id && n.dataset.group === drag.group);
    if (!hit) return;
    const order = drag.order.filter((id) => id !== drag.id);
    const to = order.indexOf(hit.dataset.tile);
    const from = drag.order.indexOf(drag.id), over = drag.order.indexOf(hit.dataset.tile);
    order.splice(over > from ? to + 1 : to, 0, drag.id);
    if (order.join() !== drag.order.join()) drag.order = order;
  }
  function up() {
    if (press) clearTimeout(press.timer);
    if (drag) {
      const { order, group } = drag;
      drag = null;
      suppressClick = true;
      setTimeout(() => (suppressClick = false), 50);
      if (order.join() !== groupOf(group).map((t) => t.id).join()) saveOrder(group, order);
    }
    press = null;
  }
  function holdScroll(e) { if (drag || (press?.armed && press.type === 'touch')) e.preventDefault(); }
  function click(e, t) {
    if (suppressClick || menu) return;
    launch(t, e.currentTarget);
  }
  function context(e, t) {
    e.preventDefault();
    if (press?.type === 'touch') return; // the long-press timer handles touch
    tileMenu(e.clientX, e.clientY, t, e.currentTarget);
  }
  function tileKey(e, t) {
    if (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')) {
      e.preventDefault();
      const r = e.currentTarget.getBoundingClientRect();
      tileMenu(r.left + r.width / 2, r.top + r.height / 2, t, e.currentTarget);
    }
  }

  // ---- search ----
  async function ensureCatalog() {
    if (catalog || catalogLoading) return;
    catalogLoading = true;
    const res = await api('/api/catalog');
    catalogLoading = false;
    if (res.ok) { catalog = res.data.apps; catalogApps = catalog; }
  }
  // The list follows the typing with a short pause, so fast typing does not filter 600 apps per key.
  let q = $state('');
  $effect(() => { const v = query.trim().toLowerCase(); const t = setTimeout(() => (q = v), v ? 110 : 0); return () => clearTimeout(t); });
  const results = $derived.by(() => {
    if (!q) return [];
    const out = [];
    if (q.startsWith('/')) out.push({ kind: 'Folder', label: query.trim(), hint: 'Open in Files', icon: FolderOpen, to: '/files?path=' + encodeURIComponent(query.trim().replace(/\/+$/, '') || '/') });
    for (const t of tiles) if (t.name.toLowerCase().includes(q)) out.push({ kind: t.sys ? 'Box' : 'App', label: t.name, hint: t.sys ? t.hint : STATE_TEXT[t.app.state] || 'Not running', tile: t });
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
    if (r.dtile) return launch(r.dtile, document.querySelector(`[data-tile="${r.dtile.id}"]`));
    go(r.to, searchEl);
  }
  function searchKey(e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); active = Math.min(results.length - 1, active + 1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); active = Math.max(0, active - 1); }
    else if (e.key === 'Enter' && results[active]) { e.preventDefault(); choose(results[active]); }
    else if (e.key === 'Escape') { e.preventDefault(); query ? (query = '') : searchEl?.blur(); }
  }
  function globalKey(e) {
    if (inert || e.target.closest?.('input, textarea, [contenteditable]')) return;
    if (e.key === '/' || ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k')) { e.preventDefault(); searchEl?.focus(); }
  }

  // ---- widget editor ----
  function moveWidget(i, by) {
    const list = [...prefs.widgets];
    [list[i], list[i + by]] = [list[i + by], list[i]];
    saveDesktop({ widgets: list });
  }
  function toggleWidget(i, on) { saveDesktop({ widgets: prefs.widgets.map((w, j) => (j === i ? { ...w, on } : w)) }); }
  const hiddenSys = $derived(SYSTEM.filter((s) => (prefs?.hidden || []).includes(s.id)));

  onMount(() => {
    loadApps();
    // Poll only while the desktop is visible (not behind a full window, not in a background tab).
    const t = setInterval(() => { if (!inert && !document.hidden) loadApps(); }, 15000);
    addEventListener('touchmove', holdScroll, { passive: false });
    return () => { clearInterval(t); removeEventListener('touchmove', holdScroll); };
  });
</script>

<svelte:window onkeydown={globalKey} onpointermove={move} onpointerup={up} onpointercancel={up} />

<div class="wall" aria-hidden="true" style:background-color={wall.color}>
  {#if wall.image}<div class="wall-img" style:background-image="url('{wall.image}')" style:filter={prefs?.blur ? `blur(${prefs.blur}px)` : null} style:inset={prefs?.blur ? `-${prefs.blur * 2}px` : null}></div>{/if}
  {#if prefs?.dim}<div class="wall-dim" style:opacity={prefs.dim / 100}></div>{/if}
</div>

<div class="desk" class:on-dark={onDark} {inert}>
  <aside class="left">
    {#if prefs}
      <Widgets {system} {username} widgets={prefs.widgets} clock={prefs.clock} paused={inert} {onNavigate} {onSessionLost} onEdit={() => (editOpen = true)} />
    {:else}
      {#each Array(3) as _}<div class="glass skel-widget"><span class="skeleton"></span></div>{/each}
    {/if}
  </aside>

  <section class="right">
    <div class="search-wrap">
      <label class="search glass" class:open={searchOpen && query}>
        <Search size={18} />
        <input bind:this={searchEl} bind:value={query} onfocus={() => { searchOpen = true; ensureCatalog(); }} onblur={() => setTimeout(() => (searchOpen = false), 150)}
          onkeydown={searchKey} placeholder={narrow ? 'Search apps, files, settings' : 'Search apps, files, settings and the App Store'} aria-label="Search" role="combobox"
          aria-expanded={searchOpen && !!query} aria-controls="desk-results" spellcheck="false" autocomplete="off" />
        <kbd class="t-caption">/</kbd>
      </label>
      {#if searchOpen && query}
        <ul class="results" id="desk-results" role="listbox">
          {#each results as r, i (r.kind + r.label)}
            {@const Icon = r.icon || r.dtile?.icon}
            <li role="option" aria-selected={i === active}>
              <button class:on={i === active} onmousedown={(e) => e.preventDefault()} onclick={() => choose(r)} onmouseenter={() => (active = i)}>
                {#if r.dtile?.app}<AppIcon id={r.dtile.app.id} name={r.dtile.app.name} size={28} icon={r.dtile.app.icon} />
                {:else if r.store}<AppIcon id={r.store.id} name={r.store.name} size={28} />
                {:else}<span class="ico r-ico"><Icon /></span>{/if}
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

    <div class="sec-head">
      <h2 class="sec-title">System</h2>
    </div>
    <div class="dock glass" role="list" aria-label="System">
      {#if !prefs}
        {#each Array(6) as _}<div class="ditem"><span class="skeleton" style:width="28px" style:height="28px" style:border-radius="8px"></span></div>{/each}
      {:else}
        {#each shownSys as t (t.id)}
          {@const Icon = t.icon}
          <div role="listitem" class="dcell" animate:flip={{ duration: 220 }}>
            <button class="ditem" class:lifted={drag?.id === t.id} data-tile={t.id} data-group="sys"
              onclick={(e) => click(e, t)} oncontextmenu={(e) => context(e, t)} onkeydown={(e) => tileKey(e, t)} onpointerdown={(e) => down(e, t)}
              title="{t.name}: {t.hint}">
              <span class="ico md accent"><Icon /></span>
              <span class="dname">{t.name}</span>
            </button>
          </div>
        {/each}
      {/if}
    </div>

    <div class="sec-head apps-head">
      <h2 class="sec-title">Apps</h2>
    </div>
    {#if appsError}<div class="notice"><CircleAlert size={18} /><span>{appsError}</span></div>{/if}

    <div class="grid" role="list" aria-label="Apps">
      {#if !apps || !prefs}
        {#each Array(6) as _}<div class="dtile glass skel"><span class="skeleton" style:width="52px" style:height="52px" style:border-radius="14px"></span><span class="skeleton" style:width="4rem" style:height="0.75rem"></span></div>{/each}
      {:else}
        {#each shownApps as t (t.id)}
          {@const work = busy[t.id] || bg[t.id]}
          <div role="listitem" class="cell" animate:flip={{ duration: 260 }}>
            <button class="dtile glass" class:lifted={drag?.id === t.id} data-tile={t.id} data-group="apps"
              onclick={(e) => click(e, t)} oncontextmenu={(e) => context(e, t)} onkeydown={(e) => tileKey(e, t)}
              onpointerdown={(e) => down(e, t)}
              title="{t.name}: click to open, right-click for more" aria-label="{t.name}, {STATE_TEXT[t.app.state] || 'not running'}">
              <span class="icon-wrap">
                <AppIcon id={t.app.id} name={t.app.name} size={52} icon={t.app.icon} />
                {#if work}<span class="busy"><Loader size={18} class="spinner" /></span>{/if}
              </span>
              <span class="name">{t.name}</span>
              <span class="dot state {STATE[t.app.state] ?? ''}" aria-hidden="true"></span>
            </button>
          </div>
        {/each}
        <!-- Always last: no data-tile, so it is never a drag target and never dragged. -->
        <div role="listitem" class="cell">
          <button class="dtile glass add" onclick={(e) => go('/apps', e.currentTarget)} title="Open the App Store">
            <span class="icon-wrap add-ico"><Plus size={34} strokeWidth={1.75} /></span>
            <span class="name">Add app</span>
            {#if !apps.length}<span class="add-hint t-caption">Install your first app</span>{/if}
          </button>
        </div>
      {/if}
    </div>
  </section>
</div>

{#if drag}
  {@const t = tiles.find((x) => x.id === drag.id)}
  {#if t}
    {@const Icon = t.icon}
    <div class="ghost" class:sys={t.sys} style:width="{drag.w}px" style:height="{drag.h}px" style:transform="translate({drag.x - drag.dx}px, {drag.y - drag.dy}px) scale(1.06)" aria-hidden="true">
      {#if t.sys}<span class="ico md accent"><Icon /></span><span class="dname">{t.name}</span>
      {:else}<span class="icon-wrap"><AppIcon id={t.app.id} name={t.app.name} size={52} icon={t.app.icon} /></span><span class="name">{t.name}</span>{/if}
    </div>
  {/if}
{/if}

<ContextMenu bind:menu onPick={pick} />

<Dialog bind:open={editOpen} label="Edit widgets">
  <div class="dlg">
    <div class="dlg-head"><h2 class="t-title">Edit widgets</h2></div>
    <p class="t-sub">Pick the widgets on the left of the desktop and their order. Saved for your account.</p>
    {#if prefs}
      <div class="group">
        {#each prefs.widgets as w, i (w.id)}
          <div class="row" animate:flip={{ duration: 220 }}>
            <input type="checkbox" class="switch" checked={w.on} onchange={(e) => toggleWidget(i, e.currentTarget.checked)} aria-label="Show {WIDGETS[w.id]}" />
            <span class="grow">{WIDGETS[w.id]}</span>
            <button class="icon-btn sm" onclick={() => moveWidget(i, -1)} disabled={i === 0} aria-label="Move {WIDGETS[w.id]} up"><ChevronUp size={18} /></button>
            <button class="icon-btn sm" onclick={() => moveWidget(i, 1)} disabled={i === prefs.widgets.length - 1} aria-label="Move {WIDGETS[w.id]} down"><ChevronDown size={18} /></button>
          </div>
        {/each}
      </div>
      {#if hiddenSys.length}
        <p class="t-section">Hidden from the desktop</p>
        <div class="group">
          {#each hiddenSys as s (s.id)}
            {@const Icon = s.icon}
            <div class="row">
              <span class="ico accent"><Icon /></span>
              <span class="grow">{s.name}</span>
              <button class="btn btn-gray btn-sm" onclick={() => saveDesktop({ hidden: prefs.hidden.filter((h) => h !== s.id) })}>Show</button>
            </div>
          {/each}
        </div>
      {/if}
      <p class="t-caption">Rearrange apps by dragging them on the desktop, or use Move earlier and Move later in their menu. The wallpaper is in Settings, General.</p>
    {/if}
    <div class="buttons"><button class="btn btn-primary" onclick={() => (editOpen = false)}>Done</button></div>
  </div>
</Dialog>

<style>
  .wall { position: fixed; inset: 0; z-index: 0; overflow: hidden; transition: background-color 400ms ease; }
  .wall-img { position: absolute; inset: 0; background-size: cover; background-position: center; }
  .wall-dim { position: absolute; inset: 0; background: #000; }

  .desk {
    position: relative; z-index: 1; display: grid; grid-template-columns: 21rem minmax(0, 1fr); gap: 1.75rem; align-items: start;
    min-height: 100svh;
    padding: calc(4.25rem + env(safe-area-inset-top, 0px)) max(1.75rem, env(safe-area-inset-right, 0px)) calc(2rem + env(safe-area-inset-bottom, 0px)) max(1.75rem, env(safe-area-inset-left, 0px));
    max-width: 110rem; margin-inline: auto;
  }
  .left { min-width: 0; }
  .skel-widget { height: 8rem; border-radius: 1.375rem; margin-bottom: 0.875rem; padding: 1rem; display: grid; }
  .right { display: grid; gap: 1rem; min-width: 0; align-content: start; }

  /* Text that sits right on the wallpaper. */
  .sec-title, .hint { color: #27272a; }
  .on-dark .sec-title, .on-dark .hint { color: #f4f4f5; text-shadow: 0 1px 10px rgba(0, 0, 0, 0.4); }

  .search-wrap { position: relative; z-index: 15; }
  .search {
    display: flex; align-items: center; gap: 0.625rem; height: 3.25rem; padding: 0 1.125rem; border-radius: 999px;
    color: var(--text-2); transition: box-shadow 200ms ease;
  }
  .search:focus-within { box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 45%, transparent), var(--glass-edge); }
  .search input { flex: 1; min-width: 0; border: 0; background: none; font-size: 1rem; color: var(--text); }
  .search input::placeholder { color: var(--text-2); }
  .search input:focus-visible { outline: none; }
  kbd { display: grid; place-items: center; min-width: 1.5rem; height: 1.5rem; padding: 0 0.375rem; border-radius: 0.375rem; background: var(--fill); font-family: var(--mono); }
  .results {
    position: absolute; left: 0; right: 0; top: calc(100% + 0.5rem); margin: 0; padding: 0.375rem; list-style: none;
    max-height: 26rem; overflow-y: auto; border-radius: 1.25rem; background: var(--material); box-shadow: var(--shadow-float);
    transform-origin: top center; animation: drop 220ms var(--spring);
  }
  @keyframes drop { from { opacity: 0; transform: scale(0.98) translateY(-4px); filter: blur(3px); } }
  .results button { display: flex; align-items: center; gap: 0.75rem; width: 100%; min-height: 3rem; padding: 0.375rem 0.625rem; border: 0; border-radius: 0.875rem; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; }
  .results button.on { background: var(--accent); color: var(--on-accent); }
  .results button.on .t-caption, .results button.on .r-ico { color: inherit; }
  .r-ico { margin: 0 4px; }
  .r-text { flex: 1; min-width: 0; display: grid; }
  .r-kind { flex-shrink: 0; }
  .r-empty { padding: 0.75rem 0.875rem; }

  .sec-head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; min-height: 2.5rem; margin: 0.5rem 0 -0.25rem 0.25rem; }
  .apps-head { margin-top: 1rem; }
  .sec-title { margin: 0; font-size: 0.75rem; line-height: 1.3; letter-spacing: 0.06em; text-transform: uppercase; font-weight: 650; }
  .dock { display: flex; flex-wrap: wrap; gap: 0.25rem; padding: 0.5rem; border-radius: 1.375rem; }
  .dcell { min-width: 0; }
  .ditem {
    display: grid; justify-items: center; gap: 0.375rem; width: 5.75rem; padding: 0.75rem 0.25rem 0.625rem; border: 0; border-radius: 1rem;
    background: none; color: var(--text); font: inherit; cursor: pointer; user-select: none; -webkit-user-select: none; -webkit-touch-callout: none;
    touch-action: manipulation; transition: background-color 150ms ease, transform 120ms ease-out, opacity 150ms ease;
  }
  .ditem:hover { background: var(--fill); }
  .ditem:active { transform: scale(0.94); }
  .ditem.lifted { opacity: 0.25; }
  .dname { font-size: 0.75rem; font-weight: 550; line-height: 1.2; text-align: center; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 100%; }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(7.5rem, 1fr)); gap: 0.875rem; }
  .cell { min-width: 0; }
  .dtile {
    position: relative; display: grid; justify-items: center; align-content: center; gap: 0.625rem; width: 100%; aspect-ratio: 1 / 1;
    padding: 0.75rem 0.5rem; border: 0; border-radius: 1.375rem; color: var(--text); font: inherit; cursor: pointer;
    user-select: none; -webkit-user-select: none; -webkit-touch-callout: none; touch-action: manipulation;
    transition: transform 140ms ease-out, opacity 150ms ease;
  }
  .dtile:hover { background: var(--glass-strong); }
  .dtile:active { transform: scale(0.95); }
  .dtile.lifted { opacity: 0.25; }
  .dtile.skel { cursor: default; }
  .icon-wrap { position: relative; display: grid; place-items: center; width: 52px; height: 52px; }
  .busy { position: absolute; inset: -4px; display: grid; place-items: center; border-radius: 14px; background: color-mix(in srgb, var(--glass-strong) 85%, transparent); }
  .name { font-size: 0.8125rem; font-weight: 550; line-height: 1.2; text-align: center; max-width: 100%; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; }
  .dot.state { position: absolute; top: 0.75rem; right: 0.75rem; }
  .add-ico { color: var(--text-2); }
  .add:hover .add-ico { color: var(--accent); }
  .add .add-hint { margin-top: -0.375rem; text-align: center; }

  .ghost.sys { gap: 0.375rem; border-radius: 1rem; }
  .ghost {
    position: fixed; left: 0; top: 0; z-index: 70; display: grid; justify-items: center; align-content: center; gap: 0.625rem;
    padding: 0.75rem 0.5rem; border-radius: 1.375rem; pointer-events: none; color: var(--text); will-change: transform;
    background: var(--glass-strong); box-shadow: var(--glass-edge), 0 24px 50px rgba(0, 0, 0, 0.25);
  }
  .icon-btn.sm { width: 2.25rem; height: 2.25rem; }
  .icon-btn:disabled { opacity: 0.35; cursor: default; }

  @media (max-width: 999px) {
    .desk { grid-template-columns: 1fr; gap: 1.25rem; padding-top: calc(4rem + env(safe-area-inset-top, 0px)); }
    .left { order: 0; }
    .left :global(.widgets) { grid-template-columns: repeat(auto-fill, minmax(17rem, 1fr)); }
    .left :global(.widgets .edit) { grid-column: 1 / -1; }
  }
  @media (max-width: 639px) {
    .desk { padding-inline: max(1rem, env(safe-area-inset-left, 0px)); }
    .left :global(.widgets) { grid-template-columns: 1fr; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(6.25rem, 1fr)); gap: 0.625rem; }
    .ditem { width: 4.75rem; }
    .dtile { aspect-ratio: auto; padding: 0.875rem 0.375rem 0.75rem; gap: 0.5rem; }
    kbd { display: none; }
    .search input { font-size: 0.9375rem; }
  }
</style>
