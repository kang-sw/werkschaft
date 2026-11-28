(function (C) {
  "use strict";

  // ============================================================================
  // Co-Lead Pure-FK Pose Target Calculator & Math Primitives
  // ============================================================================
  // Pure procedural FK (zero 2-bone IK) with frame-rate-independent exponential
  // damping (1 - Math.exp(-k * dt)). Every pose is authored in VRM 1.0
  // normalized humanoid coordinates (+X character-left, +Y up, +Z forward,
  // Euler XYZ) and conjugated for VRM 0.x by dashboard-companion-vrm.js.
  //
  // Extension hooks:
  //   - params.fingerPreset (force one finger curl preset on both hands)
  //   - params.fingerOut (caller-owned scratch map; the 30 finger target arrays are reused across frames)
  //   - params.breakoutPose (Phase 3: bleed/roam lean & walk overrides)

  const DEG = Math.PI / 180;

  // 30 VRM humanoid finger bones (thumb Metacarpal/Proximal/Distal plus
  // Index/Middle/Ring/Little Proximal/Intermediate/Distal on both hands).
  const FINGER_DIGITS = Object.freeze(["Thumb", "Index", "Middle", "Ring", "Little"]);
  const FINGER_SEGMENTS = Object.freeze({
    Thumb: Object.freeze(["Metacarpal", "Proximal", "Distal"]),
    Index: Object.freeze(["Proximal", "Intermediate", "Distal"]),
    Middle: Object.freeze(["Proximal", "Intermediate", "Distal"]),
    Ring: Object.freeze(["Proximal", "Intermediate", "Distal"]),
    Little: Object.freeze(["Proximal", "Intermediate", "Distal"])
  });
  const FINGER_BONES = Object.freeze(["left", "right"].flatMap(side =>
    FINGER_DIGITS.flatMap(d => FINGER_SEGMENTS[d].map(seg => side + d + seg))
  ));

  // Per-digit segment curl (radians, base -> tip). Thumb adds an opposition swing
  // on its metacarpal; spread fans the proximal joints apart in the palm plane.
  const FINGER_PRESETS = Object.freeze({
    relaxed: Object.freeze({
      thumb: Object.freeze([0.16, 0.18, 0.14]), thumbOpp: 0.20,
      index: Object.freeze([0.20, 0.24, 0.14]), middle: Object.freeze([0.28, 0.32, 0.18]),
      ring: Object.freeze([0.34, 0.38, 0.22]), little: Object.freeze([0.40, 0.42, 0.26]), spread: 0.05
    }),
    typing: Object.freeze({
      thumb: Object.freeze([0.12, 0.20, 0.16]), thumbOpp: 0.30,
      index: Object.freeze([0.36, 0.50, 0.28]), middle: Object.freeze([0.40, 0.54, 0.30]),
      ring: Object.freeze([0.44, 0.56, 0.32]), little: Object.freeze([0.48, 0.58, 0.34]), spread: 0.07
    }),
    "phone-grip": Object.freeze({
      thumb: Object.freeze([0.26, 0.30, 0.24]), thumbOpp: 0.58,
      index: Object.freeze([0.86, 0.74, 0.40]), middle: Object.freeze([0.96, 0.84, 0.44]),
      ring: Object.freeze([1.02, 0.88, 0.48]), little: Object.freeze([1.08, 0.92, 0.50]), spread: 0.02
    }),
    point: Object.freeze({
      thumb: Object.freeze([0.46, 0.58, 0.44]), thumbOpp: 0.52,
      index: Object.freeze([0.04, 0.04, 0.02]), middle: Object.freeze([1.20, 1.26, 0.68]),
      ring: Object.freeze([1.26, 1.32, 0.70]), little: Object.freeze([1.30, 1.36, 0.72]), spread: 0.0
    })
  });
  const FINGER_SPREAD_SIGN = Object.freeze({ index: -1, middle: 0, ring: 0.5, little: 1 });

  function resolveFingerPreset(name) {
    return Object.hasOwn(FINGER_PRESETS, name) ? FINGER_PRESETS[name] : null;
  }

  // Blend relaxed -> typing -> phone-grip -> point for one hand and write the
  // 15 VRM 1.0 Euler targets into out (reusing existing arrays when present).
  // Left curls rotate -Z; the right hand mirrors across the YZ plane.
  function writeHandFingerTargets(side, blend, out) {
    const rel = FINGER_PRESETS.relaxed, typ = FINGER_PRESETS.typing;
    const grip = FINGER_PRESETS["phone-grip"], pt = FINGER_PRESETS.point;
    const wT = clamp(blend?.typing || 0, 0, 1);
    const wG = clamp(blend?.grip || 0, 0, 1);
    const wP = clamp(blend?.point || 0, 0, 1);
    const tap = Number.isFinite(blend?.tap) ? blend.tap : 0;
    const forced = blend?.preset ? resolveFingerPreset(blend.preset) : null;
    const mirror = side === "right" ? -1 : 1;
    const mix = (key, i) => {
      if (forced) return i < 0 ? forced[key] : forced[key][i];
      const a = i < 0 ? rel[key] : rel[key][i];
      const b = i < 0 ? typ[key] : typ[key][i];
      const c = i < 0 ? grip[key] : grip[key][i];
      const d = i < 0 ? pt[key] : pt[key][i];
      return lerp(lerp(lerp(a, b, wT), c, wG), d, wP);
    };
    const put = (name, x, y, z) => {
      const arr = out[name];
      if (Array.isArray(arr) && arr.length >= 3) { arr[0] = x; arr[1] = y; arr[2] = z; }
      else out[name] = [x, y, z];
    };
    const spread = mix("spread", -1);
    const opp = mix("thumbOpp", -1);
    for (let i = 0; i < 3; i++) {
      const c = mix("thumb", i);
      put(side + "Thumb" + FINGER_SEGMENTS.Thumb[i], 0.7 * c, mirror * (i === 0 ? opp : 0), -mirror * 0.7 * c);
    }
    for (const key of ["index", "middle", "ring", "little"]) {
      const digit = key[0].toUpperCase() + key.slice(1);
      const tapCurl = (key === "index" || key === "middle") && !forced ? tap * 0.10 * wT : 0;
      for (let i = 0; i < 3; i++) {
        const c = mix(key, i) + (i < 2 ? tapCurl : 0);
        const y = i === 0 ? spread * FINGER_SPREAD_SIGN[key] : 0;
        put(side + digit + FINGER_SEGMENTS[digit][i], 0, mirror * y, -mirror * c);
      }
    }
    return out;
  }

  // Compute all 30 finger Euler targets. weights = { left: blend, right: blend }
  // where blend = { typing, grip, point, tap, preset }.
  function computeFingerTargets(weights, out) {
    const target = out && typeof out === "object" ? out : {};
    writeHandFingerTargets("left", weights?.left || null, target);
    writeHandFingerTargets("right", weights?.right || null, target);
    return target;
  }

  // World-space orthonormal palm frame from wrist + index/middle/little proximal
  // joint positions ({x, y, z}). palmY runs wrist -> middle knuckle, palmZ is the
  // palm-surface normal (out of the palm), palmX = palmY x palmZ (right-handed).
  // Inputs are world positions, so the frame is identical on VRM 0.x (after
  // VRMUtils.rotateVRM0) and VRM 1.0. Returns false on degenerate input.
  function computePalmFrame(wrist, index, middle, little, side, out) {
    if (!wrist || !index || !middle || !little || !out) return false;
    for (const p of [wrist, index, middle, little]) {
      if (!Number.isFinite(p.x) || !Number.isFinite(p.y) || !Number.isFinite(p.z)) return false;
    }
    let yx = middle.x - wrist.x, yy = middle.y - wrist.y, yz = middle.z - wrist.z;
    const len = Math.hypot(yx, yy, yz);
    let ax = index.x - little.x, ay = index.y - little.y, az = index.z - little.z;
    const width = Math.hypot(ax, ay, az);
    if (len < 1e-5 || width < 1e-5) return false;
    yx /= len; yy /= len; yz /= len;
    ax /= width; ay /= width; az /= width;
    // Left palm normal = palmY x across; right = across x palmY (mirror).
    const s = side === "right" ? -1 : 1;
    let zx = s * (yy * az - yz * ay), zy = s * (yz * ax - yx * az), zz = s * (yx * ay - yy * ax);
    const zl = Math.hypot(zx, zy, zz);
    if (zl < 1e-5) return false;
    zx /= zl; zy /= zl; zz /= zl;
    let xx = yy * zz - yz * zy, xy = yz * zx - yx * zz, xz = yx * zy - yy * zx;
    const xl = Math.hypot(xx, xy, xz);
    xx /= xl; xy /= xl; xz /= xl;
    // Re-orthogonalize palmY = palmZ x palmX.
    yx = zy * xz - zz * xy; yy = zz * xx - zx * xz; yz = zx * xy - zy * xx;
    const set = (key, x, y, z) => {
      const v = out[key] || (out[key] = { x: 0, y: 0, z: 0 });
      v.x = x; v.y = y; v.z = z;
    };
    set("palmX", xx, xy, xz);
    set("palmY", yx, yy, yz);
    set("palmZ", zx, zy, zz);
    set("center",
      wrist.x + (middle.x - wrist.x) * 0.55,
      wrist.y + (middle.y - wrist.y) * 0.55,
      wrist.z + (middle.z - wrist.z) * 0.55);
    out.length = len;
    out.width = width;
    out.side = side === "right" ? "right" : "left";
    return true;
  }

  function clamp(v, lo, hi) {
    return v < lo ? lo : (v > hi ? hi : v);
  }

  function lerp(a, b, t) {
    return a + (b - a) * t;
  }

  function easeInCubic(t) {
    const u = clamp(t, 0, 1);
    return u * u * u;
  }

  function easeOutCubic(t) {
    const u = 1 - clamp(t, 0, 1);
    return 1 - u * u * u;
  }

  function easeInOutCubic(t) {
    const u = clamp(t, 0, 1);
    return u < 0.5 ? 4 * u * u * u : 1 - Math.pow(-2 * u + 2, 3) / 2;
  }

  function dampAlpha(rate, dt) {
    const safeDt = Number.isFinite(dt) && dt > 0 ? Math.min(dt, 0.2) : 0.016;
    return 1 - Math.exp(-rate * safeDt);
  }

  function damp(cur, target, rate, dt) {
    const a = dampAlpha(rate, dt);
    return cur + (target - cur) * a;
  }

  // Deterministic pseudo-random in [0, 1) for natural non-metronomic burst lengths.
  function hashPhase(n) {
    const x = Math.sin((n + 1) * 12.9898 + 78.233) * 43758.5453;
    return x - Math.floor(x);
  }

  // Mirror a VRM 1.0 left-side XYZ Euler triple to the right side across the YZ plane.
  function mirrorLR(rx, ry, rz) {
    return [rx, -ry, -rz];
  }

  // Classify workspace/companion activity from the active beat, expression, and scenario.
  function classifyActivity(input) {
    const expr = input?.expression || "idle";
    const pose = input?.rawPose || "stand";
    const scenarioId = input?.scenarioId || "";
    const beat = input?.beat || null;

    if (expr === "idle_doze" || beat?.fx === "zzz") return "sleepy";
    if (
      expr === "oo" ||
      expr === "cry" ||
      expr === "happy" ||
      scenarioId === "celebrate_done" ||
      scenarioId === "attention_point" ||
      scenarioId === "gloom_fail" ||
      scenarioId === "startled_poke" ||
      scenarioId === "global_session_done"
    ) {
      return "reaction";
    }
    if (
      pose === "typing" ||
      expr === "focused" ||
      expr === "half_lid" ||
      scenarioId === "working_tatata" ||
      beat?.gaze === "scan" ||
      beat?.fx === "ponder"
    ) {
      return "working";
    }
    return "idle";
  }

  // Pure-FK Co-Lead bone pose calculator in VRM 1.0 normalized coordinates.
  function computeCoLeadTargets(params) {
    const t = params.elapsed || 0;
    const typingW = clamp(params.typingWeight || 0, 0, 1);
    const glanceW = clamp(params.glanceWeight || 0, 0, 1);
    const sleepyW = clamp(params.sleepyWeight || 0, 0, 1);
    const reactionW = clamp(params.reactionWeight || 0, 0, 1);
    const workingW = clamp(params.workingWeight || 0, 0, 1);
    const swapSweep = clamp(Number.isFinite(params.swapSweep) ? params.swapSweep : 0, -1, 1);
    const swapActiveW = clamp(params.swapWeight || 0, 0, 1);
    const dizzyW = clamp(params.dizzyWeight || 0, 0, 1);
    const dizzyPhaseT = Number.isFinite(params.dizzyTime) ? params.dizzyTime : t;
    const phoneHoldW = clamp(params.phoneHoldWeight || 0, 0, 1);
    const phoneTypingW = clamp(params.phoneTypingWeight || 0, 0, 1);
    const phoneMerongW = clamp(params.phoneMerongWeight || 0, 0, 1);
    const knockJoltW = clamp(params.knockJoltWeight || 0, 0, 1);
    const annoyedTapW = clamp(params.annoyedTapWeight || 0, 0, 1);
    const extraHeadRoll = Number.isFinite(params.extraHeadRoll) ? params.extraHeadRoll : 0;
    const tiltDir = params.tiltDir >= 0 ? 1 : -1;
    const headYaw = Number.isFinite(params.headYaw) ? params.headYaw : 0;
    const headPitch = Number.isFinite(params.headPitch) ? params.headPitch : 0;
    const headPitchCue = Number.isFinite(params.headPitchCue) ? params.headPitchCue : 0;

    // Gentle alive breathing (slower and deeper when sleepy).
    const breathFreq = 1.42 - sleepyW * 0.42 + workingW * 0.14;
    const breath = Math.sin(t * breathFreq);
    const sway = Math.sin(t * 0.68) * (1 - workingW * 0.45 - sleepyW * 0.3 - dizzyW * 0.8 - phoneHoldW * 0.65);

    // Organic multi-harmonic alternating left/right keystroke cadence.
    const keyMask = clamp(1 - phoneHoldW * 0.95 - knockJoltW * 0.9 - annoyedTapW * 0.9, 0, 1);
    const rate = 6.2 + workingW * 3.4;
    const tapL = (Math.sin(t * rate) * 0.62 + Math.sin(t * rate * 1.73 + 0.9) * 0.38) * typingW * keyMask;
    const tapR = (Math.sin(t * rate + Math.PI * 0.92) * 0.62 + Math.sin(t * rate * 1.61 + 2.4) * 0.38) * typingW * keyMask;
    const wristL = Math.sin(t * rate * 1.15 + 0.4) * typingW * keyMask;
    const wristR = Math.sin(t * rate * 1.15 + 3.4) * typingW * keyMask;

    // Cozy forward seated lean while typing; straightens crisply on knock startle or phone lift.
    const leanScale = clamp(1 - glanceW * 0.65 - reactionW * 0.45 - dizzyW * 0.50 - phoneHoldW * 0.55 - knockJoltW * 0.9, 0.10, 1);
    const dizzySpineYaw = Math.sin(dizzyPhaseT * 3.6) * 2.6 * DEG * dizzyW;
    const dizzySpineRoll = Math.cos(dizzyPhaseT * 3.6) * 2.4 * DEG * dizzyW;
    const swapTorsoYaw = swapSweep * 3.4 * DEG;
    const phoneTorsoYaw = 3.2 * DEG * phoneHoldW - 2.2 * DEG * annoyedTapW;
    const phoneTorsoRoll = -2.4 * DEG * phoneHoldW + 3.5 * DEG * phoneMerongW;
    const spinePitch = (0.036 + workingW * 0.014) * leanScale + sleepyW * 0.030 - knockJoltW * 0.055 + breath * 0.010;
    const chestPitch = (0.028 + workingW * 0.012) * leanScale + sleepyW * 0.022 - knockJoltW * 0.045 + breath * 0.015;
    const upperChestPitch = 0.014 * leanScale - knockJoltW * 0.025 + breath * 0.008;

    // Anime head-tilt on glance-up (+/-7.6 deg), figure-eight sway when dizzy,
    // plus gentle follow-through roll during laptop swaps.
    const glanceRoll = tiltDir * 0.132 * glanceW;
    const idleRoll = sway * 0.016 * (1 - glanceW) + Math.sin(t * 0.48) * 0.014 * workingW * (1 - glanceW);
    const sleepyNod = sleepyW * (0.125 + Math.sin(t * 0.82) * 0.035);

    const totalPitch = headPitch + headPitchCue + sleepyNod;
    const totalRoll = glanceRoll + extraHeadRoll;
    const neckPitch = totalPitch * 0.40;
    const craniumPitch = totalPitch * 0.60;
    const neckYaw = headYaw * 0.36;
    const craniumYaw = headYaw * 0.64 + sway * 0.012 * (1 - glanceW - dizzyW * 0.8);
    const neckRoll = totalRoll * 0.35;
    const craniumRoll = totalRoll * 0.65 + idleRoll * (1 - dizzyW - phoneMerongW * 0.8);

    // Relaxed lowered shoulders rolled subtly forward toward desk, lifting gently
    // on knock jolt or when raising hand with the smartphone.
    const shForward = 0.048 * (1 - sleepyW * 0.45 - dizzyW * 0.28);
    const shDrop = 0.046 + sleepyW * 0.018;
    const dizzyShWave = Math.sin(dizzyPhaseT * 3.6 + 0.6) * 0.034 * dizzyW;
    const swapShLiftL = swapSweep * 0.032;
    const swapShLiftR = -swapSweep * 0.032;
    const joltShLift = knockJoltW * 0.095;
    const lSh = [
      0.012 + tapL * 0.006 + phoneHoldW * 0.028,
      -shForward + swapSweep * 0.030 - phoneHoldW * 0.048,
      -shDrop + breath * 0.012 + tapL * 0.007 + dizzyShWave + swapShLiftL + joltShLift + phoneHoldW * 0.072 + annoyedTapW * 0.052
    ];
    const rSh = [
      0.012 + tapR * 0.006,
      shForward + swapSweep * 0.030,
      shDrop - breath * 0.012 - tapR * 0.007 + dizzyShWave + swapShLiftR - joltShLift
    ];

    // Upper arms stay naturally lowered along sides (|z| >= 1.18) while typing,
    // and smoothly lift phone-side arm (character-left / viewer-right) when holding
    // the smartphone or playfully tapping the glass back at the user.
    const activePoseBlend = clamp(1 - sleepyW * 0.55 - dizzyW * 0.25, 0.35, 1);
    const uPitch = -0.17 * activePoseBlend - workingW * 0.025 + glanceW * 0.03 + dizzyW * 0.05;
    const uYaw = 0.12 * activePoseBlend;
    const uDrop = 1.22 + sleepyW * 0.06 + dizzyW * 0.03;

    const thumbBob = Math.sin(t * 10.5) * 0.022 * phoneTypingW;
    const tapGlassPulse = Math.sin(t * 11.0) * 0.09 * annoyedTapW;

    const lUpperBase = [
      uPitch + tapL * 0.015 - Math.max(0, swapSweep) * 0.05 - knockJoltW * 0.12,
      -uYaw + tapL * 0.010 + swapSweep * 0.09,
      -uDrop - breath * 0.008 + swapActiveW * 0.02 + knockJoltW * 0.14
    ];
    // Phone hold (solved on a generic VRM 1.0 rig): elbow tucked at the side, forearm
    // raised so the palm (and the phone screen) faces the face at upper-chest height.
    const lUpperPhone = [-0.37 + thumbBob * 0.4, 0.85, -1.50 + thumbBob * 0.3];
    const lUpperTap = [-0.46 + tapGlassPulse * 0.35, -0.18, -0.84];
    const lUpper = [
      lerp(lerp(lUpperBase[0], lUpperPhone[0], phoneHoldW), lUpperTap[0], annoyedTapW),
      lerp(lerp(lUpperBase[1], lUpperPhone[1], phoneHoldW), lUpperTap[1], annoyedTapW),
      lerp(lerp(lUpperBase[2], lUpperPhone[2], phoneHoldW), lUpperTap[2], annoyedTapW)
    ];

    // Non-phone arm relaxes softly onto desk/lap while holding phone, and lifts slightly
    // to steady/thumb-type when actively replying on the smartphone.
    const rUpperBase = [
      uPitch * (1 - phoneHoldW * 0.45) + tapR * 0.015 - Math.max(0, -swapSweep) * 0.05 - knockJoltW * 0.12,
      uYaw * (1 - phoneHoldW * 0.35) - tapR * 0.010 + swapSweep * 0.09,
      uDrop + breath * 0.008 - swapActiveW * 0.02 - knockJoltW * 0.14 + phoneHoldW * 0.05
    ];
    // Right index reaches the held phone screen (solved against the palm grip).
    const rUpperType = [-0.03 - thumbBob * 0.35, 0.53, 1.48];
    const rUpper = [
      lerp(rUpperBase[0], rUpperType[0], phoneTypingW * 0.85),
      lerp(rUpperBase[1], rUpperType[1], phoneTypingW * 0.85),
      lerp(rUpperBase[2], rUpperType[2], phoneTypingW * 0.85)
    ];

    // Forearms flex cleanly forward onto keyboard, or flex up smoothly to hold smartphone.
    const fFlex = 1.12 * activePoseBlend + sleepyW * 0.28 - glanceW * 0.10 - dizzyW * 0.16 - swapActiveW * 0.08;
    const fInward = 0.28 * activePoseBlend + sleepyW * 0.06;
    const fTwist = 0.16 * activePoseBlend;

    const lLowerBase = [
      fTwist + tapL * 0.030,
      -(fFlex + tapL * 0.045 + knockJoltW * 0.18),
      -(fInward + tapL * 0.018 - swapSweep * 0.06)
    ];
    const lLowerPhone = [1.33 + thumbBob, -(2.0 + thumbBob * 0.65), 0.30];
    const lLowerTap = [0.22, -(1.56 + tapGlassPulse), -0.12];
    const lLower = [
      lerp(lerp(lLowerBase[0], lLowerPhone[0], phoneHoldW), lLowerTap[0], annoyedTapW),
      lerp(lerp(lLowerBase[1], lLowerPhone[1], phoneHoldW), lLowerTap[1], annoyedTapW),
      lerp(lerp(lLowerBase[2], lLowerPhone[2], phoneHoldW), lLowerTap[2], annoyedTapW)
    ];

    const rLowerBase = [
      fTwist * (1 - phoneHoldW * 0.4) + tapR * 0.030,
      fFlex * (1 - phoneHoldW * 0.35) + tapR * 0.045 + knockJoltW * 0.18,
      fInward * (1 - phoneHoldW * 0.4) + tapR * 0.018 + swapSweep * 0.06
    ];
    const rLowerType = [0.55 - thumbBob * 0.8, 2.52 + thumbBob * 0.9, -0.26];
    const rLower = [
      lerp(rLowerBase[0], rLowerType[0], phoneTypingW * 0.85),
      lerp(rLowerBase[1], rLowerType[1], phoneTypingW * 0.85),
      lerp(rLowerBase[2], rLowerType[2], phoneTypingW * 0.85)
    ];

    // Delicate alternating wrist/hand key-tap articulation or phone thumb-typing micro-motion.
    const lHandBase = [0.08 + wristL * 0.055, 0.05 + tapL * 0.035, 0.10 + wristL * 0.04];
    const lHandPhone = [-0.66 + thumbBob * 1.4, -0.67 - thumbBob * 0.8, 0.03];
    const lHand = [
      lerp(lHandBase[0], lHandPhone[0], phoneHoldW),
      lerp(lHandBase[1], lHandPhone[1], phoneHoldW),
      lerp(lHandBase[2], lHandPhone[2], phoneHoldW)
    ];
    const rHandBase = [0.08 + wristR * 0.055, -(0.05 + tapR * 0.035), -(0.10 + wristR * 0.04)];
    const rHandType = [-0.19 - thumbBob * 1.6, -0.11 + thumbBob * 0.9, -0.29];
    const rHand = [
      lerp(rHandBase[0], rHandType[0], phoneTypingW * 0.85),
      lerp(rHandBase[1], rHandType[1], phoneTypingW * 0.85),
      lerp(rHandBase[2], rHandType[2], phoneTypingW * 0.85)
    ];

    const targets = {
      spine: [spinePitch, sway * 0.009 * (1 - glanceW * 0.5) + dizzySpineYaw + swapTorsoYaw + phoneTorsoYaw, dizzySpineRoll + phoneTorsoRoll],
      chest: [chestPitch, swapTorsoYaw * 0.65 + phoneTorsoYaw * 0.55, dizzySpineRoll * 0.55 + phoneTorsoRoll * 0.5],
      upperChest: [upperChestPitch, 0, 0],
      neck: [neckPitch, neckYaw, neckRoll],
      head: [craniumPitch, craniumYaw, craniumRoll],
      leftShoulder: lSh,
      rightShoulder: rSh,
      leftUpperArm: lUpper,
      rightUpperArm: rUpper,
      leftLowerArm: lLower,
      rightLowerArm: rLower,
      leftHand: lHand,
      rightHand: rHand
    };

    // 30-bone finger curls: keys while typing, wrap the phone while holding it,
    // point at the glass on annoyed tap, and tap the screen with the right index.
    const forcedPreset = typeof params.fingerPreset === "string" && resolveFingerPreset(params.fingerPreset) ? params.fingerPreset : "";
    const fingerTargets = computeFingerTargets({
      left: { typing: typingW * keyMask, grip: phoneHoldW * (1 - annoyedTapW), point: annoyedTapW, tap: tapL, preset: forcedPreset },
      right: { typing: typingW * keyMask, grip: 0, point: phoneTypingW * 0.85, tap: tapR, preset: forcedPreset }
    }, params.fingerOut && typeof params.fingerOut === "object" ? params.fingerOut : null);
    for (let i = 0; i < FINGER_BONES.length; i++) {
      targets[FINGER_BONES[i]] = fingerTargets[FINGER_BONES[i]];
    }

    // Phase 3 additive hooks (no-op when omitted).
    if (params.breakoutPose && typeof params.breakoutPose === "object") {
      for (const bone of Object.keys(params.breakoutPose)) {
        const delta = params.breakoutPose[bone];
        if (!Array.isArray(delta) || delta.length < 3) continue;
        const base = targets[bone] || [0, 0, 0];
        targets[bone] = [base[0] + (delta[0] || 0), base[1] + (delta[1] || 0), base[2] + (delta[2] || 0)];
      }
    }

    return targets;
  }

  // Pure choreography pose/target evaluators shared by dashboard-companion-vrm-colead.js.
  function evalDizzyBeat(p) {
    const { inDizzySway, forcedDizzy, forcedDizzyShake, forcedDizzyRecover, dizzyElapsed, recoverDur, elapsed, gx, baseScale, baseVisH } = p;
    if (inDizzySway) {
      const dizzyTime = forcedDizzy ? (elapsed > 0.01 ? elapsed : 0.65) : dizzyElapsed;
      const env = forcedDizzy ? 1.0 : clamp(dizzyElapsed / 0.28, 0, 1);
      const flutter = 0.14 + 0.14 * (0.5 + 0.5 * Math.sin(dizzyTime * 5.2));
      return {
        dizzyTime,
        wantHeadYaw: Math.sin(dizzyTime * 3.6) * 8.8 * DEG * env,
        wantHeadPitch: (3.4 + Math.sin(dizzyTime * 7.2) * 3.0) * DEG * env,
        extraHeadRoll: Math.cos(dizzyTime * 3.6) * 8.5 * DEG * env,
        eyeYaw: Math.sin(dizzyTime * 4.4) * 11.5 * DEG * env,
        eyePitch: (2.0 + Math.cos(dizzyTime * 4.4) * 5.8) * DEG * env,
        targetOffset: { dx: -baseScale * 0.03 * env, dy: -baseVisH * 0.062 * env, dz: -baseScale * 0.01 * env, rx: 3.5 * DEG * env, ry: 0, rz: -5.2 * DEG * env },
        targetDizzyFxAlpha: env,
        glowMultiplier: 0.48,
        exprOverlay: { mode: "dizzy", surprised: 0.36 * env, sad: 0.22 * env, relaxed: 0.10 * env, blink: flutter * env, allowBlink: false }
      };
    }
    const r = forcedDizzyShake ? 0.24 : (forcedDizzyRecover ? 0.78 : clamp(dizzyElapsed / recoverDur, 0, 1));
    const liftE = easeOutCubic(clamp(r / 0.78, 0, 1));
    const targetOffset = {
      dx: lerp(-baseScale * 0.03, 0, liftE), dy: lerp(-baseVisH * 0.062, 0, liftE), dz: lerp(-baseScale * 0.01, 0, liftE),
      rx: lerp(3.5 * DEG, 0, liftE), ry: 0, rz: lerp(-5.2 * DEG, 0, liftE)
    };
    const targetDizzyFxAlpha = clamp(1 - r / 0.34, 0, 1);
    if (r < 0.48) {
      const u = r / 0.48;
      const blink1 = Math.exp(-Math.pow((u - 0.26) / 0.14, 2));
      const blink2 = Math.exp(-Math.pow((u - 0.72) / 0.14, 2));
      return {
        dizzyTime: elapsed,
        wantHeadYaw: forcedDizzyShake ? 7.4 * DEG : Math.sin(u * Math.PI * 2.5) * 8.2 * DEG * (1 - u * 0.35),
        wantHeadPitch: 2.5 * DEG,
        extraHeadRoll: forcedDizzyShake ? -4.2 * DEG : Math.sin(u * Math.PI * 2.0) * 3.8 * DEG * (1 - u * 0.4),
        eyeYaw: 0, eyePitch: 0, targetOffset, targetDizzyFxAlpha,
        glowMultiplier: lerp(0.52, 0.92, u),
        exprOverlay: { mode: "dizzy-shake", blink: forcedDizzyShake ? 0.78 : clamp(Math.max(blink1, blink2) * 0.88, 0, 0.9), sad: 0.14 * (1 - u), relaxed: 0.18 + 0.12 * u, happy: 0.14 * u, allowBlink: false }
      };
    }
    const u = (r - 0.48) / 0.52;
    const smileEnv = forcedDizzyRecover ? 1.0 : Math.sin(clamp(u, 0, 1) * Math.PI * 0.85 + 0.15);
    return {
      dizzyTime: elapsed,
      wantHeadYaw: gx * 3.0 * DEG,
      wantHeadPitch: lerp(0.5 * DEG, 4.2 * DEG, forcedDizzyRecover ? 0.22 : u),
      extraHeadRoll: 7.5 * DEG * smileEnv,
      eyeYaw: gx * 5 * DEG,
      eyePitch: lerp(1.8 * DEG, -6.5 * DEG, forcedDizzyRecover ? 0.12 : u),
      targetOffset, targetDizzyFxAlpha, glowMultiplier: 1.0,
      exprOverlay: { mode: "dizzy-recover", happy: 0.54 * smileEnv, relaxed: 0.22 * smileEnv, allowBlink: true }
    };
  }

  function evalSwapBeat(p) {
    const { forcedSwapOut, forcedSwapIn, outDur, inDur, settleDur, swapElapsed, swapDir, swapStartPose, gx, baseScale, baseVisH } = p;
    const totalDur = outDur + inDur + settleDur;
    const tSwap = forcedSwapOut ? outDur * 0.54 : (forcedSwapIn ? outDur + inDur * 0.34 : clamp(swapElapsed, 0, totalDur));
    const swapProgress = clamp(tSwap / totalDur, 0, 1);
    const troughOut = { dx: swapDir * baseScale * 0.24, dy: -baseVisH * 0.25, dz: -baseScale * 0.04, rx: 6.0 * DEG, ry: -swapDir * 10.0 * DEG, rz: -swapDir * 8.5 * DEG };
    const troughIn = { dx: -swapDir * baseScale * 0.21, dy: -baseVisH * 0.24, dz: -baseScale * 0.03, rx: 5.0 * DEG, ry: swapDir * 8.5 * DEG, rz: swapDir * 7.5 * DEG };
    if (tSwap < outDur) {
      const u = clamp(tSwap / Math.max(0.05, outDur), 0, 1);
      const e = easeInOutCubic(u);
      const sp = forcedSwapOut ? { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 } : swapStartPose;
      const follow = Math.sin(u * Math.PI * 0.85);
      return {
        swapStage: "swap-out", swapProgress, immediateOffset: true,
        targetOffset: { dx: lerp(sp.dx, troughOut.dx, e), dy: lerp(sp.dy, troughOut.dy, e), dz: lerp(sp.dz, troughOut.dz, e), rx: lerp(sp.rx, troughOut.rx, e), ry: lerp(sp.ry, troughOut.ry, e), rz: lerp(sp.rz, troughOut.rz, e) },
        swapSweep: swapDir * follow * 0.85,
        wantHeadYaw: swapDir * 9.5 * DEG * follow, wantHeadPitch: (6.8 + 3.2 * follow) * DEG, extraHeadRoll: swapDir * 4.2 * DEG * follow,
        eyeYaw: swapDir * 14.0 * DEG * follow, eyePitch: -11.5 * DEG, glowMultiplier: lerp(1.0, 0.25, e),
        exprOverlay: { mode: "swap-out", happy: 0.16, relaxed: 0.10, allowBlink: true }
      };
    }
    if (tSwap < outDur + inDur) {
      const v = clamp((tSwap - outDur) / inDur, 0, 1);
      const e = easeOutCubic(v);
      const settleOvershoot = Math.sin(v * Math.PI) * v * v * (1 - v) * baseVisH * 0.05;
      const sweepIn = 1 - e;
      return {
        swapStage: "swap-in", swapProgress, immediateOffset: true,
        targetOffset: { dx: lerp(troughIn.dx, 0, e), dy: lerp(troughIn.dy, 0, e) + settleOvershoot, dz: lerp(troughIn.dz, 0, e), rx: lerp(troughIn.rx, 0, e), ry: lerp(troughIn.ry, 0, e), rz: lerp(troughIn.rz, 0, e) },
        swapSweep: -swapDir * sweepIn * 0.72,
        wantHeadYaw: -swapDir * 7.8 * DEG * sweepIn, wantHeadPitch: (5.8 + 2.4 * sweepIn) * DEG, extraHeadRoll: -swapDir * 3.4 * DEG * sweepIn,
        eyeYaw: -swapDir * 11.5 * DEG * sweepIn, eyePitch: -9.8 * DEG, glowMultiplier: lerp(0.28, 1.08, e),
        exprOverlay: { mode: "swap-in", happy: 0.22 + 0.08 * e, relaxed: 0.12, allowBlink: true }
      };
    }
    const w = clamp((tSwap - outDur - inDur) / settleDur, 0, 1);
    const nodPulse = Math.sin(w * Math.PI);
    return {
      swapStage: "swap-settle", swapProgress, immediateOffset: true,
      targetOffset: { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 }, swapSweep: 0,
      wantHeadYaw: gx * 2.5 * DEG, wantHeadPitch: (5.4 + 3.2 * nodPulse) * DEG, extraHeadRoll: 2.4 * DEG * nodPulse,
      eyeYaw: gx * 4.5 * DEG, eyePitch: -9.2 * DEG, glowMultiplier: 1.05,
      exprOverlay: { mode: "swap-settle", happy: 0.16 + 0.18 * nodPulse, relaxed: 0.09 + 0.08 * nodPulse, allowBlink: true }
    };
  }

  function evalKnockBeat(p) {
    const { activeKnockKind, forcedWakeupJolt, forcedAnnoyedTap, forcedKnockStartle, knockElapsed, knockDuration, gx } = p;
    if (activeKnockKind === "wakeup-jolt") {
      const env = forcedWakeupJolt ? 1.0 : Math.sin(clamp(knockElapsed / Math.max(0.2, knockDuration), 0, 1) * Math.PI);
      return {
        wantHeadYaw: gx * 4 * DEG, wantHeadPitch: -4.2 * DEG * env, extraHeadRoll: 4.0 * DEG * env,
        eyeYaw: gx * 6 * DEG, eyePitch: 4.5 * DEG,
        exprOverlay: { mode: "wakeup-jolt", surprised: 0.96, blink: 0.0, allowBlink: false },
        targetActionFxKind: "wakeup-jolt", targetActionFxAlpha: Math.max(0.25, env)
      };
    }
    if (activeKnockKind === "annoyed-tap") {
      const env = forcedAnnoyedTap ? 1.0 : clamp(1 - knockElapsed / Math.max(0.3, knockDuration) * 0.25, 0.3, 1);
      return {
        wantHeadYaw: -5.5 * DEG, wantHeadPitch: -1.2 * DEG, extraHeadRoll: -6.8 * DEG * env,
        eyeYaw: -3.0 * DEG, eyePitch: 2.0 * DEG,
        exprOverlay: { mode: "annoyed-tap", angry: 0.62, blink: 0.10, allowBlink: false },
        targetActionFxKind: "annoyed-tap", targetActionFxAlpha: env
      };
    }
    const env = forcedKnockStartle ? 1.0 : Math.sin(clamp(knockElapsed / Math.max(0.2, knockDuration), 0, 1) * Math.PI);
    return {
      wantHeadYaw: gx * 5 * DEG, wantHeadPitch: -3.0 * DEG * env, extraHeadRoll: -3.2 * DEG * env,
      eyeYaw: gx * 8 * DEG, eyePitch: 3.0 * DEG,
      exprOverlay: { mode: "knock-startle", surprised: 0.88, blink: 0.0, allowBlink: false },
      targetActionFxKind: "knock-startle", targetActionFxAlpha: Math.max(0.25, env)
    };
  }

  function evalPhoneBeat(p) {
    const { activePhoneStage, baseScale, baseVisH } = p;
    if (activePhoneStage === "buzz") {
      return {
        wantHeadYaw: 12.5 * DEG, wantHeadPitch: 8.5 * DEG, extraHeadRoll: 4.5 * DEG,
        eyeYaw: 18.0 * DEG, eyePitch: -11.5 * DEG,
        exprOverlay: { mode: "phone-buzz", surprised: 0.36, happy: 0.16, blink: 0.0, allowBlink: false },
        targetActionFxKind: "phone-buzz", targetActionFxAlpha: 1.0
      };
    }
    if (activePhoneStage === "pickup" || activePhoneStage === "typing") {
      return {
        targetOffset: { dx: -baseScale * 0.03, dy: -baseVisH * 0.032, dz: 0, rx: 1.8 * DEG, ry: 0, rz: 0 },
        wantHeadYaw: 10.5 * DEG, wantHeadPitch: 6.2 * DEG, extraHeadRoll: 5.4 * DEG,
        eyeYaw: 14.5 * DEG, eyePitch: -8.5 * DEG,
        exprOverlay: { mode: activePhoneStage === "typing" ? "phone-typing" : "phone-pickup", happy: 0.28, relaxed: 0.16, allowBlink: true }
      };
    }
    if (activePhoneStage === "facedown-merong") {
      return {
        wantHeadYaw: 4.5 * DEG, wantHeadPitch: -1.8 * DEG, extraHeadRoll: 10.5 * DEG,
        eyeYaw: 3.5 * DEG, eyePitch: 2.0 * DEG,
        exprOverlay: { mode: "phone-facedown-merong", wink: 0.96, happy: 0.72, merong: 0.85, allowBlink: false },
        targetActionFxKind: "phone-facedown-merong", targetActionFxAlpha: 1.0
      };
    }
    if (activePhoneStage === "putdown") {
      return {
        wantHeadYaw: 7.5 * DEG, wantHeadPitch: 7.2 * DEG, extraHeadRoll: 2.2 * DEG,
        eyeYaw: 11.0 * DEG, eyePitch: -10.0 * DEG,
        exprOverlay: { mode: "phone-putdown", happy: 0.22, relaxed: 0.14, allowBlink: true }
      };
    }
    return null;
  }

  function evalPhonePropTarget(p) {
    const { activePhoneStage, wantPhoneHold, wantPhoneTyping, phoneIsFacedownNow, elapsed, basePos, baseScale, baseVisH, baseVisHalfW, phoneBasePos, lidTopWorldY } = p;
    if (activePhoneStage === "buzz") {
      const vib = Math.sin(elapsed * 52);
      const vib2 = Math.cos(elapsed * 46);
      return {
        phoneTarget: { x: phoneBasePos.x + vib * baseScale * 0.016, y: phoneBasePos.y + Math.abs(vib2) * baseScale * 0.008, z: phoneBasePos.z + 0.005, rx: -32 * DEG, ry: -14 * DEG, rz: -6 * DEG + vib * 6.5 * DEG },
        targetPhoneGlow: 0.95 + 0.30 * (0.5 + 0.5 * Math.sin(elapsed * 14)),
        targetBuzzRingAlpha: 0.42 + 0.28 * (0.5 + 0.5 * Math.sin(elapsed * 14))
      };
    }
    if (wantPhoneHold) {
      const thumbJiggle = wantPhoneTyping ? Math.sin(elapsed * 11) * baseScale * 0.006 : 0;
      const heldX = basePos.x + Math.min(baseScale * 0.46, Math.max(baseScale * 0.34, baseVisHalfW - baseScale * 0.28));
      const heldY = lidTopWorldY + baseVisH * 0.195 + thumbJiggle;
      return {
        phoneTarget: { x: heldX, y: heldY, z: basePos.z - baseScale * 0.03, rx: -8 * DEG, ry: 144 * DEG, rz: 12 * DEG + (wantPhoneTyping ? Math.sin(elapsed * 9) * 2.5 * DEG : 0) },
        targetPhoneGlow: 0.72,
        targetBuzzRingAlpha: 0.0
      };
    }
    if (activePhoneStage === "facedown-merong") {
      return {
        phoneTarget: { x: phoneBasePos.x, y: phoneBasePos.y + baseScale * 0.015, z: phoneBasePos.z + baseScale * 0.01, rx: -36 * DEG, ry: 168 * DEG, rz: -5 * DEG },
        targetPhoneGlow: 0.04,
        targetBuzzRingAlpha: 0.0
      };
    }
    return {
      phoneTarget: { x: phoneBasePos.x, y: phoneBasePos.y, z: phoneBasePos.z, rx: phoneIsFacedownNow ? -44 * DEG : -46 * DEG, ry: phoneIsFacedownNow ? 168 * DEG : -12 * DEG, rz: -6 * DEG },
      targetPhoneGlow: 0.14,
      targetBuzzRingAlpha: 0.0
    };
  }

  Object.assign(C, {
    vrmCoLeadPose: {
      clamp,
      lerp,
      easeInCubic,
      easeOutCubic,
      easeInOutCubic,
      dampAlpha,
      damp,
      hashPhase,
      mirrorLR,
      classifyActivity,
      FINGER_BONES,
      FINGER_PRESETS,
      computeFingerTargets,
      computePalmFrame,
      computeCoLeadTargets,
      evalDizzyBeat,
      evalSwapBeat,
      evalKnockBeat,
      evalPhoneBeat,
      evalPhonePropTarget
    }
  });
})(window.WsCompanion);
