<script>
  // Apps window: the installed apps and the App Store as two tabs (/apps/installed and /apps), with
  // "Add your own app" and "Check for updates" in the window's toolbar. The App Store loads on first
  // use and both tabs stay mounted, so switching keeps the scroll place and running installs.
  import Segmented from '$lib/Segmented.svelte';
  import Lazy from '$lib/Lazy.svelte';
  import MyApps from './MyApps.svelte';
  import Plus from '@lucide/svelte/icons/plus';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { tab = 'installed', onNavigate, onSessionLost } = $props();

  const TABS = [{ value: 'installed', label: 'Installed' }, { value: 'store', label: 'App Store' }];
  const loadStore = () => import('./AppStore.svelte');
  let mine = $state();
  let checking = $state(false);
  let storeSeen = $state(false);
  $effect(() => { if (tab === 'store') storeSeen = true; });

  const show = (t) => onNavigate(t === 'store' ? '/apps' : '/apps/installed');

  async function check() {
    show('installed');
    checking = true;
    await mine?.checkUpdates();
    checking = false;
  }
  function addOwn(e) {
    show('installed');
    mine?.openOwn(e, null);
  }
</script>

<div class="mgr">
  <div class="toolbar">
    <Segmented options={TABS} value={tab} label="Apps" onchange={show} />
    <div class="acts">
      <button class="btn btn-gray btn-sm" onclick={check} disabled={checking}>
        {#if checking}<Loader size={15} class="spinner" />Checking{:else}<RefreshCw size={15} />Check for updates{/if}
      </button>
      <button class="btn btn-gray btn-sm" onclick={addOwn}><Plus size={15} />Add your own app</button>
    </div>
  </div>
  <div class="pane" hidden={tab !== 'installed'}>
    <MyApps bind:this={mine} {onSessionLost} {onNavigate} />
  </div>
  {#if storeSeen}
    <div class="pane" hidden={tab !== 'store'}>
      <Lazy load={loadStore} {onSessionLost} />
    </div>
  {/if}
</div>

<style>
  .mgr { display: flex; flex-direction: column; flex: 1; min-height: 0; }
  .toolbar {
    flex-shrink: 0; display: flex; align-items: center; justify-content: space-between; gap: 0.75rem; flex-wrap: wrap;
    padding: 0.625rem 1.5rem; box-shadow: 0 1px 0 var(--hairline); position: relative; z-index: 6;
  }
  .acts { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .pane { --pane-top: 1.25rem; flex: 1; min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: var(--pane-top) 1.5rem 1.75rem; }
  @media (max-width: 699px) {
    .toolbar { padding-inline: 1rem; }
    .pane { --pane-top: 1rem; padding: 1rem 1rem calc(1.5rem + env(safe-area-inset-bottom, 0px)); }
  }
</style>
