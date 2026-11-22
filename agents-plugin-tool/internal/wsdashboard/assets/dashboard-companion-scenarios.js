(function (C) {
  "use strict";
  const starters = {
    idle_roam: {
      on: ["session.idle", "idle.long"], priority: 5, cooldownMs: 5000, durationMs: 1250,
      pose: "stand", expression: "idle", gaze: "cursor", stage: "hop-bounce"
    },
    working_tatata: {
      on: "session.replying", priority: 10, durationMs: 1800,
      pose: "typing", expression: "focused", gaze: "scan"
    },
    attention_point: {
      on: "ask.open", priority: 65, cooldownMs: 900, durationMs: 1700,
      pose: "stand", gesture: "point", expression: "oo", gaze: "whiteboard-ask", stage: "hop-bounce", fx: "alert"
    },
    startled_poke: {
      on: "user.poke", priority: 45, durationMs: 550,
      pose: "stand", expression: "oo", stage: "squash-pop", fx: "shock"
    },
    drag_dangle: {
      on: "user.drag", priority: 100, durationMs: 0,
      pose: "dangle", expression: "oo", stage: "pendulum-drag"
    },
    gloom_fail: {
      on: "session.error", priority: 70, cooldownMs: 1000, durationMs: 2100,
      pose: "stand", gesture: "headrub", expression: "cry", gaze: "down"
    },
    celebrate_done: {
      on: "session.celebrate", priority: 60, cooldownMs: 1000, durationMs: 1400,
      pose: "stand", gesture: "wave", expression: "happy", stage: "double-hop", fx: "sparkle"
    },
    subagent_dispatch: {
      on: "subagent.spawn", priority: 55, cooldownMs: 500, durationMs: 2400,
      pose: "stand", gesture: "cup_hands", expression: "oo", stage: "anticipate-launch", gaze: "prop:drone",
      prop: { id: "drone", motion: "hand-liftoff", action: "launch", from: "hand", to: "hud-roster" }
    },
    subagent_return: {
      on: "subagent.done", priority: 56, cooldownMs: 500,
      steps: [
        { ms: 2200, pose: "stand", gesture: "cup_hands", expression: "oo", gaze: "prop:drone",
          prop: { id: "drone", motion: "hand-liftoff", action: "return", from: "hud-roster", to: "hand" } },
        { ms: 280, pose: "stand", gesture: "cup_hands", expression: "happy", stage: "settle-sigh" }
      ]
    },
    mail_send: {
      on: "mail.send", priority: 54, durationMs: 2000,
      pose: "stand", gesture: "cup_hands", expression: "oo", stage: "anticipate-launch", gaze: "prop:mail",
      prop: { id: "mail", motion: "hand-liftoff", from: "hand", to: "hud-roster" }
    },
    mailbox_bonk: {
      on: "mail.recv", priority: 57, durationMs: 2000,
      pose: "stand", expression: "oo", gaze: "prop:mail", prop: { id: "mail", motion: "bonk-from-edge" }
    },
    global_session_done: {
      on: "global.session_done", priority: 52, durationMs: 1600,
      pose: "stand", gesture: "wave", expression: "happy", gaze: "rail-alert"
    },
    session_compacted: {
      on: "session.compacted", priority: 48, durationMs: 1600,
      pose: "stand", expression: "half_lid", stage: "settle-sigh"
    },
    route_switch: {
      on: "route.switch", priority: 25, durationMs: 900,
      pose: "stand", expression: "idle", gaze: "cursor", stage: "hop-bounce"
    }
  };

  const passive = {
    idle: { pose: "stand", expression: "idle", gaze: "cursor" },
    doze: { pose: "perched", expression: "idle_doze", gaze: "cursor", fx: "zzz" },
    user_typing: { pose: "perched", expression: "idle", gaze: "caret" },
    replying: { pose: "typing", expression: "focused", gaze: "scan" },
    tool: { pose: "stand", expression: "focused", gaze: "scan" },
    thinking: { pose: "stand", gesture: "coffee", expression: "half_lid", gaze: "up", fx: "ponder" },
    permission: { pose: "stand", expression: "oo", gaze: "whiteboard-ask", fx: "alert" },
    error: { pose: "stand", expression: "cry", gaze: "down" }
  };

  function passiveBeats(overrides) {
    const beats = Object.create(null);
    for (const [state, fallback] of Object.entries(passive)) {
      const alias = state === "replying" ? "streaming" : state === "user_typing" ? "typing_user" : "";
      const exact = C.own(overrides, state);
      const patch = C.record(exact) ? exact : C.own(overrides, alias);
      const beat = beats[state] = { ...fallback };
      for (const field of ["pose", "gesture", "expression", "gaze", "stage", "fx", "prop"]) {
        const value = C.record(patch) && C.own(patch, field);
        if (C.timingId(value) || field === "gaze" && typeof value === "string" &&
            value.startsWith("prop:") && C.timingId(value.slice(5))) beat[field] = value;
      }
    }
    beats.typing_user = beats.user_typing;
    beats.streaming = beats.replying;
    return beats;
  }

  function scenarioRunner(overrides) {
    const registry = new Map();
    const streakLimit = new Map();
    for (const id of new Set([...Object.keys(starters), ...Object.keys(overrides || {})])) {
      const data = { ...C.own(starters, id), ...C.own(overrides, id) };
      if (data.disabled) continue;
      // A flat override explicitly carrying a duration replaces starter choreography.
      const patch = C.own(overrides, id);
      if (patch && Object.hasOwn(patch, "durationMs") && !Object.hasOwn(patch, "steps")) delete data.steps;
      const beats = data.steps?.map(step => ({ ...data, ...step })) || [{ ...data, ms: data.durationMs ?? 1000 }];
      const entry = { id, priority: data.priority ?? 0, cooldown: data.cooldownMs ?? 0, when: data.when, beats };
      const events = Array.isArray(data.on) ? data.on : [data.on];
      for (const event of events) {
        if (!event) continue;
        if (!registry.has(event)) registry.set(event, []);
        registry.get(event).push(entry);
        if (data.when?.streak) streakLimit.set(event, Math.max(streakLimit.get(event) || 0, data.when.streak));
      }
    }
    for (const list of registry.values()) list.sort((a, b) => b.priority - a.priority);

    const history = new Map();
    const starts = new Map();
    let playing = null;

    function matches(guard, samples, now, state) {
      if (!guard) return true;
      if (guard.routeKind && state.routeKind !== guard.routeKind) return false;
      if (guard.pose && state.pose !== guard.pose) return false;
      if (state.idleMs < (guard.idleMs ?? 0)) return false;
      if (guard.streak && (samples?.length < guard.streak || !samples ||
          now - samples[samples.length - guard.streak] > (guard.windowMs ?? 4000))) return false;
      if (guard.hourRange) {
        const hour = new Date().getHours(), [start, end] = guard.hourRange;
        if (!(start <= end ? hour >= start && hour < end : hour >= start || hour < end)) return false;
      }
      return true;
    }

    function fire(event, now, state, signal) {
      if (!registry.has(event)) return;
      let samples = history.get(event);
      const limit = streakLimit.get(event);
      if (limit) {
        if (!samples) history.set(event, samples = []);
        samples.push(now);
        if (samples.length > limit) samples.shift();
      }
      const simultaneous = playing?.signal && now - playing.start < 300;
      const entry = registry.get(event).find(choice =>
        (playing?.entry.priority ?? -Infinity) <= choice.priority &&
        !(simultaneous && signal?.source === "rail" && playing.signal.source === "frame" && playing.entry.priority === choice.priority) &&
        (now - (starts.get(choice.id) ?? -Infinity) >= choice.cooldown ||
          simultaneous && signal?.source === "frame" && playing.signal.source === "rail" && playing.entry === choice) &&
        matches(choice.when, samples, now, state));
      if (!entry) return;
      if (playing?.entry === entry && event === "mail.recv") {
        ++playing.count;
        return;
      }
      starts.set(entry.id, now);
      playing = { entry, index: 0, start: now, beat: entry.beats[0], count: 1, signal };
    }

    function tick(now, dragging) {
      while (playing) {
        const { beat, entry, start } = playing;
        if (beat.stage === "pendulum-drag" && dragging || now - start < beat.ms) return playing;
        if (++playing.index >= entry.beats.length) playing = null;
        else {
          playing.start += beat.ms;
          playing.beat = entry.beats[playing.index];
        }
      }
      return null;
    }

    return {
      fire, tick,
      preferFrame(signal) {
        if (playing?.signal?.token === signal.token) playing.signal = { ...playing.signal, ...signal };
      },
      endReply() { if (playing?.entry.id === "working_tatata") playing = null; },
      clear() { playing = null; },
      reset() { playing = null; history.clear(); starts.clear(); }
    };
  }

  const stageEffects = {
    stay: { keyframes: [{ p: 0 }] },
    "hop-bounce": { keyframes: [
      { p: 0, ease: "easeIn" }, { p: 0.28, lift: 0.035, sx: 1.09, sy: 0.88, ease: "easeOut" },
      { p: 0.53, lift: -0.25, sx: 0.96, sy: 1.07, ease: "easeIn" },
      { p: 0.82, lift: 0.018, sx: 1.08, sy: 0.93, ease: "overshoot" }, { p: 1 }
    ] },
    "double-hop": { keyframes: [
      { p: 0 }, { p: 0.15, lift: 0.03, sy: 0.86, ease: "easeOut" },
      { p: 0.33, lift: -0.3, sy: 1.04, ease: "easeIn" },
      { p: 0.52, lift: 0.024, sy: 0.91, ease: "easeOut" },
      { p: 0.69, lift: -0.18, sy: 1.03, ease: "easeIn" },
      { p: 0.88, lift: 0.012, sy: 0.95, ease: "overshoot" }, { p: 1 }
    ] },
    "squash-pop": { keyframes: [
      { p: 0 }, { p: 0.18, sy: 0.8, sx: 1.14, ease: "overshoot" },
      { p: 0.42, sy: 1.12, sx: 0.93, ease: "easeOut" },
      { p: 0.72, sy: 0.97, sx: 1.025 }, { p: 1 }
    ] },
    "anticipate-launch": { keyframes: [
      { p: 0, ease: "easeIn" }, { p: 0.24, lift: 0.035, sy: 0.9, ease: "easeOut" },
      { p: 0.36, lift: -0.1, sy: 1.07, ease: "easeOut" }, { p: 0.55, lift: 0.012, sy: 0.98 },
      { p: 0.72 }, { p: 1 }
    ] },
    "settle-sigh": { keyframes: [
      { p: 0, ease: "easeOut" }, { p: 0.36, lift: -0.04, sy: 1.08, ease: "easeInOut" },
      { p: 0.76, lift: 0.018, sx: 1.025, sy: 0.94, ease: "easeOut" }, { p: 1 }
    ] }
  };

  const propMotions = {
    attach: { keyframes: [{ p: 0 }] },
    orbit: { keyframes: [
      { p: 0, offsetX: 0.5, ease: "easeInOut" }, { p: 0.25, offsetY: 0.24, ease: "easeInOut" },
      { p: 0.5, offsetX: -0.5, ease: "easeInOut" }, { p: 0.75, offsetY: -0.24, ease: "easeInOut" },
      { p: 1, offsetX: 0.5 }
    ] },
    "hand-liftoff": { keyframes: [
      { p: 0, along: 0 }, { p: 0.21, along: 0, rotDeg: -8, ease: "easeOut" },
      { p: 0.42, along: 0.32, arcY: -0.42, rotDeg: 8, ease: "easeInOut" },
      { p: 0.73, along: 0.8, arcY: -0.62, rotDeg: -5, ease: "overshoot" },
      { p: 0.94, along: 1.012, arcY: -0.035 }, { p: 1 }
    ] },
    "arc-to-landmark": { keyframes: [
      { p: 0, along: 0, ease: "easeOut" }, { p: 0.52, along: 0.52, arcY: -0.75, ease: "easeIn" }, { p: 1 }
    ] },
    "bonk-from-edge": { edge: "away", recoil: { start: 0.425, peak: 0.49, end: 0.73, lift: -0.045, angle: 0.22 }, keyframes: [
      { p: 0, along: 0, rotDeg: -18, ease: "easeIn" },
      { p: 0.425, rotDeg: 5, ease: "easeOut" },
      { p: 0.63, offsetX: -0.35, arcY: -0.24, rotDeg: -26, ease: "easeIn" },
      { p: 0.93, offsetX: -0.7, arcY: 0.45, rotDeg: -52, alpha: 0 }, { p: 1, alpha: 0 }
    ] },
    burst: { keyframes: [
      { p: 0, scale: 0.65, ease: "easeOut" }, { p: 0.3, offsetX: 0.08, arcY: -0.5, scale: 1.1 },
      { p: 1, offsetX: 0.2, arcY: -1, alpha: 0 }
    ] }
  };

  function timing(manifest = {}) {
    const drag = C.record(manifest.physics) && C.record(manifest.physics.drag) ? manifest.physics.drag : {};
    return {
      passive: passiveBeats(manifest.passive),
      stage: C.compileCurves(stageEffects, manifest.stageEffects, manifest.easings),
      prop: C.compileCurves(propMotions, manifest.propMotions, manifest.easings, true),
      drag: {
        stiffness: C.bounded(drag.stiffness, 5, 300, 42),
        damping: C.bounded(drag.damping, 0.5, 40, 7.5),
        impulse: C.bounded(drag.impulse, 0, 0.5, 0.07),
        maxSwing: C.bounded(drag.maxDeg, 0, 45, 18) * C.RAD,
        bodyFallbackScale: C.bounded(drag.bodyFallbackScale, 0, 1, 0.65)
      }
    };
  }

  Object.assign(C, { scenarioRunner, timing });
})(window.WsCompanion);
