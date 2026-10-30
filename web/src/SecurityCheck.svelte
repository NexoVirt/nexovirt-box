<script>
  // Security check: a score, the findings (worst first) and a fix button where a safe fix exists.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { withProof } from '$lib/proof.svelte.js';
  import { toast } from '$lib/toast.svelte.js';
  import Ring from '$lib/Ring.svelte';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import ShieldAlert from '@lucide/svelte/icons/shield-alert';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';

  let { onSessionLost, onNavigate } = $props();
  let data = $state(null), error = $state(''), loading = $state(false), fixing = $state('');
  const ORDER = { high: 0, medium: 1, low: 2 };
  const LEVEL = { high: 'High', medium: 'Medium', low: 'Low' };
  const findings = $derived(data ? [...data.findings].sort((a, b) => ORDER[a.level] - ORDER[b.level]) : []);

  async function load() {
    loading = true;
    const res = await api('/api/security/check');
    loading = false;
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { error = res.error; return; }
    error = '';
    data = res.data;
  }

  async function fix(f) {
    fixing = f.key;
    let ok = false, msg = '';
    if (f.fix === 'auto_updates') {
      const job = await runJob('/api/system-updates/auto', { op: 'on' });
      ok = job.status === 'done'; msg = job.error;
    } else {
      const res = await withProof(() => api('/api/security/fix', { method: 'POST', body: { fix: f.fix } }));
      if (res.cancelled) { fixing = ''; return; }
      ok = res.ok; msg = res.error;
    }
    fixing = '';
    if (ok) toast('Fixed.', 'success'); else toast(msg || 'This could not be fixed.', 'error', 9000);
    load();
  }

  onMount(load);
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Security check</h1>
      <p class="t-sub">What Box can see about this server's security, and how to fix it.</p>
    </div>
    <button class="btn btn-gray" onclick={load} disabled={loading}>{#if loading}<Loader size={16} class="spinner" />Checking{:else}<RefreshCw size={16} />Check again{/if}</button>
  </header>

  {#if error}
    <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
  {:else if !data}
    <div class="group">{#each Array(4) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</div>
  {:else}
    <section class="tile score">
      <Ring value={data.score} label="Score" caption={data.score >= 85 ? 'Good' : data.score >= 60 ? 'Could be better' : 'Needs work'} />
      <p class="t-sub">{findings.length ? `${findings.length} ${findings.length === 1 ? 'thing' : 'things'} to look at.` : 'Nothing to fix. Well done.'}</p>
    </section>
    <section class="group">
      {#each findings as f (f.key)}
        <div class="row finding">
          <span class="ico" class:error={f.level === 'high'} class:warn={f.level !== 'high'}><ShieldAlert /></span>
          <span class="grow">
            <span class="t-head">{f.title} <span class="pill">{LEVEL[f.level]}</span></span>
            <span class="t-caption">{f.text}</span>
          </span>
          {#if f.fix}
            <button class="btn btn-tinted btn-sm" onclick={() => fix(f)} disabled={!!fixing}>{#if fixing === f.key}<Loader size={15} class="spinner" />{/if}{f.action}</button>
          {:else if f.link}
            <button class="btn btn-gray btn-sm" onclick={() => onNavigate?.(f.link)}>{f.action}</button>
          {/if}
        </div>
      {:else}
        <div class="row"><span class="ico ok"><ShieldCheck /></span><span>No findings.</span></div>
      {/each}
    </section>
  {/if}
</div>

<style>
  .page { display: grid; gap: 1.25rem; }
  h1, p { margin: 0; }
  .score { display: grid; justify-items: center; gap: 0.75rem; padding: 1.5rem; }
  .grow { flex: 1; min-width: 0; display: grid; gap: 0.125rem; }
  .finding .pill { margin-left: 0.375rem; font-weight: 500; }
  .skeleton.line { display: block; width: 60%; height: 0.8rem; }
</style>
