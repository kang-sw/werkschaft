package wswhiteboard

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDocFrameHandsOverviewKey(t *testing.T) {
	raw, err := assetFS.ReadFile("assets/doc-frame.js")
	if err != nil {
		t.Fatal(err)
	}
	_, js, found := strings.Cut(string(raw), "  // Like the session document, leave editing, terminal and IME input native.\n")
	if !found {
		t.Fatal("missing Overview key listener")
	}
	// Stop at the next listener: later ones (find, hover) are not under test,
	// would overwrite the fake handler, and reference browser globals.
	js, _, _ = strings.Cut(js, "  // Ctrl/Cmd+F inside a framed card")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; Overview key cases not executed")
	}
	script := `var posted = 0, handler, bad = [];
var d = { addEventListener: function (kind, fn) { handler = fn; } };
function post(m) { if (m.wbDoc === "overview") posted++; }
function fire(attrs, selector) {
  var e = Object.assign({ key: "q", ctrlKey: true, target: { closest: function (s) { return selector && s.indexOf(selector) >= 0; } },
    preventDefault: function () { this.defaultPrevented = true; } }, attrs || {});
  posted = 0; handler(e); return [!!e.defaultPrevented, posted];
}
function check(what, got, want) { if (JSON.stringify(got) !== JSON.stringify(want)) bad.push(what + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want)); }
` + js + `
check("Ctrl+Q", fire(), [true, 1]);
[ {ctrlKey:false}, {metaKey:true}, {altKey:true}, {shiftKey:true}, {isComposing:true}, {keyCode:229}, {key:"k"} ].forEach(function (attrs) {
  check("unclaimed key " + JSON.stringify(attrs), fire(attrs), [false, 0]);
});
check("already claimed", fire({ defaultPrevented: true }), [true, 0]);
["input", "textarea", "select", "[contenteditable]", ".xterm", ".tm-host"].forEach(function (s) {
  check("native target " + s, fire({}, s), [false, 0]);
});
check("inherited editable", fire({ target: { isContentEditable: true } }), [false, 0]);
process.stdout.write(bad.join("\n"));
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil || len(out) > 0 {
		t.Fatalf("doc-frame Overview key: %v\n%s", err, out)
	}
}

func TestDocFrameHandsGlobalShortcutKeys(t *testing.T) {
	raw, err := assetFS.ReadFile("assets/doc-frame.js")
	if err != nil {
		t.Fatal(err)
	}
	_, js, found := strings.Cut(string(raw), "  // Like the session document, leave editing, terminal and IME input native.\n")
	if !found {
		t.Fatal("missing global shortcut key listener")
	}
	js, _, _ = strings.Cut(js, "  // Ctrl/Cmd+F inside a framed card")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; global shortcut key cases not executed")
	}
	script := `var posted = [], handler, bad = [];
var d = { addEventListener: function (kind, fn) { handler = fn; } };
function post(m) { posted.push(m.wbDoc); }
function fire(attrs, selector) {
  var e = Object.assign({ key: "g", ctrlKey: true, target: { closest: function (s) { return selector && s.indexOf(selector) >= 0; } },
    preventDefault: function () { this.defaultPrevented = true; } }, attrs || {});
  posted = []; handler(e); return [!!e.defaultPrevented, posted];
}
function check(what, got, want) { if (JSON.stringify(got) !== JSON.stringify(want)) bad.push(what + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want)); }
` + js + `
check("Ctrl+G", fire({ key: "g" }), [true, ["git"]]);
check("Ctrl+B", fire({ key: "b" }), [true, ["ticketmap"]]);
check("Ctrl+E", fire({ key: "e" }), [true, ["files"]]);
check("Ctrl+Q", fire({ key: "q" }), [true, ["overview"]]);
["g", "b", "e"].forEach(function (k) {
  ["input", "textarea", "select", "[contenteditable]", ".xterm", ".tm-host"].forEach(function (s) {
    check(k + " in " + s, fire({ key: k }, s), [false, []]);
  });
});
process.stdout.write(bad.join("\n"));
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil || len(out) > 0 {
		t.Fatalf("doc-frame global shortcut keys: %v\n%s", err, out)
	}
}
