<script>
  // A password with reveal and copy. Hidden by default; never logged or put in the URL.
  import { toast } from './toast.svelte.js';
  import Eye from '@lucide/svelte/icons/eye';
  import EyeOff from '@lucide/svelte/icons/eye-off';
  import Copy from '@lucide/svelte/icons/copy';

  let { value = '', label = 'password', secret = true } = $props();
  let shown = $state(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      toast('Copied.', 'success', 2000);
    } catch {
      shown = true;
      toast('This browser does not allow copying here. Select the text and copy it by hand.', 'info');
    }
  }
</script>

<span class="secret">
  <span class="val mono truncate" class:dots={secret && !shown}>{secret && !shown ? '••••••••••••' : value}</span>
  {#if secret}
    <button type="button" class="icon-btn mini" onclick={() => (shown = !shown)} aria-label="{shown ? 'Hide' : 'Show'} {label}" aria-pressed={shown}>
      {#if shown}<EyeOff size={16} />{:else}<Eye size={16} />{/if}
    </button>
  {/if}
  <button type="button" class="icon-btn mini" onclick={copy} aria-label="Copy {label}"><Copy size={16} /></button>
</span>

<style>
  .secret { display: inline-flex; align-items: center; gap: 0.125rem; min-width: 0; max-width: 100%; }
  .val { font-size: 0.8125rem; user-select: all; min-width: 0; padding-right: 0.25rem; }
  .dots { letter-spacing: 0.08em; color: var(--text-2); user-select: none; }
  .mini { width: 2rem; height: 2rem; min-height: 2rem; flex-shrink: 0; }
</style>
