<script>
  import { api } from '$lib/api.js';
  import { getPasskey, friendly } from '$lib/webauthn.js';
  import KeyRound from '@lucide/svelte/icons/key-round';
  import Logo from '$lib/Logo.svelte';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import Loader from '@lucide/svelte/icons/loader-circle';

  // mode: 'setup' (first start) or 'login'
  let { mode, onDone } = $props();

  let token = $state('');
  let username = $state('');
  let password = $state('');
  let password2 = $state('');
  let busy = $state(false);
  let error = $state('');
  let shake = $state(0);
  let pending = $state(''); // second step of a sign-in with 2FA
  let methods = $state([]);
  let code = $state('');
  let useRecovery = $state(false);

  async function submit(e) {
    e.preventDefault();
    error = '';
    if (mode === 'setup' && password !== password2) {
      fail('The two passwords do not match.');
      return;
    }
    busy = true;
    const res = mode === 'setup'
      ? await api('/api/setup', { method: 'POST', body: { token, username, password } })
      : await api('/api/login', { method: 'POST', body: { username, password } });
    busy = false;
    if (!res.ok) {
      fail(res.error);
      return;
    }
    password = password2 = '';
    if (res.data?.two_factor) {
      pending = res.data.pending;
      methods = res.data.methods;
      if (!methods.includes('totp')) usePasskey();
      return;
    }
    onDone();
  }

  async function submitCode(e) {
    e.preventDefault();
    error = '';
    busy = true;
    const res = await api('/api/login/2fa', { method: 'POST', body: { pending, code } });
    busy = false;
    if (!res.ok) return restartIfGone(res);
    onDone();
  }

  async function usePasskey() {
    error = '';
    busy = true;
    const begin = await api('/api/login/passkey/begin', { method: 'POST', body: { pending } });
    if (!begin.ok) {
      busy = false;
      return restartIfGone(begin);
    }
    let cred;
    try {
      cred = await getPasskey(begin.data);
    } catch (e) {
      busy = false;
      return fail(friendly(e));
    }
    const res = await fetch('/api/login/passkey/finish?pending=' + encodeURIComponent(pending), {
      method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(cred),
    }).then(async (r) => ({ ok: r.ok, status: r.status, error: (await r.json().catch(() => ({}))).error || 'The passkey was not accepted.' }))
      .catch(() => ({ ok: false, error: 'NexoVirt Box is not reachable.' }));
    busy = false;
    if (!res.ok) return restartIfGone(res);
    onDone();
  }

  function restartIfGone(res) {
    fail(res.error);
    if (/start again/i.test(res.error)) {
      pending = '';
      code = '';
    }
  }

  function back() {
    pending = '';
    code = '';
    error = '';
  }

  function fail(msg) {
    error = msg;
    shake++;
  }
</script>

<div class="auth">
  <div class="panel">
    <div class="head">
      <Logo size={56} />
      <h1 class="t-large">{mode === 'setup' ? 'Set up your Box' : pending ? 'Two-step sign-in' : 'Sign in'}</h1>
      <p class="t-sub">
        {mode === 'setup'
          ? 'Create the admin account for this server.'
          : pending ? 'Confirm it is you with your second step.' : 'Welcome back to NexoVirt Box.'}
      </p>
    </div>

    {#if pending}
    <form onsubmit={submitCode} autocomplete="off">
      {#if error}
        {#key shake}
          <div class="notice shake" role="alert"><CircleAlert size={18} /><span>{error}</span></div>
        {/key}
      {/if}
      {#if methods.includes('totp')}
        <div class="group fields">
          <label class="row">
            <span class="row-label">{useRecovery ? 'Recovery code' : 'Code'}</span>
            {#if useRecovery}
              <input class="mono" bind:value={code} required autocomplete="off" spellcheck="false" placeholder="xxxxx-xxxxx" />
            {:else}
              <input class="mono" bind:value={code} required inputmode="numeric" autocomplete="one-time-code" maxlength="7" placeholder="6 digits from your app" />
            {/if}
          </label>
        </div>
        <button type="submit" class="btn btn-primary btn-block" disabled={busy || !code}>
          {#if busy}<Loader size={17} class="spinner" />Checking{:else}Continue{/if}
        </button>
        <button type="button" class="btn btn-gray btn-block" onclick={() => { useRecovery = !useRecovery; code = ''; }}>
          {useRecovery ? 'Use the code from my app' : 'Use a recovery code'}
        </button>
      {/if}
      {#if methods.includes('passkey')}
        <button type="button" class="btn {methods.includes('totp') ? 'btn-gray' : 'btn-primary'} btn-block" onclick={usePasskey} disabled={busy}>
          {#if busy && !methods.includes('totp')}<Loader size={17} class="spinner" />{:else}<KeyRound size={17} />{/if}Use a passkey
        </button>
      {/if}
      <button type="button" class="link t-caption" onclick={back}>Back to sign in</button>
    </form>
    {:else}
    <form onsubmit={submit} autocomplete="on">
      {#if error}
        {#key shake}
          <div class="notice shake" role="alert"><CircleAlert size={18} /><span>{error}</span></div>
        {/key}
      {/if}

      {#if mode === 'setup'}
        <div class="group fields">
          <label class="row">
            <span class="row-label">Setup code</span>
            <input class="mono" bind:value={token} required autocomplete="off" spellcheck="false" placeholder="from the install" />
          </label>
        </div>
        <p class="hint t-caption">Missed it? Run <code class="mono">cat /var/lib/nexovirt-box/setup-token</code> on the server.</p>
      {/if}

      <div class="group fields">
        <label class="row">
          <span class="row-label">Username</span>
          <input bind:value={username} required autocomplete="username" autocapitalize="none" spellcheck="false" placeholder="admin" />
        </label>
        <label class="row">
          <span class="row-label">Password</span>
          <input type="password" bind:value={password} required minlength={mode === 'setup' ? 10 : undefined}
            autocomplete={mode === 'setup' ? 'new-password' : 'current-password'} placeholder={mode === 'setup' ? '10 characters or more' : 'Required'} />
        </label>
        {#if mode === 'setup'}
          <label class="row">
            <span class="row-label">Repeat</span>
            <input type="password" bind:value={password2} required minlength={10} autocomplete="new-password" placeholder="Same password again" />
          </label>
        {/if}
      </div>

      <button type="submit" class="btn btn-primary btn-block" disabled={busy}>
        {#if busy}<Loader size={17} class="spinner" />{/if}
        {busy ? (mode === 'setup' ? 'Creating account' : 'Signing in') : mode === 'setup' ? 'Create account' : 'Sign in'}
      </button>
    </form>
    {/if}
  </div>
  <p class="foot t-caption">NexoVirt Box, open source under AGPL-3.0</p>
</div>

<style>
  .auth {
    min-height: 100svh; display: flex; flex-direction: column; align-items: center; justify-content: center;
    padding: 2rem 1rem;
  }
  .panel { width: min(24rem, 100%); display: grid; gap: 2rem; }
  .head { display: grid; justify-items: center; text-align: center; gap: 0.5rem; }
  .head :global(svg) { margin-bottom: 0.75rem; }
  h1 { margin: 0; }
  p { margin: 0; }
  form { display: grid; gap: 1rem; }
  .link { background: none; border: 0; color: var(--accent); cursor: pointer; justify-self: center; min-height: 2.75rem; padding: 0 0.75rem; }
  .fields .row { cursor: text; }
  .fields .row-label { width: 6.25rem; flex-shrink: 0; color: var(--text); font-weight: 500; }
  input {
    flex: 1; min-width: 0; height: 1.75rem; border: 0; padding: 0; background: none; outline: none;
  }
  input::placeholder { color: var(--text-3); }
  .row:focus-within { background: var(--accent-fill); }
  .row:focus-within .row-label { color: var(--accent); }
  .hint { margin: -0.5rem 1.125rem 0; }
  .hint code { font-size: 0.75rem; }
  .btn { margin-top: 0.5rem; min-height: 3rem; font-size: 0.9375rem; }
  .foot { margin-top: 2.5rem; color: var(--text-3); }
  .shake { animation: shake 420ms var(--spring-bounce); }
  @keyframes shake {
    0%, 100% { transform: translateX(0); }
    20% { transform: translateX(-6px); }
    40% { transform: translateX(5px); }
    60% { transform: translateX(-3px); }
    80% { transform: translateX(2px); }
  }
</style>
