  // --- drawer: the side panels pulled down from the top --------------------
  // A frame of registered entries over the page. Each entry is { id, title,
  // icon, count, badge, content }: functions read at every sync, content
  // returning the element the entry's card is or null when the entry is
  // absent, and badge (optional) a count for the handle icon or null. The
  // entries are the drawer's own cards (drcards); the panels region lives in
  // the gallery's Workpanel (dashboard-workpanel.js), and a later entry
  // registers here without reworking the frame. The sheet shows every present entry's card at once
  // and the handle holds one icon per present entry. Open state is this
  // browser's (ws.dashboard.drawer*); height always fills the available room.
  //
  // The search handle opens the same frame on the search sheet (#sbody)
  // instead of the panels: drMode is the sheet the frame shows, "panels" or
  // "search", and only the handle of the shown sheet is hidden. Repository
  // state lives on the gallery's Git page (dashboard-gitpage.js), not here.
  //
  // Every drawer fills from the header bottom to above the composer (or
  // the viewport bottom without one), including the tracker's store sheets.
  // While a sheet shows, the fold button (#drfold, built below) replaces its
  // opener at the top, at that handle's width (--drw), and closes it.
  var drawer = $("drawer"), drclip = $("drclip"), drsheet = $("drsheet"), drbody = $("drbody");
  var drhandle = $("drhandle"), drheader = doc.querySelector(".dwrap > .top");
  var DR_OPEN = "ws.dashboard.drawer", DR_SEARCH = "ws.dashboard.drawer.search";
  var drRoomW = -1, drEntries = [], drWant = false, drOpen = false, drHas = false, drTop = 0;
  var drMode = "panels", drShown = "", drfold = null, drFoldW = -1;
  var sbody = null, shandle = null;
  if (drawer) {
    sbody = doc.createElement("div"); sbody.id = "sbody"; sbody.className = "dr-search"; sbody.hidden = true;
    drsheet.appendChild(sbody);
    shandle = doc.createElement("button"); shandle.type = "button"; shandle.id = "shandle"; shandle.className = "dr-handle dr-search-handle";
    shandle.title = wsT("js.tk.qs.tip") + " (Ctrl+K)"; shandle.setAttribute("aria-label", shandle.title); shandle.setAttribute("aria-controls", "drsheet");
    shandle.appendChild(luIcon("search")); drhandle.parentNode.insertBefore(shandle, drhandle);
  }
  // The handle's dot: a section badge. #r-panels lives in the Workpanel
  // (dashboard-workpanel.js), so a panels change no longer lights it.
  var drBadge = false;
  function drawerTab(entry) { drEntries.push(entry); }
  // cardTab registers one of the drawer's own cards (#p-<id>, drcards):
  // no region swap touches them.
  function cardTab(id) {
    drawerTab({
      id: id,
      title: function () {
        var h = $("p-" + id), sm = h && h.querySelector("summary");
        return sm ? Array.prototype.filter.call(sm.childNodes, function (n) { return n.nodeType === 3; }).map(function (n) { return n.nodeValue; }).join("").trim() : id;
      },
      count: function () { return ""; },
      content: function () { return $("p-" + id); }
    });
  }
  cardTab("holders");
  // The holders card's nodes (hdLoad, below): set here, ahead of the
  // drawer's first drApply, which a drawer left open calls on load.
  var hdlist = $("hdlist"), hdcount = $("hdcount"), hdnote = $("hdnote"), hdSeq = 0;

  function drPresent() { return drEntries.filter(function (t) { return !!t.content(); }); }
  // drawerSync re-reads the entries: each card kept open, the handle's
  // tooltip (the section titles, with what is new in each) and its dot, and
  // whether there is a drawer at all. A sync never changes the open state or
  // the scroll.
  function drawerSync() {
    if (!drawer) return;
    var present = drPresent(), tips = [];
    drBadge = false;
    present.forEach(function (t) {
      var el = t.content();
      if (el.tagName === "DETAILS" && !el.open) el.open = true;
      var bd = t.badge && t.badge(t.count());
      if (bd) drBadge = true;
      tips.push(bd ? t.title() + ": " + bd.title : t.title());
    });
    drhandle.setAttribute("data-tip", tips.join(", "));
    drDot();
    drTitle();
    var has = present.length > 0;
    if (has !== drHas) {
      drHas = has;
      drhandle.hidden = !has;
      if (drMode === "panels") {
        drOpen = has && drWant;
        if (!drBooting) drApply(false);
      }
    }
    drFill(true);
  }
  var drBooting = false;
  // drFill stretches cards into the empty cells of the panels grid's last
  // row: the card directly above each empty cell spans two rows. Cards keep
  // their DOM order (grid-auto-flow: dense would reorder them); a spanning
  // card sits in the full row above the last, right of every last-row card,
  // so auto-placement puts the last row's cards in the same cells as before.
  // The column count is the computed track list (auto-fit's collapsed tracks
  // read 0px). Spans clear first, and only while the body is a grid: the
  // narrow layout (a flex column) and the search sheet (body hidden) get none.
  // A card already placed or spanned by a stylesheet skips the fill.
  var drFilled = [], drFillFrame = 0, drFillRoom = false;
  function drFill(room) {
    if (room === true) drFillRoom = true;
    if (drFillFrame) return;
    drFilled.forEach(function (el) { el.style.gridRow = ""; });
    drFilled = [];
    drFillFrame = requestAnimationFrame(function () {
      drFillFrame = 0;
      var fill = [], cs = drbody && getComputedStyle(drbody);
      if (drbody && !drbody.hidden && cs.display === "grid") {
        var n = cs.gridTemplateColumns.split(/\s+/).filter(function (w) { return parseFloat(w) > 0; }).length;
        var cards = [];
        (function items(el) {
          Array.prototype.forEach.call(el.children, function (c) {
            var d = getComputedStyle(c).display;
            if (d === "contents") items(c);
            else if (d !== "none") cards.push(c);
          });
        })(drbody);
        var k = cards.length, rem = n > 0 ? k % n : 0;
        if (n >= 2 && k > n && rem && !cards.some(function (c) {
          var s = getComputedStyle(c);
          return [s.gridRowStart, s.gridRowEnd, s.gridColumnStart, s.gridColumnEnd].some(function (v) { return v !== "auto"; });
        })) {
          var above = (Math.floor(k / n) - 1) * n;
          for (var c = rem; c < n; c++) fill.push(cards[above + c]);
        }
      }
      if (drFillRoom) { drFillRoom = false; drRoom(); }
      fill.forEach(function (el) { el.style.gridRow = "span 2"; });
      drFilled = fill;
    });
  }
  function drDot() {
    var d = drhandle.querySelector(".tdot");
    if (d) d.classList.toggle("on", drBadge);
  }
  // drRoom keeps the header and the tracker's handle row clear of the
  // handles: --drhw is their width together (the header's padding and the
  // tracker row read it), --shr the search handle's offset from the right
  // edge (the handle right of it and its 6px gap, 0 when nothing is right
  // of it or it is absent).
  function drRoom() {
    var w = drHas ? Math.ceil(drhandle.getBoundingClientRect().width) : 0;
    var sw = shandle ? Math.ceil(shandle.getBoundingClientRect().width) : 0;
    var so = w && sw ? w + 6 : 0, all = sw ? so + sw : w;
    if (so !== drRoom._shr) {
      drRoom._shr = so;
      doc.documentElement.style.setProperty("--shr", so + "px");
    }
    if (all !== drRoomW) {
      drRoomW = all;
      doc.documentElement.style.setProperty("--drhw", all + "px");
    }
    if (drheader) drheader.classList.toggle("dr-room", all > 0);
    if (w !== drFoldW) {
      drFoldW = w;
      doc.documentElement.style.setProperty("--drw", w + "px");
    }
  }
  // drFoldShow shows the fold button in mode's handle slot, or hides it
  // (mode "").
  function drFoldShow(mode) {
    if (!drfold) return;
    drfold.hidden = !mode;
    drfold.classList.toggle("search", mode === "search");
  }
  // drTitle labels the handle and the fold button, with the approval wait
  // drawerWait (dashboard-workpanel.js) marks on them.
  function drTitle() {
    var t = wsT(drOpen && drMode === "panels" ? "js.drawer.close" : "js.drawer.open"), tip = drhandle.getAttribute("data-tip");
    if (tip) t += " (" + tip + ")";
    if (drhandle.classList.contains("wait")) t += "; " + wsT("js.drawer.waiting");
    drhandle.title = t;
    drhandle.setAttribute("aria-label", t);
    // The fold button announces the wait it shows (in the panels slot only).
    if (!drfold) return;
    var f = wsT("js.drawer.fold");
    if (drMode === "panels" && drfold.classList.contains("wait")) f += "; " + wsT("js.drawer.waiting");
    drfold.title = f;
    drfold.setAttribute("aria-label", f);
  }
  function drHeaderBottom() {
    return drheader ? Math.max(0, Math.round(drheader.getBoundingClientRect().bottom)) : 0;
  }
  // One maximum for every sheet. Retain the composer's existing clearance;
  // without a visible composer the sheet reaches the viewport bottom.
  function drMax() {
    var top = drHeaderBottom(), m = window.innerHeight - top, c = $("composer");
    if (visible(c)) m = Math.min(m, Math.round(c.getBoundingClientRect().top) - top - drhandle.offsetHeight - 6);
    return Math.max(0, m);
  }
  // drApply lays the drawer out for its state; anim animates a click's
  // toggle only.
  function drApply(anim) {
    if (!drawer) return;
    drTop = drHeaderBottom();
    var h = drMax(), search = drMode === "search";
    drawer.style.setProperty("--drtop", drTop + "px");
    drawer.classList.toggle("anim", !!anim);
    drawer.classList.toggle("open", drOpen);
    ["panels", "search"].forEach(function (mode) { drawer.classList.toggle(mode, drMode === mode); });
    drbody.hidden = search;
    if (sbody) sbody.hidden = !search;
    drsheet.setAttribute("aria-label", wsT(search ? "js.tk.qs.tip" : "js.drawer.open"));
    drsheet.style.height = h + "px";
    drclip.style.height = (drOpen ? h : 0) + "px";
    if (shandle) shandle.setAttribute("aria-expanded", String(drOpen && search));
    drhandle.setAttribute("aria-expanded", String(drOpen && !search));
    drFoldShow(drOpen ? drMode : "");
    drTitle();
    drFill();
    // Each opening of the panels sheet reads its data again: the session
    // holders and the notes. The worktree pool is the Git page's (it reads
    // its cache-only list there, never on a drawer opening).
    var shown = drOpen ? drMode : "";
    if (shown !== drShown) {
      drShown = shown;
      if (shown === "panels") hdLoad();
      if (tkApi) { if (shown === "search") tkApi.openSearch(); else tkApi.closeSearch(); }
    }
  }
  // The session drawer and a tracker store's drawer never show at once:
  // opening this one tells the tracker (which collapses its open store), and
  // the tracker opening a store closes this one (tk:drawer-open, below).
  function drAnnounce() { doc.dispatchEvent(new CustomEvent("ws:drawer-open")); }
  // drSetOpen opens the frame on mode's sheet (the shown one when omitted),
  // or closes it. Only the panels sheet's open state is kept.
  function drSetOpen(open, mode) {
    mode = mode || drMode;
    if (open && !(mode === "search" ? shandle : drHas)) return;
    var opener = mode === "search" && shandle || drhandle, kb = open && doc.activeElement === opener;
    if (open && !drOpen) drAnnounce();
    if (!open && drsheet.contains(doc.activeElement)) (drMode === "search" && shandle || drhandle).focus();
    drMode = mode;
    drOpen = open;
    drWant = open && mode === "panels";
    save(DR_OPEN, drWant ? "1" : null);
    save(DR_SEARCH, open && mode === "search" ? "1" : null);
    drApply(true);
    // The focused opener hides while its sheet shows: focus moves to the fold
    // button in its slot instead of falling to the body, only while it is
    // still on that opener (opening search focuses its input in drApply).
    if (kb && drfold && doc.activeElement === opener) drfold.focus();
  }
  // drFold is a click on the fold button: the shown sheet closes as a click
  // on its handle closes it, and keyboard focus goes to that handle, since
  // the button hides.
  function drFold() {
    if (!drOpen) return;
    var back = drMode === "search" && shandle || drhandle, kb = doc.activeElement === drfold;
    drSetOpen(false);
    if (kb) back.focus();
  }
  if (drawer) {
    // The fold button replaces the active sheet's opener.
    drfold = doc.createElement("button");
    drfold.type = "button";
    drfold.className = "dr-fold";
    drfold.id = "drfold";
    drfold.hidden = true;
    drfold.title = wsT("js.drawer.fold");
    drfold.setAttribute("aria-label", drfold.title);
    drfold.setAttribute("aria-controls", "drsheet");
    var drFoldWait = doc.createElement("span");
    drFoldWait.className = "dr-wait";
    drFoldWait.title = wsT("js.drawer.waiting");
    drFoldWait.appendChild(gIcon("lock"));
    drfold.appendChild(drFoldWait);
    drfold.appendChild(luIcon("chevron-up"));
    drhandle.parentNode.insertBefore(drfold, drhandle);
    drfold.addEventListener("click", drFold);
    drWant = load(DR_OPEN) === "1";
    drBooting = true;
    drawerSync();
    drBooting = false;
    drhandle.addEventListener("click", function () {
      drSetOpen(!(drOpen && drMode === "panels"), "panels");
    });
    shandle.addEventListener("click", function () {
      drSetOpen(!(drOpen && drMode === "search"), "search");
    });
    // A card's title bar is not a toggle: every card stays open.
    drbody.addEventListener("click", function (e) {
      if (e.target.closest && e.target.closest("details.panel > summary")) e.preventDefault();
    });
    // A store drawer opened: this one closes, and a saved open state not
    // applied yet (no panel present) is dropped, so it never opens over it.
    doc.addEventListener("tk:drawer-open", function () {
      if (drOpen) drSetOpen(false);
      else drWant = false;
    });
    // Escape closes the drawer after any open menu (link menu, gear menu)
    // has had it, and never while the composer has focus: there Escape
    // starts the composer's Escape-then-Tab way out. Capture runs this
    // before the menus' own listeners close them.
    doc.addEventListener("keydown", function (e) {
      if (e.key !== "Escape" || !drOpen || e.defaultPrevented) return;
      if (lmenu || (gmenu && !gmenu.hidden)) return;
      if (ctext && (doc.activeElement === ctext || ctext.wsc && doc.activeElement === ctext.wsc.editor)) return;
      if (drMode === "search" && sbody.contains(doc.activeElement)) return;
      drSetOpen(false);
    }, true);
    // A click outside the drawer closes it; the link menu a drawer link
    // opens counts as inside.
    doc.addEventListener("click", function (e) {
      var t = e.target;
      if (!drOpen || !t || !t.closest) return;
      if (t.closest("#drclip, #drhandle, #shandle, #drfold, .lmenu, .gwin-back")) return;
      drSetOpen(false);
    }, true);
    // The header can wrap or change height (a chip appears): the sheet
    // follows its bottom edge, and composer growth keeps the clearance.
    window.addEventListener("resize", function () { drApply(false); });
    if (window.ResizeObserver) {
      var drGeometry = new ResizeObserver(function () { drApply(false); });
      if (drheader) drGeometry.observe(drheader);
      if ($("composer")) drGeometry.observe($("composer"));
    }
    // The handles' widths follow their contents; the header pads clear of
    // them. Padding changes the shallower header's observed content box:
    // write it next frame, not during this native resize delivery.
    if (window.ResizeObserver) {
      var drRoomFrame = 0;
      var drRo = new ResizeObserver(function () {
        if (drRoomFrame) return;
        drRoomFrame = requestAnimationFrame(function () {
          drRoomFrame = 0;
          if (drawer.isConnected) drRoom();
        });
      });
      drRo.observe(drhandle);
    }
    // The panels grid's column count follows the body's width: the last
    // row's fill is laid out again (drFill).
    if (window.ResizeObserver) new ResizeObserver(drFill).observe(drbody);
    // The board-first page scrolls the document, and the header with it: the
    // sheet follows the header's bottom (the live layout never scrolls, so
    // this never fires there). Once a frame, and only when the bottom moved,
    // so a click's animation is not cut short.
    var drScrollFrame = 0;
    window.addEventListener("scroll", function () {
      if (drScrollFrame || !drHas) return;
      drScrollFrame = requestAnimationFrame(function () {
        drScrollFrame = 0;
        if (drHeaderBottom() !== drTop) drApply(false);
      });
    }, { passive: true });
    drOpen = drHas && drWant;
    if (drOpen || typeof requestAnimationFrame !== "function") {
      drApply(false);
    } else {
      drawer.classList.toggle("open", false);
      ["panels", "search"].forEach(function (mode) { drawer.classList.toggle(mode, drMode === mode); });
      drbody.hidden = drMode === "search";
      if (sbody) sbody.hidden = drMode !== "search";
      drclip.style.height = "0px";
      if (shandle) shandle.setAttribute("aria-expanded", "false");
      drhandle.setAttribute("aria-expanded", "false");
      drFoldShow("");
      drTitle();
      requestAnimationFrame(function () { if (drawer.isConnected) drApply(false); });
    }
  }

