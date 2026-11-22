(function (C) {
  "use strict";

  // focusReporter tells the daemon which session and resolved pack this
  // shell's companion shows, so an agent director can digest that session.
  // Every report is tied to the shell stream's token and is best-effort: a
  // refusal, an older daemon (404), a gone stream (410) or a network error
  // is ignored without a retry; the next state change reports again.
  // It sends nothing unless the config is in agent mode, and it sends a
  // clear only after a non-empty report for the same token.
  function focusReporter(env = {}) {
    const doc = env.document;
    let token = "", config = null, rid = "", pack = "", last = null;

    function agent() { return !!config && config.enabled === true && config.director === "agent"; }
    function visible() { return doc?.visibilityState === "visible"; }

    function post(body) {
      try {
        const request = (env.fetch || fetch)("/api/companion/focus", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body)
        });
        Promise.resolve(request).catch(() => {});
      } catch (_) { /* best effort */ }
    }

    function sync() {
      if (!token) return;
      const shown = agent() && visible() && rid && pack;
      const sessionId = shown ? rid : "", resolved = shown ? pack : "";
      const prior = last && last.token === token ? last : null;
      if (sessionId ? prior?.sessionId === sessionId && prior.pack === resolved : !prior?.sessionId) return;
      last = { token, sessionId, pack: resolved };
      post({ token, sessionId, pack: resolved });
    }

    doc?.addEventListener?.("visibilitychange", sync);
    return {
      token(next) { token = typeof next === "string" ? next : ""; sync(); },
      config(next) { config = next && typeof next === "object" ? next : null; sync(); },
      show(nextRid, nextPack) { rid = nextRid || ""; pack = nextPack || ""; sync(); }
    };
  }

  C.focusReporter = focusReporter;
  if (typeof document !== "undefined" && document.addEventListener) C.focus = focusReporter({ document });
})(window.WsCompanion);
