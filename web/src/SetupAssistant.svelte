<script>
  // First sign-in: what is this box for? Suggests stacks and apps from the catalog and installs
  // the chosen ones. Shown once (desktop pref "onboarded"), skippable.
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { toast } from '$lib/toast.svelte.js';
  import { saveDesktop } from '$lib/desktop.svelte.js';
  import Dialog from '$lib/Dialog.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import AppIcon from '$lib/AppIcon.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Clapperboard from '@lucide/svelte/icons/clapperboard';
  import Images from '@lucide/svelte/icons/images';
  import Cloud from '@lucide/svelte/icons/cloud';
  import ArchiveRestore from '@lucide/svelte/icons/archive-restore';
  import House from '@lucide/svelte/icons/house';
  import Code from '@lucide/svelte/icons/code';
  import LayoutDashboard from '@lucide/svelte/icons/layout-dashboard';

  let { open = $bindable(false), onNavigate, onSessionLost } = $props();

  // Each purpose suggests stacks (signed in the catalog) and single apps.
  const PURPOSES = [
    { key: 'media', label: 'Films and series', icon: Clapperboard, stacks: ['media'], apps: [] },
    { key: 'photos', label: 'Photos', icon: Images, stacks: [], apps: ['immich'] },
    { key: 'cloud', label: 'Files and personal cloud', icon: Cloud, stacks: ['personal-cloud'], apps: ['syncthing'] },
    { key: 'backups', label: 'Backups of my devices', icon: ArchiveRestore, stacks: [], apps: ['duplicati', 'syncthing'] },
    { key: 'home', label: 'Smart home', icon: House, stacks: ['smart-home'], apps: [] },
    { key: 'dev', label: 'Development', icon: Code, stacks: ['development'], apps: [] },
    { key: 'dash', label: 'A start page for everything', icon: LayoutDashboard, stacks: [], apps: ['homepage'] },
  ];

  let step = $state('pick'), picked = $state([]), catalog = $state(null), installed = $state(new Set());
  let chosen = $state([]), busy = $state(false), lines = $state([]), errors = $state([]), notes = $state([]);

  $effect(() => { if (open) { step = 'pick'; picked = []; errors = []; notes = []; } });

  async function suggest() {
    const [c, i] = await Promise.all([api('/api/catalog'), api('/api/apps')]);
    if (c.status === 401) return onSessionLost?.();
    catalog = c.ok ? c.data : { apps: [], stacks: [] };
    installed = new Set(i.ok ? i.data.map((a) => a.id) : []);
    chosen = suggestions.filter((s) => !s.done).map((s) => s.id);
    step = 'suggest';
  }

  const suggestions = $derived.by(() => {
    if (!catalog) return [];
    const out = [], seen = new Set();
    for (const p of PURPOSES.filter((p) => picked.includes(p.key))) {
      for (const id of p.stacks) {
        const st = catalog.stacks?.find((s) => s.id === id);
        if (st && !seen.has('s:' + id)) { seen.add('s:' + id); out.push({ id: 's:' + id, kind: 'stack', name: st.name, text: st.description, icons: st.apps.map((a) => a.id), done: st.apps.every((a) => installed.has(a.id)) }); }
      }
      for (const id of p.apps) {
        const a = catalog.apps.find((x) => x.id === id);
        if (a && a.supported !== false && !seen.has('a:' + id)) { seen.add('a:' + id); out.push({ id: 'a:' + id, kind: 'app', name: a.name, text: a.tagline, icons: [id], done: installed.has(id) }); }
      }
    }
    return out;
  });

  async function install() {
    busy = true; errors = []; notes = []; lines = [];
    for (const s of suggestions.filter((s) => chosen.includes(s.id))) {
      const [kind, id] = s.id.split(':');
      const url = kind === 's' ? `/api/stacks/${id}/install` : `/api/apps/${id}/install`;
      lines = [...lines, `Installing ${s.name}`];
      const job = await runJob(url, kind === 's' ? {} : { answers: {} }, (j) => (lines = [...lines.slice(0, -1), j.lines.at(-1) || `Installing ${s.name}`]));
      if (job.http === 401) { busy = false; return onSessionLost?.(); }
      if (job.status !== 'done') errors = [...errors, `${s.name}: ${job.error}`];
      else if (job.result?.notes) notes = [...notes, { name: s.name, text: job.result.notes }];
    }
    busy = false;
    await finish(false);
    toast(errors.length ? 'Some apps could not be installed. See Apps for details.' : 'Your apps are installed.', errors.length ? 'error' : 'success', 8000);
    if (!errors.length && !notes.length) open = false;
    else step = 'done';
  }

  async function finish(close = true) {
    await saveDesktop({ onboarded: true });
    if (close) open = false;
  }
</script>

<Dialog bind:open label="Set up your box" locked={busy} wide>
  <div class="dlg">
    {#if step === 'pick'}
      <h2 class="t-title">What do you want to use this box for?</h2>
      <p class="t-sub">Pick one or more. Box suggests matching apps; you can always add more in the App Store.</p>
      <div class="purposes">
        {#each PURPOSES as p (p.key)}
          {@const Icon = p.icon}
          <button class="purpose" class:on={picked.includes(p.key)} aria-pressed={picked.includes(p.key)} onclick={() => (picked = picked.includes(p.key) ? picked.filter((k) => k !== p.key) : [...picked, p.key])}>
            <span class="ico md" class:accent={picked.includes(p.key)}><Icon /></span><span>{p.label}</span>
          </button>
        {/each}
      </div>
      <div class="buttons">
        <button class="btn btn-gray" onclick={() => finish()}>Skip</button>
        <button class="btn btn-primary" onclick={suggest} disabled={!picked.length}>Show suggestions</button>
      </div>
    {:else if step === 'suggest'}
      <h2 class="t-title">Suggested for you</h2>
      <p class="t-sub">Untick what you do not want. Box installs the rest one after another.</p>
      <div class="group">
        {#each suggestions as s (s.id)}
          <label class="row sug">
            <span class="icons">{#each s.icons.slice(0, 4) as id (id)}<AppIcon {id} size={28} />{/each}</span>
            <span class="grow"><span class="t-head">{s.name}{s.kind === 'stack' ? ' (stack)' : ''}</span><span class="t-caption">{s.done ? 'Already installed' : s.text}</span></span>
            <input class="switch" type="checkbox" disabled={s.done || busy} checked={chosen.includes(s.id)} onchange={(e) => (chosen = e.currentTarget.checked ? [...chosen, s.id] : chosen.filter((x) => x !== s.id))} />
          </label>
        {:else}
          <div class="row t-sub">Nothing to suggest for this on this server.</div>
        {/each}
      </div>
      {#if busy}<JobLog {lines} />{/if}
      <div class="buttons">
        <button class="btn btn-gray" onclick={() => (step = 'pick')} disabled={busy}>Back</button>
        <button class="btn btn-gray" onclick={() => finish()} disabled={busy}>Not now</button>
        <button class="btn btn-primary" onclick={install} disabled={busy || !chosen.length}>{#if busy}<Loader size={16} class="spinner" />Installing{:else}Install {chosen.length}{/if}</button>
      </div>
    {:else}
      <h2 class="t-title">Done</h2>
      {#each errors as e}<div class="notice">{e}</div>{/each}
      {#each notes as n}<section class="group"><div class="row t-head">{n.name}</div><div class="row note">{n.text}</div></section>{/each}
      <div class="buttons"><button class="btn btn-gray" onclick={() => { open = false; onNavigate?.('/apps/installed'); }}>Open Apps</button><button class="btn btn-primary" data-close onclick={() => (open = false)}>Close</button></div>
    {/if}
  </div>
</Dialog>

<style>
  h2, p { margin: 0; }
  .dlg { display: grid; gap: 1rem; padding: 1.5rem; }
  .purposes { display: grid; grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr)); gap: 0.625rem; }
  .purpose {
    display: flex; align-items: center; gap: 0.75rem; min-height: 3.25rem; padding: 0.625rem 0.875rem; border: 0; border-radius: var(--r-control);
    background: var(--fill); color: var(--text); font: inherit; font-weight: 500; text-align: left; cursor: pointer; transition: transform 100ms ease-out, box-shadow 150ms ease;
  }
  .purpose:active { transform: scale(0.97); }
  .purpose.on { box-shadow: inset 0 0 0 2px var(--accent); }
  .sug { cursor: pointer; }
  .icons { display: flex; gap: 0.25rem; }
  .grow { flex: 1; min-width: 0; display: grid; }
  .note { white-space: pre-wrap; font-size: 0.8125rem; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; flex-wrap: wrap; }
</style>
