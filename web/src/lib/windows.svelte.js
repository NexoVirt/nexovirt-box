// The desktop's window manager: several Box windows open at once, each floating, draggable and
// resizable, with z-order, minimise (to the dock row) and maximise. Place, size and maximise are
// remembered per window in this browser (localStorage nxbox-win-<key>).

export const wm = $state({ list: [], seq: 0 });

const TOP = 56; // below the top bar
const CASCADE = 28; // offset of each further new window

function saved(key) {
  try { return JSON.parse(localStorage.getItem('nxbox-win-' + key) || 'null'); } catch { return null; }
}

export function remember(w) {
  try { localStorage.setItem('nxbox-win-' + w.key, JSON.stringify({ ...w.rect, max: w.max })); } catch { /* storage blocked */ }
}

// Keeps a window grabbable: at least 120 px of its title bar stay on screen.
export function clamp(r) {
  r.x = Math.min(Math.max(r.x, 120 - r.w), innerWidth - 120);
  r.y = Math.min(Math.max(r.y, TOP - 8), innerHeight - 48);
  return r;
}

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
