// Whiteboard-only mode: a per-session composer toggle. While ON, a send
// carries whiteboard_first (the daemon adds a model-only directive) and the
// lead's plain replies fold into their turn's drawer. The reply nodes stay in
// place, hidden, so the entries merge keeps its one-node-per-entry contract;
// clones fill the drawers. The toggle exists only on a session with an
// <ws-ambient> (the page renders #wbonly then); without the button the mode
// is OFF whatever is stored.
  var WBONLY_KEY = "ws.dashboard.whiteboardFirst." + rid;
  function wbButton() { return $("wbonly"); }
  function wbStored() { try { return localStorage.getItem(WBONLY_KEY) === "on"; } catch (e) { return false; } }
  function wbStore(on) { try { if (on) localStorage.setItem(WBONLY_KEY, "on"); else localStorage.removeItem(WBONLY_KEY); } catch (e) {} }
  // wbOnlyOn is the mode as the page shows it: the toggle's pressed state.
  function wbOnlyOn() { var b = wbButton(); return !!b && b.getAttribute("aria-pressed") === "true"; }
  function wbFoldSync() {
    var box = entriesBox();
    if (!box) return;
    var on = wbOnlyOn();
    box.querySelectorAll("[data-wb-clone]").forEach(function (n) { n.remove(); });
    box.querySelectorAll("[data-wb-folded]").forEach(function (n) { n.removeAttribute("data-wb-folded"); });
    box.querySelectorAll("[data-conv-turn-plain]").forEach(function (n) { n.hidden = true; });
    box.querySelectorAll("[data-conv-turn-folded]").forEach(function (n) { n.hidden = true; });
    if (!on) return;
    var replies = {};
    box.querySelectorAll(".msg.fromlead[data-entry]").forEach(function (r) { replies[r.getAttribute("data-entry")] = r; });
    box.querySelectorAll("[data-conv-turn][data-replies]").forEach(function (turn) {
      var drawer = turn.querySelector("[data-conv-turn-drawer]"), plain = turn.hasAttribute("data-conv-turn-plain"), any = false;
      if (!drawer) return;
      turn.getAttribute("data-replies").split(" ").forEach(function (key) {
        var reply = replies[key];
        if (!reply) return;
        var copy = reply.cloneNode(true);
        copy.removeAttribute("data-entry");
        copy.setAttribute("data-wb-clone", "1");
        drawer.appendChild(copy);
        reply.setAttribute("data-wb-folded", "1");
        any = true;
      });
      if (!any) return;
      if (plain) {
        turn.hidden = false;
        // The summary stands in for the turn's own .turnline beside it.
        var line = turn.parentNode && turn.parentNode.querySelector(".turnline");
        if (line) line.setAttribute("data-wb-folded", "1");
      } else {
        var chip = turn.querySelector("[data-conv-turn-folded]");
        if (chip) chip.hidden = false;
      }
    });
  }
  function wbApply(on, live) {
    var b = wbButton();
    if (!b) return;
    var s = live ? scrollState() : null;
    b.setAttribute("aria-pressed", on ? "true" : "false");
    var hint = $("wbonly-hint");
    if (hint) hint.hidden = !on;
    root.toggleAttribute("data-wb-only", on);
    wbFoldSync();
    if (s) { restoreScroll(s); queuePinConvo(); }
  }
  (function wbInit() {
    var b = wbButton();
    if (!b) return;
    b.addEventListener("click", function () {
      var on = !wbOnlyOn();
      wbStore(on);
      wbApply(on, true);
    });
    wbApply(wbStored(), false);
    var box = entriesBox();
    if (box && typeof MutationObserver === "function") {
      new MutationObserver(function () { if (wbOnlyOn()) wbFoldSync(); }).observe(box, { childList: true });
    }
  })();
