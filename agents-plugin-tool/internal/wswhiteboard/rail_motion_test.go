package wswhiteboard

import (
	"os/exec"
	"strings"
	"testing"
)

// TestRailMotionFollowsScrollOnly pins that rail.js turns a capsule into a
// bar (and back) only on a scroll. A run after a size change in the board
// (the ResizeObserver: a dashboard board swap growing and shrinking its
// topics, a body opened) redraws the stacks in place: during a swap's
// motion, item after item crossed the bottom stack's line, and each
// crossing hid that item's bar behind a copy aimed at where the bar stood
// that frame, so closed topics pouring in showed as blank rows one after
// another. The same membership change on a scroll still morphs. The cases
// execute under node when it is installed.
func TestRailMotionFollowsScrollOnly(t *testing.T) {
	data, err := assetFS.ReadFile("assets/rail.js")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; rail motion cases not executed")
	}
	// The prelude's elements gain what the motion touches (animate, clones,
	// attribute removal), the body can animate, and the board keeps its
	// listeners so a case can fire a scroll.
	script := railPrelude + `
var anims = [], listeners = {};
E.prototype.animate = function (k) { anims.push(this); return {}; };
E.prototype.cloneNode = function () { var c = new E(this.tag); c.cls = Object.assign({}, this.cls); return c; };
E.prototype.removeAttribute = function (k) { delete this.attrs[k]; };
E.prototype.removeChild = function (c) { this.children.splice(this.children.indexOf(c), 1); };
Object.defineProperty(E.prototype, "parentNode", { get: function () { return this.parent; } });
Object.defineProperty(E.prototype, "lastChild", { get: function () { return this.children[this.children.length - 1] || null; } });
document.body.animate = function () {};
board.addEventListener = function (t, f) { listeners[t] = f; };
function topics(off) { board.children.forEach(function (t) { if (t.attrs["data-topic"]) { var i = "abcdef".indexOf(t.attrs["data-topic"]); t.y = i * 600 - off; t.children[0].y = t.children[0].children[0].y = t.y; } }); }
function ghosts() { return anims.filter(function (e) { return e.cls["wb-rail-ghost"]; }).length; }
` + string(data) + `
frame();
// Topic c sits below the bottom line (745) in the bottom stack. The board
// moves it above the line by a size change (topics shift up 500): the
// stack loses it with no motion.
anims = []; topics(500); ro(); frame();
check("a size change morphs nothing", anims.length, 0);
// Back, settled by another size change, then the same crossing by a
// scroll: c's capsule grows into its bar, and a's bar, scrolled past,
// turns into the top stack's capsule.
topics(0); ro(); frame(); anims = [];
board.scrollTop = 500; listeners.scroll(); frame();
check("a scroll waits for destination layout", ghosts(), 0);
frame();
check("a scroll morphs at both edges", ghosts(), 2);
process.stdout.write(bad.join("\n"));`
	out, err := nodeScript(node, script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		t.Errorf("rail motion cases failed:\n%s", s)
	}
}

// TestRailGhostDropsOnReflowAndTargetMove pins that an active .wb-rail-ghost
// and its target's opacity-0 hide animation are cancelled immediately when
// reflow() runs (e.g. --wb-rail-inset / resize) or when a cap->bar target .tbar
// moves away from its captured to.top during a subsequent scroll/snap frame.
func TestRailGhostDropsOnReflowAndTargetMove(t *testing.T) {
	data, err := assetFS.ReadFile("assets/rail.js")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; rail ghost drop cases not executed")
	}
	script := railPrelude + `
var listeners = {}, barHides = [];
var barC = board.querySelector('section.topic[data-topic="c"] > .tophead > .tbar');
E.prototype.animate = function () {
  var rec = { el: this, cancelled: false, cancel: function () { this.cancelled = true; } };
  if (this === barC) barHides.push(rec);
  return rec;
};
E.prototype.cloneNode = function () { var c = new E(this.tag); c.cls = Object.assign({}, this.cls); return c; };
E.prototype.removeAttribute = function (k) { delete this.attrs[k]; };
E.prototype.removeChild = function (c) { var idx = this.children.indexOf(c); if (idx >= 0) { this.children.splice(idx, 1); c.parent = null; } };
Object.defineProperty(E.prototype, "parentNode", { get: function () { return this.parent; } });
Object.defineProperty(E.prototype, "lastChild", { get: function () { return this.children[this.children.length - 1] || null; } });
document.body.animate = function () {};
board.addEventListener = function (t, f) { listeners[t] = f; };
function liveGhosts() { return board.querySelectorAll(".wb-rail-ghost").length; }
` + string(data) + `
frame();
// Case 1: scroll unrails topic c from bottom stack (cap -> bar morph), then
// reflow() fires (simulating --wb-rail-inset or resize) before morph finishes.
barHides = [];
board.scrollTop = 500; listeners.scroll(); frame(); frame();
check("scroll creates ghosts at both edges", liveGhosts(), 2);
check("cap->bar morph hides topic c tbar", barHides.length, 1);
check("tbar hide animation active before reflow", barHides[0].cancelled, false);
ro();
check("reflow removes all live ghosts", liveGhosts(), 0);
check("reflow cancels tbar hide animation", barHides[0].cancelled, true);
frame();

// Case 2: scroll unrails topic c again, then a snap/scroll shifts #bcol so
// topic c's .tbar moves away from to.top (>2px) on the next scroll frame.
board.scrollTop = 0; ro(); frame();
barHides = [];
board.scrollTop = 500; listeners.scroll(); frame(); frame();
check("second scroll creates ghosts at both edges", liveGhosts(), 2);
check("second cap->bar morph hides topic c tbar", barHides.length, 1);
// Small movement (<=2px) keeps the active ghost alive.
board.scrollTop = 501; listeners.scroll(); frame();
check("tiny 1px move keeps bot ghost", liveGhosts(), 2);
check("tiny 1px move keeps tbar hide active", barHides[0].cancelled, false);
// Snap shift (>2px) drops the stale cap->bar ghost and cancels its hide animation.
board.scrollTop = 540; listeners.scroll(); frame();
check("snap shift drops bot cap->bar ghost", liveGhosts(), 1);
check("snap shift cancels tbar hide animation", barHides[0].cancelled, true);
process.stdout.write(bad.join("\n"));`
	out, err := nodeScript(node, script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		t.Errorf("rail ghost drop cases failed:\n%s", s)
	}
}
