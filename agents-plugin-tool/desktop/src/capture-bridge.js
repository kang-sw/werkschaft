function installWerkschaftCaptureBridge(origin) {
  if (window !== window.top || location.origin !== origin ||
      document.documentElement.hasAttribute('data-login-gate') || window.wsDaemonNativeCapture) return;
  const mac = window.webkit?.messageHandlers?.werkschaftCapture;
  const windows = window.chrome?.webview;
  const transport = mac || windows;
  if (!transport) return;
  const post = transport.postMessage.bind(transport);
  const pending = new Map();
  // Ids start past any an earlier page in this window used, so a late
  // answer meant for that page never settles one of this page's requests.
  let next = Date.now() * 1000;
  const settle = (id, result) => {
    const resolve = pending.get(id);
    if (!resolve) return;
    pending.delete(id);
    resolve(result && typeof result === 'object' ? result : { ok: false, error: 'screenshot_unavailable' });
  };
  Object.defineProperty(window, '__werkschaftCaptureResult', { value: settle });
  const capture = () => new Promise((resolve) => {
    const id = ++next;
    pending.set(id, resolve);
    setTimeout(() => settle(id, { ok: false, error: 'timeout' }), 15000);
    try {
      post('werkschaft:capture:' + JSON.stringify({ id }));
    } catch (e) {
      settle(id, { ok: false, error: 'screenshot_unavailable' });
    }
  });
  Object.defineProperty(window, 'wsDaemonNativeCapture', { value: Object.freeze({ capture }) });
  try {
    document.dispatchEvent(new CustomEvent('ws-daemon-native-capture-ready'));
  } catch (e) {}
}
