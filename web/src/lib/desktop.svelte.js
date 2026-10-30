// Desktop layout of the signed-in user (wallpaper, widgets, tile order), kept on the server
// (GET/POST /api/desktop), plus the tile a window was opened from (for the zoom transition).
import { api } from './api.js';
import { toast } from './toast.svelte.js';

// Built-in wallpapers: flat colours and two flat-shape pictures made for Box (no gradients, no
// third-party images). tone says whether text on it should be light or dark.
export const WALLPAPERS = [
  { id: 'auto', label: 'Automatic', hint: 'Dunes in light mode, Night hills in dark mode' },
  { id: 'mist', label: 'Mist', color: '#d4d4d8', tone: 'light' },
  { id: 'sand', label: 'Sand', color: '#ddd5c7', tone: 'light' },
  { id: 'dunes', label: 'Dunes', color: '#e6d9c4', image: '/wallpapers/dunes.svg', tone: 'light' },
  { id: 'graphite', label: 'Graphite', color: '#3f3f46', tone: 'dark' },
  { id: 'forest', label: 'Forest', color: '#1f2e27', tone: 'dark' },
  { id: 'midnight', label: 'Midnight', color: '#0f1420', tone: 'dark' },
  { id: 'hills', label: 'Night hills', color: '#141d31', image: '/wallpapers/hills.svg', tone: 'dark' },
];

export const WIDGETS = {
  clock: 'Clock and date',
  system: 'System status',
  storage: 'Storage',
  network: 'Network',
  disks: 'Disk health (only when a disk needs attention)',
  apps: 'App status',
  weather: 'Weather (Open-Meteo, no account needed)',
};

// preview: a theme being edited in Settings, shown live until it is saved or left.
export const desktop = $state({ prefs: null, loading: false, customTone: 'dark', preview: null });

export async function loadDesktop() {
  if (desktop.loading) return;
  desktop.loading = true;
  const res = await api('/api/desktop');
  desktop.loading = false;
  if (res.ok) desktop.prefs = res.data;
  else if (res.status !== 401) toast(res.error, 'error');
}

// Saves the changed fields. The new state shows at once; on failure it goes back.
export async function saveDesktop(change) {
  const before = desktop.prefs;
  const next = { ...before, ...change };
  desktop.prefs = next;
  const { custom_v, ...body } = next;
  const res = await api('/api/desktop', { method: 'POST', body });
  if (!res.ok) {
    desktop.prefs = before;
    toast(res.error, 'error');
    return false;
  }
  return true;
}

export function wallpaperOf(prefs, dark) {
  const id = prefs?.wallpaper || 'auto';
  if (id === 'custom') return { id, image: `/api/desktop/wallpaper?v=${prefs.custom_v}`, color: '#27272a', tone: desktop.customTone };
  if (id === 'auto') return { ...WALLPAPERS.find((w) => w.id === (dark ? 'hills' : 'dunes')), id };
  return WALLPAPERS.find((w) => w.id === id) || WALLPAPERS[0];
}

// Rough brightness of an uploaded picture, so text on the desktop stays readable.
export function measureTone(url) {
  const img = new Image();
  img.onload = () => {
    try {
      const c = document.createElement('canvas');
      c.width = c.height = 16;
      const g = c.getContext('2d');
      g.drawImage(img, 0, 0, 16, 16);
      const d = g.getImageData(0, 0, 16, 16).data;
      let sum = 0;
      for (let i = 0; i < d.length; i += 4) sum += 0.2126 * d[i] + 0.7152 * d[i + 1] + 0.0722 * d[i + 2];
      desktop.customTone = sum / (d.length / 4) > 150 ? 'light' : 'dark';
    } catch { /* keep the guess */ }
  };
  img.src = url;
}

// Where a window comes from: the rect of the tile or button that opened it.
export const origin = { rect: null };
export function setOrigin(el) {
  origin.rect = el?.getBoundingClientRect?.() || null;
}
