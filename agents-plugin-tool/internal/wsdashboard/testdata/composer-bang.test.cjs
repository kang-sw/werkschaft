// Composer `!` terminal look, client side (Phase 1 + Phase 4 of 261123-feat-ws-composer-bang-terminal-look):
// shell mode flag and amber `!`, the first-Esc rule, no @ or / popup in shell mode, headless
// submission tracking (`ws:cexec-admitted`, bounded poll, toast warnings), 0 px height invariance,
// Solarized Light / dark palette, and the slot blocks. Runs the real composer scripts in Chromium
// against a static composer fixture with a stubbed fetch (no daemon). Needs WS_PLAYWRIGHT_MODULE
// (a playwright-core path) and PLAYWRIGHT_BROWSERS_PATH when Chromium is not installed by default.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const mod = process.env.WS_PLAYWRIGHT_MODULE;
const skip = mod ? false : 'set WS_PLAYWRIGHT_MODULE to run Chromium acceptance';
const A = path.resolve(__dirname, '../assets');
const read = f => fs.readFileSync(path.join(A, f), 'utf8');
const en = JSON.parse(fs.readFileSync(path.resolve(__dirname, '../../wsi18n/tables/en.json'), 'utf8'));
const WB = fs.readFileSync(path.resolve(__dirname, '../../wswhiteboard/assets/page.css'), 'utf8');

function slice(src, from, to) {
  const a = src.indexOf(from), b = src.indexOf(to, a);
  assert(a >= 0 && b > a, 'production anchor exists: ' + from);
  return src.slice(a, b);
}
const pickers = read('dashboard-composer-pickers.js');
const css = [WB, read('dashboard.css'), read('dashboard-conversation.css'), read('dashboard-exec-hud.css'), read('dashboard-composer-ucmd.css'), read('dashboard-composer-ucmd-editor.css'), read('dashboard-rail.css')].join('\n');
const html = `<!doctype html><html><head><meta charset="utf-8"><style>${css}</style></head><body style="margin:0;padding:340px 24px 24px;width:760px">
<div id="composer" class="composer" data-cexec="1"><div class="cbadges"><button type="button" class="ibadge" id="ibadge" hidden><span class="istop"></span><span class="ilabel">Stop</span></button><span class="sbwrap"><button type="button" class="sbadge" id="sbadge" hidden><span class="spin"></span><span class="slabel"></span><span class="sword">running</span></button><span class="bgbadge" id="bgbadge" hidden><span class="blabel"></span></span><span class="bgbadge" id="monbadge" hidden><span class="blabel"></span></span></span></div><div class="cimgs" id="cimgs"></div><div class="cchips" id="cchips"></div><div class="crow"><div class="cfield"><div class="chl" id="chl"></div><textarea id="ctext" rows="1" placeholder="x"></textarea></div><button type="button" class="csend" id="csend">s</button></div><div class="cfoot"></div></div>
<button id="after" type="button">after</button></body></html>`;
const shim = `
var doc = document, $ = function (id) { return document.getElementById(id); }, q = function (s, r) { return Array.prototype.slice.call((r || doc).querySelectorAll(s)); }, rid = "fx";
var T = ${JSON.stringify(en)};
function wsT(k) { var s = T[k] || k; for (var i = 1; i < arguments.length; i++) s = s.replace("{" + (i - 1) + "}", arguments[i]); return s; }
function wsTn(k, n) { return wsT(k + (n === 1 ? ".one" : ".other"), n); }
var composer = $("composer"), ctext = $("ctext"), inputMode = "enter", mention = null, slash = null, cmdSel = null;
var xhPeekEl = null; function xhPeekOpen() { return false; } function xhPeekId() { return ""; } function xhOpenPeek() {} function xhClosePeek() {} function xhAskStop() {} function xhPeekSync() {} function xhPeekJobsChanged() {} function xhStopSettle() {} function xhPeekDestroy() {}
if (window.WsCapsules) window.WsCapsules.attach(ctext);
function grow() {} function clearMentions() {} function saveDraft() {} function queueDraft() {} function noteWarn(m) { window.__warn = m; } function noteOk(m) { window.__ok = m; } function post(r, b, d) { d(true, {}); }
function copyText() {} function syncMarkers() {} function trackMentions() {} function syncChips() {} function followImgs() {} function inBoard() { return false; } function uncommand() {}
function execWake() { window.__execWakes = (window.__execWakes || 0) + 1; }
function sendMessage() { if (!cexecSend()) window.__msg = ctext.value; }
function editField(el, start, end, s) {
  el.focus(); el.setSelectionRange(start, end);
  var done = false;
  try { done = s ? doc.execCommand("insertText", false, s) : start === end || doc.execCommand("delete"); } catch (e) { done = false; }
  if (!done) { el.setRangeText(s, start, end, "end"); el.dispatchEvent(new Event("input", { bubbles: true })); }
}
function editText(a, b, s) { editField(ctext, a, b, s); }
function commandList() { return [{ name: "build", description: "d" }]; }
${slice(pickers, '  var chl = $("chl")', '  function shiftMentions')}
${slice(pickers, '  function clearMentions() {', '  // sendText is')}
${slice(pickers, '  function fileCandidates', '  // --- composer slash commands')}
${slice(pickers, '  function slashToken', '  function commandRow')}
window.__slashToken = slashToken;
`.replace(/function clearMentions\(\) \{\}/, '');
const JOBS = {
  done: { phase: 'done', exec_id: 'exec_done1', exit_code: 0, output: 'hi\n', output_bytes: 3, state: 'exited' },
  sleep: { phase: 'async', exec_id: 'exec_7f3a2c', output: 'tail\n' },
  pending: { phase: 'running' },
  refused: { phase: 'refused', error: 'worktree locked' },
  unk: { phase: 'done', unknown: true },
};
const route = `
window.__slots = { rev: "1.1.x", token: "tok", commands: [{ id: "t", label: "Run tests", description: "d", line: "make test\\n./run --verbose", revision: "r1" }] };
window.__admitted = [];
window.addEventListener("ws:cexec-admitted", function (e) { window.__admitted.push(e.detail); });
window.__polls = [];
window.fetch = function (url, o) {
  var u = String(url), body = o && o.body ? JSON.parse(o.body) : null, J = ${JSON.stringify(JOBS)};
  function res(v, st) { return Promise.resolve({ ok: !st || st < 400, status: st || 200, json: function () { return Promise.resolve(v); }, text: function () { return Promise.resolve(JSON.stringify(v)); } }); }
  if (/composer-exec$/.test(u) && !body && window.__hook) { var held = window.__hook(window.__gets = (window.__gets || 0) + 1); if (held) return held; }
  if (/composer-exec$/.test(u) && !body && window.__get) { if (window.__get === "net") return Promise.reject(new TypeError("x")); return Promise.resolve({ ok: false, status: window.__get, json: function () { return Promise.reject(0); }, text: function () { return Promise.resolve(window.__getText + " "); } }); }
  if (/composer-exec$/.test(u) && !body) return res({ token: "tok", cards: [] });
  if (/composer-exec$/.test(u) && body) { if (window.__cexecPost) { var custom = window.__cexecPost(body, res); if (custom) return custom; } var cmd = body.text.slice(1); return res(Object.assign({ id: body.submission_id, command: cmd, token: "tok" }, J[cmd] || J.done), cmd === "pending" ? 202 : 200); }
  if (/user-commands\\/[^/]+\\/run$/.test(u) && body) { return res(Object.assign({ id: body.submission_id, command: "make test\\n./run --verbose", token: "tok" }, window.__ucmdReply || { phase: "running" }), window.__ucmdStatus || 202); }
  if (/composer-exec\\/([^/]+)$/.test(u)) {
    var pid = RegExp.$1;
    window.__polls.push(pid);
    if (window.__onPoll) return Promise.resolve(window.__onPoll(pid, window.__polls.length, res));
    return res({}, 404);
  }
  if (/files\\?/.test(u)) return res({ items: [{ path: "src/a.go", name: "a.go", dir: "src" }] });
  if (/exec\\/jobs$/.test(u) && window.__jobs) return res({ rev: 1, jobs: window.__jobs });
  if (/user-commands$/.test(u)) return res(window.__slots);
  return res({}, 404);
};`;

// pre runs in the page before the composer scripts: a seeded history, or a list-read hook.
async function open(t, pre, opts) {
  const { chromium } = require(mod);
  const browser = await chromium.launch({ headless: true, ...(process.env.WS_PLAYWRIGHT_EXECUTABLE ? { executablePath: process.env.WS_PLAYWRIGHT_EXECUTABLE } : {}) });
  t.after(() => browser.close());
  const ctx = await browser.newContext({ viewport: { width: 860, height: 900 }, reducedMotion: 'reduce', colorScheme: (opts && opts.colorScheme) || 'dark' });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(e.message));
  // A real origin, so sessionStorage works.
  await page.route('http://fx.test/', r => r.fulfill({ contentType: 'text/html', body: html }));
  const mount = async (afterReloadPre) => {
    await page.addScriptTag({ content: read('capsules.js') });
    await page.addScriptTag({ content: read('mention.js') });
    await page.addScriptTag({ content: read('hold-confirm.js') });
    await page.addScriptTag({ content: route });
    const hook = afterReloadPre !== undefined ? afterReloadPre : pre;
    if (hook) await page.evaluate(hook);
    const scriptNames = ['dashboard-composer-exec.js', 'dashboard-composer-ucmd-editor.js', 'dashboard-composer-ucmd-card.js', 'dashboard-composer-ucmd.js'];
    if (opts && opts.hud) scriptNames.push('dashboard-exec-hud.js');
    scriptNames.push('dashboard-composer-keyboard.js');
    const files = scriptNames.map(read).join('\n');
    await page.addScriptTag({ content: '(function(){' + shim + files + '\nwindow.__t = { slashToken: slashToken, dropToken: function () { cexecToken = ""; }, rootTag: bangRootTag, histTag: bangHistTag, setInputMode: function (m) { inputMode = m; if (typeof bangHistSyncMode === "function") bangHistSyncMode(); }, ucmdApply: function (l) { ucmdApply(l, false); }, xhDismiss: function (id) { if (typeof xhDismissed !== "undefined") { xhDismissed[id] = true; xhSaveDismissed(); xhRender(true); } } };\nif (typeof xhGroupEl === "function" && typeof execWake === "function") execWake(1);})();' });
  };
  await page.goto('http://fx.test/');
  await mount();
  const ed = page.locator('.cfield [contenteditable]').first();
  const type = async text => { await ed.click(); await page.keyboard.press('Control+A'); await page.keyboard.press('Delete'); await page.keyboard.insertText(text); };
  const state = () => page.evaluate(() => ({ v: ctext.value, bang: composer.hasAttribute('data-bang'), hl: CSS.highlights.has('ws-bang'), sel: [ctext.selectionStart, ctext.selectionEnd] }));
  return { ctx, page, ed, type, state, errs, mount };
}

test('shell mode flag follows the first character, paints the bang, and keeps 0px height growth', { skip }, async t => {
  const { page, type, state, errs } = await open(t);
  await page.waitForSelector('#ucmdbadge:not([hidden])');
  const h0 = await page.evaluate(() => composer.getBoundingClientRect().height);
  assert.equal(await page.locator('.d-cexec, .cbang').count(), 0, 'no inline console or cbang hint row');
  await type('!echo hi');
  let s = await state();
  assert.equal(s.bang, true); assert.equal(s.hl, true);
  assert.equal(await page.evaluate(() => composer.getBoundingClientRect().height), h0, '0px vertical height change in dark bang mode');
  assert.equal(await page.evaluate(() => ctext.getAttribute('aria-description')), en['js.cexec.bang.aria']);
  assert.equal(await page.evaluate(() => document.getElementById('csend').getAttribute('aria-label')), en['js.cexec.bang.send']);
  assert.equal(await page.evaluate(() => getComputedStyle(document.querySelector('.cfield [contenteditable]')).backgroundColor), 'rgb(19, 20, 23)', 'dark input');
  await type(' !echo hi');
  s = await state();
  assert.equal(s.bang, false, 'a leading space is a prompt, not shell mode'); assert.equal(s.hl, false);
  assert.equal(await page.evaluate(() => composer.getBoundingClientRect().height), h0, '0px vertical height change leaving bang mode');
  await type('hi !');
  assert.equal((await state()).bang, false);
  assert.deepEqual(errs, []);
});

test('first Esc removes the bang and keeps the caret, second Esc arms Tab-out', { skip }, async t => {
  const { page, type, state, errs } = await open(t);
  await type('!ab');
  await page.keyboard.press('Escape');
  let s = await state();
  assert.equal(s.v, 'ab'); assert.equal(s.bang, false); assert.equal(s.hl, false);
  assert.deepEqual(s.sel, [2, 2], 'the caret stays on the same text');
  // a mid-text caret moves back with the text
  await type('!abcd');
  for (let i = 0; i < 2; i++) await page.keyboard.press('ArrowLeft');
  await page.keyboard.press('Escape');
  s = await state();
  assert.equal(s.v, 'abcd'); assert.deepEqual(s.sel, [2, 2]);
  // the second Esc is the Tab-out preparation: Tab then leaves instead of indenting
  await page.keyboard.press('Escape');
  await page.keyboard.press('Tab');
  assert.equal((await state()).v, 'abcd', 'Tab after Esc Esc did not indent');
  assert.notEqual(await page.evaluate(() => document.activeElement.getAttribute('contenteditable')), 'true', 'Tab left the editor');
  // the retry text comparison is untouched: nothing was sent
  assert.equal(await page.evaluate(() => sessionStorage.getItem('ws.cexec.attempt.fx')), null);
  assert.deepEqual(errs, []);
});

test('no @ or / popup in shell mode', { skip }, async t => {
  const { page, type } = await open(t);
  const open_ = () => page.evaluate(() => Array.prototype.some.call(document.querySelectorAll('.wsm-pop'), p => !p.hidden));
  await type('cat @');
  await page.waitForTimeout(500);
  assert.equal(await open_(), true, 'the file popup opens for a message');
  await type('!cat @');
  await page.waitForTimeout(500);
  assert.equal(await open_(), false, 'no file popup after a leading bang');
  assert.equal(await page.evaluate(() => window.__slashToken('!/b', 3)), null, 'no slash token in shell mode');
  await type('/b');
  assert.notEqual(await page.evaluate(() => window.__slashToken('/b', 2)), null, 'the slash token still works elsewhere');
});

test('headless tracking: ws:cexec-admitted, execWake, ucmd origin, toast errors, and bounded polling', { skip }, async t => {
  const timerControl = `
    window.__timers = [];
    window.__now = 1000;
    Date.now = function () { return window.__now; };
    window.__origSetTimeout = window.setTimeout;
    window.setTimeout = function (fn, ms) {
      if (ms >= 700 && ms <= 5000) {
        var id = window.__timers.length + 1;
        window.__timers.push({ id: id, fn: fn, ms: ms, cleared: false });
        return id;
      }
      return window.__origSetTimeout(fn, ms);
    };
    window.__origClearTimeout = window.clearTimeout;
    window.clearTimeout = function (id) {
      var item = window.__timers.find(function (t) { return t.id === id; });
      if (item) item.cleared = true;
      return window.__origClearTimeout(id);
    };
    window.__flushTimer = function () {
      var next = window.__timers.find(function (t) { return !t.cleared; });
      if (!next) return false;
      next.cleared = true;
      window.__now += next.ms;
      next.fn();
      return true;
    };
  `;
  const { page, type, errs } = await open(t, timerControl);
  await page.waitForSelector('#ucmdbadge:not([hidden])');
  const h0 = await page.evaluate(() => composer.getBoundingClientRect().height);

  // 1) Immediate exec_id on POST (e.g. retry or fast launch) emits once and wakes HUD.
  await type('!sleep');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => window.__admitted.length === 1);
  assert.deepEqual(await page.evaluate(() => window.__admitted[0]), {
    submission_id: await page.evaluate(() => window.__admitted[0].submission_id),
    command: 'sleep',
    exec_id: 'exec_7f3a2c',
    origin: 'cexec',
  });
  assert.equal(await page.evaluate(() => window.__execWakes), 1);
  assert.equal(await page.evaluate(() => composer.getBoundingClientRect().height), h0, '0px height change after submit');

  // 2) Accepted 202 without exec_id polls until exec_id arrives, then emits once and stops polling.
  await page.evaluate(() => {
    window.__polls.length = 0;
    window.__onPoll = function (id, n, res) {
      return n < 2 ? res({ id: id, command: 'pending', phase: 'running' }) : res({ id: id, command: 'pending', phase: 'async', exec_id: 'exec_polled' });
    };
  });
  await type('!pending');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => window.__timers.some(t => !t.cleared));
  await page.evaluate(() => window.__flushTimer());
  await page.waitForFunction(() => window.__timers.some(t => !t.cleared));
  await page.evaluate(() => window.__flushTimer());
  await page.waitForFunction(() => window.__admitted.length === 2);
  assert.equal(await page.evaluate(() => window.__admitted[1].exec_id), 'exec_polled');
  assert.equal(await page.evaluate(() => window.__admitted[1].origin), 'cexec');
  assert.equal(await page.evaluate(() => window.__execWakes), 2);
  assert.equal(await page.evaluate(() => window.__timers.some(t => !t.cleared)), false, 'polling stops once admitted');

  // 3) User command 1s hold goes through headless tracker with origin: 'ucmd'.
  await page.evaluate(() => {
    window.__onPoll = function (id, n, res) {
      return res({ id: id, command: 'make test\n./run --verbose', phase: 'done', exec_id: 'exec_ucmd1' });
    };
  });
  await page.click('#ucmdbadge');
  const runBox = await page.locator('.ucmd-ibtn-run').boundingBox();
  await page.mouse.move(runBox.x + runBox.width / 2, runBox.y + runBox.height / 2);
  await page.mouse.down();
  await page.waitForFunction(() => window.__timers.some(t => !t.cleared && t.ms === 1000));
  await page.evaluate(() => window.__flushTimer());
  await page.mouse.up();
  await page.waitForFunction(() => window.__timers.some(t => !t.cleared));
  await page.evaluate(() => window.__flushTimer());
  await page.waitForFunction(() => window.__admitted.length === 3);
  assert.equal(await page.evaluate(() => window.__admitted[2].origin), 'ucmd');
  assert.equal(await page.evaluate(() => window.__admitted[2].exec_id), 'exec_ucmd1');
  assert.equal(await page.evaluate(() => window.__execWakes), 3);

  // 4) unknown: true on POST shows noteWarn and keeps the draft; refused shows refused noteWarn.
  await page.evaluate(() => { window.__warn = null; });
  await type('!unk');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => window.__warn);
  assert.equal(await page.evaluate(() => window.__warn), en['js.cexec.unknown']);
  assert.equal(await page.evaluate(() => ctext.value), '!unk', 'unknown retry leaves draft intact');

  await page.evaluate(() => { window.__warn = null; });
  await type('!refused');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => window.__warn);
  assert.equal(await page.evaluate(() => window.__warn), en['js.cexec.refused'].replace('{0}', 'worktree locked'));

  // 5) 404 on poll triggers unknown warning and stops polling.
  await page.evaluate(() => { window.__warn = null; window.__onPoll = function (_, __, res) { return res({}, 404); }; });
  await type('!pending');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => window.__timers.some(t => !t.cleared));
  await page.evaluate(() => window.__flushTimer());
  await page.waitForFunction(() => window.__warn);
  assert.equal(await page.evaluate(() => window.__warn), en['js.cexec.unknown']);
  assert.equal(await page.evaluate(() => window.__timers.some(t => !t.cleared)), false, '404 stops polling');

  // 6) Bounded poll cap: stops after <= 20 polls and <= 30000 ms on persistent running / 5xx.
  await page.evaluate(() => {
    window.__polls.length = 0;
    window.__timers.length = 0;
    window.__now = 100000;
    window.__onPoll = function (id, n, res) { return res({ id: id, command: 'pending', phase: 'running' }); };
  });
  await type('!pending');
  await page.keyboard.press('Enter');
  for (let i = 0; i < 30; i++) {
    const fired = await page.evaluate(() => window.__flushTimer());
    if (!fired) break;
    await page.waitForTimeout(10);
  }
  const pollCount = await page.evaluate(() => window.__polls.length);
  assert.ok(pollCount > 0 && pollCount <= 20, 'poll count bounded: ' + pollCount);
  assert.equal(await page.evaluate(() => window.__timers.some(t => !t.cleared)), false, 'no pending poll timer past bound');
  assert.equal(await page.locator('.d-cexec, .cbang').count(), 0);
  assert.deepEqual(errs, []);
});

test('user command popover shows the whole stored line on an inline-editable card', { skip }, async t => {
  const { page } = await open(t);
  await page.waitForSelector('#ucmdbadge:not([hidden])');
  await page.click('#ucmdbadge');
  await page.waitForSelector('.ucmd-pop:not([hidden]) .ucmd-card');
  assert.equal(await page.locator('.ucmd-card .ucmd-inline-input.is-cmd').inputValue(), 'make test\n./run --verbose');
  assert.equal(await page.locator('.ucmd-card .ucmd-title').inputValue(), 'Run tests');
  assert.equal(await page.locator('.ucmd-card .ucmd-glyph').first().textContent(), '$');
});

test('light theme tints the terminal surfaces Solarized, dark stays dark, with 0px height delta', { skip }, async t => {
  const { page, type } = await open(t);
  await page.waitForSelector('#ucmdbadge:not([hidden])');
  const hDark = await page.evaluate(() => composer.getBoundingClientRect().height);
  const edBg = () => page.evaluate(() => getComputedStyle(document.querySelector('.cfield [contenteditable]')).backgroundColor);
  await page.emulateMedia({ colorScheme: 'light' });
  const hLightIdle = await page.evaluate(() => composer.getBoundingClientRect().height);
  assert.equal(hLightIdle, hDark, 'matching baseline height across themes');
  await type('!ls');
  assert.equal(await edBg(), 'rgb(253, 246, 227)', 'light theme bang input: Solarized base3');
  assert.equal(await page.evaluate(() => composer.getBoundingClientRect().height), hLightIdle, '0px vertical height change in light bang mode');
});

test('a refused token read keeps its status and reason in the warning, and the draft', { skip }, async t => {
  const { page, type, ed } = await open(t);
  const send = async () => { await page.evaluate(() => window.__t.dropToken()); await type('!ls'); await page.keyboard.press('Enter'); await page.waitForTimeout(150); };
  const warn = () => page.evaluate(() => window.__warn);
  await page.evaluate(() => { window.__get = 404; window.__getText = 'unknown session'; });
  await send();
  assert.equal(await warn(), 'the dashboard is not reachable (404: unknown session)');
  assert.equal(await page.evaluate(() => ctext.value), '!ls', 'draft stays');
  await page.evaluate(() => { window.__get = 'net'; });
  await send();
  assert.equal(await warn(), 'the dashboard is not reachable (network error)');
  await page.evaluate(() => { window.__get = 0; });
  await send();
  assert.equal(await page.evaluate(() => ctext.value), '', 'a healthy read sends and clears the draft');
});

// A list read held by the test: resolves the nth GET of the composer-exec list by hand.
const HELD = `
window.__rel = {};
function held(n, make) { return new Promise(function (resolve) { window.__rel[n] = function () { resolve(make()); }; }); }
`;
const ok = v => `{ ok: true, status: 200, json: function () { return Promise.resolve(${v}); }, text: function () { return Promise.resolve(JSON.stringify(${v})); } }`;

test('overlapping list reads each carry their own failure cause', { skip }, async t => {
  // The restore read (1st) and a send's token read (2nd) overlap: the restore read fails with a
  // refusal while the send's read answers good headers and then a body that cannot be read. The
  // send's warning names its own cause, not the refusal the other read recorded meanwhile.
  const { page, type } = await open(t, HELD + `
window.__hook = function (n) {
  if (n === 1) return held("restore", function () { return { ok: false, status: 404, json: function () { return Promise.reject(0); }, text: function () { return Promise.resolve("unknown session"); } }; });
  if (n === 2) return Promise.resolve({ ok: true, status: 200, json: function () { return new Promise(function (_, rej) { window.__rel.body = function () { rej(new SyntaxError("cut")); }; }); }, text: function () { return Promise.resolve(""); } });
};`);
  await page.evaluate(() => window.__t.dropToken());
  await type('!ls');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => window.__rel.body);
  await page.evaluate(() => window.__rel.restore());
  await page.waitForTimeout(150);
  assert.equal(await page.evaluate(() => window.__warn), undefined, 'the send is still waiting for its own read');
  await page.evaluate(() => window.__rel.body());
  await page.waitForFunction(() => window.__warn);
  assert.equal(await page.evaluate(() => window.__warn), `the dashboard is not reachable (${en['js.cexec.badreply']})`);
  assert.equal(await page.evaluate(() => ctext.value), '!ls', 'draft stays');
});

const OLD = [{ id: 'cxold1', command: 'old1' }, { id: 'cxold2', command: 'old2' }];
const seedOld = `sessionStorage.setItem("ws.cexec.fx", ${JSON.stringify(JSON.stringify(OLD))});`;
const listOld = `{ token: "tok", cards: [{ id: "cxold1", command: "old1", phase: "done", exit_code: 0, output_bytes: 0 }, { id: "cxold2", command: "old2", phase: "done", exit_code: 0, output_bytes: 0 }] }`;

test('page load primes the admission token via composer-exec list without restoring cards', { skip }, async t => {
  const { page, type, errs } = await open(t, seedOld + `
window.__hook = function (n) { if (n === 1) return Promise.resolve(${ok(listOld)}); };`);
  await page.waitForFunction(() => window.__gets === 1);
  await page.waitForTimeout(100);
  assert.equal(await page.locator('.d-cexec, .cexec').count(), 0, 'no cards restored in composer');
  assert.equal(await page.evaluate(() => window.__polls.length), 0, 'no per-card polls triggered for old history');
  await type('!sleep');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => window.__admitted.length === 1);
  assert.equal(await page.evaluate(() => window.__gets), 1, 'primed token was reused for fresh submission');
  assert.deepEqual(errs, []);
});

function fnv1a32Base36(s) {
  let h = 0x811c9dc5;
  for (const b of Buffer.from(s, 'utf8')) {
    h ^= b;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h.toString(36);
}

test('FNV-1a rootTag parity, unknown binding unavailable, 200 cap, promotion, multibyte >2KB skip, and reload persistence', { skip }, async t => {
  const { page, type, errs, mount } = await open(t, `
    window.__slots = { rev: "1.1.", token: "tok", commands: [] };
  `);
  // Unknown binding: #hbadge stays hidden even if data exists elsewhere.
  assert.equal(await page.evaluate(() => document.getElementById('hbadge').hidden), true);
  assert.equal(await page.evaluate(() => window.__t.histTag()), '');

  // Verify FNV-1a rootTag parity on POSIX and Windows paths.
  const tagChecks = await page.evaluate(() => ({
    posix: window.__t.rootTag('/Users/dev/project/../repo/'),
    win: window.__t.rootTag('D:/Workspace/Repos/devenv/'),
    winLower: window.__t.rootTag('d:\\Workspace\\Repos\\devenv'),
    winUpper: window.__t.rootTag('D:\\Workspace\\Repos\\devenv')
  }));
  assert.equal(tagChecks.posix, fnv1a32Base36('/Users/dev/repo'));
  assert.equal(tagChecks.win, fnv1a32Base36('D:\\Workspace\\Repos\\devenv'));
  assert.equal(tagChecks.winUpper, tagChecks.win);
  assert.notEqual(tagChecks.winLower, tagChecks.winUpper, 'Windows drive letter case is preserved as in Go filepath.Clean');

  // Bind worktree to tagChecks.win, seed 200 entries, admit a verified repeat and a >2KB multibyte command.
  const wtPath = 'D:\\Workspace\\Repos\\devenv';
  const wtTag = fnv1a32Base36(wtPath);
  await page.evaluate((tag) => {
    const now = Date.now();
    const rows = [];
    for (let i = 0; i < 200; i++) rows.push({ cmd: 'cmd-' + i, ts: now - (i + 1) * 60000, exit: i % 2 });
    localStorage.setItem('ws.cexec.history.' + tag, JSON.stringify({ v: 1, clearedAt: 0, deleted: {}, rows }));
    window.__t.ucmdApply({ rev: '1.2.' + tag, token: 'tok', commands: [] });
  }, wtTag);

  assert.equal(await page.evaluate(() => document.getElementById('hbadge').hidden), false);

  // Repeat cmd-50 through cexec + verified /exec/jobs -> promotes to top, keeps 200 cap.
  await page.evaluate((cwd) => {
    window.__jobs = [{ id: 'exec_rep', tool: 'bash', command: 'cmd-50', cwd, state: 'exited', exit_code: 0, started_at: Date.now(), ended_at: Date.now() }];
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: { submission_id: 's1', command: 'cmd-50', exec_id: 'exec_rep', origin: 'cexec' } }));
  }, wtPath);
  await page.waitForFunction((tag) => {
    const s = JSON.parse(localStorage.getItem('ws.cexec.history.' + tag) || '{}');
    return Array.isArray(s.rows) && s.rows[0] && s.rows[0].cmd === 'cmd-50' && s.rows[0].exit === 0;
  }, wtTag);
  const afterRepeat = await page.evaluate((tag) => JSON.parse(localStorage.getItem('ws.cexec.history.' + tag)).rows, wtTag);
  assert.equal(afterRepeat.length, 200);
  assert.equal(afterRepeat.filter(r => r.cmd === 'cmd-50').length, 1);

  // New command pushes oldest (cmd-199) out.
  await page.evaluate((cwd) => {
    window.__jobs = [{ id: 'exec_new', tool: 'bash', command: 'brand-new', cwd, state: 'exited', exit_code: 2, started_at: Date.now(), ended_at: Date.now() }];
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: { submission_id: 's2', command: 'brand-new', exec_id: 'exec_new', origin: 'cexec' } }));
  }, wtPath);
  await page.waitForFunction((tag) => {
    const s = JSON.parse(localStorage.getItem('ws.cexec.history.' + tag) || '{}');
    return s.rows && s.rows[0] && s.rows[0].cmd === 'brand-new';
  }, wtTag);
  const afterCap = await page.evaluate((tag) => JSON.parse(localStorage.getItem('ws.cexec.history.' + tag)).rows, wtTag);
  assert.equal(afterCap.length, 200);
  assert.equal(afterCap.some(r => r.cmd === 'cmd-199'), false);

  // Multibyte UTF-8 command with .length <= 2048 but UTF-8 byte size > 2KB still runs without persisting.
  const hugeUtf8 = 'echo ' + '한'.repeat(700);
  assert.ok(hugeUtf8.length < 1000 && Buffer.byteLength(hugeUtf8, 'utf8') > 2048);
  await page.evaluate((cmd) => {
    window.__jobs = [{ id: 'exec_done1', tool: 'bash', command: cmd, cwd: 'D:\\Workspace\\Repos\\devenv', state: 'exited', exit_code: 0 }];
  }, hugeUtf8);
  await type('!' + hugeUtf8);
  await page.keyboard.press('Enter');
  await page.waitForFunction((cmd) => window.__admitted.some(a => a.command === cmd), hugeUtf8);
  const hugeStored = await page.evaluate(({ tag, cmd }) => JSON.parse(localStorage.getItem('ws.cexec.history.' + tag)).rows.some(r => r.cmd === cmd), { tag: wtTag, cmd: hugeUtf8 });
  assert.equal(hugeStored, false, 'multibyte >2KB command is omitted from localStorage while still running');

  // Real page reload preserves history and popup rows.
  await page.reload();
  await mount(`window.__slots = { rev: "1.3.${wtTag}", token: "tok", commands: [] };`);
  await page.waitForFunction(() => document.getElementById('hbadge') && !document.getElementById('hbadge').hidden);
  await type('!');
  await page.waitForSelector('.chist-row');
  assert.equal(await page.locator('.chist-row').first().locator('.chist-cmd').textContent(), 'brand-new');
  assert.equal(await page.locator('.chist-row').first().locator('.chist-ex.fail').textContent(), '2');
  assert.deepEqual(errs, []);
});

test('exec jobs history retries a rejected fetch and JSON body', { skip }, async t => {
  const cwd = '/worktrees/alpha';
  const tag = fnv1a32Base36(cwd);
  const { page, errs } = await open(t, `
    window.__slots = { rev: "1.1.${tag}", token: "tok", commands: [] };
    window.__jobsReads = 0;
    const fixtureFetch = window.fetch;
    window.fetch = function (url, options) {
      if (!String(url).endsWith("/exec/jobs")) return fixtureFetch(url, options);
      switch (++window.__jobsReads) {
      case 1:
        return Promise.reject(new TypeError("network failed"));
      case 2:
        return Promise.resolve(new Response("not JSON"));
      default:
        return Promise.resolve(Response.json({ jobs: [{
          id: "exec_retried", command: "retry-test", cwd: ${JSON.stringify(cwd)},
          state: "exited", exit_code: 0, started_at: Date.now(), ended_at: Date.now()
        }] }));
      }
    };
  `);
  await page.evaluate(() => {
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: {
      submission_id: 'retry-test', command: 'retry-test', exec_id: 'exec_retried', origin: 'cexec'
    } }));
  });
  await page.waitForFunction(key => {
    const { rows } = JSON.parse(localStorage.getItem('ws.cexec.history.' + key) || '{}');
    return rows?.some(row => row.cmd === 'retry-test' && row.exit === 0);
  }, tag, { timeout: 8000 });
  assert.equal(await page.evaluate(() => window.__jobsReads), 3);
  assert.deepEqual(errs, []);
});

test('origin verification, refusal/unknown/retry, binding switches, missing HUD job, and worktree isolation', { skip }, async t => {
  const cwdA = '/worktrees/alpha';
  const cwdB = '/worktrees/beta';
  const tagA = fnv1a32Base36(cwdA);
  const tagB = fnv1a32Base36(cwdB);
  const { page, type, errs } = await open(t, `
    window.__slots = { rev: "1.1.${tagA}", token: "tok", commands: [] };
  `, { hud: true });
  await page.waitForFunction(() => document.getElementById('hbadge'));

  // 1) Refusal (400 / refused phase) and unknown result MUST NOT create a history row.
  await type('!refused');
  await page.keyboard.press('Enter');
  await type('!unk');
  await page.keyboard.press('Enter');
  await page.evaluate(() => {
    window.__cexecPost = (body, res) => body.text === '!http400' ? res('bad args', 400) : null;
  });
  await type('!http400');
  await page.keyboard.press('Enter');
  await page.waitForTimeout(80);
  assert.equal(await page.evaluate((t) => localStorage.getItem('ws.cexec.history.' + t), tagA), null, 'refused/unknown/400 leave no history');

  // 2) origin === 'ucmd' ignored; cwdB records ONLY under verified launch worktree tagB (not tagA); missing/no-cwd jobs stay unrecorded.
  await page.evaluate(({ cwdB }) => {
    window.__jobs = [
      { id: 'exec_uc', tool: 'bash', command: 'make test', cwd: '/worktrees/alpha', state: 'exited', exit_code: 0, started_at: Date.now(), ended_at: Date.now() },
      { id: 'exec_wrong', tool: 'bash', command: 'wrong-wt', cwd: cwdB, state: 'exited', exit_code: 0, started_at: Date.now(), ended_at: Date.now() },
      { id: 'exec_nocwd', tool: 'bash', command: 'no-cwd', cwd: '', state: 'exited', exit_code: 0, started_at: Date.now(), ended_at: Date.now() },
      { id: 'exec_run', tool: 'bash', command: 'npm test', cwd: '/worktrees/alpha', state: 'running', origin: 'user', started_at: Date.now() }
    ];
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: { submission_id: 'su', command: 'make test', exec_id: 'exec_uc', origin: 'ucmd' } }));
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: { submission_id: 'sw', command: 'wrong-wt', exec_id: 'exec_wrong', origin: 'cexec' } }));
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: { submission_id: 'sm', command: 'missing-job', exec_id: 'exec_missing', origin: 'cexec' } }));
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: { submission_id: 'sn', command: 'no-cwd', exec_id: 'exec_nocwd', origin: 'cexec' } }));
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: { submission_id: 'sr', command: 'npm test', exec_id: 'exec_run', origin: 'cexec' } }));
  }, { cwdB });

  await page.waitForFunction((tag) => {
    const s = JSON.parse(localStorage.getItem('ws.cexec.history.' + tag) || '{}');
    return Array.isArray(s.rows) && s.rows.some(r => r.cmd === 'npm test');
  }, tagA);

  let rowsA = await page.evaluate((t) => JSON.parse(localStorage.getItem('ws.cexec.history.' + t)).rows, tagA);
  let rowsB = await page.evaluate((t) => JSON.parse(localStorage.getItem('ws.cexec.history.' + t) || '{}').rows || [], tagB);
  assert.equal(rowsA.some(r => r.cmd === 'make test'), false, 'ucmd ignored');
  assert.equal(rowsA.some(r => r.cmd === 'wrong-wt'), false, 'cwdB job not placed in tagA');
  assert.equal(rowsB.some(r => r.cmd === 'wrong-wt'), true, 'cwdB job recorded under verified tagB');
  assert.equal(rowsA.some(r => r.cmd === 'missing-job' || r.cmd === 'no-cwd'), false, 'missing or unusable-cwd HUD job stays unrecorded');
  assert.equal(rowsA[0].exit, undefined, 'running job has no exit yet');

  // 3) Dismiss HUD chip AND switch active worktree to B while exec_run is still tracking!
  await page.evaluate(({ tagB }) => {
    window.__t.xhDismiss('exec_run');
    window.__t.ucmdApply({ rev: '1.2.' + tagB, token: 'tok', commands: [] });
    window.__jobs = [{ id: 'exec_run', tool: 'bash', command: 'npm test', cwd: '/worktrees/alpha', state: 'exited', exit_code: 1, started_at: Date.now() - 1000, ended_at: Date.now() }];
  }, { tagB });

  await page.waitForFunction((tag) => {
    const s = JSON.parse(localStorage.getItem('ws.cexec.history.' + tag) || '{}');
    return Array.isArray(s.rows) && s.rows[0] && s.rows[0].exit === 1;
  }, tagA);

  // 4) Binding change BEFORE server admission: submit on tagA, switch ucmd to tagB before poll admits exec_pa -> records ONLY under cwdA (tagA)!
  await page.evaluate(({ tagA, cwdA }) => {
    window.__t.ucmdApply({ rev: '1.3.' + tagA, token: 'tok', commands: [] });
    window.__jobs = [
      { id: 'exec_pa', tool: 'bash', command: 'pre-admit', cwd: cwdA, state: 'exited', exit_code: 0, started_at: Date.now(), ended_at: Date.now() }
    ];
    window.__cexecPost = (body, res) => {
      if (body.text === '!pre-admit') return res({ id: body.submission_id, command: 'pre-admit', phase: 'running', token: 'tok' }, 202);
      return null;
    };
    window.__onPoll = (pid, _n, res) => res({ id: pid, command: 'pre-admit', phase: 'running', exec_id: 'exec_pa', token: 'tok' });
  }, { tagA, cwdA });
  await type('!pre-admit');
  await page.keyboard.press('Enter');
  await page.evaluate((tB) => window.__t.ucmdApply({ rev: '1.4.' + tB, token: 'tok', commands: [] }), tagB);
  await page.waitForFunction((tag) => {
    const s = JSON.parse(localStorage.getItem('ws.cexec.history.' + tag) || '{}');
    return s.rows && s.rows.some(r => r.cmd === 'pre-admit' && r.exit === 0);
  }, tagA);
  rowsB = await page.evaluate((t) => JSON.parse(localStorage.getItem('ws.cexec.history.' + t) || '{}').rows || [], tagB);
  assert.equal(rowsB.some(r => r.cmd === 'pre-admit'), false, 'pre-admission switch still records only under verified cwdA');

  // 5) Binding change BEFORE retry (500 -> immediate POST 200 retry): single row under verified cwdA (tagA), NOT duplicated!
  await page.evaluate(({ tagA, cwdA }) => {
    window.__t.ucmdApply({ rev: '1.5.' + tagA, token: 'tok', commands: [] });
    window.__retrySeen = 0;
    window.__jobs = [{ id: 'exec_rt', tool: 'bash', command: 'retry-wt', cwd: cwdA, state: 'exited', exit_code: 0, started_at: Date.now(), ended_at: Date.now() }];
    window.__cexecPost = (body, res) => {
      if (body.text !== '!retry-wt') return null;
      window.__retrySeen++;
      if (!body.retry) return res('temp error', 500);
      return res({ id: body.submission_id, command: 'retry-wt', phase: 'running', exec_id: 'exec_rt', token: 'tok' }, 200);
    };
  }, { tagA, cwdA });
  await type('!retry-wt');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => window.__retrySeen === 1);
  // Switch binding to B before pressing Enter to retry.
  await page.evaluate((tB) => window.__t.ucmdApply({ rev: '1.6.' + tB, token: 'tok', commands: [] }), tagB);
  await page.keyboard.press('Enter');
  await page.waitForFunction((tag) => {
    const s = JSON.parse(localStorage.getItem('ws.cexec.history.' + tag) || '{}');
    return s.rows && s.rows.filter(r => r.cmd === 'retry-wt').length === 1;
  }, tagA);
  rowsA = await page.evaluate((t) => JSON.parse(localStorage.getItem('ws.cexec.history.' + t)).rows, tagA);
  rowsB = await page.evaluate((t) => JSON.parse(localStorage.getItem('ws.cexec.history.' + t) || '{}').rows || [], tagB);
  assert.equal(rowsA.filter(r => r.cmd === 'retry-wt').length, 1, 'POST 200 retry creates single row under verified cwdA');
  assert.equal(rowsB.some(r => r.cmd === 'retry-wt'), false, 'retry switch does not leak into tagB');
  assert.deepEqual(errs, []);
});

test('cross-page storage sync prevents stale page from reviving deleted/cleared entries or overwriting newer runs', { skip }, async t => {
  const cwd = '/worktrees/sync';
  const tag = fnv1a32Base36(cwd);
  const a = await open(t, `window.__slots = { rev: "1.1.${tag}", token: "tok", commands: [] };`);
  const pageB = await a.ctx.newPage();
  await pageB.route('http://fx.test/', r => r.fulfill({ contentType: 'text/html', body: html }));
  await pageB.goto('http://fx.test/');
  await pageB.addScriptTag({ content: read('capsules.js') });
  await pageB.addScriptTag({ content: read('mention.js') });
  await pageB.addScriptTag({ content: route + `\nwindow.__slots = { rev: "1.1.${tag}", token: "tok", commands: [] };` });
  const files = ['dashboard-composer-exec.js', 'dashboard-composer-ucmd-editor.js', 'dashboard-composer-ucmd-card.js', 'dashboard-composer-ucmd.js', 'dashboard-composer-keyboard.js'].map(read).join('\n');
  await pageB.addScriptTag({ content: '(function(){' + shim + files + '\nwindow.__t = { ucmdApply: function (l) { ucmdApply(l, false); } };})();' });

  // 1) Page A launches running command; Page B deletes it before exit code resolves on A.
  await a.page.evaluate((cwd) => {
    window.__jobs = [{ id: 'exec_x', tool: 'bash', command: 'sleep 2', cwd, state: 'running', started_at: Date.now() }];
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: { submission_id: 'sx', command: 'sleep 2', exec_id: 'exec_x', origin: 'cexec' } }));
  }, cwd);
  await a.page.waitForFunction((t) => {
    const s = JSON.parse(localStorage.getItem('ws.cexec.history.' + t) || '{}');
    return s.rows && s.rows.some(r => r.cmd === 'sleep 2');
  }, tag);

  await pageB.waitForFunction(() => !document.getElementById('hbadge').hidden);
  await pageB.click('#hbadge');
  await pageB.click('.chist-del');
  await pageB.waitForFunction((t) => (JSON.parse(localStorage.getItem('ws.cexec.history.' + t)).rows || []).length === 0, tag);

  await a.page.evaluate((cwd) => {
    window.__jobs = [{ id: 'exec_x', tool: 'bash', command: 'sleep 2', cwd, state: 'exited', exit_code: 0, started_at: Date.now() - 1000, ended_at: Date.now() }];
  }, cwd);
  await a.page.waitForTimeout(900);
  let rowsAfter = await a.page.evaluate((t) => JSON.parse(localStorage.getItem('ws.cexec.history.' + t)).rows, tag);
  assert.equal(rowsAfter.length, 0, 'deleted row is not revived by in-flight poll');

  // 2) Newer repeat run on Page B must NOT be overwritten by late older completion on Page A!
  await a.page.evaluate((cwd) => {
    window.__jobs = [{ id: 'exec_old', tool: 'bash', command: 'rep-cmd', cwd, state: 'running', started_at: Date.now() }];
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: { submission_id: 's_old', command: 'rep-cmd', exec_id: 'exec_old', origin: 'cexec' } }));
  }, cwd);
  await a.page.waitForFunction((t) => {
    const s = JSON.parse(localStorage.getItem('ws.cexec.history.' + t) || '{}');
    return s.rows && s.rows.some(r => r.eid === 'exec_old');
  }, tag);
  await pageB.waitForTimeout(25);
  await pageB.evaluate((cwd) => {
    window.__jobs = [{ id: 'exec_new2', tool: 'bash', command: 'rep-cmd', cwd, state: 'exited', exit_code: 0, started_at: Date.now(), ended_at: Date.now() }];
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: { submission_id: 's_new2', command: 'rep-cmd', exec_id: 'exec_new2', origin: 'cexec' } }));
  }, cwd);
  await pageB.waitForFunction((t) => {
    const s = JSON.parse(localStorage.getItem('ws.cexec.history.' + t) || '{}');
    return s.rows && s.rows[0] && s.rows[0].eid === 'exec_new2' && s.rows[0].exit === 0;
  }, tag);
  await a.page.evaluate((cwd) => {
    window.__jobs = [{ id: 'exec_old', tool: 'bash', command: 'rep-cmd', cwd, state: 'exited', exit_code: 1, started_at: Date.now() - 2000, ended_at: Date.now() }];
  }, cwd);
  await a.page.waitForTimeout(900);
  rowsAfter = await a.page.evaluate((t) => JSON.parse(localStorage.getItem('ws.cexec.history.' + t)).rows, tag);
  assert.equal(rowsAfter[0].eid, 'exec_new2');
  assert.equal(rowsAfter[0].exit, 0, 'late older failure does not overwrite newer run exit 0');

  // 3) Clear all on Page B prevents stale in-flight job on Page A from reviving.
  await a.page.evaluate((cwd) => {
    window.__jobs = [{ id: 'exec_clr', tool: 'bash', command: 'clr-cmd', cwd, state: 'running', started_at: Date.now() }];
    window.dispatchEvent(new CustomEvent('ws:cexec-admitted', { detail: { submission_id: 'sc', command: 'clr-cmd', exec_id: 'exec_clr', origin: 'cexec' } }));
  }, cwd);
  await pageB.waitForFunction((t) => {
    const s = JSON.parse(localStorage.getItem('ws.cexec.history.' + t) || '{}');
    return s.rows && s.rows.some(r => r.cmd === 'clr-cmd');
  }, tag);
  await pageB.click('#hbadge');
  await pageB.click('.chist-clear');
  await pageB.click('.chist-clear.chist-armed');
  await pageB.waitForFunction((t) => (JSON.parse(localStorage.getItem('ws.cexec.history.' + t)).rows || []).length === 0, tag);
  await a.page.evaluate((cwd) => {
    window.__jobs = [{ id: 'exec_clr', tool: 'bash', command: 'clr-cmd', cwd, state: 'exited', exit_code: 0, started_at: Date.now() - 1000, ended_at: Date.now() }];
  }, cwd);
  await a.page.waitForTimeout(900);
  rowsAfter = await a.page.evaluate((t) => JSON.parse(localStorage.getItem('ws.cexec.history.' + t)).rows, tag);
  assert.equal(rowsAfter.length, 0, 'late completion after Clear all does not revive row');
  assert.deepEqual(a.errs, []);
});

test('popup states, fuzzy highlight, keyed reuse, keyboard nav, repeat guard, trigger badge & HUD layout', { skip }, async t => {
  const cwd = '/worktrees/ui';
  const tag = fnv1a32Base36(cwd);
  const now = Date.now();
  const seed = [
    { cmd: 'git status -sb', ts: now - 30000, exit: 0 },
    { cmd: 'go test ./internal/wsdashboard/...', ts: now - 5 * 60000, exit: 1 },
    { cmd: 'npm run build', ts: now - 2 * 3600000, exit: 0 },
    { cmd: 'git diff --stat', ts: now - 3 * 3600000 }
  ];
  const { page, type, errs } = await open(t, `
    window.__slots = { rev: "1.1.${tag}", token: "tok", commands: [] };
    window.__jobs = [{ id: 'j_hud', tool: 'bash', command: 'cargo check', cwd: '${cwd}', state: 'running', origin: 'agent', started_at: ${now - 4000} }];
    localStorage.setItem('ws.cexec.history.${tag}', ${JSON.stringify(JSON.stringify({ v: 1, clearedAt: 0, deleted: {}, rows: seed }))});
  `, { hud: true });

  await page.waitForSelector('.xgroup .xchip');
  const h0 = await page.evaluate(() => composer.getBoundingClientRect().height);
  const badgeRect = await page.evaluate(() => document.getElementById('hbadge').getBoundingClientRect());
  assert.equal(Math.round(badgeRect.height), 24, '#hbadge height is 24px');

  // Verify #hbadge sits beside .xgroup in .sbwrap and xhLayoutNow subtracts its width cleanly.
  const layoutInfo = await page.evaluate(() => {
    const wrap = document.querySelector('.sbwrap');
    const children = Array.from(wrap.children).map(el => el.className);
    const xg = wrap.querySelector('.xgroup').getBoundingClientRect();
    const hb = document.getElementById('hbadge').getBoundingClientRect();
    return { children, noOverlap: xg.right <= hb.left + 1 };
  });
  assert.equal(layoutInfo.children[0].includes('xgroup'), true);
  assert.equal(layoutInfo.children[1].includes('hbadge'), true);
  assert.equal(layoutInfo.noOverlap, true, '.xgroup does not overlap #hbadge');

  // State 1: type `!` -> auto-opens popup with 0px composer height growth.
  await type('!');
  await page.waitForSelector('.chist:not([hidden])');
  assert.equal(await page.evaluate(() => composer.getBoundingClientRect().height), h0, '0px height growth when popup open');
  assert.equal(await page.locator('.chist-row').count(), 4);
  assert.equal(await page.locator('.chist-row').first().locator('.chist-ex.ok').textContent(), '0');
  assert.equal(await page.locator('.chist-row').nth(3).locator('.chist-ex').isVisible(), false, 'unknown exit omitted');

  // Keep reference to 'git diff --stat' DOM row to check keyed patching (no full list rebuild).
  await page.evaluate(() => { window.__rowRef = Array.from(document.querySelectorAll('.chist-row')).find(r => r.textContent.includes('git diff')); });

  // State 2: fuzzy query `gd` -> matches git diff & go test / git status etc., highlights <mark>, reuses DOM row, shows N / M.
  await type('!gd');
  await page.waitForFunction(() => document.querySelector('.chist-count').textContent.includes('/ 4'));
  assert.equal(await page.evaluate(() => Array.from(document.querySelectorAll('.chist-row')).some(r => r === window.__rowRef)), true, 'keyed DOM row reused across filter update');
  assert.ok(await page.locator('.chist-row.sel .chist-cmd mark').count() >= 2, 'fuzzy characters wrapped in <mark>');

  // Ctrl+R advances to next match.
  const firstSel = await page.locator('.chist-row.sel .chist-cmd').textContent();
  await page.keyboard.press('Control+r');
  const secondSel = await page.locator('.chist-row.sel .chist-cmd').textContent();
  assert.notEqual(secondSel, firstSel, 'Ctrl+R moves selection to next match');

  // State 3: no match -> shows empty state + Enter hint.
  await type('!zzzz');
  await page.waitForSelector('.chist-empty:not([hidden])');
  assert.ok((await page.locator('.chist-count').textContent()).startsWith('0 / 4'));
  assert.equal(await page.locator('.chist-empty-hint').textContent(), en['js.cexec.hist.empty.enter']);

  // Enter executes typed command in State 3 AND closes the popup!
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => window.__admitted.some(a => a.command === 'zzzz'));
  await page.waitForFunction(() => document.querySelector('.chist').hidden === true);

  // Backspacing `!` to empty closes auto-opened popup.
  await type('!');
  await page.waitForSelector('.chist:not([hidden])');
  await page.keyboard.press('Backspace');
  await page.waitForFunction(() => document.querySelector('.chist').hidden === true);

  // Reopen with `!gs`: plain Enter picks selected row and closes popup WITHOUT executing; held repeat Enter MUST NOT execute!
  await type('!gs');
  await page.waitForSelector('.chist-row.sel');
  const pickedText = await page.locator('.chist-row.sel .chist-cmd').textContent();
  let admittedBefore = await page.evaluate(() => window.__admitted.length);
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => document.querySelector('.chist').hidden === true);
  assert.equal(await page.evaluate(() => ctext.value), '!' + pickedText);
  assert.equal(await page.evaluate(() => window.__admitted.length), admittedBefore, 'Enter selection does not immediately run');

  // Simulate held Enter repeat directly into contenteditable -> forwarded with repeat:true and ignored!
  await page.evaluate(() => {
    const ed = document.querySelector('.cfield [contenteditable]');
    ed.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', repeat: true, bubbles: true, cancelable: true }));
  });
  await page.waitForTimeout(80);
  assert.equal(await page.evaluate(() => window.__admitted.length), admittedBefore, 'held repeat Enter does not run');

  // In enter mode, Ctrl+Enter and Meta+Enter with a row selected also pick without running; repeat blocked; fresh chord runs!
  await type('!g');
  await page.waitForSelector('.chist-row.sel');
  await page.keyboard.press('Control+Enter');
  await page.waitForFunction(() => document.querySelector('.chist').hidden === true);
  assert.equal(await page.evaluate(() => window.__admitted.length), admittedBefore, 'enter mode Ctrl+Enter picks without running');
  await page.evaluate(() => {
    const ed = document.querySelector('.cfield [contenteditable]');
    ed.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', ctrlKey: true, repeat: true, bubbles: true, cancelable: true }));
  });
  await page.waitForTimeout(80);
  assert.equal(await page.evaluate(() => window.__admitted.length), admittedBefore, 'enter mode held repeat Ctrl+Enter does not run');
  await type('!g');
  await page.waitForSelector('.chist-row.sel');
  await page.keyboard.press('Meta+Enter');
  await page.waitForFunction(() => document.querySelector('.chist').hidden === true);
  assert.equal(await page.evaluate(() => window.__admitted.length), admittedBefore, 'enter mode Meta+Enter picks without running');
  await page.keyboard.press('Control+Enter');
  await page.waitForFunction((n) => window.__admitted.length === n + 1, admittedBefore);
  admittedBefore = await page.evaluate(() => window.__admitted.length);

  // First Esc closes open popup keeping `!`; second Esc removes `!`.
  await type('!g');
  await page.waitForSelector('.chist:not([hidden])');
  await page.keyboard.press('Escape');
  assert.equal(await page.evaluate(() => document.querySelector('.chist').hidden), true);
  assert.equal(await page.evaluate(() => ctext.value), '!g');
  await page.keyboard.press('Escape');
  assert.equal(await page.evaluate(() => ctext.value), 'g');

  // mod mode: Ctrl+Enter picks without running, repeat Ctrl+Enter blocked, fresh Ctrl+Enter runs!
  await page.evaluate(() => window.__t.setInputMode('mod'));
  await type('!g');
  await page.waitForSelector('.chist-row.sel');
  await page.keyboard.press('Control+Enter');
  await page.waitForFunction(() => document.querySelector('.chist').hidden === true);
  assert.equal(await page.evaluate(() => window.__admitted.length), admittedBefore, 'mod send chord selects without running');
  await page.evaluate(() => {
    const ed = document.querySelector('.cfield [contenteditable]');
    ed.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', ctrlKey: true, repeat: true, bubbles: true, cancelable: true }));
  });
  await page.waitForTimeout(80);
  assert.equal(await page.evaluate(() => window.__admitted.length), admittedBefore, 'held repeat Ctrl+Enter does not run');
  await page.keyboard.press('Control+Enter');
  await page.waitForFunction((n) => window.__admitted.length === n + 1, admittedBefore);

  // Trigger badge click opens from empty input, closes on non-`!` typing, replaces field on click pick.
  await type('hello');
  await page.click('#hbadge');
  await page.waitForSelector('.chist:not([hidden])');
  await page.keyboard.insertText('x');
  await page.waitForFunction(() => document.querySelector('.chist').hidden === true);
  await page.click('#hbadge');
  await page.waitForSelector('.chist-row');
  await page.locator('.chist-row').first().click();
  assert.equal(await page.evaluate(() => ctext.value.charAt(0)), '!');
  assert.equal(await page.evaluate(() => document.querySelector('.chist').hidden), true, 'pick from badge stays closed');

  // Two-step Clear all confirmation (expires after 3s, then deletes on confirm).
  await type('');
  await page.click('#hbadge');
  await page.click('.chist-clear');
  assert.equal(await page.locator('.chist-clear.chist-armed').count(), 1);
  assert.equal(await page.locator('.chist-row').count(), 4, 'first click does not wipe history');
  await page.waitForTimeout(3200);
  assert.equal(await page.locator('.chist-clear.chist-armed').count(), 0, 'armed Clear all expires after 3s');
  assert.equal(await page.locator('.chist-row').count(), 4, 'timeout expiry leaves history intact');
  await page.click('.chist-clear');
  await page.click('.chist-clear.chist-armed');
  await page.waitForFunction(() => document.getElementById('hbadge').hidden === true);
  assert.deepEqual(errs, []);
});

test('LOOK captures for bang history states and trigger badge', { skip: skip || !process.env.WS_BANG_LOOK_SHOTS }, async t => {
  const dir = process.env.WS_BANG_LOOK_SHOTS;
  fs.mkdirSync(dir, { recursive: true });
  const cwd = '/worktrees/look';
  const tag = fnv1a32Base36(cwd);
  const now = Date.now();
  const seed = [
    { cmd: 'git status -sb', ts: now - 30000, exit: 0 },
    { cmd: 'go test ./internal/wsdashboard/...', ts: now - 4 * 60000, exit: 1 },
    { cmd: 'git diff --stat HEAD~1', ts: now - 15 * 60000, exit: 0 },
    { cmd: 'npm run build', ts: now - 2 * 3600000, exit: 0 }
  ];
  const runShots = async (scheme) => {
    const { page, type } = await open(t, `
      window.__slots = { rev: "1.1.${tag}", token: "tok", commands: [] };
      window.__jobs = [{ id: 'j_hud', tool: 'bash', command: 'go test', cwd: '${cwd}', state: 'running', origin: 'user', started_at: ${now - 4000} }];
      localStorage.setItem('ws.cexec.history.${tag}', ${JSON.stringify(JSON.stringify({ v: 1, clearedAt: 0, deleted: {}, rows: seed }))});
    `, { hud: true, colorScheme: scheme });
    await page.waitForSelector('.xgroup .xchip');
    await type('!');
    await page.waitForSelector('.chist:not([hidden])');
    await page.screenshot({ path: path.join(dir, `state1-empty-${scheme}.png`) });
    await type('!gd');
    await page.waitForSelector('.chist-row mark');
    await page.screenshot({ path: path.join(dir, `state2-fuzzy-${scheme}.png`) });
    await type('!nomatch');
    await page.waitForSelector('.chist-empty:not([hidden])');
    await page.screenshot({ path: path.join(dir, `state3-empty-${scheme}.png`) });
  };
  await runShots('dark');
  await runShots('light');
});
