<script>
  import { onMount, tick } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import '@xterm/xterm/css/xterm.css';
  import { api } from '$lib/api.js';
  import { theme } from '$lib/theme.svelte.js';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Unplug from '@lucide/svelte/icons/unplug';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import LockKeyhole from '@lucide/svelte/icons/lock-keyhole';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';

  let { onSessionLost } = $props();

  // Terminal colours follow the app theme (zinc + the blue accent for the cursor).
  const THEMES = {
    dark: { background: '#18181b', foreground: '#e4e4e7', cursor: '#60a5fa', selectionBackground: '#3f3f46' },
    light: { background: '#ffffff', foreground: '#18181b', cursor: '#2563eb', selectionBackground: '#d4d4d8',
      black: '#18181b', white: '#71717a', brightWhite: '#3f3f46', yellow: '#a16207', brightYellow: '#a16207' },
  };

  let host = $state(); // terminal container
  let status = $state('checking'); // checking | reauth | connecting | connected | closed
  let password = $state('');
  let reauthError = $state('');
  let reauthBusy = $state(false);
  let needsCode = $state(false);
  let code = $state('');
  let pwInput = $state();
  let closedReason = $state('');
  let height = $state(480);

  let term, fit, ws;
  const enc = new TextEncoder();

  // The terminal fills the window below the header (xterm needs a sized box to fit into).
  function measure() {
    if (!host) return;
    const top = host.getBoundingClientRect().top + window.scrollY;
    height = Math.max(320, window.innerHeight - top - 48);
  }

  function sendResize() {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
    }
  }

  async function onWindowResize() {
    measure();
    await tick();
    fit.fit();
    sendResize();
  }

  // The console needs a password proof from the last few minutes (a root shell is the most
  // powerful thing in Box). Ask for it first, then connect.
  async function open() {
    status = 'checking';
    const res = await api('/api/console/ready');
    if (res.status === 401) return onSessionLost();
    if (res.ok && res.data.fresh) return connect();
    needsCode = !!res.data?.needs_code;
    status = 'reauth';
    await tick();
    pwInput?.focus();
  }

  async function unlock(e) {
    e.preventDefault();
    reauthError = '';
    reauthBusy = true;
    const res = await api('/api/auth/reauth', { method: 'POST', body: { password, code } });
    reauthBusy = false;
    if (res.status === 401 && /sign in/i.test(res.error)) return onSessionLost();
    if (!res.ok) {
      reauthError = res.error;
      return;
    }
    password = '';
    code = '';
    await tick();
    await onWindowResize();
    connect();
  }

  function connect() {
    status = 'connecting';
    closedReason = '';
    term.reset();
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    ws = new WebSocket(`${proto}://${location.host}/api/console?cols=${term.cols}&rows=${term.rows}`);
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      status = 'connected';
      term.focus();
    };
    ws.onmessage = (e) => term.write(new Uint8Array(e.data));
    ws.onclose = async (e) => {
      status = 'closed';
      closedReason = e.reason || 'The connection to the server was lost.';
      const res = await api('/api/auth/state');
      if (res.ok && !res.data.logged_in) onSessionLost();
    };
  }

  $effect(() => {
    const t = THEMES[theme.current];
    if (term) term.options.theme = t;
  });

  onMount(() => {
    term = new Terminal({
      cursorBlink: true,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
      fontSize: 14,
      lineHeight: 1.2,
      theme: THEMES[theme.current],
    });
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    term.onData((d) => {
      if (ws && ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
    });
    // Size the box first, then fit the terminal into it and connect with the real size.
    onWindowResize().then(open);
    addEventListener('resize', onWindowResize);
    return () => {
      removeEventListener('resize', onWindowResize);
      ws && ws.close();
      term.dispose();
    };
  });
</script>

<div class="page">
  <header class="page-head">
    <div>
      <h1 class="t-large">Console</h1>
      <p class="t-sub">Root shell on this server.</p>
    </div>
    <div class="actions">
      {#if status === 'connected'}
        <span class="pill"><span class="dot ok"></span>Connected</span>
      {:else if status === 'connecting' || status === 'checking'}
        <span class="pill"><Loader size={13} class="spinner" />Connecting</span>
      {:else if status === 'reauth'}
        <span class="pill"><LockKeyhole size={13} />Locked</span>
      {:else}
        <span class="pill"><span class="dot error"></span>Disconnected</span>
        <button class="btn btn-tinted btn-sm" onclick={open}><RefreshCw size={15} />Reconnect</button>
      {/if}
    </div>
  </header>

  {#if status === 'closed'}
    <div class="notice neutral"><Unplug size={18} /><span>{closedReason}</span></div>
  {/if}

  <div class="term tile">
    <div bind:this={host} style:height="{height}px" class:hidden={status === 'reauth'}></div>
    {#if status === 'reauth'}
      <form class="unlock" onsubmit={unlock} style:height="{height}px">
        <span class="lock"><LockKeyhole size={26} /></span>
        <h2 class="t-title">Confirm it is you</h2>
        <p class="t-sub">The console is a root shell. Enter your password{needsCode ? ' and a code from your authenticator app' : ''} to open it.</p>
        {#if reauthError}
          <div class="notice" role="alert"><CircleAlert size={18} /><span>{reauthError}</span></div>
        {/if}
        <input bind:this={pwInput} bind:value={password} type="password" autocomplete="current-password" placeholder="Password" aria-label="Password" required />
        {#if needsCode}
          <input bind:value={code} inputmode="numeric" autocomplete="one-time-code" maxlength="7" placeholder="Code from your authenticator app" aria-label="Authenticator code" required />
        {/if}
        <button class="btn btn-primary btn-block" disabled={reauthBusy || !password || (needsCode && !code)}>
          {#if reauthBusy}<Loader size={16} class="spinner" />Checking{:else}Open console{/if}
        </button>
      </form>
    {/if}
  </div>
</div>

<style>
  .page { display: grid; gap: 1.25rem; }
  .page-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; flex-wrap: wrap; padding-top: 0.5rem; }
  h1, p { margin: 0; }
  .page-head p { margin-top: 0.375rem; }
  .actions { display: flex; align-items: center; gap: 0.5rem; }
  .term { position: relative; padding: 0.875rem 0.5rem 0.5rem 1rem; background: var(--term-bg); overflow: hidden; }
  .hidden { visibility: hidden; position: absolute; }
  .unlock {
    display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem;
    max-width: 22rem; margin: 0 auto; text-align: center;
  }
  .unlock h2, .unlock p { margin: 0; }
  .unlock .notice { width: 100%; text-align: left; }
  .lock {
    display: grid; place-items: center; width: 3.5rem; height: 3.5rem; border-radius: 1.1rem;
    background: var(--accent-fill); color: var(--accent); margin-bottom: 0.25rem;
  }
  .unlock input {
    width: 100%; height: 2.75rem; padding: 0 0.875rem; border: 0; border-radius: var(--r-control);
    background: var(--fill); font-size: 0.9375rem; margin-top: 0.5rem;
  }
</style>
