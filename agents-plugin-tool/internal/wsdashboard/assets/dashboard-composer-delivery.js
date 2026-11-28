// Composer sends and outbox delivery.
  // topicNotes are the board's non-empty topic notes. A send carries them
  // as its topics, which the delivered message's answer block lists after
  // the item answers; they are no item's answer, so they are no answer
  // chip, and renderChips shows each as a topic-note chip.
  function topicNotes() {
    return boardQ(".tnote[data-topic]").map(function (el) {
      return { el: el, topic: el.getAttribute("data-topic"), value: el.value };
    }).filter(function (n) { return n.value.trim(); });
  }
  function inlineSend() {
    var win = typeof window !== "undefined" ? window : null;
    var strip = win && win.WsInput && win.WsInput.stripMentions;
    function sendSel(s) {
      return s && strip && s.commentMentions && s.commentMentions.length ? Object.assign({}, s, { comment: strip(s.comment, s.commentMentions) }) : s;
    }
    var edit = inlineEditor(), rows = edit.ordered(), raw = ctext.value, notes = rows.filter(function (r) { return r.kind === "topic-note"; }).map(function (r) {
      return { el: topicNote(r.key), topic: r.key, value: r.payload.note };
    }), said = [], picks = [], consoles = [], usedSelections = {}, at = 0, text = "";
    var hasProse = false;
    rows.forEach(function (r) { if (raw.slice(at, r.start).trim()) hasProse = true; at = r.end; });
    if (raw.slice(at).trim()) hasProse = true;
    at = 0;
    rows.forEach(function (r) {
      text += raw.slice(at, r.start);
      var p = r.payload || {}, anchor = raw.slice(r.start, r.end);
      if (r.kind === "answer" || r.kind === "topic-note") {
        if (hasProse || rows.some(function (x) { return x.kind !== "answer" && x.kind !== "topic-note"; })) text += anchor;
      } else if (r.kind === "mention" && p.selectionKey) {
        var s = bselections.filter(function (x) { return x.key === p.selectionKey; })[0] || p.selection;
        if (s) { text += "\n\n" + selectionBlock(sendSel(s)) + "\n\n"; usedSelections[p.selectionKey] = true; }
      } else {
        text += anchor;
        if (r.kind === "mention" && p.mention) said.push(p.mention);
        if (r.kind === "pick" && p.pick) picks.push(p.pick);
        if (r.kind === "console" && p.console) consoles.push(p.console);
      }
      at = r.end;
    });
    text += raw.slice(at);
    var legacy = bselections.filter(function (s) { return !usedSelections[s.key]; }).map(sendSel);
    return { text: selectionPrefix(legacy) + text.replace(/^(?:[ \t]*\r?\n)+|\s+$/g, ""), notes: notes, mentions: said.concat(bmentions), picks: picks.concat(wpicks), consoles: consoles.concat(wconsoles) };
  }
  function sendMessage() {
    if (restoring || draftStore && !draftLoaded) return;
    var comp = (typeof $ === "function" ? $("composer") : (typeof doc !== "undefined" && doc && typeof doc.getElementById === "function" ? doc.getElementById("composer") : null));
    if (comp && comp.hasAttribute && comp.hasAttribute("data-voice-locked")) return;
    // A first-character `!` is a command for the daemon's composer-exec
    // route, taken before any trimming or message assembly.
    if (cexecSend()) return;
    var inline = !cmdSel && window.WsCapsules && window.WsCapsules.command(ctext);
    // A command waits until the queued messages before it are held, so the
    // inbox keeps the order typed.
    if ((cmdSel || inline) && outboxInFlight()) return;
    if (cmdSel) { sendCommand(); return; }
    if (inline) { sendInlineCommand(inline); return; }
    var win = typeof window !== "undefined" ? window : null;
    var strip = win && win.WsInput && win.WsInput.stripMentions;
    function sendSel(s) {
      return s && strip && s.commentMentions && s.commentMentions.length ? Object.assign({}, s, { comment: strip(s.comment, s.commentMentions) }) : s;
    }
    var groups = bselections.slice(), inlineData = inlineEditor() ? inlineSend() : null;
    var t = inlineData ? inlineData.text : selectionPrefix(groups.map(sendSel)) + sendText().replace(/^(?:[ \t]*\r?\n)+|\s+$/g, ""), list = chipList(), notes = inlineData ? inlineData.notes : topicNotes(), said = inlineData ? inlineData.mentions : bmentions.slice();
    var committed = {};
    groups.forEach(function (s) { s.images.forEach(function (n) { committed[n] = true; }); });
    var pics = imgs.filter(function (p) { return !mpend || (mpend.images || []).indexOf(p.n) < 0 || committed[p.n]; });
    var wp = inlineData ? inlineData.picks : wpicks.slice(), wc = inlineData ? inlineData.consoles : wconsoles.slice();
    var closing = closeRequests.map(function (r) { return { ref: r, topic: r.topic, reason: strip && r.mentions && r.mentions.length ? strip(r.reason, r.mentions) : r.reason }; });
    var txtAttachments = typeof textRefs !== "undefined" ? textRefs.slice() : [];
    if (!t && !list.length && !pics.length && !txtAttachments.length && !notes.length && !said.length && !wp.length && !wc.length && !closing.length) { noteWarn(wsT("js.send.empty")); return; }
    if (pics.some(function (p) { return !p.ref && !p.blob && p.data === null; })) { noteWarn(wsT("js.send.reading")); return; }
    if (txtAttachments.length && typeof serializeTextRefs === "function") t = serializeTextRefs(t, txtAttachments);
    if (typeof checkTextRefCap === "function") {
      var cap = checkTextRefCap(t, said, closing);
      if (cap.over) {
        noteWarn(wsT("js.textref.overcap.send", formatTextRefBytes(cap.bytes), formatTextRefBytes(TEXTREF_MAX_BYTES)));
        return;
      }
    }
    var body = { utcOffsetMinutes: -new Date().getTimezoneOffset(), text: t, chips: list, topics: notes.map(function (n) { return { topic: n.topic, note: n.value.trim() }; }), mentions: said };
    if (typeof boardIncarnation === "string") body.boardIncarnation = boardIncarnation;
    if (typeof wbOnlyOn === "function" && wbOnlyOn()) body.whiteboard_first = true;
    if (closing.length) body.closeRequests = closing.map(function (r) { return { topic: r.topic, reason: r.reason }; });
    if (wp.length) body.picks = wp;
    if (wc.length) body.consoles = wc;
    var refs = pics.filter(function (p) { return !!p.ref; });
    if (refs.length) {
      body.refs = refs.map(function (p) { return p.ref; });
      body.refNums = refs.map(function (p) { return p.n; });
    }
    // A send arms only receipt matching for its topics, never a blanket
    // exemption for a concurrent external panel rewrite.
    var ownTopics = notes.map(function (n) { return n.topic; });
    list.forEach(function (c) { var item = boardItem(c.id), topic = item && item.closest("section.topic[data-topic]"); if (topic) ownTopics.push(topicKey(topic)); });
    ownTopics.forEach(function (k) { boardOwn[k] = true; });
    // Save the active draft before handing it off. Its recovery record
    // commits before the POST can start.
    saveDraft();
    var inlineBack = inlineEditor() ? { text: ctext.value, capsules: inlineEditor().snapshot(), selections: bselections.map(function (s) { return { key: s.key, mentions: s.mentions, comment: s.comment, images: s.images }; }) } : null;
    pics.forEach(dropImg);
    if (typeof textRefs !== "undefined") textRefs = [];
    ctext.value = ""; grow(); clearMentions();
    if (typeof resetAutoMultiline === "function") resetAutoMultiline();
    var sentIds = [], sentHints = Object.create(null);
    list.forEach(function (c) {
      var itemEl = setItem(c.id, null);
      dropChip(c.id, false, true, itemEl);
      sentIds.push(c.id);
      sentHints[c.id] = { openEl: itemEl, staged: null, skipVirtual: true };
    });
    if (sentIds.length && typeof syncWbUpdateItems === "function") syncWbUpdateItems(sentIds, sentHints);
    bmentions = []; bselections = []; closeRequests = []; wpicks = []; wconsoles = [];
    notes.forEach(function (n) {
      if (n.el) { n.el.value = ""; n.el.dispatchEvent(new Event("input", { bubbles: true })); }
    });
    closeTopicNotes();
    clearTrash();
    renderChips();
    renderImgs();
    // No toast on success: the held bubble in the conversation, shown on
    // the daemon's OK, is the confirmation.
    outboxAdd({
      body: body, pics: pics, own: ownTopics, user: lastUserEntry(),
      back: { text: t, chips: list, topics: body.topics, mentions: said, closeRequests: body.closeRequests, picks: wp, consoles: wc, inline: inlineBack, sentAt: Date.now() }
    });
    if (typeof compact !== "undefined" && compact && window.parent && window.parent !== window) {
      try { window.parent.postMessage({ wsCompanionCompact: "user-send", rid: rid || "" }, location.origin); } catch (x) { /* parent gone */ }
    }
  }
  // --- outbox -------------------------------------------------------------------
  // Only an acknowledged POST gets a held bubble. Restored deliveries stay
  // in the composer until the user chooses to send or discard each one.
  var outbox = [], outboxPosting = false, outboxRows = null, recoveredRows = null, deliverySequence = 0, OUTBOX_LINGER = 30000, OUTBOX_WAIT = 30000;
  function outboxUnsent() { return outbox.some(function (m) { return !m.acked; }); }
  function outboxInFlight() { return outbox.some(function (m) { return !m.acked && !m.paused; }); }
  // A batch (sends the session read in one turn) carries its key on its
  // .user-batch wrapper; the bubbles inside it carry none.
  function lastUserEntry() {
    var us = q("#r-entries .msg.user[data-entry], #r-entries .user-batch[data-entry]");
    return us.length ? us[us.length - 1].getAttribute("data-entry") : "";
  }
  // heldAct is a copy of the server held bubble's steer or cancel button
  // (kind) for id, cloned from the composer's heldacts template; none on a
  // page without one.
  function heldAct(kind, id) {
    var tpl = doc.querySelector("#heldacts"), b = tpl && tpl.content.querySelector("[data-" + kind + "]");
    if (!b) return null;
    b = b.cloneNode(true);
    b.setAttribute("data-" + kind, id);
    return b;
  }
  function ucmdUnescapeXml(text) {
    var entities = { "&#91;": "[", "&lt;": "<", "&gt;": ">", "&quot;": '"', "&apos;": "'", "&#39;": "'", "&amp;": "&" };
    return String(text || "").replace(/&#91;|&lt;|&gt;|&quot;|&apos;|&#39;|&amp;/g, function (s) { return entities[s]; });
  }
  // parseUcmdAddBlock mirrors wsinbox.parseUserCommandAdd: the request text of
  // a <ws:ucmd-add> block (the agent-facing guidance is never shown).
  function parseUcmdAddBlock(norm) {
    var open = '<ws:ucmd-add scope="worktree" tool="session.register_user_command">';
    var firstNL = norm.indexOf("\n");
    if (firstNL < 0 || norm.slice(0, firstNL) !== open || norm.slice(-15) !== "\n</ws:ucmd-add>") return null;
    var body = norm.slice(firstNL + 1, -14).trim();
    if (body.indexOf("<request>") !== 0) return null;
    var end = body.indexOf("</request>");
    if (end < 0) return null;
    var req = body.slice(9, end), rest = body.slice(end + 10).trim();
    if (rest.indexOf("<attachments>") === 0) {
      var ae = rest.indexOf("</attachments>");
      if (ae < 0) return null;
      rest = rest.slice(ae + 14).trim();
    }
    if (rest.indexOf("<mcp-guidance>") !== 0 || rest.slice(-15) !== "</mcp-guidance>") return null;
    if (req.indexOf("<") >= 0 || !req.trim()) return null;
    return { add: true, request: ucmdUnescapeXml(req), label: "", cmd: "", cwd: "", prompt: "", edited: false };
  }
  function parseUcmdBlock(raw) {
    var norm = String(raw || "").replace(/\r\n/g, "\n");
    if (norm.indexOf("<ws:ucmd-add") === 0) return parseUcmdAddBlock(norm);
    if (norm.indexOf("<ws:ucmd") !== 0 || norm.slice(-11) !== "\n</ws:ucmd>") return null;
    var firstNL = norm.indexOf("\n");
    if (firstNL < 0 || norm.charAt(firstNL - 1) !== ">") return null;
    var attrStr = norm.slice(8, firstNL - 1), label = "", edited = false, seenLabel = false, seenEdited = false;
    while (attrStr) {
      if (attrStr.charAt(0) !== " ") return null;
      attrStr = attrStr.slice(1);
      var am = /^([a-z]+)="([^"<]*)"/.exec(attrStr);
      if (!am) return null;
      if (am[1] === "label") {
        if (seenLabel) return null;
        seenLabel = true;
        label = ucmdUnescapeXml(am[2]);
      } else if (am[1] === "edited") {
        if (seenEdited || am[2] !== "true") return null;
        seenEdited = true;
        edited = true;
      } else {
        return null;
      }
      attrStr = attrStr.slice(am[0].length);
    }
    var body = norm.slice(firstNL + 1, -11);
    var cmdIdx = body.indexOf("<cmd");
    if (cmdIdx < 0) return null;
    if (cmdIdx > 0) {
      var lead = body.slice(0, cmdIdx);
      if (lead.charAt(lead.length - 1) !== "\n" || lead.indexOf("<") >= 0) return null;
    }
    var rest = body.slice(cmdIdx), cm = /^<cmd(?: cwd="([^"<]*)")?>/.exec(rest);
    if (!cm) return null;
    var cwd = cm[1] ? ucmdUnescapeXml(cm[1]) : "";
    rest = rest.slice(cm[0].length);
    var cmdEnd = rest.indexOf("</cmd>");
    if (cmdEnd < 0 || rest.slice(0, cmdEnd).indexOf("<") >= 0) return null;
    var cmd = ucmdUnescapeXml(rest.slice(0, cmdEnd));
    if (!cmd.trim()) return null;
    var tail = rest.slice(cmdEnd + 6), prompt = "";
    if (tail) {
      if (tail.indexOf("\n<prompt>") !== 0 || tail.slice(-9) !== "</prompt>") return null;
      var pBody = tail.slice(9, -9);
      if (pBody.indexOf("<") >= 0) return null;
      prompt = ucmdUnescapeXml(pBody);
    }
    return { label: label, edited: edited, cwd: cwd, cmd: cmd, prompt: prompt };
  }
  function splitUcmdParts(text) {
    if (!text || text.indexOf("<ws:ucmd") < 0) return [{ text: text || "" }];
    var tags = [], depth = 0, pos = 0;
    while (pos < text.length) {
      var start = text.indexOf("<", pos);
      if (start < 0) break;
      var body = text.slice(start);
      var term = body.indexOf("<!--") === 0 ? "-->" : body.indexOf("<![CDATA[") === 0 ? "]]>" : body.indexOf("<?") === 0 ? "?>" : "";
      if (!term && body.indexOf("<!") === 0) break;
      if (term) {
        var stop = body.indexOf(term);
        if (stop < 0) break;
        pos = start + stop + term.length; continue;
      }
      var candidate = /^<\/?ws:ucmd(?:-add)?(?:[^A-Za-z0-9_.:-]|$)/.test(body), quote = "", end = start + 1;
      for (; end < text.length; end++) {
        var ch = text.charAt(end);
        if (quote) { if (ch === quote) quote = ""; continue; }
        if (ch === '"' || ch === "'") quote = ch;
        else if (ch === ">" || ch === "<") break;
      }
      if (end === text.length || text.charAt(end) !== ">") {
        if (candidate) { tags.push([start, text.length]); break; }
        if (depth > 0) break;
        pos = start + 1; continue;
      }
      pos = end + 1;
      if (candidate) {
        tags.push([start, pos]);
        if (text.charAt(start + 1) !== "/") depth++;
        else if (depth > 0) depth--;
      }
    }
    var parts = [], last = 0;
    for (var i = 0; i < tags.length; i++) {
      var s = tags[i][0];
      if (text.charAt(s + 1) === "/") continue;
      if (s > last) parts.push({ text: text.slice(last, s) });
      var d = 1, e = text.length;
      for (i++; i < tags.length; i++) {
        d += text.charAt(tags[i][0] + 1) === "/" ? -1 : 1;
        if (!d) { e = tags[i][1]; break; }
      }
      var raw = text.slice(s, e);
      var cmd = (s === 0 || text.charAt(s - 1) === "\n") && (e === text.length || /^\r?\n/.test(text.slice(e))) ? parseUcmdBlock(raw) : null;
      if (cmd) parts.push({ text: raw, ucmd: cmd });
      else parts.push({ text: raw });
      last = e;
    }
    if (last < text.length) parts.push({ text: text.slice(last) });
    return parts;
  }
  function formatUcmdPreview(text) {
    if (!text || text.indexOf("<ws:ucmd") < 0) return text || "";
    var out = [];
    splitUcmdParts(text).forEach(function (p) {
      if (p.ucmd && p.ucmd.add) {
        out.push(p.ucmd.request.replace(/\s+/g, " ").trim());
      } else if (p.ucmd) {
        var cmdLine = p.ucmd.cmd.replace(/\s+/g, " ").trim();
        if (p.ucmd.label && cmdLine) out.push("[" + p.ucmd.label + "] " + cmdLine);
        else if (cmdLine) out.push(cmdLine);
        else if (p.ucmd.label) out.push("[" + p.ucmd.label + "]");
      } else if (p.text && p.text.trim()) {
        out.push(p.text.trim());
      }
    });
    return out.join(" ");
  }
  // outboxRow is an unanswered send's place in the composer: its text on
  // one line, or its image markers when it has no text, and its cancel.
  function outboxRow(m) {
    var el = doc.createElement("div"), spin = doc.createElement("span"), text = doc.createElement("span"), cancel = heldAct("cancel", "");
    el.className = "d-outbox-row";
    spin.className = "spin";
    text.className = "d-outbox-text";
    var rowSummary = typeof stripTextRefs === "function" ? stripTextRefs(m.back.text || "").text : (m.back.text || "");
    rowSummary = formatUcmdPreview(rowSummary);
    text.textContent = rowSummary || m.pics.map(function (p) { return marker(p.n); }).join(" ");
    el.appendChild(spin);
    el.appendChild(text);
    if (cancel) {
      cancel.addEventListener("click", function () { outboxCancel(m); });
      el.appendChild(cancel);
    }
    return el;
  }
  function outboxShowRows() {
    var row = ctext.closest && ctext.closest(".crow");
    if (!outboxRows && row) {
      outboxRows = doc.createElement("div");
      outboxRows.className = "d-outbox";
      outboxRows.setAttribute("aria-busy", "true");
      row.parentNode.insertBefore(outboxRows, row);
    }
    if (!outboxRows) return;
    outbox.forEach(function (m) { if (!m.acked && !m.paused && !m.cancel && m.row.parentNode !== outboxRows) outboxRows.appendChild(m.row); });
    outboxRows.hidden = !outboxRows.firstChild;
  }
  function outboxUnrow(m) {
    if (m.row.parentNode) m.row.parentNode.removeChild(m.row);
    if (outboxRows) outboxRows.hidden = !outboxRows.firstChild;
  }
  // outboxBubble is the held bubble the server will render, built from what
  // was sent: the page's own origin and held labels (the first rendered
  // ones), the text as typed, the image thumbnails, and the held bubble's
  // steer and cancel buttons for the id its post returned (a send is never
  // steered yet), which the conversation's click wiring handles as the
  // server's.
  function outboxBubble(m) {
    var el = doc.createElement("div"), mh = doc.createElement("div"), bub = doc.createElement("div");
    var src = doc.querySelector("#convo .msg .src.wb"), tag = doc.querySelector("#r-convlive .heldtag:not(.steertag)");
    var steer = heldAct("steer", m.id), cancel = heldAct("cancel", m.id);
    el.className = "msg user held";
    el.setAttribute("data-entry", m.id);
    mh.className = "mh";
    if (src) mh.appendChild(src.cloneNode(true));
    if (tag) mh.appendChild(tag.cloneNode(true));
    if (steer) mh.appendChild(steer);
    if (cancel) mh.appendChild(cancel);
    el.appendChild(mh);
    bub.className = "bub";
    // Match the server's inline capsule hydration & achips deduplication using only escaped DOM text.
    var chips = doc.createElement("div"), back = m.back;
    chips.className = "hchips";
    function singleLine(s) { return String(s || "").replace(/\s+/g, " ").trim(); }
    function attachSource(cap, build) {
      var tpl = doc.createElement("template");
      tpl.className = "icapsule-source";
      var root = tpl.content || tpl;
      build(root);
      cap.appendChild(tpl);
    }
    function appendDetailHeader(root, title, mono) {
      var hd = doc.createElement("div"), b = doc.createElement("b");
      hd.className = "icapsule-pop-hd";
      b.className = "icapsule-pop-title" + (mono ? " icapsule-mono" : "");
      b.textContent = title;
      hd.appendChild(b);
      root.appendChild(hd);
    }
    function makePill(kind, headText, valText, subText, titleText, monoHead) {
      var cap = doc.createElement("span"), hd = doc.createElement("b");
      cap.className = "icapsule icapsule-" + kind;
      hd.className = "icapsule-head" + (monoHead ? " icapsule-mono" : "");
      hd.textContent = headText;
      cap.appendChild(hd);
      if (valText) {
        appendText(cap, " ");
        var val = doc.createElement("span");
        val.className = "icapsule-val";
        val.textContent = valText;
        cap.appendChild(val);
      }
      if (subText) {
        var sub = doc.createElement("span");
        sub.className = "icapsule-sub";
        sub.textContent = subText;
        cap.appendChild(sub);
      }
      if (titleText) cap.setAttribute("title", titleText);
      return cap;
    }
    function makeInteractive(cap) {
      cap.setAttribute("data-icapsule", "1");
      cap.setAttribute("role", "button");
      cap.setAttribute("tabindex", "0");
      cap.setAttribute("aria-haspopup", "dialog");
      cap.setAttribute("aria-expanded", "false");
    }
    function appendText(parent, str) {
      if (!str) return;
      if (typeof doc.createTextNode === "function") { parent.appendChild(doc.createTextNode(str)); return; }
      var span = doc.createElement("span");
      span.textContent = str;
      parent.appendChild(span);
    }
    function appendImageTags(parent, text) {
      var re = /\[Image #([1-9][0-9]{0,5})\]/g, last = 0, match;
      while ((match = re.exec(text))) {
        if (match.index > last) appendText(parent, text.slice(last, match.index));
        var tagEl = doc.createElement("span");
        tagEl.className = "imgtag";
        tagEl.setAttribute("title", wsT("imgtag.title", match[1]));
        tagEl.textContent = "#" + match[1];
        parent.appendChild(tagEl);
        last = match.index + match[0].length;
      }
      if (last < text.length) appendText(parent, text.slice(last));
    }
    var backChips = (back.chips || []).slice(), usedChip = [];
    var backTopics = (back.topics || []).slice(), usedTopic = [];
    var backMentions = (back.mentions || []).slice(), usedMention = [];
    var backPicks = (back.picks || []).slice(), usedPick = [];
    var backConsoles = (back.consoles || []).slice(), usedConsole = [];
    var strippedText = typeof stripTextRefs === "function" ? stripTextRefs(m.back.text || "") : { text: m.back.text || "", refs: [] };
    var bubbleTextRefs = strippedText.refs || [], backTextRefs = {};
    bubbleTextRefs.forEach(function (r) { if (!backTextRefs[r.n]) backTextRefs[r.n] = r; });
    function hydrateProseInto(parent, prose) {
      if (!prose) return;
      var wsc = window.WsCapsules;
      var hasMailbox = prose.indexOf("<mailbox ") >= 0 && !!(wsc && typeof wsc.parseMailboxTag === "function");
      if (!backChips.length && !backTopics.length && !backMentions.length && !backPicks.length && !backConsoles.length && !bubbleTextRefs.length && !hasMailbox) {
        appendImageTags(parent, prose);
        return;
      }
      var last = 0, i = 0, mbCtx = null;
      function isWord(ch) { return /[A-Za-z0-9_]/.test(ch); }
      while (i < prose.length) {
        var ch = prose.charAt(i);
        if (ch === "<") {
          if (prose.slice(i, i + 9) === "<textref " && wsc && typeof wsc.skipTextref === "function") {
            var tend = wsc.skipTextref(prose, i);
            if (tend > i) { i = tend; continue; }
          }
          if (hasMailbox && prose.slice(i, i + 9) === "<mailbox ") {
            var mb = wsc.parseMailboxTag(prose, i);
            if (mb) {
              if (i > last) appendImageTags(parent, prose.slice(last, i));
              if (!mbCtx) mbCtx = wsc.viewingContext(doc);
              var mbEl = doc.createElement("span");
              mbEl.className = "icapsule icapsule-mailbox";
              mbEl.setAttribute("title", wsc.formatMailboxTooltip(mb.tag));
              mbEl.textContent = wsc.formatMailboxLabel(mb.tag, mbCtx);
              parent.appendChild(mbEl);
              i = mb.end; last = i; continue;
            }
          }
        } else if (ch === "(" && (i === 0 || !isWord(prose.charAt(i - 1)))) {
          var am = /^\(([0-9]{1,6})\)=([A-Za-z0-9]{1,16})/.exec(prose.slice(i));
          if (am) {
            var ci = -1;
            for (var k = 0; k < backChips.length; k++) {
              if (usedChip[k]) continue;
              var c = backChips[k], want = c.answer || "note";
              if (String(c.id) === am[1] && want === am[2]) { ci = k; break; }
            }
            if (ci >= 0) {
              if (i > last) appendImageTags(parent, prose.slice(last, i));
              var ac = backChips[ci], aval = ac.answer || "note", anote = String(ac.note || "");
              var apill = makePill("answer", "(" + ac.id + ")", aval, anote.trim() ? singleLine(anote) : "", anote);
              if (anote.trim()) {
                makeInteractive(apill);
                attachSource(apill, function (root) {
                  appendDetailHeader(root, "(" + ac.id + ") " + aval);
                  var body = doc.createElement("div"); body.className = "icapsule-pop-text"; body.textContent = anote;
                  root.appendChild(body);
                });
              }
              parent.appendChild(apill);
              usedChip[ci] = true;
              i += am[0].length; last = i; continue;
            }
          }
        } else if (ch === "{" && prose.slice(i, i + 2) === "{{") {
          var pm = /^\{\{(p[1-9][0-9]*)\}\}/.exec(prose.slice(i));
          if (pm) {
            var pid = pm[1], mi = -1;
            for (var mk = 0; mk < backMentions.length; mk++) {
              if (usedMention[mk]) continue;
              var mt = backMentions[mk].text || "";
              if (mt === pid || mt.indexOf(pid + " · ") === 0) { mi = mk; break; }
            }
            if (mi >= 0) {
              if (i > last) appendImageTags(parent, prose.slice(last, i));
              var mn = backMentions[mi], sub = (mn.text || "").indexOf(pid + " · ") === 0 ? mn.text.slice(pid.length + 3) : mn.text;
              var mpill = makePill("mention", pid, "", singleLine(sub), mn.text);
              makeInteractive(mpill);
              attachSource(mpill, function (root) {
                appendDetailHeader(root, "[" + mn.topic + "] " + pid);
                var qEl = doc.createElement("pre"); qEl.className = "icapsule-pop-quote"; qEl.textContent = mn.text || "";
                root.appendChild(qEl);
              });
              parent.appendChild(mpill);
              usedMention[mi] = true;
              i += pm[0].length; last = i; continue;
            }
          }
        } else if (ch === "[") {
          if (prose.slice(i, i + 6) === "[note:") {
            var ti = -1, tlen = -1;
            for (var tk = 0; tk < backTopics.length; tk++) {
              if (usedTopic[tk]) continue;
              var tn = backTopics[tk].topic || "";
              if (tn.length > tlen && prose.slice(i, i + 7 + tn.length) === "[note:" + tn + "]") { ti = tk; tlen = tn.length; }
            }
            if (ti >= 0) {
              if (i > last) appendImageTags(parent, prose.slice(last, i));
              var tc = backTopics[ti];
              var tpill = makePill("topic", "[" + tc.topic + "]", "", singleLine(tc.note), tc.note);
              makeInteractive(tpill);
              attachSource(tpill, function (root) {
                appendDetailHeader(root, "[" + tc.topic + "]");
                var body = doc.createElement("div"); body.className = "icapsule-pop-text"; body.textContent = tc.note || "";
                root.appendChild(body);
              });
              parent.appendChild(tpill);
              usedTopic[ti] = true;
              i += 7 + tlen; last = i; continue;
            }
          } else if (prose.slice(i, i + 9) === "[mention:") {
            var mni = -1, mnlen = -1;
            for (var mnk = 0; mnk < backMentions.length; mnk++) {
              if (usedMention[mnk]) continue;
              var mtop = backMentions[mnk].topic || "";
              if (mtop.length > mnlen && prose.slice(i, i + 10 + mtop.length) === "[mention:" + mtop + "]") { mni = mnk; mnlen = mtop.length; }
            }
            if (mni >= 0) {
              if (i > last) appendImageTags(parent, prose.slice(last, i));
              var mm = backMentions[mni], mhead = "[" + mm.topic + "]" + (mm.id ? " (" + mm.id + ")" : "");
              var mnpill = makePill("mention", mhead, "", singleLine(mm.text), mm.text);
              makeInteractive(mnpill);
              attachSource(mnpill, function (root) {
                appendDetailHeader(root, mhead);
                var qEl = doc.createElement("pre"); qEl.className = "icapsule-pop-quote"; qEl.textContent = mm.text || "";
                root.appendChild(qEl);
              });
              parent.appendChild(mnpill);
              usedMention[mni] = true;
              i += 10 + mnlen; last = i; continue;
            }
          } else if (prose.slice(i, i + 6) === "[pick:") {
            var pki = -1, pklen = -1;
            for (var pk = 0; pk < backPicks.length; pk++) {
              if (usedPick[pk]) continue;
              var psel = backPicks[pk].selector || "";
              if (psel.length > pklen && prose.slice(i, i + 7 + psel.length) === "[pick:" + psel + "]") { pki = pk; pklen = psel.length; }
            }
            if (pki >= 0) {
              if (i > last) appendImageTags(parent, prose.slice(last, i));
              var pp = backPicks[pki], ptitle = pp.url + "\n" + pp.selector + (pp.text ? "\n" + pp.text : "");
              var ppill = makePill("pick", pp.selector, "", singleLine(pp.text), ptitle, true);
              makeInteractive(ppill);
              attachSource(ppill, function (root) {
                appendDetailHeader(root, pp.selector, true);
                var urlEl = doc.createElement("div"); urlEl.className = "icapsule-pop-url"; urlEl.textContent = pp.url || "";
                root.appendChild(urlEl);
                if (pp.text) { var tx = doc.createElement("div"); tx.className = "icapsule-pop-text"; tx.textContent = pp.text; root.appendChild(tx); }
              });
              parent.appendChild(ppill);
              usedPick[pki] = true;
              i += 7 + pklen; last = i; continue;
            }
          } else if (prose.slice(i, i + 9) === "[console:") {
            var cni = -1, cnlen = -1;
            for (var ck = 0; ck < backConsoles.length; ck++) {
              if (usedConsole[ck]) continue;
              var curl = backConsoles[ck].url || "";
              if (curl.length > cnlen && prose.slice(i, i + 10 + curl.length) === "[console:" + curl + "]") { cni = ck; cnlen = curl.length; }
            }
            if (cni >= 0) {
              if (i > last) appendImageTags(parent, prose.slice(last, i));
              var cc = backConsoles[cni], chead = "console ×" + (cc.lines || []).length;
              var ctitle = cc.url + "\n" + (cc.lines || []).join("\n");
              var cpill = makePill("console", chead, "", cc.url, ctitle);
              makeInteractive(cpill);
              attachSource(cpill, function (root) {
                appendDetailHeader(root, chead);
                var urlEl = doc.createElement("div"); urlEl.className = "icapsule-pop-url"; urlEl.textContent = cc.url || "";
                var linesEl = doc.createElement("pre"); linesEl.className = "icapsule-pop-lines"; linesEl.textContent = (cc.lines || []).join("\n");
                root.appendChild(urlEl); root.appendChild(linesEl);
              });
              parent.appendChild(cpill);
              usedConsole[cni] = true;
              i += 10 + cnlen; last = i; continue;
            }
          } else if (bubbleTextRefs.length && prose.slice(i, i + 7) === "[Text #" && typeof makeTextTag === "function") {
            var tm = /^\[Text #([1-9][0-9]{0,5})\]/.exec(prose.slice(i));
            if (tm && backTextRefs[+tm[1]]) {
              if (i > last) appendImageTags(parent, prose.slice(last, i));
              parent.appendChild(makeTextTag(backTextRefs[+tm[1]]));
              i += tm[0].length; last = i; continue;
            }
          }
        }
        i++;
      }
      if (last < prose.length) appendImageTags(parent, prose.slice(last));
    }
    function buildSelectionPill(s) {
      var first = s.mentions[0], head = "[" + first.topic + "]" + (first.id ? " (" + first.id + ")" : first.entry ? " " + first.entry : "");
      var titles = s.mentions.map(function (x) { return x.text; });
      if (s.comment && s.comment.trim()) titles.push(s.comment);
      var pill = makePill("selection", head, "", singleLine(first.text), titles.join("\n"));
      if (s.mentions.length > 1) {
        var cnt = doc.createElement("span"); cnt.className = "icapsule-count"; cnt.textContent = "+" + (s.mentions.length - 1);
        pill.insertBefore(cnt, pill.querySelector ? pill.querySelector(".icapsule-sub") : null);
      }
      if (s.comment && s.comment.trim()) {
        var mk = doc.createElement("span"); mk.className = "icapsule-note-mk"; mk.setAttribute("aria-hidden", "true"); mk.textContent = "\u270e";
        pill.appendChild(mk);
      }
      (s.images || []).forEach(function (n) {
        var imk = doc.createElement("span"); imk.className = "icapsule-img-mk"; imk.textContent = "#" + n;
        pill.appendChild(imk);
      });
      makeInteractive(pill);
      attachSource(pill, function (root) {
        var quotes = doc.createElement("div"); quotes.className = "icapsule-pop-quotes";
        s.mentions.forEach(function (sm) {
          var item = doc.createElement("div"), meta = doc.createElement("div"), b = doc.createElement("b"), qEl = doc.createElement("pre");
          item.className = "icapsule-pop-item"; meta.className = "icapsule-pop-meta";
          b.textContent = "[" + sm.topic + "]" + (sm.id ? " (" + sm.id + ")" : "") + (sm.entry ? " " + sm.entry : "");
          qEl.className = "icapsule-pop-quote"; qEl.textContent = sm.text;
          meta.appendChild(b); item.appendChild(meta); item.appendChild(qEl); quotes.appendChild(item);
        });
        root.appendChild(quotes);
        if (s.comment) {
          var com = doc.createElement("div"); com.className = "icapsule-pop-comment"; com.textContent = s.comment;
          root.appendChild(com);
        }
        if (s.images && s.images.length) {
          var imgsEl = doc.createElement("div"); imgsEl.className = "icapsule-pop-imgs";
          s.images.forEach(function (n) {
            var it = doc.createElement("span"); it.className = "imgtag"; it.textContent = "#" + n;
            imgsEl.appendChild(it);
          });
          root.appendChild(imgsEl);
        }
      });
      return pill;
    }
    function buildUcmdCard(cmd) {
      var card = doc.createElement("div"), hd = doc.createElement("div");
      card.className = cmd.add ? "ucmd-req is-add" : "ucmd-req";
      hd.className = "ucmd-req-hd";
      var ico = doc.createElement("span");
      ico.className = "ucmd-req-ico";
      ico.setAttribute("aria-hidden", "true");
      if (typeof doc.createElementNS === "function") {
        var svg = doc.createElementNS("http://www.w3.org/2000/svg", "svg");
        svg.setAttribute("class", "li");
        svg.setAttribute("aria-hidden", "true");
        var use = doc.createElementNS("http://www.w3.org/2000/svg", "use");
        use.setAttribute("href", "#l-square-terminal");
        svg.appendChild(use);
        ico.appendChild(svg);
      }
      hd.appendChild(ico);
      if (cmd.add) {
        var badge = doc.createElement("span");
        badge.className = "ucmd-req-badge";
        badge.textContent = wsT("js.ucmd.agent.badge");
        hd.appendChild(badge);
        var tool = doc.createElement("code");
        tool.className = "ucmd-req-tool-pill";
        tool.textContent = "session.register_user_command";
        hd.appendChild(tool);
        card.appendChild(hd);
        var req = doc.createElement("div");
        req.className = "ucmd-req-prompt";
        req.textContent = cmd.request;
        card.appendChild(req);
        return card;
      }
      var lbl = doc.createElement("span");
      lbl.className = "ucmd-req-label";
      lbl.textContent = cmd.label || wsT("js.ucmd.ask.header");
      hd.appendChild(lbl);
      if (cmd.edited) {
        var edText = String(wsT("js.ucmd.ask.edited") || "").trim().replace(/^\(+|\)+$/g, "");
        if (edText) {
          var ed = doc.createElement("span");
          ed.className = "ucmd-req-edited";
          ed.textContent = edText;
          hd.appendChild(ed);
        }
      }
      if (cmd.cwd) {
        var cwdEl = doc.createElement("span");
        cwdEl.className = "ucmd-req-cwd";
        cwdEl.setAttribute("title", cmd.cwd);
        cwdEl.textContent = cmd.cwd;
        hd.appendChild(cwdEl);
      }
      card.appendChild(hd);
      var pre = doc.createElement("pre"), code = doc.createElement("code");
      pre.className = "ucmd-req-cmd";
      code.textContent = cmd.cmd;
      pre.appendChild(code);
      card.appendChild(pre);
      if (cmd.prompt) {
        var pr = doc.createElement("div");
        pr.className = "ucmd-req-prompt";
        pr.textContent = cmd.prompt;
        card.appendChild(pr);
      }
      return card;
    }
    function hydrateUcmdOrProseInto(parent, text, prevS, nextS) {
      var uparts = text && text.indexOf("<ws:ucmd") >= 0 ? splitUcmdParts(text) : [{ text: text || "" }];
      uparts.forEach(function (up, idx) {
        if (up.ucmd) { parent.appendChild(buildUcmdCard(up.ucmd)); return; }
        var ps = idx === 0 && !!prevS, ns = idx === uparts.length - 1 && !!nextS;
        var t = up.text, hp = false, hn = false;
        if (ps) { var t1 = t.replace(/^(?:\r?\n){1,2}/, ""); hp = t1 !== t; t = t1; }
        if (ns) { var t2 = t.replace(/(?:\r?\n){1,2}$/, ""); hn = t2 !== t; t = t2; }
        if (idx > 0 && uparts[idx - 1].ucmd) t = t.replace(/^(?:\r?\n){1,2}/, "");
        if (idx + 1 < uparts.length && uparts[idx + 1].ucmd) t = t.replace(/(?:\r?\n){1,2}$/, "");
        if (!t) { if (ps && ns) appendText(parent, " "); return; }
        if (hp && !/^\s/.test(t)) t = " " + t;
        if (hn && !/\s$/.test(t)) t = t + " ";
        hydrateProseInto(parent, t);
      });
    }
    var part = null;
    if (strippedText.text) {
      part = doc.createElement("div");
      part.className = "hpart";
      var raw = strippedText.text;
      if (raw.indexOf("<wb:selection") >= 0 && typeof selectionTags === "function" && typeof readSelectionBlock === "function") {
        var tags = selectionTags(raw), segs = [], pos = 0;
        for (var ti = 0; ti < tags.length; ti++) {
          var start = tags[ti][0];
          if (raw.charAt(start + 1) === "/") continue;
          if (start > pos) segs.push({ text: raw.slice(pos, start) });
          var depth = 1, end = raw.length;
          for (ti++; ti < tags.length; ti++) {
            depth += raw.charAt(tags[ti][0] + 1) === "/" ? -1 : 1;
            if (!depth) { end = tags[ti][1]; break; }
          }
          var block = raw.slice(start, end);
          var sel = (start === 0 || raw.charAt(start - 1) === "\n") && (end === raw.length || /^\r?\n/.test(raw.slice(end))) ? readSelectionBlock(block) : null;
          if (sel) segs.push({ selection: sel });
          else segs.push({ text: block, plain: true });
          pos = end;
        }
        if (pos < raw.length) segs.push({ text: raw.slice(pos) });
        segs.forEach(function (sg, idx) {
          if (sg.selection) { part.appendChild(buildSelectionPill(sg.selection)); return; }
          if (sg.plain) { appendText(part, sg.text); return; }
          hydrateUcmdOrProseInto(part, sg.text, idx > 0 && !!segs[idx - 1].selection, idx + 1 < segs.length && !!segs[idx + 1].selection);
        });
      } else {
        hydrateUcmdOrProseInto(part, raw);
      }
    }
    function chip(cls, label, value, note, title) {
      var el = doc.createElement("span"), head = doc.createElement("b");
      el.className = "achip" + (cls ? " " + cls : "");
      head.textContent = label;
      el.appendChild(head);
      if (value) {
        var answer = doc.createElement("span");
        answer.textContent = " " + value;
        el.appendChild(answer);
      }
      if (note) {
        var detail = doc.createElement("span");
        detail.className = "anote";
        detail.textContent = note;
        el.appendChild(detail);
      }
      if (title || note) el.setAttribute("title", title || note);
      chips.appendChild(el);
    }
    backChips.forEach(function (c, i) { if (!usedChip[i]) chip("", "(" + c.id + ")", c.answer || "note", c.note); });
    backTopics.forEach(function (n, i) { if (!usedTopic[i]) chip("tchip", "[" + n.topic + "]", "", n.note); });
    backMentions.forEach(function (n, i) { if (!usedMention[i]) chip("mchip", "[" + n.topic + "]" + (n.id ? " (" + n.id + ")" : ""), "", n.text); });
    (back.closeRequests || []).forEach(function (r) { chip("close-request-chip", wsT("js.closerequest.label", r.topic), "", r.reason); });
    backPicks.forEach(function (p, i) { if (!usedPick[i]) chip("mchip pchip", p.selector, "", p.text, p.url + "\n" + p.selector + (p.text ? "\n" + p.text : "")); });
    backConsoles.forEach(function (c, i) { if (!usedConsole[i]) chip("mchip cchip", "console ×" + c.lines.length, "", c.url, c.url + "\n" + c.lines.join("\n")); });
    if (chips.firstChild) bub.appendChild(chips);
    if (part && part.firstChild) bub.appendChild(part);
    if (m.pics.length || bubbleTextRefs.length) {
      var th = doc.createElement("div");
      th.className = "thumbs";
      m.pics.forEach(function (p) {
        var a = doc.createElement("span"), im = doc.createElement("img"), num = doc.createElement("span");
        a.className = "thumb";
        im.src = p.url;
        im.alt = p.name;
        num.className = "tnum";
        num.textContent = "#" + p.n;
        a.appendChild(im);
        a.appendChild(num);
        th.appendChild(a);
      });
      if (typeof makeTextRefThumb === "function") {
        bubbleTextRefs.forEach(function (r) { th.appendChild(makeTextRefThumb(r)); });
      }
      bub.appendChild(th);
    }
    if (bub.firstChild) el.appendChild(bub);
    return el;
  }
  function nextDeliveryID() {
    return Date.now().toString(36) + "-" + Math.random().toString(36).slice(2) + "-" + ++deliverySequence;
  }
  function outboxAdd(m) {
    m.recoveryID = nextDeliveryID();
    m.recoveryState = "queued";
    m.row = outboxRow(m);
    outbox.push(m);
    outboxShowRows();
    followConvo();
    if (outboxPosting) saveDraft();
    else outboxPump();
  }
  function restoreDeliveries(deliveries) {
    if (!ctext || !deliveries.length) return;
    deliveries.forEach(function (m) {
      m.pics = m.pics.map(loadDraftImage);
      m.pics.forEach(function (p) { imgSeq = Math.max(imgSeq, p.n); });
      outbox.push({ recoveryID: m.id, recoveryState: m.state, body: m.body,
        pics: m.pics, back: m.back, own: [], user: lastUserEntry(), paused: true, row: outboxRow(m) });
    });
    showRecoveredDeliveries();
  }
  function showRecoveredDeliveries() {
    var paused = outbox.filter(function (m) { return m.paused; });
    if (!recoveredRows && !paused.length) return;
    if (!recoveredRows) {
      var row = ctext.closest(".crow");
      if (!row) return;
      recoveredRows = doc.createElement("div");
      recoveredRows.className = "d-outbox d-outbox-recovery";
      row.parentNode.insertBefore(recoveredRows, row);
    }
    recoveredRows.replaceChildren();
    recoveredRows.hidden = !paused.length;
    paused.forEach(function (m) {
      var row = doc.createElement("div"), title = doc.createElement("strong"), text = doc.createElement("span");
      row.className = "d-outbox-row";
      title.textContent = wsT(m.recoveryState === "queued" ? "js.delivery.unsent" : "js.delivery.unknown");
      text.className = "d-outbox-text";
      var recSummary = typeof stripTextRefs === "function" ? stripTextRefs(m.back.text || "").text : (m.back.text || "");
      recSummary = formatUcmdPreview(recSummary);
      text.textContent = recSummary || m.pics.map(function (p) { return marker(p.n); }).join(" ") ||
        (m.back.chips || []).map(function (c) { return c.note || c.answer || c.id; }).join(" · ") ||
        (m.back.topics || []).map(function (n) { return n.note || n.topic; }).join(" · ");
      row.append(title, text);
      function action(label, click) {
        var button = doc.createElement("button");
        button.type = "button";
        button.textContent = wsT(label);
        button.addEventListener("click", click);
        row.appendChild(button);
      }
      action(m.recoveryState === "queued" ? "js.delivery.send" : "js.delivery.resend", async function () {
        if (!m.paused || outbox.indexOf(m) < 0) return;
        if (m.recoveryState !== "queued" && !await window.wsConfirm(wsT("js.delivery.confirm"), {
          accept: wsT("js.delivery.resend"), opener: doc.activeElement
        })) return;
        if (!m.paused || outbox.indexOf(m) < 0) return;
        m.paused = false;
        m.cancel = false;
        m.sent = false;
        if (m.recoveryState !== "queued") m.recoveryID = nextDeliveryID();
        m.user = lastUserEntry();
        showRecoveredDeliveries();
        outboxShowRows();
        outboxPump();
        ctext.focus();
      });
      action("js.delivery.discard", function () {
        if (!m.paused || outbox.indexOf(m) < 0) return;
        outboxDrop(m);
        showRecoveredDeliveries();
        saveDraft();
        ctext.focus();
      });
      recoveredRows.appendChild(row);
    });
  }
  function pauseDeliveries(m, state) {
    m.recoveryState = state;
    outbox.forEach(function (entry) {
      if (entry.acked) return;
      entry.paused = true;
      outboxUnrow(entry);
    });
    showRecoveredDeliveries();
    saveDraft();
  }
  function prepareDelivery(m, submit, fail) {
    var uploads = m.pics.filter(function (p) { return !p.ref; });
    if (!uploads.length) { submit(m.body); return; }
    Promise.all(uploads.map(async function (p) {
      return { name: p.name, data: p.data === null ? await imageData(p.blob) : p.data, n: p.n };
    })).then(function (images) {
      submit(Object.assign({}, m.body, { images: images }));
    }, function () { fail(false, wsT("js.send.reading")); });
  }
  function persistOutbox(done) { saveDraft().then(done); }
  function outboxPump() {
    if (outboxPosting || draftDetached) return;
    var m = outbox.find(function (x) { return !x.acked && !x.paused; });
    if (!m) return;
    outboxPosting = true;
    var previous = m.recoveryState, settled = false, wait = null;
    m.recoveryState = "posting";
    m.cancelPreparation = function () {
      settled = true;
      outboxPosting = false;
      outboxPump();
    };
    function settle(ok, res, unknown) {
      if (settled || draftDetached) return;
      settled = true;
      clearTimeout(wait);
      outboxPosting = false;
      if (unknown || !ok && previous !== "queued") {
        pauseDeliveries(m, "unknown");
        if (!unknown) noteWarn(wsT("js.send.fail", res));
        return;
      }
      if (!ok) { outboxFail(res); return; }
      if (!res || typeof res.id !== "string" || !/^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-f]{8}$/.test(res.id)) {
        pauseDeliveries(m, "unknown");
        return;
      }
      outboxAcked(m, res.id);
      outboxPump();
    }
    persistOutbox(function (saved) {
      if (settled || draftDetached) return;
      if (m.recoveryConflict) {
        outboxUnrow(m);
        outboxDrop(m);
        releaseTopics(m);
        saveDraft();
        m.cancelPreparation();
        return;
      }
      if (!saved) {
        outboxPosting = false;
        pauseDeliveries(m, previous);
        noteWarn(wsT("js.delivery.savefailed"));
        return;
      }
      prepareDelivery(m, function (body) {
        if (settled || draftDetached) return;
        m.sent = true;
        m.live = doc.querySelector("#r-convlive .clive");
        wait = setTimeout(function () { settle(false, wsT("js.post.unreachable"), true); }, OUTBOX_WAIT);
        post("message", body, settle);
      }, settle);
    });
  }
  function outboxAcked(m, id) {
    var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("composer", "outboxAcked") : null;
    try {
      m.acked = true;
      m.id = id;
      m.at = Date.now();
      outboxUnrow(m);
      if (m.body.command) outboxDrop(m);
      else {
        var live = doc.querySelector("#r-convlive .clive");
        // A replacement empty live region already reflects an authoritative
        // render; do not resurrect the held message after that render.
        var emptyRender = m.live && live && m.live !== live && !Array.prototype.some.call(live.children, function (el) { return el.classList.contains("held"); });
        if (lastUserEntry() !== m.user || emptyRender) outboxDrop(m);
        else {
          m.el = outboxBubble(m);
          m.lingerTimer = setTimeout(function () { outboxDrop(m); }, OUTBOX_LINGER);
        }
        if (typeof boardScheduleFlushViewed === "function") boardScheduleFlushViewed();
        else if (typeof requestAnimationFrame === "function") {
          if (!outboxAcked._flushRaf) {
            outboxAcked._flushRaf = requestAnimationFrame(function () {
              outboxAcked._flushRaf = 0;
              boardFlushViewed(false);
            });
          }
        } else {
          boardFlushViewed(false);
        }
      }
      // A fresh draft numbers its images from 1 again once nothing, sent or
      // not, could name a number.
      if (!outboxUnsent() && !imgs.length && !(typeof textRefs !== "undefined" && textRefs.length) && !ctext.value && !chipList().length && !topicNotes().length) {
        imgSeq = 0;
        if (typeof txtSeq === "number") txtSeq = 0;
      }
      saveDraft();
      outboxSync();
      followConvo();
      if (m.cancel) cancelEntry(id, function () { m.cancel = false; outboxSync(); followConvo(); });
    } finally {
      if (jTok) WsJank.end(jTok);
    }
  }
  function outboxDrop(m) {
    var i = outbox.indexOf(m);
    if (i < 0) return;
    outbox.splice(i, 1);
    clearTimeout(m.lingerTimer);
    if (m.el && m.el.parentNode) m.el.parentNode.removeChild(m.el);
    m.pics.forEach(function (p) { if (imgs.indexOf(p) < 0 && imgTrash[p.n] !== p) release(p); });
  }
  function outboxFail(res) {
    var saved = outboxReturn(outbox.filter(function (m) { return !m.acked && !m.paused; }));
    noteWarn(wsT("js.send.fail", res));
    if (res === "board-replaced") {
      // Preserve returned text; boardDraft strips stale IDs after reload.
      saved.then(function (ok) { if (ok && !draftDetached) window.location.reload(); });
    }
  }
  // outboxReturn takes unanswered sends out of the outbox and back into the
  // composer, in order, ahead of what was typed since.
  function releaseTopics(m) {
    m.own.forEach(function (k) { if (!outbox.some(function (o) { return o.own.indexOf(k) >= 0; })) delete boardOwn[k]; });
  }
  function outboxReturn(back) {
    back.forEach(function (m) { outbox.splice(outbox.indexOf(m), 1); });
    back.forEach(function (m) {
      outboxUnrow(m);
      // A topic a send still in the outbox names stays its own.
      releaseTopics(m);
      m.pics.forEach(function (p) { if (imgs.indexOf(p) < 0) imgs.push(p); });
    });
    restoreHeld(back.map(function (m) { return m.back; }));
    return Promise.resolve(saveDraft());
  }
  // outboxCancel is an unanswered send's cancel: one not posted yet comes
  // straight back into the composer; one whose post is out leaves its row
  // and cancels on the daemon once the OK names its id (outboxAcked), or
  // comes back with the failure if the post fails.
  function outboxCancel(m) {
    if (m.acked || m.cancel || outbox.indexOf(m) < 0) return;
    if (m.sent) { m.cancel = true; outboxUnrow(m); return; }
    outboxReturn([m]);
    if (m.cancelPreparation) m.cancelPreparation();
    noteOk(wsT("js.cancel.ok"));
    ctext.focus();
  }
  // cancelEntry takes the held message id back from the daemon into the
  // composer and drops the outbox bubble for it, so no sync puts it back.
  // A refusal (the session already received it) runs failed and warns.
  function cancelEntry(id, failed) {
    post("cancel", { id: id }, function (ok, res) {
      if (!ok) { failed(); noteWarn(wsT("js.cancel.fail", res)); return; }
      outbox.filter(function (m) { return m.acked && m.id === id; }).forEach(outboxDrop);
      restoreHeld([res]);
      noteOk(wsT("js.cancel.ok"));
      ctext.focus();
    });
  }
  // outboxSync drops the bubbles a render now shows (by id) or the session
  // already received, and puts the rest back, in send order, after the
  // held bubbles. Called after each convlive and entries swap.
  function outboxSync() {
    var shown = outbox.filter(function (m) { return m.acked; });
    if (!shown.length) return;
    var c = doc.querySelector("#r-convlive .clive"), kids = c ? Array.prototype.slice.call(c.children) : [];
    var user = lastUserEntry(), now = Date.now();
    var held = function (m) { return m.id && kids.some(function (k) { return k !== m.el && k.classList.contains("held") && k.getAttribute("data-entry") === m.id; }); };
    shown = shown.filter(function (m) {
      if (user === m.user && now - m.at <= OUTBOX_LINGER && !held(m)) return true;
      outboxDrop(m);
      return false;
    });
    if (!c) return;
    // A bubble whose cancel is out stays hidden (outboxAcked).
    shown = shown.filter(function (m) { return !m.cancel; });
    var at = kids.filter(function (k) { return k.classList.contains("sentdata"); })[0] || null;
    if (shown.some(function (m) { return m.el.parentNode !== c; })) shown.forEach(function (m) { c.insertBefore(m.el, at); });
  }
