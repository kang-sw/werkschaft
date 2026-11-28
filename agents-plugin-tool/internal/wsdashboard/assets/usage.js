/* Account plan usage: a viewer-only cache, the header's usage handle, and its popover. */
(function (window) {
  "use strict";
  function active(entry, now) {
    return (entry.windows || []).filter(function (w) { return !w.resetsAt || w.resetsAt > now; });
  }
  function vendorOrder(entry) {
    if (entry.harness === "claude") return 0;
    if (entry.harness === "codex") return 1;
    if (entry.harness === "agy") {
      if (entry.group === "gemini") return 2;
      if (entry.group === "claude_gpt") return 3;
      return 4;
    }
    return 5;
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
    if (entry.harness === "agy") return entry.label || "Antigravity";
    return entry.label || String(entry.harness || "");
  }
  function footerGroupLabel(entry) {
    if (entry.harness !== "agy" || !entry.label) return "";
    if (entry.group === "gemini") return "Gemini";
    if (entry.group === "claude_gpt") return "Claude/GPT";
    return entry.label;
  }
  function label(kind) { return kind === "five_hour" ? "5-hour" : "Weekly"; }
  function observation(w, entry) { return w.asOf || entry.asOf; }
  function stale(at, now) { return !!at && now - at > 30 * 60000; }
  function unreported(at, now) { return !at || stale(at, now); }
  function pace(w, at) {
    var duration = w.kind === "five_hour" ? 5 * 3600000 : 7 * 86400000;
    if (!w.resetsAt || !at) return null;
    var elapsed = Math.max(0, Math.min(1, (at - (w.resetsAt - duration)) / duration));
    return Math.round(w.percent - elapsed * 100);
  }
  // Pure chart geometry. Crossing points and gap interpolation are visual
  // only: points contains real observations, never an origin or extrapolation.
  function trend(entry, w, history) {
    var duration = w.kind === "five_hour" ? 5 * 3600000 : 7 * 86400000;
    var at = observation(w, entry), reset = w.resetsAt || 0, start = reset - duration;
    var knownReset = reset > 0, samples = [], points = [], segments = [];
    var valid = function (s) { return s.asOf > 0 && Number.isFinite(s.percent) && s.percent >= 0 && s.percent <= 100; };
    var identity = function (s) { return [s.source, s.resetsAt || 0, s.asOf].join("|"); };
    var indices = Object.create(null);
    function upsert(s) {
      var key = identity(s);
      if (indices[key] !== undefined) samples[indices[key]] = s;
      else { indices[key] = samples.length; samples.push(s); }
    }
    var entryGroup = entry.group || "";
    if (history && history.schema_version === 1 && history.status === "ok" && Array.isArray(history.samples)) {
      history.samples.forEach(function (s) {
        if (s.harness === entry.harness && (s.group || "") === entryGroup && s.kind === w.kind && valid(s) && s.asOf <= at) upsert(s);
      });
    }
    var current = { harness: entry.harness, group: entryGroup, kind: w.kind, source: entry.source || "", resetsAt: reset, asOf: at, percent: w.percent };
    if (valid(current)) upsert(current);
    samples.sort(function (a, b) {
      return a.asOf - b.asOf || (identity(a) < identity(b) ? -1 : identity(a) > identity(b) ? 1 : 0);
    });
    function visible(s) { return (s.resetsAt || 0) === reset && (!knownReset || s.asOf >= start && s.asOf <= reset); }
    function geometry(s) {
      var elapsed = knownReset ? Math.max(0, Math.min(1, (s.asOf - start) / duration)) : null;
      return { asOf: s.asOf, percent: s.percent, elapsed: elapsed, delta: knownReset ? s.percent - elapsed * 100 : null,
        x: knownReset ? 24 + elapsed * 246 : 147, y: 88 - s.percent * .76,
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
  var chartSerial = 0;
  window.WsUsage = { sorted: sorted, level: level, age: age, active: active, trend: trend, weeklyAxis: weeklyAxis, mount: function (doc, host, opts) {
    var switcherOn = !!(opts && opts.accountSwitcher);
    var openSettings = opts && typeof opts.openSettings === "function" ? opts.openSettings : null;
    var data = { entries: [], warnPercent: 80, critPercent: 95 }, pending = null, forceQueued = false, popover = null, shown = false, place = function () {}, followUps = [], resetTimer = null;
    var accountData = null, accountPending = null, switchBusy = false, switchNote = null, guideConv = null;
    function el(tag, cls, text) {
      var n = doc.createElement(tag); n.className = cls || "";
      if (text !== undefined) n.textContent = text;
      return n;
    }
    function setVar(node, name, value) {
      if (node.style && typeof node.style.setProperty === "function") node.style.setProperty(name, String(value));
      else if (node.style) node.style[name] = String(value);
    }
    var keys = {
      heading: "js.usage.heading", close: "js.usage.close", legend: "js.usage.legend",
      fiveHour: "js.usage.fivehour", weekly: "js.usage.weekly", age: "js.usage.age",
      unavailable: "js.usage.unavailable", signInRequired: "js.usage.signinrequired", disabled: "js.usage.disabled",
      pending: "js.usage.pending",
      noFiveHour: "js.usage.no5h", noWeekly: "js.usage.noweekly", modelGroups: "js.usage.modelgroups",
      chart: "js.usage.chart", baseline: "js.usage.baseline",
      unknownReset: "js.usage.unknownreset", noRecent5h: "js.usage.norecent5h",
      noRecentWeekly: "js.usage.norecentweekly", lastReading: "js.usage.lastreading",
      idealTitle: "js.usage.idealtitle",
      nowTitle: "js.usage.nowtitle", now: "js.usage.now", estimatedTitle: "js.usage.estimatedtitle",
      interval: "js.usage.interval", current: "js.usage.current", stale: "js.usage.stale",
      resets: "js.usage.resets", unknown: "js.usage.unknown", card: "js.usage.card",
      historyUnavailable: "js.usage.historyunavailable", historyEmpty: "js.usage.historyempty",
      historyRecorded: "js.usage.historyrecorded", historyEarlier: "js.usage.historyearlier",
      currentOnly: "js.usage.currentonly", noObservation: "js.usage.noobservation", notLoggedIn: "js.usage.notloggedin",
      observed: "js.usage.observed", ideal: "js.usage.ideal", latest: "js.usage.latest", estimated: "js.usage.estimated",
      key5h: "js.usage.key5h", keyWeekly: "js.usage.keyweekly",
      accounts: "js.usage.accounts", manageAccounts: "js.usage.manage_accounts", defaultSlot: "js.usage.default_slot",
      activeSessions: "js.usage.active_sessions", queuedSlot: "js.usage.queued_slot",
      switchNow: "js.usage.switch_now", switchBoundary: "js.usage.switch_boundary",
      switchConfirm: "js.usage.switch_confirm", switchedImmediate: "js.usage.switched_immediate",
      switchedDefault: "js.usage.switched_default", switchedDefaultOnly: "js.usage.switched_default_only",
      switchedQueued: "js.usage.switched_queued", switchedNow: "js.usage.switched_now",
      switchFailed: "js.usage.switch_failed", cancel: "js.usage.cancel", werkOpenai: "js.usage.werk_openai"
    };
    function t(key) { return window.wsT.apply(null, [keys[key]].concat(Array.prototype.slice.call(arguments, 1))); }
    function locale() { return window.wsI18n.lang; }
    function windowLabel(kind) { return t(kind === "five_hour" ? "fiveHour" : "weekly"); }
    function noRecentLabel(kind) { return t(kind === "five_hour" ? "noRecent5h" : "noRecentWeekly"); }
    function observedAge(at, now) { return at ? t("age", Math.max(0, Math.floor((now - at) / 60000))) : t("unavailable"); }
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
      if (!status || status === "usage unavailable") return t("unavailable");
      return status;
    }
    function icon(entry, small) { var n = el("span", small ? "hb sm" : "hb"); n.setAttribute("data-h", String(entry.harness || "")); return n; }
    // mini is the compact trigger the handle steps down to where the header
    // runs short (rail.js topFit): a gauge, the handle's label its tooltip.
    function mini() {
      var wrap = el("span", "usage-mini"), svg = doc.createElementNS("http://www.w3.org/2000/svg", "svg");
      svg.setAttribute("viewBox", "0 0 24 24"); svg.setAttribute("aria-hidden", "true");
      [["path", "M4.5 16.5a8 8 0 1 1 15 0"], ["path", "M12 13.5l3.5-4"], ["circle", ""]].forEach(function (d) {
        var n = doc.createElementNS("http://www.w3.org/2000/svg", d[0]);
        if (d[0] === "path") n.setAttribute("d", d[1]); else { n.setAttribute("cx", "12"); n.setAttribute("cy", "14"); n.setAttribute("r", "1.6"); }
        svg.appendChild(n);
      });
      wrap.appendChild(svg);
      return wrap;
    }
    function clock() {
      var svg = doc.createElementNS("http://www.w3.org/2000/svg", "svg");
      svg.setAttribute("class", "usage-clock"); svg.setAttribute("viewBox", "0 0 16 16"); svg.setAttribute("aria-hidden", "true");
      var circle = doc.createElementNS("http://www.w3.org/2000/svg", "circle");
      circle.setAttribute("cx", "8"); circle.setAttribute("cy", "8"); circle.setAttribute("r", "5.8"); svg.appendChild(circle);
      var hand = doc.createElementNS("http://www.w3.org/2000/svg", "path");
      hand.setAttribute("d", "M8 4.8V8l2.2 1.4"); svg.appendChild(hand);
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
    function drawFooter() {
      var button = host.querySelector(".usage-toggle"); if (!button) return;
      var now = Date.now(), rendered = 0;
      button.textContent = "";
      button.appendChild(mini());
      var items = [];
      sorted(data.entries).forEach(function (entry) {
        if (entry.pending) return;
        var windows = active(entry, now), enabled = windows.filter(function (v) { return !v.disabled; });
        if (windows.length && !enabled.length) return;
        items.push({ entry: entry, enabled: enabled });
      });
      var agyCount = items.filter(function (item) { return item.entry.harness === "agy"; }).length;
      items.forEach(function (item) {
        var entry = item.entry, enabled = item.enabled;
        var group = el("span", "usage-harness");
        var mark = el("span", "usage-mark"), columns = el("span", "usage-columns"), hasStale = false;
        var name = entryName(entry), groupLabel = footerGroupLabel(entry);
        mark.appendChild(icon(entry, true)); group.appendChild(mark);
        if (agyCount >= 2 && groupLabel) {
          group.appendChild(el("span", "usage-group-label", groupLabel));
        }
        ["five_hour", "seven_day"].forEach(function (kind) {
          var w = enabled.filter(function (v) { return v.kind === kind; })[0];
          if (!w) return;
          var at = observation(w, entry), old = stale(at, now), noRecent = unreported(at, now);
          var column = el("span", "usage-column" + (noRecent ? " usage-stale" : ""));
          var current = el("span", "usage-current"), number;
          if (noRecent) {
            if (at) {
              number = el("span", "usage-number", Math.round(w.percent));
              number.appendChild(el("span", "usage-percent", "%"));
            } else {
              number = el("span", "usage-number", "—");
            }
          } else {
            number = el("span", "usage-number", Math.round(w.percent));
            number.appendChild(el("span", "usage-percent", "%"));
          }
          current.appendChild(el("span", "usage-key", kind === "five_hour" ? t("key5h") : t("keyWeekly"))); current.appendChild(number);
          column.appendChild(current);
          if (noRecent) {
            var last = lastReading(w, at, now);
            column.title = name + " " + windowLabel(kind) + ": " + noRecentLabel(kind) + (last ? "; " + last + "; " + observedAge(at, now) : "; " + t("unavailable"));
          } else {
            var delta = pace(w, now);
            var comparison = delta === null ? "—" : (delta > 0 ? "+" : delta < 0 ? "\u2212" : "") + Math.abs(delta) + "%";
            column.appendChild(el("span", "usage-pace" + (delta > 0 ? " over" : delta < 0 ? " under" : ""), comparison));
            column.title = name + " " + label(kind) + ": " + Math.round(w.percent) + "%; " + (delta === null ? "pace unavailable" : "pace " + comparison + " (percentage points)") + "; " + age(at, now);
          }
          column.setAttribute("aria-label", column.title);
          columns.appendChild(column); hasStale = hasStale || old;
        });
        if (hasStale) mark.appendChild(clock());
        group.title = name;
        if (columns.children.length) group.appendChild(columns);
        else group.appendChild(el("span", "usage-empty", entry.status === "sign-in required" ? t("signInRequired") : "usage unavailable"));
        button.appendChild(group); rendered++;
      });
      if (!rendered) button.appendChild(el("span", "usage-empty", "Plan usage"));
      armReset();
    }
    function svgNode(tag, attrs, text) {
      var n = doc.createElementNS("http://www.w3.org/2000/svg", tag);
      Object.keys(attrs || {}).forEach(function (key) { n.setAttribute(key, String(attrs[key])); });
      if (text !== undefined) n.textContent = text;
      return n;
    }
    function tint(delta) {
      if (delta === null || delta === 0) return "var(--wb-muted-fg)";
      return "color-mix(in srgb, var(--wb-muted-fg), var(" + (delta > 0 ? "--wb-fail" : "--wb-ok") + ") " + Math.min(100, Math.abs(delta)) + "%)";
    }
    function entryHistory(entry, kind) {
      if (entry.harness === "agy") return data.agyHistory || data.history;
      if (entry.harness === "codex" && kind === "five_hour") return data.codexFiveHourHistory || data.history;
      return data.history;
    }
    function drawChart(entry, w, old, noRecent, now) {
      var geometry = trend(entry, w, entryHistory(entry, w.kind)), id = "usage-chart-" + (++chartSerial);
      var chart = svgNode("svg", { "class": "usage-chart", viewBox: "0 0 288 113", role: "img", "aria-label": t("chart", windowLabel(w.kind), t(geometry.knownReset ? "baseline" : "unknownReset")) });
      var chartTitle;
      if (noRecent) {
        var last = lastReading(w, geometry.at, now);
        chartTitle = windowLabel(w.kind) + ": " + noRecentLabel(w.kind) + (last ? "; " + last + "; " + observedAge(geometry.at, now) : "; " + t("unavailable"));
      } else {
        chartTitle = windowLabel(w.kind) + ": " + Math.round(w.percent) + "%; " + observedAge(geometry.at, now);
      }
      chart.appendChild(svgNode("title", {}, chartTitle));
      var axis = null;
      if (geometry.knownReset && w.kind === "seven_day") {
        var canvas = doc.createElement("canvas"), context = canvas.getContext && canvas.getContext("2d");
        if (context) {
          var font = window.getComputedStyle(host).getPropertyValue("--wb-sans").trim() || "sans-serif";
          context.font = "9px " + font;
        }
        axis = weeklyAxis(geometry.start, geometry.reset, locale(), function (text) {
          return context ? context.measureText(text).width : Array.from(text).length * 9;
        });
        axis.grid.forEach(function (x) { chart.appendChild(svgNode("line", { "class": "usage-day-grid", x1: x, x2: x, y1: 12, y2: 88 })); });
      }
      [12, 50, 88].forEach(function (y) { chart.appendChild(svgNode("line", { "class": "usage-grid", x1: 24, x2: 270, y1: y, y2: y })); });
      chart.appendChild(svgNode("text", { x: 0, y: 15 }, "100"));
      chart.appendChild(svgNode("text", { x: 5, y: 91 }, "0"));
      function timeLabel(at) {
        var date = new Date(at);
        return w.kind === "five_hour" ? date.toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit" }) : new Intl.DateTimeFormat(locale(), { weekday: "short" }).format(date);
      }
      if (geometry.knownReset) {
        var ideal = svgNode("path", { "class": "usage-ideal", d: "M24 88L270 12", "stroke-dasharray": "4 4" });
        ideal.appendChild(svgNode("title", {}, t("idealTitle"))); chart.appendChild(ideal);
        if (axis) {
          axis.labels.forEach(function (label) { chart.appendChild(svgNode("text", { "class": "usage-day-label", x: label.x, y: 106, "text-anchor": "middle" }, label.text)); });
        } else {
          chart.appendChild(svgNode("text", { x: 24, y: 106 }, timeLabel(geometry.start)));
          chart.appendChild(svgNode("text", { x: 270, y: 106, "text-anchor": "end" }, timeLabel(geometry.reset)));
        }
        // Wall-clock context only; never extend the observed series to now.
        if (now >= geometry.start && now <= geometry.reset) {
          var nowX = 24 + (now - geometry.start) / (geometry.reset - geometry.start) * 246;
          var guide = svgNode("line", { "class": "usage-now", x1: nowX, x2: nowX, y1: 12, y2: 88, "stroke-dasharray": "2 3" });
          guide.appendChild(svgNode("title", {}, t("nowTitle"))); chart.appendChild(guide);
          chart.appendChild(svgNode("text", { "class": "usage-now-label", x: nowX, y: 10, "text-anchor": "middle" }, t("now")));
        }
      }
      // One continuous line per run of observations; a long gap breaks the
      // run into a dashed bridge with no area. Dense runs are decimated to one
      // point per ~1.5px column (usage is cumulative, so the last point wins).
      var gapLimit = (w.kind === "five_hour" ? 30 * 60000 : 12 * 3600000);
      var runs = [], run = null;
      geometry.segments.forEach(function (s) {
        if (s.b.asOf - s.a.asOf > gapLimit) {
          run = null; runs.push({ gap: true, pts: [s.a, s.b] });
        } else if (run && run.pts[run.pts.length - 1] === s.a) run.pts.push(s.b);
        else { run = { gap: false, pts: [s.a, s.b] }; runs.push(run); }
      });
      function decimate(pts) {
        var out = [], lastBucket = null;
        pts.forEach(function (p, i) {
          var bucket = Math.round(p.x / 1.5);
          if (out.length && bucket === lastBucket && i < pts.length - 1) out[out.length - 1] = p; else out.push(p);
          lastBucket = bucket;
        });
        return out;
      }
      function d(pts) { return pts.map(function (p, i) { return (i ? "L" : "M") + p.x.toFixed(1) + " " + p.y.toFixed(1); }).join(""); }
      var clipId = id + "-above", defs = svgNode("defs"), clip = svgNode("clipPath", { id: clipId });
      clip.appendChild(svgNode("polygon", { points: "24,88 270,12 270,0 24,0" })); defs.appendChild(clip); chart.appendChild(defs);
      runs.forEach(function (r) {
        var pts = r.gap ? r.pts : decimate(r.pts), last = pts[pts.length - 1];
        var color = old && last.current ? "var(--wb-muted-fg)" : tint(last.delta);
        if (!r.gap) chart.appendChild(svgNode("path", { "class": "usage-area", d: d(pts) + "L" + last.x.toFixed(1) + " 88L" + pts[0].x.toFixed(1) + " 88Z", fill: color }));
        if (!r.gap && geometry.knownReset) chart.appendChild(svgNode("path", { "class": "usage-excess", d: d(pts) + "L" + last.x.toFixed(1) + " 88L" + pts[0].x.toFixed(1) + " 88Z", fill: "var(--wb-fail)", "clip-path": "url(#" + clipId + ")" }));
        var line = svgNode("path", { "class": "usage-actual", d: d(pts), stroke: color, "stroke-width": 2 });
        if (r.gap) line.setAttribute("stroke-dasharray", "4 3");
        line.appendChild(svgNode("title", {}, t(r.gap ? "estimatedTitle" : "interval"))); chart.appendChild(line);
      });
      geometry.points.forEach(function (p) {
        if (!p.current) return;
        var point = svgNode("circle", { "class": "usage-point usage-point-current", cx: p.x, cy: p.y, r: 4, fill: old ? "var(--wb-muted-fg)" : tint(p.delta) });
        point.appendChild(svgNode("title", {}, new Date(p.asOf).toLocaleString(locale()) + ": " + p.percent + "%; " + t("current") + (old ? "; " + t("stale") : ""))); chart.appendChild(point);
      });
      return { el: chart, geometry: geometry };
    }
    function shortReset(w) {
      if (!w.resetsAt) return t("unknown");
      var date = new Date(w.resetsAt);
      if (w.kind === "five_hour") return date.toLocaleTimeString(locale(), { hour: "numeric", minute: "2-digit" });
      return new Intl.DateTimeFormat(locale(), { weekday: "short", hour: "numeric", minute: "2-digit" }).format(date);
    }
    function emptySlot(kind) {
      var slot = el("div", "usage-window-empty", t(kind === "five_hour" ? "noFiveHour" : "noWeekly"));
      slot.setAttribute("data-kind", kind);
      return slot;
    }
    function pendingSlot(kind) {
      var slot = el("article", "usage-window-card usage-pending-card");
      slot.setAttribute("data-kind", kind);
      slot.setAttribute("aria-label", t("pending"));
      slot.setAttribute("aria-busy", "true");
      var spinner = el("span", "spin");
      spinner.setAttribute("aria-hidden", "true");
      slot.appendChild(spinner);
      return slot;
    }
    function windowCard(entry, w, now) {
      var at = observation(w, entry), old = stale(at, now), noRecent = unreported(at, now), isDisabled = !!w.disabled;
      var delta = noRecent ? null : pace(w, now);
      var comparison = delta === null ? "—" : (delta > 0 ? "+" : delta < 0 ? "−" : "") + Math.abs(delta) + "%";
      var card = el("article", "usage-window-card" + (noRecent ? " usage-stale" : "") + (isDisabled ? " usage-disabled" : ""));
      card.setAttribute("data-kind", w.kind);
      var head = el("div", "usage-window");
      head.appendChild(el("span", "usage-window-name", windowLabel(w.kind)));
      head.appendChild(el("b", "usage-number", noRecent ? "—" : Math.round(w.percent) + "%"));
      if (!noRecent) head.appendChild(el("span", "usage-pace" + (delta > 0 ? " over" : delta < 0 ? " under" : ""), "(" + comparison + ")"));
      if (isDisabled) head.appendChild(el("span", "usage-disabled-tag", t("disabled")));
      if (old) head.appendChild(clock());
      var fullReset = t("resets", w.resetsAt ? new Date(w.resetsAt).toLocaleString(locale()) : t("unknown"));
      var resetEl = el("span", "usage-reset", "\u21bb " + shortReset(w));
      resetEl.title = fullReset;
      head.appendChild(resetEl);
      card.appendChild(head);
      var last = noRecent ? lastReading(w, at, now) : "";
      if (noRecent) card.appendChild(el("div", "usage-no-report", noRecentLabel(w.kind)));
      var chart = drawChart(entry, w, old, noRecent, now); card.appendChild(chart.el);
      if (noRecent) {
        var detail = last ? last + "; " + observedAge(at, now) : t("unavailable");
        card.setAttribute("aria-label", windowLabel(w.kind) + ": " + noRecentLabel(w.kind) + "; " + detail + (isDisabled ? "; " + t("disabled") : ""));
      } else {
        card.setAttribute("aria-label", t("card", windowLabel(w.kind), Math.round(w.percent), comparison, observedAge(at, now)) + (isDisabled ? "; " + t("disabled") : ""));
      }
      var history = entryHistory(entry, w.kind), note;
      if (!history || history.schema_version !== 1 || history.status === "unavailable") note = t("historyUnavailable");
      else if (history.status === "empty") note = t("historyEmpty");
      else note = t(chart.geometry.points.length > 1 ? "historyRecorded" : "historyEarlier");
      if (chart.geometry.points.length <= 1) note += "; " + t(chart.geometry.points.length ? "currentOnly" : "noObservation");
      if (!chart.geometry.knownReset) note += "; " + t("unknownReset");
      if (noRecent) note = (last ? last + "; " + observedAge(at, now) : noRecentLabel(w.kind)) + "; " + note;
      card.title = note;
      return card;
    }
    function drawPopover() {
      if (!popover || !shown) return;
      var list = popover.querySelector(".usage-details"); list.textContent = "";
      var now = Date.now(), entries = sorted(data.entries);
      popover.setAttribute("data-cols", String(entries.length));
      if (!entries.length) {
        list.appendChild(el("p", "usage-status", t("notLoggedIn")));
        place();
        return;
      }
      var agyIdx = [];
      entries.forEach(function (entry, idx) {
        if (entry.harness === "agy" && entry.group) agyIdx.push(idx);
      });
      var hasBracket = agyIdx.length >= 2 && agyIdx[agyIdx.length - 1] - agyIdx[0] === agyIdx.length - 1;
      var agyStart = hasBracket ? agyIdx[0] : -1, agyEnd = hasBracket ? agyIdx[agyIdx.length - 1] : -1;

      var grid = el("div", "usage-layout" + (hasBracket ? " usage-has-bracket" : ""));
      setVar(grid, "--usage-cols", entries.length);

      [["five_hour", "5 h"], ["seven_day", "7 d"]].forEach(function (item) {
        var lab = el("div", "usage-row-label");
        lab.setAttribute("data-kind", item[0]);
        lab.appendChild(el("span", "", windowLabel(item[0])));
        lab.appendChild(el("small", "", item[1]));
        grid.appendChild(lab);
      });
      ["five_hour", "seven_day"].forEach(function (kind) {
        var colh = el("div", "usage-col-head", windowLabel(kind));
        colh.setAttribute("data-kind", kind);
        grid.appendChild(colh);
      });

      var cRow = 2, cnRow = 2;
      entries.forEach(function (entry, idx) {
        var inBracket = hasBracket && idx >= agyStart && idx <= agyEnd;
        if (hasBracket && idx === agyStart) {
          var bracket = el("div", "usage-vendor-bracket");
          setVar(bracket, "--b-col", agyStart + 2);
          setVar(bracket, "--b-span", agyIdx.length);
          setVar(bracket, "--c-row", cRow);
          setVar(bracket, "--cn-row", cnRow);
          bracket.appendChild(icon(entry));
          bracket.appendChild(el("b", "", "Antigravity"));
          bracket.appendChild(el("span", "usage-vendor-sub", "\u00b7 " + t("modelGroups", agyIdx.length)));
          grid.appendChild(bracket);
          cRow++; cnRow++;
        } else if (idx > 0) {
          var sep = el("div", "usage-c-sep");
          setVar(sep, "--c-row", cRow);
          setVar(sep, "--cn-row", cnRow);
          grid.appendChild(sep);
          cRow++; cnRow++;
        }

        var row = el("section", "usage-account" + (inBracket ? " usage-in-bracket" : ""));
        setVar(row, "--col", idx + 2);
        setVar(row, "--c-row", cRow);
        setVar(row, "--cn-row", cnRow);
        setVar(row, "--cn-cards-row", cnRow + 1);

        var windows = active(entry, now);
        var w5 = windows.filter(function (v) { return v.kind === "five_hour"; })[0] || null;
        var w7 = windows.filter(function (v) { return v.kind === "seven_day"; })[0] || null;
        var reported = windows.filter(function (w) { return !unreported(observation(w, entry), now); });
        var ageWindows = reported.length ? reported : windows;
        var groupAt = ageWindows.reduce(function (oldest, w) {
          var at = observation(w, entry);
          return at && (!oldest || at < oldest) ? at : oldest;
        }, 0);

        var vhead = el("div", "usage-vhead" + (inBracket ? "" : " usage-vhead-tall"));
        var head = el("div", "usage-heading");
        head.appendChild(icon(entry));
        head.appendChild(el("b", "", entryName(entry)));
        vhead.appendChild(head);
        var vmeta = el("div", "usage-vmeta");
        if (entry.plan) vmeta.appendChild(el("span", "usage-plan", entry.plan));
        if (entry.plan && groupAt) vmeta.appendChild(el("span", "usage-vdot", "\u00b7"));
        if (groupAt) {
          var ageEl = el("span", "usage-vage", shortAge(groupAt, now));
          ageEl.title = observedAge(groupAt, now);
          vmeta.appendChild(ageEl);
        }
        if (vmeta.children.length) vhead.appendChild(vmeta);
        row.appendChild(vhead);

        cRow++;
        cnRow += 2;

        if (entry.pending) {
          var pending5 = pendingSlot("five_hour"); pending5.className += " usage-slot-5h";
          var pending7 = pendingSlot("seven_day"); pending7.className += " usage-slot-7d";
          row.appendChild(pending5); row.appendChild(pending7);
        } else if (!w5 && !w7) {
          var statusSlot = el("div", "usage-status-slot");
          statusSlot.appendChild(el("p", "usage-status", statusText(entry.status)));
          if (entry.asOf) statusSlot.appendChild(el("div", "usage-meta", observedAge(entry.asOf, now)));
          row.appendChild(statusSlot);
        } else {
          var slot5 = w5 ? windowCard(entry, w5, now) : emptySlot("five_hour");
          slot5.className += " usage-slot-5h";
          var slot7 = w7 ? windowCard(entry, w7, now) : emptySlot("seven_day");
          slot7.className += " usage-slot-7d";
          row.appendChild(slot5);
          row.appendChild(slot7);
          if (entry.status) {
            var note = el("p", "usage-status usage-status-note", statusText(entry.status));
            setVar(note, "--c-status-row", cRow);
            setVar(note, "--cn-status-row", cnRow);
            row.appendChild(note);
            cRow++; cnRow++;
          }
        }
        grid.appendChild(row);
      });
      list.appendChild(grid);
      place();
      armReset();
    }
    function refresh(force) {
      if (pending) {
        if (force) forceQueued = true;
        return pending;
      }
      forceQueued = false;
      pending = window.fetch("/rail/usage?history=1" + (force ? "&refresh=1" : ""), { credentials: "same-origin", cache: "no-store", headers: force ? { "X-WS-Usage-Refresh": "1" } : {} }).then(function (r) {
        if (!r.ok) throw new Error("usage unavailable");
        return r.json();
      }).then(function (next) {
        // The handle sits in every header now, so a reply without the
        // account list (a page whose daemon reports none) reads as empty.
        data = next && Array.isArray(next.entries) ? next : { entries: [], warnPercent: 80, critPercent: 95 };
        drawFooter(); drawPopover();
        if (force) followUp();
      }).catch(function () {
        data.entries.forEach(function (entry) { entry.status = "usage unavailable"; });
        drawPopover();
        if (popover && shown && !data.entries.length) popover.querySelector(".usage-details").textContent = t("unavailable");
      }).then(function () {
        pending = null;
        // Opening during an ordinary poll still needs to start a probe. An
        // already-forced request satisfies every open that joined it.
        if (forceQueued && !force) return refresh(true);
      });
      return pending;
    }
    // A forced refresh answers from the daemon's cache while background probes
    // run (Codex HTTP up to 10 s, agy CLI up to 30 s), so refetch cache-only
    // while the popover stays open and clear every timer on close.
    function clearFollowUps() {
      followUps.forEach(function (id) { window.clearTimeout(id); });
      followUps = [];
    }
    function followUp() {
      clearFollowUps();
      if (!shown) return;
      followUps = [2000, 11000, 32000].map(function (ms) {
        return window.setTimeout(function () { if (shown && !doc.hidden) refresh(false); }, ms);
      });
    }
    function fetchAccountStatus() {
      if (!switcherOn) return Promise.resolve();
      if (accountPending) return accountPending;
      accountPending = window.fetch("/api/account/status", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
        if (!r.ok) return null;
        return r.json();
      }).then(function (next) {
        accountData = next && typeof next === "object" && typeof next.loopback_peer === "boolean" ? next : null;
        drawAccounts();
      }).catch(function () {
        accountData = null;
        drawAccounts();
      }).then(function () {
        accountPending = null;
      });
      return accountPending;
    }
    function slotDisplayLabel(harness, slotId) {
      if (!slotId || slotId === "default") return t("defaultSlot");
      var list = accountData && accountData.slots && accountData.slots[harness];
      if (Array.isArray(list)) {
        for (var i = 0; i < list.length; i++) {
          if (list[i].id === slotId) return list[i].label || list[i].id;
        }
      }
      return slotId;
    }
    function postAccountSwitch(path, body, onSuccess) {
      if (switchBusy) return;
      switchBusy = true;
      switchNote = null;
      drawAccounts();
      window.fetch("/api/account/" + path, {
        method: "POST",
        credentials: "same-origin",
        cache: "no-store",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body)
      }).then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (res) {
          if (!r.ok) throw new Error(res.error || "switch_failed");
          return res;
        });
      }).then(function (res) {
        guideConv = null;
        switchNote = { text: onSuccess(res), tone: "ok" };
        return Promise.all([fetchAccountStatus(), refresh(false)]);
      }).catch(function () {
        switchNote = { text: t("switchFailed"), tone: "fail" };
      }).then(function () {
        switchBusy = false;
        drawAccounts();
      });
    }
    function drawAccounts() {
      if (!switcherOn || !popover || !shown) return;
      var box = popover.querySelector(".usage-accounts");
      if (!box) return;
      box.textContent = "";
      var head = el("div", "usage-accounts-head");
      head.appendChild(el("span", "usage-accounts-title", t("accounts")));
      var manage = el("a", "usage-manage-link", t("manageAccounts"));
      manage.href = "/settings?section=account";
      if (openSettings) {
        manage.addEventListener("click", function (e) {
          if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button) return;
          e.preventDefault();
          openSettings("/settings?section=account", manage);
        });
      }
      head.appendChild(manage);
      box.appendChild(head);
      if (!accountData) { place(); return; }

      var oaList = Array.isArray(accountData.openai_accounts) ? accountData.openai_accounts : [];
      if (oaList.length) {
        var oaRow = el("div", "usage-slot-row");
        oaRow.setAttribute("data-slot-harness", "werk");
        var oaLabel = el("span", "usage-slot-harness");
        oaLabel.appendChild(icon({ harness: "werk" }, true));
        oaLabel.appendChild(el("span", "", t("werkOpenai")));
        oaRow.appendChild(oaLabel);
        var oaChips = el("div", "usage-slot-chips");
        oaList.forEach(function (acc) {
          var chip = el("button", "usage-slot-chip", acc.email || acc.client_id);
          chip.type = "button";
          chip.setAttribute("aria-pressed", String(!!acc.active));
          chip.disabled = switchBusy || (!acc.active && (!acc.signed_in || !acc.plan_enabled));
          chip.addEventListener("click", function () {
            if (acc.active || chip.disabled) return;
            postAccountSwitch("openai/switch", { client_id: acc.client_id }, function () {
              return t("switchedImmediate");
            });
          });
          oaChips.appendChild(chip);
        });
        oaRow.appendChild(oaChips);
        box.appendChild(oaRow);
      }

      var slotsMap = accountData.slots && typeof accountData.slots === "object" ? accountData.slots : {};
      var harnesses = Object.keys(slotsMap).sort();
      harnesses.forEach(function (harness) {
        var list = Array.isArray(slotsMap[harness]) ? slotsMap[harness] : [];
        if (!list.length) return;
        var row = el("div", "usage-slot-row");
        row.setAttribute("data-slot-harness", harness);
        var hLabel = el("span", "usage-slot-harness");
        hLabel.appendChild(icon({ harness: harness }, true));
        hLabel.appendChild(el("span", "", entryName({ harness: harness })));
        row.appendChild(hLabel);
        var chips = el("div", "usage-slot-chips");
        list.forEach(function (slot) {
          var labelText = slot.id === "default" ? t("defaultSlot") : (slot.label || slot.id);
          var chip = el("button", "usage-slot-chip", labelText);
          chip.type = "button";
          chip.setAttribute("data-slot-id", slot.id);
          chip.setAttribute("aria-pressed", String(!!slot.active));
          chip.disabled = switchBusy;
          chip.addEventListener("click", function () {
            if (slot.active || chip.disabled) return;
            postAccountSwitch("slots/switch", { harness: harness, slot_id: slot.id }, function (res) {
              return res && res.queued_count > 0 ? t("switchedDefault", res.queued_count) : t("switchedDefaultOnly");
            });
          });
          chips.appendChild(chip);
        });
        row.appendChild(chips);
        box.appendChild(row);
      });

      var convs = Array.isArray(accountData.conversations) ? accountData.conversations.filter(function (c) {
        var list = slotsMap[c.harness];
        return Array.isArray(list) && (list.length > 1 || !!c.pending_account_slot_id);
      }) : [];
      if (convs.length) {
        var cList = el("div", "usage-conv-list");
        cList.appendChild(el("div", "usage-conv-label", t("activeSessions")));
        convs.forEach(function (conv) {
          var cRow = el("div", "usage-conv-row");
          cRow.setAttribute("data-conv-id", conv.id);
          var titleText = conv.title || conv.id;
          var titleEl = el("span", "usage-conv-title", titleText);
          titleEl.title = titleText;
          cRow.appendChild(titleEl);
          cRow.appendChild(el("span", "usage-conv-badge", slotDisplayLabel(conv.harness, conv.account_slot_id)));
          if (conv.pending_account_slot_id) {
            cRow.appendChild(el("span", "usage-conv-badge queued", t("queuedSlot", slotDisplayLabel(conv.harness, conv.pending_account_slot_id))));
          }
          var actions = el("div", "usage-conv-actions");
          if (conv.pending_account_slot_id) {
            var applyNow = el("button", "usage-act-btn pri", t("switchNow"));
            applyNow.type = "button";
            applyNow.disabled = switchBusy;
            applyNow.addEventListener("click", function () {
              postAccountSwitch("slots/switch", { conversation_id: conv.id, slot_id: conv.pending_account_slot_id, mode: "now" }, function () {
                return t("switchedNow");
              });
            });
            actions.appendChild(applyNow);
          } else {
            (slotsMap[conv.harness] || []).forEach(function (slot) {
              var curSlot = conv.account_slot_id || "default";
              if (slot.id === curSlot) return;
              var slotLabel = slot.id === "default" ? t("defaultSlot") : (slot.label || slot.id);
              var pick = el("button", "usage-act-btn", slotLabel);
              pick.type = "button";
              pick.disabled = switchBusy;
              pick.addEventListener("click", function () {
                guideConv = { convId: conv.id, harness: conv.harness, slotId: slot.id, slotLabel: slotLabel, convTitle: titleText };
                drawAccounts();
              });
              actions.appendChild(pick);
            });
          }
          cRow.appendChild(actions);
          if (guideConv && guideConv.convId === conv.id) {
            var guide = el("div", "usage-conv-guide");
            guide.appendChild(el("span", "usage-conv-guide-text", t("switchConfirm", guideConv.slotLabel, guideConv.convTitle)));
            var nowBtn = el("button", "usage-act-btn pri", t("switchNow"));
            nowBtn.type = "button";
            nowBtn.disabled = switchBusy;
            nowBtn.addEventListener("click", function () {
              postAccountSwitch("slots/switch", { conversation_id: conv.id, slot_id: guideConv.slotId, mode: "now" }, function () {
                return t("switchedNow");
              });
            });
            var queueBtn = el("button", "usage-act-btn", t("switchBoundary"));
            queueBtn.type = "button";
            queueBtn.disabled = switchBusy;
            queueBtn.addEventListener("click", function () {
              postAccountSwitch("slots/switch", { conversation_id: conv.id, slot_id: guideConv.slotId, mode: "queue" }, function () {
                return t("switchedQueued");
              });
            });
            var cancelBtn = el("button", "usage-act-btn", t("cancel"));
            cancelBtn.type = "button";
            cancelBtn.disabled = switchBusy;
            cancelBtn.addEventListener("click", function () { guideConv = null; drawAccounts(); });
            guide.appendChild(nowBtn);
            guide.appendChild(queueBtn);
            guide.appendChild(cancelBtn);
            cRow.appendChild(guide);
          }
          cList.appendChild(cRow);
        });
        box.appendChild(cList);
      }

      if (switchNote && switchNote.text) {
        box.appendChild(el("p", "usage-accounts-note " + (switchNote.tone || ""), switchNote.text));
      }
      place();
    }
    function makePopover(onPlace, onClose) {
      place = onPlace;
      var box = el("div", "rpop usage-pop"); box.hidden = true; box.tabIndex = -1;
      box.setAttribute("role", "dialog"); box.setAttribute("aria-label", t("heading"));
      var header = el("div", "usage-heading"); header.appendChild(el("b", "", t("heading")));
      var help = el("span", "usage-help"), helpButton = el("button", "usage-help-button", "?");
      helpButton.type = "button"; helpButton.setAttribute("aria-label", t("legend"));
      var legendId = "usage-legend-" + (++chartSerial);
      helpButton.setAttribute("aria-describedby", legendId); help.appendChild(helpButton); header.appendChild(help);
      var close = el("button", "usage-close", "✕"); close.type = "button"; close.setAttribute("aria-label", t("close"));
      close.addEventListener("click", onClose); header.appendChild(close); box.appendChild(header);
      var legend = el("div", "usage-chart-legend");
      legend.setAttribute("id", legendId); legend.setAttribute("role", "tooltip");
      [["actual", "observed"], ["ideal", "ideal"], ["point", "latest"], ["estimated", "estimated"]].forEach(function (item) {
        var key = el("span");
        var swatch = el("i", "usage-swatch usage-swatch-" + item[0]); swatch.setAttribute("aria-hidden", "true");
        key.appendChild(swatch); key.appendChild(el("span", "", t(item[1]))); legend.appendChild(key);
      });
      help.appendChild(legend);
      box.appendChild(el("div", "usage-details"));
      if (switcherOn) box.appendChild(el("div", "usage-accounts"));
      popover = box;
      return { el: box, open: function () { shown = true; switchNote = null; guideConv = null; drawPopover(); drawAccounts(); refresh(true); if (switcherOn) fetchAccountStatus(); }, close: function () { shown = false; guideConv = null; clearFollowUps(); }, focus: function () { box.focus(); } };
    }
    refresh(false);
    // Only connected, visible pages trigger periodic queries. The daemon
    // coalesces viewers and does no background polling with no page open.
    window.setInterval(function () { if (!doc.hidden) { drawFooter(); drawPopover(); refresh(false); } }, 300000);
    doc.addEventListener("visibilitychange", function () { if (!doc.hidden) refresh(false); });
    if (switcherOn) {
      doc.addEventListener("ws-account-changed", function () {
        if (shown) { fetchAccountStatus(); refresh(false); }
        else refresh(false);
      });
    }
    return { sync: drawFooter, makePopover: makePopover };
  } };
})(window);
