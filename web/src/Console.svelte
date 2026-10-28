<script>
  import { onMount, tick } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import '@xterm/xterm/css/xterm.css';
  import { api } from './lib/api.js';
  import Icon from './lib/Icon.svelte';

  let { onSessionLost } = $props();

  let host = $state(); // terminal container
  let status = $state('connecting'); // connecting | connected | closed
  let closedReason = $state('');
  let height = $state(480);

  let term, fit, ws;
  const enc = new TextEncoder();

  // The terminal fills the window below the header (xterm needs a sized box to fit into).
  function measure() {
    if (!host) return;
    const top = host.getBoundingClientRect().top;
    height = Math.max(320, window.innerHeight - top - 56);
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
      theme: { background: '#000000', foreground: '#e4e4e7', cursor: '#60a5fa' },
    });
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    term.onData((d) => {
      if (ws && ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
    });
    // Size the box first, then fit the terminal into it and connect with the real size.
    onWindowResize().then(connect);
    addEventListener('resize', onWindowResize);
    return () => {
      removeEventListener('resize', onWindowResize);
      ws && ws.close();
      term.dispose();
    };
  });
</script>

<div class="page-header d-print-none">
  <div class="container-fluid">
    <div class="row g-2 align-items-center">
      <div class="col">
        <div class="page-pretitle">Root shell on this server</div>
        <h2 class="page-title">Console</h2>
      </div>
      <div class="col-auto ms-auto d-flex align-items-center gap-2">
        {#if status === 'connected'}
          <span class="badge bg-green-lt"><span class="status-dot status-green me-1"></span>Connected</span>
        {:else if status === 'connecting'}
          <span class="badge bg-secondary-lt"><span class="spinner-border spinner-border-sm me-1" role="status"></span>Connecting</span>
        {:else}
          <span class="badge bg-red-lt"><Icon name="plug-connected-x" class="icon me-1" />Disconnected</span>
          <button class="btn btn-primary" onclick={connect}><Icon name="refresh" />Reconnect</button>
        {/if}
      </div>
    </div>
  </div>
</div>

<div class="page-body">
  <div class="container-fluid">
    {#if status === 'closed'}
      <div class="alert alert-warning" role="alert">{closedReason}</div>
    {/if}
    <div class="card bg-black overflow-hidden">
      <div class="card-body p-2">
        <div bind:this={host} style:height="{height}px"></div>
      </div>
    </div>
  </div>
</div>
