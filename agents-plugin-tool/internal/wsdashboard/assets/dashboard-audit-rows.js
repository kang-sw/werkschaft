  // --- audit view phase 2: hidden-event rows, (607)/(681) UTC paging, keys ---
  var AU_EVENT_PAGE_LIMIT = 200;
  // One interval fetch reads at most this many pages, newest first; a fetch
  // with no lower bound reads one. The rows follow the loaded history, not the
  // whole audit store, so a capped fetch reports the floor it reached.
  var AU_MAX_INTERVAL_PAGES = 10;
  var AU_HIDDEN_KINDS = [
    { kind: "thinking", i18n: "js.audit.kind.thinking" },
    { kind: "hook", i18n: "js.audit.kind.hook" },
    { kind: "system_reminder", i18n: "js.audit.kind.reminder" },
    { kind: "subagent", i18n: "js.audit.kind.subagent" },
    { kind: "compaction", i18n: "js.audit.kind.compaction" },
    { kind: "system", i18n: "js.audit.kind.system" },
    { kind: "other", i18n: "js.audit.kind.unmapped" }
  ];
  var AU_HIDDEN_SET = Object.create(null);
  for (var ahi = 0; ahi < AU_HIDDEN_KINDS.length; ahi++) {
    AU_HIDDEN_SET[AU_HIDDEN_KINDS[ahi].kind] = AU_HIDDEN_KINDS[ahi];
  }

  function auIsHiddenAuditKind(kind) {
    return !!AU_HIDDEN_SET[kind];
  }

  function auIsSessionStartEvent(ev) {
    if (!ev || ev.kind !== "system") return false;
    var sub = String(ev.subkind || "").toLowerCase();
    return sub === "system_prompt" || sub === "session_start" || sub === "init" || sub === "system:init" || sub.indexOf("system_prompt") >= 0;
  }

  function auParseRFC3339Ns(raw) {
    if (typeof raw !== "string" || !raw) return null;
    var m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?(Z|([+-])(\d{2}):(\d{2}))$/.exec(raw.trim());
    if (!m) return null;
    var frac = (m[7] || "") + "000000000";
    var msPart = Number(frac.slice(0, 3));
    var subNs = Number(frac.slice(3, 9));
    var ms = Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]), Number(m[6])) + msPart;
    if (m[8] !== "Z") {
      var off = (Number(m[10]) * 60 + Number(m[11])) * 60000;
      ms += m[9] === "+" ? -off : off;
    }
    if (!isFinite(ms) || !isFinite(subNs)) return null;
    return { ms: ms, ns: subNs };
  }

  function auCmpNs(a, b) {
    if (!a && !b) return 0;
    if (!a) return -1;
    if (!b) return 1;
    if (a.ms !== b.ms) return a.ms < b.ms ? -1 : 1;
    if (a.ns !== b.ns) return a.ns < b.ns ? -1 : 1;
    return 0;
  }

  function auCmpStrNs(aRaw, bRaw) {
    return auCmpNs(auParseRFC3339Ns(aRaw), auParseRFC3339Ns(bRaw));
  }

  function auCmpAuditEvent(a, b) {
    var ct = auCmpStrNs(a && a.at, b && b.at);
    if (ct !== 0) return ct;
    var sa = Number(a && a.seq || 0), sb = Number(b && b.seq || 0);
    if (sa !== sb) return sa < sb ? -1 : 1;
    var ia = String(a && a.id || ""), ib = String(b && b.id || "");
    if (ia === ib) return 0;
    return ia < ib ? -1 : 1;
  }

  function auHiddenKindLabel(kind) {
    var meta = AU_HIDDEN_SET[kind];
    if (meta && typeof wsT === "function") return wsT(meta.i18n);
    return kind || "event";
  }

  function auFormatHiddenPreview(ev, fmtBytesFn) {
    if (!ev) return "";
    var parts = [];
    if (ev.subkind) parts.push(String(ev.subkind));
    if (ev.title) parts.push(String(ev.title));
    if (!parts.length && fmtBytesFn) {
      var bytes = ev.out_bytes || ev.in_bytes || 0;
      if (bytes > 0) parts.push(fmtBytesFn(bytes));
    }
    if (!parts.length && ev.id) parts.push(String(ev.id));
    return parts.join(" · ");
  }

  function auInterleavePlan(entries, events, renderedCallIds) {
    var sorted = (events || []).filter(function (ev) {
      return ev && ev.id && auIsHiddenAuditKind(ev.kind);
    }).slice().sort(auCmpAuditEvent);
    var parsedEntries = (entries || []).map(function (ent) {
      return { el: ent.el, key: ent.key, at: auParseRFC3339Ns(ent.at) };
    });
    var attached = Object.create(null);
    var standalone = [];
    // The harness repeats its init once per turn: only the oldest loaded
    // session-start event is the banner, later ones are ordinary system rows.
    var bannerSeen = false;
    for (var i = 0; i < sorted.length; i++) {
      var ev = sorted[i];
      if (ev.call_id && renderedCallIds && renderedCallIds[ev.call_id]) {
        if (!attached[ev.call_id]) attached[ev.call_id] = [];
        attached[ev.call_id].push(ev);
        continue;
      }
      var evAt = auParseRFC3339Ns(ev.at);
      var sysStart = !bannerSeen && auIsSessionStartEvent(ev);
      if (sysStart) bannerSeen = true;
      var idx = parsedEntries.length;
      if (evAt) {
        for (var j = 0; j < parsedEntries.length; j++) {
          if (!parsedEntries[j].at) continue;
          var cmp = auCmpNs(parsedEntries[j].at, evAt);
          if (sysStart ? cmp >= 0 : cmp > 0) {
            idx = j;
            break;
          }
        }
      } else if (sysStart) {
        idx = 0;
      }
      standalone.push({ event: ev, sysStart: sysStart, beforeIndex: idx });
    }
    return { attached: attached, standalone: standalone };
  }

  // Low-signal rows collapse by default: consecutive runs of these kinds fold
  // into one muted summary line. Order here is the order of the summary labels.
  var AU_RUN_KINDS = [
    { kind: "system", i18n: "js.audit.kind.system" },
    { kind: "thinking", i18n: "js.audit.kind.thinking" },
    { kind: "hook", i18n: "js.audit.kind.hook" },
    { kind: "system_reminder", i18n: "js.audit.kind.reminder" },
    { kind: "subagent", i18n: "js.audit.kind.subagent" }
  ];

  function auIsRunKind(kind) {
    for (var i = 0; i < AU_RUN_KINDS.length; i++) if (AU_RUN_KINDS[i].kind === kind) return true;
    return false;
  }

  // The fold kind of an event, or "": the run kinds, plus a subagent's task
  // lifecycle rows (its own text and tool rows are content and stay visible).
  function auRunKindOf(ev) {
    if (!ev) return "";
    if (ev.kind === "subagent") return String(ev.subkind || "").indexOf("task_") === 0 ? "subagent" : "";
    return auIsRunKind(ev.kind) ? ev.kind : "";
  }

  // "System 4 · Thinking 1" for rows given as kind strings, in fixed kind order
  // so the label does not reshuffle while counts change.
  function auRunLabel(kinds) {
    var counts = Object.create(null);
    for (var i = 0; i < kinds.length; i++) counts[kinds[i]] = (counts[kinds[i]] || 0) + 1;
    var parts = [];
    for (var k = 0; k < AU_RUN_KINDS.length; k++) {
      var n = counts[AU_RUN_KINDS[k].kind];
      if (n) parts.push((typeof wsT === "function" ? wsT(AU_RUN_KINDS[k].i18n) : AU_RUN_KINDS[k].kind) + " " + n);
    }
    return parts.join(" · ");
  }

  // Controller active only in a session dashboard with #convo and WsAudit.
  if (typeof doc !== "undefined" && typeof window !== "undefined" && $("convo") && window.WsAudit) {
    var auRowsRid = root.getAttribute("data-rid") || (typeof rid !== "undefined" ? rid : "") || "";
    var auEventsById = Object.create(null);
    var auCoveredSince = "";
    var auOpenBelowDone = false;
    var auUnboundedDone = false;
    var auLatestAfter = "";
    var auReset410Done = false;
    var auFetchQueue = Promise.resolve();
    var auEpoch = 0;
    var auFailedJobs = [];
    var auFocusedAuditEl = null;
    var auExpandedRows = Object.create(null);

    function auConvo() { return $("convo"); }
    function auEntriesContainer() {
      var r = typeof region === "function" ? region("entries") : doc.getElementById("r-entries");
      return r ? r.querySelector(".centries") : null;
    }

    function auFirstDataEntryAt(box) {
      if (!box) return "";
      for (var c = box.firstElementChild; c; c = c.nextElementSibling) {
        if (c.hasAttribute("data-entry")) {
          var at = c.getAttribute("data-at");
          if (at) return at;
        }
      }
      return "";
    }

    function auRenderedEntries(box) {
      var list = [];
      if (!box) return list;
      for (var c = box.firstElementChild; c; c = c.nextElementSibling) {
        if (c.hasAttribute("data-entry")) {
          list.push({ el: c, key: c.getAttribute("data-entry") || "", at: c.getAttribute("data-at") || "" });
        }
      }
      return list;
    }

    function auRenderedCallMap(box) {
      var map = Object.create(null);
      if (!box) return map;
      var rows = box.querySelectorAll(".au-row[data-call]");
      for (var i = 0; i < rows.length; i++) {
        var cid = rows[i].getAttribute("data-call");
        if (cid) map[cid] = rows[i];
      }
      return map;
    }

    function auBuildRowNode(ev, inner, sysStart) {
      var tag = inner ? "li" : "div";
      var el = doc.createElement(tag);
      var kindClass = "au-kind-" + String(ev.kind || "other");
      el.className = (sysStart ? "au-sysrow " : "au-hid ") + (inner ? "au-inner " : "") + kindClass;
      el.setAttribute("data-event-id", ev.id);
      el.setAttribute("data-kind", ev.kind);
      var runKind = sysStart ? "" : auRunKindOf(ev);
      if (runKind) el.setAttribute("data-run", runKind);
      if (ev.at) el.setAttribute("data-at", ev.at);
      if (ev.call_id) el.setAttribute("data-call-ref", ev.call_id);
      el.tabIndex = 0;
      el.setAttribute("role", "button");

      if (sysStart) {
        var lbl = doc.createElement("span");
        lbl.className = "au-sysrow-lbl";
        var sysText = wsT("js.audit.sysrow.start");
        var pvSys = auFormatHiddenPreview(ev, window.WsAudit.fmtBytes);
        lbl.textContent = pvSys ? sysText + " · " + pvSys : sysText;
        el.appendChild(lbl);
        return el;
      }

      var dot = doc.createElement("span");
      dot.className = "au-dot-ev";
      dot.setAttribute("aria-hidden", "true");
      var badge = doc.createElement("span");
      badge.className = "au-hbadge";
      badge.textContent = auHiddenKindLabel(ev.kind);
      var pv = doc.createElement("span");
      pv.className = "au-pv";
      var preview = auFormatHiddenPreview(ev, window.WsAudit.fmtBytes);
      pv.textContent = preview;
      if (preview) pv.title = preview;
      el.appendChild(dot);
      el.appendChild(badge);
      el.appendChild(pv);
      return el;
    }

    // Run summaries are transparent to row adjacency: a summary node never
    // counts as the sibling a row must sit next to.
    function auPrevRowEl(el) {
      var p = el.previousElementSibling;
      while (p && p.classList.contains("au-run")) p = p.previousElementSibling;
      return p;
    }
    function auNextRowEl(el) {
      var n = el.nextElementSibling;
      while (n && n.classList.contains("au-run")) n = n.nextElementSibling;
      return n;
    }

    function auIsRunRow(el) {
      return el.classList.contains("au-hid") && !!el.getAttribute("data-run");
    }

    // Patches by key: an existing summary is reused and only its label text,
    // aria-expanded and the rows' hidden flag are written when they differ.
    // Nothing is rebuilt when a row joins or leaves a run. A run is expanded
    // when any of its rows was expanded by the user; rows joining it follow.
    function auApplyRunsIn(container, tag) {
      var runs = [], orphans = [], cur = null, pending = null;
      for (var c = container.firstElementChild; c; c = c.nextElementSibling) {
        if (c.classList.contains("au-run")) {
          if (cur) orphans.push(c);
          else {
            if (pending) orphans.push(pending);
            pending = c;
          }
        } else if (auIsRunRow(c)) {
          if (!cur) {
            cur = { rows: [], sum: pending };
            runs.push(cur);
            pending = null;
          }
          cur.rows.push(c);
        } else {
          cur = null;
          if (pending) { orphans.push(pending); pending = null; }
        }
      }
      if (pending) orphans.push(pending);
      for (var o = 0; o < orphans.length; o++) {
        if (orphans[o].parentNode) { auWrites++; orphans[o].parentNode.removeChild(orphans[o]); }
      }
      for (var r = 0; r < runs.length; r++) {
        var rows = runs[r].rows, sum = runs[r].sum, expanded = false, kinds = [];
        for (var i = 0; i < rows.length; i++) {
          var eid = rows[i].getAttribute("data-event-id");
          if (eid && auExpandedRows[eid]) expanded = true;
          kinds.push(rows[i].getAttribute("data-run"));
        }
        if (!sum) {
          sum = doc.createElement(tag);
          sum.className = "au-run" + (tag === "li" ? " au-run-in" : "");
          sum.tabIndex = 0;
          sum.setAttribute("role", "button");
          var lbl = doc.createElement("span");
          lbl.className = "au-run-lbl";
          sum.appendChild(lbl);
        }
        if (sum.nextElementSibling !== rows[0] || sum.parentNode !== container) { auWrites++; container.insertBefore(sum, rows[0]); }
        var text = auRunLabel(kinds);
        var lblEl = sum.firstElementChild;
        if (lblEl && lblEl.textContent !== text) lblEl.textContent = text;
        var want = expanded ? "true" : "false";
        if (sum.getAttribute("aria-expanded") !== want) sum.setAttribute("aria-expanded", want);
        for (var j = 0; j < rows.length; j++) {
          if (expanded) {
            var rowId = rows[j].getAttribute("data-event-id");
            if (rowId) auExpandedRows[rowId] = true;
          }
          if (rows[j].hasAttribute("hidden") === expanded) { auWrites++; rows[j].toggleAttribute("hidden", !expanded); }
        }
      }
    }

    function auApplyRuns(box) {
      auApplyRunsIn(box, "div");
      var lists = box.querySelectorAll(".au-calls");
      for (var i = 0; i < lists.length; i++) auApplyRunsIn(lists[i], "li");
    }

    function auToggleRun(sum) {
      var rows = [];
      for (var n = sum.nextElementSibling; n && auIsRunRow(n); n = n.nextElementSibling) rows.push(n);
      if (!rows.length) return;
      var expand = sum.getAttribute("aria-expanded") !== "true";
      for (var i = 0; i < rows.length; i++) {
        var id = rows[i].getAttribute("data-event-id");
        if (id) { if (expand) auExpandedRows[id] = true; else delete auExpandedRows[id]; }
        rows[i].toggleAttribute("hidden", !expand);
      }
      sum.setAttribute("aria-expanded", expand ? "true" : "false");
      if (!expand && auFocusedAuditEl && auFocusedAuditEl.hasAttribute("hidden")) auSetCurrentRow(null);
    }

    // Counts the reconcile's DOM writes that can change the conversation's height.
    var auWrites = 0;

    function auReconcileRows() {
      var box = auEntriesContainer();
      if (!box) return;
      // Rows land after the view was pinned (a session shown again, a page that
      // came back late): a bottom-pinned conversation follows what they add.
      // The pin flag is read before any write; nothing is measured here.
      var pin = typeof stick !== "undefined" && stick, writes0 = auWrites;
      var entries = auRenderedEntries(box);
      var callMap = auRenderedCallMap(box);
      var allEvents = Object.keys(auEventsById).map(function (id) { return auEventsById[id]; });
      var plan = auInterleavePlan(entries, allEvents, callMap);

      // Index existing rendered hidden/sysrow elements to move/reuse cleanly without scroll jitter.
      var existing = Object.create(null);
      var oldNodes = box.querySelectorAll(".au-hid[data-event-id], .au-sysrow[data-event-id]");
      for (var i = 0; i < oldNodes.length; i++) {
        var eid = oldNodes[i].getAttribute("data-event-id");
        if (eid && !existing[eid]) existing[eid] = oldNodes[i];
        else if (oldNodes[i].parentNode) { auWrites++; oldNodes[i].parentNode.removeChild(oldNodes[i]); }
      }

      var kept = Object.create(null);
      // 1. Place attached rows directly beneath each matching .au-row[data-call].
      var attachedCalls = Object.keys(plan.attached);
      for (var ac = 0; ac < attachedCalls.length; ac++) {
        var callId = attachedCalls[ac];
        var callEl = callMap[callId];
        if (!callEl || !callEl.parentNode) continue;
        var list = plan.attached[callId];
        var anchor = callEl;
        for (var k = 0; k < list.length; k++) {
          var aev = list[k];
          kept[aev.id] = true;
          var anode = existing[aev.id];
          if (!anode || anode.tagName !== "LI" || !anode.classList.contains("au-inner")) {
            if (anode && anode.parentNode) { auWrites++; anode.parentNode.removeChild(anode); }
            anode = auBuildRowNode(aev, true, false);
          }
          if (auPrevRowEl(anode) !== anchor || anode.parentNode !== callEl.parentNode) {
            auWrites++;
            callEl.parentNode.insertBefore(anode, anchor.nextSibling);
          }
          anchor = anode;
        }
      }

      // 2. Place standalone & session-start rows chronologically inside .centries.
      // A row is left alone when it already sits right before the next row of
      // its slot (or the slot's reference entry), so a settled stream moves nothing.
      var snodes = [];
      for (var s = 0; s < plan.standalone.length; s++) {
        var item = plan.standalone[s];
        var sev = item.event;
        kept[sev.id] = true;
        var snode = existing[sev.id];
        if (!snode || snode.tagName === "LI" || snode.classList.contains("au-inner") || snode.classList.contains("au-sysrow") !== !!item.sysStart) {
          if (snode && snode.parentNode) { auWrites++; snode.parentNode.removeChild(snode); }
          snode = auBuildRowNode(sev, false, item.sysStart);
        }
        snodes.push(snode);
      }
      // Walked last to first so each row's successor in its slot is already in place.
      for (var s2 = snodes.length - 1; s2 >= 0; s2--) {
        var item2 = plan.standalone[s2];
        var refEl = item2.beforeIndex < entries.length ? entries[item2.beforeIndex].el : null;
        var wantNext = (s2 + 1 < snodes.length && plan.standalone[s2 + 1].beforeIndex === item2.beforeIndex) ? snodes[s2 + 1] : refEl;
        if (snodes[s2].parentNode !== box || auNextRowEl(snodes[s2]) !== wantNext) {
          auWrites++;
          box.insertBefore(snodes[s2], wantNext);
        }
      }

      // 3. Remove any obsolete nodes no longer in plan.
      var exIds = Object.keys(existing);
      for (var r = 0; r < exIds.length; r++) {
        if (!kept[exIds[r]] && existing[exIds[r]].parentNode) {
          auWrites++;
          existing[exIds[r]].parentNode.removeChild(existing[exIds[r]]);
        }
      }
      auApplyRuns(box);
      if (pin && auWrites !== writes0 && typeof pinConvo === "function") pinConvo();
      if (window.WsAudit && window.WsAudit.syncSelection) window.WsAudit.syncSelection();
    }

    function auStoreEvents(list) {
      if (!Array.isArray(list) || !list.length) return false;
      var changed = false;
      for (var i = 0; i < list.length; i++) {
        var ev = list[i];
        if (!ev || !ev.id || !auIsHiddenAuditKind(ev.kind)) continue;
        auEventsById[ev.id] = ev;
        changed = true;
      }
      return changed;
    }

    function auBuildEventsURL(opts) {
      var qs = ["n=" + AU_EVENT_PAGE_LIMIT];
      if (opts.since) qs.push("since=" + encodeURIComponent(opts.since));
      if (opts.until) qs.push("until=" + encodeURIComponent(opts.until));
      if (opts.before) qs.push("before=" + encodeURIComponent(opts.before));
      if (opts.after) qs.push("after=" + encodeURIComponent(opts.after));
      return "/s/" + encodeURIComponent(auRowsRid) + "/audit/events?" + qs.join("&");
    }

    // since <= at < until, newest page first, bounded: a capped result is ok
    // with capped=true and floor = the oldest fetched event's time, so the
    // caller claims coverage only down to there.
    function auFetchInterval(since, until, updateLatestAfter) {
      if (!auRowsRid || !window.fetch) return Promise.resolve({ ok: false });
      var before = "";
      var restarted = false;
      var anyAdded = false;
      var maxPages = since ? AU_MAX_INTERVAL_PAGES : 1;
      var pages = 0, floor = "";
      var epoch = auEpoch, candidateAfter = "";
      function step() {
        if (epoch !== auEpoch) return Promise.resolve({ ok: false });
        var url = auBuildEventsURL({ since: since, until: until, before: before });
        return fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
          if (epoch !== auEpoch) return null;
          if (r.status === 410 && before && !restarted) {
            restarted = true;
            before = "";
            return { restart: true };
          }
          if (!r.ok) {
            var err = new Error("HTTP " + r.status);
            err.status = r.status;
            throw err;
          }
          return r.json();
        }).then(function (page) {
          if (epoch !== auEpoch) return { ok: false };
          if (page && page.restart) { pages = 0; return step(); }
          pages++;
          if (page && Array.isArray(page.events) && page.events.length) {
            if (auStoreEvents(page.events)) anyAdded = true;
            floor = page.events[0].at || floor;
          }
          if (updateLatestAfter && !before && page && page.after) candidateAfter = page.after;
          var more = page && !page.end && page.before;
          if (!more || pages >= maxPages) {
            if (candidateAfter) auLatestAfter = candidateAfter;
            if (anyAdded) auReconcileRows();
            return { ok: true, page: page, epoch: epoch, capped: !!more, floor: more ? floor : "" };
          }
          before = page.before;
          return step();
        });
      }
      return step();
    }

    // The lower bound a fetch of `wanted` actually covers.
    function auCoveredFrom(res, wanted) {
      return res && res.capped && res.floor && auCmpStrNs(res.floor, wanted) > 0 ? res.floor : wanted;
    }

    function auRefreshLatestAfter() {
      if (!auRowsRid || !window.fetch || !auLatestAfter) return Promise.resolve({ ok: false, skipped: true });
      var epoch = auEpoch;
      function step() {
        if (epoch !== auEpoch) return Promise.resolve({ ok: false });
        var cursor = auLatestAfter;
        var url = auBuildEventsURL({ after: cursor });
        return fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
          if (epoch !== auEpoch) return { ok: false };
          if (r.status === 410) {
            auLatestAfter = "";
            if (auReset410Done) return { ok: false, status: 410 };
            auReset410Done = true;
            return auFetchInterval(auCoveredSince || "", "", true).then(function (res) {
              if (res && res.ok && res.epoch === auEpoch) auReset410Done = false;
              return res;
            });
          }
          if (!r.ok) return { ok: false, status: r.status };
          return r.json().then(function (page) {
            if (epoch !== auEpoch) return { ok: false };
            auReset410Done = false;
            var nextAfter = page && page.after;
            if (nextAfter) auLatestAfter = nextAfter;
            if (page && Array.isArray(page.events) && auStoreEvents(page.events)) auReconcileRows();
            if (page && page.events && page.events.length && nextAfter && nextAfter !== cursor) return step();
            return { ok: true };
          });
        }).catch(function () { return { ok: false }; });
      }
      return step();
    }

    function auPruneBeforeFloor(floorAt) {
      if (!floorAt) return;
      var floorNs = auParseRFC3339Ns(floorAt);
      if (!floorNs) return;
      var ids = Object.keys(auEventsById);
      for (var i = 0; i < ids.length; i++) {
        var ev = auEventsById[ids[i]];
        var evNs = auParseRFC3339Ns(ev && ev.at);
        if (evNs && auCmpNs(evNs, floorNs) < 0) delete auEventsById[ids[i]];
      }
      auOpenBelowDone = false;
    }

    function auEnqueue(fn) {
      var epoch = auEpoch;
      function run(job) {
        if (epoch !== auEpoch) return Promise.resolve();
        return Promise.resolve().then(function () {
          if (epoch === auEpoch) return job();
        }).catch(function () {
          if (epoch === auEpoch) auFailedJobs.push(job);
        });
      }
      function work() {
        if (epoch !== auEpoch) return;
        var failed = auFailedJobs;
        auFailedJobs = [];
        return failed.reduce(function (p, job) { return p.then(function () { return run(job); }); }, Promise.resolve())
          .then(function () { return run(fn); });
      }
      auFetchQueue = auFetchQueue.then(work, work);
      return auFetchQueue;
    }

    function auInitialSync() {
      var box = auEntriesContainer();
      if (!box) return;
      var oldest = auFirstDataEntryAt(box);
      if (oldest) {
        auEnqueue(function () {
          return auFetchInterval(oldest, "", true).then(function (res) {
            if (res && res.ok && res.epoch === auEpoch) {
              var cov = auCoveredFrom(res, oldest);
              if (!auCoveredSince || auCmpStrNs(cov, auCoveredSince) < 0) auCoveredSince = cov;
            }
          });
        });
      } else {
        auEnqueue(function () {
          if (auUnboundedDone) return Promise.resolve();
          return auFetchInterval("", "", true).then(function (res) {
            if (res && res.ok && res.epoch === auEpoch) auUnboundedDone = true;
          });
        });
      }
    }

    doc.addEventListener("ws-history-page", function (e) {
      var pageEpoch = auEpoch;
      var d = (e && e.detail) || {};
      var box = auEntriesContainer();
      var oldest = d.oldestAt || auFirstDataEntryAt(box);
      auEnqueue(function () {
        var p = Promise.resolve({ ok: true, epoch: pageEpoch });
        if (d.empty && d.end && !oldest) {
          if (!auUnboundedDone) {
            p = auFetchInterval("", "", true).then(function (res) {
              if (res && res.ok && res.epoch === auEpoch) auUnboundedDone = true;
            });
          }
          return p;
        }
        if (oldest && auCoveredSince && auCmpStrNs(oldest, auCoveredSince) < 0) {
          var prevUpper = auCoveredSince;
          p = auFetchInterval(oldest, prevUpper, false).then(function (res) {
            if (res && res.ok && res.epoch === auEpoch) auCoveredSince = auCoveredFrom(res, oldest);
            return res;
          });
        } else if (oldest && !auCoveredSince) {
          p = auFetchInterval(oldest, "", true).then(function (res) {
            if (res && res.ok && res.epoch === auEpoch) auCoveredSince = auCoveredFrom(res, oldest);
            return res;
          });
        }
        if (d.end && oldest && !auOpenBelowDone) {
          p = p.then(function (res) {
            // A capped span leaves a gap above `oldest`; rows below it wait.
            if (pageEpoch !== auEpoch || !res || !res.ok || res.capped) return;
            return auFetchInterval("", oldest, false).then(function (res) {
              if (res && res.ok && res.epoch === auEpoch) auOpenBelowDone = true;
            });
          });
        }
        return p;
      });
    });

    doc.addEventListener("ws-entries-merged", function (e) {
      var d = (e && e.detail) || {};
      var box = auEntriesContainer();
      var oldest = d.oldestAt || auFirstDataEntryAt(box);
      if (oldest && auCoveredSince && auCmpStrNs(oldest, auCoveredSince) > 0) {
        auPruneBeforeFloor(oldest);
        auCoveredSince = oldest;
      }
      auReconcileRows();
      auEnqueue(function () {
        if (auLatestAfter) return auRefreshLatestAfter();
        if (!d.empty && oldest) {
          return auFetchInterval(auCoveredSince || oldest, "", true).then(function (res) {
            if (res && res.ok && res.epoch === auEpoch) {
              var cov = auCoveredFrom(res, oldest);
              if (!auCoveredSince || auCmpStrNs(cov, auCoveredSince) < 0) auCoveredSince = cov;
            }
          });
        }
        if (d.empty && !oldest) {
          return auFetchInterval("", "", true).then(function (res) {
            if (res && res.ok && res.epoch === auEpoch) auUnboundedDone = true;
          });
        }
        return Promise.resolve();
      });
    });

    doc.addEventListener("ws-history-reset", function () {
      var box = auEntriesContainer();
      var oldest = auFirstDataEntryAt(box);
      auEpoch++;
      auLatestAfter = "";
      auCoveredSince = "";
      auOpenBelowDone = false;
      auUnboundedDone = false;
      auReset410Done = false;
      auFailedJobs = [];
      auFetchQueue = Promise.resolve();
      auEventsById = Object.create(null);
      auExpandedRows = Object.create(null);
      auReconcileRows();
      if (oldest) auInitialSync();
    });

    // Click and keyboard handling inside conversation pane.
    function auSetCurrentRow(row) {
      if (auFocusedAuditEl && auFocusedAuditEl !== row) auFocusedAuditEl.classList.remove("au-cur");
      auFocusedAuditEl = row || null;
      if (row) row.classList.add("au-cur");
    }

    function auOpenAuditRow(row) {
      if (!row || !window.WsAudit) return;
      auSetCurrentRow(row);
      var evId = row.getAttribute("data-event-id");
      if (evId) {
        var ev = auEventsById[evId] || null;
        window.WsAudit.openEvent(evId, {
          rowEl: row,
          summary: ev,
          callId: row.getAttribute("data-call-ref") || (ev && ev.call_id) || "",
          tab: row.getAttribute("data-kind") === "other" ? "raw" : undefined
        });
        return;
      }
      var callId = row.getAttribute("data-call");
      if (callId) window.WsAudit.openCall(callId, row);
    }

    doc.addEventListener("click", function (e) {
      var sumEl = e.target && e.target.closest ? e.target.closest(".au-run") : null;
      if (sumEl) {
        auToggleRun(sumEl);
        return;
      }
      var row = e.target && e.target.closest ? e.target.closest(".au-hid[data-event-id], .au-sysrow[data-event-id]") : null;
      if (!row) return;
      var sel = window.getSelection && window.getSelection();
      if (sel && !sel.isCollapsed) return;
      auOpenAuditRow(row);
    });

    doc.addEventListener("keydown", function (e) {
      if (e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey) return;
      var t = e.target;
      if (!t || !t.closest) return;
      var tag = t.tagName || "";
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || t.isContentEditable) return;
      // Scope Decision (605): ONLY inside conversation pane (#convo),
      // never inside .au-win, #composer, #bcol, #files, #rail, drawers, or dialogs.
      if (t.closest(".au-win, #composer, .composer, #bcol, .bcol, #files, .fpane, #rail, .rail, .rpop, .drawer, .modal, [role='dialog']")) return;
      if (!t.closest("#convo")) return;

      if (e.key === "j" || e.key === "J" || e.key === "k" || e.key === "K") {
        var rows = window.WsAudit.visibleRows ? window.WsAudit.visibleRows() : [];
        if (!rows.length) return;
        e.preventDefault();
        var dir = (e.key === "j" || e.key === "J") ? 1 : -1;
        var cur = t.closest(".au-row[data-call], .au-hid[data-event-id], .au-sysrow[data-event-id]") || auFocusedAuditEl;
        var idx = cur ? rows.indexOf(cur) : -1;
        var nextIdx = idx < 0 ? (dir > 0 ? 0 : rows.length - 1) : Math.max(0, Math.min(rows.length - 1, idx + dir));
        var next = rows[nextIdx];
        auSetCurrentRow(next);
        if (typeof next.focus === "function") next.focus();
        if (typeof next.scrollIntoView === "function") {
          try { next.scrollIntoView({ block: "nearest" }); } catch (err) {}
        }
        return;
      }

      if ((e.key === "Enter" || e.key === " ") && t.matches && t.matches(".au-run")) {
        e.preventDefault();
        auToggleRun(t);
        return;
      }

      if (e.key === "Enter" || e.key === " ") {
        var hidRow = t.matches && t.matches(".au-hid[data-event-id], .au-sysrow[data-event-id]") ? t : null;
        if (hidRow) {
          e.preventDefault();
          auOpenAuditRow(hidRow);
          return;
        }
        if (e.key === "Enter" && !t.closest("a, button, summary, details > summary, .d-act")) {
          var targetRow = auFocusedAuditEl && auFocusedAuditEl.isConnected
            ? auFocusedAuditEl
            : null;
          if (targetRow) {
            e.preventDefault();
            auOpenAuditRow(targetRow);
          }
        }
      }
    });

    auInitialSync();
    window.WsAuditRows = {
      parseRFC3339Ns: auParseRFC3339Ns,
      cmpNs: auCmpNs,
      cmpAuditEvent: auCmpAuditEvent,
      isHiddenKind: auIsHiddenAuditKind,
      isSessionStartEvent: auIsSessionStartEvent,
      isRunKind: auIsRunKind,
      runLabel: auRunLabel,
      interleavePlan: auInterleavePlan,
      reconcile: auReconcileRows
    };
  }
