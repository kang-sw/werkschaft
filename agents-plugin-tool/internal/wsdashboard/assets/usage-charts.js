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
    function layerSvg(W, H, cls, extraAttrs) {
      var attrs = Object.assign({ "class": "up-csvg " + cls, viewBox: "0 0 " + W + " " + H }, extraAttrs || {});
      return svgNode("svg", attrs);
    }
    function computeSeriesRuns(s, map) {
      var runs = [];
      if (s.knownReset) {
        s.runs.forEach(function (r) {
          var xy = r.pts.map(function (p) { return map(p.asOf, p.percent); });
          if (!r.gap) xy = decimateXY(xy);
          if (xy.length >= 2) runs.push({ gap: r.gap, xy: xy });
        });
      }
      var dotPos = s.knownReset ? map(s.at, s.pct) : map(null, s.pct);
      return { runs: runs, dot: dotPos };
    }
    function appendCurveAndDot(parent, s, geom, isPreview) {
      var col = "var(--k-" + s.def.k + ")";
      geom.runs.forEach(function (r) {
        var lineAttrs = { "class": "usage-actual", d: pathD(r.xy), stroke: col, "stroke-width": isPreview ? 1.8 : 1.9 };
        if (isPreview || r.gap) lineAttrs["stroke-dasharray"] = "4 3";
        var line = svgNode("path", lineAttrs);
        line.appendChild(svgNode("title", {}, t(r.gap ? "estimatedTitle" : "interval")));
        parent.appendChild(line);
      });
      var dot = svgNode("circle", {
        "class": "usage-point usage-point-current",
        cx: geom.dot.x.toFixed(1), cy: geom.dot.y.toFixed(1), r: isPreview ? 3.8 : 4,
        fill: s.old && !isPreview ? "var(--wb-muted-fg)" : col
      });
      dot.appendChild(svgNode("title", {}, s.def.short + " " + Math.round(s.pct) + "%" + (s.old ? " (" + t("stale") + ")" : "")));
      parent.appendChild(dot);
    }
    function appendFocusVisuals(focusSvg, s, geom, clipPoly, fillBase, pin, ganttFocus) {
      var col = "var(--k-" + s.def.k + ")";
      var cid = "usage-clip-" + (++chartSerial);
      if (clipPoly && geom.runs.some(function (r) { return !r.gap; })) {
        var defs = svgNode("defs"), cp = svgNode("clipPath", { id: cid });
        cp.appendChild(svgNode("polygon", { points: clipPoly }));
        defs.appendChild(cp); focusSvg.appendChild(defs);
      }
      geom.runs.forEach(function (r) {
        var d = pathD(r.xy);
        if (!r.gap) {
          var last = r.xy[r.xy.length - 1], first = r.xy[0];
          var base = d + "L" + last.x.toFixed(1) + " " + fillBase + "L" + first.x.toFixed(1) + " " + fillBase + "Z";
          focusSvg.appendChild(svgNode("path", { "class": "usage-area", d: base, fill: col }));
          if (clipPoly) focusSvg.appendChild(svgNode("path", { "class": "usage-excess", d: base, fill: "var(--wb-fail)", "clip-path": "url(#" + cid + ")" }));
        }
        var fLineAttrs = { "class": "up-focus-actual", d: d, stroke: col, "stroke-width": 2.5 };
        if (r.gap) fLineAttrs["stroke-dasharray"] = "4 3";
        focusSvg.appendChild(svgNode("path", fLineAttrs));
      });
      if (ganttFocus && ganttFocus.idealD) {
        var ideal = svgNode("path", {
          "class": "up-fxd", d: ganttFocus.idealD,
          stroke: col, "stroke-width": 1.3, "stroke-dasharray": "4 4", fill: "none"
        });
        ideal.appendChild(svgNode("title", {}, s.def.short + " " + t("ideal")));
        focusSvg.appendChild(ideal);
      }
      if (ganttFocus && ganttFocus.trk) {
        focusSvg.appendChild(svgNode("rect", {
          "class": "up-focus-trk",
          x: ganttFocus.trk.x, y: ganttFocus.trk.y, width: ganttFocus.trk.w, height: ganttFocus.trk.h, rx: ganttFocus.trk.rx,
          fill: "none", stroke: "var(--wb-fg)", "stroke-width": 1, "stroke-opacity": "0.5"
        }));
      }
      if (pin) {
        focusSvg.appendChild(svgNode("rect", {
          "class": "up-focus-pb",
          x: (pin.x - 0.6).toFixed(1), y: (pin.ly - 8.1).toFixed(1), width: (pin.w + 1.2).toFixed(1), height: "16.2", rx: "4.5",
          fill: "none", stroke: "var(--wb-fg)", "stroke-width": "1.2"
        }));
      }
    }
    function buildGanttGroup(def, s, y, lh, x0, x1, W, X, nowX, now) {
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
        return { g: g, trk: null };
      }
      var xs = X(s.start), xe = X(s.reset), cid = "usage-lane-" + (++chartSerial), trkW = Math.max(1, xe - xs);
      var defsEl = svgNode("defs"), cp = svgNode("clipPath", { id: cid });
      cp.appendChild(svgNode("rect", { x: xs.toFixed(1), y: y, width: trkW.toFixed(1), height: lh, rx: lh / 2 }));
      defsEl.appendChild(cp); g.appendChild(defsEl);
      g.appendChild(svgNode("rect", { "class": "up-trk", x: xs.toFixed(1), y: y, width: trkW.toFixed(1), height: lh, rx: lh / 2, fill: "color-mix(in srgb, var(--k-" + def.k + ") 18%, transparent)" }));
      var fe = xs + (s.pct / 100) * (xe - xs), fg = svgNode("g", { "class": "up-fillg", "clip-path": "url(#" + cid + ")" });
      var baseW = Math.max(0, Math.min(fe, nowX) - xs);
      fg.appendChild(svgNode("rect", { x: xs.toFixed(1), y: y, width: baseW.toFixed(1), height: lh, fill: "var(--k-" + def.k + ")" }));
      if (fe > nowX) fg.appendChild(svgNode("rect", { x: nowX.toFixed(1), y: y, width: Math.max(0, fe - nowX).toFixed(1), height: lh, fill: "var(--wb-fail)", opacity: "0.92" }));
      g.appendChild(fg);
      var txt = "\u21bb " + shortResetText(s.w, now), tw = measureText(txt, "700 9px system-ui");
      if (xe + 5 + tw <= W - 1) g.appendChild(svgNode("text", { "class": "up-lbl out", x: (xe + 5).toFixed(1), y: (y + 9.6).toFixed(1) }, txt));
      else g.appendChild(svgNode("text", { "class": "up-lbl", x: (xe - 6).toFixed(1), y: (y + 9.6).toFixed(1), "text-anchor": "end" }, txt));
      g.appendChild(svgNode("title", {}, def.name + ": " + Math.round(s.pct) + "% · " + t("resets", new Date(s.reset).toLocaleString(locale()))));
      return { g: g, trk: { x: xs.toFixed(1), y: y, w: trkW.toFixed(1), h: lh, rx: lh / 2 } };
    }
    function appendPreviewLayers(stage, cache, item, W, H, map, x0, yMin, yMax, now, ganttGeom) {
      var ps = item.series;
      if (!ps) {
        cache.callouts.push({ pH: item.slotInfo.harness, pS: item.slotInfo.slotId, k: null, el: buildNoCacheCallout(item.slotInfo) });
        return;
      }
      var geom = computeSeriesRuns(ps, map);
      var pserSvg = layerSvg(W, H, "up-clayer up-clayer-preview up-pser", {
        "data-k": ps.def.k, "data-preview-h": ps.def.harness, "data-preview-slot": ps.slotId, "aria-hidden": "true"
      });
      appendCurveAndDot(pserSvg, ps, geom, true);
      stage.appendChild(pserSvg);
      cache.previewLayers.push({ h: ps.def.harness, slot: ps.slotId, el: pserSvg });

      var pText = ps.def.short + "·" + ps.slotLabel + " · " + previewAgeText(ps.at, now);
      var pw = pinWidth(pText);
      var px = geom.dot.x + 8;
      if (px + pw > W - 2) px = Math.max(x0 + 2, geom.dot.x - pw - 8);
      var leftSide = px < geom.dot.x;
      var ppinSvg = layerSvg(W, H, "up-clayer up-clayer-preview up-ppin", {
        "data-preview-h": ps.def.harness, "data-preview-slot": ps.slotId, "aria-hidden": "true"
      });
      ppinSvg.appendChild(svgNode("line", {
        "class": "up-leader up-pleader", "stroke-dasharray": "3 2",
        x1: (leftSide ? geom.dot.x - 4 : geom.dot.x + 4).toFixed(1), y1: geom.dot.y.toFixed(1),
        x2: (leftSide ? px + pw : px).toFixed(1), y2: geom.dot.y.toFixed(1)
      }));
      ppinSvg.appendChild(pinGroup(ps.def, px, Math.max(yMin, Math.min(yMax, geom.dot.y)), pw, pText, pText, { tabindex: "-1" }));
      stage.appendChild(ppinSvg);
      cache.previewLayers.push({ h: ps.def.harness, slot: ps.slotId, el: ppinSvg });

      if (ganttGeom && ps.knownReset && ganttGeom.laneYByK[ps.def.k] !== undefined) {
        var py = ganttGeom.laneYByK[ps.def.k], lh = ganttGeom.lh;
        var pxs = Math.max(x0, Math.min(ganttGeom.x1, ganttGeom.X(ps.start))), pxe = Math.max(pxs + 4, Math.min(ganttGeom.x1, ganttGeom.X(ps.reset)));
        var pglSvg = layerSvg(W, H, "up-clayer up-clayer-preview up-pgl", {
          "data-preview-h": ps.def.harness, "data-preview-slot": ps.slotId, "aria-hidden": "true"
        });
        pglSvg.appendChild(svgNode("rect", {
          x: pxs.toFixed(1), y: py, width: (pxe - pxs).toFixed(1), height: lh, rx: lh / 2,
          fill: "color-mix(in srgb, var(--k-" + ps.def.k + ") 22%, transparent)",
          stroke: "var(--k-" + ps.def.k + ")", "stroke-width": 1.2, "stroke-dasharray": "3 2"
        }));
        var pFillW = Math.max(0, (ps.pct / 100) * (pxe - pxs));
        pglSvg.appendChild(svgNode("rect", {
          x: pxs.toFixed(1), y: py, width: pFillW.toFixed(1), height: lh, rx: lh / 2,
          fill: "var(--k-" + ps.def.k + ")", opacity: "0.75"
        }));
        stage.appendChild(pglSvg);
        cache.previewLayers.push({ h: ps.def.harness, slot: ps.slotId, el: pglSvg });
      }

      cache.callouts.push({
        pH: ps.def.harness, pS: ps.slotId, k: null,
        el: buildCalloutNode(ps, geom.dot.x / W * 100, geom.dot.y / H * 100, now, {
          "data-preview-h": ps.def.harness, "data-preview-slot": ps.slotId
        })
      });
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
      var stage = el("div", "up-cstage usage-chart");
      stage.setAttribute("role", "img");
      stage.setAttribute("aria-label", t("chart", windowLabel(kind), t(hasKnown ? "baseline" : "unknownReset")));

      var baseSvg = layerSvg(W, H, "up-clayer-base", { "aria-hidden": "true" });
      if (hasKnown) {
        baseSvg.appendChild(svgNode("polygon", { "class": "up-wash", points: x0 + "," + y1 + " " + x1 + "," + y0 + " " + x0 + "," + y0 }));
      }
      [0, 50, 100].forEach(function (p) {
        baseSvg.appendChild(svgNode("line", { "class": "usage-grid", x1: x0, x2: x1, y1: Y(p), y2: Y(p) }));
        baseSvg.appendChild(svgNode("text", { x: p === 0 ? 20 : p === 50 ? 14 : 4, y: Y(p) + 3 }, String(p)));
      });
      [0, 50, 100].forEach(function (p, i) {
        if (i) baseSvg.appendChild(svgNode("line", { "class": "usage-day-grid", x1: X(p / 100), x2: X(p / 100), y1: y0, y2: y1 }));
        baseSvg.appendChild(svgNode("text", { x: X(p / 100), y: H - 9, "text-anchor": i === 0 ? "start" : i === 2 ? "end" : "middle" }, p + "%"));
      });
      if (hasKnown) {
        var ideal = svgNode("path", { "class": "usage-ideal", d: "M" + x0 + " " + y1 + "L" + x1 + " " + y0, "stroke-dasharray": "4 4" });
        ideal.appendChild(svgNode("title", {}, t("idealTitle")));
        baseSvg.appendChild(ideal);
      }
      stage.appendChild(baseSvg);

      var poly = hasKnown ? (x0 + "," + y1 + " " + x1 + "," + y0 + " " + x1 + ",0 " + x0 + ",0") : "";
      var cache = { focusLayers: [], seriesLayers: [], pinLayers: [], previewLayers: [], callouts: [] };
      var items = [], pins = [];
      list.forEach(function (s) {
        var map = function (ts, p) {
          if (ts === null || !s.knownReset) return { x: (x0 + x1) / 2, y: Y(p) };
          return { x: X((ts - s.start) / s.dur), y: Y(p) };
        };
        var geom = computeSeriesRuns(s, map);
        var pw = pinWidth(s.def.short);
        var px = geom.dot.x + 8;
        if (px + pw > W - 2) px = Math.max(x0 + 2, geom.dot.x - pw - 8);
        var pin = { s: s, x: px, y: geom.dot.y, w: pw, leftSide: px < geom.dot.x };
        pins.push(pin);
        items.push({ s: s, geom: geom, pin: pin });
        cache.callouts.push({ pH: null, pS: null, k: s.def.k, el: buildCalloutNode(s, geom.dot.x / W * 100, geom.dot.y / H * 100, now, { "data-callout-k": s.def.k }) });
      });
      separatePins(pins, y0 - 3, y1 + 2, 16);

      var focusSvgs = [], serSvgs = [], pinSvgs = [];
      items.forEach(function (it) {
        var s = it.s, p = it.pin, geom = it.geom;
        var focusSvg = layerSvg(W, H, "up-clayer up-clayer-focus", { "data-k": s.def.k, "aria-hidden": "true" });
        appendFocusVisuals(focusSvg, s, geom, s.knownReset ? poly : "", y1, p, null);
        focusSvgs.push(focusSvg);
        cache.focusLayers.push({ k: s.def.k, el: focusSvg });

        var serCls = "up-clayer up-clayer-ser up-ser" + (swapHarness === s.def.harness ? " swapin" : "") + (s.disabled ? " is-disabled" : "");
        var serSvg = layerSvg(W, H, serCls, { "data-k": s.def.k, "aria-hidden": "true" });
        appendCurveAndDot(serSvg, s, geom, false);
        var lx1 = p.leftSide ? geom.dot.x - 4 : geom.dot.x + 4, lx2 = p.leftSide ? p.x + p.w : p.x;
        serSvg.appendChild(svgNode("line", { "class": "up-leader", "data-k": s.def.k, x1: lx1.toFixed(1), y1: geom.dot.y.toFixed(1), x2: lx2.toFixed(1), y2: p.ly.toFixed(1) }));
        serSvgs.push(serSvg);
        cache.seriesLayers.push({ k: s.def.k, el: serSvg });

        var pinSvg = layerSvg(W, H, "up-clayer up-clayer-pin", { "data-k": s.def.k });
        pinSvg.appendChild(pinGroup(s.def, p.x, p.ly, p.w, s.def.short, pinAria(s)));
        pinSvgs.push(pinSvg);
        cache.pinLayers.push({ k: s.def.k, el: pinSvg });
      });
      focusSvgs.forEach(function (n) { stage.appendChild(n); });
      serSvgs.forEach(function (n) { stage.appendChild(n); });
      pinSvgs.forEach(function (n) { stage.appendChild(n); });

      previewItems.forEach(function (item) {
        var pmap = function (ts, p) {
          var ps = item.series;
          if (ts === null || !ps.knownReset) return { x: (x0 + x1) / 2, y: Y(p) };
          return { x: X((ts - ps.start) / ps.dur), y: Y(p) };
        };
        appendPreviewLayers(stage, cache, item, W, H, pmap, x0, y0, y1, now, null);
      });
      return { stage: stage, cache: cache };
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

      var stage = el("div", "up-cstage usage-chart");
      stage.setAttribute("role", "img");
      stage.setAttribute("aria-label", t("chart", windowLabel(kind), t("subTimeline")));

      var baseSvg = layerSvg(W, H, "up-clayer-base", { "aria-hidden": "true" });
      if (nowInRange && x1 > nowX) {
        baseSvg.appendChild(svgNode("rect", { "class": "up-fut", x: nowX.toFixed(1), y: yt - 6, width: Math.max(0, x1 - nowX).toFixed(1), height: yEnd - yt + 6 }));
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
          if (day > D0) baseSvg.appendChild(svgNode("line", { "class": "usage-day-grid", x1: X(day).toFixed(1), x2: X(day).toFixed(1), y1: yt, y2: yEnd }));
          var dObj = new Date(day), c = X(day + (nx - day) / 2);
          var dateTxt = (dObj.getMonth() + 1) + "/" + dObj.getDate() + " " + wdFmt.format(dObj);
          var tw = measureText(dateTxt, "9px system-ui");
          if (c - tw / 2 >= x0 && c + tw / 2 <= x1 && c - tw / 2 >= right + 4) {
            baseSvg.appendChild(svgNode("text", { "class": "usage-day-label", x: c.toFixed(1), y: H - 6, "text-anchor": "middle" }, dateTxt));
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
        baseSvg.appendChild(svgNode("text", { "class": "up-bound-label", x: x0, y: H - 6, "text-anchor": "start" }, d0Lbl));
        baseSvg.appendChild(svgNode("text", { "class": "up-bound-label", x: x1, y: H - 6, "text-anchor": "end" }, d1Lbl));
        var curH = new Date(D0); curH.setMinutes(0, 0, 0);
        var firstTick = curH.getTime() <= D0 ? curH.getTime() + 3600000 : curH.getTime();
        var lastLblRight = d0Right;
        for (var tt = firstTick; tt < D1; tt += 3600000) {
          var dt = new Date(tt), isMidnight = dt.getHours() === 0;
          if (!isMidnight && dt.getHours() % stepH !== 0) continue;
          var tx = X(tt);
          baseSvg.appendChild(svgNode("line", { "class": isMidnight ? "usage-day-grid up-mid-grid" : "usage-day-grid", x1: tx.toFixed(1), x2: tx.toFixed(1), y1: yt, y2: yEnd }));
          var lbl = isMidnight ? ((dt.getMonth() + 1) + "/" + dt.getDate()) : dt.toLocaleTimeString(locale(), timeFmt);
          var lw = measureText(lbl, "9px system-ui");
          if (tx - lw / 2 >= lastLblRight + 3 && tx + lw / 2 <= d1Left - 3) {
            baseSvg.appendChild(svgNode("text", { "class": isMidnight ? "up-mid-label" : "", x: tx.toFixed(1), y: H - 6, "text-anchor": "middle" }, lbl));
            lastLblRight = tx + lw / 2;
          }
        }
      }
      [0, 50, 100].forEach(function (p) {
        baseSvg.appendChild(svgNode("line", { "class": "usage-grid", x1: x0, x2: x1, y1: Y(p), y2: Y(p) }));
        baseSvg.appendChild(svgNode("text", { x: p === 0 ? 30 : p === 50 ? 24 : 14, y: Y(p) + 3 }, String(p)));
      });
      if (nowInRange) {
        var guide = svgNode("line", { "class": "usage-now", x1: nowX.toFixed(1), x2: nowX.toFixed(1), y1: yt - 4, y2: yEnd + 2, "stroke-dasharray": "2 3" });
        guide.appendChild(svgNode("title", {}, t("nowTitle")));
        baseSvg.appendChild(guide);
        baseSvg.appendChild(svgNode("text", { "class": "usage-now-label", x: nowX.toFixed(1), y: yt - 6, "text-anchor": "middle" }, t("now")));
      }
      stage.appendChild(baseSvg);

      var cache = { focusLayers: [], seriesLayers: [], pinLayers: [], previewLayers: [], callouts: [] };
      var map = function (ts, p) {
        if (ts === null) return { x: nowX, y: Y(p) };
        return { x: X(ts), y: Y(p) };
      };
      var byK = Object.create(null), pins = [];
      list.forEach(function (s) {
        var smap = function (ts, p) {
          if (ts === null || !s.knownReset) return { x: nowX, y: Y(p) };
          return map(ts, p);
        };
        var geom = computeSeriesRuns(s, smap);
        // Anchor pin at X(s.at) (geom.dot.x), NOT nowX, so stale observations remain truthful.
        var pw = pinWidth(s.def.short);
        var px = geom.dot.x + 8;
        if (px + pw > W - 2) px = Math.max(x0 + 2, geom.dot.x - pw - 8);
        var pin = { s: s, x: px, y: geom.dot.y, w: pw, leftSide: px < geom.dot.x };
        pins.push(pin);
        byK[s.def.k] = { s: s, geom: geom, pin: pin };
        cache.callouts.push({ pH: null, pS: null, k: s.def.k, el: buildCalloutNode(s, geom.dot.x / W * 100, geom.dot.y / H * 100, now, { "data-callout-k": s.def.k }) });
      });
      separatePins(pins, yt - 3, yb + 2, 16);

      var laneYByK = {}, focusSvgs = [], serSvgs = [], pinSvgs = [];
      activeDefs.forEach(function (def, idx) {
        var entry = byK[def.k] || null;
        var s = entry ? entry.s : null;
        var y = ly0 + idx * ls;
        laneYByK[def.k] = y;
        var gantt = buildGanttGroup(def, s, y, lh, x0, x1, W, X, nowX, now);
        var serCls = "up-clayer up-clayer-ser" + (s ? " up-ser" + (swapHarness === s.def.harness ? " swapin" : "") + (s.disabled ? " is-disabled" : "") : "");
        var serSvg = layerSvg(W, H, serCls, { "data-k": def.k, "aria-hidden": "true" });
        if (entry) {
          var geom = entry.geom, p = entry.pin;
          var poly = s.knownReset ? (X(s.start) + "," + Y(0) + " " + X(s.reset) + "," + Y(100) + " " + X(s.reset) + ",0 " + X(s.start) + ",0") : "";
          var ganttFocus = {
            idealD: s.knownReset ? ("M" + X(s.start).toFixed(1) + " " + Y(0) + "L" + X(s.reset).toFixed(1) + " " + Y(100)) : null,
            trk: gantt.trk
          };
          var focusSvg = layerSvg(W, H, "up-clayer up-clayer-focus", { "data-k": def.k, "aria-hidden": "true" });
          appendFocusVisuals(focusSvg, s, geom, poly, yb, p, ganttFocus);
          focusSvgs.push(focusSvg);
          cache.focusLayers.push({ k: def.k, el: focusSvg });

          appendCurveAndDot(serSvg, s, geom, false);
          var lx1 = p.leftSide ? geom.dot.x - 4 : geom.dot.x + 4, lx2 = p.leftSide ? p.x + p.w : p.x;
          serSvg.appendChild(svgNode("line", { "class": "up-leader", "data-k": def.k, x1: lx1.toFixed(1), y1: geom.dot.y.toFixed(1), x2: lx2.toFixed(1), y2: p.ly.toFixed(1) }));

          var pinSvg = layerSvg(W, H, "up-clayer up-clayer-pin", { "data-k": def.k });
          pinSvg.appendChild(pinGroup(s.def, p.x, p.ly, p.w, s.def.short, pinAria(s)));
          pinSvgs.push(pinSvg);
          cache.pinLayers.push({ k: def.k, el: pinSvg });
        }
        serSvg.appendChild(gantt.g);
        serSvgs.push(serSvg);
        cache.seriesLayers.push({ k: def.k, el: serSvg });
      });
      focusSvgs.forEach(function (n) { stage.appendChild(n); });
      serSvgs.forEach(function (n) { stage.appendChild(n); });
      pinSvgs.forEach(function (n) { stage.appendChild(n); });

      var ganttGeom = { laneYByK: laneYByK, lh: lh, x1: x1, X: X };
      previewItems.forEach(function (item) {
        var pmap = function (ts, p) {
          var ps = item.series;
          if (ts === null || !ps.knownReset) return { x: nowX, y: Y(p) };
          return { x: Math.max(x0, Math.min(x1, X(ts))), y: Y(p) };
        };
        appendPreviewLayers(stage, cache, item, W, H, pmap, x0, yt, yb, now, ganttGeom);
      });
      return { stage: stage, cache: cache };
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
      chartCache = { focusLayers: [], seriesLayers: [], pinLayers: [], previewLayers: [], callouts: [] };
      lastAppliedVisSig = null;
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
          body.appendChild(res.stage);
          res.cache.callouts.forEach(function (c) { body.appendChild(c.el); });
          chartCache.focusLayers = chartCache.focusLayers.concat(res.cache.focusLayers);
          chartCache.seriesLayers = chartCache.seriesLayers.concat(res.cache.seriesLayers);
          chartCache.pinLayers = chartCache.pinLayers.concat(res.cache.pinLayers);
          chartCache.previewLayers = chartCache.previewLayers.concat(res.cache.previewLayers);
          chartCache.callouts = chartCache.callouts.concat(res.cache.callouts);
          var missing = activeDefs.filter(function (d) { return !list.some(function (s) { return s.def === d; }); });
          if (missing.length) {
            panel.appendChild(el("div", "up-cp-note", t("noCurve", missing.map(function (d) { return d.short; }).join(" · "))));
          }
        }
      });
    }
    // Zero-re-render, zero-querySelectorAll visual state: toggles ONLY compositor-only layer classes and hidden flags.
    function applyVisualState() {
      if (!popover) return;
      var pH = previewState && previewState.harness || null;
      var pS = previewState && previewState.slotId || null;
      var pDef = pH ? defForHarness(pH, "") : null;
      var pK = pDef ? pDef.k : null;
      var f = pK ? null : focusKey;
      var sig = (f || "") + "\x1f" + (pH || "") + "\x1f" + (pS || "");
      if (sig === lastAppliedVisSig) return;
      lastAppliedVisSig = sig;

      if (chartCache) {
        chartCache.focusLayers.forEach(function (item) {
          toggleCls(item.el, "is-focus", !!f && item.k === f);
        });
        chartCache.seriesLayers.forEach(function (item) {
          toggleCls(item.el, "is-focus", !!f && item.k === f);
          toggleCls(item.el, "is-preview-base", !!pK && item.k === pK);
          toggleCls(item.el, "is-dim", (!!f && item.k !== f) || (!!pK && item.k !== pK));
        });
        chartCache.pinLayers.forEach(function (item) {
          toggleCls(item.el, "is-focus", !!f && item.k === f);
          toggleCls(item.el, "is-preview-base", !!pK && item.k === pK);
          toggleCls(item.el, "is-dim", (!!f && item.k !== f) || (!!pK && item.k !== pK));
        });
        chartCache.previewLayers.forEach(function (item) {
          toggleCls(item.el, "is-preview-on", !!pH && item.h === pH && item.slot === pS);
        });
        chartCache.callouts.forEach(function (item) {
          if (item.pH) {
            item.el.hidden = !(!!pH && item.pH === pH && item.pS === pS);
          } else {
            item.el.hidden = !(!!f && item.k === f);
          }
        });
      }
      if (rowCache) {
        rowCache.forEach(function (row) {
          var isPrevRow = !!pK && row.k === pK;
          toggleCls(row.el, "is-focus", (!!f && row.k === f) || isPrevRow);
          toggleCls(row.el, "is-dim", (!!f && row.k !== f) || (!!pK && row.k !== pK));
          toggleCls(row.el, "is-previewing", isPrevRow);
          row.liveCells.forEach(function (cell) { cell.hidden = isPrevRow; });
          row.liveStatus.forEach(function (st) { st.hidden = isPrevRow; });
          row.prevCells.forEach(function (item) { item.el.hidden = !(isPrevRow && item.slot === pS); });
          row.nocacheEls.forEach(function (item) { item.el.hidden = !(isPrevRow && item.slot === pS); });
        });
      }
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
      lastHoverTarget = null;
      applyVisualState();
      return true;
    }
    function toggleLock(k) {
      if (focusLocked && focusKey === k) { focusLocked = false; focusKey = null; }
      else { focusKey = k; focusLocked = true; }
      lastHoverTarget = null;
      applyVisualState();
    }
    function setPreview(harness, slotId) {
      var nextH = harness || null, nextS = slotId || null;
      var curH = previewState && previewState.harness || null;
      var curS = previewState && previewState.slotId || null;
      if (nextH === curH && nextS === curS) return;
      previewState = nextH && nextS ? { harness: nextH, slotId: nextS } : null;
      if (!previewState) lastHoverTarget = null;
      applyVisualState();
      if (popover) {
        var curW = popover.offsetWidth || 0, curHgt = popover.offsetHeight || 0;
        if (curW !== lastPlacedW || curHgt !== lastPlacedH) place();
      }
    }
