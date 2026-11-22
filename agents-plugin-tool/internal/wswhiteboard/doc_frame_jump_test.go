package wswhiteboard

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDocFrameJumpsToInPageAnchor runs the framed panel's in-page anchor click
// under node. A settled check renders as check-N (page.tmpl "check"), so a
// link to it in a framed panel must post {wbDoc: "jump"} the same way item-N
// and pN do; the host's router (wsdashboard boardJump) takes the same ids.
// Anchors the frame does not name stay inert.
func TestDocFrameJumpsToInPageAnchor(t *testing.T) {
	raw, err := assetFS.ReadFile("assets/doc-frame.js")
	if err != nil {
		t.Fatal(err)
	}
	const start = "  d.addEventListener(\"click\", function (e) {\n    var t = e.target, a = t && t.closest && t.closest(\"a\");\n"
	const end = "\n  }, true);\n"
	js := string(raw)
	i := strings.Index(js, start)
	if i < 0 {
		t.Fatal("doc-frame.js holds no in-page anchor click listener")
	}
	block := js[i:]
	j := strings.Index(block, end)
	if j < 0 {
		t.Fatal("doc-frame.js in-page anchor click listener is not closed as expected")
	}
	block = block[:j+len(end)]
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; in-page anchor cases not executed")
	}
	script := `var handler, posted = [], bad = [];
var d = { addEventListener: function (kind, fn) { if (kind === "click") handler = fn; } };
var desktop = false;
function post(m) { posted.push(m); }
function link(a, e) { post({ wbDoc: "link" }); }
` + block + `
function fire(href) {
  var a = { getAttribute: function (n) { return n === "href" ? href : null; }, hasAttribute: function () { return false; } };
  var e = { button: 0, target: { closest: function (s) { return s === "a" ? a : null; } }, preventDefault: function () { this.defaultPrevented = true; } };
  posted = []; handler(e);
  return [!!e.defaultPrevented, JSON.stringify(posted)];
}
function check(what, got, want) { if (JSON.stringify(got) !== JSON.stringify(want)) bad.push(what + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want)); }
function jump(id) { return [true, JSON.stringify([{ wbDoc: "jump", id: id }])]; }
var inert = [false, "[]"];
check("#check-12", fire("#check-12"), jump("check-12"));
check("#check-1", fire("#check-1"), jump("check-1"));
check("#item-7", fire("#item-7"), jump("item-7"));
check("#item-1234567", fire("#item-1234567"), jump("item-1234567"));
check("#p1", fire("#p1"), jump("p1"));
check("#p12", fire("#p12"), jump("p12"));
["#check-", "#check-x", "#checkmark-3", "#item-", "#p0", "#topic-ab"].forEach(function (href) {
  check("unnamed " + href, fire(href), [true, "[]"]);
});
check("external link", fire("https://example.com/"), inert);
process.stdout.write(bad.join("\n"));
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil || len(out) > 0 {
		t.Fatalf("doc-frame in-page anchor jump: %v\n%s", err, out)
	}
}
