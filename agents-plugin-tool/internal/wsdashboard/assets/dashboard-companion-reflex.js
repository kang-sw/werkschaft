(function (C) {
  "use strict";

  // Tier 0: 0ms Client Physical Reflex.
  // Pure deterministic frontend motion layer with zero LLM/network wait.
  // - On Messenger send, triggers phone_pickup within 1 frame and arms a 6s
  //   client reflex lease that suppresses stale snapshot buzz and the 4s
  //   PHONE_UNCONFIRMED_GRACE revert in colead.js.
  // - On an authoritative Puppeteer phone_* command (monotonic seq), yields
  //   the client reflex lease immediately and tracks a bounded Puppeteer hold
  //   (clamped to [1s, 15s]) for phone_pickup/phone_typing, or clears on
  //   phone_putdown/phone_merong_facedown/phone_buzz.
  // - On Brain brain_work_start / subagent_event / brain_idle (from the shell
  //   rail SSE "companion-brain-state") or hold expiry, safely transitions to
  //   phone_putdown so the avatar never gets stuck holding the phone.
  const REFLEX_LEASE_MS = 6000;
  const PUPPETEER_HOLD_MIN_MS = 1000;
  const PUPPETEER_HOLD_DEFAULT_MS = 8000;
  const PUPPETEER_HOLD_MAX_MS = 15000;

  const PHONE_HOLD_GESTURES = new Set(["phone_pickup", "phone_typing"]);
  const PHONE_RELEASE_GESTURES = new Set(["phone_putdown", "phone_merong_facedown", "phone_buzz"]);
  const BRAIN_RELEASE_KINDS = new Set(["brain_work_start", "subagent_event", "brain_idle"]);

  let reflexActive = false;
  let reflexTimer = 0;
  let puppeteerHoldActive = false;
  let puppeteerTimer = 0;
  let lastCommandSeq = 0;
  let lastBrainSeq = 0;
  let customTrigger = null;

  function clampMs(raw, min, max, fallback) {
    const n = Number(raw);
    if (!Number.isFinite(n) || n <= 0) return fallback;
    return Math.max(min, Math.min(max, Math.round(n)));
  }

  function clearTimer(id) {
    if (id && typeof clearTimeout === "function") clearTimeout(id);
    return 0;
  }

  function invokeGesture(name) {
    if (typeof customTrigger === "function") return !!customTrigger(name);
    if (typeof C.triggerGesture === "function") {
      const ok = !!C.triggerGesture(name);
      if (ok) C.wakeStage?.();
      return ok;
    }
    return false;
  }

  function clearReflexLease() {
    reflexActive = false;
    reflexTimer = clearTimer(reflexTimer);
  }

  function clearPuppeteerHold() {
    puppeteerHoldActive = false;
    puppeteerTimer = clearTimer(puppeteerTimer);
  }

  function armReflexLease() {
    clearReflexLease();
    reflexActive = true;
    if (typeof setTimeout === "function") {
      reflexTimer = setTimeout(() => {
        reflexTimer = 0;
        if (!reflexActive) return;
        reflexActive = false;
        if (!puppeteerHoldActive) invokeGesture("phone_putdown");
      }, REFLEX_LEASE_MS);
    }
  }

  function armPuppeteerHold(ttlMs) {
    clearPuppeteerHold();
    puppeteerHoldActive = true;
    const ms = clampMs(ttlMs, PUPPETEER_HOLD_MIN_MS, PUPPETEER_HOLD_MAX_MS, PUPPETEER_HOLD_DEFAULT_MS);
    if (typeof setTimeout === "function") {
      puppeteerTimer = setTimeout(() => {
        puppeteerTimer = 0;
        if (!puppeteerHoldActive) return;
        puppeteerHoldActive = false;
        if (!reflexActive) invokeGesture("phone_putdown");
      }, ms);
    }
  }

  function isPhoneHoldActive() {
    return reflexActive || puppeteerHoldActive;
  }

  function onUserSend() {
    clearPuppeteerHold();
    armReflexLease();
    invokeGesture("phone_pickup");
    return true;
  }

  // onCommand validates monotonic seq (when present) and coordinates handoff
  // from the 0ms client reflex lease to authoritative Puppeteer phone_* commands.
  // Returns false when a stale out-of-order command should be dropped.
  function onCommand(payload) {
    if (!payload || typeof payload !== "object") return true;
    const seq = Number(payload.seq);
    if (Number.isFinite(seq) && seq > 0) {
      if (seq <= lastCommandSeq) return false;
      lastCommandSeq = seq;
    }
    const g = typeof payload.gesture === "string" ? payload.gesture.trim().toLowerCase() : "";
    if (!g || !g.startsWith("phone_")) return true;

    // Any authoritative phone_* command supersedes the optimistic client reflex lease.
    clearReflexLease();
    if (PHONE_HOLD_GESTURES.has(g)) {
      armPuppeteerHold(payload.ttl_ms);
    } else if (PHONE_RELEASE_GESTURES.has(g)) {
      clearPuppeteerHold();
    }
    return true;
  }

  // onBrainState processes additive shell rail SSE "companion-brain-state"
  // notifications ({ kind, replyId, seq }). Even when Messenger is closed,
  // work/subagent/idle transitions safely release any phone hold back to desk.
  function onBrainState(payload) {
    if (!payload || typeof payload !== "object") return false;
    const seq = Number(payload.seq);
    if (Number.isFinite(seq) && seq > 0) {
      if (seq <= lastBrainSeq) return false;
      lastBrainSeq = seq;
    }
    const kind = typeof payload.kind === "string" ? payload.kind.trim().toLowerCase() : "";
    if (!kind) return false;
    if (BRAIN_RELEASE_KINDS.has(kind)) {
      const wasHeld = isPhoneHoldActive();
      clearReflexLease();
      clearPuppeteerHold();
      if (wasHeld) invokeGesture("phone_putdown");
      return true;
    }
    return false;
  }

  // suppressPhoneSnapshot returns true while a client reflex lease or Puppeteer
  // hold should mask stale/redundant non-phone_active snapshot updates (such as
  // legacy buzz or laptop_ambient snapshots that would flicker the phone down).
  function suppressPhoneSnapshot(snap) {
    if (!isPhoneHoldActive()) return false;
    if (!snap || typeof snap !== "object") return false;
    if (snap.state === "phone_active" || snap.posture === "phone" || snap.phoneFaceDown) {
      return false;
    }
    return true;
  }

  function reset() {
    clearReflexLease();
    clearPuppeteerHold();
    lastCommandSeq = 0;
    lastBrainSeq = 0;
  }

  function setTriggerHandler(fn) {
    customTrigger = typeof fn === "function" ? fn : null;
  }

  function debugState() {
    return {
      reflexActive,
      puppeteerHoldActive,
      phoneHoldActive: isPhoneHoldActive(),
      lastCommandSeq,
      lastBrainSeq
    };
  }

  C.reflex = {
    REFLEX_LEASE_MS,
    PUPPETEER_HOLD_MAX_MS,
    onUserSend,
    onCommand,
    onBrainState,
    isPhoneHoldActive,
    suppressPhoneSnapshot,
    reset,
    setTriggerHandler,
    debugState
  };
})(window.WsCompanion);
