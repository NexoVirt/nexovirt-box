<script>
  // Phone view (the installed web app opens here, /mobile): status, apps with start and stop,
  // and the latest notifications. "Desktop" switches to the full desktop.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { runJob, appURL } from '$lib/jobs.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes, pct, duration, ago } from '$lib/format.js';
  import Ring from '$lib/Ring.svelte';
  import AppIcon from '$lib/AppIcon.svelte';
  import Logo from '$lib/Logo.svelte';
  import Play from '@lucide/svelte/icons/play';
  import Square from '@lucide/svelte/icons/square';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Monitor from '@lucide/svelte/icons/monitor';
  import Bell from '@lucide/svelte/icons/bell';

  let { system, onSessionLost, onDesktop } = $props();
  let snap = $state(null), apps = $state(null), appsError = $state(''), notes = $state(null), busy = $state({});
  const mem = $derived(snap ? pct(snap.memory.used, snap.memory.total) : 0);
  const root = $derived(snap ? snap.disks.find((d) => d.mountpoint === '/') || snap.disks[0] : null);

  async function loadApps() {
    const res = await api('/api/apps');
    if (res.status === 401) return onSessionLost?.();
    if (res.ok) { apps = res.data; appsError = ''; } else { apps = []; appsError = res.error; }
  }
  async function loadNotes() {
    const res = await api('/api/notify');
    if (res.ok) notes = (res.data.history || []).slice(0, 10);
  }

  async function act(a, op) {
    busy = { ...busy, [a.id]: op };
    const job = await runJob(`/api/apps/${a.id}/action`, { op });
    const { [a.id]: _, ...rest } = busy;
    busy = rest;
    if (job.http === 401) return onSessionLost?.();
    toast(job.status === 'done' ? `${a.name} ${op === 'start' ? 'started' : 'stopped'}.` : job.error, job.status === 'done' ? 'success' : 'error');
    loadApps();
  }

  onMount(() => {
    loadApps(); loadNotes();
    const es = new EventSource('/api/stats/stream');
    es.onmessage = (e) => { try { snap = JSON.parse(e.data); } catch { /* ignore a broken frame */ } };
    es.addEventListener('history', (e) => { try { const l = JSON.parse(e.data); if (l.length) snap = l[l.length - 1]; } catch { /* ignore */ } });
    const t = setInterval(() => { if (!document.hidden) loadApps(); }, 15000);
    return () => { es.close(); clearInterval(t); };
  });
</script>

<div class="m">
  <header class="m-head">
    <Logo size={22} />
    <span class="grow"><span class="t-head">{system?.hostname || 'NexoVirt Box'}</span><span class="t-caption">{snap ? `Up ${duration(snap.uptime)}` : ''}</span></span>
    <button class="btn btn-gray btn-sm" onclick={onDesktop}><Monitor size={15} />Desktop</button>
  </header>

  <section class="tile rings">
    {#if snap}
      <Ring value={snap.cpu.total} size={86} stroke={8} label="CPU" />
      <Ring value={mem} size={86} stroke={8} label="Memory" caption={bytes(snap.memory.used, 0)} />
      {#if root}<Ring value={pct(root.used, root.total)} size={86} stroke={8} label="Disk" caption={`${bytes(root.free, 0)} free`} />{/if}
    {:else}
      {#each Array(3) as _}<span class="skeleton" style:width="86px" style:height="86px" style:border-radius="50%"></span>{/each}
    {/if}
  </section>

  <p class="t-section">Apps</p>
  <section class="group">
    {#if !apps}<div class="row"><Loader size={16} class="spinner" />Loading</div>
    {:else}
      {#each apps as a (a.id)}
        {@const running = a.state === 'running'}
        <div class="row">
          <AppIcon id={a.id} name={a.name} size={30} icon={a.icon} />
          <span class="grow"><span class="truncate">{a.name}</span><span class="t-caption"><span class="dot" class:ok={running} class:error={a.state === 'exited'}></span> {running ? 'Running' : a.state === 'exited' ? 'Stopped with an error' : 'Stopped'}</span></span>
          {#if running && appURL(a)}<a class="btn btn-gray btn-sm" href={appURL(a)} target="_blank" rel="noopener noreferrer">Open</a>{/if}
          <button class="icon-btn" onclick={() => act(a, running ? 'stop' : 'start')} disabled={!!busy[a.id]} aria-label="{running ? 'Stop' : 'Start'} {a.name}">
            {#if busy[a.id]}<Loader size={18} class="spinner" />{:else if running}<Square size={18} />{:else}<Play size={18} />{/if}
          </button>
        </div>
      {:else}
        <div class="row t-sub">{appsError || 'No apps yet.'}</div>
      {/each}
    {/if}
  </section>

  <p class="t-section">Notifications</p>
  <section class="group">
    {#if !notes}<div class="row"><Loader size={16} class="spinner" />Loading</div>
    {:else}
      {#each notes as n, i (i)}
        <div class="row"><span class="ico accent"><Bell /></span><span class="grow"><span class="truncate">{n.title}</span><span class="t-caption">{ago(n.at)}</span></span></div>
      {:else}
        <div class="row t-sub">Nothing was sent yet. Set up a channel in Settings to get them on your phone.</div>
      {/each}
    {/if}
  </section>
</div>

<style>
  .m {
    display: grid; gap: 0.875rem; max-width: 34rem; margin: 0 auto; min-height: 100svh; align-content: start;
    padding: calc(1rem + env(safe-area-inset-top, 0px)) max(1rem, env(safe-area-inset-right, 0px)) calc(1.5rem + env(safe-area-inset-bottom, 0px)) max(1rem, env(safe-area-inset-left, 0px));
  }
  .m-head { display: flex; align-items: center; gap: 0.75rem; }
  .grow { flex: 1; min-width: 0; display: grid; }
  .rings { display: flex; justify-content: space-around; padding: 1.25rem 0.75rem; }
  .t-section { margin: 0.5rem 0 0; }
</style>
