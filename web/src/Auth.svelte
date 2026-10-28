<script>
  import { api } from './lib/api.js';
  import Logo from './lib/Logo.svelte';
  import Icon from './lib/Icon.svelte';

  // mode: 'setup' (first start) or 'login'
  let { mode, onDone } = $props();

  let token = $state('');
  let username = $state('');
  let password = $state('');
  let password2 = $state('');
  let busy = $state(false);
  let error = $state('');

  async function submit(e) {
    e.preventDefault();
    error = '';
    if (mode === 'setup' && password !== password2) {
      error = 'The two passwords do not match.';
      return;
    }
    busy = true;
    const res = mode === 'setup'
      ? await api('/api/setup', { method: 'POST', body: { token, username, password } })
      : await api('/api/login', { method: 'POST', body: { username, password } });
    busy = false;
    if (!res.ok) {
      error = res.error;
      return;
    }
    password = password2 = '';
    onDone();
  }
</script>

<div class="page page-center">
  <div class="container container-tight py-4">
    <div class="text-center mb-4 d-flex align-items-center justify-content-center gap-2">
      <Logo size={40} />
      <span class="h2 mb-0">NexoVirt Box</span>
    </div>
    <div class="card card-md">
      <div class="card-body">
        {#if mode === 'setup'}
          <h2 class="h2 text-center mb-2">Set up this Box</h2>
          <p class="text-secondary text-center mb-4">Create the admin account. You need the setup code that was shown after the install.</p>
        {:else}
          <h2 class="h2 text-center mb-4">Sign in</h2>
        {/if}

        {#if error}
          <div class="alert alert-danger" role="alert">
            <div class="d-flex gap-2">
              <Icon name="alert-triangle" class="icon alert-icon" />
              <div class="flex-fill">{error}</div>
            </div>
          </div>
        {/if}

        <form onsubmit={submit} autocomplete="on">
          {#if mode === 'setup'}
            <div class="mb-3">
              <label class="form-label" for="token">Setup code</label>
              <input id="token" class="form-control font-monospace" bind:value={token} required autocomplete="off" spellcheck="false" />
              <small class="form-hint">Run <code>cat /var/lib/nexovirt-box/setup-token</code> on the server if you missed it.</small>
            </div>
          {/if}
          <div class="mb-3">
            <label class="form-label" for="username">Username</label>
            <input id="username" class="form-control" bind:value={username} required autocomplete="username" autocapitalize="none" spellcheck="false" />
          </div>
          <div class="mb-3">
            <label class="form-label" for="password">Password</label>
            <input id="password" type="password" class="form-control" bind:value={password} required
              minlength={mode === 'setup' ? 10 : undefined} autocomplete={mode === 'setup' ? 'new-password' : 'current-password'} />
            {#if mode === 'setup'}<small class="form-hint">At least 10 characters.</small>{/if}
          </div>
          {#if mode === 'setup'}
            <div class="mb-3">
              <label class="form-label" for="password2">Repeat password</label>
              <input id="password2" type="password" class="form-control" bind:value={password2} required minlength="10" autocomplete="new-password" />
            </div>
          {/if}
          <div class="form-footer">
            <button type="submit" class="btn btn-primary w-100" class:btn-loading={busy} disabled={busy}>
              {mode === 'setup' ? 'Create account' : 'Sign in'}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</div>
