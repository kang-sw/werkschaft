(function (C) {
  "use strict";

  const PRESENCE_ROUTE = "/api/companion/messenger/presence";
  const HEARTBEAT_MS = 60000;

  // messengerPresence reports whether this tab's companion messenger is open
  // so the daemon can keep the companion's phone in hand between replies.
  // Every report is best-effort: an older daemon (404), a refusal or a
  // network error is ignored, and the daemon ages a missed close out after
  // a few minutes. While open, a heartbeat refreshes the daemon's record;
  // on page teardown a close goes out through sendBeacon (keepalive fetch
  // as the fallback) so it survives the unload.
  function messengerPresence(env = {}) {
    const win = env.window || (typeof window !== "undefined" ? window : null);
    const setTimer = env.setInterval || ((fn, ms) => setInterval(fn, ms));
    const clearTimer = env.clearInterval || (id => clearInterval(id));
    const client = env.client || newClientID(win);
    let open = false;
    let timer = 0;

    function fetchImpl() {
      if (typeof env.fetch === "function") return env.fetch;
      return win && typeof win.fetch === "function" ? win.fetch.bind(win) : null;
    }

    function post(isOpen, unloading) {
      const body = JSON.stringify({ open: isOpen, client });
      const nav = env.navigator || win?.navigator;
      if (unloading && nav && typeof nav.sendBeacon === "function") {
        try {
          if (nav.sendBeacon(PRESENCE_ROUTE, body)) return;
        } catch (_) { /* fall back to fetch */ }
      }
      const f = fetchImpl();
      if (!f) return;
      try {
        const res = f(PRESENCE_ROUTE, {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body,
          keepalive: !!unloading
        });
        if (res && typeof res.catch === "function") res.catch(() => {});
      } catch (_) { /* best effort */ }
    }

    function stopHeartbeat() {
      if (timer) clearTimer(timer);
      timer = 0;
    }

    function set(isOpen) {
      isOpen = !!isOpen;
      if (isOpen === open) return;
      open = isOpen;
      stopHeartbeat();
      post(open, false);
      if (open) timer = setTimer(() => post(true, false), HEARTBEAT_MS);
    }

    function teardown() {
      stopHeartbeat();
      if (!open) return;
      open = false;
      post(false, true);
    }

    if (win && typeof win.addEventListener === "function") {
      win.addEventListener("pagehide", teardown);
      win.addEventListener("beforeunload", teardown);
      // A tab restored from the back/forward cache reports its open
      // messenger again (pagehide sent a close).
      win.addEventListener("pageshow", event => {
        if (event && event.persisted && env.isOpen?.()) set(true);
      });
    }

    return {
      set,
      teardown,
      isOpen() { return open; },
      client
    };
  }

  function newClientID(win) {
    try {
      if (win?.crypto && typeof win.crypto.randomUUID === "function") return win.crypto.randomUUID().slice(0, 36);
    } catch (_) { /* fall through */ }
    return "tab-" + Date.now().toString(36) + "-" + Math.random().toString(36).slice(2, 10);
  }

  C.messengerPresenceReporter = messengerPresence;
  if (typeof window !== "undefined" && window.addEventListener) {
    C.messengerPresence = messengerPresence({
      isOpen: () => !!window.WsCompanionMessenger?.isOpen?.()
    });
  }
})(window.WsCompanion);
