<script>
  // Settings > General > Theme: presets with light and dark variants, and an editor (accent,
  // neutral tone, surface, corners, density, text size). Every change previews live on the whole
  // UI; Save keeps it for the account, Revert goes back, Reset returns to the preset. Own themes
  // can be saved by name and exported or imported as JSON.
  import { onDestroy } from 'svelte';
  import { toast } from '$lib/toast.svelte.js';
  import { theme as mode } from '$lib/theme.svelte.js';
  import { desktop, saveDesktop } from '$lib/desktop.svelte.js';
  import { PRESETS, SWATCHES, DEFAULT_THEME, fromPreset, presetOf, accentFor, buildVars, cleanTheme, importTheme, exportTheme, contrast } from '$lib/themes.js';
  import Segmented from '$lib/Segmented.svelte';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import Download from '@lucide/svelte/icons/download';
  import Upload from '@lucide/svelte/icons/upload';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import ImageIcon from '@lucide/svelte/icons/image';

  const NEUTRAL = [{ value: 'cool', label: 'Cool' }, { value: 'neutral', label: 'Neutral' }, { value: 'warm', label: 'Warm' }];
  const SURFACE = [{ value: 'solid', label: 'Solid' }, { value: 'glass', label: 'Subtle glass' }];
  const RADIUS = [{ value: 'small', label: 'Small' }, { value: 'medium', label: 'Medium' }, { value: 'large', label: 'Large' }];
  const DENSITY = [{ value: 'comfortable', label: 'Comfortable' }, { value: 'compact', label: 'Compact' }];

  const stored = () => { const t = desktop.prefs?.theme; return t ? cleanTheme(t) : { ...DEFAULT_THEME }; };
  const saved = $derived(desktop.prefs?.theme?.saved || []);
  let draft = $state(stored());
  let saving = $state(false);
  let saveName = $state('');
  let fileEl = $state();

  // Follow the saved theme until the user changes something here.
  let touched = false;
  $effect(() => { const t = stored(); if (!touched) draft = t; });
  const dirty = $derived(JSON.stringify(draft) !== JSON.stringify(stored()));
  $effect(() => { desktop.preview = dirty ? { ...draft } : null; });
  onDestroy(() => (desktop.preview = null));

  const dark = $derived(mode.current === 'dark');
  const acc = $derived(accentFor(draft, dark));
  const linkRatio = $derived(contrast(acc.accent, buildVars(draft, dark).vars['--surface']));

  function set(key, value) { touched = true; draft = { ...draft, [key]: value }; }
  function pickPreset(id) { touched = true; draft = { ...fromPreset(id), scale: draft.scale, density: draft.density }; }

  async function persist(next, savedList = saved) {
    saving = true;
    const ok = await saveDesktop({ theme: { ...next, saved: savedList } });
    saving = false;
    return ok;
  }
  async function save() { if (await persist(draft)) { touched = false; toast('Theme saved.', 'success'); } }
  function revert() { touched = false; draft = stored(); }
  function reset() { draft = { ...fromPreset(draft.preset) }; }

  async function saveAs(e) {
    e.preventDefault();
    const name = saveName.trim().slice(0, 40);
    if (!name) return;
    const list = [...saved.filter((s) => s.name.toLowerCase() !== name.toLowerCase()), { name, ...draft }];
    if (list.length > 20) return toast('You can keep up to 20 own themes.', 'error');
    if (await persist(draft, list)) { saveName = ''; toast(`Saved as "${name}".`, 'success'); }
  }
  function useSaved(s) { touched = true; const { name, ...t } = s; draft = cleanTheme(t); }
  async function removeSaved(s) { await persist(stored(), saved.filter((x) => x.name !== s.name)); }

  function download(name, t) {
    const blob = new Blob([exportTheme(name, t)], { type: 'application/json' });
    const a = Object.assign(document.createElement('a'), { href: URL.createObjectURL(blob), download: `${name.replace(/[^\w -]/g, '').trim() || 'theme'}.nxbox-theme.json` });
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  }
  async function importFile(e) {
    const file = e.currentTarget.files?.[0];
    e.currentTarget.value = '';
    if (!file) return;
    if (file.size > 16 << 10) return toast('That file is too big for a theme.', 'error');
    try {
      const { name, ...t } = importTheme(await file.text());
      touched = true;
      draft = t;
      saveName = name;
      toast(`"${name}" is shown now. Save it to keep it.`, 'info', 6000);
    } catch (err) { toast(err.message, 'error'); }
  }

  function swatch(p, d) { return buildVars(fromPreset(p.id), d).vars; }
  function toWallpaper() { document.getElementById('wallpaper-settings')?.scrollIntoView({ behavior: 'smooth', block: 'start' }); }
</script>

<p class="t-section sub">Theme</p>
<section class="group">
  <div class="row presets">
    {#each PRESETS as p (p.id)}
      {@const l = swatch(p, false)}
      {@const d = swatch(p, true)}
      <button class="preset" class:on={draft.preset === p.id} onclick={() => pickPreset(p.id)} aria-pressed={draft.preset === p.id}>
        <span class="pv">
          <span class="half" style:background={l['--bg']}><span class="card" style:background={l['--surface']}><span class="pbar" style:background={l['--accent']}></span><span class="line" style:background={l['--text-3']}></span></span></span>
          <span class="half" style:background={d['--bg']}><span class="card" style:background={d['--surface']}><span class="pbar" style:background={d['--accent']}></span><span class="line" style:background={d['--text-3']}></span></span></span>
        </span>
        <span class="t-caption">{p.label}</span>
      </button>
    {/each}
  </div>

  <div class="row">
    <div class="grow"><span>Accent colour</span><span class="t-caption">Buttons, links, selections and gauges.</span></div>
    <div class="accents">
      {#each SWATCHES as c (c)}
        <button class="sw" class:on={draft.accent.toLowerCase() === c} style:background={c} onclick={() => set('accent', c)} aria-label="Accent {c}"></button>
      {/each}
      <input class="picker" type="color" value={draft.accent || presetOf(draft.preset).accent[0]} oninput={(e) => set('accent', e.currentTarget.value)} aria-label="Pick any accent colour" />
      {#if draft.accent}<button class="btn btn-gray btn-sm" onclick={() => set('accent', '')}>Preset</button>{/if}
    </div>
  </div>
  {#if acc.adjusted || linkRatio < 3}
    <div class="row">
      <span class="ico warn"><TriangleAlert /></span>
      <span class="t-caption grow">
        {#if acc.adjusted}{acc.original} is too light or too dark for readable button text, so Box uses {acc.accent} in {dark ? 'dark' : 'light'} mode (contrast {acc.ratio.toFixed(1)}:1).{/if}
        {#if linkRatio < 3} Links in this colour are hard to read on the background ({linkRatio.toFixed(1)}:1).{/if}
      </span>
    </div>
  {/if}
  <div class="row"><div class="grow"><span>Neutral tone</span><span class="t-caption">The greys of backgrounds and text.</span></div><Segmented options={NEUTRAL} value={draft.neutral} onchange={(v) => set('neutral', v)} label="Neutral tone" /></div>
  <div class="row"><div class="grow"><span>Surfaces on the desktop</span><span class="t-caption">Glass is turned off when your device asks for less transparency.</span></div><Segmented options={SURFACE} value={draft.surface} onchange={(v) => set('surface', v)} label="Surfaces" /></div>
  <div class="row"><div class="grow"><span>Corners</span></div><Segmented options={RADIUS} value={draft.radius} onchange={(v) => set('radius', v)} label="Corner radius" /></div>
  <div class="row"><div class="grow"><span>Density</span></div><Segmented options={DENSITY} value={draft.density} onchange={(v) => set('density', v)} label="Density" /></div>
  <label class="row">
    <div class="grow"><span>Text size</span></div>
    <input class="range" type="range" min="85" max="125" step="5" value={draft.scale} oninput={(e) => set('scale', +e.currentTarget.value)} aria-label="Text size" />
    <span class="val num">{draft.scale} %</span>
  </label>
  <div class="row">
    <span class="ico"><ImageIcon /></span>
    <div class="grow"><span>Wallpaper</span><span class="t-caption">Picked below, saved on its own.</span></div>
    <button class="btn btn-gray btn-sm" onclick={toWallpaper}>Choose wallpaper</button>
  </div>
  <div class="row actions">
    <span class="t-caption grow">{dirty ? 'Preview: not saved yet.' : 'Saved for your account.'}</span>
    <button class="btn btn-gray btn-sm" onclick={reset}>Reset to preset</button>
    <button class="btn btn-gray btn-sm" onclick={revert} disabled={!dirty}>Revert</button>
    <button class="btn btn-primary btn-sm" onclick={save} disabled={!dirty || saving}>{#if saving}<Loader size={15} class="spinner" />{/if}Save</button>
  </div>
</section>

<p class="t-section sub">Own themes</p>
<section class="group">
  <form class="row" onsubmit={saveAs}>
    <input class="input" bind:value={saveName} maxlength="40" placeholder="Name, e.g. Office" aria-label="Theme name" />
    <button class="btn btn-tinted btn-sm" disabled={!saveName.trim() || saving}>Save as</button>
    <button type="button" class="btn btn-gray btn-sm" onclick={() => fileEl.click()}><Upload size={15} />Import</button>
    <input bind:this={fileEl} type="file" accept="application/json,.json" hidden onchange={importFile} />
  </form>
  {#each saved as s (s.name)}
    {@const v = buildVars(s, dark).vars}
    <div class="row">
      <span class="mini" style:background={v['--bg']}><span style:background={v['--accent']}></span></span>
      <span class="grow truncate">{s.name}</span>
      <button class="btn btn-gray btn-sm" onclick={() => useSaved(s)}>Use</button>
      <button class="icon-btn" onclick={() => download(s.name, s)} aria-label="Export {s.name}" title="Export as JSON"><Download size={17} /></button>
      <button class="icon-btn" onclick={() => removeSaved(s)} aria-label="Delete {s.name}" title="Delete"><Trash2 size={17} /></button>
    </div>
  {:else}
    <div class="row"><span class="t-caption">Save the current look under a name, or import a theme file. Export a theme from its row.</span></div>
  {/each}
</section>

<style>
  .sub { margin: 0.75rem 1.125rem 0; }
  .presets { flex-wrap: wrap; gap: 0.875rem 0.75rem; padding-block: 1rem; }
  .preset { display: grid; justify-items: center; gap: 0.375rem; padding: 0; border: 0; background: none; font: inherit; color: inherit; cursor: pointer; }
  .preset:active .pv { transform: scale(0.96); }
  .pv { display: flex; width: 6rem; height: 3.75rem; border-radius: 0.75rem; overflow: hidden; box-shadow: inset 0 0 0 1px var(--hairline); transition: transform 120ms ease-out, box-shadow 150ms ease; }
  .preset.on .pv { box-shadow: 0 0 0 2px var(--surface), 0 0 0 4px var(--accent); }
  .preset.on .t-caption { color: var(--text); font-weight: 600; }
  .half { flex: 1; display: grid; place-items: center; }
  .card { display: grid; gap: 4px; width: 70%; padding: 6px; border-radius: 5px; }
  .card .pbar { height: 6px; border-radius: 3px; }
  .card .line { height: 3px; width: 70%; border-radius: 2px; }
  .accents { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 0.375rem; max-width: 22rem; }
  .sw { width: 1.5rem; height: 1.5rem; border: 0; border-radius: 50%; cursor: pointer; box-shadow: inset 0 0 0 1px rgba(0, 0, 0, 0.1); transition: transform 100ms ease-out; }
  .sw:active { transform: scale(0.9); }
  .sw.on { box-shadow: 0 0 0 2px var(--surface), 0 0 0 4px var(--text); }
  .picker { width: 2rem; height: 1.75rem; padding: 0; border: 0; background: none; cursor: pointer; }
  .range { width: min(12rem, 40vw); accent-color: var(--accent); }
  .val { min-width: 3.25rem; text-align: right; color: var(--text-2); }
  .actions { flex-wrap: wrap; justify-content: flex-end; }
  .mini { position: relative; width: 2rem; height: 1.375rem; border-radius: 0.375rem; box-shadow: inset 0 0 0 1px var(--hairline); flex-shrink: 0; }
  .mini span { position: absolute; left: 5px; right: 5px; top: 8px; height: 5px; border-radius: 3px; }
  form.row .input { flex: 1; min-width: 8rem; height: 2.25rem; }
  @media (max-width: 639px) {
    .row { flex-wrap: wrap; }
    .accents { max-width: none; justify-content: flex-start; }
    .range { flex: 1; width: auto; }
  }
</style>
