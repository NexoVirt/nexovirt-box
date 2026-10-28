// Light/dark mode: follows the system until the user picks one; the pick is remembered.
// public/theme.js sets the class before first paint (no flash, no inline script).
const KEY = 'nxbox-theme';
const media = matchMedia('(prefers-color-scheme: dark)');

function stored() {
  try {
    const v = localStorage.getItem(KEY);
    return v === 'light' || v === 'dark' ? v : 'system';
  } catch {
    return 'system';
  }
}

export const theme = $state({ mode: stored(), current: document.documentElement.classList.contains('dark') ? 'dark' : 'light' });

function apply() {
  theme.current = theme.mode === 'system' ? (media.matches ? 'dark' : 'light') : theme.mode;
  document.documentElement.classList.toggle('dark', theme.current === 'dark');
}

export function setThemeMode(mode) {
  theme.mode = mode;
  try {
    if (mode === 'system') localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, mode);
  } catch { /* storage blocked */ }
  apply();
}

media.addEventListener('change', () => theme.mode === 'system' && apply());
