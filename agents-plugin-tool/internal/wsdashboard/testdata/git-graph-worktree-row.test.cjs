// Run: node --test agents-plugin-tool/internal/wsdashboard/testdata/git-graph-worktree-row.test.cjs
//
// The Git page graph's working-tree row: glDraw (dashboard-git.js) with
// glWtRow / glWtSync / glOpenRow (dashboard-gitpage-wt.js) run in a vm
// context over a fake DOM. Pins the slot shift, the hollow node's geometry
// (on HEAD's column, on a stub lane when a lane from above holds that
// column, alone at the top when HEAD is unborn or not loaded), the row on
// an empty log, the status signature (no-op, in-place patch, redraw), the
// "300+" count of a cut group, and the row's open path never reading
// gl.commits.
const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const git = readAssetSource('wsdashboard', 'dashboard-git.js');
const wt = readAssetSource('wsdashboard', 'dashboard-gitpage-wt.js');

// fn is one top-level function of a script: a one-line body, or up to its
// closing brace at the script's indentation.
function fn(src, name) {
  const at = src.indexOf('\n  function ' + name + '(');
  assert.ok(at >= 0, name + ' is missing');
  const line = src.slice(at + 1, src.indexOf('\n', at + 1));
  if (/\}\s*$/.test(line) && !/\{\s*$/.test(line)) return line + '\n';
  const end = src.indexOf('\n  }\n', at);
  assert.ok(end > at, name + ' has no end');
  return src.slice(at + 1, end + 5);
}
function line(src, prefix) {
  const at = src.indexOf('\n  ' + prefix);
  assert.ok(at >= 0, prefix + ' is missing');
  return src.slice(at + 1, src.indexOf('\n', at + 1)) + '\n';
}

class El {
  constructor(tag, cls, text) {
    this.tag = tag; this.cls = cls || ''; this._text = text == null ? '' : String(text);
    this.children = []; this.attrs = {}; this.style = { setProperty: (k, v) => { this.style[k] = v; } };
    this.title = ''; this.hidden = false; this.tabIndex = -1; this.scrollTop = 0; this.frag = false;
  }
  get textContent() { return this._text + this.children.map(c => c.textContent).join(''); }
  set textContent(v) { this._text = String(v); this.children = []; }
  get className() { return this.cls; }
  get classList() {
    const self = this, list = () => self.cls.split(/\s+/).filter(Boolean);
    return {
      contains: k => list().includes(k),
      add: k => { if (!list().includes(k)) self.cls = list().concat(k).join(' '); },
      remove: k => { self.cls = list().filter(c => c !== k).join(' '); },
    };
  }
  appendChild(c) { if (c.frag) { this.children.push(...c.children); c.children = []; } else this.children.push(c); return c; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  hasAttribute(k) { return k in this.attrs; }
  removeAttribute(k) { delete this.attrs[k]; }
  matches(sel) {
    const m = sel.match(/^(\w+)?(?:\.([\w-]+))?(?:\[([\w-]+)\])?$/);
    return !!m && (!m[1] || this.tag === m[1]) && (!m[2] || this.classList.contains(m[2])) && (!m[3] || this.hasAttribute(m[3]));
  }
  all(sel, out = []) { for (const c of this.children) { if (c.matches(sel)) out.push(c); c.all(sel, out); } return out; }
  querySelector(sel) { return this.all(sel)[0] || null; }
}

function setup() {
  const glrows = new El('div', 'glrows'), glscroll = new El('div', 'glscroll'), glcount = new El('span', 'chip');
  const calls = { open: [], worktree: [] };
  const ctx = vm.createContext({
    glrows, glscroll, glcount, gitLast: null,
    gw: { oid: '', worktree: false, back: null },
    doc: { createDocumentFragment() { const f = new El('#frag'); f.frag = true; return f; } },
    mk: (tag, cls, text) => new El(tag, cls, text),
    svgEl: (tag, attrs) => { const e = new El(tag, attrs && attrs['class']); e.attrs = Object.assign({}, attrs); return e; },
    wsT: (k, ...a) => (a.length ? k + ':' + a.join(',') : k),
    luIcon: name => new El('svg', 'i ' + name),
    glPills: () => [], glAgo: () => 'now', glWhen: () => 'when', glTipHide() {},
    gwOpen: oid => calls.open.push(oid),
    gwWorktree: (side, i) => calls.worktree.push([side, i]),
  });
  vm.runInContext(line(git, 'var GL_ROW = '), ctx);
  for (const name of ['glX', 'glY', 'glLane', 'glPath', 'glIsFrontier', 'glFind', 'glDraw', 'glOpenRow']) vm.runInContext(fn(git, name), ctx);
  vm.runInContext(line(wt, 'var gpWt = ').replace(/\$\("[^"]+"\)/g, 'null'), ctx);
  for (const name of ['glWtCounts', 'glWtSig', 'glWtNum', 'glWtSync', 'glWtRow', 'glWtFill', 'glWtOpen']) vm.runInContext(fn(wt, name), ctx);
  ctx.gl = { commits: [], by: {}, width: 1, more: 0, page: 0, head: '', seq: 0, busy: false, loaded: true, tipRow: null, frontier: '', err: '' };
  return { ctx, glrows, calls };
}

function commit(oid, col, down, extra) {
  return Object.assign({ oid, col, color: col, parents: ['p'], subject: 's-' + oid, author: 'a', time: 0, refs: [], down }, extra);
}
const DIRTY = { staged: [{ path: 'a', add: 3, del: 1 }], unstaged: [{ path: 'b', add: 2, del: 0 }, { path: 'c', add: 0, del: 4 }], untracked: [] };
const rows = g => g.children.filter(c => c.classList.contains('glr'));
const svg = g => g.children.find(c => c.tag === 'svg');
const kids = (s, cls) => s.all('.' + cls);

test('HEAD at the top: the row takes slot 0 and every commit moves one slot down', () => {
  const { ctx, glrows } = setup();
  ctx.gl.head = 'aaa';
  ctx.gl.commits = [commit('aaa', 0, [{ from: 0, to: 0, color: 0 }]), commit('bbb', 0, [{ from: 0, to: 0, color: 0 }]), commit('ccc', 0, [])];
  ctx.gitLast = DIRTY;
  ctx.glDraw();
  const r = rows(glrows);
  assert.deepEqual(r.map(x => x.getAttribute('data-worktree') ? 'wt' : x.getAttribute('data-i')), ['wt', '0', '1', '2']);
  const s = svg(glrows), node = kids(s, 'wt')[0], head = kids(s, 'head')[0];
  assert.deepEqual([node.attrs.cx, node.attrs.cy, node.attrs.r], [12, 14, 5]);
  assert.equal(head.attrs.cy, 42);
  assert.equal(kids(s, 'dot').map(d => d.attrs.cy).join(), '42,70,98');
  const conn = s.all('.cont').find(p => p.attrs.d.startsWith('M12 14'));
  assert.equal(conn.attrs.d, 'M12 14V42');
  assert.equal(s.attrs.height, 4 * 28 + 34);
  assert.equal(r[0].attrs.role, 'button');
  assert.equal(r[0].tabIndex, 0);
  assert.equal(ctx.glcount.textContent, '3');
});

test('HEAD in the middle: the row sits right above HEAD on its column', () => {
  const { ctx, glrows } = setup();
  ctx.gl.width = 2;
  ctx.gl.head = 'hhh';
  ctx.gl.commits = [commit('xxx', 1, [{ from: 1, to: 1, color: 1 }]), commit('hhh', 0, [{ from: 0, to: 0, color: 0 }, { from: 1, to: 1, color: 1 }]), commit('zzz', 0, [])];
  ctx.gitLast = DIRTY;
  ctx.glDraw();
  assert.deepEqual(rows(glrows).map(x => x.getAttribute('data-worktree') ? 'wt' : x.getAttribute('data-i')), ['0', 'wt', '1', '2']);
  const s = svg(glrows), node = kids(s, 'wt')[0];
  assert.deepEqual([node.attrs.cx, node.attrs.cy], [12, 42]);
  assert.equal(kids(s, 'head')[0].attrs.cy, 70);
  // The lane from above runs through the row's slot on its own column.
  assert.ok(s.all('path').some(p => p.attrs.d === 'M26 14V70'));
  assert.ok(s.all('.cont').some(p => p.attrs.d === 'M12 42V70'));
  assert.equal(s.attrs.width, 26 + 12);
});

test('a lane from above into HEAD: the node moves to a stub lane and curves into HEAD', () => {
  const { ctx, glrows } = setup();
  ctx.gl.head = 'hhh';
  ctx.gl.commits = [commit('xxx', 0, [{ from: 0, to: 0, color: 0 }]), commit('hhh', 0, [])];
  ctx.gitLast = DIRTY;
  ctx.glDraw();
  const s = svg(glrows), node = kids(s, 'wt')[0];
  assert.deepEqual([node.attrs.cx, node.attrs.cy], [26, 42]);
  assert.ok(s.all('.cont').some(p => p.attrs.d === 'M26 42C26 56 12 56 12 70'));
  assert.equal(s.attrs.width, 26 + 12, 'the graph column widens by the stub lane');
});

test('an unborn HEAD or an empty log still yields the row, at slot 0, alone', () => {
  const { ctx, glrows } = setup();
  ctx.gl.head = '';
  ctx.gitLast = { staged: [], unstaged: [], untracked: [{ path: 'new' }] };
  ctx.glDraw();
  const r = rows(glrows);
  assert.equal(r.length, 1);
  assert.ok(r[0].hasAttribute('data-worktree'));
  assert.ok(glrows.children.some(c => c.classList.contains('gempty') && c.textContent === 'js.git.log.empty'));
  const s = svg(glrows), node = kids(s, 'wt')[0];
  assert.deepEqual([node.attrs.cx, node.attrs.cy], [12, 14]);
  assert.equal(s.all('.cont').length, 0, 'no connector without a HEAD row');
  assert.equal(ctx.glcount.hidden, true, 'the commit count counts commits only');
});

test('HEAD not among the loaded rows: the row stands alone at slot 0', () => {
  const { ctx, glrows } = setup();
  ctx.gl.head = 'elsewhere';
  ctx.gl.commits = [commit('aaa', 0, [])];
  ctx.gitLast = DIRTY;
  ctx.glDraw();
  assert.deepEqual(rows(glrows).map(x => x.getAttribute('data-worktree') ? 'wt' : x.getAttribute('data-i')), ['wt', '0']);
  const s = svg(glrows);
  assert.deepEqual([kids(s, 'wt')[0].attrs.cy, kids(s, 'dot')[0].attrs.cy], [14, 42]);
  assert.equal(s.all('.cont').length, 0);
});

test('a clean tree draws no row and no shift', () => {
  const { ctx, glrows } = setup();
  ctx.gl.head = 'aaa';
  ctx.gl.commits = [commit('aaa', 0, [])];
  ctx.gitLast = { staged: [], unstaged: [], untracked: [] };
  ctx.glDraw();
  assert.equal(glrows.querySelector('.glr[data-worktree]'), null);
  assert.equal(kids(svg(glrows), 'head')[0].attrs.cy, 14);
});

test('the status signature: unchanged is a no-op, changed counts patch in place, clean/dirty redraws', () => {
  const { ctx, glrows } = setup();
  ctx.gl.head = 'aaa';
  ctx.gl.commits = [commit('aaa', 0, [])];
  let draws = 0;
  const draw = ctx.glDraw;
  ctx.glDraw = () => { draws++; draw(); };
  ctx.gitLast = DIRTY; ctx.glWtSync(DIRTY);
  assert.equal(draws, 1, 'clean to dirty draws');
  const row = glrows.querySelector('.glr[data-worktree]');
  assert.equal(row.querySelector('.glwt-n').textContent, '3');
  ctx.glWtSync(JSON.parse(JSON.stringify(DIRTY)));
  assert.equal(draws, 1, 'the same counts draw nothing');
  const more = JSON.parse(JSON.stringify(DIRTY));
  more.untracked.push({ path: 'd' });
  ctx.gitLast = more; ctx.glWtSync(more);
  assert.equal(draws, 1, 'changed counts patch the row in place');
  assert.equal(glrows.querySelector('.glr[data-worktree]'), row, 'the same row element');
  assert.equal(row.querySelector('.glwt-n').textContent, '4');
  assert.match(row.attrs['aria-label'], /^js\.git\.wt\.aria:4,1,2,1$/);
  assert.match(row.title, /js\.git\.wt\.lines:5,5/);
  const clean = { staged: [], unstaged: [], untracked: [] };
  ctx.gitLast = clean; ctx.glWtSync(clean);
  assert.equal(draws, 2, 'dirty to clean draws');
  assert.equal(glrows.querySelector('.glr[data-worktree]'), null);
});

test('a group the server cut counts as 300+', () => {
  const { ctx, glrows } = setup();
  const files = Array.from({ length: 300 }, (_, i) => ({ path: 'f' + i, add: 1, del: 0 }));
  ctx.gitLast = { staged: [], unstaged: files, untracked: [], truncated: true };
  ctx.glDraw();
  const row = glrows.querySelector('.glr[data-worktree]');
  assert.equal(row.querySelector('.glwt-n').textContent, '300+');
  assert.deepEqual(row.all('.glwt-g').map(c => c.textContent), ['js.git.unstaged 300+']);
  assert.match(row.title, /js\.git\.truncated:300/);
});

test('opening the row opens the working tree and never reads gl.commits', () => {
  const { ctx, glrows, calls } = setup();
  ctx.gitLast = { staged: [], unstaged: [{ path: 'b' }], untracked: [{ path: 'c' }] };
  ctx.glDraw();
  const row = glrows.querySelector('.glr[data-worktree]');
  ctx.gl.commits = new Proxy([], { get(t, k) { if (k === 'length' || typeof k === 'symbol') return Reflect.get(t, k); throw new Error('read gl.commits[' + String(k) + ']'); } });
  ctx.glOpenRow(row);
  assert.deepEqual(calls.worktree, [['unstaged', 0]]);
  assert.deepEqual(calls.open, []);
  ctx.gl.commits = [commit('aaa', 0, [])];
  const r = new El('div', 'glr'); r.setAttribute('data-i', '0');
  ctx.glOpenRow(r);
  assert.deepEqual(calls.open, ['aaa']);
});
