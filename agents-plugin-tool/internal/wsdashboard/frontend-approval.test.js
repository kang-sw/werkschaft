const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

const src = fs.readFileSync(__dirname + '/assets/dashboard-leads.js', 'utf8');
function setup() {
  let mutations = 0, events = 0;
  class Element {
    constructor(tag, cls, text) { this.tagName = tag; this.className = cls || ''; this.textContent = text || ''; this.children = []; this.attrs = {}; this.parentNode = null; }
    appendChild(el) { return this.insertBefore(el, null); }
    insertBefore(el, before) {
      if (el.parentNode) el.parentNode.children.splice(el.parentNode.children.indexOf(el), 1);
      this.children.splice(before ? this.children.indexOf(before) : this.children.length, 0, el);
      el.parentNode = this; mutations++; return el;
    }
    get nextSibling() {
      if (!this.parentNode) return null;
      return this.parentNode.children[this.parentNode.children.indexOf(this) + 1] || null;
    }
    remove() {
      this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1);
      this.parentNode = null; mutations++;
    }
    setAttribute(k, v) { this.attrs[k] = v; }
    getAttribute(k) { return this.attrs[k] || null; }
    querySelector(sel) {
      for (const c of this.children) {
        if (sel === '[data-entry][data-at]' ? c.attrs['data-entry'] && c.attrs['data-at'] : sel === '[data-at]' ? c.attrs['data-at'] : c.className.split(' ').includes(sel.slice(1))) return c;
        const found = c.querySelector(sel); if (found) return found;
      }
      return null;
    }
  }
  const convo = new Element('div'), entries = convo.appendChild(new Element('div', 'centries')), box = convo.appendChild(new Element('div'));
  const ctx = { lead: { answered: {} }, document: {
    dispatchEvent(e) { assert.equal(e.type, 'ws-approval-rows-changed'); events++; },
    addEventListener() {}
  }, CustomEvent: class { constructor(type) { this.type = type; } },
  $(id) { return id === 'convo' ? convo : box; },
  mk: (tag, cls, text) => new Element(tag, cls, text), gIcon: name => new Element('svg', name),
  wsT: key => key, toolLabel: x => x, WsRail: { approvalText: x => JSON.stringify(x) }, fmtTimes() {} };
  vm.createContext(ctx);
  vm.runInContext(src.slice(src.indexOf('  function apChanged()'), src.indexOf('  // apCard builds')), ctx);
  return { ctx, convo, entries, box, Element, counts: () => ({ mutations, events }) };
}
test('confirmed answer becomes an unboxed expandable trace and preserves linked audit nodes', () => {
  const h = setup(), pending = new h.Element('div', 'apcard');
  const audit = pending.appendChild(new h.Element('div', 'conv-audit-items'));
  audit.appendChild(new h.Element('div', 'audit-event'));
  const a = { id: 'request', tool: 'Bash', tool_use_id: 'call', started_at: 1000, input: { command: 'pwd' } };
  h.ctx.apAnswered(a, 'deny', 'reason', pending);
  const row = h.ctx.lead.answered.request;
  assert.equal(row.parentNode, h.entries);
  assert.equal(row.tagName, 'details');
  assert.equal(row.className, 'apanswered');
  assert.equal(row.getAttribute('data-audit-owner'), 'approval');
  assert.equal(row.getAttribute('data-approval-id'), 'request');
  assert.equal(row.getAttribute('data-approval-call'), 'call');
  assert.equal(row.getAttribute('data-at'), '1970-01-01T00:00:01.000Z');
  assert.equal(audit.parentNode, row);
  assert.equal(row.children[0].className, 'conv-row');
  for (const cls of ['row-icon', 'row-content', 'row-time', 'row-chevron']) assert.ok(row.querySelector('.' + cls));
  assert.equal(row.querySelector('.row-content').children[2].textContent, 'js.ap.deny');
  const counts = h.counts();
  h.ctx.apAnswered(a, 'deny', 'reason', pending);
  assert.deepEqual(h.counts(), counts);
  assert.equal(h.ctx.apMountAnswered(), false);
  assert.deepEqual(h.counts(), counts);
});
test('answered root chooses the first later timestamp and repeated reconciliation is idle', () => {
  const h = setup(), later = new h.Element('div');
  later.setAttribute('data-at', '2026-11-23T13:00:00Z');
  h.entries.appendChild(later);
  const row = new h.Element('details');
  row.setAttribute('data-at', '2026-11-23T12:00:00Z');
  h.ctx.lead.answered.a = row;
  assert.equal(h.ctx.apMountAnswered(), true);
  assert.equal(h.entries.children[0], row);
  const counts = h.counts();
  assert.equal(h.ctx.apMountAnswered(), false);
  assert.deepEqual(h.counts(), counts);
});
test('pending rendering exposes the same owner metadata and an audit slot', () => {
  const body = src.slice(src.indexOf('  function apCard('), src.indexOf('  // leadDraw shows'));
  assert.match(body, /apMetadata\(el, a\)/);
  assert.match(body, /el.appendChild\(mk\("div", "conv-audit-items"\)\)/);
  assert.match(body, /aph conv-row/);
  assert.match(body, /st === 200 && v.id.*apAnswered/);
  assert.doesNotMatch(body, /st === 409.*apAnswered/);
});

test('evicted approvals prune at the loaded transcript cutoff', () => {
  const h = setup(), oldest = new h.Element('div');
  oldest.setAttribute('data-entry', 'entry');
  oldest.setAttribute('data-at', '2026-11-23T13:00:00Z');
  const row = new h.Element('details');
  row.setAttribute('data-at', '2026-11-23T12:00:00Z');
  h.entries.appendChild(row);
  h.entries.appendChild(oldest);
  h.ctx.lead.answered.a = row;
  assert.equal(h.ctx.apMountAnswered(), true);
  assert.equal(row.parentNode, null);
  assert.equal(h.ctx.lead.answered.a, undefined);
  const counts = h.counts();
  assert.equal(h.ctx.apMountAnswered(), false);
  assert.deepEqual(h.counts(), counts);
});
test('same timestamp decisions retain stable order without idle DOM writes', () => {
  const h = setup();
  for (const id of ['b', 'a']) {
    const row = new h.Element('details');
    row.setAttribute('data-at', '2026-11-23T12:00:00Z');
    row.setAttribute('data-approval-id', id);
    h.ctx.lead.answered[id] = row;
  }
  h.ctx.apMountAnswered();
  const counts = h.counts();
  assert.equal(h.ctx.apMountAnswered(), false);
  assert.deepEqual(h.counts(), counts);
});

test('document reconciliation fires only for keyed changes and batches answered replacement', () => {
  const h = setup(), c = h.ctx;
  c.convo = h.convo;
  c.lead.cards = {};
  c.lead.st = { approvals: [], can_control: true };
  Object.assign(c, { leadChip() {}, composerSync() {}, rp: {}, handedDraw() {}, harnessDraw() {}, quotaDraw() {},
    leadGone: () => false, pinned: () => false, pinConvo() {},
    apCard() { return { el: new h.Element('div', 'apcard'), n: new h.Element('span') }; } });
  h.convo.classList = { toggle() {} };
  vm.runInContext(src.slice(src.indexOf('  function leadDraw()'), src.indexOf('  // handedDraw shows')), c);
  c.leadDraw();
  assert.equal(h.counts().events, 0);
  c.lead.st.approvals = [{ id: 'request' }];
  c.leadDraw();
  assert.equal(h.counts().events, 1);
  const stable = h.counts();
  c.leadDraw();
  assert.deepEqual(h.counts(), stable);
  c.apAnswered({ id: 'request', tool: 'Bash', input: {} }, 'allow', '', c.lead.cards.request.el);
  assert.equal(h.counts().events, 1, 'wait for pending root removal before notifying audit owner');
  c.lead.st.approvals = [];
  c.leadDraw();
  assert.equal(h.counts().events, 2);
  const answered = h.counts();
  c.leadDraw();
  assert.deepEqual(h.counts(), answered);
});
