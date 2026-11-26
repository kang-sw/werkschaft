// Pure-VRM Companion Bust Spike
// Imports bundled three + @pixiv/three-vrm + @pixiv/three-vrm-animation runtime.

async function loadVrmRuntime() {
  try {
    return await import('/vendor/vrm-runtime.mjs');
  } catch {
    return await import('./vendor/vrm-runtime.mjs');
  }
}

const {
  THREE,
  GLTFLoader,
  VRMLoaderPlugin,
  VRMUtils,
  VRMAnimationLoaderPlugin,
  createVRMAnimationClip,
} = await loadVrmRuntime();

const SAMPLE_VRM_URL = '/samples/VRM1_Constraint_Twist_Sample.vrm';
const IDLE_TIMEOUT_MS = 5000;
const DEG = Math.PI / 180;

// ============================================================================
// Unified VRM 0.x & 1.0 Expression Controller (7 Werkschaft expressions)
// ============================================================================
class VrmExpressionController {
  constructor() {
    this.vrm = null;
    this.version = '--';
    this.presets = [];
    this.customs = [];
    this.available = new Set();
    this.currentState = 'idle';
    this.weights = new Map();
    this.blinkTimer = 2.4;
    this.blinkDuration = 0.14;
    this.blinkProgress = -1;
    this.elapsed = 0;
    this.surprisedStrategy = 'preset:surprised';
  }

  attach(vrm) {
    this.vrm = vrm;
    this.weights.clear();
    this.available.clear();
    this.presets = [];
    this.customs = [];

    if (!vrm) {
      this.version = '--';
      return;
    }

    const mv = vrm.meta?.metaVersion;
    this.version = mv === '1' ? '1.0' : '0.x';

    const mgr = vrm.expressionManager;
    if (mgr) {
      const presetMap = mgr.presetExpressionMap || {};
      const customMap = mgr.customExpressionMap || {};
      this.presets = Object.keys(presetMap).sort();
      this.customs = Object.keys(customMap).sort();
      for (const name of [...this.presets, ...this.customs]) {
        this.available.add(name);
        this.weights.set(name, 0);
      }
    }

    if (this.has('surprised')) {
      this.surprisedStrategy = 'preset:surprised';
    } else if (this.has('Surprised')) {
      this.surprisedStrategy = 'custom:Surprised';
    } else if (this.has('Fcl_ALL_Surprised')) {
      this.surprisedStrategy = 'custom:Fcl_ALL_Surprised';
    } else {
      this.surprisedStrategy = 'fallback:oh+wide';
    }
  }

  has(name) {
    return this.available.has(name);
  }

  resolveAlias(...candidates) {
    for (const name of candidates) {
      if (this.has(name)) return name;
    }
    return null;
  }

  setState(state) {
    this.currentState = state;
  }

  getBadgeInfo() {
    switch (this.currentState) {
      case 'idle_doze':
        return { kind: 'zzz', label: 'Zzz' };
      case 'half_lid':
        return { kind: 'ponder', label: 'Ponder...' };
      case 'oo':
        return { kind: 'alert', label: '! Alert' };
      case 'happy':
        return { kind: 'sparkle', label: '✦ Sparkle' };
      default:
        return { kind: '', label: '' };
    }
  }

  // Returns gaze override in radians { yaw, pitch, blend } and head droop pitch
  getGazeAndHeadCue(t) {
    switch (this.currentState) {
      case 'idle_doze':
        return { yaw: 0, pitch: -0.22 + Math.sin(t * 1.1) * 0.05, overrideGaze: true, headPitch: 0.14 + Math.sin(t * 1.1) * 0.045 };
      case 'focused':
        return { yaw: Math.sin(t * 0.9) * 0.22, pitch: -0.04, overrideGaze: true, headPitch: 0.02 };
      case 'half_lid':
        return { yaw: 0.06, pitch: 0.18, overrideGaze: true, headPitch: -0.04 };
      case 'cry':
        return { yaw: 0, pitch: -0.24, overrideGaze: true, headPitch: 0.13 };
      case 'oo':
        return { yaw: 0, pitch: 0.04, overrideGaze: false, headPitch: -0.04 };
      default:
        return { yaw: 0, pitch: 0, overrideGaze: false, headPitch: 0 };
    }
  }

  update(dt) {
    if (!this.vrm?.expressionManager) return;
    this.elapsed += dt;

    // Auto-blink at ~0.23 Hz (~4.35s period) for idle
    let autoBlink = 0;
    if (this.currentState === 'idle') {
      if (this.blinkProgress >= 0) {
        this.blinkProgress += dt;
        const half = this.blinkDuration * 0.5;
        if (this.blinkProgress >= this.blinkDuration) {
          this.blinkProgress = -1;
          this.blinkTimer = 3.8 + Math.random() * 1.1; // avg ~4.35s => 0.23 Hz
        } else {
          autoBlink =
            this.blinkProgress < half
              ? this.blinkProgress / half
              : 1 - (this.blinkProgress - half) / half;
        }
      } else {
        this.blinkTimer -= dt;
        if (this.blinkTimer <= 0) {
          this.blinkProgress = 0;
        }
      }
    } else {
      this.blinkProgress = -1;
    }

    const targets = new Map();
    const setTarget = (name, val) => {
      if (!name) return;
      targets.set(name, Math.max(0, Math.min(1, val)));
    };

    const kNeutral = this.resolveAlias('neutral');
    const kBlink = this.resolveAlias('blink');
    const kRelaxed = this.resolveAlias('relaxed', 'fun');
    const kAngry = this.resolveAlias('angry');
    const kSad = this.resolveAlias('sad', 'sorrow');
    const kHappy = this.resolveAlias('happy', 'joy');
    const kSurprised = this.resolveAlias('surprised', 'Surprised', 'Fcl_ALL_Surprised');
    const kOh = this.resolveAlias('oh', 'o');

    switch (this.currentState) {
      case 'idle':
        setTarget(kNeutral, 1.0);
        setTarget(kBlink, autoBlink);
        break;
      case 'idle_doze':
        setTarget(kBlink, 1.0);
        setTarget(kRelaxed, 0.4);
        break;
      case 'focused':
        setTarget(kNeutral, 0.85);
        setTarget(kBlink, 0.15);
        setTarget(kAngry, 0.12);
        break;
      case 'half_lid':
        setTarget(kBlink, 0.5);
        setTarget(kRelaxed, 0.2);
        break;
      case 'oo':
        if (kSurprised) {
          setTarget(kSurprised, 0.95);
        } else {
          setTarget(kOh, 0.6);
          setTarget(kBlink, 0.0);
        }
        break;
      case 'cry':
        setTarget(kSad, 0.9);
        setTarget(kBlink, 0.22);
        break;
      case 'happy':
        setTarget(kHappy, 0.92);
        break;
      default:
        setTarget(kNeutral, 1.0);
    }

    const mgr = this.vrm.expressionManager;
    const lerpRate = Math.min(1, dt * 11);
    for (const name of this.available) {
      const target = targets.get(name) ?? 0;
      const prev = this.weights.get(name) ?? 0;
      const next = Math.abs(target - prev) < 0.002 ? target : prev + (target - prev) * lerpRate;
      this.weights.set(name, next);
      mgr.setValue(name, next);
    }
  }

  getDiagnostics() {
    const activeWeights = [];
    for (const [k, v] of this.weights.entries()) {
      if (v > 0.015) activeWeights.push([k, Number(v.toFixed(2))]);
    }
    activeWeights.sort((a, b) => b[1] - a[1]);
    return {
      version: this.version,
      presets: this.presets,
      customs: this.customs,
      activeWeights,
      surprisedStrategy: this.surprisedStrategy,
    };
  }
}

// ============================================================================
// Procedural Upper-Body Pose & Gesture Controller + Clamped LookAt
// ============================================================================
const POSE_BONES = [
  'spine',
  'chest',
  'neck',
  'head',
  'leftUpperArm',
  'leftLowerArm',
  'rightUpperArm',
  'rightLowerArm',
  'leftHand',
  'rightHand',
];

function computePoseTargets(pose, t, headPitchCue) {
  const breath = Math.sin(t * 1.7);
  const sway = Math.sin(t * 0.85);
  // In VRM 1.0 normalized T-pose (+X left, +Y up, +Z forward):
  // leftUpperArm Z < 0 lowers arm down; rightUpperArm Z > 0 lowers arm down.
  const p = {
    spine: [breath * 0.018, sway * 0.02, 0],
    chest: [breath * 0.024, 0, 0],
    neck: [headPitchCue * 0.45, 0, 0],
    head: [headPitchCue * 0.55, sway * 0.03, sway * 0.02],
    leftUpperArm: [0.05, 0.04, -1.24],
    leftLowerArm: [0.06, -0.14, -0.12],
    rightUpperArm: [0.05, -0.04, 1.24],
    rightLowerArm: [0.06, 0.14, 0.12],
    leftHand: [0, 0, 0],
    rightHand: [0, 0, 0],
  };

  switch (pose) {
    case 'perched':
      p.spine = [0.08 + breath * 0.02, 0.04, 0];
      p.chest = [0.05 + breath * 0.02, 0, 0];
      p.head = [headPitchCue * 0.55 - 0.04, -0.06, 0.08];
      p.leftUpperArm = [0.32, 0.22, -0.96];
      p.leftLowerArm = [0.18, -0.82, -0.25];
      p.rightUpperArm = [0.32, -0.22, 0.96];
      p.rightLowerArm = [0.18, 0.82, 0.25];
      break;

    case 'typing': {
      const lTap = Math.sin(t * 11.5) * 0.08;
      const rTap = Math.sin(t * 11.5 + 1.8) * 0.08;
      p.spine = [0.1 + breath * 0.015, 0, 0];
      p.chest = [0.06, 0, 0];
      p.head = [0.12 + headPitchCue * 0.4, Math.sin(t * 1.4) * 0.05, 0];
      p.leftUpperArm = [0.55, 0.3, -0.82];
      p.leftLowerArm = [0.14 + lTap, -1.12, -0.18];
      p.rightUpperArm = [0.55, -0.3, 0.82];
      p.rightLowerArm = [0.14 + rTap, 1.12, 0.18];
      p.leftHand = [lTap * 1.4, 0, 0];
      p.rightHand = [rTap * 1.4, 0, 0];
      break;
    }

    case 'dangle':
      p.spine = [-0.04 + breath * 0.02, sway * 0.05, sway * 0.04];
      p.head = [headPitchCue * 0.5, -sway * 0.05, -sway * 0.06];
      p.leftUpperArm = [-0.06, 0.05, -1.3 + sway * 0.05];
      p.leftLowerArm = [0.04, -0.08, -0.06];
      p.rightUpperArm = [-0.06, -0.05, 1.3 + sway * 0.05];
      p.rightLowerArm = [0.04, 0.08, 0.06];
      break;

    case 'wave': {
      const w = Math.sin(t * 6.4);
      p.spine = [-0.02 + breath * 0.02, -0.06, -0.03];
      p.head = [headPitchCue * 0.4 - 0.03, 0.06, -0.08];
      p.rightUpperArm = [0.22, -0.18, -0.35 + w * 0.06];
      p.rightLowerArm = [0.12, 0.25, 1.45 + w * 0.32];
      p.rightHand = [0, 0, w * 0.22];
      p.leftUpperArm = [0.06, 0.05, -1.22];
      p.leftLowerArm = [0.08, -0.2, -0.14];
      break;
    }

    case 'point':
      p.spine = [0.04 + breath * 0.015, -0.1, 0];
      p.head = [headPitchCue * 0.4, 0.08, 0.03];
      p.rightUpperArm = [0.75, 0.35, 0.35];
      p.rightLowerArm = [0.08, 0.35, 0.1];
      p.rightHand = [0, 0, 0.08];
      p.leftUpperArm = [0.08, 0.06, -1.22];
      p.leftLowerArm = [0.1, -0.22, -0.15];
      break;

    case 'headrub': {
      const rub = Math.sin(t * 4.5);
      p.spine = [0.02 + breath * 0.02, 0.04, 0.04];
      p.head = [0.06 + headPitchCue * 0.4, -0.05, 0.12 + rub * 0.04];
      p.rightUpperArm = [0.35, -0.25, -0.55];
      p.rightLowerArm = [0.25, 0.45, 2.05 + rub * 0.14];
      p.rightHand = [0.12, 0, 0.18];
      p.leftUpperArm = [0.06, 0.05, -1.22];
      p.leftLowerArm = [0.08, -0.2, -0.12];
      break;
    }

    case 'cup_hands':
      p.spine = [0.05 + breath * 0.02, 0, 0];
      p.head = [0.04 + headPitchCue * 0.45, sway * 0.04, 0.04];
      p.leftUpperArm = [0.52, 0.38, -0.88];
      p.leftLowerArm = [0.24, -1.22, -0.32];
      p.rightUpperArm = [0.52, -0.38, 0.88];
      p.rightLowerArm = [0.24, 1.22, 0.32];
      p.leftHand = [0.1, -0.18, -0.12];
      p.rightHand = [0.1, 0.18, 0.12];
      break;

    case 'coffee': {
      const sip = Math.max(0, Math.sin(t * 1.15)) * 0.22;
      p.spine = [0.03 + breath * 0.018, 0.05, 0];
      p.head = [0.05 + sip * 0.2 + headPitchCue * 0.4, -0.04, 0.03];
      p.rightUpperArm = [0.48 + sip * 0.3, -0.28, 0.78];
      p.rightLowerArm = [0.22 + sip * 0.35, 1.38 + sip * 0.3, 0.4];
      p.rightHand = [0.12, 0.18, 0];
      p.leftUpperArm = [0.18, 0.2, -1.08];
      p.leftLowerArm = [0.14, -0.68, -0.2];
      break;
    }

    case 'stand':
    default:
      break;
  }

  return p;
}

class VrmPoseController {
  constructor() {
    this.vrm = null;
    this.pose = 'stand';
    this.elapsed = 0;
    this.boneRotations = new Map();
    this.mixer = null;
    this.activeAction = null;
    this.vrmaName = null;
    this.lookAtEnabled = true;
    this.pointerNdc = { x: 0, y: 0 };
    this.smoothedLook = { yaw: 0, pitch: 0 };
    this.lookTarget = new THREE.Object3D();
  }

  attach(vrm, scene) {
    this.stopVrma();
    this.vrm = vrm;
    this.boneRotations.clear();
    for (const name of POSE_BONES) {
      this.boneRotations.set(name, new THREE.Euler(0, 0, 0, 'XYZ'));
    }
    if (vrm) {
      this.mixer = new THREE.AnimationMixer(vrm.scene);
      if (vrm.lookAt) {
        if (!this.lookTarget.parent) scene.add(this.lookTarget);
        vrm.lookAt.target = this.lookTarget;
      }
    } else {
      this.mixer = null;
    }
  }

  setPose(pose) {
    this.pose = pose;
  }

  playVrma(vrmAnimation, filename) {
    if (!this.vrm || !this.mixer) return;
    this.stopVrma();
    const clip = createVRMAnimationClip(vrmAnimation, this.vrm);
    this.activeAction = this.mixer.clipAction(clip);
    this.activeAction.reset().play();
    this.vrmaName = filename || 'clip.vrma';
  }

  stopVrma() {
    if (this.activeAction) {
      this.activeAction.stop();
      this.activeAction = null;
    }
    if (this.mixer) {
      this.mixer.stopAllAction();
    }
    this.vrmaName = null;
  }

  setPointerNdc(nx, ny) {
    this.pointerNdc.x = Math.max(-1, Math.min(1, nx));
    this.pointerNdc.y = Math.max(-1, Math.min(1, ny));
  }

  update(dt, camera, gazeCue) {
    if (!this.vrm) return;
    this.elapsed += dt;

    if (this.activeAction && this.mixer) {
      this.mixer.update(dt);
    } else if (this.vrm.humanoid) {
      const targets = computePoseTargets(this.pose, this.elapsed, gazeCue.headPitch);
      // VRM 0.x normalized bones remain in VRM 0.x local space (-Z forward, -X left)
      // even after VRMUtils.rotateVRM0(vrm) rotates vrm.scene by pi around Y.
      // Conjugating VRM 1.0 Euler XYZ rotations by R_y(pi) negates X and Z while keeping Y.
      const isVrm0 = this.vrm.meta?.metaVersion === '0';
      const xzSign = isVrm0 ? -1 : 1;
      const alpha = Math.min(1, dt * 7.5);
      for (const boneName of POSE_BONES) {
        const node = this.vrm.humanoid.getNormalizedBoneNode(boneName);
        if (!node) continue;
        const tRot = targets[boneName];
        if (!tRot) continue;
        const cur = this.boneRotations.get(boneName);
        cur.x += (tRot[0] - cur.x) * alpha;
        cur.y += (tRot[1] - cur.y) * alpha;
        cur.z += (tRot[2] - cur.z) * alpha;
        node.rotation.set(cur.x * xzSign, cur.y, cur.z * xzSign);
      }
    }

    // Clamped LookAt target in front of head
    const headNode = this.vrm.humanoid?.getNormalizedBoneNode('head');
    if (headNode && this.vrm.lookAt) {
      const headPos = new THREE.Vector3();
      headNode.getWorldPosition(headPos);

      let targetYaw = 0;
      let targetPitch = 0;
      if (gazeCue.overrideGaze) {
        targetYaw = gazeCue.yaw;
        targetPitch = gazeCue.pitch;
      } else if (this.lookAtEnabled) {
        targetYaw = THREE.MathUtils.clamp(this.pointerNdc.x * 22 * DEG, -22 * DEG, 22 * DEG);
        targetPitch = THREE.MathUtils.clamp(this.pointerNdc.y * 15 * DEG, -15 * DEG, 15 * DEG);
      }

      const lookLerp = Math.min(1, dt * 8);
      this.smoothedLook.yaw += (targetYaw - this.smoothedLook.yaw) * lookLerp;
      this.smoothedLook.pitch += (targetPitch - this.smoothedLook.pitch) * lookLerp;

      const dist = 1.6;
      const forward = new THREE.Vector3(0, 0, 1);
      // Point toward camera with clamped yaw/pitch offset
      if (camera) {
        forward.subVectors(camera.position, headPos).normalize();
      }
      const offset = new THREE.Vector3(
        Math.sin(this.smoothedLook.yaw) * dist,
        Math.sin(this.smoothedLook.pitch) * dist,
        Math.cos(this.smoothedLook.yaw) * Math.cos(this.smoothedLook.pitch) * dist
      );
      this.lookTarget.position.copy(headPos).add(offset);
    }
  }
}

// ============================================================================
// Main Spike Application
// ============================================================================
const dom = {
  canvas: document.getElementById('vrm-canvas'),
  dock: document.getElementById('companion-dock'),
  dockOverlay: document.getElementById('dock-overlay'),
  exprBadge: document.getElementById('expr-badge'),
  rightPanel: document.getElementById('right-panel'),
  rightPanelBadge: document.getElementById('right-panel-badge'),
  modelBadge: document.getElementById('model-badge'),
  heroFpsChip: document.getElementById('hero-fps-chip'),
  heroVerChip: document.getElementById('hero-ver-chip'),
  exprActiveChip: document.getElementById('expr-active-chip'),
  poseActiveChip: document.getElementById('pose-active-chip'),
  avoidStatusChip: document.getElementById('avoid-status-chip'),
  perfModeChip: document.getElementById('perf-mode-chip'),
  sizeReadout: document.getElementById('size-readout'),
  dockHudFps: document.getElementById('dock-hud-fps'),
  dockHudState: document.getElementById('dock-hud-state'),
  dockHudStats: document.getElementById('dock-hud-stats'),
  telFps: document.getElementById('tel-fps'),
  telMs: document.getElementById('tel-ms'),
  telCalls: document.getElementById('tel-calls'),
  telTris: document.getElementById('tel-tris'),
  telPixels: document.getElementById('tel-pixels'),
  telMem: document.getElementById('tel-mem'),
  diagFallback: document.getElementById('diag-fallback'),
  diagWeights: document.getElementById('diag-weights'),
  diagPresetCount: document.getElementById('diag-preset-count'),
  diagPresets: document.getElementById('diag-presets'),
  diagCustomCount: document.getElementById('diag-custom-count'),
  diagCustoms: document.getElementById('diag-customs'),
  vrmaStatus: document.getElementById('vrma-status'),
  btnVrmaStop: document.getElementById('btn-vrma-stop'),
  btnToggleAvoid: document.getElementById('btn-toggle-avoid'),
  btnReframe: document.getElementById('btn-reframe'),
  btnResetSample: document.getElementById('btn-reset-sample'),
  btnSimIdle: document.getElementById('btn-sim-idle'),
  btnCtxLoss: document.getElementById('btn-ctx-loss'),
  fileInput: document.getElementById('file-input'),
  dockWidth: document.getElementById('dock-width'),
  fpsMode: document.getElementById('fps-mode'),
  dprMode: document.getElementById('dpr-mode'),
  lightMode: document.getElementById('light-mode'),
  chkLookAt: document.getElementById('chk-lookat'),
  dropzone: document.getElementById('dropzone'),
  globalDrop: document.getElementById('global-drop'),
};

const state = {
  vrm: null,
  modelLabel: 'Sample VRM',
  fpsMode: 'adaptive',
  dprCap: 2,
  lightMode: 'anime',
  detachMode: 'docked',
  avoidPinned: false,
  avoidHover: false,
  forceIdle: false,
  lastActivityAt: performance.now(),
  contextLost: false,
  // Telemetry
  measuredFps: 0,
  renderMsEma: 0,
  frameCountWindow: 0,
  fpsWindowStart: performance.now(),
  lastRenderAt: 0,
  lastHudUpdateAt: 0,
  drawCalls: 0,
  triangles: 0,
};

// Scene, Camera, Renderer
const scene = new THREE.Scene();
const camera = new THREE.PerspectiveCamera(26, 240 / 280, 0.08, 25);
camera.position.set(0, 1.38, 1.45);

const renderer = new THREE.WebGLRenderer({
  canvas: dom.canvas,
  alpha: true,
  antialias: true,
  powerPreference: 'default',
});
renderer.outputColorSpace = THREE.SRGBColorSpace;
renderer.toneMapping = THREE.NoToneMapping;
renderer.shadowMap.enabled = false;

// Lighting groups
const animeLights = new THREE.Group();
const animeKey = new THREE.DirectionalLight(0xfffaf5, 1.2);
animeKey.position.set(0.6, 1.4, 2.0);
const animeHemi = new THREE.HemisphereLight(0xe4ecff, 0x3b3e4c, 0.95);
animeLights.add(animeKey, animeHemi);
scene.add(animeLights);

const studioLights = new THREE.Group();
const stKey = new THREE.DirectionalLight(0xfff4e6, 1.45);
stKey.position.set(1.6, 1.8, 1.9);
const stFill = new THREE.DirectionalLight(0x9ec0ff, 0.65);
stFill.position.set(-1.8, 1.1, 1.3);
const stRim = new THREE.DirectionalLight(0xb8d4ff, 1.1);
stRim.position.set(0.1, 1.9, -1.7);
const stAmb = new THREE.AmbientLight(0xffffff, 0.35);
studioLights.add(stKey, stFill, stRim, stAmb);
studioLights.visible = false;
scene.add(studioLights);

function applyLightingPreset(mode) {
  state.lightMode = mode;
  animeLights.visible = mode === 'anime';
  studioLights.visible = mode === 'studio';
}

const exprCtrl = new VrmExpressionController();
const poseCtrl = new VrmPoseController();

// GLTF / VRM Loader
const loader = new GLTFLoader();
loader.register((parser) => new VRMLoaderPlugin(parser));
loader.register((parser) => new VRMAnimationLoaderPlugin(parser));

function showOverlay(msg) {
  if (msg) {
    dom.dockOverlay.textContent = msg;
    dom.dockOverlay.classList.add('is-visible');
  } else {
    dom.dockOverlay.classList.remove('is-visible');
  }
}

function resizeRenderer(explicitW, explicitH) {
  const rect = dom.dock.getBoundingClientRect();
  const w = Math.max(120, Math.round(explicitW || rect.width));
  const h = Math.max(140, Math.round(explicitH || rect.height));
  const dpr = Math.min(window.devicePixelRatio || 1, state.dprCap);
  renderer.setPixelRatio(dpr);
  renderer.setSize(w, h, false);
  camera.aspect = w / h;
  camera.updateProjectionMatrix();
  dom.sizeReadout.textContent = `${w}×${h}`;
}

// Auto-frame upper-body bust directly from head & chest/upperChest bones
function autoFrameBust(vrm) {
  if (!vrm) return;
  vrm.scene.updateMatrixWorld(true);

  const headNode = vrm.humanoid?.getNormalizedBoneNode('head');
  const upperChestNode =
    vrm.humanoid?.getNormalizedBoneNode('upperChest') ||
    vrm.humanoid?.getNormalizedBoneNode('chest') ||
    vrm.humanoid?.getNormalizedBoneNode('spine');

  const headPos = new THREE.Vector3(0, 1.45, 0);
  const chestPos = new THREE.Vector3(0, 1.18, 0);

  if (headNode) headNode.getWorldPosition(headPos);
  if (upperChestNode) upperChestNode.getWorldPosition(chestPos);

  const bustSpan = Math.max(0.18, headPos.y - chestPos.y);
  // Target slightly below chin / upper sternum with comfortable headroom below HUD pills
  const targetY = chestPos.y + bustSpan * 0.68;
  const framingHeight = bustSpan * 2.95;
  const vFovRad = camera.fov * DEG;
  const dist = (framingHeight * 0.5) / Math.tan(vFovRad * 0.5);

  camera.position.set(headPos.x, targetY + bustSpan * 0.06, headPos.z + dist);
  camera.lookAt(headPos.x, targetY, headPos.z);
  camera.updateProjectionMatrix();
}

function disposeCurrentVrm() {
  if (!state.vrm) return;
  scene.remove(state.vrm.scene);
  VRMUtils.deepDispose(state.vrm.scene);
  state.vrm = null;
}

async function loadVrmFromUrl(url, label) {
  showOverlay(`Loading ${label}...`);
  try {
    const gltf = await loader.loadAsync(url);
    const vrm = gltf.userData.vrm;
    if (!vrm) {
      throw new Error('File does not contain a valid VRM payload.');
    }

    VRMUtils.removeUnnecessaryVertices(gltf.scene);
    VRMUtils.combineSkeletons(gltf.scene);
    VRMUtils.rotateVRM0(vrm);

    disposeCurrentVrm();

    vrm.scene.traverse((obj) => {
      obj.frustumCulled = false;
    });

    scene.add(vrm.scene);
    state.vrm = vrm;
    state.modelLabel = label;

    exprCtrl.attach(vrm);
    poseCtrl.attach(vrm, scene);
    autoFrameBust(vrm);

    dom.modelBadge.textContent = `${label} · VRM ${exprCtrl.version}`;
    dom.heroVerChip.textContent = `VRM ${exprCtrl.version}`;
    updateBadgeUI();
    updateDiagnosticsUI();
    showOverlay('');
    markActivity();
  } catch (err) {
    console.error('Failed to load VRM:', err);
    showOverlay(`Load error: ${err?.message || err}`);
  }
}

async function loadVrmaFromUrl(url, label) {
  if (!state.vrm) return;
  try {
    const gltf = await loader.loadAsync(url);
    const animations = gltf.userData.vrmAnimations;
    if (!animations || !animations.length) {
      throw new Error('No VRMAnimation found in file.');
    }
    poseCtrl.playVrma(animations[0], label);
    dom.vrmaStatus.textContent = label;
    dom.btnVrmaStop.hidden = false;
    dom.poseActiveChip.textContent = `vrma:${label}`;
    markActivity();
  } catch (err) {
    console.error('Failed to load VRMA:', err);
    dom.vrmaStatus.textContent = `Error: ${err?.message || err}`;
  }
}

async function handleFile(file) {
  if (!file) return;
  const lower = file.name.toLowerCase();
  const blobUrl = URL.createObjectURL(file);
  try {
    if (lower.endsWith('.vrm')) {
      await loadVrmFromUrl(blobUrl, 'Local .vrm');
    } else if (lower.endsWith('.vrma')) {
      await loadVrmaFromUrl(blobUrl, 'Local .vrma');
    }
  } finally {
    URL.revokeObjectURL(blobUrl);
  }
}

// ============================================================================
// UI State & Avoidance Logic
// ============================================================================
function markActivity() {
  state.lastActivityAt = performance.now();
  if (state.forceIdle) {
    state.forceIdle = false;
    dom.btnSimIdle.classList.remove('is-active');
  }
}

function syncAvoidanceUI() {
  const avoiding = state.avoidPinned || state.avoidHover;
  dom.dock.classList.toggle('is-avoiding', avoiding);
  dom.rightPanel.classList.toggle('is-engaged', avoiding);
  dom.btnToggleAvoid.classList.toggle('is-active', state.avoidPinned);
  dom.avoidStatusChip.textContent = avoiding ? 'Shifted Left (Main)' : 'Docked Right';
  dom.avoidStatusChip.classList.toggle('primary', avoiding);
  dom.rightPanelBadge.textContent = avoiding ? 'Avoidance active' : 'Hover to slide';
  dom.rightPanelBadge.classList.toggle('primary', avoiding);
}

function setDetachMode(mode) {
  state.detachMode = mode;
  dom.dock.classList.remove('mode-detach-card', 'mode-detach-dissolve');
  if (mode === 'detach-card') dom.dock.classList.add('mode-detach-card');
  if (mode === 'detach-dissolve') dom.dock.classList.add('mode-detach-dissolve');
  for (const btn of document.querySelectorAll('#detach-buttons [data-detach]')) {
    btn.classList.toggle('is-active', btn.dataset.detach === mode);
  }
  markActivity();
}

function setExpression(expr) {
  exprCtrl.setState(expr);
  dom.exprActiveChip.textContent = expr;
  for (const btn of document.querySelectorAll('#expr-buttons [data-expr]')) {
    btn.classList.toggle('is-active', btn.dataset.expr === expr);
  }
  updateBadgeUI();
  updateDockStateHud();
  markActivity();
}

function setPose(pose) {
  poseCtrl.stopVrma();
  poseCtrl.setPose(pose);
  dom.vrmaStatus.textContent = 'None loaded';
  dom.btnVrmaStop.hidden = true;
  dom.poseActiveChip.textContent = pose;
  for (const btn of document.querySelectorAll('#pose-buttons [data-pose]')) {
    btn.classList.toggle('is-active', btn.dataset.pose === pose);
  }
  updateDockStateHud();
  markActivity();
}

function updateBadgeUI() {
  const info = exprCtrl.getBadgeInfo();
  if (info.kind) {
    dom.exprBadge.dataset.kind = info.kind;
    dom.exprBadge.textContent = info.label;
    dom.exprBadge.classList.add('is-visible');
  } else {
    dom.exprBadge.classList.remove('is-visible');
  }
}

function updateDockStateHud() {
  const poseLabel = poseCtrl.vrmaName ? 'vrma' : poseCtrl.pose;
  dom.dockHudState.textContent = `${exprCtrl.currentState} · ${poseLabel}`;
}

function updateDiagnosticsUI() {
  const diag = exprCtrl.getDiagnostics();
  dom.diagFallback.textContent = `oo -> ${diag.surprisedStrategy}`;
  dom.diagPresetCount.textContent = String(diag.presets.length);
  dom.diagCustomCount.textContent = String(diag.customs.length);

  dom.diagWeights.innerHTML = diag.activeWeights.length
    ? diag.activeWeights
        .map(([k, v]) => `<span class="ws-mini-tag lit">${k}: ${v.toFixed(2)}</span>`)
        .join('')
    : '<span class="ws-mini-tag">all 0.00</span>';

  if (dom.diagPresets.dataset.populatedFor !== `${diag.version}:${diag.presets.length}:${diag.customs.length}`) {
    dom.diagPresets.dataset.populatedFor = `${diag.version}:${diag.presets.length}:${diag.customs.length}`;
    dom.diagPresets.innerHTML = diag.presets.length
      ? diag.presets.map((p) => `<span class="ws-mini-tag">${p}</span>`).join('')
      : '<span class="ws-mini-tag">none</span>';
    dom.diagCustoms.innerHTML = diag.customs.length
      ? diag.customs.map((c) => `<span class="ws-mini-tag">${c}</span>`).join('')
      : '<span class="ws-mini-tag">none</span>';
  }
}

// ============================================================================
// Event Wiring
// ============================================================================
document.getElementById('expr-buttons').addEventListener('click', (e) => {
  const btn = e.target.closest('[data-expr]');
  if (btn) setExpression(btn.dataset.expr);
});

document.getElementById('pose-buttons').addEventListener('click', (e) => {
  const btn = e.target.closest('[data-pose]');
  if (btn) setPose(btn.dataset.pose);
});

document.getElementById('detach-buttons').addEventListener('click', (e) => {
  const btn = e.target.closest('[data-detach]');
  if (btn) setDetachMode(btn.dataset.detach);
});

dom.btnToggleAvoid.addEventListener('click', () => {
  state.avoidPinned = !state.avoidPinned;
  syncAvoidanceUI();
  markActivity();
});

dom.rightPanel.addEventListener('pointerenter', () => {
  state.avoidHover = true;
  syncAvoidanceUI();
  markActivity();
});

dom.rightPanel.addEventListener('pointerleave', () => {
  state.avoidHover = false;
  syncAvoidanceUI();
});

dom.rightPanel.addEventListener('pointerdown', () => {
  state.avoidHover = true;
  syncAvoidanceUI();
  markActivity();
});

dom.btnReframe.addEventListener('click', () => {
  autoFrameBust(state.vrm);
  markActivity();
});

dom.btnResetSample.addEventListener('click', () => {
  loadVrmFromUrl(SAMPLE_VRM_URL, 'Sample VRM');
});

dom.btnVrmaStop.addEventListener('click', () => {
  setPose(poseCtrl.pose);
});

dom.fileInput.addEventListener('change', (e) => {
  const file = e.target.files?.[0];
  if (file) handleFile(file);
  e.target.value = '';
});

dom.dockWidth.addEventListener('input', () => {
  const w = Number(dom.dockWidth.value);
  const h = Math.round(w * (280 / 240));
  document.documentElement.style.setProperty('--companion-w', `${w}px`);
  document.documentElement.style.setProperty('--companion-h', `${h}px`);
  resizeRenderer(w, h);
  markActivity();
});

dom.fpsMode.addEventListener('change', () => {
  state.fpsMode = dom.fpsMode.value;
  dom.perfModeChip.textContent =
    state.fpsMode === 'adaptive' ? 'Adaptive 30/6/0' : `${state.fpsMode} FPS cap`;
  markActivity();
});

dom.dprMode.addEventListener('change', () => {
  state.dprCap = Number(dom.dprMode.value);
  resizeRenderer();
  markActivity();
});

dom.lightMode.addEventListener('change', () => {
  applyLightingPreset(dom.lightMode.value);
  markActivity();
});

dom.chkLookAt.addEventListener('change', () => {
  poseCtrl.lookAtEnabled = dom.chkLookAt.checked;
  markActivity();
});

dom.btnSimIdle.addEventListener('click', () => {
  state.forceIdle = !state.forceIdle;
  dom.btnSimIdle.classList.toggle('is-active', state.forceIdle);
});

// WebGL Context Loss & Recovery Handler + Test Button
dom.canvas.addEventListener('webglcontextlost', (e) => {
  e.preventDefault();
  state.contextLost = true;
  showOverlay('WebGL context lost — waiting for restore...');
});

dom.canvas.addEventListener('webglcontextrestored', () => {
  state.contextLost = false;
  showOverlay('');
  resizeRenderer();
  markActivity();
});

dom.btnCtxLoss.addEventListener('click', () => {
  const gl = renderer.getContext();
  const ext = gl?.getExtension('WEBGL_lose_context');
  if (!ext) {
    showOverlay('WEBGL_lose_context extension unavailable');
    setTimeout(() => showOverlay(''), 1500);
    return;
  }
  ext.loseContext();
  setTimeout(() => {
    ext.restoreContext();
  }, 1500);
});

// Pointer tracking for LookAt + activity
window.addEventListener('pointermove', (e) => {
  const rect = dom.dock.getBoundingClientRect();
  const cx = rect.left + rect.width * 0.5;
  const cy = rect.top + rect.height * 0.35;
  const nx = (e.clientX - cx) / Math.max(window.innerWidth * 0.5, 240);
  const ny = -(e.clientY - cy) / Math.max(window.innerHeight * 0.5, 240);
  poseCtrl.setPointerNdc(nx, ny);
  markActivity();
});

window.addEventListener('keydown', markActivity);
window.addEventListener('resize', resizeRenderer);

// Drag & Drop (.vrm / .vrma)
let dragDepth = 0;
window.addEventListener('dragenter', (e) => {
  e.preventDefault();
  dragDepth++;
  dom.globalDrop.classList.add('is-visible');
  dom.dropzone.classList.add('is-dragover');
});

window.addEventListener('dragover', (e) => {
  e.preventDefault();
});

window.addEventListener('dragleave', (e) => {
  e.preventDefault();
  dragDepth = Math.max(0, dragDepth - 1);
  if (dragDepth === 0) {
    dom.globalDrop.classList.remove('is-visible');
    dom.dropzone.classList.remove('is-dragover');
  }
});

window.addEventListener('drop', (e) => {
  e.preventDefault();
  dragDepth = 0;
  dom.globalDrop.classList.remove('is-visible');
  dom.dropzone.classList.remove('is-dragover');
  const file = e.dataTransfer?.files?.[0];
  if (file) handleFile(file);
});

// ============================================================================
// Adaptive Render Loop & Telemetry
// ============================================================================
function getTargetFps(now) {
  if (document.hidden || state.contextLost) return 0;
  if (state.fpsMode === 'adaptive') {
    const isIdle = state.forceIdle || now - state.lastActivityAt > IDLE_TIMEOUT_MS;
    return isIdle ? 6 : 30;
  }
  return Number(state.fpsMode) || 30;
}

function updateTelemetryUI(now, targetFps) {
  const fpsStr = `${Math.round(state.measuredFps)}`;
  const msStr = `${state.renderMsEma.toFixed(2)} ms`;
  const dpr = renderer.getPixelRatio();
  const pxW = dom.canvas.width;
  const pxH = dom.canvas.height;

  dom.heroFpsChip.textContent = `${fpsStr} / ${targetFps} FPS`;
  dom.dockHudFps.textContent = `${fpsStr} fps · ${state.renderMsEma.toFixed(1)}ms`;
  dom.dockHudStats.textContent = `${state.drawCalls} calls · ${state.triangles.toLocaleString()} tris`;

  dom.telFps.textContent = `${fpsStr} (cap ${targetFps})`;
  dom.telMs.textContent = msStr;
  dom.telCalls.textContent = String(state.drawCalls);
  dom.telTris.textContent = state.triangles.toLocaleString();
  dom.telPixels.textContent = `${pxW}×${pxH} @${dpr.toFixed(1)}x`;

  if (performance.memory) {
    const usedMb = (performance.memory.usedJSHeapSize / (1024 * 1024)).toFixed(1);
    dom.telMem.textContent = `${usedMb} MB`;
  } else {
    dom.telMem.textContent = 'n/a';
  }

  updateDiagnosticsUI();
  state.lastHudUpdateAt = now;
}

let prevTickAt = performance.now();
function frameLoop(now) {
  requestAnimationFrame(frameLoop);

  const targetFps = getTargetFps(now);
  if (targetFps === 0) {
    prevTickAt = now;
    return;
  }

  const minInterval = 1000 / targetFps;
  if (state.lastRenderAt > 0 && now - state.lastRenderAt < minInterval * 0.92) {
    return;
  }

  const dt = Math.min(0.1, Math.max(0.001, (now - prevTickAt) / 1000));
  prevTickAt = now;
  state.lastRenderAt = now;

  const t0 = performance.now();
  if (state.vrm) {
    exprCtrl.update(dt);
    const gazeCue = exprCtrl.getGazeAndHeadCue(exprCtrl.elapsed);
    poseCtrl.update(dt, camera, gazeCue);
    state.vrm.update(dt);
  }
  renderer.render(scene, camera);
  const renderMs = performance.now() - t0;

  state.renderMsEma = state.renderMsEma === 0 ? renderMs : state.renderMsEma * 0.85 + renderMs * 0.15;
  state.drawCalls = renderer.info.render.calls;
  state.triangles = renderer.info.render.triangles;

  state.frameCountWindow++;
  const winElapsed = now - state.fpsWindowStart;
  if (winElapsed >= 500) {
    state.measuredFps = (state.frameCountWindow * 1000) / winElapsed;
    state.frameCountWindow = 0;
    state.fpsWindowStart = now;
  }

  if (now - state.lastHudUpdateAt >= 400) {
    updateTelemetryUI(now, targetFps);
  }
}

// Expose test/inspection hook for automated verification
window.__vrmSpike = {
  isLoaded: () => Boolean(state.vrm),
  setExpression,
  setPose,
  setDetachMode,
  setAvoidPinned: (v) => {
    state.avoidPinned = Boolean(v);
    syncAvoidanceUI();
  },
  getTelemetry: () => ({
    loaded: Boolean(state.vrm),
    modelLabel: state.modelLabel,
    version: exprCtrl.version,
    expression: exprCtrl.currentState,
    pose: poseCtrl.pose,
    avoiding: state.avoidPinned || state.avoidHover,
    detachMode: state.detachMode,
    measuredFps: Number(state.measuredFps.toFixed(1)),
    renderMs: Number(state.renderMsEma.toFixed(2)),
    drawCalls: state.drawCalls,
    triangles: state.triangles,
    canvasWidth: dom.canvas.width,
    canvasHeight: dom.canvas.height,
    diagnostics: exprCtrl.getDiagnostics(),
  }),
};

// Boot
resizeRenderer();
applyLightingPreset('anime');
updateDockStateHud();
loadVrmFromUrl(SAMPLE_VRM_URL, 'Sample VRM');
requestAnimationFrame(frameLoop);
