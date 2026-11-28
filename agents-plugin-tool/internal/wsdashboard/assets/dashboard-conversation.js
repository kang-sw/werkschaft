  // --- conversation entries: merged by key ----------------------------------
  // Each entry element carries data-entry, its ring entry name; names sort in
  // time order. Only the entries container is searched: held bubbles in the
  // convlive region carry data-entry too (their inbox id) and are not
  // entries. A box keeps the names it holds (entryKeys) across merges, so a
  // merge reads only the incoming entries.
  function entriesBox() { var r = region("entries"); return r && r.querySelector(".centries"); }
  function frag(html) { var t = doc.createElement("template"); t.innerHTML = html; return t.content; }
  function entryKeys(box) {
    if (!box.wsKeys) {
      box.wsKeys = Object.create(null);
      Array.prototype.forEach.call(box.children, function (c) { var k = c.getAttribute("data-entry"); if (k) box.wsKeys[k] = c; });
    }
    return box.wsKeys;
  }
  function firstEntryEl(box) {
    var c = box && box.firstElementChild;
    while (c && (!c.getAttribute || !c.getAttribute("data-entry"))) c = c.nextSibling;
    return c || null;
  }
  function lastEntryEl(box) {
    var c = box && box.lastElementChild;
    while (c && (!c.getAttribute || !c.getAttribute("data-entry"))) c = c.previousElementSibling;
    return c || null;
  }
  function emitAuditEvent(name, detail) {
    if (typeof CustomEvent === "function" && doc && typeof doc.dispatchEvent === "function") {
      doc.dispatchEvent(new CustomEvent(name, { detail: detail || {} }));
    }
  }
  function resetEntries(box) { box.textContent = ""; box.wsKeys = Object.create(null); }
  // One completed turn is one summary. The server anchors a completed turn's
  // summary (data-turn-key = the closing turn entry) at the first folded entry
  // it can still see, so a newest window or history page that truncates the
  // turn's head re-anchors it at a later entry. The box keeps the earliest
  // anchor (the fullest counts) and turns every other row of the turn,
  // including the closing .turnline footer, into a hidden absorbed marker.
  function turnSummaryEl(box, tk, except) {
    var all = box.querySelectorAll("[data-turn-key]");
    for (var i = 0; i < all.length; i++) {
      if (all[i] !== except && all[i].getAttribute("data-turn-key") === tk) return all[i];
    }
    return null;
  }
  function absorbedMarker(n, k) {
    var m = doc.createElement("span"), at = n.getAttribute("data-at");
    m.setAttribute("hidden", ""); m.setAttribute("data-entry", k); m.setAttribute("data-turn-absorbed", "1");
    if (at) m.setAttribute("data-at", at);
    return m;
  }
  // dedupTurn returns the node to merge for incoming n: n itself, or its
  // absorbed marker when the box already shows the turn elsewhere. It retires
  // a later anchor or a visible footer the incoming summary supersedes.
  function dedupTurn(box, have, n, k) {
    var tk = n.getAttribute("data-turn-key"), cur = have[k], foot;
    if (tk) {
      if (cur && cur.getAttribute("data-turn-key") === tk && cur.getAttribute("data-turn-rev") === n.getAttribute("data-turn-rev")) return n;
      var rival = turnSummaryEl(box, tk, cur);
      if (rival) {
        var rk = rival.getAttribute("data-entry");
        if (rk < k) return absorbedMarker(n, k);
        var rm = absorbedMarker(rival, rk);
        box.replaceChild(rm, rival); have[rk] = rm;
      }
      foot = have[tk];
      if (foot && foot.matches(".turnline")) {
        var mk = absorbedMarker(foot, tk);
        box.replaceChild(mk, foot); have[tk] = mk;
      } else if (foot && foot.matches(".turn-with-summary")) {
        var fl = foot.querySelector(".turnline");
        if (fl) fl.parentNode.removeChild(fl);
      }
    } else if (cur && !cur.hasAttribute("data-turn-absorbed")) {
      return n;
    } else if (n.matches(".turnline") && turnSummaryEl(box, k)) {
      return absorbedMarker(n, k);
    } else if (n.matches(".turn-with-summary") && turnSummaryEl(box, k)) {
      var nl = n.querySelector(".turnline");
      if (nl) nl.parentNode.removeChild(nl);
    }
    return n;
  }
  // mergeEntries inserts each node of src whose name box lacks at its sorted
  // place and returns inserted/replaced nodes, in order. Existing entries
  // stay put unless their mail-wake presentation changes: a hidden mail
  // marker must replace its row (and restore it when the association expires).
  function mergeEntries(box, src, changed) {
    var added = [];
    var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("convo", "mergeEntries") : null;
    try {
      var have = entryKeys(box);
      Array.prototype.slice.call(src.children).forEach(function (n) {
        var k = n.getAttribute("data-entry");
        if (!k) return;
        n = dedupTurn(box, have, n, k);
        var old = have[k];
        if (old) {
          if (!(changed && changed[k]) &&
              old.getAttribute("data-mail-wake") === n.getAttribute("data-mail-wake") &&
              old.getAttribute("data-turn-absorbed") === n.getAttribute("data-turn-absorbed") &&
              old.getAttribute("data-turn-live-absorbed") === n.getAttribute("data-turn-live-absorbed") &&
              old.getAttribute("data-conv-turn") === n.getAttribute("data-conv-turn") &&
              old.getAttribute("data-turn-rev") === n.getAttribute("data-turn-rev")) return;
          box.replaceChild(n, old);
        } else {
          // New entries are usually the newest (live) or the oldest (history),
          // so walk back from the end strictly over [data-entry] siblings.
          var c = box.lastElementChild;
          while (c && (!c.getAttribute || !c.getAttribute("data-entry"))) c = c.previousElementSibling;
          while (c && c.getAttribute("data-entry") > k) {
            var prev = c.previousElementSibling;
            while (prev && (!prev.getAttribute || !prev.getAttribute("data-entry"))) prev = prev.previousElementSibling;
            c = prev;
          }
          box.insertBefore(n, c ? c.nextSibling : box.firstChild);
        }
        have[k] = n;
        added.push(n);
        restoreOpen(n);
        fmtTimes(n);
        if (typeof hydrateWbUpdateItems === "function") hydrateWbUpdateItems(n);
      });
      return added;
    } finally {
      if (jTok) WsJank.end(jTok, { count: added.length });
    }
  }
  // gapped reports a newest window (nbox) that does not reach what box
  // holds: its oldest entry is newer than box's newest, so entries between
  // them, missed while a stream was down, are in neither.
  function gapped(box, nbox) {
    var last = box && box.lastElementChild;
    while (last && (!last.getAttribute || !last.getAttribute("data-entry"))) last = last.previousElementSibling;
    var first = nbox && nbox.firstElementChild;
    while (first && (!first.getAttribute || !first.getAttribute("data-entry"))) first = first.nextSibling;
    return !!(last && first && first.getAttribute("data-entry") > last.getAttribute("data-entry"));
  }
  // ENTRY_CAP bounds a conversation box while the user keeps it at the
  // bottom: three of the server's newest windows (its ring size, 50).
  // evictEntries drops the oldest entries past it and reports whether it
  // dropped any, removing any preceding non-entry nodes before the new oldest entry.
  var ENTRY_CAP = 150;
  function evictEntries(box) {
    var keys = entryKeys(box), n = Object.keys(keys).length - ENTRY_CAP;
    if (n <= 0) return false;
    for (; n > 0; n--) {
      var c = box.firstElementChild;
      while (c && (!c.getAttribute || !c.getAttribute("data-entry"))) c = c.nextSibling;
      if (!c) break;
      while (c.previousElementSibling) box.removeChild(c.previousElementSibling);
      delete keys[c.getAttribute("data-entry")];
      box.removeChild(c);
    }
    var oldest = box.firstElementChild;
    while (oldest && (!oldest.getAttribute || !oldest.getAttribute("data-entry"))) oldest = oldest.nextSibling;
    if (oldest) {
      while (oldest.previousElementSibling) box.removeChild(oldest.previousElementSibling);
    }
    return true;
  }
  // Canonical transport inputs never come from the formatted, history-extended
  // live DOM. A full event establishes the shell and newest window; a delta
  // must prove the same order and replace only complete single keyed rows.
  var ENTRY_SLOT = /<!--ws-entry-row-(\d+)-->/g;
  function entryInputFull(html) {
    if (typeof html !== "string" || html.indexOf("<!--ws-entry-row-") >= 0) return null;
    var src = frag(html), boxes = src.querySelectorAll(".centries");
    if (boxes.length !== 1) return null;
    var box = boxes[0], rows = Object.create(null), order = [];
    if (Array.from(box.childNodes).some(function (n) { return n.nodeType !== 1 && (n.nodeType !== 3 || n.textContent.trim()); })) return null;
    if (Array.from(box.children).some(function (n) {
      var key = n.getAttribute("data-entry");
      if (!key || rows[key]) return true;
      rows[key] = { html: n.outerHTML }; order.push(key);
      n.replaceWith(doc.createComment("ws-entry-row-" + (order.length - 1)));
      return false;
    })) return null;
    var holder = doc.createElement("div"); holder.appendChild(src);
    return { shell: holder.innerHTML, order: order, rows: rows };
  }
  function entryRowNode(html, key) {
    if (typeof html !== "string" || html.indexOf("<!--ws-entry-row-") >= 0) return null;
    var src = frag(html), n = src.firstElementChild;
    return src.childNodes.length === 1 && n && n.getAttribute("data-entry") === key && !n.matches(".centries") && !n.querySelector(".centries") ? n : null;
  }
  function entryInputPatch(base, ev) { return keyedInputPatch(base, ev, entryRowNode); }
  function entryInputHTML(input) { return keyedInputHTML(input, ENTRY_SLOT); }

  // mergeEntriesRegion applies an entries region event: the title bar (its
  // counts and model-history source) is synced, and the entries are merged. A window
  // past a gap starts the box over; a box at the bottom (pin) sheds its oldest
  // entries past the cap. Either way the older history is loaded again from
  // what the box then holds.
  function mergeEntriesRegion(el, html, pin, previous) {
    var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("convo", "mergeEntriesRegion") : null;
    try {
      var next = typeof html === "string" ? null : html, box = entriesBox(), changed = Object.create(null);
      // Without a rendered baseline, the differing full's same-key rows may
      // have changed since page render (or an unparseable full). The swap owner
      // already bypasses this merge for equal hashes, preserving no-remount.
      if (next) next.order.forEach(function (key) {
        if (!previous || (previous.rows[key] && previous.rows[key].html !== next.rows[key].html)) changed[key] = true;
      });
      if (box && next && previous && next.shell === previous.shell && keyedSame(next.order, previous.order)) {
        var incoming = doc.createElement("div"), have = entryKeys(box);
        next.order.forEach(function (key) {
          if (!have[key] || changed[key]) incoming.appendChild(entryRowNode(next.rows[key].html, key));
        });
        var inserted = mergeEntries(box, incoming, changed);
        inserted.forEach(sweepOutImgs); mermaid(inserted);
        if (pin && evictEntries(box)) histReset();
        var oldestA = firstEntryEl(box);
        emitAuditEvent("ws-entries-merged", { oldestAt: oldestA ? (oldestA.getAttribute("data-at") || "") : "", empty: !oldestA });
        return;
      }
      html = next ? entryInputHTML(next) : html;
      var src = frag(html), head = el.querySelector(".thead"), nhead = src.querySelector(".thead");
      var nbox = src.querySelector(".centries");
      if (!box || !nbox) {
        el.innerHTML = html; restoreOpen(el); fmtTimes(el); sweepOutImgs(el); mermaid(el); if (typeof hydrateWbUpdateItems === "function") hydrateWbUpdateItems(el); histReset();
        var freshBox = entriesBox(), freshOld = firstEntryEl(freshBox);
        emitAuditEvent("ws-entries-merged", { oldestAt: freshOld ? (freshOld.getAttribute("data-at") || "") : "", empty: !freshOld });
        return;
      }
      if (head && nhead) { head.parentNode.replaceChild(nhead, head); fmtTimes(nhead); }
      if (gapped(box, nbox)) { resetEntries(box); histReset(); }
      var added = mergeEntries(box, nbox, changed);
      added.forEach(sweepOutImgs);
      mermaid(added);
      if (pin && evictEntries(box)) histReset();
      var oldestB = firstEntryEl(box);
      emitAuditEvent("ws-entries-merged", { oldestAt: oldestB ? (oldestB.getAttribute("data-at") || "") : "", empty: !oldestB });
    } finally {
      if (jTok) WsJank.end(jTok);
    }
  }

  // Model history is a click-only popover sourced by the entries title bar.
  // The popover lives outside the swapped region; syncChead calls
  // modelHistorySync after each entries refresh so an open popover keeps its
  // state while its source and anchor are replaced.
  var modelHistoryPop = null, modelHistoryOpen = false;
  function modelHistoryTrigger() { return doc.querySelector(".modelhistory-trigger"); }
  function modelHistorySource(btn) {
    return btn && btn.parentNode && btn.parentNode.querySelector("template.modelhistory-source");
  }
  function modelHistoryPlace() {
    var btn = modelHistoryTrigger();
    if (!modelHistoryPop || modelHistoryPop.hidden || !btn || !window.WsRail) return;
    var a = btn.getBoundingClientRect();
    var at = window.WsRail.popPlace(a, modelHistoryPop.offsetWidth, modelHistoryPop.offsetHeight,
      root.clientWidth || window.innerWidth, root.clientHeight || window.innerHeight);
    modelHistoryPop.style.top = at.top + "px";
    modelHistoryPop.style.left = at.left + "px";
  }
  function modelHistoryClose(refocus) {
    modelHistoryOpen = false;
    if (modelHistoryPop) modelHistoryPop.hidden = true;
    var btn = modelHistoryTrigger();
    if (btn) {
      btn.setAttribute("aria-expanded", "false");
      if (refocus) btn.focus();
    }
  }
  function modelHistorySync() {
    var btn = modelHistoryTrigger(), src = modelHistorySource(btn);
    if (!btn || !src) {
      if (modelHistoryPop) modelHistoryPop.hidden = true;
      return;
    }
    btn.setAttribute("aria-expanded", String(!!modelHistoryOpen));
    if (!modelHistoryOpen) return;
    if (!modelHistoryPop) {
      modelHistoryPop = doc.createElement("div");
      modelHistoryPop.id = "modelhistory-popover";
      modelHistoryPop.className = "rpop modelhistory-popover";
      modelHistoryPop.setAttribute("role", "dialog");
      modelHistoryPop.setAttribute("aria-label", btn.getAttribute("aria-label") || "");
      doc.body.appendChild(modelHistoryPop);
    }
    modelHistoryPop.innerHTML = src.innerHTML;
    modelHistoryPop.hidden = false;
    fmtTimes(modelHistoryPop);
    modelHistoryPlace();
  }
  function modelHistoryToggle(btn) {
    if (modelHistoryOpen) { modelHistoryClose(true); return; }
    modelHistoryOpen = true;
    btn.setAttribute("aria-expanded", "true");
    modelHistorySync();
  }
  doc.addEventListener("click", function (e) {
    var btn = e.target && e.target.closest && e.target.closest(".modelhistory-trigger");
    if (btn) { e.preventDefault(); modelHistoryToggle(btn); return; }
    if (modelHistoryOpen && (!modelHistoryPop || !modelHistoryPop.contains(e.target))) modelHistoryClose(false);
  }, true);
  window.addEventListener("keydown", function (e) {
    if (e.key !== "Escape" || !modelHistoryOpen) return;
    e.preventDefault();
    e.stopPropagation();
    modelHistoryClose(true);
  }, true);
  window.addEventListener("resize", modelHistoryPlace);
  doc.addEventListener("scroll", modelHistoryPlace, true);

  // Conversation cards navigate only this session's board and validated IDs.
  // The existing card/filter and history owners retain all geometry decisions.
  var effectJumpEpoch = 0;
  function effectTopic(id) {
    if (typeof id !== "string" || !/^topic-(?:[0-9a-f]{2})+$/.test(id)) return null;
    return boardQ("section.topic[data-topic]").filter(function (n) { return n.id === id && inBoard(n); })[0] || null;
  }
  function effectJump(topicID, id, done) {
    if (typeof topicID !== "string" || !/^topic-(?:[0-9a-f]{2})+$/.test(topicID) ||
        typeof id !== "string" || (id !== topicID && !/^(?:(?:item|check)-[1-9][0-9]{0,5}|[ep][1-9][0-9]*)$/.test(id))) return;
    var epoch = ++effectJumpEpoch, seen = Object.create(null), waited = false;
    if (doc.body.classList.contains("tk-paged") && tkApi) tkApi.leavePage();
    if (grid && !tabShown("board")) setTab("board");
    function result(topic, exact) { if (epoch === effectJumpEpoch && done) done(topic, exact); }
    function step() {
      if (epoch !== effectJumpEpoch) return;
      if (window.wbPill && window.wbPill.apply && !window.wbPill.apply(step)) return;
      var topic = effectTopic(topicID);
      if (!topic) {
        // Closed topics already have a paginated route. Do not invent a new
        // anchor/store API or search another session when a target is absent.
        var more = boardQ("button.closed-more[data-closed-next]")[0];
        var cursor = more && more.getAttribute("data-closed-next");
        if (!more || more.disabled || seen[cursor]) { result(false, false); return; }
        seen[cursor] = true;
        var pending = loadClosed(more);
        if (!pending || !pending.then) { result(false, false); return; }
        pending.then(function (loaded) { if (loaded) step(); else result(false, false); });
        return;
      }
      var cards = window.WsCards, unfold = cards && cards.unfoldTopic;
      function reveal() {
        if (epoch !== effectJumpEpoch) return;
        // SSE may have replaced the topic while overview/filter restore ran.
        if (effectTopic(topicID) !== topic) { step(); return; }
        // Hydrate collapsed far topics before target lookup or lazy-gap loading
        // so stashed revisions, live rows, and inert history gaps exist in DOM.
        if (window.WsBoardAnchor && window.WsBoardAnchor.hydrate) {
          window.WsBoardAnchor.hydrate(topic, { immediate: true });
        }
        var target = topic, loaded = false;
        if (id !== topicID) {
          var alternate = id.replace(/^(item|check)-/, function (s) { return s === "item-" ? "check-" : "item-"; });
          if (window.wbHistory && window.wbHistory.loadTarget) target = window.wbHistory.loadTarget(topic, id, unfold) || (alternate !== id && window.wbHistory.loadTarget(topic, alternate, unfold));
          else target = /^item-/.test(id) ? topic.querySelector('[id="' + id + '"]') : null;
          loaded = !!target && !!(window.wbHistory && window.wbHistory.loadTarget);
          if (!target && alternate !== id) target = topic.querySelector('[id="' + alternate + '"]');
          if (!target || (target.id !== id && target.id !== alternate) || !inBoard(target) || target.closest("section.topic[data-topic]") !== topic ||
              (/^(?:item|check)-/.test(id) && !target.matches(".card.policy[data-id], .arow[data-id], .ckrow[data-id], .srow"))) target = null;
          if (!target) loaded = false;
          // A target in history the page has not fetched yet: decide once
          // its topic arrives.
          var waiting = !target && !waited && window.wbHistory && window.wbHistory.loading &&
            [window.wbHistory.loading(id), alternate !== id && window.wbHistory.loading(alternate)].filter(Boolean);
          if (waiting && waiting.length) { waited = true; Promise.all(waiting).then(reveal); return; }
        }
        var exact = !!target;
        if (!target) target = topic;
        if (unfold) unfold(target);
        // A target on a docked page not shown (or, in the narrow view, on
        // the Main Board while a docked page shows) has its page shown first.
        for (var p = target; p && inBoard(p); p = p.parentNode) if (p.tagName === "DETAILS") p.open = true;
        if (!loaded && window.wbHistory && window.wbHistory.navigate) window.wbHistory.navigate(target);
        else if (!loaded && cards && cards.activate) cards.activate(target);
        // loadTarget owns lazy-history positioning, including version folds.
        if (!target.closest(".wb-history")) {
          if (!loaded && (!window.wbHistory || !window.wbHistory.navigate)) {
            var anchor = window.WsBoardAnchor, card = anchor && anchor.owner && anchor.owner(target);
            if (card) anchor.select(card, { smooth: target === card });
            if (target !== card) {
              target.scrollIntoView({ block: "start" });
              if (card && anchor.land) anchor.land(target);
            }
          }
          if (window.wbHistory && window.wbHistory.keep) window.wbHistory.keep(target);
          // Entry/item flashes belong to loadTarget; topic-only navigation
          // (including an absent element's fallback) uses the existing pulse.
          if (target === topic || !window.wbHistory || !window.wbHistory.loadTarget) {
            target.classList.remove("palette-flash");
            void target.offsetWidth;
            target.classList.add("palette-flash");
            setTimeout(function () { target.classList.remove("palette-flash"); }, 1600);
          }
        }
        target.setAttribute("tabindex", "-1");
        target.focus({ preventScroll: true });
        result(true, exact);
      }
      if (cards && cards.revealTopic) cards.revealTopic(topic, reveal); else reveal();
    }
    step();
  }
  function effectJumpTo(card, id) {
    effectJump(card.getAttribute("data-wb-topic"), id, function (found, exact) {
      var status = card.querySelector(".wbupdate-status");
      if (status) status.textContent = !found ? wsT("js.whiteboard.jump.missing") : !exact ? wsT("js.whiteboard.jump.elementmissing") : "";
    });
  }
  function effectJumpClick(e) {
    var t = e.target, box = entriesBox();
    if (!t || !t.closest || !box || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    if (t.closest("[data-wb-nav]")) return;
    var card = t.closest(".wbupdate-topic[data-wb-topic]");
    if (!card || !box.contains(card)) return;
    var link = t.closest("a[data-wb-jump]");
    if (!link && t.closest("a, summary, button, input, textarea, select")) return;
    var selection = window.getSelection && window.getSelection();
    if (!link && selection && !selection.isCollapsed) return;
    e.preventDefault();
    var row = t.closest("[data-wb-element]");
    if (row && card.contains && !card.contains(row)) row = null;
    var id = link ? link.getAttribute("data-wb-jump") : row ? row.getAttribute("data-wb-element") : card.getAttribute("data-wb-topic");
    effectJumpTo(card, id);
  }
  function effectJumpKey(e) {
    if ((e.key !== "Enter" && e.key !== " ") || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var t = e.target, box = entriesBox();
    if (!t || !t.closest || !box) return;
    if (t.closest("[data-wb-nav], a, summary, button, input, textarea, select")) return;
    var row = t.closest(".wbupdate-row[data-wb-element]");
    var card = row && row.closest(".wbupdate-topic[data-wb-topic]");
    if (!row || !card || !box.contains(card)) return;
    e.preventDefault();
    effectJumpTo(card, row.getAttribute("data-wb-element"));
  }
  doc.addEventListener("click", effectJumpClick);
  doc.addEventListener("keydown", effectJumpKey);

  // User chips and references in committed previews share the card jump owner.
  // Delegation also covers held bubbles and subsequently paged conversation rows.
  function conversationBoardActivate(e) {
    if (e.type === "keydown" ? e.key !== "Enter" : e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var link = e.target && e.target.closest && e.target.closest("[data-wb-nav]");
    var box = entriesBox(), held = region("convlive"), pop = typeof icapsulePop !== "undefined" ? icapsulePop : null;
    if (!link || !((box && box.contains(link)) || (held && held.contains(link)) || (pop && pop.contains(link)))) return;
    var selection = window.getSelection && window.getSelection();
    if (e.type !== "keydown" && selection && !selection.isCollapsed) return;
    e.preventDefault();
    var topic = link.getAttribute("data-wb-topic"), target = link.getAttribute("data-wb-nav");
    if (!topic) { toast(wsT("js.whiteboard.jump.itemmissing")); return; }
    if (typeof icapsuleClose === "function" && pop && pop.contains(link)) icapsuleClose(false);
    effectJump(topic, target, function (found, exact) {
      if (!found || !exact) toast(wsT("js.whiteboard.jump.itemmissing"));
    });
  }
  doc.addEventListener("click", conversationBoardActivate);
  doc.addEventListener("keydown", conversationBoardActivate);

  // --- chat block actions (msgacts, dashboard.tmpl) ---------------------------
  // Copy fetches the block's stored text from the entry text route
  // (entry_text_route.go) and copies it quietly: the button confirms
  // (data-done, with its pill pinned) for 1.6 s, so no toast doubles it. A
  // batch's bubbles share one entry key; the route answers one text per
  // bubble, in order, and the bubble's index picks its own. Where the async
  // Clipboard API takes a pending item, the write starts inside the click,
  // so a browser that ties clipboard writes to the gesture still allows it
  // after the fetch; elsewhere the fetched text goes through copyText. A
  // disabled action (rewind, edit) only shows its tooltip.
  var ACT_CONFIRM_MS = 1600;
  function entryText(msg) {
    var holder = msg.closest("[data-entry]"), key = holder && holder.getAttribute("data-entry");
    if (!key || !window.fetch) return Promise.reject(new Error("no entry"));
    var index = holder === msg ? 0 : Array.prototype.indexOf.call(holder.querySelectorAll(".msg.user"), msg);
    return fetch("/s/" + rid + "/entry?key=" + encodeURIComponent(key), { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    }).then(function (ans) {
      var text = ans && ans.blocks && ans.blocks[index];
      if (typeof text !== "string") throw new Error("no text");
      return text;
    });
  }
  function copyEntry(msg) {
    var text = entryText(msg);
    function viaCopyText() { return text.then(function (s) { return copyText(s, true); }); }
    if (window.ClipboardItem && window.isSecureContext && navigator.clipboard && navigator.clipboard.write) {
      var item = null;
      try { item = new ClipboardItem({ "text/plain": text.then(function (s) { return new Blob([s], { type: "text/plain" }); }) }); } catch (e) { item = null; }
      if (item) return navigator.clipboard.write([item]).then(function () { return true; }, viaCopyText);
    }
    return viaCopyText();
  }
  // actConfirm shows b's tooltip (a disabled action), or its done state with
  // its pill pinned (a copy), for ACT_CONFIRM_MS.
  function actConfirm(b, done) {
    var bar = b.parentNode;
    clearTimeout(b.wsActTimer);
    b.setAttribute(done ? "data-done" : "data-tipshow", "");
    if (done) bar.setAttribute("data-pinned", "");
    b.wsActTimer = setTimeout(function () {
      b.removeAttribute("data-done");
      b.removeAttribute("data-tipshow");
      bar.removeAttribute("data-pinned");
    }, ACT_CONFIRM_MS);
  }
  function msgActClick(e) {
    var t = e.target, box = entriesBox();
    var b = t && t.closest ? t.closest(".d-act") : null;
    if (!b || !box || !box.contains(b)) return;
    e.preventDefault();
    if (b.getAttribute("aria-disabled") === "true") { actConfirm(b, false); return; }
    if (b.getAttribute("data-act") !== "copy" || b.wsActBusy) return;
    b.wsActBusy = true;
    copyEntry(b.closest(".msg")).then(function (ok) {
      b.wsActBusy = false;
      if (!ok) throw new Error("copy refused");
      actConfirm(b, true);
    }).catch(function () {
      b.wsActBusy = false;
      toast(wsT("js.entry.copyfail"));
    });
  }
  doc.addEventListener("click", msgActClick);

  // --- older history: loaded a page at a time when scrolled to the top -------
  // The region draws the newest entries; older ones come from the history
  // route and are prepended with the scroll position kept. histBefore is the
  // next page's cursor ("" asks for the newest page, so a page that drew no
  // entries can still start loading); null takes it from the box's oldest
  // entry. histReset forgets the cursor and the end once the box's oldest
  // entry changed under it (evicted, or the box started over), and its epoch
  // drops a page still in flight, which would no longer join what the box
  // holds.
  var histBefore = null, histEnd = false, histBusy = false, histEpoch = 0, histPromise = null;
  function histReset() { histBefore = null; histEnd = false; histBusy = false; histPromise = null; histEpoch++; emitAuditEvent("ws-history-reset", {}); }
  function fetchHistoryPage(opts) {
    opts = opts || {};
    if (histEnd || !window.fetch || !convo || (!opts.force && !visible(convo))) return Promise.resolve({ end: !!histEnd, skipped: true });
    if (histBusy) return histPromise || Promise.resolve({ end: !!histEnd, skipped: true });
    var box = entriesBox();
    if (!box) return Promise.resolve({ end: true, skipped: true });
    var prevOldest = firstEntryEl(box), prevOldestAt = prevOldest ? prevOldest.getAttribute("data-at") || "" : "";
    if (histBefore === null) {
      histBefore = prevOldest ? prevOldest.getAttribute("data-entry") || "" : "";
    }
    histBusy = true;
    var epoch = histEpoch;
    var url = "/s/" + rid + "/history?n=50" + (histBefore ? "&before=" + encodeURIComponent(histBefore) : "");
    var p = fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    }).then(function (res) {
      if (epoch !== histEpoch) return { aborted: true, end: !!histEnd };
      histBusy = false;
      histPromise = null;
      var h = convo.scrollHeight, top = convo.scrollTop;
      var added = mergeEntries(box, frag(res.html || ""));
      added.forEach(sweepOutImgs);
      // Pagination supersedes any WS restoration queued before this fetch
      // completed; its compensated reading position is now authoritative.
      scrollRestore.delete(convo);
      // Keep what the user was reading in place: the prepended height goes
      // above the scroll offset.
      var nextTop = top + (convo.scrollHeight - h);
      if (typeof setConvoScrollTop === "function") setConvoScrollTop(nextTop);
      else convo.scrollTop = nextTop;
      mermaid(added);
      if (res.before) histBefore = res.before;
      if (res.end || !res.before) histEnd = true;
      var nextOldest = firstEntryEl(box), nextOldestAt = nextOldest ? nextOldest.getAttribute("data-at") || "" : "";
      emitAuditEvent("ws-history-page", { prevOldestAt: prevOldestAt, oldestAt: nextOldestAt, end: !!histEnd, empty: !nextOldest });
      if (!opts.skipNearTop) nearTop();
      return { added: added, end: !!histEnd };
    }, function () {
      // A failed fetch (daemon replaced, network) is retried on the next
      // scroll to the top, after a pause.
      if (epoch === histEpoch) {
        histPromise = null;
        setTimeout(function () { if (epoch === histEpoch) histBusy = false; }, 3000);
      }
      return { error: true, end: !!histEnd };
    });
    histPromise = p;
    return p;
  }
  function loadHistory() { fetchHistoryPage(); }
  // nearTop loads older entries when the conversation is scrolled to (or
  // too short to scroll away from) its top.
  function nearTop() { if (convo && visible(convo) && convo.scrollTop < 160) loadHistory(); }
  if (convo) convo.addEventListener("scroll", nearTop);

  // --- #entry=<key> jump: paginate up to 5 pages, highlight, or show notice --
  var ENTRY_JUMP_MAX_PAGES = 5, ENTRY_FLASH_MS = 1800, ENTRY_NOTICE_MS = 4500;
  var entryJumpEpoch = 0, entryJumpPending = "", entryNoticeEl = null, entryNoticeTimer = 0;
  function parseEntryHash(h) {
    var raw = String(h || "");
    if (raw.charAt(0) === "#") raw = raw.slice(1);
    var m = /(?:^|&)entry=([^&]+)/.exec(raw);
    if (!m) return "";
    try { return decodeURIComponent(m[1]).trim(); } catch (_) { return m[1].trim(); }
  }
  function entryJumpNotice(text) {
    if (!convo) return;
    if (!entryNoticeEl || !entryNoticeEl.isConnected) {
      var wrap = doc.createElement("div");
      wrap.className = "conv-entry-notice-wrap";
      var pill = doc.createElement("div");
      pill.className = "conv-entry-notice";
      pill.setAttribute("role", "status");
      pill.setAttribute("aria-live", "polite");
      pill.hidden = true;
      wrap.appendChild(pill);
      convo.insertBefore(wrap, convo.firstChild);
      entryNoticeEl = pill;
    }
    entryNoticeEl.textContent = text;
    entryNoticeEl.hidden = false;
    if (entryNoticeTimer) clearTimeout(entryNoticeTimer);
    entryNoticeTimer = setTimeout(function () {
      entryNoticeTimer = 0;
      if (entryNoticeEl) entryNoticeEl.hidden = true;
    }, ENTRY_NOTICE_MS);
  }
  function entryJumpRevealTarget(box, el, key) {
    var target = el;
    if (el.getAttribute && el.getAttribute("data-wb-folded") === "1") {
      var turns = box.querySelectorAll("[data-conv-turn][data-replies]");
      for (var i = 0; i < turns.length; i++) {
        var ids = (turns[i].getAttribute("data-replies") || "").split(" ");
        var idx = ids.indexOf(key);
        if (idx >= 0) {
          turns[i].open = true;
          var clones = turns[i].querySelectorAll("[data-wb-clone]");
          var exact = null;
          for (var c = 0; c < clones.length; c++) {
            if (clones[c].getAttribute && clones[c].getAttribute("data-wb-clone-entry") === key) { exact = clones[c]; break; }
          }
          target = exact || clones[idx] || clones[0] || turns[i];
          break;
        }
      }
    }
    // A row inside a whiteboard-only cluster is hidden until its cluster opens.
    if (typeof wbRevealClustered === "function") wbRevealClustered(box, target);
    for (var p = target; p && p !== box; p = p.parentNode) {
      if (p.tagName === "DETAILS") p.open = true;
    }
    return target;
  }
  function entryJumpFlash(target) {
    if (!target || !target.classList) return;
    target.classList.remove("conv-entry-flash");
    void target.offsetWidth;
    target.classList.add("conv-entry-flash");
    clearTimeout(target.wsEntryFlashTimer);
    target.wsEntryFlashTimer = setTimeout(function () {
      target.classList.remove("conv-entry-flash");
    }, ENTRY_FLASH_MS);
  }
  function entryJumpTo(key) {
    if (!key || !convo) return Promise.resolve(false);
    if (typeof shHidden !== "undefined" && shHidden) {
      entryJumpPending = key;
      return Promise.resolve(false);
    }
    entryJumpPending = "";
    if (grid && typeof tabShown === "function" && !tabShown("convo")) {
      if (typeof setTab === "function") setTab("convo");
      if (typeof setCtab === "function") setCtab("convo");
    }
    var epoch = ++entryJumpEpoch;
    var pages = 0;
    function finishFound(box, el) {
      if (epoch !== entryJumpEpoch) return false;
      // Flush any pending async whiteboard-only fold before revealing/flashing
      // so newly paginated replies are already cloned into their turn drawer.
      if (typeof wbOnlyOn === "function" && wbOnlyOn() && typeof wbFoldSync === "function") wbFoldSync();
      var target = entryJumpRevealTarget(box, el, key);
      stick = false;
      if (typeof queueSyncConvoBottom === "function") queueSyncConvoBottom();
      if (typeof target.scrollIntoView === "function") {
        target.scrollIntoView({ block: "center", behavior: typeof reducedMotion === "function" && reducedMotion() ? "instant" : "smooth" });
      }
      entryJumpFlash(target);
      return true;
    }
    function step() {
      if (epoch !== entryJumpEpoch) return Promise.resolve(false);
      var box = entriesBox();
      if (!box) return Promise.resolve(false);
      var hit = entryKeys(box)[key];
      if (hit) return Promise.resolve(finishFound(box, hit));
      if (histEnd || pages >= ENTRY_JUMP_MAX_PAGES || !window.fetch) {
        entryJumpNotice(wsT("js.conv.entry.not_found"));
        return Promise.resolve(false);
      }
      var wait = histBusy && histPromise ? histPromise : null;
      if (wait) {
        return wait.then(function () { return step(); });
      }
      pages++;
      return fetchHistoryPage({ force: true, skipNearTop: true }).then(function (res) {
        if (epoch !== entryJumpEpoch) return false;
        if (res && (res.aborted || res.error || res.skipped)) {
          entryJumpNotice(wsT("js.conv.entry.not_found"));
          return false;
        }
        return step();
      });
    }
    return step();
  }
  function entryJumpFromHash(h) {
    var key = parseEntryHash(h !== undefined ? h : location.hash);
    if (!key) return Promise.resolve(false);
    return entryJumpTo(key);
  }
  function entryJumpRunPending() {
    if (!entryJumpPending) return;
    var key = entryJumpPending;
    entryJumpPending = "";
    entryJumpTo(key);
  }
  if (typeof window !== "undefined") {
    window.wsEntryJumpFromHash = entryJumpFromHash;
    window.addEventListener("hashchange", function () { entryJumpFromHash(); });
  }

  // --- chat jump arrow (top-left of #convo) --------------------------------
  // Single arrow that jumps to the user's most recent own message not
  // currently visible in #convo; points up when above, down when below, and
  // hides when the latest own message is already visible (or none exist).
  function userJumpTarget(msgs, viewTop, viewBottom) {
    var latest = null, rect = null;
    for (var i = msgs.length - 1; i >= 0; i--) {
      var r = msgs[i].getBoundingClientRect();
      if (!r || (!r.width && !r.height)) continue;
      latest = msgs[i];
      rect = r;
      break;
    }
    if (!latest) return null;
    if (rect.bottom > viewTop && rect.top < viewBottom) return null;
    return { el: latest, dir: rect.bottom <= viewTop ? "up" : "down" };
  }
  var userJumpBtn = null, userJumpCur = null, userJumpFrame = 0;
  function ensureUserJump() {
    if (!convo) return null;
    if (userJumpBtn && userJumpBtn.isConnected) return userJumpBtn;
    var wrap = doc.createElement("div");
    wrap.className = "d-jump-wrap";
    var btn = doc.createElement("button");
    btn.type = "button";
    btn.className = "d-jump-btn";
    btn.hidden = true;
    btn.innerHTML = '<svg class="i" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M5 12l7-7 7 7"/></svg>';
    btn.addEventListener("click", function () {
      if (!userJumpCur || !userJumpCur.el || userJumpCur.el.isConnected === false) return;
      stick = false;
      if (typeof queueSyncConvoBottom === "function") queueSyncConvoBottom();
      userJumpCur.el.scrollIntoView({ block: "center", behavior: reducedMotion() ? "instant" : "smooth" });
    });
    wrap.appendChild(btn);
    convo.insertBefore(wrap, convo.firstChild);
    userJumpBtn = btn;
    return btn;
  }
  function updateUserJump() {
    userJumpFrame = 0;
    var btn = ensureUserJump();
    if (!btn) return;
    if (!visible(convo)) {
      userJumpCur = null;
      if (!btn.hidden) btn.hidden = true;
      return;
    }
    var cr = convo.getBoundingClientRect();
    var hit = userJumpTarget(convo.querySelectorAll(".msg.user"), cr.top, cr.bottom);
    userJumpCur = hit;
    var hide = !hit;
    if (btn.hidden !== hide) btn.hidden = hide;
    if (!hit) return;
    if (btn.getAttribute("data-dir") !== hit.dir) btn.setAttribute("data-dir", hit.dir);
    var tip = hit.dir === "down" ? wsT("js.convo.jump.down") : wsT("js.convo.jump.up");
    if (btn.title !== tip) btn.title = tip;
    if (btn.getAttribute("aria-label") !== tip) btn.setAttribute("aria-label", tip);
  }
  function scheduleUserJump() {
    if (!convo || userJumpFrame) return;
    userJumpFrame = requestAnimationFrame(updateUserJump);
  }
  if (convo) {
    convo.addEventListener("scroll", scheduleUserJump, { passive: true });
    window.addEventListener("resize", scheduleUserJump);
    if (window.ResizeObserver) new ResizeObserver(scheduleUserJump).observe(convo);
    if (window.MutationObserver) {
      new MutationObserver(function (recs) {
        if (recs.some(function (r) {
          var n = r.target;
          return !(n && n.closest && n.closest(".d-jump-wrap"));
        })) scheduleUserJump();
      }).observe(convo, { childList: true, subtree: true });
    }
    scheduleUserJump();
  }

  // mermaid draws every undrawn mermaid block, loading the shared script
  // once; a call while it loads is covered by the draw its onload runs, and
  // a failed load (daemon restarting) is retried by the next call. roots
  // (an element or a list of them: what a swap just inserted) limits the
  // look for undrawn blocks to them; omitted, the whole page is looked at.
  var mermaidLoading = false;
  function mermaid(roots) {
    var list = roots == null ? [doc] : Array.isArray(roots) ? roots : [roots];
    if (!window.wbMermaidFile || !list.some(function (r) { return !!r.querySelector("pre.mermaid:not([data-processed])"); })) return;
    if (window.mermaid) { window.wbMermaid(); return; }
    if (mermaidLoading) return;
    mermaidLoading = true;
    var s = doc.createElement("script");
    s.src = window.wbMermaidFile; // relative: the server serves it beside the page
    s.onload = function () { window.wbMermaid(); };
    s.onerror = function () { mermaidLoading = false; s.parentNode.removeChild(s); };
    doc.head.appendChild(s);
  }

  // --- closed topics past the board's cap: fetched on request ---------------
  // The board renders the most recently closed topics up to its cap, then a
  // show-more control naming the next index (data-closed-next); a click
  // fetches the next page from the closed route into the control's place,
  // ending with the next control when more remain. The fetched pages stay
  // across board swaps (morphBoard).
  function boardTopicPresent(name) {
    return boardQ("section.topic[data-topic]").some(function (shown) { return shown.getAttribute("data-topic") === name; }) ||
      !!(window.WsCards && window.WsCards.pinnedTopic(name));
  }
  function boardImageStamp() {
    var marker = doc.querySelector('template[data-history-images]');
    return marker && marker.getAttribute('data-history-images');
  }
  function loadClosed(btn) {
    if (!window.fetch || btn.disabled) return;
    btn.disabled = true;
    var from = +btn.getAttribute("data-closed-next"), images = boardImageStamp();
    return fetch("/s/" + rid + "/closed?from=" + from, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    }).then(function (res) {
      var page = frag(res.html || "").firstElementChild;
      // A board swap meanwhile replaced the control, or moved the index it
      // names (a topic closed or reopened): the page fetched may not follow
      // what shows now, and the control is clicked again.
      if (!btn.parentNode || !page || +btn.getAttribute("data-closed-next") !== from) { btn.disabled = false; return; }
      if (images !== boardImageStamp()) { btn.disabled = false; return loadClosed(btn); }
      // Pinned topics and closed topics materialized by a panel jump already
      // stand on the board; slots retain their place in the page count.
      q("section.topic[data-topic]", page).forEach(function (t) {
        var name = t.getAttribute("data-topic");
        if (!boardTopicPresent(name)) { boardPaged[name] = t.cloneNode(true); return; }
        var s = doc.createElement("span");
        s.className = "closed-slot";
        s.hidden = true;
        s.setAttribute("data-slot", name);
        t.parentNode.replaceChild(s, t);
      });
      var kept = boardKeep();
      btn.parentNode.replaceChild(page, btn);
      watchBoard(page);
      bandBoard(page);
      if (window.WsCards && window.WsCards.decorate) window.WsCards.decorate();
      restoreOpen(page);
      if (window.wbBindBoard) window.wbBindBoard(page);
      bindCapsuleNotes();
      if (window.wbAgo) window.wbAgo(page);
      mermaid(page);
      if (kept) kept.h.apply();
      if (window.WsBoardAnchor && window.WsBoardAnchor.settle) window.WsBoardAnchor.settle();
      if (typeof boardDialSync === "function") boardDialSync();
      if (typeof syncWbUpdateItems === "function") syncWbUpdateItems();
      return true;
    }, function () { btn.disabled = false; return false; });
  }

  // --- inline capsule detail popover (.icapsule-pop) ------------------------
  // Keeps rich multi-line mentions, selections, notes, picks, and consoles in
  // a viewport-clamped floating popover outside SSE-swapped regions.
  var icapsulePop = null, icapsuleActive = null, icapsulePinned = false, icapsuleLeaveTimer = 0;
  function icapsuleSource(el) {
    if (!el) return null;
    for (var i = 0; i < el.children.length; i++) {
      var c = el.children[i];
      if (c.tagName === "TEMPLATE" && c.classList.contains("icapsule-source")) return c;
    }
    return null;
  }
  function icapsuleTrigger(n) {
    if (!n || !n.closest) return null;
    if (n.closest(".unimg, .untxt")) return null;
    var cap = n.closest("[data-icapsule]");
    if (!cap) return null;
    var box = entriesBox(), held = region("convlive");
    if ((box && box.contains(cap)) || (held && held.contains(cap))) return cap;
    if ((typeof cimgs !== "undefined" && cimgs && cimgs.contains(cap)) || (typeof mimgs !== "undefined" && mimgs && mimgs.contains(cap))) return cap;
    return cap.closest(".nimgs, .selection-images") ? cap : null;
  }
  function icapsulePlace() {
    if (!icapsulePop || icapsulePop.hidden || !icapsuleActive) return;
    if (!icapsuleActive.isConnected || !icapsuleActive.getClientRects().length) { icapsuleClose(false); return; }
    var a = icapsuleActive.getBoundingClientRect();
    if (convo && convo.contains(icapsuleActive)) {
      var cr = convo.getBoundingClientRect();
      if (a.bottom < cr.top || a.top > cr.bottom) { icapsuleClose(false); return; }
    }
    var vw = root.clientWidth || window.innerWidth, vh = root.clientHeight || window.innerHeight;
    var m = 8, gap = 6, w = icapsulePop.offsetWidth, h = icapsulePop.offsetHeight;
    var left = Math.max(m, Math.min(a.left, vw - w - m));
    var top = a.bottom + gap;
    if (top + h > vh - m) top = a.top - gap - h >= m ? a.top - gap - h : Math.max(m, vh - h - m);
    icapsulePop.style.left = Math.round(left) + "px";
    icapsulePop.style.top = Math.round(top) + "px";
  }
  function icapsuleOpen(el, pin) {
    var src = icapsuleSource(el);
    if (!src) return;
    clearTimeout(icapsuleLeaveTimer);
    if (!icapsulePop) {
      icapsulePop = doc.createElement("div");
      icapsulePop.id = "icapsule-pop";
      icapsulePop.className = "icapsule-pop";
      icapsulePop.setAttribute("role", "dialog");
      icapsulePop.hidden = true;
      icapsulePop.addEventListener("mouseenter", function () { clearTimeout(icapsuleLeaveTimer); });
      icapsulePop.addEventListener("mouseleave", function () {
        if (!icapsulePinned) icapsuleScheduleClose();
      });
      doc.body.appendChild(icapsulePop);
    }
    if (icapsuleActive && icapsuleActive !== el) icapsuleActive.setAttribute("aria-expanded", "false");
    icapsuleActive = el;
    icapsulePinned = !!pin;
    el.setAttribute("aria-expanded", "true");
    // Clone inert <template> nodes directly so untrusted content never passes through innerHTML.
    if (src.content) icapsulePop.replaceChildren(src.content.cloneNode(true));
    else {
      icapsulePop.textContent = "";
      Array.prototype.forEach.call(src.children, function (c) { icapsulePop.appendChild(c.cloneNode(true)); });
    }
    icapsulePop.hidden = false;
    icapsulePlace();
  }
  function icapsuleClose(refocus) {
    clearTimeout(icapsuleLeaveTimer);
    var prev = icapsuleActive;
    icapsuleActive = null;
    icapsulePinned = false;
    if (prev) prev.setAttribute("aria-expanded", "false");
    if (icapsulePop) icapsulePop.hidden = true;
    if (refocus && prev && prev.isConnected) prev.focus();
  }
  function icapsuleScheduleClose() {
    clearTimeout(icapsuleLeaveTimer);
    icapsuleLeaveTimer = setTimeout(function () {
      if (!icapsulePinned) icapsuleClose(false);
    }, 120);
  }
  doc.addEventListener("mouseover", function (e) {
    if (icapsulePinned) return;
    var cap = icapsuleTrigger(e.target);
    if (cap && !cap.hasAttribute("data-icapsule-click")) icapsuleOpen(cap, false);
    else if (icapsulePop && icapsulePop.contains(e.target)) clearTimeout(icapsuleLeaveTimer);
  });
  doc.addEventListener("mouseout", function (e) {
    if (icapsulePinned || !icapsuleActive) return;
    var to = e.relatedTarget;
    if (to && (icapsuleActive.contains(to) || (icapsulePop && icapsulePop.contains(to)))) return;
    if (icapsuleTrigger(e.target) === icapsuleActive) icapsuleScheduleClose();
  });
  doc.addEventListener("focusin", function (e) {
    if (icapsulePinned) return;
    var cap = icapsuleTrigger(e.target);
    if (cap && !cap.hasAttribute("data-icapsule-click")) icapsuleOpen(cap, false);
    else if (icapsulePop && !icapsulePop.contains(e.target) && icapsuleActive) icapsuleClose(false);
  });
  doc.addEventListener("focusout", function (e) {
    if (icapsulePinned || !icapsuleActive) return;
    var to = e.relatedTarget;
    if (to && (icapsuleActive.contains(to) || (icapsulePop && icapsulePop.contains(to)))) return;
    icapsuleScheduleClose();
  });
  doc.addEventListener("click", function (e) {
    var cap = icapsuleTrigger(e.target);
    if (cap) {
      var selection = window.getSelection && window.getSelection();
      if (selection && !selection.isCollapsed) return;
      e.preventDefault();
      if (icapsuleActive === cap && icapsulePinned && icapsulePop && !icapsulePop.hidden) icapsuleClose(false);
      else icapsuleOpen(cap, true);
      return;
    }
    if (icapsulePop && !icapsulePop.hidden && !icapsulePop.contains(e.target)) icapsuleClose(false);
  });
  doc.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && icapsulePop && !icapsulePop.hidden) {
      e.preventDefault();
      e.stopPropagation();
      icapsuleClose(true);
      return;
    }
    if ((e.key === "Enter" || e.key === " ") && !e.metaKey && !e.ctrlKey && !e.altKey) {
      var cap = icapsuleTrigger(e.target);
      if (cap) {
        e.preventDefault();
        if (icapsuleActive === cap && icapsulePinned && icapsulePop && !icapsulePop.hidden) icapsuleClose(false);
        else icapsuleOpen(cap, true);
      }
    }
  });
  window.addEventListener("resize", icapsulePlace);
  doc.addEventListener("scroll", function (e) {
    if (icapsulePop && !icapsulePop.hidden && e.target && icapsulePop.contains(e.target)) return;
    icapsulePlace();
  }, true);

  // --- chat-stream question link capsules -----------------------------------
  // Hydrates .wbupdate-items chips inside .wbupdate-topic cards from the live
  // #r-board DOM as the single source of truth, keeping them synced across
  // picks, notes, holds (via pill.js), in-flight sends, and settlements.
  var WB_QCAP_CLASSES = { policy: "js.wbupdate.cls.policy", technical: "js.wbupdate.cls.technical", announced: "js.wbupdate.cls.announced", check: "js.wbupdate.cls.check" };
  var wbUpdateObserver = null, wbUpdateObservedBoard = null, wbUpdatePendingAll = false, wbUpdatePendingIds = null, wbUpdatePendingHints = null, wbUpdateQueued = false;

  function wbUpdateNormID(raw) {
    var s = String(raw || "").trim();
    if (!/^\d+$/.test(s)) return "";
    return String(parseInt(s, 10));
  }

  function wbUpdateItemID(el) {
    if (!el) return "";
    var id = wbUpdateNormID(el.getAttribute("data-wb-item-id"));
    if (id) return id;
    var jump = el.getAttribute("data-wb-jump") || "";
    var m = /^item-(\d+)$/.exec(jump);
    if (m) return wbUpdateNormID(m[1]);
    m = /^\((\d+)\)$/.exec((el.textContent || "").trim());
    return m ? wbUpdateNormID(m[1]) : "";
  }

  function wbUpdateFlattenMemo(raw) {
    if (!raw) return "";
    var s = String(raw)
      .replace(/<mailbox\b[^>]*>([\s\S]*?)<\/mailbox>/gi, " $1 ")
      .replace(/<mailbox\b[^>]*\/?>/gi, " ")
      .replace(/<textref\b[^>]*>([\s\S]*?)<\/textref>/gi, " $1 ")
      .replace(/<textref\b[^>]*\/?>/gi, " ")
      .replace(/\s+/g, " ")
      .trim();
    if (s.length > 60) s = s.slice(0, 59).trimEnd() + "…";
    return s;
  }

  function wbUpdateIsHeld(el) {
    if (!el) return false;
    if (window.wbPill && typeof window.wbPill.isHeld === "function") return !!window.wbPill.isHeld(el);
    return el.classList.contains("wb-held") || !!el.querySelector('.wb-hold-toggle[aria-pressed="true"]');
  }

  function wbUpdateClassFromEl(el) {
    if (!el) return "";
    var attr = el.getAttribute("data-class");
    if (attr && WB_QCAP_CLASSES[attr]) return attr;
    if (el.classList.contains("policy")) return "policy";
    if (el.classList.contains("ckrow")) return "check";
    if (el.classList.contains("arow")) {
      var tag = el.querySelector(".tag");
      if (tag) {
        if (tag.classList.contains("technical")) return "technical";
        if (tag.classList.contains("announced")) return "announced";
        if (tag.classList.contains("policy")) return "policy";
      }
      return "announced";
    }
    if (el.classList.contains("technical")) return "technical";
    if (el.classList.contains("announced")) return "announced";
    if (el.classList.contains("check")) return "check";
    return "";
  }

  function wbUpdateTitleFromEl(openEl, settledCk, settledHist, askEl) {
    var n = openEl && openEl.querySelector(".head .title");
    if (n && n.textContent.trim()) return n.textContent.trim();
    n = settledCk && settledCk.querySelector(".head .title");
    if (n && n.textContent.trim()) return n.textContent.trim();
    n = settledHist && settledHist.querySelector(".wb-hmain b");
    if (n && n.textContent.trim()) return n.textContent.trim();
    n = askEl && askEl.querySelector(".wb-hmain a, .wb-hmain b");
    if (n && n.textContent.trim()) return n.textContent.trim();
    return "";
  }

  function wbUpdateSettledSummary(settledHist, settledCk) {
    var text = "";
    if (settledHist) {
      var st = settledHist.querySelector(".wb-hstatus");
      if (st) {
        var pick = st.querySelector(".pick");
        if (pick) {
          var k = pick.querySelector(".k"), pt = pick.querySelector(".pt");
          var kTxt = k ? k.textContent.trim() : "", ptTxt = pt ? pt.textContent.trim() : "";
          var pickText = kTxt && ptTxt ? kTxt + " · " + ptTxt : (kTxt || ptTxt);
          var prefix = "";
          for (var c = st.firstChild; c && c !== pick; c = c.nextSibling) {
            if (c.nodeType === 3) prefix += c.nodeValue;
          }
          prefix = prefix.replace(/\s+/g, " ").trim();
          text = prefix ? prefix + " " + pickText : pickText;
        } else {
          text = (st.textContent || "").replace(/\s+/g, " ").trim();
        }
      }
      if (settledHist.classList.contains("commented")) {
        var qEl = settledHist.querySelector(".cmquote");
        var qTxt = qEl ? qEl.textContent.replace(/\s+/g, " ").trim() : "";
        if (qTxt) text = text ? text + " " + qTxt : qTxt;
      }
    } else if (settledCk) {
      var parts = [];
      [".amark", ".stamp", ".ans", ".said"].forEach(function (sel) {
        var n = settledCk.querySelector(sel);
        var v = n ? n.textContent.replace(/\s+/g, " ").trim() : "";
        if (v) parts.push(v);
      });
      text = parts.join(" · ");
    }
    text = text.replace(/\s+/g, " ").trim();
    if (!text) text = wsT("js.wbupdate.settled");
    if (text.length > 60) text = text.slice(0, 59).trimEnd() + "…";
    return text;
  }

  function wbUpdateQueryBoard(b, sel) {
    var api = wbUpdateQueryBoard._api;
    if (!api) {
      var CARD_SEL = '.card.policy[data-id], .arow[data-id], .ckrow[data-id]:not([data-assumed]), .ckrow[data-item], .wb-hitem.original-question[data-item], .wb-hitem[data-item][data-state="ask"], .wb-hitem[data-item][data-state="settled"]';
      var recCache = typeof WeakMap === "function" ? new WeakMap() : null;
      var vsGen = 0, lastVs = null, lastVsGen = -1, lastVsSize = -1, virtDirty = true, virtCombined = null;
      var makeBucket = function () {
        return { open: new Map(), ask: new Map(), settledHist: new Map(), settledCk: new Map() };
      };
      var putFirst = function (map, key, el, topic, order) {
        if (!key) return;
        if (!map.has(key)) map.set(key, { el: el, topic: topic, order: order });
      };
      var indexEl = function (bucket, el, topic, orderRef) {
        if (!el || el.nodeType !== 1) return;
        var did = el.getAttribute("data-id");
        if (did) {
          var cls = el.classList;
          var isOpen = (cls && ((cls.contains("card") && cls.contains("policy")) || cls.contains("arow") || (cls.contains("ckrow") && (!el.hasAttribute || !el.hasAttribute("data-assumed")))))
            || (el.matches && el.matches('.card.policy[data-id], .arow[data-id], .ckrow[data-id]:not([data-assumed])'));
          if (isOpen) putFirst(bucket.open, did, el, topic, orderRef.n++);
        }
        var item = el.getAttribute("data-item");
        if (item) {
          var isCk = (el.classList && el.classList.contains("ckrow")) || (el.matches && el.matches(".ckrow"));
          if (isCk) {
            putFirst(bucket.settledCk, item, el, topic, orderRef.n++);
          } else {
            var isHist = (el.classList && el.classList.contains("wb-hitem")) || (el.matches && el.matches(".wb-hitem"));
            if (isHist) {
              var st = el.getAttribute("data-state");
              if ((el.classList && el.classList.contains("original-question")) || st === "ask") {
                putFirst(bucket.ask, item, el, topic, orderRef.n++);
              }
              if (st === "settled") {
                putFirst(bucket.settledHist, item, el, topic, orderRef.n++);
              }
            }
          }
        }
      };
      var indexSubtree = function (bucket, root, topic, orderRef) {
        if (!root || root.nodeType !== 1) return;
        if (root.matches && root.matches(CARD_SEL)) indexEl(bucket, root, topic, orderRef);
        if (root.querySelectorAll) {
          var hits = root.querySelectorAll(CARD_SEL);
          for (var hi = 0; hi < hits.length; hi++) indexEl(bucket, hits[hi], topic, orderRef);
        }
      };
      var getRecBucket = function (rec) {
        var stashed = rec.stashed && rec.stashed.nodeType === 1 ? rec.stashed : null;
        var children = !stashed && rec.children && rec.children.length ? rec.children : null;
        var childLen = children ? children.length : 0;
        var cached = recCache ? recCache.get(rec) : null;
        if (cached && cached.stashed === stashed && cached.children === children && cached.childLen === childLen) {
          return cached.bucket;
        }
        var bucket = makeBucket();
        var orderRef = { n: 0 };
        if (stashed) {
          indexSubtree(bucket, stashed, 0, orderRef);
        } else if (children) {
          for (var ci = 0; ci < childLen; ci++) {
            indexSubtree(bucket, children[ci], 0, orderRef);
          }
        }
        if (recCache) recCache.set(rec, { stashed: stashed, children: children, childLen: childLen, bucket: bucket });
        return bucket;
      };
      var hookStore = function (vs) {
        if (!vs || vs.__wbIdxHooked) return;
        ["set", "delete", "clear"].forEach(function (m) {
          var orig = vs[m];
          if (typeof orig === "function") {
            try {
              Object.defineProperty(vs, m, {
                configurable: true,
                writable: true,
                value: function () {
                  var res = orig.apply(this, arguments);
                  vsGen++;
                  virtDirty = true;
                  return res;
                }
              });
            } catch (e) {}
          }
        });
        try {
          Object.defineProperty(vs, "__wbIdxHooked", { configurable: true, value: true });
        } catch (e) {}
      };
      var ensureVirtIdx = function () {
        if (typeof virtualStore === "undefined" || !virtualStore || !virtualStore.size) return null;
        hookStore(virtualStore);
        if (virtCombined && lastVs === virtualStore && lastVsGen === vsGen && !virtDirty && lastVsSize === virtualStore.size) {
          return virtCombined;
        }
        var combined = makeBucket();
        var topicSeq = 0;
        virtualStore.forEach(function (rec) {
          if (!rec) return;
          var tIdx = topicSeq++;
          var tb = getRecBucket(rec);
          tb.open.forEach(function (entry, k) {
            if (!combined.open.has(k)) combined.open.set(k, { el: entry.el, topic: tIdx, order: entry.order });
          });
          tb.ask.forEach(function (entry, k) {
            if (!combined.ask.has(k)) combined.ask.set(k, { el: entry.el, topic: tIdx, order: entry.order });
          });
          tb.settledHist.forEach(function (entry, k) {
            if (!combined.settledHist.has(k)) combined.settledHist.set(k, { el: entry.el, topic: tIdx, order: entry.order });
          });
          tb.settledCk.forEach(function (entry, k) {
            if (!combined.settledCk.has(k)) combined.settledCk.set(k, { el: entry.el, topic: tIdx, order: entry.order });
          });
        });
        virtCombined = combined;
        lastVs = virtualStore;
        lastVsGen = vsGen;
        lastVsSize = virtualStore.size;
        virtDirty = false;
        return combined;
      };
      var pickOpen = function (bucket, keys) {
        if (!bucket) return null;
        var best = null;
        for (var ki = 0; ki < keys.length; ki++) {
          var cand = bucket.open.get(keys[ki]);
          if (cand && (!best || cand.topic < best.topic || (cand.topic === best.topic && cand.order < best.order))) {
            best = cand;
          }
        }
        return best ? best.el : null;
      };
      var pickItem = function (map, key) {
        if (!map) return null;
        var entry = map.get(key);
        return entry ? entry.el : null;
      };
      var parseKnownSel = function (s) {
        if (s.indexOf('.card.policy[data-id="') !== -1 && s.indexOf('.arow[data-id="') !== -1 && s.indexOf('.ckrow[data-id="') !== -1) {
          var ids = [], seenIds = Object.create(null), re = /\[data-id="([^"]+)"\]/g, m;
          while ((m = re.exec(s))) {
            if (!seenIds[m[1]]) { seenIds[m[1]] = true; ids.push(m[1]); }
          }
          if (ids.length) return { slot: "open", keys: ids };
        }
        var am = s.match(/^\.wb-hitem\.original-question\[data-item="([^"]+)"\], \.wb-hitem\[data-item="([^"]+)"\]\[data-state="ask"\]$/);
        if (am && am[1] === am[2]) return { slot: "ask", key: am[1] };
        var sm = s.match(/^\.wb-hitem\[data-item="([^"]+)"\]\[data-state="settled"\]$/);
        if (sm) return { slot: "settledHist", key: sm[1] };
        var cm = s.match(/^\.ckrow\[data-item="([^"]+)"\]$/);
        if (cm) return { slot: "settledCk", key: cm[1] };
        return null;
      };
      var pass = null;
      api = wbUpdateQueryBoard._api = {
        getPass: function () { return pass; },
        beginPass: function (board) {
          if (pass && pass.board === board) { pass.depth++; return; }
          if (pass) { pass.depth++; return; }
          var liveBucket = makeBucket();
          if (board && board.querySelectorAll) indexSubtree(liveBucket, board, 0, { n: 0 });
          var virtBucket = ensureVirtIdx();
          pass = { board: board, live: liveBucket, virt: virtBucket, states: Object.create(null), depth: 1 };
        },
        endPass: function () {
          if (!pass) return;
          pass.depth--;
          if (pass.depth <= 0) pass = null;
        },
        invalidateVirtual: function (key) {
          vsGen++;
          virtDirty = true;
          virtCombined = null;
          if (key !== undefined && key !== null && recCache && typeof virtualStore !== "undefined" && virtualStore && virtualStore.get) {
            var hit = virtualStore.get(key);
            if (hit) recCache.delete(hit);
          }
        },
        lookupSlot: function (bucket, spec) {
          if (!bucket || !spec) return null;
          if (spec.slot === "open") return pickOpen(bucket, spec.keys);
          if (spec.slot === "ask") return pickItem(bucket.ask, spec.key);
          if (spec.slot === "settledHist") return pickItem(bucket.settledHist, spec.key);
          if (spec.slot === "settledCk") return pickItem(bucket.settledCk, spec.key);
          return null;
        },
        virtBucket: ensureVirtIdx,
        parseSel: parseKnownSel
      };
    }
    if (b === undefined && sel === undefined) return api;
    if (!b || !sel) return null;
    var activePass = api.getPass();
    var spec = api.parseSel(sel);
    if (spec && activePass && activePass.board === b) {
      return api.lookupSlot(activePass.live, spec) || api.lookupSlot(activePass.virt, spec);
    }
    var el = b.querySelector(sel);
    if (el) return el;
    if (typeof virtualStore === "undefined" || !virtualStore || !virtualStore.size) return null;
    if (spec) return api.lookupSlot(api.virtBucket(), spec);
    var found = null;
    virtualStore.forEach(function (rec) {
      if (found || !rec) return;
      if (rec.stashed && rec.stashed.nodeType === 1) {
        if (rec.stashed.matches && rec.stashed.matches(sel)) { found = rec.stashed; return; }
        if (rec.stashed.querySelector) found = rec.stashed.querySelector(sel);
        return;
      }
      if (rec.children && rec.children.length) {
        for (var i = 0; i < rec.children.length; i++) {
          var c = rec.children[i];
          if (!c || c.nodeType !== 1) continue;
          if (c.matches && c.matches(sel)) { found = c; return; }
          if (c.querySelector) {
            var hit = c.querySelector(sel);
            if (hit) { found = hit; return; }
          }
        }
      }
    });
    return found;
  }

  function wbUpdateChipMeta(chip) {
    if (!chip || !chip.classList || !chip.classList.contains("wbupdate-cap")) return null;
    var cls = "";
    var keys = Object.keys(WB_QCAP_CLASSES);
    for (var i = 0; i < keys.length; i++) {
      if (chip.classList.contains("wbupdate-cls-" + keys[i])) { cls = keys[i]; break; }
    }
    var ttlEl = chip.querySelector(".wbupdate-cap-ttl");
    var wrap = chip.parentNode;
    var isWrap = wrap && wrap.classList && wrap.classList.contains("wbupdate-qitem");
    var ansEl = isWrap && wrap.querySelector ? wrap.querySelector(".wbupdate-ans") : null;
    return {
      cls: cls,
      title: ttlEl ? ttlEl.textContent : "",
      state: isWrap ? (wrap.getAttribute("data-wb-state") || "") : "",
      text: ansEl ? ansEl.textContent : ""
    };
  }

  function wbUpdateOnBoard(b, el) {
    return !!(b && el && (typeof b.contains !== "function" || b.contains(el)));
  }

  function wbUpdateStagedChip(id) {
    if (typeof chips === "undefined" || !chips || typeof cchips === "undefined" || !cchips) return undefined;
    var norm = wbUpdateNormID(id);
    if (!norm) return null;
    var pad = norm.length < 2 ? "0" + norm : norm;
    return chips[pad] || chips[norm] || null;
  }

  function wbUpdateReadOpenPickMemo(openEl, id, b, opts) {
    var useStaged = opts && Object.prototype.hasOwnProperty.call(opts, "staged");
    var staged = useStaged ? opts.staged : (!wbUpdateOnBoard(b, openEl) ? wbUpdateStagedChip(id) : undefined);
    if (staged !== undefined) {
      return {
        pick: staged && staged.answer ? String(staged.answer) : "",
        memo: staged && staged.note ? wbUpdateFlattenMemo(staged.note) : ""
      };
    }
    var pickBtn = openEl.querySelector('.opt[data-opt][aria-pressed="true"], .btn[data-v][aria-pressed="true"]');
    var pick = pickBtn ? (pickBtn.getAttribute("data-opt") || pickBtn.getAttribute("data-v") || "") : "";
    var box = openEl.querySelector("label.ack > input, label.ck > input");
    if (!pick && openEl.hasAttribute("data-no")) pick = "no";
    else if (!pick && box && box.checked) pick = "ok";
    var note = openEl.querySelector("textarea.note, .wsc-backing");
    var memo = note ? wbUpdateFlattenMemo(note.value) : "";
    return { pick: pick, memo: memo };
  }

  function readBoardQuestionState(rawId, opts) {
    var b = region("board");
    var id = wbUpdateNormID(rawId);
    if (!b || !id) return null;
    var qApi = wbUpdateQueryBoard();
    var pass = qApi && qApi.getPass ? qApi.getPass() : null;
    if (!opts && pass && pass.board === b && Object.prototype.hasOwnProperty.call(pass.states, id)) {
      return pass.states[id];
    }
    var res = null;
    var pad = id.length < 2 ? "0" + id : id;
    var skipVirtual = !!(opts && opts.skipVirtual);
    var meta = opts && opts.fallbackMeta;
    var openSel = '.card.policy[data-id="' + pad + '"], .arow[data-id="' + pad + '"], .ckrow[data-id="' + pad + '"]:not([data-assumed])';
    if (pad !== id) openSel += ', .card.policy[data-id="' + id + '"], .arow[data-id="' + id + '"], .ckrow[data-id="' + id + '"]:not([data-assumed])';
    var candOpen = opts && opts.openEl;
    if (candOpen && (!candOpen.classList || candOpen.hasAttribute("data-assumed") || !((candOpen.classList.contains("card") && candOpen.classList.contains("policy")) || candOpen.classList.contains("arow") || candOpen.classList.contains("ckrow")))) {
      candOpen = null;
    }
    var openEl = candOpen || ((opts && opts.openEl) ? null : (skipVirtual ? b.querySelector(openSel) : wbUpdateQueryBoard(b, openSel)));
    var askSel = '.wb-hitem.original-question[data-item="' + id + '"], .wb-hitem[data-item="' + id + '"][data-state="ask"]';
    var askEl = null;

    // An actively open item on the board always takes precedence over historical
    // rows (e.g. an earlier .commented entry before the item was re-asked).
    if (openEl) {
      var openCls = wbUpdateClassFromEl(openEl) || (meta && meta.cls) || "";
      var openTitle = wbUpdateTitleFromEl(openEl, null, null, null) || (meta && meta.title) || "";
      if (!openCls || !openTitle) {
        askEl = skipVirtual ? b.querySelector(askSel) : wbUpdateQueryBoard(b, askSel);
        openCls = openCls || wbUpdateClassFromEl(askEl) || "check";
        openTitle = openTitle || wbUpdateTitleFromEl(null, null, null, askEl);
      }
      if (openEl.classList.contains("sent-held")) {
        res = { cls: openCls, title: openTitle, state: "sent", clsName: "st-sent", text: wsT("js.wbupdate.sent_held"), pick: "" };
      } else if (openEl.classList.contains("sent-sent")) {
        res = { cls: openCls, title: openTitle, state: "sent", clsName: "st-sent", text: wsT("js.wbupdate.sent_waiting"), pick: "" };
      } else {
        var pick, memo;
        if (typeof wbUpdateReadOpenPickMemo === "function") {
          var pm = wbUpdateReadOpenPickMemo(openEl, id, b, opts);
          pick = pm.pick;
          memo = pm.memo;
        } else {
          var pickBtn = openEl.querySelector('.opt[data-opt][aria-pressed="true"], .btn[data-v][aria-pressed="true"]');
          pick = pickBtn ? (pickBtn.getAttribute("data-opt") || pickBtn.getAttribute("data-v") || "") : "";
          var box = openEl.querySelector("label.ack > input, label.ck > input");
          if (!pick && openEl.hasAttribute("data-no")) pick = "no";
          else if (!pick && box && box.checked) pick = "ok";
          var note = openEl.querySelector("textarea.note, .wsc-backing");
          memo = note ? wbUpdateFlattenMemo(note.value) : "";
        }
        if (pick || memo) {
          var stCls = pick === "ok" ? "st-staged-ok" : pick === "no" ? "st-staged-no" : "st-staged";
          var stText = pick && memo ? wsT("js.wbupdate.staged_pick", pick) + " · " + memo
            : pick ? wsT("js.wbupdate.staged_pick", pick)
            : wsT("js.wbupdate.staged_memo", memo);
          res = { cls: openCls, title: openTitle, state: "staged", clsName: stCls, text: stText, pick: pick };
        } else if (wbUpdateIsHeld(openEl)) {
          res = { cls: openCls, title: openTitle, state: "held", clsName: "st-held", text: wsT("js.pill.held"), pick: "" };
        } else {
          res = { cls: openCls, title: openTitle, state: "open", clsName: "st-open", text: wsT("js.wbupdate.unanswered"), pick: "" };
        }
      }
      if (!opts && pass && pass.board === b) pass.states[id] = res;
      return res;
    }

    if (skipVirtual && meta && meta.state) {
      if (meta.state === "settled") {
        return { cls: meta.cls || "check", title: meta.title || "", state: "settled", clsName: "st-settled", text: meta.text || ("✓ " + wsT("js.wbupdate.settled")), pick: "" };
      }
      var sv = (opts && Object.prototype.hasOwnProperty.call(opts, "staged")) ? opts.staged : (typeof wbUpdateStagedChip === "function" ? (wbUpdateStagedChip(id) || null) : null);
      var svPick = sv && sv.answer ? String(sv.answer) : "";
      var svMemo = sv && sv.note ? wbUpdateFlattenMemo(sv.note) : "";
      if (svPick || svMemo) {
        var svCls = svPick === "ok" ? "st-staged-ok" : svPick === "no" ? "st-staged-no" : "st-staged";
        var svText = svPick && svMemo ? wsT("js.wbupdate.staged_pick", svPick) + " · " + svMemo
          : svPick ? wsT("js.wbupdate.staged_pick", svPick)
          : wsT("js.wbupdate.staged_memo", svMemo);
        return { cls: meta.cls || "check", title: meta.title || "", state: "staged", clsName: svCls, text: svText, pick: svPick };
      }
      return { cls: meta.cls || "check", title: meta.title || "", state: "open", clsName: "st-open", text: wsT("js.wbupdate.unanswered"), pick: "" };
    }

    var settledHist = skipVirtual ? b.querySelector('.wb-hitem[data-item="' + id + '"][data-state="settled"]') : wbUpdateQueryBoard(b, '.wb-hitem[data-item="' + id + '"][data-state="settled"]');
    var settledCk = skipVirtual ? b.querySelector('.ckrow[data-item="' + id + '"]') : wbUpdateQueryBoard(b, '.ckrow[data-item="' + id + '"]');
    if (!settledHist && !settledCk) {
      if (!opts && pass && pass.board === b) pass.states[id] = null;
      return null;
    }
    var cls = wbUpdateClassFromEl(settledHist) || wbUpdateClassFromEl(settledCk) || "";
    var title = wbUpdateTitleFromEl(null, settledCk, settledHist, null);
    if (!cls || !title) {
      askEl = skipVirtual ? b.querySelector(askSel) : wbUpdateQueryBoard(b, askSel);
      cls = cls || wbUpdateClassFromEl(askEl) || "check";
      title = title || wbUpdateTitleFromEl(null, null, null, askEl);
    }
    res = { cls: cls, title: title, state: "settled", clsName: "st-settled", text: "✓ " + wbUpdateSettledSummary(settledHist, settledCk), pick: "" };
    if (!opts && pass && pass.board === b) pass.states[id] = res;
    return res;
  }

  function hydrateWbUpdateChip(chip, id, state) {
    if (!state) {
      if (chip.classList.contains("wbupdate-cap")) {
        var wrap = chip.parentNode;
        chip.className = "wbupdate-item-id";
        chip.removeAttribute("title");
        chip.textContent = "(" + id + ")";
        if (wrap && wrap.classList && wrap.classList.contains("wbupdate-qitem") && wrap.parentNode) {
          wrap.parentNode.insertBefore(chip, wrap);
          wrap.parentNode.removeChild(wrap);
        }
      }
      return false;
    }
    chip.setAttribute("data-wb-item-id", id);
    var wrap = chip.parentNode;
    if (!wrap || !wrap.classList || !wrap.classList.contains("wbupdate-qitem")) {
      wrap = doc.createElement("div");
      wrap.className = "wbupdate-qitem";
      wrap.setAttribute("data-wb-qitem", id);
      if (chip.parentNode) chip.parentNode.insertBefore(wrap, chip);
      wrap.appendChild(chip);
    }
    if (wrap.getAttribute("data-wb-state") !== state.state) wrap.setAttribute("data-wb-state", state.state);
    var pickText = state.state === "staged" && state.pick ? String(state.pick) : "";
    if (pickText) {
      if (wrap.getAttribute("data-wb-empty") !== "1") wrap.setAttribute("data-wb-empty", "1");
    } else if (wrap.hasAttribute("data-wb-empty")) {
      wrap.removeAttribute("data-wb-empty");
    }

    var wantClass = "wbupdate-item-id wbupdate-cap wbupdate-cls-" + state.cls;
    if (chip.className !== wantClass) chip.className = wantClass;
    var tip = "(" + id + ")" + (state.title ? " " + state.title : "") + (state.text ? "\n" + state.text : "");
    if (chip.getAttribute("title") !== tip) chip.setAttribute("title", tip);

    var idEl = chip.querySelector(".wbupdate-cap-id");
    var clsEl = chip.querySelector(".wbupdate-cap-cls");
    var ttlEl = chip.querySelector(".wbupdate-cap-ttl");
    var valEl = chip.querySelector(".wbupdate-cap-val");
    var arrEl = chip.querySelector(".wbupdate-cap-arr");
    var markEl = chip.querySelector(".wbupdate-cap-mark");
    if (!idEl || !clsEl || !ttlEl) {
      chip.textContent = "";
      idEl = doc.createElement("span"); idEl.className = "wbupdate-cap-id";
      clsEl = doc.createElement("span"); clsEl.className = "wbupdate-cap-cls";
      ttlEl = doc.createElement("span"); ttlEl.className = "wbupdate-cap-ttl";
      chip.appendChild(idEl);
      chip.appendChild(clsEl);
      chip.appendChild(ttlEl);
      valEl = null;
      markEl = null;
      if (chip.tagName === "A") {
        arrEl = doc.createElement("span");
        arrEl.className = "wbupdate-cap-arr";
        arrEl.setAttribute("aria-hidden", "true");
        arrEl.textContent = "↗";
        chip.appendChild(arrEl);
      }
    }
    if (!markEl) {
      markEl = doc.createElement("span");
      markEl.className = "wbupdate-cap-mark";
      markEl.setAttribute("aria-hidden", "true");
      markEl.innerHTML = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 8.5l3 3 6-7"/></svg>';
      chip.insertBefore(markEl, arrEl || null);
    }
    if (!valEl) {
      valEl = doc.createElement("span");
      valEl.className = "wbupdate-cap-val";
      chip.insertBefore(valEl, markEl);
    }
    var idText = "(" + id + ")";
    if (idEl.textContent !== idText) idEl.textContent = idText;
    var clsLabel = wsT(WB_QCAP_CLASSES[state.cls] || "js.wbupdate.cls.check");
    if (clsEl.textContent !== clsLabel) clsEl.textContent = clsLabel;
    if (ttlEl.textContent !== state.title) ttlEl.textContent = state.title;
    if (valEl.textContent !== pickText) valEl.textContent = pickText;
    if (pickText) {
      if (valEl.getAttribute("data-wb-pick") !== pickText) valEl.setAttribute("data-wb-pick", pickText);
    } else if (valEl.hasAttribute("data-wb-pick")) {
      valEl.removeAttribute("data-wb-pick");
    }

    var ans = wrap.querySelector(".wbupdate-ans");
    if (!ans) {
      ans = doc.createElement("span");
      ans.className = "wbupdate-ans";
      wrap.appendChild(ans);
    }
    var ansClass = "wbupdate-ans " + state.clsName;
    if (ans.className !== ansClass) ans.className = ansClass;
    if (ans.textContent !== state.text) ans.textContent = state.text;
    return true;
  }

  function hydrateWbUpdateGroup(group) {
    if (!group) return;
    var chips = q(".wbupdate-item-id", group), any = false;
    for (var i = 0; i < chips.length; i++) {
      var chip = chips[i], id = wbUpdateItemID(chip);
      if (!id) continue;
      if (hydrateWbUpdateChip(chip, id, readBoardQuestionState(id))) any = true;
    }
    group.classList.toggle("wbupdate-hydrated", any);
  }

  function hydrateWbUpdateItems(scope) {
    var rootEl = scope || entriesBox() || doc;
    if (!rootEl || !rootEl.querySelectorAll) return;
    var qApi = wbUpdateQueryBoard();
    if (qApi && qApi.beginPass) qApi.beginPass(region("board"));
    try {
      if (rootEl.matches && rootEl.matches(".wbupdate-items")) {
        hydrateWbUpdateGroup(rootEl);
        return;
      }
      q(".wbupdate-items", rootEl).forEach(hydrateWbUpdateGroup);
    } finally {
      if (qApi && qApi.endPass) qApi.endPass();
    }
  }

  function syncWbUpdateItems(ids, hints) {
    var batch = syncWbUpdateItems._batch;
    if (batch && batch.depth > 0) {
      if (!ids || !ids.length) {
        batch.all = true;
      } else if (!batch.all) {
        for (var bi = 0; bi < ids.length; bi++) {
          var bid = wbUpdateNormID(ids[bi]);
          if (!bid) continue;
          batch.ids[bid] = true;
          var bpad = bid.length < 2 ? "0" + bid : bid;
          var bhint = hints && (hints[bid] || hints[bpad] || hints[ids[bi]]);
          if (bhint) {
            if (!batch.hints) batch.hints = Object.create(null);
            var prevHint = batch.hints[bid];
            if (!prevHint) batch.hints[bid] = Object.assign({}, bhint);
            else {
              if (Object.prototype.hasOwnProperty.call(bhint, "openEl")) prevHint.openEl = bhint.openEl;
              if (Object.prototype.hasOwnProperty.call(bhint, "staged")) prevHint.staged = bhint.staged;
              if (!bhint.skipVirtual) prevHint.skipVirtual = false;
            }
          }
        }
      }
      return;
    }
    var box = entriesBox() || doc;
    if (!box || !box.querySelectorAll) return;
    var needPass = !ids || !ids.length;
    if (!needPass) {
      for (var pi = 0; pi < ids.length; pi++) {
        var pid = wbUpdateNormID(ids[pi]);
        if (!pid) continue;
        var ppad = pid.length < 2 ? "0" + pid : pid;
        var ph = hints && (hints[pid] || hints[ppad] || hints[ids[pi]]);
        if (!ph || !ph.skipVirtual) { needPass = true; break; }
      }
    }
    var qApi = needPass ? wbUpdateQueryBoard() : null;
    if (qApi && qApi.beginPass) qApi.beginPass(region("board"));
    var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("convo", "syncWbUpdateItems") : null;
    try {
      if (!ids || !ids.length) {
        hydrateWbUpdateItems(box);
        return;
      }
      var seen = Object.create(null);
      for (var i = 0; i < ids.length; i++) {
        var id = wbUpdateNormID(ids[i]);
        if (!id || seen[id]) continue;
        seen[id] = true;
        var pad = id.length < 2 ? "0" + id : id;
        var sel = '.wbupdate-item-id[data-wb-item-id="' + id + '"], .wbupdate-item-id[data-wb-jump="item-' + id + '"]';
        if (pad !== id) sel += ', .wbupdate-item-id[data-wb-jump="item-' + pad + '"]';
        var targets = q(sel, box);
        if (!targets.length) continue;
        var hint = hints && (hints[id] || hints[pad]);
        var meta = (!hint || !hint.fallbackMeta) ? (typeof wbUpdateChipMeta === "function" ? wbUpdateChipMeta(targets[0]) : null) : hint.fallbackMeta;
        var opts = hint ? Object.assign({}, hint, { fallbackMeta: meta }) : null;
        var state = readBoardQuestionState(id, opts);
        for (var j = 0; j < targets.length; j++) {
          hydrateWbUpdateChip(targets[j], id, state);
          var grp = targets[j].closest(".wbupdate-items");
          if (grp) grp.classList.toggle("wbupdate-hydrated", !!grp.querySelector(".wbupdate-cap"));
        }
      }
    } finally {
      if (qApi && qApi.endPass) qApi.endPass();
      if (jTok) WsJank.end(jTok);
    }
  }

  function wbUpdateInvalidateVirtual(key) {
    var api = wbUpdateQueryBoard();
    if (api && api.invalidateVirtual) api.invalidateVirtual(key);
  }

  function wbUpdateBeginBatch() {
    var b = syncWbUpdateItems._batch;
    if (!b) b = syncWbUpdateItems._batch = { depth: 0, all: false, ids: Object.create(null), hints: null };
    b.depth++;
  }

  function wbUpdateEndBatch() {
    var b = syncWbUpdateItems._batch;
    if (!b || b.depth <= 0) return;
    b.depth--;
    if (b.depth > 0) return;
    var all = b.all, idMap = b.ids, batchHints = b.hints;
    b.all = false;
    b.ids = Object.create(null);
    b.hints = null;
    if (all) syncWbUpdateItems();
    else {
      var keys = Object.keys(idMap);
      if (keys.length) syncWbUpdateItems(keys, batchHints);
    }
  }

  function wbUpdateQueueSync(id, hint) {
    if (!id || id === "*") wbUpdatePendingAll = true;
    else {
      var norm = wbUpdateNormID(id);
      if (!norm) return;
      if (!wbUpdatePendingIds) wbUpdatePendingIds = Object.create(null);
      wbUpdatePendingIds[norm] = true;
      if (hint) {
        if (!wbUpdatePendingHints) wbUpdatePendingHints = Object.create(null);
        var prev = wbUpdatePendingHints[norm];
        if (!prev) wbUpdatePendingHints[norm] = hint;
        else if (!hint.skipVirtual) prev.skipVirtual = false;
      }
    }
    if (wbUpdateQueued) return;
    wbUpdateQueued = true;
    Promise.resolve().then(function () {
      wbUpdateQueued = false;
      var all = wbUpdatePendingAll, ids = wbUpdatePendingIds, hints = wbUpdatePendingHints;
      wbUpdatePendingAll = false;
      wbUpdatePendingIds = null;
      wbUpdatePendingHints = null;
      if (all) syncWbUpdateItems();
      else if (ids) syncWbUpdateItems(Object.keys(ids), hints);
    });
  }

  function wbUpdateNodeItemID(node) {
    if (!node || node.nodeType !== 1) return "";
    var host = node.closest ? node.closest("[data-id], [data-item]") : null;
    if (!host) return "";
    return wbUpdateNormID(host.getAttribute("data-id") || host.getAttribute("data-item") || "");
  }

  function watchWbUpdateBoard() {
    var b = region("board");
    if (!b || typeof MutationObserver !== "function") return;
    if (wbUpdateObserver && wbUpdateObservedBoard === b) return;
    if (wbUpdateObserver) wbUpdateObserver.disconnect();
    wbUpdateObservedBoard = b;
    wbUpdateObserver = new MutationObserver(function (list) {
      for (var i = 0; i < list.length; i++) {
        var m = list[i];
        if (m.type === "attributes") {
          var host = m.target.closest ? m.target.closest(".card[data-id], .arow[data-id], .ckrow[data-id]") : null;
          if (host) {
            var hid = wbUpdateNormID(host.getAttribute("data-id"));
            if (hid) wbUpdateQueueSync(hid, { openEl: host, skipVirtual: true });
            continue;
          }
          var aid = wbUpdateNodeItemID(m.target);
          if (aid) wbUpdateQueueSync(aid);
          continue;
        }
        if (m.type === "childList") {
          var chost = m.target.closest ? m.target.closest(".card[data-id], .arow[data-id], .ckrow[data-id]") : null;
          if (chost) {
            var cid = wbUpdateNormID(chost.getAttribute("data-id"));
            if (cid) wbUpdateQueueSync(cid, { openEl: chost, skipVirtual: true });
            continue;
          }
          var tid = wbUpdateNodeItemID(m.target);
          if (tid) { wbUpdateQueueSync(tid); continue; }
          var scan = function (nodes) {
            for (var k = 0; k < nodes.length; k++) {
              var n = nodes[k];
              if (!n || n.nodeType !== 1) continue;
              if (n.matches && n.matches(".card[data-id], .arow[data-id], .ckrow[data-id], .ckrow[data-item], .wb-hitem[data-item], .sentmark")) {
                var nid = n.getAttribute("data-id") || n.getAttribute("data-item") || wbUpdateNodeItemID(n);
                wbUpdateQueueSync(nid || "*");
              } else if (n.querySelector && n.querySelector(".card[data-id], .arow[data-id], .ckrow[data-id], .ckrow[data-item], .wb-hitem[data-item], .sentmark")) {
                wbUpdateQueueSync("*");
                return true;
              }
            }
            return false;
          };
          if (scan(m.addedNodes) || scan(m.removedNodes)) return;
        }
      }
    });
    wbUpdateObserver.observe(b, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ["class", "aria-pressed", "data-no", "data-state", "data-item"]
    });
  }

  ["input", "change"].forEach(function (type) {
    doc.addEventListener(type, function (e) {
      var t = e.target;
      if (!t || !inBoard(t)) return;
      if (t.id === "ack-all") {
        var acks = boardQ(".arow[data-id]");
        for (var i = 0; i < acks.length; i++) {
          var aid = wbUpdateNormID(acks[i].getAttribute("data-id"));
          if (aid) wbUpdateQueueSync(aid, { openEl: acks[i], skipVirtual: true });
        }
        return;
      }
      var host = t.closest ? t.closest(".card[data-id], .arow[data-id], .ckrow[data-id]") : null;
      if (host) {
        var hid = wbUpdateNormID(host.getAttribute("data-id"));
        if (hid) wbUpdateQueueSync(hid, { openEl: host, skipVirtual: true });
        return;
      }
      var id = wbUpdateNodeItemID(t);
      if (id) wbUpdateQueueSync(id);
    });
  });

  function initWbUpdateCards() {
    watchWbUpdateBoard();
    hydrateWbUpdateItems(entriesBox() || doc);
  }
  window.WsWbUpdateCards = {
    hydrate: hydrateWbUpdateItems,
    sync: syncWbUpdateItems,
    init: initWbUpdateCards,
    invalidateVirtual: wbUpdateInvalidateVirtual,
    beginBatch: wbUpdateBeginBatch,
    endBatch: wbUpdateEndBatch
  };

