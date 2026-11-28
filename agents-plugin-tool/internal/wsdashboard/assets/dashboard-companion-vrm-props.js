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

  // Paint a brushed anodized dark-slate lid texture matching Werkschaft's dark
  // tonal surface (#222630 - #2b303d) with a chamfered top specular edge.
  function makeLidTexture(THREE) {
    if (typeof document === "undefined" || typeof THREE.CanvasTexture !== "function") return null;
    const canvas = document.createElement("canvas");
    canvas.width = 512;
    canvas.height = 320;
    const ctx = canvas.getContext("2d");
    if (!ctx || typeof ctx.createLinearGradient !== "function" || typeof ctx.fillRect !== "function") return null;

    const grad = ctx.createLinearGradient(0, 0, 0, canvas.height);
    grad.addColorStop(0.0, "#3d4658");
    grad.addColorStop(0.04, "#2f3645");
    grad.addColorStop(0.30, "#262b38");
    grad.addColorStop(0.76, "#1f242f");
    grad.addColorStop(1.0, "#171a22");
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, canvas.width, canvas.height);

    const sheen = ctx.createLinearGradient(0, 0, canvas.width, 0);
    sheen.addColorStop(0.0, "rgba(185, 205, 240, 0.03)");
    sheen.addColorStop(0.28, "rgba(215, 228, 252, 0.11)");
    sheen.addColorStop(0.52, "rgba(235, 244, 255, 0.17)");
    sheen.addColorStop(0.76, "rgba(210, 224, 248, 0.09)");
    sheen.addColorStop(1.0, "rgba(185, 205, 240, 0.03)");
    ctx.fillStyle = sheen;
    ctx.fillRect(0, 0, canvas.width, 54);

    ctx.fillStyle = "rgba(255, 255, 255, 0.016)";
    for (let y = 4; y < canvas.height; y += 3) {
      ctx.fillRect(0, y, canvas.width, 1);
    }

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

  // Paint a crisp minimalist smartphone front display texture (OLED notification card).
  function makePhoneScreenTexture(THREE) {
    if (typeof document === "undefined" || typeof THREE.CanvasTexture !== "function") return null;
    const canvas = document.createElement("canvas");
    canvas.width = 128;
    canvas.height = 256;
    const ctx = canvas.getContext("2d");
    if (!ctx || typeof ctx.createLinearGradient !== "function" || typeof ctx.fillRect !== "function") return null;

    const bg = ctx.createLinearGradient(0, 0, 0, 256);
    bg.addColorStop(0.0, "#1c2740");
    bg.addColorStop(0.55, "#151d30");
    bg.addColorStop(1.0, "#0f1422");
    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, 128, 256);

    ctx.fillStyle = "rgba(180, 212, 255, 0.35)";
    ctx.fillRect(42, 10, 44, 6);

    ctx.fillStyle = "rgba(122, 162, 255, 0.92)";
    ctx.fillRect(14, 42, 100, 46);
    ctx.fillStyle = "#ffffff";
    ctx.fillRect(24, 52, 54, 7);
    ctx.fillStyle = "rgba(255, 255, 255, 0.72)";
    ctx.fillRect(24, 66, 76, 6);

    ctx.fillStyle = "rgba(246, 228, 173, 0.92)";
    ctx.fillRect(30, 106, 84, 34);
    ctx.fillStyle = "#3c3222";
    ctx.fillRect(40, 118, 60, 6);

    ctx.fillStyle = "rgba(226, 234, 250, 0.24)";
    ctx.fillRect(14, 154, 82, 34);
    ctx.fillStyle = "rgba(235, 242, 255, 0.8)";
    ctx.fillRect(24, 166, 56, 6);

    ctx.fillStyle = "rgba(255, 255, 255, 0.45)";
    ctx.fillRect(42, 240, 44, 4);

    const tex = new THREE.CanvasTexture(canvas);
    if (THREE.SRGBColorSpace) tex.colorSpace = THREE.SRGBColorSpace;
    tex.needsUpdate = true;
    return tex;
  }

  // Paint a metallic back-shell texture with matte gradient so the back side
  // of the smartphone reads unmistakably apart from the OLED front.
  function makePhoneBackTexture(THREE) {
    if (typeof document === "undefined" || typeof THREE.CanvasTexture !== "function") return null;
    const canvas = document.createElement("canvas");
    canvas.width = 128;
    canvas.height = 256;
    const ctx = canvas.getContext("2d");
    if (!ctx || typeof ctx.createLinearGradient !== "function" || typeof ctx.fillRect !== "function") return null;

    const grad = ctx.createLinearGradient(0, 0, 128, 256);
    grad.addColorStop(0.0, "#c8d2e6");
    grad.addColorStop(0.45, "#9eaac2");
    grad.addColorStop(1.0, "#76829c");
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, 128, 256);

    ctx.fillStyle = "rgba(255, 255, 255, 0.22)";
    ctx.beginPath?.();
    if (typeof ctx.arc === "function") {
      ctx.arc(64, 134, 16, 0, Math.PI * 2);
      ctx.fill();
    }

    const tex = new THREE.CanvasTexture(canvas);
    if (THREE.SRGBColorSpace) tex.colorSpace = THREE.SRGBColorSpace;
    tex.needsUpdate = true;
    return tex;
  }

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
    const scratchVec = THREE && typeof THREE.Vector3 === "function" ? new THREE.Vector3() : null;

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

    function applyLaptopTransform() {
      if (!laptopGroup) return;
      laptopGroup.position.set(
        basePos.x + liveLaptopOffset.dx,
        basePos.y + liveLaptopOffset.dy,
        basePos.z + liveLaptopOffset.dz
      );
      laptopGroup.rotation.set(
        LID_TILT_RAD + liveLaptopOffset.rx,
        liveLaptopOffset.ry,
        liveLaptopOffset.rz,
        "XYZ"
      );
    }

    function applyPhoneTransform() {
      if (!phoneGroup) return;
      phoneGroup.position.set(livePhonePose.x, livePhonePose.y, livePhonePose.z);
      phoneGroup.rotation.set(livePhonePose.rx, livePhonePose.ry, livePhonePose.rz, "XYZ");
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

        for (const ly of [0.126, 0.072]) {
          const lens = new THREE.Mesh(lensGeo, lensMat);
          lens.rotation.set(Math.PI * 0.5, 0, 0);
          lens.position.set(0.052, ly, -(phoneD * 0.5 + 0.021));
          lens.frustumCulled = false;
          phoneGroup.add(lens);
        }
        const flash = new THREE.Mesh(flashGeo, flashMat);
        flash.rotation.set(Math.PI * 0.5, 0, 0);
        flash.position.set(0.014, 0.099, -(phoneD * 0.5 + 0.020));
        flash.frustumCulled = false;
        phoneGroup.add(flash);
      }

      scene.add(phoneGroup);
      applyAccentVisuals(accentHex);
    }

    // Anchor the held 3D smartphone cleanly to the avatar's actual posed left palm
    // world position (post-vrm.update) so both VRM 1.0 and VRM 0.x physically hold
    // the phone in hand with zero floating gap regardless of arm length.
    function syncPhoneToHand(handWorldPos, phoneHoldWeight) {
      if (!phoneGroup || !handWorldPos || phoneHoldWeight <= 0.01) return;
      if (!Number.isFinite(handWorldPos.x) || !Number.isFinite(handWorldPos.y) || !Number.isFinite(handWorldPos.z)) return;
      const holdBlend = easeInOutCubic(clamp(phoneHoldWeight, 0, 1));
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
      const maxSafeTopY = Math.min(
        chestY - bustSpan * 0.14,
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
        laptopGroup.scale.set(lidScale, lidScale, lidScale);
        applyLaptopTransform();
      }
      if (phoneGroup) {
        phoneGroup.scale.set(lidScale, lidScale, lidScale);
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
        hasPhoneProp: !!phoneGroup
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
      lastFraming = null;
      phonePoseInitialized = false;
    }

    return {
      buildPropIfNeeded,
      applyAccentVisuals,
      applyLaptopTransform,
      applyPhoneTransform,
      layoutProp,
      syncPhoneToHand,
      updateVisuals,
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
      makeRoundedRectShape,
      createCoLeadProps
    }
  });
})(window.WsCompanion);
