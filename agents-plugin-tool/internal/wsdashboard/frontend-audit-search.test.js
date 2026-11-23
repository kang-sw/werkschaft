"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { test } = require("node:test");
const vm = require("node:vm");

const searchApi = require("./assets/dashboard-audit-search.js");

function loadAuditWindowHelpers() {
  const src = fs.readFileSync(path.join(__dirname, "assets", "dashboard-audit-window.js"), "utf8");
  const start = src.indexOf("  function auEsc(s) {");
  const end = src.indexOf("  function auBuildWindowDOM(");
  assert.ok(start >= 0 && end > start, "auEsc..auBuildWindowDOM markers not found");
  const snippet = src.slice(start, end);
  const sandbox = {
    wsT(k, a) { return a == null ? k : `${k}:${a}`; },
    wsTn(k, n) { return `${k}:${n}`; },
    renderMd(s) { return String(s); },
    JSON,
    Math,
    Date,
    Object,
    Array,
    String,
    Number,
    RegExp,
    Error
  };
  vm.runInNewContext(
    snippet + "\nglobalThis.__exports = { auRenderBodyContent, auRenderLineText, auSplitLinesWithBytes, auSpansByLine, auBuildSearchMatcher: typeof auBuildSearchMatcher === 'undefined' ? undefined : 1, auTranslateRe2: typeof auTranslateRe2 === 'undefined' ? undefined : 1 };",
    sandbox
  );
  return sandbox.__exports;
}

const winHelpers = loadAuditWindowHelpers();

function makeMockFetchEnv(handlers) {
  const winSrc = fs.readFileSync(path.join(__dirname, "assets", "dashboard-audit-search.js"), "utf8");
  const noop = () => {};
  const makeEl = (tag) => {
    const attrs = {};
    const children = [];
    const el = {
      tagName: (tag || "DIV").toUpperCase(),
      isConnected: true,
      children,
      className: "",
      classList: { add: noop, remove: noop, toggle: noop },
      hidden: false,
      disabled: false,
      value: "",
      innerHTML: "",
      textContent: "",
      listeners: {},
      setAttribute(k, v) { attrs[k] = String(v); },
      getAttribute(k) { return Object.prototype.hasOwnProperty.call(attrs, k) ? attrs[k] : null; },
      addEventListener(ev, fn) { (el.listeners[ev] = el.listeners[ev] || []).push(fn); },
      appendChild(c) { children.push(c); c.parentNode = el; return c; },
      insertBefore(c) { children.unshift(c); c.parentNode = el; return c; },
      removeChild(c) { const idx = children.indexOf(c); if (idx >= 0) children.splice(idx, 1); },
      querySelector(sel) {
        if (el._qMap && el._qMap[sel]) return el._qMap[sel];
        for (const c of children) {
          if (c && c._sel === sel) return c;
          const nested = c && c.querySelector ? c.querySelector(sel) : null;
          if (nested) return nested;
        }
        return null;
      },
      querySelectorAll() { return []; },
      focus: noop,
      closest() { return null; },
      contains() { return true; }
    };
    return el;
  };
  const wrap = makeEl("div");
  wrap._sel = ".au-swrap";
  const form = makeEl("form"); form._sel = ".au-sbox";
  const inp = makeEl("input"); inp._sel = ".au-sq";
  const clr = makeEl("button"); clr._sel = ".au-sclear";
  const rx = makeEl("button"); rx._sel = '[data-au-sopt="regex"]';
  const cs = makeEl("button"); cs._sel = '[data-au-sopt="case"]';
  const kindSel = makeEl("select"); kindSel._sel = ".au-skind";
  const subBtn = makeEl("button"); subBtn._sel = ".au-sbtn";
  const listBox = makeEl("div"); listBox._sel = ".au-slist";
  const hdEl = makeEl("div"); hdEl._sel = ".au-slist-hd";
  const statEl = makeEl("span"); statEl._sel = ".au-slist-stat";
  const retryBtn = makeEl("button"); retryBtn._sel = ".au-sretry";
  const colBtn = makeEl("button"); colBtn._sel = ".au-scollapse";
  const hitsEl = makeEl("div"); hitsEl._sel = ".au-shits";
  const footEl = makeEl("div"); footEl._sel = ".au-sfoot";
  const moreBtn = makeEl("button"); moreBtn._sel = ".au-smore";
  wrap._qMap = {
    ".au-sbox": form, ".au-sq": inp, ".au-sclear": clr,
    '[data-au-sopt="regex"]': rx, '[data-au-sopt="case"]': cs,
    ".au-skind": kindSel, ".au-sbtn": subBtn, ".au-slist": listBox,
    ".au-slist-hd": hdEl, ".au-slist-stat": statEl, ".au-sretry": retryBtn,
    ".au-scollapse": colBtn, ".au-shits": hitsEl, ".au-sfoot": footEl, ".au-smore": moreBtn
  };
  const bar = makeEl("div"); bar._sel = ".au-bar"; bar.appendChild(wrap);
  const convo = makeEl("div"); convo.id = "convo"; convo.appendChild(bar);

  const sandbox = {
    URL,
    Promise,
    Object,
    Array,
    String,
    Number,
    JSON,
    Math,
    RegExp,
    AbortController: globalThis.AbortController,
    encodeURIComponent,
    rid: "sess-1",
    root: { getAttribute: () => "sess-1" },
    wsT: (k, a) => (a == null ? k : `${k}:${a}`),
    wsTn: (k, n, a) => `${k}:${n}:${a}`,
    escapeHtml: (s) => String(s),
    document: {
      readyState: "complete",
      getElementById: (id) => (id === "convo" ? convo : null),
      createElement: makeEl,
      addEventListener: noop
    },
    window: { addEventListener: noop, WsAudit: { hooks: {} } },
    fetch: handlers.fetch
  };
  vm.runInNewContext(winSrc.replace("buildSearchUrl: buildSearchUrl,", "bindUI: bindSearchUI, buildSearchUrl: buildSearchUrl,"), sandbox);
  sandbox.window.WsAuditSearch.bindUI(wrap);
  return { api: sandbox.window.WsAuditSearch, sandbox, elements: { subBtn, statEl, listBox, retryBtn, footEl, moreBtn, hitsEl, inp, rxBtn: rx, csBtn: cs, kindSel } };
}

test("buildSearchUrl encodes q, regex, case, optional kinds and cursor", () => {
  assert.equal(
    searchApi.buildSearchUrl("r/1", { q: "foo bar", regex: false, caseSensitive: false, kinds: [] }, ""),
    "/s/r%2F1/audit/search?q=foo%20bar&regex=0&case=0"
  );
  assert.equal(
    searchApi.buildSearchUrl("sess-1", { q: "err.*500", regex: true, caseSensitive: true, kinds: ["tool_result", "hook"], limit: 50 }, "cur-9"),
    "/s/sess-1/audit/search?q=err.*500&regex=1&case=1&kinds=tool_result%2Chook&n=50&cursor=cur-9"
  );
  assert.equal(
    searchApi.buildRangeUrl("sess-1", "ev/1", "out", 4096, 1024),
    "/s/sess-1/audit/event/ev%2F1?part=out&offset=4096&len=1024"
  );
});

test("aggregateHitBadges resolves call_id, parent_id subagent routing, event_id fallback, and multi-field counts", () => {
  const hits = [
    { event_id: "e1", call_id: "c1", parent_id: "", field: "in", line_no: 2 },
    { event_id: "e2", call_id: "c1", parent_id: "", field: "out", line_no: 8 },
    { event_id: "e2", call_id: "c1", parent_id: "", field: "out", line_no: 14 },
    { event_id: "esub", call_id: "c_sub", parent_id: "c_parent", field: "out", line_no: 5 },
    { event_id: "ehid", call_id: "", parent_id: "", field: "in", line_no: 3 },
    { event_id: "eunrendered", call_id: "c_missing", parent_id: "", field: "out", line_no: 1 }
  ];
  const agg = searchApi.aggregateHitBadges(hits, {
    hasCall: (id) => id === "c1" || id === "c_parent",
    hasEvent: (id) => id === "ehid"
  });
  assert.equal(agg.ordered.length, 3);
  assert.deepEqual(agg.byKey["call:c1"], {
    key: "call:c1", rowType: "call", rowId: "c1", inCount: 1, outCount: 2, firstHit: hits[0], viaParent: false
  });
  assert.deepEqual(agg.byKey["call:c_parent"], {
    key: "call:c_parent", rowType: "call", rowId: "c_parent", inCount: 0, outCount: 1, firstHit: hits[3], viaParent: true
  });
  assert.deepEqual(agg.byKey["event:ehid"], {
    key: "event:ehid", rowType: "event", rowId: "ehid", inCount: 1, outCount: 0, firstHit: hits[4], viaParent: false
  });
  assert.equal(agg.byKey["call:c_missing"], undefined);
});

test("window rendering emits data-line, au-ln-target, and the server's spans as marks safely", () => {
  const body = "Alpha one\nalpha TWO\nTarget_99 here <b>\nlast\n";
  const ev = { id: "ev1", kind: "tool_result", in: "", out: body, model_seen: true };
  const res = winHelpers.auRenderBodyContent(ev, "out", {
    search: "alpha",
    regex: false,
    caseSensitive: true,
    targetLine: 3,
    matches: { out: { spans: [{ line: 2, start: 0, end: 5 }] } }
  });
  assert.equal(res.matchCount, 1);
  assert.ok(res.html.includes('data-line="2"'));
  assert.ok(res.html.includes('class="au-line au-ln-target" data-line="3"'));
  assert.ok(!res.html.includes('<mark class="au-hit" data-au-hit="0">Alpha</mark>'));
  assert.ok(res.html.includes('<mark class="au-hit au-hit-cur" data-au-hit="0">alpha</mark>'));

  const rxRes = winHelpers.auRenderBodyContent(ev, "out", {
    search: "Target_[0-9]+",
    regex: true,
    caseSensitive: true,
    targetLine: 3,
    matches: { out: { spans: [{ line: 3, start: 0, end: 9 }, { line: 3, start: 15, end: 18 }] } }
  });
  assert.equal(rxRes.matchCount, 2);
  assert.ok(rxRes.html.includes('<mark class="au-hit au-hit-cur" data-au-hit="0">Target_99</mark>'));
  assert.ok(rxRes.html.includes('<mark class="au-hit" data-au-hit="1">&lt;b&gt;</mark>'), "span text is escaped");

  // The renderer runs no matcher: without the server's spans (pending, refused,
  // not asked for) a find text marks nothing, whatever the text would match.
  for (const search of ["alpha", "[unclosed", "^", "(?i:alpha)", "\\p{Greek}+"]) {
    for (const regex of [false, true]) {
      const none = winHelpers.auRenderBodyContent(ev, "out", { search, regex });
      assert.equal(none.matchCount, 0, search);
      assert.ok(!none.html.includes("<mark"), search);
    }
  }
  assert.equal(winHelpers.auBuildSearchMatcher, undefined);
  assert.equal(winHelpers.auTranslateRe2, undefined);
  // The spans' capped flag reaches the caller for its count.
  assert.equal(winHelpers.auRenderBodyContent(ev, "out", { matches: { out: { spans: [], capped: true } } }).matchCapped, true);
});

test("ensureLineLoaded pages truncated range until targetLine and obeys cancellation", async () => {
  const ev = {
    id: "ev-trunc",
    truncated: true,
    out: "l1\nl2\n",
    range: { out: { offset: 6, total: 24 } }
  };
  const calls = [];
  const res = await searchApi.ensureLineLoaded(ev, "out", 5, async (offset, len) => {
    calls.push({ offset, len });
    if (offset === 6) return { data: "l3\nl4\n", next: 12, end: false };
    if (offset === 12) return { data: "l5\nl6\n", next: 18, end: false };
    return { data: "l7\n", next: 24, end: true };
  }, { len: 6 });
  assert.equal(res.loadedPages, 2);
  assert.equal(res.reached, true);
  assert.equal(calls.length, 2);
  assert.equal(ev.range.out.offset, 18);
  assert.ok(ev.out.includes("l5\n"));

  // Cancellation stops before next fetch.
  const ev2 = { id: "ev-cancel", truncated: true, out: "a\n", range: { out: { offset: 2, total: 20 } } };
  let cancel = false;
  let count = 0;
  const resCancel = await searchApi.ensureLineLoaded(ev2, "out", 10, async () => {
    count++;
    cancel = true;
    return { data: "b\n", next: 4, end: false };
  }, { isCancelled: () => cancel });
  assert.equal(count, 1);
  assert.equal(resCancel.cancelled, true);
});

test("executeSearch handles 410 cursor restart, 400 invalid regex, 503 retryable error, empty state, and stale guard", async () => {
  // 1) 410 stale cursor on load-more restarts once from page 1 with notice.
  const urls = [];
  const env1 = makeMockFetchEnv({
    fetch: async (url) => {
      urls.push(url);
      if (url.includes("cursor=c1")) {
        return { status: 410, ok: false, text: async () => JSON.stringify({ error: "stale cursor" }) };
      }
      if (urls.length === 1) {
        return {
          status: 200, ok: true,
          text: async () => JSON.stringify({ hits: [{ event_id: "e1", kind: "tool_call", field: "in", line_no: 1, line: "hit1", call_id: "c1" }], next: "c1" })
        };
      }
      return {
        status: 200, ok: true,
        text: async () => JSON.stringify({ hits: [{ event_id: "e2", kind: "tool_result", field: "out", line_no: 2, line: "fresh", call_id: "c2" }], next: null })
      };
    }
  });
  env1.api.state().q = "hit";
  await env1.api.executeSearch({ append: false });
  assert.equal(env1.api.state().hits.length, 1);
  env1.api.state().q = "draft";
  await env1.api.executeSearch({ append: true, cursor: "c1" });
  assert.match(urls[2], /q=hit/);
  assert.doesNotMatch(urls[2], /cursor=/);
  assert.equal(env1.api.state().staleNotice, true);
  assert.equal(env1.api.state().hits[0].event_id, "e2");

  // 2) 400 invalid regex + 503 error + empty state + stale-response suppression.
  let resolveFirst;
  const env2 = makeMockFetchEnv({
    fetch: (url) => {
      if (url.includes("q=slow")) {
        return new Promise((r) => { resolveFirst = r; });
      }
      if (url.includes("q=bad%5B")) {
        return Promise.resolve({ status: 400, ok: false, text: async () => JSON.stringify({ error: "invalid regexp" }) });
      }
      if (url.includes("q=down")) {
        return Promise.resolve({ status: 503, ok: false, text: async () => "unavailable" });
      }
      return Promise.resolve({ status: 200, ok: true, text: async () => JSON.stringify({ hits: [], next: null }) });
    }
  });
  env2.api.state().q = "bad[";
  await env2.api.executeSearch({ append: false });
  assert.equal(env2.api.state().errorKind, "invalid");
  assert.equal(env2.api.state().errorMsg, "invalid regexp");

  env2.api.state().q = "down";
  await env2.api.executeSearch({ append: false });
  assert.equal(env2.api.state().errorKind, "unavail");
  assert.equal(env2.elements.retryBtn.hidden, false);

  env2.api.state().q = "slow";
  const slowPromise = env2.api.executeSearch({ append: false });
  env2.api.state().q = "empty";
  await env2.api.executeSearch({ append: false });
  resolveFirst({ status: 200, ok: true, text: async () => JSON.stringify({ hits: [{ event_id: "stale", kind: "other", field: "in", line_no: 1, line: "stale" }], next: null }) });
  await slowPromise;
  assert.equal(env2.api.state().hits.length, 0);
  assert.ok(env2.elements.statEl.textContent.includes("js.audit.csearch.empty"));
});

const response = (hits = [], next = null, status = 200) => ({ status, ok: status === 200, text: async () => JSON.stringify({ hits, next }) });
const hit = {event_id: "e", kind: "other", field: "in", line_no: 1, line: "hit"};
test("draft input and options abort and invalidate pending responses", async () => {
  for (const [el, event] of [["inp", "input"], ["rxBtn", "click"], ["csBtn", "click"]]) {
    let resolve, signal;
    const env = makeMockFetchEnv({fetch: (_, opts) => { signal = opts.signal; return new Promise(r => resolve = r); }});
    env.api.state().q = "old";
    const pending = env.api.executeSearch();
    assert.equal(env.elements.subBtn.disabled, true);
    env.elements.inp.value = "new";
    env.elements[el].listeners[event][0]();
    assert.equal(signal.aborted, true);
    assert.equal(env.elements.subBtn.disabled, false);
    resolve(response([hit])); await pending;
    assert.equal(env.api.state().hits.length, 0);
  }
});
test("append uses successful committed query and preserves hits for 400/503/network errors", async () => {
  const urls = []; let status = 200;
  const env = makeMockFetchEnv({fetch: async url => { urls.push(url); if (status === -1) throw Error("offline"); return response([hit], "cursor", status); }});
  Object.assign(env.api.state(), {q: "exact", regex: true, caseSensitive: true, kind: "hook"});
  await env.api.executeSearch();
  Object.assign(env.api.state(), {q: "draft", regex: false, caseSensitive: false, kind: ""});
  for (status of [400,503,-1]) {
    await env.api.executeSearch({append:true, cursor:"cursor"});
    assert.match(urls.at(-1), /q=exact&regex=1&case=1&kinds=hook&cursor=cursor/);
    assert.equal(env.api.state().hits.length, 1);
    assert.equal(env.api.state().errorKind, "unavail");
    assert.match(env.elements.statEl.textContent, /unavail/);
  }
});
test("an empty page that carries a cursor keeps Load more until the cursor is exhausted", async () => {
  const urls = [];
  const env = makeMockFetchEnv({fetch: async url => {
    urls.push(url);
    if (urls.length === 1) return response([], "c-budget");
    if (urls.length === 2) return response([], "c-budget-2");
    return response([hit], null);
  }});
  env.api.state().q = "old";
  await env.api.executeSearch();
  assert.equal(env.api.state().hits.length, 0);
  assert.equal(env.elements.footEl.hidden, false, "More stays reachable on a zero-hit page");
  assert.doesNotMatch(env.elements.statEl.textContent, /csearch\.empty/);
  assert.match(env.elements.statEl.textContent, /hits_more:0/);
  // A second zero-hit page still holds the cursor; the button drives the append.
  env.elements.moreBtn.listeners.click[0]();
  await flush();
  assert.match(urls[1], /cursor=c-budget$/);
  assert.equal(env.elements.footEl.hidden, false);
  env.elements.moreBtn.listeners.click[0]();
  await flush();
  assert.match(urls[2], /cursor=c-budget-2$/);
  assert.equal(env.api.state().hits.length, 1);
  assert.equal(env.api.state().next, null);
  assert.equal(env.elements.footEl.hidden, true);
  // Only an exhausted cursor with no hit reads as "No matches".
  const done = makeMockFetchEnv({fetch: async () => response([], null)});
  done.api.state().q = "none";
  await done.api.executeSearch();
  assert.match(done.elements.statEl.textContent, /csearch\.empty/);
  assert.equal(done.elements.footEl.hidden, true);
  // A failed continuation on an empty list retries the cursor, not a restart.
  let fail = true; const u2 = [];
  const retry = makeMockFetchEnv({fetch: async url => { u2.push(url); if (u2.length === 1) return response([], "cc"); return fail ? {status:503,ok:false,text:async()=>""} : response([hit], null); }});
  retry.api.state().q = "r";
  await retry.api.executeSearch();
  retry.elements.moreBtn.listeners.click[0]();
  await flush();
  assert.equal(retry.elements.retryBtn.hidden, false);
  fail = false;
  retry.elements.retryBtn.listeners.click[0]();
  await flush();
  assert.match(u2[2], /cursor=cc$/);
  assert.equal(retry.api.state().hits.length, 1);
});
test("stored truncation numbers concatenated text in four seam variants and draws the server's seam spans", () => {
  for (const [head,tail,target] of [["head","TAIL\nhit",2],["head\n","TAIL\nhit",3],["","TAIL\nhit",2],["hit","",1]]) {
    const ev = {in:head+tail,model_seen:true,stored_trunc:{in:{head_bytes:Buffer.byteLength(head),tail_bytes:Buffer.byteLength(tail),original_bytes:100}}};
    const res = winHelpers.auRenderBodyContent(ev,"in",{search:"hit",targetLine:target,matches:{in:{spans:[{line:target,start:0,end:3}]}}});
    assert.match(res.html,new RegExp('au-ln-target" data-line="'+target+'"'));
    assert.equal(res.matchCount,1);
    assert.equal((res.html.match(/au-trunc-row/g)||[]).length,1);
    if (!head) assert.ok(res.html.indexOf("au-trunc-row") < res.html.indexOf("au-line"));
  }
  // The server's spans on the line holding the seam: one line, a mark on each
  // side (a same-start alternative is a head span, never the crossing match).
  const seamEv = {in:"headTAIL",stored_trunc:{in:{head_bytes:4,tail_bytes:4,original_bytes:99}}};
  const both = winHelpers.auRenderBodyContent(seamEv,"in",{search:"x",matches:{in:{spans:[{line:1,start:0,end:4},{line:1,start:4,end:8}]}}});
  assert.equal(both.matchCount,2);
  assert.equal((both.html.match(/au-lnum">/g)||[]).length,1);
  assert.match(both.html,/data-au-hit="0">head<\/mark><mark[^>]*data-au-hit="1">TAIL<\/mark>/);
  const headOnly = winHelpers.auRenderBodyContent({in:"ab",stored_trunc:{in:{head_bytes:1,tail_bytes:1,original_bytes:99}}},"in",{search:"ab|a",regex:true,matches:{in:{spans:[{line:1,start:0,end:1}]}}});
  assert.equal(headOnly.matchCount,1);
  assert.match(headOnly.html,/data-au-hit="0">a<\/mark>b/);
});
test("inspector draws UTF-16 spans from the server: astral offsets, Unicode text, redaction chips", () => {
  const ev = {in:"😀a\nαβγ x\n😀\nkey=*** tail\n",redactions:[{field:"in",offset:Buffer.byteLength("😀a\nαβγ x\n😀\nkey="),rule:"secret",length:3}]};
  const render = (spans) => winHelpers.auRenderBodyContent(ev,"in",{search:"q",regex:true,matches:{in:{spans}}});
  // An empty-able Unicode class on an astral char followed by a letter: the
  // renderer only slices, so opening the inspector completes.
  const uni = render([{line:1,start:2,end:3},{line:2,start:0,end:3},{line:3,start:0,end:2}]);
  assert.equal(uni.matchCount,3);
  assert.match(uni.html,/<span class="au-ltxt">😀<mark[^>]*data-au-hit="0">a<\/mark><\/span>/);
  assert.match(uni.html,/<mark[^>]*data-au-hit="1">αβγ<\/mark> x/);
  assert.match(uni.html,/<mark[^>]*data-au-hit="2">😀<\/mark>/);
  // Next to a chip, and across it: drawn around the chip, never inside the mask,
  // one hit however many pieces.
  const beside = render([{line:4,start:0,end:3},{line:4,start:7,end:12}]);
  assert.equal(beside.matchCount,2);
  assert.match(beside.html,/data-au-hit="0">key<\/mark>=<span class="au-red"[^>]*>[^<]*<\/span><mark[^>]*data-au-hit="1"> tail<\/mark>/);
  const across = render([{line:4,start:2,end:9}]);
  assert.equal(across.matchCount,1);
  assert.match(across.html,/ke<mark[^>]*data-au-hit="0">y=<\/mark><span class="au-red"[^>]*>[^<]*<\/span><mark[^>]*data-au-hit="0"> t<\/mark>ail/);
  // A span only inside the mask draws nothing and is not counted.
  const inside = render([{line:4,start:0,end:3},{line:4,start:4,end:7}]);
  assert.equal(inside.matchCount,1);
  assert.equal((inside.html.match(/<mark/g)||[]).length,1);
  // Malformed spans are ignored, not thrown on.
  assert.equal(render([null,{line:1},{line:1,start:3,end:3},{line:99,start:0,end:2}]).matchCount,0);
  // The active hit follows the navigation index across pieces of one span.
  const cur = winHelpers.auRenderBodyContent(ev,"in",{search:"q",activeMatch:0,matches:{in:{spans:[{line:4,start:2,end:9}]}}});
  assert.equal((cur.html.match(/au-hit au-hit-cur/g)||[]).length,2);
});
test("CRLF line spans trim CR while byte offsets retain CR and UTF8", () => {
  const lines = winHelpers.auSplitLinesWithBytes("é\r\nfoo\r\n***",0);
  assert.equal(lines[1].startByte,4); assert.equal(lines[1].endByte,8); assert.equal(lines[2].startByte,9);
  assert.equal(lines[1].text,"foo");
  const res = winHelpers.auRenderBodyContent({in:"é\r\nfoo\r\n***",redactions:[{field:"in",offset:9,rule:"secret",length:3}]},"in",{search:"foo$",regex:true,matches:{in:{spans:[{line:2,start:0,end:3}]}}});
  assert.equal(res.matchCount,1); assert.match(res.html,/🔒 secret/); assert.doesNotMatch(res.html,/data-line="4"/);
});

// auSpansFor is the controller's side of the server's match spans: it asks the
// event route for the displayed envelope's spans under the find text.
function loadSpansController(fetcher) {
  const src = fs.readFileSync(path.join(__dirname,"assets/dashboard-audit.js"),"utf8");
  const timers = [];
  const sandbox = {fetch:fetcher, encodeURIComponent, JSON, String, Object, Promise,
    auSessionRid:()=>"sess-1", rendered:0, auRenderWindowBody(){ sandbox.rendered++; },
    setTimeout(fn,ms){ timers.push({fn,ms,live:true}); return timers.length; },
    clearTimeout(id){ if (timers[id-1]) timers[id-1].live = false; }};
  vm.runInNewContext(src.slice(src.indexOf("  var AU_SPANS_DEBOUNCE_MS"),src.indexOf("  function auRenderWindowBody("))+"\nglobalThis.api={spansFor:auSpansFor,cancel:auSpansCancel,url:auSpansUrl,key:auSpansKey};",sandbox);
  return {api:sandbox.api, timers, sandbox};
}
const flushPromises = () => new Promise((r) => setImmediate(r));
const spanWin = (over) => Object.assign({tab:"out",search:"foo",regex:true,caseSensitive:false,el:{isConnected:true}}, over);
const jsonResp = (status, obj) => ({ok:status<400,status,text:async()=>JSON.stringify(obj)});

test("inspector asks the server for spans: request shape, loaded prefix, only searchable tabs", async () => {
  const urls = [];
  const c = loadSpansController(async (u) => { urls.push(u); return jsonResp(200,{matches:{out:{spans:[{line:1,start:0,end:3}]}}}); });
  const ev = {id:"holder:1/s7#0",kind:"tool_result",out:"foo"};
  const win = spanWin();
  assert.deepEqual({...c.api.spansFor(win,ev)}, {parts:null,pending:true,error:""});
  assert.equal(urls.length,1);
  assert.equal(urls[0],"/s/sess-1/audit/event/"+encodeURIComponent(ev.id)+"?only=matches&q=foo&regex=1");
  await flushPromises();
  assert.equal(c.sandbox.rendered,1);
  const done = c.api.spansFor(win,ev);
  assert.equal(done.pending,false);
  assert.deepEqual(JSON.parse(JSON.stringify(done.parts)),{out:{spans:[{line:1,start:0,end:3}]}});
  c.api.spansFor(win,ev);
  assert.equal(urls.length,1,"an answered key is not asked again");
  // Case flag, escaping, and the loaded prefix of a body paged in by range follow-ups.
  const paged = {id:"e2",kind:"tool_result",out:"x",range:{out:{offset:4096,total:9999}},loadedBytes:{in:7}};
  const w2 = spanWin({search:"(?i:a&b)",regex:true,caseSensitive:true});
  c.api.spansFor(w2,paged);
  assert.equal(urls[1],"/s/sess-1/audit/event/e2?only=matches&q="+encodeURIComponent("(?i:a&b)")+"&regex=1&case=1&through_in=7&through_out=4096");
  // No find text, a non-searchable tab, and the normalized raw view of a mapped event ask nothing.
  const before = urls.length;
  for (const w of [spanWin({search:""}), spanWin({tab:"meta"}), spanWin({tab:"events"}), spanWin({tab:"raw"})]) {
    assert.deepEqual({...c.api.spansFor(w,{id:"e3",kind:"tool_call"})}, {parts:null,pending:false,error:""});
  }
  assert.equal(urls.length,before);
  c.api.spansFor(spanWin({tab:"raw"}),{id:"o1",kind:"other",raw:"{}"});
  assert.equal(urls.length,before+1,"an other event's stored raw body is searched");
});

test("inspector spans: find box requests are debounced and the latest request wins", async () => {
  const pending = [];
  const urls = [];
  const c = loadSpansController((u) => { urls.push(u); return new Promise((res) => pending.push({u,res})); });
  const ev = {id:"e1",kind:"tool_result",out:"foo bar"};
  const win = spanWin({regex:false,search:"f",spansDebounce:true});
  c.api.spansFor(win,ev);
  assert.equal(urls.length,0,"a typed query waits for the debounce");
  assert.equal(c.timers.length,1);
  win.search = "fo"; win.spansDebounce = true;
  c.api.spansFor(win,ev);
  assert.equal(c.timers.length,2);
  assert.equal(c.timers[0].live,false,"the earlier keystroke's timer is cancelled");
  c.timers[1].fn();
  assert.equal(urls.length,1);
  assert.match(urls[0],/q=fo$/);
  // Type again while the request is in flight: the old answer is dropped.
  win.search = "foo"; win.spansDebounce = true;
  c.api.spansFor(win,ev);
  c.timers[2].fn();
  assert.equal(urls.length,2);
  pending[1].res(jsonResp(200,{matches:{out:{spans:[{line:1,start:0,end:3}]}}}));
  await flushPromises();
  pending[0].res(jsonResp(200,{matches:{out:{spans:[{line:1,start:0,end:2}]}}}));
  await flushPromises();
  const got = c.api.spansFor(win,ev);
  assert.equal(got.pending,false);
  assert.equal(got.parts.out.spans[0].end,3,"the latest request's spans are kept");
  // A closed window's answer is dropped.
  const closed = spanWin({search:"x"});
  c.api.spansFor(closed,ev);
  closed.el.isConnected = false;
  pending[2].res(jsonResp(200,{matches:{}}));
  await flushPromises();
  assert.ok(!closed.spans);
});

test("inspector spans: the server's refusal shows its text with zero matches, once", async () => {
  const urls = [];
  const c = loadSpansController(async (u) => { urls.push(u); return jsonResp(400,{error:"q is not a valid regular expression: error parsing regexp: missing closing ): `(`"}); });
  const ev = {id:"e1",kind:"tool_result",out:"foo"};
  const win = spanWin({search:"("});
  c.api.spansFor(win,ev);
  await flushPromises();
  const got = c.api.spansFor(win,ev);
  assert.equal(got.parts,null);
  assert.equal(got.pending,false);
  assert.match(got.error,/^q is not a valid regular expression/);
  c.api.spansFor(win,ev);
  assert.equal(urls.length,1,"a refused query is not asked again until it changes");
  // A network failure is an error too, not a hang.
  const c2 = loadSpansController(async () => { throw new Error("offline"); });
  const w2 = spanWin();
  c2.api.spansFor(w2,ev);
  await flushPromises();
  assert.equal(c2.api.spansFor(w2,ev).error,"offline");
});

function installPaging(env, wins, fetcher) {
  const src = fs.readFileSync(path.join(__dirname,"assets/dashboard-audit.js"),"utf8");
  const sandbox = {fetch:fetcher, AbortController, WeakMap, Promise, encodeURIComponent, AU_RANGE_CHUNK:100,
    auSessionRid:()=>"sess-1", auRenderWindowBody:()=>{}, toast:()=>{}, wsT:k=>k};
  vm.runInNewContext(src.slice(src.indexOf("  function auDisplayedEnvelope("),src.indexOf("  function auCopyPlain("))+"\nglobalThis.api={displayedEnvelope:auDisplayedEnvelope,loadMorePart:auLoadMorePart};",sandbox);
  Object.assign(env.sandbox.window.WsAudit,sandbox.api,{windows:()=>wins,renderWindow:()=>{}});
  return sandbox.api;
}
const pageWin = () => ({reqToken:1,tab:"out",targetLine:3, search:"hit",regex:true,caseSensitive:true,
  el:{isConnected:true,querySelector:()=>null},activeEnvelope:{id:"call",in:"call"},
  resultEnvelope:{id:"result",out:"a\n",truncated:true,range:{out:{offset:2,total:100}}}});
const flush = async () => {for(let i=0;i<12;i++) await Promise.resolve();};
test("manual and automatic same-offset paging share one promise and append once on result envelope", async () => {
  const env=makeMockFetchEnv({fetch:async()=>response()}); const win=pageWin();
  let resolve,calls=0,url;
  const paging=installPaging(env,[win],u=>{calls++;url=u;return new Promise(r=>resolve=r);});
  const manual=paging.loadMorePart(win,"out",2);
  env.api.handleWindowRender(win);
  assert.equal(calls,1); assert.match(url,/event\/result/);
  assert.equal(paging.loadMorePart(win,"out",2),manual);
  resolve({ok:true,json:async()=>({data:"b\nhit\n",end:true})});
  assert.equal(await manual,true); await flush();
  assert.equal(win.resultEnvelope.out,"a\nb\nhit\n");
  assert.equal(win.activeEnvelope.in,"call");
  assert.equal(await paging.loadMorePart(win,"out",2),false);
});
test("clear cancels owned pending auto work and resets window immediately, but leaves manual owner alive", async () => {
  for(const manualFirst of [false,true]) {
    const env=makeMockFetchEnv({fetch:async()=>response()});const win=pageWin();
    let resolve,signal,renders=0;
    const paging=installPaging(env,[win],(_,opts)=>{signal=opts.signal;return new Promise(r=>resolve=r);});
    env.sandbox.window.WsAudit.renderWindow=()=>renders++;
    const pending=manualFirst ? paging.loadMorePart(win,"out",2) : null;
    env.api.handleWindowRender(win); env.api.clearSearch();
    assert.equal(signal.aborted,!manualFirst);
    assert.equal(win.search,"");assert.equal(win.regex,false);assert.equal(win.caseSensitive,false);assert.equal(win.targetLine,0);assert.equal(renders,1);
    resolve({ok:true,json:async()=>({data:"b\nhit\n",end:true})});
    if(pending) await pending; await flush();
    assert.equal(win.resultEnvelope.out,manualFirst?"a\nb\nhit\n":"a\n");
    assert.equal(win._auPagingOwner,null);
  }
});

test("pending page discards changed offset, replacement envelope, and changed displayed result",async()=>{
  for(const change of [w=>w.resultEnvelope.range.out.offset=4,w=>w.resultEnvelope={id:"new"},w=>w.tab="in"]) {
    const env=makeMockFetchEnv({fetch:async()=>response()});const win=pageWin();const ev=win.resultEnvelope;
    let resolve;
    const paging=installPaging(env,[win],()=>new Promise(r=>resolve=r));
    const owner={};const pending=paging.loadMorePart(win,"out",2,owner);change(win);
    resolve({ok:true,json:async()=>({data:"duplicate",end:true})});
    assert.equal(await pending,false); assert.equal(ev.out,"a\n");
  }
});

test("reused window starts hit B immediately and stale hit A cannot clear its owner", async () => {
  const env = makeMockFetchEnv({fetch:async()=>response()});
  const win = pageWin(), first = win.resultEnvelope, requests = [];
  installPaging(env, [win], url => new Promise(resolve => requests.push({url, resolve})));
  env.api.handleWindowRender(win);
  const ownerA = win._auPagingOwner;
  env.api.handleWindowRender(win);
  assert.equal(requests.length, 1, "same run is shared");
  win.reqToken++;
  win.resultEnvelope = {id:"result-B", out:"B\n", truncated:true, range:{out:{offset:2,total:100}}};
  env.api.handleWindowRender(win);
  const ownerB = win._auPagingOwner;
  assert.equal(requests.length, 2, "B starts before A resolves");
  assert.notEqual(ownerB, ownerA);
  assert.match(requests[1].url, /event\/result-B/);
  requests[0].resolve({ok:true,json:async()=>({data:"stale\n",end:true})});
  await flush();
  assert.equal(first.out, "a\n");
  assert.equal(win._auPagingOwner, ownerB, "late A completion preserves B ownership");
  requests[1].resolve({ok:true,json:async()=>({data:"second\nhit\n",end:true})});
  await flush();
  assert.equal(win.resultEnvelope.out, "B\nsecond\nhit\n");
  assert.equal(win._auPagingOwner, null);
});

test("same cached envelope supersedes an aborted paging lock without losing the new lock", async () => {
  const env = makeMockFetchEnv({fetch:async()=>response()});
  const win = pageWin(), ev = win.resultEnvelope, requests = [];
  const paging = installPaging(env, [win], (url, opts) => new Promise(resolve => requests.push({url, signal:opts.signal, resolve})));
  env.api.handleWindowRender(win);
  win.reqToken++;
  win.targetLine = 4;
  env.api.handleWindowRender(win);
  const ownerB = win._auPagingOwner, lockB = win._morePending.get(ev).out;
  assert.equal(requests.length, 2, "same-envelope B fetch starts immediately");
  assert.equal(requests[0].signal.aborted, true);
  assert.equal(requests[1].signal.aborted, false);
  requests[0].resolve({ok:true,json:async()=>({data:"stale\n",end:true})});
  await flush();
  assert.equal(ev.out, "a\n");
  assert.equal(win._auPagingOwner, ownerB);
  assert.equal(win._morePending.get(ev).out, lockB, "late A finally preserves B lock");
  const manual = paging.loadMorePart(win, "out", 2);
  assert.equal(requests.length, 2, "live manual and automatic work still share");
  requests[1].resolve({ok:true,json:async()=>({data:"second\nthird\nhit\n",end:true})});
  assert.equal(await manual, true);
  await flush();
  assert.equal(ev.out, "a\nsecond\nthird\nhit\n");
  assert.equal(win._auPagingOwner, null);
  assert.equal(win._morePending.get(ev).out, undefined);
});

// The find counter of the window body: the server's spans reach it from the
// tab that was rendered, and a capped answer shows the count as N+ on every
// searchable tab, the stored Raw tab of an `other` event included.
function renderCounter(win, parts) {
  const src = fs.readFileSync(path.join(__dirname, "assets", "dashboard-audit-window.js"), "utf8");
  const ctrl = fs.readFileSync(path.join(__dirname, "assets", "dashboard-audit.js"), "utf8");
  const winSnippet = src.slice(src.indexOf("  function auEsc(s) {"), src.indexOf("  function auBuildWindowDOM("));
  const ctrlSnippet = ctrl.slice(ctrl.indexOf("  function auRenderWindowBody("), ctrl.indexOf("  function auIsResultSummary("));
  const sandbox = {
    wsT(k, a) { return a == null ? k : `${k}:${a}`; }, wsTn(k, n) { return `${k}:${n}`; }, renderMd(s) { return String(s); },
    JSON, Math, Date, Object, Array, String, Number, RegExp, Error,
    auSyncWindowHeader() {}, auDisplayedEnvelope: (w) => w.activeEnvelope, auSpansFor: () => ({ parts, pending: false, error: "" }),
    auHooks: {},
  };
  vm.runInNewContext(winSnippet + "\n" + ctrlSnippet + "\nglobalThis.render = auRenderWindowBody;", sandbox);
  const scnt = { textContent: "", classList: { toggle() {} }, setAttribute() {}, removeAttribute() {} };
  const panel = { innerHTML: "", querySelector: () => null };
  win.el = { querySelector: (sel) => ({ ".au-panel": panel, ".au-scnt": scnt }[sel] || null) };
  sandbox.render(win);
  return scnt.textContent;
}

test("a capped answer shows the count as N+ on the stored Raw tab of an other event", () => {
  const spans = (n) => Array.from({ length: n }, (_, i) => ({ line: 1, start: i, end: i + 1 }));
  const other = { id: "o1", kind: "other", harness: "pi", raw: "a".repeat(10001) };
  const win = (tab, ev) => ({ tab, search: "a", regex: true, caseSensitive: true, activeMatch: 0, activeEnvelope: ev });
  assert.equal(renderCounter(win("raw", other), { raw: { spans: spans(10000), capped: true } }), "1/10000+");
  assert.equal(renderCounter(win("raw", other), { raw: { spans: spans(3), capped: false } }), "1/3");
  // The other tabs keep their form.
  const call = { id: "c1", in: "a".repeat(10001), out: "a" };
  assert.equal(renderCounter(win("in", call), { in: { spans: spans(10000), capped: true } }), "1/10000+");
});
