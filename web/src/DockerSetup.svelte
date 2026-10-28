<script>
  // Shown when Docker is missing or stopped. Installing it runs Docker's official script after
  // an explicit confirmation.
  import { runJob } from '$lib/jobs.js';
  import { toast } from '$lib/toast.svelte.js';
  import Dialog from '$lib/Dialog.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import Container from '@lucide/svelte/icons/container';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';

  let { docker, onDone } = $props();
  let open = $state(false);
  let origin = $state(null);
  let phase = $state('confirm'); // confirm | running | failed
  let lines = $state([]);
  let error = $state('');

  function ask(e) {
    origin = { x: e.clientX, y: e.clientY };
    phase = 'confirm';
    lines = [];
    error = '';
    open = true;
  }

  async function install() {
    phase = 'running';
    const job = await runJob('/api/docker/install', {}, (j) => (lines = j.lines));
    if (job.status === 'done') {
      open = false;
      toast('Docker is installed and running.', 'success');
      onDone();
    } else {
      phase = 'failed';
      error = job.error;
    }
  }
</script>

<section class="tile setup">
  <span class="ico"><Container size={22} /></span>
  <div class="text">
    {#if !docker.installed}
      <h2 class="t-head">Docker is not installed</h2>
      <p class="t-sub">Apps run as Docker containers. Box can install Docker for you.</p>
    {:else}
      <h2 class="t-head">Docker is not running</h2>
      <p class="t-sub">Start it on the server with <span class="mono">systemctl start docker</span>, then reload this page.</p>
    {/if}
  </div>
  {#if !docker.installed}
    <button class="btn btn-primary btn-sm" onclick={ask}>Install Docker</button>
  {/if}
</section>

<Dialog bind:open label="Install Docker" {origin} locked={phase === 'running'}>
  <div class="dlg">
    <h2 class="t-title">Install Docker</h2>
    {#if phase === 'confirm'}
      <p class="t-sub">Box runs Docker's official install script from <span class="mono">get.docker.com</span> as root on this server. It adds Docker's package source and installs Docker Engine and Docker Compose.</p>
      <p class="t-sub">Anyone who can use Docker on a server can control the whole server, so only apps you trust should run here.</p>
      <div class="buttons">
        <button class="btn btn-gray" data-close onclick={() => (open = false)}>Cancel</button>
        <button class="btn btn-primary" onclick={install}>Install Docker</button>
      </div>
    {:else}
      {#if phase === 'running'}
        <p class="t-sub busy"><Loader size={16} class="spinner" />Installing. This takes a minute or two.</p>
      {:else}
        <div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
      {/if}
      <JobLog {lines} />
      {#if phase === 'failed'}
        <div class="buttons"><button class="btn btn-gray" onclick={() => (open = false)}>Close</button></div>
      {/if}
    {/if}
  </div>
</Dialog>

<style>
  .setup { display: flex; align-items: center; gap: 1rem; flex-wrap: wrap; }
  .ico { display: grid; place-items: center; width: 2.75rem; height: 2.75rem; border-radius: 0.875rem; background: var(--accent-fill); color: var(--accent); }
  .text { flex: 1; min-width: 14rem; display: grid; gap: 0.125rem; }
  h2, p { margin: 0; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .buttons { display: flex; justify-content: flex-end; gap: 0.5rem; margin-top: 0.25rem; }
  .busy { display: flex; align-items: center; gap: 0.5rem; }
</style>
