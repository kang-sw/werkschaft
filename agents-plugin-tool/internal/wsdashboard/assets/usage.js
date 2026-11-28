/* Account plan usage: viewer-only cache, bottom-bar/header handle, and unified 2-chart popover. */
(function (window) {
  "use strict";
  var DUR = { five_hour: 5 * 3600000, seven_day: 7 * 86400000 };
  var SVGNS = "http://www.w3.org/2000/svg";
  function active(entry, now) {
    return (entry.windows || []).filter(function (w) { return !w.resetsAt || w.resetsAt > now; });
  }
  function vendorOrder(entry) {
    if (entry.harness === "claude") return 0;
    if (entry.harness === "codex" || entry.harness === "codex_openai") return 1;
    if (entry.harness === "agy") {
      if (entry.group === "gemini") return 2;
      if (entry.group === "claude_gpt") return 3;
      return 4;
    }
    if (entry.harness === "werk_openai") return 5;
    if (entry.harness === "openrouter") return 6;
    return 7;
  }
  function sorted(entries) {
    return entries.slice().sort(function (a, b) { return vendorOrder(a) - vendorOrder(b); });
  }
  function level(percent, warn, crit) { return percent >= crit ? "critical" : percent >= warn ? "warning" : "normal"; }
  function age(at, now) { return at ? "value from " + Math.max(0, Math.floor((now - at) / 60000)) + " min ago" : "usage unavailable"; }
  function shortAge(at, now) {
    if (!at) return "";
    var mins = Math.max(0, Math.floor((now - at) / 60000));
    if (mins < 60) return mins + "m ago";
    var hours = Math.floor(mins / 60);
    if (hours < 24) return hours + "h ago";
    return Math.floor(hours / 24) + "d ago";
  }
  function entryName(entry) {
    if (entry.harness === "claude") return "Claude";
    if (entry.harness === "codex") return "Codex / GPT";
    if (entry.harness === "codex_openai") return entry.label || "Codex / OpenAI";
    if (entry.harness === "werk_openai") return entry.label || "Werk (OpenAI)";
    if (entry.harness === "openrouter") return entry.label || "OpenRouter";
    if (entry.harness === "agy") return entry.label || "Antigravity";
    return entry.label || String(entry.harness || "");
  }
  function formatUsd(value) {
    if (typeof value !== "number" || !Number.isFinite(value)) return "—";
    if (Math.abs(value) > 0 && Math.abs(value) < 0.01) return "$" + value.toFixed(4);
    return "$" + value.toFixed(2);
  }
  function formatTokens(count) {
    if (typeof count !== "number" || !Number.isFinite(count) || count <= 0) return "0";
    if (count >= 1000000) return (count / 1000000).toFixed(1).replace(/\.0$/, "") + "M";
    if (count >= 1000) return (count / 1000).toFixed(1).replace(/\.0$/, "") + "k";
    return String(Math.round(count));
  }
  function footerGroupLabel(entry) {
    if (entry.harness !== "agy" || !entry.label) return "";
    if (entry.group === "gemini") return "Gemini";
    if (entry.group === "claude_gpt") return "Claude/GPT";
    return entry.label;
  }
  function label(kind) { return kind === "five_hour" ? "5-hour" : "Weekly"; }
  function observation(w, entry) { return w.asOf || (entry && entry.asOf) || 0; }
  function stale(at, now) { return !!at && now - at > 30 * 60000; }
  function unreported(at, now) { return !at || stale(at, now); }
  function pace(w, at) {
    var duration = DUR[w.kind] || 7 * 86400000;
    if (!w.resetsAt || !at) return null;
    var elapsed = Math.max(0, Math.min(1, (at - (w.resetsAt - duration)) / duration));
    return Math.round(w.percent - elapsed * 100);
  }
  function signedDelta(n) { return (n > 0 ? "+" : n < 0 ? "\u2212" : "") + Math.abs(n) + "%"; }
  function remainText(ms) {
    var m = Math.max(0, Math.round(ms / 60000));
    if (m < 60) return m + "m";
    if (m < 1440) return Math.floor(m / 60) + "h " + (m % 60) + "m";
    return Math.floor(m / 1440) + "d " + Math.floor((m % 1440) / 60) + "h";
  }
  // Pure chart geometry. Crossing points and gap interpolation are visual
  // only: points contains real observations, never an origin or extrapolation.
  function trend(entry, w, history) {
    var duration = DUR[w.kind] || 7 * 86400000;
    var at = observation(w, entry), reset = w.resetsAt || 0, start = reset - duration;
    var knownReset = reset > 0, samples = [], points = [], segments = [];
    var valid = function (s) { return s.asOf > 0 && Number.isFinite(s.percent) && s.percent >= 0 && s.percent <= 100; };
    var identity = function (s) { return [s.source || "", s.resetsAt || 0, s.asOf].join("|"); };
    var indices = Object.create(null);
    function upsert(s) {
      var key = identity(s);
      if (indices[key] !== undefined) samples[indices[key]] = s;
      else { indices[key] = samples.length; samples.push(s); }
    }
    var entryGroup = (entry && entry.group) || "";
    var rawHarness = (entry && entry.harness) || "";
    var historyHarness = rawHarness === "codex_openai" ? "codex" : rawHarness;
    if (history && history.schema_version === 1 && history.status === "ok" && Array.isArray(history.samples)) {
      history.samples.forEach(function (s) {
        if (s.harness === historyHarness && (s.group || "") === entryGroup && s.kind === w.kind && valid(s) && s.asOf <= at) upsert(s);
      });
    }
    var current = { harness: historyHarness, group: entryGroup, kind: w.kind, source: (entry && entry.source) || "", resetsAt: reset, asOf: at, percent: w.percent };
    if (valid(current)) upsert(current);
    samples.sort(function (a, b) {
      return a.asOf - b.asOf || (identity(a) < identity(b) ? -1 : identity(a) > identity(b) ? 1 : 0);
    });
    function visible(s) { return (s.resetsAt || 0) === reset && (!knownReset || s.asOf >= start && s.asOf <= reset); }
    function geometry(s) {
      var elapsed = knownReset ? Math.max(0, Math.min(1, (s.asOf - start) / duration)) : null;
      return { asOf: s.asOf, percent: s.percent, elapsed: elapsed, delta: knownReset ? s.percent - elapsed * 100 : null,
        x: knownReset ? 24 + elapsed * 246 : 147, y: 88 - s.percent * 0.76,
        current: identity(s) === identity(current) };
    }
    samples.forEach(function (s, i) {
      if (!visible(s)) return;
      var b = geometry(s); points.push(b);
      var previous = samples[i - 1];
      // Inspect adjacent observations BEFORE selecting a reset/source. Otherwise
      // an intervening identity could disappear and reconnect A -> B -> A.
      if (!knownReset || !previous || !visible(previous) || !s.source || previous.source !== s.source ||
          previous.asOf >= s.asOf || previous.percent > s.percent) return;
      var a = geometry(previous), estimated = s.asOf - previous.asOf > 10 * 60000;
      function segment(left, right) { segments.push({ a: left, b: right, estimated: estimated, above: (left.delta + right.delta) / 2 > 0 }); }
      if (a.delta * b.delta < 0) {
        var fraction = -a.delta / (b.delta - a.delta);
        var cross = geometry({ asOf: a.asOf + (b.asOf - a.asOf) * fraction, percent: a.percent + (b.percent - a.percent) * fraction });
        cross.delta = 0; cross.current = false;
        segment(a, cross); segment(cross, b);
      } else segment(a, b);
    });
    return { knownReset: knownReset, start: knownReset ? start : null, reset: reset, at: at, points: points, segments: segments };
  }
  // Calendar stepping keeps the grid on local midnight even on 23/25-hour days.
  function weeklyAxis(start, reset, locale, measure) {
    var midnight = new Date(start), grid = [], labels = [], right = -Infinity;
    var formatter = new Intl.DateTimeFormat(locale, { weekday: "short" });
    var x = function (at) { return 24 + (at - start) / (reset - start) * 246; };
    midnight.setHours(0, 0, 0, 0);
    while (midnight.getTime() < reset) {
      var day = midnight.getTime();
      midnight.setDate(midnight.getDate() + 1);
      var next = midnight.getTime();
      if (day > start) grid.push(x(day));
      // Use the true day center, never clamp a label to a window edge.
      var center = x(day + (next - day) / 2), text = formatter.format(new Date(day));
      var width = measure(text), left = center - width / 2;
      if (left >= 24 && center + width / 2 <= 270 && left >= right + 3) {
        labels.push({ x: center, text: text }); right = center + width / 2;
      }
    }
    return { grid: grid, labels: labels };
  }
  function decimateXY(xy) {
    var out = [], lastBucket = null;
    xy.forEach(function (p, i) {
      var bucket = Math.round(p.x / 1.5);
      if (out.length && bucket === lastBucket && i < xy.length - 1) out[out.length - 1] = p;
      else out.push(p);
      lastBucket = bucket;
    });
    return out;
  }
  function pathD(xy) {
    return xy.map(function (p, i) { return (i ? "L" : "M") + p.x.toFixed(1) + " " + p.y.toFixed(1); }).join("");
  }
  function separatePins(items, minY, maxY, gap) {
    items.forEach(function (item) { item.ly = item.y; });
    function overlap(a, b) { return a.x < b.x + b.w && b.x < a.x + a.w; }
    for (var it = 0; it < 16; it++) {
      var moved = false;
      items.sort(function (a, b) { return a.ly - b.ly; });
      for (var i = 0; i < items.length; i++) {
        for (var j = i + 1; j < items.length; j++) {
          var a = items[i], b = items[j];
          if (overlap(a, b) && b.ly - a.ly < gap - 0.01) {
            var d = (gap - (b.ly - a.ly)) / 2;
            a.ly -= d; b.ly += d; moved = true;
          }
        }
      }
      items.forEach(function (item) { item.ly = Math.max(minY, Math.min(maxY, item.ly)); });
      if (!moved) break;
    }
  }
  function getAttr(node, name) {
    if (!node) return null;
    if (typeof node.getAttribute === "function") return node.getAttribute(name);
    return node.attributes && Object.prototype.hasOwnProperty.call(node.attributes, name) ? node.attributes[name] : null;
  }
  function getClassStr(node) {
    if (!node) return "";
    if (typeof node.className === "string") return node.className;
    if (node.className && typeof node.className.baseVal === "string") return node.className.baseVal;
    return getAttr(node, "class") || "";
  }
  function hasCls(node, cls) {
    if (!node) return false;
    if (node.classList && typeof node.classList.contains === "function") return node.classList.contains(cls);
    return getClassStr(node).split(/\s+/).indexOf(cls) >= 0;
  }
  function toggleCls(node, cls, on) {
    if (!node) return;
    if (node.classList && typeof node.classList.toggle === "function") {
      node.classList.toggle(cls, !!on);
      return;
    }
    var cur = getClassStr(node).split(/\s+/).filter(Boolean);
    var idx = cur.indexOf(cls);
    if (on && idx < 0) cur.push(cls);
    else if (!on && idx >= 0) cur.splice(idx, 1);
    var joined = cur.join(" ");
    if (typeof node.className === "string") node.className = joined;
    else if (typeof node.setAttribute === "function") node.setAttribute("class", joined);
    if (node.attributes) node.attributes["class"] = joined;
  }
  function queryAll(root, cls) {
    if (!root) return [];
    if (typeof root.querySelectorAll === "function") return Array.prototype.slice.call(root.querySelectorAll("." + cls));
    var out = [];
    (function walk(n) {
      if (!n || !n.children) return;
      for (var i = 0; i < n.children.length; i++) {
        var c = n.children[i];
        if (hasCls(c, cls)) out.push(c);
        walk(c);
      }
    })(root);
    return out;
  }
  var chartSerial = 0;
  window.WsUsage = { sorted: sorted, level: level, age: age, active: active, trend: trend, weeklyAxis: weeklyAxis, mount: function (doc, host, opts) {
    var switcherOn = !!(opts && opts.accountSwitcher);
    var openSettings = opts && typeof opts.openSettings === "function" ? opts.openSettings : null;
    var data = { entries: [], warnPercent: 80, critPercent: 95 }, pending = null, forceQueued = false, popover = null, shown = false, place = function () {}, followUps = [], resetTimer = null;
    var accountData = null, accountPending = null, switchBusy = false, busyHarness = null, busySlotId = null;
    var mode = "progress", focusKey = null, focusLocked = false, previewState = null;
    var banners = {}, bannerTimer = null, rowBannerTimers = {}, swapHarness = null, swapTimer = null;
    var confirmConv = null, sessOpen = false, legendOpen = false, refreshing = false, loadError = false;
    var measureCtx = null, lastFooterBtn = null, lastFooterSig = "";
    var lastChartW = 0, hasResizeObserver = typeof window.ResizeObserver === "function";
    var chartCache = null, rowCache = null, lastAppliedVisSig = null;
    var lastPlacedW = 0, lastPlacedH = 0;
    var hoverRaf = null, pendingHoverTarget = null, lastHoverTarget = null;

    function el(tag, cls, text) {
      var n = doc.createElement(tag); n.className = cls || "";
      if (text !== undefined) n.textContent = text;
      return n;
    }
    function svgNode(tag, attrs, text) {
      var n = doc.createElementNS(SVGNS, tag);
      Object.keys(attrs || {}).forEach(function (key) { n.setAttribute(key, String(attrs[key])); });
      if (text !== undefined) n.textContent = text;
      return n;
    }
    function setVar(node, name, value) {
      if (node.style && typeof node.style.setProperty === "function") node.style.setProperty(name, String(value));
      else if (node.style) node.style[name] = String(value);
    }
    function measureText(text, font) {
      if (!measureCtx && doc.createElement) {
        var c = doc.createElement("canvas");
        measureCtx = c && c.getContext ? c.getContext("2d") : false;
      }
      if (!measureCtx) return Array.from(String(text || "")).length * 6;
      measureCtx.font = font || "9px system-ui, sans-serif";
      return measureCtx.measureText(String(text || "")).width;
    }
    function svgIcon(paths, cls) {
      var s = svgNode("svg", { "class": cls || "up-ic", viewBox: "0 0 24 24", "aria-hidden": "true" });
      paths.forEach(function (d) { s.appendChild(svgNode("path", { d: d })); });
      return s;
    }
    var keys = {
      heading: "js.usage.heading", close: "js.usage.close", legend: "js.usage.legend", refresh: "js.usage.refresh",
      fiveHour: "js.usage.fivehour", weekly: "js.usage.weekly", age: "js.usage.age",
      unavailable: "js.usage.unavailable", signInRequired: "js.usage.signinrequired", disabled: "js.usage.disabled",
      pending: "js.usage.pending", chart: "js.usage.chart", baseline: "js.usage.baseline",
      unknownReset: "js.usage.unknownreset", noRecent5h: "js.usage.norecent5h", noRecentWeekly: "js.usage.norecentweekly",
      lastReading: "js.usage.lastreading", idealTitle: "js.usage.idealtitle",
      nowTitle: "js.usage.nowtitle", now: "js.usage.now", estimatedTitle: "js.usage.estimatedtitle",
      interval: "js.usage.interval", stale: "js.usage.stale",
      resets: "js.usage.resets", unknown: "js.usage.unknown", notLoggedIn: "js.usage.notloggedin",
      ideal: "js.usage.ideal", estimated: "js.usage.estimated",
      key5h: "js.usage.key5h", keyWeekly: "js.usage.keyweekly",
      manageAccounts: "js.usage.manage_accounts", defaultSlot: "js.usage.default_slot",
      activeSessions: "js.usage.active_sessions", queuedSlot: "js.usage.queued_slot",
      switchNow: "js.usage.switch_now", switchBoundary: "js.usage.switch_boundary",
      switchConfirm: "js.usage.switch_confirm", switchedDefault: "js.usage.switched_default",
      switchedDefaultOnly: "js.usage.switched_default_only", switchedQueued: "js.usage.switched_queued",
      switchedNow: "js.usage.switched_now", switchFailed: "js.usage.switch_failed", cancel: "js.usage.cancel",
      singleAccount: "js.usage.single_account", codexOpenai: "js.usage.codex_openai",
      werkOpenai: "js.usage.werk_openai", openrouter: "js.usage.openrouter",
      keyBal: "js.usage.key_bal", keyRem: "js.usage.key_rem", keyDay: "js.usage.key_day", keyTok: "js.usage.key_tok",
      balance: "js.usage.balance", keyLimit: "js.usage.key_limit", spendDaily: "js.usage.spend_daily",
      colCost: "js.usage.col_cost", colTokens: "js.usage.col_tokens",
      localUsage: "js.usage.local_usage", planPaused: "js.usage.plan_paused", keyRejected: "js.usage.key_rejected",
      modeProgress: "js.usage.mode_progress", modeTimeline: "js.usage.mode_timeline",
      subProgress: "js.usage.sub_progress", subTimeline: "js.usage.sub_timeline",
      syncOk: "js.usage.sync_ok", syncBusy: "js.usage.sync_busy", syncErr: "js.usage.sync_err", syncStale: "js.usage.sync_stale",
      group1Title: "js.usage.group1_title", group1Hint: "js.usage.group1_hint",
      group2Title: "js.usage.group2_title", group2Body: "js.usage.group2_body", placeholder: "js.usage.placeholder",
      noCurve: "js.usage.no_curve", noLane: "js.usage.no_lane", noHarnesses: "js.usage.no_harnesses",
      sessQueuedBadge: "js.usage.sess_queued_badge", sessCollapse: "js.usage.sess_collapse",
      sessExpand: "js.usage.sess_expand", sessEmpty: "js.usage.sess_empty",
      slotActiveTip: "js.usage.slot_active_tip", slotSwitchTip: "js.usage.slot_switch_tip",
      queuedShort: "js.usage.queued_short",
      previewAgeMin: "js.usage.preview_age_min", previewAgeHour: "js.usage.preview_age_hour",
      previewAgeDay: "js.usage.preview_age_day", previewNoCache: "js.usage.preview_no_cache",
      previewCachedTag: "js.usage.preview_cached_tag", staleMin: "js.usage.stale_min",
      legendExcess: "js.usage.legend_excess", legendPin: "js.usage.legend_pin",
      legendClock: "js.usage.legend_clock", legendFocus: "js.usage.legend_focus",
      legendGantt: "js.usage.legend_gantt", legendGanttOver: "js.usage.legend_gantt_over",
      legendReset: "js.usage.legend_reset", legendPreview: "js.usage.legend_preview"
    };
    function t(key) { return window.wsT.apply(null, [keys[key]].concat(Array.prototype.slice.call(arguments, 1))); }
    function locale() { return (window.wsI18n && window.wsI18n.lang) || "en"; }
    function windowLabel(kind) { return t(kind === "five_hour" ? "fiveHour" : "weekly"); }
    function noRecentLabel(kind) { return t(kind === "five_hour" ? "noRecent5h" : "noRecentWeekly"); }
    function observedAge(at, now) { return at ? t("age", Math.max(0, Math.floor((now - at) / 60000))) : t("unavailable"); }
    function previewAgeText(at, now) {
      if (!at) return "";
      var mins = Math.max(0, Math.floor((now - at) / 60000));
      if (mins < 60) return t("previewAgeMin", mins);
      var hours = Math.floor(mins / 60);
      if (hours < 24) return t("previewAgeHour", hours);
      return t("previewAgeDay", Math.floor(hours / 24));
    }
    function observedTime(at, now) {
      var d = new Date(at), cur = new Date(now);
      if (d.getFullYear() === cur.getFullYear() && d.getMonth() === cur.getMonth() && d.getDate() === cur.getDate()) {
        return d.toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit" });
      }
      return new Intl.DateTimeFormat(locale(), { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(d);
    }
    function lastReading(w, at, now) {
      return at && Number.isFinite(w.percent) ? t("lastReading", Math.round(w.percent), observedTime(at, now)) : "";
    }
    function statusText(status) {
      if (status === "sign-in required") return t("signInRequired");
      if (status === "key rejected") return t("keyRejected");
      if (!status || status === "usage unavailable") return t("unavailable");
      return status;
    }
    function localizedEntryName(entry) {
      if (entry.harness === "codex_openai") return t("codexOpenai");
      if (entry.harness === "werk_openai") return t("werkOpenai");
      if (entry.harness === "openrouter") return t("openrouter");
      return entryName(entry);
    }
    function icon(entry, small) { var n = el("span", small ? "hb sm" : "hb"); n.setAttribute("data-h", String(entry.harness || "")); return n; }
    // mini is the compact trigger the handle steps down to where the header
    // or bottom bar runs short (rail.js topFit / botWatch).
    function mini() {
      var wrap = el("span", "usage-mini"), svg = svgNode("svg", { viewBox: "0 0 24 24", "aria-hidden": "true" });
      [["path", "M4.5 16.5a8 8 0 1 1 15 0"], ["path", "M12 13.5l3.5-4"], ["circle", ""]].forEach(function (d) {
        var n = svgNode(d[0]);
        if (d[0] === "path") n.setAttribute("d", d[1]); else { n.setAttribute("cx", "12"); n.setAttribute("cy", "14"); n.setAttribute("r", "1.6"); }
        svg.appendChild(n);
      });
      wrap.appendChild(svg);
      return wrap;
    }
    function clock(cls) {
      var svg = svgNode("svg", { "class": cls || "usage-clock", viewBox: "0 0 16 16", "aria-hidden": "true" });
      svg.appendChild(svgNode("circle", { cx: "8", cy: "8", r: "5.8" }));
      svg.appendChild(svgNode("path", { d: "M8 4.8V8l2.2 1.4" }));
      return svg;
    }
    function armReset() {
      if (resetTimer !== null) window.clearTimeout(resetTimer);
      resetTimer = null;
      var now = Date.now(), nearest = 0;
      data.entries.forEach(function (entry) {
        active(entry, now).forEach(function (w) {
          if (w.resetsAt > now && (!nearest || w.resetsAt < nearest)) nearest = w.resetsAt;
        });
      });
      if (!nearest) return;
      resetTimer = window.setTimeout(function () {
        resetTimer = null;
        drawFooter(); drawPopover();
        if (!doc.hidden) refresh(false);
      }, nearest - now + 100);
    }
    function footerSig(items, now) {
      var agyCount = items.filter(function (item) { return item.entry.harness === "agy"; }).length;
      var parts = [locale(), String(items.length), String(agyCount)];
      items.forEach(function (item) {
        var entry = item.entry, enabled = item.enabled;
        parts.push(entry.harness || "", entry.group || "", entry.label || "", entry.status || "");
        if (entry.harness === "openrouter" && entry.openrouter) {
          var or = entry.openrouter;
          parts.push("or", or.balance, or.usageDaily, or.limitRemaining, or.totalCostUsd, or.totalInputTokens, or.totalOutputTokens);
        } else if (entry.harness === "werk_openai" && !enabled.length && entry.werk) {
          var wk = entry.werk;
          parts.push("wk", wk.totalInputTokens, wk.totalOutputTokens, wk.planPaused ? 1 : 0);
        } else {
          ["five_hour", "seven_day"].forEach(function (kind) {
            var w = enabled.filter(function (v) { return v.kind === kind; })[0];
            if (!w) { parts.push(kind, "-"); return; }
            var at = observation(w, entry), old = stale(at, now), noRecent = unreported(at, now);
            var ageMin = at ? Math.max(0, Math.floor((now - at) / 60000)) : -1;
            var delta = noRecent ? "" : pace(w, now);
            parts.push(
              kind,
              (noRecent && !at) ? "—" : Math.round(w.percent),
              old ? 1 : 0,
              noRecent ? 1 : 0,
              delta === null ? "n" : delta,
              ageMin,
              at,
              w.resetsAt || 0
            );
          });
          if (entry.harness === "werk_openai" && entry.werk) parts.push("wkl");
        }
      });
      return parts.map(function (v) { return v === undefined || v === null ? "" : String(v); }).join("\x1f");
    }
    function drawFooter() {
      var button = host.querySelector(".usage-toggle"); if (!button) return false;
      var now = Date.now(), rendered = 0;
      var items = [];
      sorted(data.entries).forEach(function (entry) {
        if (entry.pending) return;
        var windows = active(entry, now), enabled = windows.filter(function (v) { return !v.disabled; });
        if (windows.length && !enabled.length) return;
        items.push({ entry: entry, enabled: enabled });
      });
      var sig = footerSig(items, now);
      if (button === lastFooterBtn && button.children.length > 0 && sig === lastFooterSig) return false;
      lastFooterBtn = button;
      lastFooterSig = sig;
      button.textContent = "";
      button.appendChild(mini());
      var agyCount = items.filter(function (item) { return item.entry.harness === "agy"; }).length;
      items.forEach(function (item) {
        var entry = item.entry, enabled = item.enabled;
        var group = el("span", "usage-harness");
        var mark = el("span", "usage-mark"), columns = el("span", "usage-columns"), hasStale = false;
        var name = localizedEntryName(entry), groupLabel = footerGroupLabel(entry);
        mark.appendChild(icon(entry, true)); group.appendChild(mark);
        if (agyCount >= 2 && groupLabel) {
          group.appendChild(el("span", "usage-group-label", groupLabel));
        }
        if (entry.harness === "openrouter" && entry.openrouter) {
          var or = entry.openrouter, orCol = null;
          if (typeof or.balance === "number") {
            orCol = el("span", "usage-column");
            var orCur = el("span", "usage-current");
            orCur.appendChild(el("span", "usage-key", t("keyBal")));
            orCur.appendChild(el("span", "usage-number", formatUsd(or.balance)));
            orCol.appendChild(orCur);
            if (typeof or.usageDaily === "number") orCol.appendChild(el("span", "usage-pace", formatUsd(or.usageDaily)));
            orCol.title = name + " " + t("balance") + ": " + formatUsd(or.balance);
          } else if (typeof or.limitRemaining === "number") {
            orCol = el("span", "usage-column");
            var remCur = el("span", "usage-current");
            remCur.appendChild(el("span", "usage-key", t("keyRem")));
            remCur.appendChild(el("span", "usage-number", formatUsd(or.limitRemaining)));
            orCol.appendChild(remCur);
            orCol.title = name + " " + t("keyLimit") + ": " + formatUsd(or.limitRemaining);
          } else if (typeof or.usageDaily === "number") {
            orCol = el("span", "usage-column");
            var dayCur = el("span", "usage-current");
            dayCur.appendChild(el("span", "usage-key", t("keyDay")));
            dayCur.appendChild(el("span", "usage-number", formatUsd(or.usageDaily)));
            orCol.appendChild(dayCur);
            orCol.title = name + " " + t("spendDaily") + ": " + formatUsd(or.usageDaily);
          } else if (or.totalCostUsd > 0 || (or.totalInputTokens || 0) + (or.totalOutputTokens || 0) > 0) {
            orCol = el("span", "usage-column");
            var locCur = el("span", "usage-current");
            var orTok = (or.totalInputTokens || 0) + (or.totalOutputTokens || 0);
            locCur.appendChild(el("span", "usage-key", or.totalCostUsd > 0 ? t("colCost") : t("keyTok")));
            locCur.appendChild(el("span", "usage-number", or.totalCostUsd > 0 ? formatUsd(or.totalCostUsd) : formatTokens(orTok)));
            orCol.appendChild(locCur);
            orCol.title = name + " " + (or.totalCostUsd > 0 ? t("colCost") + ": " + formatUsd(or.totalCostUsd) : t("colTokens") + ": " + formatTokens(orTok));
          }
          if (orCol) {
            orCol.setAttribute("aria-label", orCol.title);
            columns.appendChild(orCol);
          }
        } else if (entry.harness === "werk_openai" && !enabled.length && entry.werk) {
          var wk = entry.werk, wkTok = (wk.totalInputTokens || 0) + (wk.totalOutputTokens || 0);
          if (wkTok > 0 || wk.planPaused) {
            var wkCol = el("span", "usage-column");
            var wkCur = el("span", "usage-current");
            wkCur.appendChild(el("span", "usage-key", t("keyTok")));
            wkCur.appendChild(el("span", "usage-number", formatTokens(wkTok)));
            wkCol.appendChild(wkCur);
            if (wk.planPaused) wkCol.appendChild(el("span", "usage-pace over", t("planPaused")));
            wkCol.title = name + ": " + formatTokens(wkTok) + (wk.planPaused ? " (" + t("planPaused") + ")" : "");
            wkCol.setAttribute("aria-label", wkCol.title);
            columns.appendChild(wkCol);
          }
        } else {
          ["five_hour", "seven_day"].forEach(function (kind) {
            var w = enabled.filter(function (v) { return v.kind === kind; })[0];
            if (!w) return;
            var at = observation(w, entry), old = stale(at, now), noRecent = unreported(at, now);
            var column = el("span", "usage-column" + (noRecent ? " usage-stale" : ""));
            var current = el("span", "usage-current"), number;
            if (noRecent && !at) {
              number = el("span", "usage-number", "—");
            } else {
              number = el("span", "usage-number", Math.round(w.percent));
              number.appendChild(el("span", "usage-percent", "%"));
            }
            current.appendChild(el("span", "usage-key", kind === "five_hour" ? t("key5h") : t("keyWeekly")));
            current.appendChild(number);
            column.appendChild(current);
            if (noRecent) {
              var last = lastReading(w, at, now);
              column.title = name + " " + windowLabel(kind) + ": " + noRecentLabel(kind) + (last ? "; " + last + "; " + observedAge(at, now) : "; " + t("unavailable"));
            } else {
              var delta = pace(w, now);
              var comparison = delta === null ? "—" : signedDelta(delta);
              column.appendChild(el("span", "usage-pace" + (delta > 0 ? " over" : delta < 0 ? " under" : ""), comparison));
              column.title = name + " " + label(kind) + ": " + Math.round(w.percent) + "%; " + (delta === null ? "pace unavailable" : "pace " + comparison + " (percentage points)") + "; " + age(at, now);
            }
            column.setAttribute("aria-label", column.title);
            columns.appendChild(column); hasStale = hasStale || old;
          });
        }
        if (hasStale) mark.appendChild(clock());
        group.title = name;
        if (columns.children.length) group.appendChild(columns);
        else group.appendChild(el("span", "usage-empty", entry.harness === "werk_openai" && entry.werk ? t("localUsage") : (entry.status === "sign-in required" ? t("signInRequired") : (entry.status === "key rejected" ? t("keyRejected") : "usage unavailable"))));
        button.appendChild(group); rendered++;
      });
      if (!rendered) button.appendChild(el("span", "usage-empty", "Plan usage"));
      armReset();
      return true;
    }
