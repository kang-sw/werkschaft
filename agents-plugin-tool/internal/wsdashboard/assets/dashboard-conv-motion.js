  // --- conversation live Extra Motion 2D canvas engine & registry ------------
  // Single swap-stable canvas overlay over #convo driving all #r-convlive live
  // states (thinking ink-cloud bloom, 5 tool-category 15fps sway signatures,
  // elastic pop-in transitions, 3-beat Hermite tension OK flights, sputtering
  // coral-red FAIL flights, dynamic [data-conv-turn-tokens] convergence, and
  // floating RPG +m popups). Enforces strictly 0 rAF when idle, OFF, hidden,
  // prefers-reduced-motion: reduce, or stale.
  var WsConvMotion = (function () {
    var STEP_FPS = 15;
    var STEP_DT = 1 / STEP_FPS;
    var SWAY_PERIOD = 1.68;
    var DEFAULT_STALE_MS = 90000; // wslive.StaleAfterBeats (3) * HeartbeatInterval (30s)
    var MONO_FONT = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace';

    // Modern 3-beat tension curve: Whip -> Hang Peak-Stop -> Snap-Whip!
    var TENSION_KEYS = [
      { t: 0.00, v: 0.00, m: 1.35 },
      { t: 0.41, v: 0.50, m: 0.13 },
      { t: 0.57, v: 0.53, m: 0.16 },
      { t: 1.00, v: 1.00, m: 2.55 }
    ];

    var PALETTES = {
      dark: {
        exec:     { id: "exec",     hex: "#e5b567", rgb: [229, 181, 103], hi: [253, 232, 188], sway: "spark",   swayAmp: 12.2 },
        tickets:  { id: "tickets",  hex: "#7aa2ff", rgb: [122, 162, 255], hi: [210, 220, 246], sway: "classic", swayAmp: 11.5 },
        agent:    { id: "agent",    hex: "#b392f0", rgb: [179, 146, 240], hi: [230, 217, 253], sway: "orbit",   swayAmp: 11.0 },
        git:      { id: "git",      hex: "#5ccfe6", rgb: [92, 207, 230],  hi: [198, 243, 252], sway: "weave",   swayAmp: 11.8 },
        file:     { id: "file",     hex: "#8ea6c8", rgb: [142, 166, 200], hi: [214, 226, 242], sway: "classic", swayAmp: 11.2 },
        thinking: { id: "thinking", hex: "#96a8e8", rgb: [150, 168, 232], hi: [221, 228, 252], sway: "cloud",   swayAmp: 0 },
        ok:       { hex: "#6cc39a", rgb: [108, 195, 154], hi: [200, 242, 220] },
        fail:     { hex: "#f0797e", rgb: [240, 121, 126], hi: [252, 206, 208] },
        popupBg:  "rgba(18, 21, 27, 0.72)"
      },
      light: {
        exec:     { id: "exec",     hex: "#b4690e", rgb: [180, 105, 14],  hi: [242, 198, 124], sway: "spark",   swayAmp: 12.2 },
        tickets:  { id: "tickets",  hex: "#2d5bd1", rgb: [45, 91, 209],   hi: [156, 186, 248], sway: "classic", swayAmp: 11.5 },
        agent:    { id: "agent",    hex: "#7248c7", rgb: [114, 72, 199],  hi: [196, 174, 246], sway: "orbit",   swayAmp: 11.0 },
        git:      { id: "git",      hex: "#0b7c96", rgb: [11, 124, 150],  hi: [130, 214, 232], sway: "weave",   swayAmp: 11.8 },
        file:     { id: "file",     hex: "#4b6484", rgb: [75, 100, 132],  hi: [168, 188, 212], sway: "classic", swayAmp: 11.2 },
        thinking: { id: "thinking", hex: "#4d63b8", rgb: [77, 99, 184],   hi: [170, 184, 238], sway: "cloud",   swayAmp: 0 },
        ok:       { hex: "#1d7348", rgb: [29, 115, 72],   hi: [132, 210, 168] },
        fail:     { hex: "#c0262d", rgb: [192, 38, 45],   hi: [244, 152, 156] },
        popupBg:  "rgba(255, 255, 255, 0.88)"
      }
    };

    function classifyTool(name) {
      var raw = String(name || "").trim().toLowerCase();
      if (!raw) return "file";
      // Strip common MCP prefixes so ws/exec.shell, mcp_ws_exec_shell, exec_shell match uniformly.
      var norm = raw.replace(/^mcp_/, "").replace(/^ws[/.:_]/, "");
      if (/^exec([/.:_]|$)/.test(norm) || /(^|[/.:_ -])(shell|bash|zsh|powershell|terminal|run_command|exec_shell|exec_run|command)($|[/.:_ -])/.test(raw) || raw === "sh") {
        return "exec";
      }
      if (/^(tickets?|whiteboard|todo|agenda|note|route|review|playbook)([/.:_]|$)/.test(norm) || /(^|[/.:_ -])(tickets?|whiteboard)($|[/.:_ -])/.test(raw)) {
        return "tickets";
      }
      if (/^(agent|mailbox|swarm|subagent)([/.:_]|$)/.test(norm) || /(^|[/.:_ -])(subagent|swarm|mailbox|spawn)($|[/.:_ -])/.test(raw)) {
        return "agent";
      }
      if (/^(git|worktree|rationale)([/.:_]|$)/.test(norm) || /^git[/.:_ -]/.test(raw)) {
        return "git";
      }
      return "file";
    }

    function clamp(v, lo, hi) { return v < lo ? lo : v > hi ? hi : v; }
    function lerp(a, b, t) { return a + (b - a) * t; }
    function mixRgb(a, b, t) {
      return [
        Math.round(lerp(a[0], b[0], t)),
        Math.round(lerp(a[1], b[1], t)),
        Math.round(lerp(a[2], b[2], t))
      ];
    }
    function rgba(rgb, a) {
      return "rgba(" + rgb[0] + "," + rgb[1] + "," + rgb[2] + "," + clamp(a, 0, 1).toFixed(3) + ")";
    }

    function evalHermite1D(keys, u) {
      u = clamp(u, 0, 1);
      for (var i = 0; i < keys.length - 1; i++) {
        var a = keys[i], b = keys[i + 1];
        if (u <= b.t || i === keys.length - 2) {
          var h = Math.max(1e-6, b.t - a.t);
          var s = clamp((u - a.t) / h, 0, 1);
          var s2 = s * s, s3 = s2 * s;
          var h00 = 2 * s3 - 3 * s2 + 1;
          var h10 = s3 - 2 * s2 + s;
          var h01 = -2 * s3 + 3 * s2;
          var h11 = s3 - s2;
          return h00 * a.v + h10 * h * a.m + h01 * b.v + h11 * h * b.m;
        }
      }
      return 1;
    }
    function warpTension(u) { return clamp(evalHermite1D(TENSION_KEYS, u), 0, 1); }

    function warpFail(u) {
      u = clamp(u, 0, 1);
      var base = u < 0.72
        ? 0.74 * Math.pow(u / 0.72, 0.92)
        : 0.74 + 0.26 * Math.pow((u - 0.72) / 0.28, 1.55);
      var cough =
        -0.024 * Math.exp(-Math.pow((u - 0.22) / 0.045, 2)) -
        0.028 * Math.exp(-Math.pow((u - 0.56) / 0.055, 2)) -
        0.020 * Math.exp(-Math.pow((u - 0.80) / 0.040, 2));
      return clamp(base + cough, 0, 1);
    }

    function evalHermite2D(segs, s) {
      s = clamp(s, 0, 1);
      var n = segs.length;
      var scaled = s * n;
      var idx = Math.min(n - 1, Math.floor(scaled));
      var u = scaled - idx;
      var seg = segs[idx];
      var u2 = u * u, u3 = u2 * u;
      var h00 = 2 * u3 - 3 * u2 + 1;
      var h10 = u3 - 2 * u2 + u;
      var h01 = -2 * u3 + 3 * u2;
      var h11 = u3 - u2;
      return {
        x: h00 * seg.p0[0] + h10 * seg.m0[0] + h01 * seg.p1[0] + h11 * seg.m1[0],
        y: h00 * seg.p0[1] + h10 * seg.m0[1] + h01 * seg.p1[1] + h11 * seg.m1[1]
      };
    }

    function pickNonRepeating(count, lastKind, rng) {
      var r = typeof rng === "function" ? rng : Math.random;
      var pick = Math.floor(r() * count);
      if (pick === lastKind) pick = (pick + 1 + Math.floor(r() * (count - 1))) % count;
      return pick;
    }

    function buildOkFlightProfile(lastKind, ox, oy, tx, ty, launchVx, launchVy, W, H, rng) {
      var r = typeof rng === "function" ? rng : Math.random;
      function rr(a, b) { return a + r() * (b - a); }
      var kind = pickNonRepeating(5, lastKind, r);
      var dx = tx - ox;
      var spanX = Math.abs(dx) < 60 ? (dx >= 0 ? 110 : -110) : dx;
      var dy = ty - oy;
      var rise = Math.max(46, Math.min(180, Math.abs(dy) || 72));
      var midY = lerp(oy, ty, 0.5);
      var minY = 10, maxY = Math.max(40, (H || 260) - 10);
      var maxX = Math.max(160, (W || 520) - 14);
      var m0x = clamp((launchVx || 70) * 0.42 + (spanX >= 0 ? 45 : -45), -170, 170);
      var m0y = clamp((launchVy || 0) * 0.35 - 16, -65, 45);
      var segs, dur, name;

      if (kind === 0) {
        name = "High Loft Apex Dive";
        dur = rr(0.84, 0.96);
        var apexX = clamp(ox + spanX * rr(0.46, 0.58), 24, maxX);
        var apexY = clamp(Math.min(oy, ty) - rise * rr(0.28, 0.44), minY, maxY);
        segs = [
          { p0: [ox, oy], m0: [m0x + spanX * 0.14, m0y - 34], p1: [apexX, apexY], m1: [spanX * 0.44, -8] },
          { p0: [apexX, apexY], m0: [spanX * 0.44, -8], p1: [tx, ty], m1: [spanX * 0.36, clamp(ty - apexY, -120, 120) * 0.65] }
        ];
      } else if (kind === 1) {
        name = "Under-Scoop Slingshot";
        dur = rr(0.82, 0.94);
        var dipX = clamp(ox + spanX * rr(0.32, 0.44), 24, maxX);
        var dipY = clamp(Math.max(oy, ty) + rr(12, 24), minY, maxY);
        segs = [
          { p0: [ox, oy], m0: [m0x + spanX * 0.12, rr(18, 30)], p1: [dipX, dipY], m1: [spanX * 0.56, -24] },
          { p0: [dipX, dipY], m0: [spanX * 0.56, -24], p1: [tx, ty], m1: [spanX * 0.24, -rise * 0.85] }
        ];
      } else if (kind === 2) {
        name = "Tight Diagonal S-Whip";
        dur = rr(0.84, 0.98);
        var m1x = clamp(ox + spanX * rr(0.36, 0.46), 24, maxX);
        var m1y = clamp(midY - rr(14, 28), minY, maxY);
        var m2x = clamp(ox + spanX * rr(0.64, 0.74), 24, maxX);
        var m2y = clamp(ty + rr(10, 22), minY, maxY);
        segs = [
          { p0: [ox, oy], m0: [m0x + spanX * 0.16, m0y - 24], p1: [m1x, m1y], m1: [spanX * 0.28, rr(-4, 14)] },
          { p0: [m1x, m1y], m0: [spanX * 0.28, rr(-4, 14)], p1: [m2x, m2y], m1: [spanX * 0.34, -22] },
          { p0: [m2x, m2y], m0: [spanX * 0.34, -22], p1: [tx, ty], m1: [spanX * 0.30, -34] }
        ];
      } else if (kind === 3) {
        name = "Sleek Single Ribbon Bank";
        dur = rr(0.76, 0.88);
        var bankX = clamp(ox + spanX * rr(0.50, 0.62), 24, maxX);
        var bankY = clamp(lerp(oy, ty, 0.62) - rr(10, 20), minY, maxY);
        segs = [
          { p0: [ox, oy], m0: [m0x + spanX * 0.24, -18], p1: [bankX, bankY], m1: [spanX * 0.46, -26] },
          { p0: [bankX, bankY], m0: [spanX * 0.46, -26], p1: [tx, ty], m1: [spanX * 0.42, -16] }
        ];
      } else {
        name = "Short Punchy Snap-Dart";
        dur = rr(0.72, 0.82);
        var dartX = clamp(ox + spanX * rr(0.42, 0.54), 24, maxX);
        var dartY = clamp(lerp(oy, ty, 0.48) - rr(8, 16), minY, maxY);
        segs = [
          { p0: [ox, oy], m0: [m0x + spanX * 0.26, -12], p1: [dartX, dartY], m1: [spanX * 0.54, -22] },
          { p0: [dartX, dartY], m0: [spanX * 0.54, -22], p1: [tx, ty], m1: [spanX * 0.50, -18] }
        ];
      }

      return { kind: kind, name: name, segs: segs, dur: dur, origTx: tx, origTy: ty, splatSeed: Math.floor(r() * 1000) };
    }

    function buildFailFlightProfile(lastKind, ox, oy, tx, ty, launchVx, W, H, rng) {
      var r = typeof rng === "function" ? rng : Math.random;
      function rr(a, b) { return a + r() * (b - a); }
      var kind = pickNonRepeating(3, lastKind, r);
      var dx = tx - ox;
      var spanX = Math.abs(dx) < 60 ? (dx >= 0 ? 110 : -110) : dx;
      var minY = 10, maxY = Math.max(40, (H || 260) - 10);
      var maxX = Math.max(160, (W || 520) - 14);
      var m0x = clamp((launchVx || 55) * 0.35 + (spanX >= 0 ? 28 : -28), -120, 120);
      var segs, dur, name;

      if (kind === 0) {
        name = "Frail Sputter Crawl";
        dur = rr(1.12, 1.24);
        var p1x = clamp(ox + spanX * 0.36, 24, maxX), p1y = clamp(lerp(oy, ty, 0.25) + 8, minY, maxY);
        var p2x = clamp(ox + spanX * 0.72, 24, maxX), p2y = clamp(lerp(oy, ty, 0.55) + 12, minY, maxY);
        segs = [
          { p0: [ox, oy], m0: [m0x, 12], p1: [p1x, p1y], m1: [spanX * 0.28, 8] },
          { p0: [p1x, p1y], m0: [spanX * 0.28, 8], p1: [p2x, p2y], m1: [spanX * 0.28, -8] },
          { p0: [p2x, p2y], m0: [spanX * 0.28, -8], p1: [tx, ty], m1: [spanX * 0.18, -32] }
        ];
      } else if (kind === 1) {
        name = "Leaky Sag Limp";
        dur = rr(1.16, 1.28);
        var sagX = clamp(ox + spanX * 0.50, 24, maxX), sagY = clamp(Math.max(oy, ty) + rr(14, 24), minY, maxY);
        segs = [
          { p0: [ox, oy], m0: [m0x, 22], p1: [sagX, sagY], m1: [spanX * 0.40, 2] },
          { p0: [sagX, sagY], m0: [spanX * 0.40, 2], p1: [tx, ty], m1: [spanX * 0.22, -40] }
        ];
      } else {
        name = "Trembling Wobble Limp";
        dur = rr(1.14, 1.26);
        var w1x = clamp(ox + spanX * 0.44, 24, maxX), w1y = clamp(lerp(oy, ty, 0.42) + 8, minY, maxY);
        var w2x = clamp(ox + spanX * 0.76, 24, maxX), w2y = clamp(lerp(oy, ty, 0.66) + 14, minY, maxY);
        segs = [
          { p0: [ox, oy], m0: [m0x + 12, 6], p1: [w1x, w1y], m1: [spanX * 0.30, 12] },
          { p0: [w1x, w1y], m0: [spanX * 0.30, 12], p1: [w2x, w2y], m1: [spanX * 0.24, -10] },
          { p0: [w2x, w2y], m0: [spanX * 0.24, -10], p1: [tx, ty], m1: [spanX * 0.20, -34] }
        ];
      }

      return { kind: kind, name: name, segs: segs, dur: dur, origTx: tx, origTy: ty, splatSeed: Math.floor(r() * 1000) };
    }

    function drawPaintBlob(ctx, x, y, r, angle, stretch, col, hi, alpha, wobbleSeed) {
      if (alpha <= 0.01 || r <= 0.4) return;
      ctx.save();
      ctx.translate(x, y);
      ctx.rotate(angle);
      var rx = r * stretch;
      var ry = r / Math.sqrt(Math.max(0.55, stretch));
      var grad = ctx.createRadialGradient(-rx * 0.24, -ry * 0.24, 0, 0, 0, Math.max(rx, ry) * 1.15);
      grad.addColorStop(0, rgba(hi, alpha * 0.96));
      grad.addColorStop(0.52, rgba(col, alpha * 0.94));
      grad.addColorStop(1, rgba(col, alpha * 0.42));
      ctx.fillStyle = grad;
      ctx.beginPath();
      var steps = 16;
      for (var i = 0; i <= steps; i++) {
        var th = (i / steps) * Math.PI * 2;
        var wob = 1 + 0.08 * Math.sin(th * 3 + wobbleSeed) + 0.05 * Math.cos(th * 5 - wobbleSeed * 1.4);
        var px = Math.cos(th) * rx * wob;
        var py = Math.sin(th) * ry * wob;
        if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
      }
      ctx.closePath();
      ctx.fill();
      ctx.beginPath();
      ctx.ellipse(-rx * 0.26, -ry * 0.22, rx * 0.28, ry * 0.20, -0.3, 0, Math.PI * 2);
      ctx.fillStyle = rgba(hi, alpha * 0.62);
      ctx.fill();
      ctx.restore();
    }

    function drawBrushTail(ctx, points, baseWidth, col, hi, alpha, wobbleAmp) {
      if (points.length < 2 || alpha <= 0.01) return;
      var n = points.length;
      var left = [], right = [];
      for (var i = 0; i < n; i++) {
        var frac = i / (n - 1);
        var p = points[i];
        var p0 = points[Math.max(0, i - 1)];
        var p1 = points[Math.min(n - 1, i + 1)];
        var dx = p1.x - p0.x, dy = p1.y - p0.y;
        var len = Math.hypot(dx, dy) || 1;
        var nx = -dy / len, ny = dx / len;
        var w = baseWidth * Math.pow(frac, 0.78) * (1 - 0.16 * Math.sin(frac * Math.PI));
        var wb = wobbleAmp ? Math.sin(i * 1.7 + p.x * 0.12) * wobbleAmp : 0;
        left.push({ x: p.x + nx * (w + wb), y: p.y + ny * (w + wb) });
        right.push({ x: p.x - nx * (w - wb), y: p.y - ny * (w - wb) });
      }
      var head = points[n - 1], tail = points[0];
      var grad = ctx.createLinearGradient(tail.x, tail.y, head.x, head.y);
      grad.addColorStop(0, rgba(col, 0.02 * alpha));
      grad.addColorStop(0.55, rgba(col, 0.54 * alpha));
      grad.addColorStop(1, rgba(hi, 0.88 * alpha));
      ctx.save();
      ctx.beginPath();
      ctx.moveTo(left[0].x, left[0].y);
      for (var j = 1; j < n; j++) ctx.lineTo(left[j].x, left[j].y);
      for (var k = n - 1; k >= 0; k--) ctx.lineTo(right[k].x, right[k].y);
      ctx.closePath();
      ctx.fillStyle = grad;
      ctx.fill();
      ctx.restore();
    }

    function drawSplat(ctx, x, y, radius, alpha, seed, col, hi) {
      if (alpha <= 0.01 || radius <= 0.5) return;
      ctx.save();
      ctx.translate(x, y);
      ctx.beginPath();
      var lobes = 9, steps = 42;
      for (var i = 0; i <= steps; i++) {
        var th = (i / steps) * Math.PI * 2;
        var wave = 1 +
          0.26 * Math.sin(th * lobes + seed) +
          0.16 * Math.cos(th * 4 - seed * 1.3) +
          0.10 * Math.sin(th * 13 + seed * 0.7);
        var rr = radius * Math.max(0.45, wave);
        var px = Math.cos(th) * rr, py = Math.sin(th) * rr;
        if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
      }
      ctx.closePath();
      var grad = ctx.createRadialGradient(-radius * 0.2, -radius * 0.2, 0, 0, 0, radius * 1.35);
      grad.addColorStop(0, rgba(hi, alpha * 0.90));
      grad.addColorStop(0.55, rgba(col, alpha * 0.86));
      grad.addColorStop(1, rgba(col, alpha * 0.36));
      ctx.fillStyle = grad;
      ctx.fill();
      ctx.restore();
    }

    function drawThinkingCloud(ctx, x, y, tq, scale, alpha, pal) {
      if (alpha <= 0.01 || scale <= 0.05) return;
      var col = pal.thinking.rgb;
      var hi = pal.thinking.hi;
      var breath = 1 + 0.14 * Math.sin(tq * 2.1);
      var rOuter = 13.5 * breath * scale;
      var rMid = 8.8 * (1 + 0.10 * Math.cos(tq * 2.6 + 0.7)) * scale;
      var rCore = 4.8 * (1 + 0.08 * Math.sin(tq * 3.1 + 1.3)) * scale;

      var halo = ctx.createRadialGradient(x, y, 0, x, y, rOuter * 1.45);
      halo.addColorStop(0, rgba(hi, 0.24 * alpha));
      halo.addColorStop(0.55, rgba(col, 0.18 * alpha));
      halo.addColorStop(1, rgba(col, 0));
      ctx.fillStyle = halo;
      ctx.beginPath();
      ctx.arc(x, y, rOuter * 1.45, 0, Math.PI * 2);
      ctx.fill();

      drawPaintBlob(ctx, x + Math.sin(tq * 1.5) * 1.6 * scale, y + Math.cos(tq * 1.3) * 1.0 * scale, rMid, tq * 0.35, 1.08, col, hi, 0.54 * alpha, tq * 1.6);
      drawPaintBlob(ctx, x - Math.cos(tq * 1.9) * 1.0 * scale, y + Math.sin(tq * 1.7) * 0.8 * scale, rCore, -tq * 0.42, 1.04, col, hi, 0.88 * alpha, tq * 2.4 + 2.1);
    }

    // Extensible motion registry: all #r-convlive live states share one canvas
    // host and register their step/draw handlers here (future permwait /
    // compacting states plug in without rewriting the loop).
    var MOTION_REGISTRY = {
      _handlers: Object.create(null),
      register: function (phase, handler) {
        if (!phase || !handler) return;
        this._handlers[phase] = handler;
      },
      get: function (phase) {
        return this._handlers[phase] || null;
      },
      list: function () {
        return Object.keys(this._handlers);
      }
    };

    MOTION_REGISTRY.register("thinking", {
      fps: STEP_FPS,
      category: function () { return "thinking"; },
      step: function (st, dt, pal) {
        var tq = Math.floor(st.runnerT * STEP_FPS) / STEP_FPS;
        var stepIdx = Math.floor(st.runnerT * STEP_FPS);
        if (stepIdx !== st.lastStepFrame) {
          st.lastStepFrame = stepIdx;
          if (stepIdx % 4 === 0 && st.motes.length < 10) {
            var ang = st.rng() * Math.PI * 2;
            st.motes.push({
              x: st.anchorX + Math.cos(ang) * (4 + st.rng() * 6),
              y: st.anchorY + Math.sin(ang) * (2.5 + st.rng() * 3.5),
              vx: Math.cos(ang) * (4 + st.rng() * 6),
              vy: -6 - st.rng() * 8,
              r: 1.2 + st.rng() * 1.1,
              age: 0,
              life: 0.72 + st.rng() * 0.38,
              col: st.rng() > 0.45 ? pal.thinking.hi : pal.thinking.rgb
            });
          }
        }
        return tq;
      },
      draw: function (ctx, st, pal) {
        var tq = Math.floor(st.runnerT * STEP_FPS) / STEP_FPS;
        drawThinkingCloud(ctx, st.anchorX, st.anchorY, tq, 1, 1, pal);
      }
    });

    MOTION_REGISTRY.register("tool", {
      fps: STEP_FPS,
      category: function (toolName) { return classifyTool(toolName); },
      step: function (st, dt, pal) {
        var cat = pal[st.catId] || pal.file;
        var amp = cat.swayAmp || 13.5;
        var omega = (2 * Math.PI) / SWAY_PERIOD;
        var stepIdx = Math.floor(st.runnerT * STEP_FPS);
        if (stepIdx !== st.lastStepFrame) {
          st.lastStepFrame = stepIdx;
          var tq = stepIdx / STEP_FPS;
          var rx = st.anchorX + amp * Math.sin(omega * tq);
          var ry = st.anchorY + 2.0 * (1 - Math.cos(2 * omega * tq));
          st.swayTrail.push({ x: rx, y: ry });
          if (st.swayTrail.length > 10) st.swayTrail.shift();

          if (cat.sway === "weave") {
            var wx = st.anchorX + amp * 0.85 * Math.sin(omega * tq - 0.55);
            var wy = st.anchorY - 3.0 * Math.sin(2 * omega * tq);
            st.swayWeave.push({ x: wx, y: wy });
            if (st.swayWeave.length > 9) st.swayWeave.shift();
          } else {
            st.swayWeave.length = 0;
          }

          if (cat.sway === "spark" && stepIdx % 3 === 0 && st.drops.length < 24) {
            var spAng = (st.rng() - 0.5) * 2.4 - Math.PI * 0.5;
            st.drops.push({
              x: rx, y: ry,
              vx: Math.cos(spAng) * (18 + st.rng() * 24),
              vy: Math.sin(spAng) * (14 + st.rng() * 22),
              r: 1.2 + st.rng() * 0.8,
              col: st.rng() > 0.4 ? cat.hi : cat.rgb,
              grav: 42, age: 0, life: 0.24 + st.rng() * 0.14
            });
          }
        }
      },
      draw: function (ctx, st, pal) {
        var cat = pal[st.catId] || pal.file;
        var amp = cat.swayAmp || 13.5;
        var omega = (2 * Math.PI) / SWAY_PERIOD;
        var tq = Math.floor(st.runnerT * STEP_FPS) / STEP_FPS;
        var rx = st.anchorX + amp * Math.sin(omega * tq);
        var ry = st.anchorY + 2.0 * (1 - Math.cos(2 * omega * tq));
        var vx = amp * omega * Math.cos(omega * tq);
        var vy = 4.0 * omega * Math.sin(2 * omega * tq);
        var ang = Math.atan2(vy, vx);
        var spd = Math.hypot(vx, vy);

        var popScale = 1.0;
        if (st.popIn) {
          var p = st.popIn;
          if (p.cloudCondense && p.t < 0.14) {
            var cu = p.t / 0.14;
            drawThinkingCloud(ctx, st.anchorX, st.anchorY, tq, 1 - 0.72 * cu, 1 - cu, pal);
          }
          var pt = p.cloudCondense ? Math.max(0, p.t - 0.08) : p.t;
          var pu = clamp(pt / p.dur, 0, 1);
          // Spring pop-in: 0 -> ~1.27 -> 0.94 -> 1.0
          popScale = pu >= 1 ? 1.0 : (1 - Math.exp(-9.5 * pu) * Math.cos(9.8 * pu) + 0.34 * Math.sin(Math.PI * pu) * Math.exp(-2.8 * pu));
          if (pu > 0 && pu < 1) {
            ctx.save();
            ctx.beginPath();
            ctx.arc(rx, ry, 5 + 14 * pu, 0, Math.PI * 2);
            ctx.strokeStyle = rgba(cat.rgb, (1 - pu) * 0.55);
            ctx.lineWidth = Math.max(0.7, 2.0 * (1 - pu));
            ctx.stroke();
            ctx.restore();
          }
        }

        if (cat.sway === "weave" && st.swayWeave.length >= 2) {
          drawBrushTail(ctx, st.swayWeave, 2.4 * popScale, cat.rgb, cat.hi, 0.55, 0.25);
        }
        if (st.swayTrail.length >= 2) {
          drawBrushTail(ctx, st.swayTrail, 3.6 * popScale, cat.rgb, cat.hi, 0.78, 0.2);
        }
        drawPaintBlob(ctx, rx, ry, 5.4 * popScale, ang, 1 + clamp(spd / 220, 0, 0.28), cat.rgb, cat.hi, 0.95, tq * 5.0);

        if (cat.sway === "orbit") {
          var oa = tq * 5.2;
          var ox2 = rx + Math.cos(oa) * 10.5 * popScale;
          var oy2 = ry + Math.sin(oa) * 5.2 * popScale;
          drawPaintBlob(ctx, ox2, oy2, 2.5 * popScale, oa, 1.12, cat.hi, cat.hi, 0.85, tq * 3.0);
        }
      }
    });

    function createEngine(opts) {
      opts = opts || {};
      var d = opts.doc || (typeof doc !== "undefined" ? doc : (typeof document !== "undefined" ? document : null));
      var win = opts.win || (typeof window !== "undefined" ? window : null);
      var rt = opts.root || (d && d.documentElement) || null;
      var convoEl = opts.convo || (d && d.getElementById("convo")) || null;
      var gridEl = opts.grid || (d && d.getElementById("grid")) || null;
      var staleMs = typeof opts.staleMs === "number" && opts.staleMs > 0 ? opts.staleMs : DEFAULT_STALE_MS;
      var nowFn = typeof opts.now === "function" ? opts.now : function () { return Date.now(); };
      var perfNow = typeof opts.perfNow === "function" ? opts.perfNow : function () {
        return win && win.performance && typeof win.performance.now === "function" ? win.performance.now() : nowFn();
      };
      var rafFn = typeof opts.requestAnimationFrame === "function" ? opts.requestAnimationFrame : function (cb) { return win && win.requestAnimationFrame ? win.requestAnimationFrame(cb) : 0; };
      var cafFn = typeof opts.cancelAnimationFrame === "function" ? opts.cancelAnimationFrame : function (id) { if (win && win.cancelAnimationFrame) win.cancelAnimationFrame(id); };
      var rng = typeof opts.rng === "function" ? opts.rng : Math.random;
      var onMute = typeof opts.onMute === "function" ? opts.onMute : function () {
        if (typeof setExtraMotion === "function") setExtraMotion("off", true);
      };
      var tFn = typeof opts.t === "function" ? opts.t : function (k, fb) {
        if (typeof wsT === "function") {
          var v = wsT(k);
          if (v && v !== k) return v;
        }
        return fb;
      };

      var overlay = null;
      var canvas = opts.canvas || null;
      var ctx = null;
      if (!canvas && d && convoEl) {
        var host = (gridEl && gridEl.classList && gridEl.classList.contains("live")) ? gridEl : convoEl.parentNode;
        if (host) {
          overlay = d.createElement("div");
          overlay.className = "d-conv-motion-overlay";
          overlay.setAttribute("aria-hidden", "true");
          overlay.hidden = true;
          canvas = d.createElement("canvas");
          canvas.className = "d-conv-motion-canvas";
          canvas.setAttribute("aria-hidden", "true");
          canvas.hidden = true;
          overlay.appendChild(canvas);
          host.appendChild(overlay);
        }
      }
      if (canvas && typeof canvas.getContext === "function") {
        ctx = canvas.getContext("2d");
      }

      var mql = opts.reducedMotionMedia || (win && win.matchMedia ? win.matchMedia("(prefers-reduced-motion: reduce)") : null);
      var reducedMotion = !!(mql && mql.matches);
      var enabled = opts.enabled !== undefined ? !!opts.enabled : (typeof extraMotion !== "undefined" ? !!extraMotion : true);
      var hidden = opts.hidden !== undefined ? !!opts.hidden : (!!(d && d.hidden) || (typeof shHidden !== "undefined" && !!shHidden));
      var stale = false;
      var staleTimer = 0;
      var pulseTimer = 0;

      var rafId = 0;
      var lastTs = 0;
      var lastDrawKey = "";
      var scrollBound = false;
      var convoRO = null;
      var geomDirty = true;

      // Observed live turn tracking
      var seeded = false;
      var lastTurn = "";
      var lastDone = 0;
      var lastFailed = 0;
      var lastExactTokens = 0;
      var lastTool = "";
      var lastPhase = "";
      var lastActivityAt = 0;
      var lastLiveStamp = 0;
      var activeTokEl = null;

      var state = {
        W: 0, H: 0, dpr: 1,
        anchorX: 28, anchorY: 100, anchorVisible: false,
        targetX: 280, targetY: 22, targetW: 52, targetVisible: false,
        runnerMode: "idle", // "idle" | "thinking" | "sway"
        catId: "file",
        runnerT: 0,
        lastStepFrame: -1,
        swayTrail: [],
        swayWeave: [],
        popIn: null,
        flights: [],
        impacts: [],
        popups: [],
        drops: [],
        motes: [],
        lastOkKind: -1,
        lastFailKind: -1,
        rng: rng
      };

      function getPalette() {
        var theme = rt && rt.getAttribute ? rt.getAttribute("data-theme") : "";
        var isLight = theme === "light" || (!theme && win && win.matchMedia && !win.matchMedia("(prefers-color-scheme: dark)").matches);
        return isLight ? PALETTES.light : PALETTES.dark;
      }

      function isConvoShown() {
        if (opts.convoVisible !== undefined) return !!opts.convoVisible;
        if (!convoEl) return false;
        if (gridEl && gridEl.getAttribute) {
          var tab = gridEl.getAttribute("data-tab");
          var ctab = gridEl.getAttribute("data-ctab");
          if (ctab === "files" || ctab === "term") return false;
          if (tab === "board") {
            // In narrow/tabbed view, convo is hidden when data-tab="board".
            if (typeof convoVisible === "function") return !!convoVisible();
            if (convoEl.getBoundingClientRect) {
              var r = convoEl.getBoundingClientRect();
              return r.width > 0 && r.height > 0;
            }
          }
        }
        return true;
      }

      function canAnimate() {
        return enabled && !reducedMotion && !hidden && !stale && isConvoShown();
      }

      function hasActiveWork() {
        return state.runnerMode !== "idle" ||
          state.flights.length > 0 ||
          state.impacts.length > 0 ||
          state.popups.length > 0 ||
          state.drops.length > 0 ||
          state.motes.length > 0 ||
          state.popIn !== null;
      }

      function isHighFpsNeeded() {
        return state.flights.length > 0 ||
          state.impacts.length > 0 ||
          state.popups.length > 0 ||
          state.drops.length > 0 ||
          state.popIn !== null;
      }

      function clearStaleTimer() {
        if (staleTimer) { clearTimeout(staleTimer); staleTimer = 0; }
      }

      function armStaleTimer() {
        clearStaleTimer();
        if (state.runnerMode === "idle") return;
        staleTimer = setTimeout(function () {
          staleTimer = 0;
          if (state.runnerMode === "idle") return;
          stale = true;
          stopAllMotion();
          syncActivityDOM();
        }, staleMs);
      }

      function markGeomDirty() { geomDirty = true; }

      function bindActiveListeners(on) {
        if (on === scrollBound) return;
        scrollBound = on;
        if (on) {
          if (convoEl && convoEl.addEventListener) convoEl.addEventListener("scroll", markGeomDirty, { passive: true });
          if (win && win.addEventListener) win.addEventListener("resize", markGeomDirty, { passive: true });
          if (convoEl && !convoRO) {
            var RO = (win && win.ResizeObserver) || (typeof ResizeObserver === "function" ? ResizeObserver : null);
            if (typeof RO === "function") {
              convoRO = new RO(markGeomDirty);
              convoRO.observe(convoEl);
            }
          }
        } else {
          if (convoEl && convoEl.removeEventListener) convoEl.removeEventListener("scroll", markGeomDirty);
          if (win && win.removeEventListener) win.removeEventListener("resize", markGeomDirty);
          if (convoRO) {
            convoRO.disconnect();
            convoRO = null;
          }
        }
      }

      function stopLoop() {
        if (rafId) {
          cafFn(rafId);
          rafId = 0;
        }
        lastTs = 0;
        lastDrawKey = "";
        bindActiveListeners(false);
        if (overlay) overlay.hidden = true;
        if (canvas) {
          if (ctx && state.W > 0 && state.H > 0) ctx.clearRect(0, 0, state.W, state.H);
          canvas.hidden = true;
        }
      }

      function stopAllMotion() {
        state.flights.length = 0;
        state.impacts.length = 0;
        state.popups.length = 0;
        state.drops.length = 0;
        state.motes.length = 0;
        state.swayTrail.length = 0;
        state.swayWeave.length = 0;
        state.popIn = null;
        stopLoop();
      }

      function ensureLoop() {
        if (!canAnimate() || !hasActiveWork()) {
          stopLoop();
          return;
        }
        if (overlay) overlay.hidden = false;
        if (canvas) canvas.hidden = false;
        geomDirty = true;
        bindActiveListeners(true);
        if (!rafId) {
          lastTs = 0;
          rafId = rafFn(tick);
        }
      }

      function findLiveRegion() {
        return opts.convlive || (d && d.getElementById("r-convlive")) || null;
      }

      function findTargetTokenEl() {
        var liveReg = findLiveRegion();
        if (liveReg && liveReg.querySelector) {
          var liveTok = liveReg.querySelector(".d-turn-live [data-conv-turn-tokens]") || liveReg.querySelector("[data-conv-turn-tokens]");
          if (liveTok) return { el: liveTok, fallbackEdge: false };
          var liveSum = liveReg.querySelector("[data-conv-turn-summary]");
          if (liveSum) return { el: liveSum, fallbackEdge: true };
        }
        // When a turn just completed and moved from #r-convlive to #r-entries, converge on the newest turn summary.
        if (convoEl && convoEl.querySelectorAll) {
          var allToks = convoEl.querySelectorAll("[data-conv-turn-tokens]");
          if (allToks.length) return { el: allToks[allToks.length - 1], fallbackEdge: false };
          var allSums = convoEl.querySelectorAll("[data-conv-turn-summary]");
          if (allSums.length) return { el: allSums[allSums.length - 1], fallbackEdge: true };
        }
        return null;
      }

      // Single batched DOM measurement pass (reads only, zero interleaved writes).
      function measureGeometry() {
        geomDirty = false;
        if (!canvas || !canvas.getBoundingClientRect) return;
        var hostRect = (convoEl && convoEl.getBoundingClientRect) ? convoEl.getBoundingClientRect() : canvas.getBoundingClientRect();
        var cRect = canvas.getBoundingClientRect();
        var baseRect = (cRect.width > 0 && cRect.height > 0) ? cRect : hostRect;
        var w = Math.max(0, Math.round(baseRect.width));
        var h = Math.max(0, Math.round(baseRect.height));
        var dpr = Math.min(2, (win && win.devicePixelRatio) || 1);

        var liveReg = findLiveRegion();
        var actEl = liveReg && liveReg.querySelector ? liveReg.querySelector(".clive .activity") : null;
        var spinEl = actEl && actEl.querySelector ? actEl.querySelector(".spin") : null;
        var anchorRect = spinEl && spinEl.getBoundingClientRect ? spinEl.getBoundingClientRect() : (actEl && actEl.getBoundingClientRect ? actEl.getBoundingClientRect() : null);
        var tgt = findTargetTokenEl();
        var tgtRect = tgt && tgt.el && tgt.el.getBoundingClientRect ? tgt.el.getBoundingClientRect() : null;

        // Apply canvas backing resize only when dimensions changed.
        if (w > 0 && h > 0 && (state.W !== w || state.H !== h || state.dpr !== dpr)) {
          state.W = w;
          state.H = h;
          state.dpr = dpr;
          canvas.width = Math.round(w * dpr);
          canvas.height = Math.round(h * dpr);
          if (ctx && ctx.setTransform) ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        }

        if (anchorRect && anchorRect.width > 0) {
          state.anchorX = anchorRect.left + anchorRect.width * 0.5 - baseRect.left;
          state.anchorY = anchorRect.top + anchorRect.height * 0.5 - baseRect.top;
          state.anchorVisible = state.anchorY > -40 && state.anchorY < state.H + 40;
        } else {
          state.anchorVisible = false;
        }

        if (tgtRect && tgtRect.width > 0) {
          activeTokEl = tgt.el;
          state.targetX = tgt.fallbackEdge
            ? (tgtRect.right - 28 - baseRect.left)
            : (tgtRect.left + tgtRect.width * 0.5 - baseRect.left);
          state.targetY = tgtRect.top + tgtRect.height * 0.5 - baseRect.top;
          state.targetW = tgt.fallbackEdge ? 44 : tgtRect.width;
          state.targetVisible = state.targetY > -40 && state.targetY < state.H + 40;
        } else {
          activeTokEl = null;
          state.targetX = Math.max(120, state.W - 56);
          state.targetY = Math.max(18, state.anchorY - 48);
          state.targetW = 48;
          state.targetVisible = false;
        }
      }

      function pulseTargetEl(mode) {
        if (!activeTokEl || !activeTokEl.classList) return;
        var cls = mode === "fail" ? "d-motion-tok-shake" : "d-motion-tok-flash";
        activeTokEl.classList.remove("d-motion-tok-flash", "d-motion-tok-shake");
        if (typeof activeTokEl.offsetWidth === "number") { void activeTokEl.offsetWidth; }
        activeTokEl.classList.add(cls);
        var el = activeTokEl;
        if (pulseTimer) { clearTimeout(pulseTimer); pulseTimer = 0; }
        pulseTimer = setTimeout(function () {
          pulseTimer = 0;
          if (el && el.classList) el.classList.remove(cls);
        }, 440);
      }

      function spawnPopIn(cat, wasThinking) {
        state.popIn = { t: 0, dur: 0.24, cloudCondense: !!wasThinking };
        for (var i = 0; i < 3; i++) {
          var a = -Math.PI * 0.85 + i * (Math.PI * 0.42) + (rng() - 0.5) * 0.25;
          var spd = 28 + rng() * 24;
          state.drops.push({
            x: state.anchorX, y: state.anchorY,
            vx: Math.cos(a) * spd, vy: Math.sin(a) * spd,
            r: 1.4 + rng() * 0.8,
            col: i === 1 ? cat.hi : cat.rgb,
            grav: 28, age: 0, life: 0.26 + rng() * 0.12
          });
        }
      }

      function launchFlight(catId, mode, tokensDelta) {
        if (!canAnimate()) return;
        measureGeometry();
        var pal = getPalette();
        var cat = pal[catId] || pal.file;
        var amp = cat.swayAmp || 13.5;
        var omega = (2 * Math.PI) / SWAY_PERIOD;
        var tq = Math.floor(state.runnerT * STEP_FPS) / STEP_FPS;
        var ox = state.anchorX + (state.runnerMode === "sway" ? amp * Math.sin(omega * tq) : 0);
        var oy = state.anchorY + (state.runnerMode === "sway" ? 2.0 * (1 - Math.cos(2 * omega * tq)) : 0);
        var rawVx = state.runnerMode === "sway" ? amp * omega * Math.cos(omega * tq) : 60;
        var launchVx = Math.max(55, rawVx * 0.85 + 75);
        var launchVy = state.runnerMode === "sway" ? 4.0 * omega * Math.sin(2 * omega * tq) : -12;
        var tx = state.targetX;
        var ty = state.targetY;
        var prof;
        if (mode === "fail") {
          prof = buildFailFlightProfile(state.lastFailKind, ox, oy, tx, ty, launchVx, state.W || 520, state.H || 260, rng);
          state.lastFailKind = prof.kind;
        } else {
          prof = buildOkFlightProfile(state.lastOkKind, ox, oy, tx, ty, launchVx, launchVy, state.W || 520, state.H || 260, rng);
          state.lastOkKind = prof.kind;
        }
        state.flights.push({
          catId: cat.id,
          mode: mode,
          prof: prof,
          t: 0,
          dur: prof.dur,
          trail: state.swayTrail.slice(-5),
          weave: state.swayWeave.slice(-4),
          dropFlags: [false, false, false, false, false],
          tokensDelta: tokensDelta > 0 ? Math.round(tokensDelta) : 0
        });
        state.swayTrail.length = 0;
        state.swayWeave.length = 0;
      }

      function handleFlightImpact(fl) {
        var pal = getPalette();
        var tx = state.targetX;
        var ty = state.targetY;
        var delta = fl.tokensDelta;
        pulseTargetEl(fl.mode);

        if (fl.mode === "ok") {
          state.impacts.push({ kind: "okring", x: tx, y: ty, t: 0, dur: 0.56 });
          var angs = [-2.4, -0.8, 0.7, 2.3];
          for (var k = 0; k < angs.length; k++) {
            state.drops.push({
              x: tx, y: ty,
              vx: Math.cos(angs[k]) * (36 + k * 6),
              vy: Math.sin(angs[k]) * (36 + k * 6),
              r: 1.9,
              col: k % 2 === 0 ? pal.ok.rgb : pal.ok.hi,
              grav: 18, age: 0, life: 0.34
            });
          }
          if (delta > 0) {
            state.popups.push({
              text: "+" + delta,
              x: tx + state.targetW * 0.28,
              y: ty - 4,
              col: pal.ok.rgb,
              hex: pal.ok.hex,
              jitter: false,
              t: 0,
              dur: 0.82
            });
          }
        } else {
          var sDrops = [];
          for (var si = 0; si < 7; si++) {
            var bAng = -Math.PI + (si / 7) * Math.PI * 2 + (rng() - 0.5) * 0.56;
            var spd = 34 + rng() * 38;
            sDrops.push({
              x: tx, y: ty,
              vx: Math.cos(bAng) * spd, vy: Math.sin(bAng) * spd,
              r: 1.3 + rng() * 1.0, drag: 6.2 + rng() * 1.6, creep: 3.0 + rng() * 3.5
            });
          }
          state.impacts.push({
            kind: "splat", x: tx, y: ty, t: 0, dur: 0.96,
            seed: fl.prof.splatSeed, drops: sDrops
          });
          if (delta > 0) {
            state.popups.push({
              text: "+" + delta,
              x: tx + state.targetW * 0.28,
              y: ty - 3,
              col: pal.fail.rgb,
              hex: pal.fail.hex,
              jitter: true,
              t: 0,
              dur: 0.88
            });
          }
        }
      }

      function stepSim(dt) {
        var pal = getPalette();
        if (state.runnerMode === "thinking" || state.runnerMode === "sway") {
          state.runnerT += dt;
          var regKey = state.runnerMode === "thinking" ? "thinking" : "tool";
          var handler = MOTION_REGISTRY.get(regKey);
          if (handler && handler.step) handler.step(state, dt, pal);
        }

        if (state.popIn) {
          state.popIn.t += dt;
          if (state.popIn.t >= state.popIn.dur + (state.popIn.cloudCondense ? 0.08 : 0)) {
            state.popIn = null;
          }
        }

        for (var i = state.flights.length - 1; i >= 0; i--) {
          var fl = state.flights[i];
          fl.t += dt;
          var u = clamp(fl.t / fl.dur, 0, 1);
          var s = fl.mode === "fail" ? warpFail(u) : warpTension(u);
          var pos = evalHermite2D(fl.prof.segs, s);
          // Smoothly track any live target X/Y shift (e.g., [data-conv-turn-fails] appearing or scroll).
          var shiftW = s * s;
          var px = pos.x + (state.targetX - fl.prof.origTx) * shiftW;
          var py = pos.y + (state.targetY - fl.prof.origTy) * shiftW;
          if (fl.mode === "fail") {
            var jAmp = 2.6 + 2.4 * Math.sin(u * Math.PI);
            px += (Math.sin(fl.t * 48) * 0.65 + Math.cos(fl.t * 73) * 0.35) * jAmp;
            py += (Math.cos(fl.t * 54) * 0.60 + Math.sin(fl.t * 81) * 0.40) * jAmp;
          }
          fl.curX = px;
          fl.curY = py;
          fl.trail.push({ x: px, y: py });
          var maxTrail = fl.mode === "fail" ? 13 : 20;
          if (fl.trail.length > maxTrail) fl.trail.shift();

          // Flung droplets (OK) / heavy leaky gravity drips (FAIL)
          var dropAt = fl.mode === "fail" ? [0.16, 0.32, 0.48, 0.66, 0.82] : [0.18, 0.46, 0.72];
          for (var di = 0; di < dropAt.length; di++) {
            if (!fl.dropFlags[di] && u >= dropAt[di]) {
              fl.dropFlags[di] = true;
              var cat = pal[fl.catId] || pal.file;
              var col = fl.mode === "fail" ? pal.fail.rgb : mixRgb(cat.rgb, pal.ok.rgb, u);
              state.drops.push({
                x: px, y: py,
                vx: fl.mode === "fail" ? (rng() - 0.5) * 18 : (rng() - 0.5) * 32,
                vy: fl.mode === "fail" ? (12 + rng() * 24) : (-16 + rng() * 24),
                r: fl.mode === "fail" ? (1.8 + rng() * 1.1) : (1.3 + rng() * 0.8),
                col: col,
                grav: fl.mode === "fail" ? 310 : 65,
                age: 0,
                life: fl.mode === "fail" ? 0.58 : 0.34
              });
            }
          }

          if (fl.t >= fl.dur) {
            state.flights.splice(i, 1);
            handleFlightImpact(fl);
          }
        }

        for (var ii = state.impacts.length - 1; ii >= 0; ii--) {
          var imp = state.impacts[ii];
          imp.t += dt;
          if (imp.kind === "splat" && imp.drops) {
            for (var sd = 0; sd < imp.drops.length; sd++) {
              var dr = imp.drops[sd];
              dr.x += dr.vx * dt;
              dr.y += dr.vy * dt + dr.creep * dt;
              var damp = Math.exp(-dr.drag * dt);
              dr.vx *= damp;
              dr.vy *= damp;
            }
          }
          if (imp.t >= imp.dur) state.impacts.splice(ii, 1);
        }

        for (var pi = state.popups.length - 1; pi >= 0; pi--) {
          state.popups[pi].t += dt;
          if (state.popups[pi].t >= state.popups[pi].dur) state.popups.splice(pi, 1);
        }

        for (var dri = state.drops.length - 1; dri >= 0; dri--) {
          var dObj = state.drops[dri];
          dObj.age += dt;
          dObj.x += dObj.vx * dt;
          dObj.y += dObj.vy * dt;
          dObj.vy += dObj.grav * dt;
          if (dObj.age >= dObj.life) state.drops.splice(dri, 1);
        }

        for (var mi = state.motes.length - 1; mi >= 0; mi--) {
          var mt = state.motes[mi];
          mt.age += dt;
          mt.x += mt.vx * dt;
          mt.y += mt.vy * dt;
          if (mt.age >= mt.life) state.motes.splice(mi, 1);
        }
      }

      function render() {
        if (!ctx || state.W <= 0 || state.H <= 0) return;
        var pal = getPalette();
        ctx.clearRect(0, 0, state.W, state.H);

        // 1. Live thinking / tool runner
        if (state.runnerMode !== "idle" && state.anchorVisible) {
          var regKey = state.runnerMode === "thinking" ? "thinking" : "tool";
          var handler = MOTION_REGISTRY.get(regKey);
          if (handler && handler.draw) handler.draw(ctx, state, pal);
        }

        // 2. Drifting thought motes
        for (var mi = 0; mi < state.motes.length; mi++) {
          var mt = state.motes[mi];
          var ma = Math.sin((mt.age / mt.life) * Math.PI) * 0.72;
          if (ma > 0.01) {
            ctx.beginPath();
            ctx.arc(mt.x, mt.y, mt.r, 0, Math.PI * 2);
            ctx.fillStyle = rgba(mt.col, ma);
            ctx.fill();
          }
        }

        // 3. Active OK/FAIL flights
        for (var fi = 0; fi < state.flights.length; fi++) {
          var fl = state.flights[fi];
          var u = clamp(fl.t / fl.dur, 0, 1);
          var cat = pal[fl.catId] || pal.file;
          var col, hi, r, tailW, wobble;
          if (fl.mode === "fail") {
            var blendF = clamp(u * 1.4, 0, 1);
            col = mixRgb(cat.rgb, pal.fail.rgb, blendF);
            hi = mixRgb(cat.hi, pal.fail.hi, blendF);
            r = lerp(4.6, 3.0, u);
            tailW = lerp(2.8, 1.4, u);
            wobble = 0.75;
          } else {
            var blendO = clamp((u - 0.22) / 0.62, 0, 1);
            col = mixRgb(cat.rgb, pal.ok.rgb, blendO);
            hi = mixRgb(cat.hi, pal.ok.hi, blendO);
            r = lerp(5.4, 3.9, u);
            tailW = lerp(4.4, 2.6, u);
            wobble = 0.16;
          }
          if (fl.trail.length >= 2) {
            drawBrushTail(ctx, fl.trail, tailW, col, hi, fl.mode === "fail" ? 0.72 : 0.88, wobble);
          }
          var p0 = fl.trail.length >= 2 ? fl.trail[fl.trail.length - 2] : { x: fl.curX - 1, y: fl.curY };
          var ang = Math.atan2(fl.curY - p0.y, fl.curX - p0.x);
          var stretch = fl.mode === "fail" ? 1.06 : 1.24;
          drawPaintBlob(ctx, fl.curX, fl.curY, r, ang, stretch, col, hi, 0.96, fl.t * 14);
        }

        // 4. Impacts (OK absorption ring / FAIL compact splat)
        for (var ii = 0; ii < state.impacts.length; ii++) {
          var imp = state.impacts[ii];
          var iu = clamp(imp.t / imp.dur, 0, 1);
          if (imp.kind === "okring") {
            var rOut = 4 + 17 * (1 - Math.pow(1 - iu, 2.4));
            var aOut = (1 - iu) * 0.82;
            ctx.save();
            ctx.beginPath();
            ctx.arc(imp.x, imp.y, rOut, 0, Math.PI * 2);
            ctx.strokeStyle = rgba(pal.ok.rgb, aOut);
            ctx.lineWidth = Math.max(0.8, 2.4 * (1 - iu));
            ctx.stroke();
            ctx.beginPath();
            ctx.arc(imp.x, imp.y, 4.5 * (1 - iu), 0, Math.PI * 2);
            ctx.fillStyle = rgba(pal.ok.hi, aOut * 0.72);
            ctx.fill();
            ctx.restore();
          } else if (imp.kind === "splat") {
            var grow = clamp(iu / 0.16, 0, 1);
            var fade = iu > 0.45 ? 1 - (iu - 0.45) / 0.55 : 1;
            drawSplat(ctx, imp.x, imp.y, 9.2 * (0.4 + 0.6 * grow), fade * 0.88, imp.seed, pal.fail.rgb, pal.fail.hi);
            for (var sd = 0; sd < imp.drops.length; sd++) {
              var dr = imp.drops[sd];
              ctx.beginPath();
              ctx.arc(dr.x, dr.y, dr.r, 0, Math.PI * 2);
              ctx.fillStyle = rgba(pal.fail.rgb, fade * 0.84);
              ctx.fill();
            }
          }
        }

        // 5. Flung droplets & micro-sparks
        for (var dri = 0; dri < state.drops.length; dri++) {
          var dObj = state.drops[dri];
          var da = clamp(1 - dObj.age / dObj.life, 0, 1);
          ctx.beginPath();
          ctx.arc(dObj.x, dObj.y, dObj.r * (0.45 + 0.55 * da), 0, Math.PI * 2);
          ctx.fillStyle = rgba(dObj.col, da * 0.88);
          ctx.fill();
        }

        // 6. RPG floating "+m" damage-number popups
        for (var pi = 0; pi < state.popups.length; pi++) {
          var pop = state.popups[pi];
          var pu = clamp(pop.t / pop.dur, 0, 1);
          var riseY = pop.y - (8 + 20 * (1 - Math.pow(1 - pu, 2.2)));
          var popAlpha = pu < 0.14 ? (pu / 0.14) : (pu > 0.62 ? clamp(1 - (pu - 0.62) / 0.38, 0, 1) : 1);
          var scale = pu < 0.16 ? lerp(0.65, 1.16, pu / 0.16) : (pu < 0.30 ? lerp(1.16, 1.0, (pu - 0.16) / 0.14) : 1.0);
          var jx = pop.jitter ? Math.sin(pop.t * 58) * 1.6 * (1 - pu) : 0;
          var px = clamp(pop.x + jx, 22, Math.max(24, state.W - 24));
          ctx.save();
          ctx.translate(px, riseY);
          ctx.scale(scale, scale);
          ctx.font = "700 11.5px " + MONO_FONT;
          ctx.textAlign = "center";
          ctx.textBaseline = "middle";
          ctx.globalAlpha = popAlpha;
          var tw = ctx.measureText ? ctx.measureText(pop.text).width : 28;
          ctx.fillStyle = pal.popupBg;
          if (ctx.beginPath) {
            var bw = tw + 10, bh = 16, bx = -bw * 0.5, by = -bh * 0.5;
            ctx.beginPath();
            ctx.rect(bx, by, bw, bh);
            ctx.fill();
          }
          ctx.fillStyle = pop.hex;
          ctx.fillText(pop.text, 0, 0.5);
          ctx.restore();
        }
      }

      function tick(ts) {
        rafId = 0;
        if (!canAnimate() || !hasActiveWork()) {
          stopLoop();
          return;
        }
        var nowT = typeof ts === "number" && ts > 0 ? ts : perfNow();
        var dt = lastTs > 0 ? clamp((nowT - lastTs) / 1000, 0.001, 0.08) : (1 / 60);
        lastTs = nowT;
        if (geomDirty) measureGeometry();
        stepSim(dt);

        // Throttle canvas raster to 15fps when only thinking/sway is active.
        var drawKey = isHighFpsNeeded()
          ? String(nowT)
          : (state.runnerMode + ":" + state.catId + ":" + state.lastStepFrame + ":" + state.motes.length + ":" + state.W + "x" + state.H + ":" + Math.round(state.anchorX) + "," + Math.round(state.anchorY));
        if (drawKey !== lastDrawKey) {
          lastDrawKey = drawKey;
          render();
        }

        if (canAnimate() && hasActiveWork()) {
          rafId = rafFn(tick);
        } else {
          stopLoop();
        }
      }

      function syncActivityDOM() {
        var liveReg = findLiveRegion();
        if (!liveReg || !liveReg.querySelector) return;
        var act = liveReg.querySelector(".clive .activity");
        if (!act) return;
        var cat = state.runnerMode === "thinking" ? "thinking" : (state.catId || "file");
        act.setAttribute("data-motion-cat", cat);
        var active = canAnimate() && state.runnerMode !== "idle";
        var muteBtn = act.querySelector("[data-motion-mute]");
        if (active) {
          act.setAttribute("data-motion-active", "1");
          act.removeAttribute("data-motion-fallback");
          if (!muteBtn && d && d.createElement) {
            muteBtn = d.createElement("button");
            muteBtn.type = "button";
            muteBtn.className = "d-motion-mute";
            muteBtn.setAttribute("data-motion-mute", "1");
            muteBtn.textContent = tFn("js.motion.off", "Turn off motion");
            muteBtn.title = tFn("js.motion.off.title", "Turn off Extra Motion (re-enable in Settings)");
            muteBtn.setAttribute("aria-label", muteBtn.title);
            act.appendChild(muteBtn);
          }
        } else {
          act.removeAttribute("data-motion-active");
          act.setAttribute("data-motion-fallback", "1");
          if (muteBtn && muteBtn.parentNode) muteBtn.parentNode.removeChild(muteBtn);
        }
      }

      function readExactTurnTokens(clive) {
        if (!clive) return 0;
        var tokEl = clive.querySelector ? clive.querySelector("[data-conv-turn-tokens]") : null;
        if (tokEl) {
          if (tokEl.getAttribute("data-estimated") === "1") return 0;
          var dtAttr = tokEl.getAttribute("data-tokens");
          if (dtAttr !== null && dtAttr !== "") return Math.max(0, parseInt(dtAttr, 10) || 0);
        }
        // Fallback to additive data-turn-tokens on .clive (exact Lead.TurnAddedTokens, 0 when estimated/absent).
        if (!tokEl || tokEl.getAttribute("data-estimated") !== "1") {
          var raw = clive.getAttribute("data-turn-tokens");
          if (raw !== null && raw !== "") return Math.max(0, parseInt(raw, 10) || 0);
        }
        return 0;
      }

      function observe(snap) {
        var phase = snap.phase || "";
        var tool = snap.tool || "";
        var done = snap.done || 0;
        var failed = snap.failed || 0;
        var turn = snap.turn || "";
        var exactTokens = snap.exactTokens || 0;
        var activityAt = snap.activityAt || 0;
        var hasActivity = !!snap.hasActivity;

        var changed = phase !== lastPhase || tool !== lastTool || done !== lastDone || failed !== lastFailed || exactTokens !== lastExactTokens || activityAt !== lastActivityAt || turn !== lastTurn;
        if (changed) {
          lastLiveStamp = nowFn();
          stale = false;
        }

        var completedCount = 0;
        if (!seeded || (turn && turn !== lastTurn) || done < lastDone || failed < lastFailed) {
          seeded = true;
          lastTurn = turn;
          lastDone = done;
          lastFailed = failed;
          lastExactTokens = exactTokens;
        } else {
          var deltaDone = Math.max(0, done - lastDone);
          var deltaFailed = Math.max(0, failed - lastFailed);
          var totalDelta = deltaDone + deltaFailed;
          completedCount = totalDelta;
          var tokenDelta = exactTokens > lastExactTokens ? (exactTokens - lastExactTokens) : 0;
          if (exactTokens > lastExactTokens) lastExactTokens = exactTokens;
          if (totalDelta > 0) {
            var launchCat = lastTool ? classifyTool(lastTool) : (state.catId !== "thinking" ? state.catId : classifyTool(tool));
            var count = Math.min(3, totalDelta);
            // Explicit flight list: oks first, then fails; truncation keeps at least one of each kind present.
            var nFail = deltaFailed > 0 ? Math.max(1, Math.min(deltaFailed, count - (deltaDone > 0 ? 1 : 0))) : 0;
            var nOk = count - nFail;
            for (var c = 0; c < count; c++) {
              var isFail = c >= nOk;
              var isLast = c === count - 1;
              launchFlight(launchCat, isFail ? "fail" : "ok", isLast ? tokenDelta : 0);
            }
          }
          lastDone = done;
          lastFailed = failed;
          if (turn) lastTurn = turn;
        }

        var prevMode = state.runnerMode;
        if (hasActivity && phase === "thinking") {
          if (state.runnerMode !== "thinking") {
            state.runnerMode = "thinking";
            state.catId = "thinking";
            state.runnerT = 0;
            state.lastStepFrame = -1;
            state.swayTrail.length = 0;
            state.swayWeave.length = 0;
            state.popIn = null;
          }
          if (changed || !staleTimer) armStaleTimer();
        } else if (hasActivity && phase === "tool") {
          var nextCat = classifyTool(tool);
          var newTool = state.runnerMode !== "sway" || tool !== lastTool || completedCount > 0;
          state.catId = nextCat;
          if (newTool) {
            state.runnerMode = "sway";
            state.runnerT = 0;
            state.lastStepFrame = -1;
            state.swayTrail.length = 0;
            state.swayWeave.length = 0;
            if (canAnimate()) {
              measureGeometry();
              spawnPopIn(getPalette()[nextCat] || getPalette().file, prevMode === "thinking");
            }
          }
          if (changed || !staleTimer) armStaleTimer();
        } else {
          state.runnerMode = "idle";
          state.swayTrail.length = 0;
          state.swayWeave.length = 0;
          state.popIn = null;
          clearStaleTimer();
        }

        lastPhase = phase;
        lastTool = tool;
        lastActivityAt = activityAt;
        syncActivityDOM();
        ensureLoop();
      }

      function syncFromDOM() {
        var liveReg = findLiveRegion();
        var clive = liveReg && liveReg.querySelector ? liveReg.querySelector(".clive") : null;
        if (!clive) {
          observe({ phase: "idle", hasActivity: false });
          return;
        }
        observe({
          phase: clive.getAttribute("data-phase") || "",
          tool: clive.getAttribute("data-tool") || "",
          done: parseInt(clive.getAttribute("data-done") || "0", 10) || 0,
          failed: parseInt(clive.getAttribute("data-failed") || "0", 10) || 0,
          turn: clive.getAttribute("data-turn") || "",
          exactTokens: readExactTurnTokens(clive),
          activityAt: parseInt(clive.getAttribute("data-activity-at") || "0", 10) || 0,
          hasActivity: !!(clive.querySelector && clive.querySelector(".activity"))
        });
      }

      function setEnabled(on) {
        var next = !!on;
        if (next === enabled) return;
        enabled = next;
        if (!enabled) {
          stopAllMotion();
        } else {
          stale = false;
          if (state.runnerMode !== "idle") armStaleTimer();
        }
        syncActivityDOM();
        ensureLoop();
      }

      function setHidden(isHidden) {
        var next = !!isHidden;
        if (next === hidden) return;
        hidden = next;
        if (hidden) stopLoop();
        else ensureLoop();
      }

      function setReducedMotion(isReduced) {
        var next = !!isReduced;
        if (next === reducedMotion) return;
        reducedMotion = next;
        if (reducedMotion) stopAllMotion();
        syncActivityDOM();
        ensureLoop();
      }

      function relayout() {
        geomDirty = true;
        syncActivityDOM();
        ensureLoop();
      }

      function handleClick(e) {
        var t = e && e.target;
        if (!t || !t.closest) return;
        var btn = t.closest("[data-motion-mute]");
        if (!btn) return;
        e.preventDefault();
        e.stopPropagation();
        setEnabled(false);
        onMute();
      }

      function handleExtraMotionEvent(e) {
        if (e && e.detail && typeof e.detail.enabled === "boolean") setEnabled(e.detail.enabled);
      }

      function handleVisibilityEvent() {
        setHidden(!!(d && d.hidden) || (typeof shHidden !== "undefined" && !!shHidden));
      }

      function handleReducedMotionChange(e) {
        setReducedMotion(!!(e && e.matches));
      }

      if (d && d.addEventListener) {
        d.addEventListener("click", handleClick);
        d.addEventListener("ws-extra-motion", handleExtraMotionEvent);
        d.addEventListener("visibilitychange", handleVisibilityEvent);
        d.addEventListener("ws-page-visibility", handleVisibilityEvent);
      }
      if (mql && mql.addEventListener) mql.addEventListener("change", handleReducedMotionChange);

      function destroy() {
        clearStaleTimer();
        if (pulseTimer) { clearTimeout(pulseTimer); pulseTimer = 0; }
        stopAllMotion();
        if (d && d.removeEventListener) {
          d.removeEventListener("click", handleClick);
          d.removeEventListener("ws-extra-motion", handleExtraMotionEvent);
          d.removeEventListener("visibilitychange", handleVisibilityEvent);
          d.removeEventListener("ws-page-visibility", handleVisibilityEvent);
        }
        if (mql && mql.removeEventListener) mql.removeEventListener("change", handleReducedMotionChange);
        if (overlay && overlay.parentNode) overlay.parentNode.removeChild(overlay);
        else if (canvas && canvas.parentNode && !opts.canvas) canvas.parentNode.removeChild(canvas);
      }

      return {
        observe: observe,
        syncFromDOM: syncFromDOM,
        setEnabled: setEnabled,
        setHidden: setHidden,
        setReducedMotion: setReducedMotion,
        relayout: relayout,
        measureGeometry: measureGeometry,
        launchFlight: launchFlight,
        stepFrame: function (dt) {
          if (!canAnimate() || !hasActiveWork()) { stopLoop(); return; }
          if (geomDirty) measureGeometry();
          stepSim(dt);
          render();
          if (!hasActiveWork()) stopLoop();
        },
        getState: function () {
          return {
            enabled: enabled,
            hidden: hidden,
            reducedMotion: reducedMotion,
            stale: stale,
            rafActive: rafId !== 0,
            rafId: rafId,
            runnerMode: state.runnerMode,
            catId: state.catId,
            flights: state.flights.length,
            impacts: state.impacts.length,
            popups: state.popups.slice(),
            targetX: state.targetX,
            targetY: state.targetY,
            anchorX: state.anchorX,
            anchorY: state.anchorY,
            canvasHidden: canvas ? !!canvas.hidden : true
          };
        },
        destroy: destroy
      };
    }

    var singleton = null;
    function mount(opts) {
      if (singleton) singleton.destroy();
      singleton = createEngine(opts);
      singleton.syncFromDOM();
      return singleton;
    }
    function getInstance() { return singleton; }

    return {
      classifyTool: classifyTool,
      MOTION_REGISTRY: MOTION_REGISTRY,
      PALETTES: PALETTES,
      TENSION_KEYS: TENSION_KEYS,
      warpTension: warpTension,
      warpFail: warpFail,
      evalHermite2D: evalHermite2D,
      buildOkFlightProfile: buildOkFlightProfile,
      buildFailFlightProfile: buildFailFlightProfile,
      createEngine: createEngine,
      mount: mount,
      getInstance: getInstance
    };
  })();

  if (typeof window !== "undefined") window.WsConvMotion = WsConvMotion;

  var convMotionEngine = null;
  function convMotionInit() {
    if (convMotionEngine) return convMotionEngine;
    if (typeof doc === "undefined" || !doc || !doc.getElementById("convo")) return null;
    convMotionEngine = WsConvMotion.mount({});
    return convMotionEngine;
  }
  function convMotionSync() {
    var eng = convMotionInit();
    if (eng) eng.syncFromDOM();
  }
  function convMotionRelayout() {
    if (convMotionEngine) convMotionEngine.relayout();
  }
  if (typeof doc !== "undefined" && doc && doc.getElementById && doc.getElementById("convo")) {
    convMotionInit();
  }
