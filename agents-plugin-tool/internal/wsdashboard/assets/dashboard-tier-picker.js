  // --- named-tier picker (new-session popover) -----------------------------
  // The popover's optional preset row: the agent.tier_routing named bundles
  // the daemon resolved in its /support/choices answer (supChoices.tiers, as
  // agent.spawn resolves a bare bundle name). Picking one calls fill(tier),
  // which fills the popover's editable harness, model, and effort; the
  // launch and a hire still send those concrete fields, and the permission
  // level and plugin isolation stay the user's own. manual() returns the row
  // to manual selection: the popover calls it when the user edits any of the
  // three fields. current() is the picked tier as the latest choices list
  // it (null when manual or no longer pickable), so the popover can re-apply
  // it after a refresh redraws the fields. usable(tier) is the popover's
  // own start check for the
  // tier's harness; a tier it refuses, or one the daemon could not resolve,
  // is listed but cannot be picked. With no bundles configured the row is
  // hidden, so the popover reads as before.
  function leadTierPicker(box, usable, fill) {
    var row = box.appendChild(mk("div", "sprc sptier")), label = row.appendChild(mk("label", null, wsT("js.lead.tier")));
    var sel = row.appendChild(mk("select", "spsm mono")), note = box.appendChild(mk("p", "spdesc sptiernote"));
    sel.id = rpId();
    label.htmlFor = sel.id;
    sel.setAttribute("data-tier-picker", "1");
    var T = { name: "", busy: false };
    function tiers() { return supChoices && Array.isArray(supChoices.tiers) ? supChoices.tiers : []; }
    function find(name) { return tiers().filter(function (t) { return t.name === name; })[0]; }
    function shown(t) { return [t.harness, t.model, t.effort].filter(Boolean).join(" · "); }
    function why(t) { return t.error || (usable(t) ? "" : wsT("js.lead.tier.off", t.harness || "")); }
    // draw lists the tiers against the current availability; an unchanged
    // list is left alone so a redraw never closes an open select.
    T.draw = function () {
      var list = tiers(), sig = JSON.stringify(list.map(function (t) { return [t.name, shown(t), why(t)]; }));
      if (sel.wsSig !== sig) {
        sel.wsSig = sig;
        sel.textContent = "";
        sel.appendChild(mk("option", null, wsT("js.lead.tier.manual"))).value = "";
        list.forEach(function (t) {
          var o = sel.appendChild(mk("option", null, t.name + (t.harness ? " · " + t.harness : "")));
          o.value = t.name;
          o.disabled = !!why(t);
          o.title = why(t) || t.trace || "";
        });
      }
      var cur = T.name && find(T.name);
      if (!cur || why(cur)) T.name = "";
      if (sel.value !== T.name) sel.value = T.name;
      sel.disabled = T.busy || !list.length;
      box.hidden = !list.length;
      cur = T.name && find(T.name);
      note.textContent = cur ? wsT("js.lead.tier.picked", cur.name, shown(cur)) : wsT("js.lead.tier.hint");
    };
    T.manual = function () {
      if (!T.name) return;
      T.name = "";
      T.draw();
    };
    T.reset = function () { T.name = ""; T.draw(); };
    // current is the picked tier as the latest choices list it, when it can
    // still be picked; null for manual, or a tier that vanished or turned
    // unusable in a refreshed list.
    T.current = function () {
      var t = T.name && find(T.name);
      return t && !why(t) ? t : null;
    };
    sel.addEventListener("change", function () {
      var t = find(sel.value);
      if (!t || why(t)) { T.name = ""; T.draw(); return; }
      T.name = t.name;
      fill(t);
      T.draw();
    });
    return T;
  }
