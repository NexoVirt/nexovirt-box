<script>
  // Login details of an installed app: its answers and generated passwords, first-login details
  // from the catalog or the app's first log lines, and the catalog's getting started note.
  import { api } from './api.js';
  import Secret from './Secret.svelte';
  import RichText from './RichText.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { id, onSessionLost = () => {} } = $props();
  let data = $state(null);
  let error = $state('');

  $effect(() => {
    data = null;
    error = '';
    api(`/api/apps/${id}/credentials`).then((res) => {
      if (res.status === 401) return onSessionLost();
      if (!res.ok) error = res.error;
      else data = Array.isArray(res.data) ? { items: res.data } : res.data;
    });
  });
  let fromLog = $derived(data?.items?.some((c) => c.source === 'log'));
</script>

{#if error}
  <p class="t-sub">{error}</p>
{:else if !data}
  <p class="t-sub busy"><Loader size={16} class="spinner" />Loading login details</p>
{:else}
  <div class="creds-wrap">
    {#if data.items.length}
      <div class="group creds">
        {#each data.items as c (c.label + c.value)}
          <div class="row">
            <span class="row-label">{c.label}{#if c.source === 'log'}<span class="pill src">From the logs</span>{/if}</span>
            <span class="row-value"><Secret value={c.value} label={c.label} secret={c.secret} /></span>
          </div>
        {/each}
      </div>
      {#if fromLog}<p class="t-caption">Read from the app's first log lines. If you changed the password since, use your own.</p>{/if}
    {:else if !data.note}
      <p class="t-sub">Box did not set a password for this app and found none in its logs. Most such apps ask you to create an account the first time you open them.</p>
    {/if}
    {#if data.note}
      <section class="note"><h3 class="t-section">First sign-in</h3><RichText text={data.note} /></section>
    {/if}
    {#if data.notes}
      <section class="note"><h3 class="t-section">Getting started</h3><RichText text={data.notes} /></section>
    {/if}
  </div>
{/if}

<style>
  .busy { display: flex; align-items: center; gap: 0.5rem; }
  .creds-wrap { display: grid; gap: 0.75rem; }
  .creds { box-shadow: none; background: var(--surface-2); }
  :global(.dark) .creds { background: var(--fill); }
  .creds .row { gap: 1rem; }
  .creds .row-label { display: inline-flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
  .creds .row-value { min-width: 0; display: flex; justify-content: flex-end; }
  .src { font-size: 0.6875rem; }
  .note { display: grid; gap: 0.375rem; }
  .note h3, p { margin: 0; }
</style>
