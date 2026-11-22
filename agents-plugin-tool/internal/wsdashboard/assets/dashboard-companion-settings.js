(function (C) {
  "use strict";
  // The companion modal edits the settings-catalog rows whose surface is
  // "companion-modal"; the shell page embeds them as window.wsCompanionModalRows.
  // Each row's key maps to its field in the live companion config payload.
  const configFields = {
    "dashboard.companion_pack": "pack",
    "dashboard.companion_preset": "preset",
    "dashboard.companion_agent_cooldown_seconds": "agentCooldownSeconds",
    "dashboard.companion_agent_hourly_cap": "agentHourlyCap",
    "dashboard.companion_sources": "sources"
  };
  const sourcesKey = "dashboard.companion_sources";
  const voicePreview = 240;

  function voiceSummary(voice) {
    const chars = Array.from(voice || "");
    return chars.length > voicePreview ? chars.slice(0, voicePreview).join("").trimEnd() + "…" : chars.join("");
  }

  // settingsModal is the floating, non-blocking companion settings dialog the
  // gear opens. Writes go to the global layer through /api/config/set and only
  // where the page may write (data-sysopen); the live config stream then
  // refreshes the shown values.
  function settingsModal(node, gear, options, hooks) {
    const doc = document, signal = options.signal;
    const dialog = doc.createElement("dialog");
    dialog.className = "ws-companion-settings";
    dialog.id = "ws-companion-settings";
    dialog.setAttribute("aria-label", wsT("js.companion.settings"));
    gear.setAttribute("aria-haspopup", "dialog");
    gear.setAttribute("aria-controls", dialog.id);
    gear.setAttribute("aria-expanded", "false");
    node.append(dialog);
    let controls = new Map(), packIds = null, packsRequest = null;

    function canWrite() { return doc.documentElement.hasAttribute("data-sysopen"); }
    // The Sources section renders dashboard.companion_sources, so it has no row of its own here.
    function rows() {
      return (hooks.rows() || []).filter(row => C.record(row) && Object.hasOwn(configFields, row.key) &&
        row.key !== sourcesKey);
    }
    function current(key) { return hooks.config()?.[configFields[key]]; }

    function el(tag, className, text) {
      const item = doc.createElement(tag);
      if (className) item.className = className;
      if (text !== undefined) item.textContent = text;
      return item;
    }

    function choices(key) {
      const value = current(key);
      const list = key === "dashboard.companion_pack" ? ["default", "auto", ...(packIds || [])] :
        hooks.presets().available;
      return [...new Set([...list, ...(typeof value === "string" && value ? [value] : [])])];
    }

    function field(row) {
      const label = el("label", "ws-companion-settings-row");
      label.title = wsT(row.description);
      label.append(el("span", "ws-companion-settings-label", wsT(row.label)));
      let input;
      if (row.key === "dashboard.companion_pack" || row.key === "dashboard.companion_preset") {
        input = el("select");
        fill(input, row.key);
      } else {
        input = el("input");
        input.type = row.control === "number" ? "number" : "text";
        if (row.control === "number") input.step = "1";
        input.value = String(current(row.key) ?? "");
      }
      input.disabled = !canWrite();
      input.addEventListener("change", () => write(row, input), options);
      controls.set(row.key, input);
      const box = el("span", "ws-companion-settings-control");
      box.append(input);
      if (row.unit) box.append(el("span", "ws-companion-settings-unit", wsT(row.unit)));
      label.append(box);
      return label;
    }

    function fill(select, key) {
      const value = current(key);
      select.replaceChildren(...choices(key).map(id => new Option(id, id, false, id === value)));
    }

    function personaBlock() {
      const persona = hooks.persona();
      const block = el("section", "ws-companion-settings-persona");
      block.append(el("h3", "", wsT("js.companion.persona")));
      if (!persona) {
        block.append(el("p", "ws-companion-settings-muted", wsT("js.companion.persona_none")));
        return block;
      }
      if (persona.name) block.append(el("p", "ws-companion-settings-name", persona.name));
      if (persona.voice) block.append(el("p", "ws-companion-settings-voice", voiceSummary(persona.voice)));
      return block;
    }

    function build() {
      controls = new Map();
      const head = el("div", "ws-companion-settings-head");
      const close = el("button", "ws-companion-settings-close", "×");
      close.type = "button";
      close.setAttribute("aria-label", wsT("js.companion.close"));
      close.title = close.getAttribute("aria-label");
      close.addEventListener("click", () => hide(true), options);
      head.append(el("h2", "", wsT("js.companion.settings")), close);
      const body = [head];
      let persona = false;
      for (const row of rows()) {
        body.push(field(row));
        if (row.key === "dashboard.companion_preset") { body.push(personaBlock()); persona = true; }
      }
      if (!persona) body.splice(1, 0, personaBlock());
      sources.redraw();
      body.push(sources.node);
      if (!canWrite()) body.push(el("p", "ws-companion-settings-muted", wsT("js.companion.settings_readonly")));
      const reset = el("button", "ws-companion-settings-action", wsT("js.companion.reset_size"));
      reset.type = "button";
      reset.addEventListener("click", hooks.resetSize, options);
      body.push(reset);
      dialog.replaceChildren(...body);
    }

    function value(row, input) {
      if (row.control !== "number") return input.value;
      const n = Number(input.value);
      return input.value !== "" && Number.isSafeInteger(n) ? n : undefined;
    }

    async function write(row, input) {
      const next = value(row, input);
      if (next === undefined || !canWrite()) { refresh(null); return; }
      if (row.key === "dashboard.companion_preset") hooks.applyPreset(next);
      try {
        const response = await fetch("/api/config/set", {
          method: "POST", credentials: "same-origin", signal,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ scope: "global", key: row.key, value: next })
        });
        if (!response.ok) throw new Error("Companion setting write failed");
      } catch (error) {
        if (signal.aborted) return;
        hooks.notice("settings_failed");
        if (row.key === "dashboard.companion_preset") hooks.applyPreset(current(row.key));
        refresh(null);
      }
    }

    // refresh shows the live config without disturbing the control in use;
    // a revert after a refused or failed write passes null to reset it too.
    function refresh(spare = doc.activeElement) {
      if (!dialog.open) return;
      sources.refresh();
      for (const [key, input] of controls) {
        if (input === spare) continue;
        if (input.tagName === "SELECT") fill(input, key);
        else input.value = String(current(key) ?? "");
      }
    }

    function applyCatalog(catalog) {
      if (!Array.isArray(catalog?.packs)) return;
      packIds = catalog.packs.map(pack => pack?.id).filter(C.timingId);
      const select = controls.get("dashboard.companion_pack");
      if (select && dialog.open) fill(select, "dashboard.companion_pack");
      sources.setCatalog(catalog);
    }

    function loadPacks() {
      if (packsRequest) return;
      packsRequest = fetch("/api/companion/packs", { credentials: "same-origin", signal })
        .then(response => response.ok ? response.json() : null)
        .then(applyCatalog)
        .catch(() => {})
        .finally(() => { packsRequest = null; });
    }

    // After an install, update, uninstall or import the modal takes the
    // returned catalog (or asks for it again) and the stage reloads the active
    // pack from the changed catalog.
    const sources = C.sourcesSection({
      doc, signal, options, canWrite, notice: hooks.notice,
      raw: () => current(sourcesKey),
      layout: () => place(),
      catalogChanged(catalog) {
        if (catalog) applyCatalog(catalog); else loadPacks();
        hooks.packsChanged();
      }
    });

    // place sets the dialog beside the character, clamped to the viewport:
    // to its right when it fits, else to its left.
    function place() {
      if (!dialog.open) return;
      const anchor = hooks.anchor(), vw = window.innerWidth, vh = window.innerHeight;
      const width = dialog.offsetWidth, height = dialog.offsetHeight, gap = 8;
      let left = anchor.right + gap;
      if (left + width > vw - 6) left = anchor.left - gap - width;
      left = C.clamp(left, 6, Math.max(6, vw - width - 6));
      const top = C.clamp(anchor.bottom - height, 6, Math.max(6, vh - height - 6));
      dialog.style.left = Math.round(left) + "px";
      dialog.style.top = Math.round(top) + "px";
    }

    function show() {
      build();
      dialog.show();
      gear.setAttribute("aria-expanded", "true");
      place();
      loadPacks();
      dialog.querySelector("select:not(:disabled), input:not(:disabled), button")?.focus({ preventScroll: true });
    }

    function hide(focusGear) {
      if (!dialog.open) return;
      const inside = dialog.contains(doc.activeElement);
      dialog.close();
      gear.setAttribute("aria-expanded", "false");
      if (focusGear || inside) gear.focus({ preventScroll: true });
    }

    gear.addEventListener("click", () => { if (dialog.open) hide(); else show(); }, options);
    doc.addEventListener("keydown", event => {
      if (event.key === "Escape" && dialog.open) { event.preventDefault(); hide(true); }
    }, options);
    window.addEventListener("resize", place, options);

    return {
      show, hide, refresh, place,
      get open() { return dialog.open; },
      // rebuild redraws the open dialog after the pack (persona, presets) changed.
      rebuild() { if (dialog.open) { build(); place(); } }
    };
  }

  Object.assign(C, { settingsModal, companionConfigFields: configFields });
})(window.WsCompanion);
