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
      if (a) window.sessionStorage.setItem(cexecAttemptKey, JSON.stringify({ id: a.id, text: a.text, tag: a.tag || "" }));
      else window.sessionStorage.removeItem(cexecAttemptKey);
    } catch (e) {}
  }
  function cexecLoadAttempt() {
    try {
      var a = JSON.parse(window.sessionStorage.getItem(cexecAttemptKey) || "null");
      return a && typeof a.id === "string" && typeof a.text === "string" ? { id: a.id, text: a.text, tag: typeof a.tag === "string" ? a.tag : "" } : null;
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
      if (typeof histSubTags !== "undefined") delete histSubTags[sub.id];
      noteWarn(wsT("js.cexec.unknown"));
      return;
    }
    if (card.phase === "refused") {
      sub.settled = true;
      clearTimeout(sub.timer);
      delete cexecSubs[sub.id];
      if (typeof histSubTags !== "undefined") delete histSubTags[sub.id];
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
        if (typeof histSubTags !== "undefined") delete histSubTags[sub.id];
        if (typeof execWake === "function") execWake();
        else if (typeof xhFetch === "function") xhFetch();
      }
      return;
    }
    if (card.phase !== "running" && card.phase !== "async") {
      sub.settled = true;
      clearTimeout(sub.timer);
      delete cexecSubs[sub.id];
      if (typeof histSubTags !== "undefined") delete histSubTags[sub.id];
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
      if (typeof resetAutoMultiline === "function") resetAutoMultiline();
    } else {
      ctext.value = now.slice(text.length); grow();
      if (typeof resetAutoMultiline === "function") resetAutoMultiline();
      if (typeof syncAutoMultilineFromValue === "function") syncAutoMultilineFromValue();
    }
    if (typeof bangHistClose === "function") bangHistClose(false);
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
    if (!retry) cexecPending = { id: cexecNewID(), text: text, tag: typeof bangHistTag === "function" ? bangHistTag() : "" };
    var attempt = cexecPending;
    if (typeof histSubTags !== "undefined") histSubTags[attempt.id] = attempt.tag || "";
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
        if (typeof syncSendButtonLabel === "function") syncSendButtonLabel();
        else if (csendBtn) { csendBtn.title = wsT("composer.send"); csendBtn.setAttribute("aria-label", wsT("composer.send")); }
      }
    }
    bangPaint(on);
    bangHistSyncInput();
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

  // --- per-worktree `!` command history -----------------------------------
  // Stored per worktree in localStorage keyed by rootTag parsed from ucmdRev.
  // Persistence happens only after an admitted cexec job's cwd in /exec/jobs
  // hashes to its verified launch worktree rootTag, and exit codes are tracked
  // with a bounded poll independent of HUD chip dismissal or fading.
  var BANG_HIST_PREFIX = "ws.cexec.history.", BANG_HIST_MAX = 200, BANG_HIST_BYTES = 2048;
  var BANG_HIST_POLL_MAX = 20, BANG_HIST_POLL_MS = 30000, BANG_HIST_CLEAR_MS = 3000;
  var histPending = Object.create(null), histSubTags = Object.create(null), histPollInflight = false, histJobsAbsent = false, histClock = 0;
  if (cexecPending && cexecPending.id) histSubTags[cexecPending.id] = cexecPending.tag || "";
  var histOpen = false, histManual = false, histSuppressAuto = false;
  var histSelIdx = -1, histMatches = [], histClearArmed = false, histClearTimer = 0;
  var histPop = null, histListEl = null, histCountEl = null, histEmptyEl = null, histEmptyHintEl = null, histHintsEl = null, histClearBtn = null;
  var histBadge = null, histRowMap = Object.create(null), histBoundTag = "";

  function bangHistNow() {
    var t = Date.now();
    histClock = t > histClock ? t : histClock + 1;
    return histClock;
  }
  function bangHistTag() {
    if (typeof ucmdRev !== "string" || !ucmdRev) return "";
    var parts = ucmdRev.split(".");
    if (parts.length < 3) return "";
    return parts.slice(2).join(".");
  }
  function bangUtf8Bytes(s) {
    if (window.TextEncoder) return new TextEncoder().encode(s).length;
    try { return unescape(encodeURIComponent(s)).length; } catch (e) { return s.length * 3; }
  }
  function bangCleanPath(p) {
    if (typeof p !== "string" || !p) return "";
    var vol = "", rest = p, isWin = false;
    var unc = /^(\\\\|\/\/)([^\\/]+)[\\/]([^\\/]+)([\s\S]*)$/.exec(p);
    if (unc) {
      isWin = true;
      vol = "\\\\" + unc[2] + "\\" + unc[3];
      rest = unc[4] || "\\";
    } else if (/^[A-Za-z]:/.test(p)) {
      isWin = true;
      vol = p.slice(0, 2);
      rest = p.slice(2);
    } else if (p.indexOf("\\") >= 0) {
      isWin = true;
    }
    var sep = isWin ? "\\" : "/";
    if (isWin) rest = rest.replace(/\//g, "\\");
    var rooted = rest.charAt(0) === sep;
    var raw = rest.split(sep), out = [];
    for (var i = 0; i < raw.length; i++) {
      var seg = raw[i];
      if (!seg || seg === ".") continue;
      if (seg === "..") {
        if (out.length && out[out.length - 1] !== "..") out.pop();
        else if (!rooted) out.push("..");
      } else out.push(seg);
    }
    if (!out.length) {
      if (rooted) return vol + sep;
      return vol ? vol + "." : ".";
    }
    return vol + (rooted ? sep : "") + out.join(sep);
  }
  function bangFnv1a32(s) {
    var bytes;
    if (window.TextEncoder) bytes = new TextEncoder().encode(s);
    else {
      var enc = unescape(encodeURIComponent(s));
      bytes = [];
      for (var i = 0; i < enc.length; i++) bytes.push(enc.charCodeAt(i));
    }
    var h = 0x811c9dc5;
    for (var j = 0; j < bytes.length; j++) {
      h ^= bytes[j];
      h = Math.imul(h, 0x01000193) >>> 0;
    }
    return (h >>> 0).toString(36);
  }
  function bangRootTag(cwd) {
    var c = bangCleanPath(cwd);
    return c ? bangFnv1a32(c) : "";
  }
  function histStorageKey(tag) { return BANG_HIST_PREFIX + tag; }
  function histNormalizeStore(raw) {
    var clearedAt = 0, deleted = Object.create(null), list = [];
    if (Array.isArray(raw)) {
      list = raw;
    } else if (raw && typeof raw === "object") {
      if (typeof raw.clearedAt === "number" && isFinite(raw.clearedAt)) clearedAt = raw.clearedAt;
      if (raw.deleted && typeof raw.deleted === "object") {
        Object.keys(raw.deleted).forEach(function (k) {
          var v = raw.deleted[k];
          if (typeof v === "number" && isFinite(v) && v > clearedAt) deleted[k] = v;
        });
      }
      if (Array.isArray(raw.rows)) list = raw.rows;
    }
    var seen = Object.create(null), rows = [];
    list.slice().sort(function (a, b) { return ((b && b.ts) || 0) - ((a && a.ts) || 0); }).forEach(function (r) {
      if (!r || typeof r.cmd !== "string" || !r.cmd || seen[r.cmd]) return;
      var ts = typeof r.ts === "number" && isFinite(r.ts) ? r.ts : 0;
      if (!ts) return;
      var rec = { cmd: r.cmd, ts: ts };
      if (typeof r.exit === "number" && isFinite(r.exit)) rec.exit = r.exit;
      if (typeof r.eid === "string" && r.eid) rec.eid = r.eid;
      if (bangUtf8Bytes(JSON.stringify(rec)) > BANG_HIST_BYTES) return;
      seen[r.cmd] = true;
      rows.push(rec);
    });
    return { v: 1, clearedAt: clearedAt, deleted: deleted, rows: rows.slice(0, BANG_HIST_MAX) };
  }
  function histReadStore(tag) {
    if (!tag) return { v: 1, clearedAt: 0, deleted: Object.create(null), rows: [] };
    try {
      var s = window.localStorage.getItem(histStorageKey(tag));
      if (!s) return { v: 1, clearedAt: 0, deleted: Object.create(null), rows: [] };
      return histNormalizeStore(JSON.parse(s));
    } catch (e) {
      return { v: 1, clearedAt: 0, deleted: Object.create(null), rows: [] };
    }
  }
  function histWriteStore(tag, store) {
    if (!tag) return false;
    var norm = histNormalizeStore(store);
    var keys = Object.keys(norm.deleted);
    if (keys.length > BANG_HIST_MAX) {
      keys.sort(function (a, b) { return norm.deleted[b] - norm.deleted[a]; }).slice(BANG_HIST_MAX).forEach(function (k) { delete norm.deleted[k]; });
    }
    try {
      window.localStorage.setItem(histStorageKey(tag), JSON.stringify(norm));
      return true;
    } catch (e) { return false; }
  }
  function bangHistRows(tag) {
    var t = tag || bangHistTag();
    return t ? histReadStore(t).rows : [];
  }
  function histEntryFits(rec) {
    return bangUtf8Bytes(JSON.stringify(rec)) <= BANG_HIST_BYTES;
  }
  function histRecordVerified(tag, entry, exitCode) {
    var store = histReadStore(tag);
    if ((store.clearedAt && entry.launchTs <= store.clearedAt) || (store.deleted[entry.cmd] && entry.launchTs <= store.deleted[entry.cmd])) {
      return false;
    }
    var idx = store.rows.findIndex(function (r) { return r.cmd === entry.cmd; });
    var cur = idx >= 0 ? store.rows[idx] : null;
    if (cur && cur.eid && cur.eid !== entry.exec_id && cur.ts > entry.launchTs) {
      return true;
    }
    if (!entry.recorded) {
      var rec = { cmd: entry.cmd, ts: entry.launchTs, eid: entry.exec_id };
      if (exitCode !== null) rec.exit = exitCode;
      if (!histEntryFits(rec)) {
        entry.recorded = true;
        return true;
      }
      if (idx >= 0) store.rows.splice(idx, 1);
      store.rows.unshift(rec);
      store.rows = store.rows.slice(0, BANG_HIST_MAX);
      histWriteStore(tag, store);
      entry.recorded = true;
      return true;
    }
    if (!cur || cur.eid !== entry.exec_id || cur.ts !== entry.launchTs) {
      return false;
    }
    if (exitCode === null || cur.exit === exitCode) return true;
    var updated = { cmd: cur.cmd, ts: cur.ts, eid: cur.eid, exit: exitCode };
    if (!histEntryFits(updated)) return true;
    store.rows[idx] = updated;
    histWriteStore(tag, store);
    return true;
  }
  function histCancelPending(tag, cmd) {
    Object.keys(histPending).forEach(function (eid) {
      var p = histPending[eid];
      var pTag = p.verifiedTag || p.launchTag;
      if (pTag !== tag) return;
      if (cmd !== undefined && p.cmd !== cmd) return;
      p.cancelled = true;
      clearTimeout(p.timer);
      delete histPending[eid];
    });
  }
  function histJobExit(j) {
    return j && j.state === "exited" && typeof j.exit_code === "number" && isFinite(j.exit_code) ? j.exit_code : null;
  }
  function bangHistOnJobs(jobs) {
    if (!Array.isArray(jobs)) return;
    var byId = Object.create(null);
    jobs.forEach(function (j) { if (j && j.id) byId[j.id] = j; });
    var dirty = false, now = Date.now();
    Object.keys(histPending).forEach(function (eid) {
      var entry = histPending[eid], j = byId[eid];
      if (!j) {
        if (entry.polls >= BANG_HIST_POLL_MAX || (now - entry.startedAt) >= BANG_HIST_POLL_MS) {
          clearTimeout(entry.timer);
          delete histPending[eid];
        }
        return;
      }
      if (!entry.verifiedTag) {
        var cwdTag = bangRootTag(j.cwd);
        if (!cwdTag) {
          if (j.state !== "running") {
            clearTimeout(entry.timer);
            delete histPending[eid];
          }
          return;
        }
        entry.verifiedTag = cwdTag;
      }
      var kept = histRecordVerified(entry.verifiedTag, entry, histJobExit(j));
      if (!kept) {
        entry.cancelled = true;
        clearTimeout(entry.timer);
        delete histPending[eid];
        return;
      }
      if (entry.verifiedTag === bangHistTag()) dirty = true;
      if (j.state !== "running") {
        entry.finished = true;
        clearTimeout(entry.timer);
        delete histPending[eid];
      }
    });
    if (dirty) {
      histSyncBadge();
      if (histOpen) histRender();
    }
  }
  function histSchedulePoll(entry) {
    clearTimeout(entry.timer);
    if (!entry || entry.finished || entry.cancelled) return;
    if (entry.polls >= BANG_HIST_POLL_MAX || (Date.now() - entry.startedAt) >= BANG_HIST_POLL_MS) {
      delete histPending[entry.exec_id];
      return;
    }
    entry.polls++;
    entry.timer = setTimeout(function () { entry.timer = 0; histFetchJobs(entry.exec_id); }, Math.min(5000, 700 + entry.polls * 400));
  }
  function histFetchJobs(triggerId) {
    if (!cexecEnabled || histJobsAbsent || !Object.keys(histPending).length) return;
    if (histPollInflight) {
      if (triggerId && histPending[triggerId]) histSchedulePoll(histPending[triggerId]);
      return;
    }
    histPollInflight = true;
    fetch("/s/" + rid + "/exec/jobs", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (r.status === 404 || r.status === 403) {
        histJobsAbsent = true;
        return null;
      }
      return r.ok ? r.json() : null;
    }).catch(function () { return null; }).then(function (res) {
      histPollInflight = false;
      if (histJobsAbsent) {
        Object.keys(histPending).forEach(function (eid) {
          clearTimeout(histPending[eid].timer);
          delete histPending[eid];
        });
        return;
      }
      if (res && Array.isArray(res.jobs)) bangHistOnJobs(res.jobs);
      Object.keys(histPending).forEach(function (eid) {
        var e = histPending[eid];
        if (!e.finished && !e.cancelled && !e.timer) histSchedulePoll(e);
      });
    });
  }
  window.addEventListener("ws:cexec-admitted", function (e) {
    if (!cexecEnabled || histJobsAbsent) return;
    var d = e && e.detail;
    if (!d || d.origin !== "cexec" || typeof d.exec_id !== "string" || !d.exec_id || typeof d.command !== "string" || !d.command) return;
    if (histPending[d.exec_id]) return;
    var subTag = typeof d.submission_id === "string" && histSubTags[d.submission_id] ? histSubTags[d.submission_id] : "";
    if (typeof d.submission_id === "string") delete histSubTags[d.submission_id];
    var launchTag = subTag || bangHistTag();
    var entry = histPending[d.exec_id] = {
      exec_id: d.exec_id, cmd: d.command, launchTag: launchTag, verifiedTag: "",
      recorded: false, finished: false, cancelled: false, launchTs: bangHistNow(),
      polls: 0, startedAt: Date.now(), timer: 0
    };
    if (typeof xhJobs !== "undefined" && Array.isArray(xhJobs) && xhJobs.length) bangHistOnJobs(xhJobs);
    if (histPending[d.exec_id] && !entry.finished && !entry.cancelled) {
      histFetchJobs(d.exec_id);
    }
  });

  // --- fuzzy matching & relative formatting -------------------------------
  function bangFuzzyMatch(cmd, query) {
    if (!query) return { score: 0, idx: [] };
    var q = (query.normalize ? query.normalize("NFC") : query).toLowerCase();
    var c = (cmd.normalize ? cmd.normalize("NFC") : cmd);
    var cLow = c.toLowerCase();
    // Keep 1:1 UTF-16 alignment with cmd so <mark> offsets match cmd.slice().
    var low = cLow.length === cmd.length ? cLow : cmd.toLowerCase();
    if (low.length !== cmd.length) {
      low = "";
      for (var ci = 0; ci < cmd.length; ci++) {
        var ch = cmd.charAt(ci).toLowerCase();
        low += ch.length === 1 ? ch : cmd.charAt(ci);
      }
    }
    var ql = q.length, cl = low.length;
    if (!ql) return { score: 0, idx: [] };
    if (ql > cl) return null;
    // DP/greedy: find earliest subsequence check, preferring word boundaries and consecutive runs.
    var pos = [], qi = 0, i;
    for (i = 0; i < cl && qi < ql; i++) {
      if (low.charCodeAt(i) === q.charCodeAt(qi)) { pos.push(i); qi++; }
    }
    if (qi < ql) return null;
    // Try aligning each occurrence of the first query char to pick the highest score.
    var bestScore = -1e9, bestIdx = pos;
    for (var start = pos[0]; start < cl; start++) {
      if (low.charCodeAt(start) !== q.charCodeAt(0)) continue;
      var idx = [start], cur = start + 1, k = 1, ok = true;
      while (k < ql) {
        var found = -1;
        for (; cur < cl; cur++) {
          if (low.charCodeAt(cur) === q.charCodeAt(k)) { found = cur; break; }
        }
        if (found < 0) { ok = false; break; }
        idx.push(found);
        cur = found + 1;
        k++;
      }
      if (!ok) break;
      var sc = 0;
      for (k = 0; k < idx.length; k++) {
        var at = idx[k];
        if (at === 0) sc += 24;
        else if (/[\s/_.:=-]/.test(cmd.charAt(at - 1))) sc += 18;
        if (k > 0 && at === idx[k - 1] + 1) sc += 20;
        else if (k > 0) sc -= (at - idx[k - 1] - 1);
        if (cmd.charCodeAt(at) === query.charCodeAt(k)) sc += 2;
      }
      sc -= start * 0.5 + (cl - ql) * 0.1;
      if (sc > bestScore) { bestScore = sc; bestIdx = idx; }
    }
    return { score: bestScore, idx: bestIdx };
  }
  function bangAgo(ts) {
    var diff = Math.max(0, Date.now() - ts), m = Math.floor(diff / 60000);
    if (m < 1) return wsT("js.wb.ago.now");
    if (m < 60) return wsT("js.wb.ago.min", m);
    var h = Math.floor(m / 60);
    if (h < 24) return wsT("js.wb.ago.hour", h);
    var d = Math.floor(h / 24);
    if (d < 30) return wsT("js.wb.ago.day", d);
    if (d < 365) return wsT("js.wb.body.month", Math.floor(d / 30));
    return wsT("js.wb.body.year", Math.floor(d / 365));
  }
  function bangSendKeyLabel() {
    var m = typeof effectiveInputMode === "function" ? effectiveInputMode() : (typeof inputMode !== "undefined" ? inputMode : "enter");
    return m === "mod" ? "Ctrl/Cmd+Enter" : "Enter";
  }

  // --- trigger badge & popup UI -------------------------------------------
  function histEnsureBadge() {
    if (!cexecEnabled || histBadge) return histBadge;
    var wrap = composer && composer.querySelector(".sbwrap");
    if (!wrap) return null;
    histBadge = doc.createElement("button");
    histBadge.type = "button";
    histBadge.className = "hbadge";
    histBadge.id = "hbadge";
    histBadge.hidden = true;
    histBadge.title = wsT("js.cexec.hist.badge.title");
    histBadge.setAttribute("aria-haspopup", "listbox");
    histBadge.setAttribute("aria-expanded", "false");
    histBadge.innerHTML = '<svg class="hico" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 8a5 5 0 1 0 1.4-3.5L3 6"/><path d="M3 3v3h3"/><path d="M8 5.2v3l2 1.2"/></svg><span></span>';
    histBadge.lastChild.textContent = wsT("js.cexec.hist.badge");
    histBadge.addEventListener("click", function () {
      if (histOpen) bangHistClose(true);
      else bangHistOpenManual();
    });
    var sb = $("sbadge");
    wrap.insertBefore(histBadge, sb && sb.parentNode === wrap ? sb : null);
    if (typeof xhRo !== "undefined" && xhRo) xhRo.observe(histBadge);
    return histBadge;
  }
  function histSyncBadge() {
    var b = histEnsureBadge();
    if (!b) return;
    var tag = bangHistTag(), rows = tag ? bangHistRows(tag) : [];
    var hide = !tag || !rows.length;
    if (b.hidden !== hide) {
      b.hidden = hide;
      if (typeof xhLayout === "function") xhLayout();
    }
    b.setAttribute("aria-expanded", String(histOpen));
  }
  function histResetClearConfirm() {
    clearTimeout(histClearTimer);
    histClearTimer = 0;
    histClearArmed = false;
    if (histClearBtn) {
      histClearBtn.classList.remove("chist-armed");
      histClearBtn.textContent = wsT("js.cexec.hist.clear");
    }
  }
  function histRenderCmdNodes(cmdEl, cmd, idx) {
    var sig = cmd + "\0" + (idx && idx.length ? idx.join(",") : "");
    if (cmdEl.__sig === sig) return;
    cmdEl.__sig = sig;
    cmdEl.textContent = "";
    if (!idx || !idx.length) {
      cmdEl.textContent = cmd;
      return;
    }
    var frag = doc.createDocumentFragment(), pos = 0, i = 0;
    while (i < idx.length) {
      var s = idx[i], e = s + 1;
      while (i + 1 < idx.length && idx[i + 1] === e) { i++; e++; }
      if (s > pos) frag.appendChild(doc.createTextNode(cmd.slice(pos, s)));
      var mk = doc.createElement("mark");
      mk.textContent = cmd.slice(s, e);
      frag.appendChild(mk);
      pos = e;
      i++;
    }
    if (pos < cmd.length) frag.appendChild(doc.createTextNode(cmd.slice(pos)));
    cmdEl.appendChild(frag);
  }
  function histEnsurePopup() {
    if (histPop) return histPop;
    histPop = doc.createElement("div");
    histPop.className = "chist";
    histPop.hidden = true;
    histPop.setAttribute("role", "dialog");
    histPop.setAttribute("aria-label", wsT("js.cexec.hist.title"));

    var head = doc.createElement("div");
    head.className = "chist-head";
    var title = doc.createElement("span");
    title.className = "chist-title";
    var bang = doc.createElement("span");
    bang.className = "chist-bang";
    bang.setAttribute("aria-hidden", "true");
    bang.textContent = "!";
    var titleTxt = doc.createElement("span");
    titleTxt.textContent = wsT("js.cexec.hist.title");
    title.appendChild(bang);
    title.appendChild(titleTxt);
    histCountEl = doc.createElement("span");
    histCountEl.className = "chist-count";
    head.appendChild(title);
    head.appendChild(histCountEl);

    histListEl = doc.createElement("div");
    histListEl.className = "chist-list";
    histListEl.setAttribute("role", "listbox");
    histListEl.setAttribute("aria-label", wsT("js.cexec.hist.title"));

    histEmptyEl = doc.createElement("div");
    histEmptyEl.className = "chist-empty";
    histEmptyEl.hidden = true;
    var emptyMsg = doc.createElement("span");
    emptyMsg.textContent = wsT("js.cexec.hist.empty");
    histEmptyHintEl = doc.createElement("span");
    histEmptyHintEl.className = "chist-empty-hint";
    histEmptyEl.appendChild(emptyMsg);
    histEmptyEl.appendChild(histEmptyHintEl);

    var foot = doc.createElement("div");
    foot.className = "chist-foot";
    histHintsEl = doc.createElement("div");
    histHintsEl.className = "chist-hints";
    histClearBtn = doc.createElement("button");
    histClearBtn.type = "button";
    histClearBtn.className = "chist-clear";
    histClearBtn.textContent = wsT("js.cexec.hist.clear");
    foot.appendChild(histHintsEl);
    foot.appendChild(histClearBtn);

    histPop.appendChild(head);
    histPop.appendChild(histListEl);
    histPop.appendChild(histEmptyEl);
    histPop.appendChild(foot);
    composer.appendChild(histPop);

    histPop.addEventListener("mousedown", function (e) {
      if (e.target && e.target.closest && (e.target.closest(".chist-row") || e.target.closest(".chist-clear"))) {
        e.preventDefault();
      }
    });
    histListEl.addEventListener("click", function (e) {
      var del = e.target && e.target.closest ? e.target.closest(".chist-del") : null;
      if (del) {
        var row = del.closest(".chist-row");
        if (row && row.__cmd) histDeleteRow(bangHistTag(), row.__cmd);
        return;
      }
      var row = e.target && e.target.closest ? e.target.closest(".chist-row") : null;
      if (row && row.__cmd) {
        histPickCmd(row.__cmd);
      }
    });
    histClearBtn.addEventListener("click", function () {
      var tag = bangHistTag();
      if (!tag) return;
      if (!histClearArmed) {
        histClearArmed = true;
        histClearBtn.classList.add("chist-armed");
        histClearBtn.textContent = wsT("js.cexec.hist.clear.confirm");
        clearTimeout(histClearTimer);
        histClearTimer = setTimeout(histResetClearConfirm, BANG_HIST_CLEAR_MS);
        return;
      }
      histClearAll(tag);
    });
    bangHistSyncMode();
    return histPop;
  }
  function bangHistSyncMode() {
    if (!histPop) return;
    var isMod = (typeof effectiveInputMode === "function" ? effectiveInputMode() : (typeof inputMode !== "undefined" ? inputMode : "enter")) === "mod";
    if (histEmptyHintEl) histEmptyHintEl.textContent = wsT(isMod ? "js.cexec.hist.empty.mod" : "js.cexec.hist.empty.enter");
    if (histHintsEl) {
      histHintsEl.textContent = "";
      [
        ["↑↓", wsT("js.cexec.hist.hint.move")],
        ["Tab", wsT("js.cexec.hist.hint.complete")],
        [bangSendKeyLabel(), wsT("js.cexec.hist.hint.fill")],
        ["Ctrl+R", wsT("js.cexec.hist.hint.next")],
        ["Esc", wsT("js.cexec.hist.hint.close")]
      ].forEach(function (pair) {
        var wrap = doc.createElement("span");
        wrap.className = "chist-hint";
        var k = doc.createElement("kbd");
        k.textContent = pair[0];
        wrap.appendChild(k);
        wrap.appendChild(doc.createTextNode(pair[1]));
        histHintsEl.appendChild(wrap);
      });
    }
  }
  function histCreateRowEl(cmd) {
    var row = doc.createElement("div");
    row.className = "chist-row";
    row.setAttribute("role", "option");
    row.setAttribute("aria-selected", "false");
    row.__cmd = cmd;
    var cmdEl = doc.createElement("span");
    cmdEl.className = "chist-cmd";
    var exEl = doc.createElement("span");
    exEl.className = "chist-ex";
    exEl.hidden = true;
    var agoEl = doc.createElement("span");
    agoEl.className = "chist-ago";
    var delEl = doc.createElement("button");
    delEl.type = "button";
    delEl.className = "chist-del";
    delEl.tabIndex = -1;
    delEl.title = wsT("js.cexec.hist.del");
    delEl.setAttribute("aria-label", wsT("js.cexec.hist.del"));
    delEl.textContent = "×";
    row.appendChild(cmdEl);
    row.appendChild(exEl);
    row.appendChild(agoEl);
    row.appendChild(delEl);
    return row;
  }
  function histPatchSelection(scrollIntoView) {
    for (var i = 0; i < histMatches.length; i++) {
      var el = histRowMap[histMatches[i].row.cmd];
      if (!el) continue;
      var sel = i === histSelIdx;
      el.classList.toggle("sel", sel);
      el.setAttribute("aria-selected", String(sel));
      if (sel && scrollIntoView && el.scrollIntoView) el.scrollIntoView({ block: "nearest" });
    }
  }
  function histCurrentQuery() {
    var v = ctext && typeof ctext.value === "string" ? ctext.value : "";
    return v.charAt(0) === "!" ? v.slice(1) : "";
  }
  function histRender() {
    if (!histOpen) return;
    var tag = bangHistTag(), rows = tag ? bangHistRows(tag) : [];
    if (!tag || !rows.length) {
      bangHistClose(false);
      return;
    }
    var pop = histEnsurePopup();
    var qStr = histCurrentQuery(), matches = [];
    for (var i = 0; i < rows.length; i++) {
      var m = bangFuzzyMatch(rows[i].cmd, qStr);
      if (m) matches.push({ row: rows[i], score: m.score, idx: m.idx, origIdx: i });
    }
    if (qStr) {
      matches.sort(function (a, b) {
        return b.score - a.score || b.row.ts - a.row.ts || a.origIdx - b.origIdx;
      });
    }
    histMatches = matches;
    if (!matches.length) histSelIdx = -1;
    else if (qStr || histSelIdx < 0 || histSelIdx >= matches.length) histSelIdx = 0;

    histCountEl.textContent = qStr
      ? wsT("js.cexec.hist.count.match", matches.length, rows.length)
      : wsT("js.cexec.hist.count.total", rows.length);

    var live = Object.create(null);
    matches.forEach(function (item) { live[item.row.cmd] = true; });
    Object.keys(histRowMap).forEach(function (cmd) {
      if (!live[cmd]) {
        if (histRowMap[cmd].parentNode) histRowMap[cmd].parentNode.removeChild(histRowMap[cmd]);
        delete histRowMap[cmd];
      }
    });

    for (var j = 0; j < matches.length; j++) {
      var item = matches[j], r = item.row, el = histRowMap[r.cmd];
      if (!el) el = histRowMap[r.cmd] = histCreateRowEl(r.cmd);
      el.__cmd = r.cmd;
      var cmdEl = el.children[0], exEl = el.children[1], agoEl = el.children[2];
      histRenderCmdNodes(cmdEl, r.cmd, item.idx);
      if (typeof r.exit === "number") {
        exEl.hidden = false;
        exEl.className = "chist-ex " + (r.exit === 0 ? "ok" : "fail");
        var exTxt = String(r.exit);
        if (exEl.textContent !== exTxt) exEl.textContent = exTxt;
      } else {
        exEl.hidden = true;
      }
      var agoTxt = bangAgo(r.ts);
      if (agoEl.textContent !== agoTxt) agoEl.textContent = agoTxt;
      var curNode = histListEl.children[j];
      if (curNode !== el) histListEl.insertBefore(el, curNode || null);
    }

    histListEl.hidden = !matches.length;
    histEmptyEl.hidden = !!matches.length;
    pop.hidden = false;
    histPatchSelection(false);
    histSyncBadge();
  }
  function bangHistOpen(manual) {
    var tag = bangHistTag(), rows = tag ? bangHistRows(tag) : [];
    if (!tag || !rows.length) return false;
    histOpen = true;
    histManual = !!manual;
    histSelIdx = rows.length ? 0 : -1;
    histResetClearConfirm();
    bangHistSyncMode();
    histRender();
    return true;
  }
  function bangHistOpenManual() {
    histSuppressAuto = false;
    if (!bangHistOpen(true)) return;
    if (ctext) ctext.focus();
  }
  function bangHistClose(suppressAuto) {
    if (!histOpen && !histPop) return;
    histOpen = false;
    histManual = false;
    if (suppressAuto && bangMode()) histSuppressAuto = true;
    histSelIdx = -1;
    histMatches = [];
    histResetClearConfirm();
    if (histPop) histPop.hidden = true;
    histSyncBadge();
  }
  function histPickCmd(cmd) {
    histSuppressAuto = true;
    bangHistClose(true);
    editText(0, ctext.value.length, "!" + cmd);
  }
  function histDeleteRow(tag, cmd) {
    if (!tag) return;
    histCancelPending(tag, cmd);
    var store = histReadStore(tag);
    var delAt = bangHistNow();
    store.rows.forEach(function (r) { if (r.cmd === cmd && r.ts > delAt) delAt = r.ts; });
    store.deleted[cmd] = Math.max(store.deleted[cmd] || 0, delAt);
    store.rows = store.rows.filter(function (r) { return r.cmd !== cmd; });
    histWriteStore(tag, store);
    histSyncBadge();
    if (histOpen) histRender();
  }
  function histClearAll(tag) {
    if (!tag) return;
    histCancelPending(tag);
    var store = histReadStore(tag);
    var clearAt = bangHistNow();
    store.rows.forEach(function (r) { if (r.ts > clearAt) clearAt = r.ts; });
    histWriteStore(tag, { v: 1, clearedAt: clearAt, deleted: {}, rows: [] });
    bangHistClose(true);
    histSyncBadge();
  }
  function bangHistSyncInput() {
    if (!cexecEnabled || !ctext) return;
    var v = typeof ctext.value === "string" ? ctext.value : "";
    if (v.charAt(0) !== "!") {
      histSuppressAuto = false;
      if (histOpen && (!histManual || v.length > 0)) bangHistClose(false);
      else if (histOpen) histRender();
      return;
    }
    var tag = bangHistTag(), rows = tag ? bangHistRows(tag) : [];
    if (!tag || !rows.length) {
      if (histOpen) bangHistClose(false);
      return;
    }
    if (histOpen) histRender();
    else if (!histSuppressAuto) bangHistOpen(false);
  }
  function bangHistSyncBinding() {
    var nextTag = bangHistTag();
    var changed = nextTag !== histBoundTag;
    histBoundTag = nextTag;
    histSyncBadge();
    if (histOpen) {
      if (!nextTag || !bangHistRows(nextTag).length || changed) {
        if (bangMode() && nextTag && bangHistRows(nextTag).length && !histSuppressAuto) histRender();
        else bangHistClose(false);
      }
    } else if (changed && bangMode() && !histSuppressAuto) {
      bangHistSyncInput();
    }
  }
  function bangHistKey(e) {
    if (!cexecEnabled) return false;
    var isCtrlR = e.ctrlKey && !e.metaKey && !e.altKey && (e.key === "r" || e.key === "R");
    if (!histOpen) {
      if (isCtrlR && bangMode() && bangHistOpen(false)) { e.preventDefault(); return true; }
      return false;
    }
    if (e.key === "Escape" && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      bangHistClose(true);
      return true;
    }
    if (e.key === "ArrowDown" && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      if (histMatches.length) {
        histSelIdx = histSelIdx < 0 ? 0 : (histSelIdx + 1) % histMatches.length;
        histPatchSelection(true);
      }
      return true;
    }
    if (e.key === "ArrowUp" && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      if (histMatches.length) {
        histSelIdx = histSelIdx <= 0 ? histMatches.length - 1 : histSelIdx - 1;
        histPatchSelection(true);
      }
      return true;
    }
    if (isCtrlR) {
      e.preventDefault();
      if (histMatches.length) {
        histSelIdx = histSelIdx < 0 ? 0 : (histSelIdx + 1) % histMatches.length;
        histPatchSelection(true);
      }
      return true;
    }
    if (e.key === "Tab" && !e.ctrlKey && !e.metaKey && !e.altKey) {
      if (histMatches.length && histSelIdx >= 0 && histMatches[histSelIdx]) {
        e.preventDefault();
        if (!e.repeat) histPickCmd(histMatches[histSelIdx].row.cmd);
        return true;
      }
      return false;
    }
    if (e.key === "Enter") {
      var mod = (e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey;
      var plain = !e.ctrlKey && !e.metaKey && !e.shiftKey && !e.altKey;
      if ((plain || mod) && histMatches.length && histSelIdx >= 0 && histMatches[histSelIdx]) {
        e.preventDefault();
        if (!e.repeat) histPickCmd(histMatches[histSelIdx].row.cmd);
        return true;
      }
    }
    return false;
  }
  if (cexecEnabled) {
    doc.addEventListener("mousedown", function (e) {
      if (!histOpen) return;
      if (composer && composer.contains(e.target)) return;
      bangHistClose(true);
    });
    window.addEventListener("storage", function (e) {
      var eventTag = "";
      if (e && typeof e.key === "string" && e.key) {
        if (e.key.indexOf(BANG_HIST_PREFIX) !== 0) return;
        eventTag = e.key.slice(BANG_HIST_PREFIX.length);
      } else {
        eventTag = bangHistTag();
      }
      if (!eventTag) return;
      var store = histReadStore(eventTag);
      var byCmd = Object.create(null);
      store.rows.forEach(function (r) { byCmd[r.cmd] = r; });
      Object.keys(histPending).forEach(function (eid) {
        var p = histPending[eid];
        if ((p.verifiedTag || p.launchTag) !== eventTag) return;
        if ((store.clearedAt && p.launchTs <= store.clearedAt) ||
            (store.deleted[p.cmd] && p.launchTs <= store.deleted[p.cmd]) ||
            (p.recorded && (!byCmd[p.cmd] || byCmd[p.cmd].eid !== p.exec_id))) {
          p.cancelled = true;
          clearTimeout(p.timer);
          delete histPending[eid];
        }
      });
      if (eventTag === bangHistTag()) {
        histSyncBadge();
        if (histOpen) histRender();
      }
    });
    histEnsureBadge();
    bangHistSyncBinding();
  }
  if (cexecEnabled && ctext) ctext.addEventListener("input", bangSync);
  cexecPrime();
