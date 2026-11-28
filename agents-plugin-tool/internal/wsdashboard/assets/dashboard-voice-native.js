// OS native speech-to-text bridge adapter and top-frame same-origin relay.
// Only the top frame talks to window.werkschaftSpeech; framed composers and
// framed settings pages communicate through same-origin postMessage relay.
(function () {
  "use strict";

  var RELAY_TAG = "wsVoiceRelay";

  function makeVoiceError(code, message) {
    var err = new Error(message || code || "recognition_error");
    err.code = code || "recognition_error";
    return err;
  }

  function cleanLocaleTag(tag) {
    var s = typeof tag === "string" ? tag.trim().replace(/_/g, "-") : "";
    if (!s || s.length > 35) return "";
    return /^[A-Za-z0-9-]+$/.test(s) ? s : "";
  }

  function normalizeLocale(language, nav) {
    var raw = typeof language === "string" ? language.trim() : "";
    if (raw) {
      var first = cleanLocaleTag(raw.split(",")[0]);
      if (first) return first;
    }
    var n = nav || (typeof navigator !== "undefined" ? navigator : null);
    var lang = n && typeof n.language === "string" ? cleanLocaleTag(n.language) : "";
    return lang;
  }

  function createNativeAdapter(opts) {
    opts = opts || {};
    var win = opts.window || window;
    var doc = opts.document || win.document || document;
    var nav = opts.navigator || win.navigator || (typeof navigator !== "undefined" ? navigator : null);
    var getVoiceInput = function () { return opts.voiceInput || win.WsVoiceInput || null; };
    var getOrigin = function () {
      try { return (opts.origin || (win.location && win.location.origin) || ""); } catch (e) { return ""; }
    };
    var isTop = opts.isTop !== undefined ? !!opts.isTop : (function () {
      try { return !win.top || win.top === win; } catch (e) { return true; }
    })();

    var status = {
      desktop: false,
      available: false,
      on_device: false,
      locale: "",
      reason: "desktop_only",
      ready: false
    };
    var lastReadyDetail = null;
    var listeners = [];
    var registered = false;

    // Single-owner tracking in top frame: null | { kind: "top" } | { kind: "frame", source, iframe }
    var activeOwner = null;
    var detachObserver = null;

    // Local active session state (both top-frame direct and framed-client).
    var partialCb = null;
    var asyncErrorCb = null;
    var hiddenCb = null;
    var autoFinalCb = null;
    var sessionActive = false;
    var pendingStop = null;
    var destroyed = false;
    // Latest partial of the live session: the fallback when a Final arrives
    // empty (at Stop or unsolicited after a pause).
    var lastPartial = "";
    // Rust backstops Stop at 2.5 s; this JS timer is the longer last resort so
    // the controller can never hang in "transcribing".
    var stopTimeoutMs = typeof opts.stopTimeoutMs === "number" ? opts.stopTimeoutMs : 4000;
    var setT = opts.setTimeout || function (fn, ms) { return win.setTimeout ? win.setTimeout(fn, ms) : setTimeout(fn, ms); };
    var clearT = opts.clearTimeout || function (id) { if (win.clearTimeout) win.clearTimeout(id); else clearTimeout(id); };

    function getBridge() {
      if (!isTop) return null;
      var b = win.werkschaftSpeech;
      if (b && typeof b.status === "function" && typeof b.start === "function" &&
          typeof b.stop === "function" && typeof b.cancel === "function") {
        return b;
      }
      return null;
    }

    function snapshotStatus() {
      return {
        desktop: status.desktop,
        available: status.available,
        on_device: status.on_device,
        locale: status.locale,
        reason: status.reason,
        ready: status.ready
      };
    }

    function dispatchCustom(target, name, detail) {
      if (!target || typeof target.dispatchEvent !== "function") return;
      try {
        var ev;
        if (typeof CustomEvent === "function") {
          ev = new CustomEvent(name, { detail: detail });
        } else if (typeof Event === "function") {
          ev = new Event(name);
          ev.detail = detail;
        } else {
          ev = { type: name, detail: detail };
        }
        target.dispatchEvent(ev);
      } catch (e) {}
    }

    function notifyStatusChange() {
      var snap = snapshotStatus();
      for (var i = 0; i < listeners.length; i++) {
        try { listeners[i](snap); } catch (e) {}
      }
      dispatchCustom(win, "ws-native-speech-status", snap);
    }

    function settleStop(err, text) {
      var p = pendingStop;
      pendingStop = null;
      if (!p) return false;
      if (p.timer != null) clearT(p.timer);
      if (err) p.reject(err);
      else p.resolve({ text: String(text || "") });
      return true;
    }

    function syncBackendRegistration() {
      var vi = getVoiceInput();
      if (!vi || typeof vi.registerBackend !== "function") return;
      if (status.desktop && status.available) {
        if (!registered) {
          vi.registerBackend(backend);
          registered = true;
        }
      } else if (registered) {
        if (typeof vi.unregisterBackend === "function") vi.unregisterBackend("native");
        registered = false;
      }
    }

    function applyReady(detail, fromDesktop) {
      lastReadyDetail = detail ? Object.assign({}, detail) : null;
      status.desktop = !!fromDesktop;
      status.available = !!(fromDesktop && detail && detail.available);
      status.on_device = !!(detail && detail.on_device);
      status.locale = (detail && typeof detail.locale === "string") ? detail.locale : "";
      status.reason = status.desktop
        ? ((detail && typeof detail.reason === "string") ? detail.reason : (status.available ? "" : "unavailable"))
        : "desktop_only";
      status.ready = true;
      syncBackendRegistration();
      notifyStatusChange();
    }

    function handleClientSpeechEvent(detail) {
      if (!detail || typeof detail !== "object") return;
      var t = detail.type;
      if (t === "ready") {
        applyReady(detail, true);
        return;
      }
      if (t === "hidden") {
        var wasActive = sessionActive || !!pendingStop;
        sessionActive = false;
        lastPartial = "";
        settleStop(makeVoiceError("cancelled", "window_hidden"));
        if (wasActive && typeof hiddenCb === "function") {
          try { hiddenCb(detail); } catch (e) {}
        }
        dispatchCustom(doc, "ws-native-speech-hidden", detail);
        return;
      }
      if (t === "partial") {
        if (!sessionActive) return;
        lastPartial = typeof detail.text === "string" ? detail.text : "";
        if (typeof partialCb === "function") {
          try { partialCb(lastPartial); } catch (e) {}
        }
        return;
      }
      if (t === "final") {
        var finalText = typeof detail.text === "string" ? detail.text : "";
        if (!finalText.trim() && lastPartial) finalText = lastPartial;
        var live = sessionActive;
        sessionActive = false;
        lastPartial = "";
        // No Stop pending: the recognizer finalized on its own (e.g. a pause).
        // Hand the text to the controller rather than dropping it, which would
        // leave it "recording" over a dead session and fail the next Stop.
        if (!settleStop(null, finalText) && live && typeof autoFinalCb === "function") {
          try { autoFinalCb(finalText); } catch (e) {}
        }
        return;
      }
      if (t === "error") {
        var err = makeVoiceError(detail.code || "recognition_error", detail.reason || detail.code);
        var hadStop = settleStop(err);
        var wasSession = sessionActive;
        sessionActive = false;
        lastPartial = "";
        if (!hadStop && wasSession && typeof asyncErrorCb === "function") {
          try { asyncErrorCb(err); } catch (e) {}
        }
      }
    }

    // --- Top-frame relay helpers ---
    function listChildIframes() {
      if (!doc || typeof doc.querySelectorAll !== "function") return [];
      try {
        var nodes = doc.querySelectorAll("iframe");
        return Array.prototype.slice.call(nodes || []);
      } catch (e) {
        return [];
      }
    }

    function findChildFrame(source) {
      if (!source || source === win) return null;
      var frames = listChildIframes();
      for (var i = 0; i < frames.length; i++) {
        try {
          if (frames[i].contentWindow === source) return frames[i];
        } catch (e) {}
      }
      return null;
    }

    function postToFrame(frameWin, payload) {
      if (!frameWin || typeof frameWin.postMessage !== "function") return;
      var origin = getOrigin();
      try { frameWin.postMessage(payload, origin || "*"); } catch (e) {}
    }

    function broadcastToFrames(payload) {
      var frames = listChildIframes();
      for (var i = 0; i < frames.length; i++) {
        try {
          var cw = frames[i].contentWindow;
          if (cw) postToFrame(cw, payload);
        } catch (e) {}
      }
    }

    function stopDetachObserver() {
      if (detachObserver) {
        try { detachObserver.disconnect(); } catch (e) {}
        detachObserver = null;
      }
    }

    function isOwnerAlive(owner) {
      if (!owner) return false;
      if (owner.kind === "top") return true;
      if (owner.iframe) {
        if (owner.iframe.isConnected === false) return false;
        if (doc && doc.documentElement && typeof doc.documentElement.contains === "function") {
          if (!doc.documentElement.contains(owner.iframe)) return false;
        }
      }
      if (owner.source && owner.source.closed) return false;
      return true;
    }

    function clearOwnerIfDead() {
      if (!activeOwner) return;
      if (isOwnerAlive(activeOwner)) return;
      activeOwner = null;
      stopDetachObserver();
      var bridge = getBridge();
      if (bridge) {
        try { bridge.cancel(); } catch (e) {}
      }
    }

    function watchOwningFrame(frameEl) {
      stopDetachObserver();
      if (!frameEl || typeof win.MutationObserver !== "function") return;
      var target = (doc && (doc.body || doc.documentElement)) || frameEl.parentNode;
      if (!target) return;
      try {
        detachObserver = new win.MutationObserver(function () {
          clearOwnerIfDead();
        });
        detachObserver.observe(target, { childList: true, subtree: true });
      } catch (e) {
        detachObserver = null;
      }
    }

    function handleTopNativeSpeech(ev) {
      var detail = ev && ev.detail;
      if (!detail || typeof detail !== "object") return;
      var t = detail.type;
      if (t === "ready") {
        applyReady(detail, true);
        broadcastToFrames({ wsVoiceRelay: true, type: "event", detail: detail });
        return;
      }
      if (t === "hidden") {
        activeOwner = null;
        stopDetachObserver();
        handleClientSpeechEvent(detail);
        broadcastToFrames({ wsVoiceRelay: true, type: "event", detail: detail });
        return;
      }
      clearOwnerIfDead();
      var owner = activeOwner;
      if (t === "final" || t === "error") {
        activeOwner = null;
        stopDetachObserver();
      }
      if (!owner || owner.kind === "top") {
        handleClientSpeechEvent(detail);
      } else if (owner.kind === "frame" && owner.source) {
        postToFrame(owner.source, { wsVoiceRelay: true, type: "event", detail: detail });
      }
    }

    function handleTopMessage(ev) {
      if (!ev || !ev.data || ev.data[RELAY_TAG] !== true) return;
      var origin = getOrigin();
      if (origin && ev.origin !== origin) return;
      var iframe = findChildFrame(ev.source);
      if (!iframe) return;
      var msg = ev.data;
      var op = msg.op;
      var bridge = getBridge();
      if (op === "status") {
        if (!bridge) {
          postToFrame(ev.source, { wsVoiceRelay: true, type: "status", desktop: false, detail: null });
          return;
        }
        if (lastReadyDetail) {
          postToFrame(ev.source, { wsVoiceRelay: true, type: "status", desktop: true, detail: lastReadyDetail });
        }
        bridge.status(normalizeLocale(msg.locale, nav));
        return;
      }
      if (!bridge) {
        postToFrame(ev.source, { wsVoiceRelay: true, type: "event", detail: { type: "error", code: "unavailable", reason: "desktop_only" } });
        return;
      }
      clearOwnerIfDead();
      if (op === "start") {
        if (activeOwner && !(activeOwner.kind === "frame" && activeOwner.source === ev.source)) {
          postToFrame(ev.source, { wsVoiceRelay: true, type: "event", detail: { type: "error", code: "busy", reason: "already_active" } });
          return;
        }
        activeOwner = { kind: "frame", source: ev.source, iframe: iframe };
        watchOwningFrame(iframe);
        var ok = bridge.start(normalizeLocale(msg.locale, nav));
        if (ok === false) {
          activeOwner = null;
          stopDetachObserver();
          postToFrame(ev.source, { wsVoiceRelay: true, type: "event", detail: { type: "error", code: "bridge_error", reason: "send_failed" } });
        }
        return;
      }
      if (op === "stop") {
        if (activeOwner && activeOwner.kind === "frame" && activeOwner.source === ev.source) {
          bridge.stop();
        }
        return;
      }
      if (op === "cancel") {
        if (activeOwner && activeOwner.kind === "frame" && activeOwner.source === ev.source) {
          activeOwner = null;
          stopDetachObserver();
          bridge.cancel();
        }
      }
    }

    function handleFrameMessage(ev) {
      if (!ev || !ev.data || ev.data[RELAY_TAG] !== true) return;
      var origin = getOrigin();
      if (origin && ev.origin !== origin) return;
      if (ev.source !== win.top) return;
      var msg = ev.data;
      if (msg.type === "status") {
        if (!msg.desktop) {
          applyReady(null, false);
        } else if (msg.detail) {
          applyReady(msg.detail, true);
        } else {
          status.desktop = true;
          notifyStatusChange();
        }
        return;
      }
      if (msg.type === "event" && msg.detail) {
        handleClientSpeechEvent(msg.detail);
      }
    }

    function sendToTop(payload) {
      if (!win.top || typeof win.top.postMessage !== "function") return false;
      var origin = getOrigin();
      try {
        win.top.postMessage(payload, origin || "*");
        return true;
      } catch (e) {
        return false;
      }
    }

    function refreshStatus(locale) {
      var loc = normalizeLocale(locale, nav);
      if (isTop) {
        var bridge = getBridge();
        if (!bridge) {
          applyReady(null, false);
          return false;
        }
        status.desktop = true;
        return !!bridge.status(loc);
      }
      return sendToTop({ wsVoiceRelay: true, op: "status", locale: loc });
    }

    // Backend conforming to window.WsVoiceInput contract.
    var backend = {
      id: "native",
      label: "OS Native STT",
      kind: "native",
      models: [],
      available: function () { return !!(status.desktop && status.available); },
      reason: function () { return status.reason; },
      onDevice: function () { return !!status.on_device; },
      onPartial: function (cb) { partialCb = cb; },
      onError: function (cb) { asyncErrorCb = cb; },
      onHidden: function (cb) { hiddenCb = cb; },
      onAutoFinal: function (cb) { autoFinalCb = cb; },
      hasLastAudio: function () { return false; },
      discardLast: function () {},
      start: function (ctx) {
        if (!status.desktop || !status.available) {
          return Promise.reject(makeVoiceError("unavailable", status.reason || "unavailable"));
        }
        var loc = normalizeLocale(ctx && ctx.language, nav);
        if (isTop) {
          var bridge = getBridge();
          if (!bridge) return Promise.reject(makeVoiceError("unavailable", "desktop_only"));
          clearOwnerIfDead();
          if (activeOwner && activeOwner.kind !== "top") {
            return Promise.reject(makeVoiceError("busy", "already_active"));
          }
          activeOwner = { kind: "top" };
          sessionActive = true;
          lastPartial = "";
          var sent = bridge.start(loc);
          if (sent === false) {
            activeOwner = null;
            sessionActive = false;
            return Promise.reject(makeVoiceError("bridge_error", "send_failed"));
          }
          return Promise.resolve();
        }
        sessionActive = true;
        lastPartial = "";
        if (!sendToTop({ wsVoiceRelay: true, op: "start", locale: loc })) {
          sessionActive = false;
          return Promise.reject(makeVoiceError("bridge_error", "relay_failed"));
        }
        return Promise.resolve();
      },
      stop: function () {
        if (!sessionActive) return Promise.reject(makeVoiceError("not_recording"));
        return new Promise(function (resolve, reject) {
          pendingStop = { resolve: resolve, reject: reject, timer: null };
          // Give up on a reply that never comes: settle with what was heard.
          function abandonStop() {
            if (!pendingStop) return;
            sessionActive = false;
            var heard = lastPartial;
            lastPartial = "";
            if (isTop && activeOwner && activeOwner.kind === "top") {
              activeOwner = null;
              stopDetachObserver();
              var b = getBridge();
              if (b) { try { b.cancel(); } catch (e) {} }
            }
            settleStop(null, heard);
          }
          pendingStop.timer = setT(abandonStop, stopTimeoutMs);
          if (isTop) {
            var bridge = getBridge();
            if (!bridge) {
              sessionActive = false;
              settleStop(makeVoiceError("unavailable", "desktop_only"));
              return;
            }
            if (bridge.stop() === false) abandonStop();
          } else {
            if (!sendToTop({ wsVoiceRelay: true, op: "stop" })) {
              sessionActive = false;
              settleStop(makeVoiceError("bridge_error", "relay_failed"));
            }
          }
        });
      },
      cancel: function () {
        var was = sessionActive || !!pendingStop;
        sessionActive = false;
        lastPartial = "";
        settleStop(makeVoiceError("cancelled"));
        if (!was) return;
        if (isTop) {
          if (activeOwner && activeOwner.kind === "top") {
            activeOwner = null;
            stopDetachObserver();
            var bridge = getBridge();
            if (bridge) {
              try { bridge.cancel(); } catch (e) {}
            }
          }
        } else {
          sendToTop({ wsVoiceRelay: true, op: "cancel" });
        }
      }
    };

    function onBridgeReady() {
      refreshStatus();
    }

    function onUnload() {
      backend.cancel();
    }

    if (isTop) {
      if (win.addEventListener) {
        win.addEventListener("ws-native-speech", handleTopNativeSpeech);
        win.addEventListener("ws-native-speech-bridge", onBridgeReady);
        win.addEventListener("message", handleTopMessage);
        win.addEventListener("pagehide", onUnload);
        win.addEventListener("beforeunload", onUnload);
      }
      if (getBridge()) {
        refreshStatus();
      } else {
        applyReady(null, false);
      }
    } else {
      if (win.addEventListener) {
        win.addEventListener("message", handleFrameMessage);
        win.addEventListener("pagehide", onUnload);
        win.addEventListener("beforeunload", onUnload);
      }
      refreshStatus();
    }

    return {
      backend: backend,
      getStatus: snapshotStatus,
      isDesktop: function () { return !!status.desktop; },
      refreshStatus: refreshStatus,
      subscribe: function (fn) {
        if (typeof fn !== "function") return function () {};
        listeners.push(fn);
        return function () {
          var idx = listeners.indexOf(fn);
          if (idx >= 0) listeners.splice(idx, 1);
        };
      },
      destroy: function () {
        if (destroyed) return;
        destroyed = true;
        backend.cancel();
        stopDetachObserver();
        if (registered) {
          var vi = getVoiceInput();
          if (vi && typeof vi.unregisterBackend === "function") vi.unregisterBackend("native");
          registered = false;
        }
        if (win.removeEventListener) {
          win.removeEventListener("ws-native-speech", handleTopNativeSpeech);
          win.removeEventListener("ws-native-speech-bridge", onBridgeReady);
          win.removeEventListener("message", isTop ? handleTopMessage : handleFrameMessage);
          win.removeEventListener("pagehide", onUnload);
          win.removeEventListener("beforeunload", onUnload);
        }
        listeners = [];
      }
    };
  }

  var defaultInstance = null;
  function ensureDefault() {
    if (!defaultInstance && typeof window !== "undefined") {
      defaultInstance = createNativeAdapter();
    }
    return defaultInstance;
  }

  window.WsVoiceNative = {
    create: createNativeAdapter,
    normalizeLocale: normalizeLocale,
    getStatus: function () {
      var inst = ensureDefault();
      return inst ? inst.getStatus() : { desktop: false, available: false, on_device: false, locale: "", reason: "desktop_only", ready: false };
    },
    isDesktop: function () {
      var inst = ensureDefault();
      return !!(inst && inst.isDesktop());
    },
    refreshStatus: function (locale) {
      var inst = ensureDefault();
      return inst ? inst.refreshStatus(locale) : false;
    },
    subscribe: function (fn) {
      var inst = ensureDefault();
      return inst ? inst.subscribe(fn) : function () {};
    }
  };

  if (typeof window !== "undefined" && typeof document !== "undefined" && !window.__wsVoiceNativeNoAuto) {
    ensureDefault();
  }
})();
