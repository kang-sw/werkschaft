(function (C) {
  "use strict";
  const RAD = Math.PI / 180;
  const maxBones = 16, maxRegionPoints = 64, maxFeather = 256;
  const defaultGrid = [12, 16], defaultFeather = 24;
  // Triangles are clipped 0.5 px wider than their rest outline so adjacent
  // triangles overlap and no seam hairline shows between them.
  const expand = 0.5, expandCap = 4, epsilon = 1e-4;

  function finite(x, fallback = 0) { return Number.isFinite(x) ? x : fallback; }
  function clamp(x, low, high) { return Math.min(high, Math.max(low, x)); }
  function record(value) { return value && typeof value === "object" && !Array.isArray(value); }
  function pair(value) {
    return Array.isArray(value) && Number.isFinite(value[0]) && Number.isFinite(value[1]) ? [value[0], value[1]] : null;
  }

  // gridSize reads deform.grid ([columns, rows]); each value is an integer
  // clamped to 2..32 and a malformed grid falls back to the default.
  function gridSize(spec) {
    const grid = Array.isArray(spec?.grid) && spec.grid.length === 2 ? spec.grid : defaultGrid;
    return grid.map((value, i) => Number.isFinite(value) ? clamp(Math.round(value), 2, 32) : defaultGrid[i]);
  }

  function polygon(value) {
    if (!Array.isArray(value)) return null;
    const out = [];
    for (const item of value.slice(0, maxRegionPoints)) {
      const p = pair(item);
      if (p) out.push(p[0], p[1]);
    }
    return out.length >= 6 ? Float64Array.from(out) : null;
  }

  // parseBones turns deform.bones into a flat list ordered parents first.
  // Each bone keeps its pivot, region polygon, feather, motion spec and swing
  // terms; `parent` is an index into the same list (-1 for a top-level bone).
  // A bone without a finite pivot or a region of 3 or more points is skipped,
  // an unknown parent makes a bone top-level, and a parent cycle drops the
  // bones on and below it.
  function parseBones(spec) {
    const raw = record(spec?.bones) ? spec.bones : {};
    const items = [];
    for (const id of Object.keys(raw).slice(0, maxBones)) {
      const bone = raw[id];
      if (!record(bone)) continue;
      const pivot = pair(bone.pivot), region = polygon(bone.region);
      if (!pivot || !region) continue;
      const weight = Number.isFinite(bone.swingWeight) ? clamp(bone.swingWeight, -2, 2) : null;
      items.push({
        id, parentId: typeof bone.parent === "string" ? bone.parent : null, pivot, region,
        feather: Number.isFinite(bone.feather) ? clamp(bone.feather, 0, maxFeather) : defaultFeather,
        motion: bone.motion, weight, lag: Number.isFinite(bone.swingLag) ? clamp(bone.swingLag, -0.5, 0.5) : 0
      });
    }
    const byId = new Map(items.map(item => [item.id, item]));
    const depth = new Map();
    for (const item of items) {
      let steps = 0, node = item;
      while (node && steps <= items.length) { node = byId.get(node.parentId); steps++; }
      if (steps <= items.length) depth.set(item, steps - 1);
    }
    const ordered = items.filter(item => depth.has(item)).sort((a, b) => depth.get(a) - depth.get(b));
    const index = new Map(ordered.map((item, i) => [item.id, i]));
    for (const item of ordered) item.parent = index.has(item.parentId) ? index.get(item.parentId) : -1;
    return ordered;
  }

  // regionWeight is 1 inside the polygon and falls to 0 over `feather` px
  // outside it on a smoothstep.
  function regionWeight(poly, feather, x, y) {
    const n = poly.length >> 1;
    let inside = false, best = Infinity;
    for (let i = 0, j = n - 1; i < n; j = i++) {
      const xi = poly[2 * i], yi = poly[2 * i + 1], xj = poly[2 * j], yj = poly[2 * j + 1];
      if ((yi > y) !== (yj > y) && x < (xj - xi) * (y - yi) / (yj - yi) + xi) inside = !inside;
      const dx = xj - xi, dy = yj - yi, len = dx * dx + dy * dy;
      const t = len > 0 ? clamp(((x - xi) * dx + (y - yi) * dy) / len, 0, 1) : 0;
      const ex = xi + t * dx - x, ey = yi + t * dy - y;
      best = Math.min(best, ex * ex + ey * ey);
    }
    if (inside) return 1;
    if (!(feather > 0)) return 0;
    const t = Math.sqrt(best) / feather;
    return t >= 1 ? 0 : 1 - t * t * (3 - 2 * t);
  }

  // coefficients fills out[0..n-1] with the weight each bone's chain
  // transform carries at (x, y) and out[n] with the weight left on the rest
  // pose; they sum to 1. Raw region weights are normalized per sibling set
  // (bones sharing a parent): a set whose weights sum past 1 is scaled down,
  // and the remainder stays on the parent's chain transform (the rest pose
  // for top-level bones). A child blends its parent's chain (1 - w) against
  // its own (w), so a vertex inside a hip and its knee takes the full knee
  // chain, not an average.
  function coefficients(bones, x, y, out = new Float64Array(bones.length + 1)) {
    const n = bones.length;
    const raw = new Float64Array(n), sums = new Float64Array(n + 1);
    for (let b = 0; b < n; b++) {
      raw[b] = regionWeight(bones[b].region, bones[b].feather, x, y);
      sums[bones[b].parent + 1] += raw[b];
    }
    const w = new Float64Array(n), kids = new Float64Array(n + 1);
    for (let b = 0; b < n; b++) {
      w[b] = raw[b] / Math.max(1, sums[bones[b].parent + 1]);
      kids[bones[b].parent + 1] += w[b];
    }
    const path = new Float64Array(n);
    for (let b = 0; b < n; b++) {
      path[b] = (bones[b].parent < 0 ? 1 : path[bones[b].parent]) * w[b];
      out[b] = path[b] * Math.max(0, 1 - kids[b + 1]);
    }
    out[n] = Math.max(0, 1 - kids[0]);
    return out;
  }

  // chain composes each bone's pivot rotation and translation after its
  // parent's, writing 2x3 matrices [a, b, c, d, e, f] (x' = ax + cy + e,
  // y' = bx + dy + f) into out. Bones are ordered parents first.
  function chain(bones, locals, out) {
    for (let b = 0; b < bones.length; b++) {
      const [px, py] = bones[b].pivot, o = b * 6, l = b * 3;
      const rot = locals[l + 2], cos = Math.cos(rot), sin = Math.sin(rot);
      const la = cos, lb = sin, lc = -sin, ld = cos;
      const le = px + locals[l] - cos * px + sin * py, lf = py + locals[l + 1] - sin * px - cos * py;
      const p = bones[b].parent * 6;
      if (bones[b].parent < 0) {
        out[o] = la; out[o + 1] = lb; out[o + 2] = lc; out[o + 3] = ld; out[o + 4] = le; out[o + 5] = lf;
      } else {
        out[o] = out[p] * la + out[p + 2] * lb;
        out[o + 1] = out[p + 1] * la + out[p + 3] * lb;
        out[o + 2] = out[p] * lc + out[p + 2] * ld;
        out[o + 3] = out[p + 1] * lc + out[p + 3] * ld;
        out[o + 4] = out[p] * le + out[p + 2] * lf + out[p + 4];
        out[o + 5] = out[p + 1] * le + out[p + 3] * lf + out[p + 5];
      }
    }
    return out;
  }

  // compile builds the static mesh of one composed column canvas: the grid
  // over its opaque bounds, per-vertex bone coefficients (computed once),
  // the active deformed triangles with their inverse rest matrices, and the
  // row runs of active cells no bone moves (drawn rigidly in one clip).
  // `pixels` is the canvas RGBA data (or empty when unreadable, which treats
  // the whole canvas as opaque).
  function compile(spec, width, height, pixels) {
    const bones = parseBones(spec);
    if (!bones.length) return null;
    const [cols, rows] = gridSize(spec);
    let x0 = 0, y0 = 0, x1 = width, y1 = height;
    if (pixels && pixels.length >= width * height * 4) {
      let minX = width, minY = height, maxX = 0, maxY = 0;
      for (let y = 0; y < height; y++) for (let x = 0; x < width; x++) {
        if (pixels[(y * width + x) * 4 + 3] === 0) continue;
        if (x < minX) minX = x;
        if (x + 1 > maxX) maxX = x + 1;
        if (y < minY) minY = y;
        if (y + 1 > maxY) maxY = y + 1;
      }
      if (maxX <= minX || maxY <= minY) return null;
      x0 = minX; y0 = minY; x1 = maxX; y1 = maxY;
    } else pixels = null;
    const n = bones.length, nv = (cols + 1) * (rows + 1);
    const rest = new Float64Array(nv * 2), start = new Uint32Array(nv + 1);
    const hold = [], alpha = [];
    const scratch = new Float64Array(n + 1);
    const still = new Uint8Array(nv);
    for (let j = 0; j <= rows; j++) for (let i = 0; i <= cols; i++) {
      const v = j * (cols + 1) + i, x = x0 + (x1 - x0) * i / cols, y = y0 + (y1 - y0) * j / rows;
      rest[2 * v] = x; rest[2 * v + 1] = y;
      coefficients(bones, x, y, scratch);
      for (let b = 0; b < n; b++) if (scratch[b] > 0) { hold.push(b); alpha.push(scratch[b]); }
      start[v + 1] = hold.length;
      still[v] = start[v + 1] === start[v] ? 1 : 0;
    }
    function covered(i, j) {
      if (!pixels) return true;
      const xa = Math.floor(rest[2 * (j * (cols + 1) + i)]), xb = Math.ceil(rest[2 * (j * (cols + 1) + i + 1)]);
      const ya = Math.floor(rest[2 * (j * (cols + 1) + i) + 1]), yb = Math.ceil(rest[2 * ((j + 1) * (cols + 1) + i) + 1]);
      for (let y = ya; y < Math.min(yb, height); y++) for (let x = xa; x < Math.min(xb, width); x++) {
        if (pixels[(y * width + x) * 4 + 3] !== 0) return true;
      }
      return false;
    }
    const tris = [], runs = [];
    for (let j = 0; j < rows; j++) {
      let runStart = -1;
      for (let i = 0; i <= cols; i++) {
        const v00 = j * (cols + 1) + i, v10 = v00 + 1, v01 = v00 + cols + 1, v11 = v01 + 1;
        const active = i < cols && covered(i, j);
        const fixed = active && still[v00] && still[v10] && still[v01] && still[v11];
        if (fixed) { if (runStart < 0) runStart = i; continue; }
        if (runStart >= 0) {
          runs.push(rest[2 * (j * (cols + 1) + runStart)], rest[2 * v00 + 1], rest[2 * v00], rest[2 * v01 + 1]);
          runStart = -1;
        }
        if (!active) continue;
        for (const t of [[v00, v10, v11], [v00, v11, v01]]) {
          const [a, b, c] = t;
          const r00 = rest[2 * b] - rest[2 * a], r01 = rest[2 * c] - rest[2 * a];
          const r10 = rest[2 * b + 1] - rest[2 * a + 1], r11 = rest[2 * c + 1] - rest[2 * a + 1];
          const det = r00 * r11 - r01 * r10;
          if (Math.abs(det) < 1e-6) continue;
          const sx = clamp(Math.floor(Math.min(rest[2 * a], rest[2 * b], rest[2 * c])) - 2, 0, width);
          const sy = clamp(Math.floor(Math.min(rest[2 * a + 1], rest[2 * b + 1], rest[2 * c + 1])) - 2, 0, height);
          const ex = clamp(Math.ceil(Math.max(rest[2 * a], rest[2 * b], rest[2 * c])) + 2, 0, width);
          const ey = clamp(Math.ceil(Math.max(rest[2 * a + 1], rest[2 * b + 1], rest[2 * c + 1])) + 2, 0, height);
          tris.push({ a, b, c, i00: r11 / det, i01: -r01 / det, i10: -r10 / det, i11: r00 / det, sx, sy, sw: ex - sx, sh: ey - sy });
        }
      }
    }
    // Static runs are [x, y0, x1, y1] per row run, widened by the seam margin.
    const rects = new Float64Array(runs.length);
    for (let k = 0; k < runs.length; k += 4) {
      rects[k] = runs[k] - expand; rects[k + 1] = runs[k + 1] - expand;
      rects[k + 2] = runs[k + 2] - runs[k] + 2 * expand; rects[k + 3] = runs[k + 3] - runs[k + 1] + 2 * expand;
    }
    return { bones, cols, rows, rest, start, hold: Uint8Array.from(hold), alpha: Float64Array.from(alpha), tris, rects, bounds: [x0, y0, x1, y1], width, height };
  }

  // instance binds one compiled mesh to per-view motion evaluators and
  // scratch buffers. `bind(bone)` returns the bone's motion function (or
  // null) and `swing(bone, physics)` its drag angle; `anchor` is the head
  // anchor whose displacement update() reports in `shift`. Nothing allocates
  // per frame.
  function instance(core, canvas, anchor, bind, swing) {
    const { bones, rest, start, hold, alpha, tris, rects } = core;
    const n = bones.length, nv = rest.length >> 1;
    const motions = bones.map(bind);
    const locals = new Float64Array(n * 3), chains = new Float64Array(n * 6), pos = new Float64Array(nv * 2);
    const own = coefficients(bones, anchor[0], anchor[1]);
    const shift = { x: 0, y: 0 };
    const mesh = {
      shift, identity: true,
      articulated: bones.some(bone => bone.weight !== null),
      update(t, physics) {
        let moving = false;
        for (let b = 0; b < n; b++) {
          const m = motions[b]?.(t);
          const tx = clamp(finite(m?.tx), -256, 256), ty = clamp(finite(m?.ty), -256, 256);
          const rot = clamp(finite(m?.rotDeg) * RAD + swing(bones[b], physics), -Math.PI, Math.PI);
          locals[b * 3] = tx; locals[b * 3 + 1] = ty; locals[b * 3 + 2] = rot;
          if (Math.abs(tx) > epsilon || Math.abs(ty) > epsilon || Math.abs(rot) > epsilon * RAD) moving = true;
        }
        mesh.identity = !moving;
        shift.x = shift.y = 0;
        if (!moving) return;
        chain(bones, locals, chains);
        for (let v = 0; v < nv; v++) {
          const x = rest[2 * v], y = rest[2 * v + 1];
          let px = x, py = y;
          if (start[v + 1] > start[v]) {
            let sum = 1;
            px = py = 0;
            for (let k = start[v]; k < start[v + 1]; k++) {
              const o = hold[k] * 6, w = alpha[k];
              px += w * (chains[o] * x + chains[o + 2] * y + chains[o + 4]);
              py += w * (chains[o + 1] * x + chains[o + 3] * y + chains[o + 5]);
              sum -= w;
            }
            px += sum * x; py += sum * y;
          }
          pos[2 * v] = px; pos[2 * v + 1] = py;
        }
        let hx = anchor[0], hy = anchor[1];
        for (let b = 0; b < n; b++) {
          const o = b * 6, w = own[b];
          if (w <= 0) continue;
          hx += w * (chains[o] * anchor[0] + chains[o + 2] * anchor[1] + chains[o + 4] - anchor[0]);
          hy += w * (chains[o + 1] * anchor[0] + chains[o + 3] * anchor[1] + chains[o + 5] - anchor[1]);
        }
        shift.x = hx - anchor[0]; shift.y = hy - anchor[1];
      },
      // draw paints the column canvas: a plain drawImage when every bone is
      // at rest, else the static cells in one clip plus one clipped affine
      // drawImage per deformed triangle.
      draw(ctx) {
        if (mesh.identity) { ctx.drawImage(canvas, 0, 0); return; }
        const [bx0, by0, bx1, by1] = core.bounds;
        if (rects.length) {
          ctx.save();
          ctx.beginPath();
          for (let k = 0; k < rects.length; k += 4) ctx.rect(rects[k], rects[k + 1], rects[k + 2], rects[k + 3]);
          ctx.clip();
          ctx.drawImage(canvas, bx0, by0, bx1 - bx0, by1 - by0, bx0, by0, bx1 - bx0, by1 - by0);
          ctx.restore();
        }
        for (let k = 0; k < tris.length; k++) {
          const tri = tris[k], a = tri.a * 2, b = tri.b * 2, c = tri.c * 2;
          const qax = pos[a], qay = pos[a + 1], qbx = pos[b], qby = pos[b + 1], qcx = pos[c], qcy = pos[c + 1];
          const area2 = Math.abs((qbx - qax) * (qcy - qay) - (qcx - qax) * (qby - qay));
          if (area2 < 1e-6) continue;
          const la = Math.hypot(qbx - qcx, qby - qcy), lb = Math.hypot(qax - qcx, qay - qcy), lc = Math.hypot(qax - qbx, qay - qby);
          const per = la + lb + lc, radius = area2 / per;
          const grow = Math.min(expandCap, 1 + expand / Math.max(radius, 1e-6));
          const ix = (la * qax + lb * qbx + lc * qcx) / per, iy = (la * qay + lb * qby + lc * qcy) / per;
          const d00 = qbx - qax, d01 = qcx - qax, d10 = qby - qay, d11 = qcy - qay;
          const a00 = d00 * tri.i00 + d01 * tri.i10, a01 = d00 * tri.i01 + d01 * tri.i11;
          const a10 = d10 * tri.i00 + d11 * tri.i10, a11 = d10 * tri.i01 + d11 * tri.i11;
          const p0x = rest[a], p0y = rest[a + 1];
          ctx.save();
          ctx.beginPath();
          ctx.moveTo(ix + (qax - ix) * grow, iy + (qay - iy) * grow);
          ctx.lineTo(ix + (qbx - ix) * grow, iy + (qby - iy) * grow);
          ctx.lineTo(ix + (qcx - ix) * grow, iy + (qcy - iy) * grow);
          ctx.closePath();
          ctx.clip();
          // transform (not setTransform) composes onto the roll rotation.
          ctx.transform(a00, a10, a01, a11, qax - a00 * p0x - a01 * p0y, qay - a10 * p0x - a11 * p0y);
          ctx.drawImage(canvas, tri.sx, tri.sy, tri.sw, tri.sh, tri.sx, tri.sy, tri.sw, tri.sh);
          ctx.restore();
        }
      }
    };
    return mesh;
  }

  Object.assign(C, { deform: { gridSize, parseBones, regionWeight, coefficients, chain, compile, instance } });
})(window.WsCompanion);
