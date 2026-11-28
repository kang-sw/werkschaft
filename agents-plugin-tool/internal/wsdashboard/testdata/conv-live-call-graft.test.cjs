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
const auditViewSrc = fs.readFileSync(path.join(assetsDir, "dashboard-audit.js"), "utf8");

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
  get title() { return this.getAttribute("title") || ""; }
  set title(v) { this.setAttribute("title", v); }
  get hidden() { return this.attrs.has("hidden"); }
  set hidden(v) { if (v) this.attrs.set("hidden", ""); else this.attrs.delete("hidden"); }
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

// harness wires the production sources against one document and a fetch stub;
// bundles maps a call id to its audit bundle.
function harness(bundles, opts = {}) {
  const listeners = new Map();
  const fetches = [];
  const updatedEvents = [];
  const openState = {};
  const doc = {
    root: null,
    addEventListener(type, fn) { listeners.set(type, (listeners.get(type) || []).concat(fn)); },
    dispatchEvent(ev) { (listeners.get(ev.type) || []).forEach((fn) => fn(ev)); },
    createElement: (tag) => new El(tag),
  };
  toggleHook = (target) => doc.dispatchEvent({ type: "toggle", target });
  class CustomEvent { constructor(type, init) { this.type = type; this.detail = (init && init.detail) || {}; } }
  const pins = { n: 0 };
  const defaultFetch = (url) => {
    const call = /call=([^&]+)/.exec(url);
    const body = /\/event\/([^?]+)\?part=(in|out)/.exec(url);
    let payload = null;
    if (call) payload = bundles[decodeURIComponent(call[1])];
    else if (body) payload = { data: body[2] === "in" ? "INPUT " + body[1] : "OUTPUT " + body[1], end: true };
    return Promise.resolve({ ok: !!payload, status: payload ? 200 : 404, json: () => Promise.resolve(payload) });
  };
  const impl = opts.fetchImpl || defaultFetch;
  const fetch = (url) => { fetches.push(url); return impl(url); };

  const convo = new El("div");
  convo.isRoot = true;
  convo.setAttribute("id", "convo");
  const entriesReg = new El("div");
  entriesReg.setAttribute("id", "r-entries");
  const box = new El("div");
  box.setAttribute("class", "centries");
  entriesReg.appendChild(box);
  const convlive = new El("div");
  convlive.setAttribute("id", "r-convlive");
  convo.appendChild(entriesReg);
  convo.appendChild(convlive);
  doc.root = convo;

  const wrap = (nodes) => { const w = new El("div"); nodes.forEach((n) => w.appendChild(n)); return w; };
  const openKey = (d) => (d && d.hasAttribute && d.hasAttribute("data-key") ? "k:" + d.getAttribute("data-key") : null);
  const syncTurnAria = (d) => {
    const s = d && d.firstElementChild;
    if (s && s.hasAttribute && s.hasAttribute("data-conv-turn-summary")) s.setAttribute("aria-expanded", d.open ? "true" : "false");
  };
  const restoreOpen = (el) => {
    const all = el.tagName === "DETAILS" ? [el] : [];
    if (el.querySelectorAll) all.push(...el.querySelectorAll("details"));
    all.forEach((d) => {
      const k = openKey(d);
      if (k && Object.prototype.hasOwnProperty.call(openState, k)) d.open = openState[k];
      syncTurnAria(d);
    });
  };
  doc.addEventListener("toggle", (e) => {
    const d = e && e.target;
    if (!d || d.tagName !== "DETAILS") return;
    syncTurnAria(d);
    const k = openKey(d);
    if (k) openState[k] = d.open;
  });
  doc.addEventListener("ws-call-details-updated", (e) => {
    const el = e && e.detail && e.detail.el;
    updatedEvents.push({ el, connected: !!(el && el.isConnected), completed: !!(e && e.detail && e.detail.completed) });
  });

  const sandbox = {
    doc, CustomEvent, fetch, Map, Object, Array, Promise, WeakSet, Number, isFinite,
    encodeURIComponent, decodeURIComponent, String, JSON,
    auRowsRid: opts.auRowsRid !== undefined ? opts.auRowsRid : "sess",
    auEpoch: 0,
    auLatestAfter: "",
    auCoveredSince: "",
    auOpenBelowDone: false,
    auUnboundedDone: false,
    auReset410Done: false,
    auFailedJobs: [],
    auFetchQueue: Promise.resolve(),
    auEventsById: Object.create(null),
    auEntriesContainer: () => box,
    auFirstDataEntryAt: () => "",
    auScheduleReconcile() {},
    auInitialSync() {},
    stick: false,
    pinConvo() { pins.n++; },
    wsT: (k) => (k === "js.audit.tab.out" ? "Output" : k),
    auIsResultSummary: (ev, id) => !!ev && ev.call_id === id && ev.kind === "tool_result",
    auFmtSpan: (ms) => ms + "ms",
    auEstContextText: (inB, outB) => (inB || outB ? "+" + (inB + outB) + "B" : ""),
    auEstContextTitle: (inB, outB) => inB + "/" + outB,
    $: (id) => (id === "convo" ? convo : null),
    q: (sel, el) => (el || convo).querySelectorAll(sel),
    region: (name) => (name === "convlive" ? convlive : name === "entries" ? entriesReg : null),
    frag: (html) => wrap(parse(html)),
    openState, openKey, syncTurnAria,
    convoSwapping: 0,
    restoreOpen, fmtTimes() {},
  };
  vm.runInNewContext(
    sliceBetween(auditSrc, "    function auDirectItems(el) {", "\n    function auRenderedOwners(") + "\n" +
    sliceBetween(auditSrc, "    doc.addEventListener(\"ws-history-reset\", function () {", "\n    doc.addEventListener(\"ws-approval-rows-changed\"") + "\n" +
    sliceBetween(convSrc, "  function entryKeys(box) {", "\n  // gapped reports") + "\n" +
    sliceBetween(auditViewSrc, "  function auApplyHydration(summaries, markChecked, opts) {", "\n  function auGetHydrationState(") +
    ";globalThis.__api = { mergeEntries, swapConvLive, syncCallSummary, liveCallStore, auApplyHydration, reset: () => doc.dispatchEvent({ type: 'ws-history-reset' }), cache: auPreviewCache, cap: AU_PREVIEW_CAP };",
    sandbox
  );
  const merge = (html) => sandbox.__api.mergeEntries(box, wrap(parse(html)));
  const swapLive = (html) => sandbox.__api.swapConvLive(convlive, html);
  return { doc, box, convlive, fetches, pins, merge, swapLive, openState, updatedEvents, api: sandbox.__api, parse, sandbox };
}

const flush = async () => { for (let i = 0; i < 40; i++) await Promise.resolve(); };
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

test("LRU recency promotion keeps re-opened entry and history-reset clears cache and rejects stale fetches", async () => {
  let resolveSlow;
  const slowDone = new Promise((r) => { resolveSlow = r; });
  const h = harness({
    slow: bundle("slow"),
  }, {
    fetchImpl: (url) => {
      if (url.includes("call=slow")) {
        return new Promise((r) => {
          resolveSlow = () => r({ ok: true, status: 200, json: () => Promise.resolve(bundle("slow")) });
          slowDone;
        });
      }
      const call = /call=([^&]+)/.exec(url);
      const body = /\/event\/([^?]+)\?part=(in|out)/.exec(url);
      const payload = call ? bundle(decodeURIComponent(call[1])) : { data: body[2] === "in" ? "IN" : "OUT", end: true };
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(payload) });
    },
  });
  const cap = h.api.cap;
  const calls = Array.from({ length: cap }, (_, i) => callLi("m" + i));
  h.merge(turn("r1", "calls", calls));
  const all = h.box.querySelectorAll("details.au-call-details");
  for (let i = 0; i < cap; i++) { all[i].open = true; await flush(); }
  // Re-open m0 (cached) to promote it to MRU before adding one more call.
  all[0].open = false;
  all[0].removeAttribute("data-au-preview");
  all[0].open = true;
  await flush();
  h.merge(turn("r2", "calls", calls.concat(callLi("extra"))));
  h.box.querySelector('details[data-key="call-extra"]').open = true;
  await flush();
  assert.equal(h.api.cache.has("m0"), true, "re-touched m0 is promoted and kept");
  assert.equal(h.api.cache.has("m1"), false, "m1 became LRU and was evicted");

  // Stale in-flight fetch across ws-history-reset must not write cache or DOM.
  h.merge(turn("r3", "calls", [callLi("slow", { dur: "" })]));
  const slowEl = h.box.querySelector('details[data-key="call-slow"]');
  slowEl.open = true;
  await Promise.resolve();
  h.api.reset();
  assert.equal(h.api.cache.size, 0, "history-reset clears auPreviewCache");
  resolveSlow();
  await flush();
  assert.equal(h.api.cache.has("slow"), false, "stale fetch from prior epoch must not repopulate cache");
  assert.equal(slowEl.querySelector(".au-call-output"), null, "stale fetch must not mutate DOM after epoch change");

  // Empty auRowsRid performs zero fetches.
  const emptyRid = harness({ c1: bundle("c1") }, { auRowsRid: "" });
  emptyRid.merge(turn("r1", "1 call", [callLi("c1")]));
  emptyRid.box.querySelector("details.au-call-details").open = true;
  await flush();
  assert.equal(emptyRid.fetches.length, 0, "empty auRowsRid skips preview fetch");
});

const liveRegionHtml = (rev, calls) =>
  `<div class="conv clive" data-phase="running"><details class="toolsline d-turn live d-turn-live" data-conv-turn="1" data-key="turn-live" data-turn-rev="${rev}">` +
  `<summary class="conv-row d-turn-sum" data-conv-turn-summary aria-expanded="false"><span class="d-turn-count">${rev}</span></summary>` +
  `<div class="d-turn-drawer" data-conv-turn-drawer><div class="d-turn-gap"><ol class="au-calls">${calls.join("")}</ol></div></div></details></div>`;

const completedTurn = (entry, rev, calls) =>
  `<details class="toolsline d-turn" data-conv-turn="1" data-entry="${entry}" data-key="turn-${entry}" data-turn-rev="${rev}">` +
  `<summary class="conv-row d-turn-sum" data-conv-turn-summary aria-expanded="false"><span class="d-turn-count">${rev}</span></summary>` +
  `<div class="d-turn-drawer" data-conv-turn-drawer><div class="d-turn-gap"><ol class="au-calls">${calls.join("")}</ol></div></div><div class="conv-audit-items"></div></details>`;

test("swapConvLive and r-convlive -> r-entries handoff preserve open call and outer turn state in both orders", async () => {
  // Order A: r-entries completes while r-convlive still holds LiveTurnSummary.
  const bundlesA = { c1: bundle("c1", { result: false }) };
  const hA = harness(bundlesA);
  hA.swapLive(liveRegionHtml("r1", [callLi("c1", { dur: "" })]));
  const liveTurnA = hA.convlive.querySelector("details.d-turn-live");
  liveTurnA.open = true;
  const callA = hA.convlive.querySelector('details[data-key="call-c1"]');
  callA.open = true;
  await flush();
  const inputA = callA.querySelector(".au-call-input");
  assert.equal(callA.hasAttribute("data-au-out-pending"), true);

  // Streaming tick updates rev inside r-convlive without rebuilding callA or collapsing liveTurnA.
  hA.swapLive(liveRegionHtml("r2", [callLi("c1", { dur: "" }), callLi("c2", { dur: "" })]));
  await flush();
  assert.equal(hA.convlive.querySelector('details[data-key="call-c1"]'), callA);
  assert.equal(hA.convlive.querySelector("details.d-turn-live").open, true);
  assert.equal(callA.querySelector(".au-call-input"), inputA);
  assert.equal(eventsFetches(hA, "c1").length, 1);

  // Live->completed handoff replaces absorbed placeholder in r-entries while r-convlive still holds liveTurnA.
  hA.merge(`<span hidden data-entry="002" data-turn-live-absorbed="1"></span>`);
  hA.sandbox.convoSwapping = 1;
  bundlesA.c1 = bundle("c1");
  hA.merge(completedTurn("002", "done", [callLi("c1", { dur: "" })]));
  hA.sandbox.convoSwapping = 0;
  await flush();
  const handedTurn = hA.box.querySelector('details[data-entry="002"]');
  assert.equal(handedTurn.open, true, "outer turn stays open across r-convlive -> r-entries handoff");
  assert.equal(handedTurn.querySelector("[data-conv-turn-summary]").getAttribute("aria-expanded"), "true");
  assert.equal(hA.openState["k:turn-002"], true);
  assert.equal(handedTurn.querySelector('details[data-key="call-c1"]'), callA, "call node grafted across regions");
  assert.equal(callA.querySelector(".au-call-output").textContent, "Output\nOUTPUT res-c1", "completion output fetched on handoff");
  assert.ok(hA.updatedEvents.some((ev) => ev.el === callA && ev.connected === true && ev.completed === true), "ws-call-details-updated fires AFTER attachment");

  // Order B: r-convlive clears BEFORE r-entries receives the completed TurnSummary.
  const hB = harness({ c9: bundle("c9") });
  hB.swapLive(liveRegionHtml("r1", [callLi("c9", { dur: "" })]));
  hB.convlive.querySelector("details.d-turn-live").open = true;
  const callB = hB.convlive.querySelector('details[data-key="call-c9"]');
  callB.open = true;
  await flush();
  // convlive goes idle first.
  hB.swapLive(`<div class="conv clive" data-phase="idle"></div>`);
  assert.equal(hB.api.liveCallStore().turnOpen, true);
  // An older history page merged before handoff must NOT consume turnOpen.
  hB.merge(completedTurn("000", "old", [callLi("old1")]));
  assert.equal(hB.box.querySelector('details[data-entry="000"]').open, false, "history page must not inherit live turnOpen");
  // Now the completed turn arrives in r-entries.
  hB.sandbox.convoSwapping = 1;
  hB.merge(completedTurn("005", "done", [callLi("c9", { dur: "2ms" })]));
  hB.sandbox.convoSwapping = 0;
  const doneB = hB.box.querySelector('details[data-entry="005"]');
  assert.equal(doneB.open, true, "turnOpen restored via liveCallStore stash");
  assert.equal(doneB.querySelector('details[data-key="call-c9"]'), callB, "callB grafted from liveCallStore stash");
});

test("syncCallSummary preserves au-sel and hydrated fields against empty server cells", () => {
  const h = harness({});
  const cur = parse(callLi("c1", { dur: "120ms", fail: true }))[0].querySelector("summary");
  cur.classList.add("au-cur");
  cur.classList.add("au-sel");
  cur.setAttribute("data-au-hydrated", "1");
  cur.querySelector(".au-tok").setAttribute("title", "100 in / 20 out · 1000 → 1120");
  const fresh = parse(`<summary class="conv-row au-row au-done" data-call="c1"><span class="row-icon au-mk">ok</span><span class="row-content trace-content"><span class="au-tool">Bash</span></span><span class="row-time au-right"><span class="au-att" hidden></span><span class="au-dur"></span><span class="au-tok"></span></span></summary>`)[0];
  h.api.syncCallSummary(cur, fresh);
  assert.equal(cur.classList.contains("au-cur"), true);
  assert.equal(cur.classList.contains("au-sel"), true);
  assert.equal(cur.classList.contains("au-fail"), true, "hydrated au-fail preserved");
  assert.equal(cur.querySelector(".row-icon").textContent, "x", "hydrated fail icon preserved");
  assert.equal(cur.querySelector(".au-dur").textContent, "120ms", "hydrated dur preserved against empty server cell");
  assert.equal(cur.querySelector(".au-tok").textContent, "+1k", "hydrated tok preserved against empty server cell");
  assert.equal(cur.querySelector(".au-tok").getAttribute("title"), "100 in / 20 out · 1000 → 1120");

  // Non-empty server dur still updates cleanly, and fresh tok without title keeps hydrated title.
  const fresh2 = parse(`<summary class="conv-row au-row au-done" data-call="c1"><span class="row-icon au-mk">ok</span><span class="row-content trace-content"><span class="au-tool">Bash</span></span><span class="row-time au-right"><span class="au-att" hidden></span><span class="au-dur">2s</span><span class="au-tok">+2k</span></span></summary>`)[0];
  h.api.syncCallSummary(cur, fresh2);
  assert.equal(cur.querySelector(".au-dur").textContent, "2s");
  assert.equal(cur.querySelector(".au-tok").textContent, "+2k");
  assert.equal(cur.querySelector(".au-tok").getAttribute("title"), "100 in / 20 out · 1000 → 1120");
});

test("in-flight completion race, non-dur signals (<1ms), and transient error retry work cleanly", async () => {
  // 1. In-flight race: initial fetch still pending when completion arrives.
  let resolveInitial;
  let phase = "running";
  const hRace = harness({}, {
    fetchImpl: (url) => {
      if (url.includes("/events?call=race")) {
        if (phase === "running") {
          return new Promise((r) => {
            resolveInitial = () => r({ ok: true, status: 200, json: () => Promise.resolve(bundle("race", { result: false })) });
          });
        }
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(bundle("race", { result: true })) });
      }
      const m = /part=(in|out)/.exec(url);
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ data: m[1].toUpperCase(), end: true }) });
    },
  });
  hRace.swapLive(liveRegionHtml("r1", [callLi("race", { dur: "" }).replace("+1k", "")]));
  const dRace = hRace.convlive.querySelector('details[data-key="call-race"]');
  dRace.open = true;
  await Promise.resolve();
  assert.equal(dRace.hasAttribute("data-au-out-inflight"), true);

  // Completion arrives while initial fetch is still in flight (via <1ms non-dur .au-tok change).
  phase = "done";
  hRace.swapLive(liveRegionHtml("r2", [callLi("race", { dur: "" })]));
  assert.equal(dRace.hasAttribute("data-au-refresh-queued"), true);
  resolveInitial();
  await flush();
  assert.equal(eventsFetches(hRace, "race").length, 2, "queued completion refetch runs once after in-flight resolves");
  assert.equal(dRace.querySelector(".au-call-output").textContent, "Output\nOUT");
  assert.equal(dRace.hasAttribute("data-au-out-pending"), false);

  // 2. Transient error on completion refetch preserves data-au-out-pending for retry.
  let failNext = false;
  const hErr = harness({}, {
    fetchImpl: (url) => {
      if (url.includes("/events?call=err1")) {
        if (failNext) return Promise.resolve({ ok: false, status: 500, json: () => Promise.resolve(null) });
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(bundle("err1", { result: phase === "done" })) });
      }
      const m = /part=(in|out)/.exec(url);
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ data: m[1].toUpperCase(), end: true }) });
    },
  });
  phase = "running";
  hErr.swapLive(liveRegionHtml("r1", [callLi("err1", { dur: "" })]));
  const dErr = hErr.convlive.querySelector('details[data-key="call-err1"]');
  dErr.open = true;
  await flush();
  assert.equal(dErr.hasAttribute("data-au-out-pending"), true);
  failNext = true;
  hErr.swapLive(liveRegionHtml("r2", [callLi("err1", { dur: "1s" })]));
  await flush();
  assert.equal(dErr.hasAttribute("data-au-out-pending"), true, "transient error must not drop data-au-out-pending");
  failNext = false;
  phase = "done";
  // Hydration tool_result completion triggers retry.
  hErr.api.auApplyHydration([{ call_id: "err1", kind: "tool_result", out_bytes: 7 }], true);
  await flush();
  assert.equal(dErr.hasAttribute("data-au-out-pending"), false);
  assert.equal(dErr.querySelector(".au-call-output").textContent, "Output\nOUT");
});
