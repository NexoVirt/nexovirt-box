<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { bytes, rate, pct, duration, level } from '$lib/format.js';
  import Ring from '$lib/Ring.svelte';
  import Sparkline from '$lib/Sparkline.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import ArrowDown from '@lucide/svelte/icons/arrow-down';
  import ArrowUp from '@lucide/svelte/icons/arrow-up';
  import HardDrive from '@lucide/svelte/icons/hard-drive';
  import Network from '@lucide/svelte/icons/ethernet-port';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { system, onSessionLost } = $props();

  const KEEP = 300; // 10 minutes at one sample every 2 s (the server sends the same window first)
  const ranges = [
    { value: 30, label: '1 min' },
    { value: 150, label: '5 min' },
    { value: 300, label: '10 min' },
  ];

  let snap = $state(null);
  let live = $state('connecting'); // connecting | live | reconnecting
  let seq = $state(0);
  let range = $state(30);
  let cpuHistory = $state([]);
  let netHistory = $state({}); // interface -> combined rx+tx rates

  let memPct = $derived(snap ? pct(snap.memory.used, snap.memory.total) : 0);
  let swapPct = $derived(snap ? pct(snap.memory.swap_used, snap.memory.swap_total) : 0);
  let root = $derived(snap ? snap.disks.find((d) => d.mountpoint === '/') || snap.disks[0] : null);
  let rootPct = $derived(root ? pct(root.used, root.total) : 0);
  let loadPct = $derived(snap && system ? Math.round((snap.load[0] / system.cores) * 100) : 0);
  let cpuShown = $derived(cpuHistory.slice(-range));

  function push(arr = [], v) {
    const next = [...arr, v];
    return next.length > KEEP ? next.slice(next.length - KEEP) : next;
  }

  onMount(() => {
    const es = new EventSource('/api/stats/stream');
    // First event: the last 10 minutes, so the charts are full right away (also after a reconnect).
    es.addEventListener('history', (e) => {
      let list;
      try { list = JSON.parse(e.data); } catch { return; }
      if (!Array.isArray(list) || !list.length) return;
      cpuHistory = list.map((s) => s.cpu.total).slice(-KEEP);
      const next = {};
      for (const s of list) for (const n of s.network) (next[n.name] ??= []).push(n.rx_rate + n.tx_rate);
      for (const k in next) next[k] = next[k].slice(-KEEP);
      netHistory = next;
      snap = list[list.length - 1];
      live = 'live';
      seq++;
    });
    es.onmessage = (e) => {
      let s;
      try { s = JSON.parse(e.data); } catch { return; } // ignore a broken frame
      if (snap && s.time === snap.time) return; // already in the history
      snap = s;
      live = 'live';
      seq++;
      cpuHistory = push(cpuHistory, s.cpu.total);
      const next = {};
      for (const n of s.network) next[n.name] = push(netHistory[n.name], n.rx_rate + n.tx_rate);
      netHistory = next;
    };
    es.onerror = async () => {
      live = 'reconnecting';
      const res = await api('/api/auth/state');
      if (res.ok && !res.data.logged_in) {
        es.close();
        onSessionLost();
      }
    };
    return () => es.close();
  });
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Dashboard</h1>
      <p class="t-sub">
        {#if system}{[system.hostname, system.os, snap ? `up ${duration(snap.uptime)}` : ''].filter(Boolean).join('  ·  ')}
        {:else}<span class="skeleton inline"></span>{/if}
      </p>
    </div>
    {#if live === 'live'}
      <span class="pill"><span class="dot ok pulse"></span>Live</span>
    {:else}
      <span class="pill"><Loader size={13} class="spinner" />{live === 'reconnecting' ? 'Reconnecting' : 'Connecting'}</span>
    {/if}
  </header>

  {#if !snap}
    <section class="tile rings" aria-busy="true">
      {#each Array(4) as _}
        <div class="ring-skel"><span class="skeleton circle"></span><span class="skeleton line"></span></div>
      {/each}
    </section>
    <div class="split">
      <div class="tile"><span class="skeleton block"></span></div>
      <div class="tile"><span class="skeleton block"></span></div>
    </div>
  {:else}
    <section class="tile rings" aria-label="Usage">
      <Ring value={snap.cpu.total} label="CPU" caption="{snap.cpu.cores.length} threads" />
      <Ring value={memPct} label="Memory" caption="{bytes(snap.memory.used)} of {bytes(snap.memory.total)}" />
      {#if snap.memory.swap_total > 0}
        <Ring value={swapPct} label="Swap" caption="{bytes(snap.memory.swap_used)} of {bytes(snap.memory.swap_total)}" />
      {:else}
        <Ring value={0} label="Swap" caption="Not configured" />
      {/if}
      {#if root}
        <Ring value={rootPct} label="Disk" caption="{bytes(root.used)} of {bytes(root.total)}" />
      {:else}
        <Ring value={0} label="Disk" caption="No drive found" />
      {/if}
    </section>

    <div class="split">
      <section class="tile cpu">
        <div class="tile-head">
          <div>
            <h2 class="t-head">Processor</h2>
            <p class="t-caption truncate">{system?.cpu_model || `${snap.cpu.cores.length} threads`}</p>
          </div>
          <Segmented options={ranges} bind:value={range} label="Time range" />
        </div>
        <div class="big num">{snap.cpu.total.toFixed(1)}<small>%</small></div>
        <div class="chart">
          <span class="guide" style:top="0%"><span>100 %</span></span>
          <span class="guide" style:top="50%"><span>50 %</span></span>
          <Sparkline values={cpuShown} {seq} capacity={range} height={120} />
        </div>
        <p class="t-caption threads-label">Per thread</p>
        <div class="threads" title="Load per CPU thread">
          {#each snap.cpu.cores as c, i (i)}
            <div class="thread"><span style:transform="scaleY({Math.max(0.04, c / 100)})" style:background={c >= 75 ? level(c) : 'var(--accent)'}></span></div>
          {/each}
        </div>
        <div class="load">
          {#each snap.load as l, i (i)}
            <div class="load-cell">
              <span class="load-num num">{l.toFixed(2)}</span>
              <span class="t-caption">Load {['1 min', '5 min', '15 min'][i]}</span>
            </div>
          {/each}
          <div class="load-cell busy">
            <span class="load-num num">{loadPct}<small>%</small></span>
            <span class="t-caption">of {system?.cores ?? '-'} threads busy</span>
          </div>
        </div>
      </section>

      <section class="side">
        <div class="group">
          <div class="group-head"><h2 class="t-head">System</h2></div>
          {#if !system}
            {#each Array(6) as _}<div class="row"><span class="skeleton line"></span></div>{/each}
          {:else}
            {#each [
              ['Hostname', system.hostname],
              ['Operating system', system.os],
              ['Kernel', system.kernel],
              ['Architecture', system.arch],
              ['Cores', `${system.physical_cores || '-'} cores, ${system.cores} threads`],
              ['Virtualization', system.virtualization || 'None detected'],
              ['Uptime', duration(snap.uptime)],
            ] as [k, v] (k)}
              <div class="row"><span class="row-label">{k}</span><span class="row-value truncate" title={v}>{v}</span></div>
            {/each}
          {/if}
        </div>
      </section>
    </div>

    <section>
      <div class="group">
        <div class="group-head"><h2 class="t-head">Network</h2></div>
        {#each snap.network as n (n.name)}
          <div class="row net">
            <span class="row-icon"><Network size={16} /></span>
            <div class="net-name">
              <span class="t-head truncate">{n.name}</span>
              <span class="rates num">
                <span><ArrowDown size={13} />{rate(n.rx_rate)}</span>
                <span><ArrowUp size={13} />{rate(n.tx_rate)}</span>
              </span>
            </div>
            <div class="net-chart"><Sparkline values={(netHistory[n.name] ?? []).slice(-60)} {seq} capacity={60} max={0} height={36} /></div>
          </div>
        {:else}
          <div class="row t-sub">No network interfaces found.</div>
        {/each}
      </div>
    </section>

    <section>
      <div class="group">
        <div class="group-head"><h2 class="t-head">Storage</h2></div>
        {#each snap.disks as d (d.mountpoint)}
          {@const p = pct(d.used, d.total)}
          <div class="row disk">
            <span class="row-icon"><HardDrive size={16} /></span>
            <div class="disk-main">
              <div class="disk-top">
                <span class="disk-name">
                  <span class="t-head truncate">{d.mountpoint}</span>
                  <span class="t-caption truncate">{d.device} · {d.fstype}</span>
                </span>
                <span class="disk-size num">
                  <span>{bytes(d.used)} <span class="t-sub">of {bytes(d.total)}</span></span>
                  <span class="t-caption">{bytes(d.free)} free</span>
                </span>
              </div>
              <div class="bar"><span style:transform="scaleX({p / 100})" style:background={p >= 75 ? level(p) : 'var(--accent)'}></span></div>
            </div>
          </div>
        {:else}
          <div class="row t-sub">No mounted drives found.</div>
        {/each}
      </div>
    </section>
  {/if}
</div>

<style>
  .page { display: grid; gap: 1.5rem; }
  .page-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; flex-wrap: wrap; padding-top: 0.5rem; }
  h1, h2, p { margin: 0; }
  .page-head p { margin-top: 0.375rem; }
  .sep { color: var(--text-3); margin: 0 0.125rem; }
  .skeleton.inline { display: inline-block; width: 14rem; height: 0.9rem; vertical-align: middle; }

  .rings { display: grid; grid-template-columns: repeat(4, 1fr); gap: 1.5rem; padding: 1.75rem 1.25rem 1.5rem; }
  .ring-skel { display: grid; justify-items: center; gap: 0.875rem; }
  .skeleton.circle { width: 112px; height: 112px; border-radius: 50%; }
  .skeleton.line { display: block; width: 60%; height: 0.8rem; }
  .skeleton.block { display: block; height: 11rem; }

  .split { display: grid; grid-template-columns: 2fr 1fr; gap: 1.5rem; }
  .group-head { padding: 1rem 1.125rem 0.25rem; }

  .tile-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; }
  .tile-head > div { min-width: 0; }
  .big { font-size: 2.5rem; font-weight: 700; letter-spacing: -0.035em; line-height: 1; margin: 1rem 0 0.75rem; }
  .big small { font-size: 1rem; font-weight: 600; color: var(--text-2); margin-left: 2px; letter-spacing: 0; }
  .threads { display: flex; gap: 3px; height: 1.25rem; margin-top: 0.375rem; }
  .threads-label { margin-top: 1rem; }
  .thread { flex: 1; display: flex; align-items: flex-end; border-radius: 4px; background: var(--fill); overflow: hidden; }
  .thread span {
    display: block; width: 100%; height: 100%; border-radius: 4px; transform-origin: bottom;
    transition: transform var(--spring-t) var(--spring), background-color 300ms ease;
  }

  .chart { position: relative; padding-top: 0.25rem; border-bottom: 1px solid var(--hairline); }
  .guide { position: absolute; left: 0; right: 0; border-top: 1px dashed var(--hairline); }
  .guide span { position: absolute; right: 0; top: 2px; font-size: 0.6875rem; color: var(--text-3); }
  .load { display: grid; grid-template-columns: repeat(4, 1fr); gap: 0.75rem; margin-top: 1.25rem; padding-top: 1rem; border-top: 1px solid var(--hairline); }
  .load-cell { display: grid; gap: 0.125rem; min-width: 0; }
  .load-num { font-size: 1.25rem; font-weight: 650; letter-spacing: -0.02em; line-height: 1.15; }
  .load-num small { font-size: 0.75rem; color: var(--text-2); font-weight: 600; margin-left: 1px; }
  .side { display: flex; flex-direction: column; }
  .side .group { flex: 1; }
  .side .row { flex: 1; }
  .net-name { display: grid; gap: 0.125rem; width: 14rem; min-width: 0; }
  .net-chart { flex: 1; min-width: 0; }

  .row-icon {
    display: grid; place-items: center; width: 1.875rem; height: 1.875rem; border-radius: 0.5rem; flex-shrink: 0;
    background: var(--accent-fill); color: var(--accent); align-self: flex-start; margin-top: 0.125rem;
  }
  .disk-main { flex: 1; min-width: 0; display: grid; gap: 0.5rem; }
  .disk-top { display: flex; align-items: flex-start; justify-content: space-between; gap: 0.75rem; }
  .rates { display: flex; gap: 0.875rem; color: var(--text-2); font-size: 0.8125rem; white-space: nowrap; }
  .net .row-icon { align-self: center; margin-top: 0; }
  .rates span { display: inline-flex; align-items: center; gap: 0.2rem; }
  .disk-name { display: grid; min-width: 0; }
  .disk-size { display: grid; justify-items: end; text-align: right; white-space: nowrap; font-weight: 500; }

  @media (max-width: 1023px) {
    .split { grid-template-columns: 1fr; }
  }
  @media (max-width: 639px) {
    .rings { grid-template-columns: repeat(2, 1fr); row-gap: 1.75rem; }
    .load { grid-template-columns: repeat(2, 1fr); }
    .net { flex-wrap: wrap; }
    .net-name { width: auto; flex: 1; }
    .net-chart { flex-basis: 100%; }
  }
</style>
