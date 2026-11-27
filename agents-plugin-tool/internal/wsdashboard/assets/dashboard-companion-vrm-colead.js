(function (C) {
  "use strict";

  // ============================================================================
  // Glass-Wall Co-Lead Mode: Pure-FK Laptop Typing Pose, Gaze Choreography,
  // Sleek Beveled Foreground Laptop Lid, and Soft Screen Bounce Glow
  // ============================================================================
  // Pure procedural FK (zero 2-bone IK) with frame-rate-independent exponential
  // damping (1 - Math.exp(-k * dt)). Every pose is authored in VRM 1.0
  // normalized humanoid coordinates (+X character-left, +Y up, +Z forward,
  // Euler XYZ) and conjugated for VRM 0.x by dashboard-companion-vrm.js.

  const DEG = Math.PI / 180;
  const DEFAULT_ACCENT = "#7aa2ff";
  const VALID_BEATS = new Set(["auto", "typing", "glance-up", "working", "sleepy", "reading-left", "reading-right"]);

  function clamp(v, lo, hi) {
    return v < lo ? lo : (v > hi ? hi : v);
  }

  function dampAlpha(rate, dt) {
    const safeDt = Number.isFinite(dt) && dt > 0 ? Math.min(dt, 0.2) : 0.016;
    return 1 - Math.exp(-rate * safeDt);
  }

  function damp(cur, target, rate, dt) {
    const a = dampAlpha(rate, dt);
    return cur + (target - cur) * a;
  }

  function normalizeHexColor(input) {
    if (typeof input === "number" && Number.isFinite(input)) {
      const clamped = Math.max(0, Math.min(0xffffff, Math.round(input)));
      return "#" + clamped.toString(16).padStart(6, "0");
    }
    if (typeof input !== "string") return null;
    const raw = input.trim();
    if (/^#[0-9a-fA-F]{6}$/.test(raw)) return raw.toLowerCase();
    if (/^#[0-9a-fA-F]{3}$/.test(raw)) {
      return "#" + raw[1] + raw[1] + raw[2] + raw[2] + raw[3] + raw[3];
    }
    return null;
  }

  function hexToRgb(hex) {
    const norm = normalizeHexColor(hex) || DEFAULT_ACCENT;
    const n = parseInt(norm.slice(1), 16);
    return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  }

  function mixRgbToHex(a, b, t) {
    const k = clamp(t, 0, 1);
    const r = Math.round(a[0] + (b[0] - a[0]) * k);
    const g = Math.round(a[1] + (b[1] - a[1]) * k);
    const bl = Math.round(a[2] + (b[2] - a[2]) * k);
    return (r << 16) | (g << 8) | bl;
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

  // Paint a refined brushed anodized dark-slate lid texture matching Werkschaft's
  // dark tonal surface (#222630 - #2b303d) with a subtle chamfered top specular edge.
  function makeLidTexture(THREE) {
    if (typeof document === "undefined" || typeof THREE.CanvasTexture !== "function") return null;
    const canvas = document.createElement("canvas");
    canvas.width = 512;
    canvas.height = 320;
    const ctx = canvas.getContext("2d");
    if (!ctx || typeof ctx.createLinearGradient !== "function" || typeof ctx.fillRect !== "function") return null;

    // Anodized slate vertical metallic sheen (brighter softly near top bezel).
    const grad = ctx.createLinearGradient(0, 0, 0, canvas.height);
    grad.addColorStop(0.0, "#3d4658");
    grad.addColorStop(0.04, "#2f3645");
    grad.addColorStop(0.30, "#262b38");
    grad.addColorStop(0.76, "#1f242f");
    grad.addColorStop(1.0, "#171a22");
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, canvas.width, canvas.height);

    // Subtle horizontal brushed-aluminum specular band across the top curve.
    const sheen = ctx.createLinearGradient(0, 0, canvas.width, 0);
    sheen.addColorStop(0.0, "rgba(185, 205, 240, 0.03)");
    sheen.addColorStop(0.28, "rgba(215, 228, 252, 0.11)");
    sheen.addColorStop(0.52, "rgba(235, 244, 255, 0.17)");
    sheen.addColorStop(0.76, "rgba(210, 224, 248, 0.09)");
    sheen.addColorStop(1.0, "rgba(185, 205, 240, 0.03)");
    ctx.fillStyle = sheen;
    ctx.fillRect(0, 0, canvas.width, 54);

    // Delicate brushed micro-grain lines.
    ctx.fillStyle = "rgba(255, 255, 255, 0.016)";
    for (let y = 4; y < canvas.height; y += 3) {
      ctx.fillRect(0, y, canvas.width, 1);
    }

    // Crisp hairline specular bevel highlight along the very top edge.
    const rim = ctx.createLinearGradient(24, 0, canvas.width - 24, 0);
    rim.addColorStop(0.0, "rgba(164, 184, 220, 0.22)");
    rim.addColorStop(0.5, "rgba(230, 240, 255, 0.56)");
    rim.addColorStop(1.0, "rgba(164, 184, 220, 0.22)");
    ctx.fillStyle = rim;
    ctx.fillRect(14, 2, canvas.width - 28, 3);

    const tex = new THREE.CanvasTexture(canvas);
    if (THREE.SRGBColorSpace) tex.colorSpace = THREE.SRGBColorSpace;
    tex.needsUpdate = true;
    return tex;
  }

  // Soft radial halo texture for the minimalist center-upper accent emblem pill.
  function makeEmblemGlowTexture(THREE) {
    if (typeof document === "undefined" || typeof THREE.CanvasTexture !== "function") return null;
    const canvas = document.createElement("canvas");
    canvas.width = 128;
    canvas.height = 64;
    const ctx = canvas.getContext("2d");
    if (!ctx || typeof ctx.createRadialGradient !== "function" || typeof ctx.beginPath !== "function") return null;

    ctx.clearRect(0, 0, 128, 64);
    const halo = ctx.createRadialGradient(64, 32, 3, 64, 32, 54);
    halo.addColorStop(0.0, "rgba(255, 255, 255, 0.92)");
    halo.addColorStop(0.28, "rgba(255, 255, 255, 0.45)");
    halo.addColorStop(0.62, "rgba(255, 255, 255, 0.12)");
    halo.addColorStop(1.0, "rgba(255, 255, 255, 0.0)");
    ctx.fillStyle = halo;
    ctx.fillRect(0, 0, 128, 64);

    // Crisp minimalist centered capsule pill inside the soft halo.
    const px = 42, py = 26, pw = 44, ph = 12, r = 6;
    ctx.beginPath();
    ctx.moveTo(px + r, py);
    ctx.lineTo(px + pw - r, py);
    ctx.quadraticCurveTo(px + pw, py, px + pw, py + r);
    ctx.lineTo(px + pw, py + ph - r);
    ctx.quadraticCurveTo(px + pw, py + ph, px + pw - r, py + ph);
    ctx.lineTo(px + r, py + ph);
    ctx.quadraticCurveTo(px, py + ph, px, py + ph - r);
    ctx.lineTo(px, py + r);
    ctx.quadraticCurveTo(px, py, px + r, py);
    ctx.closePath();
    ctx.fillStyle = "rgba(255, 255, 255, 0.96)";
    ctx.fill();

    const tex = new THREE.CanvasTexture(canvas);
    if (THREE.SRGBColorSpace) tex.colorSpace = THREE.SRGBColorSpace;
    tex.needsUpdate = true;
    return tex;
  }

  // Build a smooth rounded-rectangle THREE.Shape centered at (0, 0) for the lid.
  function makeRoundedRectShape(THREE, w, h, radius) {
    const shape = new THREE.Shape();
    const hw = w * 0.5;
    const hh = h * 0.5;
    const r = Math.min(radius, hw * 0.35, hh * 0.35);
    shape.moveTo(-hw + r, -hh);
    shape.lineTo(hw - r, -hh);
    shape.quadraticCurveTo(hw, -hh, hw, -hh + r);
    shape.lineTo(hw, hh - r);
    shape.quadraticCurveTo(hw, hh, hw - r, hh);
    shape.lineTo(-hw + r, hh);
    shape.quadraticCurveTo(-hw, hh, -hw, hh - r);
    shape.lineTo(-hw, -hh + r);
    shape.quadraticCurveTo(-hw, -hh, -hw + r, -hh);
    return shape;
  }

  // Pure-FK Co-Lead bone pose calculator in VRM 1.0 normalized coordinates.
  function computeCoLeadTargets(params) {
    const t = params.elapsed || 0;
    const typingW = clamp(params.typingWeight || 0, 0, 1);
    const glanceW = clamp(params.glanceWeight || 0, 0, 1);
    const sleepyW = clamp(params.sleepyWeight || 0, 0, 1);
    const reactionW = clamp(params.reactionWeight || 0, 0, 1);
    const workingW = clamp(params.workingWeight || 0, 0, 1);
    const tiltDir = params.tiltDir >= 0 ? 1 : -1;
    const headYaw = Number.isFinite(params.headYaw) ? params.headYaw : 0;
    const headPitch = Number.isFinite(params.headPitch) ? params.headPitch : 0;
    const headPitchCue = Number.isFinite(params.headPitchCue) ? params.headPitchCue : 0;

    // Gentle alive breathing (slower and deeper when sleepy).
    const breathFreq = 1.42 - sleepyW * 0.42 + workingW * 0.14;
    const breath = Math.sin(t * breathFreq);
    const sway = Math.sin(t * 0.68) * (1 - workingW * 0.45 - sleepyW * 0.3);

    // Organic multi-harmonic alternating left/right keystroke cadence (zero metronome feel).
    const rate = 6.2 + workingW * 3.4;
    const tapL = (Math.sin(t * rate) * 0.62 + Math.sin(t * rate * 1.73 + 0.9) * 0.38) * typingW;
    const tapR = (Math.sin(t * rate + Math.PI * 0.92) * 0.62 + Math.sin(t * rate * 1.61 + 2.4) * 0.38) * typingW;
    const wristL = Math.sin(t * rate * 1.15 + 0.4) * typingW;
    const wristR = Math.sin(t * rate * 1.15 + 3.4) * typingW;

    // Cozy forward seated lean while typing; relaxes slightly upright when glancing up.
    const leanScale = (1 - glanceW * 0.65 - reactionW * 0.45);
    const spinePitch = (0.036 + workingW * 0.014) * leanScale + sleepyW * 0.030 + breath * 0.010;
    const chestPitch = (0.028 + workingW * 0.012) * leanScale + sleepyW * 0.022 + breath * 0.015;
    const upperChestPitch = 0.014 * leanScale + breath * 0.008;

    // Charming anime head-tilt on glance-up (+/-7.6 deg) + tiny reading tilt while typing.
    const glanceRoll = tiltDir * 0.132 * glanceW;
    const idleRoll = sway * 0.016 * (1 - glanceW) + Math.sin(t * 0.48) * 0.014 * workingW * (1 - glanceW);
    const sleepyNod = sleepyW * (0.125 + Math.sin(t * 0.82) * 0.035);

    const totalPitch = headPitch + headPitchCue + sleepyNod;
    const neckPitch = totalPitch * 0.40;
    const craniumPitch = totalPitch * 0.60;
    const neckYaw = headYaw * 0.36;
    const craniumYaw = headYaw * 0.64 + sway * 0.012 * (1 - glanceW);
    const neckRoll = glanceRoll * 0.34;
    const craniumRoll = glanceRoll * 0.66 + idleRoll;

    // Relaxed lowered shoulders rolled subtly forward toward her desk.
    const shForward = 0.048 * (1 - sleepyW * 0.45);
    const shDrop = 0.046 + sleepyW * 0.018;
    const lSh = [0.012 + tapL * 0.006, -shForward, -shDrop + breath * 0.012 + tapL * 0.007];
    const rSh = [0.012 + tapR * 0.006, shForward, shDrop - breath * 0.012 - tapR * 0.007];

    // Upper arms stay naturally lowered along her sides (|z| >= 1.20) and swing
    // gently forward so elbows clear the ribcage and hands stay strictly behind the lid.
    const activePoseBlend = 1 - sleepyW * 0.55;
    const uPitch = -0.17 * activePoseBlend - workingW * 0.025 + glanceW * 0.03;
    const uYaw = 0.12 * activePoseBlend;
    const uDrop = 1.22 + sleepyW * 0.06;

    const lUpper = [
      uPitch + tapL * 0.015,
      -uYaw + tapL * 0.010,
      -uDrop - breath * 0.008
    ];
    const rUpper = [
      uPitch + tapR * 0.015,
      uYaw - tapR * 0.010,
      uDrop + breath * 0.008
    ];

    // Forearms flex cleanly forward (Ry negative on left, positive on right)
    // and angle gently inward onto the unseen laptop keyboard strictly behind the lid.
    const fFlex = 1.12 * activePoseBlend + sleepyW * 0.28 - glanceW * 0.10;
    const fInward = 0.28 * activePoseBlend + sleepyW * 0.06;
    const fTwist = 0.16 * activePoseBlend;

    const lLower = [
      fTwist + tapL * 0.030,
      -(fFlex + tapL * 0.045),
      -(fInward + tapL * 0.018)
    ];
    const rLower = [
      fTwist + tapR * 0.030,
      fFlex + tapR * 0.045,
      fInward + tapR * 0.018
    ];

    // Delicate alternating wrist/hand key-tap articulation angled downward onto keys.
    const lHand = [0.08 + wristL * 0.055, 0.05 + tapL * 0.035, 0.10 + wristL * 0.04];
    const rHand = [0.08 + wristR * 0.055, -(0.05 + tapR * 0.035), -(0.10 + wristR * 0.04)];

    return {
      spine: [spinePitch, sway * 0.009 * (1 - glanceW * 0.5), 0],
      chest: [chestPitch, 0, 0],
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
  }

  function createVrmCoLead(opts = {}) {
    const THREE = opts.THREE || null;
    const scene = opts.scene || null;
    const camera = opts.camera || null;

    let accentHex = DEFAULT_ACCENT;
    let overrideBeat = "auto";
    let explicitHover = null;

    // Rhythm state machine.
    let phase = "typing"; // 'typing' | 'reading' | 'glance-up'
    let phaseTimer = 2.1;
    let phaseCycle = 0;
    let timeSinceGlance = 0;
    let nextGlanceInterval = 8.4;
    let glanceTiltDir = 1;
    let lastActivity = "idle";

    // Smoothly damped blending weights (all frame-rate independent).
    let typingWeight = 1.0;
    let glanceWeight = 0.0;
    let sleepyWeight = 0.0;
    let reactionWeight = 0.0;
    let workingWeight = 0.0;
    let screenGlow = 0.13;

    // 3D foreground laptop prop + soft upward screen bounce light.
    let laptopGroup = null;
    let lidMesh = null;
    let rimMesh = null;
    let emblemMesh = null;
    let screenLight = null;
    let lidTexture = null;
    let emblemTexture = null;
    const geometries = [];
    const materials = [];
    const textures = [];

    // Cached framing & telemetry measurements (zero per-frame allocations).
    let lastFraming = null;
    let lidTopWorldY = 0;
    let lidTopWorldZ = 0;
    let lidTopViewportPct = 0;
    let chinViewportPct = 0;
    let headViewportPct = 0;
    const scratchVec = THREE && typeof THREE.Vector3 === "function" ? new THREE.Vector3() : null;

    const LID_TILT_RAD = 11 * DEG; // Top of open lid tilts gently toward viewer (+Z).

    function canBuild3DProp() {
      return !!(
        THREE &&
        scene &&
        typeof THREE.Group === "function" &&
        typeof THREE.Shape === "function" &&
        typeof THREE.ExtrudeGeometry === "function" &&
        typeof THREE.Mesh === "function" &&
        typeof THREE.MeshStandardMaterial === "function"
      );
    }

    function applyAccentVisuals() {
      const rgb = hexToRgb(accentHex);
      if (emblemMesh?.material?.color?.setRGB) {
        emblemMesh.material.color.setRGB(rgb[0] / 255, rgb[1] / 255, rgb[2] / 255);
      }
      if (screenLight?.color?.setHex) {
        // Blend only 22% of accent tint over flattering anime cool-ivory so skin stays rosy & cute.
        const bounceHex = mixRgbToHex([222, 236, 255], rgb, 0.22);
        screenLight.color.setHex(bounceHex);
      }
    }

    function buildPropIfNeeded() {
      if (laptopGroup || !THREE || !scene) return;
      if (typeof THREE.PointLight === "function") {
        // Gentle low-decay upward bounce fill so skin stays rosy and anime-cute
        // with zero localized specular hotspot on dark hoodies or pale MToon faces.
        screenLight = new THREE.PointLight(0xdceaff, screenGlow, 1.85, 0.65);
        scene.add(screenLight);
      }
      if (!canBuild3DProp()) {
        applyAccentVisuals();
        return;
      }

      laptopGroup = new THREE.Group();
      laptopGroup.name = "ws-colead-laptop";

      // Unit-sized sleek beveled lid (scaled cleanly per avatar framing in layoutProp).
      // Base unit width = 1.0, height = 0.62, ultra-thin depth = 0.012 + smooth bevel.
      const lidShape = makeRoundedRectShape(THREE, 1.0, 0.62, 0.048);
      const lidGeo = new THREE.ExtrudeGeometry(lidShape, {
        depth: 0.012,
        bevelEnabled: true,
        bevelThickness: 0.007,
        bevelSize: 0.006,
        bevelSegments: 4,
        curveSegments: 10
      });
      // Center depth and normalize UVs over [0, 1] so our procedural brushed texture maps cleanly.
      lidGeo.translate(0, 0, -0.006);
      const posAttr = lidGeo.attributes?.position;
      const uvAttr = lidGeo.attributes?.uv;
      if (posAttr && uvAttr) {
        for (let i = 0; i < posAttr.count; i++) {
          const u = clamp((posAttr.getX(i) + 0.506) / 1.012, 0, 1);
          const v = clamp((posAttr.getY(i) + 0.316) / 0.632, 0, 1);
          uvAttr.setXY(i, u, v);
        }
        uvAttr.needsUpdate = true;
      }
      geometries.push(lidGeo);

      lidTexture = makeLidTexture(THREE);
      if (lidTexture) textures.push(lidTexture);

      const lidMat = new THREE.MeshStandardMaterial({
        color: lidTexture ? 0xffffff : 0x262b36,
        map: lidTexture || null,
        roughness: 0.42,
        metalness: 0.34,
        emissive: 0x0b0e14,
        emissiveIntensity: 0.35
      });
      materials.push(lidMat);

      lidMesh = new THREE.Mesh(lidGeo, lidMat);
      lidMesh.frustumCulled = false;
      laptopGroup.add(lidMesh);

      // Subtle machined anodized top-edge catchlight trim so silhouette reads crisply on dark rail.
      if (typeof THREE.PlaneGeometry === "function" && typeof THREE.MeshBasicMaterial === "function") {
        const rimGeo = new THREE.PlaneGeometry(0.91, 0.010);
        const rimMat = new THREE.MeshBasicMaterial({
          color: 0xb6c8e8,
          transparent: true,
          opacity: 0.46,
          depthWrite: false
        });
        geometries.push(rimGeo);
        materials.push(rimMat);
        rimMesh = new THREE.Mesh(rimGeo, rimMat);
        rimMesh.position.set(0, 0.311, 0.0115);
        rimMesh.frustumCulled = false;
        laptopGroup.add(rimMesh);

        // Minimalist center-upper soft-glowing accent emblem pill (Step 3 themeable!).
        emblemTexture = makeEmblemGlowTexture(THREE);
        if (emblemTexture) textures.push(emblemTexture);
        const emblemGeo = new THREE.PlaneGeometry(0.125, 0.056);
        const emblemMat = new THREE.MeshBasicMaterial({
          color: 0x7aa2ff,
          map: emblemTexture || null,
          transparent: true,
          opacity: 0.88,
          depthWrite: false
        });
        geometries.push(emblemGeo);
        materials.push(emblemMat);
        emblemMesh = new THREE.Mesh(emblemGeo, emblemMat);
        // Positioned in the upper bezel zone so it stays crisply visible above the bottom dissolve mask on all avatars.
        emblemMesh.position.set(0, 0.252, 0.0145);
        emblemMesh.frustumCulled = false;
        laptopGroup.add(emblemMesh);
      }

      laptopGroup.rotation.set(LID_TILT_RAD, 0, 0, "XYZ");
      scene.add(laptopGroup);
      applyAccentVisuals();
    }

    // Position the sleek laptop lid strictly in the bottom ~16-18% of the bust viewport,
    // ALWAYS clearly below her chin, neck, and upper chest on BOTH VRM 0.x and VRM 1.0.
    function layoutProp(framing, camOverride) {
      lastFraming = framing || lastFraming;
      const cam = camOverride || camera;
      if (!lastFraming || !cam) return;
      buildPropIfNeeded();

      const headY = Number.isFinite(lastFraming.headY) ? lastFraming.headY : 1.45;
      const chestY = Number.isFinite(lastFraming.chestY) ? lastFraming.chestY : 1.18;
      const bustSpan = Math.max(0.18, Number.isFinite(lastFraming.bustSpan) ? lastFraming.bustSpan : (headY - chestY));
      const targetY = Number.isFinite(lastFraming.targetY) ? lastFraming.targetY : 1.38;
      const headZ = Number.isFinite(lastFraming.headZ) ? lastFraming.headZ : 0;
      const centerX = Number.isFinite(lastFraming.centerX) ? lastFraming.centerX : 0;
      const camZ = Number.isFinite(cam.position?.z) ? cam.position.z : (lastFraming.cameraZ || 1.85);
      const camY = Number.isFinite(cam.position?.y) ? cam.position.y : targetY;
      const camDist = Math.max(0.35, camZ - headZ);

      // Place laptop cleanly in front of her torso & hands (strictly clearing fingertips)
      // while leaving comfortable depth from the camera.
      const laptopZ = headZ + clamp(bustSpan * 1.56, 0.38, Math.min(0.58, camDist * 0.42));
      const dToCam = Math.max(0.22, camZ - laptopZ);
      const fovRad = ((cam.fov || 26) * DEG);
      const aspect = Math.max(0.5, cam.aspect || 1.4);
      const visHalfH = dToCam * Math.tan(fovRad * 0.5);
      const visHalfW = visHalfH * aspect;
      const visH = visHalfH * 2;

      // Exact vertical center of the camera frustum at z = laptopZ.
      const fracFromCam = dToCam / camDist;
      const rayY = camY + (targetY - camY) * fracFromCam;
      const screenBottomY = rayY - visHalfH;

      // Target top edge at 17% from the bottom of the viewport (inside 14-19% invariant),
      // strictly clamped well below chin, neck, and upper chest.
      const desiredTopY = screenBottomY + visH * 0.172;
      const maxSafeTopY = Math.min(
        chestY - bustSpan * 0.14,
        headY - bustSpan * 0.92
      );
      const lidTopY = Math.min(desiredTopY, maxSafeTopY);

      // Sleek ~54% viewport width so her shoulders and sleeves frame nicely on both sides.
      const lidScale = clamp(visHalfW * 1.08, 0.24, Math.max(0.32, bustSpan * 1.95));
      const halfLidH = 0.316 * lidScale;
      const halfLidD = 0.013 * lidScale;
      // Account for the +11 deg forward tilt when computing the center Y from desired top edge Y.
      const topOffsetY = halfLidH * Math.cos(LID_TILT_RAD) + halfLidD * Math.sin(LID_TILT_RAD);
      const topOffsetZ = halfLidH * Math.sin(LID_TILT_RAD) + halfLidD * Math.cos(LID_TILT_RAD);
      const centerY = lidTopY - topOffsetY;

      lidTopWorldY = lidTopY;
      lidTopWorldZ = laptopZ + topOffsetZ;

      if (laptopGroup) {
        laptopGroup.position.set(centerX, centerY, laptopZ);
        laptopGroup.scale.set(lidScale, lidScale, lidScale);
        laptopGroup.rotation.set(LID_TILT_RAD, 0, 0, "XYZ");
      }

      // Position soft upward screen bounce light with gentle distance so it bathes chin &
      // face evenly without creating a harsh circular hotspot on her chest/hoodie.
      if (screenLight) {
        screenLight.position.set(
          centerX,
          lidTopY + bustSpan * 0.32,
          laptopZ + Math.min(0.18, bustSpan * 0.55)
        );
        screenLight.distance = Math.max(1.4, bustSpan * 6.5);
      }

      // Measure exact viewport percentages for telemetry & automated framing assertions.
      if (scratchVec && typeof cam.updateMatrixWorld === "function" && typeof scratchVec.project === "function") {
        cam.updateMatrixWorld(true);
        const ndcToBottomPct = ndcY => clamp((ndcY + 1) * 50, -50, 150);
        scratchVec.set(centerX, lidTopWorldY, lidTopWorldZ).project(cam);
        lidTopViewportPct = ndcToBottomPct(scratchVec.y);

        // Conservative chin estimate: slightly below head bone (~headY - bustSpan*0.10) at front of face.
        const chinEstY = headY - bustSpan * 0.10;
        scratchVec.set(centerX, chinEstY, headZ + 0.08).project(cam);
        chinViewportPct = ndcToBottomPct(scratchVec.y);

        scratchVec.set(centerX, headY, headZ).project(cam);
        headViewportPct = ndcToBottomPct(scratchVec.y);
      } else {
        lidTopViewportPct = clamp(((lidTopWorldY - screenBottomY) / Math.max(0.001, visH)) * 100, 0, 100);
        chinViewportPct = clamp((((headY - bustSpan * 0.10) - screenBottomY) / Math.max(0.001, visH)) * 100, 0, 100);
        headViewportPct = clamp(((headY - screenBottomY) / Math.max(0.001, visH)) * 100, 0, 100);
      }
    }

    function advanceRhythm(dt, activity, hovered) {
      if (activity !== lastActivity) {
        if (activity === "working") {
          phase = "typing";
          phaseTimer = 2.4;
        } else if (activity === "idle" && lastActivity !== "idle") {
          phase = "reading";
          phaseTimer = 1.6;
        }
        lastActivity = activity;
      }

      if (overrideBeat !== "auto") {
        if (overrideBeat === "glance-up") {
          phase = "glance-up";
        } else if (overrideBeat === "typing" || overrideBeat === "working") {
          phase = "typing";
        } else {
          phase = "reading";
        }
        return;
      }

      if (activity === "sleepy" || activity === "reaction") {
        phase = "reading";
        timeSinceGlance = 0;
        return;
      }

      if (activity === "working") {
        timeSinceGlance = 0;
        phaseTimer -= dt;
        if (phaseTimer <= 0) {
          phaseCycle++;
          const r = hashPhase(phaseCycle);
          if (phase === "typing") {
            phase = "reading";
            phaseTimer = 0.55 + r * 0.60; // 0.55 - 1.15s brief reading pause
          } else {
            phase = "typing";
            phaseTimer = 1.85 + r * 1.55; // 1.85 - 3.40s brisk burst
          }
        }
        return;
      }

      // Idle mode: cozy typing bursts, calm reading pauses, and periodic glance-up beats (plus hover).
      if (hovered) {
        if (phase !== "glance-up") {
          glanceTiltDir = -glanceTiltDir;
        }
        phase = "glance-up";
        timeSinceGlance = 0;
        phaseTimer = Math.max(phaseTimer, 0.45);
        return;
      }

      if (phase === "glance-up") {
        phaseTimer -= dt;
        if (phaseTimer <= 0) {
          phaseCycle++;
          phase = "typing";
          phaseTimer = 1.4 + hashPhase(phaseCycle) * 1.0;
          timeSinceGlance = 0;
          nextGlanceInterval = 7.4 + hashPhase(phaseCycle + 17) * 3.2; // 7.4 - 10.6s
        }
        return;
      }

      timeSinceGlance += dt;
      if (timeSinceGlance >= nextGlanceInterval) {
        phaseCycle++;
        glanceTiltDir = -glanceTiltDir;
        phase = "glance-up";
        phaseTimer = 2.4 + hashPhase(phaseCycle + 31) * 0.55; // 2.4 - 2.95s warm glance-up
        timeSinceGlance = 0;
        return;
      }

      phaseTimer -= dt;
      if (phaseTimer <= 0) {
        phaseCycle++;
        const r = hashPhase(phaseCycle);
        if (phase === "typing") {
          phase = "reading";
          phaseTimer = 2.5 + r * 2.1; // 2.5 - 4.6s calm reading pause
        } else {
          phase = "typing";
          phaseTimer = 1.25 + r * 1.15; // 1.25 - 2.4s cozy burst
        }
      }
    }

    function step(ctx) {
      const dt = Number.isFinite(ctx.dt) && ctx.dt > 0 ? Math.min(ctx.dt, 0.2) : 0.016;
      const elapsed = Number.isFinite(ctx.elapsed) ? ctx.elapsed : 0;
      const hovered = explicitHover !== null ? explicitHover : !!ctx.hover;

      let activity = classifyActivity(ctx);
      if (overrideBeat === "working") activity = "working";
      else if (overrideBeat === "sleepy") activity = "sleepy";
      else if (overrideBeat === "typing" || overrideBeat === "glance-up" || overrideBeat === "reading-left" || overrideBeat === "reading-right") {
        activity = "idle";
      }

      advanceRhythm(dt, activity, hovered);

      const wantGlance = overrideBeat === "glance-up" || (overrideBeat === "auto" && activity === "idle" && (hovered || phase === "glance-up"));
      const wantSleepy = activity === "sleepy";
      const wantReaction = activity === "reaction";
      const wantWorking = activity === "working";
      const wantTyping = !wantGlance && !wantSleepy && !wantReaction && (
        overrideBeat === "typing" ||
        overrideBeat === "working" ||
        (overrideBeat === "auto" && phase === "typing")
      );

      typingWeight = damp(typingWeight, wantTyping ? 1 : 0, wantTyping ? 6.5 : 8.5, dt);
      glanceWeight = damp(glanceWeight, wantGlance ? 1 : 0, wantGlance ? 5.8 : 4.8, dt);
      sleepyWeight = damp(sleepyWeight, wantSleepy ? 1 : 0, 3.6, dt);
      reactionWeight = damp(reactionWeight, wantReaction ? 1 : 0, 8.0, dt);
      workingWeight = damp(workingWeight, wantWorking ? 1 : 0, 5.2, dt);

      // Compute charming head & eye gaze choreography:
      //  - Typing / reading: gentle pitch down toward screen (+5.5 deg) + soft horizontal reading scan.
      //  - Glance-up / hover: head lifts straight to camera with warm eye contact and +/-6 deg tilt.
      //  - Sleepy: soft lowered head nod.
      const gx = clamp(Number.isFinite(ctx.gazeX) ? ctx.gazeX : 0, -1, 1);
      const gy = clamp(Number.isFinite(ctx.gazeY) ? ctx.gazeY : 0, -1, 1);

      let readingScan = Math.sin(elapsed * (wantWorking ? 1.55 : 0.88)) * (wantWorking ? 0.58 : 0.42) +
        Math.sin(elapsed * (wantWorking ? 2.8 : 1.45) + 1.1) * 0.18;
      if (overrideBeat === "reading-left") readingScan = -0.82;
      else if (overrideBeat === "reading-right") readingScan = 0.82;

      const screenYaw = readingScan * 8.6 * DEG + gx * 3.5 * DEG;
      const screenPitch = (5.6 + workingWOffset(workingWeight)) * DEG + gy * 1.8 * DEG;
      const glanceYaw = gx * 4.5 * DEG;
      const glancePitch = -1.8 * DEG + gy * 2.2 * DEG;
      const reactionYaw = gx * 11 * DEG;
      const reactionPitch = gy * 7 * DEG;

      let wantHeadYaw = screenYaw * (1 - glanceWeight) + glanceYaw * glanceWeight;
      let wantHeadPitch = screenPitch * (1 - glanceWeight) + glancePitch * glanceWeight;
      if (reactionWeight > 0.01) {
        wantHeadYaw = wantHeadYaw * (1 - reactionWeight) + reactionYaw * reactionWeight;
        wantHeadPitch = wantHeadPitch * (1 - reactionWeight) + reactionPitch * reactionWeight;
      }
      if (sleepyWeight > 0.01) {
        wantHeadYaw *= (1 - sleepyWeight * 0.82);
        wantHeadPitch *= (1 - sleepyWeight * 0.85);
      }

      // Eye LookAt yaw/pitch (in radians): looks down-scan at screen while typing,
      // locks warmly onto the camera during glance-up!
      const eyeYawScreen = readingScan * 13.0 * DEG + gx * 6 * DEG;
      const eyePitchScreen = -9.5 * DEG;
      const eyeYawGlance = gx * 8 * DEG;
      const eyePitchGlance = 1.5 * DEG - gy * 4 * DEG;
      const eyeYawReaction = gx * 22 * DEG;
      const eyePitchReaction = -gy * 14 * DEG;

      let eyeYaw = eyeYawScreen * (1 - glanceWeight) + eyeYawGlance * glanceWeight;
      let eyePitch = eyePitchScreen * (1 - glanceWeight) + eyePitchGlance * glanceWeight;
      if (reactionWeight > 0.01) {
        eyeYaw = eyeYaw * (1 - reactionWeight) + eyeYawReaction * reactionWeight;
        eyePitch = eyePitch * (1 - reactionWeight) + eyePitchReaction * reactionWeight;
      }
      if (sleepyWeight > 0.01) {
        eyeYaw *= (1 - sleepyWeight * 0.85);
        eyePitch = eyePitch * (1 - sleepyWeight) + (-12 * DEG) * sleepyWeight;
      }

      // Subtle soft screen bounce light modulation (flattering luminous focus when working,
      // calm ambient on idle, softly dimmed on sleepy — calibrated for MToon skin).
      const keyFlicker = (Math.sin(elapsed * 9.3) * 0.5 + Math.sin(elapsed * 15.7 + 1.2) * 0.5) * 0.016 * typingWeight;
      let targetGlow = 0.13 + keyFlicker * 0.5;
      if (wantSleepy) targetGlow = 0.03;
      else if (wantWorking) targetGlow = 0.24 + keyFlicker;
      else if (wantGlance) targetGlow = 0.12;
      else if (wantReaction) targetGlow = 0.11;

      screenGlow = damp(screenGlow, targetGlow, 5.5, dt);
      if (screenLight) screenLight.intensity = screenGlow;
      if (emblemMesh?.material) {
        const emblemTarget = wantSleepy ? 0.26 : (wantWorking ? 0.96 : 0.78);
        emblemMesh.material.opacity = damp(emblemMesh.material.opacity ?? emblemTarget, emblemTarget, 5.0, dt);
      }

      // Cute expression overlay:
      //  - Glance-up: warm adorable smile (happy ~ 0.42, relaxed ~ 0.14) keeping eyes sparkling open.
      //  - Idle typing/reading: subtle warm content micro-smile (happy ~ 0.14, relaxed ~ 0.08).
      //  - Working: gentle focused softness (subtle happy ~ 0.08, soft blink alive).
      //  - Sleepy: cozy doze (handled cleanly when overridden to sleepy too).
      let exprOverlay = null;
      if (overrideBeat === "sleepy") {
        exprOverlay = { mode: "sleepy", blink: 0.92, relaxed: 0.44, happy: 0.0 };
      } else if (activity === "idle" || activity === "working") {
        const baseHappy = activity === "working" ? 0.08 : 0.15;
        const baseRelaxed = activity === "working" ? 0.06 : 0.08;
        exprOverlay = {
          mode: wantGlance ? "glance-up" : activity,
          happy: baseHappy * (1 - glanceWeight) + 0.42 * glanceWeight,
          relaxed: baseRelaxed * (1 - glanceWeight) + 0.14 * glanceWeight,
          allowBlink: true
        };
      }

      const boneTargets = computeCoLeadTargets({
        elapsed,
        typingWeight,
        glanceWeight,
        sleepyWeight,
        reactionWeight,
        workingWeight,
        tiltDir: glanceTiltDir,
        headYaw: wantHeadYaw,
        headPitch: wantHeadPitch,
        headPitchCue: ctx.headPitchCue || 0
      });

      const activeBeat = overrideBeat !== "auto"
        ? overrideBeat
        : wantSleepy ? "sleepy"
          : wantReaction ? "reaction"
            : wantGlance ? "glance-up"
              : wantWorking ? (wantTyping ? "working" : "working-read")
                : (wantTyping ? "typing" : "reading");

      return {
        beat: activeBeat,
        activity,
        boneTargets,
        eyeYaw,
        eyePitch,
        exprOverlay,
        screenGlow,
        typing: typingWeight > 0.35,
        glanceUp: glanceWeight > 0.35,
        hover: hovered
      };
    }

    function workingWOffset(w) {
      return w * 1.1;
    }

    function setLaptopAccent(hex) {
      const norm = normalizeHexColor(hex);
      if (!norm) return accentHex;
      accentHex = norm;
      applyAccentVisuals();
      return accentHex;
    }

    function setCoLeadBeat(beat) {
      if (beat === null || beat === undefined || beat === "") {
        overrideBeat = "auto";
        return overrideBeat;
      }
      const key = String(beat).trim().toLowerCase();
      if (!VALID_BEATS.has(key)) return overrideBeat;
      overrideBeat = key;
      if (key === "glance-up") {
        glanceTiltDir = 1;
        glanceWeight = 1;
        typingWeight = 0;
        sleepyWeight = 0;
        workingWeight = 0;
      } else if (key === "typing") {
        typingWeight = 1;
        glanceWeight = 0;
        sleepyWeight = 0;
        workingWeight = 0;
      } else if (key === "working") {
        typingWeight = 1;
        workingWeight = 1;
        glanceWeight = 0;
        sleepyWeight = 0;
      } else if (key === "sleepy") {
        sleepyWeight = 1;
        typingWeight = 0;
        glanceWeight = 0;
        workingWeight = 0;
      } else if (key === "reading-left" || key === "reading-right") {
        typingWeight = 0.85;
        glanceWeight = 0;
        sleepyWeight = 0;
      }
      return overrideBeat;
    }

    function setHover(flag) {
      explicitHover = flag === null || flag === undefined ? null : !!flag;
    }

    function debugState() {
      return {
        beat: overrideBeat !== "auto" ? overrideBeat : phase,
        overrideBeat,
        activity: lastActivity,
        typing: typingWeight > 0.35,
        typingWeight: Number(typingWeight.toFixed(3)),
        glanceUp: glanceWeight > 0.35,
        glanceWeight: Number(glanceWeight.toFixed(3)),
        sleepyWeight: Number(sleepyWeight.toFixed(3)),
        workingWeight: Number(workingWeight.toFixed(3)),
        hover: explicitHover === true,
        laptopAccent: accentHex,
        screenGlow: Number(screenGlow.toFixed(3)),
        hasLaptopProp: !!laptopGroup,
        laptopTopViewportPct: Number(lidTopViewportPct.toFixed(2)),
        chinViewportPct: Number(chinViewportPct.toFixed(2)),
        headViewportPct: Number(headViewportPct.toFixed(2))
      };
    }

    function dispose() {
      if (laptopGroup && scene) scene.remove(laptopGroup);
      if (screenLight && scene) scene.remove(screenLight);
      for (const g of geometries) { try { g.dispose?.(); } catch (_) { /* ignore */ } }
      for (const m of materials) { try { m.dispose?.(); } catch (_) { /* ignore */ } }
      for (const tx of textures) { try { tx.dispose?.(); } catch (_) { /* ignore */ } }
      geometries.length = 0;
      materials.length = 0;
      textures.length = 0;
      laptopGroup = lidMesh = rimMesh = emblemMesh = screenLight = lidTexture = emblemTexture = null;
      lastFraming = null;
    }

    return {
      layoutProp,
      step,
      setLaptopAccent,
      setCoLeadBeat,
      setHover,
      debugState,
      dispose
    };
  }

  Object.assign(C, {
    createVrmCoLead,
    vrmCoLeadMath: {
      computeCoLeadTargets,
      classifyActivity,
      mirrorLR,
      normalizeHexColor,
      dampAlpha
    }
  });
})(window.WsCompanion);
