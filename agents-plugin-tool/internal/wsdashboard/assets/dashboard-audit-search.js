(function () {
  "use strict";

  var AU_SEARCH_KINDS = [
    ["tool_call", "js.audit.kind.tool_call"],
    ["tool_result", "js.audit.kind.tool_result"],
    ["thinking", "js.audit.kind.thinking"],
    ["hook", "js.audit.kind.hook"],
    ["system_reminder", "js.audit.kind.reminder"],
    ["subagent", "js.audit.kind.subagent"],
    ["compaction", "js.audit.kind.compaction"],
    ["system", "js.audit.kind.system"],
    ["other", "js.audit.kind.unmapped"]
  ];

  var AU_KIND_META = Object.create(null);
  AU_KIND_META.tool_call = { cls: "tool", key: "js.audit.kind.tool_call" };
  AU_KIND_META.tool_result = { cls: "tool", key: "js.audit.kind.tool_result" };
  AU_KIND_META.thinking = { cls: "thinking", key: "js.audit.kind.thinking" };
  AU_KIND_META.hook = { cls: "hook", key: "js.audit.kind.hook" };
  AU_KIND_META.system_reminder = { cls: "reminder", key: "js.audit.kind.reminder" };
  AU_KIND_META.subagent = { cls: "subagent", key: "js.audit.kind.subagent" };
  AU_KIND_META.compaction = { cls: "compaction", key: "js.audit.kind.compaction" };
  AU_KIND_META.system = { cls: "system", key: "js.audit.kind.system" };
  AU_KIND_META.other = { cls: "other", key: "js.audit.kind.unmapped" };

  function t(key, a, b) {
    if (typeof wsT === "function") return wsT(key, a, b);
    return key;
  }

  function esc(s) {
    if (typeof escapeHtml === "function") return escapeHtml(s);
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function buildSearchUrl(rid, queryState, cursor) {
    var st = queryState || {};
    var params = [];
    params.push("q=" + encodeURIComponent(String(st.q || "")));
    params.push("regex=" + (st.regex ? "1" : "0"));
    params.push("case=" + (st.caseSensitive ? "1" : "0"));
    var kinds = Array.isArray(st.kinds)
      ? st.kinds.filter(Boolean).join(",")
      : (typeof st.kinds === "string" ? st.kinds.trim() : "");
    if (kinds) params.push("kinds=" + encodeURIComponent(kinds));
    if (typeof st.limit === "number" && st.limit > 0) {
      params.push("n=" + encodeURIComponent(String(st.limit)));
    }
    if (cursor) params.push("cursor=" + encodeURIComponent(String(cursor)));
    return "/s/" + encodeURIComponent(String(rid || "")) + "/audit/search?" + params.join("&");
  }

  function buildRangeUrl(rid, eventId, part, offset, len) {
    var params = [
      "part=" + encodeURIComponent(String(part || "")),
      "offset=" + encodeURIComponent(String(offset || 0))
    ];
    if (typeof len === "number" && len > 0) params.push("len=" + encodeURIComponent(String(len)));
    return "/s/" + encodeURIComponent(String(rid || "")) + "/audit/event/" +
      encodeURIComponent(String(eventId || "")) + "?" + params.join("&");
  }

  function countTextLines(text) {
    if (!text) return 0;
    var s = String(text);
    var count = 1;
    for (var i = 0; i < s.length - 1; i++) {
      if (s.charCodeAt(i) === 10) count++;
    }
    return count;
  }

  function isLineComplete(text, targetLine) {
    if (!(targetLine > 0) || !text) return false;
    var count = countTextLines(text);
    if (count > targetLine) return true;
    return count === targetLine && String(text).charCodeAt(text.length - 1) === 10;
  }

  function ensureLineLoaded(ev, part, targetLine, fetchRangeFn, opts) {
    opts = opts || {};
    var maxPages = typeof opts.maxPages === "number" && opts.maxPages > 0 ? opts.maxPages : 32;
    var pageLen = typeof opts.len === "number" && opts.len > 0 ? opts.len : 1048576;
    var isCancelled = typeof opts.isCancelled === "function" ? opts.isCancelled : function () { return false; };
    if (!ev || !part || !(targetLine > 0) || !ev.truncated || !ev.range || !ev.range[part]) {
      return Promise.resolve({ loadedPages: 0, reached: isLineComplete(ev && ev[part], targetLine) || !!(ev && !ev.truncated) });
    }
    var pages = 0;
    function step() {
      if (isCancelled()) return Promise.resolve({ loadedPages: pages, cancelled: true, reached: false });
      var curText = ev[part] || "";
      if (isLineComplete(curText, targetLine)) {
        return Promise.resolve({ loadedPages: pages, reached: true });
      }
      var rng = ev.range && ev.range[part];
      if (!rng || (typeof rng.total === "number" && rng.offset >= rng.total) || pages >= maxPages) {
        return Promise.resolve({ loadedPages: pages, reached: countTextLines(curText) >= targetLine });
      }
      var reqOffset = rng.offset || 0;
      if (opts.loadPage) return opts.loadPage(reqOffset, pageLen).then(function (ok) {
        if (!ok) return { loadedPages: pages, reached: false };
        pages++;
        return step();
      });
      return Promise.resolve(fetchRangeFn(reqOffset, pageLen)).then(function (chunk) {
        if (isCancelled()) return { loadedPages: pages, cancelled: true, reached: false };
        if (!chunk || typeof chunk.data !== "string") {
          return { loadedPages: pages, reached: countTextLines(ev[part] || "") >= targetLine };
        }
        if (!ev.range || !ev.range[part] || ev.range[part].offset !== reqOffset) return step();
        ev[part] = (ev[part] || "") + chunk.data;
        pages++;
        if (chunk.end && ev.range) {
          delete ev.range[part];
          if (!Object.keys(ev.range).length) ev.truncated = false;
        } else if (ev.range && ev.range[part]) {
          var nextOff = typeof chunk.next === "number" ? chunk.next : reqOffset + chunk.data.length;
          if (nextOff <= reqOffset && chunk.data.length === 0) {
            return { loadedPages: pages, reached: countTextLines(ev[part] || "") >= targetLine, stalled: true };
          }
          ev.range[part].offset = nextOff;
        }
        if (typeof opts.onChunk === "function") opts.onChunk(ev, chunk);
        if (chunk.end) {
          return { loadedPages: pages, reached: countTextLines(ev[part] || "") >= targetLine };
        }
        return step();
      });
    }
    return step();
  }

  function aggregateHitBadges(hits, lookup) {
    var byKey = Object.create(null);
    var ordered = [];
    if (!Array.isArray(hits) || !lookup) return { byKey: byKey, ordered: ordered };
    var hasCall = typeof lookup.hasCall === "function" ? lookup.hasCall : function () { return false; };
    var hasEvent = typeof lookup.hasEvent === "function" ? lookup.hasEvent : function () { return false; };
    for (var i = 0; i < hits.length; i++) {
      var hit = hits[i];
      if (!hit) continue;
      var rowType = "", rowId = "", viaParent = false;
      if (hit.call_id && hasCall(hit.call_id)) {
        rowType = "call";
        rowId = hit.call_id;
      } else if (hit.parent_id && hasCall(hit.parent_id)) {
        rowType = "call";
        rowId = hit.parent_id;
        viaParent = true;
      } else if (hit.event_id && hasEvent(hit.event_id)) {
        rowType = "event";
        rowId = hit.event_id;
      } else {
        continue;
      }
      var key = rowType + ":" + rowId;
      var entry = byKey[key];
      if (!entry) {
        entry = { key: key, rowType: rowType, rowId: rowId, inCount: 0, outCount: 0, firstHit: hit, viaParent: viaParent };
        byKey[key] = entry;
        ordered.push(entry);
      }
      if (hit.field === "out") entry.outCount++;
      else entry.inCount++;
    }
    return { byKey: byKey, ordered: ordered };
  }

  var state = {
    rid: "",
    q: "",
    regex: false,
    caseSensitive: false,
    kind: "",
    submitted: false,
    submittedQ: "",
    submittedRegex: false,
    submittedCase: false,
    submittedKind: "",
    loading: false,
    loadingMore: false,
    collapsed: false,
    staleNotice: false,
    errorKind: "", // "", "invalid", "unavail"
    errorMsg: "",
    hits: [],
    next: null,
    reqSeq: 0,
    badgeMap: Object.create(null)
  };

  var applyingBadges = false;
  var observerStarted = false;
  var convoObserver = null;
  var activeFetchController = null;

  function activeRid() {
    if (typeof root !== "undefined" && root && typeof root.getAttribute === "function") {
      var dr = root.getAttribute("data-rid");
      if (dr) return dr;
    }
    return (typeof rid !== "undefined" && rid) ? String(rid) : "";
  }

  function convoEl() {
    if (typeof document === "undefined") return null;
    return document.getElementById("convo");
  }

  function ensureSearchUI() {
    var convo = convoEl();
    if (!convo || !convo.isConnected) return null;
    var fchips = convo.querySelector(".au-bar .au-fchips");
    var bar = (fchips && fchips.parentNode) || convo.querySelector(".au-bar");
    if (!bar) {
      bar = document.createElement("div");
      bar.className = "au-bar";
      convo.insertBefore(bar, convo.firstChild);
    }
    var wrap = convo.querySelector(".au-swrap");
    if (wrap && wrap.parentNode !== bar) {
      var oldBar = wrap.parentNode;
      bar.insertBefore(wrap, bar.firstChild);
      if (oldBar && oldBar.classList && oldBar.classList.contains("au-bar") && !oldBar.children.length && oldBar.parentNode) {
        oldBar.parentNode.removeChild(oldBar);
      }
    }
    if (!wrap) {
      wrap = document.createElement("div");
      wrap.className = "au-swrap";
      wrap.innerHTML =
        '<div class="au-sbox" role="search">' +
          '<div class="au-sinp-wrap">' +
            '<input type="search" class="au-sq" autocomplete="off" spellcheck="false">' +
            '<button type="button" class="au-sclear" hidden>&times;</button>' +
          '</div>' +
          '<button type="button" class="au-stog" data-au-sopt="regex" aria-pressed="false">.*</button>' +
          '<button type="button" class="au-stog" data-au-sopt="case" aria-pressed="false">Aa</button>' +
          '<select class="au-skind"></select>' +
          '<button type="button" class="au-sbtn"></button>' +
        '</div>' +
        '<div class="au-slist" hidden>' +
          '<div class="au-slist-hd">' +
            '<span class="au-slist-stat"></span>' +
            '<span class="au-slist-act">' +
              '<button type="button" class="au-sretry" hidden></button>' +
              '<button type="button" class="au-scollapse" hidden></button>' +
            '</span>' +
          '</div>' +
          '<div class="au-shits"></div>' +
          '<div class="au-sfoot" hidden><button type="button" class="au-smore"></button></div>' +
        '</div>';
      bar.insertBefore(wrap, bar.firstChild);
      hydrateStaticLabels(wrap);
      bindSearchUI(wrap);
    }
    return wrap;
  }

  function hydrateStaticLabels(wrap) {
    var inp = wrap.querySelector(".au-sq");
    if (inp) {
      inp.setAttribute("placeholder", t("js.audit.csearch.placeholder"));
      inp.setAttribute("aria-label", t("js.audit.csearch.aria"));
      inp.value = state.q;
    }
    var clr = wrap.querySelector(".au-sclear");
    if (clr) {
      clr.setAttribute("title", t("js.audit.csearch.clear"));
      clr.setAttribute("aria-label", t("js.audit.csearch.clear"));
    }
    var rx = wrap.querySelector('[data-au-sopt="regex"]');
    if (rx) {
      rx.setAttribute("title", t("js.audit.csearch.regex"));
      rx.setAttribute("aria-label", t("js.audit.csearch.regex"));
      rx.setAttribute("aria-pressed", state.regex ? "true" : "false");
    }
    var cs = wrap.querySelector('[data-au-sopt="case"]');
    if (cs) {
      cs.setAttribute("title", t("js.audit.csearch.case"));
      cs.setAttribute("aria-label", t("js.audit.csearch.case"));
      cs.setAttribute("aria-pressed", state.caseSensitive ? "true" : "false");
    }
    var sel = wrap.querySelector(".au-skind");
    if (sel) {
      sel.setAttribute("aria-label", t("js.audit.csearch.kind.aria"));
      var optsHtml = '<option value="">' + esc(t("js.audit.csearch.kind.all")) + "</option>";
      for (var i = 0; i < AU_SEARCH_KINDS.length; i++) {
        optsHtml += '<option value="' + esc(AU_SEARCH_KINDS[i][0]) + '">' + esc(t(AU_SEARCH_KINDS[i][1])) + "</option>";
      }
      sel.innerHTML = optsHtml;
      sel.value = state.kind;
    }
    var sub = wrap.querySelector(".au-sbtn");
    if (sub) sub.textContent = t("js.audit.csearch.submit");
    var retry = wrap.querySelector(".au-sretry");
    if (retry) retry.textContent = t("js.audit.err.retry");
    var more = wrap.querySelector(".au-smore");
    if (more) more.textContent = t("js.audit.csearch.more");
  }

  function renderBadgesInConvo() {
    var convo = convoEl();
    if (!convo) return;
    applyingBadges = true;
    try {
      var prev = convo.querySelectorAll(".au-sbadges");
      for (var i = 0; i < prev.length; i++) {
        if (prev[i].parentNode) prev[i].parentNode.removeChild(prev[i]);
      }
      if (!state.submitted || !state.hits.length) {
        state.badgeMap = Object.create(null);
        return;
      }
      var callRows = Object.create(null);
      var callEls = convo.querySelectorAll(".au-row[data-call]");
      for (var c = 0; c < callEls.length; c++) {
        var cid = callEls[c].getAttribute("data-call");
        if (cid) callRows[cid] = callEls[c];
      }
      var eventRows = Object.create(null);
      var evEls = convo.querySelectorAll("[data-event-id]");
      for (var e = 0; e < evEls.length; e++) {
        var eid = evEls[e].getAttribute("data-event-id");
        if (eid && !eventRows[eid]) eventRows[eid] = evEls[e];
      }
      var agg = aggregateHitBadges(state.hits, {
        hasCall: function (id) { return !!callRows[id]; },
        hasEvent: function (id) { return !!eventRows[id]; }
      });
      state.badgeMap = agg.byKey;
      var inLbl = t("js.audit.csearch.field.in");
      var outLbl = t("js.audit.csearch.field.out");
      for (var k = 0; k < agg.ordered.length; k++) {
        var entry = agg.ordered[k];
        var targetEl = entry.rowType === "call" ? callRows[entry.rowId] : eventRows[entry.rowId];
        if (!targetEl) continue;
        var badges = document.createElement("span");
        badges.className = "au-sbadges";
        var html = "";
        if (entry.inCount > 0) {
          html += '<span class="au-sbadge au-sbadge-in">' + esc(inLbl + " " + entry.inCount) + "</span>";
        }
        if (entry.outCount > 0) {
          html += '<span class="au-sbadge au-sbadge-out">' + esc(outLbl + " " + entry.outCount) + "</span>";
        }
        badges.innerHTML = html;
        var rightEl = targetEl.querySelector(".au-right");
        if (rightEl && rightEl.parentNode === targetEl) targetEl.insertBefore(badges, rightEl);
        else targetEl.appendChild(badges);
      }
    } finally {
      if (convoObserver && typeof convoObserver.takeRecords === "function") {
        convoObserver.takeRecords();
      }
      applyingBadges = false;
    }
  }

  function renderHitList() {
    var wrap = ensureSearchUI();
    if (!wrap) return;
    var clr = wrap.querySelector(".au-sclear");
    if (clr) clr.hidden = !(state.q || state.submitted);
    var subBtn = wrap.querySelector(".au-sbtn");
    if (subBtn) subBtn.disabled = state.loading;

    var listBox = wrap.querySelector(".au-slist");
    var statEl = wrap.querySelector(".au-slist-stat");
    var hdEl = wrap.querySelector(".au-slist-hd");
    var retryBtn = wrap.querySelector(".au-sretry");
    var colBtn = wrap.querySelector(".au-scollapse");
    var hitsEl = wrap.querySelector(".au-shits");
    var footEl = wrap.querySelector(".au-sfoot");
    var moreBtn = wrap.querySelector(".au-smore");

    if (!state.submitted && !state.loading && !state.errorKind) {
      listBox.hidden = true;
      hitsEl.innerHTML = "";
      footEl.hidden = true;
      return;
    }
    listBox.hidden = false;
    statEl.className = "au-slist-stat";
    retryBtn.hidden = true;

    if (state.loading) {
      statEl.textContent = t("js.audit.csearch.searching");
      colBtn.hidden = true;
      hitsEl.hidden = true;
      hitsEl.innerHTML = "";
      footEl.hidden = true;
      hdEl.setAttribute("data-empty", "true");
      return;
    }

    if (state.errorKind === "invalid") {
      statEl.classList.add("au-sstat-err");
      statEl.textContent = t("js.audit.csearch.err.invalid", state.errorMsg || "");
      colBtn.hidden = true;
      hitsEl.hidden = true;
      hitsEl.innerHTML = "";
      footEl.hidden = true;
      hdEl.setAttribute("data-empty", "true");
      return;
    }

    if (state.errorKind === "unavail" && !state.hits.length) {
      statEl.classList.add("au-sstat-err");
      statEl.textContent = t("js.audit.csearch.err.unavail");
      retryBtn.hidden = false;
      colBtn.hidden = true;
      hitsEl.hidden = true;
      hitsEl.innerHTML = "";
      footEl.hidden = true;
      hdEl.setAttribute("data-empty", "true");
      return;
    }

    if (!state.hits.length) {
      statEl.textContent = state.staleNotice ? t("js.audit.csearch.err.stale") + " · " + t("js.audit.csearch.empty") : t("js.audit.csearch.empty");
      if (state.staleNotice) statEl.classList.add("au-sstat-warn");
      colBtn.hidden = true;
      hitsEl.hidden = true;
      hitsEl.innerHTML = "";
      footEl.hidden = true;
      hdEl.setAttribute("data-empty", "true");
      return;
    }

    var countText = state.next
      ? t("js.audit.csearch.hits_more", state.hits.length)
      : (typeof wsTn === "function" ? wsTn("js.audit.csearch.hits", state.hits.length, state.hits.length) : String(state.hits.length));
    if (state.errorKind === "unavail") {
      statEl.classList.add("au-sstat-err");
      statEl.textContent = countText + " · " + t("js.audit.csearch.err.unavail");
      retryBtn.hidden = false;
    } else if (state.staleNotice) {
      statEl.classList.add("au-sstat-warn");
      statEl.textContent = t("js.audit.csearch.err.stale") + " · " + countText;
    } else {
      statEl.textContent = countText;
    }

    colBtn.hidden = false;
    colBtn.textContent = t(state.collapsed ? "js.audit.csearch.toggle.show" : "js.audit.csearch.toggle.hide");
    colBtn.setAttribute("aria-expanded", state.collapsed ? "false" : "true");
    hitsEl.hidden = state.collapsed;
    hdEl.setAttribute("data-empty", state.collapsed ? "true" : "false");

    var html = "";
    var inLbl = t("js.audit.csearch.field.in");
    var outLbl = t("js.audit.csearch.field.out");
    for (var i = 0; i < state.hits.length; i++) {
      var hit = state.hits[i];
      var km = AU_KIND_META[hit.kind] || AU_KIND_META.other;
      var fldLbl = hit.field === "out" ? outLbl : inLbl;
      var parentBadge = hit.parent_id
        ? '<span class="au-sparent" title="' + esc(t("js.audit.csearch.subagent_of", hit.parent_id)) + '">&#x21b3; ' + esc(t("js.audit.kind.subagent")) + "</span>"
        : "";
      var lineText = esc(hit.line || "") + (hit.clipped ? "&hellip;" : "");
      html += '<button type="button" class="au-shit au-kind-' + esc(km.cls) + '" data-au-hit-idx="' + i + '">' +
        '<span class="au-skdot" aria-hidden="true"></span>' +
        '<span class="au-sklbl">' + esc(t(km.key)) + "</span>" +
        '<span class="au-sfld">' + esc(fldLbl) + "</span>" +
        '<span class="au-slnum">L' + esc(String(hit.line_no || 1)) + "</span>" +
        parentBadge +
        '<span class="au-sltxt">' + lineText + "</span>" +
        "</button>";
    }
    hitsEl.innerHTML = html;

    var hasMore = !!(state.next && !state.collapsed);
    footEl.hidden = !hasMore;
    if (moreBtn) moreBtn.disabled = state.loadingMore;
  }

  function executeSearch(opts) {
    opts = opts || {};
    var curRid = activeRid();
    if (!curRid) return Promise.resolve();
    var query = opts.query || (opts.append ? state.committed : {q: String(state.q || "").trim(), regex: !!state.regex, caseSensitive: !!state.caseSensitive, kinds: state.kind ? [state.kind] : []});
    if (!query) return Promise.resolve();
    var q = query.q;
    if (!q) {
      clearSearch();
      return Promise.resolve();
    }
    if (!opts.append && activeFetchController && typeof activeFetchController.abort === "function") {
      try { activeFetchController.abort(); } catch (e) {}
    }
    var seq = ++state.reqSeq;
    state.rid = curRid;
    state.submitted = true;
    state.submittedQ = q;
    state.submittedRegex = query.regex;
    state.submittedCase = query.caseSensitive;
    state.submittedKind = query.kinds[0] || "";
    state.errorKind = "";
    state.errorMsg = "";
    if (!opts.append) {
      state.loading = true;
      state.loadingMore = false;
      state.collapsed = false;
      state.staleNotice = !!opts.fromStale;
      if (!opts.preserveHits) {
        state.hits = [];
        state.next = null;
        renderBadgesInConvo();
      }
    } else {
      state.loadingMore = true;
    }
    renderHitList();

    var url = buildSearchUrl(curRid, {
      q: q,
      regex: state.submittedRegex,
      caseSensitive: state.submittedCase,
      kinds: state.submittedKind ? [state.submittedKind] : []
    }, opts.cursor || "");

    var fetchOpts = {};
    if (typeof AbortController === "function") {
      activeFetchController = new AbortController();
      fetchOpts.signal = activeFetchController.signal;
    } else {
      activeFetchController = null;
    }

    return fetch(url, fetchOpts).then(function (resp) {
      if (seq !== state.reqSeq) return null;
      return resp.text().then(function (raw) {
        if (seq !== state.reqSeq) return null;
        var data = null;
        try { data = raw ? JSON.parse(raw) : null; } catch (e) {}
        if (resp.status === 410 && opts.append && !opts.retriedStale) {
          state.loadingMore = false;
          return executeSearch({ append: false, fromStale: true, retriedStale: true, query: query });
        }
        if (resp.status === 400 && !opts.append) {
          state.loading = false;
          state.loadingMore = false;
          state.errorKind = "invalid";
          state.errorMsg = (data && data.error) ? String(data.error) : "HTTP 400";
          state.hits = [];
          state.next = null;
          renderBadgesInConvo();
          renderHitList();
          return null;
        }
        if (!resp.ok || !data || !Array.isArray(data.hits)) {
          state.loading = false;
          state.loadingMore = false;
          state.errorKind = "unavail";
          renderHitList();
          return null;
        }
        state.loading = false;
        state.loadingMore = false;
        state.committed = query;
        state.hits = opts.append ? state.hits.concat(data.hits) : data.hits.slice();
        state.next = data.next || null;
        renderBadgesInConvo();
        renderHitList();
        return data;
      });
    }).catch(function (err) {
      if (err && err.name === "AbortError") return null;
      if (seq !== state.reqSeq) return null;
      state.loading = false;
      state.loadingMore = false;
      state.errorKind = "unavail";
      renderHitList();
      return null;
    });
  }

  function invalidateDraft() {
    ++state.reqSeq;
    if (activeFetchController) activeFetchController.abort();
    state.loading = state.loadingMore = false;
    renderHitList();
  }

  function clearSearch() {
    if (typeof window !== "undefined" && window.WsAudit) window.WsAudit.windows().forEach(function (win) {
      if (win._auPagingOwner) {
        win._auPagingOwner.cancelled = true;
        if (win._auPagingOwner.cancel) win._auPagingOwner.cancel();
      }
      win._auPagingOwner = null;
      win.search = ""; win.regex = false; win.caseSensitive = false; win.targetLine = 0;
      window.WsAudit.renderWindow(win);
    });
    if (activeFetchController && typeof activeFetchController.abort === "function") {
      try { activeFetchController.abort(); } catch (e) {}
      activeFetchController = null;
    }
    ++state.reqSeq;
    state.q = "";
    state.committed = null;
    state.submitted = false;
    state.submittedQ = "";
    state.loading = false;
    state.loadingMore = false;
    state.collapsed = false;
    state.staleNotice = false;
    state.errorKind = "";
    state.errorMsg = "";
    state.hits = [];
    state.next = null;
    state.badgeMap = Object.create(null);
    var wrap = ensureSearchUI();
    if (wrap) {
      var inp = wrap.querySelector(".au-sq");
      if (inp) inp.value = "";
    }
    renderBadgesInConvo();
    renderHitList();
  }

  function hydrateParentContext(win, parentCallId, expectedInspectId) {
    var curRid = activeRid();
    if (!curRid || !parentCallId || typeof fetch !== "function") return;
    var reqToken = win.reqToken;
    fetch("/s/" + encodeURIComponent(curRid) + "/audit/events?call=" + encodeURIComponent(parentCallId))
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (data) {
        if (!data || win.reqToken !== reqToken || !win.el.isConnected || win.inspectEventId !== expectedInspectId) return;
        var evs = Array.isArray(data.events) ? data.events : [];
        win.callSummary = data.call || win.callSummary;
        win.linkedEvents = evs;
        for (var i = 0; i < evs.length; i++) {
          if (!evs[i] || !evs[i].id) continue;
          if (evs[i].kind === "tool_result") { if (!win.resultEventId) win.resultEventId = evs[i].id; }
          else if (!win.primaryEventId) { win.primaryEventId = evs[i].id; }
        }
        if (window.WsAudit && typeof window.WsAudit.renderWindow === "function") {
          window.WsAudit.renderWindow(win);
        }
      }).catch(function () {});
  }

  function openSearchHit(hit, viaParentRowId) {
    if (!hit || !hit.event_id || !window.WsAudit || typeof window.WsAudit.openEvent !== "function") return false;
    var parentCallId = viaParentRowId || hit.parent_id || "";
    var isSubagentRoute = !!(parentCallId && hit.call_id !== parentCallId);
    var openOpts = {
      callId: isSubagentRoute ? parentCallId : (hit.call_id || ""),
      inspectEventId: isSubagentRoute ? hit.event_id : null,
      kind: hit.kind || "",
      tab: hit.field === "out" ? "out" : "in",
      search: state.submittedQ || state.q || "",
      regex: !!state.submittedRegex,
      caseSensitive: !!state.submittedCase,
      targetLine: hit.line_no || 0
    };
    window.WsAudit.openEvent(hit.event_id, openOpts);
    var wins = typeof window.WsAudit.windows === "function" ? window.WsAudit.windows() : [];
    var win = null;
    for (var i = 0; i < wins.length; i++) {
      var w = wins[i];
      if (w && (w.eventId === hit.event_id || w.inspectEventId === hit.event_id)) {
        win = w;
        break;
      }
    }
    if (win && isSubagentRoute) hydrateParentContext(win, parentCallId, hit.event_id);
    return true;
  }

  function handleWindowRender(win) {
    if (!win || win.loading || !(win.targetLine > 0)) return;
    var part = win.tab === "out" ? "out" : (win.tab === "in" ? "in" : "");
    var ev = window.WsAudit.displayedEnvelope(win);
    if (!part || !ev || !ev.truncated || !ev.range || !ev.range[part]) return;
    if (isLineComplete(ev[part] || "", win.targetLine)) return;
    var previous = win._auPagingOwner;
    if (previous && previous.reqToken === win.reqToken && previous.envelope === ev && previous.field === part) return;
    if (previous && previous.cancel) previous.cancel();
    var curRid = activeRid();
    if (!curRid) return;
    var run = { reqToken: win.reqToken, envelope: ev, field: part };
    win._auPagingOwner = run;
    var eventId = ev.id || win.inspectEventId || win.eventId;
    ensureLineLoaded(ev, part, win.targetLine, function (offset, len) {
      return fetch(buildRangeUrl(curRid, eventId, part, offset, len)).then(function (r) {
        if (!r.ok) return null;
        return r.json();
      });
    }, {
      loadPage: function (offset, len) { return window.WsAudit.loadMorePart(win, part, offset, run); },
      isCancelled: function () { return win.reqToken !== run.reqToken || !win.el || !win.el.isConnected || window.WsAudit.displayedEnvelope(win) !== ev || win._auPagingOwner !== run; },
      onChunk: function () {
        if (window.WsAudit && typeof window.WsAudit.renderWindow === "function") {
          window.WsAudit.renderWindow(win);
        }
      }
    }).then(function (res) {
      if (win._auPagingOwner === run) win._auPagingOwner = null;
      if (res && !res.cancelled && res.loadedPages > 0 && window.WsAudit && typeof window.WsAudit.renderWindow === "function") {
        window.WsAudit.renderWindow(win);
      }
    }).catch(function () {
      if (win._auPagingOwner === run) win._auPagingOwner = null;
    });
  }

  function activateBadgedRow(rowEl) {
    if (!rowEl || !state.submitted || !state.hits.length) return false;
    var callId = rowEl.getAttribute("data-call");
    var eventId = rowEl.getAttribute("data-event-id");
    var entry = callId ? state.badgeMap["call:" + callId] : (eventId ? state.badgeMap["event:" + eventId] : null);
    if (!entry || !entry.firstHit) return false;
    return openSearchHit(entry.firstHit, entry.viaParent ? entry.rowId : "");
  }

  function bindSearchUI(wrap) {
    var subBtn = wrap.querySelector(".au-sbtn");
    var inp = wrap.querySelector(".au-sq");
    var clr = wrap.querySelector(".au-sclear");
    var rxBtn = wrap.querySelector('[data-au-sopt="regex"]');
    var csBtn = wrap.querySelector('[data-au-sopt="case"]');
    var kindSel = wrap.querySelector(".au-skind");
    var colBtn = wrap.querySelector(".au-scollapse");
    var retryBtn = wrap.querySelector(".au-sretry");
    var moreBtn = wrap.querySelector(".au-smore");
    var hitsEl = wrap.querySelector(".au-shits");

    subBtn.addEventListener("click", function () {
      state.q = inp.value || "";
      executeSearch({ append: false });
    });
    inp.addEventListener("keydown", function (e) {
      if (e.key !== "Enter") return;
      e.preventDefault();
      state.q = inp.value || "";
      executeSearch({ append: false });
    });
    inp.addEventListener("input", function () {
      invalidateDraft();
      state.q = inp.value || "";
      clr.hidden = !(state.q || state.submitted);
      if (!state.q && state.submitted) clearSearch();
    });
    clr.addEventListener("click", function () {
      clearSearch();
      inp.focus();
    });
    rxBtn.addEventListener("click", function () {
      invalidateDraft();
      state.regex = !state.regex;
      rxBtn.setAttribute("aria-pressed", state.regex ? "true" : "false");
    });
    csBtn.addEventListener("click", function () {
      invalidateDraft();
      state.caseSensitive = !state.caseSensitive;
      csBtn.setAttribute("aria-pressed", state.caseSensitive ? "true" : "false");
    });
    kindSel.addEventListener("change", function () {
      invalidateDraft();
      state.kind = kindSel.value || "";
      if (state.submitted && state.q.trim()) executeSearch({ append: false });
    });
    colBtn.addEventListener("click", function () {
      state.collapsed = !state.collapsed;
      renderHitList();
    });
    retryBtn.addEventListener("click", function () {
      if (state.hits.length && state.next) {
        executeSearch({ append: true, cursor: state.next });
      } else {
        executeSearch({ append: false });
      }
    });
    moreBtn.addEventListener("click", function () {
      if (!state.next || state.loadingMore) return;
      executeSearch({ append: true, cursor: state.next });
    });
    hitsEl.addEventListener("click", function (e) {
      var btn = e.target && e.target.closest ? e.target.closest(".au-shit") : null;
      if (!btn) return;
      var idx = Number(btn.getAttribute("data-au-hit-idx"));
      if (!(idx >= 0) || !state.hits[idx]) return;
      openSearchHit(state.hits[idx]);
    });
  }

  function boot() {
    if (typeof document === "undefined" || typeof window === "undefined") return;
    if (window.WsAudit && window.WsAudit.hooks) {
      window.WsAudit.hooks.onCallActivate = function (callId, rowEl) {
        return activateBadgedRow(rowEl);
      };
      window.WsAudit.hooks.onRender = handleWindowRender;
    }

    ensureSearchUI();

    document.addEventListener("ws-history-page", function () {
      ensureSearchUI();
      if (state.submitted) renderBadgesInConvo();
    });
    document.addEventListener("ws-entries-merged", function () {
      ensureSearchUI();
      if (state.submitted) renderBadgesInConvo();
    });
    document.addEventListener("ws-history-reset", function () {
      var cur = activeRid();
      if (state.rid && cur !== state.rid) {
        clearSearch();
      } else {
        ensureSearchUI();
        if (state.submitted) renderBadgesInConvo();
      }
    });

    window.addEventListener("keydown", function (e) {
      if (e.key !== "Enter" || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
      var convo = convoEl();
      var target = e.target;
      if (!convo || !target || !target.closest || !convo.contains(target)) return;
      if (target.closest(".au-swrap, button, a, input, select, textarea")) return;
      var row = target.closest(".au-row[data-call], .au-hid[data-event-id], .au-sysrow[data-event-id]");
      if (row && activateBadgedRow(row)) {
        e.preventDefault();
        e.stopImmediatePropagation();
      }
    }, true);

    window.addEventListener("click", function (e) {
      var target = e.target;
      if (target && target.closest && target.closest(".au-snext, .au-sprev")) {
        var winEl = target.closest(".au-win[data-au-win]");
        if (winEl && window.WsAudit && typeof window.WsAudit.windows === "function") {
          var wid = Number(winEl.getAttribute("data-au-win"));
          var wins = window.WsAudit.windows();
          for (var i = 0; i < wins.length; i++) {
            if (wins[i] && wins[i].id === wid) { wins[i].targetLine = 0; break; }
          }
        }
      }
      var convo = convoEl();
      if (!convo || !target || !target.closest || !convo.contains(target)) return;
      if (target.closest(".au-swrap, .au-more, a, input, select, textarea")) return;
      if (target.closest("button") && !target.closest(".au-hid, .au-sysrow")) return;
      var evRow = target.closest(".au-hid[data-event-id], .au-sysrow[data-event-id]");
      if (evRow && activateBadgedRow(evRow)) {
        e.preventDefault();
        e.stopImmediatePropagation();
      }
    }, true);

    if (!observerStarted && typeof MutationObserver === "function") {
      var convo = convoEl();
      if (convo) {
        observerStarted = true;
        convoObserver = new MutationObserver(function (mutations) {
          if (applyingBadges) return;
          var relevant = false;
          for (var i = 0; i < mutations.length; i++) {
            var t = mutations[i] && mutations[i].target;
            if (!t || !t.closest || (!t.closest(".au-swrap") && !t.closest(".au-sbadges"))) {
              relevant = true;
              break;
            }
          }
          if (!relevant) return;
          ensureSearchUI();
          if (state.submitted) renderBadgesInConvo();
        });
        convoObserver.observe(convo, { childList: true, subtree: true });
      }
    }
  }

  var api = {
    buildSearchUrl: buildSearchUrl,
    buildRangeUrl: buildRangeUrl,
    countTextLines: countTextLines,
    isLineComplete: isLineComplete,
    ensureLineLoaded: ensureLineLoaded,
    aggregateHitBadges: aggregateHitBadges,
    ensureUI: ensureSearchUI,
    executeSearch: executeSearch,
    clearSearch: clearSearch,
    invalidateDraft: invalidateDraft,
    handleWindowRender: handleWindowRender,
    openHit: openSearchHit,
    renderBadges: renderBadgesInConvo,
    state: function () { return state; }
  };

  if (typeof window !== "undefined") {
    window.WsAuditSearch = api;
    if (typeof document !== "undefined") {
      if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", boot);
      } else {
        boot();
      }
    }
  }
  if (typeof module !== "undefined" && module.exports) {
    module.exports = api;
  }
})();
