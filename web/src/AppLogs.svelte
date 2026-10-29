<script>
  // Logs page of one app (/apps/<id>/logs): fills the window, dark like the console, one line per
  // log line (no wrapping unless asked), live follow that pauses when you scroll up, service
  // filter with compose-style coloured prefixes, search with highlight, error/warn highlighting.
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import Search from '@lucide/svelte/icons/search';
  import Pause from '@lucide/svelte/icons/pause';
  import Play from '@lucide/svelte/icons/play';
  import Download from '@lucide/svelte/icons/download';
  import Copy from '@lucide/svelte/icons/copy';
  import ArrowDown from '@lucide/svelte/icons/arrow-down';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';

  let { id, onSessionLost, onNavigate } = $props();

  const MAX = 10000; // lines kept; older ones are trimmed
  const FIRST = 1000; // lines loaded at the start
  const POLL_MS = 2000;
  // Compose-like prefix colours, readable on the dark log background.
  const PALETTE = ['#60a5fa', '#22d3ee', '#facc15', '#4ade80', '#f472b6', '#c084fc', '#fb923c', '#94a3b8'];
  const ERR = /\b(error|err|fatal|panic|exception|critical|crit|failed|failure)\b/i;
  const WARN = /\b(warn|warning|deprecated)\b/i;

  let name = $state('');
  let services = $state([]);
  let service = $state(''); // '' = all
  let lines = $state([]); // { n, s, t, m }
  let loading = $state(true);
  let error = $state('');
  let paused = $state(false);
  let wrap = $state(false);
  let showTime = $state(false);
  let numbers = $state(false);
  let query = $state('');
  let atBottom = $state(true);
  let box = $state();
  let seq = 0, newest = '', gen = 0;

  try {
    wrap = localStorage.getItem('nxbox-logs-wrap') === '1';
    showTime = localStorage.getItem('nxbox-logs-time') === '1';
    numbers = localStorage.getItem('nxbox-logs-num') === '1';
  } catch {}
  $effect(() => {
    try {
      localStorage.setItem('nxbox-logs-wrap', wrap ? '1' : '0');
      localStorage.setItem('nxbox-logs-time', showTime ? '1' : '0');
      localStorage.setItem('nxbox-logs-num', numbers ? '1' : '0');
    } catch {}
  });

  // Docker trims trailing zeros of the nanoseconds; pad them so times compare as strings.
  const norm = (t) => t.replace(/(?:\.(\d+))?Z$/, (_, f = '') => '.' + f.padEnd(9, '0') + 'Z');
  const colour = (s) => PALETTE[Math.max(0, services.indexOf(s)) % PALETTE.length];
  const level = (m) => (ERR.test(m) ? 'err' : WARN.test(m) ? 'warn' : '');
  let multi = $derived(services.length > 1 && !service);
  let width = $derived(Math.max(0, ...services.map((s) => s.length)));
  let needle = $derived(query.trim().toLowerCase());
  let shown = $derived(needle ? lines.filter((l) => (l.s + ' ' + l.m).toLowerCase().includes(needle)) : lines);

  async function fetchLines(initial) {
    const g = gen;
    const q = new URLSearchParams({ tail: String(initial ? FIRST : MAX) });
    if (!initial && newest) q.set('since', newest);
    if (service) q.set('service', service);
    const res = await api(`/api/apps/${id}/logs?${q}`);
    if (g !== gen) return; // the service changed meanwhile
    loading = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) {
      if (initial) error = res.error;
      return;
    }
    error = '';
    services = res.data.services || [];
    const cut = newest ? norm(newest) : '';
    const add = [];
    for (const l of res.data.lines || []) {
      if (!initial && (!l.t || norm(l.t) <= cut)) continue; // already shown
      add.push({ n: ++seq, s: l.s, t: l.t, m: l.m });
      if (l.t && norm(l.t) > (newest ? norm(newest) : '')) newest = l.t;
    }
    if (!add.length) return;
    const stick = atBottom;
    lines = lines.length + add.length > MAX ? [...lines, ...add].slice(-MAX) : [...lines, ...add];
    if (stick) requestAnimationFrame(toBottom);
  }

  function restart() {
    gen++;
    lines = [];
    seq = 0;
    newest = '';
    loading = true;
    atBottom = true;
    fetchLines(true);
  }

  function toBottom() {
    if (box) box.scrollTop = box.scrollHeight;
    atBottom = true;
  }

  function onScroll() {
    atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 32;
  }

  onMount(() => {
    api('/api/apps').then((r) => { if (r.ok) name = r.data.find((a) => a.id === id)?.name || ''; });
    fetchLines(true);
  });

  // Live follow, unless paused.
  $effect(() => {
    if (paused) return;
    const t = setInterval(() => fetchLines(false), POLL_MS);
    return () => clearInterval(t);
  });

  const text = (ls) => ls.map((l) => [l.t, multi || service ? l.s : '', l.m].filter(Boolean).join('  ')).join('\n') + '\n';

  function download() {
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([text(shown)], { type: 'text/plain' }));
    a.download = `${id}${service ? '-' + service : ''}-logs.txt`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  }

  async function copy() {
    const sel = String(getSelection() || '');
    try {
      await navigator.clipboard.writeText(sel || text(shown));
      toast(sel ? 'Copied the selection.' : `Copied ${shown.length} lines.`, 'success', 2000);
    } catch {
      toast('This browser does not allow copying here. Select the text and copy it by hand.', 'info');
    }
  }

  // Splits text around the search term for highlighting (text nodes only, never HTML).
  function parts(m) {
    if (!needle) return [m];
    const out = [], low = m.toLowerCase();
    let i = 0, j;
    while ((j = low.indexOf(needle, i)) !== -1) {
      out.push(m.slice(i, j), m.slice(j, j + needle.length));
      i = j + needle.length;
    }
    out.push(m.slice(i));
    return out;
  }
  const clock = (t) => (t ? t.replace('T', ' ').replace(/\.\d+Z$|Z$/, '') : '');
</script>

<div class="page fill">
  <header class="page-head compact">
    <div>
      <button class="back" onclick={() => onNavigate('/apps/installed')}><ChevronLeft size={16} />Apps</button>
      <h1 class="t-large">Logs</h1>
      <p class="t-sub">{name || id}</p>
    </div>
  </header>

  <div class="card">
    <div class="toolbar">
      <label class="search">
        <Search size={15} />
        <input type="search" placeholder="Filter lines" bind:value={query} aria-label="Filter lines" spellcheck="false" />
      </label>
      {#if services.length > 1}
        <select class="input svc" bind:value={service} onchange={restart} aria-label="Service">
          <option value="">All services</option>
          {#each services as s (s)}<option value={s}>{s}</option>{/each}
        </select>
      {/if}
      <span class="toggles">
        <label class="tog"><input type="checkbox" bind:checked={wrap} />Wrap lines</label>
        <label class="tog"><input type="checkbox" bind:checked={showTime} />Timestamps</label>
        <label class="tog"><input type="checkbox" bind:checked={numbers} />Line numbers</label>
      </span>
      <span class="acts">
        <button class="ctl" onclick={() => (paused = !paused)} aria-pressed={paused}>
          {#if paused}<Play size={15} />Resume{:else}<Pause size={15} />Pause{/if}
        </button>
        <button class="ctl" onclick={copy} aria-label="Copy the selection, or all shown lines"><Copy size={15} />Copy</button>
        <button class="ctl" onclick={download} aria-label="Download the shown lines"><Download size={15} />Download</button>
      </span>
    </div>

    <div class="view-wrap">
      {#if loading}
        <p class="state"><Loader size={16} class="spinner" />Loading logs</p>
      {:else if error}
        <p class="state err"><CircleAlert size={16} />{error}</p>
      {:else if !lines.length}
        <p class="state">No log output yet.{paused ? '' : ' New lines show up here as they come.'}</p>
      {:else if !shown.length}
        <p class="state">No line contains "{query}".</p>
      {/if}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (keyboard scrolling of the log) -->
      <div class="view mono" class:wrap bind:this={box} onscroll={onScroll} role="log" aria-label="Logs of {name || id}" tabindex="0">
        {#each shown as l (l.n)}
          <div class="ln {level(l.m)}">{#if numbers}<span class="no">{l.n}</span>{/if}{#if showTime && l.t}<span class="ts">{clock(l.t)}</span>{/if}{#if multi}<span class="svc-p" style:color={colour(l.s)}>{l.s.padEnd(width)} |</span>{/if}<span class="msg">{#each parts(l.m) as p, i (i)}{#if i % 2}<mark>{p}</mark>{:else}{p}{/if}{/each}</span></div>
        {/each}
      </div>
      {#if !atBottom && lines.length}
        <button class="jump" onclick={toBottom}><ArrowDown size={15} />Jump to latest</button>
      {/if}
    </div>
    <div class="foot t-caption">
      {#if paused}<span class="dot"></span>Paused{:else}<span class="dot live"></span>Live{/if}
      <span>{needle ? `${shown.length} of ${lines.length}` : lines.length} lines{lines.length >= MAX ? ', older lines trimmed' : ''}</span>
    </div>
  </div>
</div>

<style>
  .back {
    display: inline-flex; align-items: center; gap: 0.125rem; margin: 0 0 0.25rem -0.25rem; padding: 0.25rem; min-height: 2rem;
    border: 0; background: none; color: var(--accent); font-weight: 600; font-size: 0.8125rem; cursor: pointer;
  }
  /* Always dark, like the console. */
  .card {
    --log-bg: #18181b; --log-text: #e4e4e7; --log-dim: #71717a; --log-line: rgba(255, 255, 255, 0.06);
    flex: 1; min-height: 0; display: flex; flex-direction: column; overflow: hidden;
    border-radius: var(--r-tile); background: var(--log-bg); color: var(--log-text);
    box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.06), var(--shadow-tile); color-scheme: dark;
  }
  .toolbar, .foot { flex-shrink: 0; }
  .toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem 0.75rem; padding: 0.625rem 0.75rem; border-bottom: 1px solid var(--log-line); }
  .search {
    display: flex; align-items: center; gap: 0.5rem; flex: 1 1 12rem; min-width: 0; max-width: 22rem; height: 2.25rem;
    padding: 0 0.75rem; border-radius: 0.625rem; background: rgba(255, 255, 255, 0.08); color: var(--log-dim);
  }
  .search:focus-within { box-shadow: 0 0 0 2px #60a5fa; }
  .search input { flex: 1; min-width: 0; border: 0; background: none; color: var(--log-text); font-size: 0.8125rem; outline: none; height: 100%; }
  .svc { width: auto; height: 2.25rem; min-height: 2.25rem; background-color: rgba(255, 255, 255, 0.08); color: var(--log-text); border: 0; font-size: 0.8125rem; }
  .toggles { display: flex; flex-wrap: wrap; gap: 0.25rem 0.875rem; }
  .tog { display: inline-flex; align-items: center; gap: 0.375rem; font-size: 0.8125rem; color: #a1a1aa; cursor: pointer; min-height: 2.25rem; }
  .tog input { accent-color: #60a5fa; }
  .acts { display: flex; gap: 0.375rem; margin-left: auto; }
  .ctl {
    display: inline-flex; align-items: center; gap: 0.375rem; height: 2.25rem; padding: 0 0.75rem; border: 0; border-radius: 0.625rem;
    background: rgba(255, 255, 255, 0.08); color: var(--log-text); font-size: 0.8125rem; font-weight: 600; cursor: pointer;
    transition: transform 100ms ease-out, background-color 150ms ease;
  }
  .ctl:hover { background: rgba(255, 255, 255, 0.14); }
  .ctl:active { transform: scale(0.97); }
  .ctl:focus-visible, .view:focus-visible, .jump:focus-visible { outline: 2px solid #60a5fa; outline-offset: -2px; }
  .view-wrap { position: relative; flex: 1; min-height: 0; display: flex; }
  .view {
    flex: 1; min-width: 0; overflow: auto; padding: 0.625rem 0; font-size: 0.75rem; line-height: 1.55;
    white-space: pre; overscroll-behavior: contain;
  }
  .view.wrap { white-space: pre-wrap; overflow-wrap: anywhere; }
  .ln { padding: 0 1rem; width: max-content; min-width: 100%; }
  .view.wrap .ln { width: auto; }
  .ln:hover { background: rgba(255, 255, 255, 0.04); }
  .ln.err .msg { color: #fca5a5; }
  .ln.warn .msg { color: #fcd34d; }
  .no { display: inline-block; min-width: 4ch; margin-right: 1.25ch; text-align: right; color: var(--log-dim); user-select: none; }
  .ts { margin-right: 1.25ch; color: var(--log-dim); }
  .svc-p { margin-right: 1ch; white-space: pre; }
  mark { background: rgba(250, 204, 21, 0.35); color: inherit; border-radius: 2px; }
  .state { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; gap: 0.5rem; margin: 0; color: #a1a1aa; pointer-events: none; text-align: center; padding: 1rem; }
  .state.err { color: #fca5a5; }
  .jump {
    position: absolute; right: 1rem; bottom: 1rem; display: inline-flex; align-items: center; gap: 0.375rem;
    height: 2.25rem; padding: 0 0.875rem; border: 0; border-radius: var(--r-pill); cursor: pointer;
    background: #60a5fa; color: #09090b; font-weight: 600; font-size: 0.8125rem; box-shadow: 0 6px 20px rgba(0, 0, 0, 0.4);
    transition: transform 100ms ease-out;
  }
  .jump:active { transform: scale(0.96); }
  .foot { display: flex; align-items: center; gap: 0.5rem; padding: 0.5rem 1rem; border-top: 1px solid var(--log-line); color: #a1a1aa; }
  .foot span + span { margin-left: 0.5rem; }
  .dot { display: inline-block; width: 0.5rem; height: 0.5rem; border-radius: 50%; background: var(--log-dim); }
  .dot.live { background: #22c55e; }
  @media (max-width: 639px) {
    .acts { margin-left: 0; }
    .search { max-width: none; flex-basis: 100%; }
  }
</style>
