(function () {
  "use strict";

  // Companion Agents card: the Lightweight (body) and Work companion tier
  // selectors with a readback of what each launches with now. The readback
  // comes from /api/companion/agents, which resolves exactly as the next
  // companion launch does (the global value, fallback to auto). Both keys are
  // global-only, so other scopes show the card read-only like its siblings.
  var ROUTE = "/api/companion/agents";
  var ROLES = [
    { id: "body", key: "dashboard.companion_body_tier", hint: "js.settings.companion.agents.body_hint" },
    { id: "work", key: "dashboard.companion_work_tier", hint: "js.settings.companion.agents.work_hint" }
  ];
  var AUTO = "auto";
  // The harness whose consumer terms the lead modal warns about
  // (dashboard-new-session.js shows js.lead.agy.terms for the same name).
  var TERMS_HARNESS = "agy";

  var state = { data: null, loading: false, error: "", seq: 0 };

  function load(rerender) {
    if (state.loading) return;
    var seq = ++state.seq;
    state.loading = true;
    state.error = "";
    fetch(ROUTE, { credentials: "same-origin", cache: "no-store" }).then(function (response) {
      return response.ok ? response.json() : Promise.reject(new Error(String(response.status)));
    }).then(function (body) {
      if (seq !== state.seq) return;
      state.loading = false;
      state.data = body && body.resolved_agents && typeof body.resolved_agents === "object" ? body.resolved_agents : null;
      if (!state.data) state.error = "load_failed";
      rerender();
    }).catch(function () {
      if (seq !== state.seq) return;
      state.loading = false;
      state.error = "load_failed";
      rerender();
    });
  }

  // Reload after a save, keeping the last readback on screen until the new
  // one lands so the selectors do not flicker back to auto.
  function refresh(rerender) {
    state.seq++;
    state.loading = false;
    load(rerender);
  }

  function normalized(value) {
    return typeof value === "string" ? value.trim().toLowerCase() : "";
  }

  // The value shown for the selected scope: its own layer, else the
  // effective value (as the other companion fields show it).
  function currentValue(ctx, entry) {
    if (ctx.scope === "builtin") return normalized(entry.default);
    var own = ctx.selected(entry);
    return normalized(own !== undefined ? own : entry.value);
  }

  function bindingText(tr, item) {
    return [item.model || tr("js.settings.companion.agents.default_model"), item.effort].filter(Boolean).join(" · ");
  }

  function optionLabel(tr, option) {
    if (option.kind === "auto") return tr("js.settings.companion.agents.auto");
    var name = option.kind === "alias" && option.target ? option.value + " → " + option.target : option.value;
    return name + " — " + bindingText(tr, option);
  }

  function readbackText(tr, res) {
    if (!res || !res.available) return tr("js.settings.companion.agents.unavailable");
    return [res.harness, res.model || tr("js.settings.companion.agents.default_model"), res.effort].filter(Boolean).join(" · ");
  }

  function readbackPill(ctx, role) {
    var res = state.data && state.data[role.id];
    var kind = "";
    var text = ctx.tr("js.settings.companion.agents.loading");
    if (state.error) {
      kind = "warn";
      text = ctx.tr("js.settings.companion.agents.load_failed");
    } else if (res) {
      kind = !res.available ? "fail" : res.fallback_reason ? "warn" : "ok";
      text = readbackText(ctx.tr, res);
    }
    var pill = ctx.el("span", "cs-pill cs-agent-readback" + (kind ? " " + kind : ""), text);
    pill.dataset.companionAgentReadback = role.id;
    pill.setAttribute("role", "status");
    if (res && res.bundle) pill.title = res.bundle;
    return pill;
  }

  function buildSelect(ctx, entry, spec, canEdit, current) {
    var select = ctx.el("select", "sel mono cs-agent-select");
    select.disabled = !canEdit;
    select.setAttribute("aria-label", ctx.tr(spec.label));
    var options = state.data && Array.isArray(state.data.options) && state.data.options.length
      ? state.data.options
      : [{ value: AUTO, kind: "auto" }];
    var shown = current === AUTO ? "" : current;
    var known = false;
    for (var option of options) {
      if (!option || typeof option.value !== "string") continue;
      var value = option.kind === "auto" ? "" : option.value;
      if (value === shown) known = true;
      select.add(new Option(optionLabel(ctx.tr, option), value, false, value === shown));
    }
    if (!known && shown) {
      // A value the options do not list (unavailable now, or typed in
      // config) stays selectable so the readback can explain it.
      select.add(new Option(ctx.tr("js.settings.companion.agents.custom", shown), shown, false, true), 1);
    }
    select.addEventListener("change", function () {
      if (!canEdit) return;
      // Both keys are global-only: auto resets the global value, and any
      // other scope is read-only (canEdit above), so no project pin exists.
      var action = select.value ? "set" : "reset";
      Promise.resolve(ctx.submitConfig(entry, action, select.value)).catch(function () { /* surfaced by settings */ })
        .then(function () { refresh(ctx.rerender); });
    });
    return select;
  }

  function renderRole(ctx, role) {
    var entry = ctx.byKey.get(role.key);
    var spec = ctx.specFor(role.key);
    if (!entry || !spec || !ctx.matches(entry, spec)) return null;
    var canEdit = ctx.editable(entry, spec);
    var row = ctx.el("div", "cs-agent-row");
    row.dataset.configKey = role.key;
    row.dataset.companionAgentRole = role.id;

    var prov = ctx.renderKeyProvenance(entry, spec);
    var titles = ctx.append(ctx.el("div", "cs-agent-titles"),
      ctx.el("span", "cs-agent-label", ctx.tr(spec.label)),
      ctx.el("span", "cs-agent-hint", ctx.tr(role.hint)));
    var head = ctx.append(ctx.el("div", "cs-agent-head"), titles, prov.node);
    var control = ctx.append(ctx.el("div", "cs-agent-control"),
      buildSelect(ctx, entry, spec, canEdit, currentValue(ctx, entry)),
      readbackPill(ctx, role));
    row.append(head, control);

    var res = state.data && state.data[role.id];
    if (res && res.fallback_reason) {
      var reason = ctx.el("p", "cs-agent-reason", ctx.tr("js.settings.companion.agents.fallback", res.fallback_reason));
      reason.dataset.companionAgentFallback = role.id;
      row.append(reason);
    }
    if (prov.drawer) row.append(prov.drawer);
    return row;
  }

  // The harness a shown value launches on, from the readback options (which
  // resolve aliases and bundles); a dotted value is read by its prefix only
  // while no readback has loaded. Auto never lands on the terms harness.
  function valueHarness(value) {
    if (!value || value === AUTO) return "";
    var options = state.data && Array.isArray(state.data.options) ? state.data.options : null;
    var hit = options && options.find(function (option) { return option && option.value === value; });
    if (hit) return hit.harness || "";
    return !options && value.indexOf(".") > 0 ? value.split(".")[0] : "";
  }

  // The agy consumer-terms notice of the lead modal (same text), shown once
  // when either listed role is set to launch on it.
  function termsNotice(ctx, roles) {
    var used = roles.some(function (role) {
      var entry = ctx.byKey.get(role.key);
      return entry && valueHarness(currentValue(ctx, entry)) === TERMS_HARNESS;
    });
    if (!used) return null;
    var note = ctx.el("p", "cs-agent-reason cs-agent-terms", ctx.tr("js.lead.agy.terms"));
    note.dataset.companionAgentTerms = "1";
    note.setAttribute("role", "note");
    return note;
  }

  function render(ctx) {
    var shown = [], rows = [];
    ROLES.forEach(function (role) {
      var row = renderRole(ctx, role);
      if (row) { shown.push(role); rows.push(row); }
    });
    if (!rows.length) return null;
    if (!state.data && !state.loading && !state.error) load(ctx.rerender);
    var card = ctx.el("div", "cs-agents-card");
    card.dataset.companionAgents = "1";
    var head = ctx.append(ctx.el("div", "cs-agents-head"),
      ctx.el("span", "cs-agents-title", ctx.tr("js.settings.companion.agents.title")),
      ctx.el("span", "cs-agents-sub", ctx.tr("js.settings.companion.agents.sub")));
    card.append(head, ...rows);
    var terms = termsNotice(ctx, shown);
    if (terms) card.append(terms);
    return card;
  }

  window.WsSettingsCompanionAgents = {
    keys: ROLES.map(function (role) { return role.key; }),
    render: render,
    refresh: refresh,
    resetState: function () {
      state.seq++;
      state.data = null;
      state.loading = false;
      state.error = "";
    }
  };
})();
