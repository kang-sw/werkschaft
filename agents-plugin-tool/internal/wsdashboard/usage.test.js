const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function environment(now = Date.UTC(2026, 10, 15), locale = 'en') {
  class Element {
    constructor(tag) { this.tagName = tag; this.children = []; this.style = {}; this.attributes = {}; this.events = {}; this._text = ''; }
    set textContent(value) { this._text = value; this.children = []; }
    get textContent() { return this._text + this.children.map(c => c.textContent).join(''); }
    appendChild(child) { this.children.push(child); return child; }
    setAttribute(key, value) { this.attributes[key] = value; if (key === 'class') this.className = value; }
    addEventListener(key, fn) { this.events[key] = fn; }
    focus() { this.focused = true; }
    querySelector(selector) {
      for (const child of this.children) {
        if ((child.className || '').split(' ').includes(selector.slice(1))) return child;
        const found = child.querySelector(selector); if (found) return found;
      }
      return null;
    }
  }
  // The header's center cluster (the tcen template): the usage handle and
  // the global alert slot beside it.
  const button = new Element('button'); button.className = 'usage-toggle';
  const alert = new Element('span'); alert.className = 'talert'; alert.textContent = 'Alert';
  const center = new Element('span'); center.className = 'tcen'; center.appendChild(button); center.appendChild(alert);
  const top = new Element('div'); top.className = 'top'; top.appendChild(center);
  const doc = { hidden: false, events: {}, createElement: tag => new Element(tag), createElementNS: (_, tag) => new Element(tag), addEventListener(key, fn) { this.events[key] = fn; } };
  const calls = [], ticks = [], timers = [];
  let payload = { entries: [], warnPercent: 80, critPercent: 95 }, fail = false;
  const window = { setInterval(fn, ms) { ticks.push({ fn, ms }); }, setTimeout(fn, ms) { timers.push({ fn, ms, live: true }); return timers.length; }, clearTimeout(id) { if (timers[id - 1]) timers[id - 1].live = false; }, fetch(url, options) { calls.push({ url, options }); return fail ? Promise.reject(new Error('fake failure')) : Promise.resolve({ ok: true, json: () => Promise.resolve(payload) }); } };
  window.wsI18n = { lang: locale, t: JSON.parse(fs.readFileSync(__dirname + '/../wsi18n/tables/' + locale + '.json', 'utf8')) };
  window.wsT = function (key, ...args) {
    assert.ok(Object.hasOwn(window.wsI18n.t, key), key);
    return window.wsI18n.t[key].replace(/\{(\d+)\}/g, (match, i) => i < args.length ? String(args[i]) : match);
  };
  class Clock extends Date { static now() { return now; } }
  vm.runInNewContext(fs.readFileSync(__dirname + '/assets/usage.js', 'utf8'), { window, Date: Clock });
  return { window, top, doc, button, alert, center, calls, ticks, timers, now(value) { now = value; }, set(value) { payload = value; }, fail() { fail = true; } };
}
const flush = () => new Promise(resolve => setImmediate(resolve));
// groups are the handle's harness groups, after its compact trigger.
const groups = button => button.children.filter(c => c.className === 'usage-harness');
function nodes(root, tagOrClass) {
  return root.children.flatMap(child => [
    ...(tagOrClass.startsWith('.') ? (child.className || '').split(' ').includes(tagOrClass.slice(1)) : child.tagName === tagOrClass) ? [child] : [],
    ...nodes(child, tagOrClass)
  ]);
}
async function chartFixture({ history = 'ok', resetKnown = true, stale = false, percents = [10, 60], weekly = false, locale = 'en' } = {}) {
  const start = Date.UTC(2026, 10, 15), duration = 5 * 3600000, asOf = start + .4 * duration;
  const env = environment(asOf + (stale ? 42 * 60000 : 0), locale), resetsAt = resetKnown ? start + duration : 0;
  const samples = percents.map((percent, i) => ({ harness: 'claude', kind: 'five_hour', source: '<safe-source>', resetsAt, asOf: start + (i ? .4 : .2) * duration, percent }));
  const entry = { harness: 'claude', source: '<safe-source>', asOf, windows: [samples.at(-1)] };
  if (weekly) entry.windows.unshift({ kind: 'seven_day', percent: 99, asOf, resetsAt: asOf + 86400000 });
  const payload = { entries: [entry] };
  if (history !== 'missing') payload.history = { schema_version: 1, status: history, samples };
  env.set(payload);
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();
  return { env, pop, payload };
}

test('compact cards draw continuous paths, clipped excess and a current marker', async () => {
  const { pop } = await chartFixture({ weekly: true });
  const cards = nodes(pop.el, '.usage-window-card');
  assert.equal(cards.length, 2);
  assert.ok(cards[0].querySelector('.usage-window').textContent.startsWith('5-hour'));
  assert.ok(cards[1].querySelector('.usage-window').textContent.startsWith('Weekly'));
  const chart = cards[0].querySelector('.usage-chart');
  assert.equal(chart.attributes.viewBox, '0 0 288 113');
  assert.equal(chart.attributes.role, 'img');
  assert.match(chart.attributes['aria-label'], /not prediction/);
  assert.equal(nodes(chart, '.usage-ideal')[0].attributes['stroke-dasharray'], '4 4');
  const lines = nodes(chart, '.usage-actual');
  assert.ok(lines.length >= 1);
  assert.ok(lines.every(line => line.tagName === 'path' && line.attributes.d.startsWith('M')));
  assert.equal(nodes(chart, 'linearGradient').length, 0);
  const clip = nodes(chart, 'clipPath')[0];
  assert.equal(nodes(clip, 'polygon')[0].attributes.points, '24,88 270,12 270,0 24,0');
  for (const area of nodes(chart, '.usage-excess')) {
    assert.equal(area.attributes.fill, 'var(--wb-fail)');
    assert.equal(area.attributes['clip-path'], 'url(#' + clip.attributes.id + ')');
  }
  assert.equal(nodes(chart, '.usage-point').length, 1);
  assert.equal(nodes(chart, '.usage-point-current')[0].attributes.r, '4');
  assert.ok(!pop.el.textContent.includes('<safe-source>'));
});

test('legend and current-time guide explain the chart without advancing stale observations', async () => {
  const { env, pop } = await chartFixture({ stale: true });
  const legend = pop.el.querySelector('.usage-chart-legend');
  for (const label of ['Observed usage', 'Ideal burn, not prediction', 'Latest observation', 'No record (estimated)']) assert.ok(legend.textContent.includes(label), label);
  for (const kind of ['actual', 'ideal', 'point', 'estimated']) assert.equal(legend.querySelector('.usage-swatch-' + kind).attributes['aria-hidden'], 'true');
  const start = Date.UTC(2026, 10, 15), duration = 5 * 3600000;
  const assertGuide = now => {
    const chart = pop.el.querySelector('.usage-chart'), guide = chart.querySelector('.usage-now');
    assert.equal(nodes(chart, '.usage-point').length, 1);
    assert.equal(chart.querySelector('.usage-point-current').attributes.cx, String(24 + .4 * 246));
    assert.match(nodes(chart, '.usage-actual').at(-1).attributes.d, /L122\.4 42\.4$/);
    const x = Number(guide.attributes.x1);
    assert.ok(Math.abs(x - (24 + (now - start) / duration * 246)) < 1e-9);
    assert.equal(guide.attributes.x2, guide.attributes.x1);
    assert.equal(guide.attributes['stroke-dasharray'], '2 3');
    assert.equal(nodes(guide, 'title')[0].textContent, 'Current time; not an observation');
    assert.equal(chart.querySelector('.usage-now-label').textContent, 'Now');
  };
  assertGuide(start + .4 * duration + 42 * 60000);
  env.now(start + .8 * duration); env.ticks[0].fn(); await flush();
  assertGuide(start + .8 * duration);
  assert.equal(nodes(pop.el, '.usage-chart-legend').length, 1); // redraw never duplicates the key
  for (const now of [start - 1, start, start + duration - 1]) {
    env.now(now); env.ticks[0].fn(); await flush();
    assert.equal(nodes(pop.el, '.usage-now').length, now < start ? 0 : 1);
  }
});

test('all eleven locales localize popup labels, chart titles, accessible text and weekday dates', async () => {
  const locales = fs.readdirSync(__dirname + '/../wsi18n/tables').map(name => name.replace('.json', ''));
  assert.equal(locales.length, 11);
  for (const locale of locales) {
    const { env, pop, payload } = await chartFixture({ weekly: true, stale: true, locale });
    const t = key => env.window.wsT('js.usage.' + key.toLowerCase());
    assert.equal(pop.el.attributes['aria-label'], t('heading'));
    assert.equal(pop.el.querySelector('.usage-close').attributes['aria-label'], t('close'));
    const help = pop.el.querySelector('.usage-help');
    assert.equal(help.children[0].tagName, 'button');
    assert.equal(help.children[0].type, 'button');
    assert.equal(help.children[0].textContent, '?');
    assert.equal(help.children[0].attributes['aria-label'], t('legend'));
    assert.equal(help.children[0].attributes['aria-describedby'], help.children[1].attributes.id);
    assert.equal(help.children[1].attributes.role, 'tooltip');
    assert.equal(pop.el.children[0].children[1], help); // immediately after heading, before close
    for (const key of ['observed', 'ideal', 'latest', 'estimated']) assert.ok(help.textContent.includes(t(key)), locale + ': ' + key);
    const cards = nodes(pop.el, '.usage-window-card');
    assert.ok(cards[0].textContent.includes(t('fiveHour')));
    assert.equal(cards[0].querySelector('.usage-number').textContent, '—');
    assert.equal(cards[0].querySelector('.usage-pace'), null);
    assert.equal(cards[0].querySelector('.usage-no-report').textContent, t('noRecent5h'));
    assert.equal(cards[1].querySelector('.usage-no-report').textContent, t('noRecentWeekly'));
    assert.ok(cards[0].attributes['aria-label'].includes(t('noRecent5h')));
    assert.ok(cards[0].title.includes('60'));
    assert.ok(cards[0].title.includes(t('historyRecorded')));
    const titles = nodes(cards[0], 'title').map(n => n.textContent);
    for (const key of ['idealTitle', 'nowTitle', 'estimatedTitle']) assert.ok(titles.includes(t(key)), locale + ': ' + key);
    assert.ok(titles.some(text => text.includes(t('current')) && text.includes(t('stale'))));
    const weekly = cards[1].querySelector('.usage-chart');
    const reset = payload.entries[0].windows[0].resetsAt;
    const formatter = new Intl.DateTimeFormat(locale, { weekday: 'short' });
    const labels = nodes(weekly, '.usage-day-label');
    assert.ok(labels.length >= 3, locale);
    for (const label of labels) {
      const x = Number(label.attributes.x);
      const at = reset - 7 * 86400000 + (x - 24) / 246 * 7 * 86400000;
      assert.equal(label.textContent, formatter.format(new Date(at)), locale);
      assert.equal(label.attributes['text-anchor'], 'middle');
      assert.ok(x > 24 && x < 270);
    }
    assert.equal(nodes(weekly, '.usage-day-grid').length, 7);
    assert.equal(nodes(cards[0], '.usage-day-grid').length, 0);
    assert.equal(nodes(cards[0], '.usage-day-label').length, 0);
    assert.doesNotMatch(pop.el.textContent, /js\.usage\.|<safe-source>/);
  }
  const css = fs.readFileSync(__dirname + '/assets/usage.css', 'utf8');
  assert.match(css, /\.usage-chart-legend \{[^}]*display: none/);
  assert.match(css, /\.usage-help:hover \.usage-chart-legend, \.usage-help:focus-within \.usage-chart-legend \{ display: grid; \}/);
});

test('baseline equality is muted, excess is clipped to above baseline, and short intervals are solid', async () => {
  for (const percents of [[20, 40], [10, 20], [40, 60]]) {
    const { env, pop, payload } = await chartFixture({ percents });
    const chart = pop.el.querySelector('.usage-chart');
    const clip = nodes(chart, 'clipPath')[0];
    assert.ok(nodes(chart, '.usage-excess').every(area => area.attributes['clip-path'] === 'url(#' + clip.attributes.id + ')'));
    if (percents[0] === 20) assert.ok(nodes(chart, '.usage-actual').every(line => line.attributes.stroke === 'var(--wb-muted-fg)'));
    payload.history.samples[0].asOf = payload.entries[0].asOf - 600000;
    env.ticks[0].fn(); await flush();
    assert.ok(nodes(pop.el, '.usage-actual').every(n => !n.attributes['stroke-dasharray']));
    assert.ok(nodes(pop.el, '.usage-excess').length > 0);
    assert.ok(nodes(pop.el, '.usage-excess').every(area => area.attributes['clip-path']));
  }
});

test('missing, empty and unavailable history are truthful current-only fallbacks', async () => {
  for (const history of ['missing', 'empty', 'unavailable']) {
    const { pop } = await chartFixture({ history });
    assert.equal(nodes(pop.el, '.usage-point').length, 1);
    assert.equal(nodes(pop.el, '.usage-actual').length, 0);
    assert.equal(nodes(pop.el, '.usage-excess').length, 0);
    assert.equal(nodes(pop.el, '.usage-ideal').length, 1);
    assert.equal(pop.el.querySelector('.usage-history-state'), null);
    const card = pop.el.querySelector('.usage-window-card');
    assert.match(card.title, /current observation only/);
    assert.match(card.title, history === 'empty' ? /No history recorded/ : /History unavailable/);
    assert.doesNotMatch(pop.el.textContent, /History unavailable|No history recorded|current observation only/);
  }
});

test('unknown resets show only the current dot without baseline or connected lines', async () => {
  const { pop } = await chartFixture({ resetKnown: false });
  assert.equal(nodes(pop.el, '.usage-point').length, 1);
  for (const selector of ['.usage-ideal', '.usage-actual', '.usage-now', '.usage-now-label', 'linearGradient', '.usage-excess']) assert.equal(nodes(pop.el, selector).length, 0);
  assert.equal(pop.el.querySelector('.usage-reset').textContent, '↻ unknown');
  assert.match(pop.el.querySelector('.usage-reset').title, /Resets unknown/);
  assert.match(pop.el.querySelector('.usage-window-card').title, /reset unknown, isolated observations only/);
});

test('clip-path ids remain unique across cards, redraws and repeated mounts', async () => {
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

test('stale cards replace the figure with em-dash, hide pace, date the last reading in tooltip, and neutralize the current run', async () => {
  const { env, pop } = await chartFixture({ stale: true, percents: [10, 20] });
  const card = pop.el.querySelector('.usage-window-card');
  assert.ok(card.className.includes('usage-stale'));
  assert.ok(card.querySelector('.usage-clock'));
  assert.equal(card.querySelector('.usage-number').textContent, '—');
  assert.equal(card.querySelector('.usage-pace'), null);
  assert.equal(card.querySelector('.usage-no-report').textContent, 'No recent 5-hour report');
  assert.doesNotMatch(card.querySelector('.usage-window').textContent, /20%/);
  assert.match(card.title, /last 20% at /);
  assert.match(card.attributes['aria-label'], /No recent 5-hour report/);
  assert.match(card.attributes['aria-label'], /last 20% at /);
  assert.equal(card.querySelector('.usage-point-current').attributes.fill, 'var(--wb-muted-fg)');
  assert.ok(nodes(card, '.usage-actual').every(line => line.attributes.stroke === 'var(--wb-muted-fg)'));
  env.now(Date.UTC(2026, 10, 15) + 3 * 3600000); env.ticks[0].fn(); await flush();
  assert.equal(pop.el.querySelector('.usage-number').textContent, '—');
  assert.equal(pop.el.querySelector('.usage-pace'), null);
  const css = fs.readFileSync(__dirname + '/assets/usage.css', 'utf8');
  assert.match(css, /\.usage-pop \{[^}]*width: min\(988px, calc\(100vw - 16px\)\)/);
  assert.match(css, /\.usage-stale, \.usage-stale \.usage-pace \{ color: var\(--wb-muted-fg\); \}/);
  assert.match(css, /\.usage-no-report \{[^}]*color: var\(--wb-muted-fg\);/);
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

test('footer and popover show detected harnesses, age, reset and safe text but hide source', async () => {
  const at = Date.UTC(2026, 10, 15), env = environment(at), reset = at + 3600000;
  env.set({ warnPercent: 80, critPercent: 95, entries: [
    { harness: 'claude', plan: 'max', source: 'Claude push', asOf: at, windows: [{ kind: 'five_hour', percent: 35, resetsAt: reset, asOf: at }, { kind: 'seven_day', percent: 82, resetsAt: reset, asOf: at }] },
    { harness: 'codex', plan: '<fake-plan>', source: 'via Pi', status: 'Pi token expired - open Pi to refresh', asOf: at, windows: [{ kind: 'seven_day', percent: 96, resetsAt: reset, asOf: at }] }
  ] });
  const mounted = env.window.WsUsage.mount(env.doc, env.top);
  await flush();
  assert.equal(groups(env.button).length, 2);
  assert.equal(groups(env.button)[0].querySelector('.usage-columns').children.length, 2); // C: 5h, week
  assert.equal(groups(env.button)[1].querySelector('.usage-columns').children.length, 1); // X: week
  assert.equal(groups(env.button)[1].querySelector('.usage-number').textContent, '96%');
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); pop.focus(); await flush();
  const text = pop.el.textContent;
  assert.ok(text.indexOf('Claude') < text.indexOf('Codex / GPT'));
  for (const s of ['96%', 'Weekly', '5-hour', '0m ago', '<fake-plan>', 'Pi token expired']) assert.ok(text.includes(s), s);
  const resetEl = pop.el.querySelector('.usage-reset');
  assert.ok(resetEl.textContent.startsWith('↻ '));
  assert.ok(resetEl.title.startsWith('Resets '));
  for (const source of ['via Pi', 'Claude push']) assert.ok(!text.includes(source), source);
  assert.equal(pop.el.attributes.role, 'dialog');
  assert.equal(pop.el.focused, true);
  assert.equal(env.calls[1].url, '/rail/usage?history=1&refresh=1');
  assert.equal(env.calls[0].url, '/rail/usage?history=1');
  assert.equal(env.calls[1].options.headers['X-WS-Usage-Refresh'], '1');
  assert.equal(env.calls[0].options.credentials, 'same-origin');
  env.button.textContent = ''; mounted.sync(); assert.equal(groups(env.button).length, 2);
  env.fail(); env.ticks[0].fn(); await flush();
  assert.ok(pop.el.textContent.includes('usage unavailable'));
  assert.ok(pop.el.textContent.includes('96%')); // retained last value
  pop.close();
});

test('fractional percentages round like the server and pace, in footer, title and popover', async () => {
  const at = Date.UTC(2026, 10, 15), env = environment(at), resetsAt = at + 3600000;
  const cases = [[59.6, '60'], [59.000000001, '59'], [28.999999999999996, '29'], [59.4, '59']];
  const mounted = env.window.WsUsage.mount(env.doc, env.top);
  const pop = mounted.makePopover(() => {}, () => {}); pop.open();
  for (const [percent, want] of cases) {
    env.set({ entries: [{ harness: 'codex', asOf: at, windows: [{ kind: 'seven_day', percent, resetsAt, asOf: at }] }] });
    env.ticks[0].fn(); await flush();
    assert.equal(env.button.querySelector('.usage-number').textContent, want + '%', String(percent));
    assert.ok(env.button.querySelector('.usage-column').title.includes('Weekly: ' + want + '%;'), String(percent));
    assert.ok(pop.el.querySelector('.usage-window').textContent.includes(want + '%'), String(percent));
  }
  pop.close();
});

test('footer matches normal, over-pace and weekly-only reference columns', async () => {
  const at = Date.UTC(2026, 10, 15), env = environment(at);
  const w = (kind, percent, elapsed) => ({ kind, percent, asOf: at, resetsAt: at + (1 - elapsed) * (kind === 'five_hour' ? 5 * 3600000 : 7 * 86400000) });
  const entries = [
    { harness: 'claude', windows: [w('seven_day', 53, .66), w('five_hour', 20, 1 / 3)] },
    { harness: 'codex', windows: [w('seven_day', 38, .45)] }
  ];
  env.set({ entries });
  env.window.WsUsage.mount(env.doc, env.top); await flush();
  const columns = groups(env.button).map(g => g.querySelector('.usage-columns').children);
  assert.deepEqual(columns.map(c => c.length), [2, 1]);
  assert.deepEqual(columns.flatMap(c => c.map(v => v.querySelector('.usage-key').textContent)), ['5h', 'wk', 'wk']);
  assert.deepEqual([env.window.wsT('js.usage.key5h'), env.window.wsT('js.usage.keyweekly')], ['5h', 'wk']); // en locale: English short keys
  assert.deepEqual(columns.flatMap(c => c.map(v => v.querySelector('.usage-number').textContent)), ['20%', '53%', '38%']);
  assert.deepEqual(columns.flatMap(c => c.map(v => v.querySelector('.usage-pace').textContent)), ['−13%', '−13%', '−7%']);
  assert.ok(columns[0][0].querySelector('.under'));
  entries[0].windows[1].percent = 64;
  env.ticks[0].fn(); await flush();
  assert.equal(groups(env.button)[0].querySelector('.over').textContent, '+31%');
  env.set({ entries: [entries[1]] }); env.ticks[0].fn(); await flush();
  assert.equal(groups(env.button).length, 1);
  assert.equal(env.button.querySelector('.usage-columns').children.length, 1);
  assert.equal(env.button.querySelector('.hb').attributes['data-h'], 'codex');
});

test('stale boundary is strictly after 30 minutes, per window, switching stale columns to em-dash with dated tooltip', async () => {
  const at = Date.UTC(2026, 10, 15), env = environment(at + 30 * 60000);
  const entry = { harness: 'claude', asOf: at + 30 * 60000, windows: [
    { kind: 'five_hour', percent: 20, asOf: at, resetsAt: at + 5 * 3600000 * 2 / 3 },
    { kind: 'seven_day', percent: 53, resetsAt: at + 7 * 86400000 * .34 }
  ] };
  env.set({ entries: [entry] });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.equal(env.button.querySelector('.usage-stale'), null);
  assert.equal(env.button.querySelector('.usage-number').textContent, '20%');
  assert.equal(env.button.querySelector('.usage-pace').textContent, '−23%');
  env.now(at + 30 * 60000 + 1); mounted.sync();
  const staleCol = env.button.querySelector('.usage-stale');
  assert.equal(staleCol.className, 'usage-column usage-stale');
  assert.equal(staleCol.querySelector('.usage-number').textContent, '20%');
  assert.equal(staleCol.querySelector('.usage-percent').textContent, '%');
  assert.equal(staleCol.querySelector('.usage-pace'), null);
  assert.ok(env.button.querySelector('.usage-clock'));
  assert.match(staleCol.attributes['aria-label'], /No recent 5-hour report/);
  assert.match(staleCol.attributes['aria-label'], /last 20% at /);
  assert.equal(env.button.querySelector('.usage-columns').children[1].className, 'usage-column');
  assert.equal(env.button.querySelector('.usage-columns').children[1].querySelector('.usage-number').textContent, '53%');
  env.now(at + 42 * 60000); mounted.sync();
  assert.equal(env.button.querySelector('.usage-stale').querySelector('.usage-number').textContent, '20%');
  assert.equal(env.button.querySelector('.usage-stale').querySelector('.usage-pace'), null);
  env.now(at + 55 * 60000); mounted.sync();
  assert.equal(env.button.querySelector('.usage-stale').querySelector('.usage-number').textContent, '20%');
  // Stale positive-pace values retain the observed value without pace or over tone.
  entry.windows[0].percent = 64; mounted.sync();
  assert.equal(env.button.querySelector('.usage-stale').querySelector('.usage-number').textContent, '64%');
  assert.equal(env.button.querySelector('.usage-stale').querySelector('.usage-pace'), null);
  assert.equal(env.button.querySelector('.usage-stale').querySelector('.over'), null);
  assert.match(env.button.querySelector('.usage-stale').title, /last 64% at /);
  const css = fs.readFileSync(__dirname + '/assets/usage.css', 'utf8');
  assert.match(css, /\.usage-stale, \.usage-stale \.usage-pace \{ color: var\(--wb-muted-fg\); \}/);
});

test('missing and expired windows never create placeholder columns or fabricated pace', async () => {
  const at = Date.UTC(2026, 10, 15), env = environment(at);
  const entry = { harness: 'claude', windows: [
    { kind: 'five_hour', percent: 99, resetsAt: at, asOf: at },
    { kind: 'seven_day', percent: 38, asOf: at }
  ] };
  env.set({ entries: [entry] });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.equal(env.button.querySelector('.usage-columns').children.length, 1);
  assert.equal(env.button.querySelector('.usage-number').textContent, '38%');
  assert.equal(env.button.querySelector('.usage-pace').textContent, '—');
  assert.ok(env.button.querySelector('.usage-column').attributes['aria-label'].includes('pace unavailable'));
  entry.windows[1].resetsAt = at + 7 * 86400000;
  delete entry.windows[1].asOf; mounted.sync();
  assert.equal(env.button.querySelector('.usage-number').textContent, '—');
  assert.equal(env.button.querySelector('.usage-pace'), null);
  assert.match(env.button.querySelector('.usage-column').attributes['aria-label'], /No recent weekly report/);
  entry.asOf = at; mounted.sync();
  assert.equal(env.button.querySelector('.usage-number').textContent, '38%');
  assert.equal(env.button.querySelector('.usage-pace').textContent, '+38%');
  entry.windows[1].percent = 0; mounted.sync();
  assert.equal(env.button.querySelector('.usage-number').textContent, '0%');
  assert.equal(env.button.querySelector('.usage-pace').textContent, '0%');
  assert.equal(env.button.querySelector('.under'), null);
  assert.equal(env.button.querySelector('.over'), null);
  entry.windows = []; mounted.sync();
  assert.equal(env.button.querySelector('.usage-column'), null);
  assert.ok(env.button.textContent.includes('usage unavailable'));
});

test('history composes into the single coalesced viewer request', async () => {
  const env = environment();
  const mounted = env.window.WsUsage.mount(env.doc, env.top);
  const pop = mounted.makePopover(() => {}, () => {});
  pop.open(); env.ticks[0].fn(); env.doc.events.visibilitychange();
  assert.equal(env.calls.length, 1); // mount request is still pending
  assert.equal(env.calls[0].url, '/rail/usage?history=1');
  await flush();
  assert.equal(env.calls.length, 2); // the open queues one forced request after the poll
  assert.equal(env.calls[1].url, '/rail/usage?history=1&refresh=1');
  assert.equal(env.calls[1].options.cache, 'no-store');
  pop.open(); pop.open();
  assert.equal(env.calls.length, 3); // repeated opens join this forced request
  await flush();
  assert.equal(env.calls.length, 3);
});

test('opening during an ordinary pending request queues one forced refresh and its follow-ups', async () => {
  const env = environment();
  let finish;
  env.window.fetch = (url, options) => {
    env.calls.push({ url, options });
    return new Promise(resolve => { finish = () => resolve({ ok: true, json: () => Promise.resolve({ entries: [] }) }); });
  };
  const mounted = env.window.WsUsage.mount(env.doc, env.top);
  const pop = mounted.makePopover(() => {}, () => {});
  pop.open(); pop.open();
  assert.equal(env.calls.length, 1);
  finish(); await flush();
  assert.equal(env.calls.length, 2);
  assert.equal(env.calls[1].url, '/rail/usage?history=1&refresh=1');
  assert.equal(env.calls[1].options.headers['X-WS-Usage-Refresh'], '1');
  pop.open(); // already-forced request must not queue another probe
  finish(); await flush();
  assert.equal(env.calls.length, 2);
  assert.deepEqual(env.timers.filter(t => t.live).map(t => t.ms), [2000, 11000, 32000]);
});

test('a forced refresh refetches while the popover is open, so the background probe result shows', async () => {
  const at = Date.UTC(2026, 10, 15), env = environment(at);
  const entry = percent => ({ entries: [{ harness: 'codex', asOf: at, windows: [{ kind: 'seven_day', percent, asOf: at, resetsAt: at + 86400000 }] }] });
  env.set(entry(10));
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.equal(env.timers.filter(t => [2000, 11000, 32000].includes(t.ms)).length, 0); // an ordinary poll schedules no follow-up
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();
  assert.equal(env.calls.at(-1).url, '/rail/usage?history=1&refresh=1');
  assert.deepEqual(env.timers.filter(t => t.live && [2000, 11000, 32000].includes(t.ms)).map(t => t.ms), [2000, 11000, 32000]);
  env.set(entry(42)); // the daemon's background probe lands
  env.timers.find(t => t.live && t.ms === 2000).fn(); await flush();
  assert.equal(env.calls.at(-1).url, '/rail/usage?history=1');
  assert.ok(pop.el.textContent.includes('42%'));
  assert.equal(env.button.querySelector('.usage-number').textContent, '42%');
  pop.open(); await flush(); // reopening replaces, not stacks, the follow-ups
  assert.deepEqual(env.timers.filter(t => t.live && [2000, 11000, 32000].includes(t.ms)).map(t => t.ms), [2000, 11000, 32000]);
  const before = env.calls.length;
  pop.close(); env.timers.at(-1).fn(); await flush();
  assert.equal(env.calls.length, before); // a closed popover does not refetch
});

test('nearest reset timer redraws expired windows and refreshes usage', async () => {
  const at = Date.UTC(2026, 10, 15), reset = at + 1000, env = environment(at);
  env.set({ entries: [{ harness: 'claude', asOf: at, windows: [{ kind: 'five_hour', percent: 42, asOf: at, resetsAt: reset }] }] });
  env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.equal(env.button.querySelector('.usage-number').textContent, '42%');
  const timer = env.timers.find(t => t.live && t.ms === 1100);
  assert.ok(timer, 'reset redraw includes the 100ms expiry margin');
  env.now(reset + 100); timer.fn();
  assert.equal(env.button.querySelector('.usage-number'), null);
  assert.ok(env.button.textContent.includes('usage unavailable'));
  await flush();
  assert.equal(env.calls.length, 2);
  assert.equal(env.calls[1].url, '/rail/usage?history=1');
});

test('popover header age uses the oldest displayed window observation', async () => {
  const now = Date.UTC(2026, 10, 15, 12), env = environment(now);
  env.set({ entries: [{ harness: 'claude', asOf: now - 60000, windows: [
    { kind: 'five_hour', percent: 20, asOf: now - 12 * 60000, resetsAt: now + 3600000 },
    { kind: 'seven_day', percent: 30, asOf: now - 3 * 60000, resetsAt: now + 86400000 }
  ] }] });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();
  assert.equal(pop.el.querySelector('.usage-vage').textContent, '12m ago');
  assert.equal(pop.el.querySelector('.usage-vage').title, 'value from 12 min ago');
});

test('handle redraw and sync preserve the sibling alert slot', async () => {
  const at = Date.UTC(2026, 10, 15), env = environment(at);
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  const unchanged = () => {
    assert.deepEqual(env.center.children, [env.button, env.alert]);
    assert.equal(env.alert.textContent, 'Alert');
    assert.equal(env.alert.className, 'talert');
  };
  unchanged(); // empty usage leaves the alert slot alone
  env.set({ entries: [{ harness: 'codex', asOf: at, windows: [{ kind: 'seven_day', percent: 38, asOf: at, resetsAt: at + 86400000 }] }] });
  env.ticks[0].fn(); await flush();
  assert.equal(env.button.querySelector('.usage-number').textContent, '38%');
  unchanged();
  env.button.textContent = ''; mounted.sync();
  assert.equal(env.button.querySelector('.usage-number').textContent, '38%');
  unchanged();
  env.set({ entries: [] }); env.ticks[0].fn(); await flush();
  assert.equal(env.button.textContent, 'Plan usage');
  unchanged();
});

test('the header handle keeps one row: small badge marks clear of the columns, a compact trigger when short', () => {
  const css = fs.readFileSync(__dirname + '/assets/usage.css', 'utf8');
  const rule = selector => {
    const start = css.indexOf('\n' + selector + ' {') + 1;
    assert.ok(start > 0, selector);
    return css.slice(start, css.indexOf('}', start) + 1);
  };
  // Plan usage left the rail footer for the header's center.
  assert.doesNotMatch(css, /\.rail \.(rfoot|usage-toggle)/);
  for (const selector of ['.usage-harness', '.usage-columns']) assert.match(rule(selector), /flex-wrap: nowrap/);
  const mark = rule('.usage-mark');
  for (const part of ['width: 19px', 'height: 16px', 'margin-right: 3px', 'flex: none']) assert.ok(mark.includes(part), part);
  const clock = rule('.usage-clock');
  for (const part of ['right: -4px', 'bottom: -3px']) assert.ok(clock.includes(part), part);
  const column = rule('.usage-column');
  assert.match(column, /display: inline-flex/);
  assert.match(column, /flex-direction: column/); // current % above pace delta
  assert.match(rule('.tcen .usage-number'), /font-size: 10\.5px/);
  assert.match(rule('.tcen .usage-pace'), /font-size: 9\.5px/);
  assert.match(rule('.usage-key'), /9px\/1\.2/);
  // Where the header runs short (tu) only the compact trigger shows.
  assert.match(rule('.usage-mini'), /display: none/);
  assert.match(rule('.dwrap > .top.tu .tcen > .usage-toggle'), /width: 34px/);
  assert.match(rule('.dwrap > .top.tu .tcen > .usage-toggle > :not(.usage-mini)'), /display: none/);
  assert.match(rule('.dwrap > .top.tu .tcen > .usage-toggle > .usage-mini'), /display: inline-flex/);
  const handleCss = css.slice(0, css.indexOf('.rpop.usage-pop'));
  assert.doesNotMatch(handleCss, /overflow:\s*hidden|text-overflow:\s*ellipsis/);
});

test('the handle marks each harness with the small badge and leads with the compact trigger', async () => {
  const at = Date.UTC(2026, 10, 15), env = environment(at);
  env.set({ entries: [
    { harness: 'claude', asOf: at - 42 * 60000, windows: [{ kind: 'five_hour', percent: 26, asOf: at - 42 * 60000, resetsAt: at + 3600000 }, { kind: 'seven_day', percent: 53, asOf: at - 42 * 60000, resetsAt: at + 86400000 }] },
    { harness: 'codex', asOf: at, windows: [{ kind: 'seven_day', percent: 79, asOf: at, resetsAt: at + 86400000 }] }] });
  env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.equal(env.button.children[0].className, 'usage-mini');
  const marks = nodes(env.button, '.usage-mark');
  assert.equal(marks.length, 2);
  assert.deepEqual(marks.map(m => [m.children[0].className, m.children[0].attributes['data-h']]), [['hb sm', 'claude'], ['hb sm', 'codex']]);
  assert.equal(nodes(marks[0], 'svg').length, 1); // the stale clock rides the stale harness's badge
  assert.equal(nodes(marks[1], 'svg').length, 0);
});

test('a reply without the account list draws the empty handle, never throws', async () => {
  const env = environment();
  env.set({});
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.equal(env.button.textContent, 'Plan usage');
  mounted.sync();
  assert.equal(env.button.textContent, 'Plan usage');
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

test('agy renders compactly when single, shows group labels without footer age when dual, and keeps popover intact', async () => {
  const at = Date.UTC(2026, 10, 19, 12, 0), observed = at - 12 * 60000, env = environment(at);
  const reset5h = observed + 3 * 3600000, resetWk = observed + 4 * 86400000;
  const geminiEntry = { harness: 'agy', group: 'gemini', label: 'Gemini Models', source: 'via agy CLI', asOf: observed, windows: [
    { kind: 'five_hour', percent: 40, asOf: observed, resetsAt: reset5h },
    { kind: 'seven_day', percent: 55, asOf: observed, resetsAt: resetWk }
  ] };
  const claudeGptEntry = { harness: 'agy', group: 'claude_gpt', label: 'Claude and GPT models', source: 'via agy CLI', asOf: observed, windows: [
    { kind: 'five_hour', percent: 80, asOf: observed, resetsAt: reset5h, disabled: true },
    { kind: 'seven_day', percent: 30, asOf: observed, resetsAt: resetWk }
  ] };
  env.set({
    entries: [
      { harness: 'claude', asOf: at, windows: [{ kind: 'five_hour', percent: 25, asOf: at, resetsAt: reset5h }] },
      geminiEntry,
      claudeGptEntry
    ],
    agyHistory: { schema_version: 1, status: 'ok', samples: [
      { harness: 'agy', group: 'gemini', kind: 'five_hour', source: 'via agy CLI', resetsAt: reset5h, asOf: observed - 20 * 60000, percent: 15 },
      { harness: 'agy', group: 'gemini', kind: 'five_hour', source: 'via agy CLI', resetsAt: reset5h, asOf: observed, percent: 40 },
      { harness: 'agy', group: 'claude_gpt', kind: 'seven_day', source: 'via agy CLI', resetsAt: resetWk, asOf: observed - 3600000, percent: 10 },
      { harness: 'agy', group: 'claude_gpt', kind: 'seven_day', source: 'via agy CLI', resetsAt: resetWk, asOf: observed, percent: 30 }
    ] }
  });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  const footerGroups = groups(env.button);
  assert.equal(footerGroups.length, 3);
  const agyFooter = footerGroups.slice(1);
  assert.deepEqual(agyFooter.map(g => g.querySelector('.hb').attributes['data-h']), ['agy', 'agy']);
  assert.deepEqual(nodes(env.button, '.usage-group-label').map(n => n.textContent), ['Gemini', 'Claude/GPT']);
  assert.equal(nodes(env.button, '.usage-group').length, 0);
  assert.equal(nodes(env.button, '.usage-group-age').length, 0);
  assert.equal(agyFooter[0].querySelector('.usage-columns').children.length, 2);
  assert.equal(agyFooter[1].querySelector('.usage-columns').children.length, 1);
  assert.equal(agyFooter[1].querySelector('.usage-number').textContent, '30%');
  assert.match(agyFooter[0].querySelector('.usage-column').title, /value from 12 min ago/);

  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();
  const sections = nodes(pop.el, '.usage-account');
  assert.equal(sections.length, 3);
  const titles = sections.map(s => s.querySelector('.usage-heading').textContent);
  assert.ok(titles.includes('Gemini Models'));
  assert.ok(titles.includes('Claude and GPT models'));
  assert.equal(sections.filter(s => s.querySelector('.hb').attributes['data-h'] === 'agy').length, 2);
  const cgSection = sections.find(s => s.querySelector('.usage-heading').textContent === 'Claude and GPT models');
  assert.equal(cgSection.querySelector('.usage-vage').textContent, '12m ago');
  assert.equal(cgSection.querySelector('.usage-vage').title, 'value from 12 min ago');
  const cgCards = nodes(cgSection, '.usage-window-card');
  assert.equal(cgCards.length, 2);
  assert.ok(cgCards[0].className.includes('usage-disabled'));
  assert.equal(cgCards[0].querySelector('.usage-disabled-tag').textContent, 'disabled');
  assert.ok(!cgCards[1].className.includes('usage-disabled'));
  const gemSection = sections.find(s => s.querySelector('.usage-heading').textContent === 'Gemini Models');
  assert.equal(gemSection.querySelector('.usage-vage').textContent, '12m ago');
  assert.ok(nodes(nodes(gemSection, '.usage-window-card')[0], '.usage-actual').length >= 1);
  assert.equal(nodes(cgCards[0], '.usage-actual').length, 0);

  // Single rendered agy group (when claude_gpt has all windows disabled): omits label & age.
  env.set({ entries: [
    { harness: 'claude', asOf: at, windows: [{ kind: 'five_hour', percent: 25, asOf: at, resetsAt: reset5h }] },
    geminiEntry,
    { harness: 'agy', group: 'claude_gpt', label: 'Claude and GPT models', source: 'via agy CLI', asOf: observed, windows: [
      { kind: 'five_hour', percent: 80, asOf: observed, resetsAt: reset5h, disabled: true },
      { kind: 'seven_day', percent: 30, asOf: observed, resetsAt: resetWk, disabled: true }
    ] }
  ] });
  env.ticks[0].fn(); await flush();
  assert.equal(groups(env.button).length, 2);
  assert.equal(nodes(env.button, '.usage-group').length, 0);
  assert.equal(nodes(env.button, '.usage-group-label').length, 0);
  assert.equal(nodes(env.button, '.usage-group-age').length, 0);
  assert.equal(groups(env.button)[1].title, 'Gemini Models');
  assert.match(groups(env.button)[1].querySelector('.usage-column').title, /value from 12 min ago/);

  // Single rendered agy group (when claude_gpt is absent): omits label & age.
  env.set({ entries: [
    { harness: 'claude', asOf: at, windows: [{ kind: 'five_hour', percent: 25, asOf: at, resetsAt: reset5h }] },
    geminiEntry
  ] });
  env.ticks[0].fn(); await flush();
  assert.equal(groups(env.button).length, 2);
  assert.equal(nodes(env.button, '.usage-group').length, 0);
  assert.equal(nodes(env.button, '.usage-group-label').length, 0);
  assert.equal(nodes(env.button, '.usage-group-age').length, 0);
  assert.equal(groups(env.button)[1].title, 'Gemini Models');

  env.set({ entries: [
    { harness: 'agy', group: 'gemini', label: 'Gemini Models', source: 'via agy CLI', asOf: observed, windows: [
      { kind: 'five_hour', percent: 40, asOf: observed, resetsAt: reset5h, disabled: true }
    ] }
  ] });
  env.ticks[0].fn(); await flush();
  assert.equal(groups(env.button).length, 0);
  assert.equal(env.button.textContent, 'Plan usage');
  pop.close();
});

test('agy sign-in required status localizes and D15 32s cache-only follow-up clears on close', async () => {
  const at = Date.UTC(2026, 10, 19, 12, 0), env = environment(at, 'en');
  env.set({ entries: [{ harness: 'agy', source: 'via agy CLI', status: 'sign-in required', windows: [] }] });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.equal(groups(env.button).length, 1);
  assert.equal(groups(env.button)[0].querySelector('.hb').attributes['data-h'], 'agy');
  assert.ok(groups(env.button)[0].textContent.includes('sign-in required'));
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();
  assert.ok(pop.el.textContent.includes('Antigravity'));
  assert.ok(pop.el.textContent.includes('sign-in required'));

  const live32 = env.timers.find(t => t.live && t.ms === 32000);
  assert.ok(live32, '32s follow-up timer scheduled');
  live32.fn(); await flush();
  assert.equal(env.calls.at(-1).url, '/rail/usage?history=1');
  assert.ok(!env.calls.at(-1).options.headers['X-WS-Usage-Refresh']);

  pop.close();
  assert.equal(env.timers.filter(t => t.live).length, 0);
  const callsAfterClose = env.calls.length;
  live32.fn(); await flush();
  assert.equal(env.calls.length, callsAfterClose);
});

test('agy pending groups reserve both chart slots and are replaced by probe data', async () => {
  const at = Date.UTC(2026, 10, 19, 12, 0), env = environment(at, 'en');
  const pending = [
    { harness: 'agy', group: 'gemini', label: 'Gemini Models', pending: true, windows: [] },
    { harness: 'agy', group: 'claude_gpt', label: 'Claude and GPT models', pending: true, windows: [] }
  ];
  env.set({ entries: pending });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  assert.equal(groups(env.button).length, 0);
  assert.equal(env.button.textContent, 'Plan usage');

  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();
  assert.equal(nodes(pop.el, '.usage-vendor-bracket')[0].textContent, 'Antigravity· 2 model groups');
  const placeholders = nodes(pop.el, '.usage-pending-card');
  assert.equal(placeholders.length, 4);
  assert.deepEqual(placeholders.map(card => card.attributes['data-kind']), ['five_hour', 'seven_day', 'five_hour', 'seven_day']);
  assert.ok(placeholders.every(card => card.attributes['aria-label'] === 'Checking usage…' && card.attributes['aria-busy'] === 'true'));
  assert.equal(nodes(pop.el, '.spin').length, 4);

  const usageWindow = (kind, percent) => ({ kind, percent, asOf: at, resetsAt: at + 3600000 });
  env.set({ entries: [
    { harness: 'agy', group: 'gemini', label: 'Gemini Models', asOf: at, windows: [usageWindow('five_hour', 30), usageWindow('seven_day', 40)] },
    { harness: 'agy', group: 'claude_gpt', label: 'Claude and GPT models', asOf: at, windows: [usageWindow('five_hour', 50), usageWindow('seven_day', 60)] }
  ] });
  const early = env.timers.find(timer => timer.live && timer.ms === 2000);
  assert.ok(early, '2s cache-only follow-up scheduled');
  early.fn(); await flush();
  assert.equal(nodes(pop.el, '.usage-pending-card').length, 0);
  assert.equal(nodes(pop.el, '.usage-window-card').length, 4);
  assert.deepEqual(nodes(pop.el, '.usage-number').map(node => node.textContent), ['30%', '40%', '50%', '60%']);

  const dashboardCss = fs.readFileSync(__dirname + '/assets/dashboard.css', 'utf8');
  assert.match(dashboardCss, /:root\[data-animation-paused\][^{]*\.spin/);
  assert.match(dashboardCss, /@media \(prefers-reduced-motion: reduce\) \{ \.spin[^}]*animation: none/);
  pop.close();
});

test('popover and gutter follow fixed vendor order with adjacent agy groups regardless of utilization', async () => {
  const at = Date.UTC(2026, 10, 19, 12, 0), reset = at + 3 * 3600000, env = environment(at);
  env.set({
    entries: [
      { harness: 'agy', group: 'claude_gpt', label: 'Claude and GPT models', source: 'via agy CLI', asOf: at, windows: [{ kind: 'five_hour', percent: 96, asOf: at, resetsAt: reset }] },
      { harness: 'codex', source: 'via Codex CLI', asOf: at, windows: [{ kind: 'seven_day', percent: 88, asOf: at, resetsAt: reset }] },
      { harness: 'agy', group: 'gemini', label: 'Gemini Models', source: 'via agy CLI', asOf: at, windows: [{ kind: 'five_hour', percent: 12, asOf: at, resetsAt: reset }] },
      { harness: 'claude', source: 'Claude push', asOf: at, windows: [{ kind: 'five_hour', percent: 5, asOf: at, resetsAt: reset }] }
    ]
  });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  const footerTitles = groups(env.button).map(g => g.title);
  assert.deepEqual(footerTitles, ['Claude', 'Codex / GPT', 'Gemini Models', 'Claude and GPT models']);
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();
  const popTitles = nodes(pop.el, '.usage-account').map(s => s.querySelector('.usage-heading').textContent);
  assert.deepEqual(popTitles, ['Claude', 'Codex / GPT', 'Gemini Models', 'Claude and GPT models']);
  pop.close();
});

test('popover uses window rows, transposed container rules and a localized missing-window slot', async () => {
  const at = Date.UTC(2026, 10, 19, 12, 0), reset = at + 3 * 3600000, env = environment(at);
  const window = (kind, percent) => ({ kind, percent, asOf: at, resetsAt: reset });
  env.set({ entries: [
    { harness: 'claude', plan: 'max', asOf: at, windows: [window('five_hour', 5), window('seven_day', 10)] },
    { harness: 'codex', plan: 'pro', asOf: at, windows: [window('seven_day', 20)] },
    { harness: 'agy', group: 'gemini', label: 'Gemini Models', asOf: at, windows: [window('five_hour', 30), window('seven_day', 40)] },
    { harness: 'agy', group: 'claude_gpt', label: 'Claude and GPT models', asOf: at, windows: [window('five_hour', 50), window('seven_day', 60)] }
  ] });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();
  assert.equal(pop.el.attributes['data-cols'], '4');
  const grid = pop.el.querySelector('.usage-details').querySelector('.usage-layout');
  assert.equal(grid.tagName, 'div');
  assert.deepEqual(nodes(pop.el, '.usage-row-label').map(n => n.textContent), ['5-hour5 h', 'Weekly7 d']);
  assert.deepEqual(nodes(pop.el, '.usage-col-head').map(n => n.textContent), ['5-hour', 'Weekly']);
  assert.equal(nodes(pop.el, '.usage-vendor-bracket')[0].textContent, 'Antigravity· 2 model groups');
  const empty = nodes(pop.el, '.usage-window-empty');
  assert.equal(empty.length, 1);
  assert.equal(empty[0].attributes['data-kind'], 'five_hour');
  assert.equal(empty[0].textContent, 'no 5-hour window');
  const css = fs.readFileSync(__dirname + '/assets/usage.css', 'utf8');
  assert.match(css, /@container usage-pop \(max-width: 956px\)/);
  assert.match(css, /@container usage-pop \(max-width: 600px\)/);
  assert.match(css, /@container usage-pop \(max-width: 336px\)/);
  pop.close();
});

test('stale 5h 0% with fresh weekly retains the value in the footer, shows em-dash in cards, and handles reset', async () => {
  const t0711 = Date.UTC(2026, 10, 22, 7, 11);
  const reset5h = Date.UTC(2026, 10, 22, 11, 0);
  const t1011 = Date.UTC(2026, 10, 22, 10, 11);
  const reset7d = Date.UTC(2026, 10, 29, 0, 0);
  const now1 = Date.UTC(2026, 10, 22, 10, 13);
  const env = environment(now1, 'en');
  env.set({
    entries: [{
      harness: 'claude', plan: 'max', source: 'Claude push', asOf: t1011,
      windows: [
        { kind: 'five_hour', percent: 0, asOf: t0711, resetsAt: reset5h },
        { kind: 'seven_day', percent: 2, asOf: t1011, resetsAt: reset7d }
      ]
    }]
  });
  const mounted = env.window.WsUsage.mount(env.doc, env.top); await flush();
  const pop = mounted.makePopover(() => {}, () => {}); pop.open(); await flush();

  // Stage 1: stale 07:11 five_hour 0% + fresh 10:11 seven_day 2%.
  const cols1 = groups(env.button)[0].querySelector('.usage-columns').children;
  assert.equal(cols1.length, 2);
  assert.ok(cols1[0].className.includes('usage-stale'));
  assert.equal(cols1[0].querySelector('.usage-number').textContent, '0%');
  assert.equal(cols1[0].querySelector('.usage-pace'), null);
  assert.match(cols1[0].title, /No recent 5-hour report/);
  assert.match(cols1[0].title, /last 0% at /);
  assert.ok(!cols1[1].className.includes('usage-stale'));
  assert.equal(cols1[1].querySelector('.usage-number').textContent, '2%');
  assert.ok(cols1[1].querySelector('.usage-pace'));

  const cards1 = nodes(pop.el, '.usage-window-card');
  assert.equal(cards1.length, 2);
  const c5 = cards1[0], c7 = cards1[1];
  assert.ok(c5.className.includes('usage-stale'));
  assert.equal(c5.querySelector('.usage-number').textContent, '—');
  assert.equal(c5.querySelector('.usage-pace'), null);
  assert.equal(c5.querySelector('.usage-no-report').textContent, 'No recent 5-hour report');
  assert.doesNotMatch(c5.querySelector('.usage-window').textContent, /0%/);
  assert.match(c5.title, /last 0% at /);
  assert.match(c5.attributes['aria-label'], /No recent 5-hour report/);
  assert.match(c5.attributes['aria-label'], /last 0% at /);
  assert.ok(!c7.className.includes('usage-stale'));
  assert.equal(c7.querySelector('.usage-number').textContent, '2%');
  assert.ok(c7.querySelector('.usage-pace'));
  assert.equal(c7.querySelector('.usage-no-report'), null);
  // Heading age excludes the stale 5h window and uses fresh weekly age (2m ago, not 3h ago).
  assert.equal(pop.el.querySelector('.usage-vage').textContent, '2m ago');
  assert.equal(pop.el.querySelector('.usage-vage').title, 'value from 2 min ago');

  // When every window is stale, heading age falls back to the oldest window.
  env.now(t1011 + 35 * 60000); env.ticks[0].fn(); await flush();
  assert.equal(pop.el.querySelector('.usage-vage').textContent, '3h ago');
  assert.equal(nodes(pop.el, '.usage-window-card')[1].querySelector('.usage-number').textContent, '—');
  assert.equal(nodes(pop.el, '.usage-window-card')[1].querySelector('.usage-no-report').textContent, 'No recent weekly report');

  // Stage 2: advance past five_hour reset with fresh weekly -> absent five_hour handled cleanly.
  const now2 = reset5h + 60000;
  env.now(now2);
  env.set({
    entries: [{
      harness: 'claude', plan: 'max', source: 'Claude push', asOf: reset5h,
      windows: [
        { kind: 'five_hour', percent: 0, asOf: t0711, resetsAt: reset5h },
        { kind: 'seven_day', percent: 2, asOf: reset5h, resetsAt: reset7d }
      ]
    }]
  });
  env.ticks[0].fn(); await flush();
  const cols2 = groups(env.button)[0].querySelector('.usage-columns').children;
  assert.equal(cols2.length, 1);
  assert.equal(cols2[0].querySelector('.usage-key').textContent, 'wk');
  assert.equal(cols2[0].querySelector('.usage-number').textContent, '2%');
  assert.equal(env.button.querySelector('.usage-clock'), null);
  const empty5 = pop.el.querySelector('.usage-window-empty');
  assert.ok(empty5);
  assert.equal(empty5.attributes['data-kind'], 'five_hour');
  assert.equal(empty5.textContent, 'no 5-hour window');
  assert.equal(nodes(pop.el, '.usage-window-card').length, 1);
  assert.equal(pop.el.querySelector('.usage-vage').textContent, '1m ago');

  // Stage 3: a fresh observed 0% five_hour report renders numeric 0% with pace (distinct from unknown).
  const now3 = now2 + 60000;
  env.now(now3);
  env.set({
    entries: [{
      harness: 'claude', plan: 'max', source: 'Claude push', asOf: now3,
      windows: [
        { kind: 'five_hour', percent: 0, asOf: now3, resetsAt: now3 + 5 * 3600000 },
        { kind: 'seven_day', percent: 2, asOf: now3, resetsAt: reset7d }
      ]
    }]
  });
  env.ticks[0].fn(); await flush();
  const cols3 = groups(env.button)[0].querySelector('.usage-columns').children;
  assert.equal(cols3.length, 2);
  assert.ok(!cols3[0].className.includes('usage-stale'));
  assert.equal(cols3[0].querySelector('.usage-number').textContent, '0%');
  assert.equal(cols3[0].querySelector('.usage-pace').textContent, '0%');
  const cards3 = nodes(pop.el, '.usage-window-card');
  assert.equal(cards3.length, 2);
  assert.ok(!cards3[0].className.includes('usage-stale'));
  assert.equal(cards3[0].querySelector('.usage-number').textContent, '0%');
  assert.equal(cards3[0].querySelector('.usage-pace').textContent, '(0%)');
  assert.equal(cards3[0].querySelector('.usage-no-report'), null);
  assert.equal(cards3[0].querySelector('.usage-clock'), null);
  assert.equal(pop.el.querySelector('.usage-vage').textContent, '0m ago');
  pop.close();

  // Korean locale sanity check for natural label.
  const koEnv = environment(now1, 'ko');
  koEnv.set({
    entries: [{
      harness: 'claude', plan: 'max', asOf: t1011,
      windows: [
        { kind: 'five_hour', percent: 0, asOf: t0711, resetsAt: reset5h },
        { kind: 'seven_day', percent: 2, asOf: t1011, resetsAt: reset7d }
      ]
    }]
  });
  const koMounted = koEnv.window.WsUsage.mount(koEnv.doc, koEnv.top); await flush();
  const koPop = koMounted.makePopover(() => {}, () => {}); koPop.open(); await flush();
  const koCard5 = nodes(koPop.el, '.usage-window-card')[0];
  assert.equal(koCard5.querySelector('.usage-number').textContent, '—');
  assert.equal(koCard5.querySelector('.usage-no-report').textContent, '최근 5시간 보고 없음');
  assert.match(koCard5.title, /마지막 0% \(/);
  koPop.close();
});

test('accountSwitcher mode renders 1-click switchers, guided Stop+Restart/queue, SSE refresh, and Manage accounts link without OAuth forms', async () => {
  const at = Date.UTC(2026, 10, 28, 9, 0), reset = at + 3 * 3600000;
  const env = environment(at, 'en');
  let accountStatus = {
    loopback_peer: true,
    openai_accounts: [
      { client_id: 'oai_a', email: 'a@example.test', signed_in: true, plan_enabled: true, active: true },
      { client_id: 'oai_b', email: 'b@example.test', signed_in: true, plan_enabled: true, active: false }
    ],
    slots: {
      codex: [
        { id: 'default', harness: 'codex', label: 'Default', implicit: true, active: true },
        { id: 'work', harness: 'codex', label: 'Work', config_dir: '/tmp/work', active: false }
      ]
    },
    conversations: [
      { id: 'conv-1', backend: 'codex', label: 'Main Lead', account_slot_id: 'default' }
    ]
  };
  const posts = [];
  env.set({ entries: [{ harness: 'codex', plan: 'pro', asOf: at, windows: [{ kind: 'five_hour', percent: 35, asOf: at, resetsAt: reset }] }] });
  const baseFetch = env.window.fetch;
  env.window.fetch = (url, options = {}) => {
    if (url === '/api/account/status') {
      env.calls.push({ url, options });
      return Promise.resolve({ ok: true, json: () => Promise.resolve(accountStatus) });
    }
    if (url.startsWith('/api/account/')) {
      const body = JSON.parse(options.body || '{}');
      posts.push({ url, body });
      if (url === '/api/account/openai/switch') {
        accountStatus = {
          ...accountStatus,
          openai_accounts: accountStatus.openai_accounts.map(a => ({ ...a, active: a.client_id === body.client_id }))
        };
        return Promise.resolve({ ok: true, json: () => Promise.resolve({ client_id: body.client_id, mode: 'immediate' }) });
      }
      if (url === '/api/account/slots/switch') {
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
          slots: { codex: accountStatus.slots.codex.map(s => ({ ...s, active: s.id === body.slot_id })) },
          conversations: [{ id: 'conv-1', backend: 'codex', label: 'Main Lead', account_slot_id: 'default', pending_account_slot: body.slot_id }]
        };
        return Promise.resolve({ ok: true, json: () => Promise.resolve({ mode: 'harness_default', queued_count: 1 }) });
      }
    }
    return baseFetch(url, options);
  };

  let openedSettingsHref = null;
  const mounted = env.window.WsUsage.mount(env.doc, env.top, {
    accountSwitcher: true,
    openSettings: href => { openedSettingsHref = href; }
  });
  await flush();
  assert.equal(nodes(env.button, '.usage-accounts').length, 0);

  const pop = mounted.makePopover(() => {}, () => {});
  pop.open();
  await flush();

  const manage = pop.el.querySelector('.usage-manage-link');
  assert.ok(manage);
  assert.equal(manage.href, '/settings?section=account');
  let prevented = false;
  manage.events.click({ preventDefault() { prevented = true; } });
  assert.equal(prevented, true);
  assert.equal(openedSettingsHref, '/settings?section=account');

  assert.equal(nodes(pop.el, 'input').length, 0);

  const werkRow = nodes(pop.el, '.usage-slot-row').find(r => r.attributes['data-slot-harness'] === 'werk');
  assert.ok(werkRow);
  const werkChips = nodes(werkRow, '.usage-slot-chip');
  assert.equal(werkChips.length, 2);
  assert.equal(werkChips[0].attributes['aria-pressed'], 'true');
  werkChips[1].events.click();
  await flush();
  assert.deepEqual(posts[0], { url: '/api/account/openai/switch', body: { client_id: 'oai_b' } });
  assert.equal(nodes(nodes(pop.el, '.usage-slot-row')[0], '.usage-slot-chip')[1].attributes['aria-pressed'], 'true');

  const convRow = pop.el.querySelector('.usage-conv-row');
  assert.ok(convRow);
  const pickBtn = nodes(convRow, '.usage-act-btn')[0];
  pickBtn.events.click();
  await flush();
  const guide = pop.el.querySelector('.usage-conv-guide');
  assert.ok(guide, 'guided Stop+Restart / queue prompt should appear');
  const guideBtns = nodes(guide, '.usage-act-btn');
  assert.equal(guideBtns.length, 3);
  guideBtns[1].events.click();
  await flush();
  assert.deepEqual(posts[1], { url: '/api/account/slots/switch', body: { conversation_id: 'conv-1', slot_id: 'work', mode: 'queue' } });
  assert.ok(nodes(pop.el, '.queued').length > 0);

  const applyNow = nodes(pop.el.querySelector('.usage-conv-row'), '.usage-act-btn')[0];
  applyNow.events.click();
  await flush();
  assert.deepEqual(posts[2], { url: '/api/account/slots/switch', body: { conversation_id: 'conv-1', slot_id: 'work', mode: 'now' } });

  const statusCallsBefore = env.calls.filter(c => c.url === '/api/account/status').length;
  const streamJs = fs.readFileSync(__dirname + '/assets/dashboard-stream.js', 'utf8');
  assert.match(streamJs, /"account-changed":\s*onAccountChanged/);
  env.doc.events['ws-account-changed']();
  await flush();
  assert.ok(env.calls.filter(c => c.url === '/api/account/status').length > statusCallsBefore);

  const css = fs.readFileSync(__dirname + '/assets/usage.css', 'utf8');
  assert.match(css, /\.usage-accounts\s*\{/);
  assert.match(css, /\.usage-slot-chip\[aria-pressed="true"\]/);
  assert.match(css, /var\(--wb-/);
  pop.close();
});

test('Phase 3 topbar switcher supports claude slots and renders agy as single-slot read-only', async () => {
  const at = Date.UTC(2026, 10, 28, 10, 0), reset = at + 3 * 3600000;
  const env = environment(at, 'en');
  let accountStatus = {
    loopback_peer: true,
    slots: {
      claude: [
        { id: 'default', harness: 'claude', label: 'Default', implicit: true, active: true },
        { id: 'work', harness: 'claude', label: 'Claude Work', config_dir: '/tmp/claude-work', active: false }
      ]
    },
    harness_auth: [
      { harness: 'agy', label: 'Antigravity', single_slot: true, available: true, probed: true, auth_required: false }
    ],
    conversations: [
      { id: 'claude-conv-1', backend: 'claude', label: 'Claude Lead', account_slot_id: 'default' }
    ]
  };
  const posts = [];
  env.set({ entries: [
    { harness: 'claude', plan: 'max', asOf: at, windows: [{ kind: 'five_hour', percent: 25, asOf: at, resetsAt: reset }] },
    { harness: 'agy', group: 'gemini', label: 'Gemini Models', asOf: at, windows: [{ kind: 'five_hour', percent: 15, asOf: at, resetsAt: reset }] }
  ] });
  const baseFetch = env.window.fetch;
  env.window.fetch = (url, options = {}) => {
    if (url === '/api/account/status') {
      env.calls.push({ url, options });
      return Promise.resolve({ ok: true, json: () => Promise.resolve(accountStatus) });
    }
    if (url === '/api/account/slots/switch') {
      const body = JSON.parse(options.body || '{}');
      posts.push({ url, body });
      accountStatus = {
        ...accountStatus,
        slots: {
          claude: accountStatus.slots.claude.map(s => ({ ...s, active: s.id === body.slot_id }))
        },
        conversations: [
          { id: 'claude-conv-1', backend: 'claude', label: 'Claude Lead', account_slot_id: 'default', pending_account_slot: body.slot_id }
        ]
      };
      return Promise.resolve({ ok: true, json: () => Promise.resolve({ mode: 'harness_default', queued_count: 1 }) });
    }
    return baseFetch(url, options);
  };

  const mounted = env.window.WsUsage.mount(env.doc, env.top, { accountSwitcher: true });
  await flush();
  const pop = mounted.makePopover(() => {}, () => {});
  pop.open();
  await flush();

  // Claude renders switchable slot chips.
  const claudeRow = nodes(pop.el, '.usage-slot-row').find(r => r.attributes['data-slot-harness'] === 'claude');
  assert.ok(claudeRow);
  const claudeChips = nodes(claudeRow, '.usage-slot-chip');
  assert.equal(claudeChips.length, 2);
  assert.equal(claudeChips[0].attributes['aria-pressed'], 'true');
  claudeChips[1].events.click();
  await flush();
  assert.deepEqual(posts[0], { url: '/api/account/slots/switch', body: { harness: 'claude', slot_id: 'work' } });
  assert.ok(nodes(pop.el, '.queued').length > 0);

  // Agy renders as read-only single-slot with zero switch buttons (D15).
  const agyRow = nodes(pop.el, '.usage-slot-row').find(r => r.attributes['data-slot-harness'] === 'agy');
  assert.ok(agyRow);
  assert.equal(agyRow.attributes['data-readonly-slot'], '1');
  assert.equal(nodes(agyRow, '.usage-slot-chip').length, 0);
  assert.equal(agyRow.querySelector('.usage-slot-readonly-badge').textContent, 'Single account');
  assert.equal(nodes(pop.el, 'input').length, 0);

  // When agy auth_required is true, the read-only badge reflects sign-in required with warn styling.
  accountStatus = {
    ...accountStatus,
    harness_auth: [
      { harness: 'agy', label: 'Antigravity', single_slot: true, available: true, probed: true, auth_required: true }
    ]
  };
  env.doc.events['ws-account-changed']();
  await flush();
  const agyRow2 = nodes(pop.el, '.usage-slot-row').find(r => r.attributes['data-slot-harness'] === 'agy');
  const warnBadge = agyRow2.querySelector('.usage-slot-readonly-badge');
  assert.equal(warnBadge.textContent, 'sign-in required');
  assert.ok(warnBadge.className.includes('warn'));
  pop.close();
});

test('OpenRouter hybrid card renders balance, key limits, spend and model table; creditsRestricted stays valid', async () => {
  const at = Date.UTC(2026, 10, 28, 12, 0);
  const env = environment(at, 'en');
  env.set({
    entries: [{
      harness: 'openrouter',
      label: 'OpenRouter',
      asOf: at,
      openrouter: {
        balance: 18.75,
        totalCredits: 25.0,
        totalUsage: 6.25,
        limit: 10.0,
        limitRemaining: 3.75,
        usageDaily: 1.25,
        usageWeekly: 4.5,
        usageMonthly: 6.25,
        totalInputTokens: 12000,
        totalOutputTokens: 3000,
        totalCostUsd: 0.42,
        models: [
          { model: 'anthropic/claude-sonnet-4', inputTokens: 10000, outputTokens: 2500, costUsd: 0.38 },
          { model: 'openai/gpt-4o-mini', inputTokens: 2000, outputTokens: 500, costUsd: 0.04 }
        ]
      }
    }]
  });
  const mounted = env.window.WsUsage.mount(env.doc, env.top);
  await flush();
  assert.equal(groups(env.button).length, 1);
  assert.equal(env.button.querySelector('.hb').attributes['data-h'], 'openrouter');
  assert.equal(env.button.querySelector('.usage-key').textContent, 'bal');
  assert.equal(env.button.querySelector('.usage-number').textContent, '$18.75');
  assert.equal(env.button.querySelector('.usage-pace').textContent, '$1.25');

  const pop = mounted.makePopover(() => {}, () => {});
  pop.open();
  await flush();
  const card = nodes(pop.el, '.usage-metric-card').find(c => c.attributes['data-harness'] === 'openrouter');
  assert.ok(card);
  for (const text of ['OpenRouter', 'Balance', '$18.75', '$6.25 / $25.00 used', 'Key limit', '$10.00', '$3.75 remaining', 'Today', '$1.25', '7-day', '$4.50', 'Monthly', 'anthropic/claude-sonnet-4', '12.5k', '$0.38']) {
    assert.ok(card.textContent.includes(text), text);
  }
  assert.ok(!card.textContent.includes('Key rejected'));

  // Restricted /credits (401/403) must omit balance and explain restriction without marking key rejected.
  env.set({
    entries: [{
      harness: 'openrouter',
      label: 'OpenRouter',
      asOf: at,
      openrouter: {
        creditsRestricted: true,
        limit: 10.0,
        limitRemaining: 6.5,
        usageDaily: 0.5,
        models: [{ model: 'openai/gpt-4o-mini', inputTokens: 500, outputTokens: 200, costUsd: 0.0025 }]
      }
    }]
  });
  env.ticks[0].fn();
  await flush();
  assert.equal(env.button.querySelector('.usage-key').textContent, 'rem');
  assert.equal(env.button.querySelector('.usage-number').textContent, '$6.50');
  const restrictedCard = nodes(pop.el, '.usage-metric-card')[0];
  assert.ok(restrictedCard.textContent.includes('Account balance unavailable for this key'));
  assert.ok(restrictedCard.textContent.includes('$6.50 remaining'));
  assert.ok(restrictedCard.textContent.includes('$0.0025'));
  assert.ok(!restrictedCard.textContent.includes('Key rejected'));

  // Key rejected status displays cleanly without fabricating balance.
  env.set({
    entries: [{
      harness: 'openrouter',
      label: 'OpenRouter',
      status: 'key rejected',
      openrouter: { models: [] }
    }]
  });
  env.ticks[0].fn();
  await flush();
  assert.ok(env.button.textContent.includes('Key rejected'));
  const rejectedCard = nodes(pop.el, '.usage-metric-card')[0];
  assert.ok(rejectedCard.textContent.includes('Key rejected'));
  pop.close();
});

test('unified codex_openai card projects codex history and renders token-first werk per-model rows', async () => {
  const start = Date.UTC(2026, 10, 28, 8, 0), duration = 5 * 3600000, at = start + 0.4 * duration;
  const reset5h = start + duration, resetWk = start + 5 * 86400000;
  const env = environment(at, 'en');
  env.set({
    entries: [{
      harness: 'codex_openai',
      label: 'Codex / OpenAI',
      accountLabel: 'al***@example.com',
      plan: 'plus',
      source: 'codex-wham',
      asOf: at,
      windows: [
        { kind: 'five_hour', percent: 45, asOf: at, resetsAt: reset5h },
        { kind: 'seven_day', percent: 30, asOf: at, resetsAt: resetWk }
      ],
      werk: {
        mode: 'unified',
        tokenFirst: true,
        totalInputTokens: 8400,
        totalOutputTokens: 1600,
        estimatedCostUsd: 0.12,
        models: [
          { model: 'gpt-5.4-codex', inputTokens: 8400, outputTokens: 1600, costUsd: 0.12 }
        ]
      }
    }],
    codexFiveHourHistory: {
      schema_version: 1,
      status: 'ok',
      samples: [
        { harness: 'codex', kind: 'five_hour', source: 'codex-wham', resetsAt: reset5h, asOf: start + 0.2 * duration, percent: 20 },
        { harness: 'codex', kind: 'five_hour', source: 'codex-wham', resetsAt: reset5h, asOf: at, percent: 45 }
      ]
    },
    history: {
      schema_version: 1,
      status: 'ok',
      samples: [
        { harness: 'codex', kind: 'seven_day', source: 'codex-wham', resetsAt: resetWk, asOf: at - 600000, percent: 25 },
        { harness: 'codex', kind: 'seven_day', source: 'codex-wham', resetsAt: resetWk, asOf: at, percent: 30 }
      ]
    }
  });
  const mounted = env.window.WsUsage.mount(env.doc, env.top);
  await flush();
  assert.equal(groups(env.button).length, 1);
  assert.equal(env.button.querySelector('.hb').attributes['data-h'], 'codex_openai');
  assert.equal(env.button.querySelector('.usage-columns').children.length, 2);

  const pop = mounted.makePopover(() => {}, () => {});
  pop.open();
  await flush();
  const sections = nodes(pop.el, '.usage-account');
  assert.equal(sections.length, 1);
  assert.equal(sections[0].querySelector('.usage-heading').textContent, 'Codex / OpenAI');
  assert.equal(sections[0].querySelector('.usage-acct-label').textContent, 'al***@example.com');
  const charts = nodes(sections[0], '.usage-chart');
  assert.equal(charts.length, 2);
  assert.ok(nodes(charts[0], '.usage-actual').length >= 1, 'codex_openai 5h chart projects codex history');
  assert.ok(nodes(charts[1], '.usage-actual').length >= 1, 'codex_openai 7d chart projects codex history');

  const detail = nodes(pop.el, '.usage-metric-card').find(c => c.attributes['data-harness'] === 'codex_openai');
  assert.ok(detail);
  for (const text of ['10k', '8.4k in · 1.6k out', 'Est. cost', 'Est. $0.12', 'gpt-5.4-codex']) {
    assert.ok(detail.textContent.includes(text), text);
  }
  pop.close();
});

test('mismatched codex and werk_openai stay separate with local fallback reasons and localize across all 11 locales', async () => {
  const at = Date.UTC(2026, 10, 28, 12, 0), reset = at + 3 * 3600000;
  const locales = fs.readdirSync(__dirname + '/../wsi18n/tables').map(name => name.replace('.json', ''));
  assert.equal(locales.length, 11);
  for (const reason of ['consent_required', 'missing_claim', 'auth_rejected', 'plan_paused', 'plan_disabled']) {
    const env = environment(at, 'en');
    env.set({
      entries: [
        { harness: 'codex', asOf: at, windows: [{ kind: 'five_hour', percent: 33, asOf: at, resetsAt: reset }] },
        {
          harness: 'werk_openai',
          label: 'Werk (OpenAI)',
          accountLabel: 'bo***@corp.io',
          asOf: at,
          werk: {
            mode: 'local',
            tokenFirst: true,
            fallbackReason: reason,
            planPaused: reason === 'plan_paused',
            totalInputTokens: 4500,
            totalOutputTokens: 500,
            models: [{ model: 'gpt-5.4', inputTokens: 4500, outputTokens: 500, costUsd: 0 }]
          }
        }
      ]
    });
    const mounted = env.window.WsUsage.mount(env.doc, env.top);
    await flush();
    assert.equal(groups(env.button).length, 2);
    assert.deepEqual(groups(env.button).map(g => g.querySelector('.hb').attributes['data-h']), ['codex', 'werk_openai']);
    assert.equal(groups(env.button)[1].querySelector('.usage-number').textContent, '5k');
    const pop = mounted.makePopover(() => {}, () => {});
    pop.open();
    await flush();
    assert.equal(nodes(pop.el, '.usage-account').length, 1, 'codex stays in quota grid');
    const werkCard = nodes(pop.el, '.usage-metric-card').find(c => c.attributes['data-harness'] === 'werk_openai');
    assert.ok(werkCard);
    assert.equal(werkCard.querySelector('.usage-acct-label').textContent, 'bo***@corp.io');
    assert.ok(werkCard.textContent.includes('gpt-5.4'));
    assert.ok(werkCard.textContent.includes('5k'));
    assert.ok(werkCard.querySelector('.usage-metric-note').textContent.length > 5, reason);
    pop.close();
  }

  for (const locale of locales) {
    const env = environment(at, locale);
    env.set({
      entries: [
        {
          harness: 'openrouter',
          asOf: at,
          openrouter: {
            balance: 12.5,
            totalCredits: 20,
            totalUsage: 7.5,
            limit: 10,
            limitRemaining: 5,
            usageDaily: 1,
            usageWeekly: 3,
            usageMonthly: 7.5,
            creditsRestricted: true,
            models: [{ model: 'openai/gpt-4o-mini', inputTokens: 1000, outputTokens: 200, costUsd: 0.01 }]
          }
        },
        {
          harness: 'werk_openai',
          accountLabel: 'bo***@corp.io',
          asOf: at,
          werk: {
            mode: 'local',
            tokenFirst: true,
            fallbackReason: 'consent_required',
            totalInputTokens: 2000,
            totalOutputTokens: 500,
            estimatedCostUsd: 0.05,
            models: [{ model: 'gpt-5.4', inputTokens: 2000, outputTokens: 500, costUsd: 0.05 }]
          }
        }
      ]
    });
    const mounted = env.window.WsUsage.mount(env.doc, env.top);
    await flush();
    const pop = mounted.makePopover(() => {}, () => {});
    pop.open();
    await flush();
    assert.doesNotMatch(pop.el.textContent, /js\.usage\./, locale);
    pop.close();
  }
});
