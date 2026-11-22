function installWerkschaftNotifyBridge(origin) {
  if (window !== window.top || location.origin !== origin ||
      document.documentElement.hasAttribute('data-login-gate') || window.werkschaftNotify) return;
  const mac = window.webkit?.messageHandlers?.werkschaftNotify;
  const windows = window.chrome?.webview;
  const transport = mac || windows;
  if (!transport) return;
  const post = transport.postMessage.bind(transport);
  const pending = new Map();
  // Ids start past any an earlier page in this window used, so a late
  // answer meant for that page never settles one of this page's requests.
  let next = Date.now() * 1000;
  const settle = (id, shown) => {
    const resolve = pending.get(id);
    if (!resolve) return;
    pending.delete(id);
    resolve(shown === true);
  };
  Object.defineProperty(window, '__werkschaftNotifyResult', { value: settle });
  // Fields are cut by code point, as the shell clips them, never inside a
  // surrogate pair, and a lone half already in the input becomes U+FFFD
  // (as toWellFormed does, which older WebViews lack): the shell's JSON
  // parser would refuse either.
  const text = (value, limit) =>
    Array.from(String(value ?? ''), (c) => (c.length === 1 && c >= '\uD800' && c <= '\uDFFF' ? '�' : c))
      .slice(0, limit)
      .join('');
  // notify resolves true once the shell showed the notification natively
  // (or is showing the same tag right now), and false when it could not:
  // the page then uses its browser notification instead, so nothing shows
  // twice.
  const notify = (title, body, tag) => new Promise((resolve) => {
    const id = ++next;
    pending.set(id, resolve);
    // A shell that does not answer counts as a failed native delivery.
    setTimeout(() => settle(id, false), 10000);
    try {
      post('werkschaft:notify:' + JSON.stringify({
        id, title: text(title, 200), body: text(body, 1000), tag: text(tag, 300)
      }));
    } catch (e) {
      settle(id, false);
    }
  });
  Object.defineProperty(window, 'werkschaftNotify', { value: Object.freeze({ notify }) });
}
