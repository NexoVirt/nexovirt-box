export function bytes(n, digits = 1) {
  if (!n || n < 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i === 0 ? 0 : digits)} ${units[i]}`;
}

export function rate(n) {
  return `${bytes(n)}/s`;
}

export function pct(used, total) {
  return total > 0 ? Math.min(100, Math.round((used / total) * 1000) / 10) : 0;
}

export function duration(sec) {
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

// Status colour for a usage percentage (gauges and dots only).
export function level(p) {
  return p >= 90 ? 'var(--error)' : p >= 75 ? 'var(--warn)' : 'var(--ok)';
}

// "5 min ago" style time for a unix timestamp in seconds.
export function ago(ts) {
  const s = Math.max(0, Math.round(Date.now() / 1000 - ts));
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return new Date(ts * 1000).toLocaleDateString();
}
