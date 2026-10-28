<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { ago } from '$lib/format.js';
  import LogIn from '@lucide/svelte/icons/log-in';
  import LogOut from '@lucide/svelte/icons/log-out';
  import ShieldAlert from '@lucide/svelte/icons/shield-alert';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import SquareTerminal from '@lucide/svelte/icons/square-terminal';
  import Package from '@lucide/svelte/icons/package';
  import Container from '@lucide/svelte/icons/container';
  import Activity from '@lucide/svelte/icons/activity';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';

  let { onSessionLost } = $props();

  // Readable text per audit action; unknown actions show as they are.
  const LABELS = {
    'login.ok': ['Signed in', LogIn],
    'login.failed': ['Sign-in failed', ShieldAlert, 'error'],
    'logout': ['Signed out', LogOut],
    'reauth.ok': ['Confirmed password', ShieldCheck],
    'reauth.failed': ['Wrong password at confirmation', ShieldAlert, 'error'],
    'console.open': ['Opened the console', SquareTerminal],
    'console.close': ['Closed the console', SquareTerminal],
    'docker.install': ['Installed Docker', Container],
    'catalog.sync': ['Synced the app catalog', Package],
  };

  let items = $state(null);
  let error = $state('');

  function label(a) {
    if (LABELS[a]) return LABELS[a];
    if (a.startsWith('app.')) return [a.slice(4).replace('.', ' ').replace(/^\w/, (c) => c.toUpperCase()), Package];
    return [a, Activity];
  }

  async function load() {
    error = '';
    const res = await api('/api/activity');
    if (res.status === 401) return onSessionLost();
    if (!res.ok) error = res.error;
    else items = res.data.items;
  }

  onMount(load);
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Activity</h1>
      <p class="t-sub">Sign-ins, console sessions and app changes on this Box.</p>
    </div>
    <button class="btn btn-gray btn-sm" onclick={load}><RefreshCw size={15} />Refresh</button>
  </header>

  {#if error}
    <div class="notice">{error}</div>
  {:else if !items}
    <div class="group">{#each Array(6) as _}<div class="row"><span class="skeleton line"></span></div>{/each}</div>
  {:else if !items.length}
    <div class="group"><div class="row t-sub">Nothing happened yet.</div></div>
  {:else}
    <div class="group">
      {#each items as it, i (i)}
        {@const [text, Icon, tone] = label(it.action)}
        <div class="row">
          <span class="ico" class:error={tone === 'error'}><Icon size={16} /></span>
          <div class="main">
            <span class="t-head">{text}{#if it.detail}<span class="t-sub detail">{'  ·  ' + it.detail}</span>{/if}</span>
            <span class="t-caption">{it.username || 'Unknown user'} from {it.ip}</span>
          </div>
          <span class="t-caption when" title={new Date(it.at * 1000).toLocaleString()}>{ago(it.at)}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .page { display: grid; gap: 1.5rem; }
  .page-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; flex-wrap: wrap; padding-top: 0.5rem; }
  h1, p { margin: 0; }
  .page-head p { margin-top: 0.375rem; }
  .skeleton.line { display: block; width: 60%; height: 0.8rem; }
  .ico {
    display: grid; place-items: center; width: 1.875rem; height: 1.875rem; border-radius: 0.5rem; flex-shrink: 0;
    background: var(--accent-fill); color: var(--accent);
  }
  .ico.error { background: var(--error-fill); color: var(--error); }
  .main { display: grid; min-width: 0; flex: 1; }
  .detail { font-weight: 400; }
  .when { white-space: nowrap; }
</style>
