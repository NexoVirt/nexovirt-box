<script>
  // The one data table of Box. A card with an optional head (title, description, search, filters and
  // buttons), a sticky header, sortable columns, row actions in a menu (button or right-click),
  // optional selection and expandable rows, loading, error and empty states, and paging while you
  // scroll: client side (rows are all there, shown in pages) or server side (onmore loads the next
  // page). On phones every row stacks into a small card with the column names as labels.
  // With `fill` the card takes the remaining height of a fill page and scrolls inside.
  import { onMount } from 'svelte';
  import ContextMenu from './ContextMenu.svelte';
  import ArrowUp from '@lucide/svelte/icons/arrow-up';
  import ArrowDown from '@lucide/svelte/icons/arrow-down';
  import Search from '@lucide/svelte/icons/search';
  import X from '@lucide/svelte/icons/x';
  import Ellipsis from '@lucide/svelte/icons/ellipsis';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import Inbox from '@lucide/svelte/icons/inbox';

  let {
    columns, // [{ key, label, sortable, value(row), width, align: 'right', mono, wrap, primary, hide: 'md'|'sm', cls }]
    rows = null, // null = still loading
    key = 'id', // field name or (row) => id
    cell, // snippet(row, col); without it the cell shows row[col.key]
    label = 'Table',
    title = '', description = '',
    toolbar, // snippet: filters and buttons on the right of the head
    searchable = false, search = $bindable(''), placeholder = 'Search', searchText,
    server = false, onsearch, onsort, onmore, hasMore = false, busy = false, total,
    sort = $bindable(null), // { key, dir: 'asc' | 'desc' }
    error = '', onretry,
    empty = {}, // { icon, title, text }
    emptyAction, // snippet under the empty text
    actions, // (row) => [{ label, icon, danger, disabled, run }]
    selectable = false, selected = $bindable(new Set()),
    expandable, detail, // (row) => bool, snippet(row)
    onrowclick, onrowdblclick, rowClass,
    fill = false, fixed = false, maxHeight = '36rem', pageSize = 100,
    foot, footText,
  } = $props();

  let scroller = $state(), sentinel = $state();
  let limit = $state(pageSize);
  let open = $state(new Set());
  let menu = $state(null), menuItems = [];
  let searchTimer;

  const keyOf = (r) => (typeof key === 'function' ? key(r) : r[key]);
  const valueOf = (c, r) => (c.value ? c.value(r) : r[c.key]);
  // A cell whose value is empty is left out of the stacked phone rows (columns without a value stay).
  const blank = (c, r) => (c.value || c.key in r) && (valueOf(c, r) == null || valueOf(c, r) === '');
  const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });
  function cmp(a, b) {
    if (a == null || a === '') return b == null || b === '' ? 0 : 1;
    if (b == null || b === '') return -1;
    if (typeof a === 'number' && typeof b === 'number') return a - b;
    if (typeof a === 'boolean') return (a ? 1 : 0) - (b ? 1 : 0);
    return collator.compare(String(a), String(b));
  }

  let filtered = $derived.by(() => {
    if (!rows) return [];
    let out = rows;
    const q = search.trim().toLowerCase();
    if (!server && q) {
      const text = searchText || ((r) => columns.map((c) => valueOf(c, r)).filter((v) => v != null).join(' '));
      out = out.filter((r) => text(r).toLowerCase().includes(q));
    }
    if (!server && sort?.key) {
      const c = columns.find((x) => x.key === sort.key);
      if (c) {
        const dir = sort.dir === 'desc' ? -1 : 1;
        out = [...out].sort((a, b) => cmp(valueOf(c, a), valueOf(c, b)) * dir);
      }
    }
    return out;
  });
  let shown = $derived(server ? filtered : filtered.slice(0, limit));
  let span = $derived(columns.length + (selectable ? 1 : 0) + (actions || expandable ? 1 : 0));
  let hasHead = $derived(!!(title || description || toolbar || searchable));
  let allOn = $derived(selectable && filtered.length > 0 && filtered.every((r) => selected.has(keyOf(r))));
  let someOn = $derived(selectable && !allOn && filtered.some((r) => selected.has(keyOf(r))));

  // Back to the first page whenever the list changes shape.
  $effect(() => { search; sort; limit = pageSize; });

  function sortBy(c) {
    if (!c.sortable) return;
    const dir = sort?.key === c.key ? (sort.dir === 'asc' ? 'desc' : 'asc') : c.firstDir || 'asc';
    sort = { key: c.key, dir };
    onsort?.(sort);
    scroller?.scrollTo({ top: 0 });
  }

  function typed() {
    if (!server) return;
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => onsearch?.(search), 250);
  }
  function clearSearch() { search = ''; if (server) onsearch?.(''); }

  function toggleRow(r) {
    const k = keyOf(r), next = new Set(selected);
    next.has(k) ? next.delete(k) : next.add(k);
    selected = next;
  }
  function toggleAll() {
    const next = new Set(selected);
    for (const r of filtered) allOn ? next.delete(keyOf(r)) : next.add(keyOf(r));
    selected = next;
  }
  function toggleOpen(r) {
    const k = keyOf(r), next = new Set(open);
    next.has(k) ? next.delete(k) : next.add(k);
    open = next;
  }

  function click(e, r) {
    if (e.target.closest('button, a, input, select, label, textarea')) return;
    if (onrowclick) onrowclick(r);
    else if (expandable?.(r)) toggleOpen(r);
    else if (selectable) toggleRow(r);
  }

  function showMenu(x, y, r) {
    menuItems = actions(r).filter(Boolean);
    if (!menuItems.length) return;
    menu = { x, y, items: menuItems.map((a, i) => ({ key: i, label: a.label, icon: a.icon, danger: a.danger, disabled: a.disabled })) };
  }
  function menuFromButton(e, r) {
    const b = e.currentTarget.getBoundingClientRect();
    showMenu(Math.max(8, b.right - 224), b.bottom + 4, r);
  }
  function context(e, r) {
    if (!actions) return;
    e.preventDefault();
    showMenu(e.clientX, e.clientY, r);
  }
  function pick(k) { menuItems[k]?.run?.(); }

  onMount(() => {
    // Next page once the end of the list scrolls into view.
    const io = new IntersectionObserver((e) => {
      if (!e[0].isIntersecting) return;
      if (server) { if (hasMore && !busy) onmore?.(); }
      else if (limit < filtered.length) limit += pageSize;
    }, { root: scroller, rootMargin: '300px' });
    io.observe(sentinel);
    return () => io.disconnect();
  });
</script>

<section class="dt tile" class:fill aria-label={label}>
  {#if hasHead}
    <div class="head">
      {#if title || description}
        <div class="title">
          {#if title}<h2 class="t-head">{title}</h2>{/if}
          {#if description}<p class="t-caption">{description}</p>{/if}
        </div>
      {/if}
      <div class="filters">
        {#if searchable}
          <label class="search">
            <Search size={14} />
            <input bind:value={search} oninput={typed} {placeholder} aria-label={placeholder} spellcheck="false" />
            {#if search}<button class="clear" onclick={clearSearch} aria-label="Clear search"><X size={13} /></button>{/if}
          </label>
        {/if}
        {#if toolbar}{@render toolbar()}{/if}
      </div>
    </div>
  {/if}

  <div class="scroller" class:top={!hasHead} bind:this={scroller} style:--dt-max={fill ? 'none' : maxHeight}>
    {#if rows !== null && !error && shown.length}
      <table class:fixed>
        <thead>
          <tr>
            {#if selectable}
              <th class="c-select"><input type="checkbox" checked={allOn} indeterminate={someOn} onchange={toggleAll} aria-label="Select all" /></th>
            {/if}
            {#each columns as c (c.key)}
              <th class="{c.cls || ''} {c.hide ? 'hide-' + c.hide : ''}" class:right={c.align === 'right'} style:width={c.width}
                aria-sort={sort?.key === c.key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : undefined}>
                {#if c.sortable}
                  <button class="sort" class:on={sort?.key === c.key} onclick={() => sortBy(c)}>
                    {c.label}
                    {#if sort?.key === c.key}{#if sort.dir === 'asc'}<ArrowUp size={12} />{:else}<ArrowDown size={12} />{/if}{/if}
                  </button>
                {:else}{c.label}{/if}
              </th>
            {/each}
            {#if actions || expandable}<th class="c-actions"><span class="sr">Actions</span></th>{/if}
          </tr>
        </thead>
        <tbody>
          {#each shown as r (keyOf(r))}
            {@const k = keyOf(r)}
            {@const canOpen = expandable?.(r)}
            <tr class={rowClass?.(r) || ''} class:on={selectable && selected.has(k)} class:open={open.has(k)}
              class:clickable={!!onrowclick || canOpen || selectable}
              onclick={(e) => click(e, r)} ondblclick={() => onrowdblclick?.(r)} oncontextmenu={(e) => context(e, r)}>
              {#if selectable}
                <td class="c-select"><input type="checkbox" checked={selected.has(k)} onchange={() => toggleRow(r)} aria-label="Select row" /></td>
              {/if}
              {#each columns as c (c.key)}
                <td class="{c.cls || ''} {c.hide ? 'hide-' + c.hide : ''}" class:right={c.align === 'right'} class:mono={c.mono} class:wrap={c.wrap}
                  class:primary={c.primary} class:blank={blank(c, r)} data-label={c.primary ? '' : c.label}>
                  <span class="cv">{#if cell}{@render cell(r, c)}{:else}{r[c.key] ?? ''}{/if}</span>
                </td>
              {/each}
              {#if actions || expandable}
                <td class="c-actions" data-label="">
                  {#if actions && actions(r).some(Boolean)}
                    <button class="icon-btn sm" onclick={(e) => menuFromButton(e, r)} aria-haspopup="menu" aria-label="Actions"><Ellipsis size={18} /></button>
                  {/if}
                  {#if canOpen}
                    <button class="icon-btn sm chev" class:turned={open.has(k)} onclick={() => toggleOpen(r)} aria-expanded={open.has(k)} aria-label="Details"><ChevronRight size={16} /></button>
                  {/if}
                </td>
              {/if}
            </tr>
            {#if canOpen && open.has(k) && detail}
              <tr class="detail-row"><td colspan={span}>{@render detail(r)}</td></tr>
            {/if}
          {/each}
        </tbody>
      </table>
    {/if}

    {#if rows === null}
      <div class="pad" aria-busy="true">{#each Array(6) as _}<div class="skeleton line"></div>{/each}</div>
    {:else if error}
      <div class="pad">
        <div class="notice" role="alert"><CircleAlert size={18} /><span>{error}</span></div>
        {#if onretry}<button class="btn btn-gray btn-sm retry" onclick={onretry}>Try again</button>{/if}
      </div>
    {:else if !shown.length && !busy}
      {@const Icon = search.trim() ? Search : empty.icon || Inbox}
      <div class="empty-state">
        <span class="ico xl"><Icon /></span>
        <p class="t-head">{search.trim() ? 'Nothing matches your search' : empty.title || 'Nothing here yet'}</p>
        {#if empty.text && !search.trim()}<p class="t-caption">{empty.text}</p>{/if}
        {#if emptyAction && !search.trim()}<div class="empty-action">{@render emptyAction()}</div>{/if}
      </div>
    {/if}
    <div bind:this={sentinel} class="sentinel">{#if busy && rows?.length}<Loader size={16} class="spinner" />{/if}</div>
  </div>

  {#if foot || footText || (rows && (server ? total !== undefined : filtered.length > pageSize))}
    <div class="foot t-caption num">
      {#if foot}{@render foot()}
      {:else if footText}{footText}
      {:else}Showing {shown.length} of {server ? total : filtered.length}{/if}
    </div>
  {/if}
</section>

<ContextMenu bind:menu onPick={pick} />

<style>
  .dt { display: flex; flex-direction: column; padding: 0; min-width: 0; }
  .dt.fill { flex: 1; min-height: 0; }
  .head { display: flex; align-items: center; justify-content: space-between; gap: 0.75rem 1rem; flex-wrap: wrap; padding: 0.875rem 1rem 0.875rem 1.25rem; border-bottom: 1px solid var(--hairline); }
  .title { display: grid; gap: 0.125rem; min-width: 0; }
  .title h2, .title p { margin: 0; }
  .filters { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; margin-left: auto; }
  .search { display: flex; align-items: center; gap: 0.375rem; height: 2.25rem; padding: 0 0.625rem; border-radius: 0.625rem; background: var(--fill); color: var(--text-3); width: 13rem; }
  .search input { flex: 1; min-width: 0; border: 0; background: none; font-size: 0.8125rem; color: var(--text); }
  .search input::placeholder { color: var(--text-3); }
  .search input:focus-visible { outline: none; }
  .search:focus-within { box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 45%, transparent); }
  .clear { display: grid; place-items: center; width: 1.25rem; height: 1.25rem; padding: 0; border: 0; border-radius: 50%; background: var(--fill-2); color: var(--text-2); cursor: pointer; }
  /* Filters and buttons in the toolbar snippet share these sizes. */
  .filters :global(select.input) { height: 2.25rem; width: auto; min-width: 9rem; font-size: 0.8125rem; border-radius: 0.625rem; }
  .filters :global(.icon-btn) { width: 2.25rem; height: 2.25rem; }

  .scroller { flex: 1; min-height: 0; max-height: var(--dt-max); overflow: auto; border-radius: 0 0 var(--r-tile) var(--r-tile); }
  .scroller.top { border-radius: var(--r-tile); }
  table { width: 100%; border-collapse: separate; border-spacing: 0; font-size: 0.8125rem; }
  table.fixed { table-layout: fixed; }
  thead th {
    position: sticky; top: 0; z-index: 1; height: 2.25rem; padding: 0 0.75rem; text-align: left; font-size: 0.75rem; font-weight: 600; color: var(--text-2); white-space: nowrap;
    background: color-mix(in srgb, var(--surface) 90%, transparent); -webkit-backdrop-filter: blur(20px); backdrop-filter: blur(20px);
    border-bottom: 1px solid var(--hairline);
  }
  td { height: 2.875rem; padding: 0.25rem 0.75rem; border-bottom: 1px solid var(--hairline); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 28rem; }
  td.wrap { white-space: normal; }
  th:first-child, td:first-child { padding-left: 1.25rem; }
  th:last-child, td:last-child { padding-right: 1rem; }
  tbody tr:last-child td { border-bottom: 0; }
  tbody tr:hover td { background: var(--fill); }
  tr.on td { background: var(--accent-fill); }
  tr.clickable { cursor: pointer; }
  .right { text-align: right; }
  .primary { font-weight: 500; }
  .cv { display: contents; }
  .sort { display: inline-flex; align-items: center; gap: 0.25rem; padding: 0; border: 0; background: none; color: inherit; font: inherit; cursor: pointer; }
  .sort:hover, .sort.on { color: var(--text); }
  .c-select { width: 2.75rem; }
  .c-select input { width: 1rem; height: 1rem; accent-color: var(--accent); cursor: pointer; }
  .c-actions { width: 1%; text-align: right; white-space: nowrap; overflow: visible; }
  .c-actions .icon-btn { width: 2.25rem; height: 2.25rem; vertical-align: middle; }
  .chev :global(svg) { transition: transform 250ms var(--spring); }
  .chev.turned :global(svg) { transform: rotate(90deg); }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
  tr.detail-row td { height: auto; padding: 0; white-space: normal; max-width: none; }
  tr.detail-row:hover td { background: none; }

  .pad { padding: 1rem 1.25rem; display: grid; gap: 0.75rem; }
  .retry { justify-self: start; }
  .skeleton.line { height: 1.25rem; }
  .empty-action { margin-top: 0.625rem; }
  .sentinel { display: grid; place-items: center; min-height: 0.5rem; color: var(--text-3); }
  .sentinel:not(:empty) { min-height: 2.5rem; }
  .foot { display: flex; align-items: center; gap: 0.75rem; min-height: 2.25rem; padding: 0 1.25rem; border-top: 1px solid var(--hairline); }

  @media (min-width: 640px) and (max-width: 899px) { .hide-md { display: none; } }
  /* Phones: every row is a small stacked card, column names as labels. */
  @media (max-width: 639px) {
    .head { padding: 0.875rem 1rem; }
    .search { width: 100%; flex: 1 1 100%; }
    .filters { width: 100%; margin-left: 0; }
    .filters :global(select.input) { flex: 1; min-width: 0; }
    .hide-sm, td.blank { display: none !important; }
    table, tbody, tr, td { display: block; width: auto !important; max-width: none; }
    thead { display: none; }
    tbody tr { position: relative; display: flex; flex-direction: column; padding: 0.75rem 1rem; border-bottom: 1px solid var(--hairline); }
    td.primary { order: -1; }
    .cv { display: block; min-width: 0; }
    tbody tr:last-child { border-bottom: 0; }
    td { height: auto; padding: 0.1875rem 0; border: 0 !important; display: flex; align-items: center; justify-content: space-between; gap: 1rem; text-align: right; white-space: normal; overflow-wrap: anywhere; }
    td:first-child, td:last-child { padding-left: 0; padding-right: 0; }
    td::before { content: attr(data-label); flex-shrink: 0; color: var(--text-2); font-size: 0.75rem; font-family: var(--font); text-align: left; }
    td[data-label='']::before { content: none; }
    td.primary { justify-content: flex-start; text-align: left; font-weight: 600; padding-right: 5rem; }
    td.c-select { position: absolute; top: 0.875rem; left: 1rem; }
    tr:has(td.c-select) { padding-left: 2.75rem; }
    td.c-actions { position: absolute; top: 0.375rem; right: 0.5rem; padding: 0; }
    tbody tr:hover td { background: none; }
    tr.on { background: var(--accent-fill); }
    tr.detail-row { padding: 0; }
    tr.detail-row td { display: block; }
  }
</style>
