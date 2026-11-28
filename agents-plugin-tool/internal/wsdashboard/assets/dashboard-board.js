  // --- region swap -----------------------------------------------------------
  // The mode pseudo-region's hash is unknown to the page until the first
  // event carries it; an empty hash makes the server send it once. A region
  // update held back while the tab is hidden (hiddenRegions, see connect)
  // counts as held: a reconnect meanwhile names its hash, and the update
  // still lands when the tab shows.
  var modeHash = "", hiddenRegions = {}, treeRendered = null, entryRendered = null, panelRendered = null;
  // pageHidden tells whether nobody sees the page (a hidden tab, or a frame
  // the shell hides): region updates are held (hiddenRegions) while it
  // holds, and land when it ends (showRegions).
  function pageHidden() { return docHidden() || shHidden; }
  function held(withoutBoard) {
    return q("[data-region]").filter(function (el) { return !withoutBoard || el.getAttribute("data-region") !== "board"; }).map(function (el) {
      var name = el.getAttribute("data-region"), p = hiddenRegions[name];
      return name + "." + (p ? p.hash : el.getAttribute("data-h"));
    }).concat(mode !== "index" ? ["mode." + modeHash] : []).join(",");
  }
  // The board's lazy history (wswhiteboard stream.js) fetches the complete
  // sections of the topics a "name=..." or "target=..." query names.
  // The route answers with an ETag; no-cache revalidates every request, so
  // an unchanged answer costs a 304 and never shows a stale one.
  window.wbHistoryFetch = function (query) {
    if (!rid || !window.fetch) return Promise.resolve([]);
    return fetch("/s/" + rid + "/topic?" + query, { credentials: "same-origin", cache: "no-cache" }).then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    }).then(function (res) { return res && Array.isArray(res.topics) ? res.topics : []; });
  };
  // Hydrated history kept in the top shell's memory (shellMemo), across
  // frames and their eviction: one entry per topic answer, keyed by
  // everything the answer depends on that the page can see — the session,
  // the theme and language cookies the route renders with, the board's
  // history image stamp, and the topic section's own hash (data-th), which
  // moves with any change to the topic. Bounded to 128 answers and about
  // 8 MB of HTML.
  function historyCookie(name) {
    var m = new RegExp("(?:^|;\\s*)" + name + "=([^;]*)").exec(doc.cookie || "");
    return m ? m[1] : "";
  }
  function historyCacheKey(name, th, stamp) {
    return [rid, historyCookie("ws_dashboard_theme"), historyCookie("ws_dashboard_lang"), stamp, th, name].join("\u0000");
  }
  window.wbHistoryCache = {
    get: function (name, th, stamp) {
      var m = rid && th ? shellMemo("ws.topic-history", 128, 4 << 20) : null;
      return m ? m.get(historyCacheKey(name, th, stamp)) : undefined;
    },
    put: function (name, th, stamp, html) {
      var m = rid && th && typeof html === "string" ? shellMemo("ws.topic-history", 128, 4 << 20) : null;
      if (m) m.set(historyCacheKey(name, th, stamp), html);
    }
  };
  // A deferred board swap lands still (no board motion), so the board never
  // moves under a just-blurred input.
  function swap(name, hash, html, still) {
    var el = region(name);
    if (!el) return;
    var board = name === "board";
    if (board && boardReturnTimer) still = true;
    if (el.getAttribute("data-h") === hash && !(board && Object.keys(boardPending).length)) { if (board) { deferred = null; boardReturnApply(); } if (name === "rail") { if (railGate) railGate.forget(); if (typeof html !== "string") railRendered = html; } if (name === "tree" && typeof html !== "string") treeRendered = html; if (name === "entries" && typeof html !== "string") entryRendered = html; if (name === "panels" && typeof html !== "string") panelRendered = html; return; }
    var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("board", { op: "swap", region: typeof name === "string" ? name : "" }) : null;
    try {
    if (board && window.WsOverview && window.WsOverview.holding()) {
      boardOverviewHeld = { hash: hash, html: html };
      patchOverviewPreview(html);
      return;
    }
    if (board && window.WsCards && window.WsCards.dragging && window.WsCards.dragging()) { boardDragHeld = { hash: hash, html: html }; return; }
    // A rail render from before the layout change this page saw applied
    // would undo it on screen until the next one: it is dropped, and the
    // render after the change follows.
    if (name === "rail" && WsRail && WsRail.staleRail(html, railMinGen)) return;
    // A rail swap waits from a press on a grip or bar until it ends, while
    // a row's rename is open, and while a layout change posts (railGate),
    // and lands when that ends (railRelease).
    if (name === "rail" && railGate && railGate.offer({ hash: hash, html: html })) return;
    if (name === "rail" && WsRail && typeof html !== "string") {
      var rowTop = railScrollTop(), rowFocus = railFocus();
      var railMerged = WsRail.mergeRows(el, railRendered, html);
      railRendered = html;
      if (railMerged !== null) {
        railHoverUpdates = null;
        el.setAttribute("data-h", hash);
        if (railMerged) { syncSpinnerPhases(el); fmtTimes(el); railMarks(); railScrollTo(rowTop); railRefocus(rowFocus); apScan(el, "rail"); }
        return;
      }
      html = WsRail.rowInputHTML(html);
      railHoverUpdates = null;
    }
    if (name === "rail" && railHoverUpdates && railHoverUpdates(html)) {
      el.setAttribute("data-h", hash);
      return;
    }
    if (name === "head") {
      if (labelEditing()) { headHeld = { hash: hash, html: html }; return; }
      headHeld = null;
    }
    if (board) {
      deferred = null;
      var canonical = typeof html === "string" ? boardInputFull(html) : html;
      boardOffered = !boardLatest || boardLatest.hash !== hash ? { hash: hash, previous: boardLatest ? boardLatest.html : boardRendered, input: canonical } : null;
      boardLatest = { hash: hash, html: canonical };
      var boardPrev = boardRendered;
      html = boardStageInput(boardLatest.html || html);
    }
    var pin = (name === "entries" || name === "convlive") && pinned();
    var scroll = scrollState();
    var treeFocus = name === "tree" ? doc.activeElement : null;
    var panelReader = name === "panels" ? panelReaderState(el) : null;
    var answers = board ? boardRead(readAnswers) : null;
    if (name === "rail") wsAdmissionCapture();
    var railTop = name === "rail" ? railScrollTop() : null, railFoc = name === "rail" ? railFocus() : null;
    var frameH = board ? boardRead(frameHeights) : null;
    var history = board ? captureBoardHistory() : null;
    // The board keeps no outer pixel state: the current card's key is it. Only
    // the current card's inner scroller (and the docked panes) is captured.
    var anchored = board ? anchorCards() : null;
    var kept = board ? boardKeep() : null;
    // The entries merge and the board morph prepare only the nodes they
    // insert; tree and panel row merges prepare only changed units too.
    // A finished reply's mermaid
    // fences draw once merged; the streaming reply (convlive) keeps them
    // code blocks.
    var ins = null, convoRegion = name === "entries" || name === "convlive";
    var wbBatch = (board || name === "convlive") && typeof wbUpdateBeginBatch === "function";
    if (wbBatch) wbUpdateBeginBatch();
    try {
      if (convoRegion) convoSwapping++;
      try {
        if (name === "entries") {
          var entryNext = typeof html === "string" ? entryInputFull(html) : html;
          mergeEntriesRegion(el, entryNext || html, pin, entryRendered);
          entryRendered = entryNext;
          if (typeof wbOnlyOn === "function" && wbOnlyOn()) wbFoldSync();
        }
        else if (name === "tree") {
          var treeNext = typeof html === "string" ? treeInputFull(html) : html;
          ins = treeNext && mergeTreeRows(el, treeRendered, treeNext);
          treeRendered = treeNext;
          if (!ins) {
            el.innerHTML = treeNext ? treeInputHTML(treeNext) : html;
            restoreOpen(el); fmtTimes(el); sweepOutImgs(el);
          } else ins.forEach(function (n) { restoreOpen(n); fmtTimes(n); sweepOutImgs(n); });
        }
        else if (name === "panels") {
          var panelNext = typeof html === "string" ? panelInputFull(html) : html;
          ins = panelNext && mergePanelRows(el, panelRendered, panelNext);
          panelRendered = panelNext;
          if (!ins) {
            el.innerHTML = panelNext ? panelInputHTML(panelNext) : html;
            restoreOpen(el); fmtTimes(el); sweepOutImgs(el);
          } else ins.forEach(function (n) { restoreOpen(n); fmtTimes(n); sweepOutImgs(n); });
        }
        else if (board) {
          ins = morphBoard(el, html, boardPrev);
          if (window.wbHistory && window.wbHistory.sweep) window.wbHistory.sweep();
          ins.forEach(bandBoard);
          ins.forEach(function (n) {
            var taken = n && n.__wbVirtualTaken || null;
            if (taken) delete n.__wbVirtualTaken;
            // Materialize the reader's older pages before restoring disclosures
            // and frame heights, including immutable panel versions off the tail.
            if (window.wbHistory) {
              window.wbHistory.init(n);
              window.wbHistory.restore(n, history);
              if (taken && taken.history) window.wbHistory.restore(n, taken.history);
            }
            watchBoard(n);
            if (taken && taken.openDetails && typeof virtualRestoreOpen === "function") virtualRestoreOpen(n, taken.openDetails, { stashed: true });
            restoreOpen(n);
            fmtTimes(n);
            sweepOutImgs(n);
            if (taken && taken.frameH) setFrameHeights(n, taken.frameH);
            setFrameHeights(n, frameH);
            mountFrames(n);
            if (taken && taken.answers) writeAnswers(n, taken.answers);
            writeAnswers(n, answers);
            if (taken && taken.scrollTop) n.scrollTop = taken.scrollTop;
            if (window.WsCards && window.WsCards.decorate) window.WsCards.decorate(n);
            // A placeholder a swap brought (the viewport bands mount it later)
            // takes the mark too, and its frame takes it once mounted.
            q(".doc-frame", n).forEach(function (f) { f.wbKeep = true; });
          });
          // The morph retains unchanged topic nodes, including hydrated closed
          // pages. Their image frames still follow the board's external-file stamp.
          if (window.wbHistory && window.wbHistory.refreshImages) window.wbHistory.refreshImages(doc);
        } else {
          el.innerHTML = html;
          if (name === "rail" && WsRail) railHoverUpdates = WsRail.hoverUpdates(el);
          restoreOpen(el);
          fmtTimes(el);
          sweepOutImgs(el);
        }
      } finally {
        if (convoRegion) convoSwapping--;
      }
      syncSpinnerPhases(el);
      el.setAttribute("data-h", hash);
      if (board) {
        indexFrames();
        if (window.WsPanelCard && window.WsPanelCard.sync) window.WsPanelCard.sync();
        if (window.wbForm) window.wbForm();
        bindCapsuleNotes();
        mermaid(ins);
        syncChips();
        // The notes came back with their markers (writeAnswers); their image
        // rows go back after them, and a note the swap took drops its images.
        if (ctext) { syncMarkers(); renderImgs(); }
        // An input-held selection is already a value snapshot, even when its
        // source frame leaves the board during an update.
        if (mpend && !mpend.locked && mpend.frame && !doc.contains(mpend.frame)) hideMention();
      }
      if (board || name === "convlive") {
        applySent(board);
        if (typeof syncWbUpdateItems === "function") syncWbUpdateItems();
      }
    } finally {
      if (wbBatch && typeof wbUpdateEndBatch === "function") wbUpdateEndBatch();
    }
    if (name === "convlive" || name === "entries") outboxSync();
    if (name === "panels") { wpSync(); restorePanelReader(el, panelReader); }
    if (name === "git") gpSwapped();
    if (treeFocus && treeFocus.isConnected && doc.activeElement !== treeFocus) treeFocus.focus({ preventScroll: true });
    restoreScroll(scroll);
    if (kept) kept.h.apply();
    if (board) {
      anchorSettle(anchored);
      boardRecordPending(); boardPendingBadge(); boardHoldIndicators();
      if (typeof boardDialSync === "function") boardDialSync();
      if (!boardRelease) boardReturnApply();
      boardApplied();
    }
    if (pin) (typeof queuePinConvo === "function" ? queuePinConvo() : pinConvo());
    else if (convoRegion && typeof queueSyncConvoBottom === "function") queueSyncConvoBottom();
    if (live && TAB[name]) queueSwapDot(TAB[name]);
    if (name === "tree" || name === "convlive") drawerWait();
    if (name === "tree") agSwapped();
    if (TL_REGIONS[name]) { delete tlHeld[name]; tlCount(); }
    if (name === "convlive") syncBadge();
    if ((name === "convlive" || name === "entries") && typeof convMotionSync === "function") convMotionSync(name);
    // A list that arrives while a / waits in the box opens the picker.
    if (name === "cmds" && slash) slash.sync();
    if (name === "entries") syncChead();
    if (name === "head" && !sseOn) connected(false);
    if (name === "head") labelSwapped();
    if (name === "rail") { railMarks(); railScrollTo(railTop); railRefocus(railFoc); }
    if (name === "head" || name === "convlive") leadSwapped(name);
    if (name === "rail" || name === "sessions") apScan(el, name);
    } finally {
      if (jTok) WsJank.end(jTok);
    }
  }

  // --- board morph: unchanged topics keep their nodes -------------------------
  // Each topic section carries data-topic, its name. A board swap keeps a
  // topic's section, and with it the body frame (no reload, no second
  // mermaid load, no second image relay), when the section's incoming HTML
  // equals the HTML it last arrived with (boardSeen). The comparison is
  // never against the live DOM, which the page changes after a swap (times,
  // sent marks, answers, frame themes and heights). Everything else the
  // board holds (the closed-topic control, the reply bar) is replaced. The
  // initial record is the board as served, read before anything changes it.
  // The server stamps each section with data-th, a hash of its rendered
  // bytes (TopicHTML, render.go), compared in place of the HTML (topicSeen).
  var boardSeen = {}, boardRendered = null, boardLatest = null, boardPending = Object.create(null), boardRelease = false;
  var boardOwn = Object.create(null), boardImmediate = Object.create(null), boardSnap = null, boardAfterApply = [];
  var boardChanges = [], boardOffered = null, boardReleaseFrame = 0, boardReleaseTimer = 0;
  var swapDotFrame = 0, swapDotTimer = 0, swapDotPending = Object.create(null);
  function flushSwapDots() {
    swapDotFrame = 0;
    swapDotTimer = 0;
    var tabs = Object.keys(swapDotPending);
    swapDotPending = Object.create(null);
    for (var i = 0; i < tabs.length; i++) {
      var t = tabs[i];
      if (!tabShown(t)) {
        var dot = tabDot(t);
        if (dot) dot.classList.add("on");
      }
      if (t === "convo") {
        var cVis = typeof convoVisible === "function" ? convoVisible() : (typeof paneShown === "function" ? paneShown("convo") : visible(convo));
        var fVis = typeof paneShown === "function" ? paneShown("files") : visible(fpane);
        var tVis = typeof paneShown === "function" ? paneShown("cterm-panes") : visible($("cterm-panes"));
        if (!cVis && (fVis || tVis || (grid && grid.getAttribute("data-ctab") !== "convo")) && ctabDot()) ctabDot().classList.add("on");
      }
    }
  }
  function queueSwapDot(tab) {
    swapDotPending[tab] = true;
    if (swapDotFrame || swapDotTimer) return;
    var hidden = (typeof pageHidden === "function" ? pageHidden() : !!(doc && doc.hidden)) || typeof requestAnimationFrame !== "function";
    if (hidden) swapDotTimer = setTimeout(flushSwapDots, 0);
    else swapDotFrame = requestAnimationFrame(flushSwapDots);
  }
  var boardPaged = Object.create(null);
  var boardReturnTimer = 0, boardReturnFresh = false;
  function topicKey(n) {
    return n.nodeType === 1 && n.tagName === "SECTION" && n.classList.contains("topic") && n.hasAttribute("data-topic") ? n.getAttribute("data-topic") : null;
  }
  function topicSeen(n) {
    return n.getAttribute("data-th") || n.outerHTML;
  }
  (function () {
    var b = region("board");
    if (b) {
      boardRendered = boardInputFull(b.innerHTML);
      boardLatest = { hash: b.getAttribute("data-h"), html: boardRendered };
      Array.prototype.forEach.call(b.children, function (n) { var k = topicKey(n); if (k !== null) boardSeen["t:" + k] = topicSeen(n); });
    }
  })();
  // --- board cards (cards.js): the page's own cards among the topics --------
  // The framework mounts on the board region once, before any board swap,
  // and restores the cards and the pinned or hand-moved topics this browser
  // kept for the session. Showing a card switches to the board tab where
  // the view shows one column at a time.
  // Terminal cards (terminal.js) exist only on a page the server marked
  // data-term; their module adds the "+ Terminal" button at the end of
  // the composer's status line (or fixed at the page's corner without one).
  // Web cards (webcard.js) exist only on a data-sysopen page; their "+ Web"
  // button stands beside it, and their picks and console errors go to the
  // composer's chips (web picks, below).
  // The paged docking gallery (cards.js #bgal, joining the page body) has
  // its shape from WsGallery.layout (gallery.js) over this page's width
  // and layout menu (mode), and the page maps it (layout): from 1600px
  // (not "tabs"), the session rail stays permanently docked left while the
  // 4-column gallery (empty slot, drag slot, or data-pincol column) opens
  // only when the right-side spare width fits at least the board's width;
  // otherwise (or below 1600px / in tabs), the Main Board is Page 0 and
  // the gallery overlays the board's column (data-gal="narrow", placed by
  // placeGallery). On the live grid the split handle sizes the
  // conversation's column beside the board (split).
  // A board swap waits while a card or topic is dragged (boardDragHeld), and
  // lands at the drag's end.
  var filePreview = null, filePreviewBack = null;
  function splitFiles() {
    return live && !worktree && layoutMode !== "tabs" && window.matchMedia(
      layoutMode === "split" ? "(min-width: 760px)" : "(min-width: 1100px)"
    ).matches;
  }
  function sizeFilePreview() {
    if (!filePreview || !filePreview.current()) return;
    var board = $("bcol").getBoundingClientRect();
    // The closed board's .dwrap is capped at 1680px without a pin column.
    // Reading space reaches the viewport gutter even while that cap remains.
    var right = doc.documentElement.clientWidth - parseFloat(window.getComputedStyle(grid.closest(".dwrap")).paddingRight);
    filePreview.el.style.width = Math.max(0, right - board.left) + "px";
  }
  function sizeFilesSheet() {
    if (!filesSheet || filesSheet.el.hidden) { doc.body.removeAttribute("data-files-docked"); return; }
    var sheet = filesSheet.el;
    if (!splitFiles()) { sheet.removeAttribute("data-docked"); doc.body.removeAttribute("data-files-docked"); return; }
    // The left column includes its header and composer, not just #convo.
    var column = chead.getBoundingClientRect(), bounds = grid.getBoundingClientRect();
    sheet.setAttribute("data-docked", "");
    if (sheet.hasAttribute("data-max")) doc.body.setAttribute("data-files-docked", "");
    else doc.body.removeAttribute("data-files-docked");
    sheet.style.setProperty("--files-dock-left", column.left + "px");
    sheet.style.setProperty("--files-dock-top", bounds.top + "px");
    sheet.style.setProperty("--files-dock-width", column.width + "px");
    sheet.style.setProperty("--files-dock-height", bounds.height + "px");
  }
  function sizeFileSurfaces() { sizeFilesSheet(); sizeFilePreview(); }
  function syncFilePreview() {
    sizeFilesSheet();
    if (splitFiles()) {
      sizeFilePreview();
    } else if (filePreview && filePreview.current()) {
      var previous = filePreview.close();
      window.WsPreview.open(previous.path, Object.assign({}, previous.options, { keep: false }));
    }
  }
  function previewLeftFile(path, options) {
    if (splitFiles() && filePreview && !options.keep) {
      filePreviewBack = doc.activeElement;
      root.setAttribute("data-file-preview", "");
      filePreview.open(path, options);
      sizeFilePreview();
    } else {
      if (filePreview) filePreview.close();
      window.WsPreview.open(path, options);
    }
  }
  // Two owners, not two views onto one explorer. Keep the tab's existing
  // storage keys; the toolbar uses the same records in its own namespace.
  if ((live || worktree) && fpane && window.WsFiles) {
    var fileHooks = {
      rid: cardIdentity, post: worktree ? browsePost : post, toast: toast, copy: copyText,
      routeBase: worktree ? "/s/host0/" : undefined,
      location: worktree ? { root: worktree, dir: "" } : undefined,
      load: function (k) { return load(worktree && k === "ws.dashboard.files.view" ? k + "." + cardIdentity : k); },
      save: function (k, v) { save(worktree && k === "ws.dashboard.files.view" ? k + "." + cardIdentity : k, v); },
      local: function () { return root.hasAttribute("data-sysopen"); },
      send: worktree ? null : attachExplorerPath,
      openCard: function (location) {
        // Dismiss before constructing/revealing the new card, not afterward.
        window.WsCards.dismissFiles();
        window.WsFiles.open(location);
      }
    };
    // Register adapters before board mount restores explorer card IDs.
    window.WsFiles.setup(fileHooks);
    function createFiles(node, rootSlot, toolbar) {
      return window.WsFiles.create(node, Object.assign({}, fileHooks, {
        rootSlot: rootSlot,
        preview: live && !worktree ? previewLeftFile : undefined,
        load: toolbar ? function (k) { return load("ws.dashboard.toolbar." + k); } : fileHooks.load,
        save: toolbar ? function (k, v) { save("ws.dashboard.toolbar." + k, v); } : fileHooks.save
      }));
    }
    filesTab = createFiles(fpane, chead && chead.querySelector(".fsum"), false);
    var sheet = doc.createElement("section");
    sheet.id = "toolbar-files";
    sheet.className = "fpane";
    sheet.hidden = true;
    sheet.setAttribute("aria-label", fpane.getAttribute("aria-label"));
    doc.body.appendChild(sheet);
    var sheetInstance = null;
    filesSheet = {
      el: sheet,
      shown: function () {
        sizeFilesSheet();
        if (!sheetInstance) sheetInstance = createFiles(sheet, null, true);
        sheetInstance.shown();
      },
      hidden: function () {
        if (sheetInstance) { sheetInstance.cancelPreview(); sheetInstance.hidden(); }
        if (sheet.hasAttribute("data-docked")) {
          // A left preflight may still be pending before any pane exists.
          if (filesTab) filesTab.cancelPreview();
          if (filePreview) filePreview.close();
        }
        sheet.removeAttribute("data-docked");
        doc.body.removeAttribute("data-files-docked");
      }
    };
    // Capture before explorer menus/inputs or pane chrome consume Escape.
    // Sandboxed preview documents intentionally remain outside this handler.
    doc.addEventListener("keydown", function (e) {
      if (e.key !== "Escape" || sheet.hidden || !sheet.hasAttribute("data-docked")) return;
      e.preventDefault(); e.stopPropagation();
      window.WsCards.dismissFiles();
    }, true);
  }
  var cardBoard = mode !== "index" && window.WsCards ? region("board") : null;
  var PIN_WIDE = window.matchMedia ? window.matchMedia("(min-width: 1600px)") : null, boardDragHeld = null, boardOverviewHeld = null;
  function afterOverview(fn) { if (window.WsOverview) window.WsOverview.after(fn); else fn(); }
  // galMode is what the gallery's layout reads: the viewport width and
  // whether the layout menu shows one column at a time.
  function galMode() {
    return { width: window.innerWidth || 0,
      tabs: !!(live && grid.getAttribute("data-layout") === "tabs") };
  }
  // placeGallery lays the narrow view's gallery over the board's column:
  // the column's box, cut to the window. A hidden column (another tab)
  // keeps the last box and moves the gallery off the window instead, so
  // its panes keep their sizes and scroll offsets.
  var galFrame = 0, galNarrow = false;
  function placeGallery() {
    galFrame = 0;
    var g = window.WsCards && window.WsCards.gallery && window.WsCards.gallery(), col = $("bcol") || cardBoard;
    if (!g || !galNarrow || !col) return;
    var r = col.getBoundingClientRect(), top = Math.max(r.top, 0), bottom = Math.min(r.bottom, window.innerHeight);
    if (!(r.width > 0) || bottom <= top) { g.setAttribute("data-away", ""); return; }
    g.removeAttribute("data-away");
    g.style.setProperty("--gal-left", r.left + "px");
    g.style.setProperty("--gal-top", top + "px");
    g.style.setProperty("--gal-width", r.width + "px");
    g.style.setProperty("--gal-height", (bottom - top) + "px");
  }
  function queueGallery() { if (galNarrow && !galFrame) galFrame = requestAnimationFrame(placeGallery); }
  // --- board export menu & handoff prompt ------------------------------------
  function bexpToast(msg, kind) {
    if (window.WsToast && typeof window.WsToast.show === "function") {
      window.WsToast.show(msg, kind ? { kind: kind } : undefined);
    } else if (typeof toast === "function") {
      toast(msg);
    }
  }
  function xmlEscapeAttr(s) {
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
  }
  function xmlEscapeText(s) {
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }
  function normExportPath(p) {
    var s = String(p || "").replace(/[\\/]+$/, "");
    if (/^[A-Za-z]:|^\\\\/.test(s)) s = s.toLowerCase();
    return s;
  }
  function cleanExportReplyId(v) {
    var s = String(v || "").trim();
    return s.indexOf("id:") === 0 ? s.slice(3).trim() : s;
  }
  function buildMailboxTag(meta) {
    var m = meta && typeof meta === "object" ? meta : {};
    var replyIdVal = cleanExportReplyId(m.reply_id || m.id || "");
    if (!replyIdVal) return "";
    var dir = String(m.dir || "");
    var rootDir = String(m.root || "");
    var branch = String(m.branch || "");
    var harness = String(m.harness || "");
    var label = String(m.label || "");
    var open = '<mailbox id="' + xmlEscapeAttr("id:" + replyIdVal) + '" dir="' + xmlEscapeAttr(dir) + '"';
    if (rootDir && normExportPath(rootDir) !== normExportPath(dir)) {
      open += ' root="' + xmlEscapeAttr(rootDir) + '"';
    }
    if (branch) open += ' branch="' + xmlEscapeAttr(branch) + '"';
    if (harness) open += ' harness="' + xmlEscapeAttr(harness) + '"';
    return label ? open + ">" + xmlEscapeText(label) + "</mailbox>" : open + "/>";
  }
  // Always keep this prompt in English regardless of dashboard UI locale.
  function buildBoardHandoffPrompt(data) {
    var d = data && typeof data === "object" ? data : {};
    var archivePath = String(d.path || "").trim();
    var m = d.mailbox && typeof d.mailbox === "object" ? d.mailbox : {};
    var replyIdVal = cleanExportReplyId(d.reply_id || m.reply_id || m.id || "");
    var mailboxTag = "";
    if (replyIdVal) {
      if (typeof d.mailbox_ref === "string" && d.mailbox_ref.indexOf("<mailbox ") === 0) {
        mailboxTag = d.mailbox_ref;
      } else {
        mailboxTag = buildMailboxTag({
          reply_id: replyIdVal,
          dir: m.dir || d.dir || "",
          root: m.root || "",
          branch: m.branch || "",
          harness: m.harness || "",
          label: m.label || ""
        });
      }
    }
    var targetId = replyIdVal ? "id:" + replyIdVal : "";
    var lines = [
      "Continue this task using the exported whiteboard archive from the previous session:",
      "- Whiteboard archive path: " + archivePath
    ];
    if (mailboxTag) lines.push("- Previous session mailbox: " + mailboxTag);
    lines.push(
      "",
      "Steps:",
      "1. First, load the exported whiteboard archive using `whiteboard.load` with `" + archivePath + "`."
    );
    if (targetId) {
      lines.push(
        "2. Next, send a message to the previous session (`" + targetId + "`) via `mailbox.send` stating that the whiteboard has already been handed over, and ask for any unrecorded working context, caveats, decisions, or remaining tasks.",
        "3. If the previous session has already closed or does not respond, proceed using the loaded whiteboard."
      );
    } else {
      lines.push("2. Proceed with the work using the loaded whiteboard.");
    }
    return lines.join("\n");
  }
  var bexpMenu = null, bexpTrigger = null, bexpFallback = null, bexpFallbackTrigger = null, bexpBusy = false;
  function dismissBoardExportFallback(refocus) {
    if (!bexpFallback) return;
    if (bexpFallback.parentNode) bexpFallback.parentNode.removeChild(bexpFallback);
    bexpFallback = null;
    if (refocus && bexpFallbackTrigger && bexpFallbackTrigger.isConnected) bexpFallbackTrigger.focus();
    bexpFallbackTrigger = null;
  }
  function showBoardExportFallback(promptText, trigger) {
    dismissBoardExportFallback(false);
    var box = doc.createElement("div");
    box.className = "d-bexp-fallback";
    box.setAttribute("role", "region");
    box.setAttribute("aria-label", wsT("js.board.export.prompt_label"));
    var head = doc.createElement("div");
    head.className = "d-bexp-fb-head";
    var msg = doc.createElement("div");
    msg.className = "d-bexp-fb-msg";
    msg.textContent = wsT("js.board.export.copy_denied");
    var closeBtn = doc.createElement("button");
    closeBtn.type = "button";
    closeBtn.className = "d-bexp-fb-close";
    closeBtn.textContent = "\u00d7";
    closeBtn.setAttribute("aria-label", wsT("js.board.export.close"));
    closeBtn.addEventListener("click", function () { dismissBoardExportFallback(true); });
    head.appendChild(msg);
    head.appendChild(closeBtn);
    var ta = doc.createElement("textarea");
    ta.className = "d-bexp-fb-text";
    ta.readOnly = true;
    ta.rows = 6;
    ta.value = promptText;
    ta.setAttribute("aria-label", wsT("js.board.export.prompt_label"));
    var acts = doc.createElement("div");
    acts.className = "d-bexp-fb-acts";
    var copyBtn = doc.createElement("button");
    copyBtn.type = "button";
    copyBtn.className = "btn pr d-bexp-fb-copy";
    copyBtn.textContent = wsT("js.board.export.copy_retry");
    copyBtn.addEventListener("click", function () {
      writeExportClipboardText(promptText).then(function (ok) {
        if (ok) {
          dismissBoardExportFallback(true);
          bexpToast(wsT("js.board.export.copied"), "info");
        } else {
          ta.focus();
          ta.select();
        }
      });
    });
    acts.appendChild(copyBtn);
    box.appendChild(head);
    box.appendChild(ta);
    box.appendChild(acts);
    doc.body.appendChild(box);
    bexpFallback = box;
    bexpFallbackTrigger = trigger || bexpFallbackTrigger;
    ta.focus();
    ta.select();
  }
  function execCommandCopyText(text) {
    var prev = doc.activeElement;
    var ta = doc.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.cssText = "position:fixed;top:0;left:0;width:1px;height:1px;opacity:0";
    doc.body.appendChild(ta);
    ta.focus();
    ta.select();
    var ok = false;
    try { ok = !!(doc.execCommand && doc.execCommand("copy")); } catch (e) { ok = false; }
    if (ta.parentNode) ta.parentNode.removeChild(ta);
    if (prev && prev.isConnected && typeof prev.focus === "function") prev.focus();
    return ok;
  }
  function writeExportClipboardText(text) {
    var nav = typeof navigator !== "undefined" ? navigator : null;
    if (nav && nav.clipboard && typeof nav.clipboard.writeText === "function" && window.isSecureContext) {
      return nav.clipboard.writeText(text).then(function () { return true; }, function () { return execCommandCopyText(text); });
    }
    return Promise.resolve(execCommandCopyText(text));
  }
  function fetchBoardExport() {
    var sessionRid = (typeof rid === "string" && rid) || (root && root.getAttribute && root.getAttribute("data-rid")) || "";
    if (!sessionRid || typeof window.fetch !== "function") {
      return Promise.reject(new Error(wsT("js.post.nobrowser")));
    }
    return window.fetch("/s/" + sessionRid + "/board-export", {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: "{}"
    }).then(function (r) {
      return r.text().then(function (body) {
        if (!r.ok) throw new Error((body && body.trim()) || ("HTTP " + r.status));
        try { return JSON.parse(body || "{}"); } catch (e) { return {}; }
      });
    });
  }
  function startBoardExportHandoff(trigger) {
    if (bexpBusy) return;
    bexpBusy = true;
    var nav = typeof navigator !== "undefined" ? navigator : null;
    var exportReq = fetchBoardExport().then(function (data) {
      return buildBoardHandoffPrompt(data);
    });
    var writeSettled = null;
    if (nav && nav.clipboard && typeof nav.clipboard.write === "function" && typeof window.ClipboardItem === "function" && typeof window.Blob === "function" && window.isSecureContext) {
      var item = new window.ClipboardItem({
        "text/plain": exportReq.then(function (text) {
          return new window.Blob([text], { type: "text/plain" });
        })
      });
      writeSettled = nav.clipboard.write([item]).then(function () { return true; }, function () { return false; });
    }
    return exportReq.then(function (text) {
      var finishCopy = function (ok) {
        bexpBusy = false;
        if (ok) {
          dismissBoardExportFallback(false);
          bexpToast(wsT("js.board.export.copied"), "info");
        } else {
          showBoardExportFallback(text, trigger);
        }
      };
      if (writeSettled) {
        return writeSettled.then(function (ok) {
          if (ok) { finishCopy(true); return; }
          return writeExportClipboardText(text).then(finishCopy);
        });
      }
      return writeExportClipboardText(text).then(finishCopy);
    }, function (err) {
      bexpBusy = false;
      bexpToast(wsT("js.board.export.fail", (err && err.message) || String(err || "")), "warn");
    });
  }
  function closeBoardExportMenu(refocus) {
    if (!bexpMenu || bexpMenu.hidden) return;
    bexpMenu.hidden = true;
    if (bexpTrigger) bexpTrigger.setAttribute("aria-expanded", "false");
    var back = bexpTrigger;
    bexpTrigger = null;
    if (refocus && back && back.isConnected) back.focus();
  }
  function placeBoardExportMenu(pop, trigger) {
    if (!pop || !trigger || !trigger.getBoundingClientRect) return;
    var r = trigger.getBoundingClientRect();
    var vw = window.innerWidth || 1024, vh = window.innerHeight || 768;
    var w = pop.offsetWidth || 240, h = pop.offsetHeight || 80;
    var left = Math.max(8, Math.min(r.left, vw - w - 8));
    var top = r.bottom + 6;
    if (top + h + 8 > vh && r.top - h - 6 >= 8) top = r.top - h - 6;
    top = Math.max(8, Math.min(top, vh - h - 8));
    pop.style.left = left + "px";
    pop.style.top = top + "px";
  }
  function openBoardExportMenu(trigger, downloadFn) {
    closeBoardExportMenu(false);
    var pop = bexpMenu;
    if (!pop || !pop.isConnected) {
      pop = doc.createElement("div");
      pop.className = "tpop d-bexp-pop";
      pop.setAttribute("role", "menu");
      pop.hidden = true;
      doc.body.appendChild(pop);
      bexpMenu = pop;
    }
    pop.innerHTML = "";
    pop.setAttribute("aria-label", wsT("js.board.export.menu.label"));
    [
      [wsT("js.board.export.handoff"), function (btn) { var p = startBoardExportHandoff(btn); closeBoardExportMenu(true); return p; }],
      [wsT("js.board.export.download"), function () { closeBoardExportMenu(true); if (downloadFn) downloadFn(); }]
    ].forEach(function (spec) {
      var item = doc.createElement("button");
      item.type = "button";
      item.className = "d-bexp-opt";
      item.setAttribute("role", "menuitem");
      item.textContent = spec[0];
      item.addEventListener("click", function (e) {
        e.stopPropagation();
        spec[1](trigger);
      });
      pop.appendChild(item);
    });
    bexpTrigger = trigger || null;
    if (bexpTrigger) bexpTrigger.setAttribute("aria-expanded", "true");
    pop.hidden = false;
    placeBoardExportMenu(pop, bexpTrigger);
    if (pop.firstElementChild && typeof pop.firstElementChild.focus === "function") pop.firstElementChild.focus();
  }
  function toggleBoardExportMenu(ev, trigger, downloadFn) {
    var btn = trigger || (ev && ev.currentTarget) || doc.querySelector(".board-download");
    if (bexpMenu && !bexpMenu.hidden && (!btn || bexpTrigger === btn)) {
      closeBoardExportMenu(false);
      return;
    }
    openBoardExportMenu(btn, downloadFn);
  }
  if (typeof window !== "undefined") {
    window.WsBoardExport = {
      buildMailboxTag: buildMailboxTag,
      buildPrompt: buildBoardHandoffPrompt,
      toggle: toggleBoardExportMenu,
      close: closeBoardExportMenu,
      handoff: startBoardExportHandoff
    };
  }
  doc.addEventListener("click", function (e) {
    if (!bexpMenu || bexpMenu.hidden) return;
    var t = e.target;
    if (t && ((bexpMenu.contains(t)) || (bexpTrigger && (t === bexpTrigger || bexpTrigger.contains(t))))) return;
    closeBoardExportMenu(false);
  }, true);
  doc.addEventListener("keydown", function (e) {
    if (bexpMenu && !bexpMenu.hidden) {
      if (e.key === "Escape") {
        e.preventDefault();
        closeBoardExportMenu(true);
        return;
      }
      if (e.key === "Tab") {
        closeBoardExportMenu(false);
        return;
      }
      if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "Home" || e.key === "End") {
        var opts = Array.prototype.slice.call(bexpMenu.querySelectorAll('[role="menuitem"]'));
        if (!opts.length) return;
        e.preventDefault();
        var idx = opts.indexOf(doc.activeElement);
        if (e.key === "Home") idx = 0;
        else if (e.key === "End") idx = opts.length - 1;
        else if (e.key === "ArrowDown") idx = idx < 0 ? 0 : (idx + 1) % opts.length;
        else idx = idx < 0 ? opts.length - 1 : (idx - 1 + opts.length) % opts.length;
        opts[idx].focus();
      }
      return;
    }
    if (bexpFallback && !bexpFallback.hidden && e.key === "Escape" && bexpFallback.contains(doc.activeElement)) {
      e.preventDefault();
      dismissBoardExportFallback(true);
    }
  }, true);
  doc.addEventListener("wheel", function (e) {
    if (bexpMenu && !bexpMenu.hidden && !(e.target && bexpMenu.contains(e.target))) closeBoardExportMenu(false);
  }, { capture: true, passive: true });
  doc.addEventListener("touchmove", function (e) {
    if (bexpMenu && !bexpMenu.hidden && !(e.target && bexpMenu.contains(e.target))) closeBoardExportMenu(false);
  }, { capture: true, passive: true });
  window.addEventListener("resize", function () {
    if (bexpMenu && !bexpMenu.hidden) closeBoardExportMenu(false);
  });
  window.addEventListener("blur", function () {
    if (bexpMenu && !bexpMenu.hidden) closeBoardExportMenu(false);
  });

  if (cardBoard) {
    if (window.WsPreview) window.WsPreview.setup({ renderDiff: gwRender, post: worktree ? browsePost : post, toast: toast });
    if (window.WsPanelCard) window.WsPanelCard.setup({ mountFrames: mountFrames, boardBoxes: boardBoxes });
    if (window.WsTerm) window.WsTerm.setup({ post: worktree ? browsePost : post, toast: toast, setCtab: live ? setCtab : null, stage: shStage });
    // The composer's chips come later in this script: the hooks are hoisted
    // functions, and cchips is not yet assigned here.
    if (window.WsWeb) window.WsWeb.setup($("cchips") ? { post: post, toast: toast, rid: rid, addPick: addWebPick, addConsole: addWebConsole } : { post: post, toast: toast, rid: rid });
    var dbody = cardBoard.closest(".dbody");
    if (live && !worktree && fpane && window.WsPreview) {
      var previewPane = doc.createElement("section");
      previewPane.className = "file-preview-pane";
      previewPane.setAttribute("aria-label", fpane.getAttribute("aria-label"));
      grid.appendChild(previewPane);
      filePreview = window.WsPreview.createPane(previewPane, function (hadFocus) {
        root.removeAttribute("data-file-preview");
        if (filesTab) filesTab.cancelPreview();
        if (sheetInstance) sheetInstance.cancelPreview();
        if (hadFocus && filePreviewBack && filePreviewBack.isConnected) filePreviewBack.focus({ preventScroll: true });
      });
      filePreview.el = previewPane;
      if (window.ResizeObserver) {
        var previewResizeFrame = 0;
        var previewResize = new ResizeObserver(function () {
          if (typeof requestAnimationFrame !== "function") { sizeFileSurfaces(); return; }
          if (previewResizeFrame) return;
          previewResizeFrame = requestAnimationFrame(function () {
            previewResizeFrame = 0;
            sizeFileSurfaces();
          });
        });
        previewResize.observe(grid);
        if (chead) previewResize.observe(chead);
        if (dbody) previewResize.observe(dbody);
      }
    }
    window.WsCards.mount(cardBoard, {
      key: "ws.dashboard.cards." + cardIdentity, load: load, save: save,
      toolbar: {
        overview: function () { if (window.WsOverview) window.WsOverview.toggle(); },
        filter: function (run) { if (window.WsOverview) window.WsOverview.filter(run); else run(); },
        download: worktree ? null : function () {
          toggleBoardExportMenu(arguments[0], arguments[1], function () {
            var a = doc.createElement("a");
            a.href = "/s/" + rid + "/board.zip"; a.download = "";
            doc.body.appendChild(a); a.click(); a.remove();
          });
        },
        files: worktree ? null : filesSheet,
        stageFill: toggleStageFill
      },
      reveal: function () {
        // Linked/durable cards must not remain behind either explorer surface.
        if (filesSheet && filesSheet.el.hasAttribute("data-docked")) window.WsCards.dismissFiles();
        if (filePreview) filePreview.close();
        if (live && !tabShown("board")) setTab("board");
      },
      host: dbody || doc.body, mode: galMode, split: grid && grid.classList.contains("live") ? grid : null,
      leading: function (c) { return worktree && c.preview && !(PIN_WIDE && PIN_WIDE.matches); },
      layout: function (s) {
        var col = !!s.column, mode = s.narrow ? "narrow" : (col ? "col" : null);
        if (col !== root.hasAttribute("data-pincol")) {
          if (col) root.setAttribute("data-pincol", ""); else root.removeAttribute("data-pincol");
          railOpen(false);
        }
        if (mode) root.setAttribute("data-gal", mode); else root.removeAttribute("data-gal");
        galNarrow = mode === "narrow";
        if (galNarrow) queueGallery();
        if (window.WsOverview && window.WsOverview.syncPreview) window.WsOverview.syncPreview();
      },
      closed: function (from, done) {
        if (!window.fetch) { done(null); return; }
        fetch("/s/" + rid + "/closed?from=" + from, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
          if (!r.ok) throw new Error("HTTP " + r.status);
          return r.json();
        }).then(function (res) { done(frag(res.html || "").firstElementChild); }, function () { done(null); });
      },
      adopted: function (n) {
        if (window.wbHistory) window.wbHistory.init(n);
        watchBoard(n);
        bandBoard(n);
        restoreOpen(n);
        mountFrames(n);
        if (window.wbBindBoard) window.wbBindBoard(n);
        if (window.wbAgo) window.wbAgo(n);
        if (window.WsCards && window.WsCards.decorate) window.WsCards.decorate(n);
        if (window.WsPanelCard && window.WsPanelCard.sync) window.WsPanelCard.sync();
        mermaid([n]);
      },
      drag: function (on) {
        if (on) return;
        var h = boardDragHeld;
        boardDragHeld = null;
        if (h) swap("board", h.hash, h.html, true);
      }
    });
    if (window.WsPanelCard && window.WsPanelCard.sync) window.WsPanelCard.sync();
    if (worktree) {
      var tools = window.WsCards.toolbar().querySelector(".board-tools"), explorer = doc.createElement("button");
      q(".board-overview, .board-state-filter", tools).forEach(function (n) { n.remove(); });
      explorer.type = "button"; explorer.className = "board-action board-explorer";
      explorer.setAttribute("aria-label", wsT("js.worktree.explorer"));
      explorer.title = wsT("js.worktree.explorer");
      explorer.innerHTML = window.WsCards.icon("folder-open");
      var label = doc.createElement("span"); label.className = "board-label";
      label.textContent = wsT("js.worktree.explorer"); explorer.appendChild(label);
      explorer.addEventListener("click", function () { window.WsFiles.open({ root: worktree, dir: "" }); });
      (tools.querySelector(".board-acts") || tools).appendChild(explorer);
    }
    // wbForm creates wbHistory near the end of page startup. Mount overview
    // after that, not here while only the card framework exists.
    function mountBoardOverview() {
      if (worktree || !window.WsOverview || !window.wbHistory) return;
      window.WsOverview.mount({
      rid: rid, board: cardBoard, toolbar: window.WsCards.toolbar(),
      keep: window.wbHistory.place, place: window.WsCards.overview, toast: toast, rail: railKey,
      reveal: function () { if (live && !tabShown("board")) setTab("board"); },
      frames: function () { return boardRead(frameHeights); },
      keepFrames: function () { return boardKeep(); },
      restoreFrames: function (h, kept) {
        if (!h || !Object.keys(h).length) { if (kept) kept.h.end(); return; }
        var k = kept || boardKeep();
        boardBoxes().forEach(function (b) { setFrameHeights(b, h); });
        if (k) k.h.apply();
      },
      changed: function () { doc.dispatchEvent(new Event("ws-overview")); },
      onPreview: function (node) {
        if (!node) return;
        if (window.WsBoardAnchor && window.WsBoardAnchor.hydrate) window.WsBoardAnchor.hydrate(node, { immediate: true });
        if (boardOverviewHeld && boardOverviewHeld.html) patchOverviewPreview(boardOverviewHeld.html);
        var cur = (window.WsOverview && window.WsOverview.previewNode && window.WsOverview.previewNode()) || node;
        if (cur) {
          mountFrames(cur);
          q("iframe.doc-frame", cur).forEach(function (f) { postDoc(f, { wbDoc: "size" }); });
        }
      },
      release: function () {
        var h = boardOverviewHeld;
        boardOverviewHeld = null;
        if (h) swap("board", h.hash, h.html, true);
      }
      });
    }
    if (window.WsTerm) window.WsTerm.mount($("bcol"));
    if (window.WsWeb) window.WsWeb.mount($("bcol"));
    if (PIN_WIDE && PIN_WIDE.addEventListener) PIN_WIDE.addEventListener("change", function () { window.WsCards.relayout(); });
    doc.addEventListener("scroll", queueGallery, { capture: true, passive: true });
    window.addEventListener("resize", queueGallery);
    if (window.ResizeObserver) new ResizeObserver(queueGallery).observe($("bcol") || cardBoard);
  }
  boardBoxes().forEach(watchBoard);
  function capturePreviewFocus(root) {
    var act = doc.activeElement;
    if (!act || !root || !root.contains || !root.contains(act)) return null;
    var info = null;
    if (act.matches && act.matches(".tnote[data-topic]")) {
      info = { kind: "tnote", topic: act.getAttribute("data-topic") };
    } else {
      var item = act.closest && act.closest(".card[data-id], .arow[data-id], .ckrow[data-id], .srow[data-id]");
      if (item) {
        var itemId = item.getAttribute("data-id");
        if (act.matches && act.matches("textarea.note")) info = { kind: "note", itemId: itemId };
        else if (act.matches && act.matches("label.ack > input, label.ck > input")) info = { kind: "check", itemId: itemId };
        else if (act.matches && act.matches(".opt[data-opt], .btn[data-v]")) info = { kind: "pick", itemId: itemId, pick: pickKey(act) };
        else if (act.matches && act.matches("button.no-toggle")) info = { kind: "no", itemId: itemId };
      }
    }
    if (!info && act.id) info = { kind: "id", id: act.id };
    if (!info) {
      var list = q("input, textarea, select, button, [contenteditable]", root);
      var idx = list.indexOf(act);
      if (idx >= 0) info = { kind: "idx", tag: act.tagName, idx: idx };
    }
    if (!info) return null;
    try {
      if (typeof act.selectionStart === "number" && typeof act.selectionEnd === "number") {
        info.start = act.selectionStart;
        info.end = act.selectionEnd;
        info.dir = act.selectionDirection || "none";
      }
    } catch (e) {}
    return info;
  }
  function restorePreviewFocus(root, info) {
    if (!root || !info) return;
    var el = null;
    if (info.kind === "tnote") {
      el = q(".tnote[data-topic]", root).filter(function (n) { return n.getAttribute("data-topic") === info.topic; })[0] || null;
    } else if (info.kind === "note" || info.kind === "check" || info.kind === "pick" || info.kind === "no") {
      var item = q(".card[data-id], .arow[data-id], .ckrow[data-id], .srow[data-id]", root).filter(function (n) { return n.getAttribute("data-id") === info.itemId; })[0] || null;
      if (item) {
        if (info.kind === "note") el = item.querySelector("textarea.note");
        else if (info.kind === "check") el = item.querySelector("label.ack > input, label.ck > input");
        else if (info.kind === "pick") el = q(".opt[data-opt], .btn[data-v]", item).filter(function (b) { return pickKey(b) === info.pick; })[0] || null;
        else if (info.kind === "no") el = item.querySelector("button.no-toggle");
      }
    } else if (info.kind === "id") {
      el = q('[id]', root).filter(function (n) { return n.id === info.id; })[0] || null;
    } else if (info.kind === "idx") {
      var list = q("input, textarea, select, button, [contenteditable]", root);
      if (list[info.idx] && list[info.idx].tagName === info.tag) el = list[info.idx];
    }
    if (!el || !el.isConnected || el.hidden) return;
    el.focus({ preventScroll: true });
    if (typeof info.start === "number" && typeof info.end === "number" && typeof el.setSelectionRange === "function") {
      try { el.setSelectionRange(info.start, info.end, info.dir || "none"); } catch (e) {}
    }
  }
  // While Overview is open with Quick Look active, admit keyed patches for the
  // actively previewed topic ONLY so live interactions (e.g. answering an ask)
  // reflect immediately without shifting the Overview tile list in #bcol.
  function patchOverviewPreview(html) {
    if (!window.WsOverview || !window.WsOverview.previewNode) return;
    var liveNode = window.WsOverview.previewNode();
    if (!liveNode || !liveNode.isConnected) return;
    var key = topicKey(liveNode);
    if (key === null) return;
    var incoming = null;
    if (typeof html === "string") {
      var parsed = boardInputFull(html);
      if (parsed && parsed.topics) incoming = parsed.topics[key] || null;
      else {
        Array.prototype.some.call(frag(html).children, function (n) {
          if (topicKey(n) === key) { incoming = n; return true; }
          return false;
        });
      }
    } else if (html && html.topics) {
      incoming = html.topics[key] || null;
    }
    if (!incoming) return;
    var seenKey = "t:" + key, h = topicSeen(incoming);
    if (boardSeen[seenKey] === h) return;
    var held = holdEcho(liveNode, boardRendered && boardRendered.topics ? boardRendered.topics[key] : null, incoming);
    boardSeen[seenKey] = h;
    if (boardRendered && boardRendered.topics) boardRendered.topics[key] = incoming.cloneNode(true);
    if (held) { if (window.wbForm) window.wbForm(); return; }
    var hadOverviewLive = liveNode.hasAttribute("data-overview-live");
    var hadOverviewHere = liveNode.classList.contains("overview-here");
    var focusSaved = capturePreviewFocus(liveNode);
    var answers = readAnswers(liveNode);
    var frameH = frameHeights(liveNode);
    var history = window.wbHistory ? window.wbHistory.capture(liveNode) : null;
    var scrollTop = liveNode.scrollTop;
    var next = incoming.cloneNode(true);
    var g = frameGraftable(liveNode, next);
    var reuse = frameKeep([{ old: liveNode, n: next, g: g }]);
    var target = liveNode;
    if (g) {
      frameGraft(g);
    } else if (liveNode.parentNode) {
      liveNode.parentNode.replaceChild(next, liveNode);
      if (window.WsOverview.replacePreviewNode) window.WsOverview.replacePreviewNode(liveNode, next);
      target = next;
    }
    if (hadOverviewLive) target.setAttribute("data-overview-live", "");
    if (hadOverviewHere) target.classList.add("overview-here");
    frameKept(reuse);
    bandBoard(target);
    if (window.wbHistory) { window.wbHistory.init(target); window.wbHistory.restore(target, history); }
    watchBoard(target);
    restoreOpen(target);
    fmtTimes(target);
    sweepOutImgs(target);
    setFrameHeights(target, frameH);
    mountFrames(target);
    writeAnswers(target, answers);
    q(".doc-frame", target).forEach(function (f) { f.wbKeep = true; });
    if (window.WsCards && window.WsCards.decorate) window.WsCards.decorate();
    if (hadOverviewLive) target.setAttribute("data-overview-live", "");
    if (hadOverviewHere) target.classList.add("overview-here");
    indexFrames(target);
    if (window.wbForm) window.wbForm();
    mermaid([target]);
    applySent(true);
    if (window.wbPill && window.wbPill.syncTopicBars) window.wbPill.syncTopicBars();
    if (window.WsOverview && window.WsOverview.syncPlaceholder) window.WsOverview.syncPlaceholder(target, true);
    restorePreviewFocus(target, focusSaved);
    target.scrollTop = scrollTop;
  }
  // A hold flip's echo re-renders its topic with only the hold markers changed
  // (pill.js holdOnly): when the live section shows the previous input, it
  // takes the new markers in place rather than being replaced, which would
  // reload its frames and lay the free canvas out around a half-built card.
  function holdEcho(old, prev, n) {
    var p = window.wbPill;
    if (!prev || !p || !p.holdOnly || old.hasAttribute("data-wb-virtual") || boardSeen["t:" + topicKey(old)] !== topicSeen(prev) || !p.holdOnly(prev, n)) return false;
    p.adoptHolds(old, n);
    old.setAttribute("data-th", n.getAttribute("data-th"));
    return true;
  }
  // morphBoard applies a board region event to el and returns the element
  // nodes it inserted, in order, for the caller to prepare.
  function morphBoard(el, html, prev) {
    var seen = {}, want = [], ins = [], live = {}, liveClosed = [], pages = [], more = null, grafts = [], changed = [];
    var prevTopics = prev && typeof prev !== "string" ? prev.topics : null;
    var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("board", "morphBoard") : null;
    try {
    var docked = window.WsCards.docked();
    Array.prototype.forEach.call(el.children, function (n) {
      var k = topicKey(n);
      if (k !== null) { live["t:" + k] = n; if (n.classList.contains("closed")) liveClosed.push(n); }
      else if (n.classList.contains("closed-page")) pages.push(n);
    });
    // The docked topics (cards.js) stand in the gallery's panes, on every
    // page. A closed one the last board showed counts as the board's own
    // closed topic for the pages below (pinShift).
    var pinShift = [];
    docked.forEach(function (n) {
      var k = topicKey(n);
      if (k === null) return;
      live["t:" + k] = n;
      if (n.classList.contains("closed") && boardSeen["t:" + k] !== undefined) pinShift.push(n);
    });
    boardNodes(html, live).forEach(function (n) {
      var k = topicKey(n);
      if (k === null) {
        want.push(n);
        if (n.nodeType !== 1) return;
        ins.push(n);
        if (n.matches("button.closed-more[data-closed-next]")) more = n;
        return;
      }
      var h = topicSeen(n), key = "t:" + k;
      seen[key] = h;
      // A topic that closes while the page shows it folds once: the
      // server folds a closed topic's body and settled rows under the same
      // keys an open topic uses, so the user's earlier choices for them are
      // forgotten here, and later toggles are kept again.
      var newlyClosed = !!(live[key] && n.classList.contains("closed") && !live[key].classList.contains("closed"));
      if (newlyClosed) { forgetOpen(live[key]); forgetOpen(n); }
      if (live[key] && (boardSeen[key] === h || holdEcho(live[key], prevTopics && prevTopics[k], n))) { want.push(live[key]); delete live[key]; }
      else if (live[key] && live[key].hasAttribute("data-wb-virtual") && !window.WsCards.isDocked(live[key]) && typeof virtualStashUpdate === "function" && virtualStashUpdate(live[key], n)) {
        want.push(live[key]); delete live[key];
      } else {
        if (live[key] && live[key].hasAttribute("data-wb-virtual")) {
          var taken = typeof virtualTakeCollapsed === "function" ? virtualTakeCollapsed(k) : null;
          if (!taken && typeof virtualDropCollapsed === "function") virtualDropCollapsed(k);
          if (taken) {
            if (newlyClosed) {
              taken.openDetails = null;
              if (taken.stashed) forgetOpen(taken.stashed);
              if (taken.children) taken.children.forEach(function (c) { if (c && c.nodeType === 1) forgetOpen(c); });
            }
            n.__wbVirtualTaken = taken;
          }
        }
        // A docked topic's live section wraps its body (cards.js wrap): the
        // incoming one takes the same wrapper first, so the graft's paths
        // match and its body frame stays live.
        if (live[key] && window.WsCards.isDocked(live[key])) window.WsCards.wrap(n);
        var g = live[key] && frameGraftable(live[key], n);
        if (g) grafts.push(g);
        if (live[key]) changed.push({ old: live[key], n: n, g: g });
        want.push(n); ins.push(n);
      }
    });
    boardSeen = seen;
    // Closed topics fetched past the cap (closed pages, each nested in the
    // one before it) stay across swaps in the new control's place, since the
    // board renders only its capped section again. Closed-topic indices
    // follow close order, so a topic that newly closes pushes the oldest
    // capped one past the cap: that one moves to the head of the pages, a
    // fetched topic the new board shows itself leaves them, and the pages'
    // own control counts on from what is shown. No topic shows twice, and
    // none drops out between the capped section and the pages. A board with
    // no control holds every closed topic, and the pages go. A docked topic
    // (cards.js) stays in the gallery: in the pages a slot (closed-slot)
    // stands in its place and counts as it would.
    if (pages.length && more) {
      var shown = {}, shifted = liveClosed.filter(function (n) { return !seen["t:" + topicKey(n)]; }), count = shifted.length;
      var slots = pinShift.filter(function (n) { return !seen["t:" + topicKey(n)]; });
      count += slots.length;
      Object.keys(seen).forEach(function (key) { shown[key] = true; });
      shifted.concat(slots).forEach(function (n) { shown["t:" + topicKey(n)] = true; });
      pages.forEach(function (p) {
        q(".closed-page > section.topic[data-topic], .closed-page > .closed-slot[data-slot]", p).forEach(function (t) {
          var key = "t:" + (t.getAttribute("data-topic") || t.getAttribute("data-slot"));
          if (shown[key]) t.parentNode.removeChild(t);
          else { shown[key] = true; count++; }
        });
      });
      var anchor = pages[0].firstChild;
      shifted.forEach(function (n) { pages[0].insertBefore(n, anchor); });
      slots.forEach(function (n) {
        var s = doc.createElement("span");
        s.className = "closed-slot";
        s.hidden = true;
        s.setAttribute("data-slot", topicKey(n));
        pages[0].insertBefore(s, anchor);
      });
      var tail = q("button.closed-more[data-closed-next]", pages[pages.length - 1]).pop();
      if (tail) {
        tail.setAttribute("data-closed-next", String(+more.getAttribute("data-closed-next") + count));
        tail.setAttribute("data-closed-more", String(Math.max(0, (+more.getAttribute("data-closed-more") || 0) - count)));
      }
      want.splice.apply(want, [want.indexOf(more), 1].concat(pages));
      ins.splice(ins.indexOf(more), 1);
    }
    // The page's own cards (cards.js) stand among the topics: they are
    // woven into the wanted nodes at their places, and the board settles
    // around them. Stale nodes go first, so the kept ones that stay in
    // order are never moved (moving a frame reloads it), and a card is
    // never moved at all. A changed topic whose old section stays in place
    // keeps that section and its body frame (frameGraft); where the browser
    // has Element.moveBefore, a kept node moves without reloading its
    // frames, so every changed topic keeps its old section (frameGrafts).
    // The docked topics go to their panes, where an update moves none of
    // them, so a changed one whose old section stands there keeps it, the
    // wrapper (.gal-pbody) its graft target.
    var plan = window.WsCards.weave(want);
    var woven = plan.board;
    // A changed topic's unchanged entry frames are kept, moved aside before
    // the grafts and the settle would detach them (frameKeep), and moved
    // into their placeholders' places once the board has settled (frameKept).
    var reuse = frameKeep(changed);
    var pinGrafts = grafts.filter(function (g) { return plan.pins.indexOf(g.n) >= 0 && window.WsCards.isDocked(g.old); });
    frameGrafts(el, woven, grafts.filter(function (g) { return plan.pins.indexOf(g.n) < 0; })).concat(pinGrafts).forEach(function (g) {
      frameGraft(g);
      var list = woven.indexOf(g.n) >= 0 ? woven : plan.pins;
      list[list.indexOf(g.n)] = g.old;
      ins[ins.indexOf(g.n)] = g.old;
    });
    window.WsCards.settle(el, plan);
    frameKept(reuse);
    if (typeof virtualPrune === "function") virtualPrune();
    return ins;
    } finally {
      if (jTok) WsJank.end(jTok, { count: ins.length });
    }
  }
  // --- incremental board inputs ---------------------------------------------
  // These are immutable incoming snapshots, not the live DOM. Hidden pages,
  // editing, overview and dragging may defer several events; the newest input
  // must include all earlier changes even though none has reached morphBoard.
  // Canonical nodes are parsed once, then cloned only for changed sections.
  function boardInputFull(html) {
    var nodes = Array.prototype.slice.call(frag(html).childNodes), topics = Object.create(null), order = [], before = [], after = [], past = false;
    for (var i = 0; i < nodes.length; i++) {
      var n = nodes[i], key = topicKey(n);
      if (key === null) { (order.length ? after : before).push(n); if (order.length && n.nodeType === 1) past = true; continue; }
      if (past || !n.getAttribute("data-th") || Object.prototype.hasOwnProperty.call(topics, key)) return null;
      topics[key] = n; order.push(key);
    }
    return { topics: topics, order: order, before: before, after: after };
  }
  function boardInputPatch(prev, ev) {
    if (!prev || !ev || typeof ev.hash !== "string" || !Array.isArray(ev.order) || !Array.isArray(ev.topics)) return null;
    var topics = Object.create(null), changes = Object.create(null);
    for (var i = 0; i < ev.topics.length; i++) {
      var t = ev.topics[i];
      if (!t || typeof t.key !== "string" || typeof t.hash !== "string" || typeof t.html !== "string" || Object.prototype.hasOwnProperty.call(changes, t.key)) return null;
      var parsed = frag(t.html), n = parsed.firstElementChild;
      if (!n || parsed.childNodes.length !== 1 || topicKey(n) !== t.key || n.getAttribute("data-th") !== t.hash) return null;
      changes[t.key] = n;
    }
    for (var j = 0; j < ev.order.length; j++) {
      var key = ev.order[j];
      if (typeof key !== "string" || Object.prototype.hasOwnProperty.call(topics, key)) return null;
      var node = changes[key] || prev.topics[key];
      if (!node) return null;
      topics[key] = node;
    }
    if (Object.keys(changes).some(function (key) { return !Object.prototype.hasOwnProperty.call(topics, key); })) return null;
    return { topics: topics, order: ev.order.slice(), before: prev.before, after: prev.after };
  }
  function boardNodes(input, live) {
    if (typeof input === "string") return Array.prototype.slice.call(frag(input).childNodes);
    function clone(n) { return n.cloneNode(true); }
    return input.before.map(clone).concat(input.order.map(function (key) {
      var n = input.topics[key], old = live && live["t:" + key];
      return old && boardSeen["t:" + key] === topicSeen(n) ? old : clone(n);
    }), input.after.map(clone));
  }
  function boardInputFragment(input) {
    var f = doc.createDocumentFragment();
    boardNodes(input).forEach(function (n) { f.appendChild(n); });
    return f;
  }
  // --- keyed frame reuse: a changed topic keeps its unchanged entry frames --
  // An entry frame (data-doc-key entry:eN) is immutable per entry, so inside
  // a changed topic an incoming entry placeholder takes the old topic's
  // mounted frame of the same data-doc-key and the same data-srcdoc (the
  // same key need not mean the same document: image inlining and relay
  // lists change it); each old frame pairs once, so duplicate keys pair off
  // one by one. Only Element.moveBefore keeps a frame's document across a
  // move, and only between connected nodes: frameKeep moves the paired old
  // frames into a hidden holder on the page before anything detaches them,
  // and frameKept moves each into its placeholder's place, the placeholder
  // dropped, once the new section stands on the page. A placeholder that
  // stays unpaired is mounted as usual (mountFrames). A frame a graft holds
  // (frameGraft's chain) and the topic-body frame (topic:, a stable key over
  // changing content) are never paired. Without moveBefore nothing is kept.
  function frameKeep(changed) {
    if (!changed.length || !doc.body || !doc.body.moveBefore) return null;
    var pairs = [];
    changed.forEach(function (c) {
      // Timeline anchors can become inert gaps after an append. Replay only
      // already-admitted rows before looking for their frame placeholders;
      // the normal post-morph restore still owns folds, mounting and layout.
      if (window.wbHistory) window.wbHistory.prepare(c.n, window.wbHistory.capture(c.old));
      function frameKeepKey(el) {
        if (el.hasAttribute("data-mockup-sha")) return "m\n" + el.getAttribute("data-doc-key") + "\n" + el.getAttribute("data-mockup-sha");
        if (el.hasAttribute("data-srcdoc")) return "s\n" + el.getAttribute("data-doc-key") + "\n" + el.getAttribute("data-srcdoc");
        return "";
      }
      var held = c.g ? c.g.oc[0] : null, olds = {};
      q('iframe.doc-frame[data-doc-key^="entry:"]:not(.doc-frame-next)', c.old).forEach(function (f) {
        if (f === held) return;
        var k = frameKeepKey(f);
        if (!k) return;
        (olds[k] = olds[k] || []).push(f);
      });
      q('div.doc-frame[data-doc-key^="entry:"]', c.n).forEach(function (p) {
        if (c.g && p === c.g.nc[0]) return;
        var k = frameKeepKey(p);
        var list = k ? olds[k] : null;
        if (list && list.length) pairs.push({ f: list.shift(), p: p });
      });
    });
    if (!pairs.length) return null;
    var holder = doc.createElement("div");
    holder.hidden = true;
    doc.body.appendChild(holder);
    pairs.forEach(function (x) { holder.moveBefore(x.f, null); });
    return { holder: holder, pairs: pairs };
  }
  function frameKept(r) {
    if (!r) return;
    r.pairs.forEach(function (x) {
      var at = x.p.parentNode;
      if (!at || !x.p.isConnected) return;
      at.moveBefore(x.f, x.p);
      at.removeChild(x.p);
      if (x.f.hasAttribute && (x.f.hasAttribute("data-mockup-src") || x.f.classList && x.f.classList.contains("mockup-frame")) && window.WsCards && window.WsCards.syncMockupElement) {
        window.WsCards.syncMockupElement(x.f);
      }
    });
    r.holder.parentNode.removeChild(r.holder);
  }
  // --- a changed topic's body frame, double-buffered --------------------------
  // A changed topic's new section would bring a new body frame, blank until
  // its document loads and reports a height. Instead, when the topic stays
  // open and its old section would stay where it stands, or the browser
  // can move it without reloading its frame (frameGrafts), the old section
  // is kept: its content becomes the new section's, except the
  // path down to the old body frame, and the new frame loads beside the old
  // one, unseen and out of the layout (.doc-frame-next), at the old one's
  // height. Once the new frame has loaded and drawn, it takes the old one's
  // place in the same frame (frameShow, frameReveal); one that never loads
  // shows after frameRevealMs anyway.
  var frameRevealMs = 1500;
  // frameChain is the path from frame f up to section sec, f first, or null.
  function frameChain(f, sec) {
    var c = [];
    for (var x = f; x && x !== sec; x = x.parentNode) c.push(x);
    return x === sec ? c.concat(sec) : null;
  }
  // frameGraftable returns the graft of new section n into old section old
  // (both open, each with a body frame of the same topic document on paths
  // of the same shape), or null.
  function frameGraftable(old, n) {
    if (old.classList.contains("closed") || n.classList.contains("closed")) return null;
    var of = q(".doc-frame:not(.doc-frame-next)", old)[0], nf = q(".doc-frame", n)[0];
    if (!of || !nf || of.getAttribute("data-doc") !== nf.getAttribute("data-doc")) return null;
    // A panel-only topic's first frame is an entry, not a body to reload:
    // leave it to frameKeep's key-and-document match like every other panel.
    if (!/^topic:/.test(of.getAttribute("data-doc-key") || "")) return null;
    var oc = frameChain(of, old), nc = frameChain(nf, n);
    if (!oc || !nc || oc.length !== nc.length) return null;
    // The frames themselves may differ in kind (a mounted iframe, an
    // incoming placeholder: frameGraft mounts it).
    for (var i = 1; i < oc.length; i++) if (oc[i].tagName !== nc[i].tagName) return null;
    return { old: old, n: n, oc: oc, nc: nc };
  }
  // frameGrafts returns the grafts that move nothing settling el into want
  // would not move anyway, each graft's new section standing for its old
  // one: moving a frame reloads it, so a topic whose old section would move
  // (an update moves its topic to the head), or would push kept topics
  // around it, takes its new section as before. When the grafts together
  // move something, each one that alone moves nothing is kept, if those
  // together still move nothing. Where the browser has Element.moveBefore,
  // settle moves a kept node without reloading its frames (cards.js), so
  // every graft is kept, whatever moves.
  function frameGrafts(el, want, grafts) {
    if (!grafts.length || el.moveBefore) return grafts;
    var base = settleMoves(el, want);
    function still(gs) {
      var by = new Map(gs.map(function (g) { return [g.n, g.old]; })), ok = true;
      settleMoves(el, want.map(function (n) { return by.get(n) || n; })).forEach(function (n) { if (!base.has(n)) ok = false; });
      return ok;
    }
    if (still(grafts)) return grafts;
    var each = grafts.filter(function (g) { return still([g]); });
    return each.length && still(each) ? each : [];
  }
  // settleMoves returns the nodes already in el that settling el into want
  // moves. It replays WsCards.settle (cards.js) on a list: keep the two in
  // step.
  function settleMoves(el, want) {
    var keep = new Set(want), moved = new Set(), i = 0;
    var cur = Array.prototype.filter.call(el.childNodes, function (n) { return keep.has(n); });
    want.forEach(function (n) {
      if (cur[i] === n) { i++; return; }
      var j = cur.indexOf(n);
      if (j >= 0 && n.nodeType === 1 && n.hasAttribute("data-card")) {
        var past = cur.splice(i, j - i);
        past.forEach(function (p) { moved.add(p); });
        cur.splice.apply(cur, [i + 1, 0].concat(past));
      } else {
        if (j >= 0) { moved.add(n); cur.splice(j, 1); }
        cur.splice(i, 0, n);
      }
      i++;
    });
    return moved;
  }
  // frameGraft moves g's new section content into its old section, level by
  // level down the frame's path: each kept element takes the new element's
  // attributes and children, except its child on the path, which stays.
  // At the bottom an unchanged document stays live; a changed frame goes
  // in after the old one, pending.
  function frameGraft(g) {
    var of = g.oc[0], mounted = of.tagName === "IFRAME";
    var same = of.getAttribute("data-doc-key") === g.nc[0].getAttribute("data-doc-key") && of.getAttribute("data-srcdoc") === g.nc[0].getAttribute("data-srcdoc");
    if (same) of.wbOut = false;
    // The next frame of a mounted frame is mounted before it goes in, and
    // before anything holds it (expandos, the load listener): a placeholder
    // would show nothing to reveal. The next frame of an unmounted one (a
    // closed fold, a topic far from the view) is a placeholder that simply
    // takes the old one's place: the topic mounts it once it comes near
    // (viewport bands).
    if (!same && mounted && g.nc[0].tagName !== "IFRAME") g.nc[0] = mountFrame(g.nc[0]);
    var nf = g.nc[0];
    if (!same && mounted) frameNext(nf, of);
    for (var i = g.oc.length - 1; i > 0; i--) {
      var oa = g.oc[i], na = g.nc[i], oc = g.oc[i - 1], nc = g.nc[i - 1], before = true;
      var keepOverviewLive = i === g.oc.length - 1 && oa.hasAttribute("data-overview-live");
      var keepOverviewHere = i === g.oc.length - 1 && oa.classList.contains("overview-here");
      Array.prototype.slice.call(oa.attributes).forEach(function (a) { if (!na.hasAttribute(a.name)) oa.removeAttribute(a.name); });
      Array.prototype.forEach.call(na.attributes, function (a) { if (oa.getAttribute(a.name) !== a.value) oa.setAttribute(a.name, a.value); });
      if (keepOverviewLive) oa.setAttribute("data-overview-live", "");
      if (keepOverviewHere) oa.classList.add("overview-here");
      Array.prototype.slice.call(oa.childNodes).forEach(function (c) { if (c !== oc) oa.removeChild(c); });
      Array.prototype.slice.call(na.childNodes).forEach(function (c) {
        if (c === nc) { before = false; if (c === nf && !same) oa.appendChild(c); }
        else if (before) oa.insertBefore(c, oc);
        else oa.appendChild(c);
      });
    }
    if (!same && !mounted) of.parentNode.removeChild(of);
  }
  // frameNext makes nf the pending next frame of mounted frame of.
  function frameNext(nf, of) {
    nf.classList.add("doc-frame-next");
    nf.setAttribute("tabindex", "-1");
    nf.setAttribute("aria-hidden", "true");
    nf.wbNext = true;
    nf.wbOld = of;
    of.wbOut = true;
    nf.addEventListener("load", function () { frameShow(nf); });
    setTimeout(function () { frameReveal(nf); }, frameRevealMs);
  }
  // frameShow reveals pending frame f once it has loaded and drawn: a
  // frame revealed in the frame it loads in can still show blank for a
  // frame, so the reveal waits two frames more.
  function frameShow(f) {
    if (f.wbShowing) return;
    f.wbShowing = true;
    requestAnimationFrame(function () { requestAnimationFrame(function () { frameReveal(f); }); });
  }
  // frameReveal shows pending frame f in its old frame's place, at the old
  // frame's height, and asks it for its height, which the frame message
  // handler sets (easing to it in a moving swap, wbGrow). A frame no longer
  // on the page (a later swap replaced it) is left alone.
  function frameReveal(f) {
    if (!f.wbNext || !f.isConnected) return;
    var w = window.wbHistory, kept = w && w.keep ? w.keep(f) : null;
    f.wbNext = false;
    f.classList.remove("doc-frame-next");
    f.removeAttribute("tabindex");
    f.removeAttribute("aria-hidden");
    var o = f.wbOld;
    f.wbOld = null;
    if (o && o.parentNode) o.parentNode.removeChild(o);
    if (kept) kept.apply();
    postDoc(f, { wbDoc: "size" });
  }
  function reducedMotion() { return !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches); }
  // Owner captures of inner scrollers only: the current card (a history load or
  // a frame height in it moves what its reader sees) and the docked panes. The
  // board's outer scroll is never captured; the current card's key is its
  // state. A node of another board card is not read at all.
  function boardKeep(nodes) {
    var w = window.wbHistory, handles = [], scrollers = new Set(), resolved = new Map(), firsts = [];
    if (!w || !w.keep) return null;
    var cur = anchorCurrent(), main = region("board");
    var inOverview = !nodes && typeof root !== "undefined" && root && root.hasAttribute && root.hasAttribute("data-overview");
    var mainItems = inOverview ? [] : (cur ? [cur.firstElementChild || cur] : main && !nodes && main.firstElementChild ? [main.firstElementChild] : []);
    var list = nodes || boardBoxes().filter(function (b) { return b !== main; }).map(function (b) { return b.firstElementChild || b; })
      .concat(mainItems);
    var fallback = !inOverview && !cur && main && !nodes ? main.firstElementChild || null : null;
    var lookup = w.snapshot && w.snapshot();
    list.forEach(function (n) {
      if (main && main.contains(n) && n !== fallback && anchorOwner(n) !== cur) return;
      if (!(lookup && lookup.rendered ? lookup.rendered(n) : !w.rendered || w.rendered(n))) return;
      var s = lookup ? lookup.scroller(n) : w.keep(n, { capture: false }).scroller;
      resolved.set(n, s);
      if (scrollers.has(s)) return;
      scrollers.add(s);
      firsts.push({ n: n, s: s });
    });
    if (w.own) w.own(firsts.map(function (f) { return f.s; }));
    firsts.forEach(function (f) { handles.push(w.keep(f.n, { scroller: f.s })); });
    return { scroller: function (n) { return resolved.get(n); }, h: { apply: function () { handles.forEach(function (h) { h.apply(); }); }, end: function () { handles.forEach(function (h) { h.end(); }); } } };
  }
  // A reload keeps only the current card's key (dashboard-board-anchor.js).
  function boardSaveReload() {}
  function boardRestoreReload() { anchorInit(); }
  if (framed) window.wsSaveBoardReload = boardSaveReload;
  // A staging pass reads exposure through one wbHistory.snapshot: nothing
  // writes the DOM inside it, so each scroller's viewport (a scan of every
  // sticky head) is measured once rather than once per node checked.
  // The snapshot also freezes rendered() per node for the pass, so nothing
  // inside may hide a node or toggle a details.
  function boardWithSnap(fn) {
    var outer = boardSnap, w = window.wbHistory;
    if (!outer && w && w.snapshot) boardSnap = w.snapshot();
    try { return fn(); } finally { boardSnap = outer; }
  }
  function boardExposed(n) {
    if (!n || n.isConnected === false) return false;
    var w = window.wbHistory, main = region("board");
    if (main && main.contains(n)) return anchorExposed(n);
    if (boardSnap) return boardSnap.visible(n);
    if (w && w.rendered && !w.rendered(n)) return false;
    return !!(w && w.keep && w.keep(n, { capture: false }).visible(n));
  }
  function boardScroller(n) {
    var w = window.wbHistory;
    if (!n || (w && w.rendered && !(boardSnap && boardSnap.rendered ? boardSnap.rendered(n) : w.rendered(n)))) return null;
    return boardSnap ? boardSnap.scroller(n) : w && w.keep ? w.keep(n, { capture: false }).scroller : null;
  }
  // Canonical panel/article bytes, including inert history pages. Live DOM
  // is used only for exposure/identity, never as an incoming snapshot.
  function boardPanelNodes(n) {
    var nodes = q(".entry-panel, .wb-entry:not(.panel), .doc-frame", n).filter(function (p) {
      return !p.classList.contains("doc-frame") || !p.closest(".entry-panel");
    });
    q("template", n).forEach(function (t) { if (t.content) nodes = nodes.concat(boardPanelNodes(t.content)); });
    return nodes;
  }
  // The mounted panels only: inert history pages are never exposed. admit,
  // when given, rejects a panel before any visibility read of it.
  function boardLivePanels(n, admit) {
    var w = window.wbHistory;
    return q(".entry-panel, .wb-entry:not(.panel), .doc-frame", n).filter(function (p) {
      return (!admit || admit(p)) && (!w || !w.rendered || w.rendered(p)) && (!p.classList.contains("doc-frame") || !p.closest(".entry-panel"));
    });
  }
  function boardPanelKey(n) { return n.id || n.getAttribute("data-key") || n.getAttribute("data-entry") || n.getAttribute("data-doc-key"); }
  function boardPanelMap(list) {
    var m = new Map();
    list.forEach(function (p) { var k = boardPanelKey(p); if (!m.has(k)) m.set(k, p); });
    return m;
  }
  function boardRenderBlockers(live, old, incoming) {
    var exposed = boardLivePanels(live).filter(boardExposed);
    if (!incoming) return exposed.length ? exposed : boardExposed(live) ? [live] : [];
    var before = boardPanelNodes(old), after = boardPanelNodes(incoming), was = boardPanelMap(before), now = boardPanelMap(after);
    var changed = exposed.filter(function (p) {
      var key = boardPanelKey(p), a = was.get(key), b = now.get(key);
      return !a || !b || a.outerHTML !== b.outerHTML;
    });
    if (exposed.length > 1) {
      var keys = exposed.map(boardPanelKey);
      var span = function (list) {
        var indices = list.map(function (p, i) { return keys.indexOf(boardPanelKey(p)) >= 0 ? i : -1; }).filter(function (i) { return i >= 0; });
        return indices.length ? list.slice(Math.min.apply(Math, indices), Math.max.apply(Math, indices) + 1).map(function (p) { return p.outerHTML; }).join("\n") : "";
      };
      if (span(before) !== span(after)) return exposed;
    }
    return changed;
  }
  function boardRecordPending() {
    boardChanges = boardChanges.filter(function (c) { return c.names.some(function (k) { return boardPending[k]; }); });
    if (boardRelease) { boardChanges = []; return; }
    var offer = boardOffered;
    boardOffered = null;
    if (!offer || !offer.input) return;
    var previous = offer.previous, input = offer.input, content = [];
    Object.keys(boardPending).forEach(function (k) {
      var old = previous && previous.topics[k], next = input.topics[k];
      if (!old || !next || topicSeen(old) !== topicSeen(next)) content.push(k);
    });
    content.forEach(function (k) { boardChanges.push({ names: [k] }); });
    if (!content.length && previous && previous.order.join("\n") !== input.order.join("\n") && Object.keys(boardPending).length)
      boardChanges.push({ names: Object.keys(boardPending) });
  }
  // A send grants no blanket exemption to its topic: a simultaneous agent
  // rewrite is still external. Server-rendered owner receipts identify the
  // new rows. Admit them alone when the same snapshot also changes panels.
  function boardOwnerSnapshot(old, next) {
    if (!old || !next) return null;
    var known = new Set(q('.wb-entry.owner', old).map(function (n) { return n.id; }));
    var arrivals = q('.wb-entry.owner', next).filter(function (n) { return !known.has(n.id); });
    if (!arrivals.length) return null;
    function external(n) { return !n.classList.contains('owner'); }
    var before = boardPanelNodes(old).filter(external), after = boardPanelNodes(next).filter(external);
    var changed = before.length !== after.length || before.some(function (n, i) { return boardPanelKey(n) !== boardPanelKey(after[i]) || n.outerHTML !== after[i].outerHTML; });
    if (!changed) return { source: next, external: false };
    var partial = old.cloneNode(true), admitted = [];
    arrivals.forEach(function (n) {
      var h = n.closest('.wb-history'), key = h && h.getAttribute('data-history');
      var target = q('.wb-history', partial).find(function (p) { return p.getAttribute('data-history') === key; });
      if (target) { target.querySelector('.history-live').appendChild(n.cloneNode(true)); admitted.push(n.id); }
    });
    if (!admitted.length) return null;
    partial.setAttribute('data-th', topicSeen(old) + ':owner:' + admitted.join(','));
    return { source: partial, external: true };
  }
  function boardStageInput(input) { return boardWithSnap(function () { return boardStageSnap(input); }); }
  // Staging is viewport-scoped: every card intersecting #bcol's visible
  // viewport (plus any card holding an active editor) keeps its previous bytes
  // while a change to it is pending. Off-viewport buffer cards and the
  // canonical order are admitted at once.
  function boardGuards() {
    var keys = [], visible = typeof anchorVisibleCards === "function" ? anchorVisibleCards() : [anchorCurrent()];
    var a = editing() ? doc.activeElement : null;
    var ed = a && a.closest ? a.closest("section.topic[data-topic]") : null;
    visible.concat(ed ? [ed] : []).forEach(function (n) {
      var k = n && topicKey(n);
      if (k && keys.indexOf(k) < 0) keys.push(k);
    });
    return keys;
  }
  function boardStageSnap(input) {
    boardPending = Object.create(null);
    boardImmediate = Object.create(null);
    if (!input || typeof input === "string") return input;
    var topics = Object.assign(Object.create(null), input.topics), order = input.order.slice();
    Object.keys(boardOwn).forEach(function (k) {
      var old = boardRendered && boardRendered.topics[k] || boardPaged[k];
      var own = boardOwnerSnapshot(old, topics[k]);
      if (!own) return;
      topics[k] = own.source; boardImmediate[k] = true; delete boardOwn[k];
      if (own.external) boardPending[k] = true;
    });
    if (!boardRelease) boardGuards().forEach(function (k) {
      var old = boardRendered && boardRendered.topics[k], next = topics[k], live = anchorNode("t:" + k);
      if (boardImmediate[k] || !old || !next || !live || topicSeen(old) === topicSeen(next)) return;
      var a = doc.activeElement, held = editing() && a && live.contains(a);
      var blockers = held ? [live] : boardRenderBlockers(live, old, next);
      if (!blockers.length) return;
      topics[k] = old;
      if (order.indexOf(k) < 0) order.push(k);
      boardPending[k] = blockers;
    });
    // Docked units keep their panes whatever the board does, so only their
    // content follows the same per-panel rendering blockers.
    var docked = window.WsCards && window.WsCards.docked ? window.WsCards.docked() : [];
    if (!boardRelease) {
      docked.filter(function (n) { return !boardImmediate[topicKey(n)] && boardExposed(n); }).forEach(function (n) {
        var k = topicKey(n), old = boardRendered && boardRendered.topics[k];
        if (k === null || !old || !topics[k] || topicSeen(old) === topicSeen(topics[k])) return;
        var blockers = boardRenderBlockers(n, old, topics[k]);
        if (blockers.length) { topics[k] = old; boardPending[k] = blockers; }
      });
    }
    // A partial input records only bytes actually admitted, never held hashes.
    boardRendered = { topics: topics, order: order, before: input.before, after: input.after };
    return boardRendered;
  }
  function boardPendingNames() {
    var names = new Set();
    boardChanges.forEach(function (c) { c.names.forEach(function (k) { if (boardPending[k]) names.add(k); }); });
    return Array.from(names);
  }
  function boardPendingBadge() {
    var pill = window.wbPill || (window.wbPill = {});
    pill.pending = boardPendingNames().length;
    pill.home = boardHome;
    // Explicit board navigation acknowledges the same staged snapshot as home,
    // without starting a competing scroll-to-top operation.
    pill.apply = boardApplyPending;
    if (pill.update) pill.update();
  }
  function boardHeldNow() { return !!(deferred || boardDragHeld || boardOverviewHeld); }
  // An explicit apply reports completion only once the pending revision is on
  // the board: while a protected interaction holds the swap, retry waits for it.
  function boardApplyPending(retry) {
    var overview = window.WsOverview;
    if (overview && overview.holding()) {
      if (retry) overview.after(retry);
      return false;
    }
    if (Object.keys(boardPending).length) boardFlushViewed(true);
    if (boardHeldNow()) {
      if (retry) boardAfterApply.push(retry);
      return false;
    }
    return true;
  }
  function boardApplied() {
    if (boardHeldNow() || !boardAfterApply.length) return;
    var list = boardAfterApply;
    boardAfterApply = [];
    list.forEach(function (fn) { fn(); });
  }
  // Only a cached shell frame's return gets this exemption, not a browser
  // tab visibility change. Restore may apply old held bytes first; keep the
  // window for the reconnect's first snapshot (or a bounded no-change wait).
  function boardReturnShown() {
    boardReturnEnd();
    boardReturnTimer = setTimeout(boardReturnEnd, 1500);
    boardReturnApply();
  }
  function boardReturnEnd() {
    clearTimeout(boardReturnTimer);
    boardReturnTimer = 0;
    boardReturnFresh = false;
  }
  function boardReturnApply() {
    if (!boardReturnTimer) return;
    if (boardApplyPending(boardReturnApply) && boardReturnFresh && !Object.keys(boardPending).length) boardReturnEnd();
  }
  function boardFlash(names, inView) {
    if (typeof boardReturnTimer !== "undefined" && boardReturnTimer) return;
    var targets = [];
    boardBoxes().forEach(function (box) {
      q('section.topic[data-topic]', box).forEach(function (n) {
        if (names.indexOf(topicKey(n)) < 0 || (inView && !boardExposed(n))) return;
        targets.push(n);
      });
    });
    if (!targets.length) return;
    targets.forEach(function (n) { n.classList.remove('board-fresh'); });
    void targets[0].offsetWidth;
    targets.forEach(function (n) {
      n.classList.add('board-fresh');
      clearTimeout(n.wbFreshTimer);
      n.wbFreshTimer = setTimeout(function () { n.classList.remove('board-fresh'); }, 650);
    });
  }
  // The up-arrow applies the current card's pending updates, then selects the
  // first topic in Main Board order the filter shows (the applied revision's
  // order); with none eligible the current card stays.
  function boardHome() {
    var names = boardPendingNames(), generation = anchorUserScroll;
    function go() {
      var first = anchorCards().filter(function (n) { return topicKey(n) !== null; })[0];
      if (first && generation === anchorUserScroll) anchorSelect(first, { smooth: true });
      boardFlash(names, false);
    }
    if (boardApplyPending(go)) go();
  }
  function boardHoldIndicators() {
    var names = boardPendingNames(), input = boardLatest && boardLatest.html;
    boardBoxes().forEach(function (box) {
      q(names.length ? 'section.topic[data-topic]' : 'section.topic[data-board-held]', box).forEach(function (n) {
        var k = topicKey(n), held = names.indexOf(k) >= 0 || !!(boardPending[k] && n.hasAttribute('data-board-held'));
        if (!held && !n.hasAttribute('data-board-held')) return;
        var tag = n.querySelector('.board-hold-tag');
        if (!held) {
          n.removeAttribute('data-board-held'); n.classList.remove('board-hold-nudge');
          if (n.wbHoldNudge) cancelAnimationFrame(n.wbHoldNudge);
          if (tag) tag.remove();
          delete n.wbHoldRevision;
          return;
        }
        var incoming = input && input.topics[k], closed = !incoming || incoming.classList.contains('closed');
        n.setAttribute('data-board-held', closed ? 'closed' : 'moved');
        if (!tag) {
          var bar = n.querySelector('.tbar');
          if (!bar) return;
          tag = doc.createElement('span'); tag.className = 'board-hold-tag'; bar.appendChild(tag);
        }
        tag.textContent = closed ? wsT("js.board.hold.closed") : wsT("js.board.hold.moved");
        var revision = incoming ? topicSeen(incoming) : 'closed';
        if (n.wbHoldRevision !== revision) {
          n.wbHoldRevision = revision;
          n.classList.remove('board-hold-nudge');
          if (n.wbHoldNudge) cancelAnimationFrame(n.wbHoldNudge);
          // Restart after a painted reset, not a forced whole-board layout
          // inside the synchronous stream admission transaction.
          n.wbHoldNudge = requestAnimationFrame(function () {
            n.wbHoldNudge = requestAnimationFrame(function () {
              n.wbHoldNudge = 0;
              if (n.isConnected && n.hasAttribute('data-board-held')) n.classList.add('board-hold-nudge');
            });
          });
        }
      });
    });
  }
  function boardScheduleFlushViewed() {
    if (boardReleaseFrame || boardReleaseTimer) return;
    if (!Object.keys(boardPending).length) return;
    var hidden = (typeof pageHidden === "function" ? pageHidden() : !!(doc && doc.hidden)) || typeof requestAnimationFrame !== "function";
    if (hidden) {
      boardReleaseTimer = setTimeout(function () {
        boardReleaseTimer = 0;
        boardFlushViewed(false);
      }, 0);
      return;
    }
    boardReleaseFrame = requestAnimationFrame(function () {
      boardReleaseFrame = 0;
      boardFlushViewed(false);
    });
  }
  function boardFlushViewed(explicit) {
    if (boardReleaseFrame && typeof cancelAnimationFrame === "function") {
      cancelAnimationFrame(boardReleaseFrame);
    }
    boardReleaseFrame = 0;
    if (typeof boardReleaseTimer !== "undefined" && boardReleaseTimer) {
      clearTimeout(boardReleaseTimer);
      boardReleaseTimer = 0;
    }
    if (!Object.keys(boardPending).length) return;
    var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("board", "boardFlushViewed") : null;
    boardRelease = !!explicit;
    try {
      if (boardLatest) swap("board", boardLatest.hash, boardLatest.html, true);
      boardPendingBadge();
    } finally {
      boardRelease = false;
      if (jTok) WsJank.end(jTok);
    }
  }
  // A card held for an active editor releases when the editor leaves it;
  // visible cards' staged updates wait until they leave the viewport.
  doc.addEventListener("focusout", function () {
    if (!Object.keys(boardPending).length) return;
    setTimeout(function () {
      if (editing()) return;
      var guarded = boardGuards();
      if (!Object.keys(boardPending).some(function (k) { return guarded.indexOf(k) < 0; })) return;
      boardFlushViewed(false);
    }, 0);
  });
  // Exposure no longer gates release, so nothing watches the board's units.
  function watchBoard() {}
