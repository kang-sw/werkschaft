  // --- daemon-hosted lead sessions ----------------------------------------------
  // A lead session the daemon spawned and feeds (the lead routes under
  // /agents) starts from the rail: the "+ session" at the end of a worktree
  // row or a plain-folder bar opens the spawn popover, a rail popover like
  // the two above. On such a session's page (the server marks it
  // data-hosted) the page reads the session's state from GET
  // /agents/<reply-id> on load, after a head update, and after a
  // conversation update that changed the permission line (the daemon
  // restamps that line on every approval change); no timer polls. From the
  // state it draws the approval cards, in a box under the conversation and
  // outside the regions so a card's typed deny message survives an update,
  // and makes the head's harness chip the button of the session menu (a
  // fourth rail popover). The controls show only when the state says this
  // viewer holds them (can_control: a browser on the daemon's machine); the
  // routes refuse anyone else whatever the page shows. A session handed to
  // a terminal (status handed_off) has no process here: it shows no
  // controls, only a notice with the command that resumes it there. The
  // server marks the page of such a session nothing runs now data-handed;
  // that page reads the state too, on load and after a head update.
  var hostedPage = mode !== "index" && root.hasAttribute("data-hosted");
  var handedPage = mode !== "index" && root.hasAttribute("data-handed");
  var LEAD_MODES = ["auto", "default", "acceptEdits", "plan", "dontAsk", "bypassPermissions"];
  var LEAD_MODE_NOTE = {
    auto: "js.lead.mode.auto", "default": "js.lead.mode.default", acceptEdits: "js.lead.mode.acceptedits",
    plan: "js.lead.mode.plan", dontAsk: "js.lead.mode.dontask", bypassPermissions: "js.lead.mode.bypass"
  };
  // The effort levels when the daemon's choices name none.
  var LEAD_EFFORTS = { claude: ["low", "medium", "high", "xhigh", "max"] };
  // lead is the viewed hosted session: its last read state, the read in
  // flight, the permission line the state was last read for, and the
  // approval cards by request id.
  var lead = { st: null, busy: false, again: false, gone: false, key: "", cards: {}, answered: {}, need: null };
  // toolLabel is a tool's name as the server's rows show it: an MCP tool
  // without its server prefix.
  function toolLabel(name) {
    var m = /^mcp__.*?__(.+)$/.exec(name);
    return m ? m[1] : name;
  }
  function leadErr(status, text) { return text || (status ? "HTTP " + status : wsT("js.post.unreachable")); }
  // Shared by the child header, tree, and HUD. Keep a pending/closed set so
  // repeated clicks cannot send two close requests while a stream catches up.
  var subagentClosing = Object.create(null), subagentDialog = null;
  function markChildCloseButton(el) {
    var closed = wsT("js.subagent.closed");
    el.disabled = true;
    el.title = closed;
    el.setAttribute("aria-label", closed);
    var lbl = el.querySelector && el.querySelector(".tph-close-lbl");
    if (lbl) lbl.textContent = closed;
    else el.textContent = closed;
  }
  function subagentCloseSend(replyId, trigger, onDone) {
    if (trigger) trigger.disabled = true;
    fetch("/agents/" + encodeURIComponent(replyId) + "/close", {
      method: "POST", credentials: "same-origin", cache: "no-store",
      headers: { "Content-Type": "application/json" }, body: "{}"
    }).then(function (r) {
      return r.text().then(function (body) {
        if (r.status !== 200) throw new Error(leadErr(r.status, body.trim()));
        document.querySelectorAll("[data-reply-id]").forEach(function (el) {
          if (el.getAttribute("data-reply-id") === replyId) {
            el.setAttribute("data-subagent-closed", "1");
            if (el.hasAttribute("data-child-close")) markChildCloseButton(el);
          }
        });
        subagentClosing[replyId] = "closed";
        document.dispatchEvent(new CustomEvent("ws:subagent-closed", { detail: { replyId: replyId } }));
      });
    }).catch(function (err) {
      delete subagentClosing[replyId];
      if (trigger && trigger.isConnected) trigger.disabled = false;
      toast(wsT("js.subagent.close.error", err.message || wsT("js.post.unreachable")));
    }).then(onDone);
  }
  function subagentCloseDirect(replyId, trigger) {
    if (!root.hasAttribute("data-can-control") || !replyId || subagentClosing[replyId]) return;
    subagentClosing[replyId] = true;
    subagentCloseSend(replyId, trigger, function () {});
  }
  function subagentClose(replyId, label, state, trigger) {
    if (!root.hasAttribute("data-can-control") || !replyId || subagentClosing[replyId]) return;
    subagentClosing[replyId] = true;
    function restore() { if (trigger && trigger.isConnected && trigger.focus) trigger.focus(); }
    function cancel() { delete subagentClosing[replyId]; restore(); }
    function send() { subagentCloseSend(replyId, trigger, restore); }
    if (state === "idle" || state === "done" || state === "failed") { send(); return; }
    if (subagentDialog && subagentDialog.open) { cancel(); return; }
    var dl = mk("dialog", "rdeldlg"), title = dl.appendChild(mk("h2", "rdhd"));
    subagentDialog = dl;
    title.id = rpId();
    title.textContent = wsT("js.subagent.close.title", label || replyId);
    dl.setAttribute("aria-labelledby", title.id);
    var phase = state === "replying" ? "thinking" : state;
    var phaseKey = { thinking: "js.hud.state.thinking", tool: "js.hud.state.tool", holding: "js.hud.state.holding", permission: "js.hud.state.permission" }[phase] || "js.hud.state.thinking";
    dl.appendChild(mk("div", "rdpath", wsT("js.subagent.close.state", wsT(phaseKey))));
    dl.appendChild(mk("p", "", wsT("js.subagent.close.explain")));
    var row = dl.appendChild(mk("div", "rdrow"));
    var no = row.appendChild(rpBtn("pbtn", wsT("js.rail.cancel")));
    var yes = row.appendChild(rpBtn("pbtn danger", wsT("js.subagent.close")));
    function end(confirmed) {
      dl.close(); dl.remove(); subagentDialog = null;
      if (confirmed) send(); else cancel();
    }
    no.addEventListener("click", function () { end(false); });
    yes.addEventListener("click", function () { end(true); });
    dl.addEventListener("cancel", function (e) { e.preventDefault(); end(false); });
    dl.addEventListener("click", function (e) { if (e.target === dl) end(false); });
    document.body.appendChild(dl); dl.showModal(); no.focus();
  }
  if (typeof document !== "undefined") document.addEventListener("click", function (e) {
    var b = e.target && e.target.closest && e.target.closest("[data-child-close]");
    if (!b) return;
    subagentClose(b.getAttribute("data-reply-id"), b.getAttribute("data-label"), b.getAttribute("data-state"), b);
  });
  // leadChoices reads the harness choices the support chat's popover uses,
  // then runs done. It reads them on every open, as that popover does: the
  // daemon's lists change while the page lives (a CLI update adds models). A
  // list already held answers at once, and refresh runs when the newer one
  // arrives.
  // A list read here is stamped with the connection epoch it was read in;
  // only a list of the current epoch can offer common permission levels.
  function leadChoices(done, refresh, active) {
    var held = !!supChoices && !supChoices.error;
    // The daemon bounds one catalog listing at 15 seconds. Leave time for
    // the final publication, and keep polling light on a slow provider.
    var deadline = Date.now() + 18000, delay = 250;
    if (held) done();
    function loadChoices() {
      if (active && !active()) return;
      var epoch = permEpoch;
      tkFetch("/support/choices", undefined, function (status, v) {
        if (status === 200 && v && Array.isArray(v.harnesses)) { v.wsEpoch = epoch; supChoices = v; }
        else if (supChoices) supChoices.wsEpoch = -1;
        if (!held) { held = true; done(); }
        else if (refresh) refresh();
        if (status === 200 && v && v.pickers_pending && refresh && Date.now() < deadline) {
          setTimeout(loadChoices, delay);
          delay = Math.min(delay * 2, 2000);
        }
      });
    }
    loadChoices();
  }

  // --- common permission level ------------------------------------------------
  // A hosted lead may carry a common permission level (read-only,
  // workdir-write, full) beside its native permission mode. The level is
  // intent, not a sandbox: the state reports how it is carried (partial,
  // advisory) apart from the native mode the harness reports. A level is
  // offered and sent only on positive support from the current daemon: the
  // harness's permission_levels in a /support/choices list read since the
  // live stream last (re)connected (permEpoch counts the drops; a daemon
  // replaced underneath drops the stream). Every selection-bearing request
  // re-reads the list first, goes only to a /permission route, and a refusal
  // is shown, never resent on a legacy route. Without support the native
  // controls work as before, and nothing selects a level for the user.
  var PERM_LEVELS = ["read-only", "workdir-write", "full"];
  var PERM_LABEL = { "": "js.perm.level.default", "read-only": "js.perm.level.readonly", "workdir-write": "js.perm.level.workdir", full: "js.perm.level.full" };
  var PERM_WHAT = { "": "js.perm.what.default", "read-only": "js.perm.what.readonly", "workdir-write": "js.perm.what.workdir", full: "js.perm.what.full" };
  var PERM_Q = { partial: "js.perm.q.partial", advisory: "js.perm.q.advisory", pending: "js.perm.q.pending", enforced: "js.perm.q.enforced" };
  // The native mode the daemon maps each level to, where it has one.
  var PERM_NATIVE = {
    claude: { "read-only": "plan", "workdir-write": "acceptEdits", full: "bypassPermissions" },
    codex: { "read-only": "read-only", "workdir-write": "workspace-write", full: "danger-full-access" }
  };
  var permEpoch = 0, permDlg = null;
  function permHarness(k) { return k === "claude" ? "Claude" : k; }
  // permLevels is harness's offered levels in order: null while unknown (no
  // list of the current epoch), empty when the daemon offers none.
  function permLevels(harness) {
    if (!supChoices || supChoices.error || supChoices.wsEpoch !== permEpoch) return null;
    var h = supHarness(harness), have = h && Array.isArray(h.permission_levels) ? h.permission_levels : [];
    return PERM_LEVELS.filter(function (l) { return have.indexOf(l) >= 0; });
  }
  // permPreflight re-reads the choices and answers whether the daemon that
  // answers now offers level on an available lead-capable harness.
  function permPreflight(harness, level, done) {
    var epoch = permEpoch;
    tkFetch("/support/choices", undefined, function (status, v) {
      if (status === 200 && Array.isArray(v.harnesses)) { v.wsEpoch = epoch; supChoices = v; }
      else if (supChoices) supChoices.wsEpoch = -1;
      var h = supHarness(harness);
      done(!!h && h.lead === true && h.available === true && (permLevels(harness) || []).indexOf(level) >= 0);
    });
  }
  // permRefused reports a selection route the daemon does not have: an
  // older daemon answers 404 or 405 before any creation or change.
  function permRefused(status) { return status === 404 || status === 405; }
  function permQ(kind) {
    var k = String(kind || "");
    return mk("span", "pmq " + (PERM_Q[k] ? k : "pending"), PERM_Q[k] ? wsT(PERM_Q[k]) : k);
  }
  // permCoded appends text to el with its \u0001 marker replaced by the
  // harness name and the native mode as code.
  function permCoded(el, text, harness, mode) {
    text.split("\u0001").forEach(function (part, i) {
      if (i) { el.appendChild(mk("span", null, permHarness(harness))); el.appendChild(mk("code", null, mode)); }
      if (part.trim()) el.appendChild(mk("span", null, part.trim()));
    });
  }
  // permMap fills el with how harness carries level: the native mode it
  // runs in (mode; for no level the native pick) and the quality to expect.
  function permMap(el, harness, level, mode, change) {
    el.textContent = "";
    if (PERM_NATIVE[harness] && mode) {
      permCoded(el, wsT(!level ? "js.perm.map.default" : change ? "js.perm.map.switch" : "js.perm.map.run", "\u0001"), harness, mode);
      if (level) {
        el.appendChild(mk("span", null, "·"));
        el.appendChild(permQ(level === "read-only" ? "partial" : "advisory"));
        if (level === "read-only") el.appendChild(mk("span", null, wsT("js.perm.map.partial")));
      }
    }
    el.hidden = !el.children.length;
  }
  // permSeg is the level segment under box; picked(level) runs on a click.
  // set shows levels (a leading "" is the default), cur checked, derived
  // dashed and tagged (a level derived from older settings, not chosen).
  function permSeg(box, picked) {
    var S = { el: box.appendChild(mk("div", "spseg pmseg")), sig: null, btn: {} };
    S.el.setAttribute("role", "radiogroup");
    S.el.setAttribute("aria-label", wsT("js.perm.title"));
    S.el.addEventListener("click", function (e) {
      var b = e.target && e.target.closest && e.target.closest("[data-level]");
      if (b && !b.disabled) picked(b.getAttribute("data-level"));
    });
    S.set = function (levels, cur, derived, off) {
      var sig = levels.join("\n") + "\n" + derived;
      if (S.sig !== sig) {
        S.sig = sig;
        S.el.textContent = "";
        S.btn = {};
        levels.forEach(function (l) {
          var dashed = !!derived && l === derived;
          var b = S.btn[l] = S.el.appendChild(rpBtn([l === "full" ? "full" : "", dashed ? "derived" : ""].join(" ").trim(), wsT(PERM_LABEL[l])));
          b.setAttribute("role", "radio");
          b.setAttribute("data-level", l);
          if (dashed) b.appendChild(mk("span", "pmtag", wsT("js.perm.tag.derived")));
        });
      }
      levels.forEach(function (l) {
        S.btn[l].setAttribute("aria-checked", String(l === cur));
        S.btn[l].disabled = !!off;
      });
    };
    return S;
  }
  // permFullAsk opens the full-permission warning for harness (o.path
  // names the target; o.change a running session's change). Its grant
  // enables only once the box is ticked; ok runs on that grant, no on
  // cancel (button, Escape), which sends nothing.
  function permFullAsk(o, ok, no) {
    var D = permDlg;
    if (!D) {
      D = permDlg = { el: mk("dialog", "rdeldlg pmdlg"), ok: null, no: null };
      var dl = D.el, hd = dl.appendChild(mk("h2", "rdhd"));
      hd.id = rpId();
      dl.setAttribute("aria-labelledby", hd.id);
      hd.appendChild(gIcon("lock")).setAttribute("class", "i pmlock");
      D.title = hd.appendChild(mk("span"));
      D.path = dl.appendChild(mk("div", "rdpath"));
      D.facts = dl.appendChild(mk("div", "rdfacts"));
      var ack = dl.appendChild(mk("label", "pmack"));
      D.box = ack.appendChild(mk("input"));
      D.box.type = "checkbox";
      ack.appendChild(mk("span", null, wsT("js.perm.full.ack")));
      var row = dl.appendChild(mk("div", "rdrow"));
      D.cancel = row.appendChild(rpBtn("pbtn", wsT("js.rail.cancel")));
      D.go = row.appendChild(rpBtn("pbtn danger"));
      D.box.addEventListener("change", function () { D.go.disabled = !D.box.checked; });
      D.cancel.addEventListener("click", function () { D.end(false); });
      dl.addEventListener("cancel", function (e) { e.preventDefault(); D.end(false); });
      D.go.addEventListener("click", function () { if (D.box.checked) D.end(true); });
      D.end = function (yes) {
        var f = yes ? D.ok : D.no;
        D.ok = D.no = null;
        if (D.el.open) D.el.close();
        if (f) f();
      };
    }
    function fact(cls, icon, text, mode) {
      var f = D.facts.appendChild(mk("div", "rdfact" + (cls ? " " + cls : "")));
      f.appendChild(gIcon(icon));
      var s = f.appendChild(mk("span"));
      if (!mode) { s.textContent = text; return; }
      text.split("\u0001").forEach(function (part, i) {
        if (i) s.appendChild(mk("span", "pmc", mode));
        if (part) s.appendChild(mk("span", null, part));
      });
    }
    if (D.no) D.end(false);
    D.facts.textContent = "";
    var native = PERM_NATIVE[o.harness] && PERM_NATIVE[o.harness].full;
    if (native) fact("bad", "lock", wsT("js.perm.full.native", permHarness(o.harness), "\u0001"), native);
    else fact("bad", "lock", wsT("js.perm.full.plain", permHarness(o.harness)));
    fact("warn", "folder", wsT("js.perm.full.outside"));
    fact("", "wrench", wsT("js.perm.full.caps"));
    fact("", "agents", wsT("js.perm.full.agents"));
    D.title.textContent = wsT(o.change ? "js.perm.full.title.change" : "js.perm.full.title");
    D.path.textContent = o.path || "";
    D.path.hidden = !o.path;
    D.go.textContent = wsT(o.change ? "js.perm.full.go.change" : "js.perm.full.go");
    D.box.checked = false;
    D.go.disabled = true;
    D.ok = ok;
    D.no = no;
    if (!D.el.parentNode) doc.body.appendChild(D.el);
    if (!D.el.open) D.el.showModal();
    D.cancel.focus();
  }
  // permStreamSync follows the live stream: a drop advances the epoch (the
  // support read before it no longer counts), and an open lead or session
  // popover redraws, reading the choices again once the stream is back.
  function permStreamSync(on) {
    if (!on) permEpoch++;
    var sp = rp.P && (rp.P.permSync ? rp.P : rp.P.sess && rp.P.sess.permSync ? rp.P.sess : null);
    if (sp) sp.permSync(on);
  }
  // permFullOpen reports the warning open; permFullDrop cancels it.
  function permFullOpen() { return !!(permDlg && permDlg.el.open); }
  function permFullDrop() { if (permDlg && (permDlg.el.open || permDlg.no)) permDlg.end(false); }
  // permObserved reports whether st carries a native mode the running
  // harness reported; a harness without native modes has none to report.
  function permObserved(st) { return !!(st.process && st.active_mode); }
  // permEvidence is the quality pill's kind for st: pending while a native
  // mode is asked for but not yet reported, else the daemon's own grade.
  function permEvidence(st) {
    return st.permission_mode && !permObserved(st) ? "pending" : String(st.permission_enforcement || "");
  }
  // permState fills box with st's permission state: the intent and its
  // source, the native mode (asked for, or as reported), and the evidence,
  // never merged into one claim. note is a closing line (may be empty).
  function permState(box, st, note) {
    box.textContent = "";
    var level = st && String(st.permission_level || "");
    box.hidden = !level;
    if (!level) return;
    var harness = String(st.backend || ""), explicit = st.permission_source === "explicit";
    function row(key) {
      box.appendChild(mk("span", "pmk", wsT(key)));
      return box.appendChild(mk("span", "pmv"));
    }
    var v = row("js.perm.k.intent");
    v.appendChild(mk("b", null, PERM_LABEL[level] ? wsT(PERM_LABEL[level]) : level));
    v.appendChild(mk("span", "pmsrc", wsT(explicit ? "js.perm.src.explicit" : "js.perm.src.derived")));
    v = row("js.perm.k.native");
    var asked = String(st.permission_mode || ""), seen = permObserved(st) ? String(st.active_mode) : "";
    if (!asked && !seen) v.appendChild(mk("span", "pmsrc", wsT("js.perm.native.none", permHarness(harness))));
    else {
      v.appendChild(mk("span", null, permHarness(harness)));
      v.appendChild(mk("code", null, seen || asked));
      if (seen && asked && seen !== asked) v.appendChild(mk("span", "pmq mismatch", wsT("js.perm.native.asked", asked)));
      else v.appendChild(mk("span", "pmsrc", wsT(seen ? "js.perm.native.reported" : st.process ? "js.perm.native.next" : "js.perm.native.waiting")));
    }
    v = row("js.perm.k.evidence");
    var q = permEvidence(st);
    v.appendChild(permQ(q));
    var why = q === "pending" ? "js.perm.ev.pending" : seen && asked && seen !== asked ? "js.perm.ev.mismatch" : q === "partial" ? "js.perm.ev.partial" : !asked && !seen ? "js.perm.ev.plain" : "js.perm.ev.advisory";
    if (q === "pending" || q === "partial" || q === "advisory") v.appendChild(mk("span", "pmsrc", wsT(why, permHarness(harness))));
    if (note) box.appendChild(mk("p", "pmnote", note));
  }
  // An older daemon may omit lead markers.
  function leadHarnesses() {
    var hs = ((supChoices && supChoices.harnesses) || []).filter(function (h) { return h.lead === true && h.harness !== "pi"; });
    return hs.length ? hs : [supHarness("claude") || { harness: "claude" }];
  }
  function leadModes(harness) {
    var h = supHarness(harness);
    if (leadHarnesses().some(function (x) { return x.lead === true; })) return h && Array.isArray(h.permission_modes) ? h.permission_modes : [];
    return harness === "claude" ? LEAD_MODES : [];
  }
  function leadModelKey(id, provider) { return provider && provider !== "openrouter" && id ? provider + " " + id : id; }
  function leadModelPick(key) {
    var match = /^(openai|openrouter|local\/[a-z0-9-]+) (.+)$/.exec(key);
    return match ? { model: match[2], provider: match[1] } : { model: key, provider: "" };
  }
  function leadModelEntries(harness, provider, spawn) {
    var h = supHarness(harness), entries = ((h && h.models) || []);
    if (harness !== "werk") return entries;
    if (!spawn) return entries.filter(function (m) { return (m.provider || "openrouter") === (provider || "openrouter"); });
    var providers = (h && h.model_providers) || [];
    return entries.filter(function (m) { return providers.indexOf(m.provider || "openrouter") >= 0; });
  }
  function leadModels(harness, provider, spawn) {
    return leadModelEntries(harness, provider, spawn).map(function (m) { return spawn ? leadModelKey(String(m.id), m.provider) : String(m.id); });
  }
  function leadModelLabels(harness, provider, spawn) {
    var labels = Object.create(null);
    leadModelEntries(harness, provider, spawn).forEach(function (m) {
      labels[spawn ? leadModelKey(String(m.id), m.provider) : String(m.id)] = (m.display_name || String(m.id)) + (spawn && m.provider && m.provider !== "openrouter" ? " · " + (m.provider === "openai" ? "OpenAI" : m.provider) : "");
    });
    return labels;
  }
  function leadEfforts(harness) {
    var h = supHarness(harness);
    return h && Array.isArray(h.efforts) && h.efforts.length ? h.efforts.map(String) : (harness === "claude" ? LEAD_EFFORTS.claude : []);
  }
  // leadOptions lists "default" and list in sel with cur picked (added when
  // the list lacks it). An unchanged list is left alone, so a redraw never
  // closes an open select.
  function leadOptions(sel, list, cur, labels) {
    var all = list.slice();
    if (cur && all.indexOf(cur) < 0) all.push(cur);
    var sig = JSON.stringify(all.map(function (v) { return [v, labels && labels[v]]; }));
    if (sel.wsSig !== sig) {
      sel.wsSig = sig;
      sel.textContent = "";
      sel.appendChild(mk("option", null, wsT("js.sup.default"))).value = "";
      all.forEach(function (v) { sel.appendChild(mk("option", null, labels && labels[v] || v)).value = v; });
    }
    if (sel.value !== (cur || "")) sel.value = cur || "";
  }
  // leadRows builds, under box, the three option rows the spawn popover and
  // the session menu share (model, permission mode, effort; label left,
  // control right); changed(select) runs on a pick. With adv (the advanced
  // disclosure) inline(false) moves the native mode row there; own(mode,
  // note) shows the mode a permission level decides, the row then fixed,
  // and free() is the user's own native pick either way. With keep (the
  // spawn popover) a model or effort the form holds stays a real, enabled
  // option on an available harness even when the catalog lists none, so
  // the shown value is the one sent.
  function leadRows(box, changed, adv, keep) {
    var R = {}, own = "", ownNote = "", free = "", extra = "", inline = true, busyNow = false, cur = { harness: "", active: "", modes: [] };
    function row(label, cls) {
      var r = box.appendChild(mk("div", "sprc")), l = r.appendChild(mk("label", null, label)), s = r.appendChild(mk("select", "spsm" + (cls ? " " + cls : "")));
      s.id = rpId();
      l.htmlFor = s.id;
      s.addEventListener("change", function () { changed(s); });
      return { row: r, sel: s };
    }
    var m = row(wsT("js.sup.model"), "mono");
    var modelNote = keep && box.appendChild(mk("p", "spdesc"));
    if (modelNote) {
      modelNote.id = rpId();
      modelNote.hidden = true;
      m.sel.setAttribute("aria-describedby", modelNote.id);
    }
    var p = row(wsT("js.lead.mode"));
    var pd = box.appendChild(mk("p", "spdesc"));
    var e = row(wsT("js.sup.effort"));
    var ed = box.appendChild(mk("p", "spdesc"));
    pd.id = rpId();
    p.sel.setAttribute("aria-describedby", pd.id);
    R.model = m.sel; R.mode = p.sel; R.effort = e.sel;
    R.inline = function (on) {
      if (!adv || inline === on) return;
      inline = on;
      if (on) { box.insertBefore(p.row, e.row); box.insertBefore(pd, e.row); }
      else { adv.appendChild(p.row); adv.appendChild(pd); }
    };
    R.own = function (mode, note) {
      mode = mode || "";
      note = note || "";
      if (mode === own && note === ownNote) return;
      if (mode && !own) free = p.sel.value;
      else if (!mode && own) p.sel.value = free;
      own = mode;
      ownNote = note;
      if (own) {
        if (cur.modes.indexOf(own) < 0) { p.sel.appendChild(mk("option", null, own)).value = own; p.sel.wsSig = null; }
        p.sel.value = own;
      }
      if (R.disable) R.disable(busyNow);
      R.notes(cur.harness, cur.active);
    };
    R.free = function () { return own ? free : p.sel.disabled ? "" : p.sel.value; };
    // mark adds text to the native mode's line.
    R.mark = function (text) {
      if ((text || "") === extra) return;
      extra = text || "";
      R.notes(cur.harness, cur.active);
    };
    // set shows harness's choices with v's model, effort, and mode picked.
    R.set = function (harness, v) {
      var h = supHarness(harness), models = leadModels(harness, v.provider, keep), efforts = leadEfforts(harness), modes = leadModes(harness);
      own = ownNote = extra = "";
      cur.modes = modes;
      leadOptions(m.sel, models, v.model, leadModelLabels(harness, v.provider, keep));
      if (modelNote) {
        var notice = h && h.model_notice || "";
        if (modelNote.textContent !== notice) modelNote.textContent = notice;
        modelNote.hidden = !notice;
      }
      leadOptions(e.sel, efforts, v.effort);
      var sig = modes.join("\n");
      if (p.sel.wsSig !== sig) {
        p.sel.wsSig = sig;
        p.sel.textContent = "";
        modes.forEach(function (mode) { p.sel.appendChild(mk("option", null, mode)).value = mode; });
      }
      p.row.classList.toggle("off", !modes.length);
      var initial = harness === "claude" && modes.indexOf("auto") >= 0 ? "auto" : modes[0] || "";
      p.sel.value = modes.indexOf(v.mode) >= 0 ? v.mode : initial;
      R.disable = function (busy) {
        var unavailable = !!h && (h.lead === true ? h.available !== true : h.available === false);
        busyNow = !!busy;
        m.sel.disabled = busy || unavailable || (!models.length && !(keep && v.model));
        e.sel.disabled = busy || unavailable || (!efforts.length && !(keep && v.effort));
        p.sel.disabled = busy || unavailable || !modes.length || !!own;
      };
      R.disable(false);
      R.notes(harness, v.active);
    };
    // notes writes the lines under the mode and the effort; active is the
    // mode a running harness reported, named when it is not the picked one.
    R.notes = function (harness, active) {
      var claude = harness === "claude", mode = p.sel.value;
      cur.harness = harness;
      cur.active = active || "";
      pd.hidden = !mode;
      pd.textContent = own ? ownNote : (!mode ? "" : (claude && LEAD_MODE_NOTE[mode] ? wsT(LEAD_MODE_NOTE[mode]) : mode) + (active && active !== mode ? " " + wsT("js.lead.mode.active", active) : "")) + (extra && !own ? " " + extra : "");
      pd.classList.toggle("warn", !own && claude && mode === "bypassPermissions");
      ed.textContent = e.sel.value ? (claude ? "--effort " : "") + e.sel.value : wsT("js.lead.effort.default");
    };
    return R;
  }

  // The spawn modal for one worktree or plain folder (v3 hybrid layout):
  // header with inline searchable Named Tier pill + close button, hero Start
  // CTA with live launch summary, full-width Harness segmented bar, matched
  // side-by-side Model combobox + horizontal Effort drag knob, and a quiet
  // recessed tonal footer band holding permission capsules (with collapsed
  // Advanced native modes) and plugin isolation toggle.
  // Start spawns the session and opens its page; a refused or failed spawn
  // shows in the modal's error line and keeps it open.
  // A picked permission level decides the native mode; Start re-reads the
  // daemon's support, and "full" first opens the warning, whose grant alone
  // carries permission_full_ack. A re-read that finds no support sends nothing
  // and says so (P.dropped): the user may then start on a native mode by hand.
  // A daemon that refuses the selection route (P.refused) is shown, and Start
  // becomes a re-check; nothing is resent on the legacy route.
  var LEAD_DANGER_MODE = { bypassPermissions: 1, "danger-full-access": 1 };
  function rpLeadMake() {
    var P = { gen: 0, busy: false, loading: false, root: "", harness: "claude", model: "", effort: "", mode: "", iso: "default", level: "", dropped: "", refused: 0, statusText: "" };
    var settingsKey = "ws.dashboard.newSession", saved = {}, picks = Object.create(null);
    function readSettings() {
      try {
        var v = JSON.parse(localStorage.getItem(settingsKey));
        return v && typeof v === "object" && !Array.isArray(v) ? v : {};
      } catch (_) { return {}; }
    }
    function pick(k) {
      var v = picks[k];
      return v && typeof v === "object" && !Array.isArray(v) ? v : {};
    }
    function defaultMode(k) {
      var modes = leadModes(k);
      return k === "claude" && modes.indexOf("auto") >= 0 ? "auto" : modes[0] || "";
    }
    function setValues(k, v) {
      var modes = leadModes(k);
      P.model = v.model || "";
      P.effort = v.effort || "";
      P.mode = modes.indexOf(v.mode) >= 0 ? v.mode : defaultMode(k);
    }
    function showPick(k) {
      var v = pick(k), models = leadModels(k, "", true), model = leadModelKey(v.model, v.provider), efforts = leadEfforts(k);
      setValues(k, { model: models.indexOf(model) >= 0 ? model : "", effort: efforts.indexOf(v.effort) >= 0 ? v.effort : "", mode: v.mode });
      P.iso = v.iso === "isolated" ? "isolated" : "default";
      permDraw();
    }
    function modelOff() {
      return P.busy || P.loading || off(P.harness) || (!leadModels(P.harness, "", true).length && !P.model);
    }
    function effortOff() {
      return P.busy || P.loading || off(P.harness) || (!leadEfforts(P.harness).length && !P.effort);
    }
    function freeMode() {
      var modes = leadModes(P.harness);
      if (off(P.harness) || !modes.length) return "";
      return modes.indexOf(P.mode) >= 0 ? P.mode : defaultMode(P.harness);
    }
    function currentPick() {
      var chosen = leadModelPick(modelOff() ? "" : P.model);
      chosen.mode = freeMode();
      chosen.effort = effortOff() ? "" : P.effort;
      chosen.iso = P.iso;
      return chosen;
    }
    var el = P.el = rpPop(wsT("js.lead.title"));
    if (el.classList && el.classList.add) el.classList.add("ns-modal");
    else if (el.classList) el.classList.toggle("ns-modal", true);
    var xBtn = el.appendChild(rpBtn("ns-x", "×"));
    xBtn.setAttribute("aria-label", wsT("js.lead.ns.close"));
    xBtn.title = wsT("js.lead.ns.close");

    // Header (title + location) and inline searchable Named Tier pill
    var hd = el.appendChild(mk("div", "ns-hd"));
    hd.appendChild(mk("h1", "sphd", wsT("js.lead.title")));
    var loc = hd.appendChild(mk("div", "sploc ns-loc"));
    var tierBox = el.appendChild(mk("div", "ns-tier ns-cb sptiers"));
    var tierPick = leadTierPicker(tierBox, tierUsable, applyTier);

    // Hero Start CTA with live summary, danger badge/hint, and error line
    var ctaW = el.appendChild(mk("div", "ns-cta-w"));
    var start = ctaW.appendChild(rpBtn("ns-cta pbtn go", ""));
    var startLbl = start.appendChild(mk("span", "ns-cta-lbl", wsT("js.lead.start")));
    var startWarn = start.appendChild(mk("span", "ns-cta-warn", wsT("js.lead.ns.danger.badge")));
    startWarn.hidden = true;
    var startSum = start.appendChild(mk("span", "ns-cta-sum"));
    var ctaHint = ctaW.appendChild(mk("p", "ns-ctahint"));
    ctaHint.hidden = true;
    var err = ctaW.appendChild(mk("p", "spnote err"));
    err.setAttribute("aria-live", "polite");
    err.hidden = true;

    // Full-width Harness segmented bar + agy terms notice
    var hsec = el.appendChild(mk("div", "ns-har spsec"));
    hsec.appendChild(mk("div", "ns-lbl", wsT("js.sup.harness")));
    var seg = hsec.appendChild(mk("div", "ns-hs spseg"));
    seg.setAttribute("role", "radiogroup");
    seg.setAttribute("aria-label", wsT("js.sup.harness"));
    var hwarn = hsec.appendChild(mk("p", "spnote spterms", wsT("js.lead.agy.terms")));
    hwarn.setAttribute("role", "note");
    hwarn.hidden = true;
    var hbtn = Object.create(null), harnessSig = "";
    function harnessButtons(keep) {
      var hs = leadHarnesses(), sig = hs.map(function (h) { return h.harness; }).join("\n");
      if (harnessSig !== sig) {
        harnessSig = sig;
        seg.textContent = "";
        hbtn = Object.create(null);
        hs.forEach(function (h) {
          var k = h.harness, b = hbtn[k] = rpBtn("", "");
          b.appendChild(mk("b", null, permHarness(k)));
          b.setAttribute("role", "radio");
          b.setAttribute("data-harness", k);
          seg.appendChild(b);
        });
      }
      if (!keep && (!hbtn[P.harness] || off(P.harness))) {
        P.harness = hs.filter(function (h) { return !off(h.harness); }).map(function (h) { return h.harness; })[0] || hs[0].harness;
      }
    }

    // Matched Model (editable combobox) + Effort (horizontal drag knob) row
    var modSec = el.appendChild(mk("div", "ns-mod"));
    var modLbl = modSec.appendChild(mk("div", "ns-lbl"));
    modLbl.appendChild(mk("span", "ns-l", wsT("js.sup.model")));
    var modelTag = modLbl.appendChild(mk("span", "ns-tag", wsT("js.lead.ns.model.custom")));
    modelTag.hidden = true;
    var modelBox = modSec.appendChild(mk("div", "ns-cb"));
    var modelCombo = nsCombo(modelBox, {
      ariaLabel: wsT("js.sup.model"),
      inputAttr: ["data-model-input", "1"],
      items: function () {
        var entries = leadModelEntries(P.harness, "", true);
        var a = entries.map(function (m) {
          var id = String(m.id), key = leadModelKey(id, m.provider);
          var disp = m.display_name && m.display_name !== id ? m.display_name : "";
          var prov = P.harness === "werk" && m.provider ? (m.provider === "openai" ? "OpenAI" : m.provider) : "";
          return { val: key, label: key, sub: [disp, prov].filter(Boolean).join(" · ") };
        });
        if (P.harness !== "werk" && a.length) a.unshift({ val: "", label: wsT("js.sup.default"), manual: true });
        return a;
      },
      current: function () { return P.model; },
      pick: function (it) {
        if (it.val !== P.model) { P.model = it.val; tierPick.manual(); }
        edited();
      },
      free: function (text) {
        var v = String(text || "").trim();
        if (v !== P.model) { P.model = v; tierPick.manual(); }
        if (!P.busy) setErr("");
        drawModelTag();
        drawCta();
        tierPick.draw();
      },
      onBlur: function () { edited(); }
    });
    var modelNote = modSec.appendChild(mk("p", "ns-note spdesc"));
    modelNote.id = rpId();
    modelNote.hidden = true;
    modelCombo.input.setAttribute("aria-describedby", modelNote.id);

    var effSec = el.appendChild(mk("div", "ns-eff"));
    var effLbl = effSec.appendChild(mk("div", "ns-lbl"));
    var effL = effLbl.appendChild(mk("span", "ns-l", wsT("js.sup.effort")));
    var effVal = effL.appendChild(mk("span", "ns-v"));
    var effRst = effLbl.appendChild(rpBtn("ns-rst", wsT("js.sup.default")));
    effRst.hidden = true;
    var effKnobEl = effSec.appendChild(mk("div", "knob ns-knob"));
    var effNote = effSec.appendChild(mk("p", "ns-note spdesc"));
    effNote.hidden = true;
    var effortKnob = nsEffortKnob(effKnobEl, function (val) {
      if (val !== P.effort) { P.effort = val; tierPick.manual(); }
      edited();
    }, function (shown, offNow) {
      effVal.textContent = offNow ? "" : (shown || wsT("js.sup.default"));
    });

    // Quiet recessed tonal footer band: Permission capsules + Advanced + Isolation switch
    var foot = el.appendChild(mk("div", "ns-foot"));
    var footMain = foot.appendChild(mk("div", "ns-foot-main"));
    var psec = footMain.appendChild(mk("div", "ns-fg pmsec"));
    var pLbl = psec.appendChild(mk("span", "ns-fl", wsT("js.perm.title")));
    var pseg = permSeg(psec, function (level) {
      if (P.busy || P.level === level) return;
      P.level = level;
      P.refused = 0;
      edited();
    });
    if (pseg.el.classList) pseg.el.classList.toggle("ns-cap", true);
    var pNativeCap = psec.appendChild(mk("span", "ns-cap ns-native-cap"));
    pNativeCap.setAttribute("role", "radiogroup");
    pNativeCap.setAttribute("aria-label", wsT("js.lead.mode"));
    pNativeCap.hidden = true;
    var pmap = psec.appendChild(mk("span", "ns-map pmmap"));
    pmap.hidden = true;
    var poff = psec.appendChild(mk("span", "ns-offnote pmoff"));
    poff.hidden = true;

    var isoSw = footMain.appendChild(rpBtn("ns-sw", ""));
    isoSw.setAttribute("role", "switch");
    isoSw.setAttribute("data-iso-switch", "1");
    isoSw.setAttribute("aria-label", wsT("js.lead.iso"));
    isoSw.setAttribute("aria-checked", "false");
    isoSw.appendChild(mk("i"));
    var isoLbl = isoSw.appendChild(mk("span", null, wsT("js.lead.iso") + " · " + wsT("js.lead.iso.default")));

    var pNativeNote = foot.appendChild(mk("p", "ns-adv-note spdesc"));
    pNativeNote.hidden = true;
    var pfine = foot.appendChild(mk("p", "ns-fine pmfine", wsT("js.perm.fine.intent")));
    pfine.hidden = true;
    var perr = foot.appendChild(mk("p", "pmerr")), perrHd = perr.appendChild(mk("b"));
    perr.appendChild(mk("span", null, wsT("js.perm.refused.create")));
    perr.hidden = true;

    var adv = foot.appendChild(mk("details", "ns-adv pmadv"));
    adv.hidden = true;
    var advSum = adv.appendChild(mk("summary"));
    var advAr = advSum.appendChild(mk("span", "ns-ar", "▸"));
    advAr.setAttribute("aria-hidden", "true");
    advSum.appendChild(mk("span", null, wsT("js.perm.adv")));
    var advOwn = advSum.appendChild(mk("span", "pmown", wsT("js.perm.adv.owned")));
    advOwn.hidden = true;
    var advBody = adv.appendChild(mk("div", "ns-adv-body"));
    var advCap = advBody.appendChild(mk("span", "ns-cap ns-adv-cap"));
    advCap.setAttribute("role", "radiogroup");
    advCap.setAttribute("aria-label", wsT("js.lead.mode"));
    var advNote = advBody.appendChild(mk("p", "ns-adv-note spdesc"));
    advNote.hidden = true;

    function fillCaps(box, modes, cur, dis, onPick) {
      var sig = modes.join("\n");
      if (box.wsSig !== sig) {
        box.wsSig = sig;
        box.textContent = "";
        modes.forEach(function (m) {
          var b = box.appendChild(rpBtn(LEAD_DANGER_MODE[m] ? "dng" : "", m));
          b.setAttribute("role", "radio");
          b.setAttribute("data-mode", m);
          b.addEventListener("click", function () { if (!b.disabled) onPick(m); });
        });
      }
      Array.prototype.forEach.call(box.children, function (b) {
        var m = b.getAttribute("data-mode");
        b.setAttribute("aria-checked", String(m === cur));
        b.disabled = !!dis;
      });
    }
    function setErr(text) { err.textContent = text || ""; err.hidden = !text; }
    function tierUsable(t) {
      return !!t.harness && !!hbtn[t.harness] && !off(t.harness) && (!t.model || leadModels(t.harness, "", true).length > 0) && (!t.effort || leadEfforts(t.harness).length > 0);
    }
    function applyTier(t) {
      if (P.busy || P.loading) return;
      var mode = freeMode();
      if (t.harness !== P.harness) {
        picks[P.harness] = currentPick();
        P.harness = t.harness;
        P.dropped = "";
        P.refused = 0;
        mode = pick(t.harness).mode;
      }
      setValues(P.harness, { model: leadModelKey(t.model, t.provider) || "", effort: t.effort || "", mode: mode });
      edited();
    }
    function tierAgrees() {
      var t = tierPick.name && tierPick.current();
      if (!tierPick.name) return true;
      return !!t && P.harness === t.harness && P.model === (leadModelKey(t.model, t.provider) || "") && P.effort === (t.effort || "");
    }
    function reapplyTier() {
      if (!tierPick.name) return false;
      var t = tierPick.current();
      if (!t) { tierPick.manual(); return false; }
      applyTier(t);
      return tierAgrees();
    }
    function off(k) {
      var h = supHarness(k);
      return !hbtn[k] || (!!h && (h.lead === true ? h.available !== true : h.available === false));
    }
    function dropped() {
      var levels = permLevels(P.harness);
      return P.dropped || (P.level && levels && levels.indexOf(P.level) < 0 ? P.level : "");
    }
    function drawModelTag() {
      var known = leadModels(P.harness, "", true);
      modelTag.hidden = !(P.model && known.indexOf(P.model) < 0);
    }
    function drawModelAndEffort() {
      var h = supHarness(P.harness), efforts = leadEfforts(P.harness);
      modelCombo.input.placeholder = wsT(P.harness === "werk" ? "js.lead.ns.model.required" : "js.lead.ns.model.placeholder");
      modelCombo.setDisabled(modelOff());
      modelCombo.sync(false);
      drawModelTag();
      var notice = h && h.model_notice || "";
      if (modelNote.textContent !== notice) modelNote.textContent = notice;
      modelNote.hidden = !notice;
      var effDisabled = effortOff();
      effortKnob.set(efforts, P.effort, effDisabled);
      var noEff = !efforts.length && !P.effort;
      effRst.hidden = effDisabled || !P.effort;
      effNote.hidden = !noEff;
      if (noEff) effNote.textContent = wsT("js.sup.noefforts");
    }
    function dangerNow() {
      var levels = permLevels(P.harness), supported = !!(levels && levels.length), gone = dropped(), level = gone ? "" : P.level;
      var modes = leadModes(P.harness), mode = freeMode();
      if (supported && !gone && level === "full") return "level";
      if (modes.length > 0 && !level && !!LEAD_DANGER_MODE[mode]) return "native";
      return "";
    }
    function drawCta() {
      var levels = permLevels(P.harness), supported = !!(levels && levels.length), gone = dropped(), level = gone ? "" : P.level;
      var modes = leadModes(P.harness), danger = dangerNow();
      var missingModel = P.harness === "werk" && !P.model && !P.loading;
      start.disabled = P.busy || P.loading || off(P.harness) || (P.harness === "werk" && !P.model);
      if (start.classList) start.classList.toggle("danger", !!danger);
      startLbl.textContent = P.busy && P.statusText ? P.statusText : wsT(P.refused ? "js.perm.recheck" : gone ? (modes.length ? "js.perm.start.native" : "js.perm.start.plain") : (supported && level === "full") ? "js.perm.start.full" : "js.lead.start");
      startWarn.hidden = !danger;
      var hasEff = leadEfforts(P.harness).length > 0 || !!P.effort;
      var parts = [permHarness(P.harness), P.model || wsT("js.sup.default"), hasEff ? (P.effort || wsT("js.sup.default")) : null].filter(Boolean);
      startSum.textContent = parts.join(" · ");
      var hint = danger === "level" ? wsT("js.lead.ns.danger.confirm") : danger === "native" ? wsT("js.lead.ns.danger.bypass") : missingModel ? wsT("js.lead.ns.model.need", permHarness(P.harness)) : "";
      ctaHint.textContent = hint;
      ctaHint.hidden = !hint;
      if (ctaHint.classList) ctaHint.classList.toggle("danger", !!danger);
    }
    function permDraw() {
      var levels = permLevels(P.harness), supported = !!(levels && levels.length), modes = leadModes(P.harness);
      var gone = dropped(), level = gone ? "" : P.level, unavail = off(P.harness);
      // A level picked before support became unknown stays visible.
      var show = !gone && (supported || (!levels && !!P.level));
      pLbl.textContent = wsT(show ? "js.perm.title" : "js.lead.mode");
      pseg.el.hidden = !show;
      if (show) pseg.set([""].concat(supported ? levels : PERM_LEVELS), level, "", P.busy || !supported);
      var offText = gone ? wsT("js.perm.off.again", wsT(PERM_LABEL[gone])) : show ? "" : !levels ? wsT("js.perm.checking") : modes.length ? wsT("js.perm.off.create") : wsT("js.perm.off.plain", permHarness(P.harness));
      poff.textContent = offText;
      poff.hidden = !offText;
      var mapped = level && PERM_NATIVE[P.harness] ? PERM_NATIVE[P.harness][level] || "" : "";
      var modeNow = freeMode();
      if (show && modes.length) {
        var shownMode = level ? mapped : modeNow;
        pmap.textContent = "";
        if (shownMode) {
          pmap.appendChild(mk("span", null, "→"));
          pmap.appendChild(mk("code", null, shownMode));
          pmap.title = wsT(PERM_WHAT[level]);
          pmap.hidden = false;
        } else pmap.hidden = true;
      } else pmap.hidden = true;
      var owned = show && modes.length ? mapped : "";
      var showNativeInline = (!supported || !!gone) && modes.length > 0 && !show;
      pNativeCap.hidden = !showNativeInline;
      if (showNativeInline) {
        fillCaps(pNativeCap, modes, modeNow, P.busy || unavail, function (m) {
          if (P.mode === m) return;
          P.mode = m;
          edited();
        });
        var claudeInline = P.harness === "claude";
        var inlineNote = (claudeInline && LEAD_MODE_NOTE[modeNow] ? wsT(LEAD_MODE_NOTE[modeNow]) : modeNow) + (gone ? " " + wsT("js.perm.native.own") : "");
        pNativeNote.textContent = inlineNote;
        pNativeNote.hidden = !inlineNote;
        if (pNativeNote.classList) pNativeNote.classList.toggle("warn", !!LEAD_DANGER_MODE[modeNow]);
      } else {
        pNativeNote.hidden = true;
      }
      var showAdv = supported && !gone && modes.length > 0;
      adv.hidden = !showAdv;
      advOwn.hidden = !owned;
      if (showAdv) {
        var shownAdvMode = owned || modeNow;
        fillCaps(advCap, modes, shownAdvMode, P.busy || unavail || !!owned, function (m) {
          if (P.mode === m) return;
          P.mode = m;
          edited();
        });
        var claude = P.harness === "claude";
        var noteText = owned ? wsT("js.perm.adv.owned.note", wsT(PERM_LABEL[level])) : (claude && LEAD_MODE_NOTE[modeNow] ? wsT(LEAD_MODE_NOTE[modeNow]) : modeNow);
        advNote.textContent = noteText;
        advNote.hidden = !noteText;
        if (advNote.classList) advNote.classList.toggle("warn", !owned && !!LEAD_DANGER_MODE[modeNow]);
      }
      pfine.hidden = !supported || !!gone;
      perr.hidden = !P.refused;
      perrHd.textContent = P.refused ? wsT("js.perm.refused", P.refused) : "";
      drawCta();
    }
    function sync() {
      Object.keys(hbtn).forEach(function (k) {
        var h = supHarness(k);
        hbtn[k].setAttribute("aria-checked", String(P.harness === k));
        hbtn[k].disabled = P.loading || off(k);
        hbtn[k].title = off(k) ? wsT("js.sup.unavail", k, h && h.error || "") : "";
      });
      hwarn.hidden = P.harness !== "agy";
      var isolated = P.iso === "isolated";
      isoSw.setAttribute("aria-checked", String(isolated));
      isoSw.disabled = P.busy;
      isoLbl.textContent = wsT("js.lead.iso") + " · " + wsT(isolated ? "js.lead.iso.isolated" : "js.lead.iso.default");
      isoSw.title = wsT(isolated ? "js.lead.iso.isolated.hint" : "js.lead.iso.default.hint");
      drawModelAndEffort();
      tierPick.busy = P.busy || !!P.loading;
      tierPick.draw();
      if (el.classList) el.classList.toggle("ns-has-tiers", !tierBox.hidden);
      drawCta();
    }
    function edited() {
      if (!P.busy) setErr("");
      permDraw();
      sync();
      railPopPlace();
    }
    function setHarness(k) {
      if (P.harness === k) return;
      tierPick.manual();
      picks[P.harness] = currentPick();
      P.harness = k;
      P.level = P.dropped = "";
      P.refused = 0;
      showPick(k);
      edited();
    }
    function locText() {
      return [loc.wsName, loc.wsBranch, permHarness(P.harness)].filter(Boolean).join(" · ");
    }
    function submit() {
      sync();
      if (start.disabled) return;
      // Never launch while the picked tier and the fields disagree: put the
      // tier back, or, when it is gone, send nothing and show manual.
      if (!tierAgrees() && !reapplyTier()) return;
      if (P.refused) { recheck(); return; }
      var level = dropped() ? "" : P.level;
      if (level === "full") {
        // The grant launches what the warning named: a refresh that
        // re-applies the tier while the warning is open (or any other
        // redraw) cannot change the harness, model, or effort it sends.
        var gen = P.gen, asked = launchSnap("full", true);
        permFullAsk({ harness: asked.harness, path: locText() }, function () { if (rpLive(P, gen)) launch(asked); }, function () {});
        return;
      }
      launch(launchSnap(level, false));
    }
    function launchSnap(level, ack) {
      var chosen = currentPick();
      return {
        harness: P.harness,
        chosen: chosen,
        form: { harness: P.harness, model: chosen.model, provider: chosen.provider, effort: effortOff() ? "" : P.effort, mode: level ? "" : freeMode(), isolation: P.iso, level: level, ack: ack }
      };
    }
    function launch(snap) {
      notifAsk();
      var gen = P.gen, chosenHarness = snap.harness, chosen = snap.chosen, form = snap.form, level = form.level;
      P.busy = true;
      P.statusText = wsT(level ? "js.perm.checking" : "js.lead.starting");
      setErr("");
      permDraw();
      sync();
      if (!level) { post(gen, chosenHarness, chosen, "/agents/leads", form); return; }
      permPreflight(chosenHarness, level, function (ok) {
        if (!rpLive(P, gen)) { P.busy = false; P.statusText = ""; return; }
        if (!ok) {
          P.busy = false;
          P.statusText = "";
          if (P.harness === chosenHarness) P.dropped = level;
          else setErr(wsT("js.perm.unsent"));
          permDraw();
          sync();
          railPopPlace();
          return;
        }
        P.statusText = wsT("js.lead.starting");
        drawCta();
        post(gen, chosenHarness, chosen, "/agents/leads/permission", form);
      });
    }
    function post(gen, chosenHarness, chosen, route, form) {
      var perm = !!form.level;
      railPost(route, WsRail.leadBody(P.root, form), function (st, v, s) {
        P.busy = false;
        P.statusText = "";
        sync();
        if (st === 201 && v.id) {
          var stored = readSettings(), byHarness = stored.harnesses;
          if (!byHarness || typeof byHarness !== "object" || Array.isArray(byHarness)) byHarness = {};
          byHarness[chosenHarness] = chosen;
          try { localStorage.setItem(settingsKey, JSON.stringify({ harness: chosenHarness, harnesses: byHarness })); } catch (_) { /* storage may be unavailable */ }
          if (rpLive(P, gen)) railPopClose(false);
          railOpen(false);
          saveDraft();
          try { sessionStorage.setItem(RENAME_NEW, JSON.stringify({ id: String(v.id), at: Date.now() })); } catch (_) { /* storage may be unavailable */ }
          go("/s/" + encodeURIComponent(String(v.id)));
          return;
        }
        if (perm && permRefused(st)) {
          if (supChoices) supChoices.wsEpoch = -1;
          if (!rpLive(P, gen)) { toast(wsT("js.perm.refused", st)); return; }
          P.refused = st;
          permDraw();
          sync();
          railPopPlace();
          return;
        }
        if (!rpLive(P, gen)) { toast(leadErr(st, s)); return; }
        permDraw();
        setErr(leadErr(st, s));
        sync();
        railPopPlace();
      }, 20000);
    }
    function recheck() {
      var gen = P.gen, level = P.level;
      P.busy = true;
      P.statusText = wsT("js.perm.checking");
      sync();
      permPreflight(P.harness, level, function (ok) {
        P.busy = false;
        P.statusText = "";
        if (!rpLive(P, gen)) return;
        P.refused = 0;
        if (!ok) P.dropped = level;
        permDraw();
        sync();
        railPopPlace();
      });
    }
    isoSw.addEventListener("click", function () { if (P.busy) return; P.iso = P.iso === "isolated" ? "default" : "isolated"; sync(); });
    seg.addEventListener("click", function (e) { var b = e.target.closest && e.target.closest("[data-harness]"); if (b && !b.disabled) setHarness(b.getAttribute("data-harness")); });
    effRst.addEventListener("click", function () { if (P.busy || P.loading || !P.effort) return; P.effort = ""; tierPick.manual(); edited(); });
    xBtn.addEventListener("click", function () { railPopClose(true); });
    start.addEventListener("click", submit);
    P.open = function (key, label, btn) {
      var gen = ++P.gen, name = btn.getAttribute("data-name") || label, branch = btn.getAttribute("data-branch") || "";
      P.busy = false;
      P.statusText = "";
      P.root = key;
      P.level = P.dropped = "";
      P.refused = 0;
      adv.open = false;
      saved = readSettings();
      picks = Object.create(null);
      if (saved.harnesses && typeof saved.harnesses === "object" && !Array.isArray(saved.harnesses)) {
        Object.keys(saved.harnesses).forEach(function (k) { picks[k] = saved.harnesses[k]; });
      }
      P.harness = "claude";
      P.iso = "default";
      P.loading = true;
      loc.textContent = "";
      loc.wsName = name;
      loc.wsBranch = branch;
      loc.appendChild(gIcon(branch ? "repo" : "wsfolder"));
      loc.appendChild(mk("span", null, name));
      if (branch) { loc.appendChild(gIcon("branch")); loc.appendChild(mk("b", null, branch)); }
      loc.appendChild(mk("code", null, key)).title = key;
      setErr("");
      tierPick.reset();
      harnessButtons();
      setValues(P.harness, { model: "", effort: "", mode: "" });
      permDraw();
      sync();
      leadChoices(function () {
        if (!rpLive(P, gen)) return;
        P.loading = false;
        P.harness = typeof saved.harness === "string" ? saved.harness : "claude";
        harnessButtons();
        showPick(P.harness);
        sync();
        railPopPlace();
      }, function () {
        if (rpLive(P, gen)) choicesArrived();
      }, function () { return rpLive(P, gen); });
    };
    function choicesArrived() {
      var shown = { model: P.model, effort: P.effort, mode: freeMode() };
      picks[P.harness] = currentPick();
      harnessButtons(true);
      setValues(P.harness, shown);
      permDraw();
      reapplyTier();
      sync();
      railPopPlace();
    }
    P.permSync = function (read) {
      var gen = P.gen, live = function () { return rpLive(P, gen) && !P.loading; };
      var arrived = function () { if (live()) choicesArrived(); };
      if (live()) { permDraw(); sync(); railPopPlace(); }
      if (read) leadChoices(arrived, arrived, live);
    };
    P.back = function () { return tierPick.close() || modelCombo.close(); };
    P.focus = function () { (hbtn[P.harness] && !hbtn[P.harness].disabled ? hbtn[P.harness] : xBtn).focus(); };
    P.close = function () { tierPick.close(); modelCombo.close(); permFullDrop(); };
    return P;
  }

  // The session menu of the viewed hosted session, on the head's harness
  // chip: model, permission mode, and effort (a pick is sent at once and
  // applies from the next turn), stop (the process ends; the next message
  // or mail starts it again), restart (shown while an MCP server stays
  // broken: the process ends and a new one resumes the conversation), hand
  // to terminal (the process ends and the page gives the command that
  // resumes the conversation in a terminal), and close (the conversation
  // ends for good, after a confirmation). Stop, restart, and hand to
  // terminal confirm first while the harness runs background tasks, which
  // they end, and close's confirmation then names their count too. A
  // refused change shows in the menu's error line, and the rows return to
  // what the session has.
  // With inRow it is instead the section a hosted row's menu (the rail row
  // menu) shows for that row's session, which may not be the viewed one:
  // P.rid is that session, its state (P.st) is read from GET
  // /agents/<P.rid> when the row menu opens, its actions post to that
  // session's routes, and the section shows only while its state says
  // this viewer holds the controls. A change to the viewed session updates
  // the page's own state (lead.st) too.
  var leadActions = new Set();
  function rpSessMake(inRow) {
    var P = { gen: 0, busy: false, rid: rid, st: null };
    var el = P.el = inRow ? mk("div", "rmsess") : rpPop(wsT("js.lead.menu"));
    if (inRow) {
      el.hidden = true;
      el.setAttribute("role", "group");
      el.setAttribute("aria-label", wsT("js.lead.menu"));
    }
    el.appendChild(mk("div", "sphd", wsT("js.lead.menu")));
    // The session's permission state, then (a lead on a daemon that offers
    // levels for its harness) the level to change it to: a pick re-reads
    // the support and posts to settings/permission at once; "full" first
    // opens the warning, and its cancel leaves the session as it was.
    var pst = el.appendChild(mk("div", "pmstate"));
    var pchg = el.appendChild(mk("div", "spsec pmchg"));
    pchg.appendChild(mk("div", "splbl", wsT("js.perm.title")));
    var pseg = permSeg(pchg, pickLevel);
    var pwhat = pchg.appendChild(mk("p", "pmwhat")), pmap = pchg.appendChild(mk("p", "pmmap")), pfine = pchg.appendChild(mk("p", "pmfine"));
    var opts = el.appendChild(mk("div", "spopts"));
    var adv = el.appendChild(mk("details", "pmadv")), advSum = adv.appendChild(mk("summary", null, wsT("js.perm.adv")));
    var advOwn = advSum.appendChild(mk("span", "pmown", wsT("js.perm.adv.owned")));
    adv.open = true;
    var rows = leadRows(opts, function (sel) {
      var body = {};
      if (sel === rows.model) body.model = sel.value;
      else if (sel === rows.effort) body.effort = sel.value;
      else body.permission_mode = sel.value;
      act("settings", body);
    }, adv);
    el.appendChild(mk("p", "spnote", wsT("js.lead.menu.note")));
    var acts = el.appendChild(mk("div", "spacts"));
    function actBtn(icon, cls, title, hold) {
      var b = acts.appendChild(rpBtn("spact" + cls));
      b.appendChild(typeof icon === "string" ? gIcon(icon) : icon);
      var s = b.appendChild(mk("span", null, title));
      var gs = s.appendChild(mk("span", "gs"));
      if (!hold) return { b: b, note: gs };
      var idle = gs.appendChild(mk("span", "gs-idle"));
      var ready = gs.appendChild(mk("span", "gs-ready", wsT("js.hold.release")));
      ready.setAttribute("aria-hidden", "true");
      return { b: b, note: idle, ready: ready };
    }
    var stop = actBtn("stop", "", wsT("js.lead.stop"), true), restart = actBtn("reload", "", wsT("js.lead.restart"));
    // archive hides the session from the rail (no process, mail held until
    // unarchive); on an archived session it is unarchive (back to asleep).
    var archive = actBtn("archive", "", wsT("js.lead.archive"), true), archT = archive.b.lastChild.firstChild;
    var close = actBtn("power", " danger", wsT("js.lead.close"), true);
    function holdHintNote(base) { return base + " · " + wsT("js.hold.hint"); }
    function canClickStop(st) { return st && st.safe_stop && !stopNeedsHold.has(P.rid); }
    function drawStopNote(st) {
      if (!st || !st.process) { stop.note.textContent = wsT("js.lead.stopped"); return; }
      stop.note.textContent = canClickStop(st) ? wsT("js.lead.stop.note") : holdHintNote(wsT("js.lead.stop.note"));
    }
    function drawArchiveNote(st) {
      archive.note.textContent = st && st.hidden ? wsT("js.lead.unarchive.note") : holdHintNote(wsT("js.lead.archive.note"));
    }
    function drawCloseNote() {
      close.note.textContent = holdHintNote(wsT("js.lead.close.note"));
    }
    drawCloseNote();
    // state is the session's last read state.
    function state() { return inRow ? P.st : lead.st; }
    // bgTasks is the background task count of the last read state.
    function bgTasks() { var st = state(); return Number(st && st.background_tasks) || 0; }
    var err = el.appendChild(mk("p", "spnote err"));
    err.setAttribute("aria-live", "polite");
    var failed = "";
    function act(action, body) {
      var target = P.rid, gen = P.gen;
      if (P.busy || leadActions.has(target)) return;
      leadActions.add(target);
      P.busy = true;
      failed = "";
      P.draw();
      railPost("/agents/" + encodeURIComponent(target) + "/" + action, body, function (st, v, s) {
        leadActions.delete(target);
        if (P.rid === target) P.busy = false;
        if (st === 200 && v.id) {
          if (action === "stop") {
            stopNeedsHold.delete(target);
            if (body.safe_only && P.live(gen)) railPopClose(false);
          }
          if (inRow && gen === P.gen) P.st = v;
          if (rid === target && (hostedPage || handedPage)) lead.st = v;
          P.redraw();
          return;
        }
        if (action === "stop" && body.safe_only && st === 409) {
          stopNeedsHold.add(target);
          railMarks();
          if (P.live(gen)) P.draw();
          else toast(wsT("js.hold.hint"));
          return;
        }
        var msg = action === "hide" || action === "unhide" ? archErr(action, st, s) : leadErr(st, s);
        // A daemon without the selection route changed nothing; its support
        // is read again, and nothing is resent on the legacy route.
        var refused = action === "settings/permission" && permRefused(st);
        if (refused) {
          msg = wsT("js.perm.refused", st) + " " + wsT("js.perm.refused.settings");
          if (supChoices) supChoices.wsEpoch = -1;
        }
        if (P.live(gen)) failed = msg;
        else toast(msg);
        P.redraw();
        P.fetch();
        if (refused) P.permSync(true);
      });
    }
    // pickLevel changes the session's level after a fresh support read.
    function pickLevel(level) {
      var st = state();
      if (P.busy || !st || (st.permission_source === "explicit" && st.permission_level === level)) return;
      var harness = String(st.backend || ""), gen = P.gen;
      function send(ack) {
        if (!P.live(gen)) return;
        P.busy = true;
        failed = "";
        P.draw();
        permPreflight(harness, level, function (ok) {
          P.busy = false;
          if (!ok) {
            if (P.live(gen)) failed = wsT("js.perm.unsent");
            else toast(wsT("js.perm.unsent"));
            P.redraw();
            return;
          }
          if (!P.live(gen)) return;
          var body = { permission_level: level };
          if (ack) body.permission_full_ack = true;
          act("settings/permission", body);
        });
      }
      if (level !== "full") { send(false); return; }
      permFullAsk({ harness: harness, change: true, path: [String(st.label || P.rid || ""), permHarness(harness)].filter(Boolean).join(" · ") }, function () { send(true); }, function () { if (P.live(gen)) P.draw(); });
    }
    // live reports the menu still open as it was when gen was taken;
    // redraw shows a new state (the head's own menu redraws with the
    // page's state), and fetch reads it again.
    P.live = function (gen) { return inRow ? !!rp.P && rp.P.sess === P && P.gen === gen : rpLive(P, gen); };
    P.redraw = function () {
      if (!inRow) { leadDraw(); return; }
      P.draw();
      if (P.rid === rid && (hostedPage || handedPage)) leadDraw();
    };
    P.fetch = function () {
      if (!inRow) { leadFetch(); return; }
      var gen = P.gen;
      tkFetch("/agents/" + encodeURIComponent(P.rid), undefined, function (status, v) {
        if (gen !== P.gen) return;
        if (status === 200 && v.id) P.st = v;
        else if (status === 404) P.st = null;
        P.draw();
      });
    };
    // The menu closes before asking, so return focus to the menu's opener.
    async function confirmAction(action, button, message, n) {
      var target = P.rid, opener = railPopBtn() || button, page = rid, st = state();
      if (button.disabled || leadGone(st) || !st.can_control || leadActions.has(target) || (!inRow && target !== page)) return;
      railPopClose(false);
      var gen = P.gen;
      if (message) {
        if (!(await window.wsConfirm(message, { opener: opener, accept: action === "close" ? wsT("js.lead.close") : undefined }))) return;
        st = state();
        if (P.gen !== gen || P.rid !== target || rid !== page || !opener.isConnected ||
            leadGone(st) || !st.can_control || bgTasks() > n || (action === "stop" && !st.process) ||
            (action === "close" && st.is_child)) return;
      }
      act(action, {});
    }
    function canActOn(button, extra) {
      var st = state();
      return !button.disabled && !leadGone(st) && !!st.can_control && !leadActions.has(P.rid) && (inRow || P.rid === rid) && (!extra || extra(st));
    }
    function bindHold(item, restoreNote, opts) {
      function showReady(on) {
        if (on) {
          item.note.setAttribute("aria-hidden", "true");
          item.ready.removeAttribute("aria-hidden");
        } else {
          item.note.removeAttribute("aria-hidden");
          item.ready.setAttribute("aria-hidden", "true");
        }
      }
      if (window.WsHoldConfirm) {
        return window.WsHoldConfirm.attach(item.b, {
          mode: "bar",
          shouldHold: opts.shouldHold,
          canStart: opts.canStart,
          onReady: function () { showReady(true); },
          onCancel: function () { showReady(false); restoreNote(state()); if (typeof railRelease === "function") railRelease(); },
          onConfirm: function (el, held) { showReady(false); opts.onConfirm(held); if (typeof railRelease === "function") railRelease(); }
        });
      }
      item.b.addEventListener("click", function () {
        if (opts.canStart(item.b)) opts.onConfirm();
      });
      return { sync: function () {}, cancel: function () {} };
    }
    var stopHold = bindHold(stop, drawStopNote, {
      shouldHold: function () { return !canClickStop(state()); },
      canStart: function () { return canActOn(stop.b, function (st) { return !!st.process; }); },
      onConfirm: function (held) {
        if (!canActOn(stop.b, function (st) { return !!st.process; })) return;
        if (held) railPopClose(false);
        act("stop", held ? {} : { safe_only: true });
      }
    });
    var archiveHold = bindHold(archive, drawArchiveNote, {
      shouldHold: function () { var st = state(); return !(st && st.hidden); },
      canStart: function () { return canActOn(archive.b); },
      onConfirm: function () {
        var st = state();
        if (!canActOn(archive.b)) return;
        railPopClose(false);
        act(st && st.hidden ? "unhide" : "hide", {});
      }
    });
    var closeHold = bindHold(close, drawCloseNote, {
      shouldHold: function () { return true; },
      canStart: function () { return canActOn(close.b); },
      onConfirm: function () {
        var st = state(), target = P.rid;
        if (!canActOn(close.b)) return;
        railPopClose(false);
        if (st && st.is_child) subagentCloseDirect(target, close.b);
        else act("close", {});
      }
    });
    restart.b.addEventListener("click", function () {
      var n = bgTasks();
      confirmAction("restart", restart.b, n > 0 ? wsTn("js.lead.restart.confirm", n) : "", n);
    });
    // draw shows the session's state; it runs when the menu opens and after
    // every read of the state while it is open.
    P.draw = function () {
      var st = state();
      if (inRow) {
        var can = !!(st && st.can_control && !leadGone(st));
        if (el.hidden !== !can) {
          el.hidden = !can;
          railPopPlace();
        }
        if (!can) return;
      }
      if (!st) return;
      var text = failed || (st.error ? String(st.error) : "");
      rows.set(String(st.backend || ""), { model: String(st.model || ""), provider: String(st.provider || ""), effort: String(st.effort || ""), mode: String(st.permission_mode || ""), active: st.process ? String(st.active_mode || "") : "" });
      permDraw(st);
      rows.disable(P.busy);
      stop.b.disabled = P.busy || !st.process;
      var safeStop = canClickStop(st);
      var stopSig = (st.process ? "1" : "0") + ":" + (safeStop ? "1" : "0");
      if (stop.b.getAttribute("data-stop-sig") !== stopSig) {
        stop.b.setAttribute("data-stop-sig", stopSig);
        stop.b.setAttribute("data-safe", safeStop ? "1" : "0");
        stop.b.classList.toggle("danger", !!(st.process && !safeStop));
        stop.b.replaceChild(gIcon(st.process && safeStop ? "moon" : "stop"), stop.b.firstChild);
        stopHold.sync();
      }
      if (stop.b.getAttribute("data-hold-state") !== "ready") drawStopNote(st);
      if (stop.b.disabled) stopHold.cancel();
      var broken = Array.isArray(st.mcp_broken) ? st.mcp_broken.map(String) : [];
      restart.b.hidden = !broken.length;
      restart.b.disabled = P.busy;
      archive.b.hidden = !!st.is_child;
      archive.b.disabled = P.busy;
      if (archive.b.getAttribute("data-hidden") !== String(!!st.hidden)) {
        archive.b.setAttribute("data-hidden", String(!!st.hidden));
        archive.b.replaceChild(gIcon(st.hidden ? "archive-restore" : "archive"), archive.b.firstChild);
        archT.textContent = wsT(st.hidden ? "js.lead.unarchive" : "js.lead.archive");
        archiveHold.sync();
      }
      if (archive.b.getAttribute("data-hold-state") !== "ready") drawArchiveNote(st);
      if (archive.b.disabled || archive.b.hidden) archiveHold.cancel();
      restart.note.textContent = broken.length ? wsT("js.lead.restart.note", broken.join(", ")) : "";
      close.b.disabled = P.busy || subagentClosing[P.rid] === "closed";
      if (close.b.getAttribute("data-hold-state") !== "ready") drawCloseNote();
      if (close.b.disabled) closeHold.cancel();
      err.textContent = text;
      err.hidden = !text;
    };
    // open takes the row's session (inRow; the head's menu is the viewed
    // session's).
    P.open = function (id) {
      var gen = ++P.gen;
      P.busy = false;
      failed = "";
      if (inRow) {
        P.rid = id;
        P.st = null;
      }
      P.draw();
      var redraw = function () { if (P.live(gen)) { P.draw(); railPopPlace(); } };
      leadChoices(redraw, redraw, function () { return P.live(gen); });
      P.fetch();
    };
    // permDraw shows st's permission state and, for a lead whose harness
    // the current daemon offers levels for, the change segment. A worker
    // keeps its state line alone: no worker selection support is offered.
    function permDraw(st) {
      var harness = String(st.backend || ""), lead = !!st.lead && !st.is_child, levels = lead ? permLevels(harness) : [];
      var supported = !!(levels && levels.length), level = String(st.permission_level || ""), explicit = st.permission_source === "explicit";
      permState(pst, st, lead && levels && !levels.length && level ? wsT("js.perm.off.settings") : "");
      pchg.hidden = !supported;
      var cur = explicit ? level : "", modes = leadModes(harness).length > 0;
      if (supported) {
        pseg.set(levels, cur, explicit ? "" : level, P.busy);
        pwhat.textContent = cur ? wsT(cur === "full" ? "js.perm.what.full.set" : PERM_WHAT[cur]) : "";
        pwhat.hidden = !cur;
        if (cur) permMap(pmap, harness, cur, String(st.permission_mode || ""), true);
        else pmap.hidden = true;
        pfine.textContent = wsT(cur ? "js.perm.fine.explicit" : "js.perm.fine.derived");
      }
      var owned = supported && cur && modes ? String(st.permission_mode || "") : "";
      rows.own(owned, owned ? wsT("js.perm.adv.owned.note", wsT(PERM_LABEL[cur])) : "");
      advOwn.hidden = !owned;
      rows.inline(!supported);
      adv.hidden = !(supported && modes);
    }
    // permSync redraws after the live stream dropped or a refusal (support
    // unknown) and re-reads the choices when read is set.
    P.permSync = function (read) {
      var gen = P.gen, redraw = function () { if (P.live(gen)) { P.draw(); railPopPlace(); } };
      redraw();
      if (read) leadChoices(redraw, redraw, function () { return P.live(gen); });
    };
    P.focus = function () { rows.model.focus(); };
    P.close = function () {
      stopHold.cancel();
      archiveHold.cancel();
      closeHold.cancel();
      permFullDrop();
      if (!inRow) return;
      P.gen++;
      el.hidden = true;
    };
    return P;
  }

  // leadGone reports a state with no process to control: closed for good,
  // or handed to a terminal.
  function leadGone(st) { return !st || st.status === "closed" || st.status === "handed_off"; }
  function leadCan() { var st = lead.st; return !!(st && st.can_control && !leadGone(st)); }
  // leadFetch reads the viewed session's state and draws it; a read asked
  // for while one is in flight runs once after it.
  function leadFetch() {
    if ((!hostedPage && !handedPage) || !window.fetch) return;
    if (lead.busy) { lead.again = true; return; }
    lead.busy = true;
    tkFetch("/agents/" + encodeURIComponent(rid), undefined, function (status, v) {
      lead.busy = false;
      // A daemon that no longer runs the session answers 404: the page then
      // shows it as any session it only watches.
      if (status === 200 && v.id) { lead.st = v; lead.gone = false; }
      else if (status === 404) { lead.st = null; lead.gone = true; }
      if (status === 200 || status === 404) leadDraw();
      if (lead.again) { lead.again = false; leadFetch(); }
    });
  }
  // leadChip makes the head's harness chip the session menu's button for a
  // viewer who holds the controls. The head region is replaced on every
  // update, so this runs again after each.
  function leadChip() {
    permChip();
    var h = region("head"), chip = h && h.querySelector(".host.sse"), can = leadCan();
    if (!chip || can === chip.classList.contains("hbtn")) return;
    chip.classList.toggle("hbtn", can);
    if (can) {
      chip.setAttribute("role", "button");
      chip.setAttribute("tabindex", "0");
      chip.setAttribute("aria-haspopup", "dialog");
      chip.setAttribute("aria-label", wsT("js.lead.menu"));
      chip.setAttribute("aria-expanded", String(!!rp.P && rp.kind === "sess"));
      chip.appendChild(gIcon("chev"));
    } else {
      ["role", "tabindex", "aria-haspopup", "aria-label", "aria-expanded"].forEach(function (a) { chip.removeAttribute(a); });
      q("svg", chip).forEach(function (s) { s.remove(); });
    }
  }
  // permChip keeps the head quiet about permissions: the level and its
  // evidence live in the session menu. Only a mismatch (the harness reports
  // a native mode other than the one asked for) stays as an icon-only chip
  // beside the harness chip, and for a viewer who holds the controls it
  // opens the session menu, as the harness chip does.
  function permChip() {
    var h = region("head"), host = h && h.querySelector(".host.sse"), st = lead.st;
    if (!host) return;
    var chip = h.querySelector(".pmhead"), asked = st ? String(st.permission_mode || "") : "";
    var bad = st && !leadGone(st) && st.permission_level && permObserved(st) && asked && String(st.active_mode) !== asked;
    if (!bad) { if (chip) chip.remove(); return; }
    if (!chip) {
      chip = mk("span", "chip pmhead warn");
      chip.addEventListener("click", function () {
        var c = region("head"), hb = c && c.querySelector(".host.hbtn");
        if (hb && leadCan() && (railEl || framed) && WsRail) railPopOpen("sess", hb);
      });
      chip.addEventListener("keydown", function (e) {
        if ((e.key === "Enter" || e.key === " ") && chip.getAttribute("role") === "button") { e.preventDefault(); chip.click(); }
      });
    }
    if (chip.previousSibling !== host) host.parentNode.insertBefore(chip, host.nextSibling);
    var text = wsT("js.perm.ev.mismatch", permHarness(String(st.backend || ""))), sig = text;
    if (chip.wsSig !== sig) {
      chip.wsSig = sig;
      chip.textContent = "";
      chip.appendChild(gIcon("lock"));
      chip.title = text;
      chip.setAttribute("aria-label", text);
    }
    var can = leadCan();
    if (can) { chip.setAttribute("role", "button"); chip.setAttribute("tabindex", "0"); }
    else { chip.removeAttribute("role"); chip.removeAttribute("tabindex"); }
  }
  function apChanged() {
    document.dispatchEvent(new CustomEvent("ws-approval-rows-changed"));
  }
  function apMetadata(el, a) {
    el.setAttribute("data-audit-owner", "approval");
    el.setAttribute("data-approval-id", String(a.id));
    if (a.tool_use_id) el.setAttribute("data-approval-call", String(a.tool_use_id));
    if (a.started_at) el.setAttribute("data-at", new Date(a.started_at).toISOString());
  }
  // State contains pending prompts only; disappearance cannot prove a decision.
  // Client-owned rows have no data-entry, so transcript merges retain them.
  function apMountAnswered() {
    var convo = $("convo"), changed = false;
    if (!convo) return false;
    var box = convo.querySelector(".centries");
    if (!box) return false;
    var oldest = box.querySelector("[data-entry][data-at]");
    var cutoff = oldest ? Date.parse(oldest.getAttribute("data-at")) : NaN;
    Object.keys(lead.answered).forEach(function (id) {
      var row = lead.answered[id], at = Date.parse(row.getAttribute("data-at"));
      if (isFinite(at) && isFinite(cutoff) && at < cutoff) {
        if (row.parentNode) { row.remove(); changed = true; }
        delete lead.answered[id];
        return;
      }
      var before = null;
      Array.prototype.some.call(box.children, function (child) {
        if (child === row) return false;
        var stamp = Date.parse(child.getAttribute("data-at"));
        var other = child.getAttribute("data-approval-id");
        if (stamp > at || (stamp === at && other && other > id)) { before = child; return true; }
        return false;
      });
      if (row.parentNode !== box || row.nextSibling !== before) {
        box.insertBefore(row, before);
        changed = true;
      }
    });
    return changed;
  }
  function apAnswered(a, behavior, message, pending) {
    if (lead.answered[a.id]) return;
    var row = mk("details", "apanswered");
    apMetadata(row, a);
    var h = row.appendChild(mk("summary", "conv-row"));
    h.appendChild(mk("span", "row-icon")).appendChild(gIcon(behavior === "allow" ? "check" : "x"));
    var content = h.appendChild(mk("span", "row-content"));
    content.appendChild(mk("b", null, wsT("js.ap.answered")));
    content.appendChild(mk("code", null, toolLabel(String(a.tool || ""))));
    content.appendChild(mk("span", null, wsT(behavior === "allow" ? "js.ap.allow" : "js.ap.deny")));
    if (a.started_at) content.appendChild(mk("span", "apwait", Math.max(0, Math.round((Date.now() - a.started_at) / 1000)) + "s"));
    var time = h.appendChild(mk("span", "row-time"));
    if (a.started_at) {
      var t = time.appendChild(mk("time", "t"));
      t.setAttribute("data-ms", String(a.started_at));
      t.setAttribute("data-f", "clock");
    }
    h.appendChild(mk("span", "row-chevron")).appendChild(gIcon("chev"));
    var body = row.appendChild(mk("div", "row-details"));
    if (a.description) body.appendChild(mk("div", "apdesc", String(a.description)));
    var text = WsRail.approvalText(a.input);
    if (text) body.appendChild(mk("pre", "apin", text));
    if (message) body.appendChild(mk("div", "apdesc", message));
    // Preserve already-linked audit nodes when the pending card becomes a trace.
    var audit = pending.querySelector(".conv-audit-items") || mk("div", "conv-audit-items");
    row.appendChild(audit);
    lead.answered[a.id] = row;
    apMountAnswered();
    fmtTimes(row);
    lead.approvalChanged = true;
  }
  document.addEventListener("ws-entries-merged", function () {
    if (apMountAnswered()) apChanged();
  });
  // apCard builds the card of approval request a: the tool, the request's
  // description, a readable preview of the input, and Allow and Deny (Deny
  // opens an optional message to send with it). A viewer without the
  // controls (can false) gets the card with one line saying where it is
  // answered. An answer that comes too late (409: another page answered, or
  // the harness withdrew the request) only reads the state again.
  function apCard(a, can) {
    var C = { busy: false };
    var el = C.el = mk("div", "apcard");
    apMetadata(el, a);
    var h = el.appendChild(mk("div", "aph conv-row"));
    h.appendChild(mk("span", "row-icon")).appendChild(gIcon("lock"));
    var content = h.appendChild(mk("span", "row-content"));
    content.appendChild(mk("b", null, wsT("js.ap.title")));
    content.appendChild(mk("code", null, toolLabel(String(a.tool || "")))).title = String(a.tool || "");
    if (a.agent_id) content.appendChild(mk("span", "apsub", wsT("js.ap.agent")));
    C.n = content.appendChild(mk("span", "apn"));
    var time = h.appendChild(mk("span", "row-time"));
    if (a.started_at) {
      var t = time.appendChild(mk("time", "t"));
      t.setAttribute("data-ms", String(a.started_at));
      t.setAttribute("data-f", "since");
    }
    h.appendChild(mk("span", "row-chevron"));
    el.appendChild(mk("div", "conv-audit-items"));
    var b = el.appendChild(mk("div", "apb")), text = WsRail.approvalText(a.input);
    if (a.description) b.appendChild(mk("div", "apdesc", String(a.description)));
    if (text) b.appendChild(mk("pre", "apin", text));
    if (!can) {
      var ro = el.appendChild(mk("div", "apro"));
      ro.appendChild(gIcon("lock"));
      ro.appendChild(mk("span", null, wsT("js.ap.readonly")));
      return C;
    }
    function btn(box, cls, icon, label) {
      var x = box.appendChild(rpBtn(cls));
      if (icon) x.appendChild(gIcon(icon));
      x.appendChild(mk("span", null, label));
      return x;
    }
    var act = el.appendChild(mk("div", "apact"));
    var allow = btn(act, "btn allow", "check", wsT("js.ap.allow")), deny = btn(act, "btn deny", "x", wsT("js.ap.deny"));
    deny.setAttribute("aria-expanded", "false");
    var dn = el.appendChild(mk("div", "apdeny"));
    dn.hidden = true;
    var msg = dn.appendChild(mk("textarea", "apmsg"));
    msg.rows = 2;
    msg.placeholder = wsT("js.ap.deny.placeholder");
    msg.setAttribute("aria-label", wsT("js.ap.deny.placeholder"));
    var dr = dn.appendChild(mk("div", "aprow"));
    dr.appendChild(mk("span", "apnote", wsT("js.ap.deny.note")));
    var back = btn(dr, "btn", "", wsT("js.rail.cancel")), send = btn(dr, "btn go deny", "", wsT("js.ap.deny.send"));
    var err = el.appendChild(mk("p", "aperr"));
    err.hidden = true;
    err.setAttribute("aria-live", "polite");
    function sync() { [allow, deny, back, send, msg].forEach(function (x) { x.disabled = C.busy; }); }
    function denying(on) {
      var pin = pinned();
      dn.hidden = !on;
      act.hidden = on;
      deny.setAttribute("aria-expanded", String(on));
      (on ? msg : deny).focus();
      if (pin) (typeof queuePinConvo === "function" ? queuePinConvo() : pinConvo());
    }
    function answer(behavior) {
      if (C.busy) return;
      notifAsk();
      var body = { behavior: behavior }, why = msg.value.trim();
      if (behavior === "deny" && why) body.message = why;
      C.busy = true;
      err.hidden = true;
      sync();
      railPost("/agents/" + encodeURIComponent(rid) + "/approvals/" + encodeURIComponent(String(a.id)), body, function (st, v, s) {
        C.busy = false;
        sync();
        if (st === 200 && v.id) { apAnswered(a, behavior, body.message, el); lead.st = v; leadDraw(); return; }
        if (st === 409) { leadFetch(); return; }
        err.textContent = leadErr(st, s);
        err.hidden = false;
      });
    }
    allow.addEventListener("click", function () { answer("allow"); });
    deny.addEventListener("click", function () { denying(true); });
    back.addEventListener("click", function () { denying(false); });
    send.addEventListener("click", function () { answer("deny"); });
    msg.addEventListener("keydown", function (e) {
      if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && !e.isComposing) { e.preventDefault(); answer("deny"); }
      else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); denying(false); }
    });
    return C;
  }
  // leadDraw shows the read state: the chip, the open session menu, one
  // card per waiting approval (oldest first, numbered when several wait),
  // and, when the harness waits on an answer no card can give, a notice in
  // their place. While the box shows anything, the conversation's own
  // permission line (the same wait, as text) is hidden.
  function leadDraw() {
    var st = lead.st, box = $("apbox");
    leadChip();
    composerSync(st);
    if (rp.P && rp.kind === "sess") {
      if (leadCan()) rp.P.draw();
      else railPopClose(false);
    }
    handedDraw(st);
    harnessDraw(st);
    quotaDraw(st);
    if (!box) return;
    var open = !leadGone(st), pin = pinned(), keep = {}, changed = apMountAnswered() || !!lead.approvalChanged;
    lead.approvalChanged = false;
    var list = open && Array.isArray(st.approvals) ? st.approvals.filter(function (a) { return a && a.id; }) : [];
    var need = open && !list.length && st.action_needed ? String(st.action_needed) : "";
    list.forEach(function (a) { keep[a.id] = true; });
    Object.keys(lead.cards).forEach(function (id) {
      if (keep[id]) return;
      lead.cards[id].el.remove();
      changed = true;
      delete lead.cards[id];
    });
    list.forEach(function (a, i) {
      var c = lead.cards[a.id] || (lead.cards[a.id] = apCard(a, !!st.can_control));
      if (box.children[i] !== c.el) { box.insertBefore(c.el, box.children[i] || null); changed = true; }
      c.n.textContent = (i + 1) + "/" + list.length;
      c.n.hidden = list.length < 2;
    });
    if (need && !lead.need) {
      lead.need = mk("div", "apneed");
      lead.need.appendChild(gIcon("lock"));
      lead.need.appendChild(mk("span", null, wsT("js.ap.needed")));
    }
    if (lead.need) {
      // the daemon's own wording, in English, stays readable on hover
      lead.need.title = need;
      if (need) box.appendChild(lead.need);
      else lead.need.remove();
    }
    box.hidden = !list.length && !need;
    if (convo) convo.classList.toggle("ap-on", !box.hidden);
    fmtTimes(box);
    if (changed) apChanged();
    if (pin) (typeof queuePinConvo === "function" ? queuePinConvo() : pinConvo());
  }
  // handedDraw shows, while st says the session was handed to a terminal,
  // the notice that says so under the banner, with the command that resumes
  // it there and its copy button when the state carries one; any other
  // state takes the notice away. The notice is outside the regions, so an
  // update leaves it.
  var handedEl = null;
  function handedDraw(st) {
    var n = WsRail ? WsRail.handedNotice(st) : null;
    if (!n) { if (handedEl) { handedEl.remove(); handedEl = null; } return; }
    if (handedEl && handedEl.wsKey === n.command) return;
    var banner = $("dash-banner");
    if (!banner) return;
    var el = mk("div", "handed");
    el.wsKey = n.command;
    el.setAttribute("role", "status");
    el.appendChild(luIcon("square-terminal"));
    var body = el.appendChild(mk("div", "hdbody"));
    body.appendChild(mk("span", "hdtext", wsT("js.lead.handed")));
    if (n.command) {
      body.appendChild(mk("span", "hdlbl", wsT("js.lead.handed.resume")));
      var line = body.appendChild(mk("span", "hdcmd"));
      line.appendChild(mk("code", null, n.command));
      var copy = line.appendChild(rpBtn("hdcopy"));
      copy.title = wsT("js.lead.handed.copy");
      copy.setAttribute("aria-label", wsT("js.lead.handed.copy"));
      copy.appendChild(gIcon("copy"));
      copy.addEventListener("click", function () { copyText(n.command); });
    }
    if (handedEl) handedEl.remove();
    banner.parentNode.insertBefore(el, banner.nextSibling);
    handedEl = el;
  }
  // Warn once per distinct harness notice for the lifetime of this page,
  // even when state updates clear a notice and later bring it back.
  var harnessNotices = new Set();
  function harnessDraw(st) {
    var c = !leadGone(st) && st.capabilities, lines = [];
    if (c && c.unverified) {
      var range = c.oldest && c.newest && c.oldest !== c.newest ? c.oldest + "\u2013" + c.newest : String(c.newest || c.oldest || c.profile_version || "");
      lines.push(wsT("js.lead.harness.unverified", c.version ? String(c.version) : wsT("js.lead.harness.unreported"), range, String(c.profile_version || "")));
    }
    if (c && Array.isArray(c.demoted) && c.demoted.length) lines.push(wsT("js.lead.harness.demoted", c.demoted.map(String).join(", ")));
    if (!lines.length || !window.WsToast) return;
    var key = lines.join(" ");
    if (harnessNotices.has(key)) return;
    window.WsToast.show(key, { kind: "warn" });
    harnessNotices.add(key);
  }
  // quotaDraw renders a hosted lead's post-usage-limit continuation status and,
  // only for viewers holding control authority (st.can_control), manual
  // schedule / cancel buttons. Resume timestamps use static <time data-f="dt">
  // with zero countdown timers or idle animations.
  var quotaState = { busy: false, sig: "" };
  function quotaToast(text, kind) {
    if (window.WsToast) window.WsToast.show(text, { kind: kind || "info" });
    else toast(text);
  }
  function quotaAction(kind) {
    if (quotaState.busy || !leadCan()) return;
    quotaState.busy = true;
    quotaDraw(lead.st);
    var url = "/agents/" + encodeURIComponent(rid) + (kind === "cancel" ? "/quota-continuation/cancel" : "/quota-continuation");
    railPost(url, {}, function (st, v, s) {
      quotaState.busy = false;
      if (st === 200 && v && v.id) {
        lead.st = v;
        leadDraw();
        quotaToast(wsT(kind === "cancel" ? "js.quota.cancelled_toast" : "js.quota.scheduled_toast"), "info");
        return;
      }
      if (st === 409) leadFetch();
      quotaToast(leadErr(st, s), "warn");
      leadDraw();
    });
  }
  function quotaDraw(st) {
    var box = $("qcbox");
    if (!box) return;
    if (!st || !st.lead || st.is_child || leadGone(st)) {
      if (!box.hidden || box.childNodes.length) { box.hidden = true; box.textContent = ""; }
      quotaState.sig = "";
      return;
    }
    var qc = st.quota_continuation || null;
    if (qc && qc.status === "in_flight") {
      if (!box.hidden || box.childNodes.length) { box.hidden = true; box.textContent = ""; }
      quotaState.sig = "";
      return;
    }
    var fail = st.failure && st.failure.kind === "usage_limit" ? st.failure : null;
    var can = !!st.can_control;
    var mode = "";
    if (qc && qc.status === "scheduled" && qc.resume_at > 0) mode = "scheduled";
    else if (qc && qc.status === "exhausted") mode = "exhausted";
    else if (fail && can && st.status !== "running") mode = "ready";
    if (!mode) {
      if (!box.hidden || box.childNodes.length) { box.hidden = true; box.textContent = ""; }
      quotaState.sig = "";
      return;
    }
    var sig = JSON.stringify([
      mode, can, quotaState.busy,
      fail ? Number(fail.resetsAt || 0) : 0,
      qc ? [qc.status || "", Number(qc.resume_at || 0), Number(qc.attempts || 0), Number(qc.max_attempts || 0)] : null
    ]);
    if (quotaState.sig === sig && !box.hidden) return;
    quotaState.sig = sig;
    var pin = pinned();
    box.textContent = "";
    var card = mk("div", "qccard qccard-" + mode);
    card.setAttribute("role", "status");
    card.setAttribute("data-qc-mode", mode);
    var body = card.appendChild(mk("div", "qcbody"));
    var hd = body.appendChild(mk("div", "qchd"));
    hd.appendChild(gIcon(mode === "scheduled" ? "clock" : "x"));
    var titleKey = mode === "scheduled" ? "js.quota.title.scheduled" : (mode === "exhausted" ? "js.quota.title.exhausted" : "js.quota.title.ready");
    hd.appendChild(mk("b", "qctitle", wsT(titleKey)));
    if (mode === "scheduled") {
      var meta = hd.appendChild(mk("span", "qcmeta"));
      meta.appendChild(doc.createTextNode(wsT("js.quota.resume_at") + " "));
      var tm = meta.appendChild(mk("time", "t"));
      tm.setAttribute("data-ms", String(qc.resume_at));
      tm.setAttribute("data-f", "dt");
      if (qc.max_attempts > 0) {
        var attText = qc.attempts > 0
          ? wsT("js.quota.attempt", String(qc.attempts), String(qc.max_attempts))
          : wsT("js.quota.fallback_hourly", String(qc.max_attempts));
        hd.appendChild(mk("span", "qcatt", attText));
      }
    } else if (mode === "ready") {
      if (fail && fail.resetsAt > 0) {
        var rmeta = hd.appendChild(mk("span", "qcmeta"));
        rmeta.appendChild(doc.createTextNode(wsT("js.quota.resets_at") + " "));
        var rtm = rmeta.appendChild(mk("time", "t"));
        rtm.setAttribute("data-ms", String(fail.resetsAt));
        rtm.setAttribute("data-f", "dt");
      }
      body.appendChild(mk("div", "qcdesc", wsT("js.quota.ready_hint")));
    } else if (mode === "exhausted") {
      var maxA = Number(qc.max_attempts || 6);
      var attA = Number(qc.attempts || maxA);
      body.appendChild(mk("div", "qcdesc", wsT("js.quota.exhausted_hint", String(attA), String(maxA))));
    }
    if (can) {
      var act = card.appendChild(mk("div", "qcact"));
      if (mode === "scheduled") {
        var cancelBtn = act.appendChild(rpBtn("btn qccancel", wsT("js.quota.cancel")));
        cancelBtn.setAttribute("data-qc-act", "cancel");
        cancelBtn.disabled = quotaState.busy;
        cancelBtn.addEventListener("click", function () { quotaAction("cancel"); });
      } else {
        var schedBtn = act.appendChild(rpBtn("btn go qcsched", wsT("js.quota.schedule")));
        schedBtn.setAttribute("data-qc-act", "schedule");
        schedBtn.disabled = quotaState.busy;
        schedBtn.addEventListener("click", function () { quotaAction("schedule"); });
      }
    }
    box.appendChild(card);
    box.hidden = false;
    fmtTimes(box);
    if (pin) (typeof queuePinConvo === "function" ? queuePinConvo() : pinConvo());
  }
  // leadKey is the conversation's live host/permission state as the state
  // follows it: permission slot, quota state, and turn activity while a
  // usage-limit alert can expose manual scheduling.
  function leadKey() {
    var c = region("convlive"), clive = c && c.querySelector(".clive");
    var quotaFailure = clive && clive.getAttribute("data-qc-fail") || "";
    var slot = c && c.querySelector(".apslot");
    var t = slot && slot.querySelector("time[data-ms]"), code = slot && slot.querySelector("code"), args = slot && slot.querySelector(".args");
    var parts = [
      t ? t.getAttribute("data-ms") : "",
      code ? code.title : "",
      args ? args.textContent : "",
      quotaFailure,
      quotaFailure === "usage_limit" && clive.hasAttribute("data-turn") ? "running" : "",
      clive ? (clive.getAttribute("data-qc-reset") || "") : "",
      clive ? (clive.getAttribute("data-qc-status") || "") : "",
      clive ? (clive.getAttribute("data-qc-resume") || "") : "",
      clive ? (clive.getAttribute("data-qc-att") || "") : ""
    ];
    return parts.join("\n");
  }
  // leadSwapped runs after an update of a region the state follows.
  function leadSwapped(name) {
    if (name === "head") {
      document.querySelectorAll("[data-child-close]").forEach(function (b) {
        if (subagentClosing[b.getAttribute("data-reply-id")] === "closed") markChildCloseButton(b);
      });
    }
    if (handedPage && !hostedPage) {
      if (name === "head") leadFetch();
      return;
    }
    if (!hostedPage) return;
    if (name === "head") {
      leadChip();
      if (rp.P && rp.kind === "sess") railPopSync();
      leadFetch();
      return;
    }
    var key = leadKey();
    if (key === lead.key) return;
    lead.key = key;
    leadFetch();
  }

  // --- composer footer 2-control strip (Model + Effort only, decision (38)) --
  var composerPills = $("composer-pills"), composerPop = { slot: "", portal: null, busy: false };
  function composerHarness() {
    if (lead.st && lead.st.backend) return String(lead.st.backend);
    var h = region("head"), el = h && h.querySelector(".host.sse");
    return el ? el.textContent.trim() : "";
  }
  function composerHarnessLabel(k) {
    if (k === "claude") return "Claude";
    if (k === "codex") return "Codex";
    if (k === "agy") return "AGY";
    return k || "";
  }
  function composerCurrent(slot) {
    if (lead.st) return String(slot === "model" ? (lead.st.model || "") : (lead.st.effort || ""));
    var btn = composerPills && composerPills.querySelector('[data-pop="' + slot + '"]');
    if (!btn) return "";
    var v = btn.getAttribute("data-val");
    if (v !== null) return v;
    var pv = btn.querySelector(".pv"), txt = pv ? pv.textContent.trim() : "";
    return txt === wsT("js.sup.default") ? "" : txt;
  }
  function composerEnsurePortal() {
    if (composerPop.portal) return composerPop.portal;
    var el = mk("div", "fpop");
    el.id = "fpop-portal";
    el.setAttribute("role", "listbox");
    el.hidden = true;
    doc.body.appendChild(el);
    composerPop.portal = el;
    return el;
  }
  function composerPlacePop() {
    var pop = composerPop.portal;
    if (!pop || pop.hidden || !composerPop.slot || !composerPills) return;
    var btn = composerPills.querySelector('[data-pop="' + composerPop.slot + '"]');
    if (!btn) return;
    var r = btn.getBoundingClientRect(), pw = pop.offsetWidth, ph = pop.offsetHeight;
    var left = Math.max(8, Math.min(r.left, window.innerWidth - pw - 8));
    var top = r.top - ph - 6;
    if (top < 8) top = Math.min(window.innerHeight - ph - 8, r.bottom + 6);
    pop.style.left = left + "px";
    pop.style.top = Math.max(8, top) + "px";
  }
  function composerClosePop(restoreFocus) {
    var was = composerPop.slot;
    composerPop.slot = "";
    if (composerPills) q("[data-pop]", composerPills).forEach(function (b) { b.setAttribute("aria-expanded", "false"); });
    if (composerPop.portal) { composerPop.portal.hidden = true; composerPop.portal.removeAttribute("data-sig"); composerPop.portal.textContent = ""; }
    if (restoreFocus && was && composerPills) {
      var btn = composerPills.querySelector('[data-pop="' + was + '"]');
      if (btn && !btn.disabled) btn.focus();
    }
  }
  function composerDrawPop() {
    var slot = composerPop.slot;
    if (!slot || !composerPills) return;
    var pop = composerEnsurePortal(), harness = composerHarness(), cur = composerCurrent(slot);
    var provider = lead.st && lead.st.provider;
    var list = slot === "model" ? leadModels(harness, provider) : leadEfforts(harness);
    var labels = slot === "model" ? leadModelLabels(harness, provider, false) : {};
    var all = list.slice();
    if (cur && all.indexOf(cur) < 0) all.push(cur);
    var sig = JSON.stringify([slot, harness, cur, all, labels]);
    if (!pop.hidden && pop.getAttribute("data-sig") === sig) {
      composerPlacePop();
      return;
    }
    pop.setAttribute("data-sig", sig);
    pop.textContent = "";
    var hdText = (composerHarnessLabel(harness) ? composerHarnessLabel(harness) + " " : "") + wsT(slot === "model" ? "js.sup.model" : "js.sup.effort");
    pop.appendChild(mk("div", "fpop-hd", hdText));
    var opts = (slot === "model" && harness === "werk" ? [] : [{ val: "", label: wsT("js.sup.default") }]).concat(all.map(function (v) { return { val: v, label: labels[v] || v }; }));
    opts.forEach(function (o) {
      var opt = mk("button", "fpop-opt");
      opt.type = "button";
      opt.setAttribute("role", "option");
      opt.setAttribute("data-slot", slot);
      opt.setAttribute("data-val", o.val);
      opt.setAttribute("aria-selected", String(o.val === cur));
      opt.appendChild(mk("span", null, o.label));
      pop.appendChild(opt);
    });
    pop.hidden = false;
    composerPlacePop();
  }
  function composerPick(slot, val) {
    if (composerPop.busy) return;
    var body = {};
    body[slot] = val;
    composerPop.busy = true;
    composerClosePop(true);
    composerSync(lead.st);
    railPost("/agents/" + encodeURIComponent(rid) + "/settings", body, function (st, v, s) {
      composerPop.busy = false;
      if (st === 200 && v.id) { lead.st = v; lead.gone = false; leadDraw(); return; }
      toast(leadErr(st, s));
      leadDraw();
      leadFetch();
    });
  }
  function composerSync(st) {
    if (!composerPills) return;
    if ((st && leadGone(st)) || (!st && lead.gone)) {
      composerPills.hidden = true;
      composerClosePop(false);
      return;
    }
    composerPills.hidden = false;
    var isChild = !!(root.hasAttribute("data-child") || (st && st.is_child));
    var canEdit = !isChild && !composerPop.busy && (st ? !!st.can_control : root.hasAttribute("data-can-edit-settings"));
    [["model", "js.lead.controls.model"], ["effort", "js.lead.controls.effort"]].forEach(function (pair) {
      var slot = pair[0], tipKey = pair[1], btn = composerPills.querySelector('[data-pop="' + slot + '"]');
      if (!btn) return;
      if (st) {
        var raw = String(slot === "model" ? (st.model || "") : (st.effort || ""));
        btn.setAttribute("data-val", raw);
        var pv = btn.querySelector(".pv");
        if (pv) pv.textContent = raw || wsT("js.sup.default");
      }
      btn.disabled = !canEdit;
      btn.title = isChild ? wsT("js.lead.controls.child") : (!canEdit && !(st ? false : composerPop.busy) ? wsT("js.lead.controls.host") : wsT(tipKey));
      if (!canEdit && composerPop.busy && !isChild && (st ? !!st.can_control : root.hasAttribute("data-can-edit-settings"))) btn.title = wsT(tipKey);
      var tail = btn.lastElementChild, wantLock = isChild;
      if (tail && tail.classList.contains("lock") !== wantLock) {
        var nextTail = luIcon(wantLock ? "lock" : "chevron-down");
        nextTail.setAttribute("class", "i li " + (wantLock ? "lock" : "chev"));
        btn.replaceChild(nextTail, tail);
      }
    });
    if (composerPop.slot) {
      if (!canEdit) composerClosePop(false);
      else composerDrawPop();
    }
  }
  if (composerPills) {
    composerPills.addEventListener("click", function (e) {
      var btn = e.target && e.target.closest && e.target.closest("[data-pop]");
      if (!btn || btn.disabled) return;
      var slot = btn.getAttribute("data-pop");
      if (composerPop.slot === slot) { composerClosePop(true); return; }
      composerClosePop(false);
      composerPop.slot = slot;
      btn.setAttribute("aria-expanded", "true");
      composerDrawPop();
      leadChoices(composerDrawPop, composerDrawPop, function () { return composerPop.slot === slot; });
    });
    doc.addEventListener("click", function (e) {
      if (!composerPop.slot) return;
      var t = e.target;
      if (t && t.closest) {
        var opt = t.closest("#fpop-portal .fpop-opt");
        if (opt) { composerPick(opt.getAttribute("data-slot"), opt.getAttribute("data-val")); return; }
        if (t.closest("#fpop-portal") || t.closest("#composer-pills")) return;
      }
      composerClosePop(false);
    });
    doc.addEventListener("keydown", function (e) {
      if (!composerPop.slot) return;
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); composerClosePop(true); return; }
      if ((e.key === "ArrowDown" || e.key === "ArrowUp") && composerPop.portal) {
        var opts = q(".fpop-opt", composerPop.portal);
        if (!opts.length) return;
        e.preventDefault();
        var idx = opts.indexOf(doc.activeElement);
        var next = e.key === "ArrowDown" ? (idx + 1) % opts.length : (idx <= 0 ? opts.length - 1 : idx - 1);
        opts[next].focus();
      }
    });
    window.addEventListener("resize", composerPlacePop);
  }

