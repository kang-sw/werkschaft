package wsdashboard

import (
	"encoding/json"
	"testing"
)

func TestBoardStateFilter(t *testing.T) {
	cards, _ := json.Marshal(mustAsset(t, "cards.js"))
	runNode(t, "board state filter", "var CARDS = "+string(cards)+";\n"+cardsDOM+`
var store = {}, A, B, C, D, R, legacy, closed, tail;
var filterOrder = ["all", "asks", "researching", "discussing", "waiting", "implementing", "awaiting-confirmation", "closed", "cards", "tickets"];
var inlineOrder = ["all", "asks", "closed", "cards", "tickets", "researching", "discussing", "waiting", "implementing", "awaiting-confirmation"];
var metaOrder = ["all", "asks", "closed", "cards", "tickets"];
var pipelineOrder = ["researching", "discussing", "waiting", "implementing", "awaiting-confirmation"];
var filterLabels = ["all", "researching", "discuss", "wait", "implement", "confirm", "closed", "cards", "tickets"].map(function (key) { return "js.board.filter." + key; });
var inlineLabels = ["all", "closed", "cards", "tickets", "researching", "discuss", "wait", "implement", "confirm"].map(function (key) { return "js.board.filter." + key; });
function stateTopic(name, state, isClosed) {
  var t = topic(name, 100, isClosed);
  if (state) t.setAttribute("data-state", state);
  return t;
}
function served() {
  A = stateTopic("a", "awaiting-confirmation"); B = stateTopic("b", "discussing");
  C = stateTopic("c", "implementing"); D = stateTopic("d", "waiting"); R = stateTopic("r", "researching");
  legacy = stateTopic("legacy"); closed = stateTopic("old", null, true); tail = more(1, 7);
  // Topic-internal panels are not framework cards.
  R.appendChild(mk("article.wb-entry", { "data-panel": "panel-1" }));
  return [A, B, C, D, R, legacy, closed, tail];
}
boot(store, served, { wide: true });
function radio(value, pop) { return page.W.toolbar().querySelector((pop ? ".board-state-pop" : ".board-state-inline") + ' [data-filter="' + value + '"]'); }
check("Row 1 meta filters order", page.W.toolbar().querySelectorAll('.board-state-meta [role="radio"]').map(function (n) { return n.getAttribute("data-filter"); }), metaOrder);
check("Row 2 pipeline filters order", page.W.toolbar().querySelectorAll('.board-state-pipeline [role="radio"]').map(function (n) { return n.getAttribute("data-filter"); }), pipelineOrder);
check("inline asks uses short label key", radio("asks").querySelector(".board-state-label").textContent, "js.board.filter.asks.short");
[".board-state-inline", ".board-state-pop"].forEach(function (group) {
  var isPop = group === ".board-state-pop";
  var radios = page.W.toolbar().querySelectorAll(group + ' [role="radio"]');
  check(group + " exact lifecycle order and label keys", radios.map(function (n) { return n.getAttribute("data-filter"); }), isPop ? filterOrder : inlineOrder);
  check(group + " existing labels", radios.filter(function(n) { return n.getAttribute("data-filter") !== "asks"; }).map(function(n) { return n.querySelector(".board-state-label").textContent; }), isPop ? filterLabels : inlineLabels);
  check(group + " uses app-window sprite for cards", radio("cards", isPop).querySelector(".board-state-icon").html, '<svg class="li" aria-hidden="true"><use href="#i-app-window"/></svg>');
  check(group + " uses existing ticket sprite", radio("tickets", isPop).querySelector(".board-state-icon").html, '<svg class="li" aria-hidden="true"><use href="#i-ticket"/></svg>');
});
function counts() { return filterOrder.filter(function (v) { return v !== "asks"; }).map(function (v) { return radio(v).querySelector(".board-state-count").textContent; }); }
function choose(value) { radio(value).fire("click", {}); }
function shown() { return [A, B, C, D, R, legacy, closed].filter(function (n) { return !n.hasAttribute("data-state-filtered"); }).map(function (n) { return n.getAttribute("data-topic"); }); }
check("all includes legacy and loaded closed; count includes unloaded closed", [shown(), counts()], [["a", "b", "c", "d", "r", "legacy", "old"], ["14", "1", "1", "1", "1", "1", "8", "0", "0"]]);
var roots = ["preview", "terminal", "web", "explorer"].map(function(kind, i) {
  page.W.register(kind, { open: function(c) { c.body.appendChild(mk("iframe", { src: "about:blank" })); } });
  var c = page.W.open(kind, { path: "same-path" });
  if (i % 2) c.pin(true);
  return c;
});
var frames = roots.map(function(c) { return c.body.firstChild; });
function cardVisibility() { return roots.map(function(c) { return !c.el.hasAttribute("data-state-filtered"); }); }
check("framework roots counted across board and pins, not by path", counts(), ["14", "1", "1", "1", "1", "1", "8", "4", "0"]);
["awaiting-confirmation", "discussing", "implementing", "waiting", "researching"].forEach(function (s, i) {
  choose(s);
  check("only authoritative " + s, shown(), [["a"], ["b"], ["c"], ["d"], ["r"]][i]);
  check("state filters hide framework roots", cardVisibility(), [false, false, false, false]);
  check("operational filter hides closed paging", tail.hasAttribute("data-state-filtered"), true);
  check("filter counts do not depend on visibility", counts(), ["14", "1", "1", "1", "1", "1", "8", "4", "0"]);
  check("radio groups share selection", [radio(s).getAttribute("aria-checked"), radio(s, true).getAttribute("aria-checked")], ["true", "true"]);
});
choose("closed"); check("closed lifecycle hides cards", [shown(), tail.hasAttribute("data-state-filtered"), cardVisibility()], [["old"], false, [false, false, false, false]]);
choose("cards");
check("Cards shows roots only, no topics or paging", [shown(), cardVisibility(), tail.hasAttribute("data-state-filtered")], [[], [true, true, true, true], true]);
check("compact trigger uses app-window too", page.W.toolbar().querySelector(".board-state-trigger .board-state-icon").html, '<svg class="li" aria-hidden="true"><use href="#i-app-window"/></svg>');
check("data-count and data-zero reflect live counts", [radio("cards").getAttribute("data-count"), radio("cards").hasAttribute("data-zero"), radio("tickets").getAttribute("data-count"), radio("tickets").hasAttribute("data-zero")], ["4", false, "0", true]);
roots[0].pin(true); roots[1].pin(false);
check("pin/unpin retains roots, frames and count", [roots.every(function(c,i){return c.body.firstChild === frames[i];}), counts()[7]], [true, "4"]);
choose("all");
check("All restores cards and topic-only total", [shown().length, cardVisibility(), counts()[0]], [7, [true, true, true, true], "14"]);
clickPin(C); choose("implementing");
check("pinned topic keeps its node and placement", [shown(), page.W.isDocked(C), counts()], [["c"], true, ["14", "1", "1", "1", "1", "1", "8", "4", "0"]]);
var before = page.board.childNodes.slice(), savedCards = store.cards;
choose("cards"); choose("closed"); choose("all");
check("filtering never moves or persists placement", [page.board.childNodes.every(function(n,i){return n === before[i];}), store.cards === savedCards], [true, true]);
choose("waiting");
D = stateTopic("d", "discussing"); R = stateTopic("r", "implementing");
swap([A, B, C, D, R, legacy, closed, tail]);
check("refresh re-evaluates selected state and research exit", [shown(), counts(), radio("waiting").getAttribute("aria-checked")], [[], ["14", "0", "2", "0", "2", "1", "8", "4", "0"], "true"]);
check("refresh retains filtered framework identity and contents", roots.every(function(c,i){return c.body.firstChild === frames[i] && c.el.parentNode && c.el.hasAttribute("data-state-filtered");}), true);
// Card lifecycle still runs while hidden; opens and both close placements recount.
var extra = page.W.open("preview", {path:"same-path"});
check("open honors active state filter and recounts", [extra.el.hasAttribute("data-state-filtered"), counts()[7]], [true, "5"]);
extra.close(); check("unpinned close recounts", counts()[7], "4");
roots[0].close(); check("pinned close recounts", counts()[7], "3");
choose("closed"); C = stateTopic("c", null, true); R = stateTopic("r", null, true);
swap([A, B, D, R, legacy, C, closed, tail]);
check("close removes operational state and increments closed", [counts(), R.hasAttribute("data-state-filtered")], [["14", "0", "2", "0", "0", "1", "10", "3", "0"], false]);
var closedPage = mk("div.closed-page", {}, [stateTopic("older", null, true), more(2, 6)]);
page.board.insertBefore(closedPage, tail); page.board.removeChild(tail); page.W.decorate();
check("history load preserves total and selected lifecycle", [counts(), closedPage.firstChild.hasAttribute("data-state-filtered")], [["14", "0", "2", "0", "0", "1", "10", "3", "0"], false]);
choose("cards"); check("Cards hides loaded closed page and paging", [closedPage.firstChild.hasAttribute("data-state-filtered"), closedPage.children[1].hasAttribute("data-state-filtered")], [true, true]);
boot(store, served, { kinds: ["preview", "terminal", "web", "explorer"] });
check("reload restores Cards selection and retained framework records", [radio("cards").getAttribute("aria-checked"), shown(), counts()[7]], ["true", [], "3"]);
[false, true].forEach(function (pop) {
  choose("researching");
  var steps = [
    ["ArrowRight", "discussing"], ["ArrowDown", "waiting"], ["ArrowLeft", "discussing"], ["ArrowUp", "researching"],
    ["ArrowLeft", pop ? "asks" : "tickets"],
    ["Home", "all"], ["ArrowRight", "asks"], ["ArrowRight", pop ? "researching" : "closed"],
    ["End", pop ? "tickets" : "awaiting-confirmation"], ["ArrowDown", "all"],
    ["ArrowUp", pop ? "tickets" : "awaiting-confirmation"]
  ];
  steps.forEach(function (step) {
    var selected = filterOrder.filter(function (value) { return radio(value, pop).getAttribute("aria-checked") === "true"; })[0], prevented = false;
    radio(selected, pop).fire("keydown", { key: step[0], preventDefault: function(){prevented=true;}, stopPropagation: function(){} });
    check("keyboard lifecycle " + pop + " " + step[0] + " to " + step[1], [radio(step[1]).getAttribute("aria-checked"), radio(step[1], true).getAttribute("aria-checked"), store["cards.state-filter"], prevented], ["true", "true", step[1], true]);
  });
});
store["cards.state-filter"] = "invented"; boot(store, served);
check("invalid persisted filter is all, never inferred", [shown().length, radio("all").getAttribute("aria-checked")], [7, "true"]);
boot({}, function(){return [];});
check("empty board has selectable zero counts", counts(), ["0","0","0","0","0","0","0","0","0"]);
var deferred;
boot({}, served, { toolbar: { filter: function(run){ deferred = run; } } });
var held = page.W.open("k", {name:"held"});
choose("cards");
check("filter waits for overview restoration callback", [shown(), held.el.hasAttribute("data-state-filtered"), radio("all").getAttribute("aria-checked"), page.store["cards.state-filter"]], [["a","b","c","d","r","legacy","old"], false, "true", undefined]);
deferred();
check("overview callback applies and persists selection", [shown(), held.el.hasAttribute("data-state-filtered"), radio("cards").getAttribute("aria-checked"), page.store["cards.state-filter"]], [[], false, "true", "cards"]);
process.stdout.write(bad.join("\n"));
`)
}

func TestBoardStateFilterAsks(t *testing.T) {
	cards, _ := json.Marshal(mustAsset(t, "cards.js"))
	runNode(t, "board ask filter", "var CARDS = "+string(cards)+";\n"+cardsDOM+`
var open, ack, no, closed, tail;
function served() {
  open = topic("open", 100, false); open.setAttribute("data-open-asks", "true");
  ack = topic("ack", 100, false); ack.setAttribute("data-open-asks", "true");
  no = topic("no", 100, false); no.setAttribute("data-open-asks", "false");
  closed = topic("closed", 100, true); closed.setAttribute("data-open-asks", "false");
  tail = more(1, 3); return [open, ack, no, closed, tail];
}
var store = {};
boot(store, served, {wide: true, kinds: ["preview"]});
function radio(pop) { return page.W.toolbar().querySelector((pop ? ".board-state-pop" : ".board-state-inline") + ' [data-filter="asks"]'); }
function count(pop) { return radio(pop).querySelector(".board-state-count").textContent; }
function visible(n) { return !n.hasAttribute("data-state-filtered"); }
var card = page.W.open("preview", {path:"ask-fixture"});
check("ask count is topics, excluding unloaded history", [count(false), count(true)], ["2", "2"]);
radio(false).fire("click", {});
check("asks shows only open ask topics, not cards or paging", [visible(open), visible(ack), visible(no), visible(closed), visible(tail), visible(card.el)], [true, true, false, false, false, false]);
check("ask selection synchronized and persisted", [radio(false).getAttribute("aria-checked"), radio(true).getAttribute("aria-checked"), store["cards.state-filter"]], ["true", "true", "asks"]);
open.setAttribute("data-open-asks", "false"); no.setAttribute("data-open-asks", "true");
swap([open, ack, no, closed, tail]);
check("refresh reevaluates asks without moving nodes", [visible(open), visible(no), count(false)], [false, true, "2"]);
boot(store, served);
check("reload restores asks", [radio(false).getAttribute("aria-checked"), visible(open), visible(no)], ["true", true, false]);
process.stdout.write(bad.join("\n"));
`)
}

func TestBoardStateFilterTickets(t *testing.T) {
	cards, _ := json.Marshal(mustAsset(t, "cards.js"))
	runNode(t, "board open tickets filter", "var CARDS = "+string(cards)+";\n"+cardsDOM+`
var openFeat, closedBug, researchOnly, doneOnly, plain, tail;
function served() {
  openFeat = topic("open-feat", 100, false); openFeat.setAttribute("data-open-tickets", "true");
  closedBug = topic("closed-bug", 100, true); closedBug.setAttribute("data-open-tickets", "true");
  researchOnly = topic("research-only", 100, false);
  doneOnly = topic("done-only", 100, false); doneOnly.setAttribute("data-open-tickets", "false");
  plain = topic("plain", 100, false);
  tail = more(1, 3);
  return [openFeat, closedBug, researchOnly, doneOnly, plain, tail];
}
var store = {};
boot(store, served, {wide: true, kinds: ["preview"]});
function radio(pop) { return page.W.toolbar().querySelector((pop ? ".board-state-pop" : ".board-state-inline") + ' [data-filter="tickets"]'); }
function count(pop) { return radio(pop).querySelector(".board-state-count").textContent; }
function visible(n) { return !n.hasAttribute("data-state-filtered"); }
var card = page.W.open("preview", {path:"ticket-fixture"});
check("ticket filter uses ticket sprite", radio(false).querySelector(".board-state-icon").html, '<svg class="li" aria-hidden="true"><use href="#i-ticket"/></svg>');
check("open ticket count includes closed topics with open non-research tickets", [count(false), count(true)], ["2", "2"]);
radio(false).fire("click", {});
check("tickets filter shows open-ticket topics including closed ones, hiding others, cards, and paging", [visible(openFeat), visible(closedBug), visible(researchOnly), visible(doneOnly), visible(plain), visible(tail), visible(card.el)], [true, true, false, false, false, false, false]);
check("compact trigger uses ticket sprite when tickets filter is active", page.W.toolbar().querySelector(".board-state-trigger .board-state-icon").html, '<svg class="li" aria-hidden="true"><use href="#i-ticket"/></svg>');
check("tickets selection synchronized and persisted", [radio(false).getAttribute("aria-checked"), radio(true).getAttribute("aria-checked"), store["cards.state-filter"]], ["true", "true", "tickets"]);
openFeat.removeAttribute("data-open-tickets"); researchOnly.setAttribute("data-open-tickets", "true");
swap([openFeat, closedBug, researchOnly, doneOnly, plain, tail]);
check("refresh reevaluates data-open-tickets without moving nodes", [visible(openFeat), visible(closedBug), visible(researchOnly), count(false)], [false, true, true, "2"]);
boot(store, served);
check("reload restores tickets filter", [radio(false).getAttribute("aria-checked"), visible(openFeat), visible(closedBug), visible(plain)], ["true", true, true, false]);
process.stdout.write(bad.join("\n"));
`)
}
