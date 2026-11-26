// User commands v2: compact always-visible .cbadges trigger button beside
// #sbadge, floating borderless popover above #composer, two-zone tiles keyed
// by source+id (header band: scope, inline-editable name, actions; body well:
// a one-line $ preview that expands into the bash-highlighted overlay editor
// from dashboard-composer-ucmd-editor.js, plus the prompt note), 1s
// press-and-hold confirmation (WsHoldConfirm) for Run / Ask Agent / Delete,
// single-click Save / Revert, inline Create form, and direct
// composer-preserving Ask Agent dispatch.
  var ucmdEnabled = cexecEnabled;
  var ucmdExpanded = "", ucmdFormLine = null;
  var ucmdList = [], ucmdRev = "", ucmdDirty = false, ucmdDenied = false;
  var ucmdOpen = false, ucmdAdding = false, ucmdFetching = false;
  var ucmdBusy = Object.create(null), ucmdDrafts = Object.create(null);
  var ucmdByKey = Object.create(null), ucmdNodes = Object.create(null);
  var ucmdBadge = null, ucmdBadgeCnt = null, ucmdPop = null;
  var ucmdCountEl = null, ucmdSubEl = null, ucmdAddBtn = null;
  var ucmdForm = null, ucmdFormErr = null, ucmdFormSave = null;
  var ucmdEmptyEl = null, ucmdListEl = null;
  var ucmdAttemptKey = "ws.ucmd.attempt." + rid, ucmdGuardMs = 600;
  var UCMD_HOLD_MS = 1000, ucmdHold = null, ucmdHoldControl = null;
  var UCMD_SUB_HOLDING = {
    run: "js.ucmd.sub.holding.run",
    ask: "js.ucmd.sub.holding.ask",
    del: "js.ucmd.sub.holding.del"
  };
  var UCMD_SUB_READY = {
    run: "js.ucmd.sub.ready.run",
    ask: "js.ucmd.sub.ready.ask",
    del: "js.ucmd.sub.ready.del"
  };
  var UCMD_EARLY = {
    run: "js.ucmd.hold.early.run",
    ask: "js.ucmd.hold.early.ask",
    del: "js.ucmd.hold.early.del"
  };

  function ucmdKey(c) {
    return (c && c.source ? c.source : "agent") + ":" + (c ? c.id : "");
  }
  // The attempt in flight per command (submission id, displayed revision,
  // effective line and cwd) is stored in sessionStorage and cleared once the
  // daemon settles it. Keyed by source:id so agent and user scopes never collide.
  function ucmdAttempts() {
    try {
      var a = JSON.parse(window.sessionStorage.getItem(ucmdAttemptKey) || "{}");
      return Object.assign(Object.create(null), a && typeof a === "object" ? a : {});
    } catch (e) { return Object.create(null); }
  }
  function ucmdSaveAttempts(a) {
    try { window.sessionStorage.setItem(ucmdAttemptKey, JSON.stringify(a)); } catch (e) {}
  }

  function ucmdSetSub(i18nKey, cls) {
    if (!ucmdSubEl) return;
    ucmdSubEl.textContent = wsT(i18nKey || "js.ucmd.sub.default");
    ucmdSubEl.className = "ucmd-pop-sub" + (cls ? " " + cls : "");
  }
  function ucmdResetSub() { ucmdSetSub("js.ucmd.sub.default", ""); }

  function ucmdSyncHud() {
    if (typeof xhLayout === "function") xhLayout();
  }
  function ucmdHoldBusy() {
    return !!(window.WsHoldConfirm && (
      (typeof window.WsHoldConfirm.isHolding === "function" && window.WsHoldConfirm.isHolding()) ||
      (typeof window.WsHoldConfirm.wasJustCancelled === "function" && window.WsHoldConfirm.wasJustCancelled())
    ));
  }

  function ucmdBuildForm() {
    var form = doc.createElement("form");
    form.className = "ucmd-add-form";
    form.hidden = true;
    form.noValidate = true;

    var hd = doc.createElement("div");
    hd.className = "ucmd-form-hd";
    var ht = doc.createElement("span");
    ht.textContent = wsT("js.ucmd.form.title");
    var hs = doc.createElement("span");
    hs.className = "ucmd-form-sub";
    hs.textContent = wsT("js.ucmd.form.sub");
    hd.appendChild(ht);
    hd.appendChild(hs);
    form.appendChild(hd);

    function makeField(name, labelText, opt, ph, kind, maxLen) {
      var wrap = doc.createElement("label");
      wrap.className = "ucmd-field";
      var lbl = doc.createElement("span");
      lbl.className = "ucmd-lbl";
      lbl.textContent = labelText;
      if (opt) {
        var o = doc.createElement("span");
        o.className = "ucmd-lbl-opt";
        o.textContent = wsT("js.ucmd.form.opt");
        lbl.appendChild(o);
      }
      var inp = doc.createElement(kind === "ta" || kind === "code" ? "textarea" : "input");
      if (kind === "ta") {
        inp.className = "ucmd-ta";
        inp.rows = 2;
      } else if (kind === "code") {
        inp.className = "ucmd-form-code";
      } else {
        inp.type = "text";
        inp.className = kind === "mono" ? "ucmd-input is-mono" : "ucmd-input";
      }
      inp.setAttribute("data-f", name);
      inp.placeholder = ph;
      inp.maxLength = maxLen;
      inp.autocomplete = "off";
      inp.spellcheck = false;
      wrap.appendChild(lbl);
      if (kind !== "code") {
        wrap.appendChild(inp);
        return wrap;
      }
      // The new command's shell line uses the same highlighted editor as
      // the tiles; Enter submits, Shift+Enter adds a line.
      var ed = ucmdFormLine = ucmdCodeEditor(inp);
      inp.addEventListener("input", function () { ucmdCodePaint(ed); });
      inp.addEventListener("keydown", function (ev) {
        if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) {
          ev.preventDefault();
          ucmdCreateSubmit();
        }
      });
      ucmdCodePaint(ed);
      wrap.appendChild(ed.wrap);
      return wrap;
    }

    var grid = doc.createElement("div");
    grid.className = "ucmd-grid2";
    grid.appendChild(makeField("label", wsT("js.ucmd.form.label"), false, wsT("js.ucmd.form.label.ph"), "text", 80));
    grid.appendChild(makeField("cwd", wsT("js.ucmd.form.cwd"), true, wsT("js.ucmd.form.cwd.ph"), "mono", 256));
    form.appendChild(grid);
    form.appendChild(makeField("line", wsT("js.ucmd.form.line"), false, wsT("js.ucmd.form.line.ph"), "code", 2048));
    form.appendChild(makeField("prompt", wsT("js.ucmd.form.prompt"), true, wsT("js.ucmd.form.prompt.ph"), "ta", 2048));

    ucmdFormErr = doc.createElement("div");
    ucmdFormErr.className = "ucmd-form-err";
    ucmdFormErr.setAttribute("role", "alert");
    ucmdFormErr.hidden = true;
    form.appendChild(ucmdFormErr);

    var acts = doc.createElement("div");
    acts.className = "ucmd-form-acts";
    var cancel = doc.createElement("button");
    cancel.type = "button";
    cancel.className = "ucmd-btn-ghost";
    cancel.textContent = wsT("js.ucmd.form.cancel");
    cancel.addEventListener("click", function () { ucmdSetAdding(false); });
    ucmdFormSave = doc.createElement("button");
    ucmdFormSave.type = "submit";
    ucmdFormSave.className = "ucmd-btn-save";
    ucmdFormSave.textContent = wsT("js.ucmd.form.save");
    acts.appendChild(cancel);
    acts.appendChild(ucmdFormSave);
    form.appendChild(acts);

    form.addEventListener("submit", function (ev) {
      ev.preventDefault();
      ucmdCreateSubmit();
    });
    return form;
  }

  function ucmdEnsureUI() {
    if (ucmdBadge || !composer || !ucmdEnabled) return;

    // 1) Always-visible trigger button inserted directly after #sbadge.
    ucmdBadge = doc.createElement("button");
    ucmdBadge.type = "button";
    ucmdBadge.id = "ucmdbadge";
    ucmdBadge.className = "ucmd-trigger is-empty";
    ucmdBadge.setAttribute("aria-expanded", "false");
    ucmdBadge.setAttribute("aria-haspopup", "dialog");
    ucmdBadge.setAttribute("aria-label", wsT("js.ucmd.trigger.title", "0"));
    ucmdBadge.title = wsT("js.ucmd.trigger.title", "0");
    ucmdBadge.appendChild(ucmdTermIcon(13));
    ucmdBadgeCnt = doc.createElement("span");
    ucmdBadgeCnt.className = "ucmd-trigger-cnt";
    ucmdBadge.appendChild(ucmdBadgeCnt);
    ucmdBadge.addEventListener("click", function () {
      if (ucmdOpen) ucmdClosePopover();
      else ucmdOpenPopover();
    });

    var wrap = composer.querySelector(".cbadges .sbwrap") || composer.querySelector(".cbadges");
    if (wrap) {
      var sb = $("sbadge");
      wrap.insertBefore(ucmdBadge, sb && sb.parentNode === wrap ? sb.nextSibling : null);
    }
    if (typeof xhRo !== "undefined" && xhRo) xhRo.observe(ucmdBadge);

    // 2) Floating borderless popover anchored above #composer.
    ucmdPop = doc.createElement("div");
    ucmdPop.className = "ucmd-pop";
    ucmdPop.setAttribute("role", "dialog");
    ucmdPop.setAttribute("aria-label", wsT("js.ucmd.aria"));
    ucmdPop.hidden = true;

    var hd = doc.createElement("div");
    hd.className = "ucmd-pop-hd";
    var titleWrap = doc.createElement("span");
    titleWrap.className = "ucmd-pop-title";
    var ico = doc.createElement("span");
    ico.className = "ucmd-pop-ico";
    ico.appendChild(ucmdTermIcon(14));
    var titleTxt = doc.createElement("span");
    titleTxt.textContent = wsT("js.ucmd.title");
    ucmdCountEl = doc.createElement("span");
    ucmdCountEl.className = "ucmd-count";
    titleWrap.appendChild(ico);
    titleWrap.appendChild(titleTxt);
    titleWrap.appendChild(ucmdCountEl);

    ucmdSubEl = doc.createElement("span");
    ucmdSubEl.className = "ucmd-pop-sub";
    ucmdResetSub();

    ucmdAddBtn = doc.createElement("button");
    ucmdAddBtn.type = "button";
    ucmdAddBtn.className = "ucmd-add-btn";
    ucmdAddBtn.textContent = wsT("js.ucmd.add");
    ucmdAddBtn.addEventListener("click", function () { ucmdSetAdding(!ucmdAdding); });

    var closeBtn = doc.createElement("button");
    closeBtn.type = "button";
    closeBtn.className = "ucmd-close-btn";
    closeBtn.textContent = "×";
    closeBtn.title = wsT("js.ucmd.close");
    closeBtn.setAttribute("aria-label", wsT("js.ucmd.close"));
    closeBtn.addEventListener("click", function () { ucmdClosePopover(true); });

    hd.appendChild(titleWrap);
    hd.appendChild(ucmdSubEl);
    hd.appendChild(ucmdAddBtn);
    hd.appendChild(closeBtn);
    ucmdPop.appendChild(hd);

    var body = doc.createElement("div");
    body.className = "ucmd-pop-body";

    ucmdForm = ucmdBuildForm();
    body.appendChild(ucmdForm);

    ucmdEmptyEl = doc.createElement("div");
    ucmdEmptyEl.className = "ucmd-empty";
    var emptyText = doc.createElement("div");
    var emptyStrong = doc.createElement("strong");
    emptyStrong.textContent = wsT("js.ucmd.empty");
    var emptySub = doc.createElement("span");
    emptySub.className = "ucmd-empty-sub";
    emptySub.textContent = wsT("js.ucmd.empty.sub");
    emptyText.appendChild(emptyStrong);
    emptyText.appendChild(emptySub);
    var emptyCta = doc.createElement("button");
    emptyCta.type = "button";
    emptyCta.className = "ucmd-add-btn";
    emptyCta.textContent = wsT("js.ucmd.add");
    emptyCta.addEventListener("click", function () { ucmdSetAdding(true); });
    ucmdEmptyEl.appendChild(emptyText);
    ucmdEmptyEl.appendChild(emptyCta);
    body.appendChild(ucmdEmptyEl);

    ucmdListEl = doc.createElement("div");
    ucmdListEl.className = "ucmd-list";
    ucmdListEl.setAttribute("role", "list");
    ucmdListEl.setAttribute("aria-label", wsT("js.ucmd.aria"));
    body.appendChild(ucmdListEl);

    ucmdPop.appendChild(body);
    composer.appendChild(ucmdPop);

    if (window.WsHoldConfirm && typeof window.WsHoldConfirm.delegate === "function") {
      ucmdHoldControl = window.WsHoldConfirm.delegate(ucmdListEl, ".ucmd-ibtn.is-hold", {
        mode: "radial",
        duration: UCMD_HOLD_MS,
        canStart: function (btn) {
          if (btn.disabled) return false;
          var key = btn.getAttribute("data-ucmd-key") || "";
          return !!ucmdByKey[key] && !ucmdBusy[key];
        },
        onStart: function (btn) {
          var act = btn.getAttribute("data-act") || "";
          var key = btn.getAttribute("data-ucmd-key") || "";
          var c = ucmdByKey[key], eff = ucmdEffective(c);
          ucmdHold = { key: key, act: act, command: c, effective: eff };
          if (act === "ask") ucmdHold.text = ucmdAskText(c, eff);
          ucmdSetSub(UCMD_SUB_HOLDING[act], "is-holding-" + act);
        },
        onReady: function (btn) {
          var act = btn.getAttribute("data-act") || "";
          ucmdSetSub(UCMD_SUB_READY[act], "is-holding-" + act);
        },
        onCancel: function (btn, reason) {
          ucmdHold = null;
          ucmdResetSub();
          if (reason === "early-release") {
            var act = btn.getAttribute("data-act") || "";
            if (UCMD_EARLY[act]) noteWarn(wsT(UCMD_EARLY[act]));
          }
        },
        onConfirm: function () {
          var hold = ucmdHold;
          ucmdHold = null;
          ucmdResetSub();
          if (!hold || !ucmdHoldCurrent(hold)) return;
          if (hold.act === "run") ucmdRun(hold.command, hold.effective);
          else if (hold.act === "ask") ucmdAsk(hold.command, hold.effective, hold.text);
          else if (hold.act === "del") ucmdRemove(hold.command);
        }
      });
    }

    // A press anywhere in the popover outside the open $ editor folds it back
    // to its one-line preview. Action buttons are exempt so a hold never
    // shifts the tiles under the pointer.
    ucmdPop.addEventListener("mousedown", function (ev) {
      var node = ucmdExpanded && ucmdNodes[ucmdExpanded], t = ev.target;
      if (!node || node.cmdBox.contains(t)) return;
      if (t && typeof t.closest === "function" && t.closest(".ucmd-ibtn")) return;
      ucmdCollapse(false);
    });
    doc.addEventListener("mousedown", function (ev) {
      if (!ucmdOpen || ucmdHoldBusy()) return;
      if (ucmdPop.contains(ev.target) || ucmdBadge.contains(ev.target)) return;
      ucmdClosePopover(false);
    });
    doc.addEventListener("keydown", function (ev) {
      if (!ucmdOpen || ev.key !== "Escape" || ev.isComposing) return;
      if (ucmdHoldBusy()) return;
      ev.preventDefault();
      ev.stopPropagation();
      if (ucmdExpanded) ucmdCollapse(true);
      else if (ucmdAdding) ucmdSetAdding(false);
      else ucmdClosePopover(true);
    });
    ucmdSyncHud();
  }

  function ucmdOpenPopover() {
    if (!ucmdEnabled || ucmdDenied) return;
    ucmdEnsureUI();
    if (!ucmdPop) return;
    ucmdOpen = true;
    ucmdPop.hidden = false;
    ucmdBadge.setAttribute("aria-expanded", "true");
    ucmdResetSub();
    ucmdRender();
  }
  function ucmdClosePopover(refocus) {
    if (!ucmdOpen) return;
    if (ucmdHoldControl) ucmdHoldControl.cancel();
    ucmdCollapse(false);
    ucmdOpen = false;
    ucmdAdding = false;
    if (ucmdPop) ucmdPop.hidden = true;
    if (ucmdForm) ucmdForm.hidden = true;
    if (ucmdAddBtn) ucmdAddBtn.classList.remove("is-active");
    if (ucmdBadge) {
      ucmdBadge.setAttribute("aria-expanded", "false");
      if (refocus) ucmdBadge.focus();
    }
    ucmdResetSub();
  }
  function ucmdSetAdding(on) {
    ucmdAdding = !!on;
    if (ucmdForm) ucmdForm.hidden = !ucmdAdding;
    if (ucmdAddBtn) ucmdAddBtn.classList.toggle("is-active", ucmdAdding);
    if (ucmdFormErr) { ucmdFormErr.hidden = true; ucmdFormErr.textContent = ""; }
    ucmdSyncEmpty();
    if (ucmdAdding && ucmdForm) {
      var first = ucmdForm.querySelector('[data-f="label"]');
      if (first) first.focus();
    }
  }
  function ucmdSyncEmpty() {
    if (!ucmdEmptyEl || !ucmdListEl) return;
    var empty = !ucmdList.length;
    ucmdEmptyEl.hidden = !empty || ucmdAdding;
    ucmdListEl.hidden = empty;
  }

  // ucmdSaved is a field's stored value as its editor shows it before any
  // edit; a command without a label shows (and renames from) its id.
  function ucmdSaved(c, field) {
    if (field === "label") return c.label || c.id || "";
    return c[field] || "";
  }
  function ucmdEffective(c) {
    var draft = ucmdDrafts[ucmdKey(c)] || {};
    var savedLine = c.line || "", savedPrompt = c.prompt || "", savedLabel = ucmdSaved(c, "label");
    var line = draft.line === undefined ? savedLine : draft.line;
    var prompt = draft.prompt === undefined ? savedPrompt : draft.prompt;
    var label = draft.label === undefined ? savedLabel : draft.label;
    var cmdDirty = line !== savedLine, prDirty = prompt !== savedPrompt, lbDirty = label !== savedLabel;
    // anyDirty marks the Ask/Run payload as edited (line or prompt); a pending
    // rename only needs Save, so saveDirty adds the label.
    return {
      line: line, prompt: prompt, label: label,
      cmdDirty: cmdDirty, prDirty: prDirty, lbDirty: lbDirty,
      anyDirty: cmdDirty || prDirty, saveDirty: cmdDirty || prDirty || lbDirty
    };
  }

  function ucmdHoldCurrent(hold) {
    var c = ucmdByKey[hold.key];
    if (!c || c.revision !== hold.command.revision || ucmdBusy[hold.key]) return false;
    if (hold.act === "del") return true;
    var eff = ucmdEffective(c);
    if (hold.act === "ask") return ucmdAskText(c, eff) === hold.text;
    return eff.line === hold.effective.line && eff.cmdDirty === hold.effective.cmdDirty && c.cwd === hold.command.cwd;
  }

  function ucmdCheckHold() {
    if (ucmdHold && !ucmdHoldCurrent(ucmdHold)) ucmdHoldControl.cancel();
  }

  // Editors normalize line endings for display. Keep untouched fields in the
  // stored representation and record only individual fields the user changed.
  function ucmdEditorValue(value, multi) {
    return multi ? value.replace(/\r\n?/g, "\n") : value.replace(/[\r\n]/g, "");
  }

  function ucmdEdit(key, field, value, multi) {
    var c = ucmdByKey[key];
    if (!c || ucmdBusy[key] === "save") return;
    var draft = ucmdDrafts[key] || {};
    if (value === ucmdEditorValue(ucmdSaved(c, field), multi)) delete draft[field];
    else draft[field] = value;
    if (draft.line === undefined && draft.prompt === undefined && draft.label === undefined) delete ucmdDrafts[key];
    else ucmdDrafts[key] = draft;
    ucmdSyncDirty(key);
    ucmdCheckHold();
  }

  function ucmdRender() {
    if (!ucmdEnabled) return;
    if (ucmdDenied) {
      if (ucmdBadge) ucmdBadge.hidden = true;
      ucmdClosePopover(false);
      ucmdSyncHud();
      return;
    }
    ucmdEnsureUI();
    if (!ucmdBadge) return;
    ucmdBadge.hidden = false;

    var count = ucmdList.length;
    ucmdBadge.classList.toggle("is-empty", count === 0);
    ucmdBadgeCnt.textContent = count > 0 ? String(count) : "";
    var trigTitle = wsT("js.ucmd.trigger.title", String(count));
    ucmdBadge.title = trigTitle;
    ucmdBadge.setAttribute("aria-label", trigTitle);
    ucmdCountEl.textContent = wsT("js.ucmd.count", String(count));

    var seen = Object.create(null);
    ucmdByKey = Object.create(null);
    for (var i = 0; i < count; i++) {
      var c = ucmdList[i];
      var key = ucmdKey(c);
      seen[key] = true;
      ucmdByKey[key] = c;
    }
    ucmdCheckHold();
    Object.keys(ucmdDrafts).forEach(function (k) { if (!seen[k]) delete ucmdDrafts[k]; });
    Object.keys(ucmdNodes).forEach(function (k) {
      if (!seen[k]) {
        if (ucmdExpanded === k) ucmdExpanded = "";
        if (ucmdNodes[k].el.parentNode) ucmdNodes[k].el.parentNode.removeChild(ucmdNodes[k].el);
        delete ucmdNodes[k];
      }
    });

    // Tiles are patched in place by key: single- and multi-line commands
    // share one editor, so a line gaining a newline keeps focus and expansion.
    for (var j = 0; j < count; j++) {
      var cmd = ucmdList[j];
      var k = ucmdKey(cmd);
      var node = ucmdNodes[k];
      if (!node) {
        node = ucmdBuildCard(cmd);
        ucmdNodes[k] = node;
      }
      ucmdUpdateCard(node, cmd);
      var at = ucmdListEl.children[j];
      if (at !== node.el) {
        if (at) ucmdListEl.insertBefore(node.el, at);
        else ucmdListEl.appendChild(node.el);
      }
    }
    ucmdSyncEmpty();
    ucmdSyncHud();
  }

  // The list revision is "epoch.N.rootTag": N only grows within one daemon run
  // (epoch) and one worktree (rootTag). ucmdCompare orders rev against the one on
  // screen: -1 older, 0 equal, 1 newer, and null when they are not comparable.
  function ucmdCompare(rev) {
    if (typeof rev !== "string" || !ucmdRev) return null;
    var a = rev.split("."), b = ucmdRev.split(".");
    if (a.length < 2 || b.length < 2 || a[0] !== b[0] || a.slice(2).join(".") !== b.slice(2).join(".")) return null;
    var x = parseInt(a[1], 10), y = parseInt(b[1], 10);
    if (x !== x || y !== y) return null;
    return x < y ? -1 : x > y ? 1 : 0;
  }
  function ucmdOlder(rev) { return ucmdCompare(rev) === -1; }
  function ucmdApply(list, post) {
    if (typeof list.token === "string" && list.token && !cexecToken) cexecToken = list.token;
    var order = ucmdCompare(list.rev);
    if (post ? order !== 0 && order !== 1 : order === -1) {
      if (post) {
        ucmdRender();
        ucmdRefresh();
      }
      return;
    }
    ucmdDenied = false;
    ucmdList = list.commands || [];
    ucmdRev = typeof list.rev === "string" ? list.rev : ucmdRev;
    ucmdRender();
    if (typeof bangHistSyncBinding === "function") bangHistSyncBinding();
  }
  function ucmdRefresh() {
    if (ucmdFetching) ucmdDirty = true;
    else ucmdFetch();
  }
  function ucmdFetch() {
    if (!ucmdEnabled || ucmdFetching) return;
    ucmdFetching = true;
    ucmdDirty = false;
    fetch("/s/" + rid + "/user-commands", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
      if (r.status === 401 || r.status === 403 || r.status === 404 || r.status === 405) return { denied: true };
      if (!r.ok) return null;
      return r.json();
    }).catch(function () { return null; }).then(function (list) {
      ucmdFetching = false;
      if (list && list.denied) {
        ucmdDenied = true;
        ucmdList = [];
        ucmdRev = "";
        ucmdRender();
        if (typeof bangHistSyncBinding === "function") bangHistSyncBinding();
      } else if (list && Array.isArray(list.commands)) {
        ucmdApply(list, false);
      }
      if (ucmdDirty) ucmdFetch();
    });
  }
  function ucmdWake(rev) {
    if (!ucmdEnabled || typeof rev !== "string" || rev === ucmdRev || ucmdOlder(rev)) return;
    ucmdRefresh();
  }

  function ucmdParseError(s, status) {
    var body = null;
    try { body = JSON.parse(s); } catch (e) { body = null; }
    var msg = (body && typeof body.error === "string" && body.error) ? body.error : (s.trim() || "HTTP " + status);
    return { body: body, msg: msg };
  }

  function ucmdCreateSubmit() {
    if (!ucmdForm || !ucmdFormSave || ucmdFormSave.disabled) return;
    var fLabel = ucmdForm.querySelector('[data-f="label"]');
    var fCwd = ucmdForm.querySelector('[data-f="cwd"]');
    var fLine = ucmdForm.querySelector('[data-f="line"]');
    var fPrompt = ucmdForm.querySelector('[data-f="prompt"]');
    var label = (fLabel.value || "").trim();
    var line = (fLine.value || "").trim();
    var cwd = (fCwd.value || "").trim();
    var prompt = fPrompt.value;
    if (!label || !line) {
      ucmdFormErr.textContent = wsT("js.ucmd.form.req");
      ucmdFormErr.hidden = false;
      (label ? fLine : fLabel).focus();
      return;
    }
    ucmdFormErr.hidden = true;
    function setSaving(on) {
      ucmdFormSave.disabled = on;
      fLabel.readOnly = fCwd.readOnly = fLine.readOnly = fPrompt.readOnly = on;
    }
    setSaving(true);
    fetch("/s/" + rid + "/user-commands", {
      method: "POST", credentials: "same-origin", cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label: label, line: line, cwd: cwd, prompt: prompt })
    }).then(function (r) {
      return r.text().then(function (s) { return { r: r, s: s }; }, function () { return { r: r, s: "" }; });
    }).then(function (x) {
      setSaving(false);
      var parsed = ucmdParseError(x.s, x.r.status);
      if (x.r.ok && parsed.body && Array.isArray(parsed.body.commands)) {
        fLabel.value = "";
        fCwd.value = "";
        fLine.value = "";
        fPrompt.value = "";
        if (ucmdFormLine) ucmdCodePaint(ucmdFormLine);
        ucmdSetAdding(false);
        var before = Object.assign(Object.create(null), ucmdNodes);
        ucmdApply(parsed.body, true);
        noteOk(wsT("js.ucmd.created", label));
        // The new tile opens with its command expanded and focused.
        var fresh = Object.keys(ucmdNodes).filter(function (k) { return !before[k]; })[0];
        if (fresh && ucmdOpen) ucmdExpand(fresh, true);
        return;
      }
      ucmdFormErr.textContent = parsed.msg;
      ucmdFormErr.hidden = false;
    }, function () {
      setSaving(false);
      ucmdFormErr.textContent = wsT("js.post.unreachable");
      ucmdFormErr.hidden = false;
    });
  }

  function ucmdSave(c) {
    var key = ucmdKey(c);
    if (ucmdBusy[key]) return;
    var eff = ucmdEffective(c);
    if (!eff.line.trim()) {
      noteWarn(wsT("js.ucmd.save.empty"));
      return;
    }
    if (eff.lbDirty && !eff.label.trim()) {
      noteWarn(wsT("js.ucmd.label.empty"));
      return;
    }
    if (!eff.saveDirty) return;
    var patch = { source: c.source || "agent", revision: c.revision };
    if (eff.lbDirty) patch.label = eff.label.trim();
    if (eff.cmdDirty) patch.line = eff.line;
    if (eff.prDirty) patch.prompt = eff.prompt;
    ucmdBusy[key] = "save";
    ucmdRender();
    fetch("/s/" + rid + "/user-commands/" + encodeURIComponent(c.id), {
      method: "PUT", credentials: "same-origin", cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch)
    }).then(function (r) {
      return r.text().then(function (s) { return { r: r, s: s }; }, function () { return { r: r, s: "" }; });
    }).then(function (x) {
      delete ucmdBusy[key];
      var parsed = ucmdParseError(x.s, x.r.status);
      if (x.r.ok && parsed.body && Array.isArray(parsed.body.commands)) {
        delete ucmdDrafts[key];
        ucmdApply(parsed.body, true);
        noteOk(wsT("js.ucmd.saved", c.label || c.id));
        return;
      }
      if (x.r.status === 409 && parsed.body && Array.isArray(parsed.body.commands)) {
        ucmdApply(parsed.body, true);
        noteWarn(wsT(parsed.body.error === "withdrawn" ? "js.ucmd.withdrawn" : "js.ucmd.stale"));
        return;
      }
      ucmdRender();
      noteWarn(wsT("js.send.fail", parsed.msg));
    }, function () {
      delete ucmdBusy[key];
      ucmdRender();
      noteWarn(wsT("js.post.unreachable"));
    });
  }

  function ucmdRun(c, eff) {
    var key = ucmdKey(c);
    if (ucmdBusy[key]) return;
    if (!eff.line.trim()) {
      noteWarn(wsT("js.ucmd.save.empty"));
      return;
    }
    var effCwd = c.cwd || "";
    var attempts = ucmdAttempts(), prior = attempts[key];
    var retry = !!(prior && prior.rev === c.revision && prior.line === eff.line && prior.cwd === effCwd && typeof prior.id === "string");
    var id = retry ? prior.id : cexecNewID();
    attempts[key] = { id: id, rev: c.revision, line: eff.line, cwd: effCwd };
    ucmdSaveAttempts(attempts);
    ucmdBusy[key] = true;
    ucmdRender();
    function finish() {
      window.setTimeout(function () { delete ucmdBusy[key]; ucmdRender(); }, ucmdGuardMs);
    }
    function settled() {
      var a = ucmdAttempts();
      if (a[key] && a[key].id === id) { delete a[key]; ucmdSaveAttempts(a); }
    }
    function sendRun() {
      var payload = {
        submission_id: id,
        revision: c.revision,
        retry: retry,
        token: cexecToken,
        source: c.source || "agent"
      };
      if (eff.cmdDirty) payload.line = eff.line;
      fetch("/s/" + rid + "/user-commands/" + encodeURIComponent(c.id) + "/run", {
        method: "POST", credentials: "same-origin", cache: "no-store",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      }).then(function (r) {
        return r.text().then(function (s) { return { r: r, s: s }; }, function () { return { r: r, s: "" }; });
      }).then(function (x) {
        var r = x.r, s = x.s, parsed = ucmdParseError(s, r.status), body = parsed.body;
        if (r.ok) {
          finish();
          if (body && typeof body.token === "string" && body.token) cexecToken = body.token;
          settled();
          ucmdClosePopover(false);
          if (body && body.unknown) {
            cexecTrackSubmission(id, eff.line, { id: id, command: eff.line, label: c.label, phase: "done", unknown: true }, "ucmd");
            return;
          }
          cexecTrackSubmission(id, eff.line, body && body.id === id ? body : null, "ucmd");
          return;
        }
        delete ucmdBusy[key];
        if (r.status === 409 && body && Array.isArray(body.commands)) {
          settled();
          ucmdApply(body, true);
          noteWarn(wsT(body.error === "withdrawn" ? "js.ucmd.withdrawn" : "js.ucmd.stale"));
          return;
        }
        ucmdRender();
        if (r.status === 404 || r.status === 405) noteWarn(wsT("js.ucmd.unsupported"));
        else noteWarn(wsT("js.send.fail", parsed.msg));
        if (r.status < 500) settled();
      }, function () {
        delete ucmdBusy[key];
        ucmdRender();
        noteWarn(cexecUnreachable(wsT("js.cexec.neterr")));
      });
    }
    if (cexecToken || retry) sendRun();
    else cexecList(function (list, why) {
      if (!list) {
        finish();
        settled();
        noteWarn(cexecUnreachable(why));
        return;
      }
      sendRun();
    });
  }

  function ucmdEscapeXml(text, attr) {
    var out = String(text || "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    if (attr) out = out.replace(/"/g, "&quot;").replace(/'/g, "&#39;");
    return out.replace(/\[Image #([1-9][0-9]{0,5})\]/g, "&#91;Image #$1]");
  }

  function ucmdAskText(c, eff) {
    var label = c.label || c.id || "";
    var open = "<ws:ucmd";
    if (label) open += ' label="' + ucmdEscapeXml(label, true) + '"';
    if (eff.anyDirty) open += ' edited="true"';
    open += ">";
    var cmdTag = "<cmd" + (c.cwd ? ' cwd="' + ucmdEscapeXml(c.cwd, true) + '"' : "") + ">" + ucmdEscapeXml(eff.line, false) + "</cmd>";
    var lines = [open, "User requested execution of the following command.", cmdTag];
    var prompt = eff.prompt || wsT("js.ucmd.ask.default");
    if (prompt) lines.push("<prompt>" + ucmdEscapeXml(prompt, false) + "</prompt>");
    lines.push("</ws:ucmd>");
    return lines.join("\n");
  }

  function ucmdAsk(c, eff, text) {
    var key = ucmdKey(c);
    if (ucmdBusy[key]) return;
    if (!eff.line.trim()) {
      noteWarn(wsT("js.ucmd.save.empty"));
      return;
    }
    ucmdBusy[key] = true;
    ucmdRender();
    post("message", {
      utcOffsetMinutes: -new Date().getTimezoneOffset(),
      text: text,
      chips: [],
      topics: [],
      mentions: []
    }, function (ok, ans) {
      delete ucmdBusy[key];
      ucmdRender();
      if (ok) {
        ucmdClosePopover(false);
        noteOk(wsT("js.ucmd.ask.sent", c.label || c.id));
      } else {
        noteWarn(wsT("js.send.fail", ans));
      }
    });
  }

  function ucmdRemove(c) {
    var key = ucmdKey(c);
    if (ucmdBusy[key]) return;
    ucmdBusy[key] = true;
    ucmdRender();
    fetch("/s/" + rid + "/user-commands/" + encodeURIComponent(c.id) + "/remove", {
      method: "POST", credentials: "same-origin", cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ revision: c.revision, source: c.source || "agent" })
    }).then(function (r) {
      return r.text().then(function (s) { return { r: r, s: s }; }, function () { return { r: r, s: "" }; });
    }).then(function (x) {
      delete ucmdBusy[key];
      var parsed = ucmdParseError(x.s, x.r.status);
      if (x.r.ok && parsed.body && Array.isArray(parsed.body.commands)) {
        delete ucmdDrafts[key];
        ucmdApply(parsed.body, true);
        noteOk(wsT("js.ucmd.deleted", c.label || c.id));
        return;
      }
      if (x.r.status === 409 && parsed.body && Array.isArray(parsed.body.commands)) {
        if (parsed.body.error === "withdrawn") delete ucmdDrafts[key];
        ucmdApply(parsed.body, true);
        noteWarn(wsT(parsed.body.error === "withdrawn" ? "js.ucmd.withdrawn" : "js.ucmd.stale"));
        return;
      }
      ucmdRender();
      noteWarn(wsT("js.send.fail", parsed.msg));
    }, function () {
      delete ucmdBusy[key];
      ucmdRender();
      noteWarn(wsT("js.post.unreachable"));
    });
  }

  ucmdEnsureUI();
  ucmdFetch();
