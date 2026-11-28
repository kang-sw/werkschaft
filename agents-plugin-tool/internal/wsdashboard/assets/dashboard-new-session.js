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
  // The label route's limit (maxLabelRunes in label_route.go) and the time a
  // slow label write may hold the page before it opens the new session anyway.
  var LEAD_LABEL_MAX = 64, LEAD_LABEL_WAIT = 5000;
  // leadLabelClean is the label the field holds as the route reads it: control
  // characters become spaces, the ends are trimmed, and at most LEAD_LABEL_MAX
  // characters stay.
  function leadLabelClean(text) {
    var v = String(text || "").replace(/[\u0000-\u001f\u007f-\u009f\u2028\u2029]+/g, " ").trim();
    return Array.from(v).slice(0, LEAD_LABEL_MAX).join("").trim();
  }
  // leadLabelSend names the session id and calls done once, after the write
  // answered, failed, or ran out of time: a label that is not saved never
  // keeps the new session from opening (a 404 means the session is not yet
  // indexed, so it is tried again shortly).
  function leadLabelSend(id, label, done) {
    var ended = false, tries = 0, timer = 0;
    function end() {
      if (ended) return;
      ended = true;
      if (timer) clearTimeout(timer);
      done();
    }
    function send() {
      tries++;
      try {
        fetch("/s/" + encodeURIComponent(id) + "/label", {
          method: "POST", credentials: "same-origin", cache: "no-store",
          headers: { "Content-Type": "application/json" }, body: JSON.stringify({ label: label })
        }).then(function (r) {
          if (r.status === 404 && tries < 3 && !ended) { setTimeout(send, 300); return; }
          end();
        }).then(null, end);
      } catch (_) { end(); }
    }
    timer = setTimeout(end, LEAD_LABEL_WAIT);
    send();
  }
  function rpLeadMake() {
    var P = { gen: 0, busy: false, loading: false, root: "", harness: "claude", model: "", effort: "", mode: "", iso: "default", wbOnly: false, convHistory: false, level: "", dropped: "", refused: 0, statusText: "", dashboardControl: false };
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
    // Optional session label, set right after the spawn succeeds (never saved
    // with the remembered picks: the field starts empty on every open).
    var labelIn = ctaW.appendChild(mk("input", "ns-label-in"));
    labelIn.type = "text";
    labelIn.maxLength = LEAD_LABEL_MAX;
    labelIn.autocomplete = "off";
    labelIn.spellcheck = false;
    labelIn.placeholder = wsT("js.lead.label.placeholder");
    labelIn.setAttribute("aria-label", wsT("js.lead.label.aria"));
    labelIn.setAttribute("data-session-label", "1");
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

    var footOpts = foot.appendChild(mk("div", "ns-foot-opts"));
    var wbSw = footOpts.appendChild(rpBtn("ns-sw", ""));
    wbSw.setAttribute("role", "switch");
    wbSw.setAttribute("data-wb-only-switch", "1");
    wbSw.setAttribute("aria-label", wsT("js.lead.wb_only"));
    wbSw.setAttribute("aria-checked", "false");
    wbSw.title = wsT("js.lead.wb_only.hint");
    wbSw.appendChild(mk("i"));
    wbSw.appendChild(mk("span", null, wsT("js.lead.wb_only")));

    var convSw = footOpts.appendChild(rpBtn("ns-sw", ""));
    convSw.setAttribute("role", "switch");
    convSw.setAttribute("data-conv-history-switch", "1");
    convSw.setAttribute("aria-label", wsT("js.lead.conv_history"));
    convSw.setAttribute("aria-checked", "false");
    convSw.title = wsT("js.lead.conv_history.hint");
    convSw.appendChild(mk("i"));
    convSw.appendChild(mk("span", null, wsT("js.lead.conv_history")));

    var dcSw = footOpts.appendChild(rpBtn("ns-sw", ""));
    dcSw.setAttribute("role", "switch");
    dcSw.setAttribute("data-dashboard-control-switch", "1");
    dcSw.setAttribute("aria-label", wsT("js.dc.title"));
    dcSw.setAttribute("aria-checked", "false");
    dcSw.hidden = true;
    dcSw.appendChild(mk("i"));
    var dcLbl = dcSw.appendChild(mk("span", null, wsT("js.dc.short") + " · " + wsT("js.dc.off")));

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
        var desc = (claudeInline && LEAD_MODE_NOTE[modeNow]) ? wsT(LEAD_MODE_NOTE[modeNow]) : "";
        var own = gone ? wsT("js.perm.native.own") : "";
        var inlineNote = desc && own ? desc + " " + own : (desc || own);
        pNativeNote.textContent = inlineNote;
        pNativeNote.hidden = !inlineNote;
        if (pNativeNote.classList) pNativeNote.classList.toggle("warn", !!inlineNote && !!LEAD_DANGER_MODE[modeNow]);
      } else {
        pNativeNote.textContent = "";
        pNativeNote.hidden = true;
        if (pNativeNote.classList) pNativeNote.classList.toggle("warn", false);
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
        var noteText = owned ? wsT("js.perm.adv.owned.note", wsT(PERM_LABEL[level])) : (claude && LEAD_MODE_NOTE[modeNow] ? wsT(LEAD_MODE_NOTE[modeNow]) : "");
        advNote.textContent = noteText;
        advNote.hidden = !noteText;
        if (advNote.classList) advNote.classList.toggle("warn", !owned && !!noteText && !!LEAD_DANGER_MODE[modeNow]);
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
      wbSw.setAttribute("aria-checked", String(!!P.wbOnly));
      wbSw.disabled = P.busy;
      convSw.setAttribute("aria-checked", String(!!P.convHistory));
      convSw.disabled = P.busy;
      labelIn.disabled = P.busy;
      isoLbl.textContent = wsT("js.lead.iso") + " · " + wsT(isolated ? "js.lead.iso.isolated" : "js.lead.iso.default");
      isoSw.title = wsT(isolated ? "js.lead.iso.isolated.hint" : "js.lead.iso.default.hint");
      var dcSup = dashboardControlSupported(P.harness) === true;
      if (!dcSup && P.dashboardControl) P.dashboardControl = false;
      dcSw.hidden = !dcSup;
      dcSw.setAttribute("aria-checked", String(!!P.dashboardControl));
      dcSw.disabled = P.busy || P.loading || off(P.harness);
      dcLbl.textContent = wsT("js.dc.short") + " · " + wsT(P.dashboardControl ? "js.dc.on" : "js.dc.off");
      dcSw.title = wsT("js.dc.desc");
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
      P.dashboardControl = false;
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
        wbOnly: !!P.wbOnly,
        form: {
          harness: P.harness,
          model: chosen.model,
          provider: chosen.provider,
          effort: effortOff() ? "" : P.effort,
          mode: level ? "" : freeMode(),
          isolation: P.iso,
          level: level,
          ack: ack,
          convHistoryAccess: !!P.convHistory,
          dashboardControl: dashboardControlSupported(P.harness) === true && P.dashboardControl === true
        }
      };
    }
    function launch(snap) {
      notifAsk();
      var gen = P.gen, chosenHarness = snap.harness, chosen = snap.chosen, form = snap.form, level = form.level, wbOnly = !!snap.wbOnly;
      var label = leadLabelClean(labelIn.value);
      P.busy = true;
      P.statusText = wsT(level || form.dashboardControl ? "js.perm.checking" : "js.lead.starting");
      setErr("");
      permDraw();
      sync();
      if (!level && !form.dashboardControl) { post(gen, chosenHarness, chosen, "/agents/leads", form, label, wbOnly); return; }
      if (!level && form.dashboardControl) {
        dashboardControlPreflight(chosenHarness, function (ok) {
          if (!rpLive(P, gen)) { P.busy = false; P.statusText = ""; return; }
          if (!ok) {
            P.busy = false;
            P.statusText = "";
            P.dashboardControl = false;
            setErr(wsT("js.dc.refused", 404));
            permDraw();
            sync();
            railPopPlace();
            return;
          }
          P.statusText = wsT("js.lead.starting");
          drawCta();
          post(gen, chosenHarness, chosen, "/agents/leads", form, label, wbOnly);
        });
        return;
      }
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
        if (form.dashboardControl && dashboardControlSupported(chosenHarness) !== true) {
          P.busy = false;
          P.statusText = "";
          P.dashboardControl = false;
          setErr(wsT("js.dc.refused", 404));
          permDraw();
          sync();
          railPopPlace();
          return;
        }
        P.statusText = wsT("js.lead.starting");
        drawCta();
        post(gen, chosenHarness, chosen, "/agents/leads/permission", form, label, wbOnly);
      });
    }
    function post(gen, chosenHarness, chosen, route, form, label, wbOnly) {
      var perm = !!form.level;
      railPost(route, WsRail.leadBody(P.root, form), function (st, v, s) {
        var created = st === 201 && v.id;
        // A label to set keeps Start busy until its write ends; the session
        // exists by then, so the page opens it whatever the write answered.
        if (created && label) {
          leadLabelSend(String(v.id), label, function () { opened(gen, chosenHarness, chosen, v, wbOnly); });
          return;
        }
        P.busy = false;
        P.statusText = "";
        sync();
        if (created) { opened(gen, chosenHarness, chosen, v, wbOnly); return; }
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
    // opened remembers the picks of a started session and opens its page.
    function opened(gen, chosenHarness, chosen, v, wbOnly) {
      if (P.busy) {
        P.busy = false;
        P.statusText = "";
        sync();
      }
      var stored = readSettings(), byHarness = stored.harnesses;
      if (!byHarness || typeof byHarness !== "object" || Array.isArray(byHarness)) byHarness = {};
      byHarness[chosenHarness] = chosen;
      var nextSettings = { harness: chosenHarness, harnesses: byHarness };
      if (wbOnly) nextSettings.whiteboardOnly = true;
      try { localStorage.setItem(settingsKey, JSON.stringify(nextSettings)); } catch (_) { /* storage may be unavailable */ }
      if (wbOnly) {
        try { localStorage.setItem("ws.dashboard.whiteboardFirst." + String(v.id), "1"); } catch (_) { /* storage may be unavailable */ }
      }
      if (rpLive(P, gen)) railPopClose(false);
      railOpen(false);
      saveDraft();
      go("/s/" + encodeURIComponent(String(v.id)));
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
    dcSw.addEventListener("click", function () { if (P.busy || dcSw.disabled || dcSw.hidden) return; P.dashboardControl = !P.dashboardControl; sync(); });
    wbSw.addEventListener("click", function () { if (P.busy) return; P.wbOnly = !P.wbOnly; sync(); });
    convSw.addEventListener("click", function () { if (P.busy) return; P.convHistory = !P.convHistory; sync(); });
    seg.addEventListener("click", function (e) { var b = e.target.closest && e.target.closest("[data-harness]"); if (b && !b.disabled) setHarness(b.getAttribute("data-harness")); });
    effRst.addEventListener("click", function () { if (P.busy || P.loading || !P.effort) return; P.effort = ""; tierPick.manual(); edited(); });
    xBtn.addEventListener("click", function () { railPopClose(true); });
    start.addEventListener("click", submit);
    labelIn.addEventListener("keydown", function (e) {
      if (e.isComposing || e.keyCode === 229 || e.key !== "Enter") return;
      e.preventDefault();
      if (!P.busy) submit();
    });
    P.open = function (key, label, btn) {
      var gen = ++P.gen, name = btn.getAttribute("data-name") || label, branch = btn.getAttribute("data-branch") || "";
      P.busy = false;
      P.statusText = "";
      P.root = key;
      P.level = P.dropped = "";
      P.refused = 0;
      adv.open = false;
      labelIn.value = "";
      saved = readSettings();
      picks = Object.create(null);
      if (saved.harnesses && typeof saved.harnesses === "object" && !Array.isArray(saved.harnesses)) {
        Object.keys(saved.harnesses).forEach(function (k) { picks[k] = saved.harnesses[k]; });
      }
      P.harness = "claude";
      P.iso = "default";
      P.dashboardControl = false;
      P.wbOnly = saved.whiteboardOnly === true;
      P.convHistory = false;
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
