<script>
  // Left column of the desktop: frosted widgets in the order the user picked (clock, system status,
  // storage, network, disk health). Live numbers come from the stats stream.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { bytes, rate, pct, duration } from '$lib/format.js';
  import { setOrigin } from '$lib/desktop.svelte.js';
  import Ring from '$lib/Ring.svelte';
  import Sparkline from '$lib/Sparkline.svelte';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import ArrowDown from '@lucide/svelte/icons/arrow-down';
  import ArrowUp from '@lucide/svelte/icons/arrow-up';
  import Thermometer from '@lucide/svelte/icons/thermometer';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import SlidersHorizontal from '@lucide/svelte/icons/sliders-horizontal';

  let { system, username = '', widgets = [], onNavigate, onEdit, onSessionLost } = $props();

  let snap = $state(null);
  let cpuHist = $state([]);
  let rxHist = $state([]), txHist = $state([]);
  let seq = $state(0);
  let now = $state(new Date());
  let diskWarn = $state([]);
  let iface = $state(readIface());

  function readIface() { try { return localStorage.getItem('nxbox-net-iface') || ''; } catch { return ''; } }
  $effect(() => { try { localStorage.setItem('nxbox-net-iface', iface); } catch { /* storage blocked */ } });

  const greeting = $derived(now.getHours() < 5 ? 'Good night' : now.getHours() < 12 ? 'Good morning' : now.getHours() < 18 ? 'Good afternoon' : 'Good evening');
  const timeText = $derived(now.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }));
  const dateText = $derived(now.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' }));
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

  onMount(() => {
    api('/api/disks/summary').then((res) => { if (res.ok) diskWarn = res.data.warnings; });
    const tick = setInterval(() => (now = new Date()), 10000);
    const es = new EventSource('/api/stats/stream');
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
      if (res.ok && !res.data.logged_in) { es.close(); onSessionLost(); }
    };
    return () => { clearInterval(tick); es.close(); };
  });
</script>

<div class="widgets">
  {#each shown as w (w.id)}
    {#if w.id === 'clock'}
      <section class="glass widget clock" aria-label="Clock">
        <div class="time num">{timeText}</div>
        <div class="date">{dateText}</div>
        <div class="t-caption greet">{greeting}{username ? `, ${username}` : ''}{system?.hostname ? `  ·  ${system.hostname}` : ''}</div>
      </section>
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
    {/if}
  {/each}
  <button class="glass edit" onclick={onEdit}><SlidersHorizontal size={15} />Edit widgets</button>
</div>

<style>
  .widgets { display: grid; gap: 0.875rem; align-content: start; }
  .widget {
    display: grid; gap: 0.75rem; padding: 1rem 1.125rem; border: 0; border-radius: 1.375rem; color: var(--text);
    font: inherit; text-align: left; min-width: 0;
  }
  button.widget { cursor: pointer; transition: transform 120ms ease-out; }
  button.widget:active { transform: scale(0.98); }
  .widget p { margin: 0; }
  .w-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; min-height: 1.75rem; }

  .clock { gap: 0.125rem; padding-block: 1.125rem; }
  @media (max-width: 639px) { .clock .time { font-size: 2.75rem; } .clock .date { margin-top: 0.375rem; } }
  .time { font-size: 3.5rem; line-height: 1; font-weight: 250; letter-spacing: -0.045em; }
  .date { font-size: 1.0625rem; font-weight: 600; letter-spacing: -0.012em; margin-top: 0.5rem; }
  .greet { margin-top: 0.125rem; }

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
