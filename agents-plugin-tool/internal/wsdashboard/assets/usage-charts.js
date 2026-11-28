    /* ---- Unified 2-chart renderer (Mode A: Progress % / Mode B: Timeline · Gantt) + pre-built hover previews ---- */
    var DEFS = [
      { k: "claude", short: "Cl", name: "Claude", harness: "claude", group: "" },
      { k: "codex", short: "Cx", name: "Codex", harness: "codex", group: "" },
      { k: "agyg", short: "Ag·G", name: "Gemini", harness: "agy", group: "gemini" },
      { k: "agyc", short: "Ag·C", name: "Claude / GPT", harness: "agy", group: "claude_gpt" }
    ];
    function hbadge(def, sm, textOverride) {
      var n = el("span", "up-hb" + (sm ? " sm" : ""), textOverride || def.short);
      n.setAttribute("data-k", def.k);
      return n;
    }
    function entryOf(def) {
      var entries = data.entries || [];
      for (var i = 0; i < entries.length; i++) {
        var e = entries[i];
        var hMatch = def.harness === "codex" ? (e.harness === "codex" || e.harness === "codex_openai") : (e.harness === def.harness);
        if (hMatch && (e.group || "") === def.group) return e;
      }
      return null;
    }
    function defForHarness(harness, group) {
      var norm = harness === "codex_openai" ? "codex" : harness;
      for (var i = 0; i < DEFS.length; i++) {
        if (DEFS[i].harness === norm && DEFS[i].group === (group || "")) return DEFS[i];
      }
      return null;
    }
    function slotsOf(harness) {
      var map = accountData && accountData.slots;
      return (map && Array.isArray(map[harness])) ? map[harness] : [];
    }
    function slotDisplayLabel(harness, slotId) {
      if (!slotId || slotId === "default") return t("defaultSlot");
      var list = slotsOf(harness);
      for (var i = 0; i < list.length; i++) {
        if (list[i].id === slotId) return list[i].label || list[i].id;
      }
      return slotId;
    }
    function entryHistory(entry, kind) {
      if (entry.harness === "agy") return data.agyHistory || data.history;
      if ((entry.harness === "codex" || entry.harness === "codex_openai") && kind === "five_hour") return data.codexFiveHourHistory || data.history;
      return data.history;
    }
    function shortResetText(w, now) {
      if (!w || !w.resetsAt) return t("unknown");
      if (w.kind === "five_hour") return remainText(w.resetsAt - now);
      return new Intl.DateTimeFormat(locale(), { weekday: "short", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(w.resetsAt));
    }
    function buildSeriesFromWindow(def, entry, w, history, now, extra) {
      var geom = trend(entry, w, history);
      var at = geom.at, old = stale(at, now), noRecent = unreported(at, now);
      var dur = DUR[w.kind] || 7 * 86400000;
      var elapsedNow = geom.knownReset ? Math.max(0, Math.min(1, (now - geom.start) / dur)) : null;
      var delta = noRecent || !geom.knownReset ? null : pace(w, now);
      var gapLimit = w.kind === "five_hour" ? 30 * 60000 : 12 * 3600000;
      var runs = [], cur = null;
      geom.segments.forEach(function (seg) {
        if (seg.b.asOf - seg.a.asOf > gapLimit) {
          cur = null; runs.push({ gap: true, pts: [seg.a, seg.b] });
        } else if (cur && cur.pts[cur.pts.length - 1] === seg.a) {
          cur.pts.push(seg.b);
        } else {
          cur = { gap: false, pts: [seg.a, seg.b] }; runs.push(cur);
        }
      });
      return {
        def: def, e: entry, w: w, kind: w.kind, dur: dur,
        knownReset: geom.knownReset, start: geom.start, reset: geom.reset,
        at: at, pct: w.percent, old: old, noRecent: noRecent, disabled: !!w.disabled,
        elapsedNow: elapsedNow, delta: delta, now: now,
        points: geom.points, runs: runs,
        slotId: extra && extra.slotId || "",
        slotLabel: extra && extra.slotLabel || "",
        isPreview: !!(extra && extra.isPreview)
      };
    }
    function activeSeries(def, kind, now) {
      var e = entryOf(def);
      if (!e || e.pending) return null;
      var wins = active(e, now);
      var w = wins.filter(function (v) { return v.kind === kind; })[0];
      if (!w) return null;
      return buildSeriesFromWindow(def, e, w, entryHistory(e, kind), now, null);
    }
    function previewRecord(harness, slotId) {
      var map = data.slotPreviews && data.slotPreviews[harness];
      return (map && map[slotId]) || null;
    }
    function previewSeries(def, slotId, kind, now) {
      var rec = previewRecord(def.harness, slotId);
      if (!rec || !Array.isArray(rec.windows)) return null;
      var w = rec.windows.filter(function (v) { return v.kind === kind && (!v.resetsAt || v.resetsAt > now); })[0];
      if (!w) return null;
      var fakeEntry = { harness: def.harness, group: def.group, asOf: rec.asOf || w.asOf, source: (rec.samples && rec.samples[0] && rec.samples[0].source) || "cache" };
      var hist = { schema_version: 1, status: "ok", samples: Array.isArray(rec.samples) ? rec.samples : [] };
      return buildSeriesFromWindow(def, fakeEntry, w, hist, now, {
        isPreview: true,
        slotId: slotId,
        slotLabel: slotDisplayLabel(def.harness, slotId)
      });
    }
    function inactiveSlotsList() {
      var out = [];
      DEFS.forEach(function (def) {
        if (def.group) return;
        slotsOf(def.harness).forEach(function (slot) {
          if (!slot.active) out.push({ def: def, harness: def.harness, slotId: slot.id, slotLabel: slot.id === "default" ? t("defaultSlot") : (slot.label || slot.id) });
        });
      });
      return out;
    }
    function pinWidth(text) { return Math.ceil(measureText(text, "800 9px ui-rounded, system-ui")) + 10; }
    function pinFillStyle(def) {
      return def.k === "codex" ? { bg: "var(--k-codex-bg)", fg: "var(--k-codex-fg)", stroke: "var(--k-codex-stroke)" }
        : { bg: "var(--k-" + def.k + ")", fg: "#ffffff", stroke: "none" };
    }
    function pinGroup(def, x, ly, w, text, aria, extraAttrs) {
      var st = pinFillStyle(def);
      var attrs = Object.assign({
        "class": "up-pin", "data-k": def.k, tabindex: "0", role: "button",
        "aria-label": aria, transform: "translate(" + x.toFixed(1) + "," + (ly - 7.5).toFixed(1) + ")"
      }, extraAttrs || {});
      var g = svgNode("g", attrs);
      g.appendChild(svgNode("rect", { "class": "up-pb", width: w, height: 15, rx: 4, fill: st.bg, stroke: st.stroke, "stroke-width": st.stroke === "none" ? 0 : 1 }));
      var txt = svgNode("text", { x: (w / 2).toFixed(1), y: "10.6", "text-anchor": "middle", fill: st.fg }, text);
      g.appendChild(txt);
      return g;
    }
    function renderSeriesGroup(s, map, clipPoly, fillBase, isPreview) {
      var attrs = isPreview
        ? { "class": "up-pser", "data-k": s.def.k, "data-preview-h": s.def.harness, "data-preview-slot": s.slotId }
        : { "class": "up-ser" + (swapHarness === s.def.harness ? " swapin" : "") + (s.disabled ? " is-disabled" : ""), "data-k": s.def.k };
      var g = svgNode("g", attrs);
      var col = "var(--k-" + s.def.k + ")";
      var cid = "usage-clip-" + (++chartSerial);
      if (!isPreview && clipPoly) {
        var defs = svgNode("defs"), cp = svgNode("clipPath", { id: cid });
        cp.appendChild(svgNode("polygon", { points: clipPoly }));
        defs.appendChild(cp); g.appendChild(defs);
      }
      if (s.knownReset) {
        s.runs.forEach(function (r) {
          var xy = r.pts.map(function (p) { return map(p.asOf, p.percent); });
          if (!r.gap) xy = decimateXY(xy);
          if (xy.length < 2) return;
          var last = xy[xy.length - 1], first = xy[0];
          if (!isPreview && !r.gap) {
            var base = pathD(xy) + "L" + last.x.toFixed(1) + " " + fillBase + "L" + first.x.toFixed(1) + " " + fillBase + "Z";
            g.appendChild(svgNode("path", { "class": "usage-area", d: base, fill: col }));
            if (clipPoly) g.appendChild(svgNode("path", { "class": "usage-excess", d: base, fill: "var(--wb-fail)", "clip-path": "url(#" + cid + ")" }));
          }
          var lineAttrs = { "class": "usage-actual", d: pathD(xy), stroke: col, "stroke-width": isPreview ? 1.8 : 1.9 };
          if (isPreview || r.gap) lineAttrs["stroke-dasharray"] = "4 3";
          var line = svgNode("path", lineAttrs);
          line.appendChild(svgNode("title", {}, t(r.gap ? "estimatedTitle" : "interval")));
          g.appendChild(line);
        });
      }
      var dotPos = s.knownReset ? map(s.at, s.pct) : map(null, s.pct);
      var dot = svgNode("circle", {
        "class": "usage-point usage-point-current",
        cx: dotPos.x.toFixed(1), cy: dotPos.y.toFixed(1), r: isPreview ? 3.8 : 4,
        fill: s.old && !isPreview ? "var(--wb-muted-fg)" : col
      });
      dot.appendChild(svgNode("title", {}, s.def.short + " " + Math.round(s.pct) + "%" + (s.old ? " (" + t("stale") + ")" : "")));
      g.appendChild(dot);
      return { g: g, dot: dotPos };
    }
    function pinAria(s) {
      return s.def.name + " " + Math.round(s.pct) + "%" + (s.delta !== null ? " (" + signedDelta(s.delta) + ")" : " (" + t("stale") + ")");
    }
    function buildCalloutNode(s, xPct, yPct, now, extraAttrs) {
      var cls = "up-callout" + (s.isPreview ? " up-pcallout" : "");
      if (!s.isPreview && xPct > 62) cls += " flip-x";
      if (yPct < 24) cls += " flip-y";
      var c = el("div", cls);
      c.hidden = true;
      Object.keys(extraAttrs || {}).forEach(function (k) { c.setAttribute(k, extraAttrs[k]); });
      c.style.left = s.isPreview ? "50%" : (Math.max(4, Math.min(96, xPct)).toFixed(1) + "%");
      c.style.top = Math.max(4, Math.min(92, yPct)).toFixed(1) + "%";
      c.appendChild(hbadge(s.def, true, s.isPreview ? (s.def.short + "·" + s.slotLabel) : s.def.short));
      c.appendChild(el("b", "", Math.round(s.pct) + "%"));
      if (s.delta !== null) c.appendChild(el("span", s.delta > 0 ? "over" : s.delta < 0 ? "under" : "dim", "(" + signedDelta(s.delta) + ")"));
      if (s.knownReset) c.appendChild(el("span", "dim", "\u21bb " + shortResetText(s.w, now)));
      if (s.isPreview) {
        c.appendChild(el("span", "up-pcache-tag", t("previewCachedTag", previewAgeText(s.at, now))));
      } else if (s.old) {
        c.appendChild(el("span", "up-warnc", t("staleMin", Math.max(1, Math.round((now - s.at) / 60000)))));
      }
      return c;
    }
    function buildNoCacheCallout(slotInfo) {
      var c = el("div", "up-callout up-pcallout up-nocache-callout");
      c.hidden = true;
      c.setAttribute("data-preview-h", slotInfo.harness);
      c.setAttribute("data-preview-slot", slotInfo.slotId);
      c.style.left = "50%";
      c.style.top = "18%";
      c.appendChild(hbadge(slotInfo.def, true, slotInfo.def.short + "·" + slotInfo.slotLabel));
      c.appendChild(el("span", "dim", t("previewNoCache")));
      return c;
    }

    // ---- Mode A: Normalized Progress % (x = window elapsed %, y = usage %) ----
    function drawModeA(kind, W, list, previewItems, now) {
      var H = 172, x0 = 30, x1 = W - 46, y0 = 14, y1 = H - 24;
      var X = function (e) { return x0 + Math.max(0, Math.min(1, e)) * (x1 - x0); };
      var Y = function (p) { return y1 - (p / 100) * (y1 - y0); };
      var hasKnown = list.some(function (s) { return s.knownReset; });
      var svg = svgNode("svg", {
        "class": "up-csvg usage-chart", viewBox: "0 0 " + W + " " + H, role: "img",
        "aria-label": t("chart", windowLabel(kind), t(hasKnown ? "baseline" : "unknownReset"))
      });
      if (hasKnown) {
        svg.appendChild(svgNode("polygon", { "class": "up-wash", points: x0 + "," + y1 + " " + x1 + "," + y0 + " " + x0 + "," + y0 }));
      }
      [0, 50, 100].forEach(function (p) {
        svg.appendChild(svgNode("line", { "class": "usage-grid", x1: x0, x2: x1, y1: Y(p), y2: Y(p) }));
        svg.appendChild(svgNode("text", { x: p === 0 ? 20 : p === 50 ? 14 : 4, y: Y(p) + 3 }, String(p)));
      });
      [0, 50, 100].forEach(function (p, i) {
        if (i) svg.appendChild(svgNode("line", { "class": "usage-day-grid", x1: X(p / 100), x2: X(p / 100), y1: y0, y2: y1 }));
        svg.appendChild(svgNode("text", { x: X(p / 100), y: H - 9, "text-anchor": i === 0 ? "start" : i === 2 ? "end" : "middle" }, p + "%"));
      });
      if (hasKnown) {
        var ideal = svgNode("path", { "class": "usage-ideal", d: "M" + x0 + " " + y1 + "L" + x1 + " " + y0, "stroke-dasharray": "4 4" });
        ideal.appendChild(svgNode("title", {}, t("idealTitle")));
        svg.appendChild(ideal);
      }
      var poly = hasKnown ? (x0 + "," + y1 + " " + x1 + "," + y0 + " " + x1 + ",0 " + x0 + ",0") : "";
      var dots = {}, pins = [], callouts = [];
      list.forEach(function (s) {
        var map = function (ts, p) {
          if (ts === null || !s.knownReset) return { x: (x0 + x1) / 2, y: Y(p) };
          return { x: X((ts - s.start) / s.dur), y: Y(p) };
        };
        var r = renderSeriesGroup(s, map, s.knownReset ? poly : "", y1, false);
        svg.appendChild(r.g);
        dots[s.def.k] = { x: r.dot.x, y: r.dot.y, s: s };
        var pw = pinWidth(s.def.short);
        var px = r.dot.x + 8;
        if (px + pw > W - 2) px = Math.max(x0 + 2, r.dot.x - pw - 8);
        pins.push({ s: s, x: px, y: r.dot.y, w: pw, leftSide: px < r.dot.x });
        callouts.push(buildCalloutNode(s, r.dot.x / W * 100, r.dot.y / H * 100, now, { "data-callout-k": s.def.k }));
      });
      separatePins(pins, y0 - 3, y1 + 2, 16);
      pins.forEach(function (p) {
        var d = dots[p.s.def.k];
        var lx1 = p.leftSide ? d.x - 4 : d.x + 4, lx2 = p.leftSide ? p.x + p.w : p.x;
        svg.appendChild(svgNode("line", { "class": "up-leader", "data-k": p.s.def.k, x1: lx1.toFixed(1), y1: d.y.toFixed(1), x2: lx2.toFixed(1), y2: p.ly.toFixed(1) }));
        svg.appendChild(pinGroup(p.s.def, p.x, p.ly, p.w, p.s.def.short, pinAria(p.s)));
      });
      // Pre-built inactive slot hover previews (hidden until pill hover/focus).
      previewItems.forEach(function (item) {
        var ps = item.series;
        if (!ps) {
          callouts.push(buildNoCacheCallout(item.slotInfo));
          return;
        }
        var pmap = function (ts, p) {
          if (ts === null || !ps.knownReset) return { x: (x0 + x1) / 2, y: Y(p) };
          return { x: X((ts - ps.start) / ps.dur), y: Y(p) };
        };
        var pr = renderSeriesGroup(ps, pmap, "", y1, true);
        svg.appendChild(pr.g);
        var pText = ps.def.short + "·" + ps.slotLabel + " · " + previewAgeText(ps.at, now);
        var pw = pinWidth(pText);
        var px = pr.dot.x + 8;
        if (px + pw > W - 2) px = Math.max(x0 + 2, pr.dot.x - pw - 8);
        var leftSide = px < pr.dot.x;
        var pinWrap = svgNode("g", { "class": "up-ppin", "data-preview-h": ps.def.harness, "data-preview-slot": ps.slotId });
        pinWrap.appendChild(svgNode("line", {
          "class": "up-leader up-pleader", "stroke-dasharray": "3 2",
          x1: (leftSide ? pr.dot.x - 4 : pr.dot.x + 4).toFixed(1), y1: pr.dot.y.toFixed(1),
          x2: (leftSide ? px + pw : px).toFixed(1), y2: pr.dot.y.toFixed(1)
        }));
        pinWrap.appendChild(pinGroup(ps.def, px, Math.max(y0, Math.min(y1, pr.dot.y)), pw, pText, pText, { tabindex: "-1" }));
        svg.appendChild(pinWrap);
        callouts.push(buildCalloutNode(ps, pr.dot.x / W * 100, pr.dot.y / H * 100, now, {
          "data-preview-h": ps.def.harness, "data-preview-slot": ps.slotId
        }));
      });
      return { svg: svg, callouts: callouts };
    }

    // ---- Mode B: Shared Wall-Clock Axis + Now Line + Staggered Gantt Lanes ----
    function drawModeB(kind, W, list, previewItems, activeDefs, now) {
      var laneCount = Math.max(1, activeDefs.length);
      var x0 = 40, x1 = W - 10, yt = 16, yb = 122, ly0 = 138, lh = 13, ls = 17;
      var yEnd = ly0 + laneCount * ls - 4, H = yEnd + 22;
      var knownList = list.filter(function (s) { return s.knownReset; });
      // Exact measured bounds for 5H and 7D (no artificial padding beyond window start/reset).
      var D0 = knownList.length ? Math.min.apply(null, knownList.map(function (s) { return s.start; })) : (now - DUR[kind]);
      var D1 = knownList.length ? Math.max.apply(null, knownList.map(function (s) { return s.reset; })) : now;
      if (D1 <= D0) D1 = D0 + DUR[kind];
      var X = function (ts) { return x0 + ((ts - D0) / (D1 - D0)) * (x1 - x0); };
      var Y = function (p) { return yb - (p / 100) * (yb - yt); };
      var nowInRange = now >= D0 && now <= D1;
      var nowX = Math.max(x0, Math.min(x1, X(now)));
      var svg = svgNode("svg", {
        "class": "up-csvg usage-chart", viewBox: "0 0 " + W + " " + H, role: "img",
        "aria-label": t("chart", windowLabel(kind), t("subTimeline"))
      });
      if (nowInRange && x1 > nowX) {
        svg.appendChild(svgNode("rect", { "class": "up-fut", x: nowX.toFixed(1), y: yt - 6, width: Math.max(0, x1 - nowX).toFixed(1), height: yEnd - yt + 6 }));
      }
      // Time axis ticks & date labels
      var timeFmt = { hour: "2-digit", minute: "2-digit", hour12: false };
      if (kind === "seven_day") {
        var mid = new Date(D0), right = -Infinity, wdFmt = new Intl.DateTimeFormat(locale(), { weekday: "short" });
        mid.setHours(0, 0, 0, 0);
        while (mid.getTime() < D1) {
          var day = mid.getTime();
          mid.setDate(mid.getDate() + 1);
          var nx = mid.getTime();
          if (day > D0) svg.appendChild(svgNode("line", { "class": "usage-day-grid", x1: X(day).toFixed(1), x2: X(day).toFixed(1), y1: yt, y2: yEnd }));
          var dObj = new Date(day), c = X(day + (nx - day) / 2);
          var dateTxt = (dObj.getMonth() + 1) + "/" + dObj.getDate() + " " + wdFmt.format(dObj);
          var tw = measureText(dateTxt, "9px system-ui");
          if (c - tw / 2 >= x0 && c + tw / 2 <= x1 && c - tw / 2 >= right + 4) {
            svg.appendChild(svgNode("text", { "class": "usage-day-label", x: c.toFixed(1), y: H - 6, "text-anchor": "middle" }, dateTxt));
            right = c + tw / 2;
          }
        }
      } else {
        var spanH = Math.max(0.5, (D1 - D0) / 3600000);
        var pxPerH = (x1 - x0) / spanH;
        var stepH = Math.max(1, Math.ceil(38 / Math.max(1, pxPerH)));
        // Bound labels at exact measured D0 and D1
        var d0Date = new Date(D0), d1Date = new Date(D1);
        var d0Lbl = d0Date.toLocaleTimeString(locale(), timeFmt);
        var d1Lbl = d1Date.toLocaleTimeString(locale(), timeFmt);
        var d0Right = x0 + measureText(d0Lbl, "9px system-ui") + 4;
        var d1Left = x1 - measureText(d1Lbl, "9px system-ui") - 4;
        svg.appendChild(svgNode("text", { "class": "up-bound-label", x: x0, y: H - 6, "text-anchor": "start" }, d0Lbl));
        svg.appendChild(svgNode("text", { "class": "up-bound-label", x: x1, y: H - 6, "text-anchor": "end" }, d1Lbl));
        var curH = new Date(D0); curH.setMinutes(0, 0, 0);
        var firstTick = curH.getTime() <= D0 ? curH.getTime() + 3600000 : curH.getTime();
        var lastLblRight = d0Right;
        for (var tt = firstTick; tt < D1; tt += 3600000) {
          var dt = new Date(tt), isMidnight = dt.getHours() === 0;
          if (!isMidnight && dt.getHours() % stepH !== 0) continue;
          var tx = X(tt);
          svg.appendChild(svgNode("line", { "class": isMidnight ? "usage-day-grid up-mid-grid" : "usage-day-grid", x1: tx.toFixed(1), x2: tx.toFixed(1), y1: yt, y2: yEnd }));
          var lbl = isMidnight ? ((dt.getMonth() + 1) + "/" + dt.getDate()) : dt.toLocaleTimeString(locale(), timeFmt);
          var lw = measureText(lbl, "9px system-ui");
          if (tx - lw / 2 >= lastLblRight + 3 && tx + lw / 2 <= d1Left - 3) {
            svg.appendChild(svgNode("text", { "class": isMidnight ? "up-mid-label" : "", x: tx.toFixed(1), y: H - 6, "text-anchor": "middle" }, lbl));
            lastLblRight = tx + lw / 2;
          }
        }
      }
      [0, 50, 100].forEach(function (p) {
        svg.appendChild(svgNode("line", { "class": "usage-grid", x1: x0, x2: x1, y1: Y(p), y2: Y(p) }));
        svg.appendChild(svgNode("text", { x: p === 0 ? 30 : p === 50 ? 24 : 14, y: Y(p) + 3 }, String(p)));
      });
      // Curves (upper band) + per-series focus ideal diagonal
      var pins = [], dots = {}, callouts = [];
      list.forEach(function (s) {
        var map = function (ts, p) {
          if (ts === null || !s.knownReset) return { x: nowX, y: Y(p) };
          return { x: X(ts), y: Y(p) };
        };
        var poly = s.knownReset ? (X(s.start) + "," + Y(0) + " " + X(s.reset) + "," + Y(100) + " " + X(s.reset) + ",0 " + X(s.start) + ",0") : "";
        var r = renderSeriesGroup(s, map, poly, yb, false);
        if (s.knownReset) {
          var ideal = svgNode("path", {
            "class": "up-fxd", d: "M" + X(s.start).toFixed(1) + " " + Y(0) + "L" + X(s.reset).toFixed(1) + " " + Y(100),
            stroke: "var(--k-" + s.def.k + ")", "stroke-width": 1.3, "stroke-dasharray": "4 4", fill: "none"
          });
          ideal.appendChild(svgNode("title", {}, s.def.short + " " + t("ideal")));
          r.g.appendChild(ideal);
        }
        svg.appendChild(r.g);
        dots[s.def.k] = { x: r.dot.x, y: r.dot.y, s: s };
        // Anchor pin at X(s.at) (r.dot.x), NOT nowX, so stale observations remain truthful.
        var pw = pinWidth(s.def.short);
        var px = r.dot.x + 8;
        if (px + pw > W - 2) px = Math.max(x0 + 2, r.dot.x - pw - 8);
        pins.push({ s: s, x: px, y: r.dot.y, w: pw, leftSide: px < r.dot.x });
        callouts.push(buildCalloutNode(s, r.dot.x / W * 100, r.dot.y / H * 100, now, { "data-callout-k": s.def.k }));
      });
      separatePins(pins, yt - 3, yb + 2, 16);
      // Staggered Gantt lanes
      var laneYByK = {};
      activeDefs.forEach(function (def, idx) {
        var s = list.filter(function (q) { return q.def === def; })[0];
        var y = ly0 + idx * ls;
        laneYByK[def.k] = y;
        var g = svgNode("g", { "class": "up-gl" + (s && swapHarness === def.harness ? " swapin" : ""), "data-k": def.k });
        var st = pinFillStyle(def), pw = pinWidth(def.short);
        var lab = svgNode("g", { transform: "translate(2," + y + ")" });
        lab.appendChild(svgNode("rect", { width: pw, height: lh, rx: 4, fill: st.bg, stroke: st.stroke, "stroke-width": st.stroke === "none" ? 0 : 1, opacity: s ? 1 : 0.35 }));
        lab.appendChild(svgNode("text", { x: (pw / 2).toFixed(1), y: "9.6", "text-anchor": "middle", fill: st.fg, "class": "up-gl-tag" }, def.short));
        g.appendChild(lab);
        if (!s || !s.knownReset) {
          var laneEnt = entryOf(def);
          var laneMsg = (laneEnt && laneEnt.status === "sign-in required") ? (t("signInRequired") + " — " + t("noLane")) : t("noLane");
          g.appendChild(svgNode("text", { "class": "up-lane-msg", x: x0 + 4, y: y + 10 }, laneMsg));
          svg.appendChild(g);
          return;
        }
        var xs = X(s.start), xe = X(s.reset), cid = "usage-lane-" + (++chartSerial);
        var defsEl = svgNode("defs"), cp = svgNode("clipPath", { id: cid });
        cp.appendChild(svgNode("rect", { x: xs.toFixed(1), y: y, width: Math.max(1, xe - xs).toFixed(1), height: lh, rx: lh / 2 }));
        defsEl.appendChild(cp); g.appendChild(defsEl);
        g.appendChild(svgNode("rect", { "class": "up-trk", x: xs.toFixed(1), y: y, width: Math.max(1, xe - xs).toFixed(1), height: lh, rx: lh / 2, fill: "color-mix(in srgb, var(--k-" + def.k + ") 18%, transparent)" }));
        var fe = xs + (s.pct / 100) * (xe - xs), fg = svgNode("g", { "class": "up-fillg", "clip-path": "url(#" + cid + ")" });
        var baseW = Math.max(0, Math.min(fe, nowX) - xs);
        fg.appendChild(svgNode("rect", { x: xs.toFixed(1), y: y, width: baseW.toFixed(1), height: lh, fill: "var(--k-" + def.k + ")" }));
        if (fe > nowX) fg.appendChild(svgNode("rect", { x: nowX.toFixed(1), y: y, width: Math.max(0, fe - nowX).toFixed(1), height: lh, fill: "var(--wb-fail)", opacity: "0.92" }));
        g.appendChild(fg);
        var txt = "\u21bb " + shortResetText(s.w, now), tw = measureText(txt, "700 9px system-ui");
        if (xe + 5 + tw <= W - 1) g.appendChild(svgNode("text", { "class": "up-lbl out", x: (xe + 5).toFixed(1), y: (y + 9.6).toFixed(1) }, txt));
        else g.appendChild(svgNode("text", { "class": "up-lbl", x: (xe - 6).toFixed(1), y: (y + 9.6).toFixed(1), "text-anchor": "end" }, txt));
        g.appendChild(svgNode("title", {}, def.name + ": " + Math.round(s.pct) + "% · " + t("resets", new Date(s.reset).toLocaleString(locale()))));
        svg.appendChild(g);
      });
      // Now line
      if (nowInRange) {
        var guide = svgNode("line", { "class": "usage-now", x1: nowX.toFixed(1), x2: nowX.toFixed(1), y1: yt - 4, y2: yEnd + 2, "stroke-dasharray": "2 3" });
        guide.appendChild(svgNode("title", {}, t("nowTitle")));
        svg.appendChild(guide);
        svg.appendChild(svgNode("text", { "class": "usage-now-label", x: nowX.toFixed(1), y: yt - 6, "text-anchor": "middle" }, t("now")));
      }
      // Active pins
      pins.forEach(function (p) {
        var d = dots[p.s.def.k];
        var lx1 = p.leftSide ? d.x - 4 : d.x + 4, lx2 = p.leftSide ? p.x + p.w : p.x;
        svg.appendChild(svgNode("line", { "class": "up-leader", "data-k": p.s.def.k, x1: lx1.toFixed(1), y1: d.y.toFixed(1), x2: lx2.toFixed(1), y2: p.ly.toFixed(1) }));
        svg.appendChild(pinGroup(p.s.def, p.x, p.ly, p.w, p.s.def.short, pinAria(p.s)));
      });
      // Pre-built inactive slot hover previews (curve + aged pin + Gantt overlay bar)
      previewItems.forEach(function (item) {
        var ps = item.series;
        if (!ps) {
          callouts.push(buildNoCacheCallout(item.slotInfo));
          return;
        }
        var pmap = function (ts, p) {
          if (ts === null || !ps.knownReset) return { x: nowX, y: Y(p) };
          return { x: Math.max(x0, Math.min(x1, X(ts))), y: Y(p) };
        };
        var pr = renderSeriesGroup(ps, pmap, "", yb, true);
        svg.appendChild(pr.g);
        var pText = ps.def.short + "·" + ps.slotLabel + " · " + previewAgeText(ps.at, now);
        var pw = pinWidth(pText);
        var px = pr.dot.x + 8;
        if (px + pw > W - 2) px = Math.max(x0 + 2, pr.dot.x - pw - 8);
        var leftSide = px < pr.dot.x;
        var pinWrap = svgNode("g", { "class": "up-ppin", "data-preview-h": ps.def.harness, "data-preview-slot": ps.slotId });
        pinWrap.appendChild(svgNode("line", {
          "class": "up-leader up-pleader", "stroke-dasharray": "3 2",
          x1: (leftSide ? pr.dot.x - 4 : pr.dot.x + 4).toFixed(1), y1: pr.dot.y.toFixed(1),
          x2: (leftSide ? px + pw : px).toFixed(1), y2: pr.dot.y.toFixed(1)
        }));
        pinWrap.appendChild(pinGroup(ps.def, px, Math.max(yt, Math.min(yb, pr.dot.y)), pw, pText, pText, { tabindex: "-1" }));
        svg.appendChild(pinWrap);
        if (ps.knownReset && laneYByK[ps.def.k] !== undefined) {
          var py = laneYByK[ps.def.k];
          var pxs = Math.max(x0, Math.min(x1, X(ps.start))), pxe = Math.max(pxs + 4, Math.min(x1, X(ps.reset)));
          var pgl = svgNode("g", { "class": "up-pgl", "data-preview-h": ps.def.harness, "data-preview-slot": ps.slotId });
          pgl.appendChild(svgNode("rect", {
            x: pxs.toFixed(1), y: py, width: (pxe - pxs).toFixed(1), height: lh, rx: lh / 2,
            fill: "color-mix(in srgb, var(--k-" + ps.def.k + ") 22%, transparent)",
            stroke: "var(--k-" + ps.def.k + ")", "stroke-width": 1.2, "stroke-dasharray": "3 2"
          }));
          var pFillW = Math.max(0, (ps.pct / 100) * (pxe - pxs));
          pgl.appendChild(svgNode("rect", {
            x: pxs.toFixed(1), y: py, width: pFillW.toFixed(1), height: lh, rx: lh / 2,
            fill: "var(--k-" + ps.def.k + ")", opacity: "0.75"
          }));
          svg.appendChild(pgl);
        }
        callouts.push(buildCalloutNode(ps, pr.dot.x / W * 100, pr.dot.y / H * 100, now, {
          "data-preview-h": ps.def.harness, "data-preview-slot": ps.slotId
        }));
      });
      return { svg: svg, callouts: callouts };
    }

    function activeGroupDefs() {
      var list = DEFS.filter(function (def) {
        if (entryOf(def)) return true;
        if (!def.group && slotsOf(def.harness).length > 0) return true;
        return false;
      });
      return list;
    }
    function drawCharts(now) {
      if (!popover) return;
      if (!popover.hidden && popover.clientWidth > 0) lastChartW = popover.clientWidth;
      var box = popover.querySelector(".up-charts");
      if (!box) return;
      box.textContent = "";
      var activeDefs = activeGroupDefs();
      if (!activeDefs.length) return;
      var grid = el("div", "up-cgrid");
      box.appendChild(grid);
      var inactiveSlots = inactiveSlotsList();
      [["five_hour", "5H"], ["seven_day", "7D"]].forEach(function (cfg) {
        var kind = cfg[0], shortKind = cfg[1];
        var panel = el("div", "up-cpanel");
        panel.setAttribute("data-kind", kind);
        var head = el("div", "up-cp-h");
        head.appendChild(el("span", "up-cp-t", windowLabel(kind) + " (" + shortKind + ")"));
        head.appendChild(el("span", "up-cp-s", t(mode === "progress" ? "subProgress" : "subTimeline")));
        panel.appendChild(head);
        var body = el("div", "up-cp-body");
        panel.appendChild(body);
        grid.appendChild(panel);
        var W = (body.clientWidth && body.clientWidth > 120) ? Math.round(body.clientWidth) : 300;
        var list = activeDefs.map(function (d) { return activeSeries(d, kind, now); }).filter(Boolean);
        var previewItems = inactiveSlots.map(function (slotInfo) {
          return { slotInfo: slotInfo, series: previewSeries(slotInfo.def, slotInfo.slotId, kind, now) };
        });
        if (!list.length && !previewItems.some(function (p) { return !!p.series; })) {
          body.appendChild(el("div", "up-pstatus", t("noHarnesses")));
        } else {
          var res = mode === "progress"
            ? drawModeA(kind, W, list, previewItems, now)
            : drawModeB(kind, W, list, previewItems, activeDefs, now);
          body.appendChild(res.svg);
          res.callouts.forEach(function (c) { body.appendChild(c); });
          var missing = activeDefs.filter(function (d) { return !list.some(function (s) { return s.def === d; }); });
          if (missing.length) {
            panel.appendChild(el("div", "up-cp-note", t("noCurve", missing.map(function (d) { return d.short; }).join(" · "))));
          }
        }
      });
    }
    // Zero-re-render visual state: toggles ONLY CSS classes and hidden attributes on pre-built DOM/SVG nodes.
    function applyVisualState() {
      if (!popover) return;
      var pH = previewState && previewState.harness || null;
      var pS = previewState && previewState.slotId || null;
      var pDef = pH ? defForHarness(pH, "") : null;
      var pK = pDef ? pDef.k : null;
      var f = pK ? null : focusKey;

      queryAll(popover, "up-ser").forEach(function (n) {
        var k = getAttr(n, "data-k");
        toggleCls(n, "is-focus", !!f && k === f);
        toggleCls(n, "is-preview-base", !!pK && k === pK);
        toggleCls(n, "is-dim", (!!f && k !== f) || (!!pK && k !== pK));
      });
      queryAll(popover, "up-pin").forEach(function (n) {
        if (getAttr(n, "tabindex") === "-1") return; // skip preview pins
        var k = getAttr(n, "data-k");
        toggleCls(n, "is-focus", !!f && k === f);
        toggleCls(n, "is-preview-base", !!pK && k === pK);
        toggleCls(n, "is-dim", (!!f && k !== f) || (!!pK && k !== pK));
      });
      queryAll(popover, "up-leader").forEach(function (n) {
        if (hasCls(n, "up-pleader")) return;
        var k = getAttr(n, "data-k");
        toggleCls(n, "is-dim", (!!f && k !== f) || (!!pK && k !== pK));
        toggleCls(n, "is-preview-base", !!pK && k === pK);
      });
      queryAll(popover, "up-gl").forEach(function (n) {
        var k = getAttr(n, "data-k");
        toggleCls(n, "is-focus", !!f && k === f);
        toggleCls(n, "is-preview-base", !!pK && k === pK);
        toggleCls(n, "is-dim", (!!f && k !== f) || (!!pK && k !== pK));
      });
      queryAll(popover, "up-hr").forEach(function (n) {
        var k = getAttr(n, "data-k");
        var isPrevRow = !!pK && k === pK;
        toggleCls(n, "is-focus", (!!f && k === f) || isPrevRow);
        toggleCls(n, "is-dim", (!!f && k !== f) || (!!pK && k !== pK));
        toggleCls(n, "is-previewing", isPrevRow);
        queryAll(n, "up-nc-live").forEach(function (cell) { cell.hidden = isPrevRow; });
        queryAll(n, "up-hr-status-live").forEach(function (st) { st.hidden = isPrevRow; });
        queryAll(n, "up-nc-prev").forEach(function (cell) {
          cell.hidden = !(isPrevRow && getAttr(cell, "data-preview-slot") === pS);
        });
        queryAll(n, "up-hr-nocache").forEach(function (st) {
          st.hidden = !(isPrevRow && getAttr(st, "data-preview-slot") === pS);
        });
      });
      queryAll(popover, "up-pser").forEach(function (n) {
        var on = !!pH && getAttr(n, "data-preview-h") === pH && getAttr(n, "data-preview-slot") === pS;
        toggleCls(n, "is-preview-on", on);
      });
      queryAll(popover, "up-ppin").forEach(function (n) {
        var on = !!pH && getAttr(n, "data-preview-h") === pH && getAttr(n, "data-preview-slot") === pS;
        toggleCls(n, "is-preview-on", on);
      });
      queryAll(popover, "up-pgl").forEach(function (n) {
        var on = !!pH && getAttr(n, "data-preview-h") === pH && getAttr(n, "data-preview-slot") === pS;
        toggleCls(n, "is-preview-on", on);
      });
      queryAll(popover, "up-callout").forEach(function (n) {
        var pHarness = getAttr(n, "data-preview-h");
        if (pHarness) {
          n.hidden = !(!!pH && pHarness === pH && getAttr(n, "data-preview-slot") === pS);
        } else {
          n.hidden = !(!!f && getAttr(n, "data-callout-k") === f);
        }
      });
    }
    function setFocus(k, lock) {
      focusKey = k;
      if (lock !== undefined) focusLocked = !!lock;
      applyVisualState();
    }
    function unlockFocus() {
      if (!focusLocked && !focusKey) return false;
      focusLocked = false;
      focusKey = null;
      applyVisualState();
      return true;
    }
    function toggleLock(k) {
      if (focusLocked && focusKey === k) { focusLocked = false; focusKey = null; }
      else { focusKey = k; focusLocked = true; }
      applyVisualState();
    }
    function setPreview(harness, slotId) {
      var nextH = harness || null, nextS = slotId || null;
      var curH = previewState && previewState.harness || null;
      var curS = previewState && previewState.slotId || null;
      if (nextH === curH && nextS === curS) return;
      previewState = nextH && nextS ? { harness: nextH, slotId: nextS } : null;
      applyVisualState();
      place();
    }
