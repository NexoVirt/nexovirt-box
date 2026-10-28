<script>
  // Asks for the password (and the authenticator code when 2FA is on) for withProof().
  import { api } from './api.js';
  import { gate } from './proof.svelte.js';
  import Dialog from './Dialog.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import Lock from '@lucide/svelte/icons/lock';

  let password = $state(''), code = $state(''), needsCode = $state(false), busy = $state(false), error = $state('');
  let open = $state(false);

  $effect(() => {
    if (gate.open && !open) {
      password = ''; code = ''; error = '';
      open = true;
      api('/api/console/ready').then((r) => { needsCode = !!r.data?.needs_code; });
    }
  });
  $effect(() => { if (!open && gate.open) finish(false); });

  function finish(ok) {
    const r = gate.resolve;
    gate.open = false;
    gate.resolve = null;
    open = false;
    r?.(ok);
  }

  async function submit(e) {
    e.preventDefault();
    busy = true;
    error = '';
    const res = await api('/api/auth/reauth', { method: 'POST', body: { password, code } });
    busy = false;
    if (!res.ok) { error = res.error; return; }
    finish(true);
  }
</script>

<Dialog bind:open label="Confirm your password" locked={busy}>
  <form class="dlg" onsubmit={submit}>
    <span class="ico lg accent"><Lock /></span>
    <h2 class="t-title">Confirm it is you</h2>
    <p class="t-sub">This changes what runs on the server as root. Enter your password to continue.</p>
    {#if error}<div class="notice" role="alert">{error}</div>{/if}
    <input class="input" type="password" bind:value={password} autocomplete="current-password" placeholder="Password" aria-label="Password" />
    {#if needsCode}<input class="input" bind:value={code} inputmode="numeric" autocomplete="one-time-code" placeholder="Code from your authenticator app" aria-label="Authenticator code" />{/if}
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => finish(false)} disabled={busy}>Cancel</button>
      <button class="btn btn-primary" disabled={busy || !password}>{#if busy}<Loader size={16} class="spinner" />Checking{:else}Continue{/if}</button>
    </div>
  </form>
</Dialog>

<style>
  h2, p { margin: 0; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; }
</style>
