<script>
  // One folder in the explorer's sidebar tree. Children (folders only) load when expanded.
  import { api } from './api.js';
  import FileTree from './FileTree.svelte';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import Folder from '@lucide/svelte/icons/folder';
  import FolderOpen from '@lucide/svelte/icons/folder-open';

  let { path, label, depth = 0, current, open: expanded = false, onOpen, onDrop } = $props();

  let isOpen = $state(expanded);
  let children = $state(null);
  let over = $state(false);

  const join = (d, n) => (d.endsWith('/') ? d : d + '/') + n;

  async function load() {
    const res = await api(`/api/files?path=${encodeURIComponent(path)}`);
    children = res.ok ? res.data.entries.filter((e) => e.dir).map((e) => join(path, e.name)) : [];
  }

  $effect(() => { if (isOpen && children === null) load(); });
  // Opening a folder deeper down expands the way to it.
  $effect(() => { if (current && current !== path && current.startsWith(path.endsWith('/') ? path : path + '/')) isOpen = true; });

  function toggle(e) {
    e.stopPropagation();
    isOpen = !isOpen;
    if (isOpen) load();
  }
</script>

<li role="treeitem" aria-expanded={isOpen} aria-selected={current === path}>
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="node" class:active={current === path} class:over style:padding-left="{0.375 + depth * 0.875}rem"
    onclick={() => onOpen(path)} onkeydown={(e) => e.key === 'Enter' && onOpen(path)} role="button" tabindex="-1"
    ondragover={(e) => { if (e.dataTransfer.types.includes('application/x-nxbox-paths') || e.dataTransfer.types.includes('Files')) { e.preventDefault(); over = true; } }}
    ondragleave={() => (over = false)}
    ondrop={(e) => { over = false; onDrop(e, path); }}>
    <button class="chev" class:open={isOpen} onclick={toggle} aria-label={isOpen ? 'Collapse' : 'Expand'} tabindex="-1"><ChevronRight size={13} /></button>
    {#if current === path}<FolderOpen size={15} />{:else}<Folder size={15} />{/if}
    <span class="truncate">{label}</span>
  </div>
  {#if isOpen && children?.length}
    <ul role="group">
      {#each children as c (c)}
        <FileTree path={c} label={c.slice(c.lastIndexOf('/') + 1)} depth={depth + 1} {current} {onOpen} {onDrop} />
      {/each}
    </ul>
  {/if}
</li>

<style>
  ul { list-style: none; margin: 0; padding: 0; }
  .node {
    display: flex; align-items: center; gap: 0.375rem; min-height: 1.875rem; padding-right: 0.5rem;
    border-radius: 0.5rem; cursor: default; color: var(--text); font-size: 0.8125rem;
  }
  .node:hover { background: var(--fill); }
  .node.active { background: var(--fill-2); font-weight: 600; }
  .node.over { background: var(--accent-fill); box-shadow: inset 0 0 0 1.5px var(--accent); }
  .node :global(svg) { color: var(--accent); }
  .chev {
    display: grid; place-items: center; width: 1.125rem; height: 1.125rem; padding: 0; border: 0; border-radius: 0.25rem;
    background: none; color: var(--text-3); cursor: pointer; transition: transform 200ms var(--spring);
  }
  .chev :global(svg) { color: var(--text-3); }
  .chev.open { transform: rotate(90deg); }
</style>
