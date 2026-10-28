<script>
  import { onMount, tick } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import '@xterm/xterm/css/xterm.css';
  import { api } from '$lib/api.js';
  import { theme } from '$lib/theme.svelte.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import * as Alert from '$lib/components/ui/alert/index.js';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import Unplug from '@lucide/svelte/icons/unplug';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { onSessionLost } = $props();

  // Terminal colours follow the app theme (zinc + the blue accent for the cursor).
  const THEMES = {
    dark: { background: '#18181b', foreground: '#e4e4e7', cursor: '#60a5fa', selectionBackground: '#3f3f46' },
    light: { background: '#ffffff', foreground: '#18181b', cursor: '#2563eb', selectionBackground: '#d4d4d8',
      black: '#18181b', white: '#71717a', brightWhite: '#3f3f46', yellow: '#a16207', brightYellow: '#a16207' },
  };

  let host = $state(); // terminal container
  let status = $state('connecting'); // connecting | connected | closed
  let closedReason = $state('');
  let height = $state(480);

  let term, fit, ws;
  const enc = new TextEncoder();

  // The terminal fills the window below the header (xterm needs a sized box to fit into).
  function measure() {
    if (!host) return;
    const top = host.getBoundingClientRect().top + window.scrollY;
    height = Math.max(320, window.innerHeight - top - 40);
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
    onWindowResize().then(connect);
    addEventListener('resize', onWindowResize);
    return () => {
      removeEventListener('resize', onWindowResize);
      ws && ws.close();
      term.dispose();
    };
  });
</script>

<div class="mx-auto grid max-w-7xl gap-4">
  <div class="flex flex-wrap items-end justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold tracking-tight">Console</h1>
      <p class="text-sm text-muted-foreground">Root shell on this server.</p>
    </div>
    <div class="flex items-center gap-2">
      {#if status === 'connected'}
        <Badge variant="outline" class="gap-1.5"><span class="size-2 rounded-full bg-ok"></span>Connected</Badge>
      {:else if status === 'connecting'}
        <Badge variant="outline" class="gap-1.5 text-muted-foreground"><Loader class="size-3 animate-spin" />Connecting</Badge>
      {:else}
        <Badge variant="outline" class="gap-1.5"><Unplug class="size-3 text-error" />Disconnected</Badge>
        <Button size="sm" onclick={connect}><RefreshCw />Reconnect</Button>
      {/if}
    </div>
  </div>

  {#if status === 'closed'}
    <Alert.Root>
      <Unplug />
      <Alert.Description>{closedReason}</Alert.Description>
    </Alert.Root>
  {/if}

  <div class="overflow-hidden rounded-xl border bg-card p-3 shadow-xs">
    <div bind:this={host} style:height="{height}px"></div>
  </div>
</div>
