// Several boxes in one UI: while another box is chosen, every /api request of the page goes to
// this box's server-side proxy /api/remote/<id>/... (the browser never talks to the other box).
// Sign-in, account, desktop and the console always stay on this box.
const KEY = 'nxbox-remote';
const LOCAL = ['/api/auth/', '/api/login', '/api/logout', '/api/setup', '/api/2fa', '/api/account', '/api/sessions',
  '/api/console', '/api/pairing', '/api/remotes', '/api/remote/', '/api/desktop'];

export function activeRemote() {
  try { return JSON.parse(sessionStorage.getItem(KEY) || 'null'); } catch { return null; }
}

export function setRemote(r) {
  try { r ? sessionStorage.setItem(KEY, JSON.stringify({ id: r.id, name: r.name })) : sessionStorage.removeItem(KEY); } catch { /* storage blocked */ }
  location.assign('/');
}

export function apiPath(p) {
  const r = activeRemote();
  if (!r || typeof p !== 'string' || !p.startsWith('/api/') || LOCAL.some((l) => p.startsWith(l))) return p;
  return `/api/remote/${encodeURIComponent(r.id)}/${p.slice(5)}`;
}

// fetch and EventSource of the whole page follow the chosen box.
export function installRemoteRouting() {
  if (!activeRemote()) return;
  const f = window.fetch.bind(window);
  window.fetch = (input, init) => f(typeof input === 'string' ? apiPath(input) : input, init);
  const ES = window.EventSource;
  window.EventSource = class extends ES { constructor(u, o) { super(apiPath(String(u)), o); } };
}
