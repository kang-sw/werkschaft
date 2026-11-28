  // --- the Git page: #bgal's Git system page --------------------------------
  // The page (#gitpage, rendered with the session page) moves into the
  // gallery's Git page host (#gitdock, cards.js) and drives the git helpers
  // in dashboard-git.js: the action strip and the commit graph, the page's
  // one full-width panel at every width, which shows the working tree's
  // changes as one row above HEAD while the tree is dirty. The pooled
  // worktrees live in the strip's Worktrees popover
  // (dashboard-gitpage-wt.js). Nothing reads while the page is hidden: each
  // showing asks the daemon to read the git badge again, reads the changed
  // files, the graph, and the review ledger; the pool is read (cache-only,
  // no disk walk) only when the popover opens. The git region (#gitdata,
  // the git badge's stream) patches the branch line by key; the review
  // ledger is read only on a showing and after an action, never per tick.
  // Leaving the page trims the graph back to its first page.
  var GP_PAGES = 10;
  var gpE = {
    strip: $("gpref"), pill: $("gppill"), pilltx: $("gppilltx"), fmini: $("gpfmini"), fminitx: $("gpfminitx"),
    mode: $("gpwtmode"), modetx: $("gpwtmodetx"), scan: $("gpscan"), scansub: $("gpscansub"), scantag: $("gpscantag"),
    empty: $("gpempty"), toast: $("gptoast")
  };
  var gpHost = null, gpVis = false, gpRefData = {}, gpLedger = null, gpLedgerSeq = 0;
  var gpScanning = false, gpToastTimer = 0, gpStatusTimer = 0, gpCarrierKey = "";
  var gpScanSub = gpE.scansub ? gpE.scansub.textContent : "";
  function gpDockHost() {
    if (!gpHost || !gpHost.isConnected) gpHost = doc.getElementById("gitdock");
    return gpHost;
  }
  // gpShown tells whether the gallery shows the Git page now.
  function gpShown() {
    var C = window.WsCards, g = C && C.gallery && C.gallery();
    return !!gpRoot && !!gpDockHost() && !!g && !g.hidden && !!C.activeSys && C.activeSys() === "git";
  }
  function gpMount() {
    var h = gpDockHost();
    if (!gpRoot || !h) return false;
    if (gpRoot.parentNode !== h) h.appendChild(gpRoot);
    gpRoot.hidden = false;
    return true;
  }
  // gpSync is the gallery's change listener: the first showing loads the
  // page, a later one reads a graph that had no layout yet; leaving trims
  // the graph and closes the Worktrees popover.
  function gpSync() {
    if (!gpShown()) {
      if (gpVis) { gpVis = false; glTrim(); gpToastHide(); }
      gpWtShow(false);
      return;
    }
    gpMount();
    if (gpVis) {
      if (!gl.loaded && glog && glog.offsetWidth) glLoad(false);
      return;
    }
    gpVis = true;
    gpActivate();
  }
  function gpNoRepo() { return !!gpRoot && gpRoot.hasAttribute("data-norepo"); }
  function gpActivate() {
    gpSwapped();
    if (gpNoRepo()) return;
    gitOpened();
    gpLedgerLoad();
  }

  // --- the branch line ------------------------------------------------------
  // gpSwapped reads the git region (#gitdata) after a stream patch: the
  // branch line and the changed-file count follow it by key, and a change
  // while the page shows reads the changed files again. A session with no
  // git read (no project root) shows the no-repository state too.
  function gpSwapped() {
    var d = $("gitdata");
    if (!gpRoot) return;
    var st = d ? d.getAttribute("data-state") || "" : "norepo";
    gpRoot.toggleAttribute("data-norepo", st === "norepo");
    if (gpE.empty) gpE.empty.hidden = st !== "norepo";
    if (st === "norepo") gpWtShow(false);
    if (st === "ok") {
      gpRefData.branch = d.getAttribute("data-branch") || "";
      gpRefData.detached = d.hasAttribute("data-detached");
      gpRefData.short = d.getAttribute("data-short") || gpRefData.branch;
      gpRefData.goal = d.hasAttribute("data-goal");
      gpRefData.impl = d.hasAttribute("data-impl");
      gpRefData.upstream = d.getAttribute("data-upstream") || "";
      gpRefData.ahead = +d.getAttribute("data-ahead") || 0;
      gpRefData.behind = +d.getAttribute("data-behind") || 0;
      gpRefData.err = "";
      var n = (+d.getAttribute("data-staged") || 0) + (+d.getAttribute("data-modified") || 0) + (+d.getAttribute("data-untracked") || 0);
      var key = [gpRefData.branch, gpRefData.ahead, gpRefData.behind, n].join("|");
      if (gpVis && gpCarrierKey && key !== gpCarrierKey) gpStatusSoon();
      gpCarrierKey = key;
    } else {
      gpRefData.err = st === "failed" ? d.getAttribute("data-err") || "" : "";
    }
    gpRefDraw();
  }
  // gpStatusSoon reads the changed files once a burst of stream patches
  // settles.
  function gpStatusSoon() {
    clearTimeout(gpStatusTimer);
    gpStatusTimer = setTimeout(function () { if (gpVis) gitLoad(); }, 300);
  }
  // gpRef takes the branch standing of a changed-files read (git/status).
  function gpRef(v) {
    if (!v) return;
    if (v.branch && v.branch !== gpRefData.branch) { gpRefData.short = v.branch; gpRefData.goal = gpRefData.impl = false; }
    gpRefData.branch = v.branch || gpRefData.branch || "";
    gpRefData.detached = !!v.detached;
    gpRefData.upstream = v.upstream || "";
    gpRefData.ahead = v.ahead || 0;
    gpRefData.behind = v.behind || 0;
    gpRefDraw();
  }
  function gpRefDraw() {
    var box = gpE.strip, r = gpRefData;
    if (!box) return;
    box.textContent = "";
    if (r.err) {
      var f = mk("span", "gp-br gp-br-fail", wsT("js.gp.ref.failed"));
      f.title = r.err;
      box.appendChild(f);
      return;
    }
    if (!r.branch) return;
    var br = mk("span", "gp-br"), mark = r.goal || r.impl ? mk("span", r.goal ? "gh-goal" : "gh-impl") : null;
    if (mark) { mark.appendChild(gIcon(r.goal ? "trophy" : "hammer")); br.appendChild(mark); } else br.appendChild(gIcon("branch"));
    br.appendChild(mk("b", "", r.detached ? wsT("js.git.detached", r.branch) : r.short || r.branch));
    br.title = r.detached ? wsT("js.git.detached", r.branch) : r.branch;
    box.appendChild(br);
    var up = mk("span", "gp-up");
    if (!r.upstream) {
      if (!r.detached) up.appendChild(mk("span", "gp-up-ref", wsT("js.git.noupstream")));
    } else {
      up.appendChild(mk("span", "gp-up-ref", r.upstream));
      up.appendChild(mk("b", "gp-ahead" + (r.ahead ? "" : " gp-zero"), "↑" + r.ahead));
      up.appendChild(mk("b", "gp-behind" + (r.behind ? "" : " gp-zero"), "↓" + r.behind));
      up.title = wsT("js.git.ref.upstream", r.upstream) + " " + wsT("js.git.ref.ab", r.ahead, r.behind);
    }
    if (up.firstChild) box.appendChild(up);
  }

  // --- worktrees: the list's mode and the full scan ---------------------------
  // gpWtMode marks the list cache-only, or measured when every size is.
  function gpWtMode(pool) {
    if (!gpE.mode) return;
    var list = pool.worktrees || [], sized = list.length > 0 && list.every(function (w) { return w.size != null; });
    gpE.mode.setAttribute("data-mode", sized ? "scanned" : "light");
    gpE.modetx.textContent = wsT(sized ? "js.gp.wt.mode.sized" : "js.gp.wt.mode.light");
  }
  function gpScan() {
    if (gpScanning || !gpE.scan) return;
    gpScanning = true;
    gpE.scan.disabled = true;
    gpE.scan.setAttribute("data-scan", "busy");
    gpE.scan.setAttribute("aria-busy", "true");
    gpE.scansub.textContent = wsT("js.gp.wt.scanning");
    gpE.scantag.textContent = "…";
    if (gpE.mode) {
      gpE.mode.setAttribute("data-mode", "scanning");
      gpE.modetx.textContent = wsT("js.gp.wt.mode.scanning");
    }
    // The rows not yet measured show the scan at once, not after its first answer.
    if (wtLast) wtDraw(wtLast, true);
    wtLoad(true, function (pool) {
      gpScanning = false;
      gpE.scan.disabled = false;
      gpE.scan.removeAttribute("aria-busy");
      if (!pool) {
        gpE.scan.removeAttribute("data-scan");
        gpE.scansub.textContent = gpScanSub;
        gpE.scantag.textContent = "WalkDir";
        if (gpE.mode) { gpE.mode.setAttribute("data-mode", "light"); gpE.modetx.textContent = wsT("js.gp.wt.mode.light"); }
        return;
      }
      var total = 0;
      (pool.worktrees || []).forEach(function (w) { if (w.size > 0) total += w.size; });
      gpE.scan.setAttribute("data-scan", "done");
      gpE.scansub.textContent = wsT("js.gp.wt.scanned", new Date().toLocaleTimeString(wsI18n.lang, { hour: "2-digit", minute: "2-digit" }));
      gpE.scantag.textContent = wtSize(total);
      gpWtMode(pool);
      if (wtnote && !wtnote.classList.contains("gp-ok")) wtnote.textContent = wsT("js.gp.wt.cachenote");
    });
  }
  if (gpE.scan) gpE.scan.addEventListener("click", gpScan);

  // --- the review ledger pill and the frontier jump ---------------------------
  // gpLedgerLoad reads the review ledger's standing (GET review-ledger, a
  // read-only route): the pill, and the frontier the graph marks.
  function gpLedgerLoad() {
    if (!gpE.pill || !window.fetch) return;
    var seq = ++gpLedgerSeq;
    fetch("/s/" + rid + "/review-ledger", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      return r.text().then(function (s) {
        if (seq !== gpLedgerSeq) return;
        var v = null;
        try { v = r.ok ? JSON.parse(s) : null; } catch (e) { v = null; }
        gpLedgerDraw(v, v ? "" : s.trim() || "HTTP " + r.status);
      });
    }, function () { if (seq === gpLedgerSeq) gpLedgerDraw(null, wsT("js.post.unreachable")); });
  }
  function gpLedgerHead(v) {
    return v && (v.state === "ok" || v.state === "warn") && v.frontier && v.frontier.head ? v.frontier.head : "";
  }
  function gpLedgerDraw(v, err) {
    var pill = gpE.pill, tx = gpE.pilltx, head = gpLedgerHead(v), num = function (n) { return (n || 0).toLocaleString(wsI18n.lang); };
    gpLedger = v;
    tx.textContent = "";
    var state = v ? v.state : "fail";
    pill.setAttribute("data-state", state);
    pill.disabled = !head;
    if (!v) {
      tx.textContent = wsT("js.gp.ledger.fail");
      pill.title = err || "";
    } else if (state === "missing" || state === "norepo") {
      tx.textContent = wsT(state === "missing" ? "js.gp.ledger.missing" : "js.gp.ledger.norepo");
      pill.title = wsT(state === "missing" ? "js.gp.ledger.missing.title" : "js.gp.ledger.norepo.title");
    } else {
      tx.appendChild(mk("b", "gp-pill-k", wsT("js.gp.ledger.review")));
      if (v.legacy_mode) tx.appendChild(mk("span", "gp-pill-stat", wsTn("js.gp.ledger.commits", v.legacy_commits_behind || 0)));
      else {
        tx.appendChild(mk("span", "gp-pill-stat", wsTn("js.gp.ledger.merges", v.merges_behind || 0)));
        tx.appendChild(mk("span", "gp-pill-lines", wsTn("js.gp.ledger.lines", v.lines_behind || 0, num(v.lines_behind))));
      }
      if (v.frontier && v.frontier.verdict) tx.appendChild(mk("span", "gp-pill-v", v.frontier.verdict + " @"));
      tx.appendChild(mk("code", "gp-pill-sha", head.slice(0, 7)));
      var title = v.legacy_mode
        ? wsT("js.gp.ledger.title.legacy", num(v.legacy_commits_behind), num(v.legacy_threshold))
        : wsT("js.gp.ledger.title", num(v.merges_behind), num(v.merges_threshold), num(v.lines_behind), num(v.lines_threshold));
      if (v.track) title = wsT("js.gp.ledger.track", v.track) + "\n" + title;
      if (v.nudge && v.nudge_text) title += "\n" + v.nudge_text;
      pill.title = title + "\n" + wsT("js.gp.frontier.jump");
    }
    if (gpE.fmini) {
      gpE.fmini.hidden = !head;
      gpE.fminitx.textContent = head ? wsT("js.gp.frontier.mini", head.slice(0, 7)) : "";
    }
    if (head !== gl.frontier) {
      gl.frontier = head;
      if (gl.commits.length) glDraw();
    }
  }
  function gpReduced() { return !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches); }
  // gpJump brings the frontier commit into the graph: the graph reads
  // further pages until the commit shows (GP_PAGES at most), then the row scrolls into view and
  // flashes. A frontier past the walk, or a failed read, says so.
  function gpJump() {
    var head = gpLedgerHead(gpLedger);
    if (!head) { gpToast(wsT("js.gp.frontier.none")); return; }
    gl.frontier = head;
    function land(i, msg) {
      if (i < 0) { gpToast(msg ? wsT("js.gp.frontier.fail", msg) : wsT("js.gp.frontier.notfound", head.slice(0, 7))); return; }
      var row = glRowEl(i);
      if (!row) return;
      var rr = row.getBoundingClientRect(), sr = glscroll.getBoundingClientRect();
      var top = glscroll.scrollTop + rr.top - sr.top - (glscroll.clientHeight - rr.height) / 2;
      glscroll.scrollTo({ top: Math.max(0, top), behavior: gpReduced() ? "auto" : "smooth" });
      row.classList.remove("gp-spot");
      void row.offsetWidth;
      row.classList.add("gp-spot");
      row.addEventListener("animationend", function () { row.classList.remove("gp-spot"); }, { once: true });
      try { row.focus({ preventScroll: true }); } catch (e) { /* detached */ }
    }
    function walk(ok) {
      if (ok === false) return;
      glWalkTo(head, GP_PAGES, land);
    }
    if (!gl.commits.length || !gl.loaded) glLoad(false, walk);
    else walk();
  }
  if (gpE.pill) gpE.pill.addEventListener("click", function () { if (!gpE.pill.disabled) gpJump(); });
  if (gpE.fmini) gpE.fmini.addEventListener("click", gpJump);
  // gpActed follows a fetch, pull, or push: the ledger may have moved.
  function gpActed() { gpLedgerLoad(); }

  // --- toast ------------------------------------------------------------------
  function gpToast(text) {
    var t = gpE.toast;
    if (!t) return;
    clearTimeout(gpToastTimer);
    t.textContent = text;
    t.hidden = true;
    void t.offsetWidth;
    t.hidden = false;
    gpToastTimer = setTimeout(gpToastHide, 2800);
  }
  function gpToastHide() {
    clearTimeout(gpToastTimer);
    if (gpE.toast) gpE.toast.hidden = true;
  }

  // --- opening (Ctrl+G) -------------------------------------------------------
  // gitPage.open shows the Git page and focuses it; a repeat only refocuses
  // (no toggle). The narrow view's gallery off the window (another column
  // tab) brings the board column back first.
  var gitPage = {
    available: function () { return !!gpRoot && !!gpDockHost(); },
    open: function () {
      var C = window.WsCards, h = gpDockHost(), g;
      if (!gpRoot || !h || !C || !C.showSys) return false;
      C.showSys("git");
      g = C.gallery && C.gallery();
      if (g && g.hasAttribute("data-away") && typeof setTab === "function" && grid) setTab("board");
      gpSync();
      if (!h.contains(doc.activeElement)) h.focus({ preventScroll: true });
      return true;
    }
  };
  function gpInit() {
    var C = window.WsCards;
    gpRefDraw();
    if (!gpRoot || !C || !C.onGalleryChange) return;
    C.onGalleryChange(gpSync);
    gpMount();
    gpSwapped();
    gpSync();
  }
  if (typeof Promise === "function") Promise.resolve().then(gpInit);
