<script>
  // Left column of the desktop: frosted widgets in the order the user picked (clock, system status,
  // storage, network, disk health). Live numbers come from the stats stream.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { bytes, rate, pct, duration } from '$lib/format.js';
  import { setOrigin } from '$lib/desktop.svelte.js';
  import Ring from '$lib/Ring.svelte';
  import ClockWidget from './ClockWidget.svelte';
  import Sparkline from '$lib/Sparkline.svelte';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import ArrowDown from '@lucide/svelte/icons/arrow-down';
  import ArrowUp from '@lucide/svelte/icons/arrow-up';
  import Thermometer from '@lucide/svelte/icons/thermometer';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import SlidersHorizontal from '@lucide/svelte/icons/sliders-horizontal';
  import GripVertical from '@lucide/svelte/icons/grip-vertical';
  import Sun from '@lucide/svelte/icons/sun';
  import MoonIcon from '@lucide/svelte/icons/moon';
  import Cloud from '@lucide/svelte/icons/cloud';
  import CloudSun from '@lucide/svelte/icons/cloud-sun';
  import CloudFog from '@lucide/svelte/icons/cloud-fog';
  import CloudDrizzle from '@lucide/svelte/icons/cloud-drizzle';
  import CloudRain from '@lucide/svelte/icons/cloud-rain';
  import Snowflake from '@lucide/svelte/icons/snowflake';
  import CloudLightning from '@lucide/svelte/icons/cloud-lightning';

  let { system, username = '', widgets = [], clock, apps = null, weatherPlace = '', paused = false, free = true, onNavigate, onEdit, onSessionLost, onMove } = $props();

  // ---- free placement: drag a widget by its grip; the spot is saved per user (desktop.go) ----
  let dragging = $state(null); // { id, x, y, dx, dy }
  function grab(e, id) {
    const box = e.currentTarget.closest('.wbox').getBoundingClientRect();
    e.currentTarget.setPointerCapture(e.pointerId);
    dragging = { id, x: box.left, y: box.top, dx: e.clientX - box.left, dy: e.clientY - box.top };
  }
  function drag(e) {
    if (!dragging) return;
    dragging = { ...dragging, x: e.clientX - dragging.dx, y: e.clientY - dragging.dy };
  }
  function drop() {
    if (!dragging) return;
    const { id, x, y } = dragging;
    dragging = null;
    onMove?.(id, Math.max(0, Math.round(x)), Math.max(0, Math.round(y)));
  }
  // Keep a freely placed widget on screen when the window got smaller.
  const clampX = (x) => Math.max(0, Math.min(x, innerWidth - 300));
  const clampY = (y) => Math.max(48, Math.min(y, innerHeight - 120));
  function spot(w) {
    if (dragging?.id === w.id) return { x: dragging.x, y: dragging.y };
    return w.x != null && free ? { x: clampX(w.x), y: clampY(w.y) } : null;
  }

  // ---- app status and weather ----
  const appCounts = $derived.by(() => {
    if (!apps) return null;
    const down = apps.filter((a) => !a.stopped && ['exited', 'restarting', 'partial'].includes(a.state));
    return { total: apps.length, running: apps.filter((a) => a.state === 'running').length, down };
  });
  let weather = $state(null), weatherErr = $state('');
  async function loadWeather() {
    if (!weatherPlace || !widgets.some((w) => w.id === 'weather' && w.on)) return;
    const res = await api('/api/widgets/weather');
    if (res.ok) { weather = res.data; weatherErr = ''; } else weatherErr = res.error;
  }
  $effect(() => { weatherPlace; loadWeather(); });
  const SKY = [
    [[0], 'Clear', Sun], [[1, 2], 'Partly cloudy', CloudSun], [[3], 'Cloudy', Cloud], [[45, 48], 'Fog', CloudFog],
    [[51, 53, 55, 56, 57], 'Drizzle', CloudDrizzle], [[61, 63, 65, 66, 67, 80, 81, 82], 'Rain', CloudRain],
    [[71, 73, 75, 77, 85, 86], 'Snow', Snowflake], [[95, 96, 99], 'Thunderstorm', CloudLightning],
  ];
  const sky = (code, day) => {
    const hit = SKY.find(([codes]) => codes.includes(code)) || [[], 'Weather', Cloud];
    return code === 0 && !day ? ['Clear', MoonIcon] : [hit[1], hit[2]];
  };

  let snap = $state(null);
  let cpuHist = $state([]);
  let rxHist = $state([]), txHist = $state([]);
  let seq = $state(0);
  let diskWarn = $state([]);
  let iface = $state(readIface());

  function readIface() { try { return localStorage.getItem('nxbox-net-iface') || ''; } catch { return ''; } }
  $effect(() => { try { localStorage.setItem('nxbox-net-iface', iface); } catch { /* storage blocked */ } });

  const memPct = $derived(snap ? pct(snap.memory.used, snap.memory.total) : 0);
  const ifaces = $derived(snap ? snap.network.map((n) => n.name) : []);
  const netOf = (s) => {
    const list = iface ? s.network.filter((n) => n.name === iface) : s.network;
    return list.reduce((a, n) => ({ rx: a.rx + n.rx_rate, tx: a.tx + n.tx_rate }), { rx: 0, tx: 0 });
  };
  const net = $derived(snap ? netOf(snap) : { rx: 0, tx: 0 });
  const netTop = $derived(Math.max(1024, ...rxHist, ...txHist) * 1.15);
  const disks = $derived(snap ? snap.disks.slice(0, 4) : []);
  const diskTotal = $derived(snap ? snap.disks.reduce((a, d) => ({ used: a.used + d.used, total: a.total + d.total }), { used: 0, total: 0 }) : null);
  const shown = $derived(widgets.filter((w) => w.on && (w.id !== 'disks' || diskWarn.length)));

  // A new interface choice starts its chart from the samples we have.
  let history = [];
  $effect(() => { iface; rebuild(); });
  function rebuild() {
    const last = history.slice(-60);
    rxHist = last.map((s) => netOf(s).rx);
    txHist = last.map((s) => netOf(s).tx);
  }

  function open(e, to) { setOrigin(e.currentTarget); onNavigate(to); }

  // The live stream runs only while the desktop is on screen: closed behind a full window and in a
  // background tab, opened again (with its history) when it comes back.
  let es = null;
  function connect() {
    if (es) return;
    es = new EventSource('/api/stats/stream');
    const take = (s) => {
      snap = s;
      history = [...history, s].slice(-60);
      cpuHist = [...cpuHist, s.cpu.total].slice(-60);
      const n = netOf(s);
      rxHist = [...rxHist, n.rx].slice(-60);
      txHist = [...txHist, n.tx].slice(-60);
      seq++;
    };
    es.addEventListener('history', (e) => {
      try {
        const list = JSON.parse(e.data);
        if (!Array.isArray(list) || !list.length) return;
        history = list.slice(-60);
        cpuHist = history.map((s) => s.cpu.total);
        rebuild();
        snap = history[history.length - 1];
        seq++;
      } catch { /* ignore a broken frame */ }
    });
    es.onmessage = (e) => { try { const s = JSON.parse(e.data); if (!snap || s.time !== snap.time) take(s); } catch { /* ignore */ } };
    es.onerror = async () => {
      const res = await api('/api/auth/state');
      if (res.ok && !res.data.logged_in) { disconnect(); onSessionLost(); }
    };
  }
  function disconnect() { es?.close(); es = null; }
  let hidden = $state(document.hidden);
  $effect(() => { if (paused || hidden) disconnect(); else connect(); });

  onMount(() => {
    api('/api/disks/summary').then((res) => { if (res.ok) diskWarn = res.data.warnings; });
    const vis = () => (hidden = document.hidden);
    document.addEventListener('visibilitychange', vis);
    return () => { document.removeEventListener('visibilitychange', vis); disconnect(); };
  });
</script>

<div class="widgets">
  {#each shown as w (w.id)}
    {@const at = spot(w)}
    <div class="wbox" class:free={!!at} class:lifted={dragging?.id === w.id} style:left={at ? at.x + 'px' : null} style:top={at ? at.y + 'px' : null}>
    {#if free}<button class="grip" onpointerdown={(e) => grab(e, w.id)} onpointermove={drag} onpointerup={drop} onpointercancel={drop} aria-label="Move this widget" title="Drag to place freely"><GripVertical size={14} /></button>{/if}
    {#if w.id === 'clock'}
      <ClockWidget {clock} {username} hostname={system?.hostname || ''} />
    {:else if w.id === 'system'}
      <button class="glass widget" onclick={(e) => open(e, '/monitor')} aria-label="System status, open Monitor">
        <div class="w-head"><span class="t-head">System status</span><span class="t-caption num">{snap ? `Up ${duration(snap.uptime)}` : ''}</span></div>
        {#if snap}
          <div class="rings" class:two={!snap.temp}>
            <Ring value={snap.cpu.total} size={70} stroke={7} label="CPU" caption={snap.cpu.cores?.length ? `${snap.cpu.cores.length} cores` : ''} />
            <Ring value={memPct} size={70} stroke={7} label="Memory" caption={bytes(snap.memory.used, 0)} />
            {#if snap.temp}
            <div class="temp">
              <span class="temp-face">
                <span class="ico" class:warn={snap.temp >= 75} class:error={snap.temp >= 90}><Thermometer /></span>
                <span class="temp-val num">{snap.temp ? `${Math.round(snap.temp)}°` : '-'}</span>
              </span>
              <span class="t-head">Temperature</span>
              <span class="t-caption">CPU</span>
            </div>
            {/if}
          </div>
          <div class="spark"><Sparkline values={cpuHist} {seq} capacity={60} height={30} /></div>
        {:else}
          <div class="rings">{#each Array(3) as _}<span class="skeleton" style:width="70px" style:height="70px" style:border-radius="50%"></span>{/each}</div>
        {/if}
      </button>
    {:else if w.id === 'storage'}
      <button class="glass widget" onclick={(e) => open(e, '/settings/disks')} aria-label="Storage, open Disks">
        <div class="w-head"><span class="t-head">Storage</span>{#if diskTotal}<span class="t-caption num">{bytes(diskTotal.total - diskTotal.used)} free</span>{/if}</div>
        {#if snap}
          {#each disks as d (d.mountpoint)}
            {@const p = pct(d.used, d.total)}
            <div class="drive">
              <span class="drive-top"><span class="truncate">{d.mountpoint}</span><span class="t-caption num">{bytes(d.used, 0)} of {bytes(d.total, 0)}</span></span>
              <span class="bar"><span style:transform="scaleX({p / 100})" style:background={p >= 90 ? 'var(--error)' : p >= 75 ? 'var(--warn)' : 'var(--accent)'}></span></span>
            </div>
          {:else}
            <p class="t-caption">No disks found.</p>
          {/each}
        {:else}
          <span class="skeleton" style:height="3.25rem"></span>
        {/if}
      </button>
    {:else if w.id === 'network'}
      <section class="glass widget" aria-label="Network">
        <div class="w-head">
          <span class="t-head">Network</span>
          <select class="input pick" bind:value={iface} aria-label="Network interface">
            <option value="">All interfaces</option>
            {#each ifaces as n (n)}<option value={n}>{n}</option>{/each}
          </select>
        </div>
        {#if snap}
          <div class="net num">
            <span><ArrowDown size={14} />{rate(net.rx)}</span>
            <span class="tx"><ArrowUp size={14} />{rate(net.tx)}</span>
          </div>
          <div class="spark chart">
            <Sparkline values={rxHist} {seq} capacity={60} top={netTop} height={44} />
            <div class="overlay"><Sparkline values={txHist} {seq} capacity={60} top={netTop} height={44} fill={false} color="var(--text-2)" /></div>
          </div>
        {:else}
          <span class="skeleton" style:height="4.5rem"></span>
        {/if}
      </section>
    {:else if w.id === 'disks'}
      <button class="glass widget diskwarn" onclick={(e) => open(e, '/settings/disks')} aria-label="Disk warning, open Disks">
        <div class="w-head"><span class="t-head dw"><span class="ico warn"><TriangleAlert /></span>{diskWarn.length === 1 ? 'A disk needs attention' : `${diskWarn.length} disks need attention`}</span><ChevronRight size={16} /></div>
        {#each diskWarn as d}<p class="t-caption"><span class="dot" class:warn={d.verdict === 'warning'} class:error={d.verdict === 'failing'}></span> {d.model || d.disk}: {d.warnings[0] || (d.verdict === 'failing' ? 'SMART says it is failing.' : 'Check its health.')}</p>{/each}
      </button>
    {:else if w.id === 'apps'}
      <button class="glass widget" onclick={(e) => open(e, '/apps/installed')} aria-label="App status, open Apps">
        <div class="w-head"><span class="t-head">Apps</span>{#if appCounts}<span class="t-caption num">{appCounts.running} of {appCounts.total} running</span>{/if}</div>
        {#if !appCounts}<span class="skeleton" style:height="2.5rem"></span>
        {:else if appCounts.down.length}
          {#each appCounts.down.slice(0, 4) as a (a.id)}<p class="t-caption"><span class="dot error"></span> {a.name} is down</p>{/each}
        {:else if appCounts.total}<p class="t-caption"><span class="dot ok"></span> Everything runs as it should.</p>
        {:else}<p class="t-caption">No apps yet.</p>{/if}
      </button>
    {:else if w.id === 'weather'}
      <section class="glass widget" aria-label="Weather">
        {#if !weatherPlace}
          <div class="w-head"><span class="t-head">Weather</span></div>
          <button class="btn btn-tinted btn-sm" onclick={onEdit}>Pick a place</button>
        {:else if weather}
          {@const [text, Sky] = sky(weather.now.code, weather.now.is_day)}
          <div class="w-head"><span class="t-head truncate">{weather.place}</span><span class="t-caption">{text}</span></div>
          <div class="wx"><span class="ico xl accent"><Sky /></span><span class="wx-temp num">{Math.round(weather.now.temp)}°</span>
            <span class="t-caption num">H {Math.round(weather.now.max)}° · L {Math.round(weather.now.min)}°<br />Wind {Math.round(weather.now.wind)} km/h</span></div>
          <p class="t-caption attrib">Weather data by Open-Meteo.com</p>
        {:else}
          <div class="w-head"><span class="t-head">Weather</span></div>
          {#if weatherErr}<p class="t-caption">{weatherErr}</p>{:else}<span class="skeleton" style:height="3rem"></span>{/if}
        {/if}
      </section>
    {/if}
    </div>
  {/each}
  <button class="glass edit" onclick={onEdit}><SlidersHorizontal size={15} />Edit widgets</button>
</div>

<style>
  .widgets { display: grid; gap: 0.875rem; align-content: start; }
  .wbox { position: relative; display: grid; min-width: 0; }
  .wbox.free { position: fixed; z-index: 2; width: 19rem; }
  .wbox.lifted { z-index: 25; transform: scale(1.02); box-shadow: var(--shadow-float); border-radius: 1.375rem; }
  .grip {
    position: absolute; top: 0.375rem; right: 0.375rem; z-index: 1; display: grid; place-items: center; width: 1.75rem; height: 1.75rem;
    border: 0; background: none; color: var(--text-3); cursor: grab; opacity: 0; transition: opacity 150ms ease; touch-action: none;
  }
  .wbox:hover .grip, .grip:focus-visible, .wbox.lifted .grip { opacity: 1; }
  .grip:active { cursor: grabbing; }
  @media (hover: none) { .grip { opacity: 0.7; } }
  .wx { display: flex; align-items: center; gap: 0.875rem; }
  .wx-temp { font-size: 2.25rem; font-weight: 650; letter-spacing: -0.03em; line-height: 1; }
  .attrib { font-size: 0.6875rem; color: var(--text-3); }
  .widget {
    display: grid; gap: 0.75rem; padding: 1rem 1.125rem; border: 0; border-radius: 1.375rem; color: var(--text);
    font: inherit; text-align: left; min-width: 0;
  }
  button.widget { cursor: pointer; transition: transform 120ms ease-out; }
  button.widget:active { transform: scale(0.98); }
  .widget p { margin: 0; }
  .w-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; min-height: 1.75rem; }


  .rings { display: flex; justify-content: space-between; gap: 0.25rem; }
  .rings.two { justify-content: space-around; }
  .rings :global(.ring) { gap: 0.5rem; }
  .rings :global(.ring .value) { font-size: 1.25rem; }
  .temp { display: grid; justify-items: center; align-content: start; text-align: center; }
  .temp-face { display: grid; place-items: center; align-content: center; gap: 0.125rem; width: 70px; height: 70px; margin-bottom: 0.5rem; }
  .temp-val { font-size: 1.25rem; font-weight: 650; letter-spacing: -0.03em; line-height: 1; }
  .spark { margin: 0 -0.25rem; }
  .chart { position: relative; }
  .overlay { position: absolute; inset: 0; }

  .drive { display: grid; gap: 0.375rem; font-size: 0.8125rem; }
  .drive-top { display: flex; justify-content: space-between; gap: 0.5rem; }
  .net { display: flex; gap: 1.25rem; font-weight: 600; }
  .net span { display: inline-flex; align-items: center; gap: 0.375rem; }
  .net .tx { color: var(--text-2); }
  .pick { width: auto; max-width: 9.5rem; height: 1.875rem; padding-left: 0.625rem; font-size: 0.75rem; border-radius: 999px; background-position: right 0.5rem center; padding-right: 1.75rem; }
  .dw { display: inline-flex; align-items: center; gap: 0.5rem; }

  .edit {
    justify-self: start; display: inline-flex; align-items: center; gap: 0.5rem; min-height: 2.25rem; padding: 0 0.95rem;
    border: 0; border-radius: 999px; color: var(--text); font: inherit; font-size: 0.8125rem; font-weight: 600; cursor: pointer;
    transition: transform 100ms ease-out;
  }
  .edit:active { transform: scale(0.96); }
</style>
