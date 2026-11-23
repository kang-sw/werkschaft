  // --- board card anchor ------------------------------------------------------
  // The Main Board scrolls by card, not by pixel. One card is the topic section
  // (section.topic[data-topic]) or an unpinned card (.bcard[data-card]) the
  // reader stands on: its key ("t:" topic, "c:" id) is the whole outer scroll
  // state, its top aligned with the usable scrollport top below the sticky
  // toolbar (scroll-margin-top, --board-toolbar). Cards keep their content
  // height up to --board-usable and scroll inside themselves; overflow
  // anchoring is scoped to the current card (dashboard-board.css), so a size
  // change of any card reads and writes nothing on the outer board. Geometry
  // is read only by an explicit user move (selecting, a native scroll's end).
  var ANCHOR_STORE = "ws.dashboard.board-card." + location.pathname + location.search;
  var anchorKey = null, anchorSized = { w: 0, h: 0, bar: 0 }, anchorWheelGate = null, anchorLast = null, anchorRO = null;
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
    anchorControls();
  }
  // select makes node n the current card and aligns it. The one alignment
  // write is the browser's scrollIntoView; no geometry is read here. Leaving a
  // card with staged updates admits the newest revision first.
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
      var calm = reducedMotion();
      n.scrollIntoView({ block: "start", behavior: opt && opt.smooth && !calm ? "smooth" : "auto" });
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
  function anchorSettle(before) {
    var list = anchorCards(), n = anchorNode(anchorKey);
    if (n) { if (n !== anchorLast) anchorMark(n); anchorControls(); return; }
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
      v = { card: cur, top: Math.max(c.top + col.clientTop + anchorSized.bar + 10, r.top), bottom: Math.min(c.bottom, r.bottom, window.innerHeight || Infinity),
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
  // --- toolbar controls --------------------------------------------------------
  var anchorBtns = null;
  function anchorControls() {
    if (!anchorBtns) return;
    var list = anchorCards(), at = list.indexOf(anchorLast);
    anchorBtns.prev.disabled = at <= 0;
    anchorBtns.next.disabled = at < 0 || at >= list.length - 1;
  }
  function anchorMount() {
    var tools = window.WsCards && window.WsCards.toolbar && window.WsCards.toolbar();
    tools = tools && tools.querySelector(".board-tools");
    if (!tools || anchorBtns) return;
    var group = doc.createElement("span");
    group.className = "board-step";
    function btn(cls, label, path, d) {
      var b = doc.createElement("button");
      b.type = "button"; b.className = "board-action board-step-" + cls;
      b.title = label; b.setAttribute("aria-label", label);
      b.innerHTML = '<svg class="li" viewBox="0 0 24 24" aria-hidden="true"><path d="' + path + '"/></svg>';
      b.addEventListener("click", function () { anchorStep(d); });
      group.appendChild(b);
      return b;
    }
    anchorBtns = { prev: btn("prev", wsT("js.board.step.prev"), "M18 15l-6-6-6 6", -1), next: btn("next", wsT("js.board.step.next"), "M6 9l6 6 6-6", 1) };
    tools.insertBefore(group, tools.firstChild);
    anchorControls();
  }
  // --- sizes: the usable scrollport ---------------------------------------------
  // ResizeObserver entries carry the sizes, so no layout is forced to read
  // them; the trailing runway derives from the usable height and the last card.
  function anchorSize(entries) {
    var col = $("bcol"), bar = window.WsCards && window.WsCards.toolbar && window.WsCards.toolbar();
    if (!col) return;
    entries.forEach(function (e) {
      var box = e.contentBoxSize && e.contentBoxSize[0], bb = e.borderBoxSize && e.borderBoxSize[0];
      if (e.target === col) { anchorSized.w = box ? box.inlineSize : e.contentRect.width; anchorSized.h = box ? box.blockSize : e.contentRect.height; }
      else if (e.target === bar) anchorSized.bar = bb ? bb.blockSize : e.contentRect.height;
      else if (e.target === anchorLastCard) anchorSized.last = bb ? bb.blockSize : e.contentRect.height;
    });
    var gap = anchorSized.bar + 10, usable = Math.max(120, anchorSized.h - gap);
    var s = col.style;
    s.setProperty("--board-toolbar", gap + "px");
    s.setProperty("--board-usable", usable + "px");
    s.setProperty("--board-runway", Math.max(0, usable - (anchorSized.last || 0)) + "px");
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
  // --- gestures -----------------------------------------------------------------
  // A wheel over a card that can still scroll inside goes to the card. At its
  // edge (or over a short card) the first gesture stops there; the next
  // gesture in the same direction selects the adjacent card. WsGallery.Gate
  // gives the pinned gallery's momentum and gesture timing; its charge alone
  // would cross in one continuous burst from an edge, so a burst that began at
  // the edge (no gap of G.C.INERTIA_MS before it) never moves the board.
  var ANCHOR_OWN = '.xterm, .tm-host, iframe, canvas, video, embed, object, [data-max], [data-max] *';
  function anchorCanScroll(t, dy, col) {
    for (var n = t; n && n !== col; n = n.parentNode) {
      if (n.nodeType !== 1 || !(n.scrollHeight - n.clientHeight > 1)) continue;
      var oy = n === doc.scrollingElement ? "" : getComputedStyle(n).overflowY;
      if (!/auto|scroll/.test(oy)) continue;
      if (dy > 0 ? n.scrollTop + n.clientHeight < n.scrollHeight - 1 : n.scrollTop > 1) return true;
    }
    return false;
  }
  var anchorBurst = { dir: 0, last: -Infinity, stop: 0, go: false, lock: -Infinity };
  function anchorWheel(e) {
    if (e.defaultPrevented || e.ctrlKey || e.deltaY === 0 || !e.target.closest) return;
    if (window.WsOverview && window.WsOverview.holding && window.WsOverview.holding()) return;
    if (doc.documentElement.hasAttribute("data-overview")) return;
    var col = $("bcol"), G = window.WsGallery;
    if (!col || !G || e.target.closest(ANCHOR_OWN) || !e.target.closest("#r-board")) return;
    var dy = +e.deltaY || 0;
    if (e.deltaMode === 1) dy *= 40; else if (e.deltaMode === 2) dy *= 800;
    var dir = dy > 0 ? 1 : -1, now = Date.now(), b = anchorBurst, fresh = now - b.last >= G.C.INERTIA_MS || b.dir !== dir;
    b.last = now; b.dir = dir;
    if (anchorCanScroll(e.target, dy, col)) { b.stop = 0; return; }
    e.preventDefault();
    if (now < b.lock) return;
    // A gesture that reaches or starts at the edge stops there; only a fresh
    // gesture in the same direction after it moves one card.
    if (fresh) {
      if (b.stop === dir) { b.stop = 0; b.lock = now + G.C.LOCK_MS; anchorStep(dir); }
      else b.stop = dir;
    }
  }
  // A native scroll (touch, keys, the scrollbar) ends on the nearest card, which
  // becomes current: the one geometry pass of a user move.
  function anchorScrollEnd(e) {
    var col = $("bcol");
    if (e.target !== col) return;
    var list = anchorCards(), top = col.getBoundingClientRect().top + anchorSized.bar + 10, best = null, gap = Infinity;
    list.forEach(function (n) {
      var d = Math.abs(n.getBoundingClientRect().top - top);
      if (d < gap) { gap = d; best = n; }
    });
    if (best && best !== anchorLast) anchorMark(best);
  }
  function anchorInit() {
    var col = $("bcol");
    if (!col || !region("board")) return;
    col.addEventListener("wheel", anchorWheel, { passive: false });
    col.addEventListener("scrollend", anchorScrollEnd, { passive: true });
    if (window.ResizeObserver) {
      anchorRO = new ResizeObserver(anchorSize);
      anchorRO.observe(col);
      var bar = window.WsCards && window.WsCards.toolbar && window.WsCards.toolbar();
      if (bar) anchorRO.observe(bar);
    }
    col.wbScrollOwner = true;
    anchorMount();
    anchorSettle(null);
    anchorWatchLast();
    window.WsBoardAnchor = { restore: function () { var n = anchorCurrent(); if (n) n.scrollIntoView({ block: "start" }); }, select: anchorSelect, step: anchorStep, owner: anchorOwner, key: function () { return anchorKey; }, cards: anchorCards };
  }
