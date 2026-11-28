  // --- cross-harness session switch (Phase 2 of 261128) ----------------------
  // Hosted leads can switch across non-durable external harnesses with a
  // checkpoint handoff via POST /agents/<id>/harness. Eligible targets come
  // solely from /support/choices's harness_switch flag (never hardcoded
  // harness names), and the switcher never preselects a target harness.
  function harnessSwitchChoices(curBackend) {
    var cur = String(curBackend || "");
    var list = supChoices && Array.isArray(supChoices.harnesses) ? supChoices.harnesses : [];
    return list.filter(function (h) {
      return !!(h && h.harness_switch === true && h.available !== false && h.harness && String(h.harness) !== cur);
    });
  }
  function harnessSwitchCurrentSupported(curBackend) {
    var cur = String(curBackend || "");
    if (!cur || !supChoices || !Array.isArray(supChoices.harnesses)) return true;
    var h = typeof supHarness === "function" ? supHarness(cur) : null;
    return !h || h.harness_switch !== false;
  }
  function harnessSwitchErr(status, text) {
    var raw = String(text || "").trim();
    if (status === 409) {
      if (raw.indexOf("conversation_busy") >= 0) return wsT("js.hswitch.err.busy");
      return raw || wsT("js.hswitch.err.conflict");
    }
    if (status === 400) {
      if (raw.indexOf("harness_unsupported") >= 0) return wsT("js.hswitch.err.unsupported");
      if (raw.indexOf("harness_unchanged") >= 0) return wsT("js.hswitch.err.unchanged");
      return raw || wsT("js.hswitch.err.invalid");
    }
    if (status === 403) return raw || wsT("js.hswitch.err.forbidden");
    if (status === 404) return raw || wsT("js.hswitch.err.notfound");
    return leadErr(status, raw);
  }
  function makeHarnessSwitchSection(opts) {
    var sec = mk("details", "spsec hssec");
    sec.setAttribute("data-harness-switch", "1");
    sec.hidden = true;
    var sum = sec.appendChild(mk("summary", "hssum"));
    sum.appendChild(mk("span", "hstitle", wsT("js.hswitch.title")));
    var curPill = sum.appendChild(mk("span", "hscur"));
    var body = sec.appendChild(mk("div", "hsbody"));
    body.appendChild(mk("p", "spdesc hsdesc", wsT("js.hswitch.desc")));

    function makeRow(labelText, cls, dataAttr) {
      var r = body.appendChild(mk("div", "sprc"));
      var l = r.appendChild(mk("label", null, labelText));
      var s = r.appendChild(mk("select", "spsm" + (cls ? " " + cls : "")));
      s.id = rpId();
      l.htmlFor = s.id;
      if (dataAttr) s.setAttribute(dataAttr, "1");
      return { row: r, sel: s };
    }
    var targetRow = makeRow(wsT("js.hswitch.target"), "", "data-hs-target");
    var emptyNote = body.appendChild(mk("p", "spnote hsempty", wsT("js.hswitch.none")));
    emptyNote.hidden = true;
    var modelRow = makeRow(wsT("js.sup.model"), "mono", "data-hs-model");
    modelRow.row.hidden = true;
    var effortRow = makeRow(wsT("js.sup.effort"), "", "data-hs-effort");
    effortRow.row.hidden = true;

    var actRow = body.appendChild(mk("div", "hsact"));
    var submitBtn = actRow.appendChild(rpBtn("pbtn go hsgo", wsT("js.hswitch.go")));
    submitBtn.setAttribute("data-hs-submit", "1");
    submitBtn.disabled = true;

    var errLine = body.appendChild(mk("p", "spnote err hserr"));
    errLine.setAttribute("aria-live", "polite");
    errLine.setAttribute("data-hs-error", "1");
    errLine.hidden = true;

    var state = {
      backend: "",
      targetSig: "",
      target: "",
      switching: false,
      err: ""
    };

    function syncTargetDetails(target, off) {
      var hasTarget = !!target;
      modelRow.row.hidden = !hasTarget;
      effortRow.row.hidden = !hasTarget;
      if (!hasTarget) return;
      var models = typeof leadModels === "function" ? leadModels(target, "", false) : [];
      var labels = typeof leadModelLabels === "function" ? leadModelLabels(target, "", false) : null;
      var efforts = typeof leadEfforts === "function" ? leadEfforts(target) : [];
      leadOptions(modelRow.sel, models, modelRow.sel.value, labels);
      leadOptions(effortRow.sel, efforts, effortRow.sel.value);
      modelRow.sel.disabled = off || !models.length;
      effortRow.sel.disabled = off || !efforts.length;
    }

    function syncControls(st) {
      var busy = state.switching || !!(opts.isBusy && opts.isBusy());
      var can = !!(st && st.can_control && !leadGone(st));
      var off = busy || !can;
      var targets = harnessSwitchChoices(st ? st.backend : "");
      targetRow.sel.disabled = off || !targets.length;
      if (state.target && !targets.some(function (h) { return String(h.harness) === state.target; })) {
        state.target = "";
        targetRow.sel.value = "";
        modelRow.sel.value = "";
        effortRow.sel.value = "";
      }
      syncTargetDetails(state.target, off);
      submitBtn.textContent = wsT(state.switching ? "js.hswitch.switching" : "js.hswitch.go");
      submitBtn.disabled = off || !state.target || !targets.length;
      errLine.textContent = state.err;
      errLine.hidden = !state.err;
    }

    function draw(st) {
      var isLead = !!(st && st.lead && !st.is_child && st.can_control && !leadGone(st));
      var cur = isLead ? String(st.backend || "") : "";
      var show = isLead && !!cur && harnessSwitchCurrentSupported(cur);
      if (sec.hidden !== !show) sec.hidden = !show;
      if (!show) return;

      if (curPill.textContent !== cur) curPill.textContent = cur;
      if (state.backend !== cur) {
        state.backend = cur;
        state.target = "";
        state.err = "";
        targetRow.sel.value = "";
        modelRow.sel.value = "";
        effortRow.sel.value = "";
      }

      var loaded = !!(supChoices && Array.isArray(supChoices.harnesses));
      var targets = harnessSwitchChoices(cur);
      var sig = cur + "\n" + targets.map(function (h) { return String(h.harness); }).join("\n");
      if (state.targetSig !== sig) {
        state.targetSig = sig;
        var prev = state.target;
        targetRow.sel.textContent = "";
        targetRow.sel.appendChild(mk("option", null, wsT("js.hswitch.pick"))).value = "";
        targets.forEach(function (h) {
          var key = String(h.harness);
          targetRow.sel.appendChild(mk("option", null, key)).value = key;
        });
        state.target = targets.some(function (h) { return String(h.harness) === prev; }) ? prev : "";
        targetRow.sel.value = state.target;
      }
      emptyNote.hidden = !loaded || targets.length > 0;
      syncControls(st);
    }

    targetRow.sel.addEventListener("change", function () {
      state.target = String(targetRow.sel.value || "");
      state.err = "";
      modelRow.sel.value = "";
      effortRow.sel.value = "";
      syncControls(opts.getState());
      if (opts.onResize) opts.onResize();
    });

    submitBtn.addEventListener("click", function () {
      var st = opts.getState();
      var id = opts.getId();
      var target = String(targetRow.sel.value || "");
      if (state.switching || (opts.isBusy && opts.isBusy()) || !st || !st.can_control || leadGone(st) || !id || !target) return;
      if (target === String(st.backend || "")) {
        state.err = wsT("js.hswitch.err.unchanged");
        syncControls(st);
        return;
      }
      if (typeof leadActions !== "undefined" && leadActions.has(id)) return;
      if (typeof leadActions !== "undefined") leadActions.add(id);

      var gen = opts.getGen ? opts.getGen() : 0;
      var payload = { harness: target };
      if (!modelRow.sel.disabled && modelRow.sel.value) {
        payload.model = String(modelRow.sel.value);
        var th = typeof supHarness === "function" ? supHarness(target) : null;
        var entries = th && Array.isArray(th.models) ? th.models : [];
        for (var i = 0; i < entries.length; i++) {
          if (entries[i] && String(entries[i].id) === payload.model && entries[i].provider) {
            payload.provider = String(entries[i].provider);
            break;
          }
        }
      }
      if (!effortRow.sel.disabled && effortRow.sel.value) payload.effort = String(effortRow.sel.value);

      state.switching = true;
      state.err = "";
      if (opts.setBusy) opts.setBusy(true);
      else syncControls(st);

      railPost("/agents/" + encodeURIComponent(id) + "/harness", payload, function (status, v, text) {
        if (typeof leadActions !== "undefined") leadActions.delete(id);
        state.switching = false;
        if (status === 200 && v && v.id) {
          state.target = "";
          state.err = "";
          targetRow.sel.value = "";
          modelRow.sel.value = "";
          effortRow.sel.value = "";
          sec.open = false;
          if (opts.setBusy) opts.setBusy(false);
          if (opts.onUpdated) opts.onUpdated(v, gen);
          if (typeof toast === "function") toast(wsT("js.hswitch.done", String(v.backend || target)));
          return;
        }
        var msg = harnessSwitchErr(status, text);
        if (opts.setBusy) opts.setBusy(false);
        if (!opts.isLive || opts.isLive(gen)) {
          state.err = msg;
          syncControls(opts.getState());
          if (opts.onResize) opts.onResize();
        } else if (typeof toast === "function") {
          toast(msg);
        }
        if (opts.onError) opts.onError(status, msg);
      });
    });

    function open(focusTarget) {
      var st = opts.getState();
      state.target = "";
      state.err = "";
      targetRow.sel.value = "";
      modelRow.sel.value = "";
      effortRow.sel.value = "";
      sec.open = true;
      draw(st);
      if (opts.onResize) opts.onResize();
      if (focusTarget && !targetRow.sel.disabled && targetRow.sel.focus) targetRow.sel.focus();
    }

    function reset() {
      sec.open = false;
      state.target = "";
      state.err = "";
      state.backend = "";
      state.targetSig = "";
      targetRow.sel.value = "";
      modelRow.sel.value = "";
      effortRow.sel.value = "";
    }

    return { el: sec, draw: draw, open: open, reset: reset };
  }

  function openHarnessSwitcher(anchorBtn) {
    if (typeof leadCan === "function" && !leadCan()) return;
    if (typeof rp !== "undefined" && rp.P && rp.kind === "sess") {
      if (typeof rp.P.openHarnessSwitch === "function") rp.P.openHarnessSwitch(true);
      return;
    }
    var h = typeof region === "function" ? region("head") : null;
    var hb = h && h.querySelector ? h.querySelector(".host.hbtn") : null;
    var anchor = hb || anchorBtn;
    if (!anchor || typeof railPopOpen !== "function") return;
    railPopOpen("sess", anchor);
    if (typeof rp !== "undefined" && rp.P && rp.kind === "sess" && typeof rp.P.openHarnessSwitch === "function") {
      rp.P.openHarnessSwitch(true);
    }
  }
