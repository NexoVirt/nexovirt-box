<script>
  // Settings > General > Wallpaper: built-in flat colours and pictures, your own picture, and dim
  // and blur so text on the desktop stays readable. Saved for the account (GET/POST /api/desktop).
  import { onMount } from 'svelte';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { theme } from '$lib/theme.svelte.js';
  import { desktop, loadDesktop, saveDesktop, WALLPAPERS } from '$lib/desktop.svelte.js';
  import Upload from '@lucide/svelte/icons/upload';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { onSessionLost } = $props();
  let uploading = $state(false);
  let fileEl = $state();
  const prefs = $derived(desktop.prefs);

  function preview(w) {
    if (w.id === 'auto') return { color: theme.current === 'dark' ? '#111113' : '#e4e4e7' };
    return w;
  }

  async function upload(e) {
    const file = e.currentTarget.files?.[0];
    e.currentTarget.value = '';
    if (!file) return;
    if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type)) return toast('Use a JPEG, PNG or WebP picture.', 'error');
    if (file.size > 8 << 20) return toast('The picture can have at most 8 MB.', 'error');
    uploading = true;
    const data = await new Promise((ok) => { const r = new FileReader(); r.onload = () => ok(r.result); r.onerror = () => ok(''); r.readAsDataURL(file); });
    const res = data ? await api('/api/desktop/wallpaper', { method: 'POST', body: { data } }) : { ok: false, error: 'The picture could not be read.' };
    uploading = false;
    if (res.status === 401) return onSessionLost();
    if (!res.ok) return toast(res.error, 'error');
    desktop.prefs = res.data;
    toast('Your picture is the wallpaper now.', 'success');
  }

  // Sliders preview live and save when let go.
  function slide(key, e) { desktop.prefs = { ...desktop.prefs, [key]: +e.currentTarget.value }; }
  function commit(key, e) { saveDesktop({ [key]: +e.currentTarget.value }); }

  onMount(() => { if (!desktop.prefs) loadDesktop(); });
</script>

<p class="t-section sub">Wallpaper</p>
<section class="group">
  <div class="row walls">
    {#if !prefs}
      {#each Array(9) as _}<span class="skeleton swatch-skel"></span>{/each}
    {:else}
      {#each WALLPAPERS as w (w.id)}
        {@const p = preview(w)}
        <button class="wall" class:on={prefs.wallpaper === w.id} onclick={() => saveDesktop({ wallpaper: w.id })} aria-pressed={prefs.wallpaper === w.id} title={w.hint || w.label}>
          <span class="swatch" style:background-color={p.color} style:background-image={p.image ? `url('${p.image}')` : null}>
          </span>
          <span class="t-caption">{w.label}</span>
        </button>
      {/each}
      <button class="wall" class:on={prefs.wallpaper === 'custom'} onclick={() => (prefs.custom_v ? saveDesktop({ wallpaper: 'custom' }) : fileEl.click())} aria-pressed={prefs.wallpaper === 'custom'} disabled={uploading}>
        <span class="swatch custom" style:background-image={prefs.custom_v ? `url('/api/desktop/wallpaper?v=${prefs.custom_v}')` : null}>
          {#if uploading}<Loader size={20} class="spinner" />{:else if !prefs.custom_v}<Upload size={20} />{/if}
        </span>
        <span class="t-caption">Your picture</span>
      </button>
    {/if}
  </div>
  <div class="row">
    <div class="grow"><span>Upload your own</span><span class="t-caption">JPEG, PNG or WebP up to 8 MB. Only you see it.</span></div>
    <button class="btn btn-gray btn-sm" onclick={() => fileEl.click()} disabled={uploading}>{#if uploading}<Loader size={15} class="spinner" />Uploading{:else}<Upload size={15} />Choose picture{/if}</button>
    <input bind:this={fileEl} type="file" accept="image/jpeg,image/png,image/webp" hidden onchange={upload} />
  </div>
  {#if prefs}
    <label class="row">
      <div class="grow"><span>Dim</span><span class="t-caption">Darkens the wallpaper so text stays readable.</span></div>
      <input class="range" type="range" min="0" max="80" step="5" value={prefs.dim} oninput={(e) => slide('dim', e)} onchange={(e) => commit('dim', e)} aria-label="Dim the wallpaper" />
      <span class="val num">{prefs.dim} %</span>
    </label>
    <label class="row">
      <div class="grow"><span>Blur</span><span class="t-caption">Softens a busy picture behind the widgets.</span></div>
      <input class="range" type="range" min="0" max="40" step="2" value={prefs.blur} oninput={(e) => slide('blur', e)} onchange={(e) => commit('blur', e)} aria-label="Blur the wallpaper" />
      <span class="val num">{prefs.blur} px</span>
    </label>
  {/if}
</section>

<style>
  .sub { margin: 0.75rem 1.125rem 0; }
  .walls { flex-wrap: wrap; gap: 0.875rem 0.75rem; padding-block: 1rem; }
  .wall { display: grid; justify-items: center; gap: 0.375rem; width: 5.25rem; padding: 0; border: 0; background: none; font: inherit; color: inherit; cursor: pointer; }
  .wall:active .swatch { transform: scale(0.96); }
  .swatch {
    position: relative; display: grid; place-items: center; width: 5.25rem; aspect-ratio: 16 / 10; border-radius: 0.75rem;
    background-size: cover; background-position: center; box-shadow: inset 0 0 0 1px var(--hairline);
    color: var(--text-2); transition: transform 120ms ease-out, box-shadow 150ms ease;
  }
  .swatch.custom { background-color: var(--fill); }
  .wall.on .swatch { box-shadow: 0 0 0 2px var(--surface), 0 0 0 4px var(--accent); }
  .wall.on .t-caption { color: var(--text); font-weight: 600; }
  .swatch-skel { width: 5.25rem; height: 3.3rem; border-radius: 0.75rem; }
  .range { width: min(12rem, 40vw); accent-color: var(--accent); }
  .val { min-width: 3.25rem; text-align: right; color: var(--text-2); }
  @media (max-width: 639px) {
    .wall, .swatch { width: 4.5rem; }
    label.row { flex-wrap: wrap; }
    .range { flex: 1; width: auto; }
  }
</style>
