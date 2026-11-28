  // --- holders: the shell's bottom-bar Holders trigger and popup ------------
  // The shell's status bar ends with a compact trigger (#holders-trigger,
  // .hd-trigger, rendered when the daemon has holders to list): a status
  // dot, an icon, and the count of running OS holder processes (a pid, not a
  // durable in-process session). Its state is "warn" while any holder is
  // unattached or not restart-safe, the listing is incomplete, or a start
  // failed; "ok" with running holders; "zero" without. Its title carries the
  // breakdown.
  //
  // Nothing polls while the popup is closed: the shell reads /holders once on
  // load, then on a return to the tab (visibility, window focus) and after a
  // frame navigation, at most once per HD_FRESH, and on each opening, refresh,
  // and stop. The popup (kind "holders" of the rail popover controller, so it
  // and the other bottom-bar popovers exclude each other) opens upward over
  // the bar and groups every holder: those needing attention (unattached, not
  // restart-safe, failed starts), the running ones (durable sessions marked
  // in-process), and the terminals: the daemon's host terminals
  // (/s/host0/terminals) and the active frame's terminals whose session is
  // gone, which the frame lists for the shell (term-list-req / term-list).
  // An unattached holder's Stop is a 1.5s hold (WsHoldConfirm); a plain click
  // only explains the hold. A running terminal's Stop asks inline first; an
  // exited one's removes it at once. Attach opens a terminal as a card on the
  // active frame's page (term-attach), so it is off without a frame that
  // offers terminals.
  //
  // The running holders form a tree: a worker's holder nests under the
  // holder of the session that spawned it (its descriptor's parent_reply_id,
  // or the listing's parents map for an older descriptor), and, while the
  // trace runs, each holder lists its OS descendant processes below it.
  //
  // The trace (/holders/metrics) is the only repeated read, and it runs only
  // while the popup is open, not paused (#hd-pause), and the tab is shown:
  // one read, then the next a setTimeout later (the answer's interval_ms),
  // so closing, pausing, or hiding the tab leaves no timer. Each answer
  // patches the metric cells, the sparklines, and the totals (#hd-agg) by
  // key in place, so a hold, the focus, and the expanded rows survive it;
  // only the OS process rows, which hold no control, are rebuilt when their
  // set changes. A holder the answer lacks is marked gone, and an answer
  // naming a holder the popup lacks re-reads /holders (at most once per
  // HD_REREAD). A daemon without the route, a viewer it refuses (404), or a
  // machine it cannot read (supported false) leaves the static rows.
  //
  // The frame side (hdFrameTermReq) and the shell's message hooks
  // (hdFrameTerms, hdNavigated) are defined on every page; each does nothing
  // where its half is absent, so an unframed page needs no bottom bar.
  var hdTrig = shell && botBarEl ? botBarEl.querySelector(".hd-trigger") : null;
  var HD_CAUSE = { no_answer: "js.hd.cause.noanswer", protocol: "js.hd.cause.protocol", token: "js.hd.cause.token" };
  var HD_FRESH = 10000, HD_DURABLE = "durable.", HD_REREAD = 5000, HD_TRACE_MIN = 1500, HD_TRACE_MAX = 2000;
  var hd = {
    P: null, list: null, err: "", seq: 0, at: 0, busy: false,
    host: [], orphans: [], tseq: 0, oseq: 0, frame: false,
    // closing/stopping: terminal closes and holder stops in flight, by id,
    // so a re-render's fresh button cannot send a second one
    confirm: "", closing: {}, stopping: {}, dirty: false, hold: null,
    // the trace: its last answer, whether the daemon serves it (null until
    // an answer says), the pending timer, and the read in flight
    mx: null, mxOK: null, mseq: 0, timer: 0, mxBusy: false, paused: false, ms: HD_TRACE_MIN,
    rr: 0, miss: "",
    // exp: a holder's expanded state the user chose; cells: each rendered
    // holder row's patchable parts, by holder id
    exp: {}, cells: {}
  };

  function hdIsDurable(h) { return typeof h.id === "string" && h.id.indexOf(HD_DURABLE) === 0; }
  // hdCounts: running OS holders (pid, not durable), lost (unattached),
  // warn (attached but not restart-safe, plus one for an incomplete scan),
  // and failed starts.
  function hdCounts(list) {
    var c = { running: 0, lost: 0, warn: 0, fails: 0 };
    if (!list) return c;
    (list.holders || []).forEach(function (h) {
      if (h.pid > 0 && !hdIsDurable(h)) c.running++;
      if (!h.attached) c.lost++;
      else if (h.restart_safe === false) c.warn++;
    });
    if (list.incomplete) c.warn++;
    c.fails = (list.failures || []).length;
    return c;
  }
  function hdSummary(c) {
    var parts = [wsT("js.holders.sum.running", c.running)];
    if (c.lost) parts.push(wsT("js.holders.sum.lost", c.lost));
    if (c.warn) parts.push(wsT("js.holders.sum.warn", c.warn));
    if (c.fails) parts.push(wsT("js.holders.sum.fail", c.fails));
    return parts.join(" · ");
  }
  // hdTrigSync writes the trigger's state, count, and breakdown, each only
  // when it changed (the bar's fit observer watches its subtree).
  function hdTrigSync() {
    if (!hdTrig) return;
    var c = hdCounts(hd.list), state = c.lost || c.warn || c.fails ? "warn" : c.running ? "ok" : "zero";
    if (hdTrig.getAttribute("data-state") !== state) hdTrig.setAttribute("data-state", state);
    var n = hdTrig.querySelector(".hd-count"), count = String(c.running);
    if (n && n.textContent !== count) n.textContent = count;
    var tip = wsT("js.holders.title") + " — " + (hd.list ? hdSummary(c) : hd.err ? wsT("js.hd.fail", hd.err) : wsT("js.hd.loading"));
    if (hdTrig.title !== tip) { hdTrig.title = tip; hdTrig.setAttribute("aria-label", tip); }
  }
  function hdIsOpen() { return !!hd.P && rp.P === hd.P; }

  // hdFetch reads /holders; an open popup also reads its terminals.
  function hdFetch() {
    if (!window.fetch) return;
    var seq = ++hd.seq;
    hd.at = Date.now();
    hd.busy = true;
    hdBusy();
    fetch("/holders", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      return r.text().then(function (s) {
        if (!r.ok) throw s.trim() || "HTTP " + r.status;
        var v;
        try { v = JSON.parse(s); } catch (e) { throw "malformed answer"; }
        if (seq === hd.seq) { hd.list = v; hd.err = ""; }
      });
    }).catch(function (e) {
      if (seq === hd.seq) hd.err = typeof e === "string" ? e : wsT("js.post.unreachable");
    }).then(function () {
      if (seq !== hd.seq) return;
      hd.busy = false;
      hdBusy();
      hdTrigSync();
      if (hdIsOpen()) hdRender();
    });
    if (hdIsOpen()) hdTerms();
  }
  // hdFresh is a closed-popup freshness read, at most once per HD_FRESH.
  function hdFresh() {
    if (!hdTrig || doc.hidden || Date.now() - hd.at < HD_FRESH) return;
    hdFetch();
  }
  // hdNavigated runs when the shell shows another frame: an open popup
  // reads that frame's terminals (Attach follows it), a closed one may
  // refresh the trigger.
  function hdNavigated() {
    if (!hdTrig) return;
    if (hdIsOpen()) { hdTerms(); hdRender(); }
    else hdFresh();
  }

  // --- terminals: the host's, and the active frame's orphans
  // hdFrameOK reports whether the active frame offers terminals (its root
  // carries data-term): Attach and the orphan list need one.
  function hdFrameOK() {
    if (typeof shOn === "undefined" || !shOn || !shOn.el || !shOn.ready) return false;
    try {
      var d = shOn.el.contentDocument;
      return !!(d && d.documentElement && d.documentElement.hasAttribute("data-term"));
    } catch (e) { return false; }
  }
  function hdTerms() {
    var seq = ++hd.tseq;
    fetch("/s/host0/terminals", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      return r.ok ? r.json() : null;
    }).then(function (v) {
      if (seq !== hd.tseq) return;
      hd.host = v && v.anchor && Array.isArray(v.terminals) ? v.terminals : [];
      hdRender();
    }).catch(function () {
      if (seq !== hd.tseq) return;
      hd.host = [];
      hdRender();
    });
    hd.frame = hdFrameOK();
    if (hd.frame) shSend(shOn, "term-list-req", { seq: ++hd.oseq });
    else { hd.oseq++; hd.orphans = []; }
  }
  // hdFrameTerms takes the active frame's answer (shell side).
  function hdFrameTerms(ent, m) {
    if (!hdIsOpen() || m.seq !== hd.oseq) return;
    hd.orphans = m.orphans.filter(function (t) { return t && typeof t === "object" && typeof t.id === "string"; });
    hdRender();
  }
  // hdFrameTermReq answers the shell (frame side): the orphan terminals
  // this page's terminal cards know of, or a terminal to attach here.
  function hdFrameTermReq(m) {
    var T = window.WsTerm;
    if (m.wsShell === "term-attach") {
      if (T && typeof m.term.id === "string") T.attach(m.term);
      return;
    }
    if (!T) { shPost("term-list", { seq: m.seq, orphans: [] }); return; }
    T.list(function (err, v) {
      shPost("term-list", { seq: m.seq, orphans: !err && v && Array.isArray(v.orphans) ? v.orphans : [] });
    });
  }

  // --- the popup
  function hdBtn(cls, label, icon, title) {
    var b = mk("button", cls);
    b.type = "button";
    if (icon) b.appendChild(gIcon(icon));
    if (label) b.appendChild(doc.createTextNode(label));
    if (title) { b.title = title; if (!label) b.setAttribute("aria-label", title); }
    return b;
  }
  function hdNote(text, err) {
    var n = hd.P && hd.P.note;
    if (!n) return;
    n.textContent = text || "";
    n.classList.toggle("hd-err", !!err);
  }
  function hdBusy() {
    if (!hd.P) return;
    hd.P.refresh.classList.toggle("hd-spin", hd.busy);
    hd.P.list.classList.toggle("hd-busy", hd.busy && !hd.list);
  }
  function hdPop() { return hd.P || (hd.P = hdMake()); }
  function hdMake() {
    var P = {};
    var el = P.el = rpPop(wsT("js.holders.title"));
    el.classList.add("hd-pop");
    el.id = "holders-pop";
    el.tabIndex = -1;
    var head = el.appendChild(mk("div", "hd-hd"));
    head.appendChild(mk("b", "hd-title", wsT("js.holders.title")));
    P.sum = head.appendChild(mk("span", "hd-sum"));
    head.appendChild(mk("span", "hd-sp"));
    P.trace = head.appendChild(mk("span", "hd-trace"));
    P.trace.id = "hd-trace";
    P.trace.hidden = true;
    P.trace.setAttribute("data-on", "false");
    P.trace.appendChild(mk("i", "hd-tdot")).setAttribute("aria-hidden", "true");
    P.traceLbl = P.trace.appendChild(mk("span"));
    P.pause = head.appendChild(hdBtn("hd-ibtn", "", "pause", wsT("js.holders.trace.pause")));
    P.pause.id = "hd-pause";
    P.pause.hidden = true;
    P.pause.setAttribute("aria-pressed", "false");
    P.pause.addEventListener("click", function () {
      hd.paused = !hd.paused;
      hdNote(wsT(hd.paused ? "js.holders.trace.paused" : "js.holders.trace.resumed"));
      hdTraceSync();
    });
    P.refresh = head.appendChild(hdBtn("hd-ibtn", "", "refresh", wsT("js.holders.refresh")));
    P.refresh.id = "hd-refresh";
    P.refresh.addEventListener("click", function () { hdNote(""); hdFetch(); });
    var close = head.appendChild(hdBtn("hd-ibtn", "", "", wsT("js.holders.close")));
    close.appendChild(gIcon("x"));
    close.id = "hd-close";
    close.addEventListener("click", function () { railPopClose(true); });
    var strip = P.strip = el.appendChild(mk("div", "hd-strip"));
    strip.hidden = true;
    P.chips = strip.appendChild(mk("div", "hd-chips"));
    P.chips.id = "hd-chips";
    P.agg = strip.appendChild(mk("div", "hd-agg"));
    P.agg.id = "hd-agg";
    P.agg.hidden = true;
    P.aggRss = hdAggPart(P.agg, "js.holders.agg.rss", "agg:rss");
    P.aggCpu = hdAggPart(P.agg, "js.holders.agg.cpu", "agg:cpu");
    P.list = el.appendChild(mk("div", "hd-body"));
    P.list.id = "hdlist";
    P.note = el.appendChild(mk("div", "hd-note"));
    P.note.id = "hdnote";
    P.note.setAttribute("role", "status");
    P.note.setAttribute("aria-live", "polite");
    P.note.setAttribute("data-idle", wsT("js.holders.idle"));
    P.list.addEventListener("click", hdClick);
    if (window.WsHoldConfirm) {
      window.WsHoldConfirm.delegate(P.list, ".hd-hold", {
        onStart: function (b) { hd.hold = b; },
        onConfirm: function (b, held) { hd.hold = null; if (held) hdStop(b); hdFlush(); },
        onCancel: function (b, reason) {
          hd.hold = null;
          if (reason === "early-release") hdNote(wsT("js.holders.hold.hint"));
          hdFlush();
        }
      });
    }
    // Each opening asks the daemon for the trace again (it may have been
    // replaced since); closing leaves no timer and drops a read in flight.
    P.open = function () { hd.confirm = ""; hd.mxOK = null; hd.miss = ""; hdNote(""); hdRender(); hdFetch(); hdTraceSync(); };
    P.close = function () { hd.confirm = ""; hd.hold = null; hd.dirty = false; hdTraceStop(); };
    // Escape first dismisses an open terminal confirmation.
    P.back = function () {
      if (!hd.confirm) return false;
      var id = hd.confirm;
      hd.confirm = "";
      hdRender();
      hdFocus("term-stop", id);
      return true;
    };
    P.focus = function () { el.focus({ preventScroll: true }); };
    return P;
  }
  function hdClick(e) {
    var b = e.target.closest && e.target.closest("button[data-act]");
    if (!b || b.disabled || !hd.P.list.contains(b)) return;
    var act = b.getAttribute("data-act"), id = b.getAttribute("data-id") || "", t = hdTermByID(id);
    if (act === "toggle") {
      var c = hd.cells[id];
      if (c) { hd.exp[id] = !hdOpenOf(id, c); hdKidsSync(id, c); railPopPlace(); }
    } else if (act === "term-attach") { if (t && hdFrameOK()) shSend(shOn, "term-attach", { term: t }); }
    else if (act === "term-stop") {
      if (!t) return;
      if (t.state === "exited") hdTermStop(t);
      else { hd.confirm = id; hdRender(); hdFocus("term-yes", id); }
    } else if (act === "term-yes") { if (t) hdTermStop(t); }
    else if (act === "term-no") { hd.confirm = ""; hdRender(); hdFocus("term-stop", id); }
  }
  function hdTermByID(id) {
    return hd.host.concat(hd.orphans).filter(function (t) { return t.id === id; })[0] || null;
  }
  function hdFocus(act, id) {
    var b = hd.P && hd.P.list.querySelector('button[data-act="' + act + '"][data-id="' + CSS.escape(id) + '"]');
    if (b) b.focus({ preventScroll: true });
  }
  function hdStop(b) {
    var id = b.getAttribute("data-id");
    if (!id || b.disabled || hd.stopping[id]) return;
    b.disabled = true;
    hd.stopping[id] = true;
    hdNote("");
    post("/holders/" + encodeURIComponent(id) + "/stop", {}, function (ok, res) {
      delete hd.stopping[id];
      b.disabled = false;
      // a re-render during the stop drew a fresh, disabled button
      var cur = hd.P && hd.P.list.querySelector('button[data-act="holder-stop"][data-id="' + CSS.escape(id) + '"]');
      if (cur) cur.disabled = false;
      if (!ok) { hdNote(wsT("js.hd.stopfail", res), true); return; }
      hdNote(wsT("js.holders.stopped", hdLabelOf(id)));
      hdFetch();
    });
  }
  // A terminal closes through the host anchor, which closes any terminal
  // by its id, whichever session opened it.
  function hdTermStop(t) {
    if (hd.closing[t.id]) return;
    hd.closing[t.id] = true;
    hd.confirm = "";
    hdNote("");
    hdRender();
    post("/s/host0/terminals/" + encodeURIComponent(t.id) + "/close", {}, function (ok, res) {
      delete hd.closing[t.id];
      if (!ok) hdNote(wsT("js.hd.stopfail", res), true);
      if (hdIsOpen()) hdTerms();
    });
  }
  function hdLabel(h) {
    var s = h.session || {}, l = typeof s.label === "string" ? s.label.trim() : "";
    if (!l && typeof s.exec_id === "string") l = s.exec_id;
    return l || h.id;
  }
  function hdLabelOf(id) {
    var h = hd.list && (hd.list.holders || []).filter(function (x) { return x.id === id; })[0];
    return h ? hdLabel(h) : id;
  }
  var HD_KIND = { lead: "js.holders.kind.lead", "agent-worker": "js.holders.kind.agent", "pi-worker": "js.holders.kind.agent", exec: "js.holders.kind.exec", terminal: "js.holders.kind.terminal" };

  // hdFlush applies a render the active hold deferred.
  function hdFlush() { if (hd.dirty) hdRender(); }
  // hdRender redraws the popup's body from the last reads. A hold in
  // progress inside it defers the redraw until the hold ends (its button
  // must stay the same element); a focused button is found again by its
  // action and id.
  function hdRender() {
    if (!hdIsOpen()) return;
    var P = hd.P, list = P.list;
    if (hd.hold && list.contains(hd.hold)) { hd.dirty = true; return; }
    hd.dirty = false;
    var c = hdCounts(hd.list), fa = doc.activeElement, fk = null;
    if (fa && list.contains(fa) && fa.getAttribute("data-act")) fk = [fa.getAttribute("data-act"), fa.getAttribute("data-id") || ""];
    hdChrome(c);
    hd.cells = {};
    var frag = doc.createDocumentFragment();
    if (!hd.list) {
      frag.appendChild(mk("div", "hd-empty hd-quiet", hd.err ? wsT("js.hd.fail", hd.err) : wsT("js.hd.loading")));
    } else {
      var holders = hd.list.holders || [], failures = hd.list.failures || [], canStop = !!hd.list.can_stop;
      if (hd.list.incomplete) {
        var ban = frag.appendChild(mk("div", "hd-banner"));
        ban.setAttribute("role", "alert");
        ban.appendChild(gIcon("warn"));
        ban.appendChild(mk("span", null, wsT("js.holders.incomplete")));
      }
      var attention = holders.filter(function (h) { return !h.attached || h.restart_safe === false; });
      var running = holders.filter(function (h) { return h.attached && h.restart_safe !== false; });
      var head = false, ta = hdTree(attention), tr = hdTree(running);
      if (attention.length || failures.length) {
        frag.appendChild(hdSec(wsT("js.holders.sec.attention"), attention.length + failures.length));
        frag.appendChild(hdHead());
        head = true;
        ta.roots.forEach(function (h) { frag.appendChild(hdNode(h, canStop, 0, ta.kids)); });
        failures.forEach(function (f) { frag.appendChild(hdFailRow(f)); });
      }
      frag.appendChild(hdSec(wsT("js.holders.sec.running"), running.filter(function (h) { return !hdIsDurable(h); }).length));
      if (running.length) {
        if (!head) frag.appendChild(hdHead());
        tr.roots.forEach(function (h) { frag.appendChild(hdNode(h, canStop, 0, tr.kids)); });
      } else {
        frag.appendChild(mk("div", "hd-empty hd-quiet", wsT("js.holders.none")));
      }
    }
    frag.appendChild(hdSec(wsT("js.holders.sec.terms"), hd.host.length + hd.orphans.length));
    if (hd.host.length) {
      frag.appendChild(mk("div", "hd-sub", wsT("js.hd.term.anchor")));
      hd.host.forEach(function (t) { frag.appendChild(hdTermRow(t)); });
    }
    if (hd.orphans.length) {
      frag.appendChild(mk("div", "hd-sub", wsT("js.hd.term.title")));
      hd.orphans.forEach(function (t) { frag.appendChild(hdTermRow(t)); });
    }
    if (!hd.host.length && !hd.orphans.length) frag.appendChild(mk("div", "hd-empty hd-quiet", wsT("js.holders.noterms")));
    list.textContent = "";
    list.appendChild(frag);
    hdMxPaint();
    if (fk) hdFocus(fk[0], fk[1]);
    railPopPlace();
  }
  function hdChrome(c) {
    var P = hd.P;
    P.chips.textContent = "";
    // The strip shows with the chips (an explicit hidden, not a :has() rule).
    P.strip.hidden = !hd.list;
    if (!hd.list) return;
    function chip(cls, text) { P.chips.appendChild(mk("span", "hd-chip" + (cls ? " " + cls : ""), text)); }
    chip("", wsT("js.holders.sum.running", c.running));
    if (c.lost) chip("hd-warn", wsT("js.holders.sum.lost", c.lost));
    if (c.warn) chip("hd-warn", wsT("js.holders.sum.warn", c.warn));
    if (c.fails) chip("hd-fail", wsT("js.holders.sum.fail", c.fails));
    var alert = c.lost || c.warn || c.fails;
    P.sum.textContent = alert ? wsT("js.holders.sum.attention") : wsT("js.holders.sum.ok");
    P.sum.setAttribute("data-tone", alert ? "warn" : "ok");
  }
  function hdSec(label, n) {
    var s = mk("div", "hd-sec");
    s.appendChild(mk("span", "hd-lbl", label));
    s.appendChild(mk("span", "hd-cnt", String(n)));
    return s;
  }
  function hdHead() {
    var h = mk("div", "hd-thead");
    h.setAttribute("aria-hidden", "true");
    h.appendChild(mk("span"));
    h.appendChild(mk("span", null, wsT("js.holders.col.proc")));
    h.appendChild(mk("span", "hd-mx", wsT("js.holders.col.rss")));
    h.appendChild(mk("span", "hd-mx", wsT("js.holders.col.cpu")));
    h.appendChild(mk("span", "hd-mx", wsT("js.holders.col.trend")));
    h.appendChild(mk("span"));
    return h;
  }
  function hdAge(ms, key) {
    var a = mk("span", "hd-age", wtAge(ms));
    a.title = wsT(key, wtStamp(ms));
    return a;
  }
  // hdParentOf is the reply id of the session that spawned h's, if known.
  function hdParentOf(h) {
    var s = h.session || {}, p = typeof s.parent_reply_id === "string" ? s.parent_reply_id : "";
    if (!p && hd.list && hd.list.parents && typeof hd.list.parents[h.id] === "string") p = hd.list.parents[h.id];
    return p;
  }
  // hdTree nests the holders of one section: a holder whose parent's holder
  // is in the same section goes under it; any other (and a cycle) is a root.
  function hdTree(hs) {
    var byReply = {}, up = {}, kids = {}, roots = [];
    hs.forEach(function (h) {
      var r = h.session && h.session.reply_id;
      if (typeof r === "string" && r && !byReply[r]) byReply[r] = h;
    });
    hs.forEach(function (h) { var p = byReply[hdParentOf(h)]; if (p && p !== h) up[h.id] = p; });
    hs.forEach(function (h) {
      var p = up[h.id], n = 0;
      for (var a = p; a && a !== h && n < 32; a = up[a.id]) n++;
      if (p && !(a === h || n >= 32)) (kids[p.id] = kids[p.id] || []).push(h);
      else roots.push(h);
    });
    return { roots: roots, kids: kids };
  }
  // hdNode is one holder's node: its row, then its children (the holders it
  // spawned, then its OS processes), shown while it is expanded.
  function hdNode(h, canStop, lv, kids) {
    var node = mk("div", "hd-node"), durable = hdIsDurable(h), lost = !h.attached, sub = kids[h.id] || [];
    node.setAttribute("data-node", h.id);
    var row = node.appendChild(mk("div", "hd-row" + (lost ? " hd-lost" : "")));
    row.setAttribute("data-id", h.id);
    row.style.setProperty("--lv", String(lv));
    row.title = durable ? wsT("js.holders.inproc.title") : h.id;
    var chev = row.appendChild(durable ? mk("span", "hd-chev") : hdBtn("hd-chev", "", "chev", wsT("js.holders.toggle")));
    if (!durable) { chev.setAttribute("data-act", "toggle"); chev.setAttribute("data-id", h.id); }
    var main = row.appendChild(mk("div", "hd-main")), l1 = main.appendChild(mk("div", "hd-l1"));
    l1.appendChild(mk("span", "hd-dot"));
    l1.appendChild(mk("b", "hd-name", durable ? h.id.slice(HD_DURABLE.length) : hdLabel(h)));
    // The kind badge heads the meta line (as the mockup), leaving the name
    // the row's full width beside the metric columns.
    var kind = h.session && HD_KIND[h.session.kind], meta = main.appendChild(mk("div", "hd-meta"));
    if (durable) meta.appendChild(mk("span", "hd-badge hd-dur", wsT("js.holders.inproc")));
    else if (kind) meta.appendChild(mk("span", "hd-badge", wsT(kind)));
    if (h.pid) meta.appendChild(mk("span", "hd-pid", wsT("js.hd.pid", h.pid)));
    if (h.session && typeof h.session.backend === "string" && h.session.backend) meta.appendChild(mk("span", null, h.session.backend));
    if (h.started_at) meta.appendChild(hdAge(h.started_at, "js.hd.started"));
    if (lost && !durable) meta.appendChild(mk("span", "hd-cause", wsT(HD_CAUSE[h.cause] || "js.hd.cause.unknown", h.protocol)));
    if (lost && durable) meta.appendChild(mk("span", "hd-cause", wsT("js.hd.cause.unknown")));
    if (!lost && h.restart_safe === false) {
      var rs = meta.appendChild(mk("span", "hd-unsafe", wsT("js.hd.unsafe")));
      rs.title = wsT("js.hd.unsafe.title");
    }
    var rss = row.appendChild(mk("span", "hd-num hd-mx", "—"));
    rss.setAttribute("data-k", h.id + ":rss");
    var cpu = row.appendChild(mk("span", "hd-num hd-cpu hd-mx", "—"));
    cpu.setAttribute("data-k", h.id + ":cpu");
    var trend = row.appendChild(mk("span", "hd-trend hd-mx")), spark = null, spR = null, spC = null;
    if (!durable) {
      trend.title = wsT("js.holders.trend.title");
      spark = trend.appendChild(svgEl("svg", { "class": "hd-spark", viewBox: "0 0 48 16", preserveAspectRatio: "none", "aria-hidden": "true", "data-k": h.id + ":spark" }));
      spR = spark.appendChild(svgEl("polyline", { "class": "hd-sp-rss", points: "" }));
      spC = spark.appendChild(svgEl("polyline", { "class": "hd-sp-cpu", points: "" }));
    }
    var act = row.appendChild(mk("span", "hd-act"));
    if (lost && !durable && canStop) {
      var b = act.appendChild(hdBtn("hd-btn hd-danger hd-hold d-hold-btn", wsT("js.hd.stop"), "stop", wsT("js.hd.stop.title") + " — " + wsT("js.holders.hold.hint")));
      b.setAttribute("data-hold-mode", "bar");
      b.setAttribute("data-act", "holder-stop");
      b.setAttribute("data-id", h.id);
      if (hd.stopping[h.id]) b.disabled = true;
    }
    if (durable) return node;
    var box = node.appendChild(mk("div", "hd-kids"));
    box.style.setProperty("--kl", String(lv));
    sub.forEach(function (k) { box.appendChild(hdNode(k, canStop, lv + 1, kids)); });
    var c = hd.cells[h.id] = {
      pid: h.pid > 0 ? h.pid : 0, lv: lv, row: row, chev: chev, rss: rss, cpu: cpu, spR: spR, spC: spC,
      box: box, procs: box.appendChild(mk("div", "hd-procs")), n: sub.length, np: 0, sig: "", pc: {}
    };
    hdKidsSync(h.id, c);
    return node;
  }
  // hdOpenOf reports whether a holder's children show: the user's choice,
  // else open while it has spawned holders under it.
  function hdOpenOf(id, c) { return c.n + c.np > 0 && (id in hd.exp ? !!hd.exp[id] : c.n > 0); }
  // hdKidsSync writes a holder's chevron and children box from its state;
  // a holder without children keeps an inert, invisible chevron.
  function hdKidsSync(id, c) {
    var has = c.n + c.np > 0, open = hdOpenOf(id, c);
    if (c.chev.disabled !== !has) c.chev.disabled = !has;
    if (has) c.chev.setAttribute("aria-expanded", open ? "true" : "false");
    else c.chev.removeAttribute("aria-expanded");
    if (c.box.hidden !== !open) c.box.hidden = !open;
  }

  // --- the trace
  function hdAggPart(box, key, k) {
    var s = box.appendChild(mk("span", null, wsT(key) + " "));
    var b = s.appendChild(mk("b", null, "—"));
    b.setAttribute("data-k", k);
    return b;
  }
  function hdSet(el, text) { if (el && el.textContent !== text) el.textContent = text; }
  function hdRss(b) {
    var mb = (+b || 0) / 1048576;
    return mb >= 1024 ? (mb / 1024).toFixed(2) + " GB" : (mb >= 10 ? String(Math.round(mb)) : mb.toFixed(1)) + " MB";
  }
  function hdCpu(v) { return (+v || 0).toFixed(1) + "%"; }
  // hdSparkPts plots a series across the 48x16 sparkline, scaled to its peak.
  function hdSparkPts(s) {
    if (!Array.isArray(s) || s.length < 2) return "";
    var max = 0, pts = [];
    s.forEach(function (v) { if (+v > max) max = +v; });
    max = max * 1.15 || 1;
    for (var i = 0; i < s.length; i++) pts.push((i * 48 / (s.length - 1)).toFixed(1) + "," + (14.5 - (+s[i] || 0) / max * 13).toFixed(1));
    return pts.join(" ");
  }
  function hdTraceLive() { return hdIsOpen() && !hd.paused && !doc.hidden && hd.mxOK !== false; }
  // hdTraceSync starts the trace when it should run and nothing is pending,
  // or stops it.
  function hdTraceSync() {
    if (!hdTraceLive()) hdTraceStop();
    else if (!hd.timer && !hd.mxBusy) hdTraceAsk();
    hdTraceChrome();
  }
  function hdTraceStop() {
    if (hd.timer) { clearTimeout(hd.timer); hd.timer = 0; }
    if (hd.mxBusy) { hd.mxBusy = false; hd.mseq++; }
  }
  function hdTraceAsk() {
    hd.timer = 0;
    if (!window.fetch) return;
    var seq = ++hd.mseq;
    hd.mxBusy = true;
    fetch("/holders/metrics", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      // An older daemon, or a viewer it does not serve: no trace.
      if (r.status === 404 || r.status === 405) return { supported: false };
      return r.ok ? r.json() : null;
    }).then(function (v) { hdTraceTook(seq, v); }, function () { hdTraceTook(seq, null); });
  }
  // hdTraceTook applies an answer (null: a failed read, retried on the next
  // turn) and schedules the next read while the trace should run.
  function hdTraceTook(seq, v) {
    if (seq !== hd.mseq) return;
    hd.mxBusy = false;
    if (v && v.supported === false) { hd.mxOK = false; hd.mx = null; }
    else if (v && v.holders && typeof v.holders === "object") {
      hd.mxOK = true;
      hd.mx = v;
      hd.ms = Math.min(HD_TRACE_MAX, Math.max(HD_TRACE_MIN, +v.interval_ms || HD_TRACE_MIN));
    }
    if (hdIsOpen()) hdMxPaint(true);
    hdTraceChrome();
    if (hdTraceLive()) hd.timer = setTimeout(hdTraceAsk, hd.ms);
  }
  function hdTraceChrome() {
    var P = hd.P;
    if (!P) return;
    var off = hd.mxOK === false, live = hdTraceLive();
    P.trace.hidden = off;
    P.pause.hidden = off;
    P.trace.setAttribute("data-on", live ? "true" : "false");
    hdSet(P.traceLbl, hd.paused ? wsT("js.holders.trace.off") : wsT("js.holders.trace.on", String(hd.ms / 1000)));
    var pressed = hd.paused ? "true" : "false";
    if (P.pause.getAttribute("aria-pressed") !== pressed) {
      var tip = wsT(hd.paused ? "js.holders.trace.resume" : "js.holders.trace.pause");
      P.pause.setAttribute("aria-pressed", pressed);
      P.pause.title = tip;
      P.pause.setAttribute("aria-label", tip);
      P.pause.textContent = "";
      P.pause.appendChild(gIcon(hd.paused ? "play" : "pause"));
    }
    P.note.setAttribute("data-idle", wsT(live ? "js.holders.idle.trace" : "js.holders.idle"));
  }
  // hdMxPaint writes the last answer into the rendered rows by key. From a
  // trace answer (tick), a holder the answer lacks or names anew re-reads
  // the listing, once per new difference and at most once per HD_REREAD.
  function hdMxPaint(tick) {
    var P = hd.P, mx = hd.mx, on = !!(mx && hd.mxOK !== false);
    P.el.classList.toggle("hd-mx-on", on);
    if (P.agg.hidden !== !on) P.agg.hidden = !on;
    if (!on) return;
    var H = mx.holders, rss = 0, cpu = 0, miss = [], own = {};
    Object.keys(hd.cells).forEach(function (id) { if (hd.cells[id].pid) own[hd.cells[id].pid] = true; });
    Object.keys(H).forEach(function (id) {
      rss += +H[id].rss || 0;
      cpu += +H[id].cpu || 0;
      if (!hd.cells[id]) miss.push(id);
    });
    Object.keys(hd.cells).forEach(function (id) {
      var c = hd.cells[id], m = H[id] || null;
      c.row.classList.toggle("hd-gone", !m && !!c.pid);
      if (!m && c.pid) miss.push(id);
      hdSet(c.rss, m ? hdRss(m.rss) : "—");
      hdSet(c.cpu, m ? hdCpu(m.cpu) : "—");
      var sp = m && m.spark || {};
      c.spR.setAttribute("points", hdSparkPts(sp.rss));
      c.spC.setAttribute("points", hdSparkPts(sp.cpu));
      hdProcSync(id, c, m ? (m.procs || []).filter(function (p) { return p && p.pid > 0 && !own[p.pid]; }) : []);
    });
    hdSet(P.aggRss, hdRss(rss));
    hdSet(P.aggCpu, hdCpu(cpu));
    var sig = miss.sort().join(" ");
    if (!tick || !sig || sig === hd.miss || Date.now() - hd.rr < HD_REREAD) return;
    hd.miss = sig;
    hd.rr = Date.now();
    hdFetch();
  }
  // hdProcSync patches a holder's OS process rows, or rebuilds them when
  // their set changed (they hold no control, so nothing is lost).
  function hdProcSync(id, c, ps) {
    var sig = ps.map(function (p) { return p.pid; }).join(",");
    if (sig === c.sig) {
      ps.forEach(function (p) { var e = c.pc[p.pid]; if (e) { hdSet(e.rss, hdRss(p.rss)); hdSet(e.cpu, hdCpu(p.cpu)); } });
      return;
    }
    c.sig = sig;
    c.pc = {};
    c.procs.textContent = "";
    hdProcOrder(ps).forEach(function (e) { c.procs.appendChild(hdProcRow(id, e[0], c.lv + 1 + Math.min(e[1], 2), c)); });
    c.np = ps.length;
    hdKidsSync(id, c);
  }
  // hdProcOrder lists processes depth first by parent pid, each with its
  // depth below the holder; siblings by pid.
  function hdProcOrder(ps) {
    var by = {}, has = {}, out = [];
    ps.forEach(function (p) { has[p.pid] = true; });
    ps.forEach(function (p) { var k = has[p.ppid] && p.ppid !== p.pid ? p.ppid : 0; (by[k] = by[k] || []).push(p); });
    (function walk(k, d) {
      (by[k] || []).sort(function (a, b) { return a.pid - b.pid; }).forEach(function (p) {
        if (out.length >= ps.length) return;
        out.push([p, d]);
        walk(p.pid, d + 1);
      });
    })(0, 0);
    return out;
  }
  function hdProcRow(id, p, lv, c) {
    var row = mk("div", "hd-row hd-os");
    row.setAttribute("data-id", id + "/" + p.pid);
    row.style.setProperty("--lv", String(lv));
    row.title = wsT("js.holders.os.title", p.pid, p.ppid);
    row.appendChild(mk("span", "hd-chev"));
    var main = row.appendChild(mk("div", "hd-main")), l1 = main.appendChild(mk("div", "hd-l1"));
    l1.appendChild(mk("span", "hd-dot"));
    l1.appendChild(mk("b", "hd-name", typeof p.name === "string" && p.name ? p.name : String(p.pid)));
    main.appendChild(mk("div", "hd-meta")).appendChild(mk("span", "hd-pid", wsT("js.holders.os.pid", p.pid, p.ppid)));
    var e = c.pc[p.pid] = {
      rss: row.appendChild(mk("span", "hd-num hd-mx", hdRss(p.rss))),
      cpu: row.appendChild(mk("span", "hd-num hd-cpu hd-mx", hdCpu(p.cpu)))
    };
    e.rss.setAttribute("data-k", id + "/" + p.pid + ":rss");
    e.cpu.setAttribute("data-k", id + "/" + p.pid + ":cpu");
    row.appendChild(mk("span", "hd-trend hd-mx"));
    row.appendChild(mk("span", "hd-act"));
    return row;
  }
  function hdFailRow(f) {
    var row = mk("div", "hd-trow hd-fail");
    row.appendChild(mk("span", "hd-dot"));
    var msg = row.appendChild(mk("span", "hd-msg", f.message));
    msg.title = f.message;
    if (f.at) row.appendChild(hdAge(f.at, "js.hd.failed"));
    return row;
  }
  function hdTermRow(t) {
    var exited = t.state === "exited", closing = !!hd.closing[t.id];
    var row = mk("div", "hd-trow hd-term" + (exited ? " hd-exited" : ""));
    row.setAttribute("data-row", t.id);
    row.title = t.id;
    row.appendChild(mk("span", "hd-dot"));
    var line = row.appendChild(mk("span", "hd-line"));
    line.appendChild(mk("b", "hd-name", t.cwd ? t.cwd.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || t.cwd : t.id));
    if (t.started_at) line.appendChild(hdAge(t.started_at, "js.hd.started"));
    var meta = row.appendChild(mk("span", "hd-meta"));
    var cwd = meta.appendChild(mk("span", "hd-cwd"));
    cwd.appendChild(mk("bdi", null, t.cwd || ""));
    cwd.title = t.cwd || "";
    if (exited) meta.appendChild(mk("span", "hd-badge hd-dur", wsT("js.term.state.exited")));
    var btns = row.appendChild(mk("span", "hd-btns"));
    if (!exited) {
      var at = btns.appendChild(hdBtn("hd-btn hd-primary", wsT("js.hd.term.attach"), "link", hd.frame ? wsT("js.hd.term.attach.title") : wsT("js.holders.attach.noframe")));
      at.setAttribute("data-act", "term-attach");
      at.setAttribute("data-id", t.id);
      at.disabled = !hd.frame;
    }
    var st = btns.appendChild(hdBtn("hd-btn hd-danger", wsT("js.hd.stop"), "stop", wsT("js.hd.term.stop.title")));
    st.setAttribute("data-act", "term-stop");
    st.setAttribute("data-id", t.id);
    st.disabled = closing;
    if (hd.confirm === t.id && !exited) {
      var cf = row.appendChild(mk("div", "hd-confirm"));
      cf.setAttribute("role", "alertdialog");
      cf.setAttribute("aria-label", wsT("js.term.confirm"));
      cf.appendChild(mk("span", null, wsT("js.term.confirm")));
      var yes = cf.appendChild(hdBtn("hd-btn hd-danger", wsT("js.holders.term.yes")));
      yes.setAttribute("data-act", "term-yes");
      yes.setAttribute("data-id", t.id);
      var no = cf.appendChild(hdBtn("hd-btn", wsT("js.holders.term.no")));
      no.setAttribute("data-act", "term-no");
      no.setAttribute("data-id", t.id);
    }
    return row;
  }
  // hdPlace stands the popup 4px above the bar, right-aligned to the
  // trigger (railPopPlace's bottom-bar branch reads it).
  function hdPlace(el, a, vw, vh) {
    var m = 8, top = botBarEl.getBoundingClientRect().top - 4;
    var maxH = Math.max(120, Math.min(560, vh - 72, Math.floor(top - m)));
    el.style.maxHeight = maxH + "px";
    var w = el.offsetWidth, h = Math.min(el.offsetHeight, maxH);
    el.style.top = Math.round(Math.max(m, top - h)) + "px";
    el.style.left = Math.round(Math.max(m, Math.min(a.right - w, vw - w - m))) + "px";
  }

  if (hdTrig) {
    hdTrigSync();
    hdFetch();
    // Hiding the tab stops the trace; showing it again resumes an open one.
    doc.addEventListener("visibilitychange", function () { if (!doc.hidden) hdFresh(); if (hdIsOpen()) hdTraceSync(); });
    window.addEventListener("focus", hdFresh);
  }
