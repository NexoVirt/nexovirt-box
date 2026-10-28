<script>
  import { onMount, tick } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import '@xterm/xterm/css/xterm.css';
  import { api } from '$lib/api.js';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Unplug from '@lucide/svelte/icons/unplug';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import LockKeyhole from '@lucide/svelte/icons/lock-keyhole';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';

  let { onSessionLost } = $props();

  // The terminal is always dark, in light and dark mode (zinc + the blue accent for the cursor).
  const TERM_THEME = { background: '#18181b', foreground: '#e4e4e7', cursor: '#60a5fa', cursorAccent: '#18181b', selectionBackground: '#3f3f46' };

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

  onMount(() => {
    term = new Terminal({
      cursorBlink: true,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
      fontSize: 14,
      lineHeight: 1.2,
      theme: TERM_THEME,
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
        <span class="ico xl accent lock"><LockKeyhole /></span>
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
  h2, p { margin: 0; }
  .actions { display: flex; align-items: center; gap: 0.5rem; }
  /* Always a dark card: the dark tokens apply inside it in both modes. */
  .term {
    position: relative; padding: 0.875rem 0.5rem 0.5rem 1rem; overflow: hidden; color-scheme: dark;
    --surface: #18181b; --text: #fafafa; --text-2: #a1a1aa; --text-3: #71717a; --fill: rgba(161, 161, 170, 0.14);
    --accent: #60a5fa; --accent-fill: rgba(96, 165, 250, 0.16); --on-accent: #09090b; --error: #f87171; --error-fill: rgba(248, 113, 113, 0.12);
    background: #18181b; color: var(--text); box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.06), var(--shadow-tile);
  }
  .hidden { visibility: hidden; position: absolute; }
  .unlock {
    display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem;
    max-width: 22rem; margin: 0 auto; text-align: center;
  }
  .unlock h2, .unlock p { margin: 0; }
  .unlock .notice { width: 100%; text-align: left; }
  .lock { margin-bottom: 0.25rem; }
  .unlock input {
    width: 100%; height: 2.75rem; padding: 0 0.875rem; border: 0; border-radius: var(--r-control);
    background: var(--fill); font-size: 0.9375rem; margin-top: 0.5rem;
  }
</style>
