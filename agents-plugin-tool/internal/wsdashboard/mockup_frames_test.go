package wsdashboard

import "testing"

// TestMockupFrameMountUnmountReuseAndSearch pins the frontend lifecycle for
// mockup snapshot panels:
//   - mountFrame sets src (not srcdoc) and omits wbFirst;
//   - unmountFrame restores a div carrying data-mockup-src, data-mockup-sha,
//     and data-sandbox without leaving src/srcdoc/sandbox attributes behind;
//   - morphBoard/frameKeep reuses a mounted mockup iframe across updates when
//     (data-doc-key, data-mockup-sha) match, and replaces it when sha changes;
//   - paletteBodyText returns data-fallback-text for mockup frames.
func TestMockupFrameMountUnmountReuseAndSearch(t *testing.T) {
	src := dashboardFuncs(t, "boardBands", "frameNear", "frameShown", "mountFrame", "mountFrames", "unmountFrame", "unmountFrames", "openKey", "restoreOpenOne", "restoreOpen", "forgetOpen", "topicKey", "topicSeen", "holdEcho", "morphBoard", "frameKeep", "frameKept", "frameChain", "frameGraftable", "frameGrafts", "settleMoves", "frameGraft", "frameNext", "paletteText", "paletteBodyText")
	runNode(t, "mockup frame lifecycle", frameDOM+`
function isFrame(n, kind) { return n.classList.contains("doc-frame") && (!kind || n.tagName === kind); }
function entry(n) { return /^entry:/.test(n.getAttribute("data-doc-key") || ""); }
SEL[".doc-frame:not(.doc-frame-next)"] = function (n) { return isFrame(n) && !n.classList.contains("doc-frame-next"); };
SEL[".doc-frame"] = function (n) { return isFrame(n); };
SEL["div.doc-frame[data-srcdoc], div.doc-frame[data-mockup-src]"] = function (n) { return isFrame(n, "DIV") && (n.hasAttribute("data-srcdoc") || n.hasAttribute("data-mockup-src")); };
SEL["iframe.doc-frame[data-srcdoc], iframe.doc-frame[data-mockup-src]"] = function (n) { return isFrame(n, "IFRAME") && (n.hasAttribute("data-srcdoc") || n.hasAttribute("data-mockup-src")); };
SEL['iframe.doc-frame[data-doc-key^="entry:"]:not(.doc-frame-next)'] = function (n) { return isFrame(n, "IFRAME") && entry(n) && !n.classList.contains("doc-frame-next"); };
SEL['div.doc-frame[data-doc-key^="entry:"]'] = function (n) { return isFrame(n, "DIV") && entry(n); };
function frames(n) { return (n.tagName === "IFRAME" ? [n] : []).concat(walk(n, []).filter(function (c) { return c.tagName === "IFRAME"; })); }
var rawRemove = El.prototype.removeChild, rawInsert = El.prototype.insertBefore;
El.prototype.removeChild = function (c) { if (c.isConnected) frames(c).forEach(function (f) { f.dropped = true; }); return rawRemove.call(this, c); };
El.prototype.insertBefore = function (c, ref) {
  if (c.isConnected) frames(c).forEach(function (f) { f.dropped = true; });
  rawInsert.call(this, c, ref);
  if (c.isConnected) frames(c).forEach(function (f) { if (f.dropped) f.reloads = (f.reloads || 0) + 1; f.dropped = false; });
  return c;
};
El.prototype.moveBefore = function (c, ref) {
  if (!c.isConnected || !this.isConnected) throw new Error("moveBefore needs connected nodes");
  rawRemove.call(c.parentNode, c);
  rawInsert.call(this, c, ref);
};
El.prototype.querySelector = function (sel) { return q(sel, this)[0] || null; };
var body = new El("body");
doc.appendChild(body);
doc.body = body;
var openState = {}, boardSeen = {}, frameRevealMs = 1500, frameSizes = new Map(), mpend = null;
function frameShow() {}
function frameReveal() {}
var window = { WsCards: {
  weave: function (w) { return { pins: [], board: w }; },
  docked: function () { return []; }, isDocked: function () { return false; }, wrap: function () {},
  settle: function (el, p) {
    el.childNodes.slice().forEach(function (n) { if (p.board.indexOf(n) < 0) el.removeChild(n); });
    p.board.forEach(function (n) { if (n.parentNode === el && el.moveBefore) el.moveBefore(n, null); else el.appendChild(n); });
  }
} };
function frag(nodes) { return { childNodes: nodes }; }
function boardNodes(input) { return frag(input).childNodes; }
`+src+`
function mockHolder(key, sha, srcUrl, fallback) {
  return new El("div", {
    "class": "doc-frame mockup-frame",
    "data-doc": key,
    "data-doc-key": key,
    "data-sandbox": SANDBOX,
    "data-mockup-src": srcUrl,
    "data-mockup-sha": sha,
    "data-fallback-text": fallback
  });
}
function run(f) {
  var wrap = new El("div", { "class": "mockup-wrap" }, [f]);
  var eb = new El("div", { "class": "entry-body" }, [wrap]);
  return new El("div", { "class": "timeline-row", "data-row": f.getAttribute("data-doc-key") }, [new El("article", {}, [new El("details", { open: "" }, [eb])])]);
}
function topic(th, runs) {
  return new El("section", { "class": "topic", "data-topic": "Design", "data-th": th }, [new El("div", { "class": "topic-stream" }, runs.map(run))]);
}

var board = new El("div", { id: "r-board" });
body.appendChild(board);
var p1 = mockHolder("entry:e5", "sha1", "/s/rid/board/mockup?entry=e5", "Interactive HTML mockup   from demo/index.html.");
var p2 = mockHolder("entry:e6", "sha2", "/s/rid/board/mockup?entry=e6", "Second mockup fallback.");
var sec = topic("h1", [p1, p2]);
board.appendChild(sec);
boardSeen = { "t:Design": "h1" };

// Search palette reads fallback text before mounting.
var eb1 = p1.parentNode.parentNode;
check("paletteBodyText reads fallback text from unmounted mockup placeholder", paletteBodyText(eb1) === "Interactive HTML mockup from demo/index.html.");

// mountFrames mounts mockup placeholders with src (no srcdoc, no wbFirst).
mountFrames(sec);
var mountedIframes = walk(sec, []).filter(function (n) { return n.tagName === "IFRAME"; });
check("both mockup placeholders mounted", mountedIframes.length === 2);
var f1 = mountedIframes[0], f2 = mountedIframes[1];
check("mockup iframe sets src, not srcdoc", f1.getAttribute("src") === "/s/rid/board/mockup?entry=e5" && !f1.hasAttribute("srcdoc"));
check("mockup iframe copies sandbox without allow-same-origin", f1.getAttribute("sandbox") === SANDBOX && !f1.hasAttribute("data-sandbox"));
check("mockup iframe skips wbFirst", !f1.wbFirst);
check("paletteBodyText still reads fallback text after mounting", paletteBodyText(f1.parentNode.parentNode) === "Interactive HTML mockup from demo/index.html.");

// morphBoard preserves f1 (same sha) and replaces f2 (changed sha).
var nextP1 = mockHolder("entry:e5", "sha1", "/s/rid/board/mockup?entry=e5", "Interactive HTML mockup from demo/index.html.");
var nextP2 = mockHolder("entry:e6", "sha2-new", "/s/rid/board/mockup?entry=e6", "Second mockup fallback v2.");
morphBoard(board, [topic("h2", [nextP1, nextP2])]);
function inBoard(n) { for (; n; n = n.parentNode) if (n === board) return true; return false; }
check("unchanged mockup iframe is preserved with zero reloads", inBoard(f1) && !f1.reloads && !nextP1.parentNode);
check("changed mockup sha replaces iframe with placeholder", !inBoard(f2) && inBoard(nextP2) && nextP2.tagName === "DIV");

// unmountFrames restores placeholder cleanly without src/sandbox attributes.
unmountFrames(board);
var restored = walk(board, []).filter(function (n) { return n.tagName === "DIV" && n.getAttribute("data-doc-key") === "entry:e5"; })[0];
check("unmountFrames restores mockup div with data-mockup-src and data-sandbox", restored && restored.getAttribute("data-mockup-src") === "/s/rid/board/mockup?entry=e5" && restored.getAttribute("data-mockup-sha") === "sha1" && restored.getAttribute("data-sandbox") === SANDBOX);
check("unmountFrames strips src and sandbox attributes", !restored.hasAttribute("src") && !restored.hasAttribute("sandbox") && !restored.hasAttribute("srcdoc"));
`)
}
