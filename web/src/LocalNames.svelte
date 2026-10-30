<script>
  // Local names in the LAN through mDNS: box.local and <app>.box.local.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { toast } from '$lib/toast.svelte.js';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Radio from '@lucide/svelte/icons/radio-tower';

  let { onSessionLost } = $props();
  let st = $state(null), busy = $state(false), line = $state('');

  async function load() {
    const res = await api('/api/local-names');
    if (res.status === 401) return onSessionLost?.();
    if (res.ok) st = res.data;
  }

  async function set(on) {
    busy = true;
    if (on && !st.avahi) {
      const job = await runJob('/api/system/install', { op: 'avahi' }, (j) => (line = j.lines.at(-1) || ''));
      line = '';
      if (job.status !== 'done') { busy = false; return toast(job.error || 'Avahi could not be installed.', 'error', 9000); }
    }
    const res = await api('/api/local-names', { method: 'POST', body: { on } });
    busy = false;
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { toast(res.error, 'error', 8000); return load(); }
    st = res.data;
    toast(on ? 'Local names are on.' : 'Local names are off.', 'success');
  }

  onMount(load);
</script>

<p class="t-section sub">Local names</p>
<section class="group">
  <div class="row">
    <span class="ico"><Radio size={16} /></span>
    <div class="grow">
      <span>Names in your network</span>
      <span class="t-caption">
        {#if !st}Loading
        {:else if busy && line}{line}
        {:else if st.enabled}Devices in your LAN reach Box at box.local{st.ip ? ` (${st.ip})` : ''}{st.proxy ? ' and each app at http://<app>.box.local.' : '. Turn on the reverse proxy for app names without a port.'}
        {:else}Publish box.local and one name per app with mDNS (Avahi){st.avahi ? '' : '; Box installs Avahi when you turn this on'}.{/if}
      </span>
    </div>
    {#if st}
      {#if busy}<Loader size={16} class="spinner" />{/if}
      <input class="switch" type="checkbox" checked={st.enabled} disabled={busy} onchange={(e) => set(e.currentTarget.checked)} aria-label="Local names" />
    {/if}
  </div>
  {#if st?.enabled}
    {#each st.names as n (n)}
      <div class="row"><span class="ico"></span><span class="grow mono">{n}</span></div>
    {/each}
  {/if}
</section>
