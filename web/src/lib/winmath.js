// Window geometry shared by the window manager and AppWindow. Plain functions (no runes) so
// they run under `node --test`.

export const TOP = 56; // below the top bar
export const MIN_Y = TOP - 8; // the highest a window's top edge goes
export const MIN_W = 420, MIN_H = 300; // smallest window; every page reflows down to it
const GAP = 4; // room kept to the browser window's edges when resizing

// Clamp v to [lo, hi]; lo wins. hi never drops below was (the size at the grab), so a window
// that is already past an edge does not jump when grabbed.
const span = (v, lo, hi, was) => Math.max(lo, Math.min(Math.max(hi, was), v));

// The rect r resized from edge or corner dir (a compass: n, ne, e, se, s, sw, w, nw) by the
// pointer moving dx, dy in a vw x vh viewport. The grabbed edges follow the pointer, the
// opposite ones stay put.
export function resizeRect(r, dir, dx, dy, vw, vh) {
  const n = { ...r };
  if (dir.includes('e')) n.w = span(r.w + dx, MIN_W, vw - r.x - GAP, r.w);
  if (dir.includes('w')) { n.w = span(r.w - dx, MIN_W, r.x + r.w - GAP, r.w); n.x = r.x + r.w - n.w; }
  if (dir.includes('s')) n.h = span(r.h + dy, MIN_H, vh - r.y - GAP, r.h);
  if (dir.includes('n')) { n.h = span(r.h - dy, MIN_H, r.y + r.h - MIN_Y, r.h); n.y = r.y + r.h - n.h; }
  return n;
}

// Keeps a window grabbable: at least 120 px of its title bar stay on screen.
export function clampRect(r, vw, vh) {
  return { ...r, x: Math.min(Math.max(r.x, 120 - r.w), vw - 120), y: Math.min(Math.max(r.y, MIN_Y), vh - 48) };
}

// Shrinks a window to fit a smaller viewport (never below the minimum the viewport allows),
// then keeps it grabbable.
export function fitRect(r, vw, vh) {
  const w = Math.max(Math.min(MIN_W, vw), Math.min(r.w, vw - 16));
  const h = Math.max(Math.min(MIN_H, vh - MIN_Y), Math.min(r.h, vh - MIN_Y - 8));
  return clampRect({ ...r, w, h }, vw, vh);
}
