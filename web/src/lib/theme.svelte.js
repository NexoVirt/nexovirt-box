// Light/dark mode: system default, the toggle is remembered. public/theme.js sets the class before first paint.
const KEY = 'nxbox-theme';

export const theme = $state({ current: document.documentElement.classList.contains('dark') ? 'dark' : 'light' });

export function toggleTheme() {
  theme.current = theme.current === 'dark' ? 'light' : 'dark';
  document.documentElement.classList.toggle('dark', theme.current === 'dark');
  try { localStorage.setItem(KEY, theme.current); } catch { /* storage blocked */ }
}
