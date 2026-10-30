// The desktop's window manager: several Box windows open at once, each floating, draggable and
// resizable, with z-order, minimise (to the dock row) and maximise. Place, size and maximise are
// remembered per window in this browser (localStorage nxbox-win-<key>).

import { TOP, clampRect, fitRect } from './winmath.js';

export const wm = $state({ list: [], seq: 0 });

const CASCADE = 28; // offset of each further new window

function saved(key) {
  try { return JSON.parse(localStorage.getItem('nxbox-win-' + key) || 'null'); } catch { return null; }
}

export function remember(w) {
  try { localStorage.setItem('nxbox-win-' + w.key, JSON.stringify({ ...w.rect, max: w.max })); } catch { /* storage blocked */ }
}

// Keeps a window grabbable: at least 120 px of its title bar stay on screen (in place).
export function clamp(r) { return Object.assign(r, clampRect(r, innerWidth, innerHeight)); }

// Shrinks a window to fit a smaller browser window (in place).
export function fit(r) { return Object.assign(r, fitRect(r, innerWidth, innerHeight)); }

// The remembered place, else the default size centred on screen, shifted per open window.
export function place(key, [dw, dh], open) {
  const s = saved(key);
  const w = Math.min(s?.w || dw, innerWidth - 32), h = Math.min(s?.h || dh, innerHeight - TOP - 16);
  if (s && Number.isFinite(s.x) && Number.isFinite(s.y)) return { rect: clamp({ x: s.x, y: s.y, w, h }), max: !!s.max };
  const off = (open % 6) * CASCADE;
  return { rect: clamp({ x: Math.round((innerWidth - w) / 2) + off, y: Math.max(TOP + 4, Math.round((innerHeight - h) / 2) + 12) + off, w, h }), max: false };
}

// The window in front (highest z among the shown ones).
export function topWindow() {
  let t = null;
  for (const w of wm.list) if (!w.min && (!t || w.z > t.z)) t = w;
  return t;
}
