"use strict";

// A running turn's summary is re-rendered on nearly every event (its
// data-turn-rev moves with duration and counts). The open tool-call details
// inside it must survive that: same node, same fetched preview, same
// reconciled audit rows, one fetch in total.

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const assetsDir = path.join(__dirname, "..", "assets");
const convSrc = fs.readFileSync(path.join(assetsDir, "dashboard-conversation.js"), "utf8");
const auditSrc = fs.readFileSync(path.join(assetsDir, "dashboard-audit-rows.js"), "utf8");

function sliceBetween(src, startMarker, endMarker) {
  const start = src.indexOf(startMarker);
  const end = src.indexOf(endMarker, start);
  assert.ok(start >= 0 && end > start, `markers not found: ${startMarker} .. ${endMarker}`);
  return src.slice(start, end);
}

// Minimal DOM: elements hold either text or element children, which is all the
// fixtures use. Setting details.open fires the document's capturing toggle
// listeners, as a browser does for a changed open attribute only.
let toggleHook = null;
class El {
  constructor(tag) {
    this.nodeType = 1;
    this.tagName = tag.toUpperCase();
    this.attrs = new Map();
    this.children = [];
    this.parentNode = null;
    this.text = "";
    this.auSource = undefined;
    const self = this;
    this.classList = {
      list: () => (self.attrs.get("class") || "").split(/\s+/).filter(Boolean),
      contains: (c) => self.classList.list().includes(c),
      add: (c) => { if (!self.classList.contains(c)) self.attrs.set("class", self.classList.list().concat(c).join(" ")); },
      remove: (c) => self.attrs.set("class", self.classList.list().filter((x) => x !== c).join(" ")),
    };
  }
  get className() { return this.attrs.get("class") || ""; }
  set className(v) { this.attrs.set("class", v); }
  get isConnected() { let n = this; while (n.parentNode) n = n.parentNode; return n.isRoot === true; }
  get open() { return this.attrs.has("open"); }
  set open(v) {
    const was = this.open;
    if (v) this.attrs.set("open", ""); else this.attrs.delete("open");
    if (was !== !!v && toggleHook) toggleHook(this);
  }
  get textContent() { return this.text + this.children.map((c) => c.textContent).join(""); }
  set textContent(v) { this.children.forEach((c) => { c.parentNode = null; }); this.children = []; this.text = String(v); }
  get firstElementChild() { return this.children[0] || null; }
  get firstChild() { return this.children[0] || null; }
  get lastElementChild() { return this.children[this.children.length - 1] || null; }
  get nextSibling() { return this.sibling(1); }
  get nextElementSibling() { return this.sibling(1); }
  get previousElementSibling() { return this.sibling(-1); }
  sibling(d) {
    if (!this.parentNode) return null;
    const s = this.parentNode.children;
    return s[s.indexOf(this) + d] || null;
  }
  getAttribute(k) { return this.attrs.has(k) ? this.attrs.get(k) : null; }
  setAttribute(k, v) { if (k === "open") { this.open = true; return; } this.attrs.set(k, String(v)); }
  hasAttribute(k) { return this.attrs.has(k); }
  removeAttribute(k) { if (k === "open") { this.open = false; return; } this.attrs.delete(k); }
  detach(ch) { if (ch.parentNode) ch.parentNode.children.splice(ch.parentNode.children.indexOf(ch), 1); ch.parentNode = null; }
  appendChild(ch) { this.detach(ch); ch.parentNode = this; this.children.push(ch); return ch; }
  removeChild(ch) { this.detach(ch); return ch; }
  insertBefore(ch, ref) {
    this.detach(ch);
    ch.parentNode = this;
    const i = ref ? this.children.indexOf(ref) : -1;
    if (i < 0) this.children.push(ch); else this.children.splice(i, 0, ch);
    return ch;
  }
  replaceChild(next, old) {
    assert.ok(this.children.includes(old), "replaceChild: old is not a child");
    this.detach(next);
    const i = this.children.indexOf(old);
    this.children[i] = next;
    next.parentNode = this;
    old.parentNode = null;
    return old;
  }
  matches(sel) { return sel.split(",").some((s) => matchOne(this, s.trim())); }
  closest(sel) { for (let n = this; n && n.matches; n = n.parentNode) if (n.matches(sel)) return n; return null; }
  querySelectorAll(sel) {
    const out = [];
    (function walk(n) { n.children.forEach((c) => { if (c.matches(sel)) out.push(c); walk(c); }); })(this);
    return out;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  get outerHTML() {
    const attrs = [...this.attrs].map(([k, v]) => (v === "" ? ` ${k}` : ` ${k}="${v}"`)).join("");
    return `<${this.tagName.toLowerCase()}${attrs}>${this.text}${this.children.map((c) => c.outerHTML).join("")}</${this.tagName.toLowerCase()}>`;
  }
}

function matchOne(el, sel) {
  const attrRe = /\[([^\]=]+)(?:="([^"]*)")?\]/g;
  let m;
  while ((m = attrRe.exec(sel))) {
    if (m[2] === undefined ? !el.hasAttribute(m[1]) : el.getAttribute(m[1]) !== m[2]) return false;
  }
  const parts = sel.replace(attrRe, "").split(".");
  if (parts[0] && parts[0].toUpperCase() !== el.tagName) return false;
  return parts.slice(1).every((c) => el.classList.contains(c));
}

function parse(html) {
  const root = new El("div");
  let cur = root;
  const re = /<(\/?)([a-z0-9]+)((?:\s+[^\s=>]+(?:="[^"]*")?)*)\s*>|([^<]+)/g;
  let m;
  while ((m = re.exec(html))) {
    if (m[4] !== undefined) { cur.text += m[4]; continue; }
    if (m[1]) { cur = cur.parentNode; continue; }
    const el = new El(m[2]);
    for (const a of m[3].matchAll(/([^\s=]+)(?:="([^"]*)")?/g)) el.attrs.set(a[1], a[2] === undefined ? "" : a[2]);
    cur.appendChild(el);
    cur = el;
  }
  return root.children.slice().map((c) => { c.parentNode = null; return c; });
}

const callLi = (id, { dur = "1s", fail = false, input = "ls" } = {}) =>
  `<li><details class="au-call-details" data-key="call-${id}"><summary class="conv-row au-row ${fail ? "au-fail" : "au-done"}" data-call="${id}">` +
  `<span class="row-icon au-mk">${fail ? "x" : "ok"}</span><span class="row-content trace-content"><span class="au-tool">Bash</span></span>` +
  `<span class="row-time au-right"><span class="au-att" hidden></span><span class="au-dur">${dur}</span><span class="au-tok">+1k</span></span></summary>` +
  `${input ? `<pre class="au-call-input">${input}</pre>` : ""}<div class="conv-audit-items"></div></details></li>`;

const turn = (rev, count, calls) =>
  `<details class="toolsline d-turn d-turn-live" data-conv-turn="1" data-entry="002" data-key="turn-live" data-turn-rev="${rev}">` +
  `<summary class="conv-row d-turn-sum" data-conv-turn-summary aria-expanded="false"><span class="d-turn-count">${count}</span></summary>` +
  `<div class="d-turn-drawer" data-conv-turn-drawer><div class="d-turn-gap"><ol class="au-calls">${calls.join("")}</ol></div></div></details>`;

// harness wires the two production sources against one document and a fetch
// stub; bundles maps a call id to its audit bundle.
function harness(bundles) {
  const listeners = new Map();
  const fetches = [];
  const doc = {
    root: null,
    addEventListener(type, fn) { listeners.set(type, (listeners.get(type) || []).concat(fn)); },
    dispatchEvent(ev) { (listeners.get(ev.type) || []).forEach((fn) => fn(ev)); },
    createElement: (tag) => new El(tag),
  };
  toggleHook = (target) => doc.dispatchEvent({ type: "toggle", target });
  class CustomEvent { constructor(type, init) { this.type = type; this.detail = (init && init.detail) || {}; } }
  const pins = { n: 0 };
  const fetch = (url) => {
    fetches.push(url);
    const call = /call=([^&]+)/.exec(url);
    const body = /\/event\/([^?]+)\?part=(in|out)/.exec(url);
    let payload = null;
    if (call) payload = bundles[decodeURIComponent(call[1])];
    else if (body) payload = { data: body[2] === "in" ? "INPUT " + body[1] : "OUTPUT " + body[1], end: true };
    return Promise.resolve({ ok: !!payload, status: payload ? 200 : 404, json: () => Promise.resolve(payload) });
  };
  const sandbox = {
    doc, CustomEvent, fetch, Map, Object, Array, Promise, WeakSet, encodeURIComponent, decodeURIComponent, String, JSON,
    auRowsRid: "sess", stick: false, pinConvo() { pins.n++; },
    wsT: (k) => (k === "js.audit.tab.out" ? "Output" : k),
    auIsResultSummary: (ev, id) => !!ev && ev.call_id === id && ev.kind === "tool_result",
    restoreOpen() {}, fmtTimes() {},
  };
  vm.runInNewContext(
    sliceBetween(auditSrc, "    function auDirectItems(el) {", "\n    function auRenderedOwners(") + "\n" +
    sliceBetween(auditSrc, "    function auInlineJSON(url) {", "\n    doc.addEventListener(\"ws-approval-rows-changed\"") +
    "\n" + sliceBetween(convSrc, "  function entryKeys(box) {", "\n  // gapped reports") +
    ";globalThis.__api = { mergeEntries, cache: auPreviewCache, cap: AU_PREVIEW_CAP };",
    sandbox
  );
  const box = new El("div");
  box.isRoot = true;
  doc.root = box;
  const merge = (html) => sandbox.__api.mergeEntries(box, wrap(parse(html)));
  const wrap = (nodes) => { const w = new El("div"); nodes.forEach((n) => w.appendChild(n)); return w; };
  return { doc, box, fetches, pins, merge, api: sandbox.__api, parse, sandbox };
}

const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
const eventsFetches = (h, id) => h.fetches.filter((u) => u.includes("/events?call=" + id));
const bundle = (id, { result = true } = {}) => ({
  call: { id: "ev-" + id, in_bytes: 5 },
  events: result ? [{ kind: "tool_result", call_id: id, id: "res-" + id, out_bytes: 7 }] : [],
});

test("an open call survives many turn-rev updates and a new sibling call, fetching once", async () => {
  const h = harness({ c1: bundle("c1") });
  h.merge(turn("r1", "1 tool call", [callLi("c1")]));
  const details = h.box.querySelector("details.au-call-details");
  const tail = () => h.box.querySelector("details.au-call-details[data-key=\"call-c1\"]");

  details.open = true;
  await flush();
  assert.equal(details.hasAttribute("data-au-preview"), true);
  const output = details.querySelector(".au-call-output"), input = details.querySelector(".au-call-input");
  assert.equal(output.textContent, "Output\nOUTPUT res-c1");
  assert.equal(input.textContent, "INPUT ev-c1", "preview input replaces the server's short argument preview");
  // What auReconcileRows places into a call's own audit container.
  const sysRow = new El("div");
  sysRow.setAttribute("data-au-row-key", "event:e1");
  details.querySelector(".conv-audit-items").appendChild(sysRow);
  const fetchesBefore = h.fetches.length;

  // Pure count/time updates.
  for (let i = 2; i <= 6; i++) {
    const added = h.merge(turn("r" + i, i + " tool calls", [callLi("c1", { dur: i + "s", fail: i === 6 })]));
    await flush();
    assert.equal(added.length, 1, "the turn node itself is replaced");
    assert.equal(tail(), details, `details node identity survives update ${i}`);
    assert.equal(tail().open, true);
    assert.equal(tail().querySelector(".au-call-output"), output, "fetched output node kept");
    assert.equal(tail().querySelector(".au-call-input"), input, "fetched input node kept");
    assert.equal(tail().querySelector(".conv-audit-items").children[0], sysRow, "reconciled audit row kept");
    assert.equal(tail().querySelector(".au-dur").textContent, i + "s", "summary duration is not stale");
    assert.equal(tail().firstElementChild.classList.contains("au-fail"), i === 6, "summary status follows the fresh markup");
    assert.equal(tail().isConnected, true);
  }
  assert.equal(output.textContent, "Output\nOUTPUT res-c1");

  // A new sibling call arrives in the same turn.
  h.merge(turn("r7", "2 tool calls", [callLi("c1", { dur: "7s" }), callLi("c2", { dur: "" })]));
  await flush();
  assert.equal(tail(), details);
  assert.equal(tail().querySelector(".au-call-output"), output);
  assert.equal(h.box.querySelectorAll("details.au-call-details").length, 2);
  const c2 = h.box.querySelector("details.au-call-details[data-key=\"call-c2\"]");
  assert.equal(c2.open, false, "the new sibling is fresh and closed");
  assert.equal(h.box.querySelector(".d-turn-count").textContent, "2 tool calls", "turn header follows the fresh markup");
  assert.equal(h.box.firstElementChild.getAttribute("data-turn-rev"), "r7");

  assert.equal(eventsFetches(h, "c1").length, 1, "the audit bundle is fetched once, not per update");
  assert.equal(h.fetches.length, fetchesBefore, "no preview fetch at all after the first load");
  assert.equal(h.pins.n, 0, "an unchanged preview does not re-pin");
});

test("a call opened while running reads its output in place once it completes", async () => {
  const bundles = { c3: bundle("c3", { result: false }) };
  const h = harness(bundles);
  h.merge(turn("r1", "1 tool call", [callLi("c3", { dur: "" })]));
  const details = h.box.querySelector("details.au-call-details");
  details.open = true;
  await flush();
  assert.equal(details.hasAttribute("data-au-out-pending"), true);
  assert.equal(details.querySelector(".au-call-output"), null);
  const input = details.querySelector(".au-call-input");
  assert.equal(eventsFetches(h, "c3").length, 1);

  // Still running: summary unchanged in the fields that matter, no refetch.
  h.merge(turn("r2", "1 tool call", [callLi("c3", { dur: "" })]));
  await flush();
  assert.equal(eventsFetches(h, "c3").length, 1, "a running call is not polled by merges");

  // The result lands: the summary gains its duration, the output appears.
  bundles.c3 = bundle("c3");
  h.merge(turn("r3", "1 tool call", [callLi("c3", { dur: "4s" })]));
  await flush();
  assert.equal(h.box.querySelector("details.au-call-details"), details, "node identity across completion");
  assert.equal(details.querySelector(".au-dur").textContent, "4s");
  assert.equal(details.querySelector(".au-call-output").textContent, "Output\nOUTPUT res-c3");
  assert.equal(details.querySelector(".au-call-input"), input, "input node is not rebuilt");
  assert.equal(details.hasAttribute("data-au-out-pending"), false);
  assert.equal(details.open, true);
  assert.equal(eventsFetches(h, "c3").length, 2, "one initial and one completion read");

  h.merge(turn("r4", "1 tool call", [callLi("c3", { dur: "5s" })]));
  await flush();
  assert.equal(eventsFetches(h, "c3").length, 2, "completed output is not read again");
});

test("a cached preview applies synchronously to a details rendered anew", async () => {
  const h = harness({ c1: bundle("c1") });
  h.merge(turn("r1", "1 tool call", [callLi("c1")]));
  const first = h.box.querySelector("details.au-call-details");
  first.open = true;
  await flush();
  assert.equal(eventsFetches(h, "c1").length, 1);

  // The node is gone (a reload of the entry): a fresh details opens with the
  // preview in the same task, with no further fetch.
  const fresh = h.parse(turn("r9", "1 tool call", [callLi("c1")]));
  const again = fresh[0].querySelector("details.au-call-details");
  const holder = new El("div");
  holder.isRoot = true;
  holder.appendChild(fresh[0]);
  again.open = true;
  assert.equal(again.hasAttribute("data-au-preview"), true, "applied before any await");
  assert.equal(again.querySelector(".au-call-output").textContent, "Output\nOUTPUT res-c1");
  assert.equal(again.querySelector(".au-call-input").textContent, "INPUT ev-c1");
  await flush();
  assert.equal(eventsFetches(h, "c1").length, 1, "cache hit performs no fetch");
});

test("the preview cache is bounded and drops the least recently used call", async () => {
  const bundles = {};
  const h = harness(bundles);
  const n = h.api.cap + 6;
  for (let i = 0; i < n; i++) bundles["k" + i] = bundle("k" + i);
  h.merge(turn("r1", "calls", Array.from({ length: n }, (_, i) => callLi("k" + i))));
  const all = h.box.querySelectorAll("details.au-call-details");
  for (let i = 0; i < n; i++) {
    all[i].open = true;
    await flush();
  }
  assert.equal(h.api.cache.size, h.api.cap, "cache never exceeds its cap");
  assert.equal(h.api.cache.has("k0"), false, "oldest entry evicted");
  assert.equal(h.api.cache.has("k" + (n - 1)), true, "newest entry kept");
});
