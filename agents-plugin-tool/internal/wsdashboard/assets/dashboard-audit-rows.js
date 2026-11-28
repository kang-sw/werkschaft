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

  function auEventOwnerKind(ev) {
    var sub = String(ev.subkind || "");
    if (ev.kind === "compaction" || sub === "compact_failed") return "compaction";
    if (sub === "api_retry") return "retry";
    if (sub === "request:session/request_permission" ||
        /^control_request:can_use_tool$|^request:.*requestApproval/i.test(sub)) return "approval";
    if (ev.kind === "subagent" && (sub.indexOf("task_") === 0 || sub.indexOf("launch_") === 0)) return "subagent";
    return "";
  }

  function auNearestOwner(owners, ev, boundary, used) {
    var at = auParseRFC3339Ns(ev.at);
    var nearest = null, distance = Infinity;
    for (var i = 0; i < owners.length; i++) {
      var owner = owners[i];
      if (owner.kind !== boundary || (used && used.has(owner.el))) continue;
      if (boundary === "approval" && owner.call && ev.call_id) {
        if (owner.call === ev.call_id) return owner;
        continue;
      }
      if (boundary === "compaction" && owner.failed !== (ev.subkind === "compact_failed")) continue;
      if (!owner.at || !at) continue;
      var delta = Math.abs(owner.at.ms - at.ms + (owner.at.ns - at.ns) / 1000000);
      // The wire event and the corresponding ring entry are stamped separately.
      if (delta <= 2000 && delta < distance) {
        distance = delta;
        nearest = owner;
      }
    }
    return nearest;
  }

  function auInterleavePlan(entries, events, renderedCallIds, owners) {
    owners = owners || [];
    var sorted = events.filter(function (ev) { return ev && ev.id; }).slice().sort(auCmpAuditEvent);
    var attached = Object.create(null), standalone = [], boundaryItems = new Map(), owned = new Map();
    var callSummaries = Object.create(null), attachmentCalls = Object.create(null);
    var completedTasks = Object.create(null), seenBoundaries = new Set();
    var turnEntries = entries.filter(function (ent) { return ent.turn; });
    var lastTurnAt = turnEntries.length ? turnEntries[turnEntries.length - 1].at : "";
    var retry = owners.find(function (owner) { return owner.kind === "retry"; });

    // Associate the active retry with its newest event in this turn. The
    // phase start is shared with other live states, so it is no retry timestamp.
    for (var i = sorted.length - 1; i >= 0; i--) {
      var ev = sorted[i], kind = auEventOwnerKind(ev);
      if (ev.kind === "tool_call" && ev.call_id) {
        callSummaries[ev.call_id] = ev;
        (ev.attached || []).forEach(function (id) { attachmentCalls[id] = ev.call_id; });
      }
      var owner = null;
      if (kind === "approval" || kind === "compaction" || ev.subkind === "task_notification" || ev.subkind === "launch_completed" || ev.subkind === "launch_failed") {
        owner = auNearestOwner(owners, ev, kind, seenBoundaries);
      } else if (kind === "retry" && retry && !seenBoundaries.has(retry.el) && (!lastTurnAt || auCmpStrNs(ev.at, lastTurnAt) > 0)) {
        owner = retry;
      }
      if (!owner) continue;
      owned.set(ev.id, owner);
      seenBoundaries.add(owner.el);
      if (kind === "subagent" && ev.agent_id) completedTasks[ev.agent_id] = { owner: owner, at: ev.at };
    }

    var start = null, turns = new Map(), turnIndex = 0, entryIndex = 0;
    var turnItems = function (turn) {
      var footer = turn.footer || turn.el;
      if (!turns.has(footer)) {
        var list = [];
        list.auditCounts = Object.create(null);
        turns.set(footer, list);
      }
      return turns.get(footer);
    };
    var attach = function (call, event) {
      if (!attached[call]) attached[call] = [];
      attached[call].push(event);
    };

    for (var j = 0; j < sorted.length; j++) {
      var row = sorted[j];
      if (!auIsHiddenAuditKind(row.kind)) continue;
      while (turnIndex < turnEntries.length && auCmpStrNs(turnEntries[turnIndex].at, row.at) < 0) turnIndex++;
      var timeline = turnIndex < turnEntries.length ? turnItems(turnEntries[turnIndex]) : null;
      if (timeline && row.subkind !== "tool_progress") {
        timeline.auditCounts[row.kind] = (timeline.auditCounts[row.kind] || 0) + 1;
      }
      var ownerKind = auEventOwnerKind(row);
      var target = owned.get(row.id);
      var task = ownerKind === "subagent" && completedTasks[row.agent_id];
      if (!target && task && auCmpStrNs(row.at, task.at) <= 0) target = task.owner;
      if (target) {
        if (!boundaryItems.has(target.el)) boundaryItems.set(target.el, []);
        boundaryItems.get(target.el).push(row);
        continue;
      }
      var call = row.call_id || attachmentCalls[row.id] || row.parent_id;
      if ((!ownerKind || ownerKind === "approval") && call && renderedCallIds[call]) {
        attach(call, row);
        continue;
      }
      if (row.subkind === "tool_progress") continue;
      if (!start && auIsSessionStartEvent(row)) {
        start = row;
        standalone.unshift({ event: row, sysStart: true, beforeIndex: 0 });
        continue;
      }
      if (timeline) {
        timeline.push({ event: row, at: row.at, seq: row.seq });
        continue;
      }
      // With no finished turn after this event, leave low-signal events for
      // the upcoming turn. Other provider events stay available as traces.
      if (auWaitForTurn(row)) continue;
      while (entryIndex < entries.length && (!entries[entryIndex].at || auCmpStrNs(entries[entryIndex].at, row.at) <= 0)) entryIndex++;
      standalone.push({ event: row, sysStart: false, beforeIndex: entryIndex });
    }

    var pending = [];
    for (var n = 0; n < entries.length; n++) {
      var ent = entries[n];
      if (ent.tools) {
        ent.tools.forEach(function (tool) {
          var summary = callSummaries[tool.call];
          pending.push({ source: tool.el, tool: true, key: tool.key, at: summary ? summary.at : ent.at, seq: summary && summary.seq });
        });
      } else if (ent.turn) {
        var list = turnItems(ent);
        Array.prototype.push.apply(list, pending);
        list.sort(auCmpAuditEvent);
        pending = [];
      } else if (ent.source) {
        pending.push({ source: ent.source, key: ent.key, at: ent.at });
      }
    }
    return { attached: attached, standalone: standalone, boundaryItems: boundaryItems, turns: turns };
  }

  var AU_COUNTED_KINDS = [
    { kind: "system", i18n: "js.audit.kind.system" },
    { kind: "hook", i18n: "js.audit.kind.hook" },
    { kind: "system_reminder", i18n: "js.audit.kind.reminder" },
    { kind: "thinking", i18n: "js.audit.kind.thinking" },
    { kind: "subagent", i18n: "js.audit.kind.subagent" },
    { kind: "compaction", i18n: "js.audit.kind.compaction" },
    { kind: "other", i18n: "js.audit.kind.unmapped" }
  ];

  function auWaitForTurn(ev) {
    return /^(system|hook|system_reminder|thinking)$/.test(ev.kind) ||
      (ev.kind === "subagent" && /^(task_|launch_)/.test(ev.subkind || ""));
  }

  function auTurnLabel(items) {
    var counts = items.auditCounts, parts = [];
    AU_COUNTED_KINDS.forEach(function (kind) {
      if (counts[kind.kind]) parts.push(wsT(kind.i18n) + " " + counts[kind.kind]);
    });
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
      for (var c = box.firstElementChild; c; c = c.nextElementSibling) {
        var key = c.getAttribute("data-entry");
        if (!key && c.classList.contains("apanswered")) key = "approval:" + c.getAttribute("data-approval-id");
        if (!key || c.hasAttribute("hidden")) continue;
        var turnFooter = c.getAttribute("data-conv-turn") === "1" && c.hasAttribute("data-turn-at") ? c : null;
        var footer = c.classList.contains("turnline") ? c : (turnFooter || c.querySelector(".turnline"));
        var at = (turnFooter && turnFooter.getAttribute("data-turn-at")) || c.getAttribute("data-at") || "";
        var ent = { el: c, key: key, at: at, turn: !!footer, footer: footer };
        if (!footer && c.matches(".msg, .user-batch, .mailline, .mail-block, .mail-notification, .exec-notice, .wbeffects, .boundary-badge, .apanswered, .cardref, .cmdrun, .uiline, .input-record")) ent.source = c;
        if (c.matches(".au-tools, .toolsline")) {
          var rows = Array.from(c.querySelectorAll(".au-row"));
          if (!rows.length) rows = Array.from(c.querySelectorAll(".brows .brow:not(.more)"));
          if (!rows.length) rows = [c.firstElementChild || c];
          ent.tools = rows.map(function (row, i) {
            return { el: row, call: row.getAttribute("data-call"), key: key + ":" + i };
          });
        }
        list.push(ent);
      }
      return list;
    }

    function auRenderedCallMap(box) {
      var map = Object.create(null);
      var rows = box.querySelectorAll(".au-row[data-call]");
      for (var i = 0; i < rows.length; i++) map[rows[i].getAttribute("data-call")] = rows[i];
      return map;
    }

    function auDirectItems(el) {
      for (var c = el.firstElementChild; c; c = c.nextElementSibling) {
        if (c.classList.contains("conv-audit-items")) return c;
      }
      return null;
    }

    function auRenderedOwners(convo) {
      return Array.from(convo.querySelectorAll("[data-audit-owner]")).map(function (el) {
        var at = auParseRFC3339Ns(el.getAttribute("data-at"));
        var kind = el.getAttribute("data-audit-owner"), ms = Number(el.getAttribute("data-at-ms"));
        if (!at && ms && kind === "compaction") at = { ms: ms, ns: 0 };
        return { el: el, kind: kind, at: at, call: el.getAttribute("data-approval-call"), failed: el.classList.contains("fail") };
      });
    }

    function auText(parent, tag, cls, value) {
      var el = doc.createElement(tag);
      el.className = cls;
      el.textContent = value || "";
      parent.appendChild(el);
      return el;
    }

    function auTime(parent, at) {
      var time = auText(parent, "time", "row-time");
      var parsed = auParseRFC3339Ns(at);
      if (!parsed) return;
      time.setAttribute("datetime", at);
      time.setAttribute("data-ms", parsed.ms);
      if (typeof fmtTimes === "function") fmtTimes(parent);
    }

    function auBuildRowNode(ev) {
      var el = doc.createElement("div");
      el.className = "au-hid conv-row au-kind-" + ev.kind + (ev.kind === "compaction" && ev.estimated ? " au-estimated" : "");
      el.setAttribute("data-event-id", ev.id);
      el.setAttribute("data-kind", ev.kind);
      if (ev.at) el.setAttribute("data-at", ev.at);
      if (ev.call_id) el.setAttribute("data-call-ref", ev.call_id);
      el.tabIndex = 0;
      el.setAttribute("role", "button");
      var icon = auText(el, "span", "row-icon au-event-icon");
      icon.setAttribute("aria-hidden", "true");
      var icons = { system: "gear", thinking: "chat", hook: "wrench", system_reminder: "clock", compaction: "archive", subagent: "agents" };
      icon.innerHTML = '<svg class="i"><use href="#i-' + (icons[ev.kind] || "ticket") + '"></use></svg>';
      var content = auText(el, "span", "row-content trace-content");
      auText(content, "span", "au-hbadge row-name", auHiddenKindLabel(ev.kind));
      if (ev.kind === "compaction" && ev.estimated) {
        var est = auText(content, "span", "au-est-badge", wsT("js.audit.compaction.estimated"));
        est.title = wsT("js.audit.compaction.estimated.note");
      }
      var preview = auFormatHiddenPreview(ev, window.WsAudit.fmtBytes);
      var pv = auText(content, "span", "au-pv", preview);
      if (preview) pv.title = preview;
      auTime(el, ev.at);
      return el;
    }

    function auBuildSessionStart(ev) {
      var el = doc.createElement("details");
      el.className = "sysline boundary-badge au-sysrow";
      var sum = auText(el, "summary", "");
      var icon = auText(sum, "span", "row-icon");
      icon.innerHTML = '<svg class="i" viewBox="0 0 16 16" aria-hidden="true"><path fill="currentColor" d="M5 3v10l8-5z"/></svg>';
      var text = wsT("js.audit.sysrow.start"), size = ev.out_bytes || ev.in_bytes;
      auText(sum, "span", "au-sysrow-lbl", size ? text + " · " + window.WsAudit.fmtBytes(size) : text);
      auTime(sum, ev.at);
      auText(el, "div", "conv-audit-items");
      return el;
    }

    function auReferenceText(el) {
      if (el.tagName === "TIME" || el.classList.contains("row-time") || el.classList.contains("conv-audit-items")) return "";
      if (!el.firstElementChild) return el.textContent || "";
      var text = [];
      for (var c = el.firstChild; c; c = c.nextSibling) {
        if (c.nodeType === 3) text.push(c.nodeValue);
        else if (c.tagName) text.push(auReferenceText(c));
      }
      return text.join(" ");
    }

    function auBuildTurnReference(item) {
      var source = item.source;
      var el = doc.createElement("button");
      el.type = "button";
      el.className = "conv-row au-turn-tool";
      var name = source.querySelector(".au-tool, .bp, .tsl, .mpeer, .nt, .wbupdate-head, .row-content > b, .cra");
      var preview = source.querySelector(".au-args, .mfirst, .bub, .cmdecho");
      var meta = item.tool && source.querySelector(".au-dur, .bm");
      var glyph = source.querySelector(".row-icon use, summary .mk use, .wbupdate-head use");
      if (source.classList.contains("turn-trigger") || source.querySelector(".turn-trigger")) el.classList.add("au-turn-trigger");
      if (source.classList.contains("au-fail") || source.classList.contains("fail")) el.classList.add("au-fail");
      var icon = auText(el, "span", "row-icon");
      if (glyph) icon.appendChild(glyph.parentNode.cloneNode(true));
      else icon.innerHTML = '<svg class="i" aria-hidden="true"><use href="#i-' + (item.tool ? (el.classList.contains("au-fail") ? "x" : "check") : "chat") + '"></use></svg>';
      var content = auText(el, "span", "row-content trace-content");
      if (name) auText(content, "span", "au-tool row-name", auReferenceText(name).trim());
      if (preview || !name) {
        var text = auReferenceText(preview || source).trim().replace(/\s+/g, " ");
        if (text) auText(content, "span", "au-pv", text.slice(0, 200));
      }
      if (meta && meta.textContent) auText(content, "span", "au-dur", meta.textContent);
      auTime(el, item.at);
      return el;
    }

    var auReconcileQueued = false, auReconcileRaf = 0, auReconcileTimer = 0;

    function auCancelScheduledReconcile() {
      auReconcileQueued = false;
      if (auReconcileRaf && typeof cancelAnimationFrame === "function") cancelAnimationFrame(auReconcileRaf);
      auReconcileRaf = 0;
      if (auReconcileTimer && typeof clearTimeout === "function") clearTimeout(auReconcileTimer);
      auReconcileTimer = 0;
    }

    function auFlushScheduledReconcile() {
      if (!auReconcileQueued) return;
      auCancelScheduledReconcile();
      auReconcileRows();
    }

    function auScheduleReconcile() {
      if (auReconcileQueued) return;
      auReconcileQueued = true;
      var hidden = !!(doc && doc.hidden) || (typeof shHidden !== "undefined" && !!shHidden);
      if (!hidden && typeof requestAnimationFrame === "function") {
        auReconcileRaf = requestAnimationFrame(auFlushScheduledReconcile);
      } else if (typeof setTimeout === "function") {
        auReconcileTimer = setTimeout(auFlushScheduledReconcile, 0);
      } else {
        Promise.resolve().then(auFlushScheduledReconcile);
      }
    }

    function auReconcileRows() {
      auCancelScheduledReconcile();
      var box = auEntriesContainer();
      if (!box) return;
      var convo = auConvo(), pin = typeof stick !== "undefined" && stick, changed = false;
      var entries = auRenderedEntries(box);
      var callMap = auRenderedCallMap(convo);
      var owners = auRenderedOwners(convo);
      var allEvents = Object.keys(auEventsById).map(function (id) { return auEventsById[id]; });
      var plan = auInterleavePlan(entries, allEvents, callMap, owners);
      var existing = new Map(), placements = new Map();
      Array.from(convo.querySelectorAll("[data-au-row-key]")).forEach(function (el) {
        existing.set(el.getAttribute("data-au-row-key"), el);
      });
      function node(key, build) {
        var el = existing.get(key);
        if (el) existing.delete(key);
        else {
          el = build();
          el.setAttribute("data-au-row-key", key);
        }
        return el;
      }
      function place(parent, el, before) {
        if (!parent) return;
        if (!placements.has(parent)) placements.set(parent, []);
        placements.get(parent).push({ el: el, before: before || null });
      }
      function placeEvent(parent, ev, before) {
        if (parent) place(parent, node("event:" + ev.id, function () { return auBuildRowNode(ev); }), before);
      }
      plan.boundaryItems.forEach(function (events, owner) {
        var body = auDirectItems(owner);
        events.forEach(function (ev) { placeEvent(body, ev); });
      });
      owners.forEach(function (owner) {
        if (owner.kind !== "compaction") return;
        var sum = owner.el.querySelector("summary");
        if (!sum) return;
        var evs = plan.boundaryItems.get(owner.el);
        var est = !!(evs && evs.some(function (ev) { return ev.kind === "compaction" && ev.estimated; }));
        var cur = sum.querySelector(".au-est-badge");
        if (est) {
          if (!cur) {
            var badge = doc.createElement("span");
            badge.className = "au-est-badge";
            badge.textContent = wsT("js.audit.compaction.estimated");
            badge.title = wsT("js.audit.compaction.estimated.note");
            var timeEl = sum.querySelector("time");
            sum.insertBefore(badge, timeEl || null);
            changed = true;
          }
        } else if (cur && cur.parentNode) {
          cur.parentNode.removeChild(cur);
          changed = true;
        }
      });
      Object.keys(plan.attached).forEach(function (call) {
        var row = callMap[call];
        var detail = row.closest(".au-call-details");
        var body = detail ? auDirectItems(detail) : row.parentNode;
        plan.attached[call].forEach(function (ev) { placeEvent(body, ev, detail ? null : row.nextElementSibling); });
      });
      plan.turns.forEach(function (items, footer) {
        var counts = footer.querySelector(".turn-audit-counts");
        if (counts) {
          var label = auTurnLabel(items);
          if (counts.textContent !== label) { counts.textContent = label; changed = true; }
          var hide = !label;
          if (counts.hidden !== hide) { counts.hidden = hide; changed = true; }
        }
        var body = auDirectItems(footer);
        if (!body) return;
        items.forEach(function (item) {
          if (item.event) placeEvent(body, item.event);
          else {
            var ref = node("turn-ref:" + item.key, function () { return auBuildTurnReference(item); });
            ref.auSource = item.source;
            place(body, ref);
          }
        });
      });
      plan.standalone.forEach(function (item) {
        var ev = item.event, before = entries[item.beforeIndex] && entries[item.beforeIndex].el;
        if (item.sysStart) {
          var start = node("start:" + ev.id, function () { return auBuildSessionStart(ev); });
          placeEvent(auDirectItems(start), ev);
          place(box, start, before);
        } else placeEvent(box, ev, before);
      });
      existing.forEach(function (el) {
        if (el.parentNode) { el.parentNode.removeChild(el); changed = true; }
      });
      placements.forEach(function (list, parent) {
        var next = null, lastBefore = null;
        for (var i = list.length - 1; i >= 0; i--) {
          var item = list[i], el = item.el;
          if (i === list.length - 1 || item.before !== lastBefore) next = item.before;
          if (el.parentNode !== parent || el.nextElementSibling !== next) {
            parent.insertBefore(el, next);
            changed = true;
          }
          next = el;
          lastBefore = item.before;
        }
      });
      Array.from(convo.querySelectorAll(".boundary-badge")).forEach(function (badge) {
        var items = auDirectItems(badge);
        if (items && items.firstElementChild) badge.setAttribute("data-has-audit", "");
        else badge.removeAttribute("data-has-audit");
      });
      if (pin && changed && typeof pinConvo === "function") pinConvo();
      if (window.WsAudit.syncSelection) window.WsAudit.syncSelection();
    }

    function auStoreEvents(list) {
      if (!Array.isArray(list) || !list.length) return false;
      var changed = false;
      for (var i = 0; i < list.length; i++) {
        var ev = list[i];
        if (!ev || !ev.id || (!auIsHiddenAuditKind(ev.kind) && ev.kind !== "tool_call")) continue;
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
            if (anyAdded) auScheduleReconcile();
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
            if (page && Array.isArray(page.events) && auStoreEvents(page.events)) auScheduleReconcile();
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
      auScheduleReconcile();
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
      auScheduleReconcile();
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
      auPreviewCache.clear();
      auScheduleReconcile();
      if (oldest) auInitialSync();
    });

    function auInlineJSON(url) {
      return fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (response) {
        if (!response.ok) throw new Error("Audit preview HTTP " + response.status);
        return response.json();
      });
    }

    function auInlineCallBody(base, event, part) {
      if (!event || !event.id || !event[part + "_bytes"]) return Promise.resolve(null);
      return auInlineJSON(base + "/event/" + encodeURIComponent(event.id) + "?part=" + part + "&len=4096").then(function (range) {
        return range && range.data ? range.data + (range.end ? "" : "\n…") : null;
      });
    }

    // Completed calls are immutable, so their fetched input/output preview is
    // kept by call id (bounded, least recently used first out). A details that
    // is rendered anew, or opened again, applies it synchronously: no fetch and
    // no frame in which the preview is missing.
    var AU_PREVIEW_CAP = 48, auPreviewCache = new Map();
    function auPreviewGet(call) {
      var v = auPreviewCache.get(call);
      if (v) { auPreviewCache.delete(call); auPreviewCache.set(call, v); }
      return v || null;
    }
    function auPreviewPut(call, v) {
      auPreviewCache.delete(call);
      auPreviewCache.set(call, v);
      while (auPreviewCache.size > AU_PREVIEW_CAP) auPreviewCache.delete(auPreviewCache.keys().next().value);
    }

    // auApplyPreview writes parts ([input, output]) into detail, touching only
    // what differs so an update of an open preview never changes its height
    // twice. It reports whether the DOM changed.
    function auApplyPreview(detail, summary, parts) {
      var input = detail.querySelector(".au-call-input"), changed = false;
      if (parts[0]) {
        if (!input) {
          input = doc.createElement("pre");
          input.className = "au-call-input";
          detail.insertBefore(input, summary.nextSibling);
          changed = true;
        }
        if (input.textContent !== parts[0]) { input.textContent = parts[0]; changed = true; }
      }
      if (parts[1]) {
        var text = wsT("js.audit.tab.out") + "\n" + parts[1];
        var output = detail.querySelector(".au-call-output");
        if (!output) {
          output = doc.createElement("pre");
          output.className = "au-call-output";
          detail.insertBefore(output, auDirectItems(detail));
          changed = true;
        }
        if (output.textContent !== text) { output.textContent = text; changed = true; }
      }
      detail.setAttribute("data-au-preview", "");
      if (changed && detail.open && typeof stick !== "undefined" && stick && typeof pinConvo === "function") pinConvo();
      return changed;
    }

    var auInlinePending = new WeakSet();
    function auLoadPreview(detail) {
      if (!detail.matches || !detail.matches(".au-call-details[open]") ||
          detail.hasAttribute("data-au-preview") || auInlinePending.has(detail)) return;
      var summary = detail.querySelector(".au-row[data-call]");
      if (!summary) return;
      var call = summary.getAttribute("data-call"), cached = auPreviewGet(call);
      if (cached) {
        detail.removeAttribute("data-au-out-pending");
        detail.removeAttribute("data-au-refresh-queued");
        auApplyPreview(detail, summary, cached);
        return;
      }
      if (!auRowsRid) return;
      auInlinePending.add(detail);
      var epoch = typeof auEpoch === "number" ? auEpoch : 0;
      var tokEl = detail.querySelector(".au-tok");
      detail.setAttribute("data-au-tok-snap", tokEl ? tokEl.textContent.trim() : "");
      detail.setAttribute("data-au-out-inflight", "");
      var base = "/s/" + encodeURIComponent(auRowsRid) + "/audit", settled = false, failed = false, applied = false;
      auInlineJSON(base + "/events?call=" + encodeURIComponent(call)).then(function (bundle) {
        if (!bundle || epoch !== (typeof auEpoch === "number" ? auEpoch : 0) || !detail.isConnected) return;
        var result = (bundle.events || []).find(function (ev) { return auIsResultSummary(ev, call); });
        settled = !!result;
        return Promise.all([auInlineCallBody(base, bundle.call, "in"), auInlineCallBody(base, result, "out")]);
      }).then(function (parts) {
        if (!parts || epoch !== (typeof auEpoch === "number" ? auEpoch : 0) || !detail.isConnected) return;
        auApplyPreview(detail, summary, parts);
        applied = true;
        // A call without a result is still running: remember to read its
        // output once the live turn row reports it complete.
        if (settled) {
          detail.removeAttribute("data-au-out-pending");
          detail.removeAttribute("data-au-refresh-queued");
          detail.removeAttribute("data-au-tok-snap");
          auPreviewPut(call, parts);
        } else {
          var curTok = detail.querySelector(".au-tok");
          detail.setAttribute("data-au-tok-snap", curTok ? curTok.textContent.trim() : "");
          detail.setAttribute("data-au-out-pending", "");
        }
      }).catch(function () {
        failed = true;
        // A later open or update retries a failed bundle or body. Keep
        // data-au-out-pending intact so transient errors are retried.
      }).finally(function () {
        auInlinePending.delete(detail);
        detail.removeAttribute("data-au-out-inflight");
        var curEpoch = typeof auEpoch === "number" ? auEpoch : 0;
        var canRetry = !failed && epoch === curEpoch && detail.isConnected && !settled && detail.hasAttribute("data-au-refresh-queued");
        if (failed || !detail.isConnected || epoch !== curEpoch) {
          detail.removeAttribute("data-au-refresh-queued");
        }
        if ((!settled || !applied) && !detail.hasAttribute("data-au-preview") && epoch === curEpoch) {
          detail.setAttribute("data-au-out-pending", "");
        }
        if (canRetry) {
          detail.removeAttribute("data-au-refresh-queued");
          detail.removeAttribute("data-au-preview");
          auLoadPreview(detail);
        }
      });
    }
    doc.addEventListener("toggle", function (e) { auLoadPreview(e.target); }, true);
    function auHasCompletionSignal(detail, evDetail) {
      if (evDetail && evDetail.completed) return true;
      var dur = detail.querySelector(".au-dur");
      if (dur && dur.textContent.trim()) return true;
      var row = detail.querySelector(".au-row");
      if (row && row.classList && row.classList.contains("au-fail")) return true;
      var tok = detail.querySelector(".au-tok");
      var tokTxt = tok ? tok.textContent.trim() : "";
      if (tokTxt) {
        var snap = detail.getAttribute("data-au-tok-snap");
        if (snap === null || tokTxt !== snap) return true;
      }
      var turn = detail.closest ? detail.closest(".d-turn") : null;
      return !!(turn && turn.classList && !turn.classList.contains("d-turn-live"));
    }
    // The conversation merge keeps an open call's details alive and syncs its
    // summary in place. A call that was running when its preview loaded has no
    // output yet; once its summary or turn signals completion, the output is
    // read in place beside the input already shown.
    doc.addEventListener("ws-call-details-updated", function (e) {
      var evDetail = (e && e.detail) || {};
      var detail = evDetail.el;
      if (!detail || !detail.hasAttribute) return;
      var inflight = detail.hasAttribute("data-au-out-inflight") || auInlinePending.has(detail);
      var pending = detail.hasAttribute("data-au-out-pending");
      if (!inflight && !pending) return;
      if (!auHasCompletionSignal(detail, evDetail)) return;
      if (inflight) {
        detail.setAttribute("data-au-refresh-queued", "");
        return;
      }
      detail.removeAttribute("data-au-preview");
      auLoadPreview(detail);
    });

    doc.addEventListener("ws-approval-rows-changed", auScheduleReconcile);
    var liveRegion = doc.getElementById("r-convlive");
    if (liveRegion && typeof MutationObserver !== "undefined") {
      // Only the region's root replacement matters. Audit writes inside it
      // should not themselves schedule a reconcile.
      var liveSelector = "[data-audit-owner], .au-row[data-call]";
      var hadOwners = !!liveRegion.querySelector(liveSelector);
      function retryAttempt() {
        var retry = liveRegion.querySelector("[data-retry-attempt]");
        return retry && retry.getAttribute("data-retry-attempt");
      }
      var lastAttempt = retryAttempt();
      new MutationObserver(function () {
        var hasOwners = !!liveRegion.querySelector(liveSelector), attempt = retryAttempt();
        if (hadOwners || hasOwners) auScheduleReconcile();
        if (attempt && attempt !== lastAttempt) {
          auEnqueue(function () { if (auLatestAfter) return auRefreshLatestAfter(); });
        }
        hadOwners = hasOwners;
        lastAttempt = attempt;
      }).observe(liveRegion, { childList: true });
    }

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
      var target = e.target;
      if (!target || !target.closest) return;
      var ref = target.closest(".au-turn-tool");
      if (ref && ref.auSource && ref.auSource.isConnected) {
        var source = ref.auSource;
        var detail = source.closest("details") || source.querySelector("details");
        if (detail) detail.open = true;
        source.scrollIntoView({ block: "nearest" });
        return;
      }
      var row = target.closest(".au-hid[data-event-id]");
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

    auReconcileRows();
    auInitialSync();
    window.WsAuditRows = {
      parseRFC3339Ns: auParseRFC3339Ns,
      cmpNs: auCmpNs,
      cmpAuditEvent: auCmpAuditEvent,
      isHiddenKind: auIsHiddenAuditKind,
      isSessionStartEvent: auIsSessionStartEvent,
      interleavePlan: auInterleavePlan,
      reconcile: auReconcileRows
    };
  }
