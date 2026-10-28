<script>
  import { icons } from '../icons.js';
  let { name, class: cls = 'icon' } = $props();
</script>

<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class={cls} aria-hidden="true">{@html icons[name] || ''}</svg>
