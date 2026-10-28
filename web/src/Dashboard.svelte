<script>
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { bytes, rate, pct, duration, level } from '$lib/format.js';
  import Sparkline from '$lib/Sparkline.svelte';
  import * as Card from '$lib/components/ui/card/index.js';
  import * as Table from '$lib/components/ui/table/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { Progress } from '$lib/components/ui/progress/index.js';
  import { Skeleton } from '$lib/components/ui/skeleton/index.js';
  import Cpu from '@lucide/svelte/icons/cpu';
  import MemoryStick from '@lucide/svelte/icons/memory-stick';
  import Activity from '@lucide/svelte/icons/activity';
  import Clock from '@lucide/svelte/icons/clock';
  import ArrowDown from '@lucide/svelte/icons/arrow-down';
  import ArrowUp from '@lucide/svelte/icons/arrow-up';
  import Network from '@lucide/svelte/icons/network';
  import HardDrive from '@lucide/svelte/icons/hard-drive';
  import Info from '@lucide/svelte/icons/info';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { system, onSessionLost } = $props();

  const HISTORY = 60; // 2 minutes at one sample every 2 s

  let snap = $state(null);
  let live = $state('connecting'); // connecting | live | reconnecting
  let cpuHistory = $state([]);
  let memHistory = $state([]);
  let netHistory = $state({}); // interface -> combined rx+tx rates

  let memPct = $derived(snap ? pct(snap.memory.used, snap.memory.total) : 0);
  let swapPct = $derived(snap ? pct(snap.memory.swap_used, snap.memory.swap_total) : 0);
  let loadPct = $derived(snap && system ? Math.round((snap.load[0] / system.cores) * 100) : 0);

  function push(arr = [], v) {
    const next = [...arr, v];
    return next.length > HISTORY ? next.slice(next.length - HISTORY) : next;
  }

  onMount(() => {
    const es = new EventSource('/api/stats/stream');
    es.onmessage = (e) => {
      let s;
      try { s = JSON.parse(e.data); } catch { return; } // ignore a broken frame
      snap = s;
      live = 'live';
      cpuHistory = push(cpuHistory, s.cpu.total);
      memHistory = push(memHistory, pct(s.memory.used, s.memory.total));
      const next = {};
      for (const n of s.network) next[n.name] = push(netHistory[n.name], n.rx_rate + n.tx_rate);
      netHistory = next;
    };
    es.onerror = async () => {
      live = 'reconnecting';
      const res = await api('/api/auth/state');
      if (res.ok && !res.data.logged_in) {
        es.close();
        onSessionLost();
      }
    };
    return () => es.close();
  });
</script>

{#snippet statCard(Icon, label, value)}
  <div class="flex items-center justify-between">
    <span class="text-sm font-medium text-muted-foreground">{label}</span>
    <Icon class="size-4 text-muted-foreground" />
  </div>
  <div class="mt-2 text-3xl font-semibold tracking-tight tabular-nums">{value}</div>
{/snippet}

<div class="mx-auto grid max-w-7xl gap-6">
  <div class="flex flex-wrap items-end justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold tracking-tight">Dashboard</h1>
      <p class="text-sm text-muted-foreground">Live view of this server, updated every 2 seconds.</p>
    </div>
    {#if live === 'live'}
      <Badge variant="outline" class="gap-1.5"><span class="size-2 animate-pulse rounded-full bg-ok"></span>Live</Badge>
    {:else}
      <Badge variant="outline" class="gap-1.5 text-muted-foreground">
        <Loader class="size-3 animate-spin" />{live === 'reconnecting' ? 'Reconnecting' : 'Connecting'}
      </Badge>
    {/if}
  </div>

  {#if !snap}
    <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      {#each Array(4) as _}
        <Card.Root class="gap-3 p-6"><Skeleton class="h-4 w-20" /><Skeleton class="h-8 w-28" /><Skeleton class="h-9 w-full" /></Card.Root>
      {/each}
    </div>
    <div class="grid gap-4 lg:grid-cols-2">
      <Card.Root class="gap-3 p-6"><Skeleton class="h-5 w-24" /><Skeleton class="h-24 w-full" /></Card.Root>
      <Card.Root class="gap-3 p-6"><Skeleton class="h-5 w-24" /><Skeleton class="h-24 w-full" /></Card.Root>
    </div>
  {:else}
    <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <Card.Root class="gap-0 p-6">
        {@render statCard(Cpu, 'CPU', `${snap.cpu.total.toFixed(1)} %`)}
        <Sparkline values={cpuHistory} class="mt-3 text-primary" />
        <div class="mt-3 flex h-6 items-end gap-0.5" title="Load per CPU thread">
          {#each snap.cpu.cores as c, i (i)}
            <div class="flex h-full flex-1 items-end rounded-sm bg-muted">
              <div class="w-full rounded-sm transition-all duration-500 {level(c)}" style:height="{Math.max(6, c)}%"></div>
            </div>
          {/each}
        </div>
        <p class="mt-2 truncate text-xs text-muted-foreground">{snap.cpu.cores.length} threads{system?.cpu_model ? `, ${system.cpu_model}` : ''}</p>
      </Card.Root>

      <Card.Root class="gap-0 p-6">
        {@render statCard(MemoryStick, 'Memory', `${memPct} %`)}
        <Sparkline values={memHistory} class="mt-3 text-primary" />
        <Progress value={memPct} class="mt-3 h-1.5" indicatorClass={level(memPct)} />
        <p class="mt-2 text-xs text-muted-foreground tabular-nums">{bytes(snap.memory.used)} of {bytes(snap.memory.total)}</p>
        <p class="text-xs text-muted-foreground tabular-nums">
          {snap.memory.swap_total > 0 ? `Swap ${bytes(snap.memory.swap_used)} of ${bytes(snap.memory.swap_total)} (${swapPct} %)` : 'No swap'}
        </p>
      </Card.Root>

      <Card.Root class="gap-0 p-6">
        {@render statCard(Activity, 'Load average', snap.load[0].toFixed(2))}
        <Progress value={Math.min(100, loadPct)} class="mt-4 h-1.5" indicatorClass={level(loadPct)} />
        <p class="mt-2 text-xs text-muted-foreground tabular-nums">1, 5, 15 min: {snap.load.map((l) => l.toFixed(2)).join(', ')}</p>
        <p class="text-xs text-muted-foreground">{loadPct} % of {system?.cores ?? '-'} threads</p>
      </Card.Root>

      <Card.Root class="gap-0 p-6">
        {@render statCard(Clock, 'Uptime', duration(snap.uptime))}
        <p class="mt-4 truncate text-xs text-muted-foreground">{system?.os ?? ''}</p>
        <p class="truncate text-xs text-muted-foreground">Kernel {system?.kernel ?? '-'}</p>
      </Card.Root>
    </div>

    <div class="grid gap-4 lg:grid-cols-2">
      <Card.Root>
        <Card.Header>
          <Card.Title class="flex items-center gap-2"><Network class="size-4 text-muted-foreground" />Network</Card.Title>
          <Card.Description>Current throughput per interface.</Card.Description>
        </Card.Header>
        <Card.Content class="grid gap-4">
          {#each snap.network as n (n.name)}
            <div class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1">
              <span class="truncate text-sm font-medium">{n.name}</span>
              <span class="flex gap-4 text-sm tabular-nums">
                <span class="flex items-center gap-1"><ArrowDown class="size-3.5 text-muted-foreground" />{rate(n.rx_rate)}</span>
                <span class="flex items-center gap-1"><ArrowUp class="size-3.5 text-muted-foreground" />{rate(n.tx_rate)}</span>
              </span>
              <div class="col-span-2"><Sparkline values={netHistory[n.name] ?? []} max={0} class="text-primary" /></div>
            </div>
          {:else}
            <p class="text-sm text-muted-foreground">No network interfaces found.</p>
          {/each}
        </Card.Content>
      </Card.Root>

      <Card.Root>
        <Card.Header>
          <Card.Title class="flex items-center gap-2"><Info class="size-4 text-muted-foreground" />System</Card.Title>
          <Card.Description>Hardware and operating system.</Card.Description>
        </Card.Header>
        <Card.Content>
          {#if !system}
            <div class="grid gap-3">{#each Array(5) as _}<Skeleton class="h-4 w-full" />{/each}</div>
          {:else}
            <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-3 text-sm">
              {#each [
                ['Hostname', system.hostname],
                ['Operating system', system.os],
                ['Kernel', system.kernel],
                ['Architecture', system.arch],
                ['Processor', system.cpu_model || '-'],
                ['Cores', `${system.physical_cores || '-'} cores, ${system.cores} threads`],
                ['Virtualization', system.virtualization || 'None detected'],
              ] as [k, v] (k)}
                <dt class="text-muted-foreground">{k}</dt>
                <dd class="truncate font-medium" title={v}>{v}</dd>
              {/each}
            </dl>
          {/if}
        </Card.Content>
      </Card.Root>
    </div>

    <Card.Root>
      <Card.Header>
        <Card.Title class="flex items-center gap-2"><HardDrive class="size-4 text-muted-foreground" />Drives</Card.Title>
        <Card.Description>Mounted filesystems on real devices.</Card.Description>
      </Card.Header>
      <Card.Content>
        {#if snap.disks.length === 0}
          <p class="text-sm text-muted-foreground">No mounted drives found.</p>
        {:else}
          <Table.Root>
            <Table.Header>
              <Table.Row>
                <Table.Head>Mount point</Table.Head>
                <Table.Head class="hidden md:table-cell">Device</Table.Head>
                <Table.Head class="hidden sm:table-cell">Filesystem</Table.Head>
                <Table.Head class="text-right">Size</Table.Head>
                <Table.Head class="hidden text-right md:table-cell">Free</Table.Head>
                <Table.Head class="w-1/4 min-w-32">Usage</Table.Head>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {#each snap.disks as d (d.mountpoint)}
                {@const p = pct(d.used, d.total)}
                <Table.Row>
                  <Table.Cell class="font-medium">{d.mountpoint}</Table.Cell>
                  <Table.Cell class="hidden text-muted-foreground md:table-cell">{d.device}</Table.Cell>
                  <Table.Cell class="hidden sm:table-cell"><Badge variant="secondary" class="font-mono">{d.fstype}</Badge></Table.Cell>
                  <Table.Cell class="text-right tabular-nums">{bytes(d.total)}</Table.Cell>
                  <Table.Cell class="hidden text-right tabular-nums md:table-cell">{bytes(d.free)}</Table.Cell>
                  <Table.Cell>
                    <div class="flex items-center gap-3">
                      <Progress value={p} class="h-1.5 flex-1" indicatorClass={level(p)} />
                      <span class="w-12 text-right text-xs text-muted-foreground tabular-nums">{p} %</span>
                    </div>
                  </Table.Cell>
                </Table.Row>
              {/each}
            </Table.Body>
          </Table.Root>
        {/if}
      </Card.Content>
    </Card.Root>
  {/if}
</div>
