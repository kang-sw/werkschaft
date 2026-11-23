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
  var ANCHOR_CAP = 17, ANCHOR_GAP = 3, ANCHOR_PAD = 4;
  var ANCHOR_BOT_0 = 6, ANCHOR_BOT_1 = 27, ANCHOR_BOT_2 = 47, ANCHOR_CARD_GAP_EXTRA = 12;
  var anchorKey = null, anchorSized = { w: 0, h: 0, bar: 0, last: 0, reply: 0, padTop: 0, inset: 0, bounded: false };
  var anchorLast = null, anchorRO = null, anchorMO = null, anchorFaceEl = null, anchorReplyEl = null;
  var anchorCap0 = null, anchorCapR0 = null, anchorCap1 = null, anchorBotNodes = [], anchorLastNode = null;
  var anchorProg = null, anchorProgSeq = 0, anchorGlideAnim = null, anchorWheelCtrl = null;
  var WHEEL_DEFAULTS = {
    threshold: 52,
    peekPx: 0,
    wallGuardMs: 140,
    chainWindowMs: 360,
    minStepMs: 165,
    animMs: 220,
    idleGapMs: 140,
    discretePx: 48,
    discreteGapMs: 60
  };
  var WHEEL_OWN = '.xterm, .tm-host, iframe:not(.doc-frame), canvas, video, embed, object, [data-max], [data-max] *, .bcard:is([data-kind="terminal"], [data-kind="web"]) > .bc-in > .bc-body';
  // Pure wheel-virtualized deck state machine (Preset A: Crisp Instant Glide).
  // Exposed as window.WsBoardWheel for deterministic unit testing without DOM.
  function createBoardWheel(opts) {
    var cfg = {};
    Object.keys(WHEEL_DEFAULTS).forEach(function (k) {
      cfg[k] = opts && opts[k] !== undefined ? opts[k] : WHEEL_DEFAULTS[k];
    });
    var state = "IDLE", accum = 0, minAbs = Infinity;
    var lastEvtAt = 0, lastCommitAt = -1e9, lastDir = 0;
    var coastDir = 0, wallDir = 0, lastInnerAt = -1e9, lastInnerDir = 0;
    function reset() {
      state = "IDLE"; accum = 0; minAbs = Infinity;
      lastEvtAt = 0; lastCommitAt = -1e9; lastDir = 0;
      coastDir = 0; wallDir = 0; lastInnerAt = -1e9; lastInnerDir = 0;
    }
    function commit(dir, now, abs) {
      state = "COAST_ABSORB";
      coastDir = dir;
      lastDir = dir;
      lastCommitAt = now;
      minAbs = abs;
      accum = 0;
      return { action: "commit", dir: dir };
    }
    function step(ev) {
      if (!ev) return { action: "ignore" };
      var dy = +ev.dy || 0, dx = +ev.dx || 0;
      var scale = ev.deltaMode === 1 ? 40 : ev.deltaMode === 2 ? 800 : 1;
      dy *= scale;
      dx *= scale;
      if (!dy || Math.abs(dy) < 0.01) return { action: "ignore" };
      if (Math.abs(dx) > Math.abs(dy) * 1.15) return { action: "ignore" };
      var now = typeof ev.now === "number" ? ev.now : Date.now();
      var dir = dy > 0 ? 1 : -1, abs = Math.abs(dy);
      var dt = lastEvtAt ? now - lastEvtAt : 9999;
      lastEvtAt = now;
      var isDiscrete = (ev.deltaMode !== 0 && ev.deltaMode != null) || (abs >= cfg.discretePx && dt >= cfg.discreteGapMs);
      if (dt >= cfg.idleGapMs) {
        if (state === "WALL_ABSORB" || state === "COAST_ABSORB") {
          state = "IDLE"; minAbs = Infinity;
        }
        accum = 0;
      }
      var inChain = (now - lastCommitAt) <= cfg.chainWindowMs && dir === lastDir;
      if (state === "WALL_ABSORB" && dir !== wallDir) {
        state = "IDLE"; accum = 0; minAbs = Infinity;
      }
      if (state === "COAST_ABSORB") {
        if (dir !== coastDir) {
          state = "IDLE"; accum = 0; minAbs = Infinity;
        } else {
          if (abs < minAbs) minAbs = abs;
          var spiked = (isDiscrete || abs > Math.max(minAbs * 1.8, minAbs + 10)) && (now - lastCommitAt >= cfg.minStepMs);
          if (!spiked) return { action: "absorb", reason: "coast" };
          state = "IDLE"; accum = 0; minAbs = Infinity;
        }
      }
      if (ev.canInnerScroll && !inChain) {
        lastInnerAt = now;
        lastInnerDir = dir;
        state = "IDLE";
        accum = 0;
        return { action: "inner" };
      }
      if (state === "IDLE" && !inChain && !isDiscrete && (now - lastInnerAt) <= cfg.wallGuardMs && dir === lastInnerDir) {
        state = "WALL_ABSORB";
        wallDir = dir;
        minAbs = abs;
        accum = 0;
        return { action: "absorb", reason: "wall" };
      }
      if (state === "WALL_ABSORB") {
        if (abs < minAbs) minAbs = abs;
        if (isDiscrete || abs > Math.max(minAbs * 1.8, minAbs + 12)) {
          state = "IDLE"; accum = 0; minAbs = Infinity;
        } else {
          return { action: "absorb", reason: "wall" };
        }
      }
      if ((dir < 0 && ev.atFirst) || (dir > 0 && ev.atLast)) {
        accum = 0;
        return { action: "edge", dir: dir };
      }
      if (isDiscrete) {
        if (now - lastCommitAt >= cfg.minStepMs || dir !== lastDir) return commit(dir, now, abs);
        return { action: "absorb", reason: "rate" };
      }
      if ((accum > 0 && dir < 0) || (accum < 0 && dir > 0)) accum = 0;
      accum += dy;
      var effThreshold = inChain ? Math.round(cfg.threshold * 0.65) : cfg.threshold;
      if (Math.abs(accum) >= effThreshold) return commit(dir, now, abs);
      return { action: "charge", dir: dir, progress: Math.min(1, Math.abs(accum) / effThreshold) };
    }
    return {
      step: step,
      reset: reset,
      config: function () { return Object.assign({}, cfg); },
      state: function () { return state; }
    };
  }
  window.WsBoardWheel = { defaults: Object.assign({}, WHEEL_DEFAULTS), create: createBoardWheel };
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
  function anchorBotReserve(n) {
    var base = n === 0 ? ANCHOR_BOT_0 : n === 1 ? ANCHOR_BOT_1 : ANCHOR_BOT_2;
    return base + (anchorSized.reply ? anchorSized.reply + 14 : 0);
  }
  var anchorGlideRealign = false;
  function anchorCancelGlide() {
    anchorGlideRealign = false;
    if (anchorGlideAnim) {
      try { anchorGlideAnim.cancel(); } catch (e) {}
      anchorGlideAnim = null;
    }
  }
  function anchorGlide(n, dir) {
    anchorCancelGlide();
    if (!n || !dir || !n.animate || reducedMotion()) return;
    if (doc.documentElement.hasAttribute("data-board-max") || doc.documentElement.hasAttribute("data-overview")) return;
    var dist = Math.min(72, Math.max(32, Math.round((anchorSized.h || 600) * 0.08)));
    var y0 = dir > 0 ? dist : -dist;
    try {
      var anim = n.animate(
        [{ transform: "translate3d(0," + y0 + "px,0)" }, { transform: "translate3d(0,0,0)" }],
        { duration: WHEEL_DEFAULTS.animMs, easing: "cubic-bezier(.22, 1, .36, 1)", fill: "none" }
      );
      anchorGlideAnim = anim;
      var finish = function () {
        if (anchorGlideAnim !== anim) return;
        anchorGlideAnim = null;
        if (anchorGlideRealign) { anchorGlideRealign = false; anchorRealign(true); }
      };
      var cancel = function () {
        if (anchorGlideAnim !== anim) return;
        anchorGlideAnim = null;
        anchorGlideRealign = false;
      };
      if (anim.addEventListener) {
        anim.addEventListener("finish", finish);
        anim.addEventListener("cancel", cancel);
      } else {
        anim.onfinish = finish;
        anim.oncancel = cancel;
      }
    } catch (e) {}
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
    if (typeof boardDialMark === "function") boardDialMark(n);
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
    var prevNode = anchorLast;
    var left = anchorKey !== null && anchorKey !== key;
    if (anchorKey !== key || anchorLast !== n) anchorMark(n);
    if (left && !(opt && opt.keep) && Object.keys(boardPending).length) {
      boardFlushViewed(true);
      n = anchorNode(key) || n;
    }
    if (!(opt && opt.still)) {
      var calm = reducedMotion(), wantSmooth = !!(opt && opt.smooth && !calm);
      var glideDir = opt && opt.glide ? opt.glide : 0;
      if (anchorSized.bounded && !glideDir && wantSmooth && prevNode && prevNode !== n) {
        var list = anchorCards(), fromIdx = list.indexOf(prevNode), toIdx = list.indexOf(n);
        if (fromIdx >= 0 && toIdx >= 0 && fromIdx !== toIdx) glideDir = toIdx > fromIdx ? 1 : -1;
      }
      var smooth = anchorSized.bounded ? false : wantSmooth;
      var bar = window.WsCards && window.WsCards.toolbar && window.WsCards.toolbar();
      var prevFloat = bar ? bar.hasAttribute("data-floating") : null;
      var pid = ++anchorProgSeq;
      anchorCancelGlide();
      anchorProg = { key: key, id: pid, smooth: smooth, until: Date.now() + (smooth ? 1500 : 400), tries: 0, waitFloat: false, scrollDone: false, target: col.scrollTop };
      var moved = anchorAlign(col, n, smooth);
      if (glideDir && !calm) anchorGlide(n, glideDir);
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
    // Stamp data-anchor-bot="0|1" on trailing edge cards (O(1) DOM writes)
    // so each card spans down to its actual bottom capsule stack (0, 1, or 2).
    var nextBots = [], botMap = new Map(), afterRail = 0;
    for (var k = cards.length - 1; k >= 0; k--) {
      var ck = cards[k];
      if (afterRail < 2) {
        var val = String(afterRail);
        nextBots.push(ck);
        botMap.set(ck, val);
      }
      if (ck.hasAttribute("data-topic") || ck.hasAttribute("data-rail")) {
        afterRail++;
        if (afterRail >= 2) break;
      }
    }
    for (var m = 0; m < anchorBotNodes.length; m++) {
      var pb = anchorBotNodes[m];
      if (pb && !botMap.has(pb)) pb.removeAttribute("data-anchor-bot");
    }
    botMap.forEach(function (val, node) {
      if (!node.getAttribute || node.getAttribute("data-anchor-bot") !== val) node.setAttribute("data-anchor-bot", val);
    });
    anchorBotNodes = nextBots;
    var lastNode = cards.length ? cards[cards.length - 1] : null;
    if (anchorLastNode && anchorLastNode !== lastNode) anchorLastNode.removeAttribute("data-anchor-last");
    if (lastNode && (!lastNode.getAttribute || lastNode.getAttribute("data-anchor-last") !== "1")) lastNode.setAttribute("data-anchor-last", "1");
    anchorLastNode = lastNode;
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
    var inset = anchorRailInset(col), topSpace = inset + anchorCapH(2);
    var bot0 = anchorBotReserve(0), bot1 = anchorBotReserve(1), bot2 = anchorBotReserve(2);
    anchorSized.inset = inset;
    var s = col.style;
    anchorSetVar(s, "--board-toolbar", topSpace + "px");
    anchorSetVar(s, "--board-pad-top", anchorSized.padTop + "px");
    if (!anchorSized.bounded) {
      if (col.removeAttribute) col.removeAttribute("data-board-bounded");
      if (s.removeProperty) {
        s.removeProperty("--board-h");
        s.removeProperty("--board-top-0");
        s.removeProperty("--board-top-rail0");
        s.removeProperty("--board-top-1");
        s.removeProperty("--board-top-2");
        s.removeProperty("--board-bot-0");
        s.removeProperty("--board-bot-1");
        s.removeProperty("--board-bot-2");
        s.removeProperty("--board-card-gap");
        s.removeProperty("--board-usable");
        s.removeProperty("--board-runway");
      }
      return;
    }
    if (col.setAttribute && (!col.hasAttribute || !col.hasAttribute("data-board-bounded"))) col.setAttribute("data-board-bounded", "");
    anchorSetVar(s, "--board-h", anchorSized.h + "px");
    anchorSetVar(s, "--board-top-0", (inset + 4) + "px");
    anchorSetVar(s, "--board-top-rail0", inset + "px");
    anchorSetVar(s, "--board-top-1", (inset + anchorCapH(1)) + "px");
    anchorSetVar(s, "--board-top-2", topSpace + "px");
    anchorSetVar(s, "--board-bot-0", bot0 + "px");
    anchorSetVar(s, "--board-bot-1", bot1 + "px");
    anchorSetVar(s, "--board-bot-2", bot2 + "px");
    anchorSetVar(s, "--board-card-gap", (bot2 + ANCHOR_CARD_GAP_EXTRA) + "px");
    var usable = Math.max(120, anchorSized.h - topSpace - bot2);
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
    if (anchorGlideAnim) { anchorGlideRealign = true; return; }
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
    var face = anchorFaceEl, reply = anchorReplyEl, insetChanged = false, prevBounded = anchorSized.bounded;
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
    if (anchorSized.bounded !== prevBounded && typeof boardDialSync === "function") boardDialSync();
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
  function anchorCanReach(el, dy) {
    if (!el || el.scrollHeight <= el.clientHeight + 2) return false;
    return dy > 0 ? el.scrollTop + el.clientHeight < el.scrollHeight - 1.5 : el.scrollTop > 1.5;
  }
  function anchorCanInnerScroll(target, card, dy) {
    if (!card) return false;
    for (var n = target; n && n.nodeType === 1; n = n.parentNode) {
      if (n === card) {
        if (card.hasAttribute("data-topic")) return anchorCanReach(card, dy);
        var body = card.querySelector ? card.querySelector(":scope > .bc-in > .bc-body") : null;
        return anchorCanReach(body, dy);
      }
      if (n.classList && n.classList.contains("bc-body")) return anchorCanReach(n, dy);
      if (n.scrollHeight > n.clientHeight + 2) {
        var cs = getComputedStyle(n);
        if (/auto|scroll/.test(cs.overflowY)) {
          if (anchorCanReach(n, dy)) return true;
          if (/contain|none/.test(cs.overscrollBehaviorY)) return false;
        }
      }
    }
    return false;
  }
  function anchorHandleWheel(target, card, rawDy, rawDx, deltaMode, prevent) {
    anchorProg = null;
    if (!anchorSized.bounded) return;
    if (doc.documentElement.hasAttribute("data-overview") || doc.documentElement.hasAttribute("data-board-max")) return;
    var list = anchorCards();
    if (!list.length) return;
    var cur = anchorCurrent() || list[0];
    var at = list.indexOf(cur);
    if (at < 0) at = 0;
    if (!card || list.indexOf(card) < 0) card = cur;
    var dy = +rawDy || 0;
    if (deltaMode === 1) dy *= 40;
    else if (deltaMode === 2) dy *= 800;
    var canInner = anchorCanInnerScroll(target || card, card, dy);
    if (!anchorWheelCtrl) anchorWheelCtrl = createBoardWheel();
    var now = window.performance && window.performance.now ? window.performance.now() : Date.now();
    var res = anchorWheelCtrl.step({
      dy: rawDy,
      dx: rawDx,
      deltaMode: deltaMode,
      now: now,
      canInnerScroll: canInner,
      atFirst: at <= 0,
      atLast: at >= list.length - 1
    });
    if (res.action === "ignore" || res.action === "inner" || res.action === "edge") return;
    if (prevent) prevent();
    if (res.action === "commit") {
      var next = list[at + res.dir];
      if (next) anchorSelect(next, { smooth: false, glide: res.dir });
    }
  }
  function anchorOnWheel(e) {
    if (e.defaultPrevented || e.ctrlKey) return;
    var t = e.target;
    if (t && t.closest && (t.closest(WHEEL_OWN) || t.closest(".tpop, [popover]:popover-open, dialog[open]"))) return;
    var card = t && t.closest ? t.closest("section.topic[data-topic], .bcard[data-card]") : null;
    anchorHandleWheel(t, card, e.deltaY, e.deltaX, e.deltaMode, function () { e.preventDefault(); });
  }
  function anchorOnFrameWheel(frame, dy, dx, mode) {
    if (!frame || !frame.isConnected) return;
    var col = $("bcol");
    if (!col || !col.contains(frame)) return;
    var card = frame.closest ? frame.closest("section.topic[data-topic], .bcard[data-card]") : null;
    anchorHandleWheel(frame, card, dy, dx, mode || 0, null);
  }
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
      if (anchorGlideAnim) {
        p.scrollDone = true;
        anchorGlideRealign = true;
        return;
      }
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
    anchorWheelCtrl = createBoardWheel();
    col.addEventListener("wheel", anchorOnWheel, { passive: false, capture: true });
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
    if (typeof boardDialInit === "function") boardDialInit();
    window.WsBoardAnchor = {
      restore: function () { var n = anchorCurrent(); if (n) anchorSelect(n, { keep: true }); },
      select: anchorSelect,
      step: anchorStep,
      owner: anchorOwner,
      key: function () { return anchorKey; },
      cards: anchorCards,
      bounded: function () { return !!anchorSized.bounded; },
      cancelGlide: anchorCancelGlide,
      wheel: anchorWheelCtrl,
      onFrameWheel: anchorOnFrameWheel,
      settle: function () { anchorSettle(null); anchorWatchLast(); if (typeof boardDialSync === "function") boardDialSync(); },
      scrubbing: function () { return typeof dialScrubbing !== "undefined" && !!dialScrubbing; },
      dialWindow: typeof boardDialWindow === "function" ? boardDialWindow : null,
      dialTier: typeof boardDialTier === "function" ? boardDialTier : null
    };
  }
