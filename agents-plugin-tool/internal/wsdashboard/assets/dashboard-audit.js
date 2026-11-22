  // --- audit view: call list controller, lazy hydration, pinning, darkroom & keys ---
  var AU_PIN_MAX = 4;
  var AU_PIN_STEP = 24;
  var AU_WIDE_MIN = 1100;

  var auWindows = [];
  var auFocusedWin = null;
  var auWinSeq = 0;
  var auScrimEl = null;
  var auSheetBackEl = null;
  var auHydrateTimer = null;
  var auHydratedSessions = Object.create(null);

  function auIsWide() {
    if (window.innerWidth < AU_WIDE_MIN) return false;
    var grid = $("grid");
    if (grid && grid.getAttribute("data-layout") === "tabs") return false;
    return true;
  }

  function auSessionRid() {
    return root.getAttribute("data-rid") || rid || "";
  }

  function auSessionHarness() {
    var hb = doc.querySelector(".tph-hs .hb[data-h]");
    return hb ? hb.getAttribute("data-h") || "" : "";
  }

  function auCallElements() {
    var box = $("convo");
    if (!box) return [];
    return q(".au-row[data-call]", box);
  }

  function auRevealCallRow(row) {
    if (!row) return;
    var tools = row.closest(".au-tools");
    if (tools) {
      var fold = tools.querySelector(".au-fold");
      var btn = tools.querySelector(".au-more");
      if (fold && fold.hidden) {
        fold.hidden = false;
        if (btn) {
          btn.setAttribute("aria-expanded", "true");
          btn.textContent = btn.getAttribute("data-less-label") || btn.textContent;
        }
      }
    }
    if (typeof row.scrollIntoView === "function") {
      try { row.scrollIntoView({ block: "nearest" }); } catch (e) {}
    }
  }

  function auSyncSelectedMarks() {
    var activeCalls = Object.create(null);
    for (var i = 0; i < auWindows.length; i++) {
      if (auWindows[i].callId) activeCalls[auWindows[i].callId] = true;
    }
    var rows = auCallElements();
    for (var j = 0; j < rows.length; j++) {
      var id = rows[j].getAttribute("data-call");
      rows[j].classList.toggle("au-sel", !!activeCalls[id]);
    }
  }

  function auClampPosition(left, top, width, height) {
    var vw = window.innerWidth || 1280;
    var vh = window.innerHeight || 800;
    var maxW = Math.max(320, vw - 24);
    var maxH = Math.max(260, vh - 54);
    var w = Math.min(Math.max(360, width), maxW);
    var h = Math.min(Math.max(300, height), maxH);
    var l = Math.max(12, Math.min(vw - w - 12, left));
    var t = Math.max(42, Math.min(vh - h - 12, top));
    return { left: Math.round(l), top: Math.round(t), width: Math.round(w), height: Math.round(h) };
  }

  function auComputeWidePlacement(step) {
    var vw = window.innerWidth || 1440;
    var vh = window.innerHeight || 900;
    var c = $("convo");
    var cr = c ? c.getBoundingClientRect() : null;
    var convoRight = cr && cr.width > 0 ? cr.right : Math.round(vw * 0.44);
    var availRight = vw - convoRight - 28;
    var w = Math.min(720, Math.max(460, availRight));
    var h = Math.min(Math.round(vh * 0.8), 760);
    var baseLeft = availRight >= 440 ? convoRight + 14 : vw - w - 20;
    var baseTop = Math.max(46, Math.round((vh - h) * 0.35));
    return auClampPosition(baseLeft + (step || 0) * AU_PIN_STEP, baseTop + (step || 0) * AU_PIN_STEP, w, h);
  }

  function auEnsureBackdrop() {
    if (!auScrimEl) {
      auScrimEl = doc.createElement("div");
      auScrimEl.className = "au-scrim";
      auScrimEl.hidden = true;
      auScrimEl.addEventListener("click", function () {
        auCloseUnpinned();
      });
      doc.body.appendChild(auScrimEl);
    }
    if (!auSheetBackEl) {
      auSheetBackEl = doc.createElement("div");
      auSheetBackEl.className = "au-sheet-backdrop";
      auSheetBackEl.hidden = true;
      auSheetBackEl.addEventListener("click", function () {
        auCloseUnpinned();
      });
      doc.body.appendChild(auSheetBackEl);
    }
  }

  function auSyncDarkroom() {
    auEnsureBackdrop();
    var hasUnpinned = auWindows.some(function (w) { return !w.pinned; });
    var wide = auIsWide();
    var darkroom = wide && hasUnpinned;
    var sheet = !wide && hasUnpinned;
    root.toggleAttribute("data-au-darkroom", darkroom);
    if (auScrimEl) auScrimEl.hidden = !darkroom;
    if (auSheetBackEl) auSheetBackEl.hidden = !sheet;
    doc.dispatchEvent(new CustomEvent("ws-audit"));
  }

  function auFocusWindow(win) {
    auFocusedWin = win || null;
    for (var i = 0; i < auWindows.length; i++) {
      auWindows[i].el.classList.toggle("au-focus", auWindows[i] === win);
    }
  }

  function auUpdateNavButtons(win) {
    var rows = auCallElements();
    var idx = -1;
    for (var i = 0; i < rows.length; i++) {
      if (rows[i].getAttribute("data-call") === win.callId) { idx = i; break; }
    }
    var prevBtn = win.el.querySelector(".au-prev");
    var nextBtn = win.el.querySelector(".au-next");
    if (prevBtn) prevBtn.disabled = idx <= 0;
    if (nextBtn) nextBtn.disabled = idx < 0 || idx >= rows.length - 1;
  }

  function auSyncWindowHeader(win) {
    var badgeSlot = win.el.querySelector(".au-hbadge");
    var nameEl = win.el.querySelector(".au-wname");
    var statEl = win.el.querySelector(".au-wstatus");
    var pinBtn = win.el.querySelector(".au-pin");
    var crumbEl = win.el.querySelector(".au-crumb");
    var crumbLbl = win.el.querySelector(".au-crumb-label");

    var harness = (win.activeEnvelope && win.activeEnvelope.harness) || auSessionHarness();
    if (badgeSlot) {
      badgeSlot.innerHTML = harness ? '<span class="hb sm" data-h="' + auEsc(harness) + '"></span>' : "";
    }
    var title = win.toolLabel || (win.callSummary && win.callSummary.title) || win.callId || wsT("js.audit.window.title");
    if (win.activeEnvelope && win.inspectEventId) {
      title = (win.activeEnvelope.kind || "") + (win.activeEnvelope.subkind ? ":" + win.activeEnvelope.subkind : "");
    }
    if (nameEl) nameEl.textContent = title;
    win.el.setAttribute("aria-label", title);

    if (statEl) {
      if (win.loading) {
        statEl.textContent = wsT("js.audit.loading");
        statEl.className = "au-wstatus";
        statEl.hidden = false;
      } else if (win.callSummary || win.activeEnvelope) {
        var err = win.activeEnvelope && win.activeEnvelope.meta && (win.activeEnvelope.meta.is_error || win.activeEnvelope.meta.error);
        statEl.textContent = err ? wsT("js.audit.status.failed") : wsT("js.audit.status.ok");
        statEl.className = "au-wstatus " + (err ? "au-err" : "au-ok");
        statEl.hidden = false;
      } else {
        statEl.hidden = true;
      }
    }
    if (pinBtn) pinBtn.setAttribute("aria-pressed", win.pinned ? "true" : "false");

    if (crumbEl && crumbLbl) {
      if (win.inspectEventId) {
        crumbEl.hidden = false;
        crumbLbl.textContent = wsT("js.audit.inspecting", win.inspectEventId);
      } else {
        crumbEl.hidden = true;
      }
    }

    var cntEl = win.el.querySelector('[data-au-tab="events"] .au-tab-cnt');
    if (cntEl) {
      var n = Array.isArray(win.linkedEvents) ? win.linkedEvents.length : 0;
      cntEl.textContent = String(n);
      cntEl.hidden = n === 0;
    }

    var tabs = q(".au-tab", win.el);
    for (var i = 0; i < tabs.length; i++) {
      var t = tabs[i].getAttribute("data-au-tab");
      tabs[i].setAttribute("aria-selected", t === win.tab ? "true" : "false");
    }
    auUpdateNavButtons(win);
  }

  function auRenderWindowBody(win) {
    auSyncWindowHeader(win);
    var tbar = win.el.querySelector(".au-tbar");
    var panel = win.el.querySelector(".au-panel");
    var wrapBtn = win.el.querySelector(".au-wrap-btn");
    var scntEl = win.el.querySelector(".au-scnt");
    if (!panel) return;

    var codeTab = win.tab === "in" || win.tab === "out" || win.tab === "raw";
    if (tbar) tbar.hidden = !codeTab || !!win.errorState || !!win.emptyState;
    if (wrapBtn) wrapBtn.setAttribute("aria-pressed", win.wrap ? "true" : "false");

    if (win.loading) {
      panel.innerHTML = '<div class="au-state">' + auEsc(wsT("js.audit.loading")) + "</div>";
      win.currentPlain = "";
      return;
    }
    if (win.errorState) {
      panel.innerHTML = auRenderWindowError(win.errorState);
      win.currentPlain = "";
      return;
    }
    if (win.emptyState) {
      panel.innerHTML = '<div class="au-state"><strong>' + auEsc(wsT("js.audit.empty.title")) + "</strong><span>" + auEsc(wsT("js.audit.empty.sub")) + "</span></div>";
      win.currentPlain = "";
      return;
    }

    if (win.tab === "meta") {
      panel.innerHTML = auRenderMetaGrid(win);
      win.currentPlain = "";
      return;
    }
    if (win.tab === "events") {
      panel.innerHTML = auRenderLinkedEventsTab(win);
      win.currentPlain = "";
      return;
    }

    var ev = win.activeEnvelope;
    if (!ev) {
      panel.innerHTML = '<div class="au-state">' + auEsc(wsT("js.audit.part.empty")) + "</div>";
      win.currentPlain = "";
      return;
    }

    var res;
    var opts = { wrap: win.wrap, search: win.search, activeMatch: win.activeMatch };
    if (win.tab === "raw") {
      res = auRenderRawTab(ev, opts);
    } else {
      res = auRenderBodyContent(ev, win.tab, opts);
    }
    panel.innerHTML = res.html;
    win.matchCount = res.matchCount;
    win.currentPlain = res.plainText || "";

    if (scntEl) {
      if (win.search) {
        var cur = res.matchCount > 0 ? ((win.activeMatch % res.matchCount) + res.matchCount) % res.matchCount + 1 : 0;
        scntEl.textContent = res.matchCount > 0 ? cur + "/" + res.matchCount : "0";
      } else {
        scntEl.textContent = "";
      }
    }
    if (win.search && res.matchCount > 0) {
      var curHit = panel.querySelector(".au-hit.au-hit-cur");
      if (curHit && typeof curHit.scrollIntoView === "function") {
        try { curHit.scrollIntoView({ block: "nearest", inline: "nearest" }); } catch (e) {}
      }
    }
  }

  function auFetchEnvelope(win, eventId) {
    if (!eventId) {
      win.loading = false;
      win.activeEnvelope = null;
      auRenderWindowBody(win);
      return;
    }
    if (win.cache[eventId]) {
      win.loading = false;
      win.errorState = null;
      win.activeEnvelope = win.cache[eventId];
      auRenderWindowBody(win);
      return;
    }
    var reqToken = ++win.reqToken;
    win.loading = true;
    win.errorState = null;
    auRenderWindowBody(win);
    var url = "/s/" + encodeURIComponent(auSessionRid()) + "/audit/event/" + encodeURIComponent(eventId);
    fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (!r.ok) return r.text().then(function (msg) { throw { status: r.status, message: msg }; });
      return r.json();
    }).then(function (ev) {
      if (reqToken !== win.reqToken || !win.el.isConnected) return;
      win.loading = false;
      win.cache[eventId] = ev;
      win.activeEnvelope = ev;
      auRenderWindowBody(win);
    }).catch(function (err) {
      if (reqToken !== win.reqToken || !win.el.isConnected) return;
      win.loading = false;
      win.errorState = { status: err && err.status || 0, message: err && err.message || String(err) };
      auRenderWindowBody(win);
    });
  }

  function auLoadCallIntoWindow(win, callId, rowEl, opts) {
    opts = opts || {};
    var reqToken = ++win.reqToken;
    win.callId = callId;
    win.inspectEventId = null;
    win.activeSummary = null;
    win.callSummary = null;
    win.activeEnvelope = null;
    win.linkedEvents = [];
    win.errorState = null;
    win.emptyState = false;
    win.loading = true;
    win.activeMatch = 0;

    if (rowEl) {
      var tn = rowEl.querySelector(".au-tool");
      var ag = rowEl.querySelector(".au-args");
      win.toolLabel = (tn ? tn.textContent : callId) + (ag && ag.textContent ? " · " + ag.textContent : "");
    } else if (!win.toolLabel) {
      win.toolLabel = callId;
    }
    auSyncSelectedMarks();
    auRenderWindowBody(win);

    var url = "/s/" + encodeURIComponent(auSessionRid()) + "/audit/events?call=" + encodeURIComponent(callId) +
      (opts.restart ? "&from=oldest" : "");
    fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (!r.ok) return r.text().then(function (msg) { throw { status: r.status, message: msg }; });
      return r.json();
    }).then(function (ans) {
      if (reqToken !== win.reqToken || !win.el.isConnected) return;
      win.callSummary = ans && ans.call ? ans.call : null;
      win.linkedEvents = Array.isArray(ans && ans.events) ? ans.events : [];
      var targetId = win.callSummary ? win.callSummary.id : (win.linkedEvents[0] ? win.linkedEvents[0].id : "");
      if (!targetId) {
        win.loading = false;
        win.emptyState = true;
        auRenderWindowBody(win);
        return;
      }
      win.primaryEventId = targetId;
      auFetchEnvelope(win, targetId);
    }).catch(function (err) {
      if (reqToken !== win.reqToken || !win.el.isConnected) return;
      win.loading = false;
      win.errorState = { status: err && err.status || 0, message: err && err.message || String(err) };
      auRenderWindowBody(win);
    });
  }

  function auLoadMorePart(win, part, offset) {
    var ev = win.activeEnvelope;
    if (!ev || !ev.id) return;
    var btn = win.el.querySelector('[data-au-more="' + part + '"]');
    if (btn) btn.disabled = true;
    var panel = win.el.querySelector(".au-panel");
    var scrollTop = panel ? panel.scrollTop : 0;
    var url = "/s/" + encodeURIComponent(auSessionRid()) + "/audit/event/" + encodeURIComponent(ev.id) +
      "?part=" + encodeURIComponent(part) + "&offset=" + encodeURIComponent(String(offset)) + "&len=" + AU_RANGE_CHUNK;
    fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (!r.ok) return r.text().then(function (msg) { throw { status: r.status, message: msg }; });
      return r.json();
    }).then(function (chunk) {
      if (!win.el.isConnected || win.activeEnvelope !== ev) return;
      if (chunk && typeof chunk.data === "string") {
        ev[part] = (ev[part] || "") + chunk.data;
      }
      if (chunk && chunk.end && ev.range) {
        delete ev.range[part];
        if (!Object.keys(ev.range).length) ev.truncated = false;
      } else if (chunk && ev.range && ev.range[part]) {
        ev.range[part].offset = chunk.next;
      }
      auRenderWindowBody(win);
      var newPanel = win.el.querySelector(".au-panel");
      if (newPanel) newPanel.scrollTop = scrollTop;
    }).catch(function () {
      if (btn) btn.disabled = false;
      toast(wsT("js.audit.loadmore.failed"));
    });
  }

  function auCopyPlain(win) {
    var text = win.currentPlain || "";
    var btn = win.el.querySelector(".au-copy-btn");
    var done = function (ok) {
      if (!ok) { toast(wsT("js.entry.copyfail")); return; }
      if (btn) {
        btn.textContent = wsT("js.audit.copied");
        setTimeout(function () { if (btn.isConnected) btn.textContent = wsT("js.audit.copy"); }, 1400);
      }
    };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(function () { done(true); }, function () { done(false); });
    } else if (typeof copyText === "function") {
      copyText(text, true);
      done(true);
    } else {
      done(false);
    }
  }

  function auCloseWindow(win) {
    if (!win) return;
    var idx = auWindows.indexOf(win);
    if (idx >= 0) auWindows.splice(idx, 1);
    if (win.el && win.el.parentNode) win.el.parentNode.removeChild(win.el);
    if (auFocusedWin === win) {
      auFocusedWin = auWindows.length ? auWindows[auWindows.length - 1] : null;
      if (auFocusedWin) auFocusedWin.el.classList.add("au-focus");
    }
    auSyncSelectedMarks();
    auSyncDarkroom();
  }

  function auCloseUnpinned() {
    var removed = false;
    for (var i = auWindows.length - 1; i >= 0; i--) {
      if (!auWindows[i].pinned) {
        var w = auWindows[i];
        auWindows.splice(i, 1);
        if (w.el && w.el.parentNode) w.el.parentNode.removeChild(w.el);
        removed = true;
      }
    }
    if (auFocusedWin && auWindows.indexOf(auFocusedWin) < 0) {
      auFocusedWin = auWindows.length ? auWindows[auWindows.length - 1] : null;
    }
    if (removed) {
      auSyncSelectedMarks();
      auSyncDarkroom();
    }
  }

  function auTogglePin(win) {
    if (!win) return;
    if (win.pinned) {
      win.pinned = false;
      auSyncWindowHeader(win);
      auSyncDarkroom();
      return;
    }
    var pinnedList = auWindows.filter(function (w) { return w.pinned && w !== win; });
    if (pinnedList.length >= AU_PIN_MAX) {
      auCloseWindow(pinnedList[0]);
      toast(wsT("js.audit.pin.limit"));
    }
    win.pinned = true;
    auSyncWindowHeader(win);
    auSyncDarkroom();
  }

  function auStepCall(win, dir) {
    var rows = auCallElements();
    if (!rows.length) return;
    var idx = -1;
    for (var i = 0; i < rows.length; i++) {
      if (rows[i].getAttribute("data-call") === win.callId) { idx = i; break; }
    }
    var nextIdx = idx + dir;
    if (nextIdx < 0 || nextIdx >= rows.length) return;
    var nextRow = rows[nextIdx];
    auRevealCallRow(nextRow);
    auLoadCallIntoWindow(win, nextRow.getAttribute("data-call"), nextRow);
  }

  function auBindWindowDragResize(win) {
    var hd = win.el.querySelector(".au-whd");
    var rz = win.el.querySelector(".au-resize");
    if (hd) {
      hd.addEventListener("pointerdown", function (e) {
        if (!auIsWide() || e.button !== 0 || (e.target && e.target.closest("button"))) return;
        e.preventDefault();
        auFocusWindow(win);
        var rect = win.el.getBoundingClientRect();
        var sx = e.clientX, sy = e.clientY, sl = rect.left, st = rect.top;
        var move = function (ev) {
          var box = auClampPosition(sl + (ev.clientX - sx), st + (ev.clientY - sy), rect.width, rect.height);
          win.el.style.left = box.left + "px";
          win.el.style.top = box.top + "px";
        };
        var up = function () {
          doc.removeEventListener("pointermove", move);
          doc.removeEventListener("pointerup", up);
        };
        doc.addEventListener("pointermove", move);
        doc.addEventListener("pointerup", up);
      });
    }
    if (rz) {
      rz.addEventListener("pointerdown", function (e) {
        if (!auIsWide() || e.button !== 0) return;
        e.preventDefault();
        auFocusWindow(win);
        var rect = win.el.getBoundingClientRect();
        var sx = e.clientX, sy = e.clientY, sw = rect.width, sh = rect.height;
        var move = function (ev) {
          var box = auClampPosition(rect.left, rect.top, sw + (ev.clientX - sx), sh + (ev.clientY - sy));
          win.el.style.width = box.width + "px";
          win.el.style.height = box.height + "px";
        };
        var up = function () {
          doc.removeEventListener("pointermove", move);
          doc.removeEventListener("pointerup", up);
        };
        doc.addEventListener("pointermove", move);
        doc.addEventListener("pointerup", up);
      });
    }
  }

  function auAttachWindowEvents(win) {
    win.el.addEventListener("pointerdown", function () { auFocusWindow(win); });
    win.el.addEventListener("focusin", function () { auFocusWindow(win); });
    win.el.addEventListener("click", function (e) {
      var t = e.target;
      if (t.closest(".au-close")) { auCloseWindow(win); return; }
      if (t.closest(".au-pin")) { auTogglePin(win); return; }
      if (t.closest(".au-prev")) { auStepCall(win, -1); return; }
      if (t.closest(".au-next")) { auStepCall(win, 1); return; }
      if (t.closest(".au-wrap-btn")) { win.wrap = !win.wrap; auRenderWindowBody(win); return; }
      if (t.closest(".au-copy-btn")) { auCopyPlain(win); return; }
      if (t.closest(".au-sprev")) {
        if (win.matchCount > 0) {
          win.activeMatch = (win.activeMatch - 1 + win.matchCount) % win.matchCount;
          auRenderWindowBody(win);
        }
        return;
      }
      if (t.closest(".au-snext")) {
        if (win.matchCount > 0) {
          win.activeMatch = (win.activeMatch + 1) % win.matchCount;
          auRenderWindowBody(win);
        }
        return;
      }
      if (t.closest(".au-crumb-back")) {
        win.inspectEventId = null;
        win.activeSummary = null;
        auFetchEnvelope(win, win.primaryEventId);
        return;
      }
      var tabBtn = t.closest(".au-tab");
      if (tabBtn) {
        var nextTab = tabBtn.getAttribute("data-au-tab");
        if (nextTab && AU_TABS.indexOf(nextTab) >= 0) {
          win.tab = nextTab;
          win.activeMatch = 0;
          auRenderWindowBody(win);
        }
        return;
      }
      var moreBtn = t.closest("[data-au-more]");
      if (moreBtn) {
        auLoadMorePart(win, moreBtn.getAttribute("data-au-more"), Number(moreBtn.getAttribute("data-au-offset") || "0"));
        return;
      }
      var evBtn = t.closest("[data-au-ev]");
      if (evBtn) {
        var evId = evBtn.getAttribute("data-au-ev");
        win.inspectEventId = evId;
        win.activeSummary = (win.linkedEvents || []).find(function (x) { return x.id === evId; }) || null;
        win.tab = "in";
        auFetchEnvelope(win, evId);
        return;
      }
      if (t.closest("[data-au-restart]")) {
        auLoadCallIntoWindow(win, win.callId, null, { restart: true });
        return;
      }
      if (t.closest("[data-au-retry]")) {
        if (win.inspectEventId) auFetchEnvelope(win, win.inspectEventId);
        else auLoadCallIntoWindow(win, win.callId, null);
      }
    });

    var sinp = win.el.querySelector(".au-sinp");
    if (sinp) {
      sinp.addEventListener("input", function () {
        win.search = sinp.value || "";
        win.activeMatch = 0;
        auRenderWindowBody(win);
      });
      sinp.addEventListener("keydown", function (e) {
        if (e.key === "Enter" && win.matchCount > 0) {
          e.preventDefault();
          win.activeMatch = e.shiftKey
            ? (win.activeMatch - 1 + win.matchCount) % win.matchCount
            : (win.activeMatch + 1) % win.matchCount;
          auRenderWindowBody(win);
        }
      });
    }
    auBindWindowDragResize(win);
  }

  function auOpenCall(callId, rowEl) {
    if (!callId) return;
    var unpinned = null;
    for (var i = auWindows.length - 1; i >= 0; i--) {
      if (!auWindows[i].pinned) { unpinned = auWindows[i]; break; }
    }
    var win = unpinned;
    if (!win) {
      win = {
        id: ++auWinSeq,
        callId: callId,
        pinned: false,
        tab: "in",
        wrap: false,
        search: "",
        activeMatch: 0,
        matchCount: 0,
        reqToken: 0,
        cache: Object.create(null),
        linkedEvents: []
      };
      auBuildWindowDOM(win);
      var pinnedCount = auWindows.filter(function (w) { return w.pinned; }).length;
      var box = auComputeWidePlacement(pinnedCount);
      win.el.style.left = box.left + "px";
      win.el.style.top = box.top + "px";
      win.el.style.width = box.width + "px";
      win.el.style.height = box.height + "px";
      auAttachWindowEvents(win);
      doc.body.appendChild(win.el);
      auWindows.push(win);
    }
    auFocusWindow(win);
    auSyncDarkroom();
    auLoadCallIntoWindow(win, callId, rowEl);
    win.el.focus();
  }

  // --- Lazy post-render hydration for call rows ---
  function auApplyHydration(summaries, markChecked) {
    var byCall = Object.create(null);
    var seenRequests = Object.create(null);
    // Summaries arrive newest first or oldest first; process oldest first so
    // first call per request_id gets "+delta" and parallel siblings get "·".
    var ordered = Array.isArray(summaries)
      ? summaries.slice().sort(function (a, b) { return (a.seq || 0) - (b.seq || 0); })
      : [];
    for (var i = 0; i < ordered.length; i++) {
      var ev = ordered[i];
      if (!ev || !ev.call_id) continue;
      if (ev.kind === "tool_call") {
        byCall[ev.call_id] = ev;
      } else if (ev.kind === "tool_result" && byCall[ev.call_id]) {
        if (ev.subkind === "error" || (ev.title && ev.title.indexOf("error") === 0)) {
          byCall[ev.call_id]._auErr = true;
        }
      }
    }
    var rows = auCallElements();
    for (var j = 0; j < rows.length; j++) {
      var row = rows[j];
      var cid = row.getAttribute("data-call");
      var s = cid ? byCall[cid] : null;
      if (s || markChecked) row.setAttribute("data-au-hydrated", "1");
      if (!s) continue;

      if (s._auErr && !row.classList.contains("au-fail")) {
        row.classList.remove("au-done");
        row.classList.add("au-fail");
        var mk = row.querySelector(".au-mk");
        if (mk && mk.textContent !== "\u2717") mk.textContent = "\u2717";
      }
      var durEl = row.querySelector(".au-dur");
      if (durEl && typeof s.duration_ms === "number" && s.duration_ms > 0) {
        var durText = auFmtSpan(s.duration_ms);
        if (durEl.textContent !== durText) durEl.textContent = durText;
        durEl.title = s.duration_ms + " ms";
      }
      var tokEl = row.querySelector(".au-tok");
      if (tokEl) {
        var tg = auTokenGrowthDisplay(s, seenRequests);
        if (tokEl.textContent !== tg.text) tokEl.textContent = tg.text;
        tokEl.classList.toggle("au-sib", !!tg.sibling);
        if (tg.delta != null) {
          tokEl.title = (s.ctx_before + " \u2192 " + s.ctx_after);
        } else if (tg.sibling && s.request_id) {
          tokEl.title = s.request_id;
        }
      }
      var attEl = row.querySelector(".au-att");
      var attCnt = Array.isArray(s.attached) ? s.attached.length : 0;
      if (attEl) {
        if (attCnt > 0) {
          var attText = "+" + attCnt;
          if (attEl.textContent !== attText) attEl.textContent = attText;
          attEl.hidden = false;
        } else {
          attEl.hidden = true;
        }
      }
    }
  }

  function auHydrateCalls() {
    auHydrateTimer = null;
    var sessionId = auSessionRid();
    if (!sessionId || !window.fetch) return;
    var unhydrated = q('#convo .au-row[data-call]:not([data-au-hydrated])');
    if (!unhydrated.length) return;
    if (auHydratedSessions[sessionId] === "pending") return;
    auHydratedSessions[sessionId] = "pending";
    fetch("/s/" + encodeURIComponent(sessionId) + "/audit/events?n=200", { credentials: "same-origin", cache: "no-store" })
      .then(function (r) {
        if (!r.ok) return null;
        return r.json();
      })
      .then(function (page) {
        var evs = page && Array.isArray(page.events) ? page.events : [];
        auHydratedSessions[sessionId] = evs;
        auApplyHydration(evs, true);
      })
      .catch(function () {
        auHydratedSessions[sessionId] = [];
        auApplyHydration([], true);
      });
  }

  function auScheduleHydration() {
    var sessionId = auSessionRid();
    if (Array.isArray(auHydratedSessions[sessionId]) && auHydratedSessions[sessionId].length) {
      auApplyHydration(auHydratedSessions[sessionId], false);
    }
    var unhydrated = q('#convo .au-row[data-call]:not([data-au-hydrated])');
    if (!unhydrated.length || auHydrateTimer) return;
    auHydrateTimer = setTimeout(auHydrateCalls, 0);
  }

  // --- Event delegation on #convo, keyboard scope & layout sync ---
  doc.addEventListener("click", function (e) {
    var t = e.target;
    var moreBtn = t && t.closest ? t.closest(".au-more") : null;
    if (moreBtn) {
      var tools = moreBtn.closest(".au-tools");
      var fold = tools ? tools.querySelector(".au-fold") : null;
      if (fold) {
        var open = fold.hidden;
        fold.hidden = !open;
        moreBtn.setAttribute("aria-expanded", open ? "true" : "false");
        moreBtn.textContent = moreBtn.getAttribute(open ? "data-less-label" : "data-more-label") || moreBtn.textContent;
      }
      return;
    }
    var row = t && t.closest ? t.closest(".au-row[data-call]") : null;
    if (row && $("convo") && $("convo").contains(row)) {
      var sel = window.getSelection && window.getSelection();
      if (sel && !sel.isCollapsed) return;
      auOpenCall(row.getAttribute("data-call"), row);
    }
  });

  doc.addEventListener("keydown", function (e) {
    var t = e.target;
    if ((e.key === "Enter" || e.key === " ") && t && t.matches && t.matches(".au-row[data-call]")) {
      e.preventDefault();
      auOpenCall(t.getAttribute("data-call"), t);
      return;
    }

    var inAuditWin = t && t.closest ? t.closest(".au-win") : null;
    if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && (e.key === "f" || e.key === "F")) {
      if (inAuditWin) {
        e.preventDefault();
        e.stopPropagation();
        var inp = inAuditWin.querySelector(".au-sinp");
        if (inp) { inp.focus(); inp.select(); }
      }
      return;
    }

    if (e.key === "Escape" && auWindows.length > 0) {
      e.preventDefault();
      if (typeof e.stopImmediatePropagation === "function") e.stopImmediatePropagation();
      else e.stopPropagation();
      var targetWin = auFocusedWin && auWindows.indexOf(auFocusedWin) >= 0
        ? auFocusedWin
        : auWindows[auWindows.length - 1];
      auCloseWindow(targetWin);
      return;
    }

    if ((e.key === "ArrowLeft" || e.key === "ArrowRight") && !e.ctrlKey && !e.metaKey && !e.altKey && auWindows.length > 0) {
      var tag = t && t.tagName ? t.tagName : "";
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || (t && t.isContentEditable)) return;
      var navWin = (inAuditWin && auWindows.find(function (w) { return w.el === inAuditWin; })) || auFocusedWin || auWindows[auWindows.length - 1];
      if (navWin) {
        e.preventDefault();
        auStepCall(navWin, e.key === "ArrowLeft" ? -1 : 1);
      }
    }
  }, true);

  window.addEventListener("resize", function () {
    auSyncDarkroom();
    if (!auIsWide()) return;
    for (var i = 0; i < auWindows.length; i++) {
      var w = auWindows[i];
      var rect = w.el.getBoundingClientRect();
      var box = auClampPosition(rect.left, rect.top, rect.width || 620, rect.height || 540);
      w.el.style.left = box.left + "px";
      w.el.style.top = box.top + "px";
      w.el.style.width = box.width + "px";
      w.el.style.height = box.height + "px";
    }
  });

  if (window.MutationObserver && $("convo")) {
    new MutationObserver(function (mutations) {
      var hasCallNodes = false;
      for (var i = 0; i < mutations.length; i++) {
        var added = mutations[i].addedNodes;
        for (var k = 0; k < added.length; k++) {
          var n = added[k];
          if (n && n.nodeType === 1 && ((n.matches && (n.matches(".au-tools") || n.matches(".au-row[data-call]"))) || (n.querySelector && n.querySelector(".au-row[data-call]")))) {
            hasCallNodes = true;
            break;
          }
        }
        if (hasCallNodes) break;
      }
      if (!hasCallNodes) return;
      auScheduleHydration();
      auSyncSelectedMarks();
      if (auFocusedWin) auUpdateNavButtons(auFocusedWin);
    }).observe($("convo"), { childList: true, subtree: true });
  }
  auScheduleHydration();

  window.WsAudit = {
    fmtBytes: auFmtBytes,
    fmtSpan: auFmtSpan,
    tokenGrowth: auTokenGrowthDisplay,
    renderBody: auRenderBodyContent,
    applyHydration: auApplyHydration,
    openCall: auOpenCall,
    closeUnpinned: auCloseUnpinned,
    windows: function () { return auWindows.slice(); }
  };
