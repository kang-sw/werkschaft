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
});
