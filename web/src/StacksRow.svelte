<script>
  // App stacks in the App Store: several apps installed together (signed in the catalog).
  import { runJob } from '$lib/jobs.js';
  import { toast } from '$lib/toast.svelte.js';
  import AppIcon from '$lib/AppIcon.svelte';
  import Dialog from '$lib/Dialog.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import RichText from '$lib/RichText.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { stacks = [], apps = [], installed = new Set(), onInstalled, onSessionLost } = $props();
  let sel = $state(null), open = $state(false), origin = $state(null), busy = $state(false), lines = $state([]), error = $state(''), done = $state(false);
  const nameOf = (id) => apps.find((a) => a.id === id)?.name || id;

  function pick(e, s) {
    origin = { x: e.clientX, y: e.clientY };
    sel = s; busy = false; lines = []; error = ''; done = false;
    open = true;
  }

  async function install() {
    busy = true; error = '';
    const job = await runJob(`/api/stacks/${sel.id}/install`, {}, (j) => (lines = j.lines));
    busy = false;
    if (job.http === 401) return onSessionLost?.();
    onInstalled?.();
    if (job.status !== 'done') { error = job.error; return; }
    done = true;
    toast(`${sel.name} is installed.`, 'success');
  }
</script>

{#if stacks.length}
  <section class="stacks">
    <h2 class="t-section">Stacks</h2>
    <div class="stack-grid">
      {#each stacks as s (s.id)}
        <button class="card stack" onclick={(e) => pick(e, s)}>
          <span class="icons">{#each s.apps.slice(0, 4) as a (a.id)}<AppIcon id={a.id} name={nameOf(a.id)} size={34} />{/each}</span>
          <span class="t-head">{s.name}</span>
          <span class="t-caption desc">{s.description}</span>
          <span class="t-caption">{s.apps.length} apps{s.apps.every((a) => installed.has(a.id)) ? ', all installed' : ''}</span>
        </button>
      {/each}
    </div>
  </section>
{/if}

<Dialog bind:open label={sel?.name || 'Stack'} {origin} locked={busy} wide>
  {#if sel}
    <div class="dlg">
      <h2 class="t-title">{sel.name}</h2>
      <p class="t-sub">{sel.description}</p>
      <div class="group">
        {#each sel.apps as a (a.id)}
          <div class="row-item"><AppIcon id={a.id} name={nameOf(a.id)} size={28} /><span class="grow">{nameOf(a.id)}</span>{#if installed.has(a.id)}<span class="t-caption">Installed</span>{/if}</div>
        {/each}
      </div>
      {#if error}<div class="notice" role="alert">{error}</div>{/if}
      {#if busy || lines.length}<JobLog {lines} />{/if}
      {#if done && sel.notes}<section class="notes"><h3 class="t-section">How they work together</h3><RichText text={sel.notes} /></section>{/if}
      <div class="buttons">
        <button class="btn btn-gray" data-close onclick={() => (open = false)} disabled={busy}>{done ? 'Close' : 'Cancel'}</button>
        {#if !done}<button class="btn btn-primary" onclick={install} disabled={busy}>{#if busy}<Loader size={16} class="spinner" />Installing{:else}Install all{/if}</button>{/if}
      </div>
    </div>
  {/if}
</Dialog>

<style>
  h2, h3, p { margin: 0; }
  .stacks { display: grid; gap: 0.625rem; }
  .stack-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(17rem, 1fr)); gap: 1rem; }
  .stack {
    display: grid; gap: 0.375rem; text-align: left; padding: 1rem; border: 0; cursor: pointer; color: var(--text); font: inherit;
    background: var(--surface-2); border-radius: var(--r-tile); box-shadow: 0 0 0 1px var(--hairline); transition: transform 100ms ease-out, background-color 150ms ease;
  }
  .stack:hover { background: var(--fill); }
  .stack:active { transform: scale(0.98); }
  .icons { display: flex; gap: 0.375rem; margin-bottom: 0.25rem; }
  .desc { display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .row-item { display: flex; align-items: center; gap: 0.75rem; padding: 0.5rem 0.875rem; }
  .grow { flex: 1; }
  .notes { display: grid; gap: 0.5rem; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; }
</style>
