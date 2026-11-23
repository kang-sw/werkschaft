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
  if (window.parent === window) {
    location.replace(window.WsShell.pagePath(location));
    return;
  }

  if (modal) document.documentElement.setAttribute("data-settings-modal", "");

  var scopes = ["builtin", "synced", "global", "repo", "project", "session"];
  var sections = ["workflow", "agents", "compaction", "adapters", "companion", "profiles", "prompts"];
  var adapters = ["claude", "pi", "agy", "dashboard"];
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
    compaction: "js.settings.section.compaction",
    adapters: "js.settings.section.adapters",
    companion: "js.settings.section.companion",
    profiles: "js.settings.section.profiles",
    prompts: "js.settings.section.prompts",
    claude: "js.settings.section.claude",
    pi: "js.settings.section.pi",
    agy: "js.settings.section.agy",
    dashboard: "js.settings.section.dashboard"
  };
  var sectionNotes = {
    workflow: "js.settings.section_note.workflow",
    agents: "js.settings.section_note.agents",
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
    requires_native_subagent_off: "js.settings.disabled.requires_native_subagent_off"
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
  var snapshot = null, failure = "", activeSection = "workflow", adapter = "claude", filter = "all";
  var buckets = new Map(), warnings = new Map(), openLayers = new Set();
  var catalog = window.WsSettingsCatalog;
  var specs = new Map(catalog.map(function (spec) { return [spec.key, spec]; }));
  var families = catalog.filter(function (spec) { return spec.key.endsWith(".*"); });
  var extension = window.WsSettingsExtension || {};
  var preferences = window.WsSettingsPreferences;
  var content = view.querySelector("[data-settings-content]");
  var search = view.querySelector("[data-settings-search]");
  var count = view.querySelector("[data-settings-count]");

  var tr = window.wsT;

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
    display.className = "scope-target-pill" + (scopes.includes(scope) ? " scope-" + scope : "");
    display.querySelector("span").textContent = scopeName(scope);
    var path = display.querySelector("code");
    path.textContent = snapshot && snapshot.path || (scope === "session" ? selection.target : "");
    path.title = path.textContent;
    path.hidden = !path.textContent;
    var strip = view.querySelector("[data-settings-precedence]");
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
        return [value.backend, value.model, value.effort].filter(Boolean).join(" · ") || tr("js.settings.empty_value");
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
      var isWinner = !draft && layer.scope === entry.source_scope && (!layer.bucket || layer.bucket === entry.source_bucket);
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
          if (row.isConnected) replace(result.entry, hadFocus);
          refreshDependentRows(entry.key);
          renderCounts();
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
    var row = el("article", "srow krow" + rowClass(entry, canEdit));
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

  function tierRow(entry, spec) {
    var canEdit = editable(entry, spec), group = el("tbody", "tier-entry" + rowClass(entry, canEdit));
    group.dataset.configKey = entry.key;
    group.dataset.settingsTier = entry.selectors.tier;
    var row = el("tr", "tier-main-row"), secondary = el("tr", "tier-secondary");
    var cell = el("td", "tname", entry.selectors.tier);
    var fields = ["backend", "model", "effort"];
    var value = (scope === "builtin" ? entry.default : selected(entry) ?? entry.value) || {};
    var combos = {};
    var cb = window.WsSettingsCombobox;
    var inputs = fields.map(function (field) {
      var input = textInput(value[field], entry.selectors.tier + " · " + tr(tierLabels[field]), !canEdit);
      var combo = cb.attach(input, {
        getOptions: function () {
          if (field === "backend") return cb.backendOptions();
          var backendVal = inputs[0] ? inputs[0].value : value.backend;
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
    var status = el("span", "hint");
    status.setAttribute("role", "status");
    var submit = makeSubmit(entry, group, status, function (fresh) {
      group.replaceWith(tierRow(fresh, spec));
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
        submit("set", Object.fromEntries(fields.map(function (field, i) { return [field, inputs[i].value]; }))).then(syncSecondary, syncSecondary);
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
    var card = el("div", "sgroup tier-group" + (!entries.some(function (e) { return editable(e, spec); }) ? " disabled" : "")), active = activeBucket(spec.key, entries);
    var heading = append(el("div", "kmeta"), append(el("div", "klabel-line"), el("b", "klabel", window.wsT(spec.label)),
      el("code", "skey", spec.key), el("span", "tag bucket", tr("js.settings.bucket.tag"))), el("p", "kdesc", window.wsT(spec.description)));
    var visible = entries.filter(function (entry) { return entry.selectors.harness === active && matches(entry, spec); });
    var top = append(el("div", "tier-group-head"), heading, bucketPicker(spec.key, entries, "seg bucket-seg"));
    if (visible.length && !editable(visible[0], spec)) {
      heading.firstChild.append(el("span", "tag lock", tr(reasons[rowReason(visible[0], spec)] || reasons.scope_unsupported)));
    }
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
    table.append(colgroup, append(el("thead"), append(el("tr"), ...headers)),
      ...visible.map(function (entry) { return tierRow(entry, spec); }));
    return append(card, top, append(el("div", "tier-scroll"), table));
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

  function group(key) {
    return snapshot.entries.filter(function (entry) { return entry.key === key; });
  }

  function harnessChoices(entries) {
    var order = ["*", "default", "claude", "codex", "pi", "agy", "werk"];
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

  // Rows with a surface other than the panel (the companion modal, or the
  // deprecated aliases no UI renders) stay in the catalog for inspection but
  // are not rendered here.
  function sectionEntries(name) {
    return snapshot ? snapshot.entries.filter(function (entry) {
      var spec = specFor(entry.key);
      return spec && !spec.surface && (name === "adapters" ? adapters.includes(spec.section) : spec.section === name);
    }) : [];
  }

  function renderCounts() {
    var n = snapshot ? snapshot.entries.filter(function (entry) { return selected(entry) !== undefined; }).length : 0;
    count.textContent = snapshot ? tr("js.settings.override_count", n) : "";
    for (var node of view.querySelectorAll("[data-settings-section]")) {
      var total = sectionEntries(node.dataset.settingsSection).filter(function (entry) { return selected(entry) !== undefined; }).length;
      node.querySelector(".cnt").textContent = total || "";
      node.querySelector(".dot").classList.toggle("none", !total);
    }
  }

  function renderSections() {
    var nav = view.querySelector("[data-settings-sections]");
    nav.replaceChildren(...sections.map(function (name) {
      var node = button("snav-btn", "", function () {
        activeSection = name;
        for (var item of nav.children) item.setAttribute("aria-current", String(item === node));
        renderContent();
        content.scrollTop = 0;
      });
      node.dataset.settingsSection = name;
      node.setAttribute("aria-current", String(name === activeSection));
      return append(node, el("span", "dot none"), el("span", "", sectionName(name)), el("span", "cnt"));
    }));
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

  function renderContent() {
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
    if (activeSection === "prompts" && preferences) {
      var slots = preferences.render({
        selection, entries: () => sectionEntries("prompts"),
        matches: entry => matches(entry, specFor(entry.key)),
        unfiltered: filter === "all" && !search.value.trim(),
        targetFor: preferenceTarget, scopeName, scopeHref, navigate, submit: changePreferences, statusText,
        pending: entry => extension.hasDraft(entry, selection),
        drafts: () => extension.operations(selection),
        discard: entry => extension.discard(entry, selection)
      });
      if (slots) list.append(slots);
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
        if (preferences && preferences.replaces(key)) continue;
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
