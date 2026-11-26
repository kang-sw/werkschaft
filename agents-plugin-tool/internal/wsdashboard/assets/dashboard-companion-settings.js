(function (C) {
  "use strict";
  // The companion modal edits the settings-catalog rows whose surface is
  // "companion-modal"; the shell page embeds them as window.wsCompanionModalRows.
  // Each row's key maps to its field in the live companion config payload.
  const configFields = {
    "dashboard.companion_pack": "pack",
    "dashboard.companion_preset": "preset",
    "dashboard.companion_outfit": "outfit",
    "dashboard.companion_hair": "hair",
    "dashboard.companion_eyes": "eyes",
    "dashboard.companion_attachments": "attachments",
    "dashboard.companion_palette": "palette",
    "dashboard.companion_agent_cooldown_seconds": "agentCooldownSeconds",
    "dashboard.companion_agent_hourly_cap": "agentHourlyCap",
    "dashboard.companion_sources": "sources"
  };
  const sourcesKey = "dashboard.companion_sources";
  const voicePreview = 240;
  const pairMax = 16, hexColor = /^#[0-9a-f]{6}$/;
  const keyAttachments = "dashboard.companion_attachments", keyPalette = "dashboard.companion_palette";
  // The wardrobe rows that pick one pack ID, and the wardrobeOptions list each reads.
  const idLists = { "dashboard.companion_outfit": "outfits", "dashboard.companion_hair": "hairs", "dashboard.companion_eyes": "eyesets" };

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
    // controls maps a row key to the function that refreshes that row's
    // inputs from the live config, sparing the one in use (a row may own
    // several inputs, so refresh never touches the inputs directly).
    let controls = new Map(), packIds = null, packRenderers = new Map(), packsRequest = null;

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

    function offered() {
      const options = hooks.wardrobe?.();
      return C.record(options) ? options : {};
    }

    function listOf(items) { return Array.isArray(items) ? items.filter(id => typeof id === "string") : []; }

    function choices(key) {
      const value = current(key);
      const list = key === "dashboard.companion_pack" ? ["default", "auto", ...(packIds || [])] :
        Object.hasOwn(idLists, key) ? ["", ...listOf(offered()[idLists[key]])] : hooks.presets().available;
      return [...new Set([...list, ...(typeof value === "string" && value ? [value] : [])])];
    }

    function option(id, selected, key) {
      let label = id === "" ? wsT("js.companion.follow_preset") : id;
      if (key === "dashboard.companion_pack" && packRenderers.get(id) === "vrm") {
        label = id + " (" + wsT("js.companion.sources.badge_vrm") + ")";
      }
      return new Option(label, id, false, selected);
    }

    function fill(select, key) {
      const value = current(key);
      select.replaceChildren(...choices(key).map(id => option(id, id === value || (id === "" && typeof value !== "string"), key)));
    }

    function labelled(tag, row) {
      const item = el(tag, "ws-companion-settings-row");
      item.title = wsT(row.description);
      item.append(el("span", "ws-companion-settings-label", wsT(row.label)));
      return item;
    }

    function field(row) {
      if (row.key === keyAttachments) return slotsRow(row);
      if (row.key === keyPalette) return paletteRow(row);
      const label = labelled("label", row);
      let input, vrmBadge = null;
      if (row.key === "dashboard.companion_pack" || row.key === "dashboard.companion_preset" || Object.hasOwn(idLists, row.key)) {
        input = el("select");
        fill(input, row.key);
        if (row.key === "dashboard.companion_pack") {
          vrmBadge = el("span", "ws-companion-sources-badge ws-companion-vrm-badge", wsT("js.companion.sources.badge_vrm"));
          vrmBadge.hidden = packRenderers.get(current(row.key)) !== "vrm";
        }
        controls.set(row.key, spare => {
          if (input !== spare) fill(input, row.key);
          if (vrmBadge) vrmBadge.hidden = packRenderers.get(current(row.key)) !== "vrm";
        });
      } else {
        input = el("input");
        input.type = row.control === "number" ? "number" : "text";
        if (row.control === "number") input.step = "1";
        input.value = String(current(row.key) ?? "");
        controls.set(row.key, spare => { if (input !== spare) input.value = String(current(row.key) ?? ""); });
      }
      input.disabled = !canWrite();
      input.addEventListener("change", () => write(row.key, value(row, input)), options);
      const box = el("span", "ws-companion-settings-control");
      box.append(input);
      if (vrmBadge) box.append(vrmBadge);
      if (row.unit) box.append(el("span", "ws-companion-settings-unit", wsT(row.unit)));
      label.append(box);
      return label;
    }

    // multiRow builds a row of one control per name (an attachment slot or a
    // palette channel). names() lists them; make(name) returns [wrapper,
    // input]; show(name, input) sets one input from the live config. A
    // refresh that changes the names redraws the row unless the focus is in it.
    function multiRow(row, names, make, show, extra) {
      const item = labelled("div", row), list = el("span", "ws-companion-settings-multi");
      let shown = null, inputs = new Map();
      function draw() {
        shown = names();
        inputs = new Map();
        list.replaceChildren(...shown.map(name => {
          const [wrapper, input] = make(name);
          input.disabled = !canWrite();
          inputs.set(name, input);
          return wrapper;
        }));
      }
      function sync(spare) {
        const next = names();
        if (shown && (next.length !== shown.length || next.some((name, i) => name !== shown[i])) && !list.contains(spare)) draw();
        for (const [name, input] of inputs) if (input !== spare) show(name, input);
        item.hidden = !extra && shown.length === 0;
      }
      draw();
      item.append(list, ...(extra ? [extra] : []));
      controls.set(row.key, sync);
      sync(null);
      return item;
    }

    // subControl wraps an input with its slot or channel name; the name is
    // pack data and only ever set as text.
    function subControl(name, input) {
      const wrapper = el("label", "ws-companion-settings-sub");
      wrapper.append(el("span", "ws-companion-settings-name-tag", name), input);
      return [wrapper, input];
    }

    // pairNames lists the pack's names first, then the config-only ones, so a
    // stored pair the pack lacks stays visible and can be cleared.
    function pairNames(packNames, stored) {
      const own = [...new Set(listOf(packNames))];
      return [...own, ...Object.keys(stored).filter(name => !own.includes(name)).sort()];
    }

    function slotsRow(row) {
      const slots = () => (C.record(offered().slots) ? offered().slots : {});
      const names = () => pairNames(Object.keys(slots()).sort(), C.attachmentPairs(current(row.key)));
      return multiRow(row, names, slot => {
        const select = el("select");
        select.addEventListener("change", () => write(row.key, composePairs(C.attachmentPairs(current(row.key)), slot, select.value)), options);
        return subControl(slot, select);
      }, (slot, select) => {
        const stored = C.attachmentPairs(current(row.key))[slot] || "";
        const ids = Object.hasOwn(slots(), slot) ? listOf(slots()[slot]) : [];
        select.replaceChildren(...[...new Set(["", "none", ...ids, ...(stored ? [stored] : [])])].map(id =>
          new Option(id === "" ? wsT("js.companion.follow_preset") : id === "none" ? wsT("js.companion.attachment_none") : id, id, false, id === stored)));
      });
    }

    function paletteRow(row) {
      const reset = el("button", "ws-companion-settings-reset", wsT("js.companion.reset_colors"));
      reset.type = "button";
      reset.disabled = !canWrite();
      reset.addEventListener("click", () => write(row.key, ""), options);
      const names = () => pairNames(offered().channels, C.palettePairs(current(row.key)));
      return multiRow(row, names, channel => {
        const input = el("input");
        input.type = "color";
        input.addEventListener("change", () => {
          const color = String(input.value).toLowerCase();
          write(row.key, hexColor.test(color) ? composePairs(C.palettePairs(current(row.key)), channel, color) : undefined);
        }, options);
        return subControl(channel, input);
      }, (channel, input) => {
        const effective = hooks.picks?.()?.palette?.[channel], stored = C.palettePairs(current(row.key))[channel];
        input.value = [effective, stored].map(color => String(color).toLowerCase()).find(color => hexColor.test(color)) || "#000000";
      }, reset);
    }

    // composePairs returns the flat config string for pairs with key set to
    // next, or removed when next is "". Pairs are sorted by key. Past the
    // 16 pairs the config value allows, it drops the last keys in sorted
    // order, never the changed one. Values are lowercased (hex colors; IDs
    // already are). No pairs left is "" (follow the preset).
    function composePairs(pairs, key, next) {
      const map = { ...pairs };
      if (next === "") delete map[key]; else map[key] = next;
      const keys = Object.keys(map).sort();
      for (let i = keys.length - 1; i >= 0 && keys.length > pairMax; i--) if (keys[i] !== key) keys.splice(i, 1);
      return keys.map(name => name + "=" + map[name].toLowerCase()).join(" ");
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

    async function write(key, next) {
      if (next === undefined || !canWrite()) { refresh(null); return; }
      if (key === "dashboard.companion_preset") hooks.applyPreset(next);
      try {
        const response = await fetch("/api/config/set", {
          method: "POST", credentials: "same-origin", signal,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ scope: "global", key, value: next })
        });
        if (!response.ok) throw new Error("Companion setting write failed");
      } catch (error) {
        if (signal.aborted) return;
        hooks.notice("settings_failed");
        if (key === "dashboard.companion_preset") hooks.applyPreset(current(key));
        refresh(null);
      }
    }

    // refresh shows the live config without disturbing the control in use;
    // a revert after a refused or failed write passes null to reset it too.
    function refresh(spare = doc.activeElement) {
      if (!dialog.open) return;
      sources.refresh();
      for (const sync of controls.values()) sync(spare);
    }

    function applyCatalog(catalog) {
      if (!Array.isArray(catalog?.packs)) return;
      packIds = catalog.packs.map(pack => pack?.id).filter(C.timingId);
      packRenderers = new Map();
      for (const pack of catalog.packs) {
        if (C.timingId(pack?.id) && typeof pack.renderer === "string") packRenderers.set(pack.id, pack.renderer);
      }
      if (dialog.open) controls.get("dashboard.companion_pack")?.(null);
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
      verdicts: () => hooks.config()?.sourceEntries,
      selectPack(id) {
        if (typeof id === "string" && id) return write("dashboard.companion_pack", id);
      },
      catalogChanged(catalog) {
        if (catalog) applyCatalog(catalog); else loadPacks();
        hooks.packsChanged();
      }
    });

    dialog.addEventListener("dragover", event => {
      if (canWrite()) event.preventDefault();
    }, options);
    dialog.addEventListener("drop", event => {
      const file = event.dataTransfer?.files?.[0];
      if (!file || !canWrite() || !/\.vrm$/i.test(String(file.name || ""))) return;
      event.preventDefault();
      sources.node.open = true;
      sources.importVrmFile(file);
    }, options);

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

    // Whatever resizes the open dialog (the Sources section opening, a listing
    // or import preview, a wardrobe row gaining inputs) re-places it, at most
    // once per frame. place() only moves the dialog, so it never feeds back.
    let placeFrame = 0;
    const resized = new ResizeObserver(() => {
      if (!placeFrame) placeFrame = requestAnimationFrame(() => { placeFrame = 0; place(); });
    });
    function unobserve() {
      resized.disconnect();
      cancelAnimationFrame(placeFrame);
      placeFrame = 0;
    }
    signal.addEventListener("abort", unobserve, { once: true });

    function show() {
      build();
      dialog.show();
      gear.setAttribute("aria-expanded", "true");
      place();
      resized.observe(dialog);
      loadPacks();
      dialog.querySelector("select:not(:disabled), input:not(:disabled), button")?.focus({ preventScroll: true });
    }

    function hide(focusGear) {
      if (!dialog.open) return;
      const inside = dialog.contains(doc.activeElement);
      unobserve();
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
