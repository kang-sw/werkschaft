// Composer `!` commands: a message whose very first character is `!` is
// posted to the session's composer-exec route (never to message), tracked
// headlessly until admitted into the exec HUD (or refused/unknown). The server
// owns the grammar; this file only sends the raw first-character candidate
// before any trim, mention or selection assembly. Nothing here falls back to
// sending the text as a prompt: a daemon that does not know the route refuses,
// and the draft stays.
  var cexecEnabled = !!(composer && composer.getAttribute("data-cexec") === "1" && rid && window.fetch);
  var CEXEC_POLL_MAX = 20, CEXEC_POLL_MS = 30000;
  var cexecSubs = Object.create(null), cexecPending = null, cexecBusy = false, cexecToken = "", cexecTrack = null, cexecAttemptKey = "ws.cexec.attempt." + rid;
  function cexecNewID() {
    var bytes = new Uint8Array(12);
    if (window.crypto && window.crypto.getRandomValues) window.crypto.getRandomValues(bytes);
    else for (var i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
    return "cx" + Array.prototype.map.call(bytes, function (b) { return (b < 16 ? "0" : "") + b.toString(16); }).join("");
  }
  // The attempt in flight (its submission id and raw text) is recorded before
  // the request goes out and cleared once the daemon's answer settles it. An
  // attempt that outlives a reload or a lost response is a retry: it asks the
  // daemon for that id's submission state and never starts the command again.
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
  // Headless submission tracking: polls GET /s/<rid>/composer-exec/<id> (bounded
  // by CEXEC_POLL_MAX polls and CEXEC_POLL_MS elapsed) until exec_id arrives,
  // or surfaces refused/unknown outcomes through noteWarn.
  function cexecHandleCard(sub, card) {
    if (!sub || sub.settled) return;
    if (!card) { cexecWatch(sub); return; }
    if (card.command) sub.command = card.command;
    if (card.unknown) {
      sub.settled = true;
      clearTimeout(sub.timer);
      delete cexecSubs[sub.id];
      noteWarn(wsT("js.cexec.unknown"));
      return;
    }
    if (card.phase === "refused") {
      sub.settled = true;
      clearTimeout(sub.timer);
      delete cexecSubs[sub.id];
      noteWarn(wsT("js.cexec.refused", card.error || ""));
      return;
    }
    if (typeof card.exec_id === "string" && card.exec_id) {
      sub.settled = true;
      clearTimeout(sub.timer);
      delete cexecSubs[sub.id];
      if (!sub.admitted) {
        sub.admitted = true;
        try {
          window.dispatchEvent(new CustomEvent("ws:cexec-admitted", {
            detail: { submission_id: sub.id, command: sub.command, exec_id: card.exec_id, origin: sub.origin }
          }));
        } catch (e) {}
        if (typeof execWake === "function") execWake();
        else if (typeof xhFetch === "function") xhFetch();
      }
      return;
    }
    if (card.phase !== "running" && card.phase !== "async") {
      sub.settled = true;
      clearTimeout(sub.timer);
      delete cexecSubs[sub.id];
      return;
    }
    cexecWatch(sub);
  }
  function cexecTrackSubmission(id, command, card, origin) {
    var sub = cexecSubs[id];
    if (!sub) {
      sub = cexecSubs[id] = { id: id, command: command, origin: origin || "cexec", polls: 0, timer: 0, startedAt: Date.now(), settled: false, admitted: false };
    } else {
      if (command) sub.command = command;
      if (origin) sub.origin = origin;
    }
    cexecHandleCard(sub, card);
    return sub;
  }
  function cexecWatch(sub) {
    clearTimeout(sub.timer);
    if (!sub || sub.settled) return;
    if (sub.polls >= CEXEC_POLL_MAX || (Date.now() - sub.startedAt) >= CEXEC_POLL_MS) {
      sub.settled = true;
      delete cexecSubs[sub.id];
      return;
    }
    sub.polls++;
    sub.timer = setTimeout(function () { cexecPoll(sub); }, Math.min(5000, 700 + sub.polls * 400));
  }
  function cexecPoll(sub) {
    if (!sub || sub.settled) return;
    fetch("/s/" + rid + "/composer-exec/" + encodeURIComponent(sub.id), { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (r.status === 404) { cexecHandleCard(sub, { id: sub.id, command: sub.command, phase: "done", unknown: true }); return null; }
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    }).then(function (card) {
      if (!card || sub.settled) return;
      cexecHandleCard(sub, card);
    }, function () {
      if (!sub.settled) cexecWatch(sub);
    });
  }
  // Prime the admission token on page load; no command history is restored in the composer.
  function cexecPrime() {
    if (!cexecEnabled) return;
    cexecList(function () {});
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
            cexecTrackSubmission(attempt.id, text.slice(1), { id: attempt.id, command: text.slice(1), phase: "done", unknown: true }, "cexec");
            return;
          }
          // The draft goes only now that the daemon holds the submission, and
          // only the acknowledged revision of it.
          cexecAcknowledged(text, track);
          cexecTrackSubmission(attempt.id, text.slice(1), card && card.id === attempt.id ? card : null, "cexec");
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
  // on the composer (terminal-tinted mono input, Run button) and an amber `!`
  // painted by the CSS highlight registry over the editor's own text, which
  // adds no markup and no layout work. The flag and the paint are written only
  // when the first character changes.
  var bangOn = false, bangDesc = null;
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
  // bangLeave is the first Esc: it removes the leading `!` through the editing
  // command (undo keeps it, an input event follows) and keeps the caret on the
  // same text.
  function bangLeave() {
    var caret = ctext.selectionEnd;
    editText(0, 1, "");
    var at = Math.max(0, caret - 1);
    ctext.setSelectionRange(at, at);
  }
  if (cexecEnabled && ctext) ctext.addEventListener("input", bangSync);
  cexecPrime();
