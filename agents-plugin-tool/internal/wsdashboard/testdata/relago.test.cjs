const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const source = readAssetSource('wsdashboard', 'dashboard.js');
const start = source.indexOf('  // --- times:');
const end = source.indexOf('  // fmtTimes formats every time', start);
const timeBlock = source.slice(start, end);

function loadTimeContext(lang, table) {
  const ctx = {
    wsI18n: { lang },
    wsT: (key, ...args) => {
      const tmpl = table[key];
      if (tmpl == null) return key;
      return tmpl.replace(/\{(\d+)\}/g, (_, i) => String(args[Number(i)]));
    },
  };
  vm.createContext(ctx);
  vm.runInContext(timeBlock, ctx);
  return ctx;
}

const EN = {
  'js.relago.now': 'just now',
  'js.relago.m': '{0}m ago',
  'js.relago.h': '{0}h ago',
  'js.relago.d': '{0}d ago',
  'js.relago.mo': '{0}mo ago',
  'js.relago.y': '{0}y ago',
};

const KO = {
  'js.relago.now': '방금 전',
  'js.relago.m': '{0}분 전',
  'js.relago.h': '{0}시간 전',
  'js.relago.d': '{0}일 전',
  'js.relago.mo': '{0}개월 전',
  'js.relago.y': '{0}년 전',
};

const SEC = 1000;
const MIN = 60 * SEC;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

test('relAgo formats always-relative ranges from <60s through multi-year', () => {
  const en = loadTimeContext('en', EN);
  assert.equal(en.relAgo(-5000), 'just now');
  assert.equal(en.relAgo(0), 'just now');
  assert.equal(en.relAgo(59 * SEC), 'just now');
  assert.equal(en.relAgo(60 * SEC), '1m ago');
  assert.equal(en.relAgo(59 * MIN + 59 * SEC), '59m ago');
  assert.equal(en.relAgo(HOUR), '1h ago');
  assert.equal(en.relAgo(23 * HOUR + 59 * MIN), '23h ago');
  assert.equal(en.relAgo(DAY), '1d ago');
  assert.equal(en.relAgo(29 * DAY), '29d ago');
  assert.equal(en.relAgo(30 * DAY), '1mo ago');
  assert.equal(en.relAgo(65 * DAY), '2mo ago');
  assert.equal(en.relAgo(364 * DAY), '12mo ago');
  assert.equal(en.relAgo(365 * DAY), '1y ago');
  assert.equal(en.relAgo(1100 * DAY), '3y ago');

  const ko = loadTimeContext('ko', KO);
  assert.equal(ko.relAgo(10 * SEC), '방금 전');
  assert.equal(ko.relAgo(3 * MIN), '3분 전');
  assert.equal(ko.relAgo(2 * HOUR), '2시간 전');
  assert.equal(ko.relAgo(4 * DAY), '4일 전');
  assert.equal(ko.relAgo(60 * DAY), '2개월 전');
  assert.equal(ko.relAgo(730 * DAY), '2년 전');
});

test('fmtTime handles data-f="relago", preserves title, and avoids redundant writes', () => {
  const ctx = loadTimeContext('ko', KO);
  const now = Date.UTC(2026, 10, 26, 12, 0, 0);
  const twoYearsAgo = now - 730 * DAY;

  let textWrites = 0;
  let textValue = '';
  const el = {
    attrs: { 'data-ms': String(twoYearsAgo), 'data-f': 'relago', 'data-tl': '마지막 활성' },
    getAttribute(k) { return this.attrs[k] ?? null; },
    get textContent() { return textValue; },
    set textContent(v) { textWrites++; textValue = v; },
    title: '',
    wsFmt: undefined,
  };

  ctx.fmtTime(el, now);
  assert.equal(el.textContent, '2년 전');
  assert.ok(el.title.startsWith('마지막 활성 · '), `unexpected title: ${el.title}`);
  assert.equal(textWrites, 1);

  // Calling again at the same bucket performs 0 DOM text writes.
  ctx.fmtTime(el, now + 30 * SEC);
  assert.equal(textWrites, 1);

  // tickTimes selector includes data-f="relago".
  assert.ok(source.includes('time[data-ms][data-f="relago"]'), 'tickTimes selector must include relago');
  assert.ok(source.includes('time[data-ms][data-f="since"]'), 'tickTimes selector must include since');
  assert.ok(source.includes('time[data-ms][data-f="ago"]'), 'tickTimes selector must include ago');
});

test('session-hover formats every <time> synchronously on mount and SSE refresh without HH:MM:SSZ flash', () => {
  const fmtEnd = source.indexOf('  // A usage limit\'s pace:', start);
  const fmtBlock = source.slice(start, fmtEnd);
  const railSource = readAssetSource('wsdashboard', 'rail.js');
  const tmplSource = readAssetSource('wsdashboard', 'dashboard.tmpl');

  // Verify session-hover template keeps all timestamp fields on relative or clock formats (never raw unformatted nodes).
  const hoverTmplMatch = tmplSource.match(/\{\{define "session-hover"\}\}([\s\S]*?)\{\{end\}\}\n/);
  assert.ok(hoverTmplMatch, 'session-hover template must exist');
  assert.ok(hoverTmplMatch[1].includes('{{template "dur" .Hover.TurnStartedAt}}'), 'current turn uses dur (since)');
  assert.ok(hoverTmplMatch[1].includes('data-f="relago"'), 'archived last active uses relago');
  assert.ok(hoverTmplMatch[1].includes('{{template "ago" .}}'), 'foot timestamps use ago');

  const KO_FULL = {
    ...KO,
    'js.ago': '{0} 전',
  };

  function parseTimes(html) {
    const nodes = [];
    const re = /<time\b([^>]*)>([^<]*)<\/time>/g;
    let m;
    while ((m = re.exec(html))) {
      const attrs = {};
      for (const a of m[1].matchAll(/([a-z-]+)="([^"]*)"/g)) attrs[a[1]] = a[2];
      let text = m[2];
      nodes.push({
        tagName: 'TIME',
        attrs,
        title: attrs.title || '',
        wsFmt: undefined,
        getAttribute(k) { return this.attrs[k] ?? null; },
        get textContent() { return text; },
        set textContent(v) { text = String(v); },
      });
    }
    return nodes;
  }

  let popup = null;
  let mutate = null;
  let hit = null;
  const events = {};
  const win = {
    innerWidth: 1280,
    innerHeight: 800,
    addEventListener() {},
    MutationObserver: function (fn) {
      mutate = fn;
      this.observe = () => {};
    },
  };
  const doc = {
    hidden: false,
    documentElement: { className: '' },
    body: {
      appendChild(el) { popup = el; },
    },
    addEventListener(k, fn) { events[k] = fn; },
    elementFromPoint() { return hit; },
    querySelectorAll(sel) {
      return popup ? popup.querySelectorAll(sel) : [];
    },
    createElement() {
      let inner = '';
      let timeNodes = [];
      let measuredWithRawUTC = false;
      const el = {
        style: {},
        hidden: true,
        className: '',
        setAttribute() {},
        contains(n) { return n === el || timeNodes.includes(n); },
        get innerHTML() { return inner; },
        set innerHTML(v) {
          inner = String(v);
          timeNodes = parseTimes(inner);
        },
        querySelectorAll(sel) {
          if (sel === 'time[data-ms]') return timeNodes.filter(n => n.attrs['data-ms'] != null);
          return [];
        },
        get offsetWidth() {
          if (timeNodes.some(n => /\d{2}:\d{2}:\d{2}Z/.test(n.textContent))) measuredWithRawUTC = true;
          return 320;
        },
        get offsetHeight() { return 200; },
        get measuredWithRawUTC() { return measuredWithRawUTC; },
      };
      return el;
    },
  };

  const ctx = {
    window: win,
    doc,
    document: doc,
    q: (sel, el) => Array.from((el || doc).querySelectorAll(sel)),
    wsI18n: { lang: 'ko' },
    wsT: (key, ...args) => {
      const tmpl = KO_FULL[key];
      if (tmpl == null) return key;
      return tmpl.replace(/\{(\d+)\}/g, (_, i) => String(args[Number(i)]));
    },
  };
  vm.createContext(ctx);
  vm.runInContext(fmtBlock, ctx);
  assert.equal(typeof win.wsFmtTimes, 'function', 'dashboard.js exposes window.wsFmtTimes');
  vm.runInContext(railSource, ctx);

  const now = Date.now();
  const hoverHTML = (snapMs) => [
    `<time class="t" data-ms="${now - 45 * SEC}" data-f="since">13:17:51Z</time>`,
    `<time class="t" data-ms="${now - 12 * SEC}" data-f="ago">13:18:51Z</time>`,
    `<time class="t" data-ms="${now - 83 * MIN}" data-f="ago">11:55:04Z</time>`,
    `<time class="t" data-ms="${snapMs}" data-f="ago">13:18:51Z</time>`,
    `<time class="t" data-ms="${now - 5 * DAY}" data-f="relago">13:18:51Z</time>`,
    `<time class="t" data-ms="${now - 30 * MIN}" data-f="clock">12:48:51Z</time>`,
  ].join(' ');

  const srcTmpl = {
    tagName: 'TEMPLATE',
    className: 'shp-source',
    innerHTML: hoverHTML(now - 2 * SEC),
    getAttribute() { return null; },
  };
  const row = {
    className: 'rs',
    isConnected: true,
    children: [srcTmpl],
    closest(sel) { return sel === '.rail' ? null : row; },
    getClientRects() { return [1]; },
    getBoundingClientRect() { return { left: 10, right: 330, top: 50 }; },
  };

  ctx.window.WsRail.hover(doc, win);
  hit = row;
  events.mouseover({ target: row, clientX: 40, clientY: 60 });

  assert.ok(popup, 'hover popover mounted');
  assert.equal(popup.hidden, false);
  assert.equal(popup.measuredWithRawUTC, false, 'fmtTimes must run synchronously before offsetWidth measurement');

  const times = popup.querySelectorAll('time[data-ms]');
  assert.equal(times.length, 6);
  for (const t of times) {
    assert.ok(!/\d{2}:\d{2}:\d{2}Z/.test(t.textContent), `raw UTC leaked on mount: ${t.textContent}`);
    assert.ok(t.title.length > 0, 'localized tooltip preserved in title');
  }
  assert.equal(times[0].textContent, '45s');
  assert.equal(times[1].textContent, '12s 전');
  assert.equal(times[2].textContent, '1h 23m 전');
  assert.equal(times[3].textContent, '2s 전');
  assert.equal(times[4].textContent, '5일 전');
  assert.match(times[5].textContent, /^\d{2}:\d{2}$/);

  // Simulate SSE update replacing shp-source with raw server clock fallback again.
  srcTmpl.innerHTML = hoverHTML(now);
  mutate([{ target: srcTmpl }]);

  const refreshed = popup.querySelectorAll('time[data-ms]');
  for (const t of refreshed) {
    assert.ok(!/\d{2}:\d{2}:\d{2}Z/.test(t.textContent), `raw UTC leaked after SSE refresh: ${t.textContent}`);
  }
  assert.equal(refreshed[3].textContent, '0s 전');

  // Long-age ticks for since, ago, and relago remain relative and never flip to absolute dates.
  const longAgo = now - 400 * DAY;
  const longSince = { attrs: { 'data-ms': String(longAgo), 'data-f': 'since' }, getAttribute(k) { return this.attrs[k] ?? null; }, textContent: '', title: '' };
  const longPast = { attrs: { 'data-ms': String(longAgo), 'data-f': 'ago' }, getAttribute(k) { return this.attrs[k] ?? null; }, textContent: '', title: '' };
  const longRelAgo = { attrs: { 'data-ms': String(longAgo), 'data-f': 'relago' }, getAttribute(k) { return this.attrs[k] ?? null; }, textContent: '', title: '' };
  ctx.fmtTime(longSince, now);
  ctx.fmtTime(longPast, now);
  ctx.fmtTime(longRelAgo, now);
  assert.equal(longSince.textContent, '9600h 0m');
  assert.equal(longPast.textContent, '9600h 0m 전');
  assert.equal(longRelAgo.textContent, '1년 전');
});
