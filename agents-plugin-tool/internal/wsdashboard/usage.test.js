const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const { readAssetSource } = require('./testdata/asset-source.cjs');

const USAGE_SOURCE = readAssetSource('wsdashboard', 'usage.js');

function environment(now = Date.UTC(2026, 10, 15, 12, 0), locale = 'en') {
  class Element {
    constructor(tag) {
      this.tagName = tag;
      this.children = [];
      this.parentNode = null;
      this.style = { setProperty(k, v) { this[k] = v; } };
      this.attributes = {};
      this.events = {};
      this._text = '';
      this.className = '';
      this.hidden = false;
    }
    set textContent(value) { this._text = value; this.children = []; }
    get textContent() { return this._text + this.children.map(c => c.textContent).join(''); }
    appendChild(child) { child.parentNode = this; this.children.push(child); return child; }
    setAttribute(key, value) { this.attributes[key] = String(value); if (key === 'class') this.className = String(value); }
    getAttribute(key) { return Object.prototype.hasOwnProperty.call(this.attributes, key) ? this.attributes[key] : null; }
    addEventListener(key, fn) {
      if (!this.events[key]) this.events[key] = [];
      this.events[key].push(fn);
    }
    emit(key, ev) {
      (this.events[key] || []).forEach(fn => fn(ev));
    }
    focus() { this.focused = true; }
    querySelector(selector) {
      const cls = selector.startsWith('.') ? selector.slice(1) : null;
      for (const child of this.children) {
        if (cls ? (child.className || '').split(/\s+/).includes(cls) : child.tagName === selector) return child;
        const found = child.querySelector(selector);
        if (found) return found;
      }
      return null;
    }
  }
  const button = new Element('button'); button.className = 'usage-toggle';
  const alert = new Element('span'); alert.className = 'talert'; alert.textContent = 'Alert';
  const center = new Element('span'); center.className = 'tcen'; center.appendChild(button); center.appendChild(alert);
  const top = new Element('div'); top.className = 'top'; top.appendChild(center);
  const doc = {
    hidden: false,
    events: {},
    createElement: tag => new Element(tag),
    createElementNS: (_, tag) => new Element(tag),
    createTextNode: text => { const n = new Element('#text'); n._text = String(text); return n; },
    addEventListener(key, fn) { this.events[key] = fn; }
  };
  const calls = [], ticks = [], timers = [];
  let payload = { entries: [], warnPercent: 80, critPercent: 95 }, fail = false;
  const window = {
    setInterval(fn, ms) { ticks.push({ fn, ms }); },
    setTimeout(fn, ms) { timers.push({ fn, ms, live: true }); return timers.length; },
    clearTimeout(id) { if (timers[id - 1]) timers[id - 1].live = false; },
    fetch(url, options) {
      calls.push({ url, options });
      return fail ? Promise.reject(new Error('fake failure')) : Promise.resolve({ ok: true, json: () => Promise.resolve(payload) });
    }
  };
  window.wsI18n = { lang: locale, t: JSON.parse(fs.readFileSync(__dirname + '/../wsi18n/tables/' + locale + '.json', 'utf8')) };
  window.wsT = function (key, ...args) {
    assert.ok(Object.hasOwn(window.wsI18n.t, key), key);
    return window.wsI18n.t[key].replace(/\{(\d+)\}/g, (match, i) => i < args.length ? String(args[i]) : match);
  };
  class Clock extends Date { static now() { return now; } }
  vm.runInNewContext(USAGE_SOURCE, { window, Date: Clock });
  return { window, top, doc, button, alert, center, calls, ticks, timers, now(value) { now = value; }, set(value) { payload = value; }, fail() { fail = true; } };
}

const flush = () => new Promise(resolve => setImmediate(resolve));
const groups = button => button.children.filter(c => c.className === 'usage-harness');
function nodes(root, tagOrClass) {
  return root.children.flatMap(child => [
    ...(tagOrClass.startsWith('.') ? (child.className || '').split(/\s+/).includes(tagOrClass.slice(1)) : child.tagName === tagOrClass) ? [child] : [],
    ...nodes(child, tagOrClass)
  ]);
}

async function chartFixture({ history = 'ok', resetKnown = true, stale = false, percents = [10, 60], weekly = false, locale = 'en' } = {}) {
  const start = Date.UTC(2026, 10, 15, 8, 0), duration = 5 * 3600000, asOf = start + 0.4 * duration;
  const env = environment(asOf + (stale ? 42 * 60000 : 0), locale), resetsAt = resetKnown ? start + duration : 0;
  const samples = percents.map((percent, i) => ({ harness: 'claude', kind: 'five_hour', source: '<safe-source>', resetsAt, asOf: start + (i ? 0.4 : 0.2) * duration, percent }));
  const entry = { harness: 'claude', source: '<safe-source>', asOf, windows: [samples.at(-1)] };
  if (weekly) entry.windows.unshift({ kind: 'seven_day', percent: 99, asOf, resetsAt: asOf + 4 * 86400000 });
  const payload = { entries: [entry] };
  if (history !== 'missing') payload.history = { schema_version: 1, status: history, samples };
  env.set(payload);
  let placeCount = 0;
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  const pop = mounted.makePopover(() => { placeCount++; }, () => {}); pop.open(); await flush();
  return { env, pop, payload, placeCount: () => placeCount };
}

test('unified 5H and 7D charts draw Mode A progress and switch to Mode B timeline with exact bounds and date labels', async () => {
  const { pop, placeCount } = await chartFixture({ weekly: true });
  const panels = nodes(pop.el, '.up-cpanel');
  assert.equal(panels.length, 2);
  assert.equal(panels[0].attributes['data-kind'], 'five_hour');
  assert.equal(panels[1].attributes['data-kind'], 'seven_day');
  const chart5 = panels[0].querySelector('.usage-chart');
  assert.equal(chart5.attributes.role, 'img');
  assert.match(chart5.attributes['aria-label'], /not prediction/);
  assert.equal(nodes(chart5, '.usage-ideal')[0].attributes['stroke-dasharray'], '4 4');
  assert.ok(nodes(chart5, '.usage-actual').length >= 1);
  assert.equal(nodes(chart5, '.usage-point-current')[0].attributes.r, '4');
  assert.equal(nodes(chart5, '.up-pin').length, 1);
  assert.equal(nodes(chart5, '.up-pin')[0].textContent, 'Cl');
  assert.ok(!pop.el.textContent.includes('<safe-source>'));

  // Toggle to Mode B (Timeline · Gantt) and assert place() is re-invoked, date labels and Gantt lanes exist.
  const placesBefore = placeCount();
  const modeBtns = nodes(pop.el, '.up-as-opt');
  assert.equal(modeBtns.length, 2);
  assert.equal(modeBtns[0].attributes['aria-selected'], 'true');
  modeBtns[1].emit('click');
  assert.equal(modeBtns[1].attributes['aria-selected'], 'true');
  assert.ok(placeCount() > placesBefore, 'mode toggle invokes railPopPlace');
  const panelsB = nodes(pop.el, '.up-cpanel');
  const chart5B = panelsB[0].querySelector('.usage-chart');
  const chart7B = panelsB[1].querySelector('.usage-chart');
  assert.equal(nodes(chart5B, '.up-gl').length, 1);
  assert.equal(nodes(chart5B, '.usage-now').length, 1);
  assert.equal(nodes(chart5B, '.up-bound-label').length, 2);
  assert.ok(nodes(chart7B, '.usage-day-label').length >= 2);
  // 7D date labels carry M/D + weekday format.
  assert.match(nodes(chart7B, '.usage-day-label')[0].textContent, /\d+\/\d+\s+\S+/);
  // Group 2 placeholder is present.
  assert.ok(pop.el.querySelector('.up-ph'));
});

test('Mode B anchors stale pin at X(s.at) rather than Now and keeps legend collapsible', async () => {
  const { pop, placeCount } = await chartFixture({ stale: true });
  const legendBtn = pop.el.querySelector('.up-legend-btn');
  const legendBox = pop.el.querySelector('.up-legend');
  assert.equal(legendBox.hidden, true);
  const placesBefore = placeCount();
  legendBtn.emit('click');
  assert.equal(legendBox.hidden, false);
  assert.ok(placeCount() > placesBefore);
  assert.ok(legendBox.textContent.includes('Ideal burn, not prediction'));

  // Switch to timeline mode and verify stale dot/pin sits strictly left of Now line.
  nodes(pop.el, '.up-as-opt')[1].emit('click');
  const chart5 = nodes(pop.el, '.up-cpanel')[0].querySelector('.usage-chart');
  const dotX = Number(chart5.querySelector('.usage-point-current').attributes.cx);
  const nowX = Number(chart5.querySelector('.usage-now').attributes.x1);
  assert.ok(dotX < nowX - 5, `stale point (${dotX}) stays left of Now (${nowX})`);
  assert.equal(chart5.querySelector('.usage-point-current').attributes.fill, 'var(--wb-muted-fg)');
  assert.equal(pop.el.querySelector('.up-sync').attributes['data-tone'], 'warn');
  assert.ok(pop.el.querySelector('.up-nc-clk'));
});

test('hover/click focus dims other harnesses with zero SVG re-render, and Esc/blank click unlocks', async () => {
  const start = Date.UTC(2026, 10, 15, 8, 0), dur = 5 * 3600000, at = start + 0.4 * dur;
  const env = environment(at, 'en');
  env.set({
    entries: [
      { harness: 'claude', source: 'Claude push', asOf: at, windows: [{ kind: 'five_hour', percent: 40, asOf: at, resetsAt: start + dur }, { kind: 'seven_day', percent: 55, asOf: at, resetsAt: at + 3 * 86400000 }] },
      { harness: 'codex', source: 'via Codex CLI', asOf: at, windows: [{ kind: 'five_hour', percent: 25, asOf: at, resetsAt: start + dur }, { kind: 'seven_day', percent: 35, asOf: at, resetsAt: at + 4 * 86400000 }] }
    ]
  });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();

  const svgsBefore = nodes(pop.el, '.usage-chart');
  assert.equal(svgsBefore.length, 2);
  const claudeRow = nodes(pop.el, '.up-hr').find(r => r.attributes['data-k'] === 'claude');
  const codexRow = nodes(pop.el, '.up-hr').find(r => r.attributes['data-k'] === 'codex');
  assert.ok(claudeRow && codexRow);

  // Hover Claude row -> Claude focused, Codex dimmed, callouts unhidden, exact same SVG nodes.
  pop.el.emit('mouseover', { target: claudeRow });
  assert.deepEqual(nodes(pop.el, '.usage-chart'), svgsBefore, 'hover must not re-render SVGs');
  assert.ok(claudeRow.className.includes('is-focus'));
  assert.ok(codexRow.className.includes('is-dim'));
  const claudeSeries = nodes(pop.el, '.up-ser').filter(s => s.attributes['data-k'] === 'claude');
  const codexSeries = nodes(pop.el, '.up-ser').filter(s => s.attributes['data-k'] === 'codex');
  assert.ok(claudeSeries.every(s => s.className.includes('is-focus')));
  assert.ok(codexSeries.every(s => s.className.includes('is-dim')));
  const visibleCallouts = nodes(pop.el, '.up-callout').filter(c => !c.hidden);
  assert.equal(visibleCallouts.length, 2);

  // Click locks focus; mouseleave keeps lock; pop.back() (Esc hook) unlocks without closing.
  pop.el.emit('click', { target: claudeRow });
  pop.el.emit('mouseleave');
  assert.ok(claudeRow.className.includes('is-focus'), 'locked focus survives mouseleave');
  assert.equal(pop.back(), true, 'back() consumes Escape when locked');
  assert.ok(!claudeRow.className.includes('is-focus'));
  assert.equal(pop.back(), false, 'second Escape lets host close popover');

  // Click lock again then click blank space to unlock.
  pop.el.emit('click', { target: codexRow });
  assert.ok(codexRow.className.includes('is-focus'));
  pop.el.emit('click', { target: pop.el });
  assert.ok(!codexRow.className.includes('is-focus'));
  assert.deepEqual(nodes(pop.el, '.usage-chart'), svgsBefore);
});

test('all eleven locales localize popup chrome, charts, rows, and preview strings with no raw keys', async () => {
  const locales = fs.readdirSync(__dirname + '/../wsi18n/tables').map(name => name.replace('.json', ''));
  assert.equal(locales.length, 11);
  for (const locale of locales) {
    const { env, pop } = await chartFixture({ weekly: true, stale: true, locale });
    const t = key => env.window.wsT('js.usage.' + key);
    assert.equal(pop.el.attributes['aria-label'], t('heading'));
    assert.equal(pop.el.querySelector('.usage-close').attributes['aria-label'], t('close'));
    assert.ok(pop.el.textContent.includes(t('mode_progress')), locale);
    assert.ok(pop.el.textContent.includes(t('mode_timeline')), locale);
    assert.ok(pop.el.textContent.includes(t('group1_title')), locale);
    assert.ok(pop.el.textContent.includes(t('group2_title')), locale);
    assert.ok(pop.el.textContent.includes(t('placeholder')), locale);
    pop.el.querySelector('.up-legend-btn').emit('click');
    assert.ok(pop.el.querySelector('.up-legend').textContent.includes(t('ideal')), locale);
    assert.doesNotMatch(pop.el.textContent, /js\.usage\.|<safe-source>/, locale);
  }
});

test('missing, empty and unavailable history and unknown resets degrade truthfully', async () => {
  for (const history of ['missing', 'empty', 'unavailable']) {
    const { pop } = await chartFixture({ history });
    const chart5 = nodes(pop.el, '.up-cpanel')[0].querySelector('.usage-chart');
    assert.equal(nodes(chart5, '.usage-point').length, 1);
    assert.equal(nodes(chart5, '.usage-actual').length, 0);
    assert.equal(nodes(chart5, '.usage-ideal').length, 1);
  }
  const { pop: unkPop } = await chartFixture({ resetKnown: false });
  const chartUnk = nodes(unkPop.el, '.up-cpanel')[0].querySelector('.usage-chart');
  assert.equal(nodes(chartUnk, '.usage-point').length, 1);
  assert.equal(nodes(chartUnk, '.usage-ideal').length, 0);
  assert.equal(nodes(chartUnk, '.usage-actual').length, 0);
});

test('clip-path ids remain unique across redraws and repeated mounts', async () => {
  const { env, pop } = await chartFixture({ weekly: true });
  const first = nodes(pop.el, 'clipPath').map(g => g.attributes.id);
  env.ticks[0].fn(); await flush();
  const second = nodes(pop.el, 'clipPath').map(g => g.attributes.id);
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  const another = mounted.makePopover(() => {}, () => {}); another.open(); await flush();
  const third = nodes(another.el, 'clipPath').map(g => g.attributes.id);
  for (const ids of [first, second, third]) assert.ok(ids.length > 0);
  const all = [...first, ...second, ...third]; assert.equal(new Set(all).size, all.length);
});

test('usage ordering, expired windows, thresholds and ages', () => {
  const u = environment().window.WsUsage;
  const entries = [
    { harness: 'agy', group: 'claude_gpt', windows: [{ percent: 95, resetsAt: 50 }] },
    { harness: 'codex', windows: [{ percent: 80, resetsAt: 50 }] },
    { harness: 'agy', group: 'gemini', windows: [{ percent: 15, resetsAt: 50 }] },
    { harness: 'claude', windows: [{ percent: 99, resetsAt: 5 }, { percent: 40, resetsAt: 50 }] }
  ];
  assert.deepEqual(
    u.sorted(entries).map(e => e.group ? e.harness + ':' + e.group : e.harness),
    ['claude', 'codex', 'agy:gemini', 'agy:claude_gpt']
  );
  assert.equal(entries[0].harness, 'agy');
  assert.equal(u.active(entries[3], 10).length, 1);
  for (const [p, want] of [[79, 'normal'], [80, 'warning'], [94, 'warning'], [95, 'critical']]) assert.equal(u.level(p, 80, 95), want);
  assert.equal(u.age(60000, 180000), 'value from 2 min ago');
  assert.equal(u.age(0, 180000), 'usage unavailable');
});

test('footer and popover show detected harnesses, safe text, and retain last value on failure', async () => {
  const at = Date.UTC(2026, 10, 15, 12, 0), env = environment(at), reset = at + 3600000;
  env.set({ warnPercent: 80, critPercent: 95, entries: [
    { harness: 'claude', plan: 'max', source: 'Claude push', asOf: at, windows: [{ kind: 'five_hour', percent: 35, resetsAt: reset, asOf: at }, { kind: 'seven_day', percent: 82, resetsAt: reset + 86400000, asOf: at }] },
    { harness: 'codex', plan: '<fake-plan>', source: 'via Pi', status: 'Pi token expired - open Pi to refresh', asOf: at, windows: [{ kind: 'seven_day', percent: 96, resetsAt: reset + 86400000, asOf: at }] }
  ] });
  const mounted = env.window.WsUsage.mount(env.doc, env.top);
  await flush();
  assert.equal(groups(env.button).length, 2);
  assert.equal(groups(env.button)[0].querySelector('.usage-columns').children.length, 2);
  assert.equal(groups(env.button)[1].querySelector('.usage-columns').children.length, 1);
  assert.equal(groups(env.button)[1].querySelector('.usage-number').textContent, '96%');
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); pop.focus(); await flush();
  const text = pop.el.textContent;
  assert.ok(text.indexOf('Claude') < text.indexOf('Codex'));
  for (const s of ['96%', 'Weekly', '5-hour']) assert.ok(text.includes(s), s);
  for (const source of ['via Pi', 'Claude push']) assert.ok(!text.includes(source), source);
  assert.equal(pop.el.attributes.role, 'dialog');
  assert.equal(pop.el.focused, true);
  assert.equal(env.calls[1].url, '/rail/usage?history=1&refresh=1');
  assert.equal(env.calls[0].url, '/rail/usage?history=1');
  assert.equal(env.calls[1].options.headers['X-WS-Usage-Refresh'], '1');
  assert.equal(env.calls[0].options.credentials, 'same-origin');
  env.button.textContent = ''; mounted.sync(); assert.equal(groups(env.button).length, 2);
  env.fail(); env.ticks[0].fn(); await flush();
  assert.equal(pop.el.querySelector('.up-sync').attributes['data-tone'], 'err');
  assert.ok(pop.el.textContent.includes('96%')); // retained last value
  pop.close();
});

test('fractional percentages round like the server and pace, in footer and popover', async () => {
  const at = Date.UTC(2026, 10, 15, 12, 0), env = environment(at), resetsAt = at + 3600000;
  const cases = [[59.6, '60'], [59.000000001, '59'], [28.999999999999996, '29'], [59.4, '59']];
  const mounted = env.window.WsUsage.mount(env.doc, env.top);
  const pop = mounted.makePopover(() => {}, () => {}); pop.open();
  for (const [percent, want] of cases) {
    env.set({ entries: [{ harness: 'codex', asOf: at, windows: [{ kind: 'seven_day', percent, resetsAt, asOf: at }] }] });
    env.ticks[0].fn(); await flush();
    assert.equal(env.button.querySelector('.usage-number').textContent, want + '%', String(percent));
    assert.ok(env.button.querySelector('.usage-column').title.includes('Weekly: ' + want + '%;'), String(percent));
    assert.ok(pop.el.textContent.includes(want + '%'), String(percent));
  }
  pop.close();
});

test('footer matches normal, over-pace and weekly-only reference columns', async () => {
  const at = Date.UTC(2026, 10, 15, 12, 0), env = environment(at);
  const w = (kind, percent, elapsed) => ({ kind, percent, asOf: at, resetsAt: at + (1 - elapsed) * (kind === 'five_hour' ? 5 * 3600000 : 7 * 86400000) });
  const entries = [
    { harness: 'claude', windows: [w('seven_day', 53, 0.66), w('five_hour', 20, 1 / 3)] },
    { harness: 'codex', windows: [w('seven_day', 38, 0.45)] }
  ];
  env.set({ entries });
  env.window.WsUsage.mount(env.doc, env.top); await flush();
  const columns = groups(env.button).map(g => g.querySelector('.usage-columns').children);
  assert.deepEqual(columns.map(c => c.length), [2, 1]);
  assert.deepEqual(columns.flatMap(c => c.map(v => v.querySelector('.usage-key').textContent)), ['5h', 'wk', 'wk']);
  assert.deepEqual([env.window.wsT('js.usage.key5h'), env.window.wsT('js.usage.keyweekly')], ['5h', 'wk']);
  assert.deepEqual(columns.flatMap(c => c.map(v => v.querySelector('.usage-number').textContent)), ['20%', '53%', '38%']);
  assert.deepEqual(columns.flatMap(c => c.map(v => v.querySelector('.usage-pace').textContent)), ['−13%', '−13%', '−7%']);
  entries[0].windows[1].percent = 64;
  env.ticks[0].fn(); await flush();
  assert.equal(groups(env.button)[0].querySelector('.over').textContent, '+31%');
});

test('stale boundary is strictly after 30 minutes, per window', async () => {
  const at = Date.UTC(2026, 10, 15, 12, 0), env = environment(at + 30 * 60000);
  const entry = { harness: 'claude', asOf: at + 30 * 60000, windows: [
    { kind: 'five_hour', percent: 20, asOf: at, resetsAt: at + 5 * 3600000 * 2 / 3 },
    { kind: 'seven_day', percent: 53, resetsAt: at + 7 * 86400000 * 0.34 }
  ] };
  env.set({ entries: [entry] });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.equal(env.button.querySelector('.usage-stale'), null);
  env.now(at + 30 * 60000 + 1); mounted.sync();
  const staleCol = env.button.querySelector('.usage-stale');
  assert.equal(staleCol.className, 'usage-column usage-stale');
  assert.equal(staleCol.querySelector('.usage-number').textContent, '20%');
  assert.equal(staleCol.querySelector('.usage-pace'), null);
  assert.ok(env.button.querySelector('.usage-clock'));
  assert.match(staleCol.attributes['aria-label'], /No recent 5-hour report/);
});

test('history composes into the single coalesced viewer request and schedules follow-ups on open', async () => {
  const env = environment();
  const mounted = env.window.WsUsage.mount(env.doc, env.top);
  const pop = mounted.makePopover(() => {}, () => {});
  pop.open(); env.ticks[0].fn(); env.doc.events.visibilitychange();
  assert.equal(env.calls.length, 1);
  assert.equal(env.calls[0].url, '/rail/usage?history=1');
  await flush();
  assert.equal(env.calls.length, 2);
  assert.equal(env.calls[1].url, '/rail/usage?history=1&refresh=1');
  assert.equal(env.calls[1].options.cache, 'no-store');
  assert.deepEqual(env.timers.filter(t => t.live && [2000, 11000, 32000].includes(t.ms)).map(t => t.ms), [2000, 11000, 32000]);
  const before = env.calls.length;
  pop.close(); env.timers.at(-1).fn(); await flush();
  assert.equal(env.calls.length, before);
});

test('nearest reset timer redraws expired windows and refreshes usage', async () => {
  const at = Date.UTC(2026, 10, 15, 12, 0), reset = at + 1000, env = environment(at);
  env.set({ entries: [{ harness: 'claude', asOf: at, windows: [{ kind: 'five_hour', percent: 42, asOf: at, resetsAt: reset }] }] });
  env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.equal(env.button.querySelector('.usage-number').textContent, '42%');
  const timer = env.timers.find(t => t.live && t.ms === 1100);
  assert.ok(timer);
  env.now(reset + 100); timer.fn();
  assert.equal(env.button.querySelector('.usage-number'), null);
  assert.ok(env.button.textContent.includes('usage unavailable'));
  await flush();
  assert.equal(env.calls.length, 2);
});

test('handle redraw and sync preserve the sibling alert slot and Track 1 CSS rules stay intact', async () => {
  const at = Date.UTC(2026, 10, 15, 12, 0), env = environment(at);
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.deepEqual(env.center.children, [env.button, env.alert]);
  env.set({ entries: [{ harness: 'codex', asOf: at, windows: [{ kind: 'seven_day', percent: 38, asOf: at, resetsAt: at + 86400000 }] }] });
  env.ticks[0].fn(); await flush();
  assert.equal(env.button.children[0].className, 'usage-mini');
  assert.deepEqual(env.center.children, [env.button, env.alert]);

  const css = fs.readFileSync(__dirname + '/assets/usage.css', 'utf8');
  assert.match(css, /\.usage-mini \{ display: none;/);
  assert.match(css, /\.dwrap > \.top\.tu \.tcen > \.usage-toggle \{ width: var\(--toph, 36px\)/);
  assert.match(css, /\.shell-bottom\.bu \.usage-toggle \{ width: 24px;/);
  assert.match(css, /\.shell-bottom\.bu \.usage-toggle > :not\(\.usage-mini\) \{ display: none;/);
  assert.match(css, /\.shell-bottom\.bu \.usage-toggle > \.usage-mini \{ display: inline-flex;/);
  assert.match(css, /width: min\(680px, calc\(100vw - 16px\)\)/);
  assert.match(css, /@container usage-pop \(max-width: 559px\)/);
  assert.match(css, /@container usage-pop \(max-width: 420px\)/);
  assert.match(css, /:root\[data-theme="light"\] \.rpop\.usage-pop[\s\S]*--k-codex-bg:\s*#3b4049/);
});

test('empty login state and hidden-page cadence', async () => {
  const env = environment();
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();
  assert.ok(pop.el.textContent.includes('not logged in'));
  assert.equal(env.button.textContent, 'Plan usage');
  assert.equal(env.ticks[0].ms, 300000);
  const before = env.calls.length;
  env.doc.hidden = true; env.ticks[0].fn(); await flush(); assert.equal(env.calls.length, before);
  env.doc.hidden = false; env.doc.events.visibilitychange(); await flush(); assert.equal(env.calls.length, before + 1);
});

test('agy renders compact footer groups and nested Group 1 rows with disabled dimming', async () => {
  const at = Date.UTC(2026, 10, 19, 12, 0), observed = at - 12 * 60000, env = environment(at);
  const reset5h = observed + 3 * 3600000, resetWk = observed + 4 * 86400000;
  env.set({
    entries: [
      { harness: 'claude', asOf: at, windows: [{ kind: 'five_hour', percent: 25, asOf: at, resetsAt: reset5h }] },
      { harness: 'agy', group: 'gemini', label: 'Gemini Models', source: 'via agy CLI', asOf: observed, windows: [
        { kind: 'five_hour', percent: 40, asOf: observed, resetsAt: reset5h },
        { kind: 'seven_day', percent: 55, asOf: observed, resetsAt: resetWk }
      ] },
      { harness: 'agy', group: 'claude_gpt', label: 'Claude and GPT models', source: 'via agy CLI', asOf: observed, windows: [
        { kind: 'five_hour', percent: 80, asOf: observed, resetsAt: reset5h, disabled: true },
        { kind: 'seven_day', percent: 30, asOf: observed, resetsAt: resetWk }
      ] }
    ]
  });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.equal(groups(env.button).length, 3);
  assert.deepEqual(nodes(env.button, '.usage-group-label').map(n => n.textContent), ['Gemini', 'Claude/GPT']);
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();
  const agyGroup = pop.el.querySelector('.up-hg');
  assert.ok(agyGroup);
  assert.equal(agyGroup.attributes['data-readonly-slot'], '1');
  const agyRows = nodes(agyGroup, '.up-hr');
  assert.equal(agyRows.length, 2);
  assert.equal(nodes(agyGroup, '.usage-disabled').length, 1);
  pop.close();
});

test('inline slot pills, collapsible sessions drawer, Group 2 placeholder, and Manage accounts link work end to end', async () => {
  const at = Date.UTC(2026, 10, 28, 9, 0), reset5h = at + 3 * 3600000, reset7d = at + 4 * 86400000;
  const env = environment(at, 'en');
  let accountStatus = {
    loopback_peer: true,
    slots: {
      claude: [
        { id: 'default', harness: 'claude', label: 'Default', implicit: true, active: true },
        { id: 'work', harness: 'claude', label: 'Work', config_dir: '/tmp/claude-work', active: false },
        { id: 'personal', harness: 'claude', label: 'Personal', config_dir: '/tmp/claude-personal', active: false }
      ],
      codex: [
        { id: 'default', harness: 'codex', label: 'Default', implicit: true, active: true },
        { id: 'team-a', harness: 'codex', label: 'Team-A', config_dir: '/tmp/codex-team', active: false }
      ]
    },
    harness_auth: [
      { harness: 'agy', label: 'Antigravity', single_slot: true, available: true, probed: true, auth_required: false }
    ],
    conversations: [
      { id: 'conv-1', backend: 'codex', label: 'Main Lead', account_slot_id: 'default' }
    ]
  };
  const posts = [];
  env.set({
    entries: [
      { harness: 'claude', asOf: at, windows: [{ kind: 'five_hour', percent: 38, asOf: at, resetsAt: reset5h }, { kind: 'seven_day', percent: 54, asOf: at, resetsAt: reset7d }] },
      { harness: 'codex', asOf: at, windows: [{ kind: 'five_hour', percent: 27, asOf: at, resetsAt: reset5h }, { kind: 'seven_day', percent: 61, asOf: at, resetsAt: reset7d }] },
      { harness: 'openrouter', label: 'OpenRouter', asOf: at, openrouter: { balance: 18.75, usageDaily: 1.25 } }
    ]
  });
  const baseFetch = env.window.fetch;
  env.window.fetch = (url, options = {}) => {
    if (url === '/api/account/status') {
      env.calls.push({ url, options });
      return Promise.resolve({ ok: true, json: () => Promise.resolve(accountStatus) });
    }
    if (url === '/api/account/slots/switch') {
      const body = JSON.parse(options.body || '{}');
      posts.push({ url, body });
      if (body.conversation_id && body.mode === 'queue') {
        accountStatus = {
          ...accountStatus,
          conversations: [{ id: 'conv-1', backend: 'codex', label: 'Main Lead', account_slot_id: 'default', pending_account_slot: body.slot_id }]
        };
        return Promise.resolve({ ok: true, json: () => Promise.resolve({ conversation_id: body.conversation_id, mode: 'queued' }) });
      }
      if (body.conversation_id && body.mode === 'now') {
        accountStatus = {
          ...accountStatus,
          conversations: [{ id: 'conv-1', backend: 'codex', label: 'Main Lead', account_slot_id: body.slot_id }]
        };
        return Promise.resolve({ ok: true, json: () => Promise.resolve({ conversation_id: body.conversation_id, mode: 'restarted_2a' }) });
      }
      accountStatus = {
        ...accountStatus,
        slots: {
          ...accountStatus.slots,
          [body.harness]: accountStatus.slots[body.harness].map(s => ({ ...s, active: s.id === body.slot_id }))
        }
      };
      return Promise.resolve({ ok: true, json: () => Promise.resolve({ mode: 'harness_default', queued_count: 1 }) });
    }
    return baseFetch(url, options);
  };

  let openedSettingsHref = null, placeCount = 0;
  const mounted = env.window.WsUsage.mount(env.doc, env.top, {
    accountSwitcher: true,
    openSettings: href => { openedSettingsHref = href; }
  });
  await flush();
  // OpenRouter still renders in footer bar, while popover replaces Werk/OpenRouter cards with Group 2 placeholder.
  assert.equal(groups(env.button).length, 3);

  const pop = mounted.makePopover(() => { placeCount++; }, () => {});
  pop.open();
  await flush();
  assert.equal(nodes(pop.el, '.usage-metric-card').length, 0);
  assert.ok(pop.el.querySelector('.up-ph'));

  // Manage accounts link works.
  const manage = pop.el.querySelector('.usage-manage-link');
  assert.equal(manage.href, '/settings?section=account');
  let prevented = false;
  manage.emit('click', { preventDefault() { prevented = true; } });
  assert.equal(prevented, true);
  assert.equal(openedSettingsHref, '/settings?section=account');

  // Inline Claude slot pill click posts /api/account/slots/switch once.
  const claudeRow = nodes(pop.el, '.up-hr').find(r => r.attributes['data-k'] === 'claude');
  const claudePills = nodes(claudeRow, '.up-sp');
  assert.equal(claudePills.length, 3);
  assert.equal(claudePills[0].attributes['aria-pressed'], 'true');
  claudePills[1].emit('click');
  await flush();
  assert.deepEqual(posts[0], { url: '/api/account/slots/switch', body: { harness: 'claude', slot_id: 'work' } });
  assert.ok(pop.el.querySelector('.up-rbanner'));

  // Sessions drawer is collapsed by default; expanding calls railPopPlace and supports queue & now flows.
  assert.equal(pop.el.querySelector('.up-sess-body'), null);
  const beforePlace = placeCount;
  pop.el.querySelector('.up-sess-hd').emit('click');
  assert.ok(placeCount > beforePlace, 'expanding sessions drawer calls railPopPlace');
  const sRow = pop.el.querySelector('.up-srow');
  assert.ok(sRow);
  const sPills = nodes(sRow, '.up-sp');
  sPills[1].emit('click'); // choose Team-A -> confirm bar
  const confirmBar = pop.el.querySelector('.up-confirm');
  assert.ok(confirmBar);
  const queueBtn = nodes(confirmBar, '.up-cbtn').find(b => b.attributes['data-mode'] === 'queue');
  queueBtn.emit('click');
  await flush();
  assert.deepEqual(posts[1], { url: '/api/account/slots/switch', body: { conversation_id: 'conv-1', slot_id: 'team-a', mode: 'queue' } });
  assert.ok(pop.el.querySelector('.up-pend'));

  // Quick "Switch now" on queued session posts mode: "now".
  const nowBtn = nodes(pop.el.querySelector('.up-srow'), '.up-cbtn').find(b => b.attributes['data-mode'] === 'now');
  nowBtn.emit('click');
  await flush();
  assert.deepEqual(posts[2], { url: '/api/account/slots/switch', body: { conversation_id: 'conv-1', slot_id: 'team-a', mode: 'now' } });

  // SSE account-changed triggers refresh of /api/account/status.
  const statusBefore = env.calls.filter(c => c.url === '/api/account/status').length;
  env.doc.events['ws-account-changed']();
  await flush();
  assert.ok(env.calls.filter(c => c.url === '/api/account/status').length > statusBefore);
  pop.close();
});

test('Phase 3 inactive-slot hover preview renders cached age, dims active baseline, shows no-cache callout, restores on leave, with zero fetch and zero SVG re-render', async () => {
  const now = Date.UTC(2026, 10, 28, 12, 0);
  const reset5h = now + 2 * 3600000, reset7d = now + 3 * 86400000;
  const cachedAt = now - 12 * 60000;
  const env = environment(now, 'ko');
  let accountStatus = {
    loopback_peer: true,
    slots: {
      claude: [
        { id: 'default', harness: 'claude', label: 'Default', implicit: true, active: true },
        { id: 'work', harness: 'claude', label: 'Work', config_dir: '/tmp/claude-work', active: false },
        { id: 'personal', harness: 'claude', label: 'Personal', config_dir: '/tmp/claude-personal', active: false }
      ]
    },
    conversations: []
  };
  env.set({
    entries: [
      { harness: 'claude', source: 'Claude push', asOf: now, windows: [
        { kind: 'five_hour', percent: 38, asOf: now, resetsAt: reset5h },
        { kind: 'seven_day', percent: 54, asOf: now, resetsAt: reset7d }
      ] }
    ],
    history: {
      schema_version: 1, status: 'ok', samples: [
        { harness: 'claude', kind: 'five_hour', source: 'Claude push', resetsAt: reset5h, asOf: now - 3600000, percent: 18, slotId: 'default' },
        { harness: 'claude', kind: 'five_hour', source: 'Claude push', resetsAt: reset5h, asOf: now, percent: 38, slotId: 'default' }
      ]
    },
    slotPreviews: {
      claude: {
        work: {
          asOf: cachedAt,
          windows: [
            { kind: 'five_hour', percent: 71, asOf: cachedAt, resetsAt: reset5h },
            { kind: 'seven_day', percent: 33, asOf: cachedAt, resetsAt: reset7d }
          ],
          samples: [
            { harness: 'claude', kind: 'five_hour', source: 'Claude push', resetsAt: reset5h, asOf: cachedAt - 3600000, percent: 45, slotId: 'work' },
            { harness: 'claude', kind: 'five_hour', source: 'Claude push', resetsAt: reset5h, asOf: cachedAt, percent: 71, slotId: 'work' }
          ]
        }
      }
    }
  });
  const baseFetch = env.window.fetch;
  env.window.fetch = (url, options = {}) => {
    if (url === '/api/account/status') {
      env.calls.push({ url, options });
      return Promise.resolve({ ok: true, json: () => Promise.resolve(accountStatus) });
    }
    return baseFetch(url, options);
  };

  const mounted = env.window.WsUsage.mount(env.doc, env.top, { accountSwitcher: true }); await flush();
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();

  const svgsBefore = nodes(pop.el, '.usage-chart');
  const fetchCountBefore = env.calls.length;
  const claudeRow = nodes(pop.el, '.up-hr').find(r => r.attributes['data-k'] === 'claude');
  const workPill = nodes(claudeRow, '.up-sp').find(b => b.attributes['data-slot-id'] === 'work');
  const personalPill = nodes(claudeRow, '.up-sp').find(b => b.attributes['data-slot-id'] === 'personal');
  assert.ok(workPill && personalPill);

  // Hover Work (cached 12m ago)
  workPill.emit('mouseenter');
  assert.equal(env.calls.length, fetchCountBefore, 'hovering inactive slot makes zero fetch calls');
  assert.deepEqual(nodes(pop.el, '.usage-chart'), svgsBefore, 'hovering inactive slot performs zero SVG re-render');
  const activePreviews = nodes(pop.el, '.up-pser').filter(n => n.className.includes('is-preview-on'));
  assert.equal(activePreviews.length, 2, 'dashed preview curves active on both 5H and 7D');
  assert.ok(nodes(activePreviews[0], '.usage-actual')[0].attributes['stroke-dasharray']);
  const previewPins = nodes(pop.el, '.up-ppin').filter(n => n.className.includes('is-preview-on'));
  assert.equal(previewPins.length, 2);
  assert.ok(previewPins[0].textContent.includes('Cl·Work · 12분 전'), previewPins[0].textContent);
  assert.ok(nodes(pop.el, '.up-ser').every(n => n.className.includes('is-preview-base')), 'active series dimmed to faint baseline');
  const liveCells = nodes(claudeRow, '.up-nc-live');
  const workCells = nodes(claudeRow, '.up-nc-prev').filter(n => n.attributes['data-preview-slot'] === 'work');
  assert.ok(liveCells.every(c => c.hidden), 'live cells hidden during preview');
  assert.ok(workCells.every(c => !c.hidden), 'cached work cells shown during preview');
  assert.ok(workCells[0].textContent.includes('71%'));
  assert.ok(workCells[1].textContent.includes('33%'));

  // Leave Work -> restores active view cleanly
  workPill.emit('mouseleave');
  assert.equal(env.calls.length, fetchCountBefore);
  assert.ok(liveCells.every(c => !c.hidden));
  assert.ok(workCells.every(c => c.hidden));
  assert.equal(nodes(pop.el, '.up-pser').filter(n => n.className.includes('is-preview-on')).length, 0);

  // Keyboard focus on Personal (never visited -> no cache)
  personalPill.emit('focus');
  assert.equal(env.calls.length, fetchCountBefore);
  assert.deepEqual(nodes(pop.el, '.usage-chart'), svgsBefore);
  const noCacheCallouts = nodes(pop.el, '.up-nocache-callout').filter(c => !c.hidden);
  assert.equal(noCacheCallouts.length, 2);
  assert.ok(noCacheCallouts[0].textContent.includes('캐시 없음 · 클릭해 전환 후 조회'));
  const noCacheRow = nodes(claudeRow, '.up-hr-nocache').find(n => n.attributes['data-preview-slot'] === 'personal');
  assert.equal(noCacheRow.hidden, false);
  assert.ok(noCacheRow.textContent.includes('캐시 없음 · 클릭해 전환 후 조회'));

  // Blur restores active view
  personalPill.emit('blur');
  assert.equal(noCacheRow.hidden, true);
  assert.ok(liveCells.every(c => !c.hidden));

  // Background redraw while hovering clears preview so rebuilt pills cannot stick previewState
  workPill.emit('mouseenter');
  assert.equal(nodes(pop.el, '.up-pser').filter(n => n.className.includes('is-preview-on')).length, 2);
  accountStatus = {
    ...accountStatus,
    slots: {
      claude: [
        { id: 'default', harness: 'claude', label: 'Default', implicit: true, active: false },
        { id: 'work', harness: 'claude', label: 'Work', config_dir: '/tmp/claude-work', active: true },
        { id: 'personal', harness: 'claude', label: 'Personal', config_dir: '/tmp/claude-personal', active: false }
      ]
    }
  };
  env.doc.events['ws-account-changed'](); await flush();
  assert.equal(nodes(pop.el, '.up-pser').filter(n => n.className.includes('is-preview-on')).length, 0, 'redraw after slot change clears preview');
  pop.close();
});

test('pi-rpc renders compact multi-slot switcher and session rows without No curve notices or dimming quota charts', async () => {
  const now = Date.UTC(2026, 10, 28, 12, 0);
  const reset5h = now + 2 * 3600000, reset7d = now + 3 * 86400000;
  const env = environment(now, 'en');
  const switchCalls = [];
  const accountStatus = {
    loopback_peer: true,
    pi_runtime: { installed: true, version: '1.5.2', installing: false },
    slots: {
      claude: [
        { id: 'default', harness: 'claude', label: 'Default', implicit: true, active: true }
      ],
      'pi-rpc': [
        { id: 'default', harness: 'pi-rpc', label: 'Default', implicit: true, active: true },
        { id: 'work-pi', harness: 'pi-rpc', label: 'Work Pi', config_dir: '/tmp/pi-work', managed: true, active: false }
      ]
    },
    conversations: [
      { id: 'conv-pi-1', label: 'Pi Feature', backend: 'pi-rpc', account_slot_id: 'default' }
    ]
  };
  env.set({
    entries: [
      { harness: 'claude', source: 'Claude push', asOf: now, windows: [
        { kind: 'five_hour', percent: 35, asOf: now, resetsAt: reset5h },
        { kind: 'seven_day', percent: 45, asOf: now, resetsAt: reset7d }
      ] }
    ],
    history: {
      schema_version: 1, status: 'ok', samples: [
        { harness: 'claude', kind: 'five_hour', source: 'Claude push', resetsAt: reset5h, asOf: now - 3600000, percent: 15 },
        { harness: 'claude', kind: 'five_hour', source: 'Claude push', resetsAt: reset5h, asOf: now, percent: 35 }
      ]
    }
  });
  const baseFetch = env.window.fetch;
  env.window.fetch = (url, options = {}) => {
    if (url === '/api/account/status') {
      env.calls.push({ url, options });
      return Promise.resolve({ ok: true, json: () => Promise.resolve(accountStatus) });
    }
    if (url === '/api/account/slots/switch') {
      const body = JSON.parse(options.body || '{}');
      switchCalls.push(body);
      accountStatus.slots['pi-rpc'].forEach(s => { s.active = s.id === body.slot_id; });
      return Promise.resolve({ ok: true, json: () => Promise.resolve({ queued_count: 0 }) });
    }
    return baseFetch(url, options);
  };

  const mounted = env.window.WsUsage.mount(env.doc, env.top, { accountSwitcher: true }); await flush();
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();

  // Zero "No curve" notices for Pi.
  const notes = nodes(pop.el, '.up-cp-note').map(n => n.textContent);
  assert.ok(notes.every(txt => !txt.includes('Pi')), 'must not emit No curve note for pi-rpc: ' + JSON.stringify(notes));

  // Pi row exists without data-k and carries Pi badge + multi-slot pills.
  const piRow = nodes(pop.el, '.up-hr-noquota').find(r => r.attributes['data-slot-harness'] === 'pi-rpc');
  assert.ok(piRow, 'expected compact pi-rpc switcher row in Group 1');
  assert.equal(piRow.attributes['data-k'], undefined, 'pi-rpc row must omit data-k so hover/click never locks focus');
  const piBadge = piRow.querySelector('.up-hb');
  assert.equal(piBadge.attributes['data-k'], 'pi-rpc');
  assert.equal(piBadge.textContent, 'Pi');

  const workPiPill = nodes(piRow, '.up-sp').find(b => b.attributes['data-slot-id'] === 'work-pi');
  assert.ok(workPiPill, 'expected Work Pi slot pill');

  // Hovering/focusing inactive Pi pill or hovering/clicking Pi row must not dim Claude charts/rows.
  const claudeRow = nodes(pop.el, '.up-hr').find(r => r.attributes['data-k'] === 'claude');
  workPiPill.emit('mouseenter');
  workPiPill.emit('focus');
  pop.el.emit('mouseover', { target: piRow });
  pop.el.emit('click', { target: piRow });
  assert.ok(!claudeRow.className.includes('is-dim'), 'Pi hover/focus must not dim Claude row');
  assert.ok(nodes(pop.el, '.up-ser').every(n => !n.className.includes('is-dim') && !n.className.includes('is-preview-base')),
    'Pi hover/focus must not dim Claude chart series');

  // Clicking Work Pi switches the pi-rpc slot via POST /api/account/slots/switch.
  workPiPill.emit('click', { stopPropagation() {} }); await flush();
  assert.equal(switchCalls.length, 1);
  assert.deepEqual(switchCalls[0], { harness: 'pi-rpc', slot_id: 'work-pi' });

  // Active session row for pi-rpc resolves the Pi badge via defForHarness.
  const sessToggle = pop.el.querySelector('.up-sess-hd');
  assert.ok(sessToggle, 'expected active sessions toggle for multi-slot pi-rpc session');
  sessToggle.emit('click', { stopPropagation() {} });
  const piSessRow = nodes(pop.el, '.up-srow').find(r => r.attributes['data-conv-id'] === 'conv-pi-1');
  assert.ok(piSessRow, 'expected pi-rpc session row');
  const sessBadge = piSessRow.querySelector('.up-hb');
  assert.equal(sessBadge.attributes['data-k'], 'pi-rpc');
  assert.equal(sessBadge.textContent, 'Pi');
  pop.close();
});
