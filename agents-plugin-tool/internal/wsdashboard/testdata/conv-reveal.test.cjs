// Run locally: node --test agents-plugin-tool/internal/wsdashboard/testdata/conv-reveal.test.cjs
// Covers conv-reveal rail highlight / badge / ancestor expansion / focus navigation
// (dashboard-rail.js) and #entry=<key> 5-page pagination / wb-fold reveal / flash /
// not-found notice (dashboard-conversation.js).
const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

class El {
  constructor(tag, cls = '', text = '') {
    this.nodeType = 1;
    this.tagName = tag.toUpperCase();
    this.tag = tag;
    this.cls = cls || '';
    this.attrs = {};
    this.children = [];
    this.parentNode = null;
    this.hidden = false;
    this.open = false;
    this.isConnected = true;
    this.scrollTop = 0;
    this.scrollHeight = 1000;
    this.offsetWidth = 100;
    this.scrolled = [];
    this.text = text == null ? '' : String(text);
    const self = this;
    this.classList = {
      toggle(k, on) {
        const s = new Set(self.cls.split(/\s+/).filter(Boolean));
        if (on === undefined ? !s.has(k) : on) s.add(k); else s.delete(k);
        self.cls = [...s].join(' ');
      },
      add(k) { this.toggle(k, true); },
      remove(k) { this.toggle(k, false); },
      contains(k) { return self.cls.split(/\s+/).includes(k); }
    };
  }
  get className() { return this.cls; }
  set className(v) { this.cls = String(v || ''); }
  get firstChild() { return this.children[0] || null; }
  get nextElementSibling() {
    if (!this.parentNode) return null;
    const i = this.parentNode.children.indexOf(this);
    return i >= 0 ? (this.parentNode.children[i + 1] || null) : null;
  }
  get previousElementSibling() {
    if (!this.parentNode) return null;
    const i = this.parentNode.children.indexOf(this);
    return i > 0 ? this.parentNode.children[i - 1] : null;
  }
  appendChild(c) {
    if (c.parentNode) c.parentNode.removeChild(c);
    c.parentNode = this;
    this.children.push(c);
    return c;
  }
  insertBefore(c, ref) {
    if (c.parentNode) c.parentNode.removeChild(c);
    c.parentNode = this;
    const i = ref ? this.children.indexOf(ref) : -1;
    if (i < 0) this.children.push(c); else this.children.splice(i, 0, c);
    return c;
  }
  removeChild(c) {
    const i = this.children.indexOf(c);
    if (i >= 0) this.children.splice(i, 1);
    c.parentNode = null;
    return c;
  }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  set textContent(v) { this.children = []; this.text = v == null ? '' : String(v); }
  get textContent() { return this.text + this.children.map(c => c.textContent).join(''); }
  setAttribute(k, v) { this.attrs[k] = String(v); if (k === 'class') this.cls = String(v); }
  getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null; }
  removeAttribute(k) { delete this.attrs[k]; }
  matches(sel) {
    const classes = new Set(this.cls.split(/\s+/).filter(Boolean));
    for (const m of sel.matchAll(/\.([\w-]+)|\[([\w-]+)(?:="([^"]*)")?\]/g)) {
      if (m[1] && !classes.has(m[1])) return false;
      if (m[2] && !(m[2] in this.attrs)) return false;
      if (m[3] !== undefined && this.attrs[m[2]] !== m[3]) return false;
    }
    return true;
  }
  closest(sel) { for (let n = this; n; n = n.parentNode) if (n.matches(sel)) return n; return null; }
  walk(out = []) { this.children.forEach(c => { out.push(c); c.walk(out); }); return out; }
  querySelectorAll(sel) { return this.walk().filter(n => n.matches(sel)); }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  addEventListener() {}
  scrollIntoView(opts) { this.scrolled.push(opts || true); }
}

const railSrc = readAssetSource('wsdashboard', 'dashboard-rail.js');
const revealBlock = railSrc.match(/  var railRevealState = null,[\s\S]*?\n  \/\/ railFocus is what holds/);
assert.ok(revealBlock, 'railReveal helpers exist in dashboard-rail.js');

function makeRailEnv(opts = {}) {
  const rail = new El('div', 'rail');
  const ws = rail.appendChild(new El('div', 'rws col'));
  ws.setAttribute('data-ws', 'proj1');
  const parentRow = ws.appendChild(new El('div', 'rs kcol'));
  parentRow.setAttribute('data-rid', 'lead1');
  const p1 = parentRow.appendChild(new El('div', 's1'));
  p1.appendChild(new El('a', 'rl', 'Lead 1'));
  p1.appendChild(new El('div', 'drtail'));
  const kids = ws.appendChild(new El('div', 'rkids'));
  const childRow = kids.appendChild(new El('div', 'rs'));
  childRow.setAttribute('data-rid', 'kid1');
  const c1 = childRow.appendChild(new El('div', 's1'));
  c1.appendChild(new El('a', 'rl', 'Kid 1'));
  c1.appendChild(new El('div', 'drtail'));

  const toggledWs = [], toggledKids = [], navs = [], timers = [];
  const doc = {
    hidden: !!opts.hidden,
    hasFocus: () => opts.hasFocus !== false
  };
  const ctx = {
    doc, rid: 'other', shell: false, memoPad: null, railSets: {},
    region: name => name === 'rail' ? rail : null,
    q: (sel, root) => (root || rail).querySelectorAll(sel),
    mk: (tag, cls, text) => new El(tag, cls, text),
    wsT: k => k,
    railWide: () => true,
    railShown: () => false,
    reducedMotion: () => false,
    colSet: { toggle: k => toggledWs.push(k) },
    kidSet: { toggle: k => toggledKids.push(k) },
    WsRail: {
      marks() {},
      refind(r, spec) {
        return r.querySelectorAll('.' + spec.cls).find(n => n.getAttribute(spec.attr) === spec.val) || null;
      }
    },
    saveDraft() {},
    railOpen() {},
    go: href => navs.push(href),
    setTimeout: (fn, ms) => { timers.push({ fn, ms, cleared: false }); return timers.length; },
    clearTimeout: id => { if (timers[id - 1]) timers[id - 1].cleared = true; }
  };
  vm.createContext(ctx);
  vm.runInContext(revealBlock[0].replace(/\n  \/\/ railFocus is what holds$/, '') + '\nthis.api = { railReveal, railRevealApply, railRevealClear };', ctx);
  return { ctx, rail, ws, parentRow, childRow, toggledWs, toggledKids, navs, timers };
}

test('railReveal expands collapsed ancestors, pulses row, mounts jump badge, and survives swap', () => {
  const env = makeRailEnv();
  env.ctx.api.railReveal({ replyId: 'kid1', entryId: 'e42', reason: 'matched turn' });
  assert.equal(env.ws.classList.contains('col'), false, 'workspace expanded');
  assert.equal(env.parentRow.classList.contains('kcol'), false, 'parent expanded');
  assert.deepEqual(env.toggledWs, ['proj1']);
  assert.deepEqual(env.toggledKids, ['lead1']);
  assert.equal(env.childRow.classList.contains('conv-reveal-pulse'), true);
  const badge = env.childRow.querySelector('.conv-reveal-jump');
  assert.ok(badge, 'jump badge mounted');
  assert.equal(badge.href, '/s/kid1#entry=e42');
  assert.equal(badge.textContent, 'js.conv.reveal.jump');
  assert.equal(badge.title, 'matched turn');
  assert.equal(env.childRow.scrolled.length, 1);
  assert.deepEqual(env.navs, [], 'focus omitted/false does not auto-navigate');

  // Simulate rail swap replacing DOM nodes -> railRevealApply restores highlight & badge.
  env.childRow.classList.remove('conv-reveal-pulse');
  badge.remove();
  env.ctx.api.railRevealApply(env.rail, false);
  assert.equal(env.childRow.classList.contains('conv-reveal-pulse'), true);
  assert.ok(env.childRow.querySelector('.conv-reveal-jump'), 'badge re-mounted after swap');

  // Timer auto-clears pulse and badge.
  env.timers[0].fn();
  assert.equal(env.childRow.classList.contains('conv-reveal-pulse'), false);
  assert.equal(env.childRow.querySelector('.conv-reveal-jump'), null);
});

test('railReveal with focus:true navigates only when tab is visible and focused', () => {
  const active = makeRailEnv({ hidden: false, hasFocus: true });
  active.ctx.api.railReveal({ replyId: 'lead1', entryId: 'e9', focus: true });
  assert.deepEqual(active.navs, ['/s/lead1#entry=e9']);

  const bg = makeRailEnv({ hidden: true, hasFocus: true });
  bg.ctx.api.railReveal({ replyId: 'lead1', entryId: 'e9', focus: true });
  assert.deepEqual(bg.navs, [], 'hidden tab does not navigate');

  const unfocused = makeRailEnv({ hidden: false, hasFocus: false });
  unfocused.ctx.api.railReveal({ replyId: 'lead1', entryId: 'e9', focus: true });
  assert.deepEqual(unfocused.navs, [], 'unfocused tab does not navigate');
});

const convSrc = readAssetSource('wsdashboard', 'dashboard-conversation.js');
const histAndJumpBlock = convSrc.match(/  var histBefore = null,[\s\S]*?\n  \/\/ --- chat jump arrow/);
assert.ok(histAndJumpBlock, 'fetchHistoryPage and entryJump helpers exist in dashboard-conversation.js');

function makeConvEnv(pagesByBefore = {}) {
  const convo = new El('div', 'convo');
  convo.scrollTop = 420;
  convo.scrollHeight = 1000;
  const box = convo.appendChild(new El('div', 'centries'));
  function addEntry(key, extra = {}) {
    const el = new El('div', 'msg', key);
    el.setAttribute('data-entry', key);
    el.setAttribute('data-at', '2026-11-28T00:00:00Z');
    if (extra.wbFolded) el.setAttribute('data-wb-folded', '1');
    return el;
  }
  box.appendChild(addEntry('e50'));
  box.appendChild(addEntry('e51'));

  const fetched = [], timers = [];
  const doc = { createElement: tag => new El(tag) };
  const win = {
    fetch: url => {
      fetched.push(url);
      const u = new URL(url, 'http://localhost');
      const before = u.searchParams.get('before') || '';
      const page = pagesByBefore[before];
      if (!page) return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ html: '', before: '', end: true }) });
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(page) });
    },
    addEventListener() {}
  };
  const ctx = {
    rid: 'r1', convo, doc, window: win, fetch: win.fetch, location: { hash: '' },
    grid: null, stick: true, shHidden: false,
    wsT: k => k,
    visible: () => true,
    entriesBox: () => box,
    firstEntryEl: b => b.querySelector('[data-entry]'),
    entryKeys: b => {
      const m = {};
      b.querySelectorAll('[data-entry]').forEach(n => { m[n.getAttribute('data-entry')] = n; });
      return m;
    },
    frag: html => html,
    mergeEntries: (b, keys) => {
      const list = Array.isArray(keys) ? keys : [];
      const added = [];
      for (let i = list.length - 1; i >= 0; i--) {
        const el = addEntry(list[i]);
        b.insertBefore(el, b.firstChild);
        added.push(el);
      }
      // Simulate prepended DOM height growing scrollHeight by 100px per page.
      if (added.length) convo.scrollHeight += 100;
      return added;
    },
    sweepOutImgs() {},
    scrollRestore: new Set(),
    mermaid() {},
    emitAuditEvent() {},
    reducedMotion: () => false,
    setTimeout: (fn, ms) => { timers.push({ fn, ms }); return timers.length; },
    clearTimeout() {}
  };
  vm.createContext(ctx);
  vm.runInContext(histAndJumpBlock[0].replace(/\n  \/\/ --- chat jump arrow$/, '') + '\nthis.api = { entryJumpFromHash, entryJumpTo, parseEntryHash };', ctx);
  return { ctx, convo, box, fetched, timers };
}

test('entryJumpFromHash flashes an in-DOM entry and reveals wb-folded turn drawer', async () => {
  const env = makeConvEnv();
  const folded = new El('div', 'msg');
  folded.setAttribute('data-entry', 'e52');
  folded.setAttribute('data-wb-folded', '1');
  env.box.appendChild(folded);
  const turn = new El('details', 'd-turn');
  turn.setAttribute('data-conv-turn', '1');
  turn.setAttribute('data-replies', 'e52');
  const clone = turn.appendChild(new El('div', 'msg'));
  clone.setAttribute('data-wb-clone', '1');
  env.box.appendChild(turn);

  const ok = await env.ctx.api.entryJumpFromHash('#entry=e52');
  assert.equal(ok, true);
  assert.equal(turn.open, true, 'folded turn drawer opened');
  assert.equal(clone.classList.contains('conv-entry-flash'), true, 'clone highlighted');
  assert.equal(clone.scrolled.length, 1);
  assert.equal(env.ctx.stick, false);
  assert.equal(env.fetched.length, 0, 'in-DOM hit makes no history fetch');
});

test('entryJumpTo paginates history up to 5 pages to find an older entry', async () => {
  const env = makeConvEnv({
    e50: { html: ['e40', 'e41'], before: 'e40', end: false },
    e40: { html: ['e30', 'e31'], before: 'e30', end: false },
    e30: { html: ['e20', 'e21'], before: 'e20', end: false }
  });
  const ok = await env.ctx.api.entryJumpFromHash('#entry=e21');
  assert.equal(ok, true);
  assert.equal(env.fetched.length, 3);
  const hit = env.box.querySelector('[data-entry="e21"]');
  assert.ok(hit);
  assert.equal(hit.classList.contains('conv-entry-flash'), true);
  assert.equal(hit.scrolled.length, 1);
});

test('entryJumpTo stops at 5 pages, shows not-found notice, and preserves visual scroll offset', async () => {
  const pages = {};
  const cursors = ['e50', 'e40', 'e30', 'e20', 'e10', 'e00'];
  for (let i = 0; i < 6; i++) {
    pages[cursors[i]] = { html: [cursors[i + 1] || 'e_old'], before: cursors[i + 1] || 'e_old', end: false };
  }
  const env = makeConvEnv(pages);
  const beforeFromBottom = env.convo.scrollHeight - env.convo.scrollTop;
  const ok = await env.ctx.api.entryJumpFromHash('#entry=missing_key');
  assert.equal(ok, false);
  assert.equal(env.fetched.length, 5, 'bounded to 5 history pages (250 entries)');
  const notice = env.convo.querySelector('.conv-entry-notice');
  assert.ok(notice, 'not-found notice mounted');
  assert.equal(notice.hidden, false);
  assert.equal(notice.textContent, 'js.conv.entry.not_found');
  assert.equal(env.convo.scrollHeight - env.convo.scrollTop, beforeFromBottom, 'visual scroll position unchanged');
  assert.equal(env.ctx.stick, true, 'stick untouched when not found');
});

test('entryJumpTo flushes wbFoldSync synchronously for paginated replies and matches partial-window clones by entry key', async () => {
  let foldCalls = 0;
  const env = makeConvEnv({
    e50: { html: ['e40', 'e41'], before: 'e40', end: false }
  });
  // Simulate wbOnlyOn() active: turn drawer declares ["e_missing", "e41"] where
  // only e41 exists in the partial DOM window; wbFoldSync folds e41 on demand.
  const turn = new El('details', 'd-turn');
  turn.setAttribute('data-conv-turn', '1');
  turn.setAttribute('data-replies', 'e_missing e41');
  const drawer = turn.appendChild(new El('div', 'd-turn-drawer'));
  drawer.setAttribute('data-conv-turn-drawer', '1');
  env.box.appendChild(turn);

  let cloned = null;
  env.ctx.wbOnlyOn = () => true;
  env.ctx.wbFoldSync = () => {
    foldCalls++;
    const orig = env.box.querySelector('[data-entry="e41"]');
    if (orig && orig.getAttribute('data-wb-folded') !== '1') {
      orig.setAttribute('data-wb-folded', '1');
      cloned = new El('div', 'msg fromlead', 'e41');
      cloned.setAttribute('data-wb-clone', '1');
      cloned.setAttribute('data-wb-clone-entry', 'e41');
      drawer.appendChild(cloned);
    }
  };

  const ok = await env.ctx.api.entryJumpFromHash('#entry=e41');
  assert.equal(ok, true);
  assert.equal(foldCalls, 1, 'wbFoldSync flushed synchronously before revealing target');
  assert.equal(turn.open, true, 'turn drawer opened for newly paginated folded reply');
  assert.ok(cloned, 'clone created for e41');
  assert.equal(cloned.classList.contains('conv-entry-flash'), true, 'exact data-wb-clone-entry clone highlighted');
  assert.equal(cloned.scrolled.length, 1, 'clone scrolled into view');
  const orig = env.box.querySelector('[data-entry="e41"]');
  assert.equal(orig.classList.contains('conv-entry-flash'), false, 'hidden original not flashed');
});
