<script>
  // Log search: every app's logs and Box's own (web service and helper) in one list, with text
  // search, a source and level filter and live tail (polls with since=, pauses when scrolled up).
  import { onMount, tick } from 'svelte';
  import { api } from '$lib/api.js';
  import Search from '@lucide/svelte/icons/search';
  import Pause from '@lucide/svelte/icons/pause';
  import Play from '@lucide/svelte/icons/play';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';

  let { onSessionLost } = $props();
  const MAX = 5000, POLL_MS = 3000;
  let lines = $state([]), apps = $state([]), app = $state(''), level = $state(''), query = $state(''), live = $state(true);
  let loading = $state(true), error = $state(''), box = $state(), atBottom = $state(true);
  let newest = '', gen = 0, qTimer;

  const norm = (t) => (t || '').replace(/(?:\.(\d+))?Z$/, (_, f = '') => '.' + f.padEnd(9, '0') + 'Z');
  const timeFmt = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' });

  function params(since) {
    const p = new URLSearchParams();
    if (app) p.set('app', app);
    if (level) p.set('level', level);
    if (query.trim()) p.set('q', query.trim());
    if (since) p.set('since', since);
    return p.toString();
  }

  async function reload() {
    const my = ++gen;
    loading = true;
    const res = await api('/api/logs?' + params(''));
    if (my !== gen) return;
    loading = false;
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { error = res.error; return; }
    error = '';
    lines = res.data.lines;
    apps = res.data.apps || [];
    newest = lines.length ? norm(lines.at(-1).t) : '';
    await tick();
    box?.scrollTo(0, box.scrollHeight);
  }

  async function poll() {
    if (!live || loading || document.hidden || !atBottom || !newest) return;
    const my = gen;
    const res = await api('/api/logs?' + params(newest));
    if (my !== gen || !res.ok) return;
    const fresh = res.data.lines.filter((l) => norm(l.t) > newest);
    if (!fresh.length) return;
    newest = norm(fresh.at(-1).t);
    lines = [...lines, ...fresh].slice(-MAX);
    await tick();
    box?.scrollTo(0, box.scrollHeight);
  }

  function onQuery() { clearTimeout(qTimer); qTimer = setTimeout(reload, 400); }
  function onScroll() { atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40; }

  onMount(() => {
    reload();
    const t = setInterval(poll, POLL_MS);
    return () => clearInterval(t);
  });
</script>

<div class="page fill">
  <header class="page-head compact">
    <div>
      <h1 class="t-large">Log search</h1>
      <p class="t-sub">The logs of every app and of Box in one place.</p>
    </div>
  </header>
  <div class="card">
    <div class="toolbar">
      <label class="search"><Search size={15} /><input bind:value={query} oninput={onQuery} placeholder="Search the logs" aria-label="Search the logs" spellcheck="false" /></label>
      <select class="input svc" bind:value={app} onchange={reload} aria-label="Source">
        <option value="">All sources</option>
        <option value="box">NexoVirt Box</option>
        {#each apps as a (a)}<option value={a}>{a}</option>{/each}
      </select>
      <select class="input svc" bind:value={level} onchange={reload} aria-label="Level">
        <option value="">All levels</option>
        <option value="info">Info and worse</option>
        <option value="warn">Warnings and errors</option>
        <option value="error">Errors only</option>
      </select>
      <div class="acts">
        <button class="ctl" onclick={() => (live = !live)} aria-pressed={live}>{#if live}<Pause size={15} />Pause{:else}<Play size={15} />Live{/if}</button>
      </div>
    </div>
    <div class="view" bind:this={box} onscroll={onScroll} role="log" aria-live="off">
      {#if loading && !lines.length}
        <p class="empty"><Loader size={16} class="spinner" /> Reading the logs</p>
      {:else if error}
        <p class="empty"><CircleAlert size={16} /> {error}</p>
      {:else}
        {#each lines as l, i (i)}
          <div class="ln {l.l}"><span class="ts">{l.t ? timeFmt.format(new Date(l.t)) : ''}</span><span class="src">{l.a}{l.s && l.s !== l.a ? '/' + l.s : ''}</span><span class="msg">{l.m}</span></div>
        {:else}
          <p class="empty">No lines match.</p>
        {/each}
      {/if}
    </div>
  </div>
</div>

<style>
  .card {
    --log-bg: #18181b; --log-text: #e4e4e7; --log-dim: #71717a; --log-line: rgba(255, 255, 255, 0.06);
    flex: 1; min-height: 0; display: flex; flex-direction: column; overflow: hidden;
    border-radius: var(--r-tile); background: var(--log-bg); color: var(--log-text); color-scheme: dark;
    box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.06), var(--shadow-tile);
  }
  .toolbar { flex-shrink: 0; display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem 0.75rem; padding: 0.625rem 0.75rem; border-bottom: 1px solid var(--log-line); }
  .search { display: flex; align-items: center; gap: 0.5rem; flex: 1 1 12rem; min-width: 0; max-width: 22rem; height: 2.25rem; padding: 0 0.75rem; border-radius: 0.625rem; background: rgba(255, 255, 255, 0.08); color: var(--log-dim); }
  .search:focus-within { box-shadow: 0 0 0 2px #60a5fa; }
  .search input { flex: 1; min-width: 0; border: 0; background: none; color: var(--log-text); font-size: 0.8125rem; outline: none; height: 100%; }
  .svc { width: auto; height: 2.25rem; min-height: 2.25rem; background-color: rgba(255, 255, 255, 0.08); color: var(--log-text); border: 0; font-size: 0.8125rem; }
  .acts { margin-left: auto; }
  .ctl { display: inline-flex; align-items: center; gap: 0.375rem; height: 2.25rem; padding: 0 0.75rem; border: 0; border-radius: 0.625rem; background: rgba(255, 255, 255, 0.08); color: var(--log-text); font-size: 0.8125rem; font-weight: 600; cursor: pointer; }
  .ctl:active { transform: scale(0.97); }
  .view { flex: 1; min-height: 0; overflow: auto; padding: 0.625rem 0; font-family: var(--mono, ui-monospace, monospace); font-size: 0.75rem; line-height: 1.55; overscroll-behavior: contain; }
  .ln { display: flex; gap: 0.75rem; padding: 0 1rem; white-space: pre-wrap; overflow-wrap: anywhere; }
  .ts { flex-shrink: 0; color: var(--log-dim); }
  .src { flex-shrink: 0; color: #60a5fa; min-width: 7rem; }
  .msg { min-width: 0; }
  .ln.error { background: rgba(239, 68, 68, 0.14); }
  .ln.warn { background: rgba(250, 204, 21, 0.1); }
  .ln.debug .msg { color: var(--log-dim); }
  .empty { display: flex; align-items: center; gap: 0.5rem; margin: 0; padding: 1rem; color: var(--log-dim); }
</style>
