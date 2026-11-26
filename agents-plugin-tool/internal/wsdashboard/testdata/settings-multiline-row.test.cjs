'use strict';

// Verifies multiline textarea settings rows (including agent.tier_routing_guidance)
// wrap onto a full-width row while preserving existing form/save/provenance semantics.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const catalog = JSON.parse(readAssetSource('wsdashboard', 'dashboard-settings-catalog.json'));
const jsSource = readAssetSource('wsdashboard', 'dashboard-settings.js');
const cssSource = readAssetSource('wsdashboard', 'dashboard-settings.css');

function makeNode(tag) {
  const el = {
    tagName: tag.toUpperCase(),
    className: '',
    dataset: {},
    attrs: {},
    children: [],
    listeners: {},
    style: {},
    disabled: false,
    hidden: false,
    value: '',
    isConnected: true,
    classList: {
      add(...cls) {
        const cur = new Set(el.className.split(/\s+/).filter(Boolean));
        cls.forEach(c => cur.add(c));
        el.className = [...cur].join(' ');
      },
      remove(...cls) {
        const cur = new Set(el.className.split(/\s+/).filter(Boolean));
        cls.forEach(c => cur.delete(c));
        el.className = [...cur].join(' ');
      },
      toggle(c, force) {
        const has = this.contains(c);
        const want = force === undefined ? !has : !!force;
        if (want) this.add(c);
        else this.remove(c);
        return want;
      },
      contains(c) {
        return el.className.split(/\s+/).filter(Boolean).includes(c);
      }
    },
    setAttribute(k, v) { el.attrs[k] = String(v); },
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(el.attrs, k) ? el.attrs[k] : null; },
    removeAttribute(k) { delete el.attrs[k]; },
    hasAttribute(k) { return Object.prototype.hasOwnProperty.call(el.attrs, k); },
    append(...nodes) {
      for (const child of nodes) {
        if (typeof child === 'string') {
          el.children.push({ tagName: '#TEXT', textContent: child, children: [], className: '', dataset: {}, attrs: {} });
        } else if (child) {
          child.parentNode = el;
          el.children.push(child);
        }
      }
    },
    prepend(...nodes) {
      for (let i = nodes.length - 1; i >= 0; i--) {
        const child = nodes[i];
        if (child) {
          child.parentNode = el;
          el.children.unshift(child);
        }
      }
    },
    replaceChildren(...nodes) {
      el.children = [];
      el.append(...nodes);
    },
    replaceWith(next) {
      if (!el.parentNode) return;
      const idx = el.parentNode.children.indexOf(el);
      if (idx >= 0) {
        next.parentNode = el.parentNode;
        el.parentNode.children[idx] = next;
      }
    },
    remove() {
      if (!el.parentNode) return;
      const idx = el.parentNode.children.indexOf(el);
      if (idx >= 0) el.parentNode.children.splice(idx, 1);
      el.parentNode = null;
    },
    addEventListener(type, fn) {
      (el.listeners[type] ||= []).push(fn);
    },
    dispatchEvent(evt) {
      const event = Object.assign({ target: el, preventDefault() {}, stopPropagation() {} }, evt);
      let cur = el;
      while (cur) {
        for (const fn of cur.listeners[event.type] || []) fn(event);
        if (!event.bubbles) break;
        cur = cur.parentNode;
      }
      return true;
    },
    contains(other) {
      return el === other || el.children.some(c => typeof c.contains === 'function' && c.contains(other));
    },
    querySelectorAll(selector) {
      const out = [];
      const match = node => {
        if (!node || !node.tagName || node.tagName === '#TEXT') return false;
        return selector.split(',').some(part => {
          const s = part.trim();
          if (s.startsWith('.')) return node.classList.contains(s.slice(1));
          if (s.startsWith('[data-')) {
            const m = s.match(/^\[data-([a-z0-9-]+)(?:="([^"]*)")?\]$/i);
            if (!m) return false;
            const key = m[1].replace(/-([a-z])/g, (_, ch) => ch.toUpperCase());
            return m[2] !== undefined ? node.dataset[key] === m[2] : key in node.dataset;
          }
          return node.tagName.toLowerCase() === s.toLowerCase();
        });
      };
      const walk = node => {
        for (const child of node.children || []) {
          if (match(child)) out.push(child);
          walk(child);
        }
      };
      walk(el);
      return out;
    },
    querySelector(selector) {
      return el.querySelectorAll(selector)[0] || null;
    },
    closest() { return null; },
    focus() {}
  };
  return el;
}

function mountSettings(entries) {
  const requests = [];
  const view = makeNode('main');
  view.attrs['data-settings-view'] = '';
  const targetDisplay = makeNode('div');
  targetDisplay.dataset.settingsTargetDisplay = '';
  targetDisplay.append(makeNode('span'), makeNode('code'));
  const precedence = makeNode('div');
  precedence.dataset.settingsPrecedence = '';
  const sections = makeNode('nav');
  sections.dataset.settingsSections = '';
  const filters = makeNode('div');
  filters.dataset.settingsFilters = '';
  const search = makeNode('input');
  search.dataset.settingsSearch = '';
  const count = makeNode('span');
  count.dataset.settingsCount = '';
  const content = makeNode('div');
  content.dataset.settingsContent = '';
  const proposal = makeNode('div');
  proposal.dataset.settingsProposal = '';
  view.append(targetDisplay, precedence, sections, filters, search, count, content, proposal);

  const doc = {
    title: 'Settings',
    activeElement: null,
    documentElement: makeNode('html'),
    createElement: makeNode,
    createElementNS: (_, tag) => makeNode(tag),
    querySelector(sel) {
      if (sel === '[data-settings-view]') return view;
      return view.querySelector(sel);
    },
    addEventListener() {}
  };

  const sandbox = {
    document: doc,
    location: { search: '?frame=1&modal=1&section=tier_routing', origin: 'http://localhost', hash: '' },
    URL,
    URLSearchParams,
    Event: function Event(type, init) { return Object.assign({ type }, init); },
    wsT: key => key,
    fetch: async (url, opts) => {
      requests.push({ url, body: opts && opts.body ? JSON.parse(opts.body) : null });
      if (url.startsWith('/api/config/snapshot')) {
        return { ok: true, status: 200, json: async () => ({ scope: 'global', target: '', context: { worktree: '', session: '' }, entries }) };
      }
      if (url === '/api/config/set') {
        const body = JSON.parse(opts.body);
        const base = entries.find(e => e.key === body.key);
        const updated = Object.assign({}, base, {
          value: body.value,
          source_scope: 'global',
          layers: base.layers.map(l => l.scope === 'global' ? Object.assign({}, l, { present: true, selected: body.value, value: body.value }) : l)
        });
        return { ok: true, status: 200, json: async () => ({ entry: updated, affected_entries: [updated], warnings: [] }) };
      }
      return { ok: false, status: 404, json: async () => ({ error: { code: 'not_found' } }) };
    }
  };
  sandbox.window = {
    parent: { postMessage() {}, location: { hash: '' }, document: { querySelectorAll: () => [], addEventListener() {}, removeEventListener() {} } },
    addEventListener() {},
    wsT: sandbox.wsT,
    WsSettingsCatalog: catalog,
    WsShell: {
      pagePath: () => '/settings',
      companionContext: () => ({}),
      message: () => null,
      settingsScopeHref: name => '/settings?scope=' + name
    },
    WsSettingsTierRouting: {
      render: () => makeNode('section')
    }
  };

  vm.runInNewContext(jsSource, sandbox, { filename: 'dashboard-settings.js' });
  return { content, requests };
}

test('agent.tier_routing_guidance renders as a full-width multiline row and preserves save semantics', async () => {
  const guidanceSpec = catalog.find(s => s.key === 'agent.tier_routing_guidance');
  assert.ok(guidanceSpec, 'catalog includes agent.tier_routing_guidance');
  assert.equal(guidanceSpec.section, 'tier_routing');
  assert.equal(guidanceSpec.control, 'textarea');

  const entries = [
    {
      key: 'agent.tier_routing_expose',
      type: 'boolean',
      selectors: {},
      editable: true,
      value: true,
      default: true,
      source_scope: 'builtin',
      layers: [
        { scope: 'global', evaluated: true, present: false },
        { scope: 'builtin', evaluated: true, present: true, value: true }
      ]
    },
    {
      key: 'agent.tier_routing_guidance',
      type: 'string',
      selectors: {},
      editable: true,
      value: '',
      default: '',
      source_scope: 'builtin',
      layers: [
        { scope: 'global', evaluated: true, present: false },
        { scope: 'builtin', evaluated: true, present: true, value: '' }
      ]
    },
    {
      key: 'agent.tier_routing',
      type: 'object',
      selectors: {},
      editable: true,
      value: {},
      default: {},
      source_scope: 'builtin',
      layers: [
        { scope: 'global', evaluated: true, present: false },
        { scope: 'builtin', evaluated: true, present: true, value: {} }
      ]
    }
  ];

  const { content, requests } = mountSettings(entries);
  for (let i = 0; i < 10; i++) await Promise.resolve();

  const exposeRow = content.querySelector('[data-config-key="agent.tier_routing_expose"]');
  const guidanceRow = content.querySelector('[data-config-key="agent.tier_routing_guidance"]');
  assert.ok(exposeRow, 'renders expose toggle row');
  assert.ok(guidanceRow, 'renders guidance textarea row');
  assert.equal(exposeRow.classList.contains('srow-multiline'), false, 'toggle row keeps standard 2-column layout');
  assert.equal(guidanceRow.classList.contains('srow-multiline'), true, 'textarea row opts into full-width multiline layout');

  const textarea = guidanceRow.querySelector('textarea');
  const saveBtn = guidanceRow.querySelector('.save-btn');
  const originPill = guidanceRow.querySelector('.origin-pill');
  const form = guidanceRow.querySelector('.kctl');
  assert.ok(textarea && textarea.classList.contains('ta-inline'), 'renders .ta-inline textarea');
  assert.ok(saveBtn && saveBtn.disabled === true, 'save button starts disabled');
  assert.ok(originPill, 'provenance pill remains intact');

  textarea.value = 'Prefer mockup:large for UI mockups and agy.small for quick lookups.';
  textarea.dispatchEvent({ type: 'input', bubbles: true });
  assert.equal(saveBtn.disabled, false, 'editing textarea enables save button');

  form.dispatchEvent({ type: 'submit', bubbles: true });
  for (let i = 0; i < 10; i++) await Promise.resolve();

  const setCall = requests.find(r => r.url === '/api/config/set');
  assert.ok(setCall, 'submitting multiline row posts to /api/config/set');
  assert.equal(setCall.body.key, 'agent.tier_routing_guidance');
  assert.equal(setCall.body.value, 'Prefer mockup:large for UI mockups and agy.small for quick lookups.');
});

test('dashboard-settings.css spans .srow-multiline textarea across the full row width across breakpoints', () => {
  assert.match(cssSource, /\[data-settings-view\]\s+\.srow\.srow-multiline\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)\s+184px/s);
  assert.match(cssSource, /\[data-settings-view\]\s+\.srow\.srow-multiline\s*>\s*\.sctl-rail\s*\{[^}]*display:\s*contents/s);
  assert.match(cssSource, /\[data-settings-view\]\s+\.srow\.srow-multiline\s+\.kctl-wrap\s*\{[^}]*grid-column:\s*1\s*\/\s*-1;[^}]*width:\s*100%/s);
  assert.match(cssSource, /\[data-settings-view\]\s+\.srow\.srow-multiline\s+\.kctl\s*\{[^}]*flex-direction:\s*column;[^}]*align-items:\s*stretch/s);
  assert.match(cssSource, /\.ta-inline\s*\{[^}]*box-sizing:\s*border-box;[^}]*width:\s*100%/s);
  assert.match(cssSource, /@media\s*\(max-width:\s*1120px\)\s*\{[\s\S]*?\[data-settings-view\]\s+\.srow\.srow-multiline\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)\s+144px/);
  assert.match(cssSource, /@media\s*\(max-width:\s*820px\)\s*\{[\s\S]*?\[data-settings-view\]\s+\.srow\.srow-multiline\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/);
});
