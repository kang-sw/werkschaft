// Composer interrupt, agent, and task status controls.
  // --- interrupt badge ---------------------------------------------------------
  // The badge on the border between the conversation and the composer shows
  // while the session has a live adapter and its lead works on a turn (the
  // convlive region's data-phase). One click stops the turn and takes every
  // held message back into the composer. It then stays disabled until that
  // turn ends, so a second click cannot cut the next turn: until the phase
  // leaves the working set or the region's data-turn (the server's
  // turn-start stamp) changes from the one seen at the click. The stamp is
  // what catches a next turn that starts at once (held input or mail
  // delivered at turn end), which no page swap ever shows as idle.
  var ibadge = $("ibadge"), ilabel = ibadge && ibadge.querySelector(".ilabel");
  var WORKING = { thinking: 1, replying: 1, tool: 1, permission: 1 };
  var liveMode = mode === "live", interrupting = false, interruptedTurn = "";
  function leadWorking() {
    var c = doc.querySelector("#r-convlive .clive");
    return !!c && WORKING[c.getAttribute("data-phase")] === 1;
  }
  function leadTurn() {
    var c = doc.querySelector("#r-convlive .clive");
    return (c && c.getAttribute("data-turn")) || "";
  }
  function syncBadge() {
    // The session page's badge opens the agents island's full view, so it shows there
    // whether or not the session is live and anything runs.
    var popover = !!$("agpop");
    subBadge(sbadge, popover ? doc.querySelector("#r-tree .ag-source") : doc.querySelector("#r-convlive .clive"), liveMode || popover, popover);
    bgBadge(doc.querySelector("#r-convlive .clive"));
    syncSteerAll();
    if (!ibadge) return;
    var on = liveMode && leadWorking();
    if (!on || leadTurn() !== interruptedTurn) interrupting = false;
    ibadge.hidden = !on;
    ibadge.disabled = interrupting;
    ibadge.classList.toggle("busy", interrupting);
    var label = wsT(interrupting ? "js.interrupt.busy" : "js.interrupt");
    if (ilabel && ilabel.textContent !== label) ilabel.textContent = label;
  }
  // --- subagent badge --------------------------------------------------------
  // Beside the interrupt badge: a spinner, the robot icon, and "N(+M)"
  // (the word "subagents" is the title and accessible name) while the session runs subagents, whether or not its lead
  // works. N is data-sub (its running direct children), M data-subdeep
  // (every running agent below them), counted by the
  // server. The session reads the tree source; tracker chat reads convlive.
  // "(+M)" shows only when M is above zero. on gates it: the
  // session page's live mode, or the support chat card's open conversation.
  // stay keeps it shown, quiet (idle), while nothing runs.
  var sbadge = $("sbadge");
  function subBadge(badge, clive, on, stay) {
    if (!badge) return;
    var n = clive ? Number(clive.getAttribute("data-sub")) || 0 : 0;
    var m = clive ? Number(clive.getAttribute("data-subdeep")) || 0 : 0;
    badge.hidden = !(on && (stay || n + m > 0));
    if (badge.hidden) return;
    badge.classList.toggle("idle", n + m === 0);
    var count = m > 0 ? n + "(+" + m + ")" : String(n);
    var label = wsTn("js.subagents", n + m, count);
    var el = badge.querySelector(".slabel");
    if (el && el.textContent !== count) el.textContent = count;
    // The word moves to the accessible name and tooltip, like the clock badge.
    if (badge.getAttribute("aria-label") !== label) {
      if (badge.dataset.tip === undefined) badge.dataset.tip = badge.title;
      badge.setAttribute("aria-label", label);
      badge.title = label + (badge.dataset.tip ? " · " + badge.dataset.tip : "");
    }
  }
  // --- background-task badge ---------------------------------------------------
  // Separate harness-owned background and monitor pills follow subagents.
  // The live region carries counts for terminal and hosted sessions alike;
  // a missing count or a zero leaves its pill hidden.
  var bgbadge = $("bgbadge"), bgTitle = bgbadge ? bgbadge.title : "";
  var monbadge = $("monbadge"), monTitle = monbadge ? monbadge.title : "";
  function taskBadge(badge, clive, attr, title) {
    if (!badge) return;
    var n = clive ? Number(clive.getAttribute(attr)) || 0 : 0;
    badge.hidden = !(liveMode && n > 0);
    if (badge.hidden) return;
    var label = attr === "data-bg" ? wsTn("js.bgtasks", n) : wsTn("js.monitors", n);
    var el = badge.querySelector(".blabel");
    if (el && el.textContent !== label) el.textContent = label;
    if (badge.getAttribute("aria-label") !== label) {
      badge.setAttribute("aria-label", label);
      badge.title = label + (title ? " \u00b7 " + title : "");
    }
  }
  function bgBadge(clive) {
    if (bgbadge) {
      var bg = clive ? Number(clive.getAttribute("data-bg")) || 0 : 0;
      var execTotal = clive ? Number(clive.getAttribute("data-bg-exec")) || 0 : 0;
      var execSelf = clive ? Number(clive.getAttribute("data-bg-exec-self")) || 0 : 0;
      var execDesc = clive ? Number(clive.getAttribute("data-bg-exec-desc")) || 0 : 0;
      var n = bg + execTotal;
      var popOpen = typeof bgExecOpen === "function" && bgExecOpen();
      bgbadge.hidden = !(liveMode && (n > 0 || popOpen));
      if (!bgbadge.hidden) {
        var label = wsTn("js.bgtasks", n);
        var el = bgbadge.querySelector(".blabel");
        if (el && el.textContent !== label) el.textContent = label;
        if (bgbadge.classList) bgbadge.classList.toggle("has-exec", execTotal > 0);
        var tip = label;
        if (execTotal > 0) {
          var parts = [];
          if (execDesc > 0 && typeof wsT === "function") parts.push(wsT("js.bgexec.split", String(execSelf), String(execDesc)));
          if (bg > 0) parts.push(wsTn("js.bgtasks", bg));
          if (parts.length) tip = label + " (" + parts.join(" \u00b7 ") + ")";
          else if (bgbadge.tagName === "BUTTON") tip = label + " \u00b7 " + wsT("js.bgexec.badge.tip");
        } else if (bgTitle) {
          tip = label + " \u00b7 " + bgTitle;
        }
        if (bgbadge.getAttribute("aria-label") !== label) bgbadge.setAttribute("aria-label", label);
        if (bgbadge.title !== tip) bgbadge.title = tip;
      } else if (bgbadge.classList) {
        bgbadge.classList.remove("has-exec");
      }
    }
    taskBadge(monbadge, clive, "data-monitors", monTitle);
  }
  // --- agents island ---------------------------------------------------------
  // The streamed tree is authoritative; the compact HUD and full popover
  // project from it without moving tree nodes, preserving disclosure state.
  var agpop = $("agpop"), agpin = $("agpin"), agisland = $("agisland"), agcompact = $("agcompact"), agidle = $("agidle");
  var AGPIN_KEY = "ws.dashboard.agpop.pin", agFull = {}, agRunning = 0, agSeen = false, agDismissed = false;
  // agSize keeps the island's pixel size in step with its view. The streamed
  // tree swaps every second or so, so a routine update must not read layout:
  // a read right after the swap's DOM writes forces a synchronous layout of the
  // whole page. A ResizeObserver reports the sizes after the frame's own layout
  // instead. Its callback measures (agMeasure) and the compact view's numbers
  // stay cached (agBox) for the next call, which only compares and, when
  // something differs, writes. The idle and full views have no cheap
  // signature of their own beyond the mode, the idle label and the observed
  // sizes (agDone), so an update that changed none of them returns at once.
  var agRO = typeof ResizeObserver === "function" ? new ResizeObserver(agObserved) : null;
  var agWatched = [], agBox, agDone = "";
  // agObserved re-measures only for a size the current mode depends on: the
  // compact HUD's canvas and scroller, the full popover, or the column.
  function agObserved(entries) {
    if (!agisland) return;
    var mode = agisland.getAttribute("data-mode");
    var hit = entries.some(function (e) {
      var t = e.target;
      return t === $("convo") || (mode === "full" && t === agpop) || (mode === "compact" && t !== agpop);
    });
    if (!hit) return;
    agDone = ""; agBox = undefined;
    agMeasure();
  }
  function agWatch() {
    if (!agRO) return;
    [agcompact && agcompact.querySelector && agcompact.querySelector(".hud-canvas"),
      agcompact && agcompact.querySelector && agcompact.querySelector(".hud-tree-scroll"), agpop, $("convo")].forEach(function (el) {
      if (el && agWatched.indexOf(el) < 0) { agWatched.push(el); agRO.observe(el); }
    });
  }
  function agSize() {
    if (!agisland || agisland.hidden) return;
    var mode = agisland.getAttribute("data-mode");
    if (!agRO) { agMeasure(); return; }
    agWatch();
    if (mode === "compact") {
      // The cache is empty on entering the mode (the observer ignored the
      // canvas meanwhile); a zero box is a hidden island, which the observer
      // reports again once it shows. Clear agDone so re-opening full/idle
      // re-measures instead of keeping compact's dimensions.
      agDone = "";
      if (agBox) agApplyCompact(agBox); else agMeasure();
      return;
    }
    agBox = undefined;
    if (agSig(mode) !== agDone) agMeasure();
  }
  function agSig(mode) { return mode + "|" + (mode === "idle" && agidle ? agidle.textContent : ""); }
  // agMeasure reads the island's sizes in one pass, before any write, and
  // applies them. It forces layout, so it runs from the observer callback
  // (clean layout) or, without a ResizeObserver, from every update.
  function agMeasure() {
    if (!agisland || agisland.hidden) return;
    var mode = agisland.getAttribute("data-mode"), view = mode === "full" ? agpop : mode === "compact" ? agcompact : agidle;
    if (agisland.getClientRects && !agisland.getClientRects().length) {
      if (mode === "compact") agBox = { hidden: true }; else agDone = agSig(mode);
      return;
    }
    if (mode === "compact" && view) {
      // Measure intrinsic rest size from .hud-canvas + .hud-tree-scroll padding
      // without mutating agisland/agcompact styles or interrupting hover.
      var scroll = view.querySelector ? view.querySelector(".hud-tree-scroll") : null;
      var canvas = view.querySelector ? view.querySelector(".hud-canvas") : null;
      var cs = scroll && typeof getComputedStyle === "function" ? getComputedStyle(scroll) : null;
      var cRect = canvas && canvas.getBoundingClientRect ? canvas.getBoundingClientRect() : null;
      var convo = $("convo");
      agBox = {
        padX: cs ? (parseFloat(cs.paddingLeft) || 0) + (parseFloat(cs.paddingRight) || 0) : (scroll ? 16 : 0),
        padY: cs ? (parseFloat(cs.paddingTop) || 0) + (parseFloat(cs.paddingBottom) || 0) : (scroll ? 12 : 0),
        cw: canvas ? (cRect && cRect.width ? Math.ceil(cRect.width) : canvas.offsetWidth) : view.offsetWidth,
        ch: canvas ? (cRect && cRect.height ? Math.ceil(cRect.height) : canvas.offsetHeight) : view.offsetHeight,
        availW: convo && convo.getBoundingClientRect ? Math.floor(convo.getBoundingClientRect().width) - 16 : 0,
        sbH: scroll ? Math.max(0, (scroll.offsetHeight || 0) - (scroll.clientHeight || 0)) : 0
      };
      agApplyCompact(agBox);
      return;
    }
    // Measure at the destination width without painting the temporary auto
    // height. Restore the current interpolated box before setting pixel targets,
    // so both dimensions can transition even during a streaming update.
    agDone = agSig(mode);
    var box = agisland.getBoundingClientRect(), transition = agisland.style.transition;
    agisland.style.transition = "none";
    agisland.style.width = mode === "full" ? "500px" : "100%";
    agisland.style.height = "auto";
    var width = agisland.offsetWidth;
    if (mode === "idle") width = Math.min(width, view.offsetWidth);
    else agisland.style.setProperty("--ag-fw", width + "px");
    var height = view.offsetHeight;
    agisland.style.width = box.width + "px";
    agisland.style.height = box.height + "px";
    void agisland.offsetWidth;
    agisland.style.transition = transition;
    agisland.style.width = width + "px";
    agisland.style.height = height + "px";
  }
  function agApplyCompact(m) {
    if (m.hidden) return;
    var naturalW = m.cw + m.padX;
    var width = m.availW > 0 ? Math.min(naturalW, m.availW) : naturalW;
    var height = m.ch + m.padY + (naturalW > width ? m.sbH : 0);
    var wPx = width + "px", hPx = height + "px";
    var prevW = agisland.style.getPropertyValue ? agisland.style.getPropertyValue("--ag-cw") : agisland.style["--ag-cw"];
    var prevH = agisland.style.getPropertyValue ? agisland.style.getPropertyValue("--ag-ch") : agisland.style["--ag-ch"];
    if (prevW === wPx && prevH === hPx && agisland.style.width === wPx && agisland.style.height === hPx) return;
    if (prevW !== wPx) agisland.style.setProperty("--ag-cw", wPx);
    if (prevH !== hPx) agisland.style.setProperty("--ag-ch", hPx);
    if (agisland.style.width !== wPx) agisland.style.width = wPx;
    if (agisland.style.height !== hPx) agisland.style.height = hPx;
  }
  function agOpen(open) {
    if (!agpop) return;
    if (open && typeof hudHideTooltip === "function") hudHideTooltip();
    if (agpop.hidden !== !open) agpop.hidden = !open;
    if (agisland) {
      var nextHidden = !open && !agSeen;
      var nextMode = open ? "full" : agRunning ? "compact" : "idle";
      if (agisland.hidden !== nextHidden) agisland.hidden = nextHidden;
      if (agisland.getAttribute("data-mode") !== nextMode) agisland.setAttribute("data-mode", nextMode);
    }
    var expanded = open ? "true" : "false";
    [sbadge, agcompact, agidle].forEach(function (el) {
      if (el && el.getAttribute("aria-expanded") !== expanded) el.setAttribute("aria-expanded", expanded);
    });
    agSize();
    if (!open && agRunning && typeof hudScheduleConnectors === "function") hudScheduleConnectors();
  }
  function agPinned() { return agpin.getAttribute("aria-pressed") === "true"; }
  function agPin(on) {
    agpin.setAttribute("aria-pressed", on ? "true" : "false");
    save(AGPIN_KEY, on ? "1" : null);
  }
  function agProject() {
    if (!agisland || !agcompact || !agidle) return;
    var tree = region("tree");
    if (!tree) return;
    var info = hudProject(tree, agcompact, agidle, agpop, agOpen);
    var firstSeen = !agSeen && (info.visibleCount > 0 || q(".tgroup .agent", tree).length > 0);
    if (firstSeen) agSeen = true;
    agRunning = info.visibleCount;
    if (firstSeen && agPinned() && !agDismissed && agpop.hidden) agOpen(true);
    else if (agpop.hidden) agOpen(false); else agSize();
  }
  function agSwapped() {
    if (!agpop) return;
    q("details[data-key]", agpop).forEach(function (d) { d.classList.toggle("d-full", !!agFull[d.getAttribute("data-key")]); });
    agProject();
    syncBadge();
  }
  if (agpop && sbadge && agpin) {
    sbadge.addEventListener("click", function () { agDismissed = !agpop.hidden; agOpen(agpop.hidden); });
    if (agidle) {
      agidle.addEventListener("click", function () { agOpen(true); });
      agidle.addEventListener("keydown", function (e) { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); agOpen(true); } });
    }
    agpin.addEventListener("click", function () { agPin(!agPinned()); });
    $("agclose").addEventListener("click", function () { agPin(false); agOpen(false); sbadge.focus(); });
    agpop.addEventListener("click", function (e) {
      var b = e.target.closest && e.target.closest("[data-full]"), d = b && b.closest("details[data-key]");
      if (!d) return;
      agFull[d.getAttribute("data-key")] = true;
      d.classList.add("d-full");
      setTimeout(agSize, 0);
    });
    agpop.addEventListener("toggle", function () { setTimeout(agSize, 0); }, true);
    doc.addEventListener("keydown", function (e) {
      if (e.key !== "Escape" || e.defaultPrevented || agpop.hidden) return;
      var inside = agpop.contains(doc.activeElement);
      agDismissed = true;
      agOpen(false);
      if (inside) sbadge.focus();
    });
    doc.addEventListener("pointerdown", function (e) {
      if (agpop.hidden || agPinned() || (agisland || agpop).contains(e.target) || sbadge.contains(e.target)) return;
      agOpen(false);
    }, true);
    window.addEventListener("resize", function () { agDone = ""; agBox = undefined; agSize(); });
    if (agisland) agisland.hidden = true;
    if (load(AGPIN_KEY) === "1") agPin(true);
    agProject();
    if (!agisland && agPinned()) agOpen(true);
  }
  function interruptTurn() {
    if (interrupting) return;
    interrupting = true;
    interruptedTurn = leadTurn();
    syncBadge();
    post("interrupt", {}, function (ok, res) {
      if (!ok) { interrupting = false; syncBadge(); noteWarn(wsT("js.interrupt.fail", res)); return; }
      var list = res.held || [];
      if (!list.length) { noteOk(wsT("js.interrupt.ok")); return; }
      restoreHeld(list);
      noteOk(wsT("js.interrupt.restored"));
      ctext.focus();
    });
  }
  // steerHeld promotes a held message to steering: the session receives it
  // at its next tool call instead of when its turn ends. The convlive
  // region's next swap shows the steer marker in place of the button.
  function steerHeld(btn) {
    btn.disabled = true;
    post("steer", { id: btn.getAttribute("data-steer") }, function (ok, res) {
      if (!ok) { btn.disabled = false; noteWarn(wsT("js.steer.fail", res)); return; }
      noteOk(wsT("js.steer.ok"));
    });
  }
  var steerAllBtn = $("convo-steer-all-btn");
  var steerAllLbl = steerAllBtn && steerAllBtn.querySelector ? steerAllBtn.querySelector(".d-convo-steer-all-lbl") : null;
  var steeringAll = false;
  function syncSteerAll() {
    if (!steerAllBtn) return;
    var count = liveMode && doc.querySelectorAll ? doc.querySelectorAll("#r-convlive [data-steer]").length : 0;
    if (count < 1) {
      steeringAll = false;
      if (!steerAllBtn.hidden) steerAllBtn.hidden = true;
      if (steerAllBtn.disabled) steerAllBtn.disabled = false;
      return;
    }
    var label = count === 1 ? wsT("js.steer_all") : wsT("js.steer_all.count", String(count));
    if (steerAllLbl) {
      if (steerAllLbl.textContent !== label) steerAllLbl.textContent = label;
    } else if (steerAllBtn.textContent !== label) {
      steerAllBtn.textContent = label;
    }
    if (steerAllBtn.hidden) steerAllBtn.hidden = false;
    if (steerAllBtn.disabled !== steeringAll) steerAllBtn.disabled = steeringAll;
  }
  function steerAllHeld(btn) {
    if (!btn || btn.disabled || steeringAll) return;
    steeringAll = true;
    btn.disabled = true;
    var heldBtns = doc.querySelectorAll ? Array.prototype.slice.call(doc.querySelectorAll("#r-convlive [data-steer]")) : [];
    heldBtns.forEach(function (b) { b.disabled = true; });
    post("steer-all", {}, function (ok, res) {
      steeringAll = false;
      if (!ok) {
        btn.disabled = false;
        heldBtns.forEach(function (b) { b.disabled = false; });
        noteWarn(wsT("js.steer_all.fail", res));
        syncSteerAll();
        return;
      }
      if (res && res.count > 0) noteOk(wsT("js.steer_all.ok"));
      syncSteerAll();
    });
  }
  if (steerAllBtn && typeof MutationObserver === "function") {
    var steerAllLive = $("r-convlive");
    if (steerAllLive) new MutationObserver(function () { syncSteerAll(); }).observe(steerAllLive, { childList: true, subtree: true });
  }
  // applySent marks each board item the session was sent an answer for, or
  // holds one for, from the inbox records the conversation region carries,
  // and dims the item's answer area (sent-sent, sent-held) until the board
  // re-renders it answered; each run recomputes both from scratch. A run
  // over the same records as the last (sentSig) leaves the board alone unless
  // a board swap asks (force): putting back the same classes would only wake
  // the open-question pill's class observer.
  var sentSig = null;
  var sentLive = doc.getElementsByClassName ? doc.getElementsByClassName("sentdata") : null;
  var sentIndex = new Map();
  var sentIndexBoxes = new Map();
  function sentIndexAdd(rows, el) {
    if (el.tagName !== "I") return;
    var id = el.getAttribute("data-id");
    if (!id) return;
    var rec = {
      el: el, id: id,
      state: el.getAttribute("data-state") || "",
      answer: el.getAttribute("data-answer") || "",
      note: el.getAttribute("data-note") || ""
    };
    rows.set(el, rec);
    var list = sentIndex.get(id);
    if (!list) { list = []; sentIndex.set(id, list); }
    var after = list.findIndex(function (other) { return el.compareDocumentPosition(other.el) & 4; });
    if (after < 0) list.push(rec);
    else list.splice(after, 0, rec);
  }
  function sentIndexRemove(rows, el) {
    var rec = rows.get(el);
    if (!rec) return;
    rows.delete(el);
    var list = sentIndex.get(rec.id);
    list.splice(list.indexOf(rec), 1);
    if (!list.length) sentIndex.delete(rec.id);
  }
  function sentIndexApplyRecords(box, rows, records) {
    var changed = new Set();
    records.forEach(function (record) {
      if (record.type === "attributes") changed.add(record.target);
      else if (record.target === box) {
        record.removedNodes.forEach(function (el) { changed.add(el); });
        record.addedNodes.forEach(function (el) { changed.add(el); });
      }
    });
    changed.forEach(function (el) {
      sentIndexRemove(rows, el);
      if (el.parentNode === box) sentIndexAdd(rows, el);
    });
  }
  // Conversation swaps reconcile sent boxes here. The observers handle row
  // edits between swaps, so hydrating a topic only reads the index.
  function sentIndexRefresh() {
    var boxes = new Set(sentLive || q(".sentdata"));
    sentIndexBoxes.forEach(function (entry, box) {
      if (!boxes.has(box)) {
        if (entry.observer) entry.observer.disconnect();
        entry.rows.forEach(function (_, el) { sentIndexRemove(entry.rows, el); });
        sentIndexBoxes.delete(box);
      }
    });
    boxes.forEach(function (box) {
      var entry = sentIndexBoxes.get(box);
      if (!entry) {
        var rows = new Map();
        var observer = window.MutationObserver && new MutationObserver(function (records) {
          sentIndexApplyRecords(box, rows, records);
        });
        sentIndexBoxes.set(box, { rows: rows, observer: observer });
        if (observer) observer.observe(box, {
          childList: true, subtree: true, attributes: true,
          attributeFilter: ["data-id", "data-state", "data-answer", "data-note"]
        });
        Array.prototype.forEach.call(box.children, function (el) { sentIndexAdd(rows, el); });
      } else if (entry.observer) {
        sentIndexApplyRecords(box, entry.rows, entry.observer.takeRecords());
      } else {
        entry.rows.forEach(function (_, el) { sentIndexRemove(entry.rows, el); });
        Array.prototype.forEach.call(box.children, function (el) { sentIndexAdd(entry.rows, el); });
      }
    });
  }
  function applySent(force) {
    var scope = arguments[1];
    var b = region("board");
    if (!b) return;
    function appendMark(item, head, rec) {
      var state = rec.state, m = doc.createElement("span");
      item.classList.add(state === "held" ? "sent-held" : "sent-sent");
      m.className = "sentmark " + state;
      m.textContent = state + " · " + (rec.answer || "note");
      m.title = wsT(state === "held" ? "js.sent.held.title" : "js.sent.sent.title") + (rec.note ? ": " + rec.note : "");
      var idEl = head.querySelector(".id");
      head.insertBefore(m, idEl ? idEl.nextSibling : head.firstChild);
    }
    var index;
    if (typeof sentIndexRefresh === "function") {
      if (!scope) sentIndexRefresh();
      index = sentIndex;
    } else {
      index = new Map();
      q(".sentdata i[data-id]").forEach(function (n) {
        var id = n.getAttribute("data-id");
        if (!id) return;
        var list = index.get(id);
        if (!list) { list = []; index.set(id, list); }
        list.push({ state: n.getAttribute("data-state") || "sent", answer: n.getAttribute("data-answer") || "", note: n.getAttribute("data-note") || "" });
      });
    }
    if (scope && scope.querySelectorAll) {
      q(".sentmark", scope).forEach(function (m) { if (m.parentNode) m.parentNode.removeChild(m); });
      q(".sent-sent, .sent-held", scope).forEach(function (el) { el.classList.remove("sent-sent", "sent-held"); });
      if (!index.size) return;
      var scopedItems = new Map();
      q(".card[data-id], .arow[data-id], .ckrow[data-id], .srow[data-id]", scope).forEach(function (item) {
        var id = item.getAttribute("data-id");
        if (id && !scopedItems.has(id)) scopedItems.set(id, item);
      });
      if (!scopedItems.size) return;
      scopedItems.forEach(function (item, id) {
        var recs = index.get(id);
        if (!recs || !recs.length) return;
        var head = item.querySelector(".head, .stext");
        if (!head) return;
        for (var r = 0; r < recs.length; r++) appendMark(item, head, recs[r]);
      });
      if (typeof syncWbUpdateItems === "function") syncWbUpdateItems();
      return;
    }
    var sigParts = [];
    index.forEach(function (recs, id) {
      for (var r = 0; r < recs.length; r++) {
        sigParts.push([id, recs[r].state, recs[r].answer, recs[r].note].join("\u0001"));
      }
    });
    var sig = sigParts.join("\u0002");
    if (!force && sig === sentSig) return;
    sentSig = sig;
    boardQ(".sentmark").forEach(function (m) { m.parentNode.removeChild(m); });
    boardQ(".sent-sent, .sent-held").forEach(function (el) { el.classList.remove("sent-sent", "sent-held"); });
    index.forEach(function (recs, id) {
      var item = boardItem(id), head = item && item.querySelector(".head, .stext");
      if (!head) return;
      for (var r = 0; r < recs.length; r++) appendMark(item, head, recs[r]);
    });
    if (typeof syncWbUpdateItems === "function") syncWbUpdateItems();
  }
