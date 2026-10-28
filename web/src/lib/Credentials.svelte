<script>
  // Login details of an installed app: its answers and generated passwords, so the admin can
  // sign in to it.
  import { api } from './api.js';
  import Secret from './Secret.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { id, onSessionLost = () => {} } = $props();
  let items = $state(null);
  let error = $state('');

  $effect(() => {
    items = null;
    error = '';
    api(`/api/apps/${id}/credentials`).then((res) => {
      if (res.status === 401) return onSessionLost();
      if (!res.ok) error = res.error;
      else items = res.data;
    });
  });
</script>

{#if error}
  <p class="t-sub">{error}</p>
{:else if !items}
  <p class="t-sub busy"><Loader size={16} class="spinner" />Loading login details</p>
{:else if !items.length}
  <p class="t-sub">This app has no password of its own. It asks you to create an account the first time you open it.</p>
{:else}
  <div class="group creds">
    {#each items as c (c.label)}
      <div class="row">
        <span class="row-label">{c.label}</span>
        <span class="row-value"><Secret value={c.value} label={c.label} secret={c.secret} /></span>
      </div>
    {/each}
  </div>
{/if}

<style>
  .busy { display: flex; align-items: center; gap: 0.5rem; }
  .creds { box-shadow: none; background: var(--surface-2); }
  :global(.dark) .creds { background: var(--fill); }
  .creds .row { gap: 1rem; }
  .creds .row-value { min-width: 0; display: flex; justify-content: flex-end; }
</style>
