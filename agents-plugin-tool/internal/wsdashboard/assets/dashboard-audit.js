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
    return q(".au-row[data-call], .au-hid[data-event-id], .au-sysrow[data-event-id]", box);
  }

  function auVisibleAuditRows() {
    return auCallElements().filter(function (el) {
      var parent = el.tagName === "SUMMARY" ? el.parentElement.parentElement : el.parentElement;
      return !el.hidden && !el.closest("[hidden]") && !(parent && parent.closest("details:not([open])"));
    });
  }

  function auRowIndex(win, rows) {
    for (var i = 0; i < rows.length; i++) {
      var evId = rows[i].getAttribute("data-event-id");
      if (win.eventId ? evId === win.eventId : (!evId && rows[i].getAttribute("data-call") === win.callId)) return i;
    }
    return -1;
  }

  function auRevealCallRow(row) {
    if (!row) return;
    if (typeof row.scrollIntoView === "function") {
      try { row.scrollIntoView({ block: "nearest" }); } catch (e) {}
    }
  }

  function auSyncSelectedMarks() {
    var box = $("convo");
    if (!box) return;
    if (!auWindows.length) {
      if (box.querySelector(".au-sel")) {
        var prevSel = q(".au-sel", box);
        for (var p = 0; p < prevSel.length; p++) prevSel[p].classList.remove("au-sel");
      }
      return;
    }
    var activeCalls = Object.create(null), activeEvents = Object.create(null);
    for (var i = 0; i < auWindows.length; i++) {
      if (auWindows[i].eventId) activeEvents[auWindows[i].eventId] = true;
      else if (auWindows[i].callId) activeCalls[auWindows[i].callId] = true;
    }
    var rows = auCallElements();
    for (var j = 0; j < rows.length; j++) {
      var evId = rows[j].getAttribute("data-event-id");
      rows[j].classList.toggle("au-sel", evId ? !!activeEvents[evId] : !!activeCalls[rows[j].getAttribute("data-call")]);
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
    var rows = auVisibleAuditRows(), idx = auRowIndex(win, rows);
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
    if (win.activeEnvelope && (win.inspectEventId || win.eventId)) {
      title = (win.activeEnvelope.kind || "") + (win.activeEnvelope.subkind ? ":" + win.activeEnvelope.subkind : "");
    }
    if (nameEl) nameEl.textContent = title;
    win.el.setAttribute("aria-label", title);

    if (statEl) {
      if (win.loading) {
        statEl.textContent = wsT("js.audit.loading");
        statEl.className = "au-wstatus";
        statEl.hidden = false;
      } else if (win.callSummary || win.activeEnvelope || (!win.inspectEventId && win.resultEnvelope)) {
        var err = auEnvelopeHasError(win.activeEnvelope) || (!win.inspectEventId && auEnvelopeHasError(win.resultEnvelope));
        var est = !err && win.activeEnvelope && win.activeEnvelope.kind === "compaction" && !!(win.activeEnvelope.meta && win.activeEnvelope.meta.estimated);
        statEl.textContent = err ? wsT("js.audit.status.failed") : (est ? wsT("js.audit.compaction.estimated") : wsT("js.audit.status.ok"));
        statEl.className = "au-wstatus " + (err ? "au-err" : (est ? "au-est" : "au-ok"));
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

  var AU_SPANS_DEBOUNCE_MS = 200;

  // The bytes of each stored body this viewer holds, for the spans request:
  // a body paged in by range follow-ups is matched only as far as it is
  // loaded; one loaded whole is matched whole (undefined: the server's
  // default).
  function auLoadedBytes(ev, part) {
    if (ev.range && ev.range[part] && typeof ev.range[part].offset === "number") return ev.range[part].offset;
    if (ev.loadedBytes && typeof ev.loadedBytes[part] === "number") return ev.loadedBytes[part];
    return undefined;
  }

  function auSpansKey(win, ev) {
    var key = [ev.id, win.search, win.regex ? 1 : 0, win.caseSensitive ? 1 : 0];
    ["in", "out", "raw"].forEach(function (part) {
      var n = auLoadedBytes(ev, part);
      key.push(n == null ? "" : n);
    });
    return key.join("\u0001");
  }

  function auSpansUrl(win, ev) {
    var url = "/s/" + encodeURIComponent(auSessionRid()) + "/audit/event/" + encodeURIComponent(ev.id) +
      "?only=matches&q=" + encodeURIComponent(win.search) + (win.regex ? "&regex=1" : "") + (win.caseSensitive ? "&case=1" : "");
    ["in", "out", "raw"].forEach(function (part) {
      var n = auLoadedBytes(ev, part);
      if (n != null) url += "&through_" + part + "=" + n;
    });
    return url;
  }

  function auSpansCancel(win) {
    win.spansToken = (win.spansToken || 0) + 1;
    if (win.spansTimer) { clearTimeout(win.spansTimer); win.spansTimer = 0; }
    win.spansPending = null;
  }

  // The server's match spans for the displayed envelope under the window's
  // find text: {parts} once they are here, {error} when the server refused the
  // query (its text is shown, zero matches), {pending} while they are asked
  // for. The request is debounced when the find box changed, and an answer
  // for a superseded request is dropped (the latest-request guard).
  function auSpansFor(win, ev) {
    var searchable = win.tab === "in" || win.tab === "out" || (win.tab === "raw" && !!ev && ev.kind === "other");
    if (!win.search || !searchable || !ev || !ev.id) {
      if (!win.search || !searchable) { auSpansCancel(win); win.spans = null; }
      win.spansDebounce = false;
      return { parts: null, pending: false, error: "" };
    }
    var key = auSpansKey(win, ev);
    if (win.spans && win.spans.key === key) {
      return { parts: win.spans.parts || null, pending: false, error: win.spans.error || "" };
    }
    if (!win.spansPending || win.spansPending.key !== key) {
      auSpansCancel(win);
      var token = win.spansToken;
      win.spansPending = { key: key };
      var run = function () {
        win.spansTimer = 0;
        fetch(auSpansUrl(win, ev), { credentials: "same-origin", cache: "no-store" }).then(function (r) {
          return r.text().then(function (txt) {
            var body = null;
            try { body = JSON.parse(txt); } catch (e) {}
            if (!r.ok) throw { message: (body && body.error) || txt || ("HTTP " + r.status) };
            return body || {};
          });
        }).then(function (ans) {
          if (token !== win.spansToken || !win.el.isConnected) return;
          win.spansPending = null;
          win.spans = { key: key, parts: ans.matches || {} };
          auRenderWindowBody(win);
        }).catch(function (err) {
          if (token !== win.spansToken || !win.el.isConnected) return;
          win.spansPending = null;
          win.spans = { key: key, error: String(err && err.message || err) };
          auRenderWindowBody(win);
        });
      };
      if (win.spansDebounce) win.spansTimer = setTimeout(run, AU_SPANS_DEBOUNCE_MS);
      else run();
      win.spansDebounce = false;
    }
    return { parts: null, pending: true, error: "" };
  }

  function auRenderWindowBody(win) {
    auSyncWindowHeader(win);
    var tbar = win.el.querySelector(".au-tbar");
    var panel = win.el.querySelector(".au-panel");
    var wrapBtn = win.el.querySelector(".au-wrap-btn");
    var scntEl = win.el.querySelector(".au-scnt");
    if (!panel) return;

    var codeTab = win.tab === "in" || win.tab === "out" || win.tab === "raw";
    var outErr = win.tab === "out" && !win.inspectEventId && win.resultError;
    var outLoading = win.tab === "out" && !win.inspectEventId && win.resultLoading && !win.resultEnvelope;
    if (tbar) tbar.hidden = !codeTab || !!win.errorState || !!win.emptyState || !!outErr || !!outLoading;
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
    if (outErr) {
      panel.innerHTML = auRenderWindowError(win.resultError);
      win.currentPlain = "";
      return;
    }
    if (outLoading) {
      panel.innerHTML = '<div class="au-state">' + auEsc(wsT("js.audit.loading")) + "</div>";
      win.currentPlain = "";
      return;
    }

    var ev = auDisplayedEnvelope(win);
    if (!ev) {
      panel.innerHTML = '<div class="au-state">' + auEsc(wsT("js.audit.part.empty")) + "</div>";
      win.currentPlain = "";
      return;
    }

    var res;
    var spans = auSpansFor(win, ev);
    var opts = { wrap: win.wrap, search: win.search, regex: !!win.regex, caseSensitive: !!win.caseSensitive, activeMatch: win.activeMatch, matches: spans.parts };
    if (win.tab === "raw") {
      res = auRenderRawTab(ev, opts);
    } else {
      res = auRenderBodyContent(ev, win.tab, opts);
    }
    panel.innerHTML = res.html;
    win.matchCount = res.matchCount;
    win.currentPlain = res.plainText || "";

    if (scntEl) {
      var scntErr = win.search && spans.error ? spans.error : "";
      scntEl.classList.toggle("au-scnt-err", !!scntErr);
      if (scntErr) scntEl.setAttribute("title", scntErr); else scntEl.removeAttribute("title");
      if (scntErr) {
        scntEl.textContent = scntErr;
      } else if (win.search && spans.pending) {
        scntEl.textContent = "\u2026";
      } else if (win.search) {
        var cur = res.matchCount > 0 ? ((win.activeMatch % res.matchCount) + res.matchCount) % res.matchCount + 1 : 0;
        scntEl.textContent = res.matchCount > 0 ? cur + "/" + res.matchCount + (res.matchCapped ? "+" : "") : "0";
      } else {
        scntEl.textContent = "";
      }
    }
    var sinpEl = win.el.querySelector(".au-sinp");
    if (sinpEl && sinpEl.value !== (win.search || "")) sinpEl.value = win.search || "";
    var jumpEl = win.search && res.matchCount > 0 ? panel.querySelector(".au-hit.au-hit-cur") : null;
    if (jumpEl && typeof jumpEl.scrollIntoView === "function") {
      try { jumpEl.scrollIntoView({ block: "nearest", inline: "nearest" }); } catch (e) {}
    }
  }

  function auIsResultSummary(ev, callId) {
    if (!ev || ev.call_id !== callId) return false;
    return ev.kind === "tool_result" || (ev.kind === "subagent" && ev.subkind === "tool_result");
  }

  function auAutoEventTab(ev) {
    if (!ev) return "in";
    if (ev.kind === "other") return "raw";
    var hasIn = !!(ev.in || (ev.stored_trunc && ev.stored_trunc.in) || (ev.redacted && ev.redacted.in));
    var hasOut = !!(ev.out || (ev.stored_trunc && ev.stored_trunc.out) || (ev.redacted && ev.redacted.out));
    if (ev.kind !== "tool_call" && !hasIn && hasOut) return "out";
    return "in";
  }

  function auFetchEnvelope(win, eventId) {
    var reqToken = ++win.reqToken;
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
      if (win.autoTab) win.tab = auAutoEventTab(win.activeEnvelope);
      win.autoTab = false;
      auRenderWindowBody(win);
      return;
    }
    win.activeEnvelope = null;
    win.resultLoading = false; win.resultEnvelope = null; win.resultError = null;
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
      if (win.autoTab) win.tab = auAutoEventTab(ev);
      win.autoTab = false;
      auRenderWindowBody(win);
    }).catch(function (err) {
      if (reqToken !== win.reqToken || !win.el.isConnected) return;
      win.loading = false;
      win.errorState = { status: err && err.status || 0, message: err && err.message || String(err) };
      auRenderWindowBody(win);
    });
  }

  function auFetchResultEnvelope(win, resultId, reqToken) {
    if (!resultId) {
      win.resultLoading = false;
      win.resultError = null;
      win.resultEnvelope = null;
      return;
    }
    if (win.cache[resultId]) {
      win.resultLoading = false;
      win.resultError = null;
      win.resultEnvelope = win.cache[resultId];
      return;
    }
    win.resultLoading = true;
    win.resultError = null;
    win.resultEnvelope = null;
    var url = "/s/" + encodeURIComponent(auSessionRid()) + "/audit/event/" + encodeURIComponent(resultId);
    fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (!r.ok) return r.text().then(function (msg) { throw { status: r.status, message: msg }; });
      return r.json();
    }).then(function (ev) {
      if (reqToken !== win.reqToken || !win.el.isConnected) return;
      win.resultLoading = false;
      win.resultError = null;
      win.cache[resultId] = ev;
      win.resultEnvelope = ev;
      auRenderWindowBody(win);
    }).catch(function (err) {
      if (reqToken !== win.reqToken || !win.el.isConnected) return;
      win.resultLoading = false;
      win.resultEnvelope = null;
      win.resultError = { status: err && err.status || 0, message: err && err.message || String(err) };
      auRenderWindowBody(win);
    });
  }

  function auFetchCallEnvelopes(win, primaryId, resultId) {
    if (!primaryId) {
      win.loading = false;
      win.activeEnvelope = null;
      win.resultLoading = false;
      win.resultEnvelope = null;
      win.resultError = null;
      auRenderWindowBody(win);
      return;
    }
    var reqToken = ++win.reqToken;
    win.errorState = null;
    if (win.cache[primaryId]) {
      win.loading = false;
      win.activeEnvelope = win.cache[primaryId];
    } else {
      win.loading = true;
      win.activeEnvelope = null;
      var pUrl = "/s/" + encodeURIComponent(auSessionRid()) + "/audit/event/" + encodeURIComponent(primaryId);
      fetch(pUrl, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
        if (!r.ok) return r.text().then(function (msg) { throw { status: r.status, message: msg }; });
        return r.json();
      }).then(function (ev) {
        if (reqToken !== win.reqToken || !win.el.isConnected) return;
        win.loading = false;
        win.cache[primaryId] = ev;
        win.activeEnvelope = ev;
        auRenderWindowBody(win);
      }).catch(function (err) {
        if (reqToken !== win.reqToken || !win.el.isConnected) return;
        win.loading = false;
        win.errorState = { status: err && err.status || 0, message: err && err.message || String(err) };
        auRenderWindowBody(win);
      });
    }
    auFetchResultEnvelope(win, resultId, reqToken);
    auRenderWindowBody(win);
  }

  function auLoadCallIntoWindow(win, callId, rowEl) {
    var reqToken = ++win.reqToken;
    win.callId = callId;
    win.eventId = "";
    win.inspectEventId = null;
    win.autoTab = false;
    win.activeSummary = null;
    win.callSummary = null;
    win.primaryEventId = "";
    win.resultEventId = "";
    win.activeEnvelope = null;
    win.resultEnvelope = null;
    win.resultError = null;
    win.resultLoading = false;
    win.linkedEvents = [];
    win.errorState = null;
    win.emptyState = false;
    win.loading = true;
    win.activeMatch = 0;
    win.search = ""; win.regex = false; win.caseSensitive = false;

    if (rowEl) {
      var tn = rowEl.querySelector(".au-tool");
      var ag = rowEl.querySelector(".au-args");
      win.toolLabel = (tn ? tn.textContent : callId) + (ag && ag.textContent ? " \u00b7 " + ag.textContent : "");
    } else if (!win.toolLabel) {
      win.toolLabel = callId;
    }
    auSyncSelectedMarks();
    auRenderWindowBody(win);

    var url = "/s/" + encodeURIComponent(auSessionRid()) + "/audit/events?call=" + encodeURIComponent(callId);
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
      var resEv = win.linkedEvents.find(function (ev) {
        return ev && ev.id !== targetId && auIsResultSummary(ev, callId);
      });
      win.primaryEventId = targetId;
      win.resultEventId = resEv ? resEv.id : "";
      auFetchCallEnvelopes(win, win.primaryEventId, win.resultEventId);
    }).catch(function (err) {
      if (reqToken !== win.reqToken || !win.el.isConnected) return;
      win.loading = false;
      win.errorState = { status: err && err.status || 0, message: err && err.message || String(err) };
      auRenderWindowBody(win);
    });
  }

  function auDisplayedEnvelope(win) {
    return (win.tab === "out" && !win.inspectEventId && win.resultEnvelope) ? win.resultEnvelope : win.activeEnvelope;
  }

  function auLoadMorePart(win, part, offset, owner) {
    var ev = (part === "out" && !win.inspectEventId && win.resultEnvelope) ? win.resultEnvelope : win.activeEnvelope;
    if (!ev || !ev.id) return Promise.resolve(false);
    win._morePending = win._morePending || new WeakMap();
    if (!win._morePending.has(ev)) win._morePending.set(ev, {});
    var paging = win._morePending.get(ev);
    var existing = paging[part];
    if (existing && !existing.aborted && !existing.controller.signal.aborted && existing.reqToken === win.reqToken) {
      if (!owner) existing.manual = true;
      return existing.promise;
    }
    if (existing) {
      existing.aborted = true;
      if (paging[part] === existing) delete paging[part];
      existing.controller.abort();
    }
    if (!ev.range || !ev.range[part] || ev.range[part].offset !== offset) return Promise.resolve(false);
    var token = win.reqToken;
    var controller = new AbortController();
    var entry = { reqToken: token, controller: controller, aborted: false, manual: !owner };
    var btn = win.el.querySelector('[data-au-more="' + part + '"]');
    if (btn) btn.disabled = true;
    var panel = win.el.querySelector(".au-panel");
    var scrollTop = panel ? panel.scrollTop : 0;
    var url = "/s/" + encodeURIComponent(auSessionRid()) + "/audit/event/" + encodeURIComponent(ev.id) +
      "?part=" + encodeURIComponent(part) + "&offset=" + encodeURIComponent(String(offset)) + "&len=" + AU_RANGE_CHUNK;
    var pending = fetch(url, { credentials: "same-origin", cache: "no-store", signal: controller.signal }).then(function (r) {
      if (!r.ok) return r.text().then(function (msg) { throw { status: r.status, message: msg }; });
      return r.json();
    }).then(function (chunk) {
      var stillActive = (ev === win.activeEnvelope) || (!win.inspectEventId && ev === win.resultEnvelope);
      if (entry.aborted || win.reqToken !== token || (owner && (owner.cancelled || auDisplayedEnvelope(win) !== ev) && !entry.manual) || !win.el.isConnected || !stillActive || !ev.range || !ev.range[part] || ev.range[part].offset !== offset) return false;
      if (!chunk || typeof chunk.data !== "string") return false;
      ev[part] = (ev[part] || "") + chunk.data;
      if (typeof chunk.next === "number") (ev.loadedBytes = ev.loadedBytes || {})[part] = chunk.next;
      if (chunk && chunk.end && ev.range) {
        delete ev.range[part];
        if (!Object.keys(ev.range).length) ev.truncated = false;
      } else if (chunk && ev.range && ev.range[part]) {
        ev.range[part].offset = chunk.next;
      }
      auRenderWindowBody(win);
      var newPanel = win.el.querySelector(".au-panel");
      if (newPanel) newPanel.scrollTop = scrollTop;
      return true;
    }).catch(function () {
      if (btn) btn.disabled = false;
      if (!entry.aborted && !(owner && owner.cancelled)) toast(wsT("js.audit.loadmore.failed"));
      return false;
    }).finally(function () { if (paging[part] === entry) delete paging[part]; });
    entry.promise = pending;
    if (owner) owner.cancel = function () {
      owner.cancelled = true;
      if (entry.manual) return;
      entry.aborted = true;
      if (paging[part] === entry) delete paging[part];
      controller.abort();
    };
    paging[part] = entry;
    return pending;
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
    var active = doc.activeElement;
    var hadFocus = !!(win.el && active && (active === win.el || (win.el.contains && win.el.contains(active))));
    var idx = auWindows.indexOf(win);
    if (idx >= 0) auWindows.splice(idx, 1);
    auSpansCancel(win);
    if (win.el && win.el.parentNode) win.el.parentNode.removeChild(win.el);
    if (auFocusedWin === win) {
      auFocusedWin = auWindows.length ? auWindows[auWindows.length - 1] : null;
      if (auFocusedWin) {
        auFocusedWin.el.classList.add("au-focus");
        if (hadFocus && typeof auFocusedWin.el.focus === "function") auFocusedWin.el.focus();
      }
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
    var rows = auVisibleAuditRows();
    if (!rows.length) return;
    var nextIdx = auRowIndex(win, rows) + dir;
    if (nextIdx < 0 || nextIdx >= rows.length) return;
    var nextRow = rows[nextIdx];
    auRevealCallRow(nextRow);
    var evId = nextRow.getAttribute("data-event-id");
    if (evId) auLoadEventIntoWindow(win, evId, { rowEl: nextRow });
    else auLoadCallIntoWindow(win, nextRow.getAttribute("data-call"), nextRow);
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
        var sx = e.clientX, sy = e.clientY, sl = rect.left, st = rect.top, sw = rect.width, sh = rect.height;
        var move = function (ev) {
          var vw = window.innerWidth || 1280;
          var vh = window.innerHeight || 800;
          var rawW = Math.min(sw + (ev.clientX - sx), vw - 12 - sl);
          var rawH = Math.min(sh + (ev.clientY - sy), vh - 12 - st);
          var box = auClampPosition(sl, st, rawW, rawH);
          win.el.style.left = box.left + "px";
          win.el.style.top = box.top + "px";
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
        auFetchCallEnvelopes(win, win.primaryEventId, win.resultEventId);
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
        auLoadCallIntoWindow(win, win.callId, null);
        return;
      }
      if (t.closest("[data-au-retry]")) {
        if (win.tab === "out" && !win.inspectEventId && win.resultError && win.resultEventId) {
          auFetchResultEnvelope(win, win.resultEventId, win.reqToken);
          auRenderWindowBody(win);
        } else if (win.inspectEventId || win.eventId) {
          auFetchEnvelope(win, win.inspectEventId || win.eventId);
        } else {
          auLoadCallIntoWindow(win, win.callId, null);
        }
      }
    });

    var sinp = win.el.querySelector(".au-sinp");
    if (sinp) {
      sinp.addEventListener("input", function () {
        win.search = sinp.value || "";
        win.activeMatch = 0;
        win.spansDebounce = true;
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

  function auEnsureWindow(initialTab) {
    var win = null;
    for (var i = auWindows.length - 1; i >= 0; i--) {
      if (!auWindows[i].pinned) { win = auWindows[i]; break; }
    }
    if (!win) {
      win = { id: ++auWinSeq, callId: "", eventId: "", pinned: false, tab: initialTab || "in", wrap: false, search: "", regex: false, caseSensitive: false, activeMatch: 0, matchCount: 0, reqToken: 0, spansToken: 0, spansDebounce: false, spans: null, spansPending: null, spansTimer: 0, cache: Object.create(null), linkedEvents: [] };
      auBuildWindowDOM(win);
      var box = auComputeWidePlacement(auWindows.filter(function (w) { return w.pinned; }).length);
      win.el.style.left = box.left + "px"; win.el.style.top = box.top + "px";
      win.el.style.width = box.width + "px"; win.el.style.height = box.height + "px";
      auAttachWindowEvents(win);
      doc.body.appendChild(win.el);
      auWindows.push(win);
    }
    auFocusWindow(win);
    auSyncDarkroom();
    return win;
  }

  function auLoadEventIntoWindow(win, eventId, opts) {
    ++win.reqToken;
    opts = opts || {};
    win.callId = opts.callId || ""; win.eventId = opts.inspectEventId ? "" : eventId; win.inspectEventId = opts.inspectEventId || null;
    win.activeSummary = opts.summary || null; win.callSummary = opts.callSummary || null;
    win.primaryEventId = opts.primaryEventId || ""; win.resultEventId = opts.resultEventId || ""; win.resultEnvelope = null; win.resultError = null; win.resultLoading = false; win.linkedEvents = opts.linkedEvents || [];
    win.errorState = null; win.emptyState = false; win.activeMatch = 0;
    win.search = opts.search || ""; win.regex = !!opts.regex; win.caseSensitive = !!opts.caseSensitive;
    var rowEl = opts.rowEl, kind = opts.kind || (rowEl && rowEl.getAttribute("data-kind")) || (opts.summary && opts.summary.kind) || "";
    var pv = rowEl && rowEl.querySelector(".au-pv, .au-sysrow-lbl");
    win.toolLabel = opts.label || (pv && pv.textContent) || eventId;
    win.autoTab = !opts.tab;
    win.tab = opts.tab || (kind === "other" ? "raw" : "in");
    auSyncSelectedMarks();
    auFetchEnvelope(win, eventId);
  }

  function auOpenCall(callId, rowEl) {
    if (!callId) return;
    var win = auEnsureWindow("in");
    auLoadCallIntoWindow(win, callId, rowEl);
    win.el.focus();
  }

  function auOpenEvent(eventId, opts) {
    if (!eventId) return;
    var win = auEnsureWindow(opts && opts.tab);
    auLoadEventIntoWindow(win, eventId, opts);
    win.el.focus();
  }

  // --- Lazy post-render hydration for call rows ---
  function auApplyHydration(summaries, markChecked, opts) {
    var byCall = Object.create(null);
    var inBytesByCall = Object.create(null);
    var outBytesByCall = Object.create(null);
    var errByCall = Object.create(null);
    var resultByCall = Object.create(null);
    var ordered = Array.isArray(summaries)
      ? summaries.slice().sort(function (a, b) { return (a.seq || 0) - (b.seq || 0); })
      : [];
    for (var i = 0; i < ordered.length; i++) {
      var ev = ordered[i];
      if (!ev || !ev.call_id) continue;
      if (ev.kind === "tool_call") {
        byCall[ev.call_id] = ev;
        if (typeof ev.in_bytes === "number" && isFinite(ev.in_bytes) && ev.in_bytes > 0) {
          inBytesByCall[ev.call_id] = ev.in_bytes;
        }
      } else if (ev.kind === "tool_result") {
        resultByCall[ev.call_id] = true;
        if (typeof ev.out_bytes === "number" && isFinite(ev.out_bytes) && ev.out_bytes > 0) {
          outBytesByCall[ev.call_id] = (outBytesByCall[ev.call_id] || 0) + ev.out_bytes;
        }
        if (ev.subkind === "error" || (ev.title && ev.title.indexOf("error") === 0)) {
          errByCall[ev.call_id] = true;
        }
      }
    }
    var box = $("convo");
    var sel = opts && opts.unhydratedOnly ? ".au-row[data-call]:not([data-au-hydrated])" : ".au-row[data-call]";
    var rows = box ? q(sel, box) : [];
    for (var j = 0; j < rows.length; j++) {
      var row = rows[j];
      var cid = row.getAttribute("data-call");
      if (!cid) continue;
      var s = byCall[cid];
      var inB = inBytesByCall[cid] || 0;
      var outB = outBytesByCall[cid] || 0;
      var hasAny = !!(s || outB > 0 || errByCall[cid] || resultByCall[cid]);
      if (s || markChecked) row.setAttribute("data-au-hydrated", "1");
      if (!hasAny) continue;

      if (errByCall[cid] && !row.classList.contains("au-fail")) {
        row.classList.remove("au-done");
        row.classList.add("au-fail");
        var useEl = row.querySelector(".au-mk use");
        if (useEl) useEl.setAttribute("href", "#i-x");
      }
      var durEl = row.querySelector(".au-dur");
      if (durEl && s && typeof s.duration_ms === "number" && s.duration_ms > 0) {
        var durText = auFmtSpan(s.duration_ms);
        if (durEl.textContent !== durText) durEl.textContent = durText;
        durEl.title = s.duration_ms + " ms";
      }
      var tokEl = row.querySelector(".au-tok");
      if (tokEl) {
        var estText = auEstContextText(inB, outB);
        tokEl.classList.remove("au-sib");
        if (estText) {
          if (tokEl.textContent !== estText) tokEl.textContent = estText;
          tokEl.title = auEstContextTitle(inB, outB, s);
        } else if (s && typeof s.ctx_before === "number" && typeof s.ctx_after === "number" &&
                   isFinite(s.ctx_before) && isFinite(s.ctx_after) && tokEl.textContent) {
          var ctxNote = s.ctx_before + " \u2192 " + s.ctx_after;
          if (!tokEl.title) tokEl.title = ctxNote;
          else if (tokEl.title.indexOf(ctxNote) === -1) tokEl.title = tokEl.title + " \u00b7 " + ctxNote;
        }
      }
      var attEl = row.querySelector(".au-att");
      var attCnt = s && Array.isArray(s.attached) ? s.attached.length : 0;
      if (attEl) {
        if (attCnt > 0) {
          var attText = "+" + attCnt;
          if (attEl.textContent !== attText) attEl.textContent = attText;
          attEl.hidden = false;
        } else {
          attEl.hidden = true;
        }
      }
      var detailEl = row.closest ? row.closest(".au-call-details") : null;
      var rowCompleted = !!(resultByCall[cid] || outB > 0 || errByCall[cid] || (s && typeof s.duration_ms === "number" && s.duration_ms > 0));
      if (rowCompleted && detailEl && (detailEl.hasAttribute("data-au-out-pending") || detailEl.hasAttribute("data-au-out-inflight")) &&
          typeof CustomEvent === "function" && doc && typeof doc.dispatchEvent === "function") {
        doc.dispatchEvent(new CustomEvent("ws-call-details-updated", { detail: { el: detailEl, completed: true } }));
      }
    }
  }

  function auGetHydrationState(sessionId) {
    var st = auHydratedSessions[sessionId];
    if (!st) {
      st = {
        pending: false,
        staleRetried: false,
        initialized: false,
        before: "",
        after: "",
        end: false,
        events: [],
        seenIds: Object.create(null)
      };
      auHydratedSessions[sessionId] = st;
    }
    return st;
  }

  function auMergeHydrationEvents(st, list) {
    if (!Array.isArray(list)) return;
    for (var i = 0; i < list.length; i++) {
      var ev = list[i];
      if (!ev || !ev.id || st.seenIds[ev.id]) continue;
      st.seenIds[ev.id] = true;
      st.events.push(ev);
    }
  }

  function auHasUnhydratedRows() {
    return q('#convo .au-row[data-call]:not([data-au-hydrated])').length > 0;
  }

  function auFetchEventsPage(sessionId, params) {
    var qs = "kinds=tool_call,tool_result&n=500" + (params || "");
    var url = "/s/" + encodeURIComponent(sessionId) + "/audit/events?" + qs;
    return fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (!r.ok) return r.text().then(function (msg) { throw { status: r.status, message: msg }; });
      return r.json();
    });
  }

  function auHydrateCalls() {
    auHydrateTimer = null;
    var sessionId = auSessionRid();
    if (!sessionId || typeof fetch !== "function") return Promise.resolve();
    if (!auHasUnhydratedRows()) return Promise.resolve();
    var st = auGetHydrationState(sessionId);
    if (st.pending) return Promise.resolve();
    st.pending = true;

    function walkForward() {
      if (!st.after || !auHasUnhydratedRows()) return Promise.resolve();
      var prevAfter = st.after;
      return auFetchEventsPage(sessionId, "&after=" + encodeURIComponent(st.after)).then(function (page) {
        var evs = page && Array.isArray(page.events) ? page.events : [];
        auMergeHydrationEvents(st, evs);
        if (page && page.after) st.after = page.after;
        auApplyHydration(st.events, false);
        if (evs.length === 500 && page && page.after && page.after !== prevAfter && auHasUnhydratedRows()) {
          return walkForward();
        }
      });
    }

    function walkBackward() {
      if (st.end || !st.before || !auHasUnhydratedRows()) return Promise.resolve();
      return auFetchEventsPage(sessionId, "&before=" + encodeURIComponent(st.before)).then(function (page) {
        var evs = page && Array.isArray(page.events) ? page.events : [];
        auMergeHydrationEvents(st, evs);
        st.before = page && page.before ? page.before : "";
        st.end = !!(page && page.end) || !st.before;
        auApplyHydration(st.events, false);
        if (!st.end && st.before && auHasUnhydratedRows()) {
          return walkBackward();
        }
      });
    }

    var startStep;
    if (!st.initialized || !st.after) {
      startStep = auFetchEventsPage(sessionId, "").then(function (page) {
        var evs = page && Array.isArray(page.events) ? page.events : [];
        st.initialized = true;
        st.before = page && page.before ? page.before : "";
        st.after = page && page.after ? page.after : "";
        st.end = !!(page && page.end) || !st.before;
        auMergeHydrationEvents(st, evs);
        auApplyHydration(st.events, false);
      });
    } else {
      startStep = walkForward();
    }

    return startStep.then(walkBackward).then(function () {
      st.pending = false;
      st.staleRetried = false;
      if (st.end) auApplyHydration(st.events, true);
    }).catch(function (err) {
      if (err && err.status === 410 && !st.staleRetried) {
        st.staleRetried = true;
        st.initialized = false;
        st.before = "";
        st.after = "";
        st.end = false;
        st.events = [];
        st.seenIds = Object.create(null);
        st.pending = false;
        return auHydrateCalls();
      }
      st.pending = false;
    });
  }

  function auScheduleHydration() {
    var sessionId = auSessionRid();
    var st = sessionId ? auHydratedSessions[sessionId] : null;
    if (st && st.events.length) {
      auApplyHydration(st.events, false, { unhydratedOnly: true });
    }
    if (!auHasUnhydratedRows() || auHydrateTimer) return;
    auHydrateTimer = setTimeout(auHydrateCalls, 0);
  }

  // --- Event delegation on #convo, keyboard scope & layout sync ---
  doc.addEventListener("click", function (e) {
    var t = e.target;
    var button = t && t.closest ? t.closest(".au-open-call[data-call]") : null;
    var row = button ? button.closest(".au-call-details").querySelector("summary.au-row") :
      (t && t.closest ? t.closest(".au-row[data-call]") : null);
    if (!button && row && row.classList.contains("conv-row") && row.tagName === "SUMMARY") return;
    if (row && $("convo") && $("convo").contains(row)) {
      var sel = window.getSelection && window.getSelection();
      if (sel && !sel.isCollapsed) return;
      auOpenCall(row.getAttribute("data-call"), row);
    }
  });

  doc.addEventListener("keydown", function (e) {
    var t = e.target;
    if ((e.key === "Enter" || e.key === " ") && t && t.matches && t.matches(".au-row[data-call]") && t.tagName !== "SUMMARY") {
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

    if (e.key === "Escape" && inAuditWin) {
      var targetWin = auWindows.find(function (w) { return w.el === inAuditWin; });
      if (targetWin) {
        e.preventDefault();
        if (typeof e.stopImmediatePropagation === "function") e.stopImmediatePropagation();
        else e.stopPropagation();
        auCloseWindow(targetWin);
      }
      return;
    }

    if ((e.key === "ArrowLeft" || e.key === "ArrowRight") && !e.ctrlKey && !e.metaKey && !e.altKey && inAuditWin) {
      var tag = t && t.tagName ? t.tagName : "";
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || (t && t.isContentEditable)) return;
      var navWin = auWindows.find(function (w) { return w.el === inAuditWin; });
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
    var auMutRaf = 0, auMutTimer = 0, auMutDirty = false;
    var auFlushMutations = function () {
      auMutRaf = 0;
      auMutTimer = 0;
      if (!auMutDirty) return;
      auMutDirty = false;
      if (auHasUnhydratedRows()) auScheduleHydration();
      auSyncSelectedMarks();
      if (auFocusedWin) auUpdateNavButtons(auFocusedWin);
    };
    var auQueueMutations = function () {
      if (auMutRaf || auMutTimer) return;
      var hidden = !!(doc && (doc.hidden || (doc.documentElement && doc.documentElement.hasAttribute && doc.documentElement.hasAttribute("data-animation-paused"))));
      if (!hidden && typeof requestAnimationFrame === "function") {
        auMutRaf = requestAnimationFrame(auFlushMutations);
      } else {
        auMutTimer = setTimeout(auFlushMutations, 0);
      }
    };
    new MutationObserver(function (mutations) {
      var hasCallNodes = false;
      for (var i = 0; i < mutations.length; i++) {
        var added = mutations[i].addedNodes;
        for (var k = 0; k < added.length; k++) {
          var n = added[k];
          if (n && n.nodeType === 1 && ((n.matches && (n.matches(".au-tools") || n.matches(".au-row[data-call]:not([data-au-hydrated])"))) || (n.querySelector && n.querySelector(".au-row[data-call]:not([data-au-hydrated])")))) {
            hasCallNodes = true;
            break;
          }
        }
        if (hasCallNodes) break;
      }
      if (!hasCallNodes) return;
      auMutDirty = true;
      auQueueMutations();
    }).observe($("convo"), { childList: true, subtree: true });
  }
  auScheduleHydration();

  window.WsAudit = {
    fmtBytes: auFmtBytes,
    fmtSpan: auFmtSpan,
    tokenGrowth: auTokenGrowthDisplay,
    estContextTokens: auEstContextTokens,
    estContextText: auEstContextText,
    estContextTitle: auEstContextTitle,
    renderBody: auRenderBodyContent,
    applyHydration: auApplyHydration,
    hydrate: auHydrateCalls,
    scheduleHydration: auScheduleHydration,
    openCall: auOpenCall,
    openEvent: auOpenEvent,
    visibleRows: auVisibleAuditRows,
    syncSelection: auSyncSelectedMarks,
    closeUnpinned: auCloseUnpinned,
    renderWindow: auRenderWindowBody,
    fetchEnvelope: auFetchEnvelope,
    displayedEnvelope: auDisplayedEnvelope,
    loadMorePart: auLoadMorePart,
    windows: function () { return auWindows.slice(); }
  };
