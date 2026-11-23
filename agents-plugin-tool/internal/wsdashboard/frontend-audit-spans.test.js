"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { test } = require("node:test");
const vm = require("node:vm");


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

test("window rendering emits data-line and the server's spans as marks safely", () => {
  const body = "Alpha one\nalpha TWO\nTarget_99 here <b>\nlast\n";
  const ev = { id: "ev1", kind: "tool_result", in: "", out: body, model_seen: true };
  const res = winHelpers.auRenderBodyContent(ev, "out", {
    search: "alpha",
    regex: false,
    caseSensitive: true,
    matches: { out: { spans: [{ line: 2, start: 0, end: 5 }] } }
  });
  assert.equal(res.matchCount, 1);
  assert.ok(res.html.includes('data-line="2"'));
  assert.ok(res.html.includes('data-line="3"'));
  assert.ok(!res.html.includes('<mark class="au-hit" data-au-hit="0">Alpha</mark>'));
  assert.ok(res.html.includes('<mark class="au-hit au-hit-cur" data-au-hit="0">alpha</mark>'));

  const rxRes = winHelpers.auRenderBodyContent(ev, "out", {
    search: "Target_[0-9]+",
    regex: true,
    caseSensitive: true,
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

test("stored truncation numbers concatenated text in four seam variants and draws the server's seam spans", () => {
  for (const [head,tail,target] of [["head","TAIL\nhit",2],["head\n","TAIL\nhit",3],["","TAIL\nhit",2],["hit","",1]]) {
    const ev = {in:head+tail,model_seen:true,stored_trunc:{in:{head_bytes:Buffer.byteLength(head),tail_bytes:Buffer.byteLength(tail),original_bytes:100}}};
    const res = winHelpers.auRenderBodyContent(ev,"in",{search:"hit",matches:{in:{spans:[{line:target,start:0,end:3}]}}});
    assert.match(res.html,new RegExp('data-line="'+target+'"'));
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
