<script>
  // Loads a page only when it is first opened (its own JS chunk), with a loading state.
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  let { load, ...rest } = $props();
  // svelte-ignore state_referenced_locally
  const page = load(); // the caller keys this component per page
</script>

{#await page}
  <div class="lazy t-sub"><Loader size={18} class="spinner" />Loading</div>
{:then m}
  <m.default {...rest} />
{:catch}
  <div class="notice"><CircleAlert size={18} /><span>This part of Box could not be loaded. Reload the page and try again.</span></div>
{/await}

<style>
  .lazy { display: flex; align-items: center; justify-content: center; gap: 0.625rem; min-height: 12rem; }
</style>
