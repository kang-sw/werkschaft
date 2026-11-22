function installWerkschaftNotifyBridge(origin) {
  if (window !== window.top || location.origin !== origin ||
      document.documentElement.hasAttribute('data-login-gate') || window.werkschaftNotify) return;
  const mac = window.webkit?.messageHandlers?.werkschaftNotify;
  const windows = window.chrome?.webview;
  const transport = mac || windows;
  if (!transport) return;
  const post = transport.postMessage.bind(transport);
  const pending = new Map();
  let next = 0;
  const settle = (id, shown) => {
    const resolve = pending.get(id);
    if (!resolve) return;
    pending.delete(id);
    resolve(shown === true);
  };
  Object.defineProperty(window, '__werkschaftNotifyResult', { value: settle });
  const text = (value, limit) => String(value ?? '').slice(0, limit);
  // notify resolves true once the shell showed the notification natively
  // (or already had for this tag), and false when it could not: the page
  // then uses its browser notification instead, so nothing shows twice.
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
