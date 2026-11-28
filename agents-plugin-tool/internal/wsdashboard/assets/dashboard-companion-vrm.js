(function (C) {
  "use strict";

  // ============================================================================
  // Pure-VRM Companion Renderer Adapter (Ground-Zero Baseline)
  // ============================================================================
  // Mirrors the 2D rig seam consumed by dashboard-companion-stage.js and adds
  // only the WebGL/VRM lifecycle hooks required by the outer shell:
  //
  //   Properties:
  //     renderer: "vrm"
  //     width:    number (logical bust width, default 240)
  //     height:   number (logical bust height, default 280)
  //
  //   Stage-driven seam (identical to 2D C.makeRig):
  //     view(pose, gesture, expression, props) -> {
  //       pose, gesture, expression, props,
  //       handAnchor: [x, y], articulatedDrag: boolean, seat: number,
  //       bounds: [0, 0, 1, 1],
  //       render(seconds, gaze, physics, extra) -> void
  //     }
  //     prop(id, registry) -> { data, drawable: false, draw() {} }
  //     setPreset(id)      -> void (no-op for VRM)
  //     setWardrobe(next)  -> void (no-op for VRM)
  //     wardrobe()         -> null
  //
  //   VRM lifecycle & gesture extension seam:
  //     mount(container, options?) -> Promise<void>
  //     setGaze({ x, y })          -> void
  //     resize(width, height, dpr) -> void
  //     pause()                    -> void
  //     resume()                   -> void
  //     dispose()                  -> void
  //     resolveScenario(id, beat)  -> { id, mode, pose, gesture, expression, prop, stage }
  //     debugState()               -> read-only telemetry snapshot
  //     C.registerVrmGesture(name, handler) / C.unregisterVrmGesture(name)
  // ============================================================================

  const DEG = Math.PI / 180;
  const BASE_FOV = 26;
  const LOGICAL_W = 240;
  const LOGICAL_H = 280;
  const ROAM_AABB_PADDING = 32;
  const DEFAULT_BLEED_PADDING = Object.freeze({ top: 48, right: 48, bottom: 24, left: 24 });

  const VRM_EXPRESSIONS = Object.freeze({
    idle: {},
    idle_doze: {},
    focused: {},
    half_lid: {},
    oo: {},
    cry: {},
    happy: {},
    wink: {},
    merong: {},
    startled: {},
    annoyed: {}
  });

  // Built-in Co-Lead procedural gestures & props (mirrored in companion_director.go).
  const BUILTIN_VRM_GESTURES = Object.freeze({
    knock_startle: {},
    annoyed_tap: {},
    wakeup_jolt: {},
    phone_buzz: {},
    phone_pickup: {},
    phone_typing: {},
    phone_putdown: {},
    phone_merong_facedown: {}
  });
  const BUILTIN_VRM_PROPS = Object.freeze({
    smartphone: {}
  });

  // Closed-eye and expressive states where automatic blinking is suppressed.
  const BLINK_SUPPRESSED = new Set(["idle_doze", "half_lid", "cry", "happy", "oo", "focused", "wink", "merong", "startled", "annoyed"]);

  // Clean gesture extension registry: starts empty at ground-zero so future
  // cycles can register VRM gestures one by one cleanly alongside .vrma clips.
  const vrmGestures = new Map();

  function registerVrmGesture(name, handler) {
    if (typeof name !== "string" || !name) return false;
    if (typeof handler !== "function" && !(handler && typeof handler.update === "function")) return false;
    vrmGestures.set(name, handler);
    return true;
  }

  function unregisterVrmGesture(name) {
    return vrmGestures.delete(name);
  }

  function vrmVocabulary(pack) {
    const gestures = Object.create(null);
    for (const key of Object.keys(BUILTIN_VRM_GESTURES)) gestures[key] = {};
    for (const key of vrmGestures.keys()) gestures[key] = {};
    if (pack?.animations && typeof pack.animations === "object") {
      for (const key of Object.keys(pack.animations)) gestures[key] = {};
    }
    return {
      expressions: VRM_EXPRESSIONS,
      gestures,
      props: BUILTIN_VRM_PROPS
    };
  }

  // Conjugate normalized humanoid rotations by R_y(pi) for VRM 0.x models
  // (after VRMUtils.rotateVRM0) so 0.x and 1.0 produce identical world poses.
  function conjugateVrm0Euler(x, y, z, isVrm0) {
    return isVrm0 ? [-x, y, -z] : [x, y, z];
  }

  function conjugateVrm0Quat(q, isVrm0) {
    if (isVrm0) q.set(-q.x, q.y, -q.z, q.w);
    return q;
  }

  let runtimePromise = null;
  function loadVrmRuntime() {
    if (typeof C.vrmRuntimeLoader === "function") return C.vrmRuntimeLoader();
    if (!runtimePromise) {
      const url = typeof window.wsCompanionVrmRuntime === "string" && window.wsCompanionVrmRuntime
        ? window.wsCompanionVrmRuntime : "";
      if (!url) return Promise.reject(new Error("VRM runtime URL is not configured"));
      runtimePromise = import(url).catch(err => {
        runtimePromise = null;
        throw err;
      });
    }
    return runtimePromise;
  }

  // ============================================================================
  // Unified VRM 0.x / 1.0 7-Expression Controller
  // ============================================================================
  class VrmExpressionController {
    constructor(expressionMap) {
      this.vrm = null;
      this.version = "unknown";
      this.expressionMap = expressionMap && typeof expressionMap === "object" ? expressionMap : {};
      this.available = new Set();
      this.weights = new Map();
      this.currentState = "idle";
      this.coLeadOverlay = null;
      this.surprisedStrategy = "none";
      this.blinkTimer = 2.6;
      this.blinkProgress = -1;
      this.blinkDuration = 0.16;
    }

    setCoLeadOverlay(overlay) {
      this.coLeadOverlay = overlay && typeof overlay === "object" ? overlay : null;
    }

    attach(vrm) {
      this.vrm = vrm;
      this.available.clear();
      this.weights.clear();
      this.version = String(vrm?.meta?.metaVersion ?? "unknown");
      const mgr = vrm?.expressionManager;
      if (!mgr) return;
      for (const expr of mgr.expressions || []) {
        if (expr?.expressionName) this.available.add(expr.expressionName);
      }
      if (mgr.presetExpressionMap) {
        for (const key of Object.keys(mgr.presetExpressionMap)) this.available.add(key);
      }
      if (mgr.customExpressionMap) {
        for (const key of Object.keys(mgr.customExpressionMap)) this.available.add(key);
      }
      if (this.has("surprised")) this.surprisedStrategy = "preset:surprised";
      else if (this.has("Surprised")) this.surprisedStrategy = "custom:Surprised";
      else if (this.has("Fcl_ALL_Surprised")) this.surprisedStrategy = "custom:Fcl_ALL_Surprised";
      else if (this.has("oh") || this.has("o")) this.surprisedStrategy = "fallback:oh+blink0";
      else this.surprisedStrategy = "badge-only";
      this.setState(this.currentState);
    }

    has(name) {
      return !!name && this.available.has(name);
    }

    resolveAlias(...candidates) {
      for (const name of candidates) {
        if (this.has(name)) return name;
      }
      return null;
    }

    setState(name) {
      this.currentState = Object.hasOwn(VRM_EXPRESSIONS, name) ? name : "idle";
    }

    update(dt) {
      if (!this.vrm?.expressionManager) return;
      const overlay = this.coLeadOverlay;
      const sleepyOverride = overlay?.mode === "sleepy";
      const dizzyOverride = overlay?.mode === "dizzy" || overlay?.mode === "dizzy-shake" || overlay?.mode === "dizzy-recover";
      const actionOverride = overlay?.mode === "knock-startle" || overlay?.mode === "wakeup-jolt" ||
        overlay?.mode === "annoyed-tap" || overlay?.mode === "phone-buzz" || overlay?.mode === "phone-facedown-merong";
      const explicitBlink = sleepyOverride || actionOverride || overlay?.mode === "dizzy" || overlay?.mode === "dizzy-shake";
      const allowBlink = !explicitBlink && (!BLINK_SUPPRESSED.has(this.currentState) || !!overlay?.allowBlink);
      let autoBlink = 0;
      if (allowBlink) {
        if (this.blinkProgress >= 0) {
          this.blinkProgress += dt;
          if (this.blinkProgress >= this.blinkDuration) {
            this.blinkProgress = -1;
            this.blinkTimer = 3.4 + Math.random() * 1.8;
          } else {
            // Smooth quick-close, gentle-open blink envelope.
            const u = this.blinkProgress / this.blinkDuration;
            autoBlink = Math.pow(Math.sin(Math.PI * Math.pow(u, 0.82)), 1.35);
          }
        } else {
          this.blinkTimer -= dt;
          if (this.blinkTimer <= 0) this.blinkProgress = 0;
        }
      } else {
        this.blinkProgress = -1;
      }

      const targets = new Map();
      const setTarget = (name, val) => {
        if (!name) return;
        targets.set(name, Math.max(0, Math.min(1, val)));
      };

      const kNeutral = this.resolveAlias("neutral");
      const kBlink = this.resolveAlias("blink");
      const kBlinkLeft = this.resolveAlias("blinkLeft", "Blink_L", "Fcl_EYE_Close_L", "blink_l");
      const kBlinkRight = this.resolveAlias("blinkRight", "Blink_R", "Fcl_EYE_Close_R", "blink_r");
      const kRelaxed = this.resolveAlias("relaxed", "fun");
      const kAngry = this.resolveAlias("angry");
      const kSad = this.resolveAlias("sad", "sorrow");
      const kHappy = this.resolveAlias("happy", "joy");
      const kSurprised = this.resolveAlias("surprised", "Surprised", "Fcl_ALL_Surprised");
      const kOh = this.resolveAlias("oh", "o");
      const kAa = this.resolveAlias("aa", "a");
      const kEe = this.resolveAlias("ee", "e");

      const override = C.own(this.expressionMap, this.currentState);
      const overridePresetAliases = {
        happy: ["happy", "joy"],
        joy: ["joy", "happy"],
        sad: ["sad", "sorrow"],
        sorrow: ["sorrow", "sad"],
        relaxed: ["relaxed", "fun"],
        fun: ["fun", "relaxed"],
        oh: ["oh", "o"],
        o: ["o", "oh"],
        surprised: ["surprised", "Surprised", "Fcl_ALL_Surprised"]
      };
      let overrideTarget = "";
      if (override && typeof override === "object") {
        if (typeof override.custom === "string" && this.has(override.custom)) {
          overrideTarget = override.custom;
        } else if (typeof override.preset === "string") {
          const candidates = C.own(overridePresetAliases, override.preset) || [override.preset];
          overrideTarget = this.resolveAlias(...candidates) || "";
        }
      }
      const overrideWeight = override && typeof override.weight === "number" && Number.isFinite(override.weight)
        ? override.weight : 1.0;

      if (sleepyOverride) {
        setTarget(kBlink, overlay.blink ?? 0.92);
        setTarget(kRelaxed, overlay.relaxed ?? 0.44);
      } else if (actionOverride) {
        if (overlay.mode === "knock-startle" || overlay.mode === "wakeup-jolt") {
          const surp = overlay.surprised ?? 0.90;
          if (kSurprised) setTarget(kSurprised, surp);
          else if (kOh) setTarget(kOh, Math.min(0.75, surp * 0.72));
          setTarget(kBlink, 0);
          setTarget(kNeutral, Math.max(0.15, 1.0 - surp * 0.75));
        } else if (overlay.mode === "annoyed-tap") {
          const ang = overlay.angry ?? 0.65;
          setTarget(kAngry, ang);
          setTarget(kBlink, overlay.blink ?? 0.12);
          setTarget(kNeutral, Math.max(0.32, 1.0 - ang * 0.65));
        } else if (overlay.mode === "phone-buzz") {
          const surp = overlay.surprised ?? 0.36;
          if (kSurprised) setTarget(kSurprised, surp);
          else if (kOh) setTarget(kOh, 0.24);
          if ((overlay.happy ?? 0) > 0.005) setTarget(kHappy, overlay.happy);
          setTarget(kBlink, 0);
          setTarget(kNeutral, 0.56);
        } else if (overlay.mode === "phone-facedown-merong") {
          // Cheeky anime wink + playful smile/merong cue; falls back cleanly
          // to bright cheeky grin on models without single-eye wink morphs.
          const winkKey = kBlinkLeft || kBlinkRight;
          if (winkKey) {
            setTarget(winkKey, overlay.wink ?? 0.96);
            setTarget(kHappy, overlay.happy ?? 0.64);
          } else {
            setTarget(kHappy, Math.max(0.84, overlay.happy ?? 0.84));
            setTarget(kRelaxed, 0.32);
          }
          const mouthKey = kEe || kAa || kOh;
          if (mouthKey) setTarget(mouthKey, 0.32);
          setTarget(kBlink, 0);
        }
      } else if (dizzyOverride) {
        if (overlay.mode === "dizzy") {
          const surp = overlay.surprised ?? 0.36;
          if (kSurprised) {
            setTarget(kSurprised, surp);
          } else if (kOh) {
            setTarget(kOh, Math.min(0.32, surp * 0.72));
          }
          if ((overlay.sad ?? 0) > 0.005) setTarget(kSad, overlay.sad);
          if ((overlay.relaxed ?? 0) > 0.005) setTarget(kRelaxed, overlay.relaxed);
          setTarget(kNeutral, Math.max(0.28, 1.0 - surp * 0.65 - (overlay.sad ?? 0) * 0.4));
          setTarget(kBlink, overlay.blink ?? 0.18);
        } else if (overlay.mode === "dizzy-shake") {
          setTarget(kBlink, overlay.blink ?? 0.78);
          if ((overlay.sad ?? 0) > 0.005) setTarget(kSad, overlay.sad);
          if ((overlay.relaxed ?? 0) > 0.005) setTarget(kRelaxed, overlay.relaxed);
          if ((overlay.happy ?? 0) > 0.005) setTarget(kHappy, overlay.happy);
          setTarget(kNeutral, 0.45);
        } else {
          // dizzy-recover: adorable warm sheepish smile with sparkling open eyes.
          const h = overlay.happy ?? 0.44;
          const r = overlay.relaxed ?? 0.18;
          setTarget(kNeutral, Math.max(0.28, 1.0 - h * 0.55 - r * 0.35));
          if (h > 0.005) setTarget(kHappy, h);
          if (r > 0.005) setTarget(kRelaxed, r);
          setTarget(kBlink, autoBlink);
        }
      } else if (overrideTarget) {
        setTarget(overrideTarget, overrideWeight);
        if (allowBlink) setTarget(kBlink, autoBlink);
      } else {
        switch (this.currentState) {
          case "idle":
            if (overlay) {
              const h = overlay.happy ?? 0;
              const r = overlay.relaxed ?? 0;
              setTarget(kNeutral, Math.max(0.25, 1.0 - h * 0.55 - r * 0.35));
              if (h > 0.005) setTarget(kHappy, h);
              if (r > 0.005) setTarget(kRelaxed, r);
            } else {
              setTarget(kNeutral, 1.0);
            }
            setTarget(kBlink, autoBlink);
            break;
          case "idle_doze":
            setTarget(kBlink, 1.0);
            setTarget(kRelaxed, 0.4);
            break;
          case "focused":
            if (overlay) {
              setTarget(kNeutral, 0.84);
              if ((overlay.happy ?? 0) > 0.005) setTarget(kHappy, overlay.happy);
              if ((overlay.relaxed ?? 0) > 0.005) setTarget(kRelaxed, overlay.relaxed);
              setTarget(kAngry, 0.04);
              setTarget(kBlink, autoBlink);
            } else {
              setTarget(kNeutral, 0.85);
              setTarget(kBlink, 0.15);
              setTarget(kAngry, 0.10);
            }
            break;
          case "half_lid":
            setTarget(kBlink, 0.50);
            setTarget(kRelaxed, 0.20);
            break;
          case "oo":
            if (kSurprised) {
              setTarget(kSurprised, 0.95);
            } else {
              setTarget(kOh, 0.60);
              setTarget(kBlink, 0.0);
            }
            break;
          case "cry":
            setTarget(kSad, 0.90);
            setTarget(kBlink, 0.22);
            break;
          case "happy":
            setTarget(kHappy, 0.92);
            break;
          case "wink": {
            const winkKey = kBlinkLeft || kBlinkRight;
            if (winkKey) setTarget(winkKey, 0.95);
            setTarget(kHappy, winkKey ? 0.62 : 0.86);
            setTarget(kBlink, 0);
            break;
          }
          case "merong": {
            const winkKey = kBlinkLeft || kBlinkRight;
            if (winkKey) setTarget(winkKey, 0.96);
            setTarget(kHappy, winkKey ? 0.72 : 0.88);
            const mouthKey = kEe || kAa || kOh;
            if (mouthKey) setTarget(mouthKey, 0.32);
            setTarget(kBlink, 0);
            break;
          }
          case "startled":
            if (kSurprised) setTarget(kSurprised, 0.95);
            else setTarget(kOh, 0.65);
            setTarget(kBlink, 0.0);
            break;
          case "annoyed":
            setTarget(kAngry, 0.74);
            setTarget(kBlink, 0.12);
            setTarget(kNeutral, 0.38);
            break;
          default:
            setTarget(kNeutral, 1.0);
        }
      }

      const mgr = this.vrm.expressionManager;
      const lerpRate = Math.min(1, Math.max(dt, 0.016) * 11);
      for (const name of this.available) {
        const target = targets.get(name) ?? 0;
        const prev = this.weights.get(name) ?? 0;
        const next = Math.abs(target - prev) < 0.002 ? target : prev + (target - prev) * lerpRate;
        this.weights.set(name, next);
        mgr.setValue(name, next);
      }
    }
  }

  // ============================================================================
  // Calm Standing Bust Rest Pose + Subtle Breathing + Clamped Head/Eye Gaze
  // ============================================================================
  const BASE_BONES = [
    "spine", "chest", "upperChest", "neck", "head",
    "leftShoulder", "rightShoulder",
    "leftUpperArm", "leftLowerArm", "rightUpperArm", "rightLowerArm",
    "leftHand", "rightHand"
  ];
  // Body bones + 30 humanoid finger bones (rigs missing finger bones skip them).
  const POSE_BONES = Object.freeze(BASE_BONES.concat(C.vrmCoLeadPose?.FINGER_BONES || []));
  const ZERO_EULER = Object.freeze([0, 0, 0]);
  const EXTRA_VRMA_BONES = [
    "hips",
    "leftUpperLeg", "rightUpperLeg", "leftLowerLeg", "rightLowerLeg",
    "leftFoot", "rightFoot", "leftToes", "rightToes"
  ];

  // Relaxed lowered-arm bust stand with gentle chest/spine/shoulder breathing.
  // Normalized VRM 1.0 axes (+X character-left, +Y up, +Z forward):
  //   leftUpperArm Z < 0 lowers arm naturally along the side;
  //   rightUpperArm Z > 0 lowers arm naturally along the side.
  function computeStandTargets(t, headPitchCue, headGazeYaw, headGazePitch, swayScale) {
    const breath = Math.sin(t * 1.45);
    const sway = Math.sin(t * 0.72) * (swayScale ?? 1);
    const neckPitch = headPitchCue * 0.42 + headGazePitch * 0.35;
    const headPitch = headPitchCue * 0.58 + headGazePitch * 0.65;
    const neckYaw = headGazeYaw * 0.35;
    const headYaw = headGazeYaw * 0.65 + sway * 0.016;
    return {
      spine: [breath * 0.012, sway * 0.010, 0],
      chest: [breath * 0.018, 0, 0],
      upperChest: [breath * 0.010, 0, 0],
      neck: [neckPitch, neckYaw, 0],
      head: [headPitch, headYaw, sway * 0.012],
      leftShoulder: [0, 0, -0.04 + breath * 0.014],
      rightShoulder: [0, 0, 0.04 - breath * 0.014],
      leftUpperArm: [0.04, 0.03, -1.26 - breath * 0.010],
      leftLowerArm: [0.04, -0.08, -0.08],
      rightUpperArm: [0.04, -0.03, 1.26 + breath * 0.010],
      rightLowerArm: [0.04, 0.08, 0.08],
      leftHand: [0, 0, 0.04],
      rightHand: [0, 0, -0.04]
    };
  }

  // Scenario resolution rule: every built-in scenario resolves to either a full
  // mapping (stand / registered gesture / pack .vrma) or an expression-only
  // degradation — never an unknown ID or throw.
  function resolveScenario(id, beat, pack) {
    const vocab = vrmVocabulary(pack);
    const b = C.record(beat) ? beat : {};
    const expr = Object.hasOwn(vocab.expressions, b.expression) ? b.expression : "idle";
    const rawPose = typeof b.pose === "string" ? b.pose : "stand";
    const hasPoseVrma = !!(rawPose && rawPose !== "stand" && pack?.animations && Object.hasOwn(pack.animations, rawPose));
    const gesture = typeof b.gesture === "string" && Object.hasOwn(vocab.gestures, b.gesture) ? b.gesture : null;
    const rawPropId = typeof b.prop === "string" ? b.prop : b.prop?.id;
    const wantedUnmapped = !!(
      (b.gesture && !gesture) ||
      rawPropId ||
      (rawPose && rawPose !== "stand" && !hasPoseVrma)
    );
    const mode = wantedUnmapped ? "expression" : "full";
    return {
      id: id || "",
      mode,
      pose: hasPoseVrma ? rawPose : "stand",
      gesture,
      expression: expr,
      prop: null,
      stage: "stay"
    };
  }

  let activeVrmRig = null;

  function makeVrmRig(pack, options = {}) {
    let mounted = false, disposed = false, paused = false, contextLost = false;
    let container = null, canvasWrap = null, canvas = null, dissolveGrid = null, fxCanvas = null, fxCtx = null, overlay = null;
    let runtime = null, renderer = null, scene = null, camera = null, vrm = null, coLead = null;
    let isVrm0 = false, lookTarget = null, headPosVec = null, projHeadVec = null, studio = null, palmProbe = null, palmSampler = null, fingerBoneCount = 0, calibFrames = 0, calibPasses = 0, projMeshBox = null, projCornerVec = null, raycaster = null, rayNdcVec = null, mixer = null, activeVrmaAction = null, activeVrmaKey = "";
    const CHAR_BOUNDS_TTL_MS = 120;
    const cachedCharBounds = { x: 0, y: 0, width: 0, height: 0, viewportX: 0, viewportY: 0 };
    let hasCachedCharBounds = false, charBoundsDirty = true, lastCharBoundsAt = -Infinity;
    let vrmaClips = new Map(), vrmaLoading = new Set(), vrmaFailed = new Set();
    let width = LOGICAL_W, height = LOGICAL_H, dpr = 1;
    let overlayMode = false;
    let viewport = { width: LOGICAL_W, height: LOGICAL_H, dpr: 1 };
    let anchorRect = { left: 0, top: 0, width: LOGICAL_W, height: LOGICAL_H, right: LOGICAL_W, bottom: LOGICAL_H };
    let renderMode = "docked"; // "docked" | "bleed" | "roam"
    let bleedPadding = { ...DEFAULT_BLEED_PADDING };
    let activeScissorRect = { left: 0, top: 0, width: LOGICAL_W, height: LOGICAL_H };
    let prevScissorRect = null;
    let needsFullClear = true;
    let viewportResizeCount = 0, anchorUpdateCount = 0, shrinkClearCount = 0;
    let lastBotbarH = 0, lastTopbarH = 0;
    let cachedChromeBounds = null;
    let cachedModel = null; // { key, buffer } retained across WebGL context loss
    let modelFetchPromise = null, loadEpoch = 0;
    let modelFetchCount = 0, framesRendered = 0;
    let lastSeconds = 0, elapsed = 0;
    let activePose = "stand", activeGesture = null, activeExpression = "idle";
    let rawPose = "stand", rawGesture = null;
    let activeBeatMeta = null;
    let pendingAccent = null, pendingCoLeadBeat = null, pendingHover = null, pendingPhoneState = null, pendingGesture = "";
    // Arrival order of the buffered gesture and phone state (0 = none).
    let pendingOrder = 0, pendingGestureOrder = 0, pendingPhoneOrder = 0;
    let pendingInitialRid = "", pendingSessionSwitch = null;
    let ridObserver = null;
    let gaze = { x: 0, y: 0 }, smoothedLook = { yaw: 0, pitch: 0 }, smoothedHead = { yaw: 0, pitch: 0 };
    const boneRotations = new Map();
    const exprCtrl = new VrmExpressionController(pack?.expressionMap);
    const loadCancel = new AbortController();
    let listenersCancel = null;
    let cameraFraming = {
      headY: 1.45, chestY: 1.18, waistY: 0.98, topY: 1.72,
      centerX: 0, headZ: 0,
      bustSpan: 0.27, targetY: 1.38, frameHeight: 0.85,
      headroomRatio: 0.13, cameraZ: 1.85, usedBox3: false
    };

    function bufferGesture(name) {
      pendingGesture = name;
      pendingGestureOrder = ++pendingOrder;
    }

    function setOverlay(text) {
      if (!overlay) return;
      overlay.textContent = text || "";
      overlay.hidden = !text;
    }

    function disposeVrmOnly() {
      if (activeVrmaAction) {
        activeVrmaAction.stop();
        activeVrmaAction = null;
        activeVrmaKey = "";
      }
      if (mixer) {
        mixer.stopAllAction();
        mixer = null;
      }
      vrmaClips.clear();
      vrmaLoading.clear();
      vrmaFailed.clear();
      if (vrm && scene && runtime) {
        scene.remove(vrm.scene);
        try { runtime.VRMUtils.deepDispose(vrm.scene); } catch (_) { /* ignore */ }
      }
      vrm = null;
      hasCachedCharBounds = false;
      charBoundsDirty = true;
      lastCharBoundsAt = -Infinity;
      boneRotations.clear();
    }

    function disposeRendererOnly(forceLoss) {
      disposeVrmOnly();
      if (coLead) {
        try { coLead.dispose(); } catch (_) { /* ignore */ }
        coLead = null;
      }
      try { studio?.dispose(); } catch (_) { /* ignore */ }
      studio = null;
      if (renderer) {
        try { renderer.dispose(); } catch (_) { /* ignore */ }
        if (forceLoss) {
          try { renderer.forceContextLoss(); } catch (_) { /* ignore */ }
        }
        renderer = null;
      }
      scene = null;
      camera = null;
      lookTarget = null;
      headPosVec = null;
      projHeadVec = null;
      palmProbe = null;
      palmSampler = null;
      projMeshBox = null;
      projCornerVec = null;
      raycaster = null;
      rayNdcVec = null;
      prevScissorRect = null;
      needsFullClear = true;
    }

    function normalizeAnchorRect(rect) {
      const w = Math.max(1, Number.isFinite(rect?.width) && rect.width > 0 ? rect.width : width);
      const h = Math.max(1, Number.isFinite(rect?.height) && rect.height > 0 ? rect.height : height);
      const left = Number.isFinite(rect?.left) ? rect.left : 0;
      const top = Number.isFinite(rect?.top) ? rect.top : 0;
      return { left, top, width: w, height: h, right: left + w, bottom: top + h };
    }

    function refreshChromeBounds() {
      const b = C.vrmOverlayMath.readChromeBounds(container, overlayMode, viewport, width, height);
      lastTopbarH = b.topbarH;
      lastBotbarH = b.botbarH;
      cachedChromeBounds = b;
      return b;
    }

    function readChromeBounds() {
      if (cachedChromeBounds &&
          cachedChromeBounds.vw === Math.max(1, Math.round(overlayMode ? viewport.width : width)) &&
          cachedChromeBounds.vh === Math.max(1, Math.round(overlayMode ? viewport.height : height))) {
        return cachedChromeBounds;
      }
      return refreshChromeBounds();
    }

    function syncSlotStyleVars() {
      if (!container) return;
      container.dataset.renderMode = renderMode;
      container.dataset.overlay = overlayMode ? "1" : "0";
      const style = container.style;
      if (!style || typeof style.setProperty !== "function") return;
      const slotL = Math.round(anchorRect.left);
      const slotT = Math.round(anchorRect.top);
      const slotW = Math.max(1, Math.round(anchorRect.width));
      const slotH = Math.max(1, Math.round(anchorRect.height));
      // Subtle 7% bottom-rim fade in docked mode only (never dissolves laptop/phone).
      const rimPct = Math.min(8, Math.max(6, Math.round((Math.max(10, Math.round(slotH * 0.07)) / slotH) * 100)));
      const fadeStartPct = 100 - rimPct;
      const fadeMidPct = Math.round(fadeStartPct + rimPct * 0.55);
      style.setProperty("--vrm-slot-left", slotL + "px");
      style.setProperty("--vrm-slot-top", slotT + "px");
      style.setProperty("--vrm-slot-width", slotW + "px");
      style.setProperty("--vrm-slot-height", slotH + "px");
      style.setProperty("--vrm-fade-start", fadeStartPct + "%");
      style.setProperty("--vrm-fade-mid", fadeMidPct + "%");
      style.setProperty("--vrm-fade-end", "100%");
    }

    function applyAsymmetricProjection(rect) {
      if (!camera) return null;
      const f = C.vrmOverlayMath.computeAsymmetricFrustum(anchorRect, rect, camera.near || 0.08, camera.far || 25, BASE_FOV);
      camera.fov = BASE_FOV;
      camera.aspect = Math.max(0.45, anchorRect.width / Math.max(1, anchorRect.height));
      if (camera.projectionMatrix && typeof camera.projectionMatrix.makePerspective === "function") {
        camera.projectionMatrix.makePerspective(f.left, f.right, f.top, f.bottom, f.near, f.far);
        if (camera.projectionMatrixInverse && typeof camera.projectionMatrixInverse.copy === "function") {
          camera.projectionMatrixInverse.copy(camera.projectionMatrix).invert();
        }
      } else if (typeof camera.updateProjectionMatrix === "function") {
        camera.updateProjectionMatrix();
      }
      return f;
    }

    function applyCanonicalSlotProjection() {
      if (!camera) return;
      camera.fov = BASE_FOV;
      camera.aspect = Math.max(0.45, anchorRect.width / Math.max(1, anchorRect.height));
      if (typeof camera.updateProjectionMatrix === "function") camera.updateProjectionMatrix();
    }

    function computeActiveScissorRect() {
      const bounds = readChromeBounds();
      if (!overlayMode) {
        return { rect: { left: 0, top: 0, width: bounds.vw, height: bounds.vh }, bounds };
      }
      const { clampRectToChrome, projectRootsToViewportRect } = C.vrmOverlayMath;
      if (renderMode === "bleed") {
        return {
          rect: clampRectToChrome({
            left: anchorRect.left - bleedPadding.left,
            top: anchorRect.top - bleedPadding.top,
            width: anchorRect.width + bleedPadding.left + bleedPadding.right,
            height: anchorRect.height + bleedPadding.top + bleedPadding.bottom
          }, bounds),
          bounds
        };
      }
      if (renderMode === "roam") {
        const roots = [];
        if (vrm?.scene) roots.push(vrm.scene);
        const props = coLead?.getPropObjects?.();
        if (Array.isArray(props)) for (const p of props) if (p) roots.push(p);
        const union = projectRootsToViewportRect(roots, bounds, camera, projMeshBox, projCornerVec, applyAsymmetricProjection, ROAM_AABB_PADDING);
        const fallback = {
          left: anchorRect.left - ROAM_AABB_PADDING,
          top: anchorRect.top - ROAM_AABB_PADDING,
          width: anchorRect.width + ROAM_AABB_PADDING * 2,
          height: anchorRect.height + ROAM_AABB_PADDING * 2
        };
        return { rect: clampRectToChrome(union || fallback, bounds), bounds, primaryBounds: union?.primary || null };
      }
      return { rect: clampRectToChrome(anchorRect, bounds), bounds, primaryBounds: null };
    }

    function initSceneAndRenderer(rt) {
      const { THREE } = rt;
      scene = new THREE.Scene();
      camera = new THREE.PerspectiveCamera(BASE_FOV, anchorRect.width / Math.max(1, anchorRect.height), 0.08, 25);
      camera.position.set(0, 1.38, 1.85);
      lookTarget = new THREE.Object3D();
      headPosVec = new THREE.Vector3();
      projHeadVec = new THREE.Vector3();
      palmProbe = C.vrmStudio?.createPalmProbe?.(THREE, "left") || null;
      palmSampler = () => (palmProbe && vrm && C.vrmStudio.samplePalmProbe(vrm.humanoid, palmProbe) ? palmProbe : null);
      projMeshBox = typeof THREE.Box3 === "function" ? new THREE.Box3() : null;
      projCornerVec = typeof THREE.Vector3 === "function" ? new THREE.Vector3() : null;
      raycaster = typeof THREE.Raycaster === "function" ? new THREE.Raycaster() : null;
      rayNdcVec = typeof THREE.Vector2 === "function" ? new THREE.Vector2() : null;
      scene.add(lookTarget);

      renderer = new THREE.WebGLRenderer({
        canvas,
        alpha: true,
        antialias: true,
        powerPreference: "default"
      });
      renderer.outputColorSpace = THREE.SRGBColorSpace;
      renderer.toneMapping = THREE.NoToneMapping;
      renderer.shadowMap.enabled = false;
      renderer.setClearColor(0x000000, 0);

      // Theme-adaptive key/fill/rim + hemisphere studio lighting (live data-theme /
      // prefers-color-scheme) with a prop-only PMREM env; avatar MToon untouched.
      studio = C.vrmStudio?.createStudioLighting?.({ THREE, scene, renderer }) || null;

      if (typeof C.createVrmCoLead === "function") {
        coLead = C.createVrmCoLead({ THREE, scene, camera });
        coLead.setBreakoutState?.(renderMode);
        if (pendingInitialRid) coLead.syncInitialSession(pendingInitialRid);
        if (pendingSessionSwitch) {
          coLead.onSessionSwitch(pendingSessionSwitch.rid, pendingSessionSwitch.meta);
          pendingSessionSwitch = null;
        }
        if (pendingAccent !== null) coLead.setLaptopAccent(pendingAccent);
        if (pendingCoLeadBeat !== null) coLead.setCoLeadBeat(pendingCoLeadBeat);
        if (pendingHover !== null) coLead.setHover(pendingHover);
        // Replay buffered gesture and phone state in arrival order so neither
        // overwrites the other: a gesture that arrived before the latest phone
        // snapshot plays first and the snapshot settles the final posture.
        const gesture = pendingGesture;
        pendingGesture = "";
        if (gesture && pendingGestureOrder < pendingPhoneOrder) coLead.triggerGesture(gesture);
        if (pendingPhoneState !== null) coLead.setPhoneState(pendingPhoneState);
        if (gesture && pendingGestureOrder >= pendingPhoneOrder) coLead.triggerGesture(gesture);
      }

      applyViewportResize(true);
    }

    function syncFxCanvasSize() {
      if (!fxCanvas) return;
      const activeDpr = overlayMode ? viewport.dpr : dpr;
      const clampedDpr = Math.min(Math.max(activeDpr || 1, 1), 2);
      const w = Math.max(64, Math.round(overlayMode ? viewport.width : width));
      const h = Math.max(64, Math.round(overlayMode ? viewport.height : height));
      const pxW = Math.round(w * clampedDpr);
      const pxH = Math.round(h * clampedDpr);
      if (fxCanvas.width !== pxW || fxCanvas.height !== pxH) {
        fxCanvas.width = pxW;
        fxCanvas.height = pxH;
      }
    }

    function applyViewportResize(force = false) {
      if (fxCanvas && !fxCanvas.hidden) syncFxCanvasSize();
      syncSlotStyleVars();
      refreshChromeBounds();
      if (!renderer || !camera) return;
      const activeDpr = overlayMode ? viewport.dpr : dpr;
      const clampedDpr = Math.min(Math.max(activeDpr || 1, 1), 2);
      const w = Math.max(64, Math.round(overlayMode ? viewport.width : width));
      const h = Math.max(64, Math.round(overlayMode ? viewport.height : height));
      renderer.setPixelRatio(clampedDpr);
      renderer.setSize(w, h, false);
      viewportResizeCount++;
      prevScissorRect = null;
      needsFullClear = true;
      applyCanonicalSlotProjection();
    }

    function applyImmediateStandPose(loadedVrm, vrm0Flag) {
      const targets = coLead && C.vrmCoLeadMath
        ? C.vrmCoLeadMath.computeCoLeadTargets({ elapsed: 0, typingWeight: 1, glanceWeight: 0, sleepyWeight: 0, reactionWeight: 0, workingWeight: 0, tiltDir: 1, headYaw: 0, headPitch: 5.6 * DEG, headPitchCue: 0 })
        : computeStandTargets(0, 0, 0, 0, 1);
      for (const boneName of POSE_BONES) {
        const node = loadedVrm.humanoid?.getNormalizedBoneNode(boneName);
        if (!node) continue;
        const rawTarget = targets[boneName] || ZERO_EULER;
        const [tx, ty, tz] = conjugateVrm0Euler(rawTarget[0], rawTarget[1], rawTarget[2], vrm0Flag);
        boneRotations.set(boneName, [tx, ty, tz]);
        node.rotation.set(tx, ty, tz, "XYZ");
      }
      loadedVrm.update(0);
      loadedVrm.scene.updateMatrixWorld(true);
    }

    // Measure posed world-space mesh bounds (including SkinnedMesh hair, ears,
    // hats, and accessories) so bust framing always gives comfortable headroom.
    function computePosedMeshBounds(THREE, loadedVrm, waistY) {
      if (typeof THREE.Box3 !== "function") return null;
      const fullBox = new THREE.Box3();
      const upperBox = new THREE.Box3();
      const meshBox = new THREE.Box3();
      let found = false;
      loadedVrm.scene.traverse(obj => {
        if (!obj || !obj.isMesh || obj.visible === false || !obj.geometry) return;
        if (obj.isSkinnedMesh && typeof obj.computeBoundingBox === "function") {
          obj.computeBoundingBox();
          if (!obj.boundingBox || obj.boundingBox.isEmpty()) return;
          meshBox.copy(obj.boundingBox).applyMatrix4(obj.matrixWorld);
        } else {
          if (!obj.geometry.boundingBox && typeof obj.geometry.computeBoundingBox === "function") {
            obj.geometry.computeBoundingBox();
          }
          if (!obj.geometry.boundingBox || obj.geometry.boundingBox.isEmpty()) return;
          meshBox.copy(obj.geometry.boundingBox).applyMatrix4(obj.matrixWorld);
        }
        if (!Number.isFinite(meshBox.max.y) || !Number.isFinite(meshBox.min.y)) return;
        fullBox.union(meshBox);
        if (meshBox.min.y >= waistY - 0.12) upperBox.union(meshBox);
        found = true;
      });
      return found ? { fullBox, upperBox } : null;
    }

    function autoFrameBust(loadedVrm) {
      if (!loadedVrm || !camera || !runtime) return;
      const { THREE } = runtime;
      loadedVrm.scene.updateMatrixWorld(true);
      const headNode = loadedVrm.humanoid?.getNormalizedBoneNode("head");
      const chestNode =
        loadedVrm.humanoid?.getNormalizedBoneNode("upperChest") ||
        loadedVrm.humanoid?.getNormalizedBoneNode("chest") ||
        loadedVrm.humanoid?.getNormalizedBoneNode("spine");
      const hipsNode = loadedVrm.humanoid?.getNormalizedBoneNode("hips");
      const leftArmNode = loadedVrm.humanoid?.getNormalizedBoneNode("leftUpperArm");
      const rightArmNode = loadedVrm.humanoid?.getNormalizedBoneNode("rightUpperArm");

      const headPos = new THREE.Vector3(0, 1.45, 0);
      const chestPos = new THREE.Vector3(0, 1.18, 0);
      if (headNode) headNode.getWorldPosition(headPos);
      if (chestNode) chestNode.getWorldPosition(chestPos);

      const bustSpan = Math.max(0.18, headPos.y - chestPos.y);
      let waistY = chestPos.y - bustSpan * 1.05;
      if (hipsNode) {
        const hipsPos = new THREE.Vector3();
        hipsNode.getWorldPosition(hipsPos);
        if (Number.isFinite(hipsPos.y) && hipsPos.y < chestPos.y) {
          waistY = hipsPos.y + (chestPos.y - hipsPos.y) * 0.32;
        }
      }

      let shoulderSpan = bustSpan * 1.25;
      if (leftArmNode && rightArmNode) {
        const lp = new THREE.Vector3(), rp = new THREE.Vector3();
        leftArmNode.getWorldPosition(lp);
        rightArmNode.getWorldPosition(rp);
        const span = Math.abs(lp.x - rp.x);
        if (Number.isFinite(span) && span > 0.12) shoulderSpan = span;
      }

      const bounds = computePosedMeshBounds(THREE, loadedVrm, waistY);
      const minHeadMargin = Math.max(0.18, bustSpan * 0.72);
      const maxHeadMargin = Math.max(0.75, bustSpan * 3.0);
      let topY = headPos.y + minHeadMargin;
      let frontOffset = 0.10;
      let centerX = headPos.x;
      let upperWidth = shoulderSpan * 1.85;
      let usedBox3 = false;

      if (bounds && !bounds.fullBox.isEmpty()) {
        usedBox3 = true;
        const rawTop = Math.min(bounds.fullBox.max.y, headPos.y + maxHeadMargin);
        topY = Math.max(topY, rawTop);
        frontOffset = C.clamp(bounds.fullBox.max.z - headPos.z, 0.08, 0.42);
        if (!bounds.upperBox.isEmpty()) {
          const halfW = Math.max(
            Math.abs(bounds.upperBox.max.x - headPos.x),
            Math.abs(bounds.upperBox.min.x - headPos.x)
          );
          upperWidth = Math.max(upperWidth, Math.min(halfW * 2.12, shoulderSpan * 3.2));
        }
      }

      // Calibrated 12% headroom above the true hair/ear/hat top on the head plane
      // (10-14% across 220-360px rails); width-limited slots grow downward only.
      const slotAspect = Math.max(0.45, anchorRect.width / Math.max(1, anchorRect.height));
      camera.fov = BASE_FOV;
      camera.aspect = slotAspect;
      const fr = C.vrmStudio.solveBustFraming({ topY, waistY, bustSpan, upperWidth, frontOffset, aspect: slotAspect, fov: BASE_FOV });
      camera.position.set(centerX, fr.targetY, headPos.z + fr.dist);
      camera.lookAt(centerX, fr.targetY, headPos.z);
      camera.updateProjectionMatrix();
      cameraFraming = {
        headY: headPos.y, chestY: chestPos.y, waistY, topY, centerX, headZ: headPos.z, bustSpan,
        targetY: fr.targetY, frameHeight: fr.frameHeight, headroomRatio: fr.headroomRatio,
        cameraZ: headPos.z + fr.dist, usedBox3, upperWidth, limitedBy: fr.limitedBy
      };
      cameraFraming.qa = C.vrmStudio.measureFraming(THREE, camera, cameraFraming);
      coLead?.layoutProp(cameraFraming, camera);
      studio?.attachPropEnvironment(coLead?.getPropObjects?.());
      calibFrames = 6;
      calibPasses = 0;
    }

    // Pixel-true headroom: once per framing, read the docked slot alpha after render
    // and slide the camera vertically so the visible silhouette top (not alpha-cut
    // card bounds) lands on the solved headroom ratio. At most two corrections.
    function calibrateHeadroom(R, bounds) {
      const fr = cameraFraming;
      if (!camera || !(fr.frameHeight > 0)) return;
      const rect = overlayMode ? R : { left: 0, top: 0, width, height };
      const probe = C.vrmStudio?.measureAlphaTopRow?.(renderer.getContext?.(), rect, overlayMode ? bounds.vh : height, renderer.getPixelRatio?.() || 1);
      if (!probe || probe.ratio < 0) {
        // Unreadable or empty slot (e.g. a frame before the avatar shows): bounded retry.
        if (++calibPasses <= 2) calibFrames = 4;
        return;
      }
      const err = probe.ratio - fr.headroomRatio;
      fr.qa = { ...(fr.qa || {}), pixelHeadroomPct: Number((probe.ratio * 100).toFixed(2)), calibPasses };
      if (Math.abs(err) < 0.006 || calibPasses >= 2) return;
      // Clamp so a mis-read can never throw the avatar out of frame.
      const dy = C.clamp(err, -0.06, 0.08) * fr.frameHeight;
      camera.position.y -= dy;
      fr.targetY -= dy;
      camera.updateMatrixWorld?.(true);
      calibPasses++;
      calibFrames = 2;
      coLead?.layoutProp(fr, camera);
    }

    async function parseVrmBuffer(buffer) {
      if (disposed || !runtime || !scene) return;
      const { GLTFLoader, VRMLoaderPlugin, VRMAnimationLoaderPlugin, VRMUtils, THREE } = runtime;
      const loader = new GLTFLoader();
      loader.register(parser => new VRMLoaderPlugin(parser));
      loader.register(parser => new VRMAnimationLoaderPlugin(parser));
      const gltf = await loader.parseAsync(buffer.slice(0), "");
      if (disposed || !scene) return;
      const loaded = gltf.userData?.vrm;
      if (!loaded) throw new Error("Asset does not contain a valid VRM payload");

      VRMUtils.removeUnnecessaryVertices(gltf.scene);
      VRMUtils.combineSkeletons(gltf.scene);
      VRMUtils.rotateVRM0(loaded);

      disposeVrmOnly();
      loaded.scene.traverse(obj => { obj.frustumCulled = false; });
      scene.add(loaded.scene);
      vrm = loaded;
      isVrm0 = String(loaded.meta?.metaVersion ?? "") === "0";
      fingerBoneCount = POSE_BONES.slice(BASE_BONES.length).filter(n => loaded.humanoid?.getNormalizedBoneNode(n)).length;
      if (loaded.lookAt && lookTarget) loaded.lookAt.target = lookTarget;
      mixer = new THREE.AnimationMixer(loaded.scene);

      exprCtrl.attach(loaded);
      exprCtrl.setState(activeExpression);
      // Apply the relaxed standing bust pose and update skeleton matrices BEFORE
      // computing THREE.Box3 skinned bounds so autoFrameBust measures the posed avatar.
      applyImmediateStandPose(loaded, isVrm0);
      autoFrameBust(loaded);
      charBoundsDirty = true;
      lastCharBoundsAt = -Infinity;

      setOverlay("");
      renderTick(0.016, gaze, null, activeBeatMeta);
      options.onReady?.();
    }

    async function ensureVrmaClip(key) {
      if (!vrm || !runtime || !pack?.animations || !Object.hasOwn(pack.animations, key)) return null;
      if (vrmaClips.has(key)) return vrmaClips.get(key);
      if (vrmaLoading.has(key) || vrmaFailed.has(key)) return null;
      vrmaLoading.add(key);
      try {
        const file = pack.animations[key];
        const fetched = await C.fetchPackAsset(pack, file, loadCancel.signal);
        if (disposed || !vrm || !runtime) return null;
        const { GLTFLoader, VRMAnimationLoaderPlugin, createVRMAnimationClip } = runtime;
        const loader = new GLTFLoader();
        loader.register(parser => new VRMAnimationLoaderPlugin(parser));
        const gltf = await loader.parseAsync(fetched.buffer.slice(0), "");
        if (disposed || !vrm || !runtime) return null;
        const anims = gltf.userData?.vrmAnimations;
        if (anims && anims.length) {
          const clip = createVRMAnimationClip(anims[0], vrm);
          vrmaClips.set(key, clip);
          return clip;
        }
        vrmaFailed.add(key);
      } catch (_) {
        // Optional .vrma failure degrades once to procedural stand without refetch storms.
        if (!disposed && !loadCancel.signal.aborted) vrmaFailed.add(key);
      } finally {
        vrmaLoading.delete(key);
      }
      return null;
    }

    function resetExtraVrmaBones() {
      if (!vrm?.humanoid) return;
      for (const boneName of EXTRA_VRMA_BONES) {
        const node = vrm.humanoid.getNormalizedBoneNode(boneName);
        if (node) node.rotation.set(0, 0, 0, "XYZ");
      }
    }

    function syncVrma(key) {
      if (!mixer || !vrm) return;
      const wanted = key && pack?.animations && Object.hasOwn(pack.animations, key) && !vrmaFailed.has(key) ? key : "";
      if (!wanted) {
        if (activeVrmaAction) {
          syncBoneCacheFromRig();
          activeVrmaAction.fadeOut(0.18);
          activeVrmaAction.stop();
          activeVrmaAction = null;
          activeVrmaKey = "";
          resetExtraVrmaBones();
        }
        return;
      }
      if (wanted === activeVrmaKey && activeVrmaAction) return;
      const clip = vrmaClips.get(wanted);
      if (!clip) {
        ensureVrmaClip(wanted).then(loaded => {
          if (loaded && !disposed && (activeGesture === wanted || activePose === wanted)) {
            syncVrma(wanted);
          }
        });
        return;
      }
      const nextAction = mixer.clipAction(clip);
      nextAction.reset();
      nextAction.fadeIn(0.18);
      nextAction.play();
      if (activeVrmaAction && activeVrmaAction !== nextAction) {
        activeVrmaAction.crossFadeTo(nextAction, 0.18, false);
      }
      activeVrmaAction = nextAction;
      activeVrmaKey = wanted;
    }

    function syncBoneCacheFromRig() {
      if (!vrm) return;
      for (const boneName of POSE_BONES) {
        const node = vrm.humanoid?.getNormalizedBoneNode(boneName);
        if (node) boneRotations.set(boneName, [node.rotation.x, node.rotation.y, node.rotation.z]);
      }
    }

    function setBoneEuler(boneName, rx, ry, rz) {
      const node = vrm?.humanoid?.getNormalizedBoneNode(boneName);
      if (!node) return;
      const [cx, cy, cz] = conjugateVrm0Euler(rx, ry, rz, isVrm0);
      boneRotations.set(boneName, [cx, cy, cz]);
      node.rotation.set(cx, cy, cz, "XYZ");
    }

    function renderDizzyFxOverlay(coState) {
      if (!fxCanvas || !fxCtx || !C.vrmDizzyFx) return;
      const dizzyAlpha = coState && Number.isFinite(coState.dizzyFxAlpha) ? coState.dizzyFxAlpha : 0;
      const actionAlpha = coState && Number.isFinite(coState.actionFxAlpha) ? coState.actionFxAlpha : 0;
      const actionKind = coState && typeof coState.actionFxKind === "string" ? coState.actionFxKind : "";
      const hasDizzy = dizzyAlpha > 0.002;
      const hasAction = actionAlpha > 0.002 && !!actionKind && typeof C.vrmDizzyFx.drawActionFx === "function";
      if (!hasDizzy && !hasAction) {
        if (!fxCanvas.hidden) {
          fxCtx.setTransform?.(1, 0, 0, 1, 0, 0);
          fxCtx.clearRect?.(0, 0, fxCanvas.width || width, fxCanvas.height || height);
          fxCanvas.hidden = true;
          if (overlayMode) {
            fxCanvas.width = 0;
            fxCanvas.height = 0;
          }
        }
        return;
      }
      syncFxCanvasSize();
      if (fxCanvas.hidden) fxCanvas.hidden = false;

      const activeDpr = overlayMode ? viewport.dpr : dpr;
      const clampedDpr = Math.min(Math.max(activeDpr || 1, 1), 2);
      const R = activeScissorRect || { left: 0, top: 0, width: Math.max(64, Math.round(width)), height: Math.max(64, Math.round(height)) };
      const fullW = Math.max(64, Math.round(overlayMode ? viewport.width : width));
      const fullH = Math.max(64, Math.round(overlayMode ? viewport.height : height));
      const w = Math.max(64, Math.round(R.width));
      const h = Math.max(64, Math.round(R.height));
      let headX = w * 0.5;
      let headY = h * 0.42;
      let headRadius = Math.min(w, h) * 0.19;
      let crownY = headY - headRadius * 1.28;

      if (vrm && camera && projHeadVec && typeof projHeadVec.project === "function") {
        const headNode = vrm.humanoid?.getNormalizedBoneNode("head");
        const bustSpan = Math.max(0.18, cameraFraming.bustSpan || 0.27);
        projHeadVec.set(cameraFraming.centerX || 0, cameraFraming.headY || 1.45, cameraFraming.headZ || 0);
        if (headNode) headNode.getWorldPosition(headPosVec ? (headPosVec.copy(projHeadVec), projHeadVec) : projHeadVec);
        projHeadVec.y += bustSpan * 0.24;
        const worldY = projHeadVec.y;
        const worldX = projHeadVec.x;
        const worldZ = projHeadVec.z;
        projHeadVec.project(camera);
        if (Number.isFinite(projHeadVec.x) && Number.isFinite(projHeadVec.y)) {
          headX = (projHeadVec.x * 0.5 + 0.5) * w;
          headY = (-projHeadVec.y * 0.5 + 0.5) * h;
        }
        projHeadVec.set(worldX, worldY + bustSpan * 0.42, worldZ).project(camera);
        if (Number.isFinite(projHeadVec.y)) {
          const topPx = (-projHeadVec.y * 0.5 + 0.5) * h;
          const est = Math.abs(headY - topPx);
          if (Number.isFinite(est) && est > 12) headRadius = est;
        }
        const crownWorldY = Math.max(worldY + bustSpan * 0.58, (cameraFraming.topY || 0) - bustSpan * 0.08);
        projHeadVec.set(worldX, crownWorldY, worldZ).project(camera);
        if (Number.isFinite(projHeadVec.y)) {
          crownY = (-projHeadVec.y * 0.5 + 0.5) * h;
        }
      }

      fxCtx.setTransform?.(clampedDpr, 0, 0, clampedDpr, 0, 0);
      fxCtx.clearRect?.(0, 0, fullW, fullH);
      fxCtx.save?.();
      if (overlayMode && (R.left || R.top)) {
        fxCtx.translate?.(R.left, R.top);
      }
      if (hasDizzy) {
        C.vrmDizzyFx.drawDizzyFx(fxCtx, {
          width: w,
          height: h,
          headX,
          headY,
          headRadius,
          crownY,
          alpha: dizzyAlpha,
          time: Number.isFinite(coState.dizzyTime) ? coState.dizzyTime : elapsed,
          skipClear: overlayMode
        });
      }
      if (hasAction) {
        C.vrmDizzyFx.drawActionFx(fxCtx, {
          width: w,
          height: h,
          headX,
          headY,
          headRadius,
          crownY,
          kind: actionKind,
          alpha: actionAlpha,
          time: elapsed
        });
      }
      fxCtx.restore?.();
    }

    function readDocRid() {
      const rootEl = container?.ownerDocument?.documentElement;
      if (!rootEl) return "";
      const fromData = rootEl.dataset?.rid;
      if (typeof fromData === "string" && fromData.trim()) return fromData.trim();
      if (typeof rootEl.getAttribute === "function") {
        const attr = rootEl.getAttribute("data-rid");
        if (typeof attr === "string" && attr.trim()) return attr.trim();
      }
      return "";
    }

    function updatePoseAndLook(dt, currentGaze, physics, extra) {
      if (!vrm || !runtime) return null;
      const { THREE } = runtime;
      elapsed += dt;

      if (coLead) {
        const domRid = readDocRid();
        if (domRid) {
          const curRid = coLead.getCoLeadState().activeSessionId;
          if (!curRid) coLead.syncInitialSession(domRid);
          else if (domRid !== curRid) coLead.onSessionSwitch(domRid, { source: "shell-rid" });
        }
      }

      const gx = C.clamp(Number.isFinite(currentGaze?.x) ? currentGaze.x : gaze.x, -1, 1);
      const gy = C.clamp(Number.isFinite(currentGaze?.y) ? currentGaze.y : gaze.y, -1, 1);

      const vrmaKey = (activeGesture && pack?.animations && Object.hasOwn(pack.animations, activeGesture))
        ? activeGesture
        : (activePose && pack?.animations && Object.hasOwn(pack.animations, activePose))
          ? activePose : "";
      if (vrmaKey || activeVrmaAction) syncVrma(vrmaKey);

      let coState = null;
      if (activeVrmaAction && mixer) {
        exprCtrl.setCoLeadOverlay(null);
        mixer.update(dt);
        syncBoneCacheFromRig();
      } else {
        let headPitchCue = 0;
        if (activeExpression === "idle_doze") headPitchCue = 0.12 + Math.sin(elapsed * 0.85) * 0.04;
        else if (activeExpression === "cry") headPitchCue = 0.11;
        else if (activeExpression === "half_lid") headPitchCue = -0.03;
        else if (activeExpression === "oo") headPitchCue = -0.03;
        else if (activeExpression === "focused") headPitchCue = 0.02;

        let targets;
        if (coLead) {
          const domHover = !!(container?.closest?.(".ws-companion")?.dataset?.hover === "1");
          coState = coLead.step({
            dt,
            elapsed,
            expression: activeExpression,
            rawPose,
            rawGesture,
            scenarioId: extra?.playing?.entry?.id || "",
            beat: extra?.beat || null,
            gazeX: gx,
            gazeY: gy,
            headPitchCue,
            hover: domHover
          });
          exprCtrl.setCoLeadOverlay(coState.exprOverlay);
          targets = coState.boneTargets;
        } else {
          exprCtrl.setCoLeadOverlay(null);
          // Gentle damped head/neck follow-through toward gaze (suppressed during doze).
          const gazeScale = activeExpression === "idle_doze" ? 0.15 : 1.0;
          const headFollowAlpha = 1 - Math.exp(-5.2 * dt);
          const wantHeadYaw = gx * 12 * DEG * gazeScale;
          const wantHeadPitch = gy * 8 * DEG * gazeScale;
          smoothedHead.yaw += (wantHeadYaw - smoothedHead.yaw) * headFollowAlpha;
          smoothedHead.pitch += (wantHeadPitch - smoothedHead.pitch) * headFollowAlpha;
          targets = computeStandTargets(
            elapsed,
            headPitchCue,
            smoothedHead.yaw,
            smoothedHead.pitch,
            activeExpression === "focused" ? 0.45 : 1
          );
        }

        const lerpAlpha = 1 - Math.exp(-8.5 * dt);
        // Inline VRM 0.x conjugation ([-x, y, -z]) so the 45-bone loop allocates nothing.
        const conjSign = isVrm0 ? -1 : 1;
        for (const boneName of POSE_BONES) {
          const node = vrm.humanoid?.getNormalizedBoneNode(boneName);
          if (!node) continue;
          const rawTarget = targets[boneName] || ZERO_EULER;
          const tx = conjSign * (Number.isFinite(rawTarget[0]) ? rawTarget[0] : 0);
          const ty = Number.isFinite(rawTarget[1]) ? rawTarget[1] : 0;
          const tz = conjSign * (Number.isFinite(rawTarget[2]) ? rawTarget[2] : 0);
          let cur = boneRotations.get(boneName);
          if (!cur) {
            cur = [tx, ty, tz];
            boneRotations.set(boneName, cur);
          } else {
            cur[0] += (tx - cur[0]) * lerpAlpha;
            cur[1] += (ty - cur[1]) * lerpAlpha;
            cur[2] += (tz - cur[2]) * lerpAlpha;
          }
          node.rotation.set(cur[0], cur[1], cur[2], "XYZ");
        }

        // Clean gesture extension seam: invokes a registered gesture handler when present.
        const gestureHandler = activeGesture ? vrmGestures.get(activeGesture) : null;
        if (gestureHandler) {
          const ctx = {
            vrm,
            THREE,
            isVrm0,
            dt,
            elapsed,
            progress: Number.isFinite(extra?.progress) ? extra.progress : 0,
            expression: activeExpression,
            physics,
            extra,
            setBoneEuler
          };
          if (typeof gestureHandler === "function") gestureHandler(ctx);
          else gestureHandler.update(ctx);
        }
      }

      // Clamped LookAt eye tracking wired to Co-Lead gaze choreography or normalized gaze.{x, y}.
      if (vrm.lookAt && lookTarget) {
        const headNode = vrm.humanoid?.getNormalizedBoneNode("head");
        const headPos = headPosVec || (headPosVec = new THREE.Vector3());
        headPos.set(0, cameraFraming.headY, 0);
        if (headNode) headNode.getWorldPosition(headPos);
        const targetYaw = coState ? coState.eyeYaw : gx * 26 * DEG;
        const targetPitch = coState ? coState.eyePitch : -gy * 16 * DEG;
        const la = 1 - Math.exp(-9.0 * dt);
        smoothedLook.yaw += (targetYaw - smoothedLook.yaw) * la;
        smoothedLook.pitch += (targetPitch - smoothedLook.pitch) * la;
        const dist = 1.6;
        lookTarget.position.set(
          headPos.x + Math.sin(smoothedLook.yaw) * dist,
          headPos.y + Math.sin(smoothedLook.pitch) * dist,
          headPos.z + Math.cos(smoothedLook.yaw) * Math.cos(smoothedLook.pitch) * dist
        );
      }
      return coState;
    }

    function renderTick(dt, currentGaze, physics, extra) {
      if (disposed || paused || contextLost || !renderer || !scene || !camera || !vrm) return;
      const clampedDt = Math.min(Math.max(dt, 0.001), 0.1);
      const coState = updatePoseAndLook(clampedDt, currentGaze, physics, extra);
      exprCtrl.update(clampedDt);
      vrm.update(clampedDt);
      // Lazy palm probe: sampled only while the phone is held (VRM 0.x/1.0 palm frame).
      if (palmSampler && typeof coLead?.syncPhoneToHand === "function") coLead.syncPhoneToHand(palmSampler);
      const { rect: R, bounds, primaryBounds } = computeActiveScissorRect();
      activeScissorRect = R;
      if (overlayMode) {
        if (needsFullClear) {
          renderer.setScissorTest?.(false);
          renderer.setViewport?.(0, 0, bounds.vw, bounds.vh);
          renderer.clear?.(true, true, true);
          needsFullClear = false;
        } else if (prevScissorRect && !C.vrmOverlayMath.rectContains(R, prevScissorRect)) {
          const pvy = Math.max(0, bounds.vh - (prevScissorRect.top + prevScissorRect.height));
          renderer.setScissorTest?.(true);
          renderer.setScissor?.(prevScissorRect.left, pvy, prevScissorRect.width, prevScissorRect.height);
          renderer.setViewport?.(prevScissorRect.left, pvy, prevScissorRect.width, prevScissorRect.height);
          renderer.clear?.(true, true, true);
          shrinkClearCount++;
        }
        const rvy = Math.max(0, bounds.vh - (R.top + R.height));
        renderer.setScissorTest?.(true);
        renderer.setScissor?.(R.left, rvy, R.width, R.height);
        renderer.setViewport?.(R.left, rvy, R.width, R.height);
        applyAsymmetricProjection(R);
        prevScissorRect = { left: R.left, top: R.top, width: R.width, height: R.height };
      }
      renderer.render(scene, camera);
      if (calibFrames > 0 && renderMode === "docked" && --calibFrames === 0) calibrateHeadroom(R, bounds);
      if (renderMode === "roam" && primaryBounds) {
        cachedCharBounds.viewportX = primaryBounds.left;
        cachedCharBounds.viewportY = primaryBounds.top;
        cachedCharBounds.x = Math.round(primaryBounds.left - anchorRect.left);
        cachedCharBounds.y = Math.round(primaryBounds.top - anchorRect.top);
        cachedCharBounds.width = primaryBounds.width;
        cachedCharBounds.height = primaryBounds.height;
        hasCachedCharBounds = true;
        charBoundsDirty = false;
        lastCharBoundsAt = performance.now();
      } else if (charBoundsDirty) {
        updateCharacterBounds(performance.now());
      }
      renderDizzyFxOverlay(coState);
      framesRendered++;
    }

    // Project posed vrm.scene character meshes ONLY (laptopGroup and phoneGroup live
    // directly under scene, never vrm.scene) into active viewport rect R.
    function updateCharacterBounds(nowMs) {
      lastCharBoundsAt = nowMs;
      charBoundsDirty = false;
      if (disposed || !vrm?.scene || !camera || !projMeshBox || !projCornerVec || typeof projCornerVec.project !== "function") {
        hasCachedCharBounds = false;
        return false;
      }
      const R = activeScissorRect || { left: 0, top: 0, width: Math.max(64, Math.round(width)), height: Math.max(64, Math.round(height)) };
      const w = Math.max(1, Math.round(R.width));
      const h = Math.max(1, Math.round(R.height));
      if (w <= 0 || h <= 0) {
        hasCachedCharBounds = false;
        return false;
      }
      if (overlayMode) applyAsymmetricProjection(R);
      vrm.scene.updateMatrixWorld?.(true);
      camera.updateMatrixWorld?.();

      let uMinX = Infinity, uMinY = Infinity, uMaxX = -Infinity, uMaxY = -Infinity;
      let anyVisible = false;

      vrm.scene.traverse(obj => {
        if (!obj || !obj.isMesh || obj.visible === false || !obj.geometry) return;
        if (obj.isSkinnedMesh && typeof obj.computeBoundingBox === "function") {
          obj.computeBoundingBox();
          if (!obj.boundingBox || obj.boundingBox.isEmpty()) return;
          projMeshBox.copy(obj.boundingBox).applyMatrix4(obj.matrixWorld);
        } else {
          if (!obj.geometry.boundingBox && typeof obj.geometry.computeBoundingBox === "function") {
            obj.geometry.computeBoundingBox();
          }
          if (!obj.geometry.boundingBox || obj.geometry.boundingBox.isEmpty()) return;
          projMeshBox.copy(obj.geometry.boundingBox).applyMatrix4(obj.matrixWorld);
        }
        const min = projMeshBox.min, max = projMeshBox.max;
        if (!Number.isFinite(min.x) || !Number.isFinite(min.y) || !Number.isFinite(min.z) ||
            !Number.isFinite(max.x) || !Number.isFinite(max.y) || !Number.isFinite(max.z)) {
          return;
        }

        let mMinX = Infinity, mMinY = Infinity, mMaxX = -Infinity, mMaxY = -Infinity;
        let projectedCount = 0;
        for (let i = 0; i < 8; i++) {
          projCornerVec.set(
            (i & 1) ? max.x : min.x,
            (i & 2) ? max.y : min.y,
            (i & 4) ? max.z : min.z
          ).project(camera);
          if (!Number.isFinite(projCornerVec.x) || !Number.isFinite(projCornerVec.y) || !Number.isFinite(projCornerVec.z)) continue;
          if (projCornerVec.z < -1 || projCornerVec.z > 1) continue;
          const sx = (projCornerVec.x * 0.5 + 0.5) * w;
          const sy = (-projCornerVec.y * 0.5 + 0.5) * h;
          if (sx < mMinX) mMinX = sx;
          if (sx > mMaxX) mMaxX = sx;
          if (sy < mMinY) mMinY = sy;
          if (sy > mMaxY) mMaxY = sy;
          projectedCount++;
        }
        if (!projectedCount || mMaxX <= 0 || mMinX >= w || mMaxY <= 0 || mMinY >= h) return;

        const cMinX = mMinX < 0 ? 0 : mMinX;
        const cMaxX = mMaxX > w ? w : mMaxX;
        const cMinY = mMinY < 0 ? 0 : mMinY;
        const cMaxY = mMaxY > h ? h : mMaxY;
        if (cMaxX <= cMinX || cMaxY <= cMinY) return;

        if (cMinX < uMinX) uMinX = cMinX;
        if (cMaxX > uMaxX) uMaxX = cMaxX;
        if (cMinY < uMinY) uMinY = cMinY;
        if (cMaxY > uMaxY) uMaxY = cMaxY;
        anyVisible = true;
      });

      if (!anyVisible || uMaxX <= uMinX || uMaxY <= uMinY) {
        hasCachedCharBounds = false;
        return false;
      }

      const x1 = C.clamp(Math.floor(uMinX), 0, w);
      const y1 = C.clamp(Math.floor(uMinY), 0, h);
      const x2 = C.clamp(Math.ceil(uMaxX), 0, w);
      const y2 = C.clamp(Math.ceil(uMaxY), 0, h);
      if (x2 <= x1 || y2 <= y1) {
        hasCachedCharBounds = false;
        return false;
      }
      const vpX = R.left + x1;
      const vpY = R.top + y1;
      cachedCharBounds.viewportX = vpX;
      cachedCharBounds.viewportY = vpY;
      cachedCharBounds.x = overlayMode ? Math.round(vpX - anchorRect.left) : x1;
      cachedCharBounds.y = overlayMode ? Math.round(vpY - anchorRect.top) : y1;
      cachedCharBounds.width = x2 - x1;
      cachedCharBounds.height = y2 - y1;
      hasCachedCharBounds = true;
      return true;
    }

    function getCharacterBounds() {
      if (disposed || !vrm || !camera) return null;
      const nowMs = performance.now();
      if (charBoundsDirty || lastCharBoundsAt === -Infinity || nowMs - lastCharBoundsAt >= CHAR_BOUNDS_TTL_MS) {
        updateCharacterBounds(nowMs);
      }
      return hasCachedCharBounds
        ? {
            x: cachedCharBounds.x,
            y: cachedCharBounds.y,
            width: cachedCharBounds.width,
            height: cachedCharBounds.height,
            viewportX: cachedCharBounds.viewportX,
            viewportY: cachedCharBounds.viewportY
          }
        : null;
    }

    function raycastAt(clientX, clientY) {
      if (disposed || !vrm?.scene || !camera || !Number.isFinite(clientX) || !Number.isFinite(clientY)) {
        return { hit: false, area: "none", clientX, clientY };
      }
      const R = activeScissorRect || anchorRect;
      if (clientX < R.left || clientX > R.left + R.width || clientY < R.top || clientY > R.top + R.height) {
        return { hit: false, area: "outside", clientX, clientY };
      }
      if (raycaster && rayNdcVec && typeof raycaster.setFromCamera === "function" && typeof raycaster.intersectObject === "function") {
        if (overlayMode) applyAsymmetricProjection(R);
        vrm.scene.updateMatrixWorld?.(true);
        camera.updateMatrixWorld?.();
        const ndcX = ((clientX - R.left) / Math.max(1, R.width)) * 2 - 1;
        const ndcY = -(((clientY - R.top) / Math.max(1, R.height)) * 2 - 1);
        rayNdcVec.set(ndcX, ndcY);
        raycaster.setFromCamera(rayNdcVec, camera);
        const hits = raycaster.intersectObject(vrm.scene, true);
        if (Array.isArray(hits) && hits.length > 0) {
          return { hit: true, area: "character", distance: hits[0].distance ?? 0, objectName: hits[0].object?.name || "", clientX, clientY };
        }
        return { hit: false, area: "messenger", clientX, clientY };
      }
      const bounds = getCharacterBounds();
      if (bounds) {
        const inside = clientX >= bounds.viewportX && clientX <= bounds.viewportX + bounds.width &&
          clientY >= bounds.viewportY && clientY <= bounds.viewportY + bounds.height;
        return { hit: inside, area: inside ? "character" : "messenger", clientX, clientY };
      }
      return { hit: false, area: "messenger", clientX, clientY };
    }

    async function mount(targetContainer, mountOptions = {}) {
      if (disposed || mounted) return;
      mounted = true;
      container = targetContainer;
      if (mountOptions.viewport && Number.isFinite(mountOptions.viewport.width) && Number.isFinite(mountOptions.viewport.height)) {
        overlayMode = true;
        viewport = {
          width: Math.max(64, Math.round(mountOptions.viewport.width)),
          height: Math.max(64, Math.round(mountOptions.viewport.height)),
          dpr: Math.min(Math.max(Number.isFinite(mountOptions.viewport.dpr) ? mountOptions.viewport.dpr : (mountOptions.dpr || 1), 1), 2)
        };
        dpr = viewport.dpr;
        anchorRect = normalizeAnchorRect(mountOptions.anchorRect || { left: 0, top: 0, width: mountOptions.width, height: mountOptions.height });
        width = anchorRect.width;
        height = anchorRect.height;
      } else {
        overlayMode = false;
        if (Number.isFinite(mountOptions.width)) width = mountOptions.width;
        if (Number.isFinite(mountOptions.height)) height = mountOptions.height;
        if (Number.isFinite(mountOptions.dpr)) dpr = mountOptions.dpr;
        viewport = { width, height, dpr: Math.min(Math.max(dpr || 1, 1), 2) };
        anchorRect = normalizeAnchorRect({ left: 0, top: 0, width, height });
      }
      activeScissorRect = { left: anchorRect.left, top: anchorRect.top, width: anchorRect.width, height: anchorRect.height };
      syncSlotStyleVars();

      const doc = container.ownerDocument || document;
      canvasWrap = doc.createElement("div");
      canvasWrap.className = "ws-companion-vrm-canvas-wrap";
      canvas = doc.createElement("canvas");
      canvas.className = "ws-companion-vrm-canvas";
      canvas.setAttribute("aria-hidden", "true");
      canvasWrap.append(canvas);

      dissolveGrid = doc.createElement("div");
      dissolveGrid.className = "ws-dissolve-grid";
      dissolveGrid.setAttribute("aria-hidden", "true");

      fxCanvas = doc.createElement("canvas");
      fxCanvas.className = "ws-companion-vrm-fx";
      fxCanvas.setAttribute("aria-hidden", "true");
      fxCanvas.hidden = true;
      if (fxCanvas.style) {
        fxCanvas.style.position = "absolute";
        fxCanvas.style.inset = "0";
        fxCanvas.style.width = "100%";
        fxCanvas.style.height = "100%";
        fxCanvas.style.pointerEvents = "none";
        fxCanvas.style.zIndex = "1";
      }
      fxCtx = typeof fxCanvas.getContext === "function" ? fxCanvas.getContext("2d") : null;
      if (overlayMode) {
        fxCanvas.width = 0;
        fxCanvas.height = 0;
      } else {
        syncFxCanvasSize();
      }

      overlay = doc.createElement("div");
      overlay.className = "ws-companion-vrm-overlay";
      overlay.hidden = true;

      container.replaceChildren(canvasWrap, dissolveGrid, fxCanvas, overlay);

      // Sync initial session ID silently (no swap animation) and observe shell data-rid changes.
      const initialRid = readDocRid();
      if (initialRid) {
        pendingInitialRid = initialRid;
        coLead?.syncInitialSession(initialRid);
      }
      if (typeof MutationObserver === "function" && doc?.documentElement) {
        try {
          ridObserver = new MutationObserver(() => {
            if (disposed) return;
            const nextRid = readDocRid();
            if (nextRid) rigApi.onSessionSwitch(nextRid, { source: "shell-rid" });
          });
          ridObserver.observe(doc.documentElement, { attributes: true, attributeFilter: ["data-rid"] });
        } catch (_) {
          ridObserver = null;
        }
      }

      async function ensureCachedModel() {
        const cacheKey = (pack?.id || "") + ":" + (pack?.rev || "");
        if (cachedModel && cachedModel.key === cacheKey) return cachedModel;
        if (!modelFetchPromise) {
          modelFetchCount++;
          modelFetchPromise = C.fetchPackAsset(pack, pack.modelFile, loadCancel.signal)
            .then(fetched => {
              cachedModel = { key: (pack?.id || "") + ":" + fetched.rev, buffer: fetched.buffer };
              return cachedModel;
            })
            .finally(() => { modelFetchPromise = null; });
        }
        return modelFetchPromise;
      }

      async function loadAndParse(isRestore) {
        const myEpoch = ++loadEpoch;
        try {
          if (!runtime) runtime = await loadVrmRuntime();
          if (disposed || contextLost || myEpoch !== loadEpoch) return;
          if (isRestore) {
            // Do not call forceContextLoss() on the newly restored canvas context.
            disposeRendererOnly(false);
          }
          if (!renderer) initSceneAndRenderer(runtime);
          const cached = await ensureCachedModel();
          if (disposed || contextLost || myEpoch !== loadEpoch) return;
          if (!renderer) initSceneAndRenderer(runtime);
          await parseVrmBuffer(cached.buffer);
        } catch (err) {
          if (!disposed && !contextLost && !loadCancel.signal.aborted && myEpoch === loadEpoch) {
            setOverlay(isRestore ? "VRM restore failed" : "VRM unavailable");
            if (!isRestore) options.onError?.(err);
          }
        }
      }

      listenersCancel = new AbortController();
      const sig = { signal: listenersCancel.signal };
      canvas.addEventListener("webglcontextlost", event => {
        event.preventDefault();
        contextLost = true;
        setOverlay("Paused");
        options.onContextLost?.();
      }, sig);
      canvas.addEventListener("webglcontextrestored", () => {
        if (disposed) return;
        contextLost = false;
        setOverlay("");
        options.onContextRestored?.();
        loadAndParse(true);
      }, sig);

      await loadAndParse(false);
    }

    const viewObj = {
      pose: "stand",
      gesture: null,
      expression: "idle",
      props: [],
      handAnchor: [LOGICAL_W * 0.5, LOGICAL_H * 0.62],
      articulatedDrag: false,
      seat: 1,
      bounds: [0, 0, 1, 1],
      render(seconds, currentGaze, physics, extra) {
        const dt = lastSeconds && seconds >= lastSeconds ? Math.max(0.001, seconds - lastSeconds) : 0.016;
        lastSeconds = seconds;
        activeBeatMeta = extra || null;
        if (currentGaze) {
          gaze.x = Number.isFinite(currentGaze.x) ? currentGaze.x : 0;
          gaze.y = Number.isFinite(currentGaze.y) ? currentGaze.y : 0;
        }
        renderTick(dt, gaze, physics, extra);
        return canvas;
      }
    };

    const rigApi = {
      renderer: "vrm",
      width: LOGICAL_W,
      height: LOGICAL_H,
      mount(targetContainer, mountOptions) {
        activeVrmRig = rigApi;
        return mount(targetContainer, mountOptions);
      },
      view(pose, gesture, expression, props) {
        const prevGesture = rawGesture;
        rawPose = typeof pose === "string" && pose ? pose : "stand";
        rawGesture = typeof gesture === "string" && gesture ? gesture : null;
        const resolved = resolveScenario("", { pose, gesture, expression, prop: Array.isArray(props) ? props[0] : null }, pack);
        activePose = resolved.pose;
        activeGesture = resolved.gesture;
        activeExpression = resolved.expression;
        exprCtrl.setState(activeExpression);
        if (rawGesture && rawGesture !== prevGesture && Object.hasOwn(BUILTIN_VRM_GESTURES, rawGesture)) {
          if (coLead) coLead.triggerGesture(rawGesture);
          else bufferGesture(rawGesture);
        }
        viewObj.pose = activePose;
        viewObj.gesture = activeGesture;
        viewObj.expression = activeExpression;
        viewObj.props = [];
        return viewObj;
      },
      prop() {
        return { data: null, drawable: false, draw() {} };
      },
      setGaze(next) {
        if (!next) return;
        gaze.x = C.clamp(Number.isFinite(next.x) ? next.x : 0, -1, 1);
        gaze.y = C.clamp(Number.isFinite(next.y) ? next.y : 0, -1, 1);
      },
      setLaptopAccent(hex) {
        pendingAccent = hex;
        return coLead ? coLead.setLaptopAccent(hex) : (C.vrmCoLeadMath?.normalizeHexColor(hex) || null);
      },
      onSessionSwitch(sessionId, meta) {
        const rid = typeof sessionId === "string" ? sessionId.trim() : "";
        if (!rid) return coLead ? coLead.getCoLeadState() : null;
        if (!coLead) {
          pendingSessionSwitch = { rid, meta };
          return {
            activeSessionId: rid,
            laptopAccent: C.vrmCoLeadMath?.sessionAccentForKey?.(rid)?.hex || null,
            swapped: false,
            dizzyTriggered: false
          };
        }
        return coLead.onSessionSwitch(rid, meta);
      },
      getCoLeadState() {
        return coLead ? coLead.getCoLeadState() : null;
      },
      setCoLeadBeat(beat) {
        pendingCoLeadBeat = beat;
        return coLead ? coLead.setCoLeadBeat(beat) : String(beat || "auto");
      },
      setHover(flag) {
        pendingHover = flag;
        coLead?.setHover(flag);
      },
      triggerKnock(opts) {
        return coLead ? coLead.triggerKnock(opts) : null;
      },
      triggerGesture(name) {
        if (!name) return false;
        if (coLead) return coLead.triggerGesture(name);
        bufferGesture(String(name));
        return true;
      },
      setPhoneState(snap) {
        pendingPhoneState = snap || null;
        pendingPhoneOrder = ++pendingOrder;
        if (!coLead && /^phone_/.test(pendingGesture)) {
          // A newer phone snapshot is authoritative over a buffered phone
          // gesture (a stale pickup must not replay over a putdown).
          pendingGesture = "";
        }
        return coLead ? coLead.setPhoneState(snap) : null;
      },
      setViewport(vw, vh, nextDpr) {
        const nextW = Number.isFinite(vw) && vw > 0 ? Math.max(64, Math.round(vw)) : viewport.width;
        const nextH = Number.isFinite(vh) && vh > 0 ? Math.max(64, Math.round(vh)) : viewport.height;
        const clampedDpr = Number.isFinite(nextDpr) && nextDpr > 0 ? Math.min(Math.max(nextDpr, 1), 2) : viewport.dpr;
        const changed = !overlayMode || nextW !== viewport.width || nextH !== viewport.height || clampedDpr !== viewport.dpr;
        overlayMode = true;
        viewport = { width: nextW, height: nextH, dpr: clampedDpr };
        dpr = clampedDpr;
        if (changed) {
          applyViewportResize();
          charBoundsDirty = true;
          lastCharBoundsAt = -Infinity;
        }
        return changed;
      },
      setAnchorRect(rect) {
        if (!rect || !Number.isFinite(rect.width) || !Number.isFinite(rect.height) || rect.width <= 0 || rect.height <= 0) {
          return false;
        }
        const prevAspect = anchorRect.width / Math.max(1, anchorRect.height);
        const next = normalizeAnchorRect(rect);
        const changed = Math.abs(next.left - anchorRect.left) > 0.5 ||
          Math.abs(next.top - anchorRect.top) > 0.5 ||
          Math.abs(next.width - anchorRect.width) > 0.5 ||
          Math.abs(next.height - anchorRect.height) > 0.5;
        anchorRect = next;
        width = next.width;
        height = next.height;
        syncSlotStyleVars();
        refreshChromeBounds();
        if (changed) {
          anchorUpdateCount++;
          const nextAspect = anchorRect.width / Math.max(1, anchorRect.height);
          if (vrm && Math.abs(nextAspect - prevAspect) > 0.01) {
            autoFrameBust(vrm);
          } else if (vrm && coLead) {
            applyCanonicalSlotProjection();
            coLead.layoutProp(cameraFraming, camera);
          }
          charBoundsDirty = true;
          lastCharBoundsAt = -Infinity;
        }
        return changed;
      },
      setRenderMode(mode) {
        const next = mode === "bleed" || mode === "roam" ? mode : "docked";
        if (renderMode === next) return renderMode;
        renderMode = next;
        syncSlotStyleVars();
        refreshChromeBounds();
        coLead?.setBreakoutState?.(renderMode);
        charBoundsDirty = true;
        lastCharBoundsAt = -Infinity;
        return renderMode;
      },
      setBleedPadding(pad) {
        if (typeof pad === "number" && Number.isFinite(pad)) {
          const v = Math.max(0, Math.round(pad));
          bleedPadding = { top: v, right: v, bottom: v, left: v };
        } else if (pad && typeof pad === "object") {
          bleedPadding = {
            top: Number.isFinite(pad.top) ? Math.max(0, Math.round(pad.top)) : bleedPadding.top,
            right: Number.isFinite(pad.right) ? Math.max(0, Math.round(pad.right)) : bleedPadding.right,
            bottom: Number.isFinite(pad.bottom) ? Math.max(0, Math.round(pad.bottom)) : bleedPadding.bottom,
            left: Number.isFinite(pad.left) ? Math.max(0, Math.round(pad.left)) : bleedPadding.left
          };
        }
        return { ...bleedPadding };
      },
      getRenderMode() {
        return renderMode;
      },
      getRenderMetrics() {
        return {
          overlayMode,
          mode: renderMode,
          viewport: { ...viewport },
          anchorRect: { ...anchorRect },
          scissorRect: { ...activeScissorRect },
          prevScissorRect: prevScissorRect ? { ...prevScissorRect } : null,
          bleedPadding: { ...bleedPadding },
          viewportResizeCount,
          anchorUpdateCount,
          shrinkClearCount,
          topbarHeight: lastTopbarH,
          botbarHeight: lastBotbarH
        };
      },
      raycastAt,
      resize(w, h, nextDpr) {
        if (overlayMode) {
          if (Number.isFinite(nextDpr) && nextDpr > 0 && Math.min(Math.max(nextDpr, 1), 2) !== viewport.dpr) {
            rigApi.setViewport(viewport.width, viewport.height, nextDpr);
          }
          if (Number.isFinite(w) && w > 0 && Number.isFinite(h) && h > 0) {
            rigApi.setAnchorRect({ left: anchorRect.left, top: anchorRect.top, width: w, height: h });
          }
          return;
        }
        const prevAspect = width / Math.max(1, height);
        if (Number.isFinite(w) && w > 0) width = w;
        if (Number.isFinite(h) && h > 0) height = h;
        if (Number.isFinite(nextDpr) && nextDpr > 0) dpr = nextDpr;
        viewport = { width, height, dpr: Math.min(Math.max(dpr || 1, 1), 2) };
        anchorRect = normalizeAnchorRect({ left: 0, top: 0, width, height });
        activeScissorRect = { left: 0, top: 0, width: anchorRect.width, height: anchorRect.height };
        applyViewportResize();
        const nextAspect = width / Math.max(1, height);
        if (vrm && Math.abs(nextAspect - prevAspect) > 0.01) autoFrameBust(vrm);
        else if (vrm && coLead) coLead.layoutProp(cameraFraming, camera);
        charBoundsDirty = true;
        lastCharBoundsAt = -Infinity;
      },
      getCharacterBounds,
      pause() { paused = true; },
      resume() { paused = false; },
      isContextLost() { return contextLost; },
      setPreset() {},
      setWardrobe() {},
      wardrobe() { return null; },
      resolveScenario: (id, beat) => resolveScenario(id, beat, pack),
      debugState() {
        return {
          loaded: !!vrm,
          vrmVersion: exprCtrl.version,
          isVrm0,
          pose: activePose,
          gesture: activeGesture,
          expression: activeExpression,
          prop: null,
          activeVrmaKey,
          surprisedStrategy: exprCtrl.surprisedStrategy,
          framing: { ...cameraFraming },
          projectedBounds: getCharacterBounds(),
          renderMetrics: rigApi.getRenderMetrics(),
          coLead: coLead ? coLead.debugState() : null,
          studio: studio ? studio.getState() : null,
          fingerBoneCount,
          headroomCalibPending: calibFrames > 0,
          slotPoints: runtime && vrm ? C.vrmStudio.measureSlotPoints(runtime.THREE, camera, vrm, coLead?.getPropObjects?.()) : null,
          contextLost,
          paused,
          disposed,
          framesRendered,
          modelFetchCount,
          drawCalls: renderer?.info?.render?.calls ?? 0,
          triangles: renderer?.info?.render?.triangles ?? 0
        };
      },
      dispose() {
        if (disposed) return;
        disposed = true;
        if (activeVrmRig === rigApi) activeVrmRig = null;
        loadEpoch++;
        loadCancel.abort();
        listenersCancel?.abort();
        listenersCancel = null;
        ridObserver?.disconnect();
        ridObserver = null;
        disposeRendererOnly(true);
        cachedModel = null;
        modelFetchPromise = null;
        if (container) container.replaceChildren();
        container = canvasWrap = canvas = dissolveGrid = fxCanvas = fxCtx = overlay = null;
      }
    };
    return rigApi;
  }

  Object.assign(C, {
    makeVrmRig,
    vrmVocabulary,
    registerVrmGesture,
    unregisterVrmGesture,
    setLaptopAccent(hex) { return activeVrmRig?.setLaptopAccent?.(hex) ?? null; },
    onSessionSwitch(sessionId, meta) { return activeVrmRig?.onSessionSwitch?.(sessionId, meta) ?? null; },
    getCoLeadState() { return activeVrmRig?.getCoLeadState?.() ?? null; },
    setCoLeadBeat(beat) { return activeVrmRig?.setCoLeadBeat?.(beat) ?? null; },
    setVrmHover(flag) { activeVrmRig?.setHover?.(flag); },
    triggerKnock(opts) { return activeVrmRig?.triggerKnock?.(opts) ?? null; },
    triggerGesture(name) { return activeVrmRig?.triggerGesture?.(name) ?? false; },
    setPhoneState(snap) { return activeVrmRig?.setPhoneState?.(snap) ?? null; },
    setVrmRenderMode(mode) { return activeVrmRig?.setRenderMode?.(mode) ?? "docked"; },
    setVrmBleedPadding(pad) { return activeVrmRig?.setBleedPadding?.(pad) ?? null; },
    getVrmRenderMetrics() { return activeVrmRig?.getRenderMetrics?.() ?? null; },
    raycastVrmAt(x, y) { return activeVrmRig?.raycastAt?.(x, y) ?? null; },
    VrmExpressionController,
    resolveVrmScenario: resolveScenario,
    vrmMath: { conjugateVrm0Euler, conjugateVrm0Quat }
  });
})(window.WsCompanion);
