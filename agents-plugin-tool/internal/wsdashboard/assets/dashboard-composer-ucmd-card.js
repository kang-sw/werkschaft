// User command tiles (mockup Option C): ucmdBuildCard assembles one two-zone
// tile (header band with scope, inline-editable name and actions; body well
// with the collapsible $ command and the prompt note), ucmdUpdateCard patches
// it by key, and ucmdExpand / ucmdCollapse toggle the $ editor in place.
// Shares state with dashboard-composer-ucmd.js (same IIFE).
  function ucmdSyncDirty(key) {
    var c = ucmdByKey[key], node = ucmdNodes[key];
    if (!c || !node) return;
    var eff = ucmdEffective(c);
    var cmdDirty = eff.cmdDirty, prDirty = eff.prDirty;
    var saving = ucmdBusy[key] === "save";
    node.el.classList.toggle("is-dirty", eff.saveDirty);
    node.titleIn.classList.toggle("is-dirty", eff.lbDirty);
    node.cmdBox.classList.toggle("is-dirty", cmdDirty);
    node.cmdRev.classList.toggle("is-dirty", cmdDirty);
    node.cmdRev.disabled = saving || !cmdDirty;
    node.cmdRevEd.classList.toggle("is-dirty", cmdDirty);
    node.cmdRevEd.disabled = saving || !cmdDirty;
    node.prBar.classList.toggle("is-dirty", prDirty);
    node.prBar.classList.toggle("has-val", !!eff.prompt.trim());
    node.prRev.classList.toggle("is-dirty", prDirty);
    node.prRev.disabled = saving || !prDirty;
  }

  // ucmdSyncCmdView refreshes whichever $ view is showing: the highlighted
  // editor and its line meta when expanded, else the one-line preview and its
  // +N lines pill. Each repaints only when its text changed.
  function ucmdSyncCmdView(node) {
    var text = node.cmdIn.value, n = ucmdLineCount(text);
    if (ucmdExpanded === node.key) {
      ucmdCodePaint(node.code);
      if (node.metaN !== n) {
        node.metaN = n;
        node.cmdMeta.textContent = wsTn("js.ucmd.ed.meta", n);
      }
      return;
    }
    if (node.pvText === text) return;
    node.pvText = text;
    var segs = ucmdPreviewSegments(text);
    node.cmdPv.classList.toggle("is-empty", !segs.length);
    if (segs.length) ucmdPaintTokens(node.cmdPv, segs);
    else node.cmdPv.textContent = wsT("js.ucmd.preview.empty");
    node.cmdPillTxt.textContent = n > 1 ? wsTn("js.ucmd.lines", n - 1) : "";
    node.cmdPill.classList.toggle("is-single", n <= 1);
  }

  function ucmdExpand(key, focus) {
    var node = ucmdNodes[key];
    if (!node) return;
    if (ucmdExpanded && ucmdExpanded !== key) ucmdCollapse(false);
    if (ucmdExpanded !== key) {
      ucmdExpanded = key;
      node.cmdBox.classList.add("is-expanded");
      node.cmdRow.setAttribute("aria-expanded", "true");
      ucmdSyncCmdView(node);
    }
    if (focus) {
      // The first open starts at the top like the preview; later opens keep
      // the caret where the user left it.
      if (!node.caretSeen && typeof node.cmdIn.setSelectionRange === "function") node.cmdIn.setSelectionRange(0, 0);
      node.caretSeen = true;
      node.cmdIn.focus();
    }
    ucmdCodeScroll(node.code);
  }
  // ucmdCollapse folds the open $ editor back to its preview, which then
  // shows the latest draft; drafts and dirty state are untouched.
  function ucmdCollapse(refocus) {
    var node = ucmdExpanded && ucmdNodes[ucmdExpanded];
    ucmdExpanded = "";
    if (!node) return;
    node.cmdBox.classList.remove("is-expanded");
    node.cmdRow.setAttribute("aria-expanded", "false");
    ucmdSyncCmdView(node);
    if (refocus) node.cmdRow.focus();
  }

  function ucmdBuildCard(c) {
    var key = ucmdKey(c);
    var el = doc.createElement("div");
    el.className = "ucmd-card";
    el.setAttribute("role", "listitem");
    el.setAttribute("data-ucmd-key", key);
    el.setAttribute("data-ucmd", c.id);
    el.setAttribute("data-source", c.source || "agent");
    var node = { key: key, multi: false, el: el, pvText: null, metaN: 0 };
    function glyph(cls, content) {
      var g = doc.createElement("span");
      g.className = "ucmd-glyph" + (cls ? " " + cls : "");
      if (typeof content === "string") g.textContent = content;
      else g.appendChild(content);
      return g;
    }
    function revBtn(i18nKey) {
      var b = doc.createElement("button");
      b.type = "button";
      b.className = "ucmd-rev-btn";
      b.title = wsT(i18nKey);
      b.setAttribute("aria-label", wsT(i18nKey));
      b.disabled = true;
      b.appendChild(ucmdRevertIcon());
      return b;
    }

    // Zone 1, header band: scope badge + inline-editable name + dirty dot +
    // cwd, then the 24x24 actions.
    var band = doc.createElement("div");
    band.className = "ucmd-band";
    var line1 = doc.createElement("div");
    line1.className = "ucmd-line1";
    var headL = doc.createElement("div");
    headL.className = "ucmd-head-l";
    var srcEl = doc.createElement("span");
    srcEl.className = "ucmd-src";
    var titleIn = doc.createElement("input");
    titleIn.type = "text";
    titleIn.className = "ucmd-title";
    titleIn.maxLength = 80;
    titleIn.spellcheck = false;
    titleIn.autocomplete = "off";
    titleIn.setAttribute("aria-label", wsT("js.ucmd.form.label"));
    titleIn.addEventListener("input", function () { ucmdEdit(key, "label", titleIn.value, false); });
    titleIn.addEventListener("keydown", function (ev) {
      if (ev.isComposing) return;
      var cur = ucmdByKey[key];
      if (ev.key === "Enter") {
        ev.preventDefault();
        if (cur && ucmdEffective(cur).saveDirty) saveBtn.focus();
        else titleIn.blur();
      } else if (ev.key === "Escape" && cur && ucmdEffective(cur).lbDirty) {
        ev.preventDefault();
        ev.stopPropagation();
        titleIn.value = ucmdEditorValue(ucmdSaved(cur, "label"), false);
        ucmdEdit(key, "label", titleIn.value, false);
      }
    });
    var dot = doc.createElement("span");
    dot.className = "ucmd-dirty-dot";
    dot.title = wsT("js.ucmd.dirty.dot");
    var cwdEl = doc.createElement("span");
    cwdEl.className = "ucmd-cwd";
    headL.appendChild(srcEl);
    headL.appendChild(titleIn);
    headL.appendChild(dot);
    headL.appendChild(cwdEl);

    var acts = doc.createElement("div");
    acts.className = "ucmd-acts";
    function makeAct(cls, act, iconEl, hold) {
      var b = doc.createElement("button");
      b.type = "button";
      b.className = "ucmd-ibtn " + cls + (hold ? " is-hold" : "");
      b.setAttribute("data-act", act);
      b.setAttribute("data-ucmd-key", key);
      b.appendChild(iconEl);
      return b;
    }
    var runBtn = makeAct("ucmd-ibtn-run", "run", ucmdPlayIcon(), true);
    var askBtn = makeAct("ucmd-ibtn-ask", "ask", ucmdAskIcon(), true);
    var saveBtn = makeAct("ucmd-ibtn-save", "save", ucmdSaveIcon(), false);
    saveBtn.title = wsT("js.ucmd.save.title");
    saveBtn.setAttribute("aria-label", wsT("js.ucmd.save.title"));
    saveBtn.addEventListener("click", function () {
      var cur = ucmdByKey[key];
      if (cur) ucmdSave(cur);
    });
    var delBtn = makeAct("ucmd-ibtn-del", "del", ucmdDelIcon(), true);
    acts.appendChild(runBtn);
    acts.appendChild(askBtn);
    acts.appendChild(saveBtn);
    acts.appendChild(delBtn);
    line1.appendChild(headL);
    line1.appendChild(acts);
    band.appendChild(line1);
    el.appendChild(band);

    // Zone 2, body well: the $ command box and the prompt note.
    var well = doc.createElement("div");
    well.className = "ucmd-well";
    var cmdBox = doc.createElement("div");
    cmdBox.className = "ucmd-cmd-box";

    // Collapsed: one-line highlighted preview, +N lines pill, ↺.
    var cmdRow = doc.createElement("div");
    cmdRow.className = "ucmd-cmd-row";
    cmdRow.tabIndex = 0;
    cmdRow.setAttribute("role", "button");
    cmdRow.setAttribute("aria-expanded", "false");
    cmdRow.title = wsT("js.ucmd.expand.title");
    var cmdPv = doc.createElement("span");
    cmdPv.className = "ucmd-cmd-preview";
    var cmdPill = doc.createElement("span");
    cmdPill.className = "ucmd-lines-pill";
    var cmdPillTxt = doc.createElement("span");
    cmdPill.appendChild(cmdPillTxt);
    cmdPill.appendChild(ucmdChevIcon(false));
    var cmdRev = revBtn("js.ucmd.revert.cmd");
    cmdRow.appendChild(glyph("", "$"));
    cmdRow.appendChild(cmdPv);
    cmdRow.appendChild(cmdPill);
    cmdRow.appendChild(cmdRev);
    cmdRow.addEventListener("click", function () { ucmdExpand(key, true); });
    cmdRow.addEventListener("keydown", function (ev) {
      if (ev.target !== cmdRow || (ev.key !== "Enter" && ev.key !== " ")) return;
      ev.preventDefault();
      ucmdExpand(key, true);
    });

    // Expanded: the overlay editor, line meta, ↺ and Collapse.
    var cmdEd = doc.createElement("div");
    cmdEd.className = "ucmd-cmd-editor";
    var edRow = doc.createElement("div");
    edRow.className = "ucmd-cmd-ed-row";
    var cmdIn = doc.createElement("textarea");
    cmdIn.className = "ucmd-inline-input is-cmd";
    cmdIn.placeholder = wsT("js.ucmd.placeholder.cmd");
    var code = ucmdCodeEditor(cmdIn);
    edRow.appendChild(glyph("", "$"));
    edRow.appendChild(code.wrap);
    var foot = doc.createElement("div");
    foot.className = "ucmd-cmd-ed-foot";
    var cmdMeta = doc.createElement("span");
    cmdMeta.className = "ucmd-ed-meta";
    var edActs = doc.createElement("div");
    edActs.className = "ucmd-ed-acts";
    var cmdRevEd = revBtn("js.ucmd.revert.cmd");
    var foldBtn = doc.createElement("button");
    foldBtn.type = "button";
    foldBtn.className = "ucmd-collapse-btn";
    foldBtn.appendChild(ucmdChevIcon(true));
    var foldTxt = doc.createElement("span");
    foldTxt.textContent = wsT("js.ucmd.collapse");
    foldBtn.appendChild(foldTxt);
    foldBtn.addEventListener("click", function () { ucmdCollapse(true); });
    edActs.appendChild(cmdRevEd);
    edActs.appendChild(foldBtn);
    foot.appendChild(cmdMeta);
    foot.appendChild(edActs);
    cmdEd.appendChild(edRow);
    cmdEd.appendChild(foot);
    cmdBox.appendChild(cmdRow);
    cmdBox.appendChild(cmdEd);

    cmdIn.addEventListener("input", function () {
      ucmdEdit(key, "line", cmdIn.value, node.multi);
      ucmdSyncCmdView(node);
    });
    cmdIn.addEventListener("keydown", function (ev) {
      if (ev.isComposing) return;
      if (ev.key === "Enter" && !ev.shiftKey) {
        ev.preventDefault();
        runBtn.focus();
        noteWarn(wsT("js.ucmd.enter.hint"));
      } else if (ev.key === "Escape") {
        ev.preventDefault();
        ev.stopPropagation();
        ucmdCollapse(true);
      }
    });
    // Tabbing out folds the editor; moving to an action button keeps it open.
    cmdBox.addEventListener("focusout", function (ev) {
      var to = ev.relatedTarget;
      if (ucmdExpanded !== key || !to || cmdBox.contains(to)) return;
      if (typeof to.closest === "function" && to.closest(".ucmd-ibtn")) return;
      ucmdCollapse(false);
    });
    function revertCmd(ev) {
      if (ev) ev.stopPropagation();
      var cur = ucmdByKey[key];
      if (!cur) return;
      cmdIn.value = ucmdEditorValue(cur.line || "", node.multi);
      ucmdEdit(key, "line", cmdIn.value, node.multi);
      ucmdSyncCmdView(node);
      noteOk(wsT("js.ucmd.reverted.cmd", cur.label));
    }
    cmdRev.addEventListener("click", revertCmd);
    cmdRevEd.addEventListener("click", revertCmd);
    well.appendChild(cmdBox);

    var divider = doc.createElement("div");
    divider.className = "ucmd-divider";
    well.appendChild(divider);

    // Prompt note: speech glyph + textarea + ↺, flush in the same well.
    var prBar = doc.createElement("div");
    prBar.className = "ucmd-note";
    var prIn = doc.createElement("textarea");
    prIn.className = "ucmd-inline-input is-prompt";
    prIn.rows = 1;
    prIn.placeholder = wsT("js.ucmd.placeholder.prompt");
    prIn.autocomplete = "off";
    prIn.addEventListener("input", function () {
      prIn.rows = Math.min(4, prIn.value.split("\n").length);
      ucmdEdit(key, "prompt", prIn.value, true);
    });
    prIn.addEventListener("keydown", function (ev) {
      if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) {
        ev.preventDefault();
        askBtn.focus();
        noteWarn(wsT("js.ucmd.enter.hint"));
      }
    });
    var prRev = revBtn("js.ucmd.revert.prompt");
    prRev.addEventListener("click", function () {
      var cur = ucmdByKey[key];
      if (!cur) return;
      prIn.value = ucmdEditorValue(cur.prompt || "", true);
      prIn.rows = Math.min(4, prIn.value.split("\n").length);
      ucmdEdit(key, "prompt", prIn.value, true);
      noteOk(wsT("js.ucmd.reverted.prompt", cur.label));
    });
    prBar.appendChild(glyph("is-prompt", ucmdPromptIcon()));
    prBar.appendChild(prIn);
    prBar.appendChild(prRev);
    well.appendChild(prBar);
    el.appendChild(well);

    node.srcEl = srcEl; node.titleIn = titleIn; node.cwdEl = cwdEl;
    node.runBtn = runBtn; node.askBtn = askBtn; node.saveBtn = saveBtn; node.delBtn = delBtn;
    node.cmdBox = cmdBox; node.cmdRow = cmdRow; node.cmdPv = cmdPv; node.cmdPill = cmdPill; node.cmdPillTxt = cmdPillTxt;
    node.cmdIn = cmdIn; node.code = code; node.cmdMeta = cmdMeta;
    node.cmdRev = cmdRev; node.cmdRevEd = cmdRevEd;
    node.prBar = prBar; node.prIn = prIn; node.prRev = prRev;
    return node;
  }

  function ucmdUpdateCard(node, c) {
    var key = node.key;
    var src = c.source === "user" ? "user" : "agent";
    node.multi = (c.line || "").indexOf("\n") >= 0;
    node.el.setAttribute("data-source", src);
    node.srcEl.className = "ucmd-src " + src;
    node.srcEl.textContent = wsT(src === "user" ? "js.ucmd.source.user" : "js.ucmd.source.agent");
    var name = c.label || c.id;
    var tip = c.description ? (name + " — " + c.description) : name;
    node.titleIn.title = tip + "\n" + wsT("js.ucmd.title.edit");
    if (c.cwd) {
      node.cwdEl.textContent = c.cwd;
      node.cwdEl.title = c.cwd;
      node.cwdEl.hidden = false;
    } else {
      node.cwdEl.textContent = "";
      node.cwdEl.hidden = true;
    }
    var busy = !!ucmdBusy[key];
    node.runBtn.disabled = busy;
    node.askBtn.disabled = busy;
    node.saveBtn.disabled = busy;
    node.delBtn.disabled = busy;
    node.titleIn.readOnly = node.cmdIn.readOnly = node.prIn.readOnly = ucmdBusy[key] === "save";
    var rt = wsT("js.ucmd.run.title", name);
    node.runBtn.title = rt;
    node.runBtn.setAttribute("aria-label", rt);
    var at = wsT("js.ucmd.ask.title", name);
    node.askBtn.title = at;
    node.askBtn.setAttribute("aria-label", at);
    var dt = wsT("js.ucmd.remove", name);
    node.delBtn.title = dt;
    node.delBtn.setAttribute("aria-label", dt);

    // A draft is shown verbatim (it came from the editor, and may have grown
    // a newline); an untouched field shows its normalized stored value.
    var eff = ucmdEffective(c);
    var targetLine = eff.cmdDirty ? eff.line : ucmdEditorValue(eff.line, node.multi);
    var targetPrompt = ucmdEditorValue(eff.prompt, true);
    var targetLabel = ucmdEditorValue(eff.label, false);
    if (node.titleIn.value !== targetLabel) node.titleIn.value = targetLabel;
    if (node.cmdIn.value !== targetLine) node.cmdIn.value = targetLine;
    if (node.prIn.value !== targetPrompt) node.prIn.value = targetPrompt;
    var rows = Math.min(4, targetPrompt.split("\n").length);
    if (node.prIn.rows !== rows) node.prIn.rows = rows;
    ucmdSyncCmdView(node);
    ucmdSyncDirty(key);
  }
