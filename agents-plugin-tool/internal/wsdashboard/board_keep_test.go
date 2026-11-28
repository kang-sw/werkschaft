package wsdashboard

import (
	"regexp"
	"strings"
	"testing"
)

// The board swap captures only inner scrollers (docked panes and the current
// card), applies them once after the morph, then settles the card anchor; it
// never numerically restores an owned scroller.
func TestBoardKeepWiring(t *testing.T) {
	data, err := dashboardTestAsset("assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	swap := dashboardFuncs(t, "swap")
	order := []string{
		"html = boardStageInput(boardLatest.html || html);",
		"var anchored = board ? anchorCards() : null;",
		"var kept = board ? boardKeep() : null",
		"ins = morphBoard(el, html, boardPrev);",
		"window.wbHistory.sweep();",
		"window.wbHistory.restore(n, history);",
		"watchBoard(n);",
		"restoreScroll(scroll);",
		"if (kept) kept.h.apply();",
		"anchorSettle(anchored);",
	}
	at := 0
	for _, s := range order {
		i := strings.Index(swap[at:], s)
		if i < 0 {
			t.Fatalf("swap lacks %q after offset %d", s, at)
		}
		at += i + len(s)
	}
	if strings.Count(swap, "kept.h.apply()") != 1 {
		t.Error("swap must apply its owner captures exactly once, outside animation setup")
	}
	for _, retired := range []string{"function boardAnchor(", "function boardKeepSettle(", "function boardView(", "function boardKept("} {
		if strings.Contains(js, retired) {
			t.Errorf("retired ordinary target selection remains: %s", retired)
		}
	}
	if !strings.Contains(js, "\n  boardBoxes().forEach(watchBoard);\n") || !strings.Contains(js, "btn.parentNode.replaceChild(page, btn);\n      watchBoard(page);") {
		t.Error("initial and fetched board sections must remain watched")
	}
	for _, re := range []string{
		`(?s)\n  var modeHash = "", hiddenRegions = \{\}, treeRendered = null, entryRendered = null, panelRendered = null;\n.*?\n  function pageHidden\(\) \{ return docHidden\(\) \|\| shHidden; \}\n`,
		`function receiveRegion\(ev\) \{[^}]*if \(pageHidden\(\)\) \{\n        delete hiddenRegions\[ev\.name\];`,
		`function showRegions\(\) \{\n    if \(pageHidden\(\)\) return;`,
	} {
		if !regexp.MustCompile(re).MatchString(js) {
			t.Errorf("dashboard.js does not match %s", re)
		}
	}
	if !strings.Contains(dashboardFuncs(t, "topicResized"), "s.wbScrollOwner") {
		t.Error("resize observer remains a competing writer on owned scrollers")
	}
}

func TestBoardKeepUnderNode(t *testing.T) {
	src := dashboardFuncs(t, "boardKeep", "watchBoard") + dashboardJSBlock(t, "\n  var scrollRestore =", "\n  [\"wheel\"")
	runNode(t, "board owner captures", animationFrameHarness+`
var bad = [], log = [], owned = null, convo = null, stick = false;
function check(what, ok) { if (!ok) bad.push(what); }
var main = {scrollTop:400, wbScrollOwner:true}, pins = {scrollTop:500, wbScrollOwner:true};
var a = {name:"a", scroller:main}, b = {name:"b", scroller:pins}, c = {name:"c", scroller:main};
var window = {wbHistory:{
 keep:function(n,opt) {
   if (opt && opt.capture === false) return {scroller:n.scroller};
   log.push("capture:"+n.name);
   return {apply:function(){log.push("apply:"+n.name);},end:function(){log.push("end:"+n.name);}};
 },
 watch:function(n){log.push("watch:"+n.name);return {};},
 keeping:function(n){return n===owned ? {} : null;}
}};
var inner = {name:"card", scroller:{scrollTop:1, wbScrollOwner:true}}, cardEl = {firstElementChild:inner}, mainBox = {contains:function(n){return n===inner || n===other2;}};
var other2 = {name:"other-card"}, cur = cardEl;
function region(){return mainBox;}
function anchorCurrent(){return cur;}
function anchorOwner(n){return n===inner ? cardEl : null;}
function boardBoxes(){return [mainBox,{firstElementChild:a},{firstElementChild:b}];}
function q(sel,root){return root.kids || [];}
`+src+`
var kept = boardKeep([a,b,c]);
check("pre-capture every actual scroller once",log.join() === "capture:a,capture:b");
kept.h.apply();
check("both independent captures apply once",log.join() === "capture:a,capture:b,apply:a,apply:b");
log=[]; boardKeep().h.end();
check("docked panes and the current card participate; the main board does not",log.join() === "capture:a,capture:b,capture:card,end:a,end:b,end:card");
log=[]; boardKeep([other2]);
check("a node of another board card is not captured",log.length === 0);
var section={name:"topic",nodeType:1,matches:function(){return true;}};
log=[];watchBoard(section);watchBoard(section);
check("legacy watch hook leaves card ownership alone",log.length === 0);
var other={scrollTop:5}, transient={scrollTop:60}; owned=transient;
restoreScroll([[main,100],[pins,100],[other,50],[transient,0]]);
check("scroll restoration waits for the frame", other.scrollTop===5 && transient.scrollTop===60);
restoreScroll([[other,99]]);
check("multiple restores share one callback", animationFrames.size===1);
flushAnimationFrames();
check("permanent owner and active jump are not numerically restored",main.scrollTop===400 && pins.scrollTop===500 && other.scrollTop===50 && transient.scrollTop===60);
owned=null;restoreScroll([[main,0],[transient,0]]);
flushAnimationFrames();
check("ownership persists between transactions",main.scrollTop===400 && transient.scrollTop===0);
process.stdout.write(bad.join("\n"));`)
}
