(function (C) {
  "use strict";

  // Standalone 3D Control-Rig Studio viewport stage.
  // Reuses C.vrmStudio (bust framing + Dark/Light studio lighting + prop PMREM),
  // C.vrmCoLeadProps (smartphone & laptop props + calibration), C.vrmIk (2-bone
  // IK + Target Attach), and C.vrmClip (clip sampler + pose application).
  // Runs on demand and pauses rAF after 2 s of idle when not playing.

  const DEG = Math.PI / 180;
  const BASE_FOV = 26;
  const IDLE_PAUSE_MS = 2000;
  const SLOT_ASPECT = 240 / 280;

  const BASE_BONES = [
    "hips", "spine", "chest", "upperChest", "neck", "head",
    "leftShoulder", "leftUpperArm", "leftLowerArm", "leftHand",
    "rightShoulder", "rightUpperArm", "rightLowerArm", "rightHand"
  ];
  const FINGER_BONES = C.vrmCoLeadPose?.FINGER_BONES ? Array.from(C.vrmCoLeadPose.FINGER_BONES) : [];
  const POSE_BONES = BASE_BONES.concat(FINGER_BONES);

  const MODE_COLORS = {
    ikLeft: 0x5fd0e0,
    ikRight: 0xff8a80,
    pole: 0xffd479,
    look: 0xb7a5f5,
    prop: 0x8fe0a8
  };

  function clamp(v, lo, hi) { return v < lo ? lo : (v > hi ? hi : v); }
  function conjugateEuler(e, isVrm0) { return isVrm0 ? [-e[0], e[1], -e[2]] : [e[0], e[1], e[2]]; }

  function createStage(opts) {
    const canvas = opts.canvas;
    const overlaySvg = opts.overlaySvg;
    const viewportEl = opts.viewportEl;
    let runtime = null;
    let renderer = null;
    let scene = null;
    let camera = null;
    let orbit = null;
    let transform = null;
    let studioLighting = null;
    let props = null;
    let vrm = null;
    let isVrm0 = false;
    let mixer = null;
    let activeVrmaAction = null;
    let activeVrmaClip = null;
    let lookTarget = null;

    let framing = {
      headY: 1.45, chestY: 1.18, waistY: 0.90, topY: 1.72, centerX: 0, headZ: 0,
      bustSpan: 0.27, targetY: 1.36, frameHeight: 0.9, headroomRatio: 0.12, cameraZ: 1.9
    };
    let armChains = { left: null, right: null };
    let calibration = { props: {} };
    let activeMode = "ik"; // "ik" | "pole" | "look" | "prop"
    let activeSide = "right"; // "left" | "right"
    let activePropId = "none"; // "none" | "smartphone" | "laptop"
    let slotPx = 280;
    let theme = "dark";
    let currentSample = null;
    let currentKeyframe = null;
    let isPlaying = false;
    let playingKind = "clip"; // "clip" | "vrma"
    let lastFrameAt = 0;
    let activeUntil = 0;
    let rafId = 0;
    let renderCount = 0;
    let disposed = false;
    let gizmoDragging = false;
    let resizeObserver = null;
    let vrmLoadToken = 0;

    const gizmos = {
      group: null,
      ikLeft: null,
      ikRight: null,
      poleLeft: null,
      poleRight: null,
      look: null,
      prop: null,
      raycaster: null,
      pointer: null,
      pickables: []
    };
    const exprAvailable = new Set();
    const exprMap = Object.create(null);
    const disposables = [];
    const scratchVec = { a: null, b: null, head: null, elbowL: null, elbowR: null };
    let lastTethers = [];

    function markActive(ms) {
      if (disposed) return;
      const until = performance.now() + (Number.isFinite(ms) ? ms : IDLE_PAUSE_MS);
      if (until > activeUntil) activeUntil = until;
      scheduleLoop();
    }

    function scheduleLoop() {
      if (disposed || rafId || (typeof document !== "undefined" && document.hidden)) return;
      lastFrameAt = performance.now();
      rafId = requestAnimationFrame(loop);
    }

    function stopLoop() {
      if (rafId) {
        cancelAnimationFrame(rafId);
        rafId = 0;
      }
    }

    function buildGizmoMesh(THREE, radius, color, shape) {
      const geo = shape === "octa"
        ? new THREE.OctahedronGeometry(radius, 0)
        : (shape === "box" ? new THREE.BoxGeometry(radius * 1.7, radius * 1.7, radius * 1.7) : new THREE.SphereGeometry(radius, 16, 12));
      const mat = new THREE.MeshBasicMaterial({
        color,
        transparent: true,
        opacity: 0.92,
        depthTest: false
      });
      disposables.push(geo, mat);
      const mesh = new THREE.Mesh(geo, mat);
      mesh.renderOrder = 20;
      mesh.frustumCulled = false;
      return mesh;
    }

    async function init() {
      if (runtime) return runtime;
      if (typeof C.vrmRuntimeLoader !== "function") {
        throw new Error("Studio VRM runtime loader is not configured");
      }
      runtime = await C.vrmRuntimeLoader();
      if (disposed) return null;
      const { THREE, OrbitControls, TransformControls } = runtime;
      scene = new THREE.Scene();
      const rect = viewportEl.getBoundingClientRect();
      const w = Math.max(120, Math.round(rect.width || 640));
      const h = Math.max(120, Math.round(rect.height || 480));
      camera = new THREE.PerspectiveCamera(BASE_FOV, w / h, 0.05, 30);
      camera.position.set(0, 1.38, 1.9);

      renderer = new THREE.WebGLRenderer({ canvas, alpha: true, antialias: true, powerPreference: "default" });
      renderer.outputColorSpace = THREE.SRGBColorSpace;
      renderer.toneMapping = THREE.NoToneMapping;
      renderer.shadowMap.enabled = false;
      renderer.setClearColor(0x000000, 0);
      renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
      renderer.setSize(w, h, false);

      studioLighting = C.vrmStudio?.createStudioLighting?.({ THREE, scene, renderer }) || null;
      studioLighting?.applyTheme(theme);

      props = C.vrmCoLeadProps?.createCoLeadProps?.({ THREE, scene, camera }) || null;
      lookTarget = new THREE.Object3D();
      scene.add(lookTarget);

      scratchVec.a = new THREE.Vector3();
      scratchVec.b = new THREE.Vector3();
      scratchVec.head = new THREE.Vector3();
      scratchVec.elbowL = new THREE.Vector3();
      scratchVec.elbowR = new THREE.Vector3();

      orbit = new OrbitControls(camera, canvas);
      orbit.enableDamping = true;
      orbit.dampingFactor = 0.09;
      orbit.minDistance = 0.45;
      orbit.maxDistance = 5.5;
      orbit.target.set(0, 1.36, 0);
      orbit.addEventListener("change", () => markActive());

      transform = new TransformControls(camera, canvas);
      transform.setMode("translate");
      transform.setSize(0.68);
      transform.addEventListener("dragging-changed", ev => {
        gizmoDragging = !!ev.value;
        if (orbit) orbit.enabled = !gizmoDragging;
        markActive();
      });
      transform.addEventListener("objectChange", () => {
        if (!gizmoDragging) return;
        handleTransformDrag();
        markActive();
      });
      // Three.js r169+ exposes TransformControls.getHelper() for scene attachment.
      const tcRoot = typeof transform.getHelper === "function" ? transform.getHelper() : transform;
      scene.add(tcRoot);

      gizmos.group = new THREE.Group();
      gizmos.group.name = "ws-rig-studio-gizmos";
      gizmos.ikLeft = buildGizmoMesh(THREE, 0.026, MODE_COLORS.ikLeft, "sphere");
      gizmos.ikLeft.userData = { gizmo: "ik", side: "left" };
      gizmos.ikRight = buildGizmoMesh(THREE, 0.026, MODE_COLORS.ikRight, "sphere");
      gizmos.ikRight.userData = { gizmo: "ik", side: "right" };
      gizmos.poleLeft = buildGizmoMesh(THREE, 0.024, MODE_COLORS.pole, "octa");
      gizmos.poleLeft.userData = { gizmo: "pole", side: "left" };
      gizmos.poleRight = buildGizmoMesh(THREE, 0.024, MODE_COLORS.pole, "octa");
      gizmos.poleRight.userData = { gizmo: "pole", side: "right" };
      gizmos.look = buildGizmoMesh(THREE, 0.024, MODE_COLORS.look, "sphere");
      gizmos.look.userData = { gizmo: "look" };
      gizmos.prop = buildGizmoMesh(THREE, 0.026, MODE_COLORS.prop, "box");
      gizmos.prop.userData = { gizmo: "prop" };
      gizmos.group.add(gizmos.ikLeft, gizmos.ikRight, gizmos.poleLeft, gizmos.poleRight, gizmos.look, gizmos.prop);
      gizmos.pickables = [gizmos.ikLeft, gizmos.ikRight, gizmos.poleLeft, gizmos.poleRight, gizmos.look, gizmos.prop];
      gizmos.raycaster = new THREE.Raycaster();
      gizmos.pointer = new THREE.Vector2();
      scene.add(gizmos.group);

      canvas.addEventListener("pointerdown", onCanvasPointerDown);
      if (typeof ResizeObserver === "function") {
        resizeObserver = new ResizeObserver(() => resize());
        resizeObserver.observe(viewportEl);
      }
      document.addEventListener("visibilitychange", onVisibilityChange);
      syncGizmoAttachment();
      markActive();
      return runtime;
    }

    function onVisibilityChange() {
      if (document.hidden) stopLoop();
      else markActive();
    }

    function onCanvasPointerDown(ev) {
      if (!gizmos.raycaster || !camera || gizmoDragging || ev.button !== 0) return;
      const rect = canvas.getBoundingClientRect();
      if (!rect.width || !rect.height) return;
      gizmos.pointer.set(((ev.clientX - rect.left) / rect.width) * 2 - 1, -((ev.clientY - rect.top) / rect.height) * 2 + 1);
      gizmos.raycaster.setFromCamera(gizmos.pointer, camera);
      const visible = gizmos.pickables.filter(m => m.visible);
      const hits = gizmos.raycaster.intersectObjects(visible, false);
      if (!hits.length) return;
      const meta = hits[0].object.userData;
      if (meta.gizmo === "ik" || meta.gizmo === "pole") {
        activeMode = meta.gizmo;
        activeSide = meta.side;
        syncGizmoAttachment();
        opts.onGizmoSelect?.({ mode: activeMode, side: activeSide });
        markActive();
      } else if (meta.gizmo === "look" || meta.gizmo === "prop") {
        activeMode = meta.gizmo;
        syncGizmoAttachment();
        opts.onGizmoSelect?.({ mode: activeMode, side: activeSide });
        markActive();
      }
    }

    function resize() {
      if (!renderer || !camera || !viewportEl) return;
      const rect = viewportEl.getBoundingClientRect();
      const w = Math.max(120, Math.round(rect.width || 640));
      const h = Math.max(120, Math.round(rect.height || 480));
      camera.aspect = w / h;
      camera.updateProjectionMatrix();
      renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
      renderer.setSize(w, h, false);
      markActive();
    }

    function disposeVrmOnly() {
      if (activeVrmaAction) {
        try { activeVrmaAction.stop(); } catch (_) {}
        activeVrmaAction = null;
      }
      activeVrmaClip = null;
      if (mixer && vrm) {
        try { mixer.stopAllAction(); mixer.uncacheRoot(vrm.scene); } catch (_) {}
      }
      mixer = null;
      if (vrm && scene) {
        try {
          scene.remove(vrm.scene);
          runtime?.VRMUtils?.deepDispose?.(vrm.scene);
        } catch (_) {}
      }
      vrm = null;
      exprAvailable.clear();
    }

    function collectExpressions(loaded, manifestExprMap) {
      exprAvailable.clear();
      for (const k of Object.keys(exprMap)) delete exprMap[k];
      if (manifestExprMap && typeof manifestExprMap === "object") Object.assign(exprMap, manifestExprMap);
      const mgr = loaded?.expressionManager;
      if (!mgr) return;
      for (const ex of mgr.expressions || []) if (ex?.expressionName) exprAvailable.add(ex.expressionName);
      if (mgr.presetExpressionMap) for (const k of Object.keys(mgr.presetExpressionMap)) exprAvailable.add(k);
      if (mgr.customExpressionMap) for (const k of Object.keys(mgr.customExpressionMap)) exprAvailable.add(k);
    }

    function computePosedBounds(THREE, loaded, waistY) {
      const fullBox = new THREE.Box3();
      const upperBox = new THREE.Box3();
      const meshBox = new THREE.Box3();
      let found = false;
      loaded.scene.traverse(obj => {
        if (!obj || !obj.isMesh || obj.visible === false || !obj.geometry) return;
        if (obj.isSkinnedMesh && typeof obj.computeBoundingBox === "function") {
          obj.computeBoundingBox();
          if (!obj.boundingBox || obj.boundingBox.isEmpty()) return;
          meshBox.copy(obj.boundingBox).applyMatrix4(obj.matrixWorld);
        } else {
          if (!obj.geometry.boundingBox) obj.geometry.computeBoundingBox();
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

    function computeBustFraming(loaded) {
      const { THREE } = runtime;
      loaded.scene.updateMatrixWorld(true);
      const h = loaded.humanoid;
      const headNode = h?.getNormalizedBoneNode("head");
      const chestNode = h?.getNormalizedBoneNode("upperChest") || h?.getNormalizedBoneNode("chest") || h?.getNormalizedBoneNode("spine");
      const hipsNode = h?.getNormalizedBoneNode("hips");
      const lArm = h?.getNormalizedBoneNode("leftUpperArm");
      const rArm = h?.getNormalizedBoneNode("rightUpperArm");
      const headPos = new THREE.Vector3(0, 1.45, 0);
      const chestPos = new THREE.Vector3(0, 1.18, 0);
      if (headNode) headNode.getWorldPosition(headPos);
      if (chestNode) chestNode.getWorldPosition(chestPos);
      const bustSpan = Math.max(0.18, headPos.y - chestPos.y);
      let waistY = chestPos.y - bustSpan * 1.05;
      if (hipsNode) {
        const hp = new THREE.Vector3();
        hipsNode.getWorldPosition(hp);
        if (Number.isFinite(hp.y) && hp.y < chestPos.y) waistY = hp.y + (chestPos.y - hp.y) * 0.32;
      }
      let shoulderSpan = bustSpan * 1.25;
      if (lArm && rArm) {
        const lp = new THREE.Vector3(), rp = new THREE.Vector3();
        lArm.getWorldPosition(lp); rArm.getWorldPosition(rp);
        const span = Math.abs(lp.x - rp.x);
        if (Number.isFinite(span) && span > 0.12) shoulderSpan = span;
      }
      const bounds = computePosedBounds(THREE, loaded, waistY);
      let topY = headPos.y + Math.max(0.18, bustSpan * 0.72);
      let frontOffset = 0.10;
      let upperWidth = shoulderSpan * 1.85;
      if (bounds && !bounds.fullBox.isEmpty()) {
        topY = Math.max(topY, Math.min(bounds.fullBox.max.y, headPos.y + Math.max(0.75, bustSpan * 3.0)));
        frontOffset = clamp(bounds.fullBox.max.z - headPos.z, 0.08, 0.42);
        if (!bounds.upperBox.isEmpty()) {
          const halfW = Math.max(Math.abs(bounds.upperBox.max.x - headPos.x), Math.abs(bounds.upperBox.min.x - headPos.x));
          upperWidth = Math.max(upperWidth, Math.min(halfW * 2.12, shoulderSpan * 3.2));
        }
      }
      const fr = C.vrmStudio.solveBustFraming({ topY, waistY, bustSpan, upperWidth, frontOffset, aspect: SLOT_ASPECT, fov: BASE_FOV });
      framing = {
        headY: headPos.y, chestY: chestPos.y, waistY, topY, centerX: headPos.x, headZ: headPos.z,
        bustSpan, targetY: fr.targetY, frameHeight: fr.frameHeight, headroomRatio: fr.headroomRatio,
        cameraZ: headPos.z + fr.dist, upperWidth, limitedBy: fr.limitedBy
      };
      resetCamera();
    }

    function computeSlotGuideRect(w, h, px) {
      const displayScale = Math.min(1.26, Math.max(0.68, (h - 152) / (360 / SLOT_ASPECT)));
      const sw = Math.round(px * displayScale);
      const sh = Math.round((px / SLOT_ASPECT) * displayScale);
      const sx = Math.round((w - sw) * 0.5);
      const sy = Math.round((h - sh) * 0.5) + 14;
      return { sx, sy, sw, sh };
    }

    function resetCamera() {
      if (!camera || !orbit || !viewportEl) return;
      const rect = viewportEl.getBoundingClientRect();
      const w = Math.max(120, Math.round(rect.width || 640));
      const h = Math.max(120, Math.round(rect.height || 480));
      const refGuide = computeSlotGuideRect(w, h, 280);
      const baseDist = Math.max(0.5, framing.cameraZ - framing.headZ);
      // Match camera distance to the 280px slot guide height so the avatar's bust
      // aligns with the 12% headroom line and hand IK gizmos stay comfortably visible.
      const zoomOut = clamp(h / Math.max(180, refGuide.sh), 1.15, 1.85);
      camera.fov = BASE_FOV;
      camera.position.set(framing.centerX, framing.targetY - framing.bustSpan * 0.12, framing.headZ + baseDist * zoomOut);
      orbit.target.set(framing.centerX, framing.targetY - framing.bustSpan * 0.12, framing.headZ);
      orbit.update();
      camera.updateProjectionMatrix();
      layoutProps();
      markActive();
    }

    function layoutProps() {
      if (!props || !camera) return;
      // Solve prop layout using a canonical bust-aspect clone of camera so the
      // laptop/phone sit where the dashboard bust slot places them.
      const savedAspect = camera.aspect;
      camera.aspect = SLOT_ASPECT;
      camera.updateProjectionMatrix();
      props.layoutProp(framing, camera, { accentHex: "#7aa2ff" });
      props.setCalibration(calibration);
      studioLighting?.attachPropEnvironment(props.getPropObjects());
      camera.aspect = savedAspect;
      camera.updateProjectionMatrix();
      updatePropVisibility();
    }

    function updatePropVisibility() {
      if (!props) return;
      const objs = props.getPropObjects();
      for (const o of objs) {
        if (o.name === "ws-colead-laptop") o.visible = activePropId === "laptop";
        else if (o.name === "ws-colead-phone") o.visible = activePropId === "smartphone";
      }
      if (activePropId === "smartphone") {
        // Bring smartphone into a clear held/inspectable foreground position.
        const m = props.getMetrics();
        const heldX = framing.centerX - 0.14;
        const heldY = framing.chestY + framing.bustSpan * 0.18;
        const heldZ = framing.headZ + 0.34;
        props.updateVisuals({
          dt: 1,
          accentHex: "#7aa2ff",
          targetOffset: { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 },
          immediateOffset: true,
          phoneTarget: { x: heldX, y: heldY, z: heldZ, rx: -18 * DEG, ry: 22 * DEG, rz: 4 * DEG },
          targetPhoneGlow: 0.28,
          targetBuzzRingAlpha: 0,
          targetGlow: 0.08,
          emblemTarget: 0.7
        });
        void m;
      } else if (activePropId === "laptop") {
        props.updateVisuals({
          dt: 1,
          accentHex: "#7aa2ff",
          targetOffset: { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 },
          immediateOffset: true,
          phoneTarget: { x: 0.25, y: 0, z: 0, rx: -48 * DEG, ry: -12 * DEG, rz: -6 * DEG },
          targetPhoneGlow: 0.1,
          targetBuzzRingAlpha: 0,
          targetGlow: 0.2,
          emblemTarget: 0.92
        });
      }
      syncGizmoPositions();
      markActive();
    }

    async function loadVrm(modelUrl, manifestExprMap, packCalibration) {
      const token = ++vrmLoadToken;
      await init();
      if (disposed || token !== vrmLoadToken) return null;
      const resp = await fetch(modelUrl, { credentials: "same-origin" });
      if (!resp.ok) throw new Error("Failed to fetch VRM asset (" + resp.status + ")");
      const buf = await resp.arrayBuffer();
      if (disposed || token !== vrmLoadToken) return null;

      const { GLTFLoader, VRMLoaderPlugin, VRMAnimationLoaderPlugin, VRMUtils, THREE } = runtime;
      const loader = new GLTFLoader();
      loader.register(p => new VRMLoaderPlugin(p));
      loader.register(p => new VRMAnimationLoaderPlugin(p));
      const gltf = await loader.parseAsync(buf.slice(0), "");
      if (disposed || token !== vrmLoadToken) return null;
      const loaded = gltf.userData?.vrm;
      if (!loaded) throw new Error("Invalid VRM payload");

      VRMUtils.removeUnnecessaryVertices(gltf.scene);
      VRMUtils.combineSkeletons(gltf.scene);
      VRMUtils.rotateVRM0(loaded);

      disposeVrmOnly();
      loaded.scene.traverse(o => { o.frustumCulled = false; });
      scene.add(loaded.scene);
      vrm = loaded;
      isVrm0 = String(loaded.meta?.metaVersion ?? "") === "0";
      if (loaded.lookAt && lookTarget) loaded.lookAt.target = lookTarget;
      mixer = new THREE.AnimationMixer(loaded.scene);
      collectExpressions(loaded, manifestExprMap);
      calibration = packCalibration && typeof packCalibration === "object" ? packCalibration : { props: {} };

      // Apply relaxed standing pose first to measure arm chains & framing cleanly.
      applyNeutralRestPose();
      if (C.vrmIk) {
        armChains = {
          left: C.vrmIk.measureArmChain(loaded, "left", THREE),
          right: C.vrmIk.measureArmChain(loaded, "right", THREE)
        };
      }
      computeBustFraming(loaded);
      if (currentSample) applyClipSample(currentSample, currentKeyframe);
      markActive();
      return {
        isVrm0,
        metaVersion: String(loaded.meta?.metaVersion ?? "1"),
        chains: armChains,
        expressions: Array.from(exprAvailable).sort()
      };
    }

    async function loadVrmaBuffer(arrayBuffer, loop) {
      if (!vrm || !runtime || !mixer) return null;
      const { GLTFLoader, VRMAnimationLoaderPlugin, createVRMAnimationClip, THREE } = runtime;
      const loader = new GLTFLoader();
      loader.register(p => new VRMAnimationLoaderPlugin(p));
      const gltf = await loader.parseAsync(arrayBuffer.slice(0), "");
      const anims = gltf.userData?.vrmAnimations;
      if (!anims || !anims.length) throw new Error("No VRMC_vrm_animation tracks in file");
      const clip = createVRMAnimationClip(anims[0], vrm);
      if (activeVrmaAction) {
        try { activeVrmaAction.stop(); } catch (_) {}
      }
      activeVrmaClip = clip;
      activeVrmaAction = mixer.clipAction(clip);
      activeVrmaAction.reset();
      activeVrmaAction.setLoop(loop === false ? THREE.LoopOnce : THREE.LoopRepeat, loop === false ? 1 : Infinity);
      activeVrmaAction.clampWhenFinished = loop === false;
      activeVrmaAction.play();
      playingKind = "vrma";
      markActive();
      return { duration_s: Number((clip.duration || 1).toFixed(2)) };
    }

    function stopVrma() {
      if (activeVrmaAction) {
        try { activeVrmaAction.stop(); } catch (_) {}
        activeVrmaAction = null;
      }
      activeVrmaClip = null;
      playingKind = "clip";
      markActive();
    }

    function applyNeutralRestPose() {
      if (!vrm?.humanoid) return;
      const base = C.vrmCoLeadPose?.computeCoLeadTargets
        ? C.vrmCoLeadPose.computeCoLeadTargets({
          elapsed: 0, typingWeight: 0, glanceWeight: 0, sleepyWeight: 0,
          reactionWeight: 0, workingWeight: 0, tiltDir: 1, headYaw: 0, headPitch: 0, headPitchCue: 0
        })
        : {};
      for (const bone of POSE_BONES) {
        const node = vrm.humanoid.getNormalizedBoneNode(bone);
        if (!node) continue;
        const t = base[bone] || [0, 0, 0];
        const [x, y, z] = conjugateEuler(t, isVrm0);
        node.rotation.set(x, y, z, "XYZ");
      }
      vrm.update(0);
      vrm.scene.updateMatrixWorld(true);
    }

    function resolveExprAlias(key) {
      const map = {
        happy: ["happy", "joy"],
        relaxed: ["relaxed", "fun"],
        sad: ["sad", "sorrow"],
        angry: ["angry"],
        surprised: ["surprised", "Surprised", "Fcl_ALL_Surprised", "oh", "o"],
        oo: ["surprised", "Surprised", "Fcl_ALL_Surprised", "oh", "o"],
        blink: ["blink"],
        half_lid: ["blink"],
        wink: ["blinkLeft", "Blink_L", "blinkRight", "Blink_R"],
        neutral: ["neutral"]
      };
      if (exprAvailable.has(key)) return key;
      const list = map[key] || [];
      for (const c of list) if (exprAvailable.has(c)) return c;
      return null;
    }

    function applyExpressions(exprObj) {
      const mgr = vrm?.expressionManager;
      if (!mgr) return;
      for (const name of exprAvailable) mgr.setValue(name, 0);
      if (!exprObj) {
        const kN = resolveExprAlias("neutral");
        if (kN) mgr.setValue(kN, 1);
        return;
      }
      for (const [k, rawVal] of Object.entries(exprObj)) {
        const val = clamp(Number(rawVal) || 0, 0, 1);
        if (val <= 0.001) continue;
        if (k === "half_lid") {
          const kb = resolveExprAlias("blink");
          if (kb) mgr.setValue(kb, Math.max(mgr.getValue?.(kb) || 0, val * 0.55));
          continue;
        }
        const resolved = resolveExprAlias(k);
        if (resolved) mgr.setValue(resolved, val);
      }
    }

    function buildRigContext() {
      const headNode = vrm?.humanoid?.getNormalizedBoneNode("head");
      let headPos = [framing.centerX, framing.headY, framing.headZ];
      if (headNode && scratchVec.head) {
        headNode.getWorldPosition(scratchVec.head);
        headPos = [scratchVec.head.x, scratchVec.head.y, scratchVec.head.z];
      }
      return {
        vrm,
        THREE: runtime?.THREE,
        camera,
        chains: armChains,
        headPos,
        getPropAnchorWorld: (propId, anchor) => {
          if (!props || !runtime) return null;
          if (propId !== "smartphone" && propId !== "laptop") return null;
          return props.getPropAnchorWorld(runtime.THREE, propId, anchor);
        }
      };
    }

    function applyClipSample(sample, keyframe) {
      currentSample = sample || null;
      currentKeyframe = keyframe || null;
      if (!vrm || !runtime || !sample || playingKind === "vrma") return;
      const targets = Object.create(null);
      // Seed relaxed base so untouched bones stay natural.
      const base = C.vrmCoLeadPose?.computeCoLeadTargets
        ? C.vrmCoLeadPose.computeCoLeadTargets({
          elapsed: 0, typingWeight: 0, glanceWeight: 0, sleepyWeight: 0,
          reactionWeight: 0, workingWeight: 0, tiltDir: 1, headYaw: 0, headPitch: 0, headPitchCue: 0
        })
        : {};
      for (const bone of POSE_BONES) targets[bone] = (base[bone] || [0, 0, 0]).slice();

      const rigCtx = buildRigContext();
      const res = C.vrmClip?.applySampleToTargets?.(sample, targets, 1, rigCtx);

      for (const bone of POSE_BONES) {
        const node = vrm.humanoid?.getNormalizedBoneNode(bone);
        if (!node) continue;
        const t = targets[bone] || [0, 0, 0];
        const [x, y, z] = conjugateEuler(t, isVrm0);
        node.rotation.set(x, y, z, "XYZ");
      }

      // Apply gaze / lookAt target.
      if (lookTarget) {
        const yaw = res && Number.isFinite(res.eyeYaw) ? res.eyeYaw : ((sample.head?.yaw_deg || 0) * DEG * 0.45);
        const pitch = res && Number.isFinite(res.eyePitch) ? -res.eyePitch : ((sample.head?.pitch_deg || 0) * DEG * 0.45);
        const dist = 1.4;
        lookTarget.position.set(
          rigCtx.headPos[0] + Math.sin(yaw) * dist,
          rigCtx.headPos[1] - Math.sin(pitch) * dist,
          rigCtx.headPos[2] + Math.cos(yaw) * dist
        );
      }

      applyExpressions(res?.expressionTargets || sample.expression || null);
      vrm.update(0);
      vrm.scene.updateMatrixWorld(true);

      // Compute active Target Attach tether endpoints for SVG overlay.
      lastTethers = [];
      if (sample.target_attach && C.vrmIk) {
        const slotOrigin = {
          gaze: rigCtx.headPos,
          head: rigCtx.headPos,
          hand_l: boneWorld("leftHand"),
          hand_r: boneWorld("rightHand")
        };
        for (const [slot, spec] of Object.entries(sample.target_attach)) {
          const resolved = C.vrmIk.resolveTargetAttach(spec, rigCtx);
          if (resolved && slotOrigin[slot]) {
            lastTethers.push({ slot, space: resolved.space, from: slotOrigin[slot], to: [resolved.x, resolved.y, resolved.z] });
          }
        }
      }

      if (!gizmoDragging) syncGizmoPositions();
      markActive();
    }

    function boneWorld(name) {
      const node = vrm?.humanoid?.getNormalizedBoneNode(name);
      if (!node || !scratchVec.a) return null;
      node.getWorldPosition(scratchVec.a);
      return [scratchVec.a.x, scratchVec.a.y, scratchVec.a.z];
    }

    function syncGizmoPositions() {
      if (!gizmos.group || !C.vrmIk || !vrm) return;
      const kf = currentKeyframe || currentSample;
      const lChain = armChains.left;
      const rChain = armChains.right;
      const setIkMesh = (mesh, side, chain) => {
        const ik = kf?.hands?.[side]?.ik;
        if (ik && chain) {
          const w = C.vrmIk.chestLocalToWorld(ik, chain);
          mesh.position.set(w[0], w[1], w[2]);
        } else {
          const bw = boneWorld(side + "Hand");
          if (bw) mesh.position.set(bw[0], bw[1], bw[2]);
        }
      };
      const setPoleMesh = (mesh, side, chain) => {
        const pole = kf?.hands?.[side]?.pole || [side === "left" ? 0.3 : -0.3, -0.35, -0.85];
        const elbow = boneWorld(side + "LowerArm") || (chain ? [chain.chestPos[0] + chain.shoulderOffset[0], chain.chestPos[1] - 0.1, chain.chestPos[2]] : [0, 1.1, 0]);
        const len = Math.hypot(pole[0], pole[1], pole[2]) || 1;
        mesh.position.set(elbow[0] + (pole[0] / len) * 0.24, elbow[1] + (pole[1] / len) * 0.24, elbow[2] + (pole[2] / len) * 0.24);
      };
      setIkMesh(gizmos.ikLeft, "left", lChain);
      setIkMesh(gizmos.ikRight, "right", rChain);
      setPoleMesh(gizmos.poleLeft, "left", lChain);
      setPoleMesh(gizmos.poleRight, "right", rChain);

      const headW = boneWorld("head") || [framing.centerX, framing.headY, framing.headZ];
      const yaw = (kf?.head?.yaw_deg || 0) * DEG;
      const pitch = (kf?.head?.pitch_deg || 0) * DEG;
      gizmos.look.position.set(headW[0] + Math.sin(yaw) * 0.42, headW[1] - Math.sin(pitch) * 0.42, headW[2] + Math.cos(yaw) * 0.42);

      if (activePropId === "smartphone" || activePropId === "laptop") {
        const p = props?.getPropAnchorWorld?.(runtime?.THREE, activePropId, activePropId === "smartphone" ? "grip" : "screen");
        if (p) gizmos.prop.position.set(p.x, p.y, p.z);
      }

      const showClipGizmos = playingKind !== "vrma";
      gizmos.ikLeft.visible = showClipGizmos && activeMode === "ik";
      gizmos.ikRight.visible = showClipGizmos && activeMode === "ik";
      gizmos.poleLeft.visible = showClipGizmos && activeMode === "pole";
      gizmos.poleRight.visible = showClipGizmos && activeMode === "pole";
      gizmos.look.visible = showClipGizmos && activeMode === "look";
      gizmos.prop.visible = activeMode === "prop" && activePropId !== "none";
    }

    function syncGizmoAttachment() {
      if (!transform) return;
      syncGizmoPositions();
      let target = null;
      if (playingKind !== "vrma") {
        if (activeMode === "ik") target = activeSide === "left" ? gizmos.ikLeft : gizmos.ikRight;
        else if (activeMode === "pole") target = activeSide === "left" ? gizmos.poleLeft : gizmos.poleRight;
        else if (activeMode === "look") target = gizmos.look;
      }
      if (activeMode === "prop" && activePropId !== "none") target = gizmos.prop;
      if (target && target.visible) transform.attach(target);
      else transform.detach();
      markActive();
    }

    function handleTransformDrag() {
      if (!vrm || !C.vrmIk) return;
      if (activeMode === "ik") {
        const mesh = activeSide === "left" ? gizmos.ikLeft : gizmos.ikRight;
        const chain = armChains[activeSide];
        if (!chain) return;
        const local = C.vrmIk.worldToChestLocal([mesh.position.x, mesh.position.y, mesh.position.z], chain)
          .map(v => Number(clamp(v, -1.35, 1.35).toFixed(3)));
        opts.onGizmoDrag?.({ kind: "ik", side: activeSide, value: local });
      } else if (activeMode === "pole") {
        const mesh = activeSide === "left" ? gizmos.poleLeft : gizmos.poleRight;
        const elbow = boneWorld(activeSide + "LowerArm") || [0, 1.1, 0];
        const dx = mesh.position.x - elbow[0], dy = mesh.position.y - elbow[1], dz = mesh.position.z - elbow[2];
        const l = Math.hypot(dx, dy, dz) || 1;
        opts.onGizmoDrag?.({ kind: "pole", side: activeSide, value: [Number((dx / l).toFixed(3)), Number((dy / l).toFixed(3)), Number((dz / l).toFixed(3))] });
      } else if (activeMode === "look") {
        const headW = boneWorld("head") || [framing.centerX, framing.headY, framing.headZ];
        const dx = gizmos.look.position.x - headW[0];
        const dy = gizmos.look.position.y - headW[1];
        const dz = Math.max(0.05, gizmos.look.position.z - headW[2]);
        const yaw_deg = Math.round(clamp(Math.atan2(dx, dz) / DEG, -45, 45));
        const pitch_deg = Math.round(clamp(-Math.atan2(dy, Math.hypot(dx, dz)) / DEG, -30, 30));
        opts.onGizmoDrag?.({ kind: "look", value: { yaw_deg, pitch_deg } });
      } else if (activeMode === "prop" && activePropId !== "none") {
        const cur = calibration?.props?.[activePropId]?.offset || [0, 0, 0];
        const anchorNow = props?.getPropAnchorWorld?.(runtime?.THREE, activePropId, activePropId === "smartphone" ? "grip" : "screen");
        if (!anchorNow) return;
        const nextOff = [
          Number(clamp((cur[0] || 0) + (gizmos.prop.position.x - anchorNow.x), -0.5, 0.5).toFixed(3)),
          Number(clamp((cur[1] || 0) + (gizmos.prop.position.y - anchorNow.y), -0.5, 0.5).toFixed(3)),
          Number(clamp((cur[2] || 0) + (gizmos.prop.position.z - anchorNow.z), -0.5, 0.5).toFixed(3))
        ];
        opts.onGizmoDrag?.({ kind: "prop", propId: activePropId, offset: nextOff });
      }
    }

    function projectToSvg(pt, w, h) {
      if (!pt || !camera || !scratchVec.b) return null;
      scratchVec.b.set(pt[0], pt[1], pt[2]).project(camera);
      if (scratchVec.b.z > 1) return null;
      return { x: (scratchVec.b.x + 1) * 0.5 * w, y: (1 - scratchVec.b.y) * 0.5 * h };
    }

    function drawOverlay() {
      if (!overlaySvg || !viewportEl) return;
      const rect = viewportEl.getBoundingClientRect();
      const w = Math.max(120, Math.round(rect.width || 640));
      const h = Math.max(120, Math.round(rect.height || 480));
      overlaySvg.setAttribute("viewBox", "0 0 " + w + " " + h);
      const ns = "http://www.w3.org/2000/svg";
      const frag = document.createDocumentFragment();
      const mk = (tag, attrs, text) => {
        const el = document.createElementNS(ns, tag);
        for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, String(v));
        if (text) el.textContent = text;
        return el;
      };

      // Companion slot framing guide (220 / 280 / 360px proportional to studio viewport).
      const { sx, sy, sw, sh } = computeSlotGuideRect(w, h, slotPx);
      const stroke = theme === "light" ? "rgba(34,36,42,0.28)" : "rgba(231,231,228,0.22)";
      const accent = theme === "light" ? "rgba(47,95,208,0.52)" : "rgba(122,162,255,0.48)";
      const headroomY = Math.round(sy + sh * 0.12);

      frag.appendChild(mk("rect", {
        x: sx, y: sy, width: sw, height: sh, rx: 12,
        fill: "none", stroke, "stroke-width": 1.25, "stroke-dasharray": "5 5"
      }));
      // 12% headroom calibration line
      frag.appendChild(mk("line", {
        x1: sx + 8, y1: headroomY, x2: sx + sw - 8, y2: headroomY,
        stroke: accent, "stroke-width": 1, "stroke-dasharray": "3 4"
      }));
      frag.appendChild(mk("text", {
        x: sx + 6, y: Math.max(14, sy - 6), fill: stroke, "font-size": 10, "font-family": "ui-monospace, monospace", "font-weight": 700
      }, "SLOT " + slotPx + "px · 12% HEADROOM"));

      // Target Attach tether lines
      for (const t of lastTethers) {
        const a = projectToSvg(t.from, w, h);
        // A camera target sits on the camera's own z=0 plane (projection singularity);
        // place its visual marker cleanly 36% along the ray toward the camera instead.
        const toPt = t.space === "camera"
          ? [t.from[0] + (t.to[0] - t.from[0]) * 0.36, t.from[1] + (t.to[1] - t.from[1]) * 0.36 + 0.08, t.from[2] + (t.to[2] - t.from[2]) * 0.36]
          : t.to;
        const b = projectToSvg(toPt, w, h);
        if (!a || !b) continue;
        const col = t.slot.startsWith("hand") ? (theme === "light" ? "#0e7a83" : "#5fd0e0") : (theme === "light" ? "#6a4fc4" : "#b7a5f5");
        frag.appendChild(mk("line", {
          x1: a.x.toFixed(1), y1: a.y.toFixed(1), x2: b.x.toFixed(1), y2: b.y.toFixed(1),
          stroke: col, "stroke-width": 1.6, "stroke-dasharray": "4 3"
        }));
        frag.appendChild(mk("circle", { cx: b.x.toFixed(1), cy: b.y.toFixed(1), r: 4.5, fill: col }));
        const halo = theme === "light" ? "rgba(246,245,241,0.92)" : "rgba(17,18,22,0.88)";
        frag.appendChild(mk("text", {
          x: (b.x + 8).toFixed(1), y: (b.y + 3).toFixed(1), fill: col,
          stroke: halo, "stroke-width": 3, "stroke-linejoin": "round", "paint-order": "stroke",
          "font-size": 10, "font-family": "ui-monospace, monospace", "font-weight": 700
        }, t.slot + " → " + t.space));
      }

      overlaySvg.replaceChildren(frag);
    }

    function loop(now) {
      rafId = 0;
      if (disposed || !renderer || !scene || !camera) return;
      const dt = Math.min(0.1, Math.max(0.001, (now - lastFrameAt) / 1000));
      lastFrameAt = now;

      if (orbit) orbit.update();
      if (isPlaying) {
        if (playingKind === "vrma" && mixer && vrm) {
          mixer.update(dt * (opts.getSpeed ? opts.getSpeed() : 1));
          vrm.update(dt);
        } else {
          opts.onPlayStep?.(dt);
        }
      }
      renderer.render(scene, camera);
      drawOverlay();
      renderCount++;

      if (isPlaying || now < activeUntil) {
        rafId = requestAnimationFrame(loop);
      }
    }

    return {
      init,
      loadVrm,
      loadVrmaBuffer,
      stopVrma,
      applyClipSample,
      setMode(mode) { activeMode = mode; syncGizmoAttachment(); },
      setSide(side) { activeSide = side === "left" ? "left" : "right"; syncGizmoAttachment(); },
      setSlot(px) { slotPx = Number(px) || 280; markActive(); },
      setTheme(next) {
        theme = next === "light" ? "light" : "dark";
        studioLighting?.applyTheme(theme);
        markActive();
      },
      setProp(propId) {
        activePropId = propId === "laptop" || propId === "smartphone" ? propId : "none";
        updatePropVisibility();
        syncGizmoAttachment();
      },
      setCalibration(cal) {
        calibration = cal && typeof cal === "object" ? cal : { props: {} };
        props?.setCalibration?.(calibration);
        syncGizmoPositions();
        markActive();
      },
      setPlaying(flag, kind) {
        isPlaying = !!flag;
        if (kind) playingKind = kind;
        syncGizmoAttachment();
        if (isPlaying) markActive();
      },
      seekVrma(timeSec) {
        if (activeVrmaAction && mixer && vrm) {
          activeVrmaAction.time = Number(timeSec) || 0;
          mixer.update(0);
          vrm.update(0);
          markActive();
        }
      },
      resetCamera,
      resize,
      markActive,
      getDebugState() {
        return {
          ready: !!vrm,
          isVrm0,
          metaVersion: vrm ? String(vrm.meta?.metaVersion ?? "1") : "",
          framing,
          chains: armChains,
          activeMode,
          activeSide,
          slotPx,
          theme,
          propId: activePropId,
          renderCount,
          rafActive: !!rafId,
          playing: isPlaying,
          playingKind,
          vrmaLoaded: !!activeVrmaClip,
          tetherCount: lastTethers.length
        };
      },
      dispose() {
        disposed = true;
        stopLoop();
        resizeObserver?.disconnect?.();
        document.removeEventListener("visibilitychange", onVisibilityChange);
        canvas?.removeEventListener("pointerdown", onCanvasPointerDown);
        disposeVrmOnly();
        props?.dispose?.();
        studioLighting?.dispose?.();
        orbit?.dispose?.();
        transform?.dispose?.();
        for (const d of disposables) { try { d.dispose?.(); } catch (_) {} }
        renderer?.dispose?.();
      }
    };
  }

  Object.assign(C, { rigStudioStage: { createStage } });
})(window.WsCompanion);
