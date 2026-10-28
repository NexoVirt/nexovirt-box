<script>
  // Catalog text (store notes, markdown-ish) as plain text with clickable web links.
  // Built from text nodes and <a> elements only, never as HTML.
  import { plainText } from './jobs.js';

  let { text = '' } = $props();
  let parts = $derived(plainText(text).split(/(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/));
</script>

<p class="rich">{#each parts as p, i (i)}{#if i % 2}<a href={p} target="_blank" rel="noopener noreferrer">{p}</a>{:else}{p}{/if}{/each}</p>

<style>
  .rich { margin: 0; white-space: pre-line; overflow-wrap: anywhere; color: var(--text-2); }
  a { color: var(--accent); }
</style>
