function installWerkschaftSpeechBridge(origin) {
  if (window !== window.top || location.origin !== origin ||
      document.documentElement.hasAttribute('data-login-gate') || window.werkschaftSpeech) return;
  const mac = window.webkit?.messageHandlers?.werkschaftSpeech;
  const windows = window.chrome?.webview;
  const transport = mac || windows;
  if (!transport) return;
  const post = transport.postMessage.bind(transport);
  const sanitizeLocale = (locale) => {
    if (typeof locale !== 'string') return undefined;
    const trimmed = locale.trim();
    if (!trimmed) return undefined;
    return trimmed.slice(0, 35);
  };
  const send = (op, locale) => {
    const payload = { op };
    const clean = sanitizeLocale(locale);
    if (clean) payload.locale = clean;
    try {
      post('werkschaft:speech:' + JSON.stringify(payload));
      return true;
    } catch (e) {
      return false;
    }
  };
  const dispatch = (detail) => {
    if (!detail || typeof detail !== 'object') return;
    window.dispatchEvent(new CustomEvent('ws-native-speech', { detail }));
  };
  Object.defineProperty(window, '__werkschaftSpeechEvent', { value: dispatch });
  Object.defineProperty(window, 'werkschaftSpeech', {
    value: Object.freeze({
      status: (locale) => send('status', locale),
      start: (locale) => send('start', locale),
      stop: () => send('stop'),
      cancel: () => send('cancel'),
    }),
  });
  window.dispatchEvent(new CustomEvent('ws-native-speech-bridge'));
}
