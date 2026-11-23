  // --- board card anchor ------------------------------------------------------
  // The Main Board scrolls by card, not by pixel. One card is the topic section
  // (section.topic[data-topic]) or an unpinned card (.bcard[data-card]) the
  // reader stands on: its key ("t:" topic, "c:" id) is the whole outer scroll
  // state, its top aligned with wswhiteboard/rail.js's active landing line
  // (--wb-rail-inset + capH(min(2, prev))). Cards keep their content
  // height up to --board-usable and scroll inside themselves; overflow
  // anchoring is scoped to the current card (dashboard-board.css), so a size
  // change of any card reads and writes nothing on the outer board. Geometry
  // is read only by an explicit user move (selecting, a native scroll's end).
  var ANCHOR_STORE = "ws.dashboard.board-card." + location.pathname + location.search;
  var ANCHOR_CAP = 17, ANCHOR_GAP = 3, ANCHOR_PAD = 4, ANCHOR_FADE = 14;
  var ANCHOR_BOT = ANCHOR_FADE + 2 * ANCHOR_CAP + ANCHOR_GAP + ANCHOR_PAD;
  var ANCHOR_BOT_MARGIN = 8;
  var anchorKey = null, anchorSized = { w: 0, h: 0, bar: 0, last: 0, reply: 0, padTop: 0, inset: 0, bounded: false };
  var anchorLast = null, anchorRO = null, anchorMO = null, anchorFaceEl = null, anchorReplyEl = null;
  var anchorCap0 = null, anchorCapR0 = null, anchorCap1 = null;
  var anchorProg = null, anchorProgSeq = 0;
  function anchorCapH(n) { return n ? ANCHOR_PAD + n * (ANCHOR_CAP + ANCHOR_GAP) : 0; }
  function anchorToolbarFace() {
    var bar = window.WsCards && window.WsCards.toolbar && window.WsCards.toolbar();
    return bar && (bar.firstElementChild || bar) || null;
  }
  function anchorRailInset(col) {
    if (col && col.style && col.style.getPropertyValue) {
      var raw = parseFloat(col.style.getPropertyValue("--wb-rail-inset"));
      if (!isNaN(raw)) return raw;
    }
    return anchorSized.bar ? anchorSized.bar + 6 : anchorSized.inset;
  }
  function anchorBotReserve() {
    return ANCHOR_BOT + ANCHOR_BOT_MARGIN + (anchorSized.reply ? anchorSized.reply + 14 : 0);
  }
  function anchorCardCap(n, list) {
    if (!n || !(n.hasAttribute("data-topic") || n.hasAttribute("data-rail"))) return 0;
    var cards = list || anchorCards(), at = cards.indexOf(n), prev = 0;
    for (var i = 0; i < at; i++) if (cards[i].hasAttribute("data-topic") || cards[i].hasAttribute("data-rail")) prev++;
    return anchorCapH(Math.min(2, prev));
  }
  function anchorLandingTop(col, colRect, n, list) {
    return colRect.top + (col.clientTop || 0) + anchorSized.padTop + anchorRailInset(col) + anchorCardCap(n, list);
  }
  function anchorId(n) {
    var k = n && n.nodeType === 1 ? topicKey(n) : null;
    return k !== null ? "t:" + k : n && n.nodeType === 1 && n.hasAttribute("data-card") ? "c:" + n.getAttribute("data-card") : null;
  }
  // The snap order: the board region's cards in board order, fetched closed
  // pages (nested in one another) in place, filtered ones left out. Docked
  // topics stand in the gallery and have their own pages.
  function anchorCards() {
    var b = region("board"), out = [];
    if (!b) return out;
    (function walk(box) {
      Array.prototype.forEach.call(box.children, function (n) {
        if (n.classList.contains("closed-page")) walk(n);
        else if (anchorId(n) !== null && !n.hidden && !n.hasAttribute("data-state-filtered")) out.push(n);
      });
    })(b);
    return out;
  }
  function anchorNode(key) {
    if (key === null) return null;
    var list = anchorCards();
    for (var i = 0; i < list.length; i++) if (anchorId(list[i]) === key) return list[i];
    return null;
  }
  function anchorCurrent() { return anchorNode(anchorKey); }
  function anchorStore() {
    try {
      if (anchorKey === null) window.sessionStorage.removeItem(ANCHOR_STORE);
      else window.sessionStorage.setItem(ANCHOR_STORE, anchorKey);
    } catch (e) {}
  }
  function anchorMark(n) {
    var was = anchorLast;
    anchorLast = n || null;
    if (was && was !== n) was.removeAttribute("data-current");
    if (n) n.setAttribute("data-current", "");
    anchorKey = n ? anchorId(n) : null;
    anchorStore();
  }
  function anchorAlign(col, n, smooth, list) {
    if (!col || !n || !n.isConnected) return false;
    var delta, target;
    if (anchorSized.bounded && n.getAttribute && n.getAttribute("data-anchor-cap") === "0") {
      target = 0;
      delta = -col.scrollTop;
    } else {
      var c = col.getBoundingClientRect(), r = n.getBoundingClientRect();
      delta = r.top - anchorLandingTop(col, c, n, list);
      target = Math.max(0, col.scrollTop + delta);
    }
    if (anchorProg && anchorProg.key === anchorId(n)) anchorProg.target = target;
    if (Math.abs(delta) <= 0.5) return false;
    if (col.scrollTo) col.scrollTo({ top: target, behavior: smooth ? "smooth" : "auto" });
    else col.scrollTop = target;
    return true;
  }
  function anchorWaitFrames(pid, count) {
    if (typeof requestAnimationFrame !== "function") return;
    requestAnimationFrame(function () {
      if (!anchorProg || anchorProg.id !== pid) return;
      if (count > 1) { anchorWaitFrames(pid, count - 1); return; }
      anchorRealign(true);
    });
  }
  // select makes node n the current card and aligns it directly to rail.js's
  // top-capsule landing line. Marking current first activates overflow-anchor
  // and unskips containment before measuring the target card alone; no non-
  // current card geometry is read. Leaving a card with staged updates admits
  // the newest revision first.
  function anchorSelect(n, opt) {
    if (!n || !n.isConnected) return false;
    var col = $("bcol");
    if (!col || !col.contains(n)) return false;
    var key = anchorId(n);
    if (key === null) return false;
    var left = anchorKey !== null && anchorKey !== key;
    if (anchorKey !== key || anchorLast !== n) anchorMark(n);
    if (left && !(opt && opt.keep) && Object.keys(boardPending).length) {
      boardFlushViewed(true);
      n = anchorNode(key) || n;
    }
    if (!(opt && opt.still)) {
      var calm = reducedMotion(), smooth = !!(opt && opt.smooth && !calm);
      var bar = window.WsCards && window.WsCards.toolbar && window.WsCards.toolbar();
      var prevFloat = bar ? bar.hasAttribute("data-floating") : null;
      var pid = ++anchorProgSeq;
      anchorProg = { key: key, id: pid, smooth: smooth, until: Date.now() + (smooth ? 1500 : 400), tries: 0, waitFloat: false, scrollDone: false, target: col.scrollTop };
      var moved = anchorAlign(col, n, smooth);
      if (!moved) { anchorProg = null; return true; }
      if (prevFloat !== null) anchorProg.waitFloat = (anchorProg.target > anchorSized.padTop + 1) !== prevFloat;
      if (!smooth) anchorWaitFrames(pid, anchorProg.waitFloat ? 5 : 2);
    }
    return true;
  }
  // step selects the card after (d = 1) or before (d = -1) the current one.
  function anchorStep(d) {
    var list = anchorCards(), at = list.indexOf(anchorCurrent());
    if (at < 0) { anchorSelect(list[d > 0 ? 0 : list.length - 1], { smooth: true }); return true; }
    var to = list[at + d];
    if (to) { anchorSelect(to, { smooth: true }); return true; }
    // Past the last card the board's tail (Show more closed topics, the
    // runway) is reachable; past the first, nothing.
    if (d > 0) { var col = $("bcol"); if (col) col.scrollTo({ top: col.scrollHeight, behavior: reducedMotion() ? "auto" : "smooth" }); }
    return false;
  }
  // fallback is the card to take when the current one leaves the order: the
  // next card, else the previous one (the list before the change).
  function anchorFallback(before, gone) {
    var at = before.indexOf(gone), list = anchorCards();
    for (var i = at + 1; i < before.length; i++) if (list.indexOf(before[i]) >= 0) return before[i];
    for (var j = at - 1; j >= 0; j--) if (list.indexOf(before[j]) >= 0) return before[j];
    return list[0] || null;
  }
  // settle keeps the selection consistent after a board change: the current
  // key is retained across insertion and reorder (the browser's anchoring
  // holds its place), the saved or first card is taken when none is current,
  // and a card that left the order hands over to a neighbour.
  // Only slot 0 and the first two rail-bearing slots carry explicit cap
  // offsets; rail slot 2+ use the CSS default (padTop + 44px) so non-current
  // updates touch no DOM. When slot 0 is a non-rail .bcard, the first railed
  // slot carries "rail0" (padTop + 0px) instead of falling through to 44px.
  function anchorSlotCaps(list) {
    var cards = list || anchorCards(), next0 = null, nextR0 = null, next1 = null, seen = 0;
    for (var i = 0; i < cards.length; i++) {
      var c = cards[i], rail = c.hasAttribute("data-topic") || c.hasAttribute("data-rail");
      if (i === 0) next0 = c;
      else if (rail && seen === 0) nextR0 = c;
      else if (rail && seen === 1) { next1 = c; break; }
      if (rail) seen++;
    }
    var prev = [anchorCap0, anchorCapR0, anchorCap1];
    for (var j = 0; j < prev.length; j++) {
      var p = prev[j];
      if (p && p !== next0 && p !== nextR0 && p !== next1) p.removeAttribute("data-anchor-cap");
    }
    if (next0 && (!next0.getAttribute || next0.getAttribute("data-anchor-cap") !== "0")) next0.setAttribute("data-anchor-cap", "0");
    if (nextR0 && (!nextR0.getAttribute || nextR0.getAttribute("data-anchor-cap") !== "rail0")) nextR0.setAttribute("data-anchor-cap", "rail0");
    if (next1 && (!next1.getAttribute || next1.getAttribute("data-anchor-cap") !== "1")) next1.setAttribute("data-anchor-cap", "1");
    anchorCap0 = next0;
    anchorCapR0 = nextR0;
    anchorCap1 = next1;
  }
  function anchorSettle(before) {
    var list = anchorCards(), n = anchorNode(anchorKey);
    anchorSlotCaps(list);
    if (n) { if (n !== anchorLast) anchorMark(n); return; }
    if (!list.length) { anchorMark(null); return; }
    var saved = null;
    if (anchorKey === null) try { saved = window.sessionStorage.getItem(ANCHOR_STORE); } catch (e) {}
    var key = saved !== null ? saved : null, next = key !== null ? anchorNode(key) : null;
    if (!next && before && anchorLast) next = anchorFallback(before, anchorLast);
    anchorSelect(next || list[0], { keep: true, still: anchorKey === null && !next });
  }
  // jump resolves a board topic or card to its containing card, then lets the
  // caller scroll inside it.
  function anchorOwner(n) {
    for (var p = n; p && p.nodeType === 1 && p !== doc.body; p = p.parentNode) if (anchorId(p) !== null && anchorCards().indexOf(p) >= 0) return p;
    return null;
  }
  // Exposure of a node for staging: inside the current card, clipped to the
  // card and to the usable board scrollport below the sticky toolbar. The
  // view is measured once per staging pass (the board snapshot carries it).
  function anchorView() {
    var snap = boardSnap;
    if (snap && snap.anchorView !== undefined) return snap.anchorView;
    var col = $("bcol"), cur = anchorCurrent(), v = null;
    if (col && cur) {
      var c = col.getBoundingClientRect(), r = cur.getBoundingClientRect();
      v = { card: cur, top: Math.max(anchorLandingTop(col, c, cur), r.top), bottom: Math.min(c.bottom - anchorBotReserve(), r.bottom, window.innerHeight || Infinity),
        left: Math.max(c.left, r.left), right: Math.min(c.right, r.right) };
    }
    if (snap) snap.anchorView = v;
    return v;
  }
  function anchorExposed(n) {
    var v = anchorView(), w = window.wbHistory;
    if (!v || !(n === v.card || v.card.contains(n))) return false;
    if (w && w.rendered && !(boardSnap && boardSnap.rendered ? boardSnap.rendered(n) : w.rendered(n))) return false;
    var r = n.getBoundingClientRect();
    return r.height > 0 && Math.min(v.bottom, r.bottom) - Math.max(v.top, r.top) >= 1 && Math.min(v.right, r.right) - Math.max(v.left, r.left) >= 1;
  }
  // --- sizes: the usable scrollport ---------------------------------------------
  // ResizeObserver entries carry the sizes, so no layout is forced to read
  // them; --board-usable clears both top 2-row rail stack and bottom 2-row
  // rail stack (+ reply bar when visible), and --board-runway keeps the last
  // card reachable at the top rail landing line.
  function anchorSetVar(s, k, v) { if (!s.getPropertyValue || s.getPropertyValue(k) !== v) s.setProperty(k, v); }
  function anchorApplySizes(col) {
    var inset = anchorRailInset(col), topSpace = inset + anchorCapH(2), botSpace = anchorBotReserve();
    anchorSized.inset = inset;
    var s = col.style;
    anchorSetVar(s, "--board-toolbar", topSpace + "px");
    anchorSetVar(s, "--board-pad-top", anchorSized.padTop + "px");
    if (!anchorSized.bounded) {
      if (s.removeProperty) { s.removeProperty("--board-usable"); s.removeProperty("--board-runway"); }
      return;
    }
    var usable = Math.max(120, anchorSized.h - topSpace - botSpace);
    anchorSetVar(s, "--board-usable", usable + "px");
    anchorSetVar(s, "--board-runway", Math.max(0, anchorSized.h - topSpace - (anchorSized.last || 0)) + "px");
  }
  function anchorObserveAux(col) {
    if (!anchorRO || !col) return;
    var face = anchorToolbarFace(), reply = col.querySelector(".reply");
    if (face !== anchorFaceEl) {
      if (anchorFaceEl) anchorRO.unobserve(anchorFaceEl);
      anchorFaceEl = face;
      if (face) anchorRO.observe(face);
    }
    if (reply !== anchorReplyEl) {
      if (anchorReplyEl) anchorRO.unobserve(anchorReplyEl);
      anchorReplyEl = reply;
      anchorSized.reply = 0;
      if (reply) anchorRO.observe(reply);
    }
  }
  function anchorRealign(fromInsetOrFrame) {
    var p = anchorProg;
    if (!p || p.key !== anchorKey || Date.now() > p.until) { anchorProg = null; return; }
    var col = $("bcol"), n = anchorNode(p.key);
    if (!col || !n || !n.isConnected) return;
    var moved = anchorAlign(col, n, !p.scrollDone && p.smooth);
    if (moved) p.scrollDone = false;
    if (fromInsetOrFrame) {
      p.waitFloat = false;
      if (p.scrollDone && !moved) anchorProg = null;
    }
  }
  function anchorSize(entries) {
    var col = $("bcol");
    if (!col) return;
    anchorObserveAux(col);
    var face = anchorFaceEl, reply = anchorReplyEl, insetChanged = false;
    entries.forEach(function (e) {
      var box = e.contentBoxSize && e.contentBoxSize[0], bb = e.borderBoxSize && e.borderBoxSize[0];
      if (e.target === col) {
        var cs = getComputedStyle(col);
        anchorSized.bounded = /auto|scroll/.test(cs.overflowY);
        anchorSized.w = box ? box.inlineSize : e.contentRect.width;
        anchorSized.h = anchorSized.bounded ? (box ? box.blockSize : e.contentRect.height) : 0;
        anchorSized.padTop = parseFloat(cs.paddingTop) || 0;
      } else if (e.target === face) {
        var nextBar = bb ? bb.blockSize : e.contentRect.height;
        if (nextBar !== anchorSized.bar) insetChanged = true;
        anchorSized.bar = nextBar;
      } else if (e.target === anchorLastCard) {
        anchorSized.last = bb ? bb.blockSize : e.contentRect.height;
      } else if (e.target === reply) {
        anchorSized.reply = bb ? bb.blockSize : e.contentRect.height;
      }
    });
    anchorApplySizes(col);
    if (insetChanged && anchorProg) anchorRealign(false);
  }
  var anchorLastCard = null;
  function anchorWatchLast() {
    if (!anchorRO) return;
    var list = anchorCards(), last = list[list.length - 1] || null;
    if (last === anchorLastCard) return;
    if (anchorLastCard) anchorRO.unobserve(anchorLastCard);
    anchorLastCard = last;
    anchorSized.last = 0;
    if (last) anchorRO.observe(last);
  }
  function anchorCancelProg() { anchorProg = null; }
  function anchorOnKey(e) {
    var k = e && e.key;
    if (k === "ArrowUp" || k === "ArrowDown" || k === "PageUp" || k === "PageDown" || k === "Home" || k === "End" || k === " ") anchorProg = null;
  }
  // A native scroll (wheel chaining across cards, touch, keys, the scrollbar)
  // ends on the active rail card, matching rail.js's cur band. Programmatic
  // anchorSelect runs are guarded so toolbar float settling never snaps back
  // to an earlier short or folded card.
  function anchorScrollEnd(e) {
    var col = $("bcol");
    if (e.target !== col) return;
    var p = anchorProg;
    if (p && p.key === anchorKey && Date.now() <= p.until) {
      if (!p.smooth && Math.abs(col.scrollTop - p.target) > 120) { anchorProg = null; }
      else {
      var targetNode = anchorNode(p.key) || anchorLast;
      if (targetNode && targetNode.isConnected) {
        var err = 0, canMove = false;
        if (anchorSized.bounded && targetNode.getAttribute && targetNode.getAttribute("data-anchor-cap") === "0") {
          err = -col.scrollTop;
          canMove = col.scrollTop > 1;
        } else {
          var c = col.getBoundingClientRect(), r = targetNode.getBoundingClientRect();
          err = r.top - anchorLandingTop(col, c, targetNode);
          canMove = err > 0 ? col.scrollTop + col.clientHeight < col.scrollHeight - 1 : col.scrollTop > 1;
        }
        if (Math.abs(err) > 1.5 && canMove && p.tries < 2) {
          p.tries++;
          anchorAlign(col, targetNode, false);
          return;
        }
      }
      p.scrollDone = true;
      if (!p.waitFloat) anchorProg = null;
      else anchorWaitFrames(p.id, 3);
      return;
      }
    }
    anchorProg = null;
    var list = anchorCards();
    if (!list.length) return;
    var colRect = col.getBoundingClientRect();
    var vt = colRect.top + (col.clientTop || 0) + anchorSized.padTop + anchorRailInset(col);
    var rects = list.map(function (n) { return n.getBoundingClientRect(); });
    var railCount = 0, pick = -1, closest = null, gap = Infinity;
    for (var k = 0; k < list.length; k++) {
      var n = list[k], rk = rects[k];
      if (!rk.height) { if (n.hasAttribute("data-topic") || n.hasAttribute("data-rail")) railCount++; continue; }
      var cap = n.hasAttribute("data-topic") || n.hasAttribute("data-rail") ? anchorCapH(Math.min(2, railCount)) : 0;
      if (n.hasAttribute("data-topic") || n.hasAttribute("data-rail")) railCount++;
      var line = vt + cap;
      if (rk.top <= line + 1.5) pick = k;
      var d = Math.abs(rk.top - line);
      if (d < gap) { gap = d; closest = n; }
    }
    var best = pick >= 0 && rects[pick].bottom > vt + anchorCardCap(list[pick], list) + 1.5 ? list[pick] : closest;
    if (best && best !== anchorLast) anchorMark(best);
  }
  function anchorInit() {
    var col = $("bcol");
    if (!col || !region("board")) return;
    var colCs = getComputedStyle(col);
    anchorSized.padTop = parseFloat(colCs.paddingTop) || 0;
    anchorSized.bounded = /auto|scroll/.test(colCs.overflowY);
    anchorSized.inset = anchorRailInset(col);
    col.addEventListener("wheel", anchorCancelProg, { passive: true });
    col.addEventListener("pointerdown", anchorCancelProg, { passive: true });
    col.addEventListener("touchstart", anchorCancelProg, { passive: true });
    col.addEventListener("keydown", anchorOnKey, { passive: true });
    col.addEventListener("scrollend", anchorScrollEnd, { passive: true });
    if (window.ResizeObserver) {
      anchorRO = new ResizeObserver(anchorSize);
      anchorRO.observe(col);
      anchorObserveAux(col);
    }
    if (window.MutationObserver) {
      anchorMO = new MutationObserver(function () {
        var nextInset = anchorRailInset(col);
        if (nextInset === anchorSized.inset) return;
        anchorSized.inset = nextInset;
        anchorApplySizes(col);
        if (anchorProg) anchorRealign(true);
      });
      anchorMO.observe(col, { attributes: true, attributeFilter: ["style"] });
    }
    col.wbScrollOwner = true;
    anchorSettle(null);
    anchorWatchLast();
    window.WsBoardAnchor = { restore: function () { var n = anchorCurrent(); if (n) anchorSelect(n, { keep: true }); }, select: anchorSelect, step: anchorStep, owner: anchorOwner, key: function () { return anchorKey; }, cards: anchorCards, settle: function () { anchorSettle(null); anchorWatchLast(); } };
  }
