(function (C) {
  "use strict";

  // ============================================================================
  // Co-Lead Studio: Bust Framing Solver, Theme-Adaptive Lighting, Prop-Only
  // PMREM Environment, Contact Shadows, Palm Probes & Procedural Prop Textures
  // ============================================================================
  // Loaded after dashboard-companion-vrm-fx.js and before the props/pose/colead
  // modules. Every per-frame helper here is allocation-free; textures, lights,
  // and the PMREM environment are built once per renderer and disposed with it.

  const DEG = Math.PI / 180;
  const HEADROOM_RATIO = 0.12;
  const HEADROOM_MIN = 0.10;
  const HEADROOM_MAX = 0.14;
  const ENV_METALNESS_MIN = 0.3;

  function clamp(v, lo, hi) {
    return v < lo ? lo : (v > hi ? hi : v);
  }

  // ---------------------------------------------------------------------------
  // Bust framing
  // ---------------------------------------------------------------------------
  // Solve camera distance/height so the true hair top sits HEADROOM_RATIO of the
  // slot height below the top edge (measured on the head plane), while the upper
  // body width (hair, head, shoulders) fits horizontally on the nearer front plane.
  // When width-limited the frame grows downward (more torso), never upward, so
  // headroom stays pinned inside 10-14% at every 220-360px rail width.
  function solveBustFraming(input) {
    const fov = Number.isFinite(input?.fov) && input.fov > 1 ? input.fov : 26;
    const tanHalf = Math.tan(fov * DEG * 0.5);
    const aspect = Math.max(0.45, Number.isFinite(input?.aspect) ? input.aspect : 1.2);
    const ratio = clamp(Number.isFinite(input?.headroomRatio) ? input.headroomRatio : HEADROOM_RATIO, HEADROOM_MIN, HEADROOM_MAX);
    const topY = Number.isFinite(input?.topY) ? input.topY : 1.6;
    const waistY = Number.isFinite(input?.waistY) ? input.waistY : topY - 0.6;
    const bustSpan = Math.max(0.18, Number.isFinite(input?.bustSpan) ? input.bustSpan : 0.3);
    const frontOffset = clamp(Number.isFinite(input?.frontOffset) ? input.frontOffset : 0.1, 0, 0.5);
    const upperWidth = Math.max(0.1, Number.isFinite(input?.upperWidth) ? input.upperWidth : bustSpan * 2.3);

    const visibleBustSpan = Math.max(bustSpan * 2.1, topY - waistY);
    const heightFrame = visibleBustSpan / (1 - ratio);
    const distForHeight = heightFrame * 0.5 / tanHalf;
    // Width must fit on the front plane, frontOffset nearer than the head plane:
    // (dist - frontOffset) * 2 * tanHalf * aspect >= upperWidth.
    const distForWidth = (upperWidth / aspect) * 0.5 / tanHalf + frontOffset;
    const limitedBy = distForWidth > distForHeight ? "width" : "height";
    const dist = Math.max(distForHeight, distForWidth);
    const frameHeight = 2 * dist * tanHalf;
    const frameTopY = topY + frameHeight * ratio;
    const targetY = frameTopY - frameHeight * 0.5;
    return { dist, frameHeight, frameTopY, targetY, headroomRatio: ratio, limitedBy, aspect, fov };
  }

  // Load-time framing QA metrics (slot percentages) from the solved camera.
  function measureFraming(THREE, camera, framing) {
    if (!THREE || typeof THREE.Vector3 !== "function" || !camera || !framing) return null;
    const v = new THREE.Vector3();
    if (typeof v.project !== "function") return null;
    camera.updateMatrixWorld?.(true);
    const cx = framing.centerX || 0;
    const z = framing.headZ || 0;
    v.set(cx, framing.topY, z).project(camera);
    const headroomPct = (1 - v.y) * 50;
    const halfW = Math.max(0, (framing.upperWidth || 0) * 0.5);
    v.set(cx + halfW, framing.chestY, z).project(camera);
    const rightEdge = v.x;
    v.set(cx - halfW, framing.chestY, z).project(camera);
    const leftEdge = v.x;
    const sideMarginPct = Math.min(1 - rightEdge, 1 + leftEdge) * 50;
    return {
      headroomPct: Number(headroomPct.toFixed(2)),
      sideMarginPct: Number(sideMarginPct.toFixed(2)),
      limitedBy: framing.limitedBy || "height"
    };
  }

  // One-shot pixel-true headroom probe (never per frame). Reads the docked slot's
  // alpha right after renderer.render (same task, so preserveDrawingBuffer is not
  // needed) and returns the first visible row from the top as a slot ratio. This
  // catches alpha-cut hair/ear/hat cards whose geometry bounds sit above the
  // visible silhouette. rect is CSS px (top-left origin) inside a viewport vh tall.
  let alphaProbeBuf = null;
  function measureAlphaTopRow(gl, rect, vh, dpr) {
    if (!gl || typeof gl.readPixels !== "function" || !rect || !(rect.width > 0) || !(rect.height > 0)) return null;
    const s = Number.isFinite(dpr) && dpr > 0 ? dpr : 1;
    const x = Math.max(0, Math.round(rect.left * s));
    const y = Math.max(0, Math.round((vh - rect.top - rect.height) * s));
    const w = Math.max(1, Math.round(rect.width * s));
    const h = Math.max(1, Math.round(rect.height * s));
    const need = w * h * 4;
    if (!alphaProbeBuf || alphaProbeBuf.length < need) alphaProbeBuf = new Uint8Array(need);
    try {
      gl.readPixels(x, y, w, h, gl.RGBA, gl.UNSIGNED_BYTE, alphaProbeBuf);
    } catch (_) {
      return null;
    }
    // GL rows are bottom-up; scan from the top row down. Require 2+ pixels so a
    // single antialiasing speck never counts as the silhouette top.
    for (let row = h - 1; row >= 0; row--) {
      let hits = 0;
      const base = row * w * 4;
      for (let i = 0; i < w; i++) {
        if (alphaProbeBuf[base + i * 4 + 3] > 12 && ++hits >= 2) break;
      }
      if (hits >= 2) return { topRow: h - 1 - row, height: h, ratio: (h - 1 - row) / h };
    }
    return { topRow: -1, height: h, ratio: -1 };
  }

  // ---------------------------------------------------------------------------
  // Theme-adaptive studio lighting presets
  // ---------------------------------------------------------------------------
  // Dark stays close to the Phase 1 flat-anime MToon rig (warm key + cool
  // separation rim against #111216), trimming key/hemisphere slightly to make room
  // for a soft cool fill. Light lowers the rim, warms the fill, and lifts the
  // hemisphere ground so the avatar never reads muddy on #e7e7e2 nor clips white.
  const STUDIO_PRESETS = Object.freeze({
    dark: Object.freeze({
      key: Object.freeze({ color: 0xfff6ec, intensity: 1.10, pos: Object.freeze([0.6, 1.4, 2.0]) }),
      fill: Object.freeze({ color: 0xc8d6ff, intensity: 0.26, pos: Object.freeze([-1.3, 1.1, 1.4]) }),
      rim: Object.freeze({ color: 0xa8c8ff, intensity: 0.74, pos: Object.freeze([-0.9, 1.75, -1.65]) }),
      hemi: Object.freeze({ sky: 0xe4ecff, ground: 0x3b3e4c, intensity: 0.88 }),
      envIntensity: 0.9,
      shadowOpacity: 0.36
    }),
    light: Object.freeze({
      key: Object.freeze({ color: 0xfff1e2, intensity: 1.0, pos: Object.freeze([0.7, 1.5, 2.0]) }),
      fill: Object.freeze({ color: 0xfff4ea, intensity: 0.30, pos: Object.freeze([-1.3, 1.0, 1.5]) }),
      rim: Object.freeze({ color: 0xffe2c8, intensity: 0.38, pos: Object.freeze([-0.9, 1.75, -1.65]) }),
      hemi: Object.freeze({ sky: 0xffffff, ground: 0x8c877c, intensity: 0.94 }),
      envIntensity: 1.1,
      shadowOpacity: 0.22
    })
  });

  // Explicit :root[data-theme] wins; otherwise follow prefers-color-scheme
  // (matching dashboard CSS), defaulting to dark when media queries are absent.
  function resolveThemeMode(doc, win) {
    const root = doc?.documentElement || null;
    const attr = root?.getAttribute?.("data-theme") || root?.dataset?.theme || "";
    if (attr === "light" || attr === "dark") return attr;
    try {
      const mq = typeof win?.matchMedia === "function" ? win.matchMedia("(prefers-color-scheme: dark)") : null;
      if (mq && typeof mq.matches === "boolean") return mq.matches ? "dark" : "light";
    } catch (_) { /* ignore */ }
    return "dark";
  }

  function makeStudioEnvScene(THREE) {
    const envScene = new THREE.Scene();
    const disposables = [];
    if (typeof THREE.SphereGeometry === "function" && typeof THREE.MeshBasicMaterial === "function") {
      const geo = new THREE.SphereGeometry(1, 24, 12);
      const pos = geo.attributes?.position;
      if (pos && typeof THREE.BufferAttribute === "function") {
        const colors = new Float32Array(pos.count * 3);
        for (let i = 0; i < pos.count; i++) {
          const t = clamp((pos.getY(i) + 1) * 0.5, 0, 1);
          // Floor (warm charcoal) -> horizon (soft gray) -> ceiling (cool white).
          colors[i * 3] = 0.10 + t * 0.62;
          colors[i * 3 + 1] = 0.10 + t * 0.66;
          colors[i * 3 + 2] = 0.12 + t * 0.74;
        }
        geo.setAttribute("color", new THREE.BufferAttribute(colors, 3));
      }
      const mat = new THREE.MeshBasicMaterial({ vertexColors: true, side: THREE.BackSide });
      envScene.add(new THREE.Mesh(geo, mat));
      disposables.push(geo, mat);
    }
    if (typeof THREE.PlaneGeometry === "function" && typeof THREE.MeshBasicMaterial === "function") {
      // Two studio softboxes (key above-front, strip at the side) for crisp
      // metallic highlights on the lid chamfer, hinge barrel, and camera lenses.
      const boxGeo = new THREE.PlaneGeometry(1, 1);
      const boxMat = new THREE.MeshBasicMaterial({ color: 0xffffff, side: THREE.DoubleSide });
      boxMat.color.multiplyScalar?.(3.2);
      const stripMat = new THREE.MeshBasicMaterial({ color: 0xdfe8ff, side: THREE.DoubleSide });
      stripMat.color.multiplyScalar?.(1.8);
      const key = new THREE.Mesh(boxGeo, boxMat);
      key.position.set(0.25, 0.72, 0.55);
      key.scale.set(0.8, 0.45, 1);
      key.lookAt(0, 0, 0);
      const strip = new THREE.Mesh(boxGeo, stripMat);
      strip.position.set(-0.85, 0.15, 0.2);
      strip.scale.set(0.18, 0.9, 1);
      strip.lookAt(0, 0, 0);
      envScene.add(key, strip);
      disposables.push(boxGeo, boxMat, stripMat);
    }
    return { envScene, disposables };
  }

  function setLight(light, spec) {
    if (!light || !spec) return;
    light.color?.setHex?.(spec.color);
    light.intensity = spec.intensity;
    light.position?.set?.(spec.pos[0], spec.pos[1], spec.pos[2]);
  }

  // Key/fill/rim directional lights + hemisphere light that follow the dashboard
  // theme live. Theme switches mutate existing light colors/intensities in place
  // (zero allocation, zero scene graph churn).
  function createStudioLighting(opts = {}) {
    const THREE = opts.THREE || null;
    const scene = opts.scene || null;
    const renderer = opts.renderer || null;
    const doc = opts.doc || (typeof document !== "undefined" ? document : null);
    const win = opts.win || (typeof window !== "undefined" ? window : null);
    let mode = "";
    let themeChanges = 0;
    let observer = null;
    let mediaQuery = null;
    let envTarget = null;
    let envTexture = null;
    let envFailed = false;
    const envMaterials = new Set();
    const shadowMaterials = new Set();

    const mk = (Ctor, a, b, c) => (typeof Ctor === "function" ? new Ctor(a, b, c) : null);
    const key = mk(THREE?.DirectionalLight, 0xffffff, 1);
    const fill = mk(THREE?.DirectionalLight, 0xffffff, 0.3);
    const rim = mk(THREE?.DirectionalLight, 0xffffff, 0.5);
    const hemi = mk(THREE?.HemisphereLight, 0xffffff, 0x404040, 0.9);
    if (key) key.name = "ws-studio-key";
    if (fill) fill.name = "ws-studio-fill";
    if (rim) rim.name = "ws-studio-rim";
    if (hemi) hemi.name = "ws-studio-hemi";
    const lights = [key, fill, rim, hemi].filter(Boolean);
    if (scene && lights.length) scene.add(...lights);

    function applyPresetToMaterials(preset) {
      for (const m of envMaterials) m.envMapIntensity = preset.envIntensity;
      for (const m of shadowMaterials) {
        if (!m.userData) m.userData = {};
        m.userData.studioOpacity = preset.shadowOpacity;
      }
    }

    function applyTheme(next) {
      const m = next === "light" ? "light" : "dark";
      if (m === mode) return mode;
      mode = m;
      themeChanges++;
      const p = STUDIO_PRESETS[m];
      setLight(key, p.key);
      setLight(fill, p.fill);
      setLight(rim, p.rim);
      if (hemi) {
        hemi.color?.setHex?.(p.hemi.sky);
        hemi.groundColor?.setHex?.(p.hemi.ground);
        hemi.intensity = p.hemi.intensity;
      }
      applyPresetToMaterials(p);
      opts.onThemeChange?.(m, p);
      return mode;
    }

    function refresh() {
      return applyTheme(resolveThemeMode(doc, win));
    }

    const MO = win?.MutationObserver || (typeof MutationObserver === "function" ? MutationObserver : null);
    if (MO && doc?.documentElement) {
      try {
        observer = new MO(() => refresh());
        observer.observe(doc.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
      } catch (_) { observer = null; }
    }
    try {
      mediaQuery = typeof win?.matchMedia === "function" ? win.matchMedia("(prefers-color-scheme: dark)") : null;
      if (mediaQuery?.addEventListener) mediaQuery.addEventListener("change", refresh);
      else if (mediaQuery?.addListener) mediaQuery.addListener(refresh);
    } catch (_) { mediaQuery = null; }
    refresh();

    function ensureEnvironment() {
      if (envTexture || envFailed) return envTexture;
      const canPmrem = THREE && typeof THREE.PMREMGenerator === "function" && renderer &&
        typeof renderer.getRenderTarget === "function" && typeof renderer.setRenderTarget === "function";
      if (!canPmrem) { envFailed = true; return null; }
      let pmrem = null;
      let built = null;
      try {
        pmrem = new THREE.PMREMGenerator(renderer);
        built = makeStudioEnvScene(THREE);
        // Keep the PMREM output target: disposing only its texture leaks the FBO.
        envTarget = pmrem.fromScene(built.envScene, 0.035) || null;
        envTexture = envTarget?.texture || null;
      } catch (_) {
        envTexture = null;
      } finally {
        if (built) for (const d of built.disposables) { try { d.dispose?.(); } catch (_) { /* ignore */ } }
        try { pmrem?.dispose?.(); } catch (_) { /* ignore */ }
      }
      if (!envTexture) {
        envFailed = true;
        try { envTarget?.dispose?.(); } catch (_) { /* ignore */ }
        envTarget = null;
      }
      return envTexture;
    }

    // Prop-only env: metallic MeshStandardMaterials under the given prop roots get
    // the PMREM env; contact-shadow materials get the theme opacity. The avatar
    // (MToon) and scene.environment are never touched.
    function attachPropEnvironment(roots) {
      if (!Array.isArray(roots) || !roots.length) return envMaterials.size;
      const preset = STUDIO_PRESETS[mode || "dark"];
      const env = ensureEnvironment();
      for (const root of roots) {
        if (!root || typeof root.traverse !== "function") continue;
        root.traverse(obj => {
          const mat = obj?.material;
          if (!mat || Array.isArray(mat)) return;
          if (obj.userData?.contactShadow) {
            if (!shadowMaterials.has(mat)) {
              shadowMaterials.add(mat);
              applyPresetToMaterials(preset);
            }
            return;
          }
          if (!env || !mat.isMeshStandardMaterial || mat.isMToonMaterial) return;
          if (!(mat.metalness >= ENV_METALNESS_MIN) || envMaterials.has(mat)) return;
          mat.envMap = env;
          mat.envMapIntensity = preset.envIntensity;
          mat.needsUpdate = true;
          envMaterials.add(mat);
        });
      }
      return envMaterials.size;
    }

    function getState() {
      const p = STUDIO_PRESETS[mode || "dark"];
      return {
        mode,
        themeChanges,
        observing: !!observer,
        followsMedia: !!mediaQuery,
        keyIntensity: key ? key.intensity : p.key.intensity,
        fillIntensity: fill ? fill.intensity : p.fill.intensity,
        rimIntensity: rim ? rim.intensity : p.rim.intensity,
        hemiIntensity: hemi ? hemi.intensity : p.hemi.intensity,
        envReady: !!envTexture,
        envMaterialCount: envMaterials.size,
        shadowOpacity: p.shadowOpacity,
        lightCount: lights.length
      };
    }

    function dispose() {
      try { observer?.disconnect?.(); } catch (_) { /* ignore */ }
      try {
        if (mediaQuery?.removeEventListener) mediaQuery.removeEventListener("change", refresh);
        else if (mediaQuery?.removeListener) mediaQuery.removeListener(refresh);
      } catch (_) { /* ignore */ }
      observer = null;
      mediaQuery = null;
      if (scene) for (const l of lights) scene.remove(l);
      for (const m of envMaterials) { if (m.envMap === envTexture) m.envMap = null; }
      envMaterials.clear();
      shadowMaterials.clear();
      // WebGLRenderTarget.dispose releases the FBO and its texture together.
      try {
        if (envTarget && typeof envTarget.dispose === "function") envTarget.dispose();
        else envTexture?.dispose?.();
      } catch (_) { /* ignore */ }
      envTarget = null;
      envTexture = null;
    }

    return { applyTheme, refresh, attachPropEnvironment, getState, getMode: () => mode, lights, dispose };
  }

  // ---------------------------------------------------------------------------
  // Palm probe (allocation-free per frame)
  // ---------------------------------------------------------------------------
  function createPalmProbe(THREE, side = "left") {
    if (!THREE || typeof THREE.Vector3 !== "function") return null;
    return {
      side: side === "right" ? "right" : "left",
      ok: false,
      hasFingers: false,
      hasHead: false,
      wrist: new THREE.Vector3(),
      index: new THREE.Vector3(),
      middle: new THREE.Vector3(),
      little: new THREE.Vector3(),
      anchor: new THREE.Vector3(),
      head: new THREE.Vector3()
    };
  }

  // Sample wrist + index/middle/little proximal world positions after vrm.update.
  // probe.anchor keeps the legacy wrist->knuckle anchor used when fingers are missing.
  function samplePalmProbe(humanoid, probe) {
    if (!probe) return false;
    probe.ok = false;
    probe.hasFingers = false;
    probe.hasHead = false;
    const get = humanoid?.getNormalizedBoneNode;
    if (typeof get !== "function") return false;
    const side = probe.side;
    const wrist = get.call(humanoid, side + "Hand");
    if (!wrist || typeof wrist.getWorldPosition !== "function") return false;
    wrist.getWorldPosition(probe.wrist);
    probe.anchor.copy(probe.wrist);
    const idx = get.call(humanoid, side + "IndexProximal");
    const mid = get.call(humanoid, side + "MiddleProximal");
    const lit = get.call(humanoid, side + "LittleProximal");
    if (mid || idx) {
      (mid || idx).getWorldPosition(probe.middle);
      probe.anchor.lerp(probe.middle, 0.45);
    }
    if (idx && mid && lit) {
      idx.getWorldPosition(probe.index);
      lit.getWorldPosition(probe.little);
      probe.hasFingers = true;
    }
    const head = get.call(humanoid, "head");
    if (head) {
      head.getWorldPosition(probe.head);
      probe.hasHead = true;
    }
    probe.ok = true;
    return true;
  }

  // Debug-only (never per frame): project hand/finger/head bones and the phone
  // into the active viewport rect as percentages (x from left, y from bottom).
  function measureSlotPoints(THREE, camera, vrm, propRoots) {
    if (!THREE || typeof THREE.Vector3 !== "function" || !camera || typeof vrm?.humanoid?.getNormalizedBoneNode !== "function") return null;
    const v = new THREE.Vector3();
    if (typeof v.project !== "function") return null;
    camera.updateMatrixWorld?.(true);
    const pct = () => {
      v.project(camera);
      return { x: Number(((v.x + 1) * 50).toFixed(2)), y: Number(((v.y + 1) * 50).toFixed(2)) };
    };
    const out = {};
    for (const name of ["head", "leftHand", "rightHand", "leftMiddleDistal", "rightMiddleDistal", "leftIndexDistal"]) {
      const node = vrm.humanoid.getNormalizedBoneNode(name);
      if (!node || typeof node.getWorldPosition !== "function") continue;
      node.getWorldPosition(v);
      out[name] = pct();
    }
    const phone = Array.isArray(propRoots) ? propRoots.find(p => p?.name === "ws-colead-phone") : null;
    if (phone && typeof phone.getWorldPosition === "function") {
      phone.getWorldPosition(v);
      out.phone = pct();
    }
    return out;
  }

  // ---------------------------------------------------------------------------
  // Procedural prop textures (MToon/Standard-compatible sRGB canvas textures)
  // ---------------------------------------------------------------------------
  function makeCanvas(w, h) {
    if (typeof document === "undefined" || typeof document.createElement !== "function") return null;
    const canvas = document.createElement("canvas");
    canvas.width = w;
    canvas.height = h;
    const ctx = canvas.getContext?.("2d");
    if (!ctx || typeof ctx.createLinearGradient !== "function" || typeof ctx.fillRect !== "function") return null;
    return { canvas, ctx };
  }

  function toTexture(THREE, canvas) {
    const tex = new THREE.CanvasTexture(canvas);
    if (THREE.SRGBColorSpace) tex.colorSpace = THREE.SRGBColorSpace;
    if (typeof THREE.LinearMipmapLinearFilter !== "undefined") tex.minFilter = THREE.LinearMipmapLinearFilter;
    tex.anisotropy = 4;
    tex.needsUpdate = true;
    return tex;
  }

  function roundRectPath(ctx, x, y, w, h, r) {
    const rr = Math.min(r, w * 0.5, h * 0.5);
    ctx.beginPath();
    ctx.moveTo(x + rr, y);
    ctx.lineTo(x + w - rr, y);
    ctx.quadraticCurveTo(x + w, y, x + w, y + rr);
    ctx.lineTo(x + w, y + h - rr);
    ctx.quadraticCurveTo(x + w, y + h, x + w - rr, y + h);
    ctx.lineTo(x + rr, y + h);
    ctx.quadraticCurveTo(x, y + h, x, y + h - rr);
    ctx.lineTo(x, y + rr);
    ctx.quadraticCurveTo(x, y, x + rr, y);
    ctx.closePath();
  }

  // Brushed anodized dark-slate lid with a chamfered specular perimeter.
  function makeLidTexture(THREE) {
    if (typeof THREE?.CanvasTexture !== "function") return null;
    const c = makeCanvas(512, 320);
    if (!c) return null;
    const { canvas, ctx } = c;
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
    for (let y = 4; y < canvas.height; y += 3) ctx.fillRect(0, y, canvas.width, 1);

    // Chamfered perimeter: bright top bevel, soft side bevels, darker bottom lip.
    const rim = ctx.createLinearGradient(24, 0, canvas.width - 24, 0);
    rim.addColorStop(0.0, "rgba(164, 184, 220, 0.22)");
    rim.addColorStop(0.5, "rgba(230, 240, 255, 0.56)");
    rim.addColorStop(1.0, "rgba(164, 184, 220, 0.22)");
    ctx.fillStyle = rim;
    ctx.fillRect(14, 2, canvas.width - 28, 3);
    if (typeof ctx.strokeRect === "function") {
      ctx.strokeStyle = "rgba(196, 210, 236, 0.16)";
      ctx.lineWidth = 2;
      ctx.strokeRect(5, 5, canvas.width - 10, canvas.height - 10);
    }
    ctx.fillStyle = "rgba(6, 8, 12, 0.32)";
    ctx.fillRect(10, canvas.height - 6, canvas.width - 20, 4);
    return toTexture(THREE, canvas);
  }

  // Soft radial halo texture for the center-upper accent emblem pill.
  function makeEmblemGlowTexture(THREE) {
    if (typeof THREE?.CanvasTexture !== "function") return null;
    const c = makeCanvas(128, 64);
    if (!c || typeof c.ctx.createRadialGradient !== "function" || typeof c.ctx.beginPath !== "function") return null;
    const { canvas, ctx } = c;
    ctx.clearRect?.(0, 0, 128, 64);
    const halo = ctx.createRadialGradient(64, 32, 3, 64, 32, 54);
    halo.addColorStop(0.0, "rgba(255, 255, 255, 0.92)");
    halo.addColorStop(0.28, "rgba(255, 255, 255, 0.45)");
    halo.addColorStop(0.62, "rgba(255, 255, 255, 0.12)");
    halo.addColorStop(1.0, "rgba(255, 255, 255, 0.0)");
    ctx.fillStyle = halo;
    ctx.fillRect(0, 0, 128, 64);
    roundRectPath(ctx, 42, 26, 44, 12, 6);
    ctx.fillStyle = "rgba(255, 255, 255, 0.96)";
    ctx.fill();
    return toTexture(THREE, canvas);
  }

  // Crisp 2x OLED chat preview: true-black panel, status bar, punch-hole camera,
  // chat bubbles with avatar dots, and a composer pill.
  function makePhoneScreenTexture(THREE) {
    if (typeof THREE?.CanvasTexture !== "function") return null;
    const c = makeCanvas(256, 512);
    if (!c) return null;
    const { canvas, ctx } = c;
    const bg = ctx.createLinearGradient(0, 0, 0, 512);
    bg.addColorStop(0.0, "#0d1220");
    bg.addColorStop(0.5, "#090d17");
    bg.addColorStop(1.0, "#05070d");
    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, 256, 512);
    const hasPath = typeof ctx.beginPath === "function" && typeof ctx.fill === "function";
    const pill = (x, y, w, h, r, style) => {
      ctx.fillStyle = style;
      if (hasPath && typeof ctx.quadraticCurveTo === "function") { roundRectPath(ctx, x, y, w, h, r); ctx.fill(); }
      else ctx.fillRect(x, y, w, h);
    };
    const dot = (x, y, r, style) => {
      ctx.fillStyle = style;
      if (hasPath && typeof ctx.arc === "function") { ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2); ctx.fill(); }
      else ctx.fillRect(x - r, y - r, r * 2, r * 2);
    };
    // Status bar + punch-hole camera.
    pill(20, 18, 34, 8, 4, "rgba(226, 234, 250, 0.78)");
    pill(196, 18, 40, 8, 4, "rgba(226, 234, 250, 0.62)");
    dot(128, 22, 7, "#000000");
    dot(128, 22, 3, "rgba(80, 110, 170, 0.55)");
    // Chat header.
    dot(36, 64, 14, "#7aa2ff");
    pill(58, 54, 92, 9, 4, "rgba(236, 242, 255, 0.92)");
    pill(58, 68, 60, 7, 3, "rgba(160, 176, 204, 0.6)");
    ctx.fillStyle = "rgba(255, 255, 255, 0.07)";
    ctx.fillRect(14, 92, 228, 1);
    // Incoming / outgoing bubbles.
    pill(18, 110, 168, 46, 16, "#1f2a40");
    pill(30, 124, 128, 7, 3, "rgba(232, 238, 250, 0.9)");
    pill(30, 138, 92, 7, 3, "rgba(232, 238, 250, 0.62)");
    pill(82, 172, 156, 40, 16, "#3b6fe0");
    pill(96, 186, 124, 7, 3, "rgba(255, 255, 255, 0.95)");
    pill(18, 228, 140, 34, 16, "#1f2a40");
    pill(30, 241, 104, 7, 3, "rgba(232, 238, 250, 0.82)");
    pill(118, 278, 120, 54, 16, "#f6e4ad");
    pill(132, 292, 84, 7, 3, "rgba(60, 50, 34, 0.9)");
    pill(132, 306, 58, 7, 3, "rgba(60, 50, 34, 0.7)");
    // Typing indicator.
    pill(18, 348, 62, 28, 14, "#1f2a40");
    dot(36, 362, 4, "rgba(200, 214, 240, 0.85)");
    dot(49, 362, 4, "rgba(200, 214, 240, 0.6)");
    dot(62, 362, 4, "rgba(200, 214, 240, 0.4)");
    // Composer + home indicator.
    pill(16, 448, 190, 30, 15, "rgba(255, 255, 255, 0.09)");
    dot(224, 463, 15, "#7aa2ff");
    pill(92, 496, 72, 5, 3, "rgba(255, 255, 255, 0.55)");
    return toTexture(THREE, canvas);
  }

  // Satin metallic back shell with brushed grain and a subtle logo disc.
  function makePhoneBackTexture(THREE) {
    if (typeof THREE?.CanvasTexture !== "function") return null;
    const c = makeCanvas(128, 256);
    if (!c) return null;
    const { canvas, ctx } = c;
    const grad = ctx.createLinearGradient(0, 0, 128, 256);
    grad.addColorStop(0.0, "#c8d2e6");
    grad.addColorStop(0.45, "#9eaac2");
    grad.addColorStop(1.0, "#76829c");
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, 128, 256);
    ctx.fillStyle = "rgba(255, 255, 255, 0.035)";
    for (let y = 2; y < 256; y += 3) ctx.fillRect(0, y, 128, 1);
    ctx.fillStyle = "rgba(255, 255, 255, 0.22)";
    if (typeof ctx.arc === "function" && typeof ctx.beginPath === "function") {
      ctx.beginPath();
      ctx.arc(64, 134, 16, 0, Math.PI * 2);
      ctx.fill();
    }
    return toTexture(THREE, canvas);
  }

  // Soft elliptical radial falloff used by prop contact shadows.
  function makeContactShadowTexture(THREE) {
    if (typeof THREE?.CanvasTexture !== "function") return null;
    const c = makeCanvas(128, 128);
    if (!c || typeof c.ctx.createRadialGradient !== "function") return null;
    const { canvas, ctx } = c;
    ctx.clearRect?.(0, 0, 128, 128);
    const g = ctx.createRadialGradient(64, 64, 4, 64, 64, 62);
    g.addColorStop(0.0, "rgba(0, 0, 0, 1)");
    g.addColorStop(0.45, "rgba(0, 0, 0, 0.55)");
    g.addColorStop(1.0, "rgba(0, 0, 0, 0)");
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, 128, 128);
    return toTexture(THREE, canvas);
  }

  Object.assign(C, {
    vrmStudio: {
      HEADROOM_RATIO,
      HEADROOM_MIN,
      HEADROOM_MAX,
      ENV_METALNESS_MIN,
      STUDIO_PRESETS,
      solveBustFraming,
      measureFraming,
      measureAlphaTopRow,
      resolveThemeMode,
      createStudioLighting,
      createPalmProbe,
      samplePalmProbe,
      measureSlotPoints,
      roundRectPath,
      makeLidTexture,
      makeEmblemGlowTexture,
      makePhoneScreenTexture,
      makePhoneBackTexture,
      makeContactShadowTexture
    }
  });
})(window.WsCompanion);
