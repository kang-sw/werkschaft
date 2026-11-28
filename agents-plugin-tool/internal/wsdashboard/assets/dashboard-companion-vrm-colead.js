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
  const VALID_BEATS = new Set([
    "auto", "typing", "glance-up", "working", "sleepy", "reading-left", "reading-right",
    "swap-out", "swap-in", "dizzy", "dizzy-shake", "dizzy-recover",
    "knock-startle", "annoyed-tap", "wakeup-jolt",
    "phone-buzz", "phone-pickup", "phone-typing", "phone-putdown", "phone-facedown-merong"
  ]);

  // Curated 6-color tasteful dark-UI-friendly session laptop palette.
  const SESSION_ACCENT_PALETTE = Object.freeze([
    Object.freeze({ id: "sapphire",  hex: "#5b9eff", rim: "#a5cbff" }),
    Object.freeze({ id: "lavender",  hex: "#a78bfa", rim: "#d2c3ff" }),
    Object.freeze({ id: "mint-teal", hex: "#34d399", rim: "#96f0ce" }),
    Object.freeze({ id: "amber",     hex: "#f59e0b", rim: "#fcd37a" }),
    Object.freeze({ id: "coral",     hex: "#fb7185", rim: "#ffb3bf" }),
    Object.freeze({ id: "ice-cyan",  hex: "#38bdf8", rim: "#9be3ff" })
  ]);

  const SWAP_OUT_DURATION = 0.30;
  const SWAP_IN_DURATION = 0.35;
  const SWAP_SETTLE_DURATION = 0.20;
  const SWAP_TOTAL_DURATION = SWAP_OUT_DURATION + SWAP_IN_DURATION + SWAP_SETTLE_DURATION; // 0.85s

  const DIZZY_WINDOW_SEC = 2.8;
  const DIZZY_TRIGGER_COUNT = 4;
  const DIZZY_SWAY_DURATION = 2.85;
  const DIZZY_RECOVER_DURATION = 0.75;
  const DIZZY_TOTAL_DURATION = DIZZY_SWAY_DURATION + DIZZY_RECOVER_DURATION; // 3.60s
  const DIZZY_COOLDOWN_SEC = 1.2;

  const KNOCK_WINDOW_SEC = 4.0;
  const KNOCK_ANNOYED_COUNT = 3;
  const KNOCK_STARTLE_DURATION = 1.05;
  const KNOCK_WAKEUP_DURATION = 1.35;
  const KNOCK_ANNOYED_DURATION = 1.85;
  const PHONE_PUTDOWN_DURATION = 0.72;
  const PHONE_MERONG_DURATION = 1.75;

  const DIZZY_STAR_COLORS = Object.freeze(["#fde68a", "#7dd3fc", "#fbcfe8"]);

  function clamp(v, lo, hi) {
    return v < lo ? lo : (v > hi ? hi : v);
  }

  function lerp(a, b, t) {
    return a + (b - a) * t;
  }

  function easeInCubic(t) {
    const u = clamp(t, 0, 1);
    return u * u * u;
  }

  function easeOutCubic(t) {
    const u = 1 - clamp(t, 0, 1);
    return 1 - u * u * u;
  }

  function easeInOutCubic(t) {
    const u = clamp(t, 0, 1);
    return u < 0.5 ? 4 * u * u * u : 1 - Math.pow(-2 * u + 2, 3) / 2;
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

  // ============================================================================
  // Crisp 2D Anime Dizzy FX (Spiral Swirls + Orbiting Pastel Stars)
  // ============================================================================
  // Computes safe, strictly-clamped 2D anchor positions inside the rail dock so
  // spirals and orbiting stars never clip against canvas edges or cover eyes.
  function computeDizzyLayout(opts = {}) {
    const w = Math.max(80, Number.isFinite(opts.width) ? opts.width : 340);
    const h = Math.max(80, Number.isFinite(opts.height) ? opts.height : 240);
    const headX = Number.isFinite(opts.headX) ? opts.headX : w * 0.5;
    const headY = Number.isFinite(opts.headY) ? opts.headY : h * 0.42;
    const headR = clamp(Number.isFinite(opts.headRadius) ? opts.headRadius : Math.min(w, h) * 0.19, 26, Math.min(w, h) * 0.28);

    const padX = 20;
    const padTop = 16;
    const padBottom = 30;
    const rightR = clamp(headR * 0.36, 13.5, 19);
    const leftR = clamp(headR * 0.26, 9.5, 14);

    // Float anime spirals cleanly beside & slightly above her temples (clear of cheeks & hair!).
    const rightSpiral = {
      x: clamp(headX + headR * 1.62, padX + rightR, w - padX - rightR),
      y: clamp(headY - headR * 0.62, padTop + rightR, h - padBottom - rightR),
      radius: rightR
    };
    const leftSpiral = {
      x: clamp(headX - headR * 1.58, padX + leftR, w - padX - leftR),
      y: clamp(headY - headR * 0.44, padTop + leftR, h - padBottom - leftR),
      radius: leftR
    };

    // Float orbiting star halo gracefully ABOVE her crown/hairline (never over forehead or eyes!).
    const rawCrownY = Number.isFinite(opts.crownY) ? opts.crownY : (headY - headR * 1.28);
    const haloCy = clamp(Math.min(rawCrownY - 6, headY - headR * 1.24), padTop + 11, h * 0.36);
    const maxRx = Math.max(32, Math.min(headX - 18, w - headX - 18));
    const haloRx = clamp(headR * 1.48, 42, Math.min(maxRx, w * 0.36));
    const haloRy = clamp(headR * 0.24, 9, Math.min(16, Math.max(9, haloCy - 10)));
    const haloCx = clamp(headX, haloRx + 14, w - haloRx - 14);

    return {
      width: w,
      height: h,
      rightSpiral,
      leftSpiral,
      halo: { cx: haloCx, cy: haloCy, rx: haloRx, ry: haloRy }
    };
  }

  function strokeAnimeSpiral(ctx, cx, cy, maxR, phaseRad, strokeColor, glowColor, lineWidth) {
    const turns = 2.35;
    const steps = 44;
    const maxTheta = turns * Math.PI * 2;
    ctx.save();
    ctx.translate(cx, cy);
    ctx.rotate(phaseRad);
    ctx.lineCap = "round";
    ctx.lineJoin = "round";

    const tracePath = () => {
      ctx.beginPath();
      for (let i = 0; i <= steps; i++) {
        const u = i / steps;
        const theta = u * maxTheta;
        const r = 1.6 + u * (maxR - 1.6);
        const px = Math.cos(theta) * r;
        const py = Math.sin(theta) * r;
        if (i === 0) ctx.moveTo(px, py);
        else ctx.lineTo(px, py);
      }
    };

    // Soft pastel outer glow pass.
    tracePath();
    ctx.strokeStyle = glowColor;
    ctx.lineWidth = lineWidth * 2.55;
    ctx.stroke();

    // Crisp luminous anime core stroke.
    tracePath();
    ctx.strokeStyle = strokeColor;
    ctx.lineWidth = lineWidth;
    ctx.stroke();
    ctx.restore();
  }

  function drawFourPointStar(ctx, x, y, outerR, innerR, rot, fill, glow) {
    ctx.save();
    ctx.translate(x, y);
    ctx.rotate(rot);

    // Subtle soft bloom disc behind the star.
    ctx.beginPath();
    ctx.arc(0, 0, outerR * 1.18, 0, Math.PI * 2);
    ctx.fillStyle = glow;
    ctx.fill();

    // Crisp 4-point anime sparkle star.
    ctx.beginPath();
    for (let i = 0; i < 8; i++) {
      const r = i % 2 === 0 ? outerR : innerR;
      const a = (i * Math.PI) / 4;
      const px = Math.cos(a) * r;
      const py = Math.sin(a) * r;
      if (i === 0) ctx.moveTo(px, py);
      else ctx.lineTo(px, py);
    }
    ctx.closePath();
    ctx.fillStyle = fill;
    ctx.fill();

    // Tiny bright ivory center dot.
    ctx.beginPath();
    ctx.arc(0, 0, Math.max(1.2, innerR * 0.65), 0, Math.PI * 2);
    ctx.fillStyle = "rgba(255, 255, 255, 0.92)";
    ctx.fill();
    ctx.restore();
  }

  function drawDizzyFx(ctx, opts = {}) {
    if (!ctx || typeof ctx.beginPath !== "function" || typeof ctx.translate !== "function") return null;
    const alpha = clamp(Number.isFinite(opts.alpha) ? opts.alpha : 0, 0, 1);
    const layout = computeDizzyLayout(opts);
    const t = Number.isFinite(opts.time) ? opts.time : 0;
    const w = layout.width;
    const h = layout.height;

    if (typeof ctx.clearRect === "function") ctx.clearRect(0, 0, w, h);
    if (alpha <= 0.002) return layout;

    ctx.save();
    ctx.globalAlpha = alpha;

    // 1. Delicate elliptical orbit swoosh arcs above her crown.
    const { cx, cy, rx, ry } = layout.halo;
    const orbitRot = t * 2.55;
    ctx.save();
    ctx.translate(cx, cy);
    ctx.rotate(-0.07);
    ctx.lineCap = "round";
    for (let k = 0; k < 2; k++) {
      const a0 = orbitRot + k * Math.PI + 0.15;
      const a1 = a0 + 1.25;
      ctx.beginPath();
      if (typeof ctx.ellipse === "function") {
        ctx.ellipse(0, 0, rx, ry, 0, a0, a1);
      } else {
        for (let s = 0; s <= 12; s++) {
          const a = lerp(a0, a1, s / 12);
          const px = Math.cos(a) * rx;
          const py = Math.sin(a) * ry;
          if (s === 0) ctx.moveTo(px, py);
          else ctx.lineTo(px, py);
        }
      }
      ctx.strokeStyle = k === 0 ? "rgba(253, 230, 138, 0.34)" : "rgba(125, 211, 252, 0.30)";
      ctx.lineWidth = 1.6;
      ctx.stroke();
    }
    ctx.restore();

    // 2. Primary pastel-gold/cyan anime spiral beside her upper-right temple.
    const pulseR = 1 + Math.sin(t * 5.0) * 0.07;
    strokeAnimeSpiral(
      ctx,
      layout.rightSpiral.x,
      layout.rightSpiral.y,
      layout.rightSpiral.radius * pulseR,
      t * 4.2,
      "#fde68a",
      "rgba(125, 211, 252, 0.34)",
      2.3
    );

    // 3. Smaller counter-rotating pastel-rose/lavender companion swirl on upper-left temple.
    const pulseL = 1 + Math.cos(t * 4.6 + 0.7) * 0.07;
    strokeAnimeSpiral(
      ctx,
      layout.leftSpiral.x,
      layout.leftSpiral.y,
      layout.leftSpiral.radius * pulseL,
      -t * 3.6 + 1.1,
      "#fbcfe8",
      "rgba(167, 139, 250, 0.32)",
      1.85
    );

    // 4. Three twinkling 4-point pastel stars orbiting her crown halo.
    const starGlows = [
      "rgba(253, 230, 138, 0.28)",
      "rgba(125, 211, 252, 0.28)",
      "rgba(251, 207, 232, 0.28)"
    ];
    for (let i = 0; i < 3; i++) {
      const a = orbitRot + (i * Math.PI * 2) / 3;
      const cosA = Math.cos(a);
      const sinA = Math.sin(a);
      const depthScale = 0.78 + (sinA + 1) * 0.22; // Slightly larger in front, softer behind
      const twinkle = 0.88 + Math.sin(t * 7.4 + i * 2.1) * 0.16;
      const sx = clamp(cx + cosA * rx, 14, w - 14);
      const sy = clamp(cy + sinA * ry - cosA * rx * 0.07, 14, h - 18);
      const outerR = (6.8 - i * 0.45) * depthScale * twinkle;
      drawFourPointStar(
        ctx,
        sx,
        sy,
        outerR,
        outerR * 0.34,
        t * 1.9 + i * 0.8,
        DIZZY_STAR_COLORS[i],
        starGlows[i]
      );
    }

    ctx.restore();
    return layout;
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

    // Top status pill / notch
    ctx.fillStyle = "rgba(180, 212, 255, 0.35)";
    ctx.fillRect(42, 10, 44, 6);

    // Bright chat notification banner card
    ctx.fillStyle = "rgba(122, 162, 255, 0.92)";
    ctx.fillRect(14, 42, 100, 46);
    ctx.fillStyle = "#ffffff";
    ctx.fillRect(24, 52, 54, 7);
    ctx.fillStyle = "rgba(255, 255, 255, 0.72)";
    ctx.fillRect(24, 66, 76, 6);

    // Warm chat reply bubble preview
    ctx.fillStyle = "rgba(246, 228, 173, 0.92)";
    ctx.fillRect(30, 106, 84, 34);
    ctx.fillStyle = "#3c3222";
    ctx.fillRect(40, 118, 60, 6);

    // Incoming companion bubble preview
    ctx.fillStyle = "rgba(226, 234, 250, 0.24)";
    ctx.fillRect(14, 154, 82, 34);
    ctx.fillStyle = "rgba(235, 242, 255, 0.8)";
    ctx.fillRect(24, 166, 56, 6);

    // Bottom home indicator bar
    ctx.fillStyle = "rgba(255, 255, 255, 0.45)";
    ctx.fillRect(42, 240, 44, 4);

    const tex = new THREE.CanvasTexture(canvas);
    if (THREE.SRGBColorSpace) tex.colorSpace = THREE.SRGBColorSpace;
    tex.needsUpdate = true;
    return tex;
  }

  // Paint a sleek metallic back-shell texture with subtle matte gradient so the
  // back side of the smartphone reads unmistakably apart from the OLED front.
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

    // Subtle frosted glass center accent mark
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

  // Draw crisp 2D canvas FX cue (buzz waves, merong sparkle, startle burst) cleanly
  // clamped inside the rail dock without covering the character's face.
  function drawActionFx(ctx, opts = {}) {
    if (!ctx || typeof ctx.fillText !== "function") return;
    const kind = opts.kind || "";
    const alpha = clamp(Number.isFinite(opts.alpha) ? opts.alpha : 0, 0, 1);
    if (!kind || alpha <= 0.01) return;
    const w = Math.max(80, Number.isFinite(opts.width) ? opts.width : 340);
    const h = Math.max(80, Number.isFinite(opts.height) ? opts.height : 240);
    const t = Number.isFinite(opts.time) ? opts.time : 0;

    ctx.save();
    ctx.globalAlpha = alpha;
    ctx.font = "800 13px system-ui, -apple-system, sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";

    let label = "";
    let bx = w * 0.76;
    let by = h * 0.68;
    let bg = "rgba(32, 36, 46, 0.90)";
    let fg = "#fde68a";
    let stroke = "rgba(122, 162, 255, 0.72)";

    if (kind === "phone-buzz") {
      label = "윙- ♪";
      bx = clamp(w * 0.80, 52, w - 42);
      by = clamp(h * 0.68 + Math.sin(t * 28) * 2.5, 34, h - 26);
      fg = "#93c5fd";
      stroke = "rgba(96, 165, 250, 0.85)";
      // Vibrating concentric wave arcs beside the desk phone
      ctx.strokeStyle = "rgba(125, 211, 252, 0.82)";
      ctx.lineWidth = 2;
      for (let i = 0; i < 2; i++) {
        const r = 10 + i * 7 + ((t * 24) % 8);
        ctx.beginPath();
        ctx.arc(bx - 2, by + 16, r, -Math.PI * 0.82, -Math.PI * 0.18);
        ctx.stroke();
      }
    } else if (kind === "phone-facedown-merong") {
      label = "메-롱 · 탁!";
      bx = clamp(w * 0.75, 58, w - 52);
      by = clamp(h * 0.24 + Math.sin(t * 5.2) * 2.5, 28, h - 32);
      fg = "#f9a8d4";
      stroke = "rgba(244, 143, 177, 0.82)";
    } else if (kind === "wakeup-jolt") {
      label = "화들짝!";
      bx = clamp(w * 0.76, 52, w - 46);
      by = clamp(h * 0.22, 28, h - 32);
      fg = "#fde68a";
      stroke = "rgba(251, 146, 60, 0.85)";
    } else if (kind === "annoyed-tap") {
      label = "톡톡톡!";
      bx = clamp(w * 0.76, 52, w - 46);
      by = clamp(h * 0.24, 28, h - 32);
      fg = "#fdba74";
      stroke = "rgba(245, 158, 11, 0.82)";
    } else if (kind === "knock-startle") {
      label = "!?";
      bx = clamp(w * 0.74, 42, w - 38);
      by = clamp(h * 0.22, 26, h - 32);
      fg = "#fde68a";
      stroke = "rgba(122, 162, 255, 0.80)";
    }

    if (label) {
      const padX = 10;
      const metrics = typeof ctx.measureText === "function" ? ctx.measureText(label) : { width: label.length * 9 };
      const pw = Math.max(36, (metrics.width || 44) + padX * 2);
      const ph = 24;
      const rx = clamp(bx - pw * 0.5, 8, w - pw - 8);
      const ry = clamp(by - ph * 0.5, 8, h - ph - 8);
      const r = 12;
      ctx.beginPath();
      ctx.moveTo(rx + r, ry);
      ctx.lineTo(rx + pw - r, ry);
      ctx.quadraticCurveTo(rx + pw, ry, rx + pw, ry + r);
      ctx.lineTo(rx + pw, ry + ph - r);
      ctx.quadraticCurveTo(rx + pw, ry + ph, rx + pw - r, ry + ph);
      ctx.lineTo(rx + r, ry + ph);
      ctx.quadraticCurveTo(rx, ry + ph, rx, ry + ph - r);
      ctx.lineTo(rx, ry + r);
      ctx.quadraticCurveTo(rx, ry, rx + r, ry);
      ctx.closePath();
      ctx.fillStyle = bg;
      ctx.fill();
      ctx.strokeStyle = stroke;
      ctx.lineWidth = 1.5;
      ctx.stroke();
      ctx.fillStyle = fg;
      ctx.fillText(label, rx + pw * 0.5, ry + ph * 0.5 + 0.5);
    }
    ctx.restore();
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
    const swapSweep = clamp(Number.isFinite(params.swapSweep) ? params.swapSweep : 0, -1, 1);
    const swapActiveW = clamp(params.swapWeight || 0, 0, 1);
    const dizzyW = clamp(params.dizzyWeight || 0, 0, 1);
    const dizzyPhaseT = Number.isFinite(params.dizzyTime) ? params.dizzyTime : t;
    const phoneHoldW = clamp(params.phoneHoldWeight || 0, 0, 1);
    const phoneTypingW = clamp(params.phoneTypingWeight || 0, 0, 1);
    const phoneMerongW = clamp(params.phoneMerongWeight || 0, 0, 1);
    const knockJoltW = clamp(params.knockJoltWeight || 0, 0, 1);
    const annoyedTapW = clamp(params.annoyedTapWeight || 0, 0, 1);
    const extraHeadRoll = Number.isFinite(params.extraHeadRoll) ? params.extraHeadRoll : 0;
    const tiltDir = params.tiltDir >= 0 ? 1 : -1;
    const headYaw = Number.isFinite(params.headYaw) ? params.headYaw : 0;
    const headPitch = Number.isFinite(params.headPitch) ? params.headPitch : 0;
    const headPitchCue = Number.isFinite(params.headPitchCue) ? params.headPitchCue : 0;

    // Gentle alive breathing (slower and deeper when sleepy).
    const breathFreq = 1.42 - sleepyW * 0.42 + workingW * 0.14;
    const breath = Math.sin(t * breathFreq);
    const sway = Math.sin(t * 0.68) * (1 - workingW * 0.45 - sleepyW * 0.3 - dizzyW * 0.8 - phoneHoldW * 0.65);

    // Organic multi-harmonic alternating left/right keystroke cadence (zero metronome feel).
    const keyMask = clamp(1 - phoneHoldW * 0.95 - knockJoltW * 0.9 - annoyedTapW * 0.9, 0, 1);
    const rate = 6.2 + workingW * 3.4;
    const tapL = (Math.sin(t * rate) * 0.62 + Math.sin(t * rate * 1.73 + 0.9) * 0.38) * typingW * keyMask;
    const tapR = (Math.sin(t * rate + Math.PI * 0.92) * 0.62 + Math.sin(t * rate * 1.61 + 2.4) * 0.38) * typingW * keyMask;
    const wristL = Math.sin(t * rate * 1.15 + 0.4) * typingW * keyMask;
    const wristR = Math.sin(t * rate * 1.15 + 3.4) * typingW * keyMask;

    // Cozy forward seated lean while typing; straightens crisply on knock startle or phone lift.
    const leanScale = clamp(1 - glanceW * 0.65 - reactionW * 0.45 - dizzyW * 0.50 - phoneHoldW * 0.55 - knockJoltW * 0.9, 0.10, 1);
    const dizzySpineYaw = Math.sin(dizzyPhaseT * 3.6) * 2.6 * DEG * dizzyW;
    const dizzySpineRoll = Math.cos(dizzyPhaseT * 3.6) * 2.4 * DEG * dizzyW;
    const swapTorsoYaw = swapSweep * 3.4 * DEG;
    const phoneTorsoYaw = 3.2 * DEG * phoneHoldW - 2.2 * DEG * annoyedTapW;
    const phoneTorsoRoll = -2.4 * DEG * phoneHoldW + 3.5 * DEG * phoneMerongW;
    const spinePitch = (0.036 + workingW * 0.014) * leanScale + sleepyW * 0.030 - knockJoltW * 0.055 + breath * 0.010;
    const chestPitch = (0.028 + workingW * 0.012) * leanScale + sleepyW * 0.022 - knockJoltW * 0.045 + breath * 0.015;
    const upperChestPitch = 0.014 * leanScale - knockJoltW * 0.025 + breath * 0.008;

    // Charming anime head-tilt on glance-up (+/-7.6 deg), figure-eight sway when dizzy,
    // plus gentle follow-through roll during laptop swaps.
    const glanceRoll = tiltDir * 0.132 * glanceW;
    const idleRoll = sway * 0.016 * (1 - glanceW) + Math.sin(t * 0.48) * 0.014 * workingW * (1 - glanceW);
    const sleepyNod = sleepyW * (0.125 + Math.sin(t * 0.82) * 0.035);

    const totalPitch = headPitch + headPitchCue + sleepyNod;
    const totalRoll = glanceRoll + extraHeadRoll;
    const neckPitch = totalPitch * 0.40;
    const craniumPitch = totalPitch * 0.60;
    const neckYaw = headYaw * 0.36;
    const craniumYaw = headYaw * 0.64 + sway * 0.012 * (1 - glanceW - dizzyW * 0.8);
    const neckRoll = totalRoll * 0.35;
    const craniumRoll = totalRoll * 0.65 + idleRoll * (1 - dizzyW - phoneMerongW * 0.8);

    // Relaxed lowered shoulders rolled subtly forward toward her desk, lifting gently
    // on knock jolt or when raising her hand with the smartphone.
    const shForward = 0.048 * (1 - sleepyW * 0.45 - dizzyW * 0.28);
    const shDrop = 0.046 + sleepyW * 0.018;
    const dizzyShWave = Math.sin(dizzyPhaseT * 3.6 + 0.6) * 0.034 * dizzyW;
    const swapShLiftL = swapSweep * 0.032;
    const swapShLiftR = -swapSweep * 0.032;
    const joltShLift = knockJoltW * 0.095;
    const lSh = [
      0.012 + tapL * 0.006 + phoneHoldW * 0.028,
      -shForward + swapSweep * 0.030 - phoneHoldW * 0.048,
      -shDrop + breath * 0.012 + tapL * 0.007 + dizzyShWave + swapShLiftL + joltShLift + phoneHoldW * 0.072 + annoyedTapW * 0.052
    ];
    const rSh = [
      0.012 + tapR * 0.006,
      shForward + swapSweep * 0.030,
      shDrop - breath * 0.012 - tapR * 0.007 + dizzyShWave + swapShLiftR - joltShLift
    ];

    // Upper arms stay naturally lowered along her sides (|z| >= 1.18) while typing,
    // and smoothly lift her phone-side arm (character-left / viewer-right) when holding
    // the smartphone or playfully tapping the glass back at the user.
    const activePoseBlend = clamp(1 - sleepyW * 0.55 - dizzyW * 0.25, 0.35, 1);
    const uPitch = -0.17 * activePoseBlend - workingW * 0.025 + glanceW * 0.03 + dizzyW * 0.05;
    const uYaw = 0.12 * activePoseBlend;
    const uDrop = 1.22 + sleepyW * 0.06 + dizzyW * 0.03;

    const thumbBob = Math.sin(t * 10.5) * 0.022 * phoneTypingW;
    const tapGlassPulse = Math.sin(t * 11.0) * 0.09 * annoyedTapW;

    const lUpperBase = [
      uPitch + tapL * 0.015 - Math.max(0, swapSweep) * 0.05 - knockJoltW * 0.12,
      -uYaw + tapL * 0.010 + swapSweep * 0.09,
      -uDrop - breath * 0.008 + swapActiveW * 0.02 + knockJoltW * 0.14
    ];
    const lUpperPhone = [-0.58 + thumbBob * 0.4, -0.14, -0.82 + thumbBob * 0.3];
    const lUpperTap = [-0.46 + tapGlassPulse * 0.35, -0.18, -0.84];
    const lUpper = [
      lerp(lerp(lUpperBase[0], lUpperPhone[0], phoneHoldW), lUpperTap[0], annoyedTapW),
      lerp(lerp(lUpperBase[1], lUpperPhone[1], phoneHoldW), lUpperTap[1], annoyedTapW),
      lerp(lerp(lUpperBase[2], lUpperPhone[2], phoneHoldW), lUpperTap[2], annoyedTapW)
    ];

    // Non-phone arm relaxes softly onto desk/lap while holding phone, and lifts slightly
    // to steady/thumb-type when actively replying on the smartphone.
    const rUpperBase = [
      uPitch * (1 - phoneHoldW * 0.45) + tapR * 0.015 - Math.max(0, -swapSweep) * 0.05 - knockJoltW * 0.12,
      uYaw * (1 - phoneHoldW * 0.35) - tapR * 0.010 + swapSweep * 0.09,
      uDrop + breath * 0.008 - swapActiveW * 0.02 - knockJoltW * 0.14 + phoneHoldW * 0.05
    ];
    const rUpperType = [-0.34 - thumbBob * 0.35, 0.22, 1.04];
    const rUpper = [
      lerp(rUpperBase[0], rUpperType[0], phoneTypingW * 0.85),
      lerp(rUpperBase[1], rUpperType[1], phoneTypingW * 0.85),
      lerp(rUpperBase[2], rUpperType[2], phoneTypingW * 0.85)
    ];

    // Forearms flex cleanly forward onto keyboard, or flex up smoothly to hold smartphone.
    const fFlex = 1.12 * activePoseBlend + sleepyW * 0.28 - glanceW * 0.10 - dizzyW * 0.16 - swapActiveW * 0.08;
    const fInward = 0.28 * activePoseBlend + sleepyW * 0.06;
    const fTwist = 0.16 * activePoseBlend;

    const lLowerBase = [
      fTwist + tapL * 0.030,
      -(fFlex + tapL * 0.045 + knockJoltW * 0.18),
      -(fInward + tapL * 0.018 - swapSweep * 0.06)
    ];
    const lLowerPhone = [0.56 + thumbBob, -(1.26 + thumbBob * 0.65), -0.28];
    const lLowerTap = [0.22, -(1.56 + tapGlassPulse), -0.12];
    const lLower = [
      lerp(lerp(lLowerBase[0], lLowerPhone[0], phoneHoldW), lLowerTap[0], annoyedTapW),
      lerp(lerp(lLowerBase[1], lLowerPhone[1], phoneHoldW), lLowerTap[1], annoyedTapW),
      lerp(lerp(lLowerBase[2], lLowerPhone[2], phoneHoldW), lLowerTap[2], annoyedTapW)
    ];

    const rLowerBase = [
      fTwist * (1 - phoneHoldW * 0.4) + tapR * 0.030,
      fFlex * (1 - phoneHoldW * 0.35) + tapR * 0.045 + knockJoltW * 0.18,
      fInward * (1 - phoneHoldW * 0.4) + tapR * 0.018 + swapSweep * 0.06
    ];
    const rLowerType = [0.32 - thumbBob * 0.8, 1.38 + thumbBob * 0.9, 0.26];
    const rLower = [
      lerp(rLowerBase[0], rLowerType[0], phoneTypingW * 0.85),
      lerp(rLowerBase[1], rLowerType[1], phoneTypingW * 0.85),
      lerp(rLowerBase[2], rLowerType[2], phoneTypingW * 0.85)
    ];

    // Delicate alternating wrist/hand key-tap articulation or phone thumb-typing micro-motion.
    const lHandBase = [0.08 + wristL * 0.055, 0.05 + tapL * 0.035, 0.10 + wristL * 0.04];
    const lHandPhone = [0.18 + thumbBob * 1.4, 0.12 - thumbBob * 0.8, 0.24];
    const lHand = [
      lerp(lHandBase[0], lHandPhone[0], phoneHoldW),
      lerp(lHandBase[1], lHandPhone[1], phoneHoldW),
      lerp(lHandBase[2], lHandPhone[2], phoneHoldW)
    ];
    const rHandBase = [0.08 + wristR * 0.055, -(0.05 + tapR * 0.035), -(0.10 + wristR * 0.04)];
    const rHandType = [0.16 - thumbBob * 1.6, -0.14 + thumbBob * 0.9, -0.18];
    const rHand = [
      lerp(rHandBase[0], rHandType[0], phoneTypingW * 0.85),
      lerp(rHandBase[1], rHandType[1], phoneTypingW * 0.85),
      lerp(rHandBase[2], rHandType[2], phoneTypingW * 0.85)
    ];

    return {
      spine: [spinePitch, sway * 0.009 * (1 - glanceW * 0.5) + dizzySpineYaw + swapTorsoYaw + phoneTorsoYaw, dizzySpineRoll + phoneTorsoRoll],
      chest: [chestPitch, swapTorsoYaw * 0.65 + phoneTorsoYaw * 0.55, dizzySpineRoll * 0.55 + phoneTorsoRoll * 0.5],
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
    let targetAccentHex = DEFAULT_ACCENT;
    let accentId = "sapphire";
    let activeSessionId = "";
    let activeSessionMeta = null;
    let overrideBeat = "auto";
    let explicitHover = null;

    // Monotonic clock bridging simulated step(dt) and live browser wall time.
    let simClockSec = 0;
    let lastStepWallMs = 0;
    let lastObservedSec = 0;

    // Rhythm state machine.
    let phase = "typing"; // 'typing' | 'reading' | 'glance-up'
    let phaseTimer = 2.1;
    let phaseCycle = 0;
    let timeSinceGlance = 0;
    let nextGlanceInterval = 8.4;
    let glanceTiltDir = 1;
    let lastActivity = "idle";

    // Session-switch laptop swap choreography (~0.85s, smoothly interruptible).
    let swapActive = false;
    let swapElapsed = 0;
    let swapOutDuration = SWAP_OUT_DURATION;
    let swapDir = 1; // alternates +1 / -1 per swap
    let swapSwitchedColor = false;
    let swapStartPose = { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 };
    let liveLaptopOffset = { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 };

    // Rapid-switch sliding-window dizzy easter egg (>=4 distinct switches within 2.8s).
    const switchTimestamps = [];
    let dizzyPhase = "none"; // 'none' | 'sway' | 'recover'
    let dizzyElapsed = 0;
    let dizzyCooldownUntil = -Infinity;
    let dizzyFxAlpha = 0;

    // Cosmetic glass knock state machine (awake startle, 3+ rapid -> annoyed-tap, sleepy -> wakeup-jolt).
    const knockTimestamps = [];
    let knockKind = ""; // '' | 'knock-startle' | 'wakeup-jolt' | 'annoyed-tap'
    let knockElapsed = 0;
    let knockDuration = 0;
    let knockCount = 0;

    // Smartphone 3D prop & 4-state choreography state.
    let phoneMode = "desk"; // 'desk' | 'buzz' | 'pickup' | 'typing' | 'putdown' | 'facedown-merong'
    let phoneFaceDown = false;
    let phoneAnimElapsed = 0;
    let phoneUnreadAgent = 0;
    let phoneLastSnapState = "laptop_ambient";
    // Server-confirmed hold: null until the first snapshot, then whether the
    // latest snapshot said the phone is held. A pickup/typing pose the server
    // does not confirm within PHONE_UNCONFIRMED_GRACE is put back down, so a
    // lost putdown command can never strand the phone in hand.
    let phoneSnapHeld = null;
    let phoneUnconfirmedElapsed = 0;
    const PHONE_UNCONFIRMED_GRACE = 4;
    let phoneScreenGlow = 0.14;
    let phoneHoldWeight = 0;
    let phoneTypingWeight = 0;
    let phoneMerongWeight = 0;
    let knockJoltWeight = 0;
    let annoyedTapWeight = 0;
    let actionFxKind = "";
    let actionFxAlpha = 0;

    // Smoothly damped blending weights (all frame-rate independent).
    let typingWeight = 1.0;
    let glanceWeight = 0.0;
    let sleepyWeight = 0.0;
    let reactionWeight = 0.0;
    let workingWeight = 0.0;
    let swapWeight = 0.0;
    let dizzyWeight = 0.0;
    let screenGlow = 0.13;
    let bounceColorRgb = hexToRgb(DEFAULT_ACCENT);

    // 3D foreground laptop prop + minimalist 3D desk smartphone prop + soft bounce light.
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

    // Cached resting prop transform & framing telemetry (zero per-frame allocations).
    let lastFraming = null;
    let basePos = { x: 0, y: 0, z: 0 };
    let baseScale = 0.4;
    let baseVisH = 0.85;
    let baseVisHalfW = 0.45;
    let phoneBasePos = { x: 0.25, y: 0, z: 0 };
    let livePhonePose = { x: 0.25, y: 0, z: 0, rx: -48 * DEG, ry: -12 * DEG, rz: -6 * DEG };
    let phonePoseInitialized = false;
    let lidTopWorldY = 0;
    let lidTopWorldZ = 0;
    let lidTopViewportPct = 0;
    let chinViewportPct = 0;
    let headViewportPct = 0;
    const scratchVec = THREE && typeof THREE.Vector3 === "function" ? new THREE.Vector3() : null;

    const LID_TILT_RAD = 11 * DEG; // Top of open lid tilts gently toward viewer (+Z).

    function readWallMs() {
      if (typeof performance !== "undefined" && typeof performance.now === "function") {
        const ms = performance.now();
        if (Number.isFinite(ms) && ms > 0) return ms;
      }
      return 0;
    }

    function currentClockSec(overrideMs) {
      if (Number.isFinite(overrideMs)) {
        const sec = overrideMs / 1000;
        lastObservedSec = Math.max(lastObservedSec, sec);
        return lastObservedSec;
      }
      const wallMs = readWallMs();
      let wallDelta = 0;
      if (wallMs > 0 && lastStepWallMs > 0 && wallMs > lastStepWallMs) {
        wallDelta = Math.min((wallMs - lastStepWallMs) / 1000, 5.0);
      } else if (wallMs > 0 && lastStepWallMs === 0 && simClockSec === 0) {
        lastStepWallMs = wallMs;
      }
      const next = simClockSec + wallDelta;
      lastObservedSec = Math.max(lastObservedSec, next);
      return lastObservedSec;
    }

    function pruneSwitchWindow(nowSec) {
      while (switchTimestamps.length && nowSec - switchTimestamps[0] > DIZZY_WINDOW_SEC) {
        switchTimestamps.shift();
      }
    }

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

    function applyAccentVisuals(immediateBounce = true) {
      const rgb = hexToRgb(accentHex);
      if (emblemMesh?.material?.color?.setRGB) {
        emblemMesh.material.color.setRGB(rgb[0] / 255, rgb[1] / 255, rgb[2] / 255);
      }
      if (rimMesh?.material?.color?.setHex) {
        // Sleek 58% session accent tint blended over machined cool silver rim catchlight.
        rimMesh.material.color.setHex(mixRgbToHex([192, 206, 228], rgb, 0.58));
      }
      if (lidMesh?.material?.emissive?.setHex) {
        // Subtle 13% anodized session sheen over dark slate #0b0e14 so lid stays classy yet distinct.
        lidMesh.material.emissive.setHex(mixRgbToHex([11, 14, 20], rgb, 0.13));
      }
      if (immediateBounce) {
        bounceColorRgb = rgb;
      }
      if (screenLight?.color?.setHex) {
        // Blend 26% of session accent over flattering cool-ivory so anime skin stays rosy & cute.
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

        // Minimalist center-upper soft-glowing accent emblem pill (per-session themed!).
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
        // Positioned in the upper bezel zone so it stays crisply visible above the bottom dissolve mask on all avatars.
        emblemMesh.position.set(0, 0.266, 0.0145);
        emblemMesh.frustumCulled = false;
        laptopGroup.add(emblemMesh);
      }

      laptopGroup.rotation.set(LID_TILT_RAD, 0, 0, "XYZ");
      scene.add(laptopGroup);

      // Minimalist 3D desk smartphone prop ("ws-colead-phone") resting beside the
      // laptop on the viewer's right (+X), with an unmistakable OLED front screen
      // (+Z) and matte titanium back shell + raised dual-lens camera island (-Z).
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
        // Front OLED screen (+Z face)
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

        // Soft pulsing notification halo around front screen during phone_buzz
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

        // Matte light-titanium back panel (-Z face) so face-down stands out cleanly
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

      // Raised dark-charcoal camera module island + dual chrome lenses on the BACK (-Z)
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
      applyAccentVisuals();
    }

    function applyPhoneTransform() {
      if (!phoneGroup) return;
      phoneGroup.position.set(livePhonePose.x, livePhonePose.y, livePhonePose.z);
      phoneGroup.rotation.set(livePhonePose.rx, livePhonePose.ry, livePhonePose.rz, "XYZ");
    }

    // Anchor the held 3D smartphone cleanly to the avatar's actual posed left palm
    // world position (post-vrm.update) so both VRM 1.0 and VRM 0.x physically hold
    // the phone in hand with zero floating gap regardless of arm length.
    function syncPhoneToHand(handWorldPos) {
      if (!phoneGroup || !handWorldPos || phoneHoldWeight <= 0.01) return;
      if (!Number.isFinite(handWorldPos.x) || !Number.isFinite(handWorldPos.y) || !Number.isFinite(handWorldPos.z)) return;
      const holdBlend = easeInOutCubic(clamp(phoneHoldWeight, 0, 1));
      const anchorX = handWorldPos.x + baseScale * 0.012;
      const anchorY = Math.max(handWorldPos.y + baseScale * 0.145, lidTopWorldY + baseScale * 0.13);
      // Sit tucked right inside her palm/fingers while staying clearly in front of torso.
      const anchorZ = Math.max(handWorldPos.z + baseScale * 0.025, basePos.z - baseScale * 0.24);
      phoneGroup.position.set(
        lerp(livePhonePose.x, anchorX, holdBlend),
        lerp(livePhonePose.y, anchorY, holdBlend),
        lerp(livePhonePose.z, anchorZ, holdBlend)
      );
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
      basePos = { x: centerX, y: centerY, z: laptopZ };
      baseScale = lidScale;
      baseVisH = visH;
      baseVisHalfW = visHalfW;

      // Resting desk position for the smartphone: right beside the laptop lid
      // on the viewer's right (+X), strictly clamped inside the viewport.
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
            ry: phoneFaceDown ? 168 * DEG : -12 * DEG,
            rz: -6 * DEG
          };
          phonePoseInitialized = true;
        }
        applyPhoneTransform();
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

      simClockSec += dt;
      const wallMs = readWallMs();
      if (wallMs > 0) lastStepWallMs = wallMs;
      lastObservedSec = Math.max(lastObservedSec, simClockSec);
      pruneSwitchWindow(lastObservedSec);

      let activity = classifyActivity(ctx);
      if (overrideBeat === "working") activity = "working";
      else if (overrideBeat === "sleepy") activity = "sleepy";
      else if (
        overrideBeat === "typing" || overrideBeat === "glance-up" ||
        overrideBeat === "reading-left" || overrideBeat === "reading-right" ||
        overrideBeat === "swap-out" || overrideBeat === "swap-in" ||
        overrideBeat === "dizzy" || overrideBeat === "dizzy-shake" || overrideBeat === "dizzy-recover" ||
        overrideBeat === "knock-startle" || overrideBeat === "annoyed-tap" || overrideBeat === "wakeup-jolt" ||
        overrideBeat === "phone-buzz" || overrideBeat === "phone-pickup" || overrideBeat === "phone-typing" ||
        overrideBeat === "phone-putdown" || overrideBeat === "phone-facedown-merong"
      ) {
        activity = "idle";
      }

      // Advance active knock / phone / dizzy / laptop swap timelines when in auto mode.
      if (overrideBeat === "auto") {
        if (knockKind) {
          knockElapsed += dt;
          if (knockElapsed >= knockDuration) {
            knockKind = "";
            knockElapsed = 0;
            knockDuration = 0;
          }
        }
        if (phoneMode === "pickup" || phoneMode === "putdown" || phoneMode === "facedown-merong") {
          phoneAnimElapsed += dt;
          if (phoneMode === "pickup" && phoneAnimElapsed >= 0.45) {
            phoneMode = "typing";
            phoneAnimElapsed = 0;
          } else if (phoneMode === "putdown" && phoneAnimElapsed >= PHONE_PUTDOWN_DURATION) {
            phoneMode = phoneUnreadAgent > 0 ? "buzz" : "desk";
            phoneAnimElapsed = 0;
          } else if (phoneMode === "facedown-merong" && phoneAnimElapsed >= PHONE_MERONG_DURATION) {
            phoneMode = phoneUnreadAgent > 0 ? "buzz" : "desk";
            phoneAnimElapsed = 0;
          }
        }
        if (phoneSnapHeld === false && (phoneMode === "pickup" || phoneMode === "typing")) {
          phoneUnconfirmedElapsed += dt;
          if (phoneUnconfirmedElapsed >= PHONE_UNCONFIRMED_GRACE) {
            phoneMode = phoneFaceDown ? "facedown-merong" : "putdown";
            phoneAnimElapsed = 0;
            phoneUnconfirmedElapsed = 0;
          }
        } else {
          phoneUnconfirmedElapsed = 0;
        }
        if (dizzyPhase !== "none") {
          dizzyElapsed += dt;
          if (dizzyPhase === "sway" && dizzyElapsed >= DIZZY_SWAY_DURATION) {
            dizzyPhase = "recover";
            dizzyElapsed = 0;
            // Ensure active session's accent is applied cleanly for recovery.
            if (accentHex !== targetAccentHex) {
              accentHex = targetAccentHex;
              applyAccentVisuals(false);
            }
          } else if (dizzyPhase === "recover" && dizzyElapsed >= DIZZY_RECOVER_DURATION) {
            dizzyPhase = "none";
            dizzyElapsed = 0;
            dizzyCooldownUntil = lastObservedSec + DIZZY_COOLDOWN_SEC;
            phase = "typing";
            phaseTimer = 1.8;
          }
        } else if (swapActive) {
          swapElapsed += dt;
          if (!swapSwitchedColor && swapElapsed >= swapOutDuration) {
            swapSwitchedColor = true;
            accentHex = targetAccentHex;
            applyAccentVisuals(false);
          }
          const totalDur = swapOutDuration + SWAP_IN_DURATION + SWAP_SETTLE_DURATION;
          if (swapElapsed >= totalDur) {
            swapActive = false;
            swapElapsed = 0;
            swapOutDuration = SWAP_OUT_DURATION;
            phase = "typing";
            phaseTimer = 1.8;
          }
        }
      }

      const forcedSwapOut = overrideBeat === "swap-out";
      const forcedSwapIn = overrideBeat === "swap-in";
      const forcedDizzy = overrideBeat === "dizzy";
      const forcedDizzyShake = overrideBeat === "dizzy-shake";
      const forcedDizzyRecover = overrideBeat === "dizzy-recover";
      const forcedKnockStartle = overrideBeat === "knock-startle";
      const forcedWakeupJolt = overrideBeat === "wakeup-jolt";
      const forcedAnnoyedTap = overrideBeat === "annoyed-tap";
      const forcedPhoneBuzz = overrideBeat === "phone-buzz";
      const forcedPhonePickup = overrideBeat === "phone-pickup";
      const forcedPhoneTyping = overrideBeat === "phone-typing";
      const forcedPhonePutdown = overrideBeat === "phone-putdown";
      const forcedPhoneMerong = overrideBeat === "phone-facedown-merong";

      const inDizzySway = forcedDizzy || (overrideBeat === "auto" && dizzyPhase === "sway");
      const inDizzyShake = forcedDizzyShake || (overrideBeat === "auto" && dizzyPhase === "recover" && dizzyElapsed < DIZZY_RECOVER_DURATION * 0.48);
      const inDizzyRecover = forcedDizzyRecover || (overrideBeat === "auto" && dizzyPhase === "recover" && dizzyElapsed >= DIZZY_RECOVER_DURATION * 0.48);
      const inAnyDizzy = inDizzySway || inDizzyShake || inDizzyRecover;

      const activeKnockKind = forcedKnockStartle ? "knock-startle"
        : forcedWakeupJolt ? "wakeup-jolt"
          : forcedAnnoyedTap ? "annoyed-tap"
            : (overrideBeat === "auto" ? knockKind : "");
      const inAnyKnock = !inAnyDizzy && !!activeKnockKind;

      const activePhoneStage = forcedPhoneBuzz ? "buzz"
        : forcedPhonePickup ? "pickup"
          : forcedPhoneTyping ? "typing"
            : forcedPhonePutdown ? "putdown"
              : forcedPhoneMerong ? "facedown-merong"
                : (overrideBeat === "auto" ? phoneMode : "desk");
      const inPhoneAction = !inAnyDizzy && !inAnyKnock && activePhoneStage !== "desk";
      const inSwap = !inAnyDizzy && !inAnyKnock && !inPhoneAction && (forcedSwapOut || forcedSwapIn || (overrideBeat === "auto" && swapActive));

      if (!inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap) {
        advanceRhythm(dt, activity, hovered);
      }

      const wantPhoneHold = !inAnyDizzy && !inAnyKnock && (
        activePhoneStage === "pickup" || activePhoneStage === "typing" ||
        (activePhoneStage === "facedown-merong" && !forcedPhoneMerong && phoneAnimElapsed < PHONE_MERONG_DURATION * 0.48)
      );
      const wantPhoneTyping = !inAnyDizzy && !inAnyKnock && activePhoneStage === "typing";
      const wantPhoneMerong = !inAnyDizzy && !inAnyKnock && activePhoneStage === "facedown-merong";
      const wantKnockJolt = activeKnockKind === "knock-startle" || activeKnockKind === "wakeup-jolt";
      const wantAnnoyedTap = activeKnockKind === "annoyed-tap";

      const wantGlance = !inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap && (overrideBeat === "glance-up" || (overrideBeat === "auto" && activity === "idle" && (hovered || phase === "glance-up")));
      const wantSleepy = !inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap && activity === "sleepy";
      const wantReaction = !inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap && activity === "reaction";
      const wantWorking = !inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap && activity === "working";
      const wantTyping = !inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap && !wantGlance && !wantSleepy && !wantReaction && (
        overrideBeat === "typing" ||
        overrideBeat === "working" ||
        (overrideBeat === "auto" && phase === "typing")
      );

      typingWeight = damp(typingWeight, wantTyping ? 1 : 0, wantTyping ? 6.5 : 10.0, dt);
      glanceWeight = damp(glanceWeight, wantGlance ? 1 : 0, wantGlance ? 5.8 : 5.2, dt);
      sleepyWeight = damp(sleepyWeight, wantSleepy ? 1 : 0, 3.6, dt);
      reactionWeight = damp(reactionWeight, wantReaction ? 1 : 0, 8.0, dt);
      workingWeight = damp(workingWeight, wantWorking ? 1 : 0, 5.2, dt);
      swapWeight = damp(swapWeight, inSwap ? 1 : 0, inSwap ? 11.0 : 8.5, dt);
      dizzyWeight = damp(dizzyWeight, inDizzySway ? 1 : 0, inDizzySway ? 7.5 : 9.0, dt);
      phoneHoldWeight = damp(phoneHoldWeight, wantPhoneHold ? 1 : 0, wantPhoneHold ? 8.5 : 9.5, dt);
      phoneTypingWeight = damp(phoneTypingWeight, wantPhoneTyping ? 1 : 0, 8.0, dt);
      phoneMerongWeight = damp(phoneMerongWeight, wantPhoneMerong ? 1 : 0, 9.5, dt);
      knockJoltWeight = damp(knockJoltWeight, wantKnockJolt ? 1 : 0, wantKnockJolt ? 16.0 : 9.0, dt);
      annoyedTapWeight = damp(annoyedTapWeight, wantAnnoyedTap ? 1 : 0, 10.0, dt);

      // Compute laptop animated offset, head/eye choreography, and expression overlays.
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
      let extraHeadRoll = 0;
      let swapSweep = 0;
      if (reactionWeight > 0.01) {
        wantHeadYaw = wantHeadYaw * (1 - reactionWeight) + reactionYaw * reactionWeight;
        wantHeadPitch = wantHeadPitch * (1 - reactionWeight) + reactionPitch * reactionWeight;
      }
      if (sleepyWeight > 0.01) {
        wantHeadYaw *= (1 - sleepyWeight * 0.82);
        wantHeadPitch *= (1 - sleepyWeight * 0.85);
      }

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

      // Compute swap / dizzy target laptop offset, head choreography, FX alpha, & expression overlay.
      let targetOffset = { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 };
      let immediateOffset = false;
      let swapStage = "none"; // 'none' | 'swap-out' | 'swap-in' | 'swap-settle'
      let swapProgress = 0;
      let dizzyTime = elapsed;
      let targetDizzyFxAlpha = 0;
      let exprOverlay = null;
      let glowMultiplier = 1.0;

      if (inAnyDizzy) {
        if (inDizzySway) {
          dizzyTime = forcedDizzy ? (elapsed > 0.01 ? elapsed : 0.65) : dizzyElapsed;
          const env = forcedDizzy ? 1.0 : clamp(dizzyElapsed / 0.28, 0, 1);
          const swayYaw = Math.sin(dizzyTime * 3.6) * 8.8 * DEG * env;
          const swayRoll = Math.cos(dizzyTime * 3.6) * 8.5 * DEG * env;
          const swayPitch = (3.4 + Math.sin(dizzyTime * 7.2) * 3.0) * DEG * env;
          wantHeadYaw = swayYaw;
          wantHeadPitch = swayPitch;
          extraHeadRoll = swayRoll;
          eyeYaw = Math.sin(dizzyTime * 4.4) * 11.5 * DEG * env;
          eyePitch = (2.0 + Math.cos(dizzyTime * 4.4) * 5.8) * DEG * env;
          targetOffset = {
            dx: -baseScale * 0.03 * env,
            dy: -baseVisH * 0.062 * env,
            dz: -baseScale * 0.01 * env,
            rx: 3.5 * DEG * env,
            ry: 0,
            rz: -5.2 * DEG * env
          };
          targetDizzyFxAlpha = env;
          glowMultiplier = 0.48;
          // Adorable flustered anime blend: gentle surprised + worried eyebrows + soft flutter blink.
          const flutter = 0.14 + 0.14 * (0.5 + 0.5 * Math.sin(dizzyTime * 5.2));
          exprOverlay = {
            mode: "dizzy",
            surprised: 0.36 * env,
            sad: 0.22 * env,
            relaxed: 0.10 * env,
            blink: flutter * env,
            allowBlink: false
          };
        } else {
          // Recovery beat: Phase 1 (0..0.48) cute double-blink + brisk head-shake;
          // Phase 2 (0.48..1.0) sheepish warm smile + laptop rises back smoothly!
          let r;
          if (forcedDizzyShake) r = 0.24;
          else if (forcedDizzyRecover) r = 0.78;
          else r = clamp(dizzyElapsed / DIZZY_RECOVER_DURATION, 0, 1);

          const liftE = easeOutCubic(clamp(r / 0.78, 0, 1));
          targetOffset = {
            dx: lerp(-baseScale * 0.03, 0, liftE),
            dy: lerp(-baseVisH * 0.062, 0, liftE),
            dz: lerp(-baseScale * 0.01, 0, liftE),
            rx: lerp(3.5 * DEG, 0, liftE),
            ry: 0,
            rz: lerp(-5.2 * DEG, 0, liftE)
          };
          targetDizzyFxAlpha = clamp(1 - r / 0.34, 0, 1);

          if (r < 0.48) {
            // Quick cute double-blink + brisk little head-shake to clear her head!
            const u = r / 0.48;
            const shakeYaw = Math.sin(u * Math.PI * 2.5) * 8.2 * DEG * (1 - u * 0.35);
            const shakeRoll = Math.sin(u * Math.PI * 2.0) * 3.8 * DEG * (1 - u * 0.4);
            const blink1 = Math.exp(-Math.pow((u - 0.26) / 0.14, 2));
            const blink2 = Math.exp(-Math.pow((u - 0.72) / 0.14, 2));
            const dblBlink = forcedDizzyShake ? 0.78 : clamp(Math.max(blink1, blink2) * 0.88, 0, 0.9);
            wantHeadYaw = forcedDizzyShake ? 7.4 * DEG : shakeYaw;
            wantHeadPitch = 2.5 * DEG;
            extraHeadRoll = forcedDizzyShake ? -4.2 * DEG : shakeRoll;
            eyeYaw = 0;
            eyePitch = 0;
            glowMultiplier = lerp(0.52, 0.92, u);
            exprOverlay = {
              mode: "dizzy-shake",
              blink: dblBlink,
              sad: 0.14 * (1 - u),
              relaxed: 0.18 + 0.12 * u,
              happy: 0.14 * u,
              allowBlink: false
            };
          } else {
            // Adorable sheepish warm smile with subtle head tilt looking warmly toward user!
            const u = (r - 0.48) / 0.52;
            const smileEnv = forcedDizzyRecover ? 1.0 : Math.sin(clamp(u, 0, 1) * Math.PI * 0.85 + 0.15);
            wantHeadYaw = gx * 3.0 * DEG;
            wantHeadPitch = lerp(0.5 * DEG, 4.2 * DEG, forcedDizzyRecover ? 0.22 : u);
            extraHeadRoll = 7.5 * DEG * smileEnv;
            eyeYaw = gx * 5 * DEG;
            eyePitch = lerp(1.8 * DEG, -6.5 * DEG, forcedDizzyRecover ? 0.12 : u);
            glowMultiplier = 1.0;
            exprOverlay = {
              mode: "dizzy-recover",
              happy: 0.54 * smileEnv,
              relaxed: 0.22 * smileEnv,
              allowBlink: true
            };
          }
        }
      } else if (inSwap) {
        const outDur = forcedSwapOut || forcedSwapIn ? SWAP_OUT_DURATION : swapOutDuration;
        const totalDur = outDur + SWAP_IN_DURATION + SWAP_SETTLE_DURATION;
        const tSwap = forcedSwapOut ? outDur * 0.54
          : forcedSwapIn ? outDur + SWAP_IN_DURATION * 0.34
            : clamp(swapElapsed, 0, totalDur);
        swapProgress = clamp(tSwap / totalDur, 0, 1);

        const troughOut = {
          dx: swapDir * baseScale * 0.24,
          dy: -baseVisH * 0.25,
          dz: -baseScale * 0.04,
          rx: 6.0 * DEG,
          ry: -swapDir * 10.0 * DEG,
          rz: -swapDir * 8.5 * DEG
        };
        const troughIn = {
          dx: -swapDir * baseScale * 0.21,
          dy: -baseVisH * 0.24,
          dz: -baseScale * 0.03,
          rx: 5.0 * DEG,
          ry: swapDir * 8.5 * DEG,
          rz: swapDir * 7.5 * DEG
        };

        if (tSwap < outDur) {
          // Phase A — Tuck Away: smooth glide downward & sideways, eyes & shoulders follow.
          swapStage = "swap-out";
          const u = clamp(tSwap / Math.max(0.05, outDur), 0, 1);
          const e = easeInOutCubic(u);
          const sp = forcedSwapOut ? { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 } : swapStartPose;
          targetOffset = {
            dx: lerp(sp.dx, troughOut.dx, e),
            dy: lerp(sp.dy, troughOut.dy, e),
            dz: lerp(sp.dz, troughOut.dz, e),
            rx: lerp(sp.rx, troughOut.rx, e),
            ry: lerp(sp.ry, troughOut.ry, e),
            rz: lerp(sp.rz, troughOut.rz, e)
          };
          immediateOffset = true;
          const follow = Math.sin(u * Math.PI * 0.85);
          swapSweep = swapDir * follow * 0.85;
          wantHeadYaw = swapDir * 9.5 * DEG * follow;
          wantHeadPitch = (6.8 + 3.2 * follow) * DEG;
          extraHeadRoll = swapDir * 4.2 * DEG * follow;
          eyeYaw = swapDir * 14.0 * DEG * follow;
          eyePitch = -11.5 * DEG;
          glowMultiplier = lerp(1.0, 0.25, e);
          exprOverlay = {
            mode: "swap-out",
            happy: 0.16,
            relaxed: 0.10,
            allowBlink: true
          };
        } else if (tSwap < outDur + SWAP_IN_DURATION) {
          // Phase B — Fresh Laptop Enters: smooth slide up from opposite slight angle with tiny settle.
          swapStage = "swap-in";
          const v = clamp((tSwap - outDur) / SWAP_IN_DURATION, 0, 1);
          const e = easeOutCubic(v);
          // Delicate tactile overshoot (+0.6% viewport max — always strictly <= 18.2% viewport height).
          const settleOvershoot = Math.sin(v * Math.PI) * v * v * (1 - v) * baseVisH * 0.05;
          targetOffset = {
            dx: lerp(troughIn.dx, 0, e),
            dy: lerp(troughIn.dy, 0, e) + settleOvershoot,
            dz: lerp(troughIn.dz, 0, e),
            rx: lerp(troughIn.rx, 0, e),
            ry: lerp(troughIn.ry, 0, e),
            rz: lerp(troughIn.rz, 0, e)
          };
          immediateOffset = true;
          const sweepIn = (1 - e);
          swapSweep = -swapDir * sweepIn * 0.72;
          wantHeadYaw = -swapDir * 7.8 * DEG * sweepIn;
          wantHeadPitch = (5.8 + 2.4 * sweepIn) * DEG;
          extraHeadRoll = -swapDir * 3.4 * DEG * sweepIn;
          eyeYaw = -swapDir * 11.5 * DEG * sweepIn;
          eyePitch = -9.8 * DEG;
          glowMultiplier = lerp(0.28, 1.08, e);
          exprOverlay = {
            mode: "swap-in",
            happy: 0.22 + 0.08 * e,
            relaxed: 0.12,
            allowBlink: true
          };
        } else {
          // Phase C — Attentive Settle: crisp resting lid, gentle micro-nod & warm micro-smile.
          swapStage = "swap-settle";
          const w = clamp((tSwap - outDur - SWAP_IN_DURATION) / SWAP_SETTLE_DURATION, 0, 1);
          const nodPulse = Math.sin(w * Math.PI);
          targetOffset = { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 };
          immediateOffset = true;
          swapSweep = 0;
          wantHeadYaw = gx * 2.5 * DEG;
          wantHeadPitch = (5.4 + 3.2 * nodPulse) * DEG;
          extraHeadRoll = 2.4 * DEG * nodPulse;
          eyeYaw = gx * 4.5 * DEG;
          eyePitch = -9.2 * DEG;
          glowMultiplier = 1.05;
          exprOverlay = {
            mode: "swap-settle",
            happy: 0.16 + 0.18 * nodPulse,
            relaxed: 0.09 + 0.08 * nodPulse,
            allowBlink: true
          };
        }
      }

      // Smoothly update laptop 3D transform (direct eased curve during swap for crisp tactile arc,
      // exponentially damped during dizzy/recovery/rest for zero snapping).
      let targetActionFxKind = "";
      let targetActionFxAlpha = 0;

      if (inAnyKnock) {
        if (activeKnockKind === "wakeup-jolt") {
          const joltEnv = forcedWakeupJolt ? 1.0 : Math.sin(clamp(knockElapsed / Math.max(0.2, knockDuration), 0, 1) * Math.PI);
          wantHeadYaw = gx * 4 * DEG;
          wantHeadPitch = -4.2 * DEG * joltEnv;
          extraHeadRoll = 4.0 * DEG * joltEnv;
          eyeYaw = gx * 6 * DEG;
          eyePitch = 4.5 * DEG;
          exprOverlay = { mode: "wakeup-jolt", surprised: 0.96, blink: 0.0, allowBlink: false };
          targetActionFxKind = "wakeup-jolt";
          targetActionFxAlpha = Math.max(0.25, joltEnv);
        } else if (activeKnockKind === "annoyed-tap") {
          const tapEnv = forcedAnnoyedTap ? 1.0 : clamp(1 - knockElapsed / Math.max(0.3, knockDuration) * 0.25, 0.3, 1);
          wantHeadYaw = -5.5 * DEG;
          wantHeadPitch = -1.2 * DEG;
          extraHeadRoll = -6.8 * DEG * tapEnv;
          eyeYaw = -3.0 * DEG;
          eyePitch = 2.0 * DEG;
          exprOverlay = { mode: "annoyed-tap", angry: 0.62, blink: 0.10, allowBlink: false };
          targetActionFxKind = "annoyed-tap";
          targetActionFxAlpha = tapEnv;
        } else {
          const stEnv = forcedKnockStartle ? 1.0 : Math.sin(clamp(knockElapsed / Math.max(0.2, knockDuration), 0, 1) * Math.PI);
          wantHeadYaw = gx * 5 * DEG;
          wantHeadPitch = -3.0 * DEG * stEnv;
          extraHeadRoll = -3.2 * DEG * stEnv;
          eyeYaw = gx * 8 * DEG;
          eyePitch = 3.0 * DEG;
          exprOverlay = { mode: "knock-startle", surprised: 0.88, blink: 0.0, allowBlink: false };
          targetActionFxKind = "knock-startle";
          targetActionFxAlpha = Math.max(0.25, stEnv);
        }
      } else if (inPhoneAction) {
        if (activePhoneStage === "buzz") {
          wantHeadYaw = 12.5 * DEG;
          wantHeadPitch = 8.5 * DEG;
          extraHeadRoll = 4.5 * DEG;
          eyeYaw = 18.0 * DEG;
          eyePitch = -11.5 * DEG;
          exprOverlay = { mode: "phone-buzz", surprised: 0.36, happy: 0.16, blink: 0.0, allowBlink: false };
          targetActionFxKind = "phone-buzz";
          targetActionFxAlpha = 1.0;
        } else if (activePhoneStage === "pickup" || activePhoneStage === "typing") {
          // Gently lowers laptop lid slightly so one-hand phone lifts cleanly beside it.
          targetOffset = { dx: -baseScale * 0.03, dy: -baseVisH * 0.032, dz: 0, rx: 1.8 * DEG, ry: 0, rz: 0 };
          wantHeadYaw = 10.5 * DEG;
          wantHeadPitch = 6.2 * DEG;
          extraHeadRoll = 5.4 * DEG;
          eyeYaw = 14.5 * DEG;
          eyePitch = -8.5 * DEG;
          exprOverlay = {
            mode: activePhoneStage === "typing" ? "phone-typing" : "phone-pickup",
            happy: 0.28,
            relaxed: 0.16,
            allowBlink: true
          };
        } else if (activePhoneStage === "facedown-merong") {
          // Cheeky anime wink + playful merong tilt while smartphone flips face-down ("탁!").
          wantHeadYaw = 4.5 * DEG;
          wantHeadPitch = -1.8 * DEG;
          extraHeadRoll = 10.5 * DEG;
          eyeYaw = 3.5 * DEG;
          eyePitch = 2.0 * DEG;
          exprOverlay = {
            mode: "phone-facedown-merong",
            wink: 0.96,
            happy: 0.72,
            merong: 0.85,
            allowBlink: false
          };
          targetActionFxKind = "phone-facedown-merong";
          targetActionFxAlpha = 1.0;
        } else if (activePhoneStage === "putdown") {
          wantHeadYaw = 7.5 * DEG;
          wantHeadPitch = 7.2 * DEG;
          extraHeadRoll = 2.2 * DEG;
          eyeYaw = 11.0 * DEG;
          eyePitch = -10.0 * DEG;
          exprOverlay = { mode: "phone-putdown", happy: 0.22, relaxed: 0.14, allowBlink: true };
        }
      }

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

      // Compute 3D smartphone target pose & notification screen glow.
      const phoneIsFacedownNow = forcedPhoneMerong
        ? true
        : (activePhoneStage === "facedown-merong" && phoneAnimElapsed >= PHONE_MERONG_DURATION * 0.48) ||
          (activePhoneStage === "desk" && phoneFaceDown);
      let phoneTarget = {
        x: phoneBasePos.x,
        y: phoneBasePos.y,
        z: phoneBasePos.z,
        rx: phoneIsFacedownNow ? -44 * DEG : -46 * DEG,
        ry: phoneIsFacedownNow ? 168 * DEG : -12 * DEG,
        rz: -6 * DEG
      };
      let targetPhoneGlow = 0.14;
      let targetBuzzRingAlpha = 0.0;

      if (activePhoneStage === "buzz") {
        const vib = Math.sin(elapsed * 52);
        const vib2 = Math.cos(elapsed * 46);
        phoneTarget = {
          x: phoneBasePos.x + vib * baseScale * 0.016,
          y: phoneBasePos.y + Math.abs(vib2) * baseScale * 0.008,
          z: phoneBasePos.z + 0.005,
          rx: -32 * DEG,
          ry: -14 * DEG,
          rz: -6 * DEG + vib * 6.5 * DEG
        };
        targetPhoneGlow = 0.95 + 0.30 * (0.5 + 0.5 * Math.sin(elapsed * 14));
        targetBuzzRingAlpha = 0.42 + 0.28 * (0.5 + 0.5 * Math.sin(elapsed * 14));
      } else if (wantPhoneHold) {
        const thumbJiggle = wantPhoneTyping ? Math.sin(elapsed * 11) * baseScale * 0.006 : 0;
        const heldX = basePos.x + Math.min(baseScale * 0.46, Math.max(baseScale * 0.34, baseVisHalfW - baseScale * 0.28));
        const heldY = lidTopWorldY + baseVisH * 0.195 + thumbJiggle;
        const heldZ = basePos.z - baseScale * 0.03;
        phoneTarget = {
          x: heldX,
          y: heldY,
          z: heldZ,
          // Sleek 3/4 held angle: camera module & metallic back angled gently toward viewer
          // while screen faces her across the glass wall (zero face occlusion!).
          rx: -8 * DEG,
          ry: 144 * DEG,
          rz: 12 * DEG + (wantPhoneTyping ? Math.sin(elapsed * 9) * 2.5 * DEG : 0)
        };
        targetPhoneGlow = 0.72;
      } else if (activePhoneStage === "facedown-merong") {
        // Flipped face-down on desk with back camera bump unmistakably facing UP & camera!
        phoneTarget = {
          x: phoneBasePos.x,
          y: phoneBasePos.y + baseScale * 0.015,
          z: phoneBasePos.z + baseScale * 0.01,
          rx: -36 * DEG,
          ry: 168 * DEG,
          rz: -5 * DEG
        };
        targetPhoneGlow = 0.04;
      }

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

      if (targetActionFxKind) {
        actionFxKind = targetActionFxKind;
      }
      actionFxAlpha = damp(actionFxAlpha, targetActionFxAlpha, targetActionFxAlpha > actionFxAlpha ? 14.0 : 10.0, dt);
      if (actionFxAlpha < 0.01) {
        actionFxAlpha = 0;
        if (!targetActionFxKind) actionFxKind = "";
      }

      // Smoothly interpolate screen bounce color toward active displayed accent.
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

      // Smoothly damp 2D anime dizzy FX alpha.
      dizzyFxAlpha = forcedDizzy ? 1.0 : damp(dizzyFxAlpha, targetDizzyFxAlpha, targetDizzyFxAlpha > dizzyFxAlpha ? 10.0 : 12.0, dt);
      if (dizzyFxAlpha < 0.002) dizzyFxAlpha = 0;

      // Subtle soft screen bounce light modulation (flattering luminous focus when working,
      // calm ambient on idle, softly dimmed on sleepy or dizzy — calibrated for MToon skin).
      const keyFlicker = (Math.sin(elapsed * 9.3) * 0.5 + Math.sin(elapsed * 15.7 + 1.2) * 0.5) * 0.016 * typingWeight;
      let targetGlow = 0.13 + keyFlicker * 0.5;
      if (wantSleepy) targetGlow = 0.03;
      else if (wantWorking) targetGlow = 0.24 + keyFlicker;
      else if (wantGlance) targetGlow = 0.12;
      else if (wantReaction) targetGlow = 0.11;
      targetGlow *= glowMultiplier;

      screenGlow = damp(screenGlow, targetGlow, 6.0, dt);
      if (screenLight) screenLight.intensity = screenGlow;
      if (emblemMesh?.material) {
        const emblemTarget = wantSleepy ? 0.26 : (inDizzySway ? 0.42 : (wantWorking ? 0.96 : 0.88));
        emblemMesh.material.opacity = damp(emblemMesh.material.opacity ?? emblemTarget, emblemTarget, 6.0, dt);
      }

      // Cute expression overlay (fallback to Step 2 rules when not in swap/dizzy).
      if (!exprOverlay) {
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
      }

      const boneTargets = computeCoLeadTargets({
        elapsed,
        typingWeight,
        glanceWeight,
        sleepyWeight,
        reactionWeight,
        workingWeight,
        swapSweep,
        swapWeight,
        dizzyWeight,
        dizzyTime,
        phoneHoldWeight,
        phoneTypingWeight,
        phoneMerongWeight,
        knockJoltWeight,
        annoyedTapWeight,
        extraHeadRoll,
        tiltDir: glanceTiltDir,
        headYaw: wantHeadYaw,
        headPitch: wantHeadPitch,
        headPitchCue: ctx.headPitchCue || 0
      });

      const activeMode = overrideBeat !== "auto"
        ? overrideBeat
        : inDizzySway ? "dizzy"
          : (inDizzyShake || inDizzyRecover) ? "dizzy-recover"
            : inAnyKnock ? activeKnockKind
              : inPhoneAction ? ("phone-" + activePhoneStage)
                : swapStage !== "none" ? swapStage
                  : wantSleepy ? "sleepy"
                    : wantReaction ? "reaction"
                      : wantGlance ? "glance-up"
                        : wantWorking ? (wantTyping ? "working" : "working-read")
                          : (wantTyping ? "typing" : "reading");

      return {
        beat: activeMode,
        mode: activeMode,
        activity,
        boneTargets,
        eyeYaw,
        eyePitch,
        exprOverlay,
        screenGlow,
        typing: typingWeight > 0.35,
        glanceUp: glanceWeight > 0.35,
        swapActive: inSwap,
        swapProgress,
        dizzyActive: inAnyDizzy,
        dizzyFxAlpha,
        dizzyTime,
        actionFxKind,
        actionFxAlpha,
        phonePose: wantPhoneHold ? "held" : (activePhoneStage === "buzz" ? "buzz" : (phoneIsFacedownNow ? "facedown" : "desk")),
        phoneFaceDown: phoneIsFacedownNow,
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
      targetAccentHex = norm;
      accentId = findPaletteEntryByHex(norm)?.id || "custom";
      applyAccentVisuals(true);
      return accentHex;
    }

    // Adopt the initial session ID on mount silently (no swap animation).
    function syncInitialSession(sessionId, meta) {
      const rid = typeof sessionId === "string" ? sessionId.trim() : "";
      if (!rid || activeSessionId) return getCoLeadState();
      activeSessionId = rid;
      activeSessionMeta = meta && typeof meta === "object" ? { ...meta } : null;
      const entry = sessionAccentForKey(rid);
      accentHex = entry.hex;
      targetAccentHex = entry.hex;
      accentId = entry.id;
      applyAccentVisuals(true);
      return getCoLeadState();
    }

    // Handle a real or QA session switch: deterministic palette, idempotency,
    // smooth interruptible laptop swap, and sliding-window rapid-switch dizzy egg.
    function onSessionSwitch(sessionId, meta) {
      const rid = typeof sessionId === "string" ? sessionId.trim() : "";
      if (!rid) return { ...getCoLeadState(), swapped: false, dizzyTriggered: false };
      if (!activeSessionId) {
        syncInitialSession(rid, meta);
        return { ...getCoLeadState(), swapped: false, dizzyTriggered: false };
      }
      if (rid === activeSessionId) {
        if (meta && typeof meta === "object") {
          activeSessionMeta = { ...(activeSessionMeta || {}), ...meta };
        }
        return { ...getCoLeadState(), swapped: false, dizzyTriggered: false };
      }

      const nowSec = currentClockSec(meta?.nowMs);
      activeSessionId = rid;
      activeSessionMeta = meta && typeof meta === "object" ? { ...meta } : null;
      const entry = sessionAccentForKey(rid);
      targetAccentHex = entry.hex;
      accentId = entry.id;

      // Update sliding window for rapid distinct switches.
      pruneSwitchWindow(nowSec);
      switchTimestamps.push(nowSec);

      // Trigger cute dizzy easter egg when >=4 distinct switches land inside 2.8s.
      let dizzyTriggered = false;
      if (switchTimestamps.length >= DIZZY_TRIGGER_COUNT && dizzyPhase === "none" && nowSec >= dizzyCooldownUntil) {
        dizzyTriggered = true;
        switchTimestamps.length = 0;
        swapActive = false;
        swapElapsed = 0;
        swapOutDuration = SWAP_OUT_DURATION;
        dizzyPhase = "sway";
        dizzyElapsed = 0;
        accentHex = targetAccentHex;
        applyAccentVisuals(false);
        return { ...getCoLeadState(), swapped: false, dizzyTriggered: true };
      }

      // If already dizzy, just update target session accent so she recovers onto it.
      if (dizzyPhase !== "none") {
        accentHex = targetAccentHex;
        applyAccentVisuals(false);
        return { ...getCoLeadState(), swapped: false, dizzyTriggered: false };
      }

      // Smoothly start or interrupt a laptop swap from the live visual offset (zero snap!).
      swapStartPose = { ...liveLaptopOffset };
      const depthRatio = clamp(Math.abs(liveLaptopOffset.dy) / Math.max(0.05, baseVisH * 0.25), 0, 0.85);
      swapOutDuration = SWAP_OUT_DURATION * (1 - depthRatio * 0.45);
      swapDir = -swapDir;
      swapActive = true;
      swapElapsed = 0;
      swapSwitchedColor = false;
      return { ...getCoLeadState(), swapped: true, dizzyTriggered: false };
    }

    function setCoLeadBeat(beat) {
      if (beat === null || beat === undefined || beat === "") {
        overrideBeat = "auto";
        return overrideBeat;
      }
      const key = String(beat).trim().toLowerCase();
      if (!VALID_BEATS.has(key)) return overrideBeat;
      overrideBeat = key;
      if (key === "auto") {
        // Leave live state clean when returning to auto from deterministic QA beats.
        dizzyFxAlpha = dizzyPhase === "sway" ? 1 : 0;
      } else if (key === "glance-up") {
        glanceTiltDir = 1;
        glanceWeight = 1;
        typingWeight = 0;
        sleepyWeight = 0;
        workingWeight = 0;
        dizzyWeight = 0;
        dizzyFxAlpha = 0;
      } else if (key === "typing") {
        typingWeight = 1;
        glanceWeight = 0;
        sleepyWeight = 0;
        workingWeight = 0;
        dizzyWeight = 0;
        dizzyFxAlpha = 0;
      } else if (key === "working") {
        typingWeight = 1;
        workingWeight = 1;
        glanceWeight = 0;
        sleepyWeight = 0;
        dizzyWeight = 0;
        dizzyFxAlpha = 0;
      } else if (key === "sleepy") {
        sleepyWeight = 1;
        typingWeight = 0;
        glanceWeight = 0;
        workingWeight = 0;
        dizzyWeight = 0;
        dizzyFxAlpha = 0;
      } else if (key === "reading-left" || key === "reading-right") {
        typingWeight = 0.85;
        glanceWeight = 0;
        sleepyWeight = 0;
        dizzyWeight = 0;
        dizzyFxAlpha = 0;
      } else if (key === "swap-out" || key === "swap-in") {
        typingWeight = 0;
        glanceWeight = 0;
        sleepyWeight = 0;
        workingWeight = 0;
        dizzyWeight = 0;
        swapWeight = 1;
        dizzyFxAlpha = 0;
      } else if (key === "dizzy") {
        typingWeight = 0;
        glanceWeight = 0;
        sleepyWeight = 0;
        workingWeight = 0;
        swapWeight = 0;
        dizzyWeight = 1;
        dizzyFxAlpha = 1;
      } else if (key === "dizzy-shake" || key === "dizzy-recover") {
        typingWeight = 0;
        glanceWeight = 0;
        sleepyWeight = 0;
        workingWeight = 0;
        swapWeight = 0;
        dizzyWeight = 0;
        dizzyFxAlpha = key === "dizzy-shake" ? 0.30 : 0;
      } else if (key === "knock-startle" || key === "wakeup-jolt" || key === "annoyed-tap") {
        typingWeight = 0;
        glanceWeight = 0;
        sleepyWeight = 0;
        workingWeight = 0;
        swapWeight = 0;
        dizzyWeight = 0;
        dizzyFxAlpha = 0;
        phoneHoldWeight = 0;
        phoneTypingWeight = 0;
        phoneMerongWeight = 0;
        knockJoltWeight = key === "annoyed-tap" ? 0 : 1;
        annoyedTapWeight = key === "annoyed-tap" ? 1 : 0;
        actionFxKind = key;
        actionFxAlpha = 1;
      } else if (key === "phone-buzz" || key === "phone-pickup" || key === "phone-typing" || key === "phone-putdown" || key === "phone-facedown-merong") {
        typingWeight = 0;
        glanceWeight = 0;
        sleepyWeight = 0;
        workingWeight = 0;
        swapWeight = 0;
        dizzyWeight = 0;
        dizzyFxAlpha = 0;
        knockJoltWeight = 0;
        annoyedTapWeight = 0;
        phoneHoldWeight = (key === "phone-pickup" || key === "phone-typing") ? 1 : 0;
        phoneTypingWeight = key === "phone-typing" ? 1 : 0;
        phoneMerongWeight = key === "phone-facedown-merong" ? 1 : 0;
        phoneFaceDown = key === "phone-facedown-merong";
        actionFxKind = (key === "phone-buzz" || key === "phone-facedown-merong") ? key : "";
        actionFxAlpha = actionFxKind ? 1 : 0;
      }
      return overrideBeat;
    }

    function triggerKnock(opts = {}) {
      const nowSec = currentClockSec(opts?.nowMs);
      while (knockTimestamps.length && nowSec - knockTimestamps[0] > KNOCK_WINDOW_SEC) {
        knockTimestamps.shift();
      }
      const isSleepy = !!opts?.sleepy || sleepyWeight > 0.35 || overrideBeat === "sleepy" || lastActivity === "sleepy";
      if (overrideBeat === "sleepy") overrideBeat = "auto";
      knockTimestamps.push(nowSec);
      knockCount = knockTimestamps.length;

      if (isSleepy) {
        knockTimestamps.length = 0;
        knockCount = 1;
        sleepyWeight = 0;
        knockKind = "wakeup-jolt";
        knockElapsed = 0;
        knockDuration = KNOCK_WAKEUP_DURATION;
        return { kind: "wakeup_jolt", count: 1, wokeFromSleepy: true };
      }
      if (knockTimestamps.length >= KNOCK_ANNOYED_COUNT) {
        knockTimestamps.length = 0;
        knockKind = "annoyed-tap";
        knockElapsed = 0;
        knockDuration = KNOCK_ANNOYED_DURATION;
        return { kind: "annoyed_tap", count: knockCount, wokeFromSleepy: false };
      }
      knockKind = "knock-startle";
      knockElapsed = 0;
      knockDuration = KNOCK_STARTLE_DURATION;
      return { kind: "knock_startle", count: knockCount, wokeFromSleepy: false };
    }

    function triggerGesture(gestureName) {
      const g = typeof gestureName === "string" ? gestureName.trim().toLowerCase() : "";
      if (!g) return false;
      if (g === "phone_buzz") {
        phoneFaceDown = false;
        phoneMode = "buzz";
        phoneAnimElapsed = 0;
        return true;
      }
      if (g === "phone_pickup") {
        phoneFaceDown = false;
        phoneMode = "pickup";
        phoneAnimElapsed = 0;
        return true;
      }
      if (g === "phone_typing") {
        phoneFaceDown = false;
        phoneMode = "typing";
        phoneAnimElapsed = 0;
        return true;
      }
      if (g === "phone_putdown") {
        // The server always emits putdown when it clears a hold, so a putdown
        // that finds the phone already resting is a no-op, not a replay.
        if (phoneMode === "desk" || phoneMode === "buzz" || phoneMode === "putdown") return true;
        phoneFaceDown = false;
        phoneMode = "putdown";
        phoneAnimElapsed = 0;
        return true;
      }
      if (g === "phone_merong_facedown") {
        phoneFaceDown = true;
        phoneMode = "facedown-merong";
        phoneAnimElapsed = 0;
        return true;
      }
      if (g === "knock_startle") {
        knockKind = "knock-startle";
        knockElapsed = 0;
        knockDuration = KNOCK_STARTLE_DURATION;
        return true;
      }
      if (g === "wakeup_jolt") {
        knockKind = "wakeup-jolt";
        knockElapsed = 0;
        knockDuration = KNOCK_WAKEUP_DURATION;
        return true;
      }
      if (g === "annoyed_tap") {
        knockKind = "annoyed-tap";
        knockElapsed = 0;
        knockDuration = KNOCK_ANNOYED_DURATION;
        return true;
      }
      return false;
    }

    function setPhoneState(snap) {
      if (!snap || typeof snap !== "object") return getCoLeadState();
      const prevState = phoneLastSnapState;
      const nextState = typeof snap.state === "string" && snap.state ? snap.state : "laptop_ambient";
      const nextPosture = typeof snap.posture === "string" && snap.posture ? snap.posture : "laptop";
      const unread = Number.isFinite(snap.unreadAgent) ? Math.max(0, snap.unreadAgent) : 0;
      const faceDown = !!snap.phoneFaceDown;
      phoneUnreadAgent = unread;
      phoneFaceDown = faceDown;
      phoneLastSnapState = nextState;
      phoneSnapHeld = nextState === "phone_active" || nextPosture === "phone";
      phoneUnconfirmedElapsed = 0;

      if (phoneSnapHeld) {
        if (phoneMode !== "pickup" && phoneMode !== "typing") {
          phoneMode = "pickup";
          phoneAnimElapsed = 0;
        }
      } else if (unread > 0) {
        phoneMode = "buzz";
        phoneAnimElapsed = 0;
      } else if (prevState === "phone_active") {
        phoneMode = faceDown ? "facedown-merong" : "putdown";
        phoneAnimElapsed = 0;
      } else if (phoneMode === "pickup" || phoneMode === "typing" || phoneMode === "buzz") {
        phoneMode = faceDown ? "facedown-merong" : "putdown";
        phoneAnimElapsed = 0;
      }
      return getCoLeadState();
    }

    function setHover(flag) {
      explicitHover = flag === null || flag === undefined ? null : !!flag;
    }

    function currentModeName() {
      if (overrideBeat !== "auto") return overrideBeat;
      if (dizzyPhase === "sway") return "dizzy";
      if (dizzyPhase === "recover") return "dizzy-recover";
      if (swapActive) {
        if (swapElapsed < swapOutDuration) return "swap-out";
        if (swapElapsed < swapOutDuration + SWAP_IN_DURATION) return "swap-in";
        return "swap-settle";
      }
      return phase;
    }

    function getCoLeadState() {
      const mode = currentModeName();
      const totalSwapDur = swapOutDuration + SWAP_IN_DURATION + SWAP_SETTLE_DURATION;
      const swapProg = overrideBeat === "swap-out" ? 0.22
        : overrideBeat === "swap-in" ? 0.56
          : swapActive ? clamp(swapElapsed / Math.max(0.05, totalSwapDur), 0, 1) : 0;
      const dizzyActiveNow = overrideBeat === "dizzy" || overrideBeat === "dizzy-shake" || overrideBeat === "dizzy-recover" || dizzyPhase !== "none";
      const dizzyProg = overrideBeat === "dizzy" ? 0.45
        : overrideBeat === "dizzy-shake" ? 0.84
          : overrideBeat === "dizzy-recover" ? 0.94
            : dizzyPhase === "sway" ? clamp(dizzyElapsed / DIZZY_TOTAL_DURATION, 0, 1)
              : dizzyPhase === "recover" ? clamp((DIZZY_SWAY_DURATION + dizzyElapsed) / DIZZY_TOTAL_DURATION, 0, 1) : 0;
      return {
        mode,
        beat: mode,
        overrideBeat,
        activity: lastActivity,
        activeSessionId,
        typing: typingWeight > 0.35,
        typingWeight: Number(typingWeight.toFixed(3)),
        glanceUp: glanceWeight > 0.35,
        glanceWeight: Number(glanceWeight.toFixed(3)),
        sleepyWeight: Number(sleepyWeight.toFixed(3)),
        workingWeight: Number(workingWeight.toFixed(3)),
        hover: explicitHover === true,
        laptopAccent: accentHex,
        targetLaptopAccent: targetAccentHex,
        laptopAccentId: accentId,
        swapActive: overrideBeat === "swap-out" || overrideBeat === "swap-in" || swapActive,
        swapProgress: Number(swapProg.toFixed(3)),
        liveLaptopOffsetY: Number(liveLaptopOffset.dy.toFixed(4)),
        liveLaptopOffsetX: Number(liveLaptopOffset.dx.toFixed(4)),
        dizzyActive: dizzyActiveNow,
        dizzyPhase: overrideBeat === "dizzy" ? "sway"
          : (overrideBeat === "dizzy-shake" || overrideBeat === "dizzy-recover") ? "recover"
            : dizzyPhase,
        dizzyProgress: Number(dizzyProg.toFixed(3)),
        dizzyFxAlpha: Number(dizzyFxAlpha.toFixed(3)),
        recentSwitchCount: switchTimestamps.length,
        screenGlow: Number(screenGlow.toFixed(3)),
        hasLaptopProp: !!laptopGroup,
        hasPhoneProp: !!phoneGroup,
        phoneMode: overrideBeat.startsWith("phone-") ? overrideBeat : phoneMode,
        phonePose: phoneHoldWeight > 0.35 ? "held"
          : (overrideBeat === "phone-buzz" || phoneMode === "buzz") ? "buzz"
            : (overrideBeat === "phone-facedown-merong" || phoneFaceDown) ? "facedown" : "desk",
        phoneFaceDown: overrideBeat === "phone-facedown-merong" ? true : phoneFaceDown,
        phoneScreenGlow: Number(phoneScreenGlow.toFixed(3)),
        knockActive: !!knockKind || overrideBeat === "knock-startle" || overrideBeat === "wakeup-jolt" || overrideBeat === "annoyed-tap",
        knockKind: (overrideBeat === "knock-startle" || overrideBeat === "wakeup-jolt" || overrideBeat === "annoyed-tap") ? overrideBeat : knockKind,
        knockCount,
        laptopTopViewportPct: Number(lidTopViewportPct.toFixed(2)),
        chinViewportPct: Number(chinViewportPct.toFixed(2)),
        headViewportPct: Number(headViewportPct.toFixed(2))
      };
    }

    function debugState() {
      return getCoLeadState();
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
      layoutProp,
      step,
      syncPhoneToHand,
      setLaptopAccent,
      syncInitialSession,
      onSessionSwitch,
      setCoLeadBeat,
      setHover,
      triggerKnock,
      triggerGesture,
      setPhoneState,
      getCoLeadState,
      debugState,
      dispose
    };
  }

  Object.assign(C, {
    createVrmCoLead,
    sessionAccentForKey,
    vrmDizzyFx: {
      computeDizzyLayout,
      drawDizzyFx,
      drawActionFx,
      DIZZY_STAR_COLORS
    },
    vrmCoLeadMath: {
      SESSION_ACCENT_PALETTE,
      sessionAccentForKey,
      computeCoLeadTargets,
      classifyActivity,
      mirrorLR,
      normalizeHexColor,
      dampAlpha
    }
  });
})(window.WsCompanion);
