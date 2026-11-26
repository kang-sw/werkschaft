  // Fresh frames retain only tab-local reading metadata, never DOM/output.
  var SESSION_VIEWS = "ws.dashboard.session-views", sessionViewReady = false;
  function sessionViews() {
    try {
      var rows = JSON.parse(window.sessionStorage.getItem(SESSION_VIEWS) || "[]");
      return Array.isArray(rows) ? rows.filter(function (r) { return r && typeof r.page === "string" && r.panes && typeof r.panes === "object"; }).slice(-50) : [];
    } catch (e) { return []; }
  }
  // The parts content-visibility may skip (dashboard-board.css).
  // The board's outer position is its current card's key (dashboard-board-anchor.js),
  // so a session view carries no board geometry.
  function sessionBoardAnchors() { return []; }
  function sessionConversationAnchor() {
    if (!convo || !visible(convo)) return null;
    if (atBottom()) return { bottom: true };
    var box = entriesBox(), edge = convo.getBoundingClientRect().top + convo.clientTop;
    var entries = box ? Array.from(box.children) : [];
    var n = entries.find(function (entry) { return entry.getBoundingClientRect().bottom > edge; });
    return n ? { entry: n.getAttribute("data-entry"), offset: n.getBoundingClientRect().top - edge } : null;
  }
  // shellMemo is the top shell's named memory (shell.js memo), which
  // outlives this frame when the shell evicts it, or this window's own when
  // no same-origin shell frames the page; null without shell.js.
  function shellMemo(name, maxEntries, maxSize) {
    var host = null;
    try { if (window.top !== window && window.top.WsShell && window.top.WsShell.memo) host = window.top.WsShell; } catch (e) { host = null; }
    if (!host && window.WsShell && window.WsShell.memo) host = window.WsShell;
    return host ? host.memo(name, maxEntries, maxSize) : null;
  }
  // The free canvas's measured card heights (dashboard-board-anchor.js
  // freeHeightCache) live in a shell-made table per session page, so a
  // reloaded frame lays its runway out with real heights rather than
  // FREE_FALLBACK_H. Called while the anchor module initializes.
  function boardHeightCache() {
    var m = shellMemo("ws.board-heights", 32, 0), t = m ? m.table(location.pathname) : new Map();
    if (t.size > 4096) t.clear();
    return t;
  }
  // The current card's offset from the board's scroll edge, kept beside the
  // card key (ANCHOR_STORE) for one reload: anchorSettle lands the restored
  // card at the same reading line (boardOffsetTake).
  function boardOffsetSave() {
    var m = shellMemo("ws.board-offset", 32, 0), col = $("bcol");
    if (!m || !col || anchorKey === null || !anchorLast || !freeActive || !freePos.has(anchorLast)) return;
    m.set(location.pathname, anchorKey + "\n" + Math.round(freePos.get(anchorLast) - col.scrollTop));
  }
  function boardOffsetTake(key) {
    var m = shellMemo("ws.board-offset", 32, 0), v = m ? m.get(location.pathname) : undefined;
    if (m) m.delete(location.pathname);
    if (typeof v !== "string") return null;
    var at = v.lastIndexOf("\n"), offset = Number(v.slice(at + 1));
    return at > 0 && v.slice(0, at) === key && Number.isFinite(offset) ? offset : null;
  }
  function saveSessionView() {
    if (shell || !sessionViewReady) return;
    boardOffsetSave();
    var panes = {};
    PANES.forEach(function (id) {
      var el = $(id), top = visible(el) ? el.scrollTop : paneScroll[id];
      if (Number.isFinite(top) && top >= 0) panes[id] = top;
    });
    var row = { page: location.pathname, panes: panes, top: doc.scrollingElement ? doc.scrollingElement.scrollTop : 0,
      board: sessionBoardAnchors(), conversation: sessionConversationAnchor() };
    var rows = sessionViews().filter(function (r) { return r.page !== row.page; });
    rows.push(row);
    try { window.sessionStorage.setItem(SESSION_VIEWS, JSON.stringify(rows.slice(-50))); } catch (e) { /* view state is optional */ }
  }
  function placeSessionScroll(el, top) {
    if (!el || !Number.isFinite(top) || top < 0) return;
    var w = window.wbHistory;
    if (el.wbScrollOwner || (w && w.keeping && w.keeping(el))) {
      // Identity missing: numeric fallback through the owner, not a content
      // anchor. This fallback cannot preserve a reading panel as layout grows.
      var ref = el.firstElementChild;
      if (!ref || !w || !w.place) return;
      var edge = el === doc.scrollingElement ? 0 : el.getBoundingClientRect().top + el.clientTop;
      w.place(ref, { offset: ref.getBoundingClientRect().top - edge + el.scrollTop - top });
    } else el.scrollTop = top;
  }
  function sessionBoardReloadPending() { return false; }
  function restoreSessionBoard(records) {
    var w = window.wbHistory;
    if (!w || !w.place || !Array.isArray(records)) return;
    records.forEach(function (r) {
      if (!r || !Number.isFinite(r.offset)) return;
      var s = r.scroller === "document" ? doc.scrollingElement : $(r.scroller);
      if (!visible(s)) return;
      var topic = boardBoxes().flatMap(function (box) { return q("section.topic[data-topic]", box); }).find(function (n) { return topicKey(n) === r.topic; });
      var n = topic && boardLivePanels(topic).find(function (p) { return boardPanelKey(p) === r.key; });
      if ((!n || !n.getBoundingClientRect().height) && /^(p|e)[1-9][0-9]*$/.test(r.key || "") && w.loadTarget) {
        (topic ? [topic] : boardBoxes()).some(function (box) { n = w.loadTarget(box, r.key, window.WsCards && window.WsCards.unfoldTopic); return !!n; });
      }
      n = n || topic;
      if (n) w.place(n, { readingOffset: r.offset });
      else placeSessionScroll(s, r.top);
    });
  }
  function restoreSessionConversation(saved) {
    if (!convo) return;
    var box = entriesBox(), n = saved && !saved.bottom && box && Array.from(box.children).find(function (entry) { return entry.getAttribute("data-entry") === saved.entry; });
    if (n && Number.isFinite(saved.offset)) {
      var edge = convo.getBoundingClientRect().top + convo.clientTop;
      var nextTop = convo.scrollTop + n.getBoundingClientRect().top - edge - saved.offset;
      if (typeof setConvoScrollTop === "function") setConvoScrollTop(nextTop);
      else convo.scrollTop = nextTop;
      paneScroll.convo = convo.scrollTop;
      stick = false;
      if (typeof queueSyncConvoBottom === "function") queueSyncConvoBottom();
    } else {
      // Missing/older window or bottom on leave: retain Phase 1's bottom follow.
      stick = true;
      pinConvo();
    }
  }
  function restoreSessionView() {
    if (shell) return;
    var saved = sessionViews().find(function (r) { return r.page === location.pathname; });
    // Explicit hash navigation outranks an old reading position.
    if (!saved || location.hash) { sessionViewReady = true; return; }
    // Snapshot this before boardRestoreReload's earlier rAF consumes its record.
    var reload = sessionBoardReloadPending(), cancelled = false;
    function cancel() { cancelled = true; }
    ["wheel", "touchstart", "pointerdown", "keydown"].forEach(function (type) { doc.addEventListener(type, cancel, { capture: true, passive: true }); });
    requestAnimationFrame(function () {
      ["wheel", "touchstart", "pointerdown", "keydown"].forEach(function (type) { doc.removeEventListener(type, cancel, true); });
      if (!cancelled) {
        Object.keys(saved.panes).forEach(function (id) { if (id !== "convo" && PANES.indexOf(id) >= 0 && Number.isFinite(saved.panes[id])) paneScroll[id] = saved.panes[id]; });
        PANES.forEach(function (id) {
          if (id === "convo" || id === "bcol") return;
          var el = $(id); if (visible(el)) placeSessionScroll(el, saved.panes[id]);
        });
        if (!reload) {
          restoreSessionBoard(saved.board);
          if (!Array.isArray(saved.board) || !saved.board.some(function (r) { return r && r.scroller === "document"; })) placeSessionScroll(doc.scrollingElement, saved.top);
        }
        restoreSessionConversation(saved.conversation);
      }
      sessionViewReady = true;
    });
  }
  if (!shell) window.addEventListener("pagehide", saveSessionView);
  if (framed) window.wsSaveSessionView = saveSessionView;
