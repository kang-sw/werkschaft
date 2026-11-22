// User commands ("command slots"): bash lines the agent registered for this
// lead, shown as a strip over the composer with the exact stored command and a
// Run click that goes through the composer's `!` path (dashboard-composer-exec.js:
// the same submission ids, admission token and result cards). The browser sends
// the command id and the revision it displayed, never text: the daemon runs the
// stored line, and only while its revision is still the displayed one. Every
// read and write is localhost-only; a denied or failed list read shows nothing.
  var ucmdEnabled = cexecEnabled;
  var ucmdList = [], ucmdRev = "", ucmdDirty = false, ucmdBox = null, ucmdSum = null, ucmdBody = null, ucmdBusy = Object.create(null), ucmdFetching = false, ucmdAttemptKey = "ws.ucmd.attempt." + rid, ucmdOpenKey = "ws.ucmd.open." + rid, ucmdGuardMs = 600;
  // The attempt in flight per command (its submission id and the revision it
  // ran) is recorded before the request goes out and cleared once the daemon's
  // answer settles it: the next click on the same revision after a lost
  // response is a retry (same id, asked not to run).
  function ucmdAttempts() {
    try {
      var a = JSON.parse(window.sessionStorage.getItem(ucmdAttemptKey) || "{}");
      // A null-prototype table: an id such as "constructor" is only ever its own entry.
      return Object.assign(Object.create(null), a && typeof a === "object" ? a : {});
    } catch (e) { return Object.create(null); }
  }
  function ucmdSaveAttempts(a) {
    try { window.sessionStorage.setItem(ucmdAttemptKey, JSON.stringify(a)); } catch (e) {}
  }
  function ucmdContainer() {
    if (ucmdBox) return ucmdBox;
    ucmdBox = doc.createElement("details");
    ucmdBox.className = "d-ucmd";
    ucmdBox.hidden = true;
    var open = true;
    try { open = window.sessionStorage.getItem(ucmdOpenKey) !== "0"; } catch (e) {}
    ucmdBox.open = open;
    ucmdBox.addEventListener("toggle", function () {
      try { window.sessionStorage.setItem(ucmdOpenKey, ucmdBox.open ? "1" : "0"); } catch (e) {}
    });
    ucmdSum = doc.createElement("summary");
    ucmdBody = doc.createElement("div");
    ucmdBody.className = "d-ucmd-list";
    ucmdBody.setAttribute("role", "list");
    ucmdBody.setAttribute("aria-label", wsT("js.ucmd.aria"));
    ucmdBox.appendChild(ucmdSum);
    ucmdBox.appendChild(ucmdBody);
    // Above the result cards when they exist, else where they will go.
    composer.insertBefore(ucmdBox, cexecBox || composer.querySelector(".cimgs") || composer.firstChild);
    return ucmdBox;
  }
  function ucmdRender() {
    if (!ucmdList.length) {
      if (ucmdBox) ucmdBox.hidden = true;
      return;
    }
    var box = ucmdContainer();
    ucmdSum.textContent = wsT("js.ucmd.title", String(ucmdList.length));
    ucmdBody.replaceChildren();
    ucmdList.forEach(function (c) {
      var row = doc.createElement("div");
      row.className = "ucmd";
      row.setAttribute("role", "listitem");
      row.setAttribute("data-ucmd", c.id);
      var head = doc.createElement("div");
      head.className = "ucmd-head";
      var run = doc.createElement("button");
      run.type = "button";
      run.className = "ucmd-run";
      run.textContent = "▶ " + c.label;
      run.title = wsT("js.ucmd.run.title", c.label);
      run.disabled = !!ucmdBusy[c.id];
      run.addEventListener("click", function (ev) {
        // The second click of a double click (and a held key's repeats) is not a
        // second approval: a click count above one is ignored.
        if (ev.detail > 1) return;
        ucmdRun(c);
      });
      head.appendChild(run);
      if (c.description) {
        var desc = doc.createElement("span");
        desc.className = "ucmd-desc";
        desc.textContent = c.description;
        head.appendChild(desc);
      }
      var x = doc.createElement("button");
      x.type = "button";
      x.className = "ucmd-x";
      x.textContent = "×";
      x.title = wsT("js.ucmd.remove");
      x.setAttribute("aria-label", wsT("js.ucmd.remove"));
      x.addEventListener("click", function () { ucmdRemove(c); });
      head.appendChild(x);
      var code = doc.createElement("code");
      code.className = "ucmd-line";
      code.textContent = c.line;
      row.appendChild(head);
      row.appendChild(code);
      ucmdBody.appendChild(row);
    });
    box.hidden = false;
  }
  // ucmdOlder reports whether rev is an older state of the list the page already
  // shows. The revision is "epoch.N.rootTag": N only grows within one daemon run
  // (epoch) and one worktree (rootTag), so a different epoch or root tag is a
  // different line of history and always wins; an unparseable one is never
  // dropped.
  function ucmdOlder(rev) {
    if (typeof rev !== "string" || !ucmdRev) return false;
    var a = rev.split("."), b = ucmdRev.split(".");
    if (a.length < 2 || b.length < 2 || a[0] !== b[0] || a.slice(2).join(".") !== b.slice(2).join(".")) return false;
    var x = parseInt(a[1], 10), y = parseInt(b[1], 10);
    return x < y;
  }
  // ucmdApply takes a list answer (a read, a removal, or a refused write's
  // refresh) and its admission token when the page holds none yet. Answers
  // arrive out of order (a slow write's reply can land after a newer read the
  // stream triggered): one older than the list on screen is dropped, so the
  // newest list stays.
  function ucmdApply(list) {
    if (typeof list.token === "string" && list.token && !cexecToken) cexecToken = list.token;
    if (ucmdOlder(list.rev)) return;
    ucmdList = list.commands || [];
    ucmdRev = typeof list.rev === "string" ? list.rev : ucmdRev;
    ucmdRender();
  }
  function ucmdFetch() {
    if (!ucmdEnabled) return;
    if (ucmdFetching) return;
    ucmdFetching = true;
    ucmdDirty = false;
    fetch("/s/" + rid + "/user-commands", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      // A denied read (a LAN page, a closed lead, an old daemon) shows nothing.
      if (!r.ok) return { denied: true };
      return r.json();
    }).catch(function () { return null; }).then(function (list) {
      ucmdFetching = false;
      if (list && list.denied) { ucmdList = []; ucmdRev = ""; ucmdRender(); }
      else if (list && Array.isArray(list.commands)) ucmdApply(list);
      // A wake that arrived while this read ran is answered with one more read
      // (never a loop: only a wake sets the flag).
      if (ucmdDirty && list && !list.denied) ucmdFetch();
    });
  }
  // The stream's opaque revision event: refetch when it is not the revision the
  // page already holds.
  function ucmdWake(rev) {
    if (!ucmdEnabled || typeof rev !== "string" || rev === ucmdRev || ucmdOlder(rev)) return;
    if (ucmdFetching) ucmdDirty = true;
    else ucmdFetch();
  }
  function ucmdRun(c) {
    if (ucmdBusy[c.id]) return;
    var attempts = ucmdAttempts(), prior = attempts[c.id];
    var retry = !!(prior && prior.rev === c.revision && typeof prior.id === "string");
    var id = retry ? prior.id : cexecNewID();
    attempts[c.id] = { id: id, rev: c.revision };
    ucmdSaveAttempts(attempts);
    ucmdBusy[c.id] = true;
    ucmdRender();
    // The slot stays guarded for a moment after its answer is rendered, so a
    // double click that straddles a fast acknowledgement never allocates a
    // second submission id: only a click after the guard is a new run.
    function finish() {
      window.setTimeout(function () { delete ucmdBusy[c.id]; ucmdRender(); }, ucmdGuardMs);
    }
    function settled() {
      var a = ucmdAttempts();
      if (a[c.id] && a[c.id].id === id) { delete a[c.id]; ucmdSaveAttempts(a); }
    }
    function post() {
      fetch("/s/" + rid + "/user-commands/" + encodeURIComponent(c.id) + "/run", {
        method: "POST", credentials: "same-origin", cache: "no-store",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ submission_id: id, revision: c.revision, retry: retry, token: cexecToken })
      }).then(function (r) {
        return r.text().then(function (s) { return { r: r, s: s }; }, function () { return { r: r, s: "" }; });
      }).then(function (x) {
        var r = x.r, s = x.s, body = null;
        try { body = JSON.parse(s); } catch (e) { body = null; }
        finish();
        if (r.ok) {
          if (body && typeof body.token === "string" && body.token) cexecToken = body.token;
          settled();
          if (body && body.unknown) {
            cexecAdd(id, c.line, { id: id, command: c.line, label: c.label, phase: "done", unknown: true });
            return;
          }
          cexecAdd(id, c.line, body && body.id === id ? body : null);
          return;
        }
        if (r.status === 409 && body && Array.isArray(body.commands)) {
          // The command changed or was withdrawn since it was displayed:
          // nothing ran, and the list is refreshed from the answer.
          settled();
          ucmdApply(body);
          noteWarn(wsT(body.error === "withdrawn" ? "js.ucmd.withdrawn" : "js.ucmd.stale"));
          return;
        }
        if (r.status === 404 || r.status === 405) noteWarn(wsT("js.ucmd.unsupported"));
        else noteWarn(wsT("js.send.fail", s.trim() || "HTTP " + r.status));
        if (r.status < 500) settled();
      }, function () {
        finish();
        // Outcome unknown: the recorded attempt stays, so the next click on this
        // revision is a retry of the same submission.
        noteWarn(wsT("js.post.unreachable"));
      });
    }
    if (cexecToken || retry) post();
    else cexecList(function (list) {
      if (!list) {
        finish();
        settled();
        noteWarn(wsT("js.post.unreachable"));
        return;
      }
      post();
    });
  }
  // The user's own withdrawal: the same service as the agent's unregister tool.
  function ucmdRemove(c) {
    fetch("/s/" + rid + "/user-commands/" + encodeURIComponent(c.id) + "/remove", {
      method: "POST", credentials: "same-origin", cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ revision: c.revision })
    }).then(function (r) {
      return r.text().then(function (s) { return { r: r, s: s }; }, function () { return { r: r, s: "" }; });
    }).then(function (x) {
      var body = null;
      try { body = JSON.parse(x.s); } catch (e) { body = null; }
      if (body && Array.isArray(body.commands)) {
        ucmdApply(body);
        return;
      }
      noteWarn(wsT("js.send.fail", x.s.trim() || "HTTP " + x.r.status));
    }, function () { noteWarn(wsT("js.post.unreachable")); });
  }
  ucmdFetch();
