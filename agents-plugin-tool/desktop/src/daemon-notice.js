(origin, reason, fallback) => {
  if (window !== window.top || location.origin !== origin) return 0;
  if (!window.WsToast || typeof window.WsToast.show !== "function") return 0;
  if (typeof window.__werkschaftDaemonToast?.close === "function") {
    window.__werkschaftDaemonToast.close();
  }
  // A fallback stays until dismissed and offers the update check that leads
  // off it; a refusal is a passing warning.
  const update = fallback ? window.werkschaftDesktop?.update : null;
  const options = fallback ? {
    kind: "warn",
    sticky: true,
    dismiss: "Dismiss",
    actions: update ? [{ label: "Check for Updates", run: () => update.check() }] : [],
  } : { kind: "warn" };
  const notice = window.WsToast.show(reason, options);
  if (!notice || typeof notice.close !== "function") return 0;
  window.__werkschaftDaemonToast = notice;
  return 1;
}
