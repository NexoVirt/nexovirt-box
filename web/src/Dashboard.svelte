<script>
  import { onMount } from 'svelte';
  import { api } from './lib/api.js';
  import { bytes, rate, pct, duration, level } from './lib/format.js';
  import Icon from './lib/Icon.svelte';
  import Sparkline from './lib/Sparkline.svelte';

  let { onSessionLost } = $props();

  const HISTORY = 60; // 2 minutes at one sample every 2 s

  let system = $state(null);
  let systemError = $state('');
  let snap = $state(null);
  let live = $state('connecting'); // connecting | live | reconnecting
  let cpuHistory = $state([]);
  let memHistory = $state([]);

  let memPct = $derived(snap ? pct(snap.memory.used, snap.memory.total) : 0);
  let swapPct = $derived(snap ? pct(snap.memory.swap_used, snap.memory.swap_total) : 0);
  let loadPct = $derived(snap && system ? Math.round((snap.load[0] / system.cores) * 100) : 0);

  function push(arr, v) {
    const next = [...arr, v];
    return next.length > HISTORY ? next.slice(next.length - HISTORY) : next;
  }

  async function loadSystem() {
    systemError = '';
    const res = await api('/api/system');
    if (res.status === 401) return onSessionLost();
    if (!res.ok) systemError = res.error;
    else system = res.data;
  }

  onMount(() => {
    loadSystem();
    const es = new EventSource('/api/stats/stream');
    es.onmessage = (e) => {
      try {
        const s = JSON.parse(e.data);
        snap = s;
        live = 'live';
        cpuHistory = push(cpuHistory, s.cpu.total);
        memHistory = push(memHistory, pct(s.memory.used, s.memory.total));
      } catch { /* ignore a broken frame */ }
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

<div class="page-header d-print-none">
  <div class="container-fluid">
    <div class="row g-2 align-items-center">
      <div class="col">
        <div class="page-pretitle">{system ? system.hostname : 'Overview'}</div>
        <h2 class="page-title">Dashboard</h2>
      </div>
      <div class="col-auto ms-auto">
        {#if live === 'live'}
          <span class="badge bg-green-lt"><span class="status-dot status-dot-animated status-green me-1"></span>Live</span>
        {:else if live === 'reconnecting'}
          <span class="badge bg-yellow-lt"><span class="spinner-border spinner-border-sm me-1" role="status"></span>Reconnecting</span>
        {:else}
          <span class="badge bg-secondary-lt"><span class="spinner-border spinner-border-sm me-1" role="status"></span>Connecting</span>
        {/if}
      </div>
    </div>
  </div>
</div>

<div class="page-body">
  <div class="container-fluid">
    {#if !snap}
      <div class="card">
        <div class="card-body text-center py-5">
          <div class="spinner-border text-secondary" role="status"></div>
          <div class="text-secondary mt-3">Reading the system...</div>
        </div>
      </div>
    {:else}
      <div class="row row-deck row-cards">
        <!-- CPU -->
        <div class="col-sm-6 col-xl-3">
          <div class="card">
            <div class="card-body">
              <div class="d-flex align-items-center mb-2">
                <span class="bg-blue-lt text-blue avatar me-3"><Icon name="cpu" class="icon icon-2" /></span>
                <div>
                  <div class="subheader">CPU</div>
                  <div class="h2 mb-0">{snap.cpu.total.toFixed(1)} %</div>
                </div>
              </div>
              <Sparkline values={cpuHistory} />
              <div class="text-secondary small text-truncate">{system?.cpu_model || ''}</div>
            </div>
          </div>
        </div>
        <!-- Memory -->
        <div class="col-sm-6 col-xl-3">
          <div class="card">
            <div class="card-body">
              <div class="d-flex align-items-center mb-2">
                <span class="bg-green-lt text-green avatar me-3"><Icon name="database" class="icon icon-2" /></span>
                <div>
                  <div class="subheader">Memory</div>
                  <div class="h2 mb-0">{memPct} %</div>
                </div>
              </div>
              <Sparkline values={memHistory} class="text-green" />
              <div class="text-secondary small">{bytes(snap.memory.used)} of {bytes(snap.memory.total)}</div>
              {#if snap.memory.swap_total > 0}
                <div class="text-secondary small">Swap {bytes(snap.memory.swap_used)} of {bytes(snap.memory.swap_total)} ({swapPct} %)</div>
              {:else}
                <div class="text-secondary small">No swap</div>
              {/if}
            </div>
          </div>
        </div>
        <!-- Load -->
        <div class="col-sm-6 col-xl-3">
          <div class="card">
            <div class="card-body">
              <div class="d-flex align-items-center mb-3">
                <span class="bg-yellow-lt text-yellow avatar me-3"><Icon name="activity" class="icon icon-2" /></span>
                <div>
                  <div class="subheader">Load average</div>
                  <div class="h2 mb-0">{snap.load[0].toFixed(2)}</div>
                </div>
              </div>
              <div class="progress progress-sm mb-2">
                <div class="progress-bar bg-{level(loadPct)}" style:width="{Math.min(100, loadPct)}%" role="progressbar" aria-valuenow={loadPct} aria-valuemin="0" aria-valuemax="100"></div>
              </div>
              <div class="text-secondary small">1, 5, 15 min: {snap.load.map((l) => l.toFixed(2)).join(', ')}</div>
              <div class="text-secondary small">{system?.cores || '-'} CPU threads</div>
            </div>
          </div>
        </div>
        <!-- Uptime -->
        <div class="col-sm-6 col-xl-3">
          <div class="card">
            <div class="card-body">
              <div class="d-flex align-items-center mb-3">
                <span class="bg-purple-lt text-purple avatar me-3"><Icon name="clock" class="icon icon-2" /></span>
                <div>
                  <div class="subheader">Uptime</div>
                  <div class="h2 mb-0">{duration(snap.uptime)}</div>
                </div>
              </div>
              <div class="text-secondary small text-truncate">{system?.os || ''}</div>
              <div class="text-secondary small text-truncate">Kernel {system?.kernel || '-'}</div>
            </div>
          </div>
        </div>

        <!-- System -->
        <div class="col-lg-6">
          <div class="card">
            <div class="card-header"><h3 class="card-title">System</h3></div>
            <div class="card-body">
              {#if systemError}
                <div class="alert alert-warning mb-0" role="alert">{systemError}</div>
              {:else if !system}
                <div class="spinner-border spinner-border-sm text-secondary" role="status"></div>
              {:else}
                <div class="datagrid">
                  <div class="datagrid-item"><div class="datagrid-title">Hostname</div><div class="datagrid-content">{system.hostname}</div></div>
                  <div class="datagrid-item"><div class="datagrid-title">Operating system</div><div class="datagrid-content">{system.os}</div></div>
                  <div class="datagrid-item"><div class="datagrid-title">Kernel</div><div class="datagrid-content">{system.kernel}</div></div>
                  <div class="datagrid-item"><div class="datagrid-title">Architecture</div><div class="datagrid-content">{system.arch}</div></div>
                  <div class="datagrid-item"><div class="datagrid-title">Processor</div><div class="datagrid-content">{system.cpu_model || '-'}</div></div>
                  <div class="datagrid-item"><div class="datagrid-title">Cores</div><div class="datagrid-content">{system.physical_cores || '-'} cores, {system.cores} threads</div></div>
                  <div class="datagrid-item"><div class="datagrid-title">Virtualization</div><div class="datagrid-content">{system.virtualization || 'None detected'}</div></div>
                </div>
              {/if}
            </div>
          </div>
        </div>

        <!-- Cores -->
        <div class="col-lg-6">
          <div class="card">
            <div class="card-header"><h3 class="card-title">CPU per thread</h3></div>
            <div class="card-body">
              <div class="row g-3">
                {#each snap.cpu.cores as c, i}
                  <div class="col-6 col-md-4">
                    <div class="d-flex justify-content-between small mb-1">
                      <span class="text-secondary">Thread {i}</span><span>{c.toFixed(0)} %</span>
                    </div>
                    <div class="progress progress-sm">
                      <div class="progress-bar bg-{level(c)}" style:width="{c}%" role="progressbar" aria-valuenow={c} aria-valuemin="0" aria-valuemax="100"></div>
                    </div>
                  </div>
                {/each}
              </div>
            </div>
          </div>
        </div>

        <!-- Disks -->
        <div class="col-12">
          <div class="card">
            <div class="card-header"><h3 class="card-title">Drives</h3></div>
            {#if snap.disks.length === 0}
              <div class="card-body text-secondary">No mounted drives found.</div>
            {:else}
              <div class="table-responsive">
                <table class="table table-vcenter card-table">
                  <thead>
                    <tr><th>Mount point</th><th>Device</th><th>Filesystem</th><th class="text-end">Size</th><th class="text-end">Used</th><th class="text-end">Free</th><th class="w-25 text-nowrap">Usage</th></tr>
                  </thead>
                  <tbody>
                    {#each snap.disks as d (d.mountpoint)}
                      {@const p = pct(d.used, d.total)}
                      <tr>
                        <td class="fw-medium text-break">{d.mountpoint}</td>
                        <td class="text-secondary">{d.device}</td>
                        <td><span class="badge bg-secondary-lt">{d.fstype}</span></td>
                        <td class="text-end text-nowrap">{bytes(d.total)}</td>
                        <td class="text-end text-nowrap">{bytes(d.used)}</td>
                        <td class="text-end text-nowrap">{bytes(d.free)}</td>
                        <td>
                          <div class="d-flex align-items-center gap-2">
                            <div class="progress progress-sm flex-fill">
                              <div class="progress-bar bg-{level(p)}" style:width="{p}%" role="progressbar" aria-valuenow={p} aria-valuemin="0" aria-valuemax="100"></div>
                            </div>
                            <span class="small text-secondary text-nowrap">{p} %</span>
                          </div>
                        </td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}
          </div>
        </div>

        <!-- Network -->
        <div class="col-12">
          <div class="card">
            <div class="card-header"><h3 class="card-title">Network</h3></div>
            {#if snap.network.length === 0}
              <div class="card-body text-secondary">No network interfaces found.</div>
            {:else}
              <div class="table-responsive">
                <table class="table table-vcenter card-table">
                  <thead><tr><th>Interface</th><th class="text-end">Download</th><th class="text-end">Upload</th></tr></thead>
                  <tbody>
                    {#each snap.network as n (n.name)}
                      <tr>
                        <td class="fw-medium"><Icon name="network" class="icon text-secondary me-1" />{n.name}</td>
                        <td class="text-end text-nowrap"><Icon name="arrow-down" class="icon text-green" /> {rate(n.rx_rate)}</td>
                        <td class="text-end text-nowrap"><Icon name="arrow-up" class="icon text-blue" /> {rate(n.tx_rate)}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}
          </div>
        </div>
      </div>
    {/if}
  </div>
</div>
