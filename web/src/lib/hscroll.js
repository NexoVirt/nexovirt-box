// Horizontal scrollers that work without a sideways wheel: a normal (vertical) mouse wheel
// scrolls them sideways while the pointer is over them, the arrow keys move between their
// items, and touch and trackpads keep their native sideways scrolling.

const LINE = 16; // px per wheel line (deltaMode 1)

// How far a wheel event should scroll the strip sideways, or 0 to leave the event alone (no
// overflow, a sideways gesture the browser already handles, or the strip is at its end, so the
// page scrolls on as usual).
export function wheelShift(e, s) {
  if (s.scrollWidth <= s.clientWidth + 1) return 0;
  if (Math.abs(e.deltaX) >= Math.abs(e.deltaY)) return 0;
  const page = e.deltaMode === 2 ? s.clientWidth : e.deltaMode === 1 ? LINE : 1;
  const d = e.deltaY * page;
  const max = s.scrollWidth - s.clientWidth;
  if ((d < 0 && s.scrollLeft <= 0) || (d > 0 && s.scrollLeft >= max - 1)) return 0;
  return d;
}

// Whether there is more to see on each side.
export function overflowSides(s) {
  const max = s.scrollWidth - s.clientWidth;
  return { any: max > 1, start: s.scrollLeft > 1, end: s.scrollLeft < max - 1 };
}

// Svelte action: wheel mapping, arrow/Home/End keys between the focusable items (each one
// scrolled into view), and onchange(overflowSides) whenever the overflow changes.
export function hscroll(node, { onchange } = {}) {
  let report = onchange;
  const update = () => report?.(overflowSides(node));
  const wheel = (e) => {
    if (e.ctrlKey) return; // pinch zoom
    const d = wheelShift(e, node);
    if (!d) return;
    e.preventDefault();
    node.scrollLeft += d;
  };
  const items = () => [...node.querySelectorAll('button:not(:disabled), a[href], [tabindex]:not([tabindex="-1"])')];
  const key = (e) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key) || e.altKey || e.ctrlKey || e.metaKey) return;
    const list = items(), i = list.indexOf(document.activeElement);
    if (i < 0) return;
    const j = e.key === 'Home' ? 0 : e.key === 'End' ? list.length - 1 : i + (e.key === 'ArrowRight' ? 1 : -1);
    const next = list[Math.max(0, Math.min(list.length - 1, j))];
    e.preventDefault();
    next.focus({ preventScroll: true });
    next.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
  };
  node.addEventListener('wheel', wheel, { passive: false });
  node.addEventListener('keydown', key);
  node.addEventListener('scroll', update, { passive: true });
  const ro = new ResizeObserver(update);
  ro.observe(node);
  const mo = new MutationObserver(() => { for (const c of node.children) ro.observe(c); update(); });
  mo.observe(node, { childList: true });
  for (const c of node.children) ro.observe(c);
  update();
  return {
    update(p = {}) { report = p.onchange; update(); },
    destroy() {
      node.removeEventListener('wheel', wheel);
      node.removeEventListener('keydown', key);
      node.removeEventListener('scroll', update);
      ro.disconnect();
      mo.disconnect();
    },
  };
}
