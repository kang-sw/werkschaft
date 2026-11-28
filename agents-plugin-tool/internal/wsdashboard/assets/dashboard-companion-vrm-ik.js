(function (C) {
  "use strict";
  // Analytical 2-bone arm IK, finger curl resolution, and D7 Target Attach (VRM 1.0 +X left, +Y up, +Z forward, Euler XYZ).
  const DEG = Math.PI / 180, DEF_L = 0.24, warnedAttachSpaces = new Set();
  const FINGER_SPREAD = Object.freeze({ index: -1, middle: 0, ring: 0.5, little: 1 });
  const FINGER_SEGS = Object.freeze({
    Thumb: ["Metacarpal", "Proximal", "Distal"],
    Index: ["Proximal", "Intermediate", "Distal"], Middle: ["Proximal", "Intermediate", "Distal"],
    Ring: ["Proximal", "Intermediate", "Distal"], Little: ["Proximal", "Intermediate", "Distal"]
  });
  const clamp = (v, lo, hi) => (v < lo ? lo : v > hi ? hi : v);
  const len3 = v => Math.hypot(v[0], v[1], v[2]);
  const dot3 = (a, b) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
  const cross3 = (a, b) => [a[1] * b[2] - a[2] * b[1], a[2] * b[0] - a[0] * b[2], a[0] * b[1] - a[1] * b[0]];
  const norm3 = (v, fb) => { const l = len3(v); return l > 1e-6 ? [v[0] / l, v[1] / l, v[2] / l] : (fb ? fb.slice() : [0, 0, 1]); };

  function basisToEulerXYZ(x, y, z) {
    const ry = Math.asin(clamp(z[0], -1, 1));
    return Math.abs(z[0]) < 0.9999999 ? [Math.atan2(-z[1], z[2]), ry, Math.atan2(-y[0], x[0])] : [Math.atan2(y[2], y[1]), ry, 0];
  }

  function solveTwoBoneArmIK(opts) {
    const side = opts?.side === "right" ? "right" : "left", sgn = side === "left" ? 1 : -1;
    const s = Array.isArray(opts?.shoulder) ? opts.shoulder : [sgn * 0.14, 0.12, 0];
    const t = Array.isArray(opts?.target) ? opts.target : [sgn * 0.2, 0, 0.3];
    const L1 = Number.isFinite(opts?.upperLen) && opts.upperLen > 1e-4 ? opts.upperLen : DEF_L;
    const L2 = Number.isFinite(opts?.lowerLen) && opts.lowerLen > 1e-4 ? opts.lowerLen : DEF_L;
    const restDir = [sgn, 0, 0];
    const dx = t[0] - s[0], dy = t[1] - s[1], dz = t[2] - s[2], rawDist = Math.hypot(dx, dy, dz);
    const minD = Math.abs(L1 - L2) + 0.004, maxD = (L1 + L2) * 0.998, d = clamp(rawDist, minD, maxD);
    const u = rawDist > 1e-6 ? [dx / rawDist, dy / rawDist, dz / rawDist] : restDir;
    const cosA = clamp((L1 * L1 + d * d - L2 * L2) / (2 * L1 * d), -1, 1), sinA = Math.sqrt(Math.max(0, 1 - cosA * cosA));
    const poleIn = Array.isArray(opts?.pole) && opts.pole.length >= 3 && opts.pole.every(Number.isFinite) ? opts.pole : [sgn * 0.18, -0.35, -0.92];
    const proj = v => { const pd = dot3(v, u); return [v[0] - u[0] * pd, v[1] - u[1] * pd, v[2] - u[2] * pd]; };
    let perp = proj(poleIn);
    if (len3(perp) < 1e-5) perp = proj(Math.abs(u[2]) < 0.9 ? [0, -0.35, -0.94] : [0, -1, 0]);
    const bD = norm3(perp, [0, -1, 0]);
    const elbow = [s[0] + u[0] * L1 * cosA + bD[0] * L1 * sinA, s[1] + u[1] * L1 * cosA + bD[1] * L1 * sinA, s[2] + u[2] * L1 * cosA + bD[2] * L1 * sinA];
    const end = [s[0] + u[0] * d, s[1] + u[1] * d, s[2] + u[2] * d];
    const d1 = norm3([elbow[0] - s[0], elbow[1] - s[1], elbow[2] - s[2]], restDir);
    const d2 = norm3([end[0] - elbow[0], end[1] - elbow[1], end[2] - elbow[2]], d1);
    const x1 = [sgn * d1[0], sgn * d1[1], sgn * d1[2]];
    const y1 = norm3(side === "left" ? cross3(d1, bD) : cross3(bD, d1), [0, 1, 0]);
    const z1 = norm3(cross3(x1, y1), [0, 0, 1]);
    const x2 = [sgn * d2[0], sgn * d2[1], sgn * d2[2]];
    const z2 = norm3(cross3(x2, y1), z1), y2 = norm3(cross3(z2, x2), y1);
    const col = c => [dot3(x1, c), dot3(y1, c), dot3(z1, c)];
    return { upperEuler: basisToEulerXYZ(x1, y1, z1), lowerEuler: basisToEulerXYZ(col(x2), col(y2), col(z2)), elbow, end, clamped: rawDist < minD || rawDist > maxD };
  }

  function measureArmChain(vrm, side, THREE) {
    const p = side === "right" ? "right" : "left";
    const fb = { side: p, upperLen: DEF_L, lowerLen: DEF_L, chainLen: DEF_L * 2, chestPos: [0, 1.18, 0], shoulderOffset: [p === "right" ? -0.14 : 0.14, 0.12, 0] };
    if (!vrm?.humanoid || typeof THREE?.Vector3 !== "function") return fb;
    const h = vrm.humanoid, chest = h.getNormalizedBoneNode("upperChest") || h.getNormalizedBoneNode("chest") || h.getNormalizedBoneNode("spine");
    const upper = h.getNormalizedBoneNode(p + "UpperArm"), lower = h.getNormalizedBoneNode(p + "LowerArm"), hand = h.getNormalizedBoneNode(p + "Hand");
    if (!upper || !lower || !hand) return fb;
    const cP = new THREE.Vector3(0, 1.18, 0), uP = new THREE.Vector3(), lP = new THREE.Vector3(), hP = new THREE.Vector3();
    if (chest) chest.getWorldPosition(cP);
    upper.getWorldPosition(uP); lower.getWorldPosition(lP); hand.getWorldPosition(hP);
    const upperLen = Math.max(0.08, uP.distanceTo(lP)), lowerLen = Math.max(0.08, lP.distanceTo(hP));
    return { side: p, upperLen, lowerLen, chainLen: upperLen + lowerLen, chestPos: [cP.x, cP.y, cP.z], shoulderOffset: [uP.x - cP.x, uP.y - cP.y, uP.z - cP.z] };
  }

  const chainScale = ch => (Number.isFinite(ch?.chainLen) && ch.chainLen > 0.05 ? ch.chainLen : DEF_L * 2);
  function chestLocalToWorld(pos, ch) {
    const c = ch?.chestPos || [0, 1.18, 0], s = chainScale(ch);
    return [c[0] + (pos[0] || 0) * s, c[1] + (pos[1] || 0) * s, c[2] + (pos[2] || 0) * s];
  }
  function worldToChestLocal(pos, ch) {
    const c = ch?.chestPos || [0, 1.18, 0], s = chainScale(ch);
    return [((pos[0] || 0) - c[0]) / s, ((pos[1] || 0) - c[1]) / s, ((pos[2] || 0) - c[2]) / s];
  }

  function resolveTargetAttach(spec, rigCtx) {
    if (!spec || typeof spec !== "object") return null;
    const space = typeof spec.space === "string" ? spec.space.trim() : "";
    if (!space || space === "free") return null;
    const weight = Number.isFinite(spec.weight) ? clamp(spec.weight, 0, 1) : 1;
    if (weight <= 0.001) return null;
    const off = Array.isArray(spec.offset) && spec.offset.length >= 3 ? [Number(spec.offset[0]) || 0, Number(spec.offset[1]) || 0, Number(spec.offset[2]) || 0] : [0, 0, 0];
    let base = null;
    if (space === "camera") {
      const cam = rigCtx?.camera;
      if (cam?.position && Number.isFinite(cam.position.x)) base = [cam.position.x, cam.position.y, cam.position.z];
    } else if (space.startsWith("prop:")) {
      const rest = space.slice(5), i = rest.indexOf("#");
      const pt = rigCtx?.getPropAnchorWorld?.(i >= 0 ? rest.slice(0, i) : rest, i >= 0 ? rest.slice(i + 1) : "");
      if (pt && Number.isFinite(pt.x)) base = [pt.x, pt.y, pt.z];
      else if (Array.isArray(pt) && pt.length >= 3 && pt.every(Number.isFinite)) base = pt;
      if (!base && !warnedAttachSpaces.has(space)) {
        warnedAttachSpaces.add(space);
        console?.warn?.("[ws-companion] unknown target_attach prop anchor, falling back to free:", space);
      }
    } else if (space.startsWith("bone:")) {
      const node = rigCtx?.vrm?.humanoid?.getNormalizedBoneNode?.(space.slice(5));
      if (node?.getWorldPosition && rigCtx?.THREE?.Vector3) {
        const v = rigCtx.scratchVec || (rigCtx.scratchVec = new rigCtx.THREE.Vector3());
        node.getWorldPosition(v);
        if (Number.isFinite(v.x)) base = [v.x, v.y, v.z];
      }
    }
    return base ? { x: base[0] + off[0], y: base[1] + off[1], z: base[2] + off[2], weight, space } : null;
  }

  function worldTargetToHeadAngles(headPos, pt) {
    const dx = pt.x - headPos[0], dy = pt.y - headPos[1], dz = pt.z - headPos[2];
    return { yaw: clamp(Math.atan2(dx, Math.max(0.05, dz)), -55 * DEG, 55 * DEG), pitch: clamp(-Math.atan2(dy, Math.max(1e-4, Math.hypot(dx, dz))), -35 * DEG, 35 * DEG) };
  }

  function resolveFingerSpec(spec) {
    const presets = C.vrmCoLeadPose?.FINGER_PRESETS || {};
    const fb = presets.relaxed || { thumb: [0.16, 0.18, 0.14], thumbOpp: 0.2, index: [0.2, 0.24, 0.14], middle: [0.28, 0.32, 0.18], ring: [0.34, 0.38, 0.22], little: [0.4, 0.42, 0.26], spread: 0.05 };
    const p = typeof spec?.preset === "string" && Object.hasOwn(presets, spec.preset) ? presets[spec.preset] : fb;
    const base = [...p.thumb, ...p.index, ...p.middle, ...p.ring, ...p.little];
    const curl = Array.isArray(spec?.curl) && spec.curl.length === 15 ? spec.curl.map((v, i) => Number.isFinite(v) ? clamp(v, -0.4, 2.0) : base[i]) : base;
    return { curl, thumb_opp: Number.isFinite(spec?.thumb_opp) ? clamp(spec.thumb_opp, -0.5, 1.5) : (p.thumbOpp ?? 0.2), spread: Number.isFinite(spec?.spread) ? clamp(spec.spread, -0.4, 0.6) : (p.spread ?? 0.05) };
  }

  function writeClipHandFingers(side, res, out) {
    const m = side === "right" ? -1 : 1;
    const put = (n, x, y, z) => { const a = out[n]; if (Array.isArray(a) && a.length >= 3) { a[0] = x; a[1] = y; a[2] = z; } else out[n] = [x, y, z]; };
    for (let i = 0; i < 3; i++) { const c = res.curl[i]; put(side + "Thumb" + FINGER_SEGS.Thumb[i], 0.7 * c, m * (i === 0 ? res.thumb_opp : 0), -m * 0.7 * c); }
    const digits = ["index", "middle", "ring", "little"];
    for (let d = 0; d < 4; d++) {
      const k = digits[d], cap = k[0].toUpperCase() + k.slice(1);
      for (let i = 0; i < 3; i++) put(side + cap + FINGER_SEGS[cap][i], 0, m * (i === 0 ? res.spread * FINGER_SPREAD[k] : 0), -m * res.curl[3 + d * 3 + i]);
    }
    return out;
  }

  Object.assign(C, { vrmIk: { solveTwoBoneArmIK, measureArmChain, chestLocalToWorld, worldToChestLocal, resolveTargetAttach, worldTargetToHeadAngles, resolveFingerSpec, writeClipHandFingers, basisToEulerXYZ } });
})(window.WsCompanion);
