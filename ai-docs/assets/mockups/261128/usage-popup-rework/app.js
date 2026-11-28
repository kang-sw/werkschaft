/* app.js (v2) — two unified graphs (5H / 7D) in two axis modes + slot rows. Backend only via MockApi.call(). */
(function () {
  "use strict";
  var doc = document, LOCALE = "ko-KR", SVGNS = "http://www.w3.org/2000/svg";
  var DEFS = [
    { k: "claude", short: "Cl", name: "Claude", harness: "claude", group: "", bg: "#d97757", fg: "#fff" },
    { k: "codex", short: "Cx", name: "Codex", harness: "codex", group: "", bg: "#f2f2f2", fg: "#1e1f22" },
    { k: "agyg", short: "Ag·G", name: "Gemini", harness: "agy", group: "gemini", bg: "#3b7be8", fg: "#fff" },
    { k: "agyc", short: "Ag·C", name: "Claude / GPT", harness: "agy", group: "claude_gpt", bg: "#8b6fd6", fg: "#fff" }
  ];
  var S = {
    usage: null, acct: null, open: "usage", loading: true, error: null, mode: "progress",
    focus: null, lock: false, banners: {}, busy: {}, swap: null, confirm: null, confirmBusy: false,
    sessOpen: false, legend: false, refreshing: false
  };
  var $ = function (id) { return doc.getElementById(id); };

  // ---- helpers ---------------------------------------------------------------
  function el(tag, cls, text) { var n = doc.createElement(tag); if (cls) n.className = cls; if (text !== undefined) n.textContent = text; return n; }
  function svgNode(tag, attrs, text) {
    var n = doc.createElementNS(SVGNS, tag);
    Object.keys(attrs || {}).forEach(function (k) { n.setAttribute(k, String(attrs[k])); });
    if (text !== undefined) n.textContent = text; return n;
  }
  function icon(path, cls) { var s = svgNode("svg", { "class": cls || "ic", viewBox: "0 0 24 24" }); path.forEach(function (d) { s.appendChild(svgNode("path", { d: d })); }); return s; }
  var CHEV = ["M6 9l6 6 6-6"], CHECK = ["M5 12.5l4.5 4.5L19 7.5"];
  function clear(n) { while (n.firstChild) n.removeChild(n.firstChild); }
  function signed(n) { return (n > 0 ? "+" : n < 0 ? "−" : "") + Math.abs(n) + "%"; }
  function clockIcon(cls) { var s = svgNode("svg", { "class": cls, viewBox: "0 0 16 16", "aria-hidden": "true" }); s.appendChild(svgNode("circle", { cx: 8, cy: 8, r: 5.8 })); s.appendChild(svgNode("path", { d: "M8 4.8V8l2.2 1.4" })); return s; }
  var mctx = null;
  function measure(text, font) {
    if (!mctx) { var c = doc.createElement("canvas"); mctx = c.getContext && c.getContext("2d"); }
    if (!mctx) return Array.from(text).length * 6;
    mctx.font = font || "9px system-ui, sans-serif"; return mctx.measureText(text).width;
  }
  function hbadge(def, sm) { var n = el("span", "hb" + (sm ? " sm" : ""), def.short); n.setAttribute("data-k", def.k); return n; }
  var timeFmt = { hour: "2-digit", minute: "2-digit", hour12: false };
  function hhmm(t) { return new Date(t).toLocaleTimeString(LOCALE, timeFmt); }
  function wdhm(t) { return new Intl.DateTimeFormat(LOCALE, { weekday: "short", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(t)); }
  function remain(ms) {
    var m = Math.max(0, Math.round(ms / 60000));
    if (m < 60) return m + "m";
    if (m < 1440) return Math.floor(m / 60) + "h " + (m % 60) + "m";
    return Math.floor(m / 1440) + "d " + Math.floor(m % 1440 / 60) + "h";
  }
  function resetText(s, now) { return s.kind === "five_hour" ? remain(s.reset - now) : wdhm(s.reset); }
  var DUR = { five_hour: 5 * 3600000, seven_day: 7 * 86400000 };

  // ---- data model ------------------------------------------------------------
  function entryOf(def) { return ((S.usage && S.usage.entries) || []).filter(function (e) { return e.harness === def.harness && (e.group || "") === def.group; })[0]; }
  function slotsOf(h) { return (S.acct && S.acct.slots[h]) || []; }
  function series(def, kind) {
    var e = entryOf(def); if (!e || !e.windows.length) return null;
    var w = e.windows.filter(function (x) { return x.kind === kind; })[0]; if (!w) return null;
    var dur = DUR[kind], reset = w.resetsAt, start = reset - dur, at = w.asOf, now = Date.now(), pts = [{ t: start, p: 0 }];
    S.usage.history.samples.forEach(function (s) {
      if (s.harness === e.harness && (s.group || "") === (e.group || "") && s.kind === kind && s.resetsAt === reset && s.asOf >= start && s.asOf <= at) pts.push({ t: s.asOf, p: s.percent });
    });
    pts.push({ t: at, p: w.percent }); pts.sort(function (a, b) { return a.t - b.t; });
    var old = now - at > 30 * 60000, el_ = Math.max(0, Math.min(1, (now - start) / dur));
    return { def: def, e: e, w: w, kind: kind, dur: dur, start: start, reset: reset, at: at, pts: pts, pct: w.percent, old: old, elapsedNow: el_,
      delta: old ? null : Math.round(w.percent - el_ * 100), now: now };
  }
  function runsOf(pts, kind) {
    var limit = kind === "five_hour" ? 30 * 60000 : 12 * 3600000, out = [], cur = [pts[0]];
    for (var i = 1; i < pts.length; i++) {
      if (pts[i].t - pts[i - 1].t > limit && i > 1) { if (cur.length > 1) out.push({ gap: false, pts: cur }); out.push({ gap: true, pts: [pts[i - 1], pts[i]] }); cur = [pts[i]]; }
      else cur.push(pts[i]);
    }
    if (cur.length > 1) out.push({ gap: false, pts: cur });
    return out;
  }
  function tint(delta) { if (delta === null || delta === 0) return "var(--wb-muted-fg)"; return "color-mix(in srgb, var(--wb-muted-fg), var(" + (delta > 0 ? "--wb-fail" : "--wb-ok") + ") " + Math.min(100, Math.abs(delta)) + "%)"; }
  function load() {
    return Promise.all([MockApi.call("GET", "/rail/usage?history=1"), MockApi.call("GET", "/api/account/status")]).then(function (r) { S.usage = r[0]; S.acct = r[1]; S.error = null; });
  }

  // ---- label separation (collision avoidance with leader ticks) ---------------
  function separate(items, min, max, gap) {
    items.forEach(function (i) { i.ly = i.y; });
    function ovl(a, b) { return a.x < b.x + b.w && b.x < a.x + a.w; }
    for (var it = 0; it < 16; it++) {
      var moved = false;
      items.sort(function (a, b) { return a.ly - b.ly; });
      for (var i = 0; i < items.length; i++) for (var j = i + 1; j < items.length; j++) {
        var a = items[i], b = items[j];
        if (ovl(a, b) && b.ly - a.ly < gap - 0.01) { var d = (gap - (b.ly - a.ly)) / 2; a.ly -= d; b.ly += d; moved = true; }
      }
      items.forEach(function (i) { i.ly = Math.max(min, Math.min(max, i.ly)); });
      if (!moved) break;
    }
  }
  function pinNode(def, x, ly, w, label) {
    var g = svgNode("g", { "class": "pin", "data-k": def.k, tabindex: 0, role: "button", "aria-label": label, transform: "translate(" + x.toFixed(1) + "," + (ly - 7.5).toFixed(1) + ")" });
    g.style.color = def.fg;
    g.appendChild(svgNode("rect", { "class": "pb", width: w, height: 15, rx: 4, fill: def.bg }));
    if (def.k === "codex") g.appendChild(svgNode("rect", { width: w, height: 15, rx: 4, fill: "none", stroke: "rgb(0 0 0 / .18)", "stroke-width": 1 }));
    g.appendChild(svgNode("text", { x: w / 2, y: 10.6, "text-anchor": "middle" }, def.short));
    var t = g.lastChild; t.setAttribute("style", "fill:" + def.fg);
    return g;
  }
  function pinW(def) { return Math.ceil(measure(def.short, "800 9px ui-rounded, system-ui")) + 10; }
  function pathD(xy) { return xy.map(function (p, i) { return (i ? "L" : "M") + p.x.toFixed(1) + " " + p.y.toFixed(1); }).join(""); }
  function decimate(xy) {
    var out = [], lb = null;
    xy.forEach(function (p, i) { var b = Math.round(p.x / 1.5); if (out.length && b === lb && i < xy.length - 1) out[out.length - 1] = p; else out.push(p); lb = b; });
    return out;
  }
  var clipSerial = 0;
  // Draws one series group (line runs, focus area + excess wash, current dot). map(t,p) -> {x,y}.
  function serGroup(s, map, clipPoly, fillBase) {
    var g = svgNode("g", { "class": "ser" + (S.swap === s.def.harness ? " swapin" : ""), "data-k": s.def.k });
    var col = "var(--k-" + s.def.k + ")", id = "clip-" + (++clipSerial);
    var defs = svgNode("defs"), cp = svgNode("clipPath", { id: id }); cp.appendChild(svgNode("polygon", { points: clipPoly })); defs.appendChild(cp); g.appendChild(defs);
    runsOf(s.pts, s.kind).forEach(function (r) {
      var xy = r.pts.map(function (p) { return map(p.t, p.p); }); if (!r.gap) xy = decimate(xy);
      var last = xy[xy.length - 1], first = xy[0];
      if (!r.gap) {
        var base = pathD(xy) + "L" + last.x.toFixed(1) + " " + fillBase + "L" + first.x.toFixed(1) + " " + fillBase + "Z";
        g.appendChild(svgNode("path", { "class": "usage-area", d: base, fill: col }));
        g.appendChild(svgNode("path", { "class": "usage-excess", d: base, fill: "var(--wb-fail)", "clip-path": "url(#" + id + ")" }));
      }
      var line = svgNode("path", { "class": "usage-actual", d: pathD(xy), stroke: col, "stroke-width": 1.8, pathLength: 1 });
      if (r.gap) { line.setAttribute("stroke-dasharray", "4 3"); line.removeAttribute("pathLength"); }
      line.appendChild(svgNode("title", {}, r.gap ? "추정 구간 (보고 공백)" : "관측 구간")); g.appendChild(line);
    });
    var d = map(s.at, s.pct);
    var dot = svgNode("circle", { "class": "usage-point usage-point-current", cx: d.x, cy: d.y, r: 4, fill: s.old ? "var(--wb-muted-fg)" : col });
    dot.appendChild(svgNode("title", {}, s.def.short + " " + Math.round(s.pct) + "%")); g.appendChild(dot);
    return { g: g, dot: d, defs: defs };
  }
  function ariaLabel(s) { return s.def.name + " " + Math.round(s.pct) + "%" + (s.delta !== null ? " (" + signed(s.delta) + ")" : " 지연"); }

  // ---- Mode A: normalized window progress -----------------------------------
  function drawA(kind, W, list) {
    var H = 172, x0 = 30, x1 = W - 46, y0 = 14, y1 = H - 24;
    var X = function (e) { return x0 + e * (x1 - x0); }, Y = function (p) { return y1 - p / 100 * (y1 - y0); };
    var svg = svgNode("svg", { "class": "csvg", viewBox: "0 0 " + W + " " + H, role: "img", "aria-label": (kind === "five_hour" ? "5시간" : "주간") + " 진행률 대비 사용량" });
    svg.appendChild(svgNode("polygon", { "class": "wash", points: x0 + "," + y1 + " " + x1 + "," + y0 + " " + x0 + "," + y0 }));
    [0, 50, 100].forEach(function (p) { svg.appendChild(svgNode("line", { "class": "usage-grid", x1: x0, x2: x1, y1: Y(p), y2: Y(p) })); svg.appendChild(svgNode("text", { x: p === 0 ? 20 : p === 50 ? 14 : 4, y: Y(p) + 3 }, String(p))); });
    [0, 50, 100].forEach(function (p, i) {
      if (i) svg.appendChild(svgNode("line", { "class": "usage-day-grid", x1: X(p / 100), x2: X(p / 100), y1: y0, y2: y1 }));
      svg.appendChild(svgNode("text", { x: X(p / 100), y: H - 9, "text-anchor": i === 0 ? "start" : i === 2 ? "end" : "middle" }, p + "%"));
    });
    var id = svgNode("path", { "class": "usage-ideal", d: "M" + x0 + " " + y1 + "L" + x1 + " " + y0, "stroke-dasharray": "4 4" });
    id.appendChild(svgNode("title", {}, "이상 속도 (y = x)")); svg.appendChild(id);
    var poly = x0 + "," + y1 + " " + x1 + "," + y0 + " " + x1 + ",0 " + x0 + ",0", dots = {}, pins = [];
    list.forEach(function (s) {
      var r = serGroup(s, function (t, p) { return { x: X((t - s.start) / s.dur), y: Y(p) }; }, poly, y1);
      svg.appendChild(r.g); dots[s.def.k] = { x: r.dot.x, y: r.dot.y, s: s };
      pins.push({ s: s, x: r.dot.x + 8, y: r.dot.y, w: pinW(s.def) });
    });
    separate(pins, y0 - 3, y1 + 2, 16);
    pins.forEach(function (p) {
      var d = dots[p.s.def.k];
      svg.appendChild(svgNode("line", { "class": "leader", x1: d.x + 4, y1: d.y, x2: p.x, y2: p.ly }));
      svg.appendChild(pinNode(p.s.def, p.x, p.ly, p.w, ariaLabel(p.s)));
    });
    return { svg: svg, dots: dots, W: W, H: H };
  }

  // ---- Mode B: shared wall-clock Now + staggered Gantt lanes --------------------
  function drawB(kind, W, list) {
    var H = 236, x0 = 40, x1 = W - 10, yt = 16, yb = 126, ly0 = 142, lh = 13, ls = 17, yEnd = ly0 + 4 * ls - 4;
    var now = Date.now(), D0 = Math.min.apply(null, list.map(function (s) { return s.start; })), D1 = Math.max.apply(null, list.map(function (s) { return s.reset; }));
    var pad = (D1 - D0) * .02; D0 -= pad; D1 += pad;
    var X = function (t) { return x0 + (t - D0) / (D1 - D0) * (x1 - x0); }, Y = function (p) { return yb - p / 100 * (yb - yt); }, nowX = X(now);
    var svg = svgNode("svg", { "class": "csvg", viewBox: "0 0 " + W + " " + H, role: "img", "aria-label": (kind === "five_hour" ? "5시간" : "주간") + " 타임라인 및 간트 리셋 레인" });
    svg.appendChild(svgNode("rect", { "class": "fut", x: nowX, y: yt - 6, width: Math.max(0, x1 - nowX), height: yEnd - yt + 6 }));
    // time axis
    var pxMeasure = function (t) { return measure(t); };
    if (kind === "seven_day") {
      var mid = new Date(D0), right = -Infinity, fmt = new Intl.DateTimeFormat(LOCALE, { weekday: "short" }); mid.setHours(0, 0, 0, 0);
      while (mid.getTime() < D1) {
        var day = mid.getTime(); mid.setDate(mid.getDate() + 1); var nx = mid.getTime();
        if (day > D0) svg.appendChild(svgNode("line", { "class": "usage-day-grid", x1: X(day), x2: X(day), y1: yt, y2: yEnd }));
        var c = X(day + (nx - day) / 2), txt = fmt.format(new Date(day)), w = pxMeasure(txt);
        if (c - w / 2 >= x0 && c + w / 2 <= x1 && c - w / 2 >= right + 3) { svg.appendChild(svgNode("text", { "class": "usage-day-label", x: c, y: H - 8, "text-anchor": "middle" }, txt)); right = c + w / 2; }
      }
    } else {
      var pxPerH = (x1 - x0) / ((D1 - D0) / 3600000), stepH = Math.max(1, Math.ceil(36 / pxPerH)), t = new Date(D0); t.setMinutes(0, 0, 0);
      var first = t.getTime() < D0 ? t.getTime() + 3600000 : t.getTime();
      for (var tt = first; tt < D1; tt += 3600000) {
        if (new Date(tt).getHours() % stepH) continue;
        svg.appendChild(svgNode("line", { "class": "usage-day-grid", x1: X(tt), x2: X(tt), y1: yt, y2: yEnd }));
        var lbl = hhmm(tt); if (X(tt) - 12 >= x0 - 4 && X(tt) + 12 <= W) svg.appendChild(svgNode("text", { x: X(tt), y: H - 8, "text-anchor": "middle" }, lbl));
      }
    }
    [0, 50, 100].forEach(function (p) { svg.appendChild(svgNode("line", { "class": "usage-grid", x1: x0, x2: x1, y1: Y(p), y2: Y(p) })); svg.appendChild(svgNode("text", { x: p === 0 ? 30 : p === 50 ? 24 : 14, y: Y(p) + 3 }, String(p))); });
    // series (curve band)
    var pins = [], dots = {};
    list.forEach(function (s) {
      var map = function (t, p) { return { x: X(t), y: Y(p) }; };
      var poly = X(s.start) + "," + Y(0) + " " + X(s.reset) + "," + Y(100) + " " + X(s.reset) + ",0 " + X(s.start) + ",0";
      var r = serGroup(s, map, poly, yb);
      var ideal = svgNode("path", { "class": "fxd", d: "M" + X(s.start).toFixed(1) + " " + Y(0) + "L" + X(s.reset).toFixed(1) + " " + Y(100), stroke: "var(--k-" + s.def.k + ")", "stroke-width": 1.3, "stroke-dasharray": "4 4", fill: "none" });
      ideal.appendChild(svgNode("title", {}, s.def.short + " 이상 속도")); r.g.insertBefore(ideal, r.g.children[1]);
      svg.appendChild(r.g); dots[s.def.k] = { x: r.dot.x, y: r.dot.y, s: s };
      pins.push({ s: s, x: nowX + 8, y: r.dot.y, w: pinW(s.def) });
    });
    separate(pins, yt - 3, yb + 2, 16);
    // Gantt lanes
    var laneGroups = [];
    DEFS.forEach(function (def, i) {
      var s = list.filter(function (q) { return q.def === def; })[0], y = ly0 + i * ls;
      var g = svgNode("g", { "class": "gl" + (s && S.swap === def.harness ? " swapin" : ""), "data-k": def.k });
      var lab = svgNode("g", { transform: "translate(2," + y + ")" });
      var pw = pinW(def); lab.appendChild(svgNode("rect", { width: pw, height: lh, rx: 4, fill: def.bg, opacity: s ? 1 : .35 }));
      var lt = svgNode("text", { x: pw / 2, y: 9.6, "text-anchor": "middle", style: "font:800 9px ui-rounded,system-ui;fill:" + def.fg }, def.short); lab.appendChild(lt); g.appendChild(lab);
      if (!s) { g.appendChild(svgNode("text", { x: x0 + 4, y: y + 10 }, "인증 필요 — 레인 없음")); svg.appendChild(g); return; }
      var xs = X(s.start), xe = X(s.reset), cid = "lane-" + (++clipSerial), cp = svgNode("clipPath", { id: cid });
      cp.appendChild(svgNode("rect", { x: xs, y: y, width: xe - xs, height: lh, rx: lh / 2 })); g.appendChild(cp);
      g.appendChild(svgNode("rect", { "class": "trk", x: xs, y: y, width: xe - xs, height: lh, rx: lh / 2, fill: "color-mix(in srgb, var(--k-" + def.k + ") 17%, transparent)" }));
      var fe = xs + s.pct / 100 * (xe - xs), fg = svgNode("g", { "class": "fillg", "clip-path": "url(#" + cid + ")" });
      fg.appendChild(svgNode("rect", { x: xs, y: y, width: Math.max(0, Math.min(fe, nowX) - xs), height: lh, fill: "var(--k-" + def.k + ")" }));
      if (fe > nowX) fg.appendChild(svgNode("rect", { x: nowX, y: y, width: fe - nowX, height: lh, fill: "var(--wb-fail)", opacity: .92 }));
      g.appendChild(fg);
      var txt = "↻ " + resetText(s, now), tw = measure(txt, "700 9px system-ui");
      if (xe + 5 + tw <= W - 1) g.appendChild(svgNode("text", { "class": "lbl out", x: xe + 5, y: y + 9.6 }, txt));
      else g.appendChild(svgNode("text", { "class": "lbl", x: xe - 6, y: y + 9.6, "text-anchor": "end" }, txt));
      g.appendChild(svgNode("title", {}, def.name + ": " + Math.round(s.pct) + "% · 리셋 " + new Date(s.reset).toLocaleString(LOCALE)));
      svg.appendChild(g);
    });
    // Now line + pinned badges
    svg.appendChild(svgNode("line", { "class": "usage-now", x1: nowX, x2: nowX, y1: yt - 4, y2: yEnd + 2, "stroke-dasharray": "2 3" }));
    svg.appendChild(svgNode("text", { "class": "usage-now-label", x: nowX, y: yt - 6, "text-anchor": "middle" }, "Now"));
    pins.forEach(function (p) {
      var d = dots[p.s.def.k];
      svg.appendChild(svgNode("line", { "class": "leader", x1: d.x + 4, y1: d.y, x2: p.x, y2: p.ly }));
      svg.appendChild(pinNode(p.s.def, p.x, p.ly, p.w, ariaLabel(p.s)));
    });
    return { svg: svg, dots: dots, W: W, H: H };
  }

  // ---- charts section -----------------------------------------------------------
  var panels = [];
  function drawCharts() {
    var box = $("charts"); clear(box); panels = [];
    if (S.loading || !S.usage) { var sk = el("div", "cgrid"); sk.appendChild(el("div", "skel")); sk.appendChild(el("div", "skel")); box.appendChild(sk); return; }
    if ($("usage-pop").hidden) return;
    var grid = el("div", "cgrid"); box.appendChild(grid);
    [["five_hour", "5시간", "5H"], ["seven_day", "주간", "7D"]].forEach(function (cfg) {
      var kind = cfg[0], p = el("div", "cpanel"), h = el("div", "cp-h");
      h.appendChild(el("span", "cp-t", cfg[1] + " (" + cfg[2] + ")"));
      h.appendChild(el("span", "cp-s", S.mode === "progress" ? "윈도우 경과 % → 사용 %" : "wall-clock · 리셋 간트"));
      p.appendChild(h); var body = el("div", "cp-body"); p.appendChild(body); grid.appendChild(p);
      var W = Math.round(body.clientWidth) || 300, list = DEFS.map(function (d) { return series(d, kind); }).filter(Boolean);
      var panel = { el: p, body: body, kind: kind, dots: {}, svg: null };
      if (!list.length) { body.appendChild(el("div", "pstatus", "연결된 하네스 없음")); }
      else {
        var r = S.mode === "progress" ? drawA(kind, W, list) : drawB(kind, W, list);
        body.appendChild(r.svg); panel.dots = r.dots; panel.svg = r.svg; panel.W = r.W; panel.H = r.H;
        var miss = DEFS.filter(function (d) { return !list.some(function (s) { return s.def === d; }); });
        if (miss.length) p.appendChild(el("div", "cp-note", miss.map(function (d) { return d.short; }).join(" · ") + " 인증 필요 — 곡선 없음"));
      }
      panels.push(panel);
    });
    applyFocus();
  }

  // ---- focus / dim ----------------------------------------------------------------
  function applyFocus() {
    var f = S.focus;
    doc.querySelectorAll("#usage-pop .ser, #usage-pop .pin, #usage-pop .gl, #usage-pop .hr[data-k]").forEach(function (n) {
      var k = n.getAttribute("data-k"); n.classList.toggle("is-focus", !!f && k === f); n.classList.toggle("is-dim", !!f && k !== f);
    });
    doc.querySelectorAll("#usage-pop .callout").forEach(function (n) { n.remove(); });
    if (!f) return;
    panels.forEach(function (pn) {
      var d = pn.dots[f]; if (!d || !pn.svg) return;
      var s = d.s, c = el("div", "callout"), now = Date.now();
      c.appendChild(hbadge(s.def, true)); c.appendChild(el("b", "", Math.round(s.pct) + "%"));
      if (s.delta !== null) c.appendChild(el("span", s.delta > 0 ? "over" : "under", "(" + signed(s.delta) + ")"));
      c.appendChild(el("span", "dim", "↻ " + (s.kind === "five_hour" ? remain(s.reset - now) : wdhm(s.reset))));
      if (s.old) c.appendChild(el("span", "warnc", Math.round((now - s.at) / 60000) + "분 전 보고 · 지연"));
      pn.el.appendChild(c);
      var sr = pn.svg.getBoundingClientRect(), pr = pn.el.getBoundingClientRect();
      var px = sr.left - pr.left + d.x / pn.W * sr.width, py = sr.top - pr.top + d.y / pn.H * sr.height;
      var left = Math.max(4, Math.min(pr.width - c.offsetWidth - 4, px - c.offsetWidth / 2));
      var top = py - c.offsetHeight - 10; if (top < 22) top = py + 12;
      c.style.left = left + "px"; c.style.top = top + "px";
    });
  }
  function setFocus(k, lock) { S.focus = k; if (lock !== undefined) S.lock = lock; applyFocus(); }

  // ---- bottom toggle / sync -------------------------------------------------------
  function drawToggle(flash) {
    var b = $("usage-toggle"); clear(b); var now = Date.now();
    DEFS.forEach(function (def) {
      var e = entryOf(def); if (!e) return;
      var g = el("span", "u-grp"); g.title = def.name; g.appendChild(hbadge(def, true));
      if (!e.windows.length) { g.appendChild(el("span", "u-warn", "로그인")); b.appendChild(g); return; }
      var anyOld = false;
      e.windows.forEach(function (w) {
        var old = now - w.asOf > 30 * 60000; anyOld = anyOld || old;
        var it = el("span", "u-item" + (old ? " u-stale" : "")); it.appendChild(el("span", "u-k", w.kind === "five_hour" ? "5h" : "7d"));
        it.appendChild(el("span", "u-n", Math.round(w.percent) + "%"));
        var el_ = Math.max(0, Math.min(1, (now - (w.resetsAt - DUR[w.kind])) / DUR[w.kind])), dl = old ? null : Math.round(w.percent - el_ * 100);
        if (dl !== null) it.appendChild(el("span", "u-pace " + (dl > 0 ? "over" : dl < 0 ? "under" : ""), signed(dl)));
        g.appendChild(it);
      });
      if (anyOld) g.appendChild(clockIcon("u-clk"));
      b.appendChild(g);
    });
    b.appendChild(icon(CHEV, "ic u-caret"));
    if (flash) { b.classList.remove("u-flash"); void b.offsetWidth; b.classList.add("u-flash"); }
  }
  function drawSync() {
    var node = $("sync"), now = Date.now(), tone = "ok", txt = "방금 동기화", sm = 0;
    ((S.usage && S.usage.entries) || []).forEach(function (e) { e.windows.forEach(function (w) { if (now - w.asOf > 30 * 60000) sm = Math.max(sm, Math.round((now - w.asOf) / 60000)); }); });
    if (S.refreshing) { tone = "busy"; txt = "동기화 중…"; } else if (S.error) { tone = "err"; txt = "동기화 실패"; } else if (sm) { tone = "warn"; txt = sm + "분 전 · 일부 지연"; }
    node.setAttribute("data-tone", tone); $("sync-t").textContent = txt;
  }

  // ---- Group 1: provider rows ------------------------------------------------------
  function slotPills(h) {
    var wrap = el("span", "slots"); wrap.setAttribute("role", "group"); wrap.setAttribute("aria-label", h + " 슬롯");
    slotsOf(h).forEach(function (s) {
      var b = el("button", "sp" + (S.busy[h] === s.id ? " busy" : ""), s.label); b.type = "button"; b.setAttribute("aria-pressed", String(s.active));
      b.disabled = !!S.busy[h]; b.title = s.active ? s.label + " (활성)" : s.label + " 슬롯으로 전환";
      b.addEventListener("click", function (ev) { ev.stopPropagation(); if (!s.active) switchHarness(h, s); }); wrap.appendChild(b);
    });
    return wrap;
  }
  function numCell(def, kind) {
    var s = series(def, kind), c = el("div", "nc" + (s && s.old ? " is-stale" : ""));
    if (!s) return c;
    var t = el("div", "nc-t"); t.appendChild(el("span", "nc-k", kind === "five_hour" ? "5H" : "7D")); t.appendChild(el("span", "nc-n", Math.round(s.pct) + "%"));
    if (s.delta !== null) t.appendChild(el("span", "nc-p " + (s.delta > 0 ? "over" : s.delta < 0 ? "under" : ""), signed(s.delta)));
    if (s.old) { var ck = clockIcon("nc-clk"); ck.appendChild(svgNode("title", {}, Math.round((s.now - s.at) / 60000) + "분 전 보고 · 지연")); t.appendChild(ck); }
    var rs = el("span", "nc-r", "↻ " + resetText(s, s.now)); rs.title = "리셋 " + new Date(s.reset).toLocaleString(LOCALE); t.appendChild(rs); c.appendChild(t);
    var mg = el("div", "mg"); mg.style.setProperty("--kc", "var(--k-" + def.k + ")");
    var el_ = s.elapsedNow * 100, p = s.pct, f1 = el("i", "f1"), nw = el("i", "nw");
    f1.style.width = Math.min(p, el_) + "%"; mg.appendChild(f1);
    if (p > el_) { var f2 = el("i", "f2"); f2.style.left = el_ + "%"; f2.style.width = (p - el_) + "%"; mg.appendChild(f2); }
    nw.style.left = el_ + "%"; mg.appendChild(nw); mg.title = "트랙 = 윈도우 전체 · 세로선 = 현재 시각 · 채움 = 사용 %"; c.appendChild(mg);
    return c;
  }
  function authCta(def) {
    var b = el("button", "pill fail sm", "로그인 필요"); b.type = "button";
    b.addEventListener("click", function (ev) { ev.stopPropagation(); showBanner(def.name + " 인증은 <code>/settings?section=account</code> 에서 진행합니다 (목업: 이동하지 않음)"); }); return b;
  }
  function banner(h) {
    var b = S.banners[h]; if (!b) return null;
    var n = el("div", "rbanner" + (b.tone ? " " + b.tone : "")); n.setAttribute("role", "status");
    n.appendChild(icon(b.tone === "warn" ? ["M12 8v5", "M12 16.5v.5"] : CHECK)); n.appendChild(el("span", "", b.text)); return n;
  }
  function row(def, opts) {
    var e = entryOf(def), r = el("div", "hr"); if (!e) return r;
    r.setAttribute("data-k", def.k);
    var id = el("div", "hr-id"); id.appendChild(hbadge(def)); id.appendChild(el("span", "hr-name", def.name));
    if (opts && opts.slots && slotsOf(def.harness).length > 1) id.appendChild(slotPills(def.harness));
    r.appendChild(id);
    if (!e.windows.length) {
      var st = el("div", "hr-status"); st.appendChild(el("span", "", e.harness === "agy" ? "인증 필요 · 곡선 없음" : "토큰 만료 · 재인증 필요"));
      if (e.harness !== "agy") st.appendChild(authCta(def)); r.appendChild(st);
    } else { r.appendChild(numCell(def, "five_hour")); r.appendChild(numCell(def, "seven_day")); }
    return r;
  }
  function drawGroup1() {
    var g = $("grp1"); clear(g);
    var hd = el("div", "grp-h"); hd.appendChild(el("span", "grp-n", "①")); var t = el("span", "grp-t", "외부 CLI 하네스 플랜 · 슬롯"); t.appendChild(el("small", "", "행에 마우스를 올리면 그래프에서 강조")); hd.appendChild(t); g.appendChild(hd);
    if (S.loading || !S.usage) { g.appendChild(el("div", "skel")); return; }
    var rows = el("div", "rows");
    rows.appendChild(row(DEFS[0], { slots: true })); var b1 = banner("claude"); if (b1) rows.appendChild(b1);
    rows.appendChild(row(DEFS[1], { slots: true })); var b2 = banner("codex"); if (b2) rows.appendChild(b2);
    var auth = entryOf(DEFS[2]) && entryOf(DEFS[2]).windows.length > 0, hg = el("div", "hg"), hh = el("div", "hg-h");
    var ab = el("span", "hb", "Ag"); ab.setAttribute("data-k", "agy"); hh.appendChild(ab); hh.appendChild(el("span", "hr-name", "Antigravity"));
    if (auth) hh.appendChild(el("span", "pill ok sm dot", "단일 계정")); else hh.appendChild(authCta({ name: "Antigravity" }));
    hg.appendChild(hh); var kids = el("div", "hg-kids"); kids.appendChild(row(DEFS[2])); kids.appendChild(row(DEFS[3])); hg.appendChild(kids); rows.appendChild(hg);
    var sess = el("div", "sess"); rows.appendChild(sess); S.sessBox = sess; g.appendChild(rows);
    drawSessions();
    applyFocus();
  }
  function drawGroup2() {
    var g = $("grp2"); clear(g);
    var hd = el("div", "grp-h"); hd.appendChild(el("span", "grp-n", "②")); hd.appendChild(el("span", "grp-t", "내장 Werk 런타임 · API")); g.appendChild(hd);
    var ph = el("div", "ph"); ph.appendChild(el("b", "", "② 내장 Werk · API 사용량"));
    ph.appendChild(el("span", "", "OpenRouter · Werk OpenAI · 로컬 엔드포인트는 후속 별도 리워크 예정")); ph.appendChild(el("span", "rsp")); ph.appendChild(el("span", "pill sm", "플레이스홀더")); g.appendChild(ph);
  }

  // ---- sessions drawer ------------------------------------------------------------
  function sessions() { return ((S.acct && S.acct.sessions) || []).filter(function (c) { return slotsOf(c.backend).length > 1; }); }
  function labelOf(h, id) { var s = slotsOf(h).filter(function (x) { return x.id === id; })[0]; return s ? s.label : id; }
  var BK = { claude: DEFS[0], codex: DEFS[1] };
  function drawSessions() {
    var box = S.sessBox; if (!box) return; clear(box);
    var list = sessions(), pend = list.filter(function (c) { return c.pending_account_slot; }).length;
    var hd = el("button", "sess-hd"); hd.type = "button"; hd.setAttribute("aria-expanded", String(S.sessOpen));
    hd.appendChild(icon(CHEV)); hd.appendChild(el("span", "", "활성 세션별 슬롯 (" + list.length + ")"));
    if (pend) hd.appendChild(el("span", "pill warn sm", "전환 대기 " + pend));
    hd.appendChild(el("span", "rsp")); hd.appendChild(el("span", "sub", S.sessOpen ? "접기" : "세션별로 다른 슬롯 지정"));
    hd.addEventListener("click", function (ev) { ev.stopPropagation(); S.sessOpen = !S.sessOpen; drawSessions(); }); box.appendChild(hd);
    if (!S.sessOpen) return;
    var body = el("div", "sess-body");
    if (!list.length) body.appendChild(el("div", "dim", "슬롯이 2개 이상인 활성 세션이 없습니다."));
    list.forEach(function (c) {
      var rw = el("div", "srow"), t = el("div", "srow-t");
      t.appendChild(hbadge(BK[c.backend])); t.appendChild(el("span", "ttl", c.title)); t.appendChild(el("span", "pill sm", c.backend)); rw.appendChild(t);
      var sl = el("div", "srow-slots");
      slotsOf(c.backend).forEach(function (s) {
        var cur = s.id === c.slot, q = c.pending_account_slot === s.id;
        var b = el("button", "sp" + (q ? " queued" : ""), s.label); b.type = "button"; b.setAttribute("aria-pressed", String(cur));
        b.addEventListener("click", function (ev) { ev.stopPropagation(); if (cur) return; S.confirm = { id: c.id, slot: s.id }; drawSessions(); }); sl.appendChild(b);
      });
      if (c.pending_account_slot) sl.appendChild(el("span", "pend", "→ 다음 턴: " + labelOf(c.backend, c.pending_account_slot)));
      rw.appendChild(sl);
      if (S.confirm && S.confirm.id === c.id) {
        var cf = el("div", "confirm"); cf.setAttribute("role", "group"); cf.appendChild(el("span", "", labelOf(c.backend, S.confirm.slot) + " 슬롯으로 전환할까요?"));
        [["지금 전환", "now", "primary"], ["턴 경계에서 전환", "queue", ""]].forEach(function (p) {
          var b = el("button", "cbtn " + p[2], p[0]); b.type = "button"; b.disabled = S.confirmBusy; b.setAttribute("data-mode", p[1]);
          b.addEventListener("click", function (ev) { ev.stopPropagation(); switchSession(c, S.confirm.slot, p[1]); }); cf.appendChild(b);
        });
        var cx = el("button", "cbtn", "취소"); cx.type = "button"; cx.disabled = S.confirmBusy; cx.addEventListener("click", function (ev) { ev.stopPropagation(); S.confirm = null; drawSessions(); }); cf.appendChild(cx);
        rw.appendChild(cf);
      }
      body.appendChild(rw);
    });
    box.appendChild(body);
  }

  // ---- actions --------------------------------------------------------------------
  var bannerTimer = null;
  function showBanner(html, err) {
    var b = $("up-banner"); b.className = "up-banner" + (err ? " err" : ""); b.innerHTML = html; b.hidden = false;
    clearTimeout(bannerTimer); bannerTimer = setTimeout(function () { b.hidden = true; }, 4200);
  }
  function switchHarness(h, slot) {
    S.busy[h] = slot.id; delete S.banners[h]; drawGroup1();
    MockApi.call("POST", "/api/account/slots/switch", { harness: h, slot_id: slot.id }).then(function (r) {
      return load().then(function () {
        var n = r.affected_sessions, name = h === "claude" ? "Claude" : "Codex";
        var tail = n ? "진행 중인 " + name + " 세션 " + n + "개는 다음 턴 경계에서 전환" : "진행 중인 " + name + " 세션 없음";
        S.banners[h] = { text: r.slot_label + " 슬롯 활성 · " + tail }; S.swap = h; S.error = null;
        setTimeout(function () { delete S.banners[h]; drawGroup1(); }, 7000);
      });
    }).catch(function (e) { S.error = e.message; S.banners[h] = { text: "슬롯 전환 실패: " + e.message, tone: "warn" }; })
      .then(function () { delete S.busy[h]; renderAll(true); setTimeout(function () { S.swap = null; }, 900); });
  }
  function switchSession(c, slot, mode) {
    S.confirmBusy = true; drawSessions();
    MockApi.call("POST", "/api/account/slots/switch", { harness: c.backend, slot_id: slot, conversation_id: c.id, mode: mode }).then(load)
      .then(function () { S.confirm = null; showBanner((mode === "now" ? "즉시 전환: " : "다음 턴 경계에서 전환: ") + "<code>" + c.title + "</code> → " + labelOf(c.backend, slot)); })
      .catch(function (e) { showBanner("전환 실패: " + e.message, true); })
      .then(function () { S.confirmBusy = false; renderAll(); });
  }
  function doRefresh() {
    var b = $("btn-refresh"); b.classList.add("spin"); S.refreshing = true; drawSync();
    load().catch(function (e) { S.error = e.message; }).then(function () { S.refreshing = false; b.classList.remove("spin"); renderAll(); });
  }

  // ---- render / legend ---------------------------------------------------------------
  function renderAll(flash) { drawToggle(flash); drawSync(); drawCharts(); drawGroup1(); drawGroup2(); }
  function reloadAll(skeleton) { S.loading = !!skeleton; S.refreshing = true; renderAll(); return load().catch(function (e) { S.error = e.message; }).then(function () { S.loading = false; S.refreshing = false; renderAll(); }); }
  function renderLegend() {
    var L = $("legend"); clear(L);
    function item(inner, label, desc) {
      var s = el("span"), v = svgNode("svg", { viewBox: "0 0 34 14" }); inner(v); s.appendChild(v);
      var t = el("span"); t.appendChild(el("b", "", label + " ")); t.appendChild(doc.createTextNode(desc)); s.appendChild(t); L.appendChild(s);
    }
    var ln = function (v, a) { v.appendChild(svgNode("path", Object.assign({ "class": "ln", "stroke-width": 1.2 }, a))); };
    if (S.mode === "progress") {
      item(function (v) { ln(v, { d: "M2 12L32 2", "stroke-dasharray": "4 4" }); }, "이상 속도 y = x", "모든 하네스 공통 대각선");
      item(function (v) { v.appendChild(svgNode("polygon", { points: "2,12 32,2 2,2", fill: "var(--wb-fail)", opacity: .2 })); }, "붉은 영역", "대각선 위 = 과소비 구간");
      item(function (v) { v.appendChild(svgNode("rect", { x: 4, y: 1, width: 26, height: 12, rx: 3, fill: "var(--k-claude)" })); }, "끝점 배지", "각 하네스의 현재 경과 지점");
      item(function (v) { v.appendChild(svgNode("path", { d: "M2 9H12", stroke: "var(--wb-muted-fg)", "stroke-width": 2, fill: "none" })); v.appendChild(svgNode("path", { d: "M12 9L26 5", stroke: "var(--wb-muted-fg)", "stroke-width": 2, fill: "none", "stroke-dasharray": "4 3" })); }, "공백 브리지", "보고 공백 구간 (추정)");
      item(function (v) { v.appendChild(svgNode("circle", { cx: 17, cy: 7, r: 5.2, fill: "none", stroke: "var(--wb-warn-ink)", "stroke-width": 1.4 })); v.appendChild(svgNode("path", { d: "M17 4.5V7l1.8 1.1", fill: "none", stroke: "var(--wb-warn-ink)", "stroke-width": 1.4, "stroke-linecap": "round" })); }, "시계", "30분 넘게 보고 없음");
      item(function (v) { v.appendChild(svgNode("circle", { cx: 17, cy: 7, r: 4, fill: "var(--k-codex)" })); }, "호버/클릭", "해당 하네스를 5H·7D 모두 강조 (클릭 = 고정)");
    } else {
      item(function (v) { ln(v, { d: "M17 1V13", "stroke-dasharray": "2 3" }); }, "Now", "모든 하네스가 공유하는 현재 시각");
      item(function (v) { v.appendChild(svgNode("rect", { x: 2, y: 3, width: 30, height: 8, rx: 4, fill: "var(--k-claude)", opacity: .25 })); v.appendChild(svgNode("rect", { x: 2, y: 3, width: 14, height: 8, rx: 4, fill: "var(--k-claude)" })); }, "간트 바", "트랙 = 윈도우 전체 · 채움 = 사용 %");
      item(function (v) { v.appendChild(svgNode("rect", { x: 2, y: 3, width: 30, height: 8, rx: 4, fill: "var(--k-claude)", opacity: .25 })); v.appendChild(svgNode("rect", { x: 2, y: 3, width: 10, height: 8, fill: "var(--k-claude)" })); v.appendChild(svgNode("rect", { x: 12, y: 3, width: 12, height: 8, fill: "var(--wb-fail)" })); }, "붉은 구간", "Now를 넘은 채움 = 오버페이스");
      item(function (v) { var t = svgNode("text", { x: 2, y: 10, style: "font:700 9px system-ui;fill:var(--wb-muted-fg)" }, "↻ 42m"); v.appendChild(t); }, "↻ 라벨", "리셋까지 남은 시간 / 요일 시각");
      item(function (v) { ln(v, { d: "M2 12L32 2", "stroke-dasharray": "4 4" }); }, "개별 이상선", "호버한 하네스의 (시작→리셋) 대각선");
      item(function (v) { v.appendChild(svgNode("path", { d: "M2 9H12", stroke: "var(--wb-muted-fg)", "stroke-width": 2, fill: "none" })); v.appendChild(svgNode("path", { d: "M12 9L26 5", stroke: "var(--wb-muted-fg)", "stroke-width": 2, fill: "none", "stroke-dasharray": "4 3" })); }, "공백 브리지", "보고 공백 구간 (추정)");
    }
  }
  function open(which) {
    S.open = which; $("usage-pop").hidden = which !== "usage"; $("holders-pop").hidden = which !== "holders";
    $("usage-toggle").setAttribute("aria-expanded", String(which === "usage")); $("holders-pill").setAttribute("aria-expanded", String(which === "holders"));
    if (which === "usage" && S.usage) requestAnimationFrame(drawCharts);
  }
  function setMode(m) {
    S.mode = m; doc.querySelectorAll("#axis-seg .as-opt").forEach(function (b) { var on = b.getAttribute("data-v") === m; b.classList.toggle("is-on", on); b.setAttribute("aria-selected", String(on)); });
    drawCharts(); if (S.legend) renderLegend();
  }

  // ---- wiring ---------------------------------------------------------------------------
  $("usage-toggle").addEventListener("click", function () { open(S.open === "usage" ? null : "usage"); });
  $("holders-pill").addEventListener("click", function () { open(S.open === "holders" ? null : "holders"); });
  $("btn-close").addEventListener("click", function () { open(null); });
  $("hp-close").addEventListener("click", function () { open(null); });
  $("btn-refresh").addEventListener("click", doRefresh);
  $("btn-settings").addEventListener("click", function () { showBanner("계정 설정 열기 → <code>/settings?section=account</code> (목업: 이동하지 않음)"); });
  $("btn-legend").addEventListener("click", function () { S.legend = !S.legend; $("legend").hidden = !S.legend; $("btn-legend").setAttribute("aria-expanded", String(S.legend)); if (S.legend) renderLegend(); });
  $("axis-seg").addEventListener("click", function (e) { var b = e.target.closest(".as-opt"); if (b) setMode(b.getAttribute("data-v")); });
  var pop = $("usage-pop");
  pop.addEventListener("mouseover", function (e) {
    if (S.lock) return;
    var t = e.target.closest && e.target.closest(".pin, .gl, .hr[data-k]"), k = t ? t.getAttribute("data-k") : null;
    if (k !== S.focus) setFocus(k);
  });
  pop.addEventListener("mouseleave", function () { if (!S.lock && S.focus) setFocus(null); });
  pop.addEventListener("focusin", function (e) { var t = e.target.closest && e.target.closest(".pin"); if (t && !S.lock) setFocus(t.getAttribute("data-k")); });
  pop.addEventListener("keydown", function (e) { var t = e.target.closest && e.target.closest(".pin"); if (t && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); toggleLock(t.getAttribute("data-k")); } });
  function toggleLock(k) { if (S.lock && S.focus === k) { S.lock = false; setFocus(null); } else setFocus(k, true); }
  pop.addEventListener("click", function (e) {
    var t = e.target.closest && e.target.closest(".pin, .gl, .hr[data-k]");
    if (e.target.closest && e.target.closest("button")) return;
    if (t) toggleLock(t.getAttribute("data-k")); else if (S.lock) { S.lock = false; setFocus(null); }
  });
  doc.addEventListener("keydown", function (e) { if (e.key === "Escape") { if (S.lock) { S.lock = false; setFocus(null); } else if (S.open) open(null); } });
  $("stage").addEventListener("mousedown", function () { if (S.open) open(null); });
  var rz = null;
  if (window.ResizeObserver) new ResizeObserver(function () { clearTimeout(rz); rz = setTimeout(function () { if (!pop.hidden && S.usage) drawCharts(); }, 60); }).observe(pop);
  function seg(id, fn) {
    var root = $(id);
    root.addEventListener("click", function (e) {
      var b = e.target.closest(".m-opt"); if (!b) return;
      root.querySelectorAll(".m-opt").forEach(function (x) { x.classList.toggle("is-on", x === b); }); fn(b.getAttribute("data-v"));
    });
  }
  seg("m-theme", function (v) { doc.documentElement.setAttribute("data-theme", v); renderAll(); });
  seg("m-w", function (v) { pop.setAttribute("data-w", v); });
  seg("m-scn", function (v) {
    MockApi.setScenario(v); S.banners = {}; S.busy = {}; S.confirm = null; S.focus = null; S.lock = false;
    if (S.open !== "usage") open("usage"); reloadAll(true);
  });

  open("usage");
  reloadAll(true);
  window.__usageMock = { state: S };
})();
