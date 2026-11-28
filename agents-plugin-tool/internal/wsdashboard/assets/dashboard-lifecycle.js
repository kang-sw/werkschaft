  // --- notifications -------------------------------------------------------------
  // Only the page that owns the rail notifies (a framed page's shell does),
  // from each read of the rail's rows (the live region, or a hidden tab's
  // held update); a row's first read only sets its baseline. It notifies
  // of three things. A row carries data-ap while its hosted session waits
  // on a tool approval: a wait the read before did not have is one. A
  // top-level session that finished work of its own (data-active), once
  // nothing of it runs any more (no child, background task, or exec job;
  // the server's data-busy), is one, and a child finishing under a busy
  // parent is none. A board question a
  // row's data-asks opened since the read before is one. The viewed
  // session's own, in a tab the user is looking at, is in front of them
  // and is none. One tag per event keeps several open tabs, and the
  // Werkschaft shell, to one notification. A page not on the daemon's
  // machine cannot answer a wait, and its notification says so.
  var apSeen = null, apHeld = [], railNotices = WsRail ? WsRail.notices() : null;
  function notifAsk() {
    try {
      if (window.Notification && window.Notification.permission === "default") window.Notification.requestPermission();
    } catch (e) { /* no notifications in this browser */ }
  }
  // notifDeliver shows the notification n (title, lines, tag, go: what a
  // click opens, keep: whether a held one still applies at a later read)
  // once, never twice. A secondary Werkschaft window shows none, as its
  // main window does. The main window's shell shows it natively
  // (werkschaftNotify, injected after load, so read here); only when that
  // is missing, refused, or fails does the browser's show it, when the
  // user granted it (asked on a click that starts a session, answers an
  // approval, or sends a message on a hosted page, never on load), else a
  // toast, once the tab shows.
  function notifDeliver(n) {
    if (window.werkschaftSecondary === true) return;
    var shell = window.werkschaftNotify, shown;
    if (!shell || typeof shell.notify !== "function") { notifFallback(n); return; }
    try { shown = shell.notify(n.title, n.lines.join("\n"), n.tag); }
    catch (e) { notifFallback(n); return; }
    Promise.resolve(shown).then(function (ok) { if (ok !== true) notifFallback(n); }, function () { notifFallback(n); });
  }
  function notifFallback(n) {
    try {
      if (window.Notification && window.Notification.permission === "granted") {
        var b = new window.Notification(n.title, { body: n.lines.join("\n"), tag: n.tag });
        b.onclick = function () { window.focus(); b.close(); n.go(); };
        return;
      }
    } catch (e) { /* a browser that notifies only through a service worker */ }
    if (doc.hidden) apHeld.push(n);
    else notifToast(n);
  }
  function notifToast(n) {
    if (!window.WsToast) return;
    window.WsToast.show([n.title].concat(n.lines).join(" · "), { kind: "notice", onClick: n.go });
  }
  function notifViewed(id) { return id === rid && !doc.hidden && (!doc.hasFocus || doc.hasFocus()); }
  function notifGo(id) {
    if (id === rid) return;
    saveDraft();
    go("/s/" + encodeURIComponent(id));
  }
  // notifWho is the session as a notification's title names it: its
  // workspace or project, then its reply ID.
  function notifWho(row) {
    var g = row.closest(".rws, .project"), nm = g && g.querySelector(".nm, .pname");
    return (nm ? nm.textContent + "/" : "") + (row.getAttribute("data-rid") || "");
  }
  function apKey(r) { return r.rid + "\n" + r.tool + "\n" + r.args; }
  function apRows(el) {
    return q("[data-ap]", el).map(function (row) {
      var g = row.closest(".rws, .project"), nm = g && g.querySelector(".nm, .pname");
      return { rid: row.getAttribute("data-rid") || "", tool: row.getAttribute("data-ap-tool") || "", args: row.getAttribute("data-ap-args") || "", project: nm ? nm.textContent : "" };
    });
  }
  // apScan reads the rows of el, the region name ("rail", or the index's
  // "sessions", whose rows carry approvals only). baseline (a reconnect's
  // first full rail) records the rail's finishes and questions without
  // notifying, as attention does: a restarted daemon re-derives every
  // row's state, and what happened while the stream was down is not news.
  // Waits keep their baseline across reconnects.
  function apScan(el, name, baseline) {
    // A framed page's shell notifies, from its rail.
    if (!el || framed) return;
    var now = {}, fresh = [], rows = null, events = [];
    apRows(el).forEach(function (r) {
      var k = apKey(r);
      if (apSeen && !apSeen[k] && !now[k]) fresh.push(r);
      now[k] = true;
    });
    apSeen = now;
    if (name === "rail" && railNotices) {
      var list = q(".rs[data-rid]", el);
      rows = {};
      list.forEach(function (row) { rows[row.getAttribute("data-rid")] = row; });
      events = railNotices.read(list, !!baseline);
    }
    apHeld = apHeld.filter(function (n) { return n.keep(now, rows); });
    fresh.forEach(apNotify);
    events.forEach(railNotify);
  }
  // apText is a wait's notification: the session in the title; the tool,
  // its arguments, and (away from the daemon's machine) where to answer in
  // the body's lines.
  function apText(r) {
    var lines = [toolLabel(r.tool) + (r.tool && r.args ? " · " : "") + r.args];
    if (!root.hasAttribute("data-sysopen")) lines.push(wsT("js.notif.lan"));
    return { title: wsT("js.notif.title", (r.project ? r.project + "/" : "") + r.rid), lines: lines.filter(Boolean) };
  }
  function apNotify(r) {
    if (notifViewed(r.rid)) return;
    var t = apText(r), k = apKey(r);
    notifDeliver({ title: t.title, lines: t.lines, tag: "ws-approval:" + k, go: function () { notifGo(r.rid); }, keep: function (now) { return !!now[k]; } });
  }
  // railNotify is a rail event's notification (WsRail.notices): a finish
  // names the row's last message, a question how many opened. A held one
  // is dropped once its row is gone, a finished session works again, or
  // every question it names has closed.
  function railNotify(e) {
    if (notifViewed(e.rid)) return;
    var who = notifWho(e.row), title, lines = [], keep;
    if (e.kind === "idle") {
      var msg = e.row.querySelector(".drmsg .s2"), text = msg ? msg.textContent : "", by = msg && msg.querySelector(".who");
      if (by && text.indexOf(by.textContent) === 0) text = by.textContent + " · " + text.slice(by.textContent.length);
      // bounded to 200 code points, so an emoji is never cut in half
      var chars = Array.from(text.replace(/\s+/g, " ").trim());
      if (chars.length) lines.push(chars.length > 200 ? chars.slice(0, 199).join("") + "…" : chars.join(""));
      title = wsT("js.notif.idle.title", who);
      keep = function (now, rows) {
        var r = rows ? rows[e.rid] : e.row;
        return !!r && r.getAttribute("data-busy") !== "1" && !r.classList.contains("ended");
      };
    } else {
      lines.push(wsTn("js.notif.ask.body", e.ids.length));
      title = wsT("js.notif.ask.title", who);
      keep = function (now, rows) {
        var r = rows ? rows[e.rid] : e.row, open = r && WsRail.rowAsks(r);
        return !!open && e.ids.some(function (id) { return open.has(id); });
      };
    }
    notifDeliver({ title: title, lines: lines, tag: e.tag, go: function () { notifGo(e.rid); }, keep: keep });
  }
  doc.addEventListener("visibilitychange", function () {
    if (doc.hidden) return;
    var held = apHeld;
    apHeld = [];
    held.forEach(notifToast);
  });
  if (hostedPage) {
    doc.addEventListener("click", function (e) {
      var t = e.target, chip = t && t.closest && t.closest(".host.hbtn");
      if (chip && (railEl || framed) && WsRail) { e.preventDefault(); railPopOpen("sess", chip); }
      else if (t && t.closest && t.closest("#csend")) notifAsk();
    });
    doc.addEventListener("keydown", function (e) {
      var t = e.target;
      if ((e.key !== "Enter" && e.key !== " ") || !t || !t.classList || !t.classList.contains("hbtn") || !(railEl || framed) || !WsRail) return;
      e.preventDefault();
      railPopOpen("sess", t);
    });
  }

  // --- per-session drafts ---------------------------------------------------------
  // Keep the legacy text projection readable by older dashboard pages.
  // Blobs and delivery records live separately in browser-owned IndexedDB.
  var draftStore = WsRail && rid && !shell && mode !== "history" ? WsRail.drafts(railStore) : null;
  var draftTimer = null, restoring = false, draftUnsaved = false, draftDetached = false;
  var draftDB = null, draftReady = null, draftLoaded = false;
  var draftLegacy = null, draftCommitted = null, draftWrite = null, draftWriteKey = "";
  var draftDeliveryState = new Map(), draftRetained = null, draftRetainedImages = [];
  // Image IDs this renderer has loaded or written, each with its content
  // key ("" until its bytes are read). Another window's image in the shared
  // active record is carried over, never dropped. An older page writes
  // images without an ID (and strips the IDs it restored); such an entry is
  // only ever matched by its bytes (draftContentKey), never by metadata.
  var draftKnownImages = new Map(), draftDigests = new WeakMap();
  function draftDigest(blob) {
    var subtle = window.crypto && window.crypto.subtle;
    if (!blob || typeof blob.arrayBuffer !== "function" || !subtle) return Promise.resolve("");
    var known = draftDigests.get(blob);
    if (known) return known;
    known = blob.arrayBuffer().then(function (bytes) { return subtle.digest("SHA-256", bytes); }).then(function (hash) {
      return Array.from(new Uint8Array(hash), function (b) { return (b + 256).toString(16).slice(1); }).join("");
    }).catch(function () { return ""; });
    draftDigests.set(blob, known);
    return known;
  }
  // The slot, name, type and SHA-256 of an image's bytes; "" when they cannot
  // be read. A legacy ID-less image gets "legacy:" + its key as its ID, the
  // same in every renderer that restores it.
  function draftContentKey(p) {
    if (!p || !p.blob) return Promise.resolve("");
    return draftDigest(p.blob).then(function (digest) {
      return digest ? "c:" + [p.n, p.name, p.blob.type, p.blob.size, digest].join("\u0000") : "";
    });
  }
  function draftKnow(id, key) {
    if (id) draftKnownImages.set(id, key || draftKnownImages.get(id) || "");
  }
  // Removal wins: an image ID this renderer knew and no longer holds goes in
  // the row's removed set (with its content key, which matches the same
  // bytes written back without an ID), so a stale window cannot bring it
  // back. Only the renderer that removed it lifts it again (an undo, a
  // returned send). The set changes only once a write commits.
  var draftTombstoned = new Set(), DRAFT_TOMBSTONES = 64;

  function draftDatabase() {
    return new Promise(function (resolve) {
      var request;
      try { request = window.indexedDB.open("ws.dashboard.recovery", 1); }
      catch (e) { resolve(null); return; }
      request.onupgradeneeded = function () { request.result.createObjectStore("drafts"); };
      request.onerror = request.onblocked = function () { resolve(null); };
      request.onsuccess = function () { resolve(request.result); };
    });
  }
  function draftTransaction(write, update) {
    return new Promise(function (resolve, reject) {
      var tx = draftDB.transaction("drafts", write ? "readwrite" : "readonly");
      var store = tx.objectStore("drafts"), request = store.get(rid), result;
      request.onsuccess = function () {
        result = request.result || null;
        if (!write) return;
        try {
          // undefined leaves the row as it is.
          var next = update(result);
          if (next === undefined) return;
          result = next;
          if (result) store.put(result, rid);
          else store.delete(rid);
        } catch (e) { tx.abort(); }
      };
      tx.oncomplete = function () { resolve(result); };
      tx.onabort = function () { reject(tx.error || new Error("Draft transaction aborted")); };
    });
  }
  function draftProjection(d) { return WsRail.emptyDraft(d) ? null : JSON.stringify(d); }
  function savedImage(p) {
    if (!p.id) p.id = Date.now().toString(36) + "-" + Math.random().toString(36).slice(2);
    return { id: p.id, n: p.n, name: p.name, ref: p.ref, blob: p.blob };
  }
  function loadDraftImage(p) {
    var id = p.id || "";
    return p.ref ? { id: id, n: p.n, name: p.name, ref: p.ref, url: attachSrc(p.ref), data: null } :
      { id: id, n: p.n, name: p.name, ref: "", blob: p.blob, url: URL.createObjectURL(p.blob), data: null };
  }
  function imageData(blob) {
    return new Promise(function (resolve, reject) {
      var reader = new FileReader();
      reader.onload = function () { var value = String(reader.result); resolve(value.slice(value.indexOf(",") + 1)); };
      reader.onerror = reject;
      reader.readAsDataURL(blob);
    });
  }
  function saveLegacyDraft(d, projection) {
    if (projection === draftLegacy) return true;
    if (draftStore.save(rid, d) === false) return false;
    draftLegacy = projection;
    return true;
  }
  function readDraft() {
    var b = region("board"), open = {};
    Object.keys(openState).forEach(function (k) { if (openState[k] && k.indexOf("k:history:") !== 0) open[k] = true; });
    var prefix = inlineEditor() ? "" : selectionPrefix(bselections);
    var shift = function (r) { return Object.assign({}, r, { start: r.start + prefix.length, end: r.end + prefix.length }); };
    var d = {
      boardIncarnation: typeof boardIncarnation === "string" ? boardIncarnation : null,
      text: prefix + (ctext ? ctext.value : ""),
      capsules: ctext && ctext.wsc ? ctext.wsc.snapshot().map(shift) : [],
      selections: inlineEditor() ? bselections.map(function (s) { return { key: s.key, mentions: s.mentions, comment: s.comment, images: s.images }; }) : [],
      cmd: cmdSel ? { name: cmdSel.name, args: cmdSel.args } : null,
      mentions: mentions.map(function (m) { return shift({ start: m.start, end: m.end, text: m.text }); }),
      answers: b ? boardRead(readAnswers) : {},
      chips: chipList(),
      bmentions: bmentions.slice(),
      closeRequests: closeRequests.map(function (r) { return { topic: r.topic, reason: r.reason }; }),
      wpicks: wpicks.slice(),
      wconsoles: wconsoles.slice(),
      refs: imgs.filter(function (p) { return !!p.ref; }).map(function (p) { return { ref: p.ref, name: p.name, n: p.n }; }),
      textRefs: typeof textRefs !== "undefined" ? textRefs.map(function (r) { return { n: r.n, body: r.body }; }) : [],
      txtSeq: typeof txtSeq === "number" ? txtSeq : 0,
      lost: imgs.filter(function (p) { return !p.ref; }).map(function (p) { return p.n; }),
      open: open,
      tab: live ? grid.getAttribute("data-tab") || "" : ""
    };
    if (ctext && d.text) d.sel = [ctext.selectionStart + prefix.length, ctext.selectionEnd + prefix.length];
    // A board-only view still edits answers and open details. Its DOM
    // cannot represent composer fields from this session's live draft.
    if (!ctext && draftRetained) {
      return Object.assign({}, draftRetained, { boardIncarnation: d.boardIncarnation, answers: d.answers, open: d.open });
    }
    return d;
  }
  // The key leaves out record.projection, which carries a fresh stamp each write.
  function draftRecoveryKey(record, states) {
    var images = record ? record.images : [];
    return JSON.stringify([record && draftProjection(record.draft),images.map(function (p) { return [p.n, p.name]; }), [...states]]);
  }
  function saveDraft() {
    if (!draftStore || !draftLoaded || restoring || draftDetached) return Promise.resolve(!draftUnsaved);
    clearTimeout(draftTimer);
    draftTimer = null;
    var d = readDraft(), previous = draftLegacy;
    delete d.stamp;
    var projection = draftProjection(d);
    var stamp = Date.now().toString(36) + "-" + Math.random().toString(36).slice(2);
    var images = ctext ? imgs.filter(function (p) { return !p.ref && p.blob; }).map(savedImage) : draftRetainedImages;
    images.forEach(function (p) {
      draftKnow(p.id);
      draftContentKey(p).then(function (key) { draftKnow(p.id, key); });
    });
    var deliveries = outbox.filter(function (m) { return !m.acked; }).map(function (m) {
      var body = Object.assign({}, m.body);
      delete body.images;
      return { id: m.recoveryID, state: m.recoveryState === "queued" ? "queued" : "unknown",
        body: body, back: m.back, pics: m.pics.map(savedImage) };
    });
    // Keep the old pre-send projection until the recovery record commits.
    // A POST starts only after this transaction (including its mirror).
    // A completed send may be the last IndexedDB record for this session.
    // Confirm the new legacy projection before dropping that recovery copy.
    // A draft with images mirrors only once its record is accepted: a
    // refused merge must leave the committed record's projection in place.
    var mirrorNow = !deliveries.length && !images.length;
    var textSaved = mirrorNow && saveLegacyDraft(d, projection);
    // A mirror written after its commit carries that write's stamp: its
    // completion can reach this renderer after another window committed and
    // mirrored, and a restore then trusts the row over the stamped mirror.
    // record.projection is the exact mirror string, so pages that compare it
    // with the legacy draft still match.
    var mirror = Object.assign({}, d, { stamp: stamp }), written = mirrorNow ? projection : draftProjection(mirror);
    function mirrorLegacy() { return mirrorNow ? saveLegacyDraft(d, projection) : saveLegacyDraft(mirror, written); }
    var missingBlob = (d.lost || []).some(function (number) {
      return !images.some(function (image) { return image.n === number; });
    });
    var hasDraft = !WsRail.emptyDraft(Object.assign({}, d, { open: {}, tab: "" }));
    var rich = !!(images.length || deliveries.length || mirrorNow && !textSaved && hasDraft);
    // mine lists the images this draft saved: only they answer its
    // markers on restore; another window's images come back loose.
    var record = rich ? {
      base: previous, projection: written, draft: d, images: images,
      mine: images.map(function (p) { return p.id; })
    } : null;
    var states = new Map(deliveries.map(function (m) { return [m.id, m.state]; }));
    var key = draftRecoveryKey(record, states);
    var clean = draftRecoveryKey(null, new Map());
    if (key === draftCommitted && !draftWrite) {
      draftUnsaved = missingBlob || !(textSaved || rich || !hasDraft || saveLegacyDraft(d, projection));
      return Promise.resolve(!draftUnsaved);
    }
    if (draftWrite) {
      if (key === draftWriteKey) return draftWrite;
      return draftWrite.then(saveDraft);
    }
    if (key === clean && draftCommitted === null && !draftWrite) {
      draftUnsaved = missingBlob || !textSaved && hasDraft;
      return Promise.resolve(!draftUnsaved);
    }
    draftUnsaved = true;
    if (!draftDB) {
      // No recovery record can hold the images: keep the text at least.
      if (!deliveries.length) saveLegacyDraft(d, projection);
      return Promise.resolve(false);
    }
    var before = draftDeliveryState, conflicts = [], pass = null;
    draftDeliveryState = states;
    // mergeImages merges this renderer's images into the row's active
    // record. Another window may have committed images this one never
    // loaded: their bytes are kept, and a restore reattaches them as loose
    // markers. looseKeys are the content keys of the row's ID-less entries in
    // order, and ownKeys those of images; without them (null) every ID-less
    // entry is carried over as it is.
    function mergeImages(existing, looseKeys, ownKeys) {
      var current = existing && existing.active, active = record;
      var removed = new Set(existing && Array.isArray(existing.removed) ? existing.removed : []);
      var own = new Set(), lift = [], bury = [], loose = [], foreign = [], held = new Set(), at = 0;
      images.forEach(function (p) {
        own.add(p.id);
        if (!draftTombstoned.has(p.id)) return;
        lift.push(p.id);
        removed.delete(p.id);
        removed.delete(draftKnownImages.get(p.id));
      });
      draftKnownImages.forEach(function (key, id) {
        if (own.has(id)) return;
        if (!draftTombstoned.has(id)) { bury.push(id); removed.add(id); }
        if (key && removed.has(id)) removed.add(key);
      });
      var kept = function (p) { return !removed.has(p.id); };
      images.forEach(function (p, i) { if (ownKeys && ownKeys[i] && kept(p)) held.add(ownKeys[i]); });
      (current && Array.isArray(current.images) ? current.images : []).forEach(function (p) {
        if (!p) return;
        if (p.id) {
          if (!draftKnownImages.has(p.id) && kept(p)) foreign.push(p);
          return;
        }
        loose.push(p);
        var key = looseKeys && looseKeys[at++] || "";
        if (!key) { foreign.push(p); return; }
        // The same bytes this renderer's record holds (a restored copy keeps
        // the legacy ID of its first slot after a renumber), or removed bytes.
        if (held.has(key) || own.has("legacy:" + key) || removed.has(key)) return;
        held.add(key);
        foreign.push(Object.assign({}, p, { id: "legacy:" + key }));
      });
      if (foreign.length || record && !record.images.every(kept)) {
        active = Object.assign({}, record || { base: previous, projection: written, draft: d, images: [], mine: [] });
        active.images = active.images.filter(kept).concat(foreign);
      }
      // Two windows' images may merge past what one message carries
      // (IMG_MAX, wsinbox.MaxImages). Nothing saved is dropped to fit: the
      // row keeps its images and this save reports failure, so this window
      // holds its draft as unsaved until images are removed.
      var full = !!(foreign.length && active.images.length > IMG_MAX);
      if (full) active = Object.assign({}, current, { images: current.images.filter(function (p) { return !p || !p.id || kept(p); }) });
      return { active: active, removed: Array.from(removed).slice(-DRAFT_TOMBSTONES), full: full, lift: lift, bury: bury, loose: loose };
    }
    function settleTombstones(merge) {
      merge.lift.forEach(function (id) { draftTombstoned.delete(id); });
      merge.bury.forEach(function (id) { draftTombstoned.add(id); });
    }
    var first = draftTransaction(true, function (existing) {
      var merged = Object.assign({}, existing && existing.deliveries);
      before.forEach(function (state, id) {
        if (!states.has(id) && merged[id] && merged[id].state === state) delete merged[id];
      });
      deliveries.forEach(function (m) {
        var previous = before.get(m.id);
        if (m.state === previous) return;
        // Another renderer may have started or already finished this queued
        // request. Only one transaction may claim its shared queued row.
        if (previous === "queued" && (!merged[m.id] || merged[m.id].state !== "queued")) {
          conflicts.push(m.id);
          states.delete(m.id);
          return;
        }
        merged[m.id] = m;
      });
      pass = mergeImages(existing, null, null);
      if (!pass.active && !Object.keys(merged).length && !pass.removed.length) return null;
      // stamp marks this write: a second pass applies only to the row as
      // this one left it (an older page's write drops the field).
      return { active: pass.active, deliveries: merged, removed: pass.removed, stamp: stamp };
    });
    // The first write starts at once (it may run on pagehide) and carries
    // every ID-less entry over. Then their bytes are read and, while no
    // other write came between, a second pass drops the copies of this
    // renderer's own or removed bytes and gives the rest their legacy ID.
    // superseded is set when another write replaced the row while this one
    // read bytes: that writer has mirrored its own newer projection, which
    // this renderer's late mirror must not overwrite.
    var superseded = false;
    var write = first.then(function () {
      settleTombstones(pass);
      if (!pass.loose.length) return pass;
      return Promise.all([Promise.all(pass.loose.map(draftContentKey)), Promise.all(images.map(draftContentKey))]).then(function (keys) {
        var second = null;
        return draftTransaction(true, function (existing) {
          if (!existing || existing.stamp !== stamp) { superseded = true; return undefined; }
          if (!keys[0].some(Boolean)) return undefined;
          second = mergeImages(existing, keys[0], keys[1]);
          return { active: second.active, deliveries: existing.deliveries, removed: second.removed, stamp: stamp };
        }).then(function () {
          if (!second) return pass;
          settleTombstones(second);
          return second;
        }, function () { return pass; });
      });
    }).then(function (merge) {
      if (draftWrite !== write) return true;
      draftWrite = null;
      conflicts.forEach(function (id) {
        var delivery = outbox.find(function (m) { return m.recoveryID === id; });
        if (delivery) delivery.recoveryConflict = true;
      });
      if (merge.full) { draftUnsaved = true; return false; }
      draftCommitted = draftRecoveryKey(record, states);
      // A superseded save's mirror would only be outranked by the row.
      draftUnsaved = missingBlob || !superseded && !mirrorLegacy() && !rich && hasDraft;
      return !draftUnsaved;
    }, function () {
      if (draftWrite === write) {
        draftWrite = null;
        draftDeliveryState = before;
      }
      return false;
    });
    draftWrite = write;
    draftWriteKey = key;
    return write;
  }
  function queueDraft() {
    if (!draftStore || restoring || draftDetached) return;
    clearTimeout(draftTimer);
    draftTimer = setTimeout(saveDraft, 400);
  }
  function restoreDraftMarkers(nums) {
    if (!nums.length) return;
    var start = ctext.selectionStart, end = ctext.selectionEnd, length = ctext.value.length;
    ctext.setSelectionRange(length, length);
    insertMarkers(nums);
    ctext.setSelectionRange(start, end);
  }
  function startDraftRecovery() {
    if (!draftStore) return;
    var legacy = draftStore.load(rid);
    draftLegacy = draftProjection(legacy);
    if (grid) grid.inert = true;
    draftReady = draftDatabase().then(function (db) {
      draftDB = db;
      return db ? draftTransaction(false).catch(function () { draftDB = null; return null; }) : null;
    }).then(function (record) {
      var stored = record && record.active && Array.isArray(record.active.images) ? record.active.images : [];
      return Promise.all(stored.map(draftContentKey)).then(function (keys) { return { record: record, keys: keys }; });
    }).then(function (loaded) {
      var record = loaded.record, active = record && record.active, removed = new Set(record && Array.isArray(record.removed) ? record.removed : []);
      if (active && Array.isArray(active.images)) {
        // An ID-less entry takes its legacy ID from its bytes; one that
        // repeats a kept image's bytes, or matches removed bytes, is a copy.
        var seen = new Set(), images = [];
        active.images.forEach(function (p, i) {
          if (p && p.id && !removed.has(p.id)) { seen.add(p.id); if (loaded.keys[i]) seen.add(loaded.keys[i]); }
        });
        active.images.forEach(function (p, i) {
          var key = loaded.keys[i];
          if (!p || (p.id ? removed.has(p.id) : key && (removed.has(key) || seen.has(key) || seen.has("legacy:" + key)))) return;
          if (!p.id && key) seen.add(key);
          var image = Object.assign({}, p, { id: p.id || (key ? "legacy:" + key : "") });
          if (!image.id && !ctext) savedImage(image);
          draftKnow(image.id, key);
          images.push(image);
        });
        active = Object.assign({}, active, { images: images });
      }
      // A stamped legacy draft is a new page's mirror, which may have landed
      // after a newer window's: between new pages the row decides. An older
      // page writes it without a stamp, and an edit it made shows as a
      // projection matching neither the row's base nor its projection.
      var stampedLegacy = !!(legacy && typeof legacy.stamp === "string");
      var useActive = active && (stampedLegacy || draftLegacy === active.base || draftLegacy === active.projection);
      var prepared = useActive ? active.draft : legacy;
      if (!ctext) {
        draftRetained = prepared && boardDraft(JSON.parse(JSON.stringify(prepared)));
        draftRetainedImages = active ? active.images : [];
      }
      restoreDraft(prepared, useActive ? active.images : [], useActive && Array.isArray(active.mine) ? new Set(active.mine) : null);
      if (active && !useActive && ctext) {
        // An older page used the legacy draft in the meantime. Keep its
        // current text and put the retained blobs back under new markers.
        var nums = active.images.map(function (p) {
          var image = loadDraftImage(p);
          image.n = nextNum();
          imgs.push(image);
          return image.n;
        });
        restoreDraftMarkers(nums);
        renderImgs();
      }
      var deliveries = ctext ? Object.values(record && record.deliveries || {}) : [];
      deliveries.forEach(function (m) { draftDeliveryState.set(m.id, m.state); });
      restoreDeliveries(deliveries);
      if (record) draftCommitted = draftRecoveryKey(active || null, draftDeliveryState);
    }).finally(function () {
      draftLoaded = true;
      if (grid) grid.inert = false;
      if (!draftDetached && ctext) saveDraft();
    });
  }
  function boardDraft(d) {
    if (d && typeof d.boardIncarnation === "string" && d.boardIncarnation !== boardIncarnation) {
      // Keep free text and image references, never reattach replaced IDs.
      var text = d.text || "", ranges = Array.isArray(d.capsules) ? d.capsules.slice() : [];
      ranges.filter(function (r) { return /^(answer|topic-note|mention)$/.test(r.kind); }).reverse().forEach(function (cut) {
        text = text.slice(0, cut.start) + text.slice(cut.end);
        ranges = ranges.filter(function (r) { return r !== cut && (r.end <= cut.start || r.start >= cut.end); }).map(function (r) {
          return r.start >= cut.end ? Object.assign({}, r, { start: r.start - (cut.end - cut.start), end: r.end - (cut.end - cut.start) }) : r;
        });
      });
      return Object.assign({}, d, { boardIncarnation: boardIncarnation, text: text, capsules: ranges, selections: [], answers: {}, chips: [], bmentions: [], closeRequests: [] });
    }
    return d;
  }
  // restoreDraft restores saved with recoveredImages. With mine (the IDs
  // the draft saved; an older page's record has none) only those images
  // answer the draft's markers by number, the rest come back loose.
  function restoreDraft(saved, recoveredImages, mine) {
    var d = boardDraft(saved);
    recoveredImages = recoveredImages || [];
    if (!d) return;
    var savedText = typeof d.text === "string" ? d.text : "";
    restoring = true;
    var bound = function (p) { return !mine || mine.has(p.id); };
    try {
      var available = new Set(recoveredImages.filter(bound).map(function (p) { return p.n; }));
      var lost = (Array.isArray(d.lost) ? d.lost : []).filter(function (n) { return !available.has(n); });
      var named = "";
      // Keep bundles in existing text storage. Extract before stripping lost
      // markers so quoted source and memo markers are never touched.
      var split = splitSelectionText(typeof d.text === "string" ? d.text : "");
      d.text = split.text;
      d.mentions = selectionRanges(d.mentions, split.cuts);
      d.capsules = selectionRanges(d.capsules, split.cuts);
      if (inlineEditor() && Array.isArray(d.selections)) bselections = d.selections.filter(function (s) { return s && s.key && Array.isArray(s.mentions) && Array.isArray(s.images); });
      if (Array.isArray(d.sel)) {
        var caret = selectionRanges([{ start: d.sel[0], end: d.sel[1] }], split.cuts)[0];
        d.sel = caret ? [caret.start, caret.end] : [0, 0];
      }
      if (!bselections.length) bselections = split.groups.map(function (s) {
        s.images = s.images.filter(function (n) { return lost.indexOf(n) < 0; });
        return s;
      });
      bselections.forEach(function (s) { named += "\n" + s.images.map(marker).join(" "); });
      // Restore the editor before board controls reconcile their answers;
      // otherwise their upserts would consume the empty composer caret.
      if (ctext && typeof d.text === "string" && d.text && inlineEditor()) {
        ctext.value = WsRail.stripMarkers(d.text, [], lost).text;
        ctext.wsc.restore(window.WsCapsules.stripMarkers(d.text, d.capsules, lost).ranges);
      }
      if (cchips) renderChips();
      // A lost image's marker leaves the notes as it leaves the text; what
      // the restored notes still name may bring a reference image back.
      var unlost = function (note) { return typeof note === "string" && note ? WsRail.stripMarkers(note, [], lost).text : note; };
      if (d.answers && typeof d.answers === "object") Object.keys(d.answers).forEach(function (k) {
        var a = d.answers[k];
        if (a && a.note) {
          if (a.capsules && window.WsCapsules) a.capsules = window.WsCapsules.stripMarkers(a.note, a.capsules, lost).ranges;
          a.note = unlost(a.note); named += "\n" + a.note;
        }
      });
      if (Array.isArray(d.chips)) d.chips.forEach(function (c) { if (c && c.note) { c.note = unlost(c.note); named += "\n" + c.note; } });
      if (d.open && typeof d.open === "object") {
        Object.keys(d.open).forEach(function (k) {
          // A panel folded by hand outranks a draft saved while it was open.
          if (k.indexOf("k:history:") !== 0 && !Object.prototype.hasOwnProperty.call(openState, k) && !((k.indexOf("k:entry:") === 0 || k.indexOf("k:panel:") === 0) && shutHas(rid + "/" + k.slice(2)))) openState[k] = true;
        });
        restoreOpen(doc);
      }
      var b = region("board");
      if (b && d.answers && typeof d.answers === "object") {
        boardBoxes().forEach(function (box) { writeAnswers(box, d.answers); });
        if (window.wbForm) window.wbForm();
      }
      if (cchips && Array.isArray(d.chips)) {
        d.chips.forEach(function (c) { if (c && c.id && !chips[c.id]) { putChip(c); setItem(c.id, c); } });
        syncChips();
      }
      if (cchips && !closeRequests.length && validCloseRequests(d.closeRequests).length) {
        closeRequests = validCloseRequests(d.closeRequests);
        renderChips();
      }
      if (cchips && !bmentions.length && validMentions(d.bmentions).length) {
        bmentions = validMentions(d.bmentions);
        renderChips();
      }
      if (cchips && !wpicks.length && !wconsoles.length && (validPicks(d.wpicks).length || validConsoles(d.wconsoles).length)) {
        wpicks = validPicks(d.wpicks);
        wconsoles = validConsoles(d.wconsoles);
        renderChips();
      }
      if (ccmd && !cmdSel && d.cmd && typeof d.cmd.name === "string" && d.cmd.name) setCommand(d.cmd.name, d.cmd.args);
      if (ctext && typeof d.text === "string" && d.text && (!ctext.value || inlineEditor())) {
        var s = WsRail.stripMarkers(d.text, Array.isArray(d.mentions) ? d.mentions : [], lost);
        ctext.value = s.text;
        if (ctext.wsc) ctext.wsc.restore(window.WsCapsules.stripMarkers(d.text, d.capsules, lost).ranges);
        mentions = s.mentions.filter(function (m) { return liveMention(m, s.text); });
        mentionWas = ctext.value;
        named += "\n" + s.text;
        grow();
        paintMentions();
        if (typeof syncAutoMultilineFromValue === "function") syncAutoMultilineFromValue();
        if (Array.isArray(d.sel)) { try { ctext.setSelectionRange(d.sel[0], d.sel[1]); } catch (e) { /* out of range */ } }
      }
      if (ctext) {
        (Array.isArray(d.refs) ? d.refs : []).forEach(function (p) {
          if (!p || !p.ref || !markerNums(named)[p.n] || imgs.some(function (q) { return q.n === p.n; })) return;
          imgs.push({ name: p.name || p.ref, url: attachSrc(p.ref), data: null, ref: p.ref, n: p.n });
          imgSeq = Math.max(imgSeq, p.n);
        });
        var loose = [];
        // Another window's image never takes a number the draft names, even
        // one whose own image is gone: it comes back past all of them.
        if (mine) Object.keys(markerNums(savedText + named)).concat(Array.isArray(d.lost) ? d.lost : []).forEach(function (n) { imgSeq = Math.max(imgSeq, +n || 0); });
        recoveredImages.filter(bound).concat(recoveredImages.filter(function (p) { return !bound(p); })).forEach(function (p) {
          var image = loadDraftImage(p), occupied = !bound(p) || imgs.some(function (other) { return other.n === p.n; });
          if (occupied) image.n = nextNum();
          // A paste may still belong to an open selection editor. Reattach
          // unreferenced blobs to the composer when restoring the session.
          if (occupied || !markerNums(named)[p.n]) loose.push(image.n);
          imgs.push(image);
          imgSeq = Math.max(imgSeq, image.n);
        });
        restoreDraftMarkers(loose);
        imgs.sort(function (a, b) { return a.n - b.n; });
        if (typeof textRefs !== "undefined" && typeof textMarkerNums === "function") {
          if (typeof d.txtSeq === "number" && d.txtSeq > txtSeq) txtSeq = d.txtSeq;
          var haveTxt = textMarkerNums(ctext.value);
          (Array.isArray(d.textRefs) ? d.textRefs : []).forEach(function (r) {
            if (!r || !(r.n >= 1) || typeof r.body !== "string" || !haveTxt[r.n] || textRefs.some(function (x) { return x.n === r.n; })) return;
            textRefs.push({ n: r.n, body: r.body, bytes: utf8ByteLen(r.body), lines: countTextRefLines(r.body) });
            txtSeq = Math.max(txtSeq, r.n);
          });
          textRefs.sort(function (a, b) { return a.n - b.n; });
        }
        renderImgs();
      }
      if (live && d.tab === "board") setTab("board");
      if (lost.length) noteWarn(wsT("js.draft.imgslost", lost.length));
    } finally {
      restoring = false;
    }
  }
  if (draftStore) {
    ["input", "change", "click", "toggle"].forEach(function (t) { doc.addEventListener(t, queueDraft, true); });
    window.addEventListener("pagehide", saveDraft);
    doc.addEventListener("visibilitychange", function () { if (doc.hidden) saveDraft(); });
    window.addEventListener("beforeunload", WsRail.leaveGuard(function () {
      saveDraft();
      return draftUnsaved;
    }));
  }
  async function persistForWindowClose() {
    await draftReady;
    return (await saveDraft()) && !draftUnsaved;
  }
  if (framed) {
    window.wsSaveSessionDraft = saveDraft;
    window.wsPersistDraftForWindowClose = persistForWindowClose;
    window.wsPrepareSessionLeave = async function () {
      await draftReady;
      await saveDraft();
      return draftUnsaved || outboxInFlight();
    };
    window.wsFinishSessionLeave = function () {
      draftDetached = true;
      clearTimeout(draftTimer);
    };
    window.wsCanCacheSession = function () {
      saveDraft();
      // Tool runtimes cannot be frozen by ordinary page code.
      return !!rid && draftLoaded && !draftUnsaved && !outboxInFlight() &&
        !q('iframe:not(.doc-frame), .bcard, .fpane[data-max]').length && !visible(fpane) && !tkBusy &&
        (!window.WsTerm || (window.WsTerm.cacheSafe && window.WsTerm.cacheSafe())) && !Object.keys(supChats).length;
    };
  }

  // --- iframe shell -------------------------------------------------------------
  // Safe, loaded session pages are retained up to shell.js's code bound.
  // Unsafe/tool/non-session pages are replaced after their draft decision. The
  // address bar names the shown page (pushState; popstate shows the page it
  // goes back to). The shell's header mirrors the shown frame's (the head
  // message): its head region, its search button, and the header's padding
  // and height, so both headers lay out alike and the frame's own items
  // stay invisible under the shell's. The rail marks the session a page
  // opens on at once (shRid). A page the shell frames (framed) hands its
  // header, title, layout, scroll, and the shell's hotkeys to the shell,
  // and every link it would load as a page to the shell (go); shell.js
  // lists the messages.
  var shFrames = [], shOn = null, shWant = null, shLeaving = null, shHeadHtml = null, shSeq = 0, shTrackerRev = null;
  var shIndex = 0, shRestoring = null, shRestoreNav = null, shRestoreTimer = null;
  var shTop = doc.querySelector(".dwrap > .top"), shBody = doc.querySelector(".dwrap > .dbody");
  var shSettingsDialog = $("workflow-settings"), shSettingsEntry = null;
  var shCompanion = null, shCompanionFrame = null, shCompanionMuted = false, shCompanionEnabled = false;
  function shCompanionTarget(ent, refreshed, switched) {
    if (!shCompanion || shCompanionFrame === ent && !refreshed) return;
    if (shCompanionFrame && shCompanionFrame !== ent) shSend(shCompanionFrame, "companion-gate", { active: false });
    shCompanionFrame = ent;
    shCompanion.reset(ent?.f.rid, switched);
    shCompanion.trackFrame(ent?.el || null);
    if (ent) shSend(ent, "companion-gate", { active: true });
  }
  function shCompanionStop() {
    if (!shCompanion) return;
    if (shCompanionFrame) shSend(shCompanionFrame, "companion-gate", { active: false });
    shCompanionFrame = null;
    shCompanion.dispose();
    shCompanion = null;
  }
  function shCompanionConfigure(config) {
    if (!config || !window.WsCompanion) return;
    var enabled = config.enabled === true;
    if (enabled !== shCompanionEnabled) shCompanionMuted = false;
    shCompanionEnabled = enabled;
    if (shCompanionMuted || !enabled) {
      shCompanionStop();
      return;
    }
    if (shCompanion) shCompanion.apply(config);
    else {
      shCompanion = window.WsCompanion.mount(config, function () {
        shCompanionMuted = true;
        shCompanionStop();
      }, function (opener) {
        shOpenSettings(new URL("/settings?section=companion", location.origin), opener);
      });
      if (!shWant && shOn?.loaded) shCompanionTarget(shOn);
    }
  }
  function shHistoryIndex() { return history.state && Number.isSafeInteger(history.state.wsShellIndex) ? history.state.wsShellIndex : shIndex; }
  function shPush(f) {
    shIndex = shHistoryIndex() + 1;
    history.pushState({ wsShellIndex: shIndex }, "", f.path + f.hash);
  }
  function shRestoreDone() {
    clearTimeout(shRestoreTimer); shRestoreTimer = null;
    shRestoring = null;
    shIndex = shHistoryIndex();
    var nav = shRestoreNav; shRestoreNav = null;
    if (nav) shOpen(nav.href, nav.push);
  }
  function shRestoreTo(index) {
    shRestoring = index;
    clearTimeout(shRestoreTimer);
    // A traversal consumed by joint frame history may never emit a shell
    // popstate. Do not leave all subsequent navigation queued indefinitely.
    shRestoreTimer = setTimeout(shRestoreDone, 1000);
    var delta = index - shHistoryIndex();
    if (delta) history.go(delta);
  }
  function shStay(leave) {
    leave.ent.el.inert = false;
    if (leave.focus && leave.focus.isConnected) leave.focus.focus({ preventScroll: true });
    shIndex = leave.from;
    if (shWant && shWant !== leave.ent) shDrop(shWant);
    // Back has already traversed history. Restore the original position.
    if (shHistoryIndex() !== leave.from) shRestoreTo(leave.from);
  }
  // shPost sends a framed page's message of kind to its shell.
  function shPost(kind, m) {
    if (!framed) return;
    m = m || {};
    m.wsShell = kind;
    try { window.parent.postMessage(m, location.origin); } catch (x) { /* the shell left */ }
  }
  function shParentDoc() { try { return window.parent.document; } catch (x) { return doc; } }
  // go shows the page href names: a framed page asks its shell, the shell
  // shows it in a frame, and any other page, or a page the shell does not
  // frame, loads at the top level.
  function go(href) {
    var u = new URL(href, location.href);
    if (shell) { shOpen(u.href, true); return; }
    if (compact) {
      if (u.origin === location.origin && u.pathname === location.pathname && u.search === location.search && u.hash) {
        if (/^#entry=/.test(u.hash) && typeof entryJumpFromHash === "function") entryJumpFromHash(u.hash);
        else location.hash = u.hash;
        return;
      }
      window.open(u.href, "_blank", "noopener");
      return;
    }
    if (framed) {
      if (window.WsShell.framed(u, location.origin) || window.WsShell.settingsFrame(u, location.origin)) shPost("nav", { href: u.href });
      else window.top.location.href = u.href;
      return;
    }
    if (u.origin === location.origin && u.pathname === location.pathname && u.hash === location.hash && /^#entry=/.test(u.hash) && typeof entryJumpFromHash === "function") {
      entryJumpFromHash(u.hash);
      return;
    }
    location.href = u.href;
  }
  window.wsOpenSettings = function (href, opener) {
    var u = new URL(href, location.href);
    if (shell && shOpenSettings(u, opener)) return true;
    go(u.href);
    return true;
  };
  // A plain click on a link the page would load goes through go, but a
  // link to the page's own document (a hash route) and one that opens
  // elsewhere (a target, a download, a modified click).
  if (shell || framed) window.addEventListener("click", function (e) {
    if (e.defaultPrevented || e.button || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var a = e.target && e.target.closest && e.target.closest("a[href]");
    if (!a || (a.target && a.target !== "_self" && a.target !== "_top") || a.hasAttribute("download")) return;
    var u;
    try { u = new URL(a.href, location.href); } catch (x) { return; }
    if (u.protocol !== "http:" && u.protocol !== "https:") return;
    if (u.origin === location.origin && u.pathname === location.pathname && u.search === location.search && u.hash) return;
    if (shell && shOpenSettings(u, a)) { e.preventDefault(); return; }
    if (shell && !window.WsShell.framed(u, location.origin)) return;
    e.preventDefault();
    go(u.href);
  });

  // shHolds reports a frame holding unsent work; shDrafts any frame, which
  // an assets reload of the shell would drop.
  function shHolds(x) {
    try { return !!(x.el.contentWindow.wsHoldsDraft && x.el.contentWindow.wsHoldsDraft()); } catch (e) { return false; }
  }
  function shDrafts() { return shFrames.some(shHolds); }
  function shSend(ent, kind, m) {
    m = m || {};
    m.wsShell = kind;
    try { ent.el.contentWindow.postMessage(m, location.origin); } catch (x) { /* unloading */ }
  }
  function shBroadcastTheme(theme, except) {
    shFrames.forEach(function (ent) { if (ent !== except) shSend(ent, "theme", { theme: theme }); });
    if (shSettingsEntry) shSend(shSettingsEntry, "theme", { theme: theme });
    if (window.WsCompanionMessenger && typeof window.WsCompanionMessenger.syncTheme === "function") {
      window.WsCompanionMessenger.syncTheme(theme);
    }
  }
  function shOpenSettings(url, opener) {
    if (!shSettingsDialog) return false;
    var dest = window.WsShell.settingsFrame(url, location.origin);
    if (!dest) return false;
    if (location.pathname === "/settings" && location.pathname + location.search + location.hash !== dest.href) {
      history.replaceState(history.state, "", dest.href);
    }
    if (!shSettingsEntry) {
      var frame = doc.createElement("iframe");
      frame.title = shSettingsDialog.getAttribute("aria-label");
      var focus = rp.kind === "settings" ? railPopBtn() : opener || doc.activeElement;
      railPopClose(false);
      var entry = { el: frame, href: dest.href, focus: focus };
      frame.addEventListener("load", function () {
        if (shSettingsEntry === entry) shSend(entry, "theme", { theme: root.getAttribute("data-theme") || "" });
      });
      shSettingsEntry = entry;
      frame.src = dest.src;
      shSettingsDialog.appendChild(frame);
      shSettingsDialog.showModal();
    } else if (shSettingsEntry.href !== dest.href) {
      shSettingsEntry.href = dest.href;
      // A normal iframe navigation creates a joint session-history entry.
      shSettingsEntry.el.contentWindow.location.replace(dest.src);
    }
    railSettingsMark();
    return true;
  }
  function shCloseSettings() {
    if (!shSettingsEntry) return;
    var entry = shSettingsEntry;
    shSettingsEntry = null;
    shSettingsDialog.close();
    entry.el.remove();
    if (location.pathname === "/settings") history.replaceState(history.state, "", "/");
    railSettingsMark();
    shFrames.forEach(function (f) {
      try { f.el.contentWindow.dispatchEvent(new Event("ws-voice-capabilities-refresh")); } catch (x) {}
    });
    try { window.dispatchEvent(new Event("ws-voice-capabilities-refresh")); } catch (x) {}
    var focus = entry.focus;
    if (!focus || !focus.isConnected || !focus.getClientRects().length) focus = railBtn;
    if (focus) focus.focus({ preventScroll: true });
  }
  function shEnt(win) { return shFrames.filter(function (x) { return x.el.contentWindow === win; })[0] || null; }
  function shCacheable(ent) {
    if (!window.WsShell.cacheLimit || !ent || !ent.loaded || !ent.f.rid) return false;
    try { return !!(ent.el.contentWindow.wsCanCacheSession && ent.el.contentWindow.wsCanCacheSession()); } catch (e) { return false; }
  }
  var shShowGen = 0, shHandoff = null, SH_HIDE_ACK_MS = 80;
  function shClearHandoff() {
    if (!shHandoff) return;
    clearTimeout(shHandoff.timer);
    shHandoff = null;
  }
  function shFinishHandoff(token) {
    if (!shHandoff || shHandoff.token !== token) return;
    var h = shHandoff;
    shClearHandoff();
    if (h.ent !== shOn || shFrames.indexOf(h.ent) < 0) return;
    if (!h.ent.shownSent) {
      h.ent.shownSent = true;
      shSend(h.ent, "shown", { shown: true });
      shNudge(h.ent);
    }
  }
  // shNudge forces one synchronous layout of a frame that was just made visible
  // and its document, so a WKWebView that composited the iframe while it was
  // display:none paints it now rather than at the next unrelated invalidation
  // (the blank session that only devtools cleared). A read only: no style
  // write, transform or allocation persists.
  function shNudge(ent) {
    try {
      void ent.el.offsetHeight;
      var d = ent.el.contentDocument;
      if (d && d.documentElement) void d.documentElement.offsetHeight;
    } catch (e) { /* unloading or inaccessible */ }
  }
  function shDrop(ent, leaving) {
    var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("shell", "shDrop") : null;
    try {
      if (shHandoff) {
        if (shHandoff.ent === ent) shClearHandoff();
        else if (shHandoff.from === ent) shFinishHandoff(shHandoff.token);
      }
      clearTimeout(ent.headTimer); ent.headTimer = null;
      try {
        if (leaving && ent.el.contentWindow.wsFinishSessionLeave) ent.el.contentWindow.wsFinishSessionLeave();
        else if (ent.el.contentWindow.wsSaveSessionDraft) ent.el.contentWindow.wsSaveSessionDraft();
        if (ent.el.contentWindow.wsSaveSessionView) ent.el.contentWindow.wsSaveSessionView();
      } catch (e) { /* only safe or never-shown documents are discarded */ }
      shFrames.splice(shFrames.indexOf(ent), 1);
      if (shWant === ent) shWant = null;
      ent.el.remove();
    } finally {
      if (jTok) WsJank.end(jTok);
    }
  }
  function shPrune() {
    var limit = Math.max(1, window.WsShell.cacheLimit || 0);
    while (shFrames.length > limit) {
      var old = shFrames.filter(function (x) { return x !== shOn && x !== shWant; })[0];
      if (!old) break;
      shDrop(old);
    }
  }
  function shAdd(el, f) {
    var ent = { el: el, f: f, head: null, title: "", pincol: false, dim: 0, y: 0, loaded: false, ready: false, settingsSent: false, shownSent: false };
    el.addEventListener("load", function () {
      if (shFrames.indexOf(ent) < 0) return;
      ent.loaded = true;
      ent.ready = true;
      if (ent.lateHash !== undefined) {
        try { ent.el.contentWindow.location.hash = ent.lateHash; } catch (x) { /* unloading */ }
        delete ent.lateHash;
      }
      if (!ent.settingsSent) { ent.settingsSent = true; shSettings(ent); if (typeof ideRailNotifyFrames === "function") ideRailNotifyFrames(ent); }
      if (shWant === ent) shShow(ent);
      else if (!ent.shownSent && !(shHandoff && shHandoff.ent === ent)) {
        ent.shownSent = true;
        shSend(ent, "shown", { shown: ent === shOn });
        if (!shWant && ent === shOn) shCompanionTarget(ent, true);
      }
    });
    shFrames.push(ent);
    return ent;
  }
  // shOpen shows the page href names (a page the shell frames; any other
  // loads at the top level); push adds a history entry for it.
  function shOpen(href, push) {
    var u = new URL(href, location.href);
    if (shOpenSettings(u)) return;
    // history.go is asynchronous; a pick after Stay must wait until Back's
    // position is restored, rather than pushing from its destination entry.
    if (shRestoring !== null) { shRestoreNav = { href: href, push: push }; return; }
    var f = window.WsShell.framed(u, location.origin);
    if (!f) { location.href = u.href; return; }
    if (shLeaving) {
      // An in-progress decision belongs to its original destination. A
      // subsequent traversal or pick gets its own decision.
      if (f.path !== shLeaving.f.path || f.hash !== shLeaving.f.hash || push !== shLeaving.push ||
          shHistoryIndex() !== shLeaving.index) shLeaving.next = { href: href, push: push };
      else shLeaving.next = null;
      return;
    }
    // A destination that never became visible owns no reader edits. A rapid
    // pick supersedes it without parking an extra loading document.
    // A repick of that same pending destination keeps its loading document.
    var pend = shOn && shWant && shWant !== shOn && shWant.f.path === f.path && f.path !== shOn.f.path ? shWant : null;
    if (window.WsShell.cacheLimit && shOn && shWant && shWant !== shOn && !pend) shDrop(shWant);
    var ent = pend || shOn || shWant;
    if (ent && ent.f.path === f.path) {
      ent.el.inert = false;
      if (push && window.WsShell.pagePath(location) + location.hash !== f.path + f.hash) shPush(f);
      if (!push) shIndex = shHistoryIndex();
      if (f.hash !== ent.f.hash) {
        // A document still loading may drop a hash assignment; the ready/load
        // handler applies it once the page is there.
        if (ent.loaded || ent.ready) {
          try { ent.el.contentWindow.location.hash = f.hash; } catch (x) { /* unloading */ }
        } else ent.lateHash = f.hash;
      } else if (/^#entry=/.test(f.hash) && (ent.loaded || ent.ready)) {
        try { if (ent.el.contentWindow.wsEntryJumpFromHash) ent.el.contentWindow.wsEntryJumpFromHash(f.hash); } catch (x) { /* unloading */ }
      }
      ent.f = f;
      // A pending destination whose ready/load already fired never gets another
      // load event to show it; go through the normal show path now.
      if (ent === shWant && ent !== shOn && (ent.loaded || ent.ready)) shShow(ent);
      return;
    }
    if (!ent) { shStart(f, push); return; }
    if (shCacheable(ent)) {
      try { if (ent.el.contentWindow.wsSaveSessionView) ent.el.contentWindow.wsSaveSessionView(); } catch (e) {}
      // The outgoing page may remain painted during a cold load, but cannot
      // acquire a new unsaved edit after the cache-safety check.
      ent.el.inert = true;
      if (ent.el.blur) ent.el.blur();
      shStart(f, push, shIndex);
      return;
    }
    shLeave(ent, f, push);
  }
  async function shLeave(ent, f, push, from) {
    var leave = { ent: ent, f: f, push: push, from: from === undefined ? shIndex : from, index: shHistoryIndex() };
    shLeaving = leave;
    if (doc.activeElement === ent.el) {
      try { leave.focus = ent.el.contentDocument.activeElement; } catch (e) { /* unavailable */ }
    }
    // Freeze editing while a pending write settles or the user decides.
    ent.el.inert = true;
    var needsDecision = false;
    try {
      var source = ent.el.contentWindow;
      if (source.wsPrepareSessionLeave) needsDecision = await source.wsPrepareSessionLeave();
      else if (source.wsHoldsDraft) needsDecision = source.wsHoldsDraft();
    } catch (e) { needsDecision = true; }
    var accepted = !needsDecision;
    if (!leave.next && needsDecision) {
      accepted = await window.wsConfirm(wsT("js.draft.leave"), {
        accept: wsT("js.draft.leave.accept"), cancel: wsT("js.draft.leave.stay")
      });
    }
    if (shLeaving !== leave) return;
    shLeaving = null;
    if ((shOn || shWant) !== ent) return;
    if (leave.next) {
      // Preserve the history entry reached by a later Back/Forward.
      ent.el.inert = false;
      if (leave.next.push && shHistoryIndex() !== leave.from) {
        shRestoreNav = leave.next;
        shStay(leave);
      } else shOpen(leave.next.href, leave.next.push);
      return;
    }
    if (!accepted || shHistoryIndex() !== leave.index) { shStay(leave); return; }
    shDrop(ent, true);
    if (shOn === ent) { shOn = null; shCompanionTarget(null); }
    if (shWant && shWant.f.path !== f.path) shDrop(shWant);
    shStart(f, push);
  }
  function shStart(f, push, from) {
    if (push && window.WsShell.pagePath(location) + location.hash !== f.path + f.hash) shPush(f);
    if (!push) shIndex = shHistoryIndex();
    var cached = window.WsShell.cacheLimit && shFrames.filter(function (x) { return (x.loaded || x.ready) && x.f.rid && x.f.path === f.path; })[0];
    if (cached) {
      if (cached.f.hash !== f.hash) try { cached.el.contentWindow.location.hash = f.hash; } catch (e) {}
      else if (/^#entry=/.test(f.hash)) try { if (cached.el.contentWindow.wsEntryJumpFromHash) cached.el.contentWindow.wsEntryJumpFromHash(f.hash); } catch (e) {}
      cached.f = f;
      shWant = cached;
      shShow(cached, from);
      return;
    }
    var el = doc.createElement("iframe");
    shCompanionTarget(null);
    el.className = shOn ? "sframe" : "sframe on";
    el.title = doc.title;
    if (shWant) shDrop(shWant);
    shWant = shAdd(el, f);
    shWant.from = from;
    el.src = window.WsShell.src(f);
    $("frames").appendChild(el);
    if (shOn) return; // Keep the previous pixels until the target loads.
    shRid(f.rid);
    shHead({ head: null });
    shLayout({ pincol: false, dim: 0 });
    shScroll({ y: 0 });
  }
  // shRid marks the session the shell shows (rid, the rail's viewed row,
  // the memo pad's workspace).
  function shRid(id) {
    rid = id;
    root.setAttribute("data-rid", id);
    railMarks();
    doc.dispatchEvent(new CustomEvent("ws-shell-route", { detail: { rid: id, path: location.pathname } }));
  }
  function shShow(ent, from) {
    // ready/head can precede iframe.load (which waits for nested about:srcdoc
    // sub-iframes). Reveal once the frame's script and chrome are ready,
    // without stranding navigation on an error/login page or broken script.
    if (!ent.loaded && !ent.ready) return;
    if (!ent.head && !ent.headWaitDone) {
      if (!ent.headTimer) ent.headTimer = setTimeout(function () {
        ent.headTimer = null;
        ent.headWaitDone = true;
        if (shWant === ent) shShow(ent, from);
      }, 400);
      return;
    }
    clearTimeout(ent.headTimer); ent.headTimer = null;
    var was = shOn;
    if (was === ent || shLeaving && shLeaving.ent === was) return;
    var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("shell", "shShow") : null;
    try {
    // Async tool/draft callbacks can change eligibility during a cold load.
    // Keep the ready destination, but settle the draft decision before
    // hiding or destroying an outgoing document that is no longer safe.
    if (was && !shCacheable(was)) {
      ent.shownSent = true;
      shSend(ent, "shown", { shown: false });
      if (!shLeaving) shLeave(was, ent.f, false, from === undefined ? ent.from : from);
      return;
    }
    shWant = null;
    if (ent.lateHash !== undefined) {
      try { ent.el.contentWindow.location.hash = ent.lateHash; } catch (x) { /* unloading */ }
      delete ent.lateHash;
    }
    // A rename in progress names the session it was started for.
    var sn = region("head") && region("head").querySelector(".sname");
    if (sn && labelEditing()) labelEnd(sn);
    headHeld = null;
    shOn = ent;
    shFrames.splice(shFrames.indexOf(ent), 1); shFrames.push(ent);
    hdNavigated();
    if (ent.cached || !ent.settingsSent) {
      ent.settingsSent = true;
      shSettings(ent);
      if (typeof ideRailNotifyFrames === "function") ideRailNotifyFrames(ent);
      if (ent.cached) shSend(ent, "theme", { theme: root.getAttribute("data-theme") || "" });
      ent.cached = false;
    }
    var gen = ++shShowGen;
    shClearHandoff();
    ent.el.inert = false;
    ent.el.classList.add("on");
    var wantAck = false;
    if (was) {
      var hasSyncHide = false;
      try {
        if (was.el && was.el.contentWindow && typeof was.el.contentWindow.wsHideFrame === "function") {
          was.el.contentWindow.wsHideFrame();
          hasSyncHide = true;
        }
      } catch (e) { /* unloading or inaccessible */ }
      wantAck = !!(was.hideAck || hasSyncHide);
      was.cached = true;
      was.el.classList.remove("on");
      was.shownSent = true;
      shSend(was, "shown", wantAck ? { shown: false, token: gen } : { shown: false });
    }
    shPrune();
    var wasAlive = !!(was && shFrames.indexOf(was) >= 0);
    if (was && wantAck && wasAlive) {
      // Sequence WS handoff: let the outgoing frame acknowledge hide (having
      // closed its WS and suspended doc frames) before activating the incoming
      // frame, with a bounded fallback timer so switches never stall.
      ent.shownSent = false;
      shHandoff = {
        token: gen, ent: ent, from: was,
        timer: setTimeout(function () { shFinishHandoff(gen); }, SH_HIDE_ACK_MS)
      };
    } else {
      ent.shownSent = true;
      shSend(ent, "shown", { shown: true });
      shNudge(ent);
    }
    shCompanionTarget(ent, false, !!was && !!ent.f.rid && was.f.rid !== ent.f.rid);
    shRid(ent.f.rid);
    shHead(ent);
    shLayout(ent);
    shScroll(ent);
    if (ent.title) doc.title = ent.title;
    if (!shSettingsEntry && !(typeof rp !== "undefined" && rp.P)) try { ent.el.contentWindow.focus(); } catch (x) { /* unloading */ }
    } finally {
      if (jTok) WsJank.end(jTok);
    }
  }
  // Private UI state only: no daemon protocol or persistence contract changes.
  function shSettings(ent) { shSend(ent, "settings", { layout: layoutMode, input: inputMode, stageFill: typeof stageFill !== "undefined" && !!stageFill, extraMotion: typeof extraMotion === "undefined" ? true : !!extraMotion }); }
  function shReload() {
    if (!shOn) return;
    shOn.loaded = false;
    shOn.ready = false;
    shOn.settingsSent = false;
    shOn.shownSent = false;
    try { shOn.el.contentWindow.location.reload(); } catch (x) { /* unloading */ }
  }
  // shHead shows the shown frame's header: its head region (none while the
  // frame has not named one) and search button, or with the head null the
  // frame's own header (sh-own) beside the shell's rail button.
  function shHead(ent) {
    var m = ent.head, html = m ? m.html : "", qs = $("qsslot");
    shTop.classList.toggle("sh-own", html === null);
    if (html === null) html = "";
    if (html !== shHeadHtml) {
      shHeadHtml = html;
      shSeq++;
      swap("head", "f" + shSeq, html);
    }
    if (qs && !(qs.hasAttribute && qs.hasAttribute("data-shell-qs")) && qs.innerHTML !== (m ? m.qs : "")) qs.innerHTML = m ? m.qs : "";
    var rightW = m && typeof m.pad === "number" && isFinite(m.pad) && m.pad > 0 ? Math.max(0, Math.ceil(m.pad - 8)) : 0;
    shTop.style.setProperty("--sh-right-w", rightW ? rightW + "px" : "");
    shTop.style.paddingRight = $("slot-top-right") ? "" : (m ? m.pad + "px" : "");
    var side = m && typeof m.side === "number" && isFinite(m.side) && m.side > 0 ? m.side : 0;
    shTop.style.setProperty("--sh-stage-side", side ? side + "px" : "");
    // Headerless fallback clears stale session controls, not their box.
    if (m) shTop.style.minHeight = m.top + "px";
  }
  // shLayout takes the shown frame's pinned column and its backdrop's dim.
  function shLayout(ent) {
    if (ent.pincol !== root.hasAttribute("data-pincol")) {
      if (ent.pincol) root.setAttribute("data-pincol", ""); else root.removeAttribute("data-pincol");
      railOpen(false);
    }
    if (ent.popover) root.setAttribute("data-fpopover", ""); else root.removeAttribute("data-fpopover");
    if (ent.dim > 0) {
      root.style.setProperty("--fdim", String(Math.max(0, 1 - ent.dim)));
      root.setAttribute("data-fdim", "");
    } else root.removeAttribute("data-fdim");
  }
  // shScroll moves the header and the rail with a frame whose page scrolls
  // as a whole, as its own would move: the header scrolls away, and the
  // rail rises with it until it stands at the wrap's top padding.
  function shScroll(ent) {
    var y = Math.max(0, ent.y || 0);
    shTop.style.transform = y ? "translateY(" + -y + "px)" : "";
    if (y > 2) root.setAttribute("data-fscrolled", "");
    else root.removeAttribute("data-fscrolled");
    // The rail no longer sits under the header: docked it runs the full
    // height beside it (the framed page's placeholder stays sticky at 12px),
    // and overlaid it is fixed, so it never moves with the page.
    if (shBody) shBody.style.marginTop = "";
  }
  function shSearch() {
    if (!shOn || shSettingsEntry) return;
    try { shOn.el.focus(); } catch (x) { /* unloading */ }
    shSend(shOn, "search");
  }
  function overviewButton(d) {
    var b = d && d.querySelector(".board-overview");
    return b && b.getClientRects().length ? b : null;
  }
  // The Git page is available while its gallery host (#bgal's #gitdock)
  // exists: a session whose root is no repository still shows its empty page.
  function gitPageEl(d) {
    return d ? (d.getElementById ? d.getElementById("gitdock") : d.querySelector && d.querySelector("#gitdock")) || null : null;
  }
  // The ticket map is available while it is expanded or its docked page
  // (#bgal's #tmdock) exists.
  function ticketMapAvailable(d) {
    if (!d) return false;
    var back = d.getElementById ? d.getElementById("tmback") : d.querySelector && d.querySelector("#tmback");
    if (back && !back.hidden) return true;
    return !!(d.getElementById ? d.getElementById("tmdock") : d.querySelector && d.querySelector("#tmdock"));
  }
  function filesSheetOpenEl(d) {
    var s = d && (d.getElementById ? d.getElementById("toolbar-files") : d.querySelector && d.querySelector("#toolbar-files"));
    return !!(s && !s.hidden);
  }
  function filesButton(d) {
    var b = d && d.querySelector && d.querySelector(".board-files");
    if (!b || b.disabled) return null;
    if (filesSheetOpenEl(d)) return b;
    return b.getClientRects && b.getClientRects().length ? b : null;
  }
  function globalShortcutAvailable(d, kind) {
    if (kind === "overview") return !!overviewButton(d);
    if (kind === "git") return !!gitPageEl(d);
    if (kind === "ticketmap") return ticketMapAvailable(d);
    if (kind === "files") return !!filesButton(d);
    return false;
  }
  function runGlobalShortcut(kind) {
    if (kind === "overview") {
      var b = overviewButton(doc);
      if (!b) return false;
      if (typeof ticketMap !== "undefined" && ticketMap && ticketMap.isOpen && ticketMap.isOpen()) ticketMap.close(true);
      if (typeof drawer !== "undefined" && drawer && typeof drOpen !== "undefined" && drOpen && typeof drSetOpen === "function") drSetOpen(false);
      if (typeof window !== "undefined" && window.WsCards && window.WsCards.dismissFiles) window.WsCards.dismissFiles();
      b.click();
      return true;
    }
    // Ctrl+G shows and focuses the Git page; a repeat only refocuses it (no
    // toggle). The expanded ticket map, the drawer, and the files sheet
    // close first.
    if (kind === "git") {
      if (!gitPageEl(doc) || typeof gitPage === "undefined" || !gitPage || typeof gitPage.open !== "function") return false;
      if (typeof ticketMap !== "undefined" && ticketMap && ticketMap.isOpen && ticketMap.isOpen()) ticketMap.close(true);
      if (typeof drawer !== "undefined" && drawer && typeof drOpen !== "undefined" && drOpen && typeof drSetOpen === "function") drSetOpen(false);
      if (typeof window !== "undefined" && window.WsCards && window.WsCards.dismissFiles) window.WsCards.dismissFiles();
      if (typeof window !== "undefined" && window.WsOverview && window.WsOverview.holding && window.WsOverview.holding()) {
        window.WsOverview.after(function () { gitPage.open(); });
        return true;
      }
      gitPage.open();
      return true;
    }
    // Ctrl+B shows and focuses the docked map (a repeat only refocuses it);
    // while the map is expanded it returns it to the dock.
    if (kind === "ticketmap") {
      if (!ticketMapAvailable(doc) || typeof ticketMap === "undefined" || !ticketMap || typeof ticketMap.open !== "function") return false;
      if (ticketMap.isOpen && ticketMap.isOpen()) {
        ticketMap.close(false);
        return true;
      }
      if (typeof drawer !== "undefined" && drawer && typeof drOpen !== "undefined" && drOpen && typeof drSetOpen === "function") drSetOpen(false);
      if (typeof window !== "undefined" && window.WsCards && window.WsCards.dismissFiles) window.WsCards.dismissFiles();
      if (typeof window !== "undefined" && window.WsOverview && window.WsOverview.holding && window.WsOverview.holding()) {
        window.WsOverview.after(function () { ticketMap.open(); });
        return true;
      }
      ticketMap.open();
      return true;
    }
    if (kind === "files") {
      var fb = filesButton(doc);
      if (!fb) return false;
      if (filesSheetOpenEl(doc)) {
        if (typeof window !== "undefined" && window.WsCards && window.WsCards.dismissFiles) window.WsCards.dismissFiles();
        else fb.click();
        return true;
      }
      if (typeof ticketMap !== "undefined" && ticketMap && ticketMap.isOpen && ticketMap.isOpen()) ticketMap.close(true);
      if (typeof drawer !== "undefined" && drawer && typeof drOpen !== "undefined" && drOpen && typeof drSetOpen === "function") drSetOpen(false);
      fb.click();
      return true;
    }
    return false;
  }
  // Keyboard events do not cross the session iframe. The rail forwards to
  // the shown frame; both paths activate the same visible control or toggle.
  doc.addEventListener("keydown", function (e) {
    if (shSettingsEntry || e.defaultPrevented || e.isComposing || e.keyCode === 229 || !e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
    var k = (e.key || "").toLowerCase();
    var kind = k === "q" ? "overview" : k === "g" ? "git" : k === "b" ? "ticketmap" : k === "e" ? "files" : "";
    if (!kind) return;
    var t = e.target;
    var editable = !!(t && (t.isContentEditable || (t.closest && t.closest("input, textarea, select, [contenteditable]:not([contenteditable=\"false\"]), .xterm, .tm-host"))));
    if (editable) {
      if (!(kind === "files" && !shell && !shHidden && filesSheetOpenEl(doc) && t.closest && t.closest("#toolbar-files") && !t.closest(".xterm, .tm-host"))) return;
    }
    if (shell) {
      var fd;
      try { fd = shOn && shOn.el.contentDocument; } catch (x) { return; }
      if (!globalShortcutAvailable(fd, kind)) return;
      shSend(shOn, kind);
    } else {
      if (shHidden) return;
      if (!runGlobalShortcut(kind)) return;
    }
    e.preventDefault();
  });
  if (shell && window.WsShell) {
    if (shSettingsDialog) {
      shSettingsDialog.addEventListener("cancel", function (e) {
        e.preventDefault();
        shCloseSettings();
      });
      shSettingsDialog.addEventListener("click", function (e) {
        if (e.target === shSettingsDialog) shCloseSettings();
      });
    }
    window.addEventListener("message", function (e) {
      if (e.origin !== location.origin) return;
      var m = window.WsShell.message(e.data);
      if (!m) return;
      if (shSettingsEntry && e.source === shSettingsEntry.el.contentWindow) {
        if (m.wsShell === "settings-close") shCloseSettings();
        else if (m.wsShell === "settings-navigate") {
          var dest;
          try { dest = new URL(m.href, location.origin); } catch (x) { return; }
          shOpenSettings(dest);
        } else if (m.wsShell === "companion-packs-changed") {
          if (shCompanion && shCompanion.packsChanged) shCompanion.packsChanged();
        } else if (m.wsShell === "companion-stage-action") {
          if (m.action === "reset-size") { if (shCompanion && shCompanion.resetSize) shCompanion.resetSize(); }
          else if (m.action === "return-home") { if (shCompanion && shCompanion.returnHome) shCompanion.returnHome(); }
        } else if (m.wsShell === "jank-stall" && typeof WsJank !== "undefined" && WsJank) {
          WsJank.ingest(m.stall);
        } else if (m.wsShell === "jank-minor" && typeof WsJank !== "undefined" && WsJank) {
          WsJank.ingestMinor(m);
        } else if (m.wsShell === "daemon-bridge-res" && typeof handleDaemonBridgeRes === "function") {
          handleDaemonBridgeRes(e.source, m);
        }
        return;
      }
      var ent = shEnt(e.source), on = !!ent && ent === shOn;
      if (!ent) return;
      switch (m.wsShell) {
        case "ready":
          ent.ready = true;
          ent.settingsSent = false;
          ent.shownSent = false;
          if (m.hideAck) ent.hideAck = true;
          if (ent.lateHash !== undefined) {
            try { ent.el.contentWindow.location.hash = ent.lateHash; } catch (x) { /* unloading */ }
            delete ent.lateHash;
          }
          if (!ent.settingsSent) { ent.settingsSent = true; shSettings(ent); if (typeof ideRailNotifyFrames === "function") ideRailNotifyFrames(ent); }
          if (on && !ent.shownSent && !(shHandoff && shHandoff.ent === ent)) {
            ent.shownSent = true;
            shSend(ent, "shown", { shown: true });
            if (!shWant) shCompanionTarget(ent, true);
          } else if (ent === shWant) shShow(ent);
          break;
        case "hidden-ack":
          if (shHandoff && shHandoff.from === ent && m.token === shHandoff.token) shFinishHandoff(m.token);
          break;
        case "head":
          ent.head = m;
          if (on) shHead(ent);
          else if (ent === shWant) shShow(ent);
          break;
        case "title": ent.title = m.title; if (on) doc.title = m.title; break;
        case "nav": if (on) shOpen(m.href, true); break;
        case "key":
          if (!on) break;
          if (m.key === "rail") railKey();
          else if (m.key === "esc" && railShown()) railOpen(false);
          break;
        case "press":
          if (on) {
            railPopClose(false);
            shCompanion?.closeMenu();
            doc.dispatchEvent(new CustomEvent("ws-shell-focus"));
          }
          break;
        case "companion-context":
          if (on && ent === shCompanionFrame && e.source === shOn.el.contentWindow) {
            if (m.sessionId !== ent.f.rid || (m.routeKind === "session") !== !!ent.f.rid) break;
            shCompanion.context(m, shOn.el.getBoundingClientRect());
          }
          break;
        case "layout": ent.pincol = m.pincol; ent.dim = m.dim; ent.popover = m.popover === true; if (on) shLayout(ent); break;
        case "scroll": ent.y = m.y; if (on) shScroll(ent); break;
        case "hash": if (on && location.pathname !== "/settings" && !shLeaving && shRestoring === null && location.hash !== m.hash) history.replaceState(history.state, "", ent.f.path + m.hash); ent.f.hash = m.hash; break;
        case "theme":
          setTheme(m.theme, true);
          shBroadcastTheme(root.getAttribute("data-theme") || "", ent);
          break;
        case "settings":
          var broadcastSettings = false;
          if (typeof m.stageFill === "boolean" && typeof setStageFill === "function" && m.stageFill !== (typeof stageFill !== "undefined" && !!stageFill)) {
            setStageFill(m.stageFill);
            broadcastSettings = true;
          }
          if (typeof m.extraMotion === "boolean" && typeof setExtraMotion === "function" && m.extraMotion !== (typeof extraMotion === "undefined" ? true : !!extraMotion)) {
            setExtraMotion(m.extraMotion);
            broadcastSettings = true;
          }
          if (broadcastSettings) shFrames.forEach(function (f) { if (f !== ent) shSettings(f); });
          break;
        case "jank-stall":
          if (typeof WsJank !== "undefined" && WsJank) WsJank.ingest(m.stall);
          break;
        case "jank-minor":
          if (typeof WsJank !== "undefined" && WsJank) WsJank.ingestMinor(m);
          break;
        case "daemon-bridge-res":
          if (typeof handleDaemonBridgeRes === "function") handleDaemonBridgeRes(e.source, m);
          break;
        case "term-list":
          if (on) hdFrameTerms(ent, m);
          break;
      }
    });
    window.addEventListener("popstate", function () {
      if (shRestoring !== null) {
        var delta = shRestoring - shHistoryIndex();
        if (delta) history.go(delta);
        else shRestoreDone();
        return;
      }
      if (shOpenSettings(new URL(location.href))) return;
      shCloseSettings();
      shOpen(location.pathname + location.hash, false);
    });
    // The shell's hotkeys besides the rail's: the search palette's keys
    // (tracker.js qShortcut) open the shown frame's palette; Ctrl+K,
    // Ctrl+Space and Cmd+K stay with a terminal by qShortcut's target rule.
    doc.addEventListener("keydown", function (e) {
      if (e.isComposing || e.keyCode === 229 || e.altKey || e.defaultPrevented) return;
      var t = e.target, typing = !!(t && t.closest && t.closest("input, textarea, select, [contenteditable]:not([contenteditable=\"false\"])"));
      var term = !!(t && t.closest && t.closest(".xterm")), k = (e.key || "").toLowerCase() === "k";
      if (e.ctrlKey || e.metaKey ? !term && !e.shiftKey && (e.metaKey ? !e.ctrlKey && k : k || e.code === "Space" || e.key === " ") : e.key === "/" && !typing) {
        e.preventDefault();
        shSearch();
      }
    });
    if (shTop) shTop.addEventListener("click", function (e) {
      var t = e.target;
      if (!t || !t.closest) return;
      if (t.closest("#tkqs")) { e.preventDefault(); shSearch(); return; }
      // The mirrored permission chip opens the same session menu.
      if (t.closest(".host.hbtn, .pmhead[role=button]") && shOn) { e.preventDefault(); try { shOn.el.focus(); } catch (x) {} shSend(shOn, "menu"); }
    });
    if (shTop) shTop.addEventListener("keydown", function (e) {
      var t = e.target;
      if ((e.key !== "Enter" && e.key !== " ") || !t || !t.classList || !(t.classList.contains("hbtn") || (t.classList.contains("pmhead") && t.getAttribute("role") === "button")) || !shOn) return;
      e.preventDefault();
      try { shOn.el.focus(); } catch (x) {}
      shSend(shOn, "menu");
    });
  }
  // shFrameInit wires a framed page to its shell: the header, the layout,
  // the scroll, and hash routes go up as they change, and the shell's
  // messages come down.
  function shFrameInit() {
    var sent = {}, queued = null, head = null, headDirty = true, lastHandlesKey = "";
    function send(kind, m) {
      if (compact) return;
      var k = JSON.stringify(m);
      if (sent[kind] === k) return;
      sent[kind] = k;
      shPost(kind, m);
    }
    // inline gives the search slot's icons their own paths: the shell
    // holds none of the tracker's symbols.
    function inline(el) {
      var c = el.cloneNode(true);
      q("svg use", c).forEach(function (u) {
        var id = (u.getAttribute("href") || "").replace(/^#/, ""), sym = id ? doc.getElementById(id) : null, svg = u.parentNode;
        if (!sym || !svg) return;
        if (sym.getAttribute("viewBox")) svg.setAttribute("viewBox", sym.getAttribute("viewBox"));
        svg.innerHTML = sym.innerHTML;
      });
      return c.innerHTML;
    }
    // dim is the alpha of the backdrop the page shows over the whole
    // window: a modal dialog's, or an overlay's (the item and git windows,
    // the image viewer, a maximized card's scrim, the ticket map).
    function dim() {
      var a = window.WsOverview && window.WsOverview.active() ? 0.72 : 0;
      q("dialog[open]").forEach(function (d) {
        var modal = false;
        try { modal = d.matches(":modal"); } catch (x) { modal = false; }
        if (modal) a = Math.max(a, window.WsShell.alpha(getComputedStyle(d, "::backdrop").backgroundColor));
      });
      q(".tk-iwin-back:not([hidden]), .gwin-back:not([hidden]), .wbv:not([hidden]), .bc-scrim:not([hidden]), .d-tm-back:not([hidden]), .au-scrim:not([hidden])").forEach(function (el) {
        if (el.getClientRects().length) a = Math.max(a, window.WsShell.alpha(getComputedStyle(el).backgroundColor));
      });
      return Math.round(a * 100) / 100;
    }
    function post() {
      queued = null;
      if (shHidden) return;
      // Board/root mutations only need the open-overlay state. Re-measure
      // the mirrored header when it changes, resizes, or right-end handle
      // widths (--drhw/--tkhw) change.
      var handlesKey = (root.style && typeof root.style.getPropertyValue === "function")
        ? ((root.style.getPropertyValue("--drhw") || "") + "|" + (root.style.getPropertyValue("--tkhw") || ""))
        : "";
      if (handlesKey !== lastHandlesKey) {
        lastHandlesKey = handlesKey;
        headDirty = true;
      }
      if (headDirty) {
        headDirty = false;
        var top = doc.querySelector(".dwrap > .top"), h = region("head"), qs = $("qsslot");
        var topCS = top ? getComputedStyle(top) : null;
        var hasGP = !!(topCS && typeof topCS.getPropertyValue === "function");
        var drhw = hasGP ? (parseFloat(topCS.getPropertyValue("--drhw")) || 0) : 0;
        var tkhw = hasGP ? (parseFloat(topCS.getPropertyValue("--tkhw")) || 0) : 0;
        var dpad = hasGP ? (parseFloat(topCS.getPropertyValue("--dpad")) || 16) : 16;
        var handlesW = (drhw > 0 || tkhw > 0) ? Math.ceil(drhw + tkhw + dpad) : 0;
        head = top ? {
          html: h ? h.innerHTML : null, qs: qs ? inline(qs) : "",
          pad: Math.max((parseFloat(topCS.paddingRight) || 0), handlesW) + Math.max(0, window.innerWidth - root.clientWidth),
          side: Math.round((parseFloat(topCS.marginRight) || 0) * 100) / 100,
          top: top.offsetHeight
        } : null;
      }
      // A link menu or open tracker drawer's local z-index cannot outrank the
      // shell header or rail either; lift its frame through the same layer as
      // the other popovers. The read-only hover card (rail.js, .shp) is a
      // tooltip and never lifts.
      // While lifted for a popover the page drops its own background
      // (data-lifted, dashboard-shell.css), so the shell's header and rail
      // stay visible under the raised frame instead of going blank.
      var popover = q(".rpop:not([hidden]), .lmenu, #tkdrawer[open]").some(function (el) { return !(el.classList && el.classList.contains("shp")) && el.getClientRects().length > 0; });
      var layout = { pincol: root.hasAttribute("data-pincol"), dim: dim(), popover: popover };
      if (root.hasAttribute("data-lifted") !== popover) {
        if (popover) root.setAttribute("data-lifted", ""); else root.removeAttribute("data-lifted");
      }
      if (head) send("head", head);
      send("layout", layout);
    }
    function queue() { if (!compact && !shHidden && !queued) queued = requestAnimationFrame(post); }
    function queueHead() { if (compact) return; headDirty = true; queue(); }
    if (!compact && typeof ideFrameQueueHead !== "undefined") ideFrameQueueHead = queueHead;
    if (!compact) {
      doc.addEventListener("ws-overview", queue);
      doc.addEventListener("ws-audit", queue);
      doc.addEventListener("ws-stage-fill", queueHead);
      var top = doc.querySelector(".dwrap > .top");
      if (window.ResizeObserver && top) new ResizeObserver(queueHead).observe(top);
      window.addEventListener("resize", queueHead);
      if (window.MutationObserver) {
        var mo = new MutationObserver(queue);
        if (top) new MutationObserver(queueHead).observe(top, { subtree: true, childList: true, attributes: true, characterData: true });
        mo.observe(root, { attributes: true, attributeFilter: ["style", "data-pincol"] });
        mo.observe(doc.body, { subtree: true, attributes: true, attributeFilter: ["open", "hidden"] });
        // A second observe() of the same node on one observer replaces its
        // options, so the body's direct children (a popover appended already
        // shown) need their own observer.
        new MutationObserver(queue).observe(doc.body, { childList: true });
      }
      window.addEventListener("resize", queueHead);
      window.addEventListener("scroll", function () { send("scroll", { y: Math.round(window.scrollY) }); });
      // A hash route moves by hashchange or by the page's own history calls
      // (tracker.js's store pages), which fire none: either way the shell's
      // address takes the hash.
      function hash() { send("hash", { hash: location.hash }); }
      window.addEventListener("hashchange", hash);
      window.addEventListener("popstate", hash);
      ["pushState", "replaceState"].forEach(function (k) {
        var orig = history[k];
        if (typeof orig !== "function") return;
        history[k] = function () {
          var r = orig.apply(history, arguments);
          hash();
          return r;
        };
      });
      // Escape and the rail hotkey reach the shell from inside the page (the
      // rail's own handler is the shell's).
      doc.addEventListener("keydown", function (e) {
        if (e.code === "Backquote" && e.ctrlKey && !e.altKey && !e.metaKey && !e.shiftKey && !e.isComposing) { e.preventDefault(); railKey(); }
        else if (e.key === "Escape" && !e.defaultPrevented) shPost("key", { key: "esc" });
      });
      // A press in the page reaches the shell, whose rail popovers and row
      // menu close on one (capture: the page's own handlers may stop it).
      doc.addEventListener("pointerdown", function () { shPost("press"); }, true);
    }
    // Same-origin synchronous hide hook invoked by the shell before sending
    // shown:true to an incoming frame so outgoing WS closes first.
    function hideFrameNow() {
      shHidden = true;
      if (queued) { cancelAnimationFrame(queued); queued = null; }
      syncPageVisibility();
      // Opaque author scripts have no safe general pause API. Retain
      // their placeholder heights, not their running child documents.
      suspendDocFrames();
      return true;
    }
    window.wsHideFrame = hideFrameNow;
    window.addEventListener("message", function (e) {
      if (e.source !== window.parent || e.origin !== location.origin) return;
      var m = window.WsShell.message(e.data);
      if (!m) return;
      if (m.wsShell === "shown") {
        if (!m.shown) {
          hideFrameNow();
          if (typeof m.token === "number") shPost("hidden-ack", { token: m.token });
          return;
        }
        shHidden = false;
        syncPageVisibility();
        shUnstage();
        showRegions();
        mountFramesPaced(doc);
        // The shown frame, a cached one returning with stale scroll/positions, and
        // the board swap showRegions just applied all lay out only after this turn.
        if (typeof boardRevealSchedule === "function") boardRevealSchedule();
        tickTimes();
        if (typeof entryJumpRunPending === "function") entryJumpRunPending();
        if (shTrackerRev !== null) { var rev = shTrackerRev; shTrackerRev = null; trackerNudge(rev); }
        queue();
      } else if (m.wsShell === "companion-gate") {
        if (compact) return;
        if (m.active && !companionRelay) companionRelay = startCompanionRelay();
        else if (!m.active && companionRelay) { companionRelay.dispose(); companionRelay = null; }
      } else if (m.wsShell === "search") {
        // Open, do not toggle a hidden header button via a synthetic click.
        tkSearch();
      } else if (m.wsShell === "overview" || m.wsShell === "git" || m.wsShell === "ticketmap" || m.wsShell === "files") {
        if (!shHidden) runGlobalShortcut(m.wsShell);
      } else if (m.wsShell === "menu") {
        var h = region("head"), chip = h && h.querySelector(".host.hbtn");
        if (chip && WsRail) railPopOpen("sess", chip);
      } else if (m.wsShell === "settings") { setLayout(m.layout); setInput(m.input); if (typeof m.stageFill === "boolean" && typeof setStageFill === "function") setStageFill(m.stageFill); if (typeof m.extraMotion === "boolean" && typeof setExtraMotion === "function") setExtraMotion(m.extraMotion); }
      else if (m.wsShell === "rail-layout") { if (typeof ideRailApplyFromShell === "function") ideRailApplyFromShell(m.width, m.collapsed); }
      else if (m.wsShell === "theme") setTheme(m.theme, true);
      else if (m.wsShell === "rail-open") { if (typeof tkApi !== "undefined" && tkApi && tkApi.closeStore) tkApi.closeStore(); }
      else if (m.wsShell === "sync") { sent = {}; queueHead(); shPost("title", { title: doc.title }); }
      else if (m.wsShell === "daemon-bridge-req") { if (typeof handleDaemonFrameBridgeReq === "function") handleDaemonFrameBridgeReq(m); }
      else if (m.wsShell === "term-list-req" || m.wsShell === "term-attach") hdFrameTermReq(m);
    });
    if (!compact) {
      queue();
      if (typeof WsJank !== "undefined" && WsJank) {
        WsJank.setRelay(function (stall) { shPost("jank-stall", { stall: stall }); });
        WsJank.setMinorRelay(function (minor) { shPost("jank-minor", minor); });
      }
      shPost("ready", { hideAck: true });
      shPost("title", { title: doc.title });
      if (window.scrollY) send("scroll", { y: Math.round(window.scrollY) });
    }
  }
  // shShellInit takes the frame the shell page loaded with: the page its
  // address names (its path and hash, which never reached the daemon; a
  // query, such as a LAN token, is not the page's) and asks it for its
  // header, in case it posted before this script listened. A frame already
  // loaded fires no load for shAdd to see.
  function shShellInit() {
    var address = new URL(location.href);
    var settings = address.pathname === "/settings";
    var el = doc.querySelector("#frames > iframe");
    var f = window.WsShell.framed(new URL(settings ? "/" : address.pathname + address.hash, location.origin), location.origin);
    if (!el || !f) return;
    shIndex = shHistoryIndex();
    history.replaceState(Object.assign({}, history.state, { wsShellIndex: shIndex }), "", location.href);
    var ent = shAdd(el, f);
    shOn = ent;
    // A hash lands as a fragment-only src change, which loads nothing again
    // when the document already finished: the readiness below still stands.
    if (f.hash) el.src = window.WsShell.src(f);
    try {
      var cd = el.contentDocument;
      var blank = (cd && cd.URL === "about:blank") || (el.contentWindow && el.contentWindow.location && el.contentWindow.location.href === "about:blank");
      ent.loaded = !!(cd && cd.readyState === "complete" && !blank);
    } catch (x) { /* not yet */ }
    if (ent.loaded) ent.ready = true;
    shSend(ent, "sync");
    if (ent.loaded) {
      ent.settingsSent = true;
      ent.shownSent = true;
      shSettings(ent);
      if (typeof ideRailNotifyFrames === "function") ideRailNotifyFrames(ent);
      // No load event will tell this frame it is the shown one.
      shSend(ent, "shown", { shown: true });
    }
    if (settings) shOpenSettings(address);
  }

  bootingPanes = true;
  if (live || gmenu) {
    setLayout();
    setInput(load(INPUT_KEY));
    setExtraMotion(load(EXTRA_MOTION_KEY));
  }
  if (live) {
    setTab("convo");
    setCtab("convo");
  }
  bootingPanes = false;
  if (live || worktree) revealPanes();
  restoreOpen(doc);
  // A body frame that posted its height before the listener above existed
  // posts it again.
  q("iframe.doc-frame").forEach(function (f) { postDoc(f, { wbDoc: "size" }); });
  if (window.wbForm) window.wbForm();
  bindCapsuleNotes();
  // The served board's frames mount once the cards have moved the pinned
  // topics into the pin list, so no frame loads and then moves, paced so the
  // whole board's mounting never lengthens one long task, and once the
  // board's script (wbForm) has set up wbHistory, whose visibility source
  // the viewport bands watch through.
  boardBoxes().forEach(bandBoard);
  mountFramesPaced(doc);
  if (cardBoard) mountBoardOverview();
  boardRestoreReload();
  applySent();
  if (typeof initWbUpdateCards === "function") initWbUpdateCards();
  startDraftRecovery();
  tlCount();
  apScan(region(mode === "index" ? "sessions" : "rail"), mode === "index" ? "sessions" : "rail");
  if (hostedPage) { lead.key = leadKey(); leadFetch(); }
  else if (handedPage) leadFetch();
  syncBadge();
  fmtTimes(doc);
  mermaid();
  pinConvo();
  restoreSessionView();
  if (typeof entryJumpFromHash === "function") entryJumpFromHash();
  nearTop();
  if (framed) shFrameInit();
  if (shell && window.WsShell) {
    shShellInit();
    doc.addEventListener("ws-companion-config", function (event) { shCompanionConfigure(event.detail); });
    doc.addEventListener("ws-companion-command", function (event) { if (shCompanion) shCompanion.command(event.detail); });
    doc.addEventListener("ws-companion-phone", function (event) {
      if (shCompanion && typeof shCompanion.phoneState === "function") shCompanion.phoneState(event.detail);
      if (window.WsCompanionMessenger && typeof window.WsCompanionMessenger.applySnapshot === "function") window.WsCompanionMessenger.applySnapshot(event.detail);
    });
    doc.addEventListener("ws-companion-brain-state", function (event) {
      var detail = event.detail;
      if (!detail || typeof detail !== "object") return;
      window.WsCompanion?.reflex?.onBrainState?.(detail);
      if (detail.kind === "handoff") window.WsCompanionMessenger?.onBrainChanged?.(detail);
    });
    shCompanionConfigure(window.wsCompanionConfig);
    if (window.wsCompanionPhoneState) {
      if (shCompanion && typeof shCompanion.phoneState === "function") shCompanion.phoneState(window.wsCompanionPhoneState);
      if (window.WsCompanionMessenger && typeof window.WsCompanionMessenger.applySnapshot === "function") window.WsCompanionMessenger.applySnapshot(window.wsCompanionPhoneState);
    }
    doc.addEventListener("ws-companion-focus", function (event) { window.WsCompanion?.focus?.token(event.detail); });
    doc.addEventListener("ws-companion-config", function (event) { window.WsCompanion?.focus?.config(event.detail); });
    window.WsCompanion?.focus?.config(window.wsCompanionConfig);
    window.WsCompanion?.focus?.token(window.wsCompanionFocusToken);
    window.addEventListener("pagehide", function (event) { if (!event.persisted) shCompanionStop(); });
  }
  if (window === window.top) {
    window.wsNativeCloseReady = async function () {
      if (!shell) return persistForWindowClose();
      var frames = shFrames.slice();
      var results = await Promise.all(frames.map(function (entry) {
        var page = entry.el.contentWindow;
        return page && typeof page.wsPersistDraftForWindowClose === "function" &&
          page.wsPersistDraftForWindowClose();
      }));
      // Settings repo proposals live in this window's memory
      // (dashboard-settings-extras.js) and outlive a dropped Settings frame.
      var settingsDrafts = window.wsSettingsRepoDrafts;
      if (settingsDrafts && Array.from(settingsDrafts.values()).some(function (draft) { return draft && draft.size > 0; })) return false;
      return shFrames.length === frames.length && frames.every(function (entry, i) {
        return entry === shFrames[i] && results[i] === true;
      });
    };
  }
  // --- desktop app update
  // The desktop shell provides the optional bridge before or after page load.
  // Status: { state, version, message, skipped, phase: { phase, version,
  // skipped } }; state drives the toast, phase (unchecked | none |
  // available | ready) the settings line. downloadPage, when the shell sends
  // it, adds [Open download page] (replacing [Update] on available: a shell
  // that does not update itself); the link opens in the system browser
  // through the shell's window-open handler. A current shell adds an
  // optional action (canary | stable-return) to statuses and phases of a
  // manual channel selection, and limited to a canary miss at the page cap.
  var desktopUpdateMounted = false;
  function desktopUpdate() {
    if (desktopUpdateMounted || window !== window.top) return;
    var api = window.werkschaftDesktop && window.werkschaftDesktop.update;
    var card = gmenu && gmenu.querySelector(".gmenu");
    if (!api || typeof api.onStatus !== "function" || !card || !window.WsToast) return;
    desktopUpdateMounted = true;
    var note = card.querySelector(".gn");
    function item(id, mark, label) {
      var b = doc.createElement("button");
      b.type = "button";
      b.id = id;
      b.innerHTML = '<span class="gk"></span><span><span class="dupd-label"></span><span class="gs"></span></span>';
      b.querySelector(".gk").textContent = mark;
      b.querySelector(".dupd-label").textContent = label;
      card.insertBefore(b, note);
      return b;
    }
    var head = doc.createElement("span");
    head.className = "gt gt2";
    head.textContent = wsT("js.update.title");
    card.insertBefore(head, note);
    var check = item("dupd-check", "↻", wsT("js.update.check"));
    // Manual channel actions: each control appears only when this shell's
    // bridge has its member, so an older shell keeps the original row. The
    // canary pill shares the check row; Back to stable shows only once the
    // shell reports a packaged canary (a stable relaunch drops it).
    if (typeof api.checkCanary === "function") {
      var row = doc.createElement("div");
      row.className = "dupd-row";
      card.insertBefore(row, check);
      row.appendChild(check);
      var canary = doc.createElement("button");
      canary.type = "button";
      canary.id = "dupd-canary";
      canary.className = "dupd-canary";
      canary.textContent = wsT("js.update.canary");
      canary.title = wsT("js.update.canary.hint");
      row.appendChild(canary);
      canary.addEventListener("click", function () { api.checkCanary(); });
    }
    var stable = null, running = "";
    if (typeof api.backToStable === "function" && typeof api.channel === "function") {
      stable = item("dupd-stable", "↩", wsT("js.update.stable"));
      stable.hidden = true;
      stable.addEventListener("click", function () { api.backToStable(); });
      Promise.resolve().then(function () { return api.channel(); }).then(function (c) {
        if (!c || c.canary !== true) return;
        running = String(c.version || "");
        stable.querySelector(".gs").textContent = wsT("js.update.stable.hint", running);
        stable.hidden = false;
      }, function () {});
    }
    var restart = item("dupd-restart", "⏻", wsT("js.update.restart"));
    restart.hidden = true;
    check.addEventListener("click", function () { api.check(); });
    restart.addEventListener("click", function () { api.restart(); });
    function phaseLine(p) {
      check.querySelector(".gs").textContent = !p ? "" :
        p.phase === "none" ? wsT("js.update.state.none") :
        p.phase === "available" ? (p.action === "stable-return" ? wsT("js.update.stable.state", p.version) :
          p.action === "canary" ? wsT("js.update.canary.state", p.version) :
          wsT("js.update.state.available", p.version)) + (p.skipped && p.action !== "stable-return" ? " " + wsT("js.update.skipped") : "") :
        p.phase === "ready" ? wsT("js.update.state.ready", p.version) : "";
      restart.hidden = !p || p.phase !== "ready";
    }
    var open = null;
    function toast(text, opts) { if (open) open.close(); open = WsToast.show(text, opts); }
    var close = wsT("js.card.close");
    api.onStatus(function (s) {
      if (!s || typeof s.state !== "string") return;
      if (s.phase) phaseLine(s.phase);
      var v = String(s.version || ""), m = String(s.message || "");
      var page = typeof s.downloadPage === "string" && /^https:\/\//.test(s.downloadPage) ? s.downloadPage : "";
      var pageAct = page ? [{ label: wsT("js.update.downloadpage"), run: function () { window.open(page, "_blank", "noopener"); } }] : [];
      // action names a manual channel selection (canary | stable-return);
      // an older shell never sends it, so those statuses read as before.
      var back = s.action === "stable-return", canaryAct = s.action === "canary";
      if (s.state === "available" && back) {
        // An explicit downgrade: name the stable target and the running
        // canary before anything installs. No skip: it is a one-off choice.
        toast(wsT("js.update.stable.available", v, String(s.current || running || "")), { sticky: true, dismiss: close, actions:
          page ? pageAct : [{ label: wsT("js.update.stable.install", v), run: function () { api.download(); } }] });
      } else if (s.state === "available") {
        toast(wsT(canaryAct ? "js.update.canary.available" : "js.update.available", v) + (s.skipped ? " " + wsT("js.update.skipped") : ""), { sticky: true, dismiss: close, actions:
          (page ? pageAct : [{ label: wsT("js.update.install"), run: function () { api.download(); } }]).concat(
          [{ label: wsT("js.update.skip"), run: function () { api.skip(v); } }]) });
      } else if (s.state === "downloading") toast(wsT("js.update.downloading", v), { sticky: true });
      else if (s.state === "ready") {
        toast(wsT(back ? "js.update.stable.ready" : "js.update.ready", v), { sticky: true, dismiss: close, actions: [
          { label: wsT("js.update.restart"), run: function () { api.restart(); } }] });
      } else if (s.state === "current" && canaryAct) toast(wsT(s.limited ? "js.update.canary.limited" : "js.update.canary.none"));
      else if (s.state === "current" && back) toast(wsT("js.update.stable.current"));
      else if (s.state === "current") toast(wsT("js.update.current", v));
      else if (s.state === "error") toast(wsT(canaryAct ? "js.update.canary.failed" : back ? "js.update.stable.failed" : "js.update.failed"), { kind: "warn" });
      else if (s.state === "download-error") toast(wsT("js.update.downloadfailed", m), { kind: "warn", sticky: true, dismiss: close, actions: pageAct });
      else if (s.state === "apply-error") toast(wsT("js.update.applyfailed", m), { kind: "warn", sticky: true, dismiss: close, actions: pageAct });
    });
  }
  var jankMenuMounted = false;
  function jankMenuMount() {
    if (jankMenuMounted || window !== window.top) return;
    if (!root || !root.hasAttribute("data-sysopen")) return;
    var card = gmenu && gmenu.querySelector(".gmenu");
    if (!card || typeof WsJank === "undefined" || !WsJank) return;
    jankMenuMounted = true;
    var note = card.querySelector(".gn");
    var head = doc.createElement("span");
    head.id = "jank-menu-head";
    head.className = "gt gt2";
    head.textContent = wsT("js.jank.section");
    card.insertBefore(head, note);
    var btn = doc.createElement("button");
    btn.type = "button";
    btn.id = "jank-open";
    var kbd = doc.createElement("span");
    kbd.className = "gk";
    kbd.textContent = "⚡";
    var lbl = doc.createElement("span");
    lbl.textContent = wsT("js.jank.open");
    var badge = doc.createElement("span");
    badge.id = "jank-badge";
    badge.className = "d-jank-badge";
    badge.hidden = true;
    btn.appendChild(kbd);
    btn.appendChild(lbl);
    btn.appendChild(badge);
    card.insertBefore(btn, note);
    function syncBadge(count) {
      var n = typeof count === "number" ? count : WsJank.count();
      badge.textContent = n > 0 ? String(n) : "";
      badge.hidden = n <= 0;
    }
    syncBadge(WsJank.count());
    WsJank.onChange(syncBadge);
    btn.addEventListener("click", function () {
      WsJank.openModal({
        copyText: typeof copyText === "function" ? copyText : null,
        triggerEl: $("gear") || btn,
        closeMenu: function () { if (typeof railPopClose === "function") railPopClose(true); }
      });
    });
  }
  if (!shell) trackerStart();
  window.addEventListener("werkschaft-update-ready", desktopUpdate);
  desktopUpdate();
  jankMenuMount();
  syncSpinnerPhases(doc);
  connect();
})();
