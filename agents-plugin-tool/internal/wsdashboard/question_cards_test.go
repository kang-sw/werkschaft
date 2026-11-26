package wsdashboard

import "testing"

// TestChatStreamQuestionCardsUnit exercises the chat-stream question capsule
// hydration and status-resolution functions from dashboard.js under Node so
// default `go test ./internal/wsdashboard/...` always verifies precedence,
// virtualization lookup, memo flattening, hold delegation, and fallback.
func TestChatStreamQuestionCardsUnit(t *testing.T) {
	src := whiteboardBridgeFunctions(t, string(mustAsset(t, "dashboard.js")),
		"wbUpdateNormID",
		"wbUpdateItemID",
		"wbUpdateFlattenMemo",
		"wbUpdateIsHeld",
		"wbUpdateClassFromEl",
		"wbUpdateTitleFromEl",
		"wbUpdateSettledSummary",
		"wbUpdateQueryBoard",
		"readBoardQuestionState",
		"hydrateWbUpdateChip",
		"hydrateWbUpdateGroup",
		"hydrateWbUpdateItems",
		"syncWbUpdateItems",
	)
	runNode(t, "chat-stream question cards unit", `
var bad = [];
function check(label, got, want) {
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    bad.push(label + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want));
  }
}
var WB_QCAP_CLASSES = {
  policy: "js.wbupdate.cls.policy",
  technical: "js.wbupdate.cls.technical",
  announced: "js.wbupdate.cls.announced",
  check: "js.wbupdate.cls.check"
};
function wsT(k, a) { return a === undefined ? k : k + ":" + a; }
var storageReads = 0;
var localStorage = {
  getItem: function () { storageReads++; return null; },
  setItem: function () { storageReads++; }
};
var heldSet = new Set();
var window = {
  wbPill: {
    isHeld: function (el) { return heldSet.has(el); }
  }
};

function makeEl(tag, opts) {
  opts = opts || {};
  var attrs = Object.assign({}, opts.attrs || {});
  var classes = new Set((opts.className || "").split(/\s+/).filter(Boolean));
  var el = {
    nodeType: 1,
    tagName: tag.toUpperCase(),
    attrs: attrs,
    children: [],
    parentNode: null,
    textContent: opts.text || "",
    value: opts.value || "",
    checked: !!opts.checked,
    get className() { return Array.from(classes).join(" "); },
    set className(v) { classes = new Set(String(v || "").split(/\s+/).filter(Boolean)); },
    classList: {
      contains: function (c) { return classes.has(c); },
      add: function (c) { classes.add(c); },
      remove: function (c) { classes.delete(c); },
      toggle: function (c, on) { if (on) classes.add(c); else classes.delete(c); return classes.has(c); }
    },
    getAttribute: function (k) { return Object.prototype.hasOwnProperty.call(attrs, k) ? attrs[k] : null; },
    setAttribute: function (k, v) { attrs[k] = String(v); },
    removeAttribute: function (k) { delete attrs[k]; },
    hasAttribute: function (k) { return Object.prototype.hasOwnProperty.call(attrs, k); },
    appendChild: function (c) {
      if (c.parentNode) c.parentNode.removeChild(c);
      c.parentNode = el;
      el.children.push(c);
      return c;
    },
    insertBefore: function (c, ref) {
      if (c.parentNode) c.parentNode.removeChild(c);
      c.parentNode = el;
      var i = ref ? el.children.indexOf(ref) : -1;
      if (i < 0) el.children.push(c); else el.children.splice(i, 0, c);
      return c;
    },
    removeChild: function (c) {
      var i = el.children.indexOf(c);
      if (i >= 0) { el.children.splice(i, 1); c.parentNode = null; }
      return c;
    },
    matches: function (sel) { return matchSelector(el, sel); },
    closest: function (sel) {
      for (var n = el; n; n = n.parentNode) if (matchSelector(n, sel)) return n;
      return null;
    },
    querySelector: function (sel) { return queryAll(el, sel)[0] || null; },
    querySelectorAll: function (sel) { return queryAll(el, sel); }
  };
  (opts.children || []).forEach(function (c) { el.appendChild(c); });
  return el;
}

function matchSingle(el, part) {
  part = part.trim();
  if (!part) return false;
  var m;
  var notMatch = part.match(/:not\(\[([^\]]+)\]\)/);
  if (notMatch) {
    if (el.hasAttribute(notMatch[1])) return false;
    part = part.replace(notMatch[0], "");
  }
  var tagMatch = part.match(/^[a-zA-Z]+/);
  if (tagMatch) {
    if (el.tagName !== tagMatch[0].toUpperCase()) return false;
    part = part.slice(tagMatch[0].length);
  }
  var attrRe = /\[([a-zA-Z0-9_-]+)(?:="([^"]*)")?\]/g;
  while ((m = attrRe.exec(part))) {
    if (!el.hasAttribute(m[1])) return false;
    if (m[2] !== undefined && el.getAttribute(m[1]) !== m[2]) return false;
  }
  part = part.replace(/\[[^\]]+\]/g, "");
  var clsRe = /\.([a-zA-Z0-9_-]+)/g;
  while ((m = clsRe.exec(part))) {
    if (!el.classList.contains(m[1])) return false;
  }
  return true;
}

function matchCompound(el, sel) {
  var parts = sel.trim().split(/\s*>\s*|\s+/).filter(Boolean);
  if (!parts.length || !matchSingle(el, parts[parts.length - 1])) return false;
  var cur = el.parentNode;
  for (var i = parts.length - 2; i >= 0; i--) {
    var ok = false;
    while (cur) {
      if (matchSingle(cur, parts[i])) { ok = true; cur = cur.parentNode; break; }
      cur = cur.parentNode;
    }
    if (!ok) return false;
  }
  return true;
}

function matchSelector(el, sel) {
  return sel.split(",").some(function (s) { return matchCompound(el, s); });
}

function queryAll(root, sel) {
  var out = [];
  (function walk(n) {
    for (var i = 0; i < n.children.length; i++) {
      var c = n.children[i];
      if (matchSelector(c, sel)) out.push(c);
      walk(c);
    }
  })(root);
  return out;
}

var board = makeEl("div", { attrs: { id: "r-board" } });
var entries = makeEl("div", { attrs: { id: "r-entries" } });
var virtualStore = new Map();
var doc = {
  createElement: function (tag) { return makeEl(tag); },
  querySelectorAll: function (sel) { return entries.querySelectorAll(sel); }
};
function region(name) { return name === "board" ? board : null; }
function entriesBox() { return entries; }
function q(sel, root) { return (root || entries).querySelectorAll(sel); }
` + src + `

// 1. Memo flattening strips <mailbox> and <textref>, collapses whitespace, truncates at 60 chars.
check("flatten strips mailbox and textref",
  wbUpdateFlattenMemo('see <mailbox to="lead">alice</mailbox> and <textref id="1" bytes="12">snippet</textref>\n  now'),
  "see alice and snippet now");
var longMemo = "a".repeat(80);
check("flatten truncates to 60", wbUpdateFlattenMemo(longMemo).length, 60);

// 2. Open policy item + historical commented row (commented-then-reasked):
//    openEl MUST win over historical wb-hitem.commented[data-state="settled"].
var histCommented = makeEl("div", {
  className: "wb-hitem commented",
  attrs: { "data-item": "1", "data-class": "policy", "data-state": "settled" },
  children: [
    makeEl("div", { className: "wb-hmain", children: [makeEl("b", { text: "Reasked policy" })] }),
    makeEl("div", { className: "wb-hstatus", text: "Commented" }),
    makeEl("div", { className: "cmquote", text: "earlier note" })
  ]
});
var policyNote = makeEl("textarea", { className: "wsc-backing", value: "" });
var policyCard = makeEl("div", {
  className: "card policy",
  attrs: { "data-id": "01" },
  children: [
    makeEl("div", { className: "head", children: [makeEl("span", { className: "title", text: "Reasked policy" })] }),
    makeEl("button", { className: "opt", attrs: { "data-opt": "a", "aria-pressed": "false" } }),
    policyNote
  ]
});
board.appendChild(histCommented);
board.appendChild(policyCard);

var st1 = readBoardQuestionState("1");
check("commented-then-reasked stays open, not settled", [st1.state, st1.cls, st1.text], ["open", "policy", "js.wbupdate.unanswered"]);

// 3. Hold via window.wbPill.isHeld (zero localStorage access).
heldSet.add(policyCard);
st1 = readBoardQuestionState("1");
check("held state via pill.js", [st1.state, st1.clsName, st1.text, storageReads], ["held", "st-held", "js.pill.held", 0]);

// 4. Memo-only staged state (.wsc-backing) beats hold; clearing returns to held.
policyNote.value = "<b>keep</b> <textref id=\"2\">ctx</textref>";
st1 = readBoardQuestionState("1");
check("memo-only staged beats hold", [st1.state, st1.clsName, st1.text], ["staged", "st-staged", "js.wbupdate.staged_memo:<b>keep</b> ctx"]);
policyNote.value = "";
heldSet.delete(policyCard);
st1 = readBoardQuestionState("1");
check("clearing memo and hold returns to open", [st1.state, st1.text], ["open", "js.wbupdate.unanswered"]);

// 5. Sent precedence: sent-sent and sent-held beat staged and held.
policyNote.value = "draft";
policyCard.classList.add("sent-sent");
check("sent-sent beats staged", readBoardQuestionState("1").text, "js.wbupdate.sent_waiting");
policyCard.classList.add("sent-held");
check("sent-held beats sent-sent", readBoardQuestionState("1").text, "js.wbupdate.sent_held");

// 6. Settled item with stale sent-held class (no openEl) reports settled (Decision 9).
var settledSuperseded = makeEl("div", {
  className: "wb-hitem superseded sent-held",
  attrs: { "data-item": "2", "data-class": "technical", "data-state": "settled" },
  children: [
    makeEl("div", { className: "wb-hmain", children: [makeEl("b", { text: "Superseded tech" })] }),
    makeEl("div", { className: "wb-hstatus", text: "Superseded" })
  ]
});
board.appendChild(settledSuperseded);
var st2 = readBoardQuestionState("2");
check("settled beats stale sent-held", [st2.state, st2.cls, st2.text], ["settled", "technical", "✓ Superseded"]);

// 7. Virtualized board topic: settled row moved into virtualStore.children or rec.stashed
//    stays hydrated instead of reverting to bare (ID).
var virtSettled = makeEl("div", {
  className: "wb-hitem answered",
  attrs: { "data-item": "3", "data-class": "announced", "data-state": "settled" },
  children: [
    makeEl("div", { className: "wb-hmain", children: [makeEl("b", { text: "Virtualized notice" })] }),
    makeEl("div", { className: "wb-hstatus", text: "Acknowledged" })
  ]
});
virtualStore.set("topic-virt-children", { children: [makeEl("div", { className: "wb-history", children: [virtSettled] })], stashed: null });
var st3 = readBoardQuestionState("3");
check("virtualStore.children keeps settled capsule hydrated", [st3.state, st3.cls, st3.title, st3.text], ["settled", "announced", "Virtualized notice", "✓ Acknowledged"]);

var stashedSettled = makeEl("div", {
  className: "wb-hitem assumed",
  attrs: { "data-item": "4", "data-class": "check", "data-state": "settled" },
  children: [
    makeEl("div", { className: "wb-hmain", children: [makeEl("b", { text: "Stashed check" })] }),
    makeEl("div", { className: "wb-hstatus", text: "Assumed ok" })
  ]
});
virtualStore.set("topic-virt-stashed", { children: null, stashed: makeEl("section", { children: [stashedSettled] }) });
var st4 = readBoardQuestionState("4");
check("virtualStore.stashed keeps settled capsule hydrated", [st4.state, st4.cls, st4.title, st4.text], ["settled", "check", "Stashed check", "✓ Assumed ok"]);

// 8. Hydration + fallback revert when item disappears from board and virtualStore.
var chip3 = makeEl("a", { className: "wbupdate-item-id", attrs: { "data-wb-jump": "item-3" }, text: "(3)" });
var grp = makeEl("div", { className: "wbupdate-items", children: [chip3] });
entries.appendChild(grp);
hydrateWbUpdateItems(entries);
check("chip hydrated in group", [grp.classList.contains("wbupdate-hydrated"), chip3.classList.contains("wbupdate-cap"), grp.querySelector(".wbupdate-ans").textContent], [true, true, "✓ Acknowledged"]);
virtualStore.delete("topic-virt-children");
syncWbUpdateItems(["3"]);
check("missing item reverts to bare (ID)", [grp.classList.contains("wbupdate-hydrated"), chip3.className, chip3.textContent], [false, "wbupdate-item-id", "(3)"]);

process.stdout.write(bad.join("\n"));
`)
}
