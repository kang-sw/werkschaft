// Composer `!` terminal look, client side (Phase 1 of 261123-feat-ws-composer-bang-terminal-look):
// shell mode flag and amber `!`, the first-Esc rule, no @ or / popup in shell mode, the console
// (fold, clear, tones, status line) and the slot blocks. Runs the real composer scripts in Chromium
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
  ok: { phase: 'done', exit_code: 0, output: 'hi\n', output_bytes: 3, state: 'exited' },
  bad: { phase: 'done', exit_code: 2, output: 'boom\n', output_bytes: 5, state: 'exited' },
  sig: { phase: 'done', signal: 9, output_bytes: 0 },
  stop: { phase: 'done', state: 'stopped', output_bytes: 0 },
  sleep: { phase: 'async', exec_id: 'exec_7f3a2c', output: 'tail\n' },
};
const route = `
window.__slots = { rev: "1.1.x", token: "tok", commands: [{ id: "t", label: "Run tests", description: "d", line: "make test\\n./run --verbose", revision: "r1" }] };
window.fetch = function (url, o) {
  var u = String(url), body = o && o.body ? JSON.parse(o.body) : null, J = ${JSON.stringify(JOBS)};
  function res(v, st) { return Promise.resolve({ ok: !st || st < 400, status: st || 200, json: function () { return Promise.resolve(v); }, text: function () { return Promise.resolve(JSON.stringify(v)); } }); }
  if (/composer-exec$/.test(u) && !body && window.__get) { if (window.__get === "net") return Promise.reject(new TypeError("x")); return Promise.resolve({ ok: false, status: window.__get, json: function () { return Promise.reject(0); }, text: function () { return Promise.resolve(window.__getText + " "); } }); }
  if (/composer-exec$/.test(u) && !body) return res({ token: "tok", cards: [] });
  if (/composer-exec$/.test(u) && body) { var cmd = body.text.slice(1); return res(Object.assign({ id: body.submission_id, command: cmd, token: "tok" }, J[cmd] || J.ok)); }
  if (/composer-exec\\//.test(u)) return res({}, 404);
  if (/files\\?/.test(u)) return res({ items: [{ path: "src/a.go", name: "a.go", dir: "src" }] });
  if (/user-commands$/.test(u)) return res(window.__slots);
  return res({}, 404);
};`;

async function open(t) {
  const { chromium } = require(mod);
  const browser = await chromium.launch({ headless: true });
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
  const files = ['dashboard-composer-exec.js', 'dashboard-composer-ucmd.js', 'dashboard-composer-keyboard.js'].map(read).join('\n');
  await page.addScriptTag({ content: '(function(){' + shim + files + '\nwindow.__t = { cards: cexecCards, slashToken: slashToken, dropToken: function () { cexecToken = ""; } };})();' });
  const ed = page.locator('.cfield [contenteditable]').first();
  const type = async text => { await ed.click(); await page.keyboard.press('Control+A'); await page.keyboard.press('Delete'); await page.keyboard.insertText(text); };
  const state = () => page.evaluate(() => ({ v: ctext.value, bang: composer.hasAttribute('data-bang'), hl: CSS.highlights.has('ws-bang'), sel: [ctext.selectionStart, ctext.selectionEnd] }));
  return { page, ed, type, state, errs };
}

test('shell mode flag follows the first character and paints the bang', { skip }, async t => {
  const { page, type, state, errs } = await open(t);
  await type('!echo hi');
  let s = await state();
  assert.equal(s.bang, true); assert.equal(s.hl, true);
  assert.equal(await page.evaluate(() => ctext.getAttribute('aria-description')), en['js.cexec.bang.aria']);
  assert.equal(await page.evaluate(() => document.getElementById('csend').getAttribute('aria-label')), en['js.cexec.bang.send']);
  assert.equal(await page.evaluate(() => getComputedStyle(document.querySelector('.cfield [contenteditable]')).backgroundColor), 'rgb(19, 20, 23)', 'dark input');
  await type(' !echo hi');
  s = await state();
  assert.equal(s.bang, false, 'a leading space is a prompt, not shell mode'); assert.equal(s.hl, false);
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

test('console: status line, tones, fold, clear', { skip }, async t => {
  const { page, type } = await open(t);
  const run = async text => { await type(text); await page.keyboard.press('Enter'); await page.waitForTimeout(150); };
  await run('!ok');
  const card = (n) => page.locator('.d-cexec .cexec').nth(n);
  assert.equal(await card(0).getAttribute('data-tone'), 'ok');
  assert.match(await card(0).locator('.cexec-stl').textContent(), /✓ exit 0\s*·\s*3 B/);
  assert.equal(await card(0).locator('code').textContent(), 'ok');
  await run('!bad'); await run('!sig'); await run('!stop'); await run('!sleep');
  assert.deepEqual(await page.$$eval('.cexec', els => els.map(e => e.getAttribute('data-tone'))), ['ok', 'fail', 'warn', 'warn', 'run']);
  assert.match(await card(1).locator('.cexec-stl').textContent(), /✗ exit 2/);
  assert.match(await card(4).locator('.cexec-stl').textContent(), /… .*job exec_7f3a2c/);
  const color = sel => page.evaluate(s => getComputedStyle(document.querySelector(s)).color, sel);
  assert.equal(await color('.cexec[data-tone="fail"] .cexec-stl'), 'rgb(240, 121, 126)', 'failure is red');
  assert.equal(await color('.cexec[data-tone="run"] .cexec-stl'), 'rgb(91, 155, 240)', 'running is blue');
  assert.equal(await color('.cexec[data-tone="warn"] .cexec-stl'), 'rgb(224, 168, 74)', 'stop and signal are amber');
  // a running run keeps the console open; the toggle folds it only when idle
  await page.click('.cexec-head');
  assert.equal(await page.locator('.cexec-log').isVisible(), true, 'active run keeps it open');
  // the user's fold applies once the run ends: closing the running card leaves a one-line bar
  await card(4).locator('.cexec-x').click();
  assert.equal(await page.locator('.cexec-log').isVisible(), false, 'idle console folds to one bar');
  assert.match(await page.locator('.cexec-mini').textContent(), /! stop/);
  await page.click('.cexec-head');
  assert.equal(await page.locator('.cexec-log').isVisible(), true);
  // clear closes every run through the per-run close and forgets them
  await page.click('.cexec-clear');
  assert.equal(await page.locator('.cexec').count(), 0);
  assert.equal(await page.locator('.d-cexec').isVisible(), false);
  assert.equal(await page.evaluate(() => sessionStorage.getItem('ws.cexec.fx')), '[]');
});

test('slot blocks show the whole stored line on a dark $ block', { skip }, async t => {
  const { page } = await open(t);
  await page.waitForSelector('.d-ucmd .ucmd');
  assert.equal(await page.locator('.ucmd-line').textContent(), 'make test\n./run --verbose');
  assert.equal(await page.locator('.ucmd-run').textContent(), '▶ Run tests');
  assert.equal(await page.evaluate(() => getComputedStyle(document.querySelector('.ucmd')).backgroundColor), 'rgb(19, 20, 23)');
  assert.equal(await page.evaluate(() => getComputedStyle(document.querySelector('.ucmd-line'), '::before').content), '"$ "');
});

test('light theme tints the terminal surfaces Solarized, dark stays dark', { skip }, async t => {
  const { page } = await open(t);
  await page.waitForSelector('.d-ucmd .ucmd');
  const bg = () => page.evaluate(() => getComputedStyle(document.querySelector('.ucmd')).backgroundColor);
  assert.equal(await bg(), 'rgb(19, 20, 23)', 'dark theme');
  await page.emulateMedia({ colorScheme: 'light' });
  assert.equal(await bg(), 'rgb(253, 246, 227)', 'light theme: Solarized base3');
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
