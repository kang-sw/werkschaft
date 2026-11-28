// Inline voice span lifecycle and Spacebar 1.0 s hold push-to-talk gesture.
// Coexists with WsCapsules (read() excludes [data-voice-span]) and WsMdEditor
// (skips reconcile while a span is live, then reconciles once after commit).
(function () {
  "use strict";

  var DEFAULT_HOLD_MS = 1000;

  function translate(key) {
    if (typeof window.wsT === "function") return window.wsT(key);
    return key;
  }

  function isSpaceKey(e) {
    return !!(e && (e.key === " " || e.code === "Space"));
  }

  // Collapse consecutive horizontal spaces immediately preceding the caret down
  // to exactly 1 space when preceded by non-space text on the line, or to ""
  // when at the start of an empty composer or line.
  function trimPrecedingSpacesForPTT(value, caret) {
    var v = String(value == null ? "" : value);
    var pos = Math.max(0, Math.min(v.length, typeof caret === "number" ? caret : v.length));
    var lineStart = v.lastIndexOf("\n", pos - 1) + 1;
    var before = v.slice(lineStart, pos);
    var m = /[ \t\u00a0]+$/.exec(before);
    if (!m) {
      return { start: pos, end: pos, replacement: "", nextCaret: pos, changed: false };
    }
    var runStart = pos - m[0].length;
    var hasNonSpaceBefore = runStart > lineStart;
    var replacement = hasNonSpaceBefore ? " " : "";
    if (m[0] === replacement) {
      return { start: runStart, end: pos, replacement: replacement, nextCaret: runStart + replacement.length, changed: false };
    }
    return {
      start: runStart,
      end: pos,
      replacement: replacement,
      nextCaret: runStart + replacement.length,
      changed: true
    };
  }

  function isPopupOpen(ctext, doc) {
    if (ctext && ctext.getAttribute && ctext.getAttribute("aria-expanded") === "true") return true;
    var editor = ctext && ctext.wsc && ctext.wsc.editor;
    if (editor && editor.getAttribute && editor.getAttribute("aria-expanded") === "true") return true;
    var d = doc || (ctext && ctext.ownerDocument) || (typeof document !== "undefined" ? document : null);
    if (d && typeof d.querySelector === "function") {
      try {
        if (d.querySelector(".wsm-pop:not([hidden]), .chist:not([hidden])")) return true;
      } catch (e) {}
    }
    return false;
  }

  function insertSpanAtCaret(editor, span, caretOffset, wsc) {
    if (!editor || !span) return false;
    if (wsc && typeof wsc.point === "function") {
      try {
        var pt = wsc.point(caretOffset);
        if (pt && pt[0]) {
          var node = pt[0];
          var off = pt[1] || 0;
          if (node.nodeType === 3) {
            var parent = node.parentNode;
            if (parent) {
              var len = (node.nodeValue || "").length;
              if (off <= 0) {
                parent.insertBefore(span, node);
              } else if (off >= len) {
                parent.insertBefore(span, node.nextSibling || null);
              } else if (typeof node.splitText === "function") {
                var tail = node.splitText(off);
                parent.insertBefore(span, tail);
              } else {
                parent.insertBefore(span, node.nextSibling || null);
              }
              return true;
            }
          } else if (node.nodeType === 1) {
            var ref = (node.childNodes && node.childNodes[off]) || null;
            if (typeof node.insertBefore === "function") {
              node.insertBefore(span, ref);
              return true;
            }
          }
        }
      } catch (e) {}
    }
    if (typeof editor.appendChild === "function") {
      editor.appendChild(span);
      return true;
    }
    return false;
  }

  function removeNode(node) {
    if (!node) return;
    if (typeof node.remove === "function") {
      node.remove();
    } else if (node.parentNode && typeof node.parentNode.removeChild === "function") {
      node.parentNode.removeChild(node);
    } else if (node.parentNode && Array.isArray(node.parentNode.children)) {
      var idx = node.parentNode.children.indexOf(node);
      if (idx >= 0) node.parentNode.children.splice(idx, 1);
      node.parentNode = null;
    }
  }

  function applyRangeEdit(ctext, start, end, replacement) {
    var val = String(ctext.value || "");
    var a = Math.max(0, Math.min(val.length, start));
    var b = Math.max(a, Math.min(val.length, end));
    if (typeof ctext.setRangeText === "function") {
      ctext.setRangeText(replacement, a, b, "end");
    } else {
      ctext.value = val.slice(0, a) + replacement + val.slice(b);
      if (typeof ctext.setSelectionRange === "function") {
        ctext.setSelectionRange(a + replacement.length, a + replacement.length);
      }
    }
    if (typeof Event === "function" && typeof ctext.dispatchEvent === "function") {
      ctext.dispatchEvent(new Event("input", { bubbles: true }));
    }
  }

  function createSpanController(opts) {
    opts = opts || {};
    var doc = opts.document || document;
    var ctext = opts.ctext;
    var cap = opts.capsule;
    var errorLabel = opts.errorLabel || function (err) { return (err && err.code) || translate("js.voice.err.failed"); };
    var onCancel = opts.onCancel || function () {};
    var onRetry = opts.onRetry || function () {};
    var onDismiss = opts.onDismiss || function () {};
    var canRetry = opts.canRetry || function () { return false; };

    var liveSpan = null;
    var caretStart = 0;
    var caretEnd = 0;

    function getEditor() {
      return (ctext && ctext.wsc && ctext.wsc.editor) || null;
    }

    function captureCaret() {
      var val = String((ctext && ctext.value) || "");
      var start = typeof ctext.selectionStart === "number" ? ctext.selectionStart : val.length;
      var end = typeof ctext.selectionEnd === "number" ? ctext.selectionEnd : start;
      caretStart = Math.max(0, Math.min(val.length, start));
      caretEnd = Math.max(caretStart, Math.min(val.length, end));
      return { start: caretStart, end: caretEnd };
    }

    function positionCapsule() {
      if (!cap) return;
      var field = cap.parentNode;
      var anchor = null;
      if (ctext && ctext.wsc && typeof ctext.wsc.anchor === "function") {
        try { anchor = ctext.wsc.anchor(caretStart); } catch (e) { anchor = null; }
      }
      var fieldRect = field && typeof field.getBoundingClientRect === "function"
        ? field.getBoundingClientRect()
        : { left: 0, top: 0, width: 320, height: 40 };
      var left = 8;
      var top = 4;
      if (anchor && typeof anchor.x === "number") {
        left = Math.max(4, Math.round(anchor.x - fieldRect.left));
        if (fieldRect.width > 80) left = Math.min(left, Math.max(4, Math.round(fieldRect.width - 140)));
        if (typeof anchor.bottom === "number") {
          top = Math.max(2, Math.round(anchor.bottom - fieldRect.top + 4));
        }
      }
      cap.style.left = left + "px";
      cap.style.top = top + "px";
      cap.setAttribute("data-caret", String(caretStart));
    }

    function renderCapsule(state, text, err) {
      if (!cap) return;
      cap.textContent = "";
      if (state === "idle") {
        cap.hidden = true;
        cap.removeAttribute("data-state");
        return;
      }
      cap.hidden = false;
      cap.setAttribute("data-state", state);
      positionCapsule();
      var dot = doc.createElement("span");
      dot.className = "cvox-dot";
      dot.setAttribute("aria-hidden", "true");
      cap.appendChild(dot);

      var span = doc.createElement("span");
      span.className = "cvox-text";
      if (state === "recording") span.textContent = text || translate("js.voice.recording");
      else if (state === "transcribing" || state === "refining") span.textContent = text || translate("js.voice.transcribing");
      else if (state === "error") span.textContent = errorLabel(err);
      cap.appendChild(span);

      var acts = doc.createElement("span");
      acts.className = "cvox-acts";
      if (state === "recording" || state === "transcribing" || state === "refining") {
        var cancelBtn = doc.createElement("button");
        cancelBtn.type = "button";
        cancelBtn.className = "cvox-btn cvox-muted";
        cancelBtn.setAttribute("data-voice-action", "cancel");
        cancelBtn.textContent = translate("js.voice.cancel");
        cancelBtn.addEventListener("click", onCancel);
        acts.appendChild(cancelBtn);
      } else if (state === "error") {
        if (canRetry()) {
          var resendBtn = doc.createElement("button");
          resendBtn.type = "button";
          resendBtn.className = "cvox-btn";
          resendBtn.setAttribute("data-voice-action", "resend");
          resendBtn.textContent = translate("js.voice.resend");
          resendBtn.addEventListener("click", onRetry);
          acts.appendChild(resendBtn);
        }
        var dismissBtn = doc.createElement("button");
        dismissBtn.type = "button";
        dismissBtn.className = "cvox-btn cvox-muted";
        dismissBtn.setAttribute("data-voice-action", "dismiss");
        dismissBtn.textContent = translate("js.voice.dismiss");
        dismissBtn.addEventListener("click", function () {
          onDismiss();
          renderCapsule("idle", "");
        });
        acts.appendChild(dismissBtn);
      }
      cap.appendChild(acts);
    }

    function clearSpan() {
      if (!liveSpan) return;
      removeNode(liveSpan);
      liveSpan = null;
    }

    function reconcileEditorOnce() {
      var editor = getEditor();
      if (editor && typeof editor._wsmdReconcile === "function") {
        editor._wsmdReconcile();
      } else if (ctext && ctext.wsInput && typeof ctext.wsInput.reconcileMarkdown === "function") {
        ctext.wsInput.reconcileMarkdown();
      }
    }

    function beginListening() {
      clearSpan();
      var editor = getEditor();
      if (editor) {
        var span = doc.createElement("span");
        span.setAttribute("data-voice-span", "listening");
        span.setAttribute("data-label", translate("js.voice.listening"));
        span.setAttribute("contenteditable", "false");
        span.setAttribute("aria-live", "polite");
        insertSpanAtCaret(editor, span, caretStart, ctext.wsc);
        liveSpan = span;
        renderCapsule("idle", "");
        return span;
      }
      renderCapsule("recording", "");
      return null;
    }

    function setPartial(text) {
      var str = String(text == null ? "" : text);
      if (liveSpan) {
        liveSpan.textContent = str;
        var editor = getEditor();
        if (editor && typeof editor.scrollHeight === "number") {
          editor.scrollTop = editor.scrollHeight;
        }
        return;
      }
      if (cap && !cap.hidden) {
        var node = cap.querySelector(".cvox-text");
        if (node) node.textContent = str;
      }
    }

    function setRefining() {
      if (liveSpan) {
        liveSpan.setAttribute("data-voice-span", "refining");
        liveSpan.setAttribute("data-label", translate("js.voice.refining"));
        return;
      }
      var editor = getEditor();
      if (editor) {
        var span = doc.createElement("span");
        span.setAttribute("data-voice-span", "refining");
        span.setAttribute("data-label", translate("js.voice.refining"));
        span.setAttribute("contenteditable", "false");
        insertSpanAtCaret(editor, span, caretStart, ctext.wsc);
        liveSpan = span;
        renderCapsule("idle", "");
        return;
      }
      renderCapsule("transcribing", "");
    }

    function commitFinal(text) {
      var finalText = String(text == null ? "" : text);
      var hadSpan = !!liveSpan;
      clearSpan();
      renderCapsule("idle", "");
      if (finalText) {
        applyRangeEdit(ctext, caretStart, caretEnd, finalText);
      }
      if (hadSpan || finalText) {
        reconcileEditorOnce();
      }
      if (ctext && typeof ctext.focus === "function") ctext.focus();
    }

    function showError(err) {
      clearSpan();
      reconcileEditorOnce();
      renderCapsule("error", "", err);
    }

    function reset() {
      var hadSpan = !!liveSpan;
      clearSpan();
      if (hadSpan) reconcileEditorOnce();
      renderCapsule("idle", "");
    }

    return {
      captureCaret: captureCaret,
      getCaret: function () { return { start: caretStart, end: caretEnd }; },
      getLiveSpan: function () { return liveSpan; },
      beginListening: beginListening,
      setPartial: setPartial,
      setRefining: setRefining,
      commitFinal: commitFinal,
      showError: showError,
      reset: reset,
      renderCapsule: renderCapsule
    };
  }

  function attachSpacebarPTT(opts) {
    opts = opts || {};
    var doc = opts.document || document;
    var win = opts.window || (typeof window !== "undefined" ? window : null);
    var ctext = opts.ctext;
    var controller = opts.controller;
    var holdMs = typeof opts.holdMs === "number" ? opts.holdMs : DEFAULT_HOLD_MS;
    var setTimer = opts.setTimeout || (win && win.setTimeout) || (typeof setTimeout === "function" ? setTimeout : null);
    var clearTimer = opts.clearTimeout || (win && win.clearTimeout) || (typeof clearTimeout === "function" ? clearTimeout : null);
    var onStartPTT = opts.onStartPTT || function () {};
    var onStopPTT = opts.onStopPTT || function () {};
    var onHoldingChange = opts.onHoldingChange || function () {};

    var timer = null;
    // Generation counter bumped on every arm/disarm; each timer callback is
    // bound to the token it was scheduled under, so a timer that somehow
    // outlives its press can never fire for a later one.
    var armToken = 0;
    var armed = false;
    var active = false;
    var composing = false;
    var lockBlurSuppressed = false;
    // ctext.value when the pending arm began (before the Space itself is typed).
    var armValue = "";

    function getEditor() {
      return (ctext && ctext.wsc && ctext.wsc.editor) || null;
    }

    function isComposerTarget(target) {
      if (!target) return false;
      if (target === ctext) return true;
      var editor = getEditor();
      if (editor && (target === editor || (typeof editor.contains === "function" && editor.contains(target)))) {
        return true;
      }
      return false;
    }

    function disarm() {
      armToken++;
      if (timer !== null) {
        clearTimer(timer);
        timer = null;
      }
      var wasArmed = armed;
      armed = false;
      active = false;
      if (wasArmed) onHoldingChange(false);
    }

    // Controller.start() accepts "idle" and "error" (a failed attempt leaves
    // "error" until something resets it); PTT must not be stricter than the
    // mic button or one failure locks Space out for good.
    function canStartFrom(state) {
      return state === "idle" || state === "error";
    }

    function isLiveState(state) {
      return state === "recording" || state === "transcribing" || state === "refining";
    }

    function disarmPending() {
      if (armed && !active) disarm();
    }

    // True when cur is prev plus exactly one inserted " " (the Space keystroke
    // that armed the hold typing itself into the composer).
    function isSingleSpaceInsertion(prev, cur) {
      if (cur.length !== prev.length + 1) return false;
      for (var i = 0; i < cur.length; i++) {
        if (cur.charAt(i) === " " && cur.slice(0, i) + cur.slice(i + 1) === prev) return true;
      }
      return false;
    }

    // Anything other than the arming Space's own typed character means the user
    // is typing/clicking, not holding to talk.
    function onPendingBreaker(e) {
      if (!armed || active) return;
      var type = e && e.type;
      if (type === "beforeinput" || type === "input") {
        if (e.data === " ") return;
        if (type === "input" && (e.data === undefined || e.data === null) &&
            isSingleSpaceInsertion(armValue, String(ctext.value || ""))) return;
      }
      disarm();
    }

    function onCompositionStart() {
      composing = true;
      disarmPending();
    }

    function onCompositionEnd() {
      composing = false;
    }

    // capsules.js re-dispatches editor key events onto ctext, so one physical
    // key press can reach this handler twice (ctext copy, then the real event
    // on the editor). The `armed` guard below makes the second one a no-op.
    function onKeyDown(e) {
      if (!e) return;
      if (!isSpaceKey(e)) {
        disarmPending();
        return;
      }
      if (e.repeat) {
        if (armed || active) {
          if (typeof e.preventDefault === "function") e.preventDefault();
        }
        return;
      }
      if (!isComposerTarget(e.target)) return;
      if (e.isComposing || e.keyCode === 229 || composing) {
        disarmPending();
        return;
      }
      if (e.defaultPrevented) {
        // Someone consumed the key (e.g. a forwarded copy was handled): a
        // pending arm from the sibling listener must not survive it.
        disarmPending();
        return;
      }
      if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
      if (isPopupOpen(ctext, doc)) return;
      if (!controller) return;
      // A hold whose recording failed (error/idle) or was cancelled underneath
      // it is stale: drop it so a fresh press can arm even if its keyup was lost.
      if (armed && active && !isLiveState(controller.getState())) disarm();
      if (!canStartFrom(controller.getState())) return;
      if (!controller.getResolvedBackend || !controller.getResolvedBackend()) return;
      // Duplicate delivery of the same press (or a press while recording).
      if (armed) return;

      armed = true;
      active = false;
      armValue = String((ctext && ctext.value) || "");
      var token = ++armToken;
      if (timer !== null) {
        clearTimer(timer);
        timer = null;
      }
      onHoldingChange(true);
      timer = setTimer(function () {
        if (token !== armToken) return;
        timer = null;
        if (!armed) return;
        if (!controller || !canStartFrom(controller.getState()) || !controller.getResolvedBackend()) {
          disarm();
          return;
        }
        active = true;
        onHoldingChange(false);
        try {
          var caret = typeof ctext.selectionStart === "number" ? ctext.selectionStart : String(ctext.value || "").length;
          var trimmed = trimPrecedingSpacesForPTT(ctext.value || "", caret);
          if (trimmed.changed) {
            applyRangeEdit(ctext, trimmed.start, trimmed.end, trimmed.replacement);
          }
          onStartPTT();
        } catch (err) {
          disarm();
          return;
        }
        // start() refused synchronously (e.g. recorder busy): nothing is
        // recording, so do not leave a phantom active hold behind.
        if (token === armToken && !isLiveState(controller.getState())) disarm();
      }, holdMs);
    }

    function releaseFromPTT(e) {
      if (!armed && !active) return;
      var wasActive = active;
      disarm();
      if (wasActive) {
        if (e && typeof e.preventDefault === "function") e.preventDefault();
        if (controller && controller.getState() === "recording") {
          onStopPTT();
        }
      }
    }

    function onDocKeyUp(e) {
      if (!isSpaceKey(e)) return;
      if (!armed && !active) return;
      releaseFromPTT(e);
    }

    function onEditorBlur() {
      if (!armed && !active) return;
      if (active && lockBlurSuppressed) return;
      releaseFromPTT(null);
    }

    function onWindowBlur() {
      if (!armed && !active) return;
      releaseFromPTT(null);
    }

    function beginLockBlurGuard() {
      lockBlurSuppressed = true;
    }

    function endLockBlurGuard() {
      if (typeof Promise === "function") {
        Promise.resolve().then(function () { lockBlurSuppressed = false; });
      } else {
        lockBlurSuppressed = false;
      }
    }

    // Listen on both ctext and (when WsCapsules is attached) its editor. Key
    // events from the editor reach ctext only as capsules' forwarded copies,
    // and capsules skips forwarding when the real event is already
    // defaultPrevented, so keyup must be observed on the editor too to release
    // reliably. Every handler is idempotent, so duplicate delivery is harmless.
    var editor = getEditor();
    var boundTargets = [];
    if (ctext && ctext.addEventListener) boundTargets.push(ctext);
    if (editor && editor !== ctext && editor.addEventListener) boundTargets.push(editor);
    var boundHandlers = [
      ["keydown", onKeyDown],
      ["keyup", onDocKeyUp],
      ["blur", onEditorBlur],
      ["compositionstart", onCompositionStart],
      ["compositionend", onCompositionEnd],
      ["mousedown", onPendingBreaker],
      ["pointerdown", onPendingBreaker],
      ["beforeinput", onPendingBreaker],
      ["input", onPendingBreaker]
    ];
    boundTargets.forEach(function (target) {
      boundHandlers.forEach(function (h) { target.addEventListener(h[0], h[1]); });
    });
    if (doc && doc.addEventListener) {
      doc.addEventListener("keyup", onDocKeyUp);
    }
    if (win && win.addEventListener) {
      win.addEventListener("blur", onWindowBlur);
    }

    return {
      isArmed: function () { return armed; },
      isActive: function () { return active; },
      disarm: disarm,
      beginLockBlurGuard: beginLockBlurGuard,
      endLockBlurGuard: endLockBlurGuard,
      destroy: function () {
        disarm();
        boundTargets.forEach(function (target) {
          if (typeof target.removeEventListener !== "function") return;
          boundHandlers.forEach(function (h) { target.removeEventListener(h[0], h[1]); });
        });
        if (doc && doc.removeEventListener) {
          doc.removeEventListener("keyup", onDocKeyUp);
        }
        if (win && win.removeEventListener) {
          win.removeEventListener("blur", onWindowBlur);
        }
      }
    };
  }

  function errorLabel(err) {
    var code = (err && err.code) || "";
    if (code === "mic_denied" || code === "mic_unavailable" || code === "permission_denied") return translate("js.voice.err.mic");
    if (code === "recorder_busy" || code === "busy") return translate("js.voice.err.busy");
    if (code === "upstream_timeout") return translate("js.voice.err.timeout");
    if (code === "provider_auth_failed") return translate("js.voice.err.auth");
    if (code === "audio_too_large") return translate("js.voice.err.too_large");
    if (code === "unavailable" || code === "privacy_disabled" || code === "language_unavailable") return translate("js.voice.err.unavailable");
    return translate("js.voice.err.failed");
  }

  function createComposerLock(composer, ctext, csend, getPtt) {
    var locked = false;
    var prevCtextDisabled = false;
    var prevCsendDisabled = false;
    return function setLock(next) {
      if (locked === next) return;
      locked = next;
      var ptt = typeof getPtt === "function" ? getPtt() : null;
      if (next) {
        if (ptt && typeof ptt.beginLockBlurGuard === "function") ptt.beginLockBlurGuard();
        prevCtextDisabled = !!ctext.disabled;
        prevCsendDisabled = !!(csend && csend.disabled);
        composer.setAttribute("data-voice-locked", "1");
        ctext.disabled = true;
        if (ctext.wsc && ctext.wsc.editor) {
          ctext.wsc.editor.setAttribute("contenteditable", "false");
          ctext.wsc.editor.setAttribute("aria-disabled", "true");
        }
        if (csend) csend.disabled = true;
        if (ptt && typeof ptt.endLockBlurGuard === "function") ptt.endLockBlurGuard();
      } else {
        composer.removeAttribute("data-voice-locked");
        ctext.disabled = prevCtextDisabled;
        if (ctext.wsc && ctext.wsc.editor) {
          ctext.wsc.editor.setAttribute("contenteditable", prevCtextDisabled ? "false" : "true");
          ctext.wsc.editor.setAttribute("aria-disabled", String(prevCtextDisabled));
        }
        if (csend) csend.disabled = prevCsendDisabled;
      }
    };
  }

  function syncMicButton(btn, state, backend, isEligible, showWhenEligible) {
    if (!btn) return;
    var visible = showWhenEligible ? !!isEligible : !!backend;
    btn.hidden = !visible;
    var slot = btn.parentNode && btn.parentNode.classList && btn.parentNode.classList.contains("cvox-slot")
      ? btn.parentNode : null;
    if (slot) {
      slot.hidden = !visible;
      var foot = slot.parentNode;
      if (foot && foot.getAttribute && foot.getAttribute("data-cfoot-empty") === "1") {
        foot.hidden = !visible;
      }
    }
    if (state === "recording") {
      btn.setAttribute("data-state", "recording");
      btn.setAttribute("aria-pressed", "true");
      btn.title = translate("js.voice.stop");
      btn.setAttribute("aria-label", translate("js.voice.stop"));
    } else if (state === "transcribing" || state === "refining") {
      btn.setAttribute("data-state", "transcribing");
      btn.setAttribute("aria-pressed", "true");
      btn.title = translate("js.voice.transcribing");
      btn.setAttribute("aria-label", translate("js.voice.transcribing"));
    } else if (!backend && isEligible && showWhenEligible) {
      btn.setAttribute("data-state", "unconfigured");
      btn.setAttribute("aria-pressed", "false");
      btn.title = translate("js.voice.setup");
      btn.setAttribute("aria-label", translate("js.voice.setup"));
    } else {
      btn.removeAttribute("data-state");
      btn.setAttribute("aria-pressed", "false");
      btn.title = translate("js.voice.start");
      btn.setAttribute("aria-label", translate("js.voice.start"));
    }
  }

  function openVoiceSettings(win, opener) {
    var target = "/settings?section=voice";
    var w = win || (typeof window !== "undefined" ? window : null);
    if (!w) return false;
    if (typeof w.wsOpenSettings === "function") {
      w.wsOpenSettings(target, opener);
      return true;
    }
    var origin = (w.location && w.location.origin) || "";
    var href = origin ? origin + target : target;
    var isFramed = false;
    try { isFramed = !!(w.parent && w.parent !== w); } catch (e) {}
    if (isFramed && w.parent && typeof w.parent.postMessage === "function") {
      w.parent.postMessage({ wsShell: "nav", href: href }, origin || "*");
      return true;
    }
    if (w.location) {
      w.location.href = target;
      return true;
    }
    return false;
  }

  window.WsVoicePTT = {
    DEFAULT_HOLD_MS: DEFAULT_HOLD_MS,
    errorLabel: errorLabel,
    createComposerLock: createComposerLock,
    syncMicButton: syncMicButton,
    openVoiceSettings: openVoiceSettings,
    trimPrecedingSpacesForPTT: trimPrecedingSpacesForPTT,
    isPopupOpen: isPopupOpen,
    createSpanController: createSpanController,
    attachSpacebarPTT: attachSpacebarPTT
  };
})();
