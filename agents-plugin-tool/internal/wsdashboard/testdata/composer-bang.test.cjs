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
const css = [WB, read('dashboard.css'), read('dashboard-conversation.css')].join('\n');
const html = `<!doctype html><html><head><meta charset="utf-8"><style>${css}</style></head><body style="margin:0;padding:24px;width:760px">
<div id="composer" class="composer" data-cexec="1"><div class="cimgs" id="cimgs"></div><div class="cchips" id="cchips"></div><div class="crow"><div class="cfield"><div class="chl" id="chl"></div><textarea id="ctext" rows="1" placeholder="x"></textarea></div><button type="button" class="csend" id="csend">s</button></div><div class="cfoot"></div></div>
<button id="after" type="button">after</button></body></html>`;
const shim = `
var doc = document, $ = function (id) { return document.getElementById(id); }, rid = "fx";
var T = ${JSON.stringify(en)};
function wsT(k) { var s = T[k] || k; for (var i = 1; i < arguments.length; i++) s = s.replace("{" + (i - 1) + "}", arguments[i]); return s; }
var composer = $("composer"), ctext = $("ctext"), inputMode = "enter", mention = null, slash = null, cmdSel = null;
if (window.WsCapsules) window.WsCapsules.attach(ctext);
function grow() {} function clearMentions() {} function saveDraft() {} function queueDraft() {} function noteWarn(m) { window.__warn = m; }
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
  if (/composer-exec$/.test(u) && body) { var cmd = body.text.slice(1); return res(Object.assign({ id: body.submission_id, command: cmd, token: "tok" }, J[cmd] || J.done), cmd === "pending" ? 202 : 200); }
  if (/user-commands\\/[^/]+\\/run$/.test(u) && body) { return res(Object.assign({ id: body.submission_id, command: "make test\\n./run --verbose", token: "tok" }, window.__ucmdReply || { phase: "running" }), window.__ucmdStatus || 202); }
  if (/composer-exec\\/([^/]+)$/.test(u)) {
    var pid = RegExp.$1;
    window.__polls.push(pid);
    if (window.__onPoll) return Promise.resolve(window.__onPoll(pid, window.__polls.length, res));
    return res({}, 404);
  }
  if (/files\\?/.test(u)) return res({ items: [{ path: "src/a.go", name: "a.go", dir: "src" }] });
  if (/user-commands$/.test(u)) return res(window.__slots);
  return res({}, 404);
};`;

// pre runs in the page before the composer scripts: a seeded history, or a list-read hook.
async function open(t, pre) {
  const { chromium } = require(mod);
  const browser = await chromium.launch({ headless: true, ...(process.env.WS_PLAYWRIGHT_EXECUTABLE ? { executablePath: process.env.WS_PLAYWRIGHT_EXECUTABLE } : {}) });
  t.after(() => browser.close());
  const page = await (await browser.newContext({ viewport: { width: 860, height: 900 }, reducedMotion: 'reduce', colorScheme: 'dark' })).newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(e.message));
  // A real origin, so sessionStorage works.
  await page.route('http://fx.test/', r => r.fulfill({ contentType: 'text/html', body: html }));
  await page.goto('http://fx.test/');
  await page.addScriptTag({ content: read('capsules.js') });
  await page.addScriptTag({ content: read('mention.js') });
  await page.addScriptTag({ content: route });
  if (pre) await page.evaluate(pre);
  const files = ['dashboard-composer-exec.js', 'dashboard-composer-ucmd.js', 'dashboard-composer-keyboard.js'].map(read).join('\n');
  await page.addScriptTag({ content: '(function(){' + shim + files + '\nwindow.__t = { slashToken: slashToken, dropToken: function () { cexecToken = ""; } };})();' });
  const ed = page.locator('.cfield [contenteditable]').first();
  const type = async text => { await ed.click(); await page.keyboard.press('Control+A'); await page.keyboard.press('Delete'); await page.keyboard.insertText(text); };
  const state = () => page.evaluate(() => ({ v: ctext.value, bang: composer.hasAttribute('data-bang'), hl: CSS.highlights.has('ws-bang'), sel: [ctext.selectionStart, ctext.selectionEnd] }));
  return { page, ed, type, state, errs };
}

test('shell mode flag follows the first character, paints the bang, and keeps 0px height growth', { skip }, async t => {
  const { page, type, state, errs } = await open(t);
  await page.waitForSelector('.d-ucmd .ucmd');
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
  await page.waitForSelector('.d-ucmd .ucmd');
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

  // 3) User command slot click goes through headless tracker with origin: 'ucmd'.
  await page.evaluate(() => {
    window.__onPoll = function (id, n, res) {
      return res({ id: id, command: 'make test\n./run --verbose', phase: 'done', exec_id: 'exec_ucmd1' });
    };
  });
  await page.click('.ucmd-run');
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

test('slot blocks show the whole stored line on a dark $ block', { skip }, async t => {
  const { page } = await open(t);
  await page.waitForSelector('.d-ucmd .ucmd');
  assert.equal(await page.locator('.ucmd-line').textContent(), 'make test\n./run --verbose');
  assert.equal(await page.locator('.ucmd-run').textContent(), '▶ Run tests');
  assert.equal(await page.evaluate(() => getComputedStyle(document.querySelector('.ucmd')).backgroundColor), 'rgb(19, 20, 23)');
  assert.equal(await page.evaluate(() => getComputedStyle(document.querySelector('.ucmd-line'), '::before').content), '"$ "');
});

test('light theme tints the terminal surfaces Solarized, dark stays dark, with 0px height delta', { skip }, async t => {
  const { page, type } = await open(t);
  await page.waitForSelector('.d-ucmd .ucmd');
  const hDark = await page.evaluate(() => composer.getBoundingClientRect().height);
  const ucmdBg = () => page.evaluate(() => getComputedStyle(document.querySelector('.ucmd')).backgroundColor);
  const edBg = () => page.evaluate(() => getComputedStyle(document.querySelector('.cfield [contenteditable]')).backgroundColor);
  assert.equal(await ucmdBg(), 'rgb(19, 20, 23)', 'dark theme ucmd');
  await page.emulateMedia({ colorScheme: 'light' });
  const hLightIdle = await page.evaluate(() => composer.getBoundingClientRect().height);
  assert.equal(hLightIdle, hDark, 'matching baseline height across themes');
  assert.equal(await ucmdBg(), 'rgb(253, 246, 227)', 'light theme ucmd: Solarized base3');
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
