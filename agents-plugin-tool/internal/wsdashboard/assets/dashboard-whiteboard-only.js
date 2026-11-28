// Whiteboard-only mode: a per-session composer toggle. While ON, a send
// carries whiteboard_first (the daemon adds a model-only directive) and the
// lead's plain replies fold into their turn's drawer. The reply nodes stay in
// place, hidden, so the entries merge keeps its one-node-per-entry contract;
// clones fill the drawers. The toggle exists only on a session with an
// <ws-ambient> (the page renders #wbonly then); without the button the mode
// is OFF whatever is stored.
  var WBONLY_KEY = "ws.dashboard.whiteboardFirst." + rid;
  function wbButton() { return $("wbonly"); }
  function wbStored() { try { var v = localStorage.getItem(WBONLY_KEY); return v === "on" || v === "1"; } catch (e) { return false; } }
  function wbStore(on) { try { if (on) localStorage.setItem(WBONLY_KEY, "on"); else localStorage.removeItem(WBONLY_KEY); } catch (e) {} }
  // wbOnlyOn is the mode as the page shows it: the toggle's pressed state.
  function wbOnlyOn() { var b = wbButton(); return !!b && b.getAttribute("aria-pressed") === "true"; }
  var wbObserver = null, wbFoldRaf = 0, wbFoldTimer = 0;
  var wbTurnState = typeof WeakMap === "function" ? new WeakMap() : null;
  var WB_CLUSTER = "wb-turn-cluster";
  // wbClusterOpen holds the keys of the members of every cluster the reader
  // opened; a cluster is open while any of its members is listed, so one that
  // grows (older history, a live turn completing) keeps its state.
  var wbClusterOpen = Object.create(null);
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
      // The cluster header is this script's own node: never a reason to sync.
      if (node.matches("." + WB_CLUSTER)) return false;
      // Any entry row can start or end a cluster run (a system mail joins one,
      // a user message splits one), so every keyed row counts.
      if (node.matches(".msg.fromlead[data-entry], [data-conv-turn], .turnline, [data-entry]")) return true;
      return !!(node.querySelector && node.querySelector(".msg.fromlead[data-entry], [data-conv-turn], [data-entry]"));
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
        wbClusterOff(box);
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
          copy.setAttribute("data-wb-clone-entry", reply.getAttribute("data-entry") || "");
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
      wbClusterSync(box);
    } finally {
      // Drain mutations we just performed (and any preceding merge already folded) so Observer won't rerun synchronously later.
      if (wbObserver && typeof wbObserver.takeRecords === "function") wbObserver.takeRecords();
    }
  }
  // --- Folded-turn clusters -------------------------------------------------
  // While the mode is ON, a maximal run of two or more consecutive completed
  // folded turns (and the standalone system mail / board-nudge rows between
  // them) collapses into one "start..end" header row. Every member stays a
  // direct child of the entries box, in place: the header is the only node
  // added, and members are only marked (data-wb-clustered hides them,
  // data-wb-cluster-open shows them indented), so mergeEntries, dedupTurn and
  // the ENTRY_CAP eviction keep their one-node-per-entry contract. Hidden
  // siblings (absorbed markers, folded replies, hidden plain summaries) are
  // transparent to a run; anything else visible (a user message, whiteboard
  // update, approval, boundary, failed or live turn, peer mail) ends it.
  function wbNum(el, attr) { var n = parseInt(el.getAttribute(attr), 10); return n > 0 ? n : 0; }
  function wbSetAttr(el, name, on) {
    if (on) { if (el.getAttribute(name) !== "1") el.setAttribute(name, "1"); }
    else if (el.hasAttribute(name)) el.removeAttribute(name);
  }
  function wbSetText(el, text) { if (el.textContent !== text) el.textContent = text; }
  function wbSetChip(el, text) {
    wbSetText(el, text);
    if (!text !== el.hasAttribute("hidden")) { if (text) el.removeAttribute("hidden"); else el.setAttribute("hidden", ""); }
  }
  function wbClock(ms) {
    var d = new Date(ms), h = d.getHours(), m = d.getMinutes();
    return (h < 10 ? "0" : "") + h + ":" + (m < 10 ? "0" : "") + m;
  }
  // wbCompactTok mirrors the server's compactTokens so a summed delta reads
  // like the per-turn ones.
  function wbCompactTok(n) {
    if (n < 1000) return String(n);
    if (n < 99950) return (n / 1000).toFixed(1) + "k";
    if (n < 999500) return Math.round(n / 1000) + "k";
    return (n / 1e6).toFixed(1) + "M";
  }
  function wbMemberTs(el, turn) {
    var ts = wbNum(turn, "data-turn-ts") || wbNum(el, "data-mail-ts");
    if (ts) return ts;
    var at = turn.getAttribute("data-turn-at") || el.getAttribute("data-at"), p = at ? Date.parse(at) : NaN;
    return p > 0 ? p : 0;
  }
  // wbClusterRole sorts one box child: "header" (ours), "skip" (invisible, so
  // transparent to a run), "break", or a member descriptor.
  function wbClusterRole(c) {
    if (!c || c.nodeType !== 1) return "skip";
    if (c.matches("." + WB_CLUSTER)) return "header";
    if (c.hasAttribute("hidden") || c.hasAttribute("data-wb-folded") || c.hasAttribute("data-turn-absorbed") ||
        c.hasAttribute("data-turn-live-absorbed") || c.getAttribute("data-mail-wake") === "suppressed") return "skip";
    var turn = null, mail = false;
    if (c.hasAttribute("data-conv-turn")) turn = c;
    else if (c.matches(".turn-plain-host")) {
      turn = c.querySelector("[data-conv-turn-plain]");
      if (!turn || turn.hasAttribute("hidden")) return "break";
    } else if (c.hasAttribute("data-mail-system") || (c.hasAttribute("data-board-nudge") && c.matches(".mail-notification"))) {
      turn = c;
      mail = true;
    }
    if (!turn) return "break";
    if (!mail && (turn.matches(".d-turn-live") || turn.matches(".live") || wbNum(turn, "data-turn-failed") > 0)) return "break";
    var key = (mail ? "" : turn.getAttribute("data-turn-key")) || c.getAttribute("data-entry") || "";
    if (!key) return "break";
    return {
      el: c, key: key, mail: mail, ts: wbMemberTs(c, turn),
      calls: mail ? 0 : wbNum(turn, "data-turn-calls"),
      mails: mail ? 1 : wbNum(turn, "data-turn-mails"),
      tok: mail ? 0 : wbNum(turn, "data-turn-tok"),
      est: !mail && turn.hasAttribute("data-turn-tok-est")
    };
  }
  function wbEl(tag, cls) {
    var e = doc.createElement(tag);
    if (cls) e.setAttribute("class", cls);
    return e;
  }
  function wbIcon(parent, name, glyph) {
    if (typeof gIcon === "function") parent.appendChild(gIcon(name));
    else parent.textContent = glyph;
  }
  function wbClusterBuild(key) {
    var h = wbEl("button", WB_CLUSTER), ico = wbEl("span", "wbc-ico"), main = wbEl("span", "wbc-main"), chev = wbEl("span", "wbc-chev");
    var label = typeof wsT === "function" ? wsT("js.conv.cluster.toggle") : "";
    h.setAttribute("type", "button");
    h.setAttribute("data-wb-cluster-key", key);
    h.setAttribute("aria-expanded", "false");
    if (label) { h.setAttribute("title", label); h.setAttribute("aria-label", label); }
    ico.setAttribute("aria-hidden", "true");
    chev.setAttribute("aria-hidden", "true");
    wbIcon(ico, "check", "✓");
    wbIcon(chev, "chev", "›");
    h.wbCount = main.appendChild(wbEl("span", "wbc-count"));
    h.wbCalls = main.appendChild(wbEl("span", "wbc-chip wbc-calls"));
    h.wbMails = main.appendChild(wbEl("span", "wbc-chip wbc-mails"));
    h.wbTok = main.appendChild(wbEl("span", "wbc-chip wbc-tok"));
    h.wbTime = wbEl("span", "wbc-time");
    h.appendChild(ico);
    h.appendChild(main);
    h.appendChild(h.wbTime);
    h.appendChild(chev);
    h.addEventListener("click", function () { wbClusterToggle(h); });
    return h;
  }
  function wbClusterPaint(h, st, open) {
    // wsTn is called with literal keys so the i18n key sweep sees the plural bases.
    wbSetText(h.wbCount, st.turns > 0 ? wsTn("js.conv.cluster.turns", st.turns) : wsTn("js.conv.cluster.mails", st.mails));
    wbSetChip(h.wbCalls, st.calls > 0 ? wsTn("js.conv.cluster.tools", st.calls) : "");
    wbSetChip(h.wbMails, st.turns > 0 && st.mails > 0 ? wsTn("js.conv.cluster.mails", st.mails) : "");
    wbSetChip(h.wbTok, st.tok > 0 ? (st.est ? "~+" : "+") + wbCompactTok(st.tok) + " tok" : "");
    var a = st.t0 ? wbClock(st.t0) : "", b = st.t1 ? wbClock(st.t1) : "";
    wbSetText(h.wbTime, a && b && a !== b ? a + ".." + b : (a || b));
    var ex = open ? "true" : "false";
    if (h.getAttribute("aria-expanded") !== ex) h.setAttribute("aria-expanded", ex);
  }
  function wbClusterSync(box) {
    if (typeof doc === "undefined" || !doc || typeof doc.createElement !== "function") return;
    var kids = Array.prototype.slice.call(box.children), scan = [], headers = [], runs = [], run = [], seen = Object.create(null);
    var close = function () { if (run.length >= 2) runs.push(run); run = []; };
    kids.forEach(function (c, i) {
      var r = wbClusterRole(c);
      if (r === "header") { headers.push(c); return; }
      if (r === "skip") { scan.push({ el: c, m: null }); return; }
      if (r === "break") { close(); scan.push({ el: c, m: null }); return; }
      r.idx = i;
      seen[r.key] = true;
      run.push(r);
      scan.push({ el: c, m: r });
    });
    close();
    runs.forEach(function (r) { r.forEach(function (m) { m.inRun = true; }); });
    // Rows that are not (or no longer) in a cluster show as themselves.
    scan.forEach(function (s) {
      if (s.m && s.m.inRun) return;
      if (s.el.hasAttribute("data-wb-clustered")) s.el.removeAttribute("data-wb-clustered");
      if (s.el.hasAttribute("data-wb-cluster-open")) s.el.removeAttribute("data-wb-cluster-open");
    });
    Object.keys(wbClusterOpen).forEach(function (k) { if (!seen[k]) delete wbClusterOpen[k]; });
    var byKey = Object.create(null), have = Object.create(null);
    runs.forEach(function (r) { byKey[r[0].key] = r; });
    headers.forEach(function (h) {
      var k = h.getAttribute("data-wb-cluster-key");
      if (byKey[k] && !have[k]) have[k] = h;
      else h.remove();
    });
    runs.forEach(function (r) {
      var first = r[0], key = first.key, h = have[key];
      var open = r.some(function (m) { return wbClusterOpen[m.key]; });
      if (!h) {
        h = wbClusterBuild(key);
        box.insertBefore(h, first.el);
      } else if (kids[first.idx - 1] !== h) {
        box.insertBefore(h, first.el);
      }
      var st = { turns: 0, calls: 0, mails: 0, tok: 0, est: false, t0: 0, t1: 0 }, keys = [];
      r.forEach(function (m) {
        keys.push(m.key);
        if (!m.mail) st.turns++;
        st.calls += m.calls;
        st.mails += m.mails;
        st.tok += m.tok;
        if (m.est) st.est = true;
        if (m.ts) { if (!st.t0) st.t0 = m.ts; st.t1 = m.ts; }
      });
      var lang = typeof wsI18n === "object" && wsI18n ? wsI18n.lang : "";
      var sig = [keys.join(" "), st.turns, st.calls, st.mails, st.tok, st.est ? 1 : 0, st.t0, st.t1, open ? 1 : 0, lang].join("|");
      if (h.wbSig !== sig) {
        wbClusterPaint(h, st, open);
        h.wbSig = sig;
      }
      h.wbKeys = keys;
      r.forEach(function (m) {
        wbSetAttr(m.el, "data-wb-clustered", !open);
        wbSetAttr(m.el, "data-wb-cluster-open", open);
      });
    });
  }
  function wbClusterToggle(h) {
    var box = entriesBox(), keys = h.wbKeys || [];
    if (!box || !keys.length) return;
    var open = keys.some(function (k) { return wbClusterOpen[k]; });
    keys.forEach(function (k) { if (open) delete wbClusterOpen[k]; else wbClusterOpen[k] = true; });
    var s = typeof scrollState === "function" ? scrollState() : null;
    wbClusterSync(box);
    if (s) { restoreScroll(s); queuePinConvo(); }
  }
  // wbRevealClustered opens the cluster that hides node (an entry jump target).
  function wbRevealClustered(box, node) {
    var c = node;
    while (c && c.parentNode !== box) c = c.parentNode;
    if (!box || !c || !c.hasAttribute || !c.hasAttribute("data-wb-clustered")) return;
    var m = wbClusterRole(c);
    if (typeof m !== "object") return;
    wbClusterOpen[m.key] = true;
    wbClusterSync(box);
  }
  function wbClusterOff(box) {
    box.querySelectorAll("." + WB_CLUSTER).forEach(function (n) { n.remove(); });
    box.querySelectorAll("[data-wb-clustered]").forEach(function (n) { n.removeAttribute("data-wb-clustered"); });
    box.querySelectorAll("[data-wb-cluster-open]").forEach(function (n) { n.removeAttribute("data-wb-cluster-open"); });
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
