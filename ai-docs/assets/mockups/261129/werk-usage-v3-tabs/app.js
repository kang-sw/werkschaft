/* Group ② (OpenRouter + Werk OpenAI) 2-Tab Visualization (v3)
   56 x 3-hour rolling 7-day sample engine, dual-axis stacked bars + burn curve + donut (Tab 1),
   and 0-based cumulative per-model geological strata area chart (Tab 2). */
(function () {
  "use strict";

  var doc = document;
  var NS = "http://www.w3.org/2000/svg";
  var REDUCED = !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);

  function $(id) { return doc.getElementById(id); }
  function el(tag, cls, text) {
    var n = doc.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  }
  function sv(tag, attrs, text) {
    var n = doc.createElementNS(NS, tag);
    Object.keys(attrs || {}).forEach(function (k) { n.setAttribute(k, String(attrs[k])); });
    if (text !== undefined) n.textContent = text;
    return n;
  }
  function clear(n) { while (n.firstChild) n.removeChild(n.firstChild); }
  function sum(a) { return a.reduce(function (s, x) { return s + x; }, 0); }
  function r2(x) { return Math.round(x * 100) / 100; }
  function r3(x) { return Math.round(x * 1000) / 1000; }
  function usd(n, d) {
    d = d === undefined ? 2 : d;
    return "$" + n.toFixed(d).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  }
  function tok(n) {
    if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e7 ? 1 : 2) + "M";
    if (n >= 1e3) return Math.round(n / 1e3) + "K";
    return String(Math.round(n));
  }
  function pct(f, d) { return (f * 100).toFixed(d === undefined ? 0 : d) + "%"; }
  function niceStep(raw) {
    if (raw <= 0) return 1;
    var p = Math.pow(10, Math.floor(Math.log10(raw)));
    var f = raw / p;
    return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10) * p;
  }
  function pillBtn(cls, text) {
    var b = el("button", cls, text);
    b.type = "button";
    return b;
  }

  // ---------------------------------------------------------------- dates (지금 = 2026-11-29 00:00, rolling 7d = 11/22 00:00 ~ 11/29 00:00)
  var WD = ["일", "월", "화", "수", "목", "금", "토"];
  function dateAt(off) { // off = -6..0 (day index d-6 where d=6 is 11/28)
    var dt = new Date(Date.UTC(2026, 10, 28) + off * 864e5);
    return { m: dt.getUTCMonth() + 1, d: dt.getUTCDate(), wd: WD[dt.getUTCDay()], off: off };
  }
  function md(o) { return o.m + "/" + o.d; }
  function mdw(o) { return o.m + "/" + o.d + " " + o.wd; }
  function bucketLabel(b) {
    var d = Math.floor(b / 8);
    var s = b % 8;
    var dd = dateAt(d - 6);
    var h0 = s * 3;
    var h1 = (s + 1) * 3;
    function pad2(n) { return n < 10 ? "0" + n : String(n); }
    return mdw(dd) + " · " + pad2(h0) + ":00–" + pad2(h1) + ":00";
  }

  // ---------------------------------------------------------------- models & 56-bucket scenario data
  var MODELS = [
    { k: "m0", name: "Claude Sonnet 4.5", short: "Sonnet 4.5", price: 5.4, rpd: 22 },
    { k: "m1", name: "GPT-5.4",           short: "GPT-5.4",    price: 4.4, rpd: 28 },
    { k: "m2", name: "Gemini 2.5 Pro",    short: "Gemini 2.5", price: 2.9, rpd: 34 },
    { k: "m3", name: "DeepSeek R1",       short: "DeepSeek R1", price: 0.9, rpd: 80 },
    { k: "m4", name: "기타 모델",          short: "기타",       price: 0.6, rpd: 120 }
  ];

  // Realistic 3-hour coding cadence weights (00-03, 03-06, 06-09, 09-12, 12-15, 15-18, 18-21, 21-24)
  var SLOT_W = [0.06, 0.015, 0.045, 0.175, 0.145, 0.21, 0.18, 0.17];

  function genDayCosts(total, base, d) {
    var w = base.map(function (b, m) {
      return b * (1 + 0.22 * Math.sin(d * 1.65 + m * 2.05 + 0.8));
    });
    var s = sum(w);
    var c = w.map(function (x) { return r2(total * x / s); });
    c[0] = r2(total - sum(c.slice(1)));
    return c;
  }

  function genWeek(totals, base) {
    return totals.map(function (t, d) { return genDayCosts(t, base, d); });
  }

  // Distribute each day's per-model spend across its 8 3-hour buckets with realistic model-specific coding bursts
  function expandTo56(perDay) {
    var buckets = [];
    for (var d = 0; d < 7; d++) {
      // Allocate each model m across the 8 3h slots of day d
      var daySlots = [];
      for (var s = 0; s < 8; s++) daySlots.push([0, 0, 0, 0, 0]);
      MODELS.forEach(function (_, m) {
        var mTot = perDay[d][m];
        var weights = SLOT_W.map(function (sw, s) {
          // Add model-specific burst wave so strata & burn steps feel realistic
          var burst = 1 + 0.45 * Math.sin((d * 8 + s) * 0.72 + m * 1.85);
          if (s === 1) burst *= 0.45; // quiet overnight sleep
          if ((d === 2 && s === 5 && m === 0) || (d === 4 && s === 6 && m === 1) || (d === 5 && s === 7 && m === 0)) {
            burst *= 1.65; // visible coding bursts
          }
          return Math.max(0.002, sw * burst);
        });
        var wSum = sum(weights);
        var acc = 0;
        for (var s = 0; s < 7; s++) {
          var v = r3(mTot * weights[s] / wSum);
          daySlots[s][m] = v;
          acc += v;
        }
        daySlots[7][m] = r3(Math.max(0, mTot - acc));
      });
      for (var s = 0; s < 8; s++) {
        buckets.push(daySlots[s]);
      }
    }
    return buckets;
  }

  var DEFS = {
    normal: {
      id: "normal",
      balance: 38.42,
      total: 100,
      topupAt: null, // { b: bucketIdx, amt: number }
      limitRemaining: null,
      // Sums to $23.68 -> start balance = 38.42 + 23.68 = $62.10
      perDay: [
        [1.12, 0.74, 0.42, 0.18, 0.12], // 2.58
        [0.84, 0.52, 0.34, 0.14, 0.08], // 1.92
        [1.82, 1.12, 0.64, 0.32, 0.16], // 4.06
        [1.64, 1.02, 0.62, 0.28, 0.14], // 3.70
        [1.56, 0.94, 0.58, 0.24, 0.12], // 3.44
        [1.72, 1.04, 0.62, 0.26, 0.14], // 3.78
        [1.94, 1.18, 0.68, 0.26, 0.14]  // 4.20 -> total 23.68
      ],
      oai: {
        paused: false,
        tokens: 142000,
        cost: 0.84,
        models: [["GPT-5.4", 98000, 0.58], ["GPT-5.4 mini", 31000, 0.09], ["GPT-5.3 Codex", 13000, 0.17]]
      }
    },
    topup: {
      id: "topup",
      balance: 86.40,
      total: 200,
      topupAt: { b: 35, amt: 100 }, // Day 4 (11/26) 09:00 jump +$100
      limitRemaining: null,
      perDay: genWeek([4.2, 3.6, 5.8, 5.6, 9.2, 9.4, 9.2], [0.36, 0.30, 0.18, 0.10, 0.06]),
      oai: {
        paused: false,
        tokens: 618000,
        cost: 3.92,
        models: [["GPT-5.4", 402000, 2.55], ["GPT-5.3 Codex", 148000, 0.91], ["GPT-5.4 mini", 68000, 0.46]]
      }
    },
    low: {
      id: "low",
      balance: 4.20,
      total: 100,
      topupAt: null,
      limitRemaining: 4.20,
      perDay: genWeek([2.4, 2.6, 3.2, 2.9, 3.4, 2.7, 2.4], [0.40, 0.30, 0.15, 0.08, 0.07]),
      oai: {
        paused: true,
        tokens: 96000,
        cost: 0.61,
        models: [["GPT-5.4", 71000, 0.44], ["GPT-5.4 mini", 25000, 0.17]]
      }
    }
  };

  function buildScenario(def) {
    var sc = {
      id: def.id,
      balance: def.balance,
      total: def.total,
      topupAt: def.topupAt,
      limitRemaining: def.limitRemaining,
      perDay: def.perDay,
      oai: def.oai
    };
    sc.tot = def.perDay.map(function (c) { return r2(sum(c)); });
    sc.weekly = r2(sum(sc.tot));
    sc.avg = Math.floor(sc.weekly / 7 * 100) / 100;
    sc.runway = sc.balance / sc.avg;
    sc.tone = sc.runway < 3 ? "fail" : sc.runway < 7 ? "warn" : "ok";

    sc.buckets = expandTo56(def.perDay); // [56][5] costs
    sc.btot = sc.buckets.map(function (b) { return r3(sum(b)); });
    sc.btok = sc.buckets.map(function (b) {
      return b.map(function (c, m) { return c / MODELS[m].price * 1e6; });
    });
    sc.dtok = def.perDay.map(function (c) {
      return c.map(function (x, m) { return x / MODELS[m].price * 1e6; });
    });
    sc.mcost = MODELS.map(function (_, m) {
      return r2(sum(def.perDay.map(function (c) { return c[m]; })));
    });
    sc.mtok = MODELS.map(function (_, m) {
      return sc.mcost[m] / MODELS[m].price * 1e6;
    });
    sc.order = MODELS.map(function (_, i) { return i; }).sort(function (a, b) {
      return sc.mcost[b] - sc.mcost[a];
    });

    // Reconstruct 57 balance boundary points (i = 0..56) backwards from sc.balance at i = 56
    // If topupAt.b === b, balance jumps up at boundary b (right before bucket b spend).
    sc.balBeforeTopup = null;
    sc.balAfterTopup = null;
    var pts = new Array(57);
    var bal = sc.balance;
    pts[56] = r2(bal);
    for (var b = 55; b >= 0; b--) {
      bal += sc.btot[b];
      if (sc.topupAt && sc.topupAt.b === b) {
        sc.balAfterTopup = r2(bal);
        bal -= sc.topupAt.amt;
        sc.balBeforeTopup = r2(bal);
      }
      pts[b] = r2(bal);
    }
    sc.start = pts[0];
    sc.bpts = pts; // balance at boundary i = 0..56 (at topup boundary i = topupAt.b, pts[i] is pre-topup; post-topup is balAfterTopup)

    // Cumulative series [57][5] for Tab 2 strata (i = 0 is 0, i = b+1 is cumulative through bucket b)
    sc.cumCost = [ [0, 0, 0, 0, 0] ];
    sc.cumTok = [ [0, 0, 0, 0, 0] ];
    for (b = 0; b < 56; b++) {
      var prevC = sc.cumCost[b];
      var prevT = sc.cumTok[b];
      sc.cumCost.push(MODELS.map(function (_, m) { return r3(prevC[m] + sc.buckets[b][m]); }));
      sc.cumTok.push(MODELS.map(function (_, m) { return prevT[m] + sc.btok[b][m]; }));
    }

    return sc;
  }

  var SCN = {
    normal: buildScenario(DEFS.normal),
    topup: buildScenario(DEFS.topup),
    low: buildScenario(DEFS.low)
  };

  function runwayTxt(sc) {
    return "약 " + (sc.runway >= 10 ? String(Math.round(sc.runway)) : sc.runway.toFixed(1)) + "일";
  }
  function curveColor(sc) {
    return sc.tone === "ok" ? "var(--wb-primary)" : sc.tone === "warn" ? "var(--wb-warn)" : "var(--wb-fail)";
  }

  // ---------------------------------------------------------------- app state
  var S = {
    scn: "normal",
    theme: "dark",
    w: "680",
    tab: "tab1",      // "tab1" | "tab2"
    metric: "cost",   // "cost" | "tok"
    axis: "dyn",      // "dyn" | "zero" (Tab 1 right Y-axis)
    pinModel: null    // locked model index 0..4 or null
  };

  var pending = [];
  var gid = 0;

  // ---------------------------------------------------------------- tooltip
  var tipEl = el("div", "tip");
  tipEl.hidden = true;
  doc.body.appendChild(tipEl);

  function tipNode(title, rows, foot) {
    var n = el("div", "tp");
    if (title) n.appendChild(el("div", "tp-t", title));
    rows.forEach(function (r) {
      if (r.div) { n.appendChild(el("div", "tp-div")); return; }
      var d = el("div", "tp-r");
      if (r.m !== undefined) {
        var dot = el("i", "dot");
        dot.style.background = "var(--" + MODELS[r.m].k + ")";
        d.appendChild(dot);
      }
      d.appendChild(el("span", "tp-l", r.l));
      d.appendChild(el("b", "tp-v" + (r.c ? " " + r.c : ""), r.v));
      n.appendChild(d);
    });
    if (foot) n.appendChild(el("div", "tp-f", foot));
    return n;
  }

  function showTip(node, x, y) {
    clear(tipEl);
    tipEl.appendChild(node);
    tipEl.hidden = false;
    var r = tipEl.getBoundingClientRect();
    var left = x + 12;
    var top = y - r.height - 12;
    if (left + r.width > innerWidth - 8) left = x - r.width - 12;
    if (left < 8) left = 8;
    if (top < 8) top = y + 16;
    tipEl.style.left = left + "px";
    tipEl.style.top = top + "px";
  }
  function hideTip() { tipEl.hidden = true; }
  function svgClient(svg, W, H, x, y) {
    var r = svg.getBoundingClientRect();
    return [r.left + x * r.width / W, r.top + y * r.height / H];
  }

  // ---------------------------------------------------------------- shared UI pieces
  function badge(k, t) {
    var b = el("span", "hb", t);
    b.setAttribute("data-k", k);
    return b;
  }
  function dotNode(i) {
    var d = el("i", "mdot");
    d.style.background = "var(--" + MODELS[i].k + ")";
    return d;
  }

  function alertBanner(sc) {
    if (sc.tone === "ok") return null;
    var b = el("div", "rbanner fail");
    b.setAttribute("role", "status");
    var ic = sv("svg", { "class": "ic", viewBox: "0 0 24 24" });
    ic.appendChild(sv("circle", { cx: 12, cy: 12, r: 9 }));
    ic.appendChild(sv("path", { d: "M12 8v5" }));
    ic.appendChild(sv("path", { d: "M12 16.5v.5" }));
    b.appendChild(ic);
    var t = el("span");
    t.appendChild(el("b", "", "잔고 부족 · "));
    t.appendChild(doc.createTextNode(usd(sc.balance) + " 남음 — 현재 속도(" + usd(sc.avg) + "/일)로 " + runwayTxt(sc) + " 내 소진 예상. 충전이 필요합니다."));
    b.appendChild(t);
    return b;
  }

  function oaiBadge(sc) {
    var o = sc.oai;
    var n = el("div", "estrip" + (o.paused ? " paused" : ""));
    n.appendChild(badge("wo", "WO"));
    n.appendChild(el("span", "nm", "Werk OpenAI"));
    n.appendChild(el("span", "pill sm dot " + (o.paused ? "warn" : "ok"), o.paused ? "플랜 일시중지" : "ChatGPT 플랜 통합"));
    n.appendChild(el("span", "tk", "· " + tok(o.tokens) + " tok"));
    n.addEventListener("pointerenter", function (e) {
      showTip(tipNode(
        "Werk OpenAI · 모델별 토큰",
        o.models.map(function (m) { return { l: m[0], v: tok(m[1]) + " · ≈" + usd(m[2]) }; }),
        o.paused ? "플랜 한도 도달 — 리셋까지 일시중지" : "ChatGPT/Codex 플랜에 통합 — 비용은 추정치"
      ), e.clientX, e.clientY);
    });
    n.addEventListener("pointermove", function (e) {
      if (!tipEl.hidden && tipEl.firstChild) showTip(tipEl.firstChild, e.clientX, e.clientY);
    });
    n.addEventListener("pointerleave", hideTip);
    return n;
  }

  // ---------------------------------------------------------------- Donut (Variant B)
  function arcPath(cx, cy, r0, r1, a0, a1) {
    var large = (a1 - a0) > Math.PI ? 1 : 0;
    function p(r, a) { return [cx + r * Math.sin(a), cy - r * Math.cos(a)]; }
    var s0 = p(r1, a0), e0 = p(r1, a1), s1 = p(r0, a1), e1 = p(r0, a0);
    function f(v) { return v.toFixed(2); }
    return "M" + f(s0[0]) + " " + f(s0[1]) +
      "A" + r1 + " " + r1 + " 0 " + large + " 1 " + f(e0[0]) + " " + f(e0[1]) +
      "L" + f(s1[0]) + " " + f(s1[1]) +
      "A" + r0 + " " + r0 + " 0 " + large + " 0 " + f(e1[0]) + " " + f(e1[1]) + "Z";
  }

  function norm(v) {
    var t = sum(v) || 1;
    return v.map(function (x) { return x / t; });
  }

  function Donut(size, thick, opts) {
    var self = this;
    this.size = size;
    this.r1 = size / 2 - 3;
    this.r0 = this.r1 - thick;
    this.o = opts || {};
    this.svg = sv("svg", {
      "class": "dsvg",
      viewBox: "0 0 " + size + " " + size,
      width: size,
      height: size,
      role: "img",
      "aria-label": "모델별 7일 사용 비중"
    });
    this.paths = MODELS.map(function (m, i) {
      var p = sv("path", { "class": "dslice", "data-m": i, fill: "var(--" + m.k + ")" });
      p.style.transformOrigin = size / 2 + "px " + size / 2 + "px";
      p.addEventListener("pointerenter", function (e) { if (self.o.onEnter) self.o.onEnter(i, e); });
      p.addEventListener("pointermove", function (e) { if (self.o.onMove) self.o.onMove(i, e); });
      p.addEventListener("pointerleave", function () { if (self.o.onLeave) self.o.onLeave(); });
      p.addEventListener("click", function () { if (self.o.onClick) self.o.onClick(i); });
      self.svg.appendChild(p);
      return p;
    });
    this.t1 = sv("text", { x: size / 2, y: size / 2 - 3, "class": "dc1" });
    this.t2 = sv("text", { x: size / 2, y: size / 2 + 13, "class": "dc2" });
    this.svg.appendChild(this.t1);
    this.svg.appendChild(this.t2);
    this.fr = MODELS.map(function () { return 0; });
    this.raf = 0;
  }
  Donut.prototype.center = function (a, b) {
    this.t1.textContent = a;
    this.t2.textContent = b;
  };
  Donut.prototype.setHi = function (k) {
    this.paths.forEach(function (p, i) {
      p.classList.toggle("is-dim", k !== null && i !== k);
      p.classList.toggle("is-hi", k !== null && i === k);
    });
  };
  Donut.prototype.render = function (fr) {
    var acc = 0;
    var nz = fr.filter(function (x) { return x > 0.0005; }).length;
    var gap = nz > 1 ? 0.03 : 0;
    var TAU = Math.PI * 2;
    var self = this;
    fr.forEach(function (f, i) {
      var a0 = acc * TAU;
      var a1 = (acc + f) * TAU;
      acc += f;
      if (f <= 0.0005) { self.paths[i].setAttribute("d", ""); return; }
      var s = a0 + gap / 2;
      var e = a1 - gap / 2;
      if (e - s < 0.01) e = s + 0.01;
      if (e - s > TAU - 0.0005) e = s + TAU - 0.0005;
      self.paths[i].setAttribute("d", arcPath(self.size / 2, self.size / 2, self.r0, self.r1, s, e));
    });
    this.fr = fr;
  };
  Donut.prototype.to = function (vals, ms) {
    var from = this.fr.slice();
    var to = norm(vals);
    var self = this;
    var t0 = null;
    cancelAnimationFrame(this.raf);
    if (REDUCED || !ms) { this.render(to); return; }
    function step(t) {
      if (t0 === null) t0 = t;
      var k = Math.min(1, (t - t0) / ms);
      var e = 1 - Math.pow(1 - k, 3);
      self.render(from.map(function (f, i) { return f + (to[i] - f) * e; }));
      if (k < 1) self.raf = requestAnimationFrame(step);
    }
    this.raf = requestAnimationFrame(step);
  };

  // ---------------------------------------------------------------- Tab 1 Composite Chart:
  // Left Y-axis (0-based): 7-day per-model stacked bars
  // Right Y-axis (dynamic/offset-skipped or $0-based): 3h rolling 56-bucket burn curve + live dot pinned at right edge
  function compositeChart(box, sc, opts) {
    var W = Math.max(240, Math.round(box.clientWidth));
    var narrow = W < 360;
    var H = narrow ? 226 : 214;
    var L = opts.metric === "cost" ? 36 : 40;
    var R = 50;
    var T = 25;
    var B = 30;
    var yb = H - B;
    var plotW = W - L - R;

    // X mapping across 56 3-hour buckets (0..56; x=56 is 지금 at right edge)
    var Xb = function (idx) { return L + (idx / 56) * plotW; };
    var dayW = plotW / 7;
    var bw = Math.min(26, dayW * 0.56);

    // Left Y-axis (0-based daily stacked bar scale)
    var dayVal = function (d, m) { return opts.metric === "cost" ? sc.perDay[d][m] : sc.dtok[d][m]; };
    var dayTotals = sc.perDay.map(function (_, d) {
      return sum(MODELS.map(function (_, m) { return dayVal(d, m); }));
    });
    var maxDay = Math.max.apply(null, dayTotals);
    // Give burn curve breathing space above bars on normal/topup, clean separation
    var leftStep = niceStep((maxDay * 1.18) / 3);
    var leftMax = Math.ceil((maxDay * 1.12) / leftStep) * leftStep;
    var YL = function (v) { return yb - (v / leftMax) * (yb - T); };
    var fmtLeft = function (v, axis) {
      if (v === 0) return "0";
      return opts.metric === "cost" ? usd(v, axis && v >= 1 ? 0 : (axis ? 1 : 2)) : tok(v);
    };

    // Right Y-axis (dynamic offset-skipped or $0-based, with 2-piece split on top-up jump)
    var allBal = sc.bpts.slice();
    if (sc.balAfterTopup !== null) allBal.push(sc.balAfterTopup);
    var minBal = Math.min.apply(null, allBal);
    var maxBal = Math.max.apply(null, allBal);
    var span = Math.max(1, maxBal - minBal);

    function rangeFor(axisMode) {
      if (axisMode === "zero") {
        var s0 = niceStep(maxBal / 3);
        return { pieces: [{ lo: 0, hi: Math.ceil((maxBal * 1.06) / s0) * s0, st: s0 }] };
      }
      // On low-balance scenario, keep $0-based automatically
      if (minBal < 10 && !sc.topupAt) {
        var sl = niceStep(maxBal / 3);
        return { pieces: [{ lo: 0, hi: Math.ceil((maxBal * 1.08) / sl) * sl, st: sl }] };
      }
      // Check if a mid-week top-up creates two distant balance bands
      if (sc.topupAt) {
        var bSplit = sc.topupAt.b;
        var preVals = sc.bpts.slice(0, bSplit + 1);
        var postVals = [sc.balAfterTopup].concat(sc.bpts.slice(bSplit + 1));
        var loPre = Math.min.apply(null, preVals), hiPre = Math.max.apply(null, preVals);
        var loPost = Math.min.apply(null, postVals), hiPost = Math.max.apply(null, postVals);
        if (loPost - hiPre > span * 0.25) {
          var st1 = niceStep((hiPre - loPre) / 2);
          var st2 = niceStep((hiPost - loPost) / 2);
          var p1lo = Math.max(0, Math.floor((loPre - (hiPre - loPre) * 0.18) / st1) * st1);
          var p1hi = Math.ceil((hiPre + (hiPre - loPre) * 0.14) / st1) * st1;
          var p2lo = Math.floor((loPost - (hiPost - loPost) * 0.18) / st2) * st2;
          var p2hi = Math.ceil((hiPost + (hiPost - loPost) * 0.15) / st2) * st2;
          if (p2lo > p1hi + 2) {
            return { pieces: [{ lo: p1lo, hi: p1hi, st: st1 }, { lo: p2lo, hi: p2hi, st: st2 }] };
          }
        }
      }
      var st = niceStep(span / 3.2);
      var yl = Math.floor((minBal - span * 0.22) / st) * st;
      var yh = Math.ceil((maxBal + span * 0.16) / st) * st;
      if (yl <= 2) yl = 0;
      return { pieces: [{ lo: yl, hi: yh, st: st }] };
    }

    function layoutPieces(r) {
      var G = 16;
      var n = r.pieces.length;
      var totSpan = 0;
      r.pieces.forEach(function (p) { totSpan += p.hi - p.lo; });
      // Reserve bottom 18px above yb when zoomed (single piece with lo > 0) for zigzag break marker
      var zoomedBottom = (n === 1 && r.pieces[0].lo > 0) ? 18 : 0;
      var availH = yb - T - zoomedBottom - G * (n - 1);
      var scale = availH / Math.max(1, totSpan);
      var off = zoomedBottom;
      var ps = [];
      r.pieces.forEach(function (p) {
        var h = (p.hi - p.lo) * scale;
        ps.push({ lo: p.lo, hi: p.hi, st: p.st, off: off, h: h });
        off += h + G;
      });
      function YR(v) {
        for (var i = 0; i < ps.length; i++) {
          var p = ps[i];
          if (v <= p.hi + 1e-9) {
            if (v >= p.lo - 1e-9 || i === 0) return yb - p.off - (v - p.lo) * scale;
            var q = ps[i - 1];
            var y0 = yb - q.off - q.h;
            var y1 = yb - p.off;
            var t = (v - q.hi) / (p.lo - q.hi);
            return y0 + t * (y1 - y0);
          }
        }
        var z = ps[ps.length - 1];
        return yb - z.off - (v - z.lo) * scale;
      }
      return { G: G, ps: ps, YR: YR, zoomedBottom: zoomedBottom };
    }

    var svg = sv("svg", {
      "class": "csvg",
      viewBox: "0 0 " + W + " " + H,
      role: "img",
      "aria-label": "일별 모델 소모 막대 및 3시간 단위 잔고 소진 곡선"
    });
    var id = "t1_" + (++gid);
    var col = curveColor(sc);
    var tgt = rangeFor(opts.axis);
    var lay = layoutPieces(tgt);
    var YR = lay.YR;

    var defs = sv("defs");
    var cp = sv("clipPath", { id: id + "c" });
    cp.appendChild(sv("rect", { x: L, y: T - 6, width: plotW + 2, height: yb - T + 8 }));
    defs.appendChild(cp);
    svg.appendChild(defs);

    // Axis top captions (y = 10 keeps >=15px clearance above T = 25)
    svg.appendChild(sv("text", { "class": "ax-hdr", x: L - 6, y: 10, "text-anchor": "end" }, opts.metric === "cost" ? "일 소모" : "토큰"));
    svg.appendChild(sv("text", { "class": "xl-now", x: W - R - 3, y: 10, "text-anchor": "end" }, "● 지금"));
    svg.appendChild(sv("text", { "class": "ax-hdr", x: W - R + 8, y: 10, "text-anchor": "start", style: "fill:" + col }, "잔고 (3h)"));

    // Faint horizontal gridlines tied to Left Y-axis (0-based)
    for (var lv = 0; lv <= leftMax + 1e-6; lv += leftStep) {
      var ylv = YL(lv);
      svg.appendChild(sv("line", { "class": "gl", x1: L, x2: W - R, y1: ylv, y2: ylv }));
      svg.appendChild(sv("text", { x: L - 6, y: ylv + 3, "text-anchor": "end" }, fmtLeft(lv, true)));
    }

    // Right Y-axis spine + ticks (avoid overlapping ticks and zigzag break label)
    var rx = W - R;
    svg.appendChild(sv("line", { "class": "ax-line", x1: rx, x2: rx, y1: T - 2, y2: yb }));
    var zoomedSingle = tgt.pieces.length === 1 && tgt.pieces[0].lo > 0;
    var breakY = yb - 9;
    var placedY = [];
    tgt.pieces.forEach(function (tp) {
      var startTick = Math.ceil(tp.lo / tp.st - 1e-9) * tp.st;
      for (var rv = startTick; rv <= tp.hi + 1e-6; rv += tp.st) {
        var yr = YR(rv);
        if (yr < T - 2 || yr > yb + 2) continue;
        if (zoomedSingle && Math.abs(yr - breakY) < 15) continue;
        var clash = placedY.some(function (py) { return Math.abs(py - yr) < 14; });
        if (clash) continue;
        placedY.push(yr);
        svg.appendChild(sv("line", { x1: rx, x2: rx + 3, y1: yr, y2: yr, stroke: "var(--wb-muted-fg)", "stroke-width": 1, opacity: 0.6 }));
        svg.appendChild(sv("text", { "class": "ax-r", x: rx + 7, y: yr + 3, "text-anchor": "start" }, usd(rv, tp.st % 1 === 0 ? 0 : 1)));
      }
    });

    // Amber zigzag axis-break on Right Y-axis when zoomed or 2-piece split
    if (zoomedSingle) {
      svg.appendChild(sv("line", { x1: L, x2: rx, y1: yb - lay.zoomedBottom, y2: yb - lay.zoomedBottom, stroke: "var(--wb-line)", "stroke-width": 0.7, "stroke-dasharray": "2 3", opacity: 0.55 }));
      svg.appendChild(sv("path", {
        d: "M" + (rx - 4) + " " + (breakY + 2) + "L" + (rx - 1) + " " + (breakY - 2) + "L" + (rx + 2) + " " + (breakY + 3) + "L" + (rx + 5) + " " + (breakY - 1),
        fill: "none",
        stroke: "var(--wb-warn)",
        "stroke-width": 1.6,
        "stroke-linecap": "round",
        "stroke-linejoin": "round"
      }));
      svg.appendChild(sv("text", { "class": "cap", x: rx + 8, y: breakY + 3, "text-anchor": "start" }, "≈ " + usd(tgt.pieces[0].lo, 0) + "~"));
    } else if (lay.ps.length > 1) {
      for (var gi = 1; gi < lay.ps.length; gi++) {
        var gy = yb - lay.ps[gi].off;
        var gLo = lay.ps[gi - 1].hi;
        var gHi = lay.ps[gi].lo;
        svg.appendChild(sv("rect", { x: L, y: gy, width: plotW, height: lay.G, fill: "var(--pbg)", opacity: 0.85 }));
        [gy, gy + lay.G].forEach(function (yy) {
          svg.appendChild(sv("line", { x1: L, x2: rx, y1: yy, y2: yy, stroke: "var(--wb-line)", "stroke-width": 0.8, "stroke-dasharray": "2 3" }));
        });
        svg.appendChild(sv("path", {
          d: "M" + (rx - 4) + " " + (gy + 6) + "L" + (rx - 1) + " " + (gy + 2) + "L" + (rx + 2) + " " + (gy + 8) + "L" + (rx + 5) + " " + (gy + 4),
          fill: "none",
          stroke: "var(--wb-warn)",
          "stroke-width": 1.6,
          "stroke-linecap": "round",
          "stroke-linejoin": "round"
        }));
        svg.appendChild(sv("text", { "class": "cap", x: L + 6, y: gy + lay.G / 2 + 3 }, "≈ " + usd(gLo, 0) + "–" + usd(gHi, 0) + " 구간 생략"));
      }
    } else {
      // $0 baseline tick on right axis if not already drawn
      var hasZero = placedY.some(function (py) { return Math.abs(py - yb) < 10; });
      if (!hasZero) {
        svg.appendChild(sv("text", { "class": "ax-r", x: rx + 7, y: yb + 3, "text-anchor": "start" }, "$0"));
      }
    }

    // Clipped plot group
    var plotG = sv("g", { "clip-path": "url(#" + id + "c)" });
    svg.appendChild(plotG);

    // 1. Stacked daily vertical bars (behind the 3h burn curve, restrained opacity so curve pops)
    var barSegs = [];
    for (var d = 0; d < 7; d++) {
      var cx = L + dayW * (d + 0.5);
      var x0 = cx - bw / 2;
      var acc = 0;
      MODELS.forEach(function (M, m) {
        var vv = dayVal(d, m);
        if (vv <= 0) return;
        var y1 = YL(acc);
        var y2 = YL(acc + vv);
        acc += vv;
        var seg = sv("rect", {
          "class": "bar-seg",
          x: x0.toFixed(1),
          y: y2.toFixed(1),
          width: bw.toFixed(1),
          height: Math.max(0.8, y1 - y2).toFixed(1),
          rx: 1.5,
          fill: "var(--" + M.k + ")",
          opacity: 0.48,
          "data-m": m
        });
        barSegs.push(seg);
        plotG.appendChild(seg);
      });
    }

    // 2. 3-Hour Rolling 56-sample Burn Curve Overlay
    function buildCurvePath(fromIdx, toIdx, startVal) {
      var cmds = [];
      for (var i = fromIdx; i <= toIdx; i++) {
        var val = (i === fromIdx && startVal !== undefined) ? startVal : sc.bpts[i];
        cmds.push((i === fromIdx ? "M" : "L") + Xb(i).toFixed(1) + " " + YR(val).toFixed(1));
      }
      return cmds.join("");
    }

    var paths = [];
    if (sc.topupAt) {
      var tb = sc.topupAt.b;
      paths.push(buildCurvePath(0, tb));
      paths.push(buildCurvePath(tb, 56, sc.balAfterTopup));
    } else {
      paths.push(buildCurvePath(0, 56));
    }

    // Subtle halo behind curve for crisp contrast over bars
    paths.forEach(function (dStr) {
      plotG.appendChild(sv("path", {
        d: dStr,
        fill: "none",
        stroke: "var(--pbg)",
        "stroke-width": 4.6,
        "stroke-linejoin": "round",
        "stroke-linecap": "round"
      }));
      plotG.appendChild(sv("path", {
        d: dStr,
        fill: "none",
        stroke: col,
        "stroke-width": 2.2,
        "stroke-linejoin": "round",
        "stroke-linecap": "round"
      }));
    });

    // Top-up jump arrow marker
    if (sc.topupAt) {
      var tx = Xb(sc.topupAt.b);
      var ty0 = YR(sc.balBeforeTopup);
      var ty1 = YR(sc.balAfterTopup);
      plotG.appendChild(sv("line", { x1: tx, x2: tx, y1: ty0, y2: ty1, stroke: "var(--pbg)", "stroke-width": 4.4 }));
      plotG.appendChild(sv("line", { x1: tx, x2: tx, y1: ty0, y2: ty1, stroke: "var(--wb-ok)", "stroke-width": 2.1, "stroke-linecap": "round" }));
      plotG.appendChild(sv("path", {
        d: "M" + (tx - 3.5) + " " + (ty1 + 5) + "L" + tx + " " + (ty1 + 0.5) + "L" + (tx + 3.5) + " " + (ty1 + 5),
        fill: "none",
        stroke: "var(--wb-ok)",
        "stroke-width": 2,
        "stroke-linecap": "round",
        "stroke-linejoin": "round"
      }));
      var lblLeft = tx - L > 90;
      svg.appendChild(sv("text", {
        "class": "up",
        x: tx + (lblLeft ? -6 : 6),
        y: Math.min(ty0 - 6, ty1 + 14),
        "text-anchor": lblLeft ? "end" : "start"
      }, "▲ +" + usd(sc.topupAt.amt, 0) + " 충전"));
    }

    // Live endpoint dot pinned at rightmost edge (지금 = Xb(56))
    var liveX = Xb(56);
    var liveY = YR(sc.balance);
    svg.appendChild(sv("circle", { cx: liveX, cy: liveY, r: 6.5, fill: col, opacity: 0.22 }));
    svg.appendChild(sv("circle", { cx: liveX, cy: liveY, r: 4, fill: col, stroke: "var(--pbg)", "stroke-width": 1.8 }));

    // X-axis day ticks & labels
    for (var d = 0; d < 7; d++) {
      var dxCenter = L + dayW * (d + 0.5);
      var dd = dateAt(d - 6);
      svg.appendChild(sv("line", { x1: L + dayW * d, x2: L + dayW * d, y1: yb, y2: yb + 3, stroke: "var(--wb-line)", "stroke-width": 0.7 }));
      svg.appendChild(sv("text", { x: dxCenter, y: yb + 12, "text-anchor": "middle", "class": d === 6 ? "xl-t" : "" }, dayW >= 42 ? md(dd) : String(dd.d)));
      svg.appendChild(sv("text", { x: dxCenter, y: yb + 23, "text-anchor": "middle", "class": d === 6 ? "xl-t" : "" }, dd.wd));
    }

    // Interactive 56-bucket crosshair + hover hit layer
    var cr = sv("line", { "class": "crs", y1: T - 4, y2: yb });
    var cd = sv("circle", { r: 4.5, fill: "var(--wb-card)", stroke: col, "stroke-width": 2, style: "pointer-events:none" });
    cr.style.display = cd.style.display = "none";
    svg.appendChild(cr);
    svg.appendChild(cd);

    var hit = sv("rect", { x: L, y: T - 4, width: plotW, height: yb - T + 8, fill: "transparent" });
    hit.style.cursor = "crosshair";
    svg.appendChild(hit);

    function showSample(b) {
      var xPos = Xb(b + 1);
      var balVal = sc.bpts[b + 1];
      var delta3h = sc.btot[b];
      var dIdx = Math.floor(b / 8);
      cr.style.display = cd.style.display = "";
      cr.setAttribute("x1", xPos);
      cr.setAttribute("x2", xPos);
      cd.setAttribute("cx", xPos);
      cd.setAttribute("cy", YR(balVal));

      var rows = [
        { l: "구간 마감 잔고", v: usd(balVal) },
        { l: "3시간 소모", v: "−" + usd(delta3h), c: "neg" }
      ];
      if (sc.topupAt && sc.topupAt.b === b) {
        rows.push({ l: "충전 반영", v: "+" + usd(sc.topupAt.amt, 0), c: "pos" });
      }
      rows.push({ div: true });
      sc.order.forEach(function (m) {
        var val = opts.metric === "cost" ? sc.buckets[b][m] : sc.btok[b][m];
        if (val <= 0) return;
        rows.push({
          m: m,
          l: MODELS[m].short,
          v: opts.metric === "cost" ? usd(val) : tok(val)
        });
      });
      var foot = mdw(dateAt(dIdx - 6)) + " 일 합계: " + (opts.metric === "cost" ? usd(sc.tot[dIdx]) : tok(sum(sc.dtok[dIdx])));
      var pc = svgClient(svg, W, H, xPos, YR(balVal));
      showTip(tipNode(bucketLabel(b) + (b === 55 ? " · 지금" : ""), rows, foot), pc[0], pc[1]);
    }

    hit.addEventListener("pointermove", function (ev) {
      var rect = svg.getBoundingClientRect();
      var px = (ev.clientX - rect.left) * W / rect.width;
      var frac = (px - L) / plotW;
      var b = Math.max(0, Math.min(55, Math.round(frac * 56 - 0.5)));
      showSample(b);
    });
    hit.addEventListener("pointerleave", function () {
      cr.style.display = cd.style.display = "none";
      hideTip();
    });

    clear(box);
    box.appendChild(svg);

    return {
      applyModelHi: function (k) {
        barSegs.forEach(function (s) {
          var m = Number(s.getAttribute("data-m"));
          s.classList.toggle("is-dim", k !== null && m !== k);
          s.classList.toggle("is-hi", k !== null && m === k);
        });
      }
    };
  }

  // ---------------------------------------------------------------- Tab 2 Cumulative Geological Strata Chart
  // 0-based Left Y-axis, 56 x 3-hour rolling samples, latest sample pinned at right edge
  function strataChart(box, sc, opts) {
    var W = Math.max(240, Math.round(box.clientWidth));
    var narrow = W < 440;
    var H = narrow ? 226 : 216;
    var L = opts.metric === "cost" ? 42 : 46;
    var R = 52;
    var T = 25;
    var B = 30;
    var yb = H - B;
    var plotW = W - L - R;
    var Xb = function (idx) { return L + (idx / 56) * plotW; };
    var dayW = plotW / 7;

    var cumSeries = opts.metric === "cost" ? sc.cumCost : sc.cumTok;
    var stack = sc.order; // heaviest primary model at bottom (k = 0) -> lightest at top (k = 4)
    var finalTotal = sum(cumSeries[56]);
    var step = niceStep((finalTotal * 1.08) / 4);
    var ymax = Math.ceil((finalTotal * 1.05) / step) * step;
    var Y = function (v) { return yb - (v / ymax) * (yb - T); };

    var fmtV = function (v, axis) {
      if (v === 0) return opts.metric === "cost" ? "$0" : "0";
      return opts.metric === "cost" ? usd(v, axis && v >= 5 ? 0 : (axis ? 1 : 2)) : tok(v);
    };

    var svg = sv("svg", {
      "class": "csvg",
      viewBox: "0 0 " + W + " " + H,
      role: "img",
      "aria-label": "7일 3시간 단위 모델별 누적 지층 그래프"
    });
    var id = "t2_" + (++gid);

    var defs = sv("defs");
    var cp = sv("clipPath", { id: id + "c" });
    cp.appendChild(sv("rect", { x: L, y: T - 6, width: plotW + 2, height: yb - T + 8 }));
    defs.appendChild(cp);
    svg.appendChild(defs);

    // Axis top header & right-edge "지금" marker
    svg.appendChild(sv("text", { "class": "ax-hdr", x: L - 6, y: 10, "text-anchor": "end" }, opts.metric === "cost" ? "누적 ($)" : "누적 토큰"));
    svg.appendChild(sv("text", { "class": "xl-now", x: L + plotW - 3, y: 10, "text-anchor": "end" }, "● 지금"));

    // Strictly 0-based gridlines
    for (var v = 0; v <= ymax + 1e-6; v += step) {
      var yv = Y(v);
      svg.appendChild(sv("line", { "class": "gl", x1: L, x2: L + plotW, y1: yv, y2: yv }));
      svg.appendChild(sv("text", { x: L - 6, y: yv + 3, "text-anchor": "end" }, fmtV(v, true)));
    }

    // Subtle daily vertical dividers
    for (var d = 1; d < 7; d++) {
      svg.appendChild(sv("line", { x1: L + dayW * d, x2: L + dayW * d, y1: T, y2: yb, stroke: "var(--wb-line)", "stroke-width": 0.6, opacity: 0.45, "stroke-dasharray": "2 3" }));
    }

    var plotG = sv("g", { "clip-path": "url(#" + id + "c)" });
    svg.appendChild(plotG);

    var bands = [];
    var lines = [];
    var topYAt = new Array(57);
    for (var i = 0; i <= 56; i++) topYAt[i] = 0;

    // Build sedimentary strata bottom-up (m0 heaviest at bottom -> m4 lightest at top)
    var upperByOrder = []; // store cumulative height per stratum index k at each sample i
    stack.forEach(function (m, k) {
      var upPts = [];
      var loPts = [];
      var upperArr = [];
      for (var i = 0; i <= 56; i++) {
        var loVal = topYAt[i];
        var hiVal = loVal + cumSeries[i][m];
        topYAt[i] = hiVal;
        upperArr.push(hiVal);
        upPts.push([Xb(i), Y(hiVal)]);
        loPts.push([Xb(i), Y(loVal)]);
      }
      upperByOrder.push({ m: m, vals: upperArr });

      var polyD = "M" + upPts.map(function (p) { return p[0].toFixed(1) + " " + p[1].toFixed(1); }).join("L") +
        "L" + loPts.slice().reverse().map(function (p) { return p[0].toFixed(1) + " " + p[1].toFixed(1); }).join("L") + "Z";
      var band = sv("path", {
        "class": "strata-band",
        d: polyD,
        fill: "var(--" + MODELS[m].k + ")",
        "fill-opacity": 0.62,
        "data-m": m
      });
      plotG.appendChild(band);
      bands.push(band);

      var lineD = "M" + upPts.map(function (p) { return p[0].toFixed(1) + " " + p[1].toFixed(1); }).join("L");
      var topLine = sv("path", {
        "class": "strata-line",
        d: lineD,
        stroke: "var(--" + MODELS[m].k + ")",
        "data-m": m
      });
      plotG.appendChild(topLine);
      lines.push(topLine);
    });

    // Rightmost edge endpoint dot & cumulative total callout
    var rightX = Xb(56);
    var topEndY = Y(finalTotal);
    svg.appendChild(sv("line", { x1: rightX, x2: rightX, y1: T, y2: yb, stroke: "var(--wb-line)", "stroke-width": 0.9 }));
    svg.appendChild(sv("circle", { cx: rightX, cy: topEndY, r: 4, fill: "var(--wb-fg)", stroke: "var(--pbg)", "stroke-width": 1.6 }));
    svg.appendChild(sv("text", { "class": "end-lbl", x: rightX + 6, y: topEndY + 3, "text-anchor": "start" }, fmtV(finalTotal, false)));

    // End-of-week subtle right-side model ticks when strata band is tall enough
    var prevLblY = topEndY + 12;
    for (var k = stack.length - 1; k >= 0; k--) {
      var mEnd = stack[k];
      var midVal = upperByOrder[k].vals[56] - cumSeries[56][mEnd] / 2;
      var ym = Y(midVal);
      if (ym - prevLblY >= 13 && ym <= yb - 5) {
        svg.appendChild(sv("text", {
          x: rightX + 6,
          y: ym + 3,
          "text-anchor": "start",
          style: "font-size:8.5px;font-weight:700;fill:var(--" + MODELS[mEnd].k + ")"
        }, pct(cumSeries[56][mEnd] / finalTotal)));
        prevLblY = ym;
      }
    }

    // X-axis day ticks & labels
    for (d = 0; d < 7; d++) {
      var dxCenter = L + dayW * (d + 0.5);
      var dd = dateAt(d - 6);
      svg.appendChild(sv("line", { x1: L + dayW * d, x2: L + dayW * d, y1: yb, y2: yb + 3, stroke: "var(--wb-line)", "stroke-width": 0.7 }));
      svg.appendChild(sv("text", { x: dxCenter, y: yb + 12, "text-anchor": "middle", "class": d === 6 ? "xl-t" : "" }, dayW >= 42 ? md(dd) : String(dd.d)));
      svg.appendChild(sv("text", { x: dxCenter, y: yb + 23, "text-anchor": "middle", "class": d === 6 ? "xl-t" : "" }, dd.wd));
    }

    // Hover crosshair & stratum sample dots
    var cr = sv("line", { "class": "crs", y1: T - 4, y2: yb });
    cr.style.display = "none";
    svg.appendChild(cr);

    var sampleDots = stack.map(function (m) {
      var c = sv("circle", { r: 3.4, fill: "var(--" + MODELS[m].k + ")", stroke: "var(--pbg)", "stroke-width": 1.4, style: "pointer-events:none" });
      c.style.display = "none";
      svg.appendChild(c);
      return c;
    });

    var hit = sv("rect", { x: L, y: T - 4, width: plotW, height: yb - T + 8, fill: "transparent" });
    hit.style.cursor = "crosshair";
    svg.appendChild(hit);

    function showStrataSample(b) {
      var idx = b + 1; // 1..56
      var xPos = Xb(idx);
      var cumTotAt = sum(cumSeries[idx]);
      var burstAt = opts.metric === "cost" ? sc.btot[b] : sum(sc.btok[b]);
      cr.style.display = "";
      cr.setAttribute("x1", xPos);
      cr.setAttribute("x2", xPos);

      upperByOrder.forEach(function (u, k) {
        sampleDots[k].style.display = "";
        sampleDots[k].setAttribute("cx", xPos);
        sampleDots[k].setAttribute("cy", Y(u.vals[idx]));
      });

      var rows = [
        { l: "누적 합계", v: fmtV(cumTotAt, false) },
        { l: "3시간 증가분", v: "+" + fmtV(burstAt, false), c: "pos" },
        { div: true }
      ];
      stack.forEach(function (m) {
        var mv = cumSeries[idx][m];
        var mb = opts.metric === "cost" ? sc.buckets[b][m] : sc.btok[b][m];
        var share = cumTotAt > 0 ? pct(mv / cumTotAt) : "0%";
        rows.push({
          m: m,
          l: MODELS[m].short + " (" + share + ")",
          v: fmtV(mv, false) + (mb > 0.001 ? " (+" + fmtV(mb, false) + ")" : "")
        });
      });

      var pc = svgClient(svg, W, H, xPos, Y(cumTotAt));
      showTip(tipNode(bucketLabel(b) + (b === 55 ? " · 지금" : ""), rows), pc[0], pc[1]);
    }

    hit.addEventListener("pointermove", function (ev) {
      var rect = svg.getBoundingClientRect();
      var px = (ev.clientX - rect.left) * W / rect.width;
      var frac = (px - L) / plotW;
      var b = Math.max(0, Math.min(55, Math.round(frac * 56 - 0.5)));
      showStrataSample(b);
    });
    hit.addEventListener("pointerleave", function () {
      cr.style.display = "none";
      sampleDots.forEach(function (c) { c.style.display = "none"; });
      hideTip();
    });

    clear(box);
    box.appendChild(svg);

    return {
      applyModelHi: function (k) {
        bands.forEach(function (b) {
          var m = Number(b.getAttribute("data-m"));
          b.classList.toggle("is-dim", k !== null && m !== k);
        });
        lines.forEach(function (l) {
          var m = Number(l.getAttribute("data-m"));
          l.classList.toggle("is-dim", k !== null && m !== k);
          l.classList.toggle("is-hi", k !== null && m === k);
        });
      }
    };
  }

  // ---------------------------------------------------------------- Group ② Renderer
  function buildGroup2(sc) {
    var root = el("div", "g2");
    var hoverModel = null;
    var activeChart = null;

    function effectiveModel() {
      return hoverModel !== null ? hoverModel : S.pinModel;
    }

    // Low-balance alert banner
    var ab = alertBanner(sc);
    if (ab) root.appendChild(ab);

    // 1. Shared Werk Group ② Header + Controls Card
    var topCard = el("div", "g2-top");
    var kpi = el("div", "g2-kpi");
    kpi.appendChild(badge("or", "OR"));
    kpi.appendChild(el("span", "g2-bal " + (sc.tone === "ok" ? "" : sc.tone), usd(sc.balance)));
    kpi.appendChild(el("span", "g2-delta", "7일 −" + usd(sc.weekly)));
    if (sc.topupAt) {
      kpi.appendChild(el("span", "pill sm ok", "충전 +" + usd(sc.topupAt.amt, 0)));
    }
    kpi.appendChild(el("span", "rw " + (sc.tone === "ok" ? "" : sc.tone), "~" + usd(sc.avg) + "/일 · " + runwayTxt(sc) + " 남음"));
    kpi.appendChild(el("span", "rsp"));
    kpi.appendChild(el("span", "pill sm", sc.limitRemaining == null ? "키 한도 없음" : "한도 " + usd(sc.limitRemaining)));
    topCard.appendChild(kpi);

    var ctrl = el("div", "g2-ctrl");
    var tabSeg = el("div", "tab-seg");
    tabSeg.setAttribute("role", "tablist");
    tabSeg.setAttribute("aria-label", "시각화 탭 전환");
    [["tab1", "탭 1 · 막대 + 소진 곡선"], ["tab2", "탭 2 · 모델별 누적 지층"]].forEach(function (t) {
      var b = pillBtn("tab-opt" + (S.tab === t[0] ? " is-on" : ""), t[1]);
      b.setAttribute("role", "tab");
      b.setAttribute("aria-selected", String(S.tab === t[0]));
      b.addEventListener("click", function () {
        if (S.tab === t[0]) return;
        S.tab = t[0];
        render();
      });
      tabSeg.appendChild(b);
    });
    ctrl.appendChild(tabSeg);

    var subCtrls = el("div", "sub-ctrls");
    var unitSeg = el("div", "axis-seg");
    unitSeg.setAttribute("role", "tablist");
    unitSeg.setAttribute("aria-label", "단위");
    [["cost", "비용($)"], ["tok", "토큰"]].forEach(function (u) {
      var b = pillBtn("as-opt" + (S.metric === u[0] ? " is-on" : ""), u[1]);
      b.setAttribute("role", "tab");
      b.setAttribute("aria-selected", String(S.metric === u[0]));
      b.addEventListener("click", function () {
        if (S.metric === u[0]) return;
        S.metric = u[0];
        render();
      });
      unitSeg.appendChild(b);
    });
    subCtrls.appendChild(unitSeg);

    if (S.tab === "tab1") {
      var axSeg = el("div", "axis-seg");
      axSeg.setAttribute("role", "tablist");
      axSeg.setAttribute("aria-label", "우측 Y축 기준");
      [["dyn", "동적 Y축"], ["zero", "$0 기준"]].forEach(function (a) {
        var b = pillBtn("as-opt" + (S.axis === a[0] ? " is-on" : ""), a[1]);
        b.setAttribute("role", "tab");
        b.setAttribute("aria-selected", String(S.axis === a[0]));
        b.addEventListener("click", function () {
          if (S.axis === a[0]) return;
          S.axis = a[0];
          render();
        });
        axSeg.appendChild(b);
      });
      subCtrls.appendChild(axSeg);
    }
    ctrl.appendChild(subCtrls);
    topCard.appendChild(ctrl);
    root.appendChild(topCard);

    // 2. Compact Balance Progress Strip + OpenAI SIWC Status Badge (Variant B .bstrip)
    var bstrip = el("div", "bstrip");
    var bb = el("div", "bbal");
    var bt = el("div", "bbal-t");
    bt.appendChild(el("b", sc.tone === "ok" ? "" : sc.tone, usd(sc.balance)));
    bt.appendChild(el("span", "", "/ " + usd(sc.total) + " 남음 · " + pct(sc.balance / sc.total, 1)));
    bb.appendChild(bt);
    var track = el("div", "btrack " + (sc.tone === "ok" ? "" : sc.tone));
    var fill = el("i");
    fill.style.width = Math.max(2, Math.min(100, sc.balance / sc.total * 100)) + "%";
    track.appendChild(fill);
    bb.appendChild(track);
    bstrip.appendChild(bb);
    bstrip.appendChild(oaiBadge(sc));
    root.appendChild(bstrip);

    // 3. Tab Page Content
    if (S.tab === "tab1") {
      var grid = el("div", "bgrid");
      var leftPanel = el("div", "cpanel");
      var lh = el("div", "cp-h");
      lh.appendChild(el("span", "cp-t", "일별 소모 + 3시간 잔고 곡선"));
      lh.appendChild(el("span", "cp-s", "56구간 · 최근 7일"));
      lh.appendChild(el("span", "rsp"));
      var keys = el("div", "keys");
      var k1 = el("span");
      var kb = el("i", "k-bar");
      kb.style.background = "var(--m0)";
      k1.appendChild(kb);
      k1.appendChild(doc.createTextNode("일 소모"));
      var k2 = el("span");
      var kl = el("i", "k-line");
      kl.style.background = curveColor(sc);
      k2.appendChild(kl);
      k2.appendChild(doc.createTextNode("잔고(3h)"));
      keys.appendChild(k1);
      keys.appendChild(k2);
      lh.appendChild(keys);
      leftPanel.appendChild(lh);
      var leftBody = el("div", "cp-body");
      leftPanel.appendChild(leftBody);

      var rightPanel = el("div", "cpanel");
      var rh = el("div", "cp-h");
      rh.appendChild(el("span", "cp-t", "모델 비중"));
      rh.appendChild(el("span", "cp-s", "클릭 고정"));
      rightPanel.appendChild(rh);

      var dwrap = el("div", "bdon");
      var legRows = [];
      var donut = new Donut(124, 20, {
        onEnter: function (i) { hoverModel = i; syncHighlight(); },
        onLeave: function () { hoverModel = null; syncHighlight(); hideTip(); },
        onClick: function (i) { S.pinModel = S.pinModel === i ? null : i; syncHighlight(); },
        onMove: function (i, e) {
          var v = S.metric === "cost" ? sc.mcost[i] : sc.mtok[i];
          var totV = S.metric === "cost" ? sc.weekly : sum(sc.mtok);
          showTip(tipNode(MODELS[i].name, [
            { m: i, l: S.metric === "cost" ? "비용" : "토큰", v: S.metric === "cost" ? usd(v) : tok(v) },
            { l: "7일 비중", v: pct(v / totV, 1) }
          ]), e.clientX, e.clientY);
        }
      });
      dwrap.appendChild(donut.svg);

      var lg = el("div", "lg");
      var vals = MODELS.map(function (_, i) { return S.metric === "cost" ? sc.mcost[i] : sc.mtok[i]; });
      var totVals = sum(vals);
      sc.order.forEach(function (i) {
        var r = pillBtn("lr");
        r.appendChild(dotNode(i));
        r.appendChild(el("span", "n", MODELS[i].short));
        r.appendChild(el("span", "v", S.metric === "cost" ? usd(vals[i]) : tok(vals[i])));
        r.appendChild(el("span", "p", pct(vals[i] / totVals)));
        r.addEventListener("pointerenter", function () { hoverModel = i; syncHighlight(); });
        r.addEventListener("pointerleave", function () { hoverModel = null; syncHighlight(); });
        r.addEventListener("click", function () { S.pinModel = S.pinModel === i ? null : i; syncHighlight(); });
        lg.appendChild(r);
        legRows.push({ i: i, el: r });
      });
      dwrap.appendChild(lg);
      rightPanel.appendChild(dwrap);

      grid.appendChild(leftPanel);
      grid.appendChild(rightPanel);
      root.appendChild(grid);

      function syncHighlight() {
        var k = effectiveModel();
        donut.setHi(k);
        if (k === null) {
          donut.center("7일 합계", S.metric === "cost" ? usd(sc.weekly) : tok(sum(sc.mtok)));
        } else {
          donut.center(MODELS[k].short.replace("Claude ", ""), S.metric === "cost" ? usd(vals[k]) : tok(vals[k]));
        }
        legRows.forEach(function (r) {
          r.el.classList.toggle("is-hi", k === r.i);
          r.el.classList.toggle("is-pin", S.pinModel === r.i);
          r.el.classList.toggle("is-dim", k !== null && k !== r.i);
        });
        if (activeChart) activeChart.applyModelHi(k);
      }

      pending.push(function () {
        donut.render(norm(vals));
        activeChart = compositeChart(leftBody, sc, { metric: S.metric, axis: S.axis });
        syncHighlight();
      });
    } else {
      // Tab 2: Full-width 0-based cumulative per-model strata chart + interactive model filter chips
      var panel = el("div", "cpanel");
      var ph = el("div", "cp-h");
      ph.appendChild(el("span", "cp-t", "모델별 누적 지층 (3시간 롤링 7일)"));
      ph.appendChild(el("span", "cp-s", "$0 기준 단조 누적 · 하단 칩으로 모델 격리"));
      panel.appendChild(ph);
      var pBody = el("div", "cp-body");
      panel.appendChild(pBody);

      var chipWrap = el("div", "fchips");
      var chips = [];
      var vals2 = MODELS.map(function (_, i) { return S.metric === "cost" ? sc.mcost[i] : sc.mtok[i]; });
      var tot2 = sum(vals2);

      sc.order.forEach(function (i) {
        var c = pillBtn("fchip");
        c.appendChild(dotNode(i));
        c.appendChild(el("span", "n", MODELS[i].short));
        c.appendChild(el("span", "v", S.metric === "cost" ? usd(vals2[i]) : tok(vals2[i])));
        c.appendChild(el("span", "pct", pct(vals2[i] / tot2)));
        c.title = MODELS[i].name + " · 클릭하여 단독 보기";
        c.addEventListener("pointerenter", function () { hoverModel = i; syncTab2(); });
        c.addEventListener("pointerleave", function () { hoverModel = null; syncTab2(); });
        c.addEventListener("click", function () { S.pinModel = S.pinModel === i ? null : i; syncTab2(); });
        chipWrap.appendChild(c);
        chips.push({ i: i, el: c });
      });
      panel.appendChild(chipWrap);
      root.appendChild(panel);

      function syncTab2() {
        var k = effectiveModel();
        chips.forEach(function (c) {
          c.el.classList.toggle("is-hi", k === c.i);
          c.el.classList.toggle("is-dim", k !== null && k !== c.i);
          c.el.setAttribute("aria-pressed", String(S.pinModel === c.i));
        });
        if (activeChart) activeChart.applyModelHi(k);
      }

      pending.push(function () {
        activeChart = strataChart(pBody, sc, { metric: S.metric });
        syncTab2();
      });
    }

    // 4. Bottom Footer with OpenRouter Key Management link
    var foot = el("div", "g2-foot");
    foot.appendChild(el("span", "", "최근 7일 (56 × 3h 샘플) · 우측 끝 ‘지금’ 기준"));
    foot.appendChild(el("span", "rsp"));
    var kb = pillBtn("ghost", "OpenRouter 키 관리 →");
    kb.title = "설정 → OpenRouter 키 (목업: 이동하지 않음)";
    foot.appendChild(kb);
    root.appendChild(foot);

    return root;
  }

  // ---------------------------------------------------------------- Popover Frame & Render
  function render() {
    hideTip();
    pending = [];
    doc.documentElement.setAttribute("data-theme", S.theme);
    var host = $("host");
    clear(host);

    var pop = el("div", "rpop usage-pop");
    pop.setAttribute("data-w", S.w);
    pop.setAttribute("role", "dialog");
    pop.setAttribute("aria-label", "플랜 사용량 · 계정");

    var hd = el("div", "usage-hd");
    hd.appendChild(el("span", "up-title", "플랜 사용량 · 계정"));
    var sy = el("span", "sync");
    sy.appendChild(el("i"));
    sy.appendChild(el("span", "", "방금 동기화"));
    hd.appendChild(sy);
    hd.appendChild(el("span", "rsp"));
    [["M20 12a8 8 0 1 1-2.6-5.9", "M20 4v5h-5"], ["M6 6l12 12M18 6 6 18"]].forEach(function (ps, idx) {
      var b = pillBtn("ibtn");
      b.setAttribute("aria-label", idx === 0 ? "새로고침" : "닫기");
      var s = sv("svg", { "class": "ic", viewBox: "0 0 24 24" });
      ps.forEach(function (d) { s.appendChild(sv("path", { d: d })); });
      b.appendChild(s);
      hd.appendChild(b);
    });
    pop.appendChild(hd);

    var scroll = el("div", "up-scroll");
    var sec = el("section", "grp g2sec");
    var gh = el("div", "grp-h");
    gh.appendChild(el("span", "grp-n", "②"));
    var gt = el("span", "grp-t", "내장 Werk 런타임 · API");
    gt.appendChild(el("small", "", "OpenRouter 잔고 · Werk OpenAI"));
    gh.appendChild(gt);
    sec.appendChild(gh);
    sec.appendChild(buildGroup2(SCN[S.scn]));
    scroll.appendChild(sec);
    pop.appendChild(scroll);

    host.appendChild(pop);
    pending.forEach(function (fn) { fn(); });
  }

  // ---------------------------------------------------------------- Toolbar Wiring
  function wireSeg(id, onSelect) {
    var root = $(id);
    root.addEventListener("click", function (e) {
      var b = e.target.closest(".m-opt");
      if (!b) return;
      Array.prototype.forEach.call(root.querySelectorAll(".m-opt"), function (x) {
        x.classList.toggle("is-on", x === b);
      });
      onSelect(b.getAttribute("data-v"));
    });
  }

  wireSeg("m-scn", function (v) { S.scn = v; render(); });
  wireSeg("m-theme", function (v) { S.theme = v; render(); });
  wireSeg("m-w", function (v) { S.w = v; render(); });

  var rzTimer = null;
  var lastW = 0;
  if (window.ResizeObserver) {
    new ResizeObserver(function (entries) {
      var w = Math.round(entries[0].contentRect.width);
      if (Math.abs(w - lastW) < 2) return;
      lastW = w;
      clearTimeout(rzTimer);
      rzTimer = setTimeout(render, 60);
    }).observe($("m-stage"));
  }

  render();
  window.__werkV3 = { state: S, render: render };
})();
