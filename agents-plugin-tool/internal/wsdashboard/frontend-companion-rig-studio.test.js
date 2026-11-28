const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function loadRigStudio() {
  const context = vm.createContext({
    window: { WsCompanion: {} },
    URLSearchParams,
    console
  });
  for (const file of [
    "dashboard-companion-vrm-pose.js",
    "dashboard-companion-vrm-ik.js",
    "dashboard-companion-vrm-clip.js",
    "dashboard-companion-rig-studio.js"
  ]) {
    vm.runInContext(
      fs.readFileSync(path.join(__dirname, "assets", file), "utf8"),
      context,
      { filename: file }
    );
  }
  return context.window.WsCompanion.rigStudio;
}

test("rigStudio.formatActionsPreview matches Go formatActionsEnvelopeBody rules and caps", () => {
  const rs = loadRigStudio();

  // Empty or all-private -> (none)
  assert.equal(rs.formatActionsPreview([]).text, "(none)");
  assert.equal(rs.formatActionsPreview([
    { key: "secret_move", expose_to_agent: false, description: "private" }
  ]).text, "(none)");

  // Sanitization: collapse whitespace, [ ] -> ( ), <<< / >>> neutralized, 120-rune trim, tags appended
  const longDesc = "a".repeat(130);
  const res = rs.formatActionsPreview([
    {
      key: "phone_peek",
      expose_to_agent: true,
      description: "  Glance   at [phone] <<<IGNORE>>>  ",
      expression: "happy",
      prop: "smartphone",
      loop: true
    },
    {
      key: "long_act",
      expose_to_agent: true,
      description: longDesc
    }
  ]);
  assert.equal(res.exposedCount, 2);
  assert.equal(res.shownCount, 2);
  assert.equal(res.omittedCount, 0);
  assert.equal(
    res.lines[0].text,
    "- phone_peek: Glance at (phone) ‹‹‹IGNORE››› [expression: happy] [prop: smartphone] [loop]"
  );
  assert.equal(Array.from(res.lines[1].text).length, Array.from("- long_act: ").length + 120);
  assert.ok(res.lines[1].text.endsWith("…"));

  // 24-entry cap with (+N more not shown)
  const many = [];
  for (let i = 1; i <= 27; i++) {
    many.push({ key: `act_${i}`, expose_to_agent: true, description: `Action ${i}` });
  }
  const capped = rs.formatActionsPreview(many);
  assert.equal(capped.exposedCount, 27);
  assert.equal(capped.shownCount, 24);
  assert.equal(capped.omittedCount, 3);
  assert.equal(capped.lines[capped.lines.length - 1].text, "(+3 more not shown)");

  // 3200-rune budget drops whole lines cleanly
  const bulky = [];
  for (let i = 1; i <= 30; i++) {
    bulky.push({
      key: `bulky_action_${i}`,
      expose_to_agent: true,
      description: "가".repeat(120),
      expression: "happy",
      prop: "smartphone",
      loop: true
    });
  }
  const budgeted = rs.formatActionsPreview(bulky);
  assert.ok(budgeted.runeCount <= 3200, `runeCount ${budgeted.runeCount} exceeded 3200`);
  assert.ok(budgeted.omittedCount > 0);
  assert.match(budgeted.text, /\(\+\d+ more not shown\)$/);
});

test("rigStudio.addKeyframeSnapshot enforces 8-keyframe cap and strictly ascending timestamps", () => {
  const rs = loadRigStudio();
  const clip = rs.makeDefaultClip("wave_hi", { duration_s: 2.0 });
  assert.equal(clip.keyframes.length, 2);

  for (let i = 2; i < 8; i++) {
    const r = rs.addKeyframeSnapshot(clip, 0.5); // colliding timestamp should nudge
    assert.equal(r.ok, true, `add kf #${i + 1}`);
  }
  assert.equal(clip.keyframes.length, 8);
  for (let i = 1; i < clip.keyframes.length; i++) {
    assert.ok(clip.keyframes[i].t > clip.keyframes[i - 1].t, `strictly ascending t at ${i}`);
  }

  // 9th keyframe rejected
  const over = rs.addKeyframeSnapshot(clip, 1.5);
  assert.equal(over.ok, false);
  assert.equal(over.reason, "max_keyframes");
});

test("rigStudio.buildPutPayload preserves unknown fields and Stage 2 socket_handoff markers", () => {
  const rs = loadRigStudio();
  const item = {
    key: "coffee_sip",
    isSentinel: false,
    kind: "clip",
    description: "Sip coffee thoughtfully",
    expression: "relaxed",
    prop: "smartphone",
    expose_to_agent: true,
    loop: false,
    duration_s: 1.8,
    rawEntry: {
      key: "coffee_sip",
      kind: "clip",
      file: "clips/coffee_sip.clip.json",
      future_stage2_tag: "kept-intact",
      nested_ext: { version: 2 }
    },
    clip: {
      schema: "ws-companion-clip/v1",
      key: "coffee_sip",
      duration_s: 1.8,
      loop: false,
      easing: "ease_out_back",
      future_clip_meta: { author: "studio" },
      keyframes: [
        {
          t: 0.0,
          label: "grab",
          socket_handoff: { prop: "smartphone", to_socket: "hand_r" },
          custom_kf_ext: 42
        }
      ]
    }
  };

  const payload = JSON.parse(JSON.stringify(rs.buildPutPayload(item)));
  assert.equal(payload.entry.future_stage2_tag, "kept-intact");
  assert.deepEqual(payload.entry.nested_ext, { version: 2 });
  assert.equal(payload.entry.expose_to_agent, true);
  assert.equal(payload.clip.future_clip_meta.author, "studio");
  assert.deepEqual(payload.clip.keyframes[0].socket_handoff, { prop: "smartphone", to_socket: "hand_r" });
  assert.equal(payload.clip.keyframes[0].custom_kf_ext, 42);
});

test("rigStudio validates keys, descriptions, and pack authorability rules", () => {
  const rs = loadRigStudio();
  assert.equal(rs.validateActionKey("wave_hi_2").ok, true);
  assert.equal(rs.validateActionKey("Idle").ok, false);
  assert.equal(rs.validateActionKey("../evil").ok, false);
  assert.equal(rs.validateActionKey("con").ok, false);
  assert.equal(rs.validateActionKey("lpt1").ok, false);

  assert.equal(rs.validateDescription("Wave warmly at the user").ok, true);
  assert.equal(rs.validateDescription("a".repeat(121)).ok, false);
  assert.equal(rs.validateDescription("bad\u202Ebidi").ok, false);
  assert.equal(rs.validateDescription("bad\nnewline").ok, false);

  assert.equal(rs.isPackAuthorable({ id: "default", renderer: "2d", builtin: true }).ok, false);
  assert.equal(rs.isPackAuthorable({ id: "my2d", renderer: "2d" }).ok, false);
  assert.equal(rs.isPackAuthorable({ id: "rem", renderer: "vrm", origin: { kind: "git" } }).ok, false);
  assert.equal(rs.isPackAuthorable({ id: "local_vrm", renderer: "vrm", origin: { kind: "local" } }).ok, true);
  assert.equal(rs.isPackAuthorable({ id: "handmade_vrm", renderer: "vrm" }).ok, true);
});

test("Settings companion Open Studio button enables only for authorable local VRM packs", () => {
  const src = fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-companion.js"), "utf8");
  assert.match(src, /function studioEligibility\(pack\)/);
  assert.match(src, /\/companion\/studio\?pack=/);
  assert.match(src, /dataset\.companionOpenStudio/);
  assert.match(src, /open_studio_disabled_builtin/);
  assert.match(src, /open_studio_disabled_2d/);
  assert.match(src, /open_studio_disabled_remote/);
});

test("rigStudio.mergeManifestIntoItems preserves dirty drafts, dirty edits, and dirty calibration on .vrma import", () => {
  const rs = loadRigStudio();
  const items = new Map();
  const baseManifest = {
    sentinels: {
      idle: { kind: "clip", file: "clips/idle.clip.json", loop: true }
    },
    actions: [
      { key: "wave_hi", kind: "clip", file: "clips/wave_hi.clip.json", description: "Old wave", loop: false },
      { key: "clean_bow", kind: "clip", file: "clips/clean_bow.clip.json", description: "Old bow", loop: false }
    ],
    calibration: { props: { laptop: { offset: [0, 0, 0], rotation_deg: [0, 0, 0], scale: 1 } } }
  };
  let cal = rs.mergeManifestIntoItems(items, baseManifest, { preserveDirty: false });
  assert.equal(cal.calibrationDirty, false);

  // User creates an unsaved draft, modifies wave_hi, and modifies prop calibration.
  const draftClip = rs.makeDefaultClip("unsaved_draft");
  items.set("unsaved_draft", {
    key: "unsaved_draft", isSentinel: false, saved: false, kind: "clip",
    file: "clips/unsaved_draft.clip.json", description: "My WIP", dirty: true, clip: draftClip
  });
  const dirtyWave = items.get("wave_hi");
  dirtyWave.description = "Edited wave";
  dirtyWave.dirty = true;
  const dirtyCal = { props: { laptop: { offset: [0.1, -0.05, 0], rotation_deg: [0, 10, 0], scale: 1.25 } } };

  // Pack reloads after importing dance.vrma.
  const nextManifest = {
    ...baseManifest,
    actions: [
      { key: "wave_hi", kind: "clip", file: "clips/wave_hi.clip.json", description: "Old wave", loop: false },
      { key: "clean_bow", kind: "clip", file: "clips/clean_bow.clip.json", description: "Updated bow", loop: false },
      { key: "dance", kind: "vrma", file: "clips/dance.vrma", loop: false }
    ]
  };
  cal = rs.mergeManifestIntoItems(items, nextManifest, {
    preserveDirty: true,
    replaceKey: "dance",
    calibration: dirtyCal,
    calibrationDirty: true
  });

  assert.equal(items.has("unsaved_draft"), true, "unsaved dirty draft must be preserved");
  assert.equal(items.get("unsaved_draft").clip, draftClip);
  assert.equal(items.get("wave_hi").description, "Edited wave", "dirty saved clip edits must be preserved");
  assert.equal(items.get("wave_hi").dirty, true);
  assert.equal(items.get("clean_bow").description, "Updated bow", "non-dirty entry should refresh from manifest");
  assert.equal(items.get("dance").kind, "vrma", "newly imported VRMA entry must be merged");
  assert.equal(cal.calibrationDirty, true, "dirty calibration flag must be preserved");
  assert.deepEqual(cal.calibration, dirtyCal, "dirty calibration values must be preserved");

  // Replacing an explicitly targeted key overwrites even if dirty.
  const replacedManifest = {
    ...nextManifest,
    actions: [
      { key: "wave_hi", kind: "vrma", file: "clips/wave_hi.vrma", loop: false },
      { key: "clean_bow", kind: "clip", file: "clips/clean_bow.clip.json", description: "Updated bow", loop: false },
      { key: "dance", kind: "vrma", file: "clips/dance.vrma", loop: false }
    ]
  };
  rs.mergeManifestIntoItems(items, replacedManifest, {
    preserveDirty: true,
    replaceKey: "wave_hi",
    calibration: dirtyCal,
    calibrationDirty: true
  });
  assert.equal(items.get("wave_hi").kind, "vrma", "explicitly replaced import key updates to VRMA");
  assert.equal(items.get("wave_hi").dirty, false);

  const studioSrc = fs.readFileSync(path.join(__dirname, "assets", "dashboard-companion-rig-studio.js"), "utf8");
  assert.match(studioSrc, /reloadPackCatalogOnly\(key,\s*\{\s*preserveDirty:\s*true,\s*replaceKey:\s*key\s*\}\)/);
  assert.match(studioSrc, /addEventListener\("pointercancel",\s*stopScrub\)/);
  assert.match(studioSrc, /stage\?\.setVrmaLoop\?\.\(item\.loop\)/);
});

test("rigStudio.rescaleClipDuration keeps timestamps strictly ascending within [0, newDur] across DUR shrinks", () => {
  const rs = loadRigStudio();
  const clip = {
    schema: "ws-companion-clip/v1",
    key: "tight_kfs",
    duration_s: 3.0,
    loop: false,
    keyframes: [
      { t: 0.0 },
      { t: 0.01 },
      { t: 0.02 },
      { t: 1.5 },
      { t: 2.97 },
      { t: 2.98 },
      { t: 2.99 },
      { t: 3.0 }
    ]
  };

  rs.rescaleClipDuration(clip, 0.8);
  assert.equal(clip.duration_s, 0.8);
  for (let i = 0; i < clip.keyframes.length; i++) {
    assert.ok(clip.keyframes[i].t >= 0 && clip.keyframes[i].t <= 0.8, `t[${i}]=${clip.keyframes[i].t} in [0, 0.8]`);
    if (i > 0) {
      assert.ok(clip.keyframes[i].t > clip.keyframes[i - 1].t, `strictly ascending at ${i}: ${clip.keyframes[i - 1].t} < ${clip.keyframes[i].t}`);
    }
  }

  // Even shrinking to minimum 0.1s with 8 keyframes must stay strictly ascending and <= 0.1.
  rs.rescaleClipDuration(clip, 0.1);
  assert.equal(clip.duration_s, 0.1);
  for (let i = 0; i < clip.keyframes.length; i++) {
    assert.ok(clip.keyframes[i].t >= 0 && clip.keyframes[i].t <= 0.1, `t[${i}]=${clip.keyframes[i].t} in [0, 0.1]`);
    if (i > 0) {
      assert.ok(clip.keyframes[i].t > clip.keyframes[i - 1].t, `strictly ascending at ${i} for 0.1s`);
    }
  }
});

test("rigStudioStage guards VRMA async race, advances VRMA playhead, auto-stops non-looping VRMA, supports live setVrmaLoop, and disposes cancelled loadVrm", async () => {
  const LoopOnce = 2200;
  const LoopRepeat = 2201;
  let nowMs = 0;
  const rafQueue = [];
  let nextRafId = 1;
  const deepDisposed = [];
  const parseResolvers = [];

  class Vector3 {
    constructor(x = 0, y = 0, z = 0) { this.x = x; this.y = y; this.z = z; }
    set(x, y, z) { this.x = x; this.y = y; this.z = z; return this; }
    copy(v) { this.x = v.x; this.y = v.y; this.z = v.z; return this; }
    project() { return this; }
  }
  class Vector2 { set() { return this; } }
  class Object3D {
    constructor(name = "") {
      this.name = name;
      this.position = new Vector3();
      this.rotation = { x: 0, y: 0, z: 0, order: "XYZ", set(x, y, z, o) { this.x = x; this.y = y; this.z = z; if (o) this.order = o; } };
      this.scale = new Vector3(1, 1, 1);
      this.children = [];
      this.visible = true;
      this.userData = {};
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
  const createdActions = [];
  class AnimationMixer {
    constructor() { this._actions = []; }
    clipAction(clip) {
      const act = {
        _clip: clip,
        time: 0,
        loop: LoopRepeat,
        repetitions: Infinity,
        clampWhenFinished: false,
        paused: false,
        _playing: false,
        setLoop(mode, reps) { this.loop = mode; this.repetitions = reps; return this; },
        reset() { this.time = 0; this.paused = false; return this; },
        play() { this._playing = true; return this; },
        stop() { this._playing = false; return this; },
        isRunning() { return this._playing && !this.paused; }
      };
      this._actions.push(act);
      createdActions.push(act);
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
    uncacheRoot() {}
  }
  class GLTFLoader {
    register() {}
    parseAsync(buf) {
      const tag = new TextDecoder().decode(new Uint8Array(buf));
      return new Promise(resolve => {
        parseResolvers.push({
          tag,
          resolve: () => {
            if (tag.startsWith("VRM:")) {
              const vrmScene = new Object3D(tag);
              const bones = {
                hips: new Object3D(), chest: new Object3D(), head: new Object3D(),
                leftUpperArm: new Object3D(), rightUpperArm: new Object3D()
              };
              bones.chest.position.set(0, 1.15, 0);
              bones.head.position.set(0, 1.48, 0);
              resolve({
                scene: vrmScene,
                userData: {
                  vrm: {
                    meta: { metaVersion: "1" },
                    scene: vrmScene,
                    humanoid: { getNormalizedBoneNode: n => bones[n] || null },
                    expressionManager: { expressions: [], setValue() {} },
                    lookAt: {},
                    update() {}
                  }
                }
              });
            } else {
              resolve({ userData: { vrmAnimations: [{ name: tag, duration: 0.3 }] } });
            }
          }
        });
      });
    }
  }

  const fakeRuntime = {
    THREE: {
      LoopOnce, LoopRepeat, Vector3, Vector2, Object3D, Scene: Object3D, Group: Object3D, Box3,
      PerspectiveCamera: class extends Object3D { constructor() { super(); this.fov = 26; this.aspect = 1; } updateProjectionMatrix() {} },
      WebGLRenderer: class { constructor() { this.shadowMap = {}; } setClearColor() {} setPixelRatio() {} setSize() {} render() {} dispose() {} },
      OctahedronGeometry: class { dispose() {} }, BoxGeometry: class { dispose() {} }, SphereGeometry: class { dispose() {} },
      MeshBasicMaterial: class { dispose() {} },
      Mesh: class extends Object3D {},
      Raycaster: class { setFromCamera() {} intersectObjects() { return []; } },
      AnimationMixer,
      SRGBColorSpace: 0, NoToneMapping: 0
    },
    OrbitControls: class { constructor() { this.target = new Vector3(); } addEventListener() {} update() {} dispose() {} },
    TransformControls: class extends Object3D { setMode() {} setSize() {} addEventListener() {} attach() {} detach() {} dispose() {} },
    GLTFLoader,
    VRMLoaderPlugin: class {},
    VRMAnimationLoaderPlugin: class {},
    VRMUtils: {
      removeUnnecessaryVertices() {}, combineSkeletons() {}, rotateVRM0() {},
      deepDispose(scene) { deepDisposed.push(scene?.name || "scene"); }
    },
    createVRMAnimationClip(anim) { return { name: anim.name, duration: anim.duration || 0.3 }; }
  };

  const context = vm.createContext({
    window: {
      devicePixelRatio: 1,
      WsCompanion: {
        vrmRuntimeLoader: async () => fakeRuntime,
        vrmStudio: {
          createStudioLighting: () => ({ applyTheme() {}, attachPropEnvironment() {}, dispose() {} }),
          solveBustFraming: () => ({ targetY: 1.35, frameHeight: 0.9, headroomRatio: 0.12, dist: 1.8, limitedBy: "height" })
        }
      }
    },
    document: {
      hidden: false,
      addEventListener() {}, removeEventListener() {},
      createDocumentFragment: () => ({ appendChild() {} }),
      createElementNS: () => ({ setAttribute() {}, textContent: "" })
    },
    performance: { now: () => nowMs },
    requestAnimationFrame: fn => { const id = nextRafId++; rafQueue.push({ id, fn }); return id; },
    cancelAnimationFrame: id => { const i = rafQueue.findIndex(r => r.id === id); if (i >= 0) rafQueue.splice(i, 1); },
    fetch: async url => ({ ok: true, arrayBuffer: async () => new TextEncoder().encode("VRM:" + url).buffer }),
    TextEncoder, TextDecoder, URLSearchParams, console
  });

  vm.runInContext(
    fs.readFileSync(path.join(__dirname, "assets", "dashboard-companion-rig-studio-stage.js"), "utf8"),
    context,
    { filename: "dashboard-companion-rig-studio-stage.js" }
  );

  const playSteps = [];
  const makeNode = () => ({
    addEventListener() {}, removeEventListener() {},
    setAttribute() {}, replaceChildren() {},
    getBoundingClientRect: () => ({ width: 640, height: 480 })
  });
  const stage = context.window.WsCompanion.rigStudioStage.createStage({
    canvas: makeNode(), overlaySvg: makeNode(), viewportEl: makeNode(),
    getSpeed: () => 1.0,
    onPlayStep: (t, finished, kind) => playSteps.push({ t, finished, kind })
  });

  // Minor #2: Cancelled loadVrm disposes its parsed scene.
  const pVrm1 = stage.loadVrm("vrm1");
  await new Promise(r => setTimeout(r, 0));
  const pVrm2 = stage.loadVrm("vrm2");
  await new Promise(r => setTimeout(r, 0));
  // Resolve vrm2 first, then stale vrm1.
  parseResolvers.find(r => r.tag === "VRM:vrm2").resolve();
  assert.ok(await pVrm2);
  parseResolvers.find(r => r.tag === "VRM:vrm1").resolve();
  assert.equal(await pVrm1, null);
  assert.ok(deepDisposed.includes("VRM:vrm1"), "cancelled loadVrm must deepDispose its parsed scene");

  // Defect #5: Stale loadVrmaBuffer aborted when stopVrma() runs before parse finishes.
  const pStaleVrma = stage.loadVrmaBuffer(new TextEncoder().encode("STALE_VRMA").buffer, false);
  stage.stopVrma();
  parseResolvers.find(r => r.tag === "STALE_VRMA").resolve();
  assert.equal(await pStaleVrma, null, "aborted loadVrmaBuffer must return null");
  assert.equal(stage.getDebugState().playingKind, "clip", "stale VRMA must not flip playingKind to vrma");
  assert.equal(stage.getDebugState().vrmaLoaded, false, "stale VRMA must not mount over clip");

  // Defect #6: Non-looping VRMA advances playhead via onPlayStep(t, finished), auto-stops and halts rAF.
  const pLiveVrma = stage.loadVrmaBuffer(new TextEncoder().encode("LIVE_VRMA").buffer, false);
  parseResolvers.find(r => r.tag === "LIVE_VRMA").resolve();
  const info = await pLiveVrma;
  assert.equal(info.duration_s, 0.3);
  assert.equal(stage.getDebugState().vrmaLoop, false);

  // Drain any pending idle rAF naturally so stage settles to rafActive=false, then start VRMA playback.
  nowMs += 5000;
  while (rafQueue.length > 0) {
    const job = rafQueue.shift();
    job.fn(nowMs);
  }
  assert.equal(stage.getDebugState().rafActive, false);
  stage.setPlaying(true, "vrma");
  assert.equal(stage.getDebugState().playing, true);
  assert.equal(rafQueue.length, 1);

  // Step 4 frames of 100ms each (0.1 -> 0.2 -> 0.3 finished).
  for (let i = 0; i < 4 && rafQueue.length > 0; i++) {
    nowMs += 100;
    const job = rafQueue.shift();
    job.fn(nowMs);
  }
  assert.ok(playSteps.length >= 3, `expected >= 3 VRMA play steps, got ${playSteps.length}`);
  assert.ok(playSteps[0].t > 0 && playSteps[0].finished === false, "playhead advances mid-clip");
  const lastStep = playSteps[playSteps.length - 1];
  assert.equal(lastStep.finished, true, "final step reports finished=true");
  assert.equal(lastStep.t, 0.3, "final step clamps at duration");
  assert.equal(stage.getDebugState().playing, false, "non-looping VRMA auto-stops at end");
  assert.equal(stage.getDebugState().rafActive, false, "rAF stops spinning once non-looping VRMA finishes");
  assert.equal(rafQueue.length, 0, "no trailing rAF queued after non-looping VRMA finishes");

  // Live Loop toggle updates active VRMA action immediately.
  const activeAct = createdActions[createdActions.length - 1];
  stage.setVrmaLoop(true);
  assert.equal(activeAct.loop, LoopRepeat);
  assert.equal(activeAct.clampWhenFinished, false);
  assert.equal(stage.getDebugState().vrmaLoop, true);
  stage.setVrmaLoop(false);
  assert.equal(activeAct.loop, LoopOnce);
  assert.equal(activeAct.clampWhenFinished, true);
  assert.equal(stage.getDebugState().vrmaLoop, false);

  stage.dispose();
});
