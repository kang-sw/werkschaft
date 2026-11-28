const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const js = fs.readFileSync(path.join(__dirname, 'assets/dashboard-drawer.js'), 'utf8');
const css = fs.readFileSync(path.join(__dirname, 'assets/dashboard-drawer.css'), 'utf8');
function section(start, end) {
  const a = js.indexOf(start), b = js.indexOf(end, a);
  assert.ok(a >= 0 && b > a);
  return js.slice(a, b);
}

test('every sheet fills the available room, ignores stale heights and follows geometry', () => {
  let headerBottom = 48, composerTop = 1020, composerVisible = true;
  const c = vm.createContext({ window: { innerWidth: 1920, innerHeight: 1080 },
    load: () => { throw Error('height must not be read'); }, save: () => { throw Error('height must not be written'); },
    drheader: { getBoundingClientRect: () => ({ bottom: headerBottom }) },
    drhandle: { offsetHeight: 34 }, $: () => ({ getBoundingClientRect: () => ({ top: composerTop }) }),
    visible: () => composerVisible });
  vm.runInContext(section('  var DR_OPEN =', '  var sbody ='), c);
  vm.runInContext(section('  function drHeaderBottom()', '  // drApply lays'), c);
  for (const width of [1920, 760, 759, 420]) {
    c.window.innerWidth = width;
    for (const mode of ['panels', 'search']) {
      c.drMode = mode;
      assert.equal(c.drMax(), 932, `${mode} at ${width}px keeps composer clearance without an 80vh cap`);
    }
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

test('only the active sheet opener hides; closed handle signals remain in place', () => {
  assert.match(css, /\.drawer\.open\.panels #drhandle, \.drawer\.open\.search #shandle \{ visibility: hidden; pointer-events: none; \}/);
  assert.doesNotMatch(css, /\.drawer\.open\.git|#r-git/, 'the git sheet retired with the Git page');
  assert.doesNotMatch(css, /cursor: grab|touch-action: none|\.drawer\.drag/);
  assert.match(css, /\.dr-handle\.wait \.dr-wait \{ display: inline-flex; \}/);
  assert.match(js, /drhandle\.addEventListener\("click", function \(\) \{\s*drSetOpen\(!\(drOpen && drMode === "panels"\), "panels"\)/);
  assert.match(js, /drhandle\.setAttribute\("aria-label", t\)/);
  // #r-panels moved to the Workpanel: only a section badge lights the dot.
  assert.match(js, /d\.classList\.toggle\("on", drBadge\)/);
  assert.doesNotMatch(js, /drSwap|function drawerSwapped/);
});

test('the fold button carries the approval wait while the panels sheet hides the handle', () => {
  const classes = () => ({ set: new Set(), toggle(k, on) { if (on) this.set.add(k); else this.set.delete(k); }, contains(k) { return this.set.has(k); } });
  const labelled = () => ({ classList: classes(), attrs: {}, getAttribute(k) { return this.attrs[k]; }, setAttribute(k, v) { this.attrs[k] = v; } });
  const drhandle = labelled(), drfold = labelled();
  let found = [];
  const row = inSource => ({ closest: s => s === '.ag-source' && inSource ? {} : null });
  const c = vm.createContext({ drhandle, drfold, drOpen: true, drMode: 'panels', wsT: k => k, q: () => found, doc: { querySelector: () => null } });
  vm.runInContext(section('  function drTitle()', '  function drHeaderBottom()'), c);
  const wp = fs.readFileSync(path.join(__dirname, 'assets/dashboard-workpanel.js'), 'utf8');
  vm.runInContext(wp.slice(wp.indexOf('  function drawerWait()'), wp.indexOf('  // wpGallery follows')), c);
  const waitLabel = 'js.drawer.fold; js.drawer.waiting';
  for (const [what, rows, want] of [['a waiting row', [row(false)], true], ['the hidden roster alone', [row(true)], false]]) {
    found = rows; c.drawerWait();
    assert.equal(drfold.classList.contains('wait'), want, 'fold wait: ' + what);
    assert.equal(drhandle.classList.contains('wait'), want, 'handle wait: ' + what);
    assert.equal(drfold.attrs['aria-label'], want ? waitLabel : 'js.drawer.fold', 'fold label announces the wait: ' + what);
    assert.equal(drfold.title, drfold.attrs['aria-label']);
  }
  found = [row(false)]; c.drMode = 'search'; c.drawerWait();
  assert.equal(drfold.attrs['aria-label'], 'js.drawer.fold', 'the search slot fold does not show or announce the wait');
  assert.match(css, /\.drawer\.panels \.dr-fold\.wait \.dr-wait \{ display: inline-flex; \}/);
  assert.match(js, /drFoldWait\.appendChild\(gIcon\("lock"\)\);\s*drfold\.appendChild\(drFoldWait\)/);
});

test('closed opener clicks open full sheets and the fold closes back to that opener', () => {
  function node() {
    const classes = new Set();
    return { style: { setProperty() {} }, attrs: {}, listeners: {}, hidden: false,
      classList: { toggle(k, on) { if (on) classes.add(k); else classes.delete(k); }, contains: k => classes.has(k) },
      setAttribute(k, v) { this.attrs[k] = v; }, addEventListener(k, fn) { this.listeners[k] = fn; },
      contains: () => false, focus() { c.doc.activeElement = this; } };
  }
  const nodes = Object.fromEntries(['drawer', 'drsheet', 'drclip', 'drbody', 'sbody', 'drhandle', 'shandle', 'drfold'].map(k => [k, node()]));
  const documentClicks = [], saved = [];
  const c = vm.createContext({ ...nodes, doc: { activeElement: null, addEventListener: (type, fn) => documentClicks.push(fn) },
    drHeaderBottom: () => 48, drMax: () => 812,
    drTitle() {}, drFill() {}, hdLoad() {}, drDot() {}, drAnnounce() {},
    wsT: k => k, save: (k, v) => saved.push([k, v]), tkApi: null });
  vm.runInContext(section('  var DR_OPEN =', '  var sbody ='), c);
  c.drHas = true; c.drfold = nodes.drfold;
  vm.runInContext(section('  function drFoldShow(mode)', '  function drTitle()'), c);
  vm.runInContext(section('  function drApply(anim)', '  // The session drawer and a tracker'), c);
  vm.runInContext(section('  function drSetOpen(open, mode)', '  if (drawer) {'), c);
  vm.runInContext(section('    drhandle.addEventListener("click"', '    // A card\'s title bar'), c);
  for (const [mode, opener, click] of [
    ['panels', nodes.drhandle, () => nodes.drhandle.listeners.click()],
    ['search', nodes.shandle, () => nodes.shandle.listeners.click()]
  ]) {
    click();
    assert.equal(c.drOpen, true, `${mode} closed opener works`);
    assert.equal(c.drMode, mode);
    assert.equal(nodes.drsheet.style.height, '812px');
    assert.equal(nodes.drclip.style.height, '812px');
    assert.equal(nodes.drawer.classList.contains(mode), true, 'active opener hiding selector is enabled');
    assert.equal(nodes.drfold.hidden, false);
    c.doc.activeElement = nodes.drfold; c.drFold();
    assert.equal(c.drOpen, false);
    assert.equal(nodes.drclip.style.height, '0px');
    assert.equal(nodes.drfold.hidden, true);
    assert.equal(c.doc.activeElement, opener, 'fold returns keyboard focus to the visible opener');
  }
  assert.ok(saved.every(([key]) => key !== 'ws.dashboard.drawer.h'));
});

test('retired #p-tickets and #p-mailbox drawer tabs and ticketIdeaSync are absent', () => {
  assert.equal(js.includes('panelTab("tickets"'), false);
  assert.equal(js.includes('panelTab("mailbox"'), false);
  assert.equal(js.includes('ticketIdeaSync'), false);
  assert.equal(css.includes('#p-tickets'), false);
  assert.equal(css.includes('#p-mailbox'), false);
});

test('the retired todo, agenda and notes drawer cards are absent; the holders card stays', () => {
  for (const gone of ['panelTab(', 'cardTab("notes")', 'notesLoad', 'notesRender', 'noteslist', 'notesrefresh', 'function drawerWait']) {
    assert.equal(js.includes(gone), false, gone);
  }
  assert.match(js, /cardTab\("holders"\);/);
  assert.equal(js.includes('cardTab("worktrees")'), false, 'the worktree pool is the Git page\'s');
  assert.match(js, /if \(shown === "panels"\) hdLoad\(\);/);
  for (const gone of ['.notes-', '.notescard', '#p-notes', '#p-todo', '#p-agenda', '.r-panels']) assert.equal(css.includes(gone), false, gone);
  const tmpl = fs.readFileSync(path.join(__dirname, 'assets/dashboard.tmpl'), 'utf8');
  const cards = tmpl.slice(tmpl.indexOf('{{define "drcards"}}'), tmpl.indexOf('{{end}}', tmpl.indexOf('id="p-holders"')) + 7);
  assert.match(cards, /id="p-holders"/);
  assert.equal(/id="p-(worktrees|notes|todo|agenda|tickets|mailbox)"/.test(cards), false, 'the holders card is the drawer\'s only card');
});

test('wide ticket spans suppress the legacy last-row vertical fill', () => {
  const frames = [], cards = Array.from({ length: 5 }, () => ({ children: [], style: {} }));
  const body = { children: cards, hidden: false };
  const c = vm.createContext({ drbody: body, drRoom() {}, requestAnimationFrame: fn => frames.push(fn),
    getComputedStyle: el => el === body ? { display: 'grid', gridTemplateColumns: '300px 300px 300px 300px' }
      : { display: 'block', gridRowStart: 'auto', gridRowEnd: 'auto', gridColumnStart: el === cards[1] ? 'span 2' : 'auto', gridColumnEnd: 'auto' } });
  vm.runInContext(section('  var drFilled =', '  function drDot()'), c);
  c.drFill(); frames.shift()();
  assert.ok(cards.every(card => !card.style.gridRow), 'wide cards retain their two-row dense layout');
});

test('wide grid uses existing breakpoints and dense order; narrow remains the DOM stack', () => {
  assert.match(css, /@media \(min-width: 1600px\)[\s\S]*?width: min\(1280px,/);
  const wide = css.slice(css.indexOf('@media (min-width: 760px)'), css.indexOf('@media (max-width: 759px)'));
  assert.match(wide, /grid-auto-flow: dense/);
  for (const [id, order] of [['holders', 1]]) {
    assert.ok(wide.includes(`#drbody #p-${id} { order: ${order};`));
  }
  for (const id of ['worktrees', 'agenda', 'todo', 'notes']) assert.equal(css.includes(`#p-${id}`), false, `the drawer no longer orders #p-${id}`);
  assert.match(css, /@media \(max-width: 759px\) \{\s*#drbody \{ display: flex; flex-direction: column; \}\s*\.dr-body details.panel \{ flex: none; overflow: hidden; \}\s*\.dr-body details.panel > summary \{ position: static; \}/);
});
