(function (C) {
  "use strict";
  // Procedural ws-companion-clip/v1 evaluator and crossfaded clip playback runtime.
  const DEG = Math.PI / 180, CROSSFADE_S = 0.22;
  const SENTINEL_KEYS = Object.freeze(["idle", "laptop_work", "phone_hold", "phone_typing", "phone_putdown", "knock_startle", "wakeup_jolt"]);
  const SENTINEL_LOOP_DEFAULT = Object.freeze({ idle: true, laptop_work: true, phone_hold: false, phone_typing: true, phone_putdown: false, knock_startle: false, wakeup_jolt: false });
  const clamp = (v, lo, hi) => (v < lo ? lo : v > hi ? hi : v);
  const lerp = (a, b, t) => a + (b - a) * t;
  const fin = (v, fb) => (Number.isFinite(v) ? v : fb);

  function resolveEntry(pack, key, opts) {
    if (!pack || !key) return null;
    if (pack.sentinels && Object.hasOwn(pack.sentinels, key)) {
      const e = pack.sentinels[key];
      return { ...e, key, source: "sentinels", loop: typeof e.loop === "boolean" ? e.loop : SENTINEL_LOOP_DEFAULT[key] };
    }
    if (opts?.isSentinel) return null;
    const am = pack.actionMap || (Array.isArray(pack.actions) ? Object.fromEntries(pack.actions.map(a => [a.key, a])) : pack.actions);
    if (am && Object.hasOwn(am, key)) return { ...am[key], key, source: "actions" };
    if (pack.animations && Object.hasOwn(pack.animations, key)) return { key, kind: "vrma", file: pack.animations[key], source: "animations", loop: false };
    return null;
  }

  function applyEase(u, ease) {
    const t = clamp(u, 0, 1), e = typeof ease === "string" ? ease.trim().toLowerCase().replace(/-/g, "_") : "";
    if (!e || e === "ease_in_out") return t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;
    if (e === "ease_in") return t * t * t;
    if (e === "ease_out") { const r = 1 - t; return 1 - r * r * r; }
    if (e === "ease_out_back") { const c = 1.70158; return 1 + (c + 1) * Math.pow(t - 1, 3) + c * Math.pow(t - 1, 2); }
    if (e === "spring") return 1 - Math.cos(t * Math.PI * 4.5) * Math.exp(-t * 6);
    return t; // D9: linear & unknown easing values
  }

  function lerpVec3(a, b, t) {
    if (!a && !b) return null;
    const va = a || b, vb = b || a;
    return [lerp(va[0], vb[0], t), lerp(va[1], vb[1], t), lerp(va[2], vb[2], t)];
  }

  function lerpHead(a, b, t) {
    if (!a && !b) return null;
    const f = k => lerp(fin(a?.[k], 0), fin(b?.[k], 0), t);
    return { yaw_deg: f("yaw_deg"), pitch_deg: f("pitch_deg"), roll_deg: f("roll_deg"), lean_deg: f("lean_deg"), turn_deg: f("turn_deg") };
  }

  function lerpFingers(a, b, t) {
    if ((!a && !b) || !C.vrmIk) return null;
    const fa = C.vrmIk.resolveFingerSpec(a || b), fb = C.vrmIk.resolveFingerSpec(b || a);
    const curl = fa.curl.map((v, i) => lerp(v, fb.curl[i], t));
    return { curl, thumb_opp: lerp(fa.thumb_opp, fb.thumb_opp, t), spread: lerp(fa.spread, fb.spread, t) };
  }

  function lerpHand(a, b, t) {
    if (!a && !b) return null;
    return {
      ik: lerpVec3(Array.isArray(a?.ik) ? a.ik : null, Array.isArray(b?.ik) ? b.ik : null, t),
      pole: lerpVec3(Array.isArray(a?.pole) ? a.pole : null, Array.isArray(b?.pole) ? b.pole : null, t),
      fingers: lerpFingers(a?.fingers, b?.fingers, t)
    };
  }

  function lerpExpression(a, b, t) {
    if (!a && !b) return null;
    const out = Object.create(null);
    for (const k of new Set([...Object.keys(a || {}), ...Object.keys(b || {})])) {
      const v = clamp(lerp(fin(a?.[k], 0), fin(b?.[k], 0), t), 0, 1);
      if (v > 0.001) out[k] = v;
    }
    return Object.keys(out).length ? out : null;
  }

  function lerpTargetAttach(a, b, t) {
    if (!a && !b) return null;
    const out = {};
    let any = false;
    for (const slot of ["gaze", "head", "hand_l", "hand_r"]) {
      const sa = a?.[slot] || null, sb = b?.[slot] || null;
      const val = sa && sb && sa.space === sb.space
        ? { space: sa.space, offset: lerpVec3(Array.isArray(sa.offset) ? sa.offset : [0, 0, 0], Array.isArray(sb.offset) ? sb.offset : [0, 0, 0], t), weight: clamp(lerp(fin(sa.weight, 1), fin(sb.weight, 1), t), 0, 1) }
        : (t < 0.5 ? sa : sb);
      if (val && val.space && val.space !== "free") { out[slot] = val; any = true; }
    }
    return any ? out : null;
  }

  function sampleClip(clip, timeSec, opts) {
    if (!clip || !Array.isArray(clip.keyframes) || !clip.keyframes.length) return null;
    const clipDur = clamp(fin(clip.duration_s, 1), 0.1, 30);
    const duration = clamp(fin(opts?.duration_s, clipDur), 0.1, 30);
    const loop = opts?.loop !== undefined ? !!opts.loop : !!clip.loop;
    const rawT = fin(timeSec, 0), scaledT = rawT * (clipDur / duration);
    const t = loop ? (((scaledT % clipDur) + clipDur) % clipDur) : clamp(scaledT, 0, clipDur);
    const kfs = clip.keyframes, clipEase = clip.easing || clip.ease || "";
    let k0 = kfs[0], k1 = kfs[kfs.length - 1], segT = 0;
    if (kfs.length === 1 || t <= kfs[0].t) {
      k0 = k1 = kfs[0];
    } else if (t >= kfs[kfs.length - 1].t) {
      if (loop && clipDur > kfs[kfs.length - 1].t + 1e-4) {
        k0 = kfs[kfs.length - 1]; k1 = kfs[0];
        segT = applyEase((t - k0.t) / Math.max(1e-4, clipDur - k0.t + k1.t), k0.easing || k0.ease || clipEase);
      } else k0 = k1 = kfs[kfs.length - 1];
    } else {
      for (let i = 0; i < kfs.length - 1; i++) {
        if (t >= kfs[i].t && t <= kfs[i + 1].t) {
          k0 = kfs[i]; k1 = kfs[i + 1];
          segT = applyEase((t - k0.t) / Math.max(1e-4, k1.t - k0.t), k0.easing || k0.ease || clipEase);
          break;
        }
      }
    }
    return {
      time: t, duration, done: !loop && rawT >= duration,
      head: lerpHead(k0.head, k1.head, segT),
      hands: { left: lerpHand(k0.hands?.left, k1.hands?.left, segT), right: lerpHand(k0.hands?.right, k1.hands?.right, segT) },
      expression: lerpExpression(k0.expression, k1.expression, segT),
      target_attach: lerpTargetAttach(k0.target_attach, k1.target_attach, segT)
    };
  }

  function blendEuler(targets, bone, euler, w) {
    const c = targets[bone] || [0, 0, 0];
    targets[bone] = [lerp(c[0], euler[0], w), lerp(c[1], euler[1], w), lerp(c[2], euler[2], w)];
  }

  function applySampleToTargets(sample, baseTargets, blendWeight, rigCtx) {
    const w = clamp(fin(blendWeight, 1), 0, 1);
    if (!sample || w <= 0.001) return null;
    const ik = C.vrmIk, out = { eyeYaw: null, eyePitch: null, expressionTargets: null };
    let headYaw = (sample.head?.yaw_deg || 0) * DEG, headPitch = (sample.head?.pitch_deg || 0) * DEG;
    const headRoll = (sample.head?.roll_deg || 0) * DEG, lean = (sample.head?.lean_deg || 0) * DEG, turn = (sample.head?.turn_deg || 0) * DEG;
    const headAttach = ik?.resolveTargetAttach?.(sample.target_attach?.head, rigCtx);
    if (headAttach && rigCtx?.headPos) {
      const ang = ik.worldTargetToHeadAngles(rigCtx.headPos, headAttach);
      headYaw = lerp(headYaw, ang.yaw, headAttach.weight);
      headPitch = lerp(headPitch, ang.pitch, headAttach.weight);
    }
    if (sample.head || headAttach) {
      blendEuler(baseTargets, "spine", [lean * 0.45, turn * 0.4, 0], w);
      blendEuler(baseTargets, "chest", [lean * 0.35, turn * 0.35, 0], w);
      blendEuler(baseTargets, "upperChest", [lean * 0.2, turn * 0.25, 0], w);
      blendEuler(baseTargets, "neck", [headPitch * 0.38, headYaw * 0.36, headRoll * 0.35], w);
      blendEuler(baseTargets, "head", [headPitch * 0.62, headYaw * 0.64, headRoll * 0.65], w);
    }
    const gazeAttach = ik?.resolveTargetAttach?.(sample.target_attach?.gaze, rigCtx);
    if (gazeAttach && rigCtx?.headPos) {
      const ang = ik.worldTargetToHeadAngles(rigCtx.headPos, gazeAttach);
      out.eyeYaw = ang.yaw * gazeAttach.weight;
      out.eyePitch = -ang.pitch * gazeAttach.weight;
    }
    for (const side of ["left", "right"]) {
      const hand = sample.hands?.[side];
      const attach = ik?.resolveTargetAttach?.(sample.target_attach?.[side === "left" ? "hand_l" : "hand_r"], rigCtx);
      if ((hand?.ik || attach) && ik) {
        const ch = rigCtx?.chains?.[side] || ik.measureArmChain(rigCtx?.vrm, side, rigCtx?.THREE);
        let loc = hand?.ik ? hand.ik.slice(0, 3) : null;
        if (attach) {
          const al = ik.worldToChestLocal([attach.x, attach.y, attach.z], ch);
          loc = loc ? lerpVec3(loc, al, attach.weight) : al;
        }
        if (loc) {
          const solved = ik.solveTwoBoneArmIK({
            side, shoulder: ch.shoulderOffset, pole: hand?.pole || null, upperLen: ch.upperLen, lowerLen: ch.lowerLen,
            target: [loc[0] * ch.chainLen, loc[1] * ch.chainLen, loc[2] * ch.chainLen]
          });
          blendEuler(baseTargets, side + "UpperArm", solved.upperEuler, w);
          blendEuler(baseTargets, side + "LowerArm", solved.lowerEuler, w);
        }
      }
      if (hand?.fingers && ik) {
        const scratch = Object.create(null);
        ik.writeClipHandFingers(side, hand.fingers, scratch);
        for (const [b, e] of Object.entries(scratch)) blendEuler(baseTargets, b, e, w);
      }
    }
    if (sample.expression) {
      const scaled = Object.create(null);
      for (const [k, v] of Object.entries(sample.expression)) scaled[k] = v * w;
      out.expressionTargets = scaled;
    }
    return out;
  }

  function createClipPlayer() {
    let active = null;
    function play(key, clip, opts) {
      if (!clip || !Array.isArray(clip.keyframes) || !clip.keyframes.length) return false;
      const loop = opts?.loop !== undefined ? !!opts.loop : !!clip.loop;
      if (active && active.key === key && !active.fadingOut && active.clip === clip) return true;
      active = {
        key: String(key || ""), clip, loop, isSentinel: !!opts?.isSentinel, holdLast: !!opts?.holdLast, holding: false,
        duration_s: Number.isFinite(opts?.duration_s) ? opts.duration_s : undefined,
        time: fin(opts?.startTime, 0), weight: opts?.immediate ? 1 : (active ? Math.min(active.weight, 0.25) : 0), fadingOut: false
      };
      return true;
    }
    function stop(imm) { if (!active) return; if (imm) active = null; else active.fadingOut = true; }
    function step(dt) {
      if (!active) return null;
      const safeDt = Number.isFinite(dt) && dt > 0 ? Math.min(dt, 0.2) : 0.016, dW = safeDt / CROSSFADE_S;
      if (active.fadingOut) {
        active.weight = Math.max(0, active.weight - dW);
        if (active.weight <= 0.001) { active = null; return null; }
      } else active.weight = Math.min(1, active.weight + dW);
      active.time += safeDt;
      const sample = sampleClip(active.clip, active.time, { loop: active.loop, duration_s: active.duration_s });
      if (!sample) { active = null; return null; }
      if (sample.done && !active.fadingOut) { if (active.holdLast) active.holding = true; else active.fadingOut = true; }
      return { key: active.key, sample, weight: active.weight, isSentinel: active.isSentinel, loop: active.loop, holding: active.holding };
    }
    return {
      play, stop, step,
      isOneShotActive: () => !!(active && !active.loop && !active.holding && !active.fadingOut),
      getState: () => active ? { key: active.key, loop: active.loop, isSentinel: active.isSentinel, holding: active.holding, time: +active.time.toFixed(3), duration: active.duration_s || active.clip.duration_s || 1, weight: +active.weight.toFixed(3), fadingOut: active.fadingOut } : null
    };
  }

  function createPackClipRuntime(pack, signal) {
    const player = createClipPlayer(), clipCache = new Map(), clipLoading = new Set(), clipFailed = new Set(), durations = Object.create(null);
    let wantedKey = "", wantedIsSentinel = false;
    if (pack?.sentinels) for (const [k, e] of Object.entries(pack.sentinels)) if (Number.isFinite(e?.duration_s)) durations[k] = e.duration_s;
    function ensureClipJson(entry, onDuration) {
      if (!entry || entry.kind !== "clip" || typeof C.fetchPackClip !== "function") return null;
      if (clipCache.has(entry.file)) return clipCache.get(entry.file);
      if (clipLoading.has(entry.file) || clipFailed.has(entry.file)) return null;
      clipLoading.add(entry.file);
      C.fetchPackClip(pack, entry.file, signal).then(res => {
        if (res?.json && Array.isArray(res.json.keyframes)) {
          clipCache.set(entry.file, res.json);
          if (Number.isFinite(res.json.duration_s) && !Number.isFinite(durations[entry.key])) {
            durations[entry.key] = res.json.duration_s;
            onDuration?.(durations);
          }
        } else clipFailed.add(entry.file);
      }).catch(() => { if (!signal?.aborted) clipFailed.add(entry.file); }).finally(() => clipLoading.delete(entry.file));
      return null;
    }
    function sync(actionKey, sentinelKey, onDuration) {
      const aEntry = actionKey ? resolveEntry(pack, actionKey, { isSentinel: false }) : null;
      const entry = aEntry || (sentinelKey ? resolveEntry(pack, sentinelKey, { isSentinel: true }) : null);
      const isSentinel = !!entry && entry.source === "sentinels";
      if (!entry) { wantedKey = ""; wantedIsSentinel = false; player.stop(false); return null; }
      wantedKey = entry.key; wantedIsSentinel = isSentinel;
      if (entry.kind === "vrma") { player.stop(false); return { vrmaEntry: entry, isSentinel }; }
      const clip = ensureClipJson(entry, onDuration);
      if (clip) {
        const effLoop = typeof entry.loop === "boolean" ? entry.loop : (typeof clip.loop === "boolean" ? clip.loop : !!SENTINEL_LOOP_DEFAULT[entry.key]);
        player.play(entry.key, clip, { loop: effLoop, duration_s: entry.duration_s, isSentinel, holdLast: isSentinel && entry.key === "phone_hold" && !effLoop });
      }
      return { clipEntry: entry, isSentinel };
    }
    return {
      sync, step: dt => player.step(dt), stop: imm => player.stop(imm),
      isOneShotActive: () => player.isOneShotActive(), getDurations: () => durations,
      getActiveSentinel: () => (wantedIsSentinel ? wantedKey : ""), getState: () => player.getState(),
      dispose() { player.stop(true); clipCache.clear(); clipLoading.clear(); clipFailed.clear(); }
    };
  }

  Object.assign(C, { vrmClip: { SENTINEL_KEYS, SENTINEL_LOOP_DEFAULT, CROSSFADE_S, resolveEntry, sampleClip, applySampleToTargets, createClipPlayer, createPackClipRuntime } });
})(window.WsCompanion);
