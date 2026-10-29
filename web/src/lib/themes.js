// Themes: presets plus the owner's tweaks, turned into the existing CSS tokens (app.css) as inline
// custom properties on <html>. The last applied tokens are cached in localStorage so public/theme.js
// can set them before the first paint (no flash, no inline script).

export const PRESETS = [
  { id: 'nexovirt', label: 'NexoVirt', accent: ['#2563eb', '#60a5fa'], neutral: 'cool' },
  { id: 'graphite', label: 'Graphite', accent: ['#3f3f46', '#d4d4d8'], neutral: 'cool' },
  { id: 'midnight', label: 'Midnight', accent: ['#4f46e5', '#818cf8'], neutral: 'cool' },
  { id: 'forest', label: 'Forest', accent: ['#15803d', '#4ade80'], neutral: 'neutral' },
  { id: 'sand', label: 'Sand', accent: ['#b45309', '#fbbf24'], neutral: 'warm' },
  { id: 'rose', label: 'Rose', accent: ['#e11d48', '#fb7185'], neutral: 'warm' },
  { id: 'contrast', label: 'High contrast', accent: ['#1d4ed8', '#93c5fd'], neutral: 'neutral', contrast: true },
];
export const SWATCHES = ['#2563eb', '#4f46e5', '#7c3aed', '#db2777', '#e11d48', '#ea580c', '#b45309', '#15803d', '#0d9488', '#0284c7', '#3f3f46'];

// Neutral palettes per tone: [light, dark].
const NEUTRALS = {
  cool: [
    { bg: '#f4f4f5', surface: '#ffffff', 'surface-2': '#fafafa', text: '#09090b', 'text-2': '#52525b', 'text-3': '#a1a1aa', fill: 'rgba(113,113,122,0.12)', 'fill-2': 'rgba(113,113,122,0.2)' },
    { bg: '#09090b', surface: '#18181b', 'surface-2': '#1f1f23', text: '#fafafa', 'text-2': '#a1a1aa', 'text-3': '#71717a', fill: 'rgba(161,161,170,0.14)', 'fill-2': 'rgba(161,161,170,0.22)' },
  ],
  neutral: [
    { bg: '#f5f5f5', surface: '#ffffff', 'surface-2': '#fafafa', text: '#0a0a0a', 'text-2': '#525252', 'text-3': '#a3a3a3', fill: 'rgba(115,115,115,0.12)', 'fill-2': 'rgba(115,115,115,0.2)' },
    { bg: '#0a0a0a', surface: '#171717', 'surface-2': '#1f1f1f', text: '#fafafa', 'text-2': '#a3a3a3', 'text-3': '#737373', fill: 'rgba(163,163,163,0.14)', 'fill-2': 'rgba(163,163,163,0.22)' },
  ],
  warm: [
    { bg: '#f5f5f4', surface: '#ffffff', 'surface-2': '#fafaf9', text: '#0c0a09', 'text-2': '#57534e', 'text-3': '#a8a29e', fill: 'rgba(120,113,108,0.12)', 'fill-2': 'rgba(120,113,108,0.2)' },
    { bg: '#0c0a09', surface: '#1c1917', 'surface-2': '#231f1d', text: '#fafaf9', 'text-2': '#a8a29e', 'text-3': '#78716c', fill: 'rgba(168,162,158,0.14)', 'fill-2': 'rgba(168,162,158,0.22)' },
  ],
};
const RADIUS = { small: ['0.75rem', '0.5rem'], medium: ['1.25rem', '0.75rem'], large: ['1.625rem', '1rem'] };

export const DEFAULT_THEME = { preset: 'nexovirt', accent: '', neutral: 'cool', surface: 'solid', radius: 'medium', density: 'comfortable', scale: 100 };
export const presetOf = (id) => PRESETS.find((p) => p.id === id) || PRESETS[0];
export function fromPreset(id) {
  const p = presetOf(id);
  return { ...DEFAULT_THEME, preset: p.id, neutral: p.neutral };
}

// ---- contrast (WCAG 2) ----
function rgb(hex) { const n = parseInt(hex.slice(1), 16); return [(n >> 16) & 255, (n >> 8) & 255, n & 255]; }
function lum(hex) {
  const [r, g, b] = rgb(hex).map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
export function contrast(a, b) { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + 0.05) / (y + 0.05); }
function mix(hex, to, t) { const a = rgb(hex), b = rgb(to); return '#' + a.map((v, i) => Math.round(v + (b[i] - v) * t).toString(16).padStart(2, '0')).join(''); }

// Accent for one mode, plus the text colour on it. White or near-black, whichever reads better;
// when neither reaches AA (4.5:1) the accent is darkened or lightened until it does.
export function accentFor(t, dark) {
  let accent = t.accent && /^#[0-9a-f]{6}$/i.test(t.accent) ? t.accent.toLowerCase() : presetOf(t.preset).accent[dark ? 1 : 0];
  const original = accent;
  const pick = (a) => (contrast(a, '#ffffff') >= contrast(a, '#09090b') ? '#ffffff' : '#09090b');
  let on = pick(accent);
  for (let i = 0; i < 20 && contrast(accent, on) < 4.5; i++) accent = mix(accent, on === '#ffffff' ? '#000000' : '#ffffff', 0.06);
  return { accent, on, adjusted: accent !== original, ratio: contrast(accent, on), original };
}

export function buildVars(t, dark) {
  t = { ...DEFAULT_THEME, ...(t || {}) };
  const n = (NEUTRALS[t.neutral] || NEUTRALS.cool)[dark ? 1 : 0];
  const { accent, on } = accentFor(t, dark);
  const [a, b, c] = rgb(accent);
  const vars = {};
  for (const [k, v] of Object.entries(n)) vars['--' + k] = v;
  Object.assign(vars, {
    '--accent': accent, '--on-accent': on, '--accent-fill': `rgba(${a},${b},${c},${dark ? 0.16 : 0.12})`,
    '--r-tile': RADIUS[t.radius]?.[0] || RADIUS.medium[0], '--r-control': RADIUS[t.radius]?.[1] || RADIUS.medium[1],
  });
  if (presetOf(t.preset).contrast) Object.assign(vars, dark ? { '--text-2': '#e5e5e5', '--text-3': '#a3a3a3', '--hairline': 'rgba(255,255,255,0.35)' } : { '--text-2': '#262626', '--text-3': '#525252', '--hairline': 'rgba(0,0,0,0.3)' });
  return { vars, attrs: { density: t.density, surface: t.surface }, scale: t.scale || 100 };
}

const CACHE = 'nxbox-theme-vars';
let applied = [];
export function applyTheme(t, dark) {
  const root = document.documentElement;
  const { vars, attrs, scale } = buildVars(t, dark);
  for (const k of applied) if (!(k in vars)) root.style.removeProperty(k);
  for (const [k, v] of Object.entries(vars)) root.style.setProperty(k, v);
  applied = Object.keys(vars);
  root.dataset.density = attrs.density;
  root.dataset.surface = attrs.surface;
  root.style.fontSize = scale === 100 ? '' : scale + '%';
  try {
    localStorage.setItem(CACHE, JSON.stringify({ light: buildVars(t, false).vars, dark: buildVars(t, true).vars, attrs, scale }));
  } catch { /* storage blocked: only the first paint is affected */ }
}

// ---- import / export ----
const ENUMS = { preset: PRESETS.map((p) => p.id), neutral: ['cool', 'neutral', 'warm'], surface: ['solid', 'glass'], radius: ['small', 'medium', 'large'], density: ['comfortable', 'compact'] };
export function cleanTheme(x) {
  if (!x || typeof x !== 'object' || Array.isArray(x)) throw new Error('The file is not a NexoVirt Box theme.');
  const out = {};
  for (const [k, list] of Object.entries(ENUMS)) {
    const v = x[k] ?? DEFAULT_THEME[k];
    if (!list.includes(v)) throw new Error(`Unknown value for ${k}: ${String(v).slice(0, 30)}`);
    out[k] = v;
  }
  const accent = x.accent ?? '';
  if (accent !== '' && !/^#[0-9a-fA-F]{6}$/.test(accent)) throw new Error('The accent colour must look like #2563eb.');
  out.accent = accent;
  const scale = x.scale ?? 100;
  if (!Number.isInteger(scale) || scale < 85 || scale > 125) throw new Error('The text size goes from 85 to 125.');
  out.scale = scale;
  return out;
}
export function importTheme(text) {
  let data;
  try { data = JSON.parse(text); } catch { throw new Error('The file is not valid JSON.'); }
  if (data?.type !== 'nexovirt-box-theme') throw new Error('The file is not a NexoVirt Box theme.');
  const name = String(data.name || 'Imported theme').trim().slice(0, 40) || 'Imported theme';
  return { name, ...cleanTheme(data.theme) };
}
export function exportTheme(name, t) {
  return JSON.stringify({ type: 'nexovirt-box-theme', version: 1, name, theme: cleanTheme(t) }, null, 2);
}
