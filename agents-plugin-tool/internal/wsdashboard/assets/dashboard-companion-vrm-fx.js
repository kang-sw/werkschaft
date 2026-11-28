(function (C) {
  "use strict";

  // ============================================================================
  // Crisp 2D Anime Dizzy & Action FX Overlay Helpers
  // ============================================================================
  // Renders strictly-clamped 2D anime spirals, orbiting pastel crown stars,
  // and glass-knock / smartphone cue pills inside the active VRM viewport rect.

  const DIZZY_STAR_COLORS = Object.freeze(["#fde68a", "#7dd3fc", "#fbcfe8"]);

  function clamp(v, lo, hi) {
    return v < lo ? lo : (v > hi ? hi : v);
  }

  function lerp(a, b, t) {
    return a + (b - a) * t;
  }

  // Compute safe, strictly-clamped 2D anchor positions inside the active rect
  // so spirals and orbiting stars never clip against edges or cover eyes.
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

    // Float anime spirals cleanly beside & slightly above temples (clear of cheeks & hair).
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

    // Float orbiting star halo gracefully above crown/hairline (never over forehead or eyes).
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

    if (!opts.skipClear && typeof ctx.clearRect === "function") ctx.clearRect(0, 0, w, h);
    if (alpha <= 0.002) return layout;

    ctx.save();
    ctx.globalAlpha = alpha;

    // 1. Delicate elliptical orbit swoosh arcs above crown.
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

    // 2. Primary pastel-gold/cyan anime spiral beside upper-right temple.
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

    // 4. Three twinkling 4-point pastel stars orbiting crown halo.
    const starGlows = [
      "rgba(253, 230, 138, 0.28)",
      "rgba(125, 211, 252, 0.28)",
      "rgba(251, 207, 232, 0.28)"
    ];
    for (let i = 0; i < 3; i++) {
      const a = orbitRot + (i * Math.PI * 2) / 3;
      const cosA = Math.cos(a);
      const sinA = Math.sin(a);
      const depthScale = 0.78 + (sinA + 1) * 0.22;
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

  // Draw crisp 2D canvas FX cue (buzz waves, merong sparkle, startle burst) cleanly
  // clamped inside the active viewport rect without covering the character's face.
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
    const bg = "rgba(32, 36, 46, 0.90)";
    let fg = "#fde68a";
    let stroke = "rgba(122, 162, 255, 0.72)";

    if (kind === "phone-buzz") {
      label = "윙- ♪";
      bx = clamp(w * 0.80, 52, w - 42);
      by = clamp(h * 0.68 + Math.sin(t * 28) * 2.5, 34, h - 26);
      fg = "#93c5fd";
      stroke = "rgba(96, 165, 250, 0.85)";
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

  // ============================================================================
  // Shell Fixed Transparent Overlay Geometry, Asymmetric Frustum & Scissor Math
  // ============================================================================
  const DEG = Math.PI / 180;
  const BASE_FOV = 26;

  function readChromeBounds(container, overlayMode, viewport, fallbackW, fallbackH) {
    const vw = Math.max(1, Math.round(overlayMode ? viewport.width : fallbackW));
    const vh = Math.max(1, Math.round(overlayMode ? viewport.height : fallbackH));
    if (!overlayMode) return { vw, vh, topbarH: 0, botbarH: 0 };
    const doc = container?.ownerDocument || (typeof document !== "undefined" ? document : null);
    const rootEl = doc?.documentElement || null;
    const cs = rootEl && typeof getComputedStyle === "function" ? getComputedStyle(rootEl) : null;
    const readCssVar = name => (cs && typeof cs.getPropertyValue === "function" ? parseFloat(cs.getPropertyValue(name)) : NaN);
    let botbarH = 26;
    const botEl = doc?.querySelector?.(".shell-bottom");
    const botRect = botEl && typeof botEl.getBoundingClientRect === "function" ? botEl.getBoundingClientRect() : null;
    if (botRect && Number.isFinite(botRect.height) && botRect.height > 0) {
      botbarH = Math.round(botRect.height);
    } else {
      const parsed = readCssVar("--botbar-h");
      if (Number.isFinite(parsed) && parsed >= 0) botbarH = Math.round(parsed);
    }
    let topbarH = 0;
    const topEl = doc?.querySelector?.(".dwrap > .top, .shell-top");
    const topRect = topEl && typeof topEl.getBoundingClientRect === "function" ? topEl.getBoundingClientRect() : null;
    if (topRect && Number.isFinite(topRect.height) && topRect.height > 0 && topRect.top <= 2) {
      topbarH = Math.round(topRect.height);
    } else {
      const parsed = readCssVar("--topbar-h");
      if (Number.isFinite(parsed) && parsed >= 0) topbarH = Math.round(parsed);
    }
    topbarH = clamp(topbarH, 0, Math.max(0, vh - 40));
    botbarH = clamp(botbarH, 0, Math.max(0, vh - topbarH - 40));
    return { vw, vh, topbarH, botbarH };
  }

  function clampRectToChrome(rect, bounds) {
    const minX = 0;
    const maxX = Math.max(1, bounds.vw);
    const minY = bounds.topbarH;
    const maxY = Math.max(minY + 1, bounds.vh - bounds.botbarH);
    const x1 = clamp(Math.floor(rect.left), minX, maxX - 1);
    const y1 = clamp(Math.floor(rect.top), minY, maxY - 1);
    const x2 = clamp(Math.ceil(rect.left + rect.width), x1 + 1, maxX);
    const y2 = clamp(Math.ceil(rect.top + rect.height), y1 + 1, maxY);
    return { left: x1, top: y1, width: Math.max(1, x2 - x1), height: Math.max(1, y2 - y1) };
  }

  function rectContains(outer, inner) {
    if (!outer || !inner) return false;
    return inner.left >= outer.left &&
      inner.top >= outer.top &&
      (inner.left + inner.width) <= (outer.left + outer.width) &&
      (inner.top + inner.height) <= (outer.top + outer.height);
  }

  function computeAsymmetricFrustum(anchorRect, rect, near = 0.08, far = 25, baseFov = BASE_FOV) {
    const slotW = Math.max(1, anchorRect.width);
    const slotH = Math.max(1, anchorRect.height);
    const slotCenterX = anchorRect.left + slotW * 0.5;
    const slotCenterY = anchorRect.top + slotH * 0.5;
    const scale = (near * Math.tan((baseFov * DEG) * 0.5)) / (slotH * 0.5);
    const left = (rect.left - slotCenterX) * scale;
    const right = (rect.left + rect.width - slotCenterX) * scale;
    const top = (slotCenterY - rect.top) * scale;
    const bottom = (slotCenterY - (rect.top + rect.height)) * scale;
    return { left, right, top, bottom, near, far, scale, slotCenterX, slotCenterY };
  }

  function projectRootsToViewportRect(roots, bounds, camera, projMeshBox, projCornerVec, applyProj, padPx = 32) {
    if (!camera || !projMeshBox || !projCornerVec || typeof projCornerVec.project !== "function") return null;
    applyProj({ left: 0, top: 0, width: bounds.vw, height: bounds.vh });
    camera.updateMatrixWorld?.();

    let uMinX = Infinity, uMinY = Infinity, uMaxX = -Infinity, uMaxY = -Infinity;
    let pMinX = Infinity, pMinY = Infinity, pMaxX = -Infinity, pMaxY = -Infinity;
    let any = false;
    let anyPrimary = false;
    for (let rootIdx = 0; rootIdx < roots.length; rootIdx++) {
      const rootObj = roots[rootIdx];
      if (!rootObj || typeof rootObj.traverse !== "function") continue;
      const isPrimary = rootIdx === 0;
      rootObj.updateMatrixWorld?.(true);
      rootObj.traverse(obj => {
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
            !Number.isFinite(max.x) || !Number.isFinite(max.y) || !Number.isFinite(max.z)) return;
        for (let i = 0; i < 8; i++) {
          projCornerVec.set(
            (i & 1) ? max.x : min.x,
            (i & 2) ? max.y : min.y,
            (i & 4) ? max.z : min.z
          ).project(camera);
          if (!Number.isFinite(projCornerVec.x) || !Number.isFinite(projCornerVec.y) || !Number.isFinite(projCornerVec.z)) continue;
          if (projCornerVec.z < -1 || projCornerVec.z > 1) continue;
          const sx = (projCornerVec.x * 0.5 + 0.5) * bounds.vw;
          const sy = (-projCornerVec.y * 0.5 + 0.5) * bounds.vh;
          if (sx < uMinX) uMinX = sx;
          if (sx > uMaxX) uMaxX = sx;
          if (sy < uMinY) uMinY = sy;
          if (sy > uMaxY) uMaxY = sy;
          any = true;
          if (isPrimary) {
            if (sx < pMinX) pMinX = sx;
            if (sx > pMaxX) pMaxX = sx;
            if (sy < pMinY) pMinY = sy;
            if (sy > pMaxY) pMaxY = sy;
            anyPrimary = true;
          }
        }
      });
    }
    if (!any || uMaxX <= uMinX || uMaxY <= uMinY) return null;
    const primary = anyPrimary && pMaxX > pMinX && pMaxY > pMinY
      ? {
          left: clamp(Math.floor(pMinX), 0, bounds.vw),
          top: clamp(Math.floor(pMinY), 0, bounds.vh),
          width: Math.max(1, clamp(Math.ceil(pMaxX), 0, bounds.vw) - clamp(Math.floor(pMinX), 0, bounds.vw)),
          height: Math.max(1, clamp(Math.ceil(pMaxY), 0, bounds.vh) - clamp(Math.floor(pMinY), 0, bounds.vh))
        }
      : null;
    return {
      left: uMinX - padPx,
      top: uMinY - padPx,
      width: (uMaxX - uMinX) + padPx * 2,
      height: (uMaxY - uMinY) + padPx * 2,
      primary
    };
  }

  Object.assign(C, {
    vrmDizzyFx: {
      computeDizzyLayout,
      drawDizzyFx,
      drawActionFx,
      DIZZY_STAR_COLORS
    },
    vrmOverlayMath: {
      BASE_FOV,
      readChromeBounds,
      clampRectToChrome,
      rectContains,
      computeAsymmetricFrustum,
      projectRootsToViewportRect
    }
  });
})(window.WsCompanion);
