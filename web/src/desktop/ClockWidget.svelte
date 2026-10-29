<script>
  // Clock widget: digital large, digital with seconds, analog or minimal; 12/24 h; date format;
  // greeting on or off; an optional second time zone. Options open from the widget's own button
  // and are saved for the account.
  import { onMount } from 'svelte';
  import { saveDesktop } from '$lib/desktop.svelte.js';
  import Dialog from '$lib/Dialog.svelte';
  import Segmented from '$lib/Segmented.svelte';
  import Ellipsis from '@lucide/svelte/icons/ellipsis';

  let { clock, username = '', hostname = '' } = $props();

  const STYLES = [
    { value: 'large', label: 'Large' },
    { value: 'seconds', label: 'Seconds' },
    { value: 'analog', label: 'Analog' },
    { value: 'minimal', label: 'Minimal' },
  ];
  const HOURS = [{ value: 'auto', label: 'Auto' }, { value: '12', label: '12 h' }, { value: '24', label: '24 h' }];
  const DATES = [{ value: 'long', label: 'Long' }, { value: 'short', label: 'Short' }, { value: 'numeric', label: 'Numbers' }, { value: 'off', label: 'Off' }];
  const ZONES = (() => { try { return Intl.supportedValuesOf('timeZone'); } catch { return []; } })();

  let now = $state(new Date());
  let open = $state(false);
  let origin = $state(null);
  let draft = $state(null);

  const c = $derived(clock);
  const fast = $derived(c.style === 'seconds' || c.style === 'analog');
  const h12 = $derived(c.hours === 'auto' ? undefined : c.hours === '12');
  const timeOpts = $derived({ hour: '2-digit', minute: '2-digit', ...(c.style === 'seconds' ? { second: '2-digit' } : {}), ...(h12 === undefined ? {} : { hour12: h12 }) });
  const timeText = $derived(now.toLocaleTimeString(undefined, timeOpts));
  const dateText = $derived(
    c.date === 'off' ? '' :
    c.date === 'numeric' ? now.toLocaleDateString(undefined, { dateStyle: 'short' }) :
    now.toLocaleDateString(undefined, c.date === 'short' ? { weekday: 'short', day: 'numeric', month: 'short' } : { weekday: 'long', day: 'numeric', month: 'long' }),
  );
  const greeting = $derived(now.getHours() < 5 ? 'Good night' : now.getHours() < 12 ? 'Good morning' : now.getHours() < 18 ? 'Good afternoon' : 'Good evening');
  const zone2 = $derived.by(() => {
    if (!c.tz2) return null;
    try {
      const t = now.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', timeZone: c.tz2, ...(h12 === undefined ? {} : { hour12: h12 }) });
      return { name: c.tz2.split('/').pop().replace(/_/g, ' '), t };
    } catch { return null; }
  });
  // Analog hands in degrees.
  const sec = $derived(now.getSeconds());
  const min = $derived(now.getMinutes() + sec / 60);
  const hr = $derived((now.getHours() % 12) + min / 60);

  function edit(e) {
    origin = { x: e.clientX, y: e.clientY };
    draft = { ...c };
    open = true;
  }
  // Each change previews at once and is saved.
  function set(key, value) {
    draft = { ...draft, [key]: value };
    saveDesktop({ clock: draft });
  }

  onMount(() => {
    let t;
    const tick = () => {
      if (!document.hidden) now = new Date();
      t = setTimeout(tick, fast ? 1000 - (Date.now() % 1000) : 10000);
    };
    tick();
    return () => clearTimeout(t);
  });
</script>

<section class="glass widget clock {c.style}" aria-label="Clock">
  <button class="more" onclick={edit} aria-label="Clock options" title="Clock options"><Ellipsis size={18} /></button>
  {#if c.style === 'analog'}
    <div class="analog-row">
      <svg class="face" viewBox="0 0 100 100" aria-hidden="true">
        <circle cx="50" cy="50" r="48" fill="none" stroke="var(--fill-2)" stroke-width="1.5" />
        {#each Array(12) as _, i}
          <line x1="50" y1="6" x2="50" y2={i % 3 === 0 ? 13 : 10} stroke="var(--text-2)" stroke-width={i % 3 === 0 ? 2.4 : 1.4} stroke-linecap="round" transform="rotate({i * 30} 50 50)" />
        {/each}
        <line x1="50" y1="50" x2="50" y2="27" stroke="var(--text)" stroke-width="4" stroke-linecap="round" transform="rotate({hr * 30} 50 50)" />
        <line x1="50" y1="50" x2="50" y2="15" stroke="var(--text)" stroke-width="2.6" stroke-linecap="round" transform="rotate({min * 6} 50 50)" />
        <line x1="50" y1="58" x2="50" y2="12" stroke="var(--accent)" stroke-width="1.2" stroke-linecap="round" transform="rotate({sec * 6} 50 50)" />
        <circle cx="50" cy="50" r="2.6" fill="var(--accent)" />
      </svg>
      <div class="side">
        <div class="time-sm num" aria-label="Time">{timeText}</div>
        {#if dateText}<div class="date">{dateText}</div>{/if}
      </div>
    </div>
  {:else}
    <div class="time num" aria-label="Time">{timeText}</div>
    {#if dateText}<div class="date">{dateText}</div>{/if}
  {/if}
  {#if c.greeting}<div class="t-caption greet">{greeting}{username ? `, ${username}` : ''}{hostname ? `  ·  ${hostname}` : ''}</div>{/if}
  {#if zone2}<div class="zone t-caption num"><span>{zone2.name}</span><span>{zone2.t}</span></div>{/if}
</section>

<Dialog bind:open label="Clock options" {origin}>
  {#if draft}
    <div class="dlg">
      <h2 class="t-title">Clock</h2>
      <div class="field"><span class="field-label">Style</span><Segmented options={STYLES} value={draft.style} onchange={(v) => set('style', v)} label="Clock style" /></div>
      <div class="field"><span class="field-label">Hours</span><Segmented options={HOURS} value={draft.hours} onchange={(v) => set('hours', v)} label="12 or 24 hours" /></div>
      <div class="field"><span class="field-label">Date</span><Segmented options={DATES} value={draft.date} onchange={(v) => set('date', v)} label="Date format" /></div>
      <label class="row-field"><span class="field-text"><span>Greeting</span><span class="t-caption">"Good morning" with your name and the server.</span></span>
        <input type="checkbox" class="switch" checked={draft.greeting} onchange={(e) => set('greeting', e.currentTarget.checked)} /></label>
      <div class="field">
        <label class="field-label" for="tz2">Second time zone</label>
        <select id="tz2" class="input" value={draft.tz2} onchange={(e) => set('tz2', e.currentTarget.value)}>
          <option value="">None</option>
          {#each ZONES as z (z)}<option value={z}>{z.replace(/_/g, ' ')}</option>{/each}
        </select>
      </div>
      <div class="buttons"><button class="btn btn-primary" onclick={() => (open = false)}>Done</button></div>
    </div>
  {/if}
</Dialog>

<style>
  .clock { position: relative; display: grid; gap: 0.125rem; padding: 1.125rem; border-radius: 1.375rem; color: var(--text); min-width: 0; }
  .more {
    position: absolute; top: 0.5rem; right: 0.5rem; display: grid; place-items: center; width: 2.25rem; height: 2.25rem; border: 0;
    border-radius: 50%; background: none; color: var(--text-2); cursor: pointer; opacity: 0; transition: opacity 150ms ease, background-color 150ms ease;
  }
  .clock:hover .more, .more:focus-visible { opacity: 1; }
  .more:hover { background: var(--fill); color: var(--text); }
  @media (hover: none) { .more { opacity: 1; } }
  .time { font-size: 3.5rem; line-height: 1; font-weight: 250; letter-spacing: -0.045em; }
  .seconds .time { font-size: 2.875rem; }
  .minimal .time { font-size: 1.75rem; font-weight: 500; letter-spacing: -0.03em; }
  .minimal .date { margin-top: 0.25rem; font-size: 0.9375rem; font-weight: 500; color: var(--text-2); }
  .date { font-size: 1.0625rem; font-weight: 600; letter-spacing: -0.012em; margin-top: 0.5rem; }
  .greet { margin-top: 0.125rem; }
  .analog-row { display: flex; align-items: center; gap: 1.125rem; }
  .face { width: 6.5rem; height: 6.5rem; flex-shrink: 0; }
  .side { display: grid; gap: 0.25rem; min-width: 0; }
  .time-sm { font-size: 1.75rem; font-weight: 300; letter-spacing: -0.03em; line-height: 1; }
  .analog-row .date { margin-top: 0.25rem; font-size: 0.9375rem; }
  .zone { display: flex; justify-content: space-between; gap: 1rem; margin-top: 0.625rem; padding-top: 0.625rem; box-shadow: 0 -1px 0 var(--hairline); }
  .zone span:first-child { color: var(--text); font-weight: 600; }
  @media (max-width: 639px) { .time { font-size: 2.75rem; } .seconds .time { font-size: 2.375rem; } }
</style>
