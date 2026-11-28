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
      if (on) {
        field.wasDisabled = !!field.el.disabled;
        field.el.setAttribute("data-voice-locked", "1");
        field.el.disabled = true;
        if (field.el.wsc && field.el.wsc.editor) field.el.wsc.editor.setAttribute("contenteditable", "false");
      } else {
        field.el.removeAttribute("data-voice-locked");
        field.el.disabled = !!field.wasDisabled;
        if (field.el.wsc && field.el.wsc.editor) field.el.wsc.editor.setAttribute("contenteditable", field.wasDisabled ? "false" : "true");
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
      onError: function (err) { var field = hub.active, span = activeSpan(); if (field) setLock(field, false); if (span) span.showError(err); }
    });
    function onKeyUp(e) { hub.fields.forEach(function (field) { if (field.ptt) field.ptt.handleDocumentKeyUp(e); }); }
    function cancelActive() {
      if (!hub.active) return;
      if (hub.active.ptt) hub.active.ptt.disarm();
      hub.controller.cancel({ discardLast: true });
      setLock(hub.active, false);
    }
    function onEscape(e) {
      var armed = hub.fields.some(function (field) { return field.ptt && field.ptt.isArmed(); });
      if (!armed && !live(hub.controller.getState())) return;
      e.preventDefault(); e.stopPropagation();
      cancelActive();
      hub.fields.forEach(function (field) { if (field.ptt && field.ptt.isArmed()) field.ptt.disarm(); });
    }
    function onBlur() { hub.fields.forEach(function (field) { if (field.ptt) field.ptt.handleWindowBlur(); }); cancelActive(); }
    function onHidden() { if (doc.hidden) cancelActive(); }
    hub.wired = [[doc, "keyup", onKeyUp], [doc, "keydown", onEscape, true], [doc, "visibilitychange", onHidden], [win, "blur", onBlur]];
    hub.wired.forEach(function (item) { item[0].addEventListener(item[1], item[2], item[3]); });
    hub.destroy = function () {
      hub.wired.forEach(function (item) { item[0].removeEventListener(item[1], item[2], item[3]); });
      hub.controller.destroy();
      delete doc.__wsVoiceFieldHub;
    };
    hub.cancelActive = cancelActive;
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
        onRetry: function () { hub.active = field; hub.controller.retry(); },
        onDismiss: function () { hub.cancelActive(); },
        canRetry: function () { return hub.controller.canRetry(); }
      });
      return field.span;
    }
    field.ptt = hub.win.WsVoicePTT.attachSpacebarPTT({
      document: doc, window: hub.win, ctext: el, controller: hub.controller, globalEvents: true,
      onStartPTT: function () { hub.active = field; ensureSpan().captureCaret(); hub.controller.start().catch(function () {}); },
      onStopPTT: function () { if (hub.active === field) hub.controller.stop(); }
    });
    hub.fields.push(field);
    return {
      destroy: function () {
        if (hub.active === field) hub.cancelActive();
        if (field.ptt) field.ptt.destroy();
        if (field.cap && field.cap.parentNode) field.cap.parentNode.removeChild(field.cap);
        var i = hub.fields.indexOf(field);
        if (i >= 0) hub.fields.splice(i, 1);
        if (!hub.fields.length) hub.destroy();
      }
    };
  }

  if (window.WsVoiceInput) window.WsVoiceInput.attachField = attachField;
  if (window.WsInput && typeof window.WsInput.flushVoice === "function") window.WsInput.flushVoice();
})();
