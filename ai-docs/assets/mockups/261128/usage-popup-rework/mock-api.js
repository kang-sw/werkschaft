/* mock-api.js — in-memory adapter mirroring the real dashboard endpoints (v2).
   GET  /rail/usage?history=1        -> { entries, history, warnPercent, critPercent }
   GET  /api/account/status          -> { slots, sessions }
   POST /api/account/slots/switch    -> { ok, harness, active_slot, mode, affected_sessions }
   POST /api/account/openai/switch   -> { ok, account_id }   (Werk panel is a placeholder in v2; kept for contract fidelity)
   UI code never touches fixtures; it only calls MockApi.call(method, path, body). */
(function () {
  "use strict";
  var H = 3600000, D = 86400000;

  function hash(s) { var h = 2166136261; for (var i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); } return h >>> 0; }
  function rng(seed) { var a = seed; return function () { a |= 0; a = a + 0x6D2B79F5 | 0; var t = Math.imul(a ^ a >>> 15, 1 | a); t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t; return ((t ^ t >>> 14) >>> 0) / 4294967296; }; }

  // spec: f = elapsed fraction at the last report, pct = reading then, gaps = [[a,b]] elapsed fractions with no samples.
  function windowFor(now, kind, spec, key) {
    var dur = kind === "five_hour" ? 5 * H : 7 * D;
    var resetsAt = Math.round(now + (1 - spec.f) * dur), start = resetsAt - dur;
    var asOf = now - (spec.staleMin || 0) * 60000;
    var fa = Math.max(0.02, (asOf - start) / dur);
    var step = kind === "five_hour" ? 6 * 60000 : 2 * H;
    var n = Math.max(3, Math.floor(fa * dur / step));
    var rand = rng(hash(key + kind)), weights = [], total = 0;
    for (var i = 0; i < n; i++) { var w = Math.pow(rand(), 2.2) + 0.03; weights.push(w); total += w; }
    var samples = [], acc = 0;
    for (var j = 0; j < n; j++) {
      acc += weights[j];
      var frac = fa * (j + 1) / n, at = Math.round(start + frac * dur);
      var inGap = (spec.gaps || []).some(function (g) { return frac >= g[0] && frac <= g[1]; });
      if (inGap || j === n - 1) continue;
      samples.push({ kind: kind, source: "oauth", resetsAt: resetsAt, asOf: at, percent: Math.round(acc / total * spec.pct * 10) / 10 });
    }
    return { win: { kind: kind, percent: spec.pct, resetsAt: resetsAt, asOf: asOf }, samples: samples };
  }

  // Each slot owns its own 5h and 7d series. Harness windows reset at staggered times.
  var BASE = {
    "claude:default": { five_hour: { f: 0.62, pct: 38 }, seven_day: { f: 0.57, pct: 54 } },
    "claude:work": { five_hour: { f: 0.62, pct: 71, gaps: [[0.30, 0.42]] }, seven_day: { f: 0.57, pct: 33 } },
    "claude:personal": { five_hour: { f: 0.62, pct: 12 }, seven_day: { f: 0.57, pct: 20, gaps: [[0.2, 0.3]] } },
    "codex:default": { five_hour: { f: 0.46, pct: 27 }, seven_day: { f: 0.41, pct: 61 } },
    "codex:team-a": { five_hour: { f: 0.46, pct: 64 }, seven_day: { f: 0.41, pct: 22, gaps: [[0.5, 0.62]] } },
    "agy:gemini": { five_hour: { f: 0.30, pct: 22 }, seven_day: { f: 0.33, pct: 40 } },
    "agy:claude_gpt": { five_hour: { f: 0.84, pct: 55 }, seven_day: { f: 0.72, pct: 28 } }
  };
  // "over-pace · delay/gap" scenario overrides
  var OVER = {
    "claude:default": { five_hour: { f: 0.55, pct: 84, gaps: [[0.30, 0.46]] }, seven_day: { f: 0.5, pct: 71 } },
    "codex:default": { five_hour: { f: 0.40, pct: 66 }, seven_day: { f: 0.74, pct: 88, gaps: [[0.4, 0.55]] } },
    "agy:gemini": { five_hour: { f: 0.30, pct: 49 }, seven_day: { f: 0.33, pct: 61 } },
    "agy:claude_gpt": { five_hour: { f: 0.84, pct: 93 }, seven_day: { f: 0.72, pct: 46 } }
  };
  function specFor(key) { return (state.scenario === "over" && OVER[key]) || BASE[key]; }

  var SLOT_LABELS = {
    claude: [{ id: "default", label: "Default" }, { id: "work", label: "Work" }, { id: "personal", label: "Personal" }],
    codex: [{ id: "default", label: "Default" }, { id: "team-a", label: "Team-A" }]
  };

  var state = null;
  function freshState(name) {
    return {
      scenario: name,
      slots: { claude: { list: SLOT_LABELS.claude.slice(), active: "default" }, codex: { list: SLOT_LABELS.codex.slice(), active: "default" } },
      sessions: [
        { id: "c-101", backend: "claude", title: "usage popover rework · 목업 정리", slot: "default", pending_account_slot: null },
        { id: "c-102", backend: "claude", title: "ticket 리뷰 라운드 2", slot: "default", pending_account_slot: "work" },
        { id: "x-201", backend: "codex", title: "dispatch 리팩터링", slot: "default", pending_account_slot: null }
      ],
      claudeStaleMin: name === "over" ? 42 : 0,
      unauth: name === "unauth"
    };
  }

  function entryFor(harness, group, key, now, stale, extra) {
    var spec = specFor(key), wins = [], samples = [];
    ["five_hour", "seven_day"].forEach(function (kind) {
      var sp = Object.assign({}, spec[kind], { staleMin: kind === "five_hour" ? stale : stale ? stale + 3 : 0 });
      var r = windowFor(now, kind, sp, key);
      wins.push(r.win);
      r.samples.forEach(function (x) { x.harness = harness; x.group = group || ""; samples.push(x); });
    });
    return { entry: Object.assign({ harness: harness, group: group || "", source: "oauth", status: "ok", windows: wins, asOf: wins[0].asOf }, extra || {}), samples: samples };
  }

  function usage() {
    var s = state, now = Date.now(), entries = [], samples = [];
    function push(r) { entries.push(r.entry); samples = samples.concat(r.samples); }
    push(entryFor("claude", "", "claude:" + s.slots.claude.active, now, s.claudeStaleMin, { slot_id: s.slots.claude.active }));
    if (s.unauth) entries.push({ harness: "codex", group: "", status: "sign-in required", windows: [], slot_id: s.slots.codex.active });
    else push(entryFor("codex", "", "codex:" + s.slots.codex.active, now, 0, { slot_id: s.slots.codex.active }));
    if (s.unauth) {
      entries.push({ harness: "agy", group: "gemini", status: "sign-in required", windows: [] });
      entries.push({ harness: "agy", group: "claude_gpt", status: "sign-in required", windows: [] });
    } else {
      push(entryFor("agy", "gemini", "agy:gemini", now, 0, {}));
      push(entryFor("agy", "claude_gpt", "agy:claude_gpt", now, 0, {}));
    }
    return { warnPercent: 80, critPercent: 95, now: now, entries: entries, history: { schema_version: 1, status: "ok", samples: samples } };
  }
  function accountStatus() {
    var slots = {};
    Object.keys(state.slots).forEach(function (h) {
      slots[h] = state.slots[h].list.map(function (x) { return { id: x.id, label: x.label, active: x.id === state.slots[h].active }; });
    });
    return { slots: slots, sessions: state.sessions.map(function (x) { return Object.assign({}, x); }) };
  }

  function switchSlot(body) {
    var s = state, h = body && body.harness, slot = s.slots[h];
    if (!slot || !slot.list.some(function (x) { return x.id === body.slot_id; })) return { status: 400, body: { error: "unknown slot" } };
    var label = slot.list.filter(function (x) { return x.id === body.slot_id; })[0].label;
    if (body.conversation_id) {
      var c = s.sessions.filter(function (x) { return x.id === body.conversation_id; })[0];
      if (!c) return { status: 404, body: { error: "unknown conversation" } };
      if (body.mode === "now") { c.slot = body.slot_id; c.pending_account_slot = null; }
      else c.pending_account_slot = body.slot_id === c.slot ? null : body.slot_id;
      return { status: 200, body: { ok: true, harness: h, conversation_id: c.id, mode: body.mode, slot_id: body.slot_id, slot_label: label } };
    }
    slot.active = body.slot_id;
    var affected = 0;
    s.sessions.forEach(function (c) {
      if (c.backend !== h || c.slot === body.slot_id || c.pending_account_slot === body.slot_id) return;
      c.pending_account_slot = body.slot_id; affected++;
    });
    return { status: 200, body: { ok: true, harness: h, active_slot: body.slot_id, slot_label: label, mode: "queue", affected_sessions: affected } };
  }

  function route(method, path, body) {
    var p = path.split("?")[0];
    if (method === "GET" && p === "/rail/usage") return { status: 200, body: usage() };
    if (method === "GET" && p === "/api/account/status") return { status: 200, body: accountStatus() };
    if (method === "POST" && p === "/api/account/slots/switch") return switchSlot(body);
    if (method === "POST" && p === "/api/account/openai/switch") return { status: 200, body: { ok: true, account_id: body && body.account_id } };
    return { status: 404, body: { error: "not found: " + method + " " + p } };
  }

  var failNext = false;
  window.MockApi = {
    latency: 240,
    setScenario: function (name) { state = freshState(name); },
    failNext: function () { failNext = true; },
    call: function (method, path, body) {
      return new Promise(function (resolve, reject) {
        var ms = window.MockApi.latency + Math.round(Math.random() * 100);
        setTimeout(function () {
          if (failNext) { failNext = false; reject(new Error("503 usage upstream unavailable")); return; }
          var r = route(method, path, body);
          if (r.status >= 400) { var e = new Error(r.status + " " + (r.body && r.body.error || "error")); e.status = r.status; reject(e); return; }
          resolve(JSON.parse(JSON.stringify(r.body)));
        }, ms);
      });
    }
  };
  state = freshState("multi");
})();
