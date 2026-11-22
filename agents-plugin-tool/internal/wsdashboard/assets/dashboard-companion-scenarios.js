(function (C) {
  "use strict";
  const starters = {
    idle_roam: {
      on: ["session.idle", "idle.long"], priority: 5, cooldownMs: 5000, durationMs: 1250,
      pose: "stand", expression: "idle", gaze: "cursor", stage: "hop-bounce"
    },
    perched_watch: {
      on: "user.typing", priority: 10, durationMs: 2500,
      pose: "perched", expression: "idle", gaze: "caret"
    },
    working_tatata: {
      on: "session.streaming", priority: 10, durationMs: 1800,
      pose: "typing", expression: "focused", gaze: "scan"
    },
    thinking_ponder: {
      on: "session.thinking", priority: 15, durationMs: 1700,
      pose: "stand", gesture: "coffee", expression: "half_lid", gaze: "up", fx: "ponder"
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
      pose: "dangle", expression: "oo", stage: "pendulum-drag", prop: "grab"
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
      on: "subagent.spawn", priority: 55, cooldownMs: 500,
      steps: [
        { ms: 1050, pose: "stand", expression: "oo", stage: "squash-pop", gaze: "prop:drone",
          prop: { id: "drone", action: "launch", to: "hud-roster" } },
        { ms: 550, pose: "stand", expression: "idle", gaze: "hud-roster" }
      ]
    },
    subagent_return: {
      on: "subagent.done", priority: 56, cooldownMs: 500, durationMs: 1250,
      pose: "stand", expression: "happy", gaze: "prop:drone",
      prop: { id: "drone", action: "return", from: "hud-roster" }
    },
    mailbox_bonk: {
      on: "mail.recv", priority: 57, durationMs: 1250,
      pose: "stand", expression: "oo", gaze: "prop:mail", prop: "mail"
    }
  };

  const passive = {
    idle: { pose: "stand", expression: "idle", gaze: "cursor" },
    doze: { pose: "perched", expression: "idle_doze", gaze: "cursor" },
    typing_user: starters.perched_watch,
    streaming: starters.working_tatata,
    thinking: { pose: "stand", expression: "half_lid", gaze: "up" },
    permission: { pose: "stand", expression: "oo", gaze: "whiteboard-ask" },
    error: { pose: "stand", expression: "cry", gaze: "down" }
  };

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

    function fire(event, now, state) {
      if (!registry.has(event)) return;
      let samples = history.get(event);
      const limit = streakLimit.get(event);
      if (limit) {
        if (!samples) history.set(event, samples = []);
        samples.push(now);
        if (samples.length > limit) samples.shift();
      }
      const entry = registry.get(event).find(choice =>
        (playing?.entry.priority ?? -Infinity) <= choice.priority &&
        now - (starts.get(choice.id) ?? -Infinity) >= choice.cooldown &&
        matches(choice.when, samples, now, state));
      if (!entry) return;
      if (playing?.entry === entry && event === "mail.recv") {
        ++playing.count;
        return;
      }
      starts.set(entry.id, now);
      playing = { entry, index: 0, start: now, beat: entry.beats[0], count: 1 };
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
      clear() { playing = null; },
      reset() { playing = null; history.clear(); starts.clear(); }
    };
  }

  const stageEffects = {
    stay: (p, t, size, state) => { state.lift = state.angle = 0; state.sx = state.sy = 1; },
    "hop-bounce": (p, t, size, state) => { state.lift = -4 * p * (1 - p) * size * 0.24; },
    "double-hop": (p, t, size, state) => {
      const second = (2 * p) % 1;
      state.lift = -4 * second * (1 - second) * size * (p < 0.5 ? 0.25 : 0.16);
    },
    "pendulum-drag": (p, t, size, state, swing) => { state.angle = swing; },
    "squash-pop": (p, t, size, state) => {
      const wave = Math.sin(p * Math.PI * 3) * (1 - p) * 0.2;
      state.sx = 1 + wave;
      state.sy = 1 - wave;
    }
  };

  const propMotion = {
    attach: (p, t, from, to, size, xy) => { xy.x = to.x; xy.y = to.y; },
    orbit: (p, t, from, to, size, xy) => {
      xy.x = to.x + Math.cos(t * 4) * size * 0.5;
      xy.y = to.y + Math.sin(t * 4) * size * 0.24;
    },
    "arc-to-landmark": (p, t, from, to, size, xy) => {
      xy.x = C.mix(from.x, to.x, p);
      xy.y = C.mix(from.y, to.y, p) - 4 * p * (1 - p) * size * 0.75;
    },
    "bonk-from-edge": (p, t, from, to, size, xy) => {
      const q = C.clamp((p - 0.28) / 0.44, 0, 1);
      xy.x = p < 0.27 ? C.mix(from.x, to.x, p / 0.27) : to.x - size * 0.7 * q;
      xy.y = p < 0.27 ? C.mix(from.y, to.y, p / 0.27) : to.y + size * q * q;
    },
    burst: (p, t, from, to, size, xy) => {
      xy.x = to.x + size * p * 0.2;
      xy.y = to.y - size * p;
    }
  };

  Object.assign(C, { scenarioRunner, passive, stageEffects, propMotion });
})(window.WsCompanion);
