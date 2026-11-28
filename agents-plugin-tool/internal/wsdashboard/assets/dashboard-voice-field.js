// Shared Spacebar PTT attachment for WsInput fields outside the main composer.
(function () {
  "use strict";

  function live(state) {
    return state === "recording" || state === "transcribing" || state === "refining";
  }

  function hubFor(doc) {
    if (doc.__wsVoiceFieldHub) return doc.__wsVoiceFieldHub;
    var win = doc.defaultView || window;
    var ptt = win.WsVoicePTT;
    var voice = win.WsVoiceInput;
    if (!ptt || !voice) return null;
    var hub = { doc: doc, win: win, fields: [], active: null, controller: null };

    function activeSpan() { return hub.active && hub.active.span; }
    function setLock(field, on) {
      if (!field || field.locked === on) return;
      field.locked = on;
      var editor = field.el.wsc && field.el.wsc.editor;
      if (on) {
        field.wasDisabled = !!field.el.disabled;
        field.el.setAttribute("data-voice-locked", "1");
        field.el.disabled = true;
        if (editor) {
          editor.setAttribute("contenteditable", "false");
          editor.setAttribute("aria-disabled", "true");
        }
      } else {
        field.el.removeAttribute("data-voice-locked");
        field.el.disabled = !!field.wasDisabled;
        if (editor) {
          editor.setAttribute("contenteditable", field.wasDisabled ? "false" : "true");
          editor.setAttribute("aria-disabled", String(!!field.wasDisabled));
        }
      }
    }
    hub.controller = voice.createController({
      onStateChange: function (state, detail) {
        var field = hub.active, span = activeSpan();
        if (!field) return;
        setLock(field, live(state));
        if (!span) return;
        if (state === "recording") span.beginListening();
        else if (state === "transcribing" || state === "refining") span.setRefining();
        else if (state === "error") span.showError(detail && detail.error);
        else span.reset();
      },
      onPartial: function (text) { var span = activeSpan(); if (span) span.setPartial(text); },
      onFinal: function (text) { var field = hub.active, span = activeSpan(); if (field) setLock(field, false); if (span) span.commitFinal(text); },
      onError: function (err) {
        var field = hub.active, span = activeSpan();
        if (field) setLock(field, false);
        // A failed attempt already reached "error" through onStateChange, which
        // rendered the capsule; only errors raised without a state change
        // (e.g. recorder busy) still need showing.
        if (span && hub.controller.getState() !== "error") span.showError(err);
      }
    });
    function armedAny() {
      return hub.fields.some(function (field) { return field.ptt && field.ptt.isArmed(); });
    }
    function disarmAll() {
      hub.fields.forEach(function (field) { if (field.ptt && field.ptt.isArmed()) field.ptt.disarm(); });
    }
    function onKeyUp(e) { hub.fields.forEach(function (field) { if (field.ptt) field.ptt.handleDocumentKeyUp(e); }); }
    function cancelActive() {
      if (!hub.active) return;
      if (hub.active.ptt) hub.active.ptt.disarm();
      hub.controller.cancel({ discardLast: true });
      setLock(hub.active, false);
    }
    // Capture on window so the drawer/overview capture listeners on document
    // never see an Escape that belongs to voice. Only Escape is touched, and only
    // while voice has something to cancel; idle Escape propagates untouched.
    function onEscape(e) {
      if (!e || e.key !== "Escape") return;
      if (!armedAny() && !live(hub.controller.getState())) return;
      if (typeof e.preventDefault === "function") e.preventDefault();
      if (typeof e.stopImmediatePropagation === "function") e.stopImmediatePropagation();
      else if (typeof e.stopPropagation === "function") e.stopPropagation();
      cancelActive();
      disarmAll();
    }
    // Window blur ends a hold cleanly: handleWindowBlur stops a recording so
    // the audio already spoken is still transcribed. It must not cancel.
    function onBlur() { hub.fields.forEach(function (field) { if (field.ptt) field.ptt.handleWindowBlur(); }); }
    function isPageHidden() {
      var root = doc.documentElement;
      return !!(doc.hidden || (root && typeof root.hasAttribute === "function" && root.hasAttribute("data-animation-paused")));
    }
    function teardownActive() {
      disarmAll();
      cancelActive();
      var span = activeSpan();
      if (span) span.reset();
    }
    function onPageVisibility() { if (isPageHidden()) teardownActive(); }
    function onCapabilitiesRefresh() { hub.controller.refreshCapabilities(); }
    hub.wired = [
      [doc, "keyup", onKeyUp],
      [doc, "ws-page-visibility", onPageVisibility],
      [doc, "visibilitychange", onPageVisibility],
      [doc, "ws-native-speech-hidden", teardownActive],
      [doc, "ws-account-changed", onCapabilitiesRefresh],
      [win, "keydown", onEscape, true],
      [win, "blur", onBlur],
      [win, "ws-voice-capabilities-refresh", onCapabilitiesRefresh],
      [win, "pagehide", teardownActive],
      [win, "beforeunload", teardownActive]
    ];
    hub.wired.forEach(function (item) { if (item[0] && item[0].addEventListener) item[0].addEventListener(item[1], item[2], item[3]); });
    hub.destroy = function () {
      hub.wired.forEach(function (item) { if (item[0] && item[0].removeEventListener) item[0].removeEventListener(item[1], item[2], item[3]); });
      hub.controller.destroy();
      hub.active = null;
      delete doc.__wsVoiceFieldHub;
    };
    hub.cancelActive = cancelActive;
    hub.setLock = setLock;
    doc.__wsVoiceFieldHub = hub;
    hub.controller.refreshCapabilities();
    return hub;
  }

  function attachField(el) {
    var doc = el && el.ownerDocument;
    var hub = doc && hubFor(doc);
    if (!hub || !el || !hub.win.WsVoicePTT) return null;
    var field = { el: el, hub: hub, ptt: null, span: null, cap: null, locked: false, wasDisabled: false };
    function ensureSpan() {
      if (field.span) return field.span;
      var cap = doc.createElement("div");
      cap.className = "cvox-cap";
      cap.setAttribute("data-floating", "1");
      cap.hidden = true;
      (doc.body || doc.documentElement).appendChild(cap);
      field.cap = cap;
      field.span = hub.win.WsVoicePTT.createSpanController({
        document: doc, ctext: el, capsule: cap, floating: true,
        errorLabel: hub.win.WsVoicePTT.errorLabel,
        onCancel: function () { hub.cancelActive(); },
        onRetry: function () {
          // Only the field that owns the failed attempt may retry it; a stale
          // capsule must never hijack the hub while another field is live.
          if (hub.active !== field) {
            if (!(hub.active && live(hub.controller.getState())) && field.span) field.span.reset();
            return;
          }
          if (!hub.controller.canRetry()) return;
          hub.controller.retry();
        },
        onDismiss: function () { hub.cancelActive(); },
        canRetry: function () { return hub.active === field && hub.controller.canRetry(); }
      });
      return field.span;
    }
    field.ptt = hub.win.WsVoicePTT.attachSpacebarPTT({
      document: doc, window: hub.win, ctext: el, controller: hub.controller, globalEvents: true,
      onStartPTT: function () {
        var prev = hub.active;
        if (prev && prev !== field) {
          // The previous field's error capsule must go before this field takes over.
          if (prev.span) prev.span.reset();
          hub.setLock(prev, false);
        }
        hub.active = field;
        ensureSpan().captureCaret();
        hub.controller.start().catch(function () {});
      },
      onStopPTT: function () { if (hub.active === field) hub.controller.stop(); }
    });
    hub.fields.push(field);
    return {
      destroy: function () {
        if (hub.active === field) {
          hub.cancelActive();
          hub.active = null;
        }
        hub.setLock(field, false);
        if (field.ptt) field.ptt.destroy();
        if (field.span) field.span.destroy();
        if (field.cap && field.cap.parentNode) field.cap.parentNode.removeChild(field.cap);
        field.cap = null;
        field.span = null;
        var i = hub.fields.indexOf(field);
        if (i >= 0) hub.fields.splice(i, 1);
        if (!hub.fields.length) hub.destroy();
      }
    };
  }

  if (window.WsVoiceInput) window.WsVoiceInput.attachField = attachField;
  if (window.WsInput && typeof window.WsInput.flushVoice === "function") window.WsInput.flushVoice();
})();
