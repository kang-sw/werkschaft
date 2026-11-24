  // --- right-side board dial --------------------------------------------------
  // Center-locked rotary vernier dial straddling #bcol's right edge for bounded
  // snap-deck layouts (.live, .history, .worktree; never .dgrid.board).
  // Renders a fixed center reticle over a GPU-translated vertical strip of up
  // to 18 slotted items (card-kind icons for active & +/-1..+/-2, graduated
  // vernier ticks for outer items). Freezes the sliding window during pointer
  // scrub so pointer capture never breaks, throttles scrub aligns to at most
  // 1 rAF/frame (zero idle rAF), and defers boardFlushViewed until pointerup.
  var DIAL_MAX = 18;
  var DIAL_SLOT = 22;
  var DIAL_SCRUB_STEP = 16;
  var DIAL_SVG_NS = "http://www.w3.org/2000/svg";
  var dialEl = null, dialMask = null, dialTrack = null, dialTip = null;
  var dialCards = [], dialIndexByNode = new Map(), dialActive = -1, dialWinStart = 0, dialWinEnd = 0;
  var dialDots = [];
  var dialTrackPos = NaN, dialVtierPos = NaN;
  var dialScrubbing = false, dialScrub = null, dialSwallowClick = false;
  var dialWheel = null, dialTipIdx = -1;

  function boardDialWindow(total, active, maxVis) {
    var cap = maxVis || DIAL_MAX;
    if (total <= 0) return { start: 0, end: 0, moreUp: false, moreDown: false };
    if (total <= cap) return { start: 0, end: total, moreUp: false, moreDown: false };
    var idx = active >= 0 && active < total ? active : 0;
    var start = Math.max(0, Math.min(total - cap, idx - Math.floor(cap / 2)));
    var end = start + cap;
    return { start: start, end: end, moreUp: start > 0, moreDown: end < total };
  }

  function boardDialTier(index, active) {
    if (active < 0) return 3;
    var d = Math.abs(index - active);
    return d === 0 ? 0 : d === 1 ? 1 : d === 2 ? 2 : 3;
  }

  function boardDialVtier(dist) {
    var a = Math.abs(dist);
    return a < 0.5 ? 0 : a < 1.5 ? 1 : a < 2.5 ? 2 : a < 3.5 ? 3 : a < 4.5 ? 4 : 5;
  }

  function dialSetAttr(el, name, val) {
    if (val === null || val === undefined || val === "") {
      if (el.hasAttribute(name)) el.removeAttribute(name);
    } else if (el.getAttribute(name) !== val) {
      el.setAttribute(name, val);
    }
  }

  var DIAL_STATE_MAP = {
    implementing: ["impl", "js.wb.state.implementing"],
    researching: ["research", "js.wb.state.researching"],
    discussing: ["discuss", "js.wb.state.discussing"],
    waiting: ["wait", "js.wb.state.waiting"],
    "awaiting-confirmation": ["confirm", "js.wb.state.awaiting-confirmation"],
    closed: ["closed", "js.board.filter.closed"]
  };

  var DIAL_ICON_SHAPES = {
    topic: [
      ["path", { d: "M3.2 4.2h9.6a1.4 1.4 0 0 1 1.4 1.4v4.6a1.4 1.4 0 0 1-1.4 1.4H7.2l-2.6 2v-2H3.2a1.4 1.4 0 0 1-1.4-1.4V5.6a1.4 1.4 0 0 1 1.4-1.4Z" }]
    ],
    closed: [
      ["circle", { cx: "8", cy: "8", r: "5.6" }],
      ["path", { d: "M5.6 8.2l1.7 1.7 3.2-3.4" }]
    ],
    terminal: [
      ["rect", { x: "2.2", y: "3", width: "11.6", height: "10", rx: "1.8" }],
      ["path", { d: "M5 6.4L7.2 8.2 5 10" }],
      ["path", { d: "M8.6 10h2.6" }]
    ],
    file: [
      ["path", { d: "M4.2 2.4h5.2l3 3v8.2a1 1 0 0 1-1 1H4.2a1 1 0 0 1-1-1V3.4a1 1 0 0 1 1-1Z" }],
      ["path", { d: "M9.4 2.4v3h3" }]
    ],
    diff: [
      ["rect", { x: "2.4", y: "2.6", width: "11.2", height: "10.8", rx: "1.8" }],
      ["path", { d: "M6 6.2h4" }],
      ["path", { d: "M8 4.4v3.6" }],
      ["path", { d: "M6 10.6h4" }]
    ],
    web: [
      ["circle", { cx: "8", cy: "8", r: "5.6" }],
      ["path", { d: "M2.6 8h10.8" }],
      ["path", { d: "M8 2.4c1.6 1.6 1.6 9.6 0 11.2M8 2.4c-1.6 1.6-1.6 9.6 0 11.2" }]
    ],
    explorer: [
      ["path", { d: "M2.4 5a1.2 1.2 0 0 1 1.2-1.2h3l1.3 1.4h4.5a1.2 1.2 0 0 1 1.2 1.2v5.4a1.2 1.2 0 0 1-1.2 1.2H3.6a1.2 1.2 0 0 1-1.2-1.2V5Z" }]
    ],
    note: [
      ["rect", { x: "2.6", y: "2.6", width: "10.8", height: "10.8", rx: "1.8" }],
      ["path", { d: "M5.2 6h5.6M5.2 8.6h5.6M5.2 11.2h3.4" }]
    ]
  };

  function dialMakeSvg(kind) {
    var shapes = DIAL_ICON_SHAPES[kind] || DIAL_ICON_SHAPES.note;
    var svg = doc.createElementNS(DIAL_SVG_NS, "svg");
    svg.setAttribute("viewBox", "0 0 16 16");
    svg.setAttribute("aria-hidden", "true");
    for (var i = 0; i < shapes.length; i++) {
      var node = doc.createElementNS(DIAL_SVG_NS, shapes[i][0]);
      var attrs = shapes[i][1];
      var keys = Object.keys(attrs);
      for (var j = 0; j < keys.length; j++) node.setAttribute(keys[j], attrs[keys[j]]);
      svg.appendChild(node);
    }
    return svg;
  }

  function dialCardTitle(el) {
    if (!el) return "";
    if (el.hasAttribute("data-topic")) {
      var tn = el.querySelector && el.querySelector(":scope > .tophead > .tbar .tname");
      return (tn && tn.textContent ? tn.textContent.trim() : "") || el.getAttribute("data-topic") || "";
    }
    var t = el.querySelector && el.querySelector(".bc-hd .bc-nm, .bc-hd [data-rail-name]");
    return (t && t.textContent ? t.textContent.trim() : "") || el.getAttribute("data-kind") || el.getAttribute("data-card") || "";
  }

  function dialCardIconKind(el) {
    if (!el) return "note";
    if (el.hasAttribute("data-topic")) {
      return el.classList.contains("closed") ? "closed" : "topic";
    }
    var k = (el.getAttribute("data-kind") || "").toLowerCase();
    var kb = el.querySelector && el.querySelector(".bc-hd .bc-kind");
    var badge = (kb && kb.textContent ? kb.textContent.trim() : "").toLowerCase();
    if (k === "terminal" || k.indexOf("term") >= 0 || badge === "term" || badge === "terminal") return "terminal";
    if (k === "web" || badge === "web") return "web";
    if (k === "explorer" || k === "files" || badge === "files" || badge === "explorer") return "explorer";
    if (k === "diff" || badge === "diff") return "diff";
    if (k === "preview" || k === "file") {
      if (el.querySelector && el.querySelector(".pv-diff")) return "diff";
      var title = dialCardTitle(el).toLowerCase();
      if (title.slice(-5) === ".diff" || title.slice(-6) === ".patch") return "diff";
      return "file";
    }
    if (k === "note") return "note";
    if (badge === "file" || badge === "md" || badge === "code" || badge === "img" || badge === "pdf" || badge === "preview") return "file";
    return "note";
  }

  function dialCardBadge(el) {
    if (!el) return null;
    if (el.hasAttribute("data-topic")) {
      var st = el.classList.contains("closed") ? "closed" : (el.getAttribute("data-state") || "");
      var sm = DIAL_STATE_MAP[st];
      if (!sm) return null;
      var tr = wsT(sm[1]);
      var label = tr && tr !== sm[1] ? tr : (st === "closed" ? "Closed" : st);
      return { text: label, state: sm[0] };
    }
    var kb = el.querySelector && el.querySelector(".bc-hd .bc-kind");
    var kind = (kb && kb.textContent ? kb.textContent.trim() : "") || el.getAttribute("data-kind") || "";
    return kind ? { text: kind, state: "card" } : null;
  }

  function dialCardHasAsks(el) {
    if (!el || !el.hasAttribute("data-topic")) return false;
    var attr = el.getAttribute("data-open-asks");
    if (attr !== null) return attr === "true";
    return !!(el.querySelector && el.querySelector(":scope > .tophead > .tbar > .chip.policy"));
  }

  function dialFormatIndex(idx) {
    var n = idx + 1;
    return "#" + (n < 10 ? "0" + n : String(n));
  }

  function dialEnsureMounted() {
    var col = $("bcol");
    if (!col || !col.parentNode || !anchorSized.bounded) return null;
    if (col.parentNode.classList && col.parentNode.classList.contains("board")) return null;
    if (!dialEl) {
      dialEl = doc.createElement("nav");
      dialEl.id = "bdial";
      dialEl.className = "d-board-dial";
      dialEl.setAttribute("aria-label", wsT("js.board.dial.label"));
      dialEl.hidden = true;
      var reticle = doc.createElement("div");
      reticle.className = "d-board-dial-reticle";
      reticle.setAttribute("aria-hidden", "true");
      dialEl.appendChild(reticle);
      dialMask = doc.createElement("div");
      dialMask.className = "d-board-dial-mask";
      dialTrack = doc.createElement("div");
      dialTrack.className = "d-board-dial-track";
      dialMask.appendChild(dialTrack);
      dialEl.appendChild(dialMask);
      dialBindEvents(dialEl);
    }
    if (dialEl.parentNode !== col.parentNode) {
      col.parentNode.insertBefore(dialEl, col.nextSibling);
    }
    return dialEl;
  }

  function dialEnsureTip() {
    if (!dialTip) {
      dialTip = doc.createElement("div");
      dialTip.className = "d-board-dial-tip";
      dialTip.setAttribute("role", "tooltip");
      dialTip.hidden = true;
      doc.body.appendChild(dialTip);
    }
    return dialTip;
  }

  function dialHideTip() {
    dialTipIdx = -1;
    if (dialTip && !dialTip.hidden) dialTip.hidden = true;
  }

  function dialShowTip(dotEl, idx) {
    var card = dialCards[idx];
    if (!dotEl || !card || !dialEl || dialEl.hidden) { dialHideTip(); return; }
    var dr = dialEl.getBoundingClientRect();
    var r = dotEl.getBoundingClientRect();
    var vw = window.innerWidth || 1200, vh = window.innerHeight || 800;
    var tip = dialEnsureTip();
    if (dialTipIdx !== idx || tip.hidden) {
      dialTipIdx = idx;
      tip.textContent = "";
      var idxEl = doc.createElement("span");
      idxEl.className = "d-board-dial-tip-idx";
      idxEl.textContent = dialFormatIndex(idx);
      tip.appendChild(idxEl);
      var title = doc.createElement("span");
      title.className = "d-board-dial-tip-title";
      title.textContent = dialCardTitle(card);
      tip.appendChild(title);
      var badge = dialCardBadge(card);
      if (badge && badge.text) {
        var sb = doc.createElement("span");
        sb.className = "d-board-dial-tip-badge d-board-dial-tip-state";
        if (badge.state) sb.setAttribute("data-state", badge.state);
        sb.textContent = badge.text;
        tip.appendChild(sb);
      }
      var asks = dialCardHasAsks(card), held = card.hasAttribute("data-board-held");
      if (asks) {
        var ab = doc.createElement("span");
        ab.className = "d-board-dial-tip-badge d-board-dial-tip-ask";
        ab.textContent = wsT("js.board.dial.asks");
        tip.appendChild(ab);
      }
      if (held) {
        var hb = doc.createElement("span");
        hb.className = "d-board-dial-tip-badge d-board-dial-tip-held";
        hb.textContent = wsT("js.board.dial.held");
        tip.appendChild(hb);
      }
      tip.hidden = false;
    }
    var tr = tip.getBoundingClientRect();
    var anchorLeft = dr.width > 0 ? Math.min(r.left, dr.right - 28) : r.left;
    var x = Math.max(8, Math.min(vw - tr.width - 8, anchorLeft - tr.width - 8));
    var cy = (dialScrubbing && dr.height > 0) ? (dr.top + dr.height / 2) : (r.top + r.height / 2);
    var y = Math.max(8, Math.min(vh - tr.height - 8, cy - tr.height / 2));
    tip.style.transform = "translate3d(" + Math.round(x) + "px, " + Math.round(y) + "px, 0)";
  }

  function dialApplyTrack(pos) {
    if (!dialTrack) return;
    var maxIdx = Math.max(0, dialCards.length - 1);
    var clamped = Math.max(0, Math.min(maxIdx, pos));
    var q = Math.round(clamped * 100) / 100;
    if (q === dialTrackPos) return;
    dialTrackPos = q;
    dialTrack.style.transform = "translate3d(0, " + (-q * DIAL_SLOT).toFixed(2) + "px, 0)";
  }

  function dialUpdateVtiers(centerPos, prevPos) {
    var c = Math.round(centerPos * 2) / 2;
    if (c === dialVtierPos) return;
    var prev = isNaN(prevPos) ? c : prevPos;
    dialVtierPos = c;
    var lo = Math.max(dialWinStart, Math.floor(Math.min(prev, c)) - 6);
    var hi = Math.min(dialWinEnd - 1, Math.ceil(Math.max(prev, c)) + 6);
    for (var k = lo; k <= hi; k++) {
      var btn = dialDots[k - dialWinStart];
      if (!btn) continue;
      dialSetAttr(btn, "data-vtier", String(boardDialVtier(k - centerPos)));
    }
  }

  function dialPaintDot(btn, card, idx, active, isFirstEdge, isLastEdge) {
    var tier = boardDialTier(idx, active);
    var vtier = boardDialVtier(idx - active);
    var title = dialCardTitle(card);
    var asks = dialCardHasAsks(card);
    var held = card.hasAttribute("data-board-held");
    var closed = card.classList.contains("closed");
    var isCard = card.hasAttribute("data-card");
    var iconKind = dialCardIconKind(card);
    var st = closed ? "closed" : (card.getAttribute("data-state") || "");
    if (btn._dialTop !== idx) {
      btn._dialTop = idx;
      btn.style.top = (idx * DIAL_SLOT) + "px";
    }
    if (btn._dialIcon !== iconKind) {
      btn._dialIcon = iconKind;
      var ic = btn._dialIc;
      if (btn._dialSvg && btn._dialSvg.parentNode === ic) ic.removeChild(btn._dialSvg);
      btn._dialSvg = dialMakeSvg(iconKind);
      ic.insertBefore(btn._dialSvg, btn._dialRing);
    }
    dialSetAttr(btn, "data-idx", String(idx));
    dialSetAttr(btn, "data-tier", String(tier));
    dialSetAttr(btn, "data-vtier", String(vtier));
    dialSetAttr(btn, "data-edge", (isFirstEdge || isLastEdge) ? "1" : null);
    dialSetAttr(btn, "data-ask", asks ? "1" : null);
    dialSetAttr(btn, "data-held", held ? "1" : null);
    dialSetAttr(btn, "data-closed", closed ? "1" : null);
    dialSetAttr(btn, "data-kind", isCard ? "card" : "topic");
    dialSetAttr(btn, "data-icon", iconKind);
    dialSetAttr(btn, "data-state", st || null);
    dialSetAttr(btn, "aria-label", title || String(idx + 1));
    dialSetAttr(btn, "aria-current", idx === active ? "true" : null);
  }

  function dialPaintWindow(start, end, moreUp, moreDown, active) {
    var count = Math.max(0, end - start);
    while (dialDots.length < count) {
      var b = doc.createElement("button");
      b.type = "button";
      b.className = "d-board-dial-dot";
      var ic = doc.createElement("span");
      ic.className = "d-board-dial-ic";
      ic.setAttribute("aria-hidden", "true");
      var ring = doc.createElement("span");
      ring.className = "d-board-dial-ring";
      ic.appendChild(ring);
      var tick = doc.createElement("span");
      tick.className = "d-board-dial-tick";
      tick.setAttribute("aria-hidden", "true");
      b.appendChild(ic);
      b.appendChild(tick);
      b._dialIc = ic;
      b._dialRing = ring;
      b._dialSvg = null;
      b._dialIcon = "";
      b._dialTop = -1;
      dialTrack.appendChild(b);
      dialDots.push(b);
    }
    while (dialDots.length > count) {
      var rem = dialDots.pop();
      if (rem.parentNode) rem.parentNode.removeChild(rem);
    }
    for (var i = 0; i < count; i++) {
      var idx = start + i;
      dialPaintDot(dialDots[i], dialCards[idx], idx, active, moreUp && i === 0, moreDown && i === count - 1);
    }
    dialWinStart = start;
    dialWinEnd = end;
    dialVtierPos = active;
    dialApplyTrack(active);
  }

  function boardDialSync() {
    var rootEl = doc.documentElement;
    var list = anchorCards();
    dialCards = list;
    dialIndexByNode = new Map();
    for (var di = 0; di < list.length; di++) dialIndexByNode.set(list[di], di);
    var show = !!(anchorSized.bounded && list.length > 1);
    if (show) {
      if (!dialEnsureMounted()) show = false;
    }
    if (show) {
      if (!rootEl.hasAttribute("data-board-dial")) rootEl.setAttribute("data-board-dial", "");
    } else {
      if (rootEl.hasAttribute("data-board-dial")) rootEl.removeAttribute("data-board-dial");
    }
    if (!dialEl) return;
    if (dialEl.hidden !== !show) dialEl.hidden = !show;
    if (!show) {
      dialHideTip();
      dialActive = -1;
      dialTrackPos = NaN;
      dialVtierPos = NaN;
      return;
    }
    var cur = anchorCurrent();
    var idx = cur ? list.indexOf(cur) : 0;
    if (idx < 0) idx = 0;
    dialActive = idx;
    var win;
    if (dialScrubbing && dialScrub) {
      var s = Math.max(0, Math.min(Math.max(0, list.length - DIAL_MAX), dialScrub.frozenStart));
      var e = Math.min(list.length, s + DIAL_MAX);
      win = { start: s, end: e, moreUp: s > 0, moreDown: e < list.length };
    } else {
      win = boardDialWindow(list.length, idx, DIAL_MAX);
    }
    dialPaintWindow(win.start, win.end, win.moreUp, win.moreDown, idx);
  }

  // Smooth fractional scroll sync called from free-canvas scroll ticks.
  // Performs pure GPU translate3d + local vtier updates with zero layout reads.
  function boardDialScroll(pos) {
    if (!dialEl || dialEl.hidden || !dialCards.length || dialScrubbing) return;
    if (typeof pos !== "number" || !isFinite(pos)) return;
    var clamped = Math.max(0, Math.min(dialCards.length - 1, pos));
    dialApplyTrack(clamped);
    dialUpdateVtiers(clamped, dialVtierPos);
  }

  // O(1) active-index update called from anchorMark: updates tiers, vtiers,
  // track transform, and slides the window in place when needed without
  // rebuilding DOM or querying topics.
  function boardDialMark(n) {
    if (!dialEl || dialEl.hidden || !dialCards.length) return;
    var idx = n && dialIndexByNode.has(n) ? dialIndexByNode.get(n) : -1;
    if (idx < 0) {
      boardDialSync();
      return;
    }
    if (idx === dialActive) {
      dialApplyTrack(idx);
      dialUpdateVtiers(idx, dialVtierPos);
      return;
    }
    var prev = dialActive;
    dialActive = idx;
    if (!dialScrubbing && dialCards.length > DIAL_MAX) {
      var win = boardDialWindow(dialCards.length, idx, DIAL_MAX);
      if (win.start !== dialWinStart || win.end !== dialWinEnd) {
        dialPaintWindow(win.start, win.end, win.moreUp, win.moreDown, idx);
        return;
      }
    }
    var lo = Math.max(dialWinStart, Math.min(prev >= 0 ? prev : idx, idx) - 6);
    var hi = Math.min(dialWinEnd - 1, Math.max(prev >= 0 ? prev : idx, idx) + 6);
    for (var k = lo; k <= hi; k++) {
      var btn = dialDots[k - dialWinStart];
      if (!btn) continue;
      dialSetAttr(btn, "data-tier", String(boardDialTier(k, idx)));
      dialSetAttr(btn, "data-vtier", String(boardDialVtier(k - idx)));
      dialSetAttr(btn, "aria-current", k === idx ? "true" : null);
    }
    dialVtierPos = idx;
    dialApplyTrack(idx);
  }

  function dialFlushScrubFrame() {
    var sc = dialScrub;
    if (!sc) return;
    sc.raf = 0;
    var targetIdx = sc.pendingIdx;
    sc.pendingIdx = -1;
    if (targetIdx >= 0 && targetIdx < dialCards.length) {
      anchorSelect(dialCards[targetIdx], { smooth: false, keep: true });
    }
  }

  function dialEndScrub(commit) {
    var sc = dialScrub;
    if (!sc) return;
    dialScrub = null;
    if (sc.raf && typeof cancelAnimationFrame === "function") cancelAnimationFrame(sc.raf);
    var moved = sc.moved;
    var finalIdx = sc.pendingIdx >= 0 ? sc.pendingIdx : sc.lastIdx;
    dialScrubbing = false;
    if (dialEl) dialEl.removeAttribute("data-scrub");
    if (moved) {
      dialSwallowClick = true;
      setTimeout(function () { dialSwallowClick = false; }, 0);
      if (commit && finalIdx >= 0 && finalIdx < dialCards.length) {
        anchorSelect(dialCards[finalIdx], { smooth: false, keep: true });
      }
      if (commit && Object.keys(boardPending).length) {
        boardFlushViewed(true);
      }
      if (typeof virtualReconcile === "function") virtualReconcile();
      boardDialSync();
    }
    dialHideTip();
  }

  function dialBindEvents(el) {
    el.addEventListener("click", function (e) {
      if (dialSwallowClick) { dialSwallowClick = false; return; }
      var btn = e.target && e.target.closest && e.target.closest(".d-board-dial-dot");
      if (!btn) return;
      var idx = parseInt(btn.getAttribute("data-idx"), 10);
      if (isNaN(idx) || !dialCards[idx]) return;
      anchorSelect(dialCards[idx], { smooth: true });
    });

    el.addEventListener("pointerover", function (e) {
      if (dialScrubbing) return;
      var btn = e.target && e.target.closest && e.target.closest(".d-board-dial-dot");
      if (!btn) return;
      var idx = parseInt(btn.getAttribute("data-idx"), 10);
      if (!isNaN(idx)) dialShowTip(btn, idx);
    });

    el.addEventListener("pointerleave", function () {
      if (!dialScrubbing) dialHideTip();
    });

    el.addEventListener("focusin", function (e) {
      var btn = e.target && e.target.closest && e.target.closest(".d-board-dial-dot");
      if (!btn) return;
      var idx = parseInt(btn.getAttribute("data-idx"), 10);
      if (!isNaN(idx)) dialShowTip(btn, idx);
    });

    el.addEventListener("focusout", function (e) {
      if (!el.contains(e.relatedTarget)) dialHideTip();
    });

    el.addEventListener("keydown", function (e) {
      if (!dialCards.length) return;
      var k = e.key, next = -1;
      if (k === "ArrowUp") next = Math.max(0, dialActive - 1);
      else if (k === "ArrowDown") next = Math.min(dialCards.length - 1, dialActive + 1);
      else if (k === "Home") next = 0;
      else if (k === "End") next = dialCards.length - 1;
      else if (k === "Escape") { dialHideTip(); return; }
      else return;
      e.preventDefault();
      if (next !== dialActive && dialCards[next]) {
        anchorSelect(dialCards[next], { smooth: true });
        var dot = dialDots[next - dialWinStart];
        if (dot) { dot.focus({ preventScroll: true }); dialShowTip(dot, next); }
      }
    });

    el.addEventListener("wheel", function (e) {
      if (e.preventDefault) e.preventDefault();
      var dir = 0;
      if (!dialWheel && window.WsGallery && window.WsGallery.RailWheel) {
        dialWheel = new window.WsGallery.RailWheel();
      }
      if (dialWheel) {
        dir = dialWheel.wheel({ dy: +e.deltaY || 0, deltaMode: e.deltaMode || 0, now: Date.now() });
      } else {
        dir = e.deltaY > 0 ? 1 : e.deltaY < 0 ? -1 : 0;
      }
      if (dir) anchorStep(dir);
    }, { passive: false });

    function onScrubMove(e) {
      var sc = dialScrub;
      if (!sc || e.pointerId !== sc.id) return;
      if (!sc.moved) {
        if (Math.abs(e.clientY - sc.startY) <= 4) return;
        sc.moved = true;
        dialScrubbing = true;
        el.setAttribute("data-scrub", "1");
        try { el.setPointerCapture(sc.id); } catch (x) {}
      }
      if (e.preventDefault) e.preventDefault();
      var targetIdx;
      if (sc.centers && sc.centers.length === dialCards.length) {
        var best = 0, bestD = Infinity;
        for (var i = 0; i < sc.centers.length; i++) {
          var d = Math.abs(e.clientY - sc.centers[i]);
          if (d < bestD) { bestD = d; best = i; }
        }
        targetIdx = best;
      } else {
        targetIdx = Math.max(0, Math.min(dialCards.length - 1, sc.startIdx + Math.round((e.clientY - sc.startY) / DIAL_SCRUB_STEP)));
      }
      if (targetIdx === sc.lastIdx) return;
      sc.lastIdx = targetIdx;
      boardDialMark(dialCards[targetIdx]);
      var dot = dialDots[targetIdx - dialWinStart];
      if (dot) dialShowTip(dot, targetIdx);
      sc.pendingIdx = targetIdx;
      if (!sc.raf) {
        if (typeof requestAnimationFrame === "function") sc.raf = requestAnimationFrame(dialFlushScrubFrame);
        else dialFlushScrubFrame();
      }
    }

    function onScrubUp(e) {
      if (!dialScrub || e.pointerId !== dialScrub.id) return;
      var wasMoved = dialScrub.moved;
      if (wasMoved) { try { el.releasePointerCapture(e.pointerId); } catch (x) {} }
      dialEndScrub(true);
    }

    function onScrubCancel(e) {
      if (!dialScrub || e.pointerId !== dialScrub.id) return;
      var wasMoved = dialScrub.moved;
      if (wasMoved) { try { el.releasePointerCapture(e.pointerId); } catch (x) {} }
      dialEndScrub(false);
    }

    el.addEventListener("pointerdown", function (e) {
      if (dialScrub || !dialCards.length || (e.button !== undefined && e.button !== 0)) return;
      var btn = e.target && e.target.closest && e.target.closest(".d-board-dial-dot");
      var startIdx = btn ? parseInt(btn.getAttribute("data-idx"), 10) : dialActive;
      if (isNaN(startIdx) || startIdx < 0) startIdx = Math.max(0, dialActive);
      var centers = null;
      if (dialCards.length <= DIAL_MAX) {
        centers = dialDots.map(function (d) { var r = d.getBoundingClientRect(); return r.top + r.height / 2; });
      }
      dialScrub = {
        id: e.pointerId,
        startY: e.clientY,
        startIdx: startIdx,
        lastIdx: startIdx,
        frozenStart: dialWinStart,
        moved: false,
        raf: 0,
        pendingIdx: -1,
        centers: centers
      };
    });

    doc.addEventListener("pointermove", onScrubMove);
    doc.addEventListener("pointerup", onScrubUp);
    doc.addEventListener("pointercancel", onScrubCancel);
  }

  function boardDialInit() {
    boardDialSync();
  }
