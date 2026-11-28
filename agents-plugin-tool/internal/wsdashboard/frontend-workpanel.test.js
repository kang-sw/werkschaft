const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const js = fs.readFileSync(path.join(__dirname, 'assets/dashboard-workpanel.js'), 'utf8');
const css = fs.readFileSync(path.join(__dirname, 'assets/dashboard-workpanel.css'), 'utf8');
// The controller's declarations, without its init block (which wires the
// live page's listeners).
const decls = js.slice(0, js.indexOf('  if (wproot) {'));

class El {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase(); this.children = []; this.attrs = {}; this.listeners = {}; this.hidden = false; this.style = {};
    this._text = ''; this._cls = new Set();
    const self = this;
    this.classList = { toggle(k, on) { if (on === undefined) on = !self._cls.has(k); if (on) self._cls.add(k); else self._cls.delete(k); }, contains: k => self._cls.has(k), add: k => self._cls.add(k) };
  }
  get className() { return [...this._cls].join(' '); }
  set className(v) { this._cls = new Set(String(v).split(/\s+/).filter(Boolean)); }
  get textContent() { return this._text + this.children.map(c => c.textContent).join(''); }
  set textContent(v) { this._text = String(v); this.children = []; }
  appendChild(el) { el.parent = this; this.children.push(el); return el; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  hasAttribute(k) { return k in this.attrs; }
  addEventListener(k, fn) { this.listeners[k] = fn; }
  all() { return this.children.flatMap(c => [c, ...c.all()]); }
  find(cls) { return this.all().filter(e => e.classList.contains(cls)); }
  querySelector(sel) { return this.all().find(e => sel[0] === '.' ? e.classList.contains(sel.slice(1)) : e.tagName === sel.toUpperCase()) || null; }
}
const tick = () => new Promise(resolve => setImmediate(resolve));

function harness(extra = {}) {
  const ids = {}, byId = id => ids[id] || (ids[id] = new El('div'));
  ['wpnoteslist', 'wpnotesrefresh', 'wpnotests', 'wpmanualslist'].forEach(byId);
  ids.wpnotesq = Object.assign(new El('input'), { value: '' });
  ids.wpmanualsq = Object.assign(new El('input'), { value: '' });
  const chips = ['all', 'worktree', 'repo', 'clone', 'machine'].map(ly => {
    const b = new El('button'); b.className = 'wp-lchip'; b.setAttribute('data-layer', ly); b.appendChild(new El('em')); return b;
  });
  const counts = { notes: new El('span'), manuals: new El('span') }, clears = { wpnotesq: new El('button'), wpmanualsq: new El('button') };
  const wproot = { querySelector(sel) {
    let m = /data-wp-count="(\w+)"/.exec(sel); if (m) return counts[m[1]];
    m = /data-clear="(\w+)"/.exec(sel); if (m) return clears[m[1]];
    return null;
  } };
  const requests = [], pending = [], opened = [];
  const c = vm.createContext({
    doc: { createElement: tag => new El(tag), createElementNS: (ns, tag) => new El(tag), querySelector: () => null },
    $: id => id === 'wproot' ? wproot : ids[id] || null, rid: 'session', wsT: k => k, glRel: () => 'ago', relAgo: () => 'just now', wsI18n: { lang: 'en' },
    q: sel => sel === '.wp-lchip' ? chips : [], region: () => null,
    window: { WsPreview: { open: (p, o) => opened.push([p, o]) } }, Date, Promise,
    fetch: (url, opts) => { requests.push([url, opts || {}]); return new Promise((resolve, reject) => pending.push({ resolve, reject })); },
    ...extra });
  vm.runInContext(decls, c);
  return { c, ids, chips, counts, clears, requests, pending, opened };
}
const ok = body => ({ ok: true, json: async () => body });

test('the head live indicator follows the stream through the connected hook', () => {
  const h = harness();
  const dot = h.ids.wpsse = new El('span');
  h.c.wpStreamSync(false);
  assert.ok(dot.classList.contains('off'));
  h.c.wpStreamSync(true);
  assert.ok(!dot.classList.contains('off'));
  const stream = fs.readFileSync(path.join(__dirname, 'assets/dashboard-stream.js'), 'utf8');
  assert.match(stream, /if \(typeof wpStreamSync === "function"\) wpStreamSync\(on\);/);
});

test('notes load the cached snapshot, wait on the worker, refresh by POST, and render safely', async () => {
  const h = harness(), list = h.ids.wpnoteslist, button = h.ids.wpnotesrefresh;
  assert.equal(h.requests.length, 0, 'nothing loads at declaration');
  h.c.wpNotesLoad(false);
  assert.deepEqual([h.requests[0][0], h.requests[0][1].method], ['/s/session/notes', 'GET']);
  assert.equal(button.disabled, true);
  h.pending.shift().resolve(ok({ layers: [], loading: true })); await tick();
  assert.equal(h.requests[1][0], '/s/session/notes?wait=1');
  assert.ok(list.find('wp-hint').some(e => e.textContent === 'js.notes.loading'));
  h.pending.shift().resolve(ok({ layers: [
    { layer: 'repo', notes: [{ key: '<unsafe>', value: '<value>', priority: 0, visible: false, written_at: '2026-01-01T00:00:00Z' },
      { key: 'repo.top', value: 'top', priority: 3, visible: true }] },
    { layer: 'worktree', notes: [{ key: 'wt.one', value: 'one', priority: 1, visible: true }] }] })); await tick();
  assert.equal(button.disabled, false);
  const keys = list.find('wp-note-key').map(e => e.textContent);
  assert.deepEqual(keys, ['wt.one', 'repo.top', '<unsafe>'], 'worktree first; priority order within a layer; text, not markup');
  const muted = list.find('wp-note-row').find(r => r.classList.contains('is-muted'));
  assert.ok(muted && muted.find('wp-note-muted').length === 1);
  assert.ok(list.find('wp-note-prio').some(e => e.textContent === 'P0' && e.classList.contains('is-zero')));
  assert.equal(h.counts.notes.textContent, '3');
  assert.equal(h.chips.find(b => b.getAttribute('data-layer') === 'clone').hidden, true, 'a layer the response lacks has no chip');
  // expand
  const body = list.find('wp-note-body')[0];
  body.listeners.click(); assert.equal(body.getAttribute('aria-expanded'), 'true');
  h.c.wpNotesRender();
  assert.equal(list.find('wp-note-body')[0].getAttribute('aria-expanded'), 'true', 'an open body survives a re-render');
  // muted toggle, search, layer chip
  h.c.wpMuted = false; h.c.wpNotesRender();
  assert.equal(list.find('wp-note-row').length, 2); assert.equal(h.counts.notes.textContent, '2/3');
  h.ids.wpnotesq.value = 'TOP'; h.c.wpNotesRender();
  assert.deepEqual(list.find('wp-note-key').map(e => e.textContent), ['repo.top']);
  assert.equal(h.clears.wpnotesq.hidden, false);
  h.ids.wpnotesq.value = 'nothing-here'; h.c.wpNotesRender();
  assert.equal(list.find('wp-empty-title')[0].textContent, 'js.wp.nomatch');
  h.ids.wpnotesq.value = ''; h.c.wpMuted = true; h.c.wpLayer = 'worktree'; h.c.wpNotesRender();
  assert.deepEqual(list.find('wp-note-key').map(e => e.textContent), ['wt.one']);
  assert.equal(h.chips.find(b => b.getAttribute('data-layer') === 'worktree').getAttribute('aria-pressed'), 'true');
  // refresh posts; a failure re-enables the button and says so
  h.c.wpNotesLoad(true);
  assert.equal(h.requests[2][1].method, 'POST');
  h.pending.shift().reject(new Error('offline')); await tick();
  assert.equal(button.disabled, false);
  assert.ok(list.find('wp-hint').some(e => e.textContent === 'js.notes.error'));
});

test('a LAN viewer sees the repo layer only, with the local-only hint', async () => {
  const h = harness();
  h.c.wpNotesLoad(false);
  h.pending.shift().resolve(ok({ local_only: true, layers: [{ layer: 'repo', notes: [{ key: 'shared', value: 'v', priority: 1 }] }] })); await tick();
  assert.ok(h.ids.wpnoteslist.find('wp-hint').some(e => e.textContent === 'js.notes.local'));
  assert.deepEqual(h.chips.filter(b => !b.hidden).map(b => b.getAttribute('data-layer')), ['all', 'repo']);
  h.c.wpLayer = 'machine'; h.c.wpNotesRender();
  assert.equal(h.c.wpLayer, 'all', 'a filter on an absent layer falls back to all');
});

test('manuals load once on demand, filter client-side, and open in the file card', async () => {
  const h = harness(), list = h.ids.wpmanualslist;
  h.c.wpRef = 'manuals'; h.c.wpRefLoad();
  assert.deepEqual(h.requests.map(r => r[0]), ['/s/session/manuals']);
  assert.ok(list.find('wp-hint').some(e => e.textContent === 'js.wp.manuals.loading'));
  h.c.wpRefLoad(); assert.equal(h.requests.length, 1, 'no second read while one runs');
  h.pending.shift().resolve(ok({ manuals: [{ path: 'ai-docs/manuals/a.md', stem: 'a', summary: 'Alpha' }, { path: 'ai-docs/manuals/b.md', stem: 'b', summary: 'Beta' }] })); await tick();
  assert.equal(list.find('wp-manual-btn').length, 2); assert.equal(h.counts.manuals.textContent, '2');
  h.c.wpRefLoad(); assert.equal(h.requests.length, 1, 'loaded once');
  h.ids.wpmanualsq.value = 'beta'; h.c.wpManualsRender();
  const rows = list.find('wp-manual-btn');
  assert.equal(rows.length, 1); assert.equal(h.counts.manuals.textContent, '1/2');
  rows[0].listeners.click();
  assert.equal(h.opened.length, 1); assert.equal(h.opened[0][0], 'ai-docs/manuals/b.md'); assert.equal(h.opened[0][1].keep, true);
  h.ids.wpmanualsq.value = ''; h.c.wpManuals = []; h.c.wpManualsRender();
  assert.equal(list.find('wp-empty-title')[0].textContent, 'js.wp.manuals.empty');
});

test('a failed manuals read says so and retries on the next opening', async () => {
  const h = harness();
  h.c.wpRef = 'manuals'; h.c.wpRefLoad();
  h.pending.shift().resolve({ ok: false, json: async () => ({}) }); await tick();
  assert.ok(h.ids.wpmanualslist.find('wp-hint').some(e => e.textContent === 'js.wp.manuals.error'));
  h.c.wpRefLoad(); assert.equal(h.requests.length, 2);
});

test('wpSync reads the region chips into the chrome, never into the region', () => {
  const chip = new El('span'); chip.textContent = '2/5';
  const todo = Object.assign(new El('details'), { open: false, querySelector: sel => sel === ':scope > summary > .chip' ? chip : null });
  const panels = { querySelector: sel => sel.includes('p-todo') ? todo : null };
  const els = { empty: { todo: new El('div'), agenda: new El('div') }, tools: { todo: new El('div'), agenda: new El('div') },
    count: { todo: [new El('span'), new El('span')], agenda: [new El('span')] } };
  const fill = new El('i'), bar = new El('div'); fill.closest = () => bar;
  const h = harness({ region: () => panels });
  h.c.wproot = {}; h.c.q = sel => {
    const m = /data-(?:for|wp-count)="(\w+)"/.exec(sel); if (!m) return [];
    if (sel.startsWith('.wp-mem-empty')) return [els.empty[m[1]]];
    if (sel.startsWith('.wp-mem-tools')) return [els.tools[m[1]]];
    return els.count[m[1]];
  };
  h.c.$ = id => id === 'wpprog' ? fill : null;
  h.c.wpSync();
  assert.equal(todo.open, true, 'the todo card stays open');
  assert.equal(els.empty.todo.hidden, true); assert.equal(els.tools.todo.hidden, false);
  assert.equal(els.empty.agenda.hidden, false, 'an absent agenda shows its quiet line'); assert.equal(els.tools.agenda.hidden, true);
  assert.deepEqual(els.count.todo.map(e => e.textContent), ['2/5', '2/5']);
  assert.equal(fill.style.transform, 'scaleX(0.4)'); assert.equal(bar.getAttribute('aria-valuenow'), '40');
  assert.equal(todo.children.length, 0, 'nothing is written inside the region');
});

test('drawerWait lights the Workpanel rail slot, then clears it; the drawer carries no wait', () => {
  const slot = new El('button'); slot.setAttribute('aria-label', 'Workpanel'); slot.title = 'Workpanel'; slot.attrs.title = 'Workpanel';
  let found = [];
  const row = inSource => ({ closest: s => s === '.ag-source' && inSource ? {} : null });
  const h = harness({});
  h.c.doc.querySelector = sel => sel === '#bgal .gal-dot.sys[data-sys="workpanel"]' ? slot : null;
  h.c.q = () => found;
  found = [row(true)]; h.c.drawerWait();
  assert.equal(slot.classList.contains('wait'), false, 'the hidden roster alone does not count');
  found = [row(false)]; h.c.drawerWait();
  assert.equal(slot.classList.contains('wait'), true);
  assert.equal(slot.getAttribute('aria-label'), 'Workpanel; js.drawer.waiting'); assert.equal(slot.title, slot.getAttribute('aria-label'));
  h.c.drawerWait(); assert.equal(slot.getAttribute('aria-label'), 'Workpanel; js.drawer.waiting', 'the suffix is not repeated');
  found = []; h.c.drawerWait();
  assert.equal(slot.classList.contains('wait'), false);
  assert.equal(slot.getAttribute('aria-label'), 'Workpanel');
  const wait = js.slice(js.indexOf('  function drawerWait()'), js.indexOf('  // wpGallery follows'));
  assert.doesNotMatch(wait, /drhandle|drfold|drTitle/, 'the panels handle retired: only the rail slot signals');
  assert.match(js, /#r-convlive \.permwait, #r-tree \.phase\.permission, #r-tree \.brow\.perm/);
  assert.match(css, /\.gal-dot\.sys\[data-sys="workpanel"\]\.wait::after \{[^}]*var\(--wb-ph-perm\)/);
});

test('the Workpanel breakpoint, tonal surfaces, and compositor-only motion', () => {
  assert.match(css, /container: wp \/ inline-size;/);
  assert.match(css, /@container wp \(max-width: 539px\) \{[\s\S]*\.wp-mem-seg \{ display: flex; \}/);
  assert.match(css, /\.wp-memory #r-panels \{ display: contents; \}/);
  assert.doesNotMatch(css, /border:\s*1px/, 'borderless tonal surfaces');
  assert.doesNotMatch(css, /transition:[^;]*\b(width|height|left|top)\b/, 'no layout-property transitions');
  assert.doesNotMatch(js, /setInterval|setTimeout\(/, 'nothing polls');
});
