<script>
  // Health overview: problems first, then disks (SMART), temperatures, UPS (NUT) and backup age.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { ago, duration } from '$lib/format.js';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import HardDrive from '@lucide/svelte/icons/hard-drive';
  import Thermometer from '@lucide/svelte/icons/thermometer';
  import BatteryCharging from '@lucide/svelte/icons/battery-charging';
  import Archive from '@lucide/svelte/icons/archive';

  let { onSessionLost, onNavigate } = $props();
  let data = $state(null), error = $state(''), loading = $state(false), names = $state({});

  const VERDICT = { good: 'Healthy', warning: 'Needs attention', failing: 'Failing', unknown: 'Unknown' };
  const DOT = { good: 'ok', warning: 'warn', failing: 'error', unknown: '' };

  async function load() {
    loading = true;
    const [res, apps] = await Promise.all([api('/api/health'), api('/api/apps')]);
    loading = false;
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { error = res.error; return; }
    error = '';
    data = res.data;
    if (apps.ok) names = Object.fromEntries(apps.data.map((a) => [a.id, a.name]));
  }

  function tempTone(t) {
    const lim = t.critical || t.high;
    if (!lim) return '';
    return t.celsius >= lim - 5 ? 'error' : t.celsius >= lim - 15 ? 'warn' : 'ok';
  }
  function upsText(u) {
    if (u.status.includes('LB')) return 'Battery low';
    if (u.status.includes('OB')) return 'On battery';
    if (u.status.includes('OL')) return 'On mains power';
    return u.status || 'Unknown';
  }
  const physical = $derived(data?.disks.filter((d) => !d.virtual) || []);

  onMount(load);
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Health</h1>
      <p class="t-sub">Disks, temperatures, power and backups at a glance. Box checks this every 10 minutes and notifies you about problems.</p>
    </div>
    <button class="btn btn-gray" onclick={load} disabled={loading}>{#if loading}<Loader size={16} class="spinner" />Checking{:else}<RefreshCw size={16} />Refresh{/if}</button>
  </header>

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else if !data}
    <div class="group">{#each Array(4) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</div>
  {:else}
    {#if data.problems.length}
      <div class="group">
        {#each data.problems as p (p.key)}
          <div class="row"><span class="ico" class:error={p.level === 'error'} class:warn={p.level !== 'error'}><TriangleAlert /></span><span>{p.text}</span></div>
        {/each}
      </div>
    {:else}
      <div class="tile ok-tile"><span class="ico md ok"><CircleCheck /></span><span class="t-head">No problems found</span></div>
    {/if}

    <section class="group">
      <div class="group-head"><h2 class="t-head">Disks</h2><button class="link" onclick={() => onNavigate?.('/settings/disks')}>Details and self-tests</button></div>
      {#each physical as d (d.path)}
        <div class="row">
          <span class="ico accent"><HardDrive /></span>
          <span class="grow"><span class="truncate">{d.model || d.name}</span><span class="t-caption mono">{d.path}</span></span>
          {#if d.smart?.temperature != null}<span class="num muted">{d.smart.temperature} °C</span>{/if}
          <span class="verdict">
            {#if d.smart?.available}<span class="dot {DOT[d.smart.verdict]}"></span>{VERDICT[d.smart.verdict]}
            {:else if d.smart?.reason === 'not_installed'}<button class="link" onclick={() => onNavigate?.('/settings/disks')}>Install smartmontools</button>
            {:else}<span class="muted">No SMART data</span>{/if}
          </span>
        </div>
      {:else}
        <div class="row t-sub">Only virtual disks (no SMART data on a VM).</div>
      {/each}
    </section>

    <section class="group">
      <div class="group-head"><h2 class="t-head">Temperatures</h2></div>
      {#each data.temps as t (t.sensor)}
        <div class="row">
          <span class="ico accent"><Thermometer /></span>
          <span class="grow truncate">{t.sensor}</span>
          <span class="verdict num">{#if tempTone(t)}<span class="dot {tempTone(t)}"></span>{/if}{t.celsius.toFixed(0)} °C{#if t.critical}<span class="t-caption"> of {t.critical.toFixed(0)}</span>{/if}</span>
        </div>
      {:else}
        <div class="row t-sub">This server reports no temperature sensors (common on virtual machines).</div>
      {/each}
    </section>

    <section class="group">
      <div class="group-head"><h2 class="t-head">Power (UPS)</h2></div>
      {#each data.ups.ups as u (u.name)}
        <div class="row">
          <span class="ico accent"><BatteryCharging /></span>
          <span class="grow"><span class="truncate">{u.model || u.name}</span><span class="t-caption">{upsText(u)}{u.load >= 0 ? `, ${u.load} % load` : ''}</span></span>
          <span class="verdict num">{#if u.charge >= 0}{u.charge} %{/if}{#if u.runtime > 0}<span class="t-caption"> · {duration(u.runtime)}</span>{/if}</span>
        </div>
      {:else}
        <div class="row t-sub">{data.ups.error || (data.ups.installed ? 'NUT is installed but has no UPS configured.' : 'No UPS: Box reads one through NUT (upsc) when it is set up on this server.')}</div>
      {/each}
    </section>

    <section class="group">
      <div class="group-head"><h2 class="t-head">Backups</h2><button class="link" onclick={() => onNavigate?.('/backups')}>Open Backups</button></div>
      {#each data.backups as b (b.app)}
        {@const old = b.last_ok && Date.now() / 1000 - b.last_ok > 8 * 86400}
        <div class="row">
          <span class="ico accent"><Archive /></span>
          <span class="grow truncate">{b.app === 'box' ? 'Box settings' : names[b.app] || b.app}</span>
          <span class="verdict">{#if b.scheduled && (!b.last_ok || old)}<span class="dot warn"></span>{/if}{b.last_ok ? `Last backup ${ago(b.last_ok)}` : 'No successful backup yet'}</span>
        </div>
      {:else}
        <div class="row t-sub">No backups yet. Set up a schedule in Backups.</div>
      {/each}
    </section>
  {/if}
</div>

<style>
  .page { display: grid; gap: 1.25rem; }
  h1, h2, p { margin: 0; }
  .group-head { display: flex; justify-content: space-between; align-items: baseline; padding: 1rem 1.125rem 0.25rem; }
  .grow { flex: 1; min-width: 0; display: grid; }
  .verdict { display: inline-flex; align-items: center; gap: 0.4rem; white-space: nowrap; }
  .muted { color: var(--text-2); }
  .ok-tile { display: flex; align-items: center; gap: 0.75rem; }
  .link { border: 0; background: none; padding: 0; color: var(--accent); font: inherit; font-size: 0.8125rem; font-weight: 500; cursor: pointer; }
  .skeleton.line { display: block; width: 60%; height: 0.8rem; }
</style>
