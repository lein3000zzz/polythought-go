// Applies the saved theme before the page paints, so a dark page never flashes white.
// A file, not an inline script: the content security policy allows only these. Keep it in
// step with src/lib/theme.ts.
;(function () {
  let dark = false
  try {
    const saved = localStorage.getItem('polythought.theme')
    dark =
      saved === 'dark' ||
      (saved !== 'light' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  } catch {
    // Storage or media queries unavailable: light it is.
  }
  document.documentElement.classList.toggle('dark', dark)
})()
