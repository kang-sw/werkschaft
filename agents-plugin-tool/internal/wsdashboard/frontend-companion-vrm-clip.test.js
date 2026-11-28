const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function loadVrmStack(globals = {}) {
  const warnings = [];
  const context = vm.createContext({
    window: {},
    URLSearchParams,
    AbortController,
    setTimeout,
    clearTimeout,
    console: { ...console, warn: (...args) => warnings.push(args.join(" ")) },
    performance: { now: () => 0 },
    ...globals
  });
  context.window = context.window || {};
  for (const name of [
    "builtins", "packs", "focus", "deform", "compositor", "scenarios", "menu",
    "vrm-fx", "vrm-studio", "vrm-props", "vrm-pose", "vrm-ik", "vrm-clip",
    "vrm-colead", "vrm", "stage"
  ]) {
    vm.runInContext(
      fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"),
      context,
      { filename: `dashboard-companion-${name}.js` }
    );
  }
  return { C: context.window.WsCompanion, context, warnings };
}

// Forward kinematics helper for XYZ intrinsic Euler angles matching Three.js Euler(x, y, z, "XYZ").
function eulerXYZMatrix(rx, ry, rz) {
  const cx = Math.cos(rx), sx = Math.sin(rx);
  const cy = Math.cos(ry), sy = Math.sin(ry);
  const cz = Math.cos(rz), sz = Math.sin(rz);
  return [
    [cy * cz, -cy * sz, sy],
    [cx * sz + cz * sx * sy, cx * cz - sx * sy * sz, -cy * sx],
    [sx * sz - cx * cz * sy, cz * sx + cx * sy * sz, cx * cy]
  ];
}

function mulMatVec(m, v) {
  return [
    m[0][0] * v[0] + m[0][1] * v[1] + m[0][2] * v[2],
    m[1][0] * v[0] + m[1][1] * v[1] + m[1][2] * v[2],
    m[2][0] * v[0] + m[2][1] * v[1] + m[2][2] * v[2]
  ];
}

function mulMat(a, b) {
  const out = [[0, 0, 0], [0, 0, 0], [0, 0, 0]];
  for (let r = 0; r < 3; r++) {
    for (let c = 0; c < 3; c++) {
      out[r][c] = a[r][0] * b[0][c] + a[r][1] * b[1][c] + a[r][2] * b[2][c];
    }
  }
  return out;
}

function makeMathTHREE() {
  class Vector3 {
    constructor(x = 0, y = 0, z = 0) { this.x = x; this.y = y; this.z = z; }
    set(x, y, z) { this.x = x; this.y = y; this.z = z; return this; }
    copy(v) { this.x = v.x; this.y = v.y; this.z = v.z; return this; }
    clone() { return new Vector3(this.x, this.y, this.z); }
    distanceTo(v) { return Math.hypot(this.x - v.x, this.y - v.y, this.z - v.z); }
  }
  class Node {
    constructor(name, worldPos) {
      this.name = name;
      this._wp = worldPos;
    }
    getWorldPosition(out) { return out.set(this._wp[0], this._wp[1], this._wp[2]); }
  }
  return { Vector3, Node };
}

function buildRig(THREE, upperLen, lowerLen) {
  const chestPos = [0, 1.15, 0];
  const rSh = [-0.15, 1.29, 0];
  const rEl = [rSh[0] - upperLen, rSh[1], rSh[2]];
  const rHa = [rEl[0] - lowerLen, rEl[1], rEl[2]];
  const lSh = [0.15, 1.29, 0];
  const lEl = [lSh[0] + upperLen, lSh[1], lSh[2]];
  const lHa = [lEl[0] + lowerLen, lEl[1], lEl[2]];
  const bones = {
    chest: new THREE.Node("chest", chestPos),
    head: new THREE.Node("head", [0, 1.47, 0]),
    rightUpperArm: new THREE.Node("rightUpperArm", rSh),
    rightLowerArm: new THREE.Node("rightLowerArm", rEl),
    rightHand: new THREE.Node("rightHand", rHa),
    leftUpperArm: new THREE.Node("leftUpperArm", lSh),
    leftLowerArm: new THREE.Node("leftLowerArm", lEl),
    leftHand: new THREE.Node("leftHand", lHa)
  };
  return { vrm: { humanoid: { getNormalizedBoneNode: n => bones[n] || null } }, bones };
}

function fkArmWorld(chain, upperEuler, lowerEuler) {
  const sign = chain.side === "left" ? 1 : -1;
  const shWorld = [
    chain.chestPos[0] + chain.shoulderOffset[0],
    chain.chestPos[1] + chain.shoulderOffset[1],
    chain.chestPos[2] + chain.shoulderOffset[2]
  ];
  const R1 = eulerXYZMatrix(upperEuler[0], upperEuler[1], upperEuler[2]);
  const R2 = mulMat(R1, eulerXYZMatrix(lowerEuler[0], lowerEuler[1], lowerEuler[2]));
  const upperVec = mulMatVec(R1, [sign * chain.upperLen, 0, 0]);
  const lowerVec = mulMatVec(R2, [sign * chain.lowerLen, 0, 0]);
  const elbow = [shWorld[0] + upperVec[0], shWorld[1] + upperVec[1], shWorld[2] + upperVec[2]];
  const wrist = [elbow[0] + lowerVec[0], elbow[1] + lowerVec[1], elbow[2] + lowerVec[2]];
  return { elbow, wrist };
}

test("vrmClip.resolveEntry enforces D4 order: sentinels -> actions -> animations -> procedural", () => {
  const { C } = loadVrmStack();
  const pack = {
    sentinels: {
      idle: { kind: "clip", file: "clips/idle.clip.json", loop: true },
      phone_hold: { kind: "clip", file: "clips/phone_hold.clip.json" },
      phone_typing: { kind: "clip", file: "clips/phone_typing.clip.json", loop: true }
    },
    actionMap: {
      wave_hi: { key: "wave_hi", kind: "clip", file: "clips/wave_hi.clip.json", loop: false, duration_s: 1.6 },
      dance_vrma: { key: "dance_vrma", kind: "vrma", file: "clips/dance_vrma.vrma", loop: false }
    },
    animations: {
      legacy_wave: "legacy_wave.vrma",
      wave_hi: "shadowed.vrma"
    }
  };

  const s1 = C.vrmClip.resolveEntry(pack, "idle", { isSentinel: true });
  assert.equal(s1.source, "sentinels");
  assert.equal(s1.kind, "clip");
  assert.equal(s1.loop, true);

  // phone_hold default playback is one-shot into hold (loop: false).
  const sHold = C.vrmClip.resolveEntry(pack, "phone_hold", { isSentinel: true });
  assert.equal(sHold.source, "sentinels");
  assert.equal(sHold.loop, false);

  // Non-sentinel gesture targeting a sentinel key resolves to the sentinel override.
  const s2 = C.vrmClip.resolveEntry(pack, "phone_typing");
  assert.equal(s2.source, "sentinels");
  assert.equal(s2.file, "clips/phone_typing.clip.json");

  // Custom action takes precedence over legacy animations of the same name.
  const a1 = C.vrmClip.resolveEntry(pack, "wave_hi");
  assert.equal(a1.source, "actions");
  assert.equal(a1.file, "clips/wave_hi.clip.json");
  assert.equal(a1.duration_s, 1.6);

  // Legacy animation fallback when no action matches.
  const leg = C.vrmClip.resolveEntry(pack, "legacy_wave");
  assert.equal(leg.source, "animations");
  assert.equal(leg.kind, "vrma");
  assert.equal(leg.file, "legacy_wave.vrma");

  // Unknown key falls back to null (procedural).
  assert.equal(C.vrmClip.resolveEntry(pack, "unknown_beat"), null);
});

test("vrmClip.sampleClip interpolates per-keyframe easing, degrades unknown easing to linear, and supports phone_hold holdLast", () => {
  const { C } = loadVrmStack();
  const clip = {
    schema: "ws-companion-clip/v1",
    key: "test_ease",
    duration_s: 2.0,
    loop: false,
    easing: "ease_in_out",
    keyframes: [
      {
        t: 0.0,
        easing: "ease_in",
        expression: { happy: 0.0 },
        head: { yaw_deg: 0, pitch_deg: 0, roll_deg: 0, lean_deg: 0, turn_deg: 0 },
        hands: { right: { ik: [0, 0, 0], pole: [0, -1, 0], fingers: { preset: "relaxed" } } }
      },
      {
        t: 1.0,
        easing: "unknown_future_curve",
        expression: { happy: 1.0 },
        head: { yaw_deg: 20, pitch_deg: 10, roll_deg: 0, lean_deg: 0, turn_deg: 0 },
        hands: { right: { ik: [0, 0.5, 0.5], pole: [0, -1, 0], fingers: { preset: "point" } } }
      },
      {
        t: 2.0,
        expression: { happy: 0.25 },
        head: { yaw_deg: 4, pitch_deg: 0, roll_deg: 0, lean_deg: 0, turn_deg: 0 },
        hands: { right: { ik: [0, 0, 0], pole: [0, -1, 0], fingers: { preset: "relaxed" } } }
      }
    ]
  };

  // At t=0.5 with ease_in (u^3 = 0.125), yaw should be ~2.5 deg (not linear 10 deg).
  const mid1 = C.vrmClip.sampleClip(clip, 0.5);
  assert.ok(mid1.head.yaw_deg > 2.0 && mid1.head.yaw_deg < 3.5, `expected ease_in curve, got ${mid1.head.yaw_deg}`);
  assert.ok(mid1.expression.happy > 0.1 && mid1.expression.happy < 0.2);

  // Between t=1.0 and t=2.0, unknown easing must degrade to linear (u=0.5 -> yaw=12).
  const mid2 = C.vrmClip.sampleClip(clip, 1.5);
  assert.ok(Math.abs(mid2.head.yaw_deg - 12) < 0.01, `unknown easing must read as linear, got ${mid2.head.yaw_deg}`);

  // phone_hold plays one-shot into last keyframe, then holds with isOneShotActive() === false.
  const player = C.vrmClip.createClipPlayer();
  player.play("phone_hold", clip, { loop: false, duration_s: 0.4, isSentinel: true, holdLast: true, immediate: true });
  assert.equal(player.isOneShotActive(), true);
  player.step(0.15);
  assert.equal(player.isOneShotActive(), true);
  for (let i = 0; i < 4; i++) player.step(0.15);
  const held = player.step(0.1);
  assert.ok(held && held.holding, "phone_hold must remain active in holding state");
  assert.equal(held.weight, 1);
  assert.equal(player.isOneShotActive(), false, "held phone_hold must not trigger active cadence");
  assert.ok(Math.abs(held.sample.head.yaw_deg - 4) < 0.01, "held phone_hold keeps last keyframe pose");
});

test("vrmIk solves 2-bone IK with reach clamp, pole swivel, unknown prop warning, and converges within 5mm across two arm lengths", () => {
  const { C, warnings } = loadVrmStack();
  const THREE = makeMathTHREE();

  // Two distinct kits: short arm (0.20 + 0.18 = 0.38m) vs long arm (0.31 + 0.29 = 0.60m).
  const shortRig = buildRig(THREE, 0.20, 0.18);
  const longRig = buildRig(THREE, 0.31, 0.29);

  // Common world contact target (prop:smartphone#grip).
  const phoneGripWorld = [-0.22, 1.18, 0.24];
  const attachSpec = { space: "prop:smartphone#grip", offset: [0, 0.005, 0], weight: 1.0 };
  const expectedContact = [phoneGripWorld[0], phoneGripWorld[1] + 0.005, phoneGripWorld[2]];

  for (const [label, rig] of [["short-kit", shortRig], ["long-kit", longRig]]) {
    const chain = C.vrmIk.measureArmChain(rig.vrm, "right", THREE);
    assert.ok(chain && chain.chainLen > 0.3, `${label} chain measured`);

    const resolved = C.vrmIk.resolveTargetAttach(attachSpec, {
      vrm: rig.vrm,
      THREE,
      camera: { position: { x: 0, y: 1.4, z: 1.2 } },
      getPropAnchorWorld: (id, anc) => (id === "smartphone" && anc === "grip" ? phoneGripWorld.slice() : null)
    });
    assert.ok(resolved, `${label} target_attach resolved`);
    const localTarget = C.vrmIk.worldToChestLocal([resolved.x, resolved.y, resolved.z], chain);
    const solved = C.vrmIk.solveTwoBoneArmIK({
      side: "right",
      shoulder: chain.shoulderOffset,
      target: [localTarget[0] * chain.chainLen, localTarget[1] * chain.chainLen, localTarget[2] * chain.chainLen],
      pole: [-0.2, -0.4, -0.85],
      upperLen: chain.upperLen,
      lowerLen: chain.lowerLen
    });
    const { wrist } = fkArmWorld(chain, solved.upperEuler, solved.lowerEuler);
    const errMeters = Math.hypot(
      wrist[0] - expectedContact[0],
      wrist[1] - expectedContact[1],
      wrist[2] - expectedContact[2]
    );
    assert.ok(errMeters < 0.005, `${label} wrist contact error ${(errMeters * 1000).toFixed(2)}mm exceeded 5mm`);
  }

  // Pole swivel changes elbow position while preserving wrist target.
  const longChain = C.vrmIk.measureArmChain(longRig.vrm, "right", THREE);
  const localT = C.vrmIk.worldToChestLocal(expectedContact, longChain);
  const scaledT = [localT[0] * longChain.chainLen, localT[1] * longChain.chainLen, localT[2] * longChain.chainLen];
  const solvedDown = C.vrmIk.solveTwoBoneArmIK({
    side: "right", shoulder: longChain.shoulderOffset, target: scaledT,
    pole: [-0.2, -1.0, -0.3], upperLen: longChain.upperLen, lowerLen: longChain.lowerLen
  });
  const solvedUp = C.vrmIk.solveTwoBoneArmIK({
    side: "right", shoulder: longChain.shoulderOffset, target: scaledT,
    pole: [-0.2, 1.0, -0.3], upperLen: longChain.upperLen, lowerLen: longChain.lowerLen
  });
  const fkDown = fkArmWorld(longChain, solvedDown.upperEuler, solvedDown.lowerEuler);
  const fkUp = fkArmWorld(longChain, solvedUp.upperEuler, solvedUp.lowerEuler);
  assert.ok(fkUp.elbow[1] > fkDown.elbow[1] + 0.08, `pole swivel must raise elbow: up=${fkUp.elbow[1]} down=${fkDown.elbow[1]}`);

  // Reach clamp: unreachable target 5m away clamps cleanly without NaN.
  const solvedFar = C.vrmIk.solveTwoBoneArmIK({
    side: "right", shoulder: longChain.shoulderOffset, target: [-5, 0, 0],
    pole: [-0.2, -0.4, -0.8], upperLen: longChain.upperLen, lowerLen: longChain.lowerLen
  });
  assert.equal(solvedFar.clamped, true);
  assert.ok(solvedFar.upperEuler.every(Number.isFinite) && solvedFar.lowerEuler.every(Number.isFinite));

  // Unknown Stage-2 prop:* resolves as free (null) with a one-time console warning.
  const warnBefore = warnings.length;
  assert.equal(C.vrmIk.resolveTargetAttach({ space: "prop:mug#handle" }, { getPropAnchorWorld: () => null }), null);
  assert.equal(C.vrmIk.resolveTargetAttach({ space: "prop:mug#handle" }, { getPropAnchorWorld: () => null }), null);
  assert.equal(warnings.length, warnBefore + 1, "unknown prop:* should warn exactly once");
});

test("vrmCoLead sentinel mapping covers all 7 sentinels, propagates knock/wakeup duration_s, and idle loop never marks activity", () => {
  const { C } = loadVrmStack();

  // Verify the 7 canonical sentinel keys and D3 default playback flags.
  const expectedSentinels = [
    ["idle", true],
    ["laptop_work", true],
    ["phone_hold", false],
    ["phone_typing", true],
    ["phone_putdown", false],
    ["knock_startle", false],
    ["wakeup_jolt", false]
  ];
  for (const [key, defaultLoop] of expectedSentinels) {
    assert.equal(C.vrmClip.SENTINEL_LOOP_DEFAULT[key], defaultLoop, `sentinel ${key} loop default`);
  }

  // Test all 7 sentinel state mappings and knock_startle / wakeup_jolt duration_s propagation.
  const coLead = C.createVrmCoLead({});
  coLead.setSentinelDurations({ knock_startle: 2.35, wakeup_jolt: 2.75 });

  // 1. idle
  let st = coLead.step({ dt: 0.05, elapsed: 1, expression: "idle" });
  assert.equal(st.activeSentinel, "idle");

  // 2. laptop_work
  st = coLead.step({ dt: 0.05, elapsed: 1.1, expression: "focused" });
  assert.equal(st.activeSentinel, "laptop_work");

  // 3-5. phone_hold, phone_typing, phone_putdown
  coLead.triggerGesture("phone_pickup");
  st = coLead.step({ dt: 0.05, elapsed: 1.2, expression: "idle" });
  assert.equal(st.activeSentinel, "phone_hold");

  coLead.triggerGesture("phone_typing");
  st = coLead.step({ dt: 0.05, elapsed: 1.3, expression: "idle" });
  assert.equal(st.activeSentinel, "phone_typing");

  coLead.triggerGesture("phone_putdown");
  st = coLead.step({ dt: 0.05, elapsed: 1.4, expression: "idle" });
  assert.equal(st.activeSentinel, "phone_putdown");

  // Clear putdown after it completes (> 0.72s).
  for (let i = 0; i < 16; i++) coLead.step({ dt: 0.06, elapsed: 1.5 + i * 0.06, expression: "idle" });

  // 6. knock_startle duration_s (2.35s > default 1.05s): still active at 1.8s, cleared after 2.5s.
  coLead.triggerKnock({ nowMs: 10000 });
  for (let i = 0; i < 18; i++) st = coLead.step({ dt: 0.1, elapsed: 3 + i * 0.1, expression: "idle" });
  assert.equal(st.activeSentinel, "knock_startle", "knock_startle must honor 2.35s sentinel duration_s past default 1.05s");
  for (let i = 0; i < 8; i++) st = coLead.step({ dt: 0.1, elapsed: 4.8 + i * 0.1, expression: "idle" });
  assert.notEqual(st.activeSentinel, "knock_startle", "knock_startle must end after 2.35s");

  // 7. wakeup_jolt duration_s (2.75s > default 1.35s): still active at 2.1s, cleared after 2.9s.
  coLead.triggerKnock({ nowMs: 20000, sleepy: true });
  for (let i = 0; i < 21; i++) st = coLead.step({ dt: 0.1, elapsed: 6 + i * 0.1, expression: "idle" });
  assert.equal(st.activeSentinel, "wakeup_jolt", "wakeup_jolt must honor 2.75s sentinel duration_s past default 1.35s");
  for (let i = 0; i < 10; i++) st = coLead.step({ dt: 0.1, elapsed: 8.1 + i * 0.1, expression: "idle" });
  assert.notEqual(st.activeSentinel, "wakeup_jolt", "wakeup_jolt must end after 2.75s");

  // Looping idle sentinel never marks one-shot active; one-shot action does.
  const loopPlayer = C.vrmClip.createClipPlayer();
  const dummyClip = { duration_s: 1.0, loop: true, keyframes: [{ t: 0, head: { yaw_deg: 0 } }] };
  loopPlayer.play("idle", dummyClip, { loop: true, isSentinel: true });
  loopPlayer.step(0.1);
  assert.equal(loopPlayer.isOneShotActive(), false, "looping idle sentinel must never mark active cadence");

  const oneShotPlayer = C.vrmClip.createClipPlayer();
  oneShotPlayer.play("wave_hi", { duration_s: 1.0, loop: false, keyframes: [{ t: 0, head: { yaw_deg: 0 } }] }, { loop: false, isSentinel: false });
  oneShotPlayer.step(0.1);
  assert.equal(oneShotPlayer.isOneShotActive(), true, "one-shot custom action marks active cadence");

  // Verify stage.js listens to BroadcastChannel("ws-companion-packs") and checks isClipOneShotActive().
  const stageSrc = fs.readFileSync(path.join(__dirname, "assets", "dashboard-companion-stage.js"), "utf8");
  assert.match(stageSrc, /BroadcastChannel\("ws-companion-packs"\)/, "stage.js listens to BroadcastChannel ws-companion-packs");
  assert.match(stageSrc, /isClipOneShotActive/, "stage.js uses isClipOneShotActive for idle cadence separation");
});
