(function () {
  // Runs after the shared mermaid script beside the page. A host that cannot
  // load that script (a sandboxed or srcdoc embed) leaves window.mermaid
  // unset, and each <pre class="mermaid"> keeps showing its readable source.
  //
  // Look: "Editorial Semantic" (ai-docs mockup 261127 mermaid-style-redesign,
  // Option C v2). Colours come from the page's own --wb-* tokens, resolved to
  // plain values (mermaid's colour math cannot read var(...) strings), then
  // mixed into role colours: decisions warm, stores cool, notes paper, Gantt
  // done/active/crit status colours, a 6-colour XYChart palette.
  var m = window.mermaid;
  if (!m) return;
  var doc = document, root = doc.documentElement;

  // ---- colour helpers ------------------------------------------------------
  function parseColor(s) {
    // "#rrggbb", "rgb(r, g, b)", "rgba(r, g, b, a)" and "color(srgb r g b / a)" -> [r,g,b,a]
    var h = /^#([0-9a-f]{6})$/i.exec(String(s || ""));
    if (h) return [parseInt(h[1].slice(0, 2), 16), parseInt(h[1].slice(2, 4), 16), parseInt(h[1].slice(4, 6), 16), 1];
    var n = String(s || "").match(/-?[\d.]+(?:e-?\d+)?%?/g);
    if (!n || n.length < 3) return null;
    var v = n.map(function (x) { return x.slice(-1) === "%" ? parseFloat(x) / 100 : parseFloat(x); });
    if (/^color\(/i.test(s)) v = [v[0] * 255, v[1] * 255, v[2] * 255, v.length > 3 ? v[3] : 1];
    else if (v.length < 4) v.push(1);
    return v;
  }
  function hex(v) {
    return "#" + [0, 1, 2].map(function (i) {
      var x = Math.max(0, Math.min(255, Math.round(v[i])));
      return (x < 16 ? "0" : "") + x.toString(16);
    }).join("");
  }
  // mix returns fg over bg at weight w (0..1), as an opaque hex colour.
  function mix(fg, bg, w) {
    var a = parseColor(fg), b = parseColor(bg);
    return hex([0, 1, 2].map(function (i) { return a[i] * w + b[i] * (1 - w); }));
  }
  function alpha(c, a) {
    var v = parseColor(c);
    return "rgba(" + Math.round(v[0]) + ", " + Math.round(v[1]) + ", " + Math.round(v[2]) + ", " + a + ")";
  }

  // The documented tokens (manuals/dashboard-style.md), used when the page
  // does not define a token (a topic-body frame carries page.css only).
  var FALLBACK = {
    dark: {
      bg: "#141518", fg: "#e7e7e4", card: "#1e2024", muted: "#2b2d32", mutedFg: "#a2a6ad",
      primary: "#7aa2ff", primaryFg: "#0c1424", now: "#243457", onNow: "#d3defb",
      ok: "#6cc39a", fail: "#f0797e", warn: "#e0a36c", hot: "#fb923c", technical: "#26385e",
      announced: "#1f4433", onAnnounced: "#c6ead6", phThink: "#3d2f6b", onPhThink: "#ddd3fb"
    },
    light: {
      bg: "#f3f3f0", fg: "#1b1d21", card: "#ffffff", muted: "#e9e8e3", mutedFg: "#5f646b",
      primary: "#2b62d9", primaryFg: "#ffffff", now: "#2b62d9", onNow: "#ffffff",
      ok: "#1d7348", fail: "#c0262d", warn: "#c27a00", hot: "#d95d16", technical: "#2457c5",
      announced: "#1d7348", onAnnounced: "#ffffff", phThink: "#6a4fc4", onPhThink: "#ffffff"
    }
  };

  // ---- the host surface a block sits on ------------------------------------
  // A chat bubble paints --wb-card, a board card --wb-bg: labels and clusters
  // must match the surface around the diagram, or they show as stickers.
  function surfaceOf(pre, card, page) {
    for (var e = pre.parentElement; e; e = e.parentElement) {
      var b = parseColor(getComputedStyle(e).backgroundColor);
      if (!b || b[3] < 0.99) continue;
      var dc = 0, dp = 0;
      for (var i = 0; i < 3; i++) { dc += Math.pow(b[i] - card[i], 2); dp += Math.pow(b[i] - page[i], 2); }
      return dc < dp ? "card" : "bg";
    }
    return "bg";
  }

  // ---- the compact inline error card ---------------------------------------
  // suppressErrorRendering keeps mermaid's own error drawing out; the block
  // shows one summary line, the parser's message on demand, and the source.
  function errorCard(err, src) {
    var msg = String(err && err.message ? err.message : err || "").replace(/\s+$/, "");
    var first = msg.split("\n").filter(function (l) { return l.trim(); })[0] || "Syntax error";
    function el(tag, cls, text) {
      var e = doc.createElement(tag);
      if (cls) e.className = cls;
      if (text != null) e.textContent = text;
      return e;
    }
    var card = el("div", "wb-mmd-err");
    var head = el("div", "wb-mmd-err-head");
    var title = el("div", "wb-mmd-err-title");
    title.appendChild(el("span", "wb-mmd-err-badge", "Syntax Error"));
    title.appendChild(el("span", "wb-mmd-err-sum", first));
    var acts = el("div", "wb-mmd-err-acts");
    var toggle = el("button", "wb-mmd-err-btn", "소스 보기");
    toggle.type = "button";
    var copy = el("button", "wb-mmd-err-btn", "소스 복사");
    copy.type = "button";
    acts.appendChild(toggle);
    acts.appendChild(copy);
    head.appendChild(title);
    head.appendChild(acts);
    var detail = el("pre", "wb-mmd-err-msg", msg);
    var raw = el("pre", "wb-mmd-err-raw", src);
    detail.hidden = true;
    raw.hidden = true;
    toggle.addEventListener("click", function () {
      var show = raw.hidden;
      raw.hidden = !show;
      detail.hidden = !show || msg === first;
      toggle.textContent = show ? "소스 숨기기" : "소스 보기";
    });
    copy.addEventListener("click", function () {
      var done = function () {
        copy.textContent = "복사됨";
        setTimeout(function () { copy.textContent = "소스 복사"; }, 1200);
      };
      try {
        if (navigator.clipboard && navigator.clipboard.writeText) { navigator.clipboard.writeText(src).then(done, done); return; }
      } catch (e) { /* fall through */ }
      var ta = doc.createElement("textarea");
      ta.value = src;
      ta.style.cssText = "position:fixed;opacity:0";
      doc.body.appendChild(ta);
      ta.select();
      try { doc.execCommand("copy"); } catch (e) { /* no clipboard */ }
      doc.body.removeChild(ta);
      done();
    });
    card.appendChild(head);
    card.appendChild(detail);
    card.appendChild(raw);
    return card;
  }

  // ---- configuration for one tone and surface ------------------------------
  function configure(dark, surface, p, font) {
    var onCard = surface === "card";
    var container = onCard ? p.card : p.bg;
    var clusterBase = onCard ? p.bg : p.card;
    var nodeBg = p.muted;
    var line = alpha(p.fg, 0.16), lineStrong = alpha(p.fg, 0.28);
    var divider = mix(p.fg, nodeBg, dark ? 0.16 : 0.14);
    var erOdd = mix(p.mutedFg, nodeBg, 0.08);
    var erEven = mix(clusterBase, nodeBg, dark ? 0.35 : 0.45);

    var cluster = mix(p.primary, clusterBase, dark ? 0.08 : 0.05);
    var actor = mix(p.primary, nodeBg, dark ? 0.20 : 0.14);
    var erHeader = mix(p.primary, nodeBg, dark ? 0.22 : 0.16);
    var classBase = dark ? (onCard ? "#23262e" : "#1f222a") : (onCard ? "#f6f8fc" : "#ffffff");
    var classCard = mix(p.primary, classBase, dark ? 0.06 : (onCard ? 0.035 : 0.02));
    var classBorder = mix(p.primary, container, dark ? 0.34 : 0.28);
    var classHeadDiv = mix(p.primary, divider, dark ? 0.42 : 0.36);
    var classSecDiv = dark ? "rgba(231, 231, 228, 0.12)" : "rgba(27, 29, 33, 0.11)";
    var xyBar = mix(p.primary, container, dark ? 0.36 : 0.18);
    var xyBarStroke = dark ? mix(p.primary, container, 0.86) : p.primary;
    var xyLine1 = dark ? p.hot : mix(p.fg, p.hot, 0.16);
    var xyAxis = mix(p.mutedFg, container, 0.38);
    var xyAxisTitle = mix(p.mutedFg, p.fg, 0.35);
    var noteBg = dark ? mix(p.warn, "#23201b", 0.15) : mix(p.warn, "#fcfaf4", 0.14);
    var noteBorder = mix(p.warn, container, dark ? 0.52 : 0.55);
    var decBg = mix(p.warn, nodeBg, dark ? 0.14 : 0.12);
    var decBorder = mix(p.warn, container, 0.45);
    var dbBg = mix(p.technical, nodeBg, dark ? 0.35 : 0.10);
    var dbBorder = mix(p.primary, container, dark ? 0.42 : 0.38);
    var actBg = mix(p.primary, container, dark ? 0.25 : 0.18);
    var loopBg = mix(p.phThink, nodeBg, dark ? 0.34 : 0.14);
    var loopBorder = mix(p.phThink, container, dark ? 0.72 : 0.50);
    var doneBg = dark ? p.announced : mix(p.ok, clusterBase, 0.16);
    var activeBg = dark ? p.now : mix(p.primary, clusterBase, 0.22);
    var critBg = mix(p.fail, clusterBase, dark ? 0.28 : 0.18);
    var edge = mix(p.fg, p.mutedFg, 0.30);
    var edgeLabelBg = container;
    var clusterBorder = classBorder;
    var headText = dark ? p.onNow : p.primary;
    var classTitle = dark ? p.onNow : mix(p.primary, p.fg, 0.32);
    var annot = dark ? p.onPhThink : p.phThink;
    var memberFg = mix(p.mutedFg, p.fg, 0.16), methodFg = mix(p.mutedFg, p.fg, 0.45);
    var edgeLabelBorder = mix(p.primary, container, dark ? 0.28 : 0.24);
    var actorBorder = mix(p.primary, container, dark ? 0.52 : 0.48);
    var actorLine = mix(p.primary, p.mutedFg, 0.35);
    var loopStroke = mix(p.phThink, p.mutedFg, dark ? 0.65 : 0.45);
    var erOuter = mix(p.primary, p.mutedFg, 0.55);
    var stateDiv = mix(p.primary, p.mutedFg, 0.42);
    var secBg = mix(p.primary, container, dark ? 0.06 : 0.045);
    var gridC = line;

    // Overrides run on the finished SVG, which sits in the page. Selectors
    // are scoped per diagram so no rule strips another diagram's strokes:
    // node outlines stay 1px, Class/ER dividers and hollow markers survive.
    var nr = ":not(.statediagram-note):not([id*=\"classId-\"]):not([id*=\"entity-\"])";
    var css = [
      ".flowchart-link,.edgePath .path,.relation,.transition,.relationshipLine,.messageLine0,.messageLine1{stroke:" + edge + " !important;stroke-width:1.45px;}",
      ".messageLine1,.edge-pattern-dashed,.dashed-line{stroke-dasharray:5 4 !important;}",
      "marker.flowchart-v2 .arrowMarkerPath,.arrowheadPath,[id$=\"-barbEnd\"] path,[id$=\"-arrowhead\"] path,[id$=\"-filled-head\"] path,[id$=\"-solidTopArrowHead\"] path,[id$=\"-solidBottomArrowHead\"] path,[id$=\"-dependencyStart\"] path,[id$=\"-dependencyEnd\"] path,[id*=\"-composition\"] path{fill:" + edge + " !important;stroke:" + edge + " !important;}",
      "marker.cross path,[id$=\"-crosshead\"] path,[id$=\"-stickTopArrowHead\"] path,[id$=\"-stickBottomArrowHead\"] path{fill:none !important;stroke:" + edge + " !important;stroke-width:1.6px !important;}",
      // hollow inheritance / aggregation markers and ER circles stay hollow
      "[id*=\"-extension\"] path,[id*=\"-extension\"] polygon,[id*=\"-aggregation\"] path,[id*=\"-aggregation\"] polygon,[id*=\"-lollipop\"] path,[id*=\"-lollipop\"] circle{fill:" + container + " !important;stroke:" + edge + " !important;stroke-width:1.5px !important;}",
      "marker.er path{fill:none !important;stroke:" + edge + " !important;stroke-width:1.4px !important;}",
      "marker.er circle{fill:" + container + " !important;stroke:" + edge + " !important;stroke-width:1.4px !important;}",
      // edge labels: pills in the surface colour, never see-through
      ".edgeLabel,.edgeLabel p,.edgeLabel span,.labelBkg,.relationshipLabelBox{background:" + edgeLabelBg + " !important;background-color:" + edgeLabelBg + " !important;color:" + p.fg + " !important;opacity:1 !important;}",
      ".edgeLabel .labelBkg:not(:has(> .edgeLabel:empty)){padding:2px 7px !important;border-radius:999px !important;border:1px solid " + edgeLabelBorder + " !important;}",
      "g.edgeLabel:has(> .label > foreignObject[width=\"0\"]),.edgeLabel .labelBkg:has(> .edgeLabel:empty),.edgeLabel foreignObject[width=\"0\"],.edgeLabel foreignObject[height=\"0\"]{display:none !important;padding:0 !important;border:0 !important;background:transparent !important;}",
      ".edgeLabel,.edgeLabel p,.edgeLabel span{font-size:11.5px !important;line-height:1.35 !important;color:" + p.fg + " !important;fill:" + p.fg + " !important;}",
      ".edgeTerminals,.edgeTerminals div,.edgeTerminals .edgeLabel,.edgeTerminals p,.edgeTerminals span{background:transparent !important;background-color:transparent !important;border:none !important;padding:0 !important;margin:0 !important;font-size:11px !important;font-weight:600 !important;line-height:1.25 !important;color:" + p.mutedFg + " !important;fill:" + p.mutedFg + " !important;font-variant-numeric:tabular-nums !important;}",
      ".edgeTerminals .edgeLabel{display:inline-block !important;}",
      ".edgeTerminals .labelBkg{background:transparent !important;background-color:transparent !important;border:none !important;padding:0 !important;}",
      ".edgeLabel rect,.relationshipLabelBox rect,.labelBox+rect{fill:" + edgeLabelBg + " !important;opacity:1 !important;}",
      // flowchart nodes by role: process, decision, database, terminal
      ".node" + nr + " rect.label-container,.node" + nr + " circle.label-container,.node" + nr + " ellipse.label-container{fill:" + nodeBg + ";stroke:" + lineStrong + ";stroke-width:1px;}",
      ".node" + nr + " rect.basic{rx:9px;ry:9px;}",
      ".node:not([id*=\"classId-\"]):not([id*=\"entity-\"]) polygon.label-container:not([points^=\"0,0 \"]){fill:" + decBg + ";stroke:" + decBorder + ";stroke-width:1px;}",
      ".node:not([id*=\"classId-\"]):not([id*=\"entity-\"]) polygon.label-container[points^=\"0,0 \"]{fill:" + nodeBg + ";stroke:" + lineStrong + ";stroke-width:1px;}",
      ".node" + nr + " path.label-container{fill:" + dbBg + ";stroke:" + dbBorder + ";stroke-width:1px;}",
      ".node" + nr + " g.label-container path:first-child{fill:" + nodeBg + ";stroke:" + lineStrong + ";stroke-width:1px;}",
      // the drawing kit's "store" class (A:::store): filled with the Now colour
      ".node.store rect,.node.store polygon,.node.store circle,.node.store ellipse,.node.store path{fill:" + p.now + " !important;}",
      ".node .label,.nodeLabel,.node .label text{color:" + p.fg + ";fill:" + p.fg + ";font-size:12.5px;}",
      ".node.store .label,.node.store .nodeLabel{color:" + p.onNow + " !important;fill:" + p.onNow + " !important;}",
      ".cluster rect{fill:" + cluster + " !important;stroke:" + clusterBorder + " !important;stroke-width:1px !important;rx:11px;ry:11px;}",
      ".cluster-label .nodeLabel,.cluster-label text,.cluster-label span,.cluster-label p{color:" + p.primary + " !important;fill:" + p.primary + " !important;font-size:12px !important;font-weight:600 !important;}",
      // class and ER: intact compartments, borders, zebra rows
      "[id*=\"classId-\"] .outer-path path:first-child{fill:" + classCard + " !important;stroke:none !important;}",
      "[id*=\"classId-\"] .outer-path path:nth-child(2){fill:none !important;stroke:" + classBorder + " !important;stroke-width:1px !important;}",
      "[id*=\"entity-\"] .outer-path path:first-child{fill:" + erHeader + " !important;stroke:none !important;}",
      "[id*=\"entity-\"] .outer-path path:nth-child(2){fill:none !important;stroke:" + erOuter + " !important;stroke-width:1.2px !important;}",
      ".node .divider path,.node g.divider path,.node .divider line{fill:none !important;stroke:" + classSecDiv + " !important;stroke-width:1px !important;}",
      "[id*=\"classId-\"] > .divider:not(.divider ~ .divider) path{stroke:" + classHeadDiv + " !important;stroke-width:1px !important;}",
      "[id*=\"entity-\"] > .divider path{stroke:" + erOuter + " !important;stroke-width:1.15px !important;}",
      "[id*=\"entity-\"] .row-rect-odd path:first-child{fill:" + erOdd + " !important;stroke:none !important;}",
      "[id*=\"entity-\"] .row-rect-even path:first-child{fill:" + erEven + " !important;stroke:none !important;}",
      "[id*=\"entity-\"] .row-rect-odd path:nth-child(2),[id*=\"entity-\"] .row-rect-even path:nth-child(2){fill:none !important;stroke:none !important;}",
      "[id*=\"classId-\"] .label-group .nodeLabel,[id*=\"classId-\"] .classTitle{color:" + classTitle + " !important;fill:" + classTitle + " !important;font-weight:650 !important;font-size:13px !important;letter-spacing:-0.01em !important;}",
      "[id*=\"classId-\"] .annotation-group .nodeLabel{color:" + annot + " !important;fill:" + annot + " !important;font-size:10.5px !important;font-weight:600 !important;letter-spacing:0.03em !important;}",
      "[id*=\"classId-\"] .members-group .nodeLabel{color:" + memberFg + " !important;fill:" + memberFg + " !important;font-size:12px !important;font-variant-numeric:tabular-nums !important;}",
      "[id*=\"classId-\"] .methods-group .nodeLabel{color:" + methodFg + " !important;fill:" + methodFg + " !important;font-size:11.5px !important;}",
      "[id*=\"entity-\"] .label.name .nodeLabel{color:" + headText + " !important;fill:" + headText + " !important;font-weight:600 !important;}",
      "[id*=\"entity-\"] .label.attribute-keys .nodeLabel{font-weight:600;color:" + p.primary + " !important;}",
      // state diagram
      ".statediagram-state rect.basic{fill:" + nodeBg + " !important;stroke:" + lineStrong + " !important;stroke-width:1px !important;rx:9px;ry:9px;}",
      ".statediagram-cluster rect.outer{fill:" + cluster + " !important;stroke:" + clusterBorder + " !important;stroke-width:1px !important;rx:11px;ry:11px;}",
      ".statediagram-cluster rect.inner{fill:" + cluster + " !important;stroke:" + divider + " !important;stroke-width:0.8px !important;}",
      ".statediagram-cluster-alt rect.divider{fill:" + cluster + " !important;stroke:" + stateDiv + " !important;stroke-width:1px !important;stroke-dasharray:6 4 !important;}",
      ".node circle.state-start{fill:" + p.ok + " !important;stroke:" + container + " !important;stroke-width:2px !important;}",
      ".node circle.state-end{fill:" + p.hot + " !important;stroke:" + container + " !important;stroke-width:2px !important;}",
      ".end-state-inner{fill:" + p.hot + " !important;stroke:none !important;}",
      "[id*=\"-state-\"][id*=\"_end-\"] .outer-path path:nth-child(1){fill:" + container + " !important;stroke:none !important;}",
      "[id*=\"-state-\"][id*=\"_end-\"] .outer-path path:nth-child(2){fill:none !important;stroke:" + p.hot + " !important;stroke-width:1.6px !important;}",
      "[id*=\"-state-\"][id*=\"_end-\"] .outer-path path:nth-child(3){fill:" + p.hot + " !important;stroke:none !important;}",
      "[id*=\"-state-\"][id*=\"_end-\"] .outer-path path:nth-child(4){fill:none !important;stroke:none !important;}",
      ".node.statediagram-note .outer-path path:first-child,.statediagram-note rect{fill:" + noteBg + " !important;stroke:none !important;}",
      ".node.statediagram-note .outer-path path:nth-child(2){fill:none !important;stroke:" + noteBorder + " !important;stroke-width:1px !important;}",
      ".statediagram-note .nodeLabel{color:" + p.fg + " !important;font-size:12px !important;}",
      // sequence
      "rect.actor{fill:" + actor + " !important;stroke:" + actorBorder + " !important;stroke-width:1.35px !important;rx:9px;ry:9px;}",
      "text.actor,text.actor>tspan{fill:" + headText + " !important;font-weight:600 !important;font-size:12.5px !important;}",
      ".actor-man circle,.actor-man line{fill:" + actor + " !important;stroke:" + actorBorder + " !important;stroke-width:1.5px !important;}",
      "line.actor-line{stroke:" + actorLine + " !important;stroke-width:1px !important;stroke-dasharray:4 4 !important;}",
      ".activation0,.activation1,.activation2{fill:" + actBg + " !important;stroke:" + p.primary + " !important;stroke-width:1px !important;}",
      ".loopLine{stroke:" + loopStroke + " !important;stroke-width:1.15px !important;stroke-dasharray:4 3 !important;fill:none !important;}",
      "polygon.labelBox{fill:" + loopBg + " !important;stroke:" + loopBorder + " !important;stroke-width:1px !important;}",
      "text.labelText,text.labelText>tspan{fill:" + p.fg + " !important;font-weight:600 !important;font-size:11.5px !important;}",
      "text.loopText,text.loopText>tspan{fill:" + p.mutedFg + " !important;font-size:11.5px !important;font-weight:500 !important;}",
      "text.messageText{fill:" + p.fg + " !important;font-size:12px !important;}",
      "rect.note{fill:" + noteBg + " !important;stroke:" + noteBorder + " !important;stroke-width:1px !important;rx:6px;ry:6px;}",
      "text.noteText,text.noteText>tspan{fill:" + p.fg + " !important;font-size:11.5px !important;}",
      "[id$=\"-sequencenumber\"] circle,[id$=\"-sequencenumber\"]{fill:" + p.primary + " !important;}",
      ".sequenceNumber{fill:" + p.primaryFg + " !important;font-weight:600 !important;font-size:10.5px !important;}",
      // gantt
      ".titleText{fill:" + p.fg + " !important;font-size:15px !important;font-weight:600 !important;}",
      ".section{stroke:none !important;opacity:1 !important;}",
      ".section0,.section2{fill:" + secBg + " !important;}",
      ".section1,.section3{fill:transparent !important;}",
      ".sectionTitle0,.sectionTitle1,.sectionTitle2,.sectionTitle3{fill:" + p.fg + " !important;font-size:12px !important;font-weight:600 !important;}",
      ".grid .tick line{stroke:" + gridC + " !important;opacity:1 !important;}",
      ".grid .tick text{fill:" + p.mutedFg + " !important;font-size:11px !important;}",
      ".today{stroke:" + p.hot + " !important;stroke-width:1.5px !important;stroke-dasharray:4 3 !important;}",
      ".task{rx:4px;ry:4px;stroke-width:1.15px !important;}",
      ".task0,.task1,.task2,.task3{fill:" + nodeBg + " !important;stroke:" + lineStrong + " !important;}",
      ".taskText0,.taskText1,.taskText2,.taskText3,.taskTextOutsideRight,.taskTextOutsideLeft{fill:" + p.fg + " !important;font-size:11.5px !important;font-weight:500 !important;}",
      ".active0,.active1,.active2,.active3{fill:" + activeBg + " !important;stroke:" + p.primary + " !important;}",
      ".activeText0,.activeText1,.activeText2,.activeText3{fill:" + (dark ? p.onNow : p.fg) + " !important;}",
      ".done0,.done1,.done2,.done3{fill:" + doneBg + " !important;stroke:" + p.ok + " !important;}",
      ".doneText0,.doneText1,.doneText2,.doneText3,.doneText0.taskTextOutsideRight,.doneText0.taskTextOutsideLeft{fill:" + (dark ? p.onAnnounced : p.ok) + " !important;}",
      ".crit0,.crit1,.crit2,.crit3{fill:" + critBg + " !important;stroke:" + p.fail + " !important;}",
      ".critText0,.critText1,.critText2,.critText3{fill:" + (dark ? p.fg : p.fail) + " !important;font-weight:600 !important;}",
      ".milestone{fill:" + p.hot + " !important;stroke:" + p.fg + " !important;rx:2px;ry:2px;}",
      ".milestoneText{fill:" + p.fg + " !important;font-style:normal !important;font-weight:600 !important;}",
      // xychart
      ".charts-background,.background{fill:transparent !important;}",
      ".chart-title text,text.chart-title{fill:" + p.fg + " !important;font-weight:600 !important;font-size:15px !important;letter-spacing:-0.01em !important;}",
      "[class*=\"bar-plot-\"] rect{rx:4px;ry:4px;opacity:1;}",
      "[class*=\"bar-plot-0\"] rect{fill:" + xyBar + " !important;stroke:" + xyBarStroke + " !important;stroke-width:1.25px !important;vector-effect:non-scaling-stroke;}",
      "[class*=\"line-plot-\"] path{stroke-width:2.6px !important;stroke-linecap:round !important;stroke-linejoin:round !important;fill:none !important;}",
      "[class*=\"line-plot-1\"] path{stroke:" + xyLine1 + " !important;}",
      ".bottom-axis .axis-line path,.left-axis .axis-line path,.left-axis .axisl-line path,.bottom-axis .ticks path,.left-axis .ticks path{stroke:" + xyAxis + " !important;stroke-width:1px !important;}",
      ".bottom-axis .label text,.left-axis .label text{fill:" + p.mutedFg + " !important;font-size:11.5px !important;font-weight:500 !important;font-variant-numeric:tabular-nums !important;}",
      ".bottom-axis .title text,.left-axis .title text{fill:" + xyAxisTitle + " !important;font-size:12px !important;font-weight:550 !important;}"
    ].join("");

    return {
      startOnLoad: false,
      securityLevel: "strict",
      theme: "base",
      darkMode: dark,
      fontFamily: font,
      suppressErrorRendering: true,
      flowchart: { htmlLabels: true, curve: "basis", nodeSpacing: 48, rankSpacing: 60, padding: 15 },
      sequence: { mirrorActors: false, useMaxWidth: true, actorMargin: 44, boxMargin: 8, noteMargin: 10, messageMargin: 36 },
      themeVariables: {
        darkMode: dark,
        background: container,
        fontFamily: font,
        fontSize: "13px",
        primaryColor: nodeBg, primaryTextColor: p.fg, primaryBorderColor: lineStrong,
        secondaryColor: actBg, secondaryTextColor: p.fg, secondaryBorderColor: p.primary,
        tertiaryColor: cluster, tertiaryTextColor: p.fg, tertiaryBorderColor: clusterBorder,
        mainBkg: nodeBg, nodeBkg: nodeBg, nodeBorder: lineStrong, nodeTextColor: p.fg, textColor: p.fg, titleColor: p.fg,
        clusterBkg: cluster, clusterBorder: clusterBorder,
        lineColor: edge, edgeLabelBackground: edgeLabelBg,
        actorBkg: actor, actorBorder: actorBorder, actorTextColor: headText, actorLineColor: actorLine,
        signalColor: edge, signalTextColor: p.fg,
        activationBkgColor: actBg, activationBorderColor: p.primary,
        labelBoxBkgColor: loopBg, labelBoxBorderColor: loopBorder, labelTextColor: p.fg, loopTextColor: p.mutedFg,
        noteBkgColor: noteBg, noteBorderColor: noteBorder, noteTextColor: p.fg,
        sequenceNumberColor: p.primaryFg,
        sectionBkgColor: secBg, altSectionBkgColor: "transparent", sectionBkgColor2: secBg,
        taskBkgColor: nodeBg, taskBorderColor: lineStrong, taskTextColor: p.fg, taskTextDarkColor: p.fg, taskTextOutsideColor: p.fg,
        activeTaskBkgColor: activeBg, activeTaskBorderColor: p.primary,
        doneTaskBkgColor: doneBg, doneTaskBorderColor: p.ok,
        critBkgColor: critBg, critBorderColor: p.fail,
        gridColor: gridC, todayLineColor: p.hot,
        excludeBkgColor: dark ? "rgba(255,255,255,0.04)" : "rgba(0,0,0,0.04)",
        xyChart: {
          backgroundColor: "transparent",
          titleColor: p.fg,
          xAxisLabelColor: p.mutedFg, xAxisTitleColor: xyAxisTitle, xAxisTickColor: xyAxis, xAxisLineColor: xyAxis,
          yAxisLabelColor: p.mutedFg, yAxisTitleColor: xyAxisTitle, yAxisTickColor: xyAxis, yAxisLineColor: xyAxis,
          plotColorPalette: [p.primary, p.hot, p.ok, p.warn, p.phThink, p.fail].join(",")
        }
      },
      themeCSS: css
    };
  }

  // ---- drawing -------------------------------------------------------------
  // One pass: reads (tone, tokens, surfaces) before writes, blocks drawn one
  // after another (mermaid.initialize is global), a pass that finds nothing
  // undrawn touches nothing. Passes queue, and one waiting pass covers any
  // number of requests made while another runs.
  function pass() {
    var mm = window.mermaid;
    if (!mm) return Promise.resolve();
    // A page that pins its theme (data-theme on the root) decides; else the OS does.
    var pinned = root.getAttribute("data-theme");
    var dark = pinned ? pinned === "dark" : !!(window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
    var tone = dark ? "dark" : "light";
    // A block keeps its source and the tone it was drawn in; one drawn in the
    // other tone gets its source back and is drawn again below.
    var todo = [];
    Array.prototype.forEach.call(doc.querySelectorAll("pre.mermaid"), function (pre) {
      if (!pre.hasAttribute("data-processed")) {
        if (!pre.hasAttribute("data-wb-src")) pre.setAttribute("data-wb-src", pre.textContent);
        else pre.textContent = pre.getAttribute("data-wb-src");
      } else if (pre.hasAttribute("data-wb-src") && pre.getAttribute("data-wb-tone") !== tone) {
        pre.textContent = pre.getAttribute("data-wb-src");
        pre.removeAttribute("data-processed");
      } else {
        return;
      }
      pre.setAttribute("data-wb-tone", tone);
      todo.push(pre);
    });
    if (!todo.length) return Promise.resolve();
    // mermaid's colour math cannot read var(...) strings: resolve every --wb-*
    // colour to the value the page computes for it before initializing.
    var probe = doc.createElement("span");
    probe.style.display = "none";
    doc.body.appendChild(probe);
    var rootStyle = getComputedStyle(root), fb = FALLBACK[tone];
    function c(name, key) {
      if (!rootStyle.getPropertyValue(name).trim()) return fb[key];
      probe.style.color = "var(" + name + ")";
      return getComputedStyle(probe).color;
    }
    var p = {
      bg: c("--wb-bg", "bg"), fg: c("--wb-fg", "fg"), card: c("--wb-card", "card"),
      muted: c("--wb-muted", "muted"), mutedFg: c("--wb-muted-fg", "mutedFg"),
      primary: c("--wb-primary", "primary"), primaryFg: c("--wb-primary-fg", "primaryFg"),
      now: c("--wb-now", "now"), onNow: c("--wb-on-now", "onNow"),
      ok: c("--wb-ok", "ok"), fail: c("--wb-fail", "fail"), warn: c("--wb-warn", "warn"), hot: c("--wb-hot", "hot"),
      technical: c("--wb-technical", "technical"),
      announced: c("--wb-announced", "announced"), onAnnounced: c("--wb-on-announced", "onAnnounced"),
      phThink: c("--wb-ph-think", "phThink"), onPhThink: c("--wb-on-ph-think", "onPhThink")
    };
    Object.keys(p).forEach(function (k) {
      // normalise to rgb()/hex so every mix sees one format
      var v = parseColor(p[k]);
      p[k] = v ? hex(v) : fb[k];
    });
    var font = getComputedStyle(doc.body).fontFamily;
    var cardRGB = parseColor(p.card), pageRGB = parseColor(p.bg);
    var jobs = todo.map(function (pre) { return { pre: pre, src: pre.getAttribute("data-wb-src"), surface: surfaceOf(pre, cardRGB, pageRGB) }; });
    doc.body.removeChild(probe);
    var cfg = {};
    var chain = Promise.resolve();
    jobs.forEach(function (job) {
      chain = chain.then(function () {
        if (!job.pre.isConnected || job.pre.hasAttribute("data-processed")) return;
        if (!cfg[job.surface]) cfg[job.surface] = configure(dark, job.surface, p, font);
        mm.initialize(cfg[job.surface]);
        var id = "wbm" + (window.__wbMermaidSeq = (window.__wbMermaidSeq || 0) + 1);
        return Promise.resolve().then(function () { return mm.render(id, job.src); }).then(function (res) {
          job.pre.innerHTML = res.svg;
          if (res.bindFunctions) res.bindFunctions(job.pre);
        }, function (err) {
          job.pre.textContent = "";
          job.pre.appendChild(errorCard(err, job.src));
        }).then(function () {
          // mermaid leaves its scratch element behind when a draw fails
          var stray = doc.getElementById("d" + id);
          if (stray && stray.parentNode) stray.parentNode.removeChild(stray);
          job.pre.setAttribute("data-processed", "true");
        });
      });
    });
    return chain;
  }

  var state = window.__wbMermaid || (window.__wbMermaid = { running: null, waiting: false });
  function request() {
    if (!state.running) {
      state.running = pass().then(function () { state.running = null; }, function () { state.running = null; });
      return;
    }
    if (state.waiting) return;
    state.waiting = true;
    state.running.then(function () {
      state.waiting = false;
      request();
    });
  }
  request();

  // Under "auto" the OS decides: redraw when it flips. One listener per
  // window however often this script runs; it calls the latest pass, and a
  // page with a pinned theme ignores it. A pass that finds every block already
  // drawn in the current tone does no work.
  state.pass = request;
  if (!state.listening && window.matchMedia) {
    var mq = window.matchMedia("(prefers-color-scheme: dark)");
    var onChange = function () {
      if (root.getAttribute("data-theme")) return;
      if (!window.innerWidth) return; // a collapsed frame draws when it is laid out
      state.pass();
    };
    if (mq.addEventListener) mq.addEventListener("change", onChange);
    else if (mq.addListener) mq.addListener(onChange);
    state.listening = true;
  }
})();
