(function (C) {
  "use strict";

  function quickMenu(node, hit, menu, options, { returnHome, resetSize, mute, applyPreset, presets, reloadPack, notice, openSettings, onOpen, extraTriggers }) {
    const doc = document, signal = options.signal;
    const triggers = [hit].concat(Array.isArray(extraTriggers) ? extraTriggers.filter(Boolean) : []);
    let width = 0, height = 0, activeTrigger = hit;
    function canWrite() { return doc.documentElement.hasAttribute("data-sysopen"); }
    function measure() { width = menu.offsetWidth; height = menu.offsetHeight; }
    function close() { menu.hidden = true; hit.setAttribute("aria-expanded", "false"); }
    function openMenu(event) {
      event.preventDefault();
      activeTrigger = event.currentTarget || hit;
      rebuild();
      menu.hidden = false;
      measure();
      onOpen?.();
      hit.setAttribute("aria-expanded", "true");
      menu.querySelector("button")?.focus({ preventScroll: true });
    }
    for (const trig of triggers) {
      trig.addEventListener("contextmenu", openMenu, options);
      trig.addEventListener("keydown", event => {
        if (event.key === "ContextMenu" || event.key === "F10" && event.shiftKey) openMenu(event);
      }, options);
    }
    doc.addEventListener("pointerdown", event => { if (!node.contains(event.target)) close(); }, options);
    doc.addEventListener("keydown", event => {
      if (event.key === "Escape" && !menu.hidden) { close(); (activeTrigger || hit).focus({ preventScroll: true }); }
    }, options);

    async function reload() {
      close();
      if (!canWrite()) return;
      try {
        await reloadPack();
      } catch (error) {
        if (!signal.aborted) notice(error.status === 403 || error.status === 404 ? "reload_denied" : "reload_failed");
      }
    }

    async function changePreset(id) {
      applyPreset(id);
      if (!canWrite()) return;
      try {
        const response = await fetch("/api/config/set", {
          method: "POST", credentials: "same-origin", signal,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ scope: "global", key: "dashboard.companion_preset", value: id })
        });
        if (!response.ok) throw new Error("Companion preset write failed");
      } catch (error) {
        if (!signal.aborted) notice("preset_failed");
      }
    }

    function rebuild() {
      menu.replaceChildren();
      function action(key, callback) {
        const button = doc.createElement("button");
        button.type = "button";
        button.textContent = wsT(key);
        button.addEventListener("click", callback);
        menu.append(button);
      }
      action("js.companion.return_home", returnHome);
      action("js.companion.reset_size", resetSize);
      action("js.companion.hide", mute);
      if (openSettings) action("js.companion.settings", () => { close(); openSettings(hit); });
      if (canWrite()) action("js.companion.reload", reload);
      const { available, preset } = presets();
      if (available.length > 1) {
        const select = doc.createElement("select"), label = doc.createElement("label");
        label.textContent = wsT("js.companion.preset");
        for (const id of available) select.add(new Option(id, id, false, id === preset));
        select.addEventListener("change", () => changePreset(select.value));
        label.append(select);
        menu.append(label);
      }
      if (!menu.hidden) measure();
    }

    return { close, rebuild, measure,
      place(x, bottom, vw, vh) {
        if (menu.hidden) return;
        const left = C.clamp(x, 6, Math.max(6, vw - width - 6));
        const top = C.clamp(bottom - height, 6, Math.max(6, vh - height - 6));
        menu.style.transform = "translate3d(" + left + "px," + top + "px,0)";
      }
    };
  }

  C.quickMenu = quickMenu;
})(window.WsCompanion);
