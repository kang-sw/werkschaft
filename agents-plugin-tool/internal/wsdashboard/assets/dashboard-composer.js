// Composer chips and inline answer state.
  // --- composer: never inside a region, so a swap never touches it ---------
  // With a live host adapter the composer sends: the typed text plus one
  // answer chip per board item. Clicking an option, ok/no, or an
  // acknowledgement on a card attaches (or replaces) that item's chip; the
  // chips live here, so a chip outlives its card leaving the board.
  var ctext = $("ctext"), cchips = $("cchips");
  if (ctext && window.WsCapsules) window.WsCapsules.attach(ctext);
  var chips = {}, chipOrder = [];
  // bmentions are the board mentions the composer holds, in the order added
  // (board mentions, below).
  var bmentions = [];
  // New pushes retain their source, one memo, and image references together.
  // Drafts and requests serialize these views into the existing text field.
  var bselections = [];
  var closeRequests = [];
  function validCloseRequests(list) {
    var out = [];
    (Array.isArray(list) ? list : []).forEach(function (r) {
      if (!r || typeof r.topic !== "string" || !r.topic.trim() || /[\r\n\x00]/.test(r.topic) || typeof r.reason !== "string") return;
      var next = { topic: r.topic.trim(), reason: r.reason }, i = out.findIndex(function (v) { return v.topic === next.topic; });
      if (i < 0) out.push(next); else out[i] = next;
    });
    return out;
  }
  function focusCloseRequest(topic) {
    if (!cchips) return;
    var input = q("[data-close-reason]", cchips).filter(function (el) { return el.getAttribute("data-close-reason") === topic; })[0];
    if (input) input.focus();
  }
  function addCloseRequest(topic) {
    if (!ctext || !cchips || !topic) return;
    if (!closeRequests.some(function (r) { return r.topic === topic; })) {
      closeRequests.push({ topic: topic, reason: "" });
      renderChips();
      queueDraft();
    }
    focusCloseRequest(topic);
  }
  function removeCloseRequest(topic) {
    closeRequests = closeRequests.filter(function (r) { return r.topic !== topic; });
    renderChips();
    queueDraft();
  }
  // wpicks and wconsoles are the web card's picked elements and console
  // blocks the composer holds, in the order added (web picks, below).
  var wpicks = [], wconsoles = [];
  function inlineEditor() { return ctext && ctext.wsc && ctext.wsc.insertCapsule ? ctext.wsc : null; }
  function inlineRows(kind) { var edit = inlineEditor(); return edit ? edit.ordered().filter(function (r) { return r.kind === kind; }) : []; }
  function answerCapsule(c) {
    return { label: "(" + c.id + ")=" + (c.answer || "note"), anchor: "(" + c.id + ")=" + (c.answer || "note"), plain: "(" + c.id + ")=" + (c.answer || "note") + (c.note ? " " + c.note : ""), answer: c };
  }
  function topicNoteCapsule(n) {
    var note = n.value.trim();
    return { label: "[" + n.topic + "] " + note.replace(/\s+/g, " ").slice(0, 40), anchor: "[note:" + n.topic + "]", plain: "[" + n.topic + "] " + note, topic: n.topic, note: note };
  }
  var restoringInlineBack = false;
  function syncInlineTopics() {
    var edit = inlineEditor(); if (!edit) return;
    if (restoringInlineBack) return;
    var notes = boardQ(".tnote[data-topic]");
    var was = syncingInline; syncingInline = true;
    try {
      notes.forEach(function (el) {
        var n = { topic: el.getAttribute("data-topic"), value: el.value }, exists = inlineRows("topic-note").some(function (r) { return r.key === n.topic; });
        if (!n.value.trim()) { if (exists) edit.remove("topic-note", n.topic); return; }
        if (exists) edit.upsert("topic-note", n.topic, topicNoteCapsule(n));
        else edit.insertCapsule("topic-note", topicNoteCapsule(n), { key: n.topic, preserveCaret: true });
      });
    } finally { syncingInline = was; }
  }
  // The editor owns order and deletion. Board controls remain a view of its
  // answer and note tokens; this guard prevents a board change echoing back.
  var syncingInline = false;
  if (ctext) ctext.addEventListener("wsc:change", function (e) {
    if (syncingInline) return;
    syncingInline = true;
    try {
      (e.detail.removed || []).forEach(function (r) {
        if (r.kind === "answer") { var remItem = setItem(r.key, null); dropChip(r.key, true, false, remItem); }
        if (r.kind === "topic-note") clearTopicNote(r.key);
        if (r.kind === "mention" && r.payload && r.payload.selectionKey) {
          var at = bselections.findIndex(function (s) { return s.key === r.payload.selectionKey; });
          if (at >= 0) dropSelection(at, true);
        }
      });
      (e.detail.added || []).forEach(function (r) {
        if (r.kind === "answer" && r.payload && r.payload.answer) {
          putChip(r.payload.answer);
          var addItem = setItem(r.key, r.payload.answer);
          if (typeof syncWbUpdateItems === "function") {
            var addHints = Object.create(null);
            addHints[r.key] = { openEl: addItem, staged: r.payload.answer, skipVirtual: true };
            syncWbUpdateItems([r.key], addHints);
          }
        }
        if (r.kind === "topic-note" && r.payload && r.payload.note) {
          var note = topicNote(r.key);
          if (note) { note.value = r.payload.note; note.dispatchEvent(new Event("input", { bubbles: true })); }
        }
        if (r.kind === "mention" && r.payload && r.payload.selection && !bselections.some(function (s) { return s.key === r.key; })) {
          bselections.push(Object.assign({ key: r.key }, r.payload.selection));
        }
      });
    } finally { syncingInline = false; }
    queueDraft();
  });
  function chipList() { return inlineEditor() ? inlineRows("answer").map(function (r) { return r.payload && r.payload.answer; }).filter(Boolean) : chipOrder.map(function (id) { return chips[id]; }); }
  function putChip(c) {
    if (!chips[c.id]) chipOrder.push(c.id);
    chips[c.id] = c;
    var edit = inlineEditor(); if (edit && !restoringInlineBack && !syncingInline) { var was = syncingInline; syncingInline = true; try {
      if (inlineRows("answer").some(function (r) { return r.key === c.id; })) edit.upsert("answer", c.id, answerCapsule(c));
      else edit.insertCapsule("answer", answerCapsule(c), { key: c.id, preserveCaret: true });
    } finally { syncingInline = was; } }
  }
  function dropChip(id, fromEditor, skipWbSync, itemEl) {
    var had = Object.prototype.hasOwnProperty.call(chips, id);
    delete chips[id]; chipOrder = chipOrder.filter(function (x) { return x !== id; });
    var edit = inlineEditor(); if (edit && !fromEditor && !syncingInline) { var was = syncingInline; syncingInline = true; try { edit.remove("answer", id); } finally { syncingInline = was; } }
    if (!skipWbSync && had && typeof syncWbUpdateItems === "function") {
      var hints = Object.create(null);
      hints[id] = { openEl: itemEl || null, staged: null, skipVirtual: true };
      syncWbUpdateItems([id], hints);
    }
  }
  function boardItem(id) {
    return boardQ('.card[data-id="' + id + '"], .arow[data-id="' + id + '"], .ckrow[data-id="' + id + '"], .srow[data-id="' + id + '"]')[0] || null;
  }
  // readItem is a board item's answer as a chip; null when it has none.
  function readItem(item) {
    var pick = item.querySelector('.opt[data-opt][aria-pressed="true"], .btn[data-v][aria-pressed="true"]');
    var box = item.querySelector("label.ack > input, label.ck > input"), note = item.querySelector("textarea.note");
    var c = { id: item.getAttribute("data-id"), answer: "", note: note ? note.value.trim() : "" };
    if (pick) c.answer = pick.getAttribute("data-opt") || pick.getAttribute("data-v");
    else if (item.hasAttribute("data-no")) c.answer = "no";
    else if (box && box.checked) c.answer = "ok";
    return c.answer || c.note ? c : null;
  }
  // setItem puts a chip's answer (or none) on the item's card and lets the
  // board's form script rebuild its reply line.
  function setItem(id, c) {
    var item = boardItem(id);
    if (!item) return null;
    q(".opt[data-opt], .btn[data-v]", item).forEach(function (b) {
      var v = b.getAttribute("data-opt") || b.getAttribute("data-v");
      b.setAttribute("aria-pressed", String(!!c && v === c.answer));
    });
    var box = item.querySelector("label.ack > input, label.ck > input"), note = item.querySelector("textarea.note");
    if (box) box.checked = !!c && c.answer === "ok";
    setCheckNo(item, !!c && c.answer === "no");
    if (note) {
      note.value = c && c.note ? c.note : "";
      if (note.value) note.hidden = false;
    }
    var fire = note || box;
    if (fire) fire.dispatchEvent(new Event("change", { bubbles: true }));
    return item;
  }
  // syncChips takes the answers of the items on the board into the chips.
  function syncChips(target) {
    if (!region("board") || !cchips) return;
    var items;
    if (target && typeof target.closest === "function") {
      var one = target.closest(".card[data-id], .arow[data-id], .ckrow[data-id], .srow[data-id]");
      if (one) items = [one];
      else if (target.id === "ack-all") items = boardQ(".arow[data-id]");
      else if (target.closest("[data-bulk]")) {
        var bulkTopic = target.closest(".topic");
        items = bulkTopic ? q(".card[data-id], .arow[data-id], .ckrow[data-id], .srow[data-id]", bulkTopic) : [];
      } else if (target.classList && target.classList.contains("tnote")) items = [];
      else items = boardQ(".card[data-id], .arow[data-id], .ckrow[data-id], .srow[data-id]");
    } else {
      items = boardQ(".card[data-id], .arow[data-id], .ckrow[data-id], .srow[data-id]");
    }
    var changedIds = [], hints = Object.create(null);
    items.forEach(function (item) {
      var c = readItem(item), id = item.getAttribute("data-id"), prev = chips[id];
      var changed = (!!c !== !!prev) || (c && prev && (c.answer !== prev.answer || c.note !== prev.note));
      if (c) putChip(c); else if (prev) dropChip(id, false, true, item);
      if (changed && id) {
        changedIds.push(id);
        hints[id] = { openEl: item, staged: c || null, skipVirtual: true };
      }
    });
    if (inlineEditor()) syncInlineTopics();
    if (!updateNoteChips()) renderChips();
    if (changedIds.length && typeof syncWbUpdateItems === "function") syncWbUpdateItems(changedIds, hints);
  }
  // Typing changes note text, not the chip set. Keep the existing strip (and
  // focused controls) intact; replacing its children also dirties :has() ancestors.
  function updateNoteChips() {
    if (inlineEditor()) return true;
    var list = chipList(), notes = topicNotes();
    var answers = q("[data-unchip]", cchips), topics = q("[data-tchip]", cchips);
    if (answers.length !== list.length || topics.length !== notes.length) return false;
    if (list.some(function (c, i) {
      return answers[i].getAttribute("data-unchip") !== c.id || !!answers[i].parentNode.querySelector(".anote") !== !!c.note;
    }) || notes.some(function (n, i) { return topics[i].getAttribute("data-tchip") !== n.topic; })) return false;
    function text(node, value) { if (node.nodeValue !== value) node.nodeValue = value; }
    list.forEach(function (c, i) {
      var el = answers[i].parentNode;
      text(el.childNodes[1], " " + (c.answer || "note"));
      if (c.note) text(el.querySelector(".anote").firstChild, c.note);
    });
    notes.forEach(function (n, i) { text(topics[i].querySelector(".anote").firstChild, n.value.trim()); });
    return true;
  }
  function renderChips() {
    if (!cchips) return;
    if (inlineEditor()) syncInlineTopics();
    var win = typeof window !== "undefined" ? window : null;
    var active = doc.activeElement;
    var activeField = (win && win.WsCapsules && win.WsCapsules.fieldFor && win.WsCapsules.fieldFor(active)) || active;
    var focused = activeField && activeField.getAttribute("data-close-reason");
    var selection = focused != null ? [activeField.selectionStart, activeField.selectionEnd] : null;
    if (win && win.WsInput && win.WsInput.sweep) win.WsInput.sweep(cchips);
    cchips.textContent = "";
    if (!inlineEditor()) chipList().forEach(function (c) {
      var el = doc.createElement("span"), b = doc.createElement("b"), x = doc.createElement("button");
      el.className = "achip";
      b.textContent = "(" + c.id + ")";
      el.appendChild(b);
      el.appendChild(doc.createTextNode(" " + (c.answer || "note")));
      if (c.note) {
        var n = doc.createElement("span");
        n.className = "anote";
        n.textContent = c.note;
        el.appendChild(n);
      }
      x.className = "unchip";
      x.setAttribute("data-unchip", c.id);
      x.title = wsT("js.chip.remove");
      x.textContent = "×";
      el.appendChild(x);
      cchips.appendChild(el);
    });
    // A topic note is no item's answer, but a send carries it, so it shows
    // here too: "[topic] text", read from the board on every render.
    if (!inlineEditor()) topicNotes().forEach(function (n) {
      var el = doc.createElement("span"), b = doc.createElement("b"), t = doc.createElement("span"), x = doc.createElement("button");
      el.className = "achip tchip";
      el.setAttribute("data-tchip", n.topic);
      el.title = wsT("js.chip.tnote.open");
      b.textContent = "[" + n.topic + "]";
      el.appendChild(b);
      t.className = "anote";
      t.textContent = n.value.trim();
      el.appendChild(t);
      x.className = "unchip";
      x.setAttribute("data-untchip", n.topic);
      x.title = wsT("js.chip.tnote.remove");
      x.textContent = "×";
      el.appendChild(x);
      cchips.appendChild(el);
    });
    // A board mention: "[topic] (id)" and the selected text, clipped.
    if (!inlineEditor()) bmentions.forEach(function (m, i) {
      var el = doc.createElement("span"), open = doc.createElement("button"), b = doc.createElement("b"), t = doc.createElement("span"), x = doc.createElement("button");
      el.className = "achip mchip";
      el.title = m.text;
      open.type = "button";
      open.className = "mchip-open";
      open.addEventListener("click", function () {
        // Use the same session-local navigation as the history's ID chips.
        var topicID = "topic-" + Array.prototype.map.call(new TextEncoder().encode(m.topic), function (n) { return ("0" + n.toString(16)).slice(-2); }).join("");
        effectJump(topicID, m.entry || (m.id ? "item-" + Number(m.id) : topicID));
      });
      b.textContent = "[" + m.topic + "]" + (m.id ? " (" + m.id + ")" : "") + (m.entry ? " " + m.entry : "");
      open.appendChild(b);
      t.className = "anote";
      t.textContent = m.text.replace(/\s+/g, " ");
      open.appendChild(t);
      el.appendChild(open);
      x.type = "button";
      x.className = "unchip";
      x.setAttribute("data-unmchip", String(i));
      x.title = wsT("js.wbmention.remove");
      x.textContent = "×";
      el.appendChild(x);
      cchips.appendChild(el);
    });
    if (!inlineEditor()) renderSelectionChips();
    closeRequests.forEach(function (r) {
      var el = doc.createElement("span"), b = doc.createElement("b"), input = doc.createElement("textarea"), x = doc.createElement("button");
      el.className = "achip close-request-chip";
      b.textContent = wsT("js.closerequest.label", r.topic);
      el.appendChild(b);
      input.className = "close-request-reason";
      input.rows = 1;
      input.value = r.reason;
      input.placeholder = wsT("js.closerequest.reason");
      input.setAttribute("aria-label", wsT("js.closerequest.reasonfor", r.topic));
      input.setAttribute("data-close-reason", r.topic);
      input.addEventListener("input", function () {
        r.reason = input.value;
        r.mentions = input.wsInput ? input.wsInput.getMentions() : [];
        queueDraft();
      });
      el.appendChild(input);
      x.type = "button";
      x.className = "unchip";
      x.setAttribute("data-unclose-request", r.topic);
      x.title = wsT("js.closerequest.remove");
      x.setAttribute("aria-label", x.title);
      x.textContent = "×";
      el.appendChild(x);
      cchips.appendChild(el);
      if (win && win.WsInput) {
        var reasonInst = win.WsInput.attach(input, {
          keymap: "newline-only",
          voice: true,
          features: {
            markdown: true,
            capsules: true,
            mentions: true,
            topics: true,
            slash: true,
            imagePaste: false,
            longPaste: false,
            listContinuation: true,
            bangMode: false,
            autoGrow: true
          },
          providers: {
            files: typeof fileCandidates === "function" && rid ? fileCandidates : null,
            topics: typeof capsuleTopics === "function" ? capsuleTopics : null,
            commands: typeof commandCandidates === "function" && rid ? commandCandidates : null
          },
          preformattedCommands: true,
          renderCommand: typeof commandRow === "function" ? commandRow : undefined,
          slashMax: 500,
          mentionLabel: wsT("js.mention.label"),
          slashLabel: wsT("js.cmd.label")
        });
        if (reasonInst && r.mentions && r.mentions.length) reasonInst.setMentions(r.mentions);
      }
    });
    // A web card's picked element: its selector and text; a console block:
    // its error count and page. The held message shows the same capsules
    // (dashboard.tmpl achips).
    if (!inlineEditor()) wpicks.forEach(function (p, i) {
      var el = doc.createElement("span"), b = doc.createElement("b"), x = doc.createElement("button");
      el.className = "achip mchip pchip";
      el.title = p.url + "\n" + p.selector + (p.text ? "\n" + p.text : "");
      b.textContent = p.selector;
      el.appendChild(b);
      if (p.text) {
        var t = doc.createElement("span");
        t.className = "anote";
        t.textContent = p.text;
        el.appendChild(t);
      }
      x.className = "unchip";
      x.setAttribute("data-unwpick", String(i));
      x.title = wsT("js.web.chip.pick.remove");
      x.textContent = "×";
      el.appendChild(x);
      cchips.appendChild(el);
    });
    if (!inlineEditor()) wconsoles.forEach(function (c, i) {
      var el = doc.createElement("span"), b = doc.createElement("b"), t = doc.createElement("span"), x = doc.createElement("button");
      el.className = "achip mchip cchip";
      el.title = c.url + "\n" + c.lines.join("\n");
      b.textContent = wsT("js.web.chip.console", c.lines.length);
      el.appendChild(b);
      t.className = "anote";
      t.textContent = c.url;
      el.appendChild(t);
      x.className = "unchip";
      x.setAttribute("data-unwcon", String(i));
      x.title = wsT("js.web.chip.console.remove");
      x.textContent = "×";
      el.appendChild(x);
      cchips.appendChild(el);
    });
    if (focused != null) {
      focusCloseRequest(focused);
      var next = (win && win.WsCapsules && win.WsCapsules.fieldFor && win.WsCapsules.fieldFor(doc.activeElement)) || doc.activeElement;
      if (next && next.getAttribute("data-close-reason") === focused) next.setSelectionRange(selection[0], selection[1]);
    }
    if (cimgs) renderImgs();
  }
  function renderSelectionChips() {
    bselections.forEach(function (s, index) {
      var group = doc.createElement("span"), edit = doc.createElement("button"), note = doc.createElement("span"), remove = doc.createElement("button");
      group.className = "selection-bundle achip mchip selection-chip";
      group.setAttribute("role", "group");
      group.setAttribute("aria-label", wsT("js.wbmention.context"));
      edit.type = "button"; edit.className = "mchip-open selection-edit";
      edit.setAttribute("data-selection-edit", String(index));
      edit.title = s.mentions.map(function (m) { return m.text; }).join("\n") + (s.comment ? "\n" + s.comment : "");
      edit.setAttribute("aria-label", wsT("js.wbmention.note") + ": " + edit.title);
      s.mentions.forEach(function (m) {
        var source = doc.createElement("span"), b = doc.createElement("b"), text = doc.createElement("span");
        source.className = "selection-source";
        b.textContent = "[" + m.topic + "]" + (m.id ? " (" + m.id + ")" : "") + (m.entry ? " " + m.entry : "");
        text.className = "anote"; text.textContent = m.text.replace(/\s+/g, " ");
        source.appendChild(b); source.appendChild(text);
        if (m.entry) source.setAttribute("data-wb-entry", m.entry);
        edit.appendChild(source);
      });
      note.className = "selection-note"; note.textContent = "✎";
      selectionNote(s, note);
      edit.appendChild(note);
      remove.type = "button"; remove.className = "unchip";
      remove.setAttribute("data-unselection", String(index));
      remove.title = wsT("js.wbmention.remove"); remove.textContent = "×";
      group.appendChild(edit); group.appendChild(remove);
      s.row = doc.createElement("div"); s.row.className = "selection-images";
      group.appendChild(s.row); cchips.appendChild(group);
    });
  }
  function selectionNote(s, note) {
    note.hidden = !s.comment.trim();
    note.title = s.comment;
    note.setAttribute("aria-label", wsT("js.wbmention.note"));
  }
  function dropSelection(index, fromEditor) {
    var s = bselections[index];
    if (!s) return;
    bselections.splice(index, 1);
    if (inlineEditor() && !fromEditor) inlineEditor().remove("mention", s.key);
    if (mpend && mpend.edit === s) hideMention();
    s.images.slice().forEach(function (n) {
      var it = imgs.filter(function (p) { return p.n === n; })[0];
      if (it) unattach(it);
    });
    renderChips(); queueDraft();
  }
  function editSelection(index) {
    var s = bselections[index];
    if (!s) return;
    hideMention();
    var row = s.row && s.row.parentNode;
    if (!row && inlineEditor()) row = q(".wsc-mention", inlineEditor().editor).filter(function (n) { return n.getAttribute("data-wsc-key") === s.key; })[0];
    var rect = row ? row.getBoundingClientRect() : { left: 4, bottom: 4 };
    placeMention({ ms: s.mentions, x: rect.left, y: rect.bottom, edit: s, comment: s.comment, commentMentions: s.commentMentions, images: s.images.slice() });
    lockMention(); mnote.focus();
    mnote.setSelectionRange(mnote.value.length, mnote.value.length);
  }
