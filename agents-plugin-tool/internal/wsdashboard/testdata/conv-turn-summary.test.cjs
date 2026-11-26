"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const assetsDir = path.join(__dirname, "..", "assets");
const dashSrc = fs.readFileSync(path.join(assetsDir, "dashboard.js"), "utf8");
const convSrc = fs.readFileSync(path.join(assetsDir, "dashboard-conversation.js"), "utf8");
const auditSrc = fs.readFileSync(path.join(assetsDir, "dashboard-audit-rows.js"), "utf8");
const relaySrc = fs.readFileSync(path.join(assetsDir, "dashboard-companion-relay.js"), "utf8");
const tmplSrc = fs.readFileSync(path.join(assetsDir, "dashboard.tmpl"), "utf8");
const convCss = fs.readFileSync(path.join(assetsDir, "dashboard-conversation.css"), "utf8");

function sliceBetween(src, startMarker, endMarker) {
  const start = src.indexOf(startMarker);
  const end = src.indexOf(endMarker, start);
  assert.ok(start >= 0 && end > start, `markers not found: ${startMarker} .. ${endMarker}`);
  return src.slice(start, end);
}

// Minimal DOM node supporting attributes, classes, details.open, toggle events,
// children, replaceChild/insertBefore, and simple CSS selector matching needed
// by dashboard.js, dashboard-conversation.js, dashboard-audit-rows.js, and
// dashboard-companion-relay.js without browser startup.
class FakeEl {
  constructor(tag, attrs = {}, children = []) {
    this.nodeType = 1;
    this.tagName = tag.toUpperCase();
    this.attrs = Object.create(null);
    this.children = [];
    this.parentNode = null;
    this._open = false;
    this.textContent = "";
    this._listeners = new Map();
    for (const [k, v] of Object.entries(attrs)) {
      if (v === false || v === null || v === undefined) continue;
      if (k === "open") { this._open = true; this.attrs.open = ""; }
      else if (v === true) this.attrs[k] = "";
      else this.attrs[k] = String(v);
    }
    this.classList = {
      _set: () => new Set((this.attrs.class || "").split(/\s+/).filter(Boolean)),
      contains: (c) => this.classList._set().has(c),
      add: (...cs) => { const s = this.classList._set(); cs.forEach(c => s.add(c)); this.attrs.class = Array.from(s).join(" "); },
      remove: (...cs) => { const s = this.classList._set(); cs.forEach(c => s.delete(c)); this.attrs.class = Array.from(s).join(" "); },
      toggle: (c, force) => {
        const has = this.classList.contains(c);
        const on = force === undefined ? !has : !!force;
        if (on) this.classList.add(c); else this.classList.remove(c);
        return on;
      },
    };
    this.dataset = new Proxy({}, {
      get: (_, prop) => {
        const attr = "data-" + String(prop).replace(/[A-Z]/g, m => "-" + m.toLowerCase());
        return this.attrs[attr];
      },
    });
    for (const ch of children) this.appendChild(ch);
  }
  get title() { return this.attrs.title || ""; }
  get hidden() { return Object.prototype.hasOwnProperty.call(this.attrs, "hidden"); }
  set hidden(v) { if (v) this.attrs.hidden = ""; else delete this.attrs.hidden; }
  get open() { return this._open; }
  set open(v) {
    const next = !!v;
    const changed = next !== this._open;
    this._open = next;
    if (next) this.attrs.open = ""; else delete this.attrs.open;
    if (changed && this._onToggle) this._onToggle(this);
  }
  get firstElementChild() { return this.children[0] || null; }
  get lastElementChild() { return this.children[this.children.length - 1] || null; }
  get firstChild() { return this.children[0] || null; }
  get nextSibling() {
    if (!this.parentNode) return null;
    const i = this.parentNode.children.indexOf(this);
    return i >= 0 ? (this.parentNode.children[i + 1] || null) : null;
  }
  get nextElementSibling() { return this.nextSibling; }
  get previousElementSibling() {
    if (!this.parentNode) return null;
    const i = this.parentNode.children.indexOf(this);
    return i > 0 ? this.parentNode.children[i - 1] : null;
  }
  getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null; }
  setAttribute(k, v) {
    this.attrs[k] = String(v);
    if (k === "open") this._open = true;
  }
  hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k); }
  removeAttribute(k) { delete this.attrs[k]; if (k === "open") this._open = false; }
  appendChild(ch) {
    if (ch.parentNode) ch.parentNode.removeChild(ch);
    ch.parentNode = this;
    this.children.push(ch);
    return ch;
  }
  removeChild(ch) {
    const i = this.children.indexOf(ch);
    if (i >= 0) { this.children.splice(i, 1); ch.parentNode = null; }
    return ch;
  }
  insertBefore(ch, ref) {
    if (ch.parentNode) ch.parentNode.removeChild(ch);
    ch.parentNode = this;
    if (!ref) { this.children.push(ch); return ch; }
    const i = this.children.indexOf(ref);
    if (i < 0) this.children.push(ch); else this.children.splice(i, 0, ch);
    return ch;
  }
  replaceChild(next, old) {
    const i = this.children.indexOf(old);
    assert.ok(i >= 0, "replaceChild: old not found");
    if (next.parentNode) next.parentNode.removeChild(next);
    next.parentNode = this;
    old.parentNode = null;
    this.children[i] = next;
    return old;
  }
  matches(sel) {
    return sel.split(",").some(part => matchSimple(this, part.trim()));
  }
  closest(sel) {
    let cur = this;
    while (cur) {
      if (cur.matches(sel)) return cur;
      cur = cur.parentNode;
    }
    return null;
  }
  querySelectorAll(sel) {
    const out = [];
    const walk = (node) => {
      for (const ch of node.children) {
        if (ch.matches(sel)) out.push(ch);
        walk(ch);
      }
    };
    walk(this);
    return out;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}

function matchSimple(el, sel) {
  let s = sel;
  const notMatches = [];
  s = s.replace(/:not\(([^)]+)\)/g, (_, inner) => { notMatches.push(inner.trim()); return ""; });
  if (notMatches.some(inner => matchSimple(el, inner))) return false;
  const attrRe = /\[([^\]=]+)(?:="([^"]*)")?\]/g;
  let m;
  while ((m = attrRe.exec(s)) !== null) {
    const [, k, val] = m;
    if (val === undefined) {
      if (!el.hasAttribute(k)) return false;
    } else if (el.getAttribute(k) !== val) return false;
  }
  s = s.replace(attrRe, "");
  const parts = s.split(".");
  const tag = parts[0].trim();
  if (tag && tag !== "*" && el.tagName !== tag.toUpperCase()) return false;
  for (let i = 1; i < parts.length; i++) {
    if (parts[i] && !el.classList.contains(parts[i])) return false;
  }
  return true;
}

function makeTurnSummaryEl({ entry, key, rev = "1:7:0:11400:2800:0:2", at = "2026-11-06T12:00:02Z", turnAt = "2026-11-06T12:00:08Z", failed = 0, live = false, wake = false, est = false, tokens = "+2.8k tok", countText = "7 tool calls", calls = [] }) {
  const countSpan = new FakeEl("span", { class: "d-turn-count", "data-conv-turn-count": true });
  countSpan.textContent = countText;
  const failsSpan = new FakeEl("span", { class: "d-turn-fails", "data-conv-turn-fails": true, hidden: failed === 0 });
  failsSpan.textContent = failed > 0 ? `${failed} failed` : "";
  const auditCounts = new FakeEl("span", { class: "turn-audit-counts" });
  const mainChildren = [countSpan, failsSpan, auditCounts];
  if (tokens) {
    const tokAttrs = { class: "d-turn-tok" + (est ? " d-turn-est" : ""), "data-conv-turn-tokens": true };
    if (est) tokAttrs["data-estimated"] = "1";
    const tokSpan = new FakeEl("span", tokAttrs);
    tokSpan.textContent = tokens;
    mainChildren.push(tokSpan);
  }
  const summary = new FakeEl("summary", {
    class: "conv-row d-turn-sum",
    "data-conv-turn-summary": true,
    "aria-expanded": "false",
  }, [new FakeEl("span", { class: "row-content d-turn-main" }, mainChildren)]);

  const drawer = new FakeEl("div", { class: "d-turn-drawer", "data-conv-turn-drawer": true }, calls);
  const attrs = {
    class: "toolsline d-turn" + (live ? " live d-turn-live" : "") + (failed ? " d-turn-has-fail" : ""),
    "data-conv-turn": "1",
    "data-key": key || (live ? "turn-live" : `turn-${entry}`),
    "data-turn-rev": rev,
  };
  if (entry) attrs["data-entry"] = entry;
  if (at) attrs["data-at"] = at;
  if (turnAt) attrs["data-turn-at"] = turnAt;
  if (failed > 0) attrs["data-turn-failed"] = String(failed);
  if (wake) attrs["data-wake-turn"] = "1";
  const children = [summary, drawer];
  if (!live && turnAt) children.push(new FakeEl("div", { class: "conv-audit-items" }));
  return new FakeEl("details", attrs, children);
}

test("default collapsed state, toggle aria-expanded, and zero storage/disk writes on toggle", () => {
  const docListeners = new Map();
  let storageWrites = 0;
  const openSection = sliceBetween(dashSrc, "  // --- open details:", "\n  // --- scroll offsets");
  const rootEl = new FakeEl("div");
  const turn = makeTurnSummaryEl({ entry: "002", failed: 0 });
  rootEl.appendChild(turn);

  const sandbox = {
    rid: "sess-1",
    railSets: null,
    load: () => null,
    save: () => { storageWrites++; },
    q: (sel, scope) => (scope || rootEl).querySelectorAll(sel),
    doc: {
      addEventListener(type, fn) { docListeners.set(type, fn); },
    },
    window: { addEventListener() {}, wbHistory: null },
    Object, Array, JSON, String, RegExp,
  };
  vm.runInNewContext(openSection + ";globalThis.__open = { openState, restoreOpen, syncTurnAria };", sandbox);

  const summary = turn.querySelector("[data-conv-turn-summary]");
  assert.equal(turn.open, false);
  assert.equal(summary.getAttribute("aria-expanded"), "false");
  assert.equal(turn.querySelector("[data-conv-turn-fails]").hidden, true);
  assert.equal(turn.querySelectorAll("[data-conv-turn-tokens]").length, 1);

  // User clicks to expand: fires toggle event, updates aria-expanded and openState, zero storage writes.
  turn._onToggle = (el) => docListeners.get("toggle")({ target: el });
  turn.open = true;
  assert.equal(summary.getAttribute("aria-expanded"), "true");
  assert.equal(sandbox.__open.openState["k:turn-002"], true);
  assert.equal(storageWrites, 0, "interactive turn drawer toggle must perform zero storage/disk I/O");

  // User clicks to collapse again.
  turn.open = false;
  assert.equal(summary.getAttribute("aria-expanded"), "false");
  assert.equal(sandbox.__open.openState["k:turn-002"], false);
  assert.equal(storageWrites, 0);
});

test("SSE mergeEntries preserves expanded state across turn updates and keeps scroll lock stick=false intact", () => {
  const docListeners = new Map();
  const openSection = sliceBetween(dashSrc, "  // --- open details:", "\n  // --- scroll offsets");
  const scrollSection = sliceBetween(dashSrc, "  var scrollSnap = null;", "\n  // --- layout and tabs");
  const mergeSection = sliceBetween(convSrc, "  function entryKeys(box) {", "\n  // gapped reports");

  const box = new FakeEl("div", { class: "conv centries" });
  const initial = makeTurnSummaryEl({ entry: "002", rev: "0:2:0:1500:0:300:1", turnAt: "", est: true, tokens: "~+300 tok", countText: "2 tool calls" });
  box.appendChild(initial);

  let scrollTop = 240;
  const convo = {
    isConnected: true,
    wbScrollOwner: false,
    scrollHeight: 1200,
    clientHeight: 300,
    get scrollTop() { return scrollTop; },
    set scrollTop(v) { scrollTop = Math.max(0, Math.min(900, v)); },
    addEventListener(type, fn) { this._ev = this._ev || {}; (this._ev[type] = this._ev[type] || []).push(fn); },
    dispatch(type, ev) { ((this._ev && this._ev[type]) || []).forEach(fn => fn(ev || {})); },
  };
  const btn = new FakeEl("button", { id: "convo-bottom-btn" });
  btn.addEventListener = () => {};
  const rafs = [];

  const sandbox = {
    rid: "sess-1",
    railSets: null,
    PANES: ["convo"],
    paneObserver: null,
    paneVisible: { convo: true },
    visible: (el) => el === convo,
    $: (id) => (id === "convo" ? convo : id === "convo-bottom-btn" ? btn : null),
    load: () => null,
    save: () => {},
    fmtTimes: () => {},
    q: (sel, scope) => (scope || box).querySelectorAll(sel),
    doc: {
      scrollingElement: null,
      addEventListener(type, fn) { docListeners.set(type, fn); },
      dispatchEvent() {},
    },
    window: { addEventListener() {}, wbHistory: null, ResizeObserver: null },
    requestAnimationFrame: (fn) => { rafs.push(fn); return rafs.length; },
    Date, Math, Map, Object, Array, JSON, String, RegExp,
  };
  vm.runInNewContext(
    openSection + "\n" + scrollSection + "\n" + mergeSection +
    ";globalThis.__api = { openState, restoreOpen, mergeEntries, get stick() { return stick; }, scrollState, restoreScroll };",
    sandbox
  );

  // User scrolls up to read history (stick becomes false).
  convo.dispatch("wheel", { deltaY: -100 });
  convo.dispatch("scroll");
  while (rafs.length) rafs.shift()();
  assert.equal(sandbox.__api.stick, false);
  const snap = sandbox.__api.scrollState();

  // User opens the collapsed turn summary drawer.
  initial._onToggle = (el) => docListeners.get("toggle")({ target: el });
  initial.open = true;
  assert.equal(initial.querySelector("[data-conv-turn-summary]").getAttribute("aria-expanded"), "true");

  // Incoming SSE update transitions orphan -> completed with exact +2.8k tok and 1 failure.
  const incoming = new FakeEl("div", {}, [
    makeTurnSummaryEl({ entry: "002", rev: "1:7:1:11400:2800:0:2", failed: 1, est: false, tokens: "+2.8k tok", countText: "7 tool calls" }),
    new FakeEl("span", { hidden: true, "data-entry": "004", "data-turn-absorbed": "1" }),
  ]);
  const added = sandbox.__api.mergeEntries(box, incoming);
  assert.equal(added.length, 2, "changed data-turn-rev must replace turn summary node and insert absorbed marker");
  const updated = box.children[0];
  assert.equal(updated.open, true, "expanded state must survive SSE mergeEntries");
  assert.equal(updated.querySelector("[data-conv-turn-summary]").getAttribute("aria-expanded"), "true");
  assert.equal(updated.querySelector("[data-conv-turn-fails]").hidden, false);
  assert.equal(updated.querySelector("[data-conv-turn-tokens]").textContent, "+2.8k tok");
  assert.equal(updated.querySelector("[data-conv-turn-tokens]").hasAttribute("data-estimated"), false);

  // Restore scroll after swap: scroll lock remains false and reading offset unchanged.
  sandbox.__api.restoreScroll(snap);
  assert.equal(sandbox.__api.stick, false);
  assert.equal(convo.scrollTop, 240);

  // Unchanged rev on next SSE tick performs zero replacement (returns empty array).
  const unchanged = new FakeEl("div", {}, [
    makeTurnSummaryEl({ entry: "002", rev: "1:7:1:11400:2800:0:2", failed: 1, est: false, tokens: "+2.8k tok", countText: "7 tool calls" }),
  ]);
  assert.equal(sandbox.__api.mergeEntries(box, unchanged).length, 0);
});

test("dashboard-audit-rows recognizes [data-conv-turn=1][data-turn-at] as turn footer", () => {
  const start = auditSrc.indexOf("    function auRenderedEntries(box) {");
  const end = auditSrc.indexOf("\n    function auRenderedCallMap(", start);
  assert.ok(start >= 0 && end > start, "auRenderedEntries not found");
  const parseSection = sliceBetween(auditSrc, "  function auParseRFC3339Ns(raw) {", "\n  function auCmpAuditEvent(");
  const body = parseSection + "\n" + auditSrc.slice(start, end);

  const box = new FakeEl("div", { class: "conv centries" }, [
    new FakeEl("div", { class: "msg user", "data-entry": "001", "data-at": "2026-11-06T12:00:01Z" }),
    makeTurnSummaryEl({ entry: "002", at: "2026-11-06T12:00:02Z", turnAt: "2026-11-06T12:00:08Z" }),
    new FakeEl("span", { hidden: true, "data-entry": "003", "data-turn-absorbed": "1", "data-at": "2026-11-06T12:00:08Z" }),
  ]);
  const sandbox = { Date, Number, String, isFinite };
  vm.runInNewContext(body + ";globalThis.__run = auRenderedEntries;", sandbox);

  const entries = sandbox.__run(box);
  assert.equal(entries.length, 2, "hidden absorbed span is skipped; user + completed turn remain");
  const turnEntry = entries[1];
  assert.equal(turnEntry.turn, true, "completed turn summary must act as audit turn footer");
  assert.equal(turnEntry.footer, box.children[1]);
  assert.equal(turnEntry.at, "2026-11-06T12:00:08Z", "turn footer must use data-turn-at timestamp");
});

test("dashboard-companion-relay fires session.error and session.celebrate across live-absorbed -> completed transition", () => {
  const start = relaySrc.indexOf("  function startCompanionRelay() {");
  const end = relaySrc.indexOf("\n    function schedule(", start);
  const celebrateAndArchived = sliceBetween(relaySrc, "    function celebrate(row) {", "\n    function publish(");
  assert.ok(start >= 0 && celebrateAndArchived.length > 0);

  const box = new FakeEl("div", { class: "conv centries" });
  const events = [];
  const tokens = new Set();
  const sandbox = {
    rid: "sess-1",
    baseline: false,
    newest: "",
    liveAbsorbed: new Set(),
    agents: new Map(),
    directCommit: /^(?:mcp__[^\s]+__)?(?:git_commit|git_merge|(?:ws\/)?git\.(?:commit|merge))$/i,
    shellTool: /^(?:mcp__[^\s]+__)?(?:bash|shell(?:_command)?|exec_command|run_command|terminal)$/i,
    concreteCommand: /(?:^|[;&|\n])\s*(?:command:\s*)?(?:[A-Z_][A-Z_0-9]*=[^\s;]+\s+)*(?:git\s+(?:commit|merge)|go\s+(?:-C\s+\S+\s+)?test|npm\s+test|pytest|cargo\s+test|(?:npx\s+)?(?:vitest|jest))(?=\s|$)/i,
    entriesBox: () => box,
    q: (sel, scope) => (scope || box).querySelectorAll(sel),
    event(kind, key) {
      const tok = `${kind}:${key}`;
      if (tokens.has(tok)) return;
      tokens.add(tok);
      events.push({ kind, key });
    },
    reportIdentity: () => "",
    done() {},
    reportDone() {},
    Set, Map, Number, Date,
  };
  vm.runInNewContext(celebrateAndArchived + ";globalThis.__archived = archived;", sandbox);

  // Step 1: Active turn flushes RingTools (002) as live-absorbed and an interim reply (003).
  box.appendChild(new FakeEl("span", { hidden: true, "data-entry": "002", "data-turn-live-absorbed": "1" }));
  box.appendChild(new FakeEl("div", { class: "msg fromlead", "data-entry": "003" }));
  sandbox.__archived();
  assert.deepEqual(events, [], "live-absorbed span should not prematurely emit error/celebrate");

  // Step 2: Turn completes -> 002 becomes completed turn summary with passing go test call.
  const auTool = new FakeEl("span", { class: "au-tool", title: "Bash" });
  auTool.textContent = "Bash";
  const auArgs = new FakeEl("span", { class: "au-args au-pv", title: "command: go test ./internal/wsdashboard/..." });
  const auRow = new FakeEl("summary", { class: "conv-row au-row au-done", "data-call": "c1" }, [auTool, auArgs]);
  box.replaceChild(makeTurnSummaryEl({ entry: "002", failed: 0, calls: [auRow] }), box.children[0]);
  sandbox.__archived();
  assert.deepEqual(events, [{ kind: "session.celebrate", key: "002" }]);

  // Step 3: Next turn completes with failure -> emits session.error (and not celebrate).
  box.appendChild(makeTurnSummaryEl({ entry: "005", failed: 1 }));
  sandbox.__archived();
  assert.deepEqual(events, [
    { kind: "session.celebrate", key: "002" },
    { kind: "session.error", key: "005" },
  ]);

  // Step 4: Older history pagination prepends an old failed turn (000) -> ignored.
  box.insertBefore(makeTurnSummaryEl({ entry: "000", failed: 1 }), box.firstChild);
  sandbox.__archived();
  assert.equal(events.length, 2, "prepended historical turns must not trigger live relay events");
});

test("template and CSS expose the shared DOM contract and reduced-motion guard", () => {
  for (const attr of [
    "data-conv-turn=\"1\"",
    "data-conv-turn-summary",
    "data-conv-turn-drawer",
    "data-conv-turn-count",
    "data-conv-turn-fails",
    "data-conv-turn-tokens",
    "data-estimated=\"1\"",
    "data-turn-rev=\"{{.TurnRev}}\"",
  ]) {
    assert.ok(tmplSrc.includes(attr), `dashboard.tmpl missing ${attr}`);
  }
  assert.ok(convCss.includes(".d-turn"), "dashboard-conversation.css missing .d-turn styles");
  assert.ok(convCss.includes("prefers-reduced-motion: reduce"), "dashboard-conversation.css missing reduced-motion rule");
});
