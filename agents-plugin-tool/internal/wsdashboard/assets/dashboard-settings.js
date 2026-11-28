var wsSettingsChoiceLabels = {
  "dashboard.companion_director": {
    rules: "js.settings.key.dashboard.companion_director.rules",
    agent: "js.settings.key.dashboard.companion_director.agent"
  }
};

(function () {
  "use strict";
  var view = document.querySelector("[data-settings-view]");
  if (!view) return;
  var query = new URLSearchParams(location.search);
  var modal = query.get("frame") === "1" && query.get("modal") === "1";
  query.delete("frame");
  query.delete("modal");
  var requestedSection = query.get("section");
  var requestedProvider = query.get("provider");
  query.delete("section");
  query.delete("provider");
  if (window.parent === window) {
    location.replace(window.WsShell.pagePath(location));
    return;
  }

  if (modal) document.documentElement.setAttribute("data-settings-modal", "");

  var scopes = ["builtin", "synced", "global", "repo", "project", "session"];
  var sections = ["workflow", "agents", "tier_routing", "account", "voice", "compaction", "adapters", "companion", "profiles", "prompts"];
  var adapters = ["claude", "agy", "dashboard"];
  var tierHarnessOrder = ["claude", "codex", "agy", "werk"];
  var filterLabels = {
    all: "js.settings.filter.all",
    overridden: "js.settings.filter.overridden",
    shadowed: "js.settings.filter.shadowed",
    editable: "js.settings.filter.editable"
  };
  var scopeLabels = {
    builtin: "js.settings.scope.builtin",
    synced: "js.settings.scope.synced",
    global: "js.settings.scope.global",
    repo: "js.settings.scope.repo",
    project: "js.settings.scope.project",
    session: "js.settings.scope.session"
  };
  var sectionLabels = {
    workflow: "js.settings.section.workflow",
    agents: "js.settings.section.agents",
    tier_routing: "js.settings.section.tier_routing",
    account: "js.settings.section.account",
    voice: "js.settings.section.voice",
    compaction: "js.settings.section.compaction",
    adapters: "js.settings.section.adapters",
    companion: "js.settings.section.companion",
    profiles: "js.settings.section.profiles",
    prompts: "js.settings.section.prompts",
    claude: "js.settings.section.claude",
    agy: "js.settings.section.agy",
    dashboard: "js.settings.section.dashboard"
  };
  var sectionNotes = {
    workflow: "js.settings.section_note.workflow",
    agents: "js.settings.section_note.agents",
    tier_routing: "js.settings.section_note.tier_routing",
    account: "js.settings.section_note.account",
    voice: "js.settings.section_note.voice",
    compaction: "js.settings.section_note.compaction",
    adapters: "js.settings.section_note.adapters",
    companion: "js.settings.section_note.companion",
    profiles: "js.settings.section_note.profiles",
    prompts: "js.settings.section_note.prompts"
  };
  var tierLabels = {
    name: "js.settings.tier.name",
    backend: "js.settings.tier.backend",
    model: "js.settings.tier.model",
    effort: "js.settings.tier.effort",
    source: "js.settings.tier.source"
  };
  var reasons = {
    global_only: "js.settings.disabled.global_only",
    builtin_read_only: "js.settings.disabled.builtin_read_only",
    profile_read_only: "js.settings.disabled.profile_read_only",
    setting_read_only: "js.settings.disabled.setting_read_only",
    tier_scope: "js.settings.disabled.tier_scope",
    repo_read_only: "js.settings.disabled.repo_read_only",
    scope_unsupported: "js.settings.disabled.scope_unsupported",
    dependency_unmet: "js.settings.disabled.dependency_unmet",
    requires_native_subagent_off: "js.settings.disabled.requires_native_subagent_off",
    requires_companion_agent_director: "js.settings.disabled.requires_companion_agent_director"
  };
  var errors = {
    invalid_request: "js.settings.error.invalid_request",
    invalid_context: "js.settings.error.invalid_context",
    target_not_found: "js.settings.error.target_not_found",
    forbidden: "js.settings.error.forbidden",
    scope_unsupported: "js.settings.error.scope_unsupported",
    upgrade_required: "js.settings.error.upgrade_required",
    config_error: "js.settings.error.config_error"
  };
  var scope = query.get("scope") || "global";
  var selection = {
    scope: scope,
    target: query.get("target") || "",
    context: {worktree: query.get("worktree") || "", session: query.get("session") || ""}
  };
  if (requestedSection === "account" && requestedProvider === "voice") {
    requestedSection = "voice";
    requestedProvider = "";
  }
  var snapshot = null, failure = "", activeSection = sections.includes(requestedSection) ? requestedSection : "workflow";
  var adapter = "claude", filter = "all", focusedTierProvider = "";
  var buckets = new Map(), warnings = new Map(), openLayers = new Set();
  var catalog = window.WsSettingsCatalog;
  var specs = new Map(catalog.map(function (spec) { return [spec.key, spec]; }));
  var families = catalog.filter(function (spec) { return spec.key.endsWith(".*"); });
  var extension = window.WsSettingsExtension || {};
  var preferences = window.WsSettingsPreferences;
  var preferenceEditor;
  var content = view.querySelector("[data-settings-content]");
  var search = view.querySelector("[data-settings-search]");
  var count = view.querySelector("[data-settings-count]");

  var tr = window.wsT;
  var accountSpec = catalog.find(function (spec) { return spec.control === "werk_endpoints"; });
  var voice = null;
  var voicePanel = null;
  var account = view.hasAttribute("data-settings-account") && accountSpec && window.WsSettingsAccount && window.WsSettingsAccount.create({
    el, button, append, svgIcon, request, showTiers, endpointSpec: accountSpec,
    openVoice: function (provider) { selectSection("voice", provider); },
    isVoiceOpenRouterShared: function () {
      return !!(voice && typeof voice.isSharedOpenRouterKey === "function" && voice.isSharedOpenRouterKey());
    },
    onStatusChange: function () {
      if (voice) voice.render();
    },
    onConfigSnapshot: function (cfg) {
      if (voice) {
        voice.applyConfigSnapshot(cfg);
        if (account && typeof account.syncVoiceState === "function") account.syncVoiceState();
      }
    },
    onChange: function () {
      renderAccountCount();
      content.querySelectorAll("[data-account-tier-provider]").forEach(syncAccountRemedy);
    }
  });
  if (account && typeof account.voiceDeps === "function" && window.WsSettingsVoice && typeof window.WsSettingsVoice.create === "function") {
    voice = window.WsSettingsVoice.create(Object.assign(account.voiceDeps(), {
      onChange: function () {
        if (typeof account.syncVoiceState === "function") account.syncVoiceState();
        renderAccountCount();
      },
      onOpenLocal: function () { selectSection("account", "local"); },
      onOpenAccount: function () { selectSection("account", "openrouter"); }
    }));
    voicePanel = append(el("div", "voice-panel"), voice.element);
  }
  if (!account && activeSection === "account") activeSection = "workflow";
  if (!voice && activeSection === "voice") activeSection = "workflow";

  function el(tag, cls, text) {
    var node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function button(cls, text, handler) {
    var node = el("button", cls, text);
    node.type = "button";
    if (handler) node.addEventListener("click", handler);
    return node;
  }

  function append(parent, ...children) {
    parent.append(...children);
    return parent;
  }

  function send(kind, fields) {
    window.parent.postMessage(Object.assign({wsShell: kind}, fields), location.origin);
  }

  function navigate(href) {
    send(modal ? "settings-navigate" : "nav", {href: href});
  }

  var close = view.querySelector("[data-settings-close]");
  if (close) close.addEventListener("click", function () {
    if (modal) send("settings-close");
  });

  function syncShell() {
    send("head", {html: null, qs: "", pad: 0, top: 0});
    send("title", {title: document.title});
  }

  function publishCompanionContext() {
    send("companion-context", window.WsShell.companionContext("settings"));
  }

  var dcSettingsGen = 0;
  var dcSettingsRefs = new Map();
  // Same-origin ref resolver for the shell's interactive guide overlay
  // (dashboard-daemon-guide.js); an Element cannot cross postMessage.
  window.wsDaemonGuideRef = function (gen, ref) {
    var g = Number(gen) || 0;
    if (!dcSettingsGen || (g && g !== dcSettingsGen)) return { error: "stale_ref" };
    var el = dcSettingsRefs.get(String(ref || ""));
    if (!el) return { error: "ref_not_found" };
    if (!el.isConnected) return { error: "stale_ref" };
    if (el.closest && el.closest('[data-ws-dom-exclude="1"]')) return { error: "target_excluded" };
    return { el: el };
  };
  var dcSettingsSensitiveRe = /(?:password|passwd|secret|token|api[-_\s]?key|credential|cookie|bearer|private[-_\s]?key|auth[-_\s]?key)/i;
  function dcSettingsRedact(s) {
    return String(s || "")
      .replace(/([?&](?:token|login)=)[^&#\s"'<>]+/g, "$1[REDACTED]")
      .replace(/\bBearer\s+[A-Za-z0-9._~+/=-]{8,}/gi, "Bearer [REDACTED]")
      .replace(/(ws_dashboard_(?:localhost|token|screenshot_render)=)[^;\s"'<>]+/g, "$1[REDACTED]")
      .replace(/\b(?:sk-[A-Za-z0-9_-]{16,}|ghp_[A-Za-z0-9]{20,}|AIza[A-Za-z0-9_-]{20,})\b/g, "[REDACTED]");
  }
  function dcSettingsSensitiveInput(el) {
    if (!el || (el.tagName !== "INPUT" && el.tagName !== "TEXTAREA")) return false;
    var t = el.tagName === "INPUT" ? String(el.getAttribute("type") || el.type || "text").toLowerCase() : "textarea";
    if (t === "password") return true;
    if ([el.getAttribute("name"), el.getAttribute("id"), el.getAttribute("autocomplete"), el.getAttribute("placeholder"), el.getAttribute("aria-label"), el.getAttribute("title")].some(function (v) {
      return !!(v && dcSettingsSensitiveRe.test(v));
    })) return true;
    if (el.attributes) {
      for (var i = 0; i < el.attributes.length; i++) {
        var a = el.attributes[i];
        if (a && a.name && a.name.indexOf("data-ws-") === 0 && (dcSettingsSensitiveRe.test(a.name) || dcSettingsSensitiveRe.test(a.value || ""))) return true;
      }
    }
    return false;
  }
  function dcSettingsDataWS(el) {
    if (!el.attributes) return null;
    var out = null;
    for (var i = 0; i < el.attributes.length; i++) {
      var a = el.attributes[i];
      if (a && a.name && a.name.indexOf("data-ws-") === 0) {
        if (!out) out = {};
        out[a.name] = dcSettingsRedact(String(a.value || "").slice(0, 200));
      }
    }
    return out;
  }
  function handleSettingsDaemonBridgeReq(msg) {
    var id = msg.id, op = msg.op, params = msg.params || {};
    function reply(ok, payload, err) {
      send("daemon-bridge-res", { id: id, ok: !!ok, payload: payload || null, error: err || null });
    }
    if (op === "dom.snapshot") {
      dcSettingsGen = Number(params.generation) || 1;
      dcSettingsRefs.clear();
      var maxNodes = Math.min(1500, Math.max(1, Number(params.max_nodes) || 1500));
      var refSeq = Number(params.ref_start) || 0;
      var nodes = [];
      var truncated = false;
      var selector = "button, a[href], input, textarea, select, h1, h2, h3, [role='button'], [role='switch'], [role='tab'], [data-ws-panel]";
      var list = document.body ? document.body.querySelectorAll(selector) : [];
      for (var i = 0; i < list.length; i++) {
        var node = list[i];
        if (node.closest && node.closest('[data-ws-dom-exclude="1"]')) continue;
        if (node.hidden || (node.getClientRects && node.getClientRects().length === 0)) continue;
        if (nodes.length >= maxNodes) { truncated = true; break; }
        var tag = node.tagName.toLowerCase();
        var interactive = tag === "button" || tag === "a" || tag === "input" || tag === "textarea" || tag === "select" || node.hasAttribute("role");
        var entry = { scope: "settings", tag: tag };
        if (node.getAttribute("role")) entry.role = node.getAttribute("role");
        if (interactive) {
          refSeq += 1;
          entry.ref = "g" + dcSettingsGen + "-r" + refSeq;
          dcSettingsRefs.set(entry.ref, node);
        }
        var label = node.getAttribute("aria-label") || node.getAttribute("placeholder") || node.getAttribute("title") || node.textContent || "";
        label = dcSettingsRedact(label.replace(/\s+/g, " ").trim().slice(0, 160));
        if (label) entry.name = label;
        var dws = dcSettingsDataWS(node);
        if (dws) entry.data_ws = dws;
        if (tag === "input" || tag === "textarea" || tag === "select") {
          if (tag === "input") entry.input_type = String(node.getAttribute("type") || node.type || "text").toLowerCase();
          if (dcSettingsSensitiveInput(node)) {
            entry.masked = true;
            if (node.value) entry.value = "[REDACTED]";
          } else if (tag === "input" && (entry.input_type === "checkbox" || entry.input_type === "radio")) {
            entry.checked = !!node.checked;
          } else if (node.value) {
            entry.value = dcSettingsRedact(String(node.value).slice(0, 240));
          }
        }
        if (node.disabled || node.getAttribute("aria-disabled") === "true") entry.disabled = true;
        nodes.push(entry);
      }
      reply(true, { nodes: nodes, truncated: truncated, ref_end: refSeq }, null);
      return;
    }
    if (op === "ui.click_ref" || op === "ui.fill_ref") {
      var gen = Number(params.generation) || 0;
      if (!dcSettingsGen || (gen && gen !== dcSettingsGen)) { reply(false, null, "stale_ref"); return; }
      var el = dcSettingsRefs.get(String(params.ref || ""));
      if (!el) { reply(false, null, "ref_not_found"); return; }
      if (!el.isConnected) { reply(false, null, "stale_ref"); return; }
      if (el.closest && el.closest('[data-ws-dom-exclude="1"]')) { reply(false, null, "denied"); return; }
      if (el.disabled || el.getAttribute("aria-disabled") === "true") { reply(false, null, "denied"); return; }
      if (typeof el.scrollIntoView === "function") el.scrollIntoView({ block: "nearest", inline: "nearest" });
      if (typeof el.focus === "function") el.focus({ preventScroll: true });
      if (op === "ui.click_ref") {
        el.click();
        reply(true, { route: location.pathname }, null);
      } else {
        if (el.readOnly) { reply(false, null, "denied"); return; }
        var masked = dcSettingsSensitiveInput(el);
        el.value = String(params.value === undefined || params.value === null ? "" : params.value);
        el.dispatchEvent(new Event("input", { bubbles: true }));
        el.dispatchEvent(new Event("change", { bubbles: true }));
        reply(true, { route: location.pathname, masked: masked }, null);
      }
      return;
    }
    reply(false, null, "invalid_request");
  }

  window.addEventListener("message", function (event) {
    if (event.source !== window.parent || event.origin !== location.origin) return;
    var message = window.WsShell.message(event.data);
    if (!message) return;
    switch (message.wsShell) {
      case "sync": syncShell(); break;
      case "companion-gate": if (message.active) publishCompanionContext(); break;
      case "theme":
        if (message.theme) document.documentElement.dataset.theme = message.theme;
        else delete document.documentElement.dataset.theme;
        break;
      case "search": search.focus(); break;
      case "daemon-bridge-req": handleSettingsDaemonBridgeReq(message); break;
    }
  });
  document.addEventListener("pointerdown", function () { send("press"); }, true);
  document.addEventListener("keydown", function (event) {
    if (event.code === "Backquote" && event.ctrlKey && !event.altKey && !event.metaKey && !event.shiftKey && !event.isComposing) {
      event.preventDefault();
      if (!modal) send("key", {key: "rail"});
    } else if (event.key === "Escape" && !modal) {
      send("key", {key: "esc"});
    }
  });

  if (modal) document.addEventListener("keydown", function (event) {
    if (event.key !== "Escape" || event.isComposing || event.defaultPrevented) return;
    // Consume before target handlers or native search clearing can erase the
    // inner state; one key press must never also dismiss the outer modal.
    var combo = window.WsSettingsCombobox;
    if (combo && combo.closeActive && combo.closeActive()) {
      event.preventDefault();
      event.stopPropagation();
      return;
    }
    if (document.querySelector("dialog[open]")) return;
    if (event.target === search && search.value) {
      event.preventDefault();
      event.stopPropagation();
      search.value = "";
      search.dispatchEvent(new Event("input", {bubbles: true}));
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    if (!event.repeat) send("settings-close");
  }, true);

  function projectForWorktree(worktree) {
    var gears = window.parent.document.querySelectorAll(".rail [data-settings-worktree]");
    for (var gear of gears) {
      if (gear.dataset.settingsWorktree !== worktree) continue;
      var workspace = gear.closest(".rws");
      var project = workspace && workspace.querySelector(".wsbar [data-scope-entry='project']");
      if (project) return project.dataset.settingsTarget;
    }
    return "";
  }

  function scopeHref(name) {
    if (name === "synced") return "";
    var rememberedProject = new URLSearchParams((modal ? location.hash : window.parent.location.hash).slice(1)).get("project");
    return window.WsShell.settingsScopeHref(name, selection, projectForWorktree, rememberedProject);
  }

  function scopeName(name) {
    return scopes.includes(name) ? tr(scopeLabels[name]) : name;
  }

  function contextMissing(name) {
    if (name !== "repo" && name !== "session") return false;
    if (snapshot && snapshot.entries.length) {
      return snapshot.entries[0].layers.some(function (layer) {
        return layer.scope === name && layer.status === "context_missing";
      });
    }
    return name === "repo" ? !selection.context.worktree : !selection.context.session;
  }

  function svgIcon(name) {
    var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "i");
    svg.setAttribute("aria-hidden", "true");
    var use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    use.setAttribute("href", "#" + name);
    svg.append(use);
    return svg;
  }

  function cloudIcon() {
    var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "i");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    var path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", "M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z");
    svg.append(path);
    return svg;
  }

  function scopeButton(name) {
    var active = name === scope, planned = name === "synced";
    var href = scopeHref(name);
    var cls = "prec-node" + (planned ? " synced" : "") + (active ? " active " + name + "-mode" : "");
    var node = button(cls, scopeName(name), function () { navigate(href); });
    node.dataset.settingsScope = name;
    node.setAttribute("aria-current", active ? "page" : "false");
    node.disabled = !href;
    if (planned) node.title = tr("js.settings.sync.planned");
    else if (contextMissing(name)) node.title = tr("js.settings.context_missing") + (!href ? " · " + tr("js.settings.target_required") : "");
    else if (!href) node.title = tr("js.settings.target_required");
    if (planned || contextMissing(name)) {
      node.append(el("span", "sub", planned ? tr("js.settings.sync.planned") : tr("js.settings.context_missing_short")));
    }
    return node;
  }

  function renderHeader() {
    var display = view.querySelector("[data-settings-target-display]");
    var accountActive = activeSection === "account" || activeSection === "voice";
    view.classList.toggle("account-active", accountActive);
    display.className = "scope-target-pill" + (accountActive ? " scope-machine" : scopes.includes(scope) ? " scope-" + scope : "");
    var label = display.querySelector("span");
    var machine = display.querySelector(".i");
    label.textContent = accountActive ? tr("js.settings.account.machine_target") : scopeName(scope);
    if (accountActive && !machine) display.prepend(svgIcon("i-hosted"));
    if (!accountActive && machine) machine.remove();
    var path = display.querySelector("code");
    path.textContent = accountActive ? "" : snapshot && snapshot.path || (scope === "session" ? selection.target : "");
    path.title = path.textContent;
    path.hidden = !path.textContent;
    var strip = view.querySelector("[data-settings-precedence]");
    var filters = search.closest(".filter-bar");
    if (filters) filters.hidden = accountActive;
    strip.hidden = accountActive;
    if (accountActive) {
      strip.replaceChildren();
      return;
    }
    strip.replaceChildren(el("strong", "prec-lbl", tr("js.settings.precedence")));
    scopes.forEach(function (name, i) {
      if (i) {
        var arrow = el("span", "prec-arrow", "→");
        arrow.setAttribute("aria-hidden", "true");
        strip.append(arrow);
      }
      strip.append(scopeButton(name));
    });
    strip.append(el("span", "prec-hint", tr("js.settings.precedence_hint")));
  }

  function specFor(key) {
    return specs.get(key) || families.find(function (spec) { return key.startsWith(spec.key.slice(0, -1)); });
  }

  function entryID(entry) {
    return JSON.stringify([entry.key, entry.selectors.harness || "", entry.selectors.tier || ""]);
  }

  function selected(entry) {
    var layer = entry.layers.find(function (layer) { return layer.scope === scope; });
    return scope !== "builtin" && layer ? layer.selected : undefined;
  }

  function source(entry) {
    var scopeLabel = entry.source_inherited ? tr("js.settings.inherited_session") : scopeName(entry.source_scope);
    return scopeLabel + (entry.source_bucket ? " · " + bucketName(entry.source_bucket) : "");
  }

  function shadowed(entry) {
    if (entry.source_scope === scope) {
      var bucket = entry.selectors.harness;
      var selectedBucket = bucket === "*" ? (entry.key.startsWith("compaction.") ? "" : "all") : (bucket || "");
      return selected(entry) !== undefined && selectedBucket !== entry.source_bucket;
    }
    return selected(entry) !== undefined || scopes.indexOf(entry.source_scope) > scopes.indexOf(scope);
  }

  function enabledWhenMet(spec) {
    if (!spec || !spec.enabled_when) return true;
    var controller = snapshot && snapshot.entries && snapshot.entries.find(function (item) {
      return item.key === spec.enabled_when.key;
    });
    return !!controller && controller.value === spec.enabled_when.equals;
  }

  function baseEditable(entry) {
    if (scope === "repo") {
      return entry.editable && !!extension.editable && extension.editable(entry, selection) === true;
    }
    return entry.editable;
  }

  function editable(entry, spec) {
    if (spec.control === "profile" || scope === "builtin") return false;
    return baseEditable(entry) && enabledWhenMet(spec);
  }

  function bucketName(name) {
    if (name === "*" || name === "all") return tr("js.settings.bucket.common");
    if (adapters.includes(name)) return sectionName(name);
    if (name === "codex") return "Codex";
    if (name === "werk") return tr("js.settings.section.werk");
    if (name === "default") return tr("js.settings.bucket.default");
    return name;
  }

  function sectionName(name) { return tr(sectionLabels[name]); }

  function shownValue(value, key) {
    var choiceKey = key && typeof value === "string" && wsSettingsChoiceLabels[key] && wsSettingsChoiceLabels[key][value];
    if (choiceKey) return tr(choiceKey);
    if (value === null || value === undefined || value === "") return tr("js.settings.empty_value");
    if (typeof value === "object") {
      if ("backend" in value || "model" in value || "effort" in value) {
        return [value.backend, value.provider, value.model, value.effort].filter(Boolean).join(" · ") || tr("js.settings.empty_value");
      }
      return JSON.stringify(value);
    }
    if (value === true || value === "on") return tr("js.settings.on");
    if (value === false || value === "off") return tr("js.settings.off");
    return String(value);
  }

  function statusText(code) {
    return tr(Object.hasOwn(errors, code) ? errors[code] : errors.config_error);
  }

  async function request(path, body) {
    var opts = {credentials: "same-origin", cache: "no-store"};
    if (body) Object.assign(opts, {
      method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)
    });
    var response = await fetch(path, opts), result;
    try { result = await response.json(); } catch (error) { /* Older daemons send a plain 404. */ }
    if (response.ok && result) return result;
    var err = new Error();
    err.code = result && result.error && result.error.code ||
      (response.status === 404 ? "upgrade_required" : "config_error");
    throw err;
  }

  function preferenceTarget(name) {
    if (name === scope) return selection;
    var href = scopeHref(name);
    if (!href) return null;
    var params = new URL(href, location.origin).searchParams;
    return {scope: name, target: params.get("target") || "", context: selection.context};
  }

  async function changePreferences(operations, target) {
    try {
      if (target.scope === "repo") {
        extension.queue(operations, target);
        return;
      }
      for (var operation of operations) {
        var result = await request("/api/config/" + operation.action, Object.assign({}, target, {
          key: operation.key, selectors: operation.selectors, value: operation.value
        }));
        var affected = new Map((result.affected_entries || [result.entry]).map(item => [entryID(item), item]));
        snapshot.entries = snapshot.entries.map(function (entry) {
          var id = entryID(entry), fresh = affected.get(id);
          affected.delete(id);
          return fresh ? Object.assign({}, fresh, {editable: entry.editable, disabled_reason: entry.disabled_reason}) : entry;
        });
        for (var fresh of affected.values()) {
          // A new slot is writable in every settings scope except built-in.
          snapshot.entries.push(Object.assign({}, fresh, {editable: scope !== "builtin"}));
        }
      }
    } finally {
      renderCounts();
      extension.onMutate({section: activeSection, list: content.querySelector(".klist"),
        entries: sectionEntries(activeSection), bucketState, bucketTitle});
    }
  }

  function buildLayerDrawer(entry, spec, drawerId) {
    var drawer = el("div", "layer-drawer");
    drawer.id = drawerId;
    drawer.setAttribute("role", "region");
    drawer.setAttribute("aria-label", tr("js.settings.layers") + " · " + entry.key);
    drawer.append(el("span", "layer-drawer-lbl", tr("js.settings.layers")));
    var chain = el("div", "layer-chain");
    var ordered = entry.layers.slice().reverse();
    var draft = scope === "repo" && extension.hasDraft && extension.hasDraft(entry, selection);
    ordered.forEach(function (layer, idx) {
      if (idx) {
        var arrow = el("span", "larrow", "→");
        arrow.setAttribute("aria-hidden", "true");
        chain.append(arrow);
      }
      var chip = el("button", "lchip");
      chip.type = "button";
      chip.dataset.settingsLayer = layer.scope;
      chip.dataset.layerStatus = layer.status;
      var from = layer.inherited ? tr("js.settings.inherited_session") : scopeName(layer.scope);
      if (layer.bucket && layer.bucket !== entry.selectors.harness) from += " · " + bucketShort(layer.bucket);
      var isWinner = !draft && !isTierUnconfigured(entry) && layer.scope === entry.source_scope && (!layer.bucket || layer.bucket === entry.source_bucket);
      var isDraftLayer = draft && layer.scope === "repo";
      var valText, isEmpty = false;
      if (isDraftLayer) {
        valText = draft.action === "reset" ? tr("js.settings.proposal.reset") : shownValue(draft.value, entry.key);
      } else if (layer.status === "context_missing") {
        valText = tr("js.settings.context_missing_short");
        isEmpty = true;
      } else if (!layer.evaluated) {
        valText = tr("js.settings.not_applicable");
        isEmpty = true;
      } else if (layer.present) {
        valText = shownValue(layer.value, entry.key);
      } else {
        valText = "—";
        isEmpty = true;
      }
      if (isEmpty && !isWinner) chip.classList.add("is-empty");
      if (isWinner) chip.classList.add("is-winner");
      if (isDraftLayer) chip.classList.add("is-draft");
      if (layer.scope === scope) chip.classList.add("is-active-scope");

      chip.append(el("span", "lscope", from), el("span", "lsep", "·"), el("span", "lval", valText));
      if (isWinner || isDraftLayer) {
        var mark = el("span", "lmark", "✓");
        mark.setAttribute("aria-hidden", "true");
        chip.append(mark);
      }
      var href = layer.scope !== scope ? scopeHref(layer.scope) : "";
      if (href) {
        chip.title = (layer.status === "context_missing" ? tr("js.settings.context_missing") :
          !layer.present && layer.evaluated ? tr("js.settings.no_override") : from + " · " + valText);
        chip.addEventListener("click", function () { navigate(href); });
      } else {
        chip.disabled = true;
        chip.title = layer.status === "context_missing" ? tr("js.settings.context_missing") :
          layer.legacy ? tr("js.settings.legacy_tier") :
          (!layer.present && layer.evaluated ? tr("js.settings.no_override") : valText);
      }
      chain.append(chip);
    });
    drawer.append(chain);
    var noteText = draft ? tr("js.settings.proposal.queued") :
      isTierUnconfigured(entry) ? tr("js.settings.tier.unconfigured_hint") :
      shadowed(entry) ? tr(entry.conditional ? "js.settings.shadow_conditional" : "js.settings.shadow", source(entry)) :
      scope === "builtin" ? tr("js.settings.builtin_value", shownValue(entry.default, entry.key)) :
      selected(entry) !== undefined ? tr("js.settings.override", shownValue(selected(entry), entry.key)) :
      tr(entry.conditional ? "js.settings.value_conditional" : "js.settings.value_effective", shownValue(entry.value, entry.key));
    if (entry.layers.some(function (l) { return l.legacy && l.scope === scope; })) {
      noteText = tr("js.settings.legacy_tier");
    }
    drawer.append(el("span", "layer-drawer-note", noteText));
    return drawer;
  }

  function resetIconButton(cls, label, handler, hiddenPlaceholder) {
    var btn = button("reset-icon-btn reset-btn" + (cls ? " " + cls : "") + (hiddenPlaceholder ? " placeholder" : ""), "", handler);
    btn.title = label;
    btn.setAttribute("aria-label", label);
    btn.append(svgIcon("i-undo"));
    if (hiddenPlaceholder) {
      btn.disabled = true;
      btn.tabIndex = -1;
      btn.setAttribute("aria-hidden", "true");
    }
    return btn;
  }

  function originPillState(entry) {
    var here = selected(entry) !== undefined;
    var bucketSuffix = entry.selectors.harness && entry.selectors.harness !== "default" ? bucketShort(entry.selectors.harness) : "";
    if (isTierUnconfigured(entry)) {
      return {
        text: tr("js.settings.tier.unconfigured"),
        style: "unset",
        title: tr("js.settings.tier.unconfigured_hint") + " · " + tr("js.settings.layers"),
        lock: scope === "builtin"
      };
    }
    if (scope === "builtin") {
      return {
        text: scopeName("builtin"),
        style: "lock",
        title: tr("js.settings.layers") + " · " + tr("js.settings.builtin_value", shownValue(entry.default, entry.key)),
        lock: true
      };
    }
    if (here) {
      var target = scopeName(scope) + (bucketSuffix ? " · " + bucketSuffix : "");
      return {
        text: target,
        style: shadowed(entry) ? "shadowed" : "here",
        title: (shadowed(entry) ? tr(entry.conditional ? "js.settings.shadow_conditional" : "js.settings.shadow", source(entry)) : tr("js.settings.override_scope", target)) + " · " + tr("js.settings.layers"),
        lock: false
      };
    }
    var srcLabel = entry.source_inherited ? tr("js.settings.inherited_session") : scopeName(entry.source_scope);
    if (entry.source_bucket && entry.source_bucket !== "default" && entry.source_bucket !== "*") {
      srcLabel += " · " + bucketShort(entry.source_bucket);
    }
    return {
      text: srcLabel,
      style: shadowed(entry) ? "shadowed" : "",
      title: tr(entry.conditional ? "js.settings.source_conditional" : "js.settings.source", source(entry)) + " · " + tr("js.settings.layers"),
      lock: false
    };
  }

  function provenance(entry, spec, canEdit, reset, onToggleLayer) {
    var node = el("div", "kprov");
    var id = entryID(entry);
    var st = originPillState(entry);
    var isOpen = openLayers.has(id);
    var drawerId = "layer-" + id.replace(/[^a-z0-9_-]+/gi, "-");
    var pill = button("origin-pill" + (st.style ? " " + st.style : ""), "", function () {
      if (openLayers.has(id)) openLayers.delete(id);
      else openLayers.add(id);
      var expanded = openLayers.has(id);
      pill.setAttribute("aria-expanded", String(expanded));
      onToggleLayer(expanded, drawerId);
    });
    if (st.lock) pill.append(svgIcon("i-lock"));
    pill.append(el("span", "lbl", st.text), el("span", "chev", isOpen ? "▴" : "▾"));
    pill.title = st.title;
    pill.setAttribute("aria-expanded", String(isOpen));
    pill.setAttribute("aria-controls", drawerId);
    pill.setAttribute("aria-label", st.text + " — " + tr("js.settings.layers"));
    pill.dataset.settingsOriginPill = "1";
    node.append(pill);

    var here = selected(entry) !== undefined;
    node.append(resetIconButton("", tr("js.settings.reset"), reset, !(here && canEdit)));
    return {node: node, drawerId: drawerId, isOpen: isOpen, pill: pill};
  }

  function localizedWarning(warning) {
    return tr(warning.code === "unverified_harness" ? "js.settings.warning.unverified_harness" : "js.settings.warning.generic");
  }

  function makeSubmit(entry, row, status, replace) {
    return async function (action, value) {
      if (row.getAttribute("aria-busy") === "true") return;
      var hadFocus = row.contains(document.activeElement);
      var id = entryID(entry), operation = {key: entry.key, selectors: entry.selectors, action: action};
      if (action === "set") operation.value = value;
      row.setAttribute("aria-busy", "true");
      status.textContent = tr("js.settings.saving");
      status.className = "hint";
      var inputs = Array.from(row.querySelectorAll("input, select, textarea, button")).filter(function (node) {
        return !node.disabled;
      });
      inputs.forEach(function (node) { node.disabled = true; });
      var queued = false;
      try {
        var pending = extension.submit && extension.submit(operation, selection);
        var result = pending ? await pending :
          await request("/api/config/" + action, Object.assign({}, selection, {key: operation.key, selectors: operation.selectors, value: operation.value}));
        if (result.entry) {
          var affected = new Map((result.affected_entries || [result.entry]).map(item => [entryID(item), item]));
          snapshot.entries = snapshot.entries.map(item => affected.get(entryID(item)) || item);
          warnings.set(id, result.warnings || []);
          if ((entry.key === "dashboard.companion_enabled" || entry.key === "dashboard.companion_director") && activeSection === "companion") {
            renderCounts();
            renderContent();
          } else {
            if (row.isConnected) replace(result.entry, hadFocus);
            refreshDependentRows(entry.key);
            renderCounts();
          }
          if (extension.onMutate) extension.onMutate({
            section: activeSection,
            list: content.querySelector(".klist"),
            entries: sectionEntries(activeSection),
            bucketState: bucketState,
            bucketTitle: bucketTitle
          });
        } else {
          queued = !!pending;
          status.textContent = "";
        }
      } catch (error) {
        status.className = "hint err";
        status.textContent = statusText(error.code);
      } finally {
        inputs.forEach(function (node) { node.disabled = queued && node.classList.contains("save-btn"); });
        row.removeAttribute("aria-busy");
        if (entry.key === preferences?.legacyKey) preferenceEditor?.sync();
      }
    };
  }

  function refreshDependentRows(controllerKey) {
    var existing = new Map();
    content.querySelectorAll(".krow").forEach(function (node) {
      if (node.dataset.configKey) existing.set(node.dataset.configKey, node);
    });
    for (var depSpec of catalog) {
      if (!depSpec.enabled_when || depSpec.enabled_when.key !== controllerKey) continue;
      var depEntry = snapshot && snapshot.entries && snapshot.entries.find(function (item) {
        return item.key === depSpec.key;
      });
      if (!depEntry) continue;
      var node = existing.get(depSpec.key);
      if (Boolean(node) !== matches(depEntry, depSpec)) {
        renderContent();
        return;
      }
      if (node) node.replaceWith(renderRow(depEntry, depSpec));
    }
  }

  function rowReason(entry, spec) {
    if (scope === "builtin") return "builtin_read_only";
    if (spec.control === "profile") return "profile_read_only";
    if (entry.disabled_reason) return entry.disabled_reason;
    if (!baseEditable(entry)) return scope === "repo" ? "repo_read_only" : "scope_unsupported";
    if (!enabledWhenMet(spec)) return (spec.enabled_when && spec.enabled_when.reason) || "dependency_unmet";
    return scope === "repo" ? "repo_read_only" : "scope_unsupported";
  }

  function textInput(value, label, disabled, multiline) {
    var input = el(multiline ? "textarea" : "input", multiline ? "ta-inline" : "inp mono");
    if (!multiline) input.type = "text";
    input.value = value == null ? "" : value;
    input.setAttribute("aria-label", label);
    input.disabled = disabled;
    return input;
  }

  function scalarControl(entry, spec, value, disabled) {
    var label = window.wsT(spec.label);
    if (spec.control === "toggle" && [true, false, "on", "off"].includes(value)) {
      var on = value === true || value === "on";
      var typedBoolean = entry.type === "boolean";
      var toggle = button("tg", "", function () {
        on = !on;
        display();
        toggle.dispatchEvent(new Event("input", {bubbles: true}));
      });
      var word = el("span", "tgl");
      function display() {
        toggle.setAttribute("aria-checked", String(on));
        word.textContent = tr(on ? "js.settings.on" : "js.settings.off");
      }
      toggle.setAttribute("role", "switch");
      toggle.setAttribute("aria-label", label);
      toggle.disabled = disabled;
      display();
      return {node: append(el("div", "ctl-row"), toggle, word), read: function () { return typedBoolean ? on : on ? "on" : "off"; }};
    }
    var choices = entry.enum || (spec.control === "toggle" && entry.type === "boolean" ? [true, false] : []);
    if (spec.control === "choice" && choices.length <= 3 && choices.includes(value)) {
      var current = value;
      var segment = el("div", "seg settings-choice");
      segment.setAttribute("role", "group");
      segment.setAttribute("aria-label", label);
      for (const choice of choices) {
        const option = button("", shownValue(choice, entry.key), function () {
          current = choice;
          for (const other of segment.children) other.setAttribute("aria-pressed", String(other === option));
          segment.dispatchEvent(new Event("input", {bubbles: true}));
        });
        option.dataset.settingsOption = String(choice);
        option.disabled = disabled;
        option.setAttribute("aria-pressed", String(choice === current));
        segment.append(option);
      }
      return {node: segment, read: function () { return current; }};
    }
    if (choices.length) {
      var select = el("select", "sel mono");
      select.setAttribute("aria-label", label);
      select.disabled = disabled;
      if (!choices.includes(value)) {
        var unknown = el("option", "", shownValue(value, entry.key));
        unknown.selected = true;
        unknown.disabled = true;
        select.append(unknown);
      }
      for (var option of choices) {
        var choice = el("option", "", shownValue(option, entry.key));
        choice.value = String(option);
        choice.selected = option === value;
        select.append(choice);
      }
      return {node: select, read: function () { return choices.find(function (choice) { return String(choice) === select.value; }); }};
    }
    var input = textInput(value, label, disabled, spec.control === "textarea" || spec.control === "prompt");
    if (spec.control === "threshold") input.placeholder = "50% · 150k · 50%|150k";
    if (spec.control === "number" && (value == null || value === "" || Number.isSafeInteger(Number(value)))) {
      input.type = "number";
      input.step = "1";
      if (entry.minimum !== undefined) input.min = entry.minimum;
      if (entry.maximum !== undefined) input.max = entry.maximum;
    }
    var node = input;
    if (spec.unit) node = append(el("div", "ctl-row has-unit"), input, el("span", "unit", window.wsT(spec.unit)));
    return {node: node, read: function () {
      if (spec.control !== "number") return input.value;
      var n = Number(input.value);
      if (input.value === "" || !Number.isSafeInteger(n) ||
          (entry.minimum !== undefined && n < entry.minimum) || (entry.maximum !== undefined && n > entry.maximum)) return undefined;
      return entry.type === "integer" ? n : String(n);
    }};
  }

  function bindForm(form, control, read, submit, status) {
    var save = el("button", "save-btn", tr("js.settings.save"));
    save.type = "submit";
    save.disabled = true;
    control.append(save);
    form.addEventListener("input", function () { save.disabled = false; });
    form.addEventListener("change", function () { save.disabled = false; });
    form.addEventListener("submit", function (event) {
      event.preventDefault();
      if (save.disabled) return;
      var value = read();
      if (value === undefined) {
        status.className = "hint err";
        status.textContent = tr("js.settings.error.invalid_request");
        return;
      }
      submit("set", value);
    });
  }

  function renderWarnings(entry) {
    return (warnings.get(entryID(entry)) || []).map(function (warning) {
      var node = el("div", "inline-warn", localizedWarning(warning));
      node.setAttribute("role", "status");
      return node;
    });
  }

  function rowClass(entry, canEdit) {
    return (selected(entry) !== undefined ? " overridden" : "") + (!canEdit ? " disabled" : "");
  }

  function renderRow(entry, spec) {
    var canEdit = editable(entry, spec);
    var row = el("article", "srow krow" + (spec.control === "textarea" ? " srow-multiline" : "") + rowClass(entry, canEdit));
    row.dataset.configKey = entry.key;
    var ctlWrap = el("div", "kctl-wrap");
    var form = el("form", "kctl");
    var status = el("span", "hint");
    status.setAttribute("role", "status");
    var submit = makeSubmit(entry, row, status, function (fresh, moveFocus) {
      var updated = renderRow(fresh, spec);
      if (moveFocus) {
        updated.tabIndex = -1;
        row.replaceWith(updated);
        updated.focus({preventScroll: true});
      } else row.replaceWith(updated);
    });
    var bucket = entry.selectors.harness;
    var keySuffix = bucket ? (bucket === "*" ? (entry.key.startsWith("compaction.") ? "" : ".all") : "." + bucket) : "";
    var label = append(el("div", "klabel-line"), el("span", "klabel", window.wsT(spec.label)), el("code", "skey", entry.key + keySuffix));
    if (spec.selectors.includes("harness")) {
      label.append(el("span", "tag bucket", tr(activeSection === "compaction" ? "js.settings.layer_major_tag" : "js.settings.bucket.tag")));
    }
    if (!canEdit) label.append(el("span", "tag lock", tr(reasons[rowReason(entry, spec)] || reasons.scope_unsupported)));
    var meta = append(el("div", "kmeta"), label, el("p", "kdesc", window.wsT(spec.description)));
    if (spec.selectors.includes("harness") && activeSection !== "compaction") {
      var choices = group(entry.key);
      meta.append(append(el("div", "bucket-bar"), el("span", "blbl", tr("js.settings.bucket.label")), bucketPicker(entry.key, choices, "seg bucket-seg")));
    }
    meta.append(...renderWarnings(entry));
    var value = scope === "builtin" ? entry.default : selected(entry);
    if (value === undefined) value = entry.value;
    if (extension.value) value = extension.value(entry, selection, value);
    if (spec.control === "profile") {
      form.append(el("span", "hint", tr("js.settings.profile_inspection")));
    } else {
      var field = scalarControl(entry, spec, value, !canEdit);
      form.append(field.node);
      if (canEdit) bindForm(form, form, field.read, submit, status);
    }
    ctlWrap.append(form, status);

    var drawerNode = null;
    function syncDrawer(expanded, drawerId) {
      prov.pill.querySelector(".chev").textContent = expanded ? "▴" : "▾";
      if (expanded) {
        if (!drawerNode) drawerNode = buildLayerDrawer(entry, spec, drawerId);
        drawerNode.hidden = false;
        if (!drawerNode.isConnected) row.append(drawerNode);
      } else if (drawerNode) {
        drawerNode.hidden = true;
      }
    }
    var prov = provenance(entry, spec, canEdit, function () { submit("reset"); }, syncDrawer);
    var rail = append(el("div", "sctl-rail"), ctlWrap, prov.node);
    row.append(meta, rail);
    if (prov.isOpen) syncDrawer(true, prov.drawerId);

    if (extension.renderRow) extension.renderRow({
      entry: entry, spec: spec, selection: selection, row: row,
      control: form, form: form, submit: submit, editable: canEdit,
      syncDrawer: function () {
        if (drawerNode && drawerNode.isConnected) {
          var fresh = buildLayerDrawer(entry, spec, prov.drawerId);
          fresh.hidden = drawerNode.hidden;
          drawerNode.replaceWith(fresh);
          drawerNode = fresh;
        }
      }
    });
    return row;
  }

  function currentSessionHarness() {
    var h = snapshot && typeof snapshot.session_harness === "string"
      ? snapshot.session_harness.trim().toLowerCase() : "";
    if (!h) {
      try {
        var badge = window.parent.document.querySelector(".rail .rs.cur .hb[data-h]") ||
          window.parent.document.querySelector(".tph-hs .hb[data-h]");
        if (badge) h = (badge.getAttribute("data-h") || "").trim().toLowerCase();
      } catch (error) { /* Ignore cross-origin or standalone frame reads. */ }
    }
    return tierHarnessOrder.includes(h) ? h : "";
  }

  function tierHarnessChoices(entries) {
    var seen = new Set(entries.map(function (entry) { return entry.selectors.harness; }).filter(function (h) {
      return h && h !== "default";
    }));
    return tierHarnessOrder.filter(function (h) { return seen.has(h); }).concat(
      [...seen].filter(function (h) { return !tierHarnessOrder.includes(h); }).sort()
    );
  }

  function activeTierBucket(entries) {
    var choices = tierHarnessChoices(entries);
    var active = buckets.get("agents.tier");
    if (choices.includes(active)) return active;
    var sessionH = currentSessionHarness();
    if (sessionH && choices.includes(sessionH)) return sessionH;
    return choices[0] || "";
  }

  function isTierUnconfigured(entry) {
    if (!entry || entry.key !== "agents.tier") return false;
    var val = scope === "builtin" ? entry.default : (selected(entry) ?? entry.value);
    return !val || (!val.backend && !val.model);
  }

  function tierHarnessSummary(harness, entries) {
    var matching = entries.filter(function (entry) { return entry.selectors.harness === harness; });
    var ownCount = 0;
    var otherScopes = [];
    var otherCount = 0;
    var allUnset = matching.length > 0;
    var models = [];
    var cb = window.WsSettingsCombobox;
    for (var entry of matching) {
      if (!isTierUnconfigured(entry)) allUnset = false;
      var val = scope === "builtin" ? entry.default : (selected(entry) ?? entry.value);
      if (val && val.model) {
        var modelText = cb ? cb.modelKey(val.model, val.provider) : val.model;
        if (modelText && !models.includes(modelText)) models.push(modelText);
      }
      if (scope !== "builtin" && selected(entry) !== undefined) {
        ownCount++;
      } else {
        var hasOther = false;
        for (var layer of entry.layers) {
          if (layer.scope !== "builtin" && layer.scope !== scope && layer.selected !== undefined) {
            hasOther = true;
            if (!otherScopes.includes(layer.scope)) otherScopes.push(layer.scope);
          }
        }
        if (hasOther) otherCount++;
      }
    }
    var badgeKind = "builtin";
    var badgeText = tr("js.settings.builtin_title");
    var overrideCount = ownCount > 0 ? ownCount : otherCount;
    var overrideScopes = ownCount > 0 ? [scope] : otherScopes;
    if (overrideCount > 0) {
      badgeKind = "ov";
      badgeText = overrideScopes.length === 1
        ? tr("js.settings.tier.badge_overrides", scopeName(overrideScopes[0]), overrideCount)
        : tr("js.settings.tier.badge_overrides_multi", overrideCount);
    } else if (allUnset) {
      badgeKind = "unset";
      badgeText = tr("js.settings.tier.unconfigured");
    }
    return {
      badgeKind: badgeKind,
      badgeText: badgeText,
      overrideCount: overrideCount,
      overrideScopes: overrideScopes,
      allUnset: allUnset,
      modelsText: models.length ? models.join(" · ") : tr("js.settings.tier.model_required")
    };
  }

  function legacyDefaultOverrides(entries) {
    var items = [];
    for (var entry of entries) {
      if (entry.selectors.harness !== "default") continue;
      for (var layer of entry.layers) {
        if (layer.scope === "builtin" || layer.selected === undefined) continue;
        items.push({
          entry: entry,
          tier: entry.selectors.tier,
          scope: layer.scope,
          value: layer.selected
        });
      }
    }
    return items;
  }

  function formatLegacyOverride(item) {
    var val = item.value || {};
    var target = [val.backend, val.model].filter(Boolean).join(" / ") || shownValue(val, item.entry.key);
    return item.tier + " → " + target + " (" + scopeName(item.scope) + ")";
  }

  function accountLink(label, action) {
    return append(button("account-link", tr(label), action), svgIcon("l-chevron-right"));
  }

  function selectSection(name, provider) {
    if (name === "account" && provider === "voice") {
      name = "voice";
      provider = "";
    }
    if (name === "account" && !account) return;
    if (name === "voice" && !voice) return;
    var wasAccount = activeSection === "account";
    var wasVoice = activeSection === "voice";
    var wasMachine = wasAccount || wasVoice;
    var goingToAccount = name === "account";
    var goingToVoice = name === "voice";
    var goingToMachine = goingToAccount || goingToVoice;
    if (wasAccount && !goingToAccount && account) account.closeEditors();
    if (wasVoice && !goingToVoice && voice) voice.closeEditors();
    if (!wasMachine && goingToMachine && account) {
      account.refreshStatus();
      account.ensureEndpoints(true);
    }
    activeSection = name;
    if (name !== "agents") focusedTierProvider = "";
    view.querySelectorAll("[data-settings-section]").forEach(function (item) {
      item.setAttribute("aria-current", String(item.dataset.settingsSection === name));
    });
    if (wasMachine !== goingToMachine) renderHeader();
    renderContent();
    content.scrollTop = 0;
    if (goingToAccount && provider && account) account.focus(provider);
    if (goingToVoice && provider && voice) voice.focus(provider);
  }

  function showTiers(provider) {
    focusedTierProvider = provider;
    search.value = "";
    filter = "all";
    view.querySelector("[data-settings-filters]").querySelectorAll("button").forEach(function (node, i) {
      node.setAttribute("aria-pressed", String(i === 0));
    });
    buckets.set("agents.tier", "werk");
    selectSection("agents");
    var row = content.querySelector("tr.account-focus");
    var tab = content.querySelector("[data-bucket-key='agents.tier'][data-settings-bucket='werk']");
    var destination = row || tab;
    if (destination) {
      destination.focus({preventScroll: true});
      destination.scrollIntoView({block: "nearest"});
    }
  }

  function syncAccountRemedy(remedy) {
    var provider = remedy.dataset.accountTierProvider;
    var issue = provider ? account.providerIssue(provider) : "";
    remedy.hidden = !issue;
    if (remedy.firstChild?.textContent === issue || (!issue && !remedy.firstChild)) return;
    remedy.replaceChildren();
    if (issue) remedy.append(el("span", "", issue), " ",
      accountLink("js.settings.account.manage", function () { selectSection("account", provider); }));
  }

  function tierRow(entry, spec, onMutated) {
    var canEdit = editable(entry, spec), group = el("tbody", "tier-entry" + rowClass(entry, canEdit));
    group.dataset.configKey = entry.key;
    group.dataset.settingsTier = entry.selectors.tier;
    var unset = isTierUnconfigured(entry);
    if (unset) group.dataset.tierUnconfigured = "1";
    var row = el("tr", "tier-main-row"), secondary = el("tr", "tier-secondary");
    var cell = el("td", "tname", entry.selectors.tier);
    var fields = ["backend", "model", "effort"];
    var value = (scope === "builtin" ? entry.default : selected(entry) ?? entry.value) || {};
    var combos = {};
    var cb = window.WsSettingsCombobox;
    var inputs = fields.map(function (field) {
      var input = textInput(field === "model" ? cb.modelKey(value.model, value.provider) : value[field], entry.selectors.tier + " · " + tr(tierLabels[field]), !canEdit);
      if (unset) {
        if (field === "backend") input.placeholder = entry.selectors.harness;
        else if (field === "model") {
          input.placeholder = tr("js.settings.tier.unset_model_placeholder");
          input.classList.add("is-unset");
        } else if (field === "effort") input.placeholder = "—";
      }
      var combo = cb.attach(input, {
        getOptions: function () {
          if (field === "backend") return cb.backendOptions();
          var backendVal = (inputs[0] ? inputs[0].value : value.backend) || entry.selectors.harness;
          return field === "model"
            ? cb.modelOptions(backendVal)
            : cb.effortOptions(backendVal);
        }
      });
      combos[field] = combo;
      row.append(el("td"));
      row.lastChild.append(combo.node);
      return input;
    });
    inputs[0].addEventListener("input", function () {
      combos.model.refresh();
      combos.effort.refresh();
    });
    row.prepend(cell);
    if (account && entry.selectors.harness === "werk") {
      var remedy = el("div", "account-tier-remedy");
      var modelCell = row.children[2];
      modelCell.append(remedy);
      function syncProvider() {
        var backend = inputs[0].value.trim().toLowerCase() || "werk";
        var model = cb.modelSelection(inputs[1].value, value.provider);
        var provider = backend === "werk" && model.model ? model.provider || "openrouter" : "";
        remedy.dataset.accountTierProvider = provider;
        syncAccountRemedy(remedy);
        var highlighted = !!focusedTierProvider && (provider === focusedTierProvider ||
          focusedTierProvider === "local" && provider.startsWith("local/"));
        row.classList.toggle("account-focus", highlighted);
        if (highlighted) row.tabIndex = -1;
        else row.removeAttribute("tabindex");
      }
      inputs[0].addEventListener("input", syncProvider);
      inputs[1].addEventListener("input", syncProvider);
      syncProvider();
    }
    var status = el("span", "hint");
    status.setAttribute("role", "status");
    var submit = makeSubmit(entry, group, status, function (fresh) {
      group.replaceWith(tierRow(fresh, spec, onMutated));
      if (onMutated) onMutated();
    });
    var layerTr = null;
    function syncTierDrawer(expanded, drawerId) {
      prov.pill.querySelector(".chev").textContent = expanded ? "▴" : "▾";
      if (expanded) {
        if (!layerTr) {
          layerTr = el("tr", "layer-tr");
          var td = el("td");
          td.colSpan = 5;
          td.append(buildLayerDrawer(entry, spec, drawerId));
          layerTr.append(td);
        }
        layerTr.hidden = false;
        if (!layerTr.isConnected) row.after(layerTr);
      } else if (layerTr) {
        layerTr.hidden = true;
      }
    }
    var prov = provenance(entry, spec, canEdit, function () { submit("reset"); }, syncTierDrawer);
    var provCell = el("div", "tier-prov-cell");
    var save = null;
    function syncSecondary() {
      secondary.hidden = !status.textContent && !secondary.querySelector(".inline-warn");
    }
    if (canEdit) {
      save = button("save-btn", tr("js.settings.save"), function () {
        var next = Object.fromEntries(fields.map(function (field, i) { return [field, inputs[i].value]; }));
        var backend = next.backend.trim().toLowerCase() || entry.selectors.harness;
        var selection = cb.modelSelection(next.model, value.provider);
        if (entry.selectors.harness !== "werk" || backend !== "werk") selection.provider = "";
        Object.assign(next, selection);
        submit("set", next).then(syncSecondary, syncSecondary);
      });
      save.disabled = true;
      group.addEventListener("input", function () {
        save.disabled = false;
      });
      provCell.append(save);
    }
    provCell.append(prov.node);
    row.append(append(el("td"), provCell));

    var actions = append(el("td"), status, ...renderWarnings(entry));
    actions.colSpan = 5;
    secondary.append(actions);
    syncSecondary();
    group.append(row);
    if (prov.isOpen) syncTierDrawer(true, prov.drawerId);
    group.append(secondary);
    return group;
  }

  function renderTier(entries, spec) {
    var wrapper = el("div", "tier-section");
    var currentEntries = function () { return group(spec.key); };
    var choices = tierHarnessChoices(entries);
    var active = activeTierBucket(entries);
    var sessionH = currentSessionHarness();
    var card = el("div", "sgroup tier-group" + (!entries.some(function (e) { return editable(e, spec); }) ? " disabled" : ""));
    var headingLine = append(el("div", "klabel-line"), el("b", "klabel", window.wsT(spec.label)),
      el("code", "skey", spec.key), el("span", "tag bucket", tr("js.settings.bucket.tag")));
    var firstVisible = entries.find(function (entry) { return entry.selectors.harness === active && matches(entry, spec); });
    if (firstVisible && !editable(firstVisible, spec)) {
      headingLine.append(el("span", "tag lock", tr(reasons[rowReason(firstVisible, spec)] || reasons.scope_unsupported)));
    }
    var heading = append(el("div", "kmeta"), headingLine, el("p", "kdesc", window.wsT(spec.description)));

    var tabBar = el("div", "tier-htabs");
    tabBar.setAttribute("role", "tablist");
    tabBar.setAttribute("aria-label", tr("js.settings.tier.harness_select"));
    var tabNodes = new Map();

    choices.forEach(function (choice) {
      var badgeEl = el("span", "tier-htab-badge");
      var subEl = el("div", "tier-htab-sub");
      var hBadge = el("span", "hb");
      hBadge.dataset.h = choice;
      hBadge.setAttribute("aria-hidden", "true");
      var tab = button("tier-htab", "", function () {
        if (choice === active) return;
        active = choice;
        focusedTierProvider = "";
        buckets.set(spec.key, choice);
        syncTabSelection();
        syncActiveBar();
        syncTable();
      });
      tab.setAttribute("role", "tab");
      tab.dataset.bucketKey = spec.key;
      tab.dataset.settingsBucket = choice;
      tab.append(
        append(el("div", "tier-htab-top"), append(el("span", "tier-htab-id"), hBadge, bucketName(choice)), badgeEl),
        subEl
      );
      tabNodes.set(choice, { tab: tab, badge: badgeEl, sub: subEl });
      tabBar.append(tab);
    });

    var top = append(el("div", "tier-group-head"), append(el("div", "tier-head-top"), heading), tabBar);
    var activeBar = el("div", "tier-active-bar");
    var activeBarBadge = el("span", "hb");
    activeBarBadge.setAttribute("aria-hidden", "true");
    var activeBarTitle = el("strong", "");
    var activeBarNote = el("span", "tier-active-note");
    activeBar.append(append(el("span", "tier-active-title"), activeBarBadge, activeBarTitle), activeBarNote);

    var table = el("table", "tier-tbl");
    var colgroup = el("colgroup");
    ["78px", "120px", "", "108px", "232px"].forEach(function (w) {
      var col = el("col");
      if (w) col.style.width = w;
      colgroup.append(col);
    });
    var headers = ["name", "backend", "model", "effort", "source"].map(function (key) {
      var th = el("th", "", tr(tierLabels[key]));
      th.scope = "col";
      return th;
    });
    table.append(colgroup, append(el("thead"), append(el("tr"), ...headers)));

    function syncTabCard(choice) {
      var refs = tabNodes.get(choice);
      if (!refs) return;
      var sum = tierHarnessSummary(choice, currentEntries());
      refs.badge.className = "tier-htab-badge " + sum.badgeKind;
      refs.badge.textContent = sum.badgeText;
      refs.sub.textContent = sum.modelsText;
      refs.sub.title = sum.modelsText;
    }

    function syncTabSelection() {
      tabNodes.forEach(function (refs, choice) {
        refs.tab.setAttribute("aria-selected", String(choice === active));
      });
    }

    function syncActiveBar() {
      if (!active) {
        activeBar.hidden = true;
        return;
      }
      activeBar.hidden = false;
      activeBarBadge.dataset.h = active;
      activeBarTitle.textContent = tr("js.settings.tier.matrix_title", bucketName(active));
      var sum = tierHarnessSummary(active, currentEntries());
      var parts = [];
      if (sessionH && sessionH === active) parts.push(tr("js.settings.tier.current_session"));
      if (sum.allUnset) {
        parts.push(tr("js.settings.tier.unconfigured_hint"));
      } else if (sum.overrideCount > 0) {
        parts.push(tr("js.settings.tier.overrides_active", sum.overrideScopes.map(scopeName).join(", "), sum.overrideCount));
      } else {
        parts.push(tr("js.settings.tier.builtin_active"));
      }
      activeBarNote.textContent = parts.join(" · ");
    }

    function onTierMutated() {
      syncTabCard(active);
      syncActiveBar();
      syncLegacyBanner();
    }

    function syncTable() {
      if (account && active === "werk") account.ensureEndpoints();
      Array.from(table.querySelectorAll("tbody")).forEach(function (tb) { tb.remove(); });
      var visible = currentEntries().filter(function (entry) {
        return entry.selectors.harness === active && matches(entry, spec);
      });
      visible.forEach(function (entry) {
        table.append(tierRow(entry, spec, onTierMutated));
      });
    }

    function legacyClearTarget(itemScope) {
      var target = preferenceTarget(itemScope);
      if (target) return target;
      if (itemScope === "project") {
        return { scope: "project", target: "", context: selection.context };
      }
      return null;
    }

    var legacyBanner = null;
    function syncLegacyBanner() {
      var items = legacyDefaultOverrides(currentEntries());
      if (!items.length) {
        if (legacyBanner) {
          legacyBanner.remove();
          legacyBanner = null;
        }
        return;
      }
      var banner = el("div", "tier-legacy");
      banner.setAttribute("role", "status");
      banner.dataset.settingsTierLegacy = "1";
      var summary = items.map(formatLegacyOverride).join(", ");
      var text = el("span", "tier-legacy-text", tr("js.settings.tier.legacy_default_banner", items.length, summary));
      var clearable = items.filter(function (item) { return Boolean(legacyClearTarget(item.scope)); });
      var errHint = el("span", "hint err");
      var clearBtn = button("tier-legacy-btn", tr("js.settings.tier.legacy_default_clear"), async function () {
        if (clearBtn.disabled) return;
        clearBtn.disabled = true;
        clearBtn.textContent = tr("js.settings.tier.legacy_default_clearing");
        errHint.textContent = "";
        var mutated = false;
        try {
          for (var item of clearable) {
            var target = legacyClearTarget(item.scope);
            if (!target) continue;
            var result = await request("/api/config/reset", Object.assign({}, target, {
              key: spec.key,
              selectors: { tier: item.tier, harness: "default" }
            }));
            if (result && result.entry) {
              mutated = true;
              var affected = new Map((result.affected_entries || [result.entry]).map(function (it) { return [entryID(it), it]; }));
              snapshot.entries = snapshot.entries.map(function (it) {
                var fresh = affected.get(entryID(it));
                return fresh ? Object.assign({}, fresh, { editable: it.editable, disabled_reason: it.disabled_reason }) : it;
              });
            }
          }
          renderCounts();
          choices.forEach(syncTabCard);
          syncActiveBar();
          syncTable();
          syncLegacyBanner();
        } catch (error) {
          if (mutated) {
            renderCounts();
            choices.forEach(syncTabCard);
            syncActiveBar();
            syncTable();
          }
          clearBtn.disabled = false;
          clearBtn.textContent = tr("js.settings.tier.legacy_default_clear");
          errHint.textContent = statusText(error.code);
        }
      });
      clearBtn.dataset.settingsTierLegacyClear = "1";
      clearBtn.disabled = scope === "builtin" || !clearable.length;
      banner.append(text, clearBtn);
      if (errHint) banner.append(errHint);
      if (legacyBanner) legacyBanner.replaceWith(banner);
      else wrapper.append(banner);
      legacyBanner = banner;
    }

    choices.forEach(syncTabCard);
    syncTabSelection();
    syncActiveBar();
    syncTable();
    card.append(top, activeBar, append(el("div", "tier-scroll"), table));
    wrapper.append(card);
    syncLegacyBanner();
    return wrapper;
  }

  // tierRoutingRow is the agent.tier_routing graph editor
  // (dashboard-settings-tiers.js); a save refreshes this row's entry.
  function tierRoutingRow(entry, spec) {
    return window.WsSettingsTierRouting.render({
      entry: entry, editable: editable(entry, spec), label: window.wsT(spec.label), description: window.wsT(spec.description),
      lockText: tr(reasons[rowReason(entry, spec)] || reasons.scope_unsupported),
      onSaved: function (result) {
        if (!result || !result.entry) return;
        var affected = new Map((result.affected_entries || [result.entry]).map(item => [entryID(item), item]));
        snapshot.entries = snapshot.entries.map(item => affected.get(entryID(item)) || item);
        renderCounts();
      }
    });
  }

  // agent.tier_aliases is deprecated in favor of agent.tier_routing: its
  // catalog row has the deprecated surface, so no tab renders a control or
  // counts it. A stored (non-builtin) value surfaces only here, as a banner
  // on the tier routing page that clears it.
  var tierAliasesKey = "agent.tier_aliases";

  function tierAliasOverrides() {
    var items = [];
    for (var entry of snapshot ? snapshot.entries : []) {
      if (entry.key !== tierAliasesKey) continue;
      for (var layer of entry.layers) {
        if (layer.scope === "builtin" || layer.selected === undefined) continue;
        items.push({entry: entry, scope: layer.scope, value: layer.selected});
      }
    }
    return items;
  }

  function tierAliasesBanner() {
    var items = tierAliasOverrides();
    if (!items.length) return null;
    var banner = el("div", "tier-legacy tier-aliases-legacy");
    banner.setAttribute("role", "status");
    banner.dataset.settingsTierAliases = "1";
    var summary = items.map(function (item) {
      return shownValue(item.value, item.entry.key) + " (" + scopeName(item.scope) + ")";
    }).join(", ");
    var text = el("span", "tier-legacy-text", tr("js.settings.tier_routing.aliases_banner", summary));
    var clearable = items.filter(function (item) { return Boolean(preferenceTarget(item.scope)); });
    var errHint = el("span", "hint err");
    var clearBtn = button("tier-legacy-btn", tr("js.settings.tier_routing.aliases_clear"), async function () {
      if (clearBtn.disabled) return;
      clearBtn.disabled = true;
      clearBtn.textContent = tr("js.settings.tier.legacy_default_clearing");
      errHint.textContent = "";
      try {
        for (var item of clearable) {
          var result = await request("/api/config/reset", Object.assign({}, preferenceTarget(item.scope), {
            key: tierAliasesKey, selectors: item.entry.selectors
          }));
          if (!result || !result.entry) continue;
          var affected = new Map((result.affected_entries || [result.entry]).map(function (it) { return [entryID(it), it]; }));
          snapshot.entries = snapshot.entries.map(function (it) {
            var fresh = affected.get(entryID(it));
            return fresh ? Object.assign({}, fresh, {editable: it.editable, disabled_reason: it.disabled_reason}) : it;
          });
        }
        renderCounts();
        var next = tierAliasesBanner();
        if (next) banner.replaceWith(next);
        else banner.remove();
      } catch (error) {
        renderCounts();
        clearBtn.disabled = false;
        clearBtn.textContent = tr("js.settings.tier_routing.aliases_clear");
        errHint.textContent = statusText(error.code);
      }
    });
    clearBtn.dataset.settingsTierAliasesClear = "1";
    clearBtn.disabled = scope === "builtin" || !clearable.length;
    banner.append(text, clearBtn, errHint);
    return banner;
  }

  function group(key) {
    return snapshot.entries.filter(function (entry) { return entry.key === key; });
  }

  function harnessChoices(entries) {
    var order = ["*", "default", "claude", "codex", "agy", "werk"];
    return [...new Set(entries.map(function (entry) { return entry.selectors.harness; }).filter(Boolean))].sort(function (a, b) {
      return (order.indexOf(a) + order.length + 1) % (order.length + 1) - (order.indexOf(b) + order.length + 1) % (order.length + 1);
    });
  }

  function activeBucket(key, entries) {
    var choices = harnessChoices(entries), active = buckets.get(key);
    return choices.includes(active) ? active : choices[0];
  }

  function bucketShort(name) {
    if (name === "*" || name === "all") return "*";
    return bucketName(name);
  }

  function bucketState(entries) {
    if (scope === "repo" && extension.hasDraft && entries.some(function (e) { return extension.hasDraft(e, selection); })) {
      return {kind: "draft", scopes: ["repo"]};
    }
    if (scope !== "builtin" && entries.some(function (e) { return selected(e) !== undefined; })) {
      return {kind: "own", scopes: [scope]};
    }
    var other = [];
    for (var e of entries) {
      for (var layer of e.layers) {
        if (layer.scope !== "builtin" && layer.scope !== scope && layer.selected !== undefined && !other.includes(layer.scope)) {
          other.push(layer.scope);
        }
      }
    }
    return other.length ? {kind: "other", scopes: other} : {kind: "none", scopes: []};
  }

  function bucketTitle(choice, st) {
    var base = choice === "*" ? tr("js.settings.bucket.common") : bucketName(choice);
    if (st.kind === "draft") return base + " · " + tr("js.settings.proposal.queued");
    if (st.kind === "own" || st.kind === "other") {
      return base + " · " + tr("js.settings.bucket.overridden_in", st.scopes.map(scopeName).join(", "));
    }
    return base;
  }

  function bucketPicker(key, entries, cls) {
    var strip = el("div", cls || "seg bucket-seg"), choices = harnessChoices(entries), active = activeBucket(key, entries);
    strip.setAttribute("role", "group");
    strip.setAttribute("aria-label", tr("js.settings.bucket.label"));
    choices.forEach(function (choice) {
      var matching = entries.filter(function (entry) { return entry.selectors.harness === choice; });
      var st = bucketState(matching);
      var node = button("", bucketShort(choice), function () {
        buckets.set(key, choice);
        renderContent();
        var match = Array.from(content.querySelectorAll("[data-settings-bucket]")).find(function (other) {
          return other.dataset.settingsBucket === choice && other.dataset.bucketKey === key;
        });
        if (match) match.focus({preventScroll: true});
      });
      node.dataset.settingsBucket = choice;
      node.dataset.bucketKey = key;
      node.setAttribute("aria-pressed", String(choice === active));
      node.title = bucketTitle(choice, st);
      if (st.kind !== "none") {
        node.dataset.bucketOverride = st.kind;
        var dot = el("span", "bdot " + st.kind);
        dot.setAttribute("aria-hidden", "true");
        node.append(dot);
      }
      strip.append(node);
    });
    return strip;
  }

  function matches(entry, spec) {
    var term = search.value.trim().toLocaleLowerCase();
    var text = [entry.key, window.wsT(spec.label), window.wsT(spec.description)].join(" ").toLocaleLowerCase();
    if (term && !text.includes(term)) return false;
    if (filter === "overridden") return selected(entry) !== undefined;
    if (filter === "shadowed") return shadowed(entry);
    if (filter === "editable") return editable(entry, spec);
    return true;
  }

  // Rows with a surface other than the panel (the deprecated aliases no UI
  // renders) stay in the catalog for inspection but are not rendered here.
  function sectionEntries(name) {
    return snapshot ? snapshot.entries.filter(function (entry) {
      var spec = specFor(entry.key);
      return spec && !spec.surface && (name === "adapters" ? adapters.includes(spec.section) : spec.section === name);
    }) : [];
  }

  function renderCounts() {
    var n = snapshot ? snapshot.entries.filter(function (entry) { return selected(entry) !== undefined; }).length : 0;
    count.textContent = snapshot ? tr("js.settings.override_count", n) : "";
    renderAccountCount();
    for (var node of view.querySelectorAll("[data-settings-section]:not([data-settings-section='account']):not([data-settings-section='voice'])")) {
      var total = sectionEntries(node.dataset.settingsSection).filter(function (entry) { return selected(entry) !== undefined; }).length;
      node.querySelector(".cnt").textContent = total || "";
      node.querySelector(".dot").classList.toggle("none", !total);
    }
  }

  function renderAccountCount() {
    if (!account) return;
    var node = view.querySelector("[data-settings-section='account']");
    if (!node) return;
    var total = account.attention();
    node.querySelector(".cnt").textContent = total || "";
    node.querySelector(".dot").classList.toggle("none", !total);
  }

  function renderSections() {
    var nav = view.querySelector("[data-settings-sections]");
    var visibleSections = sections.filter(function (name) {
      if (name === "account") return !!account;
      if (name === "voice") return !!voice;
      return true;
    });
    var navItems = [];
    var hasAuthGroup = visibleSections.includes("account") || visibleSections.includes("voice");
    var authGroupOpened = false;
    visibleSections.forEach(function (name, idx) {
      var isAuth = name === "account" || name === "voice";
      if (isAuth && hasAuthGroup && !authGroupOpened) {
        authGroupOpened = true;
        var grpLbl = el("div", "snav-group-lbl", tr("js.settings.nav.account_auth"));
        grpLbl.dataset.settingsNavGroup = "account-auth";
        grpLbl.setAttribute("aria-hidden", "true");
        navItems.push(grpLbl);
      }
      var node = button("snav-btn", "", function () { selectSection(name); });
      node.dataset.settingsSection = name;
      node.setAttribute("aria-current", String(name === activeSection));
      navItems.push(append(node, el("span", "dot none"), el("span", "", sectionName(name)), el("span", "cnt")));
      var nextIsAuth = idx + 1 < visibleSections.length && (visibleSections[idx + 1] === "account" || visibleSections[idx + 1] === "voice");
      if (isAuth && !nextIsAuth && idx + 1 < visibleSections.length) {
        var sep = el("div", "snav-sep");
        sep.setAttribute("aria-hidden", "true");
        navItems.push(sep);
      }
    });
    nav.replaceChildren(...navItems);
    var bar = view.querySelector("[data-settings-filters]");
    bar.replaceChildren(...Object.keys(filterLabels).map(function (name) {
      var node = button("fchip", tr(filterLabels[name]), function () {
        filter = name;
        for (var item of bar.children) item.setAttribute("aria-pressed", String(item === node));
        renderContent();
      });
      node.setAttribute("aria-pressed", String(name === filter));
      return node;
    }));
    renderCounts();
  }

  function syncCard() {
    var toggle = button("tg", "");
    toggle.disabled = true;
    toggle.setAttribute("role", "switch");
    toggle.setAttribute("aria-checked", "false");
    toggle.setAttribute("aria-label", tr("js.settings.sync.title"));
    var sync = button("btn", tr("js.settings.sync.disabled"));
    sync.disabled = true;
    return append(el("div", "sync-card"), append(el("div", "sync-card-left"),
      append(el("div", "sync-card-title"), cloudIcon(), el("strong", "", tr("js.settings.sync.title")), el("span", "tag", tr("js.settings.sync.planned"))),
      el("div", "sync-card-desc", tr("js.settings.sync.description"))),
      append(el("div", "settings-sync-actions"), toggle, sync));
  }

  async function submitConfig(entry, action, value) {
    var operation = { key: entry.key, selectors: entry.selectors || {}, action: action };
    if (action === "set") operation.value = value;
    var pending = extension.submit && extension.submit(operation, selection);
    var result = pending ? await pending :
      await request("/api/config/" + action, Object.assign({}, selection, {
        key: operation.key, selectors: operation.selectors, value: operation.value
      }));
    if (result && result.entry) {
      var affected = new Map((result.affected_entries || [result.entry]).map(function (item) { return [entryID(item), item]; }));
      snapshot.entries = snapshot.entries.map(function (item) { return affected.get(entryID(item)) || item; });
      warnings.set(entryID(entry), result.warnings || []);
      renderCounts();
      renderContent();
      if (extension.onMutate) extension.onMutate({
        section: activeSection,
        list: content.querySelector(".klist"),
        entries: sectionEntries(activeSection),
        bucketState: bucketState,
        bucketTitle: bucketTitle
      });
    }
    return result;
  }

  function renderKeyProvenance(entry, spec) {
    var canEdit = editable(entry, spec);
    var drawerNode = null;
    function syncDrawer(expanded, drawerId) {
      prov.pill.querySelector(".chev").textContent = expanded ? "▴" : "▾";
      if (expanded) {
        if (!drawerNode) drawerNode = buildLayerDrawer(entry, spec, drawerId);
        drawerNode.hidden = false;
        if (!drawerNode.isConnected) {
          var host = prov.node.closest("[data-config-key], .cs-wardrobe-field, .cs-group");
          if (host) {
            var groupHead = host.classList.contains("cs-group") ? host.querySelector(":scope > .cs-group-head") : null;
            if (groupHead) groupHead.after(drawerNode);
            else host.append(drawerNode);
          }
        }
      } else if (drawerNode) {
        drawerNode.hidden = true;
      }
    }
    var prov = provenance(entry, spec, canEdit, function () {
      submitConfig(entry, "reset", "");
    }, syncDrawer);
    if (prov.isOpen) {
      drawerNode = buildLayerDrawer(entry, spec, prov.drawerId);
      drawerNode.hidden = false;
    }
    return { node: prov.node, drawer: drawerNode };
  }

  function renderContent() {
    if (activeSection === "account") {
      if (!account.panel.isConnected) {
        preferenceEditor = null;
        content.replaceChildren(append(el("header", "sec-head"), el("h2", "", sectionName("account")),
          el("p", "", tr(sectionNotes.account))), account.panel);
      }
      account.ensureEndpoints();
      return;
    }
    if (activeSection === "voice" && voice && voicePanel) {
      if (!voicePanel.isConnected) {
        preferenceEditor = null;
        content.replaceChildren(append(el("header", "sec-head"), el("h2", "", sectionName("voice")),
          el("p", "", tr(sectionNotes.voice))), voicePanel);
      }
      if (account) account.ensureEndpoints();
      return;
    }
    preferenceEditor = null;
    content.replaceChildren();
    if (!snapshot) {
      var note = el("p", "info-note", failure ? statusText(failure) : tr("js.settings.loading"));
      note.setAttribute("role", failure ? "alert" : "status");
      content.append(note);
      return;
    }
    if (scope === "project") content.append(el("p", "info-note", tr("js.settings.project_path")));
    if (scope === "repo" || scope === "builtin") {
      var banner = scope + "-banner";
      var left = el("div", banner + "-left");
      left.append(
        append(el("div", banner + "-title"), svgIcon("i-lock"), el("b", "", tr(scope === "repo" ? "js.settings.repo_title" : "js.settings.builtin_title"))),
        el("span", banner + "-desc", tr(scope === "builtin" ? "js.settings.builtin_description" : extension.editable ? "js.settings.repo_proposal_description" : "js.settings.repo_description"))
      );
      content.append(append(el("div", banner), left));
    }
    var heading = append(el("header", "sec-head"), el("h2", "", sectionName(activeSection)),
      el("p", "", tr(sectionNotes[activeSection])));
    if (activeSection === "workflow" && scope === "global") content.append(syncCard());
    content.append(heading);
    if (activeSection === "agents" && account) content.append(append(el("p", "info-note account-tier-note"),
      tr("js.settings.account.tiers_note"), " ", accountLink("js.settings.account.manage", function () { selectSection("account"); })));
    if (activeSection === "tier_routing") {
      var aliasesBanner = tierAliasesBanner();
      if (aliasesBanner) content.append(aliasesBanner);
    }
    if (activeSection === "adapters") {
      var tabs = append(el("div", "subtabs"), ...adapters.map(function (name) {
        var tab = button("subtab", sectionName(name), function () {
          adapter = name;
          renderContent();
          content.querySelector(".subtab[aria-pressed=true]").focus();
        });
        tab.setAttribute("aria-pressed", String(adapter === name));
        var size = catalog.filter(function (spec) { return spec.section === name; }).length;
        return append(tab, el("span", "scnt", size));
      }));
      content.append(tabs);
    }
    var entries = sectionEntries(activeSection);
    if (activeSection === "compaction") {
      var compaction = entries.filter(function (entry) { return entry.key.startsWith("compaction."); });
      content.append(append(el("div", "info-note compaction-buckets"), el("span", "", tr("js.settings.compaction_precedence")),
        bucketPicker("compaction", compaction, "seg bucket-seg")));
    }
    var byKey = new Map();
    for (var entry of entries) {
      if (!byKey.has(entry.key)) byKey.set(entry.key, []);
      byKey.get(entry.key).push(entry);
    }
    var list = el("div", "klist");
    if (activeSection === "companion" && window.WsSettingsCompanion) {
      var companionPanel = window.WsSettingsCompanion.render({
        el: el, button: button, append: append, svgIcon: svgIcon, tr: tr,
        scope: scope, entries: entries, specFor: specFor,
        matches: matches, editable: editable, selected: selected,
        filter: filter, searchTerm: search.value.trim().toLocaleLowerCase(),
        renderRow: renderRow,
        renderKeyProvenance: renderKeyProvenance,
        submitConfig: submitConfig,
        rerender: renderContent,
        notifyPacksChanged: function () { send("companion-packs-changed", {}); },
        notifyStageAction: function (action) { send("companion-stage-action", { action: action }); }
      });
      if (companionPanel) list.append(companionPanel);
      if (extension.afterRender) extension.afterRender({section: activeSection, list: list, entries: entries, bucketState: bucketState, bucketTitle: bucketTitle});
      content.append(list.children.length ? list : el("p", "info-note", tr("js.settings.no_results")));
      return;
    }
    if (activeSection === "prompts" && preferences) {
      preferenceEditor = preferences.render({
        selection, entries: () => sectionEntries("prompts"),
        matches: entry => matches(entry, specFor(entry.key)),
        unfiltered: filter === "all" && !search.value.trim(),
        targetFor: preferenceTarget, scopeName, scopeHref, navigate, submit: changePreferences, statusText,
        pending: entry => extension.hasDraft(entry, selection),
        drafts: () => extension.operations(selection),
        discard: entry => extension.discard(entry, selection)
      });
      if (preferenceEditor) list.append(preferenceEditor.card);
    }
    var rowGroup = null;
    function appendRow(node) {
      if (!rowGroup) {
        rowGroup = el("div", "sgroup");
        list.append(rowGroup);
      }
      rowGroup.append(node);
    }
    for (var spec of catalog) {
      if (spec.section !== (activeSection === "adapters" ? adapter : activeSection)) continue;
      for (var [key, rows] of byKey) {
        if (specFor(key) !== spec) continue;
        if (preferences && preferences.replaces(key, rows)) continue;
        if (spec.control === "tier") {
          if (rows.some(function (entry) { return matches(entry, spec); })) {
            rowGroup = null;
            list.append(renderTier(rows, spec));
          }
          continue;
        }
        var groupKey = activeSection === "compaction" ? "compaction" : key;
        var bucket = activeBucket(groupKey, rows);
        var selectedRow = rows.find(function (entry) { return entry.selectors.harness === bucket; });
        if (selectedRow && matches(selectedRow, spec)) {
          if (spec.control === "tier_routing" && window.WsSettingsTierRouting) {
            rowGroup = null;
            list.append(tierRoutingRow(selectedRow, spec));
          } else if (spec.control === "profile" || spec.control === "prompt") {
            rowGroup = null;
            list.append(renderRow(selectedRow, spec));
          } else {
            appendRow(renderRow(selectedRow, spec));
          }
        }
      }
    }
    if (extension.afterRender) extension.afterRender({section: activeSection, list, entries, bucketState, bucketTitle});
    content.append(list.children.length ? list : el("p", "info-note", tr("js.settings.no_results")));
  }

  async function load() {
    renderHeader();
    renderSections();
    renderContent();
    if (account) {
      account.refreshStatus();
      window.parent.document.addEventListener("ws-openai-consent", account.refreshStatus);
      window.parent.document.addEventListener("ws-account-changed", account.refreshStatus);
      window.addEventListener("pagehide", function () {
        window.parent.document.removeEventListener("ws-openai-consent", account.refreshStatus);
        window.parent.document.removeEventListener("ws-account-changed", account.refreshStatus);
        if (typeof account.destroy === "function") account.destroy();
        if (voice && typeof voice.destroy === "function") voice.destroy();
      });
      if (activeSection === "account" && requestedProvider) account.focus(requestedProvider);
      if (activeSection === "voice" && voice && requestedProvider) voice.focus(requestedProvider);
    }
    try {
      var loaded = await request("/api/config/snapshot" + (query.size ? "?" + query.toString() : ""));
      if (!Array.isArray(loaded.entries)) throw new Error();
      snapshot = loaded;
      selection = {scope: loaded.scope, target: loaded.target, context: loaded.context};
      scope = selection.scope;
    } catch (error) {
      failure = error.code || "config_error";
    }
    renderHeader();
    renderCounts();
    renderContent();
    if (extension.mount && snapshot) extension.mount({
      proposal: view.querySelector("[data-settings-proposal]"), selection, snapshot, rerender: renderContent
    });
  }

  search.addEventListener("input", renderContent);
  syncShell();
  load();
})();
