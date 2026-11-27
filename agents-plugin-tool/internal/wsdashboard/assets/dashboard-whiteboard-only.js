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
  var wbObserver = null, wbFoldRaf = 0, wbFoldTimer = 0;
  var wbTurnState = typeof WeakMap === "function" ? new WeakMap() : null;
  function wbCancelScheduledFold() {
    if (wbFoldRaf && typeof cancelAnimationFrame === "function") cancelAnimationFrame(wbFoldRaf);
    if (wbFoldTimer) clearTimeout(wbFoldTimer);
    wbFoldRaf = 0;
    wbFoldTimer = 0;
  }
  function wbSameSources(prev, next) {
    if (!prev || prev.length !== next.length) return false;
    for (var i = 0; i < next.length; i++) {
      if (prev[i] !== next[i]) return false;
    }
    return true;
  }
  function wbNodeTouchesFold(node) {
    if (!node || (node.nodeType !== undefined && node.nodeType !== 1)) return false;
    if (node.hasAttribute && node.hasAttribute("data-wb-clone")) return false;
    if (node.matches) {
      if (node.matches(".msg.fromlead[data-entry], [data-conv-turn], .turnline")) return true;
      return !!(node.querySelector && node.querySelector(".msg.fromlead[data-entry], [data-conv-turn]"));
    }
    return true;
  }
  function wbNeedsFoldForMutations(records) {
    if (!records || typeof records.length !== "number" || records.length === 0) return true;
    for (var i = 0; i < records.length; i++) {
      var rec = records[i];
      if (!rec) continue;
      var lists = [rec.addedNodes, rec.removedNodes];
      for (var l = 0; l < lists.length; l++) {
        var nodes = lists[l];
        if (!nodes) continue;
        for (var j = 0; j < nodes.length; j++) {
          if (wbNodeTouchesFold(nodes[j])) return true;
        }
      }
    }
    return false;
  }
  function wbScheduleFoldSync() {
    if (!wbOnlyOn() || wbFoldRaf || wbFoldTimer) return;
    var flush = function () {
      wbFoldRaf = 0;
      wbFoldTimer = 0;
      if (wbOnlyOn()) wbFoldSync();
    };
    if (typeof requestAnimationFrame === "function") wbFoldRaf = requestAnimationFrame(flush);
    else if (typeof setTimeout === "function") wbFoldTimer = setTimeout(flush, 0);
    else flush();
  }
  function wbFoldSync() {
    wbCancelScheduledFold();
    var box = entriesBox();
    if (!box) return;
    try {
      var on = wbOnlyOn();
      if (!on) {
        box.querySelectorAll("[data-wb-clone]").forEach(function (n) { n.remove(); });
        box.querySelectorAll("[data-wb-folded]").forEach(function (n) { n.removeAttribute("data-wb-folded"); });
        box.querySelectorAll("[data-conv-turn-plain]").forEach(function (n) { n.hidden = true; });
        box.querySelectorAll("[data-conv-turn-folded]").forEach(function (n) { n.hidden = true; });
        if (wbTurnState) wbTurnState = new WeakMap();
        return;
      }
      var replies = {};
      box.querySelectorAll(".msg.fromlead[data-entry]").forEach(function (r) { replies[r.getAttribute("data-entry")] = r; });
      var desiredFolded = typeof Set === "function" ? new Set() : null;
      var desiredShown = typeof Set === "function" ? new Set() : null;
      box.querySelectorAll("[data-conv-turn][data-replies]").forEach(function (turn) {
        var drawer = turn.querySelector("[data-conv-turn-drawer]"), plain = turn.hasAttribute("data-conv-turn-plain");
        var chip = plain ? null : turn.querySelector("[data-conv-turn-folded]");
        var prev = wbTurnState ? wbTurnState.get(turn) : null;
        if (!drawer) {
          if (prev && prev.clones) prev.clones.forEach(function (n) { n.remove(); });
          if (wbTurnState) wbTurnState.delete(turn);
          if (plain) turn.hidden = true;
          else if (chip) chip.hidden = true;
          return;
        }
        var sources = [];
        turn.getAttribute("data-replies").split(" ").forEach(function (key) {
          var reply = replies[key];
          if (reply) sources.push(reply);
        });
        if (!sources.length) {
          if (prev && prev.clones) prev.clones.forEach(function (n) { n.remove(); });
          else if (drawer.querySelectorAll) drawer.querySelectorAll("[data-wb-clone]").forEach(function (n) { n.remove(); });
          if (wbTurnState) wbTurnState.delete(turn);
          if (plain) turn.hidden = true;
          else if (chip) chip.hidden = true;
          return;
        }
        var line = plain && turn.parentNode ? turn.parentNode.querySelector(".turnline") : null;
        if (prev && wbSameSources(prev.sources, sources) && prev.plain === plain && prev.line === line) {
          for (var i = 0; i < sources.length; i++) {
            if (sources[i].getAttribute("data-wb-folded") !== "1") sources[i].setAttribute("data-wb-folded", "1");
            if (desiredFolded) desiredFolded.add(sources[i]);
          }
          if (plain) {
            if (turn.hidden) turn.hidden = false;
            if (desiredShown) desiredShown.add(turn);
            if (line) {
              if (line.getAttribute("data-wb-folded") !== "1") line.setAttribute("data-wb-folded", "1");
              if (desiredFolded) desiredFolded.add(line);
            }
          } else if (chip) {
            if (chip.hidden) chip.hidden = false;
            if (desiredShown) desiredShown.add(chip);
          }
          return;
        }
        if (prev && prev.clones) prev.clones.forEach(function (n) { n.remove(); });
        else if (drawer.querySelectorAll) drawer.querySelectorAll("[data-wb-clone]").forEach(function (n) { n.remove(); });
        var clones = [];
        for (var j = 0; j < sources.length; j++) {
          var reply = sources[j];
          var copy = reply.cloneNode(true);
          copy.removeAttribute("data-entry");
          copy.setAttribute("data-wb-clone", "1");
          drawer.appendChild(copy);
          clones.push(copy);
          reply.setAttribute("data-wb-folded", "1");
          if (desiredFolded) desiredFolded.add(reply);
        }
        if (plain) {
          turn.hidden = false;
          if (desiredShown) desiredShown.add(turn);
          // The summary stands in for the turn's own .turnline beside it.
          if (line) {
            line.setAttribute("data-wb-folded", "1");
            if (desiredFolded) desiredFolded.add(line);
          }
        } else if (chip) {
          chip.hidden = false;
          if (desiredShown) desiredShown.add(chip);
        }
        if (wbTurnState) wbTurnState.set(turn, { sources: sources, clones: clones, plain: plain, line: line });
      });
      if (desiredFolded) {
        box.querySelectorAll("[data-wb-folded]").forEach(function (n) {
          if (!desiredFolded.has(n)) n.removeAttribute("data-wb-folded");
        });
      }
      if (desiredShown) {
        box.querySelectorAll("[data-conv-turn-plain]").forEach(function (n) {
          if (!desiredShown.has(n)) n.hidden = true;
        });
        box.querySelectorAll("[data-conv-turn-folded]").forEach(function (n) {
          if (!desiredShown.has(n)) n.hidden = true;
        });
      }
    } finally {
      // Drain mutations we just performed (and any preceding merge already folded) so Observer won't rerun synchronously later.
      if (wbObserver && typeof wbObserver.takeRecords === "function") wbObserver.takeRecords();
    }
  }
  function wbApply(on, live) {
    var b = wbButton();
    if (!b) return;
    var s = live ? scrollState() : null;
    b.setAttribute("aria-pressed", on ? "true" : "false");
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
      wbObserver = new MutationObserver(function (records) {
        if (wbOnlyOn() && wbNeedsFoldForMutations(records)) wbScheduleFoldSync();
      });
      wbObserver.observe(box, { childList: true });
    }
  })();
