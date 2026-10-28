<script>
  // Disk health: every physical disk with its SMART verdict, the attributes that predict a
  // failure, and self-tests. Virtual disks have no SMART data and say so.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes } from '$lib/format.js';
  import { runJob } from '$lib/jobs.js';
  import DataTable from '$lib/DataTable.svelte';
  import HardDrive from '@lucide/svelte/icons/hard-drive';
  import Cpu from '@lucide/svelte/icons/memory-stick';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import Stethoscope from '@lucide/svelte/icons/stethoscope';
  import Download from '@lucide/svelte/icons/download';

  let { onSessionLost } = $props();

  let data = $state(null), error = $state(''), loading = $state(false);
  let installing = $state(false), installLine = $state('');
  let testing = $state('');
  let poll;

  const VERDICT = { good: 'Healthy', warning: 'Needs attention', failing: 'Failing', unknown: 'Unknown' };
  const DOT = { good: 'ok', warning: 'warn', failing: 'error', unknown: '' };
  const DISK_COLS = [
    { key: 'model', label: 'Disk', primary: true, sortable: true, value: (d) => d.model || d.name },
    { key: 'path', label: 'Device', sortable: true },
    { key: 'size', label: 'Size', align: 'right', sortable: true },
    { key: 'kind', label: 'Type', hide: 'md' },
    { key: 'serial', label: 'Serial', hide: 'md' },
    { key: 'health', label: 'Health', value: (d) => d.smart?.verdict || '' },
  ];
  const KIND = { hdd: 'Hard disk', ssd: 'SSD', nvme: 'NVMe SSD' };
  const lost = (res) => { if (res.status === 401) { onSessionLost(); return true; } return false; };

  async function load() {
    loading = true;
    const res = await api('/api/disks');
    loading = false;
    if (lost(res)) return;
    if (!res.ok) { error = res.error; return; }
    error = '';
    data = res.data;
    clearTimeout(poll);
    if (data.disks.some((d) => d.smart?.testing)) poll = setTimeout(load, 15000);
  }

  const needsSmart = $derived(data?.disks.some((d) => d.smart?.reason === 'not_installed'));

  function why(d) {
    const s = d.smart;
    if (!s) return '';
    if (s.reason === 'virtual') return 'No SMART data (virtual disk)';
    if (s.reason === 'not_installed') return 'SMART not read yet';
    if (s.reason === 'standby') return 'Asleep, not woken up';
    if (s.reason === 'unsupported') return 'This disk does not report SMART data';
    return VERDICT[s.verdict];
  }


  async function installSmart() {
    installing = true;
    const done = await runJob('/api/system/install', { op: 'smart' }, (j) => (installLine = j.lines.at(-1) || ''));
    installing = false;
    installLine = '';
    if (done.http === 401) return onSessionLost();
    if (done.status !== 'done') return toast(done.error || 'smartmontools could not be installed.', 'error', 9000);
    toast('smartmontools is installed.', 'success');
    load();
  }

  async function selftest(d, kind) {
    testing = d.path + kind;
    const res = await api('/api/disks/selftest', { method: 'POST', body: { path: d.path, op: kind } });
    testing = '';
    if (lost(res)) return;
    if (!res.ok) return toast(res.error, 'error', 9000);
    toast(kind === 'short' ? 'The short test runs for about two minutes.' : 'The long test can take several hours. The disk stays usable.', 'info', 7000);
    setTimeout(load, 3000);
  }

  const hours = (h) => (h >= 48 ? `${Math.round(h / 24).toLocaleString()} days` : `${h} h`);

  onMount(() => {
    load();
    return () => clearTimeout(poll);
  });
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Disks</h1>
      <p class="t-sub">The physical disks of this server and what their SMART data says about their health.</p>
    </div>
    <button class="btn btn-gray" onclick={load} disabled={loading}>
      {#if loading}<Loader size={16} class="spinner" />Reading{:else}<RefreshCw size={16} />Refresh{/if}
    </button>
  </header>

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else if !data}
    <div class="group">{#each Array(3) as _}<div class="row"><span class="skeleton disk"></span><span class="skeleton line"></span></div>{/each}</div>
  {:else}
    {#if needsSmart}
      <div class="tile banner">
        <span class="ico md accent"><Stethoscope /></span>
        <span class="grow">
          <span class="t-head">Read the disks' health data</span>
          <span class="t-caption">{installing ? installLine || 'Installing smartmontools' : 'Box needs smartmontools (smartctl) for SMART data. It is a small Debian package.'}</span>
        </span>
        <button class="btn btn-primary btn-sm" onclick={installSmart} disabled={installing}>
          {#if installing}<Loader size={15} class="spinner" />Installing{:else}<Download size={15} />Install{/if}
        </button>
      </div>
    {/if}

    <DataTable label="Disks" columns={DISK_COLS} rows={data.disks} key="path" expandable={(d) => !!d.smart?.available}
      empty={{ icon: HardDrive, title: 'No disks found' }}>
      {#snippet cell(d, c)}
        {@const s = d.smart}
        {#if c.key === 'model'}
          {@const Icon = d.kind === 'nvme' ? Cpu : HardDrive}
          <span class="who"><span class="ico accent"><Icon /></span><span class="truncate">{d.model || d.name}</span></span>
        {:else if c.key === 'path'}<span class="mono">{d.path}</span>
        {:else if c.key === 'size'}<span class="num">{bytes(d.size)}</span>
        {:else if c.key === 'kind'}<span class="muted">{d.virtual ? 'Virtual disk' : KIND[d.kind]}</span>
        {:else if c.key === 'serial'}<span class="mono muted">{d.virtual ? '' : d.serial || ''}</span>
        {:else}
          <span class="state">
            {#if s?.testing}<span class="pill"><Loader size={13} class="spinner" />Self-test {100 - (s.remaining || 0)} %</span>{/if}
            <span class="verdict" class:muted={!s?.available}>{#if s?.available}<span class="dot {DOT[s.verdict]}"></span>{/if}{why(d)}</span>
          </span>
        {/if}
      {/snippet}
      {#snippet detail(d)}
        {@const s = d.smart}
      <div class="detail">
        {#if s.warnings.length}
          <div class="warns">{#each s.warnings as w}<p><CircleAlert size={15} />{w}</p>{/each}</div>
        {/if}
        <dl class="facts">
          <div><dt>Overall</dt><dd>{s.passed === undefined ? 'Not reported' : s.passed ? 'Passed' : 'Failed'}</dd></div>
          {#if s.temperature !== undefined}<div><dt>Temperature</dt><dd class="num">{s.temperature} °C</dd></div>{/if}
          {#if s.power_on_hours !== undefined}<div><dt>Powered on</dt><dd class="num">{hours(s.power_on_hours)}</dd></div>{/if}
          {#if s.wear_used !== undefined}<div><dt>Life used</dt><dd class="num">{s.wear_used} %</dd></div>{/if}
          {#if s.reallocated !== undefined}<div><dt>Reallocated sectors</dt><dd class="num">{s.reallocated}</dd></div>{/if}
          {#if s.pending !== undefined}<div><dt>Pending sectors</dt><dd class="num">{s.pending}</dd></div>{/if}
          {#if s.uncorrectable !== undefined}<div><dt>Unreadable sectors</dt><dd class="num">{s.uncorrectable}</dd></div>{/if}
          {#if s.media_errors !== undefined}<div><dt>Media errors</dt><dd class="num">{s.media_errors}</dd></div>{/if}
        </dl>
        <div class="tests">
          <div class="tests-head">
            <span class="t-section">Self-tests</span>
            <span class="btns">
              <button class="btn btn-gray btn-sm" onclick={() => selftest(d, 'short')} disabled={!!testing || s.testing}>{#if testing === d.path + 'short'}<Loader size={14} class="spinner" />{/if}Short test</button>
              <button class="btn btn-gray btn-sm" onclick={() => selftest(d, 'long')} disabled={!!testing || s.testing}>{#if testing === d.path + 'long'}<Loader size={14} class="spinner" />{/if}Long test</button>
            </span>
          </div>
          {#if s.tests.length}
            {#each s.tests as t}
              <div class="test"><span class="dot" class:ok={t.passed} class:error={!t.passed}></span><span class="grow">{t.type}</span><span class="t-caption">{t.status}</span><span class="t-caption num">at {hours(t.hours)}</span></div>
            {/each}
          {:else}
            <p class="t-caption">No self-test has run on this disk yet.</p>
          {/if}
        </div>
      </div>
      {/snippet}
    </DataTable>
    <p class="t-caption foot">Box checks the disks every six hours and shows a warning on Home when one needs attention.</p>
  {/if}
</div>

<style>
  .banner { display: flex; align-items: center; gap: 0.875rem; padding: 1rem 1.125rem; }
  .banner .grow { flex: 1; min-width: 0; display: grid; gap: 0.125rem; }
  .skeleton.line { display: block; width: 50%; height: 1rem; }
  .skeleton.disk { width: 1.75rem; height: 1.75rem; }

  .who { display: inline-flex; align-items: center; gap: 0.625rem; min-width: 0; max-width: 100%; }
  .muted { color: var(--text-2); }
  .state { display: inline-flex; align-items: center; gap: 0.625rem; }
  .verdict { display: inline-flex; align-items: center; gap: 0.4rem; font-weight: 500; }
  .verdict.muted { font-weight: 400; }

  .detail { display: grid; gap: 1rem; padding: 1rem 1.25rem 1.25rem 3.125rem; background: var(--surface-2); }
  .warns { display: grid; gap: 0.25rem; }
  .warns p { margin: 0; display: flex; align-items: center; gap: 0.5rem; color: var(--warn); font-weight: 500; }
  .facts { margin: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(10rem, 1fr)); gap: 0.75rem 1.5rem; }
  .facts div { display: grid; gap: 0.125rem; }
  .facts dt { font-size: 0.75rem; color: var(--text-2); }
  .facts dd { margin: 0; font-weight: 600; }
  .tests { display: grid; gap: 0.375rem; }
  .tests-head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap; }
  .btns { display: flex; gap: 0.5rem; }
  .test { display: flex; align-items: center; gap: 0.625rem; min-height: 2.25rem; }
  .test .grow { flex: 1; font-weight: 500; }
  .tests p { margin: 0; }
  .foot { margin: 0; }

  @media (max-width: 639px) { .detail { padding-left: 1rem; } }
</style>
