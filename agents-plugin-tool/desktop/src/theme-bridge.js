// Reports the dashboard's resolved theme to the Windows shell, which hands it
// to the title strip. The shell never reads the page otherwise: the message is
// the fixed text `werkschaft:theme:light` or `werkschaft:theme:dark`, posted
// through the page's WebView2 message channel when the theme changes. A page
// that pins a theme sets data-theme on <html>; without one the OS preference
// applies, as it does to the dashboard's own styles.
function installWerkschaftThemeBridge(origin) {
  if (window !== window.top || location.origin !== origin || window.werkschaftThemeBridge) return;
  const transport = window.chrome?.webview;
  if (!transport) return;
  const post = transport.postMessage.bind(transport);
  const root = document.documentElement;
  const dark = window.matchMedia('(prefers-color-scheme: dark)');
  let last = '';
  const report = () => {
    const pinned = root.getAttribute('data-theme');
    const theme = pinned === 'light' || pinned === 'dark' ? pinned : (dark.matches ? 'dark' : 'light');
    if (theme === last) return;
    last = theme;
    try {
      post('werkschaft:theme:' + theme);
    } catch (e) {
      last = '';
    }
  };
  Object.defineProperty(window, 'werkschaftThemeBridge', { value: true });
  new MutationObserver(report).observe(root, { attributes: true, attributeFilter: ['data-theme'] });
  dark.addEventListener('change', report);
  report();
}
