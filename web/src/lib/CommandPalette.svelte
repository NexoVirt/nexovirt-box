<script>
  // Command palette (Ctrl+K / Cmd+K): windows, settings sections, installed apps with their
  // actions, and files by name. Arrow keys move, Enter runs, Escape closes.
  import { tick } from 'svelte';
  import { api } from './api.js';
  import { runJob, appURL } from './jobs.js';
  import { toast } from './toast.svelte.js';
  import AppIcon from './AppIcon.svelte';
  import Search from '@lucide/svelte/icons/search';
  import AppWindowIcon from '@lucide/svelte/icons/app-window';
  import SettingsIcon from '@lucide/svelte/icons/settings';
  import Play from '@lucide/svelte/icons/play';
  import Square from '@lucide/svelte/icons/square';
  import RotateCw from '@lucide/svelte/icons/rotate-cw';
  import ScrollText from '@lucide/svelte/icons/scroll-text';
  import ExternalLink from '@lucide/svelte/icons/external-link';
  import FileIcon from '@lucide/svelte/icons/file';
  import Folder from '@lucide/svelte/icons/folder';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { open = $bindable(false), onNavigate, onSessionLost } = $props();

  const WINDOWS = [
    ['Apps', '/apps/installed'], ['App Store', '/apps'], ['Files', '/files'], ['Monitor', '/monitor'],
    ['Terminal', '/console'], ['Backups', '/backups'], ['Log search', '/logs'], ['Settings', '/settings'],
  ];
  const SECTIONS = [
    ['General', 'general'], ['Account and security', 'security'], ['Security check', 'security-check'], ['Apps and catalog', 'apps'],
    ['Network and domains', 'network'], ['Firewall', 'firewall'], ['Shares', 'shares'], ['Health', 'health'], ['Disks', 'disks'],
    ['System updates', 'system-updates'], ['Box updates', 'box-updates'], ['Backups settings', 'backups'],
    ['Notifications', 'notifications'], ['Activity', 'activity'], ['About', 'about'],
  ];

  let q = $state(''), apps = $state([]), files = $state([]), finding = $state(false), sel = $state(0), input = $state();
  let timer, seq = 0;

  $effect(() => {
    if (!open) return;
    q = ''; files = []; sel = 0;
    tick().then(() => input?.focus());
    api('/api/apps').then((res) => {
      if (res.status === 401) return onSessionLost?.();
      if (res.ok) apps = res.data;
    });
  });

  function match(text) {
    const s = q.trim().toLowerCase();
    return !s || text.toLowerCase().includes(s);
  }

  async function act(app, op) {
    toast(`${app.name}: ${op}...`, 'info');
    const job = await runJob(`/api/apps/${app.id}/action`, { op });
    if (job.http === 401) return onSessionLost?.();
    if (job.status === 'done') toast(`${app.name}: done.`, 'success');
    else toast(job.error, 'error', 8000);
  }

  const items = $derived.by(() => {
    const out = [];
    for (const [label, path] of WINDOWS) if (match(label)) out.push({ group: 'Windows', label, icon: AppWindowIcon, run: () => onNavigate(path) });
    for (const [label, key] of SECTIONS) if (match(label) || match('settings ' + label)) out.push({ group: 'Settings', label, icon: SettingsIcon, run: () => onNavigate('/settings/' + key) });
    for (const a of apps) {
      if (!match(a.name) && !match(a.id)) continue;
      const running = a.state === 'running';
      const url = appURL(a);
      if (running && url) out.push({ group: 'Apps', label: `Open ${a.name}`, app: a, icon: ExternalLink, run: () => window.open(url, '_blank', 'noopener') });
      if (!running) out.push({ group: 'Apps', label: `Start ${a.name}`, app: a, icon: Play, run: () => act(a, 'start') });
      if (running) out.push({ group: 'Apps', label: `Stop ${a.name}`, app: a, icon: Square, run: () => act(a, 'stop') });
      out.push({ group: 'Apps', label: `Restart ${a.name}`, app: a, icon: RotateCw, run: () => act(a, 'restart') });
      out.push({ group: 'Apps', label: `Logs of ${a.name}`, app: a, icon: ScrollText, run: () => onNavigate(`/apps/${a.id}/logs`) });
    }
    for (const f of files) {
      const dir = f.dir ? f.path : f.path.slice(0, f.path.lastIndexOf('/')) || '/';
      out.push({ group: 'Files', label: f.path.split('/').pop(), hint: f.path, icon: f.dir ? Folder : FileIcon, run: () => onNavigate('/files?path=' + encodeURIComponent(dir)) });
    }
    return out.slice(0, 60);
  });

  function onInput() {
    sel = 0;
    clearTimeout(timer);
    const s = q.trim();
    if (s.length < 2) { files = []; finding = false; return; }
    finding = true;
    const my = ++seq;
    timer = setTimeout(async () => {
      const res = await api('/api/files/find?q=' + encodeURIComponent(s));
      if (my !== seq) return;
      finding = false;
      files = res.ok ? res.data : [];
    }, 300);
  }

  function run(it) {
    if (!it) return;
    open = false;
    it.run();
  }

  function key(e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); sel = Math.min(sel + 1, items.length - 1); scroll(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); sel = Math.max(sel - 1, 0); scroll(); }
    else if (e.key === 'Enter') { e.preventDefault(); run(items[sel]); }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); open = false; }
  }
  function scroll() { tick().then(() => document.querySelector(`.cmdk [data-i="${sel}"]`)?.scrollIntoView({ block: 'nearest' })); }
</script>

{#if open}
  <div class="scrim" onclick={() => (open = false)} aria-hidden="true"></div>
  <div class="cmdk" role="dialog" aria-modal="true" aria-label="Command palette">
    <label class="field-row">
      <Search size={18} />
      <input bind:this={input} bind:value={q} oninput={onInput} onkeydown={key} placeholder="Search apps, windows, settings and files" aria-label="Search" spellcheck="false" autocomplete="off"
        role="combobox" aria-expanded="true" aria-controls="cmdk-list" aria-activedescendant={items.length ? `cmdk-${sel}` : undefined} />
      {#if finding}<Loader size={16} class="spinner" />{/if}
    </label>
    <div class="list" id="cmdk-list" role="listbox">
      {#each items as it, i (it.group + it.label + (it.hint || ''))}
        {#if i === 0 || items[i - 1].group !== it.group}<p class="t-section grp">{it.group}</p>{/if}
        {@const Icon = it.icon}
        <button id="cmdk-{i}" data-i={i} class="item" class:on={i === sel} role="option" aria-selected={i === sel} onpointermove={() => (sel = i)} onclick={() => run(it)}>
          {#if it.app && it.group === 'Apps'}<AppIcon id={it.app.id} name={it.app.name} size={22} icon={it.app.icon} />{:else}<span class="ico"><Icon /></span>{/if}
          <span class="txt"><span class="truncate">{it.label}</span>{#if it.hint}<span class="t-caption truncate">{it.hint}</span>{/if}</span>
        </button>
      {:else}
        <p class="t-sub none">{finding ? 'Searching files' : 'Nothing found.'}</p>
      {/each}
    </div>
  </div>
{/if}

<style>
  .scrim { position: fixed; inset: 0; z-index: 60; background: rgb(0 0 0 / 0.25); }
  .cmdk {
    position: fixed; z-index: 61; left: 50%; top: max(10vh, calc(3.5rem + env(safe-area-inset-top, 0px))); transform: translateX(-50%);
    width: min(40rem, calc(100vw - 1.5rem)); max-height: 70vh; display: flex; flex-direction: column;
    border-radius: 1rem; background: var(--material); box-shadow: var(--shadow-float); overflow: hidden;
    animation: grow 220ms var(--spring);
  }
  .field-row { display: flex; align-items: center; gap: 0.625rem; padding: 0 1rem; min-height: 3.25rem; color: var(--text-2); box-shadow: 0 1px 0 var(--hairline); }
  .field-row input { flex: 1; min-width: 0; border: 0; background: none; font-size: 1.0625rem; color: var(--text); }
  .field-row input:focus-visible { outline: none; }
  .list { overflow-y: auto; padding: 0.375rem; }
  .grp { margin: 0.5rem 0.625rem 0.25rem; }
  .item { display: flex; align-items: center; gap: 0.75rem; width: 100%; min-height: 2.75rem; padding: 0.25rem 0.625rem; border: 0; border-radius: 0.625rem; background: none; color: var(--text); font: inherit; text-align: left; cursor: pointer; }
  .item.on { background: var(--accent); color: var(--on-accent); }
  .item.on :global(.ico), .item.on :global(.t-caption) { color: inherit; }
  .item:active { transform: scale(0.99); }
  .txt { display: grid; min-width: 0; }
  .none { margin: 0; padding: 1rem; }
  @keyframes grow { from { opacity: 0; transform: translateX(-50%) scale(0.97); } }
  @media (prefers-reduced-motion: reduce) { .cmdk { animation: none; } }
</style>
