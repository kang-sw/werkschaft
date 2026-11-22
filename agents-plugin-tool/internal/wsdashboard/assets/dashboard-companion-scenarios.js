(function (C) {
  "use strict";
  // The built-in scenarios and effect badges (a pack's fx registry may omit
  // them) come from dashboard-companion-builtins.js, which the daemon's agent
  // director reads too.
  const { scenarios: starters, fx: fxDefaults } = window.WsCompanionBuiltins;

  // A directed beat outranks every rules scenario except the drag dangle, so
  // the character still hangs from the pointer while it is dragged.
  const directedPriority = 99;
  const directedTTL = { min: 1000, max: 15000 };
  const quipLimit = 140;

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
        if (playing.until !== undefined) {
          // A directed beat runs its steps, holds the last one until the TTL
          // ends, then hands the stage back to the rules engine.
          if (now >= playing.until) { playing = null; break; }
          if (beat.stage === "pendulum-drag" && dragging || now - start < beat.ms ||
              playing.index >= entry.beats.length - 1) return playing;
          playing.start += beat.ms;
          playing.beat = entry.beats[++playing.index];
          continue;
        }
        if (beat.stage === "pendulum-drag" && dragging || now - start < beat.ms) return playing;
        if (++playing.index >= entry.beats.length) playing = null;
        else {
          playing.start += beat.ms;
          playing.beat = entry.beats[playing.index];
        }
      }
      return null;
    }

    // direct plays beat (one beat or a scenario's steps) over the rules
    // engine until ttlMs (clamped to 1-15 s) ends. A newer call replaces it.
    function direct(beat, now, ttlMs, id = "directed") {
      const beats = (Array.isArray(beat) ? beat : [beat]).filter(C.record);
      if (!beats.length) return;
      const ttl = C.clamp(Number.isFinite(ttlMs) ? ttlMs : 0, directedTTL.min, directedTTL.max);
      const entry = { id, priority: directedPriority, cooldown: 0, beats };
      playing = { entry, index: 0, start: now, beat: beats[0], count: 1, until: now + ttl };
    }

    return {
      fire, tick, direct,
      scenario(id) {
        for (const list of registry.values()) {
          const entry = list.find(choice => choice.id === id);
          if (entry) return entry;
        }
        return null;
      },
      preferFrame(signal) {
        if (playing?.signal?.token === signal.token) playing.signal = { ...playing.signal, ...signal };
      },
      endReply() { if (playing?.entry.id === "working_tatata" && playing.until === undefined) playing = null; },
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

  // commandId returns id when a registry holds it, applying that registry's
  // fallback chain, or when it is a built-in; otherwise "" (the field drops).
  function commandId(id, registries, builtins) {
    if (!C.timingId(id)) return "";
    for (const registry of registries) {
      if (!C.record(registry) || !Object.hasOwn(registry, id)) continue;
      const found = C.resolveEntry(registry, id, null);
      if (found) return found.id;
    }
    return builtins && Object.hasOwn(builtins, id) ? id : "";
  }

  function commandQuip(text) {
    if (typeof text !== "string") return "";
    return Array.from(text.replace(/[\u0000-\u001f\u007f-\u009f]+/g, " ").trim()).slice(0, quipLimit).join("").trim();
  }

  // directedCommand validates a companion-command payload against the active
  // pack (and the shared default pack for props and fx), the built-in fx
  // badges, and the runner's scenarios. An unknown ID drops only its field;
  // a command left with nothing to show is null. The quip is plain text of at
  // most 140 code points.
  function directedCommand(command, pack, shared, runner) {
    if (!C.record(command)) return null;
    const manifest = pack?.manifest || {}, sharedManifest = shared?.manifest || {};
    const patch = {};
    const fields = [
      ["expression", [manifest.expressions]],
      ["gesture", [manifest.gestures]],
      ["prop", [manifest.props, sharedManifest.props]],
      ["fx", [manifest.fx, sharedManifest.fx], fxDefaults]
    ];
    for (const [field, registries, builtins] of fields) {
      const id = commandId(command[field], registries, builtins);
      if (id) patch[field] = id;
    }
    const quip = commandQuip(command.quip);
    if (quip) patch.quip = quip;
    const scenario = C.timingId(command.scenario) ? runner.scenario(command.scenario) : null;
    if (!scenario && !Object.keys(patch).length) return null;
    const ttl = C.clamp(Number.isFinite(command.ttl_ms) ? command.ttl_ms : 0, directedTTL.min, directedTTL.max);
    const steps = scenario ? scenario.beats : [{ ...passive.idle, ms: ttl }];
    return { id: scenario?.id || "directed", ttl, beats: steps.map(step => ({ ...step, ...patch })) };
  }

  Object.assign(C, { scenarioRunner, timing, directedCommand, fxDefaults });
})(window.WsCompanion);
