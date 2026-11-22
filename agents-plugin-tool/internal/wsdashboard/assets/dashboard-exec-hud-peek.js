// Exec job HUD, the peek: the upward, non-modal panel over the composer with a
// tab per job, the job's merged stdout/stderr (stderr tinted), the inline stop
// confirmation, the stop's notice card and the toast. The page reads output
// only while the peek is open and only for the open tab: one bounded read of
// /s/<id>/exec/<job>/output from the last offset, with backoff while idle, no
// timer once the job ended and was drained. Command and output are text nodes,
// never markup. Strip and list: dashboard-exec-hud.js.
  var xhPk = { open: false, id: "", origin: "", confirm: "", pinned: false, follow: true, pending: 0, wrap: true, cmdOpen: false, sig: "" };
  var xhPeekEl = null, xhToastEl = null, xhOutEl = null, xhNewBtn = null, xhFoot = null, xhOut = null, xhPollT = 0, xhPollGen = 0, xhConfT = 0, xhConfLeft = 0, xhToastT = 0, xhNoticeBusy = Object.create(null), xhCopiedT = 0;
  var XH_TAIL_BYTES = 16384, XH_MAX_LINES = 1500, XH_MAX_BYTES = 196608, XH_CONFIRM_S = 8, XH_POLL_MIN = 350, XH_POLL_MAX = 3000;
  function xhPeekOpen() { return xhPk.open; }
  function xhPeekId() { return xhPk.id; }
  function xhFmtBytes(n) { return n < 1024 ? n + " B" : (n / 1024).toFixed(1) + " KiB"; }
  function xhBtn(cls, act, text) {
    var b = xhEl("button", cls, text);
    b.type = "button";
    if (act) b.setAttribute("data-xact", act);
    return b;
  }
  function xhPeekNode() {
    if (xhPeekEl) return xhPeekEl;
    xhPeekEl = xhEl("div", "xpeek");
    xhPeekEl.hidden = true;
    xhPeekEl.setAttribute("role", "dialog");
    xhPeekEl.setAttribute("aria-modal", "false");
    xhToastEl = xhEl("div", "xtoast");
    xhToastEl.hidden = true;
    xhToastEl.setAttribute("role", "status");
    composer.appendChild(xhPeekEl);
    composer.appendChild(xhToastEl);
    xhPeekEl.addEventListener("click", xhPeekClick);
    xhPeekEl.addEventListener("keydown", function (e) {
      if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
        var tab = e.target.closest && e.target.closest("[data-xtab]");
        if (!tab) return;
        var ts = q("[data-xtab]", xhPeekEl), i = ts.indexOf(tab), nx = ts[(i + (e.key === "ArrowRight" ? 1 : -1) + ts.length) % ts.length];
        e.preventDefault();
        xhSwitchTab(nx.getAttribute("data-xtab"), true);
      } else if ((e.key === "Enter" || e.key === " ") && e.target.getAttribute && e.target.getAttribute("data-xact") === "cmd") {
        e.preventDefault();
        xhPk.cmdOpen = !xhPk.cmdOpen;
        e.target.classList.toggle("open", xhPk.cmdOpen);
      }
    });
    xhToastEl.addEventListener("click", function (e) {
      var id = xhToastEl.getAttribute("data-xid");
      if (e.target.getAttribute("data-xact") === "toast-view" && id) { xhToastEl.hidden = true; xhOpenPeek(id, id); }
    });
    doc.addEventListener("pointerdown", function (e) {
      if (!xhPk.open || xhPk.pinned) return;
      if (!e.target.isConnected) return;
      if (xhPeekEl.contains(e.target) || (xhGroup && xhGroup.contains(e.target)) || (xhToastEl && xhToastEl.contains(e.target))) return;
      xhClosePeek(false);
    }, true);
    doc.addEventListener("visibilitychange", function () { if (!doc.hidden && xhPk.open) xhPollNow(); });
    return xhPeekEl;
  }
  // Escape: one press cancels the confirm or closes the panel, and no more. The
  // rail, the drawer and the shell also close on Escape, and the drawer's own
  // handler is on the document in the capture phase from startup: this one is
  // on the window (capture runs window first) from load, and consumes the key.
  window.addEventListener("keydown", function (e) {
    if (e.key !== "Escape" || e.defaultPrevented || !xhPk.open || !xhPeekEl) return;
    var a = doc.activeElement;
    if (!a || !(xhPeekEl.contains(a) || (xhGroup && xhGroup.contains(a)))) return;
    e.preventDefault();
    e.stopPropagation();
    if (xhPk.confirm) xhCancelStop(true);
    else xhClosePeek(true);
  }, true);
  function xhPeekJobs() {
    return xhJobs.filter(function (j) { return !xhDismissed[j.id] || j.id === xhPk.id; }).sort(function (a, b) { return xhByStartAsc(a, b) || (a.id < b.id ? -1 : 1); });
  }
  function xhOpenPeek(id, origin) {
    if (!xhById[id]) return;
    xhPeekNode();
    var same = xhPk.open && xhPk.id === id;
    xhPk.open = true;
    xhPk.id = id;
    if (origin) xhPk.origin = origin;
    if (!same) { xhPk.confirm = ""; xhPk.follow = true; xhPk.pending = 0; }
    xhHideTip();
    xhPk.sig = "";
    xhPeekRender(true);
    xhRender(true);
    // The same job already open keeps its output and its poll.
    if (!same || !xhOut) xhPollStart();
  }
  function xhClosePeek(restoreFocus) {
    var origin = xhPk.origin;
    xhPk.open = false;
    xhPk.confirm = "";
    clearInterval(xhConfT);
    xhPollStop();
    xhOut = null;
    xhOutEl = null;
    if (xhPeekEl) xhPeekEl.hidden = true;
    xhRender(true);
    if (restoreFocus) xhFocusStrip(origin);
  }
  // focusStrip puts the keyboard on the strip: the origin chip when it is still
  // drawn, else what stands for it (+N, the aggregate, the first chip), else the
  // composer; never an element the fold or a fade took away.
  function xhFocusStrip(origin) {
    var b = origin && xhGroup && xhGroup.querySelector('.xchip[data-xid="' + origin + '"]');
    if (!b || b.closest("[hidden]") || !b.getClientRects().length) b = xhFoldTarget();
    if (!b) { var ed = typeof ctext !== "undefined" && ctext ? (ctext.wsc && ctext.wsc.editor) || ctext : null; if (ed && ed.focus) ed.focus(); return; }
    b.focus();
  }
  function xhPeekDestroy() {
    xhPk.open = false;
    clearInterval(xhConfT);
    xhPollStop();
    if (xhPeekEl) xhPeekEl.hidden = true;
    if (xhToastEl) xhToastEl.hidden = true;
  }
  function xhPeekJobsChanged() {
    if (!xhPk.open) return;
    if (!xhById[xhPk.id]) { xhClosePeek(false); return; }
    xhPeekRender(false);
    var j = xhById[xhPk.id];
    if (xhOut && xhEnded(j) && !xhOut.drained) xhPollNow();
  }
  function xhPeekSync() {
    xhToastRefresh();
    if (!xhPk.open) return;
    xhPeekRender(false);
  }
  function xhSwitchTab(id, focusTab) {
    if (!xhById[id]) return;
    if (xhPk.open && xhPk.id === id) {
      // The selected tab again changes nothing: its output and its poll stay.
      if (focusTab) { var cur = xhPeekEl.querySelector('[aria-selected="true"]'); if (cur) cur.focus(); }
      return;
    }
    xhPk.id = id;
    xhPk.confirm = "";
    xhPk.follow = true;
    xhPk.pending = 0;
    clearInterval(xhConfT);
    xhPk.sig = "";
    xhPeekRender(true);
    xhRender(true);
    xhPollStart();
    if (focusTab) { var sel = xhPeekEl.querySelector('[aria-selected="true"]'); if (sel) sel.focus(); }
  }
  // peekRender draws the head (tabs, state, buttons, command, confirm, notice)
  // when its signature changed, and the output frame when the tab changed; a
  // tick of the clock changes neither, so a polling peek never loses focus or
  // its scroll position.
  function xhPeekRender(forceOut) {
    var j = xhById[xhPk.id];
    if (!xhPk.open || !j) return;
    var t0 = window.performance && performance.now ? performance.now() : 0;
    var tabs = xhPeekJobs(), st = xhSt(j);
    // A job that ended under an open confirmation: there is nothing left to stop.
    if (xhPk.confirm && xhEnded(j)) { xhPk.confirm = ""; clearInterval(xhConfT); }
    var sig = [xhPk.id, st, j.stop_notice || "", xhPk.confirm, xhPk.pinned, xhPk.cmdOpen, !!xhNoticeBusy[j.id], xhCardFor(j) ? 1 : 0, tabs.map(function (x) { return x.id + ":" + xhSt(x); }).join(",")].join("|");
    xhPeekEl.hidden = false;
    xhPeekEl.setAttribute("aria-label", wsT("js.xhud.pk.label") + " — " + xhCmdHead(j.command));
    if (sig !== xhPk.sig || forceOut || !xhOutEl) {
      xhPk.sig = sig;
      var inPeek = !!doc.activeElement && xhPeekEl.contains(doc.activeElement);
      var keep = inPeek ? (doc.activeElement.getAttribute("data-xact") || "") : "";
      var keepTab = doc.activeElement && doc.activeElement.getAttribute ? doc.activeElement.getAttribute("data-xtab") : "";
      xhPeekHead(j, tabs);
      if (keep) { var again = xhPeekEl.querySelector('[data-xact="' + keep + '"]'); if (again) again.focus(); }
      else if (keepTab) { var again2 = xhPeekEl.querySelector('[data-xtab="' + keepTab + '"]'); if (again2) again2.focus(); }
      // The control that held the keyboard is gone (a confirmation that timed out
      // or whose job ended, a Stop that became Stopping): Stop if there is one,
      // else the selected tab, so Escape and Tab keep working from the panel.
      if (inPeek && !xhPeekEl.contains(doc.activeElement)) {
        var fb = xhPeekEl.querySelector('[data-xact="stop"]:not(:disabled)') || xhPeekEl.querySelector('[aria-selected="true"]');
        if (fb) fb.focus();
      }
    }
    xhFootText(j);
    if (t0 && performance.measure) { try { performance.measure("ws.xhud.peek-render", { start: t0, end: performance.now(), detail: { tabs: tabs.length } }); } catch (e) {} }
  }
  function xhPeekHead(j, tabs) {
    var out = xhOutEl && xhOutEl.parentNode ? xhOutEl.parentNode : null;
    var sameOut = out && xhOut && xhOut.id === j.id;
    if (!sameOut) { xhOutEl = null; }
    var keepScroll = sameOut ? xhOutEl : null;
    // Everything but the output frame is rebuilt.
    Array.prototype.slice.call(xhPeekEl.children).forEach(function (c) { if (!(sameOut && c === out)) xhPeekEl.removeChild(c); });
    var anchor = sameOut ? out : null;
    function add(n) { if (anchor) xhPeekEl.insertBefore(n, anchor); else xhPeekEl.appendChild(n); }
    var top = xhEl("div", "xp-top"), tabsEl = xhEl("div", "xp-tabs");
    tabsEl.setAttribute("role", "tablist");
    tabsEl.setAttribute("aria-label", wsT("js.xhud.pk.tabs"));
    tabs.forEach(function (x) {
      var tb = xhBtn("xp-tab", "", null);
      tb.setAttribute("role", "tab");
      tb.setAttribute("aria-selected", x.id === j.id ? "true" : "false");
      tb.setAttribute("data-xtab", x.id);
      tb.tabIndex = x.id === j.id ? 0 : -1;
      var d = xhEl("span", "xp-dot");
      d.setAttribute("data-st", xhSt(x));
      tb.appendChild(d);
      tb.appendChild(xhEl("span", "t", xhCmdHead(x.command)));
      tabsEl.appendChild(tb);
    });
    top.appendChild(tabsEl);
    var pin = xhBtn("xibtn", "pin", "⌖");
    pin.setAttribute("aria-pressed", xhPk.pinned ? "true" : "false");
    pin.title = wsT("js.xhud.pk.pin");
    pin.setAttribute("aria-label", wsT("js.xhud.pk.pin"));
    top.appendChild(pin);
    var cl = xhBtn("xibtn", "close", "×");
    cl.title = wsT("js.xhud.pk.close");
    cl.setAttribute("aria-label", wsT("js.xhud.pk.close"));
    top.appendChild(cl);
    add(top);

    var st = xhSt(j), ended = xhEnded(j), meta = xhEl("div", "xp-meta");
    meta.appendChild(xhEl("span", "xpill " + xhTone(j), xhStateText(j)));
    meta.appendChild(xhEl("span", "xown", xhOwnerLabel(j)));
    var elE = xhEl("span", "xel2", ended ? wsT("js.xhud.k.ran") + " " + xhFmtElapsed(xhEndedAt(j) - j.started_at) : xhFmtElapsed(Date.now() - j.started_at));
    if (!ended) elE.setAttribute("data-xsince", String(j.started_at));
    meta.appendChild(elE);
    meta.appendChild(xhEl("span", "xgrow"));
    if (j.origin === "user") {
      var jb = xhBtn("xbtn", "jump", "↑ " + wsT("js.xhud.pk.jump"));
      if (!xhCardFor(j)) { jb.disabled = true; jb.title = wsT("js.xhud.pk.jump.none"); }
      meta.appendChild(jb);
    }
    meta.appendChild(xhBtn("xbtn", "copy", wsT("js.xhud.pk.copy")));
    if (ended) {
      var db = xhBtn("xbtn", "dismiss", wsT("js.xhud.dismiss"));
      db.setAttribute("aria-label", wsT("js.xhud.chip.dismiss.aria", xhCmdHead(j.command)));
      meta.appendChild(db);
    }
    if (!ended) {
      var sbtn = xhBtn("xbtn stop", "stop");
      if (st === "stopping") {
        sbtn.disabled = true;
        sbtn.appendChild(xhEl("span", "xspinr"));
        sbtn.appendChild(doc.createTextNode(wsT("js.xhud.stopping")));
      } else {
        sbtn.appendChild(doc.createElement("i"));
        sbtn.appendChild(doc.createTextNode(wsT("js.xhud.stop")));
        sbtn.setAttribute("aria-expanded", xhPk.confirm === j.id ? "true" : "false");
      }
      meta.appendChild(sbtn);
    }
    add(meta);

    var pc = xhEl("div", "xp-cmd"), code = xhEl("code", "cl" + (xhPk.cmdOpen ? " open" : ""), j.command);
    code.title = wsT("js.xhud.pk.expand");
    code.setAttribute("data-xact", "cmd");
    code.tabIndex = 0;
    code.setAttribute("role", "button");
    code.setAttribute("aria-label", wsT("js.xhud.k.cmd") + ": " + j.command);
    pc.appendChild(code);
    if (j.cwd) pc.appendChild(xhEl("span", "cwd", "⌂ " + j.cwd));
    add(pc);

    if (xhPk.confirm === j.id && !ended) {
      var cf = xhEl("div", "xconf");
      cf.setAttribute("role", "alertdialog");
      cf.setAttribute("aria-label", wsT("js.xhud.conf.title"));
      cf.appendChild(xhEl("b", null, wsT("js.xhud.conf.title")));
      cf.appendChild(xhEl("p", null, wsT("js.xhud.conf.body")));
      cf.appendChild(xhEl("p", null, j.origin === "user" ? wsT("js.xhud.conf.user") : wsT("js.xhud.conf.agent")));
      var rw = xhEl("div", "xrow"), tmo = xhEl("span", "tmo", wsT("js.xhud.conf.auto", String(xhConfLeft || XH_CONFIRM_S)));
      tmo.setAttribute("data-xconf-left", "1");
      rw.appendChild(tmo);
      rw.appendChild(xhBtn("xbtn", "cancel", wsT("js.xhud.cancel")));
      rw.appendChild(xhBtn("xbtn danger", "confirm", wsT("js.xhud.stop.job")));
      cf.appendChild(rw);
      add(cf);
    }
    var card = xhNoticeCard(j);
    if (card) add(card);

    if (!sameOut) {
      var ow = xhEl("div", "xoutwrap");
      xhOutEl = xhEl("pre", "xout" + (!ended ? " live" : "") + (xhPk.wrap ? "" : " nowrap"));
      xhOutEl.setAttribute("role", "log");
      xhOutEl.setAttribute("aria-live", "off");
      xhOutEl.setAttribute("aria-label", wsT("js.xhud.out.label"));
      xhOutEl.tabIndex = 0;
      ow.appendChild(xhOutEl);
      xhNewBtn = xhBtn("xnew", "follow");
      xhNewBtn.hidden = true;
      ow.appendChild(xhNewBtn);
      xhPeekEl.appendChild(ow);
      xhOutEl.addEventListener("scroll", xhOnScroll);
      xhOut = { id: j.id, next: -1, start: 0, delay: XH_POLL_MIN, skip: false, drained: false, open: false, openEl: null, openStream: "", gen: 0, written: j.written || 0, ended: false, empty: true, busy: false, again: false, bytes: 0, first: true };
      xhOutEl.appendChild(xhEl("span", "xempty", wsT("js.xhud.out.empty")));
      out = ow;
    } else {
      xhOutEl = out.querySelector(".xout");
      xhNewBtn = out.querySelector(".xnew");
      xhOutEl.classList.toggle("live", !ended);
    }
    if (xhFoot && xhFoot.parentNode === xhPeekEl) xhPeekEl.removeChild(xhFoot);
    xhFoot = xhEl("div", "xp-foot");
    var live = xhEl("span", "xlive");
    xhFoot.appendChild(live);
    xhFoot.appendChild(xhEl("span", "ftail"));
    var wr = xhBtn("xibtn sp", "wrap", wsT("js.xhud.out.wrap"));
    wr.style.width = "auto";
    wr.style.padding = "0 7px";
    wr.style.fontSize = "11px";
    wr.setAttribute("aria-pressed", xhPk.wrap ? "true" : "false");
    xhFoot.appendChild(wr);
    xhFoot.appendChild(xhEl("span", null, wsT("js.xhud.pk.foot")));
    xhPeekEl.appendChild(xhFoot);
    xhUpdateNewBtn();
    if (!keepScroll && xhPk.follow) xhOutEl.scrollTop = xhOutEl.scrollHeight;
  }
  function xhFootText(j) {
    if (!xhFoot) return;
    var ended = xhEnded(j), live = xhFoot.querySelector(".xlive"), tail = xhFoot.querySelector(".ftail");
    live.className = "xlive" + (ended ? " off" : xhPk.follow ? "" : " off");
    live.textContent = ended ? wsT("js.xhud.out.ended") : xhPk.follow ? wsT("js.xhud.out.live") : wsT("js.xhud.out.paused");
    if (xhOut) tail.textContent = wsT("js.xhud.out.tail", xhFmtBytes(Math.max(0, (xhOut.next < 0 ? 0 : xhOut.next) - xhOut.start)), xhFmtBytes(Math.max(xhOut.written, j.written || 0)));
  }
  function xhUpdateNewBtn() {
    if (!xhNewBtn) return;
    xhNewBtn.hidden = xhPk.pending <= 0 || xhPk.follow;
    if (!xhNewBtn.hidden) xhNewBtn.textContent = wsT("js.xhud.out.new", String(xhPk.pending));
  }
  function xhOnScroll() {
    if (!xhOutEl) return;
    var atEnd = xhOutEl.scrollHeight - xhOutEl.scrollTop - xhOutEl.clientHeight < 10;
    xhPk.follow = atEnd;
    if (atEnd) xhPk.pending = 0;
    xhUpdateNewBtn();
    var j = xhById[xhPk.id];
    if (j) xhFootText(j);
  }
  // --- output ------------------------------------------------------------------
  function xhLineEl(stream) {
    var d = xhEl("span", "l" + (stream === "stderr" ? " e" : ""));
    return d;
  }
  function xhSystemLine(text) {
    var d = xhEl("span", "l sys", text);
    xhOutEl.appendChild(d);
    xhOut.open = false;
  }
  // xhAppendText adds one stream's text: it continues the open (not yet
  // newline-terminated) line of the same stream, else starts a line. cut marks
  // the first line it starts as the tail of a longer one (the window began
  // inside it).
  function xhAppendText(stream, text, frag, cut) {
    var o = xhOut, made = 0, first = true;
    text = text.replace(/\r/g, "");
    var parts = text.split("\n");
    for (var i = 0; i < parts.length; i++) {
      var part = parts[i], last = i === parts.length - 1, ln = o.open && o.openStream === stream ? o.openEl : null;
      // A window that begins mid-character starts with replacement characters.
      if (cut && first) part = part.replace(/^\uFFFD{1,3}/, "");
      if (part !== "" || (!last && !ln)) {
        if (!ln) { ln = xhLineEl(stream); if (cut && first) ln.className += " cut"; frag.appendChild(ln); made++; }
        if (part !== "") { ln.appendChild(doc.createTextNode(part)); ln._n = (ln._n || 0) + part.length; o.bytes += part.length; }
      }
      first = false;
      if (!last) o.open = false;
      else if (part !== "") { o.open = true; o.openEl = ln; o.openStream = stream; }
    }
    return made;
  }
  // trim keeps what the panel retains bounded by bytes as well as lines: old
  // lines go first, and a single endless line loses its oldest text.
  function xhTrim() {
    var o = xhOut, el = xhOutEl;
    while (el.childNodes.length > XH_MAX_LINES || o.bytes > XH_MAX_BYTES) {
      var f = el.firstChild;
      if (!f) break;
      if (el.childNodes.length === 1) {
        if (f.childNodes.length < 2) break;
        o.bytes -= f.firstChild.data.length;
        f._n = Math.max(0, (f._n || 0) - f.firstChild.data.length);
        f.removeChild(f.firstChild);
        f.classList.add("cut");
        continue;
      }
      o.bytes -= f._n || 0;
      if (f === o.openEl) { o.open = false; o.openEl = null; }
      el.removeChild(f);
    }
  }
  function xhAddOutput(res) {
    var o = xhOut;
    if (!o || !xhOutEl) return;
    var frag = doc.createDocumentFragment(), made = 0, segs = res.segments || [], cut = false;
    // The tail window begins at an arbitrary byte: its first partial line is
    // dropped when a newline follows in the window; a window with no newline
    // at all is one long line, kept as a bounded suffix marked as cut.
    if (o.skip && segs.length) {
      o.skip = false;
      var k = -1;
      for (var i = 0; i < segs.length; i++) if (segs[i].text.indexOf("\n") >= 0) { k = i; break; }
      if (k >= 0) { segs = segs.slice(k); segs[0] = { stream: segs[0].stream, text: segs[0].text.slice(segs[0].text.indexOf("\n") + 1) }; }
      else cut = true;
    }
    if (res.gap) { frag.appendChild(xhEl("span", "l sys", wsT("js.xhud.out.gap"))); o.open = false; made++; }
    segs.forEach(function (s) { made += xhAppendText(s.stream, s.text, frag, cut); cut = false; });
    if (frag.childNodes.length || made) {
      var emp = xhOutEl.querySelector(".xempty");
      if (emp) emp.parentNode.removeChild(emp);
      // The open line may sit in the live DOM already: appending to it needs no node.
      xhOutEl.appendChild(frag);
      if (xhPk.follow) xhOutEl.scrollTop = xhOutEl.scrollHeight;
      else { xhPk.pending += made; xhUpdateNewBtn(); }
    } else if (xhPk.follow && xhOutEl.scrollHeight - xhOutEl.scrollTop - xhOutEl.clientHeight > 10) xhOutEl.scrollTop = xhOutEl.scrollHeight;
    // Text that only continued the open line made no node, and still counts.
    if (segs.length) xhTrim();
  }
  function xhEndLine(j) {
    if (!xhOut || xhOut.ended) return;
    xhOut.ended = true;
    var s = xhSt(j), text = "";
    if (j.state === "exited" && j.signal != null) text = wsT("js.xhud.out.sigexit", String(j.signal));
    else if (j.state === "exited") text = wsT("js.xhud.out.exit", String(j.exit_code == null ? 0 : j.exit_code));
    else if (s === "stopped") text = j.stopped_by_user ? wsT("js.xhud.out.stopped.user") : wsT("js.xhud.out.stopped");
    else if (s === "lost") text = wsT("js.xhud.out.lost");
    if (!text) return;
    var emp = xhOutEl.querySelector(".xempty");
    if (emp) emp.parentNode.removeChild(emp);
    xhSystemLine(text);
    if (xhPk.follow) xhOutEl.scrollTop = xhOutEl.scrollHeight;
  }
  // --- polling -----------------------------------------------------------------
  function xhPollStop() { clearTimeout(xhPollT); xhPollT = 0; xhPollGen++; }
  function xhPollStart() {
    xhPollStop();
    if (!xhPk.open) return;
    var j = xhById[xhPk.id];
    if (!j || !xhOut) return;
    var w = j.written || 0;
    xhOut.start = Math.max(0, w - XH_TAIL_BYTES);
    xhOut.next = xhOut.start;
    xhOut.skip = xhOut.start > 0;
    xhOut.gen = xhPollGen;
    xhOut.first = true;
    xhPollSchedule(0);
  }
  function xhPollNow() {
    if (!xhPk.open || !xhOut || xhOut.drained) return;
    // A read in flight is never doubled: one more follows it.
    if (xhOut.busy) { xhOut.again = true; return; }
    clearTimeout(xhPollT);
    xhPollSchedule(0);
  }
  function xhPollSchedule(ms) {
    clearTimeout(xhPollT);
    var gen = xhPollGen;
    xhPollT = setTimeout(function () { xhPollOnce(gen); }, ms);
  }
  // pollOnce is one read of this tab's output: at most one is ever in flight
  // (a wake-up during it is coalesced into one more read after it), so two
  // answers never carry the same offsets, and an older answer never lands after
  // a newer one.
  function xhPollOnce(gen) {
    if (gen !== xhPollGen || !xhPk.open || !xhOut || xhOut.drained) return;
    if (doc.hidden) return;
    var o = xhOut, id = o.id;
    if (o.busy) { o.again = true; return; }
    o.busy = true;
    o.again = false;
    fetch("/s/" + rid + "/exec/" + encodeURIComponent(id) + "/output?from=" + o.next, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (r.status === 404) return { gone: true };
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    }).then(function (res) {
      o.busy = false;
      if (gen !== xhPollGen || !xhPk.open || xhOut !== o) return;
      if (res.gone) { xhFetch(); return; }
      var t0 = window.performance && performance.now ? performance.now() : 0;
      xhPollApply(res, o, id);
      if (t0 && performance.measure) { try { performance.measure("ws.xhud.poll", { start: t0, end: performance.now(), detail: { bytes: res.next - res.from } }); } catch (e) {} }
    }, function () {
      o.busy = false;
      if (gen !== xhPollGen || xhOut !== o) return;
      o.delay = Math.min(5000, o.delay * 2);
      xhPollSchedule(o.again ? 0 : o.delay);
    });
  }
  // pollApply takes one read: the job's fresher record, the new runs, the next
  // offset, and the delay to the following read (short while more is waiting,
  // growing while idle, none once an ended job is drained).
  function xhPollApply(res, o, id) {
    var before = o.next;
    xhMergeJob(res.job);
    o.written = res.written;
    // The list's size is as old as its last event (output does not raise one):
    // when the first read finds far more waiting than the tail window, it is
    // dropped and the real tail is read, so opening never pages through the log.
    if (o.first) {
      o.first = false;
      if (res.written - res.from > XH_TAIL_BYTES + 1) {
        o.start = Math.max(0, res.written - XH_TAIL_BYTES);
        o.next = o.start;
        o.skip = o.start > 0;
        xhPollSchedule(0);
        return;
      }
    }
    xhAddOutput(res);
    o.next = res.next;
    var j = xhById[id];
    // Drained: the read's own record was terminal (not an older, running snapshot
    // answered after the job ended) and the cursor reached its end.
    var term = !!res.job && res.job.state !== "running";
    if (j && term && xhEnded(j) && o.next >= res.written) { o.drained = true; xhEndLine(j); xhRender(false); xhFootText(j); return; }
    if (res.next > before) o.delay = o.next < res.written ? 20 : XH_POLL_MIN;
    else o.delay = Math.min(XH_POLL_MAX, Math.round(o.delay * 1.5));
    if (j) xhFootText(j);
    xhPollSchedule(o.again ? 0 : o.delay);
  }
  // mergeJob takes a fresher job record (an output read or a stop answer) into
  // the list and redraws when its state moved.
  var XH_NOTICE_RANK = { "": 0, pending: 1, failed: 2, sent: 3, closed: 3 };
  // stale: a record older than the one held. An ended job never runs again, and a
  // stop's notice only moves forward (none, pending, failed, sent or closed); a
  // read or list answered after a newer answer must not take either back.
  function xhStale(old, job) {
    if (old.state !== "running" && job.state === "running") return true;
    return old.state === job.state && (XH_NOTICE_RANK[job.stop_notice || ""] || 0) < (XH_NOTICE_RANK[old.stop_notice || ""] || 0);
  }
  function xhMergeJob(job) {
    if (!job || !job.id) return;
    var old = xhById[job.id];
    if (!old || xhStale(old, job)) return;
    var prev = xhSt(old) + "|" + (old.stop_notice || "") + "|" + old.written;
    job.seenEnded = old.seenEnded || (job.state !== "running" && !job.ended_at ? Date.now() : 0);
    if (job.state !== "running") delete xhStopping[job.id];
    xhJobs[xhJobs.indexOf(old)] = job;
    xhById[job.id] = job;
    if (prev !== xhSt(job) + "|" + (job.stop_notice || "") + "|" + job.written) { xhRender(false); xhPeekRender(false); }
  }
  // --- stop --------------------------------------------------------------------
  function xhAskStop(id, origin) {
    var j = xhById[id];
    if (!j || xhEnded(j) || xhStopping[id]) return;
    xhPeekNode();
    var same = xhPk.open && xhPk.id === id;
    xhPk.open = true;
    xhPk.id = id;
    if (origin) xhPk.origin = origin;
    if (!same) { xhPk.follow = true; xhPk.pending = 0; }
    xhPk.confirm = id;
    xhHideTip();
    xhPk.sig = "";
    xhPeekRender(!same);
    xhRender(true);
    if (!same) xhPollStart();
    var c = xhPeekEl.querySelector('[data-xact="cancel"]');
    if (c) c.focus();
    clearInterval(xhConfT);
    xhConfLeft = XH_CONFIRM_S;
    xhConfT = setInterval(function () {
      xhConfLeft--;
      var s = xhPeekEl.querySelector("[data-xconf-left]");
      if (xhConfLeft <= 0) { clearInterval(xhConfT); xhCancelStop(false); }
      else if (s) s.textContent = wsT("js.xhud.conf.auto", String(xhConfLeft));
    }, 1000);
  }
  function xhCancelStop(focusBack) {
    clearInterval(xhConfT);
    if (!xhPk.confirm) return;
    xhPk.confirm = "";
    xhPk.sig = "";
    xhPeekRender(false);
    if (focusBack) { var b = xhPeekEl.querySelector('[data-xact="stop"]'); if (b) b.focus(); }
  }
  function xhStopRequest(id, done) {
    fetch("/s/" + rid + "/exec/" + encodeURIComponent(id) + "/stop", { method: "POST", credentials: "same-origin", cache: "no-store" }).then(function (r) {
      return r.text().then(function (s) { return { r: r, s: s }; }, function () { return { r: r, s: "" }; });
    }).then(function (x) {
      var body = null;
      try { body = JSON.parse(x.s); } catch (e) { body = null; }
      done(x.r, body, x.s);
    }, function () { done(null, null, wsT("js.post.unreachable")); });
  }
  function xhDoStop(id) {
    clearInterval(xhConfT);
    var j = xhById[id];
    if (!j) return;
    xhPk.confirm = "";
    xhStopping[id] = true;
    xhPk.sig = "";
    xhPeekRender(false);
    xhRender(true);
    var sel = xhPeekEl && xhPeekEl.querySelector('[aria-selected="true"]');
    if (sel) sel.focus();
    xhStopRequest(id, function (r, body, text) {
      delete xhStopping[id];
      if (body && body.job) xhMergeJobForce(body.job);
      if (r && r.ok && body && body.job) { xhAfterStop(body.job); return; }
      // The job may still run (its holder did not go), or is not found.
      if (r && r.status === 404) xhFetch();
      xhRender(true);
      xhPeekRender(false);
      var head = xhCmdHead(j.command), why = body && body.error ? body.error : (text || "HTTP " + (r ? r.status : 0));
      if (window.WsToast) window.WsToast.show(wsT("js.xhud.toast.stop.fail", head, why), { kind: "warn" });
    });
  }
  function xhMergeJobForce(job) {
    var old = xhById[job.id];
    if (!old) return;
    job.seenEnded = old.seenEnded || (job.state !== "running" && !job.ended_at ? Date.now() : 0);
    xhJobs[xhJobs.indexOf(old)] = job;
    xhById[job.id] = job;
  }
  function xhAfterStop(job) {
    xhRender(true);
    xhPeekRender(false);
    if (xhOut && xhOut.id === job.id) xhPollNow();
    if (job.state !== "running" && !(xhPk.open && xhPk.id === job.id)) xhToast(job);
  }
  function xhRetryNotice(id) {
    var j = xhById[id];
    if (!j || xhNoticeBusy[id]) return;
    xhNoticeBusy[id] = true;
    xhPk.sig = "";
    xhPeekRender(false);
    xhStopRequest(id, function (r, body) {
      delete xhNoticeBusy[id];
      if (body && body.job) xhMergeJobForce(body.job);
      xhRender(true);
      xhPk.sig = "";
      xhPeekRender(false);
      xhToastRefresh();
    });
  }
  // The notice card: how the owning agent learned of the user's stop.
  function xhNoticeState(j) {
    if (j.state !== "stopped" || !j.stopped_by_user) return "";
    if (j.origin === "user") return "none";
    if (xhNoticeBusy[j.id]) return "sending";
    if (j.stop_notice === "sent") return "sent";
    if (j.stop_notice === "pending") return "sending";
    if (j.stop_notice === "closed") return "closed";
    if (j.stop_notice === "failed") return "failed";
    return "";
  }
  var XH_NOTICE_KEY = { sending: "js.xhud.nt.sending", sent: "js.xhud.nt.sent", closed: "js.xhud.nt.closed", failed: "js.xhud.nt.failed", none: "js.xhud.nt.none" };
  function xhNoticeCard(j) {
    var ns = xhNoticeState(j);
    if (!ns) return null;
    var c = xhEl("div", "xnotice");
    c.setAttribute("data-ns", ns);
    c.setAttribute("aria-live", "polite");
    var h = xhEl("div", "nh");
    h.appendChild(xhEl("span", null, (ns === "sent" ? "✓ " : ns === "sending" ? "… " : ns === "none" ? "– " : "⚠ ") + wsT(XH_NOTICE_KEY[ns])));
    if (ns === "failed") h.appendChild(xhBtn("xbtn", "retry", wsT("js.xhud.nt.retry")));
    c.appendChild(h);
    return c;
  }
  // toastText: what the stop did. A job that exited before the stop was
  // accepted was not stopped by it (the daemon marks only the job a stop ended),
  // and an agent's job says how its notice stands now.
  function xhToastText(j) {
    var head = xhCmdHead(j.command);
    if (j.state === "running") return "";
    if (j.state !== "stopped" || !j.stopped_by_user) return wsT("js.xhud.toast.ended", head);
    var ns = xhNoticeState(j);
    if (j.origin === "user" || ns === "") return wsT("js.xhud.toast.user", head);
    if (ns === "sent") return wsT("js.xhud.toast.agent", head);
    if (ns === "sending") return wsT("js.xhud.toast.agent.pending", head);
    return wsT("js.xhud.toast.agent.fail", head);
  }
  function xhToast(j) {
    xhPeekNode();
    xhToastEl.setAttribute("data-xid", j.id);
    xhToastEl.hidden = false;
    xhToastEl.textContent = "";
    // Above the panel when it is open (the stylesheet puts it above the strip).
    xhToastEl.style.bottom = xhPk.open && !xhPeekEl.hidden ? "calc(100% + " + (xhPeekEl.offsetHeight + 34) + "px)" : "";
    xhToastEl.appendChild(xhEl("span", "tm"));
    var tx = xhEl("span", "xtt-text", xhToastText(j));
    xhToastEl.appendChild(tx);
    xhToastEl.appendChild(xhBtn("", "toast-view", wsT("js.xhud.toast.view")));
    clearTimeout(xhToastT);
    xhToastT = setTimeout(function () { xhToastEl.hidden = true; }, 6500);
  }
  // A toast that is still up follows the job's notice (notifying, then sent or
  // failed), as the notice card does; its timer and its View button stay.
  function xhToastRefresh() {
    if (!xhToastEl || xhToastEl.hidden) return;
    var j = xhById[xhToastEl.getAttribute("data-xid")], tx = xhToastEl.querySelector(".xtt-text");
    if (!j || !tx) return;
    var t = xhToastText(j);
    if (tx.textContent !== t) tx.textContent = t;
  }
  // --- jump to the `!` command's card, copy, clicks ---------------------------------
  function xhCardFor(j) {
    if (j.origin !== "user" || typeof cexecCards === "undefined") return null;
    for (var i = 0; i < cexecCards.length; i++) if (cexecCards[i].card && cexecCards[i].card.exec_id === j.id && cexecCards[i].el && cexecCards[i].el.isConnected) return cexecCards[i];
    return null;
  }
  // dismissFromPeek: the keyboard's way to clear an ended job when the strip is
  // folded to its aggregate. The panel moves to the neighbouring tab, or closes
  // when it was the last one.
  function xhDismissFromPeek(id) {
    var tabs = xhPeekJobs(), at = tabs.findIndex(function (x) { return x.id === id; }), rest = tabs.filter(function (x) { return x.id !== id; });
    xhDismiss(id);
    if (!rest.length) { xhClosePeek(true); return; }
    xhPk.id = "";
    xhSwitchTab(rest[Math.min(Math.max(at, 0), rest.length - 1)].id, true);
  }
  function xhPeekClick(e) {
    var tab = e.target.closest && e.target.closest("[data-xtab]");
    if (tab) { xhSwitchTab(tab.getAttribute("data-xtab"), false); return; }
    var a = e.target.closest && e.target.closest("[data-xact]");
    if (!a) return;
    var act = a.getAttribute("data-xact"), j = xhById[xhPk.id];
    if (!j) return;
    if (act === "close") xhClosePeek(true);
    else if (act === "pin") { xhPk.pinned = !xhPk.pinned; xhPk.sig = ""; xhPeekRender(false); }
    else if (act === "stop") xhAskStop(xhPk.id);
    else if (act === "cancel") xhCancelStop(true);
    else if (act === "confirm") xhDoStop(xhPk.id);
    else if (act === "retry") xhRetryNotice(xhPk.id);
    else if (act === "cmd") { xhPk.cmdOpen = !xhPk.cmdOpen; a.classList.toggle("open", xhPk.cmdOpen); }
    else if (act === "wrap") { xhPk.wrap = !xhPk.wrap; a.setAttribute("aria-pressed", xhPk.wrap ? "true" : "false"); xhOutEl.classList.toggle("nowrap", !xhPk.wrap); }
    else if (act === "follow") { xhPk.follow = true; xhPk.pending = 0; xhUpdateNewBtn(); xhOutEl.scrollTop = xhOutEl.scrollHeight; xhFootText(j); }
    else if (act === "copy") {
      // The label follows the outcome: copyText resolves to whether it copied.
      var old = a.getAttribute("data-xlabel") || a.textContent;
      a.setAttribute("data-xlabel", old);
      Promise.resolve(copyText(j.command, true)).then(function (ok) { return ok === true; }, function () { return false; }).then(function (ok) {
        if (!a.isConnected) return;
        // The copy fallback selects a scratch field and drops it: the keyboard returns to the button.
        if (!doc.activeElement || doc.activeElement === doc.body) a.focus();
        a.textContent = ok ? "✓ " + wsT("js.xhud.pk.copied") : "⚠ " + wsT("js.xhud.pk.copyfail");
        clearTimeout(xhCopiedT);
        xhCopiedT = setTimeout(function () { a.textContent = old; }, 1400);
      });
    } else if (act === "dismiss") xhDismissFromPeek(j.id);
    else if (act === "jump") {
      var c = xhCardFor(j);
      if (!xhPk.pinned) xhClosePeek(false);
      if (c && c.el) {
        c.el.classList.remove("xflash");
        void c.el.offsetWidth;
        c.el.classList.add("xflash");
        c.el.scrollIntoView({ block: "nearest" });
      }
    }
  }
