// Composer `!` commands: a message whose very first character is `!` is
// posted to the session's composer-exec route (never to message) and shows a
// private result card. The server owns the grammar; this file only sends the
// raw first-character candidate before any trim, mention or selection
// assembly. Nothing here falls back to sending the text as a prompt: a daemon
// that does not know the route refuses, and the draft stays.
  var cexecEnabled = !!(composer && composer.getAttribute("data-cexec") === "1" && rid && window.fetch);
  var cexecCards = [], cexecById = {}, cexecBox = null, cexecPending = null, cexecBusy = false, cexecStoreKey = "ws.cexec." + rid, cexecAttemptKey = "ws.cexec.attempt." + rid;
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
  function cexecContainer() {
    if (cexecBox) return cexecBox;
    cexecBox = doc.createElement("div");
    cexecBox.className = "d-cexec";
    cexecBox.setAttribute("role", "log");
    cexecBox.setAttribute("aria-label", wsT("js.cexec.aria"));
    cexecBox.hidden = true;
    var anchor = composer.querySelector(".cimgs");
    composer.insertBefore(cexecBox, anchor || composer.firstChild);
    return cexecBox;
  }
  function cexecAdd(id, command, card) {
    var c = cexecById[id];
    if (!c) {
      c = { id: id, command: command, card: card || { id: id, command: command, phase: "running" }, el: null, polls: 0, timer: 0 };
      cexecById[id] = c;
      cexecCards.push(c);
      cexecSave();
    } else if (card) c.card = card;
    cexecRender(c);
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
    var box = cexecContainer(), card = c.card, el = c.el;
    if (!el) {
      el = c.el = doc.createElement("div");
      el.className = "cmdrun cexec";
      el.setAttribute("data-cexec", c.id);
      box.appendChild(el);
    }
    el.setAttribute("data-phase", card.unknown ? "unknown" : card.phase);
    el.replaceChildren();
    var echo = doc.createElement("div");
    echo.className = "cmdecho";
    var mk = doc.createElement("span");
    mk.className = "mk";
    mk.textContent = "❯";
    var code = doc.createElement("code");
    code.textContent = c.command;
    var state = doc.createElement("span");
    state.className = "cexec-state";
    state.textContent = cexecStateText(card);
    var close = doc.createElement("button");
    close.type = "button";
    close.className = "cexec-x";
    close.textContent = "×";
    close.title = wsT("js.cexec.dismiss");
    close.setAttribute("aria-label", wsT("js.cexec.dismiss"));
    close.addEventListener("click", function () { cexecDismiss(c); });
    echo.appendChild(mk); echo.appendChild(code); echo.appendChild(state); echo.appendChild(close);
    el.appendChild(echo);
    function note(text) {
      var n = doc.createElement("div");
      n.className = "cmdnote";
      n.textContent = text;
      el.appendChild(n);
    }
    if (card.exec_id) note(wsT("js.cexec.job", card.exec_id));
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
      row.appendChild(link); row.appendChild(copy);
      el.appendChild(row);
    }
    if (card.file_error) note(card.file_error);
    if (card.truncated) note(wsT("js.cexec.truncated", String(card.truncated)));
    if (card.replaced) note(wsT("js.cexec.replaced"));
    box.hidden = false;
  }
  function cexecDismiss(c) {
    clearTimeout(c.timer);
    c.dead = true;
    if (c.el && c.el.parentNode) c.el.parentNode.removeChild(c.el);
    delete cexecById[c.id];
    cexecCards.splice(cexecCards.indexOf(c), 1);
    cexecSave();
    if (cexecBox) cexecBox.hidden = !cexecBox.firstChild;
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
    if (!remembered.length) return;
    // Remembered commands are shown only once the daemon's private list read
    // succeeded: a denied or failed read (a LAN page, a closed lead, an old
    // daemon) renders nothing of them.
    fetch("/s/" + rid + "/composer-exec", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      return r.ok ? r.json() : null;
    }, function () { return null; }).then(function (list) {
      if (!list || !Array.isArray(list.cards)) return;
      var known = {};
      (list.cards || []).forEach(function (card) { known[card.id] = card; });
      remembered.forEach(function (m) {
        if (cexecById[m.id]) return;
        cexecAdd(m.id, m.command, known[m.id] || { id: m.id, command: m.command, phase: "done", unknown: true });
      });
    });
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
  // cexecAcknowledged clears the draft the daemon acknowledged and nothing
  // typed since: the same text clears it with its mentions; text appended
  // meanwhile stays, and a draft edited or begun anew meanwhile (with whatever
  // it gathered) is left untouched.
  function cexecAcknowledged(text) {
    var now = ctext.value;
    if (now === text) {
      ctext.value = ""; grow(); clearMentions();
    } else if (now.length > text.length && now.indexOf(text) === 0) {
      ctext.value = now.slice(text.length); grow();
    } else return;
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
    function settled() { cexecPending = null; cexecSaveAttempt(null); }
    fetch("/s/" + rid + "/composer-exec", {
      method: "POST", credentials: "same-origin", cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ submission_id: attempt.id, text: text, retry: retry })
    }).then(function (r) {
      // A body that cannot be read leaves the status as the answer.
      return r.text().then(function (s) { return { r: r, s: s }; }, function () { return { r: r, s: "" }; });
    }).then(function (x) {
      var r = x.r, s = x.s;
      cexecBusy = false;
      if (r.ok) {
        var card = {};
        try { card = JSON.parse(s); } catch (e) { card = {}; }
        settled();
        if (card && card.unknown) {
          // The daemon cannot say what became of an earlier attempt and ran
          // nothing: the draft stays, and sending again is a deliberate new run.
          cexecAdd(attempt.id, text.slice(1), { id: attempt.id, command: text.slice(1), phase: "done", unknown: true });
          return;
        }
        // The draft goes only now that the daemon holds the submission, and
        // only the acknowledged revision of it.
        cexecAcknowledged(text);
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
      noteWarn(wsT("js.post.unreachable"));
    });
    return true;
  }
  cexecRestore();
