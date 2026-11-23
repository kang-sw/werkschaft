// Composer `!` commands: a message whose very first character is `!` is
// posted to the session's composer-exec route (never to message) and shows a
// private result card. The server owns the grammar; this file only sends the
// raw first-character candidate before any trim, mention or selection
// assembly. Nothing here falls back to sending the text as a prompt: a daemon
// that does not know the route refuses, and the draft stays.
  var cexecEnabled = !!(composer && composer.getAttribute("data-cexec") === "1" && rid && window.fetch);
  var cexecCards = [], cexecById = {}, cexecBox = null, cexecPending = null, cexecBusy = false, cexecToken = "", cexecTrack = null, cexecStoreKey = "ws.cexec." + rid, cexecAttemptKey = "ws.cexec.attempt." + rid;
  function cexecNewID() {
    var bytes = new Uint8Array(12);
    if (window.crypto && window.crypto.getRandomValues) window.crypto.getRandomValues(bytes);
    else for (var i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
    return "cx" + Array.prototype.map.call(bytes, function (b) { return (b < 16 ? "0" : "") + b.toString(16); }).join("");
  }
  // The card list survives a reload in sessionStorage (the id and the typed
  // command only); the daemon answers each id's state, and an id it no longer
  // knows shows "outcome unknown" and is never run again.
  function cexecSave() {
    try {
      window.sessionStorage.setItem(cexecStoreKey, JSON.stringify(cexecCards.map(function (c) { return { id: c.id, command: c.command }; })));
    } catch (e) {}
  }
  function cexecLoad() {
    try {
      var raw = JSON.parse(window.sessionStorage.getItem(cexecStoreKey) || "[]");
      return Array.isArray(raw) ? raw.filter(function (c) { return c && typeof c.id === "string" && typeof c.command === "string"; }).slice(-32) : [];
    } catch (e) { return []; }
  }
  // The attempt in flight (its submission id and raw text) is recorded before
  // the request goes out and cleared once the daemon's answer settles it. An
  // attempt that outlives a reload or a lost response is a retry: it asks the
  // daemon for that id's card and never starts the command again.
  function cexecSaveAttempt(a) {
    try {
      if (a) window.sessionStorage.setItem(cexecAttemptKey, JSON.stringify({ id: a.id, text: a.text }));
      else window.sessionStorage.removeItem(cexecAttemptKey);
    } catch (e) {}
  }
  function cexecLoadAttempt() {
    try {
      var a = JSON.parse(window.sessionStorage.getItem(cexecAttemptKey) || "null");
      return a && typeof a.id === "string" && typeof a.text === "string" ? a : null;
    } catch (e) { return null; }
  }
  cexecPending = cexecEnabled ? cexecLoadAttempt() : null;
  // The results are one console window: a bar (bash, run count, the newest
  // run when folded, clear) over a scrollback of runs. It folds to the bar
  // alone while no run is active, unless the user opened it or a run was just
  // submitted; the bar button toggles it.
  var cexecLog = null, cexecCount = null, cexecMini = null, cexecChev = null, cexecOpen = true, cexecRestoring = false, cexecRestoreAt = 0;
  function cexecContainer() {
    if (cexecBox) return cexecBox;
    cexecBox = doc.createElement("div");
    cexecBox.className = "d-cexec";
    cexecBox.hidden = true;
    var bar = doc.createElement("div");
    bar.className = "cexec-bar";
    var head = doc.createElement("button");
    head.type = "button";
    head.className = "cexec-head";
    var sh = doc.createElement("span");
    sh.className = "cexec-sh";
    sh.textContent = "bash";
    cexecCount = doc.createElement("span");
    cexecCount.className = "cexec-count";
    cexecMini = doc.createElement("span");
    cexecMini.className = "cexec-mini";
    cexecChev = doc.createElement("span");
    cexecChev.className = "cexec-chev";
    head.appendChild(sh); head.appendChild(cexecCount); head.appendChild(cexecMini); head.appendChild(cexecChev);
    head.addEventListener("click", function () {
      cexecOpen = cexecBox.hasAttribute("data-folded");
      cexecSync();
    });
    var clear = doc.createElement("button");
    clear.type = "button";
    clear.className = "cexec-clear";
    clear.textContent = wsT("js.cexec.clear");
    // Clearing closes every run through the same per-run close.
    clear.addEventListener("click", function () { cexecCards.slice().forEach(cexecDismiss); });
    bar.appendChild(head); bar.appendChild(clear);
    cexecLog = doc.createElement("div");
    cexecLog.className = "cexec-log";
    cexecLog.setAttribute("role", "log");
    cexecLog.setAttribute("aria-label", wsT("js.cexec.aria"));
    cexecBox.appendChild(bar); cexecBox.appendChild(cexecLog);
    var anchor = composer.querySelector(".cimgs");
    composer.insertBefore(cexecBox, anchor || composer.firstChild);
    return cexecBox;
  }
  function cexecTone(card) {
    if (card.unknown || card.phase === "refused" || card.state === "failed") return "fail";
    if (card.phase === "running" || card.phase === "async") return "run";
    if (card.state === "stopped" || card.state === "lost" || typeof card.signal === "number") return "warn";
    if (typeof card.exit_code === "number" && card.exit_code !== 0) return "fail";
    return "ok";
  }
  var cexecMarks = { ok: "✓", fail: "✗", warn: "!", run: "…" };
  function cexecBytes(n) {
    if (n < 1024) return n + " B";
    if (n < 1048576) return (n / 1024).toFixed(1) + " KiB";
    return (n / 1048576).toFixed(1) + " MiB";
  }
  // cexecSync refreshes the console frame from the card list: writes only, no
  // measuring.
  function cexecSync() {
    if (!cexecBox) return;
    var n = cexecCards.length, running = cexecCards.some(function (c) { return c.card.phase === "running" || c.card.phase === "async"; });
    cexecBox.hidden = !n;
    if (!n) return;
    var folded = !cexecOpen && !running;
    cexecBox.toggleAttribute("data-folded", folded);
    cexecLog.hidden = folded;
    cexecChev.textContent = folded ? "▸ " + wsT("js.cexec.expand") : "▾ " + wsT("js.cexec.collapse");
    cexecCount.textContent = "· " + wsT("js.cexec.runs", String(n));
    var last = cexecCards[n - 1], tone = cexecTone(last.card);
    cexecMini.setAttribute("data-tone", tone);
    cexecMini.textContent = cexecMarks[tone] + " " + last.command;
  }
  function cexecAdd(id, command, card) {
    var c = cexecById[id];
    if (!c) {
      c = { id: id, command: command, card: card || { id: id, command: command, phase: "running" }, el: null, polls: 0, timer: 0 };
      cexecById[id] = c;
      // A restored card is older than every card already here (a fresh run
      // that finished before the restore read returned), so it keeps its place
      // ahead of them; saved history follows the same order.
      if (cexecRestoring) cexecCards.splice(cexecRestoreAt++, 0, c);
      else cexecCards.push(c);
      cexecSave();
    } else if (card) c.card = card;
    // A run just submitted opens the console; a restored one leaves it folded.
    if (!cexecRestoring) cexecOpen = true;
    cexecRender(c);
    if (!cexecRestoring && !c.shown) { c.shown = true; if (c.el.scrollIntoView) c.el.scrollIntoView({ block: "nearest" }); }
    cexecWatch(c);
    return c;
  }
  function cexecStateText(card) {
    if (card.unknown) return wsT("js.cexec.unknown");
    if (card.phase === "refused") return wsT("js.cexec.refused", card.error || "");
    if (card.phase === "running") return wsT("js.cexec.running");
    if (card.phase === "async") return wsT("js.cexec.async");
    if (card.state === "stopped") return wsT("js.cexec.stopped");
    if (card.state === "failed") return wsT("js.cexec.failed");
    if (card.state === "lost") return wsT("js.cexec.lost");
    if (typeof card.signal === "number") return wsT("js.cexec.signal", String(card.signal));
    if (typeof card.exit_code === "number") return wsT("js.cexec.exit", String(card.exit_code));
    return wsT("js.cexec.done");
  }
  function cexecRender(c) {
    var box = cexecContainer(), card = c.card, el = c.el, tone = cexecTone(card);
    if (!el) {
      el = c.el = doc.createElement("div");
      el.className = "cmdrun cexec";
      el.setAttribute("data-cexec", c.id);
      // Cards are chronological: a restored (older) card goes before any run
      // that finished while its read was in flight.
      var next = cexecCards[cexecCards.indexOf(c) + 1];
      cexecLog.insertBefore(el, next && next.el && next.el.parentNode === cexecLog ? next.el : null);
    }
    el.setAttribute("data-phase", card.unknown ? "unknown" : card.phase);
    el.setAttribute("data-tone", tone);
    el.replaceChildren();
    var echo = doc.createElement("div");
    echo.className = "cmdecho";
    var mk = doc.createElement("span");
    mk.className = "mk";
    mk.textContent = "$";
    var code = doc.createElement("code");
    code.textContent = c.command;
    var close = doc.createElement("button");
    close.type = "button";
    close.className = "cexec-x";
    close.textContent = "×";
    close.title = wsT("js.cexec.dismiss");
    close.setAttribute("aria-label", wsT("js.cexec.dismiss"));
    close.addEventListener("click", function () { cexecDismiss(c); });
    echo.appendChild(mk);
    if (card.label) {
      // A user command's click: its label leads the exact command it ran.
      var lbl = doc.createElement("span");
      lbl.className = "cexec-label";
      lbl.textContent = card.label;
      echo.appendChild(lbl);
    }
    echo.appendChild(code); echo.appendChild(close);
    el.appendChild(echo);
    function note(text) {
      var n = doc.createElement("div");
      n.className = "cmdnote";
      n.textContent = text;
      el.appendChild(n);
    }
    if (card.error && card.phase !== "refused") note(card.error);
    if (card.phase === "done" || (card.phase === "async" && card.output)) {
      if (card.output) {
        var pre = doc.createElement("pre");
        pre.className = "cmdout";
        pre.textContent = card.output;
        el.appendChild(pre);
      } else if (!card.file && !card.file_error && !card.output_bytes) note(wsT("js.cexec.nooutput"));
    }
    if (card.file) {
      var row = doc.createElement("div");
      row.className = "cexec-file";
      var arrow = doc.createElement("span");
      arrow.className = "cexec-arrow";
      arrow.textContent = "→";
      var link = doc.createElement("a");
      link.href = card.file_url;
      link.target = "_blank";
      link.rel = "noopener";
      link.textContent = wsT("js.cexec.file") + ": " + card.file;
      var copy = doc.createElement("button");
      copy.type = "button";
      copy.className = "cexec-copy";
      copy.textContent = wsT("js.cexec.copypath");
      copy.addEventListener("click", function () { copyText(card.file); });
      row.appendChild(arrow); row.appendChild(link); row.appendChild(copy);
      el.appendChild(row);
    }
    if (card.file_error) note(card.file_error);
    if (card.truncated) note(wsT("js.cexec.truncated", String(card.truncated)));
    if (card.replaced) note(wsT("js.cexec.replaced"));
    // The status line: mark and outcome, then the byte count and job id the
    // card already carries (no time until the server supplies one).
    var stl = doc.createElement("div");
    stl.className = "cexec-stl";
    var state = doc.createElement("span");
    state.className = "cexec-state";
    state.textContent = cexecMarks[tone] + " " + cexecStateText(card);
    stl.appendChild(state);
    function part(node) {
      var dot = doc.createElement("i");
      dot.textContent = "·";
      stl.appendChild(dot); stl.appendChild(node);
    }
    if (typeof card.output_bytes === "number" && card.output_bytes > 0) {
      var by = doc.createElement("span");
      by.className = "cexec-bytes";
      by.textContent = cexecBytes(card.output_bytes);
      part(by);
    }
    if (card.exec_id) {
      var job = doc.createElement("span");
      job.className = "cmdnote cexec-job";
      job.textContent = wsT("js.cexec.job", card.exec_id);
      part(job);
    }
    el.appendChild(stl);
    cexecSync();
  }
  function cexecDismiss(c) {
    clearTimeout(c.timer);
    c.dead = true;
    if (c.el && c.el.parentNode) c.el.parentNode.removeChild(c.el);
    delete cexecById[c.id];
    cexecCards.splice(cexecCards.indexOf(c), 1);
    cexecSave();
    cexecSync();
  }
  // An active card refreshes from the daemon's owner-checked job state with
  // a growing delay, and stops once it is terminal.
  function cexecWatch(c) {
    clearTimeout(c.timer);
    var phase = c.card.phase;
    if (c.dead || c.card.unknown || (phase !== "running" && phase !== "async")) return;
    c.polls++;
    c.timer = setTimeout(function () { cexecPoll(c); }, Math.min(5000, 700 + c.polls * 400));
  }
  function cexecPoll(c) {
    if (c.dead) return;
    fetch("/s/" + rid + "/composer-exec/" + encodeURIComponent(c.id), { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (r.status === 404) { c.card = { id: c.id, command: c.command, phase: "done", unknown: true }; cexecRender(c); return null; }
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    }).then(function (card) {
      if (!card || c.dead) return;
      c.card = card;
      cexecRender(c);
      cexecWatch(c);
    }, function () { cexecWatch(c); });
  }
  // One bounded refresh on page load: the daemon's cards for this session,
  // then each remembered id the daemon no longer knows.
  function cexecRestore() {
    if (!cexecEnabled) return;
    var remembered = cexecLoad();
    // Remembered commands are shown only once the daemon's private list read
    // succeeded: a denied or failed read (a LAN page, a closed lead, an old
    // daemon) renders nothing of them. The same read hands the page the
    // admission token its fresh submissions carry.
    cexecList(function (list) {
      if (!list) return;
      var known = {};
      (list.cards || []).forEach(function (card) { known[card.id] = card; });
      var fresh = cexecCards.length > 0;
      cexecRestoring = true;
      cexecRestoreAt = 0;
      remembered.forEach(function (m) {
        if (cexecById[m.id]) return;
        cexecAdd(m.id, m.command, known[m.id] || { id: m.id, command: m.command, phase: "done", unknown: true });
      });
      cexecRestoring = false;
      // Restored history starts folded to the bar; a run still active shows.
      if (cexecCards.length && !fresh) { cexecOpen = false; cexecSync(); }
    });
  }
  // cexecList reads the daemon's private card list once and keeps its admission
  // token; done gets the list, or null when the read was denied or failed.
  function cexecList(done) {
    // A transport failure, and a body that fails to read or parse after good
    // headers, both end as null: done always runs, so a caller that waits on it
    // (a send needing the token) clears its busy state and keeps the draft.
    // The cause of a failed read (the HTTP status and the server's short
    // reason, or "network error") is delivered with this read's own result, so a
    // send that needed the token can tell the user why instead of only that the
    // dashboard is unreachable, and overlapping reads never share a cause.
    var why = "";
    fetch("/s/" + rid + "/composer-exec", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (r.ok) return r.json();
      return r.text().then(function (s) { return s; }, function () { return ""; }).then(function (s) {
        why = r.status + (s && s.trim() ? ": " + s.trim().slice(0, 120) : "");
        return null;
      });
    }, function () { why = wsT("js.cexec.neterr"); return null; }).catch(function () { why = why || wsT("js.cexec.badreply"); return null; }).then(function (list) {
      if (!list || !Array.isArray(list.cards)) { done(null, why || wsT("js.cexec.badreply")); return; }
      if (typeof list.token === "string" && list.token) cexecToken = list.token;
      done(list, "");
    });
  }
  // cexecUnreachable is the warning for a send the dashboard could not take,
  // with the recorded cause when one is known.
  function cexecUnreachable(why) {
    return why ? wsT("js.post.unreachable.why", why) : wsT("js.post.unreachable");
  }
  // cexecAttached reports whether the draft carries any composer state beyond
  // its plain text: images, answer chips, topic notes, board selections,
  // mentions, picks, console references, close requests, a selected command, or
  // an inline capsule. A command takes none of it; a regular send does.
  function cexecAttached() {
    try {
      if (typeof imgs !== "undefined" && imgs.length) return true;
      if (typeof chipList === "function" && chipList().length) return true;
      if (typeof topicNotes === "function" && topicNotes().length) return true;
      if (typeof bmentions !== "undefined" && bmentions && bmentions.length) return true;
      if (typeof bselections !== "undefined" && bselections && bselections.length) return true;
      if (typeof wpicks !== "undefined" && wpicks && wpicks.length) return true;
      if (typeof wconsoles !== "undefined" && wconsoles && wconsoles.length) return true;
      if (typeof closeRequests !== "undefined" && closeRequests && closeRequests.length) return true;
      if (typeof cmdSel !== "undefined" && cmdSel) return true;
      if (typeof inlineEditor === "function") {
        var edit = inlineEditor();
        if (edit && edit.ordered().length) return true;
      }
    } catch (e) { return true; }
    return false;
  }
  // The draft's edits while a command is pending are tracked, so an
  // acknowledgement can tell the draft it acknowledged from a newer one. Only
  // plain appends at the very end keep it "intact"; any other edit (a
  // replacement, a deletion, an insertion inside, whatever the text ends up as,
  // even the same text typed again) makes the draft a different revision.
  if (cexecEnabled && ctext) {
    var cexecSel = null;
    // The edit happens in the composer's editor (or the plain textarea); the
    // caret is read from the field before the browser applies it.
    doc.addEventListener("beforeinput", function (e) {
      var editor = ctext.wsc && ctext.wsc.editor;
      if (e.target !== ctext && (!editor || e.target !== editor)) return;
      cexecSel = cexecTrack ? [ctext.selectionStart, ctext.selectionEnd, ctext.value.length] : null;
    }, true);
    ctext.addEventListener("input", function () {
      var t = cexecTrack;
      if (!t || !t.intact) return;
      var sel = cexecSel;
      cexecSel = null;
      var append = sel && sel[0] === sel[1] && sel[0] === sel[2] && sel[2] === t.cur.length && ctext.value.length >= t.cur.length && ctext.value.indexOf(t.cur) === 0;
      if (append) t.cur = ctext.value; else t.intact = false;
    });
  }
  // cexecAcknowledged clears the draft the daemon acknowledged and nothing
  // more: while the draft is still that revision (only appended to since) the
  // acknowledged text goes, with its mentions when nothing was appended and
  // what was appended stays; a draft replaced, edited elsewhere, or begun anew
  // meanwhile, whatever its text, is left whole.
  function cexecAcknowledged(text, track) {
    var now = ctext.value;
    if (!track || !track.intact || now.indexOf(text) !== 0) return;
    if (now === text) {
      ctext.value = ""; grow(); clearMentions();
    } else {
      ctext.value = now.slice(text.length); grow();
    }
    bangSync();
    saveDraft();
  }
  // cexecSend takes the composer's raw text when its very first character is
  // `!` and posts it. It returns true when it took the send (the caller does
  // nothing else), false for an ordinary message.
  function cexecSend() {
    if (!cexecEnabled || !ctext || typeof ctext.value !== "string" || ctext.value.charAt(0) !== "!") return false;
    if (cexecBusy) return true;
    if (cexecAttached()) { noteWarn(wsT("js.cexec.attachments")); return true; }
    var text = ctext.value;
    // The same text after an unresolved attempt is its retry (same id, asked
    // not to run); anything else is a fresh submission. The attempt is
    // recorded before the request goes out.
    var retry = !!(cexecPending && cexecPending.text === text);
    if (!retry) cexecPending = { id: cexecNewID(), text: text };
    var attempt = cexecPending;
    cexecSaveAttempt(attempt);
    cexecBusy = true;
    // The draft revision this send acknowledges, tracked from here on.
    var track = cexecTrack = { text: text, cur: text, intact: true };
    function settled() { cexecPending = null; cexecSaveAttempt(null); if (cexecTrack === track) cexecTrack = null; }
    function post() {
      fetch("/s/" + rid + "/composer-exec", {
        method: "POST", credentials: "same-origin", cache: "no-store",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ submission_id: attempt.id, text: text, retry: retry, token: cexecToken })
      }).then(function (r) {
        // A body that cannot be read leaves the status as the answer.
        return r.text().then(function (s) { return { r: r, s: s }; }, function () { return { r: r, s: "" }; });
      }).then(function (x) {
        var r = x.r, s = x.s;
        cexecBusy = false;
        if (r.ok) {
          var card = {};
          try { card = JSON.parse(s); } catch (e) { card = {}; }
          if (card && typeof card.token === "string" && card.token) cexecToken = card.token;
          settled();
          if (card && card.unknown) {
            // The daemon cannot say what became of an earlier attempt, or cannot
            // tell this request from a delayed copy of one it forgot, and ran
            // nothing: the draft stays, and sending again is a deliberate new run.
            cexecAdd(attempt.id, text.slice(1), { id: attempt.id, command: text.slice(1), phase: "done", unknown: true });
            return;
          }
          // The draft goes only now that the daemon holds the submission, and
          // only the acknowledged revision of it.
          cexecAcknowledged(text, track);
          cexecAdd(attempt.id, text.slice(1), card && card.id === attempt.id ? card : null);
          return;
        }
        // An old daemon (route unknown) or any definite refusal: nothing was
        // sent, as a prompt or otherwise, and the draft stays. A server error
        // leaves the attempt unresolved (its retry asks, never runs).
        if (r.status === 404 || r.status === 405) noteWarn(wsT("js.cexec.unsupported"));
        else noteWarn(wsT("js.send.fail", s.trim() || "HTTP " + r.status));
        if (r.status < 500) settled();
      }, function () {
        cexecBusy = false;
        // Outcome unknown: the draft and the recorded attempt stay, so sending
        // the same text again is a retry of the same submission.
        noteWarn(cexecUnreachable(wsT("js.cexec.neterr")));
      });
    }
    if (cexecToken || retry) post();
    else cexecList(function (list, why) {
      // No admission token yet (the page's first list read has not landed, or
      // was denied): a fresh submission needs one, so read it first.
      if (!list) {
        cexecBusy = false;
        settled();
        noteWarn(cexecUnreachable(why));
        return;
      }
      post();
    });
    return true;
  }
  // Shell mode is only a look over the shipped grammar: the draft's first
  // character is `!` (the same test cexecSend makes), shown by a data-bang flag
  // on the composer (dark mono input, bash badge, Run button) and an amber `!`
  // painted by the CSS highlight registry over the editor's own text, which
  // adds no markup and no layout work. The flag and the paint are written only
  // when the first character changes.
  var bangOn = false, bangDesc = null, bangHint = null;
  function bangMode() {
    return !!(cexecEnabled && ctext && typeof ctext.value === "string" && ctext.value.charAt(0) === "!");
  }
  function bangPaint(on) {
    if (!(window.CSS && CSS.highlights && typeof window.Highlight === "function")) return;
    var node = null, editor = ctext.wsc && ctext.wsc.editor;
    if (on && editor) {
      node = editor.firstChild;
      while (node && node.nodeType === 1 && node.firstChild && !node.hasAttribute("data-wsc-kind")) node = node.firstChild;
      if (!node || node.nodeType !== 3 || node.nodeValue.charAt(0) !== "!") node = null;
    }
    if (!node) { CSS.highlights.delete("ws-bang"); return; }
    var r = doc.createRange();
    r.setStart(node, 0); r.setEnd(node, 1);
    CSS.highlights.set("ws-bang", new Highlight(r));
  }
  function bangSync() {
    if (!cexecEnabled || !ctext) return;
    var on = bangMode();
    if (on !== bangOn) {
      bangOn = on;
      composer.toggleAttribute("data-bang", on);
      var csendBtn = $("csend");
      if (on) {
        bangDesc = ctext.getAttribute("aria-description") || "";
        ctext.setAttribute("aria-description", wsT("js.cexec.bang.aria"));
        if (csendBtn) { csendBtn.title = wsT("js.cexec.bang.send"); csendBtn.setAttribute("aria-label", wsT("js.cexec.bang.send")); }
      } else {
        if (bangDesc !== null) ctext.setAttribute("aria-description", bangDesc);
        if (csendBtn) { csendBtn.title = wsT("composer.send"); csendBtn.setAttribute("aria-label", wsT("composer.send")); }
      }
    }
    bangPaint(on);
  }
  function bangBuildHint() {
    if (!cexecEnabled || bangHint) return;
    bangHint = doc.createElement("div");
    bangHint.className = "cbang";
    var badge = doc.createElement("span");
    badge.className = "cbang-badge";
    badge.textContent = "bash";
    var hint = doc.createElement("span");
    hint.className = "cbang-hint";
    function kbd(t) { var k = doc.createElement("kbd"); k.textContent = t; return k; }
    function txt(t) { return doc.createTextNode(t); }
    hint.appendChild(kbd("Enter")); hint.appendChild(txt(" " + wsT("js.cexec.bang.run") + " · "));
    hint.appendChild(kbd("Esc")); hint.appendChild(txt(" / ")); hint.appendChild(kbd("⌫")); hint.appendChild(txt(" " + wsT("js.cexec.bang.leave")));
    bangHint.appendChild(badge); bangHint.appendChild(hint);
    var foot = composer.querySelector(".cfoot");
    composer.insertBefore(bangHint, foot);
  }
  // bangLeave is the first Esc: it removes the leading `!` through the editing
  // command (undo keeps it, an input event follows) and keeps the caret on the
  // same text.
  function bangLeave() {
    var caret = ctext.selectionEnd;
    editText(0, 1, "");
    var at = Math.max(0, caret - 1);
    ctext.setSelectionRange(at, at);
  }
  bangBuildHint();
  if (cexecEnabled && ctext) ctext.addEventListener("input", bangSync);
  cexecRestore();
