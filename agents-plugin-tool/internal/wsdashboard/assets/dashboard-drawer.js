  // --- drawer: the quick-search sheet pulled down from the top --------------
  // A frame over the page that shows the search sheet (#sbody), opened by
  // its handle (#shandle) or Ctrl+K. The side panels live in the gallery's
  // Workpanel (dashboard-workpanel.js), repository state on its Git page
  // (dashboard-gitpage.js), and the session holders in the shell's
  // bottom-bar Holders popup (dashboard-holders.js), so the frame carries no
  // cards of its own. drMode stays "search": the tracker and the frame
  // messages read it. Open state is this browser's (ws.dashboard.drawer.search);
  // height always fills the available room.
  //
  // The sheet fills from the header bottom to above the composer (or the
  // viewport bottom without one), as the tracker's store sheets do. While it
  // shows, the fold button (#drfold, built below) replaces the handle at the
  // top and closes it.
  var drawer = $("drawer"), drclip = $("drclip"), drsheet = $("drsheet");
  var drheader = doc.querySelector(".dwrap > .top"), drin = drawer && drawer.querySelector(".dr-in");
  var DR_SEARCH = "ws.dashboard.drawer.search";
  var drRoomW = -1, drOpen = false, drTop = 0;
  var drMode = "search", drShown = "", drfold = null;
  var sbody = null, shandle = null;
  if (drawer) {
    sbody = doc.createElement("div"); sbody.id = "sbody"; sbody.className = "dr-search"; sbody.hidden = true;
    drsheet.appendChild(sbody);
    shandle = doc.createElement("button"); shandle.type = "button"; shandle.id = "shandle"; shandle.className = "dr-handle dr-search-handle";
    shandle.title = wsT("js.tk.qs.tip") + " (Ctrl+K)"; shandle.setAttribute("aria-label", shandle.title); shandle.setAttribute("aria-controls", "drsheet");
    shandle.appendChild(luIcon("search")); drin.appendChild(shandle);
  }

  // drRoom keeps the header and the tracker's handle row clear of the
  // search handle: --drhw is its width (the header's padding and the tracker
  // row read it), --shr its offset from the right edge (0: nothing hangs
  // right of it).
  function drRoom() {
    var sw = shandle ? Math.ceil(shandle.getBoundingClientRect().width) : 0;
    if (drRoom._shr !== 0) {
      drRoom._shr = 0;
      doc.documentElement.style.setProperty("--shr", "0px");
    }
    if (sw !== drRoomW) {
      drRoomW = sw;
      doc.documentElement.style.setProperty("--drhw", sw + "px");
    }
    if (drheader) drheader.classList.toggle("dr-room", sw > 0);
  }
  // drFoldShow shows the fold button in the search handle's slot, or hides
  // it (mode "").
  function drFoldShow(mode) {
    if (!drfold) return;
    drfold.hidden = !mode;
    drfold.classList.toggle("search", mode === "search");
  }
  // drTitle labels the fold button.
  function drTitle() {
    if (!drfold) return;
    var f = wsT("js.drawer.fold");
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
    if (visible(c)) m = Math.min(m, Math.round(c.getBoundingClientRect().top) - top - (shandle ? shandle.offsetHeight : 0) - 6);
    return Math.max(0, m);
  }
  // drApply lays the drawer out for its state; anim animates a click's
  // toggle only.
  function drApply(anim) {
    if (!drawer) return;
    drTop = drHeaderBottom();
    var h = drMax();
    drawer.style.setProperty("--drtop", drTop + "px");
    drawer.classList.toggle("anim", !!anim);
    drawer.classList.toggle("open", drOpen);
    drawer.classList.toggle("search", true);
    if (sbody) sbody.hidden = false;
    drsheet.style.height = h + "px";
    drclip.style.height = (drOpen ? h : 0) + "px";
    if (shandle) shandle.setAttribute("aria-expanded", String(drOpen));
    drFoldShow(drOpen ? drMode : "");
    drTitle();
    var shown = drOpen ? drMode : "";
    if (shown !== drShown) {
      drShown = shown;
      if (tkApi) { if (shown === "search") tkApi.openSearch(); else tkApi.closeSearch(); }
    }
  }
  // The session drawer and a tracker store's drawer never show at once:
  // opening this one tells the tracker (which collapses its open store), and
  // the tracker opening a store closes this one (tk:drawer-open, below).
  function drAnnounce() { doc.dispatchEvent(new CustomEvent("ws:drawer-open")); }
  // drSetOpen opens the frame on the search sheet, or closes it. mode is
  // kept for its callers; "search" is the only sheet.
  function drSetOpen(open, mode) {
    if (open && (!shandle || (mode && mode !== "search"))) return;
    var kb = open && doc.activeElement === shandle;
    if (open && !drOpen) drAnnounce();
    if (!open && drsheet.contains(doc.activeElement) && shandle) shandle.focus();
    drOpen = open;
    save(DR_SEARCH, open ? "1" : null);
    drApply(true);
    // The focused handle hides while its sheet shows: focus moves to the fold
    // button in its slot instead of falling to the body, only while it is
    // still on that handle (opening search focuses its input in drApply).
    if (kb && drfold && doc.activeElement === shandle) drfold.focus();
  }
  // drFold is a click on the fold button: the sheet closes as a click on its
  // handle closes it, and keyboard focus goes to that handle, since the
  // button hides.
  function drFold() {
    if (!drOpen) return;
    var kb = doc.activeElement === drfold;
    drSetOpen(false);
    if (kb && shandle) shandle.focus();
  }
  if (drawer) {
    // The fold button replaces the search handle while the sheet shows.
    drfold = doc.createElement("button");
    drfold.type = "button";
    drfold.className = "dr-fold";
    drfold.id = "drfold";
    drfold.hidden = true;
    drfold.title = wsT("js.drawer.fold");
    drfold.setAttribute("aria-label", drfold.title);
    drfold.setAttribute("aria-controls", "drsheet");
    drfold.appendChild(luIcon("chevron-up"));
    drin.insertBefore(drfold, shandle);
    drfold.addEventListener("click", drFold);
    shandle.addEventListener("click", function () { drSetOpen(!drOpen, "search"); });
    // A store drawer opened: this one closes.
    doc.addEventListener("tk:drawer-open", function () {
      if (drOpen) drSetOpen(false);
    });
    // Escape closes the drawer after any open menu (link menu, gear menu)
    // has had it, and never while the composer has focus: there Escape
    // starts the composer's Escape-then-Tab way out. Capture runs this
    // before the menus' own listeners close them.
    doc.addEventListener("keydown", function (e) {
      if (e.key !== "Escape" || !drOpen || e.defaultPrevented) return;
      if (lmenu || (gmenu && !gmenu.hidden)) return;
      if (ctext && (doc.activeElement === ctext || ctext.wsc && doc.activeElement === ctext.wsc.editor)) return;
      if (sbody.contains(doc.activeElement)) return;
      drSetOpen(false);
    }, true);
    // A click outside the drawer closes it; the link menu a drawer link
    // opens counts as inside.
    doc.addEventListener("click", function (e) {
      var t = e.target;
      if (!drOpen || !t || !t.closest) return;
      if (t.closest("#drclip, #shandle, #drfold, .lmenu, .gwin-back")) return;
      drSetOpen(false);
    }, true);
    // The header can wrap or change height (a chip appears): the sheet
    // follows its bottom edge, and composer growth keeps the clearance.
    window.addEventListener("resize", function () { drApply(false); });
    if (window.ResizeObserver) {
      var drGeoFrame = 0;
      var drGeometry = new ResizeObserver(function () {
        if (typeof requestAnimationFrame !== "function") { drApply(false); return; }
        if (drGeoFrame) return;
        drGeoFrame = requestAnimationFrame(function () {
          drGeoFrame = 0;
          if (drawer.isConnected) drApply(false);
        });
      });
      if (drheader) drGeometry.observe(drheader);
      if ($("composer")) drGeometry.observe($("composer"));
    }
    // The handle's width follows its contents; the header pads clear of it.
    // Padding changes the shallower header's observed content box: write it
    // next frame, not during this native resize delivery.
    if (window.ResizeObserver) {
      var drRoomFrame = 0;
      var drRo = new ResizeObserver(function () {
        if (drRoomFrame) return;
        drRoomFrame = requestAnimationFrame(function () {
          drRoomFrame = 0;
          if (drawer.isConnected) drRoom();
        });
      });
      drRo.observe(shandle);
    }
    // The board-first page scrolls the document, and the header with it: the
    // open sheet follows the header's bottom (the live layout never scrolls,
    // so this never fires there). Once a frame, and only when the bottom
    // moved, so a click's animation is not cut short.
    var drScrollFrame = 0;
    window.addEventListener("scroll", function () {
      if (drScrollFrame || !drOpen) return;
      drScrollFrame = requestAnimationFrame(function () {
        drScrollFrame = 0;
        if (drHeaderBottom() !== drTop) drApply(false);
      });
    }, { passive: true });
    drawer.classList.toggle("open", false);
    drawer.classList.toggle("search", true);
    sbody.hidden = false;
    drclip.style.height = "0px";
    shandle.setAttribute("aria-expanded", "false");
    drFoldShow("");
    drTitle();
    drRoom();
    if (typeof requestAnimationFrame === "function") requestAnimationFrame(function () { if (drawer.isConnected) drApply(false); });
    else drApply(false);
  }

