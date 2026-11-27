// Harness-free: node internal/wsdashboard/testdata/rail-hold-dot.test.cjs
'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const railSource = fs.readFileSync(path.join(__dirname, '../assets/rail.js'), 'utf8');

function makeMiniDOM() {
  class Node {
    constructor(nodeType) {
      this.nodeType = nodeType;
      this.parentNode = null;
      this.childNodes = [];
    }
    get parentElement() { return this.parentNode && this.parentNode.nodeType === 1 ? this.parentNode : null; }
    get firstElementChild() { return this.childNodes.find(c => c.nodeType === 1) || null; }
    get nextSibling() {
      if (!this.parentNode) return null;
      const i = this.parentNode.childNodes.indexOf(this);
      return i >= 0 ? this.parentNode.childNodes[i + 1] || null : null;
    }
    appendChild(c) { c.parentNode = this; this.childNodes.push(c); return c; }
    replaceWith(n) {
      const p = this.parentNode;
      if (!p) return;
      const i = p.childNodes.indexOf(this);
      if (i < 0) return;
      n.parentNode = p;
      this.parentNode = null;
      p.childNodes[i] = n;
    }
    remove() {
      const p = this.parentNode;
      if (!p) return;
      const i = p.childNodes.indexOf(this);
      if (i >= 0) p.childNodes.splice(i, 1);
      this.parentNode = null;
    }
  }
  class Comment extends Node {
    constructor(text) { super(8); this.data = text; }
    get outerHTML() { return `<!--${this.data}-->`; }
  }
  class Text extends Node {
    constructor(text) { super(3); this.textContent = text; }
    get outerHTML() { return this.textContent; }
  }
  class Element extends Node {
    constructor(tag, attrs = {}, doc = null) {
      super(1);
      this.tagName = tag.toUpperCase();
      this.attrs = { ...attrs };
      this.ownerDocument = doc;
      const me = this;
      this.classList = {
        contains: c => me.className.split(/\s+/).includes(c),
        add: c => { const s = new Set(me.className.split(/\s+/).filter(Boolean)); s.add(c); me.className = [...s].join(' '); },
        remove: c => { const s = new Set(me.className.split(/\s+/).filter(Boolean)); s.delete(c); me.className = [...s].join(' '); },
        toggle: (c, on) => { const has = me.classList.contains(c); const next = on === undefined ? !has : !!on; if (next) me.classList.add(c); else me.classList.remove(c); }
      };
    }
    get className() { return this.attrs.class || ''; }
    set className(v) { this.attrs.class = v; }
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    removeAttribute(k) { delete this.attrs[k]; }
    hasAttribute(k) { return k in this.attrs; }
    matches(sel) {
      if (sel === 'div.rs[data-rid]') return this.tagName === 'DIV' && this.classList.contains('rs') && this.hasAttribute('data-rid');
      if (sel === '.rs[data-rid]') return this.classList.contains('rs') && this.hasAttribute('data-rid');
      if (sel === '.rs') return this.classList.contains('rs');
      if (sel === '.rl') return this.classList.contains('rl');
      if (sel === '.qdot') return this.classList.contains('qdot');
      if (sel === 'time.when.t') return this.tagName === 'TIME' && this.classList.contains('when') && this.classList.contains('t');
      if (sel === ':scope > template.shp-source') return this.tagName === 'TEMPLATE' && this.classList.contains('shp-source');
      return false;
    }
    closest(sel) {
      for (let e = this; e && e.nodeType === 1; e = e.parentElement) if (e.matches(sel)) return e;
      return null;
    }
    querySelectorAll(sel) {
      if (sel.startsWith(':scope > ')) {
        const sub = sel.slice(':scope > '.length);
        return this.childNodes.filter(c => c.nodeType === 1 && c.matches(sel) || (c.nodeType === 1 && c.matches(sub)));
      }
      const out = [];
      const walk = n => {
        for (const c of n.childNodes) {
          if (c.nodeType === 1) {
            if (c.matches(sel)) out.push(c);
            walk(c);
          }
        }
      };
      walk(this);
      return out;
    }
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
    cloneNode(deep) {
      const c = new Element(this.tagName.toLowerCase(), this.attrs, this.ownerDocument);
      if (deep) {
        for (const ch of this.childNodes) {
          if (ch.nodeType === 1) c.appendChild(ch.cloneNode(true));
          else if (ch.nodeType === 3) c.appendChild(new Text(ch.textContent));
          else if (ch.nodeType === 8) c.appendChild(new Comment(ch.data));
        }
      }
      return c;
    }
    get innerHTML() { return this.childNodes.map(c => c.outerHTML).join(''); }
    set innerHTML(html) {
      this.childNodes = [];
      const stack = [this];
      const tokenRe = /<!--([^]*?)-->|<\/([a-zA-Z0-9-]+)>|<([a-zA-Z0-9-]+)([^>]*)>|([^<]+)/g;
      let m;
      while ((m = tokenRe.exec(html))) {
        const parent = stack[stack.length - 1];
        if (m[1] !== undefined) {
          parent.appendChild(new Comment(m[1]));
        } else if (m[2] !== undefined) {
          if (stack.length > 1) stack.pop();
        } else if (m[3] !== undefined) {
          const attrs = {};
          const attrRe = /([a-zA-Z0-9:-]+)(?:="([^"]*)")?/g;
          let am;
          while ((am = attrRe.exec(m[4] || ''))) attrs[am[1]] = am[2] ?? '';
          const el = new Element(m[3], attrs, this.ownerDocument);
          parent.appendChild(el);
          if (!m[4].trim().endsWith('/')) stack.push(el);
        } else if (m[5]) {
          parent.appendChild(new Text(m[5]));
        }
      }
    }
    get outerHTML() {
      const attrStr = Object.entries(this.attrs).map(([k, v]) => ` ${k}="${v}"`).join('');
      return `<${this.tagName.toLowerCase()}${attrStr}>${this.innerHTML}</${this.tagName.toLowerCase()}>`;
    }
  }
  const doc = {
    createElement: tag => new Element(tag, {}, doc),
    createComment: text => new Comment(text)
  };
  return { doc, Element };
}

function rowHTML(rid, asks, showDot) {
  const dot = showDot ? '<span class="qdot"></span>' : '';
  return `<div class="rs" data-rid="${rid}" data-asks="${asks}"><a class="rl" href="/s/${rid}">${dot}<span class="rid">${rid}</span></a><template class="shp-source">hover</template></div>`;
}

function railHTML(headerCount, rowsHTML) {
  const chip = headerCount > 0 ? `<span class="chip policy">${headerCount}</span>` : '';
  return `<div class="rhd">${chip}</div><div class="rlist">${rowsHTML}</div>`;
}

test('rail.js mergeRows hides .qdot on hold and raises no new-question attention or notice on unhold', () => {
  const window = {};
  vm.runInNewContext(railSource, { window });
  const { doc } = makeMiniDOM();

  const initialHTML = railHTML(2, rowHTML('away', '1 2', true) + rowHTML('shown', '', false));
  const root = doc.createElement('div');
  root.innerHTML = initialHTML;

  const base = window.WsRail.rowInputFull(initialHTML, doc);
  assert.ok(base, 'rowInputFull parses initial rail');

  const attn = window.WsRail.attention();
  const notif = window.WsRail.notices();
  // Baseline first observation (away has open asks 1 and 2, dot visible).
  attn.sync(root.querySelectorAll('.rs[data-rid]'), 'shown', 'New question');
  assert.equal(notif.read(root.querySelectorAll('.rs[data-rid]'), true).length, 0);
  assert.ok(root.querySelectorAll('.rs[data-rid]')[0].querySelector('.qdot'), 'initial away row shows .qdot');

  // 1. All asks in away are held -> server patches away row with .qdot removed, data-asks="1 2" unchanged.
  const heldInput = window.WsRail.rowInputPatch(base, {
    hash: 'h1',
    order: ['away', 'shown'],
    rows: [{ key: 'away', hash: 'r1', html: rowHTML('away', '1 2', false) }]
  }, doc);
  assert.ok(heldInput, 'rowInputPatch accepts hold update');
  const mergedHeld = window.WsRail.mergeRows(root, base, heldInput);
  assert.equal(mergedHeld, true, 'mergeRows patches visible row markup when .qdot hides');
  const awayAfterHold = root.querySelectorAll('.rs[data-rid]')[0];
  assert.equal(awayAfterHold.getAttribute('data-asks'), '1 2', 'data-asks unchanged on hold');
  assert.equal(awayAfterHold.querySelector('.qdot'), null, '.qdot hidden when every open ask is held');

  attn.sync(root.querySelectorAll('.rs[data-rid]'), 'shown', 'New question');
  assert.equal(awayAfterHold.classList.contains('attn'), false, 'holding does not flag attention');
  assert.equal(notif.read(root.querySelectorAll('.rs[data-rid]')).length, 0, 'holding raises no notice');

  // 2. Ask 1 is unheld -> server patches away row with .qdot back, data-asks="1 2" still unchanged.
  const unheldInput = window.WsRail.rowInputPatch(heldInput, {
    hash: 'h2',
    order: ['away', 'shown'],
    rows: [{ key: 'away', hash: 'r2', html: rowHTML('away', '1 2', true) }]
  }, doc);
  assert.ok(unheldInput, 'rowInputPatch accepts unhold update');
  const mergedUnheld = window.WsRail.mergeRows(root, heldInput, unheldInput);
  assert.equal(mergedUnheld, true, 'mergeRows patches visible row markup when .qdot returns');
  const awayAfterUnhold = root.querySelectorAll('.rs[data-rid]')[0];
  assert.equal(awayAfterUnhold.getAttribute('data-asks'), '1 2', 'data-asks unchanged on unhold');
  assert.ok(awayAfterUnhold.querySelector('.qdot'), '.qdot reappears on unhold');

  attn.sync(root.querySelectorAll('.rs[data-rid]'), 'shown', 'New question');
  assert.equal(awayAfterUnhold.classList.contains('attn'), false, 'unholding must not flag new-question attention');
  assert.equal(awayAfterUnhold.classList.contains('attn-flash'), false, 'unholding must not flash');
  assert.equal(notif.read(root.querySelectorAll('.rs[data-rid]')).length, 0, 'unholding must not emit a new-question notification');

  // 3. Contrast: an actual new ask ID ("3") arriving in data-asks DOES still flag and notify.
  const newAskInput = window.WsRail.rowInputPatch(unheldInput, {
    hash: 'h3',
    order: ['away', 'shown'],
    rows: [{ key: 'away', hash: 'r3', html: rowHTML('away', '1 2 3', true) }]
  }, doc);
  assert.equal(window.WsRail.mergeRows(root, unheldInput, newAskInput), true);
  const awayNewAsk = root.querySelectorAll('.rs[data-rid]')[0];
  attn.sync(root.querySelectorAll('.rs[data-rid]'), 'shown', 'New question');
  assert.equal(awayNewAsk.classList.contains('attn'), true, 'real new ask flags attention');
  const events = notif.read(root.querySelectorAll('.rs[data-rid]'));
  assert.equal(events.length, 1);
  assert.equal(events[0].kind, 'ask');
  assert.deepEqual([...events[0].ids], ['3']);
});
