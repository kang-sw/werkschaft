  // --- the Git page: the Worktrees popover and the working-tree row ---------
  // The strip's Worktrees button opens a popover (not modal) holding the
  // pooled worktrees (wtLoad / wtDraw in dashboard-git.js, gpScan in
  // dashboard-gitpage.js). The page showing reads no pool: each opening
  // reads it cache-only (?no_size=1, never a disk walk) unless a full scan
  // runs, which keeps the list and the badge current itself. The popover
  // closes on an outside press, the button again, Escape (heard after the
  // commit window's own capture), leaving the page, and a root that is no
  // repository; focus inside goes back to the button.
  //
  // The graph's working-tree row: while the last status read (gitLast) is
  // dirty, glDraw (dashboard-git.js) draws one row above HEAD from
  // glWtRow; it opens the commit window on the working tree, whose Refresh
  // button reads the status again in place. A status read compares a
  // compact signature: unchanged draws nothing, changed counts patch the row
  // in place, and only a clean/dirty change draws the graph again.
  var gpWt = { btn: $("gpwtbtn"), pop: $("gpwtpop") }, glWtKey = "";
  function gpWtOpen() { return !!gpWt && !!gpWt.pop && !gpWt.pop.hidden; }
  function gpWtShow(on) {
    var b = gpWt && gpWt.btn, p = gpWt && gpWt.pop;
    if (!b || !p || on === gpWtOpen()) return;
    var inside = p.contains(doc.activeElement);
    p.hidden = !on;
    b.setAttribute("aria-expanded", String(on));
    if (!on) {
      doc.removeEventListener("keydown", gpWtKey, true);
      doc.removeEventListener("mousedown", gpWtOut, true);
      if (inside) b.focus();
      return;
    }
    doc.addEventListener("keydown", gpWtKey, true);
    doc.addEventListener("mousedown", gpWtOut, true);
    // The page clips nothing below it: the popover ends above the page's foot.
    if (gpRoot) p.style.maxHeight = Math.max(160, Math.min(440, gpRoot.getBoundingClientRect().bottom - p.getBoundingClientRect().top - 8)) + "px";
    if (wtlist && !gpScanning) wtLoad(false, function (pool) { if (pool) gpWtMode(pool); });
  }
  function gpWtKey(e) {
    if (e.key !== "Escape") return;
    e.preventDefault();
    e.stopPropagation();
    gpWtShow(false);
  }
  function gpWtOut(e) {
    var t = e.target;
    if (!gpWt.pop.contains(t) && !gpWt.btn.contains(t)) gpWtShow(false);
  }
  if (gpWt.btn && gpWt.pop) gpWt.btn.addEventListener("click", function () { gpWtShow(!gpWtOpen()); });

  // glWtCounts sums a status read's groups: files per group, the line delta,
  // and whether the server cut a group (300 files at most per group).
  function glWtCounts(v) {
    var c = { s: 0, u: 0, t: 0, n: 0, add: 0, del: 0, cut: !!(v && v.truncated) };
    [["staged", "s"], ["unstaged", "u"], ["untracked", "t"]].forEach(function (g) {
      ((v && v[g[0]]) || []).forEach(function (f) {
        c[g[1]]++;
        if (f.add > 0) c.add += f.add;
        if (f.del > 0) c.del += f.del;
      });
    });
    c.n = c.s + c.u + c.t;
    return c;
  }
  function glWtSig(c) { return c.n ? [c.s, c.u, c.t, c.add, c.del, c.cut ? 1 : 0].join("|") : ""; }
  // glWtNum is a count, "300+" for a group the server cut.
  function glWtNum(n, cut) { return cut && n >= 300 ? n + "+" : String(n); }
  // glWtSync follows a status read into the graph.
  function glWtSync(v) {
    var c = glWtCounts(v), sig = glWtSig(c), was = glWtKey;
    if (sig === was) return;
    glWtKey = sig;
    var row = was && sig && glrows ? glrows.querySelector(".glr[data-worktree]") : null;
    if (row) glWtFill(row, c);
    else if (glrows) glDraw();
  }
  // glWtRow is the working tree's row: empty author, age, and hash cells
  // keep the graph's columns.
  function glWtRow(c) {
    var row = mk("div", "glr glwt" + (gw.worktree && gw.back && !gw.back.hidden ? " sel" : ""));
    row.setAttribute("data-worktree", "1");
    row.setAttribute("role", "button");
    row.tabIndex = 0;
    row.appendChild(mk("span"));
    row.appendChild(mk("span", "gls"));
    ["gla", "glt", "glh"].forEach(function (k) { row.appendChild(mk("span", k)); });
    glWtFill(row, c);
    return row;
  }
  // glWtFill writes the counts into the row in place: the label, the total,
  // a chip per group, and the breakdown in its name and tooltip.
  function glWtFill(row, c) {
    var sub = row.querySelector(".gls"), total = c.cut ? c.n + "+" : String(c.n);
    var label = wsT("js.git.wt.aria", total, glWtNum(c.s, c.cut), glWtNum(c.u, c.cut), glWtNum(c.t, c.cut));
    sub.textContent = "";
    sub.appendChild(mk("span", "glwt-k", wsT("js.git.wt.row")));
    sub.appendChild(mk("span", "chip glwt-n", total));
    [["s", "js.git.staged"], ["u", "js.git.unstaged"], ["t", "js.git.untracked"]].forEach(function (g) {
      if (c[g[0]]) sub.appendChild(mk("span", "chip glwt-g", wsT(g[1]) + " " + glWtNum(c[g[0]], c.cut)));
    });
    row.setAttribute("aria-label", label);
    row.title = label + "\n" + wsT("js.git.wt.lines", c.add, c.del) + (c.cut ? "\n" + wsT("js.git.truncated", 300) : "");
  }
  // glWtOpen opens the commit window on the first group with files.
  function glWtOpen() {
    var c = glWtCounts(gitLast);
    gwWorktree(c.s ? "staged" : c.u ? "unstaged" : "untracked", 0);
  }

  // gwRefreshBtn is the commit window's Refresh button (worktree mode only).
  function gwRefreshBtn() {
    var b = mk("button", "ia gw-refresh");
    b.type = "button";
    b.title = wsT("js.git.win.refresh");
    b.setAttribute("aria-label", b.title);
    b.appendChild(luIcon("refresh-cw"));
    b.hidden = true;
    b.addEventListener("click", gwRefresh);
    return b;
  }
  // gwRefresh reads the working tree again and fills the window in place,
  // unless it closed or turned to a commit meanwhile: the file in view stays
  // in view while it is listed, and focus stays on the button (never
  // disabled while busy: a disabled button drops its focus). A failed read
  // says so in the window and keeps the list.
  function gwRefresh() {
    var b = gw.refresh, seq = gw.seq;
    if (b.classList.contains("busy")) return;
    b.classList.add("busy");
    b.setAttribute("aria-busy", "true");
    b.setAttribute("aria-disabled", "true");
    gitLoad(function (ok, err) {
      b.classList.remove("busy");
      b.removeAttribute("aria-busy");
      b.removeAttribute("aria-disabled");
      if (!gw.worktree || seq !== gw.seq) return;
      q(".gw-rfail", gw.files).forEach(function (n) { n.remove(); });
      if (!ok) { gw.files.appendChild(gwNote(wsT("js.git.win.refreshfail", err), true)).classList.add("gw-rfail"); return; }
      var on = gw.files.querySelector(".gwf.on"), f = on && gw.list[+on.getAttribute("data-i")];
      var sec = on && q('.dfile[data-i="' + on.getAttribute("data-i") + '"]', gw.diff)[0];
      var off = sec ? gw.diff.scrollTop - sec.offsetTop : 0, url = f ? f.url : "";
      gwWorktree("", 0, true);
      var at = -1;
      gw.list.forEach(function (g, i) { if (at < 0 && url && g.url === url) at = i; });
      var now = at >= 0 && q('.dfile[data-i="' + at + '"]', gw.diff)[0];
      if (now) { gw.diff.scrollTop = now.offsetTop + off; gwSpy(); }
    });
  }
