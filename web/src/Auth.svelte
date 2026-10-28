<script>
  import { api } from '$lib/api.js';
  import Logo from '$lib/Logo.svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import * as Card from '$lib/components/ui/card/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Label } from '$lib/components/ui/label/index.js';
  import * as Alert from '$lib/components/ui/alert/index.js';
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

<div class="flex min-h-svh items-center justify-center p-4">
  <div class="w-full max-w-sm">
    <div class="mb-8 flex items-center justify-center gap-2.5 text-foreground">
      <Logo size={36} />
      <span class="text-xl font-semibold tracking-tight">NexoVirt Box</span>
    </div>
    <Card.Root>
      <Card.Header>
        <Card.Title class="text-lg">{mode === 'setup' ? 'Set up this Box' : 'Sign in'}</Card.Title>
        <Card.Description>
          {mode === 'setup'
            ? 'Create the admin account. You need the setup code that was shown after the install.'
            : 'Welcome back. Sign in to manage this server.'}
        </Card.Description>
      </Card.Header>
      <Card.Content>
        {#if error}
          <Alert.Root variant="destructive" class="mb-5">
            <CircleAlert />
            <Alert.Description>{error}</Alert.Description>
          </Alert.Root>
        {/if}
        <form onsubmit={submit} autocomplete="on" class="grid gap-5">
          {#if mode === 'setup'}
            <div class="grid gap-2">
              <Label for="token">Setup code</Label>
              <Input id="token" class="font-mono" bind:value={token} required autocomplete="off" spellcheck="false" />
              <p class="text-xs text-muted-foreground">
                Missed it? Run <code class="rounded bg-muted px-1 py-0.5 font-mono">cat /var/lib/nexovirt-box/setup-token</code> on the server.
              </p>
            </div>
          {/if}
          <div class="grid gap-2">
            <Label for="username">Username</Label>
            <Input id="username" bind:value={username} required autocomplete="username" autocapitalize="none" spellcheck="false" />
          </div>
          <div class="grid gap-2">
            <Label for="password">Password</Label>
            <Input id="password" type="password" bind:value={password} required
              minlength={mode === 'setup' ? 10 : undefined} autocomplete={mode === 'setup' ? 'new-password' : 'current-password'} />
            {#if mode === 'setup'}<p class="text-xs text-muted-foreground">At least 10 characters.</p>{/if}
          </div>
          {#if mode === 'setup'}
            <div class="grid gap-2">
              <Label for="password2">Repeat password</Label>
              <Input id="password2" type="password" bind:value={password2} required minlength={10} autocomplete="new-password" />
            </div>
          {/if}
          <Button type="submit" class="w-full" disabled={busy}>
            {#if busy}<Loader class="animate-spin" />{/if}
            {mode === 'setup' ? 'Create account' : 'Sign in'}
          </Button>
        </form>
      </Card.Content>
    </Card.Root>
  </div>
</div>
