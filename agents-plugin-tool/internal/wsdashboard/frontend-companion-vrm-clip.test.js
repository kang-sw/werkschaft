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
    "vrm-colead", "vrm-speech", "vrm", "stage"
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

test("vrmClip one-shot fades out without re-triggering from t=0 and loading/failed clips stop prior clip", async () => {
  const { C } = loadVrmStack();
  const oneShotClip = {
    schema: "ws-companion-clip/v1",
    key: "wave_hi",
    duration_s: 0.3,
    loop: false,
    keyframes: [
      { t: 0, head: { yaw_deg: 0 } },
      { t: 0.3, head: { yaw_deg: 24 } }
    ]
  };
  const idleClip = {
    schema: "ws-companion-clip/v1",
    key: "idle",
    duration_s: 1.0,
    loop: true,
    keyframes: [{ t: 0, head: { yaw_deg: 5 } }]
  };

  // 1. Direct player: calling play() again while fadingOut or after completion must NOT restart from t=0.
  const player = C.vrmClip.createClipPlayer();
  assert.equal(player.play("wave_hi", oneShotClip, { loop: false, immediate: true, token: 1 }), true);
  player.step(0.15);
  player.step(0.16); // crosses 0.3s -> done=true, fadingOut=true
  let st = player.getState();
  assert.ok(st && st.fadingOut === true, "one-shot enters fadingOut after duration");
  const fadeStartWeight = st.weight;
  const fadeStartTime = st.time;

  // Repeated play() calls with same token during fadeout must not reset time or clear fadingOut.
  assert.equal(player.play("wave_hi", oneShotClip, { loop: false, token: 1 }), false);
  const midFade = player.step(0.08);
  assert.ok(midFade && midFade.weight < fadeStartWeight, "weight must decrease during fadeout");
  assert.ok(player.getState().time > fadeStartTime, "time must not reset to 0 during fadeout");
  assert.equal(player.getState().fadingOut, true, "fadingOut must stay true");

  // Step until completely faded out.
  for (let i = 0; i < 6; i++) {
    player.play("wave_hi", oneShotClip, { loop: false, token: 1 });
    player.step(0.08);
  }
  assert.equal(player.getState(), null, "one-shot finishes fading out to null");

  // Subsequent play() calls with the same token stay completed.
  assert.equal(player.play("wave_hi", oneShotClip, { loop: false, token: 1 }), false);
  assert.equal(player.getState(), null, "completed one-shot must not restart on same token");

  // A new activation token restarts cleanly from t=0.
  assert.equal(player.play("wave_hi", oneShotClip, { loop: false, token: 2, immediate: true }), true);
  assert.equal(player.getState().time, 0);
  assert.equal(player.getState().fadingOut, false);

  // 2. Pack clip runtime: sync() every frame lets one-shot fade out, and stops prior clip while a new clip loads/fails.
  let resolveSlow = null;
  C.fetchPackClip = (_pack, file) => {
    if (file === "clips/idle.clip.json") return Promise.resolve({ json: idleClip });
    if (file === "clips/wave_hi.clip.json") return Promise.resolve({ json: oneShotClip });
    if (file === "clips/slow.clip.json") return new Promise(res => { resolveSlow = res; });
    return Promise.reject(new Error("404"));
  };
  const pack = {
    sentinels: { idle: { kind: "clip", file: "clips/idle.clip.json", loop: true } },
    actionMap: {
      wave_hi: { key: "wave_hi", kind: "clip", file: "clips/wave_hi.clip.json", loop: false, duration_s: 0.3 },
      slow: { key: "slow", kind: "clip", file: "clips/slow.clip.json", loop: false },
      broken: { key: "broken", kind: "clip", file: "clips/broken.clip.json", loop: false }
    }
  };
  const rt = C.vrmClip.createPackClipRuntime(pack, new AbortController().signal);
  rt.sync("wave_hi", "idle");
  await Promise.resolve();
  rt.sync("wave_hi", "idle");
  assert.equal(rt.getState().key, "wave_hi");
  // Advance past 0.3s + 0.22s fadeout while calling sync("wave_hi", "idle") every frame.
  let sawFadeOut = false;
  let lastWeight = 0;
  for (let i = 0; i < 12; i++) {
    rt.sync("wave_hi", "idle");
    const stepped = rt.step(0.08);
    const cur = rt.getState();
    if (cur?.fadingOut) {
      if (sawFadeOut) assert.ok(cur.weight <= lastWeight, "fadeout weight must stay monotonic");
      sawFadeOut = true;
      lastWeight = cur.weight;
    }
    void stepped;
  }
  assert.equal(sawFadeOut, true, "one-shot clip entered fadeout via sync()");
  assert.equal(rt.getState(), null, "one-shot clip faded out completely without restarting");

  // Re-trigger via clearing action then requesting wave_hi again.
  rt.sync("", "idle");
  await Promise.resolve();
  rt.sync("", "idle");
  rt.step(0.05);
  assert.equal(rt.getState().key, "idle");

  // Requesting a loading clip ("slow") must immediately stop prior clip ("idle").
  rt.sync("slow", "idle");
  assert.equal(rt.getState().fadingOut, true, "prior clip must fade out while next clip is loading");
  for (let i = 0; i < 6; i++) { rt.sync("slow", "idle"); rt.step(0.08); }
  assert.equal(rt.getState(), null, "prior clip finishes fading out while slow clip is still loading");
  if (resolveSlow) resolveSlow(null);

  // Requesting a failed clip ("broken") must also stop prior clip ("idle").
  rt.sync("", "idle");
  rt.step(0.05);
  assert.equal(rt.getState().key, "idle");
  rt.sync("broken", "idle");
  await Promise.resolve();
  assert.equal(rt.getState().fadingOut, true, "prior clip must fade out when target clip fails");
  rt.dispose();
});

test("vrm runtime fixes VRMA cached action loop leakage, async fetch race, and finished LoopOnce isClipOneShotActive", async () => {
  const { C } = loadVrmStack();
  const LoopOnce = 2200;
  const LoopRepeat = 2201;

  const actionsByClip = new Map();
  let lastAction = null;
  let mixerActions = [];

  class Vector3 {
    constructor(x = 0, y = 0, z = 0) { this.x = x; this.y = y; this.z = z; }
    set(x, y, z) { this.x = x; this.y = y; this.z = z; return this; }
    copy(v) { this.x = v.x; this.y = v.y; this.z = v.z; return this; }
    clone() { return new Vector3(this.x, this.y, this.z); }
    project() { return this; }
  }
  class Quaternion {
    constructor(x = 0, y = 0, z = 0, w = 1) { this.x = x; this.y = y; this.z = z; this.w = w; }
    set(x, y, z, w) { this.x = x; this.y = y; this.z = z; this.w = w; return this; }
    slerp() { return this; }
  }
  class Object3D {
    constructor() {
      this.position = new Vector3();
      this.rotation = { x: 0, y: 0, z: 0, order: "XYZ", set(x, y, z, o) { this.x = x; this.y = y; this.z = z; if (o) this.order = o; } };
      this.quaternion = new Quaternion();
      this.scale = new Vector3(1, 1, 1);
      this.children = [];
      this.visible = true;
    }
    add(...items) { this.children.push(...items); }
    remove(item) { this.children = this.children.filter(c => c !== item); }
    traverse(fn) { fn(this); this.children.forEach(c => c.traverse?.(fn)); }
    updateMatrixWorld() {}
    getWorldPosition(t) { return t.copy(this.position); }
  }
  class Box3 {
    constructor() { this.min = new Vector3(0, 0, 0); this.max = new Vector3(0, 1.6, 0); }
    isEmpty() { return false; }
    union() { return this; }
  }
  class AnimationMixer {
    constructor() { this._actions = []; }
    clipAction(clip) {
      if (actionsByClip.has(clip)) return actionsByClip.get(clip);
      const act = {
        _clip: clip,
        time: 0,
        loop: LoopRepeat,
        repetitions: Infinity,
        clampWhenFinished: false,
        paused: false,
        _playing: false,
        getClip() { return clip; },
        setLoop(mode, reps) { this.loop = mode; this.repetitions = reps; return this; },
        reset() { this.time = 0; this.paused = false; return this; },
        fadeIn() { return this; },
        fadeOut() { return this; },
        play() { this._playing = true; this.paused = false; return this; },
        stop() { this._playing = false; return this; },
        crossFadeTo() { return this; },
        isRunning() { return this._playing && !this.paused; }
      };
      actionsByClip.set(clip, act);
      this._actions.push(act);
      mixerActions.push(act);
      return act;
    }
    update(dt) {
      for (const act of this._actions) {
        if (!act._playing || act.paused) continue;
        act.time += dt;
        const dur = act._clip?.duration || 1;
        if (act.loop === LoopOnce && act.time >= dur) {
          act.time = dur;
          if (act.clampWhenFinished) act.paused = true;
          else act._playing = false;
        }
      }
    }
    stopAllAction() { for (const a of this._actions) a.stop(); }
  }
  class WebGLRenderer {
    constructor() { this.shadowMap = { enabled: false }; this.info = { render: { calls: 1, triangles: 100 } }; }
    setClearColor() {}
    setPixelRatio() {}
    setSize() {}
    setViewport() {}
    setScissor() {}
    setScissorTest() {}
    clear() {}
    render() {}
    dispose() {}
    forceContextLoss() {}
  }

  const bones = {
    hips: new Object3D(), spine: new Object3D(), chest: new Object3D(), head: new Object3D(),
    leftUpperArm: new Object3D(), leftLowerArm: new Object3D(), leftHand: new Object3D(),
    rightUpperArm: new Object3D(), rightLowerArm: new Object3D(), rightHand: new Object3D()
  };
  bones.chest.position.set(0, 1.15, 0);
  bones.head.position.set(0, 1.48, 0);
  const parsedClips = new Map();
  class GLTFLoader {
    register() {}
    async parseAsync(buf) {
      const tag = new TextDecoder().decode(new Uint8Array(buf));
      if (tag === "VRM") {
        return {
          scene: new Object3D(),
          userData: {
            vrm: {
              meta: { metaVersion: "1" },
              scene: new Object3D(),
              humanoid: { getNormalizedBoneNode: n => bones[n] || null },
              expressionManager: { expressions: [], setValue() {} },
              lookAt: {},
              update() {}
            }
          }
        };
      }
      return { userData: { vrmAnimations: [{ name: tag }] } };
    }
  }
  const fakeRuntime = {
    THREE: {
      LoopOnce, LoopRepeat, Vector3, Quaternion, Object3D, Scene: Object3D, Group: Object3D,
      PerspectiveCamera: class extends Object3D { constructor() { super(); this.fov = 26; this.aspect = 1; } lookAt() {} updateProjectionMatrix() {} },
      DirectionalLight: Object3D, HemisphereLight: Object3D, PointLight: Object3D,
      Box3, AnimationMixer, WebGLRenderer, SRGBColorSpace: 0, NoToneMapping: 0
    },
    GLTFLoader,
    VRMLoaderPlugin: class {},
    VRMAnimationLoaderPlugin: class {},
    VRMUtils: { removeUnnecessaryVertices() {}, combineSkeletons() {}, rotateVRM0() {}, deepDispose() {} },
    createVRMAnimationClip(anim) {
      if (!parsedClips.has(anim.name)) parsedClips.set(anim.name, { name: anim.name, duration: 0.4 });
      return parsedClips.get(anim.name);
    }
  };

  let resolveSlowVrma = null;
  const enc = s => new TextEncoder().encode(s).buffer;
  C.vrmRuntimeLoader = async () => fakeRuntime;
  C.fetchPackAsset = async () => ({ buffer: enc("VRM") });
  C.fetchPackClip = (_pack, file) => {
    if (file === "clips/shared.vrma") return Promise.resolve({ buffer: enc("SHARED") });
    if (file === "clips/slow_race.vrma") return new Promise(res => { resolveSlowVrma = () => res({ buffer: enc("SLOW") }); });
    return Promise.reject(new Error("missing"));
  };

  const makeEl = () => ({
    className: "", hidden: false, textContent: "", width: 240, height: 280, dataset: {},
    style: { setProperty() {}, removeProperty() {} },
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    setAttribute() {}, removeAttribute() {}, getAttribute() { return ""; },
    addEventListener() {}, removeEventListener() {}, append() {}, replaceChildren() {},
    getBoundingClientRect: () => ({ left: 0, top: 0, width: 240, height: 280 }),
    getContext: () => null,
    closest: () => null
  });
  const doc = {
    createElement: () => makeEl(),
    documentElement: { dataset: {}, getAttribute: () => "" },
    addEventListener() {}, removeEventListener() {}
  };
  const container = makeEl();
  container.ownerDocument = doc;

  const pack = {
    id: "vrm-test",
    renderer: "vrm",
    model: "model.vrm",
    sentinels: {
      idle: { kind: "vrma", file: "clips/shared.vrma", loop: true }
    },
    actionMap: {
      oneshot_act: { key: "oneshot_act", kind: "vrma", file: "clips/shared.vrma", loop: false },
      loop_act: { key: "loop_act", kind: "vrma", file: "clips/shared.vrma", loop: true },
      slow_race: { key: "slow_race", kind: "vrma", file: "clips/slow_race.vrma", loop: false }
    }
  };

  const rig = C.makeVrmRig(pack, {});
  await rig.mount(container, { width: 240, height: 280 });
  let simSec = 1.0;
  const tick = (viewObj, stepSec) => {
    simSec += stepSec;
    viewObj.render(simSec, { x: 0, y: 0 }, null, null);
  };

  // (a) Play oneshot_act (LoopOnce), verify isClipOneShotActive() === true while running and false once finished.
  const vOne = rig.view("stand", "oneshot_act", "idle", []);
  tick(vOne, 0.016);
  await new Promise(r => setTimeout(r, 10));
  tick(vOne, 0.05);
  assert.equal(rig.debugState().activeVrmaKey, "oneshot_act");
  lastAction = actionsByClip.get(parsedClips.get("SHARED"));
  assert.equal(lastAction.loop, LoopOnce);
  assert.equal(lastAction.clampWhenFinished, true);
  assert.equal(rig.isClipOneShotActive(), true, "LoopOnce VRMA active before duration");

  // Advance past 0.4s duration in bounded frames so LoopOnce clamps and pauses.
  for (let i = 0; i < 10; i++) tick(vOne, 0.05);
  assert.equal(rig.isClipOneShotActive(), false, "finished LoopOnce VRMA must clear isClipOneShotActive()");

  // Switch to loop_act reusing the SAME cached clip & AnimationAction -> must reset LoopRepeat & clampWhenFinished=false.
  const vLoop = rig.view("stand", "loop_act", "idle", []);
  tick(vLoop, 0.016);
  assert.equal(rig.debugState().activeVrmaKey, "loop_act");
  assert.equal(lastAction.loop, LoopRepeat, "reused cached VRMA action must reset to LoopRepeat");
  assert.equal(lastAction.clampWhenFinished, false, "reused cached VRMA action must reset clampWhenFinished");

  // (b) Async fetch race: request slow_race, then switch to loop_act before slow_race resolves.
  const vSlow = rig.view("stand", "slow_race", "idle", []);
  tick(vSlow, 0.016);
  assert.ok(typeof resolveSlowVrma === "function", "slow_race fetch started");
  const vLoop2 = rig.view("stand", "loop_act", "idle", []);
  tick(vLoop2, 0.016);
  assert.equal(rig.debugState().activeVrmaKey, "loop_act");

  resolveSlowVrma();
  await new Promise(r => setTimeout(r, 10));
  assert.equal(rig.debugState().activeVrmaKey, "loop_act", "late-resolving superseded VRMA must not hijack playback");

  rig.dispose();
});

test("vrmCoLeadProps calibration scale upper clamp matches backend 4.0 limit", () => {
  const { C } = loadVrmStack();
  const props = C.vrmCoLeadProps.createCoLeadProps({});
  props.setCalibration({ props: { laptop: { scale: 3.8 }, smartphone: { scale: 5.5 } } });
  const cal = props.getPropCalibration();
  assert.equal(cal.laptop.scale, 3.8, "scale 3.8 must not be clamped to 3.0");
  assert.equal(cal.smartphone.scale, 4.0, "scale > 4.0 must clamp at 4.0");
});
