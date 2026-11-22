// Exec job HUD, the strip: the open lead's own hosted exec jobs as chips in
// the composer gap, folded by measurement, with a hover tooltip. The daemon
// tells the page only that the job set changed (the opaque `execjobs` stream
// event, a revision); the list itself is read from the localhost-only
// /s/<id>/exec/jobs route, so commands, directories and output never ride the
// stream every viewer shares. A daemon without the route never sends the event
// and a LAN page's read is refused: neither mounts a HUD. The peek, the stop
// flow and the toast are dashboard-exec-hud-peek.js. Contract 2: the exec chips
// are the lowest-priority element of the strip; the interrupt and subagent
// badges keep their width and the group below takes only what is left.
  var xhEnabled = cexecEnabled, xhJobs = [], xhById = Object.create(null), xhAbsent = false, xhFetching = false, xhDirty = false, xhRev = -1, xhSeq = 0;
  var xhStopping = Object.create(null), xhGroup = null, xhMore = null, xhAgg = null, xhSlots = [], xhShown = [], xhFolded = [], xhSig = "", xhTick = 0, xhFadeTimer = 0;
  var xhTip = null, xhTipFor = "", xhRo = null, xhDismissed = Object.create(null), xhDismissKey = "ws.xhud.dismissed." + rid, xhHidden = 0, xhActive = "";
  var XH_PIN_MS = 10000, XH_FADE_MS = 12000, XH_NARROW_PX = 320, XH_GAP = 6, XH_MAX_CHIPS = 12;
  var xhVis = [], xhBuilt = Object.create(null);
  var XH_STATE_KEY = { running: "js.xhud.st.running", stopping: "js.xhud.st.stopping", ok: "js.xhud.st.ok", stopped: "js.xhud.st.stopped", lost: "js.xhud.st.lost", nostart: "js.xhud.st.nostart" };
  var XH_SHORT_KEY = { ok: "js.xhud.sh.ok", stopped: "js.xhud.sh.stopped", lost: "js.xhud.sh.lost", nostart: "js.xhud.sh.nostart", stopping: "js.xhud.sh.stopping" };
  function xhEl(tag, cls, text) {
    var e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  try {
    var xhRaw = JSON.parse(window.sessionStorage.getItem(xhDismissKey) || "[]");
    if (Array.isArray(xhRaw)) xhRaw.forEach(function (id) { if (typeof id === "string") xhDismissed[id] = true; });
  } catch (e) {}
  function xhSaveDismissed() {
    try { window.sessionStorage.setItem(xhDismissKey, JSON.stringify(Object.keys(xhDismissed).slice(-400))); } catch (e) {}
  }
  function xhFmtElapsed(ms) {
    var s = Math.max(0, Math.floor(ms / 1000)), h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60;
    function p(n) { return n < 10 ? "0" + n : "" + n; }
    return h ? h + ":" + p(m) + ":" + p(r) : m + ":" + p(r);
  }
  function xhFmtClock(ms) {
    var d = new Date(ms);
    function p(n) { return n < 10 ? "0" + n : "" + n; }
    return p(d.getHours()) + ":" + p(d.getMinutes()) + ":" + p(d.getSeconds());
  }
  // cmdHead: the command's first word and, when it is not a flag or a quote,
  // its second, after a leading `cd x &&` and environment assignments.
  function xhCmdHead(cmd) {
    var c = String(cmd || "").trim().replace(/^cd\s+[^&;]+(&&|;)\s*/, "").replace(/^([A-Za-z_][A-Za-z0-9_]*=\S+\s+)+/, "");
    var t = c.split(/\s+/), a = (t[0] || "").split("/").pop();
    if (t[1] && t[1].charAt(0) !== "-" && t[1].charAt(0) !== "'" && t[1].charAt(0) !== "\"") a += " " + t[1];
    return a || String(cmd || "").slice(0, 24);
  }
  // The display state of a job: running, stopping (a stop sent, the daemon has
  // not answered), ok, failed (exit status), stopped, lost, or nostart (the
  // process never started).
  function xhSt(j) {
    if (j.state === "running") return xhStopping[j.id] ? "stopping" : "running";
    if (j.state === "exited") return j.exit_code === 0 && j.signal == null ? "ok" : "failed";
    if (j.state === "failed") return "nostart";
    return j.state === "stopped" || j.state === "lost" ? j.state : "failed";
  }
  function xhEnded(j) { var s = xhSt(j); return s !== "running" && s !== "stopping"; }
  // A job that needs a look stays until dismissed: a failure, a lost or never
  // started job, a stop in flight, a user stop whose notice did not land.
  function xhAttn(j) {
    var s = xhSt(j);
    return s === "failed" || s === "lost" || s === "nostart" || s === "stopping" || (s === "stopped" && j.stopped_by_user && j.origin !== "user" && (j.stop_notice === "failed" || j.stop_notice === "closed"));
  }
  function xhEndedAt(j) { return j.ended_at || j.seenEnded || 0; }
  function xhByStartAsc(a, b) { return (a.started_at || 0) - (b.started_at || 0); }
  // visible: what the strip may draw now. Timers count from the job's end, so
  // a job that ended before the page loaded is already past its windows: an ok
  // job is hidden into the "ended N" chip, a failure is shown.
  function xhVisible(now) {
    var vis = [], hid = 0;
    xhJobs.forEach(function (j) {
      if (xhDismissed[j.id]) return;
      if (!xhEnded(j) || xhAttn(j) || now - xhEndedAt(j) < XH_FADE_MS) vis.push(j);
      else hid++;
    });
    xhHidden = hid;
    function rank(j) { return xhAttn(j) || (xhEnded(j) && now - xhEndedAt(j) < XH_PIN_MS) ? 0 : !xhEnded(j) ? 1 : 2; }
    vis.forEach(function (j) { j.rank = rank(j); });
    vis.sort(function (a, b) {
      if (a.rank !== b.rank) return a.rank - b.rank;
      if (a.rank === 1) return xhByStartAsc(a, b);
      return xhEndedAt(b) - xhEndedAt(a);
    });
    return vis;
  }
  function xhOwnerLabel(j) { return j.origin === "user" ? wsT("js.xhud.own.user") : wsT("js.xhud.own.agent"); }
  function xhStateText(j) {
    var s = xhSt(j);
    if (s === "failed") return j.signal != null ? wsT("js.xhud.st.sig", String(j.signal)) : wsT("js.xhud.st.failed", String(j.exit_code == null ? "?" : j.exit_code));
    if (s === "stopped") return j.stopped_by_user ? wsT("js.xhud.st.stopped.user") : wsT("js.xhud.st.stopped");
    return wsT(XH_STATE_KEY[s]);
  }
  function xhTone(j) {
    var s = xhSt(j);
    return s === "running" || s === "stopping" ? "" : s === "ok" ? "ok" : s === "failed" ? "fail" : s === "lost" || s === "nostart" ? "warn" : "mute";
  }
  function xhShortText(j) {
    var s = xhSt(j);
    if (s === "failed") return j.signal != null ? wsT("js.xhud.sh.sig", String(j.signal)) : wsT("js.xhud.sh.failed", String(j.exit_code == null ? "?" : j.exit_code));
    return wsT(XH_SHORT_KEY[s]);
  }
  function xhNode(j) {
    var n = xhEl("span", "xnode");
    n.setAttribute("data-st", xhSt(j));
    n.appendChild(xhEl("span", "xemb" + (j.origin === "user" ? " user" : ""), j.origin === "user" ? "!" : ">_"));
    n.appendChild(xhEl("span", "xring"));
    var st = xhSt(j), bd = null;
    if (st === "ok") bd = ["ok", "✓"]; else if (st === "failed") bd = ["fail", "×"]; else if (st === "lost") bd = ["warn", "?"]; else if (st === "nostart") bd = ["warn", "!"]; else if (st === "stopped") bd = ["mute", "■"];
    if (bd) n.appendChild(xhEl("span", "xbadge " + bd[0], bd[1]));
    return n;
  }
  function xhWorst(list) {
    var w = null;
    list.forEach(function (j) {
      var s = xhSt(j);
      if (s === "failed") w = ["fail", "×"]; else if ((s === "lost" || s === "nostart") && (!w || w[0] !== "fail")) w = ["warn", "?"];
    });
    return w;
  }
  function xhBuildChip(j) {
    var slot = xhEl("span", "xslot");
    slot.setAttribute("data-xid", j.id);
    var st = xhSt(j), ended = xhEnded(j), head = xhCmdHead(j.command);
    var b = xhEl("button", "xchip" + (ended ? " ended" : "") + (xhBuilt[j.id] ? "" : " enter"));
    xhBuilt[j.id] = true;
    b.type = "button";
    b.setAttribute("data-xid", j.id);
    b.setAttribute("data-st", st);
    b.setAttribute("aria-haspopup", "dialog");
    b.setAttribute("aria-expanded", xhPeekOpen() && xhPeekId() === j.id ? "true" : "false");
    b.setAttribute("aria-label", wsT("js.xhud.chip.aria", head, xhStateText(j) + ", " + xhOwnerLabel(j)));
    b.tabIndex = -1;
    b.appendChild(xhNode(j));
    b.appendChild(xhEl("span", "xlab", head));
    if (st === "running") {
      var e = xhEl("span", "xel", xhFmtElapsed(Date.now() - j.started_at));
      e.setAttribute("data-xsince", String(j.started_at));
      b.appendChild(e);
    } else b.appendChild(xhEl("span", "xst " + (xhTone(j) || "mute"), xhShortText(j)));
    slot.appendChild(b);
    var c = xhEl("button", "xstop");
    c.type = "button";
    c.tabIndex = -1;
    c.setAttribute("data-xid", j.id);
    if (ended) {
      c.textContent = "×";
      c.setAttribute("data-xact", "dismiss");
      c.setAttribute("aria-label", wsT("js.xhud.chip.dismiss.aria", head));
      c.title = wsT("js.xhud.dismiss");
    } else {
      c.appendChild(doc.createElement("i"));
      c.setAttribute("data-xact", "stop");
      c.setAttribute("aria-label", wsT("js.xhud.chip.stop.aria", head));
      c.title = wsT("js.xhud.stop.job");
      if (st === "stopping") c.hidden = true;
    }
    slot.appendChild(c);
    return slot;
  }
  function xhGroupEl() {
    if (xhGroup) return xhGroup;
    var wrap = composer.querySelector(".sbwrap");
    if (!wrap) return null;
    xhGroup = xhEl("span", "xgroup fresh");
    xhGroup.setAttribute("role", "toolbar");
    xhGroup.setAttribute("aria-label", wsT("js.xhud.toolbar"));
    xhGroup.hidden = true;
    wrap.insertBefore(xhGroup, wrap.firstChild);
    xhMore = xhEl("button", "xmore");
    xhMore.type = "button";
    xhMore.tabIndex = -1;
    xhMore.setAttribute("data-xkind", "more");
    xhMore.setAttribute("aria-haspopup", "dialog");
    xhAgg = xhEl("button", "xchip xagg");
    xhAgg.type = "button";
    xhAgg.tabIndex = -1;
    xhAgg.setAttribute("data-xkind", "agg");
    xhAgg.setAttribute("aria-haspopup", "dialog");
    xhBindGroup(xhGroup);
    if (window.ResizeObserver) {
      xhRo = new ResizeObserver(function () { xhLayout(); });
      [composer.querySelector(".cbadges"), $("ibadge"), $("sbadge"), $("bgbadge"), $("monbadge")].forEach(function (n) { if (n) xhRo.observe(n); });
    }
    if (doc.fonts && doc.fonts.ready) doc.fonts.ready.then(function () { xhLayout(); });
    return xhGroup;
  }
  // render rebuilds the chips when the visible set or any chip's state changed,
  // else only the elapsed clocks tick. The cost is one pass over the list and
  // the few chips that fit: a job list of 200 draws no 200 nodes.
  function xhRender(force) {
    if (!xhEnabled || xhAbsent) return;
    var t0 = window.performance && performance.now ? performance.now() : 0;
    var now = Date.now(), vis = xhVisible(now), g = xhGroupEl();
    if (!g) return;
    var sig = vis.map(function (j) { return j.id + ":" + xhSt(j) + ":" + (j.stop_notice || "") + ":" + (xhPeekOpen() && xhPeekId() === j.id ? 1 : 0); }).join("|") + "#" + xhHidden;
    var changed = force || sig !== xhSig;
    if (changed) {
      xhSig = sig;
      // The rebuild takes the focused chip with it: focus goes back to the same
      // chip (or the toolbar's first stop), so the keyboard never falls to the page.
      var held = g.contains(doc.activeElement) ? (doc.activeElement.getAttribute("data-xid") || doc.activeElement.getAttribute("data-xkind") || "") : null;
      g.textContent = "";
      xhVis = vis;
      // Only as many chips as can ever fit are built; the rest fold by count.
      xhSlots = vis.slice(0, XH_MAX_CHIPS).map(function (j) { var s = xhBuildChip(j); g.appendChild(s); return { job: j, el: s }; });
      g.appendChild(xhMore);
      g.appendChild(xhAgg);
      xhLayout();
      if (held !== null) {
        var back = (held && g.querySelector('.xchip[data-xid="' + held + '"], [data-xkind="' + held + '"]')) || null;
        if (!back || back.hidden || !back.getClientRects().length) back = xhButtons()[0];
        if (back) back.focus();
      }
    } else {
      xhTickText();
      xhLayout();
    }
    xhSchedule(vis, now);
    xhPeekSync();
    if (window.performance && performance.measure && t0) { try { performance.measure("ws.xhud.render", { start: t0, end: performance.now(), detail: { jobs: xhJobs.length, chips: xhSlots.length, rebuilt: changed } }); } catch (e) {} }
  }
  function xhTickText() {
    q("[data-xsince]", xhGroup).concat(xhTip && !xhTip.hidden ? q("[data-xsince]", xhTip) : [], xhPeekEl && !xhPeekEl.hidden ? q("[data-xsince]", xhPeekEl) : []).forEach(function (n) {
      n.textContent = xhFmtElapsed(Date.now() - Number(n.getAttribute("data-xsince")));
    });
  }
  // schedule: a one-second clock while a chip shows elapsed time, and one timer
  // at the next pin or fade boundary, so an idle page runs neither.
  function xhSchedule(vis, now) {
    var running = vis.some(function (j) { return xhSt(j) === "running"; }) || (xhTip && !xhTip.hidden);
    if (running && !xhTick) xhTick = setInterval(function () { if (!doc.hidden) xhRender(false); }, 1000);
    else if (!running && xhTick) { clearInterval(xhTick); xhTick = 0; }
    clearTimeout(xhFadeTimer);
    var next = 0;
    xhJobs.forEach(function (j) {
      if (!xhEnded(j) || xhDismissed[j.id]) return;
      var at = xhEndedAt(j), cand = [at + XH_PIN_MS, at + XH_FADE_MS];
      cand.forEach(function (c) { if (c > now && (!next || c < next)) next = c; });
    });
    if (next) xhFadeTimer = setTimeout(function () { xhRender(false); }, Math.min(next - now + 30, 30000));
  }
  // layout decides how many chips fit in what is left of the strip. The
  // interrupt badge's slot is reserved even while it is hidden (visibility), and
  // the subagent, background and monitor badges are counted first; the group
  // gets the remainder: all chips, then chips and +N, then one aggregate
  // chip, then the aggregate's node alone, then nothing.
  function xhLayout() {
    if (!xhGroup || !xhMore) return;
    // A fold or fade can hide the chip that holds the keyboard: focus moves to
    // what stands for it instead of falling to the page.
    var held = xhGroup.contains(doc.activeElement) ? doc.activeElement : null;
    xhLayoutNow();
    if (held && (held.closest("[hidden]") || !held.getClientRects().length)) xhFocusStrip("");
  }
  // foldTarget: the visible stand-in for chips that folded away.
  function xhFoldTarget() {
    if (!xhGroup || xhGroup.hidden) return null;
    if (xhMore && !xhMore.hidden) return xhMore;
    if (xhAgg && !xhAgg.hidden) return xhAgg;
    return xhButtons()[0] || null;
  }
  function xhLayoutNow() {
    var list = xhVis;
    xhSlots.forEach(function (s) { s.el.hidden = false; });
    xhAgg.classList.remove("compact");
    // +N and the aggregate are never hidden and shown again within one pass: an
    // element that holds the keyboard loses it the moment it is display:none and
    // a measurement flushes style. Each is set once, at the decision.
    if (!list.length) {
      xhMore.hidden = true;
      if (xhHidden) {
        xhAgg.hidden = false;
        xhAgg.className = "xchip xagg quiet";
        xhAgg.textContent = "";
        var qn = xhEl("span", "xnode");
        qn.appendChild(xhEl("span", "xemb", ">_"));
        xhAgg.appendChild(qn);
        xhAgg.appendChild(xhEl("span", "xst mute", wsT("js.xhud.agg.ended", String(xhHidden))));
        xhAgg.setAttribute("aria-label", wsT("js.xhud.agg.aria", String(xhHidden)));
        xhShown = [];
        xhFolded = xhJobs.filter(function (j) { return !xhDismissed[j.id]; });
        xhGroup.hidden = false;
        xhFit(0, [], true);
      } else {
        xhShown = [];
        xhFolded = [];
        xhAgg.hidden = true;
        xhGroup.hidden = true;
      }
      xhGroup.classList.remove("fresh");
      return;
    }
    xhGroup.hidden = false;
    var cb = composer.querySelector(".cbadges"), ib = $("ibadge");
    var cbw = cb ? cb.clientWidth : composer.clientWidth, ibw = ib ? ib.offsetWidth + 8 : 0, sib = 0, cnt = 0;
    [$("bgbadge"), $("monbadge"), $("sbadge")].forEach(function (x) { if (x && !x.hidden) { sib += Math.max(x.offsetWidth, x.scrollWidth); cnt++; } });
    var avail = cbw - ibw - sib - cnt * XH_GAP;
    var w = xhSlots.map(function (s) { return s.el.offsetWidth; });
    // +N is measured as it will be drawn: the widest count it can show (all
    // jobs, three digits included) with the corner badge a failure adds.
    xhMore.hidden = false;
    xhMore.textContent = "+" + list.length;
    var mw = xhMore.offsetWidth;
    var n = xhSlots.length;
    function total(k) { var sum = 0; for (var i = 0; i < k; i++) sum += w[i] + (i ? XH_GAP : 0); if (k < list.length) sum += (k ? XH_GAP : 0) + mw; return sum; }
    while (n > 1 && total(n) > avail) n--;
    if (n === 1 && total(1) > avail) n = 0;
    if (composer.clientWidth <= XH_NARROW_PX) n = 0;
    xhShown = list.slice(0, n);
    xhFolded = list.slice(n);
    xhSlots.forEach(function (s, i) { s.el.hidden = i >= n; });
    var attn = xhWorst(list), lead = list.filter(xhAttn)[0] || list.filter(function (j) { return !xhEnded(j); })[0] || list[0];
    xhAgg.className = "xchip xagg";
    xhAgg.textContent = "";
    xhAgg.appendChild(xhNode(lead));
    xhAgg.appendChild(xhEl("span", "xn", String(list.length)));
    if (attn) xhAgg.appendChild(xhEl("span", "xst " + attn[0], attn[1]));
    xhAgg.setAttribute("aria-label", wsT("js.xhud.agg.aria", String(list.length)));
    xhMore.hidden = n === 0 || !xhFolded.length;
    xhAgg.hidden = n !== 0;
    if (n === 0) xhFit(avail, list, false);
    else if (xhFolded.length) {
      xhMore.textContent = "+" + xhFolded.length;
      xhMore.setAttribute("aria-label", wsT("js.xhud.more.aria", String(xhFolded.length)));
      var at2 = xhWorst(xhFolded);
      if (at2) xhMore.appendChild(xhEl("span", "xbadge " + at2[0], at2[1]));
    }
    xhGroup.classList.remove("fresh");
    xhRove();
  }
  // fit: the aggregate chip is the last resort; if it does not fit the
  // remainder either, it shrinks to its node, and with no room at all the
  // group hides (the chips stay reachable once the strip widens).
  function xhFit(avail, list, quiet) {
    if (quiet) {
      var cb = composer.querySelector(".cbadges"), ib = $("ibadge"), sib = 0, cnt = 0;
      [$("bgbadge"), $("monbadge"), $("sbadge")].forEach(function (x) { if (x && !x.hidden) { sib += Math.max(x.offsetWidth, x.scrollWidth); cnt++; } });
      avail = (cb ? cb.clientWidth : composer.clientWidth) - (ib ? ib.offsetWidth + 8 : 0) - sib - cnt * XH_GAP;
    }
    if (xhAgg.offsetWidth > avail) {
      xhAgg.classList.add("compact");
      if (xhAgg.offsetWidth > avail) { xhAgg.hidden = true; xhGroup.hidden = true; }
    }
    xhRove();
  }
  // The group is a toolbar: one tab stop (the active chip), arrows move.
  function xhButtons() {
    return xhGroup ? q(".xchip, .xmore", xhGroup).filter(function (b) { return !b.hidden && !b.closest("[hidden]"); }) : [];
  }
  function xhRove() {
    var btns = xhButtons();
    if (!btns.length) return;
    var cur = btns.filter(function (b) { return b === doc.activeElement; })[0] || btns.filter(function (b) { return b.getAttribute("data-xid") === xhActive && xhActive; })[0] || btns[0];
    btns.forEach(function (b) { b.tabIndex = b === cur ? 0 : -1; });
  }
  // --- tooltip -----------------------------------------------------------------
  function xhTipEl() {
    if (xhTip) return xhTip;
    xhTip = xhEl("div", "xtip");
    xhTip.hidden = true;
    xhTip.setAttribute("role", "tooltip");
    xhTip.id = "xtip";
    composer.appendChild(xhTip);
    return xhTip;
  }
  function xhShowTip(kind, id, anchor) {
    var tip = xhTipEl();
    xhTipFor = id || kind;
    tip.textContent = "";
    if (kind === "job") {
      var j = xhById[id];
      if (!j) return xhHideTip();
      var head = xhEl("div", "xtt-head");
      head.appendChild(xhNode(j));
      head.appendChild(xhEl("span", "xtt-title", j.id));
      head.appendChild(xhEl("span", "xtt-own", xhOwnerLabel(j)));
      head.appendChild(xhEl("span", "xtt-pill " + xhTone(j), xhStateText(j)));
      tip.appendChild(head);
      var box = xhEl("div", "xtt-cmdbox");
      box.appendChild(xhEl("pre", "xtt-cmd", j.command));
      tip.appendChild(box);
      var kv = xhEl("dl", "xtt-kv");
      function row(k, v, mono) { kv.appendChild(xhEl("dt", null, wsT(k))); var dd = xhEl("dd", mono ? "mono" : null, v); kv.appendChild(dd); return dd; }
      if (j.cwd) row("js.xhud.k.cwd", j.cwd, true);
      var ended = xhEnded(j), elapsed = row(ended ? "js.xhud.k.ran" : "js.xhud.k.el", xhFmtElapsed((ended ? xhEndedAt(j) : Date.now()) - j.started_at));
      if (!ended) elapsed.setAttribute("data-xsince", String(j.started_at));
      row("js.xhud.k.started", xhFmtClock(j.started_at));
      row("js.xhud.k.owner", j.origin === "user" ? wsT("js.xhud.own.user.long") : wsT("js.xhud.own.agent"));
      tip.appendChild(kv);
      var s = xhSt(j), note = "";
      if (s === "lost") note = wsT("js.xhud.tt.lost");
      else if (s === "nostart") note = wsT("js.xhud.tt.nostart");
      else if (s === "stopped" && j.stopped_by_user) note = xhStoppedNote(j);
      if (note) tip.appendChild(xhEl("div", "xtt-note", note));
      tip.appendChild(xhEl("div", "xtt-hint", j.origin === "user" ? wsT("js.xhud.tt.hint.user") : wsT("js.xhud.tt.hint.run")));
    } else {
      var list = xhFolded, h2 = xhEl("div", "xtt-head");
      h2.appendChild(xhEl("span", "xtt-title", kind === "agg" ? wsT("js.xhud.tt.agg", String(list.length)) : wsT("js.xhud.tt.more", String(list.length))));
      tip.appendChild(h2);
      var kv2 = xhEl("dl", "xtt-kv");
      list.slice(0, 6).forEach(function (j) {
        kv2.appendChild(xhEl("dt", null, xhOwnerLabel(j)));
        kv2.appendChild(xhEl("dd", "mono", xhCmdHead(j.command) + " · " + (xhEnded(j) ? xhShortText(j) : xhFmtElapsed(Date.now() - j.started_at))));
      });
      tip.appendChild(kv2);
      tip.appendChild(xhEl("div", "xtt-hint", wsT("js.xhud.tt.hint.run")));
    }
    tip.hidden = false;
    var a = (anchor || xhGroup).getBoundingClientRect(), c = composer.getBoundingClientRect(), bound = (composer.parentElement || composer).getBoundingClientRect(), tr = tip.getBoundingClientRect();
    var left = a.left - c.left + a.width / 2 - tr.width / 2;
    left = Math.max(bound.left - c.left + 8, Math.min(left, bound.right - c.left - tr.width - 8));
    tip.style.left = Math.round(left) + "px";
    tip.style.top = Math.round(a.top - c.top - tr.height - 10) + "px";
    if (anchor) anchor.setAttribute("aria-describedby", tip.id);
    if (!xhTick) xhRender(false);
  }
  // The tooltip's stop line: whether the owning agent was told.
  function xhStoppedNote(j) {
    if (j.origin === "user") return wsT("js.xhud.tt.stopped.user.none");
    if (j.stop_notice === "sent") return wsT("js.xhud.tt.stopped.user");
    if (j.stop_notice === "pending") return wsT("js.xhud.nt.sending");
    if (j.stop_notice === "closed") return wsT("js.xhud.nt.closed");
    if (j.stop_notice === "failed") return wsT("js.xhud.nt.failed");
    return wsT("js.xhud.st.stopped.user");
  }
  function xhHideTip() {
    xhTipFor = "";
    if (xhTip) xhTip.hidden = true;
    if (xhGroup) q("[aria-describedby]", xhGroup).forEach(function (n) { n.removeAttribute("aria-describedby"); });
  }
  function xhTipFrom(c) {
    var kind = c.getAttribute("data-xkind") || "job";
    if (kind === "job" && xhPeekOpen() && xhPeekId() === c.getAttribute("data-xid")) return;
    if (kind === "job") xhShowTip("job", c.getAttribute("data-xid"), c);
    else xhShowTip(kind, "", c);
  }
  function xhChipOf(target) { return target && target.closest ? target.closest(".xchip, .xmore") : null; }
  function xhBindGroup(g) {
    g.addEventListener("mouseover", function (e) { var c = xhChipOf(e.target); if (c) { c.classList.add("is-hovered"); xhTipFrom(c); } });
    g.addEventListener("mouseout", function (e) { var c = xhChipOf(e.target); if (c && (!e.relatedTarget || !c.contains(e.relatedTarget))) { c.classList.remove("is-hovered"); xhHideTip(); } });
    g.addEventListener("focusin", function (e) { var c = xhChipOf(e.target); if (c) { xhActive = c.getAttribute("data-xid") || ""; xhRove(); xhTipFrom(c); } });
    g.addEventListener("focusout", function (e) { if (!e.relatedTarget || !g.contains(e.relatedTarget)) xhHideTip(); });
    g.addEventListener("click", function (e) {
      var st = e.target.closest && e.target.closest(".xstop");
      if (st) {
        e.preventDefault();
        var id = st.getAttribute("data-xid");
        if (st.getAttribute("data-xact") === "dismiss") xhDismiss(id);
        else xhAskStop(id, id);
        return;
      }
      var c = xhChipOf(e.target);
      if (!c) return;
      var kind = c.getAttribute("data-xkind"), id2 = kind ? (xhFolded[0] || xhJobs[0] || {}).id : c.getAttribute("data-xid");
      if (!id2) return;
      if (xhPeekOpen() && xhPeekId() === id2 && !kind) { xhClosePeek(false); return; }
      xhOpenPeek(id2, kind ? "" : id2);
    });
    g.addEventListener("keydown", function (e) {
      var keys = { ArrowLeft: -1, ArrowRight: 1, Home: "first", End: "last" };
      if (!(e.key in keys)) return;
      var btns = xhButtons(), i = btns.indexOf(doc.activeElement);
      if (i < 0) return;
      e.preventDefault();
      var nx = keys[e.key] === "first" ? btns[0] : keys[e.key] === "last" ? btns[btns.length - 1] : btns[(i + keys[e.key] + btns.length) % btns.length];
      nx.focus();
    });
  }
  function xhDismiss(id) {
    xhDismissed[id] = true;
    xhSaveDismissed();
    xhHideTip();
    var focusNext = xhGroup && xhGroup.contains(doc.activeElement);
    xhRender(true);
    if (focusNext) { var b = xhButtons()[0]; if (b) b.focus(); }
  }
  // --- the list ----------------------------------------------------------------
  // Every read and every stop answer is a snapshot taken between its request and
  // its response, and answers arrive in any order. Each request takes the next
  // number when it is issued; a job record carries the number of the request that
  // produced it, and an answer only replaces a record that is not newer. Two
  // moves do not wait for the numbers: a job that ended never runs again, and a
  // job that ended is never shown running again; an answer that says it ended
  // replaces a running record whatever its number (the issue order is not the
  // order the daemon took the snapshots in). After a stop or a retry settles, the
  // list is read again, and that newest list wins.
  function xhStale(old, job, q) {
    if (old.state !== "running" && job.state === "running") return true;
    if (old.state === "running" && job.state !== "running") return false;
    return (old._q || 0) > q;
  }
  function xhApplyJobs(jobs, q) {
    var now = Date.now(), seen = Object.create(null);
    jobs.forEach(function (j, i) {
      seen[j.id] = true;
      var old = xhById[j.id];
      if (old && xhStale(old, j, q)) { j = jobs[i] = old; } else j._q = q;
      j.seenEnded = old && old.seenEnded ? old.seenEnded : (j.state !== "running" && !j.ended_at ? now : 0);
      if (j.state !== "running") delete xhStopping[j.id];
    });
    Object.keys(xhStopping).forEach(function (id) { if (!seen[id]) delete xhStopping[id]; });
    xhJobs = jobs;
    xhById = Object.create(null);
    jobs.forEach(function (j) { xhById[j.id] = j; });
  }
  // xhGet is a bounded GET: a request that never answers is aborted, so the
  // in-flight flag of its caller is released.
  var XH_REQ_MS = 10000;
  function xhGet(url) {
    var ac = window.AbortController ? new AbortController() : null, t = setTimeout(function () { if (ac) ac.abort(); }, XH_REQ_MS);
    var opts = { credentials: "same-origin", cache: "no-store" };
    if (ac) opts.signal = ac.signal;
    return fetch(url, opts).then(function (r) {
      if (r.status === 404 || r.status === 403) return { status: r.status, body: null };
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json().then(function (b) { return { status: r.status, body: b }; });
    }).then(function (v) { clearTimeout(t); return v; }, function (e) { clearTimeout(t); throw e; });
  }
  // A list read that fails (a network error, a timeout, a 5xx) keeps the refresh
  // owed: it is read again after a growing pause, one read in flight, until it
  // answers. The events that asked for it came once and do not repeat.
  var xhRetryT = 0, xhRetryMs = 0;
  function xhRetryLater() {
    clearTimeout(xhRetryT);
    xhRetryMs = Math.min(15000, xhRetryMs ? xhRetryMs * 2 : 1000);
    xhRetryT = setTimeout(function () { xhRetryT = 0; xhFetch(); }, xhRetryMs);
  }
  function xhFetch() {
    if (!xhEnabled || xhAbsent) return;
    if (xhFetching) { xhDirty = true; return; }
    clearTimeout(xhRetryT);
    xhRetryT = 0;
    xhFetching = true;
    xhDirty = false;
    var q = ++xhSeq;
    xhGet("/s/" + rid + "/exec/jobs").then(function (x) {
      // A daemon without the route, a LAN viewer and a closed lead all answer
      // 404: no HUD, and no further reads this page load.
      if (x.status === 404 || x.status === 403) { xhAbsent = true; return null; }
      return x.body;
    }).then(function (res) {
      xhFetching = false;
      if (res && Array.isArray(res.jobs)) {
        xhRetryMs = 0;
        xhRev = typeof res.rev === "number" ? res.rev : xhRev;
        xhApplyJobs(res.jobs, q);
        xhRender(false);
        xhPeekJobsChanged();
        xhStopSettle();
      } else if (xhAbsent) xhUnmount();
      if (xhDirty) xhFetch();
    }, function () {
      xhFetching = false;
      if (xhDirty) xhFetch(); else xhRetryLater();
    });
  }
  function xhUnmount() {
    clearTimeout(xhRetryT);
    xhJobs = [];
    xhById = Object.create(null);
    if (xhGroup) { xhGroup.hidden = true; xhGroup.textContent = ""; }
    xhPeekDestroy();
  }
  // execWake is the stream's `execjobs` event: the revision moved (or the
  // stream just connected), so read the list, coalesced to one in flight and
  // one queued.
  function execWake(rev) {
    if (!xhEnabled || xhAbsent) return;
    xhFetch();
  }
