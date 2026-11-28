(function () {
  // The board card framework (window.WsCards): cards the page itself puts in
  // the board column, among the board's topics. The framework owns what
  // every card kind shares: the header's pin, fold, raise, maximize, and close
  // buttons, the body's base height and its drag handle, the maximize sheet
  // with Escape (which also draws a topic or a topic's panel large), where
  // a card stands in the board flow, and the record of
  // all of that in browser storage. A kind supplies the header's content,
  // its own header actions, the body, the folded content, and its
  // lifecycle, and never places or sizes a card itself.
  //
  // A kind registers before the page mounts the framework:
  //
  //   WsCards.register(name, {
  //     base: 260,            // the body's base height in px, or a function
  //                           // of the card that returns it (optional)
  //     open(card) {},        // build card.body, call card.title(...)
  //     closing(card, opener) {}, // the close button was pressed: false
  //                           // (or a promise for false) keeps the card,
  //                           // and the kind may close it later
  //                           // (optional)
  //     close(card) {},       // the card is going away (optional)
  //     folded(card) {},      // a node shown under a folded header, or null (optional)
  //     sized(card) {},       // the body's size changed (optional)
  //     pinned(card) {},      // the card was pinned or unpinned (optional)
  //   })
  //
  // and works through the card it is handed: card.data (the kind's own
  // record, kept in storage: plain JSON), card.body, card.title, card.actions,
  // card.save, card.bump, card.show, card.fold, card.close, card.pinned, and
  // card.paint (draws the view state again: a kind whose base height
  // changed calls it).
  //
  // The board's topics take part in the same placement as the cards. A
  // topic is the board's own node (the whiteboard's section, never rebuilt
  // here); the framework adds a pin button to its title bar, and a topic is
  // pinned, unpinned, and dragged like a card. Only a topic pinned or moved
  // by hand has a record.
  //
  // Placement is one rule for cards and topics. Pinned (docked) ones stand
  // in the paged docking gallery (#bgal, built here): a sequence of pages,
  // each a split tree of panes (the model, page order and which page shows
  // are WsGallery's, gallery.js), with a trailing empty page (+) and a dot
  // rail. In the wide view the page shows it as a column beside the board;
  // in the narrow view the Main Board is Page 0 of the same rail and the
  // gallery overlays the board's column (opt.mode, opt.layout). A unit's
  // pinned is its flat pin order (WsGallery.ordinals), 0 when not docked.
  // An unpinned card enters at the head of the unpinned flow when it opens
  // (bump counts as an open); after that it only moves down, as a topic
  // updated after the card opened rises above it. A card's open time is
  // kept with its view state, so a reload puts it back where it stood among
  // the topics. Topics otherwise keep the board's own order. Dragging a
  // header moves a card or a topic: onto a rail dot it docks (+: a new
  // page; a page's dot: below what that page holds; the narrow view's home
  // dot: undocks), onto a pane of the page shown it docks at that pane's
  // zone (an edge splits the pane; the center, offered to a docked pane
  // only, swaps the two), into the board's open flow it undocks and stands
  // where it was dropped (a time between its new neighbours'). A topic moved by
  // hand keeps that time until the topic itself next updates; a closed
  // topic stands only in the closed section or the gallery. A docked topic
  // keeps its pane when it updates or closes, and the page keeps its last
  // node when the board stops serving it (past the closed-topic cap); after
  // a reload such a topic is fetched through the closed route (opt.closed),
  // and one the store no longer holds leaves the gallery.
  //
  // A card's node is never moved by a board update: the board morph weaves
  // the cards into the nodes it wants (weave) and settles the board around
  // them (settle), so a frame or a video in a card keeps running. Only the
  // card the user docks, undocks, or reopens moves, and where the browser
  // has Element.moveBefore even that move keeps the card's state. Switching
  // pages moves nothing: inactive pages stay mounted, translated out of the
  // gallery's clipped viewport.
  var doc = document, G = window.WsGallery;
  var BASE = 260, MIN = 72, STEP = 24, PINW_MIN = 280, BOARDW_MIN = 400, CONVW_MIN = 320, SPLITB_MIN = 360;
  var BGAL_W_KEY = "ws.dashboard.bgal.w", BGAL_SYS_KEY = "ws.dashboard.bgal.sys";
  var BGAL_W_DEF = 540, BGAL_W_MIN = 350, BGAL_W_MAX = 720;
  // A press drags past SLOP px; a touch drags after holding HOLD ms (the
  // session rail's drag thresholds), and EDGE px from a list's edge scrolls it.
  var SLOP = 5, HOLD = 380, EDGE = 28;
  var kinds = {}, cards = [], topics = {}, board = null, opt = null, maxed = null, scrim = null, seq = 0;
  // gal is the gallery's model (WsGallery), lay the layout the page shows
  // it in (WsGallery.layout; null before mount). galEl is the gallery's
  // element: a width handle, the dot rail (railEl, its dots in dotsEl) and
  // the clipped viewport (viewEl) holding one slide per user page (pageEls by
  // page id), the + page (plusEl), and fixed system pages (sysPageEls by id);
  // panes (paneEls by reference) and splits (splitEls by page id and path,
  // their dividers divEls) are kept so a unit's node keeps its pane. bgalW is
  // the gallery's global stored width (350-720px, --bgal-w), convow the
  // conversation's beside the board (0: the stylesheet's).
  var gal = null, lay = null, galEl = null, railEl = null, dotsEl = null, viewEl = null, plusEl = null;
  var sepEl = null, sysGroupEl = null, sysPageEls = {}, sysActive = null, storedSys = null, galListeners = [];
  var pageEls = {}, paneEls = {}, splitEls = {}, divEls = {}, dotEls = {}, railWheel = new G.RailWheel(), rg = null, railMoved = false, resizeBound = false;
  var bgalW = BGAL_W_DEF, bgalRaf = 0, convow = 0;
  var toolbar = null, overviewOn = false;
  var stateFilter = "all", stateUI = null;
  var toolbarFit = { width: 0, actsWidth: 0, contentSig: "", probing: false, modes: ["", "compact", "dropdown", "tight"] };
  var filterValues = ["all", "asks", "researching", "discussing", "waiting", "implementing", "awaiting-confirmation", "closed", "cards", "tickets"];
  var metaFilterValues = ["all", "asks", "closed", "cards", "tickets"];
  var pipelineFilterValues = ["researching", "discussing", "waiting", "implementing", "awaiting-confirmation"];

  function licon(name) { return '<svg class="li" aria-hidden="true"><use href="#l-' + name + '"/></svg>'; }
  function mk(tag, cls, text) {
    var e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function ibtn(cls, icon, title) {
    var b = mk("button", "ibtn" + (cls ? " " + cls : ""));
    b.type = "button";
    b.innerHTML = licon(icon);
    b.title = title;
    b.setAttribute("aria-label", title);
    return b;
  }
  function isCard(n) { return !!n && n.nodeType === 1 && n.hasAttribute("data-card"); }
  // The toolbar is infrastructure, not a card, flow peer, or storage record.
  function fixedNode(n) { return isCard(n) || (!!toolbar && n === toolbar); }
  function buildToolbar() {
    toolbar = mk("div", "board-toolbar");
    toolbar.setAttribute("role", "toolbar");
    toolbar.setAttribute("aria-label", wsT("js.board.toolbar.label"));
    var row = mk("div", "board-tools"), actions = opt.toolbar || {};
    function action(parent, cls, icon, label, run) {
      var b = mk("button", "board-action " + cls);
      b.type = "button";
      b.innerHTML = licon(icon);
      b.appendChild(mk("span", "board-label", label));
      b.title = label;
      b.setAttribute("aria-label", label);
      b.addEventListener("click", function (e) { if (run) run(e, b); });
      parent.appendChild(b);
      return b;
    }
    var overview = action(row, "board-overview", "chevrons-down-up", wsT("js.board.toolbar.overview"), actions.overview);
    overview.setAttribute("aria-pressed", "false");
    overview.title = wsT("js.board.toolbar.overview.title");
    var stack = mk("div", "board-stack");
    var acts = mk("div", "board-acts");
    var term = mk("span", "board-term-slot");
    term.id = "board-term-slot";
    acts.appendChild(term);
    var web = mk("span", "board-web-slot");
    web.id = "board-web-slot";
    acts.appendChild(web);
    if (actions.download) {
      var download = action(acts, "board-download", "download", wsT("js.board.toolbar.download"), actions.download);
      download.title = wsT("js.board.toolbar.download.title");
      download.setAttribute("aria-haspopup", "menu");
      download.setAttribute("aria-expanded", "false");
    }
    if (actions.files) {
      var files = action(acts, "board-files", "folder-open", wsT("js.board.toolbar.files"), function () {
        var owner = actions.files;
        if (!owner.el) return;
        maximize(maxed && maxed.files ? null : { files: true, el: owner.el, owner: owner, back: files });
      });
      if (actions.files.el) files.setAttribute("aria-controls", actions.files.el.id);
      files.setAttribute("aria-expanded", "false");
      files.title = wsT("js.board.toolbar.files.title");
    }
    if (actions.stageFill) {
      var fill = mk("button", "board-action board-stage-fill");
      fill.type = "button";
      fill.innerHTML = '<svg class="li" viewBox="0 0 24 24" aria-hidden="true"><path d="M2 5v14M22 5v14M6 12h12M9 9l-3 3 3 3M15 9l3 3-3 3"/></svg>';
      fill.appendChild(mk("span", "board-label", wsT("js.board.toolbar.stage_fill")));
      fill.title = wsT("js.board.toolbar.stage_fill.title");
      fill.setAttribute("aria-label", wsT("js.board.toolbar.stage_fill"));
      fill.setAttribute("aria-pressed", String(doc.documentElement.hasAttribute("data-stage-fill")));
      fill.addEventListener("click", function (e) { actions.stageFill(e); });
      acts.appendChild(fill);
    }
    var actsDiv = mk("span", "board-acts-vdiv");
    actsDiv.setAttribute("aria-hidden", "true");
    acts.appendChild(actsDiv);
    stack.appendChild(acts);
    stack.appendChild(buildStateFilter());
    row.appendChild(stack);
    var face = mk("div", "board-toolbar-face");
    face.appendChild(row);
    toolbar.appendChild(face);
    toolbarFit.width = 0;
    toolbarFit.actsWidth = 0;
    toolbarFit.contentSig = "";
    toolbarFit.probing = false;
    window.addEventListener("resize", function () { fitToolbar(false); });
    if (window.ResizeObserver) new window.ResizeObserver(function () { fitToolbar(false); }).observe(toolbar);
    if (window.MutationObserver) {
      new window.MutationObserver(function (records) {
        for (var i = 0; i < records.length; i++) {
          if (!stateUI || !stateUI.root.contains(records[i].target)) { fitToolbar(true); return; }
        }
      }).observe(row, { childList: true, subtree: true, attributes: true, attributeFilter: ["hidden"] });
    }
    if (doc.fonts && doc.fonts.ready) doc.fonts.ready.then(function () { fitToolbar(true); });
  }
  // The sticky root keeps the original flex footprint. Only its face changes
  // height: no scroll writer, replacement controls, or transient measuring DOM.
  var floatQueue = null;
  function floatToolbar() {
    if (!window.ResizeObserver) return;
    var face = toolbar.firstChild, row = face.firstChild, scrollBox = null, cachedScroller = null, frame = 0;
    function style(n, key, value) {
      if (n.style.getPropertyValue(key) !== value) n.style.setProperty(key, value);
    }
    function update() {
      frame = 0;
      var faceRect = face.getBoundingClientRect();
      if (!faceRect.width) return;
      var s = cachedScroller || (cachedScroller = scroller(board) || doc.scrollingElement), main = s === doc.scrollingElement;
      // Document boards share the viewport with the fixed drawer handles.
      // Keep the face below them, rather than raising the
      // toolbar over unrelated controls or changing horizontal action boxes.
      var inset = 0;
      if (main) Array.prototype.forEach.call(doc.querySelectorAll(".tk-handle, .gh, .dr-handle"), function (n) {
        if (!n.getClientRects().length) return;
        var r = n.getBoundingClientRect();
        if (r.top <= 0 && r.bottom > 0) inset = Math.max(inset, r.bottom + 4);
      });
      var top = main ? inset : s.getBoundingClientRect().top + s.clientTop;
      var snapDeck = !main && !!(s && s.wbScrollOwner) && !overviewOn;
      var floating = snapDeck ? false : board.getBoundingClientRect().top < top;
      var rows = {};
      Array.prototype.forEach.call(row.children, function (n) {
        var r = n.getBoundingClientRect();
        if (!r.height) return;
        var key = Math.round((r.top + r.bottom) * 2) / 4;
        rows[key] = 44;
      });
      var wrapped = Object.keys(rows).length > 1;
      var keys = Object.keys(rows), reserved = 6 + Math.max(0, keys.length - 1) * 6;
      keys.forEach(function (key) { reserved += rows[key]; });
      var height = faceRect.height;
      var fit = toolbar.getAttribute ? toolbar.getAttribute("data-toolbar-fit") : null;
      var narrow = fit === "dropdown" || fit === "tight";
      // All geometry above, all writes below; ResizeObserver settles a face
      // whose dimensions change when floating or wrapping changes.
      style(toolbar, "--toolbar-top", inset + "px");
      if (toolbar.hasAttribute("data-floating") !== floating) toolbar.toggleAttribute("data-floating", floating);
      if (toolbar.hasAttribute("data-wrapped-actions") !== wrapped) toolbar.toggleAttribute("data-wrapped-actions", wrapped);
      style(toolbar, "--toolbar-reserved", (floating && !narrow ? reserved : height) + "px");
      // The rail already exposes a host inset; include the lower hit extension.
      style(doc.getElementById("bcol") || board, "--wb-rail-inset", (inset + height + 6) + "px");
      if (scrollBox && scrollBox !== s) scrollBox.style.removeProperty("scroll-padding-top");
      scrollBox = s;
      style(s, "scroll-padding-top", (inset + height + 6) + "px");
      if (maxed && maxed.files) style(maxed.el, "--files-top", Math.max(52, faceRect.bottom + 8) + "px");
    }
    function queue() { if (!frame) frame = requestAnimationFrame(update); }
    function resized() { cachedScroller = null; queue(); }
    function onScroll(e) {
      var t = e && e.target;
      if (t && !overviewOn) {
        var s = cachedScroller || (cachedScroller = scroller(board) || doc.scrollingElement);
        if (t === s && s !== doc.scrollingElement && s.wbScrollOwner) return;
        if (t.id === "bcol" && t.wbScrollOwner) return;
      }
      queue();
    }
    floatQueue = queue;
    doc.addEventListener("scroll", onScroll, { capture: true, passive: true });
    window.addEventListener("resize", resized);
    var observer = new window.ResizeObserver(resized);
    observer.observe(face); observer.observe(board);
    queue();
  }
  function filterMark(value) {
    var n = mk("span", value === "asks" ? "board-state-glyph" : value === "cards" || value === "tickets" ? "board-state-icon" : "board-state-dot");
    if (value === "asks") n.textContent = "?";
    if (value === "cards") n.innerHTML = '<svg class="li" aria-hidden="true"><use href="#i-app-window"/></svg>';
    if (value === "tickets") n.innerHTML = '<svg class="li" aria-hidden="true"><use href="#i-ticket"/></svg>';
    return n;
  }
  function buildStateFilter() {
    var root = mk("div", "board-state-filter"), inline = mk("div", "board-state-inline");
    var meta = mk("div", "board-state-meta"), pipeline = mk("div", "board-state-pipeline");
    var trigger = mk("button", "board-action board-state-trigger"), pop = mk("div", "tpop board-state-pop");
    var labels = [wsT("js.board.filter.all"), wsT("js.board.filter.asks"), wsT("js.board.filter.researching"), wsT("js.board.filter.discuss"), wsT("js.board.filter.wait"), wsT("js.board.filter.implement"), wsT("js.board.filter.confirm"), wsT("js.board.filter.closed"), wsT("js.board.filter.cards"), wsT("js.board.filter.tickets")];
    var asksShort = wsT("js.board.filter.asks.short");
    var label = wsT("js.board.filter.label");
    stateFilter = opt.load(opt.key + ".state-filter");
    if (filterValues.indexOf(stateFilter) < 0) stateFilter = "all";
    stateUI = { root: root, inline: inline, meta: meta, pipeline: pipeline, trigger: trigger, pop: pop, labels: labels, radios: [], totals: {} };
    trigger.type = "button";
    trigger.hidden = true;
    trigger.setAttribute("aria-label", label);
    trigger.setAttribute("aria-haspopup", "dialog");
    trigger.setAttribute("aria-expanded", "false");
    pop.hidden = true;
    pop.setAttribute("role", "dialog");
    pop.setAttribute("aria-label", label);
    pop.appendChild(mk("div", "lb", label));
    var options = mk("div", "board-state-options");
    inline.setAttribute("role", "radiogroup");
    inline.setAttribute("aria-label", label);
    options.setAttribute("role", "radiogroup");
    options.setAttribute("aria-label", label);
    function makeRadio(group, container, value, isPop, extraCls) {
      var idx = filterValues.indexOf(value);
      var b = mk("button", (isPop ? "board-state-option" : "board-state-radio") + (extraCls ? " " + extraCls : ""));
      b.type = "button";
      b.setAttribute("role", "radio");
      b.setAttribute("data-filter", value);
      if (isPop) b.appendChild(mk("span", "board-state-ring"));
      b.appendChild(filterMark(value));
      var text = !isPop && value === "asks" ? asksShort : labels[idx];
      b.appendChild(mk("span", "board-state-label", text));
      b.appendChild(mk("span", "board-state-count"));
      b.addEventListener("click", function () { selectFilter(value, b, isPop); });
      b.addEventListener("keydown", function (e) {
        var radios = group.querySelectorAll('[role="radio"]');
        var index = Array.prototype.indexOf.call(radios, b), next;
        if (e.key === "ArrowRight" || e.key === "ArrowDown") next = (index + 1) % radios.length;
        else if (e.key === "ArrowLeft" || e.key === "ArrowUp") next = (index + radios.length - 1) % radios.length;
        else if (e.key === "Home") next = 0;
        else if (e.key === "End") next = radios.length - 1;
        if (next === undefined) return;
        e.preventDefault(); e.stopPropagation();
        selectFilter(radios[next].getAttribute("data-filter"), radios[next], false);
      });
      container.appendChild(b);
      stateUI.radios.push(b);
    }
    metaFilterValues.forEach(function (value) {
      makeRadio(inline, meta, value, false, "board-state-chip");
      if (value === "all") {
        var vdiv = mk("span", "board-state-vdiv");
        vdiv.setAttribute("aria-hidden", "true");
        meta.appendChild(vdiv);
      }
    });
    pipelineFilterValues.forEach(function (value) {
      makeRadio(inline, pipeline, value, false, "board-state-seg");
    });
    inline.appendChild(meta);
    inline.appendChild(pipeline);
    filterValues.forEach(function (value) {
      makeRadio(options, options, value, true, "");
    });
    pop.appendChild(options);
    root.appendChild(inline); root.appendChild(trigger); root.appendChild(pop);
    function close(back) {
      pop.hidden = true;
      trigger.setAttribute("aria-expanded", "false");
      if (back) trigger.focus({ preventScroll: true });
    }
    stateUI.close = close;
    trigger.addEventListener("click", function () {
      if (!pop.hidden) { close(false); return; }
      pop.hidden = false; trigger.setAttribute("aria-expanded", "true");
      placeStatePop();
      options.querySelector('[data-filter="' + stateFilter + '"]').focus({ preventScroll: true });
    });
    pop.addEventListener("keydown", function (e) {
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); close(true); }
    });
    root.addEventListener("focusout", function (e) { if (!toolbarFit.probing && e.relatedTarget && !root.contains(e.relatedTarget)) close(false); });
    doc.addEventListener("click", function (e) { if (!root.contains(e.target)) close(false); });
    doc.addEventListener("scroll", placeStatePop, { capture: true, passive: true });
    return root;
  }
  function selectFilter(value, focus, dismiss, after) {
    var run = function () {
      stateFilter = value;
      opt.save(opt.key + ".state-filter", value);
      countTopics();
      if (window.WsBoardAnchor && window.WsBoardAnchor.reveal) window.WsBoardAnchor.reveal(true);
      if (dismiss) stateUI.close(true);
      else if (focus) (stateUI.inline.hidden && stateUI.inline.contains(focus) ? stateUI.trigger : focus).focus({ preventScroll: true });
      if (after) after();
    };
    // Let Overview finish restoring its anchor before visibility changes.
    if (opt.toolbar && opt.toolbar.filter) opt.toolbar.filter(run); else run();
  }
  function placeStatePop() {
    if (!stateUI || stateUI.pop.hidden) return;
    var r = stateUI.trigger.getBoundingClientRect(), pop = stateUI.pop;
    var width = Math.min(232, window.innerWidth - 16), top = Math.min(r.bottom + 8, Math.max(8, window.innerHeight - 80));
    pop.style.width = width + "px";
    pop.style.left = Math.max(8, Math.min(r.right - width, window.innerWidth - width - 8)) + "px";
    pop.style.top = top + "px";
    pop.style.maxHeight = Math.max(0, window.innerHeight - top - 8) + "px";
  }
  function toolbarContentSig() {
    if (!stateUI || !stateUI.totals) return "";
    var counts = filterValues.map(function (v) { return stateUI.totals[v] || 0; }).join(",");
    return stateUI.inline.hidden ? counts + "|" + stateFilter : counts;
  }
  function applyToolbarFit(mode) {
    var cur = toolbar.getAttribute("data-toolbar-fit");
    if (mode) { if (cur !== mode) toolbar.setAttribute("data-toolbar-fit", mode); }
    else if (cur !== null) toolbar.removeAttribute("data-toolbar-fit");
    if (stateUI && stateUI.root.isConnected) {
      var narrow = mode === "dropdown" || mode === "tight";
      if (stateUI.inline.hidden !== narrow) stateUI.inline.hidden = narrow;
      if (stateUI.trigger.hidden !== !narrow) stateUI.trigger.hidden = !narrow;
    }
  }
  function toolbarRowFits(face, row) {
    var faceRect = face.getBoundingClientRect();
    if (!faceRect.width) return true;
    var acts = row.querySelector(".board-acts");
    var actsRect = acts ? acts.getBoundingClientRect() : null;
    if (stateUI && stateUI.root.isConnected && !stateUI.inline.hidden) {
      var metaRect = stateUI.meta.getBoundingClientRect();
      var pipeRect = stateUI.pipeline.getBoundingClientRect();
      if (actsRect && actsRect.width > 0 && metaRect.left < actsRect.right - 0.5) return false;
      if (metaRect.right > faceRect.right - 0.5) return false;
      if (pipeRect.right > faceRect.right - 0.5) return false;
      var chips = stateUI.meta.querySelectorAll(".board-state-radio");
      for (var i = 0; i < chips.length; i++) {
        if (chips[i].scrollWidth > chips[i].clientWidth + 1) return false;
      }
      var segs = stateUI.pipeline.querySelectorAll(".board-state-radio");
      for (var j = 0; j < segs.length; j++) {
        if (segs[j].scrollWidth > segs[j].clientWidth + 1) return false;
      }
      return true;
    }
    if (stateUI && stateUI.root.isConnected && !stateUI.trigger.hidden) {
      var trigRect = stateUI.trigger.getBoundingClientRect();
      if (actsRect && actsRect.width > 0 && trigRect.left < actsRect.right - 0.5) return false;
      if (trigRect.right > faceRect.right - 0.5) return false;
      return true;
    }
    return true;
  }
  function fitToolbar(force) {
    if (!toolbar || !toolbar.firstChild || !toolbar.firstChild.firstChild || toolbarFit.probing) return;
    var face = toolbar.firstChild, row = face.firstChild;
    var width = toolbar.getBoundingClientRect().width;
    if (!width) return;
    var acts = row.querySelector(".board-acts");
    var actsWidth = acts ? acts.getBoundingClientRect().width : 0;
    var sig = toolbarContentSig();
    if (!force && toolbarFit.contentSig === sig && Math.abs(width - toolbarFit.width) < 0.25 && Math.abs(actsWidth - toolbarFit.actsWidth) < 0.25) {
      placeStatePop();
      return;
    }
    toolbarFit.width = width;
    toolbarFit.actsWidth = actsWidth;
    toolbarFit.contentSig = sig;
    var ui = stateUI && stateUI.root.isConnected ? stateUI : null;
    var active = doc.activeElement;
    var wasNarrow = !!(ui && ui.inline.hidden);
    var inFilter = !!(ui && active && (ui.inline.contains(active) || active === ui.trigger || ui.pop.contains(active)));
    var modes = ui ? toolbarFit.modes : ["", "compact", "tight"];
    var chosen = modes[modes.length - 1];
    toolbarFit.probing = true;
    try {
      for (var i = 0; i < modes.length; i++) {
        applyToolbarFit(modes[i]);
        if (i === modes.length - 1 || toolbarRowFits(face, row)) {
          chosen = modes[i];
          break;
        }
      }
    } finally {
      toolbarFit.probing = false;
    }
    if (ui) {
      var narrow = chosen === "dropdown" || chosen === "tight";
      if (narrow !== wasNarrow) {
        ui.close(false);
        if (inFilter) (narrow ? ui.trigger : ui.inline.querySelector('[data-filter="' + stateFilter + '"]')).focus({ preventScroll: true });
      } else if (inFilter && active && active.isConnected && doc.activeElement !== active) {
        active.focus({ preventScroll: true });
      }
      placeStatePop();
    }
  }
  function filtered(n, hide) {
    var has = n.hasAttribute("data-state-filtered");
    if (hide) { if (!has) n.setAttribute("data-state-filtered", ""); }
    else if (has) n.removeAttribute("data-state-filtered");
  }
  function countTopics() {
    if (!stateUI || !galEl) return;
    var totals = {}, seen = new Set();
    filterValues.forEach(function (v) { totals[v] = 0; });
    [board, galEl].forEach(function (box) {
      Array.prototype.forEach.call(box.querySelectorAll("section.topic[data-topic]"), function (n) {
        var closed = n.classList.contains("closed"), state = n.getAttribute("data-state"), name = n.getAttribute("data-topic"), asks = n.getAttribute("data-open-asks") === "true", tickets = n.getAttribute("data-open-tickets") === "true";
        filtered(n, stateFilter === "asks" ? !asks : stateFilter === "tickets" ? !tickets : stateFilter === "cards" || stateFilter !== "all" && (closed ? stateFilter !== "closed" : stateFilter === "closed" || stateFilter !== state));
        if (seen.has(name)) return;
        seen.add(name);
        totals.all++;
        if (asks) totals.asks++;
        if (tickets) totals.tickets++;
        if (closed) totals.closed++;
        else if (filterValues.indexOf(state) > 0 && state !== "closed" && state !== "cards" && state !== "asks" && state !== "tickets") totals[state]++;
      });
    });
    // Registry entries are framework roots, never panels or logs within a topic.
    // Count identities rather than paths; pinning changes placement, not membership.
    cards.forEach(function (c) {
      totals.cards++;
      filtered(c.el, stateFilter !== "all" && stateFilter !== "cards");
    });
    var more = board.querySelector("button.closed-more[data-closed-more]");
    if (more) {
      var remaining = Math.max(0, +more.getAttribute("data-closed-more") || 0);
      totals.closed += remaining; totals.all += remaining;
    }
    Array.prototype.forEach.call(board.querySelectorAll(".closed-more"), function (n) { filtered(n, stateFilter !== "all" && stateFilter !== "closed"); });
    stateUI.totals = totals;
    var toolbarChanged = false;
    stateUI.radios.forEach(function (b) {
      var value = b.getAttribute("data-filter"), checked = value === stateFilter;
      var count = totals[value] || 0, countStr = String(count);
      var label = stateUI.labels[filterValues.indexOf(value)] + " " + count;
      var checkedStr = String(checked), tabStr = checked ? "0" : "-1";
      if (b.getAttribute("aria-checked") !== checkedStr) b.setAttribute("aria-checked", checkedStr);
      if (b.getAttribute("tabindex") !== tabStr) b.setAttribute("tabindex", tabStr);
      if (b.getAttribute("aria-label") !== label) b.setAttribute("aria-label", label);
      if (b.title !== label) b.title = label;
      if (b.getAttribute("data-count") !== countStr) { b.setAttribute("data-count", countStr); toolbarChanged = true; }
      var hadZero = b.hasAttribute("data-zero");
      if (count === 0) { if (!hadZero) { b.setAttribute("data-zero", ""); toolbarChanged = true; } }
      else if (hadZero) { b.removeAttribute("data-zero"); toolbarChanged = true; }
      var countEl = b.querySelector(".board-state-count");
      if (countEl && countEl.textContent !== countStr) { countEl.textContent = countStr; toolbarChanged = true; }
    });
    var i = filterValues.indexOf(stateFilter), trigger = stateUI.trigger;
    var trigCount = totals[stateFilter] || 0;
    var trigKey = stateFilter + "|" + trigCount;
    if (stateUI.trigKey !== trigKey) {
      stateUI.trigKey = trigKey;
      trigger.setAttribute("data-filter", stateFilter);
      trigger.innerHTML = licon("list-filter");
      trigger.appendChild(filterMark(stateFilter));
      trigger.appendChild(mk("span", "board-state-label", stateUI.labels[i]));
      trigger.appendChild(mk("span", "board-state-count", String(trigCount)));
      trigger.appendChild(mk("span", "term-arrow", "▾"));
      trigger.setAttribute("aria-label", wsT("js.board.filter.label") + ": " + stateUI.labels[i] + " " + trigCount);
      if (stateUI.inline.hidden) toolbarChanged = true;
    }
    var sig = toolbarContentSig();
    if (!toolbarChanged && toolbarFit.width > 0 && toolbarFit.contentSig === sig) placeStatePop();
    else fitToolbar(false);
    if (window.WsBoardAnchor && window.WsBoardAnchor.settle) window.WsBoardAnchor.settle();
  }
  function topicName(n) {
    return n && n.nodeType === 1 && n.tagName === "SECTION" && n.classList.contains("topic") && n.hasAttribute("data-topic") ? n.getAttribute("data-topic") : null;
  }

  // topicAt is an open topic's last update (epoch ms), the time in its bar
  // (a recap line carries a time of its own), or null for any other node: a
  // closed topic, the closed-topic control, the reply bar.
  function topicAt(n) {
    if (n.nodeType !== 1 || !n.hasAttribute("data-topic") || n.classList.contains("closed")) return null;
    var t = n.querySelector(".tbar time.tago[data-at]");
    return t ? +t.getAttribute("data-at") || 0 : 0;
  }
  // order weaves the cards in list into base, the board's own nodes in
  // order, by the placement rule; at reads a node's topicAt.
  function order(base, list, at) {
    var pins = list.filter(function (c) { return c.pinned; }).sort(function (a, b) { return a.pinned - b.pinned; });
    var flow = list.filter(function (c) { return !c.pinned; }).sort(function (a, b) { return b.at - a.at || rank(a) - rank(b); });
    var out = pins.map(function (c) { return c.el; }), i = 0, counts = {};
    base.forEach(function (n) {
      var t = at(n), time = t === null ? -1 : t, r = counts[time] || 0;
      counts[time] = r + 1;
      // rank is an independent snapshot within a tied timestamp (or the
      // closed tail), not an attachment to a topic. Ordinary opens retain
      // the original rule: before all equal/older topics and controls.
      while (i < flow.length && (flow[i].at > time || (flow[i].at === time && rank(flow[i]) <= r))) out.push(flow[i++].el);
      out.push(n);
    });
    while (i < flow.length) out.push(flow[i++].el);
    return out;
  }
  function rank(u) { return typeof u.rank === "number" ? u.rank : -1; }
  // settle makes el's children the nodes in want, in order. Stale nodes go
  // first, so the kept ones that stay in order are never moved (moving a
  // frame reloads it). A fixed node already in el is never moved either:
  // the nodes standing before it that are wanted after it move past it.
  // A node already in el moves with Element.moveBefore where the browser
  // has it, which keeps its frames loaded; so does a node moving in from
  // another container on the page (the pin list and the board). A new
  // node, or a browser without it, takes insertBefore. Either way the same
  // nodes move. A child in skip is not stale: another container wants it
  // and takes it from here.
  function settle(el, want, fixed, skip) {
    var keep = new Set(want);
    function put(n, ref) {
      if (el.moveBefore && (n.parentNode === el || (n.isConnected && el.isConnected))) {
        try { el.moveBefore(n, ref); return; } catch (e) { /* insertBefore below */ }
      }
      el.insertBefore(n, ref);
    }
    Array.prototype.slice.call(el.childNodes).forEach(function (n) { if (!keep.has(n) && !(skip && skip.has(n))) el.removeChild(n); });
    var cur = el.firstChild;
    want.forEach(function (n) {
      if (n === cur) { cur = cur.nextSibling; return; }
      if (fixed(n) && n.parentNode === el) {
        var after = n.nextSibling;
        while (cur !== n) { var next = cur.nextSibling; put(cur, after); cur = next; }
        cur = n.nextSibling;
        return;
      }
      put(n, cur);
    });
  }
  // headTime is an open time that puts a card at the head of the unpinned
  // flow: now, or just past the newest topic and card where the server's
  // clock (a topic's time) runs ahead of this browser's.
  function headTime() {
    var t = Date.now();
    if (board) Array.prototype.forEach.call(board.children, function (n) { var a = isCard(n) ? null : topicAt(n); if (a !== null && a >= t) t = a + 1; });
    units().forEach(function (c) { if (!c.pinned && c.at >= t) t = c.at + 1; });
    return t;
  }

  // --- topics among the units ---------------------------------------------------
  // A topic's record: name, pinned (its pin order, 0 when unpinned), at (a
  // hand-moved topic's place in the flow, 0 for none), bar (its bar time
  // when it was moved, so its own update ends the move), el (its node: the
  // last one the board served, kept while pinned).
  function topicRec(name) { return topics[name] || (topics[name] = { name: name, pinned: 0, at: 0, bar: 0, el: null }); }
  function tidy(r) { if (!r.pinned && !r.at && !r.folded && topics[r.name] === r) delete topics[r.name]; }
  // units are the cards and the topics that place themselves: pinned, or
  // moved by hand into the flow.
  function units() {
    var list = cards.slice();
    Object.keys(topics).forEach(function (k) { var r = topics[k]; if (r.el && (r.pinned || r.at)) list.push(r); });
    return list;
  }
  function pinned() { return units().filter(function (u) { return u.pinned; }).sort(function (a, b) { return a.pinned - b.pinned; }); }
  // A unit's gallery reference ("c:<id>", "t:<name>"), and a node's.
  function refOf(u) { return isCard(u.el) || u instanceof Card ? "c:" + u.id : "t:" + u.name; }
  function nodeRef(n) { return isCard(n) ? "c:" + n.getAttribute("data-card") : "t:" + topicName(n); }
  // sync writes the gallery's flat pin orders to the units (0: not docked),
  // so every reader of pinned keeps working, and drops a topic record that
  // no longer holds anything.
  function sync() {
    var o = G.ordinals(gal);
    cards.forEach(function (c) { c.pinned = o["c:" + c.id] || 0; });
    Object.keys(topics).forEach(function (k) { var r = topics[k]; r.pinned = o["t:" + k] || 0; tidy(r); });
  }
  function claimed(n) {
    var name = topicName(n), r = name !== null ? topics[name] : null;
    return !!r && r.el === n && !!(r.pinned || r.at);
  }
  function unitOf(n) {
    if (isCard(n)) return cards.filter(function (c) { return c.el === n; })[0] || null;
    var name = topicName(n);
    return name === null ? null : topics[name] && topics[name].el === n ? topics[name] : { name: name, pinned: 0, at: 0, bar: 0, el: n, loose: true };
  }

  // claim reads the board's own nodes (a board update's): each pinned or
  // hand-moved topic takes its node from them, a hand move whose topic
  // updated or closed (or left the board) lapses, and the rest stay in the
  // board's order. A pinned topic the board no longer serves keeps the node
  // it had.
  function claim(base) {
    var seen = {};
    var rest = base.filter(function (n) {
      var name = topicName(n), r = name !== null ? topics[name] : null;
      if (!r) return true;
      seen[name] = true;
      r.el = n;
      if (r.pinned) return false;
      if (r.at && topicAt(n) === r.bar) return false;
      r.at = 0;
      tidy(r);
      return true;
    });
    Object.keys(topics).forEach(function (k) { var r = topics[k]; if (!seen[k] && !r.pinned) { r.at = 0; tidy(r); } });
    return rest;
  }
  // split is the placement of base, the board's own nodes less the claimed
  // topics: the docked nodes, in pin order, and the board column's.
  function split(base) {
    var flow = units().filter(function (u) { return !u.pinned; });
    var out = order(base, flow, topicAt);
    // A page may give transient cards a leading seat (the worktree's narrow
    // preview). This changes placement only, never the persisted open time.
    var leading = flow.filter(function (u) { return opt.leading && opt.leading(u); }).map(function (u) { return u.el; });
    out = out.filter(function (n) { return leading.indexOf(n) < 0; });
    return { pins: pinned().map(function (u) { return u.el; }), board: [toolbar].concat(leading, out) };
  }
  function weave(base) {
    var rest = claim(base);
    relayout();
    return split(rest);
  }
  // seat puts a topic that leaves the gallery without a place of its own
  // back among the board's nodes: an open one above the first open topic
  // older than it, a closed one at the head of the closed section (the next
  // board update puts it where the board keeps it).
  function seat(base, n) {
    var i = base.indexOf(n);
    if (i >= 0) base.splice(i, 1);
    var t = topicAt(n);
    for (i = 0; i < base.length; i++) {
      if (base[i].nodeType !== 1) continue;
      var a = topicAt(base[i]);
      if (a === null || (t !== null && a < t)) break;
    }
    base.splice(i, 0, n);
  }
  // put moves n before ref in el: with Element.moveBefore where the browser
  // has it and both stand on the page (a frame keeps its document), else
  // with insertBefore.
  function put(el, n, ref) {
    if (el.moveBefore && (n.parentNode === el || (n.isConnected && el.isConnected))) {
      try { el.moveBefore(n, ref); return; } catch (e) { /* insertBefore below */ }
    }
    el.insertBefore(n, ref);
  }
  // arrange puts the cards and topics in their places. moved is the one
  // unit whose place changed: it alone moves, with its state kept where the
  // browser can keep it.
  function releaseFreeNode(n) {
    if (!n || n.nodeType !== 1) return;
    if (window.WsBoardAnchor && window.WsBoardAnchor.releaseNode) {
      window.WsBoardAnchor.releaseNode(n);
    } else {
      if (n.style && n.style.top) n.style.top = "";
      if (n.removeAttribute) n.removeAttribute("data-wb-offwindow");
    }
  }
  // suspendFreeNode lifts inline free-canvas top and ResizeObserver tracking
  // while a node is drawn fixed ([data-max]), keeping its free-canvas Y and
  // laid height so restore lands at the pre-maximize scroll position.
  function suspendFreeNode(n) {
    if (!n || n.nodeType !== 1) return;
    if (window.WsBoardAnchor && window.WsBoardAnchor.releaseNode) {
      window.WsBoardAnchor.releaseNode(n, { keepCoords: true });
    } else {
      if (n.style && n.style.top) n.style.top = "";
      if (n.removeAttribute) n.removeAttribute("data-wb-offwindow");
    }
  }
  function captureFreeOffset(n) {
    var anchor = window.WsBoardAnchor;
    if (!n || !board || !board.contains(n) || !anchor || !anchor.free || !anchor.free()) return undefined;
    var col = doc.getElementById("bcol");
    var info = anchor.windowInfo && anchor.windowInfo();
    var y = info && info.posOf ? info.posOf(n) : undefined;
    return col && typeof y === "number" ? y - col.scrollTop : undefined;
  }
  function restoreFreeNode(el, freeOffset) {
    var anchor = window.WsBoardAnchor;
    if (!anchor || !anchor.settle) return false;
    if (!el || !el.isConnected || !board || !board.contains(el) || !anchor.free || !anchor.free()) {
      anchor.settle();
      return false;
    }
    var hasOffset = typeof freeOffset === "number" && isFinite(freeOffset);
    var info = anchor.windowInfo && anchor.windowInfo();
    var idx = info && info.indexOf ? info.indexOf(el) : -1;
    var inWindow = !!(info && idx >= info.start && idx <= info.end);
    if (!inWindow && anchor.select) {
      var jumpOpt = { keep: true, fullVirtual: true };
      if (hasOffset) jumpOpt.jumpOffset = freeOffset;
      if (anchor.select(el, jumpOpt)) return true;
    }
    anchor.settle();
    var col = doc.getElementById("bcol");
    info = anchor.windowInfo && anchor.windowInfo();
    var y = info && info.posOf ? info.posOf(el) : undefined;
    if (typeof y === "number" && col) {
      if (hasOffset) {
        var curH = info && info.heightOf ? info.heightOf(el) : 0;
        var effOffset = freeOffset;
        if (curH > 0 && effOffset < 0 && -effOffset >= curH - 1) {
          effOffset = -Math.max(0, curH - 120);
        }
        var maxScroll = Math.max(0, col.scrollHeight - col.clientHeight);
        var target = Math.max(0, Math.min(maxScroll, Math.round(y - effOffset)));
        if (Math.abs(col.scrollTop - target) > 0.5) col.scrollTop = target;
        return true;
      }
      if (!anchor.visibleCards || anchor.visibleCards().indexOf(el) >= 0) return true;
    }
    if (anchor.select) {
      var selOpt = { keep: true, fullVirtual: true };
      if (hasOffset) selOpt.jumpOffset = freeOffset;
      return !!anchor.select(el, selOpt);
    }
    return false;
  }
  function arrange(moved) {
    if (!board) return;
    relayout();
    if (moved && moved.el && !isCard(moved.el)) leaveSlot(moved.el);
    var base = Array.prototype.filter.call(board.childNodes, function (n) { return !fixedNode(n) && !claimed(n); });
    if (moved && moved.el && !isCard(moved.el) && !claimed(moved.el) && moved.el.parentNode !== board) seat(base, moved.el);
    var p = split(base), at = moved && moved.el && moved.el.parentNode === board ? p.board.indexOf(moved.el) : -1;
    if (at >= 0) {
      var ref = p.board[at + 1] || null;
      if (moved.el.nextSibling !== ref) put(board, moved.el, ref);
    }
    place(p, function (n) { return n === toolbar; });
    if (window.WsBoardAnchor && window.WsBoardAnchor.settle) window.WsBoardAnchor.settle();
  }
  // place puts the docked nodes into their panes, then settles the board
  // (fixed: what the board's settle never moves): a node docked from the
  // board moves into its pane before the board lets it go, and one undocked
  // moves out of its pane into the board. What the gallery no longer holds
  // goes last, so nothing the board takes is detached on its way.
  function place(p, fixed) {
    p.pins.forEach(releaseFreeNode);
    var built = buildGallery(p.pins);
    settle(board, p.board, fixed, new Set(p.pins));
    sweepGallery(built);
    p.pins.concat(p.board).forEach(function (n) { var name = topicName(n); if (name !== null && topics[name]) topics[name].el = n; });
    paintGallery();
    decorate();
  }

  // --- the gallery ----------------------------------------------------------------
  // buildGallery makes the gallery's pages, splits and panes stand for its
  // model, each docked node (nodes) in its pane, and returns what each of
  // those elements holds (sweepGallery drops the rest). A page, a split and
  // a pane keep their elements across changes, so a node stays put unless
  // its own place changed; the pages' DOM order never changes either (the
  // rail and the slides' transforms order them), so a page switch moves
  // nothing. A docked topic still being fetched has an empty pane. Each
  // element is filled after it stands in its parent (top down), so a pane
  // or a unit that moves goes from one place on the page to another, with
  // Element.moveBefore where the browser has it (its frames keep their
  // documents), never through a detached element: a split that takes a
  // pane, a collapse that lifts one, a move across pages.
  function buildGallery(nodes) {
    var by = {}, want = new Map(), used = { panes: {}, splits: {}, pages: {} };
    nodes.forEach(function (n) { by[nodeRef(n)] = n; });
    function hold(el, list) {
      want.set(el, list);
      var cur = el.firstChild;
      list.forEach(function (n) {
        if (n === cur) { cur = cur.nextSibling; return; }
        put(el, n, cur);
      });
    }
    // tree gives the element standing for node n (path from its page's
    // root) and fill, which puts its children in it once it stands.
    function tree(pid, n, path) {
      if (G.isSplit(n)) {
        var key = pid + "/" + path, s = splitEls[key] || (splitEls[key] = mk("div")), v = divEls[key] || (divEls[key] = divider());
        s.className = "gal-split " + n.axis;
        s.setAttribute("data-path", path);
        used.splits[key] = true;
        var a = tree(pid, n.a, path + "a"), b = tree(pid, n.b, path + "b");
        sizeSplit(s, a.el, v, b.el, n.axis, n.ratio);
        return { el: s, fill: function () { hold(s, [a.el, v, b.el]); a.fill(); b.fill(); } };
      }
      var ref = G.refOf(n), p = paneEls[ref];
      if (!p) {
        p = paneEls[ref] = mk("div", "gal-pane");
        p.setAttribute("data-ref", ref);
      }
      used.panes[ref] = true;
      return { el: p, fill: function () { hold(p, by[ref] ? [by[ref]] : []); } };
    }
    gal.pages.forEach(function (pg) {
      var el = pageEls[pg.id];
      if (!el) {
        el = pageEls[pg.id] = mk("div", "gal-page");
        el.setAttribute("data-page", pg.id);
        viewEl.insertBefore(el, plusEl);
      }
      used.pages[pg.id] = true;
      var root = tree(pg.id, pg.root, "");
      root.el.style.flex = "";
      hold(el, [root.el]);
      root.fill();
    });
    return { want: want, used: used };
  }
  // sizeSplit gives a split's two children their shares (ratio is a's)
  // and its divider the axis and value it reports.
  function sizeSplit(s, a, v, b, axis, ratio) {
    a.style.flex = ratio + " 1 0px";
    b.style.flex = (1 - ratio) + " 1 0px";
    v.setAttribute("aria-orientation", axis === "row" ? "vertical" : "horizontal");
    v.setAttribute("aria-valuenow", String(Math.round(ratio * 100)));
  }
  // divider makes a split's divider: dragged (pointer capture), it sets the
  // split's ratio (WsGallery.setRatio clamps it) as the pointer moves and
  // saves on release; a double click goes back to half and half; focused,
  // the arrow keys along its axis nudge it by RATIO_STEP.
  var RATIO_STEP = 0.05;
  function divider() {
    var v = mk("div", "gal-div"), from = null;
    v.setAttribute("role", "separator");
    v.setAttribute("aria-valuemin", String(Math.round(G.C.RATIO_MIN * 100)));
    v.setAttribute("aria-valuemax", String(Math.round(G.C.RATIO_MAX * 100)));
    v.setAttribute("aria-label", wsT("js.pins.width"));
    v.title = wsT("js.pins.width");
    v.tabIndex = 0;
    // at is the split v divides: its page id, path and model node.
    function at() {
      var s = v.parentNode, pg = s && s.closest && s.closest(".gal-page[data-page]");
      if (!pg) return null;
      var id = pg.getAttribute("data-page"), path = s.getAttribute("data-path") || "", i = G.pageIndex(gal, id);
      var n = i < 0 ? null : G.nodeAt(gal.pages[i].root, path);
      return G.isSplit(n) ? { id: id, path: path, node: n, s: s } : null;
    }
    function set(t, r) {
      gal = G.setRatio(gal, t.id, t.path, Math.round(r * 1000) / 1000);
      var n = G.nodeAt(gal.pages[G.pageIndex(gal, t.id)].root, t.path);
      sizeSplit(t.s, t.s.children[0], v, t.s.children[2], n.axis, n.ratio);
    }
    v.addEventListener("pointerdown", function (e) {
      if (e.button !== undefined && e.button !== 0) return;
      var t = at();
      if (!t) return;
      var r = t.s.getBoundingClientRect(), w = v.getBoundingClientRect();
      from = { t: t, row: t.node.axis === "row", r: r, thick: t.node.axis === "row" ? w.width : w.height };
      v.classList.add("drag");
      try { v.setPointerCapture(e.pointerId); } catch (x) { /* no capture: the moves still land */ }
      e.preventDefault();
    });
    v.addEventListener("pointermove", function (e) {
      if (!from) return;
      var f = from, span = (f.row ? f.r.width : f.r.height) - f.thick;
      if (span <= 0) return;
      set(f.t, ((f.row ? e.clientX - f.r.left : e.clientY - f.r.top) - f.thick / 2) / span);
    });
    function end() { if (!from) return; from = null; v.classList.remove("drag"); persist(); }
    v.addEventListener("pointerup", end);
    v.addEventListener("pointercancel", end);
    v.addEventListener("dblclick", function () { var t = at(); if (t) { set(t, 0.5); persist(); } });
    v.addEventListener("keydown", function (e) {
      var t = at();
      if (!t) return;
      var row = t.node.axis === "row", d = e.key === (row ? "ArrowLeft" : "ArrowUp") ? -1 : e.key === (row ? "ArrowRight" : "ArrowDown") ? 1 : 0;
      if (!d) return;
      e.preventDefault();
      set(t, t.node.ratio + d * RATIO_STEP);
      persist();
    });
    return v;
  }
  function sweepGallery(b) {
    b.want.forEach(function (list, el) {
      Array.prototype.slice.call(el.childNodes).forEach(function (c) { if (list.indexOf(c) < 0) el.removeChild(c); });
    });
    [[paneEls, b.used.panes], [splitEls, b.used.splits], [divEls, b.used.splits], [pageEls, b.used.pages]].forEach(function (m) {
      Object.keys(m[0]).forEach(function (k) {
        if (m[1][k]) return;
        var el = m[0][k];
        if (el.parentNode) el.parentNode.removeChild(el);
        delete m[0][k];
      });
    });
  }
  function calm() { return !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches); }
  // refName is what a rail dot calls a docked unit.
  function refName(ref) {
    if (ref.charAt(0) === "t") return ref.slice(2);
    var c = cards.filter(function (x) { return "c:" + x.id === ref; })[0];
    return c ? c.name.textContent || c.kind : ref.slice(2);
  }
  function pageIcon(ref) {
    if (ref.slice(0, 2) === "t:") {
      var topic = topics[ref.slice(2)], el = topic && topic.el;
      var state = el && !el.classList.contains("closed") && el.getAttribute("data-state");
      var states = { implementing: "impl", researching: "research", discussing: "discuss", waiting: "wait", "awaiting-confirmation": "confirm" };
      var glyph = states[state];
      return { href: glyph ? "#i-st-" + glyph : "#i-chat", state: glyph ? "d-gal-" + glyph : "plain" };
    }
    var c = cards.filter(function (x) { return "c:" + x.id === ref; })[0];
    var use = c && c.icon && c.icon.querySelector("use"), href = use && use.getAttribute("href");
    if (!href || !/^#l-[a-z0-9-]+$/.test(href)) {
      var kinds = { explorer: "folder-open", terminal: "square-terminal", preview: "file", web: "globe", panel: "file-text" };
      href = "#l-" + (c && kinds[c.kind] || "file");
    }
    return { href: href, state: "card" };
  }
  function pageCapsule(b, refs) {
    b.textContent = "";
    b.setAttribute("data-count", String(refs.length));
    refs.slice(0, 5).forEach(function (ref) {
      var icon = pageIcon(ref), svg = doc.createElementNS("http://www.w3.org/2000/svg", "svg");
      svg.setAttribute("class", "gal-dot-icon " + icon.state);
      svg.setAttribute("aria-hidden", "true");
      var use = doc.createElementNS("http://www.w3.org/2000/svg", "use");
      use.setAttribute("href", icon.href);
      svg.appendChild(use);
      b.appendChild(svg);
    });
    if (refs.length > 5) b.appendChild(mk("span", "gal-dot-more", "+" + (refs.length - 5)));
  }
  var SYS_META = {
    workpanel: {
      title: "Workpanel",
      sub: "Todo · Agenda · Notes · Manuals",
      svg: '<rect x="3" y="3" width="7" height="7" rx="1.2"/><rect x="14" y="3" width="7" height="7" rx="1.2"/><rect x="14" y="14" width="7" height="7" rx="1.2"/><rect x="3" y="14" width="7" height="7" rx="1.2"/>'
    },
    ticketmap: {
      title: "Ticket Map",
      sub: "Hierarchy · Graph · Inspector",
      svg: '<circle cx="12" cy="5" r="2.2"/><circle cx="5.5" cy="18" r="2.2"/><circle cx="18.5" cy="18" r="2.2"/><path d="M10.9 6.9 6.6 16.1M13.1 6.9l4.3 9.2M7.7 18h8.6"/>'
    },
    git: {
      title: "Git",
      sub: "Graph · Diff · Worktrees",
      svg: '<line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>'
    }
  };
  function sysSvg(id) {
    var m = SYS_META[id];
    return '<svg class="gal-dot-icon" viewBox="0 0 24 24" aria-hidden="true">' + (m ? m.svg : "") + '</svg>';
  }
  function loadSysActive() {
    var v = opt && opt.load ? opt.load(BGAL_SYS_KEY) : null;
    return G.sysIndex(v) >= 0 ? v : null;
  }
  function saveSysActive(id) {
    if (opt && opt.save) opt.save(BGAL_SYS_KEY, G.sysIndex(id) >= 0 ? id : null);
  }
  function curPos() {
    var narrow = !!(lay && lay.narrow);
    return G.position(gal, narrow, narrow ? sysActive : (sysActive || storedSys || G.DEFAULT_SYS));
  }
  function pageElAt(p) {
    var n = gal.pages.length;
    if (p < 0) return null;
    if (p < n) return pageEls[gal.pages[p].id] || null;
    if (p === n) return plusEl;
    var sys = G.sysAt(gal, p);
    return sys ? sysPageEls[sys] || null : null;
  }
  function notifyGalChange(reason) {
    var pos = curPos(), sys = G.sysAt(gal, pos);
    galListeners.slice().forEach(function (fn) {
      try { fn({ reason: reason, pos: pos, sys: sys, narrow: !!(lay && lay.narrow), width: bgalW }); } catch (e) {}
    });
  }
  function buildSysPages() {
    function makeScaffold(id, badgeText, badgeCls, bannerText, bodyEl, jumpLabel) {
      var pg = mk("div", "gal-page gal-sys-page");
      pg.setAttribute("data-page", id);
      pg.setAttribute("data-page-panel", id);
      var shell = mk("div", "gal-sys-shell");
      var hd = mk("div", "gal-sys-head");
      var row = mk("div", "gal-sys-title-row");
      var ic = mk("span", "gal-sys-icon");
      ic.innerHTML = sysSvg(id);
      var meta = mk("div", "gal-sys-meta");
      meta.appendChild(mk("h3", "gal-sys-h", SYS_META[id].title));
      meta.appendChild(mk("p", "gal-sys-sub", SYS_META[id].sub));
      var badge = mk("span", "gal-sys-badge " + badgeCls, badgeText);
      row.appendChild(ic); row.appendChild(meta); row.appendChild(badge);
      hd.appendChild(row);
      if (bannerText) {
        var ban = mk("div", "gal-sys-banner");
        ban.appendChild(mk("span", "gal-sys-bdot"));
        ban.appendChild(mk("span", "", bannerText));
        hd.appendChild(ban);
      }
      shell.appendChild(hd);
      var body = mk("div", "gal-sys-body");
      if (bodyEl) body.appendChild(bodyEl);
      shell.appendChild(body);
      if (jumpLabel) {
        var foot = mk("div", "gal-sys-foot");
        var jbtn = mk("button", "gal-sys-jump", jumpLabel);
        jbtn.type = "button";
        jbtn.addEventListener("click", function () { showSys("ticketmap"); });
        foot.appendChild(jbtn);
        shell.appendChild(foot);
      }
      pg.appendChild(shell);
      return pg;
    }
    function makeZone(title, chip, desc) {
      var z = mk("section", "gal-sys-zone");
      var zh = mk("div", "gal-sys-zone-hd");
      zh.appendChild(mk("strong", "", title));
      zh.appendChild(mk("span", "gal-sys-chip", chip));
      z.appendChild(zh);
      z.appendChild(mk("p", "gal-sys-zone-desc", desc));
      return z;
    }
    // The Workpanel page is one full-fill host: the page's server-rendered
    // Workpanel (dashboard.tmpl "workpanel", #wproot, holding the panels
    // region) is adopted into it by dashboard-workpanel.js.
    var wpPg = mk("div", "gal-page gal-sys-page gal-sys-wppage");
    wpPg.setAttribute("data-page", "workpanel");
    wpPg.setAttribute("data-page-panel", "workpanel");
    var wpDock = mk("div", "wp-dock");
    wpDock.id = "wpdock";
    wpPg.appendChild(wpDock);
    sysPageEls.workpanel = wpPg;

    // The Ticket Map page is one full-fill host: the ticket map
    // (dashboard-ticketmap.js) docks its root in it when the page shows,
    // and its bare shortcuts act only while focus is inside it.
    var tmPg = mk("div", "gal-page gal-sys-page gal-sys-tmpage");
    tmPg.setAttribute("data-page", "ticketmap");
    tmPg.setAttribute("data-page-panel", "ticketmap");
    var tmDock = mk("div", "tm-docked");
    tmDock.id = "tmdock";
    tmDock.tabIndex = 0;
    tmDock.setAttribute("role", "region");
    tmDock.setAttribute("aria-label", SYS_META.ticketmap.title);
    tmPg.appendChild(tmDock);
    sysPageEls.ticketmap = tmPg;

    // The Git page is a full-fill host as well: the session page's Git page
    // (dashboard-gitpage.js) moves its root (#gitpage) into it when the page
    // first shows; Ctrl+G focuses it. A container query on it picks the wide
    // three-region grid or the narrow tabs.
    var gitPg = mk("div", "gal-page gal-sys-page gal-sys-gitpage");
    gitPg.setAttribute("data-page", "git");
    gitPg.setAttribute("data-page-panel", "git");
    var gitDock = mk("div", "gp-dock");
    gitDock.id = "gitdock";
    gitDock.tabIndex = -1;
    gitDock.setAttribute("role", "region");
    gitDock.setAttribute("aria-label", SYS_META.git.title);
    gitPg.appendChild(gitDock);
    sysPageEls.git = gitPg;

    G.SYS.forEach(function (id) { viewEl.appendChild(sysPageEls[id]); });
  }
  // paintGallery draws the rail in exact top-to-bottom order:
  // [Home (narrow)] -> [User pages] -> [+] -> [.gal-sep] -> [.gal-group-sys: workpanel, ticketmap, git]
  // and shows the current page. dir (+1 down, -1 up) animates the flip.
  function paintGallery(dir) {
    if (!galEl) return;
    var narrow = !!(lay && lay.narrow), n = gal.pages.length, pos = curPos(), seen = {};
    var topKeys = (narrow ? ["main"] : []).concat(gal.pages.map(function (pg) { return pg.id; }), [G.PLUS]);
    var topNodes = [];
    topKeys.forEach(function (k, i) {
      var p = narrow ? i - 1 : i, b = dotEls[k], label;
      if (!b) {
        b = dotEls[k] = mk("button", "gal-dot");
        b.type = "button";
      }
      seen[k] = true;
      b.className = "gal-dot" + (k === "main" ? " main" : k === G.PLUS ? " empty" : " page") + (p === pos ? " active" : "");
      b.setAttribute("data-pos", String(p));
      b.removeAttribute("data-sys");
      if (k === "main") { b.removeAttribute("data-count"); b.textContent = "⌂"; label = wsT("tab.board"); }
      else if (k === G.PLUS) { b.removeAttribute("data-count"); b.textContent = "+"; label = wsT("js.pins.drop"); }
      else {
        var refs = G.leaves(gal.pages[p].root);
        pageCapsule(b, refs);
        label = (p + 1) + ": " + refs.map(refName).join(" + ");
      }
      if (window.WsGalPreview) b.removeAttribute("title");
      else b.title = label;
      b.setAttribute("aria-label", label);
      if (p === pos) b.setAttribute("aria-current", "page"); else b.removeAttribute("aria-current");
      topNodes.push(b);
    });
    if (!sepEl) {
      sepEl = mk("span", "gal-sep");
      sepEl.setAttribute("role", "separator");
      sepEl.setAttribute("aria-orientation", "horizontal");
    }
    if (!sysGroupEl) {
      sysGroupEl = mk("div", "gal-group-sys");
      sysGroupEl.setAttribute("role", "group");
      sysGroupEl.setAttribute("aria-label", "System pages");
    }
    G.SYS.forEach(function (id, si) {
      var k = "sys:" + id, p = n + 1 + si, b = dotEls[k];
      if (!b) {
        b = dotEls[k] = mk("button", "gal-dot sys");
        b.type = "button";
        b.innerHTML = sysSvg(id);
      }
      seen[k] = true;
      // The Workpanel slot's approval wait (dashboard-workpanel.js
      // drawerWait) survives a repaint.
      var wait = b.classList.contains("wait");
      b.className = "gal-dot sys" + (p === pos ? " active" : "") + (wait ? " wait" : "");
      b.setAttribute("data-pos", String(p));
      b.setAttribute("data-sys", id);
      var label = SYS_META[id] ? SYS_META[id].title : id;
      if (wait) label += "; " + wsT("js.drawer.waiting");
      if (window.WsGalPreview) b.removeAttribute("title");
      else b.title = label;
      b.setAttribute("aria-label", label);
      if (p === pos) b.setAttribute("aria-current", "page"); else b.removeAttribute("aria-current");
      if (sysGroupEl.children[si] !== b) sysGroupEl.insertBefore(b, sysGroupEl.children[si] || null);
    });
    Object.keys(dotEls).forEach(function (k) {
      if (seen[k]) return;
      if (dotEls[k].parentNode) dotEls[k].parentNode.removeChild(dotEls[k]);
      delete dotEls[k];
    });
    var wantTop = topNodes.concat([sepEl, sysGroupEl]);
    wantTop.forEach(function (node, idx) {
      if (dotsEl.children[idx] !== node) dotsEl.insertBefore(node, dotsEl.children[idx] || null);
    });
    while (dotsEl.children.length > wantTop.length) dotsEl.removeChild(dotsEl.lastChild);
    var rail = !!(lay && lay.rail), shown = rail;
    galEl.hidden = !shown && !doc.documentElement.hasAttribute("data-overview-preview");
    railEl.hidden = !rail;
    galEl.setAttribute("data-pos", String(pos));
    galEl.setAttribute("data-pages", String(n));
    var curSys = G.sysAt(gal, pos);
    if (curSys) galEl.setAttribute("data-sys-active", curSys); else galEl.removeAttribute("data-sys-active");
    if (narrow) galEl.setAttribute("data-narrow", ""); else galEl.removeAttribute("data-narrow");
    gal.pages.forEach(function (pg, i) { slide(pageEls[pg.id], i - pos, dir); });
    slide(plusEl, n - pos, dir);
    G.SYS.forEach(function (id, si) { slide(sysPageEls[id], n + 1 + si - pos, dir); });
    var activeDot = dotEls[dotKey(pos)];
    if (rail && !galEl.hidden && activeDot && dotsEl.scrollHeight > dotsEl.clientHeight) {
      var listBox = dotsEl.getBoundingClientRect(), dotBox = activeDot.getBoundingClientRect();
      if (dotBox.top < listBox.top) dotsEl.scrollTop += dotBox.top - listBox.top;
      else if (dotBox.bottom > listBox.bottom) dotsEl.scrollTop += dotBox.bottom - listBox.bottom;
    }
    away(rail && narrow && pos >= 0);
    homeWheel(rail && narrow);
    if (window.WsGalPreview && window.WsGalPreview.sync) window.WsGalPreview.sync();
  }
  // away stands the narrow view's Main Board (Page 0) off the view while a
  // docked page shows over it, as slide does an inactive page: the root's
  // data-gal-away translates the board column's content out of the
  // column's clip (the stylesheet), so intersection observers see the
  // board out of view (viewed topics, terminals) while its DOM and scroll
  // offset stay as they were; it takes no focus (inert).
  function away(on) {
    var r = doc.documentElement;
    if (on === r.hasAttribute("data-gal-away")) return;
    if (on) r.setAttribute("data-gal-away", ""); else r.removeAttribute("data-gal-away");
    if (board) board.inert = on;
    // Return upward from Pinned Page 1 lands back on the last Hero Panel.
    if (!on && window.WsBoardAnchor && window.WsBoardAnchor.restore) window.WsBoardAnchor.restore();
  }
  // slide stands a page off positions from the one shown: an inactive page
  // stays mounted, translated out of the clipped viewport (the stylesheet's
  // --gal-off), so intersection observers see it out of view and its frames
  // keep their documents; it takes no focus (inert). The page that comes
  // into view jumps there, or settles from the flip's overshoot.
  function slide(el, off, dir) {
    if (!el) return;
    if (off) {
      el.removeAttribute("data-active");
      el.style.setProperty("--gal-off", String(off));
      el.setAttribute("aria-hidden", "true");
      el.inert = true;
      return;
    }
    var was = el.hasAttribute("data-active");
    el.setAttribute("data-active", "");
    el.style.removeProperty("--gal-off");
    el.removeAttribute("aria-hidden");
    el.inert = false;
    if (was) return;
    el.style.transition = "none";
    el.style.transform = dir && !calm() ? "translateY(" + dir * G.C.OVERSHOOT_PX + "px)" : "";
    void el.offsetWidth;
    el.style.transition = "";
    el.style.transform = "";
  }
  // galSet takes a new model: the units' pin orders follow it, and moved
  // (the unit whose place changed, if any) moves; the record is saved. A
  // card whose docking changed tells its kind (a preview's transient state
  // ends).
  function galSet(g, moved) {
    var was = cards.map(function (c) { return !!c.pinned; });
    gal = g;
    if (gal.active !== null) sysActive = null;
    var list = cards.slice();
    sync();
    list.forEach(function (c) { c.paint(); });
    arrange(moved || null);
    persist();
    list.forEach(function (c, i) { if (was[i] !== !!c.pinned && c.k.pinned && cards.indexOf(c) >= 0) c.k.pinned(c); });
    notifyGalChange("model");
  }
  // show switches the gallery to position pos (WsGallery.position: -1 the
  // narrow view's Main Board, then user pages, +, then fixed system pages),
  // animated by dir.
  function show(pos, dir) {
    var narrow = !!(lay && lay.narrow), n = gal.pages.length;
    var min = narrow ? -1 : 0, max = n + G.SYS.length;
    pos = Math.max(min, Math.min(max, pos));
    var cur = curPos();
    if (pos === cur) return false;
    var nextSys = G.sysAt(gal, pos);
    var g = G.activate(gal, pos, narrow);
    if (window.WsGalPreview && window.WsGalPreview.hide) window.WsGalPreview.hide();
    gal = g;
    sysActive = nextSys;
    if (nextSys) {
      storedSys = nextSys;
      saveSysActive(nextSys);
    }
    if (pullEl) { pullOff(); gate.clear(); }
    paintGallery(dir === undefined ? (pos > cur ? 1 : -1) : dir);
    persist();
    notifyGalChange("show");
    return true;
  }
  function showSys(id) {
    if (!gal || !galEl) return false;
    var pos = G.sysPos(gal, id);
    if (pos < 0) return false;
    if (pos === curPos()) { notifyGalChange("show"); return true; }
    return show(pos, 0);
  }
  function step(d) { show(curPos() + d, d); }
  // activate shows the page a node stands on (in the narrow view a node on
  // the Main Board shows Page 0) and tells whether it switched; a caller
  // scrolls the node into view after it.
  function activate(node) {
    if (!node || !board || !galEl || !node.closest) return false;
    var narrow = !!(lay && lay.narrow), pane = node.closest(".gal-pane[data-ref]"), want;
    if (pane && galEl.contains(pane)) {
      want = G.pageOf(gal, pane.getAttribute("data-ref"));
      if (want < 0) return false;
    } else if (narrow && board.contains(node)) want = -1;
    else return false;
    return want !== curPos() && show(want, 0);
  }
  // buildGalleryEl builds the gallery's element in host: the width handle,
  // the rail and the viewport with its + and system pages; the rail navigates
  // (a dot's click or Enter, a wheel tick, the empty rail above or below the
  // dots, a vertical press-drag) and a dragged page dot reorders user pages.
  function buildGalleryEl(host) {
    galEl = mk("aside", "bgal");
    galEl.id = "bgal";
    galEl.setAttribute("aria-label", wsT("js.pins.title"));
    galEl.hidden = true;
    railEl = mk("div", "gal-rail");
    railEl.setAttribute("role", "navigation");
    railEl.setAttribute("aria-label", wsT("js.pins.title"));
    var up = mk("div", "gal-rzone up", "▲"), down = mk("div", "gal-rzone down", "▼");
    [up, down].forEach(function (z) { z.setAttribute("aria-hidden", "true"); });
    dotsEl = mk("div", "gal-dots");
    [up, dotsEl, down].forEach(function (n) { railEl.appendChild(n); });
    viewEl = mk("div", "gal-viewport");
    plusEl = mk("div", "gal-page gal-plus", wsT("js.pins.drop"));
    plusEl.setAttribute("data-page", G.PLUS);
    viewEl.appendChild(plusEl);
    buildSysPages();
    galEl.appendChild(railEl);
    galEl.appendChild(viewEl);
    edgeHandle();
    host.appendChild(galEl);
    dotsEl.addEventListener("click", function (e) {
      var b = e.target.closest && e.target.closest(".gal-dot");
      if (b && !railMoved) show(+b.getAttribute("data-pos"));
    });
    up.addEventListener("click", function () { if (!railMoved) step(-1); });
    down.addEventListener("click", function () { if (!railMoved) step(1); });
    railEl.addEventListener("wheel", function (e) {
      if (e.preventDefault) e.preventDefault();
      var dir = railWheel.wheel({ dy: +e.deltaY || 0, deltaMode: e.deltaMode || 0, now: Date.now() });
      if (dir) step(dir);
    }, { passive: false });
    viewEl.addEventListener("wheel", function (e) { gateWheel(e, false); }, { passive: false });
    railEl.addEventListener("pointerdown", function (e) {
      if (rg || dg || (e.button !== undefined && e.button !== 0)) return;
      var b = e.target.closest && e.target.closest(".gal-dot"), p = b ? +b.getAttribute("data-pos") : NaN;
      rg = { id: e.pointerId, x0: e.clientX, y0: e.clientY, from: curPos(), on: false,
        dot: b && p >= 0 && p < gal.pages.length ? p : null, onDot: !!b, target: null };
    });
    if (window.WsGalPreview && window.WsGalPreview.attach) {
      window.WsGalPreview.attach({
        railEl: railEl,
        dotsEl: dotsEl,
        t: wsT,
        isOverview: function () { return overviewOn; },
        isDragging: function () { return !!(dg && dg.on); },
        getState: function () {
          var narrow = !!(lay && lay.narrow);
          return { gal: gal, narrow: narrow, pos: curPos() };
        },
        getSysInfo: function (id) { return SYS_META[id] || null; },
        getLeafInfo: function (ref) {
          var icon = pageIcon(ref);
          if (ref.slice(0, 2) === "t:") {
            var name = ref.slice(2), topic = topics[name], el = topic && topic.el;
            var tnEl = el && el.querySelector && el.querySelector(":scope > .tophead > .tbar .tname");
            var title = (tnEl && tnEl.textContent ? tnEl.textContent.trim() : "") || name;
            var rtextEl = el && el.querySelector && el.querySelector(":scope > .tophead > .tsub .trecap .rtext");
            var subEl = rtextEl || (el && el.querySelector && el.querySelector(":scope > .tophead > .tsub"));
            var askChip = el && el.querySelector && el.querySelector(":scope > .tophead > .tbar > .chip.policy");
            var okChip = el && el.querySelector && el.querySelector(":scope > .tophead > .tbar > .chip.ok");
            var askNumEl = askChip && !askChip.hidden ? (askChip.querySelector(".n") || askChip) : null;
            var asksCount = askNumEl && askNumEl.textContent ? askNumEl.textContent.trim() : "";
            var asks = el ? (askChip ? !!(asksCount || (okChip && !okChip.hidden)) : (el.hasAttribute("data-open-asks") ? el.getAttribute("data-open-asks") === "true" : false)) : false;
            var closed = !!(el && el.classList.contains("closed"));
            var rawState = el ? (el.getAttribute("data-state") || "") : "";
            var state = closed ? "closed" : rawState;
            var stateMap = {
              implementing: ["impl", "js.wb.state.implementing"],
              researching: ["research", "js.wb.state.researching"],
              discussing: ["discuss", "js.wb.state.discussing"],
              waiting: ["wait", "js.wb.state.waiting"],
              "awaiting-confirmation": ["confirm", "js.wb.state.awaiting-confirmation"],
              closed: ["closed", "js.board.filter.closed"]
            };
            var sm = stateMap[state];
            var stateCode = sm ? sm[0] : "";
            var stateLabel = "";
            if (sm) {
              var tr = wsT(sm[1]);
              stateLabel = tr && tr !== sm[1] ? tr : (state === "closed" ? "Closed" : state);
            }
            return {
              title: title,
              iconHref: icon.href,
              iconState: icon.state,
              state: state,
              stateCode: stateCode,
              stateLabel: stateLabel,
              sub: subEl && subEl.textContent ? subEl.textContent.trim() : "",
              asks: asks,
              asksCount: asksCount,
              closed: closed,
              kind: "topic"
            };
          }
          var c = cards.filter(function (x) { return "c:" + x.id === ref; })[0];
          var folded = c && c.k && typeof c.k.folded === "function" ? String(c.k.folded(c) || "").trim() : "";
          if (!folded && c && c.sub && c.sub.textContent) folded = c.sub.textContent.trim();
          var badge = c && c.badge && c.badge.textContent ? c.badge.textContent.trim() : "";
          return {
            title: refName(ref),
            iconHref: icon.href,
            iconState: icon.state,
            state: "card",
            stateCode: "card",
            stateLabel: badge,
            sub: folded,
            asks: false,
            closed: false,
            kind: c ? c.kind : "card"
          };
        }
      });
    }
  }
  // The rail's press: past SLOP px a press on a page dot drags it (dropped
  // on another page's dot, the page moves there), a press elsewhere on the
  // rail pans (RAIL_PAN px per page); either swallows the click it ends in.
  var RAIL_PAN = 36;
  function railMove(e) {
    var r = rg;
    if (!r || e.pointerId !== r.id) return;
    if (!r.on) {
      if (Math.abs(e.clientX - r.x0) <= SLOP && Math.abs(e.clientY - r.y0) <= SLOP) return;
      if (r.onDot && r.dot === null) { rg = null; return; }
      r.on = true;
      railMoved = true;
      if (r.dot !== null) galEl.setAttribute("data-dotdrag", "");
    }
    if (e.preventDefault) e.preventDefault();
    if (r.dot !== null) {
      var t = dotAt(e.clientX, e.clientY);
      r.target = t && t.pos >= 0 && t.pos < gal.pages.length && t.pos !== r.dot ? t : null;
      markDrop(r.target && r.target.el);
      return;
    }
    var n = gal.pages.length, narrow = !!(lay && lay.narrow);
    var next = Math.max(narrow ? -1 : 0, Math.min(n + G.SYS.length, r.from + Math.round((e.clientY - r.y0) / RAIL_PAN)));
    if (next !== curPos()) show(next);
  }
  function railEnd(e) {
    var r = rg;
    if (!r || (e && e.pointerId !== r.id)) return;
    rg = null;
    if (!r.on) return;
    galEl.removeAttribute("data-dotdrag");
    markDrop(null);
    setTimeout(function () { railMoved = false; }, 0);
    if (e && e.type === "pointerup" && r.dot !== null && r.target) {
      var id = gal.pages[r.dot].id, g = G.movePage(gal, r.dot, r.target.pos);
      galSet(G.activate(g, G.pageIndex(g, id), !!(lay && lay.narrow)), null);
    }
  }
  // dotAt is the rail dot at (x, y): {pos, el}, or null.
  function dotAt(x, y) {
    if (!galEl || galEl.hidden || !railEl || railEl.hidden) return null;
    var hit = null;
    Array.prototype.some.call(dotsEl.querySelectorAll(".gal-dot"), function (b) {
      if (!inside(rectOf(b), x, y)) return false;
      hit = { pos: +b.getAttribute("data-pos"), el: b };
      return true;
    });
    return hit;
  }
  // markDrop lights the one drop target a drag would land on.
  var lit = null;
  function markDrop(el) {
    if (lit === el) return;
    if (lit) lit.classList.remove("drop");
    lit = el || null;
    if (lit) lit.classList.add("drop");
  }
  function clock() { return window.performance && window.performance.now ? window.performance.now() : Date.now(); }
  // dotKey is the key of the rail dot (dotEls) for position p.
  function dotKey(p) {
    var n = gal.pages.length;
    if (p < 0) return "main";
    if (p < n) return gal.pages[p].id;
    if (p === n) return G.PLUS;
    var sys = G.sysAt(gal, p);
    return sys ? "sys:" + sys : G.PLUS;
  }

  // --- spring-loaded rail dots -----------------------------------------------
  // While a drag runs, a rail dot hovered SPRING_MS (WsGallery.Hover) shows
  // its page, so the drag can go on to a pane's zone or into +; the dot
  // carries .spring meanwhile. A drop before then keeps the dot's own drop
  // (+ docks on a new page, a page's dot appends, ⌂ undocks). The dot of
  // the page shown never springs.
  var hover = new G.Hover(), springT = 0;
  function spring(hit) {
    var el = hit && hit.el.classList.contains("gal-dot") && !hit.el.classList.contains("sys") && hit.pos !== curPos() ? hit.el : null;
    if (el === hover.target) return;
    unspring();
    if (!el) return;
    hover.over(el, clock());
    el.classList.add("spring");
    if (window.WsGalPreview && window.WsGalPreview.springStart) window.WsGalPreview.springStart(el, G.C.SPRING_MS);
    springT = setTimeout(sprung, G.C.SPRING_MS);
  }
  function unspring() {
    clearTimeout(springT);
    springT = 0;
    if (hover.target) hover.target.classList.remove("spring");
    hover.clear();
    if (window.WsGalPreview && window.WsGalPreview.springStop) window.WsGalPreview.springStop();
  }
  // sprung fires the hover: the page shows, and the drop under the
  // pointer is read again (over otherwise runs on a pointer move only).
  function sprung() {
    springT = 0;
    var el = hover.target, now = clock();
    if (!el) return;
    if (!hover.fire(now)) { springT = setTimeout(sprung, Math.max(1, hover.since + hover.ms - now)); return; }
    el.classList.remove("spring");
    if (!dg || !dg.on) return;
    if (window.WsGalPreview && window.WsGalPreview.sprung) window.WsGalPreview.sprung(el);
    show(+el.getAttribute("data-pos"));
    over(dg.x, dg.y);
  }

  // --- the boundary bounce-snap gate -----------------------------------------
  // A wheel event over the page shown (or, in the narrow view, over the
  // Main Board on Page 0) runs through WsGallery.Gate: past the scroll
  // limit of the pane under the pointer it charges tension toward the
  // adjacent page (a rubber band on the page, a pill with the target and
  // its percentage, the target's dot marked), flips there at 100%, and
  // springs back when idle; momentum and an edge with no page past it get
  // the damped micro-bounce. One timer asks the gate to settle (gate.due).
  // Wheel only: touch scrolling never charges it.
  // gateIn is the one input object every event refills (no allocation per
  // event); nb the neighbors of the model nbOf in the view nbNarrow.
  var gate = new G.Gate(), gateT = 0, pullEl = null, pullDot = null, pills = null, homeEl = null, homeScr = null;
  var gateIn = { dy: 0, now: 0, canUp: false, canDown: false, prev: null, next: null }, nb = null, nbOf = null, nbNarrow = null;
  // OWN is what takes its own wheel events: a terminal, a non-doc-frame
  // iframe, a canvas or media, a maximized sheet, and the bodies of terminal
  // and web cards (a preview card's body is an ordinary scroller; its frames
  // and media are OWN themselves; a topic's .doc-frame forwards its wheel to
  // the gate).
  var OWN = '.xterm, .tm-host, .d-tm-cvp, iframe:not(.doc-frame), canvas, video, embed, object, [data-max], [data-max] *, .bcard:is([data-kind="terminal"], [data-kind="web"]) > .bc-in > .bc-body';
  var PANE_BODY = ':scope > section.topic > .gal-pbody, :scope > .bcard > .bc-in > .bc-body';
  function reach(s, dy) {
    return dy > 0 ? s.scrollTop + s.clientHeight < s.scrollHeight - G.C.EPS : s.scrollTop > G.C.EPS;
  }
  // canScroll tells whether a wheel by dy over t still scrolls natively:
  // the scrollers from t up to bound (both included; the pane, else the
  // page or the view, or on Page 0 the board's scroller) are asked
  // nearest first, as the browser chains the scroll, and a scroller at
  // its limit whose overscroll behavior contains it ends the chain. Only
  // an element that overflows pays for its computed style. With no
  // scroller on the way, fall (the pane's body, the board's scroller)
  // answers.
  function canScroll(t, bound, dy, fall) {
    var any = false, top = doc.scrollingElement;
    for (var n = t; n && n.nodeType === 1; n = n.parentNode) {
      if (n.scrollHeight - n.clientHeight > G.C.EPS) {
        var cs = n === top ? null : getComputedStyle(n);
        if (!cs || /auto|scroll/.test(cs.overflowY)) {
          any = true;
          if (reach(n, dy)) return true;
          if (cs && /contain|none/.test(cs.overscrollBehaviorY)) return false;
        }
      }
      if (n === bound || n === top) break;
    }
    return !any && !!fall && reach(fall, dy);
  }
  // gateWheelInput runs a wheel delta (from a native WheelEvent or a
  // forwarded doc-frame wheel message) through the gate; home: it comes from
  // the narrow view's Main Board. The scroller is the nearest one under the
  // pointer that still scrolls that way, within its pane or, on Page 0,
  // within the board's scroller (canScroll). Predominantly horizontal swipes,
  // events inside OWN widgets, or anything while the overview runs never
  // charge.
  function gateWheelInput(target, rawDy, rawDx, deltaMode, home, prevent) {
    if (overviewOn || !lay || !target || !target.closest) return;
    var narrow = !!lay.narrow, pos = curPos();
    if (home ? pos >= 0 || (galEl && galEl.contains(target)) : pos < 0) return;
    var dy = +rawDy || 0, dx = +rawDx || 0;
    var scale = deltaMode === 1 ? 40 : deltaMode === 2 ? 800 : 1;
    dy *= scale;
    dx *= scale;
    if (Math.abs(dx) > Math.abs(dy) * 1.15) return;
    if (target.closest(OWN)) return;
    var bound, fall = null, can;
    if (home) {
      if (!homeScr) return;
      var panel = target.closest("section.topic[data-topic], .bcard[data-card]");
      bound = panel || homeScr;
      can = panel ? canScroll(target, panel, dy, null) : false;
      if (!can) {
        var anchor = window.WsBoardAnchor, list = anchor && anchor.cards ? anchor.cards() : [];
        var first = list.length ? list[0] : null, last = list.length ? list[list.length - 1] : null;
        if (dy < 0) {
          can = (first && first.hasAttribute && first.hasAttribute("data-wb-offwindow")) || reach(homeScr, dy);
        } else if (last && last.hasAttribute && last.hasAttribute("data-wb-offwindow")) {
          can = true;
        } else if (reach(homeScr, dy)) {
          if (!last || (panel && panel !== last)) can = true;
          else {
            var more = board && board.querySelector("button.closed-more[data-closed-next]:not([hidden]):not([data-state-filtered])");
            var colRect = homeScr.getBoundingClientRect();
            if (more && (!more.hasAttribute || !more.hasAttribute("data-wb-offwindow")) && more.getBoundingClientRect().bottom > colRect.bottom + G.C.EPS) can = true;
            else can = last.getBoundingClientRect().bottom > colRect.bottom + G.C.EPS;
          }
        }
      }
    } else {
      bound = target.closest(".gal-pane, .gal-page") || viewEl;
      if (bound && bound.classList.contains("gal-pane")) fall = bound.querySelector(PANE_BODY);
      can = canScroll(target, bound, dy, fall);
    }
    var w = gateIn;
    nb = G.neighbors(gal, pos, narrow); nbOf = gal; nbNarrow = narrow;
    w.dy = dy;
    w.now = clock();
    w.canUp = dy < 0 && can;
    w.canDown = dy > 0 && can;
    w.prev = nb.prev;
    w.next = nb.next;
    var r = gate.wheel(w);
    if (r.kind === "none") return;
    if (r.kind === "pass") { if (r.reset) pullOff(); gateArm(); return; }
    if (prevent) prevent();
    var el = home ? board : pageElAt(pos), to = pos + (r.dir || 0);
    if (r.kind === "reset") pullOff();
    else if (r.kind === "micro") pullShow(el, r.dir, r.shift, 0, "damped", wsT(r.edge ? "js.pins.edge" : "js.pins.wait"), null);
    else if (r.kind === "pull") pullShow(el, r.dir, r.shift, r.pct, r.ready ? "ready" : "", pullName(to) + " (" + Math.round(r.pct * 100) + "%)", to);
    else if (r.kind === "flip") { pullOff(); show(to, r.dir); }
    gateArm();
  }
  // gateWheel runs native wheel event e through the gate; an event that a
  // widget took (defaultPrevented) or a pinch (ctrlKey) never charges.
  function gateWheel(e, home) {
    if (e.defaultPrevented || e.ctrlKey || e.metaKey) return;
    gateWheelInput(e.target, e.deltaY, e.deltaX, e.deltaMode, home, function () { e.preventDefault(); });
  }
  // onFrameWheel handles passive wheel deltas forwarded from a topic's
  // .doc-frame iframe (which cannot bubble across the iframe boundary).
  function onFrameWheel(frame, dy, dx, mode) {
    if (!frame || !frame.isConnected) return false;
    if (galEl && galEl.contains(frame)) {
      var activePage = pageElAt(curPos());
      if (!activePage || !activePage.contains(frame)) return false;
      gateWheelInput(frame, dy, dx, mode || 0, false, null);
      return true;
    }
    if (homeEl && homeEl.contains(frame)) {
      gateWheelInput(frame, dy, dx, mode || 0, true, null);
      return true;
    }
    return false;
  }
  function pullName(p) {
    var b = dotEls[dotKey(p)];
    return p === gal.pages.length ? G.PLUS : b ? (b.title || b.getAttribute("aria-label") || "") : "";
  }
  function gateArm() {
    clearTimeout(gateT);
    gateT = 0;
    var d = gate.due();
    if (d !== null) gateT = setTimeout(gateTick, Math.max(0, d - clock()) + 1);
  }
  function gateTick() {
    gateT = 0;
    if (gate.tick(clock())) pullOff(); else gateArm();
  }
  function pillEls() {
    if (!pills) pills = [-1, 1].map(function (dir) {
      var p = mk("div", "gal-pullpill " + (dir > 0 ? "bottom" : "top")), txt = mk("div", "gal-pulltxt"), track = mk("div", "gal-pulltrack"), fill = mk("div", "gal-pullfill");
      p.setAttribute("aria-hidden", "true");
      track.appendChild(fill);
      p.appendChild(txt);
      p.appendChild(track);
      return { el: p, txt: txt, fill: fill };
    });
    pills.forEach(function (p) { if (p.el.parentNode !== viewEl) viewEl.appendChild(p.el); });
    return pills;
  }
  // pullShow draws a pull or a micro-bounce on el (the page shown, or the
  // Main Board): el shifts shift px against dir (its translate, apart
  // from the slides' transform; none for reduced motion), the pill on
  // dir's side reads text over pct, state "damped" or "ready" (else
  // none), and the dot of the page at target (null: none) is marked.
  function pullShow(el, dir, shift, pct, state, text, target) {
    if (pullEl && pullEl !== el) pullOff();
    pullEl = el;
    el.setAttribute("data-gal-pull", "");
    el.style.translate = calm() ? "" : "0 " + (-dir * shift).toFixed(1) + "px";
    var ps = pills || pillEls(), p = ps[dir > 0 ? 1 : 0], q = ps[dir > 0 ? 0 : 1], t = (dir > 0 ? "↓ " : "↑ ") + text, f = "scaleX(" + (Math.round(pct * 100) / 100) + ")";
    q.el.classList.remove("lit", "ready", "damped");
    p.el.classList.add("lit");
    p.el.classList.toggle("damped", state === "damped");
    p.el.classList.toggle("ready", state === "ready");
    if (p.txt.textContent !== t) p.txt.textContent = t;
    if (p.fill.style.transform !== f) p.fill.style.transform = f;
    var d = target === null ? null : dotEls[dotKey(target)] || null;
    if (pullDot === d) return;
    if (pullDot) pullDot.classList.remove("pull-target");
    pullDot = d;
    if (d) d.classList.add("pull-target");
  }
  // pullOff springs a pull or a micro-bounce back (the stylesheet's
  // transition) and hides the pills and the dot mark.
  function pullOff() {
    if (pullEl) {
      pullEl.removeAttribute("data-gal-pull");
      pullEl.style.translate = "";
      pullEl = null;
    }
    if (pills) pills.forEach(function (p) { p.el.classList.remove("lit", "ready", "damped"); });
    if (pullDot) pullDot.classList.remove("pull-target");
    pullDot = null;
  }
  // homeWheel listens for the gate on the board's column while the narrow
  // view has a docked page (with none the gate is inert there, and the
  // wide view's board never pages), so the board's own scrolling carries
  // no blocking wheel listener otherwise.
  function onHomeWheel(e) { gateWheel(e, true); }
  function homeWheel(on) {
    var col = on && board ? doc.getElementById("bcol") || board.parentNode : null;
    if (col) homeScr = scroller(board) || doc.scrollingElement;
    if (col === homeEl) return;
    if (homeEl) homeEl.removeEventListener("wheel", onHomeWheel);
    homeEl = col;
    if (col) col.addEventListener("wheel", onHomeWheel, { passive: false });
  }
  // canFitFourCol activates the 4-column layout (rail + stage + always-open
  // gallery column) iff !tabs, viewport >= WIDE (1600px), and the stage
  // beside the live left rail leaves spare right width for a column as wide
  // as the board (the grid at its default width, since the board is no
  // longer user-resized). Below that the three-column layout stays: the
  // 34px rail strip on #bcol pages the gallery over the board. Opening room
  // by collapsing the left rail (readRailReserve) can promote it.
  function canFitFourCol(vw, tabs) {
    if (tabs || !(vw >= G.C.WIDE)) return false;
    var gap = 8, stageW = vw - 32 - readRailReserve();
    var split = !!(opt && (opt.split || (board && board.closest && board.closest(".dgrid.live, .dgrid.history"))));
    var worktree = !!(board && board.closest && board.closest(".dgrid.worktree"));
    var gridW, boardW;
    if (worktree) {
      gridW = 1300;
      boardW = (gridW - gap) * 7 / 12;
    } else if (!split) {
      boardW = gridW = 900;
    } else {
      gridW = 1300;
      boardW = convow ? (gridW - gap - Math.max(CONVW_MIN, Math.min(convow, gridW - gap - SPLITB_MIN))) : ((gridW - gap) * 7 / 12);
    }
    return stageW - gridW - gap >= boardW;
  }
  // isFourCol tells whether Band 4 (true 4-column mode) is active for the
  // current viewport and layout mode, matching WsGallery.layout's exact rule.
  function isFourCol() {
    if (!opt || !board) return false;
    var m = opt.mode ? opt.mode() : null, vw = m ? +m.width || 0 : 0, tabs = !!(m && m.tabs);
    return !tabs && vw >= G.C.WIDE && canFitFourCol(vw, tabs);
  }
  // relayout asks the page what it shows (opt.mode: its width and whether
  // the layout menu shows one column at a time), maps that to the
  // gallery's shape (WsGallery.layout, plus pages and dragging) and tells
  // the page when it changes (opt.layout).
  function relayout() {
    if (!opt || !galEl) return;
    var m = opt.mode ? opt.mode() : null, vw = m ? +m.width || 0 : 0, tabs = !!(m && m.tabs);
    var n = gal.pages.length, drag = !!(dg && dg.on);
    applyBgalWidth(vw);
    var s = G.layout({ width: vw, tabs: tabs, fourCol: canFitFourCol(vw, tabs), pages: n, dragging: drag });
    s.pages = n;
    s.dragging = drag;
    if (lay && lay.narrow === s.narrow && lay.column === s.column && lay.overlay === s.overlay && lay.rail === s.rail && !lay.empty === !s.empty && !lay.pages === !n && lay.dragging === drag) return;
    lay = s;
    paintGallery();
    if (opt.layout) opt.layout(s);
    notifyGalChange("layout");
  }
  // decorate gives every topic on the board a pin button in its title bar,
  // before the note pen, pressed while the topic is docked, and the
  // maximize button and its back button right of the pin; a docked topic's
  // body is wrapped in its pane's scroller (wrapTopic), any other topic's
  // is not. A board update brings a changed topic's bar anew (a new
  // section, or the old one with the new bar's content), so this runs
  // after every settle, and draws a large topic or panel large again on the
  // node that now stands for it.
  var ZOOM_STEPS = [0.25, 0.33, 0.5, 0.67, 0.75, 0.9, 1, 1.25, 1.5, 2];
  var MIN_ZOOM = ZOOM_STEPS[0], MAX_ZOOM = ZOOM_STEPS[ZOOM_STEPS.length - 1];
  var zoomByKey = new Map();
  var zoomBound = false;

  function normZoom(z) {
    var n = Number(z);
    if (!isFinite(n) || n <= 0) return 1;
    var best = ZOOM_STEPS[0], bestDist = Math.abs(n - best);
    for (var i = 1; i < ZOOM_STEPS.length; i++) {
      var d = Math.abs(n - ZOOM_STEPS[i]);
      if (d < bestDist) { best = ZOOM_STEPS[i]; bestDist = d; }
    }
    return best;
  }
  function stepZoomValue(cur, dir) {
    if (dir === 0) return 1;
    var z = normZoom(cur);
    var idx = ZOOM_STEPS.indexOf(z);
    if (idx < 0) idx = ZOOM_STEPS.indexOf(1);
    return ZOOM_STEPS[dir > 0 ? Math.min(ZOOM_STEPS.length - 1, idx + 1) : Math.max(0, idx - 1)];
  }
  function zoomSvg(pathD) {
    return '<svg class="li" viewBox="0 0 24 24" aria-hidden="true"><path d="' + pathD + '"/></svg>';
  }
  function zoomLabel(translated, key, fallback) {
    return translated && translated !== key ? translated : fallback;
  }
  function buildZoomGroup(onAction) {
    var grp = doc.createElement("span");
    grp.className = "d-zoom-grp";
    grp.setAttribute("role", "group");
    var tOut = zoomLabel(typeof wsT === "function" ? wsT("js.preview.zoom.out") : "", "js.preview.zoom.out", "Zoom out");
    var tReset = zoomLabel(typeof wsT === "function" ? wsT("js.preview.zoom.reset") : "", "js.preview.zoom.reset", "Reset zoom (100%)");
    var tIn = zoomLabel(typeof wsT === "function" ? wsT("js.preview.zoom.in") : "", "js.preview.zoom.in", "Zoom in");
    var outBtn = doc.createElement("button");
    outBtn.type = "button";
    outBtn.className = "ibtn d-zoom-btn d-zoom-out";
    outBtn.title = tOut;
    outBtn.setAttribute("aria-label", tOut);
    outBtn.innerHTML = zoomSvg("M5 12h14");
    var pctBtn = doc.createElement("button");
    pctBtn.type = "button";
    pctBtn.className = "d-zoom-pct";
    pctBtn.title = tReset;
    pctBtn.setAttribute("aria-label", tReset);
    pctBtn.textContent = "100%";
    var inBtn = doc.createElement("button");
    inBtn.type = "button";
    inBtn.className = "ibtn d-zoom-btn d-zoom-in";
    inBtn.title = tIn;
    inBtn.setAttribute("aria-label", tIn);
    inBtn.innerHTML = zoomSvg("M12 5v14M5 12h14");
    grp._zp = { out: outBtn, pct: pctBtn, in: inBtn };
    grp.appendChild(outBtn);
    grp.appendChild(pctBtn);
    grp.appendChild(inBtn);
    grp.addEventListener("click", function (e) { e.preventDefault(); e.stopPropagation(); });
    if (onAction) {
      outBtn.addEventListener("click", function (e) { e.preventDefault(); e.stopPropagation(); onAction(-1); });
      pctBtn.addEventListener("click", function (e) { e.preventDefault(); e.stopPropagation(); onAction(0); });
      inBtn.addEventListener("click", function (e) { e.preventDefault(); e.stopPropagation(); onAction(1); });
    }
    return grp;
  }
  function applyZoomToGroup(grp, z) {
    if (!grp) return;
    var pct = Math.round(z * 100);
    var p = grp._zp;
    var outBtn = (p && p.out) || (grp.querySelector && grp.querySelector(".d-zoom-out"));
    var pctBtn = (p && p.pct) || (grp.querySelector && grp.querySelector(".d-zoom-pct"));
    var inBtn = (p && p.in) || (grp.querySelector && grp.querySelector(".d-zoom-in"));
    if (pctBtn) pctBtn.textContent = pct + "%";
    if (outBtn) outBtn.disabled = z <= MIN_ZOOM;
    if (inBtn) inBtn.disabled = z >= MAX_ZOOM;
    if (z === 1) grp.removeAttribute && grp.removeAttribute("data-zoom-custom");
    else grp.setAttribute && grp.setAttribute("data-zoom-custom", String(pct));
  }
  function applyZoomToTarget(target, z) {
    if (!target) return;
    if (z === 1) {
      if (target.removeAttribute) target.removeAttribute("data-zoom");
      if (target.style) {
        if (target.style.removeProperty) target.style.removeProperty("--wb-zoom");
        else delete target.style["--wb-zoom"];
      }
    } else {
      if (target.setAttribute) target.setAttribute("data-zoom", String(Math.round(z * 100)));
      if (target.style) {
        if (target.style.setProperty) target.style.setProperty("--wb-zoom", String(z));
        else target.style["--wb-zoom"] = String(z);
      }
    }
  }
  function mockupKeyOfWrap(wrap) {
    if (!wrap || !wrap.querySelector) return "";
    var fr = wrap.querySelector(".doc-frame");
    if (!fr) return "";
    return fr.getAttribute("data-doc-key") || fr.getAttribute("data-doc") || fr.getAttribute("data-mockup-src") || "";
  }
  function getMockupZoom(key) {
    return key && zoomByKey.has(key) ? zoomByKey.get(key) : 1;
  }
  function syncWrapAndChrome(wrap, z) {
    applyZoomToTarget(wrap, z);
    var det = wrap.closest && wrap.closest("details.entry-panel");
    if (det) {
      var sum = null;
      for (var n = det.firstElementChild; n; n = n.nextElementSibling) {
        if (n.tagName === "SUMMARY") { sum = n; break; }
      }
      if (sum) applyZoomToGroup(child(sum, "d-zoom-grp"), z);
    }
    var cardEl = wrap.closest && wrap.closest('.bcard[data-kind="panel"]');
    if (cardEl && cardEl.querySelector) {
      var acts = cardEl.querySelector(".bc-hd .bc-acts");
      if (acts) applyZoomToGroup(child(acts, "d-zoom-grp"), z);
    }
  }
  function syncMockupElement(el) {
    if (!el) return;
    var wrap = el.classList && el.classList.contains("mockup-wrap") ? el : (el.closest ? el.closest(".mockup-wrap") : null);
    if (!wrap && el.querySelector) wrap = el.querySelector(".mockup-wrap");
    if (!wrap) return;
    var key = mockupKeyOfWrap(wrap);
    if (!key) return;
    syncWrapAndChrome(wrap, getMockupZoom(key));
  }
  function setMockupZoom(key, nextZoom) {
    if (!key) return 1;
    var z = normZoom(nextZoom);
    if (z === 1) zoomByKey.delete(key); else zoomByKey.set(key, z);
    var seen = [];
    function checkWrap(w) {
      if (!w || seen.indexOf(w) >= 0) return;
      seen.push(w);
      if (mockupKeyOfWrap(w) === key) syncWrapAndChrome(w, z);
    }
    if (doc.querySelectorAll) {
      Array.prototype.forEach.call(doc.querySelectorAll(".mockup-wrap"), checkWrap);
    }
    cards.forEach(function (c) {
      if (c && c.el && c.el.querySelectorAll) {
        Array.prototype.forEach.call(c.el.querySelectorAll(".mockup-wrap"), checkWrap);
      }
    });
    return z;
  }
  function stepMockupZoom(key, dir) {
    if (!key) return 1;
    return setMockupZoom(key, stepZoomValue(getMockupZoom(key), dir));
  }
  function findMockupWrapForTarget(t) {
    if (!t || !t.closest) return null;
    var wrap = t.closest(".mockup-wrap");
    if (wrap) {
      var ownerDet = wrap.closest("details.entry-panel");
      if (ownerDet && !ownerDet.open) return null;
      if (doc.documentElement && doc.documentElement.hasAttribute("data-overview") && !wrap.closest(".overview-preview-stage") && !wrap.closest(".bgal")) return null;
      return wrap;
    }
    var det = t.closest("details.entry-panel[data-zoom-ctrl][open]");
    if (det) {
      if (doc.documentElement && doc.documentElement.hasAttribute("data-overview") && !det.closest(".overview-preview-stage") && !det.closest(".bgal")) return null;
      return det.querySelector(".doc-body > .mockup-wrap") || det.querySelector(".mockup-wrap");
    }
    var pcard = t.closest('.bcard[data-kind="panel"]');
    if (pcard && (pcard.getAttribute("data-size") !== "fold" || pcard.hasAttribute("data-max") || pcard.hasAttribute("data-overview-live"))) {
      if (doc.documentElement && doc.documentElement.hasAttribute("data-overview") && !pcard.closest(".overview-preview-stage") && !pcard.closest(".bgal")) return null;
      return pcard.querySelector(".panel-card-doc > .mockup-wrap");
    }
    return null;
  }
  function onMockupWheel(e) {
    if (!e || e.defaultPrevented || !(e.ctrlKey || e.metaKey)) return;
    var wrap = findMockupWrapForTarget(e.target);
    if (!wrap) return;
    var key = mockupKeyOfWrap(wrap);
    if (!key) return;
    e.preventDefault();
    if (!e.deltaY) return;
    stepMockupZoom(key, e.deltaY < 0 ? 1 : -1);
  }
  function onMockupZoomMessage(e) {
    if (!e || !e.source || !e.data || typeof e.data !== "object") return;
    var dir = e.data.wsPreviewZoom;
    if (dir !== 1 && dir !== -1 && dir !== 0) return;
    var frames = [];
    function collect(f) {
      if (f && frames.indexOf(f) < 0) frames.push(f);
    }
    if (doc.querySelectorAll) {
      Array.prototype.forEach.call(doc.querySelectorAll(".mockup-wrap > iframe.doc-frame.mockup-frame"), collect);
    }
    cards.forEach(function (c) {
      if (c && c.el && c.el.querySelectorAll) {
        Array.prototype.forEach.call(c.el.querySelectorAll(".mockup-wrap > iframe.doc-frame.mockup-frame"), collect);
      }
    });
    for (var i = 0; i < frames.length; i++) {
      var f = frames[i];
      if (f.contentWindow && e.source === f.contentWindow) {
        var key = mockupKeyOfWrap(f.parentNode) || f.getAttribute("data-doc-key") || f.getAttribute("data-doc") || f.getAttribute("data-mockup-src") || "";
        if (key) stepMockupZoom(key, dir);
        return;
      }
    }
  }

  function decorate(scope) {
    function decorateTopic(sec, inGallery) {
      var head = child(sec, "tophead"), bar = head && child(head, "tbar");
      if (!bar) return;
      var r = topics[sec.getAttribute("data-topic")], on = !!(r && r.pinned && r.el === sec);
      var b = child(bar, "tpin");
      if (!b) {
        b = ibtn("tpin", "pin", wsT("js.card.pin"));
        bar.insertBefore(b, child(bar, "tnote-toggle"));
      }
      [["t-fold", "chevron-down", "js.card.fold"], ["t-raise", "arrow-up-to-line", "js.card.raise"], ["d-max", "maximize-2", "js.card.max"], ["d-min", "minimize-2", "js.card.back"]].forEach(function (a) {
        if (!child(bar, a[0])) bar.insertBefore(ibtn(a[0], a[1], wsT(a[2])), child(bar, "tnote-toggle"));
      });
      sec.setAttribute("data-size", r && r.folded ? "fold" : "base");
      child(bar, "t-fold").setAttribute("aria-expanded", String(!(r && r.folded)));
      b.setAttribute("aria-pressed", String(on));
      if (on) sec.setAttribute("data-docked", ""); else sec.removeAttribute("data-docked");
      if (on && inGallery) wrapTopic(sec); else unwrapTopic(sec);
      Array.prototype.forEach.call(sec.querySelectorAll("details.entry-panel"), function (det) {
        if (det.classList.contains("msg")) return;
        var art = det.parentNode;
        var isPanel = !!(art && art.classList && art.classList.contains("wb-entry") && art.classList.contains("panel"));
        var isHistorical = !!(art && art.classList && art.classList.contains("historical"));
        var sum = null;
        for (var n = det.firstElementChild; n; n = n.nextElementSibling) {
          if (n.tagName === "SUMMARY") { sum = n; break; }
        }
        if (!sum) return;
        var dmax = child(sum, "d-max");
        if (isPanel && !isHistorical && dmax) {
          var pp = child(sum, "ppin");
          if (!pp) {
            pp = ibtn("ppin", "pin", wsT("js.card.pin"));
            sum.insertBefore(pp, dmax);
          }
          var pid = art.getAttribute && art.getAttribute("data-panel");
          var pOn = !!(pid && window.WsPanelCard && window.WsPanelCard.isPinned && window.WsPanelCard.isPinned(pid));
          pp.setAttribute("aria-pressed", String(pOn));
        }
        var mwrap = det.querySelector(".doc-body > .mockup-wrap") || det.querySelector(".mockup-wrap");
        var zkey = mwrap ? mockupKeyOfWrap(mwrap) : "";
        if (mwrap && dmax && zkey) {
          det.setAttribute("data-zoom-ctrl", "");
          if (child(sum, "ppin")) det.setAttribute("data-zoom-pin", "");
          else det.removeAttribute("data-zoom-pin");
          var zgrp = child(sum, "d-zoom-grp");
          if (!zgrp) {
            zgrp = buildZoomGroup(function (dir) {
              stepMockupZoom(mockupKeyOfWrap(mwrap) || zkey, dir);
            });
            sum.insertBefore(zgrp, dmax);
          }
          var curZ = getMockupZoom(zkey);
          applyZoomToGroup(zgrp, curZ);
          applyZoomToTarget(mwrap, curZ);
        } else {
          det.removeAttribute("data-zoom-ctrl");
          det.removeAttribute("data-zoom-pin");
          var stale = child(sum, "d-zoom-grp");
          if (stale && stale.parentNode) stale.parentNode.removeChild(stale);
        }
      });
    }
    if (scope && scope.nodeType === 1) {
      var inGal = !!(galEl && (scope === galEl || galEl.contains(scope)));
      if (scope.matches && scope.matches("section.topic[data-topic]")) {
        decorateTopic(scope, inGal);
        if (maxed && !maxed.files && !(maxed instanceof Card) && maxed.el === scope) follow(maxed);
      } else {
        var secs = scope.querySelectorAll ? scope.querySelectorAll("section.topic[data-topic]") : [];
        if (secs.length) {
          Array.prototype.forEach.call(secs, function (sec) {
            decorateTopic(sec, inGal);
          });
          if (maxed && !maxed.files && !(maxed instanceof Card)) follow(maxed);
        } else {
          var owner = scope.closest && scope.closest("section.topic[data-topic]");
          if (owner) {
            decorateTopic(owner, inGal);
            if (maxed && !maxed.files && !(maxed instanceof Card) && maxed.el === owner) follow(maxed);
          }
        }
      }
      return;
    }
    [board, galEl].forEach(function (box) {
      if (!box) return;
      Array.prototype.forEach.call(box.querySelectorAll("section.topic[data-topic]"), function (sec) {
        decorateTopic(sec, box === galEl);
      });
    });
    if (maxed && !maxed.files && !(maxed instanceof Card)) follow(maxed);
    countTopics();
  }
  // wrapTopic separates a docked topic's head from its body: the parts
  // after .tophead go into one scroller (.gal-pbody) that fills the pane
  // under a head that does not stick, so nothing a deep link scrolls to
  // lands under the head. The whiteboard's markup is unchanged; this is
  // idempotent, and parts a board update brings beside an existing wrapper
  // (a graft keeps the wrapper) move in alone. unwrapTopic gives the stock
  // markup back.
  function wrapTopic(sec) {
    if (window.WsBoardAnchor && window.WsBoardAnchor.hydrate) window.WsBoardAnchor.hydrate(sec, { immediate: true });
    var head = child(sec, "tophead");
    if (!head) return null;
    var body = child(sec, "gal-pbody"), before = [], after = [], past = false, n;
    if (!body) {
      body = mk("div", "gal-pbody");
      sec.insertBefore(body, head.nextSibling);
    }
    for (n = head.nextSibling; n; n = n.nextSibling) {
      if (n === body) past = true;
      else (past ? after : before).push(n);
    }
    var first = body.firstChild;
    before.forEach(function (x) { put(body, x, first); });
    after.forEach(function (x) { put(body, x, null); });
    return body;
  }
  function unwrapTopic(sec) {
    var body = child(sec, "gal-pbody");
    if (!body) return;
    Array.prototype.slice.call(body.childNodes).forEach(function (x) { put(sec, x, body); });
    sec.removeChild(body);
  }
  // leaveSlot marks the place of a topic that leaves a fetched closed page
  // (dashboard.js loadClosed) for the gallery or the board: the page's
  // show-more index counts it as the server does (dashboard.js
  // morphBoard), so the next page neither skips a topic nor shows one
  // twice.
  function leaveSlot(n) {
    var p = n.parentNode, name = topicName(n);
    if (!p || name === null || !p.classList || !p.classList.contains("closed-page")) return;
    var s = mk("span", "closed-slot");
    s.hidden = true;
    s.setAttribute("data-slot", name);
    p.insertBefore(s, n);
  }
  function child(el, cls) {
    for (var n = el.firstElementChild; n; n = n.nextElementSibling) if (n.classList.contains(cls)) return n;
    return null;
  }
  // pinTopic docks a topic on a new page (shown) or undocks it back to the
  // board, as a card's pin button does.
  function pinTopic(sec, on) {
    var name = topicName(sec);
    if (name === null) return;
    var r = topicRec(name);
    r.el = sec;
    r.at = 0;
    galSet(on ? G.dock(gal, refOf(r)) : G.undock(gal, refOf(r)), r);
  }

  // Shared actions adopt the renderer's topic node rather than wrapping or
  // rebuilding its body/badges. Topics have no generic close operation.
  function foldUnit(u, on) {
    if (u.folded === !!on) return;
    u.folded = !!on;
    var head = isCard(u.el) ? child(child(u.el, "bc-in"), "bc-hd") : child(child(u.el, "tophead"), "tbar");
    var active = doc.activeElement;
    for (var n = active; n; n = n.parentNode) if (n === u.el) {
      if (on && head && (!active.closest || !active.closest(".bc-hd, .tbar"))) child(head, isCard(u.el) ? "bc-fold" : "t-fold").focus({ preventScroll: true });
      break;
    }
    if (isCard(u.el)) { u.paint(); if (!on) u.sized(); }
    else { decorate(); tidy(u); }
    persist();
  }
  function topicUnit(sec) {
    var r = topicRec(topicName(sec)); r.el = sec; return r;
  }
  // raise puts an unpinned unit at the head of the flow, and a docked
  // unit's page first in the gallery (shown).
  function raise(u) {
    if (u.pinned) {
      var g = G.movePage(gal, G.pageOf(gal, refOf(u)), 0);
      galSet(G.activate(g, 0, !!(lay && lay.narrow)), null);
      return;
    }
    u.at = headTime();
    delete u.rank;
    if (!isCard(u.el)) u.bar = topicAt(u.el);
    arrange(u);
    persist();
  }
  // A preview origin is a live renderer-owned topic in this board only.
  // Detached links retain the exact node, so replacement/removal invalidates
  // their origin rather than borrowing a same-named or unrelated topic.
  function originTopic(n) {
    if (!n || topicName(n) === null || !n.isConnected) return null;
    for (var p = n.parentNode; p; p = p.parentNode) if (p === board || p === galEl) return n;
    return null;
  }
  // Navigation reveals only the owning whole-topic fold. Inner disclosures
  // and focus belong to the caller; storage and decoration stay with foldUnit.
  function unfoldTopic(node) {
    var sec = originTopic(node && node.closest && node.closest("section.topic[data-topic]"));
    var r = sec && topics[topicName(sec)];
    if (r && r.folded) foldUnit(topicUnit(sec), false);
  }
  // Topic dependency navigation resets only a filter that hides its target.
  // Overview may restore asynchronously, so geometry waits for that restoration.
  // A target on a docked page not shown has that page shown first.
  function revealTopic(node, after) {
    var sec = originTopic(node && node.closest && node.closest("section.topic[data-topic]"));
    if (!sec) return;
    var reveal = function () { unfoldTopic(sec); activate(node); if (after) after(); };
    if (sec.hasAttribute("data-state-filtered")) selectFilter("all", null, false, reveal);
    else reveal();
  }
  function openBefore(c, origin) {
    var n = originTopic(origin), u = n && unitOf(n);
    delete c.rank;
    if (!n || u.pinned) { c.at = headTime(); return; }
    // Fetched closed topics may be nested in a closed-page. Adopt just the
    // origin into the same ordinary flow, leaving its pagination slot intact.
    if (n.parentNode !== board) {
      u = topicUnit(n); u.at = -1; u.bar = topicAt(n);
      arrange(u);
    }
    c.at = eff(n);
    if (c.at === null) c.at = -1;
    var target = 0, prev = null, count = 0;
    Array.prototype.some.call(board.children, function (peer) {
      var other = unitOf(peer), placed = other && !other.loose && other.at;
      if (peer === n) { target = placed ? rank(other) : count; return true; }
      if (peer === c.el || peer === toolbar) return false;
      var time = eff(peer);
      if (time === null) time = -1;
      if (time !== c.at) return false;
      prev = placed ? rank(other) : count++;
      return false;
    });
    c.rank = prev === null ? target - 1 : (prev + target) / 2;
  }

  // --- storage ----------------------------------------------------------------
  // The record is {cards, topics, bw, bb, cw, gallery}: the cards' view
  // state, the topics docked or moved by hand, the width left of the
  // gallery column's handle (bw: the board's .dgrid cell; bb on a page with
  // a split, the board's own column), the conversation's beside the board,
  // and the gallery (WsGallery.record: its pages, split trees and the page
  // shown). cards[].pinned and topics[].p carry the gallery's flat pin
  // orders (WsGallery.ordinals) too, so a previous build, which knows no
  // gallery, shows the docked units as its flat pins; a gallery that
  // disagrees with them (a previous build saved over it) is reconciled
  // against them on load (WsGallery.load). A record from before topics
  // joined is the cards' list alone; a record's w (the column's own width,
  // before the column filled the window) is dropped. bb's presence marks
  // the post-split width layout: only a legacy bw without bb is dropped on
  // its first split-page load. Both modes share a key, so each save
  // preserves the other mode's width.
  function persist() {
    if (!opt || !gal) return;
    var ts = Object.keys(topics).map(function (k) { return topics[k]; }).filter(function (r) { return r.pinned || r.at || r.folded; }).map(function (r) {
      return { n: r.name, p: r.pinned, a: r.at, b: r.bar, f: !!r.folded, rank: r.rank };
    });
    var rec = { cards: cards.map(function (c) {
      return { id: c.id, kind: c.kind, pinned: c.pinned, at: c.at, rank: c.rank, fold: c.folded, h: c.h, data: c.data };
    }), topics: ts };
    rec.cw = convow;
    rec.gallery = G.record(gal);
    opt.save(opt.key, cards.length || ts.length || convow || gal.pages.length ? JSON.stringify(rec) : null);
  }
  function raw() {
    try { return JSON.parse(opt.load(opt.key) || "null"); } catch (e) { return null; }
  }
  function stored() {
    var v = raw();
    if (Array.isArray(v)) v = { cards: v };
    if (!v || typeof v !== "object") v = {};
    return {
      cards: (Array.isArray(v.cards) ? v.cards : []).filter(function (r) { return r && typeof r.kind === "string" && kinds[r.kind]; }),
      topics: (Array.isArray(v.topics) ? v.topics : []).filter(function (r) { return r && typeof r.n === "string" && (+r.p || +r.a || r.f); }),
      cw: +v.cw || 0
    };
  }

  // --- a card -------------------------------------------------------------------
  function Card(kind, rec) {
    this.kind = kind;
    this.k = kinds[kind];
    this.id = rec.id || "c" + Date.now().toString(36) + (seq++).toString(36);
    this.pinned = +rec.pinned || 0;
    this.at = typeof rec.at === "number" && isFinite(rec.at) ? rec.at : headTime();
    if (typeof rec.rank === "number" && isFinite(rec.rank)) this.rank = rec.rank;
    this.folded = !!rec.fold;
    this.h = +rec.h || 0;
    this.data = rec.data || {};
    this.build();
  }
  Card.prototype.build = function () {
    var c = this, el = mk("div", "bcard"), inner = mk("div", "bc-in"), head = mk("div", "bc-hd");
    el.setAttribute("data-card", c.id);
    el.setAttribute("data-kind", c.kind);
    c.el = el;
    // The topic rail (wswhiteboard rail.js) rails the card beside the
    // topics: a capsule with the header's icon and name once it scrolls
    // past, and the header sticking while it is current.
    el.setAttribute("data-rail", c.id);
    head.setAttribute("data-rail-bar", "");
    c.icon = mk("span", "bc-ic");
    c.icon.setAttribute("data-rail-icon", "");
    c.name = mk("span", "bc-nm");
    c.name.setAttribute("data-rail-name", "");
    c.sub = mk("span", "bc-sub");
    c.badge = mk("span", "bc-kind");
    c.acts = mk("span", "bc-acts");
    c.pinBtn = ibtn("bc-pin", "pin", wsT("js.card.pin"));
    c.foldBtn = ibtn("bc-fold", "chevron-down", wsT("js.card.fold"));
    c.raiseBtn = ibtn("bc-raise", "arrow-up-to-line", wsT("js.card.raise"));
    c.maxBtn = ibtn("bc-max", "maximize-2", wsT("js.card.max"));
    c.minBtn = ibtn("bc-min", "minimize-2", wsT("js.card.min"));
    c.closeBtn = ibtn("bc-close", "x", wsT("js.card.close"));
    [c.icon, c.name, c.sub, c.badge, c.acts, c.pinBtn, c.foldBtn, c.raiseBtn, c.maxBtn, c.minBtn, c.closeBtn].forEach(function (n) { head.appendChild(n); });
    c.foldEl = mk("div", "bc-folded");
    c.body = mk("div", "bc-body");
    c.grip = mk("div", "bc-grip");
    c.grip.setAttribute("role", "separator");
    c.grip.setAttribute("aria-orientation", "horizontal");
    c.grip.tabIndex = 0;
    c.grip.title = wsT("js.card.grip");
    c.grip.setAttribute("aria-label", wsT("js.card.grip"));
    [head, c.foldEl, c.body, c.grip].forEach(function (n) { inner.appendChild(n); });
    el.appendChild(inner);
    c.pinBtn.addEventListener("click", function () { c.pin(!c.pinned); });
    c.foldBtn.addEventListener("click", function () { c.fold(!c.folded); });
    c.raiseBtn.addEventListener("click", function () { raise(c); });
    c.maxBtn.addEventListener("click", function () { maximize(c); });
    c.minBtn.addEventListener("click", function () { maximize(null); });
    c.closeBtn.addEventListener("click", async function () {
      if (!c.k.closing || (await c.k.closing(c, c.closeBtn)) !== false) c.close();
    });
    c.wireGrip();
    c.paint();
  };
  // paint writes the framework's own view state to the card's node.
  Card.prototype.paint = function () {
    var c = this;
    c.el.setAttribute("data-size", c.folded ? "fold" : "base");
    if (c.pinned) c.el.setAttribute("data-docked", ""); else c.el.removeAttribute("data-docked");
    c.pinBtn.setAttribute("aria-pressed", String(!!c.pinned));
    c.foldBtn.setAttribute("aria-expanded", String(!c.folded));
    c.body.style.height = (c.h || (typeof c.k.base === "function" ? c.k.base(c) : c.k.base) || BASE) + "px";
    if (maxed === c) sizeSheet(c);
    c.foldEl.textContent = "";
    var f = c.folded && c.k.folded ? c.k.folded(c) : null;
    if (f) c.foldEl.appendChild(f);
  };
  // title sets the header's content: p.icon (a vendored Lucide icon's
  // name), p.name, p.sub (a path, shown dimmed), p.badge (the kind's word).
  Card.prototype.title = function (p) {
    this.icon.innerHTML = p.icon ? licon(p.icon) : "";
    this.name.textContent = p.name || "";
    this.sub.textContent = p.sub || "";
    this.sub.title = p.sub || "";
    this.badge.textContent = p.badge || "";
    this.badge.hidden = !p.badge;
  };
  // actions sets the kind's own header buttons: [{icon, title, run, cls}]
  // (cls: an optional class of the kind's own), and returns them in order,
  // for a kind that updates a button's state.
  Card.prototype.actions = function (list) {
    var c = this;
    c.acts.textContent = "";
    return (list || []).map(function (a) {
      if (a && a.nodeType === 1) {
        c.acts.appendChild(a);
        return a;
      }
      var b = ibtn(a.cls || "", a.icon, a.title);
      b.addEventListener("click", function () { a.run(c); });
      c.acts.appendChild(b);
      return b;
    });
  };
  Card.prototype.save = function () { persist(); };
  Card.prototype.sized = function () { if (this.k.sized) this.k.sized(this); };
  Card.prototype.fold = function (on) {
    foldUnit(this, on);
  };
  // pin docks the card on a new page, shown, or undocks it (its page goes
  // with its last pane). A kind with unpinCloses (panel mirror cards) closes
  // on unpin instead of returning to the Main Board flow.
  Card.prototype.pin = function (on) {
    if (!on && this.k.unpinCloses) { this.close(); return; }
    galSet(on ? G.dock(gal, refOf(this)) : G.undock(gal, refOf(this)), this);
  };
  // bump counts as an open: the card moves to the head of the unpinned
  // flow. A pinned card stays where it is.
  Card.prototype.bump = function (origin) {
    if (this.pinned) return;
    openBefore(this, origin);
    arrange(this);
    persist();
  };
  // show brings the card into view and marks it with a short flash.
  Card.prototype.show = function () {
    // A file preview (or a row-menu terminal) must not open behind Files.
    if (maxed && maxed.files) maximize(null);
    var el = this.el;
    if (opt && opt.reveal) opt.reveal();
    activate(el);
    var calm = !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
    // An unpinned card is a Main Board card: showing it selects it (anchor).
    if (window.WsBoardAnchor && window.WsBoardAnchor.select(el, { smooth: true })) { /* aligned */ }
    else if (el.scrollIntoView) el.scrollIntoView({ block: "nearest", behavior: calm ? "auto" : "smooth" });
    el.classList.remove("flash");
    void el.offsetWidth;
    el.classList.add("flash");
  };
  Card.prototype.close = function () {
    var i = cards.indexOf(this);
    if (i < 0) return;
    if (maxed === this) maximize(null);
    cards.splice(i, 1);
    if (this.k.close) this.k.close(this);
    if (this.el.parentNode) this.el.parentNode.removeChild(this.el);
    if (this.pinned) { gal = G.undock(gal, refOf(this)); sync(); arrange(null); }
    else { countTopics(); if (window.WsBoardAnchor && window.WsBoardAnchor.settle) window.WsBoardAnchor.settle(); }
    persist();
  };
  // The handle under the body drags its height; the pointer is captured, so
  // the drag keeps going over a frame in the body. Double click (or Home)
  // goes back to the base height.
  Card.prototype.wireGrip = function () {
    var c = this, from = null;
    function set(h) {
      var next = Math.max(MIN, Math.round(h));
      if (c.h === next && c.body.style.height === next + "px") return;
      c.h = next;
      c.body.style.height = c.h + "px";
    }
    function end() {
      if (from === null) return;
      from = null;
      c.el.classList.remove("drag");
      persist();
      c.sized();
    }
    c.grip.addEventListener("pointerdown", function (e) {
      if (e.button !== undefined && e.button !== 0) return;
      from = { y: e.clientY, h: c.body.getBoundingClientRect().height };
      c.el.classList.add("drag");
      try { c.grip.setPointerCapture(e.pointerId); } catch (x) { /* no capture: the move still lands */ }
      e.preventDefault();
    });
    c.grip.addEventListener("pointermove", function (e) { if (from) set(from.h + e.clientY - from.y); });
    c.grip.addEventListener("pointerup", end);
    c.grip.addEventListener("pointercancel", end);
    function reset() { c.h = 0; c.paint(); persist(); c.sized(); }
    c.grip.addEventListener("dblclick", reset);
    c.grip.addEventListener("keydown", function (e) {
      var d = e.key === "ArrowUp" ? -STEP : e.key === "ArrowDown" ? STEP : 0;
      if (e.key === "Home") { e.preventDefault(); reset(); return; }
      if (!d) return;
      e.preventDefault();
      set(c.body.getBoundingClientRect().height + d);
      persist();
      c.sized();
    });
  };

  // --- the maximize sheet -------------------------------------------------------
  // A maximized card is the same node, drawn fixed over both columns (the
  // stylesheet's [data-max] rules) with a scrim behind it; its place in the
  // board keeps its height meanwhile. Nothing is moved, so a frame or a
  // video keeps running through maximize and back. One thing is large at a
  // time: a card, or a board node (sheet: a topic, or a topic's panel).
  // Category identity belongs to the owning renderer, never to incidental
  // nested media. Capture column geometry before any node becomes fixed.
  // The toolbar explorer floats over the board: it spans the main (board)
  // column, or the conversation column where the board is hidden.
  function filesColumn() {
    return ["bcol", "convo"].map(function (id) {
      var box = doc.getElementById(id);
      return box ? box.getBoundingClientRect() : null;
    }).filter(function (r) { return r && r.width > 0 && isFinite(r.width); })[0] || null;
  }
  function sheetWidth(c) {
    var kind = c.kind, host, n = c.el, w;
    if (c.files) return (filesColumn() || { width: 1280 }).width;
    if (kind === "terminal") {
      host = n;
      while (host && host !== board && host !== galEl) host = host.parentNode;
      if (!host) host = board;
      if (host === galEl) host = viewEl;
      w = host && host.getBoundingClientRect().width;
      return w > 0 && isFinite(w) ? w : 960;
    }
    if (!(c instanceof Card)) return 1440; // a topic or its entry panel
    if (kind === "preview") kind = n.getAttribute("data-preview-kind");
    var widths = { text: 1280, markdown: 1280, code: 1440, diff: 1760,
      web: 1600, frame: 1600, image: 1600, video: 1600, audio: 560, topic: 1440,
      panel: 1440, approval: 1024, system: 1024 };
    return Object.prototype.hasOwnProperty.call(widths, kind) ? widths[kind] : 1920;
  }
  function clearSheetWidth(c) {
    if (c.widthBtn && c.widthBtn.parentNode) c.widthBtn.parentNode.removeChild(c.widthBtn);
    if (c.el) {
      c.el.removeAttribute("data-has-sheet-width");
      ["--sheet-inset", "--sheet-left", "--sheet-right"].forEach(function (k) { c.el.style.removeProperty(k); });
    }
    delete c.widthBtn;
    delete c.normalWidth;
    delete c.extended;
  }
  function sizeSheet(c) {
    if (!c || !c.el) return;
    // Preview responses may arrive after maximize; their renderer identity
    // can change. Files follows its column across resizes (that column is
    // never the fixed node itself); Terminal keeps the original snapshot.
    if (c.kind === "preview" || c.files) c.normalWidth = sheetWidth(c);
    var normal = Math.min(c.normalWidth, 1920), wide = window.innerWidth > 640 && window.innerWidth - 32 > normal;
    c.el.style.setProperty("--sheet-inset", (c.extended ? 16 : Math.max(16, (window.innerWidth - normal) / 2)) + "px");
    var span = c.files && !c.extended && filesColumn();
    if (span) {
      // Sit over the column itself rather than centered in the viewport.
      c.el.style.setProperty("--sheet-left", Math.max(16, span.left) + "px");
      c.el.style.setProperty("--sheet-right", Math.max(16, window.innerWidth - span.right) + "px");
    } else {
      c.el.style.removeProperty("--sheet-left");
      c.el.style.removeProperty("--sheet-right");
    }
    if (!c.widthBtn) {
      c.widthBtn = ibtn("sheet-width", "maximize-2", "");
      c.widthBtn.addEventListener("click", function (e) {
        e.preventDefault();
        c.extended = !c.extended;
        sizeSheet(c);
        if (c instanceof Card) c.sized();
      });
    }
    // Reattach after a renderer replaces only the head, or the whole node.
    var before = c.files ? c.back : c instanceof Card ? c.minBtn : sheetBtn(c.el, "d-min");
    if (before && (c.widthBtn.nextSibling !== before || c.widthBtn.parentNode !== before.parentNode)) {
      var focused = doc.activeElement === c.widthBtn;
      before.parentNode.insertBefore(c.widthBtn, before);
      if (focused) c.widthBtn.focus({ preventScroll: true });
    }
    c.widthBtn.hidden = !wide || c.el.hasAttribute("data-float");
    if (c.widthBtn.hidden) c.el.removeAttribute("data-has-sheet-width");
    else c.el.setAttribute("data-has-sheet-width", "");
    c.widthBtn.setAttribute("aria-pressed", String(!!c.extended));
    var label = c.extended ? wsT("js.card.width.normal") : wsT("js.card.width.extend");
    c.widthBtn.title = label;
    c.widthBtn.setAttribute("aria-label", label);
  }
  window.addEventListener("resize", function () { sizeSheet(maxed); });
  function maximize(c, floating) {
    if (c && window.WsOverview && window.WsOverview.holding()) { window.WsOverview.after(function () { maximize(c, floating); }); return; }
    var floatOn = !!(floating && c instanceof Card && c.kind === "explorer");
    if (maxed === c) {
      if (c && c.el.hasAttribute("data-float") !== floatOn) {
        if (floatOn) c.el.setAttribute("data-float", ""); else c.el.removeAttribute("data-float");
        c.normalWidth = floatOn ? 1100 : sheetWidth(c);
        c.extended = false;
        sizeSheet(c); c.sized();
      }
      return;
    }
    if (maxed) {
      var was = maxed;
      maxed = null;
      doc.documentElement.removeAttribute("data-board-max");
      scrim.hidden = true;
      clearSheetWidth(was);
      if (was.files) {
        was.el.removeAttribute("data-max");
        doc.body.removeAttribute("data-fpane-max");
        doc.body.removeAttribute("data-files-docked");
        was.el.style.removeProperty("--files-top");
        toolbar.removeAttribute("data-files");
        was.back.setAttribute("aria-expanded", "false");
        was.el.hidden = true;
        was.owner.hidden();
        if (was.back.isConnected) was.back.focus({ preventScroll: true });
      } else if (was instanceof Card) {
        var cardFreeOffset = was._freeOffset;
        delete was._freeOffset;
        was.el.removeAttribute("data-max");
        if (was.kind === "explorer") doc.body.removeAttribute("data-explorer-max");
        was.el.removeAttribute("data-float");
        was.el.style.height = "";
        was.sized();
        restoreFreeNode(was.el, cardFreeOffset);
        if (was.maxBtn.isConnected) was.maxBtn.focus({ preventScroll: true });
      } else {
        var origin = was.topic ? (was.el && was.el.isConnected ? was.el : null) : (was.panelHosts && was.panelHosts[0]);
        var back = shrink(was);
        restoreFreeNode(origin, was.freeOffset);
        if (back) back.focus({ preventScroll: true });
      }
    }
    if (!c) return;
    c.normalWidth = floatOn ? 1100 : sheetWidth(c);
    c.extended = false;
    if (!scrim) {
      scrim = mk("div", "bc-scrim");
      scrim.addEventListener("click", function () { maximize(null); });
      doc.body.appendChild(scrim);
    }
    scrim.hidden = false;
    maxed = c;
    syncBoardMaxAttr(c.el);
    if (c.files) {
      // The page owns this explorer independently of the conversation tab.
      c.el.hidden = false;
      c.el.style.setProperty("--files-top", Math.max(52, toolbar.firstChild.getBoundingClientRect().bottom + 8) + "px");
      c.el.setAttribute("data-max", "");
      doc.body.setAttribute("data-fpane-max", "");
      toolbar.setAttribute("data-files", "");
      c.back.setAttribute("aria-expanded", "true");
      c.owner.shown();
      sizeSheet(c);
      var filter = c.el.querySelector(".fq");
      if (filter) filter.focus({ preventScroll: true });
      return;
    }
    if (!(c instanceof Card)) {
      grow(c);
      if (!c.el) { maximize(null); return; }
      var m = sheetBtn(c.el, "d-min");
      if (m) m.focus({ preventScroll: true });
      return;
    }
    c._freeOffset = captureFreeOffset(c.el);
    suspendFreeNode(c.el);
    c.el.style.height = c.el.getBoundingClientRect().height + "px";
    c.el.setAttribute("data-max", "");
    if (c.kind === "explorer") doc.body.setAttribute("data-explorer-max", "");
    if (floatOn) c.el.setAttribute("data-float", "");
    sizeSheet(c);
    c.sized();
    c.minBtn.focus();
  }
  // A board node drawn large is the whiteboard's own (cards.js never
  // builds it), and a board update may bring it anew, so its sheet names
  // it: a topic by its name, a panel by its data-key. The node stays where
  // it stands, drawn fixed by the stylesheet's [data-max] rules: a panel's
  // entry keeps its height meanwhile, as a card does, and a topic, the
  // board's own child with nothing around it to hold its place, gives the
  // scroll offsets it changed back on its way out.
  function sheet(btn) {
    var p = btn.parentNode;
    if (p.classList.contains("tbar")) {
      var sec = p.closest("section.topic");
      return topicName(sec) !== null ? { topic: true, key: sec.getAttribute("data-topic"), el: null } : null;
    }
    var d = p.tagName === "SUMMARY" ? p.parentNode : null;
    return d && d.matches("details.entry-panel[data-key]") ? { topic: false, key: d.getAttribute("data-key"), el: null } : null;
  }
  function sheetNode(t) {
    var sel = t.topic ? "section.topic[data-topic]" : "details.entry-panel[data-key]", attr = t.topic ? "data-topic" : "data-key", n = null;
    [board, galEl].some(function (box) {
      n = box ? Array.prototype.filter.call(box.querySelectorAll(sel), function (x) { return x.getAttribute(attr) === t.key; })[0] || null : null;
      return !!n;
    });
    return n;
  }
  function sheetBtn(n, cls) {
    var row = n.tagName === "DETAILS" ? n.firstElementChild : child(child(n, "tophead") || n, "tbar");
    return row ? child(row, cls) : null;
  }
  function syncBoardMaxAttr(el) {
    doc.documentElement.setAttribute("data-board-max", galEl && el && galEl.contains(el) ? "gal" : "");
  }
  function grow(t) {
    var n = sheetNode(t);
    if (t.el && t.el !== n) shrinkNode(t);
    t.el = n;
    if (!n) return;
    syncBoardMaxAttr(n);
    sizeSheet(t);
    if (n.hasAttribute("data-max")) return;
    if (t.topic) {
      if (t.freeOffset === undefined) t.freeOffset = captureFreeOffset(n);
      suspendFreeNode(n);
      if (window.WsBoardAnchor && window.WsBoardAnchor.hydrate) window.WsBoardAnchor.hydrate(n, { immediate: true });
    }
    if (t.topic) {
      if (!t.scroll) {
        t.scroll = [];
        for (var x = n.parentNode; x && x.nodeType === 1; x = x.parentNode) if (x.scrollHeight > x.clientHeight) t.scroll.push([x, x.scrollTop]);
      }
    } else {
      n.open = true;
      n.parentNode.style.height = n.parentNode.getBoundingClientRect().height + "px";
      var sec = n.closest && n.closest("section.topic");
      var hosts = [];
      if (sec) {
        if (t.freeOffset === undefined) t.freeOffset = captureFreeOffset(sec);
        hosts.push(sec);
        for (var p = n.parentNode; p && p !== sec; p = p.parentNode) {
          if (p.parentNode === sec && !p.classList.contains("tophead")) { hosts.push(p); break; }
        }
        hosts.forEach(function (h) { h.setAttribute("data-has-panel-max", ""); });
      }
      t.panelHosts = hosts;
    }
    n.setAttribute("data-max", "");
  }
  function shrinkNode(t) {
    t.el.style.removeProperty("--sheet-inset");
    t.el.removeAttribute("data-max");
    if (t.panelHosts) {
      t.panelHosts.forEach(function (h) { if (h && h.removeAttribute) h.removeAttribute("data-has-panel-max"); });
      t.panelHosts = null;
    }
    if (!t.topic && t.el.parentNode) t.el.parentNode.style.height = "";
  }
  // shrink draws t's node in its place again and returns its maximize
  // button, the focus's way back.
  function shrink(t) {
    var n = t.el && t.el.isConnected ? t.el : null;
    if (t.el) shrinkNode(t);
    var skipFreeCol = t.topic && typeof t.freeOffset === "number" && isFinite(t.freeOffset)
      && window.WsBoardAnchor && window.WsBoardAnchor.free && window.WsBoardAnchor.free()
      ? doc.getElementById("bcol") : null;
    (t.scroll || []).forEach(function (s) { if (s[0] !== skipFreeCol) s[0].scrollTop = s[1]; });
    return n ? sheetBtn(n, "d-max") : null;
  }
  // follow draws a large board node large again after a board update: on
  // its new node, or, with the node gone, nothing is large any more. A
  // focus the update took (the old head's back button) goes to the new one.
  function follow(t) {
    if (!sheetNode(t)) { maximize(null); return; }
    grow(t);
    var a = doc.activeElement, m = sheetBtn(t.el, "d-min");
    if (m && (!a || a === doc.body)) m.focus({ preventScroll: true });
  }
  function sheetClick(e) {
    var b = e.target.closest && e.target.closest(".d-max, .d-min");
    if (b && (b.parentNode.classList.contains("tbar") || b.parentNode.tagName === "SUMMARY")) {
      // A button in a panel's head must not fold or unfold the panel.
      e.preventDefault();
      if (b.classList.contains("d-min")) { maximize(null); return; }
      var t = sheet(b);
      if (t) maximize(t);
      return;
    }
    // A large panel stays open: its head does not fold it.
    var s = e.target.closest && e.target.closest("summary");
    if (s && s.parentNode.matches("details.entry-panel[data-max]") && !e.target.closest("a, button")) e.preventDefault();
  }
  doc.addEventListener("keydown", function (e) {
    if (e.key !== "Escape" || !maxed || e.defaultPrevented) return;
    e.preventDefault();
    maximize(null);
  });

  // --- dropping a moved card or topic -----------------------------------------
  // eff is a flow node's place in time: a card's open time, a hand-moved
  // topic's time, another open topic's bar time; null for anything else.
  function eff(n) {
    var u = unitOf(n);
    return u && !u.loose && !u.pinned && (u.at || isCard(n)) ? u.at : topicAt(n);
  }
  // drop places unit u: to.pin docks it on a new page (shown); otherwise
  // it undocks and stands in the board's open flow between to.after and
  // to.before (either null at an end), or, a closed topic, back in the
  // closed section.
  function drop(u, to) {
    var topic = !isCard(u.el), g;
    delete u.rank;
    if (topic && u.loose) { var el = u.el; u = topicRec(u.name); u.el = el; }
    if (to.pin) {
      g = G.dock(gal, refOf(u));
      if (topic) u.at = 0;
    } else {
      if (!topic && u.k && u.k.unpinCloses) { u.close(); return; }
      g = G.undock(gal, refOf(u));
      u.pinned = 0;
      if (topic && topicAt(u.el) === null) u.at = 0;
      else {
        var ta = to.after ? eff(to.after) : null, tb = to.before ? eff(to.before) : null;
        u.at = ta === null ? Math.max(headTime(), tb === null ? 0 : tb + 1) : tb === null ? ta - 1 : ta > tb ? (ta + tb) / 2 : ta;
        if (topic) u.bar = topicAt(u.el);
      }
    }
    galSet(g, u);
  }
  // galDrop docks u where a drag let it go on the gallery: to.gal "dock" on
  // a new page, "append" below what the page at to.pos holds, "place" at
  // to.zone of the docked pane to.target (an edge splits it, the center
  // swaps the two), "home" (the narrow view's home dot) back to the board
  // as its unpin button does, the Main Board shown.
  function galDrop(u, to) {
    if (!isCard(u.el) && u.loose) { var el = u.el; u = topicRec(u.name); u.el = el; }
    var ref = refOf(u);
    delete u.rank;
    if (!isCard(u.el)) u.at = 0;
    if (to.gal === "home") {
      if (isCard(u.el) && u.k && u.k.unpinCloses) { u.close(); show(-1, 0); return; }
      galSet(G.activate(G.undock(gal, ref), -1, true), u);
    } else if (to.gal === "unpin") {
      if (isCard(u.el) && u.k && u.k.unpinCloses) { u.close(); return; }
      galSet(G.undock(gal, ref), u);
    } else if (to.gal === "place") galSet(G.place(gal, ref, to.target, to.zone), u);
    else galSet(to.gal === "append" ? G.append(gal, ref, to.pos) : G.dock(gal, ref), u);
  }

  // --- dragging a header ----------------------------------------------------------
  // A topic's title bar or a card's header drags the topic or card; a
  // button, link, or field in it does not. While a drag runs the page holds
  // its board updates (opt.drag), the root carries data-bdrag, and the
  // gallery's rail shows even with no docked page, so a first unit can be
  // dropped on +.
  var dg = null, dragged = false, raf = 0;
  function rectOf(el) { return el && el.getClientRects().length ? el.getBoundingClientRect() : null; }
  function inside(r, x, y) { return !!r && x >= r.left && x < r.right && y >= r.top && y < r.bottom; }
  function scroller(el) {
    for (var n = el; n && n !== doc.body; n = n.parentNode) if (n.nodeType === 1 && /auto|scroll/.test(getComputedStyle(n).overflowY)) return n;
    return null;
  }
  // pageAt is the page the gallery shows under (x, y), {pos, el}: none on
  // the narrow view's Page 0, where the board shows through.
  function pageAt(x, y) {
    if (!galEl || galEl.hidden || !inside(rectOf(viewEl), x, y)) return null;
    var pos = curPos();
    if (pos < 0) return null;
    var el = pageElAt(pos);
    return el ? { pos: pos, el: el } : null;
  }
  function inRail(x, y) { return !!galEl && !galEl.hidden && !railEl.hidden && inside(rectOf(railEl), x, y); }
  // boardShown tells whether the Main Board takes drops: always in the wide
  // view, on Page 0 in the narrow one.
  function boardShown() { return !(lay && lay.narrow) || curPos() < 0; }
  // paneZone reads a drop on the docked page shown (page el) at (x, y):
  // the pane under the pointer and its zone (WsGallery.zone: the nearest
  // edge splits it; the center swaps the two, offered only to a docked
  // pane, so a board item never unpins the pane it lands on), or null (a
  // divider, the dragged pane itself).
  function paneZone(el, x, y, d) {
    var p = Array.prototype.filter.call(el.querySelectorAll(".gal-pane[data-ref]"), function (n) { return inside(rectOf(n), x, y); })[0];
    var ref = p && p.getAttribute("data-ref");
    if (!p || ref === refOf(d.unit)) return null;
    return { gal: "place", target: ref, zone: G.zone(rectOf(p), x, y, !!d.unit.pinned), pane: p };
  }
  // drawZone shows the preview of a zone drop over its pane (null hides
  // it): the half on the zone's side, the whole pane for the center. The
  // pane carries data-dz meanwhile.
  var zoneEl = null, zonePane = null;
  function drawZone(to) {
    var p = to && to.pane;
    if (zonePane && zonePane !== p) zonePane.removeAttribute("data-dz");
    zonePane = p || null;
    if (!p) {
      if (zoneEl) { zoneEl.hidden = true; zoneEl.removeAttribute("data-dz"); }
      return;
    }
    p.setAttribute("data-dz", to.zone);
    if (!zoneEl) {
      zoneEl = mk("div", "gal-overlay");
      zoneEl.setAttribute("aria-hidden", "true");
    }
    if (zoneEl.parentNode !== viewEl) viewEl.appendChild(zoneEl);
    var r = rectOf(p), v = rectOf(viewEl), z = to.zone, I = 3;
    var w = z === "left" || z === "right" ? r.width / 2 : r.width, h = z === "top" || z === "bottom" ? r.height / 2 : r.height;
    zoneEl.setAttribute("data-dz", z);
    zoneEl.style.left = (r.left - v.left + (z === "right" ? r.width - w : 0) + I) + "px";
    zoneEl.style.top = (r.top - v.top + (z === "bottom" ? r.height - h : 0) + I) + "px";
    zoneEl.style.width = Math.max(0, w - 2 * I) + "px";
    zoneEl.style.height = Math.max(0, h - 2 * I) + "px";
    zoneEl.hidden = false;
  }
  // over reads the drop at the pointer: a rail dot (the narrow view's home
  // dot undocks, a page's dot appends to that page, + docks on a new page),
  // the page the gallery shows (+ docks on a new page; on a docked page a
  // pane's zone, paneZone), or the board's open flow. A drop that leaves
  // the item where it stands lights nothing.
  function over(x, y) {
    var d = dg;
    d.x = x; d.y = y;
    var to = null, scr = null, lightEl = null, hit = dotAt(x, y) || pageAt(x, y);
    if (hit && hit.el !== plusEl && hit.el.classList.contains("gal-page")) {
      if (hit.el.classList.contains("gal-sys-page")) { to = { gal: "dock" }; lightEl = hit.el; }
      else to = paneZone(hit.el, x, y, d);
    } else if (hit) {
      var cur = G.pageOf(gal, refOf(d.unit));
      if (hit.el && hit.el.classList.contains("sys")) to = null;
      else if (hit.pos < 0) to = d.unit.pinned ? { gal: "home" } : null;
      else if (hit.pos === gal.pages.length) to = { gal: "dock" };
      else if (hit.pos >= 0 && hit.pos < gal.pages.length) to = hit.pos === cur ? null : { gal: "append", pos: hit.pos };
      if (to) lightEl = hit.el;
    } else if (inRail(x, y) || inside(rectOf(toolbar), x, y)) { /* the rail and the toolbar are never drop targets */ }
    else if (boardShown() && inside(rectOf(scroller(board) || board), x, y)) {
      scr = scroller(board);
      // Flow reordering inside the snap deck is retired; dragging back over
      // #bcol only undocks a docked unit or cancels cleanly.
      to = d.unit.pinned ? { gal: "unpin" } : null;
    }
    d.to = to;
    var hitDot = hit && hit.el && hit.el.classList.contains("gal-dot") ? hit : null;
    spring(hitDot);
    if (window.WsGalPreview && window.WsGalPreview.dragOver) window.WsGalPreview.dragOver(hitDot ? hitDot.el : null, to);
    markDrop(lightEl);
    drawZone(to && to.gal === "place" ? to : null);
    d.scr = scr;
    var sr = rectOf(scr);
    d.edge = sr ? (y < sr.top + EDGE ? -1 : y > sr.bottom - EDGE ? 1 : 0) : 0;
    if (d.edge && !raf) raf = requestAnimationFrame(autoScroll);
  }
  function autoScroll() {
    raf = 0;
    var d = dg;
    if (!d || !d.on || !d.edge || !d.scr) return;
    var was = d.scr.scrollTop;
    d.scr.scrollTop += d.edge * 8;
    if (d.scr.scrollTop !== was) over(d.x, d.y);
  }
  function dragStart(e) {
    if (overviewOn || dg || rg || !e.isPrimary || (e.button !== undefined && e.button !== 0)) return;
    var t = e.target;
    if (!t.closest || t.closest("button, a, input, textarea, select, summary, label")) return;
    var head = t.closest(".tbar, .bc-hd");
    if (!head) return;
    var el = head.classList.contains("bc-hd") ? head.closest(".bcard") : head.closest("section.topic");
    if (!el || el.hasAttribute("data-max") || !(isCard(el) || topicName(el) !== null)) return;
    var u = unitOf(el);
    if (!u) return;
    dg = { el: el, head: head, unit: u, id: e.pointerId, x0: e.clientX, y0: e.clientY, x: e.clientX, y: e.clientY, on: false, touch: e.pointerType === "touch", timer: 0, to: null, edge: 0, scr: null };
    if (dg.touch) dg.timer = setTimeout(function () { if (dg && !dg.on) dragBegin(); }, HOLD);
  }
  function dragBegin() {
    var d = dg;
    if (!d.el.isConnected) { dg = null; return; }
    d.on = true;
    try { d.head.setPointerCapture(d.id); } catch (x) { /* no capture: the moves still land on the document */ }
    d.el.classList.add("bdrag");
    doc.documentElement.setAttribute("data-bdrag", "");
    if (opt.drag) opt.drag(true);
    relayout();
    var sel = window.getSelection && window.getSelection();
    if (sel && sel.removeAllRanges) sel.removeAllRanges();
    over(d.x, d.y);
  }
  function dragMove(e) {
    var d = dg;
    if (!d || e.pointerId !== d.id) return;
    // A mouse move with the button up is a release this page never heard.
    if (e.pointerType === "mouse" && e.buttons !== undefined && (e.buttons & 1) === 0) { dragEnd(null); return; }
    if (!d.on) {
      if (Math.abs(e.clientX - d.x0) <= SLOP && Math.abs(e.clientY - d.y0) <= SLOP) return;
      // A touch that moves before its hold is a scroll.
      if (d.touch) { dragEnd(null); return; }
      d.x = e.clientX; d.y = e.clientY;
      dragBegin();
      if (!dg) return;
    }
    e.preventDefault();
    over(e.clientX, e.clientY);
  }
  // dragEnd ends the press or drag; only a release drops (a cancel leaves
  // everything where it stood). A drag's click is swallowed.
  function dragEnd(e) {
    var d = dg;
    if (!d || (e && e.pointerId !== d.id)) return;
    dg = null;
    clearTimeout(d.timer);
    if (!d.on) return;
    d.el.classList.remove("bdrag");
    doc.documentElement.removeAttribute("data-bdrag");
    markDrop(null);
    drawZone(null);
    unspring();
    if (window.WsGalPreview && window.WsGalPreview.dragEnd) window.WsGalPreview.dragEnd();
    dragged = true;
    setTimeout(function () { dragged = false; }, 0);
    var to = d.to;
    if (e && e.type === "pointerup" && to && d.el.isConnected) {
      if (to.gal) galDrop(d.unit, to);
      else if (d.unit.pinned) { to.el = d.el; drop(d.unit, to); }
    }
    relayout();
    if (opt.drag) opt.drag(false);
  }

  // --- the gallery column's width -------------------------------------------------
  // The gallery column's stored width (--bgal-w, global ws.dashboard.bgal.w,
  // 350-720px, default 540px) is dragged by .bp-edge on its left edge using
  // RAF-throttled custom property writes. The applied width yields below 350px
  // only when the remaining viewport past rail, conversation floor, board floor
  // and gaps is smaller than 350px; the board column takes the flexible
  // remainder.
  function clampBgalWidth(w) {
    var n = Math.round(+w);
    if (!isFinite(n)) return BGAL_W_DEF;
    return Math.max(BGAL_W_MIN, Math.min(BGAL_W_MAX, n));
  }
  function loadBgalWidth() {
    var rawW = opt && opt.load ? opt.load(BGAL_W_KEY) : null;
    if (rawW === null || rawW === "") return BGAL_W_DEF;
    var n = Number(rawW);
    return isFinite(n) && n > 0 ? clampBgalWidth(n) : BGAL_W_DEF;
  }
  function saveBgalWidth() {
    if (opt && opt.save) opt.save(BGAL_W_KEY, String(bgalW));
  }
  function readRailReserve() {
    var r = doc.documentElement;
    if (!r) return 288;
    if (r.getAttribute && r.getAttribute("data-rail-collapsed") === "true") return 0;
    var rawW = r.style && r.style.getPropertyValue ? r.style.getPropertyValue("--rail-w") : "";
    if (!rawW && typeof getComputedStyle === "function") {
      try { rawW = getComputedStyle(r).getPropertyValue("--rail-w"); } catch (e) { rawW = ""; }
    }
    var w = parseFloat(rawW);
    return (isFinite(w) && w > 0 ? w : 280) + 8;
  }
  function effectiveBgalWidth(vw) {
    var w = bgalW;
    var width = vw || (opt && opt.mode ? (+opt.mode().width || 0) : 0) || (typeof window !== "undefined" ? (+window.innerWidth || 0) : 0);
    if (!(width >= G.C.WIDE)) return w;
    var split = !!(opt && (opt.split || (board && board.closest && board.closest(".dgrid.live, .dgrid.history"))));
    var worktree = !!(board && board.closest && board.closest(".dgrid.worktree"));
    var stageFloor = (split || worktree) ? (CONVW_MIN + 8 + SPLITB_MIN) : BOARDW_MIN;
    var avail = Math.round(width - readRailReserve() - 32 - 8 - stageFloor);
    if (avail >= BGAL_W_MIN) return Math.min(w, avail);
    return Math.max(PINW_MIN, Math.min(w, Math.max(0, avail)));
  }
  function applyBgalWidth(vw) {
    var r = doc.documentElement;
    if (!r || !r.style) return;
    var eff = effectiveBgalWidth(vw);
    var v = bgalW + "px", ev = eff + "px";
    if (!r.style.getPropertyValue || r.style.getPropertyValue("--bgal-w") !== v) r.style.setProperty("--bgal-w", v);
    if (!r.style.getPropertyValue || r.style.getPropertyValue("--bgal-eff-w") !== ev) r.style.setProperty("--bgal-eff-w", ev);
  }
  function queueBgalWidth() {
    if (typeof requestAnimationFrame !== "function") { applyBgalWidth(); return; }
    if (bgalRaf) return;
    bgalRaf = requestAnimationFrame(function () {
      bgalRaf = 0;
      applyBgalWidth();
      notifyGalChange("resize");
    });
  }
  function setBgalWidth(w, immediate) {
    bgalW = clampBgalWidth(w);
    if (immediate) {
      if (bgalRaf && typeof cancelAnimationFrame === "function") { cancelAnimationFrame(bgalRaf); bgalRaf = 0; }
      applyBgalWidth();
      saveBgalWidth();
      notifyGalChange("resize");
    } else {
      queueBgalWidth();
    }
  }
  // colShown tells whether the wide view shows the gallery as a column.
  function colShown() { return !!(lay && lay.column); }
  // freezeConvo holds the conversation column at its current track width
  // before a .bp-edge drag so resizing #bgal trades space with #bcol without
  // shifting #convo; dashboard-layout.css still clamps --convow to keep the
  // board's 360px floor.
  function freezeConvo() {
    var t = colShown() && tracks();
    if (t && !convow) setConvo(t[0]);
  }
  function edgeHandle() {
    grab("bp-edge", galEl, function () {
      freezeConvo();
      var w = galEl.getBoundingClientRect().width || bgalW;
      return w ? { w: w } : null;
    }, function (from, dx) {
      setBgalWidth(from.w - dx, false);
    }, function () {
      setBgalWidth(BGAL_W_DEF, true);
    }, function () {
      if (bgalRaf && typeof cancelAnimationFrame === "function") { cancelAnimationFrame(bgalRaf); bgalRaf = 0; }
      applyBgalWidth();
      saveBgalWidth();
      notifyGalChange("resize");
    }).setAttribute("data-edge", "left");
  }
  // The split handle stands in the gap between the conversation and the
  // board of the page's grid (opt.split; the stylesheet shows it where the
  // two stand side by side) and sizes the conversation's column (--convow,
  // with data-convow, on the root, kept in the record; double click goes
  // back to the stylesheet's split): at least CONVW_MIN, and at most what
  // leaves the board SPLITB_MIN.
  function setConvo(w) {
    var r = doc.documentElement;
    convow = w ? Math.max(CONVW_MIN, Math.round(w)) : 0;
    if (convow) { r.style.setProperty("--convow", convow + "px"); r.setAttribute("data-convow", ""); }
    else { r.style.removeProperty("--convow"); r.removeAttribute("data-convow"); }
  }
  // tracks is the split grid's two resolved columns in px (conversation,
  // board), or null where it shows one.
  function tracks() {
    if (!opt.split) return null;
    var t = (getComputedStyle(opt.split).gridTemplateColumns.match(/[\d.]+(?=px)/g) || []).map(Number);
    return t.length === 2 && t[0] > 0 ? t : null;
  }
  function splitHandle(grid) {
    grab("dcv-edge", grid, function () {
      var t = tracks();
      return t ? { w: t[0], max: t[0] + t[1] - SPLITB_MIN } : null;
    }, function (from, dx) {
      setConvo(Math.min(from.w + dx, Math.max(from.max, CONVW_MIN)));
    }, function () { setConvo(0); });
  }
  // grab makes a width handle (class cls) in host: a press of the main
  // button starts a drag where start() gives its state (null: nothing to
  // drag), each move hands move() that state and the distance from the
  // press, and the release saves; a double click resets and saves.
  function grab(cls, host, start, move, reset, done) {
    var edge = mk("div", cls), from = null, x0 = 0;
    edge.setAttribute("role", "separator");
    edge.setAttribute("aria-orientation", "vertical");
    edge.title = wsT("js.pins.width");
    edge.setAttribute("aria-label", wsT("js.pins.width"));
    host.appendChild(edge);
    edge.addEventListener("pointerdown", function (e) {
      if (e.button !== undefined && e.button !== 0) return;
      from = start();
      if (!from) return;
      x0 = e.clientX;
      edge.classList.add("drag");
      try { edge.setPointerCapture(e.pointerId); } catch (x) { /* no capture: the move still lands */ }
      e.preventDefault();
    });
    edge.addEventListener("pointermove", function (e) { if (from) move(from, e.clientX - x0); });
    function end() { if (!from) return; from = null; edge.classList.remove("drag"); if (done) done(); relayout(); persist(); }
    edge.addEventListener("pointerup", end);
    edge.addEventListener("pointercancel", end);
    edge.addEventListener("dblclick", function () { reset(); relayout(); persist(); });
    return edge;
  }
  // fetchMissing finds the docked topics the served board lacks (closed
  // past its cap) through the closed route, page by page from the board's
  // show-more control, until each is found or the route ends: one the
  // route ends without is gone from the store, and it leaves the gallery
  // (its page with it, when it was the page's last pane). A failed fetch
  // keeps the gallery for the next load, and so does a board with no
  // show-more control: an empty or not yet loaded board cannot tell a
  // topic past its cap from one that is gone, so only the route's answer
  // undocks a topic.
  function fetchMissing() {
    function missing() { return Object.keys(topics).filter(function (k) { return topics[k].pinned && !topics[k].el; }); }
    function gone(list) {
      list.forEach(function (k) { gal = G.undock(gal, "t:" + k); delete topics[k]; });
      sync();
      arrange(null);
      persist();
    }
    function next(box) {
      var b = box.querySelector("button.closed-more[data-closed-next]");
      return b ? +b.getAttribute("data-closed-next") : 0;
    }
    function page(from) {
      opt.closed(from, function (box) {
        if (!box) return;
        var got = [];
        Array.prototype.forEach.call(box.querySelectorAll("section.topic[data-topic]"), function (t) {
          var r = topics[t.getAttribute("data-topic")];
          if (r && r.pinned && !r.el) { r.el = t; got.push(t); }
        });
        if (got.length) {
          arrange(null);
          if (opt.adopted) got.forEach(opt.adopted);
        }
        var left = missing(), n = next(box);
        if (!left.length) return;
        if (n) page(n); else gone(left);
      });
    }
    var miss = missing();
    if (!miss.length) return;
    var from = next(board);
    if (from && opt.closed) page(from);
  }

  // --- the module -----------------------------------------------------------------
  function add(kind, rec) {
    var c = new Card(kind, rec);
    cards.push(c);
    c.k.open(c);
    c.paint();
    return c;
  }
  // --- title-bar click scroll-to-top ------------------------------------------
  var TITLE_PRESS_SLOP = 5;
  var titlePress = null;
  function reducedMotion() {
    return !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  }
  function titleInteractive(t) {
    return !!(t && t.closest && t.closest("button, a, input, textarea, select, summary, label, [role='button'], .pop"));
  }
  function titlePressStart(e) {
    if (!e || e.isPrimary === false || (e.button !== undefined && e.button !== 0)) return;
    var t = e.target;
    if (!t || !t.closest || titleInteractive(t) || !t.closest(".tbar, .bc-hd")) {
      titlePress = null;
      return;
    }
    titlePress = { id: e.pointerId, x: e.clientX || 0, y: e.clientY || 0, moved: false };
  }
  function titlePressMove(e) {
    var p = titlePress;
    if (!p || (p.id !== undefined && e.pointerId !== undefined && e.pointerId !== p.id)) return;
    if (Math.abs((e.clientX || 0) - p.x) > TITLE_PRESS_SLOP || Math.abs((e.clientY || 0) - p.y) > TITLE_PRESS_SLOP) p.moved = true;
  }
  function titlePressEnd(e) {
    var p = titlePress;
    if (!p) return;
    if (e && p.id !== undefined && e.pointerId !== undefined && e.pointerId !== p.id) return;
    if (e && e.type === "pointercancel") { titlePress = null; return; }
    setTimeout(function () { if (titlePress === p) titlePress = null; }, 0);
  }
  function scrollBoxTop(box, behavior) {
    if (!box) return false;
    if (box.scrollTo) box.scrollTo({ top: 0, behavior: behavior });
    else box.scrollTop = 0;
    return true;
  }
  function railInsetPx(bcol, sc, head) {
    var v = bcol && bcol.style && bcol.style.getPropertyValue ? parseFloat(bcol.style.getPropertyValue("--wb-rail-inset")) : NaN;
    if (!isNaN(v)) return v;
    if (bcol && typeof getComputedStyle === "function") {
      try { v = parseFloat(getComputedStyle(bcol).getPropertyValue("--wb-rail-inset")); } catch (e) {}
      if (!isNaN(v)) return v;
    }
    if (sc && sc.style && sc.style.scrollPaddingTop) {
      v = parseFloat(sc.style.scrollPaddingTop);
      if (!isNaN(v)) return v;
    }
    if (sc && typeof getComputedStyle === "function") {
      try { v = parseFloat(getComputedStyle(sc).scrollPaddingTop); } catch (e) {}
      if (!isNaN(v)) return v;
    }
    var stick = head && head.closest ? head.closest(".tophead") : null;
    var el = stick || head;
    if (el && el.style && el.style.getPropertyValue) {
      v = parseFloat(el.style.getPropertyValue("--wb-rail-top"));
      if (!isNaN(v)) return v;
    }
    return 0;
  }
  function railCapPx(cardEl) {
    if (!cardEl || !cardEl.hasAttribute || !(cardEl.hasAttribute("data-topic") || cardEl.hasAttribute("data-rail"))) return 0;
    var cap = cardEl.getAttribute("data-anchor-cap");
    if (cap === "0") return 0;
    if (cap === "rail0") return 24;
    if (cap === "1") return 44;
    var count = 0;
    if (board && board.querySelectorAll) {
      var list = board.querySelectorAll("section.topic[data-topic], .bcard[data-rail]");
      for (var i = 0; i < list.length; i++) {
        var n = list[i];
        if (n === cardEl) break;
        if (!n.hidden && !n.hasAttribute("data-state-filtered") && (!n.getBoundingClientRect || n.getBoundingClientRect().height)) {
          count++;
          if (count >= 2) break;
        }
      }
    }
    return count ? 4 + count * 20 : 0;
  }
  function titleClick(e) {
    var press = titlePress;
    titlePress = null;
    if (!e || e.defaultPrevented || (e.button !== undefined && e.button !== 0)) return false;
    if (e.ctrlKey || e.metaKey || e.shiftKey || e.altKey || e.detail > 1) return false;
    if (dragged || (dg && dg.on)) return false;
    if (press) {
      if (press.moved) return false;
      if (typeof e.clientX === "number" && typeof e.clientY === "number" && (e.clientX !== 0 || e.clientY !== 0) &&
          (Math.abs(e.clientX - press.x) > TITLE_PRESS_SLOP || Math.abs(e.clientY - press.y) > TITLE_PRESS_SLOP)) return false;
    }
    var t = e.target;
    if (!t || !t.closest || titleInteractive(t)) return false;
    var head = t.closest(".tbar, .bc-hd");
    if (!head) return false;
    var sel = window.getSelection && window.getSelection();
    if (sel && !sel.isCollapsed) return false;
    var isBcard = head.classList.contains("bc-hd");
    var cardEl = isBcard ? head.closest(".bcard") : head.closest("section.topic");
    if (!cardEl) return false;
    var calm = reducedMotion(), behavior = calm ? "auto" : "smooth";
    var inPreview = !!(cardEl.closest && cardEl.closest(".overview-preview-stage"));
    var isMax = !!(cardEl.hasAttribute && cardEl.hasAttribute("data-max"));
    if (isMax || inPreview) {
      return scrollBoxTop(isBcard ? (cardEl.querySelector && cardEl.querySelector(":scope > .bc-in > .bc-body")) : cardEl, behavior);
    }
    var inGal = !!((galEl && galEl.contains && galEl.contains(cardEl)) || (cardEl.closest && cardEl.closest("#bgal, .gal-pane")));
    if (inGal) {
      var target = cardEl.querySelector && cardEl.querySelector(isBcard ? ":scope > .bc-in > .bc-body" : ":scope > .gal-pbody");
      return scrollBoxTop(target, behavior);
    }
    if (overviewOn || (doc.documentElement && doc.documentElement.hasAttribute && doc.documentElement.hasAttribute("data-overview"))) return false;
    if (maxed || (doc.documentElement && doc.documentElement.hasAttribute && doc.documentElement.hasAttribute("data-board-max"))) return false;
    if (board && board.querySelector && board.querySelector("[data-max]")) return false;
    var bcol = doc.getElementById("bcol") || (board && board.closest && board.closest("#bcol")) || board;
    if (bcol && bcol.hasAttribute && bcol.hasAttribute("data-board-free") &&
        window.WsBoardAnchor && typeof window.WsBoardAnchor.select === "function") {
      window.WsBoardAnchor.select(cardEl, { smooth: !calm, alignTop: true });
      return true;
    }
    var sc = scroller(board) || doc.scrollingElement || doc.documentElement;
    if (!sc) return false;
    var top;
    if (cardEl.getAttribute && cardEl.getAttribute("data-anchor-cap") === "0") {
      top = 0;
    } else {
      var main = sc === doc.scrollingElement || sc === doc.documentElement;
      var scRectTop = main ? 0 : (sc.getBoundingClientRect ? sc.getBoundingClientRect().top + (sc.clientTop || 0) : 0);
      var padTop = main ? 0 : (typeof getComputedStyle === "function" ? (parseFloat(getComputedStyle(sc).paddingTop) || 0) : 0);
      var cardTop = cardEl.getBoundingClientRect().top;
      var clearTop = scRectTop + padTop + railInsetPx(bcol, sc, head) + railCapPx(cardEl);
      top = Math.max(0, Math.round(sc.scrollTop + (cardTop - clearTop)));
    }
    if (sc.scrollTo) sc.scrollTo({ top: top, behavior: behavior });
    else sc.scrollTop = top;
    return true;
  }

  window.WsCards = {
    icon: licon,
    register: function (name, kind) { kinds[name] = kind; },
    // mount starts the framework on the board region el and restores the
    // stored cards, topics and gallery. o: key (the storage key), load and
    // save (the page's guarded storage), reveal (shows the board column
    // where the layout shows one column at a time), and for the gallery:
    // host (the element the gallery's element joins; the body without
    // one), mode() (what the page shows: {width, tabs}, its width in px
    // and whether the layout menu shows one column at a time), layout(s)
    // (the gallery's shape changed: WsGallery.layout's narrow, column,
    // overlay and rail, with pages and dragging), closed(from, done)
    // (fetches the closed-topic page from index from and hands its root to
    // done, or null on a failure), adopted(node) (a topic node the closed
    // route brought, now placed), and drag(on) (a drag starts or ends:
    // board updates wait meanwhile); split (optional) is the grid whose
    // conversation column the split handle sizes, where it stands beside
    // the board. toolbar supplies overview(), optional download(), and
    // optional files: {el, shown(), hidden()} for the page-owned,
    // independent toolbar explorer sheet.
    mount: function (el, o) {
      board = el;
      opt = o;
      overviewOn = false;
      buildToolbar();
      buildGalleryEl(o.host || doc.body);
      if (o.split) splitHandle(o.split);
      var s = stored();
      bgalW = loadBgalWidth();
      storedSys = loadSysActive();
      sysActive = null;
      applyBgalWidth();
      setConvo(s.cw);
      gal = G.load(raw(), function (c) { return typeof c.kind === "string" && !!kinds[c.kind]; });
      s.topics.forEach(function (r) { topics[r.n] = { name: r.n, pinned: +r.p || 0, at: +r.a || 0, bar: r.b === null ? null : +r.b || 0, folded: !!r.f, rank: r.rank, el: null }; });
      s.cards.forEach(function (r) { add(r.kind, r); });
      sync();
      cards.slice().forEach(function (c) { if (c.k.unpinCloses && !c.pinned) c.close(); });
      cards.forEach(function (c) { c.paint(); });
      board.insertBefore(toolbar, board.firstChild);
      place(weave(Array.prototype.filter.call(board.childNodes, function (n) { return !fixedNode(n); })), fixedNode);
      persist();
      [board, galEl].forEach(function (box) {
        box.addEventListener("pointerdown", function (e) { titlePressStart(e); dragStart(e); });
        box.addEventListener("click", function (e) {
          var pp = e.target.closest && e.target.closest(".ppin");
          if (pp && pp.parentNode && pp.parentNode.tagName === "SUMMARY") {
            e.preventDefault();
            e.stopPropagation();
            var art = pp.closest && pp.closest(".wb-entry.panel[data-panel]");
            var sec = pp.closest && pp.closest("section.topic[data-topic]");
            var pid = art && art.getAttribute("data-panel");
            if (pid && window.WsPanelCard && window.WsPanelCard.toggle) {
              window.WsPanelCard.toggle(pid, sec && sec.getAttribute("data-topic") || "", art);
            }
            return;
          }
          var b = e.target.closest && e.target.closest(".tpin");
          if (b && b.parentNode.classList.contains("tbar")) pinTopic(b.parentNode.closest("section.topic"), b.getAttribute("aria-pressed") !== "true");
          var action = e.target.closest && e.target.closest(".t-fold, .t-raise");
          if (action && action.parentNode.classList.contains("tbar")) {
            var u = topicUnit(action.closest("section.topic"));
            if (action.classList.contains("t-fold")) foldUnit(u, !u.folded); else raise(u);
          }
          sheetClick(e);
          titleClick(e);
        });
        box.addEventListener("contextmenu", function (e) { if (dg && dg.touch) e.preventDefault(); });
      });
      doc.addEventListener("pointermove", function (e) { titlePressMove(e); dragMove(e); });
      doc.addEventListener("pointerup", function (e) { titlePressEnd(e); dragEnd(e); });
      doc.addEventListener("pointercancel", function (e) { titlePressEnd(e); dragEnd(e); });
      doc.addEventListener("pointermove", railMove);
      doc.addEventListener("pointerup", railEnd);
      doc.addEventListener("pointercancel", railEnd);
      doc.addEventListener("click", function (e) { if (dragged) { e.preventDefault(); e.stopPropagation(); } }, true);
      // A held touch is a drag, not a scroll.
      doc.addEventListener("touchmove", function (e) { if ((dg && dg.on) || (rg && rg.on)) e.preventDefault(); }, { passive: false });
      if (!resizeBound && typeof window !== "undefined" && window.addEventListener) {
        resizeBound = true;
        window.addEventListener("resize", function () { if (board) relayout(); });
      }
      if (!zoomBound) {
        zoomBound = true;
        if (doc.addEventListener) doc.addEventListener("wheel", onMockupWheel, { passive: false });
        if (typeof window !== "undefined" && window.addEventListener) window.addEventListener("message", onMockupZoomMessage);
      }
      floatToolbar();
      fetchMissing();
    },
    // open adds a card of a registered kind at the head of the unpinned
    // flow; data is the kind's record. at, when given, is the card's open
    // time (epoch ms) instead: a card for something that began earlier
    // stands where a card opened then would.
    open: function (kind, data, at, origin) {
      if (!board || !kinds[kind]) return null;
      var c = add(kind, { data: data, at: at });
      if (origin) openBefore(c, origin);
      arrange(null);
      persist();
      return c;
    },
    list: function (kind) { return cards.filter(function (c) { return !kind || c.kind === kind; }); },
    // Explorer float shares the sheet lifecycle; it never owns another card node.
    float: function (c) { if (c instanceof Card && c.kind === "explorer" && cards.indexOf(c) >= 0) maximize(c, true); },
    dismissFiles: function () { if (maxed && maxed.files) maximize(null); },
    // weave and settle are the board morph's two steps (dashboard.js
    // morphBoard): weave takes the nodes the board wants and returns the
    // placement, {pins, board} (every docked node, in pin order across the
    // pages, and the board region's nodes, in order); settle puts the
    // docked nodes in their panes and settles the board around the cards.
    weave: weave,
    settle: function (el, p) { place(p, fixedNode); persist(); },
    // gallery is the gallery's element (#bgal); docked is the docked units'
    // nodes in pin order; isDocked tells whether a node stands in a pane
    // now; wrap gives a topic section a docked topic's body wrapper (the
    // board morph wraps a docked topic's incoming section before it grafts
    // it into the live one); activate shows the page node stands on and
    // tells whether it switched (WsGallery, activate above).
    gallery: function () { return galEl; },
    showSys: showSys,
    sysPageEl: function (id) { return sysPageEls[id] || null; },
    activeSys: function () { return gal ? G.sysAt(gal, curPos()) : null; },
    onGalleryChange: function (fn) { if (typeof fn === "function") galListeners.push(fn); },
    galleryWidth: function () { return bgalW; },
    docked: function () { return pinned().map(function (u) { return u.el; }); },
    isDocked: function (n) { return !!(n && galEl && n.parentNode && n.parentNode.classList && n.parentNode.classList.contains("gal-pane") && galEl.contains(n)); },
    wrap: function (sec) { return topicName(sec) === null ? null : wrapTopic(sec); },
    activate: activate,
    toolbar: function () { return toolbar; },
    // Overview's view state and interactions live elsewhere; the gallery
    // stays as it stands meanwhile.
    overview: function (on) {
      overviewOn = !!on;
      if (overviewOn && window.WsGalPreview && window.WsGalPreview.hide) window.WsGalPreview.hide();
      if (floatQueue) floatQueue();
    },
    // pinnedTopic tells whether a topic name is docked on this page (its
    // node stands in a pane).
    pinnedTopic: function (name) { var r = topics[name]; return !!(r && r.pinned && r.el); },
    // isFourCol tells whether Band 4 (true 4-column mode) is active now.
    isFourCol: isFourCol,
    // relayout places everything again after the page's layout changed
    // (a width crossing, the layout menu).
    relayout: function () { if (board) { arrange(null); if (toolbar) fitToolbar(false); } },
    dragging: function () { return !!(dg && dg.on); },
    // move places a topic's or card's node as a drop does (to: pin, docked
    // on a new page; or after and before in the open flow).
    move: function (n, to) { var u = unitOf(n); if (u) drop(u, to); },
    // decorate gives topics the page added outside a board update (a
    // fetched closed page or single hydrated topic) their pin buttons.
    decorate: function (scope) { if (board) decorate(scope); },
    unfoldTopic: unfoldTopic,
    revealTopic: revealTopic,
    onFrameWheel: onFrameWheel,
    zoomSteps: ZOOM_STEPS,
    buildZoomGroup: buildZoomGroup,
    applyZoomGroup: applyZoomToGroup,
    applyZoomTarget: applyZoomToTarget,
    normZoom: normZoom,
    stepZoom: stepZoomValue,
    getMockupZoom: getMockupZoom,
    setMockupZoom: setMockupZoom,
    stepMockupZoom: stepMockupZoom,
    syncMockupElement: syncMockupElement,
    is: isCard
  };
})();
