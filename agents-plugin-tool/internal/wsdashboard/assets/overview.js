(function () {
  // Overview is a view, never a card fold or a saved card arrangement. The
  // page owns swaps and the shell bridge; this module owns the picker and
  // its one scroll writer, wbHistory.place (passed as o.keep). It covers
  // the board region only: docked units stay in the docking gallery
  // (cards.js) as they stand.
  var doc = document, root = doc.documentElement, o = null, on = false, busy = false;
  var entry = null, spacer = null, tabs = new Map(), callbacks = [], releaseCallbacks = [];
  var preview = null, previewTimer = 0, previewHoverTarget = null, previewLocked = false;
  var previewLeaveCleanup = null, previewSyncPending = false, previewSyncing = false;
  var pendingLandKey = null, pendingLandNode = null;
  var midHandled = false, midTimer = 0;
  var PREVIEW_DELAY = 120;
  var ITEM = "section.topic[data-topic], .bcard[data-card]";
  var SLOT = ITEM + ", .overview-placeholder";
  function all(sel, box) { return Array.prototype.slice.call((box || doc).querySelectorAll(sel)); }
  function identity(n) { return n && (n.hasAttribute("data-card") ? { card: n.getAttribute("data-card") } : { topic: n.getAttribute("data-topic") }); }
  function matchesKey(n, key) {
    if (!n || !key) return false;
    return key.card ? n.getAttribute("data-card") === key.card : n.getAttribute("data-topic") === key.topic;
  }
  function find(key) {
    if (!key) return null;
    if (preview && preview.node && matchesKey(preview.node, key)) return preview.node;
    return all(ITEM, o.board).filter(function (n) { return matchesKey(n, key); })[0] || null;
  }
  function resolveTarget(el) {
    if (!el || !el.closest) return null;
    if (preview && preview.placeholder && (el === preview.placeholder || preview.placeholder.contains(el))) return preview.node;
    return el.closest(ITEM);
  }
  function moveNode(parent, node, before) {
    if (!parent || !node) return;
    if (typeof parent.moveBefore === "function") {
      try { parent.moveBefore(node, before || null); return; } catch (e) {}
    }
    parent.insertBefore(node, before || null);
  }
  function clearPreviewTimer() {
    if (previewTimer) { clearTimeout(previewTimer); previewTimer = 0; }
  }
  function cancelHoverPreview() {
    clearPreviewTimer();
    previewHoverTarget = null;
  }
  function unbindPreviewCancel() {
    if (previewLeaveCleanup) previewLeaveCleanup();
  }
  function bindPreviewCancel() {
    unbindPreviewCancel();
    if (o && o.board && o.board.addEventListener) o.board.addEventListener("pointerleave", cancelHoverPreview);
    if (window.addEventListener) window.addEventListener("blur", cancelHoverPreview);
    previewLeaveCleanup = function () {
      if (o && o.board && o.board.removeEventListener) o.board.removeEventListener("pointerleave", cancelHoverPreview);
      if (window.removeEventListener) window.removeEventListener("blur", cancelHoverPreview);
      previewLeaveCleanup = null;
    };
  }
  function canPreview() {
    if (!o || !o.board) return false;
    if (!(window.WsCards && typeof window.WsCards.isFourCol === "function" && window.WsCards.isFourCol())) return false;
    var grid = doc.querySelector(".dgrid");
    if (grid && grid.getAttribute("data-layout") === "tabs") return false;
    return !!doc.getElementById("bgal");
  }
  function buildPlaceholder(node, height) {
    var ph = doc.createElement("div");
    ph.className = "overview-placeholder overview-previewing" + (node.classList.contains("overview-here") ? " overview-here" : "");
    ph.setAttribute("data-overview-placeholder", "");
    if (node.hasAttribute("data-topic")) ph.setAttribute("data-overview-target-topic", node.getAttribute("data-topic"));
    if (node.hasAttribute("data-card")) ph.setAttribute("data-overview-target-card", node.getAttribute("data-card"));
    if (height > 0) ph.style.height = height + "px";
    ph.setAttribute("tabindex", node.getAttribute("tabindex") === "0" ? "0" : "-1");
    var head = doc.createElement("div");
    head.className = "overview-placeholder-head";
    var titleEl = doc.createElement("span");
    titleEl.className = "overview-placeholder-title";
    var badgeText = "", titleText = "", recapText = "";
    if (node.hasAttribute("data-topic")) {
      var tn = node.querySelector(":scope > .tophead > .tbar .tname, :scope > .tophead > .tbar h2, :scope > .tophead > .tbar .t-title");
      titleText = (tn ? tn.textContent : node.getAttribute("data-topic") || "").trim();
      var sb = node.querySelector(":scope > .tophead > .tbar .tstate, :scope > .tophead > .tbar .badge");
      if (sb) badgeText = sb.textContent.trim();
      var rc = node.querySelector(".trecap .rtext");
      if (rc) recapText = rc.textContent.trim();
    } else {
      var nm = node.querySelector(".bc-nm");
      var sub = node.querySelector(".bc-sub");
      var kd = node.querySelector(".bc-kind");
      titleText = (nm ? nm.textContent : node.getAttribute("data-card") || "").trim();
      if (kd) badgeText = kd.textContent.trim();
      if (sub) recapText = sub.textContent.trim();
    }
    titleEl.textContent = titleText;
    head.appendChild(titleEl);
    if (badgeText) {
      var bEl = doc.createElement("span");
      bEl.className = "overview-placeholder-badge";
      bEl.textContent = badgeText;
      head.appendChild(bEl);
    }
    ph.appendChild(head);
    if (recapText) {
      var rEl = doc.createElement("div");
      rEl.className = "overview-placeholder-recap";
      rEl.textContent = recapText;
      ph.appendChild(rEl);
    }
    return ph;
  }
  function ensurePreviewHost() {
    if (preview && preview.host && preview.host.isConnected) return preview;
    var gal = doc.getElementById("bgal");
    if (!gal) return null;
    var host = doc.createElement("div");
    host.className = "overview-preview";
    host.setAttribute("role", "region");
    host.setAttribute("aria-label", typeof wsT === "function" ? wsT("js.board.toolbar.overview") : "Overview");
    var stage = doc.createElement("div");
    stage.className = "overview-preview-stage";
    host.appendChild(stage);
    host.addEventListener("pointerenter", function () {
      previewLocked = true;
      clearPreviewTimer();
      previewHoverTarget = null;
    });
    var galWasHidden = gal.hidden;
    gal.hidden = false;
    var vp = gal.querySelector(":scope > .gal-viewport");
    var rail = gal.querySelector(":scope > .gal-rail");
    if (vp) { vp.inert = true; vp.setAttribute("aria-hidden", "true"); }
    if (rail) { rail.inert = true; rail.setAttribute("aria-hidden", "true"); }
    gal.appendChild(host);
    root.setAttribute("data-overview-preview", "");
    preview = {
      node: null, placeholder: null, host: host, stage: stage,
      gal: gal, galWasHidden: galWasHidden, dockedViewport: vp, dockedRail: rail
    };
    return preview;
  }
  function restorePreviewNode(opts) {
    if (!preview || !preview.node) return;
    var node = preview.node, ph = preview.placeholder;
    if (!(opts && opts.keepLiveAttr)) node.removeAttribute("data-overview-live");
    if (ph && ph.parentNode) {
      var wasFocused = doc.activeElement === ph;
      var phTab = ph.getAttribute("tabindex");
      moveNode(ph.parentNode, node, ph);
      ph.remove();
      if (on && phTab !== null) node.setAttribute("tabindex", phTab);
      if (on && wasFocused) node.focus({ preventScroll: true });
    }
    preview.node = null;
    preview.placeholder = null;
    if (!(opts && opts.skipReconcile) && window.WsBoardAnchor && window.WsBoardAnchor.reconcile) window.WsBoardAnchor.reconcile();
  }
  function teardownPreview(opts) {
    clearPreviewTimer();
    previewLocked = false;
    previewHoverTarget = null;
    if (!preview) {
      root.removeAttribute("data-overview-preview");
      return;
    }
    restorePreviewNode(opts);
    if (preview.dockedViewport) {
      preview.dockedViewport.inert = false;
      preview.dockedViewport.removeAttribute("aria-hidden");
    }
    if (preview.dockedRail) {
      preview.dockedRail.inert = false;
      preview.dockedRail.removeAttribute("aria-hidden");
    }
    if (preview.host && preview.host.parentNode) preview.host.remove();
    var gal = preview.gal, galWasHidden = preview.galWasHidden;
    root.removeAttribute("data-overview-preview");
    preview = null;
    if (window.WsCards && window.WsCards.relayout) window.WsCards.relayout();
    else if (gal) gal.hidden = galWasHidden;
  }
  function showPreview(slot) {
    if (!on || !slot || !canPreview()) return;
    if (preview && (slot === preview.placeholder || slot === preview.node)) return;
    var node = resolveTarget(slot);
    if (!node || !node.parentNode || node.hasAttribute("data-state-filtered")) return;
    if (!ensurePreviewHost()) return;
    restorePreviewNode();
    var h = Math.round(node.getBoundingClientRect().height);
    var wasFocused = doc.activeElement === node;
    var ph = buildPlaceholder(node, h);
    node.parentNode.insertBefore(ph, node);
    moveNode(preview.stage, node, null);
    node.setAttribute("data-overview-live", "");
    preview.node = node;
    preview.placeholder = ph;
    if (wasFocused) ph.focus({ preventScroll: true });
    if (o.onPreview) o.onPreview(node);
  }
  function canMiddleToggle(t) {
    if (!o || !o.board || busy || !t || !t.closest) return false;
    var col = doc.getElementById("bcol") || o.board.parentElement;
    if (!col || !col.contains(t)) return false;
    if (t.closest("a[href], .xterm, .tm-host, .tpop, [popover]:popover-open, dialog[open], [data-max], .board-toolbar")) return false;
    if (root.hasAttribute("data-board-max")) return false;
    if (doc.querySelector("#tkqpop[open], dialog[open], .tpop:not([hidden]), [popover]:popover-open, .bcard[data-max], section.topic[data-max], details.entry-panel[data-max]")) return false;
    return true;
  }
  function scroller(n) {
    for (var p = n.parentElement; p && p !== doc.body && p !== root; p = p.parentElement) {
      if (/auto|scroll|overlay/.test(getComputedStyle(p).overflowY) && p.scrollHeight > p.clientHeight) return p;
    }
    return doc.scrollingElement || root;
  }
  function view(s) {
    return s === doc.scrollingElement || s === root ? { top: 0, bottom: innerHeight, height: innerHeight } :
      { top: s.getBoundingClientRect().top + s.clientTop, bottom: s.getBoundingClientRect().top + s.clientTop + s.clientHeight, height: s.clientHeight };
  }
  // Native scroll padding includes the live face and any fixed host controls.
  // Overview uses the same exposed viewport through its existing keep writer.
  function clearance(n) {
    var s = scroller(n), style = getComputedStyle(s), pad = parseFloat(style.scrollPaddingTop) || 0;
    var head = n.querySelector(n.hasAttribute("data-topic") ? ":scope > .tophead" : "[data-rail-bar]");
    if (!head) return pad;
    // The rail still publishes the capsule floor when Overview makes heads static.
    var rail = parseFloat(getComputedStyle(head).getPropertyValue("--wb-rail-top"));
    if (!isFinite(rail)) return pad;
    var padding = s === doc.scrollingElement || s === root ? 0 : parseFloat(style.paddingTop) || 0;
    return Math.max(pad, rail + padding);
  }
  function items() { return all(typeof SLOT !== "undefined" ? SLOT : ITEM, o.board).filter(function (n) { return !n.hasAttribute("data-state-filtered"); }); }
  function anchor(list, toolbar, bounds, floating) {
    if (!floating && toolbar.bottom > bounds.top && toolbar.top < bounds.bottom) return null;
    var top = floating ? Math.max(bounds.top, toolbar.bottom) : bounds.top;
    return list.filter(function (n) { var r = n.getBoundingClientRect(); return r.bottom > top && r.top < bounds.bottom; })[0] || null;
  }
  function classify(t) {
    if (!t || !t.closest) return "outside";
    if (typeof preview !== "undefined" && preview && preview.host && preview.host.contains(t)) return "preview";
    if (t.closest(".board-toolbar")) return "toolbar";
    if (t.closest(".tpop:not([hidden]), [popover]:popover-open")) return "popover";
    if (t.closest("#tkqpop, #tkqs, #shandle, dialog[open], .tk-iwin-back:not([hidden]), .d-tm-back")) return "palette";
    if (t.closest(typeof SLOT !== "undefined" ? SLOT : ITEM) && o.board.contains(t)) return "item";
    return "outside";
  }
  function layer() {
    if (doc.querySelector("#tkqpop[open], dialog[open]")) return "palette";
    if (doc.querySelector(".tpop:not([hidden])")) return "popover";
    return "overview";
  }
  function readState(raw) {
    try {
      var v = JSON.parse(raw);
      if (v && v.on === true && isFinite(v.offset) && (!v.key || typeof v.key.card === "string" || typeof v.key.topic === "string")) return v;
    } catch (e) { /* storage is optional */ }
    return null;
  }
  function stored() { try { return readState(sessionStorage.getItem("ws.dashboard.overview." + o.rid)); } catch (e) { return null; } }
  function save() {
    try {
      var key = "ws.dashboard.overview." + o.rid;
      if (on) sessionStorage.setItem(key, JSON.stringify({ on: true, key: entry.key, offset: entry.offset }));
      else sessionStorage.removeItem(key);
    } catch (e) { /* denied storage must not disable the view */ }
  }
  // The card the reader lands on is the board's current card (anchor).
  function select(n) { if (window.WsBoardAnchor && n) window.WsBoardAnchor.select(n.closest ? n.closest(ITEM) || n : n); }
  function keep(n, offset) { return o.keep(n, { offset: offset, observe: [o.board] }); }
  function flash(n) {
    n.classList.remove("overview-flash");
    void n.offsetWidth;
    n.classList.add("overview-flash");
    setTimeout(function () { n.classList.remove("overview-flash"); }, 1200);
  }
  function rove(n, focus) {
    tabs.forEach(function (v, x) { if (x.hasAttribute("data-state-filtered")) x.setAttribute("tabindex", "-1"); });
    items().forEach(function (x) {
      if (!tabs.has(x)) tabs.set(x, x.getAttribute("tabindex"));
      x.setAttribute("tabindex", x === n ? "0" : "-1");
    });
    if (n && focus !== false) n.focus({ preventScroll: true });
  }
  function unrove() {
    tabs.forEach(function (v, n) { if (v === null) n.removeAttribute("tabindex"); else n.setAttribute("tabindex", v); });
    tabs.clear();
  }
  function hint() {
    try {
      if (sessionStorage.getItem("ws.dashboard.overview.hint")) return;
      sessionStorage.setItem("ws.dashboard.overview.hint", "1");
    } catch (e) { /* show the hint if storage is denied */ }
    if (o.toast) o.toast(wsT("js.board.overview.hint"));
  }
  function changed() { if (o.changed) o.changed(); }
  function finish() {
    busy = false;
    if (!on && o.release) o.release();
    if (previewSyncPending) {
      previewSyncPending = false;
      if (on) syncPreview();
    }
    var c = callbacks.splice(0);
    c.forEach(function (fn) { fn(); });
    if (!on && releaseCallbacks.length) {
      var r = releaseCallbacks.splice(0);
      r.forEach(function (fn) { fn(); });
    }
  }
  // Unlike after(), a filter waits for motion without requesting an exit.
  function filter(run) {
    if (busy) { callbacks.push(function () { filter(run); }); return; }
    if (!on) { run(); return; }
    teardownPreview();
    var before = all(ITEM, o.board), list = items(), current = list.filter(function (n) { return n.getAttribute("tabindex") === "0"; })[0];
    var index = before.indexOf(current), v = view(scroller(o.toolbar)), face = o.toolbar.firstChild.getBoundingClientRect();
    var top = o.toolbar.hasAttribute("data-floating") ? Math.max(v.top, face.bottom) : v.top;
    var rows = list.map(function (n) { return { n: n, r: n.getBoundingClientRect() }; }).filter(function (a) { return a.r.bottom > top && a.r.top < v.bottom; });
    var toolbarOffset = o.toolbar.getBoundingClientRect().top - v.top;
    run();
    list = items();
    var next = list.indexOf(current) >= 0 ? current : list.filter(function (n) { return before.indexOf(n) >= index; })[0] || list[list.length - 1];
    rove(next, false);
    var row = rows.filter(function (a) { return list.indexOf(a.n) >= 0; })[0];
    if (row) keep(row.n, row.r.top - v.top);
    else if (list.length) keep(list[0], clearance(list[0]));
    else keep(o.toolbar, Math.max(0, toolbarOffset));
    if (next && canPreview()) showPreview(next);
    save();
  }
  function calm() { return !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches); }
  // The one cubic-bezier(.2,.8,.2,1) clock drives heights, the slot, scroll
  // compensation and flights. Exit traverses that same timeline backwards.
  function ease(p) {
    var lo = 0, hi = 1, t = p;
    for (var i = 0; i < 16; i++) {
      t = (lo + hi) / 2;
      var x = 0.6 * t * (1 - t) + t * t * t;
      if (x < p) lo = t; else hi = t;
    }
    return p <= 0 ? 0 : p >= 1 ? 1 : 2.4 * t * (1 - t) * (1 - t) + 3 * t * t * (1 - t) + t * t * t;
  }
  function snapshot() {
    var list = items(), seen = new Set();
    return list.filter(function (n) { if (seen.has(n) || n.hasAttribute("data-state-filtered")) return false; seen.add(n); return true; }).map(function (n) {
      var r = n.getBoundingClientRect(), text = n.querySelector(".trecap .rtext");
      return { n: n, r: r, height: r.height, margin: parseFloat(getComputedStyle(n).marginBottom) || 0,
        text: text, recap: text ? text.getBoundingClientRect().height : 0 };
    });
  }
  function motion(before, entering, node, from, to, done) {
    var end = snapshot(), v = view(scroller(node)), rows = [];
    before.forEach(function (a) {
      var b = end.filter(function (x) { return x.n === a.n; })[0];
      if (!b || a.r.bottom < v.top - v.height || a.r.top > v.bottom + v.height) return;
      var n = a.n;
      var old = { height: n.style.height, overflow: n.style.overflow, boxSizing: n.style.boxSizing, marginBottom: n.style.marginBottom };
      rows.push({ a: a, b: b, old: old, textHeight: a.text && a.text.style.maxHeight });
      n.setAttribute("data-overview-motion", "");
      n.style.boxSizing = "border-box";
      n.style.overflow = "hidden";
      n.style.height = a.height + "px";
      n.style.marginBottom = a.margin + "px";
      if (a.text) a.text.style.maxHeight = a.recap + "px";
    });
    var started = performance.now();
    function frame(now) {
      var p = Math.min(1, Math.max(0, (now - started) / 240)), e = entering ? ease(p) : 1 - ease(1 - p);
      rows.forEach(function (r) {
        r.a.n.style.height = (r.a.height + (r.b.height - r.a.height) * e) + "px";
        r.a.n.style.marginBottom = (r.a.margin + (r.b.margin - r.a.margin) * e) + "px";
        if (r.a.text) r.a.text.style.maxHeight = (r.a.recap + (r.b.recap - r.a.recap) * e) + "px";
      });
      keep(node, from + (to - from) * e);
      if (p < 1) { requestAnimationFrame(frame); return; }
      rows.forEach(function (r) {
        r.a.n.removeAttribute("data-overview-motion");
        Object.keys(r.old).forEach(function (k) { r.a.n.style[k] = r.old[k]; });
        if (r.a.text) r.a.text.style.maxHeight = r.textHeight;
      });
      keep(node, to);
      done();
    }
    frame(started);
  }
  // The instant path is also the reduced-motion end-state contract. Motion
  // interpolates around it without inventing a second set of states.
  function enter(state) {
    if (!o || on || busy || doc.querySelector(".bcard[data-max], section.topic[data-max], details.entry-panel[data-max]")) return;
    var s = scroller(o.toolbar), v = view(s), n = anchor(items(), o.toolbar.firstChild.getBoundingClientRect(), v, o.toolbar.hasAttribute("data-floating"));
    if (!n && window.WsBoardAnchor && window.WsBoardAnchor.bounded && window.WsBoardAnchor.bounded()) {
      n = o.board.querySelector("section.topic[data-current], .bcard[data-current]");
    }
    entry = state || { key: identity(n), offset: (n || o.toolbar).getBoundingClientRect().top - v.top };
    if (state && entry.key && !find(entry.key)) entry = { key: null, offset: 0 };
    n = find(entry.key);
    if (n && n.hasAttribute("data-state-filtered")) { n = items()[0] || null; entry = { key: identity(n), offset: 0 }; }
    var shown = n ? Math.max(entry.offset, clearance(n)) : entry.offset;
    var h = o.frames ? o.frames() : null, before = !state && !calm() ? snapshot() : null;
    busy = !!before;
    // Keep enough tail to reach even a partly-visible last topic's negative
    // offset. Added before folding, removed only after unfolding inside keep.
    spacer = doc.createElement("div");
    spacer.className = "overview-spacer";
    spacer.setAttribute("aria-hidden", "true");
    spacer.style.height = Math.max(v.height, v.height - entry.offset) + "px";
    o.board.appendChild(spacer);
    on = true;
    root.setAttribute("data-overview", "");
    bindPreviewCancel();
    if (n) n.classList.add("overview-here");
    o.place(true);
    if (o.restoreFrames) o.restoreFrames(h);
    o.toolbar.querySelector(".board-overview").setAttribute("aria-pressed", "true");
    save(); changed();
    var complete = function () {
      var initial = n || items()[0];
      rove(initial);
      if (initial && canPreview()) showPreview(initial);
      finish();
      if (!state) hint();
    };
    if (before) motion(before, true, n || o.toolbar, entry.offset, shown, complete);
    else { keep(n || o.toolbar, shown); complete(); }
  }
  function exit(target, done, options) {
    if (!o || !on) { if (done) done(); return; }
    if (busy) { if (done) callbacks.push(done); return; }
    var resolved = target ? resolveTarget(target) : null;
    if (resolved && window.wbPill && window.wbPill.apply) {
      // Leave the holding view before explicit apply, then resolve the live
      // target. Without pending updates the ordinary exit motion is unchanged.
      var key = identity(resolved);
      var land = function () {
        if (!window.wbPill.apply(land)) return;
        var live = find(key);
        if (live) {
          keep(live, clearance(live)); select(live); flash(live);
          if (!(options && options.keep)) { live.setAttribute("tabindex", "-1"); live.focus({ preventScroll: true }); }
        }
        if (pendingLandNode) {
          pendingLandNode.removeAttribute("data-overview-live");
          pendingLandNode = null;
        }
        pendingLandKey = null;
        if (window.WsBoardAnchor && window.WsBoardAnchor.settle) window.WsBoardAnchor.settle();
        if (done) done();
      };
      pendingLandKey = key;
      if (!window.wbPill.apply(land)) return;
      pendingLandKey = null;
      pendingLandNode = null;
    }
    unbindPreviewCancel();
    var exitingPreviewNode = preview && preview.node || null;
    teardownPreview({ skipReconcile: true, keepLiveAttr: !!exitingPreviewNode });
    var n = resolved;
    if (n && n.hasAttribute("data-state-filtered")) n = null;
    var h = o.frames ? o.frames() : null;
    var restore = find(entry.key);
    if (restore && restore.hasAttribute("data-state-filtered")) { restore = null; if (!n) n = items()[0] || null; }
    var fastSnap = !!(window.WsBoardAnchor && window.WsBoardAnchor.bounded && window.WsBoardAnchor.bounded());
    if (fastSnap) {
      on = false;
      root.removeAttribute("data-overview");
      unrove();
      o.place(false);
      if (o.restoreFrames) o.restoreFrames(h);
      if (spacer) { spacer.remove(); spacer = null; }
      var landedFast = n && n.isConnected ? n : null;
      var targetFast = landedFast || restore || items()[0] || null;
      o.toolbar.querySelector(".board-overview").setAttribute("aria-pressed", "false");
      save(); changed();
      if (targetFast && window.WsBoardAnchor && window.WsBoardAnchor.select) {
        window.WsBoardAnchor.select(targetFast, { keep: true, still: false, smooth: false });
      }
      if (pendingLandKey) {
        pendingLandNode = exitingPreviewNode;
      } else {
        if (exitingPreviewNode) exitingPreviewNode.removeAttribute("data-overview-live");
        if (window.WsBoardAnchor && window.WsBoardAnchor.settle) window.WsBoardAnchor.settle();
      }
      if (!targetFast) keep(o.toolbar, entry.offset);
      if (targetFast && !calm() && targetFast.animate) {
        try {
          targetFast.animate(
            [{ opacity: 0.86 }, { opacity: 1 }],
            { duration: 160, easing: "cubic-bezier(.22, 1, .36, 1)", fill: "none" }
          );
        } catch (e) {}
      }
      if (landedFast) {
        flash(landedFast);
        if (!(options && options.keep)) { landedFast.setAttribute("tabindex", "-1"); landedFast.focus({ preventScroll: true }); }
      } else if (!(options && options.keep)) o.toolbar.querySelector(".board-overview").focus({ preventScroll: true });
      all(".overview-here", o.board).forEach(function (x) { x.classList.remove("overview-here"); });
      var colEl = doc.getElementById("bcol");
      if (colEl) colEl.dispatchEvent(new Event("scroll"));
      finish();
      if (done) done();
      return;
    }
    var before = !calm() ? snapshot() : null;
    var planned = n || restore;
    var from = (planned || o.toolbar).getBoundingClientRect().top - view(scroller(planned || o.toolbar)).top;
    busy = !!before;
    on = false;
    root.removeAttribute("data-overview");
    unrove();
    o.place(false);
    if (o.restoreFrames) o.restoreFrames(h);
    var landed = n && n.isConnected ? n : null;
    var boardTarget = landed || restore;
    var arrival = clearance(boardTarget || o.toolbar);
    var offset = landed && boardTarget === landed ? arrival : boardTarget && restore === boardTarget ? entry.offset : entry.key ? arrival : entry.offset;
    o.toolbar.querySelector(".board-overview").setAttribute("aria-pressed", "false");
    save(); changed();
    var complete = function () {
      if (spacer) { spacer.remove(); spacer = null; }
      keep(boardTarget || o.toolbar, offset);
      if (boardTarget) select(boardTarget);
      if (landed) {
        flash(landed);
        if (!(options && options.keep)) { landed.setAttribute("tabindex", "-1"); landed.focus({ preventScroll: true }); }
      } else if (!(options && options.keep)) o.toolbar.querySelector(".board-overview").focus({ preventScroll: true });
      all(".overview-here", o.board).forEach(function (x) { x.classList.remove("overview-here"); });
      if (pendingLandKey) {
        pendingLandNode = exitingPreviewNode;
      } else {
        if (exitingPreviewNode) exitingPreviewNode.removeAttribute("data-overview-live");
        if (window.WsBoardAnchor && window.WsBoardAnchor.reconcile) window.WsBoardAnchor.reconcile();
      }
      finish();
      if (done) done();
    };
    if (before) motion(before, false, boardTarget || o.toolbar, from, offset, complete);
    else { keep(boardTarget || o.toolbar, offset); complete(); }
  }
  function swallow(e) { e.preventDefault(); e.stopImmediatePropagation(); }
  function click(e) {
    if (!on && !busy) return;
    if (busy) { swallow(e); return; }
    var c = classify(e.target);
    if (c === "toolbar" || c === "popover" || c === "palette" || c === "preview") return;
    swallow(e);
    exit(c === "item" ? e.target : null);
  }
  function previewKeepsKey(e) {
    var t = e.target;
    if (!t) return false;
    if (t.isContentEditable) return true;
    if (t.closest && t.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), .xterm, .tm-host')) return true;
    if (e.key === "Enter" && t.closest && t.closest("button, a[href], summary, label")) return true;
    return false;
  }
  function key(e) {
    if (!on && !busy) return;
    if (e.isComposing) return;
    if (e.code === "Backquote" && e.ctrlKey && !e.altKey && !e.shiftKey && !e.metaKey) {
      swallow(e); exit(null, o.rail); return;
    }
    if (layer() !== "overview") return;
    if (e.key === "Escape") { swallow(e); if (!busy) exit(null); return; }
    if (busy) { if (e.key === "Enter") swallow(e); return; }
    var cls = classify(e.target);
    if (cls === "toolbar") return;
    if (cls === "preview" && typeof previewKeepsKey === "function" && previewKeepsKey(e)) return;
    var list = items(), i = list.indexOf(doc.activeElement);
    if (i < 0 && typeof preview !== "undefined" && preview && preview.placeholder) i = list.indexOf(preview.placeholder);
    if (i < 0) {
      for (var k = 0; k < list.length; k++) {
        if (list[k].getAttribute("tabindex") === "0") { i = k; break; }
      }
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      swallow(e);
      var next = list[Math.max(0, Math.min(list.length - 1, i < 0 ? 0 : i + (e.key === "ArrowDown" ? 1 : -1)))];
      rove(next);
      if (next) {
        var r = next.getBoundingClientRect(), v = view(scroller(next)), pad = clearance(next);
        if (r.top < v.top + pad || r.bottom > v.bottom) keep(next, r.top < v.top + pad ? pad : Math.max(pad, v.height - r.height));
        if (typeof canPreview === "function" && canPreview()) { previewLocked = false; clearPreviewTimer(); showPreview(next); }
      }
    } else if (e.key === "Enter" && i >= 0) { swallow(e); exit(list[i]); }
  }
  function boardPointerMove(e) {
    if (!on || busy || !canPreview()) return;
    previewLocked = false;
    var slot = e.target && e.target.closest && e.target.closest(SLOT);
    if (!slot || !o.board.contains(slot)) {
      clearPreviewTimer();
      previewHoverTarget = null;
      return;
    }
    if (preview && (slot === preview.placeholder || slot === preview.node)) {
      clearPreviewTimer();
      previewHoverTarget = null;
      return;
    }
    if (slot === previewHoverTarget) return;
    previewHoverTarget = slot;
    clearPreviewTimer();
    previewTimer = setTimeout(function () {
      previewTimer = 0;
      if (!on || busy || previewLocked || previewHoverTarget !== slot) return;
      showPreview(slot);
    }, PREVIEW_DELAY);
  }
  function syncPreview() {
    if (!on) { previewSyncPending = false; return; }
    if (busy) { previewSyncPending = true; return; }
    if (previewSyncing) return;
    previewSyncPending = false;
    previewSyncing = true;
    try {
      if (preview && !canPreview()) {
        teardownPreview();
      } else if (!preview && canPreview()) {
        var list = items();
        var target = list.filter(function (n) { return n.getAttribute("tabindex") === "0"; })[0]
          || (o && o.board && o.board.querySelector(".overview-here"))
          || list[0];
        if (target) showPreview(target);
      }
    } finally {
      previewSyncing = false;
    }
  }
  window.WsOverview = {
    mount: function (options) {
      o = options;
      doc.addEventListener("click", click, true);
      // Middle-click toggles Overview in #bcol (suppressing autoscroll on both
      // mousedown and auxclick). Left-click mousedown during Overview prevents
      // focusing dimmed controls before click is captured; preview stays native.
      doc.addEventListener("mousedown", function (e) {
        if (e.button === 1) {
          if (canMiddleToggle(e.target)) {
            e.preventDefault();
            midHandled = true;
            clearTimeout(midTimer);
            midTimer = setTimeout(function () { midHandled = false; }, 600);
            if (on) exit(null); else enter(null);
          }
          return;
        }
        if ((on || busy) && (busy || classify(e.target) === "outside" || classify(e.target) === "item")) e.preventDefault();
      }, true);
      doc.addEventListener("auxclick", function (e) {
        if (e.button !== 1) return;
        if (e.target && e.target.closest && e.target.closest("a[href], .xterm, .tm-host")) {
          midHandled = false;
          clearTimeout(midTimer);
          return;
        }
        if (midHandled) {
          e.preventDefault();
          midHandled = false;
          clearTimeout(midTimer);
          return;
        }
        if (canMiddleToggle(e.target)) e.preventDefault();
      }, true);
      doc.addEventListener("keydown", key, true);
      if (o.board && o.board.addEventListener) o.board.addEventListener("pointermove", boardPointerMove, { passive: true });
      if (window.addEventListener) window.addEventListener("resize", syncPreview);
      var state = stored();
      if (state) requestAnimationFrame(function () { if (o.reveal) o.reveal(); enter(state); });
    },
    active: function () { return on; },
    holding: function () { return on || busy; },
    inPreview: function (el) { return !!(preview && preview.host && el && preview.host.contains(el)); },
    previewNode: function () { return preview ? preview.node : null; },
    syncPreview: syncPreview,
    replacePreviewNode: function (oldNode, newNode) {
      if (!preview || preview.node !== oldNode || !newNode) return;
      oldNode.removeAttribute("data-overview-live");
      newNode.setAttribute("data-overview-live", "");
      if (oldNode.classList.contains("overview-here")) newNode.classList.add("overview-here");
      if (tabs.has(oldNode)) {
        tabs.set(newNode, tabs.get(oldNode));
        tabs.delete(oldNode);
      }
      preview.node = newNode;
    },
    toggle: function () { if (on) exit(null); else enter(null); },
    exit: exit,
    after: function (fn) { if (on) exit(null, fn); else if (busy) callbacks.push(fn); else fn(); },
    onRelease: function (fn) { if (!on && !busy) fn(); else releaseCallbacks.push(fn); },
    filter: filter,
    pick: function (n, options) { if (!on) return false; exit(n, null, options); return true; }
  };
})();
