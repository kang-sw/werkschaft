(function (C) {
  "use strict";

  // ============================================================================
  // Co-Lead 3D Prop Builders, Procedural Textures, Framing & Hand-Sync Helpers
  // ============================================================================
  // Owns the beveled foreground laptop lid, minimalist desk/held smartphone prop,
  // per-session accent palette visuals, upward screen bounce light, and prop AABB
  // accessors consumed by the 3-mode dynamic scissor overlay.

  const DEG = Math.PI / 180;
  const DEFAULT_ACCENT = "#7aa2ff";
  const LID_TILT_RAD = 11 * DEG;
  // Palm grip tuning: phone center slides GRIP_ALONG x palm length toward the
  // fingers, lifts off the palm by half the hand depth (relative to palm width)
  // plus the phone back depth (local units x lid scale), and rolls GRIP_ROLL.
  const GRIP_ALONG = 0.15;
  const GRIP_HAND_HALF_DEPTH = 0.2;
  const PHONE_BACK_DEPTH = 0.016;
  const GRIP_ROLL = 0;

  // Curated 6-color dark-UI-friendly session laptop palette.
  const SESSION_ACCENT_PALETTE = Object.freeze([
    Object.freeze({ id: "sapphire",  hex: "#5b9eff", rim: "#a5cbff" }),
    Object.freeze({ id: "lavender",  hex: "#a78bfa", rim: "#d2c3ff" }),
    Object.freeze({ id: "mint-teal", hex: "#34d399", rim: "#96f0ce" }),
    Object.freeze({ id: "amber",     hex: "#f59e0b", rim: "#fcd37a" }),
    Object.freeze({ id: "coral",     hex: "#fb7185", rim: "#ffb3bf" }),
    Object.freeze({ id: "ice-cyan",  hex: "#38bdf8", rim: "#9be3ff" })
  ]);

  function clamp(v, lo, hi) {
    return v < lo ? lo : (v > hi ? hi : v);
  }

  function lerp(a, b, t) {
    return a + (b - a) * t;
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

  // Deterministic FNV-1a 32-bit hash mapping each distinct session ID to one of
  // the 6 curated laptop accents (sequential IDs spread cleanly across all 6).
  function sessionAccentForKey(sessionKey) {
    const raw = typeof sessionKey === "string" ? sessionKey.trim() : "";
    if (!raw) return SESSION_ACCENT_PALETTE[0];
    let h = 2166136261 >>> 0;
    for (let i = 0; i < raw.length; i++) {
      h ^= raw.charCodeAt(i);
      h = Math.imul(h, 16777619) >>> 0;
    }
    return SESSION_ACCENT_PALETTE[(h >>> 0) % SESSION_ACCENT_PALETTE.length];
  }

  function findPaletteEntryByHex(hex) {
    const norm = normalizeHexColor(hex);
    if (!norm) return null;
    for (const entry of SESSION_ACCENT_PALETTE) {
      if (entry.hex === norm) return entry;
    }
    return null;
  }

  // Procedural prop textures are painted by dashboard-companion-vrm-studio.js
  // (loaded first) and re-exported here for backwards-compatible callers.
  function studioPainter(name) {
    return THREE => (typeof C.vrmStudio?.[name] === "function" ? C.vrmStudio[name](THREE) : null);
  }
  const makeLidTexture = studioPainter("makeLidTexture");
  const makeEmblemGlowTexture = studioPainter("makeEmblemGlowTexture");
  const makePhoneScreenTexture = studioPainter("makePhoneScreenTexture");
  const makePhoneBackTexture = studioPainter("makePhoneBackTexture");
  const makeContactShadowTexture = studioPainter("makeContactShadowTexture");

  // Build a smooth rounded-rectangle THREE.Shape centered at (0, 0).
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

  function createCoLeadProps(opts = {}) {
    const THREE = opts.THREE || null;
    const scene = opts.scene || null;
    const camera = opts.camera || null;

    let laptopGroup = null;
    let lidMesh = null;
    let rimMesh = null;
    let emblemMesh = null;
    let phoneGroup = null;
    let phoneBodyMesh = null;
    let phoneScreenMesh = null;
    let phoneBackMesh = null;
    let phoneCameraBumpMesh = null;
    let phoneBuzzGlowMesh = null;
    let screenLight = null;
    let lidTexture = null;
    let emblemTexture = null;
    let phoneScreenTexture = null;
    let phoneBackTexture = null;
    const geometries = [];
    const materials = [];
    const textures = [];

    let lastFraming = null;
    let basePos = { x: 0, y: 0, z: 0 };
    let baseScale = 0.4;
    let baseVisH = 0.85;
    let baseVisHalfW = 0.45;
    let phoneBasePos = { x: 0.25, y: 0, z: 0 };
    const liveLaptopOffset = { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 };
    let livePhonePose = { x: 0.25, y: 0, z: 0, rx: -48 * DEG, ry: -12 * DEG, rz: -6 * DEG };
    let phonePoseInitialized = false;
    let lidTopWorldY = 0;
    let lidTopWorldZ = 0;
    let lidTopViewportPct = 0;
    let chinViewportPct = 0;
    let headViewportPct = 0;
    let screenGlow = 0.13;
    let phoneScreenGlow = 0.14;
    let bounceColorRgb = hexToRgb(DEFAULT_ACCENT);
    let hingeAccentMesh = null;
    let phoneShadowMesh = null;
    let shadowTexture = null;
    const calibration = {
      laptop: { offset: [0, 0, 0], rotation_deg: [0, 0, 0], scale: 1 },
      smartphone: { offset: [0, 0, 0], rotation_deg: [0, 0, 0], scale: 1 }
    };
    const scratchVec = THREE && typeof THREE.Vector3 === "function" ? new THREE.Vector3() : null;
    // Preallocated palm-grip scratch (zero allocation per frame).
    const hasQuat = !!(THREE && typeof THREE.Quaternion === "function" && typeof THREE.Matrix4 === "function" && typeof THREE.Vector3 === "function");
    const palmFrame = { palmX: { x: 0, y: 0, z: 0 }, palmY: { x: 0, y: 0, z: 0 }, palmZ: { x: 0, y: 0, z: 0 }, center: { x: 0, y: 0, z: 0 } };
    const gripBasis = hasQuat ? new THREE.Matrix4() : null;
    const gripQuat = hasQuat ? new THREE.Quaternion() : null;
    const gripRoll = hasQuat ? new THREE.Quaternion() : null;
    const gripAxisX = hasQuat ? new THREE.Vector3() : null;
    const gripAxisY = hasQuat ? new THREE.Vector3() : null;
    const gripAxisZ = hasQuat ? new THREE.Vector3() : null;
    const palmGrip = { mode: "none", side: "left", weight: 0, screenFacing: 0, normal: { x: 0, y: 0, z: 0 }, center: { x: 0, y: 0, z: 0 }, phoneUpDot: 0 };

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

    function applyAccentVisuals(accentHex, immediateBounce = true) {
      const rgb = hexToRgb(accentHex);
      if (emblemMesh?.material?.color?.setRGB) {
        emblemMesh.material.color.setRGB(rgb[0] / 255, rgb[1] / 255, rgb[2] / 255);
      }
      if (rimMesh?.material?.color?.setHex) {
        rimMesh.material.color.setHex(mixRgbToHex([192, 206, 228], rgb, 0.58));
      }
      hingeAccentMesh?.material?.color?.setHex?.(mixRgbToHex([200, 214, 236], rgb, 0.82));
      if (lidMesh?.material?.emissive?.setHex) {
        lidMesh.material.emissive.setHex(mixRgbToHex([11, 14, 20], rgb, 0.13));
      }
      if (immediateBounce) {
        bounceColorRgb = rgb;
      }
      if (screenLight?.color?.setHex) {
        const bounceHex = mixRgbToHex([224, 236, 255], bounceColorRgb, 0.26);
        screenLight.color.setHex(bounceHex);
      }
    }

    function normalizeCalEntry(raw) {
      const vec3 = (arr, lo, hi) => Array.isArray(arr) && arr.length >= 3
        ? [clamp(Number(arr[0]) || 0, lo, hi), clamp(Number(arr[1]) || 0, lo, hi), clamp(Number(arr[2]) || 0, lo, hi)]
        : [0, 0, 0];
      return {
        offset: vec3(raw?.offset, -2, 2),
        rotation_deg: vec3(raw?.rotation_deg, -180, 180),
        scale: Number.isFinite(raw?.scale) ? clamp(raw.scale, 0.25, 4.0) : 1
      };
    }

    function setCalibration(cal) {
      const propsCal = cal?.props && typeof cal.props === "object" ? cal.props : (cal || {});
      calibration.laptop = normalizeCalEntry(propsCal.laptop);
      calibration.smartphone = normalizeCalEntry(propsCal.smartphone);
      if (laptopGroup) {
        const ls = baseScale * calibration.laptop.scale;
        laptopGroup.scale.set(ls, ls, ls);
        applyLaptopTransform();
      }
      if (phoneGroup) {
        const ps = baseScale * calibration.smartphone.scale;
        phoneGroup.scale.set(ps, ps, ps);
        applyPhoneTransform();
      }
    }

    function getPropCalibration() {
      return {
        laptop: { offset: calibration.laptop.offset.slice(), rotation_deg: calibration.laptop.rotation_deg.slice(), scale: calibration.laptop.scale },
        smartphone: { offset: calibration.smartphone.offset.slice(), rotation_deg: calibration.smartphone.rotation_deg.slice(), scale: calibration.smartphone.scale }
      };
    }

    function getPropAnchorWorld(threeArg, propId, anchor, outVec) {
      const T = threeArg || THREE;
      const group = propId === "laptop" ? laptopGroup : (propId === "smartphone" ? phoneGroup : null);
      if (!group || !T || typeof T.Vector3 !== "function") return null;
      const v = outVec || new T.Vector3();
      if (propId === "smartphone") {
        if (anchor === "screen") v.set(0, 0, 0.016);
        else v.set(0, 0, 0);
      } else {
        if (anchor === "keyboard") v.set(0, -0.32, -0.22);
        else v.set(0, 0, -0.02);
      }
      group.updateMatrixWorld?.(true);
      if (typeof group.localToWorld === "function") return group.localToWorld(v);
      return v.set(group.position.x + v.x * baseScale, group.position.y + v.y * baseScale, group.position.z + v.z * baseScale);
    }

    function applyLaptopTransform() {
      if (!laptopGroup) return;
      const cal = calibration.laptop;
      laptopGroup.position.set(
        basePos.x + liveLaptopOffset.dx + cal.offset[0],
        basePos.y + liveLaptopOffset.dy + cal.offset[1],
        basePos.z + liveLaptopOffset.dz + cal.offset[2]
      );
      laptopGroup.rotation.set(
        LID_TILT_RAD + liveLaptopOffset.rx + cal.rotation_deg[0] * DEG,
        liveLaptopOffset.ry + cal.rotation_deg[1] * DEG,
        liveLaptopOffset.rz + cal.rotation_deg[2] * DEG,
        "XYZ"
      );
    }

    function applyPhoneTransform() {
      if (!phoneGroup) return;
      const cal = calibration.smartphone;
      phoneGroup.position.set(
        livePhonePose.x + cal.offset[0],
        livePhonePose.y + cal.offset[1],
        livePhonePose.z + cal.offset[2]
      );
      phoneGroup.rotation.set(
        livePhonePose.rx + cal.rotation_deg[0] * DEG,
        livePhonePose.ry + cal.rotation_deg[1] * DEG,
        livePhonePose.rz + cal.rotation_deg[2] * DEG,
        "XYZ"
      );
    }

    function buildPropIfNeeded(accentHex) {
      if (laptopGroup || !THREE || !scene) return;
      if (typeof THREE.PointLight === "function") {
        screenLight = new THREE.PointLight(0xdceaff, screenGlow, 1.85, 0.65);
        scene.add(screenLight);
      }
      if (!canBuild3DProp()) {
        applyAccentVisuals(accentHex);
        return;
      }

      laptopGroup = new THREE.Group();
      laptopGroup.name = "ws-colead-laptop";

      const lidShape = makeRoundedRectShape(THREE, 1.0, 0.62, 0.048);
      const lidGeo = new THREE.ExtrudeGeometry(lidShape, {
        depth: 0.012,
        bevelEnabled: true,
        bevelThickness: 0.007,
        bevelSize: 0.006,
        bevelSegments: 4,
        curveSegments: 10
      });
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

      if (typeof THREE.PlaneGeometry === "function" && typeof THREE.MeshBasicMaterial === "function") {
        const rimGeo = new THREE.PlaneGeometry(0.92, 0.013);
        const rimMat = new THREE.MeshBasicMaterial({
          color: 0xb6c8e8,
          transparent: true,
          opacity: 0.68,
          depthWrite: false
        });
        geometries.push(rimGeo);
        materials.push(rimMat);
        rimMesh = new THREE.Mesh(rimGeo, rimMat);
        rimMesh.position.set(0, 0.311, 0.0115);
        rimMesh.frustumCulled = false;
        laptopGroup.add(rimMesh);

        emblemTexture = makeEmblemGlowTexture(THREE);
        if (emblemTexture) textures.push(emblemTexture);
        const emblemGeo = new THREE.PlaneGeometry(0.168, 0.072);
        const emblemMat = new THREE.MeshBasicMaterial({
          color: 0x7aa2ff,
          map: emblemTexture || null,
          transparent: true,
          opacity: 0.96,
          depthWrite: false
        });
        geometries.push(emblemGeo);
        materials.push(emblemMat);
        emblemMesh = new THREE.Mesh(emblemGeo, emblemMat);
        emblemMesh.position.set(0, 0.266, 0.0145);
        emblemMesh.frustumCulled = false;
        laptopGroup.add(emblemMesh);

        // Thin accent hinge light strip just above the barrel.
        const accentGeo = new THREE.PlaneGeometry(0.30, 0.008);
        const accentMat = new THREE.MeshBasicMaterial({ color: 0x7aa2ff, transparent: true, opacity: 0.82, depthWrite: false });
        geometries.push(accentGeo);
        materials.push(accentMat);
        hingeAccentMesh = new THREE.Mesh(accentGeo, accentMat);
        hingeAccentMesh.name = "ws-colead-hinge-accent";
        hingeAccentMesh.position.set(0, -0.292, 0.0142);
        hingeAccentMesh.frustumCulled = false;
        laptopGroup.add(hingeAccentMesh);
      }

      // Diamond-cut chamfer frame catching the prop-only studio env highlights.
      if (typeof THREE.ShapeGeometry === "function" && typeof THREE.Path === "function") {
        const outer = makeRoundedRectShape(THREE, 0.998, 0.618, 0.047);
        const inner = makeRoundedRectShape(THREE, 0.978, 0.598, 0.040);
        outer.holes.push(new THREE.Path(inner.getPoints(10).reverse()));
        const chamferGeo = new THREE.ShapeGeometry(outer, 10);
        const chamferMat = new THREE.MeshStandardMaterial({ color: 0xaeb9cc, roughness: 0.24, metalness: 0.82 });
        geometries.push(chamferGeo);
        materials.push(chamferMat);
        const chamfer = new THREE.Mesh(chamferGeo, chamferMat);
        chamfer.name = "ws-colead-lid-chamfer";
        chamfer.position.set(0, 0, 0.0132);
        chamfer.frustumCulled = false;
        laptopGroup.add(chamfer);
      }

      // Brushed metal hinge barrel along the lid's bottom edge.
      if (typeof THREE.CylinderGeometry === "function") {
        const hingeGeo = new THREE.CylinderGeometry(0.017, 0.017, 0.84, 20, 1);
        const hingeMat = new THREE.MeshStandardMaterial({ color: 0x3a4152, roughness: 0.3, metalness: 0.78 });
        geometries.push(hingeGeo);
        materials.push(hingeMat);
        const hinge = new THREE.Mesh(hingeGeo, hingeMat);
        hinge.name = "ws-colead-hinge";
        hinge.rotation.set(0, 0, Math.PI * 0.5);
        hinge.position.set(0, -0.318, -0.002);
        hinge.frustumCulled = false;
        laptopGroup.add(hinge);
      }

      laptopGroup.rotation.set(LID_TILT_RAD, 0, 0, "XYZ");
      scene.add(laptopGroup);

      // Minimalist 3D desk smartphone prop ("ws-colead-phone").
      phoneGroup = new THREE.Group();
      phoneGroup.name = "ws-colead-phone";
      const phoneW = 0.21;
      const phoneH = 0.39;
      const phoneD = 0.018;
      const phoneShape = makeRoundedRectShape(THREE, phoneW, phoneH, 0.032);
      const phoneGeo = new THREE.ExtrudeGeometry(phoneShape, {
        depth: phoneD,
        bevelEnabled: true,
        bevelThickness: 0.005,
        bevelSize: 0.004,
        bevelSegments: 3,
        curveSegments: 8
      });
      phoneGeo.translate(0, 0, -phoneD * 0.5);
      geometries.push(phoneGeo);

      const phoneBodyMat = new THREE.MeshStandardMaterial({
        color: 0x303645,
        roughness: 0.32,
        metalness: 0.52,
        emissive: 0x0b0e14,
        emissiveIntensity: 0.22
      });
      materials.push(phoneBodyMat);
      phoneBodyMesh = new THREE.Mesh(phoneGeo, phoneBodyMat);
      phoneBodyMesh.frustumCulled = false;
      phoneGroup.add(phoneBodyMesh);

      if (typeof THREE.PlaneGeometry === "function") {
        phoneScreenTexture = makePhoneScreenTexture(THREE);
        if (phoneScreenTexture) textures.push(phoneScreenTexture);
        const screenGeo = new THREE.PlaneGeometry(phoneW * 0.87, phoneH * 0.90);
        const screenMat = new THREE.MeshStandardMaterial({
          color: phoneScreenTexture ? 0xffffff : 0x3b82f6,
          map: phoneScreenTexture || null,
          roughness: 0.16,
          metalness: 0.08,
          emissive: 0x4f86ff,
          emissiveIntensity: 0.18
        });
        geometries.push(screenGeo);
        materials.push(screenMat);
        phoneScreenMesh = new THREE.Mesh(screenGeo, screenMat);
        phoneScreenMesh.name = "ws-colead-phone-screen";
        phoneScreenMesh.position.set(0, 0, phoneD * 0.5 + 0.0062);
        phoneScreenMesh.frustumCulled = false;
        phoneGroup.add(phoneScreenMesh);

        if (typeof THREE.MeshBasicMaterial === "function") {
          const glowGeo = new THREE.PlaneGeometry(phoneW * 1.24, phoneH * 1.18);
          const glowMat = new THREE.MeshBasicMaterial({
            color: 0x60a5fa,
            transparent: true,
            opacity: 0.0,
            depthWrite: false
          });
          geometries.push(glowGeo);
          materials.push(glowMat);
          phoneBuzzGlowMesh = new THREE.Mesh(glowGeo, glowMat);
          phoneBuzzGlowMesh.position.set(0, 0, phoneD * 0.5 + 0.004);
          phoneBuzzGlowMesh.frustumCulled = false;
          phoneGroup.add(phoneBuzzGlowMesh);
        }

        phoneBackTexture = makePhoneBackTexture(THREE);
        if (phoneBackTexture) textures.push(phoneBackTexture);
        const backGeo = new THREE.PlaneGeometry(phoneW * 0.92, phoneH * 0.94);
        const backMat = new THREE.MeshStandardMaterial({
          color: phoneBackTexture ? 0xffffff : 0xaab6cc,
          map: phoneBackTexture || null,
          roughness: 0.36,
          metalness: 0.42,
          emissive: 0x1c2333,
          emissiveIntensity: 0.25
        });
        geometries.push(backGeo);
        materials.push(backMat);
        phoneBackMesh = new THREE.Mesh(backGeo, backMat);
        phoneBackMesh.name = "ws-colead-phone-back";
        phoneBackMesh.position.set(0, 0, -(phoneD * 0.5 + 0.0062));
        phoneBackMesh.rotation.set(0, Math.PI, 0);
        phoneBackMesh.frustumCulled = false;
        phoneGroup.add(phoneBackMesh);
      }

      const camIslandShape = makeRoundedRectShape(THREE, 0.088, 0.118, 0.018);
      const camIslandGeo = new THREE.ExtrudeGeometry(camIslandShape, {
        depth: 0.012,
        bevelEnabled: true,
        bevelThickness: 0.003,
        bevelSize: 0.002,
        bevelSegments: 2,
        curveSegments: 6
      });
      const camIslandMat = new THREE.MeshStandardMaterial({
        color: 0x181c24,
        roughness: 0.22,
        metalness: 0.65,
        emissive: 0x0a0d14,
        emissiveIntensity: 0.2
      });
      geometries.push(camIslandGeo);
      materials.push(camIslandMat);
      phoneCameraBumpMesh = new THREE.Mesh(camIslandGeo, camIslandMat);
      phoneCameraBumpMesh.name = "ws-colead-phone-camera";
      phoneCameraBumpMesh.position.set(0.036, 0.098, -(phoneD * 0.5 + 0.017));
      phoneCameraBumpMesh.frustumCulled = false;
      phoneGroup.add(phoneCameraBumpMesh);

      if (typeof THREE.CylinderGeometry === "function") {
        const lensGeo = new THREE.CylinderGeometry(0.022, 0.022, 0.008, 16);
        const lensMat = new THREE.MeshStandardMaterial({
          color: 0x67e8f9,
          roughness: 0.12,
          metalness: 0.85,
          emissive: 0x1e40af,
          emissiveIntensity: 0.55
        });
        const flashGeo = new THREE.CylinderGeometry(0.009, 0.009, 0.006, 12);
        const flashMat = new THREE.MeshStandardMaterial({
          color: 0xfef08a,
          roughness: 0.2,
          metalness: 0.1,
          emissive: 0xf59e0b,
          emissiveIntensity: 0.45
        });
        geometries.push(lensGeo, flashGeo);
        materials.push(lensMat, flashMat);

        // Polished lens rims + dark sapphire glass discs (back faces -Z).
        const hasRim = typeof THREE.TorusGeometry === "function" && typeof THREE.CircleGeometry === "function";
        const rimGeo = hasRim ? new THREE.TorusGeometry(0.0235, 0.0034, 8, 24) : null;
        const rimMat = hasRim ? new THREE.MeshStandardMaterial({ color: 0xd6dde9, roughness: 0.18, metalness: 0.92 }) : null;
        const glassGeo = hasRim ? new THREE.CircleGeometry(0.0145, 20) : null;
        const glassMat = hasRim ? new THREE.MeshStandardMaterial({ color: 0x0b1020, roughness: 0.06, metalness: 0.6, emissive: 0x10204a, emissiveIntensity: 0.4 }) : null;
        if (hasRim) {
          geometries.push(rimGeo, glassGeo);
          materials.push(rimMat, glassMat);
        }
        for (const ly of [0.126, 0.072]) {
          const lens = new THREE.Mesh(lensGeo, lensMat);
          lens.rotation.set(Math.PI * 0.5, 0, 0);
          lens.position.set(0.052, ly, -(phoneD * 0.5 + 0.021));
          lens.frustumCulled = false;
          phoneGroup.add(lens);
          if (hasRim) {
            const ring = new THREE.Mesh(rimGeo, rimMat);
            ring.position.set(0.052, ly, -(phoneD * 0.5 + 0.0252));
            ring.frustumCulled = false;
            const glass = new THREE.Mesh(glassGeo, glassMat);
            glass.rotation.set(0, Math.PI, 0);
            glass.position.set(0.052, ly, -(phoneD * 0.5 + 0.0253));
            glass.frustumCulled = false;
            phoneGroup.add(ring, glass);
          }
        }
        const flash = new THREE.Mesh(flashGeo, flashMat);
        flash.rotation.set(Math.PI * 0.5, 0, 0);
        flash.position.set(0.014, 0.099, -(phoneD * 0.5 + 0.020));
        flash.frustumCulled = false;
        phoneGroup.add(flash);
      }

      // Soft contact shadow grounding the desk phone (fades when held or flipped).
      shadowTexture = makeContactShadowTexture(THREE);
      if (shadowTexture && typeof THREE.PlaneGeometry === "function" && typeof THREE.MeshBasicMaterial === "function") {
        textures.push(shadowTexture);
        const shadowGeo = new THREE.PlaneGeometry(phoneW * 1.55, phoneH * 1.22);
        const shadowMat = new THREE.MeshBasicMaterial({ color: 0x000000, map: shadowTexture, transparent: true, opacity: 0.3, depthWrite: false });
        shadowMat.userData = { studioOpacity: 0.3 };
        geometries.push(shadowGeo);
        materials.push(shadowMat);
        phoneShadowMesh = new THREE.Mesh(shadowGeo, shadowMat);
        phoneShadowMesh.name = "ws-colead-contact-shadow";
        phoneShadowMesh.userData.contactShadow = true;
        phoneShadowMesh.position.set(0.012, -0.024, -(phoneD * 0.5 + 0.034));
        phoneShadowMesh.renderOrder = -1;
        phoneShadowMesh.frustumCulled = false;
        phoneGroup.add(phoneShadowMesh);
      }

      scene.add(phoneGroup);
      applyAccentVisuals(accentHex);
    }

    // Held phone grip (post-vrm.update). Given a palm probe (wrist + index/middle/
    // little proximal world positions) the phone sits flush in the palm: screen
    // normal = palm normal (palmZ), long axis along palmY, centered on the palm and
    // lifted by half the hand + phone thickness, so VRM 0.x and 1.0 hold it
    // identically. Rigs without finger bones (or a bare Vector3 input) keep the
    // legacy position-only knuckle anchor and the authored Euler orientation.
    function syncPhoneToHand(input, phoneHoldWeight) {
      const w = clamp(Number.isFinite(phoneHoldWeight) ? phoneHoldWeight : 0, 0, 1);
      palmGrip.weight = w;
      if (!phoneGroup || !input || w <= 0.01) { palmGrip.mode = "none"; return; }
      const probe = input.wrist ? input : null;
      const holdBlend = easeInOutCubic(w);
      const computePalm = C.vrmCoLeadPose?.computePalmFrame;
      if (probe?.hasFingers && hasQuat && typeof computePalm === "function" &&
          computePalm(probe.wrist, probe.index, probe.middle, probe.little, probe.side || "left", palmFrame)) {
        const px = palmFrame.palmX, py = palmFrame.palmY, pz = palmFrame.palmZ, pc = palmFrame.center;
        gripAxisX.set(px.x, px.y, px.z);
        gripAxisY.set(py.x, py.y, py.z);
        gripAxisZ.set(pz.x, pz.y, pz.z);
        gripBasis.makeBasis(gripAxisX, gripAxisY, gripAxisZ);
        gripQuat.setFromRotationMatrix(gripBasis);
        if (GRIP_ROLL !== 0) {
          gripAxisZ.set(0, 0, 1);
          gripRoll.setFromAxisAngle(gripAxisZ, GRIP_ROLL);
          gripQuat.multiply(gripRoll);
        }
        const along = palmFrame.length * GRIP_ALONG;
        const lift = palmFrame.width * GRIP_HAND_HALF_DEPTH + PHONE_BACK_DEPTH * baseScale;
        const cx = pc.x + py.x * along + pz.x * lift;
        const cy = pc.y + py.y * along + pz.y * lift;
        const cz = pc.z + py.z * along + pz.z * lift;
        phoneGroup.position.set(
          lerp(livePhonePose.x, cx, holdBlend),
          lerp(livePhonePose.y, cy, holdBlend),
          lerp(livePhonePose.z, cz, holdBlend)
        );
        phoneGroup.quaternion.slerp(gripQuat, holdBlend);
        palmGrip.mode = "palm";
        palmGrip.side = palmFrame.side;
        palmGrip.normal.x = pz.x; palmGrip.normal.y = pz.y; palmGrip.normal.z = pz.z;
        palmGrip.center.x = cx; palmGrip.center.y = cy; palmGrip.center.z = cz;
        palmGrip.phoneUpDot = py.y;
        if (probe.hasHead) {
          const hx = probe.head.x - cx, hy = probe.head.y - cy, hz = probe.head.z - cz;
          const hl = Math.hypot(hx, hy, hz) || 1;
          palmGrip.screenFacing = (pz.x * hx + pz.y * hy + pz.z * hz) / hl;
        }
        return;
      }
      const handWorldPos = probe ? (probe.anchor || probe.wrist) : input;
      if (!Number.isFinite(handWorldPos.x) || !Number.isFinite(handWorldPos.y) || !Number.isFinite(handWorldPos.z)) return;
      palmGrip.mode = "anchor";
      palmGrip.screenFacing = 0;
      const anchorX = handWorldPos.x + baseScale * 0.012;
      const anchorY = Math.max(handWorldPos.y + baseScale * 0.145, lidTopWorldY + baseScale * 0.13);
      const anchorZ = Math.max(handWorldPos.z + baseScale * 0.025, basePos.z - baseScale * 0.24);
      phoneGroup.position.set(
        lerp(livePhonePose.x, anchorX, holdBlend),
        lerp(livePhonePose.y, anchorY, holdBlend),
        lerp(livePhonePose.z, anchorZ, holdBlend)
      );
    }

    // Position the sleek laptop lid strictly in the bottom ~16-18% of the bust slot,
    // always clearly below chin, neck, and upper chest on both VRM 0.x and VRM 1.0.
    function layoutProp(framing, camOverride, state = {}) {
      lastFraming = framing || lastFraming;
      const cam = camOverride || camera;
      if (!lastFraming || !cam) return;
      buildPropIfNeeded(state.accentHex || DEFAULT_ACCENT);

      const headY = Number.isFinite(lastFraming.headY) ? lastFraming.headY : 1.45;
      const chestY = Number.isFinite(lastFraming.chestY) ? lastFraming.chestY : 1.18;
      const bustSpan = Math.max(0.18, Number.isFinite(lastFraming.bustSpan) ? lastFraming.bustSpan : (headY - chestY));
      const targetY = Number.isFinite(lastFraming.targetY) ? lastFraming.targetY : 1.38;
      const headZ = Number.isFinite(lastFraming.headZ) ? lastFraming.headZ : 0;
      const centerX = Number.isFinite(lastFraming.centerX) ? lastFraming.centerX : 0;
      const camZ = Number.isFinite(cam.position?.z) ? cam.position.z : (lastFraming.cameraZ || 1.85);
      const camY = Number.isFinite(cam.position?.y) ? cam.position.y : targetY;
      const camDist = Math.max(0.35, camZ - headZ);

      const laptopZ = headZ + clamp(bustSpan * 1.56, 0.38, Math.min(0.58, camDist * 0.42));
      const dToCam = Math.max(0.22, camZ - laptopZ);
      const fovRad = ((cam.fov || 26) * DEG);
      const aspect = Math.max(0.5, cam.aspect || 1.4);
      const visHalfH = dToCam * Math.tan(fovRad * 0.5);
      const visHalfW = visHalfH * aspect;
      const visH = visHalfH * 2;

      const fracFromCam = dToCam / camDist;
      const rayY = camY + (targetY - camY) * fracFromCam;
      const screenBottomY = rayY - visHalfH;

      const desiredTopY = screenBottomY + visH * 0.172;
      // Calibrated framing (12% headroom) leaves the chest bone low in the slot on
      // short avatars, so only clamp at chest level; the chin guard dominates.
      const maxSafeTopY = Math.min(
        chestY + bustSpan * 0.06,
        headY - bustSpan * 0.92
      );
      const lidTopY = Math.min(desiredTopY, maxSafeTopY);

      const lidScale = clamp(visHalfW * 1.08, 0.24, Math.max(0.32, bustSpan * 1.95));
      const halfLidH = 0.316 * lidScale;
      const halfLidD = 0.013 * lidScale;
      const topOffsetY = halfLidH * Math.cos(LID_TILT_RAD) + halfLidD * Math.sin(LID_TILT_RAD);
      const topOffsetZ = halfLidH * Math.sin(LID_TILT_RAD) + halfLidD * Math.cos(LID_TILT_RAD);
      const centerY = lidTopY - topOffsetY;

      lidTopWorldY = lidTopY;
      lidTopWorldZ = laptopZ + topOffsetZ;
      basePos = { x: centerX, y: centerY, z: laptopZ };
      baseScale = lidScale;
      baseVisH = visH;
      baseVisHalfW = visHalfW;

      const phoneDeskX = centerX + Math.min(lidScale * 0.67, Math.max(lidScale * 0.55, visHalfW - lidScale * 0.14));
      const phoneDeskY = lidTopY - lidScale * 0.03;
      const phoneDeskZ = laptopZ + lidScale * 0.045;
      phoneBasePos = { x: phoneDeskX, y: phoneDeskY, z: phoneDeskZ };

      if (laptopGroup) {
        const ls = lidScale * calibration.laptop.scale;
        laptopGroup.scale.set(ls, ls, ls);
        applyLaptopTransform();
      }
      if (phoneGroup) {
        const ps = lidScale * calibration.smartphone.scale;
        phoneGroup.scale.set(ps, ps, ps);
        if (!phonePoseInitialized) {
          livePhonePose = {
            x: phoneBasePos.x,
            y: phoneBasePos.y,
            z: phoneBasePos.z,
            rx: -46 * DEG,
            ry: state.phoneFaceDown ? 168 * DEG : -12 * DEG,
            rz: -6 * DEG
          };
          phonePoseInitialized = true;
        }
        applyPhoneTransform();
      }

      if (screenLight) {
        screenLight.position.set(
          centerX,
          lidTopY + bustSpan * 0.32,
          laptopZ + Math.min(0.18, bustSpan * 0.55)
        );
        screenLight.distance = Math.max(1.4, bustSpan * 6.5);
      }

      if (scratchVec && typeof cam.updateMatrixWorld === "function" && typeof scratchVec.project === "function") {
        cam.updateMatrixWorld(true);
        const ndcToBottomPct = ndcY => clamp((ndcY + 1) * 50, -50, 150);
        scratchVec.set(centerX, lidTopWorldY, lidTopWorldZ).project(cam);
        lidTopViewportPct = ndcToBottomPct(scratchVec.y);

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

    function updateVisuals(params) {
      const { dt, accentHex, targetOffset, immediateOffset, phoneTarget, targetPhoneGlow, targetBuzzRingAlpha, targetGlow, emblemTarget } = params;

      if (immediateOffset) {
        liveLaptopOffset.dx = targetOffset.dx;
        liveLaptopOffset.dy = targetOffset.dy;
        liveLaptopOffset.dz = targetOffset.dz;
        liveLaptopOffset.rx = targetOffset.rx;
        liveLaptopOffset.ry = targetOffset.ry;
        liveLaptopOffset.rz = targetOffset.rz;
      } else {
        const la = dampAlpha(9.5, dt);
        liveLaptopOffset.dx += (targetOffset.dx - liveLaptopOffset.dx) * la;
        liveLaptopOffset.dy += (targetOffset.dy - liveLaptopOffset.dy) * la;
        liveLaptopOffset.dz += (targetOffset.dz - liveLaptopOffset.dz) * la;
        liveLaptopOffset.rx += (targetOffset.rx - liveLaptopOffset.rx) * la;
        liveLaptopOffset.ry += (targetOffset.ry - liveLaptopOffset.ry) * la;
        liveLaptopOffset.rz += (targetOffset.rz - liveLaptopOffset.rz) * la;
      }
      applyLaptopTransform();

      const phoneA = dampAlpha(11.0, dt);
      livePhonePose.x += (phoneTarget.x - livePhonePose.x) * phoneA;
      livePhonePose.y += (phoneTarget.y - livePhonePose.y) * phoneA;
      livePhonePose.z += (phoneTarget.z - livePhonePose.z) * phoneA;
      livePhonePose.rx += (phoneTarget.rx - livePhonePose.rx) * phoneA;
      livePhonePose.ry += (phoneTarget.ry - livePhonePose.ry) * phoneA;
      livePhonePose.rz += (phoneTarget.rz - livePhonePose.rz) * phoneA;
      applyPhoneTransform();
      if (phoneShadowMesh?.material) {
        const baseOpacity = phoneShadowMesh.material.userData?.studioOpacity ?? 0.3;
        phoneShadowMesh.material.opacity = baseOpacity * clamp(1 - palmGrip.weight * 1.6, 0, 1) * Math.max(0, Math.cos(livePhonePose.ry));
      }

      phoneScreenGlow = damp(phoneScreenGlow, targetPhoneGlow, 10.0, dt);
      if (phoneScreenMesh?.material) {
        phoneScreenMesh.material.emissiveIntensity = phoneScreenGlow;
      }
      if (phoneBuzzGlowMesh?.material) {
        phoneBuzzGlowMesh.material.opacity = damp(phoneBuzzGlowMesh.material.opacity ?? 0, targetBuzzRingAlpha, 12.0, dt);
      }

      const goalRgb = hexToRgb(accentHex);
      const colA = dampAlpha(9.0, dt);
      bounceColorRgb = [
        bounceColorRgb[0] + (goalRgb[0] - bounceColorRgb[0]) * colA,
        bounceColorRgb[1] + (goalRgb[1] - bounceColorRgb[1]) * colA,
        bounceColorRgb[2] + (goalRgb[2] - bounceColorRgb[2]) * colA
      ];
      if (screenLight?.color?.setHex) {
        screenLight.color.setHex(mixRgbToHex([224, 236, 255], bounceColorRgb, 0.26));
      }

      screenGlow = damp(screenGlow, targetGlow, 6.0, dt);
      if (screenLight) screenLight.intensity = screenGlow;
      if (emblemMesh?.material) {
        emblemMesh.material.opacity = damp(emblemMesh.material.opacity ?? emblemTarget, emblemTarget, 6.0, dt);
      }
    }

    function getLaptopObject() { return laptopGroup; }

    function projectLaptopBounds(camOverride, scissorRect, anchorRect, overlayMode, scratchBox, scratchVec) {
      const cam = camOverride || camera;
      if (!laptopGroup || laptopGroup.visible === false || !cam) return null;
      const R = scissorRect || anchorRect;
      const w = Math.max(0, Math.round(R?.width || 0)), h = Math.max(0, Math.round(R?.height || 0));
      if (w <= 0 || h <= 0) return null;
      const box = scratchBox || (THREE && typeof THREE.Box3 === "function" ? new THREE.Box3() : null);
      const vec = scratchVec || (THREE && typeof THREE.Vector3 === "function" ? new THREE.Vector3() : null);
      if (!box || !vec || typeof vec.project !== "function") return null;
      laptopGroup.updateMatrixWorld?.(true);
      cam.updateMatrixWorld?.();
      let hasBox = false;
      if (typeof box.setFromObject === "function") {
        box.setFromObject(laptopGroup);
        hasBox = typeof box.isEmpty === "function" ? !box.isEmpty() : true;
      } else if (lidMesh?.geometry && typeof box.copy === "function" && typeof box.applyMatrix4 === "function") {
        if (!lidMesh.geometry.boundingBox && typeof lidMesh.geometry.computeBoundingBox === "function") lidMesh.geometry.computeBoundingBox();
        if (lidMesh.geometry.boundingBox && !lidMesh.geometry.boundingBox.isEmpty?.()) {
          box.copy(lidMesh.geometry.boundingBox).applyMatrix4(lidMesh.matrixWorld);
          hasBox = true;
        }
      }
      if (!hasBox || !box.min || !box.max) return null;
      const min = box.min, max = box.max;
      if (!Number.isFinite(min.x) || !Number.isFinite(min.y) || !Number.isFinite(min.z) ||
          !Number.isFinite(max.x) || !Number.isFinite(max.y) || !Number.isFinite(max.z)) return null;
      let uMinX = Infinity, uMinY = Infinity, uMaxX = -Infinity, uMaxY = -Infinity, count = 0;
      for (let i = 0; i < 8; i++) {
        vec.set((i & 1) ? max.x : min.x, (i & 2) ? max.y : min.y, (i & 4) ? max.z : min.z).project(cam);
        if (!Number.isFinite(vec.x) || !Number.isFinite(vec.y) || !Number.isFinite(vec.z) || vec.z < -1 || vec.z > 1) continue;
        const sx = (vec.x * 0.5 + 0.5) * w, sy = (-vec.y * 0.5 + 0.5) * h;
        if (sx < uMinX) uMinX = sx;
        if (sx > uMaxX) uMaxX = sx;
        if (sy < uMinY) uMinY = sy;
        if (sy > uMaxY) uMaxY = sy;
        count++;
      }
      if (!count || uMaxX <= 0 || uMinX >= w || uMaxY <= 0 || uMinY >= h) return null;
      const x1 = clamp(Math.floor(uMinX), 0, w), y1 = clamp(Math.floor(uMinY), 0, h);
      const x2 = clamp(Math.ceil(uMaxX), 0, w), y2 = clamp(Math.ceil(uMaxY), 0, h);
      if (x2 - x1 < 8 || y2 - y1 < 8) return null;
      const vpX = (R.left || 0) + x1, vpY = (R.top || 0) + y1;
      return { x: overlayMode ? Math.round(vpX - (anchorRect?.left || 0)) : x1, y: overlayMode ? Math.round(vpY - (anchorRect?.top || 0)) : y1, width: x2 - x1, height: y2 - y1, viewportX: vpX, viewportY: vpY };
    }

    function getPropObjects() {
      const out = [];
      if (laptopGroup) out.push(laptopGroup);
      if (phoneGroup) out.push(phoneGroup);
      return out;
    }

    function getMetrics() {
      return {
        basePos,
        baseScale,
        baseVisH,
        baseVisHalfW,
        phoneBasePos,
        liveLaptopOffset,
        lidTopWorldY,
        lidTopViewportPct,
        chinViewportPct,
        headViewportPct,
        screenGlow,
        phoneScreenGlow,
        hasLaptopProp: !!laptopGroup,
        hasPhoneProp: !!phoneGroup,
        palmGrip
      };
    }

    function dispose() {
      if (laptopGroup && scene) scene.remove(laptopGroup);
      if (phoneGroup && scene) scene.remove(phoneGroup);
      if (screenLight && scene) scene.remove(screenLight);
      for (const g of geometries) { try { g.dispose?.(); } catch (_) { /* ignore */ } }
      for (const m of materials) { try { m.dispose?.(); } catch (_) { /* ignore */ } }
      for (const tx of textures) { try { tx.dispose?.(); } catch (_) { /* ignore */ } }
      geometries.length = 0;
      materials.length = 0;
      textures.length = 0;
      laptopGroup = lidMesh = rimMesh = emblemMesh = null;
      phoneGroup = phoneBodyMesh = phoneScreenMesh = phoneBackMesh = phoneCameraBumpMesh = phoneBuzzGlowMesh = null;
      screenLight = lidTexture = emblemTexture = phoneScreenTexture = phoneBackTexture = null;
      hingeAccentMesh = phoneShadowMesh = shadowTexture = null;
      palmGrip.mode = "none";
      palmGrip.weight = 0;
      lastFraming = null;
      phonePoseInitialized = false;
    }

    return {
      buildPropIfNeeded,
      applyAccentVisuals,
      setCalibration,
      getPropCalibration,
      getPropAnchorWorld,
      applyLaptopTransform,
      applyPhoneTransform,
      layoutProp,
      syncPhoneToHand,
      updateVisuals,
      getLaptopObject,
      projectLaptopBounds,
      getPropObjects,
      getMetrics,
      dispose
    };
  }

  Object.assign(C, {
    sessionAccentForKey,
    vrmCoLeadProps: {
      DEFAULT_ACCENT,
      SESSION_ACCENT_PALETTE,
      LID_TILT_RAD,
      normalizeHexColor,
      hexToRgb,
      mixRgbToHex,
      sessionAccentForKey,
      findPaletteEntryByHex,
      makeLidTexture,
      makeEmblemGlowTexture,
      makePhoneScreenTexture,
      makePhoneBackTexture,
      makeContactShadowTexture,
      makeRoundedRectShape,
      createCoLeadProps
    }
  });
})(window.WsCompanion);
