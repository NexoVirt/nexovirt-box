<script>
  // Live output of a background action; stays scrolled to the newest line.
  import { tick } from 'svelte';
  let { lines = [] } = $props();
  let box = $state();
  $effect(() => {
    lines.length;
    tick().then(() => box && (box.scrollTop = box.scrollHeight));
  });
</script>

<pre bind:this={box} class="log mono" aria-live="polite">{lines.length ? lines.join('\n') : 'Starting'}</pre>

<style>
  .log {
    margin: 0; height: 13rem; overflow: auto; padding: 0.75rem 0.875rem; border-radius: var(--r-control);
    background: var(--fill); color: var(--text-2); font-size: 0.75rem; line-height: 1.5; white-space: pre-wrap; word-break: break-word;
  }
</style>
