  // --- right-side board dial --------------------------------------------------
  // An outer circle-chain dial (· • ○ ● ○ • ·) along #bcol's right edge for
  // bounded snap-deck layouts (.live, .history, .worktree; never .dgrid.board).
  // Shows at most 18 dots in a sliding window centered on the active card,
  // freezes that window during pointer scrub so pointer capture never breaks,
  // throttles scrub aligns to at most 1 rAF/frame (zero idle rAF), and defers
  // boardFlushViewed until pointerup.
  var DIAL_MAX = 18;
  var DIAL_SCRUB_STEP = 14;
  var dialEl = null, dialTrack = null, dialTip = null;
  var dialCards = [], dialActive = -1, dialWinStart = 0, dialWinEnd = 0;
  var dialDots = [];
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

  function dialCardTitle(el) {
    if (!el) return "";
    if (el.hasAttribute("data-topic")) {
      var tn = el.querySelector && el.querySelector(":scope > .tophead > .tbar .tname");
      return (tn && tn.textContent ? tn.textContent.trim() : "") || el.getAttribute("data-topic") || "";
    }
    var t = el.querySelector && el.querySelector(".bc-hd .bc-nm, .bc-hd [data-rail-name]");
    return (t && t.textContent ? t.textContent.trim() : "") || el.getAttribute("data-kind") || el.getAttribute("data-card") || "";
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
      dialTrack = doc.createElement("div");
      dialTrack.className = "d-board-dial-track";
      dialEl.appendChild(dialTrack);
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
    var r = dotEl.getBoundingClientRect();
    var vw = window.innerWidth || 1200, vh = window.innerHeight || 800;
    var tip = dialEnsureTip();
    if (dialTipIdx !== idx || tip.hidden) {
      dialTipIdx = idx;
      tip.textContent = "";
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
    var x = Math.max(8, Math.min(vw - tr.width - 8, r.left - tr.width - 10));
    var y = Math.max(8, Math.min(vh - tr.height - 8, r.top + r.height / 2 - tr.height / 2));
    tip.style.transform = "translate3d(" + Math.round(x) + "px, " + Math.round(y) + "px, 0)";
  }

  function dialPaintDot(btn, card, idx, active, isFirstEdge, isLastEdge) {
    var tier = boardDialTier(idx, active);
    var title = dialCardTitle(card);
    var asks = dialCardHasAsks(card);
    var held = card.hasAttribute("data-board-held");
    var closed = card.classList.contains("closed");
    var isCard = card.hasAttribute("data-card");
    var st = closed ? "closed" : (card.getAttribute("data-state") || "");
    dialSetAttr(btn, "data-idx", String(idx));
    dialSetAttr(btn, "data-tier", String(tier));
    dialSetAttr(btn, "data-edge", (isFirstEdge || isLastEdge) ? "1" : null);
    dialSetAttr(btn, "data-ask", asks ? "1" : null);
    dialSetAttr(btn, "data-held", held ? "1" : null);
    dialSetAttr(btn, "data-closed", closed ? "1" : null);
    dialSetAttr(btn, "data-kind", isCard ? "card" : "topic");
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
      var pip = doc.createElement("span");
      pip.className = "d-board-dial-pip";
      pip.setAttribute("aria-hidden", "true");
      b.appendChild(pip);
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
  }

  function boardDialSync() {
    var rootEl = doc.documentElement;
    var list = anchorCards();
    dialCards = list;
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

  // O(1) active-index update called from anchorMark: updates tiers and slides
  // the window in place when needed, without rebuilding DOM or querying topics.
  function boardDialMark(n) {
    if (!dialEl || dialEl.hidden || !dialCards.length) return;
    var idx = n ? dialCards.indexOf(n) : -1;
    if (idx < 0) {
      boardDialSync();
      return;
    }
    if (idx === dialActive) return;
    var prev = dialActive;
    dialActive = idx;
    if (!dialScrubbing && dialCards.length > DIAL_MAX) {
      var win = boardDialWindow(dialCards.length, idx, DIAL_MAX);
      if (win.start !== dialWinStart || win.end !== dialWinEnd) {
        dialPaintWindow(win.start, win.end, win.moreUp, win.moreDown, idx);
        return;
      }
    }
    // Update only the dots whose tier or aria-current could have changed.
    var lo = Math.max(dialWinStart, Math.min(prev >= 0 ? prev : idx, idx) - 3);
    var hi = Math.min(dialWinEnd - 1, Math.max(prev >= 0 ? prev : idx, idx) + 3);
    for (var k = lo; k <= hi; k++) {
      var btn = dialDots[k - dialWinStart];
      if (!btn) continue;
      dialSetAttr(btn, "data-tier", String(boardDialTier(k, idx)));
      dialSetAttr(btn, "aria-current", k === idx ? "true" : null);
    }
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
        el.setAttribute("data-scrub", "");
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
