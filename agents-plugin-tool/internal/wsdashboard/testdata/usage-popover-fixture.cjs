// Fixture builder mirroring ai-docs/assets/mockups/261128/usage-popup-rework/mock-api.js
// scenarios (multi, over, unauth) extended with Phase 1 slotPreviews (visited + never-visited
// inactive slots) and real /api/account/status conversations shape.
'use strict';

const H = 3600000;
const D = 86400000;

function hash(s) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

function rng(seed) {
  let a = seed;
  return function () {
    a |= 0;
    a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function windowFor(now, kind, spec, key, slotId) {
  const dur = kind === 'five_hour' ? 5 * H : 7 * D;
  const resetsAt = Math.round(now + (1 - spec.f) * dur);
  const start = resetsAt - dur;
  const asOf = now - (spec.staleMin || 0) * 60000;
  const fa = Math.max(0.02, (asOf - start) / dur);
  const step = kind === 'five_hour' ? 6 * 60000 : 2 * H;
  const n = Math.max(3, Math.floor((fa * dur) / step));
  const rand = rng(hash(key + kind));
  const weights = [];
  let total = 0;
  for (let i = 0; i < n; i++) {
    const w = Math.pow(rand(), 2.2) + 0.03;
    weights.push(w);
    total += w;
  }
  const samples = [];
  let acc = 0;
  for (let j = 0; j < n; j++) {
    acc += weights[j];
    const frac = (fa * (j + 1)) / n;
    const at = Math.round(start + frac * dur);
    const inGap = (spec.gaps || []).some(g => frac >= g[0] && frac <= g[1]);
    if (inGap || j === n - 1) continue;
    samples.push({
      kind,
      source: 'oauth',
      resetsAt,
      asOf: at,
      percent: Math.round((acc / total) * spec.pct * 10) / 10,
      ...(slotId ? { slotId } : {}),
    });
  }
  return { win: { kind, percent: spec.pct, resetsAt, asOf }, samples };
}

const BASE = {
  'claude:default': { five_hour: { f: 0.62, pct: 38 }, seven_day: { f: 0.57, pct: 54 } },
  'claude:work': { five_hour: { f: 0.62, pct: 71, gaps: [[0.30, 0.42]] }, seven_day: { f: 0.57, pct: 33 } },
  'claude:personal': { five_hour: { f: 0.62, pct: 12 }, seven_day: { f: 0.57, pct: 20, gaps: [[0.2, 0.3]] } },
  'codex:default': { five_hour: { f: 0.46, pct: 27 }, seven_day: { f: 0.41, pct: 61 } },
  'codex:team-a': { five_hour: { f: 0.46, pct: 64 }, seven_day: { f: 0.41, pct: 22, gaps: [[0.5, 0.62]] } },
  'agy:gemini': { five_hour: { f: 0.30, pct: 22 }, seven_day: { f: 0.33, pct: 40 } },
  'agy:claude_gpt': { five_hour: { f: 0.84, pct: 55 }, seven_day: { f: 0.72, pct: 28 } },
};

const OVER = {
  'claude:default': { five_hour: { f: 0.55, pct: 84, gaps: [[0.30, 0.46]] }, seven_day: { f: 0.50, pct: 71 } },
  'codex:default': { five_hour: { f: 0.40, pct: 66 }, seven_day: { f: 0.74, pct: 88, gaps: [[0.40, 0.55]] } },
  'agy:gemini': { five_hour: { f: 0.30, pct: 49 }, seven_day: { f: 0.33, pct: 61 } },
  'agy:claude_gpt': { five_hour: { f: 0.84, pct: 93 }, seven_day: { f: 0.72, pct: 46 } },
};

function createScenarioFixture(scenario = 'multi', now = Date.UTC(2026, 10, 28, 12, 30)) {
  const slots = {
    claude: { list: [{ id: 'default', label: 'Default' }, { id: 'work', label: 'Work' }, { id: 'personal', label: 'Personal' }], active: 'default' },
    codex: { list: [{ id: 'default', label: 'Default' }, { id: 'team-a', label: 'Team-A' }], active: 'default' },
  };
  const conversations = [
    { id: 'c-101', backend: 'claude', label: 'usage popover rework · 목업 정리', account_slot_id: 'default' },
    { id: 'c-102', backend: 'claude', label: 'ticket 리뷰 라운드 2', account_slot_id: 'default', pending_account_slot: 'work' },
    { id: 'x-201', backend: 'codex', label: 'dispatch 리팩터링', account_slot_id: 'default' },
  ];
  const claudeStaleMin = scenario === 'over' ? 42 : 0;
  const unauth = scenario === 'unauth';
  const specFor = key => (scenario === 'over' && OVER[key]) || BASE[key];

  function buildEntry(harness, group, key, staleMin, slotId) {
    const spec = specFor(key);
    const wins = [];
    const samples = [];
    for (const kind of ['five_hour', 'seven_day']) {
      const sp = { ...spec[kind], staleMin: kind === 'five_hour' ? staleMin : staleMin ? staleMin + 3 : 0 };
      const r = windowFor(now, kind, sp, key, slotId);
      wins.push(r.win);
      for (const s of r.samples) samples.push({ ...s, harness, group: group || '' });
    }
    return {
      entry: { harness, group: group || '', source: 'oauth', status: 'ok', windows: wins, asOf: wins[0].asOf },
      samples,
    };
  }

  function usagePayload() {
    const entries = [];
    let samples = [];
    const push = r => { entries.push(r.entry); samples = samples.concat(r.samples); };
    push(buildEntry('claude', '', 'claude:' + slots.claude.active, claudeStaleMin, slots.claude.active));
    if (unauth) {
      entries.push({ harness: 'codex', group: '', status: 'sign-in required', windows: [] });
      entries.push({ harness: 'agy', group: 'gemini', status: 'sign-in required', windows: [] });
      entries.push({ harness: 'agy', group: 'claude_gpt', status: 'sign-in required', windows: [] });
    } else {
      push(buildEntry('codex', '', 'codex:' + slots.codex.active, 0, slots.codex.active));
      push(buildEntry('agy', 'gemini', 'agy:gemini', 0, ''));
      push(buildEntry('agy', 'claude_gpt', 'agy:claude_gpt', 0, ''));
    }
    // slotPreviews: claude:work cached 12m ago, codex:team-a cached 25m ago; claude:personal never visited (omitted).
    const slotPreviews = {};
    if (slots.claude.active !== 'work') {
      const w = buildEntry('claude', '', 'claude:work', 12, 'work');
      slotPreviews.claude = { work: { asOf: w.entry.asOf, windows: w.entry.windows, samples: w.samples } };
    } else {
      const d = buildEntry('claude', '', 'claude:default', 2, 'default');
      slotPreviews.claude = { default: { asOf: d.entry.asOf, windows: d.entry.windows, samples: d.samples } };
    }
    if (!unauth) {
      const otherCx = slots.codex.active === 'default' ? 'team-a' : 'default';
      const cx = buildEntry('codex', '', 'codex:' + otherCx, 25, otherCx);
      slotPreviews.codex = { [otherCx]: { asOf: cx.entry.asOf, windows: cx.entry.windows, samples: cx.samples } };
    }
    return {
      warnPercent: 80,
      critPercent: 95,
      entries,
      history: { schema_version: 1, status: 'ok', samples },
      slotPreviews,
    };
  }

  function accountStatusPayload() {
    return {
      loopback_peer: true,
      slots: {
        claude: slots.claude.list.map(x => ({ id: x.id, harness: 'claude', label: x.label, active: x.id === slots.claude.active })),
        codex: slots.codex.list.map(x => ({ id: x.id, harness: 'codex', label: x.label, active: x.id === slots.codex.active })),
      },
      harness_auth: [
        { harness: 'agy', label: 'Antigravity', single_slot: true, available: true, probed: true, auth_required: unauth },
      ],
      conversations: conversations.map(c => ({ ...c })),
    };
  }

  function switchSlot(body) {
    if (body.conversation_id) {
      const c = conversations.find(x => x.id === body.conversation_id);
      if (!c) return { ok: false, status: 404, body: { error: 'unknown conversation' } };
      if (body.mode === 'now') { c.account_slot_id = body.slot_id; delete c.pending_account_slot; }
      else c.pending_account_slot = body.slot_id === c.account_slot_id ? undefined : body.slot_id;
      return { ok: true, status: 200, body: { conversation_id: c.id, mode: body.mode === 'now' ? 'restarted_2a' : 'queued' } };
    }
    const s = slots[body.harness];
    if (!s || !s.list.some(x => x.id === body.slot_id)) return { ok: false, status: 400, body: { error: 'unknown slot' } };
    s.active = body.slot_id;
    let queued = 0;
    for (const c of conversations) {
      if (c.backend !== body.harness || c.account_slot_id === body.slot_id || c.pending_account_slot === body.slot_id) continue;
      c.pending_account_slot = body.slot_id;
      queued++;
    }
    return { ok: true, status: 200, body: { mode: 'harness_default', queued_count: queued } };
  }

  return { now, usagePayload, accountStatusPayload, switchSlot };
}

module.exports = { createScenarioFixture };
