// Sets the theme before the first paint (no light/dark flash). External file: the CSP allows no inline scripts.
// Also applies the colours, corners and text size of the user's theme, cached by the app (lib/themes.js).
(function () {
  var t = null, root = document.documentElement;
  try { t = localStorage.getItem('nxbox-theme'); } catch (e) {}
  if (t !== 'dark' && t !== 'light') {
    t = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }
  if (t === 'dark') root.classList.add('dark');
  try {
    var c = JSON.parse(localStorage.getItem('nxbox-theme-vars') || 'null');
    if (!c) return;
    var vars = c[t] || {};
    for (var k in vars) if (/^--[a-z0-9-]+$/.test(k) && typeof vars[k] === 'string') root.style.setProperty(k, vars[k]);
    if (c.attrs) { root.setAttribute('data-density', String(c.attrs.density)); root.setAttribute('data-surface', String(c.attrs.surface)); }
    if (c.scale && c.scale !== 100 && c.scale >= 85 && c.scale <= 125) root.style.fontSize = c.scale + '%';
  } catch (e) {}
})();
