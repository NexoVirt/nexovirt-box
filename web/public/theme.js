// Sets the theme before the first paint (no light/dark flash). External file: the CSP allows no inline scripts.
(function () {
  var t = null;
  try { t = localStorage.getItem('nxbox-theme'); } catch (e) {}
  if (t !== 'dark' && t !== 'light') {
    t = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }
  document.documentElement.setAttribute('data-bs-theme', t);
})();
