(function () {
  "use strict";

  const prefix = "preference.";
  const defaultKey = prefix + "default";
  const legacyKey = "prompt.UserPreferenceSection";
  const scopes = ["builtin", "global", "repo", "project", "session"];
  const layersHighFirst = scopes.slice().reverse();
  const nameHints = {
    name_required: "js.settings.preference.name_rule",
    name_invalid: "js.settings.preference.name_invalid",
    name_duplicate: "js.settings.preference.name_duplicate",
    "": "js.settings.preference.name_available"
  };
  const tr = (...args) => window.wsT(...args);

  function replaces(key) {
    return key.startsWith(prefix) || key === legacyKey;
  }

  function nameError(name, keys) {
    if (!name) return "name_required";
    // Match ValidPreferenceKey's ASCII grammar, including numeric first letters.
    if (!/^[a-z0-9]/.test(name) || /[^a-z0-9_-]/.test(name)) return "name_invalid";
    if (keys.has(prefix + name)) return "name_duplicate";
    return "";
  }

  function slotOperation(key, action, value) {
    return action === "reset" || action === "restore"
      ? {key, selectors: {}, action: "reset"}
      : {key, selectors: {}, action: "set", value: action === "hide" ? "" : value};
  }

  function migrationOperations(value, selectors, copied) {
    const reset = {key: legacyKey, selectors, action: "reset"};
    return copied ? [reset] : [slotOperation(defaultKey, "save", value), reset];
  }

  function slotModel(entries, selection, operations, pending) {
    const {scope} = selection;
    const common = entries.find(entry => entry.key === legacyKey && entry.selectors.harness === "*");
    const legacyLayers = (common?.layers || []).filter(layer => layer.scope !== "builtin" && layer.present && layer.value)
      .map(layer => ({
        ...layer, selectors: layer.bucket === "all" ? {harness: "*"} : {},
        own: layer.selected !== undefined || (layer.bucket !== "all" && !layer.inherited)
      }))
      .filter(layer => pending({key: legacyKey, selectors: layer.selectors})?.action !== "reset" || layer.scope !== scope);
    // The resolver checks the common bucket across scopes before the older bare key.
    const legacy = scope === "builtin" ? undefined :
      legacyLayers.find(layer => layer.bucket === "all") || legacyLayers[0];
    const legacyHere = legacyLayers.find(layer => layer.scope === scope && layer.own);
    const fallback = legacy ? {...legacy, legacy: true} :
      common?.seed ? {scope: "builtin", value: common.seed, present: true} : undefined;
    const entriesByKey = new Map(entries.filter(entry => entry.key.startsWith(prefix)).map(entry => [entry.key, entry]));
    if (fallback && !entriesByKey.has(defaultKey)) entriesByKey.set(defaultKey, {key: defaultKey});
    for (const operation of operations) {
      if (operation.key.startsWith(prefix) && !entriesByKey.has(operation.key)) {
        entriesByKey.set(operation.key, {key: operation.key});
      }
    }

    const rows = [];
    const keys = new Set();
    let move;
    for (const [key, entry] of entriesByKey) {
      const draft = pending(entry);
      const base = key === defaultKey ? fallback : undefined;
      const layers = layersHighFirst.map(name => {
        if (name === "builtin") {
          const seed = key === defaultKey && common?.seed;
          return seed ? {scope: name, value: seed, present: true} : {scope: name};
        }
        const stored = entry.layers?.find(layer => layer.scope === name) || {scope: name};
        if (draft && name === scope) {
          return draft.action === "set" ? {...stored, present: true, value: draft.value, selected: draft.value} :
            {...stored, present: false, value: undefined, selected: undefined};
        }
        return stored;
      });
      const stored = layers.filter(layer => layer.present && layer.scope !== "builtin");
      const own = layers.find(layer => layer.scope === scope)?.selected;
      if (stored.length) keys.add(key);
      if (key === defaultKey && legacyHere && (own === undefined || own === legacyHere.value)) {
        move = {value: legacyHere.value, selectors: legacyHere.selectors, copied: own !== undefined};
      }
      const winner = (scope === "builtin" ? undefined : stored[0]) || base;
      const current = own !== undefined ? {value: own, scope} :
        key === defaultKey && legacyHere ? {...legacyHere, legacy: true} : winner;
      if (!current) continue;
      const below = (name, skipEmpty) => stored.find(layer => scopes.indexOf(layer.scope) < scopes.indexOf(name) &&
        (!skipEmpty || layer.value !== "")) || base;
      const resetTo = below(scope);
      const masked = current.value === "" ? below(current.scope, true)?.value : undefined;
      rows.push({key, entry, layers, own, draft, current, winner, masked, resetTo});
    }
    rows.sort((a, b) => Number(a.winner.value === "") - Number(b.winner.value === "") ||
      Number(b.key === defaultKey) - Number(a.key === defaultKey) || (a.key < b.key ? -1 : 1));
    return {rows, keys, move};
  }

  function node(tag, className, content) {
    const element = document.createElement(tag);
    if (className) element.className = className;
    if (content !== undefined) element.textContent = content;
    return element;
  }

  function append(parent, ...children) {
    parent.append(...children);
    return parent;
  }

  function button(label, className, handler) {
    const element = node("button", "btn " + className, label);
    element.type = "button";
    if (handler) element.addEventListener("click", handler);
    return element;
  }

  function render({selection, entries, matches, unfiltered, targetFor, scopeName, scopeHref, navigate, submit,
    pending, drafts, discard, statusText}) {
    const {scope} = selection;
    const model = () => slotModel(entries(), selection, drafts(), pending);
    if (!unfiltered && !entries().some(entry => replaces(entry.key) && matches(entry))) return null;

    const available = scopes.slice(1).map(name => [name, targetFor(name)]);
    const canEdit = available.some(([name, target]) => name === scope && target);
    const card = node("section", "settings-preferences");
    card.dataset.settingsPromptCard = prefix + "*";
    card.dataset.preferenceScope = scope;
    const heading = node("header", "sec-head");
    heading.append(append(node("h3"), tr("js.settings.preference.title") + " ", node("code", "skey", prefix + "<slot>")),
      node("p", "", tr("js.settings.preference.description")));
    const notice = node("div", "preferences-legacy");
    notice.setAttribute("role", "note");
    const legend = node("div", "preferences-legend");
    legend.append(
      append(node("div"), node("strong", "preferences-hide", tr("js.settings.preference.hide")), node("span", "", tr("js.settings.preference.hide_help"))),
      append(node("div"), node("strong", "", tr("js.settings.preference.reset")), node("span", "", tr("js.settings.preference.reset_help")))
    );
    const message = node("div", "hint preferences-message");
    message.setAttribute("role", "status");
    const rows = node("div", "preferences-rows");
    const editor = append(node("div", "preferences-list"), rows);
    const previewRows = node("div", "preferences-preview-rows");
    const preview = node("aside", "preferences-preview");
    preview.setAttribute("aria-label", tr("js.settings.preference.preview"));
    preview.append(
      append(node("header"), node("strong", "", tr("js.settings.preference.preview")), node("span", "preferences-tag", scopeName(scope)),
        node("span", "preferences-live", tr("js.settings.preference.live"))),
      append(node("div", "preferences-preview-body"), node("code", "preferences-preview-heading", "### User preferences"), previewRows)
    );
    card.append(heading, notice, legend, message, append(node("div", "preferences-grid"), editor, preview));

    // Typing changes one preview paragraph. Explicit operations sync rows by key.
    const rowNodes = new Map();
    const previewNodes = new Map();
    let busy = false;
    let refreshAdd = () => {};
    let moveState = "";

    async function change(operations, target) {
      if (busy) return false;
      busy = true;
      const locked = operations.map(operation => rowNodes.get(operation.key)?.input)
        .filter(input => input && !input.readOnly);
      for (const input of locked) input.readOnly = true;
      card.setAttribute("aria-busy", "true");
      message.className = "hint preferences-message";
      message.textContent = window.wsT("js.settings.saving");
      try {
        await submit(operations, target);
        message.textContent = target.scope === "repo" ? window.wsT("js.settings.proposal.queued") : "";
        if (target.scope === "repo" && scope !== "repo") {
          message.append(" · ", button(scopeName("repo"), "", () => navigate(scopeHref("repo"))));
        }
        return true;
      } catch (error) {
        message.classList.add("err");
        message.textContent = statusText(error.code);
        return false;
      } finally {
        busy = false;
        for (const input of locked) input.readOnly = false;
        card.removeAttribute("aria-busy");
        sync();
      }
    }

    function layerChain(row) {
      const chain = node("div", "layer-chain preferences-chain");
      row.layers.slice().reverse().forEach((layer, index) => {
        if (index) {
          const arrow = node("span", "larrow", "→");
          arrow.setAttribute("aria-hidden", "true");
          chain.append(arrow);
        }
        const href = layer.scope !== scope && scopeHref(layer.scope);
        const chip = button("", "", href ? () => navigate(href) : undefined);
        chip.className = "lchip";
        chip.disabled = !href;
        chip.classList.toggle("is-empty", !layer.present);
        chip.classList.toggle("is-winner", row.winner.scope === layer.scope);
        chip.classList.toggle("is-active-scope", scope === layer.scope);
        const value = layer.present ? (layer.value === "" ? '""' : layer.value) :
          layer.status === "context_missing" ? window.wsT("js.settings.context_missing_short") : "";
        chip.title = value || scopeName(layer.scope);
        chip.append(node("span", "lscope", scopeName(layer.scope)));
        if (value) chip.append(node("span", "lval", value));
        chain.append(chip);
      });
      return chain;
    }

    function updatePreview(row, draftText) {
      const item = previewNodes.get(row.key);
      const edited = draftText !== undefined && draftText.trim() !== "" &&
        scopes.indexOf(scope) >= scopes.indexOf(row.winner.scope);
      const point = edited ? {scope, value: draftText} : row.winner;
      const hidden = point.value === "";
      item.root.classList.toggle("is-hidden-slot", hidden);
      item.root.classList.toggle("is-legacy-slot", !!point.legacy);
      item.source.textContent = " · " + (hidden ? tr("js.settings.preference.hidden_from_manual", scopeName(point.scope)) :
        edited ? tr("js.settings.preference.unsaved") : point.legacy ? tr("js.settings.preference.legacy_fallback") : scopeName(point.scope));
      item.text.hidden = hidden;
      item.text.textContent = point.value;
    }

    function renderRow(row) {
      const {key, current, winner, own, draft} = row;
      const name = key.slice(prefix.length);
      const hidden = current.value === "";
      const inherited = own === undefined;
      const item = node("article", "preferences-slot");
      item.dataset.preferenceSlot = name;
      item.classList.toggle("is-hidden-slot", hidden);
      item.classList.toggle("is-legacy-slot", !!current.legacy);
      item.classList.toggle("is-local-slot", !inherited && !hidden);
      const order = node("span", "preferences-order");
      const label = append(node("code", "preferences-name"), node("span", "", prefix), name);
      const head = append(node("header", "preferences-slot-head"), order, label);
      if (key === defaultKey) head.append(node("span", "preferences-tag", tr("js.settings.preference.default_badge")));
      if (hidden || current.legacy) {
        head.append(node("span", "preferences-tag", tr(hidden ? "js.settings.preference.hidden" : "js.settings.preference.legacy_fallback")));
      } else if (scope === "repo" && !inherited && !row.resetTo) {
        head.append(node("span", "preferences-tag", tr("js.settings.preference.repo_only")));
      }
      const chain = layerChain(row);
      chain.hidden = !hidden;
      const sourceLabel = tr(current.legacy ? "js.settings.preference.legacy_source" : inherited ? "js.settings.preference.source" : "js.settings.preference.override",
        scopeName(current.scope));
      const source = button(sourceLabel, "", () => {
        chain.hidden = !chain.hidden;
        source.setAttribute("aria-expanded", String(!chain.hidden));
      });
      source.className = "origin-pill" + (draft ? " draft" : !inherited ? " here" : "");
      source.setAttribute("aria-expanded", String(!chain.hidden));
      source.append(node("span", "chev", "▾"));
      head.append(source);
      item.append(head);
      const footer = node("footer", "preferences-actions");
      const hint = node("span", "hint");
      footer.append(hint);
      let input;
      if (hidden) {
        if (row.masked) item.append(node("del", "preferences-masked", row.masked));
        hint.textContent = tr("js.settings.preference.hidden_from_manual", scopeName(current.scope));
      } else {
        input = node("textarea", "preferences-text" + (inherited ? " is-inherited" : ""));
        input.rows = scope === "global" || current.value.includes("\n") ? 3 : 2;
        input.value = current.value;
        input.readOnly = !canEdit || !!current.legacy;
        input.setAttribute("aria-label", key);
        item.append(input);
        const originalHint = draft ? window.wsT("js.settings.proposal.queued") :
          current.legacy ? tr("js.settings.preference.legacy_readonly") :
          inherited && canEdit ? tr("js.settings.preference.inherited", scopeName(current.scope)) :
          winner.scope !== current.scope ? window.wsT("js.settings.shadow", scopeName(winner.scope)) : "";
        hint.textContent = originalHint;
        if (!input.readOnly) {
          const save = button(scope === "repo" ? tr("js.settings.preference.queue") : window.wsT("js.settings.save"), "pri", () => {
            change([slotOperation(key, "save", input.value)], selection);
          });
          save.dataset.preferenceAction = "save";
          save.hidden = true;
          footer.append(save);
          input.addEventListener("input", () => {
            const dirty = input.value !== current.value;
            save.hidden = !dirty;
            save.disabled = !input.value.trim();
            hint.textContent = dirty ? tr(input.value.trim() ? "js.settings.preference.unsaved" : "js.settings.preference.text_required") : originalHint;
            updatePreview(row, dirty ? input.value : undefined);
          });
        }
      }
      function rowAction(action, label, disabled) {
        const control = button(label, "preferences-" + action, () => change([slotOperation(key, action)], selection));
        control.dataset.preferenceAction = action;
        control.disabled = !canEdit || disabled;
        footer.append(control);
      }
      if (hidden) {
        rowAction("restore", tr("js.settings.preference.restore"), own !== "");
      } else {
        rowAction("hide", tr("js.settings.preference.hide"), false);
        rowAction("reset", tr(own === undefined ? "js.settings.preference.no_value" : row.resetTo ? "js.settings.preference.reset" : "js.settings.preference.delete"), own === undefined);
      }
      if (draft) {
        const undo = button(window.wsT("js.settings.proposal.discard"), "", () => {
          discard(row.entry);
          sync();
        });
        undo.dataset.preferenceAction = "discard";
        footer.append(undo);
      }
      item.append(chain, footer);
      return {item, order, input};
    }

    function sync() {
      const state = model();
      const showDefault = !unfiltered && entries().some(entry => entry.key === legacyKey && matches(entry));
      const shown = state.rows.filter(row => unfiltered || row.entry.layers && matches(row.entry) ||
        row.key === defaultKey && showDefault);
      const keep = new Set(shown.map(row => row.key));
      const present = new Set(state.rows.map(row => row.key));
      for (const [key, row] of rowNodes) {
        if (keep.has(key)) continue;
        row.item.remove();
        rowNodes.delete(key);
      }
      for (const [key, item] of previewNodes) {
        if (present.has(key)) continue;
        item.root.remove();
        previewNodes.delete(key);
      }
      let nextRow = rows.firstElementChild;
      let nextPreview = previewRows.firstElementChild;
      let number = 0;
      for (const row of state.rows) {
        const {key} = row;
        const position = row.winner.value === "" ? "–" : String(++number);
        const existing = rowNodes.get(key);
        const signature = JSON.stringify(row);
        const update = previewNodes.get(key)?.signature !== signature;
        if (keep.has(key)) {
          const rowNode = !existing || update ? renderRow(row) : existing;
          if (existing && rowNode !== existing) {
            const focused = existing.item.contains(document.activeElement);
            const action = document.activeElement?.dataset.preferenceAction;
            if (nextRow === existing.item) nextRow = rowNode.item;
            existing.item.replaceWith(rowNode.item);
            if (focused) {
              const control = action && rowNode.item.querySelector('[data-preference-action="' + action + '"]:not(:disabled):not([hidden])');
              (control || rowNode.input || rowNode.item.querySelector(".origin-pill")).focus({preventScroll: true});
            }
          }
          rowNodes.set(key, rowNode);
          if (rowNode.order.textContent !== position) rowNode.order.textContent = position;
          if (rowNode.item !== nextRow) rows.insertBefore(rowNode.item, nextRow);
          nextRow = rowNode.item.nextElementSibling;
        }
        if (!previewNodes.has(key)) {
          const source = node("span");
          const text = node("p");
          const root = append(node("div", "preferences-paragraph"),
            append(node("div", "preferences-preview-label"), node("code", "", key.slice(prefix.length)), source), text);
          previewNodes.set(key, {root, source, text, signature});
        }
        if (update) {
          previewNodes.get(key).signature = signature;
          updatePreview(row);
        }
        const item = previewNodes.get(key).root;
        if (item !== nextPreview) previewRows.insertBefore(item, nextPreview);
        nextPreview = item.nextElementSibling;
      }

      const move = state.move;
      const nextMoveState = move ? JSON.stringify(move) : "";
      if (moveState !== nextMoveState) {
        moveState = nextMoveState;
        notice.replaceChildren();
        if (move) {
          const detail = append(node("div"), node("strong", "", tr("js.settings.preference.legacy_title")),
            node("p", "", tr(move.copied ? "js.settings.preference.legacy_retry" : "js.settings.preference.legacy_help", legacyKey)));
          notice.append(detail, button(tr(move.copied ? "js.settings.preference.finish_move" : "js.settings.preference.move"), "", () =>
            change(migrationOperations(move.value, move.selectors, move.copied), selection)));
        }
      }
      notice.hidden = !move;
      refreshAdd(state.keys);
    }

    if (canEdit) {
      const form = node("form", "preferences-add");
      const name = node("input");
      name.type = "text";
      name.spellcheck = false;
      name.autocomplete = "off";
      name.setAttribute("aria-describedby", "preference-name-check");
      const validation = node("span", "hint");
      validation.id = "preference-name-check";
      validation.setAttribute("aria-live", "polite");
      const text = node("textarea", "preferences-text");
      text.rows = 2;
      const targets = node("div", "seg preferences-targets");
      targets.setAttribute("role", "group");
      targets.setAttribute("aria-label", tr("js.settings.preference.target"));
      let target = scope;
      let keys;
      for (const [name, selection] of available) {
        const choice = button(scopeName(name), "", () => {
          target = name;
          syncTarget();
        });
        choice.className = "";
        choice.disabled = !selection;
        if (!selection) choice.title = window.wsT("js.settings.target_required");
        choice.dataset.preferenceTarget = name;
        targets.append(choice);
      }
      const save = button(tr("js.settings.preference.add"), "pri");
      save.type = "submit";
      const help = node("span", "hint");
      function syncTarget() {
        for (const choice of targets.children) {
          choice.setAttribute("aria-pressed", String(choice.dataset.preferenceTarget === target));
        }
        save.textContent = tr(target === "repo" ? "js.settings.preference.queue" : "js.settings.preference.add");
        save.classList.toggle("preferences-queue", target === "repo");
        help.textContent = tr(target === "repo" ? "js.settings.preference.repo_add_help" : "js.settings.preference.text_required");
      }
      refreshAdd = used => {
        keys = used;
        const error = nameError(name.value, keys);
        const hint = tr(nameHints[error]);
        if (validation.textContent !== hint) {
          validation.textContent = hint;
          validation.classList.toggle("err", !!name.value && !!error);
          name.setAttribute("aria-invalid", String(!!name.value && !!error));
        }
        save.disabled = !!error || !text.value.trim();
      };
      name.addEventListener("input", () => refreshAdd(keys));
      text.addEventListener("input", () => refreshAdd(keys));
      form.addEventListener("submit", async event => {
        event.preventDefault();
        if (save.disabled || busy) return;
        const key = prefix + name.value;
        const value = text.value;
        if (await change([slotOperation(key, "save", value)], targetFor(target)) &&
            name.value === key.slice(prefix.length) && text.value === value) {
          form.reset();
          refreshAdd(keys);
        }
      });
      form.append(node("h4", "", tr("js.settings.preference.add")), append(node("div", "preferences-add-fields"),
        append(node("label"), tr("js.settings.preference.name"), append(node("span", "preferences-namebox"), node("code", "", prefix), name), validation),
        append(node("div", "preferences-field"), node("span", "", tr("js.settings.preference.target")), targets)),
        append(node("label"), tr("js.settings.preference.text"), text), append(node("div", "preferences-actions"), save, help));
      syncTarget();
      editor.append(form);
    }

    sync();
    return card;
  }

  window.WsSettingsPreferences = {replaces, render, nameError, slotOperation, migrationOperations, slotModel};
})();
