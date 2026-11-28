const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const js = fs.readFileSync(path.join(__dirname, 'assets/dashboard-drawer.js'), 'utf8');
const css = fs.readFileSync(path.join(__dirname, 'assets/dashboard-drawer.css'), 'utf8');
function section(start, end) {
  const a = js.indexOf(start), b = js.indexOf(end, a);
  assert.ok(a >= 0 && b > a, start);
  return js.slice(a, b);
}

test('the search sheet fills the available room, ignores stale heights and follows geometry', () => {
  let headerBottom = 48, composerTop = 1020, composerVisible = true;
  const c = vm.createContext({ window: { innerWidth: 1920, innerHeight: 1080 },
    load: () => { throw Error('height must not be read'); }, save: () => { throw Error('height must not be written'); },
    drheader: { getBoundingClientRect: () => ({ bottom: headerBottom }) },
    shandle: { offsetHeight: 34 }, $: () => ({ getBoundingClientRect: () => ({ top: composerTop }) }),
    visible: () => composerVisible });
  vm.runInContext(section('  var DR_SEARCH =', '  var sbody ='), c);
  vm.runInContext(section('  function drHeaderBottom()', '  // drApply lays'), c);
  for (const width of [1920, 760, 759, 420]) {
    c.window.innerWidth = width;
    assert.equal(c.drMax(), 932, `search at ${width}px keeps composer clearance without an 80vh cap`);
  }
  composerTop = 800; assert.equal(c.drMax(), 712, 'composer growth');
  headerBottom = 80; assert.equal(c.drMax(), 680, 'header wrapping');
  composerVisible = false; assert.equal(c.drMax(), 1000, 'no composer reaches viewport bottom');
  c.window.innerHeight = 900; assert.equal(c.drMax(), 820, 'window resize');
  composerVisible = true; composerTop = 90; assert.equal(c.drMax(), 0, 'no negative room');
  assert.doesNotMatch(js, /ws\.dashboard\.drawer\.h|drDrag|drSetH|DR_DEF|DR_MIN/);
  assert.match(js, /drGeometry\.observe\(\$\("composer"\)\)/);
  assert.match(js, /var h = drMax\(\)/);
});

test('only the search handle opens the drawer and hides while its sheet shows', () => {
  assert.match(css, /\.drawer\.open\.search #shandle \{ visibility: hidden; pointer-events: none; \}/);
  assert.doesNotMatch(css, /\.drawer\.open\.git|#r-git|#drhandle|\.drawer\.panels|\.dr-wait|\.dr-handle \.tdot/, 'the git and panels sheets retired');
  assert.doesNotMatch(css, /cursor: grab|touch-action: none|\.drawer\.drag/);
  assert.match(js, /shandle\.addEventListener\("click", function \(\) \{ drSetOpen\(!drOpen, "search"\); \}\)/);
  assert.match(js, /drin\.insertBefore\(drfold, shandle\)/, 'the fold button anchors on the search handle');
  assert.doesNotMatch(js, /drhandle|drbody|drcards|drawerSync|drFill|cardTab|drEntries|DR_OPEN|hdLoad/);
  assert.doesNotMatch(js, /drSwap|function drawerSwapped/);
});

test('the fold button only folds: the Workpanel rail slot carries the approval wait', () => {
  const classes = () => ({ set: new Set(), toggle(k, on) { if (on) this.set.add(k); else this.set.delete(k); }, contains(k) { return this.set.has(k); } });
  const labelled = () => ({ classList: classes(), attrs: {}, getAttribute(k) { return this.attrs[k]; }, setAttribute(k, v) { this.attrs[k] = v; } });
  const drfold = labelled();
  const c = vm.createContext({ drfold, wsT: k => k });
  vm.runInContext(section('  function drTitle()', '  function drHeaderBottom()'), c);
  c.drTitle();
  assert.equal(drfold.attrs['aria-label'], 'js.drawer.fold');
  assert.equal(drfold.title, 'js.drawer.fold');
  const wp = fs.readFileSync(path.join(__dirname, 'assets/dashboard-workpanel.js'), 'utf8');
  const wait = wp.slice(wp.indexOf('  function drawerWait()'), wp.indexOf('  // wpGallery follows'));
  assert.doesNotMatch(wait, /drhandle|drfold|drTitle/, 'drawerWait no longer reaches the drawer');
  assert.doesNotMatch(js, /drFoldWait/);
});

test('the closed handle opens the full sheet and the fold closes back to it', () => {
  function node() {
    const classes = new Set();
    return { style: { setProperty() {} }, attrs: {}, listeners: {}, hidden: false,
      classList: { toggle(k, on) { if (on) classes.add(k); else classes.delete(k); }, contains: k => classes.has(k) },
      setAttribute(k, v) { this.attrs[k] = v; }, addEventListener(k, fn) { this.listeners[k] = fn; },
      contains: () => false, focus() { c.doc.activeElement = this; } };
  }
  const nodes = Object.fromEntries(['drawer', 'drsheet', 'drclip', 'sbody', 'shandle', 'drfold'].map(k => [k, node()]));
  const saved = [], searches = [];
  const c = vm.createContext({ ...nodes, doc: { activeElement: null },
    drHeaderBottom: () => 48, drMax: () => 812, drTitle() {}, drAnnounce() {},
    wsT: k => k, save: (k, v) => saved.push([k, v]),
    tkApi: { openSearch: () => searches.push('open'), closeSearch: () => searches.push('close') } });
  vm.runInContext(section('  var DR_SEARCH =', '  var sbody ='), c);
  c.drfold = nodes.drfold; c.shandle = nodes.shandle; c.sbody = nodes.sbody;
  vm.runInContext(section('  function drFoldShow(mode)', '  function drTitle()'), c);
  vm.runInContext(section('  function drApply(anim)', '  // The session drawer and a tracker'), c);
  vm.runInContext(section('  function drSetOpen(open, mode)', '  if (drawer) {'), c);
  vm.runInContext('shandle.addEventListener("click", function () { drSetOpen(!drOpen, "search"); });', c);
  c.drSetOpen(true, 'panels');
  assert.equal(c.drOpen, false, 'a retired panels mode opens nothing');
  nodes.shandle.listeners.click();
  assert.equal(c.drOpen, true, 'the closed handle opens');
  assert.equal(c.drMode, 'search');
  assert.equal(nodes.drsheet.style.height, '812px');
  assert.equal(nodes.drclip.style.height, '812px');
  assert.equal(nodes.drawer.classList.contains('search'), true, 'the handle hiding selector is enabled');
  assert.equal(nodes.drfold.hidden, false);
  assert.deepEqual(searches, ['open']);
  c.doc.activeElement = nodes.drfold; c.drFold();
  assert.equal(c.drOpen, false);
  assert.equal(nodes.drclip.style.height, '0px');
  assert.equal(nodes.drfold.hidden, true);
  assert.equal(c.doc.activeElement, nodes.shandle, 'fold returns keyboard focus to the visible handle');
  assert.deepEqual(searches, ['open', 'close']);
  assert.deepEqual(saved, [['ws.dashboard.drawer.search', '1'], ['ws.dashboard.drawer.search', null]]);
});

test('retired #p-tickets and #p-mailbox drawer tabs and ticketIdeaSync are absent', () => {
  assert.equal(js.includes('panelTab("tickets"'), false);
  assert.equal(js.includes('panelTab("mailbox"'), false);
  assert.equal(js.includes('ticketIdeaSync'), false);
  assert.equal(css.includes('#p-tickets'), false);
  assert.equal(css.includes('#p-mailbox'), false);
});

test('the drawer carries no cards: the holders card moved to the shell bottom bar', () => {
  for (const gone of ['panelTab(', 'cardTab(', 'notesLoad', 'notesRender', 'noteslist', 'notesrefresh', 'function drawerWait', 'hdlist', 'hdcount']) {
    assert.equal(js.includes(gone), false, gone);
  }
  for (const gone of ['.notes-', '.notescard', '#p-notes', '#p-todo', '#p-agenda', '.r-panels', '#drbody', '#p-holders', '.hdcard', '.hdrow', '.hdstop']) {
    assert.equal(css.includes(gone), false, gone);
  }
  const tmpl = fs.readFileSync(path.join(__dirname, 'assets/dashboard.tmpl'), 'utf8');
  assert.equal(tmpl.includes('{{define "drcards"}}'), false, 'the drawer cards template retired');
  assert.equal(/id="(p-holders|drbody|drhandle|hdlist|hdcount)"/.test(tmpl), false);
  const drawer = tmpl.slice(tmpl.indexOf('{{define "drawer"}}'), tmpl.indexOf('{{end}}', tmpl.indexOf('{{define "drawer"}}')));
  assert.match(drawer, /<div class="dr-sheet" id="drsheet" role="region" aria-label="\{\{t "js\.tk\.qs\.tip"\}\}"><\/div>/, 'an empty sheet the search body fills');
  assert.match(css, /@media \(min-width: 1600px\)[\s\S]*?width: min\(1280px,/);
});
