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
  // mergeEntries inserts each node of src whose name box lacks at its sorted
  // place and returns inserted/replaced nodes, in order. Existing entries
  // stay put unless their mail-wake presentation changes: a hidden mail
  // marker must replace its row (and restore it when the association expires).
  function mergeEntries(box, src, changed) {
    var have = entryKeys(box), added = [];
    Array.prototype.slice.call(src.children).forEach(function (n) {
      var k = n.getAttribute("data-entry");
      if (!k) return;
      var old = have[k];
      if (old) {
        if (!(changed && changed[k]) && old.getAttribute("data-mail-wake") === n.getAttribute("data-mail-wake")) return;
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
    });
    return added;
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
      el.innerHTML = html; restoreOpen(el); fmtTimes(el); sweepOutImgs(el); mermaid(el); histReset();
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
    var row = t.closest(".wbupdate-row[data-wb-element]");
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
  var histBefore = null, histEnd = false, histBusy = false, histEpoch = 0;
  function histReset() { histBefore = null; histEnd = false; histBusy = false; histEpoch++; emitAuditEvent("ws-history-reset", {}); }
  function loadHistory() {
    if (histEnd || histBusy || !window.fetch || !convo || !visible(convo)) return;
    var box = entriesBox();
    if (!box) return;
    var prevOldest = firstEntryEl(box), prevOldestAt = prevOldest ? prevOldest.getAttribute("data-at") || "" : "";
    if (histBefore === null) {
      histBefore = prevOldest ? prevOldest.getAttribute("data-entry") || "" : "";
    }
    histBusy = true;
    var epoch = histEpoch;
    var url = "/s/" + rid + "/history?n=50" + (histBefore ? "&before=" + encodeURIComponent(histBefore) : "");
    fetch(url, { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    }).then(function (res) {
      if (epoch !== histEpoch) return;
      histBusy = false;
      var h = convo.scrollHeight, top = convo.scrollTop;
      var added = mergeEntries(box, frag(res.html || ""));
      added.forEach(sweepOutImgs);
      // Pagination supersedes any WS restoration queued before this fetch
      // completed; its compensated reading position is now authoritative.
      scrollRestore.delete(convo);
      // Keep what the user was reading in place: the prepended height goes
      // above the scroll offset.
      convo.scrollTop = top + (convo.scrollHeight - h);
      mermaid(added);
      if (res.before) histBefore = res.before;
      if (res.end || !res.before) histEnd = true;
      var nextOldest = firstEntryEl(box), nextOldestAt = nextOldest ? nextOldest.getAttribute("data-at") || "" : "";
      emitAuditEvent("ws-history-page", { prevOldestAt: prevOldestAt, oldestAt: nextOldestAt, end: !!histEnd, empty: !nextOldest });
      nearTop();
    }, function () {
      // A failed fetch (daemon replaced, network) is retried on the next
      // scroll to the top, after a pause.
      if (epoch === histEpoch) setTimeout(function () { if (epoch === histEpoch) histBusy = false; }, 3000);
    });
  }
  // nearTop loads older entries when the conversation is scrolled to (or
  // too short to scroll away from) its top.
  function nearTop() { if (convo && visible(convo) && convo.scrollTop < 160) loadHistory(); }
  if (convo) convo.addEventListener("scroll", nearTop);

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

