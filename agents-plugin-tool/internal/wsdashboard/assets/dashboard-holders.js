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
  // The frame side (hdFrameTermReq) and the shell's message hooks
  // (hdFrameTerms, hdNavigated) are defined on every page; each does nothing
  // where its half is absent, so an unframed page needs no bottom bar.
  var hdTrig = shell && botBarEl ? botBarEl.querySelector(".hd-trigger") : null;
  var HD_CAUSE = { no_answer: "js.hd.cause.noanswer", protocol: "js.hd.cause.protocol", token: "js.hd.cause.token" };
  var HD_FRESH = 10000, HD_DURABLE = "durable.";
  var hd = {
    P: null, list: null, err: "", seq: 0, at: 0, busy: false,
    host: [], orphans: [], tseq: 0, oseq: 0, frame: false,
    confirm: "", closing: {}, dirty: false, hold: null
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
    P.open = function () { hd.confirm = ""; hdNote(""); hdRender(); hdFetch(); };
    P.close = function () { hd.confirm = ""; hd.hold = null; hd.dirty = false; };
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
    if (act === "term-attach") { if (t && hdFrameOK()) shSend(shOn, "term-attach", { term: t }); }
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
    if (!id || b.disabled) return;
    b.disabled = true;
    hdNote("");
    post("/holders/" + encodeURIComponent(id) + "/stop", {}, function (ok, res) {
      b.disabled = false;
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
      var head = false;
      if (attention.length || failures.length) {
        frag.appendChild(hdSec(wsT("js.holders.sec.attention"), attention.length + failures.length));
        frag.appendChild(hdHead());
        head = true;
        attention.forEach(function (h) { frag.appendChild(hdNode(h, canStop)); });
        failures.forEach(function (f) { frag.appendChild(hdFailRow(f)); });
      }
      frag.appendChild(hdSec(wsT("js.holders.sec.running"), running.filter(function (h) { return !hdIsDurable(h); }).length));
      if (running.length) {
        if (!head) frag.appendChild(hdHead());
        running.forEach(function (h) { frag.appendChild(hdNode(h, canStop)); });
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
  // hdNode is one holder's row (its node, which a later level nests under).
  function hdNode(h, canStop) {
    var node = mk("div", "hd-node"), durable = hdIsDurable(h), lost = !h.attached;
    node.setAttribute("data-node", h.id);
    var row = node.appendChild(mk("div", "hd-row" + (lost ? " hd-lost" : "")));
    row.setAttribute("data-id", h.id);
    row.style.setProperty("--lv", "0");
    row.title = durable ? wsT("js.holders.inproc.title") : h.id;
    row.appendChild(mk("span", "hd-chev"));
    var main = row.appendChild(mk("div", "hd-main")), l1 = main.appendChild(mk("div", "hd-l1"));
    l1.appendChild(mk("span", "hd-dot"));
    l1.appendChild(mk("b", "hd-name", durable ? h.id.slice(HD_DURABLE.length) : hdLabel(h)));
    var kind = h.session && HD_KIND[h.session.kind];
    if (durable) l1.appendChild(mk("span", "hd-badge hd-dur", wsT("js.holders.inproc")));
    else if (kind) l1.appendChild(mk("span", "hd-badge", wsT(kind)));
    var meta = main.appendChild(mk("div", "hd-meta"));
    if (h.pid) meta.appendChild(mk("span", "hd-pid", wsT("js.hd.pid", h.pid)));
    if (h.session && typeof h.session.backend === "string" && h.session.backend) meta.appendChild(mk("span", null, h.session.backend));
    if (h.started_at) meta.appendChild(hdAge(h.started_at, "js.hd.started"));
    if (lost && !durable) meta.appendChild(mk("span", "hd-cause", wsT(HD_CAUSE[h.cause] || "js.hd.cause.unknown", h.protocol)));
    if (lost && durable) meta.appendChild(mk("span", "hd-cause", wsT("js.hd.cause.unknown")));
    if (!lost && h.restart_safe === false) {
      var rs = meta.appendChild(mk("span", "hd-unsafe", wsT("js.hd.unsafe")));
      rs.title = wsT("js.hd.unsafe.title");
    }
    var rss = row.appendChild(mk("span", "hd-num hd-mx"));
    rss.setAttribute("data-k", h.id + ":rss");
    var cpu = row.appendChild(mk("span", "hd-num hd-cpu hd-mx"));
    cpu.setAttribute("data-k", h.id + ":cpu");
    row.appendChild(mk("span", "hd-trend hd-mx"));
    var act = row.appendChild(mk("span", "hd-act"));
    if (lost && !durable && canStop) {
      var b = act.appendChild(hdBtn("hd-btn hd-danger hd-hold d-hold-btn", wsT("js.hd.stop"), "stop", wsT("js.hd.stop.title") + " — " + wsT("js.holders.hold.hint")));
      b.setAttribute("data-hold-mode", "bar");
      b.setAttribute("data-act", "holder-stop");
      b.setAttribute("data-id", h.id);
    }
    return node;
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
    doc.addEventListener("visibilitychange", function () { if (!doc.hidden) hdFresh(); });
    window.addEventListener("focus", hdFresh);
  }
