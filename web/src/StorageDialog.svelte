<script>
  // Storage analysis: a treemap of what uses space below a folder (click a box to go deeper) and
  // a duplicate finder (same size, then same content). Deleting a duplicate moves it to the trash.
  import { api } from '$lib/api.js';
  import { runJob } from '$lib/jobs.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes } from '$lib/format.js';
  import Dialog from '$lib/Dialog.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import JobLog from '$lib/JobLog.svelte';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';

  let { open = $bindable(false), path = '', onChanged, onSessionLost } = $props();
  let tab = $state('usage'), stack = $state([]), loading = $state(false), error = $state(''), complete = $state(true);
  let dupes = $state(null), dupeBusy = $state(false), dupeLines = $state([]), dupeError = $state(''), w = $state(0), h = $state(0);
  const node = $derived(stack.at(-1));

  $effect(() => { if (open) { tab = 'usage'; dupes = null; dupeError = ''; scan(path, true); } });

  async function scan(p, reset) {
    loading = true; error = '';
    const res = await api('/api/files/analyze?path=' + encodeURIComponent(p));
    loading = false;
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) { error = res.error; return; }
    complete = res.data.complete;
    stack = reset ? [res.data.tree] : [...stack, res.data.tree];
  }

  function enter(c) {
    if (!c.dir) return;
    if (c.children?.length) stack = [...stack, c];
    else scan(c.path, false); // deeper than the first scan: scan that folder
  }

  // Squarified treemap (Bruls et al.): rows of boxes whose aspect ratio stays close to 1.
  function layout(items, x, y, width, height) {
    const total = items.reduce((s, i) => s + i.size, 0);
    if (!total || width <= 0 || height <= 0) return [];
    const scale = (width * height) / total;
    const rest = items.map((i) => ({ item: i, area: i.size * scale })).filter((r) => r.area > 0);
    const out = [];
    let row = [];
    const worst = (r, side) => {
      const s = r.reduce((a, b) => a + b.area, 0);
      const mx = Math.max(...r.map((b) => b.area)), mn = Math.min(...r.map((b) => b.area));
      return Math.max((side * side * mx) / (s * s), (s * s) / (side * side * mn));
    };
    const place = (r) => {
      const s = r.reduce((a, b) => a + b.area, 0);
      if (width >= height) {
        const cw = s / height;
        let cy = y;
        for (const b of r) { const bh = b.area / cw; out.push({ ...b, x, y: cy, w: cw, h: bh }); cy += bh; }
        x += cw; width -= cw;
      } else {
        const ch = s / width;
        let cx = x;
        for (const b of r) { const bw = b.area / ch; out.push({ ...b, x: cx, y, w: bw, h: ch }); cx += bw; }
        y += ch; height -= ch;
      }
    };
    for (const b of rest) {
      const side = Math.min(width, height);
      if (!row.length || worst([...row, b], side) <= worst(row, side)) row.push(b);
      else { place(row); row = [b]; }
    }
    if (row.length) place(row);
    return out;
  }
  const boxes = $derived(node ? layout([...(node.children || []), ...(node.others ? [{ name: 'Other items', size: node.others, other: true }] : [])], 0, 0, w, h) : []);

  async function findDupes() {
    dupeBusy = true; dupeError = ''; dupeLines = [];
    const job = await runJob('/api/files/dupes', { path }, (j) => (dupeLines = j.lines));
    dupeBusy = false;
    if (job.http === 401) return onSessionLost?.();
    if (job.status !== 'done') { dupeError = job.error; return; }
    dupes = job.result;
  }

  async function trashCopy(g, p) {
    const res = await api('/api/files/delete', { method: 'POST', body: { path: p } });
    if (res.status === 401) return onSessionLost?.();
    if (!res.ok) return toast(res.error, 'error', 8000);
    g.paths = g.paths.filter((x) => x !== p);
    dupes = { ...dupes, groups: dupes.groups.filter((x) => x.paths.length > 1) };
    toast(`${p.split('/').pop()} moved to the trash.`, 'success');
    onChanged?.();
  }
  const wasted = $derived(dupes ? dupes.groups.reduce((s, g) => s + g.size * (g.paths.length - 1), 0) : 0);
</script>

<Dialog bind:open label="Storage analysis" wide>
  <div class="dlg">
    <div class="head">
      <h2 class="t-title">Storage analysis</h2>
      <Segmented options={[{ value: 'usage', label: 'What uses space' }, { value: 'dupes', label: 'Duplicates' }]} bind:value={tab} label="View" />
    </div>
    {#if tab === 'usage'}
      <div class="crumbs">
        {#if stack.length > 1}<button class="link" onclick={() => (stack = stack.slice(0, -1))}><ChevronLeft size={15} />Back</button>{/if}
        <span class="mono truncate">{node?.path || path}</span>
        {#if node}<span class="t-caption">{bytes(node.size)}{complete ? '' : ' (stopped early, the folder is very large)'}</span>{/if}
      </div>
      {#if error}<div class="notice"><CircleAlert size={18} /><span>{error}</span></div>
      {:else}
        <div class="map" bind:clientWidth={w} bind:clientHeight={h} aria-busy={loading}>
          {#if loading}<div class="center t-sub"><Loader size={18} class="spinner" />Measuring</div>{/if}
          {#each boxes as b (b.item.path || b.item.name)}
            <button class="box" class:dir={b.item.dir} class:other={b.item.other} style:left="{b.x}px" style:top="{b.y}px" style:width="{b.w}px" style:height="{b.h}px"
              onclick={() => enter(b.item)} title="{b.item.name}: {bytes(b.item.size)}" disabled={!b.item.dir}>
              {#if b.w > 60 && b.h > 34}<span class="truncate">{b.item.name}</span><span class="t-caption">{bytes(b.item.size)}</span>{/if}
            </button>
          {/each}
          {#if !loading && node && !boxes.length}<div class="center t-sub">This folder is empty.</div>{/if}
        </div>
      {/if}
    {:else}
      <p class="t-sub">Finds files of 1 MB or more with exactly the same content below {path}. Deleting a copy moves it to the trash.</p>
      {#if dupeError}<div class="notice"><CircleAlert size={18} /><span>{dupeError}</span></div>{/if}
      {#if dupeBusy}<JobLog lines={dupeLines} />{/if}
      {#if !dupes}
        <button class="btn btn-primary" onclick={findDupes} disabled={dupeBusy}>{#if dupeBusy}<Loader size={16} class="spinner" />Searching{:else}Find duplicates{/if}</button>
      {:else}
        <p class="t-head">{dupes.groups.length ? `${dupes.groups.length} groups, ${bytes(wasted)} to win` : 'No duplicates found.'}{dupes.complete ? '' : ' (stopped early)'}</p>
        <div class="dupes">
          {#each dupes.groups as g (g.hash)}
            <div class="group">
              <div class="row t-caption">{g.paths.length} copies of {bytes(g.size)}</div>
              {#each g.paths as p (p)}
                <div class="row"><span class="mono truncate grow" title={p}>{p}</span><button class="icon-btn" onclick={() => trashCopy(g, p)} aria-label="Move {p} to the trash" disabled={g.paths.length < 2}><Trash2 size={16} /></button></div>
              {/each}
            </div>
          {/each}
        </div>
      {/if}
    {/if}
    <div class="buttons"><button class="btn btn-gray" data-close onclick={() => (open = false)}>Close</button></div>
  </div>
</Dialog>

<style>
  h2, p { margin: 0; }
  .dlg { display: grid; gap: 0.875rem; padding: 1.5rem; }
  .head { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: center; gap: 0.75rem; }
  .crumbs { display: flex; align-items: center; gap: 0.75rem; min-width: 0; }
  .link { display: inline-flex; align-items: center; gap: 0.125rem; border: 0; background: none; padding: 0; color: var(--accent); font: inherit; font-weight: 600; cursor: pointer; }
  .map { position: relative; height: min(26rem, 55vh); border-radius: var(--r-control); background: var(--fill); overflow: hidden; }
  .box {
    position: absolute; display: flex; flex-direction: column; align-items: flex-start; justify-content: flex-start; gap: 0.125rem; padding: 0.375rem 0.5rem;
    border: 0; box-shadow: inset 0 0 0 1px var(--surface); background: color-mix(in srgb, var(--accent) 22%, var(--surface)); color: var(--text);
    font: inherit; font-size: 0.75rem; text-align: left; overflow: hidden; cursor: default;
  }
  .box.dir { background: color-mix(in srgb, var(--accent) 38%, var(--surface)); cursor: pointer; }
  .box.dir:hover { background: color-mix(in srgb, var(--accent) 50%, var(--surface)); }
  .box.other { background: var(--fill); color: var(--text-2); }
  .box span { max-width: 100%; }
  .center { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; gap: 0.5rem; }
  .dupes { display: grid; gap: 0.75rem; max-height: 22rem; overflow: auto; }
  .grow { flex: 1; min-width: 0; }
  .buttons { display: flex; justify-content: flex-end; }
</style>
